// gms/core/agents (BOTREFLEX): what a bot does about danger and opportunity regardless of the plan its behavior tree is
// in. Engine-free; the host (gms/engine/agents.cpp, BotBrainStep) measures the sensors and carries the decision out.
//
// The user's reports (2026-10-10): bots run through enemy towers for no reason and take the damage; a Hunter clearing a
// camp does not fight back when a shaper attacks it; shapers in the jungle should be engaged by Hunters and Predators
// (unless they are weak against them, then they back off); Hunters and Predators should defend a spirit well that is
// attacked or being captured on their side of the map.
//
// Rules, in priority order (the first that holds wins; none = the plan's own gambits):
//   1. tower:   the bot stands within a tower's reach after that tower hit it recently -> back off (ability_Bot_Back),
//               unless it is diving for a kill (its target is a weak enemy shaper under the tower and it is healthy).
//   2. (Hunter, Predator) hit by an enemy shaper within the last seconds -> fight back, unless it is the weaker one or
//               outnumbered -> back off.
//   3. (Hunter, Predator) a spirit well of its team is attacked or being captured on its side of the map -> defend it
//               (fight whoever attacks, else walk to the well).
//   4. (Hunter, Predator) an enemy shaper in the jungle within reach -> engage it, unless it is the weaker one or
//               outnumbered: then back off when the enemy is close, else leave it.
#pragma once

namespace gms {
namespace agents {

constexpr int kReflexRoleHunter = 2;    // RolesTuning.role[] index (mRole)
constexpr int kReflexRolePredator = 3;

enum ReflexKind : int { kReflexNone = 0, kReflexBack = 1, kReflexFight = 2, kReflexDefend = 3 };

struct ReflexConfig {
  float towerRange = 34.f;          // a standing enemy tower's reach (2-D units)
  long long towerZoneMs = 8000;     // after a hit by a tower, its reach is avoided this long
  float diveTargetHp = 0.35f;       // an enemy shaper at or below this HP fraction is worth a dive under its tower
  float diveEngagedHp = 0.5f;       // ... when the bot's plan is EngageForKill on it
  float diveSelfHp = 0.4f;          // the diver's own HP fraction at least
  float finishTowerHp = 0.5f;       // an enemy tower at or below this HP fraction is finished, tower fire or not
  float finishSelfHp = 0.5f;        // ... by a bot with at least this HP fraction
  float weakAbsolute = 0.35f;       // below this HP fraction a bot does not start a fight
  float weakMargin = 0.15f;         // ... nor when its HP fraction is lower than the enemy's by more than this
  long long hitWindowMs = 3000;     // an enemy shaper's hit counts as an attack this long
  float jungleEngageRange = 38.f;   // an enemy shaper in the jungle is engaged from this far
  float retreatRange = 26.f;        // a stronger enemy in the jungle this close makes the bot back off
  float jungleLaneDistance = 22.f;  // farther than this from every lane = jungle (lane minions stay 20+ from creeps)
};

// What the host measured for one bot at one think.
struct ReflexSensors {
  int role = 0;                    // mRole
  float hpFrac = 1.f;              // own HP / max HP
  bool inTowerZone = false;        // within towerRange of a standing tower that hit the bot within towerZoneMs
  bool targetIsShaper = false;     // its current target (mTarget / best target) is an enemy shaper
  float targetHpFrac = 1.f;
  bool targetInTowerZone = false;  // that target stands within towerRange of a standing enemy tower
  bool engagePlan = false;         // the bot's plan is EngageForKill
  bool targetIsTower = false;      // its current target is an enemy tower
  float targetTowerHpFrac = 1.f;
  long long shaperHitAgeMs = -1;   // since an enemy shaper last hit it (-1 = never / dead / out of reach)
  float attackerHpFrac = 1.f;
  bool jungleEnemy = false;        // a visible enemy shaper in the jungle within jungleEngageRange
  float jungleEnemyHpFrac = 1.f;
  float jungleEnemyDist = 0.f;
  int enemiesNear = 0;             // enemy shapers within 35 (visible)
  int alliesNear = 0;              // allied shapers within 35 (not itself)
  bool wellThreat = false;         // a well of its team on its side of the map is attacked or being captured
};

struct ReflexDecision {
  ReflexKind kind = kReflexNone;
  const char* why = "";
};

inline bool ReflexIsJungler(int role) { return role == kReflexRoleHunter || role == kReflexRolePredator; }

// The bot would lose the fight: low in itself, or clearly lower than the enemy's.
inline bool ReflexWeaker(float mine, float theirs, const ReflexConfig& c) {
  return mine < c.weakAbsolute || mine + c.weakMargin < theirs;
}

inline bool ReflexOutnumbered(const ReflexSensors& s) { return s.enemiesNear > s.alliesNear + 1; }

// Diving under a tower for a kill: a weak enemy shaper under it, and the bot itself in shape.
inline bool ReflexDiving(const ReflexSensors& s, const ReflexConfig& c) {
  if (s.targetIsTower && s.targetTowerHpFrac <= c.finishTowerHp && s.hpFrac >= c.finishSelfHp) return true;  // finishing a tower
  if (!s.targetIsShaper || !s.targetInTowerZone || s.hpFrac < c.diveSelfHp) return false;
  return s.targetHpFrac <= c.diveTargetHp || (s.engagePlan && s.targetHpFrac <= c.diveEngagedHp);
}

inline ReflexDecision DecideReflex(const ReflexSensors& s, const ReflexConfig& c = ReflexConfig()) {
  ReflexDecision d;
  if (s.inTowerZone && !ReflexDiving(s, c)) {
    d.kind = kReflexBack;
    d.why = "tower";
    return d;
  }
  if (!ReflexIsJungler(s.role)) return d;
  if (s.shaperHitAgeMs >= 0 && s.shaperHitAgeMs <= c.hitWindowMs) {
    if (ReflexWeaker(s.hpFrac, s.attackerHpFrac, c) || ReflexOutnumbered(s)) {
      d.kind = kReflexBack;
      d.why = "outmatched";
    } else {
      d.kind = kReflexFight;
      d.why = "retaliate";
    }
    return d;
  }
  if (s.wellThreat && s.hpFrac >= c.weakAbsolute) {
    d.kind = kReflexDefend;
    d.why = "well";
    return d;
  }
  if (s.jungleEnemy && s.jungleEnemyDist <= c.jungleEngageRange) {
    if (ReflexWeaker(s.hpFrac, s.jungleEnemyHpFrac, c) || ReflexOutnumbered(s)) {
      if (s.jungleEnemyDist <= c.retreatRange) {
        d.kind = kReflexBack;
        d.why = "jungle outmatched";
      }
    } else {
      d.kind = kReflexFight;
      d.why = "jungle";
    }
  }
  return d;
}

}  // namespace agents
}  // namespace gms
