// gms/core/bots2 (BOTSFLIP): the walk watchdog of the host's executor, engine-free. The executor asks a walk once per
// goal (a goal that moved less than reaimDist keeps it: every request restarts the follow leaf). The game's follow can
// end short of its goal (`Behavior_Follow.lua`: `nThread.WaitForNearGoal` false returns from the behaviour, and the
// Pursue leaf stays current with nothing running), and then nothing asked again: in the user's VPS match of 2026-10-31
// two bots stood 171 s in "leaf 12 Pursue" at (-106, -4) with the goal (136.8, 136.4) unchanged. The watchdog asks such a
// walk again: the bot moved less than walkStuckMove in walkStuckS while it is farther than walkStuckNear from its goal.
// Header-only, no game addresses.
#pragma once

#include "core/bots2/tuning.h"
#include "core/bots2/world.h"

namespace gms::bots2 {

struct WalkWatch {
  Vec3 goal;
  Vec3 anchor;            // where the bot stood at anchorAt
  double anchorAt = -1e9;
  double askedAt = -1e9;  // the last walk asked (a new goal or a re-aim)
  Vec3 askedFrom;         // ... and where the bot stood then
  unsigned restarts = 0;  // this walk's asks again
  bool on = false;
};

// A new walk to `goal` was asked with the bot at `pos`.
inline void WalkStart(WalkWatch& w, const Vec3& goal, const Vec3& pos, double now) {
  w.goal = goal;
  w.anchor = pos;
  w.anchorAt = now;
  w.askedAt = now;
  w.askedFrom = pos;
  w.restarts = 0;
  w.on = true;
}

// BOTSFLIP round 2 (botsflip_b1: a bot stood 79 s while its goal jumped 34 units with every think, each re-aim
// restarting the follow before it had made a step): a re-aim of a running walk waits until the bot has moved
// walkStuckMove since the last ask or walkReaimMinS has passed. True: re-aim now.
inline bool WalkReaimNow(const WalkWatch& w, const Vec3& pos, double now, const Tuning& t) {
  if (!w.on) return true;
  return Dist(pos, w.askedFrom) >= t.walkStuckMove || now - w.askedAt >= t.walkReaimMinS;
}

// The same walk goes on (the executor didn't ask it again): true when it should be asked again now (the watchdog then
// waits walkStuckS again before the next).
inline bool WalkStuck(WalkWatch& w, const Vec3& pos, double now, const Tuning& t) {
  if (!w.on) return false;
  if (Dist(pos, w.anchor) >= t.walkStuckMove) {  // it moves
    w.anchor = pos;
    w.anchorAt = now;
    return false;
  }
  if (now - w.anchorAt < t.walkStuckS || Dist(pos, w.goal) <= t.walkStuckNear) return false;
  w.anchorAt = now;
  w.askedAt = now;
  w.askedFrom = pos;
  ++w.restarts;
  return true;
}

}  // namespace gms::bots2
