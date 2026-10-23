// MAPLUA: the host's fix-ups of MM_Map_v3's own scripts (the rules in
// gms/core/matchflow/mapfix.h, the Lua in gms/engine/lua/dgsim_mapfix.lua). The server simulator bound (the callers:
// experiment.cpp's MatchLevelStarted and TickMatch).
//   - At the level start: dgsim_mapfix.lua on the server's lua_State ([host] map_script_fixups, 1): the Flocklord
//     buffs' two missing sub-modifiers pointed at their SM_Map_v5 siblings, modifier_MM_regenSpeed's handle. Logs
//     `mapfix: dgsim_mapfix.lua (... bytes): ok dgsim_mapfix 1: ...`; while a table is missing it runs
//     dgsim_mapfix.Apply() again once a second (at most 60 times).
//   - On MM_Map_v3 with [host] mm_base_regen=1: each team's base object (the zone) gets MM's own regeneration
//     (modifier_MM_base_regen) instead of SM_Map_v5's modifier_base_regen. Logs `mapfix: MM base regen: team T ...`.
#pragma once

#include <cstddef>

namespace mapfix {

void OnLevelStarted(void* sim);
void Tick(void* sim);
void FormatStats(char* buf, std::size_t size);

}  // namespace mapfix
