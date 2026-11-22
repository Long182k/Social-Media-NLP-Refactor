package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5"

	"github.com/social-media-nlp/refactor/backend/internal/config"
	"github.com/social-media-nlp/refactor/backend/internal/seeddata"
)

func main() {
	if err := config.LoadDotEnvIfPresent(".env", "../.env"); err != nil {
		fmt.Fprintf(os.Stderr, "load .env: %v\n", err)
		os.Exit(1)
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable" // #nosec G101 -- Published credentials for the loopback Compose fixture.
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close(ctx) }()

	hash, err := argon2id.CreateHash("password", &argon2id.Params{
		Memory:      19 * 1024,
		Iterations:  2,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash seed password: %v\n", err)
		os.Exit(1)
	}

	users := seeddata.DeterministicUsers()
	posts := seeddata.DeterministicPosts(users)

	tx, err := conn.Begin(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin tx: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, u := range users {
		_, err := tx.Exec(ctx, `
INSERT INTO users (id, user_name, nick_name, email, password_hash, role, is_active)
VALUES ($1, $2, $3, $4, $5, $6::user_role, $7)
ON CONFLICT (id) DO NOTHING
`, u.ID, u.UserName, u.NickName, u.Email, hash, u.Role, u.IsActive)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insert seed user %s: %v\n", u.Email, err)
			os.Exit(1)
		}
	}

	for _, p := range posts {
		_, err := tx.Exec(ctx, `
INSERT INTO posts (id, user_id, content, sentiment, content_version, created_at, updated_at)
VALUES ($1, $2, $3, $4::sentiment_type, 1, $5, $5)
ON CONFLICT (id) DO NOTHING
`, p.ID, p.UserID, p.Content, p.Sentiment, p.CreatedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insert seed post %s: %v\n", p.ID, err)
			os.Exit(1)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "commit seed tx: %v\n", err)
		os.Exit(1)
	}

	var userCount, postCount int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM posts)`).Scan(&userCount, &postCount); err != nil {
		fmt.Fprintf(os.Stderr, "verify seed counts: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Seed complete (idempotent): users=%d posts=%d\n", userCount, postCount)
}
