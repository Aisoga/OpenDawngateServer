// The match host's timing over a long match (Q3c): see perf.h.
#include "perf.h"

#include <windows.h>

#include <cstdio>
#include <string>
#include <vector>

#include "guards.h"
#include "host/host.h"
#include "log.h"
#include "match.h"

namespace perf {
namespace {

using namespace gms::timing;

bool g_on = true;
bool g_csv = false;
double g_windowS = 60;
double g_tickMs = 33;
std::string g_csvPath;
int g_latencyLogLimit = 400;  // Q3c-live2: the per-command `commands latency: player ...` lines
int g_latencyLogged = 0;
MinuteTimer g_timer;
CommandLatency g_cmds;
TickSample g_cur;
bool g_inTick;
double g_tickStart = 0, g_lastStart = -1, g_engineDone = -1;
int g_kind = kTrafficSession;
int g_minute = -2;  // the open window's label; -2 = none yet
std::vector<GaugeSource> g_sources;
std::string g_headerGauges;  // the gauge names of the CSV's last header ("" = no header yet)
std::vector<std::string> g_stepNames;

// Message kinds of B4's command type (+0): 1 move, 2/8/11 stop, 3 ability, 9 attack-move; 0 = a dropped command.
unsigned KindMaskOfCommandType(std::uint32_t type) {
  switch (type) {
    case 1: return 1u << kCmdKindMove;
    case 9: return 1u << kCmdKindAttackMove;
    case 2: case 8: case 11: return 1u << kCmdKindStop;
    case 3: case 5: return 1u << kCmdKindAbility;
    default: return (1u << kCmdKindAbility) | (1u << kCmdKindMove) | (1u << kCmdKindAttackMove) | (1u << kCmdKindStop);
  }
}

double HostS() { return LogMs() / 1000.0; }

int MatchMinute() {
  if (!match::Enabled() || !match::Started()) return -1;
  const double s = match::MatchSeconds();
  return s >= 0 ? static_cast<int>(s / 60.0) : -1;
}

void WriteCsv(const std::string& header, const std::string& row, bool newHeader) {
  if (!g_csv || g_csvPath.empty()) return;
  FILE* f = std::fopen(g_csvPath.c_str(), "ab");
  if (!f) return;
  if (newHeader) std::fprintf(f, "%s\r\n", header.c_str());
  std::fprintf(f, "%s\r\n", row.c_str());
  std::fclose(f);
}

void Roll() {
  const double hostS = HostS();
  const bool started = match::Enabled() && match::Started();
  const MinuteRow row = g_timer.Roll(g_minute, hostS, started ? match::MatchSeconds() : -1.0);
  const CommandLatency::Row cmds = g_cmds.Roll();
  Log("%s", FormatTimingLine(row, g_tickMs, g_timer.SpikeMs(), g_stepNames).c_str());
  Log("%s", FormatTrafficLine(row).c_str());
  const std::string cl = CommandLatency::Format(row.minute, cmds);
  if (!cl.empty()) Log("%s", cl.c_str());
  Gauges g;
  for (GaugeSource fn : g_sources) {
    std::string line;
    fn(&g, &line, row.minute);
    if (!line.empty()) Log("%s", line.c_str());
  }
  Log("%s", FormatGauges(row.minute, g).c_str());
  std::string names;
  for (const auto& kv : g) names += kv.first + ",";
  const bool newHeader = names != g_headerGauges;
  g_headerGauges = names;
  WriteCsv(CsvHeader(g_stepNames, g), CsvRow(row, g_stepNames, cmds, g), newHeader);
}

}  // namespace

void Configure(bool hostMode, double tickMs, const char* dataDir) {
  g_tickMs = tickMs > 0 ? tickMs : 33.0;
  g_timer = MinuteTimer(g_tickMs);
  g_stepNames.clear();
  for (int i = 0; i < guards::kStepCount; ++i) g_stepNames.push_back(guards::Name(i));
  if (!hostMode) {
    Log("perf (Q3c): on (harness: the timing lines, no CSV)");
    return;
  }
  g_on = HostOptionInt("timing", 1) != 0;
  g_csv = g_on && HostOptionInt("timing_csv", 1) != 0 && dataDir && dataDir[0];
  g_windowS = HostOptionInt("timing_window_s", 60);
  if (g_windowS < 5) g_windowS = 5;
  if (g_csv) g_csvPath = std::string(dataDir) + "\\host_timing.csv";
  const int outlierMs = HostOptionInt("latency_log_ms", 1000);
  g_latencyLogLimit = HostOptionInt("latency_log_limit", 400);
  g_cmds.SetOutlierMs(g_on && g_latencyLogLimit > 0 ? outlierMs : -1);
  Log("perf (Q3c-live2): [host] latency_log_ms %d, latency_log_limit %d (a line per command answered or done after "
      "that long, timed out or superseded)",
      outlierMs, g_latencyLogLimit);
  Log("perf (Q3c): [host] timing %d, timing_csv %d (%s), timing_window_s %.0f; tick budget %.0f ms, spikes over %.0f ms",
      g_on, g_csv, g_csv ? g_csvPath.c_str() : "-", g_windowS, g_tickMs, g_timer.SpikeMs());
}

bool Enabled() { return g_on; }

void TickBegin(double lagMs) {
  if (!g_on) return;
  const double now = NowMs();
  g_cur = TickSample();
  g_cur.lagMs = lagMs > 0 ? lagMs : 0;
  g_cur.intervalMs = g_lastStart >= 0 ? now - g_lastStart : 0;
  g_cur.stepMs.assign(guards::kStepCount, 0.0);
  g_lastStart = g_tickStart = now;
  g_engineDone = -1;
  g_inTick = true;
}

void EngineFrameDone() {
  if (g_inTick) g_engineDone = NowMs();
}

void StepTime(int step, double ms) {
  if (g_inTick && step >= 0 && static_cast<std::size_t>(step) < g_cur.stepMs.size())
    g_cur.stepMs[static_cast<std::size_t>(step)] += ms;
}

void TickEnd() {
  if (!g_on || !g_inTick) return;
  const double now = NowMs();
  g_inTick = false;
  g_cur.durationMs = now - g_tickStart;
  g_cur.engineMs = g_engineDone >= 0 ? g_engineDone - g_tickStart : 0;
  const int minute = MatchMinute();
  if (g_minute == -2) g_minute = minute;
  g_timer.Add(g_cur, HostS());
  g_cmds.Expire(now);
  for (const CommandLatency::Outlier& o : g_cmds.TakeOutliers()) {
    if (++g_latencyLogged > g_latencyLogLimit) continue;
    Log("%s; taken at host %.3f s%s", CommandLatency::FormatOutlier(o).c_str(), HostS() - (now - o.intakeMs) / 1000.0,
        g_latencyLogged == g_latencyLogLimit ? " ([host] latency_log_limit reached)" : "");
  }
  if (minute != g_minute || HostS() - g_timer.WindowStartS() >= g_windowS) {
    Roll();
    g_minute = minute;
  }
}

void SetKind(int kind) { g_kind = kind >= 0 && kind < kNumTrafficKinds ? kind : kTrafficOther; }
int Kind() { return g_kind; }

void OnSend(int toPlayer, const std::uint8_t* msg, std::size_t n) {
  if (!g_on || !msg || n == 0) return;
  const int kind = msg[0] == 0xC7 ? kTrafficSession : g_kind;  // a ClockSync answer is the session's whoever asks
  if (g_inTick) {
    ++g_cur.msgs[kind];
    g_cur.bytes[kind] += static_cast<std::uint32_t>(n);
  }
  const double now = NowMs();
  const unsigned ability = 1u << kCmdKindAbility;
  switch (msg[0]) {
    case 0xB4:  // ActionCommandResponse: +0 command type, +4 player, +5 sequence, +6 kind (1 started, 2 result)
      if (n == 1 + 0x48) {
        const std::uint32_t type = msg[1] | msg[2] << 8 | msg[3] << 16 | static_cast<std::uint32_t>(msg[4]) << 24;
        const int player = msg[5];
        const std::uint8_t seq = msg[6];
        if (msg[7] == 2) {
          g_cmds.OnAnswer(player, seq, KindMaskOfCommandType(type), now);
        } else if (msg[7] == 1) {  // the instance started: the cast's effect (and its answer if none came yet)
          g_cmds.OnAnswer(player, seq, ability, now);
          g_cmds.OnEffect(player, seq, ability, now);
        }
      }
      break;
    case 0x9B:  // LocomotionCommand: u8 subtype, u8 sequence of the command it answers (0 = none)
      if (n >= 3 && msg[2] != 0 && toPlayer >= 0) g_cmds.OnAnswer(toPlayer, msg[2], 0, now);
      break;
    case 0xC3:  // the ItemTransaction answer: u8 player, ...
      if (n == 9) g_cmds.OnAnswer(msg[1], 0, 1u << kCmdKindItem, now);
      break;
    case 0xAA:  // LabsPlayerFowUpdate of player msg[1]: a purchase's gold and inventory, a rank-up's ranks
      if (n >= 2) {
        g_cmds.OnAnswer(msg[1], 0, 1u << kCmdKindRankUp, now);
        g_cmds.OnEffect(msg[1], 0, (1u << kCmdKindItem) | (1u << kCmdKindRankUp), now);
      }
      break;
    default:
      break;
  }
}

CommandLatency& Commands() { return g_cmds; }

void AddSource(GaugeSource fn) {
  for (GaugeSource f : g_sources)
    if (f == fn) return;
  g_sources.push_back(fn);
}

}  // namespace perf
