// gms/core/agents (BOTRECALL, BOTBACK; reworked in BOTTEAM): what a bot's "go home" gambit
// (ability_Bot_Back, 0xE695B80B: walk to the player's start point and wait there) turns into. Engine-free; the host
// (gms/engine/agents.cpp, BotBrainStep's request) measures the inputs and makes the request.
//
// The data uses ability_Bot_Back in two ways [data, re/out/assetdata/Phase.json]:
//   - in every class's Flee phase: "EnemiesNearby (30) -> Bot_Back", then "Bot_SafeRecall and !Bot_AtHomeBase ->
//     ability_returntobase", then "-> Bot_Back": walk away from enemies, recall once nothing hostile is within 60 (10
//     while a recall runs), else walk;
//   - in the lane and fight phases (LastHit, Harass, DontPush, SelfDefense, BackToAction, ...) on "an enemy shaper
//     close" or "too many minions on me": a step back from the fight. The walk to the start point has no end of its own
//     (MoveToPointExact + WaitForNearGoal), so the next think either requests another gambit (which aborts it) or nothing,
//     and then the bot walks all the way home (a test match: Gladiators at 70-90 % HP 150-230 units from their lane).
//
// Rules:
//   - Outside a Flee phase a Back gambit is a step of `backStep` toward the start point (a MoveToTarget request; the
//     brain thinks again after it); within backStep + backMargin of the start point the original short walk runs.
//   - In a Flee phase the walk becomes the shop's Return to Base (ability_returntobase 0x96B845C6: 8 s channel, then a
//     teleport home) once the bot is out of immediate danger: far from home, nothing hit it for quietMs, no enemy shaper
//     within enemyShaperRange, no enemy tower within towerRange, not on the parasite's ground. That is looser than the
//     data's own Bot_SafeRecall (any enemy NPC within 60), which made a fleeing bot walk the whole lane past the minions.
//   - A request of the recall that answers -0x270B ("the same ability still holds the agent") means the recall runs
//     already (ours, or the data's own recall gambit): it counts as started. It must not fall through to the walk: the
//     walk would abort the channel and the next think start it again (a test match: recall, walk, recall ... every 0.46 s).
#pragma once

#include <cmath>

namespace gms {
namespace agents {

struct RecallConfig {
  float enemyShaperRange = 45.f;  // no visible enemy shaper this close
  float towerRange = 40.f;        // no standing enemy tower this close (a Binding reaches about 28-34)
  float objectiveRange = 45.f;    // not on the parasite's ground (its kept aggro reaches 40)
  float minBaseDistance = 90.f;   // closer than this the walk is short enough
  long long quietMs = 4000;       // nothing hit the bot for this long (any hit ends the channel)
  float backStep = 30.f;          // BOTBACK: a lane phase's step toward the start point
  float backMargin = 5.f;         // ... unless the start point is within backStep + backMargin
};

struct RecallInputs {
  bool fleePhase = false;         // the bot's phase is a Flee phase (not FleeTowerTargeting)
  long long sinceHitMs = -1;      // since the last hit by anything hostile; -1 = never hit
  float nearestEnemyShaper = 1e9f;
  float nearestEnemyTower = 1e9f;
  float nearestObjective = 1e9f;  // the parasite (NPC type 8), alive
  float baseDistance = -1.f;      // to the player's start point; < 0 unknown
};

inline bool RecallAllowed(const RecallInputs& in, const RecallConfig& c) {
  if (!in.fleePhase) return false;
  if (in.baseDistance < 0.f || in.baseDistance < c.minBaseDistance) return false;
  if (in.sinceHitMs >= 0 && in.sinceHitMs < c.quietMs) return false;
  if (in.nearestEnemyShaper <= c.enemyShaperRange) return false;
  if (in.nearestEnemyTower <= c.towerRange) return false;
  if (in.nearestObjective <= c.objectiveRange) return false;
  return true;
}

// The recall request's result as the brain counts it: "the same ability still holds the agent" (-0x270B) is a recall
// that runs, i.e. started (1).
inline int RecallRequestResult(int result) { return result == -0x270B ? 1 : result; }

// BOTBACK: the goal of a lane phase's step toward the start point `base` from `from` (2-D, z kept); false when the start
// point is within backStep + backMargin (the original walk then runs: it is short).
inline bool BackStepGoal(const float* from, const float* base, const RecallConfig& c, float* out) {
  const float dx = base[0] - from[0], dy = base[1] - from[1];
  const float len = std::sqrt(dx * dx + dy * dy);
  if (len <= c.backStep + c.backMargin) return false;
  const float k = c.backStep / len;
  out[0] = from[0] + dx * k;
  out[1] = from[1] + dy * k;
  out[2] = from[2];
  return true;
}

}  // namespace agents
}  // namespace gms
