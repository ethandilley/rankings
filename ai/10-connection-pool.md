# 10 — Replace the single `pgx.Conn` with a `pgxpool.Pool`

**Goal:** stop the server from dying whenever its one database connection breaks. After this change, refreshing the page, a Postgres restart, or an idle-connection drop must never leave the server returning 500s until someone runs `docker compose restart server`.

**Effort:** S–M. Mostly mechanical type changes, plus one audit step that needs judgment. **Depends on:** nothing.

## The problem

`cmd/server/main.go` opens exactly one connection with `pgx.Connect` and hands that same `*pgx.Conn` to every service (`auth`, `rankings`, `players`, `admin`, `syncstatus`, `trades`, `espn`). That causes three failure modes:

1. **Cancelled requests kill the connection.** When an HTTP request's context is cancelled while a query is running (the browser does this on refresh or navigation, and the pages fire several requests at once on load), pgx closes the connection. A closed `pgx.Conn` never reconnects, so every later query fails.
2. **Concurrent requests are unsafe.** A `pgx.Conn` is not safe for concurrent use. Overlapping requests can fail with `conn busy`.
3. **Errors are mislabeled.** `RequireAuth` (`internal/server/auth/auth.go`) writes the body `unauthorized` even when the status is 500 (a database error during session lookup), so a database outage looks like an auth problem.

Observed symptoms, all cleared only by `docker compose restart server`:

- `POST /auth/login` returns `internal server error`.
- `GET /auth/me` returns `unauthorized` (no cookie, because login failed).
- The UI shows "Something went wrong on the server. Try again." on every action.
- `GET /players` fails, which `loadPlayers()` in `web/index.html` silently swallows, so seeding reports "180 of 180 draft-order players could not be matched".

## Before you start: reproduce it

Do this first so you can confirm the fix actually fixes the thing.

```bash
docker compose up -d --build
# create a user if none exists:
DB_URL="postgres://user:password@localhost:5432/rankings?sslmode=disable" \
  go run ./cmd/adminuser --username test --display-name test --password test --admin

# log in via the browser at http://localhost:8081/login.html, then hard-refresh
# the board page several times quickly (Cmd/Ctrl+Shift+R, or reload mid-load).
# Then:
curl -i -c jar -H 'Content-Type: application/json' \
  -d '{"username":"test","password":"test"}' http://localhost:8081/auth/login
```

If the bug reproduces you'll see a 500 `internal server error`. If it doesn't reproduce on the first try, you can force the same failure mode by restarting Postgres underneath the running server: `docker compose restart postgres`, wait for it to be healthy, then repeat the `curl`. Before the fix this returns 500; after the fix it must return 200.

## Scope

**In scope**

- Every constructor that takes `*pgx.Conn`: `auth.NewAuthService`, `rankings.NewRankingsService`, `players.NewPlayersService`, `admin.NewAdminService`, `syncstatus.New`, `trades.NewTradesService`, `espn.New`.
- `cmd/server/main.go` and `cmd/espn-sync/main.go`, which create the connection and pass it to those constructors.
- `go.mod` / `go.sum` and, if needed, the Dockerfile so the image still builds.
- The transaction audit in step 4.

**Out of scope. Do not do these:**

- `cmd/adminuser/main.go` and `cmd/rankings-snapshot/main.go` keep their own one-shot `pgx.Connect`. They are short-lived CLIs that don't pass the connection to any changed constructor.
- Do not change schema, migrations, SQL queries, or sqlc output.
- Do not touch frontend code.
- Do not add retries, circuit breakers, health endpoints, or new config knobs. Pool defaults are fine.

## Steps

### 1. Change the connection type in the services

Replace `*pgx.Conn` with `*pgxpool.Pool` in the struct fields and constructor parameters of the seven services listed above (find them with `grep -rn '\*pgx\.Conn' internal`).

Fix imports. Add `"github.com/jackc/pgx/v5/pgxpool"`. Keep `"github.com/jackc/pgx/v5"` in any file that still uses it for `pgx.ErrNoRows`, `pgx.Tx`, and similar. Remove it from files that no longer do. `goimports -w internal` handles both.

Why this compiles without further changes: `db.New(...)` takes a `DBTX` interface that a pool satisfies, `Pool.Begin(ctx)` returns a `pgx.Tx` just like `Conn.Begin`, and `queries.WithTx(tx)` works as before.

### 2. Create a pool instead of a connection

In `cmd/server/main.go`:

```go
conn, err := pgxpool.New(ctx, dbURL)   // was: pgx.Connect(ctx, dbURL)
if err != nil {
    log.Fatalf("failed to connect to db: %v", err)
}
defer conn.Close()                     // was: conn.Close(context.Background())
```

Note that `pgxpool.New` does not dial immediately. For fail-fast behavior at startup, add `if err := conn.Ping(ctx); err != nil { log.Fatalf("failed to reach db: %v", err) }` right after. Keep the variable name `conn` to minimize the diff, or rename to `pool` everywhere; either is fine, but be consistent.

Make the equivalent two changes in `cmd/espn-sync/main.go`, since it passes its connection to `espn.New`. Use `context.Background()` where that file already does.

### 3. Dependencies and build

```bash
go mod tidy
go build ./... && go vet ./... && go test ./...
```

`pgxpool` ships inside the existing `github.com/jackc/pgx/v5` module but pulls in new transitive dependencies (`puddle`, `golang.org/x/sync`), so `go.sum` will change. Commit both `go.mod` and `go.sum`.

**Dockerfile gotcha.** The current Dockerfile runs `COPY go.mod ./` with the `go.sum` copy commented out, then `go mod download`. If `docker compose build` fails with missing-checksum errors after the tidy, switch that line to `COPY go.mod go.sum ./`. Also note `go.mod` declares `go 1.27.0`; the toolchain you use must match it or be able to download it.

Tests do not appear to reference `pgx.Conn`. If `go vet` shows otherwise, update them to the pool type.

### 4. Audit transactions (the step that needs judgment)

With one connection, requests were effectively (and unsafely) serialized. With a pool they truly run in parallel, so any read-modify-write that was only "safe" by accident is now a race. Read through every handler in `internal/server/rankings/rankings.go`, `trades/trades.go`, `admin/admin.go`, and `internal/espn/sync.go`, and check two things:

1. **Inside a `withTx` / `withOwnerRowsTx` callback, use only the tx-bound `q` that the callback receives, never `h.q`.** `h.q` is bound to the pool and would run on a different connection, outside the transaction. That means it can't see uncommitted writes and can deadlock against the transaction's own locks. From a read of the current code the move/replace/add paths already follow this (`replaceRankings`, `movePlayer`, `applyRankOrder` all use the passed-in `q`). Confirm for every path, including remove, tier-break, and trade handlers.
2. **Any multi-statement write that is not in a transaction.** If a handler does select-then-update/insert across separate calls, either wrap it in `withTx` or confirm the unique constraints make it safe (`rankings_owner_player_unique` on `(owner, player_id)` and `players_name_team_unique` on `(player_name, team)` already guard some cases). Fix real races. Do not refactor code that is already correct.

Rank moves rely on `ListRankingsByOwnerForUpdate` (`SELECT ... FOR UPDATE`) to serialize per owner; preserve that.

### 5. Verify

All of these must pass:

- `go build ./...`, `go vet ./...`, `go test ./...` are green.
- `docker compose up -d --build` starts cleanly and the server logs `server listening on :8080`.
- **Postgres-restart test (the main one).** With the server running and a session created via `curl -c jar ... /auth/login`, run `docker compose restart postgres`, wait until it's healthy, then `curl -b jar http://localhost:8081/auth/me`. It must return the user JSON, or at worst one failed request followed by recovery on the next. It must not require restarting the server container.
- **Refresh test.** Reload the board page about 10 times in quick succession, then log in with `curl`. Must return 200.
- **Concurrency smoke test.** Fire a burst of parallel `GET /rankings` and `GET /players` requests (for example `seq 20 | xargs -P 20 -I{} curl -s -o /dev/null -w '%{http_code}\n' -b jar http://localhost:8081/players`). All must return 200. Keep the burst of mutating requests (`PATCH .../move`) under the rate limit (5/sec per session, burst 10) so you don't confuse a 429 with a bug.
- **Move correctness under the pool.** Seed a board, move a player up and down a few times via the UI or `curl`, and confirm ranks stay a contiguous `1..N` with no duplicates or gaps.

### 6. Optional, separate commit: stop lying about errors

In `internal/server/auth/auth.go`, `RequireAuth` calls `http.Error(w, "unauthorized", status)` for both 401 and 500. Make the 500 case say `internal server error`, and log the underlying error (`log.Printf`) in `userFromRequest` where it currently swallows it. This changes no behavior for real 401s and makes the next database problem obvious in `docker compose logs server`. Keep it as its own commit so the pool change stays reviewable.

## Definition of done

- No `*pgx.Conn` remains in `internal/` or `cmd/server`, `cmd/espn-sync`.
- `go.mod` and `go.sum` are updated and committed, and `docker compose up -d --build` works from a clean checkout.
- The Postgres-restart and refresh tests above pass without restarting the server container.
- The transaction audit is done, with a short note in your final message listing each handler you checked and anything you changed.
- Append an entry to `NOTES_PROGRESS.md` in the existing format: what was done, what was verified, what was skipped, commit hash.

## Notes for the agent

- You cannot rely on the server logs to see the underlying error today, because handlers return generic 500s without logging. Don't conclude "no errors" from a quiet log.
- The first ESPN sync populates `players` from rosters only, and the sync deletes players not on any roster unless a ranking references them. Don't resync against a dev database you've hand-seeded without checking the sync's dry run first.
- If something in this doc contradicts what you find in the code, trust the code, make the sensible call, and say so in your final message.
