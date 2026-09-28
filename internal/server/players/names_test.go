package players

import "testing"

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"D.J. Moore", "dj moore"},
		{"DJ Moore", "dj moore"},
		{"George Kittle Jr.", "george kittle"},
		{"Aaron Jones Sr.", "aaron jones"},
		{"Brian Robinson Jr.", "brian robinson"},
		{"Terrell Owens II", "terrell owens"},
		{"Amon-Ra St. Brown", "amon ra st brown"},
		{"Ja'Kobi Lane", "jakobi lane"},
		{"De'Von Achane", "devon achane"},
		{"CeeDee Lamb", "ceedee lamb"},
		{"Kaiir El-Amin", "kaiir el amin"},
		{"  spaced   out  ", "spaced out"},
		{"Vince Young", "vince young"},
		{"Christian Watson", "christian watson"},
		{"D.J. Moore Jr.", "dj moore"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeName(tt.in); got != tt.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripTeamPosSuffix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Jahmyr Gibbs (DET, RB)", "Jahmyr Gibbs"},
		{"Rams D/ST (LAR, D/ST)", "Rams D/ST"},
		{"Brandon Aubrey (DAL, K)", "Brandon Aubrey"},
		{"Chris Olave (NO, WR)", "Chris Olave"},
		{"Kyler Murray (MIN, QB)", "Kyler Murray"},
		{"Jahmyr Gibbs", "Jahmyr Gibbs"},
		{"Jahmyr Gibbs (DET", "Jahmyr Gibbs (DET"},
		{"(DET, RB)", "(DET, RB)"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := StripTeamPosSuffix(tt.in); got != tt.want {
				t.Errorf("StripTeamPosSuffix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStripThenNormalize(t *testing.T) {
	// The backfill path: a seeded ranking name must normalize to the same
	// key as the bare player name.
	seeded := "Amon-Ra St. Brown (DET, WR)"
	if got := NormalizeName(StripTeamPosSuffix(seeded)); got != NormalizeName("Amon-Ra St. Brown") {
		t.Errorf("round trip mismatch: %q", got)
	}
}
