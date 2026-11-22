-- +goose Up
ALTER TABLE users ADD COLUMN next_notif_seq BIGINT NOT NULL DEFAULT 0 CHECK (next_notif_seq >= 0);

CREATE TYPE media_visibility AS ENUM ('PUBLIC', 'PRIVATE');
CREATE TYPE media_status AS ENUM ('PENDING', 'FINALIZING', 'READY', 'REJECTED');

CREATE TABLE media_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    staging_key TEXT NOT NULL UNIQUE,
    finalized_key TEXT UNIQUE,
    sha256 TEXT,
    declared_mime TEXT NOT NULL,
    verified_mime TEXT,
    declared_size BIGINT NOT NULL CHECK (declared_size BETWEEN 1 AND 5242880),
    verified_size BIGINT CHECK (verified_size BETWEEN 1 AND 5242880),
    visibility media_visibility NOT NULL,
    status media_status NOT NULL DEFAULT 'PENDING',
    finalizing_lease_token UUID,
    finalizing_started_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ,
    staging_cleaned_at TIMESTAMPTZ,
    CHECK ((status = 'FINALIZING') = (finalizing_lease_token IS NOT NULL)),
    CHECK ((status = 'FINALIZING') = (finalizing_started_at IS NOT NULL)),
    CHECK (status <> 'READY' OR (
        finalized_key IS NOT NULL AND sha256 IS NOT NULL AND verified_mime IS NOT NULL
        AND verified_size IS NOT NULL AND finalized_at IS NOT NULL
    ))
);

CREATE INDEX idx_media_objects_owner ON media_objects (owner_id);
CREATE INDEX idx_media_objects_finalizing ON media_objects (finalizing_started_at, id)
    WHERE status = 'FINALIZING';
CREATE INDEX idx_media_objects_staging ON media_objects (created_at, id)
    WHERE staging_cleaned_at IS NULL;

CREATE TABLE groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    avatar_media_id UUID REFERENCES media_objects(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_groups_creator ON groups (creator_id);

CREATE TABLE group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'PENDING' CHECK (role IN ('ADMIN', 'MEMBER', 'PENDING')),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('JOINED', 'PENDING', 'REJECTED')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id),
    CHECK ((status = 'JOINED') = (role IN ('ADMIN', 'MEMBER')))
);

CREATE INDEX idx_group_members_user ON group_members (user_id, status, group_id);
CREATE INDEX idx_group_members_status ON group_members (group_id, status, joined_at);

ALTER TABLE posts ADD COLUMN group_id UUID REFERENCES groups(id) ON DELETE CASCADE;
CREATE INDEX idx_posts_group_created_id_desc ON posts (group_id, created_at DESC, id DESC);

CREATE TABLE comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    sentiment sentiment_type NOT NULL DEFAULT 'MODERATE',
    content_version INTEGER NOT NULL DEFAULT 1 CHECK (content_version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_comments_post_created ON comments (post_id, created_at, id);
CREATE INDEX idx_comments_user ON comments (user_id);

CREATE TABLE likes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);

CREATE INDEX idx_likes_post ON likes (post_id);

CREATE TABLE bookmarks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, post_id)
);

CREATE INDEX idx_bookmarks_post ON bookmarks (post_id);
CREATE INDEX idx_bookmarks_user_created ON bookmarks (user_id, created_at DESC, id DESC);

CREATE TABLE follows (
    follower_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    following_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, following_id),
    CHECK (follower_id <> following_id)
);

CREATE INDEX idx_follows_following ON follows (following_id, follower_id);

CREATE TABLE events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    avatar_media_id UUID REFERENCES media_objects(id) ON DELETE SET NULL,
    event_date TIMESTAMPTZ NOT NULL,
    category TEXT NOT NULL DEFAULT 'OTHER'
        CHECK (category IN ('MUSIC', 'SPORTS', 'EDUCATION', 'TECHNOLOGY', 'FOOD', 'ART', 'BUSINESS', 'HEALTH', 'OTHER')),
    address TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_events_creator ON events (creator_id, event_date, id);
CREATE INDEX idx_events_category_date ON events (category, event_date, id);
CREATE INDEX idx_events_date ON events (event_date, id);

CREATE TABLE event_attendees (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'PENDING_ATTENDEE' CHECK (role IN ('ADMIN', 'ATTENDEE', 'PENDING_ATTENDEE')),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('ENROLL', 'PENDING', 'CANCEL')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id),
    CHECK ((status = 'ENROLL') = (role IN ('ADMIN', 'ATTENDEE')))
);

CREATE INDEX idx_event_attendees_user ON event_attendees (user_id, status, event_id);
CREATE INDEX idx_event_attendees_status ON event_attendees (event_id, status, created_at);

CREATE TABLE chat_rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id UUID REFERENCES users(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('DIRECT', 'GROUP')),
    direct_key TEXT UNIQUE,
    next_seq BIGINT NOT NULL DEFAULT 0 CHECK (next_seq >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((type = 'DIRECT') = (direct_key IS NOT NULL))
);

CREATE TABLE chat_participants (
    chat_room_id UUID NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_read_seq BIGINT NOT NULL DEFAULT 0 CHECK (last_read_seq >= 0),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chat_room_id, user_id)
);

CREATE INDEX idx_chat_participants_user ON chat_participants (user_id, chat_room_id);

CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_room_id UUID NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    sender_id UUID REFERENCES users(id) ON DELETE SET NULL,
    room_seq BIGINT NOT NULL CHECK (room_seq > 0),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (chat_room_id, room_seq)
);

CREATE INDEX idx_chat_messages_sender ON chat_messages (sender_id);

CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    receiver_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sender_id UUID REFERENCES users(id) ON DELETE SET NULL,
    user_notif_seq BIGINT NOT NULL CHECK (user_notif_seq > 0),
    type TEXT NOT NULL,
    content TEXT NOT NULL,
    entity_id UUID,
    is_read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE (receiver_id, user_notif_seq)
);

CREATE INDEX idx_notifications_sender ON notifications (sender_id);
CREATE INDEX idx_notifications_receiver_read_created
    ON notifications (receiver_id, is_read, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id UUID NOT NULL REFERENCES media_objects(id) ON DELETE CASCADE,
    post_id UUID REFERENCES posts(id) ON DELETE CASCADE,
    comment_id UUID REFERENCES comments(id) ON DELETE CASCADE,
    chat_message_id UUID REFERENCES chat_messages(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (num_nonnulls(post_id, comment_id, chat_message_id) = 1)
);

CREATE INDEX idx_attachments_media ON attachments (media_id);
CREATE UNIQUE INDEX idx_attachments_post_media ON attachments (post_id, media_id) WHERE post_id IS NOT NULL;
CREATE UNIQUE INDEX idx_attachments_comment_media ON attachments (comment_id, media_id) WHERE comment_id IS NOT NULL;
CREATE UNIQUE INDEX idx_attachments_message_media ON attachments (chat_message_id, media_id) WHERE chat_message_id IS NOT NULL;

-- +goose Down
DROP TABLE attachments;
DROP TABLE notifications;
DROP TABLE chat_messages;
DROP TABLE chat_participants;
DROP TABLE chat_rooms;
DROP TABLE event_attendees;
DROP TABLE events;
DROP TABLE follows;
DROP TABLE bookmarks;
DROP TABLE likes;
DROP TABLE comments;
DELETE FROM posts WHERE group_id IS NOT NULL;
ALTER TABLE posts DROP COLUMN group_id;
DROP TABLE group_members;
DROP TABLE groups;
DROP TABLE media_objects;
DROP TYPE media_status;
DROP TYPE media_visibility;
ALTER TABLE users DROP COLUMN next_notif_seq;
