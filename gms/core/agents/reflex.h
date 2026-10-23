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
//   3. (Hunter, Predator; since WELLDEF also Tacticians) a spirit well of its team is attacked or being captured on its
//               side of the map -> defend it (fight whoever attacks, else walk to the well). Since WELLDEF (host
//               `bot_well_defense`, on) which well, how far, for how long and when to give up is welldef.h's
//               StepWellDefense; `wellThreat` is its answer. A Tactician has no rules 2 and 4: its plans fight shapers.
//   4. (Hunter, Predator) an enemy shaper in the jungle within reach -> engage it, unless it is the weaker one or
//               outnumbered: then back off when the enemy is close, else leave it.
//
// "The weaker one" (PRETUSBOT, 2026-10-18): BOTREFLEX compared HP fractions with a 15-point margin, so a Hunter fresh
// from a camp (60-85 %) was the weaker one against every full-HP shaper and let it pass (46 % of the user's server's
// snapshots with an enemy bot within 20 of a Hunter). With `junglerHpPoints` (host `bot_jungle_fight`, on) a jungler
// compares HP points, which carry the levels and items: weaker below `weakAbsolute` of its own HP, or with less than
// `weakHpRatio` of the enemy's current HP [assumed thresholds].
#pragma once

namespace gms {
namespace agents {

constexpr int kReflexRoleTactician = 1;  // RolesTuning.role[] index (mRole)
constexpr int kReflexRoleHunter = 2;
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
  float finishTowerShotHp = 0.5f;   // BOTTOWER: ... at or below this while that tower hits or targets the bot (the host
                                    // sets towersafety.h's 0.2 with bot_tower_aware; two bots died finishing a 50 % tower)
  float weakAbsolute = 0.35f;       // below this HP fraction a bot does not start a fight
  float weakMargin = 0.15f;         // ... nor when its HP fraction is lower than the enemy's by more than this
  long long hitWindowMs = 3000;     // an enemy shaper's hit counts as an attack this long
  float jungleEngageRange = 38.f;   // an enemy shaper in the jungle is engaged from this far
  float retreatRange = 26.f;        // a stronger enemy in the jungle this close makes the bot back off
  float jungleLaneDistance = 22.f;  // farther than this from every lane = jungle (lane minions stay 20+ from creeps)
  bool junglerHpPoints = true;      // PRETUSBOT: a jungler's "weaker" compares HP points (false: fractions, BOTREFLEX)
  float weakHpRatio = 0.75f;        // ... weaker with less than this share of the enemy's current HP points
  bool wellTacticians = false;      // WELLDEF: Tacticians take rule 3 too (the host sets it with bot_well_defense)
  bool noShaperDive = false;        // DESECBOT-3: never under a tower for a shaper (a Desecrator at level 1-3)
};

// What the host measured for one bot at one think.
struct ReflexSensors {
  int role = 0;                    // mRole
  float hpFrac = 1.f;              // own HP / max HP
  float hp = 0.f;                  // own HP points (PRETUSBOT; 0 = unknown: the fractions decide)
  bool inTowerZone = false;        // within towerRange of a standing tower that hit the bot within towerZoneMs (BOTTOWER:
                                   // any tower danger of towersafety.h's TowerDanger)
  int towerKind = 1;               // BOTTOWER: which danger (towersafety.h TowerDangerKind; 1 the hit, 2 targeted,
                                   // 3 no minions), for the reason's name
  bool targetIsShaper = false;     // its current target (mTarget / best target) is an enemy shaper
  float targetHpFrac = 1.f;
  bool targetInTowerZone = false;  // that target stands within towerRange of a standing enemy tower
  bool engagePlan = false;         // the bot's plan is EngageForKill
  bool targetIsTower = false;      // its current target is an enemy tower
  float targetTowerHpFrac = 1.f;
  long long shaperHitAgeMs = -1;   // since an enemy shaper last hit it (-1 = never / dead / out of reach)
  float attackerHpFrac = 1.f;
  float attackerHp = 0.f;          // its HP points (PRETUSBOT; 0 = unknown)
  bool jungleEnemy = false;        // a visible enemy shaper in the jungle within jungleEngageRange
  float jungleEnemyHpFrac = 1.f;
  float jungleEnemyHp = 0.f;       // its HP points (PRETUSBOT; 0 = unknown)
  float jungleEnemyDist = 0.f;
  int enemiesNear = 0;             // enemy shapers within 35 (visible)
  int alliesNear = 0;              // allied shapers within 35 (not itself)
  bool wellThreat = false;         // a well of its team on its side of the map is attacked or being captured (WELLDEF:
                                   // StepWellDefense says defend)
  bool finishing = false;          // KILLFINISH: it finishes a won trade on its target (killfinish.h; that decision's
                                   // own tower test passed): a dive
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

// PRETUSBOT: a jungler (Hunter, Predator) against one enemy shaper. With junglerHpPoints and both HP points known it is
// the weaker one below weakAbsolute of its own HP or with less than weakHpRatio of the enemy's current HP points;
// otherwise (another role, the option off, a value unknown) BOTREFLEX's fractions decide.
inline bool ReflexJunglerWeaker(const ReflexSensors& s, float theirsFrac, float theirsHp, const ReflexConfig& c) {
  if (!c.junglerHpPoints || !ReflexIsJungler(s.role) || !(s.hp > 0.f) || !(theirsHp > 0.f))
    return ReflexWeaker(s.hpFrac, theirsFrac, c);
  return s.hpFrac < c.weakAbsolute || s.hp < c.weakHpRatio * theirsHp;
}

inline bool ReflexOutnumbered(const ReflexSensors& s) { return s.enemiesNear > s.alliesNear + 1; }

// Diving under a tower for a kill: a weak enemy shaper under it, and the bot itself in shape.
inline bool ReflexDiving(const ReflexSensors& s, const ReflexConfig& c) {
  const float towerFinish = s.inTowerZone && (s.towerKind == 1 || s.towerKind == 2) ? c.finishTowerShotHp : c.finishTowerHp;
  if (s.targetIsTower && s.targetTowerHpFrac <= towerFinish && s.hpFrac >= c.finishSelfHp) return true;  // finishing a tower
  if (c.noShaperDive || !s.targetIsShaper) return false;
  if (s.finishing) return true;  // KILLFINISH: a near-certain kill under the tower
  if (!s.targetInTowerZone || s.hpFrac < c.diveSelfHp) return false;
  return s.targetHpFrac <= c.diveTargetHp || (s.engagePlan && s.targetHpFrac <= c.diveEngagedHp);
}

inline ReflexDecision DecideReflex(const ReflexSensors& s, const ReflexConfig& c = ReflexConfig()) {
  ReflexDecision d;
  if (s.inTowerZone && !ReflexDiving(s, c)) {
    d.kind = kReflexBack;
    d.why = s.towerKind == 2 ? "tower targets it" : s.towerKind == 3 ? "tower, no minions" : "tower";  // all 't...'
    return d;
  }
  if (!ReflexIsJungler(s.role)) {
    if (c.wellTacticians && s.role == kReflexRoleTactician && s.wellThreat && s.hpFrac >= c.weakAbsolute) {
      d.kind = kReflexDefend;
      d.why = "well";
    }
    return d;
  }
  if (s.shaperHitAgeMs >= 0 && s.shaperHitAgeMs <= c.hitWindowMs) {
    if (ReflexJunglerWeaker(s, s.attackerHpFrac, s.attackerHp, c) || ReflexOutnumbered(s)) {
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
    if (ReflexJunglerWeaker(s, s.jungleEnemyHpFrac, s.jungleEnemyHp, c) || ReflexOutnumbered(s)) {
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
