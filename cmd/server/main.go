package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/ethandilley/rankings/internal/server/rankings"
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

	rankingsService := rankings.NewRankingsService(conn)
	playerService := players.NewPlayerService(conn)

	mux := http.NewServeMux()
	rankingsService.Register(mux)

	log.Println("server listening on :8080")
	if err := http.ListenAndServe(":8080", withCORS(mux)); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// withCORS allows the static frontend (served from a different origin, or
// opened as a local file:// page) to call this API from the browser.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
