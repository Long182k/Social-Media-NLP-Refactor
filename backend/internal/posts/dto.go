package posts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
)

type PostDTO struct {
	ID             uuid.UUID       `json:"id"`
	Content        string          `json:"content"`
	UserID         uuid.UUID       `json:"userId"`
	GroupID        *uuid.UUID      `json:"groupId,omitempty"`
	Sentiment      string          `json:"sentiment"`
	ContentVersion int32           `json:"contentVersion"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	Author         auth.PublicUser `json:"author"`
	LikeCount      int64           `json:"likeCount"`
	CommentCount   int64           `json:"commentCount"`
	BookmarkCount  int64           `json:"bookmarkCount"`
	Liked          bool            `json:"liked"`
	Bookmarked     bool            `json:"bookmarked"`
	Attachments    []Attachment    `json:"attachments"`
}

type Attachment struct {
	MediaID  uuid.UUID `json:"mediaId"`
	MimeType string    `json:"mimeType"`
}

type FeedResponse struct {
	Data       []PostDTO `json:"data"`
	NextCursor *string   `json:"nextCursor"`
	HasMore    bool      `json:"hasMore"`
}

type PostInput struct {
	Content  string      `json:"content"`
	MediaIDs []uuid.UUID `json:"mediaIds"`
}

type Cursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func EncodeCursor(createdAt time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(Cursor{
		CreatedAt: createdAt.UTC(),
		ID:        id,
	})
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(raw string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, errors.New("invalid cursor encoding")
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return Cursor{}, errors.New("invalid cursor payload")
	}
	if c.CreatedAt.IsZero() || c.ID == uuid.Nil {
		return Cursor{}, errors.New("incomplete cursor")
	}
	return c, nil
}

func pgTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func groupIDPtr(id pgtype.UUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	value := uuid.UUID(id.Bytes)
	return &value
}

func postDTOFromGetRow(r sqlc.GetPostWithAuthorByIDRow) PostDTO {
	return PostDTO{
		ID:             r.ID,
		Content:        r.Content,
		UserID:         r.UserID,
		GroupID:        groupIDPtr(r.GroupID),
		LikeCount:      r.LikeCount,
		CommentCount:   r.CommentCount,
		BookmarkCount:  r.BookmarkCount,
		Sentiment:      string(r.Sentiment),
		ContentVersion: r.ContentVersion,
		CreatedAt:      r.CreatedAt.Time.UTC(),
		UpdatedAt:      r.UpdatedAt.Time.UTC(),
		Author: auth.PublicUser{
			ID:           r.UserID,
			UserName:     r.AuthorUserName,
			NickName:     r.AuthorNickName,
			Email:        r.AuthorEmail,
			Role:         string(r.AuthorRole),
			AvatarURL:    r.AuthorAvatarUrl,
			CoverPageURL: r.AuthorCoverPageUrl,
			Bio:          r.AuthorBio,
			DateOfBirth:  pgTimePtr(r.AuthorDateOfBirth),
			IsActive:     r.AuthorIsActive,
			CreatedAt:    r.AuthorCreatedAt.Time.UTC(),
			LastLoginAt:  pgTimePtr(r.AuthorLastLoginAt),
		},
	}
}

func postDTOFromFirstPageRow(r sqlc.ListPostsFirstPageRow) PostDTO {
	return PostDTO{
		ID:             r.ID,
		Content:        r.Content,
		UserID:         r.UserID,
		GroupID:        groupIDPtr(r.GroupID),
		LikeCount:      r.LikeCount,
		CommentCount:   r.CommentCount,
		BookmarkCount:  r.BookmarkCount,
		Sentiment:      string(r.Sentiment),
		ContentVersion: r.ContentVersion,
		CreatedAt:      r.CreatedAt.Time.UTC(),
		UpdatedAt:      r.UpdatedAt.Time.UTC(),
		Author: auth.PublicUser{
			ID:           r.UserID,
			UserName:     r.AuthorUserName,
			NickName:     r.AuthorNickName,
			Email:        r.AuthorEmail,
			Role:         string(r.AuthorRole),
			AvatarURL:    r.AuthorAvatarUrl,
			CoverPageURL: r.AuthorCoverPageUrl,
			Bio:          r.AuthorBio,
			DateOfBirth:  pgTimePtr(r.AuthorDateOfBirth),
			IsActive:     r.AuthorIsActive,
			CreatedAt:    r.AuthorCreatedAt.Time.UTC(),
			LastLoginAt:  pgTimePtr(r.AuthorLastLoginAt),
		},
	}
}

func postDTOFromCursorRow(r sqlc.ListPostsAfterCursorRow) PostDTO {
	return PostDTO{
		ID:             r.ID,
		Content:        r.Content,
		UserID:         r.UserID,
		GroupID:        groupIDPtr(r.GroupID),
		LikeCount:      r.LikeCount,
		CommentCount:   r.CommentCount,
		BookmarkCount:  r.BookmarkCount,
		Sentiment:      string(r.Sentiment),
		ContentVersion: r.ContentVersion,
		CreatedAt:      r.CreatedAt.Time.UTC(),
		UpdatedAt:      r.UpdatedAt.Time.UTC(),
		Author: auth.PublicUser{
			ID:           r.UserID,
			UserName:     r.AuthorUserName,
			NickName:     r.AuthorNickName,
			Email:        r.AuthorEmail,
			Role:         string(r.AuthorRole),
			AvatarURL:    r.AuthorAvatarUrl,
			CoverPageURL: r.AuthorCoverPageUrl,
			Bio:          r.AuthorBio,
			DateOfBirth:  pgTimePtr(r.AuthorDateOfBirth),
			IsActive:     r.AuthorIsActive,
			CreatedAt:    r.AuthorCreatedAt.Time.UTC(),
			LastLoginAt:  pgTimePtr(r.AuthorLastLoginAt),
		},
	}
}
