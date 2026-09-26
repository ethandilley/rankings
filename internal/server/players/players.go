package players

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/jackc/pgx/v5"
)


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

// HANDLERS

func (h *PlayersService) getPlayers(w http.ResponseWriter, r *http.Request) {

	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// withTx runs fn inside a transaction, committing on success and rolling
// back automatically otherwise.
func (h *PlayersService) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := h.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	if err := fn(h.q.WithTx(tx)); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// withOwnerRowsTx locks an owner's rows with FOR UPDATE inside a transaction,
// hands them to fn for mutation, persists whatever fn returns as the final
// row order, and converts the result into an OwnerRanking response.
//
// fn is responsible for making any DB writes it needs (via q) and returning
// the rows in their final desired order; withOwnerRowsTx does not re-derive
// rank values itself except through applyRankOrder / updateRankIfChanged,
// which callers should use for consistency.
func (h *PlayersService) withOwnerRowsTx(
	ctx context.Context,
	owner string,
	fn func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error),
) (*OwnerRanking, error) {
	var final []db.ListRankingsByOwnerForUpdateRow

	err := h.withTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListRankingsByOwnerForUpdate(ctx, owner)
		if err != nil {
			return fmt.Errorf("list rankings: %w", err)
		}

		result, err := fn(q, rows)
		if err != nil {
			return err
		}
		final = result
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &OwnerRanking{Owner: owner, Rankings: toPlayerRankings(final)}, nil
}

// applyRankOrder persists rows[i].Rank = i+1 for every row whose stored rank
// doesn't already match, given rows in their final desired order.
func (h *PlayersService) applyRankOrder(ctx context.Context, q *db.Queries, owner string, rows []db.ListRankingsByOwnerForUpdateRow) error {
	for i, row := range rows {
		if err := h.updateRankIfChanged(ctx, q, owner, row.PlayerName, row.Rank, int32(i+1)); err != nil {
			return err
		}
	}
	return nil
}

// updateRankIfChanged writes a player's new rank only if it actually differs
// from what's currently stored, avoiding no-op writes during a shift.
func (h *PlayersService) updateRankIfChanged(ctx context.Context, q *db.Queries, owner, playerName string, currentRank, wantRank int32) error {
	if currentRank == wantRank {
		return nil
	}
	if err := q.UpdateRankingRank(ctx, db.UpdateRankingRankParams{
		Owner:      owner,
		PlayerName: playerName,
		Rank:       wantRank,
	}); err != nil {
		return fmt.Errorf("update rank for %q: %w", playerName, err)
	}
	return nil
}

// indexOfPlayer returns the index of playerName in rows, or -1 if absent.
func indexOfPlayer(rows []db.ListRankingsByOwnerForUpdateRow, playerName string) int {
	for i, row := range rows {
		if row.PlayerName == playerName {
			return i
		}
	}
	return -1
}

// toPlayerRankings converts loaded rows into the API response shape, in
// order, assigning ranks 1..N positionally rather than trusting row.Rank
// (which may be stale immediately after a mutation in the same tx).
func toPlayerRankings(rows []db.ListRankingsByOwnerForUpdateRow) []PlayerRanking {
	out := make([]PlayerRanking, len(rows))
	for i, row := range rows {
		out[i] = PlayerRanking{PlayerName: row.PlayerName, Rank: i + 1}
	}
	return out
}

// writeErrIfAny maps a domain/service error to the appropriate HTTP status
// and writes it. Returns true if an error was written (caller should return).
func (h *PlayersService) writeErrIfAny(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrOwnerNotFound), errors.Is(err, ErrPlayerNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
		return true
	case errors.Is(err, ErrPlayerExists):
		http.Error(w, err.Error(), http.StatusConflict)
		return true
	default:
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return true
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
