package community

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
)

type eventDTO struct {
	ID            uuid.UUID  `json:"id"`
	CreatorID     uuid.UUID  `json:"creatorId"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatarMediaId"`
	EventDate     time.Time  `json:"eventDate"`
	Category      string     `json:"category"`
	Address       *string    `json:"address"`
	ViewerRole    string     `json:"viewerRole"`
	ViewerStatus  string     `json:"viewerStatus"`
	AttendeeCount int64      `json:"attendeeCount"`
}

func eventProjection(row sqlc.GetEventRow) eventDTO {
	return eventDTO{
		ID: row.ID, CreatorID: row.CreatorID, Name: row.Name, Description: row.Description,
		AvatarMediaID: avatarID(row.AvatarMediaID), EventDate: row.EventDate.Time, Address: row.Address,
		Category: row.Category, ViewerRole: row.ViewerRole, ViewerStatus: row.ViewerStatus, AttendeeCount: row.AttendeeCount,
	}
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	h.events(w, r, "", "")
}

func (h *Handler) trendingEvents(w http.ResponseWriter, r *http.Request) {
	h.events(w, r, "trending", "")
}

func (h *Handler) categoryEvents(w http.ResponseWriter, r *http.Request) {
	category := strings.ToUpper(chi.URLParam(r, "category"))
	if !validEventCategory(category) {
		clientError(w, "invalid event category")
		return
	}
	h.events(w, r, "", category)
}

func (h *Handler) myEvents(w http.ResponseWriter, r *http.Request) {
	h.events(w, r, "mine", "")
}

func (h *Handler) discoverEvents(w http.ResponseWriter, r *http.Request) {
	h.events(w, r, "discover", "")
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request, mode, category string) {
	query := r.URL.Query()
	var offset int64
	if raw := query.Get("offset"); raw != "" {
		var err error
		offset, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || offset < 0 || offset > 100000 {
			clientError(w, "invalid event offset")
			return
		}
	}
	rows, err := h.queries.ListEvents(r.Context(), sqlc.ListEventsParams{
		ViewerID: actor(r).UserID, Mode: mode, Category: category, Search: searchTerm(query.Get("q")), PageOffset: int32(offset),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	data := make([]eventDTO, 0, len(rows))
	for _, row := range rows {
		data = append(data, eventProjection(sqlc.GetEventRow(row)))
	}
	httpserver.WriteJSON(w, http.StatusOK, data)
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "eventID")
	if ok {
		h.writeEvent(w, r, id, http.StatusOK)
	}
}

func (h *Handler) writeEvent(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	row, err := h.queries.GetEvent(r.Context(), sqlc.GetEventParams{EventID: id, ViewerID: actor(r).UserID})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, eventProjection(row))
}

type eventInput struct {
	Name          *string    `json:"name"`
	Description   *string    `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatarMediaId"`
	EventDate     *time.Time `json:"eventDate"`
	Category      *string    `json:"category"`
	Address       *string    `json:"address"`
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	h.saveEvent(w, r, uuid.Nil)
}

func (h *Handler) updateEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "eventID")
	if ok {
		h.saveEvent(w, r, id)
	}
}

func (h *Handler) saveEvent(w http.ResponseWriter, r *http.Request, eventID uuid.UUID) {
	var input eventInput
	if !readInput(w, r, &input) {
		return
	}
	create := eventID == uuid.Nil
	if input.Name != nil {
		*input.Name = strings.TrimSpace(*input.Name)
	}
	if input.Category != nil {
		*input.Category = strings.ToUpper(strings.TrimSpace(*input.Category))
	}
	if (create && (input.Name == nil || input.Description == nil || input.EventDate == nil)) ||
		(input.Name != nil && (*input.Name == "" || utf8.RuneCountInString(*input.Name) > 100)) ||
		(input.Description != nil && (strings.TrimSpace(*input.Description) == "" || utf8.RuneCountInString(*input.Description) > 3000)) ||
		(input.Address != nil && utf8.RuneCountInString(*input.Address) > 300) ||
		(input.Category != nil && !validEventCategory(*input.Category)) ||
		(input.EventDate != nil && input.EventDate.IsZero()) {
		clientError(w, "invalid event details")
		return
	}
	category := "OTHER"
	if input.Category != nil {
		category = *input.Category
	}
	var eventDate pgtype.Timestamptz
	if input.EventDate != nil {
		eventDate = pgtype.Timestamptz{Time: input.EventDate.UTC(), Valid: true}
	}
	ctx, caller := r.Context(), actor(r)
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if !create {
			if err := requireEventManager(ctx, q, eventID, caller); err != nil {
				return err
			}
		}
		avatar, err := validateAvatar(ctx, q, caller.UserID, input.AvatarMediaID)
		if err != nil {
			return err
		}
		if create {
			eventID, err = q.CreateEvent(ctx, sqlc.CreateEventParams{
				CreatorID: caller.UserID, Name: *input.Name, Description: *input.Description,
				AvatarMediaID: avatar, EventDate: eventDate, Category: category, Address: input.Address,
			})
			if err != nil {
				return err
			}
			return q.CreateEventAdmin(ctx, sqlc.CreateEventAdminParams{EventID: eventID, UserID: caller.UserID})
		}
		return q.UpdateEvent(ctx, sqlc.UpdateEventParams{
			ID: eventID, Name: input.Name, Description: input.Description, AvatarMediaID: avatar,
			EventDate: eventDate, Category: input.Category, Address: input.Address,
		})
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	h.writeEvent(w, r, eventID, status)
}

func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "eventID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if err := requireEventManager(ctx, q, id, caller); err != nil {
			return err
		}
		_, err := q.DeleteEvent(ctx, id)
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) joinEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "eventID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		creator, err := q.LockEventOwner(ctx, id)
		if err != nil || creator == caller.UserID {
			return err
		}
		count, err := q.RequestEventAttendance(ctx, sqlc.RequestEventAttendanceParams{EventID: id, UserID: caller.UserID})
		if err == nil && count > 0 {
			notice, err = notifications.Create(ctx, q, caller.UserID, creator, id, "NEW_JOIN_EVENT_REQUEST", "Requested to join your event")
		}
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if notice.ID != uuid.Nil {
		notifications.PublishCreated(ctx, h.publisher, notice)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) attendees(w http.ResponseWriter, r *http.Request) {
	h.listAttendees(w, r, "ENROLL")
}

func (h *Handler) eventRequests(w http.ResponseWriter, r *http.Request) {
	h.listAttendees(w, r, "PENDING")
}

func (h *Handler) listAttendees(w http.ResponseWriter, r *http.Request, status string) {
	id, ok := requestID(w, r, "eventID")
	if !ok {
		return
	}
	ctx := r.Context()
	if status == "PENDING" {
		if err := requireEventManager(ctx, h.queries, id, actor(r)); err != nil {
			h.writeError(w, r, err)
			return
		}
	}
	rows, err := h.queries.ListEventAttendees(ctx, sqlc.ListEventAttendeesParams{EventID: id, Status: status})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	data := make([]profile, 0, len(rows))
	for _, row := range rows {
		data = append(data, profile{
			ID: row.ID, UserName: row.UserName, NickName: row.NickName, AvatarURL: row.AvatarUrl,
			Role: row.Role, Status: row.Status,
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, data)
}

func (h *Handler) approveEvent(w http.ResponseWriter, r *http.Request) {
	h.decideEvent(w, r, false)
}

func (h *Handler) cancelEvent(w http.ResponseWriter, r *http.Request) {
	h.decideEvent(w, r, true)
}

func (h *Handler) decideEvent(w http.ResponseWriter, r *http.Request, cancel bool) {
	eventID, ok := requestID(w, r, "eventID")
	if !ok {
		return
	}
	userID, ok := requestID(w, r, "userID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	role, state := "ATTENDEE", "ENROLL"
	kind, message := "APPROVED_JOIN_EVENT_REQUEST", "Approved your event request"
	if cancel {
		role, state = "PENDING_ATTENDEE", "CANCEL"
		kind, message = "REJECTED_JOIN_EVENT_REQUEST", "Cancelled your event attendance"
	}
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		creator, err := q.LockEventOwner(ctx, eventID)
		if err != nil {
			return err
		}
		if (!cancel || userID != caller.UserID) && creator != caller.UserID && caller.Role != "ADMIN" {
			return errForbidden
		}
		if userID == creator {
			return errConflict
		}
		count, err := q.DecideEventAttendance(ctx, sqlc.DecideEventAttendanceParams{
			Role: role, Status: state, EventID: eventID, UserID: userID,
		})
		if err != nil {
			return err
		}
		if count == 0 && (!cancel || userID != caller.UserID) {
			return errConflict
		}
		if count > 0 && userID != caller.UserID {
			notice, err = notifications.Create(ctx, q, caller.UserID, userID, eventID, kind, message)
		}
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if notice.ID != uuid.Nil {
		notifications.PublishCreated(ctx, h.publisher, notice)
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireEventManager(ctx context.Context, q *sqlc.Queries, eventID uuid.UUID, caller *auth.AuthClaims) error {
	creator, err := q.LockEventOwner(ctx, eventID)
	if err != nil || creator == caller.UserID || caller.Role == "ADMIN" {
		return err
	}
	return errForbidden
}

func validEventCategory(category string) bool {
	switch category {
	case "MUSIC", "SPORTS", "EDUCATION", "TECHNOLOGY", "FOOD", "ART", "BUSINESS", "HEALTH", "OTHER":
		return true
	default:
		return false
	}
}
