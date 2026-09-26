# 03 — Positional Rankings

**Depends on:** `02-data-model-and-players-integration.md` (needs `rankings.player_id` → `players.position` to exist).

## Goal

Today every owner has exactly one flat list mixing QBs, RBs, WRs, TEs, kickers, and defenses together. Add the ability to view (and, for some workflows, rank) by position: "show me just my RB rankings," "show me the league's WR consensus," etc.

## Decision: don't create separate storage per position — derive it

You could model this as separate ranking lists per position (`rankings_qb`, `rankings_rb`, ...) but that's the wrong shape: an owner's *overall* rank of a player already implies a relative ordering within any position once you filter. Two sources of truth for the same information (an overall list and N separate positional lists) will drift out of sync the moment someone edits one and not the other. Instead:

- **Storage stays exactly as it is post-doc-02**: one `rankings` table, one rank integer per `(owner, player_id)`, representing the owner's overall big-board order.
- **Positional rank is computed, not stored**: "Jahmyr Gibbs is RB1 for Ethan" simply means Gibbs has the lowest `rank` value among all of Ethan's `RB`-positioned entries. Compute this with a window function at query time:

```sql
-- name: ListRankingsWithPositionalRank :many
SELECT
    r.owner,
    p.id AS player_id,
    p.player_name,
    p.position,
    p.team,
    r.rank AS overall_rank,
    RANK() OVER (PARTITION BY r.owner, p.position ORDER BY r.rank) AS positional_rank
FROM rankings r
JOIN players p ON p.id = r.player_id
ORDER BY r.owner, r.rank;
```

This gives you both `overall_rank` and `positional_rank` in a single query, always consistent with each other by construction, with zero extra write-path complexity.

## Editing within a positional view

The trickier UX question: if someone opens the "RB" tab and drags Bijan Robinson above Jonathan Taylor, what actually happens to the underlying overall list? Recommended behavior:

- Reordering within a filtered positional view **moves the player to immediately adjacent to its new positional neighbor in the overall list**, not to some arbitrary new overall rank. Concretely: if Bijan moves to be directly above Jonathan Taylor in the RB view, the backend should compute Bijan's new overall rank as "the position immediately before Jonathan Taylor's current overall rank" and reuse the existing `PATCH /rankings/{owner}/move` endpoint unchanged — **don't build a second move endpoint for positional moves.** The frontend just needs to translate "move to top of filtered RB list" into "move to overall rank = (overall rank of the RB currently in slot 1)" before calling the existing API.
- This means doc 03 is mostly a **frontend + read-query feature**, not a new write path. That's intentional — it keeps the single well-tested move-and-shift transaction logic in one place rather than forking it.

## API changes

- `GET /rankings` — add optional query param `?position=RB` (also accept the synthetic value `FLEX` meaning `RB,WR,TE` combined, since that's how these leagues actually draft/start lineups — check the league's actual roster settings/scoring format before hardcoding which positions count as FLEX-eligible). Filtering happens server-side so payload size scales with what's actually displayed.
- Response includes both `overall_rank` and `positional_rank` per the query above regardless of whether a filter was applied — cheap to compute, and the "overall" view benefits from showing "RB3" as a small badge next to each player too (this is genuinely useful context for the owner even in the unfiltered view).

## Frontend changes

- Add a row of position tabs above the ranked sheet: `All | QB | RB | WR | TE | FLEX | K | D/ST`. Clicking one re-filters the existing rendered list client-side if you already fetched the full unfiltered payload (simplest — do this first), or re-fetches with `?position=` if you want to keep payloads small (only worth it once player counts get large; at ~200 players per owner this is premature).
- Each row's position badge (added in doc 02) becomes clickable/tappable to jump straight into that position's filtered tab — a nice small affordance, not required for v1.
- Keep the "All" flat view as the default — it's what the existing UI already does and most owners' mental model is "my one big board," not "my four separate boards." Positional tabs are a lens on top of that board, not a replacement for it.

## League-wide positional consensus (stretch, natural extension)

Once this exists, a very small addition gives you a genuinely useful league-wide view: `GET /rankings/consensus?position=WR` returning, across all 12 owners, the average `positional_rank` per player, sorted ascending — i.e. "the league's crowd-sourced WR big board." This is cheap (one more aggregation query, no new tables) and sets up nicely for `08-consensus-and-analytics.md` if that doc gets built. Mention it there rather than fully speccing it here to avoid overlap.

## Edge cases

- A player with position `UNKNOWN` or missing from the `players` table entirely shouldn't crash the positional view — bucket them under an explicit "Other" tab rather than silently dropping them, so a data-quality problem is visible instead of hidden.
- Ties in `positional_rank` (two players with... they can't actually tie since `rank` is a strict total order per owner already, but if you ever allow tied overall ranks in the future, decide now: `RANK()` (leaves gaps, e.g. 1,1,3) vs `DENSE_RANK()` (no gaps, 1,1,2). Recommend `RANK()` for consistency with how draft boards typically communicate ties.
