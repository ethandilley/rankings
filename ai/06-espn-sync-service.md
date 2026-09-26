# 06 — Fix the Players Service & Automate the ESPN Sync

## Part 1: fix what's currently broken

`internal/server/players/players.go` doesn't compile today (see `00-overview.md`). Rewrite it as a genuinely minimal read-only service — it doesn't need any of the transaction/locking helpers that got copy-pasted into it from `rankings.go`:

```go
package players

import (
    "net/http"

    "github.com/ethandilley/rankings/internal/db"
    "github.com/jackc/pgx/v5"
)

type Player struct {
    ID       int64  `json:"id"`
    Name     string `json:"player_name"`
    Position string `json:"position"`
    Team     string `json:"team"`
}

type PlayersService struct {
    conn *pgx.Conn
    q    *db.Queries
}

func NewPlayersService(conn *pgx.Conn) *PlayersService {
    return &PlayersService{conn: conn, q: db.New(conn)}
}

func (h *PlayersService) Register(mux *http.ServeMux) {
    mux.HandleFunc("GET /players", h.getPlayers)
}

func (h *PlayersService) getPlayers(w http.ResponseWriter, r *http.Request) {
    rows, err := h.q.ListPlayers(r.Context())
    if err != nil {
        http.Error(w, "failed to load players", http.StatusInternalServerError)
        return
    }
    out := make([]Player, len(rows))
    for i, row := range rows {
        out[i] = Player{ID: row.ID, Name: row.PlayerName, Position: row.Position, Team: row.Team}
    }
    writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}
```

Add the corresponding query to a new `db/queries/players.sql` (the old one gets deleted per doc 02 — this is its real replacement, with actual players-table queries this time):

```sql
-- name: ListPlayers :many
SELECT id, player_name, position, team
FROM players
ORDER BY player_name;

-- name: GetPlayerByID :one
SELECT id, player_name, position, team
FROM players
WHERE id = $1;

-- name: SearchPlayersByName :many
SELECT id, player_name, position, team
FROM players
WHERE player_name ILIKE '%' || $1 || '%'
ORDER BY player_name
LIMIT 25;
```

Fix `cmd/server/main.go`: add the missing import and correct the function name (`NewPlayersService`, not `NewPlayerService`), and actually register it:

```go
import (
    // ...existing imports
    "github.com/ethandilley/rankings/internal/server/players"
)

// ...
playerService := players.NewPlayersService(conn)
rankingsService.Register(mux)
playerService.Register(mux)
```

This alone unblocks `GET /players`, which doc 02's autocomplete UI needs, and is a two-line, zero-risk fix — do it as its own small PR before anything else in this doc.

## Part 2: automate the ESPN roster sync

`scripts/load_players.py` currently has to be run by hand. It's a well-structured script (dry-run mode, clear diff output, transactional DB writes) — the goal here is scheduling it and hardening its secret handling, not rewriting its logic.

### Secret handling — fix this regardless of whether you automate anything else

The script currently reads `ESPN_S2` / `ESPN_SWID` from environment variables, which is fine in principle, but `.envrc` (which sets those env vars) is currently committed to the repo with real values in it. Do all of the following:

1. Add `.envrc` to `.gitignore` immediately.
2. Rotate the leaked ESPN session: log out and back in on fantasy.espn.com, which invalidates the old `espn_s2`/`SWID` cookie pair. Anyone who had repo access had access to that fantasy league account until you do this.
3. Commit an `.envrc.example` with placeholder values and a comment explaining where to find the real ones (browser devtools → Application/Storage → Cookies → `fantasy.espn.com`, looking for `espn_s2` and `SWID`).
4. If this ever gets deployed somewhere persistent (not just run ad hoc from a laptop), put the real values in whatever secrets store the deploy target offers (e.g. a `.env` file outside version control, loaded by `compose.yaml`'s `env_file:` directive, or a proper secrets manager if the hosting platform has one) rather than shell env vars sourced from a committed file.

### Scheduling

Options, roughly in order of "least new infrastructure":

- **Simplest: a cron job on whatever machine/VM runs the Docker Compose stack**, calling `docker compose exec server python3 scripts/load_players.py` (or running the venv'd script directly against `DB_URL` if Python isn't in the server container) on a schedule — daily during the season is plenty, rosters don't change that often outside of waiver processing.
- **If deploying to a platform with built-in scheduled jobs** (Fly.io machines, Render cron jobs, etc.), use that instead of hand-rolled cron — less to maintain.
- Don't reach for a message queue or dedicated job-scheduling service for one script that runs once a day against 12 rosters. That's solving a scale problem this project doesn't have.

### Turning this into an authenticated admin action too

Alongside the scheduled job, add a manual-trigger endpoint for the commissioner: `POST /admin/sync-players`, gated by `is_admin` from doc 01's auth, that shells out to (or reimplements inline in Go) the same sync logic — useful right after a trade or waiver claim when someone doesn't want to wait for the next scheduled run. Keep the actual ESPN-fetching logic in one place; don't duplicate the Python script's logic into Go, either call the script as a subprocess from the admin handler or, if you'd rather have one language end to end, port `load_players.py` to Go using `net/http` for the ESPN call (it's a plain authenticated GET, nothing Python-specific about it) — either is reasonable, pick based on team preference rather than technical necessity.

### Data quality note carried over from doc 02

Once `players` rows can be inserted/updated/deleted by an automated sync, and `rankings.player_id` is a foreign key into it (doc 02), think through what happens when the sync **deletes** a player no longer on any ESPN roster (the existing script does this — see `sync_to_database`'s delete branch). If people have that player ranked, the foreign key will block the delete (or cascade it, depending on how you define the constraint) — decide explicitly: recommend **not** cascading deletes here (a `RESTRICT` or plain no-action FK), and instead have the sync skip deleting any player who still appears in someone's `rankings`, logging a warning instead. Silently deleting a player out from under someone's ranked board is a worse outcome than leaving a slightly-stale row in `players`.
