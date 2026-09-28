-- name: ListRankings :many
SELECT r.owner, p.id AS player_id, p.player_name, p.position, p.team, r.rank
FROM rankings r
JOIN players p ON p.id = r.player_id
ORDER BY r.owner, r.rank;

-- name: DeleteRankingsByOwner :exec
DELETE FROM rankings
WHERE owner = $1;

-- name: InsertRanking :exec
INSERT INTO rankings (owner, player_id, rank)
VALUES ($1, $2, $3);

-- name: ListRankingsByOwnerForUpdate :many
SELECT r.owner, p.id AS player_id, p.player_name, p.position, p.team, r.rank
FROM rankings r
JOIN players p ON p.id = r.player_id
WHERE r.owner = $1
ORDER BY r.rank
FOR UPDATE;

-- name: UpdateRankingRank :exec
UPDATE rankings
SET rank = $3
WHERE owner = $1 AND player_id = $2;

-- name: DeleteRanking :exec
DELETE FROM rankings
WHERE owner = $1 AND player_id = $2;
