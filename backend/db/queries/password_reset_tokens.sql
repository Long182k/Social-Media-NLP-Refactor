-- name: CreatePasswordResetToken :one
INSERT INTO password_reset_tokens (
    user_id,
    token_hash,
    expires_at
) VALUES (
    $1, $2, $3
)
RETURNING id, user_id, expires_at, used_at, created_at;

-- name: InvalidateUnusedResetTokensForUser :exec
UPDATE password_reset_tokens
SET used_at = NOW()
WHERE user_id = $1 AND used_at IS NULL;

-- name: ConsumePasswordResetToken :one
UPDATE password_reset_tokens
SET used_at = NOW()
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
RETURNING id, user_id, expires_at, used_at, created_at;
