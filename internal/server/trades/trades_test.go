package trades

import (
	"testing"
)

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: defaultLimit},
		{raw: "1", want: 1},
		{raw: "10", want: 10},
		{raw: "25", want: 25},
		{raw: "100", want: 100},
		{raw: "0", wantErr: true},
		{raw: "-5", wantErr: true},
		{raw: "101", wantErr: true},
		{raw: "abc", wantErr: true},
		{raw: "2.5", wantErr: true},
	}

	for _, tt := range tests {
		t.Run("raw="+tt.raw, func(t *testing.T) {
			got, err := parseLimit(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLimit(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseLimit(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestOwnerPairs(t *testing.T) {
	boards := map[string]map[int64]float64{
		"charlie": {1: 0.1},
		"alice":   {1: 0.2},
		"bob":     {1: 0.3},
	}

	t.Run("league-wide", func(t *testing.T) {
		pairs := ownerPairs("", boards)
		want := []ownerPair{{a: "alice", b: "bob"}, {a: "alice", b: "charlie"}, {a: "bob", b: "charlie"}}
		if len(pairs) != len(want) {
			t.Fatalf("len = %d, want %d", len(pairs), len(want))
		}
		for i := range want {
			if pairs[i] != want[i] {
				t.Errorf("pair %d = %+v, want %+v", i, pairs[i], want[i])
			}
		}
	})

	t.Run("requested owner is always side A", func(t *testing.T) {
		pairs := ownerPairs("bob", boards)
		want := []ownerPair{{a: "bob", b: "alice"}, {a: "bob", b: "charlie"}}
		if len(pairs) != len(want) {
			t.Fatalf("len = %d, want %d", len(pairs), len(want))
		}
		for i := range want {
			if pairs[i] != want[i] {
				t.Errorf("pair %d = %+v, want %+v", i, pairs[i], want[i])
			}
		}
	})
}

func TestBestDesiredOtherSide(t *testing.T) {
	own := map[int64]float64{1: 0.5, 2: 0.6, 3: 0.5, 4: 0.2, 5: 0.3}
	other := map[int64]float64{1: 0.4, 2: 0.2, 3: 0.3, 5: 0.3}
	owned := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: true}

	t.Run("largest gap wins", func(t *testing.T) {
		// p1 gap 0.1, p2 gap 0.4, p3 gap 0.2, p4 skipped (other never ranked it),
		// p5 gap 0 (tie not desired).
		id, ok := bestDesiredOtherSide(owned, own, other)
		if !ok {
			t.Fatal("expected a candidate")
		}
		if id != 2 {
			t.Errorf("id = %d, want 2", id)
		}
	})

	t.Run("ties break on lower player id", func(t *testing.T) {
		ownTie := map[int64]float64{3: 0.5, 5: 0.3, 7: 0.3}
		otherTie := map[int64]float64{3: 0.3, 5: 0.1, 7: 0.1}
		ownedTie := map[int64]bool{3: true, 5: true, 7: true}
		// p3 gap 0.2, p5 gap 0.2, p7 gap 0.2 -> p3 wins.
		id, ok := bestDesiredOtherSide(ownedTie, ownTie, otherTie)
		if !ok {
			t.Fatal("expected a candidate")
		}
		if id != 3 {
			t.Errorf("id = %d, want 3", id)
		}
	})

	t.Run("zero gap is not desired", func(t *testing.T) {
		id, ok := bestDesiredOtherSide(map[int64]bool{5: true}, own, other)
		if ok {
			t.Errorf("expected no candidate, got %d", id)
		}
	})

	t.Run("unranked by other side is skipped", func(t *testing.T) {
		ownedOnly4 := map[int64]bool{4: true}
		id, ok := bestDesiredOtherSide(ownedOnly4, own, other)
		if ok {
			t.Errorf("expected no candidate, got %d", id)
		}
	})

	t.Run("empty roster", func(t *testing.T) {
		_, ok := bestDesiredOtherSide(map[int64]bool{}, own, other)
		if ok {
			t.Error("expected no candidate for empty roster")
		}
	})
}

func TestComputePairwiseTrade(t *testing.T) {
	t.Run("mutual trade", func(t *testing.T) {
		in := pairwiseInput{
			aOwner: "ethan",
			bOwner: "frank",
			// A: X(p1) at 0.40, own p2 at 0.20, rates Y(p3) at 0.15.
			// B: rates X(p1) at 0.10 (wants it), Y(p3) at 0.50, p2 at 0.25.
			aNorm:    map[int64]float64{1: 0.4, 2: 0.2, 3: 0.15},
			bNorm:    map[int64]float64{1: 0.1, 2: 0.25, 3: 0.5},
			aDrafted: map[int64]bool{1: true, 2: true},
			bDrafted: map[int64]bool{3: true},
		}
		res, ok := computePairwiseTrade(in)
		if !ok {
			t.Fatal("expected a trade")
		}
		if res.x != 1 || res.y != 3 {
			t.Errorf("x=%d y=%d, want x=1 y=3", res.x, res.y)
		}
		if !approxEqual(res.aGain, 0.25) { // A: 0.40 - 0.15
			t.Errorf("aGain = %v, want ~0.25", res.aGain)
		}
		if !approxEqual(res.bGain, 0.4) { // B: 0.50 - 0.10
			t.Errorf("bGain = %v, want ~0.40", res.bGain)
		}
		if !approxEqual(res.combined, 0.65) {
			t.Errorf("combined = %v, want ~0.65", res.combined)
		}
	})

	t.Run("no player B wants from A", func(t *testing.T) {
		in := pairwiseInput{
			aNorm:    map[int64]float64{1: 0.4, 2: 0.2, 3: 0.15},
			bNorm:    map[int64]float64{1: 0.45, 3: 0.5}, // B rates A's p1 lower
			aDrafted: map[int64]bool{1: true},
			bDrafted: map[int64]bool{3: true},
		}
		if _, ok := computePairwiseTrade(in); ok {
			t.Error("expected no trade when B wants nothing A owns")
		}
	})

	t.Run("no player A wants from B", func(t *testing.T) {
		in := pairwiseInput{
			aNorm:    map[int64]float64{1: 0.4, 3: 0.6}, // A rates B's p3 lower
			bNorm:    map[int64]float64{1: 0.1, 3: 0.5},
			aDrafted: map[int64]bool{1: true},
			bDrafted: map[int64]bool{3: true},
		}
		if _, ok := computePairwiseTrade(in); ok {
			t.Error("expected no trade when A wants nothing B owns")
		}
	})

	t.Run("A does not gain from the swap", func(t *testing.T) {
		// Both desire each other's player, but A rates Y(p3) worse than X(p1),
		// so A would not accept the trade.
		in := pairwiseInput{
			aNorm:    map[int64]float64{1: 0.1, 3: 0.4},
			bNorm:    map[int64]float64{1: 0.05, 3: 0.5},
			aDrafted: map[int64]bool{1: true},
			bDrafted: map[int64]bool{3: true},
		}
		if _, ok := computePairwiseTrade(in); ok {
			t.Error("expected no trade when A's gain is negative")
		}
	})

	t.Run("B does not gain from the swap", func(t *testing.T) {
		// B rates Y(p3), which it gives up, higher than X(p1), which it receives.
		in := pairwiseInput{
			aNorm:    map[int64]float64{1: 0.7, 3: 0.3},
			bNorm:    map[int64]float64{1: 0.5, 3: 0.1},
			aDrafted: map[int64]bool{1: true},
			bDrafted: map[int64]bool{3: true},
		}
		if _, ok := computePairwiseTrade(in); ok {
			t.Error("expected no trade when B's gain is negative")
		}
	})

	t.Run("zero gain is not a trade", func(t *testing.T) {
		in := pairwiseInput{
			aNorm:    map[int64]float64{1: 0.3, 3: 0.3}, // aGain = 0
			bNorm:    map[int64]float64{1: 0.1, 3: 0.5},
			aDrafted: map[int64]bool{1: true},
			bDrafted: map[int64]bool{3: true},
		}
		if _, ok := computePairwiseTrade(in); ok {
			t.Error("expected no trade when aGain is exactly zero")
		}
	})

	t.Run("largest-gap player chosen for X", func(t *testing.T) {
		in := pairwiseInput{
			// A owns p1 (gap 0.1) and p2 (gap 0.4): X must be p2.
			aNorm:    map[int64]float64{1: 0.5, 2: 0.6, 3: 0.3},
			bNorm:    map[int64]float64{1: 0.4, 2: 0.2, 3: 0.5},
			aDrafted: map[int64]bool{1: true, 2: true},
			bDrafted: map[int64]bool{3: true},
		}
		res, ok := computePairwiseTrade(in)
		if !ok {
			t.Fatal("expected a trade")
		}
		if res.x != 2 {
			t.Errorf("x = %d, want 2", res.x)
		}
	})

	t.Run("player not ranked by both sides is skipped", func(t *testing.T) {
		in := pairwiseInput{
			// p1: B never ranked it. p2 is the only valid X.
			aNorm:    map[int64]float64{1: 0.2, 2: 0.4, 3: 0.3},
			bNorm:    map[int64]float64{2: 0.3, 3: 0.5},
			aDrafted: map[int64]bool{1: true, 2: true},
			bDrafted: map[int64]bool{3: true},
		}
		res, ok := computePairwiseTrade(in)
		if !ok {
			t.Fatal("expected a trade")
		}
		if res.x != 2 {
			t.Errorf("x = %d, want 2", res.x)
		}
	})
}

func TestComputeContrarian(t *testing.T) {
	players := map[int64]PlayerRef{
		1: {PlayerID: 1, PlayerName: "Player One", Position: "RB", Team: "NE"},
		2: {PlayerID: 2, PlayerName: "Player Two", Position: "WR", Team: "KC"},
		3: {PlayerID: 3, PlayerName: "Player Three", Position: "TE", Team: "SF"},
		4: {PlayerID: 4, PlayerName: "Player Four", Position: "RB", Team: "DET"},
		5: {PlayerID: 5, PlayerName: "Player Five", Position: "WR", Team: "GB"},
	}

	// p1 ranked by all four, p2 by all four, p3 by three (dave absent),
	// p4 by two only (below minConsensusOwners), p5 by one only.
	boards := map[string]map[int64]float64{
		"alice": {1: 0.10, 2: 0.90, 3: 0.50, 4: 0.05, 5: 0.05},
		"bob":   {1: 0.50, 2: 0.20, 3: 0.50, 4: 0.10},
		"carol": {1: 0.60, 2: 0.25, 3: 0.50},
		"dave":  {1: 0.40, 2: 0.30},
	}

	type wantPlayer struct {
		id    int64
		owner float64
		field float64
		gap   float64
		count int
		name  string
	}
	tests := []struct {
		owner    string
		believer *wantPlayer
		skeptic  *wantPlayer
	}{
		{
			owner: "alice",
			// p1: field ex-self (0.5+0.6+0.4)/3 = 0.5, gap 0.4; p4/p5
			// excluded (ranked by <3 owners) despite huge gaps.
			believer: &wantPlayer{id: 1, owner: 0.10, field: 0.50, gap: 0.40, count: 4, name: "Player One"},
			// p2: field ex-self (0.2+0.25+0.3)/3 = 0.25, gap 0.65.
			skeptic: &wantPlayer{id: 2, owner: 0.90, field: 0.25, gap: 0.65, count: 4, name: "Player Two"},
		},
		{
			// p2: field ex-self (0.9+0.25+0.3)/3 = 0.4833, bob rates it 0.2.
			owner:    "bob",
			believer: &wantPlayer{id: 2, owner: 0.20, field: (0.9 + 0.25 + 0.3) / 3, gap: (0.9+0.25+0.3)/3 - 0.20, count: 4, name: "Player Two"},
			// p1: field ex-self (0.1+0.6+0.4)/3 = 0.3667, bob rates it 0.5.
			skeptic: &wantPlayer{id: 1, owner: 0.50, field: (0.1 + 0.6 + 0.4) / 3, gap: 0.50 - (0.1+0.6+0.4)/3, count: 4, name: "Player One"},
		},
		{
			owner:    "carol",
			believer: &wantPlayer{id: 2, owner: 0.25, field: (0.9 + 0.2 + 0.3) / 3, gap: (0.9+0.2+0.3)/3 - 0.25, count: 4, name: "Player Two"},
			skeptic:  &wantPlayer{id: 1, owner: 0.60, field: (0.1 + 0.5 + 0.4) / 3, gap: 0.60 - (0.1+0.5+0.4)/3, count: 4, name: "Player One"},
		},
		{
			owner:    "dave",
			believer: &wantPlayer{id: 2, owner: 0.30, field: (0.9 + 0.2 + 0.25) / 3, gap: (0.9+0.2+0.25)/3 - 0.30, count: 4, name: "Player Two"},
		},
	}

	got := computeContrarian(boards, players, []string{"alice", "bob", "carol", "dave"})
	if len(got) != len(tests) {
		t.Fatalf("len = %d, want %d", len(got), len(tests))
	}

	check := func(t *testing.T, owner string, p *ContrarianPlayer, want *wantPlayer, label string) {
		t.Helper()
		if want == nil {
			if p != nil {
				t.Errorf("%s %s: got %+v, want nil", owner, label, *p)
			}
			return
		}
		if p == nil {
			t.Fatalf("%s %s: nil, want player %d", owner, label, want.id)
		}
		if p.PlayerID != want.id {
			t.Errorf("%s %s id = %d, want %d", owner, label, p.PlayerID, want.id)
		}
		if p.PlayerName != want.name {
			t.Errorf("%s %s name = %q, want %q", owner, label, p.PlayerName, want.name)
		}
		if !approxEqual(p.OwnerNormalizedRank, want.owner) {
			t.Errorf("%s %s owner = %v, want %v", owner, label, p.OwnerNormalizedRank, want.owner)
		}
		if !approxEqual(p.FieldNormalizedRank, want.field) {
			t.Errorf("%s %s field = %v, want %v", owner, label, p.FieldNormalizedRank, want.field)
		}
		if !approxEqual(p.Gap, want.gap) {
			t.Errorf("%s %s gap = %v, want %v", owner, label, p.Gap, want.gap)
		}
		if p.NumOwnersRanked != want.count {
			t.Errorf("%s %s count = %d, want %d", owner, label, p.NumOwnersRanked, want.count)
		}
	}

	for i, tt := range tests {
		t.Run(tt.owner, func(t *testing.T) {
			if got[i].Owner != tt.owner {
				t.Fatalf("owner = %q, want %q", got[i].Owner, tt.owner)
			}
			check(t, tt.owner, got[i].BiggestBeliever, tt.believer, "believer")
			check(t, tt.owner, got[i].BiggestSkeptic, tt.skeptic, "skeptic")
		})
	}
}

func TestComputeContrarianTieBreaksOnLowerPlayerID(t *testing.T) {
	players := map[int64]PlayerRef{
		3: {PlayerID: 3, PlayerName: "P3", Position: "RB", Team: "A"},
		7: {PlayerID: 7, PlayerName: "P7", Position: "WR", Team: "B"},
	}
	boards := map[string]map[int64]float64{
		"alice": {3: 0.10, 7: 0.10},
		"bob":   {3: 0.50, 7: 0.50},
		"carol": {3: 0.70, 7: 0.70},
	}
	got := computeContrarian(boards, players, []string{"alice"})
	believer := got[0].BiggestBeliever
	if believer == nil {
		t.Fatal("expected a believer")
	}
	// Both gaps are 0.5; the lower player ID must win.
	if believer.PlayerID != 3 {
		t.Errorf("believer id = %d, want 3", believer.PlayerID)
	}
	if got[0].BiggestSkeptic != nil {
		t.Errorf("skeptic = %+v, want nil", *got[0].BiggestSkeptic)
	}
}

func TestComputeContrarianSelfExcludedFromField(t *testing.T) {
	players := map[int64]PlayerRef{
		1: {PlayerID: 1, PlayerName: "P1", Position: "RB", Team: "A"},
	}
	// Only alice rates p1 high; the field (bob, carol) rates it low. The
	// field average must exclude alice's own 0.1 vote, giving 0.9, not the
	// self-included (0.1+0.9+0.9)/3.
	boards := map[string]map[int64]float64{
		"alice": {1: 0.10},
		"bob":   {1: 0.90},
		"carol": {1: 0.90},
	}
	got := computeContrarian(boards, players, []string{"alice"})
	believer := got[0].BiggestBeliever
	if believer == nil {
		t.Fatal("expected a believer")
	}
	if !approxEqual(believer.FieldNormalizedRank, 0.9) {
		t.Errorf("field = %v, want 0.9 (self excluded)", believer.FieldNormalizedRank)
	}
	if !approxEqual(believer.Gap, 0.8) {
		t.Errorf("gap = %v, want 0.8", believer.Gap)
	}
}
