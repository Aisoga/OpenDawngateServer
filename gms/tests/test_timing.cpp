// gms/core/timing and gms/core/net/netsim.h (Q3c): the tick, traffic and command-latency arithmetic with made-up
// clocks, the CSV, and the network simulator's per-direction rule (DelayLine). The simulator inside a real RakNet
// endpoint is tested in test_net.cpp. docs/SERVER_MODE.md *Timing and desync (Q3c)*.
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#include "core/net/netsim.h"
#include "core/timing/timing.h"
#include "tests/check.h"

using namespace gms::timing;

namespace {

int Count(const std::string& s, char c) {
  int n = 0;
  for (char x : s) n += x == c;
  return n;
}

bool Has(const std::string& s, const std::string& part) { return s.find(part) != std::string::npos; }

}  // namespace

GMS_TEST(TimingSummaryNearestRank) {
  std::vector<double> v;
  for (int i = 100; i >= 1; --i) v.push_back(i);  // 1..100, unsorted
  const Summary s = Summarize(v);
  CHECK_EQ(s.n, 100);
  CHECK_EQ(s.p50, 50.0);
  CHECK_EQ(s.p90, 90.0);
  CHECK_EQ(s.p99, 99.0);
  CHECK_EQ(s.max, 100.0);
  CHECK_EQ(s.mean, 50.5);
  CHECK_EQ(Summarize({}).n, 0);
  CHECK_EQ(FormatSummary(Summary()), std::string("none"));
  const Summary one = Summarize({7.0});
  CHECK(one.p50 == 7.0 && one.p99 == 7.0 && one.max == 7.0);
  CHECK_EQ(FormatSummary(Summarize({1.0, 2.0, 3.0, 4.0})), std::string("p50 2.0, p90 4.0, p99 4.0, max 4.0"));
}

GMS_TEST(TimingMinuteTimerTicksStepsAndTraffic) {
  MinuteTimer t(33.0);
  CHECK_EQ(t.SpikeMs(), 132.0);  // 4 budgets (at least 100)
  CHECK_EQ(MinuteTimer(10.0).SpikeMs(), 100.0);
  // 100 ticks of 1..100 ms, one late start, a spike, two steps and some traffic
  for (int i = 1; i <= 100; ++i) {
    TickSample s;
    s.durationMs = i;
    s.lagMs = i == 50 ? 40.0 : 0.5;
    s.intervalMs = i == 1 ? 0 : 33.3;
    s.engineMs = 0.25;
    s.stepMs = {0.0, i * 0.5, 0.1};  // step 0 never ran
    s.msgs[kTrafficDeltas] = 10;
    s.bytes[kTrafficDeltas] = 100;
    s.msgs[kTrafficCreates] = i == 7 ? 3 : 0;
    s.bytes[kTrafficCreates] = i == 7 ? 900 : 0;
    t.Add(s, 1000.0 + i * 0.0333);
  }
  TickSample spike;
  spike.durationMs = 150;
  spike.stepMs = {0.0, 149.0};
  t.Add(spike, 1004.0);
  CHECK_EQ(t.Ticks(), 101);
  const MinuteRow r = t.Roll(3, 1060.0, 180.0);
  CHECK_EQ(r.minute, 3);
  CHECK_EQ(r.ticks, 101);
  CHECK_EQ(r.late, 68);    // 34..100 and the spike: over 33 ms
  CHECK_EQ(r.behind, 1);   // the tick that started 40 ms late
  CHECK_EQ(r.spikes, 1);   // over 132 ms
  CHECK_EQ(r.duration.max, 150.0);
  CHECK_EQ(r.worstStep, 1);
  CHECK_EQ(r.worstAtS, 1004.0);
  CHECK_EQ(r.interval.n, 99);  // the first tick has none, the spike gave none
  CHECK_EQ(r.engine.n, 100);
  CHECK(r.windowS > 59.9 && r.windowS < 60.1);
  CHECK_EQ(r.stepMeanMs.size(), static_cast<size_t>(3));
  CHECK_EQ(r.stepMeanMs[0], 0.0);
  CHECK_EQ(r.stepMaxMs[1], 149.0);
  CHECK_EQ(r.msgs[kTrafficDeltas], static_cast<uint64_t>(1000));
  CHECK_EQ(r.bytes[kTrafficCreates], static_cast<uint64_t>(900));
  CHECK_EQ(r.maxTickMsgs, 13u);
  CHECK_EQ(r.maxTickBytes, 1000u);
  const std::vector<std::string> names = {"tick.time", "tick.lua", "tick.fow"};
  const std::string line = FormatTimingLine(r, 33.0, t.SpikeMs(), names);
  CHECK(Has(line, "timing: minute 3 (match 180.0 s, 101 ticks in 60.0 s): duration p50 51.0"));
  CHECK(Has(line, "late 68 (> 33 ms), behind 1, spikes 1 (> 132 ms), worst 150.0 ms at host 1004.0 s (step tick.lua)"));
  CHECK(Has(line, "steps (mean/max ms): tick.lua"));
  CHECK(Has(line, "tick.fow 0.10/0.10"));
  CHECK(!Has(line, "tick.time"));  // never ran
  const std::string traffic = FormatTrafficLine(r);
  CHECK(Has(traffic, "traffic: minute 3: 16.7 msg/s, 182 B/s; per tick: creates 0.03 msg / 9 B, deltas 9.90 msg / 99 B"));
  CHECK(Has(traffic, "busiest tick 13 msg / 1,000 B"));
  // the next window starts empty at the roll
  CHECK_EQ(t.Ticks(), 0);
  CHECK_EQ(t.WindowStartS(), 1060.0);
  const MinuteRow empty = t.Roll(-1, 1070.0, -1);
  CHECK_EQ(empty.ticks, 0);
  CHECK(Has(FormatTimingLine(empty, 33.0, 132.0, names), "before the match, host 1070.0 s, 0 ticks in 10.0 s"));
  CHECK(Has(FormatTrafficLine(empty), "nothing sent"));
}

GMS_TEST(TimingCommandLatencyAnswersAndEffects) {
  CommandLatency c(10000);
  // a move: arrived 990, taken 1000, answered (9B with its sequence) 1040, the locomotion goal at 1066
  c.OnIntake(0, kCmdKindMove, 5, 990, 1000, true, 10.f, -20.f);
  // a cast with sequence 0 (the client's W), answered by kind: B4 kind 2 at 1010, started at 1100
  c.OnIntake(0, kCmdKindAbility, 0, -1, 1005);
  // a purchase of another player
  c.OnIntake(3, kCmdKindItem, 0, 1001, 1002);
  const auto moves = c.PendingMoves(0);
  CHECK_EQ(moves.size(), static_cast<size_t>(1));
  CHECK(moves.size() == 1 && moves[0].x == 10.f && moves[0].y == -20.f && moves[0].seq == 5);
  CHECK(!c.OnAnswer(0, 9, 0, 1020));                             // no command with sequence 9
  CHECK(c.OnAnswer(0, 0, 1u << kCmdKindAbility, 1010));          // seq 0: the oldest pending ability
  CHECK(c.OnAnswer(0, 5, 0, 1040));                              // seq 5: the move
  CHECK(!c.OnAnswer(0, 5, 0, 1041));                             // answered already
  CHECK(c.OnEffect(0, 5, 0, 1066));
  CHECK(c.PendingMoves(0).empty());
  CHECK(c.OnEffect(0, 0, 1u << kCmdKindAbility, 1100));
  CHECK(!c.OnEffect(1, 0, ~0u, 1100));                           // nothing of player 1
  CHECK(c.OnAnswer(3, 0, 1u << kCmdKindItem, 1003));
  c.Expire(20000);                                               // the purchase's effect never came
  const CommandLatency::Row r = c.Roll();
  CHECK_EQ(r.pending, 0);
  CHECK_EQ(r.kinds[kCmdKindMove].n, 1);
  CHECK_EQ(r.kinds[kCmdKindMove].queued.max, 10.0);
  CHECK_EQ(r.kinds[kCmdKindMove].answer.max, 40.0);
  CHECK_EQ(r.kinds[kCmdKindMove].effect.max, 66.0);
  CHECK_EQ(r.kinds[kCmdKindAbility].queued.max, 0.0);  // no arrival time: the intake time
  CHECK_EQ(r.kinds[kCmdKindAbility].answer.max, 5.0);
  CHECK_EQ(r.kinds[kCmdKindAbility].effect.max, 95.0);
  CHECK_EQ(r.kinds[kCmdKindItem].noAnswer, 0);
  CHECK_EQ(r.kinds[kCmdKindItem].noEffect, 1);
  const std::string line = CommandLatency::Format(4, r);
  CHECK(Has(line, "commands latency: minute 4 (ms): move 1 (queued p50 10.0"));
  CHECK(Has(line, "item 1 (queued p50 1.0, p90 1.0, p99 1.0, max 1.0; answer p50 1.0, p90 1.0, p99 1.0, max 1.0; effect "
                  "none; no answer 0, no effect 1)"));
  CHECK(Has(line, "pending 0"));
  // the next window: nothing taken, nothing timed out -> no line
  CHECK(CommandLatency::Format(5, c.Roll()).empty());
}

GMS_TEST(TimingCsvColumnsMatchTheHeader) {
  const std::vector<std::string> steps = {"tick.time", "tick.lua"};
  const Gauges g = {{"objects", 512}, {"id_serial", 0x7C71}, {"los_pool", 280.5}};
  MinuteTimer t;
  TickSample s;
  s.durationMs = 3;
  s.stepMs = {1, 2};
  t.Add(s, 1);
  const MinuteRow r = t.Roll(0, 61, 60);
  CommandLatency c;
  c.OnIntake(0, kCmdKindStop, 1, 0, 1);
  const std::string header = CsvHeader(steps, g), row = CsvRow(r, steps, c.Roll(), g);
  CHECK_EQ(Count(header, ','), Count(row, ','));
  CHECK(Has(header, "tick.lua_mean_ms,tick.lua_max_ms,creates_msgs,creates_bytes"));
  CHECK(Has(header, "attack_move_n,"));
  CHECK(Has(header, ",objects,id_serial,los_pool"));
  CHECK(Has(row, ",512,31857,280.500"));
  CHECK_EQ(FormatGauges(2, g), std::string("records: minute 2: objects 512, id_serial 31857, los_pool 280.500"));
}

// A synthetic host_timing.csv of 13 windows (a pre-game one and 12 match minutes, objects and ticks growing) for
// soak.py timing's own check: written only when GMS_TIMING_CSV_OUT names a file.
GMS_TEST(TimingCsvFixtureForSoakPy) {
  const char* out = std::getenv("GMS_TIMING_CSV_OUT");
  if (!out || !*out) return;
  const std::vector<std::string> steps = {"tick.time", "tick.lua", "tick.gms out"};
  std::FILE* f = std::fopen(out, "wb");
  CHECK(f != nullptr);
  if (!f) return;
  MinuteTimer t(33.0);
  CommandLatency c;
  for (int m = -1; m < 12; ++m) {
    for (int i = 0; i < 1800; ++i) {
      TickSample s;
      s.durationMs = 2.0 + (m > 6 ? m : 0) + (i % 100 == 0 ? 20.0 : 0.0);
      s.lagMs = 0.4;
      s.intervalMs = 33.3;
      s.stepMs = {0.1, 1.0 + 0.1 * m, 0.5};
      s.msgs[kTrafficDeltas] = 12;
      s.bytes[kTrafficDeltas] = 400;
      t.Add(s, 100.0 + (m + 1) * 60.0 + i / 30.0);
    }
    c.OnIntake(0, kCmdKindMove, 7, 1000.0 * m, 1000.0 * m + 3);
    c.OnAnswer(0, 7, 0, 1000.0 * m + 20);
    c.OnEffect(0, 7, 0, 1000.0 * m + 40);
    const Gauges g = {{"objects", 500.0 + (m > 6 ? 40.0 * m : 0.0)}, {"vision_objects", 3}, {"id_serial", 0x7C71 + m}};
    const MinuteRow r = t.Roll(m, 100.0 + (m + 2) * 60.0, m < 0 ? -1.0 : 60.0 * (m + 1));
    if (m == -1) std::fprintf(f, "%s\r\n", CsvHeader(steps, g).c_str());
    std::fprintf(f, "%s\r\n", CsvRow(r, steps, c.Roll(), g).c_str());
  }
  std::fclose(f);
}

GMS_TEST(NetSimDelayLineDelayJitterLoss) {
  gms::NetSimOptions o;
  CHECK(!o.Active());
  o.delayMs = 50;
  CHECK(o.Active());
  {
    gms::DelayLine d(o);
    double due = 0;
    CHECK(d.Admit(1000, &due));
    CHECK_EQ(due, 1050.0);
    CHECK(d.Admit(1001, &due));
    CHECK_EQ(due, 1051.0);
    CHECK_EQ(d.AddedMaxMs(), 50.0);
  }
  {
    o.jitterMs = 40;
    o.seed = 7;
    gms::DelayLine d(o), same(o);
    double last = 0, minAdded = 1e9, maxAdded = 0;
    bool spread = false;
    for (int i = 0; i < 2000; ++i) {
      const double now = 1000.0 + i * 2.0;  // one datagram every 2 ms: jitter > spacing, so order matters
      double due = 0, due2 = 0;
      CHECK(d.Admit(now, &due));
      CHECK(same.Admit(now, &due2));
      CHECK_EQ(due, due2);           // the seed decides
      CHECK(due >= last);            // never overtakes the one before
      CHECK(due >= now + 50.0);
      last = due;
      minAdded = due - now < minAdded ? due - now : minAdded;
      maxAdded = due - now > maxAdded ? due - now : maxAdded;
      if (due - now > 70.0) spread = true;
    }
    CHECK(minAdded >= 50.0);
    CHECK(maxAdded <= 90.0 + 1e-9);  // the order rule waits at most for the previous one's own jitter
    CHECK(spread);
    CHECK(d.AddedMeanMs() > 60.0 && d.AddedMeanMs() < 90.0);
    CHECK_EQ(d.Dropped(), static_cast<uint64_t>(0));
  }
  {
    gms::NetSimOptions l;
    l.lossPercent = 30;
    l.seed = 3;
    gms::DelayLine d(l);
    int lost = 0;
    for (int i = 0; i < 10000; ++i) {
      double due = 0;
      if (!d.Admit(i, &due)) ++lost;
    }
    CHECK(lost > 2700 && lost < 3300);
    CHECK_EQ(d.Dropped(), static_cast<uint64_t>(lost));
    l.lossPercent = 100;
    gms::DelayLine all(l);
    double due = 0;
    CHECK(!all.Admit(0, &due));
  }
}
