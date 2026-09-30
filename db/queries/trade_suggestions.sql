-- name: ListNormalizedRankings :many
-- Every (owner, player) rank with the owner's list size, restricted to owners
-- who ranked at least min_ranked_players players. normalized_rank = rank / total
-- (0.0 = best, 1.0 = worst) so owners with different list sizes are comparable.
SELECT
    r.owner,
    r.player_id,
    r.rank,
    ot.total,
    (r.rank::float8 / ot.total::float8)::float8 AS normalized_rank
FROM rankings r
JOIN (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
    HAVING COUNT(*) >= sqlc.arg(min_ranked_players)::int
) ot ON ot.owner = r.owner
ORDER BY r.owner, r.rank;

-- name: ListDraftedPlayers :many
-- Players linked to an owner via the draft, for ownership checks in
-- pairwise trade suggestions.
SELECT id, player_name, position, team, drafted_by_username
FROM players
WHERE drafted_by_username IS NOT NULL
ORDER BY id;

-- name: ListExcludedOwners :many
-- Owners present in rankings but below the min_ranked_players threshold,
-- surfaced in responses so the exclusion is visible rather than silent.
SELECT owner
FROM (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
) e
WHERE e.total < sqlc.arg(min_ranked_players)::int
ORDER BY owner;

-- name: PlayerDisagreement :many
-- Disagreement leaderboard: players ranked by at least two qualifying owners,
-- spread = max(normalized_rank) - min(normalized_rank). Ties on spread break
-- by player name for stable output.
WITH owner_totals AS (
    SELECT owner, COUNT(*) AS total
    FROM rankings
    GROUP BY owner
    HAVING COUNT(*) >= sqlc.arg(min_ranked_players)::int
),
ranked AS (
    SELECT
        r.owner,
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
    COUNT(*)::int AS num_owners_ranked,
    MIN(rk.normalized_rank)::float8 AS best_normalized_rank,
    MAX(rk.normalized_rank)::float8 AS worst_normalized_rank,
    (MAX(rk.normalized_rank) - MIN(rk.normalized_rank))::float8 AS disagreement_spread,
    (array_agg(rk.owner ORDER BY rk.normalized_rank, rk.owner))[1]::text AS highest_rater,
    (array_agg(rk.owner ORDER BY rk.normalized_rank DESC, rk.owner))[1]::text AS lowest_rater
FROM ranked rk
JOIN players p ON p.id = rk.player_id
GROUP BY p.id, p.player_name, p.position, p.team
HAVING COUNT(*) >= 2
ORDER BY disagreement_spread DESC, p.player_name ASC
LIMIT sqlc.arg(row_limit)::int;
