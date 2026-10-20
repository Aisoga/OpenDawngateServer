// gms/core/agents (KILLFINISH): whether a bot about to leave a fight with an enemy shaper stays to finish it. Engine-free;
// the host (gms/engine/agents.cpp, BotBrainStep) measures the sensors once per think while the bot's plan is an
// EngageForKill plan and carries the decision out.
//
// The user's report (2026-10-20): "normal shapers can sometimes disengage when they would be able to kill and finish
// off the enemy, because their hp is considered too low". The AI data leaves an EngageForKill plan by two links (in
// every bot AI): to its Flee plan when condition_Bot_HealthLow holds (own HP under 30 %, the
// Tacticians and the HighHuntress) and to its lane plan when condition_Bot_BailOnKill holds, which among its five tests
// bails whenever the enemy's HP fraction is above the bot's own (plus 6 points per extra ally). Neither looks at how
// fast the fight would end. The plan's FleeTowerTargeting phase (a tower targets the bot) and its Back gambit (an enemy
// shaper close) and BOTREFLEX's tower back-off pull it away in the same way.
//
// The rule: the bot stays when it wins the trade. Its time to kill the target (the target's HP over the bot's damage
// rate of the last seconds, plus the walk beyond close range) must be short (maxTtkS) and shorter by `margin` than its
// own time to die under the damage it is taking; the target low (targetMaxFrac) and within reach; no more enemy shapers
// around than allies; one engage held at most holdMaxMs. Under an enemy tower (the bot or the target within its reach)
// only a near-certain kill counts: towerTtkS and towerMargin, and the tower's assumed damage added when it isn't hitting
// the bot yet. Every threshold is [assumed].
#pragma once

namespace gms {
namespace agents {

struct KillFinishConfig {
  float targetMaxFrac = 0.35f;   // the target at or below this HP fraction (a finish, not a fresh fight)
  float reach = 30.f;            // the target within this (2-D units) of the bot (live round 1: 22 let 16 of 29 go)
  float closeRange = 12.f;       // ... within this no walk is counted (a ranged shaper's reach)
  float walkSpeed = 9.f;         // units per second for the rest of the way
  float maxTtkS = 5.f;           // the kill (with the walk) within this many seconds
  float margin = 1.5f;           // the bot's time to die at least this times the time to kill
  float towerTtkS = 1.5f;        // under an enemy tower: the kill within this
  float towerMargin = 2.5f;      // ... and this margin
  float towerDpsFrac = 0.10f;    // a tower's damage per second, as a fraction of the bot's max HP, when it isn't hitting yet
  float allDamageWeight = 0.5f;  // damage on non-shapers (minions, creeps) counts at this weight for the bot's rate
  float minDps = 1.f;            // a damage rate below this is none: no finish
  int extraEnemies = 0;          // enemy shapers near besides the target may exceed the allies near by this many
  long long holdMaxMs = 5000;    // one engage is held at most this long
};

struct KillFinishSensors {
  bool target = false;          // an enemy shaper to finish (the bot's target, else the lowest visible one in reach)
  bool targetVisible = true;
  float selfHp = 0.f, selfMaxHp = 1.f;
  float targetHp = 0.f, targetMaxHp = 1.f;
  float dist = 0.f;             // bot to target, 2-D
  float dpsShapers = 0.f;       // the bot's damage per second on enemy shapers over the window
  float dpsAll = 0.f;           // ... on anything
  float takenDps = 0.f;         // the bot's HP lost per second over the window
  bool underTower = false;      // the bot or the target within reach of a standing enemy tower
  bool towerHitting = false;    // a tower hit the bot within the window (its damage is in takenDps)
  int enemiesNear = 0;          // visible enemy shapers near the bot, the target not counted
  int alliesNear = 0;           // allied shapers near the bot (not itself)
  long long heldMs = -1;        // since this engage's first hold (-1: none yet)
};

enum KillFinishWhy : int {
  kKfFinish = 0,       // stays
  kKfNoTarget,         // no enemy shaper to finish (or the bot itself dead)
  kKfTargetHealthy,    // the target above targetMaxFrac
  kKfFar,              // beyond reach
  kKfOutnumbered,      // more enemy shapers near than allies (+ extraEnemies)
  kKfNoDamage,         // the bot dealt (almost) nothing lately: no rate to judge by
  kKfTooSlow,          // the kill would take longer than maxTtkS
  kKfLosing,           // the bot would die first (within margin)
  kKfTower,            // under an enemy tower and not near-certain
  kKfHoldOver,         // held holdMaxMs already
  kKfWhyCount
};

inline const char* KillFinishWhyName(int w) {
  switch (w) {
    case kKfFinish: return "finish";
    case kKfNoTarget: return "no shaper to finish";
    case kKfTargetHealthy: return "target not low";
    case kKfFar: return "target far";
    case kKfOutnumbered: return "outnumbered";
    case kKfNoDamage: return "no damage dealt lately";
    case kKfTooSlow: return "kill too slow";
    case kKfLosing: return "losing the trade";
    case kKfTower: return "tower, kill not certain";
    case kKfHoldOver: return "held long enough";
    default: return "?";
  }
}

struct KillFinishDecision {
  bool finish = false;
  int why = kKfNoTarget;
  float dps = 0.f;   // the rate the decision used
  float ttk = -1.f;  // seconds to kill, the walk included (-1: not computed)
  float ttd = -1.f;  // seconds to die (-1: not computed; 1e9: nothing hits the bot)
};

// The bot's damage rate: on shapers, or a share of its rate on anything, whichever is higher.
inline float KillFinishDps(const KillFinishSensors& s, const KillFinishConfig& c) {
  const float all = s.dpsAll * c.allDamageWeight;
  return s.dpsShapers > all ? s.dpsShapers : all;
}

inline KillFinishDecision DecideKillFinish(const KillFinishSensors& s, const KillFinishConfig& c = KillFinishConfig()) {
  KillFinishDecision d;
  d.dps = KillFinishDps(s, c);
  if (!s.target || !s.targetVisible || !(s.targetHp > 0.f) || !(s.selfHp > 0.f)) return d;  // kKfNoTarget
  if (s.heldMs >= c.holdMaxMs) {
    d.why = kKfHoldOver;
    return d;
  }
  const float frac = s.targetMaxHp > 0.f ? s.targetHp / s.targetMaxHp : 1.f;
  if (frac > c.targetMaxFrac) {
    d.why = kKfTargetHealthy;
    return d;
  }
  if (s.dist > c.reach) {
    d.why = kKfFar;
    return d;
  }
  if (s.enemiesNear > s.alliesNear + c.extraEnemies) {
    d.why = kKfOutnumbered;
    return d;
  }
  if (d.dps < c.minDps) {
    d.why = kKfNoDamage;
    return d;
  }
  const float walk = s.dist > c.closeRange && c.walkSpeed > 0.f ? (s.dist - c.closeRange) / c.walkSpeed : 0.f;
  d.ttk = s.targetHp / d.dps + walk;
  float taken = s.takenDps > 0.f ? s.takenDps : 0.f;
  if (s.underTower && !s.towerHitting) taken += c.towerDpsFrac * s.selfMaxHp;
  d.ttd = taken > 0.f ? s.selfHp / taken : 1.0e9f;
  const float limit = s.underTower ? c.towerTtkS : c.maxTtkS;
  const float margin = s.underTower ? c.towerMargin : c.margin;
  if (d.ttk > limit) {
    d.why = s.underTower ? kKfTower : kKfTooSlow;
    return d;
  }
  if (d.ttd < margin * d.ttk) {
    d.why = s.underTower ? kKfTower : kKfLosing;
    return d;
  }
  d.finish = true;
  d.why = kKfFinish;
  return d;
}

// The damage a bot dealt over the last seconds (the host's damage hook feeds it): a small ring of hits.
struct KillFinishDealt {
  static constexpr int kHits = 32;
  long long ms[kHits] = {};
  float amount[kHits] = {};
  bool toShaper[kHits] = {};
  int n = 0, head = 0;
  void Note(float dealt, bool shaper, long long now) {
    if (!(dealt > 0.f)) return;
    ms[head] = now;
    amount[head] = dealt;
    toShaper[head] = shaper;
    head = (head + 1) % kHits;
    if (n < kHits) ++n;
  }
  // Damage per second within windowMs: the sum over the time since the earliest hit in the window, at least minSpanMs.
  void Rates(long long now, long long windowMs, long long minSpanMs, float* shapers, float* all) const {
    float s = 0.f, a = 0.f;
    long long first = now;
    for (int i = 0; i < n; ++i) {
      if (now - ms[i] > windowMs || ms[i] > now) continue;
      a += amount[i];
      if (toShaper[i]) s += amount[i];
      if (ms[i] < first) first = ms[i];
    }
    long long span = now - first;
    if (span < minSpanMs) span = minSpanMs;
    const float sec = static_cast<float>(span) / 1000.f;
    *shapers = sec > 0.f ? s / sec : 0.f;
    *all = sec > 0.f ? a / sec : 0.f;
  }
};

// BOTCYCLE: a plan entered in this think whose own link fires at once is passed through: at most `maxHops` plan changes
// per think, and a passed-through plan's link never leads back into a plan visited in this think. True when the link
// into `node` may be taken in hop `hop` (0: the think's first plan change, always allowed).
inline bool PlanChainAllows(const int* visited, int visitedCount, int node, int hop, int maxHops) {
  if (hop == 0) return true;
  if (hop >= maxHops) return false;
  for (int i = 0; i < visitedCount; ++i)
    if (visited[i] == node) return false;
  return true;
}

// BOTCYCLE: the lane plans' stance links have no hysteresis (Lane -> Lane_Cautious on HealthMedium or Outnumbered and
// back when neither holds; Outnumbered counts shapers within 40, which walk in and out of it), so a bot flipped between
// them every think. A link back into the plan the bot left less than dwellMs ago is held, unless the current plan or
// the destination is a fight (EngageForKill, PlayerCombat) or a flight (Flee), or the destination takes the bot away
// from danger (the cautious stances; the host passes that in nextFightOrFlee): leaving a fight, fleeing, turning
// cautious and engaging are never delayed; only the step back from caution to the normal stance waits.
inline bool PlanBounceHeld(bool backIntoPrevious, long long sinceLeftMs, long long dwellMs, bool currentFightOrFlee,
                           bool nextFightOrFlee) {
  return dwellMs > 0 && backIntoPrevious && sinceLeftMs >= 0 && sinceLeftMs < dwellMs && !currentFightOrFlee &&
         !nextFightOrFlee;
}

// BOTCYCLE: an engage (EngageForKill entered by its condition or by a cautious plan's KillShaper join) that the bot left
// again within the quick-exit time blocks the bot's next engages until blockedUntil (each is judged at its own think
// and can still fail at the next one: the target walked beyond 55, the plan's gambits retargeted the nearest shaper,
// which BailOnKill judges; live round 2: 12 of the Creature's 20 engages). KILLFINISH's "would finish" still passes.
inline bool EngageBlocked(long long now, long long blockedUntil) { return blockedUntil > 0 && now < blockedUntil; }

}  // namespace agents
}  // namespace gms
