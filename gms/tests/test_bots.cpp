// gms/core/bots (D6b-shims): the bots' host state without the engine: the mission board, lanes and fronts, team fill
// and the shopping plan (docs/SERVER_LUA_API.md tier 3 *D6b-shims*).
#include <cmath>
#include <cstdint>
#include <string>
#include <vector>

#include "core/bots/bots.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {
bool Near(float a, float b, float eps) { return std::fabs(a - b) <= eps; }

constexpr std::uint32_t kWellA = 0x2001, kWellB = 0x2002, kBot1 = 0x3001, kBot2 = 0x3002, kBot3 = 0x3003;
constexpr std::uint32_t kGuid = 0xF5D7FD28;  // any condition GUID

// Positions for the range test: the two wells and nothing else.
bool WellPos(std::uint32_t id, Vec3* p) {
  if (id == kWellA) {
    *p = MakeVec(-182.3f, 27.9f, 0.f);
    return true;
  }
  if (id == kWellB) {
    *p = MakeVec(28.2f, -182.6f, 0.f);
    return true;
  }
  return false;
}

// SM_Map_v5's Top lane (`blue_top`, MATCH_FLOW.md §2.4) and, for the tests, its mirror across y = x as the Bottom lane.
LanePath V5Top() {
  const float k[10][2] = {{-210.5f, -184.9f}, {-171.7f, -146.0f}, {-153.1f, -114.8f}, {-131.6f, -44.2f},
                          {-106.4f, 28.7f},   {-69.6f, 68.0f},    {-24.2f, 110.1f},   {43.5f, 131.4f},
                          {114.9f, 152.7f},   {144.7f, 165.5f}};
  LanePath l;
  l.lane = kLaneTop;
  for (const auto& p : k) l.knots.push_back(MakeVec(p[0], p[1], 0.f));
  return l;
}
LanePath V5BottomMirror() {
  LanePath l = V5Top();
  l.lane = kLaneBottom;
  for (Vec3& v : l.knots) std::swap(v.x, v.y);
  return l;
}

Unit U(int team, float x, float y) {
  Unit u;
  u.team = team;
  u.pos = MakeVec(x, y, 0.f);
  return u;
}

ShaperType T(const char* a, const char* s, const char* d) {
  ShaperType t;
  t.archetype = a;
  t.attackStyle = s;
  t.damageType = d;
  return t;
}
Slot S(std::vector<ShaperType> any) {
  Slot s;
  s.shaperTypes = std::move(any);
  return s;
}

// Tuning.BotPlan (AGENT_AI.md §11.2) and the plan bots' shaper values (PlayerClass).
std::vector<Slot> PlanSlots() {
  return {S({T("carry", "ranged", "any")}), S({T("mage", "any", "any"), T("assassin", "any", "magical")}),
          S({T("tank", "any", "any"), T("support", "melee", "any")}), S({T("support", "ranged", "any")}),
          S({T("bruiser", "any", "any"), T("assassin", "any", "physical")})};
}
std::vector<BotCandidate> PlanBots() {
  return {{"PC_HighHuntress", T("carry", "ranged", "physical")},
          {"PC_TidalQueen", T("mage", "ranged", "magical")},
          {"PC_Guardian", T("tank", "melee", "physical")},
          {"PC_Artist", T("support", "ranged", "magical")},
          {"PC_Warden", T("bruiser", "melee", "physical")}};
}

// Items (Item.json): the Ambition line, Basic_Life, the potion.
constexpr std::uint32_t kHunger = 1, kDesire = 2, kAmbition = 3, kLife = 4, kPotion = 5, kVigor = 6, kDefiance = 7;
ItemCatalog Catalog() {
  ItemCatalog c;
  c.Add({kHunger, 460, {}, false, 1});
  c.Add({kDesire, 1200, {kHunger}, false, 1});
  c.Add({kAmbition, 2700, {kDesire}, false, 1});
  c.Add({kLife, 380, {}, false, 1});
  c.Add({kPotion, 40, {}, true, 5});
  c.Add({kVigor, 900, {kLife}, false, 1});
  c.Add({kDefiance, 3000, {kVigor}, false, 1});
  return c;
}
}  // namespace

GMS_TEST(BotsMissionRequestDedupes) {
  MissionBoard b;
  const std::uint32_t a = b.Request(1, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 10.0);
  CHECK(a > 0);
  CHECK_EQ(b.Request(1, kAttackWell, kWellA, 3, 0.f, kGuid, nullptr, 11.0), a);  // the same (team, type, target)
  CHECK_EQ(b.Find(a)->agents, 3);
  CHECK(Near(static_cast<float>(b.Find(a)->refreshed), 11.f, 1e-6f));
  CHECK(Near(static_cast<float>(b.Find(a)->opened), 10.f, 1e-6f));
  const std::uint32_t other = b.Request(1, kAttackWell, kWellB, 2, 0.f, kGuid, nullptr, 11.0);
  CHECK(other != a);
  CHECK(b.Request(2, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 11.0) != a);  // the other team's own mission
  CHECK_EQ(b.Request(0, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 11.0), 0u);  // NPC team: none
  CHECK_EQ(b.Request(1, kMissionNone, kWellA, 2, 0.f, kGuid, nullptr, 11.0), 0u);
  CHECK_EQ(b.Request(1, kDefendWell, kWellA, 0, 5.f, 0, nullptr, 11.0) > 0, true);  // agents 0 counts as 1
  CHECK_EQ(b.Find(b.Request(1, kDefendWell, kWellA, 0, 5.f, 0, nullptr, 11.0))->agents, 1);
  CHECK_EQ(static_cast<int>(b.Count()), 4);
  CHECK(b.IsAvailable(1, kAttackWell));
  CHECK(!b.IsAvailable(1, kDefendBase));
  CHECK(b.IsAvailable(2, kAttackWell));
}

GMS_TEST(BotsMissionFindAndAccept) {
  MissionBoard b;
  const std::uint32_t a = b.Request(1, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 0.0);
  const std::uint32_t bb = b.Request(1, kAttackWell, kWellB, 2, 0.f, kGuid, nullptr, 0.0);
  const Vec3 nearA = MakeVec(-150.f, 20.f, 0.f);  // 33 from well A, ~285 from well B
  CHECK_EQ(b.FindAvailable(kBot1, 1, kAttackWell, nearA, 0.f, WellPos), a);    // nearest without a range
  CHECK_EQ(b.FindAvailable(kBot1, 1, kAttackWell, nearA, 80.f, WellPos), a);   // Main -> Well: range 80
  CHECK_EQ(b.FindAvailable(kBot1, 1, kAttackWell, nearA, 20.f, WellPos), 0u);  // out of range
  CHECK_EQ(b.FindAvailable(kBot1, 2, kAttackWell, nearA, 0.f, WellPos), 0u);   // the other team's board
  CHECK_EQ(b.FindAvailable(kBot1, 1, kDefendWell, nearA, 0.f, WellPos), 0u);   // another type
  CHECK(b.Accept(kBot1, 1, a));
  CHECK(b.Accept(kBot2, 1, a));
  CHECK(!b.Accept(kBot3, 1, a));  // full (2 agents)
  CHECK_EQ(b.FindAvailable(kBot3, 1, kAttackWell, nearA, 0.f, WellPos), bb);  // A is full: B
  CHECK_EQ(b.FindAvailable(kBot1, 1, kAttackWell, nearA, 0.f, WellPos), a);   // its own mission first, even full
  CHECK(!b.Accept(kBot3, 2, bb));                                             // another team's bot
  CHECK_EQ(b.MissionOf(kBot1), a);
  CHECK_EQ(b.TargetOf(kBot1), kWellA);
  CHECK(b.Accept(kBot1, 1, bb));  // switching leaves A
  CHECK_EQ(b.MissionOf(kBot1), bb);
  CHECK_EQ(static_cast<int>(b.Find(a)->bots.size()), 1);
  CHECK(b.Accept(kBot3, 1, a));  // A has a free place again
  b.Leave(kBot3);
  CHECK_EQ(b.MissionOf(kBot3), 0u);
  CHECK_EQ(b.TargetOf(kBot3), 0u);
  CHECK(!b.Accept(kBot3, 1, 999));  // unknown id
}

GMS_TEST(BotsMissionDestination) {
  MissionBoard b;
  const Vec3 rally = MakeVec(-36.f, -6.f, 0.f);  // condition_Mission_RallyForParasite's Mortal rally point
  const std::uint32_t parasite = 0x4001;
  const std::uint32_t r = b.Request(1, kRally, parasite, 5, 0.f, kGuid, &rally, 0.0);
  // the parasite's position is unknown here: the destination is the place, so the range test still works
  CHECK_EQ(b.FindAvailable(kBot1, 1, kRally, MakeVec(-40.f, -10.f, 0.f), 10.f, WellPos), r);
  CHECK_EQ(b.FindAvailable(kBot1, 1, kRally, MakeVec(100.f, 100.f, 0.f), 10.f, WellPos), 0u);
  CHECK(b.Accept(kBot1, 1, r));
  const Vec3 d = b.DestinationOf(kBot1);
  CHECK(Near(d.x, -36.f, 1e-6f) && Near(d.y, -6.f, 1e-6f));
  // a destination with x or y 0 is no place for the range test (the scripts' `x ~= 0 and y ~= 0`)
  const Vec3 axis = MakeVec(0.f, 50.f, 0.f);
  const std::uint32_t k = b.Request(1, kKillParasite, kWellA, 5, 0.f, kGuid, &axis, 0.0);
  CHECK_EQ(b.FindAvailable(kBot2, 1, kKillParasite, MakeVec(-182.f, 28.f, 0.f), 5.f, WellPos), k);  // the target's
  // without a destination and without a mission: the zero vector
  CHECK(Near(b.DestinationOf(kBot3).x, 0.f, 0.f) && Near(b.DestinationOf(kBot3).y, 0.f, 0.f));
  // a refresh without a destination drops it
  b.Request(1, kRally, parasite, 5, 0.f, kGuid, nullptr, 1.0);
  CHECK(Near(b.DestinationOf(kBot1).x, 0.f, 0.f));
}

GMS_TEST(BotsMissionExpireEndAndGone) {
  MissionBoard b;
  const std::uint32_t tower = 0x5001, enemy = 0x6001;
  const std::uint32_t defend = b.Request(2, kDefendTower, enemy, 1, 5.f, kGuid, nullptr, 100.0);  // timeout 5
  const std::uint32_t well = b.Request(2, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 100.0);    // timeout 0
  CHECK(b.Accept(kBot1, 2, defend));
  CHECK(b.Expire(104.9).empty());
  b.Request(2, kDefendTower, enemy, 1, 5.f, kGuid, nullptr, 104.0);  // refreshed by the next Check
  CHECK(b.Expire(108.9).empty());
  const std::vector<std::uint32_t> ended = b.Expire(109.0);
  CHECK_EQ(static_cast<int>(ended.size()), 1);
  CHECK_EQ(ended.empty() ? 0u : ended[0], defend);
  CHECK_EQ(b.MissionOf(kBot1), 0u);       // released
  CHECK(b.Find(well) != nullptr);         // timeout 0: only CheckEnd or the target's end close it
  CHECK(b.Expire(100000.0).empty());
  CHECK(b.Accept(kBot2, 2, well));
  CHECK(b.End(well));
  CHECK(!b.End(well));
  CHECK_EQ(b.MissionOf(kBot2), 0u);
  // the target leaves the game: its missions end; a bot leaves the game: it leaves its mission
  const std::uint32_t t = b.Request(1, kDefendTower, tower, 1, 0.f, kGuid, nullptr, 0.0);
  const std::uint32_t w = b.Request(1, kAttackWell, kWellB, 2, 0.f, kGuid, nullptr, 0.0);
  CHECK(b.Accept(kBot3, 1, w));
  const std::vector<std::uint32_t> gone = b.OnObjectGone(tower);
  CHECK_EQ(static_cast<int>(gone.size()), 1);
  CHECK(b.Find(t) == nullptr);
  b.OnObjectGone(kBot3);
  CHECK_EQ(b.MissionOf(kBot3), 0u);
  CHECK(b.Find(w) != nullptr && b.Find(w)->bots.empty());
  CHECK_EQ(static_cast<int>(b.Open(1).size()), 1);
  CHECK_EQ(static_cast<int>(b.Open(2).size()), 0);
  // a new mission for an ended key gets a new id
  const std::uint32_t again = b.Request(2, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 200.0);
  CHECK(again != well && again > w);
}

GMS_TEST(BotsLanesProjectAndClosest) {
  const LanePath top = V5Top();
  const LanePath bottom = V5BottomMirror();
  CHECK(Near(top.Length(), 536.0f, 1.0f));  // MATCH_FLOW §2.4: blue_top 536.0
  float d = 0.f;
  CHECK(Near(top.Project(MakeVec(-210.5f, -184.9f, 0.f), &d), 0.f, 1e-3f) && Near(d, 0.f, 1e-3f));
  CHECK(Near(top.Project(MakeVec(144.7f, 165.5f, 0.f), &d), top.Length(), 1e-2f));
  const Vec3 mid = top.PointAt(top.Length() * 0.5f);
  CHECK(Near(top.Project(mid, &d), top.Length() * 0.5f, 1e-2f) && d < 1e-3f);
  const Vec3 dir = top.DirectionAt(0.f);  // knot 0 -> 1: up and right
  CHECK(dir.x > 0.f && dir.y > 0.f && Near(std::sqrt(dir.x * dir.x + dir.y * dir.y), 1.f, 1e-4f));
  const std::vector<LanePath> lanes = {top, bottom};
  // the twelve Bindings (SM_Map_v5_towers): y - x > 0 is Top
  const float bindings[12][2] = {{-116.3f, 28.9f},  {-124.5f, -44.4f}, {-158.4f, -111.0f}, {28.8f, -116.4f},
                                 {-44.3f, -124.4f}, {-110.9f, -158.4f}, {-28.8f, 116.4f},  {44.3f, 124.4f},
                                 {110.9f, 158.4f},  {116.3f, -28.9f},  {124.5f, 44.4f},    {158.4f, 111.0f}};
  for (const auto& p : bindings) {
    const int byLane = ClosestLane(lanes, MakeVec(p[0], p[1], 0.f));
    CHECK_EQ(byLane, LaneByDiagonal(p[0], p[1]));
  }
  CHECK_EQ(LaneByDiagonal(-116.3f, 28.9f), static_cast<int>(kLaneTop));
  CHECK_EQ(LaneByDiagonal(28.8f, -116.4f), static_cast<int>(kLaneBottom));
  CHECK_EQ(ClosestLane({}, MakeVec(0.f, 0.f, 0.f)), static_cast<int>(kLaneNone));
}

GMS_TEST(BotsLaneFrontRules) {
  const LanePath top = V5Top();
  const float len = top.Length();
  // no minions: the middle of the lane, for both teams the same point, directions opposite
  Front m = ComputeFront(top, 1, {});
  Front s = ComputeFront(top, 2, {});
  CHECK(Near(m.progress, len * 0.5f, 1e-2f) && Near(s.progress, len * 0.5f, 1e-2f));
  CHECK(Near(m.center.x, s.center.x, 1e-3f) && Near(m.center.y, s.center.y, 1e-3f));
  CHECK(Near(m.direction.x, -s.direction.x, 1e-5f) && Near(m.direction.y, -s.direction.y, 1e-5f));
  CHECK(m.direction.x > 0.f);  // Mortal looks toward the Spirit end
  // Mortal minions only: the front minion
  const Vec3 k4 = top.knots[4];
  Front f1 = ComputeFront(top, 1, {U(1, top.knots[2].x, top.knots[2].y), U(1, k4.x, k4.y)});
  CHECK(Near(f1.progress, top.Project(k4), 1e-2f) && f1.own == 2 && f1.enemy == 0);
  // the same minions seen by Spirit: enemy only -> the least advanced enemy from Spirit's base = knot 4 too
  Front f2 = ComputeFront(top, 2, {U(1, top.knots[2].x, top.knots[2].y), U(1, k4.x, k4.y)});
  CHECK(Near(f2.progress, len - top.Project(k4), 1e-2f) && f2.enemy == 2);
  CHECK(Near(f2.center.x, k4.x, 1e-2f) && Near(f2.center.y, k4.y, 1e-2f));
  // both: the midpoint between the fronts (Mortal at knot 4, Spirit at knot 6)
  const Vec3 k6 = top.knots[6];
  Front both = ComputeFront(top, 1, {U(1, k4.x, k4.y), U(2, k6.x, k6.y)});
  CHECK(Near(both.progress, 0.5f * (top.Project(k4) + top.Project(k6)), 1e-2f));
  // a minion off the lane (the jungle, 40 away) doesn't count
  Front off = ComputeFront(top, 1, {U(1, -60.f, -100.f)});
  CHECK(off.own == 0 && Near(off.progress, len * 0.5f, 1e-2f));
  // an NPC-team unit doesn't count
  CHECK_EQ(ComputeFront(top, 1, {U(0, k4.x, k4.y)}).own + ComputeFront(top, 1, {U(0, k4.x, k4.y)}).enemy, 0);
  // the fronts cache
  LaneFronts fr;
  fr.SetLanes({top, V5BottomMirror()});
  fr.Update({U(1, k4.x, k4.y)});
  Front g;
  CHECK(fr.Get(kLaneTop, 1, &g) && Near(g.progress, top.Project(k4), 1e-2f));
  CHECK(fr.Get(kLaneBottom, 2, &g) && g.own == 0 && g.enemy == 0);
  CHECK(!fr.Get(kLaneNone, 1, &g));
  CHECK(!fr.Get(kLaneTop, 0, &g));
  CHECK_EQ(fr.Closest(MakeVec(-131.6f, -44.2f, 0.f)), static_cast<int>(kLaneTop));
}

GMS_TEST(BotsLaneAssignments) {
  BotLanes l;
  CHECK_EQ(l.LaneOf(kBot1), static_cast<int>(kLaneNone));
  l.Assign(kBot1, kLaneTop);
  l.Assign(kBot2, kLaneBottom);
  l.Assign(kBot3, kLaneBottom);
  CHECK_EQ(l.LaneOf(kBot1), static_cast<int>(kLaneTop));
  CHECK_EQ(l.CountOn({kBot1, kBot2, kBot3}, kLaneBottom), 2);
  l.Assign(kBot3, kLaneTop);  // ability_Bot_AssignToSiegeLane
  CHECK_EQ(l.CountOn({kBot1, kBot2, kBot3}, kLaneTop), 2);
  l.Assign(kBot3, kLaneNone);  // anything but Top/Bottom clears it
  CHECK_EQ(l.LaneOf(kBot3), static_cast<int>(kLaneNone));
  l.Assign(kBot2, 7);
  CHECK_EQ(l.LaneOf(kBot2), static_cast<int>(kLaneNone));
}

GMS_TEST(BotsTeamFillFromBotPlan) {
  // five bots: the plan's bots fill the five slots in order; slots 0-1 Top, 2-4 Bottom (topLaners 2, botLaners 3)
  std::vector<Seat> seats = FillTeam(PlanSlots(), PlanBots(), {}, 5, 2, 3);
  CHECK_EQ(static_cast<int>(seats.size()), 5);
  const char* expect[5] = {"PC_HighHuntress", "PC_TidalQueen", "PC_Guardian", "PC_Artist", "PC_Warden"};
  const int lanes[5] = {kLaneTop, kLaneTop, kLaneBottom, kLaneBottom, kLaneBottom};
  for (int i = 0; i < 5 && i < static_cast<int>(seats.size()); ++i) {
    CHECK_EQ(seats[static_cast<size_t>(i)].botClass, std::string(expect[i]));
    CHECK_EQ(seats[static_cast<size_t>(i)].slot, i);
    CHECK_EQ(seats[static_cast<size_t>(i)].lane, lanes[i]);
    CHECK(!seats[static_cast<size_t>(i)].human);
  }
  // a human Alpha (mage, ranged, magical) takes the mage slot; the bots fill the other four, TidalQueen stays out
  seats = FillTeam(PlanSlots(), PlanBots(), {T("mage", "ranged", "magical")}, 5, 2, 3);
  CHECK_EQ(static_cast<int>(seats.size()), 5);
  CHECK(seats[0].human && seats[0].slot == 1 && seats[0].lane == kLaneTop);
  std::vector<std::string> bots;
  for (const Seat& s : seats)
    if (!s.human) bots.push_back(s.botClass);
  CHECK_EQ(static_cast<int>(bots.size()), 4);
  for (const std::string& b : bots) CHECK(b != "PC_TidalQueen");
  // a human Otter (bruiser) and a human Gunman (carry): slots 4 and 0
  seats = FillTeam(PlanSlots(), PlanBots(), {T("bruiser", "melee", "physical"), T("carry", "ranged", "physical")}, 5, 2,
                   3);
  CHECK(seats[0].slot == 4 && seats[1].slot == 0);
  CHECK_EQ(seats.size() > 2 ? seats[2].botClass : std::string(), std::string("PC_TidalQueen"));  // slot 1
  // two humans of the same kind: the second takes the first free slot
  seats = FillTeam(PlanSlots(), PlanBots(), {T("tank", "melee", "physical"), T("tank", "melee", "magical")}, 5, 2, 3);
  CHECK(seats[0].slot == 2 && seats[1].slot == 0);
  // a smaller team: 3 seats with one human -> 2 bots
  seats = FillTeam(PlanSlots(), PlanBots(), {T("support", "ranged", "magical")}, 3, 2, 3);
  CHECK_EQ(static_cast<int>(seats.size()), 3);
  CHECK(seats[0].slot == 3 && seats[0].lane == kLaneBottom);
  CHECK(seats[1].botClass == "PC_HighHuntress" && seats[2].botClass == "PC_TidalQueen");
  // a slot no plan bot matches takes the first unused one
  std::vector<BotCandidate> odd = {{"PC_X", T("mage", "ranged", "magical")}};
  seats = FillTeam(PlanSlots(), odd, {}, 5, 2, 3);
  CHECK(seats.size() == 1 && seats[0].slot == 0 && seats[0].botClass == "PC_X");
  // more humans than slots: slot -1, no lane
  std::vector<ShaperType> six(6, T("tank", "melee", "physical"));
  seats = FillTeam(PlanSlots(), PlanBots(), six, 5, 2, 3);
  CHECK(seats.size() == 6 && seats[5].slot == -1 && seats[5].lane == kLaneNone);
}

GMS_TEST(BotsShoppingBuildOrder) {
  const ItemCatalog c = Catalog();
  CHECK_EQ(c.PriceWith(kDesire, kHunger), 740);  // Item_GetCost 0x008FCB30: 1200 - 460
  CHECK_EQ(c.PriceWith(kAmbition, kDesire), 1500);
  // D15: Item_GetCost's discount is recursive (0x008FCA90 walks UpgradesInto): Ambition over Hunger pays 2700 - 460
  CHECK_EQ(c.PriceWith(kAmbition, kHunger), 2240);
  CHECK_EQ(c.PriceWith(kAmbition, kLife), 2700);   // Life is no component of Ambition: full price
  CHECK_EQ(c.PriceWith(kAmbition, kAmbition), 2700);
  CHECK_EQ(c.PriceWith(kHunger, kAmbition), 460);  // the other way round: no discount
  CHECK(c.Covers(kAmbition, kHunger) && !c.Covers(kHunger, kAmbition));
  // the starting items (Basic_Life, the potion), then Ambition, then Defiance (built on Basic_Life)
  ShopPlanner plan(&c, {kLife, kPotion, kAmbition, kDefiance});
  Inventory inv;
  Purchase p;
  CHECK(plan.Next(inv, &p) && p.item == kLife && p.price == 380 && p.replaces == 0);
  CHECK(plan.CanAffordNext(inv, 500.f));
  CHECK(!plan.CanAffordNext(inv, 379.f));
  inv.items = {kLife};
  CHECK(plan.Next(inv, &p) && p.item == kHunger && p.price == 460);  // Ambition's first step
  inv.items = {kLife, kHunger};
  CHECK(plan.Next(inv, &p) && p.item == kDesire && p.price == 740 && p.replaces == kHunger);
  inv.items = {kLife, kDesire};
  CHECK(plan.Next(inv, &p) && p.item == kAmbition && p.price == 1500 && p.replaces == kDesire);
  inv.items = {kLife, kAmbition};
  CHECK(plan.Next(inv, &p) && p.item == kVigor && p.price == 520 && p.replaces == kLife);  // 900 - 380
  inv.items = {kVigor, kAmbition};  // Basic_Life is used up, but covered by Vigor
  CHECK(plan.Next(inv, &p) && p.item == kDefiance && p.price == 2100 && p.replaces == kVigor);
  inv.items = {kDefiance, kAmbition};
  CHECK(!plan.Next(inv, &p));  // the build is done
  // full slots block a new item, not an upgrade
  inv.items = {9, 9, 9, 9, 9, kLife};
  CHECK(!plan.Next(inv, &p));  // Hunger would need a slot
  inv.items = {9, 9, 9, 9, kHunger, kLife};
  CHECK(plan.Next(inv, &p) && p.item == kDesire);
  // unknown items in the build are skipped
  ShopPlanner unknown(&c, {42, kLife});
  inv.items.clear();
  CHECK(unknown.Next(inv, &p) && p.item == kLife);
}

GMS_TEST(BotsShoppingPotions) {
  const ItemCatalog c = Catalog();
  ShopPlanner plan(&c, {kLife});
  Inventory inv;
  CHECK_EQ(plan.PotionsToBuy(inv, 500.f, 40), 2);
  CHECK_EQ(plan.PotionsToBuy(inv, 79.f, 40), 1);
  CHECK_EQ(plan.PotionsToBuy(inv, 39.f, 40), 0);
  inv.potions = 1;
  CHECK_EQ(plan.PotionsToBuy(inv, 500.f, 40), 1);
  inv.potions = 2;
  CHECK_EQ(plan.PotionsToBuy(inv, 500.f, 40), 0);
  CHECK_EQ(plan.PotionsToBuy(inv, 500.f, 40, 5), 3);
  CHECK_EQ(plan.PotionsToBuy(inv, 500.f, 0), 0);
}

GMS_TEST(BotsStateForget) {
  BotsState st;
  st.bots.insert(kBot1);
  st.firstTimePlayers.insert(0x7001);
  st.lanes.Assign(kBot1, kLaneTop);
  const std::uint32_t m = st.missions.Request(1, kKillShaper, 0x7001, 2, 0.f, 0, nullptr, 0.0);
  CHECK(st.missions.Accept(kBot1, 1, m));
  CHECK(st.IsBot(kBot1));
  CHECK(st.AnyFirstTime(std::vector<std::uint32_t>{0x7002, 0x7001}));
  CHECK(!st.AnyFirstTime(std::vector<std::uint32_t>{0x7002}));
  st.Forget(0x7001);  // the target human left: its KillShaper mission ends
  CHECK(st.missions.Find(m) == nullptr);
  CHECK(!st.AnyFirstTime(std::vector<std::uint32_t>{0x7001}));
  st.Forget(kBot1);
  CHECK(!st.IsBot(kBot1));
  CHECK_EQ(st.lanes.LaneOf(kBot1), static_cast<int>(kLaneNone));
}

// D6b: the plan layer's mission rule (BotsState::EnterPlan, RefreshOwnMission).
GMS_TEST(BotsPlanMissionRules) {
  BotsState st;
  constexpr std::uint32_t kEnemy = 0x7001, kEnemy2 = 0x7002;
  const std::uint32_t well = st.missions.Request(1, kAttackWell, kWellA, 2, 0.f, kGuid, nullptr, 0.0);
  // Main -> Well by condition_Bot_IsMissionAvailable(3, 80): the link's id is accepted
  PlanEntry wellPlan;
  wellPlan.missionType = kAttackWell;
  wellPlan.linkMission = well;
  std::uint32_t held = 0;
  CHECK(st.EnterPlan(kBot1, 1, wellPlan, 1.0, &held) == PlanMission::kAccepted);
  CHECK_EQ(held, well);
  CHECK(st.EnterPlan(kBot2, 1, wellPlan, 1.0) == PlanMission::kAccepted);
  CHECK(st.EnterPlan(kBot3, 1, wellPlan, 1.0, &held) == PlanMission::kRefused);  // full (2 agents)
  CHECK_EQ(held, 0u);
  CHECK(st.EnterPlan(kBot1, 2, wellPlan, 1.0) == PlanMission::kRefused);  // the other team's bot can't take it
  CHECK_EQ(st.missions.MissionOf(kBot1), 0u);  // ... and holds nothing afterwards
  CHECK(st.EnterPlan(kBot1, 1, wellPlan, 1.0) == PlanMission::kAccepted);
  // a mission plan entered without a link id (none of the data's links does that): the bot keeps what it holds
  PlanEntry noLink;
  noLink.missionType = kDefendBase;
  CHECK(st.EnterPlan(kBot1, 1, noLink, 2.0, &held) == PlanMission::kKept);
  CHECK_EQ(held, well);
  // Well -> Flee (no missionType): the bot leaves, the mission stays open for the others
  PlanEntry flee;
  CHECK(st.EnterPlan(kBot1, 1, flee, 3.0, &held) == PlanMission::kLeft);
  CHECK_EQ(held, 0u);
  CHECK(st.missions.Find(well) != nullptr && st.missions.Find(well)->bots.size() == 1u);
  CHECK(st.EnterPlan(kBot1, 1, flee, 3.0) == PlanMission::kNone);
  CHECK(st.EnterPlan(kBot3, 1, wellPlan, 3.0) == PlanMission::kAccepted);  // the freed place
  // Main -> EngageForKill (requestMission, KillShaper 8) without a link id: its own mission on its target
  PlanEntry engage;
  engage.missionType = kKillShaper;
  engage.requestMission = true;
  engage.target = kEnemy;
  CHECK(st.EnterPlan(kBot1, 1, engage, 4.0, &held) == PlanMission::kOpened);
  const Mission* own = st.missions.Find(held);
  CHECK(own && own->type == kKillShaper && own->target == kEnemy && own->agents == kOwnMissionAgents);
  CHECK(own && own->timeout == kOwnMissionTimeout && own->bots.size() == 1u);
  const std::uint32_t ownId = held;
  // an ally joins through IsMissionAvailable(8, 60) into its own EngageForKill plan: it accepts, opens nothing
  PlanEntry join = engage;
  join.linkMission = ownId;
  join.target = kEnemy2;
  CHECK(st.EnterPlan(kBot2, 1, join, 4.5, &held) == PlanMission::kAccepted);
  CHECK_EQ(held, ownId);
  CHECK(st.missions.Find(well)->bots.size() == 1u);  // kBot2 left the well mission for it
  // every think: the requester refreshes, the joiner rides along; both keep the id
  CHECK_EQ(st.RefreshOwnMission(kBot1, 1, kKillShaper, kEnemy2, 8.0), ownId);
  CHECK_EQ(st.RefreshOwnMission(kBot2, 1, kKillShaper, kEnemy2, 12.0), ownId);
  CHECK(st.missions.Find(ownId)->refreshed == 8.0);
  st.missions.Expire(12.0);  // 4 s after the requester's last refresh: still open
  CHECK(st.missions.Find(ownId) != nullptr);
  st.missions.Expire(13.0);  // 5 s: it ends, both bots are free
  CHECK(st.missions.Find(ownId) == nullptr);
  CHECK_EQ(st.missions.MissionOf(kBot2), 0u);
  // without a target a requestMission plan opens nothing
  PlanEntry noTarget = engage;
  noTarget.target = 0;
  CHECK(st.EnterPlan(kBot2, 1, noTarget, 14.0, &held) == PlanMission::kKept);
  CHECK_EQ(held, 0u);
  CHECK_EQ(st.RefreshOwnMission(kBot2, 1, kKillShaper, 0, 14.0), 0u);
  CHECK_EQ(st.RefreshOwnMission(kBot2, 1, kKillShaper, kEnemy, 14.0) > 0, true);  // a target came: opened
  // a link id that is gone (ended meanwhile) into a requestMission plan: its own instead
  join.linkMission = 999;
  CHECK(st.EnterPlan(kBot3, 1, join, 15.0, &held) == PlanMission::kOpened);
  CHECK(st.missions.Find(held) && st.missions.Find(held)->target == kEnemy2);
  CHECK(std::string(PlanMissionName(PlanMission::kOpened)) == "opened");
}

GMS_TEST(BotsInventoryFromSlots) {
  // 11 slots: stat items 0-5, spells 6-8, consumables 9-10 (cLabsPlayer mItems)
  const std::uint32_t items[11] = {kLife, 0, kHunger, 0, 0, 0, 0x5001, 0, 0, kPotion, 0};
  const int counts[11] = {1, 0, 1, 0, 0, 0, 1, 0, 0, 3, 0};
  Inventory inv = InventoryFromSlots(items, counts, 11, kPotion);
  CHECK_EQ(static_cast<int>(inv.items.size()), 6);
  CHECK_EQ(inv.Used(), 2);
  CHECK(inv.Owns(kLife) && inv.Owns(kHunger) && !inv.Owns(0x5001));
  CHECK_EQ(inv.potions, 3);
  const std::uint32_t two[11] = {0, 0, 0, 0, 0, 0, 0, 0, 0, kPotion, kPotion};
  const int twoCounts[11] = {0, 0, 0, 0, 0, 0, 0, 0, 0, 5, 1};
  CHECK_EQ(InventoryFromSlots(two, twoCounts, 11, kPotion).potions, 6);
  CHECK_EQ(InventoryFromSlots(two, twoCounts, 11, 0).potions, 0);
}

// D6a: the game's enum values to ShaperType, and the bot players of a practice match.
GMS_TEST(BotsShaperTypeEnums) {
  const ShaperType alpha = ShaperTypeOf(2, 1, 1);  // PC_Alpha: mage, ranged, magical
  CHECK(alpha.archetype == "mage" && alpha.attackStyle == "ranged" && alpha.damageType == "magical");
  const ShaperType warden = ShaperTypeOf(1, 0, 0);
  CHECK(warden.archetype == "bruiser" && warden.attackStyle == "melee" && warden.damageType == "physical");
  const ShaperType slot0 = SlotPatternOf(0, 1, 2);  // carry, ranged, any
  CHECK(slot0.archetype == "carry" && slot0.attackStyle == "ranged" && slot0.damageType == "any");
  CHECK(TypeMatches(slot0, ShaperTypeOf(0, 1, 0)));
  CHECK(!TypeMatches(slot0, alpha));
  CHECK(TypeMatches(SlotPatternOf(-1, 2, 2), warden));  // archetype -1 = any
  CHECK(ShaperTypeOf(0, 2, 2).attackStyle == "hybrid");
}

GMS_TEST(BotsPlanBotPlayers) {
  // the practice match: one human Alpha at index 0 on team 1, five seats a team
  HumanPlayer h;
  h.index = 0;
  h.team = 1;
  h.shaper = T("mage", "ranged", "magical");
  std::vector<BotPlayer> bots = PlanBotPlayers(PlanSlots(), PlanBots(), {h}, 5, {1, 2}, 2, 3);
  CHECK_EQ(static_cast<int>(bots.size()), 9);
  for (std::size_t i = 0; i < bots.size(); ++i) {
    CHECK_EQ(bots[i].index, static_cast<int>(i) + 1);  // 1-9, team 1's four first
    CHECK_EQ(bots[i].onlineId, static_cast<std::uint64_t>(i) + 2);
    CHECK_EQ(bots[i].team, i < 4 ? 1 : 2);
  }
  CHECK(bots[0].botClass == "PC_HighHuntress" && bots[0].lane == kLaneTop && bots[0].slot == 0);
  CHECK(bots[1].botClass == "PC_Guardian" && bots[1].lane == kLaneBottom);  // the human holds the mage slot (Top)
  CHECK(bots[4].botClass == "PC_HighHuntress" && bots[5].botClass == "PC_TidalQueen" && bots[8].botClass == "PC_Warden");
  CHECK(bots[5].lane == kLaneTop && bots[6].lane == kLaneBottom);
  // the enemy team only, two seats; a reserved index (a roster human not connected yet) is skipped
  bots = PlanBotPlayers(PlanSlots(), PlanBots(), {h}, 2, {2}, 2, 3, 1u << 1);
  CHECK_EQ(static_cast<int>(bots.size()), 2);
  CHECK(bots[0].index == 2 && bots[1].index == 3 && bots[0].team == 2 && bots[0].onlineId == 3);
  // the human's team is full: no bots for it
  bots = PlanBotPlayers(PlanSlots(), PlanBots(), {h}, 1, {1, 2}, 2, 3);
  CHECK(bots.size() == 1 && bots[0].team == 2 && bots[0].index == 1);
  // the indexes run out: at most 10 players
  std::vector<HumanPlayer> many;
  for (int i = 0; i < 8; ++i) {
    HumanPlayer m = h;
    m.index = i;
    m.team = i < 4 ? 1 : 2;
    many.push_back(m);
  }
  bots = PlanBotPlayers(PlanSlots(), PlanBots(), many, 5, {1, 2}, 2, 3);
  CHECK(bots.size() == 2 && bots[0].index == 8 && bots[1].index == 9 && bots[0].team == 1 && bots[1].team == 2);
}

GMS_TEST(BotsWaitingRosterHumanCountsForItsTeam) {
  // G3b (d8b_run6d/run7d): a party of two in a 2 v 2 queue; the guest (roster index 1, team 1) connects first, the
  // leader (index 0, team 1) is still waiting. Before G3b only index 0 was kept free, and team 1 got a bot: a 3 v 2.
  HumanPlayer guest;
  guest.index = 1;
  guest.team = 1;
  guest.shaper = T("bruiser", "melee", "physical");
  HumanPlayer leader;
  leader.index = 0;
  leader.team = 1;
  const std::vector<HumanPlayer> counted = CountedHumans({guest}, {leader});
  CHECK_EQ(static_cast<int>(counted.size()), 2);
  CHECK(counted[1].index == 0 && counted[1].team == 1 && counted[1].shaper.archetype.empty());
  std::vector<BotPlayer> bots = PlanBotPlayers(PlanSlots(), PlanBots(), counted, 2, BotTeams(counted, true), 2, 3);
  CHECK_EQ(static_cast<int>(bots.size()), 2);
  for (const BotPlayer& b : bots) CHECK_EQ(b.team, 2);  // team 1 is full with its two humans
  CHECK(bots[0].index == 2 && bots[1].index == 3);    // indexes 0 and 1 stay the humans'
  // the old input (the waiting leader only as a reserved index) gives team 1 a bot
  bots = PlanBotPlayers(PlanSlots(), PlanBots(), {guest}, 2, {1, 2}, 2, 3, 1u << 0);
  CHECK_EQ(static_cast<int>(bots.size()), 3);
  // a waiting index a connected human holds already (a duplicate roster entry) is not counted twice; a bad index is out
  HumanPlayer bad;
  bad.index = 12;
  bad.team = 2;
  CHECK_EQ(static_cast<int>(CountedHumans({guest}, {guest, bad}).size()), 1);
  // without allies only the team without a human gets bots, waiting humans included
  CHECK(BotTeams(counted, false) == std::vector<int>{2});
  CHECK(BotTeams({}, false) == (std::vector<int>{1, 2}));
  // the solo queue's 1 v 1 with fill (B10d): the first human connected, the second (team 2) waiting: one bot a team
  HumanPlayer a = guest, b = leader;
  a.index = 0;
  b.index = 1;
  b.team = 2;
  bots = PlanBotPlayers(PlanSlots(), PlanBots(), CountedHumans({a}, {b}), 2, {1, 2}, 2, 3);
  CHECK(bots.size() == 2 && bots[0].team == 1 && bots[1].team == 2 && bots[0].index == 2 && bots[1].index == 3);
}

GMS_TEST(BotsTeamSizeRule) {
  // D6a's auto rule: the practice game's descriptors 0x50C have BotsFillTeams; a matchmade game's 0x1 don't
  CHECK_EQ(BotTeamSize(-1, 0, 0x50C), 5);
  CHECK_EQ(BotTeamSize(-1, 0, 0x1), 0);
  CHECK_EQ(BotTeamSize(-1, 0, 0), 0);
  // B10d: createGame's fillTeams (a matchmade game with open seats) wins over the descriptors, whatever the team size
  CHECK_EQ(BotTeamSize(-1, 5, 0x1), 5);
  CHECK_EQ(BotTeamSize(-1, 3, 0x1), 3);
  CHECK_EQ(BotTeamSize(-1, 2, 0x50C), 2);
  // [host] bots=N and bots=0 win over both
  CHECK_EQ(BotTeamSize(0, 5, 0x50C), 0);
  CHECK_EQ(BotTeamSize(4, 5, 0x1), 4);
  // a lone human on team 1 of a 5v5 matchmade game: four allies and five enemies, indexes 1-9
  HumanPlayer h;
  h.index = 0;
  h.team = 1;
  h.shaper = T("mage", "ranged", "magical");
  const std::vector<BotPlayer> bots =
      PlanBotPlayers(PlanSlots(), PlanBots(), {h}, BotTeamSize(-1, 5, 0x1), {1, 2}, 2, 3);
  CHECK_EQ(static_cast<int>(bots.size()), 9);
  CHECK(bots.size() == 9 && bots[3].team == 1 && bots[4].team == 2 && bots[8].index == 9);
}
