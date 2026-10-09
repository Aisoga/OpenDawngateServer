// The headless host, stage 2 (roadmap H1-live; ADR-2 decision 2; docs/SERVER_MODE.md *Headless host, stage 2
// (H1-live)*): the DLL shell's part.
//
// - The [host] keys of the start-up changes in engine/startup.h (startup_marks, nobake, nosound, preload, core_only;
//   host mode only, overridable through DGGMS_OPTIONS), installed from HostInstall. core_only is 1 by default since
//   H1b (the hosting default; core_only=0 = stage 1), the others are off (stage 1's fallbacks and test aids).
// - Stage 2's main loop: with core_only the engine's own frame (MainLoop_RunFrame) does nothing, so the shipped loop
//   would spin on the window manager's pump. Around the harness's frame (the PeekMessage hook in engine/experiment.cpp)
//   the host runs the per-frame engine work the server needs (HeadlessFrameBegin) and then sleeps until the next
//   server tick (HeadlessFrameEnd).
// - The low-memory reservation test for a LARGE_ADDRESS_AWARE host copy ([host] laa_reserve, step 4): every free block
//   below 2 GB but a margin (laa_reserve_margin_mb, 32) is reserved, so what the host allocates afterwards lands above
//   2 GB: laa_reserve = 1 after the level load, 2 at once (DllMain, before the game's main: the engine's heaps, the
//   Lua heap, NavPower and the level all above 2 GB).
#pragma once

#include <cstddef>

// Reads the keys and installs engine/startup.h's pieces (HostInstall, DllMain time).
void HeadlessInstall();
// core_only is installed (step 3).
bool HeadlessCoreOnly();
// Core-only mode, around the harness's frame: before it the engine work while the server doesn't tick yet (every
// 33 ms), after it the sleep until the next tick (msUntilNextTick < 0: not ticking yet). Once it ticks, the harness
// runs HeadlessEngineFrame right before each server tick (the engine's frame work with the elapsed dt).
void HeadlessFrameBegin(bool ticking);
void HeadlessFrameEnd(double msUntilNextTick);
void HeadlessEngineFrame();
// Once the server's level is loaded ([host] laa_reserve).
void HeadlessAfterLevel();
// ", headless: ..." for the stats host line ("" when nothing of stage 2 is on).
void HeadlessFormatStats(char* buf, std::size_t size);
