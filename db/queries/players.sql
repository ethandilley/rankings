-- name: ListPlayers :many
SELECT id, owner, player_name, position, team, drafted_at
FROM players
ORDER BY owner, player_name;
