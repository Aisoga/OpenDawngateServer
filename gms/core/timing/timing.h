// Timing, traffic and command latency over a long match (roadmap Q3c; docs/SERVER_MODE.md *Timing and desync (Q3c)*).
//
// Testers of another Dawngate server saw its steps run longer than real time, lag spikes that grew with the match,
// commands seconds behind and timing checks answered a tick late. This is the engine-free part of the match host's
// instrumentation: per window (a match minute) it summarizes
//   MinuteTimer     each server tick's duration (the core-only engine frame before it included), how late it started
//                   against its schedule and the gap to the previous one: percentiles, ticks over the budget (late),
//                   ticks that started more than a budget behind, spikes, each guarded step's share; and the messages
//                   and bytes the tick sent, per kind (creates, deltas, forwarded, player updates, fog, answers,
//                   session, other), to see whether the replication stays flat;
//   CommandLatency  each client command from its arrival in RakNet over its intake (the engine's poll) to its answer
//                   (the first message that carries its sequence back) and its effect (the server state shows it: a
//                   move's locomotion goal, a cast's "ability started", a purchase's player update);
//   Gauges          whatever the host samples once per window (records alive, pools, the object id counter).
// The host (gms/engine/perf.cpp) feeds them, writes one `timing:`, `traffic:`, `commands latency:` and `records:`
// line per window and a CSV row (host_timing.csv, in the host's data folder beside dgsim.log). gms_tests checks
// the arithmetic with made-up clocks (tests/test_timing.cpp). Built into dgsim and gms_tests directly (no RakNet).
#pragma once

#include <cstdint>
#include <deque>
#include <string>
#include <utility>
#include <vector>

#include "core/timing/clock.h"  // NowMs(): what the endpoint stamps arrivals with

namespace gms {
namespace timing {

// Nearest-rank percentiles of a sample set.
struct Summary {
  int n = 0;
  double mean = 0, p50 = 0, p90 = 0, p99 = 0, max = 0;
};
Summary Summarize(std::vector<double> v);  // by value: sorted inside
// "p50 2.1, p90 3.4, p99 6.0, max 12.3" (one decimal), or "none" when n == 0.
std::string FormatSummary(const Summary& s);

// ---------------------------------------------------------------------------------------------- ticks and traffic

enum TrafficKind : int {
  kTrafficCreates = 0,  // 8E ... 8F of objects a player didn't know
  kTrafficDeltas,       // per-struct deltas (90/9C/9F/A0/A1/A2) and animation overrides (9E)
  kTrafficForwarded,    // the server simulator's outgoing vector (seam 1)
  kTrafficPlayers,      // the players' A9/AA deltas and catch-ups
  kTrafficFog,          // 93, 94 + full state + 95, A3
  kTrafficAnswers,      // the command intake's answers (B4, C3, C2, the send stub's messages)
  kTrafficSession,      // the session's own (ClockSync answers, status echoes, the pre-game)
  kTrafficOther,        // death recaps, the HUD data, the end, the post-game
  kNumTrafficKinds
};
const char* TrafficKindName(int kind);

struct TickSample {
  double lagMs = 0;       // how late the tick started against its schedule (0 = on time)
  double intervalMs = 0;  // since the previous tick's start (0 for the first)
  double durationMs = 0;  // the whole tick, the core-only engine frame before it included
  double engineMs = 0;    // the core-only engine frame alone (0 in stage 1)
  std::vector<double> stepMs;  // per guarded step (index = the host's step number; may be shorter)
  std::uint32_t msgs[kNumTrafficKinds] = {};
  std::uint32_t bytes[kNumTrafficKinds] = {};
};

struct MinuteRow {
  int minute = 0;          // the window's label (the host's match minute; -1 = before the match)
  double hostEndS = 0;     // the host clock (s) when it closed
  double matchEndS = -1;   // the match clock (s) when it closed (-1 = no match yet)
  int ticks = 0;
  Summary duration, lag, interval, engine;
  int late = 0;            // duration over the budget
  int behind = 0;          // started more than one budget late
  int spikes = 0;          // duration over the spike threshold
  double worstAtS = -1;    // the host clock (s) of the longest tick
  int worstStep = -1;      // its slowest step
  std::vector<double> stepMeanMs, stepMaxMs;  // per step, over the ticks that ran it
  std::uint64_t msgs[kNumTrafficKinds] = {}, bytes[kNumTrafficKinds] = {};
  std::uint32_t maxTickMsgs = 0, maxTickBytes = 0;  // the busiest tick (all kinds)
  double windowS = 0;      // the window's length (host seconds)
};

class MinuteTimer {
 public:
  // budgetMs: the tick interval (33); a spike is a tick over spikeMs (default 4 budgets, at least 100 ms).
  explicit MinuteTimer(double budgetMs = 33.0, double spikeMs = 0.0);
  void Add(const TickSample& s, double hostS);
  // Closes the window: the row (minute and clocks as given), and a fresh window.
  MinuteRow Roll(int minute, double hostS, double matchS);
  int Ticks() const { return static_cast<int>(duration_.size()); }
  double WindowStartS() const { return startS_; }
  double BudgetMs() const { return budgetMs_; }
  double SpikeMs() const { return spikeMs_; }

 private:
  double budgetMs_, spikeMs_;
  double startS_ = -1;
  std::vector<double> duration_, lag_, interval_, engine_;
  std::vector<double> stepSum_, stepMax_;
  std::vector<int> stepRuns_;
  int late_ = 0, behind_ = 0, spikes_ = 0;
  double worstMs_ = -1, worstAtS_ = -1;
  int worstStep_ = -1;
  std::uint64_t msgs_[kNumTrafficKinds] = {}, bytes_[kNumTrafficKinds] = {};
  std::uint32_t maxTickMsgs_ = 0, maxTickBytes_ = 0;
};

// "timing: minute 3 (match 180.0 s, 1800 ticks in 60.0 s): duration p50 ... ms; late 0 (> 33 ms), behind 0, spikes 0
// (> 132 ms), worst 12.3 ms at host 456.7 s (step lua); lag ...; interval ...; engine frame ...; steps (mean/max ms):
// lua 0.80/4.10, ..." (the steps whose max is at least 0.05 ms, slowest mean first). `stepNames[i]` names step i.
std::string FormatTimingLine(const MinuteRow& r, double budgetMs, double spikeMs,
                             const std::vector<std::string>& stepNames);
// "traffic: minute 3: 412.3 msg/s, 11,904 B/s; per tick: creates 0.1 msg / 31 B, deltas ...; busiest tick 140 msg /
// 9,812 B"
std::string FormatTrafficLine(const MinuteRow& r);

// ---------------------------------------------------------------------------------------------- command latency

enum CommandKind : int {
  kCmdKindMove = 0,   // move (1)
  kCmdKindAttackMove, // attack-move (9)
  kCmdKindStop,       // stop moving (2), stop (8), hold (11)
  kCmdKindAbility,    // ability (3) and dance (5: the emote ability)
  kCmdKindItem,       // ItemTransaction (C3)
  kCmdKindRankUp,     // AbilityRankUp (A7)
  kCmdKindOther,      // ping, taunt, voice line, interact
  kNumCommandKinds
};
const char* CommandKindName(int kind);

class CommandLatency {
 public:
  // timeoutMs: a command without its answer or effect that long is counted as "none" and dropped.
  explicit CommandLatency(double timeoutMs = 10000.0) : timeoutMs_(timeoutMs) {}

  // A command from player `player` (gameplay index). arrivedMs = its arrival in RakNet (< 0: unknown, the intake time
  // is used); intakeMs = when the engine took it. `hasTarget`: a move's point (for the effect test).
  // A move, attack-move or stop supersedes the player's older ones that have no effect yet (Q3c-live2: counted as
  // `superseded`, not as a timeout, and never credited with the newer command's effect: a test run's "move effect 6.8 s"
  // was a move given while the shaper was dead, credited with the next move to the same point).
  void OnIntake(int player, int kind, std::uint8_t seq, double arrivedMs, double intakeMs, bool hasTarget = false,
                float targetX = 0, float targetY = 0);
  // The first message that answers a command of `player`: the oldest unanswered one with that sequence (seq != 0) or,
  // with seq 0, the oldest one *with sequence 0* (the client's self-casts, purchases, rank-ups) of one of the kinds in
  // `kindMask` (bit = CommandKind). False if none was pending. (Q3c-live2: a seq-0 message never answers a command
  // that has its own sequence: an auto-repeating basic attack's later instances come as `B4` kind 1 with sequence 0,
  // and until then they were credited to older clicks that never started, a test run's "ability effect 9.0 s".)
  bool OnAnswer(int player, std::uint8_t seq, unsigned kindMask, double ms);
  // The command's effect, matched the same way (only answered or unanswered commands without an effect yet).
  bool OnEffect(int player, std::uint8_t seq, unsigned kindMask, double ms);
  // The pending moves of `player` (for the engine's locomotion test): kind, target, intake time, answered yet.
  struct PendingMove {
    int kind;
    std::uint8_t seq;
    float x, y;
    double intakeMs;
    bool answered;
  };
  std::vector<PendingMove> PendingMoves(int player) const;
  // Drops what is older than the timeout (counted as no answer / no effect).
  void Expire(double nowMs);

  struct KindRow {
    int n = 0;                    // commands taken in the window
    Summary queued;               // arrival -> intake (the wait for the engine's poll)
    Summary answer;               // intake -> answer
    Summary effect;               // intake -> effect
    int noAnswer = 0, noEffect = 0;  // timed out
    int superseded = 0;              // a newer move, attack-move or stop came before its effect
  };
  struct Row {
    KindRow kinds[kNumCommandKinds];
    int pending = 0;
  };
  // The window's numbers (and a fresh window; pending commands stay).
  Row Roll();
  // "commands latency: minute 3: move 12 (queued p50 .., answer .., effect ..), ability ...; pending 0", or an empty
  // string when no command was taken and none timed out.
  static std::string Format(int minute, const Row& r);

  // Q3c-live2: each command that left with its answer or effect at or after `outlierMs`, or without them (a timeout,
  // superseded), for a line of its own in the host log (soak.py latency matches them to the edge cases).
  struct Outlier {
    int player = 0, kind = 0;
    std::uint8_t seq = 0;
    double queuedMs = 0, intakeMs = 0;   // arrival -> intake; the intake (NowMs)
    double answerMs = -1, effectMs = -1;  // after the intake; -1 = none
    const char* end = "done";             // "done", "timeout", "superseded"
    std::uint8_t bySeq = 0;               // superseded: the newer command's sequence
  };
  void SetOutlierMs(double ms) { outlierMs_ = ms; }  // < 0: none collected (the default)
  std::vector<Outlier> TakeOutliers();
  // "commands latency: player 0 seq 37 attack-move (queued 0.0 ms): answer 5543.0 ms, effect none (timeout)"
  static std::string FormatOutlier(const Outlier& o);

 private:
  struct Cmd {
    int player, kind;
    std::uint8_t seq;
    double arrived, intake;
    bool hasTarget;
    float x, y;
    bool answered = false, effected = false;
    double answerMs = -1, effectMs = -1;
    const char* end = nullptr;  // "timeout", "superseded" (nullptr: answered and effected)
    std::uint8_t bySeq = 0;
  };
  Cmd* Find(int player, std::uint8_t seq, unsigned kindMask, bool answer);
  void Done();
  double timeoutMs_;
  double outlierMs_ = -1;
  std::deque<Cmd> pending_;
  std::vector<Outlier> outliers_;
  std::vector<double> queued_[kNumCommandKinds], answer_[kNumCommandKinds], effect_[kNumCommandKinds];
  int n_[kNumCommandKinds] = {}, noAnswer_[kNumCommandKinds] = {}, noEffect_[kNumCommandKinds] = {},
      superseded_[kNumCommandKinds] = {};
};

// ---------------------------------------------------------------------------------------------- the CSV and gauges

// Named values the host samples once per window (records alive, pools, the id counter), in a fixed order.
using Gauges = std::vector<std::pair<std::string, double>>;
// "records: minute 3: name value, name value, ..." (integers without decimals).
std::string FormatGauges(int minute, const Gauges& g);

// host_timing.csv: one row per window. The header names every column; the step columns are "<step>_mean_ms" and
// "<step>_max_ms", the traffic columns "<kind>_msgs"/"<kind>_bytes" (window totals), then the command latency per
// kind ("<kind>_n", "<kind>_answer_p50_ms", "<kind>_answer_max_ms", "<kind>_effect_p50_ms", "<kind>_effect_max_ms",
// "<kind>_timeouts", since Q3c-live2 "<kind>_superseded"), then the gauges (their names).
std::string CsvHeader(const std::vector<std::string>& stepNames, const Gauges& gauges);
std::string CsvRow(const MinuteRow& r, const std::vector<std::string>& stepNames, const CommandLatency::Row& c,
                   const Gauges& gauges);

}  // namespace timing
}  // namespace gms
