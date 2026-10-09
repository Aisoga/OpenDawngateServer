// Timing, traffic and command latency over a long match (Q3c): see timing.h.
#include "core/timing/timing.h"

#include <algorithm>
#include <cmath>
#include <cstdio>

namespace gms {
namespace timing {

Summary Summarize(std::vector<double> v) {
  Summary s;
  s.n = static_cast<int>(v.size());
  if (v.empty()) return s;
  std::sort(v.begin(), v.end());
  double sum = 0;
  for (double x : v) sum += x;
  s.mean = sum / v.size();
  // nearest rank: the smallest value with at least p % of the samples at or below it
  auto rank = [&v](double p) {
    std::size_t k = static_cast<std::size_t>(std::ceil(p / 100.0 * v.size()));
    if (k < 1) k = 1;
    if (k > v.size()) k = v.size();
    return v[k - 1];
  };
  s.p50 = rank(50);
  s.p90 = rank(90);
  s.p99 = rank(99);
  s.max = v.back();
  return s;
}

std::string FormatSummary(const Summary& s) {
  if (s.n == 0) return "none";
  char b[120];
  std::snprintf(b, sizeof b, "p50 %.1f, p90 %.1f, p99 %.1f, max %.1f", s.p50, s.p90, s.p99, s.max);
  return b;
}

const char* TrafficKindName(int kind) {
  static const char* const kNames[kNumTrafficKinds] = {"creates", "deltas", "forwarded", "players",
                                                       "fog",     "answers", "session",  "other"};
  return kind >= 0 && kind < kNumTrafficKinds ? kNames[kind] : "?";
}

// ---------------------------------------------------------------------------------------------- MinuteTimer

MinuteTimer::MinuteTimer(double budgetMs, double spikeMs)
    : budgetMs_(budgetMs > 0 ? budgetMs : 33.0),
      spikeMs_(spikeMs > 0 ? spikeMs : std::max(100.0, 4.0 * (budgetMs > 0 ? budgetMs : 33.0))) {}

void MinuteTimer::Add(const TickSample& s, double hostS) {
  if (startS_ < 0) startS_ = hostS;
  duration_.push_back(s.durationMs);
  lag_.push_back(s.lagMs);
  if (s.intervalMs > 0) interval_.push_back(s.intervalMs);
  if (s.engineMs > 0) engine_.push_back(s.engineMs);
  if (s.durationMs > budgetMs_) ++late_;
  if (s.lagMs > budgetMs_) ++behind_;
  if (s.durationMs > spikeMs_) ++spikes_;
  if (stepSum_.size() < s.stepMs.size()) {
    stepSum_.resize(s.stepMs.size(), 0.0);
    stepMax_.resize(s.stepMs.size(), 0.0);
    stepRuns_.resize(s.stepMs.size(), 0);
  }
  int slowest = -1;
  for (std::size_t i = 0; i < s.stepMs.size(); ++i) {
    const double ms = s.stepMs[i];
    if (ms <= 0) continue;
    stepSum_[i] += ms;
    ++stepRuns_[i];
    if (ms > stepMax_[i]) stepMax_[i] = ms;
    if (slowest < 0 || ms > s.stepMs[static_cast<std::size_t>(slowest)]) slowest = static_cast<int>(i);
  }
  if (s.durationMs > worstMs_) {
    worstMs_ = s.durationMs;
    worstAtS_ = hostS;
    worstStep_ = slowest;
  }
  std::uint32_t tm = 0, tb = 0;
  for (int k = 0; k < kNumTrafficKinds; ++k) {
    msgs_[k] += s.msgs[k];
    bytes_[k] += s.bytes[k];
    tm += s.msgs[k];
    tb += s.bytes[k];
  }
  maxTickMsgs_ = std::max(maxTickMsgs_, tm);
  maxTickBytes_ = std::max(maxTickBytes_, tb);
}

MinuteRow MinuteTimer::Roll(int minute, double hostS, double matchS) {
  MinuteRow r;
  r.minute = minute;
  r.hostEndS = hostS;
  r.matchEndS = matchS;
  r.ticks = static_cast<int>(duration_.size());
  r.duration = Summarize(duration_);
  r.lag = Summarize(lag_);
  r.interval = Summarize(interval_);
  r.engine = Summarize(engine_);
  r.late = late_;
  r.behind = behind_;
  r.spikes = spikes_;
  r.worstAtS = worstAtS_;
  r.worstStep = worstStep_;
  r.stepMeanMs.assign(stepSum_.size(), 0.0);
  r.stepMaxMs = stepMax_;
  for (std::size_t i = 0; i < stepSum_.size(); ++i)
    if (stepRuns_[i]) r.stepMeanMs[i] = stepSum_[i] / stepRuns_[i];
  for (int k = 0; k < kNumTrafficKinds; ++k) {
    r.msgs[k] = msgs_[k];
    r.bytes[k] = bytes_[k];
  }
  r.maxTickMsgs = maxTickMsgs_;
  r.maxTickBytes = maxTickBytes_;
  r.windowS = startS_ >= 0 ? hostS - startS_ : 0;
  *this = MinuteTimer(budgetMs_, spikeMs_);
  startS_ = hostS;
  return r;
}

namespace {

std::string Thousands(std::uint64_t v) {
  std::string s = std::to_string(v);
  for (int i = static_cast<int>(s.size()) - 3; i > 0; i -= 3) s.insert(static_cast<std::size_t>(i), ",");
  return s;
}

std::string StepName(const std::vector<std::string>& names, int i) {
  return i >= 0 && static_cast<std::size_t>(i) < names.size() ? names[static_cast<std::size_t>(i)]
                                                              : "step " + std::to_string(i);
}

}  // namespace

std::string FormatTimingLine(const MinuteRow& r, double budgetMs, double spikeMs,
                             const std::vector<std::string>& stepNames) {
  char head[200];
  if (r.matchEndS >= 0)
    std::snprintf(head, sizeof head, "timing: minute %d (match %.1f s, %d ticks in %.1f s): ", r.minute, r.matchEndS,
                  r.ticks, r.windowS);
  else
    std::snprintf(head, sizeof head, "timing: minute %d (before the match, host %.1f s, %d ticks in %.1f s): ",
                  r.minute, r.hostEndS, r.ticks, r.windowS);
  std::string s = head;
  char b[400];
  std::snprintf(b, sizeof b, "duration %s ms; late %d (> %.0f ms), behind %d, spikes %d (> %.0f ms)",
                FormatSummary(r.duration).c_str(), r.late, budgetMs, r.behind, r.spikes, spikeMs);
  s += b;
  if (r.ticks) {
    std::snprintf(b, sizeof b, ", worst %.1f ms at host %.1f s (step %s)", r.duration.max, r.worstAtS,
                  StepName(stepNames, r.worstStep).c_str());
    s += b;
  }
  s += "; lag " + FormatSummary(r.lag) + " ms; interval " + FormatSummary(r.interval) + " ms";
  if (r.engine.n) s += "; engine frame " + FormatSummary(r.engine) + " ms";
  std::vector<int> order;
  for (std::size_t i = 0; i < r.stepMaxMs.size(); ++i)
    if (r.stepMaxMs[i] >= 0.05) order.push_back(static_cast<int>(i));
  std::sort(order.begin(), order.end(), [&r](int a, int c) {
    return r.stepMeanMs[static_cast<std::size_t>(a)] > r.stepMeanMs[static_cast<std::size_t>(c)];
  });
  if (!order.empty()) {
    s += "; steps (mean/max ms):";
    for (std::size_t k = 0; k < order.size(); ++k) {
      const std::size_t i = static_cast<std::size_t>(order[k]);
      std::snprintf(b, sizeof b, "%s %s %.2f/%.2f", k ? "," : "", StepName(stepNames, order[k]).c_str(),
                    r.stepMeanMs[i], r.stepMaxMs[i]);
      s += b;
    }
  }
  return s;
}

std::string FormatTrafficLine(const MinuteRow& r) {
  std::uint64_t tm = 0, tb = 0;
  for (int k = 0; k < kNumTrafficKinds; ++k) {
    tm += r.msgs[k];
    tb += r.bytes[k];
  }
  const double secs = r.windowS > 0 ? r.windowS : 1.0;
  const double ticks = r.ticks > 0 ? r.ticks : 1.0;
  char b[300];
  std::snprintf(b, sizeof b, "traffic: minute %d: %.1f msg/s, %s B/s; per tick:", r.minute, tm / secs,
                Thousands(static_cast<std::uint64_t>(tb / secs + 0.5)).c_str());
  std::string s = b;
  bool any = false;
  for (int k = 0; k < kNumTrafficKinds; ++k) {
    if (!r.msgs[k]) continue;
    std::snprintf(b, sizeof b, "%s %s %.2f msg / %.0f B", any ? "," : "", TrafficKindName(k), r.msgs[k] / ticks,
                  r.bytes[k] / ticks);
    s += b;
    any = true;
  }
  if (!any) s += " nothing sent";
  std::snprintf(b, sizeof b, "; busiest tick %u msg / %s B", r.maxTickMsgs, Thousands(r.maxTickBytes).c_str());
  return s + b;
}

// ---------------------------------------------------------------------------------------------- CommandLatency

const char* CommandKindName(int kind) {
  static const char* const kNames[kNumCommandKinds] = {"move", "attack-move", "stop", "ability",
                                                       "item", "rank-up",     "other"};
  return kind >= 0 && kind < kNumCommandKinds ? kNames[kind] : "?";
}

void CommandLatency::OnIntake(int player, int kind, std::uint8_t seq, double arrivedMs, double intakeMs,
                              bool hasTarget, float targetX, float targetY) {
  if (kind < 0 || kind >= kNumCommandKinds) kind = kCmdKindOther;
  Cmd c;
  c.player = player;
  c.kind = kind;
  c.seq = seq;
  c.arrived = arrivedMs >= 0 && arrivedMs <= intakeMs ? arrivedMs : intakeMs;
  c.intake = intakeMs;
  c.hasTarget = hasTarget;
  c.x = targetX;
  c.y = targetY;
  ++n_[kind];
  queued_[kind].push_back(c.intake - c.arrived);
  pending_.push_back(c);
}

CommandLatency::Cmd* CommandLatency::Find(int player, std::uint8_t seq, unsigned kindMask, bool answer) {
  for (Cmd& c : pending_) {
    if (c.player != player || (answer ? c.answered : c.effected)) continue;
    if (seq != 0 ? c.seq == seq : ((kindMask >> c.kind) & 1u) != 0) return &c;
  }
  return nullptr;
}

void CommandLatency::Done() {
  pending_.erase(std::remove_if(pending_.begin(), pending_.end(),
                                [](const Cmd& c) { return c.answered && c.effected; }),
                 pending_.end());
}

bool CommandLatency::OnAnswer(int player, std::uint8_t seq, unsigned kindMask, double ms) {
  Cmd* c = Find(player, seq, kindMask, true);
  if (!c) return false;
  c->answered = true;
  answer_[c->kind].push_back(ms - c->intake);
  Done();
  return true;
}

bool CommandLatency::OnEffect(int player, std::uint8_t seq, unsigned kindMask, double ms) {
  Cmd* c = Find(player, seq, kindMask, false);
  if (!c) return false;
  c->effected = true;
  effect_[c->kind].push_back(ms - c->intake);
  Done();
  return true;
}

std::vector<CommandLatency::PendingMove> CommandLatency::PendingMoves(int player) const {
  std::vector<PendingMove> v;
  for (const Cmd& c : pending_)
    if (c.player == player && !c.effected &&
        (c.kind == kCmdKindMove || c.kind == kCmdKindAttackMove || c.kind == kCmdKindStop))
      v.push_back(PendingMove{c.kind, c.seq, c.x, c.y, c.intake});
  return v;
}

void CommandLatency::Expire(double nowMs) {
  for (Cmd& c : pending_) {
    if (nowMs - c.intake < timeoutMs_) continue;
    if (!c.answered) ++noAnswer_[c.kind];
    if (!c.effected) ++noEffect_[c.kind];
    c.answered = c.effected = true;
  }
  Done();
}

CommandLatency::Row CommandLatency::Roll() {
  Row r;
  for (int k = 0; k < kNumCommandKinds; ++k) {
    KindRow& kr = r.kinds[k];
    kr.n = n_[k];
    kr.queued = Summarize(queued_[k]);
    kr.answer = Summarize(answer_[k]);
    kr.effect = Summarize(effect_[k]);
    kr.noAnswer = noAnswer_[k];
    kr.noEffect = noEffect_[k];
    queued_[k].clear();
    answer_[k].clear();
    effect_[k].clear();
    n_[k] = noAnswer_[k] = noEffect_[k] = 0;
  }
  r.pending = static_cast<int>(pending_.size());
  return r;
}

std::string CommandLatency::Format(int minute, const Row& r) {
  std::string s;
  for (int k = 0; k < kNumCommandKinds; ++k) {
    const KindRow& kr = r.kinds[k];
    if (!kr.n && !kr.answer.n && !kr.effect.n && !kr.noAnswer && !kr.noEffect) continue;
    char b[400];
    std::snprintf(b, sizeof b, "%s%s %d (queued %s; answer %s; effect %s", s.empty() ? "" : "; ", CommandKindName(k),
                  kr.n, FormatSummary(kr.queued).c_str(), FormatSummary(kr.answer).c_str(),
                  FormatSummary(kr.effect).c_str());
    s += b;
    if (kr.noAnswer || kr.noEffect) {
      std::snprintf(b, sizeof b, "; no answer %d, no effect %d", kr.noAnswer, kr.noEffect);
      s += b;
    }
    s += ")";
  }
  if (s.empty()) return s;
  return "commands latency: minute " + std::to_string(minute) + " (ms): " + s + "; pending " +
         std::to_string(r.pending);
}

// ---------------------------------------------------------------------------------------------- gauges and CSV

namespace {
std::string Num(double v) {
  char b[40];
  if (v == std::floor(v) && std::fabs(v) < 1e15) std::snprintf(b, sizeof b, "%.0f", v);
  else std::snprintf(b, sizeof b, "%.3f", v);
  return b;
}
}  // namespace

std::string FormatGauges(int minute, const Gauges& g) {
  std::string s = "records: minute " + std::to_string(minute) + ":";
  for (std::size_t i = 0; i < g.size(); ++i) s += (i ? ", " : " ") + g[i].first + " " + Num(g[i].second);
  return s;
}

std::string CsvHeader(const std::vector<std::string>& stepNames, const Gauges& gauges) {
  std::string h =
      "minute,host_s,match_s,window_s,ticks,dur_p50_ms,dur_p90_ms,dur_p99_ms,dur_max_ms,late,behind,spikes,"
      "lag_p99_ms,lag_max_ms,interval_p50_ms,interval_p99_ms,interval_max_ms,engine_p50_ms,engine_max_ms,"
      "busiest_tick_msgs,busiest_tick_bytes";
  for (const std::string& n : stepNames) h += "," + n + "_mean_ms," + n + "_max_ms";
  for (int k = 0; k < kNumTrafficKinds; ++k)
    h += std::string(",") + TrafficKindName(k) + "_msgs," + TrafficKindName(k) + "_bytes";
  for (int k = 0; k < kNumCommandKinds; ++k) {
    std::string n = CommandKindName(k);
    std::replace(n.begin(), n.end(), '-', '_');
    h += "," + n + "_n," + n + "_queued_max_ms," + n + "_answer_p50_ms," + n + "_answer_max_ms," + n +
         "_effect_p50_ms," + n + "_effect_max_ms," + n + "_timeouts";
  }
  for (const auto& kv : gauges) h += "," + kv.first;
  return h;
}

std::string CsvRow(const MinuteRow& r, const std::vector<std::string>& stepNames, const CommandLatency::Row& c,
                   const Gauges& gauges) {
  char b[600];
  std::snprintf(b, sizeof b, "%d,%.1f,%.1f,%.1f,%d,%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%.2f,%u,%u",
                r.minute, r.hostEndS, r.matchEndS, r.windowS, r.ticks, r.duration.p50, r.duration.p90, r.duration.p99,
                r.duration.max, r.late, r.behind, r.spikes, r.lag.p99, r.lag.max, r.interval.p50, r.interval.p99,
                r.interval.max, r.engine.p50, r.engine.max, r.maxTickMsgs, r.maxTickBytes);
  std::string s = b;
  for (std::size_t i = 0; i < stepNames.size(); ++i) {
    const double mean = i < r.stepMeanMs.size() ? r.stepMeanMs[i] : 0.0;
    const double mx = i < r.stepMaxMs.size() ? r.stepMaxMs[i] : 0.0;
    std::snprintf(b, sizeof b, ",%.3f,%.3f", mean, mx);
    s += b;
  }
  for (int k = 0; k < kNumTrafficKinds; ++k)
    s += "," + std::to_string(r.msgs[k]) + "," + std::to_string(r.bytes[k]);
  for (int k = 0; k < kNumCommandKinds; ++k) {
    const CommandLatency::KindRow& kr = c.kinds[k];
    std::snprintf(b, sizeof b, ",%d,%.1f,%.1f,%.1f,%.1f,%.1f,%d", kr.n, kr.queued.max, kr.answer.p50, kr.answer.max,
                  kr.effect.p50, kr.effect.max, kr.noAnswer + kr.noEffect);
    s += b;
  }
  for (const auto& kv : gauges) s += "," + Num(kv.second);
  return s;
}

}  // namespace timing
}  // namespace gms
