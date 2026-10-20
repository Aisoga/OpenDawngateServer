// gms/core/agents (MIKELLABOT): Mikella's kit layer on the High Huntress's Gladiator AI, without the engine. Header-only.
//
// A 2014 player guide (a Gladiator Mikella, Q maxed first, E second): Q is the harass and farming tool; W (the coins)
// wins trades: the next three basic attacks deal 20/40/60 % more; E (Waltz) is the escape and the reposition, "not for
// damage"; R (a barrage at one shaper) before a fight or to assassinate or force someone back after it, never in the
// middle of one (the channel costs her basic attacks). The kit in build 854 [script, data]:
//   Basic  ability_MerchantPrincess_Basic     a target within 16.18
//   Q      ability_MerchantPrincess_Q_Return  a line at a point, 24.27 long, out and back, slows (cooldown 10-8)
//   W      ability_MerchantPrincess_W_TriShot self: resets the basic's cooldown, the next three basics x1.2/1.4/1.6 within
//                                             5 s (cooldown 16-10)
//   E      ability_MerchantPrincess_E_Spin    a charge of 11.77 toward a point (speed 50) firing three shots at the
//                                             nearest enemies within 16 on the way (cooldown 14-10)
//   R      ability_MerchantPrincess_R_Barrage an enemy shaper within 55.89, a 1-s channel, six shots (cooldown 140-100)
// The donor AI (BOT_HighHuntress_Gladiator, Nissa's) names slots: its Harass and LastHit phases throw Q at enemies in a
// line (that fits); its E gambits aim at the nearest enemy shaper (40), which for Mikella is a charge into the enemy;
// its W (Nissa's steroid) fires on entering EngageForKill and in Flee and on slows (the coins without a target); its R
// fires on anyone in a line. So the host (gms/engine/agents.cpp MikellaKitStep, KitAllowsNow) leaves the plan's E and R
// gambits out and her W unless an enemy shaper is within wNear, and DecideMikella casts, once per think:
//   1. the escape E (Flee phases): away from the visible enemy shapers within fleeNear;
//   2. R (lane and fight phases): at the lowest visible enemy shaper within rRange at or below rLowHp (assassinate), or
//      at or below rPokeHp while no visible enemy shaper is within rClear of her (before or after the fight);
//   3. the trade W (lane and fight phases): her target is a visible enemy shaper within wNear;
//   4. the kite E (lane and fight phases): a visible enemy shaper within kiteNear: the charge away from it (its shots
//      still hit it, they fire at enemies within 16 during the charge);
//   5. the chase E (fight phases): her target, a visible enemy shaper at or below chaseHp, beyond her basic's reach and
//      within chaseMax: the charge toward it.
// Every threshold not from the data is [assumed].
#pragma once

#include <cmath>
#include <cstdint>
#include <cstring>
#include <vector>

#include "brain.h"  // kSlot*

namespace gms::agents {

struct MikellaConfig {
  float basicReach = 16.18f;  // ability_MerchantPrincess_Basic's range
  float wNear = 17.f;         // the trade W (and the plan's W gambits): an enemy shaper within her reach and a margin
  float eLength = 11.77f;     // E_Spin's charge
  float kiteNear = 10.f;      // the kite E: an enemy shaper this close [assumed]
  float chaseMax = 26.f;      // the chase E: the target within this (her reach + the charge, less a margin) [assumed]
  float chaseHp = 0.35f;      // ... at or below this HP fraction [assumed]
  float fleeNear = 20.f;      // the escape E: an enemy shaper within this while fleeing [assumed]
  float rRange = 50.f;        // R_Barrage's 55.89, less a margin for the target's walk
  float rLowHp = 0.35f;       // R to assassinate [assumed]
  float rPokeHp = 0.60f;      // R before or after a fight [assumed]
  float rClear = 20.f;        // ... with no enemy shaper this close to her [assumed]
  long long rHoldMs = 1200;   // after a started R the layer holds the think (the 1-s channel) [assumed]
  long long retryMs = 1000;   // a refused cast of the layer is tried again after this
  long long cooldownRetryMs = 3000;  // ... after this when it was on cooldown (-9999: live, 317 R tries for 13 starts)
};

// The phases the layer acts in, by the High Huntress AI's phase names (BOT_HighHuntress_*, HighHuntress_Gladiator_*).
enum MikellaPhaseClass : int { kMikPhaseOther = 0, kMikPhaseLane = 1, kMikPhaseFight = 2, kMikPhaseFlee = 3 };

inline int MikellaPhaseClassOf(const char* phase) {
  if (!phase) return kMikPhaseOther;
  if (std::strstr(phase, "Flee")) return kMikPhaseFlee;  // Flee, FleeTowerTargeting
  static const char* const kFight[] = {"EngageForKill", "SelfDefense", "BackToAction", "Gather", "MoveToMissionTarget",
                                       "RallyForSiege", "WellFight", "FightParasite", "AttackBase", "BaseDefend"};
  static const char* const kLane[] = {"Harass", "LastHit", "Push", "DontPush", "Reposition"};
  for (const char* f : kFight)
    if (std::strstr(phase, f)) return kMikPhaseFight;
  for (const char* l : kLane)
    if (std::strstr(phase, l)) return kMikPhaseLane;  // TowerPush counts as Push
  return kMikPhaseOther;
}

// One enemy shaper as the host saw it this think.
struct MikEnemy {
  std::uint32_t id = 0;
  float pos[2] = {};
  float dist = 0.f;  // from her
  float hpFrac = 1.f;
  bool visible = false;
};

struct MikSelf {
  float pos[2] = {};
  int phaseClass = kMikPhaseOther;
  long long nowMs = 0;
  int rankW = 0, rankE = 0, rankR = 0;
  int target = -1;  // her target (mTarget) among the enemies, -1 when it is no enemy shaper
};

enum MikKind : int { kMikNone = 0, kMikEscapeE, kMikR, kMikTradeW, kMikKiteE, kMikChaseE, kMikKinds };

inline const char* MikKindName(int k) {
  switch (k) {
    case kMikEscapeE: return "escape E";
    case kMikR: return "R";
    case kMikTradeW: return "trade W";
    case kMikKiteE: return "kite E";
    case kMikChaseE: return "chase E";
    default: return "none";
  }
}

// What the layer remembers per bot between thinks.
struct MikMemory {
  long long nextTryMs[kMikKinds] = {};  // per kind: after a refusal
  long long holdUntilMs = -1;           // a started R's channel
};

struct MikAction {
  int kind = kMikNone;
  int enemy = -1;          // index into the enemies (the escape E: the nearest one it flees)
  float point[2] = {};     // the E's point; the R's target position
  const char* why = "";
};

// The point `len` from `from` in the direction (dx, dy); false when the direction is null or the point would be the
// null position (NULLPOS: (0, 0) is "no point" for the bots).
inline bool MikPointToward(const float* from, float dx, float dy, float len, float* out) {
  const float n = std::sqrt(dx * dx + dy * dy);
  if (n < 1e-3f) return false;
  out[0] = from[0] + dx / n * len;
  out[1] = from[1] + dy / n * len;
  return !(out[0] == 0.f && out[1] == 0.f);
}

// Whether the layer holds this think (a started R channels).
inline bool MikHolding(const MikMemory& m, long long now) { return m.holdUntilMs >= 0 && now < m.holdUntilMs; }

inline MikAction DecideMikella(const MikSelf& s, const std::vector<MikEnemy>& enemies, const MikMemory& m,
                               const MikellaConfig& c = MikellaConfig()) {
  MikAction a;
  const long long now = s.nowMs;
  auto ready = [&](int k) { return now >= m.nextTryMs[k]; };
  int nearest = -1;
  for (std::size_t i = 0; i < enemies.size(); ++i) {
    const MikEnemy& e = enemies[i];
    if (!e.visible || e.hpFrac <= 0.f) continue;
    if (nearest < 0 || e.dist < enemies[static_cast<std::size_t>(nearest)].dist) nearest = static_cast<int>(i);
  }
  const MikEnemy* closest = nearest >= 0 ? &enemies[static_cast<std::size_t>(nearest)] : nullptr;
  // 1. the escape E: away from the enemy shapers near (their centre)
  if (s.phaseClass == kMikPhaseFlee) {
    if (s.rankE <= 0 || !ready(kMikEscapeE) || !closest || closest->dist > c.fleeNear) return a;
    float cx = 0.f, cy = 0.f;
    int n = 0;
    for (const MikEnemy& e : enemies)
      if (e.visible && e.hpFrac > 0.f && e.dist <= c.fleeNear) {
        cx += e.pos[0];
        cy += e.pos[1];
        ++n;
      }
    cx /= static_cast<float>(n);
    cy /= static_cast<float>(n);
    if (MikPointToward(s.pos, s.pos[0] - cx, s.pos[1] - cy, c.eLength, a.point) ||
        MikPointToward(s.pos, s.pos[0] - closest->pos[0], s.pos[1] - closest->pos[1], c.eLength, a.point)) {
      a.kind = kMikEscapeE;
      a.enemy = nearest;
      a.why = n > 1 ? "away from the enemy shapers near" : "away from the enemy shaper near";
    }
    return a;
  }
  if (s.phaseClass != kMikPhaseLane && s.phaseClass != kMikPhaseFight) return a;
  // 2. R: assassinate a low one, or poke before or after the fight
  if (s.rankR > 0 && ready(kMikR)) {
    int low = -1;
    for (std::size_t i = 0; i < enemies.size(); ++i) {
      const MikEnemy& e = enemies[i];
      if (!e.visible || e.hpFrac <= 0.f || e.dist > c.rRange) continue;
      if (low < 0 || e.hpFrac < enemies[static_cast<std::size_t>(low)].hpFrac) low = static_cast<int>(i);
    }
    if (low >= 0) {
      const MikEnemy& t = enemies[static_cast<std::size_t>(low)];
      const bool clear = !closest || closest->dist > c.rClear;
      if (t.hpFrac <= c.rLowHp || (t.hpFrac <= c.rPokeHp && clear)) {
        a.kind = kMikR;
        a.enemy = low;
        a.point[0] = t.pos[0];
        a.point[1] = t.pos[1];
        a.why = t.hpFrac <= c.rLowHp ? "assassinate" : "before or after the fight";
        return a;
      }
    }
  }
  const MikEnemy* tgt = s.target >= 0 && s.target < static_cast<int>(enemies.size())
                            ? &enemies[static_cast<std::size_t>(s.target)]
                            : nullptr;
  if (tgt && (!tgt->visible || tgt->hpFrac <= 0.f)) tgt = nullptr;
  // 3. the trade W: her target an enemy shaper within her reach
  if (s.rankW > 0 && ready(kMikTradeW) && tgt && tgt->dist <= c.wNear) {
    a.kind = kMikTradeW;
    a.enemy = s.target;
    a.point[0] = s.pos[0];
    a.point[1] = s.pos[1];
    a.why = "her target within reach";
    return a;
  }
  // 4. the kite E: an enemy shaper too close, the charge away from it
  if (s.rankE > 0 && ready(kMikKiteE) && closest && closest->dist <= c.kiteNear &&
      MikPointToward(s.pos, s.pos[0] - closest->pos[0], s.pos[1] - closest->pos[1], c.eLength, a.point)) {
    a.kind = kMikKiteE;
    a.enemy = nearest;
    a.why = "an enemy shaper too close";
    return a;
  }
  // 5. the chase E: a low target beyond her reach
  if (s.phaseClass == kMikPhaseFight && s.rankE > 0 && ready(kMikChaseE) && tgt && tgt->hpFrac <= c.chaseHp &&
      tgt->dist > c.basicReach && tgt->dist <= c.chaseMax &&
      MikPointToward(s.pos, tgt->pos[0] - s.pos[0], tgt->pos[1] - s.pos[1], c.eLength, a.point)) {
    a.kind = kMikChaseE;
    a.enemy = s.target;
    a.why = "a low target beyond her reach";
    return a;
  }
  return a;
}

// After the host's request for `a`: a refused one waits retryMs (cooldownRetryMs when the slot was on cooldown), a
// started R holds the think for the channel.
inline void MikNoteResult(MikMemory* m, const MikAction& a, bool started, long long now,
                          const MikellaConfig& c = MikellaConfig(), bool onCooldown = false) {
  if (a.kind <= kMikNone || a.kind >= kMikKinds) return;
  if (!started) {
    m->nextTryMs[a.kind] = now + (onCooldown ? c.cooldownRetryMs : c.retryMs);
    return;
  }
  if (a.kind == kMikR) m->holdUntilMs = now + c.rHoldMs;
}

// Whether the plan's gambit (or BOTREFLEX's attack) may request `slot` for her: the layer casts her E and R itself;
// her W only with a living visible enemy shaper within wNear (`nearestEnemy`, negative: none).
inline bool MikellaAllowsSlot(int slot, float nearestEnemy, const MikellaConfig& c = MikellaConfig()) {
  if (slot == kSlotQ + 2 || slot == kSlotR) return false;                                      // E, R
  if (slot == kSlotQ + 1) return nearestEnemy >= 0.f && nearestEnemy <= c.wNear;  // W
  return true;
}

}  // namespace gms::agents
