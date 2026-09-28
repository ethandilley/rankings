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
