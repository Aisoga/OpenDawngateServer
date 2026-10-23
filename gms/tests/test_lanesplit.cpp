// gms_tests: BOTLANESPLIT, the bots of a team divide each role evenly over the lanes (core/bots/lanesplit.h) on every
// path that gives a bot its lane: the fixed setup and the free draw (teamfill.h PlanBotPlayersComposed), the BotPlan fill
// (PlanBotPlayers), BOTDRAFT's draw (draft.h DraftMatch), MAPHOST's level without camps, and a lane change in the match.
#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <map>
#include <set>
#include <string>
#include <vector>

#include "core/bots/draft.h"
#include "core/bots/lanesplit.h"
#include "core/bots/teamfill.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

std::set<std::string> Everything() {
  const std::vector<std::string> c = CatalogCandidates();
  return std::set<std::string>(c.begin(), c.end());
}

// The shipped classes only (no profile in the copy): the Predator is PC_Warden with its role set after it.
std::set<std::string> ShippedOnly() {
  return {"PC_HighHuntress", "PC_Creature", "PC_Guardian", "PC_Artist", "PC_TidalQueen", "PC_Sculptor", "PC_Warden",
          "PC_Artificer"};
}

// Few classes: one Gladiator class, so a team's second Gladiator seat is a fallback (another role's class).
std::set<std::string> Scarce() {
  return {"PC_HighHuntress", "PC_Guardian", "PC_Artist", "PC_TidalQueen", "PC_Sculptor", "PC_Warden", "PC_Artificer"};
}

LaneMember M(int role, int lane) { return LaneMember{role, lane}; }

// Every multiset of 0..4 humans over the roles unknown, Gladiator, Tactician, Hunter, Predator.
std::vector<std::vector<int>> HumanSets() {
  std::vector<std::vector<int>> out = {{}};
  const int roles[] = {kRoleUnknown, kRoleGladiator, kRoleTactician, kRoleHunter, kRolePredator};
  for (int n = 1; n <= 4; ++n) {
    std::vector<int> idx(static_cast<size_t>(n), 0);
    for (;;) {
      std::vector<int> set;
      for (int i : idx) set.push_back(roles[i]);
      out.push_back(set);
      int k = n - 1;
      while (k >= 0 && idx[static_cast<size_t>(k)] == 4) --k;
      if (k < 0) break;
      ++idx[static_cast<size_t>(k)];
      for (int j = k + 1; j < n; ++j) idx[static_cast<size_t>(j)] = idx[static_cast<size_t>(k)];
    }
  }
  return out;
}

int CountRole(const std::vector<int>& roles, int role) {
  return static_cast<int>(std::count(roles.begin(), roles.end(), role));
}

// The check over one team's bots (their roles and lanes): for the Gladiators and the Tacticians, no lane holds more of
// the role's bots than half the team's members of that role (bots and humans of known role) rounded up, and with no
// human of the role the bots differ by at most one. `what` names the case in a failure.
void CheckTeamSplit(const std::vector<LaneMember>& bots, const std::vector<int>& humanRoles, const std::string& what) {
  for (int role : {kRoleGladiator, kRoleTactician}) {
    int top = 0, bottom = 0;
    for (const LaneMember& b : bots) {
      if (b.role != role) continue;
      top += b.lane == kLaneTop ? 1 : 0;
      bottom += b.lane == kLaneBottom ? 1 : 0;
    }
    const int humans = CountRole(humanRoles, role);
    const int all = top + bottom + humans;
    const bool ok = std::max(top, bottom) <= (all + 1) / 2 && (humans > 0 || std::abs(top - bottom) <= 1);
    if (!ok) std::printf("  lane split: %s: %s bots %d top, %d bottom\n", what.c_str(), RoleName(role), top, bottom);
    CHECK(ok);
  }
}

std::vector<LaneMember> Members(const std::vector<ComposedSeat>& seats) {
  std::vector<LaneMember> m;
  for (const ComposedSeat& s : seats)
    if (!s.botClass.empty()) m.push_back(LaneMember{SeatRole(s), s.lane});
  return m;
}

std::vector<int> LaneCounts(const std::vector<LaneMember>& m) {
  int top = 0, bottom = 0, hunterBottom = 0;
  for (const LaneMember& b : m) {
    top += b.lane == kLaneTop ? 1 : 0;
    bottom += b.lane == kLaneBottom ? 1 : 0;
    hunterBottom += b.role == kRoleHunter && b.lane == kLaneBottom ? 1 : 0;
  }
  return {top, bottom, hunterBottom};
}

std::string Describe(const std::vector<int>& humans, bool jungle, size_t classes) {
  std::string d = std::to_string(classes) + " classes, " + (jungle ? "camps" : "no camps") + ", humans [";
  for (size_t i = 0; i < humans.size(); ++i) d += (i ? "," : "") + std::string(RoleName(humans[i]));
  return d + "]";
}

}  // namespace

// The rule itself: two of a role go one per lane, the lane counts stay, the fewest bots move, the Hunter stays.
GMS_TEST(LaneSplitRule) {
  // two Tacticians on top (a flex seat's draw): one goes bottom, the bottom flex seat's Predator comes up
  std::vector<LaneMember> bots = {M(kRoleGladiator, kLaneTop), M(kRoleTactician, kLaneTop), M(kRoleGladiator, kLaneBottom),
                                  M(kRoleTactician, kLaneBottom), M(kRoleHunter, kLaneBottom)};
  CHECK(LanesSplit(bots));
  CHECK(SplitLanes(bots, {}) == (std::vector<int>{kLaneTop, kLaneTop, kLaneBottom, kLaneBottom, kLaneBottom}));
  // MAPHOST's third flex seat: Predator on top, both Tacticians at the bottom -> a Tactician and the Predator swap
  bots = {M(kRoleGladiator, kLaneTop), M(kRolePredator, kLaneTop), M(kRoleGladiator, kLaneBottom),
          M(kRoleTactician, kLaneBottom), M(kRoleTactician, kLaneBottom)};
  CHECK(!LanesSplit(bots));
  std::vector<int> lanes = SplitLanes(bots, {});
  CHECK(lanes == (std::vector<int>{kLaneTop, kLaneBottom, kLaneBottom, kLaneTop, kLaneBottom}));
  // two Gladiators on one lane: one moves, the supports stay one a lane
  bots = {M(kRoleGladiator, kLaneTop), M(kRoleGladiator, kLaneTop), M(kRoleTactician, kLaneBottom),
          M(kRolePredator, kLaneBottom), M(kRoleHunter, kLaneBottom)};
  lanes = SplitLanes(bots, {});
  for (size_t i = 0; i < bots.size(); ++i) bots[i].lane = lanes[i];
  CHECK(LanesSplit(bots));
  CHECK_EQ(lanes[0], static_cast<int>(kLaneTop));  // the first Gladiator (the draw's top one) stays
  CHECK_EQ(lanes[4], static_cast<int>(kLaneBottom));  // the Hunter stays
  const std::vector<int> counts = LaneCounts(bots);
  CHECK_EQ(counts[0], 2);
  // three Tacticians on a 2/3 team: two at the bottom is the best there is; no Gladiator moves for it
  bots = {M(kRoleGladiator, kLaneTop), M(kRoleTactician, kLaneTop), M(kRoleGladiator, kLaneBottom),
          M(kRoleTactician, kLaneBottom), M(kRoleTactician, kLaneBottom)};
  CHECK(SplitLanes(bots, {}) == (std::vector<int>{kLaneTop, kLaneTop, kLaneBottom, kLaneBottom, kLaneBottom}));
  // a bot on no lane and unknown roles are left alone
  bots = {M(kRoleTactician, kLaneNone), M(kRoleUnknown, kLaneTop), M(kRoleTactician, kLaneTop)};
  CHECK(SplitLanes(bots, {}) == (std::vector<int>{kLaneNone, kLaneTop, kLaneTop}));
}

// A human of known role is counted on a lane the bots leave open [assumed]; unknown ones count for nothing.
GMS_TEST(LaneSplitHumansCounted) {
  // three bots of a 4-player team (top shape 2: a place on top is open). A human of unknown role or a Tactician takes it
  // and nothing moves; a human Gladiator is counted at the bottom, so the bot Tactician comes up beside the bot Gladiator
  const std::vector<LaneMember> bots = {M(kRoleGladiator, kLaneTop), M(kRoleTactician, kLaneBottom),
                                        M(kRoleHunter, kLaneBottom)};
  CHECK(SplitLanes(bots, {}) == (std::vector<int>{kLaneTop, kLaneBottom, kLaneBottom}));
  CHECK(SplitLanes(bots, {kRoleUnknown}) == (std::vector<int>{kLaneTop, kLaneBottom, kLaneBottom}));
  CHECK(SplitLanes(bots, {kRoleGladiator}) == (std::vector<int>{kLaneTop, kLaneTop, kLaneBottom}));
  // a human Predator beside two bot Tacticians at the bottom (the free draw without camps gave the human the top flex
  // seat): the human counts at the bottom, a Tactician goes up
  const std::vector<LaneMember> two = {M(kRoleGladiator, kLaneTop), M(kRoleGladiator, kLaneBottom),
                                       M(kRoleTactician, kLaneBottom), M(kRoleTactician, kLaneBottom)};
  CHECK(SplitLanes(two, {kRolePredator}) == (std::vector<int>{kLaneTop, kLaneBottom, kLaneTop, kLaneBottom}));
  // without humans the bots' own lane counts stay (a 3-bot team with one on top)
  CHECK(SplitLanes(bots, {}) == (std::vector<int>{kLaneTop, kLaneBottom, kLaneBottom}));
  CHECK(SplitLanes(bots, {kRoleTactician}) == (std::vector<int>{kLaneTop, kLaneBottom, kLaneBottom}));
  // the open place is the bottom one: a human Gladiator there changes nothing
  const std::vector<LaneMember> full = {M(kRoleGladiator, kLaneTop), M(kRoleTactician, kLaneTop),
                                        M(kRoleTactician, kLaneBottom), M(kRoleHunter, kLaneBottom)};
  CHECK(SplitLanes(full, {kRoleGladiator}) == (std::vector<int>{kLaneTop, kLaneTop, kLaneBottom, kLaneBottom}));
}

// The roles a fallback seat plays: its class's own, unless the seat sets mRole after it.
GMS_TEST(LaneSplitSeatRole) {
  ComposedSeat s;
  s.role = kRoleGladiator;
  s.botClass = "PC_Guardian";
  CHECK_EQ(SeatRole(s), static_cast<int>(kRoleGladiator));  // a seat's own class
  s.fallback = true;
  CHECK_EQ(SeatRole(s), static_cast<int>(kRoleTactician));
  s.roleOverride = true;
  CHECK_EQ(SeatRole(s), static_cast<int>(kRoleGladiator));
}

// The composed bots (bot_draft 0): the fixed setup (bot_shuffle 0) and the free draw (every seed tried), with 0-4
// humans of every role on team 1, with and without camps, with every class, the shipped ones, and few.
GMS_TEST(LaneSplitComposedEveryDraw) {
  const std::vector<std::vector<int>> sets = HumanSets();
  const std::vector<int> drop = ParseDropOrder("");
  const std::vector<std::string> anyClass = {"PC_HighHuntress", "PC_TidalQueen", "PC_Guardian", "PC_Artist", "PC_Warden"};
  int cases = 0, moved = 0, unsplit = 0;
  for (const std::set<std::string>& avail : {Everything(), ShippedOnly(), Scarce()})
    for (bool jungle : {true, false})
      for (const std::vector<int>& roles : sets)
        for (unsigned seed : {0u, 1u, 2u, 3u, 5u, 8u, 13u, 21u, 34u, 55u, 89u, 144u, 233u, 377u, 610u, 987u}) {
          std::vector<HumanPlayer> humans;
          for (size_t i = 0; i < roles.size(); ++i) {
            HumanPlayer h;
            h.index = static_cast<int>(i);
            h.team = 1;
            h.role = roles[i];
            humans.push_back(h);
          }
          const std::vector<BotPlayer> on = PlanBotPlayersComposed(humans, 5, {1, 2}, avail, anyClass, drop, 0, seed,
                                                                   ParseHunterList("default"), {}, {}, jungle, true);
          const std::vector<BotPlayer> off = PlanBotPlayersComposed(humans, 5, {1, 2}, avail, anyClass, drop, 0, seed,
                                                                    ParseHunterList("default"), {}, {}, jungle, false);
          CHECK_EQ(on.size(), off.size());
          for (int team = 1; team <= 2; ++team) {
            std::vector<LaneMember> a, b;
            for (size_t i = 0; i < on.size(); ++i) {
              if (on[i].team != team) continue;
              // the role the bot plays: the seat's when it is set after the class, else its class's own
              const int ra = on[i].roleOverride || ClassRole(on[i].botClass) < 0 ? on[i].role : ClassRole(on[i].botClass);
              a.push_back(M(ra, on[i].lane));
              b.push_back(M(ra, off[i].lane));
              CHECK(on[i].botClass == off[i].botClass);  // only the lanes change
              moved += on[i].lane != off[i].lane ? 1 : 0;
            }
            CheckTeamSplit(a, team == 1 ? roles : std::vector<int>{},
                           "composed seed " + std::to_string(seed) + " team " + std::to_string(team) + ", " + Describe(roles, jungle, avail.size()));
            // the Hunter keeps the bottom lane; without humans the lane counts stay, with humans the top holds at most
            // the team's two top laners (or the composition's own count when that is more)
            const std::vector<int> ca = LaneCounts(a), cb = LaneCounts(b);
            CHECK_EQ(ca[2], cb[2]);
            if (team == 2 || roles.empty()) CHECK(ca == cb);
            else CHECK(ca[0] <= std::max(2, cb[0]));
            int before = 0;
            SplitExcess(b, &before, nullptr);
            unsplit += before > 0 ? 1 : 0;
            ++cases;
          }
        }
  CHECK(cases > 10000);
  CHECK(moved > 0);
  CHECK(unsplit > 0);  // some draws did put two of a role on one lane before
}

// The BotPlan fill (bot_composition 0): four Tacticians and one Gladiator split two and two.
GMS_TEST(LaneSplitBotPlanFill) {
  Slot any;
  any.shaperTypes = {ShaperType{"any", "any", "any"}};
  const std::vector<Slot> slots(5, any);
  std::vector<BotCandidate> plan;
  for (const char* c : {"PC_HighHuntress", "PC_TidalQueen", "PC_Guardian", "PC_Artist", "PC_Warden"})
    plan.push_back(BotCandidate{c, ShaperType{"any", "any", "any"}});
  const std::vector<BotPlayer> before = PlanBotPlayers(slots, plan, {}, 5, {1}, 2, 3, 0, false);
  const std::vector<BotPlayer> after = PlanBotPlayers(slots, plan, {}, 5, {1}, 2, 3);
  CHECK_EQ(after.size(), static_cast<size_t>(5));
  std::vector<LaneMember> a, b;
  for (size_t i = 0; i < after.size(); ++i) {
    a.push_back(M(ClassRole(after[i].botClass), after[i].lane));
    b.push_back(M(ClassRole(before[i].botClass), before[i].lane));
  }
  int tTop = 0;
  for (const LaneMember& m : a) tTop += m.role == kRoleTactician && m.lane == kLaneTop ? 1 : 0;
  CHECK_EQ(tTop, 2);
  CHECK(LaneCounts(a) == LaneCounts(b));
  int bTop = 0;
  for (const LaneMember& m : b) bTop += m.role == kRoleTactician && m.lane == kLaneTop ? 1 : 0;
  CHECK_EQ(bTop, 1);  // the slots' lanes put three Tacticians at the bottom
}

// BOTDRAFT: the bots' draw after the humans, every seed tried, with 0-4 humans of every intent on team 1, with and
// without camps, with every class, the shipped ones and few (fallback seats).
GMS_TEST(LaneSplitDraftEveryDraw) {
  const std::vector<std::vector<int>> sets = HumanSets();
  int cases = 0, moved = 0, unsplit = 0;
  for (const std::set<std::string>& avail : {Everything(), ShippedOnly(), Scarce()})
    for (bool jungle : {true, false})
      for (const std::vector<int>& roles : sets)
        for (unsigned seed = 1; seed <= 12; ++seed) {
          std::vector<DraftHuman> humans;
          for (size_t i = 0; i < roles.size(); ++i) {
            DraftHuman h;
            h.index = static_cast<int>(i);
            h.team = 1;
            h.role = roles[i];
            humans.push_back(h);
          }
          DraftOptions o;
          o.seed = seed;
          o.hunters = ParseHunterList("default");
          o.jungle = jungle;
          const std::vector<DraftTeam> teams = {DraftTeam{1, 5 - static_cast<int>(roles.size())}, DraftTeam{2, 5}};
          const std::map<int, std::vector<ComposedSeat>> on = DraftMatch(teams, humans, avail, o);
          o.laneSplit = false;
          const std::map<int, std::vector<ComposedSeat>> off = DraftMatch(teams, humans, avail, o);
          for (int team = 1; team <= 2; ++team) {
            const std::vector<ComposedSeat>& a = on.at(team);
            const std::vector<ComposedSeat>& b = off.at(team);
            CHECK_EQ(a.size(), b.size());
            for (size_t i = 0; i < a.size() && i < b.size(); ++i) {
              CHECK(a[i].botClass == b[i].botClass && a[i].role == b[i].role);
              moved += a[i].lane != b[i].lane ? 1 : 0;
            }
            CheckTeamSplit(Members(a), team == 1 ? roles : std::vector<int>{},
                           "draft seed " + std::to_string(seed) + " team " + std::to_string(team) + ", " + Describe(roles, jungle, avail.size()));
            const std::vector<int> ca = LaneCounts(Members(a)), cb = LaneCounts(Members(b));
            CHECK_EQ(ca[2], cb[2]);
            if (team == 2 || roles.empty()) CHECK(ca == cb);
            else CHECK(ca[0] <= std::max(2, cb[0]));
            int before = 0;
            SplitExcess(Members(b), &before, nullptr);
            unsplit += before > 0 ? 1 : 0;
            ++cases;
          }
        }
  CHECK(cases > 5000);
  CHECK(moved > 0);
  CHECK(unsplit > 0);
}

// In the match: the threatened-lane change of a practice match (the top Tactician to the bottom beside the other
// Tactician) is refused; a change that keeps or mends the split is taken.
GMS_TEST(LaneSplitMoveInTheMatch) {
  // team 1 of that match: Mikella and the Desecrator on top, the Creature, the TidalQueen and Freia at the bottom
  const std::vector<LaneMember> others = {M(kRoleGladiator, kLaneTop), M(kRoleGladiator, kLaneBottom),
                                          M(kRoleTactician, kLaneBottom), M(kRoleHunter, kLaneBottom)};
  CHECK(!MoveKeepsSplit(kRoleTactician, kLaneTop, kLaneBottom, others));  // the Desecrator stays on top
  CHECK(MoveKeepsSplit(kRoleTactician, kLaneTop, kLaneTop, others));      // no change
  CHECK(MoveKeepsSplit(kRoleHunter, kLaneBottom, kLaneTop, others));      // the Hunter goes where he likes
  CHECK(MoveKeepsSplit(kRoleTactician, kLaneTop, kLaneNone, others));     // not a lane: the caller's business
  // the top Gladiator to the bottom: two Gladiators there, refused
  const std::vector<LaneMember> withT = {M(kRoleTactician, kLaneTop), M(kRoleGladiator, kLaneBottom),
                                         M(kRoleTactician, kLaneBottom), M(kRoleHunter, kLaneBottom)};
  CHECK(!MoveKeepsSplit(kRoleGladiator, kLaneTop, kLaneBottom, withT));
  // a change that mends the split (both Tacticians on top after a push call): taken
  const std::vector<LaneMember> pushed = {M(kRoleGladiator, kLaneTop), M(kRoleGladiator, kLaneBottom),
                                          M(kRoleTactician, kLaneTop)};
  CHECK(MoveKeepsSplit(kRoleTactician, kLaneTop, kLaneBottom, pushed));
  // the supports: the Predator to the Tactician's lane is refused; with a third support it is taken
  const std::vector<LaneMember> supports = {M(kRoleTactician, kLaneTop), M(kRoleGladiator, kLaneTop),
                                            M(kRoleGladiator, kLaneBottom)};
  CHECK(!MoveKeepsSplit(kRolePredator, kLaneBottom, kLaneTop, supports));
  const std::vector<LaneMember> three = {M(kRoleTactician, kLaneTop), M(kRoleTactician, kLaneBottom),
                                         M(kRoleGladiator, kLaneTop), M(kRoleGladiator, kLaneBottom)};
  CHECK(MoveKeepsSplit(kRolePredator, kLaneBottom, kLaneTop, three));
}
