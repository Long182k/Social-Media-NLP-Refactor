-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX idx_users_user_name_lower ON users (LOWER(user_name));
CREATE UNIQUE INDEX idx_users_email_lower ON users (LOWER(email));
ALTER TABLE users ADD CONSTRAINT chk_users_user_name_no_at CHECK (POSITION('@' IN user_name) = 0);
ALTER TABLE users ADD COLUMN session_epoch BIGINT NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS session_epoch;
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_users_user_name_no_at;
DROP INDEX IF EXISTS idx_users_email_lower;
DROP INDEX IF EXISTS idx_users_user_name_lower;
-- +goose StatementEnd
