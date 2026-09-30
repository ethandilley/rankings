-- name: SyncListPlayers :many
SELECT id, drafted_by_username, player_name, position, team, drafted_at
FROM players
ORDER BY id;

-- name: SyncInsertPlayer :exec
INSERT INTO players (drafted_by_username, player_name, position, team, drafted_at)
VALUES ($1, $2, $3, $4, $5);

-- name: SyncUpdatePlayer :exec
UPDATE players
SET drafted_by_username = $1,
    player_name = $2,
    position = $3,
    team = $4,
    drafted_at = $5
WHERE id = $6;

-- name: SyncDeletePlayer :exec
DELETE FROM players WHERE id = $1;

-- name: SyncReferencedPlayerIDs :many
SELECT DISTINCT player_id FROM rankings;
