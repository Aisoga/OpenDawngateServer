// gms/core/damage/upkeep.h (DISCIPLINE): an ability's upkeep cost, the resource a running ability spends over time,
// which the client build lost with the server's frame update. Header-only, no game addresses: the harness
// (gms/engine/abilities.cpp) reads the values from the ability's static data and applies them with these rules.
//   - The data [code, script]: cStaticAbility +0x340 upkeepResourcePointCost (float x 8, by rank) and +0x360
//     upkeepResourcePointTickRate (float, seconds); the reflection (0x01104EA0) loads them from the script and the copy
//     (0x0090E90D) duplicates them; no other client instruction reads them. The one ability with an upkeep is Raina's W
//     (ability_Guardian_W_ConeShield: 1.25 every 0.1 s, so 12.5 Discipline a second, while the shield is up).
//   - [assumed] the first payment one tick after the ability started, then one per tick while the instance lives; a
//     tick that finds less than the cost ends the ability instead (the client refuses an activation the same way:
//     LabsAbilities_ValidateRequest 0x009053C0 returns "insufficient resource" for points < cost); a long gap (a stall)
//     pays at most kUpkeepMaxTicksPerStep ticks.
#pragma once

namespace gms::damage {

constexpr int kUpkeepMaxTicksPerStep = 10;

// The upkeep array's entry for `rank` (1-based, clamped to 1..8 as the client's cost getters clamp the rank).
inline float UpkeepCostForRank(const float* costs, int rank) {
  if (!costs) return 0.f;
  int i = rank < 1 ? 1 : (rank > 8 ? 8 : rank);
  return costs[i - 1];
}

struct UpkeepResult {
  float resource;               // the resource after the ticks paid
  unsigned long long lastTickMs;  // the time of the last tick counted (paid or failed)
  int ticksPaid;
  bool end;                     // a tick found less than the cost: end the ability
};

// The ticks due between `lastTickMs` (the instance's start, then the last tick) and `nowMs`, each paying `cost` from
// `resource`; stops at the first tick that can't pay (end = true). Nothing is due for a cost or a tick rate <= 0.
inline UpkeepResult UpkeepStep(float resource, float cost, float tickSeconds, unsigned long long lastTickMs,
                               unsigned long long nowMs) {
  UpkeepResult r{resource, lastTickMs, 0, false};
  if (!(cost > 0.f) || !(tickSeconds > 0.f) || nowMs <= lastTickMs) return r;
  unsigned long long tickMs = static_cast<unsigned long long>(tickSeconds * 1000.f + 0.5f);
  if (tickMs == 0) tickMs = 1;
  unsigned long long due = (nowMs - lastTickMs) / tickMs;
  if (due == 0) return r;
  const bool stalled = due > static_cast<unsigned long long>(kUpkeepMaxTicksPerStep);
  if (stalled) due = kUpkeepMaxTicksPerStep;
  for (unsigned long long k = 0; k < due; ++k) {
    r.lastTickMs += tickMs;
    if (r.resource < cost) {
      r.end = true;
      break;
    }
    r.resource -= cost;
    ++r.ticksPaid;
  }
  // after a stall the clock catches up instead of paying the whole gap later
  if (stalled && !r.end) r.lastTickMs = nowMs - (nowMs - lastTickMs) % tickMs;
  return r;
}

}  // namespace gms::damage
