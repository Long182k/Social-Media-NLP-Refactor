package posts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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
	"github.com/social-media-nlp/refactor/backend/internal/posts"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func setupPostsTestEnv(t *testing.T) (*pgxpool.Pool, *auth.Service, http.Handler) {
	t.Helper()
	ctx := context.Background()

	dbURL := envOrDefault("DATABASE_URL", "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable")
	valkeyURL := envOrDefault("VALKEY_URL", "redis://127.0.0.1:6379/0")

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
		t.Fatal(err)
	}
	rdb := redis.NewClient(rdbOpts)
	t.Cleanup(func() { _ = rdb.Close() })

	metrics := httpserver.NewMetrics()
	authSvc := auth.NewService(pool, rdb, mail.NewSMTPMailer("127.0.0.1", 1025, "no-reply@social-media.local"), auth.ServiceConfig{
		JWTSecret:        "posts-test-access-secret-123456",
		RefreshJWTSecret: "posts-test-refresh-secret-789012",
		JWTTTL:           10 * time.Minute,
		RefreshJWTTTL:    1 * time.Hour,
		ArgonParams: &argon2id.Params{
			Memory:      19 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
	})
	postsSvc := posts.NewService(pool, metrics.NLPLocalDuration, nil)

	authHandler := auth.NewHandler(authSvc)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	postsHandler := posts.NewHandler(postsSvc, authSvc, realtime.Publisher{Client: rdb, Logger: logger})

	router := httpserver.NewRouter(httpserver.Options{
		Logger:  logger,
		Metrics: metrics,
		RegisterAPIRoutes: func(api chi.Router) {
			authHandler.RegisterRoutes(api)
			postsHandler.RegisterRoutes(api)
		},
	})
	return pool, authSvc, router
}

func doReq(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func createTestUser(t *testing.T, pool *pgxpool.Pool, authSvc *auth.Service, role string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	email := "postuser_" + suffix + "@example.com"
	username := "postuser_" + suffix

	resp, _, err := authSvc.Register(ctx, auth.RegisterRequest{
		Username: username,
		Email:    email,
		Password: "Password123!",
		NickName: "Post User " + suffix,
	})
	if err != nil {
		t.Fatalf("Register test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, resp.User.ID)
	})

	if role == "ADMIN" {
		if _, err := pool.Exec(ctx, `UPDATE users SET role = 'ADMIN' WHERE id = $1`, resp.User.ID); err != nil {
			t.Fatal(err)
		}
		loginResp, _, err := authSvc.Login(ctx, auth.LoginRequest{
			Email:    email,
			Password: "Password123!",
		}, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		return resp.User.ID, loginResp.AccessToken
	}

	return resp.User.ID, resp.AccessToken
}

func TestPostsCRUDRevisionSentimentAndRBAC(t *testing.T) {
	pool, authSvc, router := setupPostsTestEnv(t)

	ownerID, ownerToken := createTestUser(t, pool, authSvc, "USER")
	_, otherToken := createTestUser(t, pool, authSvc, "USER")
	_, adminToken := createTestUser(t, pool, authSvc, "ADMIN")

	// Unauthenticated access -> 401
	unauthRec := doReq(t, router, http.MethodGet, "/api/posts", "", nil)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated GET /api/posts, got %d", unauthRec.Code)
	}

	// Create positive Vietnamese post
	createRec := doReq(t, router, http.MethodPost, "/api/posts", ownerToken, map[string]string{
		"content": "Hôm nay ứng dụng chạy rất tuyệt vời và mượt mà ❤️",
	})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 create post, got %d: %s", createRec.Code, createRec.Body.String())
	}
	lowerRaw := strings.ToLower(createRec.Body.String())
	if strings.Contains(lowerRaw, "passwordhash") || strings.Contains(lowerRaw, "password_hash") {
		t.Fatalf("post JSON leaked password hash: %s", createRec.Body.String())
	}

	var created posts.PostDTO
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.UserID != ownerID || created.Sentiment != "GOOD" || created.ContentVersion != 1 {
		t.Fatalf("unexpected created post: %+v", created)
	}

	// Non-owner PATCH -> 403
	forbidPatch := doReq(t, router, http.MethodPatch, "/api/posts/"+created.ID.String(), otherToken, map[string]string{
		"content": "hijack",
	})
	if forbidPatch.Code != http.StatusForbidden {
		t.Fatalf("expected 403 non-owner PATCH, got %d", forbidPatch.Code)
	}

	// Owner PATCH -> content_version 1 -> 2, sentiment GOOD -> BAD
	patchRec := doReq(t, router, http.MethodPatch, "/api/posts/"+created.ID.String(), ownerToken, map[string]string{
		"content": "Dịch vụ quá tệ và cùi bắp, rất thất vọng 😡",
	})
	if patchRec.Code != http.StatusOK {
		t.Fatalf("expected 200 owner PATCH, got %d: %s", patchRec.Code, patchRec.Body.String())
	}
	var updated posts.PostDTO
	if err := json.Unmarshal(patchRec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.ContentVersion != 2 || updated.Sentiment != "BAD" {
		t.Fatalf("expected contentVersion=2 and sentiment=BAD, got version=%d sentiment=%s", updated.ContentVersion, updated.Sentiment)
	}

	// GET single post
	getRec := doReq(t, router, http.MethodGet, "/api/posts/"+created.ID.String(), otherToken, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 GET /api/posts/:id, got %d", getRec.Code)
	}

	// Non-owner non-admin DELETE -> 403
	forbidDel := doReq(t, router, http.MethodDelete, "/api/posts/"+created.ID.String(), otherToken, nil)
	if forbidDel.Code != http.StatusForbidden {
		t.Fatalf("expected 403 non-owner DELETE, got %d", forbidDel.Code)
	}

	// ADMIN DELETE -> 200
	adminDel := doReq(t, router, http.MethodDelete, "/api/posts/"+created.ID.String(), adminToken, nil)
	if adminDel.Code != http.StatusOK {
		t.Fatalf("expected 200 ADMIN DELETE, got %d: %s", adminDel.Code, adminDel.Body.String())
	}

	// Confirm deleted -> 404
	afterDel := doReq(t, router, http.MethodGet, "/api/posts/"+created.ID.String(), ownerToken, nil)
	if afterDel.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", afterDel.Code)
	}
}

func TestPostsCursorPaginationTieBreakerAndSearchEscape(t *testing.T) {
	pool, authSvc, router := setupPostsTestEnv(t)
	ctx := context.Background()

	userID, token := createTestUser(t, pool, authSvc, "USER")
	tag := "tie_" + uuid.NewString()[:8] + "_100%"
	fixedTS := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	// Insert 5 posts with IDENTICAL created_at timestamp to force (created_at DESC, id DESC) tie-breaking
	insertedIDs := make([]uuid.UUID, 0, 5)
	for i := 0; i < 5; i++ {
		id := uuid.New()
		insertedIDs = append(insertedIDs, id)
		_, err := pool.Exec(ctx, `
INSERT INTO posts (id, user_id, content, sentiment, content_version, created_at, updated_at)
VALUES ($1, $2, $3, 'MODERATE', 1, $4, $4)
`, id, userID, fmt.Sprintf("%s item %d", tag, i), fixedTS)
		if err != nil {
			t.Fatalf("insert tie-breaker post: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM posts WHERE id = ANY($1)`, insertedIDs)
	})

	// Paginate limit=2 with q=tag -> should retrieve all 5 across 3 pages with zero duplicates/skips
	seen := make(map[uuid.UUID]struct{})
	var cursor string
	pages := 0
	for {
		pages++
		path := fmt.Sprintf("/api/posts?limit=2&q=%s", url.QueryEscape(tag))
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := doReq(t, router, http.MethodGet, path, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page %d failed: %d %s", pages, rec.Code, rec.Body.String())
		}
		var page posts.FeedResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Data {
			if _, dup := seen[item.ID]; dup {
				t.Fatalf("duplicate post ID %s returned across cursor pages", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
		if !page.HasMore {
			if page.NextCursor != nil {
				t.Fatalf("expected nextCursor=nil on final page, got %v", *page.NextCursor)
			}
			break
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			t.Fatal("expected non-empty nextCursor when hasMore=true")
		}
		cursor = *page.NextCursor
	}

	if pages != 3 || len(seen) != 5 {
		t.Fatalf("expected 3 pages and 5 unique posts, got pages=%d unique=%d", pages, len(seen))
	}

	// Verify '%' in query does NOT match unrelated posts when searching literal "100%"
	wildcardRec := doReq(t, router, http.MethodGet, "/api/posts?limit=10&q="+url.QueryEscape(tag), token, nil)
	if wildcardRec.Code != http.StatusOK {
		t.Fatal(wildcardRec.Code)
	}
	var wildPage posts.FeedResponse
	_ = json.Unmarshal(wildcardRec.Body.Bytes(), &wildPage)
	if len(wildPage.Data) != 5 {
		t.Fatalf("expected exactly 5 matches for literal %% search, got %d", len(wildPage.Data))
	}
}
