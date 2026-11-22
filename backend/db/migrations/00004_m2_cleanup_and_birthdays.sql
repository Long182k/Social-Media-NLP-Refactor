-- +goose Up
CREATE TABLE media_cleanup_queue (
    storage_key TEXT PRIMARY KEY,
    delete_after TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_media_cleanup_due ON media_cleanup_queue (delete_after, storage_key);

-- +goose StatementBegin
CREATE FUNCTION enqueue_deleted_media() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO media_cleanup_queue (storage_key, delete_after)
    VALUES (OLD.staging_key, GREATEST(NOW(), OLD.created_at + INTERVAL '5 minutes'))
    ON CONFLICT (storage_key) DO NOTHING;
    IF OLD.finalized_key IS NOT NULL THEN
        INSERT INTO media_cleanup_queue (storage_key, delete_after)
        VALUES (OLD.finalized_key, CASE WHEN OLD.status = 'FINALIZING'
            THEN GREATEST(NOW(), OLD.finalizing_started_at + INTERVAL '60 seconds') ELSE NOW() END)
        ON CONFLICT (storage_key) DO NOTHING;
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cleanup_deleted_media AFTER DELETE ON media_objects
FOR EACH ROW EXECUTE FUNCTION enqueue_deleted_media();

CREATE INDEX idx_users_birth_month_day ON users (
    EXTRACT(MONTH FROM date_of_birth AT TIME ZONE 'UTC'),
    EXTRACT(DAY FROM date_of_birth AT TIME ZONE 'UTC')
) WHERE is_active AND date_of_birth IS NOT NULL;
CREATE INDEX idx_users_created_at ON users (created_at DESC, id DESC);
CREATE INDEX idx_users_active_recent ON users (last_login_at DESC) WHERE is_active;

-- +goose Down
DROP INDEX idx_users_active_recent;
DROP INDEX idx_users_created_at;
DROP INDEX idx_users_birth_month_day;
DROP TRIGGER cleanup_deleted_media ON media_objects;
DROP FUNCTION enqueue_deleted_media();
DROP TABLE media_cleanup_queue;
