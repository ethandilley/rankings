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

	mux := http.NewServeMux()
	rankingsService.Register(mux)

	log.Println("server listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
