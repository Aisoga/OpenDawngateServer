// Who gets a player's labsPlayerFowData (gms/core, D2h; docs/GMS_PROTOCOL.md *The enemy players' level and FoW data
// (D2h)*). No game addresses: the engine side (gms/engine/gms_host.cpp FowDataStep) passes each player's team mask and
// the players that currently see its creature, and sends what Route returns.
//
// LabsPlayerFowUpdate (AA, id 43) carries labsPlayerFowData: XP, available and total gold, creeps, level, ability and
// spell points, the ranks and the 11 item slots of one player. The client reads it for any player without a team
// check (GameClient_OnGmsLabsPlayerUpdate 0x006B9A70 -> GameSimulator_ReadLabsPlayerUpdate 0x008F80B0) and shows it
// for enemies too: the level on every player's health bar (HealthBars_UpdateAll 0x004BC9E0: LabsPlayer_GetLevel of the
// creature's player, no team test), the target panel (TargetPanelUI_Update 0x00531D80), the scoreboard's rows of both
// teams (level, creeps, total gold and the items: ScoreboardUI_UpdatePlayerRow 0x00505B30, ScoreboardUI_UpdateRows
// 0x005060E0), and GameObject_GetLevelIndex (0x008808B0, a player creature's level for its per-level class attributes).
// The server shell that sent it is stripped. The rule here is the binary's own rule for data about an object that fog
// of war hides, GameSimulator_GetPlayersThatSeeObject (0x008F0A30: GameSimulator_GetPlayerMaskForTeams of the object's
// mVisibleToTeam, spectators included; SetObjectGfxState's recipients), applied to the player's creature, and the
// struct's name says the same ("Fow" data next to the unfogged labsPlayerGlobalData that goes to everyone) [the
// recipients are inferred, not read from a sender]. Per-player private data has its own team-only rule in the binary
// (AbilityUpdate BC: GameSimulator_GetPlayerMaskForTeams(1 << team)), which is not this struct's.
//
// So a player's AA goes to:
//   - its own team's players with the world and the spectators, always (they always see its creature anyway);
//   - with byVision, every other player that currently sees its creature (network component +0xE & +0x10 after the
//     tick's fog transitions; without fog of war that is everyone who knows the creature).
// A recipient that starts receiving it (an enemy that sees the creature again, a player that just got its world, a
// rejoin) missed the deltas since it last did, so it gets the whole struct (members 0-62) once; the others get the
// tick's delta. A recipient that stops receiving it keeps its last copy (the enemy's level as last seen) until the next
// whole struct. The field state (cLabsPlayer +0x3AC) stays one per player: the engine writes the delta first (which
// advances it) and the whole struct after it, so both carry the current values.
#pragma once

#include <array>
#include <cstdint>

namespace gms {

constexpr int kFowDataMembers = 63;  // labsPlayerFowData members 0-62

// The players that get player P's AA this tick (bit = gameplay index): `teamMask` (P's team with the world and the
// spectators), and with `byVision` also `creatureSeers` (the players that know and see P's creature now), each limited
// to `world` (the players with the world).
inline uint16_t FowDataRecipients(uint16_t teamMask, uint16_t creatureSeers, uint16_t world, bool byVision) {
  const uint16_t to = byVision ? static_cast<uint16_t>(teamMask | creatureSeers) : teamMask;
  return static_cast<uint16_t>(to & world);
}

struct FowDataSends {
  uint16_t delta = 0;    // had every earlier change: the tick's delta, if any
  uint16_t full = 0;     // new recipients: the whole struct (members 0-62)
  uint16_t dropped = 0;  // stopped receiving it this tick: their copy goes stale until their next whole struct
};

// Per player, the recipients that are in step with its field state (they got every delta since their last whole
// struct). Route is called every tick for every replicated player, with or without a delta, so a newcomer is seen in
// the tick it appears.
class FowDataRouter {
 public:
  FowDataSends Route(int player, uint16_t recipients) {
    FowDataSends s;
    if (player < 0 || player >= kSlots) return s;
    uint16_t& synced = synced_[static_cast<size_t>(player)];
    s.delta = static_cast<uint16_t>(recipients & synced);
    s.full = static_cast<uint16_t>(recipients & ~synced);
    s.dropped = static_cast<uint16_t>(synced & ~recipients);
    synced = recipients;
    return s;
  }
  // The whole struct for `failed` could not be written: they are not in step (the next tick tries again).
  void Unsync(int player, uint16_t failed) {
    if (player >= 0 && player < kSlots)
      synced_[static_cast<size_t>(player)] = static_cast<uint16_t>(synced_[static_cast<size_t>(player)] & ~failed);
  }
  uint16_t Synced(int player) const {
    return player >= 0 && player < kSlots ? synced_[static_cast<size_t>(player)] : 0;
  }

 private:
  static constexpr int kSlots = 16;  // gameplay indices 0-15 (players 0-9, spectators 10-14)
  std::array<uint16_t, kSlots> synced_{};
};

}  // namespace gms
