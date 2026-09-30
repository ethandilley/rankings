package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ethandilley/rankings/internal/server/admin"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/ethandilley/rankings/internal/server/players"
	"github.com/ethandilley/rankings/internal/server/rankings"
	"github.com/ethandilley/rankings/internal/server/trades"
	"github.com/jackc/pgx/v5"
)

func main() {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL not set")
	}
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer conn.Close(context.Background())

	authService := auth.NewAuthService(conn)
	rankingsService := rankings.NewRankingsService(conn, authService)
	playerService := players.NewPlayersService(conn, authService)
	adminService := admin.NewAdminService(conn, authService)
	tradesService := trades.NewTradesService(conn, authService)

	mux := http.NewServeMux()
	authService.Register(mux)
	rankingsService.Register(mux)
	playerService.Register(mux)
	adminService.Register(mux)
	tradesService.Register(mux)
	mux.Handle("/", http.FileServer(http.Dir(staticDir())))

	log.Println("server listening on :8080")
	if err := http.ListenAndServe(":8080", withCORS(mux)); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func staticDir() string {
	dir := os.Getenv("WEB_DIR")
	if dir == "" {
		dir = "web"
	}
	return dir
}

func allowedOrigins() map[string]bool {
	raw := os.Getenv("ALLOWED_ORIGINS")
	if raw == "" {
		raw = os.Getenv("ALLOWED_ORIGIN")
	}
	if raw == "" {
		raw = "http://localhost:8081"
	}

	allowed := map[string]bool{}
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = true
		}
	}
	return allowed
}

// withCORS allows configured frontend origins to call this API from the browser
// while still sending session cookies.
func withCORS(next http.Handler) http.Handler {
	allowed := allowedOrigins()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
