// BACKTOACTION: a bot whose Pursue leaf walks to nothing, and the safety net for a
// bot lost on the map's centre. Engine-free; agents.cpp feeds it once per bot think.
//
// The cause (the user's practice match of 2026-10-20, the Warden Tactician 208 s on (0, 0)): the Pursue leaf (12) runs
// nBehavior_Follow, whose loop re-reads the blackboard's target position (+0x1E64, nAgent.GetTargetPosition) on every
// pass and, with no object (the request block's target kObjIDNone), walks to it within 0.5 and jitters 4-7 units
// around it while it can't get closer. D6d's point pursuit (a ward's spot) writes +0x1E64 and clears mTarget; when it
// ends (the ward placed, the bot chose otherwise) the host writes the zero vector back, which the Pursue leaf's scorer
// reads as "no point", but the running Follow loop doesn't re-score: it walks to the zero vector, the map's centre, and
// stays there until something restarts the tree's default behavior (a new phase's different default, a new point, a
// hit). The tree re-checks only the leaves above the running one, and nothing above Pursue was due.
//
// The fix (`[host] bot_point_follow_fix` 1): the first think after a point pursuit ended that finds the Pursue leaf
// still running restarts it (request 0x400), so the tree redecides (an object pursuit, the phase's default, idle).
// The safety net (`[host] bot_lost_ms` 10000, every bot): a Pursue leaf with neither mTarget nor a point pursuit for
// kNoTargetRestartMs is restarted the same way (any other way to a Follow without a goal); a bot in the Pursue or
// IdleDefault leaf without mTarget within `centreRadius` of the map's centre for `lostMs`, outside a parasite plan or
// phase, is walked to its lane's front (ability_Bot_MoveToTarget), at most once per `cooldownMs`.
#pragma once

#include <cmath>
#include <cstring>

namespace gms::agents {

struct LostOptions {
  bool pointFollowFix = true;    // [host] bot_point_follow_fix: restart a Pursue leaf that outlived its point pursuit
  int lostMs = 10000;            // [host] bot_lost_ms: the safety net's window (0 = off)
  int noTargetRestartMs = 2000;  // a Pursue leaf without mTarget or a point this long is restarted [assumed]
  float centreRadius = 20.f;     // "near the map's centre" (the parasite's lair is about 25 across) [assumed]
  int cooldownMs = 15000;        // one walk to the lane front per this long [assumed]
};

enum class LostLeaf { kOther, kPursue, kIdleDefault };

struct LostInput {
  unsigned long long nowMs = 0;
  float x = 0.f, y = 0.f;
  LostLeaf leaf = LostLeaf::kOther;
  bool hasTarget = false;     // the agent's raw mTarget is set
  bool pointPursuit = false;  // a D6d point pursuit is on (+0x1E64 holds its point)
  bool parasiteTask = false;  // the plan or the phase is about the parasite (its gather, contest, kill)
  bool held = false;          // an uninterruptible ability holds the agent (a restart would abort it)
};

enum class LostAction {
  kNone,
  kRestartPointFollow,  // the Pursue leaf outlived the point pursuit (the root fix)
  kRestartNoTarget,     // the safety net: a Pursue leaf with nothing to follow
  kWalkToFront,         // the safety net: lost on the centre
};

struct LostState {
  bool pendingPointFollow = false;  // a point pursuit ended; its Follow may still run
  // NULLPOS: the ended pursuit's point is still in +0x1E64 (EndPointPursuit leaves it there with the fix on), so a
  // Follow that still runs walks on to it, never to the zero vector; LostStep says when to write the zero vector back
  bool clearPending = false;
  unsigned long long noTargetSinceMs = 0, centreSinceMs = 0, lastWalkMs = 0;
  bool walked = false;  // lastWalkMs is set
};

// A point pursuit ended (D6d's EndPointPursuit): the Follow it started may still run. `pointKept`: the point is
// still in the blackboard (NULLPOS: cleared when LostStep says so, never under a running Follow).
inline void LostNotePointEnded(LostState& s, bool pointKept = false) {
  s.pendingPointFollow = true;
  s.clearPending = pointKept;
}
// A new point pursuit began: its point replaced the kept one.
inline void LostNotePointBegun(LostState& s) { s.clearPending = false; }

inline bool NearCentre(float x, float y, float radius) { return std::sqrt(x * x + y * y) <= radius; }

// One think. At most one action; the caller performs it (restart: request 0x400; walk: to the lane front).
// NULLPOS: `*clearPoint` true = write the zero vector into +0x1E64 now, before the restart (so the tree never redecides
// on the old point and no running Follow ever reads (0, 0)).
inline LostAction LostStep(LostState& s, const LostInput& in, const LostOptions& o, bool* clearPoint = nullptr) {
  const bool pursue = in.leaf == LostLeaf::kPursue;
  if (clearPoint) *clearPoint = false;
  // the root fix: the first think after the end that finds the Pursue leaf still running restarts it
  if (s.pendingPointFollow) {
    if (in.pointPursuit) {
      s.pendingPointFollow = false;  // a new point (D6d restarts the leaf itself; its point replaced the old one)
      s.clearPending = false;
    } else if (!pursue || !o.pointFollowFix) {
      s.pendingPointFollow = false;  // the leaf has changed: no Follow reads the point any more
      if (clearPoint) *clearPoint = s.clearPending;
      s.clearPending = false;
    } else if (!in.held) {
      s.pendingPointFollow = false;
      if (clearPoint) *clearPoint = s.clearPending;
      s.clearPending = false;
      s.noTargetSinceMs = 0;
      return LostAction::kRestartPointFollow;
    }
    // held: the restart waits, and the point stays (the Follow walks on to the old spot)
  }
  if (o.lostMs <= 0) {
    s.noTargetSinceMs = s.centreSinceMs = 0;
    return LostAction::kNone;
  }
  // the net, part 1: a Pursue leaf with nothing to follow
  if (pursue && !in.hasTarget && !in.pointPursuit) {
    if (!s.noTargetSinceMs) s.noTargetSinceMs = in.nowMs;
    if (!in.held && in.nowMs - s.noTargetSinceMs >= static_cast<unsigned long long>(o.noTargetRestartMs)) {
      s.noTargetSinceMs = 0;
      return LostAction::kRestartNoTarget;
    }
  } else {
    s.noTargetSinceMs = 0;
  }
  // the net, part 2: lost on the centre
  const bool lost = (pursue || in.leaf == LostLeaf::kIdleDefault) && !in.hasTarget && !in.pointPursuit &&
                    !in.parasiteTask && NearCentre(in.x, in.y, o.centreRadius);
  if (!lost) {
    s.centreSinceMs = 0;
    return LostAction::kNone;
  }
  if (!s.centreSinceMs) s.centreSinceMs = in.nowMs;
  if (in.held || in.nowMs - s.centreSinceMs < static_cast<unsigned long long>(o.lostMs)) return LostAction::kNone;
  if (s.walked && in.nowMs - s.lastWalkMs < static_cast<unsigned long long>(o.cooldownMs)) return LostAction::kNone;
  s.walked = true;
  s.lastWalkMs = in.nowMs;
  s.centreSinceMs = in.nowMs;
  return LostAction::kWalkToFront;
}

// A plan's or phase's name about the parasite (GatherForParasite, ContestParasite, KillParasite, FightParasite):
// standing near the centre there is the plan's own.
inline bool ParasiteTaskName(const char* name) { return name && std::strstr(name, "Parasite") != nullptr; }

}  // namespace gms::agents
