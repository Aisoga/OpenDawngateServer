// D6c: the shapers' passives on the server (docs/AGENT_AI.md *D6c*). A shaper's passive is the modifier of its
// PlayerClass.passiveAbility (ability slot 2, LabsPlayer_GetAbilityInSlot 0x00911B80, PlayerClass +0xE4):
// modifier_Artist_Passive, modifier_HighHuntress_Passive, ... (37 shapers; 47 *_Passive scripts, nearly all with
// deactivationType OnAgentDeath). Nothing in the client build activates it on the server: of the twelve callers of
// LabsAbilities_ActivateFromDefinition (0x00908AC0) none reads the PlayerClass's passive; GameObject_OnRevived
// (0x008E0510) re-activates an NPC's AIDefinition.passiveAbility but for a player-controlled object calls a server-only
// player hook that the client build compiled out (0x009A3F00, a shared empty function; D1g). So the host activates it
// [inferred: the stripped server code did, since the scripts depend on it: modifier_Artist_Passive's Activate sets the
// Artist's wHolder blackboard entry that ability_Artist_W_Shred's Tick reads, and without it every W errs with "Could
// not find object!"]:
//   - after LabsPlayer_DeployCharacter (every player, humans and bots: replicate's DeployShaper),
//   - after a respawn's resurrect (death.cpp; the death path ends OnAgentDeath modifiers, the passive among them),
// through LabsAbilities_ActivateFromDefinition(passive, creature, creature, 0, rank 1, zero vector, 0), the call
// AgentBrain_Init and GameObject_OnRevived make for an NPC's passive [rank 1 assumed, as OnRevived's]; not when the
// creature already has an instance of it. [host] shaper_passives (1).
// D13 (docs/DAMAGE.md *Combat edge cases (D13)*): nothing in the client reads RolesTuning.role[].rolePassive either
// (the four readers of the level's RolesTuning, 0x009105F0's callers, read only the gold/XP members, the labels and the
// count), so modifier_Role_Gladiator/_Tactician/_Hunter/_Predator were never active on the host; three of them end
// OnAgentDeath. With [host] role_passives (1, on by default since D13-live: every role's effect checked live; 0 = off)
// the same two moments also activate the player's role passive (mRole, cLabsPlayer +0x08).
#pragma once

#include <cstddef>
#include <cstdint>

namespace passives {

// Server simulator bound. Activates player `index`'s shaper passive on its current creature; `why` names the moment
// for the log ("deploy", "respawn"). Returns the modifier instance id, 0 if none was activated.
std::uint32_t Activate(void* sim, unsigned index, const char* why);

// Whether the creature holds a live (not ending) instance of the static ability among its modifiers (object +0x8A8:
// modifier ids +0x9C..+0xA0); the caller binds the server simulator and catches exceptions. Also D3c's (perks.cpp).
bool CreatureHasLiveInstance(void* creature, void* staticAbility);

// One-line counters for the stats.
void FormatStats(char* buf, std::size_t size);

}  // namespace passives
