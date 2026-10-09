// The match host's step guards (roadmap H5): see guards.h.
#include "guards.h"

#include <windows.h>

#include <malloc.h>  // _resetstkoflw

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>

#include "control/messages.h"
#include "core/watchdog/monitor.h"
#include "core/watchdog/rules.h"
#include "host/control.h"
#include "host/host.h"
#include "host/watchdog.h"
#include "log.h"
#include "match.h"
#include "perf.h"

namespace guards {

namespace {

const char* const kNames[kStepCount] = {
    "tick.time",       "tick.lua",      "tick.locomotion", "tick.projectiles", "tick.world",       "tick.delete",
    "tick.fow",        "tick.agents",   "tick.abilities",  "tick.gms",         "tick.commands",    "tick.gms out",
    "tick.network rate", "tick.match",  "tick.regen",      "tick.bots",        "frame.loader",     "frame.dispatch",
    "frame.clock",     "frame.navpower", "frame.update list", "frame.stats",   "frame.control"};

gms::watchdog::StepGuards g_guards(kStepCount, 10000);
gms::watchdog::CodeRanges g_code;  // Dawngate.exe and this DLL: names in the detailed log
bool g_hostMode;
bool g_disabled[kStepCount];
DWORD g_lastCode[kStepCount], g_lastAddress[kStepCount];
int g_fatalStep = -1;
double g_fatalForS;
unsigned g_fatalRuns;
int g_failS = 10;
LARGE_INTEGER g_freq, g_t0;

// the test keys
double g_throwAt;
int g_throwStep = kTickLua;
int g_throwRuns = 1;  // -1 = every run
int g_thrown;
double g_stallAt;
int g_stallForS;  // 0 = for ever
bool g_stalled;

std::int64_t NowMs() {
  if (!g_freq.QuadPart) {
    QueryPerformanceFrequency(&g_freq);
    QueryPerformanceCounter(&g_t0);
  }
  LARGE_INTEGER c;
  QueryPerformanceCounter(&c);
  return (c.QuadPart - g_t0.QuadPart) * 1000 / g_freq.QuadPart;
}

// Q3c: the counter's frequency for the step times (perf.h)
std::int64_t QpcFrequency() {
  if (!g_freq.QuadPart) NowMs();
  return g_freq.QuadPart;
}

void AddModule(HMODULE m) {
  char path[MAX_PATH];
  if (!m || !GetModuleFileNameA(m, path, MAX_PATH)) return;
  const char* base = path;
  for (const char* p = path; *p; ++p)
    if (*p == '\\' || *p == '/') base = p + 1;
  g_code.AddModule(m, base);
}

// The first exceptions of a step in full: registers and the return addresses on the stack (as experiment.cpp's
// Filter logs a one-time step's). Nothing for a stack overflow: the filter runs on what is left of the stack.
DWORD Filter(EXCEPTION_POINTERS* ep, int step) {
  const EXCEPTION_RECORD* er = ep->ExceptionRecord;
  g_lastCode[step] = er->ExceptionCode;
  g_lastAddress[step] = static_cast<DWORD>(reinterpret_cast<std::uintptr_t>(er->ExceptionAddress));
  if (er->ExceptionCode == EXCEPTION_STACK_OVERFLOW || !g_guards.DetailNext(step)) return EXCEPTION_EXECUTE_HANDLER;
  const CONTEXT* c = ep->ContextRecord;
  Log("EXCEPTION in %s: code %08lX at %s", kNames[step], er->ExceptionCode, g_code.Name(g_lastAddress[step]).c_str());
  if (er->ExceptionCode == EXCEPTION_ACCESS_VIOLATION && er->NumberParameters >= 2)
    Log("  access violation: %s of %08lX", er->ExceptionInformation[0] ? "write" : "read",
        static_cast<DWORD>(er->ExceptionInformation[1]));
  Log("  eax %08lX ebx %08lX ecx %08lX edx %08lX esi %08lX edi %08lX ebp %08lX esp %08lX", c->Eax, c->Ebx, c->Ecx,
      c->Edx, c->Esi, c->Edi, c->Ebp, c->Esp);
  static gms::watchdog::ThreadSample s;  // the faulting thread's own stack, read like the watchdog reads a sample
  s.context = *c;
  s.stackDwords = 0;
  MEMORY_BASIC_INFORMATION mbi;
  if (VirtualQuery(reinterpret_cast<const void*>(static_cast<std::uintptr_t>(c->Esp)), &mbi, sizeof mbi) == sizeof mbi &&
      mbi.State == MEM_COMMIT) {
    const std::uintptr_t end = reinterpret_cast<std::uintptr_t>(mbi.BaseAddress) + mbi.RegionSize;
    std::size_t bytes = static_cast<std::size_t>(end - c->Esp) & ~static_cast<std::size_t>(3);
    if (bytes > sizeof s.stack) bytes = sizeof s.stack;
    SIZE_T got = 0;
    if (ReadProcessMemory(GetCurrentProcess(), reinterpret_cast<const void*>(static_cast<std::uintptr_t>(c->Esp)),
                          s.stack, bytes, &got))
      s.stackDwords = static_cast<int>(got / 4);
  }
  std::string list;
  for (std::uint32_t a : gms::watchdog::ReturnAddresses(s, g_code, 24)) list += " " + g_code.Name(a);
  Log("  return addresses on the stack:%s", list.empty() ? " none" : list.c_str());
  return EXCEPTION_EXECUTE_HANDLER;
}

// test_throw_at: an access violation (a write to 0x10) in the chosen step, inside its guard
__declspec(noinline) void MaybeThrow(int step) {
  if (step != g_throwStep || g_throwAt <= 0 || (g_throwRuns >= 0 && g_thrown >= g_throwRuns)) return;
  if (!match::Started() || match::MatchSeconds() < g_throwAt) return;
  if (g_thrown++ == 0)
    Log("test_throw_at: an access violation in %s from match %.1f s on (%d run%s; [host] test_throw_ticks)", kNames[step],
        match::MatchSeconds(), g_throwRuns, g_throwRuns == 1 ? "" : "s");
  *reinterpret_cast<volatile int*>(static_cast<std::uintptr_t>(0x10)) = 0x48354835;
}

bool RunGuarded(int step, StepFn fn) {
  __try {
    MaybeThrow(step);
    fn();
    return true;
  } __except (Filter(GetExceptionInformation(), step)) {
    return false;
  }
}

void OnFailed(int step) {
  const gms::watchdog::StepVerdict v = g_guards.Failed(step, NowMs());
  const std::string at = g_code.Name(g_lastAddress[step]);
  if (v.log)
    Log("step %s: exception %08lX at %s: skipped this run (exception #%u of this step, %u in a row)%s", kNames[step],
        g_lastCode[step], at.c_str(), v.count, v.inARow,
        v.count == gms::watchdog::StepGuards::kLogFirst ? "; from now on every 100th is logged" : "");
  if (v.report && g_hostMode) {
    char text[400];
    std::snprintf(text, sizeof text,
                  "step %s threw %08lX at %s (exception #%u of this step); that run was skipped, the tick goes on",
                  kNames[step], g_lastCode[step], at.c_str(), v.count);
    hostctl::ReportError(dggms::control::kErrStepException, text, false);
  }
  if (!v.fatal) return;
  if (g_hostMode) {
    if (g_fatalStep < 0) {
      g_fatalStep = step;
      g_fatalForS = v.failingForMs / 1000.0;
      g_fatalRuns = v.inARow;
    }
    Log("step %s: threw in every run for %.1f s (%u runs in a row): the match ends after this frame ([host] "
        "step_fail_s %d)",
        kNames[step], v.failingForMs / 1000.0, v.inARow, g_failS);
  } else {
    g_disabled[step] = true;
    Log("step %s switched off: it threw in every run for %.1f s (%u runs in a row)", kNames[step],
        v.failingForMs / 1000.0, v.inARow);
  }
}

}  // namespace

const char* Name(int step) { return step >= 0 && step < kStepCount ? kNames[step] : "?"; }

int Find(const char* name) {
  if (!name || !*name) return -1;
  for (int i = 0; i < kStepCount; ++i) {
    const char* dot = std::strchr(kNames[i], '.');
    if (_stricmp(name, kNames[i]) == 0 || (dot && _stricmp(name, dot + 1) == 0)) return i;
  }
  return -1;
}

void Configure(bool hostMode) {
  g_hostMode = hostMode;
  NowMs();
  AddModule(GetModuleHandleA(nullptr));
  HMODULE self = nullptr;
  if (GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                         reinterpret_cast<LPCSTR>(&Configure), &self))
    AddModule(self);
  if (!hostMode) {
    Log("guards: harness mode: a step that throws skips that run; one that throws in every run for %d s is switched "
        "off",
        g_failS);
    return;
  }
  g_failS = HostOptionInt("step_fail_s", HostOptionInt("watchdog_s", 10));
  g_guards.SetFatalAfterMs(g_failS > 0 ? static_cast<std::int64_t>(g_failS) * 1000 : 0);
  char v[64], stepName[64];
  HostOptionString("test_throw_at", "0", v, sizeof v);
  g_throwAt = std::atof(v);
  HostOptionString("test_throw_step", "lua", stepName, sizeof stepName);
  g_throwStep = Find(stepName);
  g_throwRuns = HostOptionInt("test_throw_ticks", 1);
  HostOptionString("test_stall_at", "0", v, sizeof v);
  g_stallAt = std::atof(v);
  g_stallForS = HostOptionInt("test_stall_for_s", 0);
  Log("guards: every step of the host's frame (%d) under SEH: an exception skips that run (counted, logged, reported "
      "as stepException); a step that throws in every run for %d s ([host] step_fail_s; 0 = never) ends the match "
      "(stepFailing, exit code 7)",
      static_cast<int>(kStepCount), g_failS);
  if (g_throwAt > 0) {
    if (g_throwStep < 0)
      Log("guards: test_throw_step '%s' is no step; test_throw_at %.1f is off", stepName, g_throwAt);
    else
      Log("guards: test_throw_at %.1f s (match): an access violation in %s, %d run(s) in a row (-1 = every run)",
          g_throwAt, kNames[g_throwStep], g_throwRuns);
  }
  if (g_stallAt > 0)
    Log("guards: test_stall_at %.1f s (match): the engine thread stops at the start of a tick %s", g_stallAt,
        g_stallForS > 0 ? "for test_stall_for_s" : "for ever (the watchdog ends the host)");
}

bool Run(int step, StepFn fn) {
  if (step < 0 || step >= kStepCount || g_disabled[step]) return false;
  const char* before = hostwd::Enter(kNames[step]);
  LARGE_INTEGER t0, t1;
  QueryPerformanceCounter(&t0);
  const bool ok = RunGuarded(step, fn);
  QueryPerformanceCounter(&t1);
  perf::StepTime(step, static_cast<double>(t1.QuadPart - t0.QuadPart) * 1000.0 / static_cast<double>(QpcFrequency()));
  hostwd::Enter(before);
  if (ok) {
    g_guards.Succeeded(step);
    return true;
  }
  if (g_lastCode[step] == EXCEPTION_STACK_OVERFLOW) {
    _resetstkoflw();  // the guard page back, or the next overflow ends the process
    Log("step %s: stack overflow; the stack's guard page restored", kNames[step]);
  }
  OnFailed(step);
  return false;
}

bool Disabled(int step) { return step >= 0 && step < kStepCount && g_disabled[step]; }

void TickStart() {
  if (g_stallAt <= 0 || g_stalled || !match::Started() || match::MatchSeconds() < g_stallAt) return;
  g_stalled = true;
  Log("test_stall_at: the engine thread stops at match %.1f s, %s", match::MatchSeconds(),
      g_stallForS > 0 ? "for test_stall_for_s" : "for ever");
  const char* before = hostwd::Enter("test.stall");
  const DWORD start = GetTickCount();
  while (g_stallForS <= 0 || GetTickCount() - start < static_cast<DWORD>(g_stallForS) * 1000u) Sleep(50);
  hostwd::Enter(before);
  Log("test_stall_at: the engine thread goes on after %.1f s", (GetTickCount() - start) / 1000.0);
}

void AfterFrame() {
  if (g_fatalStep < 0) return;
  const int s = g_fatalStep;
  char text[500];
  std::snprintf(text, sizeof text,
                "step %s threw in every run for %.1f s (%u runs in a row, %u exceptions in all; the last %08lX at %s); "
                "[host] step_fail_s %d",
                kNames[s], g_fatalForS, g_fatalRuns, g_guards.Count(s), g_lastCode[s],
                g_code.Name(g_lastAddress[s]).c_str(), g_failS);
  const bool sent = hostctl::ReportError(dggms::control::kErrStepFailing, text, true);
  Log("guards: %s %s", sent ? "reported to dgserver (stepFailing, fatal):" : "not reported (no control channel):", text);
  char why[200];
  std::snprintf(why, sizeof why, "step %s threw in every run for %.1f s ([host] step_fail_s)", kNames[s], g_fatalForS);
  hostctl::Exit(7, why);
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size, "step exceptions: %s", g_guards.Describe(kNames).c_str());
}

}  // namespace guards
