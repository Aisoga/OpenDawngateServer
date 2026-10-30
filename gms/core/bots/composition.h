// gms/core/bots (BOTCOMP): the preferred 5v5 bot team setup without the engine. Header-only.
//
// The user's spec (2026-10-10): a team of 2 Gladiators (one per lane), 1 Tactician supporting one of those lanes,
// 1 Predator supporting the other lane (and harassing workers when his team outnumbers the enemy in his lane), 1 Hunter
// (camps; the parasite only with enough bots gathered, or to contest a steal). The roles are RolesTuning.role[] indexes
// (mRole): 0 Gladiator ("Carry"), 1 Tactician ("Support"), 2 Hunter ("Jungler"), 3 Predator ("Roamer") [data].
//
// BotClass assets with an AIDefinition (AssetData BotClass.json; the others, e.g. PC_Alpha, have none and idle):
//   Gladiator  PC_HighHuntress (carry ranged), PC_Creature (carry ranged); MIKELLABOT: our profile
//              PC_MerchantPrincess_Gladiator (Mikella on the High Huntress's AI), only by [host] bot_gladiators
//   Tactician  PC_Guardian (tank melee), PC_Artist (support ranged), PC_TidalQueen (mage ranged), PC_Sculptor
//              (support melee), PC_Warden (bruiser melee); DESECBOT: our profile PC_Desecrator_Tactician (Desecrator,
//              tank melee, on the Guardian's AI: profiles.h), in the draw when the copy has it ([host] bot_tacticians)
//   Hunter     PC_Artificer (bruiser melee); HUNTERBOT: our profiles PC_Golem_Hunter (Faris) and PC_Survivor_Hunter
//              (Freia) on his AI (profiles.h), drawn per match (DrawHunters, [host] bot_hunters); MOYABOT:
//              PC_Otter_Hunter (Moya), in "all" and a list naming her, not yet in the default draw
//   Predator   none shipped. BOTPROFILE: our profile PC_Warden_Predator (profiles.h: PC_Warden's BotClass, AI
//              warden_Tactician, a bruiser with a Well plan and a Harass phase, with role Predator) when the copy has
//              it; else PC_Warden's own with the player's mRole set to 3 after LabsPlayer_ReflectBotData [assumed: the
//              role only picks the role passive, the gold/XP modifiers and the shown role; the AI is the shaper's own].
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
#include <random>
#include <set>
#include <string>
#include <vector>

#include "core/bots/lanes.h"  // kLaneTop, kLaneBottom
#include "core/bots/profiles.h"

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

// MAPHOST: what a Hunter seat becomes on a level without jungle camps (MM_Map_v3: one lane, no camps; HUNTERROUTE's
// steps and the Farm plan's MoveToNextCamp find nothing, so a bot Hunter stood in its base at level 1 for the whole
// match, MAPLEVEL1). The original's rules for that map aren't known; the host's default [assumed]: a fifth laner, a
// flex (Tactician) seat at the bottom.
inline WantedSeat NoJungleSeat() { return {kRoleTactician, kLaneBottom}; }

// The default 5v5 setup, in the order the seats are filled; `jungle` false: the Hunter's seat is NoJungleSeat().
inline std::vector<WantedSeat> DefaultSetup(bool jungle = true) {
  return {{kRoleGladiator, kLaneTop},    {kRoleTactician, kLaneTop},  {kRoleGladiator, kLaneBottom},
          {kRolePredator, kLaneBottom},  jungle ? WantedSeat{kRoleHunter, kLaneBottom} : NoJungleSeat()};
}

// A BotClass the composition may pick for a role: `roleOverride` says the seat's mRole is set to the wanted role
// after the BotClass's own (the Predator on PC_Warden, when the copy lacks the PC_Warden_Predator profile).
struct ClassChoice {
  std::string botClass;
  bool roleOverride = false;
};
inline std::vector<ClassChoice> ClassPreference(int role) {
  switch (role) {
    case kRoleGladiator: return {{"PC_HighHuntress", false}, {"PC_Creature", false}};
    case kRoleTactician:
      return {{"PC_Guardian", false},  {"PC_Artist", false}, {"PC_TidalQueen", false},
              {"PC_Sculptor", false},  {"PC_Warden", false}, {"PC_Desecrator_Tactician", false}};
    case kRoleHunter: return {{"PC_Artificer", false}};
    case kRolePredator: return {{"PC_Warden_Predator", false}, {"PC_Warden", true}};
  }
  return {};
}

// HUNTERBOT: the classes the Hunter seat draws from: Petrus's own and the profiles on his AI (profiles.h; MOYABOT:
// Moya's, PC_Otter_Hunter).
inline const std::vector<std::string>& HunterClasses() {
  static const std::vector<std::string> k = {"PC_Artificer", "PC_Golem_Hunter", "PC_Survivor_Hunter",
                                             "PC_Otter_Hunter"};
  return k;
}

// The default draw: Petrus and Freia. Faris (PC_Golem_Hunter) stays out until his camp clear works: at level 1 he
// can't kill the Ugger camp his route starts with and fell 6-11 levels behind in the live matches; "all" or a list
// naming him brings him in. MOYABOT: Moya (PC_Otter_Hunter) likewise until a live match shows her clear.
inline const std::vector<std::string>& DefaultHunters() {
  static const std::vector<std::string> k = {"PC_Artificer", "PC_Survivor_Hunter"};
  return k;
}

// The role a BotClass plays by itself: a profile's own, else the composition's role lists (PC_Warden: Tactician).
// BOTDRAFT's fallback seats take it; BOTLANESPLIT counts a fallback seat's role with it.
inline int ClassRole(const std::string& botClass) {
  for (const Profile& p : Profiles())
    if (botClass == p.name) return p.role;
  for (int role : {kRoleGladiator, kRoleHunter, kRoleTactician})
    for (const ClassChoice& c : ClassPreference(role))
      if (c.botClass == botClass && !c.roleOverride) return role;
  for (const std::string& h : HunterClasses())
    if (h == botClass) return kRoleHunter;
  return kRoleUnknown;
}

// [host] bot_hunters: "" or "default" = DefaultHunters(); "all" = every Hunter class (HunterClasses' order); else a
// comma list in the listed order, each a class or profile name, a shaper's name or its noun's (artificer/petrus,
// golem/faris, survivor/freia, otter/moya; any case); "0", "off" and "none" = PC_Artificer alone (the old single Hunter). Unknown
// names are ignored; nothing left = PC_Artificer.
inline std::vector<std::string> ParseHunterList(const std::string& text) {
  auto lower = [](std::string s) {
    for (char& c : s) c = static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
    return s;
  };
  const std::string all = lower(text);
  if (all.empty() || all == "default" || all == "1") return DefaultHunters();
  if (all == "all") return HunterClasses();
  std::vector<std::string> out;
  size_t pos = 0;
  while (pos <= all.size()) {
    size_t comma = all.find(',', pos);
    if (comma == std::string::npos) comma = all.size();
    std::string w = all.substr(pos, comma - pos);
    while (!w.empty() && w.front() == ' ') w.erase(w.begin());
    while (!w.empty() && w.back() == ' ') w.pop_back();
    pos = comma + 1;
    std::string cls;
    if (w == "artificer" || w == "petrus" || w == "pretus" || w == "pc_artificer" || w == "0" || w == "off" ||
        w == "none")
      cls = "PC_Artificer";
    else if (w == "golem" || w == "faris" || w == "pc_golem" || w == "pc_golem_hunter")
      cls = "PC_Golem_Hunter";
    else if (w == "survivor" || w == "freia" || w == "pc_survivor" || w == "pc_survivor_hunter")
      cls = "PC_Survivor_Hunter";
    else if (w == "otter" || w == "moya" || w == "pc_otter" || w == "pc_otter_hunter")  // MOYABOT
      cls = "PC_Otter_Hunter";
    if (!cls.empty() && std::find(out.begin(), out.end(), cls) == out.end()) out.push_back(cls);
  }
  if (out.empty()) out.push_back("PC_Artificer");
  return out;
}

// The class of one seat for each of `teams` teams (index = the team's place in the plan), from `wanted` as far as
// `available` has them, team i taking entry i mod n: seed 0 in the listed order (reproducible: team 1 the first, team
// 2 the second; a list of one pins it for both), else shuffled once per match with the seat's own `salt` (the other
// draws stay unchanged). So the two teams of a match get different classes whenever two are available. "" = none.
inline std::vector<std::string> DrawPerTeam(const std::vector<std::string>& wanted, const std::set<std::string>& available,
                                            unsigned seed, int teams, unsigned salt) {
  std::vector<std::string> have;
  for (const std::string& c : wanted)
    if (available.count(c)) have.push_back(c);
  std::vector<std::string> out(static_cast<size_t>(teams > 0 ? teams : 0));
  if (have.empty()) return out;
  if (seed != 0) {
    std::mt19937 rng(seed ^ salt);
    std::shuffle(have.begin(), have.end(), rng);
  }
  for (size_t i = 0; i < out.size(); ++i) out[i] = have[i % have.size()];
  return out;
}

// The Hunter class of each team, from `wanted` (ParseHunterList); "" = none available (the seat takes PC_Artificer's
// path).
inline std::vector<std::string> DrawHunters(const std::vector<std::string>& wanted, const std::set<std::string>& available,
                                            unsigned seed, int teams) {
  return DrawPerTeam(wanted, available, seed, teams, 0x48554E54u);  // "HUNT"
}

// DESECBOT: the classes with the Tactician AI the flex seats draw from (BOTVARY), the composition's Tactician list.
inline std::vector<std::string> TacticianClasses() {
  std::vector<std::string> out;
  for (const ClassChoice& c : ClassPreference(kRoleTactician)) out.push_back(c.botClass);
  return out;
}

// [host] bot_tacticians: "" or "default" = no pin (the draw as it is); else a comma list in the listed order, each a
// class or profile name or a shaper's noun name (guardian, artist, tidalqueen, sculptor, warden, desecrator; any case),
// pinning each team's first Tactician seat (the top lane's, beside Gladiator 1): team i takes entry i mod n (DrawPerTeam).
// Unknown names are ignored; nothing left = no pin.
inline std::vector<std::string> ParseTacticianList(const std::string& text) {
  auto lower = [](std::string s) {
    for (char& c : s) c = static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
    return s;
  };
  const std::string all = lower(text);
  std::vector<std::string> out;
  if (all.empty() || all == "default") return out;
  const std::vector<std::string> classes = TacticianClasses();
  size_t pos = 0;
  while (pos <= all.size()) {
    size_t comma = all.find(',', pos);
    if (comma == std::string::npos) comma = all.size();
    std::string w = all.substr(pos, comma - pos);
    while (!w.empty() && w.front() == ' ') w.erase(w.begin());
    while (!w.empty() && w.back() == ' ') w.pop_back();
    pos = comma + 1;
    for (const std::string& c : classes) {
      const std::string base = lower(BaseClass(c));  // "pc_desecrator"
      if (w == lower(c) || w == base || "pc_" + w == base) {
        if (std::find(out.begin(), out.end(), c) == out.end()) out.push_back(c);
        break;
      }
    }
  }
  return out;
}

// MIKELLABOT: the classes a Gladiator seat may be pinned to: the two shipped Gladiator AIs and our profile on the High
// Huntress's (profiles.h). The draw itself stays the two shipped ones (BOTVARY); the profile comes only by the pin.
inline std::vector<std::string> GladiatorClasses() {
  return {"PC_HighHuntress", "PC_Creature", "PC_MerchantPrincess_Gladiator"};
}

// [host] bot_gladiators: "" or "default" = no pin; else a comma list in the listed order, each a class or profile name
// or a shaper's noun or name (highhuntress/nissa, creature/vex, merchantprincess/merchant_princess/mikella; any case),
// pinning each team's first Gladiator seat (the top lane's in the fixed setup and the free draw): team i takes entry
// i mod n (DrawPerTeam). Unknown names are ignored; nothing left = no pin.
inline std::vector<std::string> ParseGladiatorList(const std::string& text) {
  auto lower = [](std::string s) {
    for (char& c : s) c = static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
    return s;
  };
  const std::string all = lower(text);
  std::vector<std::string> out;
  if (all.empty() || all == "default") return out;
  size_t pos = 0;
  while (pos <= all.size()) {
    size_t comma = all.find(',', pos);
    if (comma == std::string::npos) comma = all.size();
    std::string w = all.substr(pos, comma - pos);
    while (!w.empty() && w.front() == ' ') w.erase(w.begin());
    while (!w.empty() && w.back() == ' ') w.pop_back();
    pos = comma + 1;
    if (w == "nissa") w = "highhuntress";
    else if (w == "vex") w = "creature";
    else if (w == "mikella" || w == "merchant_princess") w = "merchantprincess";
    for (const std::string& c : GladiatorClasses()) {
      const std::string base = lower(BaseClass(c));  // "pc_merchantprincess"
      if (w == lower(c) || w == base || "pc_" + w == base) {
        if (std::find(out.begin(), out.end(), c) == out.end()) out.push_back(c);
        break;
      }
    }
  }
  return out;
}

// The roles (bit per role) a BotClass may play besides its seat's, when the human teammates fill the seat's role: the
// label (mRole: role passive, gold/XP modifiers, the Predator's well harassing) changes, the class's AI does not, so
// only a class whose AI fits the other role is allowed one: PC_Warden plays both the Tactician and the Predator (and so
// does its Predator profile; the session's role follow-up then shows the label the seat took).
inline unsigned AltRoles(const std::string& botClass) {
  if (BaseClass(botClass) == "PC_Warden") return (1u << kRoleTactician) | (1u << kRolePredator);
  return 0;
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
// (more seats than entries) extended by repeating the setup. Entries keep the default order. `jungle` false (MAPHOST):
// DefaultSetup(false), no Hunter seat.
inline std::vector<WantedSeat> WantedSeats(int botSeats, const std::vector<int>& humanRoles,
                                           const std::vector<int>& dropOrder, bool jungle = true) {
  std::vector<WantedSeat> wanted = DefaultSetup(jungle);
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
  const std::vector<WantedSeat> base = DefaultSetup(jungle);
  for (size_t i = 0; static_cast<int>(wanted.size()) < botSeats; ++i) wanted.push_back(base[i % base.size()]);
  return wanted;
}

// Picks a BotClass for every wanted seat. `available` = the BotClass assets that exist (names like "PC_Warden").
// Seats are served Predator, Hunter, Gladiator, Tactician (so the Predator gets PC_Warden before the Tacticians'
// list reaches it); a shaper is used once per team, a profile counting as its base class (BaseClass); a role whose preferred classes are all used or missing takes any unused available class from
// `anyClass` (fallback = true), or stays empty. Output order = wanted order.
// `seed` != 0 shuffles each role's preference list first (varied teams from game to game: which Gladiator takes the top
// lane, which Tactician comes); 0 keeps the fixed order.
// HUNTERBOT: `hunterClass` (DrawHunters' entry for this team) is the Hunter seat's first choice, PC_Artificer its
// fallback. DESECBOT: `tacticianClass` (a [host] bot_tacticians pin) is the first Tactician seat's first choice.
inline std::vector<ComposedSeat> ComposeTeam(const std::vector<WantedSeat>& wanted, const std::set<std::string>& available,
                                             const std::vector<std::string>& anyClass, unsigned seed = 0,
                                             const std::string& hunterClass = "", const std::string& tacticianClass = "") {
  std::mt19937 rng(seed);
  std::vector<ComposedSeat> out(wanted.size());
  std::set<std::string> used;
  for (size_t i = 0; i < wanted.size(); ++i) {
    out[i].role = wanted[i].role;
    out[i].lane = wanted[i].lane;
  }
  bool tacticianPinned = false;
  for (int role : {kRolePredator, kRoleHunter, kRoleGladiator, kRoleTactician}) {
    for (size_t i = 0; i < wanted.size(); ++i) {
      if (wanted[i].role != role) continue;
      std::vector<ClassChoice> prefs = ClassPreference(role);
      if (seed != 0) std::shuffle(prefs.begin(), prefs.end(), rng);
      if (role == kRoleHunter && !hunterClass.empty()) prefs.insert(prefs.begin(), ClassChoice{hunterClass, false});
      // a profile before its base class: the class that says the seat's role itself (a profile whose base class
      // isn't in the list, e.g. the Desecrator's, keeps its place)
      std::set<std::string> listed;
      for (const ClassChoice& c : prefs) listed.insert(c.botClass);
      std::stable_partition(prefs.begin(), prefs.end(), [&listed](const ClassChoice& c) {
        return IsProfile(c.botClass) && listed.count(BaseClass(c.botClass)) != 0;
      });
      if (role == kRoleTactician && !tacticianClass.empty() && !tacticianPinned) {
        prefs.insert(prefs.begin(), ClassChoice{tacticianClass, false});
        tacticianPinned = true;
      }
      for (const ClassChoice& c : prefs) {
        if (available.count(c.botClass) && !used.count(BaseClass(c.botClass))) {
          out[i].botClass = c.botClass;
          out[i].roleOverride = c.roleOverride;
          used.insert(BaseClass(c.botClass));
          break;
        }
      }
    }
  }
  for (ComposedSeat& s : out) {
    if (!s.botClass.empty()) continue;
    for (const std::string& name : anyClass)
      if (available.count(name) && !used.count(BaseClass(name))) {
        s.botClass = name;
        s.fallback = true;
        used.insert(BaseClass(name));
        break;
      }
  }
  return out;
}

// BOTVARY (free draw, seed != 0): every team gets exactly 2 Gladiators (both Gladiator classes, one per lane), 1 Hunter
// (PC_Artificer) and 2 flex seats, each a Tactician or a Predator (the user's rule, 2026-10-11): the flex seats draw two
// distinct classes of those with a Tactician AI (Guardian, Artist, TidalQueen, Sculptor, Warden, Desecrator);
// PC_Warden is the Predator (its PC_Warden_Predator profile when available, else mRole 3 on its own Tactician class)
// or, by a coin flip, a Tactician, so a team has 0 or 1 Predator. Lanes as the
// default setup: Gladiator + flex on top, Gladiator + flex + Hunter at the bottom (the Hunter's lane only matters until it
// leaves for the camps); which Gladiator takes the top lane is drawn. A human of known role (`humanRoles`, the team's
// humans' mRole, -1 unknown) takes that role's seat; with still fewer seats the flex seats go first, then the Hunter,
// then the second Gladiator. Output order = top Gladiator, top flex, bottom Gladiator, bottom flex, Hunter.
// DESECBOT: the flex classes are TacticianClasses() (the five above and PC_Desecrator_Tactician, as far as the copy
// has it); `tacticianClass` (a [host] bot_tacticians pin) takes the first flex seat as a Tactician.
// MAPHOST: `jungle` false (a level without camps): no Hunter, a third flex seat at the bottom in the Hunter's place.
inline std::vector<ComposedSeat> ComposeTeamFree(int seats, const std::set<std::string>& available, unsigned seed,
                                                 const std::vector<int>& humanRoles = {},
                                                 const std::string& hunterClass = "",
                                                 const std::string& tacticianClass = "", bool jungle = true) {
  std::mt19937 rng(seed);
  std::vector<std::string> glad = {"PC_HighHuntress", "PC_Creature"};
  std::vector<std::string> flex = TacticianClasses();
  std::shuffle(glad.begin(), glad.end(), rng);
  std::shuffle(flex.begin(), flex.end(), rng);
  auto have = [&](const std::string& c) { return available.count(c) != 0; };
  if (!tacticianClass.empty() && have(tacticianClass)) {
    flex.erase(std::remove(flex.begin(), flex.end(), tacticianClass), flex.end());
    flex.insert(flex.begin(), tacticianClass);
  }
  const std::string pinned = !tacticianClass.empty() && have(tacticianClass) ? tacticianClass : "";
  auto seat = [&](const std::string& cls, int role, int lane) {
    ComposedSeat s;
    s.botClass = cls;
    s.role = role;
    s.lane = lane;
    return s;
  };
  std::vector<ComposedSeat> g, f, h;
  for (size_t i = 0; i < glad.size(); ++i)
    if (have(glad[i])) g.push_back(seat(glad[i], kRoleGladiator, i == 0 ? kLaneTop : kLaneBottom));
  for (const std::string& c : flex) {
    if (f.size() >= (jungle ? 2u : 3u)) break;
    if (!have(c)) continue;
    ComposedSeat s = seat(c, kRoleTactician, f.empty() ? kLaneTop : kLaneBottom);
    if (c == "PC_Warden" && c != pinned && (rng() & 1u)) {
      s.role = kRolePredator;
      const std::string profile = ProfileFor(c, kRolePredator);
      if (!profile.empty() && have(profile)) s.botClass = profile;  // BOTPROFILE: the class itself says Predator
      else s.roleOverride = true;
    }
    f.push_back(s);
  }
  if (!jungle) {
    // MAPHOST: no Hunter; a human Hunter takes nothing
  } else if (!hunterClass.empty() && have(hunterClass)) {
    h.push_back(seat(hunterClass, kRoleHunter, kLaneBottom));  // HUNTERBOT
  } else if (have("PC_Artificer")) {
    h.push_back(seat("PC_Artificer", kRoleHunter, kLaneBottom));
  }
  // BOTTEAM: a human of known role takes that role's seat (a human Hunter: no bot Hunter; a human Gladiator: the bottom
  // Gladiator's seat; a human Tactician or Predator: a flex seat), as WantedSeats does for the fixed setup
  for (int role : humanRoles) {
    if (role == kRoleHunter && !h.empty()) h.pop_back();
    else if (role == kRoleGladiator && g.size() > 1) g.pop_back();
    else if (role == kRolePredator && !f.empty()) {
      // the bot Predator's seat if there is one (a team has at most one Predator), else a flex seat
      auto it = std::find_if(f.begin(), f.end(), [](const ComposedSeat& s) { return s.role == kRolePredator; });
      f.erase(it != f.end() ? it : f.end() - 1);
    } else if (role == kRoleTactician && !f.empty()) {
      f.pop_back();
    }
  }
  // fewer seats than that (humans of unknown role): the flex seats go first, then the Hunter, then the second Gladiator
  while (static_cast<int>(g.size() + f.size() + h.size()) > seats) {
    if (!f.empty()) f.pop_back();
    else if (!h.empty()) h.pop_back();
    else if (g.size() > 1) g.pop_back();
    else break;
  }
  std::vector<ComposedSeat> out;
  auto add = [&](const std::vector<ComposedSeat>& v, size_t i) { if (i < v.size()) out.push_back(v[i]); };
  add(g, 0); add(f, 0); add(g, 1); add(f, 1); add(h, 0); add(f, 2);
  if (static_cast<int>(out.size()) > seats) out.resize(static_cast<size_t>(seats));
  return out;
}

// The BotClass names the host looks up in the copy's catalog before it composes the teams (gms/engine/bots.cpp): every
// role list, the Hunter and Gladiator classes and every profile of profiles.h, each once. A class missing here is never
// "available", so a pin or draw of it finds nothing (MIKELLABOT's first live run: the Gladiator pin said "(missing)").
inline std::vector<std::string> CatalogCandidates() {
  std::vector<std::string> out;
  auto add = [&out](const std::string& n) {
    if (std::find(out.begin(), out.end(), n) == out.end()) out.push_back(n);
  };
  for (int role = 0; role < 4; ++role)
    for (const ClassChoice& c : ClassPreference(role)) add(c.botClass);
  for (const std::string& h : HunterClasses()) add(h);
  for (const std::string& g : GladiatorClasses()) add(g);
  for (const Profile& p : Profiles()) add(p.name);
  return out;
}

// BOTS2c (the head-to-head rule, 2026-10-29): [host] bot_mirror gives every team the same five seats, Gladiator,
// Tactician, Gladiator, Tactician, Hunter (GTGTH; no Predator), with the same BotClass per seat on both sides, so a
// brain against brain match has no lineup difference (the composed draw gave the teams different Hunters, Tacticians and
// sometimes a Predator). Seat order and lanes as the default setup: top Gladiator, top Tactician, bottom Gladiator,
// bottom Tactician, Hunter (bottom until the camps). The default lineup: the classic brain's shipped AIs of each role,
// the High Huntress and the Creature as the Gladiators (top, bottom), the Guardian and the Artist as the Tacticians, the
// Artificer (Petrus) as the Hunter [assumed: the lineup classic plays best with is untested].
inline std::vector<std::string> MirrorDefault() {
  return {"PC_HighHuntress", "PC_Guardian", "PC_Creature", "PC_Artist", "PC_Artificer"};
}
inline const int* MirrorRoles() {
  static const int k[5] = {kRoleGladiator, kRoleTactician, kRoleGladiator, kRoleTactician, kRoleHunter};
  return k;
}

// [host] bot_mirror: "", "0" or "off" = no mirror (empty result); "1", "on" or "default" = MirrorDefault(); else a comma
// list of up to five classes in seat order (GTGTH), each a class or profile name or a shaper's noun (any case:
// "highhuntress", "PC_Guardian"); an entry that is empty, unknown, missing from `available` or a shaper already in the
// lineup takes the default's class for its seat, and a seat whose default is missing too stays empty on every team.
// `why` (optional) collects what was replaced, for the log.
inline std::vector<ComposedSeat> MirrorLineup(const std::string& text, const std::set<std::string>& available,
                                              std::string* why = nullptr) {
  auto lower = [](std::string s) {
    for (char& c : s) c = static_cast<char>(c >= 'A' && c <= 'Z' ? c - 'A' + 'a' : c);
    return s;
  };
  std::string all = lower(text);
  while (!all.empty() && all.front() == ' ') all.erase(all.begin());
  while (!all.empty() && all.back() == ' ') all.pop_back();
  if (all.empty() || all == "0" || all == "off" || all == "none") return {};
  std::vector<std::string> words(5);
  if (all != "1" && all != "on" && all != "default") {
    size_t pos = 0;
    for (size_t i = 0; i < 5 && pos <= all.size(); ++i) {
      size_t comma = all.find(',', pos);
      if (comma == std::string::npos) comma = all.size();
      std::string w = all.substr(pos, comma - pos);
      while (!w.empty() && w.front() == ' ') w.erase(w.begin());
      while (!w.empty() && w.back() == ' ') w.pop_back();
      words[i] = w;
      pos = comma + 1;
    }
  }
  const std::vector<std::string> def = MirrorDefault();
  const int* roles = MirrorRoles();
  const int lanes[5] = {kLaneTop, kLaneTop, kLaneBottom, kLaneBottom, kLaneBottom};
  std::vector<ComposedSeat> out(5);
  std::set<std::string> used;
  auto note = [why](const std::string& s) {
    if (why) *why += (why->empty() ? "" : "; ") + s;
  };
  for (size_t i = 0; i < 5; ++i) {
    std::string cls;
    if (!words[i].empty()) {
      for (const std::string& c : available)
        if (lower(c) == words[i] || lower(c) == "pc_" + words[i]) cls = c;
      if (cls.empty()) note("seat " + std::to_string(i + 1) + " '" + words[i] + "' not available: " + def[i]);
    }
    if (!cls.empty() && used.count(BaseClass(cls))) {
      note("seat " + std::to_string(i + 1) + " " + cls + " twice: " + def[i]);
      cls.clear();
    }
    if (cls.empty()) cls = def[i];
    if (!available.count(cls) || used.count(BaseClass(cls))) {
      note("seat " + std::to_string(i + 1) + " " + cls + " missing: empty");
      cls.clear();
    }
    out[i].role = roles[i];
    out[i].lane = lanes[i];
    out[i].botClass = cls;
    if (!cls.empty()) used.insert(BaseClass(cls));
  }
  return out;
}

// MIKELLABOT: a team's first Gladiator seat takes `gladiatorClass` (a [host] bot_gladiators pin; "" or a class the copy
// lacks: unchanged). A team seat that already holds the same shaper gets the replaced class instead (one shaper per
// team). True when the pin changed a seat.
inline bool PinGladiator(std::vector<ComposedSeat>& seats, const std::string& gladiatorClass,
                         const std::set<std::string>& available) {
  if (gladiatorClass.empty() || !available.count(gladiatorClass)) return false;
  auto first = std::find_if(seats.begin(), seats.end(), [](const ComposedSeat& s) {
    return s.role == kRoleGladiator && !s.botClass.empty();
  });
  if (first == seats.end() || first->botClass == gladiatorClass) return false;
  const std::string old = first->botClass;
  for (ComposedSeat& s : seats)
    if (&s != &*first && !s.botClass.empty() && BaseClass(s.botClass) == BaseClass(gladiatorClass)) {
      s.botClass = old;
      s.roleOverride = false;
    }
  first->botClass = gladiatorClass;
  first->roleOverride = false;
  first->fallback = false;
  return true;
}

}  // namespace gms::bots
