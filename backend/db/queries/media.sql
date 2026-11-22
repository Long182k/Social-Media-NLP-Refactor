-- name: CreateMediaObject :one
INSERT INTO media_objects (id, owner_id, staging_key, declared_mime, declared_size, visibility)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetMediaObject :one
SELECT * FROM media_objects WHERE id = $1;

-- name: ClaimMediaForFinalization :one
UPDATE media_objects
SET status = 'FINALIZING', finalizing_lease_token = sqlc.arg(lease_token), finalizing_started_at = NOW()
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND status = 'PENDING'
  AND created_at > NOW() - INTERVAL '5 minutes'
RETURNING *;

-- name: SaveVerifiedMedia :execrows
UPDATE media_objects
SET finalized_key = $3, sha256 = $4, verified_mime = $5, verified_size = $6
WHERE id = $1 AND finalizing_lease_token = $2 AND status = 'FINALIZING';

-- name: MarkMediaReady :execrows
UPDATE media_objects
SET status = 'READY', finalized_at = NOW(), finalizing_lease_token = NULL, finalizing_started_at = NULL
WHERE id = $1 AND finalizing_lease_token = $2 AND status = 'FINALIZING';

-- name: RejectMediaFinalization :execrows
UPDATE media_objects
SET status = 'REJECTED', finalizing_lease_token = NULL, finalizing_started_at = NULL
WHERE id = $1 AND finalizing_lease_token = $2 AND status = 'FINALIZING';

-- name: RejectExpiredMedia :many
WITH expired AS (
    SELECT id FROM media_objects
    WHERE (status = 'PENDING' AND created_at <= NOW() - INTERVAL '5 minutes')
       OR (status = 'FINALIZING' AND finalizing_started_at <= NOW() - INTERVAL '60 seconds')
    ORDER BY created_at, id
    LIMIT 100
    FOR UPDATE SKIP LOCKED
)
UPDATE media_objects m
SET status = 'REJECTED', finalizing_lease_token = NULL, finalizing_started_at = NULL
FROM expired WHERE m.id = expired.id
RETURNING m.*;

-- name: ListExpiredStagingKeys :many
SELECT id, staging_key, status, finalized_key FROM media_objects
WHERE status IN ('READY', 'REJECTED') AND staging_cleaned_at IS NULL
  AND created_at <= NOW() - INTERVAL '5 minutes'
ORDER BY created_at, id
LIMIT 100;

-- name: MarkStagingCleaned :exec
UPDATE media_objects SET staging_cleaned_at = NOW() WHERE id = $1;

-- name: HasPrivateMediaAccess :one
SELECT EXISTS (
    SELECT 1 FROM attachments a
    JOIN chat_messages cm ON cm.id = a.chat_message_id AND cm.deleted_at IS NULL
    JOIN chat_participants cp ON cp.chat_room_id = cm.chat_room_id
    WHERE a.media_id = sqlc.arg(media_id) AND cp.user_id = sqlc.arg(user_id)
);

-- name: LockAttachableMedia :many
SELECT id, visibility, owner_id, status
FROM media_objects
WHERE id = ANY(sqlc.arg(media_ids)::uuid[])
ORDER BY id
FOR SHARE;

-- name: ListDueMediaKeys :many
SELECT storage_key FROM media_cleanup_queue
WHERE delete_after <= NOW()
ORDER BY delete_after, storage_key
LIMIT 100;

-- name: RemoveMediaCleanupKey :exec
DELETE FROM media_cleanup_queue WHERE storage_key = $1;
