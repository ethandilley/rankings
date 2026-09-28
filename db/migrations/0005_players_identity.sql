-- +goose Up

-- players: pure identity + attributes, no ownership concept
ALTER TABLE players RENAME COLUMN owner TO drafted_by_username;
ALTER TABLE players ALTER COLUMN drafted_by_username DROP NOT NULL;

-- name alone isn't unique enough (multiple "Josh Allen"s in the NFL); team
-- disambiguates almost all real collisions.
ALTER TABLE players ADD CONSTRAINT players_name_team_unique UNIQUE (player_name, team);

-- +goose Down
ALTER TABLE players DROP CONSTRAINT players_name_team_unique;
ALTER TABLE players ALTER COLUMN drafted_by_username SET NOT NULL;
ALTER TABLE players RENAME COLUMN drafted_by_username TO owner;
