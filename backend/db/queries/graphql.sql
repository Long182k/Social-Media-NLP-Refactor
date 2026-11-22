-- name: GraphPost :one
SELECT * FROM posts WHERE id = $1;

-- name: GraphPostPage :many
SELECT * FROM posts p
WHERE (
    (sqlc.narg(author_id)::uuid IS NOT NULL AND p.user_id = sqlc.narg(author_id)::uuid)
    OR (sqlc.narg(author_id)::uuid IS NULL AND
        (p.group_id = sqlc.narg(group_id)::uuid OR (p.group_id IS NULL AND sqlc.narg(group_id)::uuid IS NULL)))
)
AND (sqlc.arg(search)::text = '' OR p.content ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\')
AND (sqlc.narg(cursor_at)::timestamptz IS NULL OR
     (p.created_at, p.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY p.created_at DESC, p.id DESC LIMIT sqlc.arg(page_limit);

-- name: GraphAuthors :many
SELECT id, user_name, nick_name, avatar_url
FROM users WHERE id = ANY(sqlc.arg(user_ids)::uuid[]);

-- name: GraphPostCounts :many
SELECT p.id,
    (SELECT COUNT(*) FROM likes l WHERE l.post_id = p.id) AS like_count,
    (SELECT COUNT(*) FROM comments c WHERE c.post_id = p.id) AS comment_count,
    (SELECT COUNT(*) FROM bookmarks b WHERE b.post_id = p.id) AS bookmark_count
FROM posts p WHERE p.id = ANY(sqlc.arg(post_ids)::uuid[]);
