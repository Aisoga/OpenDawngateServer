// gms/core/net: the match host's network limits. NetLimiter's rules with made-up clocks (datagram and byte
// rates per address and IP, offline datagrams, drop then kick, the block, GMS messages per connection, connections per
// IP, the hello timeout, bounded tables), then the endpoint with the limits on against RakNet 4.081 test clients on
// loopback: a normal client's traffic passes, a message flood is closed, a second connection from one IP over the cap,
// a silent connection, and a junk flood from another loopback address (127.0.9.1) dropped while a client connects.
//.
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
#include <winsock2.h>
#include <ws2tcpip.h>
#include "core/common/bytes.h"
#include "core/common/log.h"
#include "core/net/endpoint.h"
#include "core/net/limits.h"
#include "core/timing/clock.h"
#include "tests/check.h"
#pragma comment(lib, "ws2_32.lib")

using gms::Bytes;
using gms::NetLimiter;
using gms::NetLimitOptions;
using gms::timing::NowMs;

namespace {

constexpr uint32_t kIp1 = 0x7F000001, kIp2 = 0x0A000002;

NetLimitOptions On() {
  NetLimitOptions o;
  o.on = true;
  return o;
}

}  // namespace

GMS_TEST(LimitsOffPassesEverything) {
  NetLimiter l;  // on = false
  for (int i = 0; i < 100000; ++i) CHECK_EQ(l.OnDatagram(kIp1, 1000, 1400, i % 2 == 0, 0), NetLimiter::kPass);
  CHECK(l.OnConnected(1, kIp1, 1000, 0));
  CHECK_EQ(l.OnMessage(1, 10, 0), NetLimiter::kPass);
  CHECK(l.Silent(1e9).empty());
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.datagrams, static_cast<uint64_t>(100000));
  CHECK_EQ(t.droppedRate + t.droppedOffline, static_cast<uint64_t>(0));
}

GMS_TEST(LimitsDatagramRatePerAddressAndIp) {
  NetLimitOptions o = On();
  o.datagramsPerSec = 10;
  o.datagramBurst = 20;
  o.ipFactor = 2;  // the IP: 20/s, burst 40
  NetLimiter l(o);
  int passed = 0;
  for (int i = 0; i < 30; ++i) passed += l.OnDatagram(kIp1, 1000, 100, false, 1000) == NetLimiter::kPass;
  CHECK_EQ(passed, 20);
  // a second port of the IP has its own address bucket but shares the IP's (40, 20 of it taken)
  passed = 0;
  for (int i = 0; i < 30; ++i) passed += l.OnDatagram(kIp1, 1001, 100, false, 1000) == NetLimiter::kPass;
  CHECK_EQ(passed, 20);
  CHECK_EQ(l.OnDatagram(kIp1, 1002, 100, false, 1000), NetLimiter::kDrop);  // the IP's burst is used up
  CHECK_EQ(l.OnDatagram(kIp2, 1000, 100, false, 1000), NetLimiter::kPass);  // another IP
  // a second later: 10 more for the address (the IP got 20)
  passed = 0;
  for (int i = 0; i < 30; ++i) passed += l.OnDatagram(kIp1, 1000, 100, false, 2000) == NetLimiter::kPass;
  CHECK_EQ(passed, 10);
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.droppedRate, static_cast<uint64_t>(10 + 10 + 1 + 20));
  CHECK_EQ(t.maxDatagramsPerS, 30u);
  CHECK_EQ(t.kickedRate, static_cast<uint64_t>(0));  // nobody connected: dropped only
}

GMS_TEST(LimitsBytesAndOffline) {
  NetLimitOptions o = On();
  o.bytesPerSec = 10000;
  o.byteBurst = 20000;
  o.offlinePerSec = 4;
  o.offlineBurst = 16;
  NetLimiter l(o);
  int passed = 0;
  for (int i = 0; i < 20; ++i) passed += l.OnDatagram(kIp1, 1000, 1400, false, 0) == NetLimiter::kPass;
  CHECK_EQ(passed, 14);  // 14 x 1,400 = 19,600 of 20,000
  passed = 0;
  for (int i = 0; i < 40; ++i) passed += l.OnDatagram(kIp2, static_cast<uint16_t>(2000 + i), 30, true, 0) == NetLimiter::kPass;
  CHECK_EQ(passed, 16);  // offline datagrams: per IP, whatever the port
  CHECK_EQ(l.OnDatagram(kIp2, 3000, 30, true, 250), NetLimiter::kPass);  // 4/s: one more after 250 ms
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.droppedOffline, static_cast<uint64_t>(24));
  CHECK_EQ(t.droppedRate, static_cast<uint64_t>(6));
}

// Drop, then kick: a connected address with drops in kickAfterS different seconds is kicked and blocked.
GMS_TEST(LimitsDropThenKick) {
  NetLimitOptions o = On();
  o.datagramsPerSec = 10;
  o.datagramBurst = 10;
  o.kickAfterS = 3;
  o.blockS = 30;
  NetLimiter l(o);
  CHECK(l.OnConnected(7, kIp1, 1000, 0));
  NetLimiter::Verdict last = NetLimiter::kPass;
  int kicks = 0;
  for (int sec = 0; sec < 3; ++sec)
    for (int i = 0; i < 40; ++i) {
      last = l.OnDatagram(kIp1, 1000, 100, false, sec * 1000.0 + i);
      kicks += last == NetLimiter::kKick;
    }
  CHECK_EQ(kicks, 1);
  // blocked now: everything dropped, for 30 s
  CHECK_EQ(l.OnDatagram(kIp1, 1000, 10, false, 5000), NetLimiter::kDrop);
  CHECK_EQ(l.OnDatagram(kIp1, 1000, 10, false, 33000), NetLimiter::kPass);
  // drops in two seconds only (a burst): no kick
  NetLimiter m(o);
  CHECK(m.OnConnected(8, kIp1, 1000, 0));
  for (int sec = 0; sec < 2; ++sec)
    for (int i = 0; i < 40; ++i) CHECK(m.OnDatagram(kIp1, 1000, 100, false, sec * 1000.0 + i) != NetLimiter::kKick);
  // ... and drops spread over more than the window don't add up
  for (int k = 0; k < 4; ++k)
    for (int i = 0; i < 40; ++i)
      CHECK(m.OnDatagram(kIp1, 1000, 100, false, 20000.0 + k * 11000.0 + i) != NetLimiter::kKick);
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.kickedRate, static_cast<uint64_t>(1));
  CHECK(t.droppedBlocked >= 1);  // the rest of the third second's datagrams and the one at 5 s
}

GMS_TEST(LimitsMessagesPerConnection) {
  NetLimitOptions o = On();
  o.messagesPerSec = 5;
  o.messageBurst = 10;
  o.kickAfterS = 2;
  NetLimiter l(o);
  CHECK(l.OnConnected(1, kIp1, 1000, 0));
  int passed = 0, kicked = 0;
  for (int i = 0; i < 20; ++i) {
    const NetLimiter::Verdict v = l.OnMessage(1, 20, 100);
    passed += v == NetLimiter::kPass;
    kicked += v == NetLimiter::kKick;
  }
  CHECK_EQ(passed, 10);
  CHECK_EQ(kicked, 0);
  CHECK_EQ(l.OnMessage(1, 20, 1100), NetLimiter::kPass);  // 5 more a second later
  for (int i = 0; i < 4; ++i) l.OnMessage(1, 20, 1100);
  CHECK_EQ(l.OnMessage(1, 20, 1100), NetLimiter::kKick);  // the second second with a drop
  CHECK_EQ(l.OnMessage(99, 20, 0), NetLimiter::kPass);    // not a known connection
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.droppedMessages, static_cast<uint64_t>(11));
  CHECK_EQ(t.maxMessagesPerS, 20u);
}

GMS_TEST(LimitsConnectionsPerIpAndSilent) {
  NetLimitOptions o = On();
  o.connectionsPerIp = 2;
  o.helloTimeoutS = 5;
  NetLimiter l(o);
  CHECK(l.OnConnected(1, kIp1, 1000, 0));
  CHECK(l.OnConnected(2, kIp1, 1001, 0));
  CHECK(!l.OnConnected(3, kIp1, 1002, 0));
  CHECK(l.OnConnected(4, kIp2, 1000, 0));
  l.OnDisconnected(1);
  CHECK(l.OnConnected(5, kIp1, 1003, 1000));
  l.OnMessage(2, 10, 2000);  // 2 spoke
  CHECK(l.Silent(4000).empty());
  std::vector<uint64_t> s = l.Silent(5500);  // 4 (connected at 0) is silent; 5 (at 1,000) not yet
  CHECK_EQ(s.size(), static_cast<size_t>(1));
  CHECK(!s.empty() && s[0] == 4);
  CHECK(l.Silent(5800).empty());  // at most once a second
  s = l.Silent(6600);
  CHECK_EQ(s.size(), static_cast<size_t>(1));
  CHECK(!s.empty() && s[0] == 5);
  CHECK(l.Silent(60000).empty());  // each reported once
  const NetLimiter::Totals t = l.Take();
  CHECK_EQ(t.kickedCap, static_cast<uint64_t>(1));
  CHECK_EQ(t.kickedSilent, static_cast<uint64_t>(2));
}

// A flood from more addresses than maxTracked: the tables stay bounded, a connected address keeps its state.
GMS_TEST(LimitsBoundedTables) {
  NetLimitOptions o = On();
  o.maxTracked = 100;
  NetLimiter l(o);
  CHECK(l.OnConnected(1, kIp2, 5000, 0));
  l.OnDatagram(kIp2, 5000, 10, false, 0);
  for (uint32_t i = 0; i < 5000; ++i) l.OnDatagram(0x0B000000 + i, 1000, 10, true, i * 0.1);
  CHECK(l.Take().tracked <= 100);
  CHECK_EQ(l.OnDatagram(kIp2, 5000, 10, false, 600), NetLimiter::kPass);
  // two seconds later the idle ones are forgotten and new addresses get in again
  CHECK_EQ(l.OnDatagram(0x0C000001, 1000, 10, true, 3000), NetLimiter::kPass);
}

// The tunnel gateway's IP (sharedIps): its ports are separate clients, without per-IP rules.
GMS_TEST(LimitsSharedIp) {
  NetLimitOptions o = On();
  o.connectionsPerIp = 2;
  o.offlineBurst = 4;
  o.datagramsPerSec = 10;
  o.datagramBurst = 10;
  o.ipFactor = 1;
  o.sharedIps = {kIp1};
  NetLimiter l(o);
  for (uint64_t c = 1; c <= 10; ++c) CHECK(l.OnConnected(c, kIp1, static_cast<uint16_t>(1000 + c), 0));
  CHECK(l.OnConnected(20, kIp2, 1, 0));
  CHECK(l.OnConnected(21, kIp2, 2, 0));
  CHECK(!l.OnConnected(22, kIp2, 3, 0));
  // ten ports of the shared IP, ten datagrams each: all pass (per IP they would be 100 of 10)
  int passed = 0;
  for (uint16_t p = 0; p < 10; ++p)
    for (int i = 0; i < 10; ++i) passed += l.OnDatagram(kIp1, static_cast<uint16_t>(2000 + p), 50, false, 0) == NetLimiter::kPass;
  CHECK_EQ(passed, 100);
  CHECK_EQ(l.OnDatagram(kIp1, 2000, 50, false, 0), NetLimiter::kDrop);  // still per address
  // offline datagrams per address there
  passed = 0;
  for (uint16_t p = 0; p < 3; ++p)
    for (int i = 0; i < 6; ++i) passed += l.OnDatagram(kIp1, static_cast<uint16_t>(3000 + p), 50, true, 0) == NetLimiter::kPass;
  CHECK_EQ(passed, 12);
}

GMS_TEST(LimitsPublicAddresses) {
  CHECK(!gms::IsPublicIpv4(0x7F000004));  // 127.0.0.4
  CHECK(!gms::IsPublicIpv4(0xC0A81428));  // 192.168.20.40
  CHECK(!gms::IsPublicIpv4(0x0A000001));
  CHECK(!gms::IsPublicIpv4(0xAC100001));  // 172.16.0.1
  CHECK(gms::IsPublicIpv4(0xAC200001));   // 172.32.0.1
  CHECK(!gms::IsPublicIpv4(0x64400001));  // 100.64.0.1
  CHECK(!gms::IsPublicIpv4(0xA9FE0001));  // 169.254.0.1
  CHECK(!gms::IsPublicIpv4(0xCB007105));  // 203.0.113.5 (documentation)
  CHECK(gms::IsPublicIpv4(0x5DB8D822));   // 93.184.216.34
  CHECK(!gms::IsPublicIpv4(0xE0000001));  // multicast
}

// ---- the endpoint with the limits on, against RakNet test clients on loopback ------------------------------------

namespace {

class Client {
 public:
  ~Client() {
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
    if (!peer_) return;
    for (RakNet::Packet* p = peer_->Receive(); p; peer_->DeallocatePacket(p), p = peer_->Receive()) {
      const uint8_t id = p->data[0];
      if (id == ID_CONNECTION_REQUEST_ACCEPTED) {
        server_ = p->guid;
        connected_ = true;
      } else if (id == ID_DISCONNECTION_NOTIFICATION || id == ID_CONNECTION_LOST) {
        lost_ = true;
      }
    }
  }
  void Send(const Bytes& m) {
    peer_->Send(reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), HIGH_PRIORITY, RELIABLE_ORDERED,
                0, server_, false);
  }
  bool Connected() const { return connected_; }
  bool Lost() const { return lost_; }

 private:
  RakNet::RakPeerInterface* peer_ = nullptr;
  RakNet::RakNetGUID server_;
  bool connected_ = false, lost_ = false;
};

struct Server {
  gms::Endpoint ep;
  std::vector<gms::ConnId> connected, lost;
  size_t messages = 0;
  bool Start(const NetLimitOptions& o) {
    ep.SetLimits(o);
    std::string err;
    if (!ep.Start("127.0.0.1", 0, 8, &err)) {
      std::printf("  endpoint: %s\n", err.c_str());
      return false;
    }
    if (!err.empty()) std::printf("  endpoint: %s\n", err.c_str());
    return true;
  }
  void Step() {
    gms::Endpoint::Event e;
    while (ep.Poll(&e)) {
      if (e.kind == gms::Endpoint::Event::kConnected) connected.push_back(e.conn);
      if (e.kind == gms::Endpoint::Event::kDisconnected) lost.push_back(e.conn);
      if (e.kind == gms::Endpoint::Event::kGms) ++messages;
    }
  }
};

bool RunUntil(const std::function<void()>& step, const std::function<bool()>& done, int timeoutMs) {
  const double end = NowMs() + timeoutMs;
  while (NowMs() < end) {
    step();
    if (done()) return true;
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  return false;
}

Bytes Msg(uint8_t wire, size_t size) {
  Bytes m(size, 0);
  m[0] = wire;
  return m;
}

std::vector<std::string> g_lines;
void Capture(const std::string& line) { g_lines.push_back(line); }
bool Logged(const char* what) {
  for (const std::string& l : g_lines)
    if (l.find(what) != std::string::npos) return true;
  return false;
}

}  // namespace

// A normal client's pace (a ClockSync and a command every few frames) passes the defaults untouched.
GMS_TEST(LimitsEndpointNormalTrafficPasses) {
  Server s;
  CHECK(s.Start(On()));
  Client c;
  CHECK(c.Start(s.ep.BoundPort()));
  auto step = [&] {
    s.Step();
    c.Poll();
  };
  CHECK(RunUntil(step, [&] { return c.Connected() && !s.connected.empty(); }, 8000));
  for (int i = 0; i < 300; ++i) {  // 300 messages over about 1.5 s
    c.Send(Msg(0xB4, 40));
    step();
    std::this_thread::sleep_for(std::chrono::milliseconds(5));
  }
  CHECK(RunUntil(step, [&] { return s.messages >= 300; }, 5000));
  CHECK_EQ(s.messages, static_cast<size_t>(300));
  CHECK(s.lost.empty());
  const NetLimiter::Totals t = s.ep.TakeLimitTotals();
  std::printf("  normal client: %llu datagrams in, busiest second %u datagrams / %llu B, %u messages; dropped %llu\n",
              static_cast<unsigned long long>(t.datagrams), t.maxDatagramsPerS,
              static_cast<unsigned long long>(t.maxBytesPerS), t.maxMessagesPerS,
              static_cast<unsigned long long>(t.droppedRate + t.droppedMessages + t.droppedOffline));
  CHECK_EQ(t.droppedRate + t.droppedMessages + t.droppedOffline + t.droppedBlocked, static_cast<uint64_t>(0));
}

// A message flood: dropped, then the connection is closed (the session sees it lost) and the client hears it.
GMS_TEST(LimitsEndpointFloodIsClosed) {
  g_lines.clear();
  gms::SetLogSink(Capture);
  NetLimitOptions o = On();
  o.messagesPerSec = 50;
  o.messageBurst = 50;
  o.kickAfterS = 2;
  Server s;
  CHECK(s.Start(o));
  Client c;
  CHECK(c.Start(s.ep.BoundPort()));
  auto step = [&] {
    s.Step();
    c.Poll();
  };
  CHECK(RunUntil(step, [&] { return c.Connected() && !s.connected.empty(); }, 8000));
  const double t0 = NowMs();
  CHECK(RunUntil(
      [&] {
        for (int i = 0; i < 20; ++i) c.Send(Msg(0xB4, 40));
        step();
      },
      [&] { return !s.lost.empty(); }, 10000));
  std::printf("  closed after %.0f ms, %zu messages handed out\n", NowMs() - t0, s.messages);
  CHECK(!s.lost.empty() && s.lost[0] == s.connected[0]);
  CHECK(s.messages < 200);
  CHECK(RunUntil(step, [&] { return c.Lost(); }, 5000));
  const NetLimiter::Totals t = s.ep.TakeLimitTotals();
  CHECK(t.droppedMessages > 0);
  CHECK_EQ(t.kickedRate, static_cast<uint64_t>(1));
  CHECK(Logged("net limits: closed 127.0.0.1|"));
  CHECK(Logged("GMS messages over the limits (net_limits)"));
  gms::SetLogSink(nullptr);
}

// A second connection from an IP at its cap is closed at once (the session never hears of it), and a connection
// that sends nothing is closed after the hello timeout.
GMS_TEST(LimitsEndpointCapAndSilent) {
  NetLimitOptions o = On();
  o.connectionsPerIp = 1;
  o.helloTimeoutS = 1;
  Server s;
  CHECK(s.Start(o));
  Client a, b;
  CHECK(a.Start(s.ep.BoundPort()));
  auto step = [&] {
    s.Step();
    a.Poll();
    b.Poll();
  };
  CHECK(RunUntil(step, [&] { return a.Connected() && s.connected.size() == 1; }, 8000));
  CHECK(b.Start(s.ep.BoundPort()));
  CHECK(RunUntil(step, [&] { return b.Lost() || (b.Connected() && s.ep.TakeLimitTotals().kickedCap > 0); }, 8000));
  CHECK_EQ(s.connected.size(), static_cast<size_t>(1));
  // a stays silent: closed after about a second
  CHECK(RunUntil(step, [&] { return !s.lost.empty(); }, 5000));
  CHECK(!s.lost.empty() && s.lost[0] == s.connected[0]);
}

// Junk from another loopback address (127.0.9.1, no lane's): offline datagrams over the IP's rate are dropped before
// RakNet sees them, and a client from 127.0.0.1 still connects and talks during the flood.
GMS_TEST(LimitsEndpointJunkFlood) {
  NetLimitOptions o = On();
  Server s;
  CHECK(s.Start(o));
  SOCKET u = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
  CHECK(u != INVALID_SOCKET);
  sockaddr_in from{};
  from.sin_family = AF_INET;
  inet_pton(AF_INET, "127.0.9.1", &from.sin_addr);
  CHECK(bind(u, reinterpret_cast<sockaddr*>(&from), sizeof from) == 0);
  sockaddr_in to{};
  to.sin_family = AF_INET;
  to.sin_port = htons(s.ep.BoundPort());
  inet_pton(AF_INET, "127.0.0.1", &to.sin_addr);
  Client c;
  CHECK(c.Start(s.ep.BoundPort()));
  char junk[600] = {};
  junk[0] = static_cast<char>(ID_OPEN_CONNECTION_REQUEST_1);
  bool talked = false;
  auto flood = [&] {
    for (int i = 0; i < 50; ++i) sendto(u, junk, sizeof junk, 0, reinterpret_cast<sockaddr*>(&to), sizeof to);
    junk[0] = static_cast<char>(junk[0] == ID_OPEN_CONNECTION_REQUEST_1 ? 0x84 : ID_OPEN_CONNECTION_REQUEST_1);
    s.Step();
    c.Poll();
    if (c.Connected() && !talked) {
      c.Send(Msg(0x81, 20));
      talked = true;
    }
  };
  CHECK(RunUntil(flood, [&] { return s.messages > 0; }, 10000));
  for (int i = 0; i < 200; ++i) flood();
  closesocket(u);
  const NetLimiter::Totals t = s.ep.TakeLimitTotals();
  std::printf("  junk flood: %llu datagrams in, %llu offline and %llu connected-looking dropped; client connected %d\n",
              static_cast<unsigned long long>(t.datagrams), static_cast<unsigned long long>(t.droppedOffline),
              static_cast<unsigned long long>(t.droppedRate), c.Connected() ? 1 : 0);
  CHECK(c.Connected());
  CHECK(t.droppedOffline > 1000);
  CHECK(t.droppedRate > 0);
}
