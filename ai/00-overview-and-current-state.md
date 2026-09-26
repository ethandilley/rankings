# 00 — Project Overview & Current State

Read this before touching any other doc in this folder. It's the shared context every other markdown assumes you already have.

## What this project is

A fantasy football league (12 known members) wants a shared web app where:
- Each owner can view everyone's rankings.
- Each owner can **only edit their own** ranking list.
- Rankings should eventually support tiers, per-position views, and a "biggest disagreements" trade-suggestion page.

## Stack as it exists today

- **Backend**: Go, `net/http` (stdlib router, Go 1.22+ pattern matching like `GET /rankings`), `pgx/v5` for Postgres, `sqlc` for generated query code.
- **DB**: Postgres 18, migrations via `goose` (`db/migrations/*.sql`).
- **Frontend**: a single static `index.html` with vanilla JS, no build step, no framework. It calls the Go API directly via `fetch`, with the API base URL configurable in-browser (stored in `localStorage`).
- **Deploy**: `docker compose` — one `server` container, one `postgres` container.

## What already works

- `rankings` table: `id, owner (text), player_name (text), rank (int), created_at`.
- Full CRUD on one owner's list:
  - `GET /rankings` — every owner's full list, grouped.
  - `POST /rankings` — full replace of one owner's list.
  - `PATCH /rankings/{owner}/move` — move one player to a new rank, shifting others.
  - `POST /rankings/{owner}/players` — insert a player at a given rank (or end).
  - `DELETE /rankings/{owner}/players/{player}` — remove a player, close the gap.
- All of the above run inside a Postgres transaction with `FOR UPDATE` row locking on the owner's rows, so concurrent edits to the *same* owner's list can't corrupt rank ordering. This part is solid — don't rewrite it, build on it.
- The frontend renders this correctly: owner list on the left, ranked sheet on the right, up/down arrows, add/remove forms, toast notifications, a settings dialog for pointing at a different API host.

## What's broken right now (fix before or alongside new features)

1. **`internal/server/players/players.go` does not compile.** `getPlayers` writes `out` but `out` is never defined — the handler body was never finished. It also has copy-pasted helper functions (`withOwnerRowsTx`, `applyRankOrder`, etc.) left over from `rankings.go` that don't belong in a read-only players service and won't compile against `PlayersService`.
2. **`cmd/server/main.go` references a package it never imports.** It calls `players.NewPlayerService(conn)` — wrong name (`NewPlayersService` is what's defined, singular vs. plural mismatch) and there's no `import ".../internal/server/players"` line at all, and the players service is never `Register`'d on the mux even if it were fixed.
3. **`db/queries/players.sql` and `db/queries/rankings.sql` are byte-identical duplicates.** Both define the same five `rankings`-table queries under the same names, which will make `sqlc generate` either fail or silently pick one arbitrarily. Delete one; the queries in it belong conceptually to rankings, not players. Any *actual* players-table queries (`ListPlayers`, `GetPlayerByID`, etc.) still need to be written.
4. **The `players` table is populated but never used by the app.** `scripts/load_players.py` syncs real ESPN roster data into it (owner, position, team, draft slot) but the rankings API and frontend never join against it — ranking entries are just raw typed strings like `"Jahmyr Gibbs (DET, RB)"`. This means there's no reliable way to know a ranked entry's position, no autocomplete, and no protection against typos creating phantom "players." **This is the single biggest structural fix and it blocks the positional-rankings and trade-suggestion docs below.** See `02-data-model-and-players-integration.md`.
5. **No authentication whatsoever.** `owner` is a free-text path/body parameter. Anyone with the URL can pass any owner name and edit that person's board. This directly contradicts the stated goal ("12 members log in and edit only their own rankings") and should be treated as priority #1. See `01-authentication.md`.
6. **CORS is wide open** (`Access-Control-Allow-Origin: *`) with no auth to compensate. Fine for a pre-auth prototype, not fine once real people's session cookies are involved. Addressed in `01-authentication.md` and `07-security-hardening.md`.
7. **A live ESPN session cookie is committed in `.envrc`** (`ESPN_S2`, `ESPN_SWID`) inside the git repo. Treat that ESPN login session as burned — log out/back in on the ESPN Fantasy site to invalidate the old cookie, then keep `.envrc` out of git going forward (it's currently *not* in `.gitignore`). Don't reuse the literal values found in this repo snapshot for anything.
8. **`LEAGUE_TEAMS` (fantasy team nicknames) and the ESPN sync's `team_to_owner` map (real people's names) are two different identity systems for the same 12 people**, and nothing reconciles them. The rankings table's `owner` column has been fed both team-nickname strings (via the "seed all 12 teams" button) and would presumably need to be fed real names once login exists. Pick **one** canonical identity per owner (recommendation: real name or a stable username, not the fantasy team nickname — nicknames change every season) before building auth. This is called out again in `01-authentication.md`.

## Recommended build order

The docs are numbered in the order we'd suggest tackling them, because later ones depend on earlier ones:

1. `01-authentication.md` — nobody should touch prod data ownership rules until this exists.
2. `02-data-model-and-players-integration.md` — rankings need to reference real player rows, not free text, before tiers/positions/trades can be built cleanly.
3. `03-positional-rankings.md`
4. `04-tiers.md`
5. `05-trade-suggestions.md` — depends on 02, 03, and ideally 04.
6. `06-espn-sync-service.md` — fixes the broken players service + automates roster sync.
7. `07-security-hardening.md` — can happen in parallel with anything, but do it before sharing the URL with all 12 people.
8. `08-consensus-and-analytics.md` — optional/stretch, nice-to-have once the above exists.

## Ground rules for every doc in this set

- **Twelve users, known in advance.** This is not a public SaaS product. Don't build subscription billing, email verification flows, password-reset-via-support-ticket infrastructure, or anything sized for strangers signing up. Optimize for "commissioner sets up 12 accounts once."
- **Single Postgres instance, single Go binary.** Don't introduce Redis, a second microservice, Kafka, etc. unless a doc explicitly says so.
- **No framework migration.** The frontend stays a single static HTML file unless a doc says otherwise (tiers and trade-suggestions may reasonably want a second static HTML page — that's fine, that's not a framework).
- **Every migration is additive and reversible.** Use goose's `-- +goose Up` / `-- +goose Down` pairs, matching the existing style in `db/migrations/`.
- **Every new query goes through sqlc**, matching the existing `db/queries/*.sql` → `internal/db/*.sql.go` pattern. Don't hand-write raw SQL string building in handlers.
