// gms/core/bots (BOTDRAFT): the bots of a game draw their shapers and roles after the humans have chosen.
// Header-only, engine-free.
//
// The user's rule (2026-10-20): in a custom game with bots, the bots announce their shapers and roles only after every
// human has made a selection, so a human who wants to play the Hunter doesn't get a bot Hunter (Petrus) beside him, and
// the shapers get shuffled more. Since 2026-10-21 the same in practice games and in the PvP queue's games with bots
// (DraftOn).
//
// What a human "wants" (HumanIntent) is, in this order: its Preferred Roles check boxes in phase 1 (C8 06, the
// original UI's way to say it), a role card it chose on its own role page after its lock-in (C8 07 that changed its
// record's role), the role its shaper is usually played in (TypicalRole: the moba-champion.com guides' top role,
// [guide]), else unknown. Several check boxes: the scarcest role first (Hunter, Predator,
// Tactician, Gladiator) [assumed].
//
// The team setup is BOTVARY's (composition.h ComposeTeamFree): 2 Gladiators (one per lane), 1 Hunter (bottom) and 2
// flex seats (Tactician or Predator, one per lane; at most one Predator per team, from PC_Warden by a coin flip). A
// human of known role takes its role's seat (a Gladiator the bottom one, a Tactician or Predator a flex seat); humans of
// unknown role take seats in the order flex, Hunter, second Gladiator. Then each seat draws its class:
//   * the pools are every available class of the role: the composition's role lists, the Hunter list ([host]
//     bot_hunters), and every profile of profiles.h with that role (so new profiles join the draw by themselves);
//   * shuffled per team with the match's seed; the pins come first: each team's Hunter (DrawHunters over bot_hunters),
//     Tactician (bot_tacticians, the first flex seat) and Gladiator (bot_gladiators, the first Gladiator seat); a pin
//     a human of the team holds gives way to the pool;
//   * unlike the composed bots (bot_draft 0), Mikella (PC_MerchantPrincess_Gladiator) is in the Gladiator pool
//     without a pin; Faris (PC_Golem_Hunter) stays out of the default Hunter list (he can't clear his first camp), as
//     with the composed bots: bot_hunters=all brings him in;
//   * hard rule: no shaper twice on a team, and none a human of the team picked (a profile counts as its base class);
//   * soft rule: a shaper a human of the other team picked, or one the other team's bots drew, only when the pool has
//     nothing else (the original game allowed the same shaper on both teams, blind pick: a shaper is unique within a
//     team only; and there are only three Gladiator classes for four Gladiator seats).
// A seat whose pools are used up takes any unused available class (fallback, with that class's own role).
#pragma once

#include <algorithm>
#include <map>
#include <random>
#include <set>
#include <string>
#include <vector>

#include "core/bots/composition.h"
#include "core/bots/lanesplit.h"
#include "core/bots/profiles.h"

namespace gms::bots {

// Whether a game's bots draw after the humans: [host] bot_draft (`option`: -1 auto, 0 off, 1 always) and createGame's
// mode. Auto takes every game dgserver makes: "custom", "practice" (PRACTICE DAWNGATE) and "pvp" (the matchmaker's,
// with bots in its open seats); a host without createGame (mode "") or with a mode it doesn't know keeps the composed
// bots. The draft itself only runs where shaper select does (the host's BeginDraft).
inline bool DraftOn(int option, const std::string& mode) {
  if (option > 0) return true;
  if (option == 0) return false;
  return mode == "custom" || mode == "practice" || mode == "pvp";
}

// The role a shaper is usually played in, by its PlayerClass name (PC_<X>): the top guide's role of the 420 archived
// moba-champion.com guides [guide]. kRoleUnknown for any other name.
inline int TypicalRole(const std::string& playerClass) {
  struct Row {
    const char* cls;
    int role;
  };
  static const Row kRows[] = {
      {"PC_Artificer", kRoleHunter},       {"PC_Artist", kRoleTactician},         {"PC_Creature", kRoleGladiator},
      {"PC_Guardian", kRoleTactician},     {"PC_HighHuntress", kRoleGladiator},   {"PC_Sculptor", kRoleTactician},
      {"PC_TidalQueen", kRoleTactician},   {"PC_Warden", kRoleTactician},         {"PC_Desecrator", kRoleTactician},
      {"PC_Golem", kRoleGladiator},        {"PC_Survivor", kRoleHunter},          {"PC_Otter", kRoleHunter},
      {"PC_BloodMage", kRoleTactician},    {"PC_WarlockKnight", kRoleHunter},     {"PC_Savage", kRoleGladiator},
      {"PC_Doll", kRoleTactician},         {"PC_Druid", kRoleTactician},          {"PC_MerchantPrincess", kRoleGladiator},
      {"PC_Outlaw", kRoleGladiator},       {"PC_Dancer", kRoleTactician},         {"PC_Dreamer", kRoleGladiator},
      {"PC_ShadowAssassin", kRoleGladiator}, {"PC_Enforcer", kRoleHunter},        {"PC_DeepOne", kRoleGladiator},
      {"PC_Shepherd", kRoleTactician},     {"PC_Alpha", kRoleGladiator},          {"PC_KingOfMasks", kRoleTactician},
      {"PC_Penitent", kRoleHunter},        {"PC_HuTank", kRoleHunter},            {"PC_SnowQueen", kRoleTactician},
      {"PC_Gunman", kRoleGladiator},       {"PC_SkyCaptain", kRoleGladiator},     {"PC_LoreMaster", kRoleGladiator},
      {"PC_Planeswalker", kRoleGladiator}, {"PC_Raider", kRoleHunter}};
  for (const Row& r : kRows)
    if (playerClass == r.cls) return r.role;
  return kRoleUnknown;
}

// Where a human's intention came from (for the log).
enum IntentSource : int { kIntentNone = 0, kIntentPreferred = 1, kIntentRoleCard = 2, kIntentTypical = 3 };
inline const char* IntentSourceName(int s) {
  switch (s) {
    case kIntentPreferred: return "preferred-role boxes";
    case kIntentRoleCard: return "role card";
    case kIntentTypical: return "its shaper's usual role";
  }
  return "none";
}

// A human's intended role: `preferredBits` (bit per role, its C8 06 toggles), `roleCard` (a C8 07 that changed its
// record's role, else -1), `typicalRole` (TypicalRole of its shaper, else -1). `source` (optional) says which decided.
inline int HumanIntent(unsigned preferredBits, int roleCard, int typicalRole, int* source = nullptr) {
  for (int r : {kRoleHunter, kRolePredator, kRoleTactician, kRoleGladiator})
    if (preferredBits & (1u << r)) {
      if (source) *source = kIntentPreferred;
      return r;
    }
  if (roleCard >= 0 && roleCard < 4) {
    if (source) *source = kIntentRoleCard;
    return roleCard;
  }
  if (typicalRole >= 0 && typicalRole < 4) {
    if (source) *source = kIntentTypical;
    return typicalRole;
  }
  if (source) *source = kIntentNone;
  return kRoleUnknown;
}

// Every available class of `role`, in a fixed order (the composition's list, the Hunter list for the Hunter, then the
// profiles of profiles.h with that role), each once; the Predator's PC_Warden entry (roleOverride) is left out here.
inline std::vector<std::string> DraftPool(int role, const std::set<std::string>& available,
                                          const std::vector<std::string>& hunters) {
  std::vector<std::string> out;
  auto add = [&](const std::string& c) {
    if (available.count(c) && std::find(out.begin(), out.end(), c) == out.end()) out.push_back(c);
  };
  if (role == kRoleHunter) {
    for (const std::string& h : hunters) add(h);
  } else {
    for (const ClassChoice& c : ClassPreference(role))
      if (!c.roleOverride) add(c.botClass);
  }
  for (const Profile& p : Profiles())
    if (p.role == role && (role != kRoleHunter || hunters.empty())) add(p.name);
  return out;
}

struct DraftHuman {
  int index = -1;
  int team = 0;
  std::string baseClass;  // its shaper's PlayerClass (PC_<X>), "" unknown
  int role = kRoleUnknown;  // HumanIntent
};

struct DraftTeam {
  int team = 0;
  int seats = 0;  // its bot players
};

struct DraftOptions {
  unsigned seed = 1;
  std::vector<std::string> hunters = {"PC_Artificer"};  // ParseHunterList([host] bot_hunters)
  bool huntersPinned = false;  // bot_hunters names classes (not "" or "default"): each team's Hunter is a pin
  std::vector<std::string> tacticians;                  // ParseTacticianList([host] bot_tacticians); empty = no pin
  std::vector<std::string> gladiators;                  // ParseGladiatorList([host] bot_gladiators); empty = no pin
  // MAPHOST: false on a level without jungle camps (MM_Map_v3): no Hunter seat, its seat becomes a flex seat at the
  // bottom (composition.h NoJungleSeat), and the fallback takes no Hunter class
  bool jungle = true;
  // BOTLANESPLIT: each team's drawn bots divide their roles over the lanes, the humans' intents counted (lanesplit.h)
  bool laneSplit = true;
};

// Picks the first entry of `order` whose base class is neither in `hard` nor (first pass) in `soft`; "" = none. A
// `pin` (an operator's [host] bot_* pin) in `order` wins over `soft`, not over `hard`.
inline std::string DraftPick(const std::vector<std::string>& order, const std::set<std::string>& hard,
                             const std::set<std::string>& soft, const std::string& pin = "") {
  if (!pin.empty() && std::find(order.begin(), order.end(), pin) != order.end() && !hard.count(BaseClass(pin)))
    return pin;
  for (int pass = 0; pass < 2; ++pass)
    for (const std::string& c : order) {
      const std::string base = BaseClass(c);
      if (hard.count(base) || (pass == 0 && soft.count(base))) continue;
      return c;
    }
  return "";
}

// One team's bot seats (output order: top Gladiator, top flex, bottom Gladiator, bottom flex, Hunter, any extra flex;
// as many as `seats`). `humanRoles`: the team's humans' intents; `hard`/`soft`: base classes (header comment).
// `hunterFirst`/`tacticianFirst`: the team's pins ("" = none). `rng` shuffles the pools and flips the Warden's coin.
inline std::vector<ComposedSeat> ComposeDraftTeam(int seats, const std::vector<int>& humanRoles,
                                                  std::set<std::string> hard, const std::set<std::string>& soft,
                                                  const std::set<std::string>& available, const DraftOptions& o,
                                                  const std::string& hunterFirst, const std::string& tacticianFirst,
                                                  const std::string& gladiatorFirst, std::mt19937& rng) {
  if (seats <= 0) return {};
  // the seats by role (positions: g top/bottom, f top/bottom, h)
  std::vector<int> g = {kLaneTop, kLaneBottom}, f = {kLaneTop, kLaneBottom};
  int h = o.jungle ? 1 : 0;  // MAPHOST: no Hunter seat without camps (the extra flex seat below takes it)
  bool predatorTaken = false;
  for (int role : humanRoles) {
    if (role == kRoleHunter && h > 0) --h;
    else if (role == kRoleGladiator && !g.empty()) g.pop_back();
    else if ((role == kRoleTactician || role == kRolePredator) && !f.empty()) f.pop_back();
    if (role == kRolePredator) predatorTaken = true;
  }
  // humans of unknown role, or more bots than open roles: the flex seats first, then the Hunter, then the 2nd Gladiator
  while (static_cast<int>(g.size() + f.size()) + h > seats) {
    if (!f.empty()) f.pop_back();
    else if (h > 0) --h;
    else if (g.size() > 1) g.pop_back();
    else break;
  }
  std::vector<int> extra;  // more bot seats than the setup has: flex seats at the bottom
  while (static_cast<int>(g.size() + f.size() + extra.size()) + h < seats) extra.push_back(kLaneBottom);

  auto shuffled = [&](std::vector<std::string> v, const std::string& first) {
    std::shuffle(v.begin(), v.end(), rng);
    if (!first.empty() && available.count(first)) {
      v.erase(std::remove(v.begin(), v.end(), first), v.end());
      v.insert(v.begin(), first);
    }
    return v;
  };
  auto seat = [](const std::string& cls, int role, int lane) {
    ComposedSeat s;
    s.botClass = cls;
    s.role = role;
    s.lane = lane;
    return s;
  };
  // any available class as the last resort (its own role)
  std::vector<std::string> anyClass;
  for (const std::string& c : available)
    if (o.jungle || ClassRole(c) != kRoleHunter) anyClass.push_back(c);
  auto fallback = [&](int lane) {
    ComposedSeat s;
    s.lane = lane;
    const std::string c = DraftPick(shuffled(anyClass, ""), hard, soft);
    if (c.empty()) return s;
    s.botClass = c;
    s.role = ClassRole(c) >= 0 ? ClassRole(c) : kRoleTactician;
    s.fallback = true;
    hard.insert(BaseClass(c));
    return s;
  };

  ComposedSeat hunterSeat;
  if (h > 0) {  // the scarce pools first: the Hunter, then the Gladiators, then the flex seats
    const std::vector<std::string> order = shuffled(DraftPool(kRoleHunter, available, o.hunters), hunterFirst);
    std::string c = DraftPick(order, hard, soft, o.huntersPinned ? hunterFirst : "");
    if (c.empty()) {  // the list's classes all taken: the other Hunter classes (e.g. Faris outside the default list)
      std::vector<std::string> others;
      for (const std::string& x : DraftPool(kRoleHunter, available, {}))
        if (std::find(order.begin(), order.end(), x) == order.end()) others.push_back(x);
      for (const std::string& x : HunterClasses())
        if (available.count(x) && std::find(order.begin(), order.end(), x) == order.end() &&
            std::find(others.begin(), others.end(), x) == others.end())
          others.push_back(x);
      c = DraftPick(shuffled(others, ""), hard, soft);
    }
    hunterSeat = c.empty() ? fallback(kLaneBottom) : seat(c, kRoleHunter, kLaneBottom);
    if (!c.empty()) hard.insert(BaseClass(c));
  }
  std::vector<ComposedSeat> gs;
  {
    const std::vector<std::string> pool = shuffled(DraftPool(kRoleGladiator, available, o.hunters), "");
    for (size_t i = 0; i < g.size(); ++i) {
      const int lane = g[i];
      std::vector<std::string> order = pool;
      if (i == 0 && !gladiatorFirst.empty() && available.count(gladiatorFirst)) {  // MIKELLABOT's pin: the first seat
        order.erase(std::remove(order.begin(), order.end(), gladiatorFirst), order.end());
        order.insert(order.begin(), gladiatorFirst);
      }
      const std::string c = DraftPick(order, hard, soft, i == 0 ? gladiatorFirst : "");
      if (c.empty()) {
        gs.push_back(fallback(lane));
        continue;
      }
      hard.insert(BaseClass(c));
      gs.push_back(seat(c, kRoleGladiator, lane));
    }
  }
  std::vector<ComposedSeat> fs;
  {
    std::vector<int> lanes = f;
    lanes.insert(lanes.end(), extra.begin(), extra.end());
    std::vector<std::string> order = shuffled(DraftPool(kRoleTactician, available, o.hunters), "");
    if (available.count("PC_Warden") && std::find(order.begin(), order.end(), "PC_Warden") == order.end())
      order.push_back("PC_Warden");
    for (size_t i = 0; i < lanes.size(); ++i) {
      std::vector<std::string> mine = order;
      const bool pinned = i == 0 && !tacticianFirst.empty() && available.count(tacticianFirst) &&
                          !hard.count(BaseClass(tacticianFirst));
      if (pinned) {
        mine.erase(std::remove(mine.begin(), mine.end(), tacticianFirst), mine.end());
        mine.insert(mine.begin(), tacticianFirst);
      }
      const std::string c = DraftPick(mine, hard, soft, pinned ? tacticianFirst : "");
      if (c.empty()) {
        fs.push_back(fallback(lanes[i]));
        continue;
      }
      hard.insert(BaseClass(c));
      ComposedSeat s = seat(c, kRoleTactician, lanes[i]);
      // BOTVARY: PC_Warden is the Predator by a coin flip while the team has none (its Predator profile when the copy
      // has it, else its own class with the role set after it)
      if (BaseClass(c) == "PC_Warden" && !predatorTaken && !(pinned && c == tacticianFirst) && (rng() & 1u)) {
        s.role = kRolePredator;
        const std::string profile = ProfileFor("PC_Warden", kRolePredator);
        if (!profile.empty() && available.count(profile)) s.botClass = profile;
        else s.roleOverride = true;
        predatorTaken = true;
      } else if (ClassRole(c) == kRolePredator) {
        s.role = kRolePredator;
        predatorTaken = true;
      }
      fs.push_back(s);
    }
  }
  std::vector<ComposedSeat> out;
  auto add = [&](const std::vector<ComposedSeat>& v, size_t i) {
    if (i < v.size()) out.push_back(v[i]);
  };
  add(gs, 0);
  add(fs, 0);
  add(gs, 1);
  add(fs, 1);
  if (h > 0) out.push_back(hunterSeat);
  for (size_t i = 2; i < fs.size(); ++i) out.push_back(fs[i]);
  out.erase(std::remove_if(out.begin(), out.end(), [](const ComposedSeat& s) { return s.botClass.empty(); }), out.end());
  if (static_cast<int>(out.size()) > seats) out.resize(static_cast<size_t>(seats));
  return out;
}

// The whole match's draw, team by team in `teams` order: each team's seats (ComposeDraftTeam) with its humans' intents,
// hard-excluding its humans' shapers, soft-excluding the other team's humans' shapers and the classes the teams drawn
// before it took. Key = team.
inline std::map<int, std::vector<ComposedSeat>> DraftMatch(const std::vector<DraftTeam>& teams,
                                                           const std::vector<DraftHuman>& humans,
                                                           const std::set<std::string>& available,
                                                           const DraftOptions& o) {
  std::map<int, std::vector<ComposedSeat>> out;
  const int n = static_cast<int>(teams.size());
  const std::vector<std::string> hunterOf = DrawHunters(o.hunters, available, o.seed, n);
  const std::vector<std::string> tacticianOf = DrawPerTeam(o.tacticians, available, o.seed, n, 0x54414354u);  // "TACT"
  const std::vector<std::string> gladiatorOf = DrawPerTeam(o.gladiators, available, o.seed, n, 0x474C4144u);  // "GLAD"
  std::set<std::string> drawn;  // the bases earlier teams' bots took
  for (int ti = 0; ti < n; ++ti) {
    const DraftTeam& t = teams[static_cast<size_t>(ti)];
    std::vector<int> roles;
    std::set<std::string> hard, soft = drawn;
    for (const DraftHuman& hu : humans) {
      if (hu.team == t.team) {
        roles.push_back(hu.role);
        if (!hu.baseClass.empty()) hard.insert(hu.baseClass);
      } else if (!hu.baseClass.empty()) {
        soft.insert(hu.baseClass);
      }
    }
    std::mt19937 rng(o.seed ^ (static_cast<unsigned>(t.team) * 0x9E3779B9u));
    std::vector<ComposedSeat> seats = ComposeDraftTeam(t.seats, roles, hard, soft, available, o,
                                                       hunterOf[static_cast<size_t>(ti)],
                                                       tacticianOf[static_cast<size_t>(ti)],
                                                       gladiatorOf[static_cast<size_t>(ti)], rng);
    if (o.laneSplit) SplitSeatLanes(seats, roles);  // BOTLANESPLIT
    for (const ComposedSeat& s : seats) drawn.insert(BaseClass(s.botClass));
    out[t.team] = seats;
  }
  return out;
}

}  // namespace gms::bots
