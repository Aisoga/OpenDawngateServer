// RakNet 4.081's handshake and receive hardening (gms/cmake/raknet_patches/RakPeer.*.patch,
// ReliabilityLayer.*.patch), against the endpoint on loopback, each
// with the stock behaviour beside it:
//   HardeningCookieHandshake       REPLY_1 carries a cookie; REQUEST_2 without it, with another port's, with a
//                                  challenge or a wrong one gets nothing; with it REPLY_2; RakNet's own client connects
//   HardeningShortRequest1         a REQUEST_1 shorter than its reply gets none (stock: a 28-byte reply to 25 bytes)
//   HardeningSpoofedHandshake      what a never-acknowledging sender (a spoofed source) gets back for REQUEST_2 and a
//                                  CONNECTION_REQUEST: stock the reliable ACCEPTED and its resends, hardened nothing
//   HardeningNonsenseNoIpBan       an unverified sender's nonsense closes only its slot: another port of the same IP
//                                  (one NAT, the tunnel gateway) still connects (stock: ID_CONNECTION_BANNED), also with
//                                  the limits on and the IP shared
//   HardeningOrderedAheadLimit     reliable-ordered messages ahead of a hole: over 1,024 (or over the bytes) the
//                                  connection is closed; below, it stays; stock buffers them all
// Addresses 127.0.9.200-215 (no test lane's).
#include <winsock2.h>
#include <ws2tcpip.h>

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <random>
#include <string>
#include <thread>
#include <vector>

#include "MessageIdentifiers.h"
#include "RakNetSocket2.h"
#include "RakNetTypes.h"
#include "RakPeerInterface.h"
#include "core/common/log.h"
#include "core/net/endpoint.h"
#include "core/net/limits.h"
#include "core/timing/clock.h"
#include "tests/check.h"

using gms::Bytes;
using gms::timing::NowMs;

namespace {

const uint8_t kMagic[16] = {0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE,
                            0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78};

gms::RakHardeningOptions Stock() {
  gms::RakHardeningOptions o;
  o.cookies = false;
  o.nonsenseBan = true;
  o.request1NoSmaller = false;
  o.orderedAheadMessages = 0;
  o.orderedAheadKb = 0;
  return o;
}

// The server: an endpoint whose events are collected.
struct Server {
  gms::Endpoint ep;
  std::vector<gms::Endpoint::Event> events;
  bool Start(const gms::RakHardeningOptions& h, const gms::NetLimitOptions* lim = nullptr) {
    ep.SetHardening(h);
    if (lim) ep.SetLimits(*lim);
    std::string err;
    if (!ep.Start("127.0.0.1", 0, 16, &err)) {
      std::printf("  endpoint: %s\n", err.c_str());
      return false;
    }
    return true;
  }
  void Pump() {
    gms::Endpoint::Event e;
    while (ep.Poll(&e)) events.push_back(e);
  }
  int Count(gms::Endpoint::Event::Kind k) const {
    int n = 0;
    for (const auto& e : events) n += e.kind == k ? 1 : 0;
    return n;
  }
};

// A raw UDP socket on loopback that records what comes back.
struct Raw {
  SOCKET s = INVALID_SOCKET;
  sockaddr_in to{};
  uint64_t sentBytes = 0, gotBytes = 0, gotDatagrams = 0;
  std::vector<Bytes> got;
  Raw(uint16_t port, const char* bindIp) {
    WSADATA w;
    WSAStartup(MAKEWORD(2, 2), &w);
    s = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    sockaddr_in from{};
    from.sin_family = AF_INET;
    inet_pton(AF_INET, bindIp, &from.sin_addr);
    bind(s, reinterpret_cast<const sockaddr*>(&from), sizeof from);
    u_long nb = 1;
    ioctlsocket(s, FIONBIO, &nb);
    to.sin_family = AF_INET;
    to.sin_port = htons(port);
    inet_pton(AF_INET, "127.0.0.1", &to.sin_addr);
  }
  ~Raw() {
    if (s != INVALID_SOCKET) closesocket(s);
  }
  void Send(const Bytes& m) {
    sentBytes += m.size();
    sendto(s, reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), 0,
           reinterpret_cast<const sockaddr*>(&to), sizeof to);
  }
  // collects for `ms` (the server pumped meanwhile)
  void Collect(Server& srv, int ms) {
    const double end = NowMs() + ms;
    while (NowMs() < end) {
      char buf[2048];
      for (;;) {
        const int n = recv(s, buf, sizeof buf, 0);
        if (n <= 0) break;
        got.emplace_back(buf, buf + n);
        gotBytes += static_cast<uint64_t>(n);
        ++gotDatagrams;
      }
      srv.Pump();
      std::this_thread::sleep_for(std::chrono::milliseconds(2));
    }
  }
  const Bytes* First(uint8_t id) const {
    for (const Bytes& m : got)
      if (!m.empty() && m[0] == id) return &m;
    return nullptr;
  }
};

Bytes Request1(size_t length, uint8_t protocol = 5) {
  Bytes m{ID_OPEN_CONNECTION_REQUEST_1};
  m.insert(m.end(), kMagic, kMagic + 16);
  m.push_back(protocol);
  m.resize(length, 0);
  return m;
}

// REQUEST_2 as RakNet's client writes it: [cookie (BE), challenge flag], the server's address, MTU, the client's GUID.
Bytes Request2(bool withCookie, uint32_t cookie, uint8_t challenge, uint16_t serverPort, uint64_t guid) {
  Bytes m{ID_OPEN_CONNECTION_REQUEST_2};
  m.insert(m.end(), kMagic, kMagic + 16);
  if (withCookie) {
    for (int i = 3; i >= 0; --i) m.push_back(static_cast<uint8_t>(cookie >> (8 * i)));
    m.push_back(challenge);
  }
  m.push_back(4);  // IPv4, bit-inverted
  for (int b : {127, 0, 0, 1}) m.push_back(static_cast<uint8_t>(~b));
  m.push_back(static_cast<uint8_t>(serverPort >> 8));
  m.push_back(static_cast<uint8_t>(serverPort));
  m.push_back(0x05);  // MTU 1400
  m.push_back(0x78);
  for (int i = 7; i >= 0; --i) m.push_back(static_cast<uint8_t>(guid >> (8 * i)));
  return m;
}

void Put24(Bytes& b, uint32_t v) {
  b.push_back(static_cast<uint8_t>(v));
  b.push_back(static_cast<uint8_t>(v >> 8));
  b.push_back(static_cast<uint8_t>(v >> 16));
}
void Put16BE(Bytes& b, uint16_t v) {
  b.push_back(static_cast<uint8_t>(v >> 8));
  b.push_back(static_cast<uint8_t>(v));
}

// A data datagram (number `dg`) with one RELIABLE (2) message, or RELIABLE_ORDERED (3) on channel 0 with `orderIndex`.
Bytes Reliable(uint32_t dg, uint32_t messageNumber, const Bytes& data, bool ordered = false, uint32_t orderIndex = 0) {
  Bytes b{0x84};  // valid, data, needs B and AS
  Put24(b, dg);
  b.push_back(ordered ? 0x60 : 0x40);
  Put16BE(b, static_cast<uint16_t>(data.size() * 8));
  Put24(b, messageNumber);
  if (ordered) {
    Put24(b, orderIndex);
    b.push_back(0);
  }
  b.insert(b.end(), data.begin(), data.end());
  return b;
}

Bytes ConnectionRequest(uint64_t guid) {
  Bytes m{ID_CONNECTION_REQUEST};
  for (int i = 7; i >= 0; --i) m.push_back(static_cast<uint8_t>(guid >> (8 * i)));
  for (int i = 0; i < 8; ++i) m.push_back(0);  // time
  m.push_back(0);                               // no security, empty password
  return m;
}

// The cookie of a REPLY_1 (06, magic, GUID, hasSecurity 1, cookie BE, MTU); false without one.
bool CookieOf(const Bytes& r, uint32_t* cookie) {
  if (r.size() != 32 || r[0] != ID_OPEN_CONNECTION_REPLY_1 || r[25] != 1) return false;
  *cookie = static_cast<uint32_t>(r[26]) << 24 | static_cast<uint32_t>(r[27]) << 16 |
            static_cast<uint32_t>(r[28]) << 8 | r[29];
  return true;
}

// A plain RakNet client (protocol 5, RakNet's own client code: the non-LIBCAT path the Dawngate client has too).
struct Client {
  RakNet::RakPeerInterface* peer = nullptr;
  RakNet::RakNetGUID server;
  bool connected = false, lost = false, banned = false, failed = false;
  ~Client() { Stop(); }
  void Stop() {
    if (!peer) return;
    peer->Shutdown(0);
    RakNet::RakPeerInterface::DestroyInstance(peer);
    peer = nullptr;
  }
  bool Start(uint16_t port, const char* bindIp) {
    peer = RakNet::RakPeerInterface::GetInstance();
    RakNet::SocketDescriptor sd(0, bindIp);
    if (peer->Startup(1, &sd, 1) != RakNet::RAKNET_STARTED) return false;
    peer->SetTimeoutTime(3000, RakNet::UNASSIGNED_SYSTEM_ADDRESS);
    return peer->Connect("127.0.0.1", port, nullptr, 0) == RakNet::CONNECTION_ATTEMPT_STARTED;
  }
  void Poll() {
    for (RakNet::Packet* p = peer->Receive(); p; peer->DeallocatePacket(p), p = peer->Receive()) {
      const uint8_t id = p->length ? p->data[0] : 0;
      if (id == ID_CONNECTION_REQUEST_ACCEPTED) {
        server = p->guid;
        connected = true;
      } else if (id == ID_CONNECTION_BANNED) {
        banned = true;
      } else if (id == ID_CONNECTION_ATTEMPT_FAILED) {
        failed = true;
      } else if (id == ID_DISCONNECTION_NOTIFICATION || id == ID_CONNECTION_LOST) {
        lost = true;
      }
    }
  }
  void Send(const Bytes& m) {
    peer->Send(reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), HIGH_PRIORITY, RELIABLE_ORDERED, 0,
               server, false);
  }
  // A datagram from the client's own socket (its address is the connection's).
  void SendRaw(const Bytes& d, uint16_t port) {
    DataStructures::List<RakNet::RakNetSocket2*> sockets;
    peer->GetSockets(sockets);
    if (!sockets.Size()) return;
    RakNet::RNS2_SendParameters sp;
    sp.data = const_cast<char*>(reinterpret_cast<const char*>(d.data()));
    sp.length = static_cast<int>(d.size());
    sp.systemAddress = RakNet::SystemAddress("127.0.0.1", port);
    sockets[0]->Send(&sp, _FILE_AND_LINE_);
  }
};

bool RunUntil(Server& s, Client& c, const std::function<bool()>& done, int ms) {
  const double end = NowMs() + ms;
  while (NowMs() < end) {
    s.Pump();
    c.Poll();
    if (done()) return true;
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  return false;
}

// RakNet's client connects from `ip` and the endpoint reports the connection.
bool Connects(Server& s, const char* ip, Client* out = nullptr) {
  Client local;
  Client& c = out ? *out : local;
  if (!c.Start(s.ep.BoundPort(), ip)) return false;
  const int before = s.Count(gms::Endpoint::Event::kConnected);
  return RunUntil(s, c, [&] { return c.banned || c.failed || (c.connected && s.Count(gms::Endpoint::Event::kConnected) > before); },
                  4000) &&
         c.connected;
}

}  // namespace

GMS_TEST(HardeningCookieHandshake) {
  CHECK(gms::Endpoint::HardeningAvailable());
  gms::SetLogSink([](const std::string&) {});
  Server s;
  CHECK(s.Start(gms::RakHardeningOptions()));
  const uint16_t port = s.ep.BoundPort();
  Raw a(port, "127.0.9.200"), b(port, "127.0.9.200");
  a.Send(Request1(548));
  a.Collect(s, 150);
  const Bytes* r1 = a.First(ID_OPEN_CONNECTION_REPLY_1);
  CHECK(r1 != nullptr);
  uint32_t cookie = 0;
  CHECK(r1 && CookieOf(*r1, &cookie));
  CHECK(r1 && r1->size() <= 548);
  // without the cookie (the stock layout), another port's cookie, a challenge, a wrong cookie: nothing back
  a.Send(Request2(false, 0, 0, port, 0x1111));
  b.Send(Request2(true, cookie, 0, port, 0x2222));
  a.Send(Request2(true, cookie, 1, port, 0x3333));
  a.Send(Request2(true, cookie ^ 0x10000, 0, port, 0x4444));
  a.got.clear();
  b.got.clear();
  a.Collect(s, 200);
  b.Collect(s, 10);
  CHECK_EQ(a.gotDatagrams, uint64_t(1));  // REPLY_1 only
  CHECK_EQ(b.gotDatagrams, uint64_t(0));
  // with it: REPLY_2
  a.Send(Request2(true, cookie, 0, port, 0x5555));
  a.Collect(s, 200);
  CHECK(a.First(ID_OPEN_CONNECTION_REPLY_2) != nullptr);
  const gms::RakHardeningTotals t = s.ep.TakeHardeningTotals();
  CHECK_EQ(t.cookiesIssued, uint64_t(1));
  CHECK_EQ(t.request2BadCookie, uint64_t(4));
  // RakNet's own client (the Dawngate client's non-LIBCAT path) echoes it and connects
  CHECK(Connects(s, "127.0.9.201"));
  CHECK(s.ep.TakeHardeningTotals().cookiesIssued >= 1);
  // the stock server: no cookie, REQUEST_2 in the stock layout answered
  Server st;
  CHECK(st.Start(Stock()));
  Raw c(st.ep.BoundPort(), "127.0.9.202");
  c.Send(Request1(548));
  c.Collect(st, 150);
  const Bytes* r1s = c.First(ID_OPEN_CONNECTION_REPLY_1);
  CHECK(r1s && r1s->size() == 28 && (*r1s)[25] == 0);
  c.Send(Request2(false, 0, 0, st.ep.BoundPort(), 0x6666));
  c.Collect(st, 200);
  CHECK(c.First(ID_OPEN_CONNECTION_REPLY_2) != nullptr);
  gms::SetLogSink(nullptr);
}

GMS_TEST(HardeningShortRequest1) {
  gms::SetLogSink([](const std::string&) {});
  for (int stock = 0; stock < 2; ++stock) {
    Server s;
    CHECK(s.Start(stock ? Stock() : gms::RakHardeningOptions()));
    Raw r(s.ep.BoundPort(), "127.0.9.203");
    r.Send(Request1(25));     // the shortest the stock server takes as offline: 28 bytes back
    r.Send(Request1(25, 6));  // a wrong protocol: ID_INCOMPATIBLE_PROTOCOL_VERSION, 26 bytes
    r.Send(Request1(31));     // one byte short of the hardened REPLY_1
    r.Collect(s, 200);
    std::printf("  %s: 3 short REQUEST_1 (%llu B) -> %llu datagrams, %llu B back\n", stock ? "stock" : "hardened",
                static_cast<unsigned long long>(r.sentBytes), static_cast<unsigned long long>(r.gotDatagrams),
                static_cast<unsigned long long>(r.gotBytes));
    if (stock) {
      CHECK_EQ(r.gotDatagrams, uint64_t(3));
      CHECK(r.gotBytes > r.sentBytes);  // the stock reflection gain (28 for 25 or 31, 26 for 25)
    } else {
      CHECK_EQ(r.gotDatagrams, uint64_t(0));
      CHECK_EQ(s.ep.TakeHardeningTotals().request1Short, uint64_t(3));
      r.Send(Request1(32));  // as long as its reply: answered
      r.Collect(s, 150);
      CHECK_EQ(r.gotDatagrams, uint64_t(1));
      CHECK(r.gotBytes <= 32);
    }
  }
  gms::SetLogSink(nullptr);
}

// What a sender that never acknowledges anything (a spoofed source: the answers go to someone else) gets for its
// REQUEST_2 and a CONNECTION_REQUEST: GMS_AMP_SECONDS (default 3; 12 covers the unverified connection's 10 s).
GMS_TEST(HardeningSpoofedHandshake) {
  gms::SetLogSink([](const std::string&) {});
  double seconds = 3;
  if (const char* e = std::getenv("GMS_AMP_SECONDS")) seconds = std::atof(e);
  for (int stock = 1; stock >= 0; --stock) {
    Server s;
    CHECK(s.Start(stock ? Stock() : gms::RakHardeningOptions()));
    Raw r(s.ep.BoundPort(), stock ? "127.0.9.204" : "127.0.9.205");
    r.Send(Request2(false, 0, 0, s.ep.BoundPort(), 0x7777));  // straight to REQUEST_2: no REPLY_1 was seen
    r.Collect(s, 50);
    r.Send(Reliable(0, 0, ConnectionRequest(0x7777)));
    r.Collect(s, static_cast<int>(seconds * 1000));
    uint64_t accepted = 0;
    for (const Bytes& m : r.got)
      if (m.size() > 4 && (m[0] & 0xC0) == 0x80) ++accepted;  // data datagrams (ACCEPTED and its resends)
    std::printf("  %s: sent %llu B in 2 datagrams; got back %llu datagrams, %llu B (%.1fx) in %.1f s, of them %llu data "
                "datagrams (the reliable ACCEPTED and resends)\n",
                stock ? "stock" : "hardened", static_cast<unsigned long long>(r.sentBytes),
                static_cast<unsigned long long>(r.gotDatagrams), static_cast<unsigned long long>(r.gotBytes),
                static_cast<double>(r.gotBytes) / static_cast<double>(r.sentBytes), seconds,
                static_cast<unsigned long long>(accepted));
    if (stock) {
      CHECK(accepted >= 2);  // ACCEPTED, resent while unacknowledged
      CHECK(r.gotBytes > 2 * r.sentBytes);
    } else {
      CHECK_EQ(r.gotDatagrams, uint64_t(0));
      CHECK_EQ(s.ep.TakeHardeningTotals().request2BadCookie, uint64_t(1));
    }
    CHECK_EQ(s.Count(gms::Endpoint::Event::kConnected), 0);
  }
  gms::SetLogSink(nullptr);
}

// An unverified sender (REQUEST_2 answered) whose first message isn't CONNECTION_REQUEST: stock RakNet bans its IP for
// the connection's timeout, so another player on that IP (one NAT; every tunnelled player at the gateway's address)
// can't connect; hardened, only that slot is closed.
GMS_TEST(HardeningNonsenseNoIpBan) {
  gms::SetLogSink([](const std::string&) {});
  struct Case {
    const char* name;
    bool stock, limits;
    const char* ip;
  };
  const Case cases[] = {{"stock", true, false, "127.0.9.206"},
                        {"hardened", false, false, "127.0.9.207"},
                        {"hardened, limits on, the IP shared (the tunnel gateway)", false, true, "127.0.9.208"}};
  for (const Case& k : cases) {
    Server s;
    gms::NetLimitOptions lim;
    lim.on = k.limits;
    lim.sharedIps.push_back(127u << 24 | 9u << 8 | static_cast<uint32_t>(std::atoi(k.ip + 8)));
    CHECK(s.Start(k.stock ? Stock() : gms::RakHardeningOptions(), &lim));
    const uint16_t port = s.ep.BoundPort();
    Raw r(port, k.ip);
    r.Send(Request1(548));
    r.Collect(s, 100);
    uint32_t cookie = 0;
    const Bytes* r1 = r.First(ID_OPEN_CONNECTION_REPLY_1);
    const bool hasCookie = r1 && CookieOf(*r1, &cookie);
    CHECK_EQ(hasCookie, !k.stock);
    r.Send(Request2(hasCookie, cookie, 0, port, 0x8888));
    r.Collect(s, 100);
    CHECK(r.First(ID_OPEN_CONNECTION_REPLY_2) != nullptr);
    r.Send(Reliable(0, 0, Bytes{0x88, 1, 2, 3, 4, 5, 6, 7, 8}));  // a GMS message before CONNECTION_REQUEST
    r.Collect(s, 200);
    Client c;
    const bool ok = Connects(s, k.ip, &c);
    const gms::RakHardeningTotals t = s.ep.TakeHardeningTotals();
    std::printf("  %s: nonsense closed %llu, banned %llu; another port of %s: %s\n", k.name,
                static_cast<unsigned long long>(t.nonsenseClosed), static_cast<unsigned long long>(t.nonsenseBanned),
                k.ip, ok ? "connected" : c.banned ? "ID_CONNECTION_BANNED" : "not connected");
    CHECK_EQ(t.nonsenseClosed, uint64_t(1));
    if (k.stock) {
      CHECK(!ok);
      CHECK(c.banned);
      CHECK_EQ(t.nonsenseBanned, uint64_t(1));
    } else {
      CHECK(ok);
      CHECK_EQ(t.nonsenseBanned, uint64_t(0));
    }
  }
  gms::SetLogSink(nullptr);
}

// Reliable-ordered messages that can't be delivered (ahead of a hole that never fills), from a connected client's own
// socket: over the limit the connection is closed (one ID_CONNECTION_LOST event), below it stays; stock keeps them all.
GMS_TEST(HardeningOrderedAheadLimit) {
  gms::SetLogSink([](const std::string&) {});
  struct Case {
    const char* name;
    unsigned messages, kb;
    int send, size;
    bool closed;
  };
  const Case cases[] = {{"stock, 1,500 ahead", 0, 0, 1500, 40, false},
                        {"limit 1,024, 1,000 ahead", 1024, 1024, 1000, 40, false},
                        {"limit 1,024, 1,100 ahead", 1024, 1024, 1100, 40, true},
                        {"limit 64 KB, 100 x 1,000 B ahead", 1024, 64, 100, 1000, true}};
  int n = 0;
  for (const Case& k : cases) {
    gms::RakHardeningOptions h;
    h.orderedAheadMessages = k.messages;
    h.orderedAheadKb = k.kb;
    Server s;
    CHECK(s.Start(h));
    const uint16_t port = s.ep.BoundPort();
    Client c;
    const std::string ip = "127.0.9." + std::to_string(210 + n++);
    CHECK(Connects(s, ip.c_str(), &c));
    for (int i = 0; i < k.send; ++i) {
      Bytes data(static_cast<size_t>(k.size), 0xAB);
      data[0] = 0x88;
      c.SendRaw(Reliable(0, 5000 + static_cast<uint32_t>(i), data, true, 10000 + static_cast<uint32_t>(i)), port);
      if (i % 50 == 49) {
        s.Pump();
        c.Poll();
        std::this_thread::sleep_for(std::chrono::milliseconds(2));
      }
    }
    // closed: the endpoint's lost event; open: a message still gets through? (no: channel 0 now waits on the hole;
    // the connection's liveness is RakNet's own: no loss within the wait)
    const bool lost = RunUntil(s, c, [&] { return s.Count(gms::Endpoint::Event::kDisconnected) > 0; }, k.closed ? 3000 : 1000);
    const gms::RakHardeningTotals t = s.ep.TakeHardeningTotals();
    std::printf("  %s: %s (closed over the limit %llu)\n", k.name, lost ? "connection closed" : "connection kept",
                static_cast<unsigned long long>(t.orderedAheadClosed));
    CHECK_EQ(lost, k.closed);
    CHECK_EQ(t.orderedAheadClosed, uint64_t(k.closed ? 1 : 0));
  }
  gms::SetLogSink(nullptr);
}
