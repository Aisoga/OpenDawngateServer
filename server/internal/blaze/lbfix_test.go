package blaze

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// LBFIX: the replies to the requests the public server left unanswered match their golden bytes (tdf_golden.py,
// written from the member tables; the member-table check is in TestRepliesMatchClientMemberTables).
func TestLBFIXRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range []struct {
		name string
		got  []byte
	}{
		{"getleaderboard_reply", LeaderboardReply([]store.LeaderRow{
			{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 4, Wins: 3, Shaper: 1001},
			{PersonaID: store.FirstPersonaID, Name: "player", Games: 5, Wins: 3}}, 1)},
		{"getleaderboard_empty_reply", LeaderboardReply(nil, 1)},
		{"filepetition_reply", FilePetitionReply("report-1")},
		{"selectcategoryupdates_reply", SelectCategoryUpdatesReply(0)},
	} {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// seedGames gives a pipe session's account its profile totals, its shapers and today's games.
func seedGames(t *testing.T, s *Server, email string, played, wins string, shapers map[uint32]store.GamesWon,
	today map[uint32]store.ShaperDay) {
	t.Helper()
	a, _, err := s.Store.Login(email, time.Unix(testTime, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Update(a.UserID, func(a *store.Account) {
		a.Stats["PlayerStats"] = map[string]string{"PLAYED": played, "WINS": wins}
		a.History.Shapers = shapers
		if today != nil {
			a.History.Days = []store.HistoryDay{{Day: time.Unix(testTime, 0).UTC().Format("2006-01-02"), Shapers: today}}
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// The Leaderboard as the client opens it: getLeaderboard 'Skill' (the request as the public server's log showed it)
// gets the stored accounts' ranking (the golden reply: second ahead of player with as many wins in fewer games), then
// 'SkillDaily' the ranking without today's games (second's three wins today put player first), an unknown name and a
// start past the end no rows; the player report gets its GUID and the logout's selectCategoryUpdates its view id; a
// request without a handler an error reply (golden frame), after which the connection goes on.
func TestLeaderboardSession(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		s.LeaderboardMinGames = 1
		seedGames(t, s, "player@example.com", "5", "3", map[uint32]store.GamesWon{1002: {Games: 1}}, nil)
		seedGames(t, s, "second@example.com", "4", "3", map[uint32]store.GamesWon{1001: {Games: 4, Wins: 3}},
			map[uint32]store.ShaperDay{1001: {Games: 3, Wins: 3}})
		seedGames(t, s, "third@example.com", "0", "0", nil, nil) // no games: not ranked
	})
	ps.loginAs("player@example.com", 1)
	// 'Skill': the golden reply but for player's shaper (1002 here)
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 10, ps.request("getleaderboard_request"))
	want := LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 4, Wins: 3, Shaper: 1001},
		{PersonaID: store.FirstPersonaID, Name: "player", Games: 5, Wins: 3, Shaper: 1002}}, 1)
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 10, want)
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 11, ps.request("getleaderboard_daily_request"))
	want = LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID, Name: "player", Games: 5, Wins: 3, Shaper: 1002},
		{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 1, Wins: 0, Shaper: 1001}}, 1)
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 11, want)
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 12, tdf.NewWriter().Int("COUN", 200).String("NAME", "Kills").
		Int("STRT", 1).Bytes())
	ps.expect(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 12, "getleaderboard_empty_reply")
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 13, tdf.NewWriter().Int("COUN", 200).String("NAME", "Skill").
		Int("STRT", 3).Bytes())
	ps.expect(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 13, "getleaderboard_empty_reply")
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 14, tdf.NewWriter().Int("COUN", 1).String("NAME", "Skill").
		Int("STRT", 2).Bytes())
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 14, LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID, Name: "player", Games: 5, Wins: 3, Shaper: 1002}}, 2))

	ps.send(ComponentGpsContentController, CmdGpsFilePetition, 15, ps.request("filepetition_request"))
	f := ps.next("filePetition reply")
	if guid, _ := payloadOf(t, "Blaze::GpsContentController::FilePetitionResponse", f).Str("GUID"); f.Type != fire.Reply ||
		f.ID != 15 || !strings.HasPrefix(guid, "report-") {
		t.Errorf("filePetition: %+v, GUID %q", f, guid)
	}
	ps.send(ComponentRooms, CmdRoomsSelectCategoryUpdates, 16, ps.request("selectcategoryupdates_request"))
	ps.expect(ComponentRooms, CmdRoomsSelectCategoryUpdates, fire.Reply, 16, "selectcategoryupdates_reply")
	ps.send(0x7802, 0x23, 5, nil) // resumeSession: no handler
	ps.expectFrame("resumesession_notfound_reply")
	ps.send(9, 2, 17, nil)
	ps.next("ping reply")
	log := ps.close()
	for _, want := range []string{
		"[blaze]    leaderboard \"Skill\": 2 row(s) from rank 1\n",
		"[blaze]    leaderboard \"SkillDaily\": 2 row(s) from rank 1\n",
		"[blaze]    (leaderboard \"Kills\" unknown: no rows)\n",
		" from persona 2000000001 about [2000000002] (Harassment)\n",
		"[blaze] -> error reply (no handler; error 0x4003), 0 bytes:\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// By default an account needs the 50 games the screen's text names: 49 games aren't ranked, 50 are.
func TestLeaderboardMinGames(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		seedGames(t, s, "player@example.com", "49", "30", nil, nil)
		seedGames(t, s, "second@example.com", "50", "3", nil, nil)
	})
	ps.loginAs("player@example.com", 1)
	ps.send(ComponentStats, CmdStatsGetLeaderboard, 10, ps.request("getleaderboard_request"))
	ps.expectPayload(ComponentStats, CmdStatsGetLeaderboard, fire.Reply, 10, LeaderboardReply([]store.LeaderRow{
		{PersonaID: store.FirstPersonaID + 1, Name: "second", Games: 50, Wins: 3}}, 1))
	ps.close()
}
