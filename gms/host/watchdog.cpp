// The match host's watchdog (roadmap H5): see watchdog.h.
#include "watchdog.h"

#include <windows.h>

#include <atomic>
#include <cstdio>
#include <string>
#include <vector>

#include "control.h"
#include "control/messages.h"
#include "core/watchdog/monitor.h"
#include "engine/log.h"
#include "host.h"

namespace hostwd {

namespace {

using gms::watchdog::DumpKind;
using gms::watchdog::Monitor;

Monitor* g_monitor;  // never deleted: the host ends with TerminateProcess
int g_thresholdS;
DumpKind g_dumpKind = DumpKind::kSmall;
std::atomic<unsigned> g_matchMs{0};
gms::watchdog::CodeRanges g_code;
unsigned long g_engineThread;
const char* const kDumpNames[] = {"none", "small", "full"};

constexpr unsigned kLogWaitMs = 1000;  // the monitor thread never waits longer for the log lock
constexpr int kDumpTimeoutMs = 15000;  // dgserver's heartbeat timeout (30 s) runs from the last tick's heartbeat
constexpr unsigned kControlWaitMs = 2000;

void AddModule(HMODULE m) {
  char path[MAX_PATH];
  if (!m || !GetModuleFileNameA(m, path, MAX_PATH)) return;
  const char* base = path;
  for (const char* p = path; *p; ++p)
    if (*p == '\\' || *p == '/') base = p + 1;
  g_code.AddModule(m, base);
}

// Writes <data dir>\watchdog.txt: what the log got, the dump's result and the last log lines.
void WriteReport(const char* path, const char* const* lines, int n) {
  FILE* f = std::fopen(path, "w");
  if (!f) return;
  for (int i = 0; i < n; ++i) std::fprintf(f, "%s\n", lines[i]);
  static char tail[kLogTailLines * 330];
  std::fprintf(f, "\nthe last %d log lines before the stall report:\n", kLogTailLines);
  if (LogTail(tail, sizeof tail, kLogTailLines, kLogWaitMs))
    std::fputs(tail, f);
  else
    std::fputs("(the log lock was held: no lines)\n", f);
  std::fclose(f);
}

void OnStall(const Monitor::Stall& st) {
  const double stalledS = st.stalledMs / 1000.0;
  const double matchS = g_matchMs.load() / 1000.0;
  char where[200];
  if (st.step)
    std::snprintf(where, sizeof where, "in step %s for %.1f s", st.step, st.stepMs / 1000.0);
  else
    std::snprintf(where, sizeof where, "outside the host's steps (the engine's own frame or a wait)");
  char head[400], regs[400], rets[800];
  std::snprintf(head, sizeof head,
                "watchdog: STALL: no server tick for %.1f s (tick %u, match %.1f s; [host] watchdog_s %d); the engine "
                "thread %lu was %s",
                stalledS, st.ticks, matchS, g_thresholdS, g_engineThread, where);
  std::string eip = "?";
  if (st.sample.ok) {
    const CONTEXT& c = st.sample.context;
    eip = g_code.Name(st.sample.Eip());
    std::snprintf(regs, sizeof regs,
                  "watchdog:   eip %s, esp %08lX ebp %08lX eax %08lX ebx %08lX ecx %08lX edx %08lX esi %08lX edi %08lX",
                  eip.c_str(), c.Esp, c.Ebp, c.Eax, c.Ebx, c.Ecx, c.Edx, c.Esi, c.Edi);
    std::string list;
    for (std::uint32_t a : gms::watchdog::ReturnAddresses(st.sample, g_code, 24)) list += " " + g_code.Name(a);
    std::snprintf(rets, sizeof rets, "watchdog:   return addresses on the stack (%d dwords read):%s",
                  st.sample.stackDwords, list.empty() ? " none" : list.c_str());
  } else {
    std::snprintf(regs, sizeof regs, "watchdog:   the engine thread's registers could not be read");
    rets[0] = 0;
  }
  LogTimed(kLogWaitMs, "%s", head);
  LogTimed(kLogWaitMs, "%s", regs);
  if (rets[0]) LogTimed(kLogWaitMs, "%s", rets);
  if (IsDebuggerPresent()) {
    LogTimed(kLogWaitMs, "watchdog: a debugger is attached: no dump, no report, no exit (it fires again after the "
                         "next tick and another stall)");
    return;
  }

  const HostConfig& host = HostGet();
  char dmp[MAX_PATH], txt[MAX_PATH], dumpLine[400];
  std::snprintf(dmp, sizeof dmp, "%s\\watchdog.dmp", host.dataDir);
  std::snprintf(txt, sizeof txt, "%s\\watchdog.txt", host.dataDir);
  std::string err;
  bool dumped = false;
  if (g_dumpKind != DumpKind::kNone) {
    dumped = gms::watchdog::WriteDump(dmp, st.sample, g_dumpKind, kDumpTimeoutMs, &err);
    std::snprintf(dumpLine, sizeof dumpLine, "watchdog: minidump (%s) %s%s", kDumpNames[static_cast<int>(g_dumpKind)],
                  dumped ? "written: " : "FAILED: ", dumped ? dmp : err.c_str());
  } else {
    std::snprintf(dumpLine, sizeof dumpLine, "watchdog: no minidump ([host] watchdog_dump 0)");
  }
  LogTimed(kLogWaitMs, "%s", dumpLine);
  const char* lines[] = {head, regs, rets, dumpLine};
  WriteReport(txt, lines, rets[0] ? 4 : 3);
  LogTimed(kLogWaitMs, "watchdog: report %s", txt);

  // dgserver: the fatal report, then logoutGMS (it ends the match as aborted and frees the slot)
  char text[600];
  std::snprintf(text, sizeof text,
                "no server tick for %.1f s (tick %u, match %.1f s); the engine thread was %s, eip %s; %s", stalledS,
                st.ticks, matchS, where, eip.c_str(), dumped ? "minidump watchdog.dmp" : "no minidump");
  std::string ctl;
  const bool sent = hostctl::GiveUp(dggms::control::kErrTickStalled, text, kControlWaitMs, &ctl);
  LogTimed(kLogWaitMs, "watchdog: %s", sent ? "reported to dgserver: reportGMSError tick stalled (fatal), logoutGMS"
                                            : "not reported (no control channel, or its lock was not free)");
  char why[300];
  std::snprintf(why, sizeof why, "the watchdog: no server tick for %.1f s (%s)", stalledS,
                st.step ? st.step : "outside the host's steps");
  LogTimed(kLogWaitMs, "host: exiting (code 6): %s", why);
  if (!ctl.empty()) LogTimed(kLogWaitMs, "host: %s", ctl.c_str());
  TerminateProcess(GetCurrentProcess(), 6);
  for (;;) Sleep(1000);
}

}  // namespace

void Start(unsigned long engineThreadId) {
  if (g_monitor || !HostGet().enabled) return;
  g_thresholdS = HostOptionInt("watchdog_s", 10);
  int dump = HostOptionInt("watchdog_dump", 1);
  if (dump < 0 || dump > 2) dump = 1;
  g_dumpKind = static_cast<DumpKind>(dump);
  if (g_thresholdS <= 0) {
    Log("watchdog: off ([host] watchdog_s %d); a hung engine thread ends only by dgserver's heartbeat timeout",
        g_thresholdS);
    return;
  }
  g_engineThread = engineThreadId;
  AddModule(GetModuleHandleA(nullptr));
  HMODULE self = nullptr;
  if (GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT,
                         reinterpret_cast<LPCSTR>(&Start), &self))
    AddModule(self);
  AddModule(GetModuleHandleA("ntdll.dll"));
  AddModule(GetModuleHandleA("KERNELBASE.dll"));
  AddModule(GetModuleHandleA("kernel32.dll"));
  std::string err;
  if (g_dumpKind != DumpKind::kNone && !gms::watchdog::LoadDbgHelp(&err))
    Log("watchdog: %s; stalls are reported without a minidump", err.c_str());
  Monitor::Options o;
  o.thresholdMs = static_cast<std::int64_t>(g_thresholdS) * 1000;
  o.pollMs = HostOptionInt("watchdog_poll_ms", 200);
  g_monitor = new Monitor;
  if (!g_monitor->Start(o, engineThreadId, &OnStall, &err)) {
    Log("watchdog: NOT started: %s", err.c_str());
    delete g_monitor;
    g_monitor = nullptr;
    return;
  }
  Log("watchdog: on: the engine thread %lu without a server tick for %d s ends the match (minidump %s, report "
      "reportGMSError tick stalled, exit code 6); armed at the first tick",
      engineThreadId, g_thresholdS, kDumpNames[dump]);
}

bool Active() { return g_monitor != nullptr; }

void Beat(unsigned ticks, double matchSeconds) {
  if (!g_monitor) return;
  g_matchMs.store(matchSeconds > 0 ? static_cast<unsigned>(matchSeconds * 1000.0) : 0u);
  g_monitor->Beat(ticks);
}

const char* Enter(const char* step) {
  if (!g_monitor) return nullptr;
  const char* before = g_monitor->Step();
  g_monitor->Enter(step);
  return before;
}

void Disarm() {
  if (g_monitor) g_monitor->Disarm();
}

void Describe(char* out, unsigned size) {
  if (!g_monitor) {
    std::snprintf(out, size, "watchdog off");
    return;
  }
  std::snprintf(out, size, "watchdog %d s (%s, tick %u, stalls %u), dump %s", g_thresholdS,
                g_monitor->Armed() ? "armed" : "not armed", g_monitor->Ticks(), g_monitor->Stalls(),
                kDumpNames[static_cast<int>(g_dumpKind)]);
}

}  // namespace hostwd
