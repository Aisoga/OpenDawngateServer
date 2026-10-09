// D1f-live: the core of cGameObject::TakeDamage (0x008E01C0), whose body the client build doesn't contain
// (docs/SERVER_MODE.md *D1a-live*, docs/DAMAGE.md). The detour runs DAMAGE.md's pipeline with the client's own pieces:
//   1 validate (combatant, HP > 0; Immune (46) -> combat event 0x80, no damage)
//   2 Damage_ComputeInitialDamage (0x008E0130; ModifyDamageInitial on the attacker)
//   3 the crit roll for basic attacks (Damage_RollCritical 0x008DFD50) x criticalDamageBonusPercent (+ 35, 34)
//   4 ModifyDamageOutgoing (11) on the attacker, 5 ModifyDamageIncoming (12) on the target
//     (AbilityDataContainer_ModifyDamage 0x00906220)
//   6 mitigation: Damage_GetPhysicalMultiplier / _GetMagicalMultiplier (0x008DFEB0 / 0x008DFFF0) unless true damage,
//     then [assumed] DamageReduction (21), PercentDamageDebuff (33), AoeResistance (63), the structure attributes (67, 68)
//   7 ModifyDamageFinal (13) on the target
//   8 absorb: AbsorbDamage_Physical / _Magical (16 / 15), then AbsorbDamage (14) on the target's modifiers, the one
//     that expires first first (the handlers called through LabsAbilities_CallModifyDamageFunction 0x00900460 with
//     absorbDamage / absorbDirection set in their private table)
//   9 HP (dealt = after absorb; HP floored at 0); a lethal hit writes the death descriptors, the hit direction and
//     zeroes the shields
//  10 Damage_ReportHpChange (0x008DFB10, the combat event), 11 Damage_RecordAttacker (0x008E1AD0)
//  12 the agent AI's damage hook (agents::OnDamaged, D1b-live: the attacker onto the victim's aggro list, call for
//     help), then OnTookDamage / OnDealtDamage (0x00906760 / 0x00906CA0) unless DoesNotGenerateDamageCallbacks (D1i:
//     in that order, so the creeps' regen modifiers see the attacker on the list in OnTookDamage)
//  13 lifedrain (LifeDrain 64 of the snapshot; GameObject_HealDamage 0x008E0720 as lifesteal, x0.33 for AoE / DoT)
//  14 spell overload: a second hit of (Mastery / 2 + FlatSpellOverloadDamage) % of the pre-mitigation damage
//  15 death processing (death.h) on a lethal hit.
// D13: steps 3-9 run as gms::damage::RunHit (core/damage/pipeline.h, unit-tested in gms_tests DamageEdge*) with hooks
// into the client; ModifyDamageIncoming/_Final only for a hit with a snapshot (a health-resource shaper's cost has none);
// a dead attacker draws no aggro (12) and drains no life (13: the heal core would resurrect it); the overload needs a
// positive pre-mitigation damage (14).
// Each hit logs one line with every step's value. The shield totals (combatant +0x4C/+0x50/+0x54 = the sums of the
// target's AbsorbDamageAvailable* handlers, 17-19) are refreshed after every hit and once per frame (DamageTickShields).
// All of it only while the server simulator is current; any other simulator gets the client's original (a no-op).
#pragma once

#include <cstddef>

struct DamageOptions {
  int logLimit = 200;       // per-hit lines before only every 100th is logged
  bool absorb = true;       // step 8
  bool reductions = true;   // step 6's [assumed] extra attributes
  bool lifedrain = true;    // step 13
  bool overload = true;     // step 14
  bool shields = true;      // the shield totals
};

// One named field for an instance's private table (_PRIVATETHREADTABLE of its GearsLua thread), set the way
// LabsAbilities_SetModifyDamageArgs (0x008FE110) sets modifyDamage*.
struct HandlerField {
  const char* name;
  int type;  // 0 number, 1 vector (null = zero), 2 object id
  float number;
  const float* vector;
  unsigned id;
};

// One instance's number-returning handler `index` (static +0x14 + index), with `fields` set in its private table first,
// through LabsAbilities_CallModifyDamageFunction (0x00900460): `value` in, the handler's result out (`value` unchanged if
// the instance has no such handler or is ending). For the AbsorbDamage* handlers and OnGrantGold (death.cpp).
// `*called` (optional) tells whether the handler ran. Server simulator bound.
float CallInstanceHandler(unsigned instanceId, int index, float value, const HandlerField* fields, int count,
                          unsigned otherId, bool* called);

bool DamageInstall(const DamageOptions& options);
void DamageSetSim(void* sim);
// Once per frame after the Lua scheduler (server simulator bound): the shield totals of every object that carries a
// modifier with an AbsorbDamageAvailable* handler, and back to 0 once it is gone.
void DamageTickShields();
void DamageFormatStats(char* buf, std::size_t size);
