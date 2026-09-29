package rankings

import (
	"testing"

	"github.com/ethandilley/rankings/internal/db"
)

func TestParsePositionFilter(t *testing.T) {
	tests := []struct {
		raw      string
		wantKind positionFilterKind
		wantVal  string
	}{
		{raw: "", wantKind: filterNone, wantVal: ""},
		{raw: "FLEX", wantKind: filterFlex, wantVal: ""},
		{raw: "flex", wantKind: filterFlex, wantVal: ""},
		{raw: "Flex", wantKind: filterFlex, wantVal: ""},
		{raw: "RB", wantKind: filterExact, wantVal: "RB"},
		{raw: "rb", wantKind: filterExact, wantVal: "RB"},
		{raw: "D/ST", wantKind: filterExact, wantVal: "D/ST"},
		{raw: "d/st", wantKind: filterExact, wantVal: "D/ST"},
		{raw: "QB", wantKind: filterExact, wantVal: "QB"},
	}

	for _, tt := range tests {
		t.Run("raw="+tt.raw, func(t *testing.T) {
			kind, value, err := parsePositionFilter(tt.raw)
			if err != nil {
				t.Fatalf("parsePositionFilter(%q) unexpected error: %v", tt.raw, err)
			}
			if kind != tt.wantKind {
				t.Errorf("parsePositionFilter(%q) kind = %v, want %v", tt.raw, kind, tt.wantKind)
			}
			if value != tt.wantVal {
				t.Errorf("parsePositionFilter(%q) value = %q, want %q", tt.raw, value, tt.wantVal)
			}
		})
	}
}

func row(id int64, name, pos, team string, rank int32) db.ListRankingsByOwnerForUpdateRow {
	return db.ListRankingsByOwnerForUpdateRow{
		Owner: "ethan", PlayerID: id, PlayerName: name,
		Position: pos, Team: team, Rank: rank,
	}
}

func TestToPlayerRankingsPositional(t *testing.T) {
	// A mixed board. The Rank fields are deliberately left at 0 (stale/ignored)
	// to prove overall_rank is derived positionally (i+1), not from row.Rank.
	rows := []db.ListRankingsByOwnerForUpdateRow{
		row(1, "Aaron Rodgers", "QB", "GB", 0),
		row(2, "Josh Allen", "QB", "BUF", 0),
		row(3, "Jahmyr Gibbs", "RB", "DET", 0),
		row(4, "Ja'Marr Chase", "WR", "KC", 0),
		row(5, "Bijan Robinson", "RB", "ATL", 0),
		row(6, "George Kittle", "TE", "SF", 0),
		row(7, "Jonathan Taylor", "RB", "IND", 0),
	}

	want := []struct {
		overall, positional int
	}{
		{1, 1}, // QB1
		{2, 2}, // QB2
		{3, 1}, // RB1
		{4, 1}, // WR1
		{5, 2}, // RB2
		{6, 1}, // TE1
		{7, 3}, // RB3
	}

	got := toPlayerRankings(rows)
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].OverallRank != w.overall {
			t.Errorf("row %d overall_rank = %d, want %d", i, got[i].OverallRank, w.overall)
		}
		if got[i].PositionalRank != w.positional {
			t.Errorf("row %d positional_rank = %d, want %d", i, got[i].PositionalRank, w.positional)
		}
	}
}

func TestToPlayerRankingsSinglePosition(t *testing.T) {
	// All same position: positional_rank must track overall_rank exactly.
	rows := []db.ListRankingsByOwnerForUpdateRow{
		row(1, "Jahmyr Gibbs", "RB", "DET", 0),
		row(2, "Bijan Robinson", "RB", "ATL", 0),
		row(3, "Jonathan Taylor", "RB", "IND", 0),
	}
	want := []int{1, 2, 3}

	got := toPlayerRankings(rows)
	for i, w := range want {
		if got[i].PositionalRank != w {
			t.Errorf("row %d positional_rank = %d, want %d", i, got[i].PositionalRank, w)
		}
		if got[i].OverallRank != w {
			t.Errorf("row %d overall_rank = %d, want %d", i, got[i].OverallRank, w)
		}
	}
}

func TestToPlayerRankingsEmpty(t *testing.T) {
	got := toPlayerRankings(nil)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
