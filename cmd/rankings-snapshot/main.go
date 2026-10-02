// Command rankings-snapshot captures one timestamped copy of the current
// rankings table for movement and trend history.
//
// Each invocation creates a new snapshot. Example:
//
//	docker compose --env-file .envrc --profile snapshot run --rm rankings-snapshot
//
// Environment: DB_URL (required).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ethandilley/rankings/internal/db"
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

	q := db.New(conn)
	takenAt := time.Now().UTC().Truncate(time.Microsecond)

	if err := q.SnapshotCurrentRankings(context.Background(), pgtype.Timestamptz{
		Time:  takenAt,
		Valid: true,
	}); err != nil {
		log.Fatalf("failed to snapshot rankings: %v", err)
	}

	count, err := q.CountSnapshotRows(context.Background(), pgtype.Timestamptz{
		Time:  takenAt,
		Valid: true,
	})
	if err != nil {
		log.Fatalf("failed to count snapshot rows: %v", err)
	}

	fmt.Println("Ranking snapshot complete:")
	fmt.Printf("  Taken at : %s\n", takenAt.Format(time.RFC3339Nano))
	fmt.Printf("  Rows     : %d\n", count)
}
