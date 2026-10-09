// D1i: health and resource regeneration on the server simulator (docs/DAMAGE.md *D1i: regeneration on the host*).
// The client build reads HealthRegen, ResourcePointRegen, ResourcePointPercentRegen and DisableResourcePointRegen only
// to compute and show them (the attribute code, the HUD's "Hp5"/"Mp5"); the step that spent them per frame went with
// the server's frame update, so a damaged Guardian, creep or shaper stayed damaged (MATCH_FLOW.md *D5* findings). Every
// `stepMs` of the server clock (and only while the match runs: not after its end) the host walks the live objects that
// have combatant and attribute data and, with the server simulator bound and every engine call under SEH:
//   - HP: + TotalHealthRegen (derived 105, the client's getter: HealthRegen x (1 + HealIncrease) x (1 - HealingReduction))
//     x seconds / 5, up to the top of the current health bar (GameObject_GetCurrentHealthBar 0x0087E420,
//     GameObject_GetHealthBarMax 0x0087E2F0; the max HP without tiers); nothing for the dead (HP <= 0);
//   - the resource (combatant +0x58) of an object whose resource isn't its health (type 8): + (ResourcePointRegen +
//     ResourcePointPercentRegen x MaxResourcePoints) x seconds / 5 up to MaxResourcePoints, none while
//     DisableResourcePointRegen > 0 [assumed: the percent's unit, DAMAGE.md *D1i*].
// The values are written straight into the combatant data, as the client's own setters do (CombatantData_SetResourcePoints
// 0x0087E200 for a non-health resource): no combat event [assumed]. The replication step's A0 delta (D2g-live) carries
// mHitPoints / mResourcePoints to the clients. The rules are gms/core/damage/regen.h (gms_tests DamageRegen*).
// The regen attributes themselves come from the data and the scripts: ClassAttributes.baseHealthRegen + (level - 1) x
// healthRegenPerLevel (shapers only on v5), modifier_BaseTitan_V5_Passive (the Guardian's bar over 240 s),
// modifier_Jungle_Creep_Regen / modifier_Jungle_Buff_Regen_* (2000 per 5 s while a creep is home with an empty aggro
// list), modifier_base_regen (the base zones Base_Health_Regen_Attackers/Defenders: + max HP per 5 s for allied
// players within 40 units, plus 2 % of the resource every 0.1 s, which the script adds itself).
// Settings ([dgsim] keys, in host mode also through DGGMS_OPTIONS): regen (1), regen_health (1), regen_resource (1),
// regen_step_ms (500), regen_log_s (5: one progress line per regenerating object), regen_log_limit (300).
#pragma once

#include <cstddef>

namespace regen {

struct Options {
  bool enabled = true;
  bool health = true;
  bool resource = true;
  int stepMs = 500;         // [assumed] the regen step on the server clock; 0 = every tick
  double logSeconds = 5.0;  // a progress line per regenerating object at most this often
  int logLimit = 300;       // log lines before only every 100th is logged
};

// DllMain time: checks the prologues of the client functions it calls and logs the settings.
bool Install(const Options& options);
bool Enabled();
void SetSim(void* sim);
// Every server tick while the simulator runs and isn't paused (server simulator bound), after the ability end pass.
void Tick();
void FormatStats(char* buf, std::size_t size);

}  // namespace regen
