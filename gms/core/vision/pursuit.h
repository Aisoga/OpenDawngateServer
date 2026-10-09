// gms/core/vision (D2g-pursuit): the per-team last-seen pursuit position, engine-free and header-only
// (docs/GMS_PROTOCOL.md *The last-seen pursuit position (D2g-pursuit)*).
//
// What the client build has (Ghidra, D2g-pursuit):
//   - GameObject_GetLastSeenPursuitPosition (0x008814B0; object, team, out position) returns the line-of-sight
//     record's (object +0x858) per-team pair {x at +0x2C + team x 8, y at +0x30 + team x 8}, z 0, whenever the object
//     has a record ("Only objects with line of sight Tracked object update a last seen pursuit position").
//   - Its only callers are the follow: Locomotion_FollowObject (0x0088FD50) takes it as the goal when the follower's
//     Agent_IsVisibleTo (0x008A9B90) says the target is hidden, and Locomotion_TickMovers (0x008A6CA0) refreshes the
//     goal from it while the follow's "target visible" byte (locomotion +0xC) is 0 and an automatic refresh is due.
//     Every server walk-up goes through that follow: nLocomotion.MoveToObject (a human's walk-up then cast,
//     nBehavior_MoveToRange; the agents' pursuit, nBehavior_Follow; the bots' missions, ability_Bot_*), the behavior
//     tree's FollowObject leaf and nLocomotion.FollowAsPet.
//   - Nothing in the client writes +0x2C..+0x43, and LineOfSight_AllocTracked (0x00917450) doesn't initialise it (it
//     clears +0x24, +0x28 and +0x44..+0x4C): the stripped visibility pass kept it. On the match host it stayed 0 (or a
//     pooled record's old value), so a walk-up on a target hidden at its start walked to the map origin (D11, G3).
//
// The rule the host writes after each tick's vision answer (VisionModel::Bits, mVisibleToTeam):
//   - every team that sees the object gets its current (x, y): its own team always, a never-fogged object every team;
//   - a team that doesn't see it keeps the value it had: the position where that team saw it last [inferred from
//     the name and the follow's use];
//   - a record the host meets for the first time (a new object, a record from nGameObject.SetGrantsVision, a pooled
//     record reused for another object) gets the object's current position for every team first [assumed: what the
//     original wrote for a team that never saw the object is unknown; this is its first position, usually its spawn
//     point, never the map origin or another object's old value].
#pragma once

#include <cstdint>
#include <unordered_map>

#include "core/vision/vision.h"

namespace gms::vision {

class LastSeenBook {
 public:
  // Starts a tick.
  void Begin() {
    now_.clear();
    fresh_ = 0;
  }

  // The teams (bits) whose last-seen position of object `id` (line-of-sight record `record`, any key that changes
  // when the record does) to set to its current position this tick, given the vision answer `seen` (kAllTeams for a
  // never-fogged object). 0: write nothing.
  uint8_t Teams(uint32_t id, std::uintptr_t record, uint8_t seen) {
    if (!id || !record) return 0;
    now_[id] = record;
    const auto it = last_.find(id);
    if (it == last_.end() || it->second != record) {
      ++fresh_;
      return kAllTeams;
    }
    return static_cast<uint8_t>(seen & kAllTeams);
  }

  // Ends the tick: objects that were not in it are forgotten (a later record for the same id counts as new).
  void End() { last_.swap(now_); }

  // Records met for the first time in this tick, and the records known after End().
  int Fresh() const { return fresh_; }
  size_t Known() const { return last_.size(); }

 private:
  std::unordered_map<uint32_t, std::uintptr_t> last_, now_;
  int fresh_ = 0;
};

}  // namespace gms::vision
