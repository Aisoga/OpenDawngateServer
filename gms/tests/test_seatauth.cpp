// SEATAUTH and HOSTHARD: who may claim a seat, and what a client may decide. GmsSession against a fake transport: no
// sockets, no game.
#include <chrono>
#include <map>
#include <set>
#include <string>
#include <thread>
#include <vector>

#include "core/common/bytes.h"
#include "core/common/log.h"
#include "core/protocol/comm_rate.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "core/protocol/wire.h"
#include "core/session/gms_session.h"
#include "core/session/match.h"
#include "tests/check.h"

using gms::Bytes;

namespace {

constexpr uint64_t kVictim = 2000000001ull, kOther = 2000000002ull, kHostile = 2000000009ull;

std::vector<std::string>& Lines() {
  static std::vector<std::string> lines;
  return lines;
}
void Sink(const std::string& line) { Lines().push_back(line); }
struct Capture {
  Capture() {
    Lines().clear();
    gms::SetLogSink(Sink);
  }
  ~Capture() {
    gms::SetLogSink(nullptr);
    if (gmstest::Failures() != failures)
      for (const std::string& l : Lines()) std::printf("  | %s\n", l.c_str());
  }
  int failures = gmstest::Failures();
  bool Has(const std::string& text) const {
    for (const std::string& l : Lines())
      if (l.find(text) != std::string::npos) return true;
    return false;
  }
  int Count(const std::string& text) const {
    int n = 0;
    for (const std::string& l : Lines()) n += l.find(text) != std::string::npos ? 1 : 0;
    return n;
  }
};

struct Net final : gms::ITransport {
  std::map<gms::ConnId, std::vector<Bytes>> sent;
  std::set<gms::ConnId> closed;
  bool Send(gms::ConnId conn, const Bytes& msg) override {
    sent[conn].push_back(msg);
    return true;
  }
  void Close(gms::ConnId conn) override { closed.insert(conn); }
  bool Got(gms::ConnId conn, uint8_t wire) const {
    auto it = sent.find(conn);
    if (it == sent.end()) return false;
    for (const Bytes& m : it->second)
      if (m[0] == wire) return true;
    return false;
  }
  int Count(gms::ConnId conn, uint8_t wire) const {
    auto it = sent.find(conn);
    int n = 0;
    if (it != sent.end())
      for (const Bytes& m : it->second) n += m[0] == wire ? 1 : 0;
    return n;
  }
  bool GotPreGame(gms::ConnId conn, uint8_t sub) const {
    auto it = sent.find(conn);
    if (it == sent.end()) return false;
    for (const Bytes& m : it->second)
      if (m[0] == gms::kGmsBattlegroundsPreGameMsgs && m.size() >= 2 && m[1] == sub) return true;
    return false;
  }
};

// ServerMatch's rule: only the roster gets in, a user with a seat takes it back, and with a control channel every
// HelloReq is asked about first.
struct M final : gms::IMatch {
  std::map<uint64_t, uint8_t> roster{{kVictim, uint8_t{0}}, {kOther, uint8_t{1}}};
  bool verify = true;
  bool lobby = false;
  std::vector<std::string> asked;
  std::map<gms::ConnId, uint64_t> seated;
  std::vector<gms::ConnId> rejoinedFrom;
  std::vector<std::vector<uint64_t>> aborted;
  int loadedPoints = 0;
  gms::GameSetup setup;
  gms::GameSetup Setup() override { return setup; }
  bool AskHelloVerdict(gms::ConnId, const std::string& addr, const gms::HelloReq& req) override {
    if (!verify) return false;
    asked.push_back(addr + " " + std::to_string(req.userId));
    return true;
  }
  bool HoldHello(gms::ConnId, const gms::HelloReq&) override { return lobby; }
  bool OnHelloReq(gms::ConnId conn, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    auto it = roster.find(req.userId);
    if (it == roster.end()) return false;
    slot->gameplayIndex = it->second;
    seated[conn] = req.userId;
    return true;
  }
  void OnRejoin(gms::ConnId, gms::ConnId previous, const gms::PlayerSlot&) override { rejoinedFrom.push_back(previous); }
  void OnPoint(gms::ConnId, gms::SessionPoint p, gms::ISender&) override {
    if (p == gms::SessionPoint::kLoaded) ++loadedPoints;
  }
  void OnDisconnected(gms::ConnId conn) override { seated.erase(conn); }
  void OnGameAborted(const std::vector<uint64_t>& culprits, const std::string&) override { aborted.push_back(culprits); }
};

gms::SessionOptions Options() {
  gms::SessionOptions so;
  so.stepMs = 1;
  so.skipShaper = so.echoStatus = so.playerUpdate = true;
  so.loadTimeoutMs = 60000;
  return so;
}

void Hello(gms::GmsSession& s, gms::ConnId c, uint64_t user) {
  Bytes m{gms::kGmsHelloReq};
  gms::PutU64LE(m, user);
  gms::PutU64LE(m, 0);
  s.OnMessage(c, m.data(), m.size());
}

void Status(gms::GmsSession& s, gms::ConnId c, uint32_t flags, float progress) {
  Bytes m{gms::kGmsPlayerStatusUpdate};
  gms::PutU32LE(m, flags);
  gms::PutF32LE(m, progress);
  s.OnMessage(c, m.data(), m.size());
}

void Clock5(gms::GmsSession& s, gms::ConnId c) {
  for (int i = 0; i < 5; ++i) {
    Bytes m{gms::kGmsClockSync};
    gms::PutI64LE(m, 1000 + i);
    s.OnMessage(c, m.data(), m.size());
  }
}

void AllLoaded(gms::GmsSession& s, gms::ConnId c) {
  Bytes m{gms::kGmsBattlegroundsPreGameMsgs, gms::kPreGameAllLoaded};
  s.OnMessage(c, m.data(), m.size());
}

template <class F>
bool TickUntil(gms::GmsSession& s, F done, int ms = 2000) {
  const auto end = gms::SteadyClock::now() + std::chrono::milliseconds(ms);
  while (gms::SteadyClock::now() < end) {
    s.Tick();
    if (done()) return true;
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  return false;
}

void TickFor(gms::GmsSession& s, int ms) {
  TickUntil(s, [] { return false; }, ms);
}

}  // namespace

GMS_TEST(SeatAuthHelloHeldUntilItsVerdict) {
  Capture log;
  Net net;
  M match;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kVictim);
  TickFor(s, 20);
  CHECK_EQ(match.asked.size(), size_t{1});
  CHECK_EQ(match.asked[0], std::string("127.0.1.10:50001 2000000001"));  // ip:port, as the control channel writes it
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  CHECK(match.seated.empty());  // the match hasn't heard of it
  CHECK(s.State(1).find("HelloReq being verified") != std::string::npos);
  s.OnHelloVerdict("127.0.1.10:50002", kVictim, true);  // another connection's address: not this one's verdict
  s.OnHelloVerdict("127.0.1.10:50001", kOther, true);   // ... nor another claim
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  s.OnHelloVerdict("127.0.1.10:50001", kVictim, true);
  CHECK(net.Got(1, gms::kGmsHelloPlayer));
  CHECK_EQ(match.seated.size(), size_t{1});
  CHECK(net.closed.empty());
}

GMS_TEST(SeatAuthARefusedHelloCloses) {
  Capture log;
  Net net;
  M match;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kVictim);
  s.OnHelloVerdict("127.0.1.10:50001", kVictim, false);
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  CHECK(match.seated.empty());
  CHECK_EQ(s.NumConnections(), size_t{0});
  CHECK(log.Has("refused: the server says this connection may not claim that user"));
  // a verdict for a connection that has gone is dropped
  s.OnHelloVerdict("127.0.1.10:50001", kVictim, true);
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
}

GMS_TEST(SeatAuthNoVerdictMeansRefused) {
  Capture log;
  Net net;
  M match;
  gms::SessionOptions so = Options();
  so.verifyTimeoutMs = 30;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kVictim);
  TickFor(s, 5);
  CHECK(net.closed.empty());
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  CHECK(log.Has("no verdict from the server in time"));
}

GMS_TEST(SeatAuthARosterRefusalCloses) {
  Capture log;
  Net net;
  M match;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kHostile);
  s.OnHelloVerdict("127.0.1.10:50001", kHostile, true);  // its own login, a route, but not in this game
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  CHECK(log.Has("refused by the match"));
}

GMS_TEST(SeatAuthAVerifiedHelloIsStillHeldInTheLobby) {
  Capture log;
  Net net;
  M match;
  match.lobby = true;
  gms::SessionOptions so = Options();
  so.helloTimeoutMs = 20;  // a held HelloReq is no silent connection
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kVictim);
  s.OnHelloVerdict("127.0.1.10:50001", kVictim, true);
  TickFor(s, 50);
  CHECK(!net.Got(1, gms::kGmsHelloPlayer));
  CHECK(net.closed.empty());
  CHECK(s.State(1).find("HelloReq held") != std::string::npos);
  match.lobby = false;
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsHelloPlayer); }));
  CHECK(net.closed.empty());
}

GMS_TEST(SeatAuthOneHeldConnectionPerUser) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  match.lobby = true;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  s.OnConnected(2, "a|2");
  Hello(s, 2, kVictim);
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK(net.closed.count(2) == 0);
  CHECK(log.Has("connected again while held in the lobby"));
}

GMS_TEST(SeatAuthARealRejoinPassesAndAFalseClaimLeavesTheVictim) {
  Capture log;
  Net net;
  M match;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "127.0.1.10|50001");
  Hello(s, 1, kVictim);
  s.OnHelloVerdict("127.0.1.10:50001", kVictim, true);
  CHECK(net.Got(1, gms::kGmsHelloPlayer));

  // the attack: another connection claims the victim's id. Refused: the victim keeps its seat and its connection
  s.OnConnected(2, "127.0.1.10|50002");
  Hello(s, 2, kVictim);
  s.OnHelloVerdict("127.0.1.10:50002", kVictim, false);
  CHECK(TickUntil(s, [&] { return net.closed.count(2) != 0; }));
  CHECK(match.rejoinedFrom.empty());
  CHECK(net.closed.count(1) == 0);
  CHECK(s.State(1).find("superseded") == std::string::npos);
  CHECK(match.seated.count(1) == 1);

  // a real reconnect: a new tunnel session with the same login gets its ok, and the old connection is superseded
  s.OnConnected(3, "127.0.1.10|50003");
  Hello(s, 3, kVictim);
  s.OnHelloVerdict("127.0.1.10:50003", kVictim, true);
  CHECK(net.Got(3, gms::kGmsHelloPlayer));
  CHECK_EQ(match.rejoinedFrom.size(), size_t{1});
  CHECK_EQ(match.rejoinedFrom[0], gms::ConnId{1});
  CHECK(log.Has("rejoins as player 0"));
  // the superseded connection doesn't keep a slot
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK(net.closed.count(3) == 0);
}

GMS_TEST(SeatAuthAnUnseatedConnectionTimesOut) {
  Capture log;
  Net net;
  M match;
  gms::SessionOptions so = Options();
  so.helloTimeoutMs = 30;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  TickFor(s, 5);
  CHECK(net.closed.empty());
  CHECK(TickUntil(s, [&] { return net.closed.count(1) != 0; }));
  CHECK_EQ(s.NumConnections(), size_t{0});
  CHECK(log.Has("sent no HelloReq within 30 ms"));
  // a session that stops before the hello (the research probes) keeps its connections
  Net net2;
  so.stopAfter = gms::kStepConnected;
  gms::GmsSession s2(net2, match, so, gms::SteadyClock::now());
  s2.OnConnected(1, "a|1");
  TickFor(s2, 60);
  CHECK(net2.closed.empty());
}

GMS_TEST(SeatAuthAKickClosesAndBars) {
  Capture log;
  Net net;
  M match;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  s.OnHelloVerdict("a:1", kVictim, true);
  CHECK(net.Got(1, gms::kGmsHelloPlayer));
  CHECK_EQ(s.KickUser(kVictim, "PLAYER_KICKED", true), 1);
  CHECK(net.closed.count(1) != 0);
  CHECK_EQ(s.NumConnections(), size_t{0});
  CHECK(match.seated.empty());
  // a rejoin of the kicked user: refused without asking, and closed
  match.asked.clear();
  s.OnConnected(2, "a|2");
  Hello(s, 2, kVictim);
  CHECK(match.asked.empty());
  CHECK(TickUntil(s, [&] { return net.closed.count(2) != 0; }));
  CHECK(!net.Got(2, gms::kGmsHelloPlayer));
  // someone else is not touched
  s.OnConnected(3, "a|3");
  Hello(s, 3, kOther);
  s.OnHelloVerdict("a:3", kOther, true);
  CHECK(net.Got(3, gms::kGmsHelloPlayer));
  // a verdict that was on its way when the kick came doesn't seat it
  s.OnConnected(4, "a|4");
  match.verify = true;
  Hello(s, 4, kHostile);
  s.KickUser(kHostile, "PLAYER_KICKED", true);
  CHECK(net.closed.count(4) != 0);
  s.OnHelloVerdict("a:4", kHostile, true);
  CHECK(!net.Got(4, gms::kGmsHelloPlayer));
}

GMS_TEST(SeatAuthALobbyKickDoesNotBar) {
  Capture log;
  Net net;
  M match;
  match.lobby = true;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  s.OnHelloVerdict("a:1", kVictim, true);
  CHECK_EQ(s.KickUser(kVictim, "PLAYER_KICKED", false), 1);
  CHECK(net.closed.count(1) != 0);
  s.OnConnected(2, "a|2");
  Hello(s, 2, kVictim);
  s.OnHelloVerdict("a:2", kVictim, true);  // let in again by a later roster: held, not refused
  TickFor(s, 10);
  CHECK(net.closed.count(2) == 0);
}

GMS_TEST(SeatAuthWithoutAControlChannelNothingIsAsked) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  CHECK(net.Got(1, gms::kGmsHelloPlayer));
  CHECK(match.asked.empty());
}

GMS_TEST(HostHardOnlyTheFirstHelloReqCounts) {
  // a seated client can't make GameAborted name someone else by claiming another id afterwards
  Capture log;
  Net net;
  M match;
  match.verify = false;
  match.setup.prepareFlags = 1;  // connect type 1: the match-found prompt
  gms::SessionOptions so = Options();
  so.matchFoundTimeoutMs = 60000;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  CHECK(net.Got(1, gms::kGmsHelloPlayer));
  Hello(s, 1, kOther);
  CHECK(s.Seats().ByUser(kOther) == nullptr);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGamePrepareForStart); }));
  Status(s, 1, gms::kStatusMatchDeclined, 0.0f);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGameAborted); }));
  Status(s, 1, gms::kStatusLeftGame, 0.0f);
  CHECK(TickUntil(s, [&] { return !match.aborted.empty(); }));
  CHECK_EQ(match.aborted[0].size(), size_t{1});
  CHECK_EQ(match.aborted[0][0], kVictim);
}

GMS_TEST(HostHardALoadedReportBeforeTheLoadDoesNotCount) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::GmsSession s(net, match, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  Status(s, 1, gms::kStatusLevelLoaded | gms::kStatusLoadedPart, 1.0f);  // right after HelloPlayer
  CHECK_EQ(match.loadedPoints, 0);  // no deploy, no bots
  CHECK(s.State(1).find("client loaded (0x401) no") != std::string::npos);
  CHECK(s.State(1).find("status 0x10;") != std::string::npos);  // no 0x400 in the word the others see
  CHECK(log.Has("reports the level loaded before its load; not counted"));
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGamePrepareForStart); }));
  Clock5(s, 1);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterPreGameFlow); }));
  // with status 0x10 B6 opens the loading: from here on it counts, as the real client's
  Status(s, 1, gms::kStatusLevelLoaded | gms::kStatusLoadedPart, 1.0f);
  CHECK_EQ(match.loadedPoints, 1);
  CHECK(s.State(1).find("client loaded (0x401) yes") != std::string::npos);
}

GMS_TEST(HostHardAnEarlyAllLoadedStartsNoClock) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::SessionOptions so = Options();
  so.waitForPlayers = false;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  s.OnConnected(2, "b|1");
  Hello(s, 2, kOther);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGamePrepareForStart) && net.Got(2, gms::kGmsGamePrepareForStart); }));
  Clock5(s, 1);
  Clock5(s, 2);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterPreGameFlow) && net.Got(2, gms::kGmsEnterPreGameFlow); }));
  // client 1 says everybody loaded, at once, though it and client 2 still load: no C0 03, no EnterGame
  AllLoaded(s, 1);
  TickFor(s, 30);
  CHECK(!net.GotPreGame(1, gms::kPreGameDone));
  CHECK(!net.Got(1, gms::kGmsEnterGame));
  CHECK(log.Has("the server's statuses don't say so yet"));
  Status(s, 1, gms::kStatusLevelLoaded | gms::kStatusLoadedPart, 1.0f);  // its own report alone isn't enough
  TickFor(s, 30);
  CHECK(!net.Got(1, gms::kGmsEnterGame));
  Status(s, 2, gms::kStatusLevelLoaded | gms::kStatusLoadedPart, 1.0f);  // now the server's statuses agree
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterGame); }));
  CHECK(!net.Got(2, gms::kGmsEnterGame));  // client 2 hasn't sent its C0 04
  AllLoaded(s, 2);
  CHECK(TickUntil(s, [&] { return net.Got(2, gms::kGmsEnterGame); }));
}

GMS_TEST(HostHardAnAllLoadedThatDoesNotComeTrueEndsAtTheDeadline) {
  // the gate never hangs a match: with a client that never loads, the load's deadline goes on
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::SessionOptions so = Options();
  so.waitForPlayers = false;
  so.loadTimeoutMs = 40;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGamePrepareForStart); }));
  Clock5(s, 1);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterPreGameFlow); }));
  AllLoaded(s, 1);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterGame); }));
  CHECK(log.Has("no C0 04 from the client within 40 ms of the load; continuing"));
}

GMS_TEST(HostHardADepartedPlayerHoldsNobodyUp) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::SessionOptions so = Options();
  so.waitForPlayers = false;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  s.OnConnected(2, "b|1");
  Hello(s, 2, kOther);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsGamePrepareForStart) && net.Got(2, gms::kGmsGamePrepareForStart); }));
  Clock5(s, 1);
  Clock5(s, 2);
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterPreGameFlow) && net.Got(2, gms::kGmsEnterPreGameFlow); }));
  Status(s, 1, gms::kStatusLevelLoaded | gms::kStatusLoadedPart, 1.0f);
  AllLoaded(s, 1);
  s.OnDisconnected(2);  // client 2 left while loading
  CHECK(TickUntil(s, [&] { return net.Got(1, gms::kGmsEnterGame); }));
}

GMS_TEST(HostHardCommRate) {
  gms::CommRate r;
  double now = 1000;
  int pings = 0, voices = 0;
  for (int i = 0; i < 100; ++i) {
    pings += r.Allow(0, gms::kCmdPing, now) ? 1 : 0;
    voices += r.Allow(0, gms::kCmdVoiceLine, now) ? 1 : 0;
  }
  CHECK_EQ(pings, 10);  // the bursts, at the same instant
  CHECK_EQ(voices, 6);
  CHECK(!r.Allow(0, gms::kCmdDance, now));   // dances, taunts and voice lines share one bucket
  CHECK(!r.Allow(0, gms::kCmdTaunt, now));
  CHECK(r.Allow(1, gms::kCmdPing, now));     // another player has its own
  CHECK(r.Allow(0, gms::kCmdMove, now));     // the rest isn't limited
  CHECK(r.Allow(0, gms::kCmdAbility, now));
  now += 1000;
  CHECK(r.Allow(0, gms::kCmdPing, now) && r.Allow(0, gms::kCmdPing, now) && !r.Allow(0, gms::kCmdPing, now));  // 2 a second
  CHECK(r.Allow(0, gms::kCmdVoiceLine, now) && !r.Allow(0, gms::kCmdVoiceLine, now));                           // 1 a second
  now += 3600000;
  pings = 0;
  for (int i = 0; i < 100; ++i) pings += r.Allow(0, gms::kCmdPing, now) ? 1 : 0;
  CHECK_EQ(pings, 10);  // tokens don't pile up past the burst
  gms::CommRateOptions off;
  off.on = false;
  gms::CommRate none(off);
  for (int i = 0; i < 100; ++i) CHECK(none.Allow(0, gms::kCmdPing, 1000));
}

GMS_TEST(HostHardPingsOverTheRateAreDropped) {
  Capture log;
  Net net;
  int handled = 0;
  struct Counting final : gms::IMatch {
    int* n;
    explicit Counting(int* c) : n(c) {}
    gms::GameSetup Setup() override { return gms::GameSetup(); }
    bool OnHelloReq(gms::ConnId, const gms::HelloReq&, gms::PlayerSlot*) override { return true; }
    void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
    void OnClientMessage(gms::ConnId, uint8_t, const uint8_t*, size_t) override { ++*n; }
  } counting(&handled);
  gms::GmsSession s(net, counting, Options(), gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  gms::ActionCommand voice;
  voice.type = gms::kCmdVoiceLine;
  voice.seq = 1;
  voice.caster = 1;
  const Bytes ping = gms::BuildActionCommand(voice);
  const int before = handled;
  for (int i = 0; i < 50; ++i) s.OnMessage(1, ping.data(), ping.size());
  CHECK_EQ(handled - before, 6);
  CHECK(net.closed.empty());  // dropped, not kicked
  CHECK(log.Has("voice line over the player's rate"));
}

GMS_TEST(HostHardProgressOnlyEchoesAreThrottledAndQuiet) {
  Capture log;
  Net net;
  M match;
  match.verify = false;
  gms::SessionOptions so = Options();
  so.progressEchoMs = 1000;
  so.statusEchoLogs = 2;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(1, "a|1");
  Hello(s, 1, kVictim);
  const int before = net.Count(1, gms::kGmsLabsPlayerGlobalUpdate);
  for (int i = 0; i < 500; ++i) Status(s, 1, 0, 0.001f * static_cast<float>(i + 1));
  CHECK_EQ(net.Count(1, gms::kGmsLabsPlayerGlobalUpdate) - before, 1);  // only the first within the second
  // a status word that changes always goes out, whatever the progress does
  Status(s, 1, gms::kStatusMatchAccepted, 0.9f);
  CHECK_EQ(net.Count(1, gms::kGmsLabsPlayerGlobalUpdate) - before, 2);

  // past statusEchoLogs the echoes aren't logged
  Net net2;
  so.progressEchoMs = 0;
  gms::GmsSession s2(net2, match, so, gms::SteadyClock::now());
  s2.OnConnected(1, "a|1");
  Hello(s2, 1, kVictim);
  Lines().clear();
  for (int i = 0; i < 50; ++i) Status(s2, 1, 0, 0.01f * static_cast<float>(i + 1));
  CHECK(log.Count("status echo 0x") <= 2);
  CHECK(log.Has("further echoes of this client's progress aren't logged"));
}
