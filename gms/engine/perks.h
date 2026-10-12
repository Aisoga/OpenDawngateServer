// D3-perks: a perk page's effect on the server simulator (docs/GMS_PROTOCOL.md *Perk pages in shaper select
// (D3-perks)*). The client keeps a player's page stats (the pre-game loadout C0 02) in its cLabsPlayer (+0x2D4
// attributes, +0x2F4 modifiers); the shared attribute code adds the attributes of op Add to the player's creature
// (AttributeData_ComputePlayerAttribute 0x008D8180 -> LabsPlayer_GetLoadoutAttribute 0x00914890), on the server
// simulator as on the client's. Nothing else in the binary reads them, so the server part that turned a page into
// modifiers went with the stripped GMS shell; the bots' stand-in for a page, modifier_Bot_DefaultLoadout (Power +9,
// MaxHealth +108, Haste +4, PhysicalDamageReduction +4: a "Perks" modifier no data or script names), shows that it
// applied them as modifiers. The host:
//   - at the reveal (ServerMatch::OnShaperPicks, before the client's 0x401 and the deploy): every entry into the
//     server player's loadout attributes (PerkAttributeMap_GetOrInsert 0x0047AF60), as the client's C0 02 handler;
//   - at the deploy (after LabsPlayer_DeployCharacter): the entries of other ops (PercentChange) as attribute modifiers
//     on the creature (AttributeData_AddModifier 0x008D9C70) [assumed], and the page's modifiers activated on the
//     creature (LabsAbilities_ActivateAbility 0x0090A390, as a brain's passive) [assumed]: the perk modifiers are
//     permanent (duration 0, OnDuration | OnAgentDestroyed), so they outlive a death and respawn (all but one: D3c);
//   - each bot's creature gets modifier_Bot_DefaultLoadout at its deploy [inferred].
//   - D3c, at a respawn (death.cpp RespawnDue, after the shaper's passive): the page's modifiers the creature no longer
//     holds activated again (gms::PageModifiersToRenew). 17 of the 18 perk modifiers end OnAgentDestroyed and survive
//     the death, restarting their own children in OnRevive (Forcebubble, Lurker, Harrier, Invigorate, spellweaver, savant,
//     marauder), so the perk design keeps them through death; only vanguard's modifier_PerkHandler_ProtectDistantAllies
//     ends OnAgentDeath (its enemy aura stops with its holder's death; it has no OnRevive), and nothing in the client
//     activates it again: the stripped player hook GameObject_OnRevived calls (0x009A3F00) [assumed, as passives.h].
// Settings ([host] keys / DGGMS_OPTIONS): perks (1; 0 = no server-side effect: C8 09, C8 0A and C0 02 still go out),
// perk_percent (1: the non-Add entries as creature modifiers), perk_modifiers (1), bot_loadout (1), perk_respawn (1:
// D3c's re-activation at a respawn; 0 = a death-ended page modifier stays gone for the match, the binary's own state).
#pragma once

#include <cstdint>
#include <vector>

#include "core/protocol/shaper_select.h"

namespace perks {

struct Options {
  bool enabled = true;
  bool percent = true;
  bool modifiers = true;
  bool botLoadout = true;
  bool respawn = true;  // D3c
};

void Configure(const Options& options);
const Options& Settings();

// The page's attributes into the server player's loadout (the simulator is bound inside). Logs each entry with the
// value LabsPlayer_GetLoadoutAttribute reads back. False if the player or the map call failed.
bool WritePlayerLoadout(void* sim, unsigned index, const std::vector<gms::LoadoutAttribute>& attributes);

// At the deploy of a human's creature: the non-Add attributes as attribute modifiers, the modifiers activated.
void ApplyAtDeploy(void* sim, unsigned index, std::uint32_t creature, const std::vector<gms::LoadoutAttribute>& attributes,
                   const std::vector<std::uint32_t>& modifiers);

// D3c: at a respawn (server simulator bound or not; it binds): the page's modifiers player `index`'s creature no longer
// holds, activated again. The page is the one ApplyAtDeploy got for that index.
void AtRespawn(void* sim, unsigned index);

// At the deploy of a bot's creature: modifier_Bot_DefaultLoadout (with bot_loadout).
void ApplyBotLoadout(void* sim, unsigned index, std::uint32_t creature);

}  // namespace perks
