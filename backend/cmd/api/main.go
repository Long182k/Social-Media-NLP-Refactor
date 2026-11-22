package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/admin"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/chat"
	"github.com/social-media-nlp/refactor/backend/internal/community"
	"github.com/social-media-nlp/refactor/backend/internal/config"
	graphapi "github.com/social-media-nlp/refactor/backend/internal/graphql"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/mail"
	"github.com/social-media-nlp/refactor/backend/internal/media"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/worker"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
	"github.com/social-media-nlp/refactor/backend/internal/posts"
	"github.com/social-media-nlp/refactor/backend/internal/profiles"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
	"github.com/social-media-nlp/refactor/backend/internal/ws"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "probe" {
		if !probeReady() {
			os.Exit(1)
		}
		return
	}
	if err := config.LoadDotEnvIfPresent(".env", "../.env"); err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		logger.Error("failed to parse .env file", slog.String("error", err.Error()))
		os.Exit(1)
	}

	cfg, err := config.LoadFromEnv(os.LookupEnv)
	if err != nil {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		logger.Error("configuration validation failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pgCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("invalid DATABASE_URL", slog.String("error", err.Error()))
		os.Exit(1)
	}
	pgPool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		logger.Error("failed to initialize postgres pool", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pgPool.Close()

	redisOpts, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Error("invalid VALKEY_URL", slog.String("error", err.Error()))
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	defer func() { _ = rdb.Close() }()

	appCtx, stopApp := context.WithCancel(context.Background())
	defer stopApp()
	router, hub, err := newApplication(appCtx, cfg, pgPool, rdb, logger)
	if err != nil {
		logger.Error("application initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.ListenHost, cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      50 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", slog.String("addr", srv.Addr), slog.String("env", cfg.AppEnv))
		if serveErr := srv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		}
		close(errCh)
	}()

	select {
	case serveErr := <-errCh:
		if serveErr != nil {
			logger.Error("server error", slog.String("error", serveErr.Error()))
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		hub.StopAccepting()
		httpDrain, cancelHTTPDrain := context.WithTimeout(context.Background(), 50*time.Second)
		httpErr := srv.Shutdown(httpDrain)
		cancelHTTPDrain()
		if httpErr != nil {
			logger.Error("graceful shutdown failed", slog.String("error", httpErr.Error()))
			_ = srv.Close()
		}
		wsDrain, cancelWSDrain := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelWSDrain()
		if err := hub.Shutdown(wsDrain); err != nil {
			logger.Error("websocket shutdown failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		if httpErr != nil {
			os.Exit(1)
		}
		logger.Info("server stopped cleanly")
	}
}

func newApplication(ctx context.Context, cfg *config.Config, pgPool *pgxpool.Pool, rdb *redis.Client, logger *slog.Logger) (http.Handler, *ws.Hub, error) {
	metrics := httpserver.NewMetrics()
	var mailer mail.MailProvider
	switch cfg.MailProvider {
	case "", "mailpit", "smtp":
		mailer = mail.NewSMTPMailer(cfg.SMTPHost, cfg.SMTPPort, cfg.MailFrom)
	case "resend":
		var err error
		mailer, err = mail.NewResendMailer(cfg.ResendAPIKey, cfg.ResendOwnerEmail, nil)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, errors.New("invalid MAIL_PROVIDER")
	}

	authSvc := auth.NewService(pgPool, rdb, mailer, auth.ServiceConfig{
		JWTSecret:          cfg.JWTSecret,
		RefreshJWTSecret:   cfg.RefreshJWTSecret,
		JWTTTL:             cfg.JWTTTL,
		RefreshJWTTTL:      cfg.RefreshJWTTTL,
		CookieSecure:       cfg.CookieSecure,
		LoginLimit:         cfg.LoginLimit,
		LoginIPLimit:       cfg.LoginIPLimit,
		LoginLimitDuration: cfg.LoginLimitDuration,
		TrustProxyHeaders:  cfg.TrustProxyHeaders,
		PublicBaseURL:      cfg.PublicBaseURL,
		ResetTokenTTL:      cfg.ResetTokenTTL,
		Logger:             logger,
		LoginMetrics:       metrics.AuthLoginAttemptsTotal,
	})
	authHandler := auth.NewHandler(authSvc)

	publisher := realtime.Publisher{Client: rdb, Logger: logger}
	var refinement *worker.Pool
	if cfg.EnableCloudNLP {
		client, err := configuredNLP(cfg, rdb)
		if err != nil {
			logger.Warn("cloud sentiment disabled", slog.String("reason", err.Error()))
		} else {
			refinement = worker.Start(ctx, client, sqlc.New(pgPool), publisher, metrics.NLPDroppedRefinements)
		}
	}
	postsSvc := posts.NewService(pgPool, metrics.NLPLocalDuration, refinement)
	postsHandler := posts.NewHandler(postsSvc, authSvc, publisher)
	chatHandler := chat.NewHandler(pgPool, authSvc, publisher)
	communityHandler := community.NewHandler(pgPool, authSvc, publisher)
	notificationHandler := notifications.NewHandler(pgPool, authSvc, publisher)
	adminHandler := admin.NewHandler(pgPool, authSvc, publisher)

	var mediaHandler *media.Handler
	var mediaSvc *media.Service
	if cfg.S3.Endpoint != "" {
		var err error
		mediaSvc, err = media.NewService(pgPool, cfg.S3)
		if err != nil {
			return nil, nil, err
		}
		mediaHandler = media.NewHandler(mediaSvc, authSvc, logger)
		go mediaSvc.RunCleanup(ctx, logger)
	}
	profileHandler := profiles.NewHandler(pgPool, authSvc, mediaSvc, publisher)
	wsOrigins := append([]string{cfg.AppOrigin, cfg.PublicBaseURL}, cfg.CORSAllowedOrigins...)
	hub := ws.NewHub(pgPool, rdb, authSvc, logger, wsOrigins)
	metrics.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_connections", Help: "PostgreSQL connections open in this replica.",
		}, func() float64 { return float64(pgPool.Stat().TotalConns()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "db_pool_acquired_connections", Help: "PostgreSQL connections currently checked out.",
		}, func() float64 { return float64(pgPool.Stat().AcquiredConns()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "websocket_connections", Help: "WebSocket connections open in this replica.",
		}, hub.ConnectionCount),
	)
	go hub.Run(ctx)

	router := httpserver.NewRouter(httpserver.Options{
		Logger:             logger,
		Metrics:            metrics,
		PostgresPinger:     httpserver.PingFunc(pgPool.Ping),
		ValkeyPinger:       httpserver.PingFunc(func(c context.Context) error { return rdb.Ping(c).Err() }),
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		StaticDir:          cfg.StaticDir,
		WebSocket:          hub,
		GraphQL:            graphapi.NewHandler(pgPool, authSvc, logger),
		RegisterAPIRoutes: func(api chi.Router) {
			authHandler.RegisterRoutes(api)
			postsHandler.RegisterRoutes(api)
			chatHandler.RegisterRoutes(api)
			communityHandler.RegisterRoutes(api)
			profileHandler.RegisterRoutes(api)
			notificationHandler.RegisterRoutes(api)
			adminHandler.RegisterRoutes(api)
			hub.RegisterAPIRoutes(api)
			if mediaHandler != nil {
				mediaHandler.RegisterRoutes(api)
			}
		},
	})

	return router, hub, nil
}

func configuredNLP(cfg *config.Config, cache *redis.Client) (*worker.CloudClient, error) {
	evidence, err := worker.ReadEvidence(cfg.NLPCloudEvidence)
	if err != nil {
		return nil, err
	}
	if _, err := worker.ReadEvaluation(cfg.NLPCloudEvaluation, evidence.AccountID); err != nil {
		return nil, err
	}
	return worker.NewCloudClient(evidence, cfg.NLPCloudToken, cache)
}
