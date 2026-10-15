// MINTARGET: which shapers a LANE MINION may target.
// The original server's rules were compiled out; the spec is the user's description (2026-10-10):
//   - towers/structures and enemy minions always come before enemy shapers;
//   - a shaper is a target only if there is no other valid target in the minion's range, or if an enemy shaper is
//     attacking (has damaged within a short window) an own-team shaper that is within the minion's range;
//   - once the shaper is far enough away the minion drops it and goes back to its lane (Behavior_Assault restarts when
//     the aggro list is empty).
// Header-only and engine-free; gms_tests (test_agents.cpp) checks it.
#pragma once

#include <cstdint>
#include <unordered_map>

namespace gms {
namespace agents {

struct MinionTargetOptions {
  bool enabled = true;
  float leashRange = 25.f;             // a shaper farther (edge distance) than this is dropped (aggro 20 + 5 hysteresis)
  std::int64_t defendWindowMs = 2500;  // an own shaper counts as attacked this long after the enemy shaper's last hit
};

// Whether an aggro-list shaper stays on a lane minion's list.
//   otherInRange: some other valid non-shaper target lies within the minion's aggro range (edge distance)
//   defends:      the shaper hit an own-team shaper that is within the minion's range, within the window
// A taunt holds.
inline bool ShaperAllowed(const MinionTargetOptions& o, bool taunting, float distance, bool otherInRange, bool defends) {
  if (!o.enabled || taunting) return true;
  if (distance > o.leashRange) return false;  // leash: back to the lane
  return !otherInRange || defends;
}

// MINJUNGLE (the user, 2026-10-15: minions follow shapers into the jungle and return to their jobs far too late): how
// long a lane minion chases a shaper. The data gives a minion a tether of 30 around the point of its lane segment
// nearest the target (AgentBlackboard_GetTetherCircle) and a drop range of 33 around itself, and MINTARGET added a
// leash of 25 around the minion: a minion is faster (9.56) than a walking shaper, catches it, hits it and follows
// again, so none of them ends a kite; measured in the host's kite sessions, a wave followed a shaper 10-15 s and up to
// 62 units along the lane, or 26 units into the jungle until the tether dropped it. The stripped aggro update's rule is
// unknown [assumed], so three limits of our own, on top of MINTARGET's:
//   - the chase distance: a minion that has moved farther than chaseDistance from where it took the shaper as its best
//     target drops it;
//   - the chase time: a minion that has had the shaper as its best target for chaseMs and has moved at least
//     chaseMinMove since (it follows the shaper) drops it; a shaper that stands and fights is not dropped by this;
//   - the lane leash: a minion that is itself farther than laneLeash from its lane drops its shapers (only while it
//     still walks its lane: a minion at the lane's end, in the enemy base, has no lane band);
// and a shaper dropped by any of them is not taken again on sight for ignoreMs (else the next update re-adds it within
// the aggro range and the chase starts over), unless it hits an own-team shaper in range (MINTARGET's defend rule).
// A taunt holds.
struct MinionChaseOptions {
  bool enabled = true;
  float chaseDistance = 20.f;    // from where the chase began (the minion's aggro range 20 [assumed])
  std::int64_t chaseMs = 3000;   // the shaper as the best target this long while following it ends it [assumed]
  float chaseMinMove = 6.f;      // "following": moved this far since the chase began [assumed]
  float laneLeash = 20.f;        // the minion's own distance from its lane [assumed]
  std::int64_t ignoreMs = 4000;  // a dropped shaper isn't taken again on sight this long [assumed]
};

enum MinionChaseDrop { kChaseKeep = 0, kChaseLaneLeash = 1, kChaseTime = 2, kChaseDistance = 3, kChaseIgnored = 4 };

// The per-minion state of the rule.
struct MinionChase {
  std::uint32_t target = 0;        // the shaper that is the best target now
  std::int64_t sinceMs = -1;       // since when
  float start[2] = {};             // where the minion was then
  std::uint32_t ignored = 0;       // the last shaper dropped by the rule ...
  std::int64_t ignoreUntilMs = 0;  // ... and until when it isn't taken again on sight
};

// Whether a shaper on a lane minion's list stays. onLane: the minion still walks its lane (a spline id);
// laneDistance: the minion's own distance from the nearest lane; pos: the minion's position.
inline int ShaperChaseCheck(const MinionChaseOptions& o, const MinionChase& s, std::uint32_t shaper, bool taunting,
                            bool defends, bool onLane, float laneDistance, const float* pos, std::int64_t nowMs) {
  if (!o.enabled || taunting) return kChaseKeep;
  if (onLane && laneDistance > o.laneLeash) return kChaseLaneLeash;
  if (s.target == shaper && s.sinceMs >= 0) {
    const float dx = pos[0] - s.start[0], dy = pos[1] - s.start[1], moved2 = dx * dx + dy * dy;
    if (moved2 > o.chaseDistance * o.chaseDistance) return kChaseDistance;
    if (nowMs - s.sinceMs >= o.chaseMs && moved2 >= o.chaseMinMove * o.chaseMinMove) return kChaseTime;
  }
  if (s.ignored == shaper && nowMs < s.ignoreUntilMs && !defends) return kChaseIgnored;
  return kChaseKeep;
}

// After a drop by the rule: the shaper is ignored on sight for a while, the chase ends.
inline void NoteChaseDrop(const MinionChaseOptions& o, MinionChase* s, std::uint32_t shaper, std::int64_t nowMs) {
  s->ignored = shaper;
  s->ignoreUntilMs = nowMs + o.ignoreMs;
  if (s->target == shaper) {
    s->target = 0;
    s->sinceMs = -1;
  }
}

// After the update: a chase begins when a shaper becomes the best target (time and place kept) and ends when the best
// target is something else or nothing.
inline void UpdateChase(MinionChase* s, std::uint32_t best, bool bestIsShaper, const float* pos, std::int64_t nowMs) {
  if (!best || !bestIsShaper) {
    s->target = 0;
    s->sinceMs = -1;
    return;
  }
  if (s->target != best || s->sinceMs < 0) {
    s->target = best;
    s->sinceMs = nowMs;
    s->start[0] = pos[0];
    s->start[1] = pos[1];
  }
}

// Recent shaper-on-shaper hits: victim id -> (attacker id, time of the hit).
class ShaperHitLog {
 public:
  void Record(std::uint32_t attacker, std::uint32_t victim, std::int64_t nowMs) { last_[victim] = {attacker, nowMs}; }
  // Did `attacker` hit `victim` within `windowMs` of `nowMs`?
  bool Attacked(std::uint32_t attacker, std::uint32_t victim, std::int64_t nowMs, std::int64_t windowMs) const {
    const auto it = last_.find(victim);
    return it != last_.end() && it->second.attacker == attacker && nowMs >= it->second.ms &&
           nowMs - it->second.ms <= windowMs;
  }
  void Clear() { last_.clear(); }

 private:
  struct Hit {
    std::uint32_t attacker = 0;
    std::int64_t ms = 0;
  };
  std::unordered_map<std::uint32_t, Hit> last_;
};

}  // namespace agents
}  // namespace gms
