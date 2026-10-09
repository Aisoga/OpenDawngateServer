// gms/core/bots (D6b-shims): lanes for the bots without the engine: the lane polylines, the closest lane, each team's
// front on a lane (nBot.GetLaneCenterAndDirection) and the bots' lane assignments (nBot.AssignLane / GetLaneID).
// Header-only, no game addresses.
//
// The lanes are the minion splines of SM_Map_v5 (MATCH_FLOW.md §2.4: four splines of 10 knots, `blue_top`/`red_top` =
// Top (1), `blue_midTop`/`red_midTop` = Bottom (2); a lane's two splines mirror each other). The host gives each lane one
// polyline from the Mortal (team 1) base to the Spirit (team 2) base, e.g. the team-1 spline (D1c-live already loads
// them into the engine's spline-path map).
//
// What the callers expect from GetLaneCenterAndDirection(lane, team) [script]: nBotUtils.GetLanePosition stands a bot
// 12-15 units *behind* the centre (centre + dir * -random(12, 15)), condition_Bot_GroupedWithAllies counts allies within
// 60 of it (searchRadiusClashPoint), ability_Bot_AssignToThreatenedLane / condition_Bot_EnemiesThreateningBase call the
// lane whose centre is nearest to the own Guardian threatened. So the centre is the lane's current clash point for that
// team and the direction points along the lane toward the enemy base. The rule here [assumed]: the midpoint between the
// team's most advanced minion and the enemy's least advanced one on that lane (each measured from the team's own base);
// only one side's minions -> that side's front minion; none -> `defaultFraction` of the lane (its middle, where the
// first waves meet at match ~93 s).
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <map>
#include <vector>

#include "core/bots/missions.h"  // Vec3, Distance

namespace gms::bots {

// nLaneID (Lua/GlobalDefinitions). GameModeHandlers' nLaneIndex is Top 0, Bottom 1 instead.
enum LaneId : int { kLaneNone = 0, kLaneTop = 1, kLaneBottom = 2 };

inline float Distance2D(float ax, float ay, float bx, float by) {
  const float dx = ax - bx, dy = ay - by;
  return std::sqrt(dx * dx + dy * dy);
}

// One lane's polyline, from the Mortal (team 1) base to the Spirit (team 2) base.
struct LanePath {
  int lane = kLaneNone;
  std::vector<Vec3> knots;

  float Length() const {
    float s = 0.f;
    for (size_t i = 1; i < knots.size(); ++i) s += Distance2D(knots[i - 1].x, knots[i - 1].y, knots[i].x, knots[i].y);
    return s;
  }
  // The point's projection onto the polyline (2-D): the arc length from knot 0 of the nearest point, and (optionally)
  // the distance to it. 0 and a huge distance without two knots.
  float Project(const Vec3& p, float* distance = nullptr) const {
    float bestS = 0.f, bestD = 3.0e38f, s0 = 0.f;
    for (size_t i = 1; i < knots.size(); ++i) {
      const Vec3& a = knots[i - 1];
      const Vec3& b = knots[i];
      const float ex = b.x - a.x, ey = b.y - a.y;
      const float len2 = ex * ex + ey * ey;
      const float len = std::sqrt(len2);
      float t = len2 > 0.f ? ((p.x - a.x) * ex + (p.y - a.y) * ey) / len2 : 0.f;
      t = std::min(1.f, std::max(0.f, t));
      const float d = Distance2D(p.x, p.y, a.x + t * ex, a.y + t * ey);
      if (d < bestD) {
        bestD = d;
        bestS = s0 + t * len;
      }
      s0 += len;
    }
    if (distance) *distance = bestD;
    return bestS;
  }
  // The point at arc length s (clamped to the lane); z is interpolated too.
  Vec3 PointAt(float s) const {
    if (knots.empty()) return Vec3();
    if (s <= 0.f || knots.size() == 1) return knots.front();
    for (size_t i = 1; i < knots.size(); ++i) {
      const Vec3& a = knots[i - 1];
      const Vec3& b = knots[i];
      const float len = Distance2D(a.x, a.y, b.x, b.y);
      if (s <= len && len > 0.f) {
        const float t = s / len;
        return MakeVec(a.x + t * (b.x - a.x), a.y + t * (b.y - a.y), a.z + t * (b.z - a.z));
      }
      s -= len;
    }
    return knots.back();
  }
  // The unit direction along the lane at arc length s, toward the Spirit end (2-D, z = 0).
  Vec3 DirectionAt(float s) const {
    if (knots.size() < 2) return Vec3();
    float acc = 0.f;
    size_t seg = knots.size() - 1;
    for (size_t i = 1; i < knots.size(); ++i) {
      const float len = Distance2D(knots[i - 1].x, knots[i - 1].y, knots[i].x, knots[i].y);
      if (s <= acc + len) {
        seg = i;
        break;
      }
      acc += len;
    }
    const float dx = knots[seg].x - knots[seg - 1].x, dy = knots[seg].y - knots[seg - 1].y;
    const float n = std::sqrt(dx * dx + dy * dy);
    return n > 0.f ? MakeVec(dx / n, dy / n, 0.f) : Vec3();
  }
};

// nBot.GetClosestLane(obj): the lane whose polyline is nearest (2-D) to p; kLaneNone without lanes. Ties keep the
// first lane given.
inline int ClosestLane(const std::vector<LanePath>& lanes, const Vec3& p, float* distance = nullptr) {
  int best = kLaneNone;
  float bestD = 3.0e38f;
  for (const LanePath& l : lanes) {
    float d = 0.f;
    l.Project(p, &d);
    if (d < bestD) {
      bestD = d;
      best = l.lane;
    }
  }
  if (distance) *distance = bestD;
  return best;
}

// SM_Map_v5 without lane data: the map is mirrored across y = x, the Top lane runs through the half with y > x (every
// knot of `blue_top` has y - x between 20.8 and 135.1; every Top-lane Binding has y - x >= 47.5), the Bottom lane
// through the other half. The Lua shims use the same rule as their fallback (dgsim_bots_api.lua).
inline int LaneByDiagonal(float x, float y) { return y - x >= 0.f ? kLaneTop : kLaneBottom; }

// A minion for the front: its team and position (the host passes the living lane minions: NPC type melee/ranged/super).
struct Unit {
  int team = 0;
  Vec3 pos;
};

struct FrontConfig {
  float laneWidth = 20.f;        // a minion counts for a lane within this distance of its polyline [assumed]
  float defaultFraction = 0.5f;  // the front without minions, as a fraction of the lane from the team's base [assumed]
};

// One team's front on one lane: what nBot.GetLaneCenterAndDirection(lane, team) returns.
struct Front {
  Vec3 center;
  Vec3 direction;        // unit, along the lane toward the enemy base
  float progress = 0.f;  // arc length of `center` from the team's own base
  int own = 0, enemy = 0;  // the minions that counted
};

// The front of `team` (1 or 2) on `lane` [assumed rule, header comment].
inline Front ComputeFront(const LanePath& lane, int team, const std::vector<Unit>& minions,
                          const FrontConfig& cfg = FrontConfig()) {
  Front f;
  const float length = lane.Length();
  float ownMax = -1.f, enemyMin = 3.0e38f;
  for (const Unit& u : minions) {
    if (u.team != 1 && u.team != 2) continue;
    float d = 0.f;
    const float s = lane.Project(u.pos, &d);
    if (d > cfg.laneWidth) continue;
    const float fromOwnBase = team == 1 ? s : length - s;
    if (u.team == team) {
      ++f.own;
      ownMax = std::max(ownMax, fromOwnBase);
    } else {
      ++f.enemy;
      enemyMin = std::min(enemyMin, fromOwnBase);
    }
  }
  float p = cfg.defaultFraction * length;
  if (f.own && f.enemy) {
    p = 0.5f * (ownMax + enemyMin);
  } else if (f.own) {
    p = ownMax;
  } else if (f.enemy) {
    p = enemyMin;
  }
  p = std::min(length, std::max(0.f, p));
  f.progress = p;
  const float s = team == 1 ? p : length - p;
  f.center = lane.PointAt(s);
  Vec3 d = lane.DirectionAt(s);
  if (team != 1) d = MakeVec(-d.x, -d.y, 0.f);
  f.direction = d;
  return f;
}

// The fronts of both teams on every lane, recomputed by the host (about twice a second [assumed]) from the living lane
// minions, so the frequent GetLaneCenterAndDirection calls only read them.
class LaneFronts {
 public:
  void SetLanes(const std::vector<LanePath>& lanes) {
    lanes_ = lanes;
    fronts_.clear();
  }
  const std::vector<LanePath>& Lanes() const { return lanes_; }
  void Update(const std::vector<Unit>& minions, const FrontConfig& cfg = FrontConfig()) {
    fronts_.clear();
    for (const LanePath& l : lanes_)
      for (int team = 1; team <= 2; ++team) fronts_[Key(l.lane, team)] = ComputeFront(l, team, minions, cfg);
  }
  // False for an unknown lane or team (the shim then falls back to its static centre).
  bool Get(int lane, int team, Front* out) const {
    auto it = fronts_.find(Key(lane, team));
    if (it == fronts_.end()) return false;
    *out = it->second;
    return true;
  }
  int Closest(const Vec3& p) const { return ClosestLane(lanes_, p); }

 private:
  static int Key(int lane, int team) { return lane * 16 + team; }
  std::vector<LanePath> lanes_;
  std::map<int, Front> fronts_;
};

// nBot.AssignLane(bot, lane) / GetLaneID(bot): the lane each bot was told to hold. Team fill gives the first ones
// (teamfill.h); ability_Bot_AssignToSiegeLane / _AssignToThreatenedLane / _GroupInLane change them later [script].
class BotLanes {
 public:
  void Assign(std::uint32_t bot, int lane) {
    if (lane == kLaneTop || lane == kLaneBottom)
      lanes_[bot] = lane;
    else
      lanes_.erase(bot);
  }
  // kLaneNone for an object that isn't a bot or has no lane (the shim then uses the object's position).
  int LaneOf(std::uint32_t bot) const {
    auto it = lanes_.find(bot);
    return it == lanes_.end() ? kLaneNone : it->second;
  }
  void Forget(std::uint32_t bot) { lanes_.erase(bot); }
  // How many of `bots` hold `lane`.
  int CountOn(const std::vector<std::uint32_t>& bots, int lane) const {
    int n = 0;
    for (std::uint32_t b : bots) n += LaneOf(b) == lane ? 1 : 0;
    return n;
  }

 private:
  std::map<std::uint32_t, int> lanes_;
};

}  // namespace gms::bots
