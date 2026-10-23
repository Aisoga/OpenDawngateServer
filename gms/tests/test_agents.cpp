// gms/core/agents (D1b-live): the aggro list's order (docs/AGENT_AI.md §6.3) and the battle-ready hysteresis.
#include <string>
#include <vector>

#include "core/agents/aggro.h"
#include "core/agents/leash.h"
#include "core/agents/minion_target.h"
#include "core/agents/tower_target.h"
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

GMS_TEST(MinionChaseLaneLeash) {
  using namespace gms::agents;
  MinionChaseOptions o;
  MinionChase s;
  const float p[2] = {0.f, 0.f};
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 19.f, p, 0) == kChaseKeep);
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 21.f, p, 0) == kChaseLaneLeash);
  CHECK(ShaperChaseCheck(o, s, 5, false, true, true, 21.f, p, 0) == kChaseLaneLeash);  // the lane comes first
  CHECK(ShaperChaseCheck(o, s, 5, true, false, true, 40.f, p, 0) == kChaseKeep);       // a taunt holds
  CHECK(ShaperChaseCheck(o, s, 5, false, false, false, 60.f, p, 0) == kChaseKeep);     // lane done: no band
  o.enabled = false;
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 60.f, p, 0) == kChaseKeep);
}

GMS_TEST(MinionChaseDistanceTimeAndIgnore) {
  using namespace gms::agents;
  MinionChaseOptions o;
  MinionChase s;
  // from a: 2.0 (fighting on the spot), 7.1 (following), 21.2 (beyond the chase distance)
  const float a[2] = {10.f, 10.f}, still[2] = {12.f, 10.f}, follow[2] = {15.f, 15.f}, far[2] = {25.f, 25.f};
  UpdateChase(&s, 5, true, a, 1000);  // shaper 5 the best target from 1 s, at a
  CHECK(s.target == 5 && s.sinceMs == 1000 && s.start[0] == 10.f);
  UpdateChase(&s, 5, true, follow, 2500);  // the chase keeps its start
  CHECK(s.sinceMs == 1000 && s.start[0] == 10.f);
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, follow, 3999) == kChaseKeep);
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, follow, 4000) == kChaseTime);
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, still, 9000) == kChaseKeep);  // it stands and fights
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, far, 2000) == kChaseDistance);
  CHECK(ShaperChaseCheck(o, s, 6, false, false, true, 5.f, far, 9000) == kChaseKeep);  // another shaper
  NoteChaseDrop(o, &s, 5, 4000);
  CHECK(s.target == 0 && s.sinceMs == -1);
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, a, 7999) == kChaseIgnored);
  CHECK(ShaperChaseCheck(o, s, 5, false, true, true, 5.f, a, 7999) == kChaseKeep);  // it hits an own shaper
  CHECK(ShaperChaseCheck(o, s, 5, false, false, true, 5.f, a, 8000) == kChaseKeep);  // ignore over
  UpdateChase(&s, 5, true, far, 9000);
  CHECK(s.sinceMs == 9000 && s.start[0] == 25.f);
  UpdateChase(&s, 7, false, far, 9700);  // a minion as the best target: the chase ends
  CHECK(s.target == 0 && s.sinceMs == -1);
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

// ---- MINPURSUE: a lane minion's Pursue leaf ends with its target ----

GMS_TEST(MinionFollowCheck) {
  using namespace gms::agents;
  MinionFollowOptions o;  // lane leash 20
  CHECK(MinionFollowCheck(o, 5, 5, true, false, true, 10.f) == kFollowKeep);
  CHECK(MinionFollowCheck(o, 5, 7, false, false, true, 10.f) == kFollowNotBest);  // a minion became the best target
  CHECK(MinionFollowCheck(o, 5, 7, true, false, true, 10.f) == kFollowNotBest);   // another shaper
  CHECK(MinionFollowCheck(o, 5, 0, false, false, true, 10.f) == kFollowListEmpty);
  CHECK(MinionFollowCheck(o, 5, 5, true, false, true, 21.f) == kFollowLaneLeash);  // a shaper, beyond the lane leash
  CHECK(MinionFollowCheck(o, 5, 5, true, true, true, 40.f) == kFollowKeep);        // a taunt holds
  CHECK(MinionFollowCheck(o, 5, 5, false, false, true, 40.f) == kFollowKeep);      // a minion or structure: no leash
  CHECK(MinionFollowCheck(o, 5, 5, true, false, false, 40.f) == kFollowKeep);      // the lane's end: no band
  CHECK(MinionFollowCheck(o, 0, 0, false, false, true, 10.f) == kFollowKeep);      // no follow known
  CHECK(MinionFollowCheck(o, 0xFFFFFFFFu, 5, true, false, true, 1.f) == kFollowNotBest);  // started without a best
  o.enabled = false;
  CHECK(MinionFollowCheck(o, 5, 0, false, false, true, 40.f) == kFollowKeep);
  CHECK(std::string(MinionFollowEndName(kFollowListEmpty)) == "list empty");
}

// ---- TOWERAGGRO: whom a tower shoots ----

namespace {
gms::agents::ShaperHit Hit(unsigned attacker, unsigned victim, int team, float x, float y, long long ms) {
  gms::agents::ShaperHit h;
  h.attacker = attacker;
  h.victim = victim;
  h.victimTeam = team;
  h.pos[0] = x;
  h.pos[1] = y;
  h.radius = 1.f;
  h.ms = ms;
  return h;
}
}  // namespace

GMS_TEST(TowerDefendTrigger) {
  using namespace gms::agents;
  const float tower[2] = {0.f, 0.f};
  // a Binding: footprint 2.76, range 28; the victim's footprint 1: edge distance = centre - 3.76
  CHECK(TowerDefendTrigger(Hit(9, 4, 1, 31.7f, 0.f, 0), 1, tower, 2.76f, 28.f));   // edge 27.94: within
  CHECK(!TowerDefendTrigger(Hit(9, 4, 1, 31.8f, 0.f, 0), 1, tower, 2.76f, 28.f));  // edge 28.04: outside
  CHECK(!TowerDefendTrigger(Hit(9, 4, 2, 5.f, 0.f, 0), 1, tower, 2.76f, 28.f));    // a shaper of the other team
}

GMS_TEST(TowerDefendLockUntilOutOfRange) {
  using namespace gms::agents;
  ShaperHitHistory hist;
  TowerDefend d;
  const float tower[2] = {0.f, 0.f};
  hist.Record(Hit(9, 4, 1, 10.f, 0.f, 1000));  // enemy shaper 9 hits own shaper 4 under the tower
  hist.Record(Hit(8, 4, 1, 40.f, 0.f, 1000));  // enemy shaper 8 hits it outside the range
  hist.Record(Hit(4, 9, 2, 10.f, 0.f, 1000));  // the own shaper hits back: not a lock for this tower
  CHECK(d.Scan(hist, 1, tower, 2.76f, 28.f, 1200) == 1);
  CHECK(d.Has(9) && !d.Has(8) && !d.Has(4));
  CHECK(d.Scan(hist, 1, tower, 2.76f, 28.f, 1700) == 0);  // the same hits aren't seen twice
  hist.Record(Hit(6, 4, 1, 10.f, 0.f, 1700));            // a hit in the same millisecond as the last scan counts
  CHECK(d.Scan(hist, 1, tower, 2.76f, 28.f, 1700) == 1 && d.Has(6));
  d.locked.erase(d.locked.begin() + 1);
  // the lock holds without new hits while 9 stays within 28 (here 27.9), long after any window
  float dist9 = 27.9f;
  auto onList = [&dist9](std::uint32_t id, float* dd) {
    if (id != 9 || dist9 < 0.f) return false;
    *dd = dist9;
    return true;
  };
  CHECK(d.Release(28.f, onList).empty() && d.Has(9));
  hist.Expire(60000, 2000);
  CHECK(hist.Hits().empty());
  CHECK(d.Scan(hist, 1, tower, 2.76f, 28.f, 60000) == 0 && d.Has(9));
  dist9 = 28.1f;  // it left the range
  CHECK(d.Release(28.f, onList) == std::vector<std::uint32_t>{9});
  CHECK(!d.Has(9));
  // a hit before a tower's first scan counts if it is recent (1 s), not otherwise
  TowerDefend fresh, late;
  ShaperHitHistory h2;
  h2.Record(Hit(7, 4, 1, 5.f, 0.f, 5000));
  CHECK(fresh.Scan(h2, 1, tower, 2.76f, 28.f, 5900) == 1);
  CHECK(late.Scan(h2, 1, tower, 2.76f, 28.f, 6100) == 0);
  // a lock whose shaper left the list (dead, hidden) ends
  dist9 = -1.f;
  d.locked.push_back(9);
  CHECK(d.Release(28.f, onList).size() == 1 && d.locked.empty());
}

GMS_TEST(TowerOrderMinionsFirstUnlessDefended) {
  using namespace gms::agents;
  // the public server's case: a shaper first on a Binding's list (sticky, reported by a minion it hit) ahead of minions
  AggroCandidate shaper = Cand(1, 5.f, 0);
  shaper.isPlayer = true;
  shaper.sticky = true;
  shaper.otherReportAgeMs = 200;
  AggroCandidate m1 = Cand(2, 20.f, 1), m2 = Cand(3, 12.f, 2);
  std::vector<AggroCandidate> v = {shaper, m1, m2};
  OrderAggro(&v, kAggroTower);  // the old order
  CHECK(v[0].id == 1);
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);  // TOWERAGGRO: every non-player first, the nearer first
  CHECK(Ids(v) == (std::vector<unsigned>{3, 2, 1}));
  // stickiness only within a class: the sticky minion stays ahead of a nearer one
  for (auto& c : v) c.sticky = c.id == 2;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);
  CHECK(Ids(v) == (std::vector<unsigned>{2, 3, 1}));
  // a player's report (a shaper hit by it) doesn't lift it over minions either; the defend lock does
  for (auto& c : v)
    if (c.id == 1) c.playerReportAgeMs = 100;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);
  CHECK(v[2].id == 1);
  for (auto& c : v) c.defend = c.id == 1;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);
  CHECK(v[0].id == 1);
  // a taunt first of all
  AggroCandidate t = Cand(4, 27.f, 3);
  t.taunting = true;
  v.push_back(t);
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);
  CHECK(v[0].id == 4 && v[1].id == 1);
}

// ---- TOWERSTICK: a tower keeps the minion it shoots ----

GMS_TEST(TowerStickKeepsMinionUntilGone) {
  using namespace gms::agents;
  // the player's report: the Binding shot the first minion in (m1, 20 units: its passive's OnTargetEnterVolume), then
  // took a nearer one (m2) or one a call for help named (m3) without killing m1
  AggroCandidate m1 = Cand(1, 20.f, 0), m2 = Cand(2, 8.f, 1), m3 = Cand(3, 15.f, 2);
  m1.sticky = true;               // what the tower shoots
  m2.otherReportAgeMs = 300;      // an allied minion it hits called for help
  m3.playerReportAgeMs = 500;     // it hit an allied shaper near the tower
  AggroCandidate s = Cand(4, 5.f, 3);  // an enemy shaper, nearest, not defended
  s.isPlayer = true;
  std::vector<AggroCandidate> v = {m1, m2, m3, s};
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);  // TOWERAGGRO's order: the player's report first
  CHECK(Ids(v) == (std::vector<unsigned>{3, 1, 2, 4}));
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);  // TOWERSTICK: the current target first
  CHECK(Ids(v) == (std::vector<unsigned>{1, 3, 2, 4}));
  // without the reports the old order took the nearer one (the first minion in wasn't the update's best target)
  for (auto& c : v) c.playerReportAgeMs = c.otherReportAgeMs = -1;
  for (auto& c : v) c.sticky = false;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true);
  CHECK(v[0].id == 2);
  for (auto& c : v) c.sticky = c.id == 1;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(Ids(v) == (std::vector<unsigned>{1, 2, 3, 4}));
  // m1 dies (off the list): the nearer next, the shaper last
  v.erase(v.begin());
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(Ids(v) == (std::vector<unsigned>{2, 3, 4}));
}

GMS_TEST(TowerStickYieldsToDefendAndTaunt) {
  using namespace gms::agents;
  AggroCandidate m1 = Cand(1, 20.f, 0), m2 = Cand(2, 8.f, 1);
  m1.sticky = true;
  AggroCandidate s = Cand(3, 25.f, 2);
  s.isPlayer = true;
  std::vector<AggroCandidate> v = {m1, m2, s};
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(Ids(v) == (std::vector<unsigned>{1, 2, 3}));
  // the defend rule (the shaper hit an allied shaper within the tower's range) still switches
  for (auto& c : v) c.defend = c.id == 3;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(v[0].id == 3);
  bool first = false;
  CHECK(AggroDecide(v[0], v[1], true, kCallForHelpTimeMs, true, true, &first) == kRuleDefend && first);
  // a taunt before everything
  for (auto& c : v) c.defend = false;
  for (auto& c : v) c.taunting = c.id == 2;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(v[0].id == 2);
  // a sticky shaper doesn't hold against minions (stickiness within a class), and among shapers the old rules stay
  for (auto& c : v) c.taunting = false;
  for (auto& c : v) c.sticky = c.id == 3;
  OrderAggro(&v, kAggroTower, kCallForHelpTimeMs, true, true);
  CHECK(v[2].id == 3);
  AggroCandidate s1 = Cand(5, 10.f, 0), s2 = Cand(6, 12.f, 1);
  s1.isPlayer = s2.isPlayer = true;
  s1.sticky = true;
  s2.playerReportAgeMs = 100;
  CHECK(AggroDecide(s2, s1, true, kCallForHelpTimeMs, true, true, &first) == kRulePlayerReport && first);
}

GMS_TEST(TowerStickRulesAndChanges) {
  using namespace gms::agents;
  AggroCandidate a = Cand(1, 20.f, 0), b = Cand(2, 8.f, 1);
  bool first = true;
  CHECK(AggroDecide(a, b, true, kCallForHelpTimeMs, true, false, &first) == kRuleNearer && !first);
  a.sticky = true;
  b.playerReportAgeMs = 0;
  CHECK(AggroDecide(b, a, true, kCallForHelpTimeMs, true, false, &first) == kRulePlayerReport && first);
  CHECK(AggroDecide(b, a, true, kCallForHelpTimeMs, true, true, &first) == kRuleStickyFirst && !first);
  // stickFirst needs strict mode (the minions' and other agents' orders don't change)
  CHECK(AggroDecide(b, a, true, kCallForHelpTimeMs, false, true, &first) == kRulePlayerReport && first);
  CHECK(std::string(AggroRuleName(kRuleStickyFirst)) == "the current target (stick)");
  CHECK(std::string(AggroRuleName(99)) == "?");
  // the current target: the shot target while on the list, else the last best target; without stick the best target
  CHECK(TowerStickyId(true, 7, true, 9) == 7);
  CHECK(TowerStickyId(true, 7, false, 9) == 9);
  CHECK(TowerStickyId(true, 0, false, 9) == 9);
  CHECK(TowerStickyId(false, 7, true, 9) == 9);
  // the change classes and the broken case
  CHECK(ClassifyTowerChange(0, 5, false) == kTowerKeep);
  CHECK(ClassifyTowerChange(5, 5, true) == kTowerKeep);
  CHECK(ClassifyTowerChange(5, 6, false) == kTowerOldGone);
  CHECK(ClassifyTowerChange(5, 6, true) == kTowerOffLiving);
  CHECK(TowerStickBroken(kTowerOffLiving, false, false, 6));
  CHECK(!TowerStickBroken(kTowerOffLiving, false, true, 6));   // for a shaper (defend, taunt)
  CHECK(!TowerStickBroken(kTowerOffLiving, true, false, 6));   // a shaper left for a minion
  CHECK(!TowerStickBroken(kTowerOldGone, false, false, 6));    // the old one died or left
  CHECK(!TowerStickBroken(kTowerOffLiving, false, false, 0));  // nothing new
}

GMS_TEST(LeashOwnAndTarget) {
  using namespace gms::agents;
  LeashOptions o;  // 15 own, +4 reach, home 6, 3 s chase
  LeashState s;
  CHECK(LeashSelfCheck(o, s, 14.9f, 0) == kLeashKeep);
  CHECK(LeashSelfCheck(o, s, 15.1f, 0) == kLeashOwn);
  CHECK(!LeashTargetBeyond(o, 19.f));
  CHECK(LeashTargetBeyond(o, 19.1f));
  o.enabled = false;  // the old rule: the tether only
  CHECK(LeashSelfCheck(o, s, 40.f, 0) == kLeashKeep);
  CHECK(!LeashTargetBeyond(o, 40.f));
}

GMS_TEST(LeashChaseTime) {
  using namespace gms::agents;
  LeashOptions o;
  LeashState s;
  UpdateLeash(&s, o, 5, 2.f, 1000);  // a fight at home: no clock
  CHECK(s.target == 5 && s.sinceMs == 1000 && s.outSinceMs == -1);
  CHECK(LeashSelfCheck(o, s, 2.f, 9000) == kLeashKeep);
  UpdateLeash(&s, o, 5, 8.f, 2000);  // out of its home radius: the clock runs
  CHECK(s.outSinceMs == 2000);
  CHECK(LeashSelfCheck(o, s, 8.f, 4999) == kLeashKeep);
  NoteLeashHit(&s, 5, 4000);  // it hits its target: the clock starts over
  NoteLeashHit(&s, 6, 4500);  // a hit on someone else doesn't count
  CHECK(LeashSelfCheck(o, s, 8.f, 6999) == kLeashKeep);
  CHECK(LeashSelfCheck(o, s, 8.f, 7000) == kLeashChase);
  CHECK(LeashSelfCheck(o, s, 5.f, 9000) == kLeashKeep);  // back within its home radius
  UpdateLeash(&s, o, 5, 5.f, 7500);  // home again: the out clock stops
  CHECK(s.outSinceMs == -1);
  UpdateLeash(&s, o, 7, 9.f, 8000);  // another target: a new fight
  CHECK(s.target == 7 && s.sinceMs == 8000 && s.outSinceMs == 8000 && s.lastHitMs == -1);
  UpdateLeash(&s, o, 0, 9.f, 8500);  // nothing: no fight
  CHECK(s.target == 0 && s.sinceMs == -1 && LeashSelfCheck(o, s, 9.f, 20000) == kLeashKeep);
  o.chaseMs = 0;  // no time limit
  UpdateLeash(&s, o, 5, 9.f, 9000);
  CHECK(LeashSelfCheck(o, s, 9.f, 60000) == kLeashKeep);
}

GMS_TEST(LeashGroupDropsAndPlatform) {
  using namespace gms::agents;
  LeashGroupDrops g;
  g.Drop(100, 5, 3000);
  CHECK(g.Ignored(100, 5, 2999) && !g.Ignored(100, 5, 3000));
  CHECK(!g.Ignored(101, 5, 1000) && !g.Ignored(100, 6, 1000));  // another camp, another target
  g.Drop(100, 5, 2000);  // an earlier end doesn't shorten it
  CHECK(g.Ignored(100, 5, 2500));
  g.Expire(3000);
  CHECK(g.Size() == 0);
  const float centre[3] = {10.f, 20.f, 0.f};
  const float spots[9] = {32.f, 20.f, 0.f, 10.f, -2.5f, 0.f, 0.f, 20.f, 0.f};
  CHECK(PlatformRadius(centre, spots, 3) > 22.49f && PlatformRadius(centre, spots, 3) < 22.51f);
  CHECK(PlatformRadius(centre, spots, 0) == 0.f);
}
