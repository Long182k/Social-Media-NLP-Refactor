-- name: CreateComment :one
INSERT INTO comments (post_id, user_id, content, sentiment)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: LockComment :one
SELECT user_id FROM comments WHERE id = $1 FOR NO KEY UPDATE;

-- name: GetComment :one
SELECT c.*, u.user_name, u.nick_name, u.avatar_url
FROM comments c
JOIN users u ON u.id = c.user_id
WHERE c.id = $1;

-- name: ListComments :many
SELECT c.*, u.user_name, u.nick_name, u.avatar_url
FROM comments c
JOIN users u ON u.id = c.user_id
WHERE c.post_id = sqlc.arg(post_id)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR
      (c.created_at, c.id) > (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY c.created_at, c.id
LIMIT sqlc.arg(page_limit);

-- name: UpdateComment :exec
UPDATE comments
SET content = $2, sentiment = $3, updated_at = NOW(),
    content_version = content_version + CASE WHEN content IS DISTINCT FROM $2 THEN 1 ELSE 0 END
WHERE id = $1;

-- name: DeleteComment :exec
DELETE FROM comments WHERE id = $1;

-- name: ClearCommentAttachments :exec
DELETE FROM attachments WHERE comment_id = $1;

-- name: AddCommentAttachments :exec
INSERT INTO attachments (comment_id, media_id)
SELECT sqlc.arg(comment_id), UNNEST(sqlc.arg(media_ids)::uuid[]);

-- name: ListCommentAttachments :many
SELECT a.comment_id, m.id AS media_id, m.verified_mime AS mime_type
FROM attachments a
JOIN media_objects m ON m.id = a.media_id
WHERE a.comment_id = ANY(sqlc.arg(comment_ids)::uuid[])
  AND m.status = 'READY' AND m.visibility = 'PUBLIC'
ORDER BY a.created_at, a.id;

-- name: UpdateCommentSentimentIfCurrent :execrows
UPDATE comments
SET sentiment = sqlc.arg(sentiment)
WHERE id = sqlc.arg(id) AND content_version = sqlc.arg(content_version)
  AND sentiment IS DISTINCT FROM sqlc.arg(sentiment);
