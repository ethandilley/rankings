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

	got := toPlayerRankings(rows, nil)
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
		if got[i].Tier != 1 {
			t.Errorf("row %d tier = %d, want 1 (no breaks)", i, got[i].Tier)
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

	got := toPlayerRankings(rows, nil)
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
	got := toPlayerRankings(nil, nil)
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestToPlayerRankingsTiers(t *testing.T) {
	rows := []db.ListRankingsByOwnerForUpdateRow{
		row(1, "A", "QB", "GB", 0),
		row(2, "B", "QB", "BUF", 0),
		row(3, "C", "RB", "DET", 0),
		row(4, "D", "RB", "ATL", 0),
		row(5, "E", "WR", "KC", 0),
	}
	// A break before rank 3: overall ranks 1-2 are tier 1, 3-5 are tier 2.
	got := toPlayerRankings(rows, []int{3})
	wantTiers := []int{1, 1, 2, 2, 2}
	for i, w := range wantTiers {
		if got[i].Tier != w {
			t.Errorf("row %d tier = %d, want %d", i, got[i].Tier, w)
		}
	}
}

func TestTierForRank(t *testing.T) {
	tests := []struct {
		name   string
		rank   int
		breaks []int
		want   int
	}{
		{name: "no breaks is always tier 1", rank: 5, breaks: nil, want: 1},
		{name: "empty breaks slice is always tier 1", rank: 1, breaks: []int{}, want: 1},
		{name: "rank just before a break stays in prior tier", rank: 4, breaks: []int{5}, want: 1},
		{name: "rank at a break steps into the new tier", rank: 5, breaks: []int{5}, want: 2},
		{name: "rank well past a break", rank: 10, breaks: []int{5}, want: 2},
		{name: "two breaks, between them", rank: 9, breaks: []int{5, 13}, want: 2},
		{name: "two breaks, at the second", rank: 13, breaks: []int{5, 13}, want: 3},
		{name: "two breaks, past both", rank: 30, breaks: []int{5, 13}, want: 3},
		{name: "break at rank 1 makes everyone tier 2", rank: 1, breaks: []int{1}, want: 2},
		{name: "three breaks, in the middle", rank: 20, breaks: []int{5, 13, 21}, want: 3},
		{name: "three breaks, at the last", rank: 21, breaks: []int{5, 13, 21}, want: 4},
		{name: "break beyond rank is ignored", rank: 3, breaks: []int{10, 20}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tierForRank(tt.rank, tt.breaks); got != tt.want {
				t.Errorf("tierForRank(%d, %v) = %d, want %d", tt.rank, tt.breaks, got, tt.want)
			}
		})
	}
}

func TestValidateTierBreaks(t *testing.T) {
	tests := []struct {
		name    string
		breaks  []TierBreak
		wantErr string
	}{
		{name: "empty is valid", breaks: nil, wantErr: ""},
		{name: "single break is valid", breaks: []TierBreak{{BeforeRank: 5, Label: "Elite"}}, wantErr: ""},
		{name: "empty label is valid", breaks: []TierBreak{{BeforeRank: 13, Label: ""}}, wantErr: ""},
		{name: "rank zero is invalid", breaks: []TierBreak{{BeforeRank: 0}}, wantErr: "before_rank must be >= 1 (got 0)"},
		{name: "negative rank is invalid", breaks: []TierBreak{{BeforeRank: -2}}, wantErr: "before_rank must be >= 1 (got -2)"},
		{name: "duplicate ranks are rejected", breaks: []TierBreak{{BeforeRank: 5}, {BeforeRank: 13}, {BeforeRank: 5}}, wantErr: "a tier break already exists at rank 5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTierBreaks(tt.breaks)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateTierBreaks(%v) unexpected error: %v", tt.breaks, err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("validateTierBreaks(%v) = %v, want error %q", tt.breaks, err, tt.wantErr)
			}
		})
	}
}
