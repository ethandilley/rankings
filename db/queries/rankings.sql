-- name: ListRankingsWithPositionalRank :many
-- Positional rank is derived, not stored: the player's rank within their
-- position, computed at query time from the overall (big-board) order.
-- RANK() (not DENSE_RANK) so future ties read like a draft board (1,1,3).
SELECT
    r.owner,
    p.id AS player_id,
    p.player_name,
    p.position,
    p.team,
    r.rank AS overall_rank,
    RANK() OVER (PARTITION BY r.owner, p.position ORDER BY r.rank) AS positional_rank
FROM rankings r
JOIN players p ON p.id = r.player_id
ORDER BY r.owner, r.rank;

-- name: ListRankingsWithPositionalRankFiltered :many
SELECT
    r.owner,
    p.id AS player_id,
    p.player_name,
    p.position,
    p.team,
    r.rank AS overall_rank,
    RANK() OVER (PARTITION BY r.owner, p.position ORDER BY r.rank) AS positional_rank
FROM rankings r
JOIN players p ON p.id = r.player_id
WHERE p.position = ANY(sqlc.arg(positions)::text[])
ORDER BY r.owner, r.rank;

-- name: ListDistinctPositions :many
SELECT DISTINCT position
FROM players
ORDER BY position;

-- name: ListPositionalConsensus :many
-- League-wide crowd-sourced positional big board: the average positional
-- rank per player across every owner who ranked them.
WITH positioned AS (
    SELECT
        r.owner,
        r.player_id,
        p.position,
        RANK() OVER (PARTITION BY r.owner, p.position ORDER BY r.rank) AS positional_rank
    FROM rankings r
    JOIN players p ON p.id = r.player_id
)
SELECT
    p.id AS player_id,
    p.player_name,
    p.position,
    p.team,
    (AVG(pos.positional_rank))::float8 AS avg_positional_rank,
    COUNT(*) AS owner_count
FROM positioned pos
JOIN players p ON p.id = pos.player_id
WHERE p.position = ANY(sqlc.arg(positions)::text[])
GROUP BY p.id, p.player_name, p.position, p.team
ORDER BY AVG(pos.positional_rank) ASC, p.player_name ASC;

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
