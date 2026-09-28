-- name: ListPlayers :many
SELECT id, drafted_by_username, player_name, position, team
FROM players
ORDER BY position, player_name;

-- name: GetPlayerByID :one
SELECT id, drafted_by_username, player_name, position, team
FROM players
WHERE id = $1;

-- name: SearchPlayersByName :many
-- Caller passes a pre-wildcarded pattern, e.g. "%smith%".
SELECT id, drafted_by_username, player_name, position, team
FROM players
WHERE player_name ILIKE $1
ORDER BY player_name
LIMIT 20;

-- name: FindPlayersByName :many
SELECT id, drafted_by_username, player_name, position, team
FROM players
WHERE lower(player_name) = lower($1);
