package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

type Options struct {
	Logger             *slog.Logger
	Metrics            *Metrics
	PostgresPinger     Pinger
	ValkeyPinger       Pinger
	CORSAllowedOrigins []string
	StaticDir          string
	WebSocket          http.Handler
	GraphQL            http.Handler
	RegisterAPIRoutes  func(r chi.Router)
}

func NewRouter(opts Options) chi.Router {
	r := chi.NewRouter()

	r.Use(RequestIDMiddleware)
	r.Use(SlogMetricsMiddleware(opts.Logger, opts.Metrics))
	r.Use(middleware.Recoverer)

	if len(opts.CORSAllowedOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   opts.CORSAllowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-Id"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}

	r.Get("/healthz", HealthzHandler())
	r.Get("/readyz", ReadyzHandler(opts.PostgresPinger, opts.ValkeyPinger))
	if opts.Metrics != nil {
		r.Handle("/metrics", opts.Metrics.Handler())
	}
	if opts.WebSocket != nil {
		r.Get("/ws", opts.WebSocket.ServeHTTP)
	}
	if opts.GraphQL != nil {
		r.Post("/graphql", opts.GraphQL.ServeHTTP)
	}

	r.Route("/api", func(api chi.Router) {
		if opts.RegisterAPIRoutes != nil {
			opts.RegisterAPIRoutes(api)
		}
		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
		})
	})

	spa := NewSPAHandler(opts.StaticDir)
	r.NotFound(spa.ServeHTTP)

	return r
}
