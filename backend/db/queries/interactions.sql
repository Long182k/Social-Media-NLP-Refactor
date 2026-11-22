-- name: LockPostForInteraction :one
SELECT user_id FROM posts WHERE id = $1 FOR NO KEY UPDATE;

-- name: CreateLike :exec
INSERT INTO likes (user_id, post_id) VALUES ($1, $2);

-- name: DeleteLike :execrows
DELETE FROM likes WHERE user_id = $1 AND post_id = $2;

-- name: CreateBookmark :one
INSERT INTO bookmarks (user_id, post_id) VALUES ($1, $2) RETURNING id;

-- name: DeleteBookmarkForPost :execrows
DELETE FROM bookmarks WHERE user_id = $1 AND post_id = $2;

-- name: GetBookmarkOwner :one
SELECT user_id FROM bookmarks WHERE id = $1;

-- name: DeleteOwnedBookmark :exec
DELETE FROM bookmarks WHERE id = $1 AND user_id = $2;

-- name: GetPostInteractions :one
SELECT (SELECT COUNT(*) FROM likes l WHERE l.post_id = p.id) AS like_count,
       (SELECT COUNT(*) FROM bookmarks b WHERE b.post_id = p.id) AS bookmark_count,
       EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = sqlc.arg(user_id)) AS liked,
       EXISTS (SELECT 1 FROM bookmarks b WHERE b.post_id = p.id AND b.user_id = sqlc.arg(user_id)) AS bookmarked
FROM posts p WHERE p.id = sqlc.arg(post_id);

-- name: ListViewerPostInteractions :many
SELECT p.id,
       EXISTS (SELECT 1 FROM likes l WHERE l.post_id = p.id AND l.user_id = sqlc.arg(viewer_id)) AS liked,
       EXISTS (SELECT 1 FROM bookmarks b WHERE b.post_id = p.id AND b.user_id = sqlc.arg(viewer_id)) AS bookmarked
FROM posts p WHERE p.id = ANY(sqlc.arg(post_ids)::uuid[]);

-- name: ListBookmarks :many
SELECT b.id, b.created_at, b.post_id, p.content, p.sentiment, p.user_id AS author_id,
       u.nick_name AS author_nick_name, u.avatar_url AS author_avatar_url
FROM bookmarks b
JOIN posts p ON p.id = b.post_id
JOIN users u ON u.id = p.user_id
WHERE b.user_id = sqlc.arg(user_id)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR
      (b.created_at, b.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(page_limit);
