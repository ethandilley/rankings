package espn

import (
	"fmt"

	"github.com/ethandilley/rankings/internal/server/players"
)

// proTeamMap mirrors PRO_TEAM_MAP from the former scripts/load_players.py.
// 0 is the free-agent pool.
var proTeamMap = map[int]string{
	0: "FA", 1: "ATL", 2: "BUF", 3: "CHI", 4: "CIN", 5: "CLE", 6: "DAL",
	7: "DEN", 8: "DET", 9: "GB", 10: "TEN", 11: "IND", 12: "KC", 13: "LV",
	14: "LAR", 15: "MIA", 16: "MIN", 17: "NE", 18: "NO", 19: "NYG",
	20: "NYJ", 21: "PHI", 22: "ARI", 23: "PIT", 24: "LAC", 25: "SF",
	26: "SEA", 27: "TB", 28: "WSH", 29: "CAR", 30: "JAX", 33: "BAL", 34: "HOU",
}

// positionMap extends the old script's map (QB/RB/WR/TE) with the kicker
// (5) and D/ST (16) ids the API actually returns; the script silently
// skipped those slots, which left K and D/ST rows un-synced.
var positionMap = map[int]string{
	1: "QB", 2: "RB", 3: "WR", 4: "TE", 5: "K", 16: "D/ST",
}

// teamOwnerMap maps fantasy team IDs to the usernames in this league. The
// old script used display names; the players table stores usernames.
var teamOwnerMap = map[int]string{
	10: "hisrchel", 12: "johnny", 13: "rohan", 15: "eric", 16: "john",
	17: "william", 20: "tony", 21: "yash", 22: "vibhav", 23: "ethan",
	24: "tommy", 25: "anwar",
}

// RosterPlayer is one ESPN roster slot, ready for the players table.
type RosterPlayer struct {
	NormalizedName string
	PlayerName     string
	Owner          string // "" when the fantasy team is not in teamOwnerMap
	Position       string
	Team           string
	DraftedAt      int
}

// BuildRoster flattens the league rosters into a map keyed by normalized
// name, mirroring build_player_lookup in the old script. Entries with an
// unknown position are skipped and reported in the returned warnings.
func BuildRoster(league League) (map[string]RosterPlayer, []string) {
	draftedAt := make(map[int64]int, len(league.Picks))
	for _, p := range league.Picks {
		draftedAt[p.PlayerID] = p.OverallPickNumber
	}

	var warnings []string
	out := map[string]RosterPlayer{}
	for _, team := range league.Teams {
		owner := teamOwnerMap[team.ID]
		for _, e := range team.Entries {
			if e.FullName == "" {
				continue
			}
			position, ok := positionMap[e.PositionID]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("skipping %q: unknown position id %d", e.FullName, e.PositionID))
				continue
			}
			proTeam, ok := proTeamMap[e.ProTeamID]
			if !ok {
				proTeam = "FA"
				warnings = append(warnings, fmt.Sprintf("unknown pro team id %d for %q, using FA", e.ProTeamID, e.FullName))
			}
			norm := players.NormalizeName(e.FullName)
			out[norm] = RosterPlayer{
				NormalizedName: norm,
				PlayerName:     e.FullName,
				Owner:          owner,
				Position:       position,
				Team:           proTeam,
				DraftedAt:      draftedAt[e.PlayerID],
			}
		}
	}
	return out, warnings
}
