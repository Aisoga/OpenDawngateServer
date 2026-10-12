// H1d: the headless host without Direct3D 9,
// engine-free and header-only, so gms_tests can check it without the game.
//
// What the binary does (Dawngate.exe 854): the app system's systems set-up (slot 5, 0x0096A7B0, at 0x006B25F5 in
// SporeApp_Initialize, before the core-only jump 0x006B26C8) creates the graphics system (0x00A63A20) and its render
// manager (GraphicsSystem_CreateRenderManager 0x00A63540 -> RenderManager_InitializeDevice 0x00997150), which calls
// RwDevice_Initialize (0x00B914C0, rw::graphics::Device::Initialize): Direct3DCreate9(32) into g_RwD3D9 (0x01BCE0E0),
// D3DXCheckVersion, the adapter count (0x01BD00A4), GetDeviceCaps HAL per adapter (else REF on adapter 0), the
// adapter's display mode, CheckDeviceFormat/CheckDepthStencilMatch for nine depth formats. Any failure returns 0, and
// the app system shows "Could not start the renderer. Please ensure your display is set to 32 bits." (error 1001,
// App_ShowAlertMessageBox 0x00A97980; a host answers it unseen) and SporeApp_Initialize shuts down: the host can't
// start on a machine without a Direct3D 9 HAL. Then the config manager's set-up (0x00963750, the same app-system step)
// enumerates the adapters' modes through g_RwD3D9 (0x00961FA0). Every other user of g_RwD3D9 is on the device's
// paths (AppSystem_CreateGraphicsDevice and later), which a core-only host (core_only = 1) never runs.
//
// The host's H1d rule (core_only = 1, [host] d3d9 = 0, the default): RenderManager_InitializeDevice's call of
// RwDevice_Initialize gets the host's answer instead (the RW driver globals reset as the original's first step,
// g_RwD3D9 = the stand-in below, no adapter, the device state "initialized", success), and the config manager's mode
// enumeration is skipped; d3d9.dll is still loaded (a static import of the exe) but never called.
//
// StandIn is an object with IDirect3D9's 17 methods (x86 COM: stdcall, `this` first) that answers every call as
// D3D9 does without a usable adapter (counts 0, HRESULTs D3DERR_NOTAVAILABLE, no monitor, no device) and counts and
// reports each call. No code a core-only host runs calls it; if something does, the host logs it instead of
// crashing on a null pointer.
#pragma once

#include <intrin.h>  // _ReturnAddress

#include <cstdint>

namespace gms::d3d9 {

// D3DERR_NOTAVAILABLE (MAKE_D3DHRESULT(2154)) and E_NOINTERFACE.
constexpr std::uint32_t kErrNotAvailable = 0x8876086Au;
constexpr std::uint32_t kErrNoInterface = 0x80004002u;

// IDirect3D9's vtable slots (d3d9.h), in order.
enum Slot : int {
  kQueryInterface,
  kAddRef,
  kRelease,
  kRegisterSoftwareDevice,
  kGetAdapterCount,
  kGetAdapterIdentifier,
  kGetAdapterModeCount,
  kEnumAdapterModes,
  kGetAdapterDisplayMode,
  kCheckDeviceType,
  kCheckDeviceFormat,
  kCheckDeviceMultiSampleType,
  kCheckDepthStencilMatch,
  kCheckDeviceFormatConversion,
  kGetDeviceCaps,
  kGetAdapterMonitor,
  kCreateDevice,
  kSlotCount
};

inline const char* SlotName(int slot) {
  static const char* const kNames[kSlotCount] = {"QueryInterface",
                                                 "AddRef",
                                                 "Release",
                                                 "RegisterSoftwareDevice",
                                                 "GetAdapterCount",
                                                 "GetAdapterIdentifier",
                                                 "GetAdapterModeCount",
                                                 "EnumAdapterModes",
                                                 "GetAdapterDisplayMode",
                                                 "CheckDeviceType",
                                                 "CheckDeviceFormat",
                                                 "CheckDeviceMultiSampleType",
                                                 "CheckDepthStencilMatch",
                                                 "CheckDeviceFormatConversion",
                                                 "GetDeviceCaps",
                                                 "GetAdapterMonitor",
                                                 "CreateDevice"};
  return slot >= 0 && slot < kSlotCount ? kNames[slot] : "?";
}

struct StandIn;
// Called on every method call (slot, the caller's return address); may be null.
using CallFn = void (*)(const StandIn& self, int slot, void* returnAddress);

// The object: its first member is the vtable pointer, as a COM object's. Static lifetime (Release never frees it).
struct StandIn {
  const void* const* vtbl;
  long refs;
  long calls[kSlotCount];
  CallFn onCall;

  long Total() const {
    long n = 0;
    for (long c : calls) n += c;
    return n;
  }
};

namespace detail {

using U = std::uintptr_t;  // every argument is a 32-bit value on x86 (UINT, D3DFORMAT, DWORD, BOOL, pointers)

inline void Note(StandIn* self, int slot, void* ret) {
  ++self->calls[slot];
  if (self->onCall) self->onCall(*self, slot, ret);
}
inline void Clear(U out) {
  if (out) *reinterpret_cast<void**>(out) = nullptr;
}

inline std::uint32_t __stdcall QueryInterface(StandIn* s, U, U out) {
  Note(s, kQueryInterface, _ReturnAddress());
  Clear(out);
  return kErrNoInterface;
}
inline std::uint32_t __stdcall AddRef(StandIn* s) {
  Note(s, kAddRef, _ReturnAddress());
  return static_cast<std::uint32_t>(++s->refs);
}
inline std::uint32_t __stdcall Release(StandIn* s) {
  Note(s, kRelease, _ReturnAddress());
  if (s->refs > 1) --s->refs;  // never below 1: the object is static
  return static_cast<std::uint32_t>(s->refs);
}
inline std::uint32_t __stdcall RegisterSoftwareDevice(StandIn* s, U) {
  Note(s, kRegisterSoftwareDevice, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall GetAdapterCount(StandIn* s) {
  Note(s, kGetAdapterCount, _ReturnAddress());
  return 0;
}
inline std::uint32_t __stdcall GetAdapterIdentifier(StandIn* s, U, U, U) {
  Note(s, kGetAdapterIdentifier, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall GetAdapterModeCount(StandIn* s, U, U) {
  Note(s, kGetAdapterModeCount, _ReturnAddress());
  return 0;
}
inline std::uint32_t __stdcall EnumAdapterModes(StandIn* s, U, U, U, U) {
  Note(s, kEnumAdapterModes, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall GetAdapterDisplayMode(StandIn* s, U, U) {
  Note(s, kGetAdapterDisplayMode, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall CheckDeviceType(StandIn* s, U, U, U, U, U) {
  Note(s, kCheckDeviceType, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall CheckDeviceFormat(StandIn* s, U, U, U, U, U, U) {
  Note(s, kCheckDeviceFormat, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall CheckDeviceMultiSampleType(StandIn* s, U, U, U, U, U, U out) {
  Note(s, kCheckDeviceMultiSampleType, _ReturnAddress());
  if (out) *reinterpret_cast<std::uint32_t*>(out) = 0;  // quality levels
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall CheckDepthStencilMatch(StandIn* s, U, U, U, U, U) {
  Note(s, kCheckDepthStencilMatch, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall CheckDeviceFormatConversion(StandIn* s, U, U, U, U) {
  Note(s, kCheckDeviceFormatConversion, _ReturnAddress());
  return kErrNotAvailable;
}
inline std::uint32_t __stdcall GetDeviceCaps(StandIn* s, U, U, U) {
  Note(s, kGetDeviceCaps, _ReturnAddress());
  return kErrNotAvailable;
}
inline void* __stdcall GetAdapterMonitor(StandIn* s, U) {
  Note(s, kGetAdapterMonitor, _ReturnAddress());
  return nullptr;
}
inline std::uint32_t __stdcall CreateDevice(StandIn* s, U, U, U, U, U, U out) {
  Note(s, kCreateDevice, _ReturnAddress());
  Clear(out);
  return kErrNotAvailable;
}

inline const void* const* Vtable() {
  static const void* const kVtbl[kSlotCount] = {
      reinterpret_cast<const void*>(&QueryInterface),
      reinterpret_cast<const void*>(&AddRef),
      reinterpret_cast<const void*>(&Release),
      reinterpret_cast<const void*>(&RegisterSoftwareDevice),
      reinterpret_cast<const void*>(&GetAdapterCount),
      reinterpret_cast<const void*>(&GetAdapterIdentifier),
      reinterpret_cast<const void*>(&GetAdapterModeCount),
      reinterpret_cast<const void*>(&EnumAdapterModes),
      reinterpret_cast<const void*>(&GetAdapterDisplayMode),
      reinterpret_cast<const void*>(&CheckDeviceType),
      reinterpret_cast<const void*>(&CheckDeviceFormat),
      reinterpret_cast<const void*>(&CheckDeviceMultiSampleType),
      reinterpret_cast<const void*>(&CheckDepthStencilMatch),
      reinterpret_cast<const void*>(&CheckDeviceFormatConversion),
      reinterpret_cast<const void*>(&GetDeviceCaps),
      reinterpret_cast<const void*>(&GetAdapterMonitor),
      reinterpret_cast<const void*>(&CreateDevice)};
  return kVtbl;
}

}  // namespace detail

// Makes `s` a fresh stand-in (one reference, no calls).
inline void Init(StandIn& s, CallFn onCall = nullptr) {
  s.vtbl = detail::Vtable();
  s.refs = 1;
  for (long& c : s.calls) c = 0;
  s.onCall = onCall;
}

// What the host does about D3D9 at start-up, from the [host] keys.
struct Plan {
  bool answerRwDevice = false;     // RwDevice_Initialize's call answered by the host (no Direct3DCreate9, the stand-in)
  bool skipResolutionList = false;  // the config manager's adapter-mode enumeration skipped
  bool failCreate = false;          // test aid: Direct3DCreate9 answers NULL (a machine without D3D9)
};

// coreOnly: [host] core_only (0 stage 1, 1 before the D3D9 device, 2 before SP_Graphics); d3d9: [host] d3d9 (0 = none
// in a core-only host, the default; 1 = the game's own RwDevice_Initialize, as before H1d); testNoD3d9: [host]
// test_no_d3d9. Only core_only = 1 can do without D3D9: stage 1 and core_only = 2 create the device.
inline Plan Decide(int coreOnly, int d3d9, bool testNoD3d9) {
  Plan p;
  p.answerRwDevice = coreOnly == 1 && d3d9 == 0;
  p.skipResolutionList = p.answerRwDevice;
  p.failCreate = testNoD3d9;
  return p;
}

// The start-up line's verdict on d3d9.dll: "not touched" when nothing called Direct3DCreate9 (the import the exe
// has) and nothing called the stand-in; a stand-in call means some path reached for the D3D9 object (look at the log).
inline const char* Verdict(long createCalls, long standInCalls) {
  if (createCalls != 0) return "TOUCHED";
  return standInCalls == 0 ? "not touched" : "not touched, but the stand-in was called";
}

}  // namespace gms::d3d9
