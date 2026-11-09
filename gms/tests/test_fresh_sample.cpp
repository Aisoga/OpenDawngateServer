// FOGSLIDE: the position sample a full state carries (core/replication/fresh_sample.h): the engine's quantization, the
// client's reading of it, and which sample replaces a stale one.
#include <cmath>
#include <cstdint>

#include "core/replication/fresh_sample.h"
#include "tests/check.h"

namespace {

// A coordinate that quantizes to `q` (the middle of its step, away from the truncation's edge).
float At(unsigned q) { return (static_cast<float>(q) + 0.5f) / 65.535f - 500.f; }

}  // namespace

// LocomotionData_QuantizePosition (0x00548880): the block holds both high bytes, xy both low bytes (y's in the high byte).
GMS_TEST(FreshSampleQuantizeLayout) {
  std::uint16_t block = 0, xy = 0;
  gms::QuantizeSamplePosition(At(0x1234), At(0xABCD), &block, &xy);
  CHECK_EQ(block, 0xAB12);
  CHECK_EQ(xy, 0xCD34);
  gms::QuantizeSamplePosition(-500.f, -500.f, &block, &xy);
  CHECK_EQ(block, 0x0000);
  CHECK_EQ(xy, 0x0000);
  gms::QuantizeSamplePosition(800.f, -900.f, &block, &xy);  // clamped, as the engine would assert
  CHECK_EQ(block, 0x00FF);
  CHECK_EQ(xy, 0x00FF);
}

// The client's reading (GameClient_OnGmsObjectJump 0x006B85C0): a sample comes back within one step (1/65.535 unit).
GMS_TEST(FreshSampleRoundTrip) {
  const float pts[][2] = {{0.f, 0.f}, {52.17f, -83.23f}, {-234.93f, -229.92f}, {225.5f, 225.86f}, {499.9f, -499.9f}};
  for (const auto& p : pts) {
    std::uint16_t block = 0, xy = 0;
    gms::QuantizeSamplePosition(p[0], p[1], &block, &xy);
    float x = 0.f, y = 0.f;
    gms::DequantizeSamplePosition(block, xy, &x, &y);
    CHECK(std::fabs(x - p[0]) < 0.016f);
    CHECK(std::fabs(y - p[1]) < 0.016f);
  }
}

// The replacement: the landing point while jumping; the position when the server's sample is stale; a walking mover's
// look-ahead sample and a stopped mover's own stay.
GMS_TEST(FreshSampleChoice) {
  gms::SampleInput in;
  std::uint16_t block = 0, xy = 0;
  float at[2] = {0.f, 0.f};
  // seen live: the bot slid out of the fog toward (62.94, -68.04); the server's sample was from before the slide
  gms::QuantizeSamplePosition(31.53f, -28.56f, &in.block, &in.xy);
  in.pos[0] = 53.7f;
  in.pos[1] = -56.4f;
  in.jumping = true;
  in.jumpGoal[0] = 62.94f;
  in.jumpGoal[1] = -68.04f;
  CHECK(gms::FreshSample(in, &block, &xy, at));
  CHECK_EQ(at[0], 62.94f);
  CHECK_EQ(at[1], -68.04f);
  std::uint16_t b2 = 0, x2 = 0;
  gms::QuantizeSamplePosition(62.94f, -68.04f, &b2, &x2);
  CHECK_EQ(block, b2);
  CHECK_EQ(xy, x2);
  // landed, standing: its position
  in.jumping = false;
  in.pos[0] = 62.94f;
  in.pos[1] = -68.04f;
  CHECK(gms::FreshSample(in, &block, &xy, at));
  CHECK_EQ(at[0], 62.94f);
  CHECK_EQ(block, b2);
  // the sample already where it should be: nothing to change
  in.block = b2;
  in.xy = x2;
  CHECK(!gms::FreshSample(in, &block, &xy, at));
  in.jumping = true;
  CHECK(!gms::FreshSample(in, &block, &xy, at));
  in.jumping = false;
  // walking at 10.4 units/s with the sample 2.5 units ahead (0.2 s): fresh
  gms::QuantizeSamplePosition(65.44f, -68.04f, &in.block, &in.xy);
  in.velocity[0] = 10.4f;
  CHECK(!gms::FreshSample(in, &block, &xy, at));
  // standing with the sample 0.6 units off (a stop sample, the quantization): fresh
  in.velocity[0] = 0.f;
  gms::QuantizeSamplePosition(63.54f, -68.04f, &in.block, &in.xy);
  CHECK(!gms::FreshSample(in, &block, &xy, at));
  // standing 1.5 units from it: stale
  gms::QuantizeSamplePosition(64.44f, -68.04f, &in.block, &in.xy);
  CHECK(gms::FreshSample(in, &block, &xy, at));
  CHECK_EQ(block, b2);
  CHECK_EQ(xy, x2);
}
