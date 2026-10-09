// D1k: the minions' crowd on the server (docs/MATCH_FLOW.md *D1k: waves one by one, and the minions' repulsion*).
//
// Every server mover gets a NavPower repulsor (the mover constructor's Mover::EnableRepulsor) with the flags the client
// sets for it (locomotion +0x94/+0x98: an NPC is pushed by players and NPCs), the radius from extentsNavmesh and
// LabsTuning's cushions, but on the host they never pushed each other: d1k_run1's minions walked 0.97 units apart
// with a mover radius of 1.24. NavPower's navigator module resolves repulsors only in a space whose constraint solver
// is on, which starts off and which nothing in the client switches on (game.h *navpower*) [inferred: the stripped
// server shell did]. The host does it for the server simulator's space after the level load: in d1k_run2 the same
// wave kept 2.47-2.52 units between centres (twice the mover radius), so the minions collide and push each other.
// Evidence that the original's units collided: the data marks 140 movers noRepulsion (ghosts, tentacles, ability
// objects), and modifier_Item_Legendary_UnitPassThrough and modifier_Artificer_E_Spin give the attribute
// UnitPassThrough, which only switches a mover's repulsor blockage off (0x0089D1B0).
// [host]/[dgsim] navpower_constraint_solver (1; 0 = off, as the client build leaves it).
#pragma once

#include <cstddef>
#include <cstdint>

namespace crowd {

struct Options {
  bool constraintSolver = true;
};

void Configure(const Options& options);

// After the level load, with the server simulator bound: switches the space's constraint solver on (the option) and
// logs the space, the flag before and after, and the navigator module's navigators and repulsors.
void OnLevelStarted(void* sim);

// The mover of object `id` (object +0x8DC): its repulsor (+0x1B8) and whether it is on (+0x1C0), for the log.
bool MoverRepulsor(std::uint32_t id, void** mover, void** repulsor, unsigned* on);

// "constraint solver N, navigators N, repulsors N" (the server space's flag and the navigator module's counts).
void FormatStats(char* buf, std::size_t size);

}  // namespace crowd
