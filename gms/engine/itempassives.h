// A player's item passives across its death and its respawn on the server simulator: the rules are
// gms/core/damage/itempassives.h (engine-free, in gms_tests); the death path (death.cpp) calls these.
//   - WhileDead: after the death path's step 5 has ended the victim's OnAgentDeath modifiers, and after every scheduler
//     run until the respawn: the item slots' dead ids cleared, a death-type passive running on the dead creature (a
//     purchase while dead) ended and cleared;
//   - AtRespawn: after the resurrect and the shaper's passive, the slots' death-ended passives activated again as
//     LabsPlayer_OnItemAdded (0x00912950) does at a purchase, their ids written into the slots.
// Settings ([host] keys, read by experiment.cpp into death::Options): death_keep_item_passives (0; 1 = the old rule:
// the death path leaves a player's item passives alone, they run on through the death) and item_passive_respawn (1;
// 0 = the death-ended passives stay gone for the match and the slots keep their dead ids, the client build's own state).
#pragma once

#include <cstddef>
#include <cstdint>

namespace itempassives {

// Server simulator bound by the caller. `why` names the moment for the log. Returns how many entries changed.
int WhileDead(void* sim, int index, std::uint32_t creature, const char* why);

// Server simulator bound by the caller: the respawned player's death-ended item passives activated again.
void AtRespawn(void* sim, int index);

// "item passives: ..." for the stats lines.
void FormatStats(char* buf, std::size_t size);

}  // namespace itempassives
