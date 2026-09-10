package main

import (
	"log"
	"net/http"
	"time"

	"github.com/ethandilley/rankings/internal/server/rankings"
)

func main() {

	mux := http.NewServeMux()
	mux.Handle("/api/v1/rankings/", http.StripPrefix("/api/v1/rankings", rankings.RegisterRoutes()))

	srv := &http.Server{
		Addr: ":8080",
		Handler: mux,
		IdleTimeout: time.Minute,
		ReadTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	log.Printf("starting server on %s", srv.Addr)
	err := srv.ListenAndServe()
	log.Fatal(err)
}
