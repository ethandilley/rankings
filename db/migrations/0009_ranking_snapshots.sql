-- +goose Up
CREATE TABLE ranking_snapshots (
    id          BIGSERIAL PRIMARY KEY,
    taken_at    TIMESTAMPTZ NOT NULL,
    owner       TEXT NOT NULL,
    player_id   BIGINT NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    rank        INT NOT NULL,
    owner_total INT NOT NULL,
    CONSTRAINT ranking_snapshots_owner_player_taken UNIQUE (owner, player_id, taken_at),
    CONSTRAINT ranking_snapshots_positive CHECK (rank > 0 AND owner_total > 0)
);

CREATE INDEX idx_ranking_snapshots_owner_taken ON ranking_snapshots (owner, taken_at DESC);
CREATE INDEX idx_ranking_snapshots_player_taken ON ranking_snapshots (player_id, taken_at);

-- +goose Down
DROP TABLE ranking_snapshots;
