-- name: AdminDashboardTotals :one
SELECT (SELECT COUNT(*) FROM users) AS total_users,
       (SELECT COUNT(*) FROM users WHERE is_active AND last_login_at >= NOW() - INTERVAL '24 hours') AS active_users,
       (SELECT COUNT(*) FROM posts) AS total_posts,
       (SELECT COUNT(*) FROM groups) AS total_groups,
       (SELECT COUNT(*) FROM events) AS total_events;

-- name: AdminDashboardSentiment :many
SELECT sentiment, SUM(post_count)::bigint AS post_count, SUM(comment_count)::bigint AS comment_count
FROM (
    SELECT p.sentiment, COUNT(*) AS post_count, 0::bigint AS comment_count FROM posts p GROUP BY p.sentiment
    UNION ALL
    SELECT c.sentiment, 0::bigint, COUNT(*) FROM comments c GROUP BY c.sentiment
) scores
GROUP BY sentiment;

-- name: AdminHighestSentimentUser :one
WITH totals AS (
    SELECT user_id, SUM(post_count)::bigint AS post_count, SUM(comment_count)::bigint AS comment_count
    FROM (
        SELECT p.user_id, COUNT(*) AS post_count, 0::bigint AS comment_count
        FROM posts p WHERE p.sentiment = sqlc.arg(sentiment) GROUP BY p.user_id
        UNION ALL
        SELECT c.user_id, 0::bigint, COUNT(*) FROM comments c
        WHERE c.sentiment = sqlc.arg(sentiment) GROUP BY c.user_id
    ) scored GROUP BY user_id
)
SELECT u.id, u.user_name, u.nick_name, t.post_count, t.comment_count,
       (t.post_count + t.comment_count)::bigint AS total_count
FROM totals t JOIN users u ON u.id = t.user_id
ORDER BY total_count DESC, u.id
LIMIT 1;

-- name: AdminUserGrowth :many
SELECT day::date::text AS day,
       (SELECT COUNT(*) FROM users u WHERE u.created_at >= day AND u.created_at < day + INTERVAL '1 day') AS count
FROM generate_series(date_trunc('day', NOW()) - INTERVAL '6 days', date_trunc('day', NOW()), INTERVAL '1 day') AS day
ORDER BY day;

-- name: AdminUsers :many
SELECT u.id, u.user_name, u.nick_name, u.email, u.role, u.is_active, u.last_login_at,
       u.created_at, u.avatar_url,
       (SELECT COUNT(*) FROM follows f WHERE f.following_id = u.id) AS followers,
       (SELECT COUNT(*) FROM follows f WHERE f.follower_id = u.id) AS following,
       (SELECT COUNT(*) FROM bookmarks b WHERE b.user_id = u.id) AS bookmarks,
       (SELECT COUNT(*) FROM likes l WHERE l.user_id = u.id) AS likes,
       (SELECT COUNT(*) FROM posts p WHERE p.user_id = u.id AND p.sentiment = 'GOOD') AS positive_posts,
       (SELECT COUNT(*) FROM posts p WHERE p.user_id = u.id AND p.sentiment = 'MODERATE') AS neutral_posts,
       (SELECT COUNT(*) FROM posts p WHERE p.user_id = u.id AND p.sentiment = 'BAD') AS negative_posts,
       (SELECT COUNT(*) FROM comments c WHERE c.user_id = u.id AND c.sentiment = 'GOOD') AS positive_comments,
       (SELECT COUNT(*) FROM comments c WHERE c.user_id = u.id AND c.sentiment = 'MODERATE') AS neutral_comments,
       (SELECT COUNT(*) FROM comments c WHERE c.user_id = u.id AND c.sentiment = 'BAD') AS negative_comments
FROM users u
WHERE (sqlc.arg(search)::text = '' OR
       u.user_name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\' OR
       u.nick_name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\')
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL OR
       (u.created_at, u.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY u.created_at DESC, u.id DESC LIMIT 51;

-- name: AdminToggleUserActive :one
UPDATE users
SET is_active = NOT is_active, session_epoch = session_epoch + 1, updated_at = NOW()
WHERE id = $1
RETURNING is_active;

-- name: AdminGroups :many
SELECT g.id, g.name, g.creator_id, g.created_at,
       (SELECT COUNT(*) FROM group_members gm WHERE gm.group_id = g.id AND gm.status = 'JOINED') AS member_count
FROM groups g ORDER BY g.created_at DESC, g.id DESC LIMIT 50 OFFSET $1::integer;

-- name: AdminEvents :many
SELECT e.id, e.name, e.creator_id, e.event_date, e.category,
       (SELECT COUNT(*) FROM event_attendees ea WHERE ea.event_id = e.id AND ea.status = 'ENROLL') AS attendee_count
FROM events e ORDER BY e.event_date, e.id LIMIT 50 OFFSET $1::integer;
