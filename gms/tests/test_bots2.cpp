// gms_tests: BOTS2, the second bot brain's decision layer (core/bots2): the tuning table and its overrides, the states
// and their order, the hysteresis and the commitment, the structures' safety, the push, the defence, the jungle, the
// never-idle rule over random worlds, and the census.
#include <cstdint>
#include <string>
#include <vector>

#include "core/bots2/brain.h"
#include "core/bots2/census.h"
#include "core/matchrec/matchrec.h"
#include "tests/check.h"

using namespace gms::bots2;

namespace {

// SM_Map_v5's shape: two lanes from team 1's base (-226, -226) to team 2's (226, 226), the Top lane through y > x
// (blue_top's knots), the Bottom lane its mirror; the Loci and the Guardians on the diagonal.
World MapWorld(double now = 300.0) {
  World w;
  w.now = now;
  const float top[][2] = {{-210.5f, -184.9f}, {-171.7f, -146.0f}, {-153.1f, -114.8f}, {-131.6f, -44.2f},
                          {-106.4f, 28.7f},   {-69.6f, 68.0f},    {-24.2f, 110.1f},   {43.5f, 131.4f},
                          {114.9f, 152.7f},   {144.7f, 165.5f}};
  gms::bots::LanePath t, b;
  t.lane = gms::bots::kLaneTop;
  b.lane = gms::bots::kLaneBottom;
  for (const auto& k : top) {
    t.knots.push_back(V(k[0], k[1]));
    b.knots.push_back(V(k[1], k[0]));
  }
  w.lanes = {t, b};
  for (int team = 1; team <= 2; ++team) {
    const float s = team == 1 ? -1.f : 1.f;
    TeamBase& base = w.base[team];
    base.ok = true;
    base.spawn = V(226.f * s, 226.f * s);
    base.hasLocus = true;
    base.locus = V(242.f * s, 242.f * s);
    base.hasGuardian = true;
    base.guardian = V(182.f * s, 182.f * s);
  }
  return w;
}

Shaper& AddShaper(World& w, std::uint32_t id, int team, float x, float y, float hpFrac = 1.f, bool bot = true,
                  int lane = gms::bots::kLaneTop) {
  Shaper s;
  s.id = id;
  s.team = team;
  s.pos = V(x, y);
  s.maxHp = 1000.f;
  s.hp = 1000.f * hpFrac;
  s.level = 6;
  s.alive = hpFrac > 0.f;
  s.seenBy[1] = s.seenBy[2] = true;
  s.bot = bot;
  s.lane = lane;
  w.shapers.push_back(s);
  return w.shapers.back();
}

Unit& AddMinion(World& w, std::uint32_t id, int team, float x, float y, float hp = 300.f) {
  Unit u;
  u.id = id;
  u.team = team;
  u.npc = kNpcMelee;
  u.pos = V(x, y);
  u.hp = hp;
  u.maxHp = 400.f;
  w.minions.push_back(u);
  return w.minions.back();
}

Structure& AddStructure(World& w, std::uint32_t id, int team, Kind kind, float x, float y, float reach) {
  Structure s;
  s.id = id;
  s.team = team;
  s.kind = kind;
  s.pos = V(x, y);
  s.hp = s.maxHp = 3000.f;
  s.reach = reach;
  w.structures.push_back(s);
  return w.structures.back();
}

Intent Think(Brain& b, const World& w, std::uint32_t id) {
  b.PlanTeams(w);
  return b.Decide(w, id);
}

// BOTS2c: SM_Map_v5's wells (well 0 and 2 team 2's, 1 and 3 team 1's), unlocked unless asked
Well& AddWell(World& w, std::uint32_t id, int owner, float x, float y, bool locked = false, int workers = 0) {
  Well wl;
  wl.id = id;
  wl.pos = V(x, y);
  wl.owner = owner;
  wl.locked = locked;
  wl.workers = workers;
  w.wells.push_back(wl);
  return w.wells.back();
}

Unit& AddWorker(World& w, std::uint32_t id, int team, float x, float y, float hp = 200.f) {
  Unit u;
  u.id = id;
  u.team = team;
  u.npc = kNpcWorker;
  u.pos = V(x, y);
  u.hp = hp;
  u.maxHp = 250.f;
  w.workers.push_back(u);
  return w.workers.back();
}

}  // namespace

GMS_TEST(Bots2TuningOverrides) {
  Tuning t;
  std::string bad;
  const int set = ApplyOverrides(&t, "retreatHp=0.4, groupAtS=600;siegeAllies=2 nonsense=1 engageRange=x", &bad);
  CHECK_EQ(set, 3);
  CHECK(t.retreatHp > 0.39f && t.retreatHp < 0.41f);
  CHECK(t.groupAtS > 599.f && t.groupAtS < 601.f);
  CHECK_EQ(t.siegeAllies, 2);
  CHECK_EQ(bad, std::string("nonsense=1,engageRange=x"));
  const std::string d = Describe(t);
  CHECK(d.find("retreatHp=0.4") != std::string::npos);
  CHECK(d.find("siegeAllies=2") != std::string::npos);
  int n = 0;
  const TuningField* f = TuningFields(&n);
  CHECK(n >= 40);
  for (int i = 0; i < n; ++i) CHECK((f[i].f != nullptr) != (f[i].i != nullptr));
}

GMS_TEST(Bots2DeadAndBase) {
  Brain b;
  World w = MapWorld();
  AddShaper(w, 1, 1, -100.f, 30.f, 0.f);  // dead
  CHECK(Think(b, w, 1).act == Act::kNone);
  CHECK(b.MemoryOf(1)->state == State::kDead);
  // respawned at the shop: shops (stands) shopStandS, heals, then leaves
  World w2 = MapWorld(301.0);
  AddShaper(w2, 1, 1, -226.f, -226.f, 1.f);
  Intent in = Think(b, w2, 1);
  CHECK(in.act == Act::kShop);
  CHECK(b.MemoryOf(1)->state == State::kBase);
  w2.now = 302.0;
  CHECK(Think(b, w2, 1).act == Act::kShop);  // 1 s of shopStandS's 1.5
  w2.now = 302.6;
  in = Think(b, w2, 1);
  CHECK(in.act == Act::kMove);  // shopped 1.5 s: out to the lane
  CHECK(b.MemoryOf(1)->state == State::kLane);
  // low HP in the base: stays to heal
  World w3 = MapWorld(400.0);
  AddShaper(w3, 2, 1, -224.f, -224.f, 0.5f);
  Think(b, w3, 2);
  w3.now = 402.0;
  CHECK(Think(b, w3, 2).act == Act::kShop);
  CHECK(std::string(Think(b, w3, 2).why) == "base: healing");
  // in the base but away from the shop: walks to it first
  World w4 = MapWorld(500.0);
  AddShaper(w4, 3, 1, -200.f, -215.f, 1.f);
  in = Think(b, w4, 3);
  CHECK(in.act == Act::kMove);
  CHECK(in.point.x == -226.f && in.point.y == -226.f);
}

GMS_TEST(Bots2RetreatHysteresisAndRecall) {
  Brain b;
  World w = MapWorld(900.0);  // the push phase: BOTS2's retreat (the laning phase's: Bots2cFewerRecalls)
  AddShaper(w, 1, 1, -100.f, 30.f, 0.25f);
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kRecall);  // nobody near: recall at once
  CHECK(b.MemoryOf(1)->retreating);
  // an enemy near: walk home instead
  Brain b2;
  World w2 = MapWorld(900.0);
  AddShaper(w2, 1, 1, -100.f, 30.f, 0.25f);
  AddShaper(w2, 9, 2, -90.f, 40.f, 1.f);
  in = Think(b2, w2, 1);
  CHECK(in.act == Act::kMove);
  CHECK(b2.MemoryOf(1)->state == State::kRetreat);
  // healing to 50 % keeps the retreat (hysteresis), 75 % ends it
  World w3 = MapWorld(901.0);
  AddShaper(w3, 1, 1, -110.f, 20.f, 0.5f);
  AddShaper(w3, 9, 2, -90.f, 40.f, 1.f);
  CHECK(Think(b2, w3, 1).act == Act::kMove);
  CHECK(b2.MemoryOf(1)->state == State::kRetreat);
  World w4 = MapWorld(902.0);
  AddShaper(w4, 1, 1, -110.f, 20.f, 0.75f);
  Think(b2, w4, 1);
  CHECK(!b2.MemoryOf(1)->retreating);
  // a recall broken by a hit: not asked again for recallRetryS, and not while hit in the last second
  Brain b3;
  World w5 = MapWorld(100.0);
  AddShaper(w5, 1, 1, -100.f, 30.f, 0.1f);  // below recallHp (the laning phase recalls below it only)
  CHECK(Think(b3, w5, 1).act == Act::kRecall);
  b3.NoteDamage(1, 50, false, 100.5);
  w5.now = 101.0;
  CHECK(Think(b3, w5, 1).act == Act::kMove);
  w5.now = 104.2;
  CHECK(Think(b3, w5, 1).act == Act::kRecall);
}

GMS_TEST(Bots2RecallChannelIsHeld) {
  Brain b;
  World w = MapWorld();
  Shaper& me = AddShaper(w, 1, 1, -100.f, 30.f, 0.9f);
  me.channelling = true;
  AddShaper(w, 9, 2, -95.f, 35.f, 1.f);  // an enemy near: still nothing else while the channel runs
  const Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kRecall);
  CHECK(b.MemoryOf(1)->state == State::kRecall);
}

// The push phase's (and a teamfight's) fight rules: even odds engage within engageRange, the commitment, the chase.
// (The laning phase's trades and kill chances: Bots2cLaningTradesAndKills.)
GMS_TEST(Bots2FightEngagesAndCommits) {
  Brain b;
  World w = MapWorld(900.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f);
  AddShaper(w, 9, 2, -85.f, 40.f, 0.8f);  // 18 away, one against one
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack);
  CHECK_EQ(in.target, 9u);
  CHECK(in.abilities);
  CHECK(b.MemoryOf(1)->state == State::kFight);
  // the target steps out of engageRange but within chaseRange of the engage point: still fought
  World w2 = MapWorld(901.0);
  AddShaper(w2, 1, 1, -98.f, 31.f, 0.9f);
  AddShaper(w2, 9, 2, -72.f, 45.f, 0.6f);
  in = Think(b, w2, 1);
  CHECK(in.act == Act::kAttack);
  CHECK_EQ(in.target, 9u);
  // past the chase range: dropped
  World w3 = MapWorld(902.0);
  AddShaper(w3, 1, 1, -98.f, 31.f, 0.9f);
  AddShaper(w3, 9, 2, -50.f, 70.f, 0.6f);
  in = Think(b, w3, 1);
  CHECK(in.act != Act::kAttack || in.target != 9u);
  // a low target gets the R
  Brain b2;
  World w4 = MapWorld(900.0);
  AddShaper(w4, 1, 1, -100.f, 30.f, 1.f);
  AddShaper(w4, 9, 2, -90.f, 35.f, 0.3f);
  in = Think(b2, w4, 1);
  CHECK(in.act == Act::kAttack && in.ult);
}

GMS_TEST(Bots2NoEngageOutnumbered) {
  Brain b;
  World w = MapWorld();
  AddShaper(w, 1, 1, -100.f, 30.f, 0.9f);
  AddShaper(w, 9, 2, -85.f, 40.f, 1.f);
  AddShaper(w, 8, 2, -82.f, 44.f, 1.f);
  AddShaper(w, 7, 2, -80.f, 38.f, 1.f);
  const Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kMove);  // three to one: backs off toward home (disengaging), no recall at 90 %
  CHECK(b.MemoryOf(1)->state == State::kRetreat);
  CHECK(b.MemoryOf(1)->disengaging && !b.MemoryOf(1)->retreating);
  // the enemies gone: still backing off for disengageMinS (no flicker), then back to the lane
  World w2 = MapWorld(301.0);
  AddShaper(w2, 1, 1, -110.f, 20.f, 0.9f);
  Think(b, w2, 1);
  CHECK(b.MemoryOf(1)->disengaging);
  w2.now = 302.5;
  Think(b, w2, 1);
  CHECK(!b.MemoryOf(1)->disengaging);
  CHECK(b.MemoryOf(1)->state == State::kLane);
}

GMS_TEST(Bots2GroupPushAndAdvantage) {
  // after groupAtS one bot alone at the rally point waits for the group (farms or walks there), three hit the Binding
  // without minions
  Brain b;
  World w = MapWorld(900.0);
  AddStructure(w, 50, 2, Kind::kBinding, -24.f, 110.f, 28.f);  // Top lane, team 2's nearest
  AddStructure(w, 51, 2, Kind::kBinding, 115.f, 152.f, 28.f);
  AddStructure(w, 52, 2, Kind::kBinding, 152.f, 115.f, 28.f);  // Bottom
  AddStructure(w, 53, 2, Kind::kBinding, 110.f, -24.f, 28.f);  // Bottom
  AddShaper(w, 1, 1, -75.f, 70.f, 1.f);
  Intent in = Think(b, w, 1);
  CHECK_EQ(b.Plan(1).pushLane, static_cast<int>(gms::bots::kLaneTop));
  CHECK_EQ(b.Plan(1).objective, 50u);
  CHECK(!b.Plan(1).grouped);
  CHECK(in.act == Act::kMove);  // alone: to the rally point (BOTS2c round 5; no minions there), no dive
  CHECK(std::string(in.why) == "push: gather at the rally point");  // its lane work would keep it far behind
  CHECK(Dist(b.Plan(1).rally, V(-24.f, 110.f)) > 28.f);  // outside the Binding's reach
  AddShaper(w, 2, 1, -72.f, 72.f, 1.f);
  AddShaper(w, 3, 1, -70.f, 68.f, 1.f);
  w.shapers[0].pos = b.Plan(1).rally;
  w.shapers[1].pos = Toward(b.Plan(1).rally, V(-100.f, 0.f), 5.f);
  w.shapers[2].pos = Toward(b.Plan(1).rally, V(-100.f, 0.f), 8.f);
  w.now = 901.0;
  in = Think(b, w, 1);
  CHECK(b.Plan(1).grouped);
  CHECK(in.act == Act::kAttack && in.target == 50u);
  CHECK(b.MemoryOf(1)->state == State::kPush);
  // the bot the Binding shoots steps out of its reach at any HP (the group stays and goes on hitting)
  w.structures[0].target = 1;
  w.shapers[0].pos = Toward(V(-24.f, 110.f), b.Plan(1).rally, 20.f);
  w.now = 903.0;
  in = Think(b, w, 1);
  CHECK(b.Plan(1).grouped);
  CHECK(in.act == Act::kMove);
  CHECK(std::string(in.why) == "retreat: out of a structure's fire");
  CHECK_EQ(b.Stats().towerEscapes, 1u);
  in = b.Decide(w, 2);
  CHECK(in.act == Act::kAttack && in.target == 50u);
  // ... unless it finishes a low enemy shaper right beside it
  AddShaper(w, 9, 2, w.shapers[0].pos.x + 4.f, w.shapers[0].pos.y, 0.15f);
  w.now = 904.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
  // the advantage: four living against two before groupAtS pushes at once, with a group of two
  Brain b2;
  World w2 = MapWorld(400.0);
  AddStructure(w2, 50, 2, Kind::kBinding, -24.f, 110.f, 28.f);
  AddStructure(w2, 52, 2, Kind::kBinding, 110.f, -24.f, 28.f);
  AddShaper(w2, 1, 1, -75.f, 70.f, 1.f);
  AddShaper(w2, 2, 1, -150.f, -110.f, 1.f, true, gms::bots::kLaneBottom);
  AddShaper(w2, 3, 1, -200.f, -200.f, 1.f);
  AddShaper(w2, 4, 1, -190.f, -210.f, 1.f);
  AddShaper(w2, 9, 2, 200.f, 200.f, 0.f);
  AddShaper(w2, 8, 2, 210.f, 200.f, 0.f);
  AddShaper(w2, 7, 2, 0.f, 150.f, 1.f);
  AddShaper(w2, 6, 2, 150.f, 0.f, 1.f);
  Think(b2, w2, 1);
  CHECK(b2.Plan(1).push && b2.Plan(1).advantage);
  CHECK_EQ(b2.Plan(1).groupNeeded, 2);
  CHECK(!b2.Plan(2).push);
  CHECK(b2.MemoryOf(1)->state == State::kPush);
}

GMS_TEST(Bots2GoldRecallQuiet) {
  Brain b;
  World w = MapWorld(500.0);
  Shaper& me = AddShaper(w, 1, 1, -100.f, 30.f, 0.6f);
  me.gold = 1500.f;
  AddMinion(w, 30, 2, -95.f, 38.f);  // an enemy minion 9.4 away: no recall
  Intent in = Think(b, w, 1);
  CHECK(in.act != Act::kRecall);
  w.minions.clear();
  w.now = 501.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kRecall);
  // hit a second ago: no recall
  Brain b2;
  World w2 = MapWorld(500.0);
  Shaper& me2 = AddShaper(w2, 1, 1, -100.f, 30.f, 0.6f);
  me2.gold = 1500.f;
  Think(b2, w2, 1);
  b2.NoteDamage(1, 77, false, 500.5);
  w2.now = 501.5;
  CHECK(Think(b2, w2, 1).act != Act::kRecall);
}

GMS_TEST(Bots2FleesOutnumberedWhenHurt) {
  Brain b;
  World w = MapWorld();
  AddShaper(w, 1, 1, -100.f, 30.f, 0.5f);
  AddShaper(w, 9, 2, -85.f, 40.f, 1.f);
  AddShaper(w, 8, 2, -82.f, 44.f, 1.f);
  Think(b, w, 1);
  CHECK(b.MemoryOf(1)->retreating);
}

GMS_TEST(Bots2HitBackAndNoDiveUnderTower) {
  Brain b;
  World w = MapWorld();
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f);
  // the enemy stands under its own Binding (reach 28): not engaged
  AddStructure(w, 50, 2, Kind::kBinding, -70.f, 60.f, 28.f);
  AddShaper(w, 9, 2, -85.f, 45.f, 0.8f);
  Intent in = Think(b, w, 1);
  CHECK(in.act != Act::kAttack || in.target != 9u);
  // low enough for a dive: engaged
  Brain b2;
  World w2 = MapWorld();
  AddShaper(w2, 1, 1, -100.f, 30.f, 1.f);
  AddStructure(w2, 50, 2, Kind::kBinding, -70.f, 60.f, 28.f);
  AddShaper(w2, 9, 2, -85.f, 45.f, 0.2f);
  in = Think(b2, w2, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
  // an enemy 27 away that just hit the bot is fought back
  Brain b3;
  World w3 = MapWorld();
  AddShaper(w3, 1, 1, -100.f, 30.f, 1.f);
  AddShaper(w3, 9, 2, -100.f, 57.f, 1.f);
  Think(b3, w3, 1);  // makes the memory
  b3.NoteDamage(1, 9, true, 300.0);
  in = Think(b3, w3, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
}

GMS_TEST(Bots2LaneFarmsAndKeepsOutOfTowers) {
  Brain b;
  World w = MapWorld();
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f);
  AddMinion(w, 20, 1, -95.f, 40.f);
  AddMinion(w, 30, 2, -90.f, 45.f, 200.f);
  AddMinion(w, 31, 2, -91.f, 46.f, 80.f);
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack);
  CHECK_EQ(in.target, 31u);  // the lowest HP first
  CHECK(!in.abilities);
  // the target is kept for retargetS even when another gets lower
  w.now = 300.5;
  w.minions[1].hp = 10.f;
  in = Think(b, w, 1);
  CHECK_EQ(in.target, 31u);
  // an enemy minion under an enemy Binding without own minions there: left alone
  Brain b2;
  World w2 = MapWorld();
  AddShaper(w2, 1, 1, -100.f, 30.f, 1.f);
  AddStructure(w2, 50, 2, Kind::kBinding, -80.f, 60.f, 28.f);
  AddMinion(w2, 30, 2, -91.f, 46.f, 80.f);
  in = Think(b2, w2, 1);
  CHECK(in.act == Act::kMove);
  // own minions tank the Binding: the minion and then the Binding are fair game
  AddMinion(w2, 20, 1, -82.f, 52.f);
  AddMinion(w2, 21, 1, -84.f, 55.f);
  in = Think(b2, w2, 1);
  CHECK(in.act == Act::kAttack && in.target == 30u);
  w2.minions.erase(w2.minions.begin());  // the enemy minion died
  w2.now = 302.0;
  in = Think(b2, w2, 1);
  CHECK(in.act == Act::kAttack && in.target == 50u);
  // ... unless the Binding shoots the bot
  w2.structures[0].target = 1;
  w2.now = 304.0;
  in = Think(b2, w2, 1);
  CHECK(!(in.act == Act::kAttack && in.target == 50u));
}

GMS_TEST(Bots2LaneWalksWithTheFront) {
  // BOTS2c: the Gladiator laneCarryBack behind its own minion and laneSide to one side, its mate laneSupportBack
  // behind and to the other side
  Brain b;
  World w = MapWorld(200.0);
  AddShaper(w, 1, 1, -112.f, 0.f, 1.f).role = kRoleGladiator;
  AddShaper(w, 2, 1, -114.f, -2.f, 1.f).role = 1;  // a Tactician
  AddMinion(w, 20, 1, -110.f, 15.f);
  AddShaper(w, 8, 2, 200.f, 200.f, 1.f);  // two enemies alive far away: no advantage push
  AddShaper(w, 9, 2, 205.f, 200.f, 1.f);
  const Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kMove);
  CHECK(b.MemoryOf(1)->state == State::kLane);
  CHECK(Dist(in.point, V(-110.f, 15.f)) <= 10.f);  // behind the own minion, on the lane
  CHECK(in.point.y < 15.f);
  const Intent mate = b.Decide(w, 2);
  CHECK(mate.act == Act::kMove);
  CHECK(Dist(mate.point, V(-110.f, 15.f)) > Dist(in.point, V(-110.f, 15.f)) + 3.f);
  CHECK(Dist(mate.point, in.point) >= 8.f);  // apart, never one point
  // two bots of one role on a lane still get two slots (by id)
  w.shapers[1].role = kRoleGladiator;
  const LaneSlot s1 = LaneSlotOf(w, w.shapers[0], b.tuning()), s2 = LaneSlotOf(w, w.shapers[1], b.tuning());
  CHECK(s1.rank == 0 && s2.rank == 1);
  CHECK(s1.side != s2.side && s2.back > s1.back);
}

GMS_TEST(Bots2PushAfterGroupTime) {
  Brain b;
  World w = MapWorld(900.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f, true, gms::bots::kLaneTop);
  // the Top lane has two enemy Bindings, the Bottom lane one: push Bottom
  AddStructure(w, 50, 2, Kind::kBinding, -24.f, 110.f, 28.f);
  AddStructure(w, 51, 2, Kind::kBinding, 115.f, 152.f, 28.f);
  AddStructure(w, 52, 2, Kind::kBinding, 152.f, 115.f, 28.f);
  Think(b, w, 1);
  CHECK(b.Plan(1).push);
  CHECK_EQ(b.Plan(1).pushLane, static_cast<int>(gms::bots::kLaneBottom));
  CHECK(b.MemoryOf(1)->state == State::kPush);
  // held: a change of the counts within pushLaneHoldS doesn't move it (the Top lane's two fall)
  w.structures.erase(w.structures.begin(), w.structures.begin() + 2);
  w.now = 950.0;
  Think(b, w, 1);
  CHECK_EQ(b.Plan(1).pushLane, static_cast<int>(gms::bots::kLaneBottom));
  w.now = 991.0;
  Think(b, w, 1);
  CHECK_EQ(b.Plan(1).pushLane, static_cast<int>(gms::bots::kLaneTop));
  // before groupAtS: no push
  Brain b2;
  World w2 = MapWorld(600.0);
  AddShaper(w2, 1, 1, -100.f, 30.f, 1.f);
  Think(b2, w2, 1);
  CHECK(!b2.Plan(1).push);
  CHECK(b2.MemoryOf(1)->state == State::kLane);
}

GMS_TEST(Bots2SiegeCoresThenGuardian) {
  Brain b;
  World w = MapWorld(1200.0);
  // three bots of team 1 at team 2's Core A; the Guardian shielded while a Core lives
  AddShaper(w, 1, 1, 135.f, 135.f, 1.f);
  AddShaper(w, 2, 1, 138.f, 132.f, 1.f);
  AddShaper(w, 3, 1, 132.f, 138.f, 1.f);
  AddStructure(w, 60, 2, Kind::kCore, 149.2f, 149.5f, 0.f);
  Structure& g = AddStructure(w, 70, 2, Kind::kGuardian, 182.f, 182.f, 29.f);
  g.shielded = true;
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 60u);
  // the Core dies, the shield drops: the Guardian (round 11: the group stages outside its reach first, then goes in)
  w.structures.erase(w.structures.begin());
  w.structures[0].shielded = false;
  w.now = 1202.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 70u);  // round 12: in together, on to the Guardian without a regather
  // shielded: never
  w.structures[0].shielded = true;
  w.now = 1204.0;
  in = Think(b, w, 1);
  CHECK(!(in.act == Act::kAttack && in.target == 70u));
  // alone at the Guardian (no allies, no minions): not hit
  Brain b2;
  World w2 = MapWorld(1200.0);
  AddShaper(w2, 1, 1, 150.f, 150.f, 1.f);
  AddStructure(w2, 70, 2, Kind::kGuardian, 182.f, 182.f, 29.f);
  in = Think(b2, w2, 1);
  CHECK(!(in.act == Act::kAttack && in.target == 70u));
}

GMS_TEST(Bots2LocusKeptClear) {
  const World w = MapWorld();
  Tuning t;
  const Vec3 p = AwayFromLocus(w, 1, V(230.f, 230.f), t);
  CHECK(Dist(p, V(242.f, 242.f)) >= t.locusAvoid - 0.01f);
  const Vec3 q = AwayFromLocus(w, 1, V(0.f, 10.f), t);
  CHECK(q.x == 0.f && q.y == 10.f);
}

GMS_TEST(Bots2Defends) {
  Brain b;
  World w = MapWorld(500.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f);
  AddShaper(w, 2, 1, -150.f, -150.f, 1.f);
  AddStructure(w, 60, 1, Kind::kCore, -149.5f, -149.5f, 0.f);
  AddMinion(w, 30, 2, -140.f, -130.f);
  Intent in = Think(b, w, 2);
  CHECK(b.Plan(1).threatened);
  CHECK(in.act == Act::kAttack && in.target == 30u);
  CHECK(b.MemoryOf(2)->state == State::kDefend);
  // the threat holds threatHoldS after the minion died
  w.minions.clear();
  w.now = 502.0;
  Think(b, w, 2);
  CHECK(b.Plan(1).threatened);
  w.now = 504.0;
  Think(b, w, 2);
  CHECK(!b.Plan(1).threatened);
}

GMS_TEST(Bots2JungleOwnSideFirst) {
  Brain b;
  World w = MapWorld(120.0);
  Shaper& h = AddShaper(w, 1, 1, -10.f, -30.f, 1.f);
  h.role = kRoleHunter;
  // an own-side camp 60 away and an enemy-side camp 56 away: the own one (the enemy half costs campPenalty more)
  Unit c1;
  c1.id = 40;
  c1.pos = V(-70.f, -30.f);
  c1.hp = c1.maxHp = 500.f;
  c1.npc = kNpcCreep;
  Unit c2 = c1;
  c2.id = 41;
  c2.pos = V(15.f, 20.f);
  w.creeps = {c1, c2};
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kMove);
  CHECK(Dist(in.point, V(-70.f, -30.f)) < b.tuning().walkSpread + 0.5f);  // BOTS2c: a long walk's spread
  CHECK(b.MemoryOf(1)->state == State::kJungle);
  // at the camp: its weakest creature, with the kit
  w.shapers[0].pos = V(-60.f, -30.f);
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 40u && in.abilities);
  // camps grouped by distance
  World w2 = MapWorld();
  w2.creeps = {c1, c1, c2};
  w2.creeps[1].id = 42;
  w2.creeps[1].pos = V(-62.f, -28.f);
  const std::vector<Camp> camps = Camps(w2, 14.f);
  CHECK_EQ(camps.size(), static_cast<std::size_t>(2));
}

// The never-idle rule over random worlds: a living bot outside its base always moves, attacks or recalls.
GMS_TEST(Bots2NeverIdleRandomWorlds) {
  std::uint32_t seed = 12345u;
  auto rnd = [&seed]() {
    seed = seed * 1664525u + 1013904223u;
    return static_cast<float>((seed >> 8) & 0xFFFF) / 65535.f;
  };
  Brain b;
  unsigned decided = 0;
  unsigned extras[static_cast<int>(ExtraKind::kCount)] = {};
  for (int round = 0; round < 400; ++round) {
    World w = MapWorld(rnd() * 1800.0);
    const int n = 2 + static_cast<int>(rnd() * 9.f);
    for (int i = 0; i < n; ++i) {
      Shaper& s = AddShaper(w, static_cast<std::uint32_t>(i + 1), i % 2 + 1, rnd() * 440.f - 220.f, rnd() * 440.f - 220.f,
                            rnd() < 0.1f ? 0.f : 0.05f + rnd() * 0.95f, true, rnd() < 0.5f ? 1 : 2);
      s.gold = rnd() * 4000.f;
      s.level = 1 + static_cast<int>(rnd() * 17.f);
      s.role = rnd() < 0.2f ? kRoleHunter : 0;
      s.seenBy[s.team == 1 ? 2 : 1] = rnd() < 0.7f;
      s.channelling = rnd() < 0.05f;
      // BOTS2c: spells, potions, the Predator
      if (rnd() < 0.2f) s.role = kRolePredator;
      for (int k = 0; k < 3; ++k)
        s.spells[k] = static_cast<Spell>(static_cast<int>(rnd() * static_cast<float>(Spell::kCount)) %
                                         static_cast<int>(Spell::kCount));
      if (rnd() < 0.5f) {
        s.potions = 1 + static_cast<int>(rnd() * 4.f);
        s.potionSlot = kExtraSlotConsume1 + (rnd() < 0.5f ? 0 : 1);
      }
    }
    // BOTS2c: wells (some taken, some locked, captures running), workers, wards, ward spots and the parasite
    for (int i = 0; i < 4; ++i) {
      Well& wl = AddWell(w, 600u + static_cast<std::uint32_t>(i), i % 2 + 1, rnd() * 440.f - 220.f,
                         rnd() * 440.f - 220.f, rnd() < 0.4f, static_cast<int>(rnd() * 18.f));
      if (rnd() < 0.3f) wl.owner = 3 - wl.owner;
      wl.capturers = rnd() < 0.3f ? 1 + static_cast<int>(rnd() * 3.f) : 0;
      wl.moving = wl.capturers > 0 && rnd() < 0.6f;
      wl.toCapture = wl.moving ? rnd() * 16.0 : -1.0;
      wl.workerHit = rnd() < 0.2f;
      for (int k = 0; k < 3; ++k)
        AddWorker(w, 700u + static_cast<std::uint32_t>(i * 3 + k), wl.owner, wl.pos.x + rnd() * 40.f - 20.f,
                  wl.pos.y + rnd() * 40.f - 20.f, 10.f + rnd() * 240.f);
    }
    for (int i = 0; i < 4; ++i) {
      Unit ward;
      ward.id = 800u + static_cast<std::uint32_t>(i);
      ward.team = i % 2 + 1;
      ward.npc = kNpcWard;
      ward.pos = V(rnd() * 440.f - 220.f, rnd() * 440.f - 220.f);
      w.wards.push_back(ward);
    }
    int nSpots = 0;
    const float (*spots)[2] = DataWardSpots(&nSpots);
    for (int i = 0; i < nSpots; ++i) w.wardSpots.push_back(V(spots[i][0], spots[i][1]));
    if (rnd() < 0.6f) {
      w.hasParasite = true;
      w.parasiteHittable = rnd() < 0.8f;
      w.parasite.id = 900;
      w.parasite.npc = kNpcParasite;
      w.parasite.pos = V(0.f, -1.f);
      w.parasite.hp = 1000.f + rnd() * 12000.f;
      w.parasite.maxHp = 13050.f;
    }
    for (int i = 0; i < 12; ++i)
      AddMinion(w, 100u + static_cast<std::uint32_t>(i), i % 2 + 1, rnd() * 440.f - 220.f, rnd() * 440.f - 220.f,
                10.f + rnd() * 390.f);
    for (int i = 0; i < 6; ++i) {
      Structure& s = AddStructure(w, 200u + static_cast<std::uint32_t>(i), i % 2 + 1,
                                  i < 4 ? Kind::kBinding : Kind::kCore, rnd() * 440.f - 220.f, rnd() * 440.f - 220.f,
                                  i < 4 ? 28.f : 0.f);
      if (rnd() < 0.3f) s.target = 1u + static_cast<std::uint32_t>(rnd() * 9.f);
    }
    if (rnd() < 0.5f) {
      Unit c;
      c.id = 300;
      c.pos = V(rnd() * 200.f - 100.f, rnd() * 200.f - 100.f);
      c.hp = c.maxHp = 400.f;
      w.creeps.push_back(c);
    }
    b.PlanTeams(w);
    for (const Shaper& s : w.shapers) {
      const Intent in = b.Decide(w, s.id);
      ++decided;
      if (IntentIdle(in, s.alive, InBase(w, s, b.tuning()))) {
        std::printf("    idle: round %d bot %u at (%.1f, %.1f) hp %.2f: %s (%s)\n", round, s.id,
                    static_cast<double>(s.pos.x), static_cast<double>(s.pos.y), static_cast<double>(s.Frac()),
                    ActName(in.act), in.why);
      }
      if (in.act == Act::kMove) CHECK(!(in.point.x == 0.f && in.point.y == 0.f));  // NULLPOS
      if (in.act == Act::kAttack) CHECK(in.target != 0u);
      // BOTS2c: an extra is well formed (its slot, an object for a targeted spell, a point never (0, 0)), and never
      // goes with a recall or the shop
      const Extra& e = in.extra;
      if (e.kind != ExtraKind::kNone) {
        ++extras[static_cast<int>(e.kind)];
        CHECK(in.act == Act::kMove || in.act == Act::kAttack);
        CHECK(s.alive);
        if (e.atPoint) CHECK(!(e.point.x == 0.f && e.point.y == 0.f));
      }
      if (e.kind == ExtraKind::kSpell) {
        CHECK(e.slot >= kExtraSlotSpell1 && e.slot <= kExtraSlotSpell1 + 2);
        CHECK(s.spells[e.slot - kExtraSlotSpell1] == e.spell);
        const SpellAim aim = DataOf(e.spell).aim;
        if (aim == SpellAim::kEnemyShaper || aim == SpellAim::kEnemyNpc) CHECK(e.target != 0u);
        if (aim == SpellAim::kPoint) CHECK(e.atPoint);
      }
      if (e.kind == ExtraKind::kPotion) CHECK(e.slot == s.potionSlot && s.potions > 0);
      if (e.kind == ExtraKind::kWard) CHECK(e.slot == kExtraSlotWard && e.atPoint);
      if (e.kind != ExtraKind::kNone) b.NoteExtra(s.id, e, rnd() < 0.7f ? 1 : -0x270F, w.now);
    }
  }
  CHECK(decided > 1000u);
  CHECK_EQ(b.Stats().idleViolations, 0u);
  // every kind of extra came up somewhere
  CHECK(extras[static_cast<int>(ExtraKind::kSpell)] > 0u);
  CHECK(extras[static_cast<int>(ExtraKind::kPotion)] > 0u);
  CHECK(extras[static_cast<int>(ExtraKind::kWard)] > 0u);
}

// The match recorder files v2's commands as a bot's: its walks (agents.cpp bot2io::FollowPoint, no request) as "move"
// with the point, its attacks, casts and recalls through the request path (BotRequest's `where`).
GMS_TEST(Bots2MatchRecorderCommands) {
  namespace mr = gms::matchrec;
  const float at[3] = {-110.f, 15.f, 2.f};
  const mr::Command move = mr::FromBotRequest(3, "BOTS2 follow point", mr::kGuidBotMoveToTarget, -1, 0, at, 1);
  CHECK(move.bot);
  CHECK_EQ(move.kind, std::string("move"));
  CHECK_EQ(move.source, std::string("bot:BOTS2 follow point"));
  CHECK(move.hasPos);
  const mr::Command cast = mr::FromBotRequest(3, "BOTS2 cast", 0, 4, 0x7C78012Du, at, 1);
  CHECK_EQ(cast.kind, std::string("ability"));
  CHECK_EQ(cast.source, std::string("bot:BOTS2 cast"));
  const mr::Command hit = mr::FromBotRequest(3, "BOTS2 attack", 0, 0, 0x7C78012Du, at, 5);
  CHECK_EQ(hit.kind, std::string("attack"));
  CHECK_EQ(hit.value, static_cast<std::int64_t>(5));
  // BOTS2c: a battle spell (slot 7-9) and the ward (slot 1) are the bot's ability commands, through the request path
  const mr::Command spell = mr::FromBotRequest(3, "BOTS2 spell", 0, kExtraSlotSpell1, 0x7C78012Du, at, 1);
  CHECK_EQ(spell.kind, std::string("ability"));
  CHECK_EQ(spell.slot, kExtraSlotSpell1);
  CHECK_EQ(spell.source, std::string("bot:BOTS2 spell"));
  const mr::Command ward = mr::FromBotRequest(3, "BOTS2 ward", 0, kExtraSlotWard, 0, at, 1);
  CHECK_EQ(ward.kind, std::string("ability"));
  CHECK(ward.hasPos && ward.target == 0u);
  // ... and a potion is its item command (a client's use: ItemTransaction type 3), no request
  const mr::Command potion = mr::FromBotItemUse(3, "BOTS2 potion", 0x11A0F947u, 9);
  CHECK(potion.bot);
  CHECK_EQ(potion.kind, std::string("item"));
  CHECK_EQ(potion.source, std::string("bot:BOTS2 potion"));
  CHECK_EQ(potion.slot, 9);
  CHECK_EQ(potion.ability, 0x11A0F947u);
  CHECK_EQ(potion.value, static_cast<std::int64_t>(3));
  const std::string line = mr::EncodeCommand(1000, potion);
  CHECK(line.find("item") != std::string::npos && line.find("bot:BOTS2 potion") != std::string::npos);
}

GMS_TEST(Bots2Census) {
  Census c;
  const float move = 1.f, hitS = 2.f;
  c.Sample(1, 1, true, false, V(0.f, 10.f), 100.0, 500.f, 300.f, 3, move, hitS);  // starts
  c.Sample(1, 1, true, false, V(0.2f, 10.f), 101.0, 500.f, 300.f, 3, move, hitS);  // still: idle
  c.Sample(1, 1, true, false, V(5.f, 10.f), 102.0, 500.f, 300.f, 3, move, hitS);   // moved
  c.NoteHit(1, 102.5);
  c.Sample(1, 1, true, false, V(5.f, 10.f), 103.0, 500.f, 300.f, 3, move, hitS);  // still but hitting
  c.Sample(1, 1, true, true, V(5.f, 10.f), 104.0, 500.f, 300.f, 3, move, hitS);   // in the base: not counted
  c.Sample(1, 1, false, false, V(5.f, 10.f), 105.0, 500.f, 300.f, 3, move, hitS);  // died
  c.Sample(1, 1, false, false, V(5.f, 10.f), 106.0, 500.f, 300.f, 3, move, hitS);  // dead: not counted
  const CensusBot& b = c.Bots().at(1);
  CHECK(b.idleS > 0.99 && b.idleS < 1.01);
  CHECK(b.awayS > 2.99 && b.awayS < 3.01);
  CHECK_EQ(b.deaths, 1);
  CHECK_EQ(c.Deaths(), 1);
}

// ---- BOTS2c: wells, battle spells, the parasite, wards and potions ----------------------------------------------------

GMS_TEST(Bots2cDataSpellsPotionsWardSpots) {
  CHECK(SpellFromItemName("Spell_Drain.Item") == Spell::kDrain);
  CHECK(SpellFromItemName("Spell_Blink.Item") == Spell::kBlink);
  CHECK(SpellFromItemName("Spell_Vanquish") == Spell::kVanquish);
  CHECK(SpellFromItemName("Spell_DrainX.Item") == Spell::kNone);
  CHECK(SpellFromItemName("Basic_Life.Item") == Spell::kNone);
  CHECK(SpellFromItemName(nullptr) == Spell::kNone);
  CHECK(IsPotionItemName("Consumable_Potion_HealingPotion.Item"));
  CHECK(!IsPotionItemName("Consumable_Flask_Vim.Item"));
  CHECK(DataOf(Spell::kVanquish).cooldownS > 74.f && DataOf(Spell::kVanquish).cooldownS < 76.f);
  CHECK(DataOf(Spell::kVanquish).aim == SpellAim::kEnemyNpc);
  CHECK(DataOf(Spell::kDrain).aim == SpellAim::kEnemyShaper);
  CHECK(DataOf(Spell::kBlink).aim == SpellAim::kPoint);
  for (int i = 1; i < static_cast<int>(Spell::kCount); ++i) {
    const SpellData& d = DataOf(static_cast<Spell>(i));
    CHECK(static_cast<int>(d.spell) == i);
    CHECK(d.cooldownS >= 75.f);
    CHECK(SpellFromItemName((std::string("Spell_") + d.name + ".Item").c_str()) == d.spell);
  }
  int n = 0;
  const float (*spots)[2] = DataWardSpots(&n);
  CHECK_EQ(n, 18);
  for (int i = 0; i < n; ++i) CHECK(!(spots[i][0] == 0.f && spots[i][1] == 0.f));  // NULLPOS
}

// An own-side well the enemy took: the nearest able bot retakes it (into the ring; hits the workers that come in) and
// goes back to its lane once it is the team's again; no new task for it within wellRetryS.
GMS_TEST(Bots2cWellRetake) {
  Brain b;
  World w = MapWorld(1000.0);
  AddWell(w, 900, 2, -182.3f, 27.9f);
  AddShaper(w, 1, 1, -150.f, 20.f, 1.f);
  AddShaper(w, 2, 1, -60.f, 120.f, 1.f);  // farther
  Intent in = Think(b, w, 1);
  CHECK_EQ(b.Plan(1).wellBot, 1u);
  CHECK_EQ(b.Plan(1).wellKind, static_cast<int>(TeamPlan::kWellRetake));
  CHECK(b.MemoryOf(1)->state == State::kWell);
  CHECK(in.act == Act::kMove && std::string(in.why) == "well: to retake it");
  CHECK(Dist(in.point, V(-182.3f, 27.9f)) < 9.f);  // the stand point (and a long walk's spread): inside the ring
  CHECK(Think(b, w, 2).act != Act::kNone);
  CHECK(b.MemoryOf(2)->state != State::kWell);  // one bot a task
  // in the ring: it stays (capturing), and the census counts that as an action
  w.shapers[0].pos = in.point;
  w.now = 1004.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kMove && std::string(in.why) == "well: capturing");
  CHECK(InWellAction(w, w.shapers[0]));
  CHECK(!InWellAction(w, w.shapers[1]));
  // a worker inside the ring is hit, one out on its spot is not (the capturer stays in the ring)
  AddWorker(w, 300, 2, -160.f, 30.f);  // 22 out
  CHECK(Think(b, w, 1).act == Act::kMove);
  AddWorker(w, 301, 2, -180.f, 31.f, 150.f);
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 301u);
  // taken: the task ends, the bot goes back to its lane, and isn't tasked again for wellRetryS
  w.wells[0].owner = 1;
  w.wells[0].locked = true;
  w.workers.clear();
  w.now = 1020.0;
  in = Think(b, w, 1);
  CHECK_EQ(b.Plan(1).wellBot, 0u);
  CHECK_EQ(std::string(b.Plan(1).wellEndWhy), std::string("taken"));
  CHECK(b.MemoryOf(1)->state != State::kWell);
  w.wells[0].owner = 2;
  w.wells[0].locked = false;
  w.now = 1030.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).wellBot != 1u);  // bot 2 may take it; bot 1 rests
  CHECK(b.Stats().wellTasks[TeamPlan::kWellRetake] >= 1u);
}

// An enemy well on its own side: only the well taker (the Predator, else the Hunter) takes it while it is unlocked, or
// raids its workers while it is locked; never with an enemy shaper near it.
GMS_TEST(Bots2cWellTakeAndRaid) {
  {
    Brain b;
    World w = MapWorld(1000.0);
    AddWell(w, 901, 2, 182.5f, -27.9f);
    AddShaper(w, 1, 1, 150.f, -60.f, 1.f, true, gms::bots::kLaneBottom).role = kRolePredator;
    AddShaper(w, 2, 1, 165.f, -35.f, 1.f, true, gms::bots::kLaneBottom);  // nearer, but not the taker
    CHECK_EQ(WellTaker(w, 1), 1u);
    const Intent in = Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellBot, 1u);
    CHECK_EQ(b.Plan(1).wellKind, static_cast<int>(TeamPlan::kWellTake));
    CHECK(in.act == Act::kMove && std::string(in.why) == "well: to take it");
  }
  {  // a well an enemy Binding covers: no task; a taker shot by a structure on its way gives the task up (round 1:
     // the walk and the tower escape took turns for 16 s)
    Brain b;
    World w = MapWorld(1000.0);
    AddWell(w, 901, 2, 182.5f, -27.9f);
    AddShaper(w, 1, 1, 150.f, -60.f, 1.f, true, gms::bots::kLaneBottom).role = kRolePredator;
    AddStructure(w, 70, 2, Kind::kBinding, 175.f, -20.f, 28.f);
    Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellBot, 0u);
    Brain b2;
    w.structures.clear();
    Think(b2, w, 1);
    CHECK_EQ(b2.Plan(1).wellBot, 1u);
    AddStructure(w, 71, 2, Kind::kBinding, 130.f, -60.f, 28.f).target = 1;  // shoots the taker
    w.now = 1001.0;
    CHECK(std::string(Think(b2, w, 1).why) == "retreat: out of a structure's fire");
    w.now = 1001.5;
    Think(b2, w, 1);
    CHECK_EQ(b2.Plan(1).wellBot, 0u);
    CHECK_EQ(std::string(b2.Plan(1).wellEndWhy), std::string("a structure's fire"));
  }
  {  // an enemy shaper near the well: no task
    Brain b;
    World w = MapWorld(1000.0);
    AddWell(w, 901, 2, 182.5f, -27.9f);
    AddShaper(w, 1, 1, 150.f, -60.f, 1.f, true, gms::bots::kLaneBottom).role = kRolePredator;
    AddShaper(w, 9, 2, 190.f, -10.f, 1.f);
    Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellBot, 0u);
  }
  {  // locked with workers: a raid on the lowest worker; it ends below raidEndWorkers
    Brain b;
    World w = MapWorld(400.0);
    AddWell(w, 901, 2, 182.5f, -27.9f, true, 10);
    AddShaper(w, 1, 1, 150.f, -60.f, 1.f, true, gms::bots::kLaneBottom).role = kRolePredator;
    AddWorker(w, 300, 2, 164.f, -20.f, 150.f);
    AddWorker(w, 301, 2, 170.f, -45.f, 80.f);
    AddWorker(w, 302, 2, 120.f, -45.f, 10.f);  // off the platform: not the raid's
    Intent in = Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellKind, static_cast<int>(TeamPlan::kWellRaid));
    CHECK(in.act == Act::kAttack && in.target == 301u && std::string(in.why) == "well: raid the workers");
    w.wells[0].workers = 1;
    w.now = 405.0;
    in = Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellBot, 0u);
    CHECK_EQ(std::string(b.Plan(1).wellEndWhy), std::string("raided"));
    // too few workers: no raid at all
    Brain b2;
    Think(b2, w, 1);
    CHECK_EQ(b2.Plan(1).wellBot, 0u);
  }
  {  // no Predator: the Hunter is the taker, but only once he has left his camps (hunterJoinS)
    World w = MapWorld(400.0);
    AddWell(w, 901, 2, 182.5f, -27.9f, true, 10);
    AddShaper(w, 1, 1, 150.f, -60.f, 1.f).role = kRoleHunter;
    AddShaper(w, 2, 1, 150.f, -60.f, 1.f).role = 0;
    CHECK_EQ(WellTaker(w, 1), 1u);
    Brain b;
    Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellBot, 0u);  // jungling
    w.now = 850.0;
    Brain b2;
    Think(b2, w, 1);
    CHECK_EQ(b2.Plan(1).wellBot, 1u);
    CHECK_EQ(b2.Plan(1).wellKind, static_cast<int>(TeamPlan::kWellRaid));
    w.shapers[0].alive = false;
    CHECK_EQ(WellTaker(w, 1), 0u);
  }
}

// An own well under capture: the nearest bot that gets there in time defends it (fights the capturer); none when the
// capture would be done first or when the enemies there are too many.
GMS_TEST(Bots2cWellDefence) {
  auto world = [](double toCapture, int enemies) {
    World w = MapWorld(1000.0);
    Well& wl = AddWell(w, 902, 1, -182.3f, 27.9f);
    wl.capturers = enemies;
    wl.moving = true;
    wl.toCapture = toCapture;
    for (int i = 0; i < enemies; ++i)
      AddShaper(w, 9 + static_cast<std::uint32_t>(i), 2, -180.f + static_cast<float>(i), 30.f, 1.f);
    AddShaper(w, 1, 1, -150.f, -20.f, 1.f);  // 58 away
    AddShaper(w, 2, 1, -60.f, -120.f, 1.f);  // 192 away: beyond wellDefendReach
    return w;
  };
  {
    Brain b;
    World w = world(10.0, 1);
    const Intent in = Think(b, w, 1);
    CHECK_EQ(b.Plan(1).wellThreat, 902u);
    CHECK_EQ(b.Plan(1).wellDefenders.size(), static_cast<std::size_t>(1));
    CHECK(in.act == Act::kAttack && in.target == 9u && std::string(in.why) == "well: the capturer");
    CHECK(b.MemoryOf(1)->state == State::kWell);
    CHECK(Think(b, w, 2).act != Act::kNone);
    CHECK(b.MemoryOf(2)->state != State::kWell);
  }
  {  // too late: (58 - 12) / 7 = 6.6 s against 2 + 3
    Brain b;
    World w = world(2.0, 1);
    Think(b, w, 1);
    CHECK(b.Plan(1).wellDefenders.empty());
  }
  {  // too many: 4 enemies there against 0 allies and 2 defenders
    Brain b;
    World w = world(10.0, 4);
    Think(b, w, 1);
    CHECK(b.Plan(1).wellDefenders.empty());
  }
}

// The parasite: from 15:00, after a won fight (parasiteLead more living shapers), not during the base's siege; the
// group gathers outside its reach and hits it together, never alone; the attempt ends when it is down.
GMS_TEST(Bots2cParasiteAfterAWonFight) {
  auto world = [](double now, int enemiesAlive, bool binding) {
    World w = MapWorld(now);
    if (binding) AddStructure(w, 50, 2, Kind::kBinding, -24.f, 110.f, 28.f);
    else AddStructure(w, 60, 2, Kind::kCore, 160.f, 190.f, 0.f);
    AddShaper(w, 1, 1, -40.f, -35.f, 1.f);  // within parasiteNear (80), beyond the group's 42
    AddShaper(w, 2, 1, -35.f, -45.f, 1.f);
    AddShaper(w, 3, 1, -50.f, -30.f, 1.f);
    for (int i = 0; i < 5; ++i)
      AddShaper(w, 10 + static_cast<std::uint32_t>(i), 2, 200.f, 200.f - static_cast<float>(i),
                i < enemiesAlive ? 1.f : 0.f);
    w.hasParasite = true;
    w.parasiteHittable = true;
    w.parasite.id = 77;
    w.parasite.npc = kNpcParasite;
    w.parasite.pos = V(0.f, -1.f);
    w.parasite.hp = w.parasite.maxHp = 13050.f;
    return w;
  };
  {
    Brain b;
    World w = world(1000.0, 1, true);
    Intent in = Think(b, w, 1);
    CHECK(b.Plan(1).advantage);
    CHECK(b.Plan(1).parasite);
    CHECK_EQ(b.Plan(1).parasiteNeeded, 3);
    CHECK(b.MemoryOf(1)->state == State::kParasite);
    CHECK(in.act == Act::kMove && std::string(in.why) == "parasite: gather");
    CHECK(Dist(in.point, w.parasite.pos) > 30.f);  // outside its reach
    // one bot at the gathering point alone waits (never alone); three together hit it
    w.shapers[0].pos = in.point;
    w.now = 1005.0;
    in = Think(b, w, 1);
    CHECK(!b.Plan(1).parasiteGrouped);
    CHECK(in.act == Act::kMove);
    w.shapers[1].pos = Toward(in.point, V(-100.f, -100.f), 3.f);
    w.shapers[2].pos = Toward(in.point, V(-100.f, -50.f), 3.f);
    w.now = 1010.0;
    in = Think(b, w, 1);
    CHECK(b.Plan(1).parasiteGrouped);
    CHECK(in.act == Act::kAttack && in.target == 77u && in.abilities);
    // down: the attempt ends, and no new one for parasiteRetryS
    w.hasParasite = false;
    w.now = 1030.0;
    Think(b, w, 1);
    CHECK(!b.Plan(1).parasite);
    CHECK_EQ(std::string(b.Plan(1).parasiteEndWhy), std::string("down"));
    CHECK_EQ(b.Stats().parasiteDowns, 1u);
    w.hasParasite = true;
    w.now = 1040.0;
    Think(b, w, 1);
    CHECK(!b.Plan(1).parasite);
  }
  {  // before 15:00 an advantage pushes, no parasite
    Brain b;
    World w = world(800.0, 1, true);
    Think(b, w, 1);
    CHECK(b.Plan(1).advantage && !b.Plan(1).parasite);
  }
  {  // no won fight: none
    Brain b;
    World w = world(1000.0, 3, true);
    Think(b, w, 1);
    CHECK(!b.Plan(1).parasite);
  }
  {  // the group far from it: no attempt (round 5: only with parasiteAllies near)
    Brain b;
    World w = world(1000.0, 1, true);
    for (Shaper& s : w.shapers)
      if (s.team == 1) s.pos = V(-150.f, -120.f);
    Think(b, w, 1);
    CHECK(b.Plan(1).advantage && !b.Plan(1).parasite);
  }
  {  // round 8: the enemy base open didn't block it, the base's siege did; round 12: the open base starts the base phase
    // (every bot to the enemy base), which blocks it
    Brain b;
    World w = world(1000.0, 1, false);
    Think(b, w, 1);
    CHECK(!b.Plan(1).parasite && b.Plan(1).guard != TeamPlan::kGuardOff);
    Brain b2;
    World w2 = world(1000.0, 1, false);
    for (Shaper& s : w2.shapers)
      if (s.team == 1) s.pos = V(150.f + static_cast<float>(s.id), 180.f);  // grouped at the Core
    Think(b2, w2, 1);
    CHECK(b2.Plan(1).siege && !b2.Plan(1).parasite);
    CHECK(b2.Stats().parasiteNo[static_cast<int>(ParasiteNo::kSiege)] >= 1u);
  }
  {  // the reasons are counted: before 15:00, no won fight
    Brain b;
    World w = world(800.0, 1, true);
    Think(b, w, 1);
    CHECK(b.Stats().parasiteNo[static_cast<int>(ParasiteNo::kEarly)] >= 1u);
    World w2 = world(1000.0, 5, true);
    w2.now = 1000.0;
    Think(b, w2, 1);
    CHECK(b.Stats().parasiteNo[static_cast<int>(ParasiteNo::kNoWonFight)] >= 1u);
  }
  {  // outnumbered later: given up
    Brain b;
    World w = world(1000.0, 1, true);
    Think(b, w, 1);
    CHECK(b.Plan(1).parasite);
    for (Shaper& s : w.shapers)
      if (s.team == 2) {
        s.hp = s.maxHp;
        s.alive = true;
      }
    w.now = 1001.0;
    Think(b, w, 1);
    CHECK(!b.Plan(1).parasite);
    CHECK_EQ(std::string(b.Plan(1).parasiteEndWhy), std::string("outnumbered"));
  }
}

// The battle spells: in a fight at the target (Drain in range), a chase spell when it is far; their cooldown after a
// cast; in a retreat the escapes and the last stand; the Hunter's Vanquish on a camp creature it kills.
GMS_TEST(Bots2cBattleSpells) {
  Brain b;
  World w = MapWorld(500.0);
  Shaper& me = AddShaper(w, 1, 1, -100.f, 0.f, 1.f);
  me.spells[0] = Spell::kDrain;
  me.spells[1] = Spell::kBlitz;
  AddShaper(w, 9, 2, -90.f, 0.f, 0.25f);  // low: a kill chance (a fight to the kill, chased)
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
  CHECK(in.extra.kind == ExtraKind::kSpell && in.extra.spell == Spell::kDrain);
  CHECK_EQ(in.extra.slot, kExtraSlotSpell1);
  CHECK_EQ(in.extra.target, 9u);
  b.NoteExtra(1, in.extra, 1, 500.0);  // started: 180 s of cooldown
  w.now = 500.25;
  in = Think(b, w, 1);
  CHECK(in.extra.kind == ExtraKind::kNone);  // Drain cooling down, the target near (no chase)
  w.shapers[1].pos = V(-84.f, 0.f);           // 16 away: out of Drain's range, a chase
  w.now = 500.5;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.extra.spell == Spell::kBlitz);
  CHECK_EQ(in.extra.slot, kExtraSlotSpell1 + 1);
  CHECK_EQ(in.extra.target, 0u);
  b.NoteExtra(1, in.extra, -0x270F, 500.5);  // refused: again after castRetryS
  w.now = 501.0;
  CHECK(Think(b, w, 1).extra.kind == ExtraKind::kNone);
  w.now = 502.6;
  CHECK(Think(b, w, 1).extra.spell == Spell::kBlitz);
  CHECK(b.Stats().spellsStarted[static_cast<int>(Spell::kDrain)] == 1u);

  // the retreat: an escape while an enemy is near and the bot was hit; the last stand at very low HP
  Brain r;
  World w2 = MapWorld(600.0);
  Shaper& low = AddShaper(w2, 2, 1, -100.f, 0.f, 0.3f);
  low.spells[0] = Spell::kStasis;
  low.spells[1] = Spell::kBlitz;
  AddShaper(w2, 9, 2, -92.f, 0.f, 1.f);
  Think(r, w2, 2);
  r.NoteDamage(2, 9, true, 600.0);
  w2.now = 600.25;
  in = Think(r, w2, 2);
  CHECK(r.MemoryOf(2)->state == State::kRetreat);
  CHECK(in.act == Act::kMove && in.extra.spell == Spell::kBlitz);
  w2.shapers[0].hp = 150.f;  // 15 %
  r.NoteDamage(2, 9, true, 600.4);
  w2.now = 600.5;
  in = Think(r, w2, 2);
  CHECK(in.extra.spell == Spell::kStasis);

  // the Hunter's Vanquish at a camp's medium it kills
  Brain h;
  World w3 = MapWorld(300.0);
  Shaper& hunter = AddShaper(w3, 3, 1, -60.f, -30.f, 1.f);
  hunter.role = kRoleHunter;
  hunter.spells[0] = Spell::kVanquish;
  Unit c;
  c.id = 400;
  c.npc = kNpcObjectiveMedium;
  c.pos = V(-55.f, -30.f);
  c.hp = 400.f;
  c.maxHp = 1000.f;
  w3.creeps.push_back(c);
  in = Think(h, w3, 3);
  CHECK(h.MemoryOf(3)->state == State::kJungle);
  CHECK(in.act == Act::kAttack && in.target == 400u);
  CHECK(in.extra.spell == Spell::kVanquish && in.extra.target == 400u);
  w3.creeps[0].hp = 900.f;  // more than it kills (480 at level 6): no Vanquish
  w3.now = 301.0;
  CHECK(Think(h, w3, 3).extra.kind == ExtraKind::kNone);
}

// Potions: below potionHp outside the base, one heal at a time, not when the heal would be wasted.
GMS_TEST(Bots2cPotions) {
  Brain b;
  World w = MapWorld(300.0);
  Shaper& me = AddShaper(w, 1, 1, -100.f, 0.f, 0.5f);
  me.potions = 2;
  me.potionSlot = kExtraSlotConsume1;
  Intent in = Think(b, w, 1);
  CHECK(in.act != Act::kNone);
  CHECK(in.extra.kind == ExtraKind::kPotion);
  CHECK_EQ(in.extra.slot, kExtraSlotConsume1);
  b.NoteExtra(1, in.extra, 1, 300.0);
  w.now = 301.0;
  CHECK(Think(b, w, 1).extra.kind != ExtraKind::kPotion);  // the heal runs 15 s
  w.now = 315.5;
  CHECK(Think(b, w, 1).extra.kind == ExtraKind::kPotion);
  w.shapers[0].hp = 800.f;  // 80 %
  w.now = 340.0;
  CHECK(Think(b, w, 1).extra.kind != ExtraKind::kPotion);
  w.shapers[0].maxHp = 300.f;  // 60 % of 300: 120 missing, less than a potion's 150
  w.shapers[0].hp = 180.f;
  w.now = 360.0;
  CHECK(Think(b, w, 1).extra.kind != ExtraKind::kPotion);
  // in the base: none (the fountain heals)
  World w2 = MapWorld(400.0);
  Shaper& home = AddShaper(w2, 5, 1, -224.f, -224.f, 0.5f);
  home.potions = 2;
  home.potionSlot = kExtraSlotConsume1;
  CHECK(Think(b, w2, 5).extra.kind == ExtraKind::kNone);
}

// Wards: a free spot near the bot's lane walk turns the walk to it; within range the ward is cast at the spot; no ward
// where an allied ward stands, nor during its cooldown, nor in a fight.
GMS_TEST(Bots2cWardsOnTheWay) {
  Brain b;
  World w = MapWorld(300.0);
  w.wardSpots = {V(-143.f, 4.f), V(140.f, 140.f)};
  AddShaper(w, 1, 1, -120.f, 0.f, 1.f);
  Intent in = Think(b, w, 1);
  CHECK(b.MemoryOf(1)->state == State::kLane);
  CHECK(in.act == Act::kMove && std::string(in.why) == "ward: to a free spot");
  CHECK(Dist(in.point, V(-143.f, 4.f)) < b.tuning().walkSpread + 0.5f);  // (a long walk's spread)
  CHECK(in.extra.kind == ExtraKind::kNone);
  w.shapers[0].pos = V(-133.f, 2.f);  // 10 from the spot
  w.now = 303.0;
  in = Think(b, w, 1);
  CHECK(in.extra.kind == ExtraKind::kWard);
  CHECK_EQ(in.extra.slot, kExtraSlotWard);
  CHECK(in.extra.atPoint && Dist(in.extra.point, V(-143.f, 4.f)) < 0.1f);
  CHECK(std::string(in.why) != "ward: to a free spot");  // the lane walk goes on
  b.NoteExtra(1, in.extra, 1, 303.0);
  w.now = 304.0;
  CHECK(Think(b, w, 1).extra.kind == ExtraKind::kNone);  // its cooldown (180 s)
  // an allied ward there: the spot isn't free; an enemy ward doesn't take it
  Brain b2;
  Unit ward;
  ward.id = 500;
  ward.team = 1;
  ward.npc = kNpcWard;
  ward.pos = V(-140.f, 10.f);
  w.wards.push_back(ward);
  CHECK(Think(b2, w, 1).extra.kind == ExtraKind::kNone);
  w.wards[0].team = 2;
  CHECK(Think(b2, w, 1).extra.kind == ExtraKind::kWard);
  // no ward in a fight
  Brain b3;
  AddShaper(w, 9, 2, -128.f, 4.f, 0.3f);
  in = Think(b3, w, 1);
  CHECK(b3.MemoryOf(1)->state == State::kFight);
  CHECK(in.extra.kind != ExtraKind::kWard);
}

// BOTS2c: a hurt laner (below tradeHp) without a threat farms on (round 6: round 5's last-hits-only mode cost farm); the
// Hunter lanes at level 1 (hunterJungleLevel), then takes the small camps before the buff and pig camps (level 3).
GMS_TEST(Bots2cCarefulLaneAndSmallCampsFirst) {
  Brain b;
  World w = MapWorld(300.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 0.5f);
  AddShaper(w, 8, 2, 200.f, 200.f, 1.f);  // team 2 alive far away: no advantage push
  AddMinion(w, 30, 2, -90.f, 45.f, 300.f);
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 30u);
  // the Hunter at level 1: his lane; at level 2 the small camp, though the buff camp is nearer
  Brain h;
  World j = MapWorld(120.0);
  Shaper& hunter = AddShaper(j, 3, 1, -60.f, -30.f, 1.f);
  hunter.role = kRoleHunter;
  hunter.level = 1;
  AddShaper(j, 8, 2, 200.f, 200.f, 1.f);
  Unit major;
  major.id = 40;
  major.npc = kNpcObjectiveMedium;
  major.pos = V(-50.f, -30.f);
  major.hp = major.maxHp = 1200.f;
  Unit small = major;
  small.id = 41;
  small.npc = kNpcCreep;
  small.pos = V(-110.f, -60.f);
  small.hp = small.maxHp = 300.f;
  j.creeps = {major, small};
  in = Think(h, j, 3);
  CHECK(h.MemoryOf(3)->state != State::kJungle);
  j.shapers[0].level = 2;
  j.now = 120.5;
  in = Think(h, j, 3);
  CHECK(h.MemoryOf(3)->state == State::kJungle);
  CHECK(in.act == Act::kMove && Dist(in.point, V(-110.f, -60.f)) < 1.f);
  j.shapers[0].level = 3;  // from level 3 the buff camp too
  j.now = 121.0;
  in = Think(h, j, 3);
  CHECK(in.act == Act::kAttack && in.target == 40u);
}

// BOTS2c round 6: the base's siege holds: its rally point stands beside the Core (not at the lane's end), a sieging bot
// with half its HP doesn't step out of the Guardian's fire, and the group stays while one of it is there and the team
// isn't outnumbered.
GMS_TEST(Bots2cBaseSiegeHolds) {
  Brain b;
  World w = MapWorld(1000.0);
  AddStructure(w, 60, 2, Kind::kCore, 229.f, 178.f, 0.f);
  Structure& g = AddStructure(w, 61, 2, Kind::kGuardian, 182.f, 182.f, 30.f);
  g.shielded = true;
  AddStructure(w, 63, 2, Kind::kCore, 138.9f, 198.9f, 0.f);  // round 11: a second Core (the last one is the Guardian phase's)
  AddShaper(w, 9, 2, 230.f, 230.f, 1.f);  // team 2: two alive, in its base
  AddShaper(w, 8, 2, 225.f, 232.f, 1.f);
  Think(b, w, 1);  // no bot of team 1 yet: the plan
  AddShaper(w, 1, 1, 214.f, 170.f, 1.f);
  AddShaper(w, 2, 1, 216.f, 166.f, 1.f);
  AddShaper(w, 3, 1, 212.f, 174.f, 1.f);
  w.now = 1001.0;
  Intent in = Think(b, w, 1);
  CHECK_EQ(b.Plan(1).objective, 60u);
  CHECK(Dist(b.Plan(1).rally, V(229.f, 178.f)) < 25.f);  // beside the Core
  CHECK(b.Plan(1).grouped && b.Plan(1).siege);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardGather && in.act == Act::kMove);  // round 12: the base phase gathers first
  // the Guardian shoots bot 1 (80 %, inside its reach): without an edge (no advantage, no own minions there: round 7)
  // it steps out of the fire ...
  w.structures[1].target = 1;
  w.shapers[2].hp = 800.f;
  w.shapers[2].pos = V(210.f, 176.f);  // 28.6 from the Guardian
  w.now = 1002.0;
  in = Think(b, w, 1);
  CHECK(!b.Plan(1).siegeEdge);
  CHECK(std::string(in.why) == "retreat: out of a structure's fire");
  // ... with own minions at the Core it stays in the siege
  AddMinion(w, 70, 1, 222.f, 176.f);
  AddMinion(w, 71, 1, 224.f, 172.f);
  w.now = 1002.5;
  in = Think(b, w, 1);
  CHECK(b.Plan(1).siegeEdge);
  CHECK(in.act == Act::kMove && std::string(in.why) != "retreat: out of a structure's fire");  // no escape: the phase
  w.minions.clear();
  // one of the group dead, two against two: the siege holds (BOTS2's rule ended it two short of the group)
  w.shapers[3].hp = 0.f;
  w.shapers[3].alive = false;
  w.now = 1003.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).grouped);
  // outnumbered (one against three): over
  w.shapers[4].hp = 0.f;
  w.shapers[4].alive = false;
  w.now = 1004.0;
  w.shapers.push_back(w.shapers[1]);
  w.shapers.back().id = 7;
  w.shapers.back().pos = V(228.f, 228.f);
  Think(b, w, 1);
  CHECK(!b.Plan(1).grouped);
  // round 8: and it doesn't form again on the next think while still outnumbered (round 7's `_9d` flipped every think)
  w.now = 1004.5;
  Think(b, w, 1);
  CHECK(!b.Plan(1).grouped);
}

// ---- BOTS2c: head to head (a brain per team) ----------------------------------------------------------------------------

GMS_TEST(Bots2cBrainPerTeam) {
  BrainChoice c = ChooseBrains("classic", "", "");
  CHECK(!c.Any() && !c.Mixed());
  CHECK_EQ(std::string(c.Label()), std::string("classic"));
  c = ChooseBrains("v2", "", "");
  CHECK(c.v2[1] && c.v2[2] && !c.Mixed());
  CHECK_EQ(std::string(c.Label()), std::string("v2"));
  c = ChooseBrains("classic", "v2", "");
  CHECK(c.v2[1] && !c.v2[2] && c.Mixed());
  CHECK_EQ(std::string(c.Label()), std::string("t1v2_t2classic"));
  c = ChooseBrains("v2", "", "classic");
  CHECK(c.v2[1] && !c.v2[2]);
  c = ChooseBrains("v2", "classic", "");
  CHECK(!c.v2[1] && c.v2[2]);
  CHECK_EQ(std::string(c.Label()), std::string("t1classic_t2v2"));
  c = ChooseBrains("v22", "V2", nullptr);  // anything but "v2" is the classic brain
  CHECK(!c.Any());
  // a brain plans only its teams: after groupAtS team 1 (v2) pushes, team 2 (classic) has no v2 plan
  Brain b;
  b.SetTeams(true, false);
  World w = MapWorld(900.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f);
  AddShaper(w, 9, 2, 100.f, -30.f, 1.f);
  b.PlanTeams(w);
  CHECK(b.Plan(1).push);
  CHECK(!b.Plan(2).push);
}

// BOTS2c round 7: the hybrid's choice per team and per bot: the classic brain's micro (an enemy near, hurt, in base, no
// task), v2's macro (the team's task when calm), the idle rescue, and a takeover's minimum hold.
GMS_TEST(Bots2cHybridDriver) {
  BrainChoice c = ChooseBrains("classic", "hybrid", "");
  CHECK(c.Hybrid(1) && !c.Hybrid(2) && c.v2[1] && !c.v2[2]);
  CHECK_EQ(std::string(c.Label()), std::string("t1hybrid_t2classic"));
  c = ChooseBrains("hybrid", "", "v2");
  CHECK(c.Hybrid(1) && c.kind[2] == kBrainV2);
  CHECK_EQ(std::string(c.Label()), std::string("t1hybrid_t2v2"));
  CHECK_EQ(std::string(ChooseBrains("hybrid", "", "").Label()), std::string("hybrid"));
  const Tuning t;
  HybridView v;
  v.alive = true;
  v.hp = 0.9f;
  v.now = 1000.0;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);  // no task: the classic brain's laning
  v.teamTask = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Task);  // the push, calm
  v.nearestEnemy = 20.f;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);  // an enemy near: the classic brain fights
  v.nearestEnemy = 1.0e30f;
  v.hp = 0.3f;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);  // hurt: its retreat and recall
  v.hp = 0.9f;
  v.inBase = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);  // in base: its shop
  v.inBase = false;
  v.teamTask = false;
  v.idleRunS = 3.5;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Rescue);  // the classic brain left it standing
  v.idleRunS = 0.0;
  v.driven = true;
  v.drivenSince = 999.0;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Task);  // a takeover holds hybridHoldS
  v.drivenSince = 990.0;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  v.alive = false;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  // round 8: the hysteresis. An enemy at 35 (between hybridFightRange and hybridCalmRange) a second ago: a classic bot
  // stays classic, a driven one stays driven; after hybridCalmS without one the task takes it
  v.alive = true;
  v.teamTask = true;
  v.nearestEnemy = 35.f;
  v.calmS = 0.0;
  v.driven = false;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  v.driven = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Task);
  v.driven = false;
  v.nearestEnemy = 1.0e30f;
  v.calmS = 3.5;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Task);
  // a task an enemy ended isn't taken again while the back-off runs (the idle rescue still is)
  v.backoff = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  v.backoff = false;
  // the idle rescue needs no calm time (the classic brain left it standing)
  v.teamTask = false;
  v.calmS = 0.5;
  v.idleRunS = 3.5;
  v.backoff = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Rescue);
}

// Round 9: the finish. In the push group of the base's siege with its edge v2 keeps the bot with enemies near; hurt, the
// classic brain takes it back
GMS_TEST(Bots2cHybridFinish) {
  const Tuning t;
  HybridView v;
  v.alive = true;
  v.hp = 0.8f;
  v.now = 2000.0;
  v.nearestEnemy = 12.f;
  v.calmS = 0.0;
  v.backoff = true;
  v.finish = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kV2Task);
  v.hp = 0.3f;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  v.hp = 0.8f;
  v.finish = false;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
  v.finish = true;
  v.inBase = true;
  CHECK(ChooseDriver(v, t) == HybridDriver::kClassic);
}

// Round 10: at the unshielded Guardian not being outnumbered is the edge (no minions needed); outnumbered it isn't
GMS_TEST(Bots2cGuardianEdge) {
  Brain b;
  World w = MapWorld(2400.0);
  AddStructure(w, 62, 2, Kind::kGuardian, 182.f, 182.f, 30.f);  // unshielded: every Core dead
  AddShaper(w, 1, 1, 150.f, 152.f, 1.f);
  AddShaper(w, 2, 1, 152.f, 149.f, 1.f);
  AddShaper(w, 3, 1, 148.f, 150.f, 1.f);
  AddShaper(w, 9, 2, 230.f, 230.f, 1.f);
  AddShaper(w, 8, 2, 225.f, 232.f, 1.f);
  AddShaper(w, 7, 2, 228.f, 226.f, 1.f);
  Think(b, w, 1);
  CHECK_EQ(b.Plan(1).objective, 62u);
  CHECK(!b.Plan(1).advantage && b.Plan(1).siegeEdge && b.Plan(1).rallySafe);
  w.shapers[2].hp = 0.f;  // three against two of the team: no edge
  w.shapers[2].alive = false;
  w.now = 2401.0;
  Think(b, w, 1);
  CHECK(!b.Plan(1).siegeEdge);
}

// Round 11/12: the base phase (the user's rule: group up, in together, out together). Every living bot gathers at the
// staging point outside the Guardian's reach; four there (fewer than the enemy's five alive) wait guardStragglerS for
// the fifth, then go in together; a member the Guardian shoots stays in; half the group down: back off together; Cores
// regrown: the phase goes on for a Core.
GMS_TEST(Bots2cGuardianPhase) {
  Brain b;
  World w = MapWorld(2400.0);
  AddStructure(w, 62, 2, Kind::kGuardian, 182.f, 182.f, 30.f);  // unshielded: every Core dead
  AddShaper(w, 9, 2, 230.f, 230.f, 1.f);                         // five enemies alive, in their base
  AddShaper(w, 8, 2, 226.f, 232.f, 1.f);
  AddShaper(w, 13, 2, 232.f, 226.f, 1.f);
  AddShaper(w, 14, 2, 228.f, 236.f, 1.f);
  AddShaper(w, 15, 2, 236.f, 228.f, 1.f);
  for (std::uint32_t id = 1; id <= 5; ++id) AddShaper(w, id, 1, -100.f, -100.f + static_cast<float>(id), 1.f);
  Intent in = Think(b, w, 1);
  const TeamPlan& p = b.Plan(1);
  CHECK(p.guard == TeamPlan::kGuardGather && p.guardTarget == 62u);
  CHECK(Dist(p.staging, V(182.f, 182.f)) >= 30.f + 4.f);  // outside its reach ...
  CHECK(Dist(p.staging, V(226.f, 226.f)) >= 75.f - 0.5f);  // ... and the enemy spawn's guardSpawnKeep
  CHECK(in.act == Act::kMove && std::string(in.why) == "guardian: to the staging point");
  CHECK_EQ(b.Stats().guardPhases, 1u);
  // four at the staging point: they wait for the fifth (nobody goes in alone or early)
  const Vec3 st = p.staging;
  for (int i = 5; i < 9; ++i) w.shapers[static_cast<size_t>(i)].pos = V(st.x + static_cast<float>(i - 5), st.y);
  w.now = 2401.0;
  in = Think(b, w, 1);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardGather && b.Plan(1).guardCount == 4 && b.Plan(1).guardNeeded == 5);
  CHECK(std::string(in.why) == "guardian: wait for the group");
  w.now = 2401.0 + 4.5;  // guardStragglerS later: in with four
  in = Think(b, w, 1);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardEngage && b.Plan(1).guardEngaged == 4);
  CHECK(in.act == Act::kAttack && in.target == 62u && std::string(in.why) == "guardian: engage");
  CHECK_EQ(b.Stats().guardEngages, 1u);
  in = b.Decide(w, 5);  // the straggler, far away: to the staging point, not into the fight
  CHECK(in.act == Act::kMove && std::string(in.why) == "guardian: wait at the staging point (the group is in)");
  // a member the Guardian shoots stays in (at 80 %)
  w.shapers[5].pos = V(165.f, 165.f);
  w.shapers[5].hp = 800.f;
  w.structures[0].target = 1;
  w.now = 2409.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 62u);
  // two of the four down (and two enemies, so not outnumbered): back off together
  for (size_t i : {size_t{6}, size_t{7}, size_t{2}, size_t{3}}) {
    w.shapers[i].alive = false;
    w.shapers[i].hp = 0.f;
  }
  w.now = 2410.0;
  in = Think(b, w, 1);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardGather && std::string(b.Plan(1).guardWhy) == "half of the group down");
  CHECK(in.act == Act::kMove && std::string(in.why) == "guardian: back off together");
  CHECK_EQ(b.Stats().guardBackOffs, 1u);
  // outnumbered (three living against five): no engage even with every living bot at the staging point
  for (size_t i : {size_t{2}, size_t{3}}) {
    w.shapers[i].alive = true;
    w.shapers[i].hp = w.shapers[i].maxHp;
  }
  w.shapers[5].pos = st;
  w.shapers[9].pos = st;  // bot 5
  w.now = 2430.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardGather);
  // two Cores regrew: the phase goes on, for a Core now (round 12: the whole base siege)
  AddStructure(w, 60, 2, Kind::kCore, 229.f, 178.f, 0.f);
  AddStructure(w, 61, 2, Kind::kCore, 149.f, 149.f, 0.f);
  w.structures[0].shielded = true;
  w.now = 2431.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).guard == TeamPlan::kGuardGather && (b.Plan(1).guardTarget == 60u || b.Plan(1).guardTarget == 61u));
}

// Round 12: the way to the staging point avoids a living enemy Binding: by the push lane (its Bindings are down)
GMS_TEST(Bots2cRouteAroundBindings) {
  Brain b;
  World w = MapWorld(2000.0);
  AddStructure(w, 60, 2, Kind::kCore, 149.f, 149.f, 0.f);
  AddStructure(w, 52, 2, Kind::kBinding, 124.5f, 44.4f, 28.f);  // the other lane's living Binding
  Shaper& me = AddShaper(w, 1, 1, 112.f, -20.f, 1.f);
  const Vec3 dest = V(140.f, 130.f);  // straight north: past the Binding
  const Vec3 r = b.RouteTo(w, me, dest, gms::bots::kLaneTop);
  CHECK(Dist(r, dest) > 1.f);  // not straight
  const Vec3 free = V(100.f, -60.f);  // away from it: straight
  CHECK(Dist(b.RouteTo(w, me, free, gms::bots::kLaneTop), free) < 0.01f);
}

// Round 9: at the enemy base the next Core is the one nearest the team's bots there, a damaged one first, the current
// one kept against a slightly nearer one (round 8's `_d2`: one far Core held the plan for 20 minutes)
GMS_TEST(Bots2cSiegeCoreChoice) {
  Brain b;
  World w = MapWorld(1500.0);
  AddStructure(w, 60, 2, Kind::kCore, 229.f, 178.f, 0.f);  // beside the push lane's end
  AddStructure(w, 61, 2, Kind::kCore, 149.f, 149.f, 0.f);
  Structure& g = AddStructure(w, 62, 2, Kind::kGuardian, 182.f, 182.f, 30.f);
  g.shielded = true;
  AddShaper(w, 1, 1, 147.f, 141.f, 1.f);  // the team stands at Core 61 (within siegeAnchorRange of the enemy spawn)
  AddShaper(w, 2, 1, 149.f, 137.f, 1.f);
  AddShaper(w, 3, 1, 143.f, 144.f, 1.f);
  Think(b, w, 1);
  CHECK_EQ(b.Plan(1).objective, 61u);
  // the bots walk over to Core 60, which is low: it is next
  for (Shaper& s : w.shapers)
    if (s.team == 1) s.pos = V(s.pos.x + 75.f, s.pos.y + 20.f);
  w.structures[0].hp = 0.2f * w.structures[0].maxHp;
  w.now = 1501.0;
  Think(b, w, 1);
  CHECK_EQ(b.Plan(1).objective, 60u);
  // a few units nearer to Core 61 again: 60 stays (siegeKeep)
  for (Shaper& s : w.shapers)
    if (s.team == 1) s.pos = V(186.f, 160.f);
  w.structures[0].hp = w.structures[0].maxHp;
  w.now = 1502.0;
  Think(b, w, 1);
  const float d60 = Dist(V(186.f, 160.f), V(229.f, 178.f)), d61 = Dist(V(186.f, 160.f), V(149.f, 149.f));
  CHECK(d61 < d60 && d61 > d60 - 15.f);  // the setup: 61 nearer, by less than siegeKeep
  CHECK_EQ(b.Plan(1).objective, 60u);
}

// Round 8: the hybrid's push takes hybridPushBots bots outside the enemy base, the nearest to the rally point, a Hunter
// last, and keeps them; at a Core every healthy bot (v2 itself still sends every bot)
GMS_TEST(Bots2cHybridPushGroup) {
  Brain b;
  World w = MapWorld(900.0);
  AddStructure(w, 50, 2, Kind::kBinding, -24.f, 110.f, 28.f);  // Top lane, team 2's nearest
  AddStructure(w, 52, 2, Kind::kBinding, 152.f, 115.f, 28.f);
  AddShaper(w, 1, 1, -75.f, 70.f, 1.f);
  AddShaper(w, 2, 1, -80.f, 60.f, 1.f);
  AddShaper(w, 3, 1, -150.f, -150.f, 1.f);  // far, at home
  AddShaper(w, 4, 1, -85.f, 55.f, 1.f).role = kRoleHunter;
  AddShaper(w, 5, 1, -90.f, 50.f, 0.2f);  // hurt: not in it
  AddShaper(w, 6, 1, -60.f, -120.f, 1.f);
  Think(b, w, 1);
  const TeamPlan& p = b.Plan(1);
  CHECK(p.push && p.objective == 50u);
  CHECK_EQ(static_cast<int>(p.pushGroup.size()), 3);
  CHECK(std::find(p.pushGroup.begin(), p.pushGroup.end(), 1u) != p.pushGroup.end());
  CHECK(std::find(p.pushGroup.begin(), p.pushGroup.end(), 2u) != p.pushGroup.end());
  CHECK(std::find(p.pushGroup.begin(), p.pushGroup.end(), 4u) == p.pushGroup.end());  // the Hunter last
  CHECK(std::find(p.pushGroup.begin(), p.pushGroup.end(), 5u) == p.pushGroup.end());  // hurt
  // a member stays while healthy even when another comes nearer
  const std::vector<std::uint32_t> before = p.pushGroup;
  w.shapers[5].pos = p.rally;  // bot 6 (the third member or not) now stands at the rally point
  w.now = 901.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).pushGroup == before);
  // the walk to the rally point is safe only behind the own wave: none yet, then two own minions past the rally point
  CHECK(!b.Plan(1).rallySafe);
  const Vec3 past = Toward(b.Plan(1).rally, V(-24.f, 110.f), 6.f);
  AddMinion(w, 70, 1, past.x, past.y);
  AddMinion(w, 71, 1, past.x + 1.f, past.y - 1.f);
  w.now = 902.0;
  Think(b, w, 1);
  CHECK(b.Plan(1).rallySafe);
  // at a Core: every healthy bot
  World w2 = MapWorld(1000.0);
  AddStructure(w2, 60, 2, Kind::kCore, 229.f, 178.f, 0.f);
  for (std::uint32_t id = 1; id <= 5; ++id) AddShaper(w2, id, 1, 200.f + static_cast<float>(id), 160.f, id == 5 ? 0.2f : 1.f);
  Brain b2;
  Think(b2, w2, 1);
  CHECK_EQ(static_cast<int>(b2.Plan(1).pushGroup.size()), 4);
}

// ---- BOTS2c: the laning phase (the user's first match: "no traditional laning and trading. It's KILL THE OTHER") ------

// Before the push a fight is a short trade (in tradeRange, even odds, every tradeGapS; then the bot steps back), a kill
// chance (the target low or a gank) or an answer to a hit (back at even odds, else a step back).
GMS_TEST(Bots2cLaningTradesAndKills) {
  Brain b;
  World w = MapWorld(300.0);
  AddShaper(w, 1, 1, -100.f, 30.f, 1.f).role = kRoleGladiator;
  AddShaper(w, 9, 2, -90.f, 40.f, 1.f);  // 14 away, one against one, both full: a trade
  Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u && in.abilities);
  CHECK(b.MemoryOf(1)->state == State::kFight);
  CHECK_EQ(b.MemoryOf(1)->fightMode, static_cast<int>(Memory::kFightTrade));
  CHECK_EQ(b.Stats().trades, 1u);
  w.now = 301.5;  // within tradeS: the trade holds
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
  w.now = 302.6;  // over: the bot steps back behind its minions, and doesn't trade again before tradeGapS
  in = Think(b, w, 1);
  CHECK(in.act == Act::kMove);
  CHECK(b.MemoryOf(1)->state == State::kLane);
  CHECK(b.MemoryOf(1)->backOffUntil > w.now);
  w.now = 309.0;
  CHECK(Think(b, w, 1).act == Act::kMove);
  w.now = 313.0;
  in = Think(b, w, 1);
  CHECK(in.act == Act::kAttack && in.target == 9u);
  CHECK_EQ(b.Stats().trades, 2u);
  {  // out of trade range at full HP: no fight (BOTS2 fought any even fight within engageRange 22)
    Brain c;
    World x = MapWorld(300.0);
    AddShaper(x, 1, 1, -100.f, 30.f, 1.f);
    AddShaper(x, 9, 2, -86.f, 41.f, 1.f);  // 17.8 away
    in = Think(c, x, 1);
    CHECK(!(in.act == Act::kAttack && in.target == 9u));
    CHECK(c.MemoryOf(1)->state == State::kLane);
  }
  {  // a kill chance: the target low
    Brain c;
    World x = MapWorld(300.0);
    AddShaper(x, 1, 1, -100.f, 30.f, 1.f);
    AddShaper(x, 9, 2, -86.f, 41.f, 0.3f);
    in = Think(c, x, 1);
    CHECK(in.act == Act::kAttack && in.target == 9u);
    CHECK_EQ(c.MemoryOf(1)->fightMode, static_cast<int>(Memory::kFightKill));
  }
  {  // two against one at full HP 18 away: no fight (odds 2.0 under gankOdds 2.5, out of trade range); three: a gank
    Brain c;
    World x = MapWorld(300.0);
    AddShaper(x, 1, 1, -100.f, 30.f, 1.f);
    AddShaper(x, 2, 1, -102.f, 28.f, 1.f);
    AddShaper(x, 9, 2, -86.f, 41.f, 1.f);
    AddShaper(x, 7, 2, 200.f, 200.f, 1.f);  // the rest of team 2 alive far away: no advantage push
    AddShaper(x, 8, 2, 205.f, 200.f, 1.f);
    in = Think(c, x, 1);
    CHECK(!(in.act == Act::kAttack && in.target == 9u));
    AddShaper(x, 3, 1, -101.f, 32.f, 1.f);
    x.now = 300.25;
    in = Think(c, x, 1);
    CHECK(in.act == Act::kAttack && in.target == 9u);
    CHECK_EQ(c.MemoryOf(1)->fightMode, static_cast<int>(Memory::kFightKill));
    CHECK_EQ(c.Stats().killGanks, 1u);
  }
  {  // hit at bad odds (70 % against a full enemy): a step back, no fight
    Brain c;
    World x = MapWorld(300.0);
    AddShaper(x, 1, 1, -100.f, 30.f, 0.7f);
    AddShaper(x, 9, 2, -90.f, 40.f, 1.f);
    Think(c, x, 1);
    c.NoteDamage(1, 9, true, 300.0);
    x.now = 300.25;
    in = Think(c, x, 1);
    CHECK(in.act == Act::kMove);
    CHECK(c.MemoryOf(1)->state == State::kLane);
    CHECK_EQ(c.Stats().backOffs, 1u);
  }
  {  // hit at even odds from beyond the trade range: hit back
    Brain c;
    World x = MapWorld(300.0);
    AddShaper(x, 1, 1, -100.f, 30.f, 1.f);
    AddShaper(x, 9, 2, -100.f, 55.f, 1.f);  // 25 away
    Think(c, x, 1);
    c.NoteDamage(1, 9, true, 300.0);
    x.now = 300.25;
    in = Think(c, x, 1);
    CHECK(in.act == Act::kAttack && in.target == 9u);
  }
}

// Without own minions on the lane a bot waits in front of its outermost Binding, not at mid lane (BOTS2's bots met at
// 0:31 at mid lane and fought); a long walk's goal is spread to each bot's side, so two bots don't walk in single file.
GMS_TEST(Bots2cLaneStandAtTheTowerAndSpread) {
  Brain b;
  World w = MapWorld(40.0);
  AddStructure(w, 60, 1, Kind::kBinding, -131.6f, -44.2f, 28.f);  // team 1's outer Binding on the Top lane
  AddShaper(w, 1, 1, -200.f, -180.f, 1.f).role = kRoleGladiator;
  const Intent in = Think(b, w, 1);
  CHECK(in.act == Act::kMove);
  CHECK(Dist(in.point, V(-131.6f, -44.2f)) < b.tuning().towerWait + b.tuning().laneSide + b.tuning().walkSpread + 1.f);
  // two bots with one goal get two walks
  std::uint32_t a = 0, c = 0;
  for (std::uint32_t id = 1; id < 40 && (!a || !c); ++id) {
    if (WalkSideOf(id) > 0.f && !a) a = id;
    if (WalkSideOf(id) < 0.f && !c) c = id;
  }
  CHECK(a && c);
  Brain b2;
  World w2 = MapWorld(40.0);
  AddShaper(w2, a, 1, -200.f, -180.f, 1.f).role = 1;
  AddShaper(w2, c, 1, -200.f, -180.f, 1.f).role = 1;
  b2.PlanTeams(w2);
  const Intent ia = b2.Decide(w2, a), ic = b2.Decide(w2, c);
  CHECK(ia.act == Act::kMove && ic.act == Act::kMove);
  CHECK(Dist(ia.point, ic.point) > 1.5f * b2.tuning().walkSpread);
  // Beside never makes the null position
  CHECK(!(Beside(V(4.f, 0.f), V(0.f, 1.f), 1.f, 4.f).x == 0.f && Beside(V(4.f, 0.f), V(0.f, 1.f), 1.f, 4.f).y == 0.f));
}

// Fewer recalls: in the laning phase a hurt bot without a threat lanes on (and drinks); threatened it steps back behind
// its Binding instead of recalling, potion or not, and lanes on when the threat is gone; it recalls below recallHp. No
// gold recall in the first minutes, nor without gold for its next item step.
GMS_TEST(Bots2cFewerRecalls) {
  auto world = [](double now, float hp, int potions) {
    World w = MapWorld(now);
    AddStructure(w, 60, 1, Kind::kBinding, -131.6f, -44.2f, 28.f);
    Shaper& s = AddShaper(w, 1, 1, -110.f, 20.f, hp);
    s.role = kRoleGladiator;
    s.potions = potions;
    s.potionSlot = potions > 0 ? kExtraSlotConsume1 : -1;
    AddShaper(w, 8, 2, 200.f, 200.f, 1.f);  // team 2 alive far away: no advantage push
    return w;
  };
  {  // hurt, no threat: lanes on and drinks
    Brain b;
    World w = world(300.0, 0.3f, 1);
    Intent in = Think(b, w, 1);
    CHECK(b.MemoryOf(1)->state == State::kLane);
    CHECK(in.extra.kind == ExtraKind::kPotion);
    CHECK_EQ(b.Stats().recalls, 0u);
  }
  {  // hurt with an enemy shaper near, no potion: to the Binding, no recall; the threat gone: back to its lane
    Brain b;
    World w = world(300.0, 0.3f, 0);
    AddShaper(w, 9, 2, -100.f, 30.f, 1.f);  // 14 away
    Intent in = Think(b, w, 1);
    CHECK(b.MemoryOf(1)->state == State::kRetreat);
    CHECK(in.act == Act::kMove && std::string(in.why) == "retreat: heal at the Binding");
    CHECK_EQ(b.Stats().laneStays, 1u);
    CHECK_EQ(b.Stats().recalls, 0u);
    w.shapers.pop_back();  // the enemy gone
    w.shapers[0].pos = V(-135.f, -60.f);
    w.now = 304.0;
    Think(b, w, 1);
    CHECK(b.MemoryOf(1)->state == State::kLane);
  }
  {  // below recallHp: recall
    Brain b;
    World w = world(300.0, 0.1f, 1);
    CHECK(Think(b, w, 1).act == Act::kRecall);
  }
  {  // a camp's or a minion's hit is no threat: a hurt laner hit by one lanes on
    Brain b;
    World w = world(300.0, 0.3f, 0);
    Think(b, w, 1);
    b.NoteDamage(1, 500, false, 300.0);
    w.now = 300.25;
    CHECK(Think(b, w, 1).act != Act::kRecall);
    CHECK(b.MemoryOf(1)->state == State::kLane);
  }
  {  // in the push phase: BOTS2's retreat (home and recall)
    Brain b;
    World w = world(900.0, 0.3f, 1);
    CHECK(Think(b, w, 1).act == Act::kRecall);
  }
  auto gold = [&](double now, float g, int price) {
    World w = world(now, 0.6f, 0);
    w.shapers[0].gold = g;
    w.shapers[0].nextItemPrice = price;
    Brain b;
    return Think(b, w, 1).act;
  };
  CHECK(gold(200.0, 3000.f, 1200) != Act::kRecall);  // the first minutes
  CHECK(gold(300.0, 3000.f, 1200) == Act::kRecall);
  CHECK(gold(300.0, 3000.f, 0) != Act::kRecall);     // nothing to buy
  CHECK(gold(300.0, 3000.f, 3500) != Act::kRecall);  // not enough for the next step
  CHECK(gold(300.0, 3000.f, -1) == Act::kRecall);    // unknown: BOTS2's thresholds
}
