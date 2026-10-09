// D1a-live: the piece of the server ability update that Dawngate.exe lacks (docs/SERVER_MODE.md
// *D1a static analysis* and *D1a-live*). The server paths prepare each ability's or modifier's Tick
// on its own GearsLua thread (status 2) but never hand that thread to the scheduler, and nothing ends
// an instance whose Tick has returned. This module does both:
//   - detours LabsAbilities_CreateTickThread (0x009020A0) and LabsAbilities_RecreateTickThread
//     (0x00902580); after the original, with our simulator current, a thread with status 2 is added
//     to the sleeping set (GearsLua_AddSleepingThread 0x00874170), or queued while the scheduler runs;
//   - after each GearsLua_TickThreads, registers the queued threads and ends (LabsAbilities_EndAbility
//     0x00905E90) every remembered instance whose thread finished (-1), failed (-2) or is gone, until
//     LabsAbilities_GetInstance (0x008FF220) no longer finds it;
//   - D10: detours GearsLua_ResumeThread (0x00875B20) to hold the resumed thread's instance in use (+0x230) for the
//     length of the resume, so EndAbility from inside its own Tick defers (+0x234, "end pending") instead of freeing
//     the running thread (the host died in d10_run1: ability_Alpha_E_HealWolf's return projectile marks itself for
//     delete in OnHitTarget, its piercing loop then waits on a freed thread, nThread.WaitForProjectile 0x009305D0
//     writes to null + 0x54, the Lua tick part was switched off and every ability stalled); the end pass ends
//     instances whose end is pending once nothing holds them.
//   - D6c: detours LabsAbilities_EndAbility (0x00905E90) and LabsAbilities_InterruptAbility (0x00905FA0) to log who
//     ends an agent ability (instance type 0) before its Tick finished: the behavior tree's aborts, the command queue,
//     the MarkForDelete bindings, an event function, the host's death path or end pass (the "stats abilities" line
//     counts them by cause). A projectile's "ability has already been destroyed" assert follows such an end.
// All functions run on the game's main thread; the tick functions with our simulator bound.
#pragma once

#include <cstddef>

struct AbilityTickOptions {
  int endNoTick = 1;      // also end agent abilities (instance type 0) that have no Tick (status 1) after Activate
  int logLimit = 400;     // per-event log lines before only every 100th is logged
  int modifierExpiry = 1; // D1e: end modifiers whose OnDuration time ran out (see abilities.cpp)
  int holdRunning = 1;    // D10: hold an instance "in use" (+0x230) while its Tick thread is resumed (see abilities.cpp)
  int endTrace = 1;       // D6c: detour EndAbility/InterruptAbility and log who ends an agent ability before its Tick did
  int luaContext = 1;     // D12: detour GearsLua_CallFunction and track the resumed Tick (AbilityLuaContext)
};

// Installs both detours (DllMain time, before the level loads). Returns false if a prologue differs.
bool AbilityTickInstall(const AbilityTickOptions& options);
// The simulator whose instances are handled (only while it is the thread's current simulator).
void AbilityTickSetSim(void* sim);
// Around GearsLua_TickThreads: threads prepared while it runs are queued and registered afterwards;
// the after-call also ends finished instances.
void AbilityTickBeforeScheduler();
void AbilityTickAfterScheduler();
// One-line counters for the harness stats line.
void AbilityTickFormatStats(char* buf, std::size_t size);
// D6c: the host's reason for its own EndAbility/InterruptAbility calls while they run (the early-end trace names it;
// nullptr = none). The death path sets it around its interrupts.
void AbilityTickSetHostCause(const char* cause);
// D12: the Lua the server simulator runs right now, for the assert and Lua error lines: the innermost synchronous
// GearsLua_CallFunction (table name and nAbilityFns function, the instance and its agent for an ability's table) or
// the resumed Tick thread's instance. Writes " [in <table>.<function> inst <id> agent <id>]" (outermost call after
// "<-"), or an empty string outside any.
void AbilityLuaContext(char* buf, std::size_t size);
