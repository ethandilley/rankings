-- +goose Up
-- Requires every rankings row to already have a player_id (backfilled by
-- cmd/backfill-players before this migration runs).
ALTER TABLE rankings ALTER COLUMN player_id SET NOT NULL;
ALTER TABLE rankings DROP COLUMN player_name;
ALTER TABLE rankings ADD CONSTRAINT rankings_owner_player_unique UNIQUE (owner, player_id);

-- +goose Down
ALTER TABLE rankings DROP CONSTRAINT rankings_owner_player_unique;
ALTER TABLE rankings ADD COLUMN player_name TEXT;
ALTER TABLE rankings ALTER COLUMN player_id DROP NOT NULL;
