package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baseValidEnv() map[string]string {
	return map[string]string{
		"JWT_SECRET":         "test-access-secret-value-12345",
		"REFRESH_JWT_SECRET": "test-refresh-secret-value-67890",
		"DATABASE_URL":       "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable",
		"VALKEY_URL":         "redis://127.0.0.1:6379/0",
	}
}

func mapLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadFromEnvValidAndDefaults(t *testing.T) {
	env := baseValidEnv()
	cfg, err := LoadFromEnv(mapLookup(env))
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("expected default port 8080, got %q", cfg.Port)
	}
	if cfg.JWTTTL != 15*time.Minute {
		t.Fatalf("expected 15m JWT_TTL, got %v", cfg.JWTTTL)
	}
	if cfg.RefreshJWTTTL != 168*time.Hour {
		t.Fatalf("expected 168h REFRESH_JWT_TTL, got %v", cfg.RefreshJWTTTL)
	}
	if cfg.LoginLimit != 5 || cfg.LoginIPLimit != 20 || cfg.LoginLimitDuration != 900*time.Second || cfg.TrustProxyHeaders {
		t.Fatalf("unexpected login limit defaults: acct=%d ip=%d dur=%v trustProxy=%v", cfg.LoginLimit, cfg.LoginIPLimit, cfg.LoginLimitDuration, cfg.TrustProxyHeaders)
	}
	if cfg.MailProvider != "mailpit" || cfg.SMTPHost != "127.0.0.1" || cfg.SMTPPort != 1025 {
		t.Fatalf("unexpected mailpit defaults: %+v", cfg)
	}
}

func TestLoadFromEnvRedisURLFallback(t *testing.T) {
	env := baseValidEnv()
	delete(env, "VALKEY_URL")
	env["REDIS_URL"] = "redis://127.0.0.1:6380/2"

	cfg, err := LoadFromEnv(mapLookup(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ValkeyURL != "redis://127.0.0.1:6380/2" {
		t.Fatalf("expected REDIS_URL fallback, got %q", cfg.ValkeyURL)
	}
}

func TestProductionRequiresSecureRefreshCookie(t *testing.T) {
	env := baseValidEnv()
	env["APP_ENV"] = "production"
	if _, err := LoadFromEnv(mapLookup(env)); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected insecure production cookie configuration to fail, got %v", err)
	}
	env["COOKIE_SECURE"] = "true"
	cfg, err := LoadFromEnv(mapLookup(env))
	if err != nil || !cfg.CookieSecure {
		t.Fatalf("expected secure production configuration, got %v", err)
	}
}

func TestLoadFromEnvFailFastSecretsAndDB(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{
			name:    "missing JWT_SECRET",
			mutate:  func(m map[string]string) { delete(m, "JWT_SECRET") },
			wantErr: "JWT_SECRET",
		},
		{
			name:    "whitespace JWT_SECRET",
			mutate:  func(m map[string]string) { m["JWT_SECRET"] = "   \t\n  " },
			wantErr: "JWT_SECRET",
		},
		{
			name:    "legacy placeholder JWT_SECRET",
			mutate:  func(m map[string]string) { m["JWT_SECRET"] = legacyAccessSecretPlaceholder },
			wantErr: "JWT_SECRET",
		},
		{
			name:    "swapped legacy refresh placeholder in JWT_SECRET",
			mutate:  func(m map[string]string) { m["JWT_SECRET"] = legacyRefreshSecretPlaceholder },
			wantErr: "JWT_SECRET",
		},
		{
			name:    "missing REFRESH_JWT_SECRET",
			mutate:  func(m map[string]string) { delete(m, "REFRESH_JWT_SECRET") },
			wantErr: "REFRESH_JWT_SECRET",
		},
		{
			name:    "whitespace REFRESH_JWT_SECRET",
			mutate:  func(m map[string]string) { m["REFRESH_JWT_SECRET"] = "   " },
			wantErr: "REFRESH_JWT_SECRET",
		},
		{
			name:    "legacy placeholder REFRESH_JWT_SECRET",
			mutate:  func(m map[string]string) { m["REFRESH_JWT_SECRET"] = legacyRefreshSecretPlaceholder },
			wantErr: "REFRESH_JWT_SECRET",
		},
		{
			name:    "swapped legacy access placeholder in REFRESH_JWT_SECRET",
			mutate:  func(m map[string]string) { m["REFRESH_JWT_SECRET"] = legacyAccessSecretPlaceholder },
			wantErr: "REFRESH_JWT_SECRET",
		},
		{
			name:    "invalid LOGIN_IP_LIMIT",
			mutate:  func(m map[string]string) { m["LOGIN_IP_LIMIT"] = "0" },
			wantErr: "LOGIN_IP_LIMIT",
		},
		{
			name:    "invalid TRUST_PROXY_HEADERS",
			mutate:  func(m map[string]string) { m["TRUST_PROXY_HEADERS"] = "not-a-bool" },
			wantErr: "TRUST_PROXY_HEADERS",
		},
		{
			name:    "missing DATABASE_URL",
			mutate:  func(m map[string]string) { delete(m, "DATABASE_URL") },
			wantErr: "DATABASE_URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseValidEnv()
			tc.mutate(env)
			_, err := LoadFromEnv(mapLookup(env))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestLoadDotEnvIfPresentDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# comment\nDOTENV_TEST_UNSET=from_file\nDOTENV_TEST_PRESET=\"from_file_should_not_win\"\nexport DOTENV_TEST_QUOTED='quoted val'\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOTENV_TEST_PRESET", "already_exported")
	_ = os.Unsetenv("DOTENV_TEST_UNSET")
	_ = os.Unsetenv("DOTENV_TEST_QUOTED")
	t.Cleanup(func() {
		_ = os.Unsetenv("DOTENV_TEST_UNSET")
		_ = os.Unsetenv("DOTENV_TEST_QUOTED")
	})

	if err := LoadDotEnvIfPresent(filepath.Join(dir, ".missing"), envPath); err != nil {
		t.Fatalf("LoadDotEnvIfPresent: %v", err)
	}
	if got := os.Getenv("DOTENV_TEST_PRESET"); got != "already_exported" {
		t.Fatalf("expected existing env to win, got %q", got)
	}
	if got := os.Getenv("DOTENV_TEST_UNSET"); got != "from_file" {
		t.Fatalf("expected DOTENV_TEST_UNSET=from_file, got %q", got)
	}
	if got := os.Getenv("DOTENV_TEST_QUOTED"); got != "quoted val" {
		t.Fatalf("expected DOTENV_TEST_QUOTED='quoted val', got %q", got)
	}
}
