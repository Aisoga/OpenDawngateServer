// The agent AI's engine-free rules (roadmap D1b-live; docs/AGENT_AI.md §6.3): the order of an agent's aggro list.
// The client's AgentBlackboard_GetBestTarget (0x008AA270) takes the first valid entry of the list, so the order is the
// targeting priority; the code that ordered it was compiled out, so this rule is [assumed] (AGENT_AI.md §6.3):
//   1. taunting (the entry's taunt end lies in the future);
//   2. reported by a player-controlled ally within callForHelpTime (entry +0x18);
//   3. the current best target, if it is still on the list (stickiness: targets don't flip each update);
//   4. reported by a non-player ally within callForHelpTime (entry +0x20);
//   5. for the aggro types minion and tower: non-players before players;
//   6. nearer first (edge distance); then the old list order.
// Header-only, so the harness (dgsim.dll) uses it with or without gms_core; gms_tests checks it.
#pragma once

#include <algorithm>
#include <cstdint>
#include <vector>

namespace gms {
namespace agents {

// AIDefinition.aggroType (AIDefinition +0x38; Agent_GetAggroType 0x008ABC30).
enum AggroType : int {
  kAggroNormal = 0,
  kAggroIgnore = 1,
  kAggroMinion = 2,
  kAggroJungleCreature = 3,
  kAggroTower = 4,
  kAggroBot = 5,
  kAggroObjective = 6,
};

// BattlegroundsGameplayTuning.callForHelpTime [data].
constexpr std::int64_t kCallForHelpTimeMs = 3000;

struct AggroCandidate {
  std::uint32_t id = 0;
  bool taunting = false;
  std::int64_t playerReportAgeMs = -1;  // now - entry +0x18; -1 = never reported by a player-controlled ally
  std::int64_t otherReportAgeMs = -1;   // now - entry +0x20; -1 = never reported by a non-player ally
  bool isPlayer = false;                // the target is player-controlled
  bool sticky = false;                  // the agent's current best target
  bool defend = false;                  // MINTARGET: a shaper attacking an own-team shaper in range (goes first)
  float distance = 0.f;                 // edge distance from the agent
  int index = 0;                        // position in the list before ordering
};

inline bool NonPlayersFirst(int aggroType) { return aggroType == kAggroMinion || aggroType == kAggroTower; }

// D14: whether the blackboard update adds a perceived enemy within aggroRange to the list on sight. Not for the
// objective type (the parasite, aggro range 27): it fights only those who hit it [assumed; the script evidence: its
// passive's dance easter egg needs five enemy players dancing within 25 units while its aggro list is empty, and the
// untargetable egg before 300 s would otherwise shoot whoever walks by; MATCH_FLOW.md *D14*]. Ignore and bot agents
// keep no list of their own. Jungle creatures and nubs have aggro range 0, so nothing is added for them anyway.
inline bool AddsOnSight(int aggroType) {
  return aggroType != kAggroObjective && aggroType != kAggroIgnore && aggroType != kAggroBot;
}

inline bool Recent(std::int64_t ageMs, std::int64_t windowMs) { return ageMs >= 0 && ageMs <= windowMs; }

// True if `a` goes before `b`.
// `strict` (MINTARGET, lane minions): after a taunt the defended shaper, then every non-player (tower, structure,
// minion) before every shaper, ahead of call-for-help reports and stickiness (which kept a shaper first once chosen).
inline bool AggroBefore(const AggroCandidate& a, const AggroCandidate& b, bool nonPlayersFirst,
                        std::int64_t windowMs = kCallForHelpTimeMs, bool strict = false) {
  if (a.taunting != b.taunting) return a.taunting;
  if (strict) {
    if (a.defend != b.defend) return a.defend;
    if (a.isPlayer != b.isPlayer) return !a.isPlayer;
  }
  const bool ap = Recent(a.playerReportAgeMs, windowMs), bp = Recent(b.playerReportAgeMs, windowMs);
  if (ap != bp) return ap;
  if (a.sticky != b.sticky) return a.sticky;
  const bool ao = Recent(a.otherReportAgeMs, windowMs), bo = Recent(b.otherReportAgeMs, windowMs);
  if (ao != bo) return ao;
  if (nonPlayersFirst && a.isPlayer != b.isPlayer) return !a.isPlayer;
  if (a.distance != b.distance) return a.distance < b.distance;
  return a.index < b.index;
}

// Orders the candidates in place (the agent's aggro list in its new order).
inline void OrderAggro(std::vector<AggroCandidate>* list, int aggroType, std::int64_t windowMs = kCallForHelpTimeMs,
                       bool strict = false) {
  const bool npf = NonPlayersFirst(aggroType);
  std::stable_sort(list->begin(), list->end(),
                   [npf, windowMs, strict](const AggroCandidate& a, const AggroCandidate& b) {
                     return AggroBefore(a, b, npf, windowMs, strict);
                   });
}

// The flag byte of an aggro-list entry (entry +0x28, AgentBlackboard_AddToAggroList's last argument, written only when
// the entry is created): the client's own adders (Agent_CallForHelp, Agent_Taunt) pass 0 and neither
// AgentBlackboard_GetBestTarget nor the Lua getters read it. The host marks the base structures that
// nBattlegrounds.AddBaseStructuresToAggroList puts on a lane minion's list with 1 (D5).
constexpr unsigned char kEntryFlagBaseStructure = 1;

// Whether an entry that passed the other checks (alive, a valid hostile target, targetable, visible) stays by range
// [assumed rule]: within dropAggroRange (edge distance) and inside the tether, except a base structure, which stays
// wherever the minion stands: Behavior_Assault puts them on the list at the lane's end and waits for the list to empty
// (nThread.WaitForEmptyAggroList), so dropping a Guardian 41 units away (beyond a minion's 33) would make the minion
// add it again every frame and never walk to it.
inline bool KeepByRange(bool baseStructure, float distance, float dropRange, bool inTether) {
  if (baseStructure) return true;
  return distance <= dropRange && inTether;
}

// Battle ready for player-controlled agents (AGENT_AI.md §6.3, [inferred] from BattlegroundsGameplayTuning): set when an
// enemy shaper is within the engage range, cleared only beyond the disengage range.
constexpr float kBattleReadyEngageRange = 27.f;
constexpr float kBattleReadyDisengageRange = 35.f;
inline bool NextBattleReady(bool current, float nearestEnemyShaper /* < 0: none */) {
  if (nearestEnemyShaper < 0) return false;
  return current ? nearestEnemyShaper <= kBattleReadyDisengageRange : nearestEnemyShaper <= kBattleReadyEngageRange;
}

}  // namespace agents
}  // namespace gms
