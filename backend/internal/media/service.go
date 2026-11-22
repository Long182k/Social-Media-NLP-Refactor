package media

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/config"
)

const (
	MaxUploadSize = 5 * 1024 * 1024
	uploadTTL     = 5 * time.Minute
	downloadTTL   = 2 * time.Minute
)

var (
	ErrInvalid   = errors.New("invalid media type, size, or visibility")
	ErrNotFound  = errors.New("media not found")
	ErrForbidden = errors.New("media access denied")
	ErrConflict  = errors.New("media cannot be finalized in its current state")
)

type Service struct {
	queries       *sqlc.Queries
	client        *s3.Client
	presigner     *s3.PresignClient
	bucket        string
	publicBaseURL string
}

func NewService(pool *pgxpool.Pool, cfg config.S3Config) (*Service, error) {
	endpoint, err := parseEndpoint("S3_ENDPOINT", cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	presignEndpoint := endpoint
	if cfg.PresignEndpoint != "" {
		presignEndpoint, err = parseEndpoint("S3_PRESIGN_ENDPOINT", cfg.PresignEndpoint)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Region == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("S3_REGION, S3_BUCKET, S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	publicBaseURL := strings.TrimRight(cfg.PublicBaseURL, "/")
	if publicBaseURL == "" {
		if cfg.UsePathStyle {
			publicBaseURL = strings.TrimRight(presignEndpoint.String(), "/") + "/" + url.PathEscape(cfg.Bucket)
		} else {
			publicEndpoint := *presignEndpoint
			publicEndpoint.Host = cfg.Bucket + "." + presignEndpoint.Host
			publicBaseURL = strings.TrimRight(publicEndpoint.String(), "/")
		}
	}
	if _, err := parseEndpoint("S3_PUBLIC_BASE_URL", publicBaseURL); err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(aws.Config{
		Region:      cfg.Region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
		HTTPClient:  &http.Client{Timeout: 15 * time.Second},
	}, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
		options.UsePathStyle = cfg.UsePathStyle
	})
	presigner := s3.NewPresignClient(client)
	if cfg.PresignEndpoint != "" {
		signingOptions := client.Options()
		signingOptions.BaseEndpoint = aws.String(presignEndpoint.String())
		presigner = s3.NewPresignClient(s3.New(signingOptions))
	}
	return &Service{
		queries: sqlc.New(pool), client: client, presigner: presigner,
		bucket: cfg.Bucket, publicBaseURL: publicBaseURL,
	}, nil
}

func parseEndpoint(name, raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("%s must be an HTTP or HTTPS URL", name)
	}
	return endpoint, nil
}

type PresignRequest struct {
	MimeType   string               `json:"mimeType"`
	SizeBytes  int64                `json:"sizeBytes"`
	Visibility sqlc.MediaVisibility `json:"visibility"`
}

type Upload struct {
	MediaID    uuid.UUID   `json:"mediaId"`
	UploadURL  string      `json:"uploadUrl"`
	StagingKey string      `json:"stagingKey"`
	Headers    http.Header `json:"headers"`
}

type ReadyMedia struct {
	MediaID      uuid.UUID            `json:"mediaId"`
	MimeType     string               `json:"mimeType"`
	SizeBytes    int64                `json:"sizeBytes"`
	Visibility   sqlc.MediaVisibility `json:"visibility"`
	FinalizedKey string               `json:"finalizedKey"`
	URL          string               `json:"url,omitempty"`
}

func (s *Service) Presign(ctx context.Context, ownerID uuid.UUID, req PresignRequest) (Upload, error) {
	if !allowedMIME(req.MimeType) || req.SizeBytes <= 0 || req.SizeBytes > MaxUploadSize ||
		(req.Visibility != sqlc.MediaVisibilityPUBLIC && req.Visibility != sqlc.MediaVisibilityPRIVATE) {
		return Upload{}, ErrInvalid
	}
	id := uuid.New()
	stagingKey := "uploads/pending/" + id.String()
	signed, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(stagingKey),
		ContentType: aws.String(req.MimeType), ContentLength: aws.Int64(req.SizeBytes),
	}, s3.WithPresignExpires(uploadTTL))
	if err != nil {
		return Upload{}, fmt.Errorf("sign staging PUT: %w", err)
	}
	_, err = s.queries.CreateMediaObject(ctx, sqlc.CreateMediaObjectParams{
		ID: id, OwnerID: ownerID, StagingKey: stagingKey, DeclaredMime: req.MimeType,
		DeclaredSize: req.SizeBytes, Visibility: req.Visibility,
	})
	if err != nil {
		return Upload{}, fmt.Errorf("record pending upload: %w", err)
	}
	headers := signed.SignedHeader.Clone()
	headers.Del("Host")
	headers.Del("Content-Length")
	headers.Set("Content-Type", req.MimeType)
	return Upload{MediaID: id, UploadURL: signed.URL, StagingKey: stagingKey, Headers: headers}, nil
}

func (s *Service) Finalize(ctx context.Context, ownerID, id uuid.UUID) (ReadyMedia, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	lease := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	object, err := s.queries.ClaimMediaForFinalization(ctx, sqlc.ClaimMediaForFinalizationParams{
		ID: id, LeaseToken: lease, OwnerID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReadyMedia{}, s.finalizationConflict(ctx, ownerID, id)
	}
	if err != nil {
		return ReadyMedia{}, fmt.Errorf("claim media finalization: %w", err)
	}

	digest, etag, err := s.verifyUpload(ctx, object)
	if err != nil {
		s.reject(ctx, object, lease, "")
		return ReadyMedia{}, err
	}
	finalKey := "media/verified/" + strings.ToLower(string(object.Visibility)) + "/" + object.ID.String() + "/" + digest
	count, err := s.queries.SaveVerifiedMedia(ctx, sqlc.SaveVerifiedMediaParams{
		ID: id, FinalizingLeaseToken: lease, FinalizedKey: &finalKey, Sha256: &digest,
		VerifiedMime: &object.DeclaredMime, VerifiedSize: &object.DeclaredSize,
	})
	if err != nil {
		s.reject(ctx, object, lease, finalKey)
		return ReadyMedia{}, fmt.Errorf("record verified metadata: %w", err)
	}
	if count != 1 {
		return ReadyMedia{}, ErrConflict
	}

	_, err = s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(finalKey),
		CopySource: aws.String(url.PathEscape(s.bucket + "/" + object.StagingKey)), CopySourceIfMatch: aws.String(etag),
	})
	if err != nil {
		s.reject(ctx, object, lease, finalKey)
		return ReadyMedia{}, storageError("copy verified upload", err)
	}
	count, err = s.queries.MarkMediaReady(ctx, sqlc.MarkMediaReadyParams{ID: id, FinalizingLeaseToken: lease})
	if err != nil || count != 1 {
		// A dropped database reply can hide a successful commit. Rejection
		// deletes only an object that is confirmed to be in REJECTED state.
		s.reject(ctx, object, lease, finalKey)
		return ReadyMedia{}, ErrConflict
	}
	cleanupCtx, stopCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stopCleanup()
	_ = s.deleteObject(cleanupCtx, object.StagingKey)

	result := ReadyMedia{
		MediaID: id, MimeType: object.DeclaredMime, SizeBytes: object.DeclaredSize,
		Visibility: object.Visibility, FinalizedKey: finalKey,
	}
	if object.Visibility == sqlc.MediaVisibilityPUBLIC {
		result.URL = s.publicBaseURL + "/" + finalKey
	}
	return result, nil
}

func (s *Service) verifyUpload(ctx context.Context, object sqlc.MediaObject) (string, string, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(object.StagingKey),
	})
	if err != nil {
		return "", "", storageError("inspect staged upload", err)
	}
	etag := aws.ToString(head.ETag)
	if aws.ToInt64(head.ContentLength) != object.DeclaredSize || etag == "" {
		return "", "", ErrInvalid
	}
	staged, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(object.StagingKey), IfMatch: aws.String(etag),
	})
	if err != nil {
		return "", "", storageError("read staged upload", err)
	}
	defer staged.Body.Close()
	body := bufio.NewReader(io.LimitReader(staged.Body, MaxUploadSize+1))
	prefix, err := body.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("read upload header: %w", err)
	}
	if http.DetectContentType(prefix) != object.DeclaredMime {
		return "", "", ErrInvalid
	}
	hash := sha256.New()
	size, err := io.Copy(hash, body)
	if err != nil {
		return "", "", fmt.Errorf("hash staged upload: %w", err)
	}
	if size != object.DeclaredSize {
		return "", "", ErrInvalid
	}
	return hex.EncodeToString(hash.Sum(nil)), etag, nil
}

func (s *Service) finalizationConflict(ctx context.Context, ownerID, id uuid.UUID) error {
	object, err := s.getObject(ctx, id)
	if err != nil {
		return err
	}
	if object.OwnerID != ownerID {
		return ErrForbidden
	}
	return ErrConflict
}

func (s *Service) getObject(ctx context.Context, id uuid.UUID) (sqlc.MediaObject, error) {
	object, err := s.queries.GetMediaObject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.MediaObject{}, ErrNotFound
	}
	return object, err
}

func (s *Service) URL(ctx context.Context, userID, id uuid.UUID) (string, error) {
	object, err := s.getObject(ctx, id)
	if err != nil {
		return "", err
	}
	if object.Status != sqlc.MediaStatusREADY || object.FinalizedKey == nil {
		return "", ErrNotFound
	}
	if object.Visibility == sqlc.MediaVisibilityPUBLIC {
		return s.publicBaseURL + "/" + *object.FinalizedKey, nil
	}
	allowed, err := s.queries.HasPrivateMediaAccess(ctx, sqlc.HasPrivateMediaAccessParams{MediaID: id, UserID: userID})
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrForbidden
	}
	signed, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: object.FinalizedKey,
	}, s3.WithPresignExpires(downloadTTL))
	if err != nil {
		return "", fmt.Errorf("sign private media download: %w", err)
	}
	return signed.URL, nil
}

func (s *Service) ProfileImageURL(ctx context.Context, q *sqlc.Queries, ownerID, id uuid.UUID) (string, error) {
	if err := VerifyAttachments(ctx, q, ownerID, sqlc.MediaVisibilityPUBLIC, []uuid.UUID{id}); err != nil {
		return "", err
	}
	object, err := q.GetMediaObject(ctx, id)
	if err != nil {
		return "", err
	}
	if object.VerifiedMime == nil || !strings.HasPrefix(*object.VerifiedMime, "image/") || object.FinalizedKey == nil {
		return "", ErrInvalid
	}
	return s.publicBaseURL + "/" + *object.FinalizedKey, nil
}

func VerifyAttachments(ctx context.Context, queries *sqlc.Queries, ownerID uuid.UUID, visibility sqlc.MediaVisibility, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := queries.LockAttachableMedia(ctx, ids)
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return ErrInvalid
	}
	for _, row := range rows {
		if row.OwnerID != ownerID {
			return ErrForbidden
		}
		if row.Status != sqlc.MediaStatusREADY || row.Visibility != visibility {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Service) reject(ctx context.Context, object sqlc.MediaObject, lease pgtype.UUID, finalKey string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	count, err := s.queries.RejectMediaFinalization(ctx, sqlc.RejectMediaFinalizationParams{
		ID: object.ID, FinalizingLeaseToken: lease,
	})
	if err != nil {
		return
	}
	if count == 0 {
		current, err := s.getObject(ctx, object.ID)
		if err != nil || current.Status != sqlc.MediaStatusREJECTED {
			return
		}
	}
	if finalKey != "" {
		_ = s.deleteObject(ctx, finalKey)
	}
	_ = s.deleteObject(ctx, object.StagingKey)
}

func (s *Service) RunCleanup(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.cleanup(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("media cleanup failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) cleanup(ctx context.Context) error {
	expired, err := s.queries.RejectExpiredMedia(ctx)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, object := range expired {
		cleanupErrors = append(cleanupErrors, s.cleanKeys(ctx, object.StagingKey, object.FinalizedKey))
	}
	keys, err := s.queries.ListExpiredStagingKeys(ctx)
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	for _, object := range keys {
		var orphan *string
		if object.Status == sqlc.MediaStatusREJECTED {
			orphan = object.FinalizedKey
		}
		if err := s.cleanKeys(ctx, object.StagingKey, orphan); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		cleanupErrors = append(cleanupErrors, s.queries.MarkStagingCleaned(ctx, object.ID))
	}
	queued, err := s.queries.ListDueMediaKeys(ctx)
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	for _, key := range queued {
		if err := s.deleteObject(ctx, key); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		cleanupErrors = append(cleanupErrors, s.queries.RemoveMediaCleanupKey(ctx, key))
	}
	return errors.Join(cleanupErrors...)
}

func (s *Service) cleanKeys(ctx context.Context, stagingKey string, orphanKey *string) error {
	stagingErr := s.deleteObject(ctx, stagingKey)
	if orphanKey == nil {
		return stagingErr
	}
	return errors.Join(stagingErr, s.deleteObject(ctx, *orphanKey))
}

func (s *Service) deleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	return err
}

func allowedMIME(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}

func storageError(action string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed":
			return ErrConflict
		case "NotFound", "NoSuchKey":
			return ErrInvalid
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
