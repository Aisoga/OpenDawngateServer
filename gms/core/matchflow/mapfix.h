// MAPLUA: the host's fix-ups of MM_Map_v3's own scripts. Engine-free; the engine side is
// gms/engine/mapfix.cpp, the Lua side gms/engine/lua/dgsim_mapfix.lua. Header-only; gms_tests MapLua*.
//
// 1. Two sub-modifiers MM's Flocklord buffs request don't exist in build 854 [data: ServerData]:
//    modifier_MM_BlueBuff.OnAbilityUsed requests its `bonusHasteMod` = SPID("modifier_MM_BlueBuff_BonusHaste") and
//    modifier_MM_OrangeBuff.OnDealtDamage its `damageReductionMod` = SPID("modifier_MM_OrangeBuff_DamageReduction");
//    no script defines either, so each request asserts LabsAbilities.cpp 2346 ("Unable to lookup modifier") and gives
//    nothing. The two buffs are SM_Map_v5's modifier_Jungle_BlueBuff / _OrangeBuff with another duration (60 s, not
//    150 s) and name; the SM siblings' sub-modifiers (3 s +20 haste; 3 s stacks of 1 % damage reduction, at most 10)
//    read only their own tables. The fix points the MM field at the SM sibling's GUID (a redirect), not a new
//    registration under the MM name: the client knows only the scripts of its own data, and a modifier GUID it can't
//    look up would give it an AB record without a static ability (no icon, no effects, no client-side thread)
//    [inferred: AgentSummary_OnModifierCreated 0x00658480 skips the effects when LabsAbilities_FindStaticAbility
//    fails]. The SM sibling's name, icon and tooltip show instead [assumed: the original's MM sub-modifiers are
//    unknown, presumably copies like their parents].
// 2. modifier_MM_regenSpeed removes "modifier 0" (MAPHOST point 5): its Tick, Deactivate and OnDealtDamage call
//    nAttribute.RemoveAttributeModifier(obj, 0) and OnTookDamage (kObjIDNone, 0), where every other script passes the
//    handle its Activate kept (here `translatedToken`). The fix-up keeps that handle and removes it once.
// 3. MM's own base regeneration (opt-in, [host] mm_base_regen=1): the base zones stay SM_Map_v5's spawn points (the
//    only way to a base object, so a shop: MAPHOST point 1); on MM_Map_v3 the host ends each zone's
//    modifier_base_regen and starts MM's modifier_MM_base_regen on the same object. Off by default: MM's scripts are
//    unfinished (point 2; the haste has no duration and a speed token of 300 %), so SM's regeneration stays the
//    default (ASSUMPTIONS MF7).
#pragma once

#include <cctype>
#include <cstdint>
#include <string>
#include <vector>

#include "core/protocol/messages.h"

namespace gms {
namespace mapfix {

// A field of a holder modifier's table that names a missing modifier, and the existing one it should name instead.
struct ModifierRedirect {
  const char* holder;   // the table whose script requests it
  const char* field;    // the holder's field with the GUID
  const char* missing;  // the name the field holds (no script defines it)
  const char* sibling;  // the existing modifier the field names after the fix
};

inline const std::vector<ModifierRedirect>& Redirects() {
  static const std::vector<ModifierRedirect> r = {
      {"modifier_MM_BlueBuff", "bonusHasteMod", "modifier_MM_BlueBuff_BonusHaste",
       "modifier_Jungle_BlueBuff_BonusHaste"},
      {"modifier_MM_OrangeBuff", "damageReductionMod", "modifier_MM_OrangeBuff_DamageReduction",
       "modifier_Jungle_OrangeBuff_DamageReduction"},
  };
  return r;
}

// A modifier's GUID: nUtil.SPID's and nAbility.RegisterAbility's FNV-1 of the lower-case name (the same as an asset
// id, gms::AssetIdOf).
inline std::uint32_t ModifierGuid(const std::string& name) { return AssetIdOf(name); }

// Whether the level asset's name is MM_Map_v3's ("MM_Map_v3", any case, with or without an extension).
inline bool IsMmLevel(const std::string& levelName) {
  const std::string want = "mm_map_v3";
  if (levelName.size() < want.size()) return false;
  for (std::size_t i = 0; i < want.size(); ++i)
    if (std::tolower(static_cast<unsigned char>(levelName[i])) != want[i]) return false;
  return levelName.size() == want.size() || levelName[want.size()] == '.';
}

// Which base regeneration a level's zones run: SM_Map_v5's (as placed) or MM_Map_v3's own.
enum class BaseRegen { kAsPlaced, kMm };

// [host] mm_base_regen: 0 (default) as placed, 1 MM's own on MM_Map_v3 (never on another level).
inline BaseRegen ChooseBaseRegen(int option, bool mmLevel) {
  return option == 1 && mmLevel ? BaseRegen::kMm : BaseRegen::kAsPlaced;
}

// The Lua the host runs before dgsim_mapfix.lua: its switches.
inline std::string PreChunk(bool fixups) {
  return std::string("dgsim_mapfix = dgsim_mapfix or {} dgsim_mapfix.wanted = ") + (fixups ? "true" : "false") +
         " return 'ok'";
}

}  // namespace mapfix
}  // namespace gms
