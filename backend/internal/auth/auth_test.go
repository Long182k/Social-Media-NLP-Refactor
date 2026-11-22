package auth_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/mail"
)

func testEnvOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func setupAuthTestEnv(t *testing.T) (*pgxpool.Pool, *redis.Client, *auth.Service, http.Handler) {
	t.Helper()
	return setupAuthTestEnvWithMail(t, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"))
}

func setupAuthTestEnvWithMail(t *testing.T, mailer mail.MailProvider) (*pgxpool.Pool, *redis.Client, *auth.Service, http.Handler) {
	t.Helper()
	ctx := context.Background()

	dbURL := testEnvOrDefault("DATABASE_URL", "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable")
	valkeyURL := testEnvOrDefault("VALKEY_URL", "redis://127.0.0.1:6379/0")

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("postgres ping failed: %v", err)
	}
	t.Cleanup(pool.Close)

	rdbOpts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(rdbOpts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Fatalf("valkey ping failed: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	svc := auth.NewService(pool, rdb, mailer, auth.ServiceConfig{
		JWTSecret:          "integration-test-access-secret-123456",
		RefreshJWTSecret:   "integration-test-refresh-secret-789012",
		JWTTTL:             5 * time.Minute,
		RefreshJWTTTL:      1 * time.Hour,
		CookieSecure:       false,
		LoginLimit:         5,
		LoginIPLimit:       20,
		LoginLimitDuration: 60 * time.Second,
		PublicBaseURL:      "http://localhost:5173",
		ResetTokenTTL:      15 * time.Minute,
		ArgonParams: &argon2id.Params{
			Memory:      19 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	handler := auth.NewHandler(svc)

	router := httpserver.NewRouter(httpserver.Options{
		Logger:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics:        httpserver.NewMetrics(),
		PostgresPinger: httpserver.PingFunc(pool.Ping),
		ValkeyPinger:   httpserver.PingFunc(func(c context.Context) error { return rdb.Ping(c).Err() }),
		RegisterAPIRoutes: func(api chi.Router) {
			handler.RegisterRoutes(api)
			api.With(auth.RequireAuth(svc), auth.RequireRole("ADMIN")).Get("/admin-only", func(w http.ResponseWriter, r *http.Request) {
				httpserver.WriteJSON(w, http.StatusOK, map[string]string{"ok": "admin"})
			})
		},
	})

	return pool, rdb, svc, router
}

func doJSONReq(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func findCookie(res *http.Response, name string) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func assertNoSensitiveJSONFields(t *testing.T, raw []byte) {
	t.Helper()
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"passwordhash", "password_hash", "hashedpassword", "hashedrefreshtoken", "refreshtoken"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("response JSON leaked forbidden field %q: %s", forbidden, string(raw))
		}
	}
}

func TestAuthRegisterLoginRefreshReuseAndDeactivated(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "authtest_" + suffix
	email := "authtest_" + suffix + "@example.com"
	password := "SecretPass123!"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
		_ = rdb.Del(ctx,
			"login_attempts:ident:"+strings.ToLower(username),
			"login_attempts:ident:"+strings.ToLower(email),
			"login_attempts:ip:192.0.2.1",
		).Err()
	})

	// 1. Register -> 201 + HttpOnly SameSite=Lax Path=/api/auth cookie + zero leaked hashes
	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
		"nickName": "Auth Test " + suffix,
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 register, got %d: %s", regRec.Code, regRec.Body.String())
	}
	assertNoSensitiveJSONFields(t, regRec.Body.Bytes())

	regCookie := findCookie(regRec.Result(), auth.RefreshCookieName)
	if regCookie == nil || regCookie.Value == "" {
		t.Fatal("expected refreshToken cookie on register")
	}
	if !regCookie.HttpOnly || regCookie.SameSite != http.SameSiteLaxMode || regCookie.Path != "/api/auth" {
		t.Fatalf("unexpected cookie attributes: %+v", regCookie)
	}

	var regBody auth.AuthResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &regBody); err != nil {
		t.Fatal(err)
	}

	// Duplicate register -> 409
	dupRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": password,
	}, nil)
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate register, got %d", dupRec.Code)
	}

	// /api/auth/me works with Bearer token
	meRec := doJSONReq(t, router, http.MethodGet, "/api/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + regBody.AccessToken,
	})
	if meRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me, got %d: %s", meRec.Code, meRec.Body.String())
	}
	assertNoSensitiveJSONFields(t, meRec.Body.Bytes())

	// RBAC /api/admin-only -> 403 for USER
	adminRec := doJSONReq(t, router, http.MethodGet, "/api/admin-only", nil, map[string]string{
		"Authorization": "Bearer " + regBody.AccessToken,
	})
	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for USER on admin route, got %d", adminRec.Code)
	}

	// 2. Refresh rotation -> new cookie, last_login_at updated, and old cookie replay triggers 401 + family revocation
	time.Sleep(15 * time.Millisecond)
	ref1Rec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, regCookie)
	if ref1Rec.Code != http.StatusOK {
		t.Fatalf("expected 200 refresh, got %d: %s", ref1Rec.Code, ref1Rec.Body.String())
	}
	assertNoSensitiveJSONFields(t, ref1Rec.Body.Bytes())
	rotatedCookie := findCookie(ref1Rec.Result(), auth.RefreshCookieName)
	if rotatedCookie == nil || rotatedCookie.Value == "" || rotatedCookie.Value == regCookie.Value {
		t.Fatal("expected rotated refreshToken cookie with new value")
	}

	// Replay old regCookie -> 401 AND revokes session family
	replayRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, regCookie)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on refresh token reuse, got %d", replayRec.Code)
	}
	// Legitimate rotatedCookie must now ALSO fail with 401 because family was revoked
	afterRevokeRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, rotatedCookie)
	if afterRevokeRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after family revocation, got %d", afterRevokeRec.Code)
	}

	// 3. Login rate limiting -> 5 bad attempts -> 6th returns 429 with Retry-After
	for i := 0; i < 5; i++ {
		badRec := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
			"username": username,
			"password": "wrong-password",
		}, nil)
		if badRec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, badRec.Code)
		}
	}
	lockedRec := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password, // even valid password is locked out
	}, nil)
	if lockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 lockout on 6th attempt, got %d", lockedRec.Code)
	}
	if lockedRec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}

	// Clear rate limit key to test successful login, signout, and deactivation
	_ = rdb.Del(ctx,
		"login_attempts:acct:"+regBody.User.ID.String(),
		"login_attempts:ip:192.0.2.1",
	).Err()

	loginRec := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, nil)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 login, got %d: %s", loginRec.Code, loginRec.Body.String())
	}
	assertNoSensitiveJSONFields(t, loginRec.Body.Bytes())
	loginCookie := findCookie(loginRec.Result(), auth.RefreshCookieName)
	var loginBody auth.AuthResponse
	_ = json.Unmarshal(loginRec.Body.Bytes(), &loginBody)

	// Deactivate user -> refresh -> 403, login -> 403, /me -> 403
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, loginBody.User.ID); err != nil {
		t.Fatal(err)
	}
	deactRefresh := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, loginCookie)
	if deactRefresh.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on refresh for deactivated user, got %d", deactRefresh.Code)
	}
	deactLogin := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, nil)
	if deactLogin.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on login for deactivated user, got %d", deactLogin.Code)
	}
	deactMe := doJSONReq(t, router, http.MethodGet, "/api/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + loginBody.AccessToken,
	})
	if deactMe.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on /me for deactivated user, got %d", deactMe.Code)
	}

	// Reactivate, login, and test signout clears cookie & invalidates refresh
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = TRUE WHERE id = $1`, loginBody.User.ID); err != nil {
		t.Fatal(err)
	}
	login2Rec := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"username": username,
		"password": password,
	}, nil)
	if login2Rec.Code != http.StatusOK {
		t.Fatalf("expected 200 login after reactivate, got %d", login2Rec.Code)
	}
	login2Cookie := findCookie(login2Rec.Result(), auth.RefreshCookieName)

	signoutRec := doJSONReq(t, router, http.MethodPost, "/api/auth/signout", nil, nil, login2Cookie)
	if signoutRec.Code != http.StatusOK {
		t.Fatalf("expected 200 signout, got %d", signoutRec.Code)
	}
	afterSignout := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, login2Cookie)
	if afterSignout.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 refresh after signout, got %d", afterSignout.Code)
	}
}

func TestAuthMailpitForgotAndResetPassword(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "mailtest_" + suffix
	email := "mailtest_" + suffix + "@example.com"
	oldPass := "InitialPass123!"
	newPass := "UpdatedPass456!"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE email = $1`, email)
		_ = rdb.Del(ctx, "login_attempts:ident:"+strings.ToLower(email), "login_attempts:ip:192.0.2.1").Err()
	})

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": oldPass,
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	activeCookie := findCookie(regRec.Result(), auth.RefreshCookieName)

	// Trigger forgot-password -> sends via live Mailpit SMTP on 127.0.0.1:1025
	fpRec := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{
		"email": email,
	}, nil)
	if fpRec.Code != http.StatusOK {
		t.Fatalf("expected 200 forgot-password, got %d: %s", fpRec.Code, fpRec.Body.String())
	}

	// Verify old password STILL works before reset confirmation!
	preResetLogin := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": oldPass,
	}, nil)
	if preResetLogin.Code != http.StatusOK {
		t.Fatalf("expected old password to still work before reset confirmation, got %d", preResetLogin.Code)
	}

	// Fetch email from Mailpit HTTP API (http://127.0.0.1:8025/api/v1/search?query=...)
	token := fetchResetTokenFromMailpit(t, email)
	if token == "" {
		t.Fatal("failed to extract reset token from Mailpit")
	}

	// Reset password with extracted token -> 200
	resetRec := doJSONReq(t, router, http.MethodPost, "/api/auth/reset-password", map[string]string{
		"token":       token,
		"newPassword": newPass,
	}, nil)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("expected 200 reset-password, got %d: %s", resetRec.Code, resetRec.Body.String())
	}

	// Reusing the same reset token fails -> 400
	reuseTokenRec := doJSONReq(t, router, http.MethodPost, "/api/auth/reset-password", map[string]string{
		"token":       token,
		"newPassword": "AnotherPassword999!",
	}, nil)
	if reuseTokenRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on second use of reset token, got %d", reuseTokenRec.Code)
	}

	// Existing refresh session must be revoked after password reset -> 401
	revokedRef := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, activeCookie)
	if revokedRef.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for active refresh session after password reset, got %d", revokedRef.Code)
	}

	// Old password fails -> 401, new password succeeds -> 200
	oldLogin := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": oldPass,
	}, nil)
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with old password after reset, got %d", oldLogin.Code)
	}
	newLogin := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"email":    email,
		"password": newPass,
	}, nil)
	if newLogin.Code != http.StatusOK {
		t.Fatalf("expected 200 with new password after reset, got %d", newLogin.Code)
	}
}

func TestAuthRegisterReturns503WhenPostgresDown(t *testing.T) {
	ctx := context.Background()
	rdbOpts, _ := redis.ParseURL("redis://127.0.0.1:6379/0")
	rdb := redis.NewClient(rdbOpts)
	defer func() { _ = rdb.Close() }()

	// Unreachable closed port on localhost for Postgres
	pool, err := pgxpool.New(ctx, "postgres://postgres:postgres@127.0.0.1:1/social_media?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	svc := auth.NewService(pool, rdb, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "test-secret-1",
		RefreshJWTSecret: "test-secret-2",
	})
	handler := auth.NewHandler(svc)
	router := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			handler.RegisterRoutes(api)
		},
	})

	rec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "downcheck",
		"email":    "downcheck@example.com",
		"password": "Password123!",
	}, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Postgres unreachable, got %d: %s", rec.Code, rec.Body.String())
	}
}

func doJSONReqFromAddr(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string, remoteAddr string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, buf)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func countMailpitMessages(t *testing.T, recipient string) int {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	searchURL := "http://127.0.0.1:8025/api/v1/search?query=" + url.QueryEscape("to:"+recipient)
	resp, err := client.Get(searchURL)
	if err != nil {
		t.Fatalf("mailpit search: %v", err)
	}
	defer resp.Body.Close()
	var list struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode mailpit search: %v", err)
	}
	return len(list.Messages)
}

func TestAuthForgotPasswordDeactivatedUserReturns403(t *testing.T) {
	pool, _, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "fpdeact_" + suffix
	email := "fpdeact_" + suffix + "@example.com"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": "Password123!",
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	var regBody auth.AuthResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &regBody); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
	})

	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, regBody.User.ID); err != nil {
		t.Fatal(err)
	}

	fpRec := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{
		"email": email,
	}, nil)
	if fpRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on forgot-password for deactivated user, got %d: %s", fpRec.Code, fpRec.Body.String())
	}

	var tokenCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`, regBody.User.ID).Scan(&tokenCount); err != nil {
		t.Fatal(err)
	}
	if tokenCount != 0 {
		t.Fatalf("expected 0 password_reset_tokens for deactivated user, got %d", tokenCount)
	}
	if got := countMailpitMessages(t, email); got != 0 {
		t.Fatalf("expected 0 emails sent to Mailpit for deactivated user, got %d", got)
	}

	// Non-existent email still returns 200 and sends 0 emails.
	unknownEmail := "nonexistent_" + suffix + "@example.com"
	unknownRec := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{
		"email": unknownEmail,
	}, nil)
	if unknownRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown email forgot-password, got %d", unknownRec.Code)
	}
	if got := countMailpitMessages(t, unknownEmail); got != 0 {
		t.Fatalf("expected 0 emails for unknown email, got %d", got)
	}
}

func TestAuthCaseInsensitiveRegistrationAndCanonicalConcurrentRateLimit(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	mixedUser := "Alice_" + suffix
	mixedEmail := "Alice_" + suffix + "@Example.com"
	password := "SecretPass123!"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": mixedUser,
		"email":    mixedEmail,
		"password": password,
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	var regBody auth.AuthResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &regBody); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
		_ = rdb.Del(ctx,
			"login_attempts:acct:"+regBody.User.ID.String(),
			"login_attempts:ip:10.10.10.1",
			"login_attempts:ip:10.10.10.2",
			"login_attempts:ip:10.10.10.3",
			"login_attempts:ip:10.10.10.4",
			"login_attempts:ip:10.10.10.5",
			"login_attempts:ip:10.10.10.6",
		).Err()
	})

	// Case-insensitive username collision -> 409
	dupUserRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": strings.ToLower(mixedUser),
		"email":    "different_" + suffix + "@example.com",
		"password": password,
	}, nil)
	if dupUserRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for case-insensitive duplicate username, got %d: %s", dupUserRec.Code, dupUserRec.Body.String())
	}

	// Case-insensitive email collision -> 409
	dupEmailRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "other_" + suffix,
		"email":    strings.ToUpper(mixedEmail),
		"password": password,
	}, nil)
	if dupEmailRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for case-insensitive duplicate email, got %d: %s", dupEmailRec.Code, dupEmailRec.Body.String())
	}

	// Email-shaped username rejected on registration -> 400 (prevents cross-field login ambiguity)
	emailShapedUserRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": mixedEmail,
		"email":    "crossfield_" + suffix + "@example.com",
		"password": password,
	}, nil)
	if emailShapedUserRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for email-shaped username, got %d: %s", emailShapedUserRec.Code, emailShapedUserRec.Body.String())
	}

	// Mixed username (3) + email (2) failed logins share canonical account counter -> 6th returns 429
	for i := 0; i < 3; i++ {
		r := doJSONReqFromAddr(t, router, http.MethodPost, "/api/auth/login", map[string]string{
			"identifier": strings.ToLower(mixedUser),
			"password":   "wrong-pass",
		}, nil, "10.10.10.1:41001")
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("username bad attempt %d: expected 401, got %d", i+1, r.Code)
		}
	}
	for i := 0; i < 2; i++ {
		r := doJSONReqFromAddr(t, router, http.MethodPost, "/api/auth/login", map[string]string{
			"identifier": strings.ToUpper(mixedEmail),
			"password":   "wrong-pass",
		}, nil, "10.10.10.1:41002")
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("email bad attempt %d: expected 401, got %d", i+1, r.Code)
		}
	}
	lockedRec := doJSONReqFromAddr(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": mixedUser,
		"password":   password,
	}, nil, "10.10.10.2:41003")
	if lockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from shared canonical account counter across username/email, got %d", lockedRec.Code)
	}

	// Reset counters and verify atomic concurrent enforcement (15 goroutines -> <= 5 pass limiter, rest 429)
	_ = rdb.Del(ctx, "login_attempts:acct:"+regBody.User.ID.String(), "login_attempts:ip:10.10.10.1", "login_attempts:ip:10.10.10.2").Err()

	const totalConcurrent = 15
	codes := make(chan int, totalConcurrent)
	var wg sync.WaitGroup
	startGate := make(chan struct{})
	for i := 0; i < totalConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate
			ident := mixedUser
			if idx%2 == 1 {
				ident = mixedEmail
			}
			rec := doJSONReqFromAddr(t, router, http.MethodPost, "/api/auth/login", map[string]string{
				"identifier": ident,
				"password":   "wrong-password",
			}, nil, fmt.Sprintf("10.10.10.3:%d", 50000+idx))
			codes <- rec.Code
		}(i)
	}
	close(startGate)
	wg.Wait()
	close(codes)

	var unauthCount, lockedCount int
	for c := range codes {
		switch c {
		case http.StatusUnauthorized:
			unauthCount++
		case http.StatusTooManyRequests:
			lockedCount++
		default:
			t.Fatalf("unexpected concurrent login status: %d", c)
		}
	}
	if unauthCount != 5 || lockedCount != 10 {
		t.Fatalf("expected exactly 5 unauthorized and 10 rate-limited concurrently, got 401=%d 429=%d", unauthCount, lockedCount)
	}

	// Test IP rate limit across distinct identifiers using a low LoginIPLimit router (TrustProxyHeaders=false)
	ipLimitedSvc := auth.NewService(pool, rdb, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:          "integration-test-access-secret-123456",
		RefreshJWTSecret:   "integration-test-refresh-secret-789012",
		LoginLimit:         5,
		LoginIPLimit:       4,
		LoginLimitDuration: 60 * time.Second,
		TrustProxyHeaders:  false,
		ArgonParams: &argon2id.Params{
			Memory:      19 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	ipHandler := auth.NewHandler(ipLimitedSvc)
	ipRouter := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			ipHandler.RegisterRoutes(api)
		},
	})

	for i := 0; i < 4; i++ {
		unknownID := fmt.Sprintf("unknown_%s_%d", suffix, i)
		t.Cleanup(func() { _ = rdb.Del(ctx, "login_attempts:ident:"+unknownID).Err() })
		r := doJSONReqFromAddr(t, ipRouter, http.MethodPost, "/api/auth/login", map[string]string{
			"identifier": unknownID,
			"password":   "wrong",
		}, nil, fmt.Sprintf("10.10.10.4:%d", 30000+i))
		if r.Code != http.StatusUnauthorized {
			t.Fatalf("distinct identifier %d: expected 401, got %d", i, r.Code)
		}
	}
	// 5th distinct identifier from same IP (even with spoofed X-Forwarded-For) -> 429
	u5 := fmt.Sprintf("unknown_%s_5", suffix)
	t.Cleanup(func() { _ = rdb.Del(ctx, "login_attempts:ident:"+u5).Err() })
	ipLockedRec := doJSONReqFromAddr(t, ipRouter, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": u5,
		"password":   "wrong",
	}, map[string]string{"X-Forwarded-For": "203.0.113.99"}, "10.10.10.4:39999")
	if ipLockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on IP limit across distinct identifiers, got %d", ipLockedRec.Code)
	}
	// Different IP still gets 401 for u5
	otherIPRec := doJSONReqFromAddr(t, ipRouter, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": u5,
		"password":   "wrong",
	}, nil, "10.10.10.5:39999")
	if otherIPRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from unaffected IP, got %d", otherIPRec.Code)
	}

	// Valid-password deactivated login refunds attempt so repeated 403 does not lock out with 429
	_ = rdb.Del(ctx, "login_attempts:acct:"+regBody.User.ID.String()).Err()
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id = $1`, regBody.User.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		r := doJSONReqFromAddr(t, router, http.MethodPost, "/api/auth/login", map[string]string{
			"identifier": mixedUser,
			"password":   password,
		}, nil, "10.10.10.6:42000")
		if r.Code != http.StatusForbidden {
			t.Fatalf("deactivated valid login attempt %d: expected 403 (not 429), got %d", i+1, r.Code)
		}
	}
}

func TestAuthResetPasswordSurvivesValkeyFailureAndEpochMismatch(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "resetvk_" + suffix
	email := "resetvk_" + suffix + "@example.com"
	oldPass := "OldPass123!"
	newPass := "NewPass456!"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": oldPass,
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	preResetCookie := findCookie(regRec.Result(), auth.RefreshCookieName)
	var regBody auth.AuthResponse
	_ = json.Unmarshal(regRec.Body.Bytes(), &regBody)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
		_ = rdb.Del(ctx, "login_attempts:acct:"+regBody.User.ID.String(), "login_attempts:ip:192.0.2.1").Err()
	})

	fpRec := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{
		"email": email,
	}, nil)
	if fpRec.Code != http.StatusOK {
		t.Fatalf("forgot-password failed: %d", fpRec.Code)
	}
	resetToken := fetchResetTokenFromMailpit(t, email)

	// Simulate unreachable Valkey during ResetPassword
	downRDB := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 100 * time.Millisecond,
		MaxRetries:  0,
	})
	defer func() { _ = downRDB.Close() }()

	downVKSvc := auth.NewService(pool, downRDB, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "integration-test-access-secret-123456",
		RefreshJWTSecret: "integration-test-refresh-secret-789012",
		ArgonParams: &argon2id.Params{
			Memory:      19 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	downVKHandler := auth.NewHandler(downVKSvc)
	downVKRouter := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			downVKHandler.RegisterRoutes(api)
		},
	})

	resetRec := doJSONReq(t, downVKRouter, http.MethodPost, "/api/auth/reset-password", map[string]string{
		"token":       resetToken,
		"newPassword": newPass,
	}, nil)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when Valkey fails during ResetPassword, got %d: %s", resetRec.Code, resetRec.Body.String())
	}

	// Token cannot be reused
	reuseRec := doJSONReq(t, router, http.MethodPost, "/api/auth/reset-password", map[string]string{
		"token":       resetToken,
		"newPassword": "AnotherPass789!",
	}, nil)
	if reuseRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on reset token reuse, got %d", reuseRec.Code)
	}

	// Old refresh cookie still fails with 401 against live Valkey due to bumped session_epoch!
	oldRefreshRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, preResetCookie)
	if oldRefreshRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for pre-reset refresh cookie after epoch bump, got %d: %s", oldRefreshRec.Code, oldRefreshRec.Body.String())
	}
}

func TestAuthRefresh503PreservesCookieAndSucceedsOnRetry(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "ref503_" + suffix
	email := "ref503_" + suffix + "@example.com"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": "Password123!",
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	cookie := findCookie(regRec.Result(), auth.RefreshCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("expected refresh cookie")
	}
	var regBody auth.AuthResponse
	_ = json.Unmarshal(regRec.Body.Bytes(), &regBody)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
	})

	// Router with unreachable Postgres but same secrets & live Valkey
	downPool, err := pgxpool.New(ctx, "postgres://postgres:postgres@127.0.0.1:1/social_media?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer downPool.Close()

	downPGSvc := auth.NewService(downPool, rdb, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "integration-test-access-secret-123456",
		RefreshJWTSecret: "integration-test-refresh-secret-789012",
	})
	downPGHandler := auth.NewHandler(downPGSvc)
	downPGRouter := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			downPGHandler.RegisterRoutes(api)
		},
	})

	failRec := doJSONReq(t, downPGRouter, http.MethodPost, "/api/auth/refresh", nil, nil, cookie)
	if failRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Postgres down during refresh, got %d: %s", failRec.Code, failRec.Body.String())
	}
	if cleared := findCookie(failRec.Result(), auth.RefreshCookieName); cleared != nil {
		t.Fatalf("expected refresh cookie NOT to be cleared on 503, got Set-Cookie: %+v", cleared)
	}

	// Retry with the exact same cookie against healthy router -> 200
	retryRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, cookie)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on refresh retry after transient 503, got %d: %s", retryRec.Code, retryRec.Body.String())
	}
}

func readRESPFrame(r *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	var readVal func() error
	readVal = func() error {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return err
		}
		out.Write(line)
		if len(line) < 3 {
			return fmt.Errorf("malformed RESP line %q", line)
		}
		switch line[0] {
		case '+', '-', ':', '_', '#', ',', '(':
			return nil
		case '$', '!', '=':
			n, err := strconv.Atoi(strings.TrimSpace(string(line[1:])))
			if err != nil {
				return err
			}
			if n < 0 {
				return nil
			}
			buf := make([]byte, n+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return err
			}
			out.Write(buf)
			return nil
		case '*', '~':
			n, err := strconv.Atoi(strings.TrimSpace(string(line[1:])))
			if err != nil {
				return err
			}
			for i := 0; i < n; i++ {
				if err := readVal(); err != nil {
					return err
				}
			}
			return nil
		case '%':
			n, err := strconv.Atoi(strings.TrimSpace(string(line[1:])))
			if err != nil {
				return err
			}
			for i := 0; i < 2*n; i++ {
				if err := readVal(); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unexpected RESP prefix %q", line[0])
		}
	}
	if err := readVal(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func startValkeyProxy(t *testing.T, targetAddr string, intercept func(req []byte, upstream *bufio.Reader, upstreamConn net.Conn, clientConn net.Conn) (handled bool, err error)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			cConn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer func() { _ = client.Close() }()
				uConn, err := net.DialTimeout("tcp", targetAddr, 2*time.Second)
				if err != nil {
					return
				}
				defer func() { _ = uConn.Close() }()

				cReader := bufio.NewReader(client)
				uReader := bufio.NewReader(uConn)
				for {
					req, err := readRESPFrame(cReader)
					if err != nil {
						return
					}
					if intercept != nil {
						handled, iErr := intercept(req, uReader, uConn, client)
						if iErr != nil {
							return
						}
						if handled {
							continue
						}
					}
					if _, err := uConn.Write(req); err != nil {
						return
					}
					resp, err := readRESPFrame(uReader)
					if err != nil {
						return
					}
					if _, err := client.Write(resp); err != nil {
						return
					}
				}
			}(cConn)
		}
	}()
	return ln.Addr().String()
}

func valkeyHostPortFromEnv(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(testEnvOrDefault("VALKEY_URL", "redis://127.0.0.1:6379/0"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestAuthResetPasswordPreservesConcurrentNewEpochLogin(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "raceep_" + suffix
	email := "raceep_" + suffix + "@example.com"
	oldPass := "OldPass123!"
	newPass := "NewPass456!"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": oldPass,
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	oldCookie := findCookie(regRec.Result(), auth.RefreshCookieName)
	var regBody auth.AuthResponse
	_ = json.Unmarshal(regRec.Body.Bytes(), &regBody)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
		_ = rdb.Del(ctx, "login_attempts:acct:"+regBody.User.ID.String(), "login_attempts:ip:192.0.2.1").Err()
	})

	fpRec := doJSONReq(t, router, http.MethodPost, "/api/auth/forgot-password", map[string]string{
		"email": email,
	}, nil)
	if fpRec.Code != http.StatusOK {
		t.Fatalf("forgot-password failed: %d", fpRec.Code)
	}
	resetToken := fetchResetTokenFromMailpit(t, email)

	revocationStarted := make(chan struct{})
	allowRevocation := make(chan struct{})
	var notifyOnce sync.Once

	proxyAddr := startValkeyProxy(t, valkeyHostPortFromEnv(t), func(req []byte, upstream *bufio.Reader, uConn net.Conn, client net.Conn) (bool, error) {
		if bytes.HasPrefix(req, []byte("*5\r\n")) && bytes.Contains(req, []byte("refresh:user:"+regBody.User.ID.String())) {
			notifyOnce.Do(func() { close(revocationStarted) })
			<-allowRevocation
		}
		return false, nil
	})

	proxyRDB := redis.NewClient(&redis.Options{Addr: proxyAddr, DB: 0, Protocol: 2})
	defer func() { _ = proxyRDB.Close() }()

	proxySvc := auth.NewService(pool, proxyRDB, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "integration-test-access-secret-123456",
		RefreshJWTSecret: "integration-test-refresh-secret-789012",
		ArgonParams: &argon2id.Params{
			Memory:      19 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	proxyHandler := auth.NewHandler(proxySvc)
	proxyRouter := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			proxyHandler.RegisterRoutes(api)
		},
	})

	resetDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resetDone <- doJSONReq(t, proxyRouter, http.MethodPost, "/api/auth/reset-password", map[string]string{
			"token":       resetToken,
			"newPassword": newPass,
		}, nil)
	}()

	// Wait until Postgres committed epoch 2 and ResetPassword is about to clean Valkey (bounded wait)
	select {
	case <-revocationStarted:
	case <-time.After(5 * time.Second):
		close(allowRevocation)
		t.Fatal("timed out waiting for ResetPassword to invoke Valkey session cleanup")
	}

	// Concurrent new-password login creates an epoch-2 session in Valkey BEFORE ResetPassword cleanup runs
	newLoginRec := doJSONReq(t, router, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": username,
		"password":   newPass,
	}, nil)
	if newLoginRec.Code != http.StatusOK {
		close(allowRevocation)
		t.Fatalf("concurrent new-password login failed: %d %s", newLoginRec.Code, newLoginRec.Body.String())
	}
	newLoginCookie := findCookie(newLoginRec.Result(), auth.RefreshCookieName)
	if newLoginCookie == nil || newLoginCookie.Value == "" {
		close(allowRevocation)
		t.Fatal("expected refresh cookie from concurrent new-password login")
	}

	oldFamID := extractJWTFamilyID(t, oldCookie.Value)
	newFamID := extractJWTFamilyID(t, newLoginCookie.Value)

	// Let ResetPassword's RevokeUserSessionsBeforeEpoch execute now
	close(allowRevocation)
	resetRec := <-resetDone
	if resetRec.Code != http.StatusOK {
		t.Fatalf("expected 200 reset-password, got %d: %s", resetRec.Code, resetRec.Body.String())
	}

	// Verify directly in Valkey BEFORE any HTTP refresh call that cleanup deleted the old epoch-1
	// family and preserved the concurrent epoch-2 family.
	if exists, err := rdb.Exists(ctx, "refresh:family:"+oldFamID).Result(); err != nil || exists != 0 {
		t.Fatalf("expected old epoch-1 Valkey family %s to be deleted (exists=%d, err=%v)", oldFamID, exists, err)
	}
	if exists, err := rdb.Exists(ctx, "refresh:family:"+newFamID).Result(); err != nil || exists != 1 {
		t.Fatalf("expected concurrent epoch-2 Valkey family %s to be preserved (exists=%d, err=%v)", newFamID, exists, err)
	}
	if isMember, err := rdb.SIsMember(ctx, "refresh:user:"+regBody.User.ID.String(), oldFamID).Result(); err != nil || isMember {
		t.Fatalf("expected old family %s removed from user session set (isMember=%v, err=%v)", oldFamID, isMember, err)
	}
	if isMember, err := rdb.SIsMember(ctx, "refresh:user:"+regBody.User.ID.String(), newFamID).Result(); err != nil || !isMember {
		t.Fatalf("expected new family %s retained in user session set (isMember=%v, err=%v)", newFamID, isMember, err)
	}

	// Old epoch-1 session is rejected (401), while concurrent epoch-2 login session survives & refreshes (200)!
	oldRefreshRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, oldCookie)
	if oldRefreshRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for pre-reset epoch-1 cookie, got %d", oldRefreshRec.Code)
	}
	newRefreshRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, newLoginCookie)
	if newRefreshRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for concurrent post-reset epoch-2 session after cleanup, got %d: %s", newRefreshRec.Code, newRefreshRec.Body.String())
	}
}

func extractJWTFamilyID(t *testing.T, jwtStr string) string {
	t.Helper()
	parts := strings.Split(jwtStr, ".")
	if len(parts) != 3 {
		t.Fatalf("invalid JWT format: %q", jwtStr)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims struct {
		Fam string `json:"fam"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Fam == "" {
		t.Fatalf("extract fam from JWT claims: err=%v fam=%q", err, claims.Fam)
	}
	return claims.Fam
}

func TestAuthRefreshLostValkeyReplyRetryDoesNotRevokeFamily(t *testing.T) {
	pool, rdb, _, router := setupAuthTestEnv(t)
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	username := "lostack_" + suffix
	email := "lostack_" + suffix + "@example.com"

	regRec := doJSONReq(t, router, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    email,
		"password": "Password123!",
	}, nil)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", regRec.Code, regRec.Body.String())
	}
	cookie := findCookie(regRec.Result(), auth.RefreshCookieName)
	var regBody auth.AuthResponse
	_ = json.Unmarshal(regRec.Body.Bytes(), &regBody)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, regBody.User.ID)
	})

	var droppedSuccessReply atomic.Bool

	proxyAddr := startValkeyProxy(t, valkeyHostPortFromEnv(t), func(req []byte, upstream *bufio.Reader, uConn net.Conn, client net.Conn) (bool, error) {
		// RotateSession sends 2 keys + 7 args -> *12 RESP array containing "refresh:family:"
		if bytes.HasPrefix(req, []byte("*12\r\n")) && bytes.Contains(req, []byte("refresh:family:")) {
			if _, err := uConn.Write(req); err != nil {
				return true, err
			}
			reply, err := readRESPFrame(upstream)
			if err != nil {
				return true, err
			}
			// Only drop AFTER observing a genuine "OK" rotation reply (never drop -NOSCRIPT).
			if bytes.Equal(reply, []byte("$2\r\nOK\r\n")) || bytes.Equal(reply, []byte("+OK\r\n")) {
				if droppedSuccessReply.CompareAndSwap(false, true) {
					return true, fmt.Errorf("simulated dropped Valkey OK reply")
				}
			}
			if _, err := client.Write(reply); err != nil {
				return true, err
			}
			return true, nil
		}
		return false, nil
	})

	proxyRDB := redis.NewClient(&redis.Options{
		Addr:       proxyAddr,
		DB:         0,
		Protocol:   2,
		MaxRetries: 3,
	})
	defer func() { _ = proxyRDB.Close() }()

	proxySvc := auth.NewService(pool, proxyRDB, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "integration-test-access-secret-123456",
		RefreshJWTSecret: "integration-test-refresh-secret-789012",
	})
	proxyHandler := auth.NewHandler(proxySvc)
	proxyRouter := httpserver.NewRouter(httpserver.Options{
		Logger:  slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Metrics: httpserver.NewMetrics(),
		RegisterAPIRoutes: func(api chi.Router) {
			proxyHandler.RegisterRoutes(api)
		},
	})

	// go-redis retries RotateSession after the dropped OK reply -> must succeed 200 without revoking family!
	refRec := doJSONReq(t, proxyRouter, http.MethodPost, "/api/auth/refresh", nil, nil, cookie)
	if refRec.Code != http.StatusOK {
		t.Fatalf("expected 200 after idempotent RotateSession retry on lost Valkey reply, got %d: %s", refRec.Code, refRec.Body.String())
	}
	if !droppedSuccessReply.Load() {
		t.Fatal("expected proxy to observe and drop a successful RotateSession OK reply")
	}
	rotatedCookie := findCookie(refRec.Result(), auth.RefreshCookieName)
	if rotatedCookie == nil || rotatedCookie.Value == "" {
		t.Fatal("expected rotated refresh cookie after recovery")
	}

	// Subsequent refresh with rotatedCookie against live Valkey also succeeds 200
	nextRec := doJSONReq(t, router, http.MethodPost, "/api/auth/refresh", nil, nil, rotatedCookie)
	if nextRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on follow-up refresh with rotated cookie, got %d: %s", nextRec.Code, nextRec.Body.String())
	}
	_ = rdb.Ping(ctx).Err()
}

func fetchResetTokenFromMailpit(t *testing.T, recipient string) string {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	searchURL := "http://127.0.0.1:8025/api/v1/search?query=" + url.QueryEscape("to:"+recipient)

	type mpList struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	var msgID string
	for attempt := 0; attempt < 15; attempt++ {
		resp, err := client.Get(searchURL)
		if err == nil {
			var list mpList
			_ = json.NewDecoder(resp.Body).Decode(&list)
			_ = resp.Body.Close()
			if len(list.Messages) > 0 {
				msgID = list.Messages[0].ID
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if msgID == "" {
		t.Fatalf("no message found in Mailpit for %s", recipient)
	}

	msgResp, err := client.Get(fmt.Sprintf("http://127.0.0.1:8025/api/v1/message/%s", msgID))
	if err != nil {
		t.Fatalf("fetch mailpit message: %v", err)
	}
	defer msgResp.Body.Close()

	var detail struct {
		Text string `json:"Text"`
	}
	if err := json.NewDecoder(msgResp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)
	m := re.FindStringSubmatch(detail.Text)
	if len(m) < 2 {
		t.Fatalf("token not found in email body: %q", detail.Text)
	}
	return m[1]
}
