package posts

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/media"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/worker"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
)

type commentAuthor struct {
	ID        uuid.UUID `json:"id"`
	UserName  string    `json:"userName"`
	NickName  string    `json:"nickName"`
	AvatarURL *string   `json:"avatarUrl"`
}

type commentDTO struct {
	ID             uuid.UUID     `json:"id"`
	PostID         uuid.UUID     `json:"postId"`
	Content        string        `json:"content"`
	Sentiment      string        `json:"sentiment"`
	ContentVersion int32         `json:"contentVersion"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
	Author         commentAuthor `json:"author"`
	Attachments    []Attachment  `json:"attachments"`
}

func toCommentDTO(row sqlc.GetCommentRow) commentDTO {
	return commentDTO{
		ID: row.ID, PostID: row.PostID, Content: row.Content, Sentiment: string(row.Sentiment),
		ContentVersion: row.ContentVersion, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		Author: commentAuthor{ID: row.UserID, UserName: row.UserName, NickName: row.NickName, AvatarURL: row.AvatarUrl},
	}
}

func attachCommentMedia(ctx context.Context, q *sqlc.Queries, comments []commentDTO) error {
	if len(comments) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(comments))
	byID := make(map[uuid.UUID]*commentDTO, len(comments))
	for i := range comments {
		comments[i].Attachments = []Attachment{}
		ids = append(ids, comments[i].ID)
		byID[comments[i].ID] = &comments[i]
	}
	rows, err := q.ListCommentAttachments(ctx, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.MimeType != nil {
			item := byID[uuid.UUID(row.CommentID.Bytes)]
			item.Attachments = append(item.Attachments, Attachment{MediaID: row.MediaID, MimeType: *row.MimeType})
		}
	}
	return nil
}

func readComment(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (commentDTO, error) {
	row, err := q.GetComment(ctx, id)
	if err != nil {
		return commentDTO{}, err
	}
	items := []commentDTO{toCommentDTO(row)}
	err = attachCommentMedia(ctx, q, items)
	return items[0], err
}

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	postID, err := uuid.Parse(r.URL.Query().Get("postId"))
	if err != nil {
		h.writePostError(w, &ValidationError{Message: "postId is required"})
		return
	}
	ts, cursorID, err := optionalCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		h.writePostError(w, err)
		return
	}
	rows, err := h.svc.q.ListComments(r.Context(), sqlc.ListCommentsParams{
		PostID: postID, CursorCreatedAt: ts, CursorID: cursorID, PageLimit: 51,
	})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	var next *string
	if len(rows) > 50 {
		rows = rows[:50]
		last := rows[len(rows)-1]
		value := EncodeCursor(last.CreatedAt.Time, last.ID)
		next = &value
	}
	items := make([]commentDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCommentDTO(sqlc.GetCommentRow(row)))
	}
	if err := attachCommentMedia(r.Context(), h.svc.q, items); err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data       []commentDTO `json:"data"`
		NextCursor *string      `json:"nextCursor"`
	}{items, next})
}

func (h *Handler) getComment(w http.ResponseWriter, r *http.Request) {
	id, ok := postRouteID(w, r)
	if !ok {
		return
	}
	result, err := readComment(r.Context(), h.svc.q, id)
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	h.writeComment(w, r, uuid.Nil)
}

func (h *Handler) updateComment(w http.ResponseWriter, r *http.Request) {
	id, ok := postRouteID(w, r)
	if ok {
		h.writeComment(w, r, id)
	}
}

func (h *Handler) writeComment(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	var input struct {
		PostInput
		PostID uuid.UUID `json:"postId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input); err != nil {
		h.writePostError(w, &ValidationError{Message: "invalid JSON request body"})
		return
	}
	create := id == uuid.Nil
	if create && input.PostID == uuid.Nil {
		h.writePostError(w, &ValidationError{Message: "postId is required"})
		return
	}
	content := strings.TrimSpace(input.Content)
	ctx := r.Context()
	claims, _ := auth.ClaimsFromContext(ctx)
	actor := claims.UserID
	var result commentDTO
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.svc.pool, func(tx pgx.Tx) error {
		q := h.svc.q.WithTx(tx)
		mediaCount := len(input.MediaIDs)
		if !create {
			owner, err := q.LockComment(ctx, id)
			if err != nil {
				return err
			}
			if owner != actor && claims.Role != "ADMIN" {
				return ErrForbidden
			}
			if content == "" && input.MediaIDs == nil {
				existing, err := q.ListCommentAttachments(ctx, []uuid.UUID{id})
				if err != nil {
					return err
				}
				mediaCount = len(existing)
			}
		}
		if err := validatePostInput(content, mediaCount); err != nil {
			return err
		}
		sentiment := h.svc.scoreSentiment(content)
		if create {
			parent, err := q.GetPostOwnerByID(ctx, input.PostID)
			if err != nil {
				return err
			}
			id, err = q.CreateComment(ctx, sqlc.CreateCommentParams{
				UserID: actor, PostID: input.PostID, Content: content, Sentiment: sentiment,
			})
			if err != nil {
				return err
			}
			if parent.UserID != actor {
				notice, err = notifications.Create(ctx, q, actor, parent.UserID, input.PostID, "COMMENT", "Commented on your post")
				if err != nil {
					return err
				}
			}
		} else if err := q.UpdateComment(ctx, sqlc.UpdateCommentParams{ID: id, Content: content, Sentiment: sentiment}); err != nil {
			return err
		}
		if input.MediaIDs != nil {
			if err := media.VerifyAttachments(ctx, q, actor, sqlc.MediaVisibilityPUBLIC, input.MediaIDs); err != nil {
				return err
			}
			commentID := pgtype.UUID{Bytes: id, Valid: true}
			if err := q.ClearCommentAttachments(ctx, commentID); err != nil {
				return err
			}
			if err := q.AddCommentAttachments(ctx, sqlc.AddCommentAttachmentsParams{CommentID: commentID, MediaIds: input.MediaIDs}); err != nil {
				return err
			}
		}
		var err error
		result, err = readComment(ctx, q, id)
		return err
	})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	if notice.ID != uuid.Nil {
		notifications.PublishCreated(ctx, h.publisher, notice)
	}
	h.svc.refinement.Offer(worker.Job{EntityType: worker.Comment, ID: result.ID, Version: result.ContentVersion, Content: content})
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpserver.WriteJSON(w, status, result)
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	id, ok := postRouteID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	claims, _ := auth.ClaimsFromContext(ctx)
	err := pgx.BeginFunc(ctx, h.svc.pool, func(tx pgx.Tx) error {
		q := h.svc.q.WithTx(tx)
		owner, err := q.LockComment(ctx, id)
		if err != nil {
			return err
		}
		if owner != claims.UserID && claims.Role != "ADMIN" {
			return ErrForbidden
		}
		return q.DeleteComment(ctx, id)
	})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
