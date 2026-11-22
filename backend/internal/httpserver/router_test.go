package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpsEndpointsAndSPAFallback(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html><body>SPA</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	assetsDir := filepath.Join(staticDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('ok');"), 0o600); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	metrics := NewMetrics()

	router := NewRouter(Options{
		Logger:         logger,
		Metrics:        metrics,
		PostgresPinger: PingFunc(func(ctx context.Context) error { return nil }),
		ValkeyPinger:   PingFunc(func(ctx context.Context) error { return nil }),
		StaticDir:      staticDir,
	})

	// /healthz
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}

	// /readyz healthy
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /readyz, got %d", rec.Code)
	}

	// /metrics
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Fatalf("expected prometheus metrics on /metrics, got status=%d", rec.Code)
	}

	// /api/does-not-exist returns JSON 404, NOT index.html
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unmatched /api/*, got %d", rec.Code)
	}
	var errResp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil || errResp.Error != "NOT_FOUND" {
		t.Fatalf("expected JSON NOT_FOUND on /api/*, got %q", rec.Body.String())
	}

	// SPA fallback route /feed serves index.html
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SPA") {
		t.Fatalf("expected index.html on /feed, got status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Static asset gets immutable cache header
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("expected 200 + immutable Cache-Control on /assets/app.js, got status=%d cc=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestReadyzReturns503WhenDependencyDown(t *testing.T) {
	router := NewRouter(Options{
		Metrics:        NewMetrics(),
		PostgresPinger: PingFunc(func(ctx context.Context) error { return errors.New("pg down") }),
		ValkeyPinger:   PingFunc(func(ctx context.Context) error { return nil }),
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when postgres down, got %d", rec.Code)
	}
}

func TestSPAKeepsStaticFilesInsideRoot(t *testing.T) {
	static := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(filepath.Join(static, "index.html"), []byte("SPA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(static, "external.txt")); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewSPAHandler(static).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/external.txt", nil))
	if response.Code != http.StatusOK || response.Body.String() != "SPA" {
		t.Fatal("a path outside the static root must preserve SPA fallback")
	}
}
