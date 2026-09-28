package players

import (
	"encoding/json"
	"net/http"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/jackc/pgx/v5"
)

type Player struct {
	ID          int64  `json:"id"`
	Owner       string `json:"owner"`
	PlayerName  string `json:"player_name"`
	Position    string `json:"position"`
	Team        string `json:"team"`
	DraftedAt   int    `json:"drafted_at"`
}

type PlayersService struct {
	conn *pgx.Conn
	q    *db.Queries
	auth *auth.AuthService
}

func NewPlayersService(conn *pgx.Conn, authService *auth.AuthService) *PlayersService {
	return &PlayersService{conn: conn, q: db.New(conn), auth: authService}
}

func (h *PlayersService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /players", h.auth.RequireAuth(h.getPlayers))
}

func (h *PlayersService) getPlayers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListPlayers(r.Context())
	if err != nil {
		http.Error(w, "failed to load players", http.StatusInternalServerError)
		return
	}

	out := make([]Player, 0, len(rows))
	for _, row := range rows {
		out = append(out, Player{
			ID:         row.ID,
			Owner:      row.Owner,
			PlayerName: row.PlayerName,
			Position:   row.Position,
			Team:       row.Team,
			DraftedAt:  int(row.DraftedAt),
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
