// gms/core/postgame (D8-live): the post-game messages against postgame.py's golden bytes (MATCH_FLOW.md *Match end and
// post-game (D8-static)*), a practice game's rewards, and the PostGame machine (T1 of MATCH_FLOW.md §5).
#include <cstdint>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/postgame/postgame.h"
#include "tests/check.h"

using namespace gms::postgame;
using gms::Bytes;

namespace {

// postgame.py FTUE_WIN_HEX: a won FTUE practice game of 12 minutes, roll 0.5.
const char kFtueWinHex[] =
    "c104f7bd3e100c000000da000000c80000000a00000000000000000000000000000000000000000000008f63a410000000000000000000"
    "0000008363a4100000000000000000000000008363a41000000000000000000000000000000000000000000000000000000000030000"
    "00000000000000000001000000";

const Send* Find(const std::vector<Send>& v, gms::ConnId conn, uint8_t wire, uint8_t sub) {
  for (const Send& s : v)
    if (s.conn == conn && s.msg.size() >= 2 && s.msg[0] == wire && s.msg[1] == sub) return &s;
  return nullptr;
}

Rewards ServerRewards() {
  Rewards r;
  r.lootBag = kLootBagBronze;
  r.destiny = 7;
  r.baseRoll = 100;
  r.ftue = true;
  return r;
}

}  // namespace

GMS_TEST(PostGameGoldenBytes) {
  CHECK_HEX(BuildGameOver(), "bf00");
  CHECK_HEX(BuildOver(1, 4660, kPracticeDescriptors), "c10101000000341200000c050000");
  CHECK_HEX(BuildVoteTimer(100000, 120000), "c102a086010000000000c0d4010000000000");
  CHECK_HEX(BuildExit(), "c105");
  Bytes m;
  CHECK(BuildRewards(PracticeRewards(true, 720, kPracticeDescriptors, 0.5), &m));
  CHECK_HEX(m, kFtueWinHex);
  CHECK_EQ(m.size(), static_cast<size_t>(2 + 0x78));
  // a lost practice game of one minute, roll 0.25 (postgame.py rewards --won 0 --seconds 60 --roll 0.25)
  CHECK(BuildRewards(PracticeRewards(false, 60, kPracticeDescriptors, 0.25), &m));
  CHECK_HEX(m,
            "c104939e074f050000007f00000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
            "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
            "0000000000000000000001000000");
  Rewards five;
  five.items.resize(5);
  CHECK(!BuildRewards(five, &m));  // C1 04 carries at most 4 items
  // the client's messages: the ack (c1 01) and the votes for 1 and 3 (c1 03 0a 00)
  ClientMessage cm;
  const uint8_t ack[] = {0x01};
  CHECK(ParseClientMessage(ack, 1, &cm) && cm.sub == kSubOver);
  const uint8_t votes[] = {0x03, 0x0A, 0x00};
  CHECK(ParseClientMessage(votes, 3, &cm) && cm.sub == kSubVotes && cm.votes == 0x000A);
  const uint8_t over[] = {0x01, 1, 0, 0, 0};
  CHECK(!ParseClientMessage(over, sizeof over, &cm));  // the client's C1 01 has no body
}

GMS_TEST(PostGameRewardsFormulas) {
  CHECK_EQ(BaseRoll(0.0), 67);
  CHECK_EQ(BaseRoll(0.5), 218);
  CHECK_EQ(BaseRoll(1.0), 525);
  int win = 0, alt = 0;
  OutcomePoints(true, 720, &win, &alt);
  CHECK_EQ(win, 200);
  CHECK_EQ(alt, 10);  // 0.91 x 12 = 10.92, truncated
  OutcomePoints(false, 3600 * 2, &win, &alt);
  CHECK_EQ(win, 0);
  CHECK_EQ(alt, 50);  // 55 minutes at most: 50.05
  CHECK_EQ(static_cast<int>(EcAward("Platinum", 720, kPracticeDescriptors)), 12);  // 5 + 0.63 x 12
  CHECK_EQ(static_cast<int>(EcAward("Bronze", 45 * 60, kPracticeDescriptors)), 26);  // 17 + 0.22 x 45 (> 30 min)
  CHECK_EQ(static_cast<int>(EcAward("Bronze", 10 * 60, kDescMatchmade)), 24);       // 22.4 + 0.225 x 10
  CHECK_EQ(static_cast<int>(EcAward("Nope", 600, kDescMatchmade)), 0);
  const Rewards won = PracticeRewards(true, 720, kPracticeDescriptors, 0.5);
  CHECK_EQ(won.lootBag, kLootBagPlatinum);
  CHECK_EQ(won.items.size(), static_cast<size_t>(3));
  CHECK(won.ftue);
  const Rewards lost = PracticeRewards(false, 720, kPracticeDescriptors, 0.5);
  CHECK_EQ(lost.lootBag, kLootBagBronze);
  CHECK(lost.items.empty() && lost.ftue);
  const Rewards noFtue = PracticeRewards(true, 720, kDescSolo | kDescSoloPractice, 0.5);
  CHECK(noFtue.lootBag == kLootBagBronze && noFtue.items.empty() && !noFtue.ftue);
  CHECK_EQ(AssetId("Platinum.LootBag"), kLootBagPlatinum);
  CHECK_EQ(AssetId("Diamond.LootBag"), kLootBagDiamond);
  CHECK_EQ(std::string(LootBagName(kLootBagGold)), std::string("Gold"));
  Rewards back;
  Bytes m;
  CHECK(BuildRewards(won, &m) && ParseRewards(m.data() + 2, m.size() - 2, &back));
  CHECK(back.lootBag == won.lootBag && back.destiny == 12 && back.items.size() == 3 && back.ftue);
  CHECK_EQ(ModeOf(kPracticeDescriptors) == Mode::kPractice, true);
  CHECK_EQ(ModeOf(kDescMatchmade) == Mode::kMatchmade, true);
  CHECK_EQ(ModeOf(kDescCustom) == Mode::kCustom, true);
  CHECK_EQ(WinnerTeam(kReasonMortalWins), 1);
  CHECK_EQ(WinnerTeam(kReasonSpiritBySurrender), 2);
}

// A lost practice game: BF 00, C1 01 {...} only after the client's own C1 01, then C1 04 at once with the server's
// rewards; a lost one from the host's own after rewardsWaitMs; every player gone = AllLeft.
GMS_TEST(PostGameLostPractice) {
  PostGame pg;
  pg.AddPlayer(7, 0, 1, PracticeRewards(false, 60, kPracticeDescriptors, 0.25));
  std::vector<Send> out;
  pg.Start(100.0, kReasonSpiritWins, 0xA6700171, kPracticeDescriptors, &out);  // the Mortal Guardian died: team 1 lost
  CHECK_EQ(out.size(), static_cast<size_t>(1));
  CHECK_HEX(out[0].msg, "bf00");
  out.clear();
  pg.Tick(100.5, 0, &out);
  CHECK(out.empty());  // no C1 01 before the client's own
  const uint8_t ack[] = {0x01};
  CHECK(pg.OnClientMessage(100.064, 7, ack, 1));
  pg.OnServerRewards(7, ServerRewards());
  pg.Tick(100.07, 0, &out);
  const Send* over = Find(out, 7, 0xC1, 1);
  CHECK(over != nullptr);
  if (over) CHECK_HEX(over->msg, "c10103000000710170a60c050000");
  const Send* rew = Find(out, 7, 0xC1, 4);
  CHECK(rew != nullptr);  // lost: C1 04 at once, from the server's rewards
  if (rew) {
    Rewards r;
    CHECK(ParseRewards(rew->msg.data() + 2, rew->msg.size() - 2, &r));
    CHECK(r.destiny == 7 && r.baseRoll == 100 && r.lootBag == kLootBagBronze);
    CHECK(rew->what.find("dgserver") != std::string::npos);
  }
  out.clear();
  pg.Tick(200.0, 0, &out);
  CHECK(out.empty());  // nothing twice
  CHECK(!pg.AllLeft());
  pg.OnDisconnected(7);
  CHECK(pg.AllLeft());

  // no ack and no server rewards: C1 01 after the 2 s fallback, the host's own C1 04 after the 3 s wait
  PostGame pg2;
  pg2.AddPlayer(9, 0, 1, PracticeRewards(false, 60, kPracticeDescriptors, 0.25));
  pg2.Start(10.0, kReasonSpiritWins, 1, kPracticeDescriptors, &out);
  out.clear();
  pg2.Tick(11.9, 0, &out);
  CHECK(out.empty());
  pg2.Tick(12.0, 0, &out);
  CHECK(Find(out, 9, 0xC1, 1) != nullptr && Find(out, 9, 0xC1, 1)->what.find("fallback") != std::string::npos);
  CHECK(Find(out, 9, 0xC1, 4) == nullptr);  // waits for the server's rewards
  out.clear();
  pg2.Tick(13.0, 0, &out);
  const Send* own = Find(out, 9, 0xC1, 4);
  CHECK(own != nullptr && own->what.find("the host's own") != std::string::npos);
  if (own) CHECK_EQ(own->msg.size(), static_cast<size_t>(2 + 0x78));
}

// A won practice game: C1 04 only on the client's 0x80000 (or after readyWaitMs), a server bag of 0 replaced by the
// host's, no C1 04 with rewards = none.
GMS_TEST(PostGameWonPractice) {
  PostGame pg;
  pg.AddPlayer(3, 0, 1, PracticeRewards(true, 156, kPracticeDescriptors, 0.5));
  std::vector<Send> out;
  pg.Start(0.0, kReasonMortalWins, 0xA6720171, kPracticeDescriptors, &out);
  const uint8_t ack[] = {0x01};
  pg.OnClientMessage(0.067, 3, ack, 1);
  Rewards noBag = ServerRewards();
  noBag.lootBag = 0;
  noBag.items = {RewardItem{0x10A4638F, false, 0}, RewardItem{0x10A46383, true, 15}};
  pg.OnServerRewards(3, noBag);
  out.clear();
  pg.Tick(0.1, 0, &out);
  CHECK(Find(out, 3, 0xC1, 1) != nullptr);
  CHECK(Find(out, 3, 0xC1, 4) == nullptr);  // won: waits for the player's 0x80000
  out.clear();
  pg.OnStatus(5.0, 3, 0x10491);  // no 0x80000
  pg.Tick(5.0, 0, &out);
  CHECK(out.empty());
  pg.OnStatus(9.5, 3, 0x90491);  // the practice cinematic's SetReadyForKarmaVote
  pg.Tick(9.5, 0, &out);
  const Send* rew = Find(out, 3, 0xC1, 4);
  CHECK(rew != nullptr);
  if (rew) {
    Rewards r;
    CHECK(ParseRewards(rew->msg.data() + 2, rew->msg.size() - 2, &r));
    CHECK_EQ(r.lootBag, kLootBagPlatinum);  // the server had no bag: the host's own (an unknown id asserts)
    CHECK(r.items.size() == 2 && r.items[1].duplicate && r.items[1].alternate == 15);
  }
  CHECK(pg.Describe(3).find("C1 04 sent (dgserver's)") != std::string::npos);

  // no 0x80000: C1 04 after readyWaitMs (60 s)
  PostGame pg2;
  pg2.AddPlayer(4, 0, 2, PracticeRewards(true, 156, kPracticeDescriptors, 0.5));
  pg2.Start(0.0, kReasonSpiritWins, 1, kPracticeDescriptors, &out);
  out.clear();
  pg2.Tick(2.0, 0, &out);  // C1 01 by the fallback
  pg2.Tick(61.9, 0, &out);
  CHECK(Find(out, 4, 0xC1, 4) == nullptr);
  pg2.Tick(62.0, 0, &out);
  CHECK(Find(out, 4, 0xC1, 4) != nullptr);

  // rewards = none: C1 01, never C1 04
  Options none;
  none.sendRewards = false;
  PostGame pg3(none);
  pg3.AddPlayer(5, 0, 1, Rewards());
  pg3.Start(0.0, kReasonSpiritWins, 1, kPracticeDescriptors, &out);
  out.clear();
  pg3.Tick(100.0, 0, &out);
  CHECK(Find(out, 5, 0xC1, 1) != nullptr && Find(out, 5, 0xC1, 4) == nullptr);
}

// A matchmade game: C1 02 {now, now + 20 s} once every player is ready, the masks, C1 04 2 s after the vote's end.
GMS_TEST(PostGameMatchmade) {
  PostGame pg;
  pg.AddPlayer(1, 0, 1, PracticeRewards(true, 600, kDescMatchmade, 0.5));
  pg.AddPlayer(2, 5, 2, PracticeRewards(false, 600, kDescMatchmade, 0.5));
  std::vector<Send> out;
  pg.Start(0.0, kReasonMortalWins, 1, kDescMatchmade, &out);
  CHECK_EQ(out.size(), static_cast<size_t>(2));
  CHECK(pg.GameMode() == Mode::kMatchmade);
  const uint8_t ack[] = {0x01};
  pg.OnClientMessage(0.05, 1, ack, 1);
  pg.OnClientMessage(0.06, 2, ack, 1);
  out.clear();
  pg.Tick(0.1, 100000, &out);
  CHECK(Find(out, 1, 0xC1, 1) && Find(out, 2, 0xC1, 1));
  CHECK(!Find(out, 1, 0xC1, 2));  // nobody ready yet
  pg.OnStatus(8.0, 1, 0x80000);
  out.clear();
  pg.Tick(8.0, 108000, &out);
  CHECK(out.empty());  // player 2 not ready
  pg.OnStatus(9.0, 2, 0x80000);
  pg.Tick(9.0, 109000, &out);
  const Send* vt = Find(out, 2, 0xC1, 2);
  CHECK(vt != nullptr);
  if (vt) CHECK_HEX(vt->msg, "c102c8a9010000000000e8f7010000000000");  // 109000 .. 129000
  const uint8_t votes[] = {0x03, 0x20, 0x00};  // player 1 votes for gameplay index 5
  CHECK(pg.OnClientMessage(29.0, 1, votes, 3));
  out.clear();
  pg.Tick(30.9, 130900, &out);
  CHECK(!Find(out, 1, 0xC1, 4) && pg.TakeKarmaReports().empty());
  pg.Tick(31.0, 131000, &out);  // vote 20 s + grace 2 s after 9.0: the vote closes
  CHECK(!Find(out, 1, 0xC1, 4));  // D8c: C1 04 waits for dgserver's karma-aware rewards
  CHECK_EQ(pg.TakeKarmaReports().size(), static_cast<size_t>(2));
  pg.Tick(33.9, 133900, &out);
  CHECK(!Find(out, 1, 0xC1, 4));
  pg.Tick(34.0, 134000, &out);  // none came within rewardsWaitMs (3 s) of the close: the host's own, its own tally
  CHECK(Find(out, 1, 0xC1, 4) && Find(out, 2, 0xC1, 4));
  CHECK(Find(out, 2, 0xC1, 4)->what.find("the host's own") != std::string::npos);
  // D8b-core: player 1's vote for gameplay index 5 is an enemy vote for player 2
  Rewards r;
  const Send* r2 = Find(out, 2, 0xC1, 4);
  CHECK(r2 && ParseRewards(r2->msg.data() + 2, r2->msg.size() - 2, &r));
  CHECK(r.karmaEnemy == 1 && r.karmaTeam == 0 && r.karmaParty == 0 && r.karmaPoints == 10);
  const Send* r1 = Find(out, 1, 0xC1, 4);
  CHECK(r1 && ParseRewards(r1->msg.data() + 2, r1->msg.size() - 2, &r));
  CHECK(r.karmaPoints == 0 && r.karmaEnemy == 0);
}

// D8b-core: the karma tally (postgame.py karma's KARMA_EXAMPLE): party, team and enemy votes; self-votes and unknown
// indices ignored.
GMS_TEST(PostGameKarmaTally) {
  const std::vector<KarmaVoter> voters = {
      {0, 1, 7, 0x0026}, {1, 1, 7, 0x0001}, {2, 1, -1, 0x0003}, {5, 2, -1, 0x0041}, {6, 2, -1, 0x0061}};
  const auto k = TallyKarma(voters);
  CHECK_EQ(k.size(), static_cast<size_t>(5));
  CHECK(k.at(0).party == 1 && k.at(0).team == 1 && k.at(0).enemy == 2 && k.at(0).Total() == 4);
  CHECK(k.at(1).party == 1 && k.at(1).team == 1 && k.at(1).enemy == 0);
  CHECK(k.at(2).party == 0 && k.at(2).team == 1 && k.at(2).enemy == 0);
  CHECK(k.at(5).party == 0 && k.at(5).team == 1 && k.at(5).enemy == 1);
  CHECK(k.at(6).Total() == 1 && k.at(6).team == 1);  // its own bit (6) is ignored
  // a bit for an index nobody has, and a voter without a mask
  const auto none = TallyKarma({{0, 1, -1, 0x8000}, {1, 1, -1, 0}});
  CHECK(none.at(0).Total() == 0 && none.at(1).Total() == 0 && none.size() == 2);
}

// D8b-core: a matchmade game of five players (the tally's example) gives player 0 postgame.py's MATCHMADE_KARMA_HEX:
// the server's rewards (no karma: an older dgserver that answered at finishGame) with the karma votes the host
// counted, rewardsWaitMs after the vote's close (D8c); a C1 03 after the vote closed is ignored.
GMS_TEST(PostGameMatchmadeKarma) {
  PostGame pg;
  const int idx[] = {0, 1, 2, 5, 6}, team[] = {1, 1, 1, 2, 2};
  const long long party[] = {7, 7, -1, -1, -1};
  const uint16_t mask[] = {0x0026, 0x0001, 0x0003, 0x0041, 0x0061};
  for (int i = 0; i < 5; ++i) {
    pg.AddPlayer(10 + i, idx[i], team[i], PracticeRewards(team[i] == 2, 600, kDescMatchmade, 0.5));
    pg.SetParty(10 + i, party[i]);
  }
  std::vector<Send> out;
  pg.Start(0.0, kReasonSpiritWins, 1, kDescMatchmade, &out);
  Rewards server;  // dgserver's playerRewards for player 0: a lost 10-minute game
  server.lootBag = kLootBagBronze;
  server.destiny = 24;
  server.baseRoll = 218;
  server.winAltPoints = 9;
  pg.OnServerRewards(10, server);
  const uint8_t ack[] = {0x01};
  for (int i = 0; i < 5; ++i) {
    pg.OnClientMessage(0.05, 10 + i, ack, 1);
    pg.OnStatus(5.0, 10 + i, 0x80000);
  }
  pg.Tick(5.0, 50000, &out);  // C1 01, then C1 02 {50000, 70000}
  CHECK(Find(out, 14, 0xC1, 2) != nullptr);
  for (int i = 0; i < 5; ++i) {
    const uint8_t votes[] = {0x03, static_cast<uint8_t>(mask[i] & 0xFF), static_cast<uint8_t>(mask[i] >> 8)};
    CHECK(pg.OnClientMessage(25.0, 10 + i, votes, 3));
  }
  out.clear();
  pg.Tick(26.9, 71900, &out);
  CHECK(pg.Karma().empty() && !Find(out, 10, 0xC1, 4));
  pg.Tick(27.0, 72000, &out);  // 20 s + 2 s after 5.0: the vote closes
  CHECK_EQ(pg.Karma().size(), static_cast<size_t>(5));
  CHECK(!Find(out, 10, 0xC1, 4));  // D8c: waiting for the karma-aware rewards
  pg.Tick(30.0, 75000, &out);      // none: C1 04 from the earlier ones and the host's tally
  const Send* r0 = Find(out, 10, 0xC1, 4);
  CHECK(r0 != nullptr);
  if (r0) {
    CHECK_HEX(r0->msg,
              "c104939e074f18000000da000000000000000900000000000000280000000100000001000000020000000000000000000000"
              "0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
              "00000000000000000000000000000000000000000000");
    CHECK(r0->what.find("karma 40") != std::string::npos);
  }
  // a late C1 03 changes nothing any more
  const uint8_t late[] = {0x03, 0xFF, 0xFF};
  CHECK(pg.OnClientMessage(28.0, 13, late, 3));
  CHECK(pg.Karma().at(0).Total() == 4);
}

// D8c: the vote's close hands out every player's karma votes once (TakeKarmaReports -> playerKarma); C1 04 goes out
// only when dgserver's karma-aware rewards are in (sent as they are, its karma included), or rewardsWaitMs after the
// close from the host's own rewards and tally; without a control channel (rewardsWaitMs 0) at the close.
GMS_TEST(PostGameMatchmadeServerKarma) {
  PostGame pg;
  pg.AddPlayer(1, 0, 1, PracticeRewards(false, 1200, kDescMatchmade, 0.8));  // team 1 lost
  pg.AddPlayer(2, 1, 2, PracticeRewards(true, 1200, kDescMatchmade, 0.8));
  std::vector<Send> out;
  pg.Start(0.0, kReasonSpiritWins, 1, kDescMatchmade, &out);
  const uint8_t ack[] = {0x01};
  for (gms::ConnId c : {1u, 2u}) {
    pg.OnClientMessage(0.05, c, ack, 1);
    pg.OnStatus(5.0, c, 0x80000);
  }
  pg.Tick(5.0, 50000, &out);  // C1 01, C1 02 {50000, 70000}
  CHECK(Find(out, 1, 0xC1, 2) && Find(out, 2, 0xC1, 2));
  const uint8_t v1[] = {0x03, 0x02, 0x00}, v2[] = {0x03, 0x01, 0x00};  // each votes for the other (enemy votes)
  pg.OnClientMessage(25.0, 1, v1, 3);
  pg.OnClientMessage(25.0, 2, v2, 3);
  out.clear();
  pg.Tick(26.9, 71900, &out);
  CHECK(pg.TakeKarmaReports().empty());  // the vote is still open
  pg.Tick(27.0, 72000, &out);            // closed
  CHECK(!Find(out, 1, 0xC1, 4) && !Find(out, 2, 0xC1, 4));
  const std::vector<KarmaReport> reports = pg.TakeKarmaReports();
  CHECK_EQ(reports.size(), static_cast<size_t>(2));
  for (const KarmaReport& k : reports)
    CHECK(k.votes.enemy == 1 && k.votes.team == 0 && k.votes.party == 0 && k.index == (k.conn == 1 ? 0 : 1));
  CHECK(pg.TakeKarmaReports().empty());  // once
  // dgserver's answer to player 1's playerKarma, 33 ms later: its bag decided with the karma in the score; its karma is
  // sent as it is (15 points here, where the host would count 10: the server's word wins)
  Rewards server;
  server.lootBag = kLootBagSilver;
  server.destiny = 34;
  server.baseRoll = 379;
  server.winAltPoints = 18;
  server.karmaPoints = 15;
  server.karmaEnemy = 1;
  pg.OnServerRewards(1, server, true);
  pg.Tick(27.033, 72033, &out);
  const Send* r1 = Find(out, 1, 0xC1, 4);
  CHECK(r1 != nullptr && !Find(out, 2, 0xC1, 4));
  Rewards r;
  if (r1) {
    CHECK(ParseRewards(r1->msg.data() + 2, r1->msg.size() - 2, &r));
    CHECK(r.lootBag == kLootBagSilver && r.baseRoll == 379 && r.karmaPoints == 15 && r.karmaEnemy == 1 &&
          r.karmaTeam == 0 && r.karmaParty == 0);
    CHECK(r1->what.find("dgserver's playerRewards with its karma") != std::string::npos);
  }
  CHECK(pg.Describe(1).find("C1 04 sent (dgserver's, karma counted)") != std::string::npos);
  pg.OnServerRewards(1, ServerRewards());  // anything later changes nothing
  out.clear();
  pg.Tick(29.9, 74900, &out);
  CHECK(out.empty());  // player 2: still waiting
  pg.Tick(30.0, 75000, &out);
  const Send* r2 = Find(out, 2, 0xC1, 4);
  CHECK(r2 != nullptr && out.size() == 1);
  if (r2) {
    CHECK(ParseRewards(r2->msg.data() + 2, r2->msg.size() - 2, &r));
    CHECK(r.karmaPoints == 10 && r.karmaEnemy == 1);  // the host's own tally
    CHECK(r2->what.find("the host's own") != std::string::npos);
  }

  // no control channel (rewardsWaitMs 0): C1 04 at the close, from the host's own rewards and tally
  Options solo;
  solo.rewardsWaitMs = 0;
  PostGame pg2(solo);
  pg2.AddPlayer(3, 0, 1, PracticeRewards(false, 600, kDescMatchmade, 0.5));
  pg2.Start(0.0, kReasonSpiritWins, 1, kDescMatchmade, &out);
  pg2.OnClientMessage(0.05, 3, ack, 1);
  pg2.OnStatus(1.0, 3, 0x80000);
  out.clear();
  pg2.Tick(1.0, 1000, &out);
  pg2.Tick(23.0, 23000, &out);
  CHECK(Find(out, 3, 0xC1, 4) != nullptr);
  CHECK_EQ(pg2.TakeKarmaReports().size(), static_cast<size_t>(1));
}

// B15b: a spectator in the post-game gets BF 00 and C1 01 (on its own C1 01, as a player), nothing else: the
// matchmade vote doesn't wait for it and its C1 02 isn't sent to it, its votes are ignored, it has no karma report and
// no C1 04; AllLeft counts the players only. (Spectators exist in custom games; the matchmade mode has the vote.)
GMS_TEST(PostGameSpectator) {
  PostGame pg;
  pg.AddPlayer(1, 0, 1, PracticeRewards(true, 600, kDescMatchmade, 0.5));
  pg.AddPlayer(2, 5, 2, PracticeRewards(false, 600, kDescMatchmade, 0.5));
  pg.AddSpectator(3, 10);
  std::vector<Send> out;
  pg.Start(0.0, kReasonMortalWins, 1, kDescMatchmade, &out);
  CHECK_EQ(out.size(), static_cast<size_t>(3));
  CHECK(Find(out, 3, 0xBF, 0) != nullptr);
  const uint8_t ack[] = {0x01};
  pg.OnClientMessage(0.05, 1, ack, 1);
  pg.OnClientMessage(0.06, 2, ack, 1);
  CHECK(pg.OnClientMessage(0.07, 3, ack, 1));
  out.clear();
  pg.Tick(0.1, 100000, &out);
  const Send* over = Find(out, 3, 0xC1, 1);
  CHECK(over != nullptr);
  if (over) CHECK(over->msg == BuildOver(kReasonMortalWins, 1, kDescMatchmade));
  pg.OnStatus(9.0, 1, 0x80000);
  pg.OnStatus(9.0, 2, 0x80000);  // the spectator never reports ready
  out.clear();
  pg.Tick(9.0, 109000, &out);
  CHECK(Find(out, 1, 0xC1, 2) && Find(out, 2, 0xC1, 2) && !Find(out, 3, 0xC1, 2));
  const uint8_t votes[] = {0x03, 0x01, 0x00};  // the spectator votes for player 0: ignored
  CHECK(pg.OnClientMessage(20.0, 3, votes, 3));
  out.clear();
  pg.Tick(31.0, 131000, &out);
  const std::vector<KarmaReport> reports = pg.TakeKarmaReports();
  CHECK_EQ(reports.size(), static_cast<size_t>(2));
  for (const KarmaReport& k : reports) CHECK(k.conn != 3 && k.votes.Total() == 0);
  pg.Tick(40.0, 140000, &out);
  CHECK(Find(out, 1, 0xC1, 4) && Find(out, 2, 0xC1, 4) && !Find(out, 3, 0xC1, 4));
  CHECK(pg.Describe(3).find("a spectator") != std::string::npos);
  pg.OnDisconnected(1);
  pg.OnDisconnected(2);
  CHECK(pg.AllLeft());  // the spectator is still there
}
