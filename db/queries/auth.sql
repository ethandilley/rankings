-- name: GetUserByUsername :one
SELECT id, username, display_name, password_hash, is_admin, created_at
FROM users
WHERE username = $1;

-- name: CreateUser :one
INSERT INTO users (username, display_name, password_hash, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, display_name, password_hash, is_admin, created_at;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2
WHERE username = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT s.token, s.user_id, s.expires_at, u.username, u.display_name, u.is_admin
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token = $1;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < now();

-- name: ListUsers :many
SELECT id, username, display_name, is_admin, created_at
FROM users
ORDER BY username;

-- name: SetUserAdmin :exec
UPDATE users
SET is_admin = $2
WHERE username = $1;

-- name: DeleteUser :exec
DELETE FROM users
WHERE username = $1;

-- name: CountAdmins :one
SELECT count(*) FROM users
WHERE is_admin = true;
