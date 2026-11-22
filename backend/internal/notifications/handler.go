package notifications

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

type Handler struct {
	queries   *sqlc.Queries
	auth      *auth.Service
	publisher realtime.Publisher
}

func NewHandler(pool *pgxpool.Pool, authService *auth.Service, publisher realtime.Publisher) *Handler {
	return &Handler{queries: sqlc.New(pool), auth: authService, publisher: publisher}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/notifications", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Get("/", h.list)
		r.Get("/unread-count", h.unreadCount)
		r.Patch("/read", h.readAll)
		r.Get("/{id}", h.get)
		r.Patch("/{id}/read", h.read)
		r.Delete("/{id}", h.remove)
	})
}

type Notification struct {
	ID           uuid.UUID  `json:"id"`
	UserNotifSeq int64      `json:"userNotifSeq"`
	SenderID     *uuid.UUID `json:"senderId,omitempty"`
	Type         string     `json:"type,omitempty"`
	Content      string     `json:"content,omitempty"`
	EntityID     *uuid.UUID `json:"entityId,omitempty"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	IsRead       *bool      `json:"isRead,omitempty"`
	Deleted      bool       `json:"deleted,omitempty"`
}

func Create(ctx context.Context, q *sqlc.Queries, sender, receiver, entity uuid.UUID, kind, content string) (sqlc.Notification, error) {
	return q.CreateNotification(ctx, sqlc.CreateNotificationParams{
		ReceiverID: receiver, SenderID: pgtype.UUID{Bytes: sender, Valid: sender != uuid.Nil},
		EntityID: pgtype.UUID{Bytes: entity, Valid: entity != uuid.Nil}, Type: kind, Content: content,
	})
}

func PublishCreated(ctx context.Context, publisher realtime.Publisher, row sqlc.Notification) {
	publisher.Publish(ctx, []uuid.UUID{row.ReceiverID}, "NOTIFICATION_CREATED", notificationDTO(row))
}

func notificationDTO(row sqlc.Notification) Notification {
	dto := Notification{ID: row.ID, UserNotifSeq: row.UserNotifSeq, Deleted: row.DeletedAt.Valid}
	if dto.Deleted {
		return dto
	}
	dto.Type = row.Type
	dto.Content = row.Content
	dto.IsRead = &row.IsRead
	dto.CreatedAt = &row.CreatedAt.Time
	if row.SenderID.Valid {
		sender := uuid.UUID(row.SenderID.Bytes)
		dto.SenderID = &sender
	}
	if row.EntityID.Valid {
		entity := uuid.UUID(row.EntityID.Bytes)
		dto.EntityID = &entity
	}
	return dto
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	ctx, receiver := r.Context(), receiverID(r)
	const pageSize = 50
	var rows []sqlc.Notification
	var err error
	if raw := r.URL.Query().Get("afterSeq"); raw != "" {
		seq, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || seq < 0 {
			httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "afterSeq must be a non-negative integer")
			return
		}
		rows, err = h.queries.ListNotificationsAfterSeq(ctx, sqlc.ListNotificationsAfterSeqParams{
			ReceiverID: receiver, AfterSeq: seq, PageLimit: pageSize + 1,
		})
	} else {
		rows, err = h.queries.ListRecentNotifications(ctx, sqlc.ListRecentNotificationsParams{
			ReceiverID: receiver, PageLimit: pageSize,
		})
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	items := make([]Notification, 0, len(rows))
	for _, row := range rows {
		items = append(items, notificationDTO(row))
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data    []Notification `json:"data"`
		HasMore bool           `json:"hasMore"`
	}{items, hasMore})
}

func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	count, err := h.queries.CountUnreadNotifications(r.Context(), receiverID(r))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]int64{"unreadCount": count})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, valid := notificationID(w, r)
	if !valid {
		return
	}
	row, err := h.queries.GetNotification(r.Context(), sqlc.GetNotificationParams{ID: id, ReceiverID: receiverID(r)})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, notificationDTO(row))
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	id, valid := notificationID(w, r)
	if !valid {
		return
	}
	row, err := h.queries.MarkNotificationRead(r.Context(), sqlc.MarkNotificationReadParams{ID: id, ReceiverID: receiverID(r)})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, notificationDTO(row))
}

func (h *Handler) readAll(w http.ResponseWriter, r *http.Request) {
	if err := h.queries.MarkAllNotificationsRead(r.Context(), receiverID(r)); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, valid := notificationID(w, r)
	if !valid {
		return
	}
	receiver := receiverID(r)
	row, err := h.queries.DeleteNotification(r.Context(), sqlc.DeleteNotificationParams{ID: id, ReceiverID: receiver})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.publisher.Publish(r.Context(), []uuid.UUID{receiver}, "NOTIFICATION_DELETED", notificationDTO(row))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "notification not found")
		return
	}
	h.publisher.Logger.ErrorContext(r.Context(), "notification operation failed", slog.String("error", err.Error()))
	httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "notification operation unavailable")
}

func notificationID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid notification id")
	}
	return id, err == nil
}

func receiverID(r *http.Request) uuid.UUID {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return claims.UserID
}
