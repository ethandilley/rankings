-- +goose Up
-- Tier breaks mark "a new tier begins immediately before this rank" within a
-- (owner, position) scope. position is 'ALL' for overall-board tiers or a
-- position code ('QB'/'RB'/'WR'/'TE'/'K'/'D/ST') for positional tiers.
-- Membership is computed at read time (doc 04), so no per-player column.
CREATE TABLE tier_breaks (
    id          BIGSERIAL PRIMARY KEY,
    owner       TEXT NOT NULL,
    position    TEXT NOT NULL,
    before_rank INT NOT NULL,
    label       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, position, before_rank)
);

CREATE INDEX idx_tier_breaks_owner ON tier_breaks (owner);

-- +goose Down
DROP TABLE tier_breaks;
