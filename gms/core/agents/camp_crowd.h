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
// The obstacle flags (+0x9C, the auto obstacle of a standing NPC): CampCreepObstacle below (since CAMPREACH-live).
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

// CAMPREACH-live: the obstacle half. The repulsor flags alone weren't enough: in campreach_on3c the major stopped 3.1
// from the standing minor's centre (inside the repulsor's reach, so that part worked) and flipped between Pursue and
// CombatIdle every frame without a hit for 14 s, its path cut by the minor's auto obstacle. A standing NPC's mover makes
// an auto obstacle of identity 2 (MoverTune +0x68, from locomotion +0xA0), and every NPC's planner avoids obstacles
// whose identity shares a bit with its obstacle blockage (MoverTune +0x4C, 0x17 from locomotion +0x9C) [inferred: the
// same and-test as the repulsors]. So for a camp creature:
//   - its auto obstacle's identity keeps the NPC bit (2), so players, bots, minions, workers and its camp's smaller
//     creatures plan around it as before, and gets its tier's bit;
//   - a medium or bigger creature's obstacle blockage loses the NPC bit and gets the bits of its own and the higher
//     tiers: it plans through its camp's smaller creatures (the repulsors then let it pass) and still around creatures
//     of its tier or bigger and every non-NPC obstacle (bits 0x1, 0x4, 0x10). The cost [assumed]: it also plans through
//     other NPCs that stand (a minion, a worker); its repulsor still collides with them.
// A minor is unchanged but for its obstacle's tier bit. Unchanged: a tier out of range, a mover without an auto
// obstacle (identity 0: noRepulsion) or not an NPC's (identity other than 2).
struct ObstacleFlags {
  std::uint32_t blockage = 0;      // MoverTune +0x4C m_obstacleBlockageFlags (locomotion +0x9C)
  std::uint32_t autoIdentity = 0;  // MoverTune +0x68, the auto obstacle's identity (locomotion +0xA0)
};

inline ObstacleFlags CampCreepObstacle(int tier, ObstacleFlags in) {
  const std::uint32_t own = CreepTierIdentity(tier);
  if (!own || in.autoIdentity != kRepulsorIdentityNpc) return in;
  ObstacleFlags out = in;
  out.autoIdentity = in.autoIdentity | own;
  if (tier > 0 && (in.blockage & kRepulsorIdentityNpc) != 0) {
    std::uint32_t sameOrHigher = 0;
    for (int t = tier; t < kCreepTiers; ++t) sameOrHigher |= CreepTierIdentity(t);
    out.blockage = (in.blockage & ~kRepulsorIdentityNpc) | sameOrHigher;
  }
  return out;
}

// Whether a mover with `blockage` is blocked by (avoids, is pushed by) one with `identity` (NavPower's repulsor test
// [inferred]: the bitwise and of the two; the direction from the attribute UnitPassThrough, which clears only its
// holder's blockage, and Fluidity's Spirit Walk lets its holder walk through units).
inline bool RepulsorBlocks(std::uint32_t blockage, std::uint32_t identity) { return (blockage & identity) != 0; }

}  // namespace agents
}  // namespace gms
