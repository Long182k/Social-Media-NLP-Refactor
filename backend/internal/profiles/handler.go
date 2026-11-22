package profiles

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/media"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

type Handler struct {
	pool      *pgxpool.Pool
	queries   *sqlc.Queries
	auth      *auth.Service
	media     *media.Service
	publisher realtime.Publisher
}

func NewHandler(pool *pgxpool.Pool, authService *auth.Service, mediaService *media.Service, publisher realtime.Publisher) *Handler {
	return &Handler{pool: pool, queries: sqlc.New(pool), auth: authService, media: mediaService, publisher: publisher}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth))
		r.Get("/users", h.directory)
		r.Get("/users/available-contact", h.contacts)
		r.Get("/users/suggestions", h.suggestions)
		r.Get("/users/recent_birthday", h.birthdays)
		r.Get("/users/userName", h.byUsername)
		r.Get("/users/keyword", h.directory)
		r.Patch("/users/edit-profile", h.edit)
		r.Patch("/users/change/avatar", h.avatar)
		r.Patch("/users/change/cover-page", h.cover)
		r.Get("/users/{userID}", h.get)
		r.Delete("/users/{userID}", h.delete)
		r.Post("/users/{userID}/follow", h.toggleFollow)
		r.Get("/users/{userID}/follow-status", h.followStatus)
		r.Get("/users/{userID}/followers", h.followers)
		r.Get("/users/{userID}/following", h.following)
		r.Get("/users/{userID}/photos", h.photos)
		r.Get("/users/{userID}/videos", h.videos)
	})
}

type profile struct {
	ID          uuid.UUID  `json:"id"`
	UserName    string     `json:"userName"`
	NickName    string     `json:"nickName"`
	Bio         *string    `json:"bio"`
	AvatarURL   *string    `json:"avatarUrl"`
	DateOfBirth *time.Time `json:"dateOfBirth"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if ok {
		h.writeProfile(w, r, id)
	}
}

func (h *Handler) byUsername(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("userName"))
	if name == "" {
		invalid(w, "userName is required")
		return
	}
	id, err := h.queries.GetUserProfileByName(r.Context(), name)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeProfile(w, r, id)
}

func (h *Handler) writeProfile(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	row, err := h.queries.GetUserProfile(r.Context(), sqlc.GetUserProfileParams{UserID: id, ViewerID: caller(r)})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		profile
		CoverPageURL   *string   `json:"coverPageUrl"`
		CreatedAt      time.Time `json:"createdAt"`
		FollowerCount  int64     `json:"followerCount"`
		FollowingCount int64     `json:"followingCount"`
		Following      bool      `json:"following"`
	}{
		profile: profile{
			ID: row.ID, UserName: row.UserName, NickName: row.NickName,
			Bio: row.Bio, AvatarURL: row.AvatarUrl, DateOfBirth: birthDate(row.DateOfBirth),
		},
		CoverPageURL: row.CoverPageUrl, CreatedAt: row.CreatedAt.Time,
		FollowerCount: row.FollowerCount, FollowingCount: row.FollowingCount, Following: row.Following,
	})
}

func (h *Handler) edit(w http.ResponseWriter, r *http.Request) {
	var input struct {
		NickName    *string    `json:"nickName"`
		Bio         *string    `json:"bio"`
		DateOfBirth *time.Time `json:"dateOfBirth"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.NickName != nil {
		*input.NickName = strings.TrimSpace(*input.NickName)
	}
	if (input.NickName != nil && (*input.NickName == "" || utf8.RuneCountInString(*input.NickName) > 100)) ||
		(input.Bio != nil && utf8.RuneCountInString(*input.Bio) > 1000) ||
		(input.DateOfBirth != nil && (input.DateOfBirth.IsZero() || input.DateOfBirth.After(time.Now()))) {
		invalid(w, "invalid profile details")
		return
	}
	var birthDate pgtype.Timestamptz
	if input.DateOfBirth != nil {
		birthDate = pgtype.Timestamptz{Time: input.DateOfBirth.UTC(), Valid: true}
	}
	id := caller(r)
	err := h.queries.EditUserProfile(r.Context(), sqlc.EditUserProfileParams{
		UserID: id, NickName: input.NickName, Bio: input.Bio, DateOfBirth: birthDate,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeProfile(w, r, id)
}

func (h *Handler) avatar(w http.ResponseWriter, r *http.Request) {
	h.setImage(w, r, false)
}

func (h *Handler) cover(w http.ResponseWriter, r *http.Request) {
	h.setImage(w, r, true)
}

func (h *Handler) setImage(w http.ResponseWriter, r *http.Request, cover bool) {
	if h.media == nil {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "media storage unavailable")
		return
	}
	var input struct {
		MediaID uuid.UUID `json:"mediaId"`
	}
	if !decode(w, r, &input) {
		return
	}
	ctx, id := r.Context(), caller(r)
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		imageURL, err := h.media.ProfileImageURL(ctx, q, id, input.MediaID)
		if err != nil {
			return err
		}
		_, err = q.SetProfileImage(ctx, sqlc.SetProfileImageParams{UserID: id, IsCover: cover, ImageUrl: imageURL})
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.writeProfile(w, r, id)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	if id != caller(r) {
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "only the owner can delete this account")
		return
	}
	if _, err := h.queries.DeleteOwnAccount(r.Context(), id); err != nil {
		h.writeError(w, r, err)
		return
	}
	// nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Match the configured secure setting of the refresh cookie.
		Name: auth.RefreshCookieName, Path: "/api/auth", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.auth.CookieSecure(),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) directory(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "", caller(r))
}

func (h *Handler) contacts(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "contacts", caller(r))
}

func (h *Handler) suggestions(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, "suggestions", caller(r))
}

func (h *Handler) followers(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if ok {
		h.list(w, r, "followers", id)
	}
}

func (h *Handler) following(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if ok {
		h.list(w, r, "following", id)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, mode string, subject uuid.UUID) {
	offset, ok := pageOffset(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("q"))
	if search == "" {
		search = strings.TrimSpace(query.Get("keyword"))
	}
	search = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)
	rows, err := h.queries.ListDirectory(r.Context(), sqlc.ListDirectoryParams{
		Mode: mode, SubjectID: subject, Search: search, PageOffset: offset,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	writeDirectory(w, rows)
}

func (h *Handler) birthdays(w http.ResponseWriter, r *http.Request) {
	params := sqlc.ListBirthdaysParams{ViewerID: caller(r), FromDays: -7, ToDays: 0}
	if r.URL.Query().Get("upcoming") == "true" {
		params.FromDays, params.ToDays = 0, 14
	}
	rows, err := h.queries.ListBirthdays(r.Context(), params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	directory := make([]sqlc.ListDirectoryRow, 0, len(rows))
	for _, row := range rows {
		directory = append(directory, sqlc.ListDirectoryRow(row))
	}
	writeDirectory(w, directory)
}

func writeDirectory(w http.ResponseWriter, rows []sqlc.ListDirectoryRow) {
	items := make([]profile, 0, len(rows))
	for _, row := range rows {
		items = append(items, profile{
			ID: row.ID, UserName: row.UserName, NickName: row.NickName,
			Bio: row.Bio, AvatarURL: row.AvatarUrl, DateOfBirth: birthDate(row.DateOfBirth),
		})
	}
	httpserver.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) followStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	following, err := h.queries.IsFollowing(r.Context(), sqlc.IsFollowingParams{FollowerID: caller(r), FollowingID: id})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]bool{"following": following})
}

func (h *Handler) toggleFollow(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	ctx, user := r.Context(), caller(r)
	if id == user {
		invalid(w, "cannot follow your own profile")
		return
	}
	var notice sqlc.Notification
	err := pgx.BeginFunc(ctx, h.pool, func(tx pgx.Tx) error {
		q := h.queries.WithTx(tx)
		if _, err := q.LockFollowTarget(ctx, id); err != nil {
			return err
		}
		count, err := q.DeleteFollow(ctx, sqlc.DeleteFollowParams{FollowerID: user, FollowingID: id})
		if err != nil || count > 0 {
			return err
		}
		if err := q.CreateFollow(ctx, sqlc.CreateFollowParams{FollowerID: user, FollowingID: id}); err != nil {
			return err
		}
		notice, err = notifications.Create(ctx, q, user, id, user, "FOLLOW", "Followed your profile")
		return err
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	following := notice.ID != uuid.Nil
	if following {
		notifications.PublishCreated(ctx, h.publisher, notice)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]bool{"following": following})
}

func (h *Handler) photos(w http.ResponseWriter, r *http.Request) {
	h.profileMedia(w, r, "image")
}

func (h *Handler) videos(w http.ResponseWriter, r *http.Request) {
	h.profileMedia(w, r, "video")
}

func (h *Handler) profileMedia(w http.ResponseWriter, r *http.Request, kind string) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	offset, ok := pageOffset(w, r)
	if !ok {
		return
	}
	rows, err := h.queries.ListProfileMedia(r.Context(), sqlc.ListProfileMediaParams{UserID: id, MediaType: kind, PageOffset: offset})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	type asset struct {
		MediaID  uuid.UUID `json:"mediaId"`
		MimeType string    `json:"mimeType"`
		AuthorID uuid.UUID `json:"authorId"`
		Created  time.Time `json:"createdAt"`
	}
	items := make([]asset, 0, len(rows))
	for _, row := range rows {
		if row.VerifiedMime != nil {
			items = append(items, asset{
				MediaID: row.ID, MimeType: *row.VerifiedMime, AuthorID: id, Created: row.FinalizedAt.Time,
			})
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var pgError *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "user not found")
	case errors.Is(err, media.ErrForbidden):
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, media.ErrInvalid):
		invalid(w, "profile images require finalized public image media")
	case errors.As(err, &pgError) && pgError.Code == "23505":
		httpserver.WriteError(w, http.StatusConflict, "CONFLICT", "profile name already in use")
	default:
		h.publisher.Logger.ErrorContext(r.Context(), "profile operation failed", slog.String("error", err.Error()))
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "profile operation unavailable")
	}
}

func caller(r *http.Request) uuid.UUID {
	claims, _ := auth.ClaimsFromContext(r.Context())
	return claims.UserID
}

func userID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil || id == uuid.Nil {
		invalid(w, "invalid user id")
		return uuid.Nil, false
	}
	return id, true
}

func pageOffset(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, true
	}
	offset, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || offset < 0 || offset > 100000 {
		invalid(w, "invalid offset")
		return 0, false
	}
	return int32(offset), true
}

func birthDate(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func decode(w http.ResponseWriter, r *http.Request, input any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(input); err != nil {
		invalid(w, "invalid JSON request body")
		return false
	}
	return true
}

func invalid(w http.ResponseWriter, message string) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
}
