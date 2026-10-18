// gms/core/agents (WELLDEF): which threatened spirit well of its team a bot answers, and when it gives up. Engine-free;
// the host (gms/engine/agents.cpp, BotReflexStep) measures the wells (gms/engine/wells.cpp, Threats) and the shapers
// around them, and carries the decision out through BOTREFLEX's defend rule (reflex.h, rule 3: fight whoever is at the
// well, else walk there).
//
// The user (2026-10-18): Tacticians, Predators and Hunters "should also try to defend their spirit wells a bit more
// seriously". Before, BOTREFLEX's rule took Hunters and Predators only, and a well counted as threatened only while its
// capture moved or a worker had been hit in the last 5 s: a defender that reached the ring (contested: the capture
// stops) or had hit every capturer (paused) lost the threat at once and went back to its plan while the capturers stayed;
// nothing weighed the distance against the capture's time, the numbers at the well or the bot's own fight. The data's
// own DefendWell mission (Tacticians and the Warden's AI: an enemy within 20 of the well, 5-s timeout) stays as it was.
//
// Rules (thresholds [assumed]):
//   - who: Tacticians within rangeTactician of the well, Hunters and Predators within rangeJungler (BOTREFLEX's 220),
//     the well on the bot's half of the map (LaneByDiagonal of well and bot agree, as BOTREFLEX);
//   - the threat: enemy shapers in the well's capture ring (moving, contested or paused alike), a capture that moves, or
//     a worker an enemy shaper (or its pet) hit in the last 5 s; a threat seen stays for memoryMs (fog flicker, deaths);
//   - in time: a moving capture the bot would reach (walkSpeed, from crewRange out) only after it completes, plus
//     arriveSlackS, is not taken up;
//   - its own fight first: an enemy shaper the bot fights (its target or its attacker) that is nearer than the well and
//     not itself at the well;
//   - numbers: not when the visible enemies at the well outnumber the bot and the allies there by more than outnumberBy;
//   - health: starts at startHp or more, keeps on down to keepHp (BOTREFLEX's weakAbsolute);
//   - give up: outnumbered at the well, under keepHp, not at the well maxWalkMs after it set out, or still on it
//     maxDefendMs after it set out -> that well is left for cooldownMs; the threat gone for memoryMs -> done; its own
//     fight -> done (it may come back after the fight).
#pragma once

#include <cmath>

#include "core/bots/lanes.h"

namespace gms {
namespace agents {

constexpr int kWellRoleTactician = 1;  // RolesTuning.role[] index (mRole)
constexpr int kWellRoleHunter = 2;
constexpr int kWellRolePredator = 3;

struct WellDefConfig {
  float rangeTactician = 120.f;  // a Tactician answers a well this far away (2-D units)
  float rangeJungler = 220.f;    // a Hunter or Predator (BOTREFLEX's kWellSideReach)
  bool sameSide = true;          // the well on the bot's half of the map only
  float crewRange = 30.f;        // shapers within this of the well's centre are at the well (BOTREFLEX's fight radius)
  float walkSpeed = 7.f;         // a shaper's walk, units per second, for the in-time test
  float arriveSlackS = 3.f;      // ... the capture may complete this much before the bot arrives
  int outnumberBy = 1;           // enemies at the well > defenders (allies there + the bot) + this: outnumbered
  float startHp = 0.45f;         // HP fraction to set out
  float keepHp = 0.35f;          // ... to stay on it (BOTREFLEX's weakAbsolute)
  long long memoryMs = 4000;     // a threat seen stays this long
  long long maxWalkMs = 35000;   // not at the well this long after setting out: given up (220 units at 7/s = 31 s)
  long long maxDefendMs = 60000; // a defence lasts at most this long (e.g. an attacker it never sees, hitting workers)
  long long cooldownMs = 20000;  // a well given up is left this long
};

// One threatened well of the bot's team, as the host measured it.
struct WellThreatView {
  int well = -1;                   // the host's well index
  float pos[2] = {};               // its centre
  bool captureMoving = false;      // the capture moved forward in the last tick
  double secondsToCapture = -1.0;  // while it moves: seconds to completion at its speed
  int capturers = 0;               // enemy shapers in the capture ring (the capture's own count: visible or not)
  bool workerHit = false;          // an enemy shaper (or its pet) hit one of its workers in the last 5 s
  int enemiesNear = 0;             // visible enemy shapers within crewRange of the centre
  int alliesNear = 0;              // allied shapers within crewRange of the centre, the bot not counted
};

struct WellDefInputs {
  int role = 0;                  // mRole
  float hpFrac = 1.f;
  float pos[2] = {};
  bool ownFight = false;         // the bot fights an enemy shaper (its target, or its attacker of the last seconds)
  float fightPos[2] = {};        // ... standing there
  const WellThreatView* threats = nullptr;
  int count = 0;
  long long nowMs = 0;
};

// Per bot, kept by the host between thinks.
struct WellDefState {
  int well = -1;                // the well it defends (-1: none)
  float pos[2] = {};            // its centre
  long long sinceMs = 0;        // set out at
  long long lastThreatMs = 0;   // the threat last seen
  bool arrived = false;         // it came within crewRange
  int leftWell = -1;            // the well it gave up last
  long long leftUntilMs = 0;    // ... left until then
};

struct WellDefDecision {
  bool defend = false;          // walk to / fight at `pos`
  int well = -1;
  float pos[2] = {};
  bool started = false;         // set out this think
  const char* ended = nullptr;  // a defence ended this think: "safe", "low hp", "outnumbered", "too slow", "too long",
                                // "own fight"
};

inline float WellDist2D(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

inline float WellDefRange(int role, const WellDefConfig& c) {
  if (role == kWellRoleTactician) return c.rangeTactician;
  if (role == kWellRoleHunter || role == kWellRolePredator) return c.rangeJungler;
  return 0.f;
}

inline bool WellOutnumbered(const WellThreatView& t, const WellDefConfig& c) {
  return t.enemiesNear > t.alliesNear + 1 + c.outnumberBy;
}

// Its own fight comes first: the enemy it fights is nearer than the well and not at the well.
inline bool WellOwnFightFirst(const WellDefInputs& in, const float* wellPos, float wellDist, const WellDefConfig& c) {
  if (!in.ownFight) return false;
  return WellDist2D(in.pos, in.fightPos) < wellDist && WellDist2D(in.fightPos, wellPos) > c.crewRange;
}

// A moving capture completes before the bot could reach the well.
inline bool WellTooLate(const WellThreatView& t, float dist, const WellDefConfig& c) {
  if (!t.captureMoving || t.secondsToCapture < 0.0 || !(c.walkSpeed > 0.f)) return false;
  const float walk = dist > c.crewRange ? dist - c.crewRange : 0.f;
  return static_cast<double>(walk / c.walkSpeed) > t.secondsToCapture + static_cast<double>(c.arriveSlackS);
}

inline const WellThreatView* FindWellThreat(const WellDefInputs& in, int well) {
  for (int i = 0; i < in.count && in.threats; ++i)
    if (in.threats[i].well == well) return &in.threats[i];
  return nullptr;
}

// One think: keep, end or start a well's defence.
inline WellDefDecision StepWellDefense(const WellDefInputs& in, WellDefState& st, const WellDefConfig& c = WellDefConfig()) {
  WellDefDecision d;
  const float range = WellDefRange(in.role, c);
  if (!(range > 0.f)) {
    st.well = -1;
    return d;
  }
  if (st.well >= 0) {  // the defence under way
    const WellThreatView* t = FindWellThreat(in, st.well);
    if (t) {
      st.lastThreatMs = in.nowMs;
      st.pos[0] = t->pos[0];
      st.pos[1] = t->pos[1];
    }
    const float dist = WellDist2D(in.pos, st.pos);
    if (dist <= c.crewRange) st.arrived = true;
    const char* why = nullptr;
    bool leave = false;  // given up: the well is left for cooldownMs
    if (!t && in.nowMs - st.lastThreatMs > c.memoryMs) {
      why = "safe";
    } else if (in.hpFrac < c.keepHp) {
      why = "low hp";
      leave = true;
    } else if (t && WellOutnumbered(*t, c)) {
      why = "outnumbered";
      leave = true;
    } else if (!st.arrived && in.nowMs - st.sinceMs > c.maxWalkMs) {
      why = "too slow";
      leave = true;
    } else if (in.nowMs - st.sinceMs > c.maxDefendMs) {
      why = "too long";
      leave = true;
    } else if (WellOwnFightFirst(in, st.pos, dist, c)) {
      why = "own fight";
    }
    if (!why) {
      d.defend = true;
      d.well = st.well;
      d.pos[0] = st.pos[0];
      d.pos[1] = st.pos[1];
      return d;
    }
    if (leave) {
      st.leftWell = st.well;
      st.leftUntilMs = in.nowMs + c.cooldownMs;
    }
    d.ended = why;
    d.well = st.well;
    st.well = -1;
    return d;
  }
  if (in.hpFrac < c.startHp) return d;
  const WellThreatView* best = nullptr;
  float bestDist = 0.f;
  for (int i = 0; i < in.count && in.threats; ++i) {
    const WellThreatView& t = in.threats[i];
    if (t.well == st.leftWell && in.nowMs < st.leftUntilMs) continue;
    const float dist = WellDist2D(in.pos, t.pos);
    if (dist > range) continue;
    if (c.sameSide && gms::bots::LaneByDiagonal(t.pos[0], t.pos[1]) != gms::bots::LaneByDiagonal(in.pos[0], in.pos[1]))
      continue;
    if (WellOutnumbered(t, c) || WellTooLate(t, dist, c) || WellOwnFightFirst(in, t.pos, dist, c)) continue;
    if (!best || dist < bestDist) {
      best = &t;
      bestDist = dist;
    }
  }
  if (!best) return d;
  st.well = best->well;
  st.pos[0] = best->pos[0];
  st.pos[1] = best->pos[1];
  st.sinceMs = in.nowMs;
  st.lastThreatMs = in.nowMs;
  st.arrived = bestDist <= c.crewRange;
  d.defend = true;
  d.started = true;
  d.well = best->well;
  d.pos[0] = best->pos[0];
  d.pos[1] = best->pos[1];
  return d;
}

}  // namespace agents
}  // namespace gms
