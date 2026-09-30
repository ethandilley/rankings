// Command espn-sync syncs the ESPN league rosters into the players table.
//
// It shares the sync code path with POST /admin/sync-players and is meant
// for a daily cron entry. Example (this machine has no crontab installed;
// see NOTES_DECISIONS.md for the recommended line):
//
//	docker compose --env-file .envrc --profile sync run --rm espn-sync
//
// Environment: DB_URL (required), ESPN_S2 and ESPN_SWID (required),
// ESPN_LEAGUE_ID and ESPN_SEASON (optional, default to this league).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5"

	"github.com/ethandilley/rankings/internal/espn"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report changes without writing to the database")
	flag.Parse()

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL not set")
	}
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer conn.Close(context.Background())

	svc := espn.New(conn, espn.ConfigFromEnv())
	report, err := svc.Sync(context.Background(), *dryRun)
	if err != nil {
		log.Fatalf("sync failed: %v", err)
	}

	for _, line := range report.Changes {
		fmt.Println(line)
	}
	for _, warning := range report.Warnings {
		fmt.Println("WARN    " + warning)
	}

	fmt.Println()
	fmt.Println("Database sync summary:")
	fmt.Printf("  ESPN players : %d\n", report.EspnPlayers)
	fmt.Printf("  DB players   : %d\n", report.DBPlayers)
	fmt.Printf("  Inserted     : %d\n", report.Inserted)
	fmt.Printf("  Updated      : %d\n", report.Updated)
	fmt.Printf("  Deleted      : %d (skipped %d referenced by rankings)\n", report.Deleted, len(report.DeleteSkipped))
	fmt.Printf("  Unchanged    : %d\n", report.Unchanged)
	if *dryRun {
		fmt.Println("\n(dry run — no changes committed)")
	} else {
		fmt.Println("\nChanges committed.")
	}
}
