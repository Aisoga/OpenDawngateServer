// gms_tests: MAPHOST, the host's rules for a second level (MM_Map_v3): no Hunter seat without jungle camps
// (core/bots/composition.h, draft.h, teamfill.h) and the lanes of a one-lane level (core/bots/lanes.h).
#include <set>
#include <string>
#include <vector>

#include "core/bots/draft.h"
#include "core/bots/lanes.h"
#include "core/bots/teamfill.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

std::set<std::string> Everything() {
  const std::vector<std::string> c = CatalogCandidates();
  return std::set<std::string>(c.begin(), c.end());
}

int Count(const std::vector<ComposedSeat>& seats, int role) {
  int n = 0;
  for (const ComposedSeat& s : seats) n += s.role == role ? 1 : 0;
  return n;
}

LanePath Lane(int id, const std::vector<std::vector<float>>& xy) {
  LanePath p;
  p.lane = id;
  for (const auto& k : xy) p.knots.push_back(MakeVec(k[0], k[1], 0.f));
  return p;
}

// The team-1 splines of both levels (their SplinePathNode markers): SM_Map_v5's blue_top / blue_midTop (10 knots)
// and MM_Map_v3's blue_top / blue_midTop (14 and 13 knots).
std::vector<LanePath> SmLanes() {
  return {Lane(kLaneTop, {{-210.5f, -184.9f}, {-171.7f, -146.0f}, {-153.1f, -114.8f}, {-131.6f, -44.2f}, {-106.4f, 28.7f},
                          {-69.6f, 68.0f}, {-24.2f, 110.1f}, {43.5f, 131.4f}, {114.9f, 152.7f}, {144.7f, 165.5f}}),
          Lane(kLaneBottom, {{-185.8f, -210.5f}, {-145.5f, -170.5f}, {-113.7f, -152.5f}, {-42.9f, -131.7f}, {26.4f, -107.5f},
                             {68.4f, -66.9f}, {108.3f, -27.8f}, {130.9f, 42.8f}, {153.0f, 113.8f}, {164.5f, 144.2f}})};
}
std::vector<LanePath> MmLanes() {
  return {Lane(kLaneTop, {{-184.7f, -156.9f}, {-128.2f, -138.2f}, {-91.7f, -105.2f}, {-68.2f, -74.8f}, {-49.0f, -39.3f},
                          {-12.0f, 12.0f}, {31.3f, 53.1f}, {54.7f, 83.8f}, {82.2f, 114.9f}, {122.0f, 146.6f},
                          {139.7f, 168.7f}, {149.0f, 196.5f}, {162.3f, 196.4f}, {163.7f, 178.4f}}),
          Lane(kLaneBottom, {{-158.6f, -185.2f}, {-99.1f, -135.1f}, {-79.4f, -115.1f}, {-54.1f, -84.3f}, {-30.8f, -54.0f},
                             {11.6f, -12.4f}, {49.7f, 39.0f}, {67.0f, 74.9f}, {91.1f, 103.5f}, {132.8f, 140.4f},
                             {191.1f, 148.4f}, {197.9f, 160.6f}, {179.5f, 163.1f}})};
}

}  // namespace

// The default setup with and without a jungle: the Hunter's seat becomes a bottom flex (Tactician) seat.
GMS_TEST(MapHostDefaultSetupWithoutJungle) {
  const std::vector<WantedSeat> with = DefaultSetup();
  const std::vector<WantedSeat> without = DefaultSetup(false);
  CHECK_EQ(with.size(), without.size());
  CHECK_EQ(with[4].role, static_cast<int>(kRoleHunter));
  CHECK_EQ(without[4].role, static_cast<int>(kRoleTactician));
  CHECK_EQ(without[4].lane, static_cast<int>(kLaneBottom));
  for (const WantedSeat& s : without) CHECK(s.role != kRoleHunter);
  // more seats than the setup: the repeat has no Hunter either
  for (const WantedSeat& s : WantedSeats(7, {}, ParseDropOrder(""), false)) CHECK(s.role != kRoleHunter);
}

// The free draw and the draft: no Hunter, five seats, the extra flex seat at the bottom; with a jungle as before.
GMS_TEST(MapHostNoHunterSeat) {
  const std::set<std::string> all = Everything();
  for (unsigned seed = 1; seed <= 40; ++seed) {
    const std::vector<ComposedSeat> free = ComposeTeamFree(5, all, seed, {}, "", "", false);
    CHECK_EQ(free.size(), size_t{5});
    CHECK_EQ(Count(free, kRoleHunter), 0);
    CHECK_EQ(Count(free, kRoleGladiator), 2);
    CHECK_EQ(ComposeTeamFree(5, all, seed).size(), size_t{5});
    CHECK_EQ(Count(ComposeTeamFree(5, all, seed), kRoleHunter), 1);
    DraftOptions o;
    o.seed = seed;
    o.hunters = ParseHunterList("default");
    o.jungle = false;
    for (const auto& kv : DraftMatch({DraftTeam{1, 4}, DraftTeam{2, 5}}, {}, all, o)) {
      CHECK_EQ(Count(kv.second, kRoleHunter), 0);
      CHECK_EQ(kv.second.size(), static_cast<size_t>(kv.first == 1 ? 4 : 5));
      std::set<std::string> bases;
      for (const ComposedSeat& s : kv.second) {
        CHECK(bases.insert(BaseClass(s.botClass)).second);
        CHECK(ClassRole(s.botClass) != kRoleHunter);
      }
    }
    o.jungle = true;
    int hunters = 0;
    for (const auto& kv : DraftMatch({DraftTeam{1, 5}, DraftTeam{2, 5}}, {}, all, o)) hunters += Count(kv.second, kRoleHunter);
    CHECK_EQ(hunters, 2);
  }
  // the team fill passes it through (the fixed setup and the free draw)
  std::vector<HumanPlayer> humans(1);
  humans[0].index = 0;
  humans[0].team = 1;
  for (unsigned seed : {0u, 7u}) {
    const std::vector<BotPlayer> plan = PlanBotPlayersComposed(humans, 5, {1, 2}, all, {}, ParseDropOrder(""), 0, seed,
                                                               {"PC_Artificer"}, {}, {}, false);
    CHECK_EQ(plan.size(), size_t{9});
    for (const BotPlayer& b : plan) CHECK(b.role != kRoleHunter);
  }
}

// SM_Map_v5's lanes are separate (193 units apart in the middle); MM_Map_v3's two splines are one path (at most 45).
GMS_TEST(MapHostSharedLanes) {
  const float sm = LaneSeparation(SmLanes()), mm = LaneSeparation(MmLanes());
  CHECK(sm > 150.f);
  CHECK(mm > 30.f && mm < 60.f);
  CHECK(!LanesShareOnePath(SmLanes()));
  CHECK(LanesShareOnePath(MmLanes()));
  CHECK(!LanesShareOnePath({MmLanes()[0]}));  // one lane: nothing to share
  CHECK_EQ(LaneSeparation({}), 0.f);
  // the fronts: a minion on the Bottom spline counts for the Top lane's front too on the shared path
  LaneFronts fronts;
  fronts.SetLanes(MmLanes());
  CHECK(fronts.Shared());
  Unit m;
  m.team = 1;
  m.pos = MakeVec(11.6f, -12.4f, 0.f);  // Bottom knot 5, farther than the lane width (20) off the Top spline
  float off = 0.f;
  MmLanes()[0].Project(m.pos, &off);
  CHECK(off > FrontConfig().laneWidth);
  fronts.Update({m});
  Front top, bottom;
  CHECK(fronts.Get(kLaneTop, 1, &top) && fronts.Get(kLaneBottom, 1, &bottom));
  CHECK_EQ(top.own, 1);
  CHECK_EQ(bottom.own, 1);
  LaneFronts sep;
  sep.SetLanes(SmLanes());
  CHECK(!sep.Shared());
  Unit n;
  n.team = 1;
  n.pos = MakeVec(-42.9f, -131.7f, 0.f);  // on SM's Bottom lane (knot 3)
  sep.Update({n});
  CHECK(sep.Get(kLaneTop, 1, &top) && sep.Get(kLaneBottom, 1, &bottom));
  CHECK_EQ(top.own, 0);
  CHECK_EQ(bottom.own, 1);
}
