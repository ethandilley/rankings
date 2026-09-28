-- +goose Up
ALTER TABLE rankings ADD COLUMN player_id BIGINT REFERENCES players(id);

-- Best-effort automatic backfill by exact name match. This WILL leave rows
-- unmatched (seeded names carry a " (TEAM, POS)" suffix, plus players not in
-- the players table) — those are handled by the one-off backfill tool
-- (cmd/backfill-players) before migration 0007 tightens the schema.
UPDATE rankings r
SET player_id = p.id
FROM players p
WHERE r.player_id IS NULL
  AND lower(trim(r.player_name)) = lower(trim(p.player_name));

-- +goose Down
ALTER TABLE rankings DROP COLUMN player_id;
