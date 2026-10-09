// gms/core/vision (D2g-fog-core): the per-player fog-of-war transitions and their messages, engine-free
// (docs/GMS_PROTOCOL.md *Fog of war on the host: the engine-free core (D2g-fog-core)*). Pure data in, an action list
// out: the host (D2g-live step 2) executes the actions in order and then writes the new visible masks back.
//
// The client gets fog only through these messages (readers in SporeOnline_Common.cpp):
//   93 ObjectEnterFoW (id): GameClient_OnGmsObjectEnterFoW 0x006B83C0 asserts the object is not disabled
//      ((flags & (kCF_Disabled | kCF_SpectatorDisabled)) == 0), sets kCF_Disabled and, with
//      Noun.fowControlsObjectVisibility, stops it and plays "Set_FOW_Invisible". So never twice in a row.
//   94 ObjectPrepareForExitFoW (id, f32 pos[3], f32 Euler radians[3]; 28 bytes): 0x006B8080 asserts it is disabled,
//      sets kCF_PreparingForEnable and teleports it to the pose (Math_EulerToQuaternion 0x00673A90).
//   95 ObjectExitFoW (id, n x 41-byte modifier records): 0x006BB7D0 rebuilds the modifiers, enables the object,
//      "Set_FOW_Visible". Between 94 and 95 the host sends the full state of every struct (the player missed the
//      deltas), written without the field states after the tick's delta pass.
//   A3 DisabledObjectUpdate (id, u16 block, u16 xy): 0x006B9B10 asserts the object is disabled and not preparing, and
//      moves it (z = 0) without enabling it. For MaintainPosThroughFow objects (record +0x20 > 0).
// The masks on the server: network component +0xE = the players that know the object, +0x10 = the players that
// currently see it (deltas and kind-2 messages go to +0xE & +0x10). A player sees an object when its team's bit is in
// the vision model's answer; a player without a team (>= kNumTeams, e.g. a spectator) sees everything [assumed].
#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_map>
#include <vector>

#include "core/common/bytes.h"
#include "core/vision/vision.h"

namespace gms::vision {

constexpr size_t kPrepareExitFowBodySize = 28;
constexpr size_t kDisabledObjectUpdateBodySize = 8;
// A maintained object's next A3 after it moved this far from the last one [assumed: the server's own sampling step,
// Locomotion_UpdateClientPathPosition 0x0089FDB0, 0.75 units].
constexpr float kDisabledUpdateMinDistance = 0.75f;

struct FogPlayer {
  int index = -1;     // gameplay index (bit in the masks), 0..15
  uint8_t team = 0;   // the player's team
};

// B15b: a spectator (gameplay index 10-14) is a FogPlayer without a team, whatever mTeam its player carries: it sees
// everything, so the planner never fogs an object for it (no 93/94/95/A3, which would assert against the client's
// spectator view: 93 asserts the object is neither kCF_Disabled nor kCF_SpectatorDisabled), its visible bit stays set
// and every kind-3 message reaches it. Its client hides what the watched teams can't see by mVisibleToTeam itself
// (SpectatorState_UpdateObjectVisibility 0x005490C0; kVisionBlueRed, teams 1 and 2, by default).
constexpr uint8_t kObserverTeam = 0xFF;
inline FogPlayer ObserverFogPlayer(int index) {
  FogPlayer p;
  p.index = index;
  p.team = kObserverTeam;
  return p;
}

struct FogObject {
  uint32_t id = 0;
  uint16_t known = 0;            // network component +0xE
  uint16_t visible = 0;          // +0x10, as the host left it (creates set it with +0xE)
  uint8_t teamBits = kAllTeams;  // the vision model's answer
  bool maintainPos = false;      // record +0x20 > 0
  float x = 0.f, y = 0.f;        // its position now (A3)
};

enum class FogActionKind : uint8_t {
  kEnter,        // 93; then clear the player's bit in +0x10
  kExit,         // 94 (pose) + the full state of every struct + 95 (the registry's records); then set the bit
  kDisabledPos,  // A3 with the object's position
};

struct FogAction {
  FogActionKind kind = FogActionKind::kEnter;
  int player = -1;
  uint32_t object = 0;
  size_t objectIndex = 0;  // into the Plan input
};

// The players of `players` whose team is in `teamBits` (players without a team: always). Recipients of a kind-3
// message from VisionModel::TeamsSeeingPoint, and the +0x10 mask a fresh create would get.
uint16_t PlayersForTeams(uint8_t teamBits, const std::vector<FogPlayer>& players);

class FogPlanner {
 public:
  // One tick, after the delta pass (step 5 of the replication step). For every object and every player in
  // `players` that knows it: seen now (its team in teamBits) vs. shown (its bit in `visible`):
  //   shown, not seen -> kEnter; not shown, seen -> kExit; neither, maintainPos and moved >= 0.75 units since the
  //   last A3 -> kDisabledPos (not to a player that enters fog in this tick: it got this tick's position delta).
  // Actions come in object order, then player order. `visibleAfter` (optional) gets every object's +0x10 after the
  // actions (bits of players outside `players` are kept). Objects not in this call are forgotten.
  std::vector<FogAction> Plan(const std::vector<FogObject>& objects, const std::vector<FogPlayer>& players,
                              std::vector<uint16_t>* visibleAfter = nullptr);

 private:
  struct LastPos {
    float x, y;
  };
  std::unordered_map<uint32_t, LastPos> lastA3_;  // per maintained object: where its last A3 (or fog entry) put it
};

// ---- messages (wire byte first) --------------------------------------------------------------------------------------
Bytes BuildObjectEnterFoW(uint32_t id);  // 93 id (5 bytes)
Bytes BuildObjectPrepareForExitFoW(uint32_t id, const float pos[3], const float eulerRadians[3]);  // 94 (29 bytes)
Bytes BuildObjectExitFoW(uint32_t id, const Bytes& modifierRecords);  // 95 id + ModifierRegistry::RecordsFor(id)
Bytes BuildDisabledObjectUpdate(uint32_t id, float x, float y);       // A3 id block xy (9 bytes)

// LocomotionData_QuantizePosition (0x00548880; |x|, |y| <= 500): q = (c + 500) x 65.535 truncated to 16 bits;
// block = qx >> 8 | (qy >> 8) << 8, xy = (qx & 0xFF) | (qy & 0xFF) << 8. The client decodes
// c = ((hi byte) << 8 | lo byte) / 65.535 - 500 (0x006B9B10).
void QuantizeXY(float x, float y, uint16_t* block, uint16_t* xy);
void DequantizeXY(uint16_t block, uint16_t xy, float* x, float* y);

// The object's orientation (cGameObject +0x1C, quaternion x, y, z, w) as the Euler angles (radians) 94 carries:
// the inverse of Math_EulerToQuaternion (0x00673A90: q = qz(ez) qy(ey) qx(ex), the usual roll-pitch-yaw order).
void QuaternionToEuler(const float q[4], float euler[3]);
void EulerToQuaternion(const float euler[3], float q[4]);  // the client's formula, for checks

}  // namespace gms::vision
