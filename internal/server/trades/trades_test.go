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
