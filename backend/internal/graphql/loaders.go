package graphql

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/vikstrous/dataloadgen"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
)

type loaders struct {
	authors     *dataloadgen.Loader[uuid.UUID, *User]
	attachments *dataloadgen.Loader[uuid.UUID, []*Attachment]
	counts      *dataloadgen.Loader[uuid.UUID, *PostCounts]
	viewer      *dataloadgen.Loader[uuid.UUID, *ViewerInteractions]
}

func newLoaders(q *sqlc.Queries, viewerID uuid.UUID) *loaders {
	batchWait := dataloadgen.WithWait(3 * time.Millisecond)
	return &loaders{
		authors: dataloadgen.NewMappedLoader(func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*User, error) {
			rows, err := q.GraphAuthors(ctx, ids)
			result := make(map[uuid.UUID]*User, len(rows))
			for _, row := range rows {
				result[row.ID] = &User{ID: row.ID, UserName: row.UserName, NickName: row.NickName, AvatarURL: row.AvatarUrl}
			}
			return result, err
		}, batchWait),
		attachments: dataloadgen.NewMappedLoader(func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]*Attachment, error) {
			rows, err := q.ListPostAttachments(ctx, ids)
			result := make(map[uuid.UUID][]*Attachment, len(ids))
			for _, id := range ids {
				result[id] = []*Attachment{}
			}
			for _, row := range rows {
				id := uuid.UUID(row.PostID.Bytes)
				if row.MimeType != nil {
					result[id] = append(result[id], &Attachment{MediaID: row.MediaID, MimeType: *row.MimeType})
				}
			}
			return result, err
		}, batchWait),
		counts: dataloadgen.NewMappedLoader(func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*PostCounts, error) {
			rows, err := q.GraphPostCounts(ctx, ids)
			result := make(map[uuid.UUID]*PostCounts, len(rows))
			for _, row := range rows {
				result[row.ID] = &PostCounts{
					LikeCount: int(row.LikeCount), CommentCount: int(row.CommentCount), BookmarkCount: int(row.BookmarkCount),
				}
			}
			return result, err
		}, batchWait),
		viewer: dataloadgen.NewMappedLoader(func(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*ViewerInteractions, error) {
			rows, err := q.ListViewerPostInteractions(ctx, sqlc.ListViewerPostInteractionsParams{ViewerID: viewerID, PostIds: ids})
			result := make(map[uuid.UUID]*ViewerInteractions, len(rows))
			for _, row := range rows {
				result[row.ID] = &ViewerInteractions{Liked: row.Liked, Bookmarked: row.Bookmarked}
			}
			return result, err
		}, batchWait),
	}
}
