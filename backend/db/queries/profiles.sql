-- name: GetUserProfile :one
SELECT u.id, u.user_name, u.nick_name, u.bio, u.avatar_url, u.cover_page_url, u.date_of_birth, u.created_at,
       (SELECT COUNT(*) FROM follows f WHERE f.following_id = u.id) AS follower_count,
       (SELECT COUNT(*) FROM follows f WHERE f.follower_id = u.id) AS following_count,
       EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = sqlc.arg(viewer_id) AND f.following_id = u.id) AS following
FROM users u WHERE u.is_active AND u.id = sqlc.arg(user_id);

-- name: GetUserProfileByName :one
SELECT id FROM users WHERE is_active AND LOWER(user_name) = LOWER($1);

-- name: EditUserProfile :exec
UPDATE users
SET nick_name = COALESCE(sqlc.narg(nick_name)::text, nick_name),
    bio = COALESCE(sqlc.narg(bio)::text, bio),
    date_of_birth = COALESCE(sqlc.narg(date_of_birth)::timestamptz, date_of_birth),
    updated_at = NOW()
WHERE id = sqlc.arg(user_id);

-- name: SetProfileImage :execrows
UPDATE users
SET avatar_url = CASE WHEN sqlc.arg(is_cover)::bool THEN avatar_url ELSE sqlc.arg(image_url)::text END,
    cover_page_url = CASE WHEN sqlc.arg(is_cover)::bool THEN sqlc.arg(image_url)::text ELSE cover_page_url END,
    updated_at = NOW()
WHERE id = sqlc.arg(user_id);

-- name: DeleteOwnAccount :execrows
DELETE FROM users WHERE id = $1;

-- name: ListDirectory :many
SELECT u.id, u.user_name, u.nick_name, u.bio, u.avatar_url, u.date_of_birth
FROM users u
WHERE u.is_active
  AND (sqlc.arg(search)::text = '' OR
      u.user_name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\' OR
      u.nick_name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\')
  AND CASE sqlc.arg(mode)::text
      WHEN 'followers' THEN EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = u.id AND f.following_id = sqlc.arg(subject_id))
      WHEN 'following' THEN EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = sqlc.arg(subject_id) AND f.following_id = u.id)
      WHEN 'suggestions' THEN u.id <> sqlc.arg(subject_id) AND NOT EXISTS (SELECT 1 FROM follows f WHERE f.follower_id = sqlc.arg(subject_id) AND f.following_id = u.id)
      WHEN 'contacts' THEN u.id <> sqlc.arg(subject_id)
      ELSE TRUE
  END
ORDER BY u.nick_name, u.id
LIMIT 50 OFFSET sqlc.arg(page_offset);

-- name: ListBirthdays :many
WITH days AS (
    SELECT day, EXTRACT(MONTH FROM day) AS month, EXTRACT(DAY FROM day) AS month_day
    FROM generate_series(
        CURRENT_DATE + sqlc.arg(from_days)::integer,
        CURRENT_DATE + sqlc.arg(to_days)::integer, INTERVAL '1 day'
    ) AS day
)
SELECT u.id, u.user_name, u.nick_name, u.bio, u.avatar_url, u.date_of_birth
FROM days
JOIN users u ON EXTRACT(MONTH FROM u.date_of_birth AT TIME ZONE 'UTC') = days.month
  AND EXTRACT(DAY FROM u.date_of_birth AT TIME ZONE 'UTC') = days.month_day
WHERE u.is_active AND u.date_of_birth IS NOT NULL AND u.id <> sqlc.arg(viewer_id)
ORDER BY days.day, u.nick_name, u.id
LIMIT 50;

-- name: LockFollowTarget :one
SELECT id FROM users WHERE id = $1 AND is_active FOR NO KEY UPDATE;

-- name: CreateFollow :exec
INSERT INTO follows (follower_id, following_id) VALUES ($1, $2);

-- name: DeleteFollow :execrows
DELETE FROM follows WHERE follower_id = $1 AND following_id = $2;

-- name: IsFollowing :one
SELECT EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND following_id = $2);

-- name: ListProfileMedia :many
SELECT DISTINCT m.id, m.verified_mime, m.finalized_at
FROM media_objects m
JOIN attachments a ON a.media_id = m.id
WHERE m.owner_id = sqlc.arg(user_id) AND m.status = 'READY' AND m.visibility = 'PUBLIC'
  AND m.verified_mime LIKE sqlc.arg(media_type)::text || '/%'
  AND (a.post_id IS NOT NULL OR a.comment_id IS NOT NULL)
ORDER BY m.finalized_at DESC, m.id DESC
LIMIT 50 OFFSET sqlc.arg(page_offset);
