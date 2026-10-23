// MAPMINES: MM_Map_v3's two team gold mines in the wells core: which wells no player can reach by the level's island
// markers (core/wells/islands.h), the bots' getter without them, and a mine that starts owned and full (36 slots, 48
// on spawn, 1.0 gold per worker per minute) next to SM_Map_v5's rules, which stay as they were.
#include <cmath>
#include <cstdint>
#include <vector>

#include "core/wells/islands.h"
#include "core/wells/wells.h"
#include "tests/check.h"

using namespace gms::wells;

namespace {

// The levels' island markers (navmeshmarker_valid in <map>_design, match data): {x, y, players, npcs}.
std::vector<IslandMarker> MmIslands() {
  const float p[5][2] = {{-8.5f, 9.7f}, {-166.3f, -166.3f}, {165.0f, 165.4f}, {260.6f, 261.1f}, {-260.8f, -260.1f}};
  std::vector<IslandMarker> v;
  for (int i = 0; i < 5; ++i) {
    IslandMarker m;
    m.x = p[i][0];
    m.y = p[i][1];
    m.players = i == 0;
    m.npcs = true;
    v.push_back(m);
  }
  return v;
}
std::vector<IslandMarker> SmIslands() {
  const float p[3][2] = {{-164.7f, -198.0f}, {-181.8f, -181.7f}, {182.0f, 182.0f}};
  std::vector<IslandMarker> v;
  for (int i = 0; i < 3; ++i) {
    IslandMarker m;
    m.x = p[i][0];
    m.y = p[i][1];
    m.players = i == 0;
    m.npcs = true;
    v.push_back(m);
  }
  return v;
}

constexpr std::uint32_t kMineBlue = 0x2001, kMineRed = 0x2002;

// MM_Map_v3's mines with 36 spots each, 15.5-18.2 units out (here a ring of 17), as the level has them.
std::vector<WellDef> MmMines(bool reach) {
  const float pos[2][2] = {{-260.3f, -260.3f}, {260.5f, 260.4f}};
  std::vector<WellDef> d;
  for (int i = 0; i < 2; ++i) {
    WellDef w;
    w.object = i == 0 ? kMineBlue : kMineRed;
    w.team = i + 1;
    w.pos.x = pos[i][0];
    w.pos.y = pos[i][1];
    w.playersReach = reach;
    for (int s = 0; s < 36; ++s) {
      const float a = static_cast<float>(s) * 6.2831853f / 36.f;
      Vec3 p;
      p.x = w.pos.x + 17.f * std::cos(a);
      p.y = w.pos.y + 17.f * std::sin(a);
      w.spots.push_back(p);
    }
    d.push_back(w);
  }
  return d;
}

// Tuning_MM_V3's well part (the level's gameplay tuning) and the mine noun's 36 slots.
Tuning MmTuning() {
  Tuning t;
  t.followersOnSpawn = 48;
  t.minedPerMinutePerFollower = 1.0f;
  t.followerSlots = 36;
  return t;
}

int GoldOf(const std::vector<TeamGold>& g, int team, int source) {
  int s = 0;
  for (const TeamGold& x : g)
    if (x.team == team && x.source == source) s += x.amount;
  return s;
}

}  // namespace

GMS_TEST(MapMinesIslandsMm) {
  const std::vector<IslandMarker> m = MmIslands();
  // both mines: an NPC-only marker under a unit from the centre, no players' marker within the 12-unit ring
  CHECK(OutOfPlayersReach(-260.3f, -260.3f, m, 12.f));
  CHECK(OutOfPlayersReach(260.5f, 260.4f, m, 12.f));
  const IslandView v = ViewAt(-260.3f, -260.3f, m, 12.f);
  CHECK(v.npcOnly >= 0.f && v.npcOnly < 1.f);
  CHECK(v.players < 0.f);
  // the lane's centre (the players' marker) and the base are in reach; so is a point 13 units from a mine's marker
  CHECK(!OutOfPlayersReach(-8.5f, 9.7f, m, 12.f));
  CHECK(!OutOfPlayersReach(-206.f, -206.f, m, 12.f));
  CHECK(!OutOfPlayersReach(-260.8f + 13.f, -260.1f, m, 12.f));
  // no markers: everything in reach
  CHECK(!OutOfPlayersReach(-260.3f, -260.3f, {}, 12.f));
}

GMS_TEST(MapMinesIslandsSmUnchanged) {
  // SM_Map_v5's four spirit wells: the NPC-only markers are the Guardians' islands, 150+ units away
  const std::vector<IslandMarker> m = SmIslands();
  const float wells[4][2] = {{-182.3f, 27.9f}, {28.2f, -182.6f}, {182.5f, -27.9f}, {-27.6f, 182.7f}};
  for (int i = 0; i < 4; ++i) CHECK(!OutOfPlayersReach(wells[i][0], wells[i][1], m, 12.f));
  // the Guardians' own islands would count (no well stands there)
  CHECK(OutOfPlayersReach(182.f, 182.f, m, 12.f));
}

GMS_TEST(MapMinesPlayersMarkerWins) {
  // a players' marker on the same spot keeps the well in reach whatever NPC-only marker is nearer
  std::vector<IslandMarker> m = MmIslands();
  IslandMarker p;
  p.x = -255.f;
  p.y = -255.f;
  p.players = true;
  p.npcs = false;
  m.push_back(p);
  CHECK(!OutOfPlayersReach(-260.3f, -260.3f, m, 12.f));
  CHECK(OutOfPlayersReach(260.5f, 260.4f, m, 12.f));
}

GMS_TEST(MapMinesBotsGetterLeavesThemOut) {
  Wells w;
  w.Init(MmTuning(), MmMines(false), 0.0);
  CHECK(!w.PlayersReach(0));
  CHECK(!w.PlayersReach(1));
  CHECK(!w.PlayersReach(7));
  // the plain getter (the HUD's view, wells_bots_unreachable=1) lists them; the bots' getter doesn't
  CHECK_EQ(w.MinesForTeam(1).size(), static_cast<size_t>(1));
  CHECK_EQ(w.MinesForTeam(2).size(), static_cast<size_t>(1));
  CHECK(w.MinesForTeam(1, false, true).empty());
  CHECK(w.MinesForTeam(2, false, true).empty());
  CHECK(w.MinesForTeam(2, true, true).empty());
  // a reachable well stays in every view (SM_Map_v5)
  Wells r;
  r.Init(MmTuning(), MmMines(true), 0.0);
  CHECK_EQ(r.MinesForTeam(1, false, true).size(), static_cast<size_t>(1));
  const std::vector<std::uint32_t> red = r.MinesForTeam(2, false, true);
  CHECK(!red.empty() && red.front() == kMineRed);
  CHECK(r.PlayersReach(0));
}

GMS_TEST(MapMinesOwnedAndFullAtOnce) {
  Wells w;
  w.Init(MmTuning(), MmMines(false), 0.0);
  CHECK_EQ(w.Owner(0), 1);
  CHECK_EQ(w.Owner(1), 2);
  CHECK(w.IsLocked(0));
  TickResult r;
  w.Tick(0.0, {}, &r);
  // 48 on spawn into 36 slots: 36 at once a mine, each at its own spot (none on the centre), owned by its mine
  CHECK_EQ(r.spawns.size(), static_cast<size_t>(72));
  int onCentre = 0;
  for (const WorkerSpawn& s : r.spawns) {
    const Vec3& c = s.well == 0 ? MmMines(false)[0].pos : MmMines(false)[1].pos;
    if (std::hypot(s.pos.x - c.x, s.pos.y - c.y) < 1.f) ++onCentre;
    CHECK_EQ(s.owner, s.well == 0 ? kMineBlue : kMineRed);
    CHECK_EQ(s.team, s.well + 1);
  }
  CHECK_EQ(onCentre, 0);
  for (const WorkerSpawn& s : r.spawns) w.OnWorkerSpawned(s.well, s.slot, 0x3000u + static_cast<std::uint32_t>(s.slot));
  CHECK_EQ(w.Workers(0), 36);
  CHECK_EQ(w.FollowersForTitan(kMineRed), 36);
  // 36 workers x 1.0 = 36 gold per minute per player of each team, from the first second; no more workers come
  CHECK(std::fabs(w.GoldPerMinute(0) - 36.f) < 1e-4f);
  CHECK_EQ(static_cast<int>(w.TeamGpm(1)), 36);
  CHECK_EQ(static_cast<int>(w.TeamGpm(2)), 36);
  std::vector<TeamGold> gold;
  size_t more = 0;
  for (int k = 1; k <= 600; ++k) {  // a minute at ten ticks a second
    TickResult t;
    w.Tick(static_cast<double>(k) / 10.0, {}, &t);
    gold.insert(gold.end(), t.gold.begin(), t.gold.end());
    more += t.spawns.size();
  }
  CHECK_EQ(more, static_cast<size_t>(0));
  CHECK(std::abs(GoldOf(gold, 1, kGoldSourceWellPassive) - 36) <= 1);
  CHECK(std::abs(GoldOf(gold, 2, kGoldSourceWellPassive) - 36) <= 1);
}

GMS_TEST(MapMinesNobodyInTheRing) {
  // past the 900-s lock the rules are the data's: a shaper in the ring would capture (the rule is unchanged); on MM no
  // shaper gets there, the nearest walkable ground is 53 units out, so the mine keeps its owner
  Wells w;
  w.Init(MmTuning(), MmMines(false), 0.0);
  TickResult r;
  w.Tick(0.0, {}, &r);
  Shaper s;
  s.object = 0x100;
  s.player = 5;
  s.team = 2;
  s.alive = true;
  s.pos.x = -221.6f;  // the base's corner, the Player graph's nearest area to the blue mine
  s.pos.y = -221.6f;
  for (int k = 0; k <= 400; ++k) {
    TickResult t;
    w.Tick(900.0 + k * 0.1, {s}, &t);
    CHECK(t.captures.empty());
  }
  CHECK(!w.IsLocked(0));
  CHECK_EQ(w.Owner(0), 1);
  CHECK_EQ(w.Capturers(0), 0);
}
