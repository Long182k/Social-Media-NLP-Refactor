package posts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/media"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/worker"
)

const (
	MaxPostContentRunes = 10000
	DefaultFeedLimit    = 10
	MaxFeedLimit        = 50
)

var (
	ErrValidation         = errors.New("validation error")
	ErrNotFound           = errors.New("post not found")
	ErrForbidden          = errors.New("forbidden")
	ErrServiceUnavailable = errors.New("service unavailable")
)

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrValidation
}

type Service struct {
	pool       *pgxpool.Pool
	q          *sqlc.Queries
	nlpMetrics prometheus.Histogram
	refinement *worker.Pool
}

func NewService(pool *pgxpool.Pool, nlpMetrics prometheus.Histogram, refinement *worker.Pool) *Service {
	return &Service{
		pool:       pool,
		q:          sqlc.New(pool),
		nlpMetrics: nlpMetrics,
		refinement: refinement,
	}
}

func (s *Service) scoreSentiment(content string) sqlc.SentimentType {
	start := time.Now()
	sent := lexicon.Score(content)
	if s.nlpMetrics != nil {
		s.nlpMetrics.Observe(time.Since(start).Seconds())
	}
	return sqlc.SentimentType(sent)
}

func (s *Service) CreatePost(ctx context.Context, userID uuid.UUID, groupID pgtype.UUID, input PostInput) (*PostDTO, error) {
	content := strings.TrimSpace(input.Content)
	if err := validatePostInput(content, len(input.MediaIDs)); err != nil {
		return nil, err
	}
	sentiment := s.scoreSentiment(content)
	var result *PostDTO
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := s.q.WithTx(tx)
		if groupID.Valid {
			member, err := queries.CanPostToGroup(ctx, sqlc.CanPostToGroupParams{
				GroupID: uuid.UUID(groupID.Bytes), UserID: userID,
			})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && !member) {
				return ErrForbidden
			}
			if err != nil {
				return err
			}
		}
		created, err := queries.CreatePost(ctx, sqlc.CreatePostParams{
			UserID: userID, GroupID: groupID, Content: content, Sentiment: sentiment,
		})
		if err != nil {
			return err
		}
		if err := setPostAttachments(ctx, queries, created.ID, userID, input.MediaIDs); err != nil {
			return err
		}
		result, err = readPost(ctx, queries, created.ID)
		return err
	})
	if err == nil {
		s.refinement.Offer(worker.Job{EntityType: worker.Post, ID: result.ID, Version: result.ContentVersion, Content: content})
	}
	return result, postStoreError(err)
}

func (s *Service) ListPosts(ctx context.Context, viewerID uuid.UUID, groupID, authorID pgtype.UUID, cursorStr string, limit int, search string) (*FeedResponse, error) {
	if limit <= 0 {
		limit = DefaultFeedLimit
	}
	if limit > MaxFeedLimit {
		return nil, &ValidationError{Message: "limit must be between 1 and 50"}
	}

	escapedSearch := escapeILIKE(strings.TrimSpace(search))
	fetchLimit := int32(limit + 1)

	items := make([]PostDTO, 0, limit)
	var hasMore bool

	if strings.TrimSpace(cursorStr) == "" {
		rows, err := s.q.ListPostsFirstPage(ctx, sqlc.ListPostsFirstPageParams{
			GroupID:   groupID,
			AuthorID:  authorID,
			Search:    escapedSearch,
			PageLimit: fetchLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
		if len(rows) > limit {
			hasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			items = append(items, postDTOFromFirstPageRow(row))
		}
	} else {
		cur, err := DecodeCursor(cursorStr)
		if err != nil {
			return nil, &ValidationError{Message: "invalid cursor"}
		}
		rows, err := s.q.ListPostsAfterCursor(ctx, sqlc.ListPostsAfterCursorParams{
			GroupID:         groupID,
			AuthorID:        authorID,
			Search:          escapedSearch,
			CursorCreatedAt: pgtype.Timestamptz{Time: cur.CreatedAt.UTC(), Valid: true},
			CursorID:        cur.ID,
			PageLimit:       fetchLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
		}
		if len(rows) > limit {
			hasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			items = append(items, postDTOFromCursorRow(row))
		}
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		enc := EncodeCursor(last.CreatedAt, last.ID)
		nextCursor = &enc
	}

	if err := loadPostAttachments(ctx, s.q, items); err != nil {
		return nil, postStoreError(err)
	}
	if err := loadViewerInteractions(ctx, s.q, viewerID, items); err != nil {
		return nil, postStoreError(err)
	}
	return &FeedResponse{
		Data:       items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) GetPost(ctx context.Context, postID, viewerID uuid.UUID) (*PostDTO, error) {
	post, err := readPost(ctx, s.q, postID)
	if err != nil {
		return nil, postStoreError(err)
	}
	result := []PostDTO{*post}
	if err := loadViewerInteractions(ctx, s.q, viewerID, result); err != nil {
		return nil, postStoreError(err)
	}
	return &result[0], nil
}

func (s *Service) UpdatePost(ctx context.Context, postID, callerUserID uuid.UUID, callerRole string, input PostInput) (*PostDTO, error) {
	content := strings.TrimSpace(input.Content)
	var result *PostDTO
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := s.q.WithTx(tx)
		ownerID, err := queries.LockPostForInteraction(ctx, postID)
		if err != nil {
			return err
		}
		if ownerID != callerUserID && callerRole != "ADMIN" {
			return ErrForbidden
		}
		mediaCount := len(input.MediaIDs)
		if content == "" && input.MediaIDs == nil {
			attachments, err := queries.ListPostAttachments(ctx, []uuid.UUID{postID})
			if err != nil {
				return err
			}
			mediaCount = len(attachments)
		}
		if err := validatePostInput(content, mediaCount); err != nil {
			return err
		}
		if _, err := queries.UpdatePostContent(ctx, sqlc.UpdatePostContentParams{
			ID: postID, UserID: ownerID, Content: content, Sentiment: s.scoreSentiment(content),
		}); err != nil {
			return err
		}
		if input.MediaIDs != nil {
			if err := queries.ClearPostAttachments(ctx, pgtype.UUID{Bytes: postID, Valid: true}); err != nil {
				return err
			}
			if err := setPostAttachments(ctx, queries, postID, callerUserID, input.MediaIDs); err != nil {
				return err
			}
		}
		result, err = readPost(ctx, queries, postID)
		return err
	})
	if err == nil {
		s.refinement.Offer(worker.Job{EntityType: worker.Post, ID: result.ID, Version: result.ContentVersion, Content: content})
	}
	return result, postStoreError(err)
}

func validatePostInput(content string, mediaCount int) error {
	if content == "" && mediaCount == 0 {
		return &ValidationError{Message: "post content or an attachment is required"}
	}
	if utf8.RuneCountInString(content) > MaxPostContentRunes {
		return &ValidationError{Message: "post content exceeds 10000 characters"}
	}
	return nil
}

func setPostAttachments(ctx context.Context, queries *sqlc.Queries, postID, ownerID uuid.UUID, mediaIDs []uuid.UUID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	if err := media.VerifyAttachments(ctx, queries, ownerID, sqlc.MediaVisibilityPUBLIC, mediaIDs); err != nil {
		return err
	}
	return queries.AddPostAttachments(ctx, sqlc.AddPostAttachmentsParams{
		PostID: pgtype.UUID{Bytes: postID, Valid: true}, MediaIds: mediaIDs,
	})
}

func readPost(ctx context.Context, queries *sqlc.Queries, id uuid.UUID) (*PostDTO, error) {
	row, err := queries.GetPostWithAuthorByID(ctx, id)
	if err != nil {
		return nil, err
	}
	posts := []PostDTO{postDTOFromGetRow(row)}
	if err := loadPostAttachments(ctx, queries, posts); err != nil {
		return nil, err
	}
	return &posts[0], nil
}

func loadPostAttachments(ctx context.Context, queries *sqlc.Queries, posts []PostDTO) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(posts))
	byID := make(map[uuid.UUID]*PostDTO, len(posts))
	for i := range posts {
		posts[i].Attachments = []Attachment{}
		ids = append(ids, posts[i].ID)
		byID[posts[i].ID] = &posts[i]
	}
	attachments, err := queries.ListPostAttachments(ctx, ids)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		if attachment.MimeType != nil {
			post := byID[uuid.UUID(attachment.PostID.Bytes)]
			post.Attachments = append(post.Attachments, Attachment{
				MediaID: attachment.MediaID, MimeType: *attachment.MimeType,
			})
		}
	}
	return nil
}

func postStoreError(err error) error {
	switch {
	case err == nil, errors.Is(err, ErrForbidden), errors.Is(err, ErrValidation):
		return err
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.Is(err, media.ErrInvalid):
		return &ValidationError{Message: "attachments must be finalized public media"}
	case errors.Is(err, media.ErrForbidden):
		return ErrForbidden
	default:
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
}

func loadViewerInteractions(ctx context.Context, q *sqlc.Queries, viewerID uuid.UUID, posts []PostDTO) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(posts))
	byID := make(map[uuid.UUID]*PostDTO, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
		byID[posts[i].ID] = &posts[i]
	}
	rows, err := q.ListViewerPostInteractions(ctx, sqlc.ListViewerPostInteractionsParams{ViewerID: viewerID, PostIds: ids})
	if err != nil {
		return err
	}
	for _, row := range rows {
		byID[row.ID].Liked = row.Liked
		byID[row.ID].Bookmarked = row.Bookmarked
	}
	return nil
}

func (s *Service) DeletePost(ctx context.Context, postID, callerUserID uuid.UUID, callerRole string) error {
	owner, err := s.q.GetPostOwnerByID(ctx, postID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	if owner.UserID != callerUserID && strings.ToUpper(callerRole) != "ADMIN" {
		return ErrForbidden
	}
	if err := s.q.DeletePost(ctx, postID); err != nil {
		return fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	return nil
}

func escapeILIKE(s string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(s)
}
