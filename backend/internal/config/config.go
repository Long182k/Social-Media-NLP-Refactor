package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

const (
	legacyAccessSecretPlaceholder  = "super_secret_jwt_access_token_key_2026"
	legacyRefreshSecretPlaceholder = "super_secret_jwt_refresh_token_key_2026"
)

type Config struct {
	Port               string
	ListenHost         string
	AppEnv             string
	AppOrigin          string
	PublicBaseURL      string
	CORSAllowedOrigins []string
	StaticDir          string

	DatabaseURL string
	ValkeyURL   string

	JWTSecret        string
	RefreshJWTSecret string
	JWTTTL           time.Duration
	RefreshJWTTTL    time.Duration
	CookieSecure     bool

	LoginLimit         int
	LoginIPLimit       int
	LoginLimitDuration time.Duration
	TrustProxyHeaders  bool

	MailProvider     string
	ResendAPIKey     string
	ResendOwnerEmail string
	SMTPHost         string
	SMTPPort         int
	MailFrom         string
	ResetTokenTTL    time.Duration

	S3                 S3Config
	EnableCloudNLP     bool
	NLPCloudEvidence   string
	NLPCloudEvaluation string
	NLPCloudToken      string

	LogLevel slog.Level
}

type S3Config struct {
	Endpoint        string
	PresignEndpoint string
	Region          string
	Bucket          string
	AccessKey       string
	SecretKey       string
	PublicBaseURL   string
	UsePathStyle    bool
}

func LoadFromEnv(lookup func(string) (string, bool)) (*Config, error) {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				return trimmed
			}
		}
		return fallback
	}

	jwtSecret := get("JWT_SECRET", "")
	if jwtSecret == "" {
		return nil, errors.New("JWT_SECRET is required and must not be empty or whitespace")
	}
	if isLegacyPlaceholderSecret(jwtSecret) {
		return nil, errors.New("JWT_SECRET must not use the legacy hardcoded placeholder secret")
	}

	refreshSecret := get("REFRESH_JWT_SECRET", "")
	if refreshSecret == "" {
		return nil, errors.New("REFRESH_JWT_SECRET is required and must not be empty or whitespace")
	}
	if isLegacyPlaceholderSecret(refreshSecret) {
		return nil, errors.New("REFRESH_JWT_SECRET must not use the legacy hardcoded placeholder secret")
	}

	databaseURL := get("DATABASE_URL", "")
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required and must not be empty")
	}

	valkeyURL := get("VALKEY_URL", "")
	if valkeyURL == "" {
		valkeyURL = get("REDIS_URL", "redis://127.0.0.1:6379/0")
	}

	jwtTTL, err := parseDuration(get("JWT_TTL", "15m"), "JWT_TTL")
	if err != nil {
		return nil, err
	}
	refreshTTL, err := parseDuration(get("REFRESH_JWT_TTL", "168h"), "REFRESH_JWT_TTL")
	if err != nil {
		return nil, err
	}
	loginLimitDuration, err := parseDuration(get("LOGIN_LIMIT_DURATION", "900s"), "LOGIN_LIMIT_DURATION")
	if err != nil {
		return nil, err
	}
	resetTokenTTL, err := parseDuration(get("RESET_TOKEN_TTL", "30m"), "RESET_TOKEN_TTL")
	if err != nil {
		return nil, err
	}

	cookieSecure, err := strconv.ParseBool(get("COOKIE_SECURE", "false"))
	if err != nil {
		return nil, fmt.Errorf("invalid COOKIE_SECURE: %w", err)
	}
	appEnv := strings.ToLower(get("APP_ENV", "development"))
	if appEnv == "production" && !cookieSecure {
		return nil, errors.New("production requires COOKIE_SECURE=true")
	}

	loginLimit, err := strconv.Atoi(get("LOGIN_LIMIT", "5"))
	if err != nil || loginLimit <= 0 {
		return nil, errors.New("LOGIN_LIMIT must be a positive integer")
	}

	loginIPLimit, err := strconv.Atoi(get("LOGIN_IP_LIMIT", "20"))
	if err != nil || loginIPLimit <= 0 {
		return nil, errors.New("LOGIN_IP_LIMIT must be a positive integer")
	}

	trustProxyHeaders, err := strconv.ParseBool(get("TRUST_PROXY_HEADERS", "false"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRUST_PROXY_HEADERS: %w", err)
	}

	smtpPort, err := strconv.Atoi(get("SMTP_PORT", "1025"))
	if err != nil || smtpPort <= 0 || smtpPort > 65535 {
		return nil, errors.New("SMTP_PORT must be a valid TCP port")
	}

	var corsOrigins []string
	if rawCors := get("CORS_ALLOWED_ORIGINS", ""); rawCors != "" {
		for _, part := range strings.Split(rawCors, ",") {
			if origin := strings.TrimSpace(part); origin != "" {
				corsOrigins = append(corsOrigins, origin)
			}
		}
	}

	staticDir := get("STATIC_DIR", "")
	if staticDir == "" {
		staticDir = get("SERVE_SPA_DIR", "")
	}

	logLevel := slog.LevelInfo
	switch strings.ToLower(get("LOG_LEVEL", "info")) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	publicBaseURL := strings.TrimRight(get("PUBLIC_BASE_URL", get("APP_ORIGIN", "http://localhost:5173")), "/")
	usePathStyle, err := strconv.ParseBool(get("S3_USE_PATH_STYLE", "true"))
	if err != nil {
		return nil, fmt.Errorf("invalid S3_USE_PATH_STYLE: %w", err)
	}
	enableCloudNLP, err := strconv.ParseBool(get("ENABLE_CLOUD_NLP", "false"))
	if err != nil {
		return nil, fmt.Errorf("invalid ENABLE_CLOUD_NLP: %w", err)
	}

	return &Config{
		Port:               get("PORT", "8080"),
		ListenHost:         get("LISTEN_HOST", ""),
		AppEnv:             appEnv,
		AppOrigin:          get("APP_ORIGIN", "http://localhost:5173"),
		PublicBaseURL:      publicBaseURL,
		CORSAllowedOrigins: corsOrigins,
		StaticDir:          staticDir,
		DatabaseURL:        databaseURL,
		ValkeyURL:          valkeyURL,
		JWTSecret:          jwtSecret,
		RefreshJWTSecret:   refreshSecret,
		JWTTTL:             jwtTTL,
		RefreshJWTTTL:      refreshTTL,
		CookieSecure:       cookieSecure,
		LoginLimit:         loginLimit,
		LoginIPLimit:       loginIPLimit,
		LoginLimitDuration: loginLimitDuration,
		TrustProxyHeaders:  trustProxyHeaders,
		MailProvider:       strings.ToLower(get("MAIL_PROVIDER", "mailpit")),
		ResendAPIKey:       get("RESEND_API_KEY", ""),
		ResendOwnerEmail:   get("RESEND_OWNER_EMAIL", ""),
		SMTPHost:           get("SMTP_HOST", "127.0.0.1"),
		SMTPPort:           smtpPort,
		MailFrom:           get("MAIL_FROM", "no-reply@social-media.local"),
		ResetTokenTTL:      resetTokenTTL,
		EnableCloudNLP:     enableCloudNLP,
		NLPCloudEvidence:   get("NLP_CLOUD_EVIDENCE", ""),
		NLPCloudEvaluation: get("NLP_CLOUD_EVALUATION", ""),
		NLPCloudToken:      get("NLP_CLOUD_TOKEN", ""),
		S3: S3Config{
			Endpoint:        get("S3_ENDPOINT", ""),
			PresignEndpoint: get("S3_PRESIGN_ENDPOINT", ""),
			Region:          get("S3_REGION", "us-east-1"),
			Bucket:          get("S3_BUCKET", ""),
			AccessKey:       get("S3_ACCESS_KEY", ""),
			SecretKey:       get("S3_SECRET_KEY", ""),
			PublicBaseURL:   get("S3_PUBLIC_BASE_URL", ""),
			UsePathStyle:    usePathStyle,
		},
		LogLevel: logLevel,
	}, nil
}

func isLegacyPlaceholderSecret(secret string) bool {
	return secret == legacyAccessSecretPlaceholder || secret == legacyRefreshSecretPlaceholder
}

func parseDuration(raw, name string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (got %q)", name, raw)
	}
	return d, nil
}
