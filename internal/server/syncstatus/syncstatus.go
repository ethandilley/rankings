package syncstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ethandilley/rankings/internal/db"
	"github.com/ethandilley/rankings/internal/espn"
	"github.com/ethandilley/rankings/internal/server/auth"
)

type Service struct {
	q        *db.Queries
	auth     *auth.AuthService
	espn     *espn.Service
	enabled  bool
	interval time.Duration
}

func New(
	conn *pgx.Conn,
	auth *auth.AuthService,
	espnService *espn.Service,
	enabled bool,
	interval time.Duration,
) *Service {
	return &Service{
		q:        db.New(conn),
		auth:     auth,
		espn:     espnService,
		enabled:  enabled,
		interval: interval,
	}
}

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /sync/status", s.auth.RequireAuth(s.status))
}

func IntervalFromEnv() (bool, time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("ESPN_SYNC_INTERVAL"))
	if raw == "" {
		return false, 0, nil
	}
	interval, err := time.ParseDuration(raw)
	if err != nil {
		return false, 0, fmt.Errorf("invalid ESPN_SYNC_INTERVAL %q: %w", raw, err)
	}
	if interval <= 0 {
		return false, 0, fmt.Errorf("ESPN_SYNC_INTERVAL must be positive, got %s", raw)
	}
	return true, interval, nil
}

func (s *Service) Start(ctx context.Context) {
	if !s.enabled {
		return
	}

	go func() {
		s.run(ctx, "startup")
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.run(ctx, "ticker")
			}
		}
	}()
}

type SyncEntry struct {
	ID                 int64      `json:"id"`
	StartedAt          time.Time  `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	Source             string     `json:"source"`
	Status             string     `json:"status"`
	Message            string     `json:"message,omitempty"`
	Inserted           int32      `json:"inserted"`
	Updated            int32      `json:"updated"`
	Deleted            int32      `json:"deleted"`
	Unchanged          int32      `json:"unchanged"`
	DeleteSkippedCount int32      `json:"delete_skipped_count"`
}

type StatusResponse struct {
	Enabled       bool        `json:"enabled"`
	Interval      string      `json:"interval,omitempty"`
	LastSuccessAt *time.Time  `json:"last_success_at,omitempty"`
	LastAttemptAt *time.Time  `json:"last_attempt_at,omitempty"`
	LastStatus    string      `json:"last_status,omitempty"`
	LastError     string      `json:"last_error,omitempty"`
	Recent        []SyncEntry `json:"recent"`
}

func (s *Service) Run(ctx context.Context, source string, dryRun bool) (*espn.Report, error) {
	started := time.Now().UTC().Truncate(time.Microsecond)
	report, err := s.espn.Sync(ctx, dryRun)
	if !dryRun {
		s.record(ctx, started, source, report, err)
	}
	return report, err
}

func (s *Service) run(ctx context.Context, source string) {
	report, err := s.Run(ctx, source, false)
	if err != nil {
		log.Printf("espn sync failed (%s): %v", source, err)
		return
	}
	log.Printf("espn sync ok (%s): inserted %d, updated %d, deleted %d, unchanged %d",
		source, report.Inserted, report.Updated, report.Deleted, report.Unchanged)
}

func (s *Service) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	recentRows, err := s.q.ListRecentSyncLog(ctx, 20)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	recent := make([]SyncEntry, 0, len(recentRows))
	for _, row := range recentRows {
		recent = append(recent, toEntry(row))
	}

	res := StatusResponse{
		Enabled:  s.enabled,
		Recent:   recent,
		Interval: s.interval.String(),
	}

	if latest, err := s.q.GetLatestSyncLog(ctx); err == nil {
		started := latest.StartedAt.Time.UTC()
		res.LastAttemptAt = &started
		res.LastStatus = latest.Status
		if latest.Status == "failure" {
			res.LastError = latest.Message.String
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if lastSuccess, err := s.q.GetLastSuccessfulSync(ctx); err == nil {
		res.LastSuccessAt = timeFromTimestamptz(lastSuccess)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Service) record(ctx context.Context, started time.Time, source string, report *espn.Report, syncErr error) {
	finished := time.Now().UTC().Truncate(time.Microsecond)

	params := db.InsertSyncLogParams{
		StartedAt:  timestamptz(started),
		FinishedAt: timestamptz(finished),
		Source:     source,
		Status:     "failure",
	}

	if syncErr == nil && report != nil {
		params.Status = "success"
		params.Message = pgtype.Text{
			String: fmt.Sprintf(
				"inserted %d, updated %d, deleted %d, unchanged %d",
				report.Inserted, report.Updated, report.Deleted, report.Unchanged,
			),
			Valid: true,
		}
		params.Inserted = int32(report.Inserted)
		params.Updated = int32(report.Updated)
		params.Deleted = int32(report.Deleted)
		params.Unchanged = int32(report.Unchanged)
		params.DeleteSkippedCount = int32(len(report.DeleteSkipped))
	} else if syncErr != nil {
		params.Message = pgtype.Text{String: syncErr.Error(), Valid: true}
	}

	if _, err := s.q.InsertSyncLog(ctx, params); err != nil {
		log.Printf("record sync log: %v", err)
	}
}

func toEntry(row db.SyncLog) SyncEntry {
	entry := SyncEntry{
		ID:                 row.ID,
		StartedAt:          row.StartedAt.Time.UTC(),
		Source:             row.Source,
		Status:             row.Status,
		Message:            row.Message.String,
		Inserted:           row.Inserted,
		Updated:            row.Updated,
		Deleted:            row.Deleted,
		Unchanged:          row.Unchanged,
		DeleteSkippedCount: row.DeleteSkippedCount,
	}
	if t := timeFromTimestamptz(row.FinishedAt); t != nil {
		entry.FinishedAt = t
	}
	return entry
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func timeFromTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
