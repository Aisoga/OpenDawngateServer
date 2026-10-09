// The headless host's start-up, stage 2 (H1-live): see startup.h. Addresses and their evidence: game.h, namespace
// startup.
#include "startup.h"

#include <windows.h>
#include <mmsystem.h>  // timeBeginPeriod (winmm)

#include <float.h>   // _controlfp_s
#include <intrin.h>  // _ReturnAddress

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <cwchar>

#include "game.h"
#include "guards.h"
#include "hook.h"
#include "log.h"

using namespace game;
using namespace game::startup;

namespace startup {

namespace {

Options g_opt;
MarkFn g_mark;

// ---------------------------------------------------------------------------------------------
// generated code: one RWX page holds the probes' stubs and the core-only stub

unsigned char* g_code;
std::size_t g_codeUsed;
constexpr std::size_t kCodeSize = 8192;

struct Emitter {
  unsigned char* p;
  void B(unsigned v) { *p++ = static_cast<unsigned char>(v); }
  void D(std::uint32_t v) {
    std::memcpy(p, &v, 4);
    p += 4;
  }
  // a rel32 operand: target - the next instruction (the operand is the last 4 bytes of the instruction)
  void Rel(std::uintptr_t target) { D(static_cast<std::uint32_t>(target - (reinterpret_cast<std::uintptr_t>(p) + 4))); }
};

unsigned char* CodeAlloc(std::size_t n) {
  if (!g_code) {
    g_code = static_cast<unsigned char*>(VirtualAlloc(nullptr, kCodeSize, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE));
    if (!g_code) return nullptr;
    std::memset(g_code, 0xCC, kCodeSize);
  }
  if (g_codeUsed + n > kCodeSize) return nullptr;
  unsigned char* p = g_code + g_codeUsed;
  g_codeUsed += (n + 15) & ~static_cast<std::size_t>(15);
  return p;
}

// Writes `call target` at `site` and NOPs up to `len` bytes.
void PatchCall(std::uintptr_t site, std::size_t len, const void* target) {
  auto* p = reinterpret_cast<unsigned char*>(site);
  DWORD old;
  VirtualProtect(p, len, PAGE_EXECUTE_READWRITE, &old);
  p[0] = 0xE8;
  const auto rel = static_cast<std::int32_t>(reinterpret_cast<std::uintptr_t>(target) - (site + 5));
  std::memcpy(p + 1, &rel, 4);
  for (std::size_t i = 5; i < len; ++i) p[i] = 0x90;
  VirtualProtect(p, len, old, &old);
  FlushInstructionCache(GetCurrentProcess(), p, len);
}

// ---------------------------------------------------------------------------------------------
// step 0: call-site probes

constexpr int kProbeCount = static_cast<int>(sizeof kProbeSites / sizeof kProbeSites[0]);
DWORD g_probeReturn[kProbeCount];  // each stub keeps its caller's return address here (main thread, no recursion)
int g_probes, g_probeMarks;

void __cdecl ProbeMark(int index, int after) {
  ++g_probeMarks;
  if (!g_mark || index < 0 || index >= kProbeCount) return;
  char what[160];
  std::snprintf(what, sizeof what, "%s %s", after ? "after" : "before", kProbeSites[index].what);
  g_mark(what);
}

// pushad; pushfd; save the FPU (a callee may return in ST0); ProbeMark(index, after); restore all of it
void EmitMark(Emitter& e, int index, int after) {
  e.B(0x60);                                                // pushad
  e.B(0x9C);                                                // pushfd
  e.B(0x81), e.B(0xEC), e.D(108);                           // sub esp, 108
  e.B(0xDD), e.B(0x34), e.B(0x24);                          // fnsave [esp]
  e.B(0x6A), e.B(static_cast<unsigned>(after));             // push after
  e.B(0x6A), e.B(static_cast<unsigned>(index));             // push index
  e.B(0xE8), e.Rel(reinterpret_cast<std::uintptr_t>(&ProbeMark));  // call ProbeMark
  e.B(0x83), e.B(0xC4), e.B(0x08);                          // add esp, 8
  e.B(0xDD), e.B(0x24), e.B(0x24);                          // frstor [esp]
  e.B(0x81), e.B(0xC4), e.D(108);                           // add esp, 108
  e.B(0x9D);                                                // popfd
  e.B(0x61);                                                // popad
}

bool InstallProbe(int i) {
  const ProbeSite& s = kProbeSites[i];
  const auto* site = reinterpret_cast<const unsigned char*>(s.site);
  std::size_t len;
  if (s.kind == 0) {
    std::int32_t rel;
    std::memcpy(&rel, site + 1, 4);
    if (site[0] != 0xE8 || s.site + 5 + static_cast<std::uintptr_t>(rel) != s.callee) {
      Log("startup: probe %08X (%s): not a call to %08X; not installed", static_cast<unsigned>(s.site), s.what,
          static_cast<unsigned>(s.callee));
      return false;
    }
    len = 5;
  } else {
    if (std::memcmp(site, s.prefix, s.prefixLen) != 0 || site[s.prefixLen] != 0xFF || site[s.prefixLen + 1] != 0xD0) {
      Log("startup: probe %08X (%s): bytes differ; not installed", static_cast<unsigned>(s.site), s.what);
      return false;
    }
    len = s.prefixLen + 2u;
  }
  unsigned char* stub = CodeAlloc(128);
  if (!stub) return false;
  Emitter e{stub};
  e.B(0x8F), e.B(0x05), e.D(static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(&g_probeReturn[i])));  // pop [ret]
  for (unsigned k = 0; k < s.prefixLen; ++k) e.B(s.prefix[k]);  // kind 1: the replaced instructions before call eax
  EmitMark(e, i, 0);
  if (s.kind == 0) {
    e.B(0xE8), e.Rel(s.callee);  // call callee
  } else {
    e.B(0xFF), e.B(0xD0);  // call eax
  }
  EmitMark(e, i, 1);
  e.B(0xFF), e.B(0x35), e.D(static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(&g_probeReturn[i])));  // push [ret]
  e.B(0xC3);                                                                                                    // ret
  PatchCall(s.site, len, stub);
  return true;
}

// ---------------------------------------------------------------------------------------------
// step 1: the shipped switches -nobake and -noSound

using FindSwitchFn = int(__fastcall*)(void* cmdline, void* edx, const wchar_t* name, int flag, void* value, int start);
FindSwitchFn g_findSwitch;
volatile LONG g_switchAnswers;

int __fastcall FindSwitchHook(void* cmdline, void* edx, const wchar_t* name, int flag, void* value, int start) {
  const int r = g_findSwitch(cmdline, edx, name, flag, value, start);
  if (r != -1 || !name) return r;
  const bool bake = g_opt.nobake && _wcsicmp(name, L"nobake") == 0;
  const bool sound = g_opt.nosound && _wcsicmp(name, L"noSound") == 0;
  if (!bake && !sound) return r;
  if (InterlockedIncrement(&g_switchAnswers) <= 10) Log("startup: CommandLine_FindSwitch(\"%ls\") answered as given ([host] %s)", name,
                                                        bake ? "nobake" : "nosound");
  return 0;
}

// ---------------------------------------------------------------------------------------------
// step 2: the preloader's directory filter

using LoadDirectoryFn = bool(__fastcall*)(void* preloader, void* edx, const char* dir);
int g_preloadSkipped, g_preloadLoaded;

// "assetdata/binary", "levels/" and "Levels\\" alike
bool SameDir(const char* dir, const char* want) {
  std::size_t n = std::strlen(want);
  for (std::size_t i = 0; i < n; ++i) {
    char c = dir[i];
    if (c == '\\') c = '/';
    if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
    if (c != want[i]) return false;
  }
  return dir[n] == 0 || ((dir[n] == '/' || dir[n] == '\\') && dir[n + 1] == 0);
}

bool __fastcall PreloadFilter(void* preloader, void* edx, const char* dir) {
  const char* d = dir ? dir : "";
  const bool server = SameDir(d, "assetdata/binary") || SameDir(d, "levels");
  if (g_opt.preload == 2 && server) {
    ++g_preloadLoaded;
    Log("startup: preload %s (kept: the server reads it, [host] preload = server)", d);
    return At<LoadDirectoryFn>(kPreloaderLoadDirectory)(preloader, edx, dir);
  }
  ++g_preloadSkipped;
  Log("startup: preload %s skipped ([host] preload = %d)", d, g_opt.preload);
  return true;
}

// ---------------------------------------------------------------------------------------------
// step 0 (with the marks): does anything create a D3D9 object? (S3: a core-only host must not)

using Direct3DCreate9Fn = void*(WINAPI*)(UINT sdk);
Direct3DCreate9Fn g_d3dCreate;
volatile LONG g_d3dCreates;

void* WINAPI Direct3DCreate9Hook(UINT sdk) {
  InterlockedIncrement(&g_d3dCreates);
  Log("startup: Direct3DCreate9(%u) called (returning to %08lX)", sdk,
      static_cast<unsigned long>(reinterpret_cast<std::uintptr_t>(_ReturnAddress())));
  if (g_mark) g_mark("Direct3DCreate9");
  return g_d3dCreate(sdk);
}

// ---------------------------------------------------------------------------------------------
// step 3: core only

bool g_coreOnly, g_coreRan, g_coreOk;
int g_coreFailures;
bool g_loaderOff, g_dispatchOff, g_clockOff, g_navOff, g_listOff;
unsigned g_coreFrames;
float g_frameDt;  // the frame's dt in seconds, for the Gears pieces

// AssetCatalog_Initialize's settings, as Battlegrounds_AppInit builds them on its stack (game.h). Kept for the
// process's lifetime: the game destroys its copy at the end of Battlegrounds_AppInit, the catalog keeps its own.
struct CatalogSettings {
  std::uint16_t flags;
  std::uint16_t pad;
  std::uintptr_t begin, end, capacity;  // eastl::wstring (the data folder)
  std::uint32_t rest[4];
};
CatalogSettings g_catalog;

using VoidFn = void(__cdecl*)();
using IntArgFn = void(__cdecl*)(int);
using FloatArgFn = void(__cdecl*)(float);

void CoreTypes() {
  for (std::uintptr_t f : kAppInitTypeSteps) At<VoidFn>(f)();
}
void CoreCatalog() {
  std::memset(&g_catalog, 0, sizeof g_catalog);
  g_catalog.flags = 0x0101;
  g_catalog.begin = g_catalog.end = kEmptyWString;
  g_catalog.capacity = kEmptyWString + 2;
  const wchar_t* dir = At<const wchar_t*(__cdecl*)()>(kDataFolder)();
  const wchar_t* end = dir + std::wcslen(dir);
  At<void(__thiscall*)(void*, const wchar_t*, const wchar_t*)>(kWStringAssign)(&g_catalog.begin, dir, end);
  Log("startup: core: AssetCatalog_Initialize, data folder %ls", dir);
  At<void(__cdecl*)(void*)>(kAssetCatalogInitialize)(&g_catalog);
}
void CoreMaster() { At<IntArgFn>(kInitializeMasterGameSimulator)(1); }
void CoreGlobals() { At<IntArgFn>(kLabsGlobalDataInitialize)(0); }
void CoreNavPower() { At<IntArgFn>(kNavPowerInitializeSystem)(1); }
void CoreGmsMessages() {
  const char* name = reinterpret_cast<const char*>(kSporeNetAllocName);
  void* ids = Alloc(kTransportIdAllocatorSize, name, 0, 0, 0, 0);
  At<void*(__thiscall*)(void*)>(kTransportIdAllocatorConstruct)(ids);
  void* platform = Alloc(4, name, 0, 0, 0, 0);
  At<void*(__thiscall*)(void*)>(kSnPlatformConstruct)(platform);
  At<void(__cdecl*)(void*)>(kSnTransportSetPlatform)(platform);
  void* factory = At<void*(__cdecl*)()>(kGetMessageFactory)();
  At<void(__thiscall*)(void*, void*)>(kRegisterGmsMessages)(factory, ids);
  Log("startup: core: GMS message types registered (factory %p, transport-id allocator %p)", factory, ids);
}

DWORD CoreFilter(EXCEPTION_POINTERS* ep, const char* what) {
  const EXCEPTION_RECORD* er = ep->ExceptionRecord;
  Log("startup: EXCEPTION in core step %s: code %08lX at %08lX", what, er->ExceptionCode,
      static_cast<unsigned long>(reinterpret_cast<std::uintptr_t>(er->ExceptionAddress)));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool Guard(const char* what, VoidFn fn) {
  __try {
    fn();
    return true;
  } __except (CoreFilter(GetExceptionInformation(), what)) {
    return false;
  }
}

void CoreStep(const char* what, VoidFn fn) {
  char m[120];
  std::snprintf(m, sizeof m, "before core %s", what);
  if (g_mark) g_mark(m);
  if (!Guard(what, fn)) ++g_coreFailures;
  std::snprintf(m, sizeof m, "after core %s", what);
  if (g_mark) g_mark(m);
}

// Called by the core-only stub inside SporeApp_Initialize (main thread, before the first frame).
void __cdecl CoreInit() {
  g_coreRan = true;
  Log("startup: core only: SporeApp_Initialize skips %08X-0x006B2D93 (%s); the simulation core instead",
      static_cast<unsigned>(g_opt.coreOnly == 2 ? kCoreJumpFromGraphics : kCoreJumpFromDevice),
      g_opt.coreOnly == 2 ? "SP_Graphics, Wwise, the UI, AppInit" : "the D3D9 device, graphics, Wwise, the UI, AppInit");
  // The client runs the x87 FPU at single precision and round-to-nearest (D3D9's CreateDevice sets it, and
  // AppSystem_Update asserts "GetFPUPrecision() == kFPUPrecisionSingle"); without a device a host would compute the
  // simulation's x87 floats at another precision than the clients and stage 1.
  unsigned int cw = 0;
  _controlfp_s(&cw, _PC_24 | _RC_NEAR, _MCW_PC | _MCW_RC);
  Log("startup: core: x87 control word: single precision, round to nearest (as with a D3D9 device); now %08X", cw);
  CoreStep("types (string_cow, game clock, asset types, CollectionData)", &CoreTypes);
  CoreStep("AssetCatalog_Initialize", &CoreCatalog);
  CoreStep("InitializeMasterGameSimulator(1)", &CoreMaster);
  CoreStep("LabsGlobalData_Initialize(0)", &CoreGlobals);
  CoreStep("NavPower_InitializeSystem(1)", &CoreNavPower);
  CoreStep("GMS message registration", &CoreGmsMessages);
  timeBeginPeriod(1);  // the renderer's start-up does this in a full client; Sleep in the host's loop needs it
  g_coreOk = g_coreFailures == 0;
  Log("startup: core only: %s; SporeApp_Initialize continues at its success exit 0x006B2D93 (SP_App +0xC stays 0)",
      g_coreOk ? "every step ran" : "SOME STEPS FAILED (above)");
}

bool InstallCoreOnly(int point) {
  unsigned char* stub = CodeAlloc(32);
  if (!stub) return false;
  Emitter e{stub};
  e.B(0x60);                                                     // pushad
  e.B(0x9C);                                                     // pushfd
  e.B(0xE8), e.Rel(reinterpret_cast<std::uintptr_t>(&CoreInit));  // call CoreInit
  e.B(0x9D);                                                     // popfd
  e.B(0x61);                                                     // popad
  e.B(0xE9), e.Rel(kCoreJumpTo);                                 // jmp 0x006B2D93
  if (point == 2)
    return HookReplace(kCoreJumpFromGraphics, kCoreJumpFromGraphicsBytes, sizeof kCoreJumpFromGraphicsBytes, stub);
  return HookReplace(kCoreJumpFromDevice, kCoreJumpFromDeviceBytes, sizeof kCoreJumpFromDeviceBytes, stub);
}

void LoaderUpdate() { At<VoidFn>(kLoaderUpdate)(); }
void Dispatch() {
  void* mm = At<void*(__cdecl*)()>(kGetMessageManager)();
  if (!mm) return;
  auto fn = reinterpret_cast<void(__thiscall*)(void*)>((*static_cast<std::uintptr_t**>(mm))[kMessageManagerDispatchSlot / 4]);
  fn(mm);
}
void ClockAdvance() { At<VoidFn>(kGameClockAdvance)(); }
void NavPowerFrame() { At<FloatArgFn>(kNavPowerFrame)(g_frameDt); }
void UpdateListFrame() { At<FloatArgFn>(kUpdateListFrame)(g_frameDt); }

// H5: a piece runs as guards.h's step (guards::kFrameLoader ... kFrameUpdateList): an exception skips that frame's run
// only (before H5 the piece was off for good after one, e.g. a stopped game clock); `off` follows the harness's
// switch-off after step_fail_s of failures (a host ends the match instead)
void FramePiece(int step, VoidFn fn, bool& off) {
  if (off) return;
  if (!guards::Run(step, fn) && guards::Disabled(step)) {
    off = true;
    Log("startup: core frame: %s is off (it threw in every frame for step_fail_s)", guards::Name(step));
  }
}

}  // namespace

void Install(const Options& o, MarkFn mark) {
  g_opt = o;
  g_mark = mark;
  Log("startup (H1-live): startup_marks %d, nobake %d, nosound %d, preload %d, core_only %d", o.marks, o.nobake, o.nosound,
      o.preload, o.coreOnly);
  if (o.marks) {
    for (int i = 0; i < kProbeCount; ++i)
      if (InstallProbe(i)) ++g_probes;
    g_d3dCreate = reinterpret_cast<Direct3DCreate9Fn>(
        HookIat(kD3d9Dll, "Direct3DCreate9", reinterpret_cast<void*>(&Direct3DCreate9Hook)));
    Log("startup: %d of %d start-up probes installed; Direct3DCreate9 watch %s", g_probes, kProbeCount,
        g_d3dCreate ? "installed" : "NOT installed (import not found)");
  }
  if (o.nobake || o.nosound) {
    g_findSwitch = reinterpret_cast<FindSwitchFn>(
        HookDetour(kFindSwitch, kFindSwitchBytes, sizeof kFindSwitchBytes, reinterpret_cast<void*>(&FindSwitchHook)));
    Log("startup: CommandLine_FindSwitch detour %s (%s%s)", g_findSwitch ? "installed" : "FAILED", o.nobake ? "-nobake " : "",
        o.nosound ? "-noSound" : "");
  }
  if (o.preload != 1) {
    auto* p = reinterpret_cast<unsigned char*>(kPreloadCallbackPush);
    if (std::memcmp(p, kPreloadCallbackPushBytes, sizeof kPreloadCallbackPushBytes) == 0) {
      DWORD old;
      VirtualProtect(p, 5, PAGE_EXECUTE_READWRITE, &old);
      const auto filter = static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(&PreloadFilter));
      std::memcpy(p + 1, &filter, 4);
      VirtualProtect(p, 5, old, &old);
      FlushInstructionCache(GetCurrentProcess(), p, 5);
      Log("startup: preloader filter installed (preload %d: %s)", o.preload,
          o.preload == 2 ? "only assetdata/binary and levels" : "no directory");
    } else {
      Log("startup: preloader callback push at %08X differs; preload filter NOT installed",
          static_cast<unsigned>(kPreloadCallbackPush));
    }
  }
  if (o.coreOnly) {
    g_coreOnly = InstallCoreOnly(o.coreOnly);
    Log("startup: core-only jump %s (%08X -> core init -> 0x006B2D93)", g_coreOnly ? "installed" : "FAILED",
        static_cast<unsigned>(o.coreOnly == 2 ? kCoreJumpFromGraphics : kCoreJumpFromDevice));
  }
}

bool CoreOnly() { return g_coreOnly; }

bool CoreInitOk() { return g_coreRan && g_coreOk; }

void CoreFrame(float dtSeconds) {
  ++g_coreFrames;
  g_frameDt = dtSeconds;
  FramePiece(guards::kFrameLoader, &LoaderUpdate, g_loaderOff);       // the loader's update
  FramePiece(guards::kFrameDispatch, &Dispatch, g_dispatchOff);       // the message dispatch
  FramePiece(guards::kFrameClock, &ClockAdvance, g_clockOff);         // the game clock
  FramePiece(guards::kFrameNavPower, &NavPowerFrame, g_navOff);       // NavPower's frame
  FramePiece(guards::kFrameUpdateList, &UpdateListFrame, g_listOff);  // the update list
}

void Describe(char* out, unsigned size) {
  std::snprintf(out, size,
                "probes %d (%d marks, Direct3DCreate9 %ld), switches %s%s(answered %ld), preload %d (%d skipped, %d kept), "
                "core only %s (frames %u%s%s%s%s%s)",
                g_probes, g_probeMarks, g_d3dCreates, g_opt.nobake ? "nobake " : "", g_opt.nosound ? "nosound " : "",
                g_switchAnswers, g_opt.preload, g_preloadSkipped, g_preloadLoaded,
                !g_coreOnly ? "off" : (!g_coreRan ? "installed, not run" : (g_coreOk ? "ok" : "FAILED")), g_coreFrames,
                g_loaderOff ? ", loader off" : "", g_dispatchOff ? ", dispatch off" : "", g_clockOff ? ", clock off" : "",
                g_navOff ? ", navpower off" : "", g_listOff ? ", update list off" : "");
}

}  // namespace startup
