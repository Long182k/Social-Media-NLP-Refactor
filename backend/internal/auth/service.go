package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/mail"
)

var (
	ErrValidation            = errors.New("validation error")
	ErrConflict              = errors.New("conflict")
	ErrUnauthorized          = errors.New("unauthorized")
	ErrForbidden             = errors.New("forbidden")
	ErrRateLimited           = errors.New("rate limited")
	ErrServiceUnavailable    = errors.New("service unavailable")
	ErrInvalidResetToken     = errors.New("invalid or expired reset token")
	ErrDemoRecipientFiltered = errors.New("demo recipient restricted")
)

type RateLimitError struct {
	RetryAfterSeconds int
}

func (e *RateLimitError) Error() string {
	return "too many failed login attempts"
}

func (e *RateLimitError) Is(target error) bool {
	return target == ErrRateLimited
}

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrValidation
}

type ServiceConfig struct {
	JWTSecret          string
	RefreshJWTSecret   string
	JWTTTL             time.Duration
	RefreshJWTTTL      time.Duration
	CookieSecure       bool
	LoginLimit         int
	LoginIPLimit       int
	LoginLimitDuration time.Duration
	TrustProxyHeaders  bool
	PublicBaseURL      string
	ResetTokenTTL      time.Duration
	ArgonParams        *argon2id.Params
	Logger             *slog.Logger
	LoginMetrics       *prometheus.CounterVec
}

type Service struct {
	pool   *pgxpool.Pool
	q      *sqlc.Queries
	valkey *valkeyStore
	mailer mail.MailProvider
	cfg    ServiceConfig
	argon  *argon2id.Params
}

func NewService(pool *pgxpool.Pool, rdb *redis.Client, mailer mail.MailProvider, cfg ServiceConfig) *Service {
	argon := cfg.ArgonParams
	if argon == nil {
		argon = &argon2id.Params{
			Memory:      19 * 1024, // 19 MiB (OWASP baseline)
			Iterations:  2,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		}
	}
	if cfg.LoginLimit <= 0 {
		cfg.LoginLimit = 5
	}
	if cfg.LoginIPLimit <= 0 {
		cfg.LoginIPLimit = 20
	}
	if cfg.LoginLimitDuration <= 0 {
		cfg.LoginLimitDuration = 15 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.JWTTTL <= 0 {
		cfg.JWTTTL = 15 * time.Minute
	}
	if cfg.RefreshJWTTTL <= 0 {
		cfg.RefreshJWTTTL = 168 * time.Hour
	}
	if cfg.ResetTokenTTL <= 0 {
		cfg.ResetTokenTTL = 30 * time.Minute
	}

	return &Service{
		pool:   pool,
		q:      sqlc.New(pool),
		valkey: newValkeyStore(rdb),
		mailer: mailer,
		cfg:    cfg,
		argon:  argon,
	}
}

func (s *Service) CookieSecure() bool {
	return s.cfg.CookieSecure
}

func (s *Service) RefreshCookieMaxAge() int {
	return int(s.cfg.RefreshJWTTTL.Seconds())
}

type accessJWTClaims struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Role     string `json:"role"`
	Type     string `json:"typ"`
	jwt.RegisteredClaims
}

type refreshJWTClaims struct {
	FamilyID string `json:"fam"`
	Type     string `json:"typ"`
	Epoch    int64  `json:"ep"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

func (s *Service) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, string, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.TrimSpace(req.UserName)
	}
	if len(username) < 3 || len(username) > 50 || strings.ContainsAny(username, " \t\r\n@") {
		return nil, "", &ValidationError{Message: "username must be 3-50 characters without whitespace or '@'"}
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !isValidEmail(email) {
		return nil, "", &ValidationError{Message: "valid email is required"}
	}

	if len(req.Password) < 6 {
		return nil, "", &ValidationError{Message: "password must be at least 6 characters"}
	}

	nickName := strings.TrimSpace(req.NickName)
	if nickName == "" {
		nickName = username
	}

	hash, err := argon2id.CreateHash(req.Password, s.argon)
	if err != nil {
		return nil, "", fmt.Errorf("hash password: %w", err)
	}

	row, err := s.q.CreateUser(ctx, sqlc.CreateUserParams{
		UserName:     username,
		NickName:     nickName,
		Email:        email,
		PasswordHash: hash,
		Role:         sqlc.UserRoleUSER,
		AvatarUrl:    req.AvatarURL,
		CoverPageUrl: req.CoverPageURL,
		IsActive:     true,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, "", ErrConflict
		}
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	pub := publicUserFromCreateRow(row)
	pair, err := s.issueSessionTokens(ctx, pub, row.SessionEpoch)
	if err != nil {
		return nil, "", err
	}

	return &AuthResponse{
		AccessToken: pair.AccessToken,
		User:        pub,
	}, pair.RefreshToken, nil
}

func (s *Service) Login(ctx context.Context, req LoginRequest, clientIP string) (*AuthResponse, string, error) {
	identifier := firstNonEmpty(req.Identifier, req.Username, req.Email)
	if identifier == "" || req.Password == "" {
		return nil, "", &ValidationError{Message: "identifier and password are required"}
	}

	normID := strings.ToLower(identifier)
	normIP := normalizeClientIP(clientIP)
	if normIP == "" {
		normIP = "unknown"
	}
	ipKey := loginAttemptIPKey(normIP)

	user, err := s.q.GetUserAuthByIdentifier(ctx, normID)
	userExists := true
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			userExists = false
		} else {
			return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
	}

	acctKey := loginAttemptIdentKey(normID)
	if userExists {
		acctKey = loginAttemptAccountKey(user.ID)
	}

	locked, retryAfter, err := s.valkey.AcquireLoginAttempt(
		ctx,
		acctKey,
		ipKey,
		s.cfg.LoginLimit,
		s.cfg.LoginIPLimit,
		s.cfg.LoginLimitDuration,
	)
	if err != nil {
		s.incLoginMetric("valkey_error")
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if locked {
		s.incLoginMetric("rate_limited")
		return nil, "", &RateLimitError{RetryAfterSeconds: retryAfter}
	}

	if !userExists {
		s.incLoginMetric("invalid_credentials")
		return nil, "", ErrUnauthorized
	}

	match, err := argon2id.ComparePasswordAndHash(req.Password, user.PasswordHash)
	if err != nil || !match {
		s.incLoginMetric("invalid_credentials")
		return nil, "", ErrUnauthorized
	}

	if !user.IsActive {
		_ = s.valkey.RefundLoginAttempt(ctx, acctKey, ipKey)
		s.incLoginMetric("inactive")
		return nil, "", ErrForbidden
	}

	_ = s.valkey.ReleaseLoginSuccess(ctx, acctKey, ipKey)

	updated, err := s.q.UpdateLastLoginAtForEpoch(ctx, sqlc.UpdateLastLoginAtForEpochParams{
		ID:           user.ID,
		SessionEpoch: user.SessionEpoch,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, checkErr := s.q.GetPublicUserByID(ctx, user.ID)
			if checkErr == nil && !current.IsActive {
				s.incLoginMetric("inactive")
				return nil, "", ErrForbidden
			}
			return nil, "", ErrUnauthorized
		}
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	pub := publicUserFromUpdateLoginForEpochRow(updated)
	pair, err := s.issueSessionTokens(ctx, pub, updated.SessionEpoch)
	if err != nil {
		return nil, "", err
	}

	s.incLoginMetric("success")
	return &AuthResponse{
		AccessToken: pair.AccessToken,
		User:        pub,
	}, pair.RefreshToken, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (*AuthResponse, string, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, "", ErrUnauthorized
	}

	userID, famID, jti, epoch, err := s.parseRefreshToken(refreshToken)
	if err != nil || epoch <= 0 {
		return nil, "", ErrUnauthorized
	}

	userRow, err := s.q.GetPublicUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_ = s.valkey.RevokeSession(ctx, userID, famID)
			return nil, "", ErrUnauthorized
		}
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	if !userRow.IsActive {
		_ = s.valkey.RevokeAllUserSessions(ctx, userID)
		return nil, "", ErrForbidden
	}

	if epoch != userRow.SessionEpoch {
		_ = s.valkey.RevokeSession(ctx, userID, famID)
		return nil, "", ErrUnauthorized
	}

	outcome, err := s.valkey.PeekSession(ctx, userID, famID, jti, epoch)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if outcome != rotateOK {
		return nil, "", ErrUnauthorized
	}

	newJTI := uuid.NewString()
	newRefreshToken, err := s.signRefreshToken(userRow.ID, famID, newJTI, userRow.SessionEpoch)
	if err != nil {
		return nil, "", err
	}

	updated, err := s.q.UpdateLastLoginAtForEpoch(ctx, sqlc.UpdateLastLoginAtForEpochParams{
		ID:           userID,
		SessionEpoch: epoch,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, checkErr := s.q.GetPublicUserByID(ctx, userID)
			if checkErr == nil && !current.IsActive {
				_ = s.valkey.RevokeAllUserSessions(ctx, userID)
				return nil, "", ErrForbidden
			}
			_ = s.valkey.RevokeSession(ctx, userID, famID)
			return nil, "", ErrUnauthorized
		}
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	pub := publicUserFromUpdateLoginForEpochRow(updated)
	accessToken, err := s.signAccessToken(pub, uuid.NewString())
	if err != nil {
		return nil, "", err
	}

	outcome, err = s.valkey.RotateSession(ctx, userID, famID, jti, newJTI, epoch, s.cfg.RefreshJWTTTL)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if outcome != rotateOK {
		return nil, "", ErrUnauthorized
	}

	return &AuthResponse{
		AccessToken: accessToken,
		User:        pub,
	}, newRefreshToken, nil
}

func (s *Service) Signout(ctx context.Context, refreshToken string) {
	if strings.TrimSpace(refreshToken) == "" {
		return
	}
	userID, famID, _, _, err := s.parseRefreshTokenAllowExpired(refreshToken)
	if err != nil {
		return
	}
	_ = s.valkey.RevokeSession(ctx, userID, famID)
}

func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	if !isValidEmail(email) {
		return &ValidationError{Message: "valid email is required"}
	}
	if policy, ok := s.mailer.(mail.ResetRecipientPolicy); ok {
		if err := policy.CheckResetRecipient(email); err != nil {
			if errors.Is(err, mail.ErrDemoRecipientRestricted) {
				return ErrDemoRecipientFiltered
			}
			return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
	}

	userRow, err := s.q.GetPublicUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if !userRow.IsActive {
		return ErrForbidden
	}

	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return fmt.Errorf("generate reset token: %w", err)
	}
	rawToken := base64.RawURLEncoding.EncodeToString(rawBytes)
	tokenHash := sha256Hex(rawToken)
	expiresAt := time.Now().UTC().Add(s.cfg.ResetTokenTTL)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	if err := qtx.InvalidateUnusedResetTokensForUser(ctx, userRow.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if _, err := qtx.CreatePasswordResetToken(ctx, sqlc.CreatePasswordResetTokenParams{
		UserID:    userRow.ID,
		TokenHash: tokenHash,
		ExpiresAt: pgTimestamptz(expiresAt),
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	resetURL := fmt.Sprintf("%s/reset-password?token=%s", strings.TrimRight(s.cfg.PublicBaseURL, "/"), rawToken)
	if err := s.mailer.SendPasswordReset(ctx, userRow.Email, resetURL, s.cfg.ResetTokenTTL); err != nil {
		if errors.Is(err, mail.ErrDemoRecipientRestricted) {
			return ErrDemoRecipientFiltered
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, req ResetPasswordRequest) error {
	rawToken := strings.TrimSpace(req.Token)
	if rawToken == "" {
		return &ValidationError{Message: "reset token is required"}
	}
	if len(req.NewPassword) < 6 {
		return &ValidationError{Message: "newPassword must be at least 6 characters"}
	}

	newHash, err := argon2id.CreateHash(req.NewPassword, s.argon)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tokenHash := sha256Hex(rawToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	consumed, err := qtx.ConsumePasswordResetToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidResetToken
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	userRow, err := qtx.GetPublicUserByID(ctx, consumed.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidResetToken
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if !userRow.IsActive {
		return ErrForbidden
	}

	newEpoch, err := qtx.UpdateUserPasswordAndBumpEpoch(ctx, sqlc.UpdateUserPasswordAndBumpEpochParams{
		ID:           consumed.UserID,
		PasswordHash: newHash,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}

	if err := s.valkey.RevokeUserSessionsBeforeEpoch(ctx, consumed.UserID, newEpoch); err != nil {
		s.cfg.Logger.Warn("best-effort valkey session revocation failed after password reset",
			slog.String("user_id", consumed.UserID.String()),
			slog.String("error", err.Error()),
		)
	}
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, req ChangePasswordRequest) error {
	if req.OldPassword == "" || len(req.NewPassword) < 6 {
		return &ValidationError{Message: "oldPassword and newPassword (min 6 chars) are required"}
	}

	userAuth, err := s.q.GetUserAuthByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if !userAuth.IsActive {
		return ErrForbidden
	}

	match, err := argon2id.ComparePasswordAndHash(req.OldPassword, userAuth.PasswordHash)
	if err != nil || !match {
		return ErrUnauthorized
	}

	newHash, err := argon2id.CreateHash(req.NewPassword, s.argon)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	if err := s.q.UpdateUserPasswordHash(ctx, sqlc.UpdateUserPasswordHashParams{
		ID:           userID,
		PasswordHash: newHash,
	}); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	return nil
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (*PublicUser, error) {
	row, err := s.q.GetPublicUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if !row.IsActive {
		return nil, ErrForbidden
	}
	pub := publicUserFromGetByIDRow(row)
	return &pub, nil
}

func (s *Service) ParseAccessToken(tokenStr string) (*AuthClaims, error) {
	claims := &accessJWTClaims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid || claims.Type != "access" {
		return nil, ErrUnauthorized
	}
	uid, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return &AuthClaims{
		UserID:   uid,
		UserName: claims.UserName,
		Role:     claims.Role,
		JTI:      claims.ID,
	}, nil
}

func (s *Service) issueSessionTokens(ctx context.Context, user PublicUser, epoch int64) (TokenPair, error) {
	famID := uuid.NewString()
	jti := uuid.NewString()
	if err := s.valkey.CreateSession(ctx, user.ID, famID, jti, epoch, s.cfg.RefreshJWTTTL); err != nil {
		return TokenPair{}, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	access, err := s.signAccessToken(user, uuid.NewString())
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := s.signRefreshToken(user.ID, famID, jti, epoch)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

func (s *Service) signAccessToken(user PublicUser, jti string) (string, error) {
	now := time.Now().UTC()
	claims := accessJWTClaims{
		UserID:   user.ID.String(),
		UserName: user.UserName,
		Role:     user.Role,
		Type:     "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.JWTTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(s.cfg.JWTSecret))
}

func (s *Service) signRefreshToken(userID uuid.UUID, famID, jti string, epoch int64) (string, error) {
	now := time.Now().UTC()
	claims := refreshJWTClaims{
		FamilyID: famID,
		Type:     "refresh",
		Epoch:    epoch,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.RefreshJWTTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(s.cfg.RefreshJWTSecret))
}

func (s *Service) parseRefreshToken(tokenStr string) (uuid.UUID, string, string, int64, error) {
	return s.parseRefreshTokenWithOpts(tokenStr)
}

func (s *Service) parseRefreshTokenAllowExpired(tokenStr string) (uuid.UUID, string, string, int64, error) {
	return s.parseRefreshTokenWithOpts(tokenStr, jwt.WithoutClaimsValidation())
}

func (s *Service) parseRefreshTokenWithOpts(tokenStr string, opts ...jwt.ParserOption) (uuid.UUID, string, string, int64, error) {
	claims := &refreshJWTClaims{}
	parserOpts := append([]jwt.ParserOption{jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})}, opts...)
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.cfg.RefreshJWTSecret), nil
	}, parserOpts...)
	if err != nil || !token.Valid || claims.Type != "refresh" || claims.FamilyID == "" || claims.ID == "" {
		return uuid.Nil, "", "", 0, ErrUnauthorized
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, "", "", 0, ErrUnauthorized
	}
	return uid, claims.FamilyID, claims.ID, claims.Epoch, nil
}

func (s *Service) incLoginMetric(outcome string) {
	if s.cfg.LoginMetrics != nil {
		s.cfg.LoginMetrics.WithLabelValues(outcome).Inc()
	}
}

func isValidEmail(email string) bool {
	if email == "" || len(email) > 254 {
		return false
	}
	addr, err := netmail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	return strings.Contains(email, "@")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
