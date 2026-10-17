// MODCAP: the rules for an agent's ability and modifier id lists, engine free. An agent's ability data container (object +0x8A8) keeps the ids of its ability instances (+0x08) and of its
// modifiers (+0x9C) in two vectors with 32 inline slots (kMaxActiveAbilitiesPerAgent, kMaxActiveModifiersPerAgent).
//   - AbilityDataContainer_AddModifierInstance (0x00904CA0) appends; at 32 entries it asserts (LabsAbilities.cpp 3089),
//     which only logs (the reported counts, CapAssertReported), and appends anyway: the vector moves to the heap
//     (VectorU32_InsertSlow 0x0097F4D0 doubles it). No id is ever left out (MODCAP-live: in a lane session 77 adds past
//     the cap, 77 appended; the offline reading "a reported count returns" came from Ghidra's wrong CALL_RETURN flow
//     overrides at the assert calls). The cost is the assert line, the heap buffer, and a buffer that moves while
//     the client's raw loops below may hold a pointer into it.
//   - LabsAbilities_RemoveInstance (0x00905D40) frees an ended instance and erases its id from neither list; no function
//     in the client erases one (D1j). So every Unique replacement (LabsAbilities_ActivateFromDefinition, activation
//     type 1: end every instance of the GUID, then a new one) leaves one stale id behind until someone compacts.
//   - The host compacts with each agent's 500 ms blackboard period (gms/engine/agents.cpp SehPruneInstanceLists); a
//     modifier a script requests on every hit (Ablative Armor's HiddenTimer, SpeedWhenNotDamaging's Wait) fills the
//     list with stale ids inside one period.
// PlaceInstanceId is the add rule of the host's detour on AddModifierInstance/AddAbilityInstance (gms/engine/
// components.cpp, [host] modifier_slot_reuse): below the
// cap append (the original); at the cap reuse the first stale entry in place (no entry moves, so the client's loops that
// walk the raw list while ending instances, AbilityDataContainer_RemoveHoldingInstances 0x00906120 and
// LabsAbilities_CanStartControllingModifier 0x009088D0, stay valid); only 32 live instances reach the original's assert.
// CompactInstanceIds is the order-keeping prune for a safe point outside any Lua call.
#pragma once

#include <cstddef>
#include <cstdint>

namespace gms::damage {

constexpr std::size_t kMaxModifiersPerAgent = 32;  // kMaxActiveModifiersPerAgent (0x00904CA0: size < 0x20)
constexpr std::size_t kMaxAbilitiesPerAgent = 32;  // kMaxActiveAbilitiesPerAgent (0x00904AB0)

struct IdPlacement {
  bool reuse = false;     // true: write the id at `index` and skip the original add; false: the original add (append)
  std::size_t index = 0;  // the entry to overwrite (reuse) or the list's size (append)
};

// Where a new instance id goes in a list of `n` ids with `cap` inline slots. `isLive(id)` is true while the id names an
// existing instance (LabsAbilities_GetInstance / ObjectPool_TryToGet not null); 0 is never live.
template <class IsLive>
IdPlacement PlaceInstanceId(const std::uint32_t* ids, std::size_t n, std::size_t cap, IsLive isLive) {
  IdPlacement p;
  p.index = n;
  if (n < cap || !ids) return p;
  for (std::size_t i = 0; i < n; ++i)
    if (ids[i] == 0 || !isLive(ids[i])) {
      p.reuse = true;
      p.index = i;
      return p;
    }
  return p;  // every entry is live: the original's assert (and its throttle's drop or heap append)
}

// Drops the stale ids from a list in place, keeping the order of the live ones; returns the new size (the caller writes
// the vector's end). Only at a point where nothing walks the list (not inside a Lua call or an engine loop).
template <class IsLive>
std::size_t CompactInstanceIds(std::uint32_t* ids, std::size_t n, IsLive isLive) {
  if (!ids) return 0;
  std::size_t w = 0;
  for (std::size_t r = 0; r < n; ++r)
    if (ids[r] != 0 && isLive(ids[r])) ids[w++] = ids[r];
  return w;
}

// ShipLog_ThrottleAssert's answer (0x00896970 with its table at 0x0141C260: every count to 10, every 20th to 100, every
// 200th to 1000, then every 10,000th): true when the assert with this count is written to Application.log (per file and
// line, for the whole process). The add goes on either way.
inline bool CapAssertReported(std::uint32_t count) {
  if (count == 0) return false;
  if (count <= 10) return true;
  if (count <= 100) return count % 20 == 0;
  if (count <= 1000) return count % 200 == 0;
  return count % 10000 == 0;
}

}  // namespace gms::damage
