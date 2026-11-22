-- name: CreatePost :one
INSERT INTO posts (
    user_id,
    group_id,
    content,
    sentiment,
    content_version
) VALUES (
    $1, $2, $3, $4, 1
)
RETURNING id, user_id, content, sentiment, content_version, created_at, updated_at;

-- name: GetPostOwnerByID :one
SELECT id, user_id, group_id
FROM posts
WHERE id = $1;

-- name: GetPostWithAuthorByID :one
SELECT
    p.id, p.user_id, p.group_id, p.content, p.sentiment, p.content_version, p.created_at, p.updated_at,
    (SELECT COUNT(*) FROM likes l WHERE l.post_id = p.id) AS like_count,
    (SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id) AS comment_count,
    (SELECT COUNT(*) FROM bookmarks b WHERE b.post_id = p.id) AS bookmark_count,
    u.user_name AS author_user_name,
    u.nick_name AS author_nick_name,
    u.email AS author_email,
    u.role AS author_role,
    u.avatar_url AS author_avatar_url,
    u.cover_page_url AS author_cover_page_url,
    u.bio AS author_bio,
    u.date_of_birth AS author_date_of_birth,
    u.is_active AS author_is_active,
    u.last_login_at AS author_last_login_at,
    u.created_at AS author_created_at
FROM posts p
JOIN users u ON u.id = p.user_id
WHERE p.id = $1;

-- name: ListPostsFirstPage :many
SELECT
    p.id, p.user_id, p.group_id, p.content, p.sentiment, p.content_version, p.created_at, p.updated_at,
    (SELECT COUNT(*) FROM likes l WHERE l.post_id = p.id) AS like_count,
    (SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id) AS comment_count,
    (SELECT COUNT(*) FROM bookmarks b WHERE b.post_id = p.id) AS bookmark_count,
    u.user_name AS author_user_name,
    u.nick_name AS author_nick_name,
    u.email AS author_email,
    u.role AS author_role,
    u.avatar_url AS author_avatar_url,
    u.cover_page_url AS author_cover_page_url,
    u.bio AS author_bio,
    u.date_of_birth AS author_date_of_birth,
    u.is_active AS author_is_active,
    u.last_login_at AS author_last_login_at,
    u.created_at AS author_created_at
FROM posts p
JOIN users u ON u.id = p.user_id
WHERE (
    (sqlc.narg(author_id)::uuid IS NOT NULL AND p.user_id = sqlc.narg(author_id)::uuid)
    OR (sqlc.narg(author_id)::uuid IS NULL AND
        (p.group_id = sqlc.narg(group_id)::uuid OR (p.group_id IS NULL AND sqlc.narg(group_id)::uuid IS NULL)))
)
  AND (sqlc.arg(search)::text = '' OR p.content ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\')
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListPostsAfterCursor :many
SELECT
    p.id, p.user_id, p.group_id, p.content, p.sentiment, p.content_version, p.created_at, p.updated_at,
    (SELECT COUNT(*) FROM likes l WHERE l.post_id = p.id) AS like_count,
    (SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id) AS comment_count,
    (SELECT COUNT(*) FROM bookmarks b WHERE b.post_id = p.id) AS bookmark_count,
    u.user_name AS author_user_name,
    u.nick_name AS author_nick_name,
    u.email AS author_email,
    u.role AS author_role,
    u.avatar_url AS author_avatar_url,
    u.cover_page_url AS author_cover_page_url,
    u.bio AS author_bio,
    u.date_of_birth AS author_date_of_birth,
    u.is_active AS author_is_active,
    u.last_login_at AS author_last_login_at,
    u.created_at AS author_created_at
FROM posts p
JOIN users u ON u.id = p.user_id
WHERE (
    (sqlc.narg(author_id)::uuid IS NOT NULL AND p.user_id = sqlc.narg(author_id)::uuid)
    OR (sqlc.narg(author_id)::uuid IS NULL AND
        (p.group_id = sqlc.narg(group_id)::uuid OR (p.group_id IS NULL AND sqlc.narg(group_id)::uuid IS NULL)))
)
  AND (sqlc.arg(search)::text = '' OR p.content ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\')
  AND (p.created_at, p.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdatePostContent :one
UPDATE posts
SET content = $3,
    sentiment = $4,
    content_version = content_version + CASE WHEN content IS DISTINCT FROM $3 THEN 1 ELSE 0 END,
    updated_at = NOW()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, content, sentiment, content_version, created_at, updated_at;

-- name: DeletePost :exec
DELETE FROM posts
WHERE id = $1;

-- name: CanPostToGroup :one
SELECT status = 'JOINED'
FROM group_members
WHERE group_id = $1 AND user_id = $2
FOR SHARE;

-- name: ClearPostAttachments :exec
DELETE FROM attachments WHERE post_id = $1;

-- name: AddPostAttachments :exec
INSERT INTO attachments (post_id, media_id)
SELECT sqlc.arg(post_id), UNNEST(sqlc.arg(media_ids)::uuid[]);

-- name: ListPostAttachments :many
SELECT a.post_id, m.id AS media_id, m.verified_mime AS mime_type
FROM attachments a
JOIN media_objects m ON m.id = a.media_id
WHERE a.post_id = ANY(sqlc.arg(post_ids)::uuid[]) AND m.status = 'READY' AND m.visibility = 'PUBLIC'
ORDER BY a.created_at, a.id;

-- name: UpdatePostSentimentIfCurrent :execrows
UPDATE posts
SET sentiment = sqlc.arg(sentiment)
WHERE id = sqlc.arg(id) AND content_version = sqlc.arg(content_version)
  AND sentiment IS DISTINCT FROM sqlc.arg(sentiment);
