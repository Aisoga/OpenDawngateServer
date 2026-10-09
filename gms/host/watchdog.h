// The match host's watchdog (roadmap H5; docs/SERVER_MODE.md *A frozen host: the watchdog and the step guards (H5)*,
// docs/HOST_CONTROL.md *A frozen host (H5)*).
//
// A host whose server tick stops (a hung Lua call, a deadlock, an endless loop) looks to its players like the
// "soft crash" testers of another server reported: the world stands still while the client's own prediction still
// walks the shaper. Before H5 only dgserver's heartbeat timeout (30 s, once the engine thread no longer polled the
// control channel) would have ended such a match, with no word on where the host hung. The watchdog is a thread of
// its own (gms/core/watchdog's Monitor) that watches the engine thread's tick counter; once the counter has stood still
// for [host] watchdog_s (10; 0 = off) seconds it
//   1. logs the stall (the step the engine thread was in, its registers and the return addresses on its stack) and
//      writes <data dir>\watchdog.txt with that and the last log lines;
//   2. writes the minidump <data dir>\watchdog.dmp (dbghelp from System32; [host] watchdog_dump: 1 = the threads'
//      stacks and the memory they point to, the default; 2 = all memory; 0 = none), with the engine thread as its
//      exception thread (re/scripts/minidump_stack.py reads it);
//   3. reports the stall to dgserver: reportGMSError {code 2 "tick stalled", text, fatal true}, then logoutGMS;
//   4. ends the process with exit code 6, after the usual "host: exiting" and "host: control" lines.
// dgserver then ends the match as aborted (Outcome.Error; the clients get NotifyPlayerRemoved GAME_DESTROYED and go
// back to the main menu) and frees the GMS port, so the next game can start. Nothing of this runs on the engine thread
// (it may hold any lock): the monitor thread logs with a time limit, writes the files, sends on the control channel
// only when its lock is free within 2 s, and never calls the engine. With a debugger attached the stall is only
// logged. Host mode only; the harness has no watchdog.
#pragma once

namespace hostwd {

// Host mode, the first main-thread frame: reads the [host] keys and starts the monitor thread watching
// `engineThreadId` (the thread that runs the server tick). It arms itself at the first Beat.
void Start(unsigned long engineThreadId);
bool Active();
// The engine thread, after every server tick: the tick counter and the match clock (match seconds, for the report).
void Beat(unsigned ticks, double matchSeconds);
// The engine thread: the step it enters (a string literal: "tick.lua", "frame.stats", "test.stall"); nullptr when it
// leaves it. Returns the step it was in before.
const char* Enter(const char* step);
// No ticks are expected until the next Beat (the harness's run_ms end).
void Disarm();
// "watchdog 10 s (armed, ticks N), dump small" for a stats line; "watchdog off" when it doesn't run.
void Describe(char* out, unsigned size);

}  // namespace hostwd
