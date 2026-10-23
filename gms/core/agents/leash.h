// LEASH: how far a jungle creature or a spirit well's worker follows its target away from its home.
// The data gives jungle creatures tether 0, so the engine's default 22 applies (AgentBlackboard_GetTetherCircle
// 0x008AAB10: a circle around the object's initial position), and workers tether 15 around their spawn spot; the
// engine tests the tether against the TARGET's position (AgentBlackboard_IsInTether 0x008AB040), so a creep followed a
// kiting shaper until the shaper was 22 units from the creep's spot, and stood 22 + its reach from home before it let go.
// The stripped aggro update's own leash is unknown [assumed], so three limits of our own, measured on the NPC itself,
// on top of the tether:
//   - the own leash: an NPC farther than ownLimit from its home drops every target (the creep then walks home
//     untargetable with nBehavior_Defend, the worker goes back to its mining loop);
//   - the target leash: a target whose edge distance from the NPC's home exceeds ownLimit + targetMargin (the NPC's
//     reach from the edge of its leash) is dropped, and is not put on the list by a hit in the first place;
//   - the chase time: an NPC outside homeRadius that has not landed a hit on its target for chaseMs drops it (a target
//     that outruns it or circles it; an NPC that fights is not dropped by this);
// and the NPC's group (a creep's camp, a worker's well) leashes together: a target one member dropped by its own leash
// or the chase time is dropped by the others too and is not taken back for ignoreMs. A taunt holds.
// CAMPLEASH (2026-10-23, the players: creeps "attack 2-3 times and then walk back", "you have to be almost on top of
// them"): the chase time and the group leash ended fights against a target that stood in the camp (a bot match of
// CAMPFINISH: a Boar major 10.6 from its spot that didn't reach a Hunter 6.0 from it ran out its chase time, and the
// group leash pulled the medium fighting 0.9 from home along). With areaHolds (the default) a target inside the NPC's
// home radius (its edge distance from home) is dropped by neither, and a member that hit the target within its chase
// time keeps it when its group lets go; the own leash and the target leash still hold.
// Header-only and engine-free; gms_tests (test_agents.cpp) checks it.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <vector>

namespace gms {
namespace agents {

struct LeashOptions {
  bool enabled = true;
  float ownLimit = 15.f;          // the NPC's own distance from its home
  float targetMargin = 4.f;       // a target farther (edge distance) than ownLimit + this from home is dropped
  float homeRadius = 6.f;         // within this of home no chase clock runs
  std::int64_t chaseMs = 3000;    // outside homeRadius without a hit on its target this long: dropped (0 = no limit)
  std::int64_t ignoreMs = 2000;   // a target the group dropped is not taken back for this long
  bool areaHolds = true;          // CAMPLEASH: a target in the home radius (or one the NPC hits) outlasts chase and group
};

enum LeashDrop {
  kLeashKeep = 0,
  kLeashOwn = 1,      // the NPC is beyond its own leash
  kLeashChase = 2,    // the chase time ran out
  kLeashTarget = 3,   // the target is beyond the NPC's reach from its leash
  kLeashGroup = 4,    // the NPC's group dropped this target a moment ago
};

inline const char* LeashDropName(int why) {
  switch (why) {
    case kLeashOwn: return "own leash";
    case kLeashChase: return "chase time";
    case kLeashTarget: return "target beyond the leash";
    case kLeashGroup: return "group leash";
    default: return "kept";
  }
}

// The per-NPC state: the target it fights, since when it is outside its home radius in this fight, and its last hit.
struct LeashState {
  std::uint32_t target = 0;
  std::int64_t sinceMs = -1;
  std::int64_t outSinceMs = -1;  // -1: inside homeRadius
  std::int64_t lastHitMs = -1;   // the last hit it landed on `target`
  float targetEdge = -1.f;       // CAMPLEASH: the target's edge distance from home at the last update (-1 unknown)
};

// CAMPLEASH: whether a target at `targetEdgeDist` (its distance from the NPC's home minus its footprint; < 0 unknown)
// stands inside the NPC's area, its home radius.
inline bool LeashTargetInArea(const LeashOptions& o, float targetEdgeDist) {
  return o.areaHolds && targetEdgeDist >= 0.f && targetEdgeDist <= o.homeRadius;
}

// Whether the NPC keeps fighting at all (its own leash and the chase time). ownDist: its distance from home.
inline int LeashSelfCheck(const LeashOptions& o, const LeashState& s, float ownDist, std::int64_t nowMs) {
  if (!o.enabled) return kLeashKeep;
  if (ownDist > o.ownLimit) return kLeashOwn;
  if (o.chaseMs > 0 && s.target && s.outSinceMs >= 0 && ownDist > o.homeRadius &&
      !LeashTargetInArea(o, s.targetEdge)) {
    const std::int64_t ref = std::max(s.outSinceMs, s.lastHitMs);
    if (nowMs - ref >= o.chaseMs) return kLeashChase;
  }
  return kLeashKeep;
}

// Whether a target is beyond the NPC's reach from the edge of its leash. targetEdgeDist: the target's distance from the
// NPC's home minus its footprint radius.
inline bool LeashTargetBeyond(const LeashOptions& o, float targetEdgeDist) {
  return o.enabled && targetEdgeDist > o.ownLimit + o.targetMargin;
}

// After the update: a new best target starts a fight (and its clocks); outside homeRadius the chase clock runs.
// targetEdgeDist: the best target's edge distance from home (< 0 unknown).
inline void UpdateLeash(LeashState* s, const LeashOptions& o, std::uint32_t best, float ownDist, std::int64_t nowMs,
                        float targetEdgeDist = -1.f) {
  if (best != s->target) {
    s->target = best;
    s->sinceMs = best ? nowMs : -1;
    s->outSinceMs = -1;
    s->lastHitMs = -1;
  }
  s->targetEdge = best ? targetEdgeDist : -1.f;
  if (!best) return;
  if (ownDist > o.homeRadius && !LeashTargetInArea(o, s->targetEdge)) {  // CAMPLEASH: no clock while it's in the area
    if (s->outSinceMs < 0) s->outSinceMs = nowMs;
  } else {
    s->outSinceMs = -1;
  }
}

// The NPC landed a hit on `victim` (the damage hook): the chase clock of a fight against it starts over.
inline void NoteLeashHit(LeashState* s, std::uint32_t victim, std::int64_t nowMs) {
  if (s->target && victim == s->target) s->lastHitMs = nowMs;
}

// CAMPLEASH: whether a member keeps `target` although its group dropped it: the target stands in the member's area, or
// it is the member's own target and the member hit it within its chase time (it fights it, it doesn't chase it).
inline bool LeashGroupHolds(const LeashOptions& o, const LeashState& s, std::uint32_t target, float targetEdgeDist,
                            std::int64_t nowMs) {
  if (!o.areaHolds || !target) return false;
  if (LeashTargetInArea(o, targetEdgeDist)) return true;
  const std::int64_t window = o.chaseMs > 0 ? o.chaseMs : 3000;
  return s.target == target && s.lastHitMs >= 0 && nowMs - s.lastHitMs < window;
}

// The targets a group (a camp, a well's workers) dropped by a member's own leash or chase time, until when.
class LeashGroupDrops {
 public:
  void Drop(std::uint32_t group, std::uint32_t target, std::int64_t untilMs) {
    for (Entry& e : list_)
      if (e.group == group && e.target == target) {
        e.untilMs = std::max(e.untilMs, untilMs);
        return;
      }
    list_.push_back({group, target, untilMs});
  }
  bool Ignored(std::uint32_t group, std::uint32_t target, std::int64_t nowMs) const {
    for (const Entry& e : list_)
      if (e.group == group && e.target == target && nowMs < e.untilMs) return true;
    return false;
  }
  void Expire(std::int64_t nowMs) {
    list_.erase(std::remove_if(list_.begin(), list_.end(), [nowMs](const Entry& e) { return nowMs >= e.untilMs; }),
                list_.end());
  }
  std::size_t Size() const { return list_.size(); }
  void Clear() { list_.clear(); }

 private:
  struct Entry {
    std::uint32_t group = 0, target = 0;
    std::int64_t untilMs = 0;
  };
  std::vector<Entry> list_;
};

// The worker's platform: the farthest worker spot from the well's centre (the spots are the pad's outer ring).
inline float PlatformRadius(const float* centre, const float* spots, int n) {
  float r = 0.f;
  for (int i = 0; i < n; ++i) {
    const float dx = spots[3 * i] - centre[0], dy = spots[3 * i + 1] - centre[1];
    r = std::max(r, dx * dx + dy * dy);
  }
  return r > 0.f ? std::sqrt(r) : 0.f;
}

}  // namespace agents
}  // namespace gms
