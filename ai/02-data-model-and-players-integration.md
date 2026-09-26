# 02 — Data Model Refactor: Rankings Should Reference Real Players

## Why this matters

Right now a ranking row is `(owner: text, player_name: text, rank: int)`. `player_name` is whatever string the owner typed — `"Jahmyr Gibbs"` from one owner, `"J. Gibbs"` from another, `"Jahmyr  Gibbs"` (double space, from a copy-paste) from a third. There is no shared identity for "Jahmyr Gibbs the player" across owners.

This breaks three things the project owner explicitly asked for:
- **Positional rankings** (`03-positional-rankings.md`) need to know each ranked entry's position. That data lives in the `players` table today and isn't joined.
- **Trade suggestions** (`05-trade-suggestions.md`) need to compare *the same player* across 12 owners' lists. String matching on free text is fragile and will silently produce wrong "disagreements" whenever spelling differs even slightly.
- **Tiers** (`04-tiers.md`) are easiest to reason about per-position, which again needs the join to exist.

Fix this before building any of the three.

## The change

### 1. Finish the `players` table as the single source of truth for player identity

It already exists (`db/migrations/0002_add_players.sql`) but has a design problem worth fixing in the same migration pass: it has an `owner` column, meaning today it models "which fantasy team drafted this player," not "this player exists." That's a second, redundant place tracking ownership (the `rankings` table also encodes a notion of ownership, but for *ranking lists*, which is a different concept). Split these:

```sql
-- +goose Up

-- players: pure identity + attributes, no ownership concept
ALTER TABLE players RENAME COLUMN owner TO drafted_by_username; -- keep the history, rename for clarity
ALTER TABLE players ALTER COLUMN drafted_by_username DROP NOT NULL; -- undrafted/free agents have none

ALTER TABLE players ADD CONSTRAINT players_name_team_unique UNIQUE (player_name, team);
-- (name alone isn't unique enough — there have been multiple "Josh Allen"s in the NFL,
--  one QB one LB; team disambiguates almost all real collisions)

-- +goose Down
ALTER TABLE players DROP CONSTRAINT players_name_team_unique;
ALTER TABLE players ALTER COLUMN drafted_by_username SET NOT NULL;
ALTER TABLE players RENAME COLUMN drafted_by_username TO owner;
```

Keep `drafted_by_username` around — it's genuinely useful context to show in the UI ("drafted by Rohan"), it's just not the same thing as "belongs on Rohan's ranking sheet," which is what `rankings` is for.

### 2. Add a `player_id` foreign key to `rankings`, migrate existing data, then drop the free-text column

This has to be a **two-step migration** because you can't add a `NOT NULL` foreign key to a table with existing rows that don't have a matching player row yet — you need a data-fixing pass in between.

**Migration A — add nullable FK, backfill:**

```sql
-- +goose Up
ALTER TABLE rankings ADD COLUMN player_id BIGINT REFERENCES players(id);

-- Best-effort automatic backfill by exact name match. This WILL leave rows
-- unmatched (typos, "Team D/ST" naming variants, players not yet in the
-- players table) — that's expected and handled by the app-level backfill
-- script described below, not by this migration alone.
UPDATE rankings r
SET player_id = p.id
FROM players p
WHERE r.player_id IS NULL
  AND lower(trim(r.player_name)) = lower(trim(p.player_name));

-- +goose Down
ALTER TABLE rankings DROP COLUMN player_id;
```

**App-level backfill step (run once, not a migration):** write a small Go or SQL script that lists every `rankings` row where `player_id IS NULL`, along with a fuzzy match against `players.player_name` (reuse the `normalize_name()` logic already written in `scripts/load_players.py` — port it to Go or just run the Python script against the `rankings` table too, same normalization rules: lowercase, strip punctuation, strip Jr/Sr/II/III/IV suffixes). Print a diff for a human to eyeball and confirm before writing, the same way `load_players.py`'s `sync_to_database` does with its dry-run mode. Do not auto-apply fuzzy matches without a human glancing at the list — a wrong auto-match silently corrupts someone's ranking.

**Migration B — once every row has a `player_id`, tighten the schema:**

```sql
-- +goose Up
ALTER TABLE rankings ALTER COLUMN player_id SET NOT NULL;
ALTER TABLE rankings DROP COLUMN player_name;
ALTER TABLE rankings ADD CONSTRAINT rankings_owner_player_unique UNIQUE (owner, player_id);

-- +goose Down
ALTER TABLE rankings DROP CONSTRAINT rankings_owner_player_unique;
ALTER TABLE rankings ADD COLUMN player_name TEXT;
ALTER TABLE rankings ALTER COLUMN player_id DROP NOT NULL;
```

The `UNIQUE (owner, player_id)` constraint is new and worth having — it makes "this owner already has this player ranked" a database guarantee instead of just the `ErrPlayerExists` check the Go code currently does in application code (keep that check too, for a friendlier error message before the DB constraint fires).

### 3. Update sqlc queries

`db/queries/rankings.sql` needs every query touching `player_name` rewritten to join `players`:

```sql
-- name: ListRankings :many
SELECT r.owner, p.id AS player_id, p.player_name, p.position, p.team, r.rank
FROM rankings r
JOIN players p ON p.id = r.player_id
ORDER BY r.owner, r.rank;
```

...and so on for the other five queries. `InsertRanking` and `AddPlayer`-style operations now take a `player_id` instead of a `player_name` string — which means the "add player" flow in the frontend changes from a free-text box to an **autocomplete/search-select against `GET /players`** (add this endpoint — see doc `06` for finishing the players service). This is a meaningful UX improvement on its own: it prevents typos, and it lets you show position/team right in the search results.

Also: delete `db/queries/players.sql` entirely (see `00-overview.md` point 3 — it's a byte-identical duplicate of `rankings.sql` today, and once players gets its own real queries in `06-espn-sync-service.md` it should only contain player-specific ones like `ListPlayers`, `GetPlayerByID`, `SearchPlayersByName`).

### 4. API shape changes

- `GET /rankings` response shape gains `position` and `team` per entry:
  ```json
  { "owner": "ethan", "rankings": [
      { "player_id": 42, "player_name": "Jahmyr Gibbs", "position": "RB", "team": "DET", "rank": 1 }
  ]}
  ```
- `POST /rankings/{owner}/players` body changes from `{ "player_name": "...", "rank": N }` to `{ "player_id": 42, "rank": N }`. Keep accepting `player_name` as a fallback for one release (resolve it server-side via a case-insensitive lookup against `players`, `400` if zero or multiple matches) so you're not forced to ship the frontend autocomplete in the same deploy as the backend change — but plan to remove the fallback once the new "add player" UI ships.

### 5. Frontend changes

- Replace the free-text "Player name" input in `buildAddPlayerForm` with a `<datalist>`-backed input or a small autocomplete dropdown sourced from `GET /players` (client-side filter is plenty at ~200-300 players; no need for server-side search-as-you-type at this scale).
- Show position (and maybe team) as a small badge next to each ranked player row — trivial once the API returns it, and it's a prerequisite UI piece for `03-positional-rankings.md`.

### Non-goals

- Don't try to model bye weeks, injury status, or live stats here — that's out of scope for this doc and arguably out of scope for the whole project unless a later doc calls for it explicitly. This doc is only about giving every ranked entry a stable, shared identity.
