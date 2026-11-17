// MAPRULES3: a destroyed Binding comes back on a level that enables it (LevelGameplayTuning towerRespawnEnabled and
// tower[].respawnTime; a compiled map's `bindings.respawn`). The rules are engine-free (gms/core/matchflow/mapbase.h:
// BindingRespawns, BindingDueMs, TowerTuningIndex); this module does the engine half:
//   - at the death (match.cpp TowerDestroyed): the slot's value, the server clock of the return (all ones: never);
//   - each match tick: resurrect the due Bindings (GameObject_Resurrect: full HP and, for an NPC, its AI definition's
//     passive again), write 0 into the slot (standing), check that modifier_Binding_Passive is back (requested again if
//     not, [host] tower_respawn_passive), call nGameModeHandlers[OnTowerRespawned], apply the tower entry's
//     laneAgentRespawnCounterAdjustment to the Binding's team (director::AdjustLaneAgents) and log
//     `match: Binding ... respawned after N s`.
// The client draws the countdown by itself from the slot (TowerGraphics_Update: the rebuild state, tower_rebirth_effect
// and TowerRespawnWarning, then TowerRespawned 2 s before), but only on a level whose own tuning has
// towerRespawnEnabled. Test aid: [host] tower_respawn_s (-1: the level's; >= 0: that many seconds, at least 5, also on
// a level without the flag, where the client keeps drawing the Binding destroyed until it stands again).
#pragma once

#include <cstdint>
#include <vector>

#include "core/matchflow/mapbase.h"

namespace bindrespawn {

// At the level load (server simulator bound), with the level's tower part (match.cpp LogMapBase reads it; n < 0:
// unreadable). Reads [host] tower_respawn_s and tower_respawn_passive and logs the rule.
void OnLevel(bool enabled, const gms::flow::TowerTuning* list, int n);

// Whether a destroyed Binding can come back on this level (or by the test aid).
bool Active();

// A Binding died (bound): the value for its mTowerRespawnTime slot (all ones when it stays down). `tierMask` is the
// tiers its team has on the level (gms::flow::TierMask), for the tower[] entry.
unsigned long long OnDestroyed(void* sim, std::uint32_t id, const char* noun, int team, int teamIndex, int lane,
                               int tier, unsigned tierMask, double matchSeconds);

// Each match tick (bound): resurrects the due Bindings and returns their ids (the caller marks them standing).
std::vector<std::uint32_t> Tick(void* sim, double matchSeconds, bool ended);

void FormatStats(char* buf, unsigned size);

}  // namespace bindrespawn
