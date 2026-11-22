package graphql

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/admin"
	"github.com/social-media-nlp/refactor/backend/internal/posts"
)

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

type Resolver struct {
	queries *sqlc.Queries
}

func (r *Resolver) postPage(ctx context.Context, groupID, authorID uuid.UUID, after *string, first *int, search string) (*PostPage, error) {
	limit := posts.DefaultFeedLimit
	if first != nil {
		limit = *first
	}
	if limit < 1 || limit > posts.MaxFeedLimit {
		return nil, gqlerror.Errorf("first must be between 1 and %d", posts.MaxFeedLimit)
	}
	params := sqlc.GraphPostPageParams{
		GroupID:   pgtype.UUID{Bytes: groupID, Valid: groupID != uuid.Nil},
		AuthorID:  pgtype.UUID{Bytes: authorID, Valid: authorID != uuid.Nil},
		Search:    strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(search)),
		PageLimit: int32(limit + 1),
	}
	if after != nil && *after != "" {
		cursor, err := posts.DecodeCursor(*after)
		if err != nil {
			return nil, gqlerror.Errorf("invalid cursor")
		}
		params.CursorAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.CursorID = pgtype.UUID{Bytes: cursor.ID, Valid: true}
	}
	rows, err := r.queries.GraphPostPage(ctx, params)
	if err != nil {
		return nil, err
	}
	page := &PostPage{HasMore: len(rows) > limit, Data: make([]*Post, 0, min(limit, len(rows)))}
	if page.HasMore {
		rows = rows[:limit]
		last := rows[limit-1]
		cursor := posts.EncodeCursor(last.CreatedAt.Time, last.ID)
		page.NextCursor = &cursor
	}
	for _, row := range rows {
		page.Data = append(page.Data, graphPost(row))
	}
	return page, nil
}

func graphPost(row sqlc.Post) *Post {
	return &Post{
		ID: row.ID, UserID: row.UserID, GroupID: graphUUID(row.GroupID), Content: row.Content,
		Sentiment: Sentiment(row.Sentiment), ContentVersion: int(row.ContentVersion),
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func graphUUID(value pgtype.UUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := uuid.UUID(value.Bytes)
	return &id
}

func graphAdmin(dashboard admin.Dashboard) *AdminDashboard {
	result := &AdminDashboard{
		TotalUsers: int(dashboard.TotalUsers), ActiveUsers: int(dashboard.ActiveUsers), TotalPosts: int(dashboard.TotalPosts),
		TotalGroups: int(dashboard.TotalGroups), TotalEvents: int(dashboard.TotalEvents),
		SentimentRatios:  make([]*SentimentRatio, 0, len(AllSentiment)),
		UserGrowthData:   make([]*UserGrowth, 0, len(dashboard.UserGrowthData)),
		MostPositiveUser: graphRank(dashboard.MostPositiveUser), MostNegativeUser: graphRank(dashboard.MostNegativeUser),
	}
	for _, label := range AllSentiment {
		ratio := dashboard.SentimentRatios[string(label)]
		result.SentimentRatios = append(result.SentimentRatios, &SentimentRatio{Label: label, Count: int(ratio.Count), Ratio: ratio.Ratio})
	}
	for _, day := range dashboard.UserGrowthData {
		result.UserGrowthData = append(result.UserGrowthData, &UserGrowth{Day: day.Day, Count: int(day.Count)})
	}
	return result
}

func graphRank(user *admin.RankedUser) *RankedUser {
	if user == nil {
		return nil
	}
	return &RankedUser{
		ID: user.ID, UserName: user.UserName, NickName: user.NickName,
		PostCount: int(user.PostCount), CommentCount: int(user.CommentCount), TotalCount: int(user.TotalCount),
	}
}
