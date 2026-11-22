-- name: CreateUser :one
INSERT INTO users (
    user_name,
    nick_name,
    email,
    password_hash,
    role,
    avatar_url,
    cover_page_url,
    is_active,
    last_login_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, NOW()
)
RETURNING
    id, user_name, nick_name, email, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch;

-- name: GetPublicUserByID :one
SELECT
    id, user_name, nick_name, email, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch
FROM users
WHERE id = $1;

-- name: GetPublicUserByEmail :one
SELECT
    id, user_name, nick_name, email, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch
FROM users
WHERE LOWER(email) = LOWER($1);

-- name: GetUserAuthByIdentifier :one
-- Auth-internal only: selects password_hash strictly for login verification.
SELECT
    id, user_name, nick_name, email, password_hash, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch
FROM users
WHERE (POSITION('@' IN $1::text) > 0 AND LOWER(email) = LOWER($1::text))
   OR (POSITION('@' IN $1::text) = 0 AND LOWER(user_name) = LOWER($1::text));

-- name: GetUserAuthByID :one
-- Auth-internal only: selects password_hash strictly for change-password verification.
SELECT
    id, password_hash, is_active
FROM users
WHERE id = $1;

-- name: UpdateLastLoginAt :one
UPDATE users
SET last_login_at = NOW(), updated_at = NOW()
WHERE id = $1
RETURNING
    id, user_name, nick_name, email, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch;

-- name: UpdateLastLoginAtForEpoch :one
UPDATE users
SET last_login_at = NOW(), updated_at = NOW()
WHERE id = $1 AND is_active = TRUE AND session_epoch = $2
RETURNING
    id, user_name, nick_name, email, role, bio, date_of_birth,
    avatar_url, cover_page_url, is_active, last_login_at, created_at, updated_at, session_epoch;

-- name: UpdateUserPasswordHash :exec
UPDATE users
SET password_hash = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserPasswordAndBumpEpoch :one
UPDATE users
SET password_hash = $2, session_epoch = session_epoch + 1, updated_at = NOW()
WHERE id = $1
RETURNING session_epoch;
