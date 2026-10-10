// gms/core/bots (BOTCOMP): the preferred 5v5 bot team setup without the engine. Header-only.
//
// The user's spec (2026-10-10): a team of 2 Gladiators (one per lane), 1 Tactician supporting one of those lanes,
// 1 Predator supporting the other lane (and harassing workers when his team outnumbers the enemy in his lane), 1 Hunter
// (camps; the parasite only with enough bots gathered, or to contest a steal). The roles are RolesTuning.role[] indexes
// (mRole): 0 Gladiator ("Carry"), 1 Tactician ("Support"), 2 Hunter ("Jungler"), 3 Predator ("Roamer") [data].
//
// BotClass assets with an AIDefinition (AssetData BotClass.json; the others, e.g. PC_Alpha, have none and idle):
//   Gladiator  PC_HighHuntress (carry ranged), PC_Creature (carry ranged)
//   Tactician  PC_Guardian (tank melee), PC_Artist (support ranged), PC_TidalQueen (mage ranged), PC_Sculptor
//              (support melee), PC_Warden (bruiser melee)
//   Hunter     PC_Artificer (bruiser melee)
//   Predator   none. PC_Warden's BotClass (AI warden_Tactician: a bruiser with a Well plan and a Harass phase) is used
//              with the player's mRole set to 3 after LabsPlayer_ReflectBotData [assumed: the role only picks the role
//              passive, the gold/XP modifiers and the shown role; the AI is the shaper's own].
// Lanes (nLaneID via nBot.AssignLane at the deploy; Tuning.BotPlan has 2 top and 3 bottom lane slots):
//   Top     Gladiator 1 (PC_HighHuntress) + Tactician          (2 = topLaners)
//   Bottom  Gladiator 2 (PC_Creature) + Predator + Hunter      (3 = botLaners; the Hunter's lane only matters until
//                                                               it leaves for the camps)
// With humans: a human whose role is known (it picked in shaper select, or the host's status says so) takes the first
// wanted entry of its role off the list; a human with an unknown role takes none, and when the bot seats are fewer
// than the wanted entries the surplus roles go in `dropOrder` (default Predator, Hunter, Tactician, Gladiator: the
// last wanted entry of the role first, so Gladiator 1 stays longest) [assumed order, [host] bot_composition_drop].
#pragma once

#include <algorithm>
#include <cstdint>
#include <set>
#include <string>
#include <vector>

#include "core/bots/lanes.h"  // kLaneTop, kLaneBottom

namespace gms::bots {

enum Role : int { kRoleUnknown = -1, kRoleGladiator = 0, kRoleTactician = 1, kRoleHunter = 2, kRolePredator = 3 };

inline const char* RoleName(int r) {
  switch (r) {
    case kRoleGladiator: return "gladiator";
    case kRoleTactician: return "tactician";
    case kRoleHunter: return "hunter";
    case kRolePredator: return "predator";
  }
  return "unknown";
}
inline int RoleFromName(const std::string& s) {
  for (int r = 0; r < 4; ++r)
    if (s == RoleName(r)) return r;
  return kRoleUnknown;
}

// One wanted seat: a role and the lane its player takes.
struct WantedSeat {
  int role = kRoleUnknown;
  int lane = kLaneNone;
};

// The default 5v5 setup, in the order the seats are filled.
inline std::vector<WantedSeat> DefaultSetup() {
  return {{kRoleGladiator, kLaneTop},    {kRoleTactician, kLaneTop},  {kRoleGladiator, kLaneBottom},
          {kRolePredator, kLaneBottom},  {kRoleHunter, kLaneBottom}};
}

// A BotClass the composition may pick for a role: `roleOverride` says the seat's mRole is set to the wanted role
// after the BotClass's own (the Predator on PC_Warden).
struct ClassChoice {
  std::string botClass;
  bool roleOverride = false;
};
inline std::vector<ClassChoice> ClassPreference(int role) {
  switch (role) {
    case kRoleGladiator: return {{"PC_HighHuntress", false}, {"PC_Creature", false}};
    case kRoleTactician:
      return {{"PC_Guardian", false}, {"PC_Artist", false}, {"PC_TidalQueen", false}, {"PC_Sculptor", false},
              {"PC_Warden", false}};
    case kRoleHunter: return {{"PC_Artificer", false}};
    case kRolePredator: return {{"PC_Warden", true}};
  }
  return {};
}

// A planned seat.
struct ComposedSeat {
  int role = kRoleUnknown;
  int lane = kLaneNone;
  std::string botClass;
  bool roleOverride = false;  // set mRole to `role` after the BotClass's own
  bool fallback = false;      // no preferred class available: any unused one
};

// The roles in the order they give up a seat; unknown names are ignored, missing roles are appended.
inline std::vector<int> ParseDropOrder(const std::string& text) {
  std::vector<int> order;
  size_t pos = 0;
  while (pos <= text.size()) {
    size_t comma = text.find(',', pos);
    if (comma == std::string::npos) comma = text.size();
    const int r = RoleFromName(text.substr(pos, comma - pos));
    if (r != kRoleUnknown && std::find(order.begin(), order.end(), r) == order.end()) order.push_back(r);
    pos = comma + 1;
  }
  for (int r : {kRolePredator, kRoleHunter, kRoleTactician, kRoleGladiator})
    if (std::find(order.begin(), order.end(), r) == order.end()) order.push_back(r);
  return order;
}

// The wanted seats of a team: the default setup minus the known humans' roles, cut to `botSeats` by `dropOrder`, or
// (more seats than entries) extended by repeating the setup. Entries keep the default order.
inline std::vector<WantedSeat> WantedSeats(int botSeats, const std::vector<int>& humanRoles,
                                           const std::vector<int>& dropOrder) {
  std::vector<WantedSeat> wanted = DefaultSetup();
  for (int role : humanRoles) {
    if (role < 0) continue;
    for (size_t i = 0; i < wanted.size(); ++i)
      if (wanted[i].role == role) {
        wanted.erase(wanted.begin() + static_cast<std::ptrdiff_t>(i));
        break;
      }
  }
  if (botSeats < 0) botSeats = 0;
  for (int role : dropOrder) {
    while (static_cast<int>(wanted.size()) > botSeats) {
      int last = -1;
      for (size_t i = 0; i < wanted.size(); ++i)
        if (wanted[i].role == role) last = static_cast<int>(i);
      if (last < 0) break;
      wanted.erase(wanted.begin() + last);
    }
  }
  if (static_cast<int>(wanted.size()) > botSeats) wanted.resize(static_cast<size_t>(botSeats));
  const std::vector<WantedSeat> base = DefaultSetup();
  for (size_t i = 0; static_cast<int>(wanted.size()) < botSeats; ++i) wanted.push_back(base[i % base.size()]);
  return wanted;
}

// Picks a BotClass for every wanted seat. `available` = the BotClass assets that exist (names like "PC_Warden").
// Seats are served Predator, Hunter, Gladiator, Tactician (so the Predator gets PC_Warden before the Tacticians'
// list reaches it); a role whose preferred classes are all used or missing takes any unused available class from
// `anyClass` (fallback = true), or stays empty. Output order = wanted order.
inline std::vector<ComposedSeat> ComposeTeam(const std::vector<WantedSeat>& wanted, const std::set<std::string>& available,
                                             const std::vector<std::string>& anyClass) {
  std::vector<ComposedSeat> out(wanted.size());
  std::set<std::string> used;
  for (size_t i = 0; i < wanted.size(); ++i) {
    out[i].role = wanted[i].role;
    out[i].lane = wanted[i].lane;
  }
  for (int role : {kRolePredator, kRoleHunter, kRoleGladiator, kRoleTactician}) {
    for (size_t i = 0; i < wanted.size(); ++i) {
      if (wanted[i].role != role) continue;
      for (const ClassChoice& c : ClassPreference(role)) {
        if (available.count(c.botClass) && !used.count(c.botClass)) {
          out[i].botClass = c.botClass;
          out[i].roleOverride = c.roleOverride;
          used.insert(c.botClass);
          break;
        }
      }
    }
  }
  for (ComposedSeat& s : out) {
    if (!s.botClass.empty()) continue;
    for (const std::string& name : anyClass)
      if (available.count(name) && !used.count(name)) {
        s.botClass = name;
        s.fallback = true;
        used.insert(name);
        break;
      }
  }
  return out;
}

}  // namespace gms::bots
