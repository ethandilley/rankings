# 08 — League Consensus Board & Analytics (Optional / Stretch)

**Depends on:** `02-data-model-and-players-integration.md`. Benefits from `03-positional-rankings.md`. Not required for the core ask — build this only once auth, positions, tiers, and trade suggestions are solid. Included because the underlying data supports it almost for free once those exist.

## 1. League consensus big board

A single page showing, across all 12 owners, the average normalized rank per player (reuse the exact normalization approach from `05-trade-suggestions.md` — don't invent a second normalization scheme). This produces the league's own crowd-sourced ranking, distinct from any single owner's opinion — genuinely fun to have as a shared artifact, and a nice thing to screenshot into the league group chat weekly.

```sql
-- name: ConsensusRankings :many
WITH normalized AS (
    SELECT r.player_id, r.rank::float / t.total AS normalized_rank
    FROM rankings r
    JOIN (SELECT owner, COUNT(*) AS total FROM rankings GROUP BY owner) t
      ON t.owner = r.owner
)
SELECT
    p.id, p.player_name, p.position, p.team,
    AVG(n.normalized_rank) AS avg_normalized_rank,
    COUNT(*) AS num_owners_ranked
FROM normalized n
JOIN players p ON p.id = n.player_id
GROUP BY p.id, p.player_name, p.position, p.team
HAVING COUNT(*) >= 3  -- avoid a "consensus" of one or two opinions
ORDER BY avg_normalized_rank ASC;
```

Expose as `GET /rankings/consensus`, optionally with the same `?position=` filter from doc 03.

## 2. "How contrarian am I" per owner

For each owner, compare their normalized rank of every player against the league consensus (from #1) and surface their biggest positive and negative outliers: "You're the highest on Player X in the league (by a wide margin)" / "You're the only one low on Player Y." This is essentially the single-owner-vs-field version of the pairwise disagreement logic already built for trade suggestions — reuse that computation rather than writing a third variant. Frame it as a fun personality-driven stat ("You're the league's biggest Chase Brown believer") rather than a dry table — this kind of thing gets shared in group chats, which is really the whole point of building a shared tool for a friend group.

## 3. Ranking history / movement over time

Not currently possible — `rankings` only stores current state, no history. If this is wanted, it needs a genuinely new piece: either (a) an append-only `ranking_history` table written to on every mutation (simplest: a trigger, or just an extra insert in the same transaction every write handler already runs), or (b) periodic snapshots (e.g. a nightly job that copies the full `rankings` table into a dated snapshot table). Recommend (b) if this is wanted at all — daily granularity is plenty for "how has my WR7 changed since Week 1," and it's a lot less write-path complexity than instrumenting every mutation. This is meaningfully more work than the other items in this doc and should be treated as its own separate project, not a quick add-on — don't build it opportunistically alongside something else.

## Explicitly out of scope for this whole project (flagging so nobody builds it by accident)

- Live NFL stats / real-time scoring integration — this is a rankings tool, not a scoreboard. ESPN's fantasy API (already used for roster sync) technically exposes live scoring, but pulling that in turns this into a much bigger project with a much higher maintenance burden (bye weeks, stat corrections, Thursday/Sunday/Monday timing quirks) for a feature that duplicates what ESPN's own app already does well.
- Push notifications of any kind.
- Any feature that requires knowing about other fantasy platforms (Sleeper, Yahoo) — this repo is ESPN-specific by design (see `scripts/load_players.py`); generalizing to multiple platforms is a real undertaking and shouldn't be scoped in casually.
