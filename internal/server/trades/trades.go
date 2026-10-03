package trades

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// minRankedPlayers is the minimum number of ranked players an owner must have
// to be included in trade-suggestion computation. Owners below the threshold
// are excluded and surfaced in responses (ai/05-trade-suggestions.md).
const minRankedPlayers = 20

const (
	defaultLimit = 25
	maxLimit     = 100
)

// PlayerRef identifies a player with the context the UI needs to display it.
type PlayerRef struct {
	PlayerID   int64  `json:"player_id"`
	PlayerName string `json:"player_name"`
	Position   string `json:"position"`
	Team       string `json:"team"`
}

type DisagreementPlayer struct {
	PlayerID            int64   `json:"player_id"`
	PlayerName          string  `json:"player_name"`
	Position            string  `json:"position"`
	Team                string  `json:"team"`
	NumOwnersRanked     int     `json:"num_owners_ranked"`
	BestNormalizedRank  float64 `json:"best_normalized_rank"`
	WorstNormalizedRank float64 `json:"worst_normalized_rank"`
	DisagreementSpread  float64 `json:"disagreement_spread"`
	HighestRater        string  `json:"highest_rater"`
	LowestRater         string  `json:"lowest_rater"`
}

type DisagreementsResponse struct {
	Limit            int                  `json:"limit"`
	MinRankedPlayers int                  `json:"min_ranked_players"`
	ExcludedOwners   []string             `json:"excluded_owners"`
	Players          []DisagreementPlayer `json:"players"`
}

// TradeSuggestion is one two-sided trade: owner A gives OwnerAGives (which B
// values more than A does) and owner B gives OwnerBGives (which A values more
// than B does). Normalized ranks (0.0 = best, 1.0 = worst) are included so the
// UI can explain each suggestion.
type TradeSuggestion struct {
	OwnerA        string    `json:"owner_a"`
	OwnerB        string    `json:"owner_b"`
	OwnerAGives   PlayerRef `json:"owner_a_gives"`
	OwnerBGives   PlayerRef `json:"owner_b_gives"`
	AGainScore    float64   `json:"a_gain_score"`
	BGainScore    float64   `json:"b_gain_score"`
	CombinedScore float64   `json:"combined_score"`
	ANormOfAGives float64   `json:"a_norm_of_a_gives"`
	BNormOfAGives float64   `json:"b_norm_of_a_gives"`
	ANormOfBGives float64   `json:"a_norm_of_b_gives"`
	BNormOfBGives float64   `json:"b_norm_of_b_gives"`
}

type PairwiseResponse struct {
	Owner            string            `json:"owner,omitempty"`
	MinRankedPlayers int               `json:"min_ranked_players"`
	ExcludedOwners   []string          `json:"excluded_owners"`
	Suggestions      []TradeSuggestion `json:"suggestions"`
}

// minConsensusOwners is the minimum number of owners that must have ranked a
// player before it can appear in contrarian comparisons (ai/08-consensus-and-
// analytics.md). Below that, the "field" opinion is one or two owners.
const minConsensusOwners = 3

// ContrarianPlayer is one outlier stat: how far an owner's normalized rank of
// a player sits from the rest of the league's (self-excluded) average.
type ContrarianPlayer struct {
	PlayerID            int64   `json:"player_id"`
	PlayerName          string  `json:"player_name"`
	Position            string  `json:"position"`
	Team                string  `json:"team"`
	OwnerNormalizedRank float64 `json:"owner_normalized_rank"`
	FieldNormalizedRank float64 `json:"field_normalized_rank"`
	NumOwnersRanked     int     `json:"num_owners_ranked"`
	Gap                 float64 `json:"gap"`
}

// ContrarianOwner is one owner's "how contrarian am I" personality: their
// single biggest believer (ranks a player higher than the field) and biggest
// skeptic (ranks a player lower than the field), if either exists.
type ContrarianOwner struct {
	Owner           string            `json:"owner"`
	BiggestBeliever *ContrarianPlayer `json:"biggest_believer"`
	BiggestSkeptic  *ContrarianPlayer `json:"biggest_skeptic"`
}

type ContrarianResponse struct {
	Owner              string            `json:"owner,omitempty"`
	MinConsensusOwners int               `json:"min_consensus_owners"`
	Owners             []ContrarianOwner `json:"owners"`
}

type TradesService struct {
	conn *pgxpool.Pool
	q    *db.Queries
	auth *auth.AuthService
}

func NewTradesService(conn *pgxpool.Pool, authService *auth.AuthService) *TradesService {
	return &TradesService{conn: conn, q: db.New(conn), auth: authService}
}

func (s *TradesService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /trade-suggestions/disagreements", s.auth.RequireAuth(s.getDisagreements))
	mux.HandleFunc("GET /trade-suggestions/pairwise", s.auth.RequireAuth(s.getPairwise))
	mux.HandleFunc("GET /contrarian", s.auth.RequireAuth(s.getContrarian))
}

func (s *TradesService) getDisagreements(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := s.q.PlayerDisagreement(r.Context(), db.PlayerDisagreementParams{
		RowLimit:         int32(limit),
		MinRankedPlayers: int32(minRankedPlayers),
	})
	if err != nil {
		http.Error(w, "failed to compute disagreement leaderboard", http.StatusInternalServerError)
		return
	}

	excluded, err := s.q.ListExcludedOwners(r.Context(), int32(minRankedPlayers))
	if err != nil {
		http.Error(w, "failed to load owners", http.StatusInternalServerError)
		return
	}

	players := make([]DisagreementPlayer, 0, len(rows))
	for _, row := range rows {
		players = append(players, DisagreementPlayer{
			PlayerID:            row.PlayerID,
			PlayerName:          row.PlayerName,
			Position:            row.Position,
			Team:                row.Team,
			NumOwnersRanked:     int(row.NumOwnersRanked),
			BestNormalizedRank:  row.BestNormalizedRank,
			WorstNormalizedRank: row.WorstNormalizedRank,
			DisagreementSpread:  row.DisagreementSpread,
			HighestRater:        row.HighestRater,
			LowestRater:         row.LowestRater,
		})
	}

	writeJSON(w, http.StatusOK, DisagreementsResponse{
		Limit:            limit,
		MinRankedPlayers: minRankedPlayers,
		ExcludedOwners:   excluded,
		Players:          players,
	})
}

func (s *TradesService) getPairwise(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	requestedOwner := r.URL.Query().Get("owner")

	normRows, err := s.q.ListNormalizedRankings(r.Context(), int32(minRankedPlayers))
	if err != nil {
		http.Error(w, "failed to load rankings", http.StatusInternalServerError)
		return
	}
	draftedRows, err := s.q.ListDraftedPlayers(r.Context())
	if err != nil {
		http.Error(w, "failed to load players", http.StatusInternalServerError)
		return
	}
	excluded, err := s.q.ListExcludedOwners(r.Context(), int32(minRankedPlayers))
	if err != nil {
		http.Error(w, "failed to load owners", http.StatusInternalServerError)
		return
	}

	if requestedOwner != "" {
		if !ownerKnown(requestedOwner, normRows, excluded) {
			http.Error(w, "owner not found", http.StatusNotFound)
			return
		}
		for _, name := range excluded {
			if name == requestedOwner {
				http.Error(w, fmt.Sprintf("owner %s has fewer than %d ranked players and is excluded from trade suggestions", requestedOwner, minRankedPlayers), http.StatusBadRequest)
				return
			}
		}
	}

	boards := map[string]map[int64]float64{}
	for _, row := range normRows {
		if boards[row.Owner] == nil {
			boards[row.Owner] = map[int64]float64{}
		}
		boards[row.Owner][row.PlayerID] = row.NormalizedRank
	}

	playersByID := map[int64]PlayerRef{}
	draftedBy := map[string]map[int64]bool{}
	for _, row := range draftedRows {
		playersByID[row.ID] = PlayerRef{
			PlayerID:   row.ID,
			PlayerName: row.PlayerName,
			Position:   row.Position,
			Team:       row.Team,
		}
		if row.DraftedByUsername.Valid {
			owner := row.DraftedByUsername.String
			if draftedBy[owner] == nil {
				draftedBy[owner] = map[int64]bool{}
			}
			draftedBy[owner][row.ID] = true
		}
	}

	suggestions := []TradeSuggestion{}
	for _, pair := range ownerPairs(requestedOwner, boards) {
		result, ok := computePairwiseTrade(pairwiseInput{
			aOwner:   pair.a,
			bOwner:   pair.b,
			aNorm:    boards[pair.a],
			bNorm:    boards[pair.b],
			aDrafted: draftedBy[pair.a],
			bDrafted: draftedBy[pair.b],
		})
		if !ok {
			continue
		}
		suggestions = append(suggestions, toTradeSuggestion(pair.a, pair.b, playersByID, result))
	}

	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].CombinedScore != suggestions[j].CombinedScore {
			return suggestions[i].CombinedScore > suggestions[j].CombinedScore
		}
		if suggestions[i].OwnerA != suggestions[j].OwnerA {
			return suggestions[i].OwnerA < suggestions[j].OwnerA
		}
		if suggestions[i].OwnerB != suggestions[j].OwnerB {
			return suggestions[i].OwnerB < suggestions[j].OwnerB
		}
		if suggestions[i].OwnerAGives.PlayerID != suggestions[j].OwnerAGives.PlayerID {
			return suggestions[i].OwnerAGives.PlayerID < suggestions[j].OwnerAGives.PlayerID
		}
		return suggestions[i].OwnerBGives.PlayerID < suggestions[j].OwnerBGives.PlayerID
	})
	if len(suggestions) > limit {
		suggestions = suggestions[:limit]
	}

	writeJSON(w, http.StatusOK, PairwiseResponse{
		Owner:            requestedOwner,
		MinRankedPlayers: minRankedPlayers,
		ExcludedOwners:   excluded,
		Suggestions:      suggestions,
	})
}

// getContrarian returns "how contrarian am I" personality stats
// (ai/08-consensus-and-analytics.md): for each owner (or one owner via
// ?owner=), the player they rate furthest above and below the rest of the
// league. The field average excludes the owner's own vote, and only players
// ranked by at least minConsensusOwners owners are compared.
func (s *TradesService) getContrarian(w http.ResponseWriter, r *http.Request) {
	requestedOwner := r.URL.Query().Get("owner")

	normRows, err := s.q.ListNormalizedRankings(r.Context(), 1)
	if err != nil {
		http.Error(w, "failed to load rankings", http.StatusInternalServerError)
		return
	}
	playerRows, err := s.q.ListPlayers(r.Context())
	if err != nil {
		http.Error(w, "failed to load players", http.StatusInternalServerError)
		return
	}

	boards := map[string]map[int64]float64{}
	for _, row := range normRows {
		if boards[row.Owner] == nil {
			boards[row.Owner] = map[int64]float64{}
		}
		boards[row.Owner][row.PlayerID] = row.NormalizedRank
	}

	playersByID := make(map[int64]PlayerRef, len(playerRows))
	for _, row := range playerRows {
		playersByID[row.ID] = PlayerRef{
			PlayerID:   row.ID,
			PlayerName: row.PlayerName,
			Position:   row.Position,
			Team:       row.Team,
		}
	}

	owners := make([]string, 0, len(boards))
	for owner := range boards {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	if requestedOwner != "" {
		found := false
		for _, owner := range owners {
			if owner == requestedOwner {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "owner not found", http.StatusNotFound)
			return
		}
		owners = []string{requestedOwner}
	}

	writeJSON(w, http.StatusOK, ContrarianResponse{
		Owner:              requestedOwner,
		MinConsensusOwners: minConsensusOwners,
		Owners:             computeContrarian(boards, playersByID, owners),
	})
}

type ownerPair struct{ a, b string }

// ownerPairs returns the owner pairs to evaluate. With a requested owner, that
// owner is always side A (the viewer's perspective); league-wide, pairs use
// lexicographic order so output is stable.
func ownerPairs(requestedOwner string, boards map[string]map[int64]float64) []ownerPair {
	owners := make([]string, 0, len(boards))
	for owner := range boards {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	if requestedOwner != "" {
		pairs := []ownerPair{}
		for _, other := range owners {
			if other != requestedOwner {
				pairs = append(pairs, ownerPair{a: requestedOwner, b: other})
			}
		}
		return pairs
	}

	pairs := []ownerPair{}
	for i := 0; i < len(owners); i++ {
		for j := i + 1; j < len(owners); j++ {
			pairs = append(pairs, ownerPair{a: owners[i], b: owners[j]})
		}
	}
	return pairs
}

func ownerKnown(owner string, rows []db.ListNormalizedRankingsRow, excluded []string) bool {
	for _, row := range rows {
		if row.Owner == owner {
			return true
		}
	}
	for _, name := range excluded {
		if name == owner {
			return true
		}
	}
	return false
}

// pairwiseInput is everything computePairwiseTrade needs for one owner pair,
// extracted from the DB rows so the scoring logic is testable without Postgres.
type pairwiseInput struct {
	aOwner string
	bOwner string
	// Normalized rank (0.0 = best, 1.0 = worst) of each player each owner
	// ranked; missing player = not ranked by that owner.
	aNorm map[int64]float64
	bNorm map[int64]float64
	// Players each owner drafted (owns).
	aDrafted map[int64]bool
	bDrafted map[int64]bool
}

type pairwiseResult struct {
	x        int64 // the player A gives up (B values it more than A does)
	y        int64 // the player B gives up (A values it more than B does)
	aGain    float64
	bGain    float64
	combined float64
	aNormX   float64
	bNormX   float64
	aNormY   float64
	bNormY   float64
}

// computePairwiseTrade finds the best two-sided trade between owners a and b,
// or reports that none exists. See ai/05-trade-suggestions.md:
//
//   - X = A-owned player B rates higher than A does (largest gap), if any
//   - Y = B-owned player A rates higher than B does (largest gap), if any
//   - A's gain = how much better A rates Y (received) than X (given up)
//   - B's gain = how much better B rates X (received) than Y (given up)
//
// A trade is only suggested when both owners strictly prefer receiving the
// other's player to keeping their own.
func computePairwiseTrade(in pairwiseInput) (pairwiseResult, bool) {
	x, okX := bestDesiredOtherSide(in.aDrafted, in.aNorm, in.bNorm)
	if !okX {
		return pairwiseResult{}, false
	}
	y, okY := bestDesiredOtherSide(in.bDrafted, in.bNorm, in.aNorm)
	if !okY {
		return pairwiseResult{}, false
	}

	// A gives up X to receive Y: A gains if it rates Y higher than X
	// (lower normalized rank is better, so A_norm(X) - A_norm(Y) > 0).
	aGain := in.aNorm[x] - in.aNorm[y]
	// B gives up Y to receive X: B gains if it rates X higher than Y.
	bGain := in.bNorm[y] - in.bNorm[x]
	if aGain <= 0 || bGain <= 0 {
		return pairwiseResult{}, false
	}

	return pairwiseResult{
		x:        x,
		y:        y,
		aGain:    aGain,
		bGain:    bGain,
		combined: aGain + bGain,
		aNormX:   in.aNorm[x],
		bNormX:   in.bNorm[x],
		aNormY:   in.aNorm[y],
		bNormY:   in.bNorm[y],
	}, true
}

// maxGapPlayer returns the player in scope where own rates it more favorably
// (lower normalized rank) than other does, by the largest strictly positive
// gap, along with that gap. Players missing from either side are skipped.
// Ties break on lower player ID for deterministic output.
func maxGapPlayer(scope map[int64]bool, own, other map[int64]float64) (int64, float64, bool) {
	var bestID int64
	bestGap := 0.0
	found := false
	for id := range scope {
		ownRank, rankedByOwn := own[id]
		otherRank, rankedByOther := other[id]
		if !rankedByOwn || !rankedByOther {
			continue
		}
		gap := ownRank - otherRank
		if gap <= 0 {
			continue
		}
		if !found || gap > bestGap || (gap == bestGap && id < bestID) {
			bestID, bestGap, found = id, gap, true
		}
	}
	return bestID, bestGap, found
}

// bestDesiredOtherSide returns the player in owned that the other owner rates
// more favorably (lower normalized rank) than own does, by the largest gap.
// Players not ranked by both sides are skipped. Ties break on lower player ID
// for deterministic output.
func bestDesiredOtherSide(owned map[int64]bool, own, other map[int64]float64) (int64, bool) {
	id, _, ok := maxGapPlayer(owned, own, other)
	return id, ok
}

// playerConsensus aggregates one player's normalized ranks across owners.
type playerConsensus struct {
	sum   float64
	count int
}

// contrarianForOwner finds the single player an owner rates furthest above
// (biggest believer) and furthest below (biggest skeptic) the rest of the
// league. The field average for each player excludes the owner's own vote so
// an owner can't inflate their own outlier. A player only qualifies when at
// least minConsensusOwners owners ranked it.
func contrarianForOwner(owner string, board map[int64]float64, consensus map[int64]playerConsensus, playersByID map[int64]PlayerRef) ContrarianOwner {
	out := ContrarianOwner{Owner: owner}

	scope := map[int64]bool{}
	fieldExcl := map[int64]float64{}
	for id, norm := range board {
		c, ok := consensus[id]
		if !ok || c.count < minConsensusOwners {
			continue
		}
		scope[id] = true
		fieldExcl[id] = (c.sum - norm) / float64(c.count-1)
	}

	// Believer: the field rates the player higher (lower normalized rank) than
	// the owner does, so maximize fieldExcl - board.
	if id, gap, ok := maxGapPlayer(scope, fieldExcl, board); ok {
		out.BiggestBeliever = toContrarianPlayer(id, board[id], fieldExcl[id], consensus[id].count, gap, playersByID)
	}
	// Skeptic: the owner rates the player higher than the field, so maximize
	// board - fieldExcl.
	if id, gap, ok := maxGapPlayer(scope, board, fieldExcl); ok {
		out.BiggestSkeptic = toContrarianPlayer(id, board[id], fieldExcl[id], consensus[id].count, gap, playersByID)
	}
	return out
}

// computeContrarian returns contrarian stats for the given owners (already
// sorted) against the league-wide consensus built from all boards.
func computeContrarian(boards map[string]map[int64]float64, playersByID map[int64]PlayerRef, owners []string) []ContrarianOwner {
	consensus := map[int64]playerConsensus{}
	for _, board := range boards {
		for id, norm := range board {
			c := consensus[id]
			c.sum += norm
			c.count++
			consensus[id] = c
		}
	}

	out := make([]ContrarianOwner, 0, len(owners))
	for _, owner := range owners {
		out = append(out, contrarianForOwner(owner, boards[owner], consensus, playersByID))
	}
	return out
}

func toContrarianPlayer(id int64, ownerNorm, fieldNorm float64, numOwners int, gap float64, playersByID map[int64]PlayerRef) *ContrarianPlayer {
	ref := playersByID[id]
	return &ContrarianPlayer{
		PlayerID:            ref.PlayerID,
		PlayerName:          ref.PlayerName,
		Position:            ref.Position,
		Team:                ref.Team,
		OwnerNormalizedRank: ownerNorm,
		FieldNormalizedRank: fieldNorm,
		NumOwnersRanked:     numOwners,
		Gap:                 gap,
	}
}

func toTradeSuggestion(aOwner, bOwner string, playersByID map[int64]PlayerRef, result pairwiseResult) TradeSuggestion {
	return TradeSuggestion{
		OwnerA:        aOwner,
		OwnerB:        bOwner,
		OwnerAGives:   playersByID[result.x],
		OwnerBGives:   playersByID[result.y],
		AGainScore:    result.aGain,
		BGainScore:    result.bGain,
		CombinedScore: result.combined,
		ANormOfAGives: result.aNormX,
		BNormOfAGives: result.bNormX,
		ANormOfBGives: result.aNormY,
		BNormOfBGives: result.bNormY,
	}
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxLimit)
	}
	return n, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
