package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/posts"
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
	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.RequireAuth(h.auth), auth.RequireRole("ADMIN"))
		r.Get("/dashboard-stats", h.dashboard)
		r.Get("/users", h.users)
		r.Patch("/users/{id}/toggle-activity", h.toggleUser)
		r.Get("/groups", h.groups)
		r.Delete("/groups/{id}", h.deleteGroup)
		r.Get("/events", h.events)
		r.Delete("/events/{id}", h.deleteEvent)
	})
}

type SentimentRatio struct {
	Count int64   `json:"count"`
	Ratio float64 `json:"ratio"`
}

func sentimentRatios(good, moderate, bad int64) map[string]SentimentRatio {
	total := good + moderate + bad
	result := make(map[string]SentimentRatio, 3)
	for label, count := range map[string]int64{"GOOD": good, "MODERATE": moderate, "BAD": bad} {
		item := SentimentRatio{Count: count}
		if total != 0 {
			item.Ratio = float64(count) / float64(total)
		}
		result[label] = item
	}
	return result
}

type RankedUser struct {
	ID           uuid.UUID `json:"id"`
	UserName     string    `json:"userName"`
	NickName     string    `json:"nickName"`
	PostCount    int64     `json:"postCount"`
	CommentCount int64     `json:"commentCount"`
	TotalCount   int64     `json:"totalCount"`
}

type Dashboard struct {
	TotalUsers       int64                     `json:"totalUsers"`
	ActiveUsers      int64                     `json:"activeUsers"`
	TotalPosts       int64                     `json:"totalPosts"`
	TotalGroups      int64                     `json:"totalGroups"`
	TotalEvents      int64                     `json:"totalEvents"`
	SentimentRatios  map[string]SentimentRatio `json:"sentimentRatios"`
	UserGrowthData   []sqlc.AdminUserGrowthRow `json:"userGrowthData"`
	MostPositiveUser *RankedUser               `json:"mostPositiveUser"`
	MostNegativeUser *RankedUser               `json:"mostNegativeUser"`
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := ReadDashboard(r.Context(), h.queries)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, dashboard)
}

func ReadDashboard(ctx context.Context, queries *sqlc.Queries) (Dashboard, error) {
	totals, err := queries.AdminDashboardTotals(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	scores, err := queries.AdminDashboardSentiment(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	counts := map[sqlc.SentimentType]int64{}
	for _, row := range scores {
		counts[row.Sentiment] = row.PostCount + row.CommentCount
	}
	growth, err := queries.AdminUserGrowth(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	ranked := make(map[sqlc.SentimentType]*RankedUser, 2)
	for _, label := range []sqlc.SentimentType{sqlc.SentimentTypeGOOD, sqlc.SentimentTypeBAD} {
		row, err := queries.AdminHighestSentimentUser(ctx, label)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Dashboard{}, err
		}
		ranked[label] = &RankedUser{
			ID: row.ID, UserName: row.UserName, NickName: row.NickName,
			PostCount: row.PostCount, CommentCount: row.CommentCount, TotalCount: row.TotalCount,
		}
	}
	return Dashboard{
		TotalUsers: totals.TotalUsers, ActiveUsers: totals.ActiveUsers, TotalPosts: totals.TotalPosts,
		TotalGroups: totals.TotalGroups, TotalEvents: totals.TotalEvents, UserGrowthData: growth,
		SentimentRatios:  sentimentRatios(counts[sqlc.SentimentTypeGOOD], counts[sqlc.SentimentTypeMODERATE], counts[sqlc.SentimentTypeBAD]),
		MostPositiveUser: ranked[sqlc.SentimentTypeGOOD], MostNegativeUser: ranked[sqlc.SentimentTypeBAD],
	}, nil
}

type userSummary struct {
	ID               uuid.UUID                 `json:"id"`
	UserName         string                    `json:"userName"`
	NickName         string                    `json:"nickName"`
	Email            string                    `json:"email"`
	Role             string                    `json:"role"`
	AvatarURL        *string                   `json:"avatarUrl"`
	IsActive         bool                      `json:"isActive"`
	LastLoginAt      *time.Time                `json:"lastLoginAt"`
	Followers        int64                     `json:"followers"`
	Following        int64                     `json:"following"`
	Likes            int64                     `json:"likes"`
	Bookmarks        int64                     `json:"bookmarks"`
	Posts            int64                     `json:"posts"`
	Comments         int64                     `json:"comments"`
	PostSentiment    map[string]SentimentRatio `json:"postSentiment"`
	CommentSentiment map[string]SentimentRatio `json:"commentSentiment"`
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	search := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(query.Get("q")))
	params := sqlc.AdminUsersParams{Search: search}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := posts.DecodeCursor(raw)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid cursor")
			return
		}
		params.CursorAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.CursorID = pgtype.UUID{Bytes: cursor.ID, Valid: true}
	}
	rows, err := h.queries.AdminUsers(r.Context(), params)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var next *string
	if len(rows) > 50 {
		rows = rows[:50]
		last := rows[len(rows)-1]
		cursor := posts.EncodeCursor(last.CreatedAt.Time, last.ID)
		next = &cursor
	}
	data := make([]userSummary, 0, len(rows))
	for _, row := range rows {
		item := userSummary{
			ID: row.ID, UserName: row.UserName, NickName: row.NickName, Email: row.Email, Role: string(row.Role),
			AvatarURL: row.AvatarUrl, IsActive: row.IsActive, Followers: row.Followers, Following: row.Following,
			Likes: row.Likes, Bookmarks: row.Bookmarks, Posts: row.PositivePosts + row.NeutralPosts + row.NegativePosts,
			Comments:         row.PositiveComments + row.NeutralComments + row.NegativeComments,
			PostSentiment:    sentimentRatios(row.PositivePosts, row.NeutralPosts, row.NegativePosts),
			CommentSentiment: sentimentRatios(row.PositiveComments, row.NeutralComments, row.NegativeComments),
		}
		if row.LastLoginAt.Valid {
			item.LastLoginAt = &row.LastLoginAt.Time
		}
		data = append(data, item)
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Data       []userSummary `json:"data"`
		NextCursor *string       `json:"nextCursor"`
	}{data, next})
}

func (h *Handler) toggleUser(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if !ok {
		return
	}
	active, err := h.queries.AdminToggleUserActive(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !active {
		h.publisher.Publish(r.Context(), []uuid.UUID{id}, "USER_DEACTIVATED", nil)
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]bool{"isActive": active})
}

func (h *Handler) groups(w http.ResponseWriter, r *http.Request) {
	offset, ok := listOffset(w, r)
	if !ok {
		return
	}
	rows, err := h.queries.AdminGroups(r.Context(), offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	offset, ok := listOffset(w, r)
	if !ok {
		return
	}
	rows, err := h.queries.AdminEvents(r.Context(), offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, rows)
}

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if ok {
		count, err := h.queries.DeleteGroup(r.Context(), id)
		h.deleteResponse(w, r, count, err)
	}
}

func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := resourceID(w, r)
	if ok {
		count, err := h.queries.DeleteEvent(r.Context(), id)
		h.deleteResponse(w, r, count, err)
	}
}

func (h *Handler) deleteResponse(w http.ResponseWriter, r *http.Request, count int64, err error) {
	if err == nil && count == 0 {
		err = pgx.ErrNoRows
	}
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "resource not found")
		return
	}
	h.publisher.Logger.ErrorContext(r.Context(), "admin operation failed", slog.String("error", err.Error()))
	httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "admin operation unavailable")
}

func resourceID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil || id == uuid.Nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid resource id")
		return uuid.Nil, false
	}
	return id, true
}

func listOffset(w http.ResponseWriter, r *http.Request) (int32, bool) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, true
	}
	offset, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || offset < 0 || offset > 100000 {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid offset")
		return 0, false
	}
	return int32(offset), true
}
