// gms/core/watchdog (H5): the frozen host's rules (a stalled tick, a step that fails in every run) with a made-up
// clock, and the real watchdog thread against a test thread: the stall, the step it names, the sample, the stack scan
// and the minidump (written to %TEMP%, checked and deleted). docs/SERVER_MODE.md *A frozen host (H5)*.
#include <windows.h>

#include <atomic>
#include <cstdint>
#include <cstdio>
#include <fstream>
#include <string>
#include <thread>
#include <vector>

#include "core/watchdog/monitor.h"
#include "core/watchdog/rules.h"
#include "tests/check.h"

using namespace gms::watchdog;

GMS_TEST(WatchdogStallDetector) {
  StallDetector d(10000);
  CHECK(!d.Check(0, 0, false));  // not armed: no ticks expected (start-up, level load, a warm host)
  CHECK(!d.Check(60000, 0, false));
  CHECK(!d.Armed());
  CHECK(!d.Check(61000, 1, true));  // the first tick arms it
  CHECK(d.Armed());
  for (std::int64_t t = 61033; t < 70000; t += 33) CHECK(!d.Check(t, static_cast<std::uint32_t>(t / 33), true));
  const std::uint32_t last = 69999 / 33;
  // the tick stops at 69,999 ms: nothing until 10 s later, then once
  CHECK(!d.Check(70000, last, true));
  CHECK(!d.Check(79900, last, true));
  CHECK(d.Check(80000, last, true));
  CHECK(d.Fired());
  CHECK_EQ(d.SinceChangeMs(80000), static_cast<std::int64_t>(10000));  // since the poll that saw the last change
  CHECK_EQ(d.LastTicks(), last);
  CHECK(!d.Check(90000, last, true));   // once per stall
  CHECK(!d.Check(200000, last, true));
  // the tick moves again (a stall that ended by itself, e.g. under a debugger): a new stall fires again
  CHECK(!d.Check(200100, last + 1, true));
  CHECK(!d.Fired());
  CHECK(!d.Check(210000, last + 1, true));
  CHECK(d.Check(210100, last + 1, true));
  // disarmed (the harness's run_ms end): nothing; armed again, the age starts again
  CHECK(!d.Check(300000, last + 1, false));
  CHECK(!d.Check(300100, last + 1, true));
  CHECK(!d.Check(310000, last + 1, true));
  CHECK(d.Check(310100, last + 1, true));
  // threshold 0 = off
  StallDetector off(0);
  CHECK(!off.Check(0, 1, true));
  CHECK(!off.Check(1000000, 1, true));
}

GMS_TEST(WatchdogStepGuardsSkipAndLog) {
  StepGuards g(3, 10000);
  CHECK(StepGuards::IsPowerOfTen(1) && StepGuards::IsPowerOfTen(10) && StepGuards::IsPowerOfTen(1000));
  CHECK(!StepGuards::IsPowerOfTen(0) && !StepGuards::IsPowerOfTen(20) && !StepGuards::IsPowerOfTen(11));
  // one exception: counted, logged in full, reported; the next run goes through
  CHECK(g.DetailNext(1));
  StepVerdict v = g.Failed(1, 1000);
  CHECK(v.log && v.detail && v.report && !v.fatal);
  CHECK_EQ(v.count, 1u);
  CHECK_EQ(v.inARow, 1u);
  g.Succeeded(1);
  CHECK_EQ(g.InARow(1), 0u);
  CHECK_EQ(g.Count(1), 1u);
  CHECK_EQ(g.Describe(nullptr), std::string("step 1 1"));
  // later exceptions: detail for the first 3, a log line for the first 20 then every 100th, a report at 1, 10, 100
  int logs = 0, details = 0, reports = 0;
  for (int i = 2; i <= 1000; ++i) {
    v = g.Failed(1, 1000 + i);
    g.Succeeded(1);  // never in a row: never fatal
    logs += v.log;
    details += v.detail;
    reports += v.report;
    CHECK(!v.fatal);
  }
  CHECK_EQ(details, 2);    // #2, #3
  CHECK_EQ(logs, 19 + 10);  // #2..#20, then #100, #200, ..., #1000
  CHECK_EQ(reports, 3);     // #10, #100, #1000
  CHECK_EQ(g.Total(), 1000u);
  const char* names[] = {"tick.time", "tick.lua", "tick.locomotion"};
  CHECK_EQ(g.Describe(names), std::string("tick.lua 1000"));
  CHECK(!g.DetailNext(1));
  CHECK(g.DetailNext(0));
  // out of range: nothing
  v = g.Failed(7, 0);
  CHECK(!v.log && !v.fatal && v.count == 0);
  g.Succeeded(-1);
}

GMS_TEST(WatchdogStepGuardsPersistentFailure) {
  // a step that throws in every run at 30 Hz fails persistently after 10 s (and at least 10 runs in a row)
  StepGuards g(2, 10000);
  StepVerdict v;
  int fatalAt = -1;
  for (int k = 0; k < 400; ++k) {
    v = g.Failed(0, 5000 + k * 33);
    if (v.fatal) {
      CHECK_EQ(fatalAt, -1);  // once
      fatalAt = k;
    }
  }
  CHECK_EQ(fatalAt, 304);  // 304 * 33 = 10,032 ms >= 10,000
  CHECK(g.Fatal(0));
  CHECK(!g.Fatal(1));
  CHECK_EQ(g.Describe(nullptr), std::string("step 0 400 (400 in a row) FAILING"));
  // a run that goes through in between starts the count again
  StepGuards h(1, 10000);
  for (int k = 0; k < 300; ++k) CHECK(!h.Failed(0, k * 33).fatal);
  h.Succeeded(0);
  for (int k = 300; k < 600; ++k) CHECK(!h.Failed(0, k * 33).fatal);
  CHECK(!h.Failed(0, 600 * 33).fatal);      // 301 in a row since 9,900 ms: 9,900 ms
  CHECK(h.Failed(0, 600 * 33 + 100).fatal);  // 10,000 ms
  // two failures far apart without a run between (a step that runs only now and then): not before 10 runs
  StepGuards rare(1, 10000);
  CHECK(!rare.Failed(0, 0).fatal);
  CHECK(!rare.Failed(0, 60000).fatal);
  for (int k = 2; k < 9; ++k) CHECK(!rare.Failed(0, 60000 + k).fatal);
  CHECK(rare.Failed(0, 70000).fatal);  // the 10th in a row, 70 s after the first
  // step_fail_s 0: skipped run after run, never fatal
  StepGuards never(1, 0);
  for (int k = 0; k < 2000; ++k) CHECK(!never.Failed(0, k * 33).fatal);
}

namespace {

// The test thread: beats like the engine thread, then "hangs" in a wait inside a named step until released.
struct Engine {
  Monitor* mon = nullptr;
  HANDLE release = nullptr;
  std::atomic<std::uint32_t> ticks{0};
  std::atomic<bool> stalled{false};
  std::atomic<bool> stop{false};
  DWORD id = 0;
};

__declspec(noinline) void HangInStep(Engine* e) {
  e->mon->Enter("test.stall");
  e->stalled = true;
  WaitForSingleObject(e->release, INFINITE);  // the "hung Lua call"
  e->mon->Enter(nullptr);
}

DWORD WINAPI EngineMain(void* p) {
  Engine* e = static_cast<Engine*>(p);
  for (int i = 0; i < 20; ++i) {
    e->mon->Enter("tick.lua");
    Sleep(10);
    e->mon->Enter(nullptr);
    e->mon->Beat(++e->ticks);
  }
  HangInStep(e);
  while (!e->stop) {  // released: it ticks again
    Sleep(10);
    e->mon->Beat(++e->ticks);
  }
  return 0;
}

bool ReadFile(const std::string& path, std::string* out) {
  std::ifstream f(path, std::ios::binary);
  if (!f) return false;
  out->assign(std::istreambuf_iterator<char>(f), std::istreambuf_iterator<char>());
  return true;
}

std::uint32_t U32(const std::string& d, std::size_t at) {
  if (at + 4 > d.size()) return 0;
  return static_cast<std::uint32_t>(static_cast<unsigned char>(d[at])) |
         static_cast<std::uint32_t>(static_cast<unsigned char>(d[at + 1])) << 8 |
         static_cast<std::uint32_t>(static_cast<unsigned char>(d[at + 2])) << 16 |
         static_cast<std::uint32_t>(static_cast<unsigned char>(d[at + 3])) << 24;
}

}  // namespace

GMS_TEST(WatchdogMonitorStallSampleDump) {
  Monitor mon;
  Engine e;
  e.mon = &mon;
  e.release = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  HANDLE engine = CreateThread(nullptr, 0, &EngineMain, &e, CREATE_SUSPENDED, &e.id);
  CHECK(engine != nullptr);
  if (!engine) return;

  std::atomic<int> calls{0};
  Monitor::Stall seen;
  HANDLE fired = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  Monitor::Options o;
  o.thresholdMs = 400;
  o.pollMs = 20;
  std::string err;
  CHECK(mon.Start(o, e.id, [&](const Monitor::Stall& st) {
    if (calls++ == 0) {
      seen = st;
      SetEvent(fired);
    }
  }, &err));
  CHECK(mon.Running());
  CHECK(!mon.Armed());  // no beat yet
  const DWORD start = GetTickCount();
  ResumeThread(engine);
  CHECK(WaitForSingleObject(fired, 10000) == WAIT_OBJECT_0);
  const DWORD tookMs = GetTickCount() - start;
  CHECK(e.stalled.load());
  CHECK_EQ(calls.load(), 1);
  CHECK(tookMs >= 550);  // 20 beats of >= 10 ms, then the threshold (GetTickCount's 16 ms steps allowed for)
  CHECK(seen.stalledMs >= 400);
  CHECK(seen.stalledMs < 3000);
  CHECK_EQ(seen.ticks, 20u);
  CHECK(seen.step != nullptr && std::string(seen.step) == "test.stall");
  CHECK(seen.stepMs >= 300);
  // the sample: the engine thread's registers and stack, a return address into this exe (HangInStep's call of
  // WaitForSingleObject, or EngineMain's of HangInStep)
  CHECK(seen.sample.ok);
  CHECK_EQ(seen.sample.threadId, static_cast<std::uint32_t>(e.id));
  CHECK(seen.sample.Eip() != 0 && seen.sample.Esp() != 0);
  CHECK(seen.sample.stackDwords > 16);
  CodeRanges code;
  CHECK(code.AddModule(GetModuleHandleW(nullptr), "gms_tests.exe"));
  CHECK(!code.Empty());
  const std::vector<std::uint32_t> rets = ReturnAddresses(seen.sample, code, 24);
  CHECK(!rets.empty());
  const std::uint32_t hang = static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(&HangInStep));
  bool inHang = false;
  for (std::uint32_t r : rets) inHang = inHang || (r > hang && r < hang + 0x200);
  CHECK(inHang);
  CHECK(code.Name(rets[0]).size() == 8);  // the exe: Ghidra-style absolute address
  CHECK(!code.IsReturnAddress(0));
  CHECK(!code.IsReturnAddress(static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(&seen))));  // data
  CodeRanges k32;  // a DLL's addresses are named module+offset
  CHECK(k32.AddModule(GetModuleHandleW(L"kernel32.dll"), "kernel32.dll"));
  const std::uintptr_t k32base = reinterpret_cast<std::uintptr_t>(GetModuleHandleW(L"kernel32.dll"));
  bool named = false;
  for (std::uintptr_t a = k32base; a < k32base + 0x400000 && !named; a += 0x100)
    named = k32.Name(static_cast<std::uint32_t>(a)).find("(kernel32.dll+0x") != std::string::npos;
  CHECK(named);

  // the dump: a minidump of this process with the engine thread as its exception thread (minidump_stack.py's input)
  char tmp[MAX_PATH];
  GetTempPathA(MAX_PATH, tmp);
  const std::string path = std::string(tmp) + "gms_tests_watchdog_" + std::to_string(GetCurrentProcessId()) + ".dmp";
  CHECK(LoadDbgHelp(&err));
  const bool dumped = WriteDump(path, seen.sample, DumpKind::kSmall, 30000, &err);
  CHECK(dumped);
  if (!dumped) std::printf("  dump: %s\n", err.c_str());
  std::string d;
  CHECK(ReadFile(path, &d));
  DeleteFileA(path.c_str());
  CHECK(d.size() > 1024 && d.compare(0, 4, "MDMP") == 0);
  bool exceptionStream = false;
  const std::uint32_t streams = U32(d, 8), dir = U32(d, 12);
  for (std::uint32_t i = 0; i < streams && dir + 12 * i + 12 <= d.size(); ++i) {
    if (U32(d, dir + 12 * i) != 6) continue;  // ExceptionStream: ThreadId, alignment, MINIDUMP_EXCEPTION (code first)
    const std::uint32_t at = U32(d, dir + 12 * i + 8);
    exceptionStream = true;
    CHECK_EQ(U32(d, at), static_cast<std::uint32_t>(e.id));
    CHECK_EQ(U32(d, at + 8), kStallExceptionCode);
    CHECK_EQ(U32(d, at + 8 + 16), seen.sample.Eip());  // ExceptionAddress (u64, low half)
  }
  CHECK(exceptionStream);
  CHECK(!WriteDump(path, seen.sample, DumpKind::kNone, 1000, &err));  // watchdog_dump 0

  // released, the thread ticks again: no second stall while it does
  SetEvent(e.release);
  Sleep(600);
  CHECK_EQ(calls.load(), 1);
  CHECK(mon.Ticks() > 20u);
  CHECK_EQ(mon.Stalls(), 1u);
  // disarmed (after its last beat: a beat arms it again), a thread that stops ticking is no stall
  e.stop = true;
  WaitForSingleObject(engine, 5000);
  mon.Disarm();
  Sleep(700);
  CHECK_EQ(calls.load(), 1);
  mon.Stop();
  CHECK(!mon.Running());
  CloseHandle(engine);
  CloseHandle(e.release);
  CloseHandle(fired);
}

GMS_TEST(WatchdogMonitorNeverArmed) {
  // a monitor whose thread never ticks (a warm host waiting for createGame) never fires; nor one that is off
  Monitor mon;
  std::atomic<int> calls{0};
  Monitor::Options o;
  o.thresholdMs = 100;
  o.pollMs = 10;
  CHECK(mon.Start(o, GetCurrentThreadId(), [&](const Monitor::Stall&) { ++calls; }));
  Sleep(400);
  CHECK_EQ(calls.load(), 0);
  mon.Stop();
  Monitor off;
  o.thresholdMs = 0;
  CHECK(off.Start(o, GetCurrentThreadId(), [&](const Monitor::Stall&) { ++calls; }));
  off.Beat(1);
  Sleep(300);
  CHECK_EQ(calls.load(), 0);
  off.Stop();
  // a thread id that doesn't exist: no start
  Monitor bad;
  std::string err;
  CHECK(!bad.Start(o, 0xFFFFFFF0u, nullptr, &err) && !err.empty());
  // the calling thread is never sampled (it would suspend itself)
  ThreadSample s;
  CHECK(!SampleThread(GetCurrentThread(), GetCurrentThreadId(), &s, &err));
}
