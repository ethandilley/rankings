-- name: ListRankings :many
SELECT owner, player_name, rank
FROM rankings
ORDER BY owner, rank;

-- name: DeleteRankingsByOwner :exec
DELETE FROM rankings
WHERE owner = $1;

-- name: InsertRanking :exec
INSERT INTO rankings (owner, player_name, rank)
VALUES ($1, $2, $3);

-- name: ListRankingsByOwnerForUpdate :many
SELECT owner, player_name, rank
FROM rankings
WHERE owner = $1
ORDER BY rank
FOR UPDATE;

-- name: UpdateRankingRank :exec
UPDATE rankings
SET rank = $3
WHERE owner = $1 AND player_name = $2;

-- name: DeleteRanking :exec
DELETE FROM rankings
WHERE owner = $1 AND player_name = $2;
