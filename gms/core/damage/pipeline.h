// gms/core/damage/pipeline.h (D13): the damage core's steps and the combat edge cases' rules without the engine
// (docs/DAMAGE.md *Combat edge cases (D13)*). Header-only, no game addresses. The harness (gms/engine/damage.cpp)
// runs a hit through RunHit with hooks that call the client's own functions and the scripts' handlers; gms_tests runs it
// with models of the scripts (gms/tests/test_damage_edge.cpp). What is here:
//   - the hit's steps from the initial damage to the HP (DAMAGE.md *The damage pipeline* steps 3-9): crit, the
//     ModifyDamage events, mitigation, the absorb passes over the shields, HP and integerHpChange;
//   - the shields' order (the one that expires first absorbs first, untimed ones last) and a handler's clamped result;
//   - the client's mitigation formulas (TotalArmor/TotalMR with shred, the multiplier with penetration), mirrored for
//     tests: the host itself calls Damage_GetPhysicalMultiplier/_GetMagicalMultiplier (0x008DFEB0/0x008DFFF0);
//   - what follows a hit: attacker bookkeeping, the damage events, lifedrain, the spell overload, the aggro hook;
//   - a health-resource shaper's cost (the pool HP + mShieldHealth, the refusal, the payment as self-inflicted damage);
//   - the heal core (0x008E0720) and the Lua heal binding's rule (0x009401B0), mirrored for tests;
//   - modifier lifetime: OnDuration's expiry (moved here from gms/engine/abilities.cpp) and what a death ends (from
//     gms/engine/death.cpp).
// [code] = read from the client at the address given; [script] = ServerData Lua; [assumed] = no evidence, a choice.
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>

#include "core/damage/rules.h"

namespace gms::damage {

// ---- mitigation: the client's formulas ---------------------------------------------------------------------------

// AttributeData_GetTotalArmor (0x008DA880) [code]: PhysicalDamageReduction (24) x (1 + ArmorReductionPercentDecrease
// (27)) + ArmorReductionFlatDecrease (26); a structure ignores the shred. The shred values are negative (e.g.
// modifier_Artist_Q_Shred -15..-35). For negative armor the same product: a percentage shred brings it toward 0.
inline float TotalArmor(float armor, float pctDecrease, float flatDecrease, bool structure) {
  if (structure) return armor;
  return (pctDecrease + 1.f) * armor + flatDecrease;
}
// AttributeData_GetTotalMR (0x008DAAF0) [code]: MagicalDamageReduction (25) with MagicResistReduction*Decrease
// (29/28); below 0 the percentage works the other way (MR x (1 - pct)), so a percentage shred makes negative MR more
// negative, unlike armor.
inline float TotalMR(float mr, float pctDecrease, float flatDecrease, bool structure) {
  if (structure) return mr;
  if (mr >= 0.f) return flatDecrease + mr * (pctDecrease + 1.f);
  return flatDecrease + (mr - pctDecrease * mr);
}
// Damage_GetPhysicalMultiplier (0x008DFEB0) and Damage_GetMagicalMultiplier (0x008DFFF0) [code], identical apart from
// the attributes: total = TotalArmor/TotalMR (86/90), raw = RawArmor/RawMR (87/91). Below 0: 2 - 100 / (100 - total),
// no penetration. Otherwise, when the hit has a snapshot and the target is no structure (`penetrate`), with the
// attacker's snapshot attributes: (total - PercentBonusDefensePenetration (32) x max(0, total - raw)) x
// (1 - PercentDefensePenetration (31)), then max(0, that - DefensePenetration (30)); the result 100 / (100 + d). The
// same three attributes act on armor and magic resistance.
inline float DefenseMultiplierWithPenetration(float total, float raw, bool penetrate, float flatPen, float pctPen,
                                              float pctBonusPen) {
  if (total < 0.f) return 2.f - 100.f / (100.f - total);
  float d = total;
  if (penetrate) {
    const float bonus = std::max(0.f, total - raw);
    d = (total - pctBonusPen * bonus) * (1.f - pctPen);
    d = std::max(0.f, d - flatPen);
  }
  return 100.f / (d + 100.f);
}
// Which multiplier a hit takes (DAMAGE.md step 6): by its damage type only, so a basic attack (IsBasic |
// IsPhysicalDamage, every shaper's and minion's basic) is mitigated like an ability; true damage takes none.
inline bool UsesPhysicalDefense(unsigned descLo) { return !(descLo & kIsTrueDamage) && (descLo & kIsPhysical) != 0; }
inline bool UsesMagicalDefense(unsigned descLo) { return !(descLo & kIsTrueDamage) && (descLo & kIsMagical) != 0; }

// ---- shields ------------------------------------------------------------------------------------------------------

// nAbilityFns indices of the absorb handlers (DAMAGE.md *Damage events*).
constexpr int kAbsorbGeneric = 14, kAbsorbMagical = 15, kAbsorbPhysical = 16;
constexpr int kAbsorbAvailable = 17, kAbsorbAvailableMagical = 18, kAbsorbAvailablePhysical = 19;
constexpr long long kUntimedMs = 0x7FFFFFFFFFFFLL;

// A modifier's time left: start (+0x208) + duration (+0x220) - now; a duration <= 0 is no time limit (kUntimedMs).
// nModifier.ResetDuration sets the start to now (0x008C7650), nModifier.SetDuration the duration (0x008C7A40) [code].
inline long long TimeLeftMs(int durationMs, std::uint64_t startMs, std::uint64_t nowMs) {
  if (durationMs <= 0) return kUntimedMs;
  return static_cast<long long>(startMs + static_cast<std::uint64_t>(durationMs)) - static_cast<long long>(nowMs);
}

struct ShieldRef {
  std::uint32_t id = 0;       // the modifier instance
  long long leftMs = kUntimedMs;
  void* handle = nullptr;     // the engine's instance pointer (opaque here)
};
constexpr int kMaxShields = 32;

// The order of one absorb pass [assumed, DAMAGE.md step 8]: the shield that expires first absorbs first (the order the
// client's ModifyDamage dispatcher sorts modifiers in, 0x009052F0), untimed ones last; ties keep the container's order.
inline void SortShields(ShieldRef* s, int n) {
  std::stable_sort(s, s + n, [](const ShieldRef& a, const ShieldRef& b) { return a.leftMs < b.leftMs; });
}
// The typed pass before the generic one [assumed]: physical damage AbsorbDamage_Physical (16), else magical damage
// AbsorbDamage_Magical (15); true damage (and a hit of no type) only the generic AbsorbDamage (14).
inline int TypedAbsorbIndex(unsigned descLo) {
  if (descLo & kIsTrueDamage) return 0;
  if (descLo & kIsPhysical) return kAbsorbPhysical;
  if (descLo & kIsMagical) return kAbsorbMagical;
  return 0;
}
// A handler returns the damage it lets through; NaN or below 0 counts as all absorbed, more than it got as nothing.
inline float ClampAbsorbResult(float r, float left) {
  if (!(r >= 0.f)) return 0.f;
  return r > left ? left : r;
}

// ---- one hit ------------------------------------------------------------------------------------------------------

// What RunHit asks of the world. The engine's implementation calls the client's dispatchers and the scripts' handlers.
class HitHooks {
 public:
  virtual ~HitHooks() = default;
  virtual float Outgoing(float d) = 0;  // ModifyDamageOutgoing (11) on the attacker, other object = target
  virtual float Incoming(float d) = 0;  // ModifyDamageIncoming (12) on the target, other object = attacker
  virtual float Mitigation() = 0;       // the defense multipliers and the extra reductions (not for true damage)
  virtual float Final(float d) = 0;     // ModifyDamageFinal (13) on the target
  // The target's modifiers with absorb handler `index` that aren't ending, at most `cap` (unsorted).
  virtual int Shields(int index, ShieldRef* out, int cap) = 0;
  // One absorb handler: absorbDamage = `left`; returns what it lets through (clamped by RunHit).
  virtual float Absorb(const ShieldRef& shield, int index, float left) = 0;
};

struct HitInput {
  float initial = 0.f;    // Damage_ComputeInitialDamage's result (damage + Power x coefficient, ModifyDamageInitial)
  bool crit = false;      // the crit roll hit (only basic attacks roll: MayCrit)
  float critMult = 1.f;   // criticalDamageBonusPercent + FlatCriticalStrikeDamage + PercentCriticalStrikeDamage
  unsigned descLo = 0;
  bool attacker = false;  // the attacker object exists: ModifyDamageOutgoing runs on it
  // D13 [assumed, from Damage_ComputeInitialDamage 0x008E0130, which runs ModifyDamageInitial only with an attacker]:
  // ModifyDamageIncoming and _Final run only for a hit with a snapshot. The one hit without is a health-resource
  // shaper's cost (CombatantData_SetResourcePoints 0x0087E200 passes none): the handlers read modifyDamageAttacker, and
  // modifier_Item_Legendary_DamageRedirection_Handler (Empathy) would build a snapshot from object 0 and redirect a
  // tenth of the cost.
  bool snapshot = false;
  bool absorb = true;     // [dgsim] damage_absorb
  float hp = 0.f;         // the target's HP before the hit (> 0: the core refuses dead targets first)
};

struct HitResult {
  float outgoing = 0.f, incoming = 0.f, preMitigation = 0.f, mult = 1.f, final = 0.f, absorbed = 0.f, dealt = 0.f,
        hpAfter = 0.f;
  int intChange = 0, absorbHandlers = 0, outgoingCalls = 0, incomingCalls = 0, finalCalls = 0;
  bool lethal = false;
};

// One absorb pass: the shields of `index`, in SortShields' order, each takes what it can while damage is left.
inline float AbsorbPass(HitHooks& h, int index, float left, int* handlers) {
  ShieldRef s[kMaxShields];
  const int n = std::min(h.Shields(index, s, kMaxShields), kMaxShields);
  SortShields(s, n);
  for (int i = 0; i < n && left > 0.f; ++i) {
    left = ClampAbsorbResult(h.Absorb(s[i], index, left), left);
    ++*handlers;
  }
  return left;
}

// DAMAGE.md steps 3-9. A value below 0 (an attack reduced below zero: Discipline's -10 Power per stack against a small
// base) runs through the ModifyDamage steps as it is (the client's initial damage has no floor either, 0x008E0130);
// only after ModifyDamageFinal is it floored at 0 [assumed], so no absorb handler ever sees 0 or less (a negative
// absorbDamage would grow a shield: absorbAmountRemaining - absorbDamage).
inline HitResult RunHit(const HitInput& in, HitHooks& h) {
  HitResult r;
  float d = in.initial;
  if (in.crit) d *= in.critMult;
  if (in.attacker) {
    d = h.Outgoing(d);
    ++r.outgoingCalls;
  }
  r.outgoing = d;
  if (in.snapshot) {
    d = h.Incoming(d);
    ++r.incomingCalls;
  }
  r.incoming = d;
  r.preMitigation = d;
  float mult = 1.f;
  if (!(in.descLo & kIsTrueDamage)) {
    mult = h.Mitigation();
    if (!(mult >= 0.f)) mult = 0.f;  // also NaN
  }
  r.mult = mult;
  d *= mult;
  if (in.snapshot) {
    d = h.Final(d);
    ++r.finalCalls;
  }
  if (!(d > 0.f)) d = 0.f;  // also NaN
  r.final = d;
  float left = d;
  if (in.absorb && left > 0.f) {
    const int typed = TypedAbsorbIndex(in.descLo);
    if (typed) left = AbsorbPass(h, typed, left, &r.absorbHandlers);
    if (left > 0.f) left = AbsorbPass(h, kAbsorbGeneric, left, &r.absorbHandlers);
  }
  r.absorbed = d - left;
  r.dealt = left;
  r.hpAfter = in.hp - left;
  if (!(r.hpAfter > 0.f)) r.hpAfter = 0.f;
  r.intChange = std::max(0, IntegerHpChange(in.hp, r.hpAfter));
  r.lethal = in.hp > 0.f && r.hpAfter <= 0.f;
  return r;
}

// ---- after the hit ------------------------------------------------------------------------------------------------

// Step 11: the attacker bookkeeping (combat times, last player to hit, death recap, assists) for a hit that took HP,
// with a snapshot, from another object of another team (DamageTracker.cpp asserts on friendly fire). A dead attacker
// still counts (a projectile landing after its caster died keeps the kill credit).
inline bool RecordsAttacker(bool snapshot, float dealt, std::uint32_t attackerId, std::uint32_t targetId, bool sameTeam) {
  return snapshot && dealt > 0.f && attackerId != 0 && attackerId != targetId && !sameTeam;
}
// Step 10: the combat event only for a hit that lowered the integer HP, or a killing blow. The client draws a damage
// number for every event with deltaHealth > 0 and prints its integerHpChange (CombatEvent_DamageAbsorb), so a hit
// below one HP (a 10 % spell overload of a 12-damage cone tick) showed as a second number "0". The heal core, which
// the client still has, reports only when the integer HP rose (GameObject_HealDamage 0x008E0720: the report is behind
// `0 < (int)newHP - (int)oldHP`) [code]; the damage core is taken to do the same [assumed], and a killing blow is
// always reported, since its event carries the kill (flags, streaks, the killing-blow feedback).
inline bool ReportsHit(int intChange, bool lethal) { return intChange > 0 || lethal; }
// Step 12: OnTookDamage / OnDealtDamage fire for every applied hit unless DoesNotGenerateDamageCallbacks, also one a
// shield took whole (dealt 0) [assumed]: so on-hit scripts (Finesse's stacks, Resonance's zap, Discipline, the MR
// steal) still proc against a shield, and a recall's own OnTookDamage (ability_returntobase returns false) ends it.
// OnDealtDamage needs the snapshot (its arguments come from it).
inline bool FiresDamageEvents(unsigned descLo) { return !(descLo & kNoDamageCallbacks); }
// Step 12 (D1b-live's hook): the attacker onto the victim's aggro list and the call for help, only for a live attacker
// that took HP [D13, assumed: a corpse draws no aggro].
inline bool DrawsAggro(float dealt, bool attackerPresent, bool attackerAlive) {
  return dealt > 0.f && attackerPresent && attackerAlive;
}
// Step 13: lifedrain heals the attacker by dealt x LifeDrain (x 0.33 for AoE and DoT) [text], through the heal core with
// noScaling (so HealIncrease doesn't count, HealingReduction does: HealAmount). D13 [code]: never for a dead attacker,
// as the Lua binding refuses a heal on HP <= 0 without its resurrect flag (HealAllowed); the heal core itself would
// resurrect (a projectile with lifedrain landing after its caster died brought it back).
inline float LifedrainAmount(float dealt, float lifeDrain, unsigned descLo, bool attackerAlive) {
  if (!(dealt > 0.f) || !(lifeDrain > 0.f) || !attackerAlive) return 0.f;
  return dealt * lifeDrain * ((descLo & (kIsAoE | kIsDoT)) ? 0.33f : 1.f);
}
// Step 14: the spell overload's share, (Mastery / spellOverloadFromMasteryRating + FlatSpellOverloadDamage) / 100.
inline float OverloadPercent(float mastery, float rating, float flatOverload) {
  return ((rating > 0.f ? mastery / rating : 0.f) + flatOverload) / 100.f;
}
// Step 14: the overload hit's damage, 0 for none: only the first hit (depth 0), not lethal, HP taken, the heal core's
// descriptor test (MayOverload), and [D13] a positive pre-mitigation damage (an attack reduced below zero that a
// later step lifted above 0 must not overload with a negative amount).
inline float OverloadDamage(float preMitigation, float pct, int depth, bool lethal, float dealt, unsigned descLo,
                            bool snapshot) {
  if (depth != 0 || lethal || !(dealt > 0.f) || !MayOverload(descLo, snapshot)) return 0.f;
  if (!(pct > 0.f) || !(preMitigation > 0.f)) return 0.f;
  return preMitigation * pct;
}

// ---- a health-resource shaper's cost ------------------------------------------------------------------------------

// The resource of a health shaper (resource type 8, Fenmore, Viyana) is HP + mShieldHealth (combatant +0x48 + +0x4C;
// CombatantData_GetResourcePoints 0x0087DFD0) [code].
inline float HealthPool(float hp, float shield) { return hp + shield; }
// LabsAbilities_ValidateRequest (0x009053C0) [code]: a cost is refused (-0x270D) when the pool is below it, and for a
// health shaper also when it equals it: a health cost never takes the last point of HP + shield.
inline bool CanPayCost(float pool, float cost, bool healthResource) {
  if (cost == 0.f) return true;
  if (pool < cost) return false;
  return !(healthResource && pool == cost);
}
// The payment (nAbility.PayMana 0x008C2590 / LabsAbilities_CreateAbilityInstance: new = max(0, pool - cost);
// CombatantData_SetResourcePoints 0x0087E200: damage = max(0, HP + mShieldHealth - new)) as true self-inflicted damage
// (0x400800) without a snapshot [code]: the cost itself when payable. The core then takes it from the generic shields
// first (true damage: AbsorbDamage only), the rest from HP, fractions kept.
inline float HealthCostDamage(float hp, float shield, float cost) {
  const float pool = HealthPool(hp, shield);
  float next = pool - cost;
  if (next < 0.f) next = 0.f;
  const float dmg = pool - next;
  return dmg > 0.f ? dmg : 0.f;
}

// ---- the heal core ------------------------------------------------------------------------------------------------

// nGameObject.HealDamage (0x009401B0) [code]: the heal core runs only when the target's HP > 0 or the script passes
// argument 7 (resurrect) true.
inline bool HealAllowed(float hp, bool canResurrect) { return canResurrect || hp > 0.f; }

struct HealResult {
  float amount = 0.f;     // *healed and the combat event's -deltaHealth: the full amount, overheal included
  float hpAfter = 0.f;
  int intGain = 0;        // (int)new - (int)old: the report needs > 0
  bool reported = false;  // Damage_ReportHeal queued a combat event (Healing, Lifesteal 0x400 with isLifesteal)
  bool resurrected = false;
};
// GameObject_HealDamage (0x008E0720) [code], without the crit roll (critMult 1) and the overload re-heal:
//   unless noScaling: amount x (1 + HealIncrease (40)) + snapshot Power x coefficient, x critMult;
//   always: x (1 - HealingReduction (39)) of the target (so Mortal Strike reduces lifesteal too);
//   HP = min(max, HP + amount) (no floor: a negative amount would lower HP); reported when the integer HP rose;
//   a heal of > 0 on HP <= 0 resurrects (Damage_ReportResurrect, GameObject_OnRevived).
inline HealResult ApplyHeal(float hp, float maxHp, float amount, bool noScaling, float healIncrease, float powerCoef,
                            float critMult, float healingReduction) {
  HealResult r;
  const bool wasDead = !(hp > 0.f);
  float a = amount;
  if (!noScaling) {
    a = (1.f + healIncrease) * amount + powerCoef;  // the target's HealIncrease, the snapshot's Power x coefficient
    a *= critMult;
  }
  a = (1.f - healingReduction) * a;
  r.amount = a;
  r.hpAfter = (hp + a <= maxHp) ? hp + a : maxHp;
  r.intGain = static_cast<int>(r.hpAfter) - static_cast<int>(hp);
  r.reported = r.intGain > 0;
  r.resurrected = wasDead && a > 0.f;
  return r;
}

// ---- modifier lifetime --------------------------------------------------------------------------------------------

// cStaticAbility.deactivationType (+0x428, nDeactivationType) [script]; the client reads none of them (D1e).
constexpr unsigned kDeactOnDuration = 1, kDeactOnAgentDestroyed = 2, kDeactOnAgentDeath = 4;

// D1e (gms/engine/abilities.cpp's end pass): a modifier with OnDuration ends once the clock reaches start + duration;
// a duration <= 0 is no time limit. Mortal Strike (modifier_StatusEffect_MortalStrike: duration {0}, then
// nModifier.SetDuration(3 or 5 s) from its requester's "duration"), the Purity/Devotion lockout
// (modifier_Item_*_HealNearbyAlly_RecentlyHealed: SetDuration(15 s)) and King of Masks' damage stacker (0.1 s, reset on
// each stack) end this way, at the first end pass at or after their time.
inline bool ModifierExpired(unsigned deactivationType, int durationMs, std::uint64_t startMs, std::uint64_t nowMs) {
  return (deactivationType & kDeactOnDuration) && durationMs > 0 &&
         nowMs >= startMs + static_cast<std::uint64_t>(durationMs);
}
// nModifier.SetDuration (0x008C7A40) [code]: instance +0x220 = round(seconds x 1000) (x87 rounding to nearest even).
inline int DurationMsFromSeconds(float seconds) {
  return static_cast<int>(std::nearbyint(static_cast<double>(seconds * 1000.f)));
}

enum class AtDeath { kKeep, kEnd, kInterrupt, kKeepReleased };
// D1f-live step 5 / Q3 (gms/engine/death.cpp): at a death (3 Lua scheduler runs later) the victim's modifiers with
// OnAgentDeath end, except a player's item passives, which its item slots hold (death_keep_item_passives; 20 of the 77
// item passives have OnAgentDeath, among them Purity's and Devotion's heal and Faith's regeneration) [assumed: the
// original kept or re-created them]. Lockouts and debuffs on the victim (Mortal Strike, Recently Transfused) end then.
inline AtDeath ModifierAtDeath(unsigned deactivationType, bool itemPassiveKept) {
  if (itemPassiveKept) return AtDeath::kKeep;
  return (deactivationType & kDeactOnAgentDeath) ? AtDeath::kEnd : AtDeath::kKeep;
}
// D6c: the victim's abilities are interrupted unless finishOnDeath, and only while they still hold the agent (an
// ability that released it, waiting for its projectile, runs on) unless death_interrupt_released.
inline AtDeath AbilityAtDeath(bool finishOnDeath, bool holdsAgent, bool interruptReleased) {
  if (finishOnDeath) return AtDeath::kKeep;
  if (!holdsAgent && !interruptReleased) return AtDeath::kKeepReleased;
  return AtDeath::kInterrupt;
}

}  // namespace gms::damage
