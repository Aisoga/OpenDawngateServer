// The match host's step guards (roadmap H5; docs/SERVER_MODE.md *A frozen host: the watchdog and the step guards
// (H5)*).
//
// Every step of the host's frame runs under SEH here: the server tick's sixteen parts (experiment.cpp ServerTick),
// the core-only engine frame's five pieces (startup.cpp CoreFrame) and the rest of the frame (the stats lines, the
// control channel's poll). Before H5 a tick part or a core-frame piece that threw once was switched off for the rest
// of the process: one exception in the Lua scheduler, the locomotion or the game clock froze the world while the host
// went on answering its heartbeats, the "soft crash". Now (gms/core/watchdog/rules.h StepGuards):
//   - an exception is counted, logged (the first 20 of a step, then every 100th; the first 3 in full with registers
//     and return addresses) and skips only that run of the step; the next run goes ahead;
//   - the 1st, 10th, 100th, ... exception of a step is reported to dgserver (reportGMSError stepException, not fatal);
//   - a step that has thrown in every run for [host] step_fail_s (default watchdog_s, 10; at least 10 runs in a row;
//     0 = never) fails persistently: in host mode the host reports it (reportGMSError stepFailing, fatal) and exits
//     with code 7 after the frame, so dgserver ends the match as aborted; the harness switches the step off instead.
// The watchdog (host/watchdog.h) sees each step as it runs (its marker), so a stall names the step.
// Test keys ([host], match seconds: the clock starts at the first EnterGame, or [host] match_start_s without one):
//   test_throw_at=<s>      an access violation in one step from then on ...
//   test_throw_step=<name> ... in this step (default lua; the names below, with or without "tick."/"frame.") ...
//   test_throw_ticks=<n>   ... in n runs in a row (default 1; -1 = every run: the persistent failure)
//   test_stall_at=<s>      the engine thread stops at the start of a server tick (Sleep) ...
//   test_stall_for_s=<s>   ... for this long (default 0 = for ever: the watchdog ends the host).
#pragma once

#include <cstddef>

namespace guards {

enum Step : int {
  // the server tick's parts, in experiment.cpp's old kTickNames order
  kTickTime = 0,
  kTickLua,
  kTickLocomotion,
  kTickProjectiles,
  kTickWorld,
  kTickDelete,
  kTickFow,
  kTickAgents,
  kTickAbilities,
  kTickGms,
  kTickCommands,
  kTickGmsOut,
  kTickNetworkRate,
  kTickMatch,
  kTickRegen,
  kTickBots,
  // the core-only engine frame (startup.cpp CoreFrame)
  kFrameLoader,
  kFrameDispatch,
  kFrameClock,
  kFrameNavPower,
  kFrameUpdateList,
  // the rest of the host's frame (experiment.cpp Drive)
  kFrameStats,
  kFrameControl,
  kStepCount
};

// Reads the [host] keys (host mode) or keeps the defaults (harness: a persistently failing step is switched off).
void Configure(bool hostMode);
// "tick.lua", "frame.clock", "frame.stats", ...
const char* Name(int step);
// A step by its name, with or without its "tick."/"frame." prefix; -1 if there is none.
int Find(const char* name);

using StepFn = void (*)();
// Runs fn under SEH as `step` (the watchdog's marker meanwhile). True = it ran through; false = it threw (counted,
// logged, skipped) or the step is switched off.
bool Run(int step, StepFn fn);
bool Disabled(int step);

// The start of a server tick (the server simulator bound): the test stall (test_stall_at).
void TickStart();
// After the host's frame: a persistent failure ends the host (host mode: reportGMSError stepFailing, exit code 7).
void AfterFrame();

// "step exceptions: lua 3 (2 in a row), gms 1" or "step exceptions: none" for a stats line.
void FormatStats(char* buf, std::size_t size);

}  // namespace guards
