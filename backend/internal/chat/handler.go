package chat

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

var errRoomAccess = errors.New("room membership required")

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
	r.Route("/chat/rooms", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Get("/", h.syncRooms)
		r.Get("/sync", h.syncRooms)
		r.Post("/", h.createRoom)
		r.Route("/{roomID}", func(r chi.Router) {
			r.Use(h.requireRoomMember)
			r.Get("/messages", h.listMessages)
			r.Post("/messages", h.sendMessage)
			r.Get("/shared", h.shared)
			r.Patch("/read", h.markRead)
		})
	})
}

type attachment struct {
	MessageID uuid.UUID `json:"messageId"`
	MediaID   uuid.UUID `json:"mediaId"`
	MimeType  string    `json:"mimeType"`
}

type message struct {
	ID          uuid.UUID    `json:"id"`
	RoomID      uuid.UUID    `json:"roomId"`
	RoomSeq     int64        `json:"roomSeq"`
	SenderID    *uuid.UUID   `json:"senderId,omitempty"`
	Content     string       `json:"content,omitempty"`
	CreatedAt   *time.Time   `json:"createdAt,omitempty"`
	Deleted     bool         `json:"deleted,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
}

type roomPreview struct {
	ID      uuid.UUID `json:"id"`
	Content string    `json:"content"`
	RoomSeq int64     `json:"roomSeq"`
}

type roomProfile struct {
	ID        uuid.UUID `json:"id"`
	UserName  string    `json:"userName"`
	NickName  string    `json:"nickName"`
	AvatarURL *string   `json:"avatarUrl"`
}

type roomSummary struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	LastSeq      int64         `json:"lastSeq"`
	LastReadSeq  int64         `json:"lastReadSeq"`
	LastMessage  *roomPreview  `json:"lastMessage"`
	UnreadCount  int64         `json:"unreadCount"`
	Participants []roomProfile `json:"participants"`
}

func (h *Handler) syncRooms(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.SyncChatRooms(r.Context(), caller(r))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	roomIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		roomIDs = append(roomIDs, row.ID)
	}
	profiles, err := h.queries.ListChatProfiles(r.Context(), roomIDs)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	byRoom := map[uuid.UUID][]roomProfile{}
	for _, profile := range profiles {
		byRoom[profile.ChatRoomID] = append(byRoom[profile.ChatRoomID], roomProfile{
			ID: profile.ID, UserName: profile.UserName, NickName: profile.NickName, AvatarURL: profile.AvatarUrl,
		})
	}
	rooms := make([]roomSummary, 0, len(rows))
	for _, row := range rows {
		summary := roomSummary{
			ID: row.ID, Name: row.Name, Type: row.Type, LastSeq: row.NextSeq, LastReadSeq: row.LastReadSeq,
			UnreadCount: row.UnreadCount, Participants: byRoom[row.ID],
		}
		if row.LastMessageSeq > 0 {
			summary.LastMessage = &roomPreview{ID: row.LastMessageID, Content: row.LastMessageContent, RoomSeq: row.LastMessageSeq}
		}
		rooms = append(rooms, summary)
	}
	httpserver.WriteJSON(w, http.StatusOK, rooms)
}

func (h *Handler) createRoom(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type           string      `json:"type"`
		Name           string      `json:"name"`
		ParticipantIDs []uuid.UUID `json:"participantIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	ctx := r.Context()
	userID := caller(r)
	input.Name = strings.TrimSpace(input.Name)
	if input.Type == "" {
		input.Type = "DIRECT"
	}
	if (input.Type != "DIRECT" && input.Type != "GROUP") || len(input.ParticipantIDs) == 0 ||
		len(input.ParticipantIDs) > 49 || (input.Type == "DIRECT" && len(input.ParticipantIDs) != 1) ||
		(input.Type == "GROUP" && input.Name == "") || utf8.RuneCountInString(input.Name) > 100 {
		badRequest(w, "invalid room name, type, or participants")
		return
	}
	ids := append([]uuid.UUID{userID}, input.ParticipantIDs...)
	slices.SortFunc(ids, func(a, b uuid.UUID) int {
		return strings.Compare(a.String(), b.String())
	})
	ids = slices.Compact(ids)
	if len(ids) != len(input.ParticipantIDs)+1 || ids[0] == uuid.Nil {
		badRequest(w, "participants must be distinct active users")
		return
	}
	var directKey *string
	if input.Type == "DIRECT" {
		key := ids[0].String() + ":" + ids[1].String()
		directKey = &key
		input.Name = "Direct message"
	}
	var roomID uuid.UUID
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		count, err := q.CountActiveChatUsers(ctx, ids)
		if err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return media.ErrInvalid
		}
		roomID, err = q.CreateChatRoom(ctx, sqlc.CreateChatRoomParams{
			CreatorID: dbUUID(userID), Name: input.Name, Type: input.Type, DirectKey: directKey,
		})
		if err != nil {
			return err
		}
		return q.AddChatParticipants(ctx, sqlc.AddChatParticipantsParams{RoomID: roomID, UserIds: ids})
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string]uuid.UUID{"id": roomID})
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var afterSeq int64
	if raw := query.Get("afterSeq"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			badRequest(w, "afterSeq must be a non-negative integer")
			return
		}
		afterSeq = value
	}
	const pageSize = 50
	rows, err := h.queries.ListChatMessagesAfterSeq(r.Context(), sqlc.ListChatMessagesAfterSeqParams{
		RoomID: roomID(r), AfterSeq: afterSeq, PageLimit: pageSize + 1,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	items, err := h.messagesWithMedia(r, h.queries, rows)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data    []message `json:"data"`
		HasMore bool      `json:"hasMore"`
	}{items, hasMore})
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content  string      `json:"content"`
		MediaIDs []uuid.UUID `json:"mediaIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	content := strings.TrimSpace(input.Content)
	if (content == "" && len(input.MediaIDs) == 0) || utf8.RuneCountInString(content) > 10000 || len(input.MediaIDs) > 10 {
		badRequest(w, "invalid message content or attachments")
		return
	}
	ctx, actor, id := r.Context(), caller(r), roomID(r)
	var result message
	var recipients []uuid.UUID
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		seq, err := q.AdvanceChatRoomSequence(ctx, sqlc.AdvanceChatRoomSequenceParams{RoomID: id, UserID: actor})
		if errors.Is(err, pgx.ErrNoRows) {
			return errRoomAccess
		}
		if err != nil {
			return err
		}
		if err := media.VerifyAttachments(ctx, q, actor, sqlc.MediaVisibilityPRIVATE, input.MediaIDs); err != nil {
			return err
		}
		row, err := q.CreateChatMessage(ctx, sqlc.CreateChatMessageParams{
			ChatRoomID: id, SenderID: dbUUID(actor), RoomSeq: seq, Content: content,
		})
		if err != nil {
			return err
		}
		if len(input.MediaIDs) > 0 {
			if err := q.AddChatAttachments(ctx, sqlc.AddChatAttachmentsParams{MessageID: dbUUID(row.ID), MediaIds: input.MediaIDs}); err != nil {
				return err
			}
		}
		items, err := h.messagesWithMedia(r, q, []sqlc.ChatMessage{row})
		if err != nil {
			return err
		}
		result = items[0]
		recipients, err = q.ListChatParticipantIDs(ctx, id)
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.publisher.Publish(ctx, recipients, "NEW_MESSAGE", result)
	httpserver.WriteJSON(w, http.StatusCreated, result)
}

func (h *Handler) messagesWithMedia(r *http.Request, q *sqlc.Queries, rows []sqlc.ChatMessage) ([]message, error) {
	items := make([]message, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		item := message{ID: row.ID, RoomID: row.ChatRoomID, RoomSeq: row.RoomSeq, Deleted: row.DeletedAt.Valid}
		if !item.Deleted {
			item.CreatedAt = &row.CreatedAt.Time
			item.Content = row.Content
			if row.SenderID.Valid {
				sender := uuid.UUID(row.SenderID.Bytes)
				item.SenderID = &sender
			}
			ids = append(ids, row.ID)
		}
		items = append(items, item)
	}
	rowsMedia, err := q.ListChatMessageAttachments(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	byMessage := map[uuid.UUID][]attachment{}
	for _, row := range rowsMedia {
		if row.MimeType != nil {
			id := uuid.UUID(row.ChatMessageID.Bytes)
			byMessage[id] = append(byMessage[id], attachment{MessageID: id, MediaID: row.MediaID, MimeType: *row.MimeType})
		}
	}
	for i := range items {
		items[i].Attachments = byMessage[items[i].ID]
	}
	return items, nil
}

func (h *Handler) shared(w http.ResponseWriter, r *http.Request) {
	id := roomID(r)
	assets, err := h.queries.ListChatSharedMedia(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	rows, err := h.queries.ListChatSharedLinkMessages(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	files := make([]attachment, 0, len(assets))
	for _, item := range assets {
		if item.MimeType != nil {
			files = append(files, attachment{MessageID: uuid.UUID(item.ChatMessageID.Bytes), MediaID: item.MediaID, MimeType: *item.MimeType})
		}
	}
	links := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		for _, token := range strings.Fields(row.Content) {
			token = strings.Trim(token, "<>()[]{}.,!?")
			link, err := url.Parse(token)
			if err == nil && (link.Scheme == "http" || link.Scheme == "https") && link.Host != "" && !seen[token] && len(links) < 100 {
				seen[token] = true
				links = append(links, token)
			}
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Media []attachment `json:"media"`
		Links []string     `json:"links"`
	}{files, links})
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ThroughSeq int64 `json:"throughSeq"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ThroughSeq < 0 {
		badRequest(w, "throughSeq must be a non-negative integer")
		return
	}
	_, err := h.queries.MarkChatRoomRead(r.Context(), sqlc.MarkChatRoomReadParams{
		RoomID: roomID(r), UserID: caller(r), ThroughSeq: input.ThroughSeq,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireRoomMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := roomID(r)
		if id == uuid.Nil {
			badRequest(w, "invalid room id")
			return
		}
		member, err := h.queries.IsChatParticipant(r.Context(), sqlc.IsChatParticipantParams{
			ChatRoomID: id, UserID: caller(r),
		})
		if err == nil && !member {
			err = errRoomAccess
		}
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errRoomAccess), errors.Is(err, media.ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, media.ErrInvalid):
		badRequest(w, "invalid participants or attachments")
	default:
		h.publisher.Logger.ErrorContext(r.Context(), "chat operation failed", slog.String("error", err.Error()))
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "chat operation unavailable")
	}
}

func decode(w http.ResponseWriter, r *http.Request, input any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(input); err != nil {
		badRequest(w, "invalid JSON request body")
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, message string) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
}

func caller(r *http.Request) uuid.UUID {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return claims.UserID
}

func roomID(r *http.Request) uuid.UUID {
	id, _ := uuid.Parse(chi.URLParam(r, "roomID"))
	return id
}

func dbUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
