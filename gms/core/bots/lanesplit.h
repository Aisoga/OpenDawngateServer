// gms/core/bots (BOTLANESPLIT): the bots of a team divide each role evenly over the two lanes. Header-only, engine-free.
//
// The user's report (2026-10-23): "sometimes 2 tacticians might end up on the same lane. They should always try to
// 'divide themselves' evenly. Same with gladiators." Two causes [code, log]:
//   1. The composition placed seats by position, not by role: a flex seat beyond the second (MAPHOST's level without
//      camps: a third flex seat at the bottom; more bot seats than the setup has) and a fallback seat (a class of
//      another role when a role's pool was used up) could put two Tacticians, or two Gladiators, on one lane.
//   2. In the match, ability_Bot_AssignToThreatenedLane (gambit 2 or 3 of the BackToAction phases of the High
//      Huntress, Guardian, Artist, TidalQueen and Warden AIs, so also the Desecrator's and Mikella's profiles)
//      reassigns a bot in its own base to the lane whose front is nearest its Guardian, for good. Our fronts stand at
//      the team's own fresh wave right after it spawns, so the first wave already moved a laner over: in a practice
//      match at 105 s, the top Tactician (the Desecrator) to the bottom beside the TidalQueen.
//
// The rule, one place for every path (composition.h ComposeTeam/ComposeTeamFree via teamfill.h, the BotPlan fill,
// draft.h's draw, a role changed in shaper select, the match's lane changes):
//   * the groups: each of Gladiator, Tactician, Predator; and the supports (Tactician + Predator together, the setup's
//     "one support per lane"). The Hunter is left where he is (he leaves for the camps); an unknown role counts for
//     nothing.
//   * at the composition (SplitLanes): the team keeps its shape (Tuning.BotPlan's topLaners, 2, on top, the rest at the
//     bottom; without humans the bots' lane counts as composed; the Hunter stays at the bottom), and the lanes are
//     chosen so that no group has more than one member more on one lane than on the other; where that can't be had,
//     the smallest excess (a role first, then the supports), then the fewest bots moved off the lane the composition
//     gave them (which Gladiator takes the top lane stays the draw's).
//   * the humans [assumed]: a human's lane isn't known when the bots are composed. A human of known role is counted on
//     whichever lane suits the split best within the team's shape, i.e. as if the human took the lane its role is short
//     of, and the bots take the other places; a human of unknown role, or a Hunter, fills what is left. In the match
//     only the bots count (MoveKeepsSplit).
//   * in the match (MoveKeepsSplit, [host] bot_lane_split): a bot's single lane change is taken only when its groups
//     stay split (at most one member more on a lane) or get closer to it. The team's push call and the siege rally,
//     which move the whole team to one lane on purpose, don't ask.
#pragma once

#include <algorithm>
#include <cstdlib>
#include <vector>

#include "core/bots/composition.h"
#include "core/bots/lanes.h"  // kLaneTop, kLaneBottom

namespace gms::bots {

// One member of a team for the split: its role (mRole, -1 unknown) and lane (kLaneTop, kLaneBottom; anything else is
// not on a lane).
struct LaneMember {
  int role = kRoleUnknown;
  int lane = kLaneNone;
};

// The groups a role belongs to (bit 0 Gladiator, 1 Tactician, 2 Predator, 3 the supports).
inline unsigned SplitGroups(int role) {
  switch (role) {
    case kRoleGladiator: return 1u;
    case kRoleTactician: return 2u | 8u;
    case kRolePredator: return 4u | 8u;
  }
  return 0u;  // the Hunter, unknown
}
constexpr int kSplitGroups = 4;
constexpr unsigned kSplitRoleGroups = 7u;  // the three roles; bit 3 is the supports

// The role a planned seat plays: a fallback seat (another role's class) plays its class's own role, unless the seat
// sets mRole after it.
inline int SeatRole(const ComposedSeat& s) {
  if (s.roleOverride || !s.fallback) return s.role;
  const int r = ClassRole(s.botClass);
  return r >= 0 ? r : s.role;
}

// The excess of a team's lanes: per group, how many members it has on one lane beyond one more than on the other.
// `roleExcess` gets the three roles' sum, `supportExcess` the supports'.
inline void SplitExcess(const std::vector<LaneMember>& members, int* roleExcess, int* supportExcess) {
  int top[kSplitGroups] = {}, bottom[kSplitGroups] = {};
  for (const LaneMember& m : members) {
    if (m.lane != kLaneTop && m.lane != kLaneBottom) continue;
    const unsigned g = SplitGroups(m.role);
    for (int i = 0; i < kSplitGroups; ++i)
      if (g & (1u << i)) (m.lane == kLaneTop ? top : bottom)[i]++;
  }
  int roles = 0, supports = 0;
  for (int i = 0; i < kSplitGroups; ++i) {
    const int d = std::abs(top[i] - bottom[i]);
    const int over = d > 1 ? d - 1 : 0;
    if ((1u << i) & kSplitRoleGroups) roles += over;
    else supports += over;
  }
  if (roleExcess) *roleExcess = roles;
  if (supportExcess) *supportExcess = supports;
}

// True when a team's members are split: no group has more than one member more on one lane than on the other.
inline bool LanesSplit(const std::vector<LaneMember>& members) {
  int r = 0, s = 0;
  SplitExcess(members, &r, &s);
  return r == 0 && s == 0;
}

// The lanes for a team's bots [rule: header comment]: `bots` (role, the lane the composition gave), `humanRoles` (the
// team's humans: mRole or intent, -1 unknown), `topShape` (the team's top laners, Tuning.BotPlan's topLaners). Returns
// one lane per bot, in order. Bots on no lane and Hunters keep theirs; with more than 12 movable bots or 8 humans of
// known role nothing moves.
// The top lane holds the team's top laners: without humans the bots' own top count (the composition's), with humans
// min(topShape, team size) or the bots' top count if that is more; the bots and the humans of known role are placed
// within it, and the other humans (unknown, Hunters) fill whatever is left on either lane.
inline std::vector<int> SplitLanes(const std::vector<LaneMember>& bots, const std::vector<int>& humanRoles,
                                   int topShape = 2) {
  std::vector<int> out;
  for (const LaneMember& b : bots) out.push_back(b.lane);
  std::vector<size_t> movable;
  int fixedTop = 0, botTop = 0, onLane = 0;
  for (size_t i = 0; i < bots.size(); ++i) {
    const bool laned = bots[i].lane == kLaneTop || bots[i].lane == kLaneBottom;
    if (!laned) continue;
    ++onLane;
    botTop += bots[i].lane == kLaneTop ? 1 : 0;
    if (bots[i].role == kRoleHunter) fixedTop += bots[i].lane == kLaneTop ? 1 : 0;
    else movable.push_back(i);
  }
  std::vector<int> known;
  for (int r : humanRoles)
    if (SplitGroups(r) != 0) known.push_back(r);
  if (movable.empty() || movable.size() > 12 || known.size() > 8) return out;
  const int teamSize = onLane + static_cast<int>(humanRoles.size());
  const int fillers = static_cast<int>(humanRoles.size() - known.size());
  const int teamTop = humanRoles.empty() ? botTop : std::max(std::min(topShape, teamSize), botTop);
  const size_t n = movable.size(), h = known.size();
  long best = -1;
  unsigned bestBots = 0;
  std::vector<LaneMember> team(bots.begin(), bots.end());
  for (int r : known) team.push_back(LaneMember{r, kLaneNone});
  for (unsigned mask = 0; mask < (1u << n); ++mask) {
    int top = fixedTop, moved = 0;
    for (size_t k = 0; k < n; ++k) {
      const bool isTop = (mask >> k) & 1u;
      top += isTop ? 1 : 0;
      const int lane = isTop ? kLaneTop : kLaneBottom;
      moved += lane != bots[movable[k]].lane ? 1 : 0;
      team[movable[k]].lane = lane;
    }
    if (top > teamTop || top + static_cast<int>(h) + fillers < teamTop) continue;
    for (unsigned hm = 0; hm < (1u << h); ++hm) {
      int htop = 0;
      for (size_t k = 0; k < h; ++k) htop += (hm >> k) & 1u ? 1 : 0;
      // the top lane's places: the bots', these humans', and the rest for the other humans
      if (top + htop > teamTop || top + htop + fillers < teamTop) continue;
      for (size_t k = 0; k < h; ++k) team[bots.size() + k].lane = (hm >> k) & 1u ? kLaneTop : kLaneBottom;
      int roles = 0, supports = 0;
      SplitExcess(team, &roles, &supports);
      const long cost = roles * 10000L + supports * 100L + moved;
      if (best < 0 || cost < best) {
        best = cost;
        bestBots = mask;
      }
    }
  }
  if (best < 0) return out;
  for (size_t k = 0; k < n; ++k) out[movable[k]] = (bestBots >> k) & 1u ? kLaneTop : kLaneBottom;
  return out;
}

// SplitLanes over a team's planned seats (seats without a class are no bots and are left out).
inline void SplitSeatLanes(std::vector<ComposedSeat>& seats, const std::vector<int>& humanRoles, int topShape = 2) {
  std::vector<LaneMember> bots;
  std::vector<size_t> at;
  for (size_t i = 0; i < seats.size(); ++i) {
    if (seats[i].botClass.empty()) continue;
    bots.push_back(LaneMember{SeatRole(seats[i]), seats[i].lane});
    at.push_back(i);
  }
  const std::vector<int> lanes = SplitLanes(bots, humanRoles, topShape);
  for (size_t k = 0; k < at.size(); ++k) seats[at[k]].lane = lanes[k];
}

// In the match: may a bot of `role` change from lane `from` to lane `to`, given its team's other bots (`others`)? Yes
// for the Hunter, an unknown role, no change, or a target that is no lane; else only when each of its groups stays
// split after the move (at most one member more on a lane) or gets closer to it.
inline bool MoveKeepsSplit(int role, int from, int to, const std::vector<LaneMember>& others) {
  const unsigned groups = SplitGroups(role);
  if (groups == 0 || from == to || (to != kLaneTop && to != kLaneBottom)) return true;
  for (int i = 0; i < kSplitGroups; ++i) {
    if (!(groups & (1u << i))) continue;
    int top = 0, bottom = 0;
    for (const LaneMember& m : others) {
      if (!(SplitGroups(m.role) & (1u << i))) continue;
      top += m.lane == kLaneTop ? 1 : 0;
      bottom += m.lane == kLaneBottom ? 1 : 0;
    }
    const int before = std::abs(top + (from == kLaneTop) - bottom - (from == kLaneBottom));
    const int after = std::abs(top + (to == kLaneTop) - bottom - (to == kLaneBottom));
    if (after > 1 && after >= before) return false;
  }
  return true;
}

}  // namespace gms::bots
