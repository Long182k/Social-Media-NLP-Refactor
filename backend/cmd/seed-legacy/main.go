package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/jackc/pgx/v5"

	"github.com/social-media-nlp/refactor/backend/internal/seeddata"
)

func main() {
	dbURL := os.Getenv("LEGACY_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "LEGACY_DATABASE_URL (or DATABASE_URL) is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect legacy database: %v\n", err)
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
INSERT INTO users (id, "userName", "nickName", email, "hashedPassword", role, "isActive", "createdAt")
VALUES ($1, $2, $3, $4, $5, $6::"Role", $7, NOW())
ON CONFLICT (id) DO NOTHING
`, u.ID.String(), u.UserName, u.NickName, u.Email, hash, u.Role, u.IsActive)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insert legacy seed user %s: %v\n", u.Email, err)
			os.Exit(1)
		}
	}

	for _, p := range posts {
		_, err := tx.Exec(ctx, `
INSERT INTO posts (id, content, "userId", "groupId", sentiment, "createdAt")
VALUES ($1, $2, $3, NULL, $4, $5)
ON CONFLICT (id) DO NOTHING
`, p.ID.String(), p.Content, p.UserID.String(), p.Sentiment, p.CreatedAt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insert legacy seed post %s: %v\n", p.ID, err)
			os.Exit(1)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "commit legacy seed tx: %v\n", err)
		os.Exit(1)
	}

	var userCount, postCount int
	if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM posts)`).Scan(&userCount, &postCount); err != nil {
		fmt.Fprintf(os.Stderr, "verify legacy seed counts: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Legacy seed complete (idempotent): users=%d posts=%d\n", userCount, postCount)
}
