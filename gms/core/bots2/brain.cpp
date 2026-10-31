// gms/core/bots2 (BOTS2): the decision layer (brain.h has the states and their order).
#include "core/bots2/brain.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>

#include "core/bots/jungle.h"  // CampRouteCost
#include "core/bots/route.h"   // VanquishPick (HUNTERROUTE's Vanquish rule)

namespace gms::bots2 {

namespace {

constexpr float kHuge = 1.0e30f;

Intent MoveTo(const Vec3& p, const char* why) {
  Intent in;
  in.act = Act::kMove;
  in.point = p;
  in.why = why;
  return in;
}
Intent AttackIt(std::uint32_t target, bool abilities, bool ult, const char* why) {
  Intent in;
  in.act = Act::kAttack;
  in.target = target;
  in.abilities = abilities;
  in.ult = ult;
  in.why = why;
  return in;
}
Intent Plain(Act a, const char* why) {
  Intent in;
  in.act = a;
  in.why = why;
  return in;
}

bool Seen(const Shaper& s, int team) { return s.alive && team >= 1 && team <= 2 && s.seenBy[team]; }

// The nearest living enemy shaper the bot's team sees, and its distance (kHuge: none).
const Shaper* NearestEnemy(const World& w, const Shaper& me, float* dist) {
  const Shaper* best = nullptr;
  float bd = kHuge;
  for (const Shaper& s : w.shapers) {
    if (s.team == me.team || !Seen(s, me.team)) continue;
    const float d = Dist(s.pos, me.pos);
    if (d < bd) {
      bd = d;
      best = &s;
    }
  }
  *dist = bd;
  return best;
}

int AlliesNear(const World& w, const Shaper& me, float range) {
  int n = 0;
  for (const Shaper& s : w.shapers)
    if (s.team == me.team && s.alive && Dist(s.pos, me.pos) <= range) ++n;  // the bot itself counts
  return n;
}

int OwnMinionsWithin(const World& w, int team, const Vec3& p, float range) {
  int n = 0;
  for (const Unit& u : w.minions)
    if (u.team == team && Dist(u.pos, p) <= range) ++n;
  return n;
}

// A unit (or a point) the bot shouldn't step into: an enemy structure that would shoot it there, or the enemy Locus.
bool Unsafe(const World& w, const Shaper& me, const Vec3& p, const Tuning& t) {
  const TeamBase& eb = w.base[Enemy(me.team)];
  if (eb.hasLocus && Dist(p, eb.locus) < t.locusAvoid) return true;
  const Structure* s = EnemyStructureCovering(w, me.team, p, t.towerMargin);
  return s && !StructureSafe(w, me, *s, t);
}

gms::bots::Front OwnFront(const World& w, const gms::bots::LanePath& lane, int team, const Tuning& t) {
  std::vector<gms::bots::Unit> units;
  units.reserve(w.minions.size());
  for (const Unit& u : w.minions) {
    gms::bots::Unit v;
    v.team = u.team;
    v.pos = u.pos;
    units.push_back(v);
  }
  gms::bots::FrontConfig cfg;
  cfg.laneWidth = t.laneWidth;
  return gms::bots::ComputeFront(lane, team, units, cfg);
}

// The point `progress` units along the lane from `team`'s own base.
Vec3 LanePoint(const gms::bots::LanePath& lane, int team, float progress) {
  const float len = lane.Length();
  progress = std::min(len, std::max(0.f, progress));
  return lane.PointAt(team == 1 ? progress : len - progress);
}

// Home for a bot: its spawn, else (no start point known) its end of the lane, else the start of the first lane.
Vec3 HomeOf(const World& w, const Shaper& me) {
  const TeamBase& b = w.base[me.team == 2 ? 2 : 1];
  if (b.ok) return b.spawn;
  const gms::bots::LanePath* lane = w.Lane(me.lane);
  if (lane && !lane->knots.empty()) return LanePoint(*lane, me.team, 0.f);
  return me.pos;
}

// A structure whose attack is a threat to the base: an own Core or the own Guardian. (The first v2 match also counted
// a lane's last Binding; defending those pulled bots across the map and no push converted.)
bool IsKeyStructure(const Structure& s, int team) {
  return s.team == team && (s.kind == Kind::kCore || s.kind == Kind::kGuardian);
}

int LivingShapers(const World& w, int team) {
  int n = 0;
  for (const Shaper& s : w.shapers)
    if (s.team == team && s.alive) ++n;
  return n;
}

// BOTS2c: the enemy shapers (of `team`) its team sees within `r` of `at`, and `team`'s living shapers there.
int EnemiesNear(const World& w, int team, const Vec3& at, float r) {
  int n = 0;
  for (const Shaper& s : w.shapers)
    if (s.team != team && Seen(s, team) && Dist(s.pos, at) <= r) ++n;
  return n;
}
int AlliesAt(const World& w, int team, const Vec3& at, float r) {
  int n = 0;
  for (const Shaper& s : w.shapers)
    if (s.team == team && s.alive && Dist(s.pos, at) <= r) ++n;
  return n;
}

bool Has(const std::vector<std::uint32_t>& v, std::uint32_t id) { return std::find(v.begin(), v.end(), id) != v.end(); }

// The distance of `c` from the segment a-b (2-D).
float SegDist(const Vec3& a, const Vec3& b, const Vec3& c) {
  const float dx = b.x - a.x, dy = b.y - a.y;
  const float len2 = dx * dx + dy * dy;
  float u = len2 > 0.f ? ((c.x - a.x) * dx + (c.y - a.y) * dy) / len2 : 0.f;
  u = std::max(0.f, std::min(1.f, u));
  return Dist(V(a.x + u * dx, a.y + u * dy), c);
}

// BOTSFLIP: where an attack's target stands (a shaper, a unit, a structure); false if unknown.
bool TargetPos(const World& w, std::uint32_t id, Vec3* out) {
  if (const Shaper* s = w.Find(id)) {
    *out = s->pos;
    return true;
  }
  if (const Unit* u = w.FindUnit(id)) {
    *out = u->pos;
    return true;
  }
  if (const Structure* st = w.FindStructure(id)) {
    *out = st->pos;
    return true;
  }
  return false;
}

// BOTS2c: the lane's minions for `team`: how many of each side, the own minions' front and the enemy's (progress from
// the team's base; core/bots/lanes.h ComputeFront's rule without its midpoint).
struct LaneMinionFronts {
  int own = 0, enemy = 0;
  float ownMax = -1.f, enemyMin = kHuge;
};
LaneMinionFronts LaneMinions(const World& w, const gms::bots::LanePath& lane, int team, float width) {
  LaneMinionFronts f;
  const float len = lane.Length();
  for (const Unit& u : w.minions) {
    float d = 0.f;
    const float s = lane.Project(u.pos, &d);
    if (d > width) continue;
    const float p = team == 1 ? s : len - s;
    if (u.team == team) {
      ++f.own;
      f.ownMax = std::max(f.ownMax, p);
    } else {
      ++f.enemy;
      f.enemyMin = std::min(f.enemyMin, p);
    }
  }
  return f;
}

// BOTS2c: the progress (from the team's base) of its outermost living Binding on `lane`; -1 without one.
float OwnTowerProgress(const World& w, const gms::bots::LanePath& lane, int team) {
  const float len = lane.Length();
  float best = -1.f;
  for (const Structure& s : w.structures) {
    if (s.team != team || s.kind != Kind::kBinding || gms::bots::ClosestLane(w.lanes, s.pos) != lane.lane) continue;
    const float arc = lane.Project(s.pos);
    best = std::max(best, team == 1 ? arc : len - arc);
  }
  return best;
}

// The lane's direction at progress `p` for `team` (toward the enemy base).
Vec3 LaneDir(const gms::bots::LanePath& lane, int team, float p) {
  const float len = lane.Length();
  p = std::min(len, std::max(0.f, p));
  const Vec3 d = lane.DirectionAt(team == 1 ? p : len - p);
  return team == 1 ? d : V(-d.x, -d.y);
}

// Where a capturer or defender stands at a well: wellStand from its centre toward the bot's base (inside the ring).
Vec3 WellStand(const World& w, const Shaper& me, const Well& wl, const Tuning& t) {
  return Toward(wl.pos, HomeOf(w, me), t.wellStand);
}

}  // namespace

const char* StateName(State s) {
  switch (s) {
    case State::kDead: return "dead";
    case State::kRetreat: return "retreat";
    case State::kRecall: return "recall";
    case State::kBase: return "base";
    case State::kFight: return "fight";
    case State::kDefend: return "defend";
    case State::kWell: return "well";
    case State::kParasite: return "parasite";
    case State::kJungle: return "jungle";
    case State::kLane: return "lane";
    case State::kPush: return "push";
    default: return "?";
  }
}

const char* ActName(Act a) {
  switch (a) {
    case Act::kNone: return "none";
    case Act::kMove: return "move";
    case Act::kAttack: return "attack";
    case Act::kRecall: return "recall";
    case Act::kShop: return "shop";
    default: return "?";
  }
}

const char* ExtraName(ExtraKind k) {
  switch (k) {
    case ExtraKind::kNone: return "none";
    case ExtraKind::kSpell: return "spell";
    case ExtraKind::kPotion: return "potion";
    case ExtraKind::kWard: return "ward";
    default: return "?";
  }
}

bool OwnSide(const World& w, int team, const Vec3& p) {
  const TeamBase& own = w.base[team == 2 ? 2 : 1];
  const TeamBase& other = w.base[Enemy(team)];
  return own.ok && other.ok && Dist(p, own.spawn) < Dist(p, other.spawn);
}

bool InWellAction(const World& w, const Shaper& s) {
  if (!s.alive) return false;
  for (const Well& wl : w.wells) {
    if (wl.locked || Dist(s.pos, wl.pos) > wl.radius) continue;
    if (wl.owner == Enemy(s.team)) return true;                // capturing
    if (wl.owner == s.team && wl.capturers > 0) return true;   // contesting
  }
  return false;
}

LaneSlot LaneSlotOf(const World& w, const Shaper& me, const Tuning& t) {
  auto order = [](const Shaper& s) { return s.role == kRoleGladiator ? 0 : 1; };
  int rank = 0;
  for (const Shaper& s : w.shapers) {
    if (s.id == me.id || s.team != me.team || !s.alive || !s.bot || s.lane != me.lane) continue;
    if (order(s) < order(me) || (order(s) == order(me) && s.id < me.id)) ++rank;
  }
  LaneSlot slot;
  slot.rank = rank;
  slot.side = rank % 2 == 0 ? 1.f : -1.f;
  slot.back = t.laneCarryBack + (t.laneSupportBack - t.laneCarryBack) * static_cast<float>(rank);
  return slot;
}

float WalkSideOf(std::uint32_t id) {
  std::uint32_t h = id * 2654435761u;  // Knuth's multiplicative hash: neighbouring ids get either side
  return (h >> 16) & 1u ? 1.f : -1.f;
}

Vec3 Beside(const Vec3& p, const Vec3& dir, float side, float by) {
  const float n = std::sqrt(dir.x * dir.x + dir.y * dir.y);
  if (!(n > 0.f)) return p;
  const Vec3 q = V(p.x - dir.y / n * side * by, p.y + dir.x / n * side * by, p.z);
  return q.x == 0.f && q.y == 0.f ? p : q;
}

std::uint32_t WellTaker(const World& w, int team) {
  std::uint32_t hunter = 0;
  for (const Shaper& s : w.shapers) {
    if (s.team != team || !s.alive || !s.bot) continue;
    if (s.role == kRolePredator) return s.id;
    if (s.role == kRoleHunter && !hunter) hunter = s.id;
  }
  return hunter;
}

bool InBase(const World& w, const Shaper& s, const Tuning& t) {
  const TeamBase& b = w.base[s.team == 2 ? 2 : 1];
  return b.ok && Dist(s.pos, b.spawn) <= t.baseRadius;
}

float LevelWeight(int level) { return 1.f + 0.1f * static_cast<float>(std::max(1, level) - 1); }

// The odds: the own side (the bot's living team mates within fightCountRange of it, the bot included) against the
// enemy side (the enemy shapers its team sees within fightCountRange of the target, or of the bot without a target),
// each shaper weighed by its HP fraction and level (LevelWeight); a target at or below diveHp weighs lowTargetBonus
// less; a structure that shoots counts towerOdds for its side when its reach covers the fight (the target's spot for
// the own structures, the bot's spot for the enemy's, unless own minions or the group tank it: StructureSafe).
float FightOdds(const World& w, const Shaper& me, const Shaper* target, const Tuning& t) {
  const Vec3 there = target ? target->pos : me.pos;
  float ours = 0.f, theirs = 0.f;
  for (const Shaper& s : w.shapers) {
    if (!s.alive) continue;
    const float weight = s.Frac() * LevelWeight(s.level);
    if (s.team == me.team) {
      if (Dist(s.pos, me.pos) <= t.fightCountRange) ours += weight;
    } else if (Seen(s, me.team) && Dist(s.pos, there) <= t.fightCountRange) {
      float wgt = weight;
      if (target && s.id == target->id && s.Frac() <= t.diveHp) wgt = std::max(0.f, wgt - t.lowTargetBonus);
      theirs += wgt;
    }
  }
  for (const Structure& s : w.structures) {
    if (!s.Shoots() || s.kind == Kind::kLocus) continue;
    if (s.team == me.team && Dist(s.pos, there) <= s.reach + t.towerMargin) ours += t.towerOdds;
    if (s.team != me.team && Dist(s.pos, me.pos) <= s.reach + t.towerMargin && !StructureSafe(w, me, s, t))
      theirs += t.towerOdds;
  }
  if (theirs <= 0.f) return 1000.f;
  return ours / theirs;
}

const Structure* EnemyStructureCovering(const World& w, int team, const Vec3& p, float margin) {
  for (const Structure& s : w.structures)
    if (s.team != team && s.Shoots() && Dist(s.pos, p) <= s.reach + margin) return &s;
  return nullptr;
}

bool StructureSafe(const World& w, const Shaper& me, const Structure& s, const Tuning& t) {
  if (s.kind == Kind::kLocus) return false;
  if (!s.Shoots()) return true;
  if (s.target == me.id) return false;
  if (OwnMinionsWithin(w, me.team, s.pos, s.reach) >= t.towerMinMinions) return true;
  return AlliesNear(w, me, t.siegeAllyRange) >= t.siegeAllies && me.Frac() >= t.diveSelfHp;
}

std::vector<Camp> Camps(const World& w, float join) {
  std::vector<Camp> camps;
  for (const Unit& u : w.creeps) {
    Camp* home = nullptr;
    for (Camp& c : camps)
      if (Dist(c.center, u.pos) <= join) {
        home = &c;
        break;
      }
    if (!home) {
      camps.push_back(Camp());
      home = &camps.back();
      home->center = u.pos;
    }
    home->members.push_back(u.id);
    // the centre: the members' mean
    float x = 0.f, y = 0.f, z = 0.f;
    int n = 0;
    for (std::uint32_t id : home->members)
      for (const Unit& v : w.creeps)
        if (v.id == id) {
          x += v.pos.x;
          y += v.pos.y;
          z += v.pos.z;
          ++n;
        }
    if (n) home->center = V(x / n, y / n, z / n);
  }
  return camps;
}

Vec3 AwayFromLocus(const World& w, int team, const Vec3& p, const Tuning& t) {
  const TeamBase& eb = w.base[Enemy(team)];
  if (!eb.hasLocus) return p;
  const float d = Dist(p, eb.locus);
  if (d >= t.locusAvoid) return p;
  if (!(d > 0.01f)) {  // on the Locus: toward the map's centre
    const float n = std::sqrt(eb.locus.x * eb.locus.x + eb.locus.y * eb.locus.y);
    if (!(n > 0.f)) return p;
    return V(eb.locus.x - eb.locus.x / n * t.locusAvoid, eb.locus.y - eb.locus.y / n * t.locusAvoid, p.z);
  }
  return V(eb.locus.x + (p.x - eb.locus.x) * t.locusAvoid / d, eb.locus.y + (p.y - eb.locus.y) * t.locusAvoid / d, p.z);
}

// ---- the team plan --------------------------------------------------------------------------------------------------

void Brain::PlanTeams(const World& w) {
  for (int team = 1; team <= 2; ++team) {
    if (!teams_[team]) continue;  // BOTS2c: the other brain's team
    TeamPlan& p = plans_[team];
    const int enemy = Enemy(team);
    // the push: from groupAtS on, or earlier while the team has advantageLead more living shapers than the enemy
    const int own = LivingShapers(w, team), theirs = LivingShapers(w, enemy);
    const bool advantage = own >= 2 && own >= theirs + t_.advantageLead;
    if (advantage && !p.advantage && w.now < t_.groupAtS) ++stats_.advantages;
    p.advantage = advantage;
    p.push = w.now >= t_.groupAtS || advantage;
    // the push lane: the lane with the fewest living enemy Bindings (then the own front farthest out), held for a while;
    // round 8: held while the objective is a Core or the Guardian (round 7's `_9d` switched lanes at the enemy base, so
    // the nearest Core and the group changed with it)
    const Structure* current = p.objective ? w.FindStructure(p.objective) : nullptr;
    const bool atBase = current && (current->kind == Kind::kCore || current->kind == Kind::kGuardian);
    if (p.push && !w.lanes.empty() && (p.pushLane == 0 || (!atBase && w.now - p.pushLaneSince >= t_.pushLaneHoldS))) {
      int best = 0;
      int bestTowers = 1 << 20;
      float bestProgress = -1.f;
      for (const auto& lane : w.lanes) {
        int towers = 0;
        for (const Structure& s : w.structures) {
          if (s.team != enemy || s.kind != Kind::kBinding) continue;
          if (gms::bots::ClosestLane(w.lanes, s.pos) == lane.lane) ++towers;
        }
        const float progress = OwnFront(w, lane, team, t_).progress;
        if (towers < bestTowers || (towers == bestTowers && progress > bestProgress + 1.f)) {
          best = lane.lane;
          bestTowers = towers;
          bestProgress = progress;
        }
      }
      if (best != p.pushLane) {
        if (p.pushLane != 0) ++stats_.pushLaneChanges;
        p.pushLane = best;
      }
      p.pushLaneSince = w.now;
    }
    // BOTS2c: the wells (a defence, the team's well task) before the push's group counts its bots; the parasite after
    // the objective (not while the enemy base is open)
    PlanWells(w, team, p);
    if (p.push) PlanObjective(w, team, p);
    else {
      p.objective = 0;
      p.grouped = false;
      p.siege = false;
      p.pushGroup.clear();
    }
    PlanGuardian(w, team, p);  // round 11: before the parasite (which waits during it)
    PlanParasite(w, team, p);
    // the threat: an enemy shaper (seen) or minion near an own Core or the Guardian; the one nearest the own Guardian
    // (or spawn) counts
    const TeamBase& b = w.base[team];
    const Vec3 home = b.hasGuardian ? b.guardian : b.spawn;
    bool found = false;
    float bestD = kHuge;
    Vec3 at;
    for (const Structure& s : w.structures) {
      if (!IsKeyStructure(s, team)) continue;
      bool hit = false;
      for (const Shaper& e : w.shapers)
        if (e.team == enemy && Seen(e, team) && Dist(e.pos, s.pos) <= t_.defendRadius) hit = true;
      for (const Unit& u : w.minions)
        if (u.team == enemy && Dist(u.pos, s.pos) <= t_.defendRadius) hit = true;
      if (!hit) continue;
      const float d = Dist(s.pos, home);
      if (d < bestD) {
        bestD = d;
        at = s.pos;
        found = true;
      }
    }
    if (found) {
      if (!p.threatened) ++stats_.threats;
      p.threatened = true;
      p.threatSeenAt = w.now;
      p.threat = at;
    } else if (p.threatened && w.now - p.threatSeenAt > t_.threatHoldS) {
      p.threatened = false;
      p.defenders.clear();
    }
    if (!p.threatened) continue;
    // the defenders: bots within defendReach of the threat (all of them when it is the Guardian itself); those already
    // defending stay while alive and not retreating, the nearest others fill up to `defenders`
    const bool guardian = b.hasGuardian && Dist(p.threat, b.guardian) <= 1.f;
    const int wanted = guardian ? 5 : t_.defenders;
    auto eligible = [&](const Shaper& s) {
      if (s.team != team || !s.alive || !s.bot || Dist(s.pos, p.threat) > t_.defendReach) return false;
      const auto mi = mem_.find(s.id);
      return !(mi != mem_.end() && mi->second.retreating);
    };
    std::vector<std::uint32_t> keep;
    for (std::uint32_t id : p.defenders) {
      const Shaper* s = w.Find(id);
      if (s && eligible(*s)) keep.push_back(id);
    }
    while (static_cast<int>(keep.size()) < wanted) {
      const Shaper* pick = nullptr;
      float pd = kHuge;
      for (const Shaper& s : w.shapers) {
        if (!eligible(s) || std::find(keep.begin(), keep.end(), s.id) != keep.end()) continue;
        const float d = Dist(s.pos, p.threat);
        if (d < pd) {
          pd = d;
          pick = &s;
        }
      }
      if (!pick) break;
      keep.push_back(pick->id);
    }
    p.defenders = keep;
  }
}

// The push's objective and rally point, and whether the group stands together there.
void Brain::PlanObjective(const World& w, int team, TeamPlan& p) {
  const int enemy = Enemy(team);
  p.siege = false;
  p.siegeEdge = false;
  const gms::bots::LanePath* lane = w.Lane(p.pushLane);
  // the objective: the push lane's enemy Bindings from the own side, then the Cores (the nearest to the lane's enemy
  // end first), then the Guardian once unshielded
  const Structure* obj = nullptr;
  float best = kHuge;
  const float len = lane ? lane->Length() : 0.f;
  const Vec3 laneEnd = lane ? LanePoint(*lane, team, len) : w.base[enemy].spawn;
  // round 9: at the enemy base the Core nearest the team's bots there (else the push lane's end), a damaged one first,
  // the current one kept unless another is clearly nearer (round 8's `_d2`: the plan held one far Core for 20 minutes
  // while the classic brain's bots killed the other four, which then regrew; the Guardian needs all five dead)
  Vec3 anchor = laneEnd;
  {
    float sx = 0.f, sy = 0.f;
    int n = 0;
    for (const Shaper& s : w.shapers)
      if (s.team == team && s.alive && s.bot && Dist(s.pos, w.base[enemy].spawn) <= t_.siegeAnchorRange) {
        sx += s.pos.x;
        sy += s.pos.y;
        ++n;
      }
    if (n > 0) anchor = V(sx / static_cast<float>(n), sy / static_cast<float>(n));
  }
  for (const Structure& s : w.structures) {
    if (s.team != enemy || s.kind == Kind::kLocus || s.shielded) continue;
    float key = kHuge;
    if (s.kind == Kind::kBinding) {
      if (!lane || gms::bots::ClosestLane(w.lanes, s.pos) != lane->lane) continue;
      const float arc = lane->Project(s.pos);
      key = team == 1 ? arc : len - arc;  // its distance from the own base along the lane
    } else if (s.kind == Kind::kCore) {
      const float frac = s.maxHp > 0.f ? s.hp / s.maxHp : 1.f;
      key = 10000.f + Dist(s.pos, anchor) + t_.siegeCoreHpWeight * frac - (s.id == p.objective ? t_.siegeKeep : 0.f);
    } else {
      key = 20000.f;
    }
    if (key < best) {
      best = key;
      obj = &s;
    }
  }
  if (!obj) {
    p.objective = 0;
    p.grouped = false;
    p.pushGroup.clear();
    return;
  }
  if (obj->id != p.objective) {
    ++stats_.objectives;
    p.objective = obj->id;
    p.grouped = false;
  }
  p.objectivePos = obj->pos;
  // the rally point: on the push lane, rallyBack outside the objective's reach toward the own base
  const float back = std::max(obj->reach, 8.f) + t_.rallyBack;
  // BOTS2c round 6: a Core's or the Guardian's rally point stands beside it toward the own base, not at the lane's end
  // (round 5: the lane's end was about 90 from the Core; the group gathered there and never held at the Core)
  const bool base = obj->kind == Kind::kCore || obj->kind == Kind::kGuardian;
  if (lane && !base) {
    const float arc = lane->Project(obj->pos);
    const float progress = (team == 1 ? arc : len - arc) - back;
    p.rally = LanePoint(*lane, team, progress);
  } else {
    p.rally = Toward(obj->pos, w.base[team].spawn, back);
  }
  p.rally = AwayFromLocus(w, team, p.rally, t_);
  // the group: living bots of the team, not retreating and not on a well's duty, at the rally point or at the objective
  int living = 0, count = 0;
  for (const Shaper& s : w.shapers) {
    if (s.team != team || !s.alive || !s.bot || OnTeamDuty(p, s.id)) continue;
    const auto mi = mem_.find(s.id);
    if (mi != mem_.end() && mi->second.retreating) continue;
    ++living;
    // at the rally point, or anywhere between it and the objective (the own front's lane work stands there)
    if (Dist(s.pos, p.rally) <= t_.groupRadius || Dist(s.pos, obj->pos) <= std::max(obj->reach, 8.f) + t_.rallyBack + 10.f)
      ++count;
  }
  const int needed = std::max(2, std::min(p.advantage ? 2 : t_.siegeAllies, living));
  p.groupCount = count;
  p.groupNeeded = needed;
  // round 8: at a Core or the Guardian the group forms only while the team isn't outnumbered, the rule that ends it
  // (round 7's `_9d`: "grouped" flipped on and off every think for minutes at the Cores, 4 near and one shaper short)
  const bool outnumbered = LivingShapers(w, team) < LivingShapers(w, Enemy(team));
  if (!p.grouped && count >= needed && living >= 2 && !(base && outnumbered)) {
    p.grouped = true;
    p.groupedSince = w.now;
    ++stats_.groupedOn;
  } else if (p.grouped && !base && count < needed - 1 && (count == 0 || w.now - p.groupedSince >= t_.groupHoldS)) {
    p.grouped = false;  // two short of the group (or one of two): gather again; not within groupHoldS (round 6's flips)
  } else if (p.grouped && base && (count == 0 || outnumbered)) {
    // round 6: the base's siege holds while one of the group is there and the team isn't outnumbered (round 5: the
    // group flipped between grouped and "0 of 2" within a second, 39-minute matches with the structure lead unfinished)
    p.grouped = false;
  }
  p.siege = p.grouped && base;
  // round 7: the base is hit only with an edge, a numbers advantage or own minions at the objective (round 6's siege
  // under the Guardian's fire without one: v2 died 28-41 times against classic's 13-22)
  // round 10: at the unshielded Guardian, not being outnumbered is the edge (its death ends the match, and waiting for
  // minions there lost the window: round 9's `_d1` left the enemy Guardian unshielded for 336 s with "group 0 of 2",
  // its lowest 3,655 of 5,000 HP, until a Core regrew)
  p.siegeEdge = p.advantage ||
                OwnMinionsWithin(w, team, obj->pos, std::max(obj->reach, 8.f) + t_.rallyBack) >= t_.towerMinMinions ||
                (obj->kind == Kind::kGuardian && !outnumbered);
  // round 8 (the hybrid): the walk to the rally point is safe when the team's own wave on the push lane stands at or
  // past it (the walk stays behind the wave), at the enemy base with the siege's edge
  p.rallySafe = false;
  if (base) {
    p.rallySafe = p.siegeEdge;
  } else if (lane) {
    const float rallyProgress = team == 1 ? lane->Project(p.rally) : len - lane->Project(p.rally);
    const gms::bots::Front f = OwnFront(w, *lane, team, t_);
    p.rallySafe = f.own > 0 && f.progress >= rallyProgress;
  }
  // round 8 (the hybrid's push): the bots the push takes, every healthy one at a Core or the Guardian, else
  // hybridPushBots of them (the others farm with the classic brain: round 7's hybrid fell 8,000 gold behind from the
  // push phase on, every calm bot walking to the rally point): those already in it first, then the nearest to the rally
  // point, a Hunter last (his camps). Only the hybrid reads it; v2 sends every bot.
  std::vector<const Shaper*> healthy;
  for (const Shaper& s : w.shapers)
    if (s.team == team && s.alive && s.bot && !OnTeamDuty(p, s.id) && s.Frac() >= t_.retreatHp) healthy.push_back(&s);
  const size_t want = base ? healthy.size() : std::min(healthy.size(), static_cast<size_t>(std::max(0, t_.hybridPushBots)));
  std::vector<std::uint32_t> group;
  for (std::uint32_t id : p.pushGroup)
    for (const Shaper* s : healthy)
      if (s->id == id && group.size() < want) group.push_back(id);
  while (group.size() < want) {
    const Shaper* pick = nullptr;
    float pd = kHuge;
    for (const Shaper* s : healthy) {
      if (Has(group, s->id)) continue;
      const float d = Dist(s->pos, p.rally) + (s->role == kRoleHunter ? 1000.f : 0.f);
      if (d < pd) {
        pd = d;
        pick = s;
      }
    }
    if (!pick) break;
    group.push_back(pick->id);
  }
  p.pushGroup = group;
}

bool Brain::OnTeamDuty(const TeamPlan& p, std::uint32_t bot) const {
  return bot && (bot == p.wellBot || (p.wellThreat && Has(p.wellDefenders, bot)));
}

// BOTS2c: the wells. (1) An own well under capture (enemy capturers in its ring, a capture moving, or an enemy shaper's
// hit on a worker): its defenders, the nearest bots that get there in time, not into a crowd (WELLDEF's rules). (2) The
// team's well task, one bot at a time: retake an own-side well the enemy holds (the nearest able bot), take an unlocked
// enemy well or raid a locked one's workers (the well taker: the Predator, else the Hunter), each only while no enemy
// shaper is near the well.
void Brain::PlanWells(const World& w, int team, TeamPlan& p) {
  const int enemy = Enemy(team);
  const double now = w.now;
  // round 6: the push's objective is a Core or the Guardian: every bot to the base, no well task
  const Structure* pushObj = p.push && p.objective ? w.FindStructure(p.objective) : nullptr;
  const bool baseOpen = pushObj && (pushObj->kind == Kind::kCore || pushObj->kind == Kind::kGuardian);
  auto memOf = [&](std::uint32_t id) -> Memory* {
    const auto it = mem_.find(id);
    return it == mem_.end() ? nullptr : &it->second;
  };
  auto baseDefender = [&](std::uint32_t id) { return p.threatened && Has(p.defenders, id); };
  // a bot of the grouped push at its objective stays there: a siege is worth more than a well
  auto inSiege = [&](const Shaper& s) {
    if (!p.push || !p.grouped || !p.objective) return false;
    const Structure* o = w.FindStructure(p.objective);
    return Dist(s.pos, p.objectivePos) <= (o ? std::max(o->reach, 8.f) : 8.f) + t_.rallyBack + 10.f;
  };

  // (1) the threat
  const Well* threat = nullptr;
  for (const Well& wl : w.wells) {
    if (wl.owner != team || wl.locked || !wl.reachable) continue;
    if (wl.capturers <= 0 && !wl.moving && !wl.workerHit) continue;
    if (!threat || (wl.moving && (!threat->moving || wl.toCapture < threat->toCapture))) threat = &wl;
  }
  if (threat) {
    if (p.wellThreat != threat->id) {
      ++stats_.wellThreats;
      p.wellDefenders.clear();
    }
    p.wellThreat = threat->id;
    p.wellThreatSeenAt = now;
  } else if (p.wellThreat && now - p.wellThreatSeenAt > t_.threatHoldS) {
    p.wellThreat = 0;
    p.wellDefenders.clear();
  }
  const Well* tw = p.wellThreat ? w.FindWell(p.wellThreat) : nullptr;
  if (p.wellThreat && (!tw || tw->owner != team)) {  // gone or taken meanwhile: retaking it is a task
    p.wellThreat = 0;
    p.wellDefenders.clear();
    tw = nullptr;
  }
  if (tw) {
    const int wanted = tw->capturers >= 2 ? 2 : 1;
    auto eligible = [&](const Shaper& s) {
      if (s.team != team || !s.alive || !s.bot || s.Frac() < t_.wellDefendHp) return false;
      const Memory* m = memOf(s.id);
      if ((m && (m->retreating || m->disengaging)) || baseDefender(s.id) || s.id == p.wellBot || inSiege(s)) return false;
      const float d = Dist(s.pos, tw->pos);
      if (d > t_.wellDefendReach) return false;
      // in time: a moving capture done before the bot arrives is left (it is retaken later)
      return !(tw->moving && tw->toCapture >= 0.0 &&
               std::max(0.f, d - tw->radius) / t_.walkSpeed > tw->toCapture + t_.wellInTimeS);
    };
    std::vector<std::uint32_t> keep;
    for (std::uint32_t id : p.wellDefenders) {
      const Shaper* s = w.Find(id);
      if (s && eligible(*s)) keep.push_back(id);
    }
    // numbers (WELLDEF): not into a crowd: the enemies seen there may exceed the allies there and the defenders by one
    const float there = tw->radius + 18.f;
    const bool worth = EnemiesNear(w, team, tw->pos, there) <= AlliesAt(w, team, tw->pos, there) + wanted + 1;
    while (worth && static_cast<int>(keep.size()) < wanted) {
      const Shaper* pick = nullptr;
      float pd = kHuge;
      for (const Shaper& s : w.shapers) {
        if (!eligible(s) || Has(keep, s.id)) continue;
        const float d = Dist(s.pos, tw->pos);
        if (d < pd) {
          pd = d;
          pick = &s;
        }
      }
      if (!pick) break;
      keep.push_back(pick->id);
    }
    p.wellDefenders = keep;
    // none: why (the team's living bots by the first reason that rules each out)
    p.wellNoDefenderWhy[0] = '\0';
    if (keep.empty()) {
      int hurt = 0, busy = 0, away = 0, late = 0;
      for (const Shaper& s : w.shapers) {
        if (s.team != team || !s.alive || !s.bot) continue;
        const Memory* m = memOf(s.id);
        if (s.Frac() < t_.wellDefendHp || (m && (m->retreating || m->disengaging))) ++hurt;
        else if (baseDefender(s.id) || s.id == p.wellBot || inSiege(s)) ++busy;
        else if (Dist(s.pos, tw->pos) > t_.wellDefendReach) ++away;
        else ++late;
      }
      std::snprintf(p.wellNoDefenderWhy, sizeof p.wellNoDefenderWhy, "%sfar %d, too late %d, busy %d, hurt %d",
                    worth ? "" : "too many enemies there; ", away, late, busy, hurt);
    }
  }

  // (2) the task: ends ...
  if (p.wellBot) {
    const Shaper* s = w.Find(p.wellBot);
    const Well* wl = w.FindWell(p.wellTarget);
    const Memory* m = memOf(p.wellBot);
    const char* why = nullptr;
    if (!s || !s->alive) why = "dead";
    else if (InBase(w, *s, t_)) why = "home";
    else if (m && m->retreating) why = "retreat";
    else if (m && m->disengaging) why = "outnumbered";
    // an enemy structure shot it on the way (round 1: Faris walked into a Binding's reach and out again twice a second
    // for 16 s, the task's walk against the tower escape)
    else if (m && m->escaping) why = "a structure's fire";
    else if (!wl) why = "no well";
    else if (wl->owner != enemy) why = p.wellKind == TeamPlan::kWellRaid ? "the well changed hands" : "taken";
    else if (p.wellKind != TeamPlan::kWellRaid && wl->locked) why = "locked";
    else if (p.wellKind == TeamPlan::kWellRaid && wl->workers < t_.raidEndWorkers) why = "raided";
    else if (now - p.wellSince > t_.wellTaskMaxS) why = "too long";
    else if (baseDefender(p.wellBot)) why = "the base";
    else if (baseOpen) why = "the siege";
    if (why) {
      if (Memory* mm = memOf(p.wellBot)) mm->wellDoneAt = now;
      p.wellEndWhy = why;
      p.wellBot = 0;
      p.wellTarget = 0;
      p.wellKind = TeamPlan::kWellNone;
    }
  }
  // ... and a new one (none while the team takes the parasite or the enemy base: every bot there)
  if (p.wellBot || p.parasite || baseOpen) return;
  // the taker: the Predator (its double worker gold) at any time; the Hunter only once he has left his camps
  std::uint32_t taker = WellTaker(w, team);
  if (const Shaper* ts = taker ? w.Find(taker) : nullptr)
    if (ts->role == kRoleHunter && now < t_.hunterJoinS) taker = 0;
  auto canTask = [&](const Shaper& s) {
    if (s.team != team || !s.alive || !s.bot || s.Frac() < t_.wellHp || InBase(w, s, t_)) return false;
    const Memory* m = memOf(s.id);
    if (m && (m->retreating || m->disengaging || now - m->wellDoneAt < t_.wellRetryS)) return false;
    if (baseDefender(s.id) || (p.wellThreat && Has(p.wellDefenders, s.id)) || inSiege(s)) return false;
    return EnemiesNear(w, team, s.pos, t_.wellSafeRange) == 0;  // not in a fight or about to be
  };
  int bestKind = TeamPlan::kWellNone;
  float bestD = kHuge;
  std::uint32_t bestBot = 0, bestWell = 0;
  auto offer = [&](int kind, const Shaper& s, const Well& wl) {
    const float d = Dist(s.pos, wl.pos);
    // retake before take before raid, then the nearest
    if (bestKind == TeamPlan::kWellNone || kind < bestKind || (kind == bestKind && d < bestD)) {
      bestKind = kind;
      bestD = d;
      bestBot = s.id;
      bestWell = wl.id;
    }
  };
  for (const Well& wl : w.wells) {
    if (wl.owner != enemy || !wl.reachable || EnemiesNear(w, team, wl.pos, t_.wellSafeRange) > 0) continue;
    if (!wl.locked && OwnSide(w, team, wl.pos)) {  // an own-side well the enemy took: the nearest able bot
      for (const Shaper& s : w.shapers)
        if (canTask(s) && Dist(s.pos, wl.pos) <= t_.wellReach && !Unsafe(w, s, WellStand(w, s, wl, t_), t_))
          offer(TeamPlan::kWellRetake, s, wl);
      continue;
    }
    const Shaper* s = taker ? w.Find(taker) : nullptr;
    if (!s || !canTask(*s) || Dist(s->pos, wl.pos) > t_.wellTakerReach) continue;
    if (Unsafe(w, *s, WellStand(w, *s, wl, t_), t_)) continue;  // an enemy structure covers its spot
    if (!wl.locked) offer(TeamPlan::kWellTake, *s, wl);
    else if (wl.workers >= t_.raidMinWorkers) offer(TeamPlan::kWellRaid, *s, wl);
  }
  if (bestKind == TeamPlan::kWellNone) return;
  p.wellBot = bestBot;
  p.wellTarget = bestWell;
  p.wellKind = bestKind;
  p.wellSince = now;
  ++stats_.wellTasks[bestKind];
}

// BOTS2c: the parasite as a team objective (the user: "a possibility after min 15 after a won fight"): from
// parasiteFromS, while the team has its advantage (advantageLead more living shapers: a won fight) and the enemy base
// isn't open yet (a won fight there ends the match); the group of parasiteAllies bots gathers outside its reach and hits
// it together, never alone. It ends when the parasite is down, the team is outnumbered, fewer than two bots are left or
// after parasiteMaxS.
void Brain::PlanParasite(const World& w, int team, TeamPlan& p) {
  const double now = w.now;
  const int own = LivingShapers(w, team), theirs = LivingShapers(w, Enemy(team));
  auto able = [&](const Shaper& s) {
    if (s.team != team || !s.alive || !s.bot || OnTeamDuty(p, s.id) || (p.threatened && Has(p.defenders, s.id)))
      return false;
    const auto it = mem_.find(s.id);
    return !(it != mem_.end() && it->second.retreating);
  };
  int living = 0;
  for (const Shaper& s : w.shapers)
    if (able(s)) ++living;
  if (p.parasite) {
    const char* why = nullptr;
    if (!w.hasParasite) why = "down";
    else if (own < theirs) why = "outnumbered";
    else if (living < 2) why = "too few";
    else if (now - p.parasiteSince > t_.parasiteMaxS) why = "too long";
    if (why) {
      if (!w.hasParasite) ++stats_.parasiteDowns;
      else ++stats_.parasiteGiveUps;
      p.parasite = false;
      p.parasiteGrouped = false;
      p.parasiteEndedAt = now;
      p.parasiteEndWhy = why;
      return;
    }
    p.parasiteId = w.parasite.id;  // a stage-up makes a new object
  } else {
    // a won fight: parasiteLead more living shapers than the enemy (round 7; advantageLead was never reached there);
    // round 5: only with the group already near it (round 4's attempts started with "0 of 4 there" and ended too long).
    // Round 8: the healthy start counts per bot near it (parasiteHp each; the team's average over every bot, far ones
    // too, was rarely met right after a fight), only the base's siege blocks it (any base objective did, and with the
    // Cores regrowing in 480 s that was most of a late match), and each think without an attempt counts its reason
    // (BrainStats::parasiteNo: rounds 5-7 had no attempt at all).
    int nearby = 0;
    if (w.hasParasite)
      for (const Shaper& s : w.shapers)
        if (able(s) && s.Frac() >= t_.parasiteHp && Dist(s.pos, w.parasite.pos) <= t_.parasiteNear) ++nearby;
    ParasiteNo no = ParasiteNo::kNone;
    if (now < t_.parasiteFromS) no = ParasiteNo::kEarly;
    else if (!w.hasParasite || !w.parasiteHittable) no = ParasiteNo::kNoParasite;
    else if (now - p.parasiteEndedAt < t_.parasiteRetryS) no = ParasiteNo::kRetry;
    else if (own < theirs + t_.parasiteLead) no = ParasiteNo::kNoWonFight;
    else if (p.siege || p.guard != TeamPlan::kGuardOff) no = ParasiteNo::kSiege;  // round 11: the Guardian phase too
    else if (living < 2 || nearby < std::max(2, std::min(t_.parasiteAllies, living))) no = ParasiteNo::kTooFewNear;
    if (no != ParasiteNo::kNone) {
      ++stats_.parasiteNo[static_cast<int>(no)];
      return;
    }
    p.parasite = true;
    p.parasiteSince = now;
    p.parasiteId = w.parasite.id;
    p.parasiteGrouped = false;
    ++stats_.parasiteStarts;
  }
  // the group: the able bots near it (the gathering point stands parasiteGather out)
  int count = 0;
  for (const Shaper& s : w.shapers)
    if (able(s) && Dist(s.pos, w.parasite.pos) <= t_.parasiteGather + 8.f) ++count;
  const int needed = std::max(2, std::min(t_.parasiteAllies, living));
  p.parasiteCount = count;
  p.parasiteNeeded = needed;
  if (!p.parasiteGrouped && count >= needed) {
    p.parasiteGrouped = true;
    ++stats_.parasiteGroups;
  } else if (p.parasiteGrouped && count < std::max(2, needed - 1)) {
    p.parasiteGrouped = false;  // never alone: one short of the group, or a bot left by itself, gathers again
  }
}

// ---- round 11/12: the base phase (the Guardian phase of round 11, the whole base siege since round 12) --------------

// The user's rule (2026-10-29): "if the guardian is 'open', the team needs to group up and push as a unit. If shapers
// are just 'tickling in' one by one, nothing happens obviously." Since round 12 for the whole enemy base: from the
// moment the push's objective is a Core (the base is open) every living bot of the team gathers at a staging point
// outside the target's and every shooting enemy structure's reach and at least guardSpawnKeep from the enemy spawn
// (its defenders respawn there); the group goes in together once every living bot is there, or guardMinBots of them
// with as many as the enemy has alive or after guardStragglerS of waiting, and never while the team is outnumbered.
// In, it takes the Cores one after another (the nearest to it, damaged first: PlanObjective) and then the Guardian
// without gathering again in between (the 480-s regrowth: round 11's sieges took the Cores one at a time and lost them
// again); it backs off together (to the staging point) when outnumbered or with half of those who went in down, and
// waits guardRetryS before the next engage. A bot that respawns walks to the staging point (by the push lane when the
// straight way passes a living enemy Binding: RouteTo); one that reaches it within guardStragglerS of the engage joins,
// later ones wait for the next. Round 11's `_d1`: 367 s of an open Guardian with the group never past 3 of 5 (a bot
// walked into an enemy Binding's fire 104 times on the straight way to the staging point); `_c1`: the model case, four
// in together, the Guardian dead 31 s later.
void Brain::PlanGuardian(const World& w, int team, TeamPlan& p) {
  const int enemy = Enemy(team);
  auto off = [&](const char* why) {
    if (p.guard != TeamPlan::kGuardOff) p.guardWhy = why;
    p.guard = TeamPlan::kGuardOff;
    p.guardTarget = 0;
    p.guardMembers.clear();
    p.guardCount = p.guardNeeded = 0;
  };
  const Structure* objective = p.objective ? w.FindStructure(p.objective) : nullptr;
  const bool baseObjective = objective && (objective->kind == Kind::kCore || objective->kind == Kind::kGuardian);
  if (!p.push || !baseObjective) {
    off(!p.push ? "no push" : "the base closed");
    return;
  }
  // the target: kept while it lives and can be hit (a stable staging point while the group gathers), else the plan's
  // objective (the Core nearest the team's bots there, damaged first; the Guardian once unshielded)
  const Structure* kept = p.guardTarget ? w.FindStructure(p.guardTarget) : nullptr;
  const Structure* target = kept && !kept->shielded ? kept : objective;
  const bool newTarget = target->id != p.guardTarget;
  if (newTarget) {
    // the staging point: outside the target's reach and every shooting enemy structure's, toward the own base, and
    // guardSpawnKeep from the enemy spawn
    Vec3 st = Toward(target->pos, w.base[team].spawn, std::max(target->reach, 8.f) + t_.guardStageBack);
    for (int pass = 0; pass < 2; ++pass) {
      for (const Structure& s : w.structures) {
        if (s.team != enemy || !s.Shoots()) continue;
        const float keep = s.reach + t_.towerMargin + 2.f;
        const float d = Dist(st, s.pos);
        if (d < keep) st = d > 0.5f ? Toward(s.pos, st, keep) : Toward(s.pos, w.base[team].spawn, keep);
      }
      const Vec3 spawn = w.base[enemy].spawn;
      if (w.base[enemy].ok && Dist(st, spawn) < t_.guardSpawnKeep)
        st = Dist(st, spawn) > 0.5f ? Toward(spawn, st, t_.guardSpawnKeep) : Toward(spawn, w.base[team].spawn, t_.guardSpawnKeep);
    }
    p.staging = AwayFromLocus(w, team, st, t_);
    p.guardTarget = target->id;
    if (p.guard == TeamPlan::kGuardOff) {
      ++stats_.guardPhases;
      p.guard = TeamPlan::kGuardGather;
      p.guardSince = w.now;
      p.guardReadyAt = -1e9;
      p.guardMembers.clear();
      p.guardWhy = target->kind == Kind::kGuardian ? "the Guardian is open" : "the enemy base is open";
    } else if (p.guard == TeamPlan::kGuardGather) {
      p.guardReadyAt = -1e9;  // the staging point moved
      p.guardWhy = target->kind == Kind::kGuardian ? "the Guardian is open" : "the next Core";
    } else {
      p.guardWhy = target->kind == Kind::kGuardian ? "on to the Guardian" : "on to the next Core";  // in: no regather
    }
  }
  const int own = LivingShapers(w, team), theirs = LivingShapers(w, enemy);
  int living = 0, there = 0;
  auto atStaging = [&](const Shaper& s) {
    return s.team == team && s.alive && s.bot && Dist(s.pos, p.staging) <= t_.guardStageRadius;
  };
  for (const Shaper& s : w.shapers) {
    if (s.team != team || !s.alive || !s.bot) continue;
    ++living;
    if (atStaging(s)) ++there;
  }
  p.guardCount = there;
  p.guardNeeded = living;
  if (p.guard == TeamPlan::kGuardGather) {
    const int minBots = std::max(2, std::min(t_.guardMinBots, living));
    if (there >= minBots) {
      if (p.guardReadyAt < 0.0) p.guardReadyAt = w.now;
    } else {
      p.guardReadyAt = -1e9;
    }
    const bool all = there >= 2 && there >= living;
    const bool enough = there >= minBots && there >= theirs;  // as many there as the enemy has alive
    const bool waited = p.guardReadyAt >= 0.0 && w.now - p.guardReadyAt >= t_.guardStragglerS;
    if ((all || enough || waited) && own >= theirs && w.now - p.guardBackOffAt >= t_.guardRetryS) {
      p.guard = TeamPlan::kGuardEngage;
      p.guardSince = w.now;
      p.guardMembers.clear();
      for (const Shaper& s : w.shapers)
        if (atStaging(s)) p.guardMembers.push_back(s.id);
      p.guardEngaged = static_cast<int>(p.guardMembers.size());
      ++stats_.guardEngages;
      stats_.guardEngageBots += static_cast<unsigned>(p.guardEngaged);
      p.guardWhy = all ? "every living bot there" : enough ? "as many there as the enemy has alive"
                                                           : "the stragglers waited for";
    }
    return;
  }
  // in: a straggler at the staging point soon after the engage joins; out together when outnumbered or half down
  if (w.now - p.guardSince <= t_.guardStragglerS)
    for (const Shaper& s : w.shapers)
      if (atStaging(s) && !Has(p.guardMembers, s.id)) p.guardMembers.push_back(s.id);
  int left = 0;
  for (std::uint32_t id : p.guardMembers) {
    const Shaper* s = w.Find(id);
    if (s && s->alive) ++left;
  }
  if (own < theirs || left * 2 <= static_cast<int>(p.guardMembers.size())) {
    p.guard = TeamPlan::kGuardGather;
    p.guardSince = w.now;
    p.guardBackOffAt = w.now;
    p.guardReadyAt = -1e9;
    ++stats_.guardBackOffs;
    p.guardWhy = own < theirs ? "outnumbered" : "half of the group down";
  }
}

// Round 12: the way to `dest` past the enemy's living Bindings: when the straight line passes within a Binding's reach
// (plus towerMargin), first to the push lane (its Bindings are down), then along it toward the enemy base (round 11's
// `_d1`: a bot walking straight to the staging point stepped into a Binding's fire and out again 104 times).
// BOTSFLIP: the staging point stands towerMargin + 2 outside every shooting structure's reach, so a straight way to it
// ends near the reach of a Binding beside it: in the user's VPS match the line from about (-92, 5) passed the top inner
// Binding at 31.9 and 32.1 in turn (reach 28 + 4), the way switched between the staging point and the bottom lane 140
// units south with each step, and each switch restarted the walk (bots stood 3-28 s); and the game's own path from
// there followed the top lane past its two living Bindings. The way's last guardStageRadius (the arrival) doesn't count,
// a line closer than routeClear outside a Binding's reach plus towerMargin goes round, and a way round is kept until the
// line clears by routeHysteresis more.
Vec3 Brain::RouteTo(const World& w, const Shaper& me, const Vec3& dest, int pushLane, bool* viaLane) const {
  const Vec3 end = Dist(me.pos, dest) > t_.guardStageRadius ? Toward(dest, me.pos, t_.guardStageRadius) : me.pos;
  const float keep = t_.routeClear + (viaLane && *viaLane ? t_.routeHysteresis : 0.f);
  bool blocked = false;
  for (const Structure& s : w.structures)
    if (s.team != me.team && s.kind == Kind::kBinding && s.Shoots() &&
        SegDist(me.pos, end, s.pos) < s.reach + t_.towerMargin + keep)
      blocked = true;
  const gms::bots::LanePath* lane = w.Lane(pushLane);
  if (viaLane) *viaLane = blocked && lane;
  if (!blocked || !lane) return dest;
  const float len = lane->Length();
  const float arc = me.team == 1 ? lane->Project(me.pos) : len - lane->Project(me.pos);
  const Vec3 onLane = LanePoint(*lane, me.team, arc);
  // to the open lane first, then along it. BOTSFLIP round 2: by a step that grows as the bot nears the lane (none at
  // 2 guardStageRadius off it, all of guardLaneStep within guardStageRadius); the old switch at guardStageRadius moved
  // the goal 34 units with 0.3 units of jitter (botsflip_b1: a bot 14 off the top lane stood 79 s, re-aimed every think)
  const float off = Dist(me.pos, onLane), r = t_.guardStageRadius;
  const float ahead = t_.guardLaneStep * std::max(0.f, std::min(1.f, (2.f * r - off) / r));
  return LanePoint(*lane, me.team, arc + ahead);
}

// A bot's part in the base phase: in with the group (the target, with its abilities), else to the staging point
// (backing off together, gathering, or waiting while the group is in), by RouteTo.
bool Brain::GuardianIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  const TeamPlan& p = Plan(me.team);
  if (p.guard == TeamPlan::kGuardOff) return false;
  const Structure* target = w.FindStructure(p.guardTarget);
  if (!target) return false;
  const bool member = Has(p.guardMembers, me.id);
  m.farmTarget = 0;
  if (p.guard == TeamPlan::kGuardEngage && member) {
    *out = AttackIt(target->id, true, target->kind == Kind::kGuardian, "guardian: engage");
    return true;
  }
  const char* why = p.guard == TeamPlan::kGuardEngage ? "guardian: wait at the staging point (the group is in)"
                    : member && w.now - p.guardBackOffAt < t_.guardRetryS
                        ? "guardian: back off together"
                        : Dist(me.pos, p.staging) > t_.guardStageRadius ? "guardian: to the staging point"
                                                                         : "guardian: wait for the group";
  const bool wasLane = m.routeLane;
  *out = MoveTo(RouteTo(w, me, p.staging, p.pushLane, &m.routeLane), why);
  if (m.routeLane != wasLane) ++stats_.routeSwitches;
  return true;
}

// BOTSFLIP: the threat a disengage fled (the user's VPS match: a bot of the base phase's gather met three enemy
// shapers mid-map, disengaged "outnumbered", and 2 s later, out of their count range, the plan sent it straight back:
// push -> retreat every 2 s). While the bot disengages, the seen enemy shapers within fightCountRange of it are the
// threat (their centre); afterwards the memory is refreshed while enemy shapers are seen within fleeAvoidRange of it
// (fleeMemoryS after the last sight, at most fleeMemoryMaxS after the disengage, then the bot looks again).
void Brain::RememberThreat(const World& w, const Shaper& me, Memory& m) {
  const bool active = w.now < m.fledUntil;
  if (!m.disengaging && !active) return;
  const Vec3 at = m.disengaging ? me.pos : m.fledFrom;
  const float r = m.disengaging ? t_.fightCountRange : t_.fleeAvoidRange;
  float sx = 0.f, sy = 0.f;
  int n = 0;
  for (const Shaper& s : w.shapers) {
    if (s.team == me.team || !Seen(s, me.team) || Dist(s.pos, at) > r) continue;
    sx += s.pos.x;
    sy += s.pos.y;
    ++n;
  }
  if (n == 0) return;
  if (!active) {
    m.fledSince = w.now;
    ++stats_.fleeMemories;
  }
  m.fledFrom = V(sx / static_cast<float>(n), sy / static_cast<float>(n), me.pos.z);
  m.fledUntil = std::min(w.now + t_.fleeMemoryS, m.fledSince + t_.fleeMemoryMaxS);
}

// BOTSFLIP: while the threat is remembered, a macro walk (the push, the lane, a well, the parasite, the jungle, the
// base phase's gather) or a farm target that would take the bot nearer to it than fleeAvoidRange (or than it is now)
// becomes a hold: on toward home (the retreat's way) while a seen enemy shaper is within fleeAvoidRange + 2 of the bot,
// else a stand where it is. Not a fight (an enemy that hit the bot is answered), not the retreat, not a bot that went in
// with the base phase's group. Round 3 (botsflip_b2): the hold "back" used to step away from the threat's centre,
// which in the enemy base pointed deeper into it (a team 2 bot went from (-143, -194) to (-155, -214), toward the enemy
// spawn, and was outnumbered again 2.4 s later, three times in 9 s); and its stand let the enemies walk up to it.
bool Brain::HoldBack(const World& w, const Shaper& me, const Memory& m, State s, Intent& in) {
  if (w.now >= m.fledUntil) return false;
  if (s != State::kPush && s != State::kLane && s != State::kWell && s != State::kParasite && s != State::kJungle)
    return false;
  const TeamPlan& p = Plan(me.team);
  if (p.guard == TeamPlan::kGuardEngage && Has(p.guardMembers, me.id)) return false;
  Vec3 to;
  if (in.act == Act::kMove) {
    to = in.point;
  } else if (in.act != Act::kAttack || !TargetPos(w, in.target, &to)) {
    return false;
  }
  const float d = Dist(me.pos, m.fledFrom);
  if (SegDist(me.pos, to, m.fledFrom) >= std::min(t_.fleeAvoidRange, d - 1.f)) return false;
  ++stats_.fleeHolds;
  float nearest = kHuge;
  NearestEnemy(w, me, &nearest);
  if (nearest < t_.fleeAvoidRange + 2.f) {
    in = MoveTo(HomeOf(w, me), "hold: back from the threat it fled");
  } else {
    in = MoveTo(me.pos, "hold: wait out the threat it fled");
  }
  return true;
}

// ---- one bot's think ------------------------------------------------------------------------------------------------

const Memory* Brain::MemoryOf(std::uint32_t bot) const {
  const auto it = mem_.find(bot);
  return it == mem_.end() ? nullptr : &it->second;
}

void Brain::NoteDamage(std::uint32_t victim, std::uint32_t attacker, bool attackerIsShaper, double now) {
  const auto it = mem_.find(victim);
  if (it == mem_.end()) return;
  it->second.hitAt = now;
  if (attackerIsShaper && attacker) {
    it->second.hitBy = attacker;
    it->second.hitByAt = now;
  }
}

Intent Brain::Finish(Memory& m, State s, const Intent& in, double now, bool inBase, bool alive) {
  if (m.state != s) {
    m.state = s;
    m.stateSince = now;
  }
  ++stats_.decisions;
  ++stats_.byState[static_cast<int>(s)];
  ++stats_.byAct[static_cast<int>(in.act)];
  if (in.act == Act::kRecall && s != State::kRecall) ++stats_.recalls;
  if (IntentIdle(in, alive, inBase)) ++stats_.idleViolations;
  return in;
}

Intent Brain::Done(const World& w, const Shaper& me, Memory& m, State s, Intent in, bool inBase) {
  const bool held = HoldBack(w, me, m, s, in);  // BOTSFLIP: not back into the threat it fled
  // BOTSFLIP round 3: a walk that hasn't moved the bot walkStuckMove in walkStuckS, with enemy minions within
  // unstuckRange, hits the weakest of them (a ring of minions holds a walking bot in place; it hits its way out)
  if (in.act == Act::kMove && w.now - m.stillSince >= t_.walkStuckS && Dist(me.pos, in.point) > t_.walkStuckNear) {
    const Unit* weakest = nullptr;
    for (const Unit& u : w.minions)
      if (u.team != me.team && Dist(u.pos, me.pos) <= t_.unstuckRange && (!weakest || u.hp < weakest->hp)) weakest = &u;
    if (weakest) {
      ++stats_.unstuckHits;
      in = AttackIt(weakest->id, false, false, "unstuck: hit the minions in the way");
    }
  }
  AddExtras(w, me, m, s, in);
  // BOTS2c: a long walk's goal (the lane, the push's front, the way home, a gathering point) moves to the bot's own
  // side, by its lane slot (lane mates opposite sides) and a distance of its own (its rank, its lane), so bots with one
  // goal don't walk one path in single file; near the goal it is the goal itself. Not for a well's ring, a camp or a
  // ward spot, whose point matters.
  const bool spread = s == State::kLane || s == State::kPush || s == State::kRetreat || s == State::kParasite;
  if (spread && !held && in.act == Act::kMove && Dist(in.point, me.pos) >= 3.f * t_.walkSpread + 3.f &&
      std::strncmp(in.why, "ward:", 5) != 0) {
    const LaneSlot slot = LaneSlotOf(w, me, t_);
    const float side = me.lane > 0 ? slot.side : WalkSideOf(me.id);
    const float by = t_.walkSpread * (1.f + 0.5f * static_cast<float>(slot.rank) + (me.lane == 2 ? 0.25f : 0.f));
    in.point = Beside(in.point, V(in.point.x - me.pos.x, in.point.y - me.pos.y), side, by);
  }
  return Finish(m, s, in, w.now, inBase);
}

bool Brain::RecallAllowed(const World& w, const Shaper& me, const Memory& m) const {
  float d = kHuge;
  NearestEnemy(w, me, &d);
  return d > t_.recallSafeRange && w.now - m.recallAt >= t_.recallRetryS && w.now - m.hitAt >= 1.0;
}

bool Brain::WantsGoldRecall(const World& w, const Shaper& me, const Memory& m) const {
  // BOTS2c: not in the first minutes, and only with gold for the next step of its build (nothing to buy: no recall)
  if (w.now < t_.goldRecallFromS || me.nextItemPrice == 0 ||
      (me.nextItemPrice > 0 && me.gold < static_cast<float>(me.nextItemPrice)))
    return false;
  const bool want = (me.gold >= t_.recallGold && me.Frac() < t_.recallGoldHp) || me.gold >= t_.recallGoldAlways;
  if (!want || w.now - m.hitAt < t_.recallQuietS) return false;
  // not among enemy minions: their hits break the channel
  for (const Unit& u : w.minions)
    if (u.team != me.team && Dist(u.pos, me.pos) <= t_.recallMinionRange) return false;
  // not in the middle of a siege: near the enemy Guardian on a push, or with the group at its objective
  const TeamBase& eb = w.base[Enemy(me.team)];
  const TeamPlan& p = Plan(me.team);
  if (p.push && eb.hasGuardian && Dist(me.pos, eb.guardian) <= t_.siegeNoRecallRange) return false;
  if (p.grouped && Dist(me.pos, p.objectivePos) <= t_.siegeNoRecallRange) return false;
  // BOTS2c: nor at the team's parasite attempt, nor on its well task
  if (p.parasite && w.hasParasite && Dist(me.pos, w.parasite.pos) <= t_.siegeNoRecallRange) return false;
  if (p.wellBot == me.id) return false;
  return true;
}

// BOTS2c: a fight to the kill in the laning phase: 1 a low target (killOdds, the target at killHp), 2 a gank (gankOdds
// with the bot at tradeHp or more), 0 none.
int Brain::KillChance(const Shaper& me, const Shaper& target, float odds) const {
  if (odds >= t_.killOdds && target.Frac() <= t_.killHp) return 1;
  if (odds >= t_.gankOdds && me.Frac() >= t_.tradeHp) return 2;
  return 0;
}

// The fight's target: the one held (committed) if it still qualifies, else the best new one. In the laning phase
// (BOTS2c: the team doesn't push yet) a fight is a short trade (an enemy shaper within tradeRange, at even odds or
// better, the bot healthy, every tradeGapS; or one that hit it), or a fight to the kill (KillChance); a hit at bad odds
// sends the bot back behind its minions.
const Shaper* Brain::FightTarget(const World& w, const Shaper& me, Memory& m) {
  const double now = w.now;
  const float hp = me.Frac();
  const bool lane = LanePhase(me.team);
  if (m.fightTarget) {
    const Shaper* s = w.Find(m.fightTarget);
    bool keep = s && s->alive && s->team != me.team;
    if (keep) {
      if (Seen(*s, me.team)) m.targetSeenAt = now;
      else if (now - m.targetSeenAt > t_.lostSightS) keep = false;
    }
    const float chase = lane && m.fightMode == Memory::kFightKill ? t_.laneChaseRange : t_.chaseRange;
    if (keep && Dist(m.engagePoint, s->pos) > chase) keep = false;
    if (keep && Unsafe(w, me, s->pos, t_) && !(s->Frac() <= t_.diveHp && hp >= t_.diveSelfHp)) keep = false;
    const float odds = keep ? FightOdds(w, me, s, t_) : 0.f;
    const float commit = lane && m.fightMode == Memory::kFightTrade ? t_.tradeS : t_.fightCommitS;
    if (keep && now - m.fightSince >= commit && odds < t_.fleeOdds) keep = false;
    if (keep && lane && m.fightMode == Memory::kFightTrade) {
      if (KillChance(me, *s, odds)) {  // the trade turned into a kill chance
        m.fightMode = Memory::kFightKill;
        ++stats_.tradeToKill;
      } else if (now - m.fightSince >= t_.tradeS || Dist(me.pos, s->pos) > t_.tradeRange + 6.f) {
        keep = false;  // the trade is over: step back
        m.tradeEndAt = now;
        m.backOffUntil = now + t_.tradeGapS;
      }
    }
    if (keep) return s;
    m.fightTarget = 0;
    m.fightMode = Memory::kFightNone;
  }
  const Shaper* best = nullptr;
  float bestScore = kHuge;
  int bestMode = Memory::kFightKill, bestKind = 0;
  for (const Shaper& e : w.shapers) {
    if (e.team == me.team || !Seen(e, me.team)) continue;
    const float d = Dist(e.pos, me.pos);
    const bool hitMe = m.hitBy == e.id && now - m.hitByAt <= t_.hitBackS;
    if (d > t_.engageRange && !(hitMe && d <= t_.engageRange + 10.f)) continue;
    if (Unsafe(w, me, e.pos, t_) && !(e.Frac() <= t_.diveHp && hp >= t_.diveSelfHp)) continue;
    const float odds = FightOdds(w, me, &e, t_);
    int mode = Memory::kFightKill;
    int kind = 0;
    if (lane) {
      kind = KillChance(me, e, odds);
      const bool kill = kind != 0;
      const bool trade = d <= t_.tradeRange && now - m.tradeEndAt >= t_.tradeGapS && hp >= t_.tradeHp &&
                         odds >= t_.engageOdds;
      const bool hitBack = hitMe && odds >= t_.engageOdds && now >= m.backOffUntil;  // within engageRange + 10
      if (!kill && !trade && !hitBack) {
        if (hitMe && odds < t_.engageOdds && now >= m.backOffUntil) {  // hit at bad odds: back behind the minions
          m.backOffUntil = now + t_.tradeGapS;
          ++stats_.backOffs;
        }
        continue;
      }
      mode = kill ? Memory::kFightKill : Memory::kFightTrade;
    } else if (odds < t_.engageOdds && !(hitMe && odds >= t_.fleeOdds)) {
      continue;
    }
    const float score = e.Frac() * 100.f + d;  // the weakest first, then the nearest
    if (score < bestScore) {
      bestScore = score;
      best = &e;
      bestMode = mode;
      bestKind = kind;
    }
  }
  if (best) {
    m.fightTarget = best->id;
    m.fightSince = now;
    m.engagePoint = me.pos;
    m.targetSeenAt = now;
    m.fightMode = bestMode;
    ++stats_.engages;
    if (lane) ++(bestMode == Memory::kFightTrade ? stats_.trades : bestKind == 2 ? stats_.killGanks : stats_.killLow);
  }
  return best;
}

bool Brain::KeepFarm(const World& w, const Shaper& me, const Memory& m, float range) const {
  if (!m.farmTarget || w.now - m.farmSince >= t_.retargetS) return false;
  if (const Unit* u = w.FindUnit(m.farmTarget)) return Dist(u->pos, me.pos) <= range && !Unsafe(w, me, u->pos, t_);
  if (const Structure* s = w.FindStructure(m.farmTarget)) return !s->shielded && StructureSafe(w, me, *s, t_);
  return false;
}

bool Brain::DefendIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  const TeamPlan& p = Plan(me.team);
  if (!p.threatened || std::find(p.defenders.begin(), p.defenders.end(), me.id) == p.defenders.end()) return false;
  const int enemy = Enemy(me.team);
  const Shaper* shaper = nullptr;
  float sd = kHuge;
  for (const Shaper& e : w.shapers) {
    if (e.team != enemy || !Seen(e, me.team)) continue;
    const float d = Dist(e.pos, p.threat);
    if (d <= t_.defendRadius + 10.f && d < sd) {
      sd = d;
      shaper = &e;
    }
  }
  if (shaper) {
    *out = AttackIt(shaper->id, true, shaper->Frac() <= t_.ultHp, "defend: the shaper at the base");
    return true;
  }
  const Unit* minion = nullptr;
  float md = kHuge;
  for (const Unit& u : w.minions) {
    if (u.team != enemy) continue;
    const float d = Dist(u.pos, p.threat);
    if (d <= t_.defendRadius && d < md) {
      md = d;
      minion = &u;
    }
  }
  if (minion) {
    if (m.farmTarget != minion->id) {
      m.farmTarget = minion->id;
      m.farmSince = w.now;
    }
    *out = AttackIt(minion->id, false, false, "defend: the minions at the base");
    return true;
  }
  *out = MoveTo(p.threat, "defend: to the threatened structure");
  return true;
}

bool Brain::JungleIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  if (KeepFarm(w, me, m, t_.campEngage)) {
    if (const Unit* u = w.FindUnit(m.farmTarget); u && u->team == 0) {
      *out = AttackIt(u->id, true, false, "jungle: the camp");
      return true;
    }
  }
  const std::vector<Camp> camps = Camps(w, t_.campJoin);
  const Camp* best = nullptr;
  float bestCost = kHuge;
  // BOTS2c round 5: a buff or pig camp (its major) only from level 3 and with half its HP (HUNTERROUTE's opening takes
  // them from level 3 too): round 4's Hunter went from full HP to 21 % on his first camp at level 1, and recalled
  const bool weak = me.level < 3 || me.Frac() < t_.tradeHp;
  for (const Camp& c : camps) {
    if (Unsafe(w, me, c.center, t_)) continue;
    bool major = false;
    for (std::uint32_t id : c.members)
      if (const Unit* u = w.FindUnit(id); u && (u->npc == kNpcObjectiveMinor || u->npc == kNpcObjectiveMedium))
        major = true;
    if (major && weak) continue;
    const float cost = gms::bots::CampRouteCost(Dist(c.center, me.pos), c.center.x, c.center.y, me.team, t_.campPenalty);
    if (cost < bestCost) {
      bestCost = cost;
      best = &c;
    }
  }
  if (!best) return false;
  if (Dist(best->center, me.pos) > t_.campEngage) {
    *out = MoveTo(best->center, "jungle: to the camp");
    return true;
  }
  const Unit* weakest = nullptr;
  for (std::uint32_t id : best->members) {
    const Unit* u = w.FindUnit(id);
    if (u && (!weakest || u->hp < weakest->hp)) weakest = u;
  }
  if (!weakest) return false;
  m.farmTarget = weakest->id;
  m.farmSince = w.now;
  *out = AttackIt(weakest->id, true, false, "jungle: the camp");
  return true;
}

// The push (the team plan's objective): the group hits the objective together; until the group is there, a bot farms
// the push lane's lane work (Lane's rules there).
bool Brain::PushIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  const TeamPlan& p = Plan(me.team);
  if (!p.push || !p.objective) return false;
  const Structure* obj = w.FindStructure(p.objective);
  if (!obj) return false;
  // (the one the structure shoots steps out first: Decide's 1b); the base only with an edge (round 7), else the waves
  if (p.grouped && (!p.siege || p.siegeEdge)) {
    m.farmTarget = obj->id;
    m.farmSince = w.now;
    *out = AttackIt(obj->id, false, false, "push: the group hits the objective");
    return true;
  }
  // until the group is together: the push lane's lane work (its own front, its minions, a tanked structure), which
  // brings the bots to the objective with the waves; waiting at the rally point would be idle time ...
  *out = LaneIntent(w, me, m, p.pushLane, false);
  // ... unless the lane work keeps the bot well behind the rally point (round 4: a group never formed at the inner
  // Binding or the Cores for 6 and 9 minutes: the waves died before the rally point and the bots stood behind them)
  const bool baseObj = obj->kind == Kind::kCore || obj->kind == Kind::kGuardian;  // round 7: the base only with an edge
  if (out->act == Act::kMove && (!baseObj || p.siegeEdge) &&
      Dist(out->point, obj->pos) > Dist(p.rally, obj->pos) + t_.rallyPull)
    *out = MoveTo(p.rally, "push: gather at the rally point");
  return true;
}

// BOTS2c: a defender of an own well under capture fights the capturer it sees there, else stands in the ring (which
// contests the capture); the team's well bot walks to its well and stands in the ring (a capture: hitting the workers
// that come into the ring) or hits the well's workers (a raid).
bool Brain::WellIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  const TeamPlan& p = Plan(me.team);
  if (p.wellThreat && Has(p.wellDefenders, me.id)) {
    if (const Well* wl = w.FindWell(p.wellThreat)) {
      const Shaper* cap = nullptr;
      float cd = kHuge;
      for (const Shaper& e : w.shapers) {
        if (e.team == me.team || !Seen(e, me.team)) continue;
        const float d = Dist(e.pos, wl->pos);
        if (d <= wl->radius + 6.f && d < cd) {
          cd = d;
          cap = &e;
        }
      }
      if (cap) {
        *out = AttackIt(cap->id, true, cap->Frac() <= t_.ultHp, "well: the capturer");
        return true;
      }
      *out = MoveTo(WellStand(w, me, *wl, t_), "well: into the ring (contest)");
      return true;
    }
  }
  if (p.wellBot != me.id) return false;
  const Well* wl = w.FindWell(p.wellTarget);
  if (!wl) return false;
  const Vec3 stand = WellStand(w, me, *wl, t_);
  const bool raid = p.wellKind == TeamPlan::kWellRaid;
  const bool inRing = Dist(me.pos, wl->pos) <= wl->radius - 1.5f;
  if (raid || inRing) {
    // the workers: a raid takes those of the platform, a capture only those inside the ring (it must stay there)
    const float reach = raid ? t_.raidRange : wl->radius - 2.f;
    const Unit* best = nullptr;
    for (const Unit& u : w.workers) {
      if (u.team != wl->owner || Dist(u.pos, wl->pos) > reach || Unsafe(w, me, u.pos, t_)) continue;
      if (!best || u.hp < best->hp) best = &u;
    }
    if (best) {
      m.farmTarget = best->id;
      m.farmSince = w.now;
      *out = AttackIt(best->id, raid, false, raid ? "well: raid the workers" : "well: capturing (a worker)");
      return true;
    }
    if (inRing && !raid) {
      *out = MoveTo(stand, "well: capturing");
      return true;
    }
  }
  *out = MoveTo(stand, raid ? "well: to the workers"
                            : p.wellKind == TeamPlan::kWellRetake ? "well: to retake it" : "well: to take it");
  return true;
}

// BOTS2c: the team's parasite attempt: gather outside its reach until the group is there, then hit it together.
bool Brain::ParasiteIntent(const World& w, const Shaper& me, Memory& m, Intent* out) {
  const TeamPlan& p = Plan(me.team);
  if (!p.parasite || !w.hasParasite || OnTeamDuty(p, me.id)) return false;
  if (p.threatened && Has(p.defenders, me.id)) return false;
  if (p.parasiteGrouped && w.parasiteHittable) {
    m.farmTarget = w.parasite.id;
    m.farmSince = w.now;
    *out = AttackIt(w.parasite.id, true, false, "parasite: the group hits it");
    return true;
  }
  *out = MoveTo(Toward(w.parasite.pos, HomeOf(w, me), t_.parasiteGather), "parasite: gather");
  return true;
}

// ---- BOTS2c: the extras ---------------------------------------------------------------------------------------------

// Whether the bot carries spell `s` and may cast it now (its slot in `slot`).
bool Brain::SpellReady(const Memory& m, const Shaper& me, Spell s, int* slot, double now) const {
  for (int k = 0; k < 3; ++k)
    if (me.spells[k] == s && now >= m.spellReadyAt[k]) {
      *slot = kExtraSlotSpell1 + k;
      return true;
    }
  return false;
}

Extra Brain::SpellAt(const Shaper& me, int slot, Spell s, std::uint32_t target, const char* why) const {
  (void)me;
  Extra e;
  e.kind = ExtraKind::kSpell;
  e.slot = slot;
  e.spell = s;
  e.target = target;
  e.why = why;
  return e;
}

// A fight's spell (the target an enemy shaper): first survive (Stasis at the last stand, a shield while being hit),
// then a hurt team mate's shield, then the target (Drain, Wither, Stagger in range; Exceed close; Blitz or Tailwind to
// close in).
Extra Brain::FightSpell(const World& w, const Shaper& me, const Memory& m, const Shaper& target) const {
  const double now = w.now;
  const float hp = me.Frac();
  const bool hit = now - m.hitAt <= t_.hitRecentS;
  float de = kHuge;
  NearestEnemy(w, me, &de);
  int slot = -1;
  if (hp < t_.lastStandHp && hit && de <= t_.escapeRange && SpellReady(m, me, Spell::kStasis, &slot, now))
    return SpellAt(me, slot, Spell::kStasis, 0, "fight: last stand");
  if (hp < t_.spellHurtHp && hit) {
    if (SpellReady(m, me, Spell::kBastion, &slot, now)) return SpellAt(me, slot, Spell::kBastion, 0, "fight: shield");
    if (SpellReady(m, me, Spell::kDeflect, &slot, now)) {
      Extra e = SpellAt(me, slot, Spell::kDeflect, 0, "fight: shield");
      e.atPoint = true;
      e.point = me.pos;
      return e;
    }
  }
  if (SpellReady(m, me, Spell::kDeflect, &slot, now)) {  // the most hurt team mate in its range
    const Shaper* ally = nullptr;
    for (const Shaper& s : w.shapers) {
      if (s.team != me.team || !s.alive || s.id == me.id || s.Frac() >= t_.allyShieldHp) continue;
      if (Dist(s.pos, me.pos) > DataOf(Spell::kDeflect).range - t_.spellRangeMargin) continue;
      if (!ally || s.Frac() < ally->Frac()) ally = &s;
    }
    if (ally) {
      Extra e = SpellAt(me, slot, Spell::kDeflect, 0, "fight: shield a team mate");
      e.atPoint = true;
      e.point = ally->pos;
      return e;
    }
  }
  const float dt = Dist(me.pos, target.pos);
  for (Spell s : {Spell::kDrain, Spell::kWither, Spell::kStagger})
    if (dt <= DataOf(s).range - t_.spellRangeMargin && SpellReady(m, me, s, &slot, now))
      return SpellAt(me, slot, s, target.id, "fight: at the target");
  if (dt <= t_.exceedRange && SpellReady(m, me, Spell::kExceed, &slot, now))
    return SpellAt(me, slot, Spell::kExceed, 0, "fight: power");
  if (dt > t_.chaseSpellDist)
    for (Spell s : {Spell::kBlitz, Spell::kTailwind})
      if (SpellReady(m, me, s, &slot, now)) return SpellAt(me, slot, s, 0, "fight: close in");
  return Extra();
}

// A retreat's spell (low HP, latched; not a mere disengage): the last stand, a shield while being hit, an escape or a
// heal while an enemy shaper is near (Blink back along the lane, Blitz, Tailwind, Dispel when hit, Drain on the chaser).
Extra Brain::RetreatSpell(const World& w, const Shaper& me, const Memory& m) const {
  const double now = w.now;
  const float hp = me.Frac();
  const bool hit = now - m.hitAt <= t_.hitRecentS;
  float de = kHuge;
  const Shaper* chaser = NearestEnemy(w, me, &de);
  int slot = -1;
  if (hp < t_.lastStandHp && hit && de <= t_.escapeRange && SpellReady(m, me, Spell::kStasis, &slot, now))
    return SpellAt(me, slot, Spell::kStasis, 0, "retreat: last stand");
  if (hp < t_.spellHurtHp && hit) {
    if (SpellReady(m, me, Spell::kBastion, &slot, now)) return SpellAt(me, slot, Spell::kBastion, 0, "retreat: shield");
    if (SpellReady(m, me, Spell::kDeflect, &slot, now)) {
      Extra e = SpellAt(me, slot, Spell::kDeflect, 0, "retreat: shield");
      e.atPoint = true;
      e.point = me.pos;
      return e;
    }
  }
  if (!chaser || de > t_.escapeRange) return Extra();
  if (SpellReady(m, me, Spell::kBlink, &slot, now)) {  // back along the bot's lane, when it stands on one
    const gms::bots::LanePath* lane = w.Lane(me.lane);
    if (lane && lane->knots.size() >= 2 && gms::bots::ClosestLane(w.lanes, me.pos) == lane->lane) {
      const float len = lane->Length();
      const float arc = lane->Project(me.pos);
      const float own = me.team == 1 ? arc : len - arc;
      const Vec3 onLane = LanePoint(*lane, me.team, own);
      const Vec3 back = LanePoint(*lane, me.team, own - t_.blinkStep);
      if (Dist(onLane, me.pos) <= t_.laneWidth && Dist(back, me.pos) <= DataOf(Spell::kBlink).range - t_.spellRangeMargin &&
          Dist(back, me.pos) >= t_.blinkStep * 0.5f && !(back.x == 0.f && back.y == 0.f)) {
        Extra e = SpellAt(me, slot, Spell::kBlink, 0, "retreat: blink home");
        e.atPoint = true;
        e.point = back;
        return e;
      }
    }
  }
  for (Spell s : {Spell::kBlitz, Spell::kTailwind})
    if (SpellReady(m, me, s, &slot, now)) return SpellAt(me, slot, s, 0, "retreat: escape");
  if (hit && SpellReady(m, me, Spell::kDispel, &slot, now)) return SpellAt(me, slot, Spell::kDispel, 0, "retreat: cleanse");
  if (de <= DataOf(Spell::kDrain).range - t_.spellRangeMargin && SpellReady(m, me, Spell::kDrain, &slot, now))
    return SpellAt(me, slot, Spell::kDrain, chaser->id, "retreat: drain the chaser");
  return Extra();
}

// Vanquish (300 + 30 x level true damage; NPCs, never a shaper or the Guardian): at a camp creature it kills
// (HUNTERROUTE's rule, VanquishPick: the mediums and majors, any at low HP), else at the parasite the bot hits.
Extra Brain::VanquishAt(const World& w, const Shaper& me, const Memory& m, std::uint32_t target, bool jungle) const {
  int slot = -1;
  if (!SpellReady(m, me, Spell::kVanquish, &slot, w.now)) return Extra();
  const float reach = DataOf(Spell::kVanquish).range - t_.spellRangeMargin;
  if (jungle) {
    gms::bots::VanquishConfig cfg;
    cfg.reach = reach;
    std::vector<gms::bots::VanquishCandidate> cands;
    std::vector<std::uint32_t> ids;
    for (const Unit& u : w.creeps) {
      const float d = Dist(u.pos, me.pos);
      if (d > t_.campEngage) continue;
      gms::bots::VanquishCandidate c;
      c.hp = u.hp;
      c.maxHp = u.maxHp;
      c.dist = d;
      c.objective = u.npc >= kNpcObjectiveMinor && u.npc <= kNpcParasite;
      cands.push_back(c);
      ids.push_back(u.id);
    }
    const int i = gms::bots::VanquishPick(cands, me.level, me.Frac(), cfg);
    if (i < 0) return Extra();
    return SpellAt(me, slot, Spell::kVanquish, ids[static_cast<std::size_t>(i)], "jungle: vanquish");
  }
  // the parasite only: the engine refused every Vanquish at an enemy Binding (-0x2708, an invalid target; 35 of 35 in
  // the first live round), whatever the script's target mask says
  if (!w.hasParasite || target != w.parasite.id || Dist(w.parasite.pos, me.pos) > reach) return Extra();
  return SpellAt(me, slot, Spell::kVanquish, target, "vanquish the parasite");
}

// The extras besides the intent, at most one a think: a potion when hurt (outside the base, not recalling), else the
// state's spell (a retreat's, a fight's at an enemy shaper, Vanquish at a camp or the parasite), else a ward
// at a free spot on the way (a lane, push or jungle walk turns to a spot within wardDetour).
void Brain::AddExtras(const World& w, const Shaper& me, Memory& m, State s, Intent& in) {
  if (!me.alive || s == State::kDead || s == State::kBase || s == State::kRecall || in.act == Act::kRecall ||
      me.channelling || InBase(w, me, t_))
    return;
  const double now = w.now;
  Extra e;
  // 1. a potion
  if (me.potions > 0 && me.potionSlot >= kExtraSlotConsume1 && me.Frac() < t_.potionHp &&
      me.maxHp - me.hp >= kPotionHeal && now >= m.potionReadyAt) {
    e.kind = ExtraKind::kPotion;
    e.slot = me.potionSlot;
    e.why = "hurt: a potion";
  }
  // 2. a spell
  if (e.kind == ExtraKind::kNone) {
    if (s == State::kRetreat) {
      if (m.retreating) e = RetreatSpell(w, me, m);
    } else if (in.act == Act::kAttack) {
      const Shaper* t = w.Find(in.target);
      if (t && t->team != me.team) e = FightSpell(w, me, m, *t);
      else if (s == State::kJungle) e = VanquishAt(w, me, m, 0, true);
      else e = VanquishAt(w, me, m, in.target, false);
    }
  }
  // 3. a ward on the way
  const bool wardState = s == State::kLane || s == State::kPush || s == State::kJungle || s == State::kWell ||
                         s == State::kParasite;
  if (e.kind == ExtraKind::kNone && wardState && now >= m.wardReadyAt && !w.wardSpots.empty()) {
    const Vec3* best = nullptr;
    float bd = kHuge;
    for (const Vec3& spot : w.wardSpots) {
      const float d = Dist(spot, me.pos);
      if (d > t_.wardDetour || d >= bd || Unsafe(w, me, spot, t_)) continue;
      bool taken = false;
      for (const Unit& ward : w.wards)
        if (ward.team == me.team && Dist(ward.pos, spot) <= kWardSpacing) taken = true;
      if (taken) continue;
      bd = d;
      best = &spot;
    }
    if (best) {
      if (bd <= kWardRange - t_.wardCastMargin) {
        e.kind = ExtraKind::kWard;
        e.slot = kExtraSlotWard;
        e.atPoint = true;
        e.point = *best;
        e.why = "ward: a free spot";
      } else if (in.act == Act::kMove && (s == State::kLane || s == State::kPush || s == State::kJungle)) {
        in.point = *best;  // the detour: walk to the spot (the cast comes within its range)
        in.why = "ward: to a free spot";
        ++stats_.wardDetours;
      }
    }
  }
  if (e.kind == ExtraKind::kNone) return;
  in.extra = e;
  ++stats_.extras[static_cast<int>(e.kind)];
}

void Brain::NoteExtra(std::uint32_t bot, const Extra& e, int result, double now) {
  const auto it = mem_.find(bot);
  if (it == mem_.end()) return;
  Memory& m = it->second;
  const bool started = result >= 1 && result <= 4;  // started, or queued for the moment its cooldown ends
  const int k = static_cast<int>(e.kind);
  if (started && k > 0 && k < static_cast<int>(ExtraKind::kCount)) ++stats_.extrasStarted[k];
  switch (e.kind) {
    case ExtraKind::kSpell: {
      const int i = e.slot - kExtraSlotSpell1;
      if (i < 0 || i > 2) return;
      m.spellReadyAt[i] = now + (started ? static_cast<double>(DataOf(e.spell).cooldownS) : t_.castRetryS);
      if (started) ++stats_.spellsStarted[static_cast<int>(e.spell)];
      return;
    }
    case ExtraKind::kPotion:
      m.potionReadyAt = now + (started ? static_cast<double>(kPotionSeconds) : t_.castRetryS);
      return;
    case ExtraKind::kWard:
      m.wardReadyAt = now + (started ? static_cast<double>(kWardCooldownS) : t_.wardRetryS);
      return;
    default: return;
  }
}

Intent Brain::LaneIntent(const World& w, const Shaper& me, Memory& m, int laneId, bool push) {
  const gms::bots::LanePath* lane = w.Lane(laneId);
  const int enemy = Enemy(me.team);
  // 1. the target held
  if (KeepFarm(w, me, m, t_.farmRange + 5.f)) {
    const bool structure = w.FindStructure(m.farmTarget) != nullptr;
    return AttackIt(m.farmTarget, false, false, structure ? "lane: the structure" : "lane: the minions");
  }
  // 2. the enemy minions within reach (the lowest HP first: last hits); not while it backs off after a trade, and not
  // the minion a lane mate near it hits (BOTS2c: round 2's two melee bots of a lane stood on one point at one minion)
  // (round 6: no more spacing rule for the lane's second bot and no last-hits-only mode for a hurt one: both cost farm,
  // the duo lanes' second bots took 4-9 creeps in 10 minutes in round 5, and the close pairs stayed below classic's)
  std::vector<std::uint32_t> mates;
  for (const Shaper& s : w.shapers) {
    if (s.id == me.id || s.team != me.team || !s.alive || !s.bot || Dist(s.pos, me.pos) > t_.farmRange) continue;
    const auto it = mem_.find(s.id);
    if (it != mem_.end() && it->second.farmTarget) mates.push_back(it->second.farmTarget);
  }
  const Unit* minion = nullptr;
  for (const Unit& u : w.minions) {
    if (!push && w.now < m.backOffUntil) break;
    if (Has(mates, u.id)) continue;
    if (u.team != enemy || Dist(u.pos, me.pos) > t_.farmRange || Unsafe(w, me, u.pos, t_)) continue;
    if (!minion || u.hp < minion->hp) minion = &u;
  }
  if (minion) {
    m.farmTarget = minion->id;
    m.farmSince = w.now;
    return AttackIt(minion->id, false, false, "lane: the minions");
  }
  // 3. an enemy structure it may hit (Bindings when tanked or with the group; Cores; the Guardian unshielded)
  const Structure* structure = nullptr;
  float sd = kHuge;
  for (const Structure& s : w.structures) {
    if (s.team != enemy || s.kind == Kind::kLocus || s.shielded) continue;
    const float d = Dist(s.pos, me.pos);
    const float reachOut = std::max(s.reach, 15.f) + 12.f;  // near enough to walk up and hit
    if (d > reachOut || !StructureSafe(w, me, s, t_)) continue;
    if (d < sd) {
      sd = d;
      structure = &s;
    }
  }
  if (structure) {
    m.farmTarget = structure->id;
    m.farmSince = w.now;
    return AttackIt(structure->id, false, false, push ? "push: the structure" : "lane: the structure");
  }
  // 4. the push: the next enemy structure when the group is together (or the minions are there)
  if (push && lane) {
    const Structure* next = nullptr;
    float nd = kHuge;
    for (const Structure& s : w.structures) {
      if (s.team != enemy || s.kind == Kind::kLocus || s.shielded) continue;
      if (s.kind == Kind::kBinding && gms::bots::ClosestLane(w.lanes, s.pos) != lane->lane) continue;
      // Bindings in lane order (the one nearest the own base first), then the base
      const float order = s.kind == Kind::kBinding ? 0.f : s.kind == Kind::kCore ? 1000.f : 2000.f;
      const float d = order + Dist(s.pos, me.pos);
      if (d < nd) {
        nd = d;
        next = &s;
      }
    }
    if (next && (AlliesNear(w, me, t_.siegeAllyRange) >= t_.siegeAllies ||
                 OwnMinionsWithin(w, me.team, next->pos, 25.f) >= t_.towerMinMinions)) {
      if (StructureSafe(w, me, *next, t_)) {
        m.farmTarget = next->id;
        m.farmSince = w.now;
        return AttackIt(next->id, false, false, "push: the next structure");
      }
      // not safe yet: up to its reach
      const float keep = next->reach + t_.towerMargin + 2.f;
      const float d = Dist(next->pos, me.pos);
      if (d > keep + 3.f) return MoveTo(AwayFromLocus(w, me.team, Toward(me.pos, next->pos, d - keep), t_), "push: to the next structure");
    }
  }
  // 5. its spot on the lane (LaneStand), never in an unsafe structure's reach
  if (lane) return MoveTo(LaneStand(w, me, m, *lane), push ? "push: with the own front" : "lane: with the own front");
  return MoveTo(HomeOf(w, me), "no lane: home");
}

// BOTS2c: a bot's spot on its lane. In the laning phase behind its own minions' front (the Gladiator laneCarryBack,
// its mate laneSupportBack; tradeBackOff more after a trade or a hit at bad odds), never within minionKeepOut of the
// enemy minions' front; without own minions on the lane in front of its outermost Binding (towerWait). In the push
// laneBackOff behind the front (BOTS2's rule). Always laneSide to its own side of the lane's line: the two bots of a
// lane stand and walk apart, never in single file. Pulled back out of any unsafe structure's reach.
Vec3 Brain::LaneStand(const World& w, const Shaper& me, const Memory& m, const gms::bots::LanePath& lane) const {
  float progress = 0.f;
  const LaneSlot slot = LaneSlotOf(w, me, t_);
  if (LanePhase(me.team)) {
    const LaneMinionFronts f = LaneMinions(w, lane, me.team, t_.laneWidth);
    const float mate = slot.back - t_.laneCarryBack;
    float back = slot.back;
    if (w.now < m.backOffUntil) back += t_.tradeBackOff;  // after a trade
    if (f.own > 0) {
      progress = f.ownMax - back;
    } else {
      const float tower = OwnTowerProgress(w, lane, me.team);
      progress = (tower >= 0.f ? tower : 0.3f * lane.Length()) + t_.towerWait - mate;
    }
    if (f.enemy > 0) progress = std::min(progress, f.enemyMin - t_.minionKeepOut - (back - t_.laneCarryBack));
  } else {
    progress = OwnFront(w, lane, me.team, t_).progress - t_.laneBackOff;
  }
  const float side = slot.side;
  Vec3 p = Beside(LanePoint(lane, me.team, progress), LaneDir(lane, me.team, progress), side, t_.laneSide);
  for (int i = 0; i < 40 && Unsafe(w, me, p, t_) && progress > 0.f; ++i) {
    progress -= 5.f;
    p = Beside(LanePoint(lane, me.team, progress), LaneDir(lane, me.team, progress), side, t_.laneSide);
  }
  return AwayFromLocus(w, me.team, p, t_);
}

// BOTS2c: where a hurt bot heals in the laning phase: behind its lane's outermost own Binding (towerWait back), to its
// side; false without one, or when home is nearer (the bot isn't on its lane).
bool Brain::LaneSafePoint(const World& w, const Shaper& me, Vec3* out) const {
  const gms::bots::LanePath* lane = w.Lane(me.lane);
  if (!lane || lane->knots.size() < 2) return false;
  const float tower = OwnTowerProgress(w, *lane, me.team);
  if (tower < 0.f) return false;
  const float p = std::max(0.f, tower - t_.towerWait);
  const Vec3 safe =
      Beside(LanePoint(*lane, me.team, p), LaneDir(*lane, me.team, p), LaneSlotOf(w, me, t_).side, t_.laneSide);
  if (Dist(me.pos, safe) >= Dist(me.pos, HomeOf(w, me))) return false;
  *out = safe;
  return true;
}

Intent Brain::Decide(const World& w, std::uint32_t bot) {
  Memory& m = mem_[bot];
  const double now = w.now;
  const Shaper* mep = w.Find(bot);
  if (!mep || !mep->alive) {
    m.retreating = false;
    m.fightTarget = 0;
    m.farmTarget = 0;
    m.wasInBase = false;
    m.fledUntil = -1e9;  // BOTSFLIP: a respawn starts without the old threat
    m.routeLane = false;
    m.stillSince = -1e9;
    return Finish(m, State::kDead, Plain(Act::kNone, "dead"), now, false, false);
  }
  const Shaper& me = *mep;
  if (m.stillSince < -1e8 || Dist(me.pos, m.stillAt) >= t_.walkStuckMove) {  // BOTSFLIP round 3: it moves
    m.stillAt = me.pos;
    m.stillSince = now;
  }
  const TeamBase& base = w.base[me.team == 2 ? 2 : 1];
  const bool inBase = InBase(w, me, t_);
  const float hp = me.Frac();
  if (inBase && !m.wasInBase) {  // arrived (respawn, recall, walk): a new shop visit
    m.shopDone = false;
    m.shopSince = -1.0;
  }
  m.wasInBase = inBase;

  // the recall channel runs (or was asked a moment ago): nothing else
  if (me.channelling || (m.state == State::kRecall && now - m.recallAt < 0.75)) {
    return Finish(m, State::kRecall, Plain(Act::kRecall, "the recall channels"), now, inBase);
  }

  // round 11: a bot that went in with the group in the Guardian phase leaves only hurt (the team backs off as a unit)
  const TeamPlan& sp = Plan(me.team);
  const bool guardIn = sp.guard == TeamPlan::kGuardEngage && Has(sp.guardMembers, me.id);
  // BOTSFLIP round 2: a defender at its threatened base holds its ground too (botsflip_b1: 19 of 24 quick re-flips were
  // "defend -> retreat: outnumbered", home, and "retreat -> defend" 2 s later, a team 2 bot 12 times); hurt and
  // outnumbered it still leaves (the full retreat below)
  const bool baseDefender = !guardIn && sp.threatened && Has(sp.defenders, me.id) &&
                            Dist(me.pos, sp.threat) <= t_.defendRadius + t_.fightCountRange;
  if (guardIn || baseDefender) m.disengaging = false;

  // 1. Retreat: low HP (latched until retreatExitHp), or outnumbered (disengaging until the odds reach
  // disengageExitOdds; outnumbered and hurt latches the full retreat)
  const float odds = FightOdds(w, me, nullptr, t_);
  if (!m.disengaging && !guardIn && !baseDefender && odds < t_.fleeOdds) {
    m.disengaging = true;
    m.disengageSince = now;
  }
  if (m.disengaging && (inBase || (odds >= t_.disengageExitOdds && now - m.disengageSince >= t_.disengageMinS)))
    m.disengaging = false;
  RememberThreat(w, me, m);  // BOTSFLIP: the threat it fled, avoided for a while (HoldBack)
  const bool lanePhase = LanePhase(me.team);
  // BOTS2c: a Hunter clearing his camps with a potion to drink (or one healing), no enemy shaper near, clears on until
  // recallHp (round 2: the Hunters recalled 6-9 times in 10 minutes for the camps' damage)
  float nearest = kHuge;
  NearestEnemy(w, me, &nearest);
  // (round 4: with or without a potion; the Hunters recalled 5-10 times in 10 minutes, the classic Hunters 4-8)
  const bool jungleStay = me.role == kRoleHunter && lanePhase && now < t_.hunterJoinS && !w.creeps.empty() &&
                          !m.disengaging && hp >= t_.recallHp && nearest > t_.recallSafeRange;
  if (jungleStay && hp < t_.retreatHp && !m.jungleStaying) ++stats_.jungleStays;
  m.jungleStaying = jungleStay && hp < t_.retreatHp;
  // BOTS2c round 4: in the laning phase a hurt bot retreats only from a threat (an enemy shaper within escapeRange, or
  // a shaper's hit in the last threatS: camps and minions don't count); without one it lanes on carefully (no trade
  // below tradeHp, its potions), and below recallHp it recalls. Round 3 against the classic brain: v2 recalled 4.2-4.4
  // times a bot in 10 minutes against 1.5-1.6 and lost the farm (creep score 2.1-2.3 a minute against 2.7-3.4).
  const bool threatened = nearest <= t_.escapeRange || now - m.hitByAt <= t_.threatS;
  const bool hurt = lanePhase ? (hp < t_.recallHp || (hp < t_.retreatHp && threatened)) : hp < t_.retreatHp;
  const bool outnumbered = m.disengaging || (baseDefender && odds < t_.fleeOdds);
  if (!m.retreating && !jungleStay && (hurt || (outnumbered && hp < t_.retreatOutnumberedHp))) {
    m.retreating = true;
    ++stats_.retreats;
  }
  const bool calm = lanePhase && hp >= t_.recallHp && !threatened && now - m.hitByAt > t_.threatS;
  if (m.retreating &&
      (inBase || jungleStay || calm || hp >= (lanePhase ? t_.laneRetreatExitHp : t_.retreatExitHp)))
    m.retreating = false;
  if (m.retreating || m.disengaging) {
    m.fightTarget = 0;
    m.fightMode = Memory::kFightNone;
    m.farmTarget = 0;
    // BOTS2c, the laning phase: a hurt bot steps back behind its lane's Binding (and drinks) instead of recalling, down
    // to recallHp (the user's match: fight, retreat, recall, 33 recalls in 4.5 minutes; round 3: a potion was needed,
    // and with the two drunk the bots recalled at 46-50 %)
    Vec3 safe;
    const bool stay = m.retreating && lanePhase && hp >= t_.recallHp && LaneSafePoint(w, me, &safe);
    // the recall also waits until no enemy minion is near: their hits broke round 4's low recalls, which were asked
    // again and again (the gold recall's rule)
    bool minionNear = false;
    for (const Unit& u : w.minions)
      if (u.team != me.team && Dist(u.pos, me.pos) <= t_.recallMinionRange) minionNear = true;
    for (const Unit& u : w.creeps)  // round 6: a camp's creature that follows the Hunter breaks his recall too
      if (Dist(u.pos, me.pos) <= t_.recallMinionRange) minionNear = true;
    if (m.retreating && !stay && !minionNear && RecallAllowed(w, me, m)) {
      m.recallAt = now;
      return Finish(m, State::kRecall, Plain(Act::kRecall, "retreat: recall"), now, inBase);
    }
    if (stay) {
      if (m.state != State::kRetreat) ++stats_.laneStays;
      // at its Binding: the enemy minions the own Binding shoots are safe to hit
      if (Dist(me.pos, safe) <= t_.towerWait) {
        const Unit* best = nullptr;
        for (const Unit& u : w.minions) {
          if (u.team == me.team || Dist(u.pos, me.pos) > t_.farmRange) continue;
          bool covered = false;
          for (const Structure& s : w.structures)
            if (s.team == me.team && s.Shoots() && Dist(u.pos, s.pos) <= s.reach) covered = true;
          if (covered && (!best || u.hp < best->hp)) best = &u;
        }
        if (best) return Done(w, me, m, State::kRetreat, AttackIt(best->id, false, false, "retreat: farm at the Binding"), inBase);
      }
      return Done(w, me, m, State::kRetreat, MoveTo(safe, "retreat: heal at the Binding"), inBase);
    }
    return Done(w, me, m, State::kRetreat,
                MoveTo(HomeOf(w, me), m.retreating ? "retreat: home" : "retreat: outnumbered"), inBase);
  }

  // 1b. Out of a structure's fire: an enemy Binding or Guardian that shoots this bot steps it out of its reach toward
  // home at once, unless it is finishing a kill right there (round 2: a Binding shot takes 25-35 % of a bot's HP, and
  // 21 of 68 deaths of one match were under Bindings, bots shot from full HP while the group hit them)
  // BOTS2c round 6: not in the base's siege with HP enough for it: the group tanks the base's fire (round 5: 139 escapes
  // of v2's team in one match broke every siege of the Cores)
  const Structure* siegeObj = sp.siege ? w.FindStructure(sp.objective) : nullptr;
  const bool sieging = guardIn || (siegeObj && sp.siegeEdge && hp >= t_.diveSelfHp &&
                                   Dist(me.pos, siegeObj->pos) <= std::max(siegeObj->reach, 8.f) + t_.rallyBack + 10.f);
  if (!inBase && !sieging) {
    bool finishing = false;  // a low enemy shaper the team sees right beside it, and HP enough for the dive
    for (const Shaper& e : w.shapers)
      if (e.team != me.team && Seen(e, me.team) && e.Frac() <= t_.diveHp && Dist(e.pos, me.pos) <= 10.f &&
          hp >= t_.diveSelfHp)
        finishing = true;
    for (const Structure& s : w.structures) {
      if (finishing || s.team == me.team || !s.Shoots() || s.kind == Kind::kLocus || s.target != me.id) continue;
      if (Dist(s.pos, me.pos) > s.reach + t_.towerMargin) continue;
      if (!m.escaping) ++stats_.towerEscapes;
      m.escaping = true;
      m.farmTarget = 0;
      return Done(w, me, m, State::kRetreat, MoveTo(HomeOf(w, me), "retreat: out of a structure's fire"), inBase);
    }
  }
  m.escaping = false;

  // 2. Base: the shop, then heal
  if (inBase) {
    if (!m.shopDone) {
      if (Dist(me.pos, base.spawn) > t_.shopNear) return Finish(m, State::kBase, MoveTo(base.spawn, "base: to the shop"), now, inBase);
      if (m.shopSince < 0) m.shopSince = now;
      if (now - m.shopSince < t_.shopStandS) return Finish(m, State::kBase, Plain(Act::kShop, "base: shopping"), now, inBase);
      m.shopDone = true;
    }
    if (hp < t_.leaveBaseHp) return Finish(m, State::kBase, Plain(Act::kShop, "base: healing"), now, inBase);
    // BOTS2c: before its lane's first own minions come (the first wave at 60 s), it waits in its base rather than
    // 25-40 s at its Binding (round 2's longest idle stretch); a Hunter with camps up goes to them
    const gms::bots::LanePath* waveLane = w.Lane(me.lane);
    if (lanePhase && now < t_.firstWaveWaitS && waveLane &&
        !(me.role == kRoleHunter && !w.creeps.empty() && me.level >= t_.hunterJungleLevel) &&
        LaneMinions(w, *waveLane, me.team, t_.laneWidth).own == 0) {
      if (!m.waveWaited) ++stats_.firstWaveWaits;
      m.waveWaited = true;
      return Finish(m, State::kBase, Plain(Act::kShop, "base: waiting for the first wave"), now, inBase);
    }
  }

  // 2b. Round 11: the Guardian phase. Gathering, waiting and backing off come before a fight unless a shaper hit the
  // bot (nobody walks in alone); a bot that went in fights the defenders it meets (3) and otherwise hits the target
  // round 12: a defender of a threatened own Core or Guardian defends (5) instead of gathering (a base race)
  Intent guardIntent;
  const bool guardDefends = !guardIn && sp.threatened && Has(sp.defenders, me.id);
  const bool guardOn = !guardDefends && GuardianIntent(w, me, m, &guardIntent);
  if (guardOn && !guardIn && now - m.hitByAt > t_.threatS) return Done(w, me, m, State::kPush, guardIntent, inBase);

  // 3. Fight
  if (const Shaper* target = FightTarget(w, me, m)) {
    int crowd = 0;
    for (const Shaper& e : w.shapers)
      if (e.team == target->team && e.id != target->id && Seen(e, me.team) &&
          Dist(e.pos, target->pos) <= t_.ultCrowdRange)
        ++crowd;
    const bool ult = target->Frac() <= t_.ultHp || crowd >= t_.ultCrowd;
    m.farmTarget = 0;
    return Done(w, me, m, State::kFight, AttackIt(target->id, true, ult, "fight"), inBase);
  }

  if (guardOn) return Done(w, me, m, State::kPush, guardIntent, inBase);  // round 11

  // 4. Recall to shop
  if (!inBase && WantsGoldRecall(w, me, m) && RecallAllowed(w, me, m)) {
    m.recallAt = now;
    m.farmTarget = 0;
    return Finish(m, State::kRecall, Plain(Act::kRecall, "gold: recall to shop"), now, inBase);
  }

  // 5. Defend
  Intent in;
  if (DefendIntent(w, me, m, &in)) return Done(w, me, m, State::kDefend, in, inBase);

  // 5b. Wells (BOTS2c): a defence of an own well, the team's well task
  if (WellIntent(w, me, m, &in)) return Done(w, me, m, State::kWell, in, inBase);
  // 5c. The parasite (BOTS2c): the team's attempt
  if (ParasiteIntent(w, me, m, &in)) return Done(w, me, m, State::kParasite, in, inBase);

  // 6. The macro task
  const TeamPlan& plan = Plan(me.team);
  if (me.role == kRoleHunter && now < t_.hunterJoinS && !plan.push && me.level >= t_.hunterJungleLevel &&
      JungleIntent(w, me, m, &in))
    return Done(w, me, m, State::kJungle, in, inBase);
  if (PushIntent(w, me, m, &in)) return Done(w, me, m, State::kPush, in, inBase);
  int lane = plan.push && plan.pushLane ? plan.pushLane : me.lane;
  if (!w.Lane(lane) && !w.lanes.empty()) lane = gms::bots::ClosestLane(w.lanes, me.pos);
  in = LaneIntent(w, me, m, lane, plan.push);
  return Done(w, me, m, plan.push ? State::kPush : State::kLane, in, inBase);
}

}  // namespace gms::bots2
