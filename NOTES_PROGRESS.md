# Progress Log

One entry per doc. Newest at the bottom. Each entry: what was done, what was
verified, what was skipped, and the commit hash.

---

## 02 — Data Model: Rankings Reference Real Players

**Commit:** `6409d7a`

### What was done
- **Migrations (all applied, `goose_db_version` = 7):**
  - `0005_players_identity.sql` — rename `players.owner` → `drafted_by_username`,
    DROP NOT NULL (undrafted/free agents have none), add
    `UNIQUE(player_name, team)`.
  - `0006_rankings_player_id.sql` — ADD nullable `rankings.player_id` FK to
    `players(id)`, best-effort exact-name backfill.
  - `0007_rankings_player_fk_strict.sql` — SET `player_id` NOT NULL, DROP the
    free-text `player_name` column, add `UNIQUE(owner, player_id)`.
- **One-off backfill** (`cmd/backfill-players/main.go`): lists every
  `player_id IS NULL` row, strips the trailing `" (TEAM, POS)"` suffix, matches
  to `players` using the ported `normalize_name()`, and creates the 33 seeded
  names that had no player row (12 kickers, 12 team D/STs, 9 skill players).
  Dry-run by default; `-apply` writes in one transaction. Used raw pgx (not
  sqlc) because sqlc models the final schema, not the intermediate state.
- **`internal/server/players/names.go`** — Go port of
  `normalize_name()` (from `scripts/load_players.py`) plus
  `StripTeamPosSuffix`, with table-driven tests (`names_test.go`).
- **sqlc** — regenerated against the final schema; `db/queries/rankings.sql`
  now joins `players`, `db/queries/players.sql` holds the real player queries
  (ListPlayers, GetPlayerByID, FindPlayersByName, SearchPlayersByName).
- **Rankings service** — `GET /rankings` rows now carry `player_id`,
  `position`, `team`; mutations accept `player_id` (numeric) with `player_name`
  kept as a one-release server-side fallback (case-insensitive exact match,
  400 on zero/many). `DELETE` path param is the numeric player id.
- **Players service** — `GET /players` (all), `GET /players/search?q=`.
- **Frontend** (`web/index.html`, `login.html`, `admin.html`) — datalist-backed
  add-player input sourced from `GET /players`, position/team badge on each
  ranked row, id-based move/add/remove, seed flow resolves the 180 draft-order
  names to player ids (name fallback + info toast for any unmatched), and the
  default API base / placeholders moved to port 8081.
- **Port** — app exposed at `http://localhost:8081` (`compose.yaml`
  `"8081:8080"`, CORS default origin updated); container still listens on 8080.

### What was verified
- `go build ./...`, `go vet ./...`, `go test ./...` all green.
- `node --check` on the extracted JS of `index.html`, `login.html`, `admin.html`.
- JS `normalizeName` / `stripTeamPosSuffix` cross-checked line-by-line against
  the Go source of truth (same steps, order, regexes).
- Backfill dry run: 2160 rows / 180 distinct names / 160 players → 147 exact,
  0 fuzzy, 33 new, 0 ambiguous. `-apply`: 2160 updated, 33 created, 0 NULL
  remaining. `players` now 193 rows.
- Smoke test (`docker compose up -d --build` + curl): 401 with no cookie, 200
  login, `GET /rankings` = 12 owners × 180 rows each carrying
  `player_id`/`position`/`team`, `GET /players` = 193, `GET /players/search?q=`
  works, `PATCH .../move` by id works, 403 when editing another owner.
- Data integrity: every owner's board is exactly ranks 1–180, no duplicate
  ranks, no gaps (the move smoke test's 1→2→1 round-trip was a no-op).

### What was skipped / deferred
- `player_name` fallback is intentionally left in the API for one release;
  remove it once the autocomplete add-player UI is confirmed in production.
- `scripts/__pycache__/` is untracked and not committed (out of scope; not yet
  added to `.gitignore`).

---

## 03 — Positional Rankings

**Commit:** `3783d65`

### What was done
- **sqlc** (`db/queries/rankings.sql`, regenerated `internal/db/rankings.sql.go`):
  replaced `ListRankings` with `ListRankingsWithPositionalRank` (adds a
  `RANK() OVER (PARTITION BY owner, position ORDER BY rank)` window column);
  added `ListRankingsWithPositionalRankFiltered` (`WHERE position = ANY(...)`),
  `ListDistinctPositions`, and `ListPositionalConsensus` (league-wide average
  positional rank per player + owner count for a position or FLEX).
- **Rankings service** (`internal/server/rankings/rankings.go`):
  `GET /rankings` now returns `overall_rank` + `positional_rank` (dropped the
  old `rank`). `?position=` accepts `FLEX` (hardcoded RB/WR/TE) or an exact
  position validated data-driven against `ListDistinctPositions` (400 on
  unknown/missing). `parsePositionFilter` is a pure, table-tested helper.
  Mutation responses derive positional rank in Go (`toPlayerRankings`).
  Added `GET /rankings/consensus?position=` (stretch goal, no UI yet).
- **Tests** (`internal/server/rankings/rankings_test.go`): table-driven tests
  for `parsePositionFilter` and `toPlayerRankings` (mixed boards, single
  position, empty).
- **Frontend** (`web/index.html`): position-tab bar (All/QB/RB/WR/TE/FLEX/K/
  D/ST, plus an Other tab only when unknown positions exist) with per-tab
  counts; client-side filtering over the single unfiltered payload; the rank
  number shows `overall_rank` in All and `positional_rank` in position tabs; a
  clickable "RB3" positional badge (All/FLEX/Other) that jumps to a position's
  tab; and filtered moves that send the neighbor's `overall_rank` through the
  unchanged `PATCH .../move`. Removed the now-ignored `rank` field from the
  replace/seed request bodies. Added CSS for the tabs and positional badge.

### What was verified
- `go build ./...`, `go vet ./...`, `go test ./...` green; `node --check` on the
  extracted JS of `index.html` passes.
- Smoke test (`docker compose up -d --build` + curl): unfiltered `GET /rankings`
  returns `overall_rank`/`positional_rank`; `?position=RB` returns only RBs in
  positional order; `?position=FLEX` returns RB/WR/TE in overall order with each
  row's positional rank; `?position=BOGUS` → 400; `GET /rankings/consensus`
  with no position → 400, and `?position=RB`/`FLEX` return correct league
  averages (e.g. Jahmyr Gibbs avg 1.17 across 12 owners).
- Filtered move verified end-to-end: on ethan's RB tab, moving the #2 RB up
  (sending the #1 RB's overall rank) placed it at RB #1 and shifted the other
  to RB #2; the test move was then reverted, restoring the board.

### What was skipped / deferred
- The consensus endpoint has no frontend view yet (follow-up doc).
- The UI filters client-side in v1; the server-side `?position=` filter is
  implemented and smoke-tested but not wired into the UI.
