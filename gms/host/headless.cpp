// The headless host, stage 2 (H1-live): see headless.h.
#include "headless.h"

#include <windows.h>

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>

#include "engine/log.h"
#include "engine/startup.h"
#include "host.h"

namespace {

bool g_any;  // some stage-2 piece is on (the stats line says so)
int g_reserveMb = -1;  // [host] laa_reserve: -1 off, else the margin in MB left free below 2 GB
int g_reserveWhen;     // 1 = after the level load, 2 = at once (DllMain: everything the engine allocates lands high)
unsigned g_reservedMb, g_reservedBlocks;
bool g_highMemory;  // the process can address above 2 GB (a LARGE_ADDRESS_AWARE exe on 64-bit Windows)
unsigned long long g_frames, g_engineFrames, g_sleeps, g_sleptMs;

constexpr std::uintptr_t k2Gb = 0x80000000u;

void ReserveLow(const char* when);  // the low-memory reservation ([host] laa_reserve, below)

// Address space in use (reserved or committed) at and above 2 GB, in MB.
unsigned HighInUseMb() {
  unsigned long long used = 0;
  MEMORY_BASIC_INFORMATION mbi;
  for (std::uintptr_t a = k2Gb; VirtualQuery(reinterpret_cast<void*>(a), &mbi, sizeof mbi) == sizeof mbi;) {
    if (mbi.State != MEM_FREE) used += mbi.RegionSize;
    const std::uintptr_t next = reinterpret_cast<std::uintptr_t>(mbi.BaseAddress) + mbi.RegionSize;
    if (next <= a) break;
    a = next;
  }
  return static_cast<unsigned>(used / (1024 * 1024));
}

}  // namespace

void HeadlessInstall() {
  startup::Options o;
  o.marks = HostOptionInt("startup_marks", 0) != 0;
  o.nobake = HostOptionInt("nobake", 0) != 0;
  o.nosound = HostOptionInt("nosound", 0) != 0;
  char v[32];
  HostOptionString("preload", "1", v, sizeof v);
  o.preload = _stricmp(v, "server") == 0 ? 2 : static_cast<int>(std::strtol(v, nullptr, 0));
  if (o.preload < 0 || o.preload > 2) o.preload = 1;
  // 1 = no D3D9 device either (0x006B26C8), 2 = H1-static's 0x006B279E, 0 = stage 1 (the full client start-up). On by
  // default since H1b (docs/SERVER_MODE.md *The headless host as the default (H1b)*); core_only=0 is the way back.
  o.coreOnly = HostOptionInt("core_only", 1);
  if (o.coreOnly < 0 || o.coreOnly > 2) o.coreOnly = 1;
  g_reserveWhen = HostOptionInt("laa_reserve", 0);
  g_reserveMb = g_reserveWhen ? HostOptionInt("laa_reserve_margin_mb", 32) : -1;
  SYSTEM_INFO si;
  GetSystemInfo(&si);
  g_highMemory = reinterpret_cast<std::uintptr_t>(si.lpMaximumApplicationAddress) > k2Gb;
  g_any = o.marks || o.nobake || o.nosound || o.preload != 1 || o.coreOnly || g_reserveMb >= 0;
  Log("host: stage 2 (H1-live): startup_marks %d, nobake %d, nosound %d, preload %d, core_only %d, laa_reserve %s; "
      "the process addresses up to %p (%s)",
      o.marks, o.nobake, o.nosound, o.preload, o.coreOnly, g_reserveMb >= 0 ? "on" : "off", si.lpMaximumApplicationAddress,
      g_highMemory ? "LARGE_ADDRESS_AWARE" : "2 GB");
  if (g_any) startup::Install(o, &HostMark);
  if (g_reserveWhen == 2) ReserveLow("at start-up");  // before the game's main: the engine's heaps, Lua, the level
}

bool HeadlessCoreOnly() { return startup::CoreOnly(); }

namespace {

LARGE_INTEGER g_freq, g_lastEngineFrame;

// Milliseconds since the last engine frame (33.3 before the first).
double MsSinceEngineFrame(LARGE_INTEGER* now) {
  if (!g_freq.QuadPart) QueryPerformanceFrequency(&g_freq);
  QueryPerformanceCounter(now);
  if (!g_lastEngineFrame.QuadPart) return 33.3;
  return static_cast<double>(now->QuadPart - g_lastEngineFrame.QuadPart) * 1000.0 / static_cast<double>(g_freq.QuadPart);
}

}  // namespace

void HeadlessEngineFrame() {
  LARGE_INTEGER now;
  const double ms = MsSinceEngineFrame(&now);
  g_lastEngineFrame = now;
  startup::CoreFrame(static_cast<float>((ms > 333.0 ? 333.0 : ms) / 1000.0));
  ++g_engineFrames;
}

void HeadlessFrameBegin(bool ticking) {
  ++g_frames;
  // Ticking: the engine frame runs right before each server tick (HeadlessEngineFrame from the harness), as in the
  // stage-1 host (one frame per tick at 30 frames/s). Before the ticks start: every 33 ms.
  if (ticking) return;
  LARGE_INTEGER now;
  if (g_lastEngineFrame.QuadPart && MsSinceEngineFrame(&now) < 33.0) return;
  HeadlessEngineFrame();
}

void HeadlessFrameEnd(double msUntilNextTick) {
  // Not ticking yet (start-up, waiting for createGame): 10 ms frames keep the control channel and the GMS responsive.
  double ms = msUntilNextTick < 0 ? 10.0 : msUntilNextTick;
  if (ms > 50.0) ms = 50.0;
  if (ms > 0.0) {
    const DWORD d = static_cast<DWORD>(ms) + 1;  // a millisecond late rather than a busy wait for the rest
    Sleep(d);
    ++g_sleeps;
    g_sleptMs += d;
  }
}

void HeadlessAfterLevel() {
  if (g_reserveWhen == 1) ReserveLow("after the level load");
}

namespace {

void ReserveLow(const char* when) {
  if (g_reserveMb < 0) return;
  if (!g_highMemory) {
    Log("host: laa_reserve: the process can't address above 2 GB (no LARGE_ADDRESS_AWARE: setup_server_copy.py --laa); "
        "nothing reserved");
    return;
  }
  // the free blocks below 2 GB, lowest first; the highest ones up to the margin stay free
  struct Block {
    std::uintptr_t base;
    std::size_t size;
  };
  static Block blocks[4096];
  int n = 0;
  unsigned long long freeTotal = 0;
  MEMORY_BASIC_INFORMATION mbi;
  for (std::uintptr_t a = 0x10000; a < k2Gb && VirtualQuery(reinterpret_cast<void*>(a), &mbi, sizeof mbi) == sizeof mbi;) {
    const std::uintptr_t base = reinterpret_cast<std::uintptr_t>(mbi.BaseAddress);
    std::size_t size = mbi.RegionSize;
    if (base + size > k2Gb) size = k2Gb - base;
    if (mbi.State == MEM_FREE && n < 4096) {
      // reservations start on 64 KiB boundaries
      const std::uintptr_t start = (base + 0xFFFF) & ~static_cast<std::uintptr_t>(0xFFFF);
      if (start < base + size && base + size - start >= 0x10000) {
        blocks[n++] = {start, (base + size - start) & ~static_cast<std::size_t>(0xFFFF)};
        freeTotal += blocks[n - 1].size;
      }
    }
    if (base + mbi.RegionSize <= a) break;
    a = base + mbi.RegionSize;
  }
  const unsigned long long margin = static_cast<unsigned long long>(g_reserveMb) * 1024 * 1024;
  unsigned long long left = freeTotal;
  for (int i = 0; i < n && left > margin; ++i) {
    std::size_t take = blocks[i].size;
    if (left - take < margin) take = static_cast<std::size_t>((left - margin) & ~0xFFFFull);
    if (!take) break;
    if (VirtualAlloc(reinterpret_cast<void*>(blocks[i].base), take, MEM_RESERVE, PAGE_NOACCESS)) {
      g_reservedMb += static_cast<unsigned>(take / (1024 * 1024));
      ++g_reservedBlocks;
      left -= take;
    }
  }
  Log("host: laa_reserve %s: %u MB in %u blocks below 2 GB reserved (of %llu MB free; margin %d MB); what the host "
      "allocates from now on lands above 2 GB; in use above 2 GB now %u MB",
      when, g_reservedMb, g_reservedBlocks, freeTotal / (1024 * 1024), g_reserveMb, HighInUseMb());
}

}  // namespace

void HeadlessFormatStats(char* buf, std::size_t size) {
  buf[0] = 0;
  if (!g_any) return;
  char st[400];
  startup::Describe(st, sizeof st);
  int len = std::snprintf(buf, size, ", headless: %s", st);
  if (len > 0 && static_cast<std::size_t>(len) < size && startup::CoreOnly())
    len += std::snprintf(buf + len, size - len, ", loop frames %llu (engine frames %llu), sleeps %llu (%llu ms)", g_frames,
                         g_engineFrames, g_sleeps, g_sleptMs);
  if (len > 0 && static_cast<std::size_t>(len) < size && g_highMemory)
    std::snprintf(buf + len, size - len, ", above 2 GB in use %u MB (reserved below: %u MB)", HighInUseMb(), g_reservedMb);
}
