// The agent brain's engine-free rules (roadmap D1b-live step 3; docs/AGENT_AI.md §6.5): which gambit of a phase is
// used, gambit cooldowns, how a condition list combines, which link moves the brain to another node. The brain's update
// was compiled out of Dawngate.exe (AGENT_AI.md §3); these rules are [assumed] where the data alone doesn't say:
//   - phaseType (enum 0x0141F7D8): prioritizedList = the first usable gambit; sequential = the next usable one after the
//     last used, wrapping; random = a uniform pick among the usable; weightedRandom = a pick weighted by `weight`;
//   - a gambit is usable when its conditions pass and its own cooldown is over; `waitOnCondition` keeps a prioritized
//     list from falling through to later gambits while that gambit is only cooling down;
//   - a condition list: each condition (negated if `negateCondition`) joins the result so far with the previous
//     condition's postfixOperator (1 and, 2 or; 0 counts as and), left to right; an empty list passes;
//   - the gambit cooldown: `cooldown` s, or uniform in [cooldown, maxCooldown] with randomizeCooldown;
//   - a link fires when its conditions pass, its activateTime has passed since the node was entered, it isn't cooling
//     down, and (activateOnce) it hasn't fired before; its cooldown starts when it fires (or when the node is entered,
//     startOnCooldown).
// Header-only, so the harness (dgsim.dll) uses it with or without gms_core; gms_tests checks it.
#pragma once

#include <cstdint>
#include <vector>

namespace gms {
namespace agents {

enum PhaseType : int { kPrioritizedList = 0, kSequential = 1, kRandom = 2, kWeightedRandom = 3 };

struct GambitView {
  bool conditionsPass = true;
  bool coolingDown = false;
  bool waitOnCondition = false;
  float weight = 1.f;
  bool Usable() const { return conditionsPass && !coolingDown; }
};

// The gambit to use now, or -1. `cursor` (sequential) is the last used index, updated on a pick; `random01` in [0, 1).
inline int ChooseGambit(int phaseType, const std::vector<GambitView>& g, int* cursor, double random01) {
  const int n = static_cast<int>(g.size());
  if (n == 0) return -1;
  switch (phaseType) {
    case kSequential: {
      const int start = cursor ? *cursor : -1;
      for (int k = 1; k <= n; ++k) {
        const int i = ((start + k) % n + n) % n;
        if (g[i].Usable()) {
          if (cursor) *cursor = i;
          return i;
        }
      }
      return -1;
    }
    case kRandom:
    case kWeightedRandom: {
      double total = 0;
      for (const GambitView& v : g)
        if (v.Usable()) total += phaseType == kRandom ? 1.0 : (v.weight > 0 ? v.weight : 0.0);
      if (total <= 0) return -1;
      double pick = random01 * total;
      int last = -1;
      for (int i = 0; i < n; ++i) {
        if (!g[i].Usable()) continue;
        const double w = phaseType == kRandom ? 1.0 : (g[i].weight > 0 ? g[i].weight : 0.0);
        if (w <= 0) continue;
        last = i;
        if (pick < w) return i;
        pick -= w;
      }
      return last;
    }
    case kPrioritizedList:
    default:
      for (int i = 0; i < n; ++i) {
        if (g[i].Usable()) return i;
        if (g[i].waitOnCondition && g[i].conditionsPass) return -1;  // wait for this one's cooldown
      }
      return -1;
  }
}

inline double GambitCooldownSeconds(float cooldown, float maxCooldown, bool randomize, double random01) {
  if (!randomize || maxCooldown <= cooldown) return cooldown > 0 ? cooldown : 0.0;
  return cooldown + (maxCooldown - cooldown) * random01;
}

struct ConditionResult {
  bool pass = true;
  bool negate = false;
  int postfixOperator = 0;  // 1 and, 2 or, 0 = and
};

inline bool CombineConditions(const std::vector<ConditionResult>& c) {
  if (c.empty()) return true;
  bool result = c[0].pass != c[0].negate;
  for (std::size_t i = 1; i < c.size(); ++i) {
    const bool v = c[i].pass != c[i].negate;
    if (c[i - 1].postfixOperator == 2) result = result || v;
    else result = result && v;
  }
  return result;
}

struct LinkView {
  bool conditionsPass = true;
  bool activateOnce = false;
  bool firedBefore = false;
  float activateTime = 0.f;       // s after entering the node
  double secondsInNode = 0;
  double cooldownLeft = 0;        // s
};

inline bool LinkFires(const LinkView& l) {
  if (l.activateOnce && l.firedBefore) return false;
  if (l.secondsInNode < l.activateTime) return false;
  if (l.cooldownLeft > 0) return false;
  return l.conditionsPass;
}

// ---- D6a: the bots' plan layer (docs/AGENT_AI.md §11, *D6a*) -------------------------------------------------------
// A bot's AIDefinition mixes plan nodes and phase nodes. The brain stands in one plan node (brain +0x14, the start plan
// from AgentBrain_InitBot) and one phase node (+0x18, what AgentBrain_GetCurrentPhase reads). Two link sets [data]:
// the plan node's own cAINode.output (to other plans: Main -> Flee, Idle -> Main, ...) and its Plan's output (to phase
// nodes: Main -> TowerPush, LastHit, ...). Every plan's phaseDecisionType and planDecisionType is prioritizedList. The
// rules [assumed: the brain's update was compiled out]:
//   - per think the plan links first (the first that fires moves the brain to its plan, the phase is chosen anew),
//     then the phase links (ChoosePhaseLink), then the phase's gambits as for NPCs (ChooseGambit);
//   - a link's cooldown (it has no randomizeCooldown) is uniform in [cooldown, maxCooldown] when maxCooldown is larger,
//     else `cooldown` (Main -> Gank: 0 and 240 reads as "some time within four minutes");
//   - a phase link into the phase the brain is in counts while its cooldown runs: the cooldown keeps a bot from
//     re-entering the phase, not from staying in it (Main -> TowerPush has a 5 s cooldown and its conditions hold for
//     the whole push).
inline double LinkCooldownSeconds(float cooldown, float maxCooldown, double random01) {
  if (maxCooldown > cooldown) return cooldown + (maxCooldown - cooldown) * random01;
  return cooldown > 0 ? cooldown : 0.0;
}

// Whether a link may fire before its conditions are looked at (activateOnce, activateTime, cooldown); `intoCurrent`:
// the link leads to the node the brain is in (a phase link: its cooldown doesn't count).
inline bool LinkOpen(const LinkView& l, bool intoCurrent = false) {
  if (l.activateOnce && l.firedBefore) return false;
  if (l.secondsInNode < l.activateTime) return false;
  if (l.cooldownLeft > 0 && !intoCurrent) return false;
  return true;
}

// The plan's phase choice: the first open link whose conditions pass, in order; `outputs[i]` is link i's node.
// -1 = none (the current phase stays).
inline int ChoosePhaseLink(const std::vector<LinkView>& links, const std::vector<int>& outputs, int currentPhase) {
  for (std::size_t i = 0; i < links.size() && i < outputs.size(); ++i)
    if (LinkOpen(links[i], outputs[i] == currentPhase) && links[i].conditionsPass) return static_cast<int>(i);
  return -1;
}

// A bot gambit's ability slot (cGambitDefinition +0x18, enum 0x0141F830): -1 none (the gambit's own ability), 0 basic,
// 1 ward, 3-6 Q-R (ranks: cLabsPlayer +0x2A8 + 4 * slot), 7-9 spell1-3, 10-11 consume1-2. LabsPlayer_GetAbilityInSlot
// (0x00911B80) maps 0-6 to the shaper's PlayerClass abilities and 7-11 to the item in inventory slot (slot - 1).
enum GambitSlot : int { kSlotNone = -1, kSlotBasic = 0, kSlotWard = 1, kSlotQ = 3, kSlotR = 6, kSlotSpell1 = 7,
                        kSlotSpell3 = 9, kSlotConsume1 = 10, kSlotConsume2 = 11 };
inline bool SlotIsRanked(int slot) { return slot >= kSlotQ && slot <= kSlotR; }
inline bool SlotIsItem(int slot) { return slot >= kSlotSpell1 && slot <= kSlotConsume2; }
inline bool SlotIsConsumable(int slot) { return slot == kSlotConsume1 || slot == kSlotConsume2; }
inline int SlotInventoryIndex(int slot) { return SlotIsItem(slot) ? slot - 1 : -1; }

// ---- D6d: what a bot's gambit aims at (docs/AGENT_AI.md *D6d*) ------------------------------------------------------
// From what its conditions gave: an object (a passing condition's target, else the agent's mTarget) and a point (the
// first passing condition's position). A condition with useTargetInfo that names a point and, with useTarget,
// kObjIDNone as its target (condition_Bot_Ward: `return true, x, y, z, kObjIDNone, true`) aims the gambit at the point
// alone: mTarget doesn't stand in. Such a request that is out of range (5) pursues the point: the blackboard's target
// position, mTarget cleared, which the Pursue leaf's nBehavior_Follow walks to [inferred from BTNode_Pursue_Evaluate
// 0x008A8CA0 and Behavior_Follow.lua]. A point without useTargetInfo (condition_base_position: the direction of a cast
// towards the base) stays the request's position only, as in D6a: with no object it pursues nothing (d6d_soak2c: a
// Guardian's shield gambit at its base point, 314 units away, would otherwise walk it home). `pursuePoint` false is
// D6a's rule: mTarget always stands in for a missing object, and no request is at a point alone.
struct GambitAim {
  std::uint32_t target = 0;  // the request's object (0 = none)
  bool atPoint = false;      // the request aims at the condition's point alone (its target info)
};
inline GambitAim AimGambit(std::uint32_t conditionTarget, std::uint32_t mTarget, bool hasPoint, bool pointOnly,
                           bool pursuePoint) {
  GambitAim a;
  a.target = conditionTarget;
  const bool pointGiven = pursuePoint && pointOnly && hasPoint;
  if (!a.target && !pointGiven) a.target = mTarget;  // [assumed] the agent's mTarget when the condition named none
  a.atPoint = pointGiven && !a.target;
  return a;
}
// Whether a request's result starts (or keeps) the pursuit of its point: out of range (5) at a point alone.
inline bool PursuesPoint(const GambitAim& a, int result) { return a.atPoint && result == 5; }

// ---- BOTIDLE: bots that stand still --------------------------------------------------------
// The host's bot brain took the first gambit of a prioritized list whose conditions pass and, when the engine refused its
// request (the ability on cooldown, no resource, the script's CanUse false, a disabled caster), did nothing that think -
// and at the next one, and so on until the refusal ended: every gambit below it starved. In the Hunter's ClearCamp phase
// the W (cooldown 8+ s) sits above the Q and the basic attack, all with the same condition (an enemy creep within 30), so
// the Hunter stood at the camp on IdleDefault while the creeps killed it (in a test run: HP 700 -> 215 in 120 s).
// LabsAbilities_ValidateRequest (0x009053C0) results [code]: 1/2 accepted, 5 out of range (pursue), 3 the cooldown ends
// within the command queue window, 4 queued, -0x270F on cooldown, -0x270D resource, -0x2704 CanUse/CanActivate false,
// -0x2708 invalid target, -0x2709 disabled (silence/disarm attributes), -0x2706 other attribute, -0x270C out of range
// without pursuit, -0x270B the same ability still holds the agent, -0x2707 the caster is dead, -0x2703 unknown ability,
// -0x270A bad slot. The host's own: -0x7FFE an item refused, -0x7FFF an exception.
constexpr int kResultCooldown = -0x270F;
constexpr int kResultHoldsAgent = -0x270B;
constexpr int kResultDead = -0x2707;
constexpr int kResultItemRefused = -0x7FFE;
constexpr int kResultException = -0x7FFF;

// Whether a refused request leaves the gambit "not usable now", so the prioritized list goes on with the next one whose
// conditions pass [assumed: the original's brain skipped unusable gambits; the stripped update is not in the client].
// Kept as before (the bot waits for the next think): an accepted or pending request (1, 2, 3, 4), the pursuit (5), an
// ability still holding the agent (-0x270B: a cast or swing in progress), a dead caster, an exception, and a basic attack
// on cooldown (the swing interval: the bot stays and swings again rather than walking off to a lower gambit's cast).
inline bool GambitRefusalSkips(int result, int slot) {
  if (result >= 0) return false;
  if (result == kResultHoldsAgent || result == kResultDead || result == kResultException) return false;
  if (slot == kSlotBasic && result == kResultCooldown) return false;
  return true;
}

// A pursuit (request result 5, the Pursue leaf follows the target) that makes no progress: `startDistance` when the
// window began, `nowDistance` now, after `seconds`. Stuck when the window is `limitSeconds` old and the bot got closer
// by less than `minProgress` (a target behind a wall NavPower cannot path to, a creep that is leashing home, an enemy
// that runs away at the bot's speed). The gambit is then left out for a few seconds.
inline bool PursuitStuck(double seconds, float startDistance, float nowDistance, double limitSeconds, float minProgress) {
  return seconds >= limitSeconds && startDistance - nowDistance < minProgress;
}

// BOTTEMPO: a bot frozen under its own ability. A bot ability that holds the agent and waits for the bot to reach a
// point (ability_Bot_MoveToTarget: MoveToPointExact + WaitForNearGoal(0), the back step's ability) never ends when the
// point can't be reached, and while it holds the agent the phase's default behavior can't run: in a test match a
// Gladiator stood 211 s at one spot through four plans. Stuck when the same instance has held the agent for
// `limitSeconds` while the bot moved less than `minMove` (the caller restarts the window whenever the bot has moved
// that far); the host then ends the instance. The recall's 8-s channel and every cast are shorter; at home (healing,
// shopping) the host doesn't ask.
inline bool HoldStuck(double heldSeconds, float moved, double limitSeconds, float minMove) {
  return limitSeconds > 0 && heldSeconds >= limitSeconds && moved < minMove;
}

// BOTTEMPO: a bot's mTarget (its best target, which the Pursue leaf follows) that is no longer a valid hostile target
// (untargetable, unattackable, dead, an ally) is dropped: in test matches Tacticians walked from their base to the
// parasite's spot after it died (its next stage untargetable there) and stood on it for two to three minutes, the
// Pursue leaf following the old target with no gambit to replace it.
inline bool TargetStale(bool hasTarget, bool validHostile) { return hasTarget && !validHostile; }

// Who may be answered when a bot idles under attack: players and the mobile NPC types (nNPCType: lane minions 0-2, creeps
// 3, objectives 6-8, pets 11, followers 12), not structures (Tower 9, Guardian 13, Nub 14, Locus 15, Ward 16), mines,
// destructibles or interactables.
inline bool FallbackAttackerType(int npcType, bool isPlayer) {
  if (isPlayer) return true;
  return (npcType >= 0 && npcType <= 3) || (npcType >= 6 && npcType <= 8) || npcType == 11 || npcType == 12;
}

// The idle fallback is due when the bot has stood without an acting gambit for `idleMs` and something hit it within
// `hitWindowMs` (both measured by the caller); a bot nobody attacks may stand in its phase as the scripts say.
inline bool IdleFallbackDue(long long idleForMs, long long sinceHitMs, long long idleMs, long long hitWindowMs) {
  return idleMs > 0 && idleForMs >= idleMs && sinceHitMs >= 0 && sinceHitMs <= hitWindowMs;
}

}  // namespace agents
}  // namespace gms
