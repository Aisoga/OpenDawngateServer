// gms/core/damage/regen.h (D1i): health and resource regeneration, which the client build lost with the server's frame
// update (docs/DAMAGE.md *D1i: regeneration on the host*). Header-only, no game addresses: the harness
// (gms/engine/regen.cpp) reads the rates with the client's own attribute getters and applies them with these rules.
//   - Units: every regen attribute is "per 5 seconds" [data, code, script]: the HUD labels HealthRegen's total and
//     ResourcePointRegen "Hp5"/"Mp5" (AbilityPanelUI.cpp's mHpRegen/mMpRegen, 0x004060f0/0x00408130), the perk texts
//     read "+1.4 Health Regen Per 5 Seconds", modifier_BaseTitan_V5_Passive sets maxHealth / (240 / 5) for "the bar over
//     240 s", modifier_Savage_Passive sets ResourcePointRegen to attacksPerSecond x 5.
//   - The health rate is the derived TotalHealthRegen (nDerivedAttributeType 105, AttributeData's table 0x01246814 ->
//     0x008db1a0): HealthRegen x (1 + HealIncrease) x (1 - HealingReduction) [code]; HealthRegen itself is
//     ClassAttributes.baseHealthRegen + (level - 1) x healthRegenPerLevel + the modifiers (0x008d7340, 0x008d7470).
//   - [assumed] the resource rate is ResourcePointRegen + ResourcePointPercentRegen x MaxResourcePoints per 5 s, none
//     while DisableResourcePointRegen > 0; a health-resource shaper's resource is its HP (CombatantData_SetResourcePoints
//     0x0087E200 turns a resource change into damage for resource type 8), so it gets only the health regen.
//   - [assumed] nothing for the dead (HP <= 0) and no degeneration (a rate <= 0 does nothing); HP stops at the top of
//     the current health bar (a tiered Guardian regains "the current health bar", modifier_BaseTitan_V5_Passive), the
//     resource at its maximum.
#pragma once

namespace gms::damage {

constexpr float kRegenPeriodSeconds = 5.f;
constexpr int kResourceTypeHealth = 8;  // nCharacterResourceType.Health

// TotalHealthRegen as the client's derived getter computes it (0x008db1a0).
inline float TotalHealthRegen(float healthRegen, float healIncrease, float healingReduction) {
  return healthRegen * (1.f + healIncrease) * (1.f - healingReduction);
}

// HP after `seconds` of regeneration at `per5` (TotalHealthRegen), never above `cap`; unchanged for the dead, for a rate
// <= 0 and for HP already at or above the cap.
inline float RegenHealth(float hp, float per5, float cap, float seconds) {
  if (!(hp > 0.f) || !(per5 > 0.f) || !(seconds > 0.f) || hp >= cap) return hp;
  const float next = hp + per5 * seconds / kRegenPeriodSeconds;
  return next < cap ? next : cap;
}

// The resource's rate per 5 s [assumed]: flat + percent x max; 0 while disabled.
inline float ResourceRegenPer5(float flatPer5, float percentPer5, float maxPoints, bool disabled) {
  if (disabled) return 0.f;
  return flatPer5 + percentPer5 * (maxPoints > 0.f ? maxPoints : 0.f);
}

// The resource after `seconds`, never above `maxPoints`; unchanged for a rate <= 0 or a resource already full.
inline float RegenResource(float current, float per5, float maxPoints, float seconds) {
  if (!(per5 > 0.f) || !(seconds > 0.f) || !(maxPoints > 0.f) || current >= maxPoints) return current;
  const float next = current + per5 * seconds / kRegenPeriodSeconds;
  return next < maxPoints ? next : maxPoints;
}

// The top of health bar `current` (0 = the first bar, lost first; GameObject_GetCurrentHealthBar 0x0087E420 counts
// the same way): the maximum minus the bars before it. bars[i] is bar i's health (GameObject_GetHealthBarMax
// 0x0087E2F0). An object without tiers has one bar, the maximum.
inline float HealthBarTop(float maxHp, const float* bars, int count, int current) {
  float top = maxHp;
  for (int i = 0; i < current && i < count; ++i) top -= bars[i];
  return top;
}

}  // namespace gms::damage
