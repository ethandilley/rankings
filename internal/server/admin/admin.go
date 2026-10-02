package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/espn"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/ethandilley/rankings/internal/server/syncstatus"
)

type AdminService struct {
	conn *pgx.Conn
	q    *db.Queries
	auth *auth.AuthService
	espn *espn.Service
	sync *syncstatus.Service
}

func NewAdminService(
	conn *pgx.Conn,
	auth *auth.AuthService,
	espnService *espn.Service,
	syncService *syncstatus.Service,
) *AdminService {
	return &AdminService{
		conn: conn,
		q:    db.New(conn),
		auth: auth,
		espn: espnService,
		sync: syncService,
	}
}

func (s *AdminService) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users", s.auth.RequireAuth(s.requireAdmin(s.listUsers)))
	mux.HandleFunc("POST /admin/users", s.auth.RequireAuth(s.requireAdmin(s.createUser)))
	mux.HandleFunc("POST /admin/users/{username}/password", s.auth.RequireAuth(s.requireAdmin(s.resetPassword)))
	mux.HandleFunc("PATCH /admin/users/{username}/admin", s.auth.RequireAuth(s.requireAdmin(s.setAdmin)))
	mux.HandleFunc("DELETE /admin/users/{username}", s.auth.RequireAuth(s.requireAdmin(s.deleteUser)))
	mux.HandleFunc("POST /admin/sync-players", s.auth.RequireAuth(s.requireAdmin(s.syncPlayers)))
}

// syncPlayers runs the ESPN roster sync. ?dry_run=true reports the changes
// without writing to the database.
func (s *AdminService) syncPlayers(w http.ResponseWriter, r *http.Request) {
	dryRun := false
	if v := r.URL.Query().Get("dry_run"); v == "true" || v == "1" {
		dryRun = true
	}

	var report *espn.Report
	var err error
	if s.sync != nil {
		report, err = s.sync.Run(r.Context(), "manual", dryRun)
	} else {
		report, err = s.espn.Sync(r.Context(), dryRun)
	}
	if err != nil {
		if errors.Is(err, espn.ErrFetchFailed) {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, report)
}

type userResponse struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	CreatedAt   string `json:"created_at,omitempty"`
}

var (
	usernamePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
	bcryptCost      = 12
)

func (s *AdminService) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || !user.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *AdminService) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.q.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	responses := make([]userResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, publicUser(db.User{
			ID:          user.ID,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			IsAdmin:     user.IsAdmin,
			CreatedAt:   user.CreatedAt,
		}))
	}
	writeJSON(w, http.StatusOK, responses)
}

func (s *AdminService) createUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		IsAdmin     bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	username := normalizeUsername(request.Username)
	displayName := strings.TrimSpace(request.DisplayName)
	if !usernamePattern.MatchString(username) || displayName == "" || request.Password == "" {
		http.Error(w, "username, display_name, and password are required", http.StatusBadRequest)
		return
	}

	hash, err := hashPassword(request.Password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := s.q.CreateUser(r.Context(), db.CreateUserParams{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		IsAdmin:      request.IsAdmin,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "username already exists", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, publicUser(user))
}

func (s *AdminService) resetPassword(w http.ResponseWriter, r *http.Request) {
	username := normalizeUsername(r.PathValue("username"))

	var request struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if username == "" || request.NewPassword == "" {
		http.Error(w, "username and new_password are required", http.StatusBadRequest)
		return
	}

	if _, err := s.q.GetUserByUsername(r.Context(), username); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hash, err := hashPassword(request.NewPassword)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.q.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		Username:     username,
		PasswordHash: hash,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *AdminService) setAdmin(w http.ResponseWriter, r *http.Request) {
	username := normalizeUsername(r.PathValue("username"))

	var request struct {
		IsAdmin bool `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	current, _ := auth.UserFromContext(r.Context())
	target, err := s.q.GetUserByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if !request.IsAdmin {
		if target.Username == current.Username {
			http.Error(w, "cannot remove your own admin role", http.StatusForbidden)
			return
		}

		admins, err := s.q.CountAdmins(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if admins <= 1 {
			http.Error(w, "cannot remove the last admin", http.StatusConflict)
			return
		}
	}

	if err := s.q.SetUserAdmin(r.Context(), db.SetUserAdminParams{
		Username: username,
		IsAdmin:  request.IsAdmin,
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	updated, err := s.q.GetUserByUsername(r.Context(), username)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, publicUser(updated))
}

func (s *AdminService) deleteUser(w http.ResponseWriter, r *http.Request) {
	username := normalizeUsername(r.PathValue("username"))
	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}

	current, _ := auth.UserFromContext(r.Context())
	if username == current.Username {
		http.Error(w, "cannot delete your own account", http.StatusForbidden)
		return
	}

	target, err := s.q.GetUserByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if target.IsAdmin {
		admins, err := s.q.CountAdmins(r.Context())
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if admins <= 1 {
			http.Error(w, "cannot delete the last admin", http.StatusConflict)
			return
		}
	}

	if err := s.q.DeleteUser(r.Context(), username); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func publicUser(user db.User) userResponse {
	var createdAt string
	if user.CreatedAt.Valid {
		createdAt = user.CreatedAt.Time.UTC().Format(time.RFC3339)
	}

	return userResponse{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		IsAdmin:     user.IsAdmin,
		CreatedAt:   createdAt,
	}
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

