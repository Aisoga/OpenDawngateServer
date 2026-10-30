// gms/core/replication/effect_ledger.h: the attached effects an ended modifier leaves behind (FOGSTACKS).
#include <cstdint>
#include <set>
#include <vector>

#include "core/replication/effect_ledger.h"
#include "tests/check.h"

using namespace gms::replication;

namespace {

constexpr int kAbility = 0;
constexpr int kModifier = 1;
constexpr std::uintptr_t kCounterStatic = 0x1000;  // made-up static ability pointers
constexpr std::uintptr_t kSlowStatic = 0x2000;
constexpr std::uintptr_t kQStatic = 0x3000;
constexpr std::uint32_t kVictim = 0x7C7F0124;
constexpr std::uint32_t kOther = 0x7C79012C;

EffectEntry E(std::uint32_t object, std::uint32_t handle) {
  EffectEntry e;
  e.object = object;
  e.handle = handle;
  return e;
}

}  // namespace

// Duress's counter: marker1 at Activate, destroyed at the second stack and marker2 made; no Deactivate, so marker2 is
// the leftover once the instance is gone.
GMS_TEST(FogStacksDuressMarkerIsLeftover) {
  EffectLedger l;
  CHECK(l.OnCreated(0x8440003F, kModifier, kCounterStatic, "counter", E(kVictim, 0x00012300)));
  CHECK(l.OnDestroyed(kVictim, 0x00012300));
  CHECK(l.OnCreated(0x8440003F, kModifier, kCounterStatic, "counter", E(kVictim, 0x00012401)));
  CHECK_EQ(l.Effects(), static_cast<std::size_t>(1));
  std::set<std::uint32_t> alive = {0x8440003F};
  auto isAlive = [&](std::uint32_t i, std::uintptr_t) { return alive.count(i) != 0; };
  CHECK(l.TakeEnded(isAlive).empty());  // still running
  alive.clear();
  const auto ended = l.TakeEnded(isAlive);
  CHECK_EQ(ended.size(), static_cast<std::size_t>(1));
  CHECK_EQ(ended[0].instance, 0x8440003Fu);
  CHECK_EQ(ended[0].effects.size(), static_cast<std::size_t>(1));
  CHECK_EQ(ended[0].effects[0].handle, 0x00012401u);
  CHECK_EQ(EffectSlotOf(ended[0].effects[0].handle), 1);
  CHECK_EQ(l.Owners(), static_cast<std::size_t>(0));
  CHECK(l.TakeEnded(isAlive).empty());  // taken once
}

// A slow whose Deactivate destroys its effect leaves nothing.
GMS_TEST(FogStacksDestroyedEffectNoLeftover) {
  EffectLedger l;
  CHECK(l.OnCreated(0x84400050, kModifier, kSlowStatic, "slow", E(kVictim, 0x00012502)));
  CHECK(l.OnDestroyed(kVictim, 0x00012502));
  CHECK_EQ(l.Owners(), static_cast<std::size_t>(0));
  CHECK(l.TakeEnded([](std::uint32_t, std::uintptr_t) { return false; }).empty());
  CHECK(!l.OnDestroyed(kVictim, 0x00012502));  // unknown now
}

// An ability's effects are forgotten at its end, never returned (it may hand them on).
GMS_TEST(FogStacksAbilityEffectsDropped) {
  EffectLedger l;
  CHECK(l.OnCreated(0x84400060, kAbility, kQStatic, "q", E(kOther, 0x00012603)));
  const auto ended = l.TakeEnded([](std::uint32_t, std::uintptr_t) { return false; });
  CHECK(ended.empty());
  CHECK_EQ(l.Owners(), static_cast<std::size_t>(0));
}

// A reused instance id with another static ability: the old owner counts as ended.
GMS_TEST(FogStacksReusedInstanceId) {
  EffectLedger l;
  CHECK(l.OnCreated(0x84400070, kModifier, kCounterStatic, "counter", E(kVictim, 0x00012704)));
  CHECK(l.OnCreated(0x84400070, kModifier, kSlowStatic, "slow", E(kVictim, 0x00012805)));
  const auto ended = l.TakeEnded([](std::uint32_t, std::uintptr_t ident) { return ident == kSlowStatic; });
  CHECK_EQ(ended.size(), static_cast<std::size_t>(1));
  CHECK_EQ(ended[0].ident, kCounterStatic);
  CHECK_EQ(ended[0].effects[0].handle, 0x00012704u);
  CHECK_EQ(l.Owners(), static_cast<std::size_t>(1));
  CHECK_EQ(l.Effects(), static_cast<std::size_t>(1));
}

// Nothing is recorded without an instance, an object or a handle; a long-lived instance keeps only its newest effects.
GMS_TEST(FogStacksLimits) {
  EffectLedger l;
  CHECK(!l.OnCreated(0, kModifier, kCounterStatic, "x", E(kVictim, 0x100)));
  CHECK(!l.OnCreated(1, kModifier, kCounterStatic, "x", E(0, 0x100)));
  CHECK(!l.OnCreated(1, kModifier, kCounterStatic, "x", E(kVictim, 0)));
  for (std::uint32_t i = 0; i < EffectLedger::kMaxPerInstance + 5; ++i)
    CHECK(l.OnCreated(1, kModifier, kCounterStatic, "x", E(kVictim, (i + 1) << 8)));
  CHECK_EQ(l.Effects(), EffectLedger::kMaxPerInstance);
  const auto ended = l.TakeEnded([](std::uint32_t, std::uintptr_t) { return false; });
  CHECK_EQ(ended.size(), static_cast<std::size_t>(1));
  CHECK_EQ(ended[0].effects.front().handle, 6u << 8);  // the first five went
}

// The engine destroys a leftover only while its slot still holds that handle.
GMS_TEST(FogStacksStillAttached) {
  CHECK(EffectStillAttached(0x00012401, true, 0x00012401));
  CHECK(!EffectStillAttached(0x00012401, false, 0x00012401));  // the slot is empty
  CHECK(!EffectStillAttached(0x00013001, true, 0x00012401));   // the slot was reused
  CHECK(!EffectStillAttached(0x00012420, true, 0x00012420));   // no slot 32 on a server object
}
