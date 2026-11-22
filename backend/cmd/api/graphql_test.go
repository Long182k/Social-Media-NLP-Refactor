package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	graphapi "github.com/social-media-nlp/refactor/backend/internal/graphql"
)

type queryCounter struct {
	sqlc.DBTX
	count atomic.Int32
}

func (c *queryCounter) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	c.count.Add(1)
	return c.DBTX.Query(ctx, query, args...)
}

func (c *queryCounter) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	c.count.Add(1)
	return c.DBTX.QueryRow(ctx, query, args...)
}

func TestGraphQLBatchesNestedReadsAndGuardsAdmin(t *testing.T) {
	f := newSocialFixture(t)
	viewer, admin := f.user(t, false), f.user(t, true)
	ctx := context.Background()
	tag := "graph-" + uuid.NewString()
	for range 12 {
		id := uuid.New()
		name := "graph-author-" + id.String()
		if _, err := f.pool.Exec(ctx, "INSERT INTO users (id, user_name, nick_name, email, password_hash) VALUES ($1, $2, $2, $3, 'integration-test')", id, name, name+"@example.test"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = f.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id) })
		if _, err := f.pool.Exec(ctx, "INSERT INTO posts (user_id, content) VALUES ($1, $2)", id, tag); err != nil {
			t.Fatal(err)
		}
	}
	counter := &queryCounter{DBTX: f.pool}
	authService := auth.NewService(f.pool, f.cache, nil, auth.ServiceConfig{
		JWTSecret: "local-social-access-session-fixture", RefreshJWTSecret: "local-social-refresh-session-fixture",
	})
	server := httptest.NewServer(graphapi.NewHandler(counter, authService, slog.New(slog.NewTextHandler(io.Discard, nil))))
	t.Cleanup(server.Close)
	viewer.url = server.URL
	for _, first := range []int{1, 12} {
		counter.count.Store(0)
		response := viewer.call(t, http.MethodPost, "/", map[string]any{
			"query": `query TestFeed($first: Int, $search: String) {
				feed(first: $first, search: $search) {
					data { id author { userName } attachments { mediaId } counts { commentCount } viewerInteractions { liked } }
				}
			}`,
			"variables": map[string]any{"first": first, "search": tag},
		}, http.StatusOK)
		var decoded struct {
			Data struct {
				Feed struct {
					Posts []map[string]any `json:"data"`
				} `json:"feed"`
			} `json:"data"`
			Errors []any `json:"errors"`
		}
		decodeSocial(t, response, &decoded)
		if len(decoded.Errors) != 0 || len(decoded.Data.Feed.Posts) != first {
			t.Fatalf("unexpected graph response: %s", response)
		}
		if count := counter.count.Load(); count != 5 {
			t.Fatalf("feed of %d posts executed %d queries, expected one base and four batch queries", first, count)
		}
	}
	counter.count.Store(0)
	query := map[string]string{"query": `query { ...Stats } fragment Stats on Query { alias: adminDashboardStats { totalUsers } }`}
	response := viewer.call(t, http.MethodPost, "/", query, http.StatusForbidden)
	if counter.count.Load() != 0 || !bytes.Contains(response, []byte(`"FORBIDDEN"`)) {
		t.Fatalf("admin authorization performed a data read or malformed denial: %s", response)
	}
	admin.url = f.url
	response = admin.call(t, http.MethodPost, "/graphql", query, http.StatusOK)
	if !bytes.Contains(response, []byte(`"totalUsers":`)) {
		t.Fatalf("approved admin graph query missing data: %s", response)
	}
}
