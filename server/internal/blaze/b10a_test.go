package blaze

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/golden"
	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// testMatchedGame is matchmade game 1 as the server makes it from the golden startmatchmaking_request
// (tdf_golden.py b10a_game).
func testMatchedGame() Game {
	return Game{ID: 1, Name: "player's Game", Settings: 0x506, Capacity: []int64{10, 0}, TeamCap: 5,
		TeamIDs: []int64{1, 2}, Version: "1.0.903.854-RL-Prod-Release", GMS: Addr{IP: 0x7F000001, Port: GMSPort},
		Seed: 7919, Attrs: []tdf.Pair{tdf.KV("ExpectedPlayerCount", "10"), tdf.KV("GameFlags", "1"),
			tdf.KV("GameOwnerId", "77359401"), tdf.KV("GameOwnerName", "player"), tdf.KV("GameType", "1"),
			tdf.KV("LevelId", "0")}}
}

func testMatchedPlayers() []Player {
	loop := Addr{IP: 0x7F000001, Port: 3659}
	t := int64(testTime) * 1000000
	return []Player{{ID: store.FirstPersonaID, Name: "player", Ext: loop, Int: loop, JoinTime: t},
		{ID: store.FirstPersonaID + 1, Name: "second", Ext: loop, Int: loop, JoinTime: t}}
}

// testParty is PvP party 1 as the server makes it from the golden createplaygroup_party_request.
func testParty() PlaygroupInfo {
	return PlaygroupInfo{ID: 1, Name: "2f1e4c9a-0b7d-4e21-9c3f-5a6b7c8d9", MemberLimit: 5, Owner: store.FirstPersonaID}
}

func testPartyLeader() PlaygroupMember {
	m := testMember()
	m.Perm = PartyLeaderPermissions
	return m
}

// b10aPayloads are B10a's replies and notifications with the golden case and the class they must match.
func b10aPayloads() []struct {
	name, class string
	got         []byte
} {
	party := MatchmakingContext{Session: 1, User: store.FirstPersonaID + 1, Result: MatchmakingCreatedGame,
		Group: &tdf.ObjID{Component: ComponentPlaygroups, Type: 1, ID: 1}}
	return []struct {
		name, class string
		got         []byte
	}{
		{"startmatchmaking_reply", "Blaze::GameManager::StartMatchmakingResponse", StartMatchmakingReply(1)},
		{"notifymatchmakingfailed_notification", "Blaze::GameManager::NotifyMatchmakingFailed",
			MatchmakingFailedNotification(MatchmakingContext{Session: 1, User: store.FirstPersonaID, Result: MatchmakingCanceled})},
		{"notifygamesetup_matchmade_notification", "Blaze::GameManager::NotifyGameSetup",
			MatchedGameSetupNotification(testMatchedGame(), testMatchedPlayers()[:1],
				MatchmakingContext{Session: 1, User: store.FirstPersonaID, Result: MatchmakingCreatedGame})},
		{"notifygamesetup_party_member_notification", "Blaze::GameManager::NotifyGameSetup",
			MatchedGameSetupNotification(testMatchedGame(), testMatchedPlayers(), party)},
		{"createplaygroup_party_reply", "Blaze::Playgroups::JoinPlaygroupResponse", JoinPlaygroupReply(testParty())},
		{"notifyjoinplaygroup_party_notification", "Blaze::Playgroups::NotifyJoinPlaygroup",
			JoinPlaygroupNotification(testParty(), []PlaygroupMember{testPartyLeader()}, store.FirstPersonaID)},
		{"notifyplaygroupattributesset_notification", "Blaze::Playgroups::NotifyPlaygroupAttributesSet",
			PlaygroupAttributesSetNotification(1, []tdf.Pair{tdf.KV("MatchmakingStatus", "1|1|0")})},
		{"notifymemberattributesset_notification", "Blaze::Playgroups::NotifyMemberAttributesSet",
			MemberAttributesSetNotification(1, store.FirstPersonaID+1, []tdf.Pair{tdf.KV("memberGEM", "0")})},
		{"notifyleaderchange_notification", "Blaze::Playgroups::NotifyLeaderChange",
			LeaderChangeNotification(1, store.FirstPersonaID+1, 1)},
		{"notifymemberpermissionschange_notification", "Blaze::Playgroups::NotifyMemberPermissionsChange",
			MemberPermissionsChangeNotification(1, store.FirstPersonaID+1, PartyLeaderPermissions)},
		{"notifyjoincontrolschange_notification", "Blaze::Playgroups::NotifyJoinControlsChange",
			JoinControlsChangeNotification(1, PlaygroupClosed)},
		{"notifydestroyplaygroup_notification", "Blaze::Playgroups::NotifyDestroyPlaygroup",
			DestroyPlaygroupNotification(1, PlaygroupDestroyDefault)},
	}
}

// B10a payloads, written independently by tdf_golden.py from the member tables (and checked against the
// client's tables in TestRepliesMatchClientMemberTables).
func TestB10aRepliesMatchGolden(t *testing.T) {
	g := golden.Load(t)
	for _, c := range b10aPayloads() {
		if got, want := hex.EncodeToString(c.got), g.WriterCase(t, c.name).Hex; got != want {
			t.Errorf("%s\n got %s\nwant %s", c.name, got, want)
		}
	}
}

// The client's B10a requests (rebuilt from the static reading in tdf_golden.py) fit their classes, and the
// server reads what it needs from them.
func TestB10aClientRequests(t *testing.T) {
	ps := newPipeSession(t)
	defer ps.close()
	for _, c := range []struct{ name, class string }{
		{"startmatchmaking_request", "Blaze::GameManager::StartMatchmakingRequest"},
		{"startmatchmaking_party_request", "Blaze::GameManager::StartMatchmakingRequest"},
		{"updatenetworkinfo_request", "Blaze::NetworkInfo"},
		{"createplaygroup_party_request", "Blaze::Playgroups::CreatePlaygroupRequest"},
		{"joinplaygroup_request", "Blaze::Playgroups::JoinPlaygroupRequest"},
	} {
		tdfcheck.Require(t, c.class, ps.request(c.name))
	}
	req, err := tdf.Decode(ps.request("startmatchmaking_request"))
	if err != nil {
		t.Fatal(err)
	}
	crit, _ := req.Sub("CRIT")
	psr, _ := crit.Sub("PSR")
	thld, _ := psr.Str("THLD")
	sites := map[string]int64{"eu": 150, "eu-any": 200, "na": 50, "na-any": 100}
	if r := MatchRegion(thld, sites); r != "na" {
		t.Errorf("region %q", r)
	}
	for _, c := range []struct {
		thld  string
		sites map[string]int64
		want  string
	}{
		{"flexRegion", sites, ""},
		{"specificRegion", nil, ""},
		{"specificRegion", map[string]int64{"eu": 20, "na": 90}, "eu"},
		{"specificRegion", map[string]int64{"eu-any": 20, "na": 90}, "eu"},
	} {
		if r := MatchRegion(c.thld, c.sites); r != c.want {
			t.Errorf("MatchRegion(%q, %v) = %q, want %q", c.thld, c.sites, r, c.want)
		}
	}
}

// A solo PvP queue as the client runs it (static reading): updateNetworkInfo with the NA region's ping
// sites, startMatchmaking; the reply carries MSID 1 and, with a lone player allowed at once, the
// matchmade NotifyGameSetup follows (both golden). Without a start rule met, cancelMatchmaking gets an
// empty reply and NotifyMatchmakingFailed SESSION_CANCELED (golden).
func TestMatchmadeGameGolden(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 1})
	})
	ps.loginAs("player@example.com", 0)
	ps.send(0x7802, 0x14, 2, ps.request("updatenetworkinfo_request"))
	ps.expect(0x7802, 0x14, fire.Reply, 2, "")
	ps.send(4, 0x0D, 3, ps.request("startmatchmaking_request"))
	ps.expect(4, 0x0D, fire.Reply, 3, "startmatchmaking_reply")
	ps.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_matchmade_notification")
	log := ps.close()
	for _, want := range []string{
		"[blaze]    matchmaking session 1: player 2000000001, region na, timeout 3h0m0s; 1 sessions queued (5v5, start with 1+ players after 0s)\n",
		"[blaze]    matchmaking: game 1 \"player's Game\", 1 v 0 (full false, region \"na\"): sessions [1] v []\n",
		"[blaze]    game 1 \"player's Game\": GMS 127.0.0.1:10042 (HNET), players [2000000001]\n",
		"[blaze] -> GameManager::NotifyGameSetup notification, ",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	ps = newPipeSessionWith(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 10, FillAfter: time.Hour})
	})
	ps.loginAs("player@example.com", 0)
	ps.send(4, 0x0D, 2, ps.request("startmatchmaking_request"))
	ps.expect(4, 0x0D, fire.Reply, 2, "startmatchmaking_reply")
	cancel := tdf.NewWriter().Int("MSID", 1).Bytes()
	tdfcheck.Require(t, "Blaze::GameManager::CancelMatchmakingRequest", cancel)
	ps.send(4, 0x0E, 3, cancel)
	ps.expect(4, 0x0E, fire.Reply, 3, "")
	ps.expect(4, NotifyMatchmakingFailedID, fire.Notification, 0, "notifymatchmakingfailed_notification")
	if log := ps.close(); !strings.Contains(log, "matchmaking session 1 ended: SESSION_CANCELED (canceled)") {
		t.Errorf("log:\n%s", log)
	}
}

// A PvP party as the client runs it (static reading): the leader creates the party (createPlaygroup JOIN
// 0, no key) and gets the leader's permissions; the guest joins it by id; the leader queues the party
// (BTPL); the guest's NotifyGameSetup carries the indirect matchmaking context with the party (golden).
func TestPartyMatchmakingGolden(t *testing.T) {
	ps := newPipeSessionWith(t, func(s *Server) {
		s.MM = matchmaker.New(matchmaker.Config{TeamSize: 5, MinPlayers: 1})
	})
	ps.loginAs("player@example.com", 0)
	ps.send(6, 1, 2, ps.request("createplaygroup_party_request"))
	ps.expect(6, 1, fire.Reply, 2, "createplaygroup_party_reply")
	ps.expect(6, 0x33, fire.Notification, 0, "notifyjoinplaygroup_party_notification")
	two := ps.another()
	two.loginAs("second@example.com", 0)
	two.send(0x7802, 0x14, 2, ps.request("updatenetworkinfo_request"))
	two.expect(0x7802, 0x14, fire.Reply, 2, "")
	two.send(6, 3, 3, ps.request("joinplaygroup_request"))
	two.expect(6, 3, fire.Reply, 3, "createplaygroup_party_reply")
	// B14: the guest learns the leader as a user (with its extended data) before the member list, the leader the
	// guest before NotifyMemberJoinedPlaygroup
	two.expectUserAdded(store.FirstPersonaID, true)
	join := payloadOf(t, "Blaze::Playgroups::NotifyJoinPlaygroup", two.next("NotifyJoinPlaygroup"))
	if f, _ := join.Get("MLST"); len(f.Value.(*tdf.List).Items) != 2 {
		t.Errorf("MLST %v", f.Value)
	}
	ps.expect(ComponentUserSessions, NotifyUserAdded, fire.Notification, 0, "useradded_notification")
	ps.expect(ComponentUserSessions, NotifyUserUpdated, fire.Notification, 0, "userupdated_online_notification")
	payloadOf(t, "Blaze::Playgroups::NotifyMemberJoinedPlaygroup", ps.next("NotifyMemberJoinedPlaygroup"))

	ps.send(4, 0x0D, 3, ps.request("startmatchmaking_party_request"))
	ps.expect(4, 0x0D, fire.Reply, 3, "startmatchmaking_reply")
	lead := payloadOf(t, "Blaze::GameManager::NotifyGameSetup", ps.next("leader's NotifyGameSetup"))
	if f, _ := lead.Get("PROS"); len(f.Value.(*tdf.List).Items) != 2 {
		t.Errorf("leader's PROS %v", f.Value)
	}
	if f, _ := lead.Get("REAS"); f.Value.(*tdf.Union).Member != setupReasonMatchmaking {
		t.Errorf("leader's REAS member %d", f.Value.(*tdf.Union).Member)
	}
	two.expect(4, 0x14, fire.Notification, 0, "notifygamesetup_party_member_notification")
	// the leader quits: its close hooks tell the guest (a synchronous pipe, so read before waiting)
	ps.client.Close()
	if f := two.next("NotifyMemberRemovedFromPlaygroup"); f.Command != NotifyMemberRemovedFromPlaygroupID ||
		hex.EncodeToString(f.Payload) != hex.EncodeToString(MemberRemovedFromPlaygroupNotification(1, store.FirstPersonaID, PlaygroupMemberRemoveDisconnected)) {
		t.Errorf("member removed: %#x %x", f.Command, f.Payload)
	}
	two.expect(6, NotifyLeaderChangeID, fire.Notification, 0, "notifyleaderchange_notification")
	two.expect(6, NotifyMemberPermissionsChangeID, fire.Notification, 0, "notifymemberpermissionschange_notification")
	<-ps.done
	two.close()
	if log := ps.log.String(); !strings.Contains(log, "matchmaking session 1: players [2000000001 2000000002], region any") ||
		!strings.Contains(log, "playgroup 1: 2000000002 is the new leader") {
		t.Errorf("log:\n%s", log)
	}
}
