package players

import (
	"regexp"
	"strings"
)

var (
	generationSuffixRe = regexp.MustCompile(`\b(jr|sr|ii|iii|iv|v)\b`)
	whitespaceRe       = regexp.MustCompile(`\s+`)
	teamPosSuffixRe    = regexp.MustCompile(` \([A-Z]+, [A-Z/]+\)$`)
)

// NormalizeName is a Go port of normalize_name() in scripts/load_players.py:
// lowercase, drop "." and "'", turn "-" into a space, strip generation
// suffixes (jr/sr/ii/iii/iv/v), collapse whitespace, trim.
func NormalizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, ".", "")
	name = strings.ReplaceAll(name, "'", "")
	name = strings.ReplaceAll(name, "-", " ")
	name = generationSuffixRe.ReplaceAllString(name, "")
	name = whitespaceRe.ReplaceAllString(name, " ")
	return strings.TrimSpace(name)
}

// StripTeamPosSuffix removes the trailing " (TEAM, POS)" marker that seeded
// ranking names carry, e.g. "Jahmyr Gibbs (DET, RB)" -> "Jahmyr Gibbs".
func StripTeamPosSuffix(name string) string {
	return teamPosSuffixRe.ReplaceAllString(name, "")
}
