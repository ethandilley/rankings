# 09 — Next Steps & Backlog

Not a feature spec — a prioritized list of what to do next, based on the state of the codebase after doc 08. Each item has a rough effort (S/M/L) and dependencies. Recommended order is at the top; skip around freely.

**Recommended order:** 1.1 → 1.2 → 2.2 → 2.1 → 2.3 → the rest. Rationale: kill the cheap maintenance burden first, then the one feature that makes the board feel alive (per-player detail), then the big one (history) on its own, then automation.

## 1. Cleaning slop

### 1.1 Deduplicate the shared frontend boilerplate (S, no deps)
`apiRequest`, `escapeHtml`, the toast helper, the auth-init pattern, and ~100 lines of identical CSS (fonts, header, status dot, buttons, cards) are copy-pasted into `web/index.html`, `web/trades.html`, `web/consensus.html`, and `web/admin.html` (four divergent copies of `apiRequest` alone — each page's error handling has already drifted slightly). Extract `web/app.css` + `web/app.js` and have the four pages include them. Verify every page after (login, board, tiers, trades, consensus, admin, the 401-redirect path in particular). This is the single biggest maintenance win on the list — every future page currently starts with a paste job.

### 1.2 Delete `cmd/backfill-players` (S, no deps)
Its own header says so: a one-off tool to assign `players.id` to rankings rows between migrations 0006 and 0007. That migration is done, the tool has been run, and it matches against a `players` table that now stays current via ESPN sync. ~280 lines of dead weight. Delete the command; the name-normalization it needed lives on in `internal/server/players` and is tested.

### 1.3 Leave `index.html` alone beyond 1.1 (no effort — a decision, not a task)
The board page is 1,800+ lines of single-file HTML/JS. It works, it's readable top to bottom, and splitting it into a component architecture for a 12-user static tool would cost more than it saves. Do **not** start an SPA framework migration. The 1.1 extraction is the ceiling of frontend refactoring for this project.

## 2. Features

### 2.1 Ranking history & movement — its own project (L, depends on nothing but patience)
Doc 08 §3 deferred this explicitly and correctly. Build it as doc 10, using option (b) from that doc: a nightly snapshot of the full `rankings` table into a dated `ranking_snapshots` table (reuse the espn-sync pattern — same image, one-shot command + compose profile), no write-path instrumentation. Then the payoff UI, in two increments:
- **Movement arrows on the board:** up/down/unchanged vs the previous snapshot, per player, in the owner's own view. A tiny inline glyph, not a new page.
- **Per-player trend:** the per-player detail from 2.2 gains a sparkline of that player's normalized rank across snapshots.

Daily granularity is plenty ("how has my WR7 drifted since Week 1"). Don't instrument every mutation — that's option (a) and it's strictly more work for strictly less insight at this scale.

### 2.2 Per-player detail drawer (M, depends on nothing)
Click any player row (board, big board, or trade list) and open a side drawer: who ranked them where (per-owner list, their positional rank vs the field), their consensus percentile from doc 08, and — once 2.1 exists — their trend. Pure read: `ListNormalizedRankings` already returns everything needed; this is a UI feature with zero schema change. It's the glue that turns the flat board into something you actually explore, and it's the highest-value feature on this list relative to its cost.

### 2.3 Automatic ESPN sync (M, depends on the cookie rotation in §3)
There is no `crontab` on the host, so "run espn-sync daily" is a documented manual step nobody will do. Add an in-process ticker to the server (env-gated, off by default, e.g. `ESPN_SYNC_INTERVAL=24h`), reusing `espn.Service` — the fetch/diff/apply logic is already one code path. Surface failures properly: when ESPN returns 401 (expired cookie), the admin page should say "sync failing: cookie expired, re-login and update `.envrc`" instead of silently staying stale, and the board should show a `last successful sync` timestamp so nobody mistakes a two-week-old board for a live one.

## 3. Ops / security follow-ups

### 3.1 Rotate the ESPN session cookie (urgent, manual, human-only)
Still outstanding since doc 07: the old `ESPN_S2`/`ESPN_SWID` values are in the public repo's history and remain valid until you log out/in on fantasy.espn.com. Everything else on this list that touches ESPN (2.3, and any future live-data temptation) sits on top of this.

### 3.2 If this ever goes public-facing: `COOKIE_SECURE=true` + TLS termination + change the compose dev DB credentials (S, only if applicable)
Already noted in doc 07's progress log; restated here so it doesn't get lost in the backlog.

## 4. Stretch / nice-to-have (don't schedule these, take them when they fit)

- **Deep links:** `index.html?owner=ethan`, `trades.html?owner=ethan&scope=all`, `consensus.html?owner=ethan`. Trivial once 1.1 lands (one shared query-param reader), and it makes "check out how contrarian Tommy is" a shareable URL instead of a screenshot.
- **Print/screenshot stylesheet for the consensus page:** doc 08's stated point is "screenshot into the league group chat weekly." A `@media print` pass (hide nav/chips, black on white, show the owner name + date) makes that one tap.
- **Sticky owner dropdown on the consensus page** and a small "ranked by N" bar under each big-board row — cosmetic, do them inside 1.1 or 2.2 rather than as their own change.

## Explicitly not doing (so nobody builds it by accident)

- Live NFL stats / scoring — flagged out of scope in doc 08, still true.
- An SPA framework, build step, or TypeScript for the frontend — 1.1 is the refactoring ceiling; `node --check` on extracted scripts is the tooling ceiling.
- Distributed/persistent rate limiting, multi-instance anything — 12 users, one box (doc 07's decision, still correct).
- Notification digests / email / push — the group chat already exists; deep links + the print stylesheet are the right size of "shareability."
