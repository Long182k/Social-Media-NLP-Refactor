-- name: CreateChatRoom :one
INSERT INTO chat_rooms (creator_id, name, type, direct_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT (direct_key) DO UPDATE SET direct_key = EXCLUDED.direct_key
RETURNING id;

-- name: CountActiveChatUsers :one
SELECT COUNT(*) FROM users WHERE id = ANY($1::uuid[]) AND is_active;

-- name: AddChatParticipants :exec
INSERT INTO chat_participants (chat_room_id, user_id)
SELECT sqlc.arg(room_id), UNNEST(sqlc.arg(user_ids)::uuid[])
ON CONFLICT (chat_room_id, user_id) DO NOTHING;

-- name: IsChatParticipant :one
SELECT EXISTS (SELECT 1 FROM chat_participants WHERE chat_room_id = $1 AND user_id = $2);

-- name: ListChatParticipantIDs :many
SELECT user_id FROM chat_participants WHERE chat_room_id = $1 ORDER BY user_id;

-- name: ListChatProfiles :many
SELECT cp.chat_room_id, u.id, u.user_name, u.nick_name, u.avatar_url
FROM chat_participants cp
JOIN users u ON u.id = cp.user_id
WHERE cp.chat_room_id = ANY(sqlc.arg(room_ids)::uuid[])
ORDER BY u.nick_name, u.id;

-- name: SyncChatRooms :many
SELECT cr.id, cr.name, cr.type, cr.next_seq, cr.updated_at, cp.last_read_seq,
       COALESCE(latest.id, '00000000-0000-0000-0000-000000000000'::uuid) AS last_message_id,
       COALESCE(latest.content, '') AS last_message_content,
       COALESCE(latest.room_seq, 0)::bigint AS last_message_seq,
       (SELECT COUNT(*) FROM chat_messages cm
        WHERE cm.chat_room_id = cr.id AND cm.room_seq > cp.last_read_seq
          AND cm.sender_id IS DISTINCT FROM cp.user_id AND cm.deleted_at IS NULL) AS unread_count
FROM chat_participants cp
JOIN chat_rooms cr ON cr.id = cp.chat_room_id
LEFT JOIN LATERAL (
    SELECT m.id, m.content, m.room_seq
    FROM chat_messages m
    WHERE m.chat_room_id = cr.id AND m.deleted_at IS NULL
    ORDER BY m.room_seq DESC LIMIT 1
) latest ON TRUE
WHERE cp.user_id = $1
ORDER BY cr.updated_at DESC, cr.id;

-- name: AdvanceChatRoomSequence :one
UPDATE chat_rooms cr
SET next_seq = cr.next_seq + 1, updated_at = NOW()
WHERE cr.id = sqlc.arg(room_id)
  AND EXISTS (
      SELECT 1 FROM chat_participants cp
      WHERE cp.chat_room_id = cr.id AND cp.user_id = sqlc.arg(user_id)
  )
RETURNING next_seq;

-- name: CreateChatMessage :one
INSERT INTO chat_messages (chat_room_id, sender_id, room_seq, content)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: AddChatAttachments :exec
INSERT INTO attachments (chat_message_id, media_id)
SELECT sqlc.arg(message_id), UNNEST(sqlc.arg(media_ids)::uuid[]);

-- name: ListChatMessagesAfterSeq :many
SELECT * FROM chat_messages
WHERE chat_room_id = sqlc.arg(room_id) AND room_seq > sqlc.arg(after_seq)
ORDER BY room_seq
LIMIT sqlc.arg(page_limit);

-- name: ListChatMessageAttachments :many
SELECT a.chat_message_id, m.id AS media_id, m.verified_mime AS mime_type
FROM attachments a
JOIN media_objects m ON m.id = a.media_id
WHERE a.chat_message_id = ANY(sqlc.arg(message_ids)::uuid[])
  AND m.status = 'READY' AND m.visibility = 'PRIVATE'
ORDER BY a.created_at, a.id;

-- name: ListChatSharedMedia :many
SELECT a.chat_message_id, m.id AS media_id, m.verified_mime AS mime_type
FROM chat_messages cm
JOIN attachments a ON a.chat_message_id = cm.id
JOIN media_objects m ON m.id = a.media_id
WHERE cm.chat_room_id = $1 AND cm.deleted_at IS NULL
  AND m.status = 'READY' AND m.visibility = 'PRIVATE'
ORDER BY cm.room_seq DESC, a.id
LIMIT 100;

-- name: ListChatSharedLinkMessages :many
SELECT id, content FROM chat_messages
WHERE chat_room_id = $1 AND deleted_at IS NULL
  AND (content LIKE '%https://%' OR content LIKE '%http://%')
ORDER BY room_seq DESC
LIMIT 100;

-- name: MarkChatRoomRead :execrows
UPDATE chat_participants cp
SET last_read_seq = GREATEST(cp.last_read_seq, LEAST(cr.next_seq, sqlc.arg(through_seq)::bigint))
FROM chat_rooms cr
WHERE cr.id = cp.chat_room_id AND cp.chat_room_id = sqlc.arg(room_id) AND cp.user_id = sqlc.arg(user_id);
