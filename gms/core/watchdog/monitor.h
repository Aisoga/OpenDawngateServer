// The watchdog thread (roadmap H5; docs/SERVER_MODE.md *A frozen host: the watchdog and the step guards (H5)*).
//
// A Monitor watches one thread (the host's engine thread, which runs the server tick): the engine thread reports each
// tick (Beat) and the step it is in (Enter); the monitor's own thread polls the tick counter every pollMs with
// rules.h's StallDetector. When the counter has stood still for thresholdMs it samples the engine thread (suspended
// for a moment: its registers and the top of its stack, copied without the heap or any lock) and calls OnStall on the
// monitor's thread. The host's OnStall (gms/host/watchdog.cpp) logs it, writes a minidump (WriteDump), reports the
// stall to dgserver and ends the process.
//
// Also here, as they need no game address either: the stack scan (CodeRanges: the executable sections of loaded
// modules from their PE headers in memory, and a call instruction before each candidate) and the minidump (dbghelp
// from System32, loaded ahead with LoadDbgHelp so the stall doesn't take the loader lock, written on a worker thread
// with a time limit). The dump names the sampled thread as its exception thread (code kStallExceptionCode at the
// thread's EIP, the sampled context), so re/scripts/minidump_stack.py reads a stall dump like a crash dump.
// Engine-free, Windows only; gms_tests (tests/test_watchdog.cpp) runs a real monitor against a test thread.
#pragma once

#include <windows.h>

#include <atomic>
#include <cstdint>
#include <functional>
#include <string>
#include <vector>

#include "core/watchdog/rules.h"

namespace gms {
namespace watchdog {

// The exception code of a stall dump: no real exception, the watchdog's mark ("0057A11", stall).
constexpr std::uint32_t kStallExceptionCode = 0xE0057A11;

// One thread's registers and the top of its stack.
struct ThreadSample {
  static constexpr int kMaxStack = 512;  // dwords from esp up (2 KiB)
  bool ok = false;
  std::uint32_t threadId = 0;
  CONTEXT context{};
  std::uint32_t stack[kMaxStack]{};
  int stackDwords = 0;

  std::uint32_t Eip() const { return static_cast<std::uint32_t>(context.Eip); }
  std::uint32_t Esp() const { return static_cast<std::uint32_t>(context.Esp); }
  std::uint32_t Ebp() const { return static_cast<std::uint32_t>(context.Ebp); }
};

// Suspends `thread` (THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT), copies its full context and up to kMaxStack dwords
// from its stack pointer, resumes it. Nothing allocates while it is suspended. Never the calling thread.
bool SampleThread(HANDLE thread, std::uint32_t threadId, ThreadSample* out, std::string* err = nullptr);

// The code of loaded modules, for telling return addresses on a stack from data.
class CodeRanges {
 public:
  // The executable sections of a loaded module (from its PE header in memory). False if it has none.
  bool AddModule(HMODULE module, const std::string& name);
  // True if v lies in an added module's code and the instruction before it is a call (E8 rel32 or FF /2).
  bool IsReturnAddress(std::uint32_t v) const;
  // "00ABCDEF" for the exe (Ghidra's addresses), "6F001234 (dgsim.dll+0x1234)" for a DLL, plain hex elsewhere.
  std::string Name(std::uint32_t v) const;
  bool Empty() const { return ranges_.empty(); }

 private:
  struct Range {
    std::uint32_t lo = 0, hi = 0, base = 0;
    bool exe = false;
    std::string module;
  };
  const Range* Find(std::uint32_t v) const;
  std::vector<Range> ranges_;
};

// The first `max` dwords on the sampled stack (from esp up) that look like return addresses; the EIP is not included.
std::vector<std::uint32_t> ReturnAddresses(const ThreadSample& s, const CodeRanges& code, int max);

enum class DumpKind : int { kNone = 0, kSmall = 1, kFull = 2 };

// Loads dbghelp.dll from System32 (SysWOW64 for this x86 process) once; later calls return the first result.
bool LoadDbgHelp(std::string* err = nullptr);
// Writes a minidump of this process to `path`: the sample's thread is the exception thread (kStallExceptionCode at
// its EIP, with its sampled context). kSmall: every thread's stack, the memory they point to, thread and module
// lists (a few MB); kFull: all memory. The dump runs on a worker thread; false after timeoutMs (the worker is left).
bool WriteDump(const std::string& path, const ThreadSample& s, DumpKind kind, int timeoutMs,
               std::string* err = nullptr);

class Monitor {
 public:
  struct Options {
    std::int64_t thresholdMs = 10000;  // no tick for this long = a stall (<= 0: the monitor never fires)
    int pollMs = 200;
  };
  struct Stall {
    std::int64_t stalledMs = 0;  // how long the tick counter had stood still
    std::uint32_t ticks = 0;     // its value
    const char* step = nullptr;  // the step the engine thread was in (Enter's string; nullptr = none of them)
    std::int64_t stepMs = 0;     // for how long
    ThreadSample sample;         // the engine thread (sample.ok false if it could not be read)
  };
  // Called on the monitor's thread, once per stall. If it returns, the monitor goes on (and fires again only after
  // the tick has moved).
  using OnStall = std::function<void(const Stall&)>;

  Monitor() = default;
  ~Monitor();
  Monitor(const Monitor&) = delete;
  Monitor& operator=(const Monitor&) = delete;

  // Starts the monitor's thread, watching thread `threadId` (opened here). False: no handle or no thread.
  bool Start(const Options& o, std::uint32_t threadId, OnStall onStall, std::string* err = nullptr);
  void Stop();
  bool Running() const { return thread_ != nullptr; }

  // The engine thread: the tick counter after each tick (the first beat arms the check).
  void Beat(std::uint32_t ticks);
  // The engine thread: the step it enters now (a string that lives for the process, e.g. a literal); nullptr when it
  // leaves it.
  void Enter(const char* step);
  // No ticks are expected until the next Beat (the check pauses).
  void Disarm() { armed_.store(false); }

  std::uint32_t Ticks() const { return ticks_.load(); }
  bool Armed() const { return armed_.load(); }
  std::uint32_t Stalls() const { return stalls_.load(); }
  const char* Step() const { return step_.load(); }
  std::int64_t ThresholdMs() const { return o_.thresholdMs; }

 private:
  static DWORD WINAPI ThreadMain(void* self);
  void Run();
  std::uint32_t NowMs() const;

  Options o_;
  OnStall onStall_;
  HANDLE thread_ = nullptr;   // the monitor's thread
  HANDLE watched_ = nullptr;  // the engine thread
  HANDLE stop_ = nullptr;     // event
  std::uint32_t watchedId_ = 0;
  LARGE_INTEGER freq_{}, t0_{};
  std::atomic<std::uint32_t> ticks_{0};
  std::atomic<bool> armed_{false};
  std::atomic<const char*> step_{nullptr};
  std::atomic<std::uint32_t> stepSinceMs_{0};
  std::atomic<std::uint32_t> stalls_{0};
  ThreadSample sample_;  // filled while the engine thread is suspended: no heap there
};

}  // namespace watchdog
}  // namespace gms
