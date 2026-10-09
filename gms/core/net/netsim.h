// The match host's network simulator (roadmap Q3c; docs/SERVER_MODE.md *Timing and desync (Q3c)*): artificial delay,
// jitter and loss inside our own endpoint, so a test on this PC can show what a player at 50-150 ms sees (the
// client's prediction against the server's cast holds, sliding, walking backwards) without a VM.
//
// RakNet 4.081 has a simulator (RakPeer::ApplyNetworkSimulator), but it is compiled only with _DEBUG (RakPeer.cpp
// 2944, ReliabilityLayer.cpp 2257), and our raknet_p5 is a Release build. So the endpoint (endpoint.cpp) does it at
// RakNet's socket layer, datagram by datagram, through 4.081's public hooks: SocketLayerOverride (RNS2_Windows) for
// what goes out, SetIncomingDatagramEventHandler for what comes in, both queued here and released from RakNet's own
// update thread (SetUserUpdateThread, RakNetRecvFrom). RakNet's acknowledgements, resends, pings and congestion
// control all see the simulated link, as they would a real one.
//
// DelayLine is one direction's rule, engine- and RakNet-free (gms_tests checks it with made-up clocks):
//   - a datagram is lost with lossPercent (RakNet's reliability resends it);
//   - else it is due delayMs + U[0, jitterMs] after it was handed in, but never before the datagram handed in
//     before it: a jittery path that keeps the order (a reliable ordered stream would hold a reordered message back
//     anyway; and a reordering would make RakNet NAK and resend at once).
#pragma once

#include <cstdint>

namespace gms {

struct NetSimOptions {
  int delayMs = 0;           // added to every datagram, each direction (so a round trip grows by 2 x delayMs)
  int jitterMs = 0;          // plus 0 .. jitterMs, uniformly, per datagram (order kept)
  double lossPercent = 0.0;  // datagrams lost, each direction
  std::uint32_t seed = 1;    // the random sequence (jitter, loss)
  bool Active() const { return delayMs > 0 || jitterMs > 0 || lossPercent > 0.0; }
};

class DelayLine {
 public:
  explicit DelayLine(const NetSimOptions& o = NetSimOptions()) : o_(o), rng_(o.seed ? o.seed : 0x9E3779B9u) {}

  // A datagram handed in at nowMs: false = lost; else true and its due time.
  bool Admit(double nowMs, double* dueMs) {
    ++admitted_;
    if (o_.lossPercent > 0.0 && Uniform() * 100.0 < o_.lossPercent) {
      ++dropped_;
      return false;
    }
    double due = nowMs + (o_.delayMs > 0 ? o_.delayMs : 0);
    if (o_.jitterMs > 0) due += Uniform() * o_.jitterMs;
    if (due < lastDue_) due = lastDue_;  // never overtakes the one before
    lastDue_ = due;
    const double added = due - nowMs;
    addedSum_ += added;
    if (added > addedMax_) addedMax_ = added;
    *dueMs = due;
    return true;
  }

  const NetSimOptions& Options() const { return o_; }
  std::uint64_t Admitted() const { return admitted_; }
  std::uint64_t Dropped() const { return dropped_; }
  double AddedMeanMs() const { return admitted_ > dropped_ ? addedSum_ / static_cast<double>(admitted_ - dropped_) : 0; }
  double AddedMaxMs() const { return addedMax_; }

 private:
  // xorshift32 in [0, 1)
  double Uniform() {
    rng_ ^= rng_ << 13;
    rng_ ^= rng_ >> 17;
    rng_ ^= rng_ << 5;
    return (rng_ >> 8) / 16777216.0;
  }
  NetSimOptions o_;
  std::uint32_t rng_;
  double lastDue_ = 0;
  std::uint64_t admitted_ = 0, dropped_ = 0;
  double addedSum_ = 0, addedMax_ = 0;
};

}  // namespace gms
