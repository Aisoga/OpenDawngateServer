// gms/core/agents (D1b-live): the aggro list's order (docs/AGENT_AI.md §6.3) and the battle-ready hysteresis.
#include <vector>

#include "core/agents/aggro.h"
#include "core/agents/minion_target.h"
#include "tests/check.h"

using gms::agents::AggroCandidate;

namespace {

AggroCandidate Cand(unsigned id, float distance, int index) {
  AggroCandidate c;
  c.id = id;
  c.distance = distance;
  c.index = index;
  return c;
}

std::vector<unsigned> Ids(const std::vector<AggroCandidate>& v) {
  std::vector<unsigned> ids;
  for (const auto& c : v) ids.push_back(c.id);
  return ids;
}

}  // namespace

GMS_TEST(AggroOrderNearerFirst) {
  std::vector<AggroCandidate> v = {Cand(1, 12.f, 0), Cand(2, 3.f, 1), Cand(3, 7.5f, 2)};
  gms::agents::OrderAggro(&v, gms::agents::kAggroNormal);
  CHECK(Ids(v) == (std::vector<unsigned>{2, 3, 1}));
  // equal distances keep the list order
  std::vector<AggroCandidate> w = {Cand(5, 4.f, 0), Cand(4, 4.f, 1)};
  gms::agents::OrderAggro(&w, gms::agents::kAggroNormal);
  CHECK(Ids(w) == (std::vector<unsigned>{5, 4}));
}

GMS_TEST(AggroOrderPriorities) {
  // every rule against the next one: taunt > player call for help > sticky > non-player call for help > non-players
  // (minion/tower) > distance
  AggroCandidate near = Cand(1, 1.f, 0);
  AggroCandidate otherHelp = Cand(2, 9.f, 1);
  otherHelp.otherReportAgeMs = 500;
  AggroCandidate sticky = Cand(3, 8.f, 2);
  sticky.sticky = true;
  AggroCandidate playerHelp = Cand(4, 7.f, 3);
  playerHelp.playerReportAgeMs = 2999;
  AggroCandidate taunt = Cand(5, 20.f, 4);
  taunt.taunting = true;
  std::vector<AggroCandidate> v = {near, otherHelp, sticky, playerHelp, taunt};
  gms::agents::OrderAggro(&v, gms::agents::kAggroMinion);
  CHECK(Ids(v) == (std::vector<unsigned>{5, 4, 3, 2, 1}));
}

GMS_TEST(AggroOrderCallForHelpExpires) {
  AggroCandidate old = Cand(1, 9.f, 0);
  old.playerReportAgeMs = 3001;  // older than callForHelpTime: no priority left
  AggroCandidate near = Cand(2, 2.f, 1);
  std::vector<AggroCandidate> v = {old, near};
  gms::agents::OrderAggro(&v, gms::agents::kAggroNormal);
  CHECK(Ids(v) == (std::vector<unsigned>{2, 1}));
  CHECK(!gms::agents::Recent(-1, 3000));
  CHECK(gms::agents::Recent(0, 3000));
  CHECK(gms::agents::Recent(3000, 3000));
}

GMS_TEST(AggroOrderNonPlayersFirstOnlyForMinionsAndTowers) {
  AggroCandidate shaper = Cand(1, 2.f, 0);
  shaper.isPlayer = true;
  AggroCandidate minion = Cand(2, 10.f, 1);
  for (int type : {gms::agents::kAggroMinion, gms::agents::kAggroTower}) {
    std::vector<AggroCandidate> v = {shaper, minion};
    gms::agents::OrderAggro(&v, type);
    CHECK(Ids(v) == (std::vector<unsigned>{2, 1}));
  }
  for (int type : {gms::agents::kAggroNormal, gms::agents::kAggroJungleCreature, gms::agents::kAggroObjective}) {
    std::vector<AggroCandidate> v = {shaper, minion};
    gms::agents::OrderAggro(&v, type);
    CHECK(Ids(v) == (std::vector<unsigned>{1, 2}));
  }
  // a shaper that hit an allied shaper (player call for help) still goes first for a tower
  AggroCandidate hitter = shaper;
  hitter.playerReportAgeMs = 100;
  std::vector<AggroCandidate> v = {minion, hitter};
  gms::agents::OrderAggro(&v, gms::agents::kAggroTower);
  CHECK(Ids(v) == (std::vector<unsigned>{1, 2}));
}

GMS_TEST(BattleReadyHysteresis) {
  using gms::agents::NextBattleReady;
  CHECK(!NextBattleReady(false, -1.f));
  CHECK(!NextBattleReady(false, 30.f));  // between the ranges: stays off
  CHECK(NextBattleReady(false, 27.f));
  CHECK(NextBattleReady(true, 30.f));  // between the ranges: stays on
  CHECK(NextBattleReady(true, 35.f));
  CHECK(!NextBattleReady(true, 35.5f));
  CHECK(!NextBattleReady(true, -1.f));
}

GMS_TEST(AggroKeepByRange) {
  using gms::agents::KeepByRange;
  // an ordinary entry: within the drop range and the tether
  CHECK(KeepByRange(false, 33.f, 33.f, true));
  CHECK(!KeepByRange(false, 33.5f, 33.f, true));
  CHECK(!KeepByRange(false, 5.f, 33.f, false));
  // a base structure (D5): kept wherever the minion stands, so a Guardian 41 units away stays on the list
  CHECK(KeepByRange(true, 41.f, 33.f, true));
  CHECK(KeepByRange(true, 80.f, 33.f, false));
  CHECK_EQ(static_cast<int>(gms::agents::kEntryFlagBaseStructure), 1);
}

// ---- MINTARGET: lane minions and shapers ----

GMS_TEST(MinionTargetOrderNonPlayersBeforeSticky) {
  using namespace gms::agents;
  AggroCandidate shaper = Cand(1, 3.f, 0);
  shaper.isPlayer = true;
  shaper.sticky = true;
  shaper.playerReportAgeMs = 100;
  AggroCandidate tower = Cand(2, 19.f, 1);
  std::vector<AggroCandidate> v = {shaper, tower};
  OrderAggro(&v, kAggroMinion);  // the old rule: a sticky/reported shaper stays first
  CHECK(Ids(v) == (std::vector<unsigned>{1, 2}));
  OrderAggro(&v, kAggroMinion, kCallForHelpTimeMs, true);
  CHECK(Ids(v) == (std::vector<unsigned>{2, 1}));
  v[1].defend = true;  // the defended shaper goes first
  OrderAggro(&v, kAggroMinion, kCallForHelpTimeMs, true);
  CHECK(Ids(v) == (std::vector<unsigned>{1, 2}));
  AggroCandidate taunt = Cand(3, 30.f, 2);
  taunt.taunting = true;
  taunt.isPlayer = true;
  v.push_back(taunt);
  OrderAggro(&v, kAggroMinion, kCallForHelpTimeMs, true);
  CHECK(v[0].id == 3);
}

GMS_TEST(MinionTargetShaperAllowed) {
  using namespace gms::agents;
  MinionTargetOptions o;
  CHECK(ShaperAllowed(o, false, 10.f, false, false));  // nothing else in range
  CHECK(!ShaperAllowed(o, false, 10.f, true, false));  // a minion/tower in range
  CHECK(ShaperAllowed(o, false, 10.f, true, true));    // defending an own shaper
  CHECK(!ShaperAllowed(o, false, 26.f, false, true));  // beyond the leash: dropped even then
  CHECK(ShaperAllowed(o, false, 25.f, false, false));
  CHECK(ShaperAllowed(o, true, 40.f, true, false));    // a taunt holds
  o.enabled = false;
  CHECK(ShaperAllowed(o, false, 40.f, true, false));   // the old behaviour
}

GMS_TEST(MinionTargetShaperHitLog) {
  gms::agents::ShaperHitLog log;
  log.Record(7, 9, 1000);
  CHECK(log.Attacked(7, 9, 3500, 2500));
  CHECK(!log.Attacked(7, 9, 3501, 2500));  // window over
  CHECK(!log.Attacked(8, 9, 1500, 2500));  // another attacker
  CHECK(!log.Attacked(7, 10, 1500, 2500));
  log.Record(8, 9, 2000);                  // the latest hit replaces the earlier one
  CHECK(log.Attacked(8, 9, 2500, 2500) && !log.Attacked(7, 9, 2500, 2500));
}
