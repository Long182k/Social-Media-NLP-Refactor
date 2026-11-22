package community

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/media"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

var (
	errForbidden = errors.New("resource management permission required")
	errConflict  = errors.New("membership is not in the requested state")
)

type Handler struct {
	pool      *pgxpool.Pool
	queries   *sqlc.Queries
	auth      *auth.Service
	publisher realtime.Publisher
}

func NewHandler(pool *pgxpool.Pool, authService *auth.Service, publisher realtime.Publisher) *Handler {
	return &Handler{pool: pool, queries: sqlc.New(pool), auth: authService, publisher: publisher}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Get("/groups", h.listGroups)
		r.Post("/groups", h.createGroup)
		r.Get("/groups/{groupID}", h.getGroup)
		r.Patch("/groups/{groupID}", h.updateGroup)
		r.Delete("/groups/{groupID}", h.deleteGroup)
		r.Post("/groups/{groupID}/join", h.joinGroup)
		r.Delete("/groups/{groupID}/membership", h.leaveGroup)
		r.Get("/groups/{groupID}/join-requests", h.groupRequests)
		r.Post("/groups/{groupID}/approve/{userID}", h.approveGroup)
		r.Post("/groups/{groupID}/reject/{userID}", h.rejectGroup)
	})
	r.Route("/events", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Get("/", h.listEvents)
		r.Post("/", h.createEvent)
		r.Get("/trending/top", h.trendingEvents)
		r.Get("/category/{category}", h.categoryEvents)
		r.Get("/all/discover", h.discoverEvents)
		r.Get("/all/my-events", h.myEvents)
		r.Get("/{eventID}", h.getEvent)
		r.Patch("/{eventID}", h.updateEvent)
		r.Delete("/{eventID}", h.deleteEvent)
		r.Get("/{eventID}/attendees", h.attendees)
		r.Get("/{eventID}/requests", h.eventRequests)
		r.Post("/{eventID}/join", h.joinEvent)
		r.Post("/{eventID}/approve/{userID}", h.approveEvent)
		r.Post("/{eventID}/cancel/{userID}", h.cancelEvent)
	})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "resource not found")
	case errors.Is(err, errForbidden), errors.Is(err, media.ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, errConflict):
		httpserver.WriteError(w, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, media.ErrInvalid):
		clientError(w, "avatar must be finalized public media")
	default:
		h.publisher.Logger.ErrorContext(r.Context(), "community operation failed", slog.String("error", err.Error()))
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "community operation unavailable")
	}
}

func clientError(w http.ResponseWriter, message string) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
}

func requestID(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		clientError(w, "invalid "+param)
		return uuid.Nil, false
	}
	return id, true
}

func readInput(w http.ResponseWriter, r *http.Request, input any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(input); err != nil {
		clientError(w, "invalid JSON request body")
		return false
	}
	return true
}

func actor(r *http.Request) *auth.AuthClaims {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return claims
}

func validateAvatar(ctx context.Context, q *sqlc.Queries, ownerID uuid.UUID, avatar *uuid.UUID) (pgtype.UUID, error) {
	if avatar == nil {
		return pgtype.UUID{}, nil
	}
	if err := media.VerifyAttachments(ctx, q, ownerID, sqlc.MediaVisibilityPUBLIC, []uuid.UUID{*avatar}); err != nil {
		return pgtype.UUID{}, err
	}
	object, err := q.GetMediaObject(ctx, *avatar)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if object.VerifiedMime == nil || !strings.HasPrefix(*object.VerifiedMime, "image/") {
		return pgtype.UUID{}, media.ErrInvalid
	}
	return pgtype.UUID{Bytes: *avatar, Valid: true}, nil
}

func avatarID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func searchTerm(text string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(text))
}

type profile struct {
	ID        uuid.UUID `json:"id"`
	UserName  string    `json:"userName"`
	NickName  string    `json:"nickName"`
	AvatarURL *string   `json:"avatarUrl"`
	Role      string    `json:"role,omitempty"`
	Status    string    `json:"status,omitempty"`
}
