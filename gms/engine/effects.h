// FOGSTACKS: the attached effects an ended modifier leaves on the server simulator are destroyed.
//
// A script's nEffect.CreateEffect(true, asset, object, ...) fills one of the object's 16 attached-effect slots
// (GameObject_CreateAttachedEffect 0x00881D30) and only its nEffect.DestroyEffect empties it again
// (GameObject_DestroyAttachedEffect 0x00880A40). Duress's counter (modifier_Item_Legendary_OnBasic_Root_Counter) puts
// its stack marker over the victim and has no Deactivate, so after the counter ran out the marker's slot stayed in
// use for the match, and every fog exit (and every later create) sent it to the player again: the victim came out of
// the fog with the stacks it had when it went in.
//
// The host detours both functions: a create made while a modifier's or ability's script runs (abilities.h
// AbilityLuaInstance) is recorded with that instance, a destroy forgets it (gms/core/replication/effect_ledger.h).
// After every scheduler run (TickAbilities) the effects of every modifier instance that has ended are destroyed, as
// the script would have done, while the object's slot still holds the same handle: the destroy goes to the players
// that see the object and the slot is free, so a later fog exit no longer sends it.
// [host] modifier_effects_end (1; 0 = leave them, the stripped server's behaviour), modifier_effects_log_limit (100).
#pragma once

#include <cstddef>

namespace effects {

struct Options {
  bool endWithModifier = true;
  int logLimit = 100;
};

bool Install(const Options& options);
bool Installed();
void SetSim(void* sim);

// After the scheduler run and the ability ends of a tick, with the server simulator current.
void AfterAbilities();

void FormatStats(char* buf, std::size_t size);

}  // namespace effects
