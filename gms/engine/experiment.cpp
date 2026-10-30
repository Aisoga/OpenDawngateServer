// D1 server-mode experiment (docs/SERVER_MODE.md). Runs on the game's primary thread, from the
// PeekMessage IAT hook:
//   phase 0  wait until the game is idle (login screen) and its globals exist
//   phase 1  create a cGameSimulator like InitializeClientGameSimulator does, but with the base
//            cGameObjectManager, mbClientInstance (+0x17) = 0 and component mode (+0x550) = 1, then Init
//   phase 2  activate the AI director, set and start the level (as Client_LoadLevel does), then run
//            <game dir>\dgsim_start.lua on the server simulator
//   phase 3  tick it with the pieces of the client tick plus the agent step (D1b-live, agents.h)
//            every tick_ms for run_ms, with stats and dgsim_probe.lua every probe_ms
// Every step runs under SEH; an exception is logged (address, registers, return addresses on the
// stack) and stops that step, the game keeps running. Since H5 the tick parts and the rest of the frame run through
// guards.h: an exception skips only that run of the step (before, a failing tick part was disabled for good), and a
// step that throws in every run for [host] step_fail_s ends the match; host mode also starts the watchdog
// (host/watchdog.h), which ends a host whose tick has stalled for [host] watchdog_s. Hooks log
// asserts, Lua runtime errors and ability request results raised while our simulator is current,
// and narrow the client's wildcard socket binds to 127.0.0.1. Settings: <game dir>\dgsim.ini [dgsim].
// In host mode (D1d, host.h) the same phases run right after the engine is up ([host] start_ms, default 0) and tick
// until the process ends ([host] run_ms, default 0); host.cpp keeps the front end in kGameStateBoot.
// Since G2 the host serves the GMS port itself (gms_host.h): the endpoint is bound once the level is loaded, and
// every server tick polls it first (ADR-2's loop: network, then the simulation). The Lua probes (dgsim_start.lua,
// dgsim_probe.lua: two test minions and a scripted tower attack) are off in host mode unless [host] lua_probes = 1.
// Since C1b a host that dgserver started (DGGMS_CONTROL) speaks the control channel (control.h): hello at the first
// frame, the level load waits for createGame (its level, seed and game id), gameReady once the GMS port is bound, and
// the channel is polled every frame (the heartbeat, abortGame, a lost connection: the host exits).
#include "experiment.h"

#include <windows.h>

#include <intrin.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

#include "abilities.h"
#include "agents.h"
#include "banter.h"
#include "bots.h"
#include "bots2.h"
#include "components.h"
#include "control/messages.h"
#include "volumes.h"
#include "effects.h"
#include "crowd.h"
#include "damage.h"
#include "death.h"
#include "game.h"
#include "gms_host.h"
#include "guards.h"
#include "hook.h"
#include "host/control.h"
#include "host/headless.h"
#include "host/host.h"
#include "host/watchdog.h"
#include "hunterroute.h"
#include "itempassives.h"
#include "log.h"
#include "mapfix.h"
#include "match.h"
#include "matchstats_sources.h"
#include "passives.h"
#include "matchrec.h"
#include "perf.h"
#include "regen.h"
#include "shop.h"

using namespace game;

namespace {

struct Config {
  int startMs = 20000;  // after the first main-thread frame
  int runMs = 60000;    // how long phase 3 ticks
  int tickMs = 33;
  unsigned flags = 0;  // level flags: a markerset loads only if all its condition bits are set (SM_Map_v5 has none)
  unsigned seed = 12345;
  char level[96] = "SM_Map_v5.Level";
  int stopAfter = 3;     // last phase to run (1 create, 2 level, 3 tick)
  int setRunning = 1;    // set sim+0x14 if the level start leaves it 0 (the client tick needs it)
  int logAllAsserts = 0;
  int statsMs = 2000;
  int histoMs = 10000;
  int componentMode = 1;  // sim+0x550; the client uses 0
  int directorActive = 1;  // set cAIDirector+0 before the level loads (nothing in the client does)
  int probeMs = 5000;      // dgsim_probe.lua interval while ticking
  int directAbility = 0;   // activate dgsim_start.lua's tower attack directly (0x0090A390)
  int tickAgents = 1;      // D1b-live: the agent step (agents.h) each server tick; host mode: [host] agents
  int luaProbes = 1;       // run dgsim_start.lua / dgsim_probe.lua (host mode: [host] lua_probes, default 0)
  int assertLogLimit = 3;  // D12: assert lines per file:line before only every 1000th ([dgsim]/[host] assert_log_limit)
  int requestLogLimit = 200;  // D12: ability request/validate lines ([dgsim]/[host] ability_request_log_limit)
};

Config g_cfg;
char g_ini[MAX_PATH];
DWORD g_mainThread;
void* g_sim;
void* g_prevSim;
int g_phase;  // 0 wait, 1 created, 2 level started, 3 ticking, 4 done, -1 failed
unsigned g_firstFrame, g_tickStart, g_lastStats, g_lastHisto, g_lastProbe;
// The tick schedule runs on QueryPerformanceCounter: GetTickCount (LogMs) moves in 15.6 ms steps, so "33 ms since the
// last tick" held only on every other 33 ms frame of a background window (the game's frame cap there is 30 fps,
// MainLoop_LimitFrameRate 0x006B1A50), and a hidden host ticked at 16 Hz (D1d, d1d_run1).
double g_nextTickMs;
double PreciseMs() {
  static LARGE_INTEGER freq;
  if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
  LARGE_INTEGER c;
  QueryPerformanceCounter(&c);
  return static_cast<double>(c.QuadPart) * 1000.0 / static_cast<double>(freq.QuadPart);
}
unsigned g_ticks;
bool g_inFrame;
unsigned g_otherThreadPeeks;

// ---------------------------------------------------------------------------------------------
// SEH helpers

bool ReadDword(const void* p, DWORD* out) {
  __try {
    *out = *static_cast<const volatile DWORD*>(p);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

bool LooksLikeReturnAddress(DWORD v) {
  if (v < 0x00401005 || v >= 0x011AB0FB) return false;  // Dawngate.exe .text
  DWORD a, b;
  if (!ReadDword(reinterpret_cast<void*>(v - 8), &a) || !ReadDword(reinterpret_cast<void*>(v - 4), &b)) return false;
  const unsigned char* q = reinterpret_cast<const unsigned char*>(v);
  return q[-5] == 0xE8 || q[-6] == 0xFF || q[-3] == 0xFF || q[-2] == 0xFF;
}

DWORD Filter(EXCEPTION_POINTERS* ep, const char* what) {
  const EXCEPTION_RECORD* er = ep->ExceptionRecord;
  const CONTEXT* c = ep->ContextRecord;
  Log("EXCEPTION in %s: code %08lX at %08lX", what, er->ExceptionCode, reinterpret_cast<DWORD>(er->ExceptionAddress));
  if (er->ExceptionCode == EXCEPTION_ACCESS_VIOLATION && er->NumberParameters >= 2)
    Log("  access violation: %s of %08lX", er->ExceptionInformation[0] ? "write" : "read", static_cast<DWORD>(er->ExceptionInformation[1]));
  Log("  eax %08lX ebx %08lX ecx %08lX edx %08lX esi %08lX edi %08lX ebp %08lX esp %08lX", c->Eax, c->Ebx, c->Ecx,
      c->Edx, c->Esi, c->Edi, c->Ebp, c->Esp);
  char line[600];
  int n = 0, len = 0;
  line[0] = 0;
  for (int i = 0; i < 512 && n < 24; ++i) {
    DWORD v;
    if (!ReadDword(reinterpret_cast<DWORD*>(c->Esp) + i, &v)) break;
    if (LooksLikeReturnAddress(v)) {
      len += std::snprintf(line + len, sizeof line - len, " %08lX", v);
      ++n;
    }
  }
  Log("  return addresses on the stack:%s", line);
  return EXCEPTION_EXECUTE_HANDLER;
}

using StepFn = void (*)();

bool Guarded(const char* what, StepFn fn) {
  __try {
    fn();
    return true;
  } __except (Filter(GetExceptionInformation(), what)) {
    return false;
  }
}

// Runs fn with our simulator bound to this thread (TLS slot +0x34), then restores the previous one.
bool WithServerSim(const char* what, StepFn fn) {
  g_prevSim = GetCurrentSim();
  SetCurrentSim(g_sim);
  bool ok = Guarded(what, fn);
  SetCurrentSim(g_prevSim);
  return ok;
}

// ---------------------------------------------------------------------------------------------
// asserts

struct AssertSlot {
  const char* file;
  int line;
  unsigned count;
};
AssertSlot g_asserts[2048];
int g_assertSlots;
unsigned g_assertsServer, g_assertsOther;

using AssertFn = void(__cdecl*)(int, const char*, const char*, int, int, const char*);
AssertFn g_origAssert;

const char* SafeStr(const char* s) {
  DWORD d;
  return (s && ReadDword(s, &d)) ? s : "";
}

void __cdecl AssertHook(int kind, const char* expr, const char* file, int line, int count, const char* msg) {
  DWORD ret = reinterpret_cast<DWORD>(_ReturnAddress());
  bool ours = g_sim && GetCurrentSim() == g_sim;
  if (ours) ++g_assertsServer; else ++g_assertsOther;
  if (ours || g_cfg.logAllAsserts) {
    AssertSlot* s = nullptr;
    for (int i = 0; i < g_assertSlots; ++i)
      if (g_asserts[i].file == file && g_asserts[i].line == line) { s = &g_asserts[i]; break; }
    if (!s && g_assertSlots < 2048) { s = &g_asserts[g_assertSlots++]; s->file = file; s->line = line; s->count = 0; }
    unsigned k = s ? ++s->count : 0;
    if (k <= static_cast<unsigned>(g_cfg.assertLogLimit) || k % 1000 == 0) {
      char ctx[400];  // D12: the script running (abilities.h AbilityLuaContext)
      AbilityLuaContext(ctx, sizeof ctx);
      Log("ASSERT %s #%u kind %d %s:%d (call at %08lX): %s | %s%s", ours ? "[server]" : "[other]", k, kind, SafeStr(file),
          line, ret - 5, SafeStr(expr), SafeStr(msg), ctx);
    }
  }
  g_origAssert(kind, expr, file, line, count, msg);
}

// Lua runtime errors of failed thread resumes, each distinct message logged once (then every 100th).
struct LuaErrSlot {
  unsigned hash;
  unsigned count;
};
LuaErrSlot g_luaErrs[1024];
int g_luaErrSlots;
unsigned g_luaErrsServer, g_luaErrsOther;
using LuaErrorFn = void(__cdecl*)(void* L);
LuaErrorFn g_origLuaError;

void LogLuaError(void* L, bool ours) {
  const char* msg = LuaToString(L, -1, nullptr);
  msg = SafeStr(msg);
  unsigned h = 2166136261u;
  for (const char* p = msg; *p; ++p) h = (h ^ static_cast<unsigned char>(*p)) * 16777619u;
  LuaErrSlot* s = nullptr;
  for (int i = 0; i < g_luaErrSlots; ++i)
    if (g_luaErrs[i].hash == h) { s = &g_luaErrs[i]; break; }
  if (!s && g_luaErrSlots < 1024) { s = &g_luaErrs[g_luaErrSlots++]; s->hash = h; s->count = 0; }
  unsigned k = s ? ++s->count : 0;
  if (k == 1 || k % 100 == 0) {
    char ctx[400];  // D12: the script running (abilities.h AbilityLuaContext)
    AbilityLuaContext(ctx, sizeof ctx);
    Log("LUA ERROR %s #%u: %s%s", ours ? "[server]" : "[other]", k, msg, ctx);
  }
}

void __cdecl LuaErrorHook(void* L) {
  bool ours = g_sim && GetCurrentSim() == g_sim;
  if (ours) ++g_luaErrsServer; else ++g_luaErrsOther;
  if (ours || g_cfg.logAllAsserts) {
    __try {
      LogLuaError(L, ours);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      Log("LUA ERROR: message unreadable");
    }
  }
  g_origLuaError(L);
}

// Ability requests made while our simulator is current: arguments and result codes.
RequestAbilityFn g_origRequestAbility;
ValidateAbilityFn g_origValidateAbility;
unsigned g_abilityLogs;

int __cdecl RequestAbilityHook(unsigned ability, int slot, unsigned agent, unsigned target, float x, float y, float z, int rank,
                               unsigned flags, unsigned snapshot, int b11, char b12, void* L, int* out) {
  int r = g_origRequestAbility(ability, slot, agent, target, x, y, z, rank, flags, snapshot, b11, b12, L, out);
  if (g_sim && GetCurrentSim() == g_sim && g_abilityLogs++ < static_cast<unsigned>(g_cfg.requestLogLimit))
    Log("ability request %08X agent %08X target %08X rank %d slot %d -> %d (out %d)", ability, agent, target, rank, slot, r,
        out ? *out : 0);
  return r;
}

int __cdecl ValidateAbilityHook(unsigned ability, int slot, unsigned agent, unsigned target, float x, float y, float z, int rank,
                                int b9) {
  int r = g_origValidateAbility(ability, slot, agent, target, x, y, z, rank, b9);
  if (g_sim && GetCurrentSim() == g_sim && g_abilityLogs++ < static_cast<unsigned>(g_cfg.requestLogLimit))
    Log("ability validate %08X agent %08X target %08X rank %d -> %d (%s0x%X)", ability, agent, target, rank, r, r < 0 ? "-" : "",
        r < 0 ? -r : r);
  return r;
}

// ---------------------------------------------------------------------------------------------
// diagnostics

const char* AssetName(void* asset) {
  DWORD p;
  if (!asset || !ReadDword(asset, &p) || !p) return "?";
  return SafeStr(reinterpret_cast<const char*>(p));
}

void LogSimFields(const char* when) {
  char* s = static_cast<char*>(g_sim);
  Log("sim %p %s: +14 %u +16 %u +17(client) %u objmgr %p aidirector %p botdirector %p replay %p pathfinder %p "
      "luathread %08X level %p outgoing %d objupdates %u",
      g_sim, when, Field<unsigned char>(s, sim::kRunning), Field<unsigned char>(s, sim::kState16),
      Field<unsigned char>(s, sim::kIsClient), Field<void*>(s, sim::kObjectManager), Field<void*>(s, sim::kAIDirector),
      Field<void*>(s, sim::kBotDirector), Field<void*>(s, sim::kReplayRecorder), Field<void*>(s, sim::kPathfinder),
      Field<unsigned>(s, sim::kLuaThreadId), Field<void*>(s, sim::kLevel),
      static_cast<int>((Field<char*>(s, sim::kOutgoingEnd) - Field<char*>(s, sim::kOutgoingBegin)) / 28),
      Field<unsigned>(s, sim::kObjectUpdates));
}

struct Histo {
  const char* name;
  int count;
};
Histo g_histo[512];
int g_histoN;
int g_objects, g_pendingDelete;
int g_census[7];  // objects with: ability data, lua thread, behavior tree, replicated, brain, agent, mover
const char* const kCensusNames[7] = {"abilitydata", "luathread", "behaviortree", "replicated", "brain", "agent", "mover"};
const std::uintptr_t kCensusFields[7] = {obj::kAbilityData, obj::kLuaThread, obj::kBehaviorTree, obj::kReplicated,
                                         obj::kBrain, obj::kAgent, obj::kMover};

void CountObjects() {
  g_histoN = 0;
  g_objects = g_pendingDelete = 0;
  std::memset(g_census, 0, sizeof g_census);
  void* om = Field<void*>(g_sim, sim::kObjectManager);
  int it = ObjMgrIterBegin(om);
  for (void* obj = ObjMgrIterNext(om, &it); obj; obj = ObjMgrIterNext(om, &it)) {
    ++g_objects;
    if (Field<unsigned char>(obj, obj::kPendingDelete)) ++g_pendingDelete;
    for (int k = 0; k < 7; ++k)
      if (Field<void*>(obj, kCensusFields[k])) ++g_census[k];
    const char* name = AssetName(Field<void*>(obj, 4));
    int i = 0;
    while (i < g_histoN && g_histo[i].name != name) ++i;
    if (i == g_histoN && g_histoN < 512) g_histo[g_histoN++] = {name, 0};
    if (i < g_histoN) ++g_histo[i].count;
  }
}

void LogHisto() {
  // sort by count, descending (small n, insertion sort)
  for (int i = 1; i < g_histoN; ++i)
    for (int j = i; j > 0 && g_histo[j].count > g_histo[j - 1].count; --j) {
      Histo t = g_histo[j]; g_histo[j] = g_histo[j - 1]; g_histo[j - 1] = t;
    }
  Log("objects by noun (%d nouns, %d objects); with %s %d, %s %d, %s %d, %s %d, %s %d, %s %d, %s %d:", g_histoN, g_objects,
      kCensusNames[0], g_census[0], kCensusNames[1], g_census[1], kCensusNames[2], g_census[2], kCensusNames[3], g_census[3],
      kCensusNames[4], g_census[4], kCensusNames[5], g_census[5], kCensusNames[6], g_census[6]);
  char line[400];
  int len = 0;
  for (int i = 0; i < g_histoN; ++i) {
    len += std::snprintf(line + len, sizeof line - len, " %s=%d", g_histo[i].name, g_histo[i].count);
    if (len > 300 || i == g_histoN - 1) { Log("  %s", line); len = 0; line[0] = 0; }
  }
}

void DumpMarkersets(void* levelAsset) {
  int* data = Field<int*>(levelAsset, 0x68);
  if (!data) { Log("level %s has no data (+0x68 = 0)", AssetName(levelAsset)); return; }
  void** sets = reinterpret_cast<void**>(data[0]);
  int n = data[1];
  Log("level %s: %d markersets", AssetName(levelAsset), n);
  for (int i = 0; i < n; ++i) {
    void* ms = sets[i];
    char* d = ms ? Field<char*>(ms, 0x68) : nullptr;
    if (!d) { Log("  [%d] %s: not loaded", i, AssetName(ms)); continue; }
    int* flags = Field<int*>(d, 0x20);
    int nf = Field<int>(d, 0x24);
    char fl[128];
    int len = 0;
    fl[0] = 0;
    for (int k = 0; k < nf && k < 16; ++k) len += std::snprintf(fl + len, sizeof fl - len, "%s%d", k ? "," : "", flags[k]);
    Log("  [%d] %s: markers %d group %08X weight %.2f flags [%s]", i, AssetName(ms), Field<int>(d, 4), Field<unsigned>(d, 8),
        Field<float>(d, 0x18), fl);
  }
}

// The component type list (descriptors built by 0x0087F580): +8 flags (bit 0 = registered in a
// storage map whose mode byte is 0, bit 1 = mode byte nonzero), +0xC name, +0x30 next.
void LogComponentTypes() {
  char line[400];
  int len = 0, n = 0, both = 0, only0 = 0, only1 = 0;
  for (char* c = *At<char**>(kComponentTypes); c; c = Field<char*>(c, 0x30)) {
    unsigned f = Field<unsigned>(c, 8);
    ++n;
    if ((f & 3) == 3) ++both; else if (f & 1) ++only0; else if (f & 2) ++only1;
    len += std::snprintf(line + len, sizeof line - len, " %s=%X", SafeStr(Field<const char*>(c, 0xC)), f);
    if (len > 300) { Log("  components:%s", line); len = 0; line[0] = 0; }
  }
  if (len) Log("  components:%s", line);
  Log("component types: %d (flag bit 0 and 1: %d, only bit 0: %d, only bit 1: %d); this simulator's mode byte = %d", n, both,
      only0, only1, g_cfg.componentMode);
}

// ---------------------------------------------------------------------------------------------
// Lua probes: <game dir>\dgsim_start.lua runs once after the level starts, dgsim_probe.lua every
// probe_ms while ticking. Each chunk runs on the master lua_State (which a server simulator uses),
// with our simulator bound, under lua_pcall; it returns a string that is logged line by line.

char g_gameDir[MAX_PATH];
char g_luaBuf[65536];
const char* g_luaName;

bool LoadLuaFile(const char* name) {
  char path[MAX_PATH];
  std::snprintf(path, sizeof path, "%s\\%s", g_gameDir, name);
  FILE* f = std::fopen(path, "rb");
  if (!f) return false;
  size_t n = std::fread(g_luaBuf, 1, sizeof g_luaBuf - 1, f);
  std::fclose(f);
  g_luaBuf[n] = 0;
  return true;
}

void LogLines(const char* tag, const char* text) {
  char line[600];
  while (*text) {
    const char* nl = std::strchr(text, '\n');
    size_t n = nl ? static_cast<size_t>(nl - text) : std::strlen(text);
    if (n >= sizeof line) n = sizeof line - 1;
    std::memcpy(line, text, n);
    line[n] = 0;
    Log("%s %s", tag, line);
    text += n;
    while (*text == '\n' || *text == '\r') ++text;
  }
}

void RunLoadedLua() {
  void* L = Field<void*>(static_cast<char*>(g_sim) + sim::kLua, luasim::kLuaState);
  int top = LuaGetTop(L);
  int rc = LuaLoadBuffer(L, g_luaBuf, static_cast<unsigned>(std::strlen(g_luaBuf)), g_luaName);
  if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
  const char* out = SafeStr(LuaToString(L, -1, nullptr));
  char tag[64];
  std::snprintf(tag, sizeof tag, "lua %s%s:", g_luaName, rc ? " FAILED" : "");
  LogLines(tag, out);
  LuaSetTop(L, top);
}

void RunLuaFile(const char* name) {
  if (!LoadLuaFile(name)) return;
  g_luaName = name;
  WithServerSim(name, RunLoadedLua);
}

// direct_ability = 1: after dgsim_start.lua, activate the tower attack it prepared (Lua globals
// dgsim_attack, dgsim_tower, dgsim_victim) through ActivateAbility (0x0090A390) instead of a request,
// i.e. the step a working server update would reach once the behavior tree picks the ability.
unsigned LuaGlobalId(void* L, const char* name) {
  LuaPushLString(L, name, static_cast<unsigned>(std::strlen(name)));
  LuaGetTable(L, kLuaGlobalsIndex);
  unsigned v = static_cast<unsigned>(reinterpret_cast<std::uintptr_t>(LuaToUserdata(L, -1)));
  LuaSetTop(L, -2);
  return v;
}

void DirectAbility() {
  void* L = Field<void*>(static_cast<char*>(g_sim) + sim::kLua, luasim::kLuaState);
  int top = LuaGetTop(L);
  unsigned ability = LuaGlobalId(L, "dgsim_attack"), tower = LuaGlobalId(L, "dgsim_tower"), victim = LuaGlobalId(L, "dgsim_victim");
  LuaSetTop(L, top);
  Log("direct ability: ActivateAbility(%08X, agent %08X, target %08X, 0, rank 1, 0, 0, 0, 0)", ability, tower, victim);
  if (!ability || !tower || !victim) return;
  unsigned inst = ActivateAbility(ability, tower, victim, 0, 1, 0.f, 0.f, 0.f, 0);
  Log("direct ability: instance %08X; outgoing now %d", inst,
      static_cast<int>((Field<char*>(g_sim, sim::kOutgoingEnd) - Field<char*>(g_sim, sim::kOutgoingBegin)) / 28));
}

// GMS id -> wire byte, read from the client's own message factory (open question in GMS_PROTOCOL.md).
void LogTransportIds() {
  void* factory = *At<void**>(kMessageFactory);
  if (!factory) { Log("message factory not created yet"); return; }
  char line[600];
  int len = 0, contiguous = 1;
  for (int id = 0; id <= 76; ++id) {
    unsigned char b = GetTransportId(factory, id);
    if (b != 0x81 + id) contiguous = 0;
    len += std::snprintf(line + len, sizeof line - len, " %d=%02X", id, b);
    if (len > 400 || id == 76) { Log("gms transport ids:%s", line); len = 0; line[0] = 0; }
  }
  Log("gms transport ids are %s", contiguous ? "0x81 + id for all 77 ids" : "NOT 0x81 + id throughout (see above)");
}

// Histogram of the wire bytes of the messages in the simulator's outgoing vector.
void LogOutgoing() {
  char* b = Field<char*>(g_sim, sim::kOutgoingBegin);
  char* e = Field<char*>(g_sim, sim::kOutgoingEnd);
  int counts[256] = {};
  int unknown = 0;
  for (char* entry = b; entry < e; entry += 28) {
    bool found = false;
    for (int k = 4; k <= 5 && !found; ++k) {
      unsigned* msg = reinterpret_cast<unsigned*>(Field<unsigned>(entry, 4 * k));
      DWORD vt;
      if (msg && ReadDword(msg, &vt) && vt == kMessageVtable) {
        ++counts[reinterpret_cast<unsigned char*>(msg)[5]];
        found = true;
      }
    }
    if (!found) ++unknown;
  }
  int idOf[256];
  for (int w = 0; w < 256; ++w) idOf[w] = -1;
  if (void* factory = *At<void**>(kMessageFactory))
    for (int id = 0; id <= 76; ++id) idOf[GetTransportId(factory, id)] = id;
  char line[600];
  int len = 0;
  for (int w = 0; w < 256; ++w)
    if (counts[w]) len += std::snprintf(line + len, sizeof line - len, " %02X(gms id %d)x%d", w, idOf[w], counts[w]);
  Log("outgoing messages by wire byte (%d entries, %d unreadable):%s", static_cast<int>((e - b) / 28), unknown, line);
}

// ---------------------------------------------------------------------------------------------
// phases

void* g_level;

void StepCreate() {
  void* gameClient = *At<void**>(kGameClient);  // null in a core-only host (H1-live)
  void* client = gameClient ? Field<void*>(gameClient, 8) : nullptr;
  Log("create: current simulator %p, client simulator %p, master %p", GetCurrentSim(), client, *At<void**>(kMasterSimulator));
  void* s = Alloc(kSimulatorSize, "SP_Simulator/cGameSimulator", 0, 0, 0, 0);
  SimCtor(s);
  SimReset(s);
  void* om = Alloc(kObjectManagerSize, "SP_Simulator/cGameObjectManager", 0, 0, 0, 0);
  ObjMgrCtor(om);
  Field<void*>(s, sim::kObjectManager) = om;
  Field<unsigned>(s, 0x10) = 0;
  Field<unsigned char>(s, sim::kIsClient) = 0;
  Field<void*>(s, 0xD8) = static_cast<char*>(s) + 0xE4;
  // Component storage map mode: the client writes 0, which registers the pools of the component
  // types whose flag bit 0 is set; a nonzero byte selects flag bit 1 (0x0087EE10).
  Field<unsigned char>(s, sim::kComponentMode) = static_cast<unsigned char>(g_cfg.componentMode);
  LogComponentTypes();
  ObjMgrSetSim(om, s);
  g_sim = s;
  AbilityTickSetSim(s);
  DamageSetSim(s);
  matchstats::SetSim(s);  // MSTATS3: the heal-core detour counts on this simulator only
  death::SetSim(s);
  regen::SetSim(s);
  components::SetSim(s);  // D1j
  volumes::SetSim(s);     // D12c
  effects::SetSim(s);     // FOGSTACKS
  Log("create: simulator %p constructed, object manager %p (vtable %08X); running Init with it bound", s, om, Field<unsigned>(om, 0));
  g_prevSim = GetCurrentSim();
  SetCurrentSim(s);
  SimInit(s);
  // D1c-live: the game-mode Lua functions over the stubs, before the level (the nubs' passives call SetEnergyLineState
  // while StartLevel creates them)
  match::OnSimulatorCreated(s);
  // D6a: the bots' optional nBotHost namespace (bots.h), before dgsim_bots_api.lua runs at the level load
  bots::OnSimulatorCreated(s);
  // D1f-live: the player match-stats table the gold and XP grants add to (death.h)
  death::OnSimulatorCreated(s);
  SetCurrentSim(g_prevSim);
  LogSimFields("after Init");
}

void StepLevel() {
  g_level = FindAssetByName(g_cfg.level);
  Log("level: %s -> asset %p data %p", g_cfg.level, g_level, g_level ? Field<void*>(g_level, 0x68) : nullptr);
  if (!g_level) return;
  DumpMarkersets(g_level);
  void* dir = Field<void*>(g_sim, sim::kAIDirector);
  if (g_cfg.directorActive && dir) {
    Field<unsigned char>(dir, aidir::kActive) = 1;
    Log("level: AI director %p set active (director_active = 1)", dir);
  }
  SetLevel(g_sim, g_level, g_cfg.seed, g_cfg.flags);
  Log("level: SetLevel(seed %u, flags %08X) done; StartLevel", g_cfg.seed, g_cfg.flags);
  StartLevel(g_sim);
  Log("level: StartLevel returned");
}

bool g_abilityTick;  // D1a-live hooks installed (ability_tick = 1)
bool g_damage;       // D1f-live TakeDamage core installed (damage = 1; the old key placeholder_damage still works)

void TickTime() { TimeTick(g_sim); }
unsigned g_luaResumes;
unsigned g_resumeTicks;  // ticks with at least one resume
void TickLua() {
  char* lua = static_cast<char*>(g_sim) + sim::kLua;
  if (g_abilityTick) AbilityTickBeforeScheduler();
  LuaTick(lua);
  unsigned r = Field<unsigned>(lua, luasim::kResumesThisTick);
  g_luaResumes += r;
  if (r && ++g_resumeTicks <= 5) Log("lua scheduler: %u resume(s) in tick %u", r, g_ticks);
}
// D1a-live: register the Tick threads prepared during the scheduler run, end finished instances. D1f-live: then the
// deaths' deferred instance ends (death.h) and the shield totals (damage.h).
void TickAbilities() {
  if (g_abilityTick) AbilityTickAfterScheduler();
  death::AfterScheduler();
  if (g_damage) DamageTickShields();
  effects::AfterAbilities();  // FOGSTACKS: the attached effects an ended modifier left
}
// D1i: health and resource regeneration (regen.h), after the frame's Lua and ability ends, while not paused.
void TickRegen() { regen::Tick(); }

int CountLuaThreads() {
  char* lua = static_cast<char*>(g_sim) + sim::kLua;
  int** buckets = Field<int**>(lua, luasim::kThreadBuckets);
  int nb = Field<int>(lua, luasim::kThreadBucketCount), n = 0;
  for (int b = 0; b < nb; ++b)
    for (int* node = buckets[b]; node; node = reinterpret_cast<int*>(node[1])) ++n;
  return n;
}
int g_luaThreads = -1;
void CountLua() { g_luaThreads = CountLuaThreads(); }
void TickLocomotion() { LocomotionTick(0, 0); }
void TickProjectiles() { ProjectileTick(); }
void TickWorld() {
  volumes::Tick();  // D12c: attached trigger volumes follow their owners before the world's trigger step
  void* world = static_cast<char*>(g_sim) + sim::kWorld;
  WorldTickFn fn = reinterpret_cast<WorldTickFn>((*static_cast<void***>(world))[3]);
  fn(world, GetDt(g_sim));
}
void TickDelete() {
  if (GmsHostActive()) GmsHostBeforeDelete();  // ABILVFX: effects made and destroyed in this tick still go out
  DeletePending();
}
void TickFow() { FowTick(static_cast<char*>(g_sim) + sim::kFow); }
// D1b-live (agents.h, docs/AGENT_AI.md §5): the agent step the stripped server update ran per frame, after the command
// intake (and the director) and before the Lua scheduler: every agent's behavior tree with the frame's dt.
void TickAgents() { agents::Tick(g_sim, GetDt(g_sim)); }

// G2: the GMS first (RakNet polled into the session, the session's timed steps), as ADR-2's loop step 1.
void TickGms() { GmsHostPoll(); }
// D7-live: the players' command queues and their creatures' behavior trees (ADR-2's loop step 2).
void TickCommands() { GmsHostTickCommands(); }
// D7-live: what the simulation produced goes out after it (ADR-2's loop steps 4-5).
void TickGmsOut() { GmsHostFlush(); }
// D2g-live: the network-rate timers (stripped per-frame update), before the locomotion tick reads their mask.
void TickNetworkRate() { GmsHostTickNetworkRate(); }
// D1c-live: the match and the director (match.h), after the command intake and before the agent step (the director's
// slot of AGENT_AI.md §5).
void TickMatch() {
  match::Tick(g_sim);
  matchrec::Tick(g_sim);  // MATCHREC: the match recorder's snapshots ([host] match_record)
  mapfix::Tick(g_sim);  // MAPLUA: dgsim_mapfix.Apply() again while a script's table is missing
  if (GmsHostActive()) banter::Tick(g_sim, match::MatchSeconds());  // D11b: the meeting banter (banter.h)
}
void MatchLevelStarted() {
  match::OnLevelStarted(g_sim);
  bots::OnLevelStarted(g_sim);  // D6a: Tuning.BotPlan, the lanes (the director's splines), dgsim_bots_api.lua
  mapfix::OnLevelStarted(g_sim);  // MAPLUA: MM_Map_v3's script fix-ups (dgsim_mapfix.lua), MM's base regen (opt-in)
}
// D6a: the bots' lane fronts (bots.h), after the match's tick and before the agent step.
void TickBots() { bots::Tick(g_sim); }

// H5: a tick part runs as guards.h's step i (guards::kTickTime ... kTickBots, the old kTickNames order): an exception
// skips that run only (before H5 the part was disabled for the rest of the match: the frozen world of a "soft crash")
void RunPart(int i, StepFn fn) { guards::Run(i, fn); }

// R1b: the master Lua state's memory (lua_gc(L, LUA_GCCOUNT) 0x0085BD20 on the lua_State at master +0x14; the
// "Gears/Lua" heap it lives in is (n + 3) x 2 MiB = 8 MiB with the client's n = 1, Q1_SIMULATION_HOST.md *The master
// Lua heap*). Sampled after every server tick; the stats host line shows the current value and the peak.
constexpr unsigned kLuaHeapKb = 8 * 1024;
int g_luaKb = -1, g_luaPeakKb = -1;
double g_luaPeakAt = 0;
using LuaGcFn = int(__cdecl*)(void* L, int what, int data);
int SehLuaKb() {
  __try {
    void* master = *At<void**>(kMasterSimulator);
    if (!master) return -1;
    void* L = Field<void*>(master, 0x14);
    if (!L) return -1;
    return At<LuaGcFn>(0x0085BD20)(L, 3, 0);  // LUA_GCCOUNT: KiB in use
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return -1;
  }
}
void SampleLuaHeap() {
  const int kb = SehLuaKb();
  if (kb < 0) return;
  g_luaKb = kb;
  if (kb > g_luaPeakKb) {
    g_luaPeakKb = kb;
    g_luaPeakAt = LogMs() / 1000.0;
  }
}

void ServerTick() {
  g_prevSim = GetCurrentSim();
  SetCurrentSim(g_sim);
  guards::TickStart();  // H5: [host] test_stall_at
  char* s = static_cast<char*>(g_sim);
  if (GmsHostActive()) RunPart(9, TickGms);
  RunPart(0, TickTime);
  if (Field<unsigned char>(s, sim::kRunning) && Field<unsigned char>(s, sim::kState16) != 1) {
    agents::BeginFrame();
    if (GmsHostActive() && !IsPaused(g_sim)) RunPart(10, TickCommands);
    if (match::Enabled() && !IsPaused(g_sim)) RunPart(13, TickMatch);
    if (match::Enabled() && !IsPaused(g_sim)) RunPart(15, TickBots);
    if (g_cfg.tickAgents && !IsPaused(g_sim)) RunPart(7, TickAgents);
    RunPart(1, TickLua);
    RunPart(8, TickAbilities);
    if (regen::Enabled() && !IsPaused(g_sim)) RunPart(14, TickRegen);
    if (!IsPaused(g_sim)) {
      if (GmsHostActive()) RunPart(12, TickNetworkRate);
      RunPart(2, TickLocomotion);
      RunPart(3, TickProjectiles);
      RunPart(4, TickWorld);
      RunPart(5, TickDelete);
      RunPart(6, TickFow);
    }
  }
  if (GmsHostActive()) RunPart(11, TickGmsOut);
  if (HostGet().enabled) SampleLuaHeap();
  perf::TickEnd();  // Q3c: the tick's time and traffic; at a new match minute the window's lines (server sim bound)
  SetCurrentSim(g_prevSim);
  ++g_ticks;
  hostwd::Beat(g_ticks, match::MatchSeconds());  // H5: the watchdog's tick counter (host mode)
}

void Stats(unsigned now, bool histo) {
  if (!Guarded("stats", CountObjects)) return;
  Guarded("stats.lua", CountLua);
  char* s = static_cast<char*>(g_sim);
  Log("stats t+%us ticks %u: objects %d (pending delete %d), outgoing %d, objupdates %u, lua scheduled threads %d resumes %u, "
      "asserts server %u other %u, lua errors server %u other %u, +14 %u +16 %u",
      (now - g_tickStart) / 1000, g_ticks, g_objects, g_pendingDelete,
      static_cast<int>((Field<char*>(s, sim::kOutgoingEnd) - Field<char*>(s, sim::kOutgoingBegin)) / 28),
      Field<unsigned>(s, sim::kObjectUpdates), g_luaThreads, g_luaResumes, g_assertsServer, g_assertsOther, g_luaErrsServer,
      g_luaErrsOther, Field<unsigned char>(s, sim::kRunning), Field<unsigned char>(s, sim::kState16));
  if (g_abilityTick) {
    char ab[1200];
    AbilityTickFormatStats(ab, sizeof ab);
    Log("stats abilities: %s", ab);
  }
  if (agents::Installed() && g_cfg.tickAgents && histo) {  // every histo_ms (10 s): one line per agent step summary
    char ag[2400];
    agents::FormatStats(ag, sizeof ag);
    Log("stats agents: %s", ag);
    if (agents::FormatMinionStats(ag, sizeof ag)) Log("stats minions: %s", ag);  // MINJUNGLE
    if (agents::FormatNpcStats(ag, sizeof ag)) Log("stats npc: %s", ag);          // NPCTARGET
    if (agents::FormatLeashStats(ag, sizeof ag)) Log("stats leash: %s", ag);      // LEASH
    if (agents::FormatTowerStats(ag, sizeof ag)) Log("stats towers: %s", ag);     // TOWERAGGRO
    if (agents::FormatHunterStats(ag, sizeof ag)) Log("stats hunters: %s", ag);   // HUNTERBOT
    if (agents::FormatSightStats(ag, sizeof ag)) Log("stats botbush: %s", ag);   // BOTBUSH
    if (hunterroute::FormatStats(ag, sizeof ag)) Log("stats hunterroute: %s", ag);  // HUNTERROUTE
    if (agents::FormatCampFinishStats(ag, sizeof ag)) Log("stats campfinish: %s", ag);  // CAMPFINISH
    if (agents::FormatDesecStats(ag, sizeof ag)) Log("stats desecrator: %s", ag);  // DESECBOT-3
    if (agents::FormatMikellaStats(ag, sizeof ag)) Log("stats mikella: %s", ag);    // MIKELLABOT
    if (bots2::FormatStats(ag, sizeof ag)) Log("stats bots2: %s", ag);  // BOTS2
    if (bots2::FormatExtraStats(ag, sizeof ag)) Log("stats bots2c: %s", ag);  // BOTS2c
    if (agents::FormatLostStats(ag, sizeof ag)) Log("stats lost: %s", ag);         // BACKTOACTION
    if (agents::FormatNullPosStats(ag, sizeof ag)) Log("stats nullpos: %s", ag);   // NULLPOS
    if (agents::FormatKillFinishStats(ag, sizeof ag)) Log("stats killfinish: %s", ag);  // KILLFINISH
    if (agents::FormatBotTowerStats(ag, sizeof ag)) Log("stats bottower: %s", ag);      // BOTTOWER
    if (agents::FormatBotTowerBots(ag, sizeof ag)) Log("stats bottower bots: %s", ag);  // BOTTOWER
  }
  if (g_damage && histo) {
    char dm[640];
    DamageFormatStats(dm, sizeof dm);
    Log("stats damage: %s", dm);
  }
  if (death::Enabled() && histo) {
    char de[1200];
    death::FormatStats(de, sizeof de);
    Log("stats death: %s", de);
    char pa[200];
    passives::FormatStats(pa, sizeof pa);  // D6c
    Log("stats passives: %s", pa);
    char ip[240];
    itempassives::FormatStats(ip, sizeof ip);  // ITEMDEATH
    Log("stats %s", ip);
    char mf[200];
    mapfix::FormatStats(mf, sizeof mf);  // MAPLUA
    Log("stats %s", mf);
  }
  if (shop::Enabled() && histo) {  // D15c
    char sh[300];
    shop::FormatStats(sh, sizeof sh);
    Log("stats shop: %s", sh);
  }
  if (volumes::Installed() && histo) {  // D12c
    char vs[300];
    volumes::FormatStats(vs, sizeof vs);
    Log("stats volumes: %s", vs);
  }
  if (effects::Installed() && histo) {  // FOGSTACKS
    char es[400];
    effects::FormatStats(es, sizeof es);
    Log("stats effects: %s", es);
  }
  if (components::Installed() && histo) {  // D1j: the ability data containers and every server pool
    char cs[600];
    components::FormatStats(cs, sizeof cs, g_census[0]);
    Log("stats containers: %s", cs);
    char ps[3000];
    components::FormatPools(ps, sizeof ps);
    if (ps[0]) Log("stats pools: %s", ps);
  }
  if (regen::Enabled() && histo) {
    char rg[400];
    regen::FormatStats(rg, sizeof rg);
    Log("stats regen: %s", rg);
  }
  if (HostGet().enabled) {
    char hs[400];
    HostFormatStats(hs, sizeof hs);
    char hl[700];
    HeadlessFormatStats(hl, sizeof hl);  // H1-live: ", headless: ..." when a stage-2 piece is on
    if (g_luaKb >= 0)
      Log("stats host: %s, lua heap %d KiB of %u (%d %%), peak %d KiB (%d %%) at %.0f s%s", hs, g_luaKb, kLuaHeapKb,
          g_luaKb * 100 / static_cast<int>(kLuaHeapKb), g_luaPeakKb, g_luaPeakKb * 100 / static_cast<int>(kLuaHeapKb),
          g_luaPeakAt, hl);
    else
      Log("stats host: %s%s", hs, hl);
    if (hostctl::Active()) Log("stats control: %s", hostctl::Describe().c_str());
  }
  if (HostGet().enabled && histo) {  // H5: the step guards and the watchdog
    char gs[700], wd[200];
    guards::FormatStats(gs, sizeof gs);
    hostwd::Describe(wd, sizeof wd);
    Log("stats guards: %s; %s", gs, wd);
  }
  if (match::Enabled() && histo) {
    char ms[1400];
    match::FormatStats(ms, sizeof ms);
    Log("stats match: %s", ms);
    char bs[3000];
    bots::FormatStats(bs, sizeof bs);
    Log("stats bots: %s", bs);
  }
  if (GmsHostActive()) {
    char gs[600];
    GmsHostFormatStats(gs, sizeof gs);
    Log("stats gms: %s", gs);
    char cs[1400];  // D7c: the takeover counters made the line longer than 700; REFUND1 the undo counters
    if (GmsHostFormatCommandStats(cs, sizeof cs)) Log("stats commands: %s", cs);
    if (histo && banter::Enabled()) {  // D11b
      char bt[600];
      banter::FormatStats(bt, sizeof bt);
      Log("stats banter: %s", bt);
    }
  }
  if (histo) LogHisto();
}

// H5: the stats lines as a guarded step of the frame (guards::kFrameStats)
unsigned g_statsNow;
bool g_statsHisto;
void StatsStep() { Stats(g_statsNow, g_statsHisto); }
void GuardedStats(unsigned now, bool histo) {
  g_statsNow = now;
  g_statsHisto = histo;
  guards::Run(guards::kFrameStats, StatsStep);
}

// Q3c (perf.h): the records per window. The game objects' pool hands out ids as (serial << 16) | slot: each pool's
// serial starts at (the global count of pools initialized before it) x 256 (ObjectPool_Init 0x00A81C70: pool +0xC =
// [0x0144F7E8] << 8, then [0x0144F7E8] + 1) and goes up by one at every element's creation, skipping a 0 in its low
// 16 bits (ObjectPool_CreateElement 0x00A817D0), so an id is never <= 0xFFFF and the 16-bit serial wraps after 65,535
// creations. Projectiles, vision objects and effects are game objects too: one counter for all of them.
struct IdCounter {
  bool ok = false;
  std::uint32_t counter = 0;  // pool +0xC: the serial of the last element created (32 bits, not masked)
  int live = 0, highWater = 0, capacity = 0;
};
IdCounter SehIdCounter() {
  IdCounter c;
  __try {
    void* om = g_sim ? Field<void*>(g_sim, sim::kObjectManager) : nullptr;
    void* pool = om ? Field<void*>(om, 4) : nullptr;  // GameObjectManager_CreateObject: ObjectPool_Get(om +4, id)
    if (!pool) return c;
    c.counter = Field<std::uint32_t>(pool, 0xC);
    c.live = Field<int>(pool, opool::kLive);
    c.highWater = Field<int>(pool, opool::kHighWater);
    c.capacity = Field<int>(pool, opool::kCapacity);
    c.ok = true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    c.ok = false;
  }
  return c;
}
bool g_idFirstSeen;
std::uint32_t g_idFirst;

void EngineGauges(gms::timing::Gauges* g, std::string* line, int minute) {
  int vision = 0;
  for (int i = 0; i < g_histoN; ++i)
    if (g_histo[i].name && _strnicmp(g_histo[i].name, "Vision_Object", 13) == 0) vision += g_histo[i].count;
  g->push_back({"objects", g_objects});
  g->push_back({"pending_delete", g_pendingDelete});
  g->push_back({"vision_objects", vision});
  g->push_back({"with_ability_data", g_census[0]});
  g->push_back({"lua_threads", g_luaThreads});
  g->push_back({"lua_heap_kb", g_luaKb});
  g->push_back({"asserts_server", g_assertsServer});
  g->push_back({"lua_errors_server", g_luaErrsServer});
  const IdCounter c = SehIdCounter();
  if (c.ok && !g_idFirstSeen) {
    g_idFirstSeen = true;
    g_idFirst = c.counter;
  }
  g->push_back({"id_serial", c.ok ? static_cast<double>(c.counter & 0xFFFF) : -1.0});
  g->push_back({"id_created", c.ok ? static_cast<double>(c.counter - g_idFirst) : -1.0});
  g->push_back({"id_wraps", c.ok ? static_cast<double>((c.counter >> 16) - (g_idFirst >> 16)) : -1.0});
  g->push_back({"object_pool_live", c.ok ? c.live : -1});
  g->push_back({"object_pool_high_water", c.ok ? c.highWater : -1});
  static const char* const kPools[][2] = {{"cAbilityInstanceData", "ability_instances"},
                                          {"cAbilityDataContainer", "ability_containers"},
                                          {"cProjectileData", "projectiles"},
                                          {"cDamageEvent", "damage_events"},
                                          {"cTriggerVolume", "trigger_volumes"},
                                          {"cAttributeSnapshot", "attribute_snapshots"}};
  for (const auto& p : kPools) {
    int live = -1, hw = -1, cap = -1;
    components::PoolCount(p[0], &live, &hw, &cap);
    g->push_back({p[1], live});
  }
  char b[300];
  if (c.ok)
    std::snprintf(b, sizeof b,
                  "ids: minute %d: game-object pool serial 0x%04X (ids 0x%04Xxxxx), %u created since the first window "
                  "(%u wrap(s) of the 16-bit serial), live %d of %d (high water %d)",
                  minute, c.counter & 0xFFFF, c.counter & 0xFFFF, c.counter - g_idFirst,
                  (c.counter >> 16) - (g_idFirst >> 16), c.live, c.capacity, c.highWater);
  else
    std::snprintf(b, sizeof b, "ids: minute %d: the game-object pool could not be read", minute);
  *line = b;
}

void ReadConfig() {
  g_cfg.startMs = GetPrivateProfileIntA("dgsim", "start_ms", g_cfg.startMs, g_ini);
  g_cfg.runMs = GetPrivateProfileIntA("dgsim", "run_ms", g_cfg.runMs, g_ini);
  g_cfg.tickMs = GetPrivateProfileIntA("dgsim", "tick_ms", g_cfg.tickMs, g_ini);
  g_cfg.stopAfter = GetPrivateProfileIntA("dgsim", "stop_after", g_cfg.stopAfter, g_ini);
  g_cfg.setRunning = GetPrivateProfileIntA("dgsim", "set_running", g_cfg.setRunning, g_ini);
  g_cfg.logAllAsserts = GetPrivateProfileIntA("dgsim", "log_all_asserts", g_cfg.logAllAsserts, g_ini);
  g_cfg.statsMs = GetPrivateProfileIntA("dgsim", "stats_ms", g_cfg.statsMs, g_ini);
  g_cfg.histoMs = GetPrivateProfileIntA("dgsim", "histo_ms", g_cfg.histoMs, g_ini);
  g_cfg.seed = GetPrivateProfileIntA("dgsim", "seed", g_cfg.seed, g_ini);
  g_cfg.componentMode = GetPrivateProfileIntA("dgsim", "component_mode", g_cfg.componentMode, g_ini);
  g_cfg.directorActive = GetPrivateProfileIntA("dgsim", "director_active", g_cfg.directorActive, g_ini);
  g_cfg.probeMs = GetPrivateProfileIntA("dgsim", "probe_ms", g_cfg.probeMs, g_ini);
  g_cfg.tickAgents = GetPrivateProfileIntA("dgsim", "tick_agents", g_cfg.tickAgents, g_ini);
  g_cfg.directAbility = GetPrivateProfileIntA("dgsim", "direct_ability", g_cfg.directAbility, g_ini);
  Log("config: direct_ability %d", g_cfg.directAbility);
  Log("config: tick_agents %d, probe_ms %d", g_cfg.tickAgents, g_cfg.probeMs);
  char buf[96];
  GetPrivateProfileStringA("dgsim", "flags", "0", buf, sizeof buf, g_ini);
  g_cfg.flags = std::strtoul(buf, nullptr, 0);
  GetPrivateProfileStringA("dgsim", "level", g_cfg.level, g_cfg.level, sizeof g_cfg.level, g_ini);
  Log("config (%s): start_ms %d run_ms %d tick_ms %d stop_after %d level %s flags %08X seed %u set_running %d component_mode %d",
      g_ini, g_cfg.startMs, g_cfg.runMs, g_cfg.tickMs, g_cfg.stopAfter, g_cfg.level, g_cfg.flags, g_cfg.seed, g_cfg.setRunning,
      g_cfg.componentMode);
  Log("config: director_active %d", g_cfg.directorActive);
  // Host mode (D1d): start as soon as the engine is up and tick until the process ends, unless [host] says otherwise.
  if (HostGet().enabled) {
    g_cfg.startMs = HostOptionInt("start_ms", 0);
    g_cfg.runMs = HostOptionInt("run_ms", 0);
    g_cfg.luaProbes = HostOptionInt("lua_probes", 0);
    g_cfg.tickAgents = HostOptionInt("agents", g_cfg.tickAgents);
    Log("config [host]: start_ms %d run_ms %d (0 = tick until the process ends), lua_probes %d, agents %d", g_cfg.startMs,
        g_cfg.runMs, g_cfg.luaProbes, g_cfg.tickAgents);
  }
  // D1b-live: the agent AI per frame (agents.h)
  if (g_cfg.tickAgents) {
    agents::Options ao;
    ao.logLimit = HostGet().enabled ? HostOptionInt("agent_log_limit", ao.logLimit)
                                    : GetPrivateProfileIntA("dgsim", "agent_log_limit", ao.logLimit, g_ini);
    ao.blackboard = (HostGet().enabled ? HostOptionInt("agent_blackboard", 1)
                                       : GetPrivateProfileIntA("dgsim", "agent_blackboard", 1, g_ini)) != 0;
    ao.brain = (HostGet().enabled ? HostOptionInt("agent_brain", 1)
                                  : GetPrivateProfileIntA("dgsim", "agent_brain", 1, g_ini)) != 0;
    // D2g-live step 2: with [host] fog the vision model writes mVisibleToTeam, so the stand-in is off by default
    ao.allVisible = (HostGet().enabled ? HostOptionInt("agent_all_visible", HostOptionInt("fog", 1) ? 0 : 1)
                                       : GetPrivateProfileIntA("dgsim", "agent_all_visible", 1, g_ini)) != 0;
    ao.botBrain = HostOptionInt("bot_brain", 1) != 0;          // D6a
    ao.botThinkMs = HostOptionInt("bot_think_ms", ao.botThinkMs);  // D6a
    ao.keepNodeTimers = HostOptionInt("brain_node_timers", 1) != 0;  // D6c
    ao.respectCanInterrupt = HostOptionInt("brain_can_interrupt", 1) != 0;  // D6c
    ao.botPursuePoint = HostOptionInt("bot_pursue_point", 1) != 0;  // D6d
    {  // BOTIDLE: [host] bot_idle_fixes=0 or bot_fixups=0 turns the three off, each has its own key too
      const int all = HostOptionInt("bot_idle_fixes", 1) != 0 && HostOptionInt("bot_fixups", 1) != 0 ? 1 : 0;
      ao.botGambitFallthrough = all != 0 && HostOptionInt("bot_gambit_fallthrough", 1) != 0;
      ao.botPursueStuckMs = all != 0 ? HostOptionInt("bot_pursue_stuck_ms", ao.botPursueStuckMs) : 0;
      ao.botIdleFallbackMs = all != 0 ? HostOptionInt("bot_idle_fallback_ms", ao.botIdleFallbackMs) : 0;
    }
    ao.parasiteReach = static_cast<float>(HostOptionInt("parasite_reach", 40));
    ao.botReflex = HostOptionInt("bot_reflex", 1) != 0 && HostOptionInt("bot_fixups", 1) != 0;  // BOTREFLEX
    ao.botJungleFight = HostOptionInt("bot_jungle_fight", 1) != 0;  // PRETUSBOT
    ao.botWellDefense = HostOptionInt("bot_well_defense", 1) != 0;  // WELLDEF
    ao.botKits = HostOptionInt("bot_kits", 1) != 0;                 // HUNTERBOT
    ao.botHunterUlt = HostOptionInt("bot_hunter_ult", 1) != 0 && ao.botReflex;  // HUNTERBOT
    ao.botDesecKit = HostOptionInt("bot_desec_kit", 1) != 0 && ao.botReflex && ao.botKits;  // DESECBOT-3
    ao.botMikellaKit = HostOptionInt("bot_mikella_kit", 1) != 0 && ao.botKits;               // MIKELLABOT
    ao.botHunterIdleMs = HostOptionInt("bot_hunter_idle_ms", ao.botHunterIdleMs);  // HUNTERBOT
    if (ao.botHunterIdleMs < 0) ao.botHunterIdleMs = 0;
    ao.botCampFinish = HostOptionInt("bot_camp_finish", 1) != 0;  // CAMPFINISH
    ao.campFinishLogLimit = HostOptionInt("camp_finish_log_limit", ao.campFinishLogLimit);
    ao.botRecall = HostOptionInt("bot_recall", 1) != 0;  // BOTRECALL
    ao.botHoldStuckMs = HostOptionInt("bot_hold_stuck_ms", ao.botHoldStuckMs);  // BOTTEMPO
    if (ao.botHoldStuckMs < 0) ao.botHoldStuckMs = 0;
    ao.botStaleTarget = HostOptionInt("bot_stale_target", 1) != 0;  // BOTTEMPO
    ao.botFogCheck = HostOptionInt("bot_fog_check", 1) != 0;        // BOTBUSH
    ao.botPointFollowFix = HostOptionInt("bot_point_follow_fix", 1) != 0;  // BACKTOACTION
    ao.botLostMs = HostOptionInt("bot_lost_ms", ao.botLostMs);             // BACKTOACTION
    if (ao.botLostMs < 0) ao.botLostMs = 0;
    ao.botNullPosStrict = HostOptionInt("bot_nullpos_strict", 0) != 0;     // NULLPOS
    ao.testNullPosFollowS = HostOptionInt("test_nullpos_follow", 0);       // NULLPOS: the forced case (test aid)
    ao.botBackStep = HostOptionInt("bot_back_step", 1) != 0 && ao.botReflex;  // BOTBACK
    ao.botKillFinish = HostOptionInt("bot_kill_finish", 1) != 0 && ao.botReflex;  // KILLFINISH
    ao.botTowerAware = HostOptionInt("bot_tower_aware", 1) != 0 && ao.botReflex;      // BOTTOWER
    ao.botTowerKeepOut = HostOptionInt("bot_tower_keepout", 1) != 0 && ao.botReflex;  // BOTTOWER
    ao.botReflexWalk = HostOptionInt("bot_reflex_walk", 1) != 0 && ao.botReflex;      // BOTTOWER (REFLEXWALK)
    ao.botTowerLogLimit = HostOptionInt("bot_tower_log_limit", ao.botTowerLogLimit);  // BOTTOWER
    ao.botPlanChain = HostOptionInt("bot_plan_chain", 1) != 0 && HostOptionInt("bot_fixups", 1) != 0;  // BOTCYCLE
    ao.botPlanDwellMs = HostOptionInt("bot_fixups", 1) != 0 ? HostOptionInt("bot_plan_dwell_ms", ao.botPlanDwellMs) : 0;
    if (ao.botPlanDwellMs < 0) ao.botPlanDwellMs = 0;
    ao.botEngageBlockMs = HostOptionInt("bot_engage_block_ms", ao.botEngageBlockMs);  // BOTCYCLE
    if (ao.botEngageBlockMs < 0) ao.botEngageBlockMs = 0;
    ao.npcConditionTarget = HostOptionInt("npc_condition_target", 1) != 0;    // NPCTARGET
    ao.npcLazyConditions = HostOptionInt("npc_lazy_conditions", 1) != 0;      // NPCTARGET
    ao.npcRangeFallthrough = HostOptionInt("npc_range_fallthrough", 1) != 0;  // NPCTARGET
    ao.npcTargetLogLimit = HostOptionInt("npc_target_log_limit", ao.npcTargetLogLimit);
    ao.objectiveOnSight = HostOptionInt("objective_aggro_on_sight", 0) != 0;  // D14: the parasite fights only its attackers (1 from 2026-10-10 to 2026-10-15)
    ao.minionTarget = HostOptionInt("minion_target_priority", 1) != 0;  // MINTARGET
    ao.minionLeash = static_cast<float>(HostOptionInt("minion_leash_range", static_cast<int>(ao.minionLeash)));
    ao.minionDefendMs = HostOptionInt("minion_defend_ms", ao.minionDefendMs);
    ao.minionWatchMs = HostOptionInt("minion_watch_ms", ao.minionWatchMs);  // MINJUNGLE
    ao.minionIdleMs = HostOptionInt("minion_idle_ms", ao.minionIdleMs);
    ao.minionOffLane = static_cast<float>(HostOptionInt("minion_off_lane", static_cast<int>(ao.minionOffLane)));
    ao.minionWatchLogLimit = HostOptionInt("minion_watch_log_limit", ao.minionWatchLogLimit);
    ao.minionUnstick = HostOptionInt("minion_unstick", 1) != 0;
    ao.minionChase.enabled = HostOptionInt("minion_chase", 1) != 0;  // MINJUNGLE
    ao.minionChase.laneLeash =
        static_cast<float>(HostOptionInt("minion_lane_leash", static_cast<int>(ao.minionChase.laneLeash)));
    ao.minionChase.chaseMs = HostOptionInt("minion_chase_ms", static_cast<int>(ao.minionChase.chaseMs));
    ao.minionChase.chaseMinMove =
        static_cast<float>(HostOptionInt("minion_chase_min_move", static_cast<int>(ao.minionChase.chaseMinMove)));
    ao.minionChase.chaseDistance =
        static_cast<float>(HostOptionInt("minion_chase_distance", static_cast<int>(ao.minionChase.chaseDistance)));
    ao.minionChase.ignoreMs = HostOptionInt("minion_chase_ignore_ms", static_cast<int>(ao.minionChase.ignoreMs));
    ao.minionFollow.enabled = HostOptionInt("minion_follow_reset", 1) != 0;  // MINPURSUE
    ao.towerTarget.enabled = HostOptionInt("tower_target", 1) != 0;          // TOWERAGGRO
    ao.towerTarget.stick = HostOptionInt("tower_stick", 1) != 0;             // TOWERSTICK
    ao.towerLogLimit = HostOptionInt("tower_log_limit", ao.towerLogLimit);
    // LEASH: jungle creatures and well workers (gms/core/agents/leash.h)
    // CAMPFINISH round 3 (the user: players found the leash "a bit strong"): the engine's own tether for a jungle
    // creature is 22 around its spot, tested on the target (AgentBlackboard_GetTetherCircle 0x008AAB10, type 3 without
    // the footprint), so the target leash is 18 + 4 = 22 (LEASH: 15 + 4), the chase clock runs from 10 out (6) and
    // lasts 5 s for creeps (3) [assumed]
    ao.creepLeash.ownLimit = static_cast<float>(HostOptionInt("creep_leash", 18));
    ao.creepLeash.enabled = ao.creepLeash.ownLimit > 0.f;
    ao.creepLeash.targetMargin = static_cast<float>(HostOptionInt("creep_leash_reach", 4));
    ao.creepLeash.homeRadius = static_cast<float>(HostOptionInt("creep_leash_home", 10));
    ao.workerLeash.ownLimit = static_cast<float>(HostOptionInt("worker_leash", 2));
    ao.workerLeash.enabled = ao.workerLeash.ownLimit > 0.f;
    ao.workerLeash.targetMargin = static_cast<float>(HostOptionInt("worker_leash_reach", 2));
    const int chaseMs = HostOptionInt("leash_chase_ms", -1);  // both, when given (the workers' default 3000)
    ao.workerLeash.chaseMs = chaseMs >= 0 ? chaseMs : 3000;
    ao.creepLeash.chaseMs = HostOptionInt("creep_leash_chase_ms", chaseMs >= 0 ? chaseMs : 5000);
    ao.creepLeash.ignoreMs = ao.workerLeash.ignoreMs = HostOptionInt("leash_ignore_ms", 2000);
    // CAMPLEASH: a target inside the NPC's home radius stops neither by the chase time nor by its group's drop, and a
    // member that hit the target within its chase time keeps it when the group lets go (leash_area=0: CAMPFINISH's rule)
    ao.creepLeash.areaHolds = ao.workerLeash.areaHolds = HostOptionInt("leash_area", 1) != 0;
    ao.leashLogLimit = HostOptionInt("leash_log_limit", ao.leashLogLimit);
    {  // HUNTERROUTE: the Hunters' opening route, Vanquish rule and the first buff's pull (bot_hunter_route=0: all off)
      hunterroute::Options ho;
      ho.route = HostOptionInt("bot_hunter_route", 1) != 0;
      ho.vanquish = HostOptionInt("bot_hunter_vanquish", 1) != 0;
      ho.pull = HostOptionInt("bot_hunter_pull", ho.pull);
      ho.logLimit = HostOptionInt("hunter_route_log_limit", ho.logLimit);
      ho.table = HostOptionInt("bot_hunter_route_table", 0) != 0;  // MAPED4: SM_Map_v5's coordinates
      hunterroute::Configure(ho);
    }
    agents::Install(ao);
    bots2::Configure();  // BOTS2: [host] bot_ai (classic or v2), bot2_tune, bot2_census, bot2_log_limit
  } else {
    Log("agent step off (tick_agents / [host] agents = 0)");
  }
  // D1c-live: the match flow (match.h, director.h); host mode only ([host] match, 1)
  if (HostGet().enabled) {
    match::Options mo;
    mo.enabled = HostOptionInt("match", 1) != 0;
    mo.guardians = HostOptionInt("match_guardians", 1) != 0;
    mo.levels = HostOptionInt("match_levels", 1) != 0;
    mo.wells = HostOptionInt("match_wells", 1) != 0;
    mo.lua = HostOptionInt("match_lua", 1) != 0;
    mo.endOnHp = HostOptionInt("match_end_hp", 1) != 0;
    mo.endHpGraceMs = HostOptionInt("match_end_hp_grace_ms", mo.endHpGraceMs);
    mo.minionRangedPursue = HostOptionInt("minion_ranged_pursue", 1) != 0;  // MINRANGE2
    mo.guardianShield = HostOptionInt("guardian_shield", 1) != 0;  // D14: invincible while a core lives
    mo.descriptors = static_cast<unsigned>(HostOptionInt("match_descriptors", 0x50C));
    mo.gameId = static_cast<unsigned>(HostOptionInt("match_game_id", 0));
    mo.startAfterS = HostOptionInt("match_start_s", 0);
    mo.logLimit = HostOptionInt("match_log_limit", mo.logLimit);
    char v[32];
    // D8-live test fixture: end the match after end_after_s s as if a Guardian had died (end_reason 1 or 3)
    HostOptionString("end_after_s", "0", v, sizeof v);
    mo.endAfterS = std::atof(v);
    mo.endReason = HostOptionInt("end_reason", mo.endReason);
    HostOptionString("end_guardian", "0", v, sizeof v);
    mo.endGuardian = static_cast<std::uint32_t>(std::strtoul(v, nullptr, 0));
    char stats[64];
    HostOptionString("end_stats", "", stats, sizeof stats);  // T5: "K,D,A,creeps,gold,level[,role]" (role: B7c-live)
    mo.endStats = stats;
    mo.director.waves = HostOptionInt("director_waves", 1) != 0 && HostOptionInt("director", 1) != 0;
    mo.director.camps = HostOptionInt("director_camps", 1) != 0 && HostOptionInt("director", 1) != 0;
    mo.director.parasite = HostOptionInt("director_parasite", 1) != 0 && HostOptionInt("director", 1) != 0;
    mo.director.laneTable = HostOptionInt("lane_table", 1) != 0;
    HostOptionString("wave_delay", "-1", v, sizeof v);
    mo.director.waveDelay = std::atof(v);
    HostOptionString("wave_interval", "-1", v, sizeof v);
    mo.director.waveInterval = std::atof(v);
    mo.director.waveLimit = HostOptionInt("wave_limit", 0);
    char filter[128];
    HostOptionString("wave_spawners", "", filter, sizeof filter);
    mo.director.spawnerFilter = filter;
    HostOptionString("formation_spacing", "2.5", v, sizeof v);
    mo.director.formationSpacing = static_cast<float>(std::atof(v));
    HostOptionString("director_trace_s", "5", v, sizeof v);
    mo.director.traceSeconds = std::atof(v);
    mo.director.logLimit = HostOptionInt("director_log_limit", mo.director.logLimit);
    // D1k: a wave's minions one by one (0 = D1c-live's whole wave at once in its formation), the wave shape lines
    mo.director.spawnIntervalS = HostOptionInt("wave_spawn_interval_ms", 500) / 1000.0;
    HostOptionString("wave_shape_trace_s", "1", v, sizeof v);
    mo.director.shapeTraceS = std::atof(v);
    mo.director.shapeWaves = HostOptionInt("wave_shape_waves", mo.director.shapeWaves);
    HostOptionString("wave_shape_for_s", "40", v, sizeof v);
    mo.director.shapeForS = std::atof(v);
    mo.director.campMajorSpot = HostOptionInt("camp_major_spot", 1) != 0;  // D14: a buff major on its scale-2.0 marker
    {
      char py[32];  // NULLPOS: the parasite off the map's origin
      HostOptionString("parasite_offset_y", "-1", py, sizeof py);
      mo.director.parasiteOffsetY = static_cast<float>(std::atof(py));
    }
    mo.director.campIcons = HostOptionInt("camp_icons", 3);  // CAMPICONS: each team's knowledge (director::Configure reads it too)
    crowd::Options co;  // D1k: NavPower's constraint solver for the server space
    co.constraintSolver = HostOptionInt("navpower_constraint_solver", 1) != 0;
    co.campReach = HostOptionInt("camp_reach", 2);  // CAMPREACH(-live): 1 the repulsors, 2 also the auto obstacles
    crowd::Configure(co);
    match::Configure(mo);
    // D6a: the bot players (bots.h)
    bots::Options bo;
    bo.teamSize = HostOptionInt("bots", bo.teamSize);
    bo.allies = HostOptionInt("bots_allies", 1) != 0;
    char classes[256];
    HostOptionString("bot_classes", "", classes, sizeof classes);
    bo.classes = classes;
    bo.lua = HostOptionInt("bots_lua", 1) != 0;
    bo.lanes = HostOptionInt("bot_lanes", 1) != 0;
    bo.ftue = HostOptionInt("bot_ftue", 1) != 0;
    bo.logLimit = HostOptionInt("bot_log_limit", bo.logLimit);
    bots::Configure(bo);
  }
}

bool Ready() {
  // H1-live core_only: no client game at all; the master simulator and the asset catalog are what the server needs
  if (HostGet().enabled && HeadlessCoreOnly()) return *At<void**>(kMasterSimulator) && *At<void**>(kAssetGlobals);
  void* gc = *At<void**>(kGameClient);
  return *At<void**>(kMasterSimulator) && *At<void**>(kAssetGlobals) && gc && Field<void*>(gc, 8);
}

void Drive() {
  unsigned now = LogMs();
  if (!g_firstFrame) {
    g_firstFrame = now ? now : 1;
    Log("first main-thread frame");
    HostMark("first main-thread frame");
    ReadConfig();
    guards::Configure(HostGet().enabled);  // H5: the step guards' [host] keys (step_fail_s, test_throw_*, test_stall_*)
    if (HostGet().enabled) {
      hostctl::Start();             // C1b: dgserver's control channel (only with DGGMS_CONTROL)
      hostwd::Start(g_mainThread);  // H5: the watchdog on this (the engine) thread, armed at the first tick
    }
  }
  // C1b: every frame (createGame, the heartbeat, abortGame, a lost connection); H5: as a guarded step
  guards::Run(guards::kFrameControl, &hostctl::Poll);
  switch (g_phase) {
    case 0: {
      if (now - g_firstFrame < static_cast<unsigned>(g_cfg.startMs)) return;
      static bool waitedLogged;
      if (!Ready()) {
        if (!waitedLogged) Log("waiting for the master simulator, the asset catalog and the client simulator");
        waitedLogged = true;
        return;
      }
      Log("phase 1: create the server simulator");
      HostMark("engine ready (master simulator, asset catalog, client simulator)");
      g_phase = Guarded("create", StepCreate) ? 1 : -1;
      if (g_phase == -1) SetCurrentSim(g_prevSim);
      if (g_phase == 1 && g_cfg.stopAfter < 2) g_phase = 4;
      return;
    }
    case 1: {
      // C1b: the level, the seed and the game id come from createGame
      if (hostctl::Active()) {
        static bool waitLogged;
        if (!hostctl::HasGame()) {
          if (!waitLogged) Log("phase 2 waits for the control channel's createGame%s", hostctl::Warm() ? " (warm host)" : "");
          waitLogged = true;
          // H1-live: an idle (warm) host's footprint, every 10 s
          static unsigned lastIdleStats;
          if (now - lastIdleStats >= 10000) {
            lastIdleStats = now;
            char hs[400], hl[700];
            HostFormatStats(hs, sizeof hs);
            HeadlessFormatStats(hl, sizeof hl);
            Log("stats host: %s, waiting for createGame%s", hs, hl);
          }
          return;
        }
        const auto& game = hostctl::Game();
        if (!game.level.empty())
          std::snprintf(g_cfg.level, sizeof g_cfg.level, "%s%s", game.level.c_str(),
                        game.level.find('.') == std::string::npos ? ".Level" : "");
        g_cfg.seed = static_cast<unsigned>(game.seed);
        match::SetGameId(static_cast<unsigned long long>(game.gameId));
        if (game.descriptors) match::SetDescriptors(static_cast<std::uint32_t>(game.descriptors));  // D8-live
        if (game.fillTeams) bots::SetFillTeams(static_cast<int>(game.fillTeams));  // B10d: the open seats' bots
        bots::SetGameMode(game.mode);  // BOTDRAFT: a custom game's bots draw after the humans
        // HOLIDAY1: the server's level flags (bit 0 = the Halloween decoration's markerset condition): the server
        // simulator loads the level with them and GamePrepareForStart sends them, so every client loads it with them
        if (game.levelFlags) g_cfg.flags = static_cast<unsigned>(game.levelFlags);
        Log("createGame: level %s, seed %u, level flags %08X, game id %lld", g_cfg.level, g_cfg.seed, g_cfg.flags,
            static_cast<long long>(game.gameId));
      }
      Log("phase 2: level %s", g_cfg.level);
      bool ok = WithServerSim("level", StepLevel) && g_level;
      HostMark(ok ? "level loaded" : "level FAILED");
      // D1c-live: the director's level data, the Guardians, the tower table and the wells, before the first tick
      if (ok && match::Enabled()) WithServerSim("match level", MatchLevelStarted);
      Guarded("stats", CountObjects);
      Guarded("stats.lua", CountLua);
      Guarded("transport ids", LogTransportIds);
      Guarded("outgoing", LogOutgoing);
      LogSimFields("after StartLevel");
      Log("lua threads after StartLevel: %d; asserts server %u, lua errors server %u", g_luaThreads, g_assertsServer, g_luaErrsServer);
      if (g_cfg.directAbility) {
        std::strcpy(g_luaBuf, "dgsim_direct = true return 'dgsim_direct set'");
        g_luaName = "direct flag";
        WithServerSim("direct flag", RunLoadedLua);
      }
      if (g_cfg.luaProbes) RunLuaFile("dgsim_start.lua");
      else Log("lua probes off (host mode, [host] lua_probes = 0): no dgsim_start.lua, no dgsim_probe.lua");
      if (g_cfg.directAbility && g_cfg.luaProbes) WithServerSim("direct ability", DirectAbility);
      LogHisto();
      if (!ok) {
        if (hostctl::Active()) {
          // PORTSTUCK: say why before exit code 2, so dgserver does not take it for a port it should quarantine
          hostctl::ReportError(dggms::control::kErrLevelLoad, "level load: the level did not load", true);
          hostctl::Exit(2, "the level did not load");
        }
        g_phase = -1;
        return;
      }
      // G2: the level is loaded, so the host serves its GMS port now (ADR-2's gameReady point)
      if (HostGet().enabled) {
        const bool bound = GmsHostStart(g_sim, g_level, g_cfg.level, g_cfg.seed, g_cfg.flags);
        // C1b: gameReady {port} once the level is loaded and the port listens
        if (hostctl::Active()) {
          if (!bound) {
            // PORTSTUCK: the port could not be bound (or DGGMS_BIND is bad): dgserver quarantines the port and
            // starts the game once more on another (older hosts: the bare exit code 2 means the same to it)
            hostctl::ReportError(dggms::control::kErrGmsEndpoint, std::string("GMS endpoint: ") + GmsHostError(), true);
            hostctl::Exit(2, "no GMS endpoint (DGGMS_BIND)");
          }
          hostctl::Ready(static_cast<int>(GmsHostPort()));
        }
        HeadlessAfterLevel();  // H1-live: [host] laa_reserve (the low-memory reservation test)
      }
      char* s = static_cast<char*>(g_sim);
      if (g_cfg.setRunning && !Field<unsigned char>(s, sim::kRunning)) {
        Log("+0x14 is 0 after StartLevel; setting it to 1 (set_running = 1)");
        Field<unsigned char>(s, sim::kRunning) = 1;
      }
      g_phase = g_cfg.stopAfter < 3 ? 4 : 2;
      return;
    }
    case 2:
      Log("phase 3: ticking every %d ms for %d ms%s", g_cfg.tickMs, g_cfg.runMs, g_cfg.runMs > 0 ? "" : " (no limit)");
      perf::Configure(HostGet().enabled, g_cfg.tickMs, HostGet().dataDir);  // Q3c: timing per match minute
      perf::AddSource(EngineGauges);
      HostMark("ticking");
      g_tickStart = g_lastStats = g_lastHisto = g_lastProbe = now;
      g_nextTickMs = PreciseMs();
      g_phase = 3;
      return;
    case 3:
      {
        // one tick per frame at most, every tick_ms on average; after a stall the schedule restarts instead of catching up
        double p = PreciseMs();
        if (p >= g_nextTickMs) {
          perf::TickBegin(p - g_nextTickMs);  // Q3c: how late this tick starts against its schedule
          g_nextTickMs = (p - g_nextTickMs > 4.0 * g_cfg.tickMs) ? p + g_cfg.tickMs : g_nextTickMs + g_cfg.tickMs;
          // H1-live core_only: the engine frame (game clock, NavPower, ...) that MainLoop_RunFrame ran before each tick
          if (HostGet().enabled && HeadlessCoreOnly()) {
            HeadlessEngineFrame();
            perf::EngineFrameDone();
          }
          ServerTick();
        }
      }
      if (now - g_lastStats >= static_cast<unsigned>(g_cfg.statsMs)) {
        bool histo = now - g_lastHisto >= static_cast<unsigned>(g_cfg.histoMs);
        if (histo) g_lastHisto = now;
        g_lastStats = now;
        GuardedStats(now, histo);
      }
      if (g_cfg.luaProbes && now - g_lastProbe >= static_cast<unsigned>(g_cfg.probeMs)) {
        g_lastProbe = now;
        RunLuaFile("dgsim_probe.lua");
      }
      if (g_cfg.runMs > 0 && now - g_tickStart >= static_cast<unsigned>(g_cfg.runMs)) {
        GuardedStats(now, true);
        LogSimFields("at the end");
        Guarded("outgoing", LogOutgoing);
        Log("phase 3 done after %u ticks; the server simulator stays allocated, nothing ticks it any more", g_ticks);
        g_phase = 4;
        hostwd::Disarm();  // H5: no ticks any more, none expected
      }
      return;
    default:
      return;
  }
}

void OnFrame(bool messagePending) {
  if (GetCurrentThreadId() != g_mainThread) {
    if (++g_otherThreadPeeks == 1) Log("PeekMessage from another thread (%lu); ignored", GetCurrentThreadId());
    return;
  }
  if (g_inFrame) return;
  g_inFrame = true;
  // H1-live core_only: the engine's own frame does nothing, so the host's loop (host/headless.h) runs the engine work
  // the server needs before the frame and sleeps until the next server tick after it; once per pump, at the
  // PeekMessage that finds the queue empty
  const bool loopStep = !messagePending && HostGet().enabled && HeadlessCoreOnly();
  if (loopStep) HeadlessFrameBegin(g_phase == 3);
  Drive();
  guards::AfterFrame();  // H5: a step that threw in every run for step_fail_s ends the host here (exit code 7)
  if (loopStep) HeadlessFrameEnd(g_phase == 3 ? g_nextTickMs - PreciseMs() : -1.0);
  g_inFrame = false;
}

// WSOCK32!bind: the client binds its sockets to INADDR_ANY (DirtySDK's UDP 3659 at startup, RakNet's
// game socket after login), which makes Windows Firewall ask about every new Dawngate.exe path.
// This copy only ever talks to 127.0.0.1, so every IPv4 wildcard bind is narrowed to loopback.
using BindFn = int(WINAPI*)(UINT_PTR s, void* name, int namelen);
BindFn g_bind;
int WINAPI BindHook(UINT_PTR s, void* name, int namelen) {
  unsigned char* a = static_cast<unsigned char*>(name);
  if (a && namelen >= 8 && *reinterpret_cast<unsigned short*>(a) == 2 /* AF_INET */) {
    unsigned port = (a[2] << 8) | a[3];
    unsigned& addr = *reinterpret_cast<unsigned*>(a + 4);
    if (addr == 0) {
      addr = 0x0100007F;  // 127.0.0.1, network byte order
      Log("bind: socket %u to 0.0.0.0:%u narrowed to 127.0.0.1:%u (call at %08lX)", static_cast<unsigned>(s), port, port,
          reinterpret_cast<DWORD>(_ReturnAddress()));
    } else {
      Log("bind: socket %u to %u.%u.%u.%u:%u", static_cast<unsigned>(s), a[4], a[5], a[6], a[7], port);
    }
  }
  return g_bind(s, name, namelen);
}

using PeekFn = BOOL(WINAPI*)(LPMSG, HWND, UINT, UINT, UINT);
PeekFn g_peekW, g_peekA;
BOOL WINAPI PeekW(LPMSG m, HWND h, UINT a, UINT b, UINT r) {
  BOOL res = g_peekW(m, h, a, b, r);
  OnFrame(res != FALSE);
  return res;
}
BOOL WINAPI PeekA(LPMSG m, HWND h, UINT a, UINT b, UINT r) {
  BOOL res = g_peekA(m, h, a, b, r);
  OnFrame(res != FALSE);
  return res;
}

}  // namespace

void ExperimentInstall(const char* gameDir, unsigned long mainThreadId) {
  g_mainThread = mainThreadId;
  std::snprintf(g_ini, sizeof g_ini, "%s\\dgsim.ini", gameDir);
  std::snprintf(g_gameDir, sizeof g_gameDir, "%s", gameDir);
  // Before anything else (sockets are created long before the first frame): loopback-only binds.
  if (GetPrivateProfileIntA("dgsim", "loopback_bind", 1, g_ini)) {
    g_bind = reinterpret_cast<BindFn>(HookIat("WSOCK32.dll", "bind", reinterpret_cast<void*>(&BindHook), 2 /* WSOCK32 ordinal of bind */));
    Log("bind hook (wildcard -> 127.0.0.1) %s", g_bind ? "installed" : "FAILED");
  }
  // Build check: InitializeClientGameSimulator's prologue (push -1; push 0x010A3D2A).
  static const unsigned char kInitClientSim[] = {0x6A, 0xFF, 0x68, 0x2A, 0x3D, 0x0A, 0x01};
  if (std::memcmp(reinterpret_cast<void*>(0x008F5650), kInitClientSim, sizeof kInitClientSim) != 0) {
    Log("not build 1.0.903.854 (InitializeClientGameSimulator bytes differ); harness disabled");
    return;
  }
  // D12: how many assert and ability request lines are logged ([dgsim], overridden by a host run's [host] keys)
  g_cfg.assertLogLimit = GetPrivateProfileIntA("dgsim", "assert_log_limit", g_cfg.assertLogLimit, g_ini);
  g_cfg.requestLogLimit = GetPrivateProfileIntA("dgsim", "ability_request_log_limit", g_cfg.requestLogLimit, g_ini);
  if (HostGet().enabled) {
    g_cfg.assertLogLimit = HostOptionInt("assert_log_limit", g_cfg.assertLogLimit);
    g_cfg.requestLogLimit = HostOptionInt("ability_request_log_limit", g_cfg.requestLogLimit);
  }
  g_origAssert = reinterpret_cast<AssertFn>(HookDetour(kReportAssertFailure, kReportAssertFailureBytes,
                                                       sizeof kReportAssertFailureBytes, reinterpret_cast<void*>(&AssertHook)));
  Log("assert hook %s", g_origAssert ? "installed" : "FAILED");
  if (!g_origAssert) return;
  g_origLuaError = reinterpret_cast<LuaErrorFn>(HookDetour(kLuaThreadError, kLuaThreadErrorBytes, sizeof kLuaThreadErrorBytes,
                                                           reinterpret_cast<void*>(&LuaErrorHook)));
  Log("Lua error hook %s", g_origLuaError ? "installed" : "FAILED");
  if (!g_origLuaError) return;
  g_origRequestAbility = reinterpret_cast<RequestAbilityFn>(HookDetour(
      kRequestAbility, kRequestAbilityBytes, sizeof kRequestAbilityBytes, reinterpret_cast<void*>(&RequestAbilityHook)));
  g_origValidateAbility = reinterpret_cast<ValidateAbilityFn>(HookDetour(
      kValidateAbility, kValidateAbilityBytes, sizeof kValidateAbilityBytes, reinterpret_cast<void*>(&ValidateAbilityHook)));
  Log("ability hooks: request %s, validate %s", g_origRequestAbility ? "installed" : "FAILED",
      g_origValidateAbility ? "installed" : "FAILED");
  if (!g_origRequestAbility || !g_origValidateAbility) return;
  // D1a-live: hand prepared ability/modifier Ticks to the scheduler and end finished instances.
  if (GetPrivateProfileIntA("dgsim", "ability_tick", 1, g_ini)) {
    AbilityTickOptions o;
    o.endNoTick = GetPrivateProfileIntA("dgsim", "end_no_tick", o.endNoTick, g_ini);
    o.logLimit = GetPrivateProfileIntA("dgsim", "ability_log_limit", o.logLimit, g_ini);
    o.modifierExpiry = GetPrivateProfileIntA("dgsim", "modifier_expiry", o.modifierExpiry, g_ini);
    o.holdRunning = GetPrivateProfileIntA("dgsim", "ability_hold_running", o.holdRunning, g_ini);  // D10
    o.endTrace = GetPrivateProfileIntA("dgsim", "ability_end_trace", o.endTrace, g_ini);         // D6c
    o.luaContext = GetPrivateProfileIntA("dgsim", "lua_context", o.luaContext, g_ini);         // D12
    o.upkeep = GetPrivateProfileIntA("dgsim", "ability_upkeep", o.upkeep, g_ini);              // DISCIPLINE
    if (HostGet().enabled) {  // D12: a host run may override these (DGGMS_OPTIONS)
      o.logLimit = HostOptionInt("ability_log_limit", o.logLimit);
      o.luaContext = HostOptionInt("lua_context", o.luaContext);
      o.upkeep = HostOptionInt("ability_upkeep", o.upkeep);
    }
    g_abilityTick = AbilityTickInstall(o);
  } else {
    Log("ability tick hooks off (ability_tick = 0)");
  }
  // D1a-live: the client build has no TakeDamage body. Since D1f-live the detour runs DAMAGE.md's pipeline
  // (damage.cpp) and the death module (death.cpp) does what follows a death; their switches are [dgsim] keys that a
  // host run can override like [host] keys (DGGMS_OPTIONS).
  auto opt = [](const char* key, int def) {
    const int v = GetPrivateProfileIntA("dgsim", key, def, g_ini);
    return HostGet().enabled ? HostOptionInt(key, v) : v;
  };
  if (opt("damage", opt("placeholder_damage", 1))) {
    DamageOptions d;
    d.logLimit = opt("damage_log_limit", d.logLimit);
    d.absorb = opt("damage_absorb", 1) != 0;
    d.reductions = opt("damage_reductions", 1) != 0;
    d.lifedrain = opt("damage_lifedrain", 1) != 0;
    d.overload = opt("damage_overload", 1) != 0;
    d.shields = opt("damage_shields", 1) != 0;
    d.reportZero = opt("damage_report_zero", 0) != 0;
    d.eventOwnInstance = opt("damage_event_instance", 1) != 0;
    g_damage = DamageInstall(d);
  } else {
    Log("damage hook off (damage = 0)");
  }
  matchstats::InstallHealHook();  // MSTATS3: healing by source ([host] match_stats)
  {
    death::Options o;
    o.enabled = opt("death", 1) != 0;
    o.nearby = opt("death_nearby", 1) != 0;
    o.rewards = opt("death_rewards", 1) != 0;
    o.goldHook = opt("death_gold", 1) != 0;
    o.killHook = opt("death_kill_hook", 1) != 0;
    o.interrupt = opt("death_interrupt", 1) != 0;
    o.interruptReleased = opt("death_interrupt_released", 0) != 0;  // D6c
    o.endRuns = opt("death_end_runs", o.endRuns);
    o.startGold = opt("gold_start", 1) != 0;
    o.passiveGold = opt("gold_passive", 1) != 0;
    o.logLimit = opt("death_log_limit", o.logLimit);
    o.respawn = opt("respawn", 1) != 0;                          // D1g
    o.respawnSeconds = static_cast<float>(opt("respawn_s", 0));  // D1g: 0 = LevelGameplayTuning's formula
    o.keepItemPassives = opt("death_keep_item_passives", 0) != 0;  // Q3's rule, off since ITEMDEATH
    o.itemPassiveRespawn = opt("item_passive_respawn", 1) != 0;   // ITEMDEATH (itempassives.h)
    Log("death: item passives (ITEMDEATH): %s",
        o.keepItemPassives    ? "kept through the death (death_keep_item_passives=1, Q3's rule)"
        : o.itemPassiveRespawn ? "those that end OnAgentDeath end with the death, the slots' dead ids are cleared, "
                                 "activated again at the respawn"
                               : "those that end OnAgentDeath end with the death and stay gone, the slots keep the dead "
                                 "ids (item_passive_respawn=0)");
    o.respawnFeedback = opt("respawn_feedback", 1) != 0;         // D11: PlayerRespawned at the respawn
    o.rewardBuffs = opt("reward_buffs", 1) != 0;                 // D14: the jungle buffs, the parasite's team buffs
    o.roleGold = opt("role_passives", 1) != 0;                   // ROLEVIM: the roles' minion/creature gold modifiers
    o.roleGoldBonus = opt("role_gold_bonus", 1) != 0;            // VIMPOPUP: the role's part as the grant's bonus
    death::Install(o);
  }
  {  // D1j: the ability data containers' release at an object's destroy, and the server pools' census (components.h)
    components::Options o;
    o.release = opt("container_release", 1) != 0;
    o.endInstances = opt("container_end_instances", 1) != 0;
    o.freeHeap = opt("container_free_heap", 1) != 0;
    o.poolStats = opt("pool_stats", 1) != 0;
    o.logLimit = opt("container_log_limit", o.logLimit);
    components::Install(o);
  }
  {  // D12c: attached trigger volumes follow their objects (volumes.h)
    volumes::Options o;
    o.follow = opt("volume_follow", 1) != 0;
    o.logLimit = opt("volume_log_limit", o.logLimit);
    volumes::Install(o);
  }
  {  // FOGSTACKS: an ended modifier's attached effects are destroyed (effects.h)
    effects::Options o;
    o.endWithModifier = opt("modifier_effects_end", 1) != 0;
    o.logLimit = opt("modifier_effects_log_limit", o.logLimit);
    effects::Install(o);
  }
  {  // D1i: health and resource regeneration (regen.h)
    regen::Options o;
    o.enabled = opt("regen", 1) != 0;
    o.health = opt("regen_health", 1) != 0;
    o.resource = opt("regen_resource", 1) != 0;
    o.stepMs = opt("regen_step_ms", o.stepMs);
    o.logSeconds = opt("regen_log_s", 5);
    o.logLimit = opt("regen_log_limit", o.logLimit);
    regen::Install(o);
  }
  g_peekW = reinterpret_cast<PeekFn>(HookIat("USER32.dll", "PeekMessageW", reinterpret_cast<void*>(&PeekW)));
  g_peekA = reinterpret_cast<PeekFn>(HookIat("USER32.dll", "PeekMessageA", reinterpret_cast<void*>(&PeekA)));
  Log("frame hooks: PeekMessageW %s, PeekMessageA %s", g_peekW ? "ok" : "missing", g_peekA ? "ok" : "missing");
}
