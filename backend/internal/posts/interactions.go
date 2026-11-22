package posts

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
)

type interactionState struct {
	Liked         bool  `json:"liked"`
	Bookmarked    bool  `json:"bookmarked"`
	LikeCount     int64 `json:"likeCount"`
	BookmarkCount int64 `json:"bookmarkCount"`
}

func (h *Handler) interactions(w http.ResponseWriter, r *http.Request) {
	postID, ok := postRouteID(w, r)
	if !ok {
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	row, err := h.svc.q.GetPostInteractions(r.Context(), sqlc.GetPostInteractionsParams{PostID: postID, UserID: claims.UserID})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, interactionDTO(row))
}

func (h *Handler) toggleLike(w http.ResponseWriter, r *http.Request) {
	h.toggle(w, r, false)
}

func (h *Handler) toggleBookmark(w http.ResponseWriter, r *http.Request) {
	h.toggle(w, r, true)
}

func (h *Handler) toggle(w http.ResponseWriter, r *http.Request, bookmark bool) {
	postID, ok := postRouteID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	claims, _ := auth.ClaimsFromContext(ctx)
	userID := claims.UserID
	var result sqlc.GetPostInteractionsRow
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.svc.pool, func(tx pgx.Tx) error {
		q := h.svc.q.WithTx(tx)
		authorID, err := q.LockPostForInteraction(ctx, postID)
		if err != nil {
			return err
		}
		if bookmark {
			removed, err := q.DeleteBookmarkForPost(ctx, sqlc.DeleteBookmarkForPostParams{UserID: userID, PostID: postID})
			if err != nil {
				return err
			}
			text := "Removed a bookmark from your post"
			if removed == 0 {
				if _, err := q.CreateBookmark(ctx, sqlc.CreateBookmarkParams{UserID: userID, PostID: postID}); err != nil {
					return err
				}
				text = "Bookmarked your post"
			}
			notice, err = notifications.Create(ctx, q, userID, authorID, postID, "BOOKMARK", text)
			if err != nil {
				return err
			}
		} else {
			removed, err := q.DeleteLike(ctx, sqlc.DeleteLikeParams{UserID: userID, PostID: postID})
			if err != nil {
				return err
			}
			if removed == 0 {
				if err := q.CreateLike(ctx, sqlc.CreateLikeParams{UserID: userID, PostID: postID}); err != nil {
					return err
				}
				if authorID != userID {
					notice, err = notifications.Create(ctx, q, userID, authorID, postID, "LIKE", "Liked your post")
					if err != nil {
						return err
					}
				}
			}
		}
		result, err = q.GetPostInteractions(ctx, sqlc.GetPostInteractionsParams{PostID: postID, UserID: userID})
		return err
	})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	if notice.ID != uuid.Nil {
		notifications.PublishCreated(ctx, h.publisher, notice)
	}
	httpserver.WriteJSON(w, http.StatusOK, interactionDTO(result))
}

func interactionDTO(row sqlc.GetPostInteractionsRow) interactionState {
	return interactionState{Liked: row.Liked, Bookmarked: row.Bookmarked, LikeCount: row.LikeCount, BookmarkCount: row.BookmarkCount}
}

type bookmarkDTO struct {
	ID         uuid.UUID `json:"id"`
	PostID     uuid.UUID `json:"postId"`
	Content    string    `json:"content"`
	Sentiment  string    `json:"sentiment"`
	AuthorID   uuid.UUID `json:"authorId"`
	AuthorName string    `json:"authorName"`
	AvatarURL  *string   `json:"avatarUrl"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (h *Handler) listBookmarks(w http.ResponseWriter, r *http.Request) {
	ts, cursorID, err := optionalCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		h.writePostError(w, err)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	rows, err := h.svc.q.ListBookmarks(r.Context(), sqlc.ListBookmarksParams{
		UserID: claims.UserID, CursorCreatedAt: ts, CursorID: cursorID, PageLimit: 21,
	})
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	var next *string
	if len(rows) > 20 {
		rows = rows[:20]
		last := rows[len(rows)-1]
		value := EncodeCursor(last.CreatedAt.Time, last.ID)
		next = &value
	}
	items := make([]bookmarkDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, bookmarkDTO{
			ID: row.ID, PostID: row.PostID, Content: row.Content, Sentiment: string(row.Sentiment),
			AuthorID: row.AuthorID, AuthorName: row.AuthorNickName, AvatarURL: row.AuthorAvatarUrl, CreatedAt: row.CreatedAt.Time,
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data       []bookmarkDTO `json:"data"`
		NextCursor *string       `json:"nextCursor"`
	}{items, next})
}

func (h *Handler) removeBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := postRouteID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	claims, _ := auth.ClaimsFromContext(ctx)
	owner, err := h.svc.q.GetBookmarkOwner(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err == nil && owner != claims.UserID {
		err = ErrForbidden
	}
	if err == nil {
		err = h.svc.q.DeleteOwnedBookmark(ctx, sqlc.DeleteOwnedBookmarkParams{ID: id, UserID: claims.UserID})
	}
	if err != nil {
		h.writePostError(w, postStoreError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func optionalCursor(raw string) (pgtype.Timestamptz, pgtype.UUID, error) {
	if raw == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	cursor, err := DecodeCursor(raw)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, &ValidationError{Message: "invalid cursor"}
	}
	return pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}, pgtype.UUID{Bytes: cursor.ID, Valid: true}, nil
}

func postRouteID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid resource id")
	}
	return id, err == nil && id != uuid.Nil
}
