-- +goose Up
CREATE TABLE rankings (
    id          BIGSERIAL PRIMARY KEY,
    owner       TEXT NOT NULL,
    player_name TEXT NOT NULL,
    rank        INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_rankings_owner ON rankings (owner);

-- +goose Down
DROP TABLE rankings;
