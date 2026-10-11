// gms/core/bots (D6b-shims): team fill and the first lanes from Tuning.BotPlan, without the engine. Header-only.
//
// Tuning.BotPlan (BattlegroundsGameplayTuning.defaultBotPlan; AGENT_AI.md §11.2) [data]:
//   bot[]      PC_HighHuntress, PC_TidalQueen, PC_Guardian, PC_Artist, PC_Warden (5 BotClass assets);
//   slot[]     5 slots, each a list of {archetype, attackStyle, damageType} ("any" matches every value):
//              1 carry/ranged/any; 2 mage/any/any or assassin/any/magical; 3 tank/any/any or support/melee/any;
//              4 support/ranged/any; 5 bruiser/any/any or assassin/any/physical;
//   topLaners 2, botLaners 3.
// The shapers' own values are PlayerClass.CharacterArchetype, ShaperStyle_AttackStyle, ShaperStyle_DamageStyle
// (HighHuntress carry/ranged/physical, TidalQueen mage/ranged/magical, Guardian tank/melee/physical, Artist
// support/ranged/magical, Warden bruiser/melee/physical: the plan's five bots fill the five slots in order).
//
// Nothing of the original slot matching survives (R10: the director's reset sizes two byte vectors by the slot count
// and copies the plan's bot list per team, and nothing reads them). The rules here are [assumed] (AGENT_AI.md §11.4):
//   1. the humans of a team take slots first: each the first free slot its shaper matches, else the first free slot;
//   2. every other seat (up to the team size) takes the next free slot, in slot order, and the first bot class of the
//      plan that matches the slot and the team doesn't use yet, else the first unused one;
//   3. the first lanes go by slot: slots 0 .. topLaners-1 Top, the next botLaners Bottom, any further slot Bottom.
// D6a adds the step from seats to bot players (PlanBotPlayers: gameplay indexes and online ids) and the enum values as
// the game stores them (ShaperTypeOf, SlotPatternOf).
#pragma once

#include <cstdint>
#include <set>
#include <string>
#include <vector>

#include "core/bots/composition.h"
#include "core/bots/lanes.h"  // LaneId

namespace gms::bots {

// {archetype, attackStyle, damageType}: a shaper's values, or a slot's pattern where "any" (or empty) matches all.
struct ShaperType {
  std::string archetype, attackStyle, damageType;
};

inline bool FieldMatches(const std::string& pattern, const std::string& value) {
  return pattern.empty() || pattern == "any" || pattern == value;
}
inline bool TypeMatches(const ShaperType& pattern, const ShaperType& shaper) {
  return FieldMatches(pattern.archetype, shaper.archetype) && FieldMatches(pattern.attackStyle, shaper.attackStyle) &&
         FieldMatches(pattern.damageType, shaper.damageType);
}

// One slot of BotPlan.slot[]: its shaperType[] list (any entry may match).
struct Slot {
  std::vector<ShaperType> shaperTypes;
  bool Accepts(const ShaperType& shaper) const {
    for (const ShaperType& p : shaperTypes)
      if (TypeMatches(p, shaper)) return true;
    return false;
  }
};

// A bot class of BotPlan.bot[] with its shaper's values.
struct BotCandidate {
  std::string botClass;  // e.g. "PC_HighHuntress" (the BotClass asset name)
  ShaperType shaper;
};

struct Seat {
  bool human = false;
  int humanIndex = -1;   // index into FillTeam's `humans` for a human seat
  int slot = -1;         // the BotPlan slot it holds
  std::string botClass;  // empty for a human
  int lane = kLaneNone;  // the first lane (nBot.AssignLane at deploy for a bot; informational for a human)
};

inline int SlotLane(int slot, int topLaners, int botLaners) {
  if (slot < 0) return kLaneNone;
  if (slot < topLaners) return kLaneTop;
  (void)botLaners;  // every slot after the top laners goes Bottom, also beyond topLaners + botLaners [assumed]
  return kLaneBottom;
}

// Fills one team up to `teamSize` seats [assumed rules, header comment]. The humans' seats come first in the result (in
// their order), then the bots' seats in slot order. A human that finds no free slot gets slot -1 (more humans than
// slots); bots stop when the slots or the plan's bot classes run out.
inline std::vector<Seat> FillTeam(const std::vector<Slot>& slots, const std::vector<BotCandidate>& plan,
                                  const std::vector<ShaperType>& humans, int teamSize, int topLaners, int botLaners) {
  std::vector<Seat> seats;
  std::vector<bool> taken(slots.size(), false);
  for (size_t h = 0; h < humans.size(); ++h) {
    int pick = -1;
    for (size_t s = 0; s < slots.size() && pick < 0; ++s)
      if (!taken[s] && slots[s].Accepts(humans[h])) pick = static_cast<int>(s);
    for (size_t s = 0; s < slots.size() && pick < 0; ++s)
      if (!taken[s]) pick = static_cast<int>(s);
    if (pick >= 0) taken[static_cast<size_t>(pick)] = true;
    Seat seat;
    seat.human = true;
    seat.humanIndex = static_cast<int>(h);
    seat.slot = pick;
    seat.lane = SlotLane(pick, topLaners, botLaners);
    seats.push_back(seat);
  }
  std::vector<bool> used(plan.size(), false);
  for (size_t s = 0; s < slots.size() && static_cast<int>(seats.size()) < teamSize; ++s) {
    if (taken[s]) continue;
    int pick = -1;
    for (size_t b = 0; b < plan.size() && pick < 0; ++b)
      if (!used[b] && slots[s].Accepts(plan[b].shaper)) pick = static_cast<int>(b);
    for (size_t b = 0; b < plan.size() && pick < 0; ++b)
      if (!used[b]) pick = static_cast<int>(b);
    if (pick < 0) break;
    used[static_cast<size_t>(pick)] = true;
    taken[s] = true;
    Seat seat;
    seat.slot = static_cast<int>(s);
    seat.botClass = plan[static_cast<size_t>(pick)].botClass;
    seat.lane = SlotLane(seat.slot, topLaners, botLaners);
    seats.push_back(seat);
  }
  return seats;
}

// ---- D6a: from the game's enums to ShaperType, and from seats to bot players ------------------------------------------

// A shaper's values as PlayerClass stores them: CharacterArchetype (enum 0x0124982C: -1 any, 0 carry, 1 bruiser, 2 mage,
// 3 support, 4 tank, 5 assassin), ShaperStyle_AttackStyle (0x01249900: 0 melee, 1 ranged, 2 hybrid),
// ShaperStyle_DamageStyle (0x01249918: 0 physical, 1 magical, 2 hybrid) [data: docs/data/assetdata_types.txt].
inline std::string ArchetypeName(int v) {
  static const char* const kNames[] = {"carry", "bruiser", "mage", "support", "tank", "assassin"};
  return v >= 0 && v < 6 ? kNames[v] : "any";
}
inline ShaperType ShaperTypeOf(int archetype, int attackStyle, int damageStyle) {
  static const char* const kAttack[] = {"melee", "ranged", "hybrid"};
  static const char* const kDamage[] = {"physical", "magical", "hybrid"};
  ShaperType t;
  t.archetype = ArchetypeName(archetype);
  t.attackStyle = attackStyle >= 0 && attackStyle < 3 ? kAttack[attackStyle] : "?";
  t.damageType = damageStyle >= 0 && damageStyle < 3 ? kDamage[damageStyle] : "?";
  return t;
}
// A slot entry as BotPlan's cBotShaperType stores it: archetype (the same enum, -1 any), attackStyle (0x0123EAF4: 0
// melee, 1 ranged, 2 any), damageType (0x0123EB0C: 0 physical, 1 magical, 2 any).
inline ShaperType SlotPatternOf(int archetype, int attackStyle, int damageType) {
  static const char* const kAttack[] = {"melee", "ranged", "any"};
  static const char* const kDamage[] = {"physical", "magical", "any"};
  ShaperType t;
  t.archetype = ArchetypeName(archetype);
  t.attackStyle = attackStyle >= 0 && attackStyle < 3 ? kAttack[attackStyle] : "any";
  t.damageType = damageType >= 0 && damageType < 3 ? kDamage[damageType] : "any";
  return t;
}

// A human of the match (connected or on createGame's roster): gameplay index, team (1 or 2), shaper.
struct HumanPlayer {
  int index = -1;
  int team = 0;
  ShaperType shaper;
  int role = -1;  // BOTCOMP: its mRole when known (picked), else -1
};

// One bot player of the match.
struct BotPlayer {
  int index = -1;          // gameplay index 0-9
  int team = 0;            // nTeamID 1 or 2
  int slot = -1;           // its BotPlan slot
  std::string botClass;    // the BotClass asset's name (PC_<Shaper>)
  int lane = kLaneNone;    // the first lane (FillTeam)
  std::uint64_t onlineId = 0;
  int role = -1;           // BOTCOMP: the composition's role (mRole is set to it when roleOverride), -1 = the BotClass's
  bool roleOverride = false;
};

constexpr int kMaxPlayerIndexes = 10;  // gameplay indexes 0-9 (10-14 are spectators)

// The bots of a match [assumed rules, D6a]: every team in `teams` (1, 2) gets FillTeam's bot seats with that team's
// humans (in `humans` order) and `teamSize` seats; the bots take the lowest gameplay indexes that no human holds and
// `reserved` (bit i = index i: e.g. a roster index whose player hasn't connected yet) doesn't name, in team order (the
// first team's bots first, each in slot order); online id = index + 1, so 1-10, the range LabsPlayer_HandlePlayerOnlineId
// (0x00915DC0) treats as a bot (0 asserts). Bots stop when the indexes run out.
inline std::vector<BotPlayer> PlanBotPlayers(const std::vector<Slot>& slots, const std::vector<BotCandidate>& plan,
                                             const std::vector<HumanPlayer>& humans, int teamSize,
                                             const std::vector<int>& teams, int topLaners, int botLaners,
                                             unsigned reserved = 0) {
  unsigned taken = reserved;
  for (const HumanPlayer& h : humans)
    if (h.index >= 0 && h.index < kMaxPlayerIndexes) taken |= 1u << h.index;
  std::vector<BotPlayer> out;
  for (int team : teams) {
    std::vector<ShaperType> mine;
    for (const HumanPlayer& h : humans)
      if (h.team == team) mine.push_back(h.shaper);
    for (const Seat& s : FillTeam(slots, plan, mine, teamSize, topLaners, botLaners)) {
      if (s.human) continue;
      int index = -1;
      for (int i = 0; i < kMaxPlayerIndexes && index < 0; ++i)
        if (!(taken & (1u << i))) index = i;
      if (index < 0) return out;
      taken |= 1u << index;
      BotPlayer b;
      b.index = index;
      b.team = team;
      b.slot = s.slot;
      b.botClass = s.botClass;
      b.lane = s.lane;
      b.onlineId = static_cast<std::uint64_t>(index) + 1;
      out.push_back(b);
    }
  }
  return out;
}

// BOTCOMP: the same with composition.h's setup per team: each team's bot seats are teamSize minus its humans; a human
// of known role takes that role off the list (`WantedSeats`), the class per seat comes from `ComposeTeam`. A seat that
// finds no class (none available) is left out. `slot` is the seat's position in the team's wanted list.
inline std::vector<BotPlayer> PlanBotPlayersComposed(const std::vector<HumanPlayer>& humans, int teamSize,
                                                     const std::vector<int>& teams, const std::set<std::string>& available,
                                                     const std::vector<std::string>& anyClass,
                                                     const std::vector<int>& dropOrder, unsigned reserved = 0,
                                                     unsigned seed = 0) {
  unsigned taken = reserved;
  for (const HumanPlayer& h : humans)
    if (h.index >= 0 && h.index < kMaxPlayerIndexes) taken |= 1u << h.index;
  std::vector<BotPlayer> out;
  for (int team : teams) {
    std::vector<int> roles;
    int mineCount = 0;
    for (const HumanPlayer& h : humans)
      if (h.team == team) {
        roles.push_back(h.role);
        ++mineCount;
      }
    // seed != 0: the free draw (any class in any seat, ComposeTeamFree); 0: the fixed setup
    const std::vector<ComposedSeat> seats =
        seed ? ComposeTeamFree(teamSize - mineCount, available, seed + static_cast<unsigned>(team) * 7919u, roles)
             : ComposeTeam(WantedSeats(teamSize - mineCount, roles, dropOrder), available, anyClass, 0u);
    int slot = 0;
    for (const ComposedSeat& s : seats) {
      const int seatSlot = slot++;
      if (s.botClass.empty()) continue;
      int index = -1;
      for (int i = 0; i < kMaxPlayerIndexes && index < 0; ++i)
        if (!(taken & (1u << i))) index = i;
      if (index < 0) return out;
      taken |= 1u << index;
      BotPlayer b;
      b.index = index;
      b.team = team;
      b.slot = seatSlot;
      b.botClass = s.botClass;
      b.lane = s.lane;
      b.onlineId = static_cast<std::uint64_t>(index) + 1;
      b.role = s.role;
      b.roleOverride = s.roleOverride;
      out.push_back(b);
    }
  }
  return out;
}

// G3b: the humans the team fill counts. `connected` are the players with a connection; `waiting` are createGame's
// roster humans that haven't connected yet (index and roster team; e.g. a party's leader when its guest's HelloReq came
// first). Each waiting human counts for its team like a connected one, with an unknown shaper (empty fields: the first
// free slot); a waiting index a connected human holds already is left out. Before G3b only the waiting indexes were
// kept free (PlanBotPlayers' `reserved`), so the party's team got a bot for its missing member (d8b_run6d/run7d: a
// party of two in a 2 v 2 queue became a 3 v 2).
inline std::vector<HumanPlayer> CountedHumans(const std::vector<HumanPlayer>& connected,
                                              const std::vector<HumanPlayer>& waiting) {
  std::vector<HumanPlayer> out = connected;
  for (const HumanPlayer& w : waiting) {
    bool held = false;
    for (const HumanPlayer& h : out) held = held || h.index == w.index;
    if (held || w.index < 0 || w.index >= kMaxPlayerIndexes) continue;
    HumanPlayer p;
    p.index = w.index;
    p.team = w.team;  // its shaper stays unknown until it connects
    out.push_back(p);
  }
  return out;
}

// The teams that get bot seats: both, or (allies false) only the teams without a human, waiting ones included.
inline std::vector<int> BotTeams(const std::vector<HumanPlayer>& humans, bool allies) {
  std::vector<int> teams;
  for (int team = 1; team <= 2; ++team) {
    bool humanTeam = false;
    for (const HumanPlayer& h : humans) humanTeam = humanTeam || h.team == team;
    if (humanTeam && !allies) continue;
    teams.push_back(team);
  }
  return teams;
}

// The seats a team the host fills with bots (D6a's `[host] bots`, B10d's createGame fillTeams): `option` >= 0 decides
// (N seats, 0 = no bots); -1 (auto) takes `fillTeams` when dgserver asked for one (> 0: a matchmade game that started
// with open seats, the matchmaker's team size), else kBotsFillTeamsSeats when the game's descriptors have BotsFillTeams
// (0x400, the practice game's 0x50C) [assumed team size: SM_Map_v5 is 5v5, Tuning.BotPlan has 5 slots], else none.
constexpr std::uint32_t kDescriptorBotsFillTeams = 0x400;  // nGameDescriptors.BotsFillTeams (Lua/GlobalDefinitions)
constexpr int kBotsFillTeamsSeats = 5;
inline int BotTeamSize(int option, int fillTeams, std::uint32_t descriptors) {
  if (option >= 0) return option;
  if (fillTeams > 0) return fillTeams;
  return (descriptors & kDescriptorBotsFillTeams) ? kBotsFillTeamsSeats : 0;
}

}  // namespace gms::bots
