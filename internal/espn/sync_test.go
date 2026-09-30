package espn

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlanSync(t *testing.T) {
	same := map[string]DBPlayer{
		"lamar jackson": {ID: 1, Owner: "ethan", PlayerName: "Lamar Jackson", Position: "QB", Team: "BAL", DraftedAt: 5},
	}
	tests := []struct {
		name     string
		current  map[string]DBPlayer
		espn     map[string]RosterPlayer
		wantIns  []RosterPlayer
		wantUpd  []Update
		wantDel  []DBPlayer
		wantUnch int
	}{
		{
			name:     "everything matches",
			current:  same,
			espn:     map[string]RosterPlayer{"lamar jackson": {NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 5}},
			wantUnch: 1,
		},
		{
			name:    "player added to a roster is an insert",
			current: same,
			espn: map[string]RosterPlayer{
				"lamar jackson":  {NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 5},
				"tyler bass":     {NormalizedName: "tyler bass", PlayerName: "Tyler Bass", Owner: "tommy", Position: "K", Team: "BUF", DraftedAt: 0},
			},
			wantIns:  []RosterPlayer{{NormalizedName: "tyler bass", PlayerName: "Tyler Bass", Owner: "tommy", Position: "K", Team: "BUF", DraftedAt: 0}},
			wantUnch: 1,
		},
		{
			name: "player gone from ESPN is a delete",
			current: map[string]DBPlayer{
				"lamar jackson":     {ID: 1, Owner: "ethan", PlayerName: "Lamar Jackson", Position: "QB", Team: "BAL", DraftedAt: 5},
				"cameron dicker":    {ID: 2, Owner: "ethan", PlayerName: "Cameron Dicker", Position: "K", Team: "LAC", DraftedAt: 9},
			},
			espn: map[string]RosterPlayer{
				"lamar jackson": {NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 5},
			},
			wantDel:  []DBPlayer{{ID: 2, Owner: "ethan", PlayerName: "Cameron Dicker", Position: "K", Team: "LAC", DraftedAt: 9}},
			wantUnch: 1,
		},
		{
			name:    "trade changes the owner",
			current: same,
			espn: map[string]RosterPlayer{
				"lamar jackson": {NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "tommy", Position: "QB", Team: "BAL", DraftedAt: 5},
			},
			wantUpd: []Update{{
				ID: 1, PlayerName: "Lamar Jackson", Owner: "tommy", Position: "QB", Team: "BAL", DraftedAt: 5,
				Changes: []string{"owner: ethan -> tommy"},
			}},
		},
		{
			name:    "injury changes the team and drafted_at resets for a waiver add",
			current: same,
			espn: map[string]RosterPlayer{
				"lamar jackson": {NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "FA", DraftedAt: 0},
			},
			wantUpd: []Update{{
				ID: 1, PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "FA", DraftedAt: 0,
				Changes: []string{"team: BAL -> FA", "drafted_at: 5 -> 0"},
			}},
		},
		{
			name:    "ESPN name correction updates player_name",
			current: same,
			espn: map[string]RosterPlayer{
				"lamar jackson": {NormalizedName: "lamar jackson", PlayerName: "Lamar A. Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 5},
			},
			wantUpd: []Update{{
				ID: 1, PlayerName: "Lamar A. Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 5,
				Changes: []string{"player_name: Lamar Jackson -> Lamar A. Jackson"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := PlanSync(tt.current, tt.espn)
			if !reflect.DeepEqual(plan.Inserts, tt.wantIns) {
				t.Errorf("Inserts = %v, want %v", plan.Inserts, tt.wantIns)
			}
			if !reflect.DeepEqual(plan.Updates, tt.wantUpd) {
				t.Errorf("Updates = %v, want %v", plan.Updates, tt.wantUpd)
			}
			if !reflect.DeepEqual(plan.Deletes, tt.wantDel) {
				t.Errorf("Deletes = %v, want %v", plan.Deletes, tt.wantDel)
			}
			if plan.Unchanged != tt.wantUnch {
				t.Errorf("Unchanged = %d, want %d", plan.Unchanged, tt.wantUnch)
			}
		})
	}
}

func TestBuildRoster(t *testing.T) {
	league := League{
		Teams: []Team{
			{ID: 23, Entries: []Entry{
				{PlayerID: 100, FullName: "Lamar Jackson", PositionID: 1, ProTeamID: 33},
				{PlayerID: 101, FullName: "Evan McPherson", PositionID: 5, ProTeamID: 4},
				{PlayerID: 102, FullName: "Ravens D/ST", PositionID: 16, ProTeamID: 33},
				{PlayerID: 103, FullName: "Mystery Prospect", PositionID: 99, ProTeamID: 1},
			}},
			{ID: 99, Entries: []Entry{
				{PlayerID: 104, FullName: "No Owner RB", PositionID: 2, ProTeamID: 99},
			}},
		},
		Picks: []Pick{
			{PlayerID: 100, OverallPickNumber: 1},
			{PlayerID: 102, OverallPickNumber: 60},
		},
	}

	got, warnings := BuildRoster(league)

	if len(got) != 4 {
		t.Fatalf("BuildRoster returned %d players, want 4 (unknown position skipped): %v", len(got), got)
	}
	wantLamar := RosterPlayer{NormalizedName: "lamar jackson", PlayerName: "Lamar Jackson", Owner: "ethan", Position: "QB", Team: "BAL", DraftedAt: 1}
	if !reflect.DeepEqual(got["lamar jackson"], wantLamar) {
		t.Errorf("lamar jackson = %v, want %v", got["lamar jackson"], wantLamar)
	}
	wantK := RosterPlayer{NormalizedName: "evan mcpherson", PlayerName: "Evan McPherson", Owner: "ethan", Position: "K", Team: "CIN", DraftedAt: 0}
	if !reflect.DeepEqual(got["evan mcpherson"], wantK) {
		t.Errorf("evan mcpherson = %v, want %v", got["evan mcpherson"], wantK)
	}
	wantDst := RosterPlayer{NormalizedName: "ravens d/st", PlayerName: "Ravens D/ST", Owner: "ethan", Position: "D/ST", Team: "BAL", DraftedAt: 60}
	if !reflect.DeepEqual(got["ravens d/st"], wantDst) {
		t.Errorf("ravens d/st = %v, want %v", got["ravens d/st"], wantDst)
	}
	// Unknown fantasy team -> no owner; unknown pro team -> FA.
	wantNoOwner := RosterPlayer{NormalizedName: "no owner rb", PlayerName: "No Owner RB", Owner: "", Position: "RB", Team: "FA", DraftedAt: 0}
	if !reflect.DeepEqual(got["no owner rb"], wantNoOwner) {
		t.Errorf("no owner rb = %v, want %v", got["no owner rb"], wantNoOwner)
	}

	var joined string
	for _, w := range warnings {
		joined += w + "\n"
	}
	if !strings.Contains(joined, "unknown position id 99") {
		t.Errorf("warnings missing unknown-position note: %v", warnings)
	}
	if !strings.Contains(joined, "unknown pro team id 99") {
		t.Errorf("warnings missing unknown-pro-team note: %v", warnings)
	}
}
