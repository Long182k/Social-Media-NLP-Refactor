\set ON_ERROR_STOP 1
BEGIN;

CREATE TEMP TABLE m4_seed ON COMMIT DROP AS
SELECT n,
       (SELECT id::text FROM users ORDER BY email OFFSET n - 1 LIMIT 1) AS owner_id,
       (SELECT id::text FROM users ORDER BY email OFFSET n + 11 LIMIT 1) AS peer_id,
       (SELECT id::text FROM posts ORDER BY id OFFSET n - 1 LIMIT 1) AS post_id,
       md5('m4-group-' || n)::uuid AS group_id,
       md5('m4-event-' || n)::uuid AS event_id,
       md5('m4-room-' || n)::uuid AS room_id,
       md5('m4-comment-' || n)::uuid AS comment_id,
       md5('m4-group-post-' || n)::uuid AS group_post_id
FROM generate_series(1, 12) AS n;

CREATE TEMP TABLE m4_messages ON COMMIT DROP AS
SELECT n, room_id, message_index,
       CASE WHEN message_index = 2 THEN peer_id ELSE owner_id END AS sender_id,
       CASE WHEN message_index = 2 THEN owner_id ELSE peer_id END AS receiver_id,
       md5('m4-message-' || n || '-' || message_index)::uuid AS id
FROM m4_seed CROSS JOIN generate_series(1, 3) AS message_index;

CREATE TEMP TABLE m4_attachments ON COMMIT DROP AS
SELECT s.n, attachment_type, s.owner_id,
       md5('m4-media-' || s.n || '-' || attachment_type)::uuid AS media_id,
       md5('m4-attachment-' || s.n || '-' || attachment_type)::uuid AS id,
       CASE WHEN attachment_type = 'post' THEN s.post_id::uuid END AS post_id,
       CASE WHEN attachment_type = 'comment' THEN s.comment_id END AS comment_id,
       CASE WHEN attachment_type = 'chat' THEN m.id END AS chat_message_id
FROM m4_seed s JOIN m4_messages m ON m.n = s.n AND m.message_index = 1
CROSS JOIN (VALUES ('post'), ('comment'), ('chat')) AS kinds(attachment_type);

\if :legacy

INSERT INTO groups(id, "creatorId", name, description)
SELECT group_id::text, owner_id, 'Fixture group ' || n, 'Benchmark group' FROM m4_seed;
INSERT INTO group_members(id, "groupId", "userId", role, status)
SELECT md5('m4-group-owner-' || n), group_id::text, owner_id, 'ADMIN'::"GroupRole", 'JOINED'::"GroupStatus" FROM m4_seed
UNION ALL
SELECT md5('m4-group-peer-' || n), group_id::text, peer_id, 'MEMBER', 'JOINED' FROM m4_seed;

INSERT INTO posts(id, "userId", "groupId", content, sentiment)
SELECT group_post_id::text, owner_id, group_id::text, 'I love this group ' || n, 'GOOD' FROM m4_seed;
INSERT INTO comments(id, "userId", "postId", content, sentiment)
SELECT comment_id::text, peer_id, post_id, 'Tôi thích bài viết ' || n, 'GOOD' FROM m4_seed;
INSERT INTO likes(id, "userId", "postId")
SELECT md5('m4-like-' || n), peer_id, post_id FROM m4_seed;
INSERT INTO bookmarks(id, "userId", "postId")
SELECT md5('m4-bookmark-' || n), peer_id, post_id FROM m4_seed;
INSERT INTO follows(id, "followerId", "followingId")
SELECT md5('m4-follow-' || n), peer_id, owner_id FROM m4_seed;

INSERT INTO events(id, "creatorId", name, description, "eventDate", category)
SELECT event_id::text, owner_id, 'Fixture event ' || n, 'Benchmark event', now() + interval '30 days', 'TECHNOLOGY' FROM m4_seed;
INSERT INTO event_attendees(id, "eventId", "userId", role, status)
SELECT md5('m4-event-owner-' || n), event_id::text, owner_id, 'ADMIN'::"AttendeeRole", 'ENROLL'::"AttendeeStatus" FROM m4_seed
UNION ALL
SELECT md5('m4-event-peer-' || n), event_id::text, peer_id, 'ATTENDEE', 'ENROLL' FROM m4_seed;

INSERT INTO "ChatRoom"(id, "creatorId", name, type, "updatedAt")
SELECT room_id::text, owner_id, 'Fixture chat ' || n, 'GROUP', now() FROM m4_seed;
INSERT INTO "ChatParticipant"(id, "chatRoomId", "userId")
SELECT md5('m4-chat-owner-' || n), room_id::text, owner_id FROM m4_seed
UNION ALL
SELECT md5('m4-chat-peer-' || n), room_id::text, peer_id FROM m4_seed;
INSERT INTO "ChatMessage"(id, "chatRoomId", "senderId", "receiverId", content, "updatedAt")
SELECT id::text, room_id::text, sender_id, receiver_id, 'Fixture message ' || message_index, now() FROM m4_messages;

INSERT INTO "Notification"(id, "senderId", "receiverId", content, type, "updatedAt")
SELECT md5('m4-notification-' || n), peer_id, owner_id, 'Bookmarked your post', 'BOOKMARK', now() FROM m4_seed;
INSERT INTO attachments(id, "postId", "commentId", "chatMessageId", type, url)
SELECT id::text, post_id::text, comment_id::text, chat_message_id::text, 'IMAGE',
       'https://example.test/fixture/' || media_id || '.gif' FROM m4_attachments;

SELECT concat_ws(',', (SELECT count(*) FROM groups), (SELECT count(*) FROM group_members),
  (SELECT count(*) FROM posts), (SELECT count(*) FROM comments), (SELECT count(*) FROM likes),
  (SELECT count(*) FROM bookmarks), (SELECT count(*) FROM follows), (SELECT count(*) FROM events),
  (SELECT count(*) FROM event_attendees), (SELECT count(*) FROM "ChatRoom"), (SELECT count(*) FROM "ChatParticipant"),
  (SELECT count(*) FROM "ChatMessage"), (SELECT count(*) FROM "Notification"), (SELECT count(*) FROM attachments));

\else

INSERT INTO groups(id, creator_id, name, description)
SELECT group_id, owner_id::uuid, 'Fixture group ' || n, 'Benchmark group' FROM m4_seed;
INSERT INTO group_members(group_id, user_id, role, status)
SELECT group_id, owner_id::uuid, 'ADMIN', 'JOINED' FROM m4_seed
UNION ALL
SELECT group_id, peer_id::uuid, 'MEMBER', 'JOINED' FROM m4_seed;
INSERT INTO posts(id, user_id, group_id, content, sentiment, content_version)
SELECT group_post_id, owner_id::uuid, group_id, 'I love this group ' || n, 'GOOD', 1 FROM m4_seed;
INSERT INTO comments(id, user_id, post_id, content, sentiment)
SELECT comment_id, peer_id::uuid, post_id::uuid, 'Tôi thích bài viết ' || n, 'GOOD' FROM m4_seed;
INSERT INTO likes(user_id, post_id)
SELECT peer_id::uuid, post_id::uuid FROM m4_seed;
INSERT INTO bookmarks(id, user_id, post_id)
SELECT md5('m4-bookmark-' || n)::uuid, peer_id::uuid, post_id::uuid FROM m4_seed;
INSERT INTO follows(follower_id, following_id)
SELECT peer_id::uuid, owner_id::uuid FROM m4_seed;

INSERT INTO events(id, creator_id, name, description, event_date, category)
SELECT event_id, owner_id::uuid, 'Fixture event ' || n, 'Benchmark event', now() + interval '30 days', 'TECHNOLOGY' FROM m4_seed;
INSERT INTO event_attendees(event_id, user_id, role, status)
SELECT event_id, owner_id::uuid, 'ADMIN', 'ENROLL' FROM m4_seed
UNION ALL
SELECT event_id, peer_id::uuid, 'ATTENDEE', 'ENROLL' FROM m4_seed;

INSERT INTO chat_rooms(id, creator_id, name, type, next_seq)
SELECT room_id, owner_id::uuid, 'Fixture chat ' || n, 'GROUP', 3 FROM m4_seed;
INSERT INTO chat_participants(chat_room_id, user_id)
SELECT room_id, owner_id::uuid FROM m4_seed
UNION ALL
SELECT room_id, peer_id::uuid FROM m4_seed;
INSERT INTO chat_messages(id, chat_room_id, sender_id, room_seq, content)
SELECT id, room_id, sender_id::uuid, message_index, 'Fixture message ' || message_index FROM m4_messages;

INSERT INTO notifications(id, sender_id, receiver_id, user_notif_seq, content, type, entity_id)
SELECT md5('m4-notification-' || n)::uuid, peer_id::uuid, owner_id::uuid, 1, 'Bookmarked your post', 'BOOKMARK', post_id::uuid FROM m4_seed;
UPDATE users SET next_notif_seq = 1 FROM m4_seed s WHERE users.id = s.owner_id::uuid;

INSERT INTO media_objects(id, owner_id, staging_key, finalized_key, sha256, declared_mime, verified_mime,
  declared_size, verified_size, visibility, status, finalized_at, staging_cleaned_at)
SELECT media_id, owner_id::uuid, 'uploads/pending/' || media_id,
       'media/verified/' || CASE WHEN attachment_type = 'chat' THEN 'private/' ELSE 'public/' END || media_id || '/fixture',
       repeat('0',64), 'image/gif', 'image/gif', 35, 35,
       CASE WHEN attachment_type = 'chat' THEN 'PRIVATE'::media_visibility ELSE 'PUBLIC'::media_visibility END,
       'READY', now(), now() FROM m4_attachments;
INSERT INTO attachments(id, media_id, post_id, comment_id, chat_message_id)
SELECT id, media_id, post_id, comment_id, chat_message_id FROM m4_attachments;

SELECT concat_ws(',', (SELECT count(*) FROM groups), (SELECT count(*) FROM group_members),
  (SELECT count(*) FROM posts), (SELECT count(*) FROM comments), (SELECT count(*) FROM likes),
  (SELECT count(*) FROM bookmarks), (SELECT count(*) FROM follows), (SELECT count(*) FROM events),
  (SELECT count(*) FROM event_attendees), (SELECT count(*) FROM chat_rooms), (SELECT count(*) FROM chat_participants),
  (SELECT count(*) FROM chat_messages), (SELECT count(*) FROM notifications), (SELECT count(*) FROM attachments));

\endif
COMMIT;
