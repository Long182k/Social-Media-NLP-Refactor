-- name: CreateNotification :one
WITH sequence AS (
    UPDATE users u
    SET next_notif_seq = u.next_notif_seq + 1
    WHERE u.id = sqlc.arg(receiver_id)
    RETURNING u.id, u.next_notif_seq
)
INSERT INTO notifications (receiver_id, sender_id, user_notif_seq, type, content, entity_id)
SELECT seq.id, sqlc.arg(sender_id), seq.next_notif_seq, sqlc.arg(type), sqlc.arg(content), sqlc.narg(entity_id)
FROM sequence seq
RETURNING *;

-- name: ListRecentNotifications :many
SELECT * FROM notifications
WHERE receiver_id = sqlc.arg(receiver_id) AND deleted_at IS NULL
ORDER BY user_notif_seq DESC
LIMIT sqlc.arg(page_limit);

-- name: ListNotificationsAfterSeq :many
SELECT * FROM notifications
WHERE receiver_id = sqlc.arg(receiver_id) AND user_notif_seq > sqlc.arg(after_seq)
ORDER BY user_notif_seq
LIMIT sqlc.arg(page_limit);

-- name: GetNotification :one
SELECT * FROM notifications WHERE id = $1 AND receiver_id = $2 AND deleted_at IS NULL;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications WHERE receiver_id = $1 AND NOT is_read AND deleted_at IS NULL;

-- name: MarkNotificationRead :one
UPDATE notifications SET is_read = TRUE, updated_at = NOW()
WHERE id = $1 AND receiver_id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET is_read = TRUE, updated_at = NOW()
WHERE receiver_id = $1 AND NOT is_read AND deleted_at IS NULL;

-- name: DeleteNotification :one
UPDATE notifications SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND receiver_id = $2 AND deleted_at IS NULL
RETURNING *;
