// The frozen host's engine-free rules (roadmap H5; docs/SERVER_MODE.md *A frozen host: the watchdog and the step
// guards (H5)*, docs/HOST_CONTROL.md *A frozen host (H5)*).
//
// Testers of another Dawngate server saw "soft crashes": the world froze while the player could still walk (the
// client's own prediction) but not attack or cast. A match host looks exactly like that when its server tick stops
// (a hung Lua call, a deadlock) or when one step of the tick stops doing its work (before H5 a tick part that threw
// once was switched off for the rest of the match). Two rules decide when the host gives up:
//   StallDetector  the tick counter hasn't moved for thresholdMs since the detector was armed (the watchdog thread
//                  polls it; the first tick arms it): the tick is stalled. It fires once per stall and fires again
//                  only after the counter has moved (a stall that ends by itself, e.g. under a debugger).
//   StepGuards     one guarded step of the host's frame threw: count it, skip that run of the step, go on; the step
//                  "fails persistently" once it has thrown in every run for fatalAfterMs (and at least
//                  kMinFatalRuns runs in a row). Also which failures get a log line, a detailed log (registers and
//                  stack) and a report to dgserver (reportGMSError stepException), so a step that throws 30 times a
//                  second doesn't flood either.
// Header-only and engine-free (no Windows, no game address): the host DLL (gms/host/watchdog.cpp,
// gms/engine/guards.cpp) uses it; gms_tests (tests/test_watchdog.cpp) checks it with a made-up clock.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace gms {
namespace watchdog {

// ---------------------------------------------------------------------------------------------- the stall

class StallDetector {
 public:
  explicit StallDetector(std::int64_t thresholdMs = 10000) : thresholdMs_(thresholdMs) {}

  // Observes the tick counter at nowMs. `armed` is the engine's state (false until the first tick, or while the
  // engine says ticks are not expected). Returns true once per stall: armed, and the counter unchanged for
  // thresholdMs (since it last moved or since the detector was armed, whichever is later). thresholdMs <= 0: never.
  bool Check(std::int64_t nowMs, std::uint32_t ticks, bool armed) {
    if (!armed) {
      armed_ = false;
      return false;
    }
    if (!armed_ || ticks != lastTicks_) {  // armed now, or the tick moved: the age starts again
      armed_ = true;
      lastTicks_ = ticks;
      lastChangeMs_ = nowMs;
      fired_ = false;
      return false;
    }
    if (fired_ || thresholdMs_ <= 0 || nowMs - lastChangeMs_ < thresholdMs_) return false;
    fired_ = true;
    return true;
  }

  // How long the counter has stood still (0 when not armed).
  std::int64_t SinceChangeMs(std::int64_t nowMs) const { return armed_ ? nowMs - lastChangeMs_ : 0; }
  std::uint32_t LastTicks() const { return lastTicks_; }
  bool Armed() const { return armed_; }
  bool Fired() const { return fired_; }
  std::int64_t ThresholdMs() const { return thresholdMs_; }

 private:
  std::int64_t thresholdMs_;
  bool armed_ = false;
  bool fired_ = false;
  std::uint32_t lastTicks_ = 0;
  std::int64_t lastChangeMs_ = 0;
};

// ---------------------------------------------------------------------------------------------- the steps

// What to do with one failure of a guarded step.
struct StepVerdict {
  bool log = false;          // one log line ("skipped this run"): the first kLogFirst of a step, then every kLogEvery-th
  bool detail = false;       // the exception in full (registers, return addresses): the first kDetailFirst of a step
  bool report = false;       // a reportGMSError stepException to dgserver: the 1st, 10th, 100th, ... of a step
  bool fatal = false;        // the step fails persistently (see StepGuards); true once per step
  std::uint32_t count = 0;   // this step's failures so far, this one included
  std::uint32_t inARow = 0;  // its failed runs in a row, this one included
  std::int64_t failingForMs = 0;  // since the first of those
};

class StepGuards {
 public:
  static constexpr std::uint32_t kLogFirst = 20;
  static constexpr std::uint32_t kLogEvery = 100;
  static constexpr std::uint32_t kDetailFirst = 3;
  static constexpr std::uint32_t kMinFatalRuns = 10;

  // `steps` guarded steps; fatalAfterMs <= 0: a step is skipped run after run but never fails persistently.
  explicit StepGuards(int steps = 0, std::int64_t fatalAfterMs = 10000)
      : steps_(steps > 0 ? steps : 0), fatalAfterMs_(fatalAfterMs) {}

  void SetFatalAfterMs(std::int64_t ms) { fatalAfterMs_ = ms; }
  std::int64_t FatalAfterMs() const { return fatalAfterMs_; }
  int Steps() const { return static_cast<int>(steps_.size()); }

  // Whether the next failure of `step` gets the detailed log (asked by the exception filter, before Failed counts it).
  bool DetailNext(int step) const { return Valid(step) && steps_[step].count < kDetailFirst; }

  // A run of `step` threw at nowMs.
  StepVerdict Failed(int step, std::int64_t nowMs) {
    StepVerdict v;
    if (!Valid(step)) return v;
    State& s = steps_[step];
    if (s.inARow == 0) s.firstFailMs = nowMs;
    ++s.count;
    ++s.inARow;
    ++total_;
    v.count = s.count;
    v.inARow = s.inARow;
    v.failingForMs = nowMs - s.firstFailMs;
    v.log = s.count <= kLogFirst || s.count % kLogEvery == 0;
    v.detail = s.count <= kDetailFirst;
    v.report = IsPowerOfTen(s.count);
    if (!s.fatal && fatalAfterMs_ > 0 && s.inARow >= kMinFatalRuns && v.failingForMs >= fatalAfterMs_) {
      s.fatal = true;
      v.fatal = true;
    }
    return v;
  }

  // A run of `step` went through: its failures in a row end.
  void Succeeded(int step) {
    if (Valid(step)) steps_[step].inARow = 0;
  }

  std::uint32_t Count(int step) const { return Valid(step) ? steps_[step].count : 0; }
  std::uint32_t InARow(int step) const { return Valid(step) ? steps_[step].inARow : 0; }
  bool Fatal(int step) const { return Valid(step) && steps_[step].fatal; }
  std::uint32_t Total() const { return total_; }

  // "lua 3 (2 in a row), gms 1" for the steps that have failed (names[i] for step i); "none" when none has.
  std::string Describe(const char* const* names) const {
    std::string out;
    for (int i = 0; i < Steps(); ++i) {
      const State& s = steps_[i];
      if (!s.count) continue;
      if (!out.empty()) out += ", ";
      out += (names && names[i]) ? names[i] : ("step " + std::to_string(i));
      out += " " + std::to_string(s.count);
      if (s.inARow) out += " (" + std::to_string(s.inARow) + " in a row)";
      if (s.fatal) out += " FAILING";
    }
    return out.empty() ? "none" : out;
  }

  static bool IsPowerOfTen(std::uint32_t n) {
    if (n == 0) return false;
    while (n % 10 == 0) n /= 10;
    return n == 1;
  }

 private:
  struct State {
    std::uint32_t count = 0;
    std::uint32_t inARow = 0;
    std::int64_t firstFailMs = 0;
    bool fatal = false;
  };
  bool Valid(int step) const { return step >= 0 && step < Steps(); }

  std::vector<State> steps_;
  std::int64_t fatalAfterMs_;
  std::uint32_t total_ = 0;
};

}  // namespace watchdog
}  // namespace gms
