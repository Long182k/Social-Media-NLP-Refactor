package community

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
	"github.com/social-media-nlp/refactor/backend/internal/posts"
)

type groupDTO struct {
	ID            uuid.UUID  `json:"id"`
	CreatorID     uuid.UUID  `json:"creatorId"`
	Name          string     `json:"name"`
	Description   *string    `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatarMediaId"`
	CreatedAt     time.Time  `json:"createdAt"`
	ViewerRole    string     `json:"viewerRole"`
	ViewerStatus  string     `json:"viewerStatus"`
	MemberCount   int64      `json:"memberCount"`
}

func groupProjection(row sqlc.GetGroupRow) groupDTO {
	return groupDTO{
		ID: row.ID, CreatorID: row.CreatorID, Name: row.Name, Description: row.Description,
		AvatarMediaID: avatarID(row.AvatarMediaID), CreatedAt: row.CreatedAt.Time,
		ViewerRole: row.ViewerRole, ViewerStatus: row.ViewerStatus, MemberCount: row.MemberCount,
	}
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := query.Get("onlyUserGroups")
	if filter != "" && filter != "true" && filter != "false" {
		clientError(w, "onlyUserGroups must be true or false")
		return
	}
	params := sqlc.ListGroupsParams{ViewerID: actor(r).UserID, OnlyJoined: filter == "true", Search: searchTerm(query.Get("q"))}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := posts.DecodeCursor(raw)
		if err != nil {
			clientError(w, "invalid cursor")
			return
		}
		params.CursorAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.CursorID = pgtype.UUID{Bytes: cursor.ID, Valid: true}
	}
	rows, err := h.queries.ListGroups(r.Context(), params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var next *string
	if len(rows) > 50 {
		rows = rows[:50]
		last := rows[len(rows)-1]
		value := posts.EncodeCursor(last.CreatedAt.Time, last.ID)
		next = &value
	}
	data := make([]groupDTO, 0, len(rows))
	for _, row := range rows {
		data = append(data, groupProjection(sqlc.GetGroupRow(row)))
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data       []groupDTO `json:"data"`
		NextCursor *string    `json:"nextCursor"`
	}{data, next})
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "groupID")
	if ok {
		h.writeGroup(w, r, id, http.StatusOK)
	}
}

func (h *Handler) writeGroup(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	row, err := h.queries.GetGroup(r.Context(), sqlc.GetGroupParams{GroupID: id, ViewerID: actor(r).UserID})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, status, groupProjection(row))
}

type groupInput struct {
	Name          *string    `json:"name"`
	Description   *string    `json:"description"`
	AvatarMediaID *uuid.UUID `json:"avatarMediaId"`
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	h.saveGroup(w, r, uuid.Nil)
}

func (h *Handler) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "groupID")
	if ok {
		h.saveGroup(w, r, id)
	}
}

func (h *Handler) saveGroup(w http.ResponseWriter, r *http.Request, groupID uuid.UUID) {
	var input groupInput
	if !readInput(w, r, &input) {
		return
	}
	create := groupID == uuid.Nil
	if input.Name != nil {
		*input.Name = strings.TrimSpace(*input.Name)
	}
	if (create && input.Name == nil) ||
		(input.Name != nil && (*input.Name == "" || utf8.RuneCountInString(*input.Name) > 100)) ||
		(input.Description != nil && utf8.RuneCountInString(*input.Description) > 3000) {
		clientError(w, "invalid group name or description")
		return
	}
	ctx, caller := r.Context(), actor(r)
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if !create {
			if err := requireGroupManager(ctx, q, groupID, caller); err != nil {
				return err
			}
		}
		avatar, err := validateAvatar(ctx, q, caller.UserID, input.AvatarMediaID)
		if err != nil {
			return err
		}
		if create {
			groupID, err = q.CreateGroup(ctx, sqlc.CreateGroupParams{
				CreatorID: caller.UserID, Name: *input.Name, Description: input.Description, AvatarMediaID: avatar,
			})
			if err != nil {
				return err
			}
			return q.CreateGroupAdmin(ctx, sqlc.CreateGroupAdminParams{GroupID: groupID, UserID: caller.UserID})
		}
		return q.UpdateGroup(ctx, sqlc.UpdateGroupParams{ID: groupID, Name: input.Name, Description: input.Description, AvatarMediaID: avatar})
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	h.writeGroup(w, r, groupID, status)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "groupID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if err := requireGroupManager(ctx, q, id, caller); err != nil {
			return err
		}
		_, err := q.DeleteGroup(ctx, id)
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) joinGroup(w http.ResponseWriter, r *http.Request) {
	groupID, ok := requestID(w, r, "groupID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		creator, err := q.LockGroupOwner(ctx, groupID)
		if err != nil {
			return err
		}
		if creator == caller.UserID {
			return q.RestoreGroupCreatorMembership(ctx, sqlc.RestoreGroupCreatorMembershipParams{GroupID: groupID, UserID: caller.UserID})
		}
		count, err := q.RequestGroupMembership(ctx, sqlc.RequestGroupMembershipParams{GroupID: groupID, UserID: caller.UserID})
		if err == nil && count > 0 {
			notice, err = notifications.Create(ctx, q, caller.UserID, creator, groupID, "NEW_JOIN_GROUP_REQUEST", "Requested to join your group")
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

func (h *Handler) leaveGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "groupID")
	if !ok {
		return
	}
	if err := h.queries.DeleteGroupMembership(r.Context(), sqlc.DeleteGroupMembershipParams{GroupID: id, UserID: actor(r).UserID}); err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) groupRequests(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r, "groupID")
	if !ok {
		return
	}
	ctx := r.Context()
	if err := requireGroupManager(ctx, h.queries, id, actor(r)); err != nil {
		h.writeError(w, r, err)
		return
	}
	rows, err := h.queries.ListGroupRequests(ctx, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	result := make([]profile, 0, len(rows))
	for _, row := range rows {
		result = append(result, profile{ID: row.ID, UserName: row.UserName, NickName: row.NickName, AvatarURL: row.AvatarUrl})
	}
	httpserver.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) approveGroup(w http.ResponseWriter, r *http.Request) {
	h.decideGroup(w, r, "JOINED", "MEMBER", "APPROVED_JOIN_GROUP_REQUEST", "Approved your group request")
}

func (h *Handler) rejectGroup(w http.ResponseWriter, r *http.Request) {
	h.decideGroup(w, r, "REJECTED", "PENDING", "REJECTED_JOIN_GROUP_REQUEST", "Declined your group request")
}

func (h *Handler) decideGroup(w http.ResponseWriter, r *http.Request, status, role, kind, message string) {
	groupID, ok := requestID(w, r, "groupID")
	if !ok {
		return
	}
	memberID, ok := requestID(w, r, "userID")
	if !ok {
		return
	}
	ctx, caller := r.Context(), actor(r)
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if err := requireGroupManager(ctx, q, groupID, caller); err != nil {
			return err
		}
		count, err := q.DecideGroupMembership(ctx, sqlc.DecideGroupMembershipParams{
			GroupID: groupID, UserID: memberID, Role: role, Status: status,
		})
		if err != nil {
			return err
		}
		if count == 0 {
			return errConflict
		}
		notice, err = notifications.Create(ctx, q, caller.UserID, memberID, groupID, kind, message)
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	notifications.PublishCreated(ctx, h.publisher, notice)
	w.WriteHeader(http.StatusNoContent)
}

func requireGroupManager(ctx context.Context, q *sqlc.Queries, groupID uuid.UUID, caller *auth.AuthClaims) error {
	creator, err := q.LockGroupOwner(ctx, groupID)
	if err != nil || creator == caller.UserID || caller.Role == "ADMIN" {
		return err
	}
	role, err := q.GetGroupMemberRole(ctx, sqlc.GetGroupMemberRoleParams{GroupID: groupID, UserID: caller.UserID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "ADMIN") {
		return errForbidden
	}
	return err
}
