// FOGSLIDE: the position sample a full state carries (gms/core). No game addresses: the engine side
// (gms/engine/replicate.cpp) passes the locomotion data's values.
//
// A client walks every object it doesn't predict to the last position sample it got (cLocomotionData's
// mClientPathBlock/XY at +0xE0/+0xE2; Locomotion_TickMovers' client branch, D2g-static). The server takes a sample only
// from a walking NavPower mover (Locomotion_UpdateClientPathPosition 0x0089FDB0, 0.2 s ahead, every 0.75 units, 0.05
// when it stops). A jump or a slide (nLocomotion.SlideInDirection/SlideToPoint/Jump*, every nCharge, Locomotion_StartJump
// 0x00890C40) takes none, during or after it: the clients that see its start get ObjectJump (96) and play it, and
// keep the shaper where it lands. A client whose team didn't see the start gets no 96 (a kind-2 message goes only to
// the players whose team sees the object, FOGSOUND), and its fog exit's full state (or a create, or a rejoin's catch-up)
// carried the old sample from before the jump: the client walked the shaper back there (into the fog) and kept it there
// until the shaper's next walk made new samples (the user's report: Dibs's R, Viridian's R and W).
//
// So a full state's sample is replaced with where the object really is going when the server's own is stale: the
// jump's landing point while it jumps (locomotion +0xF0, SetJumpState 0x0088F130: 0 = none), otherwise its position
// when the sample is farther from it than a walking mover's look-ahead allows. A fresh sample of a walking object stays.
#pragma once

#include <cmath>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <vector>

namespace gms {

// LocomotionData_QuantizePosition (0x00548880): x and y in [-500, 500] to 16 bits each (x + 500) * 65.535, truncated;
// the block holds both high bytes (y's in its high byte), xy both low bytes. Outside the range the engine asserts; the
// host clamps.
inline void QuantizeSamplePosition(float x, float y, std::uint16_t* block, std::uint16_t* xy) {
  auto q = [](float v) -> unsigned {
    if (!(v >= -500.f)) v = -500.f;  // NaN too
    if (v > 500.f) v = 500.f;
    const unsigned u = static_cast<unsigned>((v + 500.f) * 65.535f);
    return u > 0xFFFFu ? 0xFFFFu : u;
  };
  const unsigned qx = q(x), qy = q(y);
  *block = static_cast<std::uint16_t>((qy >> 8) << 8 | (qx >> 8));
  *xy = static_cast<std::uint16_t>((qy & 0xFFu) << 8 | (qx & 0xFFu));
}

// The inverse, as the client reads a sample (GameClient_OnGmsObjectJump 0x006B85C0's start position).
inline void DequantizeSamplePosition(std::uint16_t block, std::uint16_t xy, float* x, float* y) {
  const unsigned qx = (block & 0xFFu) << 8 | (xy & 0xFFu), qy = (block >> 8) << 8 | (xy >> 8);
  *x = static_cast<float>(qx) * (1000.f / 65535.f) - 500.f;
  *y = static_cast<float>(qy) * (1000.f / 65535.f) - 500.f;
}

// A walking mover's sample lies up to 0.2 s of its velocity ahead plus the 0.75-unit resampling distance from where
// it is; with room for a turn and the quantization.
constexpr float kSampleSlack = 1.0f;
constexpr float kSampleLookAheadS = 0.3f;

struct SampleInput {
  bool jumping = false;              // locomotion +0xF0 != 0
  float pos[2] = {0.f, 0.f};         // the object's position (+0x10)
  float jumpGoal[2] = {0.f, 0.f};    // locomotion +0x150 (Locomotion_StartJump's landing point)
  std::uint16_t block = 0, xy = 0;   // the server's sample (+0xE0/+0xE2)
  float velocity[2] = {0.f, 0.f};    // the object's velocity (+0x30, the mover's)
};

// Whether a full state should carry another sample than the server's, and which (`block`, `xy`; `at` gets x and y):
// the landing point while the object jumps, its position when the server's sample is farther from it than
// kSampleSlack + 0.3 s of its speed. False: the server's sample is fine (or already that one).
inline bool FreshSample(const SampleInput& in, std::uint16_t* block, std::uint16_t* xy, float at[2]) {
  if (in.jumping) {
    at[0] = in.jumpGoal[0];
    at[1] = in.jumpGoal[1];
  } else {
    float sx, sy;
    DequantizeSamplePosition(in.block, in.xy, &sx, &sy);
    const float speed = std::sqrt(in.velocity[0] * in.velocity[0] + in.velocity[1] * in.velocity[1]);
    const float limit = kSampleSlack + kSampleLookAheadS * speed;
    const float dx = sx - in.pos[0], dy = sy - in.pos[1];
    if (dx * dx + dy * dy <= limit * limit) return false;
    at[0] = in.pos[0];
    at[1] = in.pos[1];
  }
  QuantizeSamplePosition(at[0], at[1], block, xy);
  return *block != in.block || *xy != in.xy;
}

// FOGSLIDE (jump replay): a fog exit in the middle of a jump re-sends the jump's ObjectJump (96) to the players who
// see the object from now on, so their game plays the rest as a slide (Locomotion_TickJump 0x008A2550: a slide moves at
// its speed, locomotion +0x1C8, from the client's own position for any object but the player's own creature,
// GameClient_OnGmsObjectJump 0x006B85C0) instead of walking it to its sample. The message as GameSimulator_SendObjectJump
// (0x008F5D40) writes it: the wire byte, then 153 bytes: u32 id, f32 x 3 the landing point (+0x150), the path's 8
// waypoints (+0x15C, 96 bytes; zero = straight to the landing point), +0x1BC..+0x1D4, u8 +0x1E9, the quantized start
// (+0x144: block, xy) and +0x210/+0x20C (the arc's height and the distance moved so far). An enemy shaper's game takes
// the start and those two from the message (the reader's second branch; seen live: a replay with the start in the fog
// slid the whole original length from where the shaper appeared, then snapped back to the landing point).
constexpr std::size_t kObjectJumpSize = 154;
constexpr std::size_t kObjectJumpGoalAt = 5;
constexpr std::size_t kObjectJumpPathAt = 17;
constexpr std::size_t kObjectJumpPathSize = 96;
constexpr std::size_t kObjectJumpStartAt = 142;  // u16 block, u16 xy
constexpr std::size_t kObjectJumpMovedAt = 146;  // f32 +0x210, f32 +0x20C

// The replay of `jump` (an ObjectJump as the engine sent it at the jump's start): it starts at `start` (where the late
// player's game shows the object: the fog exit's pose) with nothing moved yet, its landing point is `goal` (the
// server's current one: a jump at a moving target re-aims) and its path is cleared (its waypoints lead from the start
// the late player never saw). False if `jump` isn't an ObjectJump of that size.
inline bool JumpReplay(const std::vector<std::uint8_t>& jump, const float start[2], const float goal[3],
                       std::vector<std::uint8_t>* out) {
  if (jump.size() != kObjectJumpSize || jump[0] != 0x96) return false;
  *out = jump;
  std::memcpy(out->data() + kObjectJumpGoalAt, goal, 12);
  std::memset(out->data() + kObjectJumpPathAt, 0, kObjectJumpPathSize);
  std::uint16_t block = 0, xy = 0;
  QuantizeSamplePosition(start[0], start[1], &block, &xy);
  std::uint8_t* p = out->data() + kObjectJumpStartAt;
  p[0] = static_cast<std::uint8_t>(block);
  p[1] = static_cast<std::uint8_t>(block >> 8);
  p[2] = static_cast<std::uint8_t>(xy);
  p[3] = static_cast<std::uint8_t>(xy >> 8);
  std::memset(out->data() + kObjectJumpMovedAt, 0, 8);
  return true;
}

}  // namespace gms
