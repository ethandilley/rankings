# NOTES_DECISIONS

Decisions made while implementing the `ai/` docs, with reasoning. Appended per session.

## doc 02 — data model & players integration

### Backfilled rankings → players in a single commit of work, but doc-01 committed first
- The working tree carried uncommitted doc-01 work (auth, admin, CORS, players service, web/). Committed it separately as `706a867` before starting doc 02 so each commit maps to one doc.

### sqlc cannot model the intermediate (0006-applied, 0007-pending) schema
- sqlc builds its schema from **all** files in `db/migrations/`, so it always sees the final schema (rankings has NOT NULL player_id, no player_name). A sqlc-generated backfill tool would not compile against the intermediate schema.
- Decision: the one-off tool `cmd/backfill-players` uses a handful of fixed parameterized pgx queries directly instead of sqlc. It is a migration-adjacent utility, not service code, so losing sqlc's compile-time check on ~4 queries is acceptable. (Attempted `db/queries/backfill.sql` first; deleted.)

### 33 seeded ranking names have no players row — create them, never drop
- Stripping the `" (TEAM, POS)"` suffix and normalizing, 147/180 distinct names match `players` **exactly** (the ESPN sync kept identical spellings, so zero fuzzy-only matches were needed). 33 do not, because `players` was built from a skill-position draft pool with no kickers or D/STs:
  - 12 kickers: Brandon Aubrey, Cairo Santos, Cam Little, Cameron Dicker, Eddy Pineiro, Evan McPherson, Harrison Butker, Harrison Mevis, Jake Bates, Jason Myers, Ka'imi Fairbairn, Tyler Loop
  - 12 D/STs: Broncos, Buccaneers, Chargers, Eagles, Jaguars, Jets, Patriots, Rams, Ravens, Seahawks, Steelers, Texans
  - 9 skill players: Dylan Sampson (RB), Ray Davis (RB), Samaje Perine (RB), Ja'Kobi Lane (WR), Jalen McMillan (WR), Kayshon Boutte (WR), Rashid Shaheed (WR), Travis Hunter (WR), Kyler Murray (QB)
- Migration 0007 sets `player_id NOT NULL`, and doc 02 says never silently drop a row, so the backfill creates these 33 player rows: `player_name` = suffix-stripped name, `position`/`team` from the suffix, `drafted_by_username` = NULL, `drafted_at` = 0 (same convention as the 13 already-undrafted rows). Full dry-run report was reviewed before applying (147 exact, 0 fuzzy, 33 new, 0 ambiguous).

### API shape
- Request bodies accept `player_id` (preferred) with `player_name` as a one-release fallback (case-insensitive exact match; 400 if zero or multiple rows match).
- `DELETE /rankings/{owner}/players/{player}` path param becomes the numeric `player_id` (400 if not numeric). Old name-based URLs are gone after this doc; there are no external consumers.
- `PATCH .../move` accepts either `player_id` or `player_name` for the source and target rows.
- `SearchPlayersByName` takes a pre-wildcarded pattern (`%term%` built in Go) so sqlc generates a plain string param instead of `pgtype.Text`.

### Port 8081
- Host port 8080 is taken by llama-server, so the app is exposed on 8081: compose mapping becomes `"8081:8080"` (container still listens on 8080), the CORS default origin becomes `http://localhost:8081`, and the hardcoded `localhost:8080` fallback literals in `web/*.html` are updated to 8081.

## doc 03 — positional rankings

### FLEX = RB, WR, TE (no league roster settings file exists)
- No settings/roster config file exists anywhere in the repo to define the flex slots, so the doc's own definition (`FLEX = RB, WR, TE`) is used. `flexPositions` is a hardcoded slice in the rankings service; `FLEX_POSITIONS` is the matching JS set. If a real league config is added later, both should read from it.

### Exact positions are validated data-driven, FLEX is not
- `?position=` accepts `FLEX` (hardcoded) or an exact position. Exact values are validated against `ListDistinctPositions` (the `DISTINCT position` set from `players`), case-insensitively, so a typo or a position string not present in the data returns 400 instead of silently returning an empty board. FLEX bypasses this check because it's a keyword, not a stored position.

### Consensus endpoint built in doc 03 with no dedicated UI
- The stretch goal `GET /rankings/consensus?position=` is implemented (league-wide average `positional_rank` per player, plus `owner_count`), but no frontend view consumes it yet. It's a pure read endpoint; surfacing it is a follow-up doc.

### Frontend uses client-side filtering (v1)
- The board fetches the full unfiltered `GET /rankings` payload once and filters by position tab in the browser. At ~180 players this is trivially cheap and avoids a round-trip per tab click; the server-side `?position=` filter still exists and is smoke-tested, but the UI doesn't use it in v1.

### Positional rank is derived, never stored
- `positional_rank` is a `RANK() OVER (PARTITION BY owner, position ORDER BY rank)` window function, computed in every read query. No new column or migration. The mutation responses derive it in Go (`toPlayerRankings`) by counting per-position as rows are walked in overall order.

### Filtered-view move reuses the overall move endpoint (doc off-by-one resolved)
- A move inside a filtered view sends the **overall rank of the neighbor** the player is moving next to, via the unchanged `PATCH .../move`. Because the server removes the player and inserts at `newRank-1`, the player lands exactly in that neighbor's overall slot — immediately adjacent in the overall list. In the All tab this collapses to the old `rank ± 1`.
- The doc's drag example suggested `neighbor.overall_rank - 1` for "move up", which is off by one (it would leave a gap). The bold principle — "immediately adjacent in the overall list" — and the doc's "move to top → overall rank of the player currently in slot 1" example both agree on `neighbor.overall_rank`, so that is the formula used. Verified end-to-end: moving Bijan up on the RB tab (to Gibbs's overall rank) put Bijan at RB #1 and shifted Gibbs to RB #2.

## doc 04 — Tiers

### Tier scope is per (owner, position); FLEX/Other have none
- A board's value clusters are scoped by position: `position = 'ALL'` holds the overall board's breaks and a position code (`QB`, `RB`, …) holds positional ones. `FLEX` and `Other` are composite views (they mix positions), so they have no tier scope of their own — the frontend hides the tier UI on those tabs.

### Full-replace PUT, not the doc's single-row upsert/delete
- The doc's SQL section lists a single-row `SetTierBreak` (upsert) + `DeleteTierBreak`, but its API section prescribes a `PUT` that takes the whole set. The implementation follows the API section: `PUT /tiers/{owner}?position=` = `DeleteTierBreaksForScope` + one `InsertTierBreak` per break, in a transaction. The frontend sends the complete (possibly edited) list on every change.

### `tier` is computed server-side per request, never stored
- `GET /rankings` rows carry a `tier` (1-based) derived in Go by `tierForRank(rank, breaks)` over the owner's breaks for the active scope. Scope → rank mapping: `ALL` → `overall_rank`, an exact position → `positional_rank`, `FLEX` → each row's own position `positional_rank`. On a breaks-load error the handler degrades gracefully (all tiers stay 1) rather than failing the whole rankings read.

### `tierForRank` is a pure function, ported to JS
- Server (`tierForRank`) and frontend (`tierForRank`) are independent ports of the same rule: tier = 1 + the count of breaks with `before_rank <= rank`. Both are table-tested. The frontend is the source of truth for rendering (it places dividers and computes labels from the fetched breaks); the server's `tier` field is for API completeness.

### Tier UI is owner-only
- Tiers are one owner's private value judgment, so the dividers and the "+ tier break" / rename / delete affordances render only on the board owner's own board (`canEdit`). Other boards are read-only and show plain ranks.

### Both GET and PUT `/tiers` are owner-gated
- The doc explicitly gates only the `PUT`. Both `GET /tiers/{owner}` and `PUT /tiers/{owner}` use `requireOwner` (403 cross-owner), so an owner's tier structure is not exposed to other users — consistent with the owner-only UI.

### Nullable label via `sqlc.narg`
- No prior query had a nullable column. `label` is nullable, so `InsertTierBreak` uses a named `sqlc.narg(label)` param (mixing positional `$n` with `narg` is invalid) and generates a `pgtype.Text` (`.String == ""` when NULL). Verified a label-omitted break round-trips as a real SQL NULL.

## doc 05 — Trade Suggestions

### Disagreement metric: max − min for v1 (as the doc recommends)
- The doc offers stddev vs max − min and explicitly recommends shipping max − min first because it's directly explainable in the UI ("ranked as high as #3 by Ethan, as low as #145 by Rohan"). Shipped max − min (`disagreement_spread = worst_normalized_rank - best_normalized_rank`); stddev is a follow-up only if spreads prove too noisy from single outlier rankers.

### `min_ranked_players = 20` threshold, applied in SQL and surfaced
- Per the doc's edge-case section, both queries take `min_ranked_players` (const 20) as a param: the disagreement CTE and `ListNormalizedRankings` only include owners with ≥20 ranked rows, and `ListExcludedOwners` returns the rest. Both endpoints return `excluded_owners` in the response and `trades.html` renders a note, so an exclusion is never a silent bug. All 12 current owners rank 180, so the list is empty today.

### `highest_rater`/`lowest_rater` via `array_agg(... ORDER BY)[1]`, not the doc's `WITHIN GROUP` sketch
- The doc's sketch uses `array_agg(owner ORDER BY normalized_rank ASC)[1]`; my first version tried `MIN(owner) WITHIN GROUP (ORDER BY normalized_rank, owner)`, which fails: Postgres resolves an ordered-set aggregate's ORDER BY expressions as *extra function arguments*, and `min(text, double precision, text)` does not exist. `array_agg` is a regular aggregate and supports `ORDER BY` internally. The owner name is also added as a second sort key so tied rankers (e.g. ten owners with a player at #1) resolve deterministically (alphabetically first wins).

### Owner pairing: requested owner is always side A; league-wide uses lexicographic order
- `GET /trade-suggestions/pairwise?owner=X` puts X as `owner_a` on every card — the viewer's perspective ("you give X, they give Y"), which is what the doc says that owner cares about. The league-wide variant has no viewer, so pairs are canonicalized `a < b` (lexicographic) for stable, deterministic output.

### Candidate selection: single largest positive gap per side, both gains strictly > 0
- For a pair, X = the A-owned player B rates highest relative to A (max of `A_norm(X) - B_norm(X)` over A's drafted players ranked by both, tie → lower player ID), Y = the symmetric pick for B's roster. A trade is emitted only if both gaps are > 0 **and** both resulting gains are > 0: `AGain = A_norm(X) - A_norm(Y)` (A rates what it receives above what it gives up) and `BGain = B_norm(Y) - B_norm(X)`. The doc's "strong candidate" is operationalized as "the single best candidate per side" — no threshold tuning, no multi-candidate pairing, keeping the Go loop readable per the doc's "compute in application code" note.

### Both endpoints honor `limit` (default 25, max 100)
- The doc asks for "top N pairs per owner, or top N league-wide". Both `GET .../pairwise` (all valid trades for the owner, or all 66-pair results league-wide, sorted by `combined_score` desc with deterministic tie-breaks, then truncated) and `GET .../disagreements` (SQL `LIMIT`) take `?limit=`; 0, >100, or non-integer → 400.

### Transparency fields beyond the doc's struct
- The doc's `TradeSuggestion` has the three scores; the API also returns the four raw normalized ranks (`a_norm_of_a_gives`, `b_norm_of_a_gives`, `a_norm_of_b_gives`, `b_norm_of_b_gives`) so the card can show the doc's "reasoning is transparent, not a black box" line verbatim.

### Error handling
- `?owner=unknown` → 404 `owner not found`; an owner below the 20-player threshold → 400 with an explanatory message; query errors → 500. Free agents (NULL `drafted_by_username`) never appear as a "gives" side because `ListDraftedPlayers` filters them out of the ownership map, but they remain in the disagreement leaderboard — exactly the doc's edge case.

### sqlc type-inference workarounds in `trade_suggestions.sql`
- `limit` is a reserved word, so the param is `sqlc.arg(row_limit)`.
- sqlc cannot infer types of CTE/computed expressions (float division came back as `int32`/`interface{}`), so every computed column carries an explicit cast: `::float8` for normalized ranks/spread, `::int` for `COUNT(*)`, `::text` for the `array_agg(...)[1]` raters.

## doc 06 — Fix the Players Service & Automate the ESPN Sync

### Part 1 (players service) was already done in doc 02
- Doc 02 shipped `internal/server/players/players.go` as a compiling, auth-gated read-only service (`GET /players` + `GET /players/search` autocomplete) with `db/queries/players.sql` (ListPlayers/GetPlayerByID/SearchPlayersByName/FindPlayersByName) registered in `main.go`. Doc 06's Part 1 asked for a strict subset of that — nothing to implement; recorded here so the doc's status is accurate.

### Sync logic ported to Go rather than shelled out to Python
- The doc allows either. Subprocess is a dead end on this stack: the server image is `golang:1.27.0-alpine` with no python (the Dockerfile builds only `/bin/server`), and the host `python3` has no `psycopg2`. The Go port lives in `internal/espn` (`client.go` HTTP + parse, `roster.go` maps + `BuildRoster`, `sync.go` pure `PlanSync` + transactional apply); `cmd/espn-sync` (one-shot, for cron) and `POST /admin/sync-players` share the same `espn.Service`, so the fetch/diff logic exists in exactly one place. Name normalization reuses `players.NormalizeName` so ESPN↔DB matching stays single-sourced.

### Extended the position map with K (5) and D/ST (16)
- The Python script's `POSITION_MAP` only had QB/RB/WR/TE, so every kicker and D/ST on a roster was silently skipped and could never be updated or assigned an owner. The live API returns `defaultPositionId` 5 (K) and 16 (D/ST); the port maps both. The first real run assigned owners to 24 K/D/ST rows that doc 02's backfill had created with `drafted_by_username = NULL`.

### Owners are usernames, not display names
- The script mapped fantasy team IDs to display names ("Ethan Stone"); the players table has stored usernames since migration 0005. `teamOwnerMap` in `roster.go` maps the 12 team IDs to usernames, verified against the distinct `drafted_by_username` values in the DB.

### `player_name` participates in change detection
- The script compared owner/position/team/drafted_at but wrote `player_name` unconditionally on update, so a pure ESPN name correction counted as "unchanged" and the DB kept the stale display name. `PlanSync` diffs all five columns — strictly closer to "make the players table match the ESPN rosters".

### Deletion guard: skip players referenced by `rankings`
- The FK `rankings_player_id_fkey` is already NO ACTION (doc 02), which is what the doc recommends. The sync loads `SELECT DISTINCT player_id FROM rankings` once per run, refuses to delete referenced players, and reports them in `delete_skipped` / `SKIP DELETE ...` lines. The NO-ACTION FK independently backstops the guard: a delete that ever slipped through the guard would fail with an FK violation instead of corrupting a ranked board.

### Secrets: `.envrc` untracked, `.envrc.example` committed
- `.envrc` added to `.gitignore` and removed from the index. `.envrc.example` has placeholders plus where to find the real cookie values. The previously committed values are leaked and must be treated as such: log out/in on fantasy.espn.com to rotate the session (manual human step, flagged in NOTES_PROGRESS).

### Scheduling: one-shot compose service + documented cron line
- `espn-sync` compose service (profile `sync`, same image, `command: ["/bin/espn-sync"]`), invoked as `docker compose --env-file .envrc --profile sync run --rm espn-sync`. There is no `crontab` binary on this machine, so the daily cron line is documented in NOTES_PROGRESS rather than installed. Gotcha: `docker compose run SERVICE --flag` replaces the command, so flags must be prefixed with the binary (`run --rm espn-sync /bin/espn-sync --dry-run`).

## doc 07 — Security Hardening

### CORS was already an allow-list (doc 01); verified, not rewritten
- `withCORS` in `cmd/server/main.go` has allowed origins via the `ALLOWED_ORIGINS` env var (default `http://localhost:8081`), `Vary: Origin`, credentials only for allowed origins, and the frontend is served same-origin by the Go binary's `http.FileServer` — the doc's "simpler fix". Verified live in the smoke test; no code change.

### History purge via `git filter-branch`, not repo recreation
- The `.envrc` with real ESPN cookies was committed in the early "stash" commits and the repo is **public** on GitHub, so the leak is live until the cookie is rotated (manual user step, flagged since doc 06). Chose `git filter-branch --index-filter 'git rm -r --cached --ignore-unmatch .envrc' --prune-empty -- --all` over the doc's "recreate fresh" option because it preserves the doc-commit trail (the recorded hashes in NOTES are matched by commit message and re-recorded after the rewrite), then force-pushed `main` and `01` and pruned the old objects locally (`refs/original` deletion + `reflog expire` + `gc --prune=now`).

### Validation caps: rank ≤ 500, list ≤ 500, owner ≤ 64, player_name ≤ 200
- Per the doc: `moveRanking`/`addPlayer` now reject `rank > 500` (the in-memory shift loop already clamps, but the cap rejects the request up front); full-replace `POST /rankings` rejects lists over 500 entries; `requireOwner` rejects path owners over 64 chars (matches the username format); `player_name` over 200 chars is rejected in all three write paths. The players table columns are `TEXT`, so 200 is an application-level sanity cap, not a schema limit.

### Rate limiting: in-memory token bucket per session, mutating methods only
- `internal/server/middleware` (`golang.org/x/time/rate`), wrapped in `main.go` around the whole mux: 5 req/s, burst 10, one bucket per session cookie value (keyed via `auth.SessionCookieName`), falling back to remote IP (port stripped) for unauthenticated requests. GET/HEAD/OPTIONS bypass it. Deliberately not persisted and not distributed — one server instance, 12 users (doc's "don't over-invest").

### Transport security: no code change
- The `Secure` cookie flag is already env-gated (`COOKIE_SECURE=true`); TLS termination itself is a property of wherever this is hosted (Caddy / platform HTTPS), per the doc. The compose `DB_URL`/`POSTGRES_PASSWORD` of `password` is a dev default on the internal docker network — fine on a LAN, must be changed if the stack is ever exposed publicly (noted in NOTES_PROGRESS).

## doc 08 — League Consensus Board & Analytics

### `GET /rankings/consensus` keeps doc 03's positional behavior; the big board is the unfiltered view
- Doc 03 already owns `GET /rankings/consensus?position=` (average positional rank). Doc 08 asks for the same path to also serve the normalized big board "optionally with the same `?position=` filter". Rather than break the shipped doc 03 response, the endpoint now branches: no `?position=` → new league-wide big board (average of each owner's normalized overall rank, players ranked by ≥3 owners); with `?position=` → unchanged positional consensus (including FLEX). The consensus UI fetches the unfiltered big board once and filters positions client-side, mirroring index.html's position-tab pattern, so the page never mixes the two metrics.

### Contrarian lives in the trades package as `GET /contrarian`
- The doc doesn't specify a path. It goes with the normalization + gap machinery it must reuse, in `internal/server/trades` (route `GET /contrarian`, auth-gated, `?owner=` optional). Unknown owner → 404, matching the pairwise endpoint.

### The field average excludes the owner's own vote
- Comparing an owner against a consensus that includes their own rank would shrink their outliers and let a single vote move the baseline. The per-player field average is computed as `(sum - own) / (count - 1)`. A player only qualifies when ranked by ≥3 owners (`minConsensusOwners = 3`, same floor as the big board's `HAVING COUNT(*) >= 3`), which also keeps `count - 1 ≥ 2`.

### No `minRankedPlayers` gate for contrarian owners
- The trades endpoints exclude owners with <20 ranked players because trade math needs a full board; personality stats are meaningful at any board size ("you're the only one low on X" is exactly what a 5-player board should surface). All owners with ≥1 ranked player appear; `ListNormalizedRankings` is called with `min_ranked_players = 1` to reach them all.

### `maxGapPlayer` extracted from `bestDesiredOtherSide` instead of a third variant
- Doc 08 says to reuse the doc 05 pairwise gap computation, not invent a new one. `bestDesiredOtherSide` is now a thin wrapper over `maxGapPlayer(scope, own, other) (id, gap, bool)`, which both trade directions and both contrarian directions (believer = `maxGapPlayer(scope, fieldExcl, board)`, skeptic = `maxGapPlayer(scope, board, fieldExcl)`) share. Same strictly-positive-gap rule and lower-player-ID tie-break as before; existing trade tests pass unchanged.

### Ranking history / movement tracking explicitly not built
- The doc defers it to its own separate project. Not built opportunistically here.
