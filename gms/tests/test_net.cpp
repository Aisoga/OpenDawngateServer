// gms/core/net (Q3c): the endpoint's timing and its network simulator against a RakNet 4.081 test client (protocol 5,
// as the live client) on loopback, on ephemeral ports: arrival times and send timing, the simulator's delay in both
// directions, jitter and loss with every message still arriving in order, and the fast lane (a ClockSync answer that
// would wait behind a replication burst on channel 0 skips it). docs/SERVER_MODE.md *Timing and desync (Q3c)*.
#include <algorithm>
#include <chrono>
#include <cstdio>
#include <functional>
#include <string>
#include <thread>
#include <vector>

#include "MessageIdentifiers.h"
#include "RakNetTypes.h"
#include "RakPeerInterface.h"
// after RakNet's headers (winsock2.h before windows.h)
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>
#include <mmsystem.h>  // timeBeginPeriod
#pragma comment(lib, "winmm.lib")
#include "core/common/bytes.h"
#include "core/net/endpoint.h"
#include "core/timing/clock.h"
#include "tests/check.h"

using gms::Bytes;
using gms::timing::NowMs;

namespace {

// The client side: a bare RakNet peer, like the live client's transport (HIGH_PRIORITY, RELIABLE_ORDERED, channel 0).
class Peer {
 public:
  struct Got {
    Bytes msg;
    double at;
  };
  ~Peer() {
    if (peer_) {
      peer_->Shutdown(100);
      RakNet::RakPeerInterface::DestroyInstance(peer_);
    }
  }
  bool Start(uint16_t serverPort) {
    peer_ = RakNet::RakPeerInterface::GetInstance();
    RakNet::SocketDescriptor sd(0, "127.0.0.1");
    if (peer_->Startup(1, &sd, 1) != RakNet::RAKNET_STARTED) return false;
    return peer_->Connect("127.0.0.1", serverPort, nullptr, 0) == RakNet::CONNECTION_ATTEMPT_STARTED;
  }
  void Poll() {
    for (RakNet::Packet* p = peer_->Receive(); p; peer_->DeallocatePacket(p), p = peer_->Receive()) {
      const uint8_t id = p->data[0];
      if (id == ID_CONNECTION_REQUEST_ACCEPTED) {
        server_ = p->guid;
        connected_ = true;
      } else if (id >= 0x81) {
        got_.push_back(Got{Bytes(p->data, p->data + p->length), NowMs()});
      }
    }
  }
  void Send(const Bytes& m) {
    peer_->Send(reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), HIGH_PRIORITY, RELIABLE_ORDERED,
                0, server_, false);
  }
  bool Connected() const { return connected_; }
  std::vector<Got>& Inbox() { return got_; }

 private:
  RakNet::RakPeerInterface* peer_ = nullptr;
  RakNet::RakNetGUID server_;
  bool connected_ = false;
  std::vector<Got> got_;
};

// Server and client in one thread, alternately; the server's events are kept.
struct Rig {
  gms::Endpoint ep;
  Peer client;
  gms::ConnId conn = 0;
  std::vector<gms::Endpoint::Event> events;  // the server's GMS messages
  std::function<void(const gms::Endpoint::Event&)> onServerMessage;

  bool Start(const gms::NetSimOptions& sim = gms::NetSimOptions()) {
    ep.SetNetSim(sim);
    std::string err;
    if (!ep.Start("127.0.0.1", 0, 4, &err)) {
      std::printf("  endpoint: %s\n", err.c_str());
      return false;
    }
    if (!err.empty()) std::printf("  endpoint: %s\n", err.c_str());
    if (!client.Start(ep.BoundPort())) return false;
    return RunUntil([this] { return conn != 0 && client.Connected(); }, 8000);
  }
  void Step() {
    gms::Endpoint::Event e;
    while (ep.Poll(&e)) {
      if (e.kind == gms::Endpoint::Event::kConnected) conn = e.conn;
      if (e.kind != gms::Endpoint::Event::kGms) continue;
      events.push_back(e);
      if (onServerMessage) onServerMessage(e);
    }
    client.Poll();
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  bool RunUntil(const std::function<bool()>& done, int timeoutMs) {
    const double end = NowMs() + timeoutMs;
    while (NowMs() < end) {
      Step();
      if (done()) return true;
    }
    return false;
  }
};

Bytes Numbered(uint8_t wire, uint32_t i, size_t size) {
  Bytes m(size < 5 ? 5 : size, 0);
  m[0] = wire;
  m[1] = static_cast<uint8_t>(i);
  m[2] = static_cast<uint8_t>(i >> 8);
  m[3] = static_cast<uint8_t>(i >> 16);
  m[4] = static_cast<uint8_t>(i >> 24);
  return m;
}
uint32_t NumberOf(const Bytes& m) { return m.size() < 5 ? 0 : m[1] | m[2] << 8 | m[3] << 16 | static_cast<uint32_t>(m[4]) << 24; }

// Round trips of a GMS message the server echoes at its poll: client send -> server echo -> client receive (ms).
std::vector<double> EchoRoundTrips(Rig& r, int count) {
  std::vector<double> rtt;
  r.onServerMessage = [&r](const gms::Endpoint::Event& e) { r.ep.Send(e.conn, e.data); };
  for (int i = 0; i < count; ++i) {
    const size_t before = r.client.Inbox().size();
    const double t0 = NowMs();
    r.client.Send(Numbered(0xC7, 1000 + i, 9));
    if (!r.RunUntil([&r, before] { return r.client.Inbox().size() > before; }, 3000)) break;
    rtt.push_back(r.client.Inbox().back().at - t0);
  }
  r.onServerMessage = nullptr;
  return rtt;
}

// timeBeginPeriod(1) for a test's length, as the core-only host has it.
struct TimerResolution {
  TimerResolution() { timeBeginPeriod(1); }
  ~TimerResolution() { timeEndPeriod(1); }
  TimerResolution(const TimerResolution&) = delete;
  TimerResolution& operator=(const TimerResolution&) = delete;
};

double Median(std::vector<double> v) {
  if (v.empty()) return -1;
  std::sort(v.begin(), v.end());
  return v[v.size() / 2];
}

}  // namespace

GMS_TEST(NetEndpointArrivalTimesAndSendTiming) {
  Rig r;
  CHECK(r.Start());
  // client -> server: every GMS message gets its arrival in RakNet's thread, before the poll and after the send
  std::vector<double> sentAt;
  for (uint32_t i = 0; i < 20; ++i) {
    sentAt.push_back(NowMs());
    r.client.Send(Numbered(0x81, i, 12));
  }
  CHECK(r.RunUntil([&r] { return r.events.size() >= 20; }, 3000));
  int stamped = 0;
  for (const auto& e : r.events) {
    const uint32_t i = NumberOf(e.data);
    if (e.arrivedMs < 0 || i >= sentAt.size()) continue;
    ++stamped;
    CHECK(e.arrivedMs <= e.polledMs);
    CHECK(e.arrivedMs >= sentAt[i] - 0.5);
  }
  CHECK_EQ(stamped, 20);
  // server -> client: the tracked wire bytes are timed from Send() to the wire, the others in RakNet's queue
  r.ep.SetTrackedSends({0xC7});
  for (uint32_t i = 0; i < 10; ++i) r.ep.Send(r.conn, Numbered(0xC7, i, 17));
  for (uint32_t i = 0; i < 5; ++i) r.ep.Send(r.conn, Numbered(0x90, i, 30));
  CHECK(r.RunUntil([&r] { return r.client.Inbox().size() >= 15; }, 3000));
  r.ep.SampleConnections();
  const gms::Endpoint::Stats st = r.ep.TakeStats();
  CHECK_EQ(st.trackedWireMs.size(), static_cast<size_t>(10));
  for (double ms : st.trackedWireMs) CHECK(ms >= 0 && ms < 100);
  std::printf("  idle loopback, HIGH_PRIORITY: Send -> wire median %.1f ms (RakNet's update cycle), max %.1f ms\n",
              Median(st.trackedWireMs),
              st.trackedWireMs.empty() ? 0.0 : *std::max_element(st.trackedWireMs.begin(), st.trackedWireMs.end()));
  CHECK(std::all_of(st.trackedWire.begin(), st.trackedWire.end(), [](uint8_t w) { return w == 0xC7; }));
  CHECK_EQ(st.queueMs.size(), static_cast<size_t>(5));
  CHECK(st.gmsSent >= 15);
  CHECK_EQ(st.resends, static_cast<uint64_t>(0));
  CHECK_EQ(st.simOut, static_cast<uint64_t>(0));  // no simulator
  CHECK(r.ep.TakeStats().trackedWireMs.empty());  // taken
}

GMS_TEST(NetSimDelaysBothDirections) {
  // the host runs with a 1 ms timer (startup.cpp, core-only); at Windows' default 15.6 ms every RakNet update cycle
  // (WaitOnEvent(10)) and this loop's 1-ms sleep take 15.6 ms, which put about 60 ms into a loopback echo
  TimerResolution oneMs;
  Rig base;
  CHECK(base.Start());
  const std::vector<double> fast = EchoRoundTrips(base, 10);
  CHECK_EQ(fast.size(), static_cast<size_t>(10));
  gms::NetSimOptions o;
  o.delayMs = 60;
  Rig r;
  CHECK(r.Start(o));
  CHECK_EQ(r.ep.NetSim().delayMs, 60);
  // the client's message arrives 60 ms after its send (the incoming delay)
  const double t0 = NowMs();
  r.client.Send(Numbered(0x81, 7, 9));
  CHECK(r.RunUntil([&r] { return !r.events.empty(); }, 3000));
  CHECK(!r.events.empty() && r.events[0].arrivedMs - t0 >= 58.0);
  const std::vector<double> slow = EchoRoundTrips(r, 10);
  CHECK_EQ(slow.size(), static_cast<size_t>(10));
  const double minSlow = slow.empty() ? 0 : *std::min_element(slow.begin(), slow.end());
  std::printf("  echo round trip: without the simulator median %.1f ms; with 60 ms each way min %.1f, median %.1f ms\n",
              Median(fast), minSlow, Median(slow));
  CHECK(Median(fast) < 45);  // RakNet's update cycles (up to 10 ms each side) and the loops' steps
  CHECK(minSlow >= 118.0);   // 2 x 60 ms
  CHECK(Median(slow) - Median(fast) > 100 && Median(slow) - Median(fast) < 160);
  r.ep.SampleConnections();
  const gms::Endpoint::Stats st = r.ep.TakeStats();
  CHECK(st.simOut > 0 && st.simIn > 0);
  CHECK(st.simOutDropped == 0 && st.simInDropped == 0);
  CHECK(st.simOutAddedMeanMs >= 59.9 && st.simOutAddedMaxMs < 60.5);
  CHECK(st.simInAddedMeanMs >= 59.9);
  std::printf("  simulator: %llu datagrams out, %llu in, added %.1f/%.1f ms; RakNet's ping %d ms\n",
              static_cast<unsigned long long>(st.simOut), static_cast<unsigned long long>(st.simIn),
              st.simOutAddedMeanMs, st.simInAddedMeanMs, st.pingLast);
}

GMS_TEST(NetSimJitterAndLossKeepEveryMessageInOrder) {
  gms::NetSimOptions o;
  o.delayMs = 10;
  o.jitterMs = 20;
  o.lossPercent = 15;
  o.seed = 11;
  Rig r;
  CHECK(r.Start(o));
  const uint32_t n = 300;
  for (uint32_t i = 0; i < n; ++i) {
    r.ep.Send(r.conn, Numbered(0x90, i, 40));
    r.client.Send(Numbered(0xA6, i, 40));
    if (i % 10 == 0) r.Step();
  }
  CHECK(r.RunUntil([&r, n] { return r.client.Inbox().size() >= n && r.events.size() >= n; }, 30000));
  bool inOrder = r.client.Inbox().size() == n && r.events.size() == n;
  for (uint32_t i = 0; inOrder && i < n; ++i)
    inOrder = NumberOf(r.client.Inbox()[i].msg) == i && NumberOf(r.events[i].data) == i;
  CHECK(inOrder);
  const gms::Endpoint::Stats st = r.ep.TakeStats();
  std::printf("  15 %% loss each way: %llu of %llu datagrams lost out, %llu of %llu in; %llu resends by the endpoint\n",
              static_cast<unsigned long long>(st.simOutDropped), static_cast<unsigned long long>(st.simOut),
              static_cast<unsigned long long>(st.simInDropped), static_cast<unsigned long long>(st.simIn),
              static_cast<unsigned long long>(st.resends));
  CHECK(st.simOutDropped > 0 && st.simInDropped > 0);
  CHECK(st.resends > 0);  // RakNet resent what the simulator lost
}

// The question of Q3c's item 3: every GMS message goes HIGH_PRIORITY, RELIABLE_ORDERED on channel 0, so an answer sent
// after a burst waits behind it, in RakNet's send queue (congestion window) and in the client's ordering. With 50 ms
// each way and a fresh connection (RakNet's sliding window starts at one datagram and doubles per round trip) a
// 400 KB burst takes several round trips; a ClockSync answer after it arrives last. On the fast lane
// (IMMEDIATE_PRIORITY, channel 1) it goes out in the next window and isn't held for channel 0.
GMS_TEST(NetFastLaneSkipsAReplicationBurst) {
  TimerResolution oneMs;
  for (int fastLane = 0; fastLane <= 1; ++fastLane) {
    gms::NetSimOptions o;
    o.delayMs = 50;
    Rig r;
    CHECK(r.Start(o));
    if (fastLane) r.ep.SetFastLane({0xC7, 0xB4});
    r.ep.SetTrackedSends({0xC7});
    const uint32_t burst = 400;
    const double t0 = NowMs();
    for (uint32_t i = 0; i < burst; ++i) r.ep.Send(r.conn, Numbered(0x9C, i, 1000));
    r.ep.Send(r.conn, Numbered(0xC7, 99999, 17));
    CHECK(r.RunUntil([&r, burst] { return r.client.Inbox().size() >= burst + 1; }, 30000));
    int answerAt = -1;
    double answerMs = -1, lastBurstMs = -1;
    for (size_t i = 0; i < r.client.Inbox().size(); ++i) {
      const auto& g = r.client.Inbox()[i];
      if (g.msg[0] == 0xC7) {
        answerAt = static_cast<int>(i);
        answerMs = g.at - t0;
      } else {
        lastBurstMs = g.at - t0;
      }
    }
    const gms::Endpoint::Stats st = r.ep.TakeStats();
    std::printf("  %s: the answer arrived as message %d of %u, %.0f ms after the burst started (Send -> wire %.0f ms); "
                "the burst's last message at %.0f ms\n",
                fastLane ? "fast lane (C7 IMMEDIATE on channel 1)" : "channel 0 (as the host sends today)", answerAt,
                burst + 1, answerMs, st.trackedWireMs.empty() ? -1.0 : st.trackedWireMs[0], lastBurstMs);
    if (!fastLane) {
      CHECK_EQ(answerAt, static_cast<int>(burst));  // behind every message of the burst
      CHECK(answerMs >= lastBurstMs);
      CHECK(lastBurstMs > 300);  // the burst needed several round trips
    } else {
      CHECK(answerAt >= 0 && answerAt < static_cast<int>(burst));
      CHECK(answerMs < lastBurstMs - 150);
      CHECK_EQ(st.fastLane, static_cast<uint64_t>(1));
    }
  }
}
