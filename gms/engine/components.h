// D1j: the server's per-object components (docs/SERVER_MODE.md *The ability data containers (D1j)*).
//
// Every object with abilities on the server simulator (minions, creeps, shapers, towers, wards: GameObjectManager_
// OnObjectCreated 0x008E5310 creates one for the server only) gets an ability data container, a 0x560-byte element of
// the per-simulator pool "SP_Simulator/cObjectPoolT/cAbilityDataContainer" (1,024 elements; AbilityDataContainer_
// CreatePool 0x008FEC70). Nothing in the client build gives one back: its ComponentType_Register (0x01129420) has no
// per-object destroy callback, GameObjectManager_OnObjectDestroyed (0x008E4F40) only removes the instances that hold
// the agent (0x00906120), and no code pushes the type's hash 0x850A1302 besides the pool functions and the create
// [inferred: the release was in the stripped server code]. After about 13 minutes of waves the pool was full and new
// minions had no container: no attacks, no modifiers (D6c's d6c_run1/run3, d6b_run2).
//
// The host does it: a detour on OnObjectDestroyed, for the server simulator's objects only,
//   - before the original: ends every instance still in the container's lists (LabsAbilities_EndAbility: Deactivate
//     and removal, or end pending while a Lua call holds it, which the end pass in abilities.cpp finishes) while the
//     object is intact; the original's own removal then finds nothing left;
//   - after it: ends what the teardown added, frees the container's heap parts (spell-shield records beyond 4,
//     cooldown nodes beyond 8; the fixed vectors of ids are capped at 32 and never leave the element) with the game's
//     free, gives the element back to the pool (ObjectPool_DeleteElement 0x00A81BB0, the cAgentBrain's and
//     cAttributeData's pattern) and clears the object's +0x8A8.
// [host]/[dgsim] container_release (1), container_end_instances (1), container_free_heap (1), container_log_limit (100:
// the first 100 destroys with a container, then every 100th and every one that ended, freed or failed something);
// pool_stats (1): a `stats pools:` line with every server pool's live count, high water and capacity.
#pragma once

#include <cstddef>

namespace components {

struct Options {
  bool release = true;        // give the container back at the object's destroy
  bool endInstances = true;   // end the instances still listed in it first
  bool freeHeap = true;       // free its heap parts (else only counted)
  bool poolStats = true;      // the `stats pools:` line
  int logLimit = 100;
};

bool Install(const Options& options);
bool Installed();
void SetSim(void* sim);

// The containers line (`stats containers:` "pool live N of 1024 (high water H, peak P), objects with one M; destroyed
// ..., released R, ..."), with `objectsWithContainer` from the host's object census.
void FormatStats(char* buf, std::size_t size, int objectsWithContainer);

// Every server pool of the registered component types (`stats pools:` "Name live/capacity (hw H, peak P), ...");
// empty with pool_stats 0.
void FormatPools(char* buf, std::size_t size);

// Q3c: one server pool by its component type's name ("cAbilityInstanceData"): live elements, high water, capacity.
// False when the type or its pool isn't there (or before SetSim).
bool PoolCount(const char* typeName, int* live, int* highWater, int* capacity);

}  // namespace components
