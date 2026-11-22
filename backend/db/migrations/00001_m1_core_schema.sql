-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE user_role AS ENUM ('USER', 'ADMIN');
CREATE TYPE sentiment_type AS ENUM ('GOOD', 'MODERATE', 'BAD');

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_name       TEXT NOT NULL UNIQUE,
    nick_name       TEXT NOT NULL UNIQUE,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    role            user_role NOT NULL DEFAULT 'USER',
    bio             TEXT,
    date_of_birth   TIMESTAMPTZ,
    avatar_url      TEXT,
    cover_page_url  TEXT,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_user_name_trgm ON users USING GIN (user_name gin_trgm_ops);
CREATE INDEX idx_users_nick_name_trgm ON users USING GIN (nick_name gin_trgm_ops);

CREATE TABLE posts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    sentiment       sentiment_type NOT NULL DEFAULT 'MODERATE',
    content_version INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_posts_created_id_desc ON posts (created_at DESC, id DESC);
CREATE INDEX idx_posts_user_created_id_desc ON posts (user_id, created_at DESC, id DESC);
CREATE INDEX idx_posts_content_trgm ON posts USING GIN (content gin_trgm_ops);

CREATE TABLE password_reset_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_password_reset_tokens_user_id ON password_reset_tokens (user_id);
CREATE INDEX idx_password_reset_tokens_expires_at ON password_reset_tokens (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS users;

DROP TYPE IF EXISTS sentiment_type;
DROP TYPE IF EXISTS user_role;
-- +goose StatementEnd
