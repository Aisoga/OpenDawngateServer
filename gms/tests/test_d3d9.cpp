// H1d: the headless host without Direct3D 9 (core/d3d9/stand_in.h): the plan from the [host] keys, the start-up
// verdict and the stand-in IDirect3D9, called through its vtable the way the game's code calls an IDirect3D9 (x86 COM:
// stdcall, `this` first), so a wrong argument count would unbalance the stack here as it would in the host.
#include <cstdint>
#include <string>

#include "core/d3d9/stand_in.h"
#include "tests/check.h"

using namespace gms::d3d9;

namespace {

using U = std::uintptr_t;

template <typename Fn>
Fn Method(const StandIn& s, int slot) {
  return reinterpret_cast<Fn>(const_cast<void*>(s.vtbl[slot]));
}

int g_seenSlot = -1;
long g_seenCount;
void Seen(const StandIn&, int slot, void*) {
  g_seenSlot = slot;
  ++g_seenCount;
}

}  // namespace

GMS_TEST(D3d9PlanFromHostKeys) {
  // the default host (core_only 1, d3d9 0): no D3D9
  Plan p = Decide(1, 0, false);
  CHECK(p.answerRwDevice);
  CHECK(p.skipResolutionList);
  CHECK(!p.failCreate);
  // the way back: the game's own RwDevice_Initialize
  p = Decide(1, 1, false);
  CHECK(!p.answerRwDevice);
  CHECK(!p.skipResolutionList);
  // stage 1 and core_only 2 make the device, so they keep D3D9 whatever d3d9 says
  CHECK(!Decide(0, 0, false).answerRwDevice);
  CHECK(!Decide(2, 0, false).answerRwDevice);
  // the test aid works in every mode
  CHECK(Decide(1, 0, true).failCreate);
  CHECK(Decide(1, 1, true).failCreate);
  CHECK(Decide(0, 0, true).failCreate);
}

GMS_TEST(D3d9Verdict) {
  CHECK_EQ(std::string(Verdict(0, 0)), std::string("not touched"));
  CHECK_EQ(std::string(Verdict(0, 3)), std::string("not touched, but the stand-in was called"));
  CHECK_EQ(std::string(Verdict(1, 0)), std::string("TOUCHED"));
}

GMS_TEST(D3d9StandInAnswersAsNoAdapter) {
  StandIn s;
  Init(s, &Seen);
  CHECK_EQ(s.Total(), 0L);
  // RwDevice_Initialize's calls, in its order: GetAdapterCount, GetDeviceCaps, GetAdapterDisplayMode,
  // CheckDeviceFormat, CheckDepthStencilMatch, Release
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*)>(s, kGetAdapterCount)(&s), 0u);
  CHECK_EQ(g_seenSlot, static_cast<int>(kGetAdapterCount));
  unsigned char caps[304] = {};
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U)>(s, kGetDeviceCaps)(&s, 0, 1, reinterpret_cast<U>(caps)),
           kErrNotAvailable);
  unsigned char mode[16] = {};
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*, U, U)>(s, kGetAdapterDisplayMode)(&s, 0, reinterpret_cast<U>(mode)),
           kErrNotAvailable);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U, U, U)>(s, kCheckDeviceFormat)(&s, 0, 1, 22, 2, 1, 75)),
           kErrNotAvailable);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U, U)>(s, kCheckDepthStencilMatch)(&s, 0, 1, 22, 22, 75)),
           kErrNotAvailable);
  // the config manager's enumeration and the device paths
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U)>(s, kGetAdapterModeCount)(&s, 0, 22)), 0u);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U)>(s, kEnumAdapterModes)(&s, 0, 22, 0,
                                                                                           reinterpret_cast<U>(mode))),
           kErrNotAvailable);
  CHECK((Method<void*(__stdcall*)(StandIn*, U)>(s, kGetAdapterMonitor)(&s, 0)) == nullptr);
  void* device = &s;  // must come back null
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U, U, U)>(s, kCreateDevice)(
               &s, 0, 1, 0, 0x40, reinterpret_cast<U>(mode), reinterpret_cast<U>(&device))),
           kErrNotAvailable);
  CHECK(device == nullptr);
  std::uint32_t quality = 7;
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U, U, U)>(s, kCheckDeviceMultiSampleType)(
               &s, 0, 1, 22, 1, 0, reinterpret_cast<U>(&quality))),
           kErrNotAvailable);
  CHECK_EQ(quality, 0u);
  void* iface = &s;
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U)>(s, kQueryInterface)(&s, 0, reinterpret_cast<U>(&iface))),
           kErrNoInterface);
  CHECK(iface == nullptr);
  // the reference count never drops below one: the object is static
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*)>(s, kAddRef)(&s), 2u);
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*)>(s, kRelease)(&s), 1u);
  CHECK_EQ(Method<std::uint32_t(__stdcall*)(StandIn*)>(s, kRelease)(&s), 1u);
  CHECK_EQ(s.calls[kRelease], 2L);
  CHECK_EQ(s.Total(), 14L);
  CHECK_EQ(g_seenCount, 14L);
  CHECK_EQ(std::string(SlotName(kGetDeviceCaps)), std::string("GetDeviceCaps"));
  CHECK_EQ(std::string(SlotName(kCreateDevice)), std::string("CreateDevice"));
  CHECK_EQ(std::string(SlotName(99)), std::string("?"));
}

// The other methods answer too, and every slot is a distinct function (a vtable slip would show here).
GMS_TEST(D3d9StandInEverySlot) {
  StandIn s;
  Init(s);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U)>(s, kRegisterSoftwareDevice)(&s, 0)), kErrNotAvailable);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U)>(s, kGetAdapterIdentifier)(&s, 0, 0, 0)),
           kErrNotAvailable);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U, U)>(s, kCheckDeviceType)(&s, 0, 1, 22, 22, 1)),
           kErrNotAvailable);
  CHECK_EQ((Method<std::uint32_t(__stdcall*)(StandIn*, U, U, U, U)>(s, kCheckDeviceFormatConversion)(&s, 0, 1, 22, 22)),
           kErrNotAvailable);
  for (int i = 0; i < kSlotCount; ++i)
    for (int j = i + 1; j < kSlotCount; ++j) CHECK(s.vtbl[i] != s.vtbl[j]);
  CHECK_EQ(s.calls[kRegisterSoftwareDevice] + s.calls[kGetAdapterIdentifier] + s.calls[kCheckDeviceType] +
               s.calls[kCheckDeviceFormatConversion],
           4L);
}
