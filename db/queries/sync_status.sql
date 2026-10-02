-- name: InsertSyncLog :one
INSERT INTO sync_log (
    started_at, finished_at, source, status, message,
    inserted, updated, deleted, unchanged, delete_skipped_count
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, started_at, finished_at, source, status, message, inserted, updated, deleted, unchanged, delete_skipped_count;

-- name: ListRecentSyncLog :many
SELECT id, started_at, finished_at, source, status, message, inserted, updated, deleted, unchanged, delete_skipped_count
FROM sync_log
ORDER BY started_at DESC, id DESC
LIMIT $1;

-- name: GetLatestSyncLog :one
SELECT id, started_at, finished_at, source, status, message, inserted, updated, deleted, unchanged, delete_skipped_count
FROM sync_log
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: GetLastSuccessfulSync :one
SELECT finished_at
FROM sync_log
WHERE status = 'success'
ORDER BY started_at DESC, id DESC
LIMIT 1;
