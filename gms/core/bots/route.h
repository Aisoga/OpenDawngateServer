// gms/core/bots (HUNTERROUTE): the bot Hunters' opening route through their own jungle, Vanquish on the camps, and a
// laner's pull at the first buff. Engine-free, header-only; the host (gms/engine/hunterroute.cpp, agents.cpp) feeds it
// the camps' state and carries the choices out.
//
// Why: HUNTERBOT's route is the nearest camp by route cost (jungle.h CampRouteCost). In MOYABOT's three matches team
// 1's Moya started at the Ugger camp (level 1, only Q ranked), took the Buff_Ugger camp next, lost it at 25 % HP and
// fled home: level 2 at 228-230 s against team 2's 144 s. Faris never cleared his first camp (HUNTERBOT-2).
//
// The guides (moba-champion.com 143, Moya, and 72, Voluc) start at the own green (Power) buff, take the small fish and
// mushroom camps on the way to blue (Haste), then the mushroom and Ugger camps, orange (Armor), the last Ugger camp and
// the Gold Pigs, Vanquish on the buffs and the pigs: "you're 6. Gank." Mapped to SM_Map_v5's camps [data]:
//   green  = SpawnPoint_v5_Buff_Fish    (Jungle_Fish_Major   -> modifier_Jungle_GreenBuff:  +15 Power, +10 % Power)
//   blue   = SpawnPoint_v5_Buff_Fungus  (Jungle_Fungus_Major -> modifier_Jungle_BlueBuff:   +20 Haste, more on casts)
//   orange = SpawnPoint_v5_Buff_Ugger   (Jungle_Ugger_Major  -> modifier_Jungle_OrangeBuff: +20 armor, damage reduction)
//   small fish / mushroom / Ugger camps = SpawnPoint_v5_CreepsFish / CreepsFungus / CreepsUgger
//   the Gold (Money) Pigs = SpawnPoint_v5_CreepsBoar (a Boar medium and major: 162 gold, 215 XP; first spawn 210 s, its noun's;
//   the marker's 120 s with [host] camp_timers=marker, CAMPTIMER)
// Every buff camp alone gives level 2 (260-280 XP; level 2 needs 260), a small camp 135-200.
#pragma once

#include <cmath>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "core/matchflow/camprole.h"

namespace gms::bots {

enum RouteKind : int { kRouteSmall = 0, kRouteBuff = 1, kRoutePigs = 2 };

struct RouteStep {
  float x = 0.f, y = 0.f;  // the camp's marker (team 1's; team 2's through the map's centre, SM_Map_v5 is point-symmetric)
  int kind = kRouteSmall;
  int minLevel = 1;        // taken only from this level on (else later steps first)
  std::string label;       // the guides' name (MAPED4's derived route: the camp's role and noun)
};

// The opening of team `team` (1, 2); empty for any other. Team 1's camps in the guides' order on its half (base at
// (-182, -182)); orange and the pigs from level 3 [assumed: the camps a level-1 Hunter lost].
inline std::vector<RouteStep> OpeningRoute(int team) {
  static const RouteStep kTeam1[] = {
      {68.0f, -165.6f, kRouteBuff, 1, "green (Power) Buff_Fish"},
      {65.7f, -132.2f, kRouteSmall, 1, "small fish"},
      {-4.8f, -163.7f, kRouteSmall, 1, "small fish"},
      {-54.4f, -88.7f, kRouteSmall, 1, "small mushroom"},
      {-59.4f, -57.4f, kRouteBuff, 1, "blue (Haste) Buff_Fungus"},
      {-87.5f, -52.6f, kRouteSmall, 1, "small mushroom"},
      {-67.4f, 3.3f, kRouteSmall, 1, "small mushroom"},
      {-170.9f, -9.4f, kRouteSmall, 1, "small Ugger"},
      {-169.1f, 61.5f, kRouteBuff, 3, "orange (Armor) Buff_Ugger"},
      {-219.8f, 76.9f, kRouteSmall, 1, "small Ugger"},
      {-175.8f, 147.9f, kRoutePigs, 3, "the Gold Pigs (Boar)"},
  };
  std::vector<RouteStep> out;
  if (team != 1 && team != 2) return out;
  for (const RouteStep& s : kTeam1) {
    RouteStep m = s;
    if (team == 2) {
      m.x = -s.x;
      m.y = -s.y;
    }
    out.push_back(m);
  }
  return out;
}

// ---- MAPED4: the opening route from the level's camps ---------------------------------------------------------------
// OpeningRoute is SM_Map_v5's camps by coordinates: on a compiled map every step found no camp ("no such camp", MAPED3's
// CM_Grove). DeriveRoute builds the route from the level's camp markers and their roles (gms/core/matchflow/camprole.h)
// by one rule, and on SM_Map_v5's 22 camps gives OpeningRoute's 11 steps again (gms_tests MapedRouteSmIsTheTable):
//   - the team's half: the camps nearer the team's Guardian than the enemy's (without both Guardians: x + y < 0 for
//     team 1, the diagonal rule of jungle.h);
//   - the order: from a buff camp of that half (any camp when it has none), each next step the nearest camp not taken
//     yet; of the buff camps the start whose walk is shortest (SM_Map_v5's team 1: green 632 units, blue 775, orange
//     834). That is the guides' route on SM_Map_v5 (green, the small camps toward blue, blue, ..., orange, the pigs);
//   - the levels: the first `buffsAtLevel1` buff camps of the route and every small camp from level 1, a later buff
//     camp and the big camps (the pigs) from `lateMinLevel` (HUNTERROUTE's orange and pigs at 3) [assumed];
//   - at most 32 steps (RouteMemory's bits).
struct CampMarker {
  float x = 0.f, y = 0.f;
  flow::CampRole role = flow::CampRole::kSmall;
  std::string name;  // the camp's noun (the step's label)
};

struct RouteDeriveConfig {
  int buffsAtLevel1 = 2;
  int lateMinLevel = 3;
};

// Whether (x, y) is on `team`'s half: nearer its own Guardian `own` than the enemy's `enemy` (both non-null), else the
// diagonal (team 1: x + y < 0; team 2: x + y > 0).
inline bool OnOwnHalf(float x, float y, int team, const float* own, const float* enemy) {
  if (own && enemy) {
    const float ox = x - own[0], oy = y - own[1], ex = x - enemy[0], ey = y - enemy[1];
    return ox * ox + oy * oy < ex * ex + ey * ey;
  }
  return team == 1 ? x + y < 0.f : team == 2 ? x + y > 0.f : false;
}

inline std::vector<RouteStep> DeriveRoute(const std::vector<CampMarker>& camps, int team, const float* ownGuardian,
                                          const float* enemyGuardian, const RouteDeriveConfig& cfg = RouteDeriveConfig()) {
  std::vector<RouteStep> out;
  if (team != 1 && team != 2) return out;
  std::vector<int> own;
  for (int i = 0; i < static_cast<int>(camps.size()); ++i) {
    const CampMarker& c = camps[static_cast<std::size_t>(i)];
    if (OnOwnHalf(c.x, c.y, team, ownGuardian, enemyGuardian)) own.push_back(i);
  }
  if (own.empty()) return out;
  auto dist = [&](int a, int b) {
    const float dx = camps[static_cast<std::size_t>(a)].x - camps[static_cast<std::size_t>(b)].x;
    const float dy = camps[static_cast<std::size_t>(a)].y - camps[static_cast<std::size_t>(b)].y;
    return std::sqrt(dx * dx + dy * dy);
  };
  // the walk from `start`: the nearest camp not taken yet, every time (ties: the earlier camp)
  auto walk = [&](int start, float* length) {
    std::vector<int> order(1, start);
    std::vector<bool> taken(camps.size(), false);
    taken[static_cast<std::size_t>(start)] = true;
    *length = 0.f;
    while (order.size() < own.size()) {
      int best = -1;
      float bd = 0.f;
      for (int k : own) {
        if (taken[static_cast<std::size_t>(k)]) continue;
        const float d = dist(order.back(), k);
        if (best < 0 || d < bd) {
          best = k;
          bd = d;
        }
      }
      taken[static_cast<std::size_t>(best)] = true;
      order.push_back(best);
      *length += bd;
    }
    return order;
  };
  bool anyBuff = false;
  for (int k : own) anyBuff = anyBuff || camps[static_cast<std::size_t>(k)].role == flow::CampRole::kBuff;
  std::vector<int> bestOrder;
  float bestLen = 0.f;
  for (int k : own) {
    if (anyBuff && camps[static_cast<std::size_t>(k)].role != flow::CampRole::kBuff) continue;
    float len = 0.f;
    std::vector<int> order = walk(k, &len);
    if (bestOrder.empty() || len < bestLen) {
      bestOrder = order;
      bestLen = len;
    }
  }
  int buffs = 0;
  for (int k : bestOrder) {
    if (out.size() >= 32) break;
    const CampMarker& c = camps[static_cast<std::size_t>(k)];
    RouteStep s;
    s.x = c.x;
    s.y = c.y;
    s.kind = c.role == flow::CampRole::kBuff ? kRouteBuff : c.role == flow::CampRole::kBig ? kRoutePigs : kRouteSmall;
    if (c.role == flow::CampRole::kBuff) ++buffs;
    const bool late = c.role == flow::CampRole::kBig || (c.role == flow::CampRole::kBuff && buffs > cfg.buffsAtLevel1);
    s.minLevel = late && !out.empty() ? cfg.lateMinLevel : 1;
    s.label = std::string(flow::CampRoleName(c.role)) + " " + (c.name.empty() ? "camp" : c.name);
    out.push_back(s);
  }
  return out;
}

struct RouteConfig {
  int endLevel = 6;         // the route ends at this level (the guides: "you're 6. Gank.") [assumed]
  double endS = 600.0;      // ... or at this match time [assumed]
  double waitS = 20.0;      // a camp not up yet is walked to when it spawns within this [assumed]
  float buffHpMin = 0.5f;   // a buff or pig step (but the first) only at this HP share or more [assumed]
  float buffHpHyst = 0.1f;  // HUNTERROUTE-2: a buff step he is on holds down to buffHpMin - this; one he left for
                            // another step not over needs buffHpMin + this (Faris flipped between blue and a small
                            // camp twice a second at 50 % HP for 3 minutes, hr2_mf_on2b) [assumed]
  float matchRadius = 6.f;  // (host) a step's camp: the camp marker within this of the step's point
};

// A step's camp as the host sees it now.
struct RouteCamp {
  bool found = false;   // a camp marker within matchRadius of the step
  bool alive = false;   // spawned and not cleared
  int spawns = 0;       // spawns so far
  double dueAt = -1.0;  // the next spawn while not alive (< 0: unknown)
};

// One Hunter's progress.
struct RouteMemory {
  std::uint32_t done = 0;  // bit i: step i is over (its camp cleared, by anyone, or missing)
  bool over = false;
  const char* overWhy = "";
  int current = -1;        // the last choice (-1 none; NextRouteStep sets it, for the buff HP's hysteresis)
};

struct RouteChoice {
  int step = -1;            // -1: no step now (the default route)
  const char* why = "";
};

inline bool RouteStepDone(const RouteMemory& m, int i) { return i >= 0 && i < 32 && (m.done >> i & 1u) != 0; }

// The step to walk to now. A step is over once its camp was cleared (spawned and not alive: whoever killed it); the
// first step not over that the Hunter's level and HP allow wins when its camp is up or spawns within waitS; with no
// such step, the earliest spawning one (the match's start: the camps come at 90 s); else none (the default route
// meanwhile, the route goes on later). The route ends at endLevel, at endS or with every step over.
// A buff or pig step's HP floor: buffHpMin, lower for the step he is on, higher while he is on another step not over.
inline float RouteBuffHpMin(const RouteMemory& m, int i, const RouteConfig& cfg) {
  if (m.current == i) return cfg.buffHpMin - cfg.buffHpHyst;
  if (m.current >= 0 && !RouteStepDone(m, m.current)) return cfg.buffHpMin + cfg.buffHpHyst;
  return cfg.buffHpMin;
}

inline RouteChoice NextRouteStepPick(const std::vector<RouteStep>& steps, const std::vector<RouteCamp>& camps,
                                     RouteMemory* m, int level, float hpFrac, double now, const RouteConfig& cfg) {
  if (m->over) return {-1, m->overWhy};
  if (level >= cfg.endLevel) {
    m->over = true;
    m->overWhy = "the end level";
    return {-1, m->overWhy};
  }
  if (now >= cfg.endS) {
    m->over = true;
    m->overWhy = "the end time";
    return {-1, m->overWhy};
  }
  const int n = static_cast<int>(steps.size() < camps.size() ? steps.size() : camps.size());
  bool anyDone = false;
  for (int i = 0; i < n && i < 32; ++i) {
    const RouteCamp& c = camps[static_cast<std::size_t>(i)];
    if (!RouteStepDone(*m, i) && (!c.found || (!c.alive && c.spawns > 0))) m->done |= 1u << i;
    if (RouteStepDone(*m, i)) anyDone = true;
  }
  bool pending = false;
  int waitBest = -1;
  double waitDue = 0.0;
  for (int i = 0; i < n && i < 32; ++i) {
    if (RouteStepDone(*m, i)) continue;
    pending = true;
    const RouteStep& s = steps[static_cast<std::size_t>(i)];
    const RouteCamp& c = camps[static_cast<std::size_t>(i)];
    if (level < s.minLevel) continue;
    if (s.kind != kRouteSmall && anyDone && hpFrac < RouteBuffHpMin(*m, i, cfg)) continue;
    if (c.alive) return {i, "up"};
    if (c.dueAt >= 0.0 && c.dueAt - now <= cfg.waitS) return {i, "spawns soon"};
    if (c.dueAt >= 0.0 && (waitBest < 0 || c.dueAt < waitDue)) {
      waitBest = i;
      waitDue = c.dueAt;
    }
  }
  if (!pending) {
    m->over = true;
    m->overWhy = "every step over";
    return {-1, m->overWhy};
  }
  bool anyUp = false;
  for (int i = 0; i < n; ++i)
    if (!RouteStepDone(*m, i) && camps[static_cast<std::size_t>(i)].alive) anyUp = true;
  if (waitBest >= 0 && !anyUp) return {waitBest, "waits for its spawn"};
  return {-1, "nothing for his level or HP now"};
}

// The step to walk to now (above); remembers it as the current one.
inline RouteChoice NextRouteStep(const std::vector<RouteStep>& steps, const std::vector<RouteCamp>& camps,
                                 RouteMemory* m, int level, float hpFrac, double now, const RouteConfig& cfg) {
  const RouteChoice r = NextRouteStepPick(steps, camps, m, level, hpFrac, now, cfg);
  m->current = r.step;
  return r;
}

// ---- Vanquish (spell1 of every Hunter profile: 300 + 30 x level true damage, range 17.65, cooldown 75 s) ----------
// The data's gambit (ClearCamp's condition_Bot_VanquishTarget) casts it only at a buff camp's or the pigs' major (NPC
// type "objective medium") within 20 whose HP it kills: our Moyas cast it 5-7 times a match. The host's rule adds the
// camps' mediums: any jungle creature within reach whose HP it kills and whose max HP is at least minMaxHp (no fresh
// minor), or any such creature while the Hunter is below lowHp (it ends the fight sooner) [assumed]. The objective types
// first, then the most HP.
struct VanquishConfig {
  float reach = 17.f;       // inside the 17.65 range
  float minMaxHp = 600.f;   // the mediums (950-1200) and majors; the minors (200-350) only at low HP
  float lowHp = 0.35f;
};

struct VanquishCandidate {
  float hp = 0.f, maxHp = 0.f, dist = 0.f;
  bool objective = false;  // NPC type 6-8 (the buff and pig majors)
};

inline float VanquishDamage(int level) { return 300.f + 30.f * static_cast<float>(level > 0 ? level : 1); }

inline int VanquishPick(const std::vector<VanquishCandidate>& c, int level, float hunterHpFrac,
                        const VanquishConfig& cfg) {
  const float dmg = VanquishDamage(level);
  int best = -1;
  for (int i = 0; i < static_cast<int>(c.size()); ++i) {
    const VanquishCandidate& v = c[static_cast<std::size_t>(i)];
    if (!(v.hp > 0.f) || v.hp > dmg || v.dist > cfg.reach) continue;
    if (v.maxHp < cfg.minMaxHp && hunterHpFrac >= cfg.lowHp) continue;
    if (best < 0) {
      best = i;
      continue;
    }
    const VanquishCandidate& b = c[static_cast<std::size_t>(best)];
    if (v.objective != b.objective ? v.objective : v.hp > b.hp) best = i;
  }
  return best;
}

// ---- The pull at the first buff (guide 72: "ask for a strong pull") ------------------------------------------------
// A laner of the Hunter's team hits the first buff camp's biggest creature for a few seconds while the Hunter is there,
// so the camp takes damage and hits the laner, then goes back to its lane before the kill (the XP is shared within
// 15.8 of it) [assumed: every value]. For the kits that can't clear a buff alone at level 1 (Faris) by default.
struct PullConfig {
  float reach = 100.f;       // the laner at most this far from the camp when the pull opens
  float hunterNear = 20.f;   // the Hunter within this of the camp's marker
  double windowS = 10.0;     // the laner hits for at most this long
  double openS = 30.0;       // the pull opens within this after the camp's first spawn
  float stopFrac = 0.6f;     // ... and ends once the camp is at or below this share of its full HP
  float lanerHpMin = 0.7f;   // a laner below this HP share isn't asked, and leaves
};

// Whether the pull (`started` at `startedAt`, or not yet) goes on now.
inline bool PullGoesOn(double now, double spawnAt, bool started, double startedAt, float campFrac, float lanerHpFrac,
                       float hunterDist, const PullConfig& cfg) {
  if (spawnAt < 0.0 || now < spawnAt || lanerHpFrac < cfg.lanerHpMin || campFrac <= cfg.stopFrac) return false;
  if (!started) return now - spawnAt <= cfg.openS && hunterDist <= cfg.hunterNear;
  return now - startedAt <= cfg.windowS;
}

// The kits that want the pull at bot_hunter_pull 1 (2: every Hunter, 0: none).
inline bool RouteKitWantsPull(const char* noun, int mode) {
  if (mode <= 0) return false;
  if (mode >= 2) return true;
  return noun && (std::strcmp(noun, "golem.noun") == 0 || std::strcmp(noun, "golem.Noun") == 0);
}

// The level times a stats line reports (2, 4, 6).
struct LevelTimes {
  double at[3] = {-1.0, -1.0, -1.0};
  int last = 0;
  // A new level seen at `now`: the first time each of 2, 4, 6 is reached.
  void Note(int level, double now) {
    if (level <= last) return;
    last = level;
    const int marks[3] = {2, 4, 6};
    for (int i = 0; i < 3; ++i)
      if (level >= marks[i] && at[i] < 0.0) at[i] = now;
  }
};

}  // namespace gms::bots
