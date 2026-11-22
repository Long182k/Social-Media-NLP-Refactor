-- name: CreateGroup :one
INSERT INTO groups (creator_id, name, description, avatar_media_id)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: CreateGroupAdmin :exec
INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'ADMIN', 'JOINED');

-- name: RestoreGroupCreatorMembership :exec
INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'ADMIN', 'JOINED')
ON CONFLICT (group_id, user_id) DO UPDATE SET role = 'ADMIN', status = 'JOINED';

-- name: LockGroupOwner :one
SELECT creator_id FROM groups WHERE id = $1 FOR NO KEY UPDATE;

-- name: GetGroup :one
SELECT g.*, COALESCE(gm.role, '') AS viewer_role, COALESCE(gm.status, '') AS viewer_status,
       (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.status = 'JOINED') AS member_count
FROM groups g
LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = sqlc.arg(viewer_id)
WHERE g.id = sqlc.arg(group_id);

-- name: ListGroups :many
SELECT g.*, COALESCE(gm.role, '') AS viewer_role, COALESCE(gm.status, '') AS viewer_status,
       (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id AND m.status = 'JOINED') AS member_count
FROM groups g
LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = sqlc.arg(viewer_id)
WHERE COALESCE(gm.status = 'JOINED', FALSE) = sqlc.arg(only_joined)::bool
  AND g.name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\'
  AND (sqlc.narg(cursor_at)::timestamptz IS NULL OR
      (g.created_at, g.id) < (sqlc.narg(cursor_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY g.created_at DESC, g.id DESC
LIMIT 51;

-- name: UpdateGroup :exec
UPDATE groups
SET name = COALESCE(sqlc.narg(name)::text, name),
    description = COALESCE(sqlc.narg(description)::text, description),
    avatar_media_id = COALESCE(sqlc.narg(avatar_media_id)::uuid, avatar_media_id),
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: DeleteGroup :execrows
DELETE FROM groups WHERE id = $1;

-- name: RequestGroupMembership :execrows
INSERT INTO group_members (group_id, user_id, role, status) VALUES ($1, $2, 'PENDING', 'PENDING')
ON CONFLICT (group_id, user_id) DO UPDATE SET role = 'PENDING', status = 'PENDING', joined_at = NOW()
WHERE group_members.status = 'REJECTED';

-- name: DeleteGroupMembership :exec
DELETE FROM group_members WHERE group_id = $1 AND user_id = $2;

-- name: GetGroupMemberRole :one
SELECT role FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'JOINED';

-- name: DecideGroupMembership :execrows
UPDATE group_members
SET status = sqlc.arg(status), role = sqlc.arg(role), joined_at = NOW()
WHERE group_id = sqlc.arg(group_id) AND user_id = sqlc.arg(user_id) AND status = 'PENDING';

-- name: ListGroupRequests :many
SELECT u.id, u.user_name, u.nick_name, u.avatar_url
FROM group_members gm JOIN users u ON u.id = gm.user_id
WHERE gm.group_id = $1 AND gm.status = 'PENDING'
ORDER BY gm.joined_at, u.id
LIMIT 100;

-- name: CreateEvent :one
INSERT INTO events (creator_id, name, description, avatar_media_id, event_date, category, address)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;

-- name: CreateEventAdmin :exec
INSERT INTO event_attendees (event_id, user_id, role, status) VALUES ($1, $2, 'ADMIN', 'ENROLL');

-- name: LockEventOwner :one
SELECT creator_id FROM events WHERE id = $1 FOR NO KEY UPDATE;

-- name: GetEvent :one
SELECT e.*, COALESCE(a.role, '') AS viewer_role, COALESCE(a.status, '') AS viewer_status,
       (SELECT COUNT(*) FROM event_attendees ea WHERE ea.event_id = e.id AND ea.status = 'ENROLL') AS attendee_count
FROM events e
LEFT JOIN event_attendees a ON a.event_id = e.id AND a.user_id = sqlc.arg(viewer_id)
WHERE e.id = sqlc.arg(event_id);

-- name: ListEvents :many
SELECT e.*, COALESCE(a.role, '') AS viewer_role, COALESCE(a.status, '') AS viewer_status,
       (SELECT COUNT(*) FROM event_attendees ea WHERE ea.event_id = e.id AND ea.status = 'ENROLL') AS attendee_count
FROM events e
LEFT JOIN event_attendees a ON a.event_id = e.id AND a.user_id = sqlc.arg(viewer_id)
WHERE (sqlc.arg(category)::text = '' OR e.category = sqlc.arg(category)::text)
  AND (sqlc.arg(mode)::text <> 'mine' OR
      (e.creator_id = sqlc.arg(viewer_id) OR a.status IN ('ENROLL', 'PENDING')))
  AND (sqlc.arg(mode)::text <> 'discover' OR
      (e.creator_id <> sqlc.arg(viewer_id) AND COALESCE(a.status, '') <> 'ENROLL'))
  AND e.name ILIKE '%' || sqlc.arg(search)::text || '%' ESCAPE '\'
ORDER BY
    CASE WHEN sqlc.arg(mode)::text = 'trending' THEN
      (SELECT COUNT(*) FROM event_attendees ea WHERE ea.event_id = e.id AND ea.status = 'ENROLL')
    END DESC,
    e.event_date, e.id
LIMIT 50 OFFSET sqlc.arg(page_offset);

-- name: UpdateEvent :exec
UPDATE events
SET name = COALESCE(sqlc.narg(name)::text, name),
    description = COALESCE(sqlc.narg(description)::text, description),
    avatar_media_id = COALESCE(sqlc.narg(avatar_media_id)::uuid, avatar_media_id),
    event_date = COALESCE(sqlc.narg(event_date)::timestamptz, event_date),
    category = COALESCE(sqlc.narg(category)::text, category),
    address = COALESCE(sqlc.narg(address)::text, address),
    updated_at = NOW()
WHERE id = sqlc.arg(id);

-- name: DeleteEvent :execrows
DELETE FROM events WHERE id = $1;

-- name: RequestEventAttendance :execrows
INSERT INTO event_attendees (event_id, user_id, role, status) VALUES ($1, $2, 'PENDING_ATTENDEE', 'PENDING')
ON CONFLICT (event_id, user_id) DO UPDATE SET role = 'PENDING_ATTENDEE', status = 'PENDING', created_at = NOW()
WHERE event_attendees.status = 'CANCEL';

-- name: DecideEventAttendance :execrows
UPDATE event_attendees SET role = sqlc.arg(role), status = sqlc.arg(status)
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id)
  AND (status = 'PENDING' OR (status = 'ENROLL' AND sqlc.arg(status)::text = 'CANCEL'));

-- name: ListEventAttendees :many
SELECT u.id, u.user_name, u.nick_name, u.avatar_url, a.role, a.status
FROM event_attendees a JOIN users u ON u.id = a.user_id
WHERE a.event_id = sqlc.arg(event_id) AND a.status = sqlc.arg(status)
ORDER BY a.created_at, u.id
LIMIT 100;
