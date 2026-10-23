// CAMPREACH: a jungle camp's bigger creature walks through (or pushes) its smaller camp-mates instead of standing
// against them.
// Every server mover gets a NavPower repulsor whose flags LocomotionData_InitRepulsorFlags (0x0088E750) writes into its
// locomotion (+0x94 blockage, +0x98 identity): an NPC has blockage 0xFFFFFFFB and identity 2, so every NPC is blocked
// by every other, and since D1k the server space's constraint solver resolves that. A camp's major chasing a player who
// stands behind the camp's minor walked straight against the minor (which stands still while it attacks) and stopped
// there, out of reach (the players' report, 2026-10-23). The client's own Pathfinder_UpdateMoverRepulsorFlags
// (0x0089D1B0) copies the locomotion's two words into the mover's MoverTune every frame, so the host only rewrites the
// locomotion's words once at the creature's spawn:
//   - identity: the NPC bit (2) is replaced by a bit of the creature's tier (minor, medium, major, uber). Nobody tests
//     for the NPC bit alone (the blockages in the client are 0xFFFFFFFB, 0xFFFFFFFF and 0), so players, bots, minions
//     and workers are blocked by a creature exactly as before;
//   - blockage: a creature is no longer blocked by the creatures of lower tiers (their bits cleared): the major walks
//     through the medium and the minors, the medium through the minors; the smaller ones are still blocked by the
//     bigger (so the solver pushes them aside where it moves them [assumed]), and creatures of one tier still collide.
// The obstacle flags (+0x9C, the auto obstacle of a standing NPC) are left as they are.
// Header-only and engine-free; gms_tests (test_camp_crowd.cpp) checks it.
#pragma once

#include <cstdint>

namespace gms {
namespace agents {

constexpr std::uint32_t kRepulsorIdentityNpc = 2;             // LocomotionData_InitRepulsorFlags: any NPC
constexpr std::uint32_t kRepulsorIdentityCreepTier0 = 0x100;  // ours: minor 0x100, medium 0x200, major 0x400, uber 0x800
constexpr int kCreepTiers = 4;

// The identity bit of a camp creature's tier (0 minor .. 3 uber); 0 for anything else.
inline std::uint32_t CreepTierIdentity(int tier) {
  return tier >= 0 && tier < kCreepTiers ? kRepulsorIdentityCreepTier0 << tier : 0u;
}

struct RepulsorFlags {
  std::uint32_t blockage = 0;
  std::uint32_t identity = 0;
};

// The repulsor flags of a camp creature of `tier`, given the ones the client gave it. Unchanged: a tier out of range,
// a mover without repulsion (both 0: noRepulsion), or one that isn't a plain NPC (identity other than 2: a player's
// creature, NPC type 12).
inline RepulsorFlags CampCreepRepulsor(int tier, RepulsorFlags in) {
  const std::uint32_t own = CreepTierIdentity(tier);
  if (!own || in.identity != kRepulsorIdentityNpc || in.blockage == 0) return in;
  RepulsorFlags out;
  out.identity = own;
  std::uint32_t lower = 0;
  for (int t = 0; t < tier; ++t) lower |= CreepTierIdentity(t);
  out.blockage = in.blockage & ~lower;
  return out;
}

// Whether a mover with `blockage` is blocked by (avoids, is pushed by) one with `identity` (NavPower's repulsor test
// [inferred]: the bitwise and of the two; the direction from the attribute UnitPassThrough, which clears only its
// holder's blockage, and Fluidity's Spirit Walk lets its holder walk through units).
inline bool RepulsorBlocks(std::uint32_t blockage, std::uint32_t identity) { return (blockage & identity) != 0; }

}  // namespace agents
}  // namespace gms
