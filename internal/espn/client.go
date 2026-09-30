// Package espn fetches the league rosters from the ESPN fantasy read API
// and syncs them into the players table.
//
// It is a port of the former scripts/load_players.py. Name normalization is
// shared with the players service via players.NormalizeName so ESPN and DB
// names keep matching the same way everywhere.
package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	apiBase = "https://lm-api-reads.fantasy.espn.com/apis/v3/games/ffl/seasons"

	// DefaultLeagueID and DefaultSeason match this league.
	DefaultLeagueID = "260889507"
	DefaultSeason   = "2026"
)

// Config holds what the ESPN read API needs. S2 and SWID are the session
// cookies of a logged-in fantasy.espn.com browser session.
type Config struct {
	Season   string
	LeagueID string
	S2       string
	SWID     string
}

// ConfigFromEnv builds a Config from the environment, defaulting the league
// and season to this league.
func ConfigFromEnv() Config {
	season := os.Getenv("ESPN_SEASON")
	if season == "" {
		season = DefaultSeason
	}
	leagueID := os.Getenv("ESPN_LEAGUE_ID")
	if leagueID == "" {
		leagueID = DefaultLeagueID
	}
	return Config{
		Season:   season,
		LeagueID: leagueID,
		S2:       os.Getenv("ESPN_S2"),
		SWID:     os.Getenv("ESPN_SWID"),
	}
}

// League is the subset of the ESPN league response the sync uses.
type League struct {
	Teams []Team
	Picks []Pick
}

// Team is one fantasy team with its roster entries.
type Team struct {
	ID      int
	Entries []Entry
}

// Entry is one roster slot.
type Entry struct {
	PlayerID   int64
	FullName   string
	PositionID int
	ProTeamID  int
}

// Pick is one draft pick, used to populate drafted_at.
type Pick struct {
	PlayerID          int64
	OverallPickNumber int
}

// FetchLeague calls the ESPN read API and returns the parsed league.
func FetchLeague(ctx context.Context, cfg Config) (League, error) {
	if cfg.S2 == "" || cfg.SWID == "" {
		return League{}, fmt.Errorf("ESPN credentials not configured (set ESPN_S2 and ESPN_SWID)")
	}

	q := url.Values{}
	for _, view := range []string{"mSettings", "mRoster", "mTeam", "modular", "mNav", "mDraftDetail"} {
		q.Add("view", view)
	}
	endpoint := fmt.Sprintf("%s/%s/segments/0/leagues/%s?%s", apiBase, cfg.Season, cfg.LeagueID, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return League{}, fmt.Errorf("espn request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://fantasy.espn.com")
	req.Header.Set("Referer", "https://fantasy.espn.com/")
	req.Header.Set("X-Fantasy-Platform", "espn-fantasy-web")
	req.Header.Set("X-Fantasy-Source", "kona")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:154.0) Gecko/20100101 Firefox/154.0")
	req.Header.Set("Cookie", "espn_s2="+cfg.S2+"; SWID="+cfg.SWID)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return League{}, fmt.Errorf("espn fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return League{}, fmt.Errorf("ESPN API returned 403: the session cookies are expired or invalid, rotate ESPN_S2 and ESPN_SWID in .envrc")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return League{}, fmt.Errorf("ESPN API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var raw struct {
		Teams []struct {
			ID     int `json:"id"`
			Roster struct {
				Entries []struct {
					PlayerID        int64 `json:"playerId"`
					PlayerPoolEntry struct {
						Player struct {
							ID                int64  `json:"id"`
							FullName          string `json:"fullName"`
							DefaultPositionID int    `json:"defaultPositionId"`
							ProTeamID         int    `json:"proTeamId"`
						} `json:"player"`
					} `json:"playerPoolEntry"`
				} `json:"entries"`
			} `json:"roster"`
		} `json:"teams"`
		DraftDetail struct {
			Picks []struct {
				PlayerID          int64 `json:"playerId"`
				OverallPickNumber int   `json:"overallPickNumber"`
			} `json:"picks"`
		} `json:"draftDetail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return League{}, fmt.Errorf("espn decode: %w", err)
	}

	league := League{}
	for _, t := range raw.Teams {
		team := Team{ID: t.ID}
		for _, e := range t.Roster.Entries {
			entry := Entry{
				PlayerID:   e.PlayerID,
				FullName:   e.PlayerPoolEntry.Player.FullName,
				PositionID: e.PlayerPoolEntry.Player.DefaultPositionID,
				ProTeamID:  e.PlayerPoolEntry.Player.ProTeamID,
			}
			if entry.PlayerID == 0 {
				entry.PlayerID = e.PlayerPoolEntry.Player.ID
			}
			team.Entries = append(team.Entries, entry)
		}
		league.Teams = append(league.Teams, team)
	}
	for _, p := range raw.DraftDetail.Picks {
		league.Picks = append(league.Picks, Pick{
			PlayerID:          p.PlayerID,
			OverallPickNumber: p.OverallPickNumber,
		})
	}
	return league, nil
}
