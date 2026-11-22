package posts

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

type Handler struct {
	svc       *Service
	authSvc   *auth.Service
	publisher realtime.Publisher
}

func NewHandler(svc *Service, authSvc *auth.Service, publisher realtime.Publisher) *Handler {
	return &Handler{svc: svc, authSvc: authSvc, publisher: publisher}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/posts", func(pr chi.Router) {
		pr.Use(auth.RequireAuth(h.authSvc))
		pr.Post("/", h.Create)
		pr.Get("/", h.List)
		pr.Get("/{id}", h.Get)
		pr.Patch("/{id}", h.Update)
		pr.Delete("/{id}", h.Delete)
		pr.Post("/{id}/like", h.toggleLike)
		pr.Get("/{id}/interactions", h.interactions)
	})
	r.Route("/bookmarks", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.authSvc))
		r.Get("/", h.listBookmarks)
		r.Post("/{id}", h.toggleBookmark)
		r.Delete("/{id}", h.removeBookmark)
	})
	r.Route("/comments", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.authSvc))
		r.Get("/", h.listComments)
		r.Post("/", h.createComment)
		r.Get("/{id}", h.getComment)
		r.Patch("/{id}", h.updateComment)
		r.Delete("/{id}", h.deleteComment)
	})
	r.With(auth.RequireAuth(h.authSvc)).Post("/groups/{groupID}/posts", h.Create)
	r.With(auth.RequireAuth(h.authSvc)).Get("/groups/{groupID}/posts", h.List)
	r.With(auth.RequireAuth(h.authSvc)).Get("/users/{userID}/posts", h.List)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	groupID, err := scopeID(r, "groupID")
	if err != nil {
		h.writePostError(w, err)
		return
	}
	var req PostInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	post, err := h.svc.CreatePost(r.Context(), claims.UserID, groupID, req)
	if err != nil {
		h.writePostError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, post)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	groupID, err := scopeID(r, "groupID")
	if err != nil {
		h.writePostError(w, err)
		return
	}
	authorID, err := scopeID(r, "userID")
	if err != nil {
		h.writePostError(w, err)
		return
	}
	limit := DefaultFeedLimit
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "limit must be a positive integer")
			return
		}
		limit = parsed
	}

	cursor := r.URL.Query().Get("cursor")
	q := r.URL.Query().Get("q")

	claims, _ := auth.ClaimsFromContext(r.Context())
	feed, err := h.svc.ListPosts(r.Context(), claims.UserID, groupID, authorID, cursor, limit, q)
	if err != nil {
		h.writePostError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, feed)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	postID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid post id")
		return
	}

	claims, _ := auth.ClaimsFromContext(r.Context())
	post, err := h.svc.GetPost(r.Context(), postID, claims.UserID)
	if err != nil {
		h.writePostError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	postID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid post id")
		return
	}

	var req PostInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON request body")
		return
	}

	post, err := h.svc.UpdatePost(r.Context(), postID, claims.UserID, claims.Role, req)
	if err != nil {
		h.writePostError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, post)
}

func scopeID(r *http.Request, parameter string) (pgtype.UUID, error) {
	raw := chi.URLParam(r, parameter)
	if raw == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, &ValidationError{Message: "invalid " + parameter}
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	postID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid post id")
		return
	}

	if err := h.svc.DeletePost(r.Context(), postID, claims.UserID, claims.Role); err != nil {
		h.writePostError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"message": "Post deleted successfully"})
}

func (h *Handler) writePostError(w http.ResponseWriter, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", valErr.Message)
		return
	}

	switch {
	case errors.Is(err, ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "post not found")
	case errors.Is(err, ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have permission to modify this post")
	case errors.Is(err, ErrServiceUnavailable):
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "posts service temporarily unavailable")
	default:
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
