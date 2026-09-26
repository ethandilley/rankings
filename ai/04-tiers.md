# 04 — Tiers

**Depends on:** `02-data-model-and-players-integration.md`. Pairs well with `03-positional-rankings.md` but doesn't strictly require it.

## What "tiers" means here

Fantasy rankers group players into tiers — clusters of roughly-equal value with a meaningful drop-off between clusters — rather than treating rank 12 and rank 13 as equally different from rank 1 and rank 2. "My tier 1 RBs are these 4 guys, then there's a cliff, then tier 2 starts." This is one of the most requested features in any fantasy ranking tool because a raw 1-180 ordinal list hides where the real value gaps are.

## Data model: tier breaks, not a tier number per player

Two ways to model this. Pick the tier-break approach below — it's simpler to maintain as rankings get reordered.

**Rejected approach:** add a `tier INT` column directly to `rankings` rows. Problem: every time a player moves rank, you'd need to decide whether they also changed tiers, and inserting a new player in the middle of a tier means relabeling every row below it. Fragile, lots of write-path complexity.

**Recommended approach:** store tier boundaries as a separate, small table of "a new tier starts before this rank," decoupled from the player rows themselves:

```sql
-- +goose Up
CREATE TABLE tier_breaks (
    id          BIGSERIAL PRIMARY KEY,
    owner       TEXT NOT NULL,
    position    TEXT NOT NULL,      -- 'ALL' for overall-board tiers, or 'QB'/'RB'/'WR'/'TE'/etc for positional tiers
    before_rank INT NOT NULL,       -- a new tier begins immediately before this rank, within (owner, position)
    label       TEXT,               -- optional, e.g. "Elite", "WR1 Territory" — nullable, defaults to "Tier N" in the UI
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, position, before_rank)
);

-- +goose Down
DROP TABLE tier_breaks;
```

Why this works well: a tier break is just a marker "there's a line here." Tier *membership* for any given player is computed at read time exactly like positional rank was in doc 03 — take the player's rank (overall, or positional if `position != 'ALL'`), find how many tier-break rows have `before_rank <= that rank`, and that count (+1) is the tier number. No relabeling needed when players are inserted, removed, or reordered — the breaks stay anchored to rank thresholds, and if a player moves across a threshold, their computed tier just naturally updates.

```sql
-- name: ListTierBreaks :many
SELECT position, before_rank, label
FROM tier_breaks
WHERE owner = $1
ORDER BY position, before_rank;

-- name: SetTierBreak :exec
INSERT INTO tier_breaks (owner, position, before_rank, label)
VALUES ($1, $2, $3, $4)
ON CONFLICT (owner, position, before_rank) DO UPDATE SET label = EXCLUDED.label;

-- name: DeleteTierBreak :exec
DELETE FROM tier_breaks
WHERE owner = $1 AND position = $2 AND before_rank = $3;
```

## Computing tier number for display

Do this in the application layer (Go), not SQL, since it's a simple linear pass and keeping it out of SQL makes it trivial to unit test:

```go
// tierBreaksAscending is the before_rank values for one (owner, position),
// sorted ascending. Returns which tier (1-indexed) a given rank falls into.
func tierForRank(rank int, tierBreaksAscending []int) int {
    tier := 1
    for _, breakRank := range tierBreaksAscending {
        if rank >= breakRank {
            tier++
        }
    }
    return tier
}
```

## API

- `GET /rankings` (and the positional variant from doc 03) — add `tier` to each entry, computed server-side using the function above, joined against that owner's `tier_breaks` for the relevant `position` scope (`ALL` for the overall list, the specific position code for a positional view).
- `GET /tiers/{owner}?position=ALL` — returns the raw tier-break rows for editing.
- `PUT /tiers/{owner}?position=ALL` — full replace of that owner's tier breaks for that position scope, body `{ "breaks": [{ "before_rank": 5, "label": "Elite" }, { "before_rank": 13 }] }`. Full-replace (delete-then-insert, same transactional pattern as `replaceRankings` in the existing codebase) is simpler and safer than fine-grained add/remove endpoints here, since tier breaks are edited far less frequently than individual player ranks and there's no meaningful "shift" operation to preserve — auth-gated to the owner matching `{owner}`, same middleware as doc 01.

## Frontend

- In the ranked sheet, render a visually distinct divider row wherever a tier break falls, showing the label if set ("— Elite —") or a default ("— Tier 2 —").
- Add a small "+ tier break" affordance between any two adjacent rows (a thin clickable gap, or a small `+` button that appears on hover) that calls the tiers API to insert a break at that rank.
- Clicking an existing tier divider lets you rename its label or remove it.
- Tiers are scoped per position (`ALL` vs `RB` vs `WR` etc.) — if doc 03 is implemented, switching position tabs should show that position's own tier breaks, not the overall ones. An overall-board tier break at rank 15 doesn't imply anything about where the RB-only tier breaks fall, since RB15 overall and RB15-among-RBs are different things once you filter.

## Edge cases

- `before_rank` beyond the length of the owner's list (e.g. tier break at rank 50 but they've only ranked 40 players) — allowed, just renders as a break with nothing below it. Don't validate this away; it's harmless and owners may set up tier structure ahead of ranking that many players.
- Two tier breaks at the same `before_rank` — prevented by the `UNIQUE` constraint; surface this as a friendly "a tier break already exists here" error rather than a raw constraint-violation message.
- Deleting a player who sits exactly at a tier boundary — since tiers are computed from rank thresholds rather than stored per-player, removing a player just shifts everyone below up by one rank (existing behavior, unchanged) and the tier breaks stay put at their `before_rank` values, which may shift what falls into which tier by one slot. This is correct, expected behavior, not a bug — no special-casing needed.
