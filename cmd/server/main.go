package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethandilley/rankings/internal/espn"
	"github.com/ethandilley/rankings/internal/server/admin"
	"github.com/ethandilley/rankings/internal/server/auth"
	"github.com/ethandilley/rankings/internal/server/middleware"
	"github.com/ethandilley/rankings/internal/server/players"
	"github.com/ethandilley/rankings/internal/server/rankings"
	"github.com/ethandilley/rankings/internal/server/syncstatus"
	"github.com/ethandilley/rankings/internal/server/trades"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(ctx); err != nil {
		log.Fatalf("failed to reach db: %v", err)
	}

	authService := auth.NewAuthService(conn)
	rankingsService := rankings.NewRankingsService(conn, authService)
	playerService := players.NewPlayersService(conn, authService)

	syncEnabled, syncInterval, err := syncstatus.IntervalFromEnv()
	if err != nil {
		log.Fatalf("failed to parse sync interval: %v", err)
	}
	espnService := espn.New(conn, espn.ConfigFromEnv())
	syncService := syncstatus.New(conn, authService, espnService, syncEnabled, syncInterval)
	adminService := admin.NewAdminService(conn, authService, espnService, syncService)
	tradesService := trades.NewTradesService(conn, authService)

	mux := http.NewServeMux()
	authService.Register(mux)
	rankingsService.Register(mux)
	playerService.Register(mux)
	adminService.Register(mux)
	syncService.Register(mux)
	tradesService.Register(mux)
	mux.Handle("/", http.FileServer(http.Dir(staticDir())))

	// 5 requests/second per session with a burst of 10: plenty for a human
	// or the UI, enough of a wall against retry-loops and scripts.
	limiter := middleware.NewRateLimiter(5, 10)

	syncService.Start(ctx)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: limiter.Wrap(withCORS(mux)),
	}

	log.Println("server listening on :8080")
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	case err := <-errCh:
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
