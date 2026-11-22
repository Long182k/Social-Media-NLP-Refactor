package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/eval"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestWorkersAndEvaluationShareBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cache := localCache(t, 15)
	now := time.Now()
	key, _, midnight := dayWindow(now)
	const startingUsage = 8800
	evidence := Evidence{
		AccountID: strings.Repeat("a", 32), Plan: "workers-free", VerifiedAt: now,
		NoPaymentMethod: true, NoCredits: true, HardDailyLimit: 10000,
		SharedBudget: true, ValkeyNoEviction: true, ValkeyPersistent: true, ValkeyDurableWrites: true,
		Models: []string{PrimaryModel, SecondaryModel}, UsageObservedAt: now, NeuronsAlreadyUsed: startingUsage,
	}
	client, err := NewCloudClient(evidence, "test-token", cache)
	if err != nil {
		t.Fatal(err)
	}
	var calls, cost atomic.Int64
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Host != "api.cloudflare.com" || req.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected Workers AI HTTP request")
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var input struct {
			Model     string    `json:"model"`
			Messages  []message `json:"messages"`
			MaxTokens int64     `json:"max_completion_tokens"`
		}
		if err := json.Unmarshal(data, &input); err != nil {
			return nil, err
		}
		if input.MaxTokens != maxOutputTokens || len(input.Messages) != 2 {
			t.Error("output bound or prompt missing")
		}
		rate := rates[input.Model]
		weighted := int64(len(data)+1024)*rate.input + input.MaxTokens*rate.output
		cost.Add((weighted + 999_999) / 1_000_000)
		status := http.StatusOK
		if input.Model == PrimaryModel && input.Messages[1].Content == "runtime sample" {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(
			`{"choices":[{"message":{"content":"\"GOOD\""}}],"usage":{"prompt_tokens":58,"completion_tokens":3}}`))}, nil
	})
	if _, err := client.Infer(ctx, "runtime sample"); !errors.Is(err, ErrBudgetUnavailable) || calls.Load() != 0 {
		t.Fatalf("missing ledger attempted inference: %v, calls %d", err, calls.Load())
	}
	if err := InitializeBudget(ctx, cache, evidence, now); err != nil {
		t.Fatalf("initialize isolated Valkey DB 15: %v", err)
	}
	t.Cleanup(func() { _ = cache.Del(context.Background(), key).Err() })
	if err := InitializeBudget(ctx, cache, evidence, now); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("existing ledger can be reset: %v", err)
	}
	samples := []eval.Example{{Language: "en", Text: "evaluation sample", Label: lexicon.SentimentGood}}
	report, err := client.EvaluateModel(ctx, PrimaryModel, samples)
	if err != nil || report.InputTokens != 58 || report.OutputTokens != 3 || report.Scores.MacroF1 != 1.0/3 || report.Neurons <= 0 {
		t.Fatalf("evaluation result: %+v, %v", report, err)
	}
	if label, err := client.Infer(ctx, "runtime sample"); err != nil || label != lexicon.SentimentGood || calls.Load() != 4 {
		t.Fatalf("fallback result: %s, %v, calls %d", label, err, calls.Load())
	}
	var burst sync.WaitGroup
	for index := range 48 {
		burst.Go(func() {
			var err error
			if index%3 == 0 {
				_, err = client.EvaluateModel(ctx, SecondaryModel, samples)
			} else {
				_, err = client.Infer(ctx, "runtime sample")
			}
			if err != nil && !errors.Is(err, ErrBudgetUnavailable) {
				t.Errorf("unexpected burst error: %v", err)
			}
		})
	}
	burst.Wait()
	used, err := cache.Get(ctx, key).Int64()
	if err != nil || used != startingUsage+cost.Load() || used > DailyLimit || used < DailyLimit-15 {
		t.Fatalf("actual ledger=%d, expected cost=%d, calls=%d, err=%v", used, cost.Load(), calls.Load(), err)
	}
	expiresAt, err := cache.ExpireTime(ctx, key).Result()
	if err != nil || int64(expiresAt/time.Second) != midnight.Add(time.Hour).Unix() {
		t.Fatalf("wrong UTC expiry: %v (%v)", expiresAt, err)
	}
	if _, err := reserve(ctx, cache, now.AddDate(0, 0, 1), 1); !errors.Is(err, ErrBudgetUnavailable) {
		t.Fatalf("future UTC day accepted: %v", err)
	}
}

func TestQueueDropsImmediatelyWhenFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, concurrency)
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_refinement_drops"})
	pool := Start(ctx, fakeClassifier(func(ctx context.Context, _ string) (lexicon.Sentiment, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}), nil, realtime.Publisher{}, counter)
	job := Job{EntityType: Post, ID: uuid.New(), Version: 1, Content: "text"}
	for range concurrency {
		if !pool.Offer(job) {
			t.Fatal("worker job dropped early")
		}
	}
	for range concurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	for range queueCapacity {
		if !pool.Offer(job) {
			t.Fatal("buffer has fewer than 256 entries")
		}
	}
	result := make(chan bool, 1)
	go func() { result <- pool.Offer(job) }()
	select {
	case accepted := <-result:
		if accepted || testutil.ToFloat64(counter) != 1 {
			t.Fatal("saturated queue should drop and count the job")
		}
	case <-time.After(time.Second):
		t.Fatal("saturated queue blocked its caller")
	}
}

type fakeClassifier func(context.Context, string) (lexicon.Sentiment, error)

func (f fakeClassifier) Infer(ctx context.Context, content string) (lexicon.Sentiment, error) {
	return f(ctx, content)
}

func TestDelayedRefinementCannotOverwriteEdits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	database := os.Getenv("DATABASE_URL")
	if database == "" {
		database = "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable"
	}
	db, err := pgxpool.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queries := sqlc.New(db)
	name := "nlp_" + uuid.NewString()[:8]
	owner, err := queries.CreateUser(ctx, sqlc.CreateUserParams{
		UserName: name, NickName: name, Email: name + "@example.test",
		PasswordHash: "unused", IsActive: true, Role: sqlc.UserRoleUSER,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(context.Background(), "DELETE FROM users WHERE id = $1", owner.ID)
	post, err := queries.CreatePost(ctx, sqlc.CreatePostParams{UserID: owner.ID, Content: "original", Sentiment: sqlc.SentimentTypeMODERATE})
	if err != nil {
		t.Fatal(err)
	}
	comment, err := queries.CreateComment(ctx, sqlc.CreateCommentParams{
		UserID: owner.ID, PostID: post.ID, Content: "original", Sentiment: sqlc.SentimentTypeMODERATE,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := localCache(t, 15)
	sub := cache.Subscribe(ctx, realtime.SentimentChannel)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	started, resume := make(chan struct{}, 2), make(chan struct{})
	pool := &Pool{
		ctx: ctx, queries: queries, publisher: realtime.Publisher{Client: cache, Logger: slog.New(slog.DiscardHandler)},
		inference: fakeClassifier(func(ctx context.Context, text string) (lexicon.Sentiment, error) {
			if text == "original" {
				started <- struct{}{}
				select {
				case <-resume:
				case <-ctx.Done():
					return "", ctx.Err()
				}
				return lexicon.SentimentGood, nil
			}
			return lexicon.SentimentModerate, nil
		}),
	}
	jobs := []Job{
		{EntityType: Post, ID: post.ID, Version: 1, Content: "original"},
		{EntityType: Comment, ID: comment, Version: 1, Content: "original"},
	}
	var inFlight sync.WaitGroup
	for _, job := range jobs {
		inFlight.Go(func() { pool.refine(job) })
	}
	for range jobs {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("old classification did not start")
		}
	}
	if _, err := queries.UpdatePostContent(ctx, sqlc.UpdatePostContentParams{
		ID: post.ID, UserID: owner.ID, Content: "edited", Sentiment: sqlc.SentimentTypeBAD,
	}); err != nil {
		t.Fatal(err)
	}
	if err := queries.UpdateComment(ctx, sqlc.UpdateCommentParams{
		ID: comment, Content: "edited", Sentiment: sqlc.SentimentTypeBAD,
	}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	inFlight.Wait()
	savedPost, err := queries.GetPostWithAuthorByID(ctx, post.ID)
	if err != nil || savedPost.ContentVersion != 2 || savedPost.Sentiment != sqlc.SentimentTypeBAD {
		t.Fatalf("post edit overwritten: version %d, sentiment %s, err=%v", savedPost.ContentVersion, savedPost.Sentiment, err)
	}
	savedComment, err := queries.GetComment(ctx, comment)
	if err != nil || savedComment.ContentVersion != 2 || savedComment.Sentiment != sqlc.SentimentTypeBAD {
		t.Fatalf("comment edit overwritten: version %d, sentiment %s, err=%v", savedComment.ContentVersion, savedComment.Sentiment, err)
	}
	for _, job := range jobs {
		job.Content, job.Version = "edited", 2
		pool.refine(job)
		event, err := sub.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Type    string                   `json:"type"`
			Payload realtime.SentimentUpdate `json:"payload"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &envelope); err != nil {
			t.Fatal(err)
		}
		kind := strings.ToUpper(string(job.EntityType)) + "_SENTIMENT_UPDATED"
		if envelope.Type != kind || envelope.Payload.EntityType != string(job.EntityType) ||
			envelope.Payload.EntityID != job.ID || envelope.Payload.ContentVersion != 2 ||
			envelope.Payload.Sentiment != string(lexicon.SentimentModerate) {
			t.Fatalf("unexpected refinement event: %+v", envelope)
		}
	}
}

func localCache(t *testing.T, db int) *redis.Client {
	t.Helper()
	url := os.Getenv("VALKEY_URL")
	if url == "" {
		url = "redis://127.0.0.1:6379/0"
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	options.DB = db
	cache := redis.NewClient(options)
	t.Cleanup(func() { _ = cache.Close() })
	return cache
}
