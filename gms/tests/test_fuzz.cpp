// fuzzing the match host's receive side, each test for a
// fixed time budget: GMS_FUZZ_SECONDS (default 1.5 s each, so a normal gms_tests run stays quick; the done-when run
// used 300) and GMS_FUZZ_SEED (default: from the clock; printed, so a crash can be replayed).
//   FuzzParsers          random and mutated messages (the live client's own as seeds) through every parser of
//                        gms/core that reads client bytes, the check, and the decoders the logs use
//   FuzzSession          the same through GmsSession (shaper select set up; the check in modes 1, 2 and 0, so mode 0
//                        reaches the session's, ShaperSelect's, SurrenderVote's and PostGame's own handling unchecked),
//                        several connections coming and going
//   FuzzRakNetOffline    RakNet 4.081's receive path on its loopback endpoint: random and mutated unconnected
//                        datagrams (the open-connection requests, pings, garbage) from a plain UDP socket
//   FuzzRakNetConnected  a connected RakNet client whose datagrams are mutated on the way (bit flips, truncation,
//                        extension, random ones from the same port) while it sends GMS messages, plus crafted
//                        attacks on the reliability layer (RangeList, split packets): the server must stay up and
//                        serve a fresh client at the end
//   RakNetReceivePathFixes  the receive-path patches one by one, deterministic (a NAK range to 0xFFFFFF, huge ACK ranges,
//                        duplicate and out-of-order fragments, a count mismatch, a split count of 2^32-16, too many split
//                        messages)
#include <winsock2.h>
#include <ws2tcpip.h>

#include <atomic>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <limits>
#include <mutex>
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
#include "core/postgame/postgame.h"
#include "core/protocol/check.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"
#include "core/protocol/wire.h"
#include "core/session/gms_session.h"
#include "core/session/pump.h"
#include "core/surrender/surrender.h"
#include "core/timing/clock.h"
#include "tests/check.h"

using gms::Bytes;
using gms::timing::NowMs;

namespace {

double FuzzSeconds() {
  const char* v = std::getenv("GMS_FUZZ_SECONDS");
  const double s = v ? std::atof(v) : 0.0;
  return s > 0 ? s : 1.5;
}

uint32_t FuzzSeed(const char* test) {
  const char* v = std::getenv("GMS_FUZZ_SEED");
  const uint32_t seed = v ? static_cast<uint32_t>(std::strtoul(v, nullptr, 0))
                          : static_cast<uint32_t>(std::chrono::steady_clock::now().time_since_epoch().count());
  std::printf("  %s: seed %u (GMS_FUZZ_SEED), %.1f s (GMS_FUZZ_SECONDS)\n", test, seed, FuzzSeconds());
  return seed;
}

// The live client's messages (test_check.cpp's census) and a few built ones: the mutation seeds.
const char* const kSeeds[] = {
    "810100000000000000a2c5000000000000", "880000010000000000", "88010400000000803f", "888000000000000000",
    "a6010250c3350171a6000000001e9753c396656cc39933333f0000000000000000",
    "a60200803f2e01777c2cfc190008d4a7230100000008d4a72301000000f815f24b",
    "a60300cdc42e0177a60000000000000000000000000000000000000000000000000000000004000000000000000000000000004f23",
    "a6070200002d01787ca1ea5fc3146b66c3000000000000000003000000", "a608028f002d01787c",
    "a609056cc3350171a6000000001e9753c396656cc39933333f0000000000000000", "a60a0200002e01777c0d000000",
    "a60b038f002d01787c", "a60500000030017707", "a703000000", "a706000000", "c004", "c101", "c1030100",
    "c3010367208d0600000000ff", "c30347f9a0110900000000ff", "c3050000000006000000000700", "c7d4d8b35100000000",
    "c801", "c802", "c804e6fa88af", "c805e6fa88af", "c80603", "c80700", "c80802", "c80a01000000", "c80ce6fa88af",
    "c80de6fa88af", "c80e00a867ef59", "ca01", "ca02", "cb0000", "cc01", "cd0001", "b80000", "be0000000000000000",
};

std::vector<Bytes> Seeds() {
  std::vector<Bytes> v;
  for (const char* h : kSeeds) {
    Bytes b;
    gms::ParseHex(h, b);
    v.push_back(b);
  }
  return v;
}

const uint8_t kInteresting[] = {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x0A, 0x0B, 0x0C, 0x0E, 0x10, 0x7F,
                                0x80, 0x81, 0xA6, 0xC3, 0xC8, 0xFE, 0xFF};

// One to four mutations of `m` (or a fresh random message).
void Mutate(Bytes& m, std::mt19937& rng) {
  auto r = [&rng](uint32_t n) { return n ? static_cast<uint32_t>(rng() % n) : 0u; };
  const int steps = 1 + static_cast<int>(r(4));
  for (int s = 0; s < steps; ++s) {
    switch (r(11)) {
      case 0:  // flip a bit
        if (!m.empty()) m[r(static_cast<uint32_t>(m.size()))] ^= static_cast<uint8_t>(1u << r(8));
        break;
      case 1:  // an interesting byte
        if (!m.empty()) m[r(static_cast<uint32_t>(m.size()))] = kInteresting[r(sizeof kInteresting)];
        break;
      case 2:  // truncate
        if (!m.empty()) m.resize(r(static_cast<uint32_t>(m.size())));
        break;
      case 3:  // extend
        for (uint32_t i = 1 + r(8); i > 0; --i) m.push_back(static_cast<uint8_t>(rng()));
        break;
      case 4:  // insert a byte
        if (!m.empty()) m.insert(m.begin() + r(static_cast<uint32_t>(m.size())), static_cast<uint8_t>(rng()));
        break;
      case 5:  // delete a byte
        if (m.size() > 1) m.erase(m.begin() + 1 + r(static_cast<uint32_t>(m.size() - 1)));
        break;
      case 6: {  // a special float at an aligned offset
        if (m.size() >= 5) {
          const float f[] = {std::numeric_limits<float>::quiet_NaN(), std::numeric_limits<float>::infinity(), -1e30f,
                             1e30f, 263.f, -0.f, std::numeric_limits<float>::denorm_min()};
          const float v = f[r(sizeof f / sizeof f[0])];
          std::memcpy(&m[1 + 4 * r(static_cast<uint32_t>((m.size() - 1) / 4))], &v, 4);
        }
        break;
      }
      case 7: {  // a special u32
        if (m.size() >= 5) {
          const uint32_t u[] = {0, 1, 0xFFFFFFFFu, 0x7FFFFFFFu, 0x80000000u, 0x0400u, 0x96B845C6u, 0x00010000u};
          const uint32_t v = u[r(sizeof u / sizeof u[0])];
          std::memcpy(&m[1 + r(static_cast<uint32_t>(m.size() - 4))], &v, 4);
        }
        break;
      }
      case 8:  // another wire byte (any client-to-GMS or other)
        if (!m.empty()) m[0] = static_cast<uint8_t>(0x81 + r(0x4D));
        break;
      case 9:  // a whole random message
        m.assign(1 + r(64), 0);
        for (uint8_t& b : m) b = static_cast<uint8_t>(rng());
        m[0] = static_cast<uint8_t>(0x81 + r(0x4D));
        break;
      default:  // duplicate a slice
        if (m.size() > 2 && m.size() < 4096) {
          const uint32_t a = r(static_cast<uint32_t>(m.size())), len = 1 + r(static_cast<uint32_t>(m.size() - a));
          Bytes slice(m.begin() + a, m.begin() + a + len);
          m.insert(m.end(), slice.begin(), slice.end());
        }
        break;
    }
  }
  if (m.empty()) m.push_back(static_cast<uint8_t>(0x81 + r(0x4D)));
}

volatile size_t g_sink = 0;  // keeps the parsers' results alive

struct RandomWorld final : gms::check::IWorld {
  std::mt19937* rng = nullptr;
  bool Sender(gms::ConnId, gms::check::SenderView* out) override {
    out->player = (*rng)() % 8 != 0;
    out->gameplayIndex = static_cast<uint8_t>((*rng)() % 15);
    out->creature = (*rng)() % 4 ? 0x7c770130u : (*rng)();
    return (*rng)() % 16 != 0;
  }
  gms::check::TargetView Target(gms::ConnId, uint32_t) override {
    return static_cast<gms::check::TargetView>((*rng)() % 4);
  }
  bool Inventory(gms::ConnId, gms::check::InventoryView* out) override {
    for (int i = 0; i < 11; ++i) {
      out->item[i] = (*rng)() % 2 ? gms::check::ShopItems()[(*rng)() % gms::check::ShopItems().size()] : (*rng)();
      out->count[i] = static_cast<int32_t>((*rng)() % 3) - 1;
    }
    return (*rng)() % 8 != 0;
  }
};

void ParseEverything(const Bytes& m, RandomWorld& world) {
  const uint8_t* b = m.data() + 1;
  const size_t n = m.size() - 1;
  size_t s = 0;
  const gms::check::Bounds bounds = gms::check::BoundsForLevel(gms::kLevelSmMapV5);
  gms::check::Result r = gms::check::CheckFormat(m[0], b, n, bounds);
  s += r.reason.size();
  r = gms::check::CheckWorld(m[0], b, n, 1, world);  // also for messages the format refused: it must not care
  s += r.reason.size();
  gms::ActionCommand c;
  std::string err;
  if (gms::ParseActionCommand(b, n, &c, &err)) s += gms::DescribeActionCommand(c).size();
  s += err.size();
  gms::ItemTransactionRequest t;
  if (gms::ParseItemTransaction(b, n, &t)) s += gms::DescribeItemTransaction(t).size();
  uint32_t slot;
  s += gms::ParseAbilityRankUp(b, n, &slot);
  gms::HelloReq h;
  s += gms::ParseHelloReq(b, n, &h);
  int64_t ms;
  s += gms::ParseClockSync(b, n, &ms);
  gms::PlayerStatusReport st;
  s += gms::ParsePlayerStatusUpdate(b, n, &st);
  gms::ShaperSelectRequest q;
  s += gms::ParseShaperSelectRequest(b, n, &q);
  gms::ShaperRecord rec;
  s += gms::ParseShaperRecord(b, n, &rec);
  bool yes;
  s += gms::surrender::ParseClientVote(b, n, &yes);
  gms::surrender::ServerMessage sm;
  s += gms::surrender::ParseServerMessage(b, n, &sm);
  gms::postgame::ClientMessage pm;
  s += gms::postgame::ParseClientMessage(b, n, &pm);
  gms::postgame::Rewards rw;
  s += gms::postgame::ParseRewards(b, n, &rw);
  uint8_t index;
  std::vector<int> members;
  gms::PlayerGlobalValues v;
  s += gms::ParsePlayerGlobalUpdate(m.data(), m.size(), &index, &members, &v);
  gms::ActionCommandResponse ar;
  s += gms::ParseActionCommandResponse(b, n, &ar);
  s += gms::DescribeCommandAnswer(m).size();
  g_sink = g_sink + s;
}

}  // namespace

GMS_TEST(FuzzParsers) {
  std::mt19937 rng(FuzzSeed("FuzzParsers"));
  RandomWorld world;
  world.rng = &rng;
  const std::vector<Bytes> seeds = Seeds();
  const double end = NowMs() + FuzzSeconds() * 1000;
  uint64_t runs = 0;
  while (NowMs() < end) {
    for (int i = 0; i < 1000; ++i, ++runs) {
      Bytes m = seeds[rng() % seeds.size()];
      Mutate(m, rng);
      ParseEverything(m, world);
    }
  }
  std::printf("  %llu messages\n", static_cast<unsigned long long>(runs));
  CHECK(runs > 0);
}

namespace {

struct NullNet final : gms::ITransport {
  uint64_t sent = 0, closed = 0;
  bool Send(gms::ConnId, const Bytes& m) override {
    sent += m.size();
    return true;
  }
  void Close(gms::ConnId) override { ++closed; }
};

// A match with shaper select, a surrender vote and a post-game, so the session's and these parts' own handling of the
// client's C8, CA and C1 run (with the check off they get every mutated message).
struct FuzzMatch final : gms::IMatch {
  RandomWorld world;
  bool useWorld = true;
  gms::surrender::SurrenderVote vote;
  gms::postgame::PostGame post;
  std::vector<gms::surrender::Send> voteOut;
  std::vector<gms::postgame::Send> postOut;
  uint8_t next = 0;
  double t = 0;
  FuzzMatch() {
    gms::surrender::Tuning k;
    k.enableMatchMinutes = 0;
    vote = gms::surrender::SurrenderVote(k);
  }
  gms::GameSetup Setup() override { return gms::GameSetup(); }
  bool OnHelloReq(gms::ConnId conn, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    if (req.userId % 7 == 6) return false;  // some refused
    slot->gameplayIndex = static_cast<uint8_t>(next++ % 15);
    slot->team = static_cast<uint8_t>(1 + slot->gameplayIndex % 2);
    vote.AddPlayer(conn, slot->gameplayIndex, slot->team, gms::IsSpectatorIndex(slot->gameplayIndex));
    post.AddPlayer(conn, slot->gameplayIndex, slot->team, gms::postgame::Rewards());
    return true;
  }
  void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
  void OnClientMessage(gms::ConnId conn, uint8_t wire, const uint8_t* body, size_t n) override {
    t += 0.05;
    if (wire == gms::kGmsSurrenderMsgs) vote.OnClientMessage(t, 1000, conn, body, n, &voteOut);
    if (wire == gms::kGmsBattlegroundsPostGameMsgs) post.OnClientMessage(t, conn, body, n);
    if (voteOut.size() > 1000) voteOut.clear();
  }
  void OnDisconnected(gms::ConnId conn) override {
    vote.OnConnection(conn, false, t);
    post.OnDisconnected(conn);
  }
  void Tick(gms::SteadyClock::time_point, gms::ISender&) override {
    gms::surrender::MatchView mv;
    mv.matchS = t;
    vote.Tick(t, 1000, mv, &voteOut);
    if (voteOut.size() > 1000) voteOut.clear();
  }
  bool ShaperSelectSetup(gms::ShaperSelectConfig* c) override {
    c->timers.pickMs = c->timers.rolePerkMs = c->timers.skinMs = 30;
    for (const char* name : {"alpha.Noun", "artificer.Noun", "gunman.Noun"}) {
      gms::ShaperListEntry e;
      e.noun = gms::AssetIdOf(name);
      e.flags = gms::kShaperOwned;
      c->humanDefault.list.push_back(e);
    }
    gms::PerkPage page;
    page.id = 1;
    c->humanDefault.pages.push_back(page);
    return true;
  }
  gms::check::IWorld* CheckWorld() override { return useWorld ? &world : nullptr; }
};

}  // namespace

GMS_TEST(FuzzSession) {
  std::mt19937 rng(FuzzSeed("FuzzSession"));
  gms::SetLogSink([](const std::string&) {});  // the session logs every message; not here
  const std::vector<Bytes> seeds = Seeds();
  const double end = NowMs() + FuzzSeconds() * 1000;
  uint64_t runs = 0, sessions = 0, kicked = 0;
  while (NowMs() < end) {
    NullNet net;
    FuzzMatch match;
    match.world.rng = &rng;
    match.useWorld = rng() % 2 != 0;
    gms::SessionOptions so;
    so.stepMs = 1;
    so.loadTimeoutMs = 50;
    so.waitPlayersMs = 50;
    so.matchFoundTimeoutMs = 80;
    so.abortAckMs = 20;
    so.shaperSelect = rng() % 2 != 0;
    so.check.mode = static_cast<int>(rng() % 3);  // 0 off (the deep paths), 1 drop, 2 count only
    so.check.kickCount = 1 + static_cast<int>(rng() % 20);
    gms::GmsSession s(net, match, so, gms::SteadyClock::now());
    ++sessions;
    std::vector<gms::ConnId> conns;
    for (int i = 0; i < 3000 && NowMs() < end; ++i, ++runs) {
      const uint32_t what = rng() % 100;
      if (what < 2 || conns.empty()) {
        const gms::ConnId c = 1000 + rng() % 40;
        s.OnConnected(c, "127.0.0.1|" + std::to_string(c));
        conns.push_back(c);
        Bytes hello{gms::kGmsHelloReq};
        gms::PutU64LE(hello, 1 + rng() % 12);
        gms::PutU64LE(hello, rng() % 3);
        s.OnMessage(c, hello.data(), hello.size());
        continue;
      }
      const gms::ConnId c = conns[rng() % conns.size()];
      if (what < 4) {
        s.OnDisconnected(c);
        continue;
      }
      if (what < 10) {
        s.Tick();
        continue;
      }
      if (what < 12) {
        const uint8_t other[] = {static_cast<uint8_t>(rng() % 0x81), 1, 2, 3};
        s.OnOtherPacket(c, other, 1 + rng() % 3);
        continue;
      }
      Bytes m = seeds[rng() % seeds.size()];
      if (what < 40) {
        // mostly valid messages keep the session moving through its steps
      } else {
        Mutate(m, rng);
      }
      s.OnMessage(c, m.data(), m.size());
    }
    kicked += s.CheckTotals().kicked;
    s.Finish();
  }
  gms::SetLogSink(nullptr);
  std::printf("  %llu events in %llu sessions, %llu kicks\n", static_cast<unsigned long long>(runs),
              static_cast<unsigned long long>(sessions), static_cast<unsigned long long>(kicked));
  CHECK(runs > 0);
}

namespace {

// ---- RakNet 4.081's receive path on loopback -----------------------------------------------------------------------

// The server: an Endpoint pumped into a GmsSession (the host's own way, PumpEndpoint) with the check on.
struct Server {
  int kickCount = 10;  // the check's kick (FuzzRakNetConnected raises it, so a fuzzing client lives longer)
  gms::Endpoint ep;
  NullNet unused;
  struct Match final : gms::IMatch {
    gms::GameSetup Setup() override { return gms::GameSetup(); }
    bool OnHelloReq(gms::ConnId, const gms::HelloReq&, gms::PlayerSlot* slot) override {
      slot->gameplayIndex = 0;
      return true;
    }
    void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
  } match;
  std::unique_ptr<gms::GmsSession> session;
  gms::SteadyClock::time_point start = gms::SteadyClock::now();
  uint64_t events = 0;
  bool Start() {
    std::string err;
    if (!ep.Start("127.0.0.1", 0, 32, &err)) {
      std::printf("  endpoint: %s\n", err.c_str());
      return false;
    }
    gms::SessionOptions so;
    so.stopAfter = gms::kStepPrepare;
    so.stepMs = 1;
    so.check.kickCount = kickCount;
    session.reset(new gms::GmsSession(ep, match, so, start));
    return true;
  }
  void Pump() {
    events += static_cast<uint64_t>(gms::PumpEndpoint(ep, *session, start, nullptr));
    session->Tick();
  }
};

// A plain RakNet client (protocol 5, as the live client), optionally with a socket layer override that mutates what it
// sends.
struct Mutator final : RakNet::SocketLayerOverride {
  RakNet::RNS2_Windows* sock = nullptr;
  std::mt19937 rng;
  std::mutex mu;
  double p = 0;  // the share of datagrams mutated
  std::atomic<uint64_t> mutated{0}, injected{0}, passed{0};
  static thread_local bool bypass;
  void SendRaw(const Bytes& m, const RakNet::SystemAddress& to) {
    if (!sock || m.empty() || m.size() > MAXIMUM_MTU_SIZE) return;
    RakNet::RNS2_SendParameters sp;
    sp.data = const_cast<char*>(reinterpret_cast<const char*>(m.data()));
    sp.length = static_cast<int>(m.size());
    sp.systemAddress = to;
    bypass = true;
    sock->Send(&sp, _FILE_AND_LINE_);
    bypass = false;
  }
  int RakNetSendTo(const char* data, int length, const RakNet::SystemAddress& to) override {
    if (bypass) return -1;
    std::lock_guard<std::mutex> lock(mu);
    std::uniform_real_distribution<double> u(0, 1);
    if (u(rng) < p / 4) {
      Bytes junk(1 + rng() % 200);
      for (uint8_t& b : junk) b = static_cast<uint8_t>(rng());
      if (rng() % 2) junk[0] = static_cast<uint8_t>(0x80 | (rng() & 0x7F));  // a valid-looking datagram header
      SendRaw(junk, to);
      ++injected;
    }
    if (u(rng) < p) {
      Bytes m(data, data + length);
      Mutate(m, rng);
      m[0] = static_cast<uint8_t>(rng() % 4 ? (m[0] | 0x80) : m[0]);  // mostly keep it a datagram
      SendRaw(m, to);
      ++mutated;
      return length;  // the original is "lost"
    }
    ++passed;
    return -1;
  }
  int RakNetRecvFrom(char[MAXIMUM_MTU_SIZE], RakNet::SystemAddress*, bool) override { return 0; }
};
thread_local bool Mutator::bypass = false;

struct Client {
  RakNet::RakPeerInterface* peer = nullptr;
  RakNet::RakNetGUID server;
  RakNet::SystemAddress serverAddr;
  bool connected = false, lost = false;
  std::vector<Bytes> got;
  std::vector<uint8_t> ids;  // RakNet's own packets it got (diagnostics)
  Mutator* mut = nullptr;
  ~Client() { Stop(); }
  void Stop() {
    if (!peer) return;
    if (mut) {
      DataStructures::List<RakNet::RakNetSocket2*> sockets;
      peer->GetSockets(sockets);
      if (sockets.Size()) static_cast<RakNet::RNS2_Windows*>(sockets[0])->SetSocketLayerOverride(nullptr);
    }
    peer->Shutdown(0);
    RakNet::RakPeerInterface::DestroyInstance(peer);
    peer = nullptr;
  }
  // `bindIp`: the fuzzing clients send from 127.0.9.x (no test lane's address), so RakNet's own temporary IP ban of a
  // sender of nonsense (RakPeer's "Temporarily banning ... for sending nonsense data", the connection's timeout long)
  // hits them, and the fresh client of ServerServes on 127.0.0.1 shows that everyone else is still served (now
  // the endpoint bans no IP by default, test_hardening.cpp; the addresses stay apart anyway)
  bool Start(uint16_t port, Mutator* m, const char* bindIp = "127.0.0.1") {
    peer = RakNet::RakPeerInterface::GetInstance();
    RakNet::SocketDescriptor sd(0, bindIp);
    if (peer->Startup(1, &sd, 1) != RakNet::RAKNET_STARTED) return false;
    peer->SetTimeoutTime(3000, RakNet::UNASSIGNED_SYSTEM_ADDRESS);
    mut = m;
    if (m) {
      DataStructures::List<RakNet::RakNetSocket2*> sockets;
      peer->GetSockets(sockets);
      if (!sockets.Size() || sockets[0]->GetSocketType() != RakNet::RNS2T_WINDOWS) return false;
      m->sock = static_cast<RakNet::RNS2_Windows*>(sockets[0]);
      m->sock->SetSocketLayerOverride(m);
    }
    serverAddr = RakNet::SystemAddress("127.0.0.1", port);
    return peer->Connect("127.0.0.1", port, nullptr, 0) == RakNet::CONNECTION_ATTEMPT_STARTED;
  }
  void Poll() {
    for (RakNet::Packet* p = peer->Receive(); p; peer->DeallocatePacket(p), p = peer->Receive()) {
      const uint8_t id = p->length ? p->data[0] : 0;
      if (id < 0x81 && ids.size() < 16) ids.push_back(id);
      if (id == ID_CONNECTION_REQUEST_ACCEPTED) {
        server = p->guid;
        connected = true;
      } else if (id == ID_DISCONNECTION_NOTIFICATION || id == ID_CONNECTION_LOST || id == ID_CONNECTION_ATTEMPT_FAILED) {
        lost = true;
      } else if (id >= 0x81) {
        if (got.size() < 10000) got.emplace_back(p->data, p->data + p->length);
      }
    }
  }
  void Send(const Bytes& m) {
    peer->Send(reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), HIGH_PRIORITY, RELIABLE_ORDERED, 0,
               server, false);
  }
  bool Got(uint8_t wire) const {
    for (const Bytes& m : got)
      if (!m.empty() && m[0] == wire) return true;
    return false;
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

// A fresh, unmutated client gets Connected (84) and, after its HelloReq, HelloPlayer (82): the server is up.
bool ServerServes(Server& s) {
  Client c;
  if (!c.Start(s.ep.BoundPort(), nullptr)) return false;
  const double t0 = NowMs();
  if (!RunUntil(s, c, [&] { return c.connected && c.Got(gms::kGmsConnected); }, 5000)) {
    std::string ids;
    for (uint8_t id : c.ids) ids += " " + std::string(gms::RakMessageName(id)) + "(" + std::to_string(id) + ")";
    std::printf("  fresh client: no connection in 5 s (connected %d; RakNet packets:%s)\n", c.connected ? 1 : 0,
                ids.c_str());
    return false;
  }
  Bytes hello{gms::kGmsHelloReq};
  gms::PutU64LE(hello, 42);
  gms::PutU64LE(hello, 0);
  c.Send(hello);
  const bool ok = RunUntil(s, c, [&] { return c.Got(gms::kGmsHelloPlayer); }, 5000);
  std::printf("  fresh client: %s after %.0f ms\n", ok ? "served" : "NO HelloPlayer", NowMs() - t0);
  return ok;
}

// A UDP socket on loopback for raw datagrams.
struct Udp {
  SOCKET s = INVALID_SOCKET;
  sockaddr_in to{};
  explicit Udp(uint16_t port, const char* bindIp) {
    WSADATA w;
    WSAStartup(MAKEWORD(2, 2), &w);
    s = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    sockaddr_in from{};
    from.sin_family = AF_INET;
    inet_pton(AF_INET, bindIp, &from.sin_addr);
    bind(s, reinterpret_cast<const sockaddr*>(&from), sizeof from);
    to.sin_family = AF_INET;
    to.sin_port = htons(port);
    inet_pton(AF_INET, "127.0.0.1", &to.sin_addr);
  }
  ~Udp() {
    if (s != INVALID_SOCKET) closesocket(s);
  }
  void Send(const Bytes& m) {
    sendto(s, reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), 0,
           reinterpret_cast<const sockaddr*>(&to), sizeof to);
  }
};

const uint8_t kOfflineMagic[16] = {0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE,
                                   0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78};

// RakNet 4's unconnected messages as a client writes them (with random fields).
Bytes OfflineMessage(std::mt19937& rng) {
  Bytes m;
  auto magic = [&m] { m.insert(m.end(), kOfflineMagic, kOfflineMagic + 16); };
  switch (rng() % 6) {
    case 0:  // ID_OPEN_CONNECTION_REQUEST_1: magic, protocol, MTU padding
      m.push_back(ID_OPEN_CONNECTION_REQUEST_1);
      magic();
      m.push_back(rng() % 4 ? 5 : static_cast<uint8_t>(rng()));
      m.resize(rng() % 8 == 0 ? 1464 : 18 + rng() % 600, 0);
      break;
    case 1: {  // ID_OPEN_CONNECTION_REQUEST_2: magic, binding address, MTU, GUID
      m.push_back(ID_OPEN_CONNECTION_REQUEST_2);
      magic();
      m.push_back(4);
      for (int i = 0; i < 6; ++i) m.push_back(static_cast<uint8_t>(rng()));
      m.push_back(static_cast<uint8_t>(rng() % 6));  // MTU high byte
      m.push_back(static_cast<uint8_t>(rng()));
      for (int i = 0; i < 8; ++i) m.push_back(static_cast<uint8_t>(rng()));
      break;
    }
    case 2:  // ID_UNCONNECTED_PING(_OPEN_CONNECTIONS): time, magic, GUID
      m.push_back(static_cast<uint8_t>(rng() % 2 ? ID_UNCONNECTED_PING : ID_UNCONNECTED_PING_OPEN_CONNECTIONS));
      for (int i = 0; i < 8; ++i) m.push_back(static_cast<uint8_t>(rng()));
      magic();
      for (int i = 0; i < 8; ++i) m.push_back(static_cast<uint8_t>(rng()));
      break;
    case 3:  // ID_OUT_OF_BAND_INTERNAL / ID_ADVERTISE_SYSTEM style: id, magic, payload
      m.push_back(static_cast<uint8_t>(rng() % 2 ? ID_OUT_OF_BAND_INTERNAL : ID_ADVERTISE_SYSTEM));
      magic();
      for (uint32_t i = rng() % 40; i > 0; --i) m.push_back(static_cast<uint8_t>(rng()));
      break;
    case 4:  // a connected-looking datagram from an unconnected address
      m.push_back(static_cast<uint8_t>(0x80 | (rng() & 0x7F)));
      for (uint32_t i = rng() % 60; i > 0; --i) m.push_back(static_cast<uint8_t>(rng()));
      break;
    default:  // anything
      m.resize(1 + rng() % 1500);
      for (uint8_t& b : m) b = static_cast<uint8_t>(rng());
      break;
  }
  return m;
}

}  // namespace

GMS_TEST(FuzzRakNetOffline) {
  std::mt19937 rng(FuzzSeed("FuzzRakNetOffline"));
  gms::SetLogSink([](const std::string&) {});
  Server s;
  CHECK(s.Start());
  Udp udp(s.ep.BoundPort(), "127.0.9.250");
  const double end = NowMs() + FuzzSeconds() * 1000;
  uint64_t sent = 0;
  while (NowMs() < end) {
    // a few thousand datagrams a second: the test lanes' captures record all loopback traffic
    for (int i = 0; i < 4; ++i, ++sent) {
      Bytes m = OfflineMessage(rng);
      if (rng() % 2) Mutate(m, rng);
      udp.Send(m);
    }
    s.Pump();
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  const bool serves = ServerServes(s);
  gms::SetLogSink(nullptr);
  std::printf("  %llu datagrams, %llu server events; a fresh client served: %s\n", static_cast<unsigned long long>(sent),
              static_cast<unsigned long long>(s.events), serves ? "yes" : "NO");
  CHECK(serves);
}

GMS_TEST(FuzzRakNetConnected) {
  const uint32_t seed = FuzzSeed("FuzzRakNetConnected");
  std::mt19937 rng(seed);
  gms::SetLogSink([](const std::string&) {});
  Server s;
  s.kickCount = 1000000;
  CHECK(s.Start());
  const std::vector<Bytes> seeds = Seeds();
  const double end = NowMs() + FuzzSeconds() * 1000;
  uint64_t clients = 0, sent = 0, mutated = 0, injected = 0;
  while (NowMs() < end) {
    Mutator mut;
    mut.rng.seed(rng());
    mut.p = 0;  // connect cleanly first
    Client c;
    ++clients;
    const std::string ip = "127.0.9." + std::to_string(1 + clients % 200);
    // mutated only once the server has the connection (its session's 84): RakPeer bans an address whose first
    // datagrams after the open-connection requests are nonsense, for the connection's timeout (stock; only
    // that connection is closed)
    if (!c.Start(s.ep.BoundPort(), &mut, ip.c_str()) ||
        !RunUntil(s, c, [&] { return c.connected && c.Got(gms::kGmsConnected); }, 3000))
      continue;
    {
      std::lock_guard<std::mutex> lock(mut.mu);
      mut.p = 0.02 + (rng() % 30) / 100.0;  // then 2-31 % of its datagrams mutated
    }
    const double until = std::min(end, NowMs() + 300 + rng() % 1500);
    while (NowMs() < until && !c.lost) {
      for (int i = 0; i < 1 + static_cast<int>(rng() % 20); ++i, ++sent) {
        Bytes m = seeds[rng() % seeds.size()];
        if (rng() % 3) Mutate(m, rng);
        if (rng() % 50 == 0) m.resize(1 + rng() % 6000, static_cast<uint8_t>(rng()));  // a split message
        c.Send(m);
      }
      s.Pump();
      c.Poll();
      std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    mutated += mut.mutated;
    injected += mut.injected;
    c.Stop();
  }
  const bool serves = ServerServes(s);
  gms::SetLogSink(nullptr);
  std::printf("  %llu clients, %llu messages, %llu datagrams mutated, %llu injected, %llu server events, check %s; a "
              "fresh client served: %s\n",
              static_cast<unsigned long long>(clients), static_cast<unsigned long long>(sent),
              static_cast<unsigned long long>(mutated), static_cast<unsigned long long>(injected),
              static_cast<unsigned long long>(s.events), s.session->CheckTotals().Describe().c_str(),
              serves ? "yes" : "NO");
  CHECK(serves);
}

namespace {

// ---- crafted datagrams (ReliabilityLayer's wire format, sliding-window build: no timestamp) --------------------------
void Put24(Bytes& b, uint32_t v) {  // uint24_t: little-endian
  b.push_back(static_cast<uint8_t>(v));
  b.push_back(static_cast<uint8_t>(v >> 8));
  b.push_back(static_cast<uint8_t>(v >> 16));
}
void Put16BE(Bytes& b, uint16_t v) {  // BitStream writes integers in network order
  b.push_back(static_cast<uint8_t>(v >> 8));
  b.push_back(static_cast<uint8_t>(v));
}
void Put32BE(Bytes& b, uint32_t v) {
  for (int i = 3; i >= 0; --i) b.push_back(static_cast<uint8_t>(v >> (8 * i)));
}
// An ACK (0xC0) or NAK (0xA0) datagram with the ranges {min, max}.
Bytes RangeDatagram(bool ack, const std::vector<std::pair<uint32_t, uint32_t>>& ranges) {
  Bytes b{static_cast<uint8_t>(ack ? 0xC0 : 0xA0)};
  Put16BE(b, static_cast<uint16_t>(ranges.size()));
  for (const auto& r : ranges) {
    b.push_back(r.first == r.second ? 1 : 0);
    Put24(b, r.first);
    if (r.first != r.second) Put24(b, r.second);
  }
  return b;
}
// A data datagram (number 0, so never "ahead") with one unreliable fragment of a split message.
Bytes SplitDatagram(uint32_t count, uint16_t id, uint32_t index, const Bytes& part) {
  Bytes b{0x84};  // valid, data, continuous send
  Put24(b, 0);
  b.push_back(0x10);  // reliability UNRELIABLE (0), has split
  Put16BE(b, static_cast<uint16_t>(part.size() * 8));
  Put32BE(b, count);
  Put16BE(b, id);
  Put32BE(b, index);
  b.insert(b.end(), part.begin(), part.end());
  return b;
}

// The connected client's datagram goes out from its own socket (its address is the connection's).
void Inject(Mutator& m, const Client& c, const Bytes& d) { m.SendRaw(d, c.serverAddr); }

// The server answers a ClockSync within `ms`: its update thread and the engine poll are not stuck.
bool Answers(Server& s, Client& c, int ms) {
  c.got.clear();
  Bytes clock{gms::kGmsClockSync};
  gms::PutI64LE(clock, 12345);
  c.Send(clock);
  return RunUntil(s, c, [&] { return c.Got(gms::kGmsClockSync); }, ms);
}

}  // namespace

GMS_TEST(RakNetReceivePathFixes) {
  gms::SetLogSink([](const std::string&) {});
  Server s;
  CHECK(s.Start());
  Mutator mut;
  Client c;
  CHECK(c.Start(s.ep.BoundPort(), &mut));
  CHECK(RunUntil(s, c, [&] { return c.connected && c.Got(gms::kGmsConnected); }, 5000));
  Bytes hello{gms::kGmsHelloReq};
  gms::PutU64LE(hello, 42);
  gms::PutU64LE(hello, 0);
  c.Send(hello);
  CHECK(RunUntil(s, c, [&] { return c.Got(gms::kGmsHelloPlayer); }, 5000));
  CHECK(Answers(s, c, 3000));

  // 1. a NAK range ending at 0xFFFFFF: the stock loop's counter wrapped and never ended (RakNet issue #102)
  Inject(mut, c, RangeDatagram(false, {{0xFFFF00, 0xFFFFFF}}));
  // 2. 200 ACK ranges of 16.7 million datagrams each: 3.3 billion steps before
  std::vector<std::pair<uint32_t, uint32_t>> huge;
  for (int i = 0; i < 200; ++i) huge.push_back({0, 0xFFFFFE});
  Inject(mut, c, RangeDatagram(true, huge));
  Inject(mut, c, RangeDatagram(false, huge));
  // 3. overlapping and descending ranges, a count beyond the bytes
  Inject(mut, c, RangeDatagram(true, {{10, 20}, {15, 30}, {5, 6}}));
  Bytes truncated = RangeDatagram(true, {{1, 2}});
  truncated[1] = 0xFF;
  Inject(mut, c, truncated);
  CHECK(Answers(s, c, 3000));  // still answering within 3 s

  // 4. a ClockSync in two fragments, the second sent twice and first: rebuilt once, in index order, and answered
  //    (the stock layer took the repeated fragment as the missing one and rebuilt it in arrival order)
  c.got.clear();
  const Bytes part0 = {gms::kGmsClockSync, 0x39, 0x30, 0, 0}, part1 = {0, 0, 0, 0};  // client clock 12345
  Inject(mut, c, SplitDatagram(2, 9, 1, part1));
  Inject(mut, c, SplitDatagram(2, 9, 1, part1));
  for (int i = 0; i < 20; ++i) s.Pump();
  CHECK(!c.Got(gms::kGmsClockSync));
  Inject(mut, c, SplitDatagram(2, 9, 0, part0));
  CHECK(RunUntil(s, c, [&] { return c.Got(gms::kGmsClockSync); }, 3000));
  for (const Bytes& m : c.got)
    if (m.size() == 17 && m[0] == gms::kGmsClockSync) CHECK_EQ(gms::GetI64LE(m.data() + 1), int64_t(12345));
  // 5. a fragment whose count differs from its message's first fragment's: dropped, the message never completes
  c.got.clear();
  Inject(mut, c, SplitDatagram(3, 10, 0, part0));
  Inject(mut, c, SplitDatagram(2, 10, 1, part1));
  Inject(mut, c, SplitDatagram(2, 10, 2, part1));  // index >= count: the stock check already refused it
  for (int i = 0; i < 50; ++i) {
    s.Pump();
    c.Poll();
  }
  CHECK(!c.Got(gms::kGmsClockSync));
  CHECK(Answers(s, c, 3000));
  // 6. a split count of 2^32-16: the stock layer preallocated count pointers for the message (bad_alloc)
  Inject(mut, c, SplitDatagram(0xFFFFFFF0u, 77, 0, Bytes{1, 2, 3}));
  // 7. more split messages than a connection may reassemble at once (each would hold up to 1,024 fragments)
  for (uint16_t id = 100; id < 140; ++id) Inject(mut, c, SplitDatagram(1024, id, 1, Bytes(1000, 0xAB)));
  CHECK(Answers(s, c, 3000));  // (this connection can rebuild no split message any more: its own cost)

  CHECK(!c.lost);
  c.Stop();
  gms::SetLogSink(nullptr);
}
