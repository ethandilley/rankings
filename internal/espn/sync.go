package espn

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/server/players"
)

// ErrFetchFailed indicates the ESPN API could not be reached or the session
// cookies were rejected. Callers may map it to a distinct HTTP status.
var ErrFetchFailed = errors.New("ESPN fetch failed")

// DBPlayer is one current players row relevant to the sync.
type DBPlayer struct {
	ID         int64
	Owner      string // "" for NULL
	PlayerName string
	Position   string
	Team       string
	DraftedAt  int
}

// Update is a players row to rewrite because ESPN changed it.
type Update struct {
	ID         int64
	PlayerName string
	Owner      string
	Position   string
	Team       string
	DraftedAt  int
	Changes    []string
}

// Plan is the full diff between the players table and the ESPN rosters.
type Plan struct {
	Inserts   []RosterPlayer
	Updates   []Update
	Deletes   []DBPlayer
	Unchanged int
}

// PlanSync diffs the players table (keyed by normalized name) against the
// ESPN rosters (keyed by normalized name). A player present on only one side
// is an insert or a delete; present on both with any differing column is an
// update. Results are sorted by player name so reports are deterministic.
func PlanSync(current map[string]DBPlayer, espn map[string]RosterPlayer) Plan {
	plan := Plan{}

	for norm, p := range espn {
		cur, ok := current[norm]
		if !ok {
			plan.Inserts = append(plan.Inserts, p)
			continue
		}
		var changes []string
		if cur.PlayerName != p.PlayerName {
			changes = append(changes, fmt.Sprintf("player_name: %s -> %s", cur.PlayerName, p.PlayerName))
		}
		if cur.Owner != p.Owner {
			changes = append(changes, fmt.Sprintf("owner: %s -> %s", display(cur.Owner), display(p.Owner)))
		}
		if cur.Position != p.Position {
			changes = append(changes, fmt.Sprintf("position: %s -> %s", cur.Position, p.Position))
		}
		if cur.Team != p.Team {
			changes = append(changes, fmt.Sprintf("team: %s -> %s", cur.Team, p.Team))
		}
		if cur.DraftedAt != p.DraftedAt {
			changes = append(changes, fmt.Sprintf("drafted_at: %d -> %d", cur.DraftedAt, p.DraftedAt))
		}
		if len(changes) == 0 {
			plan.Unchanged++
			continue
		}
		plan.Updates = append(plan.Updates, Update{
			ID:         cur.ID,
			PlayerName: p.PlayerName,
			Owner:      p.Owner,
			Position:   p.Position,
			Team:       p.Team,
			DraftedAt:  p.DraftedAt,
			Changes:    changes,
		})
	}

	for norm, cur := range current {
		if _, onESPN := espn[norm]; !onESPN {
			plan.Deletes = append(plan.Deletes, cur)
		}
	}

	sort.Slice(plan.Inserts, func(i, j int) bool {
		return plan.Inserts[i].PlayerName < plan.Inserts[j].PlayerName
	})
	sort.Slice(plan.Updates, func(i, j int) bool {
		return plan.Updates[i].PlayerName < plan.Updates[j].PlayerName
	})
	sort.Slice(plan.Deletes, func(i, j int) bool {
		return plan.Deletes[i].PlayerName < plan.Deletes[j].PlayerName
	})
	return plan
}

func display(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Service runs the sync: fetch ESPN, diff against the players table, and
// apply the diff in one transaction.
type Service struct {
	mu      sync.Mutex
	conn    *pgxpool.Pool
	Queries *db.Queries
	Config  Config
}

// New builds a sync Service on the given connection. The ESPN Config is
// taken from the environment at construction time; rotating cookies takes
// effect on the next process start without a rebuild.
func New(conn *pgxpool.Pool, cfg Config) *Service {
	return &Service{conn: conn, Queries: db.New(conn), Config: cfg}
}

// Report summarizes one sync run, dry or applied.
type Report struct {
	DryRun        bool     `json:"dry_run"`
	EspnPlayers   int      `json:"espn_players"`
	DBPlayers     int      `json:"db_players"`
	Inserted      int      `json:"inserted"`
	Updated       int      `json:"updated"`
	Deleted       int      `json:"deleted"`
	DeleteSkipped []string `json:"delete_skipped"`
	Unchanged     int      `json:"unchanged"`
	Warnings      []string `json:"warnings,omitempty"`
	Changes       []string `json:"changes"`
}

// Sync fetches the league, diffs it against the players table, and applies
// the diff in a single transaction unless dryRun is set. Players that are no
// longer on any ESPN roster are deleted unless they are still referenced by
// rankings; those deletes are skipped and reported instead.
func (s *Service) Sync(ctx context.Context, dryRun bool) (*Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	league, err := FetchLeague(ctx, s.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	espn, warnings := BuildRoster(league)

	rows, err := s.Queries.SyncListPlayers(ctx)
	if err != nil {
		return nil, fmt.Errorf("load players: %w", err)
	}
	current := make(map[string]DBPlayer, len(rows))
	for _, r := range rows {
		current[players.NormalizeName(r.PlayerName)] = DBPlayer{
			ID:         r.ID,
			Owner:      r.DraftedByUsername.String,
			PlayerName: r.PlayerName,
			Position:   r.Position,
			Team:       r.Team,
			DraftedAt:  int(r.DraftedAt),
		}
	}

	plan := PlanSync(current, espn)

	referenced, err := s.Queries.SyncReferencedPlayerIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("load referenced player ids: %w", err)
	}
	protected := make(map[int64]bool, len(referenced))
	for _, id := range referenced {
		protected[id] = true
	}

	report := &Report{
		DryRun:        dryRun,
		EspnPlayers:   len(espn),
		DBPlayers:     len(rows),
		Inserted:      len(plan.Inserts),
		Updated:       len(plan.Updates),
		Deleted:       len(plan.Deletes),
		Unchanged:     plan.Unchanged,
		Warnings:      warnings,
		DeleteSkipped: []string{},
		Changes:       []string{},
	}
	for _, p := range plan.Inserts {
		report.Changes = append(report.Changes,
			fmt.Sprintf("INSERT  %-25s %-5s %-4s -> %s", p.PlayerName, p.Position, p.Team, display(p.Owner)))
	}
	for _, u := range plan.Updates {
		report.Changes = append(report.Changes,
			fmt.Sprintf("UPDATE  %-25s %s", u.PlayerName, strings.Join(u.Changes, ", ")))
	}
	for _, d := range plan.Deletes {
		if protected[d.ID] {
			report.Deleted--
			report.DeleteSkipped = append(report.DeleteSkipped, d.PlayerName)
			report.Changes = append(report.Changes,
				fmt.Sprintf("SKIP    DELETE %-25s still referenced by rankings", d.PlayerName))
			continue
		}
		report.Changes = append(report.Changes,
			fmt.Sprintf("DELETE  %-25s no longer on ESPN", d.PlayerName))
	}

	if dryRun {
		return report, nil
	}

	tx, err := s.conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin sync transaction: %w", err)
	}
	q := s.Queries.WithTx(tx)

	for _, p := range plan.Inserts {
		if err := q.SyncInsertPlayer(ctx, db.SyncInsertPlayerParams{
			DraftedByUsername: text(p.Owner),
			PlayerName:        p.PlayerName,
			Position:          p.Position,
			Team:              p.Team,
			DraftedAt:         int32(p.DraftedAt),
		}); err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("insert %s: %w", p.PlayerName, err)
		}
	}
	for _, u := range plan.Updates {
		if err := q.SyncUpdatePlayer(ctx, db.SyncUpdatePlayerParams{
			DraftedByUsername: text(u.Owner),
			PlayerName:        u.PlayerName,
			Position:          u.Position,
			Team:              u.Team,
			DraftedAt:         int32(u.DraftedAt),
			ID:                u.ID,
		}); err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("update %s: %w", u.PlayerName, err)
		}
	}
	for _, d := range plan.Deletes {
		if protected[d.ID] {
			continue
		}
		if err := q.SyncDeletePlayer(ctx, d.ID); err != nil {
			tx.Rollback(ctx)
			return nil, fmt.Errorf("delete %s: %w", d.PlayerName, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sync: %w", err)
	}
	return report, nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}
