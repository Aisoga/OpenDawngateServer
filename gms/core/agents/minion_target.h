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
