package blaze

import (
	"encoding/hex"
	"strings"
	"testing"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
)

// RATINGSHOW2: the 'Skill' reply with the shown ratings in the names matches its golden bytes (tdf_golden.py's
// ratingshow2_getleaderboard_reply): RatingMark after the name of a row whose rating players see, none after the
// other's.
func TestRatingShow2ReplyMatchesGolden(t *testing.T) {
	g := golden.Load(t)
	rows, n := (&Server{ShowRating: true, LeaderboardMinGames: LeaderboardMinGames}).markRatings([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 13, Wins: 9, Shaper: 1001, Skill: 1688,
			Rating: 1688, RatedGames: 13},
		{PersonaID: store.FirstPersonaID, Name: "player", Games: 3, Wins: 2, Skill: 1500, Rating: 1500, RatedGames: 3}})
	if n != 1 || rows[0].Name != "second<dgr r='1688'/>" || rows[1].Name != "player" {
		t.Fatalf("markRatings: %d, %+v", n, rows)
	}
	if got, want := hex.EncodeToString(LeaderboardReply(rows, 1)), g.WriterCase(t, "ratingshow2_getleaderboard_reply").Hex; got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// The marks follow ShownRating's rule in both rankings: show_rating on and at least the Leaderboard's rated games;
// only the 'Skill' reply carries them (the client reads the names of 'SkillDaily' never); the rows themselves are
// copies (the store's rows keep their names).
func TestLeaderboardRatingMarks(t *testing.T) {
	for _, c := range []struct {
		name          string
		ranking, show bool
		want          []store.LeaderRow
		line          string
	}{
		{"wins ranking, shown", false, true, []store.LeaderRow{
			{PersonaID: store.FirstPersonaID, Name: "player", Games: 99, Wins: 99, Skill: 99},
			{PersonaID: store.FirstPersonaID + 1, Name: "second" + RatingMark(1688), Games: 99, Wins: 99, Shaper: 1001, Skill: 99}},
			"leaderboard \"Skill\": 2 row(s) from rank 1, 1 with the rating shown\n"},
		{"wins ranking, not shown", false, false, []store.LeaderRow{
			{PersonaID: store.FirstPersonaID, Name: "player", Games: 99, Wins: 99, Skill: 99},
			{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 99, Wins: 99, Shaper: 1001, Skill: 99}},
			"leaderboard \"Skill\": 2 row(s) from rank 1\n"},
		{"rating ranking, not shown", true, false, []store.LeaderRow{
			{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 13, Wins: 9, Shaper: 1001, Skill: 1688}},
			"leaderboard \"Skill\": 1 row(s) from rank 1 (by rating)\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ps := newPipeSessionWith(t, func(s *Server) {
				s.RatingLeaderboard, s.ShowRating = c.ranking, c.show
				seedRating(t, s, "player@example.com", store.Rating{Value: 1711.6, RD: 90, Vol: 0.06, Games: 3, Wins: 2}, nil, 0)
				seedRating(t, s, "second@example.com", store.Rating{Value: 1688.2, RD: 80, Vol: 0.06, Games: 13, Wins: 9}, nil, 1001)
			})
			ps.loginAs("player@example.com", 1)
			ps.send(ComponentStats, CmdStatsGetLeaderboard, 10, ps.request("getleaderboard_request"))
			ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 10, LeaderboardReply(c.want, 1))
			ps.send(ComponentStats, CmdStatsGetLeaderboard, 11, ps.request("getleaderboard_daily_request"))
			daily := append([]store.LeaderRow(nil), c.want...)
			for i := range daily {
				daily[i].Name, _, _ = strings.Cut(daily[i].Name, "<dgr")
			}
			ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 11, LeaderboardReply(daily, 1))
			if log := ps.close(); !strings.Contains(log, c.line) {
				t.Errorf("log lacks %q:\n%s", c.line, log)
			}
		})
	}
}
