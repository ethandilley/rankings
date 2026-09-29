-- name: ListTierBreaks :many
SELECT position, before_rank, label
FROM tier_breaks
WHERE owner = $1
ORDER BY position, before_rank;

-- name: ListTierBreaksAllOwners :many
SELECT owner, position, before_rank, label
FROM tier_breaks
ORDER BY owner, position, before_rank;

-- name: DeleteTierBreaksForScope :exec
DELETE FROM tier_breaks
WHERE owner = $1 AND position = $2;

-- name: InsertTierBreak :exec
INSERT INTO tier_breaks (owner, position, before_rank, label)
VALUES (@owner, @position, @before_rank, sqlc.narg(label));
