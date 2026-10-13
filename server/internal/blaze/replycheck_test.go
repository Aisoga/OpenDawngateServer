package blaze

import (
	"testing"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tdf/tdfcheck"
)

// Every reply the server builds, checked against its class's member table in the client
// (docs/data/tdf_member_tables.txt via tdfcheck): only members of the class, in tag order, with the
// wire types the client's decoder expects, nested structs included. Add a row for each new reply.
func TestRepliesMatchClientMemberTables(t *testing.T) {
	acct, p := testAccount()
	b7a := newB7aFixture(t)
	for _, r := range []struct {
		class   string
		payload []byte
	}{
		{"Blaze::Util::PreAuthResponse", PreAuthReply("127.0.0.1", 17502, Service)},
		{"Blaze::Util::PingResponse", PingReply(1791405059)},
		{"Blaze::Authentication::LoginResponse", LoginReply(acct, testKey, testTime)},
		{"Blaze::Authentication::SessionInfo", SessionInfoReply(acct, p, testKey, true, testTime)},
		{"Blaze::UserSessionLoginInfo", UserAuthenticatedNotification(acct, p, testKey, true, testTime)},
		{"Blaze::UserSessionExtendedDataUpdate", ExtendedDataUpdateNotification(p.ID, 0, 0)},
		// the end of a session (kick.go)
		{"Blaze::UserSessionDisconnectReason", UserSessionDisconnectedNotification(DisconnectDuplicateLogin)},
		{"Blaze::UserSessionLogoutInfo", UserUnauthenticatedNotification(p.ID, LogoutTypeAccount)},
		{"Blaze::Util::PostAuthResponse", PostAuthReply("127.0.0.1", 9988, acct.UserID)},
		{"Blaze::Util::GetTelemetryServerResponse", TelemetryServerReply("127.0.0.1", 9988)},
		{"Blaze::Entry::EnterServiceResponse", EnterServiceReply()},
		// B5 part 2: the meta data up to the main menu (nil = an empty reply, all defaults)
		{"Blaze::BattlegroundsAccounts::BGAMetagameGetCompleteAccountDataResponse", CompleteAccountDataReply(BasicData{BlazeID: p.ID, Level: 1}, nil, nil)},
		{"Blaze::BattlegroundsAccounts::BGAMetagameGetBasicAccountDataResponse", BasicAccountDataReply([]BasicData{{BlazeID: p.ID, Level: 1}})},
		{"Blaze::BattlegroundsAccounts::GetUserSettingsResponse", GetUserSettingsReply(nil)},
		{"Blaze::BGEcommerce::GetCatalogResponse", GetCatalogReply("", nil)},
		{"Blaze::BGEcommerce::GetCommerceProfileResponse", GetCommerceProfileReply(store.Commerce{})},
		{"Blaze::BGEcommerce::GetBalanceResponse", GetBalanceReply(0, 0, 0)},
		{"Blaze::BGEcommerce::GetGameItemsResponse", GetGameItemsReply(nil)},
		{"Blaze::LivingLore::GetCurrentVoteResponse", GetCurrentVoteReply()},
		{"Blaze::BGWeb::GetCurrentVersionResponse", BGWebCurrentVersionReply()},
		{"Blaze::Stats::KeyScopes", KeyScopesReply()},
		{"Blaze::Stats::StatGroupResponse", StatGroupReply("PlayerStats", nil)},
		{"Blaze::Stats::KeyScopedStatValues", StatsAsyncNotification("PlayerStats", 1, 0, nil)},
		{"Blaze::Association::Lists", GetListsReply(emptyLists(), 0)}, // B13: an empty list of each requested type
		{"Blaze::Association::Lists", GetListsReply(nil, 0)},
		// B13b: the lists with members, the add and remove replies, the other side's notification, the users and
		// their presence
		{"Blaze::Association::Lists", GetListsReply([]ListContent{{Info: clientLists()[2],
			Members: []store.ListMember{{ID: p.ID, Name: p.Name}}, Total: 3}}, 1)},
		{"Blaze::Association::UpdateListMembersResponse", UpdateListMembersReply(nil, nil)},
		{"Blaze::Association::UpdateListMembersResponse", UpdateListMembersReply([]store.ListMember{{ID: p.ID, Name: p.Name}},
			[]store.ListMember{{ID: 7, Name: "old"}})},
		{"Blaze::Association::UpdateListWithMembersRequest", ListMembershipNotification(ListTypeFriends,
			[]store.ListMember{{ID: p.ID, Name: p.Name}}, ListUpdateAdd)},
		{"Blaze::NotifyUserAdded", UserAddedNotification(acct, p, 0x20001)},
		{"Blaze::UserStatus", UserUpdatedNotification(p.ID, true)},
		{"Blaze::UserSessionExtendedDataUpdate", PresenceNotification(acct, p, 0x20001)},
		{"Blaze::Messaging::FetchMessageResponse", MessageCountReply(0)},
		{"Blaze::Messaging::PurgeMessageResponse", MessageCountReply(0)},
		{"Blaze::BGOps::GetPlayerGameIDResponse", GetGameIDReply("")},
		{"Blaze::BGOps::GetCurrentVersionResponse", BGOpsCurrentVersionReply("prod-info")},
		{"Blaze::BGOps::IsUserMMLockedoutResponse", MMLockoutReply(0)},
		// B8: PLAY -> PRACTICE DAWNGATE -> GameManager::resetDedicatedServer
		{"Blaze::GameManager::JoinGameResponse", JoinGameReply(1)},
		{"Blaze::GameManager::NotifyGameSetup", GameSetupNotification(testGame(), testPlayer())},
		// B7a: player content (the synthetic content fixture's account)
		{"Blaze::BattlegroundsAccounts::BGAMetagameGetCompleteAccountDataResponse", b7a.completeData()},
		{"Blaze::BattlegroundsAccounts::BGAMetagameGetBasicAccountDataResponse", b7a.basicData()},
		{"Blaze::BattlegroundsAccounts::GetUserSettingsResponse", GetUserSettingsReply(b7a.acct.Settings)},
		{"Blaze::BGEcommerce::GetBalanceResponse", GetBalanceReply(b7a.acct.CC, b7a.acct.EC, b7a.acct.PC)},
		{"Blaze::BGEcommerce::GetCatalogResponse", GetCatalogReply(CatalogVersion, OffersOf(b7a.content))},
		{"Blaze::BGEcommerce::GetGameItemsResponse", GetGameItemsReply(GameItemsOf(b7a.content, 1))},
		{"Blaze::Stats::StatGroupResponse", StatGroupReply("PlayerStats", StatGroups["PlayerStats"])},
		{"Blaze::Stats::KeyScopedStatValues", b7a.statsNotification()},
		{"Blaze::UserSessionExtendedDataUpdate", ExtendedDataUpdateNotification(p.ID, b7a.acct.UserInfo, 0)},
		// B11: the game's chat playgroup (Playgroups::createPlaygroup "GG-1" and what follows)
		{"Blaze::Playgroups::JoinPlaygroupResponse", JoinPlaygroupReply(testPlaygroup())},
		{"Blaze::Playgroups::NotifyJoinPlaygroup", JoinPlaygroupNotification(testPlaygroup(),
			[]PlaygroupMember{testMember(), secondMember()}, p.ID)},
		{"Blaze::Playgroups::NotifyMemberJoinedPlaygroup", MemberJoinedPlaygroupNotification(1, secondMember())},
		{"Blaze::Playgroups::NotifyMemberRemoveFromPlaygroup", MemberRemovedFromPlaygroupNotification(1,
			secondMember().BlazeID, PlaygroupMemberRemoveDisconnected)},
		// B12: another player's session ended (logout or closed connection) in a running hosted game. The
		// logout's own reply is empty and its RPC job has no response class (logout_test.go).
		{"Blaze::GameManager::NotifyPlayerRemoved", PlayerRemovedNotification(1, p.ID, RemovedBlazeServerConnLost)},
	} {
		t.Run(r.class, func(t *testing.T) { tdfcheck.Require(t, r.class, r.payload) })
	}
	// B10a: PvP matchmaking and the PvP lobby's party (b10a_test.go)
	for _, c := range b10aPayloads() {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	// B10c: the Messaging relay and the profanity filter (messaging_test.go)
	for _, c := range b10cPayloads() {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	// B7c: perk pages, purchases, the commerce profile, wins, the account after a game (progression_test.go)
	for _, c := range b7cPayloads(t) {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	// B7d: the profile's keyed stat groups (history_test.go)
	for _, c := range b7dPayloads(t) {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	// B14: the system messages (kill switches, broadcasts) and a party member's data with its wins (b14_test.go)
	for _, c := range b14Payloads() {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	// B15: custom games and the game browser (customgames_test.go)
	for _, c := range b15Payloads() {
		t.Run(c.name, func(t *testing.T) { tdfcheck.Require(t, c.class, c.got) })
	}
	for _, m := range []ClientMessage{KillSwitchMessage(nil), BroadcastMessage(Broadcast{ID: 64, Flags: 7, Text: "x"})} {
		tdfcheck.Require(t, "Blaze::Messaging::ServerMessage", SystemMessageNotification(1<<40, m, testTime))
	}
}

// testGame and testPlayer are the practice game of session b8_run5 with fixed ids and time (the
// request's values; tdf_golden.py writes the same).
func testGame() Game {
	return Game{ID: 1, Name: "player's Game", Settings: 1284, Capacity: []int64{10, 0}, TeamCap: 5,
		TeamIDs: []int64{1, 2}, Version: "1.0.903.854-RL-Prod-Release", GMS: Addr{IP: 0x7F000001, Port: GMSPort},
		Seed: 7919, Attrs: []tdf.Pair{tdf.KV("ExpectedPlayerCount", "1"), tdf.KV("GameFlags", "1292"),
			tdf.KV("GameOwnerId", "77359401"), tdf.KV("GameOwnerName", "player"), tdf.KV("GameType", "1"),
			tdf.KV("LevelId", "0")}}
}

func testPlayer() Player {
	return Player{ID: 2000000001, Name: "player", Team: 0, Ext: Addr{IP: 0x7F000001, Port: 3659},
		Int: Addr{IP: 0x7F000001, Port: 3659}, JoinTime: int64(testTime) * 1000000}
}
