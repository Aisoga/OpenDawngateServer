// gms/core/agents (BOTDEFEND): a bot that an enemy shaper attacks while its plan backs off or flees shoots back when it
// can't get away, and doesn't start a recall while that shaper is still on it. Engine-free; the host
// (gms/engine/agents.cpp, DefendStep and the recall checks) measures the inputs and makes the requests.
//
// The user's report (2026-10-31, the public server): "when I killed the Petrus bot for the first time in the jungle, he
// just completely ignored me and let me kill him ... when it was running away it just took a little bit of distance and
// tried to recall". The chain in the data [data: re/out/assetdata/Plan.json, Phase.json; script:
// Conditions/condition_Bot_Counterattack.lua]:
//   - RecentPlayerDamage takes the Gladiator into its PlayerCombat plan. That plan's phase is SelfDefense (the kit and
//     the basic attack) only while condition_Bot_Counterattack holds: the bot at 60 % HP or more, no enemy 2+ levels
//     above it, not outnumbered within 30, no enemy tower within 30, and not beyond its lane's centre (+11, its "harass
//     leash"). Otherwise the phase is Reposition, which has no attack at all: LanePosition_Rear with nobody near, else
//     ability_Bot_Back (an enemy within 20 or a tower), default behavior nBehavior_GoBack.
//   - So from 60 % down to 30 % (Bot_HealthLow, where PlayerCombat's link goes to the lane plan and the lane plan's to
//     Flee) the bot only walks away, in BOTBACK's 30-unit steps, and an attacker as fast as it (a chasing assassin)
//     hits it the whole way: Petrus at 59 % lost 570 HP in 3.8 s without a shot back, then fled at 19 % and died 1.4 s
//     later.
//   - The Flee phase walks home (Back while anything hostile is within 30), and recalls on Bot_SafeRecall (nothing
//     hostile the bot's team sees within 60, 10 while the recall runs) and the host's BOTRECALL rule (recall.h).
//
// Rules [assumed thresholds; what a human player does]:
//   - An episode: an enemy shaper hit the bot within hitWindowMs, it is alive and the bot's team sees it.
//   - In a backing-off phase (Reposition: the plan left the trade) the bot shoots its attacker with its basic attack
//     whenever the attack is ready and the attacker within its reach, and walks on between the shots (the plan's Back
//     step): a kite, not a stand.
//   - In a Flee phase it shoots back only when it is caught: the attacker has stayed within its reach for caughtMs of
//     the flight (it can't outrun it, so the walk alone just takes the hits). Otherwise it runs.
//   - Never a walk toward the attacker: beyond the bot's reach nothing happens, and a request that the engine answers
//     "out of range" (5, a pursuit) shrinks the reach the bot remembers; the plan's retreat request in the same think
//     replaces the pursuit.
//   - The reach is learned per bot: every basic attack that started on a shaper at distance d proves reach >= d, every
//     "out of range" at d proves reach < d. It starts at reachDefault.
//   - A recall (the host's BOTRECALL, the data's Flee gambit) waits while a chaser is near: an enemy shaper that hit the
//     bot within chaseMemoryMs and is still within chaseRange of it (and seen). That is in addition to the quiet time
//     (no hit for 4 s) and recall.h's 45-unit rule; the channel lasts 8 s, so a chaser 45-60 units away reaches the bot
//     long before it ends.
#pragma once

#include <cstring>

namespace gms {
namespace agents {

struct DefendConfig {
  long long hitWindowMs = 3000;   // an enemy shaper's hit makes it the attacker this long (BOTREFLEX's window)
  long long endMs = 4000;         // an episode ends after this long without a hit by an enemy shaper
  long long caughtMs = 1500;      // in a Flee phase: the attacker stayed within reach this long -> shoot back
  float reachDefault = 14.f;      // the basic attack's reach before the bot learned it (ranged basics reach 16-20)
  float reachMin = 3.f;           // never less (a melee swing)
  float reachMax = 30.f;          // never more (nothing reaches farther; Counterattack's own radius)
  float missMargin = 0.5f;        // an "out of range" at d sets the reach to d minus this
  float chaseRange = 60.f;        // a recall waits while a recent attacker is this close (Bot_SafeRecall's awareness)
  long long chaseMemoryMs = 8000; // ... and hit the bot within this (the recall's channel)
};

// The phases the rule acts in, from the phase's name (the data's naming: <Class>_<Role>_<Phase>.Phase).
enum DefendPhase : int { kDefendOtherPhase = 0, kDefendBackOff = 1, kDefendFlee = 2 };

inline int DefendPhaseOf(const char* phaseName) {
  if (!phaseName) return kDefendOtherPhase;
  if (std::strstr(phaseName, "Reposition")) return kDefendBackOff;
  if (std::strstr(phaseName, "Flee") && !std::strstr(phaseName, "TowerTargeting")) return kDefendFlee;
  return kDefendOtherPhase;
}

// The bot's reach as it learned it.
struct ReachMemory {
  float reach = -1.f;  // < 0: not learned yet
  float Get(const DefendConfig& c) const { return reach < 0.f ? c.reachDefault : reach; }
  void Hit(float dist, const DefendConfig& c) {  // a basic attack started at this distance
    if (!(dist > 0.f)) return;
    const float r = Get(c);
    reach = dist > r ? (dist < c.reachMax ? dist : c.reachMax) : r;
  }
  void Miss(float dist, const DefendConfig& c) {  // "out of range" at this distance
    if (!(dist > 0.f)) return;
    const float r = dist - c.missMargin;
    const float now = Get(c);
    reach = r < now ? (r > c.reachMin ? r : c.reachMin) : now;
  }
};

struct DefendInputs {
  bool attacker = false;      // an enemy shaper hit the bot within hitWindowMs, alive
  bool visible = true;        // the bot's team sees it
  float dist = 0.f;           // to it (2-D)
  int phase = kDefendOtherPhase;
  long long caughtForMs = 0;  // in a Flee phase: how long the attacker has stayed within reach (0 = not now)
  float reach = 14.f;         // the bot's basic attack reach (ReachMemory)
};

// The host's refusal of a recall gambit while chased: a refusal that lets the prioritized list go on (brain.h
// GambitRefusalSkips), so the Flee phase's walk home runs.
constexpr int kResultRecallChased = -0x7E01;

// Why the rule did not shoot (for the log and the stats).
enum DefendWhy : int {
  kDwShoot = 0,       // it shoots
  kDwNoAttacker,      // nobody attacks it
  kDwHidden,          // its attacker is hidden from its team
  kDwPhase,           // a phase the rule leaves alone (the plan's own fight, the lane, ...)
  kDwOutOfReach,      // the attacker is beyond its reach: the retreat goes on
  kDwRunning,         // fleeing and not caught (yet)
  kDwWhyCount
};

inline const char* DefendWhyName(int w) {
  switch (w) {
    case kDwShoot: return "shoot";
    case kDwNoAttacker: return "no attacker";
    case kDwHidden: return "attacker hidden";
    case kDwPhase: return "phase left alone";
    case kDwOutOfReach: return "out of reach";
    case kDwRunning: return "running";
    default: return "?";
  }
}

inline int DecideDefend(const DefendInputs& in, const DefendConfig& c) {
  if (!in.attacker) return kDwNoAttacker;
  if (!in.visible) return kDwHidden;
  if (in.phase == kDefendOtherPhase) return kDwPhase;
  if (in.dist > in.reach) return kDwOutOfReach;
  if (in.phase == kDefendFlee && in.caughtForMs < c.caughtMs) return kDwRunning;
  return kDwShoot;
}

// How long the attacker has kept within reach during a flight: `sinceMs` is when it came within reach (0 = it isn't);
// the caller keeps it. Returns the new `sinceMs`.
inline long long CaughtSince(long long sinceMs, bool inFlee, bool attacker, bool visible, float dist, float reach,
                             long long nowMs) {
  if (!inFlee || !attacker || !visible || dist > reach) return 0;
  return sinceMs ? sinceMs : (nowMs > 0 ? nowMs : 1);
}

// A recall waits while a chaser is near: `chaserDist` is the distance to the enemy shaper that hit the bot last (seen,
// alive; < 0 none), `chaserHitAgeMs` since that hit.
inline bool RecallChased(float chaserDist, long long chaserHitAgeMs, const DefendConfig& c) {
  return chaserDist >= 0.f && chaserHitAgeMs >= 0 && chaserHitAgeMs <= c.chaseMemoryMs && chaserDist <= c.chaseRange;
}

// How an episode ended (the log's and the stats' names).
enum DefendEnd : int { kDeDied = 0, kDeAttackerDied, kDeEscaped, kDeRecalled, kDeEndCount };
inline const char* DefendEndName(int e) {
  switch (e) {
    case kDeDied: return "the bot died";
    case kDeAttackerDied: return "the attacker died";
    case kDeEscaped: return "no more hits";
    case kDeRecalled: return "recalled";
    default: return "?";
  }
}

}  // namespace agents
}  // namespace gms
