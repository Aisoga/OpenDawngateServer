// MAPED4: the bots on compiled maps. The camps' roles from data (core/matchflow/camprole.h), the Hunters' route from the
// level's camps (core/bots/route.h DeriveRoute; on SM_Map_v5 HUNTERROUTE's table again), the parasite's rally points
// from its camp (core/bots/rally.h), the level's own size and shop for the strict check (core/protocol/check.h).
#include <cmath>
#include <string>
#include <vector>

#include "core/bots/rally.h"
#include "core/bots/route.h"
#include "core/matchflow/camprole.h"
#include "core/protocol/check.h"
#include "core/protocol/messages.h"
#include "tests/check.h"

using namespace gms::bots;
using gms::flow::CampRole;
using gms::flow::CampRoleOf;

namespace {

// SM_Map_v5's 22 camps as the director reads them (the host log's "director: camp" lines) with the
// roles their data gives: the buff camps' 2.0 marker, the respawn times (the Boar's 300 s).
struct SmCamp {
  const char* noun;
  float x, y;
  CampRole role;
};
const SmCamp kSmCamps[] = {
    {"SpawnPoint_v5_Buff_Fish", 68.0f, -165.6f, CampRole::kBuff},
    {"SpawnPoint_v5_CreepsFish", 65.7f, -132.2f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFungus", 54.1f, 88.0f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsBoar", 176.1f, -147.5f, CampRole::kBig},
    {"SpawnPoint_v5_Buff_Fungus", -59.4f, -57.4f, CampRole::kBuff},
    {"SpawnPoint_v5_CreepsFungus", -67.4f, 3.3f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsUgger", 170.3f, 8.9f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFish", -65.0f, 130.7f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsUgger", -219.8f, 76.9f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFish", -4.8f, -163.7f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFungus", -54.4f, -88.7f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsBoar", -175.8f, 147.9f, CampRole::kBig},
    {"SpawnPoint_v5_CreepsFish", 4.4f, 164.7f, CampRole::kSmall},
    {"SpawnPoint_v5_Buff_Ugger", -169.1f, 61.5f, CampRole::kBuff},
    {"SpawnPoint_v5_Buff_Fish", -67.6f, 165.9f, CampRole::kBuff},
    {"SpawnPoint_v5_CreepsUgger", 221.0f, -76.5f, CampRole::kSmall},
    {"SpawnPoint_v5_Buff_Fungus", 59.0f, 58.7f, CampRole::kBuff},
    {"SpawnPoint_v5_CreepsFungus", 67.6f, -3.2f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFungus", 86.4f, 52.5f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsUgger", -170.9f, -9.4f, CampRole::kSmall},
    {"SpawnPoint_v5_CreepsFungus", -87.5f, -52.6f, CampRole::kSmall},
    {"SpawnPoint_v5_Buff_Ugger", 167.5f, -62.7f, CampRole::kBuff},
};
const float kSmG1[2] = {-182.f, -182.f}, kSmG2[2] = {182.f, 182.f};

std::vector<CampMarker> SmCamps() {
  std::vector<CampMarker> v;
  for (const SmCamp& c : kSmCamps) {
    CampMarker m;
    m.x = c.x;
    m.y = c.y;
    m.role = c.role;
    m.name = c.noun;
    v.push_back(m);
  }
  return v;
}

// MAPED3's CM_Grove (server/testdata/maps/CM_Grove/map.json and its host log): two buffs and two small camps a side.
std::vector<CampMarker> GroveCamps() {
  struct G {
    const char* noun;
    float x, y;
    CampRole role;
  };
  const G t1[] = {{"SpawnPoint_v5_Buff_Fungus", -100.f, -15.f, CampRole::kBuff},
                  {"SpawnPoint_v5_Buff_Fish", -15.f, -100.f, CampRole::kBuff},
                  {"SpawnPoint_v5_CreepsUgger", -105.f, 60.f, CampRole::kSmall},
                  {"SpawnPoint_v5_CreepsFungus", 60.f, -105.f, CampRole::kSmall}};
  std::vector<CampMarker> v;
  for (int team = 1; team <= 2; ++team)
    for (const G& g : t1) {
      CampMarker m;
      m.x = team == 1 ? g.x : -g.x;
      m.y = team == 1 ? g.y : -g.y;
      m.role = g.role;
      m.name = g.noun;
      v.push_back(m);
    }
  return v;
}
const float kGroveG1[2] = {-142.f, -142.f}, kGroveG2[2] = {142.f, 142.f};

float Len(float x, float y) { return std::sqrt(x * x + y * y); }

// CM_Grove's open ground near the centre (server/testdata/maps/CM_Grove/map.json terrain, "default": "wall"): the pit (a circle of 28
// round the centre), the paths from each side's crossing (-65, -65) / (65, 65) to the pit (width 14), the crossings'
// circles (14), the river along x + y = 0 (width 24, from (-140, 140) to (140, -140)).
float SegDist(float px, float py, float ax, float ay, float bx, float by) {
  const float vx = bx - ax, vy = by - ay;
  float t = ((px - ax) * vx + (py - ay) * vy) / (vx * vx + vy * vy);
  t = t < 0.f ? 0.f : t > 1.f ? 1.f : t;
  return Len(px - (ax + t * vx), py - (ay + t * vy));
}
bool GroveOpen(float x, float y) {
  if (Len(x, y) <= 28.f) return true;
  if (Len(x + 65.f, y + 65.f) <= 14.f || Len(x - 65.f, y - 65.f) <= 14.f) return true;
  if (SegDist(x, y, -65.f, -65.f, -20.f, -20.f) <= 7.f || SegDist(x, y, 65.f, 65.f, 20.f, 20.f) <= 7.f) return true;
  if (SegDist(x, y, -110.f, -110.f, -65.f, -65.f) <= 8.f || SegDist(x, y, 110.f, 110.f, 65.f, 65.f) <= 8.f) return true;
  return SegDist(x, y, -140.f, 140.f, 140.f, -140.f) <= 12.f;
}

}  // namespace

GMS_TEST(MapedCampRoleFromData) {
  // a buff camp: its major's marker 2.0, the minors' 1.0 (SM_Map_v5's six, and CM_Grove's copies of them)
  const float buff[3] = {2.f, 1.f, 1.f};
  CHECK(CampRoleOf(buff, 3, 300.0) == CampRole::kBuff);
  const float buffLast[3] = {1.f, 1.f, 2.f};
  CHECK(CampRoleOf(buffLast, 3, 60.0) == CampRole::kBuff);  // the marker decides, not the timing
  // the Gold Pigs: every marker 1.0, respawn 300 s
  const float pigs[2] = {1.f, 1.f};
  CHECK(CampRoleOf(pigs, 2, 300.0) == CampRole::kBig);
  // the small camps: 60 s, and the 180-s mushroom camp
  const float smalls[4] = {1.f, 1.f, 1.f, 1.f};
  CHECK(CampRoleOf(smalls, 4, 60.0) == CampRole::kSmall);
  CHECK(CampRoleOf(smalls, 4, 180.0) == CampRole::kSmall);
  // one marker, or none: no buff by scale
  const float one[1] = {2.f};
  CHECK(CampRoleOf(one, 1, 60.0) == CampRole::kSmall);
  CHECK(CampRoleOf(nullptr, 0, 300.0) == CampRole::kBig);
  // a small difference isn't a buff marker
  const float close[2] = {1.f, 1.1f};
  CHECK(CampRoleOf(close, 2, 60.0) == CampRole::kSmall);
  CHECK(std::string(gms::flow::CampRoleName(CampRole::kBuff)) == "buff");
}

GMS_TEST(MapedRouteSmIsTheTable) {
  const std::vector<CampMarker> camps = SmCamps();
  const std::vector<RouteStep> t1 = DeriveRoute(camps, 1, kSmG1, kSmG2), table1 = OpeningRoute(1);
  CHECK_EQ(t1.size(), table1.size());
  for (std::size_t i = 0; i < t1.size() && i < table1.size(); ++i) {
    CHECK(t1[i].x == table1[i].x && t1[i].y == table1[i].y);
    CHECK_EQ(t1[i].kind, table1[i].kind);
    CHECK_EQ(t1[i].minLevel, table1[i].minLevel);
  }
  CHECK(t1[0].label == "buff SpawnPoint_v5_Buff_Fish");
  CHECK(t1[10].label == "big SpawnPoint_v5_CreepsBoar");
  // team 2: its own camps (SM_Map_v5 is point-symmetric to 0.5-2 units), the table's mirror in the same order
  const std::vector<RouteStep> t2 = DeriveRoute(camps, 2, kSmG2, kSmG1), table2 = OpeningRoute(2);
  CHECK_EQ(t2.size(), table2.size());
  for (std::size_t i = 0; i < t2.size() && i < table2.size(); ++i) {
    CHECK(Len(t2[i].x - table2[i].x, t2[i].y - table2[i].y) < RouteConfig().matchRadius);  // the host's match
    CHECK_EQ(t2[i].kind, table2[i].kind);
    CHECK_EQ(t2[i].minLevel, table2[i].minLevel);
  }
  // without the Guardians: the diagonal gives the same halves on SM_Map_v5
  const std::vector<RouteStep> d1 = DeriveRoute(camps, 1, nullptr, nullptr);
  CHECK_EQ(d1.size(), t1.size());
  for (std::size_t i = 0; i < d1.size() && i < t1.size(); ++i) CHECK(d1[i].x == t1[i].x && d1[i].y == t1[i].y);
  CHECK(DeriveRoute(camps, 0, kSmG1, kSmG2).empty());
}

GMS_TEST(MapedRouteGrove) {
  const std::vector<CampMarker> camps = GroveCamps();
  for (int team = 1; team <= 2; ++team) {
    const float* own = team == 1 ? kGroveG1 : kGroveG2;
    const float* enemy = team == 1 ? kGroveG2 : kGroveG1;
    const std::vector<RouteStep> r = DeriveRoute(camps, team, own, enemy);
    CHECK_EQ(r.size(), static_cast<std::size_t>(4));
    if (r.size() != 4) continue;
    // from the first buff camp (the walks from both buffs are 330 long: the earlier marker), the Ugger camp beside it,
    // the other buff, the mushroom camp; both buffs from level 1 (only two)
    const float s = team == 1 ? 1.f : -1.f;
    CHECK(r[0].x == -100.f * s && r[0].y == -15.f * s && r[0].kind == kRouteBuff && r[0].minLevel == 1);
    CHECK(r[1].x == -105.f * s && r[1].y == 60.f * s && r[1].kind == kRouteSmall);
    CHECK(r[2].x == -15.f * s && r[2].y == -100.f * s && r[2].kind == kRouteBuff && r[2].minLevel == 1);
    CHECK(r[3].x == 60.f * s && r[3].y == -105.f * s && r[3].kind == kRouteSmall);
    for (const RouteStep& st : r) CHECK(OnOwnHalf(st.x, st.y, team, own, enemy));
  }
}

GMS_TEST(MapedRouteEdges) {
  // no buff camp on the half: the shortest walk from any camp; a big camp first is still taken at level 1
  std::vector<CampMarker> c(3);
  c[0].x = -50.f, c[0].y = -50.f, c[0].role = CampRole::kSmall;
  c[1].x = -150.f, c[1].y = -50.f, c[1].role = CampRole::kBig;
  c[2].x = -60.f, c[2].y = -40.f, c[2].role = CampRole::kSmall;
  std::vector<RouteStep> r = DeriveRoute(c, 1, nullptr, nullptr);
  CHECK_EQ(r.size(), static_cast<std::size_t>(3));
  CHECK(r.size() == 3 && r[0].minLevel == 1 && r.back().kind == kRoutePigs && r.back().minLevel == 3);
  // nothing on the half: no route (the host's default camp route then)
  CHECK(DeriveRoute(c, 2, nullptr, nullptr).empty());
  CHECK(DeriveRoute(std::vector<CampMarker>(), 1, kSmG1, kSmG2).empty());
  // a third buff and later from level 3; at most 32 steps
  std::vector<CampMarker> many;
  for (int i = 0; i < 40; ++i) {
    CampMarker m;
    m.x = -10.f - 5.f * static_cast<float>(i);
    m.y = -10.f;
    m.role = i < 4 ? CampRole::kBuff : CampRole::kSmall;
    many.push_back(m);
  }
  r = DeriveRoute(many, 1, nullptr, nullptr);
  CHECK_EQ(r.size(), static_cast<std::size_t>(32));
  int buffs = 0;
  for (const RouteStep& st : r) {
    if (st.kind != kRouteBuff) continue;
    ++buffs;
    CHECK_EQ(st.minLevel, buffs <= 2 ? 1 : 3);
  }
  CHECK_EQ(buffs, 4);
  // the route's labels are the camp's role and noun (a camp without a noun: "camp")
  CHECK(r[0].label == "buff camp");
}

GMS_TEST(MapedRallyFromTheParasiteCamp) {
  const RallyConfig cfg;
  float p[2] = {};
  // open everywhere: 62 from the parasite toward the Guardian
  auto all = [](float, float) { return true; };
  CHECK(ParasiteRallyPoint(-3.4f, 3.2f, -182.f, -182.f, cfg, all, p) == RallyQuality::kOpenWithLine);
  CHECK(std::fabs(Len(p[0] + 3.4f, p[1] - 3.2f) - 62.f) < 0.01f);
  CHECK(p[0] < -40.f && p[1] < -40.f);
  // CM_Grove: (66, -10) and (-66, -10) are walls; the derived points are open, on the team's path, 47-62 out
  CHECK(!GroveOpen(66.f, -10.f) && !GroveOpen(-66.f, -10.f));
  for (int team = 1; team <= 2; ++team) {
    const float g = team == 1 ? -142.f : 142.f;
    const RallyQuality q = ParasiteRallyPoint(-3.4f, 3.2f, g, g, cfg, GroveOpen, p);
    CHECK(q == RallyQuality::kOpenWithLine);
    CHECK(GroveOpen(p[0], p[1]));
    const float d = Len(p[0] + 3.4f, p[1] - 3.2f);
    CHECK(d >= cfg.minDist - 0.01f && d <= cfg.dist + 0.01f);
    CHECK(team == 1 ? p[0] + p[1] < 0.f : p[0] + p[1] > 0.f);
  }
  // a wall on the straight line: the direction turns until the line is open
  auto wallAhead = [](float x, float y) { return !(x < -20.f && x > -60.f && y < -20.f && y > -60.f); };
  CHECK(ParasiteRallyPoint(0.f, 0.f, -100.f, -100.f, cfg, wallAhead, p) == RallyQuality::kOpenWithLine);
  CHECK(wallAhead(p[0], p[1]));
  // walls everywhere: the plain point, unchecked
  auto none = [](float, float) { return false; };
  CHECK(ParasiteRallyPoint(0.f, 0.f, -100.f, -100.f, cfg, none, p) == RallyQuality::kUnchecked);
  CHECK(std::fabs(p[0] + 43.84f) < 0.01f && std::fabs(p[1] + 43.84f) < 0.01f);
  // the Guardian on the parasite: toward the map's low corner
  ParasiteRallyPoint(0.f, 0.f, 0.f, 0.f, cfg, all, p);
  CHECK(p[0] < 0.f && p[1] < 0.f);
  // which levels derive: [host] bot_parasite_rally
  CHECK(!RallyDerivedOn(1, "SM_Map_v5.Level") && !RallyDerivedOn(1, "sm_map_v5"));
  CHECK(RallyDerivedOn(1, "CM_Grove.Level") && RallyDerivedOn(1, "SM_Map_v4.Level") && RallyDerivedOn(1, "SM_Map_v5x"));
  CHECK(RallyDerivedOn(2, "SM_Map_v5.Level") && !RallyDerivedOn(0, "CM_Grove.Level"));
  // the ward spots: checked on a compiled map, SM_Map_v5 keeps the data's ([host] bot_ward_spot_check)
  CHECK(IsSmLevel("SM_Map_v5.Level") && !IsSmLevel("SM_Map_v5x") && !IsSmLevel("CM_Ring.Level") && !IsSmLevel(""));
  CHECK(WardSpotCheckedOn(1, "CM_Ring.Level") && !WardSpotCheckedOn(1, "SM_Map_v5.Level"));
  CHECK(WardSpotCheckedOn(2, "SM_Map_v5.Level") && !WardSpotCheckedOn(0, "CM_Ring.Level"));
}

GMS_TEST(MapedLevelSizeAndShop) {
  namespace ck = gms::check;
  const std::uint32_t grove = gms::AssetIdOf("CM_Grove.Level");
  const unsigned g0 = ck::LevelDataGeneration();
  // an unknown level: +-300 until the host read its size
  CHECK(ck::BoundsForLevel(grove).maxX == 300.f);
  ck::SetLevelSize(grove, 450.f, 450.f);
  CHECK(ck::LevelDataGeneration() != g0);
  const ck::Bounds b = ck::BoundsForLevel(grove);
  CHECK(b.maxX == 227.f && b.minY == -227.f);
  CHECK(!b.Contains(240.f, 0.f, 0.f));
  // another level keeps its table entry
  CHECK(ck::BoundsForLevel(gms::kLevelSmMapV5).maxX > 264.f);
  // the shop from the level's items; the table back with an empty list
  const std::uint32_t blink = gms::AssetIdOf("Spell_Blink.Item"), made = gms::AssetIdOf("Made_Up.Item");
  CHECK(ck::IsShopItem(blink) && !ck::IsShopItem(made));
  ck::SetLevelShopItems({made, 0u});
  CHECK(ck::IsShopItem(made) && !ck::IsShopItem(blink) && !ck::IsShopItem(0));
  ck::SetLevelShopItems({});
  CHECK(ck::IsShopItem(blink) && !ck::IsShopItem(made));
  CHECK_EQ(ck::ShopItems().size(), static_cast<std::size_t>(104));
  CHECK(ck::IsDebugItemName("Consumable_Debug_GrantGold.Item") && !ck::IsDebugItemName("Spell_Blink.Item"));
  // a bad size is ignored (the table or +-300 again)
  ck::SetLevelSize(grove, 0.f, 450.f);
  CHECK(ck::BoundsForLevel(grove).maxX == 300.f);
}
