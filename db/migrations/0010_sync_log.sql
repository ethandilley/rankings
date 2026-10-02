-- +goose Up
CREATE TABLE sync_log (
    id BIGSERIAL PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    source TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT,
    inserted INT NOT NULL DEFAULT 0,
    updated INT NOT NULL DEFAULT 0,
    deleted INT NOT NULL DEFAULT 0,
    unchanged INT NOT NULL DEFAULT 0,
    delete_skipped_count INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_sync_log_started_desc ON sync_log (started_at DESC, id DESC);

-- +goose Down
DROP INDEX idx_sync_log_started_desc;
DROP TABLE sync_log;
