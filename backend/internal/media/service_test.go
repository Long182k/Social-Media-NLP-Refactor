package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/config"
)

type mediaFixture struct {
	service *Service
	pool    *pgxpool.Pool
	owner   uuid.UUID
	member  uuid.UUID
	guest   uuid.UUID
}

func testEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func newMediaFixture(t *testing.T) mediaFixture {
	t.Helper()
	ctx := context.Background()
	db := testEnv("DATABASE_URL", "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable")
	pool, err := pgxpool.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := config.S3Config{
		Endpoint:        testEnv("S3_ENDPOINT", "http://127.0.0.1:9000"),
		PresignEndpoint: os.Getenv("S3_PRESIGN_ENDPOINT"),
		Region:          testEnv("S3_REGION", "us-east-1"),
		Bucket:          testEnv("S3_BUCKET", "social-media-local"),
		AccessKey:       testEnv("S3_ACCESS_KEY", "minioadmin"),
		SecretKey:       testEnv("S3_SECRET_KEY", "minioadmin"),
		UsePathStyle:    true,
	}
	service, err := NewService(pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Integration tests use the local bucket. Hosted verification uses its own gate.
	if !strings.HasPrefix(cfg.Endpoint, "http://127.0.0.1:") && !strings.HasPrefix(cfg.Endpoint, "http://localhost:") {
		t.Fatal("media test requires a loopback S3 endpoint")
	}
	f := mediaFixture{service: service, pool: pool, owner: uuid.New(), member: uuid.New(), guest: uuid.New()}
	for _, id := range []uuid.UUID{f.owner, f.member, f.guest} {
		name := "media-test-" + id.String()
		_, err := pool.Exec(ctx, `
INSERT INTO users (id, user_name, nick_name, email, password_hash)
VALUES ($1, $2, $2, $3, 'integration-only')`, id, name, name+"@example.test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
			if err != nil {
				t.Error(err)
			}
		})
	}
	return f
}

func (f mediaFixture) stage(t *testing.T, content []byte, visibility sqlc.MediaVisibility) Upload {
	t.Helper()
	ctx := context.Background()
	upload, err := f.service.Presign(ctx, f.owner, PresignRequest{
		MimeType: "image/png", SizeBytes: int64(len(content)), Visibility: visibility,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		object, err := f.service.getObject(ctx, upload.MediaID)
		if err != nil {
			t.Error(err)
			return
		}
		if err := f.service.cleanKeys(ctx, object.StagingKey, object.FinalizedKey); err != nil {
			t.Error(err)
		}
	})
	putStaged(t, upload, content)
	return upload
}

func putStaged(t *testing.T, upload Upload, content []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, upload.UploadURL, bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	request.Header = upload.Headers.Clone()
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		t.Fatalf("staged PUT status = %d: %s", response.StatusCode, body)
	}
}

func assertServedMedia(t *testing.T, url string, expected []byte) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	actual, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(actual, expected) {
		t.Fatalf("unexpected served media: status %d, equal %t, error %v", response.StatusCode, bytes.Equal(actual, expected), err)
	}
}

func assertObjectRemoved(t *testing.T, service *Service, key string) {
	t.Helper()
	_, err := service.client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(service.bucket), Key: aws.String(key),
	})
	if !errors.Is(storageError("test HEAD", err), ErrInvalid) {
		t.Fatalf("expected object %q removed, got %v", key, err)
	}
}

func TestMinIOConcurrentFinalizeAndStagingCleanup(t *testing.T) {
	ctx := context.Background()
	f := newMediaFixture(t)
	original := []byte("\x89PNG\r\n\x1a\noriginal")
	replacement := []byte("\x89PNG\r\n\x1a\nreplaced")
	upload := f.stage(t, original, sqlc.MediaVisibilityPUBLIC)
	start := make(chan struct{})
	type outcome struct {
		media ReadyMedia
		err   error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			media, err := f.service.Finalize(ctx, f.owner, upload.MediaID)
			results <- outcome{media, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, ErrConflict) {
		t.Fatalf("expected one successful finalize and one conflict, got %v and %v", first.err, second.err)
	}
	assertServedMedia(t, first.media.URL, original)
	putStaged(t, upload, replacement)
	assertServedMedia(t, first.media.URL, original)
	if _, err := f.pool.Exec(ctx, "UPDATE media_objects SET created_at = NOW() - INTERVAL '6 minutes' WHERE id = $1", upload.MediaID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	assertObjectRemoved(t, f.service, upload.StagingKey)
	assertServedMedia(t, first.media.URL, original)

	err := VerifyAttachments(ctx, f.service.queries, f.owner, sqlc.MediaVisibilityPRIVATE, []uuid.UUID{upload.MediaID})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected public media rejected for chat, got %v", err)
	}
}

func TestMinIOSeparateBrowserSigningEndpoint(t *testing.T) {
	endpoint, err := url.Parse(testEnv("S3_ENDPOINT", "http://127.0.0.1:9000"))
	if err != nil {
		t.Fatal(err)
	}
	browser := *endpoint
	if endpoint.Hostname() == "localhost" {
		browser.Host = net.JoinHostPort("127.0.0.1", endpoint.Port())
	} else {
		browser.Host = net.JoinHostPort("localhost", endpoint.Port())
	}
	t.Setenv("S3_PRESIGN_ENDPOINT", browser.String())
	f := newMediaFixture(t)
	ctx := context.Background()
	contents := []byte("\x89PNG\r\n\x1a\nseparate-browser-host")
	upload := f.stage(t, contents, sqlc.MediaVisibilityPUBLIC)
	if !strings.HasPrefix(upload.UploadURL, browser.String()+"/") {
		t.Fatal("the browser PUT should be signed using its own origin")
	}
	ready, err := f.service.Finalize(ctx, f.owner, upload.MediaID)
	if err != nil || !strings.HasPrefix(ready.URL, browser.String()+"/") {
		t.Fatalf("server finalize and browser delivery failed: %v", err)
	}
	assertServedMedia(t, ready.URL, contents)
}

func TestMinIOSpoofedUploadAndPrivateAccess(t *testing.T) {
	ctx := context.Background()
	f := newMediaFixture(t)
	invalid := f.stage(t, []byte("plain-text"), sqlc.MediaVisibilityPRIVATE)
	if _, err := f.service.Finalize(ctx, f.owner, invalid.MediaID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid MIME, got %v", err)
	}
	assertObjectRemoved(t, f.service, invalid.StagingKey)

	content := []byte("\x89PNG\r\n\x1a\nprivate")
	upload := f.stage(t, content, sqlc.MediaVisibilityPRIVATE)
	result, err := f.service.Finalize(ctx, f.owner, upload.MediaID)
	if err != nil || result.URL != "" {
		t.Fatalf("expected private finalization without a public URL, got %v", err)
	}
	roomID, messageID := uuid.New(), uuid.New()
	if _, err := f.pool.Exec(ctx, "INSERT INTO chat_rooms (id, creator_id, name, type, next_seq) VALUES ($1, $2, 'media test', 'GROUP', 1)", roomID, f.owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(ctx, "DELETE FROM chat_rooms WHERE id = $1", roomID); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.pool.Exec(ctx, "INSERT INTO chat_participants (chat_room_id, user_id) VALUES ($1, $2), ($1, $3)", roomID, f.owner, f.member); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO chat_messages (id, chat_room_id, sender_id, room_seq, content) VALUES ($1, $2, $3, 1, '')", messageID, roomID, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAttachments(ctx, f.service.queries, f.owner, sqlc.MediaVisibilityPRIVATE, []uuid.UUID{upload.MediaID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "INSERT INTO attachments (media_id, chat_message_id) VALUES ($1, $2)", upload.MediaID, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.URL(ctx, f.guest, upload.MediaID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected access denied for non-member, got %v", err)
	}
	signedURL, err := f.service.URL(ctx, f.member, upload.MediaID)
	if err != nil {
		t.Fatal(err)
	}
	assertServedMedia(t, signedURL, content)
}

type beforeCopyTransport struct {
	replace func() error
	once    sync.Once
	err     error
}

func (tr *beforeCopyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("X-Amz-Copy-Source") != "" {
		tr.once.Do(func() { tr.err = tr.replace() })
		if tr.err != nil {
			return nil, tr.err
		}
	}
	return http.DefaultTransport.RoundTrip(request)
}

func TestMinIOConditionalCopyAndExpiredLease(t *testing.T) {
	ctx := context.Background()
	f := newMediaFixture(t)
	content := []byte("\x89PNG\r\n\x1a\nfirst")
	upload := f.stage(t, content, sqlc.MediaVisibilityPRIVATE)
	originalClient := f.service.client
	cfg := originalClient.Options()
	cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second, Transport: &beforeCopyTransport{
		replace: func() error {
			_, err := originalClient.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(f.service.bucket), Key: aws.String(upload.StagingKey),
				Body: bytes.NewReader([]byte("\x89PNG\r\n\x1a\nother")),
			})
			return err
		},
	}}
	f.service.client = s3.New(cfg)
	if _, err := f.service.Finalize(ctx, f.owner, upload.MediaID); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conditional copy conflict, got %v", err)
	}
	f.service.client = originalClient
	assertObjectRemoved(t, f.service, upload.StagingKey)

	stale := f.stage(t, content, sqlc.MediaVisibilityPRIVATE)
	orphan := "media/verified/private/" + stale.MediaID.String() + "/orphan"
	if _, err := f.service.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(f.service.bucket), Key: aws.String(orphan), Body: bytes.NewReader(content),
	}); err != nil {
		t.Fatal(err)
	}
	lease := uuid.New()
	if _, err := f.pool.Exec(ctx, `
UPDATE media_objects
SET status = 'FINALIZING', finalizing_lease_token = $2, finalizing_started_at = NOW() - INTERVAL '2 minutes',
    created_at = NOW() - INTERVAL '6 minutes', finalized_key = $3
WHERE id = $1`, stale.MediaID, lease, orphan); err != nil {
		t.Fatal(err)
	}
	if err := f.service.cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	object, err := f.service.getObject(ctx, stale.MediaID)
	if err != nil || object.Status != sqlc.MediaStatusREJECTED {
		t.Fatalf("expected expired lease rejected, got %s, %v", object.Status, err)
	}
	assertObjectRemoved(t, f.service, stale.StagingKey)
	assertObjectRemoved(t, f.service, orphan)
}
