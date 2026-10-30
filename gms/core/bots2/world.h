// gms/core/bots2 (BOTS2): what the second bot brain sees. The host fills one World per think from the frame's objects
// (gms/engine/bots2.cpp); the decision layer (brain.h) reads nothing else. Header-only, no game addresses.
#pragma once

#include <cmath>
#include <cstdint>
#include <vector>

#include "core/bots/lanes.h"  // LanePath, ComputeFront, Vec3
#include "core/bots2/data.h"  // Spell

namespace gms::bots2 {

using Vec3 = gms::bots::Vec3;

inline float Dist(const Vec3& a, const Vec3& b) {
  const float dx = a.x - b.x, dy = a.y - b.y;
  return std::sqrt(dx * dx + dy * dy);
}
inline Vec3 V(float x, float y, float z = 0.f) { return gms::bots::MakeVec(x, y, z); }
// The point `step` units from `from` toward `to` (2-D; z of `from`); `from` itself when they coincide.
inline Vec3 Toward(const Vec3& from, const Vec3& to, float step) {
  const float d = Dist(from, to);
  if (!(d > 0.f)) return from;
  return V(from.x + (to.x - from.x) * step / d, from.y + (to.y - from.y) * step / d, from.z);
}

// nNPCType values the snapshot sorts by (GlobalDefinitions.lua): lane minions 0-2, creeps 3, buff camps 6-7, the
// parasite 8, Bindings 9, Guardians 13, Cores (nubs) 14, the Locus 15.
enum NpcType : int {
  kNpcMelee = 0, kNpcRanged = 1, kNpcSuper = 2, kNpcCreep = 3, kNpcObjectiveMinor = 6, kNpcObjectiveMedium = 7,
  kNpcParasite = 8, kNpcBinding = 9, kNpcGoldMine = 10, kNpcWorker = 12, kNpcGuardian = 13, kNpcCore = 14,
  kNpcLocus = 15, kNpcWard = 16
};

enum class Kind : int { kBinding, kCore, kGuardian, kLocus };

// The roles of RolesTuning (core/bots/composition.h): the Hunter's (the jungle) and, since BOTS2c, the Predator's (the
// team's well taker: its double worker gold).
constexpr int kRoleGladiator = 0;  // BOTS2c: the lane's carry (the last hits: nearer its minions)
constexpr int kRoleHunter = 2;
constexpr int kRolePredator = 3;

struct Shaper {
  std::uint32_t id = 0;
  int team = 0;
  Vec3 pos;
  float hp = 0.f, maxHp = 1.f;
  int level = 1;
  bool alive = false;
  bool seenBy[3] = {false, false, false};  // [team]: that team sees it (its own team always does)
  // a bot's own data (the host fills them for the bots it drives)
  bool bot = false;
  int lane = 0;          // nLaneID of its lane (1 Top, 2 Bottom; 0 none)
  int role = -1;         // RolesTuning index
  float gold = 0.f;      // available gold
  bool channelling = false;  // its recall channel runs

  // BOTS2c: its inventory's battle spells (inventory 6-8 = gambit slots 7-9) and Vitality Potions (inventory 9-10)
  Spell spells[3] = {Spell::kNone, Spell::kNone, Spell::kNone};
  int potions = 0;      // potions carried
  int potionSlot = -1;  // the gambit slot of a stack (10 or 11; -1: none)
  int nextItemPrice = -1;  // what the next step of its build costs (its shop plan; 0: nothing to buy; -1: unknown)
  float Frac() const { return maxHp > 0.f ? hp / maxHp : 0.f; }
};

struct Unit {  // a lane minion or a jungle creature
  std::uint32_t id = 0;
  int team = 0;  // 0 for the jungle's creatures
  int npc = 0;
  Vec3 pos;
  float hp = 0.f, maxHp = 1.f;
};

struct Structure {
  std::uint32_t id = 0;
  int team = 0;
  Kind kind = Kind::kBinding;
  Vec3 pos;
  float hp = 0.f, maxHp = 1.f;
  float reach = 0.f;          // how far it shoots (0: it doesn't)
  std::uint32_t target = 0;   // what it shoots now
  bool shielded = false;      // a Guardian while a Core of its team lives (it can't be hit)
  bool Shoots() const { return reach > 0.f; }
};

// BOTS2c: a spirit well, as the host's well rules (gms/core/wells) hold it: only a well a team owns can be captured, by
// the other team's shapers standing in its ring (an owner's shaper there contests it, an enemy player's hit pauses a
// capturer), and only while it isn't locked (the first 15 minutes, 4 after each capture).
struct Well {
  std::uint32_t id = 0;
  Vec3 pos;
  int owner = 0;            // 1, 2 (0: nobody: not capturable)
  bool locked = true;
  int workers = 0;
  int capturers = 0;        // the capturing team's shapers in the ring in the last tick
  bool moving = false;      // the capture moved in the last tick ...
  double toCapture = -1.0;  // ... and completes in this many seconds (-1: not moving)
  bool workerHit = false;   // an enemy player's creature or pet hit one of its workers in the last 5 s
  bool reachable = true;    // a shaper can walk there (MAPMINES: MM's island mines can't be reached)
  float radius = 12.f;      // the capture ring
};

struct TeamBase {
  bool ok = false;
  Vec3 spawn;  // the start point: the shop and the fountain
  bool hasLocus = false;
  Vec3 locus;
  bool hasGuardian = false;
  Vec3 guardian;
};

struct World {
  double now = 0.0;  // match seconds
  std::vector<Shaper> shapers;
  std::vector<Unit> minions;  // living lane minions (teams 1 and 2)
  std::vector<Unit> creeps;   // living jungle creatures (not the parasite)
  std::vector<Structure> structures;  // living structures
  std::vector<gms::bots::LanePath> lanes;  // from team 1's base to team 2's
  TeamBase base[3];  // [1], [2]

  // BOTS2c
  std::vector<Well> wells;
  std::vector<Unit> workers;     // living well workers (team: their well's owner)
  std::vector<Unit> wards;       // living wards (team: their placer's)
  std::vector<Vec3> wardSpots;   // where wards go: the level's own spots (MAPED4b), else the data's (DataWardSpots)
  bool hasParasite = false;      // the parasite lives ...
  bool parasiteHittable = false; // ... and can be hit now (born, not transforming)
  Unit parasite;

  const Shaper* Find(std::uint32_t id) const {
    for (const Shaper& s : shapers)
      if (s.id == id) return &s;
    return nullptr;
  }
  const Structure* FindStructure(std::uint32_t id) const {
    for (const Structure& s : structures)
      if (s.id == id) return &s;
    return nullptr;
  }
  const Unit* FindUnit(std::uint32_t id) const {
    for (const Unit& u : minions)
      if (u.id == id) return &u;
    for (const Unit& u : creeps)
      if (u.id == id) return &u;
    for (const Unit& u : workers)
      if (u.id == id) return &u;
    if (hasParasite && parasite.id == id) return &parasite;
    return nullptr;
  }
  const Well* FindWell(std::uint32_t id) const {
    for (const Well& w : wells)
      if (w.id == id) return &w;
    return nullptr;
  }
  const gms::bots::LanePath* Lane(int lane) const {
    for (const auto& l : lanes)
      if (l.lane == lane) return &l;
    return lanes.empty() ? nullptr : &lanes.front();
  }
};

inline int Enemy(int team) { return team == 1 ? 2 : 1; }

}  // namespace gms::bots2
