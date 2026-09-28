# Auth Implementation Notes

This repository now has:

- Core login/logout/session authentication.
- Auth-protected `/rankings` and `/players` APIs.
- A minimal login page.
- A main web UI served by the Go server.
- An admin-only user-management API.
- A minimal admin web page.
- A CLI for provisioning the initial league users.
- An owner-backfill migration that moves legacy free-text owners to canonical usernames.

## Verification performed

```sh
go build ./...
/home/ethan/go/bin/sqlc generate
```

Both commands pass.

Not yet verified:

- Live API behavior against Postgres.
- Browser login flow.
- Admin page behavior against a live server.
- `0004_backfill_owner.sql` against a real database.

Local database verification is currently blocked because `.envrc` points Postgres at `localhost`, and the local Postgres connection is refused.

## Authentication model

- No open signup.
- The commissioner creates the 12 league-member accounts up front.
- Users log in with `username` and `password`.
- Successful login creates a `sessions` row and sets an HTTP-only `session` cookie.
- Logout deletes the current session row and clears the cookie.
- `/auth/me` returns the currently authenticated user.
- `/auth/password` lets the authenticated user change their own password.

Canonical username convention:

- Use lowercase first names as usernames.
- Display names are human-readable and may include spaces.
- Ethan’s display name is `Ethan Stone`.

## Auth routes

### POST /auth/login

Request:

```json
{
  "username": "ethan",
  "password": "password"
}
```

Success:

- `200 OK`
- Body: `{"status":"ok"}`
- Sets `session` cookie:
  - `Path=/`
  - `HttpOnly=true`
  - `Secure=false`
  - `SameSite=Lax`
  - `Max-Age=2592000` (30 days)

Errors:

- `400` for missing/invalid JSON
- `401` for invalid username or password

### POST /auth/logout

Success:

- `200 OK`
- Body: `{"status":"ok"}`
- Deletes the `session` cookie

Errors:

- `401` if no valid session

### GET /auth/me

Success:

- `200 OK`

Response:

```json
{
  "id": 1,
  "username": "ethan",
  "display_name": "Ethan Stone",
  "is_admin": true
}
```

Errors:

- `401` if no valid session

### PATCH /auth/password

Request:

```json
{
  "current_password": "oldpassword",
  "new_password": "newpassword"
}
```

Success:

- `200 OK`
- Body: `{"status":"ok"}`
- Invalidates all existing sessions for the user
- Creates a new session and sets a new `session` cookie

Errors:

- `400` for missing/invalid JSON
- `401` for no valid session or wrong current password
- `500` for unexpected database errors

## Admin user-management API

Admin routes are in `/home/ethan/code/rankings/internal/server/admin/admin.go`.

All admin routes require:

1. A valid authenticated session.
2. `users.is_admin = true` for the current user.

Non-admin authenticated users receive `403 Forbidden`.

### GET /admin/users

Returns all users.

Success:

- `200 OK`

Response:

```json
[
  {
    "id": 1,
    "username": "ethan",
    "display_name": "Ethan Stone",
    "is_admin": true,
    "created_at": "2026-09-27T12:34:56Z"
  }
]
```

Errors:

- `500` for unexpected database errors

### POST /admin/users

Creates a new user.

Request:

```json
{
  "username": "ethan",
  "display_name": "Ethan Stone",
  "password": "password",
  "is_admin": true
}
```

Rules:

- `username`, `display_name`, and `password` are required.
- Username is normalized with `strings.ToLower(strings.TrimSpace(username))`.
- Username must match `^[a-z0-9_-]+$`.
- `is_admin` is optional and defaults to `false`.
- Password is hashed with bcrypt cost `12`.

Success:

- `201 Created`
- Returns the created user object.

Errors:

- `400` for invalid JSON or missing/invalid fields
- `409` for duplicate username
- `500` for unexpected database errors

### POST /admin/users/{username}/password

Resets a user’s password.

Request:

```json
{
  "new_password": "newpassword"
}
```

Rules:

- Path username is normalized with `strings.ToLower(strings.TrimSpace(username))`.
- `new_password` is required.
- Password is hashed with bcrypt cost `12`.

Success:

- `200 OK`
- Body: `{"status":"ok"}`

Errors:

- `400` for invalid JSON or missing fields
- `404` if target user does not exist
- `500` for unexpected database errors

### PATCH /admin/users/{username}/admin

Promotes or demotes a user.

Request:

```json
{
  "is_admin": true
}
```

Rules:

- Path username is normalized.
- An admin cannot remove their own admin role.
- The last remaining admin cannot be demoted.

Success:

- `200 OK`
- Returns the updated user object.

Errors:

- `400` for invalid JSON or empty username
- `403` if the target is the current user and `is_admin` is `false`
- `404` if target user does not exist
- `409` if demoting would leave zero admins
- `500` for unexpected database errors

### DELETE /admin/users/{username}

Deletes a user.

Rules:

- Path username is normalized.
- An admin cannot delete their own account.
- The last remaining admin cannot be deleted.
- Sessions cascade-delete because `sessions.user_id` references `users(id)` with `ON DELETE CASCADE`.
- `rankings.owner` and `players.owner` are free-text and are not foreign-keyed to `users`, so deleting a user does not delete boards or players.

Success:

- `204 No Content`

Errors:

- `400` for empty username
- `403` if deleting the current user
- `404` if target user does not exist
- `409` if deleting would leave zero admins
- `500` for unexpected database errors

## User provisioning CLI

The CLI lives in `/home/ethan/code/rankings/cmd/adminuser/main.go`.

Build and run:

```sh
go build -o /tmp/rankings-adminuser ./cmd/adminuser
/tmp/rankings-adminuser
```

Environment:

- `DB_URL` is required.
- `NEW_USERNAME` is optional; defaults to `ethan`.
- `NEW_DISPLAY_NAME` is optional; defaults to the trimmed username.
- `NEW_PASSWORD` is optional; if empty, a 12-character random password is generated and printed once.
- `NEW_IS_ADMIN` is optional; defaults to `true`.

Behavior:

- Creates one user.
- Prints the generated password to stdout if one was generated.
- If the username already exists, reports the conflict and does not modify existing data.

This CLI is intended for the initial 12-user league provisioning, not for normal runtime signup.

## Static frontend and CORS

`/home/ethan/code/rankings/cmd/server/main.go` serves the frontend from:

```sh
WEB_DIR
```

If `WEB_DIR` is unset, it defaults to `web`.

Static files are mounted at `/`:

```go
mux.Handle("/", http.FileServer(http.Dir(staticDir())))
```

Frontend files are under `/home/ethan/code/rankings/web/`:

- `web/index.html`
- `web/login.html`
- `web/admin.html`

CORS:

- `ALLOWED_ORIGINS` is preferred.
- `ALLOWED_ORIGIN` is supported as a legacy fallback.
- If neither is set, the default allowlist is `http://localhost:8080`.
- `ALLOWED_ORIGINS` is comma-separated.
- For allowed origins, the server echoes the request `Origin`, sets `Access-Control-Allow-Credentials: true`, and adds `Vary: Origin`.
- CORS headers are not set for non-allowed origins.

The frontend uses `localStorage` key `rankings_api_base` if present. Otherwise it derives the default API base from `window.location.origin` for HTTP/HTTPS pages, with `http://localhost:8080` as the fallback for non-secure local pages.

## Admin web page

`/home/ethan/code/rankings/web/admin.html` is a minimal admin UI.

Behavior:

- Calls `GET /auth/me` on load.
- If the current user is not admin, it shows a forbidden state.
- If the current user is admin, it loads `GET /admin/users` and renders a table.
- Provides an add-user form.
- Provides password reset, admin toggle, and delete actions for each user.
- Has a `boards` button linking to `index.html`.
- Has a `logout` button that calls `POST /auth/logout` and redirects to `login.html`.

`web/index.html` shows an `admin` header link only when `currentUser.is_admin` is true. Clicking it navigates to `admin.html`.

## Owner backfill migration

`/home/ethan/code/rankings/db/migrations/0004_backfill_owner.sql` migrates legacy free-text `owner` values in `rankings.owner` and `players.owner` to canonical usernames.

Properties:

- Idempotent.
- Case- and trim-insensitive matching.
- Skips rows already equal to the new canonical owner.
- Does not rewrite players owned by the commissioner.

Known mapping includes:

- `Ethan Stone` → `ethan`
- `Chris` → `chris`
- `Dave` → `dave`
- `Drew` → `drew`
- `Jared` → `jared`
- `Justin` → `justin`
- `Matt` → `matt`
- `Nick` → `nick`
- `Ryan` → `ryan`
- `T.J.` → `tj`
- `Tyler` → `tyler`
- `Zack` → `zack`
- `Zach` → `zach`

The down migration is intentionally a no-op because legacy owner values cannot be reliably reconstructed after the backfill.

Run migrations with:

```sh
make migrate
```

## SQL and sqlc

Auth/admin queries live in `/home/ethan/code/rankings/db/queries/auth.sql`.

New admin queries:

```sql
-- name: ListUsers :many
-- name: SetUserAdmin :exec
-- name: DeleteUser :exec
-- name: CountAdmins :one
```

Generated code lives in `/home/ethan/code/rankings/internal/db/auth.sql.go`.

After changing SQL queries, run:

```sh
/home/ethan/go/bin/sqlc generate
```

## Build and repo state

As of the last check:

```sh
go build ./...
/home/ethan/go/bin/sqlc generate
```

Both pass.

Relevant files:

- `cmd/server/main.go`
- `cmd/adminuser/main.go`
- `internal/server/auth/auth.go`
- `internal/server/admin/admin.go`
- `internal/server/rankings/rankings.go`
- `internal/server/players/players.go`
- `internal/db/models.go`
- `internal/db/auth.sql.go`
- `internal/db/players.sql.go`
- `db/queries/auth.sql`
- `db/queries/players.sql`
- `db/migrations/0001_init.sql`
- `db/migrations/0002_add_players.sql`
- `db/migrations/0003_auth.sql`
- `db/migrations/0004_backfill_owner.sql`
- `web/index.html`
- `web/login.html`
- `web/admin.html`

## Remaining gaps

- Run migrations against a real database.
- Create the initial league users with `cmd/adminuser`.
- Verify login, logout, password change, and admin user management in a browser against the running server.
- Verify `0004_backfill_owner.sql` against a database containing legacy owner values.
- Decide whether `web/index.html` should automatically redirect unauthenticated users to `login.html`.
- Decide whether the admin page should include richer confirmation dialogs or inline forms.
