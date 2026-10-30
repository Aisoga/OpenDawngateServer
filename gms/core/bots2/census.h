// gms/core/bots2 (BOTS2): the census of the bots, for comparing two brains on the same measure. The host samples every
// bot once a second under either brain (classic or v2): alive, in its own base, its position, gold, XP and level, and
// the moments it dealt damage. An **idle second** is a second a living bot spent away from its base without moving
// `idleMove` units and without dealing damage for `idleHitS`. Header-only, no game addresses.
#pragma once

#include <cstdint>
#include <map>

#include "core/bots2/world.h"

namespace gms::bots2 {

struct CensusBot {
  int team = 0;
  bool init = false;
  bool wasAlive = false;
  Vec3 lastPos;
  double lastSample = 0.0;
  double lastHitAt = -1e9;  // when it last dealt damage
  double aliveS = 0.0, awayS = 0.0, idleS = 0.0;
  double idleRunS = 0.0, idleLongestS = 0.0;  // the current and the longest unbroken idle stretch
  int deaths = 0;
  float gold = 0.f, xp = 0.f;  // the last sample's total gold and XP
  int level = 1;
};

class Census {
 public:
  // The bot `id` dealt damage at `now` (match seconds).
  void NoteHit(std::uint32_t id, double now) {
    const auto it = bots_.find(id);
    if (it != bots_.end()) it->second.lastHitAt = now;
    else pendingHit_[id] = now;
  }

  // One sample (about once a second). The first sample of a bot only starts it. `busy`: an ability holds the bot now
  // (a cast, the recall's channel), or (BOTS2c, brain.h InWellAction) it stands in a well's ring capturing or
  // contesting it: an action, not an idle second.
  void Sample(std::uint32_t id, int team, bool alive, bool inBase, const Vec3& pos, double now, float gold, float xp,
              int level, float idleMove, float idleHitS, bool busy = false) {
    CensusBot& b = bots_[id];
    b.team = team;
    b.gold = gold;
    b.xp = xp;
    b.level = level;
    const auto ph = pendingHit_.find(id);
    if (ph != pendingHit_.end()) {
      if (ph->second > b.lastHitAt) b.lastHitAt = ph->second;
      pendingHit_.erase(ph);
    }
    if (!b.init) {
      b.init = true;
      b.wasAlive = alive;
      b.lastPos = pos;
      b.lastSample = now;
      return;
    }
    double dt = now - b.lastSample;
    if (dt < 0.0) dt = 0.0;
    if (dt > 2.0) dt = 2.0;  // a gap (the host stalled) counts at most two seconds
    if (b.wasAlive && !alive) {
      ++deathsTotal_;
      ++b.deaths;
    }
    if (alive && b.wasAlive) {
      b.aliveS += dt;
      if (!inBase) {
        b.awayS += dt;
        const bool still = Dist(pos, b.lastPos) < idleMove;
        const bool hitting = now - b.lastHitAt <= idleHitS;
        if (still && !hitting && !busy) {
          b.idleS += dt;
          b.idleRunS += dt;
          if (b.idleRunS > b.idleLongestS) b.idleLongestS = b.idleRunS;
        } else {
          b.idleRunS = 0.0;
        }
      } else {
        b.idleRunS = 0.0;
      }
    } else {
      b.idleRunS = 0.0;
    }
    b.wasAlive = alive;
    b.lastPos = pos;
    b.lastSample = now;
  }

  const std::map<std::uint32_t, CensusBot>& Bots() const { return bots_; }
  int Deaths() const { return deathsTotal_; }
  double IdleSeconds() const {
    double s = 0.0;
    for (const auto& kv : bots_) s += kv.second.idleS;
    return s;
  }
  double AwaySeconds() const {
    double s = 0.0;
    for (const auto& kv : bots_) s += kv.second.awayS;
    return s;
  }

 private:
  std::map<std::uint32_t, CensusBot> bots_;
  std::map<std::uint32_t, double> pendingHit_;
  int deathsTotal_ = 0;
};

}  // namespace gms::bots2
