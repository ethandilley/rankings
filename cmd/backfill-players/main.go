// Command backfill-players is a one-off tool that assigns a players.id to
// every rankings row that has none (between migrations 0006 and 0007).
//
// It strips the trailing " (TEAM, POS)" suffix from seeded ranking names and
// matches them against players using the same normalization as
// scripts/load_players.py (see players.NormalizeName). Names with no
// matching player row get a new players row (never dropped).
//
// Usage:
//
//	DB_URL=... go run ./cmd/backfill-players            # dry run, report only
//	DB_URL=... go run ./cmd/backfill-players -apply     # write the changes
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/ethandilley/rankings/internal/server/players"
	"github.com/jackc/pgx/v5"
)

var suffixRe = regexp.MustCompile(` \(([A-Z]+), ([A-Z/]+)\)$`)

type playerRow struct {
	ID   int64
	Name string
	Pos  string
	Team string
}

type pendingRow struct {
	ID      int64
	Owner   string
	RawName string
	Rank    int32
}

type resolution struct {
	playerID  int64
	kind      string // "exact", "fuzzy", "new"
	player    playerRow
	created   bool
}

func main() {
	apply := flag.Bool("apply", false, "write changes to the database (default: dry run)")
	flag.Parse()

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Fatal("DB_URL not set")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Load the players table.
	var allPlayers []playerRow
	pr, err := conn.Query(ctx, "SELECT id, player_name, position, team FROM players ORDER BY id")
	if err != nil {
		log.Fatalf("list players: %v", err)
	}
	for pr.Next() {
		var p playerRow
		if err := pr.Scan(&p.ID, &p.Name, &p.Pos, &p.Team); err != nil {
			log.Fatalf("scan player: %v", err)
		}
		allPlayers = append(allPlayers, p)
	}
	pr.Close()

	byNorm := map[string][]playerRow{}
	for _, p := range allPlayers {
		byNorm[players.NormalizeName(p.Name)] = append(byNorm[players.NormalizeName(p.Name)], p)
	}

	// Load the rankings rows still missing a player_id.
	var pending []pendingRow
	rq, err := conn.Query(ctx, "SELECT id, owner, player_name, rank FROM rankings WHERE player_id IS NULL ORDER BY id")
	if err != nil {
		log.Fatalf("list rankings: %v", err)
	}
	for rq.Next() {
		var r pendingRow
		if err := rq.Scan(&r.ID, &r.Owner, &r.RawName, &r.Rank); err != nil {
			log.Fatalf("scan ranking: %v", err)
		}
		pending = append(pending, r)
	}
	rq.Close()

	if len(pending) == 0 {
		fmt.Println("nothing to do: every rankings row already has a player_id")
		return
	}

	// Resolve each distinct raw name once; rows sharing a name share a result.
	type nameInfo struct {
		clean    string
		team     string
		position string
	}
	distinct := map[string]nameInfo{}
	for _, r := range pending {
		if _, ok := distinct[r.RawName]; ok {
			continue
		}
		info := nameInfo{clean: players.StripTeamPosSuffix(r.RawName)}
		if m := suffixRe.FindStringSubmatch(r.RawName); m != nil {
			info.team, info.position = m[1], m[2]
		}
		distinct[r.RawName] = info
	}

	resolved := map[string]*resolution{}
	var names []string
	for n := range distinct {
		names = append(names, n)
	}
	sort.Strings(names)

	var createdCache = map[string]int64{} // norm name + team -> id of created player
	for _, raw := range names {
		info := distinct[raw]
		norm := players.NormalizeName(info.clean)
		candidates := byNorm[norm]

		switch {
		case len(candidates) == 1 && info.clean == candidates[0].Name:
			resolved[raw] = &resolution{playerID: candidates[0].ID, kind: "exact", player: candidates[0]}
		case len(candidates) == 1:
			resolved[raw] = &resolution{playerID: candidates[0].ID, kind: "fuzzy", player: candidates[0]}
		case len(candidates) > 1:
			// Disambiguate with the team from the " (TEAM, POS)" suffix.
			var byTeam []playerRow
			for _, c := range candidates {
				if strings.EqualFold(c.Team, info.team) {
					byTeam = append(byTeam, c)
				}
			}
			if len(byTeam) == 1 {
				resolved[raw] = &resolution{playerID: byTeam[0].ID, kind: "fuzzy", player: byTeam[0]}
			} else {
				resolved[raw] = &resolution{kind: "ambiguous", player: playerRow{}}
			}
		default:
			// No player row exists: create one (keyed on normalized name +
			// team so identical names in one pass share a row).
			key := norm + " | " + info.team
			if id, ok := createdCache[key]; ok {
				resolved[raw] = &resolution{playerID: id, kind: "new"}
				break
			}
			p := playerRow{Name: info.clean, Pos: info.position, Team: info.team}
			createdCache[key] = 0
			resolved[raw] = &resolution{kind: "new", player: p, created: true}
		}
	}

	// Report.
	counts := map[string]int{}
	fmt.Printf("%d rankings rows pending, %d distinct names, %d players in table\n\n",
		len(pending), len(names), len(allPlayers))
	for _, raw := range names {
		res := resolved[raw]
		n := 0
		for _, r := range pending {
			if r.RawName == raw {
				n++
			}
		}
		counts[res.kind]++
		switch res.kind {
		case "exact":
			fmt.Printf("EXACT   x%-4d %-32s -> id=%d %s (%s, %s)\n", n, raw, res.playerID, res.player.Name, res.player.Pos, res.player.Team)
		case "fuzzy":
			fmt.Printf("FUZZY   x%-4d %-32s -> id=%d %s (%s, %s)\n", n, raw, res.playerID, res.player.Name, res.player.Pos, res.player.Team)
		case "new":
			fmt.Printf("NEW     x%-4d %-32s -> create %q (%s, %s)\n", n, raw, res.player.Name, res.player.Pos, res.player.Team)
		case "ambiguous":
			fmt.Printf("AMBIGUOUS x%-4d %-32s -> candidates: %s (NOT resolved)\n", n, raw, describeCandidates(byNorm[players.NormalizeName(distinct[raw].clean)]))
		}
	}
	fmt.Printf("\nsummary: %d exact, %d fuzzy, %d new, %d ambiguous\n",
		counts["exact"], counts["fuzzy"], counts["new"], counts["ambiguous"])

	if counts["ambiguous"] > 0 {
		log.Fatal("ambiguous names remain — resolve them manually before applying")
	}

	if !*apply {
		fmt.Println("\ndry run — no changes written. Re-run with -apply to write.")
		return
	}

	// Apply inside one transaction.
	tx, err := conn.Begin(ctx)
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	q := func(sql string, args ...any) (int64, error) {
		row := tx.QueryRow(ctx, sql, args...)
		var id int64
		if err := row.Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}

	var created int
	newIDByKey := map[string]int64{}
	for _, raw := range names {
		res := resolved[raw]
		if !res.created {
			continue
		}
		id, err := q("INSERT INTO players (player_name, position, team, drafted_at) VALUES ($1, $2, $3, 0) RETURNING id",
			res.player.Name, res.player.Pos, res.player.Team)
		if err != nil {
			log.Fatalf("insert player %q: %v", res.player.Name, err)
		}
		res.playerID = id
		key := players.NormalizeName(distinct[raw].clean) + " | " + distinct[raw].team
		newIDByKey[key] = id
		created++
	}
	// Deduped "new" entries (same normalized name + team, different raw
	// spelling) share the id of the row created for the first one.
	for _, raw := range names {
		res := resolved[raw]
		if res.kind != "new" || res.created || res.playerID != 0 {
			continue
		}
		key := players.NormalizeName(distinct[raw].clean) + " | " + distinct[raw].team
		res.playerID = newIDByKey[key]
	}

	var updated int
	for _, r := range pending {
		res := resolved[r.RawName]
		if _, err := tx.Exec(ctx, "UPDATE rankings SET player_id = $2 WHERE id = $1", r.ID, res.playerID); err != nil {
			log.Fatalf("update ranking %d: %v", r.ID, err)
		}
		updated++
	}

	if err := tx.Commit(ctx); err != nil {
		log.Fatalf("commit: %v", err)
	}

	var remaining int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM rankings WHERE player_id IS NULL").Scan(&remaining); err != nil {
		log.Fatalf("verify: %v", err)
	}
	fmt.Printf("applied: %d rows updated, %d players created, %d rows still NULL\n", updated, created, remaining)
	if remaining > 0 {
		log.Fatal("rows still missing player_id — do not run migration 0007")
	}
}

func describeCandidates(cs []playerRow) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("id=%d %s (%s, %s)", c.ID, c.Name, c.Pos, c.Team))
	}
	return strings.Join(parts, "; ")
}
