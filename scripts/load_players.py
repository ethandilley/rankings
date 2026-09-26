import argparse
import os
import re
import sys

import psycopg2
import requests

PRO_TEAM_MAP = {
    0: "FA",
    1: "ATL",
    2: "BUF",
    3: "CHI",
    4: "CIN",
    5: "CLE",
    6: "DAL",
    7: "DEN",
    8: "DET",
    9: "GB",
    10: "TEN",
    11: "IND",
    12: "KC",
    13: "LV",
    14: "LAR",
    15: "MIA",
    16: "MIN",
    17: "NE",
    18: "NO",
    19: "NYG",
    20: "NYJ",
    21: "PHI",
    22: "ARI",
    23: "PIT",
    24: "LAC",
    25: "SF",
    26: "SEA",
    27: "TB",
    28: "WSH",
    29: "CAR",
    30: "JAX",
    33: "BAL",
    34: "HOU",
}


POSITION_MAP = {
    1: "QB",
    2: "RB",
    3: "WR",
    4: "TE",
}


def normalize_name(name: str) -> str:
    """
    Normalize player names so ESPN and DB names can be matched.

    Examples:

        D.J. Moore -> dj moore
        DJ Moore   -> dj moore
        George Kittle Jr. -> george kittle
    """

    name = name.lower()

    name = name.replace(".", "")
    name = name.replace("'", "")
    name = name.replace("-", " ")

    # Remove common suffixes.
    name = re.sub(r"\b(jr|sr|ii|iii|iv|v)\b", "", name)

    # Collapse whitespace.
    name = re.sub(r"\s+", " ", name).strip()

    return name


def fetch_league_rosters(
    league_id: str,
    season: str,
    espn_s2: str | None,
    swid: str | None,
) -> dict:
    """Fetch fantasy teams and rosters from ESPN."""

    url = (
        f"https://lm-api-reads.fantasy.espn.com"
        f"/apis/v3/games/ffl/seasons/{season}"
        f"/segments/0/leagues/{league_id}"
    )

    params = {
        "view": [
            "mSettings",
            "mRoster",
            "mTeam",
            "modular",
            "mNav",
            "mDraftDetail",
        ],
    }

    cookies = {}

    if espn_s2:
        cookies["espn_s2"] = espn_s2.strip()

    if swid:
        cookies["SWID"] = swid

    headers = {
        "Accept": "application/json",
        "Origin": "https://fantasy.espn.com",
        "Referer": "https://fantasy.espn.com/",
        "X-Fantasy-Platform": "espn-fantasy-web",
        "X-Fantasy-Source": "kona",
        "User-Agent": (
            "Mozilla/5.0 (X11; Linux x86_64; rv:154.0) Gecko/20100101 Firefox/154.0"
        ),
    }

    resp = requests.get(
        url,
        params=params,
        cookies=cookies,
        headers=headers,
        timeout=30,
        allow_redirects=True,
    )

    resp.raise_for_status()
    return resp.json()


def build_team_name_lookup() -> dict:
    team_to_owner = {
        "team10": "Hisrchel Nambiar",
        "team12": "Johnny Meshramkar",
        "team13": "Rohan Thandu",
        "team15": "Eric Ming",
        "team16": "John Webster",
        "team17": "William Beard",
        "team20": "Tony Capiello",
        "team21": "Yash Patel",
        "team22": "Vibhav Kumar",
        "team23": "Ethan Stone",
        "team24": "Tommy Zaffiro",
        "team25": "Anwar Adous",
    }

    return team_to_owner


def build_player_lookup(data: dict, team_names: dict) -> dict:

    draft_picks = {}
    for pick in data.get("draftDetail", {}).get("picks", []):
        player_id = pick.get("playerId")
        overall_pick = pick.get("overallPickNumber")
        if player_id is None or overall_pick is None:
            continue
        draft_picks[int(player_id)] = int(overall_pick)

    players = {}
    for fantasy_team in data.get("teams", []):
        # ESPN gives us an integer ID, e.g. 10.
        # Our lookup uses keys like "team10".
        team_id = fantasy_team["id"]
        owner = team_names.get(f"team{team_id}")

        roster = fantasy_team.get("roster", {})
        for entry in roster.get("entries", []):
            player = entry.get("playerPoolEntry", {}).get("player", {})
            player_name = player.get("fullName")

            player_id = entry.get("playerId")

            if player_id is None:
                player_id = player.get("id")

            position_id = player.get("defaultPositionId")

            position = POSITION_MAP.get(
                position_id,
                "UNKNOWN",
            )

            if not player_name or position == "UNKNOWN":
                continue

            pro_team_id = player.get("proTeamId")
            team = PRO_TEAM_MAP.get(pro_team_id)

            drafted_at = (
                draft_picks.get(int(player_id)) if player_id is not None else None
            )

            if drafted_at is None:
                drafted_at = 0

            normalized_name = normalize_name(player_name)

            players[normalized_name] = {
                "player_name": player_name,
                "owner": owner,
                "position": position,
                "team": team,
                "drafted_at": drafted_at,
            }

    return players


# ---------------------------------------------------------------------
# Database synchronization
# ---------------------------------------------------------------------


def sync_to_database(
    database_url: str,
    espn_players: dict,
    dry_run: bool = False,
):
    """
    Make the players table match the ESPN rosters.
    """

    if psycopg2 is None:
        raise SystemExit(
            "psycopg2 is required for DB writes:\n"
            "pip install psycopg2-binary --break-system-packages"
        )

    conn = psycopg2.connect(database_url)
    conn.autocommit = False

    try:
        with conn.cursor() as cur:
            # ---------------------------------------------------------
            # Get current DB state
            # ---------------------------------------------------------

            cur.execute(
                """
                SELECT
                    id,
                    owner,
                    player_name,
                    position,
                    team,
                    drafted_at
                FROM players;
                """
            )

            db_rows = cur.fetchall()

            db_players = {
                normalize_name(row[2]): {
                    "id": row[0],
                    "owner": row[1],
                    "player_name": row[2],
                    "position": row[3],
                    "team": row[4],
                    "drafted_at": row[5],
                }
                for row in db_rows
            }

            inserted = 0
            updated = 0
            deleted = 0
            unchanged = 0

            # ---------------------------------------------------------
            # INSERT / UPDATE
            # ---------------------------------------------------------

            for normalized_name, espn_player in espn_players.items():
                db_player = db_players.get(normalized_name)

                # =====================================================
                # PLAYER DOES NOT EXIST
                # =====================================================

                if db_player is None:
                    print(
                        f"INSERT  "
                        f"{espn_player['player_name']:<25} "
                        f"{espn_player['position']:<5} "
                        f"{espn_player['team']:<4} "
                        f"-> {espn_player['owner']}"
                    )

                    if not dry_run:
                        cur.execute(
                            """
                            INSERT INTO players (
                                owner,
                                player_name,
                                position,
                                team,
                                drafted_at
                            )
                            VALUES (
                                %s,
                                %s,
                                %s,
                                %s,
                                %s
                            );
                            """,
                            (
                                espn_player["owner"],
                                espn_player["player_name"],
                                espn_player["position"],
                                espn_player["team"],
                                espn_player["drafted_at"],
                            ),
                        )

                    inserted += 1

                    continue

                # =====================================================
                # PLAYER EXISTS
                # =====================================================

                changes = []

                if db_player["owner"] != espn_player["owner"]:
                    changes.append(
                        f"owner: {db_player['owner']} -> {espn_player['owner']}"
                    )

                if db_player["position"] != espn_player["position"]:
                    changes.append(
                        f"position: "
                        f"{db_player['position']} "
                        f"-> "
                        f"{espn_player['position']}"
                    )

                if db_player["team"] != espn_player["team"]:
                    changes.append(
                        f"team: {db_player['team']} -> {espn_player['team']}"
                    )

                if db_player["drafted_at"] != espn_player["drafted_at"]:
                    changes.append(
                        f"drafted_at: "
                        f"{db_player['drafted_at']} "
                        f"-> "
                        f"{espn_player['drafted_at']}"
                    )

                if not changes:
                    unchanged += 1
                    continue

                # -----------------------------------------------------
                # Player changed
                # -----------------------------------------------------

                print(f"UPDATE  {db_player['player_name']}")

                for change in changes:
                    print(f"         {change}")

                if not dry_run:
                    cur.execute(
                        """
                        UPDATE players
                        SET
                            owner = %s,
                            player_name = %s,
                            position = %s,
                            team = %s,
                            drafted_at = %s
                        WHERE id = %s;
                        """,
                        (
                            espn_player["owner"],
                            espn_player["player_name"],
                            espn_player["position"],
                            espn_player["team"],
                            espn_player["drafted_at"],
                            db_player["id"],
                        ),
                    )

                updated += 1

            # ---------------------------------------------------------
            # DELETE players that are no longer on ESPN
            # ---------------------------------------------------------

            espn_names = set(espn_players.keys())

            for normalized_name, db_player in db_players.items():
                if normalized_name in espn_names:
                    continue

                print(f"DELETE  {db_player['player_name']:<25} (no longer on ESPN)")

                if not dry_run:
                    cur.execute(
                        """
                        DELETE FROM players
                        WHERE id = %s;
                        """,
                        (db_player["id"],),
                    )

                deleted += 1

            # ---------------------------------------------------------
            # Summary
            # ---------------------------------------------------------

            print()
            print("Database sync summary:")
            print(f"  ESPN players : {len(espn_players)}")
            print(f"  Inserted     : {inserted}")
            print(f"  Updated      : {updated}")
            print(f"  Unchanged    : {unchanged}")
            print(f"  Deleted      : {deleted}")

        # -------------------------------------------------------------
        # Commit / rollback
        # -------------------------------------------------------------

        if dry_run:
            conn.rollback()
            print("\n(dry run — no changes committed)")
        else:
            conn.commit()
            print("\nChanges committed.")

    except Exception:
        conn.rollback()
        raise

    finally:
        conn.close()


def print_out(team_names, espn_players):
    for team_id, team_name in team_names.items():
        print(f"== {team_name} ==")
        for player in espn_players.values():
            if player["owner"] == team_name:
                print(
                    f"   {player['player_name']:<25} "
                    f"{player['position']:<5} "
                    f"{player['team']} "
                    f"{player['drafted_at']}"
                )
        print()


# ---------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(
        description="Sync ESPN fantasy rosters into the players table."
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Show changes without modifying the database.",
    )
    args = parser.parse_args()
    league_id = os.environ.get(
        "ESPN_LEAGUE_ID",
        "260889507",
    )
    season = os.environ.get(
        "ESPN_SEASON",
        "2026",
    )
    espn_s2 = os.environ.get("ESPN_S2")
    espn_swid = os.environ.get("ESPN_SWID")
    database_url = os.environ.get("DB_URL")

    if not database_url and not args.dry_run:
        sys.exit("Set DB_URL, or use --dry-run.")

    data = fetch_league_rosters(
        league_id,
        season,
        espn_s2,
        espn_swid,
    )
    team_names = build_team_name_lookup()
    espn_players = build_player_lookup(data, team_names)

    if args.dry_run or not database_url:
        print_out(team_names, espn_players)
        return

    sync_to_database(
        database_url,
        espn_players,
        dry_run=args.dry_run,
    )


if __name__ == "__main__":
    main()
