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

---

## 04 — Tiers

**Commit:** `070f83a`

### What was done
- **Migration `0008_tier_breaks.sql` (applied, `goose_db_version` = 8):**
  new `tier_breaks` table — `id BIGSERIAL PK`, `owner TEXT NOT NULL`,
  `position TEXT NOT NULL` (`'ALL'` or a position code), `before_rank INT
  NOT NULL`, `label TEXT` (nullable), `created_at TIMESTAMPTZ DEFAULT now()`,
  `UNIQUE(owner, position, before_rank)`, index on `owner`.
- **sqlc** (`db/queries/tiers.sql`, regenerated `internal/db/tiers.sql.go`):
  `ListTierBreaks(owner)`, `ListTierBreaksAllOwners()`,
  `DeleteTierBreaksForScope(owner, position)`, and `InsertTierBreak`
  (named `sqlc.narg(label)` → `pgtype.Text`).
- **Rankings service** (`internal/server/rankings/rankings.go`):
  `PlayerRanking` gains `tier`. `stampTiers` loads the owner's breaks once per
  request and stamps each row's tier by the active scope's rank (ALL→overall,
  exact position→positional, FLEX→per-row positional), degrading to all-1 on
  a load error. `toPlayerRankings(rows, allBreaks)` stamps mutation
  responses. New `GET /tiers/{owner}?position=` (owner-gated) and
  `PUT /tiers/{owner}?position=` (owner-gated, transactional full-replace).
  `validateTierBreaks` rejects `before_rank < 1` and duplicates with friendly
  messages. `tierForRank` is a pure, table-tested helper.
- **Tests** (`rankings_test.go`): table-driven `TestTierForRank` (12 cases),
  `TestValidateTierBreaks` (6 cases), and `TestToPlayerRankingsTiers`
  (break at 3 → `[1,1,2,2,2]`); existing calls updated to the new
  `toPlayerRankings(rows, allBreaks)` signature.
- **Frontend** (`web/index.html`): API client `getTierBreaks`/`setTierBreaks`;
  `activeTierScope()` (All→ALL, position tab→code, FLEX/Other→none), a JS
  port of `tierForRank`, and `loadTierBreaks` (owner-only, key-guarded,
  race-safe). In the rank sheet each inter-row gap renders either a labeled
  divider (click to rename inline, × to delete) or a thin "+ tier break" pill
  (owner boards only, never before the first row); all mutations full-replace
  via `PUT`. Added the matching CSS.

### What was verified
- `go build ./...`, `go vet ./...`, `go test ./...` green; `node --check` on
  the extracted JS of `index.html` passes.
- Smoke test (`docker compose up -d --build` + curl): `GET /rankings` rows
  carry `tier` (all 1 with no breaks); `GET /tiers/ethan?position=ALL` → 200
  empty; `PUT` a break at rank 3 → `GET /rankings` shows ranks 1–2 tier 1 and
  3+ tier 2; positional `?position=QB` break at pos 2 → pos 1 tier 1, pos 2+
  tier 2, independent of the ALL scope; label-omitted break round-trips as a
  SQL NULL; empty-array `PUT` clears breaks (tiers back to 1); duplicate
  `before_rank` → 400 "a tier break already exists at rank N"; `before_rank 0`
  → 400; missing `position` → 400; cross-owner `GET` → 403; no cookie → 401.
  All test breaks were cleared afterward (`tier_breaks` back to 0 rows).

### What was skipped / deferred
- The server `tier` field is API completeness only; the frontend renders
  tiers from the fetched breaks itself (single source of truth for placement
  and labels).
- Tier UI is owner-only by design; read-only boards show plain ranks.

---

## 05 — Trade Suggestions

**Commit:** `5c48ed0`

### What was done
- **sqlc** (`db/queries/trade_suggestions.sql`, regenerated
  `internal/db/trade_suggestions.sql.go`): `ListNormalizedRankings(min_ranked_players)`
  (rank / total per owner), `ListDraftedPlayers` (all players + nullable
  `drafted_by_username`), `ListExcludedOwners(min_ranked_players)`, and
  `PlayerDisagreement(row_limit, min_ranked_players)` — CTE over qualifying
  owners, `HAVING COUNT(*) >= 2`, spread desc, raters via
  `array_agg(owner ORDER BY normalized_rank[, DESC], owner)[1]` for
  deterministic ties. Explicit `::float8`/`::int`/`::text` casts everywhere
  because sqlc can't infer CTE expression types.
- **Trades service** (`internal/server/trades/trades.go`):
  `GET /trade-suggestions/disagreements?limit=` (leaderboard, max − min
  spread) and `GET /trade-suggestions/pairwise[?owner=&limit=]` (two-sided
  trade matching per the doc: X = largest `A_norm - B_norm` gap over A's
  drafted players, Y = symmetric, both gaps and both gains strictly > 0,
  `combined = aGain + bGain`, sorted desc with deterministic tie-breaks).
  Requested owner is always side A; league-wide pairs canonicalized
  `a < b`. Unknown owner → 404, sub-threshold owner → 400, bad `limit`
  (0/101/non-int) → 400. Both responses carry `excluded_owners` and
  `min_ranked_players`; suggestions carry the four raw normalized ranks for
  UI transparency. Pure helpers `ownerPairs`, `computePairwiseTrade`,
  `bestDesiredOtherSide`, `parseLimit` are table-tested (`trades_test.go`);
  the bGain sign bug (`b_norm[x] - b_norm[y]` vs correct
  `b_norm[y] - b_norm[x]`) was caught by the tests.
- **Wiring** (`cmd/server/main.go`): `tradesService.Register(mux)`.
- **Frontend** (`web/trades.html`, new; `web/index.html` nav): dark-theme
  page in the existing design language — "just me" (default, uses the
  logged-in username) / "whole league" scope toggle, suggestion cards
  ("A gives X ↔ B gives Y" + combined/gain scores + each owner's normalized
  rank of both players underneath), excluded-owners note, and the
  disagreement table (player, pos/team, ranked-by count, best/worst/spread,
  biggest fan/skeptic). `index.html` header gains a "trades" link button.

### What was verified
- `go build ./...`, `go vet ./...`, `go test ./...` green; `node --check` on
  the extracted JS of `trades.html` and `index.html` passes.
- First draft of the disagreement SQL 500'd (`MIN(...) WITHIN GROUP (ORDER
  BY norm, owner)` → `function min(text, double precision, text) does not
  exist`); rewrote with `array_agg(...) [1]` and regenerated.
- Smoke test (`docker compose up -d --build` + curl): no cookie → 401 on both
  endpoints; `limit=0` and `limit=101` → 400; `?owner=nobody` → 404;
  `trades.html` serves.
- `disagreements?limit=3`: top row Jahmyr Gibbs spread 0.0333 — independently
  re-derived in psql (10 owners at #1 → best 1/180; anwar #7 → worst 7/180;
  "eric" is the alphabetical tie-break among the ten #1 rankers). ✓
- `pairwise?owner=ethan`: one suggestion (Gibbs ↔ Jaxon Smith-Njigba with
  tommy); every gain re-derived by hand from the raw ranks. ✓
- `pairwise` (league-wide): exactly 1 valid trade across all 66 owner pairs —
  confirmed by an independent SQL port of the whole algorithm
  (`ROW_NUMBER()` per side, both-gains > 0): same single ethan↔tommy trade,
  same scores (a 0.0056, b 0.0167, combined 0.0222). ✓

### What was skipped / deferred
- Stddev-based disagreement metric (doc: ship max − min first, revisit if
  noisy).
- No "propose this trade" workflow — v1 is a conversation-starter per the doc.
- Multi-candidate pairing (considering the 2nd/3rd best X/Y per side) not
  done; the single-largest-gap candidate per side is what ships.

## 06 — Fix the Players Service & Automate the ESPN Sync

**Commit:** `e0b3a64`

### What was done
- Part 1 (minimal players service) confirmed already shipped by doc 02 —
  `internal/server/players/players.go` compiles, is auth-gated, and is
  registered; no code changes.
- Secrets: `.envrc` added to `.gitignore` and untracked (`git rm --cached`);
  committed `.envrc.example` with placeholders and where-to-find values.
- New `internal/espn` package — Go port of `scripts/load_players.py`:
  `client.go` (authenticated GET to `lm-api-reads.fantasy.espn.com`; 403 →
  "rotate ESPN_S2/ESPN_SWID" error), `roster.go` (pro-team / position /
  team→username maps; `BuildRoster` keyed by `players.NormalizeName`),
  `sync.go` (pure table-tested `PlanSync` diff + transactional apply, JSON
  `Report`).
- `db/queries/espn_sync.sql`: SyncListPlayers / SyncInsertPlayer /
  SyncUpdatePlayer / SyncDeletePlayer / SyncReferencedPlayerIDs; sqlc
  regenerated.
- `POST /admin/sync-players` (RequireAuth + requireAdmin, `?dry_run=true`
  supported); ESPN-side failure → 502 with the cause, DB-side → 500.
- `cmd/espn-sync` one-shot binary sharing the same Service (`-dry-run` flag,
  script-style summary for cron logs); Dockerfile builds both binaries;
  compose gains an `espn-sync` service behind the `sync` profile.
- Deleted `scripts/load_players.py` (and `scripts/__pycache__`); the Go port
  replaces it with two deliberate improvements: K (5) and D/ST (16) are now
  synced (the script's map only had QB/RB/WR/TE), and `player_name`
  participates in change detection.

### What was verified
- `go build ./...`, `go vet ./...`, `go test ./...` green; `internal/espn`
  table tests cover PlanSync (insert / delete / owner / team / drafted_at /
  name-only changes) and BuildRoster (K + D/ST mapping, unknown position
  skipped with warning, unknown fantasy team → no owner, unknown pro team →
  FA, drafted_at from draft picks).
- Live API probe before writing the client: 12 teams (IDs 10–25), position
  ids 1–5 and 16, 180 draft picks, current `.envrc` cookies still valid.
- Smoke (`docker compose --env-file .envrc up -d --build server`):
  unauthenticated → 401; non-admin (tommy) → 403; `dry_run=true` as ethan →
  internally consistent report: espn 189 = unchanged 139 + inserted 26 +
  updated 24; db 193 = unchanged 139 + updated 24 + deleted 5 + skipped 25.
- Real run applied 26 inserts / 24 updates / 5 deletes → `players` 193 → 214.
  K/D/ST rows now carry owners and pick numbers (e.g. Ravens D/ST → hisrchel
  pick 161; Brandon Aubrey → vibhav pick 86). The 5 deletions
  (Antonio Williams, Chris Bell, Kendrick Bourne, Michael Mayer, Mike
  Gesicki) succeeded under the NO-ACTION FK, proving no rankings row
  referenced them.
- Second `dry_run=true`: 0 / 0 / 0, unchanged 189, same 25 permanent skips —
  idempotent.
- `docker compose --env-file .envrc --profile sync run --rm espn-sync` (and
  the `/bin/espn-sync --dry-run` flag form) both run clean against the live
  API. `GET /` still 200.

### What was skipped / deferred
- Manual ESPN session rotation: the previously committed `ESPN_S2`/`ESPN_SWID`
  are leaked. Log out/in on fantasy.espn.com and update `.envrc` — human
  step, not done in this session.
- Installing the daily cron: no `crontab` on this machine. Recommended line
  for the machine running the stack:
  `0 12 * * * cd /home/ethan/code/rankings && docker compose --env-file .envrc --profile sync run --rm espn-sync >> /tmp/espn-sync.log 2>&1`
