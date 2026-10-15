// gms/core/session: GmsSession with a ScriptedMatch, served by gms::Endpoint (RakNet 4.081, protocol 5) on
// 127.0.0.1 and played against a RakNet 4.081 test client (protocol 5 too) that behaves like the live client did in
// d2b_run1/d2b_run2/d2e_run5 (docs/GMS_PROTOCOL.md "The live session (D2b-live)"). Server and client run in one
// thread, alternately; RakNet's I/O threads are internal. Plus a fake transport for the load timeout.
// D3-core: shaper select over RakNet (O2 script B with a scripted ally, O4 the timers with a silent client, a match
// without shaper select), and gms_script.exe itself with the shaper select options against the same test client.
// G3a: several humans against one session over a fake transport (several client models, connections that end and
// come back on new connection ids): the departed human's 0x4, the reveal for a late C8 02, the records only of
// players a client has, a returning user's slot (RejoinMap), a superseded connection.
#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <deque>
#include <fstream>
#include <functional>
#include <map>
#include <memory>
#include <set>
#include <sstream>
#include <string>
#include <thread>
#include <utility>
#include <vector>

#include "MessageIdentifiers.h"
#include "RakNetTypes.h"
#include "RakPeerInterface.h"
#include "core/common/bytes.h"
#include "core/common/log.h"
#include "core/net/endpoint.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"
#include "core/protocol/wire.h"
#include "core/session/gms_session.h"
#include "core/session/pump.h"
#include "core/session/rejoin.h"
#include "core/session/spectators.h"
#include "script/scripted_match.h"
#include "tests/check.h"

// D3-core: the gms_script.exe test starts the binary (after the RakNet headers, which bring winsock2.h first)
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

using gms::Bytes;

namespace {

// ---- log capture: the session's lines are kept and printed only when a test failed --------------------------------
std::vector<std::string>& Captured() {
  static std::vector<std::string> lines;
  return lines;
}
void CaptureSink(const std::string& line) { Captured().push_back(line); }

struct LogCapture {
  int failuresBefore = gmstest::Failures();
  LogCapture() {
    Captured().clear();
    gms::SetLogSink(CaptureSink);
  }
  ~LogCapture() {
    gms::SetLogSink(nullptr);
    if (gmstest::Failures() != failuresBefore) {
      std::printf("  --- session log ---\n");
      for (const std::string& l : Captured()) std::printf("  | %s\n", l.c_str());
    }
  }
  bool Has(const std::string& text) const {
    for (const std::string& l : Captured())
      if (l.find(text) != std::string::npos) return true;
    return false;
  }
};

Bytes FromHex(const char* hex) {
  Bytes b;
  gms::ParseHex(hex, b);
  return b;
}

// ---- the test client: RakNet 4.081 (protocol 5), like the live client's transport ----------------------------------
class TestClient {
 public:
  ~TestClient() {
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
      } else if (id == ID_DISCONNECTION_NOTIFICATION || id == ID_CONNECTION_LOST) {
        connected_ = false;
      } else if (id >= gms::kGmsFirstWireByte) {
        inbox_.emplace_back(p->data, p->data + p->length);
      }
    }
  }
  void Send(const Bytes& m) {
    peer_->Send(reinterpret_cast<const char*>(m.data()), static_cast<int>(m.size()), HIGH_PRIORITY, RELIABLE_ORDERED,
                0, server_, false);
  }
  void Disconnect() { peer_->CloseConnection(server_, true); }
  gms::ConnId Guid() const { return peer_->GetMyGUID().g; }
  bool Connected() const { return connected_; }
  std::deque<Bytes>& Inbox() { return inbox_; }

 private:
  RakNet::RakPeerInterface* peer_ = nullptr;
  RakNet::RakNetGUID server_;
  bool connected_ = false;
  std::deque<Bytes> inbox_;
};

// ---- the live client's side of the session, as recorded in d2b_run2 / d2e_run5 ----------------------------------------
// D3-core adds the client's shaper select as GMS_PROTOCOL.md *Shaper select* derives it (PreDungeonState,
// cShaperSelect): C8 01 and C8 02 on B6, C8 07 on entering phase 2 (by the lock answer or the GMS's phase 2), C8 08
// on 2 -> 3 by the GMS, C0 04 once every player's status has 0x100 (sub-state 1) or 0x400 (loading), and optional
// clicks (a card, Lock in, the role/perk Lock in once the GMS's phase 2 enabled it, the skin Lock in).
struct LiveClientModel {
  std::function<void(const Bytes&)> send;
  bool loadOnC001 = false;  // D2b-static's script (status without 0x10): the level loads on C0 01, not on B6
  uint64_t userId = 2000000011ull;
  int64_t clientMs = 31314;
  int clocksSent = 0, answers = 0;
  bool primedSent = false, allLoadedSent = false;
  std::vector<Bytes> received;   // every GMS message from the server, in order
  std::vector<Bytes> answersGot;  // the ClockSync answers
  // every player from HelloPlayer, and its mStatus as the updates set it (the pre-game gates count them all)
  uint8_t myIndex = 0xFF;
  std::vector<uint8_t> players;
  std::map<uint8_t, uint32_t> statuses;
  std::map<uint8_t, uint32_t> nouns;  // each player's last mpNounDef
  // D3-core: shaper select
  bool shaperSelect = false;  // B6 opens shaper select (the GMS gave no status 0x10)
  uint32_t click = 0;         // a card to click (C8 04), once player `clickAfterLockOf`'s record is locked
  int clickAfterLockOf = -1;  // -1: at the first phase message
  bool lockShaper = false, lockRole = false, lockSkin = false;  // Lock in clicks
  bool inShaperSelect = false, clicked = false, roleLocked = false, skinLocked = false, picksAckSent = false;
  int phase = 1;  // cShaperSelect +0x1740: 1 until a phase change took
  std::map<uint8_t, gms::ShaperRecord> records;
  std::vector<std::pair<int, uint64_t>> phases;  // every C8 01 received: phase, end (server ms)
  std::vector<int64_t> phaseServerNow;           // the server clock when it arrived (from the rig)
  std::function<int64_t()> serverNow;
  int lockAnswersNot01 = 0, shaperAfterLoad = 0;
  // G3a: PlayerJoined/PlayerDeparted change the players; shaper select has records only for the players at B6, and a
  // record or role toggle for another index asserts ("Using invalid player index"); an update for an index without a
  // player is dropped
  std::set<uint8_t> recordsKnown;
  int unknownRecords = 0, unknownUpdates = 0;
  // G3: with connect type 1 the lobby sends 0x10000 only when the player accepts (MatchFoundUI, 0x005456F0)
  bool autoAccept = true;
  // G3b: GameAborted (0x89) in the lobby: LobbyState_HandleMessage answers with status 0x40000
  bool ackAbort = true;
  std::vector<Bytes> aborts;
  // D3-perks: the page the role/perk page has selected (cShaperSelect +0x1F64; 0 = none), sent as C8 0A right before the
  // role lock (ShaperSelect_LockInPerksAndRoles); the pages C8 09 listed
  uint32_t perkPage = 0;
  std::vector<uint32_t> pagesListed;
  // WARDSKIN: the ward skin and announcer pack it reports on entering phase 3 (C8 0E kind 0/1; 0 = none sent), and its
  // local player's vanity slots as the C8 0E echo and OnLocalPlayerReconnect (0x8A) set them
  uint32_t ward = 0, announcer = 0;
  uint32_t myVanity[2] = {0, 0};
  int reconnects = 0;
  // B15b: the game state EnterGame took it to (GameClient_OnGmsEnterGame 0x006B5CD0): 5 Dungeon, 6 Spectator (its own
  // status has the observer bit 0x20), 8 GameOver; 0 before
  int state = 0;
  int shaperReadySent = 0;  // C8 02s it sent (an observer's client sends none)

  // B15b: LabsPlayer_IsObserver of its own player
  bool Observer() const {
    auto it = statuses.find(myIndex);
    return it != statuses.end() && (it->second & gms::kStatusObserver) != 0;
  }
  bool Skips() const {
    auto it = statuses.find(myIndex);
    return it != statuses.end() && (it->second & gms::kStatusSkipShaperSelect) != 0;
  }

  uint8_t MyRole() const {
    auto it = records.find(myIndex);
    return it == records.end() ? 0 : it->second.role;
  }
  uint32_t MySkin() const {
    auto it = records.find(myIndex);
    return it == records.end() ? 0 : it->second.skin;
  }
  // The pre-game gates: every player without status 0x4 (GameSimulator_ListPlayerIndices) has `bit`.
  bool AllHave(uint32_t bit) const {
    if (players.empty()) return false;
    for (uint8_t p : players) {
      auto it = statuses.find(p);
      if (it != statuses.end() && (it->second & gms::kStatusGone)) continue;
      if (it == statuses.end() || !(it->second & bit)) return false;
    }
    return true;
  }
  bool Has(uint8_t index) const { return std::find(players.begin(), players.end(), index) != players.end(); }
  void SendShaper(uint8_t sub, uint32_t id = 0, uint8_t role = 0, uint8_t kind = 0) {
    gms::ShaperSelectRequest r;
    r.sub = sub;
    r.id = id;
    r.role = role;
    r.kind = kind;
    send(gms::BuildShaperSelectRequest(r));
  }
  void LockInPerksAndRoles() {  // 0x00521140: the selected page (C8 0A) if there is one, then the role lock (C8 08)
    roleLocked = true;
    if (perkPage) SendShaper(gms::kShaperPerkPage, perkPage);
    SendShaper(gms::kShaperLockRole, 0, MyRole());
  }
  void EnterPhase(int p) {
    // B15b: an observer's client sends nothing (no record of its own; 2 -> 3 is ShaperSelect_SetPhase(3) for it)
    if (p == 2 && !Observer()) SendShaper(gms::kShaperSelectRole, 0, MyRole());  // the record's role entering phase 2
    if (p == 3 && phase == 2 && !roleLocked && !Observer()) LockInPerksAndRoles();  // on 2 -> 3
    // WARDSKIN: entering phase 3 the client reports its preselected ward (seen live: C8 0E 00 <ward>)
    if (p == 3 && !Observer()) {
      if (ward) SendShaper(gms::kShaperSelectVanity, ward, 0, gms::kVanityWard);
      if (announcer) SendShaper(gms::kShaperSelectVanity, announcer, 0, gms::kVanityAnnouncer);
    }
    phase = p;
  }
  void Click() {
    clicked = true;
    SendShaper(gms::kShaperRequest, click);
  }
  void OnShaper(const Bytes& m) {
    if (!inShaperSelect) {  // after C0 01 the client drops them silently
      ++shaperAfterLoad;
      return;
    }
    if (m.size() < 2) return;
    switch (m[1]) {
      case gms::kShaperPhase: {
        if (m.size() < 14) return;
        const int p = static_cast<int>(gms::GetU32LE(&m[10]));
        phases.emplace_back(p, gms::GetU64LE(&m[2]));
        phaseServerNow.push_back(serverNow ? serverNow() : 0);
        if (p != phase) {
          EnterPhase(p);
        } else if (p == 2 && lockRole && !roleLocked) {  // the GMS's phase 2 enabled the role/perk Lock in
          LockInPerksAndRoles();
          phase = 3;
        } else if (p == 3 && lockSkin && !skinLocked) {
          skinLocked = true;
          SendShaper(gms::kShaperLockSkin, MySkin());
        }
        if (p == 1 && click && !clicked && clickAfterLockOf < 0) Click();
        break;
      }
      case gms::kShaperRecord: {
        gms::ShaperRecord r;
        if (!gms::ParseShaperRecord(m.data() + 2, m.size() - 2, &r)) return;
        if (!recordsKnown.count(r.index)) {  // ShaperSelect_SetPlayerRecord asserts
          ++unknownRecords;
          return;
        }
        records[r.index] = r;
        if (click && !clicked && clickAfterLockOf == r.index && (r.flags & gms::kRecordShaperLocked)) Click();
        break;
      }
      case gms::kShaperRequestResult:
        if (m.size() >= 7 && m[6] == 1 && gms::GetU32LE(&m[2]) == click && lockShaper)
          SendShaper(gms::kShaperLockIn, click);
        break;
      case gms::kShaperLockResult:
        if (m.size() < 7 || m[6] != 1) ++lockAnswersNot01;
        if (phase != 2) EnterPhase(2);
        break;
      case gms::kShaperRoleToggle:
        if (m.size() >= 3 && !recordsKnown.count(m[2])) ++unknownRecords;
        break;
      case gms::kShaperVanity:  // LabsPlayer_SetVanityAsset on the local player
        if (m.size() >= 7 && m[2] <= 1) myVanity[m[2]] = gms::GetU32LE(&m[3]);
        break;
      case gms::kShaperPerkPages:  // ShaperSelect_ReadPerkPageIds: u8 n, n x u32
        pagesListed.clear();
        for (size_t i = 3; m.size() >= 3 && i + 4 <= m.size() && pagesListed.size() < m[2]; i += 4)
          pagesListed.push_back(gms::GetU32LE(&m[i]));
        // the drop-down preselects its page and reports it at once (d3perks_run1: C8 0A right after C8 09)
        if (perkPage && std::find(pagesListed.begin(), pagesListed.end(), perkPage) != pagesListed.end())
          SendShaper(gms::kShaperPerkPage, perkPage);
        break;
      default:
        break;
    }
  }

  static Bytes Status(uint32_t flags, float progress) {
    Bytes m{gms::kGmsPlayerStatusUpdate};
    gms::PutU32LE(m, flags);
    gms::PutF32LE(m, progress);
    return m;
  }
  void SendClock() {
    Bytes m{gms::kGmsClockSync};
    gms::PutI64LE(m, clientMs);
    clientMs += 400;
    ++clocksSent;
    send(m);
  }
  void On(const Bytes& m) {
    received.push_back(m);
    switch (m[0]) {
      case gms::kGmsConnected: {  // HelloReq: user id, playgroup id 0
        Bytes h{gms::kGmsHelloReq};
        gms::PutU64LE(h, userId);
        gms::PutU64LE(h, 0);
        send(h);
        break;
      }
      case gms::kGmsGamePrepareForStart:  // ready, status 0x10000 (accepted), first ClockSync
        send(gms::BuildPreGame(gms::kPreGameAllLoaded));
        if (autoAccept) send(Status(0x10000, 0.0f));  // connect type 1: only once the player clicks ACCEPT (G3)
        SendClock();
        break;
      case gms::kGmsOnLocalPlayerReconnect:  // 0x006B6480: two u32, each non-zero one into the local player
        ++reconnects;
        for (int k = 0; k < 2 && m.size() >= 9; ++k)
          if (const uint32_t id = gms::GetU32LE(&m[1 + 4 * k])) myVanity[k] = id;
        break;
      case gms::kGmsGameAborted:
        aborts.push_back(m);
        if (ackAbort) send(Status(0x40000, 0.0f));
        break;
      case gms::kGmsClockSync:
        answersGot.push_back(m);
        ++answers;
        if (clocksSent < 5) {
          SendClock();
        } else if (!primedSent) {  // clock primed
          primedSent = true;
          send(Status(0x80, 0.0f));
        }
        break;
      case gms::kGmsHelloPlayer:  // n x {type, index, IP, port}: the players; type 0 = me
        for (size_t i = 1; i + 8 <= m.size(); i += 8) {
          players.push_back(m[i + 1]);
          if (m[i] == 0) myIndex = m[i + 1];
        }
        break;
      case gms::kGmsPlayerJoined:  // G3a: GameSimulator_AddPlayer
        if (m.size() >= 2 && !Has(m[1])) players.push_back(m[1]);
        break;
      case gms::kGmsPlayerDeparted:  // G3a: GameSimulator_RemovePlayer
        if (m.size() >= 2) players.erase(std::remove(players.begin(), players.end(), m[1]), players.end());
        break;
      case gms::kGmsEnterPreGameFlow:  // DemoModeMsgs 01 on leaving the lobby, FriendInfo, and with 0x10 the load
        send(Bytes{gms::kGmsDemoModeMsgs, 0x01});
        send(Bytes{gms::kGmsFriendInfo, 0x00, 0x00});
        if (shaperSelect && !Skips()) {  // without 0x10: shaper select (C8 01 hello, C8 02 ready)
          recordsKnown = std::set<uint8_t>(players.begin(), players.end());  // the records it creates
          inShaperSelect = true;
          SendShaper(gms::kShaperHello);
          if (!Observer()) {  // B15b: ShaperSelect_Init 0x00524620 sends no C8 02 for an observer
            SendShaper(gms::kShaperReady);
            ++shaperReadySent;
          }
        } else if (!loadOnC001) {
          send(Status(0x401, 1.0f));
        }
        break;
      case gms::kGmsBattlegroundsPreGameMsgs:
        if (m.size() >= 2 && m[1] == gms::kPreGameLoad && (loadOnC001 || shaperSelect)) {
          inShaperSelect = false;
          send(Status(0x401, 1.0f));
        }
        break;
      case gms::kGmsLabsPlayerGlobalUpdate: {
        // every player's status; in shaper select "all have 0x100", while loading "all have 0x400" -> C0 04, once
        uint8_t index = 0;
        std::vector<int> members;
        gms::PlayerGlobalValues v;
        if (!gms::ParsePlayerGlobalUpdate(m.data(), m.size(), &index, &members, &v)) break;
        if (!Has(index)) {  // 0x006B9A70: an index without a player drops the update
          ++unknownUpdates;
          break;
        }
        if (!members.empty() && members[0] == gms::kPlayerStatus) statuses[index] = v.status;
        if (std::find(members.begin(), members.end(), gms::kPlayerNounDef) != members.end()) nouns[index] = v.noun;
        if (inShaperSelect && !picksAckSent && AllHave(gms::kStatusShaperSelectDone)) {
          picksAckSent = true;
          send(gms::BuildPreGame(gms::kPreGameAllLoaded));
        } else if (!inShaperSelect && !allLoadedSent && AllHave(gms::kStatusLevelLoaded)) {
          allLoadedSent = true;
          send(gms::BuildPreGame(gms::kPreGameAllLoaded));
        }
        break;
      }
      case gms::kGmsShaperSelectMsgs:
        OnShaper(m);
        break;
      case gms::kGmsEnterGame:  // B15b: Dungeon, or Spectator for an observer, GameOver for a non-zero byte
        state = m.size() >= 2 && m[1] ? 8 : Observer() ? 6 : 5;
        break;
      default:
        break;
    }
  }
  // The received messages without the ClockSync answers, as hex.
  std::vector<std::string> Sequence() const {
    std::vector<std::string> s;
    for (const Bytes& m : received)
      if (m[0] != gms::kGmsClockSync) s.push_back(gms::Hex(m, static_cast<size_t>(-1)));
    return s;
  }
  // The same, with each C8 01 as "c801:p<phase>" (its end depends on the clock) and its one-second repeats left out.
  std::vector<std::string> ShaperSequence() const {
    std::vector<std::string> s;
    int lastPhase = 0;
    for (const Bytes& m : received) {
      if (m[0] == gms::kGmsClockSync) continue;
      if (m[0] == gms::kGmsShaperSelectMsgs && m.size() >= 14 && m[1] == gms::kShaperPhase) {
        const int p = static_cast<int>(gms::GetU32LE(&m[10]));
        if (p != lastPhase) s.push_back("c801:p" + std::to_string(p));
        lastPhase = p;
        continue;
      }
      s.push_back(gms::Hex(m, static_cast<size_t>(-1)));
    }
    return s;
  }
};

// ---- server and client in one loop -----------------------------------------------------------------------------------
struct LoopbackRig {
  gms::Endpoint ep;
  gms::SteadyClock::time_point start;
  std::unique_ptr<gms::GmsSession> session;
  TestClient client;
  LiveClientModel* model = nullptr;

  bool Start(gms::IMatch& match, const gms::SessionOptions& o) {
    std::string err;
    if (!ep.Start("127.0.0.1", 0, 8, &err)) {
      std::printf("  endpoint: %s\n", err.c_str());
      return false;
    }
    start = gms::SteadyClock::now();
    session.reset(new gms::GmsSession(ep, match, o, start));
    return client.Start(ep.BoundPort());
  }
  void Step() {
    gms::PumpEndpoint(ep, *session, start);
    session->Tick();
    client.Poll();
    while (model && !client.Inbox().empty()) {
      const Bytes m = client.Inbox().front();
      client.Inbox().pop_front();
      model->On(m);
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  bool RunUntil(const std::function<bool()>& done, int timeoutMs) {
    const auto end = gms::SteadyClock::now() + std::chrono::milliseconds(timeoutMs);
    while (gms::SteadyClock::now() < end) {
      Step();
      if (done()) return true;
    }
    return false;
  }
  void RunFor(int ms) {
    RunUntil([] { return false; }, ms);
  }
};

void CheckSequence(const std::vector<std::string>& got, const std::vector<std::string>& want) {
  CHECK_EQ(got.size(), want.size());
  for (size_t i = 0; i < got.size() && i < want.size(); ++i) {
    if (got[i] != want[i]) {
      gmstest::Fail(__FILE__, __LINE__,
                    "message " + std::to_string(i) + ": got " + got[i] + ", want " + want[i]);
      return;
    }
  }
}

// A match that records what the session tells it; accepts or refuses every HelloReq.
struct RecordingMatch final : gms::IMatch {
  bool accept = true;
  int connected = 0, hellos = 0, disconnected = 0;
  std::vector<std::string> points;
  gms::GameSetup Setup() override { return gms::GameSetup{}; }
  void OnConnected(gms::ConnId, const std::string&) override { ++connected; }
  bool OnHelloReq(gms::ConnId, const gms::HelloReq&, gms::PlayerSlot* slot) override {
    ++hellos;
    *slot = gms::PlayerSlot{};
    return accept;
  }
  void OnPoint(gms::ConnId, gms::SessionPoint p, gms::ISender&) override { points.push_back(SessionPointName(p)); }
  void OnDisconnected(gms::ConnId) override { ++disconnected; }
};

}  // namespace

GMS_TEST(SessionScriptAWithInjectsOverRakNet) {
  LogCapture log;
  gms::SessionOptions so;
  so.skipShaper = so.echoStatus = true;  // --script-a
  so.stepMs = 150;
  gms::ScriptOptions mo;
  for (const char* spec : {"hello:c20101", "clock5:c20105", "load:c20104", "loaded:8e01000100", "loaded:8f01000100",
                           "done:c20103", "entergame:c20106", "entergame+50:b300010002"}) {
    gms::InjectSpec s;
    std::string err;
    CHECK(gms::ParseInject(spec, &s, &err));
    mo.inject.push_back(s);
  }
  gms::ScriptedMatch match(mo);
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;

  const bool finished = rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == 0xB3; },
                                     8000);
  CHECK(finished);
  rig.RunFor(100);  // nothing else may follow

  CheckSequence(client.Sequence(), {
      "84",
      "8200007f000001273a",                              // HelloPlayer: me, index 0
      "c20101",                                          // injected at hello
      "a9003301011000000001ab4a04ef0b94357700000000",    // status 0x10, team 1, alpha.Noun, online id (d2e_run5)
      "b50000000002000000fe416d94010000000000000000",    // GamePrepareForStart
      "a900131000010000000000",                          // echo 0x10010
      "c20105",                                          // injected at clock5
      "a900139000010000000000",                          // echo 0x10090
      "b6",                                              // EnterPreGameFlow (load implicit)
      "c20104",                                          // injected at load
      "a90013910401000000803f",                          // echo 0x10491
      "8e01000100", "8f01000100",                        // injected at loaded (D2e's world goes here)
      "c003",                                            // after the client's C0 04
      "c20103",                                          // injected at done
      "b700",                                            // EnterGame
      "c20106",                                          // injected at entergame
      "b300010002",                                      // injected at entergame+50
  });
  // ClockSync: 5 answers, each the client's clock echoed plus a positive server clock
  CHECK_EQ(client.answersGot.size(), size_t{5});
  for (size_t i = 0; i < client.answersGot.size(); ++i) {
    const Bytes& a = client.answersGot[i];
    CHECK_EQ(a.size(), size_t{17});
    if (a.size() != 17) continue;
    CHECK_EQ(gms::GetI64LE(&a[1]), 31314 + 400 * static_cast<int64_t>(i));
    CHECK(gms::GetI64LE(&a[9]) >= 1000);
  }
  // the state line run_session.ps1 summarizes (d2e_run5's, with this test's inject count)
  CHECK_EQ(rig.session->State(rig.client.Guid()),
           std::string("steps sent connected>hello>prepare>pregameflow>load(implicit)>done>entergame; status 0x10491; "
                       "all loaded (C0 04) yes; injected 8/8; client loaded (0x401) yes; client sent: "
                       "BattlegroundsPreGameMsgs x2; DemoModeMsgs x1; FriendInfo x1; HelloReq x1; PlayerStatusUpdate x3;"));
  rig.session->Finish();
  CHECK(log.Has(": steps sent connected>hello>prepare>pregameflow>load(implicit)>done>entergame; status 0x10491;"));
  CHECK(log.Has("ClockSync x5 (all answered)"));
  CHECK(log.Has(" 13 ID_NEW_INCOMING_CONNECTION ("));
  CHECK(log.Has("<- 81 HelloReq (16 B body) 0b943577000000000000000000000000: user id 2000000011, playgroup id 0"));
  CHECK(log.Has("-> b3 PlayerCharacterDeploy (5 B) b300010002: injected at entergame+50"));
}

namespace {

// D2f, seam 5: a match that writes the player updates from its own player state, as the host does from the server's
// cLabsPlayer. Its first update carries team 2 (the session's would say 1), so the test sees whose bytes went out.
struct PlayerStateMatch final : gms::IMatch {
  uint32_t status = 0;
  float progress = 0.f;
  uint64_t user = 0;
  std::vector<std::string> events;  // "status <hex>" (OnPlayerStatus) and "update <members>" (PlayerUpdate), in order
  gms::GameSetup Setup() override { return gms::GameSetup{}; }
  bool OnHelloReq(gms::ConnId, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    *slot = gms::PlayerSlot{};
    user = req.userId;
    status = gms::kStatusSkipShaperSelect;  // as ServerMatch stores it in the server player
    return true;
  }
  void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
  void OnPlayerStatus(gms::ConnId, uint32_t s, float p) override {
    status = s;
    progress = p;
    char b[32];
    std::snprintf(b, sizeof b, "status %x", s);
    events.push_back(b);
  }
  bool PlayerUpdate(gms::ConnId, uint8_t index, const std::vector<int>& members, Bytes* msg) override {
    std::string e = "update";
    for (int m : members) e += " " + std::to_string(m);
    events.push_back(e);
    if (members == gms::PlayerUpdateMembers(status, gms::kNounAlpha)) {
      *msg = gms::BuildPlayerUpdate(index, status, 2, gms::kNounAlpha, user);
      return true;
    }
    if (members == gms::PlayerStatusEchoMembers()) {
      *msg = gms::BuildPlayerStatusEcho(index, status, progress);
      return true;
    }
    return false;
  }
};

}  // namespace

GMS_TEST(SessionPlayerUpdatesFromTheMatch) {
  LogCapture log;
  gms::SessionOptions so;
  so.skipShaper = so.echoStatus = true;  // script A
  so.stepMs = 100;
  PlayerStateMatch match;
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == gms::kGmsEnterGame; },
                     8000));
  CheckSequence(client.Sequence(), {
      "84", "8200007f000001273a",
      "a9003301011000000002ab4a04ef0b94357700000000",  // the match's first update (team 2)
      "b50000000002000000fe416d94010000000000000000",
      "a900131000010000000000", "a900139000010000000000", "b6", "a90013910401000000803f",  // the match's echoes
      "c003", "b700",
  });
  // the members asked for, and every echo built after the match stored the status
  const std::vector<std::string> want = {"update 0 8 16 17", "status 10010", "update 0 1", "status 10090",
                                         "update 0 1", "status 10491", "update 0 1"};
  CHECK_EQ(match.events.size(), want.size());
  for (size_t i = 0; i < want.size() && i < match.events.size(); ++i) CHECK_EQ(match.events[i], want[i]);
  CHECK(log.Has("; from the match (the session's: a9003301011000000001ab4a04ef0b94357700000000)"));
  CHECK(log.Has("status echo 0x10491, progress 1.000; from the match (= the session's bytes)"));
}

GMS_TEST(SessionD2bStaticScriptOverRakNet) {
  // no player update, no echo: C0 01 loads the level, the first 0x401 triggers C0 03 (d2b_run1)
  LogCapture log;
  gms::SessionOptions so;
  so.stepMs = 100;
  gms::ScriptedMatch match(gms::ScriptOptions{});
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.loadOnC001 = true;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;

  CHECK(rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == gms::kGmsEnterGame; },
                     8000));
  rig.RunFor(100);
  CheckSequence(client.Sequence(), {"84", "8200007f000001273a", "b50000000002000000fe416d94010000000000000000", "b6",
                                    "c001", "c003", "b700"});
  CHECK_EQ(rig.session->State(rig.client.Guid()),
           std::string("steps sent connected>hello>prepare>pregameflow>load>done>entergame; client loaded (0x401) yes; "
                       "client sent: BattlegroundsPreGameMsgs x1; DemoModeMsgs x1; FriendInfo x1; HelloReq x1; "
                       "PlayerStatusUpdate x3;"));
}

GMS_TEST(SessionStopAfterAndRefusedHelloAndDisconnect) {
  LogCapture log;
  RecordingMatch match;
  match.accept = false;
  gms::SessionOptions so;
  so.stepMs = 50;
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return match.hellos == 1; }, 5000));
  rig.RunFor(300);
  CheckSequence(client.Sequence(), {"84"});  // no HelloPlayer for a refused user
  CHECK_EQ(match.connected, 1);
  CHECK(match.points.empty());
  CHECK_EQ(rig.session->State(rig.client.Guid()),
           std::string("steps sent connected; HelloReq refused by the match; client loaded (0x401) no; client sent: "
                       "HelloReq x1;"));
  CHECK(log.Has("HelloReq from user 2000000011 refused by the match"));

  // the client leaves: the session reports the connection and forgets it, the match hears of it
  rig.client.Disconnect();
  CHECK(rig.RunUntil([&] { return match.disconnected == 1; }, 5000));
  CHECK_EQ(rig.session->NumConnections(), size_t{0});
  CHECK(log.Has(" 15 ID_DISCONNECTION_NOTIFICATION ("));
}

GMS_TEST(SessionStopsAfterTheNamedStep) {
  LogCapture log;
  RecordingMatch match;
  gms::SessionOptions so;
  so.stepMs = 20;
  so.stopAfter = gms::ParseSessionStep("prepare");
  CHECK_EQ(so.stopAfter, static_cast<int>(gms::kStepPrepare));
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return client.answers == 5; }, 5000));
  rig.RunFor(300);  // well past step-ms: no EnterPreGameFlow (d2b_run4's --stop-after prepare)
  CheckSequence(client.Sequence(), {"84", "8200007f000001273a", "b50000000002000000fe416d94010000000000000000"});
  CHECK_EQ(match.points.size(), size_t{3});
  if (match.points.size() == 3) {
    CHECK_EQ(match.points[0], std::string("hello"));
    CHECK_EQ(match.points[1], std::string("prepare"));
    CHECK_EQ(match.points[2], std::string("clock5"));
  }
}

namespace {

struct FakeTransport final : gms::ITransport {
  std::vector<Bytes> sent;
  bool Send(gms::ConnId, const Bytes& msg) override {
    sent.push_back(msg);
    return true;
  }
  bool SentWire(uint8_t wire) const {
    for (const Bytes& m : sent)
      if (m[0] == wire) return true;
    return false;
  }
};

}  // namespace

GMS_TEST(SessionLoadTimeoutWithoutTheClientsC004) {
  // script A, but the client never sends C0 04: after load-timeout-ms the session goes on with C0 03 and EnterGame
  LogCapture log;
  FakeTransport net;
  RecordingMatch match;
  gms::SessionOptions so;
  so.skipShaper = so.echoStatus = true;
  so.stepMs = 1;
  so.loadTimeoutMs = 40;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  const gms::ConnId conn = 7;
  s.OnConnected(conn, "fake|1");
  Bytes hello = FromHex("810b943577000000000000000000000000");
  s.OnMessage(conn, hello.data(), hello.size());
  auto tickUntil = [&](uint8_t wire, int ms) {
    const auto end = gms::SteadyClock::now() + std::chrono::milliseconds(ms);
    while (gms::SteadyClock::now() < end) {
      s.Tick();
      if (net.SentWire(wire)) return true;
      std::this_thread::sleep_for(std::chrono::milliseconds(1));
    }
    return false;
  };
  CHECK(tickUntil(gms::kGmsGamePrepareForStart, 1000));
  for (int i = 0; i < 5; ++i) {
    Bytes c{gms::kGmsClockSync};
    gms::PutI64LE(c, 1000 + i);
    s.OnMessage(conn, c.data(), c.size());
  }
  CHECK(tickUntil(gms::kGmsEnterPreGameFlow, 1000));
  CHECK(!net.SentWire(gms::kGmsEnterGame));
  CHECK(tickUntil(gms::kGmsEnterGame, 2000));
  CHECK(log.Has("no C0 04 from the client within 40 ms of the load; continuing"));
  CHECK_EQ(s.State(conn), std::string("steps sent connected>hello>prepare>pregameflow>load(implicit)>done>entergame; "
                                      "status 0x10; all loaded (C0 04) no; client loaded (0x401) no; client sent: "
                                      "HelloReq x1;"));
}

// ---- D3-core: shaper select over RakNet ------------------------------------------------------------------------------

namespace {

// an empty 15-byte record (nothing picked) for players 0 and 1
const char* const kRecordEmpty0 = "c80300" "00000000" "00000000" "00" "00000000" "00";
const char* const kRecordEmpty1 = "c80301" "00000000" "00000000" "01" "00000000" "00";

gms::ScriptOptions ShaperOptions(const char* list, const char* timers, const char* ally) {
  gms::ScriptOptions mo;
  std::string err;
  CHECK(gms::ParseShaperList(list, &mo.shapers.list, &err));
  CHECK(gms::ParseShaperTimers(timers, &mo.shaperTimers, &err));
  if (ally) {
    gms::AllySpec a;
    CHECK(gms::ParseAlly(ally, &a, &err));
    mo.allies.push_back(a);
  }
  return mo;
}

// The session's server clock (GmsSession::ServerMs) as the rig started it.
int64_t RigServerMs(const LoopbackRig& rig) {
  return 1000 + std::chrono::duration_cast<std::chrono::milliseconds>(gms::SteadyClock::now() - rig.start).count();
}

}  // namespace

GMS_TEST(SessionShaperSelectScriptBOverRakNet) {
  // O2: script B (GMS_PROTOCOL.md *Script B (final)*) with the ally scripted at +150/+300 ms; the client model clicks
  // alpha once the ally has locked, then Lock in, the role/perk Lock in and the skin Lock in (04, 05, 07, 08, 0D)
  LogCapture log;
  gms::SessionOptions so;
  so.shaperSelect = true;
  so.stepMs = 50;
  gms::ScriptedMatch match(ShaperOptions("alpha.Noun:1,artificer.Noun:1,gunman.Noun:2", "30000,30000,10000,16",
                                         "1:1:artificer.Noun:150:300:1"));
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.userId = 2000000001ull;  // script B's
  client.shaperSelect = true;
  client.click = gms::kNounAlpha;
  client.clickAfterLockOf = 1;
  client.lockShaper = client.lockRole = client.lockSkin = true;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  client.serverNow = [&rig] { return RigServerMs(rig); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == gms::kGmsEnterGame; },
                     10000));
  rig.RunFor(100);
  CheckSequence(client.ShaperSequence(), {
      "84",
      "8200007f000001273a01017f000001273a",                      // 1: me and the ally
      "a900320100010194357700000000",                            // 2: team 1, online id, no 0x10, no shaper
      "a9013209010000010001010100000000000000",                  // 2a: the ally: 0x10000, team 1, bot, online id 1
      "b50000000002000000fe416d94010000000000000000",            // 3
      "a900130000010000000000", "a900138000010000000000",        // 3-6: echoes 0x10000, 0x10080
      "b6",                                                      // 7
      "c802ab4a04ef0100000000009eb0a85d010000000000271e1d7c020000000000",  // 9
      kRecordEmpty0, kRecordEmpty1,                              // 10
      "c801:p1",                                                 // 11
      "c803019eb0a85d9eb0a85d010000000000",                      // 12: the ally picks
      "c803019eb0a85d9eb0a85d010000000001",                      // 13: the ally locks
      "c80300ab4a04efab4a04ef000000000000", "c804ab4a04ef01",    // 15
      "c80300ab4a04efab4a04ef000000000001", "c80b01ab4a04ef00", "c805ab4a04ef01",  // 17
      "c801:p2", "c8060101",                                     // 20 (+ the ally's intended role)
      "c80300ab4a04efab4a04ef000000000003", "c803019eb0a85d9eb0a85d010000000003",  // 22
      "c801:p3",                                                 // 23
      "c801:p4",                                                 // 25
      "a9003102018001010000ab4a04ef", "a90131020100010100019eb0a85d",  // 26: the reveal
      "c001",                                                    // 28
      "a90113000501000000803f",                                  // 28a: the ally loaded
      "a90013810501000000803f",                                  // 30: echo 0x10581
      "c003", "b700",                                            // 31
  });
  CHECK_EQ(client.lockAnswersNot01, 0);
  CHECK_EQ(client.shaperAfterLoad, 0);
  // the phase ends: phase 1 the full timer from the C8 02, the others from the lock that ended the phase before
  CHECK_EQ(client.phases.size(), client.phaseServerNow.size());
  const int64_t timer[5] = {0, 30000, 30000, 10000, 16};
  for (size_t i = 0; i < client.phases.size(); ++i) {
    const int p = client.phases[i].first;
    if (i > 0 && client.phases[i - 1].first == p) continue;  // repeats
    const int64_t left = static_cast<int64_t>(client.phases[i].second) - client.phaseServerNow[i];
    CHECK(p >= 1 && p <= 4);
    if (p < 1 || p > 4) continue;
    if (!(left <= timer[p] && left > timer[p] - 1500))
      gmstest::Fail(__FILE__, __LINE__, "phase " + std::to_string(p) + " ends " + std::to_string(left) + " ms after it arrived");
  }
  CHECK_EQ(rig.session->State(rig.client.Guid()),
           std::string("steps sent connected>hello>prepare>pregameflow>shaperselect>load>done>entergame; "
                       "status 0x10581; all loaded (C0 04) yes; shaper select phase 4; picks 0=0xef044aab/0xef044aab r0 "
                       "1=0x5da8b09e/0x5da8b09e r1; client loaded (0x401) yes; client sent: BattlegroundsPreGameMsgs x3; "
                       "DemoModeMsgs x1; FriendInfo x1; HelloReq x1; PlayerStatusUpdate x3; ShaperSelectMsgs x7;"));
  CHECK(log.Has("step shaperselect: the client's C8 02 answered (shaper select phase 1)"));
  CHECK(log.Has("shaper select: reveal: shaper select phase 4; picks 0=0xef044aab/0xef044aab r0"));
}

GMS_TEST(SessionShaperSelectTimeoutsOverRakNet) {
  // O4 over the session: L1's options with short timers and a client that clicks nothing; the phase-1 timer picks
  // the first selectable entry (artificer), C8 0B before phase 2, every phase on time, the default skin, C0 01 ... B7
  LogCapture log;
  gms::SessionOptions so;
  so.shaperSelect = true;
  so.stepMs = 50;
  gms::ScriptOptions mo = ShaperOptions("artificer.Noun:1,alpha.Noun:1", "300,300,200,16", nullptr);
  gms::InjectSpec inj;
  std::string err;
  CHECK(gms::ParseInject("picked:c20107", &inj, &err));  // the new injection point
  mo.inject.push_back(inj);
  gms::ScriptedMatch match(mo);
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.userId = 2000000001ull;
  client.shaperSelect = true;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  client.serverNow = [&rig] { return RigServerMs(rig); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == gms::kGmsEnterGame; },
                     10000));
  rig.RunFor(100);
  CheckSequence(client.ShaperSequence(), {
      "84", "8200007f000001273a", "a900320100010194357700000000",
      "b50000000002000000fe416d94010000000000000000", "a900130000010000000000", "a900138000010000000000", "b6",
      "c8029eb0a85d010000000000ab4a04ef010000000000", kRecordEmpty0, "c801:p1",
      "c803009eb0a85d9eb0a85d000000000001", "c80b019eb0a85d00", "c801:p2",  // the phase-1 timer
      "c801:p3", "c803009eb0a85d9eb0a85d000000000003",                     // the client's 08 on 2 -> 3
      "c801:p4", "a9003102018001010000" "9eb0a85d", "c20107",              // the reveal, injected at picked
      "c001", "a90013810501000000803f", "c003", "b700",
  });
  // timer-ended phases follow each other exactly: 2 ends 300 ms after 1, 3 200 ms after 2, 4 16 ms after 3
  std::vector<uint64_t> ends(5, 0);
  for (const auto& p : client.phases)
    if (p.first >= 1 && p.first <= 4) ends[static_cast<size_t>(p.first)] = p.second;
  CHECK_EQ(ends[2] - ends[1], uint64_t{300});
  CHECK_EQ(ends[3] - ends[2], uint64_t{200});
  CHECK_EQ(ends[4] - ends[3], uint64_t{16});
  CHECK_EQ(rig.session->State(rig.client.Guid()),
           std::string("steps sent connected>hello>prepare>pregameflow>shaperselect>load>done>entergame; "
                       "status 0x10581; all loaded (C0 04) yes; shaper select phase 4; picks 0=0x5da8b09e/0x5da8b09e r0; "
                       "injected 1/1; client loaded (0x401) yes; client sent: BattlegroundsPreGameMsgs x3; DemoModeMsgs x1; "
                       "FriendInfo x1; HelloReq x1; PlayerStatusUpdate x3; ShaperSelectMsgs x4;"));
}

GMS_TEST(SessionShaperSelectWithoutTheMatchsSetup) {
  // --shaper-select against a match that sets up none (IMatch's default): the session falls back to script A
  LogCapture log;
  gms::SessionOptions so;
  so.shaperSelect = true;
  so.stepMs = 50;
  RecordingMatch match;
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return !client.received.empty() && client.received.back()[0] == gms::kGmsEnterGame; },
                     8000));
  CheckSequence(client.Sequence(), {"84", "8200007f000001273a", "a9003301011000000001ab4a04ef0b94357700000000",
                                    "b50000000002000000fe416d94010000000000000000", "a900131000010000000000",
                                    "a900139000010000000000", "b6", "a90013910401000000803f", "c003", "b700"});
  CHECK(log.Has("the match sets up no shaper select; the session skips it (status 0x10)"));
  CHECK(rig.session->Shapers() == nullptr);
}

namespace {

// Runs gms_script.exe (next to gms_tests.exe) with `args`, its output into a temporary file.
struct ScriptProcess {
  PROCESS_INFORMATION pi{};
  std::string outPath;
  bool started = false;

  bool Start(const std::string& args) {
    char exe[MAX_PATH];
    const DWORD n = GetModuleFileNameA(nullptr, exe, MAX_PATH);
    std::string dir(exe, n);
    dir.resize(dir.find_last_of("\\/") + 1);
    char tmp[MAX_PATH];
    GetTempPathA(MAX_PATH, tmp);
    outPath = std::string(tmp) + "gms_tests_script_" + std::to_string(GetCurrentProcessId()) + ".out";
    SECURITY_ATTRIBUTES sa{};
    sa.nLength = sizeof sa;
    sa.bInheritHandle = TRUE;
    HANDLE out = CreateFileA(outPath.c_str(), GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, &sa,
                             CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (out == INVALID_HANDLE_VALUE) return false;
    STARTUPINFOA si{};
    si.cb = sizeof si;
    si.dwFlags = STARTF_USESTDHANDLES;
    si.hStdOutput = si.hStdError = out;
    std::string cmd = "\"" + dir + "gms_script.exe\" " + args;
    started = CreateProcessA(nullptr, &cmd[0], nullptr, nullptr, TRUE, CREATE_NO_WINDOW, nullptr, nullptr, &si, &pi) != 0;
    CloseHandle(out);
    if (!started) std::printf("  cannot start %s (error %lu)\n", cmd.c_str(), GetLastError());
    return started;
  }
  std::string Output() const {
    std::ifstream in(outPath, std::ios::binary);
    std::stringstream s;
    s << in.rdbuf();
    return s.str();
  }
  ~ScriptProcess() {
    if (started) {
      TerminateProcess(pi.hProcess, 0);
      WaitForSingleObject(pi.hProcess, 5000);
      CloseHandle(pi.hProcess);
      CloseHandle(pi.hThread);
    }
    if (!outPath.empty()) DeleteFileA(outPath.c_str());
  }
};

}  // namespace

GMS_TEST(GmsScriptExeShaperSelectOptions) {
  // gms_script.exe itself with D3-core's options (an ephemeral port on 127.0.0.1, the test client as the live client):
  // L3's ally plus --skins and short timers; the client clicks nothing
  ScriptProcess p;
  CHECK(p.Start("--port 0 --seconds 30 --step-ms 50 --shaper-select --shaper-list artificer.Noun:1,alpha.Noun:1 "
                "--skins artificer:artificer_b.Noun --shaper-timers 300,300,200,16 --ally 1:1:alpha.Noun:100:200:2"));
  if (!p.started) return;
  // the port from "listening on 127.0.0.1:<port>"
  uint16_t port = 0;
  for (int i = 0; i < 500 && !port; ++i) {
    const std::string out = p.Output();
    const size_t at = out.find("listening on 127.0.0.1:");
    if (at != std::string::npos) port = static_cast<uint16_t>(std::atoi(out.c_str() + at + 23));
    if (!port) std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  CHECK(port != 0);
  if (!port) return;
  TestClient tc;
  CHECK(tc.Start(port));
  LiveClientModel client;
  client.shaperSelect = true;
  client.send = [&tc](const Bytes& m) { tc.Send(m); };
  bool entered = false;
  const auto end = gms::SteadyClock::now() + std::chrono::seconds(10);
  while (gms::SteadyClock::now() < end && !entered) {
    tc.Poll();
    while (!tc.Inbox().empty()) {
      const Bytes m = tc.Inbox().front();
      tc.Inbox().pop_front();
      client.On(m);
      entered = entered || m[0] == gms::kGmsEnterGame;
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  CHECK(entered);
  tc.Disconnect();  // the session prints the connection's last state line at once
  std::string out;
  const std::string want =
      "steps sent connected>hello>prepare>pregameflow>shaperselect>load>done>entergame; status 0x10581; "
      "all loaded (C0 04) yes; shaper select phase 4; picks 0=0x5da8b09e/0x5da8b09e r0 1=0xef044aab/0xef044aab r2;";
  for (int i = 0; i < 500; ++i) {
    out = p.Output();
    if (out.find(want) != std::string::npos) break;
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  CHECK(out.find(want) != std::string::npos);
  CHECK(out.find("shaper select options: on; list 0x5da8b09e:0x1:0x0 0xef044aab:0x1:0x0; skins for 1 shaper(s); "
                 "timers 300,300,200,16 ms; allies 1:1:0xef044aab:100:200:2") != std::string::npos);
  CHECK(out.find("shaper select set up: timers 300/300/200/16 ms, 1 configured player(s), 2 list entries") !=
        std::string::npos);
  // the human's skins: artificer first, then its other owned skin
  CHECK(out.find("-> c8 ShaperSelectMsgs (13 B) c80b029eb0a85d00") != std::string::npos);
  // HelloPlayer with the ally, and the ally loaded at C0 01
  CHECK(out.find("-> 82 HelloPlayer (17 B) 820000") != std::string::npos);
  CHECK(out.find("player 1 (no connection) loaded: status 0x10500, progress 1.000") != std::string::npos);
  if (out.find(want) == std::string::npos) std::printf("  --- gms_script output ---\n%s\n", out.c_str());
}

// ---- G3a: several humans: the rejoin map, departures, late C8 02s, the players a client has ---------------------------

GMS_TEST(RejoinMapKeepsAReturningUsersSlot) {
  CHECK_HEX(gms::BuildPlayerJoined(3), "8603");
  CHECK_HEX(gms::BuildPlayerDeparted(3), "8703");
  gms::RejoinMap m;
  gms::PlayerSlot s0, s1, other, earlier;
  s0.gameplayIndex = 0;
  s1.gameplayIndex = 6;
  s1.team = gms::kTeamRed;
  other.gameplayIndex = 9;
  other.noun = 0x5678;
  CHECK(!m.Earlier(11, &earlier));
  gms::RejoinMap::Admission a = m.Admit(11, 101, s0);
  CHECK(!a.rejoin && a.previous == 0 && a.slot.gameplayIndex == 0);
  a = m.Admit(12, 102, s1);
  CHECK(!a.rejoin && a.slot.gameplayIndex == 6 && a.slot.team == gms::kTeamRed);
  CHECK(m.Earlier(12, &earlier) && earlier.gameplayIndex == 6 && earlier.team == gms::kTeamRed);
  // user 12 is back on 202 after 102 ended, and the match proposes another slot: the index and team stay, the noun is
  // the match's
  m.OnDisconnected(102);
  CHECK(!m.ByUser(12)->connected);
  a = m.Admit(12, 202, other);
  CHECK(a.rejoin && a.previous == 102 && !a.previousConnected);
  CHECK(a.slot.gameplayIndex == 6 && a.slot.team == gms::kTeamRed && a.slot.noun == 0x5678);
  // and again before 202's end was noticed: the previous connection is still up (the session supersedes it)
  a = m.Admit(12, 302, other);
  CHECK(a.rejoin && a.previous == 202 && a.previousConnected);
  m.OnDisconnected(202);  // the replaced connection's end doesn't touch the seat
  CHECK(m.ByUser(12)->conn == 302 && m.ByUser(12)->connected);
  // user id 0 is never mapped
  a = m.Admit(0, 400, other);
  a = m.Admit(0, 401, s0);
  CHECK(!a.rejoin && a.slot.gameplayIndex == 0);
  CHECK_EQ(m.Size(), size_t{2});
}

namespace {

// Several clients against one session without RakNet: what the session sends waits in a queue per connection, and
// each client model answers through the session's OnMessage at once. A connection "ends" when the test says so.
struct MultiNet final : gms::ITransport {
  std::map<gms::ConnId, std::deque<Bytes>> inbox;
  bool Send(gms::ConnId conn, const Bytes& msg) override {
    inbox[conn].push_back(msg);
    return true;
  }
};

const uint64_t kUserA = 2000000011ull, kUserB = 2000000012ull;

// createGame's roster by user id and shaper select for the humans. At each HelloPlayer it plays G3's part: a
// PlayerJoined for every other seated human, both ways (GmsSession::SendPlayerJoined keeps the bookkeeping and sends
// nothing for a player a client has already).
struct RosterMatch final : gms::IMatch {
  gms::GmsSession* session = nullptr;
  std::map<uint64_t, gms::PlayerSlot> roster;
  gms::ShaperSelectConfig config;
  std::map<gms::ConnId, uint8_t> seated;
  std::vector<std::string> rejoins;  // "<index>: <previous> -> <conn>"
  std::vector<gms::ShaperPick> picks;
  int picksCalls = 0;
  // G3: the session names the humans itself (SessionOptions::nameHumans); joinOnHello = G3a's way (the match sends
  // PlayerJoined at each HelloPlayer), off in the G3 tests. `expected` = IMatch::ExpectedHumans; `setup` = Setup().
  bool joinOnHello = true;
  std::vector<uint64_t> expected;
  gms::GameSetup setup;
  std::vector<uint64_t> ExpectedHumans() override { return expected; }
  // B15: a custom game's lobby: every HelloReq held while `lobby` (IMatch::HoldHello)
  bool lobby = false;
  bool HoldHello(gms::ConnId, const gms::HelloReq&) override { return lobby; }
  // G3b: OnGameAborted's calls
  std::vector<std::vector<uint64_t>> aborted;
  std::string abortWhy;
  void OnGameAborted(const std::vector<uint64_t>& culprits, const std::string& why) override {
    aborted.push_back(culprits);
    abortWhy = why;
  }

  RosterMatch() {
    gms::PlayerSlot a, b;
    a.gameplayIndex = 0;
    b.gameplayIndex = 1;
    roster[kUserA] = a;
    roster[kUserB] = b;
  }
  gms::GameSetup Setup() override { return setup; }
  bool OnHelloReq(gms::ConnId conn, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    auto it = roster.find(req.userId);
    if (it == roster.end()) return false;
    *slot = it->second;
    seated[conn] = it->second.gameplayIndex;
    return true;
  }
  void OnRejoin(gms::ConnId conn, gms::ConnId previous, const gms::PlayerSlot& slot) override {
    rejoins.push_back(std::to_string(slot.gameplayIndex) + ": " + std::to_string(previous) + " -> " +
                      std::to_string(conn));
    seated.erase(previous);
    seated[conn] = slot.gameplayIndex;
  }
  void OnPoint(gms::ConnId conn, gms::SessionPoint p, gms::ISender&) override {
    if (p != gms::SessionPoint::kHello || !session || !joinOnHello) return;
    const uint8_t me = seated[conn];
    for (const auto& kv : seated) {
      if (kv.first == conn) continue;
      session->SendPlayerJoined(conn, kv.second);
      session->SendPlayerJoined(kv.first, me);
    }
  }
  void OnDisconnected(gms::ConnId conn) override { seated.erase(conn); }
  bool ShaperSelectSetup(gms::ShaperSelectConfig* c) override {
    *c = config;
    return true;
  }
  void OnShaperPicks(const std::vector<gms::ShaperPick>& p) override {
    picks = p;
    ++picksCalls;
  }
  const gms::ShaperPick* Pick(uint8_t index) const {
    for (const gms::ShaperPick& k : picks)
      if (k.gameplayIndex == index) return &k;
    return nullptr;
  }
};

// Two humans on team 1 (index 0 and 1, configured), alpha, artificer and gunman owned, phases of `ms` each.
gms::ShaperSelectConfig TwoHumans(uint32_t ms) {
  gms::ShaperSelectConfig cfg;
  cfg.timers.pickMs = cfg.timers.rolePerkMs = cfg.timers.skinMs = ms;
  cfg.timers.revealMs = 16;
  for (const char* name : {"alpha.Noun", "artificer.Noun", "gunman.Noun"}) {
    gms::ShaperListEntry e;
    e.noun = gms::AssetIdOf(name);
    e.flags = gms::kShaperOwned;
    cfg.humanDefault.list.push_back(e);
  }
  for (uint8_t i = 0; i < 2; ++i) {
    gms::ShaperSelectPlayer p;
    p.gameplayIndex = i;
    p.content = cfg.humanDefault;  // a configured human's own content (humanDefault is for unconfigured indices)
    cfg.players.push_back(p);
  }
  return cfg;
}

struct Human {
  gms::ConnId conn = 0;
  LiveClientModel model;
  bool up = false;  // the transport delivers to it
  // G3: the other humans' status changes may follow EnterGame, so "entered" = got EnterGame
  bool Entered() const {
    for (const Bytes& m : model.received)
      if (m[0] == gms::kGmsEnterGame) return true;
    return false;
  }
  bool Got(const Bytes& m) const { return std::find(model.received.begin(), model.received.end(), m) != model.received.end(); }
  // the position of the first received message starting with `prefix` (hex), or -1
  int First(const std::string& prefix) const {
    for (size_t i = 0; i < model.received.size(); ++i)
      if (gms::Hex(model.received[i], static_cast<size_t>(-1)).compare(0, prefix.size(), prefix) == 0)
        return static_cast<int>(i);
    return -1;
  }
};

struct MultiRig {
  MultiNet net;
  RosterMatch match;
  std::unique_ptr<gms::GmsSession> session;
  std::vector<std::unique_ptr<Human>> clients;
  gms::ConnId next = 1000;

  bool shaperSelect = true;
  // G3: `g3` = the session names the humans itself (the match doesn't), and without shaper select the clients skip it
  // (status 0x10, script A's path, the practice and PvP default); `tweak` changes the session options.
  explicit MultiRig(uint32_t phaseMs, bool g3 = false, bool withShaperSelect = true,
                    const std::function<void(gms::SessionOptions&)>& tweak = nullptr) {
    match.config = TwoHumans(phaseMs);
    match.joinOnHello = !g3;
    shaperSelect = withShaperSelect;
    gms::SessionOptions so;
    so.shaperSelect = withShaperSelect;
    so.skipShaper = !withShaperSelect;
    so.echoStatus = so.playerUpdate = true;
    so.stepMs = 10;
    so.loadTimeoutMs = 250;  // before G3 the other human's 0x400 never reached a client (the echo was its own only)
    if (tweak) tweak(so);
    session.reset(new gms::GmsSession(net, match, so, gms::SteadyClock::now()));
    match.session = session.get();
  }
  Human& Connect(uint64_t user) {
    clients.emplace_back(new Human);
    Human& h = *clients.back();
    h.conn = ++next;
    h.model.userId = user;
    h.model.shaperSelect = shaperSelect;
    gms::GmsSession* s = session.get();
    const gms::ConnId id = h.conn;
    h.model.send = [s, id](const Bytes& m) { s->OnMessage(id, m.data(), m.size()); };
    h.up = true;
    session->OnConnected(id, "fake|" + std::to_string(id));
    return h;
  }
  void End(Human& h) {  // the connection ends (the transport noticed it)
    h.up = false;
    session->OnDisconnected(h.conn);
    net.inbox.erase(h.conn);
  }
  void Step() {
    session->Tick();
    for (auto& hp : clients) {
      Human& h = *hp;
      if (!h.up) continue;
      std::deque<Bytes>& q = net.inbox[h.conn];
      while (!q.empty()) {
        const Bytes m = q.front();
        q.pop_front();
        h.model.On(m);
      }
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(1));
  }
  bool RunUntil(const std::function<bool()>& done, int timeoutMs) {
    const auto end = gms::SteadyClock::now() + std::chrono::milliseconds(timeoutMs);
    while (gms::SteadyClock::now() < end) {
      Step();
      if (done()) return true;
    }
    return false;
  }
  int Phase() const { return session->Shapers() ? session->Shapers()->Phase() : 0; }
};

}  // namespace

// Q2's first hole: a human whose connection ended got status 0x100 alone at the reveal, so the other clients' 0x400
// gate waited for it. Now it keeps its last status plus 0x4: the other client's gates leave it out (no load timeout).
// A configured human who never connected gets 0x104, and no record or update of it reaches a client without it.
GMS_TEST(SessionShaperSelectDepartedHumanAtTheReveal) {
  LogCapture log;
  MultiRig rig(300);
  gms::ShaperSelectPlayer absent;  // index 2, team 1: configured, never connects
  absent.gameplayIndex = 2;
  absent.content = rig.match.config.humanDefault;
  rig.match.config.players.push_back(absent);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return rig.Phase() == 2 && b.model.inShaperSelect; }, 3000));
  CHECK(a.model.Has(1) && b.model.Has(0));  // G3's PlayerJoined, both ways, before their B6
  CHECK(a.model.recordsKnown == std::set<uint8_t>({0, 1}));
  rig.End(b);  // in phase 2, for good: B had reported 0x10000 and 0x80
  CHECK(rig.RunUntil([&] { return a.Entered(); }, 5000));
  CHECK_EQ(a.model.statuses[1], 0x10080u | gms::kStatusGone | gms::kStatusShaperSelectDone);
  CHECK(a.model.picksAckSent && a.model.allLoadedSent);  // both gates passed without B
  CHECK(!log.Has("no C0 04 from the client"));
  CHECK(log.Has("player 1's connection ended: status 0x10084 kept for the pre-game gates"));
  // the match's picks: B's shaper from the phase-1 timer (artificer: alpha went to A), its status with 0x4
  CHECK_EQ(rig.match.picksCalls, 1);
  const gms::ShaperPick* pb = rig.match.Pick(1);
  CHECK(pb && pb->status == 0x10184u && pb->shaper == gms::AssetIdOf("artificer.Noun"));
  const gms::ShaperPick* p2 = rig.match.Pick(2);
  CHECK(p2 && p2->status == (gms::kStatusGone | gms::kStatusShaperSelectDone) && p2->shaper == gms::AssetIdOf("gunman.Noun"));
  // A never had player 2: no record (the client would assert) and no update of it
  CHECK_EQ(a.model.unknownRecords, 0);
  CHECK_EQ(a.model.unknownUpdates, 0);
  CHECK(!rig.session->Knows(a.conn, 2));
  CHECK_EQ(a.model.lockAnswersNot01, 0);
}

// D3-perks: a human's perk pages from its content: C8 09 lists them at its C8 02, its C8 0A (sent by the client's
// LockInPerksAndRoles before C8 08) becomes the pick's page, the reveal's update carries mLoadoutID/Version (members
// 13/14), and the pre-game loadout C0 02 with the page's stats follows it to every client that has the player, before
// C0 01. A page the human doesn't have is refused; a human without pages gets no C8 09 and sends no C8 0A.
GMS_TEST(SessionShaperSelectPerkPage) {
  LogCapture log;
  MultiRig rig(300);
  gms::PerkPage p1, p4;
  p1.id = 1;
  p1.version = 3;
  p1.attributes = {gms::LoadoutAttribute{0, 1, 0.8f}, gms::LoadoutAttribute{0, 2, 0.05f}, gms::LoadoutAttribute{2, 1, 50.f}};
  p1.modifiers = {gms::AssetIdOf("modifier_MediumPerk_BushSpeed")};
  p4.id = 4;
  p4.version = 1;
  rig.match.config.players[0].content.pages = {p1, p4};
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  a.model.perkPage = 1;
  b.model.perkPage = 9;  // not one of its pages (it has none): refused
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 8000));
  CHECK(a.model.pagesListed == (std::vector<uint32_t>{1, 4}));
  CHECK(a.First("c8090201000000") >= 0 && b.First("c809") < 0);
  CHECK(log.Has("not one of its pages"));
  const gms::ShaperPick* pa = rig.match.Pick(0);
  const gms::ShaperPick* pb = rig.match.Pick(1);
  CHECK(pa && pa->page == 1 && pa->pageVersion == 3 && pa->loadout.size() == 3 && pa->modifiers == p1.modifiers);
  CHECK(pb && pb->page == 0 && pb->loadout.empty());
  if (!pa) return;
  // the reveal's update of player 0 with members 0, 9, 13, 14, 16 (status, role, mLoadoutID 1, mLoadoutVersion 3, noun)
  const std::vector<int> members = gms::RevealMembers(*pa);
  CHECK(members == (std::vector<int>{gms::kPlayerStatus, gms::kPlayerRole, gms::kPlayerLoadoutId,
                                     gms::kPlayerLoadoutVersion, gms::kPlayerNounDef}));
  const Bytes loadout = gms::BuildPreGameLoadout(0, p1.attributes, p1.modifiers);
  CHECK_HEX(loadout, "c00200" "03" "0001cdcc4c3f" "0002cdcc4c3d" "020100004842" "01" + gms::Hex(
                         Bytes{static_cast<uint8_t>(p1.modifiers[0]), static_cast<uint8_t>(p1.modifiers[0] >> 8),
                               static_cast<uint8_t>(p1.modifiers[0] >> 16), static_cast<uint8_t>(p1.modifiers[0] >> 24)},
                         static_cast<size_t>(-1)));
  CHECK(gms::RevealLoadout(*pa) == loadout && gms::RevealLoadout(*pb).empty());
  for (Human* h : {&a, &b}) {
    const std::string hex = gms::Hex(loadout, static_cast<size_t>(-1));
    const int at = h->First(hex), load = h->First("c001");
    // the reveal's update of player 0: mLoadoutID 1, mLoadoutVersion 3
    int reveal = -1;
    for (size_t i = 0; i < h->model.received.size() && reveal < 0; ++i) {
      const Bytes& m = h->model.received[i];
      uint8_t index = 0;
      std::vector<int> got;
      gms::PlayerGlobalValues v;
      if (m[0] == gms::kGmsLabsPlayerGlobalUpdate && gms::ParsePlayerGlobalUpdate(m.data(), m.size(), &index, &got, &v) &&
          index == 0 && v.loadoutId == 1 && v.loadoutVersion == 3)
        reveal = static_cast<int>(i);
    }
    CHECK(at >= 0 && reveal >= 0 && reveal < at && at < load);  // after player 0's reveal update, before C0 01
    CHECK(h->First("c00201") < 0);                                // no loadout for player 1 (no page)
  }
  CHECK(log.Has("pre-game loadout: player 0 perk page 1 (3 attribute(s), 1 modifier(s))"));
  CHECK_EQ(a.model.lockAnswersNot01, 0);
}

// Q2's last hole (the session side) and the second: a human leaves and comes back on a new connection in each phase;
// the session keeps its slot (RejoinMap), the match hears of it (OnRejoin), shaper select continues it (its skins
// before the phase once its shaper is locked), and after the reveal (phase 4) its late C8 02 still gets the reveal's
// updates and C0 01. A human leaving after the reveal gets 0x4 at once at the other client.
GMS_TEST(SessionShaperSelectRejoinInEachPhase) {
  const uint32_t kArtificer = gms::AssetIdOf("artificer.Noun");
  for (int p = 1; p <= 4; ++p) {
    std::printf("  phase %d\n", p);
    LogCapture log;
    MultiRig rig(300);
    Human& a = rig.Connect(kUserA);
    Human& b = rig.Connect(kUserB);
    CHECK(rig.RunUntil([&] { return rig.Phase() == p && b.model.inShaperSelect; }, 3000));
    const gms::ConnId old = b.conn;
    rig.End(b);
    rig.Step();
    if (p == 4) {  // after the reveal: A already has B's status, and gets the 0x4 at once
      CHECK(a.model.statuses[1] & gms::kStatusGone);
      CHECK(!b.Got(gms::BuildPreGame(gms::kPreGameLoad)));  // B left between the reveal and its C0 01
    }
    Human& b2 = rig.Connect(kUserB);  // the same user on a new connection
    CHECK(rig.RunUntil([&] { return b2.model.inShaperSelect && !b2.model.phases.empty(); }, 2000));
    CHECK(!b2.model.phases.empty() && b2.model.phases.front().first == p);  // its C8 02 came in phase p
    CHECK(rig.RunUntil([&] { return a.Entered() && b2.Entered(); }, 5000));
    CHECK_EQ(rig.match.rejoins.size(), size_t{1});
    if (!rig.match.rejoins.empty())
      CHECK_EQ(rig.match.rejoins[0], "1: " + std::to_string(old) + " -> " + std::to_string(b2.conn));
    CHECK(log.Has("user 2000000012 rejoins as player 1 (team 1); its earlier connection had ended"));
    CHECK_EQ(b2.model.myIndex, 1);
    CHECK(b2.model.Has(0) && b2.model.recordsKnown == std::set<uint8_t>({0, 1}));
    // nothing of shaper select before its own (B6, C8 02), nothing after its C0 01
    CHECK_EQ(b2.model.shaperAfterLoad, 0);
    // a shaper locked before it came back: its skins before its first phase message
    const int skins = b2.First("c80b"), phase = b2.First("c801");
    if (p >= 2) CHECK(skins >= 0 && skins < phase);
    // the reveal's updates (members 0, 9, 16) and C0 01 reached the new connection, also after the reveal
    CHECK(b2.First("a90131") >= 0);
    CHECK(b2.Got(gms::BuildPreGame(gms::kPreGameLoad)));
    for (Human* h : {&a, &b2}) {
      CHECK_EQ(h->model.unknownRecords, 0);
      CHECK_EQ(h->model.unknownUpdates, 0);
      CHECK_EQ(h->model.lockAnswersNot01, 0);
    }
    const gms::ShaperPick* pb = rig.match.Pick(1);
    CHECK(pb && pb->shaper == kArtificer && pb->skin == kArtificer);
    // back before the reveal: no 0x4 in its pick; gone just after it (p = 4): it had 0x100 without 0x4 then
    if (pb) CHECK(!(pb->status & gms::kStatusGone));
    CHECK_EQ(rig.match.picksCalls, 1);
    const std::string steps = "steps sent connected>hello>prepare>pregameflow>shaperselect>load>done>entergame;";
    CHECK_EQ(rig.session->State(b2.conn).compare(0, steps.size(), steps), 0);
    if (p < 4) CHECK(!(a.model.statuses[1] & gms::kStatusGone));
  }
}

// WARDSKIN: the vanity picks reach the pick (IMatch::OnShaperPicks, which writes them into the server player) and the
// client's local player (the C8 0E echo); a human who comes back after its pick gets them on its new client by
// OnLocalPlayerReconnect (0x8A, before C0 01), a first connection doesn't (it had the echoes). An unowned ward is refused.
GMS_TEST(SessionShaperSelectVanityAndItsRejoin) {
  const uint32_t kEast = gms::AssetIdOf("wardskin_east.Noun"), kToad = gms::AssetIdOf("wardskin_toad.Noun");
  const uint32_t kFenmore = gms::AssetIdOf("PC_Alpha.AnnouncerPack");
  CHECK_HEX(gms::BuildLocalPlayerReconnect(0x11223344u, 0), "8a4433221100000000");
  for (int p = 0; p <= 1; ++p) {  // 0: nobody leaves; 1: B leaves after the reveal and comes back
    std::printf("  %s\n", p ? "rejoin after the reveal" : "no rejoin");
    LogCapture log;
    MultiRig rig(300);
    rig.match.config.players[1].content.wards = {gms::AssetIdOf("wardskin_default.Noun"), kEast};
    Human& a = rig.Connect(kUserA);
    Human& b = rig.Connect(kUserB);
    a.model.ward = kToad;  // A has no ownership data: any ward is taken
    b.model.ward = kEast;
    b.model.announcer = kFenmore;
    CHECK(rig.RunUntil([&] { return rig.Phase() == 4; }, 5000));
    CHECK_EQ(a.model.myVanity[0], kToad);
    CHECK(b.model.myVanity[0] == kEast && b.model.myVanity[1] == kFenmore);
    Human* bb = &b;
    if (p) {
      const bool loaded = b.Got(gms::BuildPreGame(gms::kPreGameLoad));
      rig.End(b);
      rig.Step();
      Human& b2 = rig.Connect(kUserB);
      bb = &b2;
      if (loaded) std::printf("  (B had C0 01 before it left)\n");
    }
    CHECK(rig.RunUntil([&] { return a.Entered() && bb->Entered(); }, 6000));
    const gms::ShaperPick* pa = rig.match.Pick(0);
    const gms::ShaperPick* pb = rig.match.Pick(1);
    CHECK(pa && pa->ward == kToad && pa->announcer == 0);
    CHECK(pb && pb->ward == kEast && pb->announcer == kFenmore);
    CHECK_EQ(a.model.reconnects, 0);
    const std::string want = gms::Hex(gms::BuildLocalPlayerReconnect(kEast, kFenmore), static_cast<size_t>(-1));
    if (p) {
      const int at = bb->First(want), load = bb->First("c001");
      CHECK(at >= 0 && load >= 0 && at < load);  // before C0 01 (TransitionToLoading preloads them)
      CHECK_EQ(bb->model.reconnects, 1);
      CHECK(bb->model.myVanity[0] == kEast && bb->model.myVanity[1] == kFenmore);
      CHECK(log.Has("local player reconnect: player 1 ward"));
    } else {
      CHECK_EQ(b.model.reconnects, 0);
    }
  }
  // an unowned ward is refused and not echoed: the pick keeps the default
  LogCapture log;
  MultiRig rig(300);
  rig.match.config.players[0].content.wards = {kEast};
  Human& a = rig.Connect(kUserA);
  rig.Connect(kUserB);
  a.model.ward = kToad;
  CHECK(rig.RunUntil([&] { return rig.Phase() == 4; }, 5000));
  CHECK_EQ(a.model.myVanity[0], 0u);
  const gms::ShaperPick* pa = rig.match.Pick(0);
  CHECK(pa && pa->ward == 0);
  CHECK(log.Has("wrong phase or not owned"));
}

// Q2's third hole at the session: a PlayerJoined after a client's B6 gives it the player but no shaper select record,
// so the records of that player (its picks, the timers' locks: same team) don't go to it; the reveal's update does.
GMS_TEST(SessionShaperSelectRecordsOnlyForPlayersAtB6) {
  LogCapture log;
  MultiRig rig(200);
  Human& a = rig.Connect(kUserA);
  CHECK(rig.RunUntil([&] { return a.model.inShaperSelect; }, 2000));
  Human& b = rig.Connect(kUserB);  // after A's B6
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  CHECK(rig.session->Knows(a.conn, 1) && a.model.Has(1));
  CHECK(a.model.recordsKnown == std::set<uint8_t>({0}));
  CHECK(log.Has("player 1 joined (after B6: its shaper select has no record for it)"));
  CHECK_EQ(a.model.unknownRecords, 0);
  CHECK(a.model.statuses[1] & gms::kStatusShaperSelectDone);  // the reveal's update of B
  // B was named before its B6: it has A's records
  CHECK(b.model.recordsKnown == std::set<uint8_t>({0, 1}));
  CHECK_EQ(b.model.unknownRecords, 0);
  CHECK(b.model.records.count(0) == 1 && (b.model.records[0].flags & gms::kRecordShaperLocked));
}

// A rejoin before the old connection's loss was noticed: the old connection is superseded (nothing more to it, its
// messages dropped), and its end, when it comes, is no departure (no 0x4 for the player, shaper select keeps the new).
GMS_TEST(SessionRejoinSupersedesALiveConnection) {
  LogCapture log;
  MultiRig rig(300);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return rig.Phase() == 2 && b.model.inShaperSelect; }, 3000));
  Human& b2 = rig.Connect(kUserB);  // b is still up
  CHECK(rig.RunUntil([&] { return b2.model.inShaperSelect && !b2.model.phases.empty(); }, 2000));
  const size_t oldGot = b.model.received.size();
  CHECK(rig.session->State(b.conn).find("; superseded by its user's new connection") != std::string::npos);
  CHECK(log.Has("its earlier connection (fake|" + std::to_string(b.conn) + ") is superseded"));
  // the old client's role lock is dropped; the new one's counts
  b.model.SendShaper(gms::kShaperLockRole, 0, 3);
  rig.Step();
  CHECK(!(rig.session->Shapers()->Record(1)->flags & gms::kRecordRoleLocked));
  rig.End(b);  // now its loss is noticed
  CHECK(rig.RunUntil([&] { return a.Entered() && b2.Entered(); }, 5000));
  CHECK_EQ(b.model.received.size(), oldGot);  // nothing went to it after the rejoin
  CHECK(!log.Has("player 1's connection ended"));
  CHECK(!(a.model.statuses[1] & gms::kStatusGone));
  CHECK(rig.session->Shapers()->HasHuman(b2.conn) && !rig.session->Shapers()->HasHuman(b.conn));
  CHECK_EQ(rig.match.rejoins.size(), size_t{1});
  CHECK_EQ(rig.session->NumConnections(), size_t{2});
}

// ---- G3: several humans live: who names whom, every status to every client, the pre-game gate ----------------------

namespace {
const uint64_t kUserC = 2000000013ull;
Bytes StatusMsg(uint32_t flags) { return LiveClientModel::Status(flags, 0.0f); }
int CountOf(const Human& h, const Bytes& m) {
  return static_cast<int>(std::count(h.model.received.begin(), h.model.received.end(), m));
}
}  // namespace

// Two humans without shaper select (status 0x10, the practice and PvP default), the session naming them (the match
// sends nothing): the newcomer's HelloPlayer names the other human (type 1) and it gets that human's update; the first
// client gets PlayerJoined and the newcomer's update; every status echo reaches both clients, so each client's 0x400
// gate passes with the other's 0x401 (no load timeout).
GMS_TEST(SessionNamesEveryHumanAndSendsEveryStatus) {
  LogCapture log;
  MultiRig rig(300, true, false);
  Human& a = rig.Connect(kUserA);
  rig.Step();
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  CHECK(!log.Has("no C0 04 from the client"));
  CHECK(a.model.allLoadedSent && b.model.allLoadedSent);
  // B's HelloPlayer: itself (type 0, index 1) and A (type 1, index 0); A gets PlayerJoined 1, once
  CHECK(b.Got(FromHex("8200017f000001273a01007f000001273a")));
  CHECK_EQ(CountOf(a, gms::BuildPlayerJoined(1)), 1);
  CHECK(a.model.Has(1) && b.model.Has(0));
  // B's first update to A: status 0x10, progress 0, team 1, alpha, online id; A's to B: no status yet was 0x10 too
  CHECK(a.Got(FromHex("a901330103100000000000000001ab4a04ef0c94357700000000")));
  CHECK(log.Has("player update (another human): player 0, status 0x10"));
  // each client has the other's whole status: 0x10 (skip), 0x10000, 0x80, 0x401
  CHECK_EQ(a.model.statuses[1], 0x10491u);
  CHECK_EQ(b.model.statuses[0], 0x10491u);
  CHECK_EQ(a.model.unknownUpdates, 0);
  CHECK_EQ(b.model.unknownUpdates, 0);
  const std::string steps = "steps sent connected>hello>prepare>pregameflow>load(implicit)>done>entergame;";
  CHECK_EQ(rig.session->State(a.conn).compare(0, steps.size(), steps), 0);
  CHECK_EQ(rig.session->State(b.conn).compare(0, steps.size(), steps), 0);
}

// B15: a custom game's lobby. While the match holds HelloReqs (IMatch::HoldHello: the host until dgserver's startGame)
// neither client gets HelloPlayer, whatever the time; meanwhile the lobby moves B to team 2 (index 5). Once the match
// starts, both held HelloReqs are answered with the final slots, the humans name each other, and both enter the game.
GMS_TEST(SessionCustomLobbyHoldsHelloUntilTheStart) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.lobby = true;
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  CHECK(!rig.RunUntil([&] { return a.First("82") >= 0 || b.First("82") >= 0; }, 300));
  CHECK(log.Has("HelloReq from user 2000000011 held: the match waits in its lobby"));
  CHECK(rig.session->State(b.conn).find("HelloReq held (the match waits in its lobby)") != std::string::npos);
  gms::PlayerSlot moved;  // the lobby's final roster: B on team 2 at index 5
  moved.gameplayIndex = 5;
  moved.team = 2;
  rig.match.roster[kUserB] = moved;
  rig.match.lobby = false;
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  CHECK(log.Has("the match left its lobby: user 2000000012's held HelloReq answered"));
  CHECK(b.model.myIndex == 5);
  CHECK(a.model.Has(5) && b.model.Has(0));
  CHECK_EQ(a.model.unknownUpdates, 0);
  CHECK_EQ(b.model.unknownUpdates, 0);
  CHECK(rig.session->State(b.conn).find("held") == std::string::npos);
}

// The pre-game gate: with createGame's roster expected (IMatch::ExpectedHumans), A's B6 waits until B is there and
// ready, so both clients enter the pre-game with both players (and shaper select would create both records).
GMS_TEST(SessionPreGameWaitsForEveryExpectedHuman) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.expected = {kUserA, kUserB};
  const Bytes b6 = gms::BuildEnterPreGameFlow();
  Human& a = rig.Connect(kUserA);
  CHECK(!rig.RunUntil([&] { return a.Got(b6); }, 300));
  CHECK(log.Has("pre-game: player 0 is ready; EnterPreGameFlow waits for user 2000000012 (not connected)"));
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return a.Got(b6) && b.Got(b6); }, 2000));
  CHECK(log.Has("pre-game: every human is ready after"));
  CHECK(a.model.Has(1) && b.model.Has(0));  // both before their B6
  CHECK(a.First("8601") >= 0 && a.First("8601") < a.First("b6"));
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  CHECK(!log.Has("no C0 04 from the client"));
}

// The gate's timeout: a roster user who never connects holds the others up for waitPlayersMs only; a connection that
// comes after the first B6 waits for nobody absent.
GMS_TEST(SessionPreGameWaitTimesOut) {
  LogCapture log;
  MultiRig rig(300, true, false, [](gms::SessionOptions& so) { so.waitPlayersMs = 300; });
  rig.match.expected = {kUserA, kUserB, kUserC};
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  CHECK(log.Has("pre-game: still waiting for user 2000000013 (not connected) after 300 ms; going on without them"));
  CHECK(log.Has("pre-game: the wait for the other humans timed out after"));
}

// Connect type 1 (GamePrepareForStart flags bit 0: MatchFoundUI's accept prompt, B10b): B6 waits for every human's
// answer (0x10000); each accept reaches the other client (MatchFoundUI's count reads every player's status).
GMS_TEST(SessionPreGameWaitsForTheMatchFoundAccepts) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.setup.prepareFlags = 1;
  const Bytes b6 = gms::BuildEnterPreGameFlow();
  Human& a = rig.Connect(kUserA);
  a.model.autoAccept = false;
  Human& b = rig.Connect(kUserB);
  b.model.autoAccept = false;
  CHECK(!rig.RunUntil([&] { return a.Got(b6) || b.Got(b6); }, 300));
  CHECK(log.Has("no answer to the match-found prompt"));
  a.model.send(StatusMsg(gms::kStatusMatchAccepted));
  CHECK(!rig.RunUntil([&] { return a.Got(b6) || b.Got(b6); }, 200));
  CHECK(b.model.statuses[0] & gms::kStatusMatchAccepted);  // A's accept counted on B's prompt
  b.model.send(StatusMsg(gms::kStatusMatchAccepted));
  CHECK(rig.RunUntil([&] { return a.Got(b6) && b.Got(b6); }, 1000));
  CHECK(a.model.statuses[1] & gms::kStatusMatchAccepted);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
}

// G3b: a decline of the match-found prompt aborts the game before the pre-game: GameAborted (0x89, 92 bytes with the
// wire byte) to both clients, the decliner's id first (the range the client checks), then the other's; no B6; once
// both answered 0x40000 the match hears it once, with the decliner.
GMS_TEST(SessionMatchFoundDeclineAbortsTheGame) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.setup.prepareFlags = 1;
  rig.match.setup.blazeGameId = 7;
  const Bytes b6 = gms::BuildEnterPreGameFlow();
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  b.model.autoAccept = false;
  CHECK(rig.RunUntil([&] { return b.model.clocksSent >= 5; }, 2000));
  b.model.send(StatusMsg(gms::kStatusMatchDeclined));
  CHECK(rig.RunUntil([&] { return !rig.match.aborted.empty(); }, 2000));
  CHECK(!a.Got(b6) && !b.Got(b6));
  const Bytes want = gms::BuildGameAborted(7, {kUserB, kUserA}, 0, 1);
  CHECK_EQ(want.size(), size_t{92});
  CHECK(a.model.aborts.size() == 1 && a.model.aborts[0] == want);
  CHECK(b.model.aborts.size() == 1 && b.model.aborts[0] == want);
  CHECK(want[0] == 0x89 && want[1] == 7 && want[0x59] == 0 && want[0x5A] == 1 && want[0x5B] == 1);
  CHECK_EQ(rig.match.aborted.size(), size_t{1});
  CHECK(rig.match.aborted[0] == std::vector<uint64_t>{kUserB});
  CHECK(rig.match.abortWhy.find("declined by player 1 (user 2000000012)") != std::string::npos);
  CHECK(log.Has("the game is ABORTED; GameAborted to 2 connection(s)"));
  CHECK(log.Has("every client answered GameAborted (0x40000)"));
  CHECK(a.model.statuses[1] & gms::kStatusMatchDeclined);  // B's decline reached A's prompt (its count)
  for (int i = 0; i < 20; ++i) rig.Step();
  CHECK(!a.Got(b6) && !b.Got(b6));
  CHECK_EQ(rig.match.aborted.size(), size_t{1});  // once
}

// G3b: the prompt's timeout: a human that doesn't answer within matchFoundTimeoutMs aborts the game the same way; a
// client that doesn't answer GameAborted holds the end up for abortAckMs only. Without the prompt nothing aborts.
GMS_TEST(SessionMatchFoundTimeoutAbortsTheGame) {
  LogCapture log;
  MultiRig rig(300, true, false, [](gms::SessionOptions& so) {
    so.matchFoundTimeoutMs = 400;
    so.abortAckMs = 200;
  });
  rig.match.setup.prepareFlags = 1;
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  b.model.autoAccept = false;
  a.model.ackAbort = false;
  CHECK(rig.RunUntil([&] { return !rig.match.aborted.empty(); }, 3000));
  CHECK(rig.match.aborted[0] == std::vector<uint64_t>{kUserB});
  CHECK(rig.match.abortWhy.find("not answered within 400 ms by player 1") != std::string::npos);
  CHECK(log.Has("1 client(s) didn't answer GameAborted within 200 ms"));
  CHECK(a.model.aborts.size() == 1 && b.model.aborts.size() == 1);
  // the same without the prompt: both enter the game
  LogCapture log2;
  MultiRig plain(300, true, false, [](gms::SessionOptions& so) { so.matchFoundTimeoutMs = 100; });
  Human& c = plain.Connect(kUserA);
  Human& d = plain.Connect(kUserB);
  CHECK(plain.RunUntil([&] { return c.Entered() && d.Entered(); }, 5000));
  CHECK(plain.match.aborted.empty() && c.model.aborts.empty());
}

// A human leaves in the game: the other client gets its 0x4 at once (no PlayerDeparted: its player and shaper stay); a
// third human who joins meanwhile is named the departed one with its 0x4; the user comes back on a new connection:
// the others get no second PlayerJoined but its update and statuses, which replace the 0x4, and it gets theirs.
GMS_TEST(SessionDepartedHumanInGameAndItsRejoin) {
  LogCapture log;
  MultiRig rig(300, true, false);
  gms::PlayerSlot c;
  c.gameplayIndex = 2;
  c.team = gms::kTeamRed;
  rig.match.roster[kUserC] = c;
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered(); }, 5000));
  rig.End(b);
  rig.Step();
  CHECK_EQ(a.model.statuses[1], 0x10491u | gms::kStatusGone);
  CHECK(!a.Got(gms::BuildPlayerDeparted(1)));
  // a newcomer gets the departed human's record and status
  Human& h3 = rig.Connect(kUserC);
  CHECK(rig.RunUntil([&] { return h3.Entered(); }, 5000));
  CHECK(h3.model.Has(0) && h3.model.Has(1));
  CHECK_EQ(h3.model.statuses[1], 0x10491u | gms::kStatusGone);
  CHECK(log.Has("player update (a departed human): player 1, status 0x10495"));
  CHECK_EQ(a.model.statuses[2], 0x10491u);
  // B comes back
  Human& b2 = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return b2.Entered(); }, 5000));
  CHECK_EQ(rig.match.rejoins.size(), size_t{1});
  CHECK_EQ(b2.model.myIndex, 1);
  CHECK(b2.model.Has(0) && b2.model.Has(2));
  CHECK_EQ(CountOf(a, gms::BuildPlayerJoined(1)), 1);  // A had it all along
  CHECK_EQ(CountOf(h3, gms::BuildPlayerJoined(1)), 0);  // and so had the third
  CHECK_EQ(a.model.statuses[1], 0x10491u);  // the 0x4 replaced
  CHECK_EQ(h3.model.statuses[1], 0x10491u);
  CHECK_EQ(b2.model.statuses[0], 0x10491u);
  CHECK_EQ(b2.model.statuses[2], 0x10491u);
  for (Human* h : {&a, &h3, &b2}) CHECK_EQ(h->model.unknownUpdates, 0);
}

// ---- B15b: spectators (docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*) ----------------------------------

namespace {

const uint64_t kUserS = 2000000014ull, kUserT = 2000000015ull;

gms::PlayerSlot SpectatorSlot(uint8_t index) {
  gms::PlayerSlot s;
  s.gameplayIndex = index;
  s.team = gms::kSpectatorTeam;
  s.noun = gms::kNounAlpha;  // the session leaves a spectator's noun out whatever the match gives
  return s;
}

// A match that seats users from a roster and records every status the session hands it (the host stores it in its
// server player before seam 5 writes the player's update from there).
struct SpectatorMatch final : gms::IMatch {
  std::map<uint64_t, gms::PlayerSlot> roster;
  std::vector<uint32_t> statuses;
  gms::GameSetup Setup() override { return gms::GameSetup{}; }
  bool OnHelloReq(gms::ConnId, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    auto it = roster.find(req.userId);
    if (it == roster.end()) return false;
    *slot = it->second;
    return true;
  }
  void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
  void OnPlayerStatus(gms::ConnId, uint32_t s, float) override { statuses.push_back(s); }
};

// B6's position and the position of the first message starting with `prefix` (hex) after it, or -1.
int FirstAfter(const Human& h, const std::string& after, const std::string& prefix) {
  const int from = h.First(after);
  if (from < 0) return -1;
  for (size_t i = static_cast<size_t>(from); i < h.model.received.size(); ++i)
    if (gms::Hex(h.model.received[i], static_cast<size_t>(-1)).compare(0, prefix.size(), prefix) == 0)
      return static_cast<int>(i);
  return -1;
}

// The position of the first shaper select phase message of `phase`, or -1.
int FirstPhase(const Human& h, int phase) {
  for (size_t i = 0; i < h.model.received.size(); ++i) {
    const Bytes& m = h.model.received[i];
    if (m[0] == gms::kGmsShaperSelectMsgs && m.size() >= 14 && m[1] == gms::kShaperPhase &&
        static_cast<int>(gms::GetU32LE(&m[10])) == phase)
      return static_cast<int>(i);
  }
  return -1;
}

// The position of the first record of player `index` with its shaper locked, or -1.
int FirstLockedRecord(const Human& h, uint8_t index) {
  for (size_t i = 0; i < h.model.received.size(); ++i) {
    const Bytes& m = h.model.received[i];
    gms::ShaperRecord r;
    if (m[0] == gms::kGmsShaperSelectMsgs && m.size() >= 2 && m[1] == gms::kShaperRecord &&
        gms::ParseShaperRecord(m.data() + 2, m.size() - 2, &r) && r.index == index &&
        (r.flags & gms::kRecordShaperLocked))
      return static_cast<int>(i);
  }
  return -1;
}

}  // namespace

// A spectator alone over RakNet (the test client on loopback plays the observer): HelloPlayer names it at index 10
// (type 0: its client's spectator map), its first update carries the observer bit with 0x10 (no shaper select) and no
// mpNounDef, the match stores that status first, its statuses are echoed, and EnterGame takes its client to
// kGameStateSpectator (6).
GMS_TEST(SessionSpectatorOverRakNet) {
  LogCapture log;
  gms::SessionOptions so;
  so.skipShaper = so.echoStatus = so.playerUpdate = true;
  so.stepMs = 50;
  SpectatorMatch match;
  const uint64_t user = 2000000011ull;
  match.roster[user] = SpectatorSlot(10);
  LoopbackRig rig;
  CHECK(rig.Start(match, so));
  LiveClientModel client;
  client.send = [&rig](const Bytes& m) { rig.client.Send(m); };
  rig.model = &client;
  CHECK(rig.RunUntil([&] { return client.state != 0; }, 8000));
  CHECK_EQ(client.state, 6);
  CHECK(client.Observer());
  CHECK_EQ(static_cast<int>(client.myIndex), 10);
  CheckSequence(client.Sequence(),
                {"84", "82000a7f000001273a", gms::Hex(gms::BuildPlayerUpdate(10, 0x30, 1, 0, user), static_cast<size_t>(-1)),
                 "b50000000002000000fe416d94010000000000000000",
                 gms::Hex(gms::BuildPlayerStatusEcho(10, 0x10030, 0.0f), static_cast<size_t>(-1)),
                 gms::Hex(gms::BuildPlayerStatusEcho(10, 0x100b0, 0.0f), static_cast<size_t>(-1)), "b6",
                 gms::Hex(gms::BuildPlayerStatusEcho(10, 0x104b1, 1.0f), static_cast<size_t>(-1)), "c003", "b700"});
  // index 10, mask 32 01 01 (members 0, 8, 17: no mpNounDef), status 0x30, team 1, online id
  CHECK_HEX(gms::BuildPlayerUpdate(10, 0x30, 1, 0, user), "a90a320101" "30000000" "01" "0b94357700000000");
  CHECK(!match.statuses.empty() && match.statuses.front() == 0x30u);  // stored before seam 5's first update
  CHECK(log.Has("user 2000000011 is a spectator (gameplay index 10, team 1): status 0x20"));
  CHECK(gms::IsSpectatorIndex(10) && gms::IsSpectatorIndex(14) && !gms::IsSpectatorIndex(9) &&
        !gms::IsSpectatorIndex(15) && !gms::IsSpectatorIndex(0xFF));
}

// Two players (index 0, team 1; index 5, team 2) and a spectator (index 10) without shaper select: every client gets
// every other one (the players' loading screens and gates count the spectator), the spectator's statuses reach the
// players (so their 0x400 gates pass with its 0x401: no load timeout), the players enter Dungeon, the spectator
// Spectator.
GMS_TEST(SessionSpectatorBesideTwoPlayers) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.roster[kUserB].gameplayIndex = 5;
  rig.match.roster[kUserB].team = gms::kTeamRed;
  rig.match.roster[kUserS] = SpectatorSlot(10);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  Human& s = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 5000));
  CHECK(!log.Has("no C0 04 from the client"));
  CHECK_EQ(a.model.state, 5);
  CHECK_EQ(b.model.state, 5);
  CHECK_EQ(s.model.state, 6);
  // the spectator's HelloPlayer: itself (type 0) and both players (type 1)
  CHECK(s.Got(FromHex("82000a7f000001273a01007f000001273a01057f000001273a")));
  CHECK(s.model.Has(0) && s.model.Has(5));
  CHECK_EQ(s.model.statuses[0], 0x10491u);
  CHECK_EQ(s.model.statuses[5], 0x10491u);
  // both players got it: PlayerJoined 10 and its update (the observer bit, no noun), then every status of it
  for (Human* p : {&a, &b}) {
    CHECK_EQ(CountOf(*p, gms::BuildPlayerJoined(10)), 1);
    CHECK(p->model.Has(10));
    CHECK_EQ(p->model.statuses[10], 0x104b1u);  // 0x10 | 0x20 | 0x80 | 0x401 | 0x10000
    CHECK(p->model.allLoadedSent);
  }
  CHECK(log.Has("player update (a spectator): player 10, status 0x30, team 1, noun 0x00000000"));
  for (Human* h : {&a, &b, &s}) CHECK_EQ(h->model.unknownUpdates, 0);
  CHECK(rig.session->State(s.conn).find("; spectator") != std::string::npos);
  CHECK(rig.session->State(a.conn).find("; spectator") == std::string::npos);
}

// nameSpectators off: the players' clients never get the spectator (no PlayerJoined, no status); the spectator's
// client still gets both players; everyone enters.
GMS_TEST(SessionSpectatorNotNamedToPlayers) {
  LogCapture log;
  MultiRig rig(300, true, false, [](gms::SessionOptions& so) { so.nameSpectators = false; });
  rig.match.roster[kUserS] = SpectatorSlot(11);
  Human& a = rig.Connect(kUserA);
  Human& s = rig.Connect(kUserS);
  Human& b = rig.Connect(kUserB);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 5000));
  for (Human* p : {&a, &b}) {
    CHECK(!p->model.Has(11));
    CHECK_EQ(CountOf(*p, gms::BuildPlayerJoined(11)), 0);
    CHECK_EQ(p->model.unknownUpdates, 0);  // nothing of it was sent to them
  }
  CHECK(s.model.Has(0) && s.model.Has(1));
  CHECK_EQ(s.model.state, 6);
}

// With shaper select, two teams (A team 1 at 0, B team 2 at 5) and a spectator at 10: the spectator's client sends C8
// 01 and no C8 02, which starts its step; it gets B's locked record in phase 1-3 (no blind pick for an observer) while
// A gets B's only at the reveal; at the reveal its status gets 0x100 at every client, so each client's 0x100 gate (the
// spectator's own too) passes and sends C0 04 without a timeout; the picks are the two players'; it enters Spectator.
GMS_TEST(SessionSpectatorInShaperSelect) {
  LogCapture log;
  MultiRig rig(300, true, true);
  rig.match.roster[kUserB].gameplayIndex = 5;
  rig.match.roster[kUserB].team = gms::kTeamRed;
  rig.match.config.players[1].gameplayIndex = 5;
  rig.match.config.players[1].team = gms::kTeamRed;
  rig.match.roster[kUserS] = SpectatorSlot(10);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  Human& s = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 8000));
  CHECK(!log.Has("no C0 04 from the client"));
  CHECK(!log.Has("no C8 01 from the spectator's client"));
  CHECK(log.Has("step shaperselect: the observer's C8 01 answered"));
  CHECK_EQ(s.model.shaperReadySent, 0);
  CHECK_EQ(s.model.state, 6);
  CHECK(a.model.state == 5 && b.model.state == 5);
  // the observer saw B's locked record before the reveal; A only with it
  const int sReveal = FirstPhase(s, 4), aReveal = FirstPhase(a, 4);
  CHECK(sReveal > 0 && aReveal > 0);
  const int sB = FirstLockedRecord(s, 5), aB = FirstLockedRecord(a, 5);
  CHECK(sB >= 0 && sB < sReveal);
  CHECK(aB > aReveal);
  CHECK(FirstLockedRecord(s, 0) >= 0 && FirstLockedRecord(s, 0) < sReveal);
  // its 0x100 at every client, and every gate passed
  for (Human* h : {&a, &b, &s}) {
    CHECK(h->model.statuses[10] & gms::kStatusShaperSelectDone);
    CHECK(h->model.statuses[10] & gms::kStatusObserver);
    CHECK(h->model.picksAckSent && h->model.allLoadedSent);
    CHECK_EQ(h->model.unknownRecords, 0);
    CHECK_EQ(h->model.unknownUpdates, 0);
  }
  CHECK(s.model.recordsKnown.count(0) && s.model.recordsKnown.count(5));
  CHECK(log.Has("spectator at the reveal: status 0x"));
  CHECK_EQ(rig.match.picks.size(), size_t{2});  // no pick of the spectator
  const std::string steps = "steps sent connected>hello>prepare>pregameflow>shaperselect>load>done>entergame;";
  CHECK_EQ(rig.session->State(s.conn).compare(0, steps.size(), steps), 0);
  CHECK(!rig.session->Shapers()->HasHuman(s.conn));
}

// A spectator who comes while the players are in shaper select (phase 2): the players' clients are between B6 and
// EnterGame, so they get it only at their EnterGame (their gates never wait for it); the spectator itself goes through
// shaper select as an observer and enters. One who comes after the reveal skips shaper select (0x10) with 0x100.
GMS_TEST(SessionLateSpectatorIsNamedAfterThePlayersPreGame) {
  {
    LogCapture log;
    MultiRig rig(300, true, true);
    rig.match.roster[kUserS] = SpectatorSlot(10);
    Human& a = rig.Connect(kUserA);
    Human& b = rig.Connect(kUserB);
    CHECK(rig.RunUntil([&] { return rig.Phase() == 2; }, 3000));
    Human& s = rig.Connect(kUserS);
    // the players get it at the first tick after their EnterGame
    CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered() && a.model.Has(10) && b.model.Has(10); },
                       8000));
    CHECK(!log.Has("no C0 04 from the client"));
    for (Human* p : {&a, &b}) {
      const int named = p->First("860a"), entered = p->First("b700");
      CHECK(named > entered && entered >= 0);
      CHECK(p->model.Has(10));
    }
    CHECK(log.Has("player update (a spectator, named after the client's pre-game)"));
    CHECK_EQ(s.model.state, 6);
    CHECK_EQ(s.model.shaperReadySent, 0);
    CHECK(s.model.inShaperSelect == false && s.model.recordsKnown.count(0) && s.model.recordsKnown.count(1));
  }
  {
    LogCapture log;
    MultiRig rig(150, true, true);
    rig.match.roster[kUserS] = SpectatorSlot(12);
    Human& a = rig.Connect(kUserA);
    Human& b = rig.Connect(kUserB);
    CHECK(rig.RunUntil([&] { return rig.Phase() == 4; }, 3000));
    Human& s = rig.Connect(kUserS);
    CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 8000));
    const uint32_t st = s.model.statuses[12];
    CHECK((st & gms::kStatusSkipShaperSelect) && (st & gms::kStatusObserver) && (st & gms::kStatusShaperSelectDone));
    CHECK(!s.model.inShaperSelect && s.model.recordsKnown.empty());  // its client never opened shaper select
    CHECK_EQ(s.model.state, 6);
    CHECK(!rig.session->Shapers()->HasObserver(s.conn));
    // its client never saw the reveal: the players' updates carry their picks (mpNounDef, mRole), or the spectator's
    // client has a player without a shaper (its minimap's player list asserts pDefaultSkinNoun, MinimapUI.cpp 369)
    for (const gms::ShaperPick& k : rig.session->Shapers()->Picks()) {
      if (k.scripted) continue;
      CHECK(k.skin != 0);
      CHECK_EQ(s.model.nouns[k.gameplayIndex], k.skin);
    }
    CHECK(log.Has("player update (another human): player 0"));
  }
}

// A spectator leaves in the game: every client that had it gets PlayerDeparted (no 0x4 is kept); the same user comes
// back on a new connection, keeps its gameplay index (RejoinMap), the others get PlayerJoined again and it enters
// Spectator again.
GMS_TEST(SessionSpectatorLeavesAndReturns) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.roster[kUserS] = SpectatorSlot(10);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  Human& s = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 5000));
  rig.End(s);
  rig.Step();
  for (Human* p : {&a, &b}) {
    CHECK(p->Got(gms::BuildPlayerDeparted(10)));
    CHECK(!p->model.Has(10));
  }
  CHECK(log.Has("spectator 10's connection ended: PlayerDeparted to 2 client(s)"));
  CHECK(!log.Has("player 10's connection ended"));  // no 0x4 kept for it
  Human& s2 = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return s2.Entered(); }, 5000));
  CHECK_EQ(static_cast<int>(s2.model.myIndex), 10);
  CHECK_EQ(s2.model.state, 6);
  CHECK_EQ(rig.match.rejoins.size(), size_t{1});
  for (Human* p : {&a, &b}) {
    CHECK_EQ(CountOf(*p, gms::BuildPlayerJoined(10)), 2);
    CHECK(p->model.Has(10));
    CHECK_EQ(p->model.unknownUpdates, 0);
  }
  CHECK(s2.model.Has(0) && s2.model.Has(1));
}

// The match gave a spectator's old index to another spectator while it was away (the host's SpectatorSeats give away
// the seat kept longest when all five are taken): the returning user is seated anew with the match's index instead of
// colliding with the other.
GMS_TEST(SessionSpectatorSeatGivenAway) {
  LogCapture log;
  MultiRig rig(300, true, false);
  rig.match.roster[kUserS] = SpectatorSlot(10);
  Human& a = rig.Connect(kUserA);
  Human& s = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return a.Entered() && s.Entered(); }, 5000));
  rig.End(s);
  rig.match.roster[kUserT] = SpectatorSlot(10);  // the seat went to T
  Human& t = rig.Connect(kUserT);
  CHECK(rig.RunUntil([&] { return t.Entered(); }, 5000));
  rig.match.roster[kUserS] = SpectatorSlot(11);  // S back: the match gives it another index
  Human& s2 = rig.Connect(kUserS);
  CHECK(rig.RunUntil([&] { return s2.Entered(); }, 5000));
  CHECK(log.Has("user 2000000014's earlier gameplay index 10 is another user's now; it is seated anew"));
  CHECK_EQ(static_cast<int>(s2.model.myIndex), 11);
  CHECK_EQ(static_cast<int>(t.model.myIndex), 10);
  CHECK(a.model.Has(10) && a.model.Has(11));
}

// With the match-found prompt (connect type 1) a spectator answers nothing: the pre-game doesn't wait for it and it is
// never a culprit; the players accept and everyone enters.
GMS_TEST(SessionSpectatorAnswersNoMatchFoundPrompt) {
  LogCapture log;
  MultiRig rig(300, true, false, [](gms::SessionOptions& so) { so.matchFoundTimeoutMs = 400; });
  rig.match.setup.prepareFlags = 1;
  rig.match.roster[kUserS] = SpectatorSlot(10);
  Human& a = rig.Connect(kUserA);
  Human& b = rig.Connect(kUserB);
  Human& s = rig.Connect(kUserS);
  s.model.autoAccept = false;
  CHECK(rig.RunUntil([&] { return a.Entered() && b.Entered() && s.Entered(); }, 5000));
  CHECK(rig.match.aborted.empty());
  CHECK(!log.Has("player 10 (no answer to the match-found prompt)"));
  CHECK_EQ(s.model.state, 6);
}

// The host's spectator seats (core/session/spectators.h): the roster's index when free, else the first free one; a
// user keeps its index across a leave; with every index taken the seat kept longest for a user who left goes; none
// while five are connected.
GMS_TEST(SpectatorSeatsRule) {
  gms::SpectatorSeats seats;
  CHECK_EQ(seats.Admit(1, 12), 12);
  CHECK_EQ(seats.Admit(2, 12), 10);  // 12 taken: the first free
  CHECK_EQ(seats.Admit(3, 99), 11);  // no roster index in range
  CHECK_EQ(seats.Admit(1, 10), 12);  // a connected user again: its own
  seats.Left(1);
  CHECK(!seats.Connected(1) && seats.IndexOf(1) == 12);
  CHECK_EQ(seats.Admit(4, 12), 13);  // 12 is kept for user 1
  CHECK_EQ(seats.Admit(1, 10), 12);  // back: its own again
  CHECK_EQ(seats.Admit(5, 0), 14);
  CHECK_EQ(seats.ConnectedCount(), 5);
  CHECK_EQ(seats.Admit(6, 10), -1);  // five connected
  seats.Left(3);                     // 11
  seats.Left(2);                     // 10, later
  uint64_t evicted = 0;
  CHECK_EQ(seats.Admit(6, 10, &evicted), 11);  // the seat kept longest: user 3's
  CHECK_EQ(evicted, 3ull);
  CHECK_EQ(seats.IndexOf(3), -1);
  CHECK_EQ(seats.Admit(0, 10), -1);  // no user id
  CHECK_EQ(seats.Describe(), "10=(2, left) 11=6 12=1 13=4 14=5");
}
