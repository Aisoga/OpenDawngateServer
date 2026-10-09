// gms/core/surrender (D8b-core): SurrenderMsgs against re/scripts/surrender.py's golden bytes (MATCH_FLOW.md
// *Surrender and the karma vote (D8b-core)*), and the SurrenderVote machine: availability, refusals, a passed vote
// (the end reason), a failed one (the cooldown), the time-out. G3a: a player back on a new connection (RejoinMap).
#include <cstdint>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/session/rejoin.h"
#include "core/surrender/surrender.h"
#include "core/surrender/team_left.h"
#include "tests/check.h"

using namespace gms::surrender;
using gms::Bytes;
using gms::ConnId;

namespace {

// connections 101..105 = team 1 (gameplay indices 0..4), 201..205 = team 2 (5..9), 900 an observer (index 10)
void AddFiveVsFive(SurrenderVote* v, bool observer = true) {
  for (int i = 0; i < 5; ++i) {
    v->AddPlayer(101 + i, i, 1);
    v->AddPlayer(201 + i, 5 + i, 2);
  }
  if (observer) v->AddPlayer(900, 10, 0, true);
}

int Count(const std::vector<Send>& v, uint8_t sub) {
  int n = 0;
  for (const Send& s : v) n += s.msg.size() >= 2 && s.msg[0] == kWire && s.msg[1] == sub ? 1 : 0;
  return n;
}

const Send* Find(const std::vector<Send>& v, ConnId conn, uint8_t sub) {
  for (const Send& s : v)
    if (s.conn == conn && s.msg.size() >= 2 && s.msg[0] == kWire && s.msg[1] == sub) return &s;
  return nullptr;
}

bool Has(const std::vector<Send>& v, ConnId conn, uint8_t sub) { return Find(v, conn, sub) != nullptr; }

Status StatusOf(const Send* s) {
  ServerMessage m;
  if (s && ParseServerMessage(s->msg.data() + 1, s->msg.size() - 1, &m)) return m.status;
  return Status();
}

bool Vote(SurrenderVote& v, double now, int64_t ms, ConnId conn, bool yes, std::vector<Send>* out) {
  const uint8_t body[] = {yes ? kClientVoteYes : kClientVoteNo};
  return v.OnClientMessage(now, ms, conn, body, 1, out);
}

MatchView At(double matchS) {
  MatchView m;
  m.matchS = matchS;
  return m;
}

}  // namespace

GMS_TEST(SurrenderGoldenBytes) {
  CHECK_HEX(BuildAvailable(kAvailMatchTime), "ca0001");
  CHECK_HEX(BuildAvailable(kAvailKills), "ca0004");
  CHECK_HEX(BuildRequestFailed(kFailRecentVote), "ca0101");
  CHECK_HEX(BuildTeamResult(kVoteSucceeded, 2, 80), "ca04030250");
  CHECK_HEX(BuildTeamResult(kVoteFailed, 1, 25), "ca04040119");
  Status s;
  s.team = 1;
  s.startMs = 600000;
  s.state = kVoteFailing;
  const uint8_t votes[kSlots] = {kSlotYes, kSlotPending, kSlotPending, kSlotNo, kSlotIneligible};
  for (int i = 0; i < kSlots; ++i) {
    s.slots[i].index = static_cast<uint8_t>(i);
    s.slots[i].vote = votes[i];
    s.slots[i].flag = i != 4;
  }
  const Bytes st = BuildStatus(s);
  CHECK_HEX(st, "ca0301c0270900000000000200010203048482828501");
  CHECK_EQ(st.size(), static_cast<size_t>(2 + kStatusBodySize));
  ServerMessage m;
  CHECK(ParseServerMessage(st.data() + 1, st.size() - 1, &m));
  CHECK(m.sub == kSubStatus && m.status.team == 1 && m.status.startMs == 600000 && m.status.state == kVoteFailing);
  CHECK(m.status.slots[3].vote == kSlotNo && m.status.slots[3].flag && !m.status.slots[4].flag);
  const Bytes res = BuildTeamResult(kVoteSucceeded, 2, 80);
  CHECK(ParseServerMessage(res.data() + 1, res.size() - 1, &m) && m.result == 3 && m.team == 2 && m.yesPercent == 80);
  const uint8_t two[] = {2, 0};
  CHECK(!ParseServerMessage(two, 2, &m));  // subtype 2 asserts on the client: never built, not parsed
  // the client's votes: ca 01 = yes, ca 02 = no
  bool yes = false;
  const uint8_t y[] = {1}, n[] = {2}, other[] = {3};
  CHECK(ParseClientVote(y, 1, &yes) && yes);
  CHECK(ParseClientVote(n, 1, &yes) && !yes);
  CHECK(!ParseClientVote(other, 1, &yes));
  CHECK(Describe(Bytes{0xCA, 0x01}, true).find("vote for surrender") != std::string::npos);
  CHECK(Describe(st, false).find("running, failing") != std::string::npos);
  // the end reasons: team 1 (Mortal) surrendered -> 4 Spirit wins by surrender; team 2 -> 2
  CHECK_EQ(EndReason(1), 4u);
  CHECK_EQ(EndReason(2), 2u);
  CHECK_EQ(EndReason(0), 0u);
  CHECK_EQ(YesPercent(4, 5), 80);
  CHECK_EQ(YesPercent(2, 3), 67);
  CHECK_EQ(YesPercent(0, 0), 0);
}

// Availability: the match time for both teams, the team behind on kills or gold, a team with a player disconnected
// for 300 s; once per team, to the team and the observers.
GMS_TEST(SurrenderAvailability) {
  SurrenderVote v;
  AddFiveVsFive(&v);
  std::vector<Send> out;
  v.Tick(0, 0, At(19 * 60 + 59), &out);
  CHECK(out.empty());
  CHECK_EQ(v.UiAvailability(), 0);
  v.Tick(1, 0, At(20 * 60), &out);
  CHECK_EQ(Count(out, kSubAvailable), 10);  // the ten players (observers get no availability line [assumed])
  const Send* a = Find(out, 101, kSubAvailable);
  CHECK(a != nullptr);
  if (a) CHECK_HEX(a->msg, "ca0001");
  CHECK(v.Availability(1) == kAvailMatchTime && v.Availability(2) == kAvailMatchTime);
  CHECK_EQ(v.UiAvailability(), kAvailMatchTime);
  out.clear();
  v.Tick(2, 0, At(21 * 60), &out);
  CHECK(out.empty());  // sticky, announced once

  // kills: team 2 behind by 19 -> team 2 only (reason 4); gold: team 1 behind by 20000 -> team 1 (reason 3)
  SurrenderVote k;
  AddFiveVsFive(&k, false);
  MatchView m = At(600);
  m.kills[1] = 25;
  m.kills[2] = 7;  // 18 behind: not yet
  k.Tick(0, 0, m, &out);
  CHECK(out.empty());
  m.kills[2] = 6;  // 19 behind
  k.Tick(1, 0, m, &out);
  CHECK_EQ(Count(out, kSubAvailable), 5);
  CHECK(Has(out, 203, kSubAvailable) && !Has(out, 103, kSubAvailable));
  if (Find(out, 203, kSubAvailable)) CHECK_HEX(Find(out, 203, kSubAvailable)->msg, "ca0004");
  CHECK(k.Availability(1) == kUnavailable && k.Availability(2) == kAvailKills);
  CHECK_EQ(k.UiAvailability(), kAvailKills);
  out.clear();
  m.gold[1] = 10000;
  m.gold[2] = 30000;
  k.Tick(2, 0, m, &out);
  CHECK(Count(out, kSubAvailable) == 5 && Has(out, 101, kSubAvailable));
  CHECK_EQ(k.Availability(1), kAvailEconomy);

  // a team 1 player gone for 300 s -> team 1 (reason 2); the message goes to the connected ones
  SurrenderVote d;
  AddFiveVsFive(&d, false);
  out.clear();
  d.OnConnection(104, false, 10);
  d.Tick(309.9, 0, At(300), &out);
  CHECK(out.empty());
  d.Tick(310, 0, At(300), &out);
  CHECK_EQ(Count(out, kSubAvailable), 4);
  CHECK(!Has(out, 104, kSubAvailable) && Has(out, 105, kSubAvailable));
  CHECK_EQ(d.Availability(1), kAvailDisconnect);
  CHECK_EQ(d.Availability(2), kUnavailable);
}

// A request that can't start a vote: not available (0), an observer (3), a player disconnected 300 s in all (2), the
// cooldown after a failed vote (1, below); a no without a vote is ignored.
GMS_TEST(SurrenderRefused) {
  SurrenderVote v;
  AddFiveVsFive(&v);
  std::vector<Send> out;
  CHECK(Vote(v, 1, 1000, 101, true, &out));
  CHECK_EQ(out.size(), static_cast<size_t>(1));
  if (!out.empty()) {
    CHECK(out[0].conn == 101);
    CHECK_HEX(out[0].msg, "ca0100");
  }
  out.clear();
  CHECK(Vote(v, 1, 1000, 102, false, &out));
  CHECK(out.empty());  // a no without a vote
  CHECK(Vote(v, 1, 1000, 900, true, &out));
  CHECK(out.size() == 1 && out[0].conn == 900);
  if (!out.empty()) CHECK_HEX(out[0].msg, "ca0103");
  out.clear();
  // 103 was away 200 s and then 100 s more: 300 s in all -> ineligible (2), also once the team may surrender
  v.OnConnection(103, false, 10);
  v.OnConnection(103, true, 210);
  v.OnConnection(103, false, 300);
  v.OnConnection(103, true, 400);
  v.Tick(400, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(v, 401, 1000, 103, true, &out));
  CHECK(out.size() == 1 && out[0].conn == 103);
  if (!out.empty()) CHECK_HEX(out[0].msg, "ca0102");
  CHECK(!v.Running(1));
  // an unknown connection or another message is not the vote's
  const uint8_t three[] = {3};
  CHECK(!v.OnClientMessage(401, 0, 101, three, 1, &out));
  const uint8_t yes[] = {1};
  CHECK(!v.OnClientMessage(401, 0, 777, yes, 1, &out));
}

// A passing vote: the request is the first yes, the status goes to the team (and the observer) after every vote, the
// state is "failing" until the threshold; 4 of 5 pass at once: the final status (pending -> abstained), CA 04 to
// everyone, the end reason (team 1 surrendered -> 4), nothing after it.
GMS_TEST(SurrenderVotePasses) {
  SurrenderVote v;
  AddFiveVsFive(&v);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  out.clear();
  // 105 is disconnected at the start: ineligible (slot 4, flag 0)
  v.OnConnection(105, false, 5);
  CHECK(Vote(v, 10, 600000, 101, true, &out));
  CHECK(v.Running(1) && !v.Running(2));
  CHECK_EQ(Count(out, kSubStatus), 5);  // the four connected team-1 players and the observer
  CHECK(!Has(out, 201, kSubStatus) && Has(out, 900, kSubStatus));
  Status s = StatusOf(Find(out, 101, kSubStatus));
  CHECK(s.team == 1 && s.startMs == 600000 && s.state == kVoteFailing);
  CHECK(s.slots[0].vote == kSlotYes && s.slots[1].vote == kSlotPending && s.slots[4].vote == kSlotIneligible);
  out.clear();
  // 104 votes no: surrender.py's golden status
  CHECK(Vote(v, 12, 602000, 104, false, &out));
  const Send* st = Find(out, 102, kSubStatus);
  CHECK(st != nullptr);
  if (st) CHECK_HEX(st->msg, "ca0301c0270900000000000200010203048482828501");
  out.clear();
  // a second vote of the same player and a vote of the ineligible one change nothing
  CHECK(Vote(v, 13, 603000, 104, true, &out));
  CHECK(out.empty());
  v.OnConnection(105, true, 14);
  CHECK(Vote(v, 14, 604000, 105, true, &out));
  CHECK(out.empty());
  // 102 yes: 2 of 4 eligible = 50 % (failing), 103 yes: 3 of 4 = 75 % >= 66.6 %: passed at once
  CHECK(Vote(v, 15, 605000, 102, true, &out));
  CHECK(StatusOf(Find(out, 101, kSubStatus)).state == kVoteFailing);
  out.clear();
  CHECK(Vote(v, 16, 606000, 103, true, &out));
  s = StatusOf(Find(out, 103, kSubStatus));
  CHECK(s.state == kVoteSucceeded && s.slots[3].vote == kSlotNo);
  CHECK_EQ(Count(out, kSubTeamResult), 11);  // to everyone: the other team reads "The Enemy team has surrendered!"
  const Send* r = Find(out, 201, kSubTeamResult);
  CHECK(r != nullptr);
  if (r) CHECK_HEX(r->msg, "ca0403014b");  // succeeded, team 1, 75 %
  End e;
  CHECK(v.TakeEnd(&e));
  CHECK(e.team == 1 && e.reason == 4 && e.yesPercent == 75);
  CHECK(!v.TakeEnd(&e));
  CHECK(!v.Running(1));
  // after the end nothing more
  out.clear();
  v.Tick(100, 0, At(1300), &out);
  CHECK(Vote(v, 100, 0, 201, true, &out));
  CHECK(out.empty());
  CHECK(v.Describe().find("team 1 surrendered") != std::string::npos);
}

// A failing vote ends as soon as it can't pass; the team gets CA 04 (the other team doesn't), then the 300 s cooldown
// (CA 01 {1}); after it a new vote starts. Pending players abstain.
GMS_TEST(SurrenderVoteFails) {
  SurrenderVote v;
  AddFiveVsFive(&v, false);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(v, 50, 1000, 202, true, &out));  // team 2
  CHECK(Vote(v, 51, 2000, 201, false, &out));
  out.clear();
  CHECK(Vote(v, 52, 3000, 203, false, &out));  // 1 yes + 2 pending < 3.33: failed
  CHECK(!v.Running(2));
  const Status s = StatusOf(Find(out, 205, kSubStatus));
  CHECK(s.team == 2 && s.state == kVoteFailed);
  CHECK(s.slots[3].vote == kSlotAbstained && s.slots[4].vote == kSlotAbstained && s.slots[1].vote == kSlotYes);
  CHECK_EQ(Count(out, kSubTeamResult), 5);  // the team only
  CHECK(!Has(out, 101, kSubTeamResult));
  if (Find(out, 204, kSubTeamResult)) CHECK_HEX(Find(out, 204, kSubTeamResult)->msg, "ca04040214");  // 20 %
  End e;
  CHECK(!v.TakeEnd(&e));
  out.clear();
  CHECK(Vote(v, 351.9, 0, 204, true, &out));
  CHECK(out.size() == 1);
  if (!out.empty()) CHECK_HEX(out[0].msg, "ca0101");  // recent failed vote
  out.clear();
  CHECK(Vote(v, 352, 302000, 204, true, &out));
  CHECK(v.Running(2));
  CHECK(StatusOf(Find(out, 201, kSubStatus)).startMs == 302000);
  // team 1 is independent of team 2's cooldown
  out.clear();
  CHECK(Vote(v, 353, 303000, 101, true, &out));
  CHECK(v.Running(1) && v.Running(2));
}

// The time-out: the vote runs surrenderVoteDurationInSeconds (60 s); then pending players abstain and the share of
// all eligible players decides. With endWhenDecided off a vote at the threshold shows "passing" (state 1) until then.
GMS_TEST(SurrenderVoteTimesOut) {
  SurrenderVote v;
  for (int i = 0; i < 3; ++i) v.AddPlayer(11 + i, i, 1);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(v, 100, 0, 11, true, &out));  // 1 of 3 yes, 2 pending: can still pass
  CHECK(v.Running(1));
  out.clear();
  v.Tick(159.9, 0, At(1300), &out);
  CHECK(out.empty() && v.Running(1));
  v.Tick(160, 0, At(1300), &out);
  CHECK(!v.Running(1));
  const Status s = StatusOf(Find(out, 12, kSubStatus));
  CHECK(s.state == kVoteFailed && s.slots[1].vote == kSlotAbstained);
  if (Find(out, 13, kSubTeamResult)) CHECK_HEX(Find(out, 13, kSubTeamResult)->msg, "ca04040121");  // 33 %

  Tuning t;
  t.endWhenDecided = false;
  SurrenderVote w(t);
  for (int i = 0; i < 3; ++i) w.AddPlayer(21 + i, i, 1);
  w.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(w, 10, 0, 21, true, &out));
  CHECK(StatusOf(Find(out, 23, kSubStatus)).state == kVoteFailing);
  out.clear();
  CHECK(Vote(w, 11, 0, 22, true, &out));  // 2 of 3 = 66.7 % >= 66.6 %: would pass, keeps running
  CHECK(w.Running(1));
  CHECK(StatusOf(Find(out, 23, kSubStatus)).state == kVotePassing);
  out.clear();
  w.Tick(70, 0, At(1300), &out);
  End e;
  CHECK(!w.Running(1) && w.TakeEnd(&e) && e.reason == 4 && e.yesPercent == 67);

  // a one-player team (a solo game): the request alone passes
  SurrenderVote solo;
  solo.AddPlayer(31, 0, 2);
  solo.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(solo, 1, 0, 31, true, &out));
  CHECK(solo.TakeEnd(&e) && e.team == 2 && e.reason == 2 && e.yesPercent == 100);
}

// Q2: a pending voter whose connection ends during the vote is no longer pending, like a player who was disconnected
// at the start: the outcome doesn't depend on whether the disconnect came just before or just after the start, and the
// vote doesn't wait for someone who can't vote. A vote already cast stays.
GMS_TEST(SurrenderPendingVoterDisconnects) {
  SurrenderVote v;
  AddFiveVsFive(&v, false);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  CHECK(Vote(v, 10, 0, 101, true, &out));
  CHECK(Vote(v, 11, 0, 102, true, &out));
  CHECK(Vote(v, 12, 0, 103, true, &out));  // 3 of 5 yes, 2 pending: undecided
  CHECK(v.Running(1));
  out.clear();
  v.OnConnection(104, false, 13);  // pending: ineligible from now on
  v.OnConnection(101, false, 13);  // voted yes: the vote stays
  v.Tick(13.5, 0, At(1213.5), &out);
  // 3 yes of 4 eligible (104 out, 105 pending) = 75 % >= 66.6 %: passes at once
  CHECK(!v.Running(1));
  End e;
  CHECK(v.TakeEnd(&e) && e.team == 1 && e.reason == 4 && e.yesPercent == 75);
  const Status s = StatusOf(Find(out, 102, kSubStatus));
  CHECK_EQ(s.state, static_cast<uint8_t>(kVoteSucceeded));
  CHECK_EQ(s.slots[0].vote, static_cast<uint8_t>(kSlotYes));         // 101, disconnected after its vote
  CHECK_EQ(s.slots[3].vote, static_cast<uint8_t>(kSlotIneligible));  // 104
  CHECK_EQ(s.slots[4].vote, static_cast<uint8_t>(kSlotAbstained));   // 105
  CHECK(!Has(out, 104, kSubStatus) && !Has(out, 101, kSubStatus));  // nothing to a lost connection

  // the same with the disconnect before the start: the same outcome
  SurrenderVote w;
  AddFiveVsFive(&w, false);
  w.Tick(0, 0, At(1200), &out);
  w.OnConnection(104, false, 5);
  CHECK(Vote(w, 10, 0, 101, true, &out));
  CHECK(Vote(w, 11, 0, 102, true, &out));
  CHECK(Vote(w, 12, 0, 103, true, &out));
  CHECK(w.TakeEnd(&e) && e.yesPercent == 75);

  // a pending voter leaving can also decide a failure: 1 yes, 2 no, then the last two pending leave -> 1 of 3
  SurrenderVote f;
  AddFiveVsFive(&f, false);
  f.Tick(0, 0, At(1200), &out);
  CHECK(Vote(f, 10, 0, 201, true, &out));
  CHECK(Vote(f, 11, 0, 202, false, &out));
  CHECK(f.Running(2));
  out.clear();
  f.OnConnection(204, false, 12);
  f.OnConnection(205, false, 12);
  f.Tick(12.5, 0, At(1212.5), &out);
  CHECK(f.Running(2));  // 203 still pending: 1 + 1 of 3 could still pass
  CHECK(Vote(f, 13, 0, 203, false, &out));
  CHECK(!f.Running(2) && !f.TakeEnd(&e));
}

// Q2's last hole (the surrender side, G3a): a player back on a new connection. The session's RejoinMap gives the
// returning user its old gameplay index, and AddPlayer with that index moves its entry to the new connection: the team
// is complete again (no reason 2 300 s after the disconnect, which the old entry, left away, used to cause), the
// disconnected time is kept across connections (refusal 2 counts both absences), the old connection's late news is
// ignored.
GMS_TEST(SurrenderRejoinKeepsThePlayersEntry) {
  gms::RejoinMap seats;
  SurrenderVote v;
  // what the host does at an accepted HelloReq (IMatch::OnRejoin for a returning user): AddPlayer, then the connection
  auto join = [&](uint64_t user, ConnId conn, int index, int team, double now, std::vector<Send>* out) {
    gms::PlayerSlot s;
    s.gameplayIndex = static_cast<uint8_t>(index);
    s.team = static_cast<uint8_t>(team);
    const gms::RejoinMap::Admission a = seats.Admit(user, conn, s);
    v.AddPlayer(conn, a.slot.gameplayIndex, a.slot.team);
    v.OnConnection(conn, true, now, out);
    return a;
  };
  for (int i = 0; i < 5; ++i) {
    join(1000 + i, 101 + i, i, 1, 0, nullptr);
    join(2000 + i, 201 + i, 5 + i, 2, 0, nullptr);
  }
  std::vector<Send> out;
  // user 1003 (index 3) is away from 10 s to 40 s, then back on 1104; the match proposed index 9, the seat says 3
  v.OnConnection(104, false, 10);
  seats.OnDisconnected(104);
  const gms::RejoinMap::Admission back = join(1003, 1104, 9, 1, 40, &out);
  CHECK(back.rejoin && back.previous == 104 && back.slot.gameplayIndex == 3);
  CHECK(out.empty());  // no vote running: nothing to catch up
  v.Tick(400, 0, At(600), &out);
  CHECK(out.empty());
  CHECK_EQ(v.Availability(1), static_cast<uint8_t>(kUnavailable));
  v.OnConnection(104, false, 401);  // the old connection: no entry any more
  v.Tick(800, 0, At(700), &out);
  CHECK(out.empty());
  // its absences add up across connections: 30 s, then 270 s on 1104 -> 300 s in all: refused with 2
  v.OnConnection(1104, false, 900);
  seats.OnDisconnected(1104);
  join(1003, 2104, 3, 1, 1170, &out);
  v.Tick(1170, 0, At(1200), &out);  // 20 minutes: surrender available (reason 1)
  out.clear();
  const uint8_t yes[] = {kClientVoteYes};
  CHECK(v.OnClientMessage(1171, 0, 2104, yes, 1, &out));
  CHECK(out.size() == 1 && out[0].conn == 2104);
  if (!out.empty()) CHECK_HEX(out[0].msg, "ca0102");
  // the old connections are no players any more
  CHECK(!v.OnClientMessage(1172, 0, 104, yes, 1, &out));
  CHECK(!v.OnClientMessage(1172, 0, 1104, yes, 1, &out));
  // a vote's status has one slot per player: the returning player's once, by its index
  out.clear();
  CHECK(Vote(v, 1173, 700000, 101, true, &out));
  const Status s = StatusOf(Find(out, 2104, kSubStatus));
  for (int i = 0; i < kSlots; ++i) CHECK_EQ(s.slots[i].index, static_cast<uint8_t>(i));
  CHECK_EQ(s.slots[3].vote, static_cast<uint8_t>(kSlotIneligible));  // 300 s away in all
}

// G3a: leaving and coming back during the team's vote. A pending voter who leaves is ineligible for the rest of it (Q2)
// and stays so on its new connection, whose client gets the vote's status at once (its window shows it); the result
// reaches the new connection, not the old. A rejoin before the old connection's loss was noticed keeps a pending voter
// pending: it votes on its new connection.
GMS_TEST(SurrenderRejoinDuringAVote) {
  SurrenderVote v;
  AddFiveVsFive(&v, false);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(v, 10, 600000, 101, true, &out));
  CHECK(Vote(v, 11, 601000, 102, true, &out));  // 2 yes, 3 pending
  out.clear();
  v.OnConnection(104, false, 12);  // pending -> ineligible; the recount: 2 of 4, undecided
  v.Tick(12.5, 0, At(1212.5), &out);
  CHECK(v.Running(1));
  out.clear();
  v.AddPlayer(1104, 3, 1);  // back on 1104 with its old index
  v.OnConnection(1104, true, 13, &out);
  CHECK_EQ(out.size(), size_t{1});
  const Status s = StatusOf(Find(out, 1104, kSubStatus));
  CHECK(s.team == 1 && s.startMs == 600000 && s.state == kVoteFailing);
  for (int i = 0; i < kSlots; ++i) CHECK_EQ(s.slots[i].index, static_cast<uint8_t>(i));
  CHECK(s.slots[0].vote == kSlotYes && s.slots[2].vote == kSlotPending && s.slots[4].vote == kSlotPending);
  CHECK(s.slots[3].vote == kSlotIneligible && s.slots[3].flag);  // ineligible, connected again
  out.clear();
  CHECK(Vote(v, 14, 602000, 1104, true, &out));  // not a voter of this vote
  CHECK(out.empty());
  CHECK(Vote(v, 15, 603000, 103, true, &out));  // 3 of 4: passed
  CHECK(Has(out, 1104, kSubTeamResult) && !Has(out, 104, kSubTeamResult));
  End e;
  CHECK(v.TakeEnd(&e) && e.team == 1 && e.yesPercent == 75);

  SurrenderVote w;
  AddFiveVsFive(&w, false);
  w.Tick(0, 0, At(1200), &out);
  out.clear();
  CHECK(Vote(w, 10, 0, 201, true, &out));  // team 2: 1 yes, 4 pending
  out.clear();
  w.AddPlayer(1205, 9, 2);  // 205's user back on 1205 while 205 still counts as connected
  w.OnConnection(1205, true, 11, &out);
  CHECK(Has(out, 1205, kSubStatus));
  CHECK_EQ(StatusOf(Find(out, 1205, kSubStatus)).slots[4].vote, static_cast<uint8_t>(kSlotPending));
  w.OnConnection(205, false, 12);  // the old connection's loss, noticed late: nothing
  out.clear();
  CHECK(Vote(w, 13, 0, 1205, true, &out));
  CHECK_EQ(StatusOf(Find(out, 202, kSubStatus)).slots[4].vote, static_cast<uint8_t>(kSlotYes));
  CHECK(!Has(out, 205, kSubStatus));
  CHECK(w.Running(2));
}

// The match ended another way: Stop() ends everything quietly.
GMS_TEST(SurrenderStopped) {
  SurrenderVote v;
  AddFiveVsFive(&v);
  std::vector<Send> out;
  v.Tick(0, 0, At(1200), &out);
  CHECK(Vote(v, 1, 0, 101, true, &out));
  out.clear();
  v.Stop();
  v.Tick(100, 0, At(1300), &out);
  CHECK(Vote(v, 100, 0, 102, true, &out));
  CHECK(out.empty());
  End e;
  CHECK(!v.TakeEnd(&e));
}

// G3b: a team whose every human has been gone for the grace period surrenders (the other team must have a connected
// human); a rejoin within it resets the clock; it fires once; off with a negative grace.
GMS_TEST(SurrenderTeamLeftWatch) {
  TeamLeftWatch w(10.0);
  const TeamHumans here{1, 1}, gone{1, 0}, none{0, 0};
  CHECK_EQ(w.Tick(0.0, here, here), 0);
  CHECK_EQ(w.Tick(1.0, here, gone), 0);  // team 2's human left at 1 s
  CHECK(w.LeftSince(2) == 1.0);
  CHECK_EQ(w.Tick(8.0, here, here), 0);  // it came back (a rejoin): the clock resets
  CHECK(w.LeftSince(2) < 0);
  CHECK_EQ(w.Tick(9.0, here, gone), 0);
  CHECK_EQ(w.Tick(18.9, here, gone), 0);
  CHECK_EQ(w.Tick(19.0, here, gone), 2);  // 10 s gone
  CHECK(w.Fired());
  CHECK_EQ(w.Tick(30.0, here, gone), 0);  // once
  // nobody connected on either side: no surrender (dgserver ends the game); a team of bots only never counts
  TeamLeftWatch b(0.0);
  CHECK_EQ(b.Tick(0.0, gone, gone), 0);
  CHECK_EQ(b.Tick(1.0, here, none), 0);
  CHECK_EQ(b.Tick(2.0, TeamHumans{2, 0}, TeamHumans{3, 1}), 1);  // team 1's two humans both gone
  TeamLeftWatch off(-1.0);
  CHECK_EQ(off.Tick(100.0, here, gone), 0);
}
