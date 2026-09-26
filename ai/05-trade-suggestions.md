# 05 — Trade Suggestions Page

**Depends on:** `02-data-model-and-players-integration.md` (must have shared `player_id` across owners — this whole feature is meaningless with free-text player names, since it hinges on reliably comparing the same player across different owners' lists).

## The idea

If Owner A ranks a player much higher than Owner B does, that's a signal: A values that player more than B does, which is exactly the situation where a trade could make both sides happy — B should be willing to trade that player away for less than A would demand, in A's eyes. A ranking disagreement, and specifically a disagreement between two owners *who each currently roster a player the other undervalues*, is a genuine trade-idea signal, not just a curiosity.

## Two different things this page could show — build both, they answer different questions

### 1. Global disagreement leaderboard: "which players does the league disagree about most?"

For every player who appears on at least 2 owners' boards, compute a disagreement score across all owners who've ranked them, then sort descending. This doesn't require the two owners to currently roster anything relevant to each other — it's just "where is opinion split," useful context even without an actionable trade.

**Disagreement metric — don't use raw rank, normalize first.** Owner A might rank 180 players, Owner B only 40. A raw rank of "50" means very different things in those two lists. Normalize each owner's rank for a player to a percentile within their own list before comparing:

```
normalized_rank(owner, player) = rank / total_players_ranked_by(owner)   -- 0.0 (best) to 1.0 (worst)
```

Then disagreement for a player = standard deviation (or simply max − min) of `normalized_rank` across every owner who ranked them. Standard deviation is more robust if you want to eventually weight by how many owners ranked a player at all; max − min is more intuitive to display ("Player X: ranked as high as #3 by Ethan, as low as #145 by Rohan") and is fine to ship first — recommend starting with max − min for v1, since it's directly explainable in the UI without a stats-background reader getting lost, and revisiting stddev only if max−min proves too noisy from single-outlier rankers.

```sql
-- name: PlayerDisagreement :many
WITH normalized AS (
    SELECT
        r.player_id,
        r.owner,
        r.rank::float / owner_totals.total AS normalized_rank
    FROM rankings r
    JOIN (
        SELECT owner, COUNT(*) AS total FROM rankings GROUP BY owner
    ) owner_totals ON owner_totals.owner = r.owner
)
SELECT
    player_id,
    COUNT(*) AS num_owners_ranked,
    MIN(normalized_rank) AS best_normalized_rank,
    MAX(normalized_rank) AS worst_normalized_rank,
    MAX(normalized_rank) - MIN(normalized_rank) AS disagreement_spread,
    (array_agg(owner ORDER BY normalized_rank ASC))[1] AS highest_on_owner,
    (array_agg(owner ORDER BY normalized_rank DESC))[1] AS lowest_on_owner
FROM normalized
GROUP BY player_id
HAVING COUNT(*) >= 2
ORDER BY disagreement_spread DESC;
```

Join this against `players` for name/position/team, and expose as `GET /trade-suggestions/disagreements?limit=25`.

### 2. Actionable pairwise suggestions: "which specific trade between which two owners makes sense?"

This is the more genuinely useful view and the one worth featuring most prominently. The shape of an actionable suggestion is:

> Owner A rosters (per `players.drafted_by_username`, from doc 02) a player that **Owner B ranks much higher than Owner A does**, **and** Owner B rosters a player that **Owner A ranks much higher than Owner B does**. That's a two-sided disagreement — a potential trade where both sides plausibly think they're winning.

Query sketch (compute in application code, not one giant SQL query — this is a nested comparison that's much more readable as Go):

1. Fetch every owner's normalized rankings (reuse the `normalized` CTE above as a named query, `ListNormalizedRankings`).
2. For each pair of owners `(A, B)`:
   a. For each player A currently rosters (`players.drafted_by_username = A`) that B has also ranked: compute `B's normalized rank of that player` minus `A's normalized rank of that player`. A large **positive** value means B rates A's player much higher than A does — a candidate "B wants this."
   b. Same in reverse for players B rosters that A has ranked.
   c. If both (a) and (b) produce a strong candidate for the same pair, pair them up into one suggested trade: "A's Player X ↔ B's Player Y."
3. Score each suggested trade by the **sum of both sides' rank-gap magnitudes** (bigger combined gap = more mutually beneficial the trade looks on paper) and return the top N pairs per owner, or top N league-wide.

```go
type TradeSuggestion struct {
    OwnerA        string
    OwnerAGives   PlayerRef // a player A rosters
    OwnerB        string
    OwnerBGives   PlayerRef // a player B rosters
    AGainScore    float64   // how much better A rates OwnerBGives vs their own player
    BGainScore    float64   // how much better B rates OwnerAGives vs their own player
    CombinedScore float64
}
```

Expose as `GET /trade-suggestions/pairwise?owner=ethan` (suggestions involving one specific owner — this is what that owner actually cares about when they open the page) and `GET /trade-suggestions/pairwise` (league-wide top suggestions, fun for the commissioner/group chat to browse).

## Why not just "highest owner should trade with lowest owner" naively

A naive version of this feature would just be "here's who ranks this player highest, here's who ranks them lowest, suggest they talk." That's the disagreement leaderboard (#1 above) and it's worth having, but it's not a *trade* suggestion by itself — it doesn't check whether the low-ranking owner actually **has** that player to trade, or whether there's anything the high-ranking owner has to offer that the other side would want back. Section #2's two-sided matching is what actually makes this a "trade" suggestion rather than a "here's a fun fact" page. Build both, but the pairwise one is the actual headline feature — make sure it's not an afterthought relative to the simpler leaderboard.

## Frontend: new page, `trades.html`

- Top section: pairwise suggestions, filterable to "just involving me" (default, once auth exists — see doc 01) vs. "whole league."
- Each suggestion card: two player names/photos-if-you-have-them side by side with an arrow between, each owner's current normalized rank of both players shown small underneath so the reasoning is transparent, not a black box ("You have Player X ranked #38, Rohan has him #6 — Rohan has Player Y, who you have ranked #4 and he has at #29").
- Below that: the global disagreement leaderboard as a simple sortable table (player, position, biggest fan, biggest skeptic, spread).
- No "propose this trade" messaging/notification system needed for v1 — this is a conversation-starter tool for a league that already talks in a group chat, not a transactional trade-execution platform. Don't over-build this into needing accept/reject workflows unless the league explicitly asks for that later.

## Edge cases

- Players rostered by nobody yet (waiver-wire/free agents in `players` with a null `drafted_by_username`) never appear as an "A gives" side of a pairwise suggestion, since nobody owns them to trade — but they can and should still appear in the disagreement leaderboard.
- An owner who's only ranked a handful of players will have very coarse `normalized_rank` values (dividing by a small `total`), which can produce misleadingly huge disagreement spreads. Consider a minimum-ranked-players threshold (e.g. require `total >= 20`) before including an owner in either query, and surface in the UI when an owner is excluded for this reason so it doesn't look like a silent bug.
- Recompute on demand (these are read queries over current data, not a cached/precomputed table) — at 12 owners and a few hundred players this is cheap enough to run live on every page load; don't add a caching layer or background job for this unless real usage shows it's slow.
