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

-- name: ListPlayerRankings :many
WITH owner_totals AS (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
)
SELECT
    r.owner,
    r.rank AS overall_rank,
    RANK() OVER (PARTITION BY r.owner, p.position ORDER BY r.rank) AS positional_rank,
    ot.total AS owner_total,
    (r.rank::float8 / ot.total::float8)::float8 AS normalized_rank
FROM rankings r
JOIN players p ON p.id = r.player_id
JOIN owner_totals ot ON ot.owner = r.owner
WHERE r.player_id = $1
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

-- name: BigBoardConsensus :many
-- League-wide crowd-sourced big board (ai/08-consensus-and-analytics.md): the
-- average of every owner's normalized overall rank (rank / their list size,
-- 0.0 = best, 1.0 = worst) per player, so owners with different list sizes
-- are comparable. Players ranked by fewer than 3 owners are excluded so the
-- "consensus" is not one or two opinions.
WITH owner_totals AS (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
),
normalized AS (
    SELECT
        r.player_id,
        (r.rank::float8 / ot.total::float8)::float8 AS normalized_rank
    FROM rankings r
    JOIN owner_totals ot ON ot.owner = r.owner
)
SELECT
    p.id AS player_id,
    p.player_name,
    p.position,
    p.team,
    (AVG(n.normalized_rank))::float8 AS avg_normalized_rank,
    COUNT(*)::int AS num_owners_ranked
FROM normalized n
JOIN players p ON p.id = n.player_id
GROUP BY p.id, p.player_name, p.position, p.team
HAVING COUNT(*) >= 3
ORDER BY AVG(n.normalized_rank) ASC, p.player_name ASC;

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

-- name: SnapshotCurrentRankings :exec
WITH owner_totals AS (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
)
INSERT INTO ranking_snapshots (taken_at, owner, player_id, rank, owner_total)
SELECT $1, r.owner, r.player_id, r.rank, ot.total
FROM rankings r
JOIN owner_totals ot ON ot.owner = r.owner;

-- name: CountSnapshotRows :one
SELECT COUNT(*)::int
FROM ranking_snapshots
WHERE taken_at = $1;

-- name: GetOwnerLatestSnapshotTakenAt :one
SELECT MAX(taken_at)
FROM ranking_snapshots
WHERE owner = $1;

-- name: ListSnapshotRanksForOwner :many
SELECT player_id, rank
FROM ranking_snapshots
WHERE owner = $1 AND taken_at = $2;

-- name: ListOwnerRankings :many
SELECT player_id, rank
FROM rankings
WHERE owner = $1
ORDER BY rank;

-- name: ListPlayerSnapshotTrend :many
SELECT
    taken_at,
    (AVG((rank::float8 / owner_total::float8)))::float8 AS avg_normalized_rank
FROM ranking_snapshots
WHERE player_id = $1
GROUP BY taken_at
ORDER BY taken_at;

