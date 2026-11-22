package media

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
)

type Handler struct {
	service *Service
	auth    *auth.Service
	logger  *slog.Logger
}

func NewHandler(service *Service, authService *auth.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, auth: authService, logger: logger}
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Route("/media", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Post("/presign", h.presign)
		r.Post("/finalize", h.finalize)
		r.Get("/{id}/url", h.getURL)
	})
}

func (h *Handler) presign(w http.ResponseWriter, r *http.Request) {
	var request PresignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		h.writeError(w, r, ErrInvalid)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	result, err := h.service.Presign(r.Context(), claims.UserID, request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) finalize(w http.ResponseWriter, r *http.Request) {
	var request struct {
		MediaID uuid.UUID `json:"mediaId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil || request.MediaID == uuid.Nil {
		h.writeError(w, r, ErrInvalid)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	result, err := h.service.Finalize(r.Context(), claims.UserID, request.MediaID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) getURL(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, ErrInvalid)
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	link, err := h.service.URL(r.Context(), claims.UserID, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusOK, map[string]string{"url": link})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_MEDIA", err.Error())
	case errors.Is(err, ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, ErrConflict):
		httpserver.WriteError(w, http.StatusConflict, "MEDIA_CONFLICT", err.Error())
	default:
		h.logger.ErrorContext(r.Context(), "media operation failed", slog.String("error", err.Error()))
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "media operation unavailable")
	}
}
