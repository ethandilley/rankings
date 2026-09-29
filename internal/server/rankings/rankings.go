package rankings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/jackc/pgx/v5"
)

type PlayerRanking struct {
	PlayerID       int64  `json:"player_id"`
	PlayerName     string `json:"player_name"`
	Position       string `json:"position"`
	Team           string `json:"team"`
	OverallRank    int    `json:"overall_rank"`
	PositionalRank int    `json:"positional_rank"`
}

type ConsensusEntry struct {
	PlayerID          int64   `json:"player_id"`
	PlayerName        string  `json:"player_name"`
	Position          string  `json:"position"`
	Team              string  `json:"team"`
	AvgPositionalRank float64 `json:"avg_positional_rank"`
	OwnerCount        int     `json:"owner_count"`
}

type OwnerRanking struct {
	Owner    string          `json:"owner"`
	Rankings []PlayerRanking `json:"rankings"`
}

type MoveRankingRequest struct {
	PlayerID   *int64 `json:"player_id,omitempty"`
	PlayerName string `json:"player_name,omitempty"`
	Rank       int    `json:"rank"`
}

type AddPlayerRequest struct {
	PlayerID   *int64 `json:"player_id,omitempty"`
	PlayerName string `json:"player_name,omitempty"`
	Rank       *int   `json:"rank,omitempty"` // optional; nil = append to end
}

var (
	ErrOwnerNotFound    = errors.New("owner not found")
	ErrPlayerNotFound   = errors.New("player not found")
	ErrPlayerAmbiguous  = errors.New("player name is ambiguous")
	ErrPlayerExists     = errors.New("player already ranked for this owner")
	ErrInvalidPosition  = errors.New("unknown position")
	ErrPositionRequired = errors.New("position is required")
)

// flexPositions are the positions counted as FLEX-eligible. The league
// starts RB/WR/TE in its flex slots (see NOTES_DECISIONS.md, doc 03).
var flexPositions = []string{"RB", "WR", "TE"}

type positionFilterKind int

const (
	filterNone  positionFilterKind = iota // no ?position= param: unfiltered
	filterFlex                            // ?position=FLEX: RB, WR, TE combined
	filterExact                           // ?position=<one position>
)

// parsePositionFilter interprets the raw ?position= query value. The exact
// value is upper-cased but not validated here; the caller checks it against
// the positions that actually exist in the players table so the API doesn't
// drift when new position values appear in the data.
func parsePositionFilter(raw string) (kind positionFilterKind, value string, err error) {
	if raw == "" {
		return filterNone, "", nil
	}
	value = strings.ToUpper(raw)
	if value == "FLEX" {
		return filterFlex, "", nil
	}
	return filterExact, value, nil
}

type RankingsService struct {
	conn *pgx.Conn
	q    *db.Queries
	auth *auth.AuthService
}

func NewRankingsService(conn *pgx.Conn, authService *auth.AuthService) *RankingsService {
	return &RankingsService{conn: conn, q: db.New(conn), auth: authService}
}

func (h *RankingsService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /rankings", h.auth.RequireAuth(h.getRankings))
	mux.HandleFunc("POST /rankings", h.auth.RequireAuth(h.postRankings))
	mux.HandleFunc("GET /rankings/consensus", h.auth.RequireAuth(h.getConsensus))
	mux.HandleFunc("PATCH /rankings/{owner}/move", h.auth.RequireAuth(h.moveRanking))
	mux.HandleFunc("POST /rankings/{owner}/players", h.auth.RequireAuth(h.addPlayer))
	mux.HandleFunc("DELETE /rankings/{owner}/players/{player}", h.auth.RequireAuth(h.removePlayer))
}

func (h *RankingsService) requireOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}

	owner := r.PathValue("owner")
	if owner != user.Username {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}

	return owner, true
}

// ---------------------------------------------------------------------------
// GET /rankings
// ---------------------------------------------------------------------------

func (h *RankingsService) getRankings(w http.ResponseWriter, r *http.Request) {
	positions, err := h.resolvePositionFilter(r.Context(), r.URL.Query().Get("position"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var rows []db.ListRankingsWithPositionalRankRow
	if len(positions) == 0 {
		fetched, ferr := h.q.ListRankingsWithPositionalRank(r.Context())
		if ferr != nil {
			http.Error(w, "failed to load rankings", http.StatusInternalServerError)
			return
		}
		rows = fetched
	} else {
		filtered, ferr := h.q.ListRankingsWithPositionalRankFiltered(r.Context(), positions)
		if ferr != nil {
			http.Error(w, "failed to load rankings", http.StatusInternalServerError)
			return
		}
		for _, f := range filtered {
			rows = append(rows, db.ListRankingsWithPositionalRankRow{
				Owner: f.Owner, PlayerID: f.PlayerID, PlayerName: f.PlayerName,
				Position: f.Position, Team: f.Team,
				OverallRank: f.OverallRank, PositionalRank: f.PositionalRank,
			})
		}
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
		or.Rankings = append(or.Rankings, toPlayerRanking(row.PlayerID, row.PlayerName, row.Position, row.Team, int(row.OverallRank), int(row.PositionalRank)))
	}

	out := make([]OwnerRanking, 0, len(order))
	for _, owner := range order {
		out = append(out, *byOwner[owner])
	}

	writeJSON(w, http.StatusOK, out)
}

// resolvePositionFilter turns the raw ?position= value into the concrete list
// of position strings to filter on (empty = no filter). FLEX expands to the
// flex-eligible positions; a single position must exist in the players table.
func (h *RankingsService) resolvePositionFilter(ctx context.Context, raw string) ([]string, error) {
	kind, value, err := parsePositionFilter(raw)
	if err != nil {
		return nil, err
	}
	switch kind {
	case filterNone:
		return nil, nil
	case filterFlex:
		return flexPositions, nil
	default:
		existing, err := h.q.ListDistinctPositions(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load positions: %w", err)
		}
		for _, pos := range existing {
			if strings.EqualFold(pos, value) {
				return []string{pos}, nil
			}
		}
		return nil, ErrInvalidPosition
	}
}

// ---------------------------------------------------------------------------
// GET /rankings/consensus?position=
// ---------------------------------------------------------------------------

// getConsensus returns the league-wide positional consensus: for each player
// in the requested position(s), the average positional rank across every
// owner who ranked them, sorted best-first. "position" is required and may be
// a single position or the synthetic FLEX value.
func (h *RankingsService) getConsensus(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("position")
	if raw == "" {
		http.Error(w, ErrPositionRequired.Error(), http.StatusBadRequest)
		return
	}

	positions, err := h.resolvePositionFilter(r.Context(), raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := h.q.ListPositionalConsensus(r.Context(), positions)
	if err != nil {
		http.Error(w, "failed to load consensus", http.StatusInternalServerError)
		return
	}

	out := make([]ConsensusEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConsensusEntry{
			PlayerID:          row.PlayerID,
			PlayerName:        row.PlayerName,
			Position:          row.Position,
			Team:              row.Team,
			AvgPositionalRank: row.AvgPositionalRank,
			OwnerCount:        int(row.OwnerCount),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// POST /rankings (full replace)
// ---------------------------------------------------------------------------

func (h *RankingsService) postRankings(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var in OwnerRanking
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if in.Owner != user.Username {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if len(in.Rankings) == 0 {
		http.Error(w, "rankings must not be empty", http.StatusBadRequest)
		return
	}

	// Resolve every entry to a player id before touching the database, so a
	// bad name fails the request without deleting anything. In the request
	// body player_id is a plain int64; zero means "not provided".
	ids := make([]int64, len(in.Rankings))
	for i, pr := range in.Rankings {
		var idPtr *int64
		if pr.PlayerID != 0 {
			idPtr = &pr.PlayerID
		}
		id, err := h.resolvePlayerID(r.Context(), idPtr, pr.PlayerName)
		if err != nil {
			http.Error(w, fmt.Sprintf("rankings[%d]: %s", i, err.Error()), http.StatusBadRequest)
			return
		}
		ids[i] = id
	}

	if err := h.replaceRankings(r.Context(), in.Owner, ids); err != nil {
		http.Error(w, "failed to save rankings", http.StatusInternalServerError)
		return
	}

	out, err := h.ownerRanking(r.Context(), in.Owner)
	if err != nil {
		http.Error(w, "failed to load rankings", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, out)
}

// replaceRankings swaps an owner's full ranking list in one transaction:
// delete whatever they had, insert what they just posted.
func (h *RankingsService) replaceRankings(ctx context.Context, owner string, playerIDs []int64) error {
	return h.withTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteRankingsByOwner(ctx, owner); err != nil {
			return fmt.Errorf("delete existing rankings: %w", err)
		}

		for i, id := range playerIDs {
			if err := q.InsertRanking(ctx, db.InsertRankingParams{
				Owner:    owner,
				PlayerID: id,
				Rank:     int32(i + 1),
			}); err != nil {
				return fmt.Errorf("insert ranking %d: %w", id, err)
			}
		}

		return nil
	})
}

// ---------------------------------------------------------------------------
// PATCH /rankings/{owner}/move
// ---------------------------------------------------------------------------

func (h *RankingsService) moveRanking(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.requireOwner(w, r)
	if !ok {
		return
	}

	var req MoveRankingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if (req.PlayerID == nil) == (req.PlayerName == "") {
		http.Error(w, "exactly one of player_id or player_name is required", http.StatusBadRequest)
		return
	}
	if req.Rank < 1 {
		http.Error(w, "rank must be >= 1", http.StatusBadRequest)
		return
	}

	id, err := h.resolvePlayerID(r.Context(), req.PlayerID, req.PlayerName)
	if h.writeErrIfAny(w, err) {
		return
	}

	updated, err := h.movePlayer(r.Context(), owner, id, req.Rank)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// movePlayer moves the player to newRank within owner's list, shifting
// everyone else to keep ranks a contiguous 1..N sequence. Only rows whose
// rank actually changes get written.
func (h *RankingsService) movePlayer(ctx context.Context, owner string, playerID int64, newRank int) (*OwnerRanking, error) {
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if len(rows) == 0 {
			return nil, ErrOwnerNotFound
		}

		idx := indexOfPlayerID(rows, playerID)
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
	owner, ok := h.requireOwner(w, r)
	if !ok {
		return
	}

	var req AddPlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if (req.PlayerID == nil) == (req.PlayerName == "") {
		http.Error(w, "exactly one of player_id or player_name is required", http.StatusBadRequest)
		return
	}
	if req.Rank != nil && *req.Rank < 1 {
		http.Error(w, "rank must be >= 1", http.StatusBadRequest)
		return
	}

	id, err := h.resolvePlayerID(r.Context(), req.PlayerID, req.PlayerName)
	if h.writeErrIfAny(w, err) {
		return
	}

	player, err := h.q.GetPlayerByID(r.Context(), id)
	if err != nil {
		http.Error(w, "player not found", http.StatusNotFound)
		return
	}

	updated, err := h.addPlayerToRankings(r.Context(), owner, player, req.Rank)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusCreated, updated)
}

// addPlayerToRankings inserts the player at the requested rank (or the end,
// if rank is nil), shifting existing players down to make room.
func (h *RankingsService) addPlayerToRankings(ctx context.Context, owner string, player db.GetPlayerByIDRow, rank *int) (*OwnerRanking, error) {
	playerID := player.ID
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if indexOfPlayerID(rows, playerID) != -1 {
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
			if err := h.updateRankIfChanged(ctx, q, owner, rows[i].PlayerID, rows[i].Rank, int32(i+2)); err != nil {
				return nil, err
			}
		}

		if err := q.InsertRanking(ctx, db.InsertRankingParams{
			Owner:    owner,
			PlayerID: playerID,
			Rank:     int32(insertAt + 1),
		}); err != nil {
			return nil, fmt.Errorf("insert ranking %d: %w", playerID, err)
		}

		final := make([]db.ListRankingsByOwnerForUpdateRow, 0, len(rows)+1)
		final = append(final, rows[:insertAt]...)
		final = append(final, db.ListRankingsByOwnerForUpdateRow{
			Owner:      owner,
			PlayerID:   playerID,
			PlayerName: player.PlayerName,
			Position:   player.Position,
			Team:       player.Team,
		})
		final = append(final, rows[insertAt:]...)
		return final, nil
	})
}

// ---------------------------------------------------------------------------
// DELETE /rankings/{owner}/players/{player} (remove)
// ---------------------------------------------------------------------------

func (h *RankingsService) removePlayer(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	rawID := r.PathValue("player")
	playerID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		http.Error(w, "player must be a numeric player id", http.StatusBadRequest)
		return
	}

	updated, err := h.removePlayerFromRankings(r.Context(), owner, playerID)
	if h.writeErrIfAny(w, err) {
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// removePlayerFromRankings deletes the player from owner's list and shifts
// everyone ranked below them up by one to keep ranks contiguous.
func (h *RankingsService) removePlayerFromRankings(ctx context.Context, owner string, playerID int64) (*OwnerRanking, error) {
	return h.withOwnerRowsTx(ctx, owner, func(q *db.Queries, rows []db.ListRankingsByOwnerForUpdateRow) ([]db.ListRankingsByOwnerForUpdateRow, error) {
		if len(rows) == 0 {
			return nil, ErrOwnerNotFound
		}

		idx := indexOfPlayerID(rows, playerID)
		if idx == -1 {
			return nil, ErrPlayerNotFound
		}

		if err := q.DeleteRanking(ctx, db.DeleteRankingParams{
			Owner:    owner,
			PlayerID: playerID,
		}); err != nil {
			return nil, fmt.Errorf("delete ranking %d: %w", playerID, err)
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

// resolvePlayerID maps a request reference to a players.id. A numeric
// player_id wins and is validated against the table; otherwise the name is
// resolved with a case-insensitive exact match, which must hit exactly one
// player.
func (h *RankingsService) resolvePlayerID(ctx context.Context, id *int64, name string) (int64, error) {
	if id != nil {
		if _, err := h.q.GetPlayerByID(ctx, *id); err != nil {
			return 0, ErrPlayerNotFound
		}
		return *id, nil
	}

	rows, err := h.q.FindPlayersByName(ctx, name)
	if err != nil {
		return 0, err
	}
	switch len(rows) {
	case 0:
		return 0, ErrPlayerNotFound
	case 1:
		return rows[0].ID, nil
	default:
		return 0, ErrPlayerAmbiguous
	}
}

// ownerRanking loads one owner's list (1..N ranks) for a response body.
func (h *RankingsService) ownerRanking(ctx context.Context, owner string) (*OwnerRanking, error) {
	rows, err := h.q.ListRankingsByOwnerForUpdate(ctx, owner)
	if err != nil {
		return nil, err
	}
	return &OwnerRanking{Owner: owner, Rankings: toPlayerRankings(rows)}, nil
}

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
		if err := h.updateRankIfChanged(ctx, q, owner, row.PlayerID, row.Rank, int32(i+1)); err != nil {
			return err
		}
	}
	return nil
}

// updateRankIfChanged writes a player's new rank only if it actually differs
// from what's currently stored, avoiding no-op writes during a shift.
func (h *RankingsService) updateRankIfChanged(ctx context.Context, q *db.Queries, owner string, playerID int64, currentRank, wantRank int32) error {
	if currentRank == wantRank {
		return nil
	}
	if err := q.UpdateRankingRank(ctx, db.UpdateRankingRankParams{
		Owner:    owner,
		PlayerID: playerID,
		Rank:     wantRank,
	}); err != nil {
		return fmt.Errorf("update rank for %d: %w", playerID, err)
	}
	return nil
}

// indexOfPlayerID returns the index of the row with playerID in rows, or -1
// if absent.
func indexOfPlayerID(rows []db.ListRankingsByOwnerForUpdateRow, playerID int64) int {
	for i, row := range rows {
		if row.PlayerID == playerID {
			return i
		}
	}
	return -1
}

func toPlayerRanking(playerID int64, playerName, position, team string, overallRank, positionalRank int) PlayerRanking {
	return PlayerRanking{
		PlayerID: playerID, PlayerName: playerName, Position: position, Team: team,
		OverallRank: overallRank, PositionalRank: positionalRank,
	}
}

// toPlayerRankings converts ordered rows into the API response shape,
// assigning overall_rank = i+1 positionally rather than trusting row.Rank
// (which may be stale immediately after a mutation in the same tx).
// positional_rank is derived the same way the SQL window function does it:
// the 1-based count of how many rows at or before this one share its
// position.
func toPlayerRankings(rows []db.ListRankingsByOwnerForUpdateRow) []PlayerRanking {
	out := make([]PlayerRanking, len(rows))
	seen := make(map[string]int)
	for i, row := range rows {
		seen[row.Position]++
		out[i] = toPlayerRanking(row.PlayerID, row.PlayerName, row.Position, row.Team, i+1, seen[row.Position])
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
	case errors.Is(err, ErrPlayerAmbiguous):
		http.Error(w, err.Error(), http.StatusBadRequest)
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
