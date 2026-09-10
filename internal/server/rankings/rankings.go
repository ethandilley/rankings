package rankings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/jackc/pgx/v5"
)

type PlayerRanking struct {
	PlayerName string `json:"player_name"`
	Rank       int    `json:"rank"`
}

type OwnerRanking struct {
	Owner    string          `json:"owner"`
	Rankings []PlayerRanking `json:"rankings"`
}

type MoveRankingRequest struct {
	PlayerName string `json:"player_name"`
	Rank       int    `json:"rank"`
}

type AddPlayerRequest struct {
	PlayerName string `json:"player_name"`
	Rank       *int   `json:"rank,omitempty"` // optional; nil = append to end
}

var (
	ErrOwnerNotFound  = errors.New("owner not found")
	ErrPlayerNotFound = errors.New("player not found")
	ErrPlayerExists   = errors.New("player already ranked for this owner")
)

type RankingsService struct {
	conn *pgx.Conn
	q    *db.Queries
}

func NewRankingsService(conn *pgx.Conn) *RankingsService {
	return &RankingsService{conn: conn, q: db.New(conn)}
}

func (h *RankingsService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /rankings", h.getRankings)
	mux.HandleFunc("POST /rankings", h.postRankings)
	mux.HandleFunc("PATCH /rankings/{owner}/move", h.moveRanking)
	mux.HandleFunc("POST /rankings/{owner}/players", h.addPlayer)
	mux.HandleFunc("DELETE /rankings/{owner}/players/{player}", h.removePlayer)
}

// ---------------------------------------------------------------------------
// GET /rankings
// ---------------------------------------------------------------------------

func (h *RankingsService) getRankings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListRankings(r.Context())
	if err != nil {
		http.Error(w, "failed to load rankings", http.StatusInternalServerError)
		return
	}

	// Rows come back ordered by owner, rank (see the SQL). Group them into
	// OwnerRanking while preserving that order.
	byOwner := make(map[string]*OwnerRanking, len(rows))
	var order []string
	for _, row := range rows {
		or, ok := byOwner[row.Owner]
		if !ok {
			or = &OwnerRanking{Owner: row.Owner}
			byOwner[row.Owner] = or
			order = append(order, row.Owner)
		}
		or.Rankings = append(or.Rankings, PlayerRanking{
			PlayerName: row.PlayerName,
			Rank:       int(row.Rank),
		})
	}

	out := make([]OwnerRanking, 0, len(order))
	for _, owner := range order {
		out = append(out, *byOwner[owner])
	}

	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// POST /rankings (full replace)
// ---------------------------------------------------------------------------

func (h *RankingsService) postRankings(w http.ResponseWriter, r *http.Request) {
	var in OwnerRanking
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if in.Owner == "" {
		http.Error(w, "owner is required", http.StatusBadRequest)
		return
	}
	if len(in.Rankings) == 0 {
		http.Error(w, "rankings must not be empty", http.StatusBadRequest)
		return
	}
	for _, pr := range in.Rankings {
		if pr.PlayerName == "" {
			http.Error(w, "each ranking must include a player_name", http.StatusBadRequest)
			return
		}
	}

	if err := h.replaceRankings(r.Context(), in); err != nil {
		http.Error(w, "failed to save rankings", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, in)
}

// replaceRankings swaps an owner's full ranking list in one transaction:
// delete whatever they had, insert what they just posted.
func (h *RankingsService) replaceRankings(ctx context.Context, in OwnerRanking) error {
	return h.withTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteRankingsByOwner(ctx, in.Owner); err != nil {
			return fmt.Errorf("delete existing rankings: %w", err)
		}

		for _, pr := range in.Rankings {
			if err := q.InsertRanking(ctx, db.InsertRankingParams{
				Owner:      in.Owner,
				PlayerName: pr.PlayerName,
				Rank:       int32(pr.Rank),
			}); err != nil {
				return fmt.Errorf("insert ranking %q: %w", pr.PlayerName, err)
			}
		}

		return nil
	})
}

// ---------------------------------------------------------------------------
// PATCH /rankings/{owner}/move
// ---------------------------------------------------------------------------

func (h *RankingsService) moveRanking(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	if owner == "" {
		http.Error(w, "owner is required", http.StatusBadRequest)
		return
	}

	var req MoveRankingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.PlayerName == "" {
		http.Error(w, "player_name is required", http.StatusBadRequest)
		return
	}
	if req.Rank < 1 {
		http.Error(w, "rank must be >= 1", http.StatusBadRequest)
		return
	}

	updated, err := h.movePlayer(r.Context(), owner, req.PlayerName, req.Rank)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// movePlayer moves playerName to newRank within owner's list, shifting
// everyone else to keep ranks a contiguous 1..N sequence. Only rows whose
// rank actually changes get written.
func (h *RankingsService) movePlayer(ctx context.Context, owner, playerName string, newRank int) (*OwnerRanking, error) {
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if len(rows) == 0 {
			return nil, ErrOwnerNotFound
		}

		idx := indexOfPlayer(rows, playerName)
		if idx == -1 {
			return nil, ErrPlayerNotFound
		}

		if newRank > len(rows) {
			newRank = len(rows)
		}

		moved := rows[idx]
		rows = append(rows[:idx], rows[idx+1:]...)
		insertAt := newRank - 1
		rows = append(rows[:insertAt], append([]db.ListRankingsByOwnerForUpdateRow{moved}, rows[insertAt:]...)...)

		if err := h.applyRankOrder(ctx, q, owner, rows); err != nil {
			return nil, err
		}
		return rows, nil
	})
}

// ---------------------------------------------------------------------------
// POST /rankings/{owner}/players (add)
// ---------------------------------------------------------------------------

func (h *RankingsService) addPlayer(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	if owner == "" {
		http.Error(w, "owner is required", http.StatusBadRequest)
		return
	}

	var req AddPlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.PlayerName == "" {
		http.Error(w, "player_name is required", http.StatusBadRequest)
		return
	}
	if req.Rank != nil && *req.Rank < 1 {
		http.Error(w, "rank must be >= 1", http.StatusBadRequest)
		return
	}

	updated, err := h.addPlayerToRankings(r.Context(), owner, req.PlayerName, req.Rank)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusCreated, updated)
}

// addPlayerToRankings inserts playerName at the requested rank (or the end,
// if rank is nil), shifting existing players down to make room.
func (h *RankingsService) addPlayerToRankings(ctx context.Context, owner, playerName string, rank *int) (*OwnerRanking, error) {
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if indexOfPlayer(rows, playerName) != -1 {
			return nil, ErrPlayerExists
		}

		insertAt := len(rows) // default: append to end
		if rank != nil {
			insertAt = *rank - 1
			if insertAt > len(rows) {
				insertAt = len(rows)
			}
		}

		// Shift everyone at or after insertAt down by one before inserting,
		// so the new row's UNIQUE(owner, rank) slot (if any) is free.
		for i := len(rows) - 1; i >= insertAt; i-- {
			if err := h.updateRankIfChanged(ctx, q, owner, rows[i].PlayerName, rows[i].Rank, int32(i+2)); err != nil {
				return nil, err
			}
		}

		if err := q.InsertRanking(ctx, db.InsertRankingParams{
			Owner:      owner,
			PlayerName: playerName,
			Rank:       int32(insertAt + 1),
		}); err != nil {
			return nil, fmt.Errorf("insert ranking %q: %w", playerName, err)
		}

		final := make([]db.ListRankingsByOwnerForUpdateRow, 0, len(rows)+1)
		final = append(final, rows[:insertAt]...)
		final = append(final, db.ListRankingsByOwnerForUpdateRow{
			Owner:      owner,
			PlayerName: playerName,
		})
		final = append(final, rows[insertAt:]...)
		return final, nil
	})
}

// ---------------------------------------------------------------------------
// DELETE /rankings/{owner}/players/{player} (remove)
// ---------------------------------------------------------------------------

func (h *RankingsService) removePlayer(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	player := r.PathValue("player")
	if owner == "" || player == "" {
		http.Error(w, "owner and player are required", http.StatusBadRequest)
		return
	}

	updated, err := h.removePlayerFromRankings(r.Context(), owner, player)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// removePlayerFromRankings deletes playerName from owner's list and shifts
// everyone ranked below them up by one to keep ranks contiguous.
func (h *RankingsService) removePlayerFromRankings(ctx context.Context, owner, playerName string) (*OwnerRanking, error) {
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if len(rows) == 0 {
			return nil, ErrOwnerNotFound
		}

		idx := indexOfPlayer(rows, playerName)
		if idx == -1 {
			return nil, ErrPlayerNotFound
		}

		if err := q.DeleteRanking(ctx, db.DeleteRankingParams{
			Owner:      owner,
			PlayerName: playerName,
		}); err != nil {
			return nil, fmt.Errorf("delete ranking %q: %w", playerName, err)
		}

		rows = append(rows[:idx], rows[idx+1:]...)

		if err := h.applyRankOrder(ctx, q, owner, rows); err != nil {
			return nil, err
		}
		return rows, nil
	})
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// withTx runs fn inside a transaction, committing on success and rolling
// back automatically otherwise.
func (h *RankingsService) withTx(ctx context.Context, fn func(q *db.Queries) error) error {
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
func (h *RankingsService) withOwnerRowsTx(
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
func (h *RankingsService) applyRankOrder(ctx context.Context, q *db.Queries, owner string, rows []db.ListRankingsByOwnerForUpdateRow) error {
	for i, row := range rows {
		if err := h.updateRankIfChanged(ctx, q, owner, row.PlayerName, row.Rank, int32(i+1)); err != nil {
			return err
		}
	}
	return nil
}

// updateRankIfChanged writes a player's new rank only if it actually differs
// from what's currently stored, avoiding no-op writes during a shift.
func (h *RankingsService) updateRankIfChanged(ctx context.Context, q *db.Queries, owner, playerName string, currentRank, wantRank int32) error {
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
func (h *RankingsService) writeErrIfAny(w http.ResponseWriter, err error) bool {
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
