package players

import (
	"encoding/json"
	"net/http"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Player struct {
	ID                int64  `json:"id"`
	PlayerName        string `json:"player_name"`
	Position          string `json:"position"`
	Team              string `json:"team"`
	DraftedByUsername string `json:"drafted_by_username,omitempty"`
}

type PlayersService struct {
	conn *pgxpool.Pool
	q    *db.Queries
	auth *auth.AuthService
}

func NewPlayersService(conn *pgxpool.Pool, authService *auth.AuthService) *PlayersService {
	return &PlayersService{conn: conn, q: db.New(conn), auth: authService}
}

func (h *PlayersService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /players", h.auth.RequireAuth(h.getPlayers))
	mux.HandleFunc("GET /players/search", h.auth.RequireAuth(h.searchPlayers))
}

func (h *PlayersService) getPlayers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListPlayers(r.Context())
	if err != nil {
		http.Error(w, "failed to load players", http.StatusInternalServerError)
		return
	}

	out := make([]Player, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPlayer(row.ID, row.PlayerName, row.Position, row.Team, row.DraftedByUsername))
	}

	writeJSON(w, http.StatusOK, out)
}

func (h *PlayersService) searchPlayers(w http.ResponseWriter, r *http.Request) {
	term := r.URL.Query().Get("q")
	if term == "" {
		http.Error(w, "q is required", http.StatusBadRequest)
		return
	}

	rows, err := h.q.SearchPlayersByName(r.Context(), "%"+term+"%")
	if err != nil {
		http.Error(w, "failed to search players", http.StatusInternalServerError)
		return
	}

	out := make([]Player, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPlayer(row.ID, row.PlayerName, row.Position, row.Team, row.DraftedByUsername))
	}

	writeJSON(w, http.StatusOK, out)
}

func toPlayer(id int64, name, position, team string, draftedByUsername pgtype.Text) Player {
	p := Player{ID: id, PlayerName: name, Position: position, Team: team}
	if draftedByUsername.Valid {
		p.DraftedByUsername = draftedByUsername.String
	}
	return p
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
