#include "core/session/gms_session.h"

#include <algorithm>
#include <cstdio>

#include "core/common/log.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "core/protocol/wire.h"

namespace gms {
namespace {

const char* const kStepNames[kNumSessionSteps] = {"connected", "hello", "prepare",   "pregameflow", "shaperselect",
                                                  "load",      "done",  "entergame"};

bool IsSet(SteadyClock::time_point t) { return t.time_since_epoch().count() != 0; }

SessionPoint PointOfStep(int step) {
  switch (step) {
    case kStepHello: return SessionPoint::kHello;
    case kStepPrepare: return SessionPoint::kPrepare;
    case kStepPreGameFlow: return SessionPoint::kPreGameFlow;
    case kStepShaperSelect: return SessionPoint::kShaperSelect;
    case kStepLoad: return SessionPoint::kLoad;
    case kStepDone: return SessionPoint::kDone;
    default: return SessionPoint::kEnterGame;
  }
}

// D3-core: shaper select needs the player update and the status model (the 0x100 and 0x400 gates).
SessionOptions Normalized(SessionOptions o) {
  if (o.shaperSelect) o.playerUpdate = o.echoStatus = true;
  return o;
}

}  // namespace

const char* SessionStepName(int step) { return step >= 0 && step < kNumSessionSteps ? kStepNames[step] : "?"; }

int ParseSessionStep(const std::string& name) {
  for (int i = 0; i < kNumSessionSteps; ++i)
    if (name == kStepNames[i]) return i;
  return -1;
}

struct GmsSession::Conn {
  ConnId id = 0;
  std::string addr;
  SteadyClock::time_point start;
  int sent = -1;                            // last step sent
  SteadyClock::time_point due{};            // when the next step is due; epoch = waiting for the client
  SteadyClock::time_point loadDeadline{};
  int clockAnswers = 0;
  bool loaded = false;                      // the client reported 0x400 (its 0x401)
  bool loading = false;                     // in the pre-game's loading sub-state (C0 01 sent, or B6 with 0x10)
  bool loadImplicit = false;                // loading started by B6 with status 0x10 (no C0 01)
  bool allLoaded = false;                   // the client sent C0 04 while loading
  bool helloRefused = false;                // the match refused the HelloReq
  bool helloHeld = false;                   // B15: the match holds the HelloReq (a custom game's lobby)
  HelloReq heldHello;                       // B15: that HelloReq
  uint64_t userId = 0;                      // from HelloReq
  PlayerSlot slot;                          // from the match, at HelloReq
  uint32_t status = 0;                      // the GMS's copy of the player's mStatus (echoStatus)
  float progress = 0.0f;                    // and mStatusProgress
  bool skip = false;                        // status 0x10: no shaper select (script A)
  bool shaperSelect = false;                // D3-core: this connection goes through the match's ShaperSelect
  bool revealed = false;                    // the reveal's player updates went to it
  bool picksAcked = false;                  // its C0 04 after the reveal (every player has 0x100)
  SteadyClock::time_point revealAt{};
  std::set<uint8_t> known;                  // G3a: the players its client has (HelloPlayer, PlayerJoined/Departed)
  bool superseded = false;                  // G3a: its user came back on another connection
  bool rejoined = false;                    // WARDSKIN: its user had a slot (a returning player's new client)
  bool waitB6 = false;                      // G3: its B6 waits for the other humans (waitForPlayers)
  bool b6Go = false;                        // G3: the gate let it through (also after the wait's timeout)
  SteadyClock::time_point prepareAt{};      // G3b: GamePrepareForStart sent (the match-found prompt's countdown)
  bool abortSent = false;                   // G3b: GameAborted went to it
  bool leftGame = false;                    // G3b: it answered with status 0x40000
  bool spectator = false;                   // B15b: its slot is a spectator's gameplay index (10-14)
  std::map<std::string, int> printed;       // messages printed per key (wire byte + body for most)
  std::map<std::string, int> suppressed;    // repeats not printed since the last report
  std::map<std::string, int> total;         // every received GMS message by name
  std::string lastState;                    // the last state line, without the ClockSync count
  SteadyClock::time_point lastStateAt{};

  double Secs() const { return std::chrono::duration<double>(SteadyClock::now() - start).count(); }
};

GmsSession::GmsSession(ITransport& net, IMatch& match, const SessionOptions& options,
                       SteadyClock::time_point serverStart)
    : net_(net), match_(match), o_(Normalized(options)), serverStart_(serverStart), guard_(o_.check) {}

GmsSession::~GmsSession() = default;

int64_t GmsSession::ServerMs() const {
  if (serverClock_) {
    const int64_t v = serverClock_();
    if (v > 0) return v;
  }
  return 1000 + std::chrono::duration_cast<std::chrono::milliseconds>(SteadyClock::now() - serverStart_).count();
}

void GmsSession::OnConnected(ConnId conn, const std::string& addr) {
  std::unique_ptr<Conn>& slot = conns_[conn];
  slot.reset(new Conn);
  Conn& c = *slot;
  c.id = conn;
  c.addr = addr;
  c.start = SteadyClock::now();
  match_.OnConnected(conn, addr);
  SendStep(c, kStepConnected, BuildConnected());
}

void GmsSession::OnDisconnected(ConnId conn) {
  auto it = conns_.find(conn);
  if (it == conns_.end()) return;
  const Conn& c = *it->second;
  Report(*it->second, true);
  // G3a: a seated human leaves (not a connection its user has replaced already): its status is kept with 0x4
  const bool gone = c.sent >= kStepHello && !c.superseded;
  const bool spectator = c.spectator;  // B15b: PlayerDeparted instead
  Departed d;
  d.status = c.status | kStatusGone;
  d.progress = c.progress;
  d.seated = true;
  d.slot = c.slot;
  d.userId = c.userId;
  conns_.erase(it);
  seats_.OnDisconnected(conn);
  if (ss_) ss_->RemoveHuman(conn);
  match_.OnDisconnected(conn);
  if (gone && spectator) SpectatorGone(d.slot.gameplayIndex);
  else if (gone) PlayerGone(d);
}

void GmsSession::OnMessage(ConnId conn, const uint8_t* data, size_t n) {
  auto it = conns_.find(conn);
  if (it == conns_.end() || n == 0) return;
  Conn& c = *it->second;
  const uint8_t b = data[0];
  const uint8_t* body = data + 1;
  const size_t nb = n - 1;
  if (o_.check.mode && Screen(c, b, body, nb)) return;  // dropped (or the connection kicked: `c` is gone)
  ++c.total[GmsName(b)];
  LogClientMessage(c, b, body, nb);
  if (c.superseded) return;  // G3a: its user is back on another connection
  const auto now = SteadyClock::now();
  const auto step = std::chrono::milliseconds(o_.stepMs);

  if (b == kGmsHelloReq) {
    HelloReq req;
    if (!ParseHelloReq(body, nb, &req)) return;
    c.userId = req.userId;
    if (c.sent != kStepConnected || o_.stopAfter < kStepHello || c.helloHeld) return;
    if (match_.HoldHello(c.id, req)) {  // B15: a custom game's lobby; Tick answers it once the match starts
      c.helloHeld = true;
      c.heldHello = req;
      Logf("%s %8.3f    HelloReq from user %llu held: the match waits in its lobby", Clock().c_str(), c.Secs(),
           static_cast<unsigned long long>(req.userId));
      return;
    }
    HandleHello(c, req, now);
    return;
  }

  if (b == kGmsShaperSelectMsgs && c.shaperSelect && ss_ && c.sent >= kStepPreGameFlow && !c.loading) {
    // D3-core: from B6 on the client's C8 messages go to the match's shaper select (its C8 02 starts it)
    if (o_.stopAfter < kStepShaperSelect) return;
    if (c.spectator) {
      // B15b: an observer's client sends C8 01 and no C8 02 (ShaperSelect_Init 0x00524620); its C8 01 starts its step
      if (nb >= 1 && body[0] == kShaperHello) {
        ss_->OnObserverReady(c.id, ServerMs(), *this);
        if (c.sent == kStepPreGameFlow) {
          c.sent = kStepShaperSelect;
          Logf("%s %8.3f    step shaperselect: the observer's C8 01 answered (%s)", Clock().c_str(), c.Secs(),
               ss_->Describe().c_str());
          Point(c, SessionPoint::kShaperSelect);
        }
      } else {
        Logf("%s %8.3f    C8 %s from a spectator; ignored (an observer picks nothing)", Clock().c_str(), c.Secs(),
             Hex(body, nb).c_str());
      }
      AfterShaperSelect();
      return;
    }
    if (nb >= 1 && body[0] == kShaperReady) {
      ss_->OnReady(c.id, ServerMs(), *this);
      if (c.sent == kStepPreGameFlow) {
        c.sent = kStepShaperSelect;
        Logf("%s %8.3f    step shaperselect: the client's C8 02 answered (%s)", Clock().c_str(), c.Secs(),
             ss_->Describe().c_str());
        Point(c, SessionPoint::kShaperSelect);
      }
    } else {
      ss_->OnMessage(c.id, body, nb, ServerMs(), *this);
    }
    AfterShaperSelect();
    return;
  }

  if (b == kGmsBattlegroundsPreGameMsgs && nb >= 1 && body[0] == kPreGameAllLoaded && c.revealed && !c.loading &&
      !c.picksAcked) {
    // D3-core: C0 04 after the reveal: every player's status has 0x100 (PreDungeonState_Update, sub-state 1)
    c.picksAcked = true;
    Logf("%s %8.3f    client reports every player's shaper select done (C0 04)", Clock().c_str(), c.Secs());
    return;
  }

  if (b == kGmsBattlegroundsPreGameMsgs && nb >= 1 && body[0] == kPreGameAllLoaded && c.loading && !c.allLoaded) {
    // C0 04 while loading: every player's echoed status has 0x400 (PreDungeonState_Update 0x006AB070)
    c.allLoaded = true;
    Logf("%s %8.3f    client reports all players loaded (C0 04)", Clock().c_str(), c.Secs());
    if (o_.echoStatus && c.sent == kStepLoad && o_.stopAfter >= kStepDone) c.due = now + step;
    return;
  }

  if (b == kGmsClockSync) {
    int64_t clientMs;
    if (!ParseClockSync(body, nb, &clientMs)) return;
    const int64_t serverMs = ServerMs();
    net_.Send(c.id, BuildClockSyncAnswer(clientMs, serverMs));
    ++c.clockAnswers;
    if (c.clockAnswers <= 6 || c.clockAnswers % 25 == 0)
      Logf("%s %8.3f    -> C7 ClockSync answer #%d: client %lld ms, server %lld ms", Clock().c_str(), c.Secs(),
           c.clockAnswers, static_cast<long long>(clientMs), static_cast<long long>(serverMs));
    if (c.clockAnswers == 5) {
      Point(c, SessionPoint::kClock5);
      if (c.sent == kStepPrepare && o_.stopAfter >= kStepPreGameFlow) c.due = now + step;
    }
    return;
  }

  if (b == kGmsPlayerStatusUpdate) {
    PlayerStatusReport r;
    if (!ParsePlayerStatusUpdate(body, nb, &r)) return;
    if ((r.flags & kStatusLeftGame) && !c.leftGame) {
      c.leftGame = true;  // G3b: the answer to GameAborted (LobbyState_HandleMessage 0x00546A40)
      Logf("%s %8.3f    client left the aborted game (status 0x%x)", Clock().c_str(), c.Secs(), r.flags);
    }
    if (o_.echoStatus) {
      // D2c's status model: OR in what the client reports, echo the whole word when it (or the progress) changed.
      const uint32_t status = c.status | r.flags;
      if (status != c.status || r.progress != c.progress) {
        c.status = status;
        c.progress = r.progress;
        char what[80];
        std::snprintf(what, sizeof what, "status echo 0x%x, progress %.3f", c.status, c.progress);
        match_.OnPlayerStatus(c.id, c.status, c.progress);  // first, so the match's own echo has the new status
        const Bytes echo = BuildPlayerStatusEcho(c.slot.gameplayIndex, c.status, c.progress);
        SendPlayerUpdate(c, echo, PlayerStatusEchoMembers(), what);
        if (o_.nameHumans) SendStatusToOthers(c, echo, what);  // G3: the other clients' gates and accept counts
      }
    }
    if ((r.flags & kStatusLevelLoaded) && !c.loaded) {
      c.loaded = true;
      Logf("%s %8.3f    client reports the level loaded (status 0x%x)", Clock().c_str(), c.Secs(), r.flags);
      Point(c, SessionPoint::kLoaded);
      if (!o_.echoStatus && c.sent == kStepLoad && o_.stopAfter >= kStepDone) c.due = now + step;
    }
    return;
  }

  match_.OnClientMessage(c.id, b, body, nb);
}

// A HelloReq the match takes now (OnMessage's, or B15's held one once the match leaves its lobby): the match's slot,
// HelloPlayer, the first player updates, and the next step.
void GmsSession::HandleHello(Conn& c, const HelloReq& req, SteadyClock::time_point now) {
  const auto step = std::chrono::milliseconds(o_.stepMs);
  PlayerSlot slot;
  if (seats_.Earlier(req.userId, &slot)) {  // G3a: a returning user's slot, for the match to keep
    // B15b: unless another user's connection has that gameplay index now (a spectator seat given away meanwhile)
    const Conn* holder = ConnOfIndex(slot.gameplayIndex);
    if (holder && holder != &c && holder->userId != req.userId) {
      Logf("%s %8.3f    user %llu's earlier gameplay index %u is another user's now; it is seated anew",
           Clock().c_str(), c.Secs(), static_cast<unsigned long long>(req.userId), slot.gameplayIndex);
      seats_.Forget(req.userId);
      slot = PlayerSlot();
    }
  }
  if (!match_.OnHelloReq(c.id, req, &slot)) {
    c.helloRefused = true;
    Logf("%s %8.3f    HelloReq from user %llu refused by the match; no HelloPlayer", Clock().c_str(), c.Secs(),
         static_cast<unsigned long long>(req.userId));
    return;
  }
  const RejoinMap::Admission seat = seats_.Admit(req.userId, c.id, slot);
  if (seat.rejoin) {
    if (seat.slot.gameplayIndex != slot.gameplayIndex || seat.slot.team != slot.team)
      Logf("%s %8.3f    the match gave user %llu player %u, team %u; it keeps its earlier slot", Clock().c_str(),
           c.Secs(), static_cast<unsigned long long>(req.userId), slot.gameplayIndex, slot.team);
    slot = seat.slot;
  }
  c.slot = slot;
  c.spectator = IsSpectatorIndex(slot.gameplayIndex);  // B15b: an observer
  if (c.spectator) {
    c.slot.noun = 0;  // no shaper (TransitionToLoading preloads no noun for an observer)
    c.status |= kStatusObserver;
    Logf("%s %8.3f    user %llu is a spectator (gameplay index %u, team %u): status 0x%x", Clock().c_str(), c.Secs(),
         static_cast<unsigned long long>(req.userId), slot.gameplayIndex, slot.team, c.status);
  }
  departed_.erase(slot.gameplayIndex);  // a connection has the index (again)
  if (seat.rejoin) Rejoined(c, seat);
  SetUpShaperSelect(c);
  // HelloPlayer: one record for the client itself (type 0), then one per player without a connection (type 1)
  HelloPlayerRecord me;
  me.type = 0;
  me.gameplayIndex = slot.gameplayIndex;
  me.ipv4 = o_.helloIpv4;
  me.port = o_.helloPort;
  std::vector<HelloPlayerRecord> records{me};
  for (const auto& kv : others_) {
    HelloPlayerRecord r = me;
    r.type = 1;
    r.gameplayIndex = kv.first;
    records.push_back(r);
  }
  if (o_.nameHumans) {
    // G3: every other seated human, and every human who had a connection and left (it keeps its 0x4); B15b: the
    // spectators only with nameSpectators (a spectator's client gets every player)
    std::set<uint8_t> humans;
    for (const auto& kv : conns_) {
      const Conn& o = *kv.second;
      if (&o != &c && !o.superseded && o.sent >= kStepHello && o.slot.gameplayIndex != slot.gameplayIndex &&
          Names(c, o))
        humans.insert(o.slot.gameplayIndex);
    }
    for (const auto& kv : departed_)
      if (kv.second.seated && kv.first != slot.gameplayIndex) humans.insert(kv.first);
    for (uint8_t index : humans) {
      HelloPlayerRecord r = me;
      r.type = 1;
      r.gameplayIndex = index;
      records.push_back(r);
    }
  }
  for (const HelloPlayerRecord& r : records) c.known.insert(r.gameplayIndex);
  SendStep(c, kStepHello, BuildHelloPlayer(records));
  // B15b: a spectator's own update always goes out: its observer bit decides its client's shaper select and EnterGame
  if (o_.playerUpdate || o_.skipShaper || c.spectator) SendInitialPlayerUpdate(c);
  for (const auto& kv : others_) {
    const OtherPlayer& p = kv.second.p;
    std::vector<int> members;
    if (p.status) members.push_back(kPlayerStatus);
    members.push_back(kPlayerTeam);
    if (p.isBot) members.push_back(kPlayerIsBot);
    members.push_back(kPlayerOnlineId);
    char what[120];
    std::snprintf(what, sizeof what, "player update (no connection): player %u, status 0x%x, team %u%s, online id %llu",
                  p.gameplayIndex, p.status, p.team, p.isBot ? ", bot" : "",
                  static_cast<unsigned long long>(p.onlineId));
    SendOtherUpdate(c, kv.second, members, what);
  }
  if (o_.nameHumans) NameHumans(c);
  if (o_.stopAfter >= kStepPrepare) c.due = now + step;
}

// LabsPlayerGlobalUpdate after HelloPlayer (D2c): mStatus (0x10 with skipShaper), mTeam, mpNounDef and
// mPlayerOnlineId = HelloReq's user id. With shaper select (D3-core) no 0x10 and no mpNounDef: the shaper comes with
// the reveal (script B step 2). B15b: a spectator's carries its observer bit 0x20 and no mpNounDef.
void GmsSession::SendInitialPlayerUpdate(Conn& c) {
  if (c.skip) c.status |= kStatusSkipShaperSelect;
  // B15b: the match stores a spectator's status (0x20, and 0x10 when it skips) before seam 5 writes the update from it
  if (c.spectator) match_.OnPlayerStatus(c.id, c.status, c.progress);
  const uint32_t noun = c.shaperSelect || c.spectator ? 0 : c.slot.noun;
  char what[120];
  std::snprintf(what, sizeof what, "player update: status 0x%x, team %u, noun 0x%08x, online id %llu", c.status,
                c.slot.team, noun, static_cast<unsigned long long>(c.userId));
  SendPlayerUpdate(c, BuildPlayerUpdate(c.slot.gameplayIndex, c.status, c.slot.team, noun, c.userId),
                   PlayerUpdateMembers(c.status, noun), what);
}

// Seam 5 (D2f): the match's own LabsPlayerGlobalUpdate with the same members if it has one, else the session's bytes.
// The log says whether the two are equal (the host's, written from the server's cLabsPlayer, should be).
void GmsSession::SendPlayerUpdate(Conn& c, const Bytes& own, const std::vector<int>& members, const std::string& what) {
  SendPlayerUpdateFor(c, c.id, c.slot.gameplayIndex, own, members, what);
}

// The same for any player's update to the connection `to`: `owner` is the player's own connection (kNoConn for a
// player without one), as IMatch::PlayerUpdate takes it.
void GmsSession::SendPlayerUpdateFor(Conn& to, ConnId owner, uint8_t index, const Bytes& own,
                                     const std::vector<int>& members, const std::string& what) {
  Bytes m;
  if (match_.PlayerUpdate(owner, index, members, &m) && !m.empty()) {
    SendRaw(to, m, what + (m == own ? "; from the match (= the session's bytes)"
                                    : "; from the match (the session's: " + Hex(own, 64) + ")"));
    Bytes follow;
    // (the host adds member 15, mpBotData, to a bot's update itself, so `members` does not show it: ask for every
    // player without a connection; the host answers only for bots)
    if (owner == kNoConn && match_.PlayerRoleFollowUp(index, &follow) && !follow.empty())
      SendRaw(to, follow, what + "; the bot's role again after its bot data (ReflectBotData resets it)");
    return;
  }
  SendRaw(to, own, what);
}

void GmsSession::SendOtherUpdate(Conn& to, const Other& o, const std::vector<int>& members, const std::string& what) {
  PlayerGlobalValues v;
  v.status = o.p.status;
  v.progress = o.progress;
  v.team = o.p.team;
  v.isBot = o.p.isBot ? 1 : 0;
  v.onlineId = o.p.onlineId;
  SendPlayerUpdateFor(to, kNoConn, o.p.gameplayIndex, BuildPlayerGlobalUpdate(o.p.gameplayIndex, members, v), members,
                      what);
}

// At an accepted HelloReq (D3-core): the match's shaper select (asked once), its players without a connection, and
// whether this connection goes through shaper select or skips it with status 0x10.
void GmsSession::SetUpShaperSelect(Conn& c) {
  if (o_.shaperSelect && !ssAsked_) {
    ssAsked_ = true;
    ShaperSelectConfig cfg;
    if (match_.ShaperSelectSetup(&cfg)) {
      ss_.reset(new ShaperSelect(cfg));
      Logf("%s %8.3f    shaper select set up: timers %u/%u/%u/%u ms, %zu configured player(s), %zu list entr%s for "
           "humans, seed %u",
           Clock().c_str(), c.Secs(), cfg.timers.pickMs, cfg.timers.rolePerkMs, cfg.timers.skinMs, cfg.timers.revealMs,
           cfg.players.size(), cfg.humanDefault.list.size(), cfg.humanDefault.list.size() == 1 ? "y" : "ies",
           cfg.seed);
    } else {
      Logf("%s %8.3f    the match sets up no shaper select; the session skips it (status 0x10)", Clock().c_str(),
           c.Secs());
    }
  }
  for (const OtherPlayer& p : match_.OtherPlayers()) {
    if (p.gameplayIndex == c.slot.gameplayIndex || ConnOfIndex(p.gameplayIndex)) {
      Logf("%s %8.3f    player %u without a connection left out: a connection has that gameplay index",
           Clock().c_str(), c.Secs(), p.gameplayIndex);
      continue;
    }
    if (!others_.count(p.gameplayIndex)) others_[p.gameplayIndex].p = p;
  }
  // B15b: a spectator who comes after the reveal skips shaper select (status 0x10; the picks are made)
  c.shaperSelect = ss_ != nullptr && !(c.spectator && revealed_);
  c.skip = !c.shaperSelect && (o_.skipShaper || o_.shaperSelect || (c.spectator && ss_ != nullptr));
  if (c.shaperSelect && c.spectator) ss_->AddObserver(c.id);
  else if (c.shaperSelect) ss_->AddHuman(c.id, c.slot.gameplayIndex, c.slot.team);
}

GmsSession::Conn* GmsSession::ConnOfIndex(uint8_t gameplayIndex) {
  for (auto& kv : conns_)
    if (kv.second->sent >= kStepHello && !kv.second->superseded && kv.second->slot.gameplayIndex == gameplayIndex)
      return kv.second.get();
  return nullptr;
}

// G3a: an accepted HelloReq of a user who had a slot: its earlier connection, if it is still there, gets nothing more
// (the user's new client replaces it), and the match moves its per-player state (IMatch::OnRejoin).
void GmsSession::Rejoined(Conn& c, const RejoinMap::Admission& a) {
  Conn* old = nullptr;
  auto it = conns_.find(a.previous);
  if (a.previous != c.id && it != conns_.end()) old = it->second.get();
  if (old) {
    old->superseded = true;
    old->due = {};
  }
  Logf("%s %8.3f    user %llu rejoins as player %u (team %u); its earlier connection %s", Clock().c_str(), c.Secs(),
       static_cast<unsigned long long>(c.userId), a.slot.gameplayIndex, a.slot.team,
       old ? ("(" + old->addr + ") is superseded").c_str() : "had ended");
  c.rejoined = true;
  match_.OnRejoin(c.id, a.previous, a.slot);
}

// The player's status at the reveal, with 0x100 set: a connection's (OnPlayerStatus first), a player without a
// connection's, or (G3a) a human whose connection ended: its last status with 0x4, so the other clients' 0x100 and
// 0x400 gates leave it out (GMS_PROTOCOL.md *1 to 10 players*); a configured human who never connected likewise.
uint32_t GmsSession::RevealStatus(uint8_t gameplayIndex) {
  if (Conn* owner = ConnOfIndex(gameplayIndex)) {
    if (!(owner->status & kStatusShaperSelectDone)) {
      owner->status |= kStatusShaperSelectDone;
      match_.OnPlayerStatus(owner->id, owner->status, owner->progress);
    }
    return owner->status;
  }
  auto other = others_.find(gameplayIndex);
  if (other != others_.end()) return other->second.p.status |= kStatusShaperSelectDone;
  auto gone = departed_.find(gameplayIndex);
  if (gone == departed_.end()) {
    Logf("%s    shaper select: player %u has a pick but never had a connection; status 0x%x", Clock().c_str(),
         gameplayIndex, kStatusGone | kStatusShaperSelectDone);
    gone = departed_.emplace(gameplayIndex, Departed()).first;
  }
  return gone->second.status |= kStatusShaperSelectDone;
}

// The reveal (D3-core, GMS_PROTOCOL.md *Into the game* step 1): every player's status | 0x100, the match's
// OnShaperPicks, then to every connection in the pre-game one LabsPlayerGlobalUpdate per player its client has
// (mStatus, mRole, mpNounDef = the skin, mLoadoutID/Version with a perk page) through seam 5, and the point `picked`.
// G3a: a connection that comes after the reveal (a late or returning player) gets them too, with the statuses of
// then, and so reaches C0 01 like the others; a player new to shaper select after the reveal asks OnShaperPicks again.
void GmsSession::AfterShaperSelect() {
  if (!ss_ || !ss_->Revealed()) return;
  MarkSpectatorsPicked();  // B15b
  std::vector<Conn*> to;
  for (auto& kv : conns_) {
    Conn& c = *kv.second;
    if (c.shaperSelect && !c.loading && !c.revealed && !c.superseded && c.sent >= kStepHello) to.push_back(&c);
  }
  const bool first = !revealed_;
  if (!first && to.empty()) return;
  revealed_ = true;
  std::vector<ShaperPick> picks = ss_->Picks();
  std::vector<ConnId> owners;
  bool grown = false;
  for (ShaperPick& k : picks) {
    k.status = RevealStatus(k.gameplayIndex);
    const Conn* owner = ConnOfIndex(k.gameplayIndex);
    owners.push_back(owner ? owner->id : kNoConn);
    grown = picked_.insert(k.gameplayIndex).second || grown;
  }
  if (first) Logf("%s    shaper select: reveal: %s", Clock().c_str(), ss_->Describe().c_str());
  if (grown) match_.OnShaperPicks(picks);
  for (Conn* c : to) SendReveal(*c, picks, owners);
}

void GmsSession::SendReveal(Conn& to, const std::vector<ShaperPick>& picks, const std::vector<ConnId>& owners) {
  for (size_t i = 0; i < picks.size(); ++i) {
    const ShaperPick& k = picks[i];
    if (!to.known.count(k.gameplayIndex)) continue;  // its client has no such player (it would drop the update)
    const std::vector<int> members = RevealMembers(k);
    char what[120];
    std::snprintf(what, sizeof what, "reveal: player %u status 0x%x, role %u, noun 0x%08x%s", k.gameplayIndex,
                  k.status, k.role, k.skin, k.page ? ", perk page" : "");
    SendPlayerUpdateFor(to, owners[i], k.gameplayIndex,
                        BuildPlayerGlobalUpdate(k.gameplayIndex, members, RevealValues(k)), members, what);
    // D3-perks: the pick's perk page stats as the pre-game loadout (C0 02, before C0 01): the client keeps them in the
    // player's loadout (LabsPlayer_SetLoadout), which the shaper and target panels' perk tooltips show
    const Bytes loadout = RevealLoadout(k);
    if (!loadout.empty()) {
      char lw[120];
      std::snprintf(lw, sizeof lw, "pre-game loadout: player %u perk page %u (%zu attribute(s), %zu modifier(s))",
                    k.gameplayIndex, k.page, k.loadout.size(), k.modifiers.size());
      SendRaw(to, loadout, lw);
    }
    // WARDSKIN: a returning player's new client gets the ward skin and announcer pack it picked before the reveal
    // (OnLocalPlayerReconnect 0x8A, before C0 01, where TransitionToLoading preloads them): its own C8 0E echoes went to
    // the earlier client, and the new one doesn't pass phase 3 (it comes in phase 4 or later) [assumed: the original's
    // use of 0x8A; the message has no other sender's trace]
    if (to.rejoined && k.gameplayIndex == to.slot.gameplayIndex && (k.ward || k.announcer)) {
      char vw[96];
      std::snprintf(vw, sizeof vw, "local player reconnect: player %u ward 0x%08x, announcer 0x%08x", k.gameplayIndex,
                    k.ward, k.announcer);
      SendRaw(to, BuildLocalPlayerReconnect(k.ward, k.announcer), vw);
    }
  }
  to.revealed = true;
  to.revealAt = SteadyClock::now();
  Point(to, SessionPoint::kPicked);
}

// G3a: a seated human's connection ended (after the match's OnDisconnected). Its last status is kept with 0x4 for the
// reveal; the clients that have its status from the reveal already and are still in the pre-game get it at once, so
// their 0x400 gate leaves it out (through seam 5 as a player without a connection: members 0 and 1). G3 (nameHumans):
// every client that has the player gets it at once, in the pre-game or in the game.
void GmsSession::PlayerGone(const Departed& gone) {
  const uint8_t gameplayIndex = gone.slot.gameplayIndex;
  Departed& d = departed_[gameplayIndex];
  d = gone;
  Logf("%s    player %u's connection ended: status 0x%x kept for the pre-game gates", Clock().c_str(), gameplayIndex,
       d.status);
  if (!revealed_ && !o_.nameHumans) return;
  PlayerGlobalValues v;
  v.status = d.status;
  v.progress = d.progress;
  const std::vector<int> members = PlayerStatusEchoMembers();
  char what[96];
  std::snprintf(what, sizeof what, "player %u gone: status 0x%x, progress %.3f", gameplayIndex, d.status, d.progress);
  for (auto& kv : conns_) {
    Conn& to = *kv.second;
    if (to.superseded || to.sent < kStepHello || !to.known.count(gameplayIndex)) continue;
    if (!o_.nameHumans && (!to.revealed || to.sent >= kStepEnterGame)) continue;
    SendPlayerUpdateFor(to, kNoConn, gameplayIndex, BuildPlayerGlobalUpdate(gameplayIndex, members, v), members, what);
  }
}

// G3: a newcomer `c` (its HelloPlayer named every other seated and departed human) gets one update per such player,
// written by the match through seam 5 like that player's own; each other seated human's client gets PlayerJoined for
// `c` (nothing if it has the player already: a rejoin) and `c`'s update with its status, which replaces the 0x4 a
// rejoining player had there.
void GmsSession::NameHumans(Conn& c) {
  const uint8_t me = c.slot.gameplayIndex;
  for (auto& kv : conns_) {
    Conn& o = *kv.second;
    if (&o == &c || o.superseded || o.sent < kStepHello || o.slot.gameplayIndex == me) continue;
    if (!c.known.count(o.slot.gameplayIndex)) continue;  // B15b: a spectator without nameSpectators
    SendHumanUpdate(c, o, o.status != 0, o.spectator ? "player update (a spectator)" : "player update (another human)");
  }
  for (const auto& kv : departed_) {
    const Departed& d = kv.second;
    if (!d.seated || kv.first == me || !c.known.count(kv.first)) continue;
    uint32_t noun = ss_ ? 0 : d.slot.noun;
    uint8_t role = 0;
    bool picked = false;
    if (ss_ && ss_->Revealed())  // its pick, as for a seated player (SendHumanUpdate)
      for (const ShaperPick& k : ss_->Picks())
        if (k.gameplayIndex == kv.first && k.skin) {
          noun = k.skin;
          role = k.role;
          picked = true;
        }
    std::vector<int> members{kPlayerStatus, kPlayerStatusProgress, kPlayerTeam};
    if (picked) members.push_back(kPlayerRole);
    if (noun) members.push_back(kPlayerNounDef);
    members.push_back(kPlayerOnlineId);
    PlayerGlobalValues v;
    v.status = d.status;
    v.progress = d.progress;
    v.team = d.slot.team;
    v.role = role;
    v.noun = noun;
    v.onlineId = d.userId;
    char what[160];
    std::snprintf(what, sizeof what,
                  "player update (a departed human): player %u, status 0x%x, team %u, noun 0x%08x, online id %llu",
                  kv.first, d.status, d.slot.team, noun, static_cast<unsigned long long>(d.userId));
    SendPlayerUpdateFor(c, kNoConn, kv.first, BuildPlayerGlobalUpdate(kv.first, members, v), members, what);
  }
  for (auto& kv : conns_) {
    Conn& o = *kv.second;
    if (&o == &c || o.superseded || o.sent < kStepHello || o.slot.gameplayIndex == me) continue;
    if (!Names(o, c)) continue;
    // B15b: a spectator is named to a client between its B6 and its EnterGame only at its EnterGame (its gates would
    // wait for the spectator's 0x100/0x400); NameSpectatorsLate does it then
    if (c.spectator && InGates(o)) continue;
    SendPlayerJoined(o.id, me);
    SendHumanUpdate(o, c, true, c.spectator ? "player update (a spectator)" : "player update (another human)");
  }
}

// B15b: whether `to`'s client gets a record (HelloPlayer, PlayerJoined) of `player`: every player always (a spectator's
// client too), a spectator with nameSpectators.
bool GmsSession::Names(const Conn& to, const Conn& player) const {
  (void)to;
  return !player.spectator || o_.nameSpectators;
}

// B15b: a client between its B6 and its EnterGame: its pre-game gates count every player and spectator it has.
bool GmsSession::InGates(const Conn& c) { return c.sent >= kStepPreGameFlow && c.sent < kStepEnterGame; }

// B15b: a spectator whose client was named to nobody that sat in its pre-game gates then: once such a client has left
// them (EnterGame) it gets PlayerJoined and the spectator's update.
void GmsSession::NameSpectatorsLate() {
  if (!o_.nameHumans || !o_.nameSpectators) return;
  for (auto& skv : conns_) {
    Conn& s = *skv.second;
    if (!s.spectator || s.superseded || s.sent < kStepHello) continue;
    for (auto& kv : conns_) {
      Conn& to = *kv.second;
      if (&to == &s || to.superseded || to.sent < kStepHello || InGates(to) || to.known.count(s.slot.gameplayIndex))
        continue;
      SendPlayerJoined(to.id, s.slot.gameplayIndex);
      SendHumanUpdate(to, s, true, "player update (a spectator, named after the client's pre-game)");
    }
  }
}

// B15b: at the reveal every seated spectator's status gets 0x100 (no pick: the observer's own client and every client
// that has it count it in their 0x100 gate), sent to it and to the clients that have it.
void GmsSession::MarkSpectatorsPicked() {
  for (auto& kv : conns_) {
    Conn& s = *kv.second;
    if (!s.spectator || s.superseded || s.sent < kStepHello || (s.status & kStatusShaperSelectDone)) continue;
    s.status |= kStatusShaperSelectDone;
    match_.OnPlayerStatus(s.id, s.status, s.progress);
    char what[96];
    std::snprintf(what, sizeof what, "spectator at the reveal: status 0x%x, progress %.3f", s.status, s.progress);
    const Bytes echo = BuildPlayerStatusEcho(s.slot.gameplayIndex, s.status, s.progress);
    SendPlayerUpdate(s, echo, PlayerStatusEchoMembers(), what);
    SendStatusToOthers(s, echo, what);
  }
}

// B15b: a spectator's connection ended: PlayerDeparted to every client that has it (GameSimulator_RemovePlayer takes it
// out of their spectator maps, so no gate waits for it; nothing of it is kept but its seat in the RejoinMap).
void GmsSession::SpectatorGone(uint8_t gameplayIndex) {
  int n = 0;
  for (auto& kv : conns_)
    if (Knows(kv.first, gameplayIndex) && SendPlayerDeparted(kv.first, gameplayIndex)) ++n;
  Logf("%s    spectator %u's connection ended: PlayerDeparted to %d client(s)", Clock().c_str(), gameplayIndex, n);
}

// One human's update to another client: mStatus and mStatusProgress (`withStatus`), mTeam, mpNounDef (without shaper
// select: the reveal sends the picks), mPlayerOnlineId; through seam 5, so the host writes it from its server player.
// B15b: a spectator's without mpNounDef (its status has the observer bit).
void GmsSession::SendHumanUpdate(Conn& to, const Conn& p, bool withStatus, const std::string& what) {
  uint32_t noun = p.shaperSelect || p.spectator ? 0 : p.slot.noun;
  // A player whose shaper select is revealed: its pick (role, perk page, mpNounDef = the skin), which a client that
  // comes after the reveal without shaper select of its own (a spectator of the running game) gets nowhere else: the
  // reveal's updates go only to clients in shaper select. Without it that client had the player without a shaper
  // (a late spectator's minimap list asserted pDefaultSkinNoun, MinimapUI.cpp 369, and stopped).
  const ShaperPick* pick = nullptr;
  std::vector<ShaperPick> picks;
  if (p.shaperSelect && !p.spectator && ss_ && ss_->Revealed()) {
    picks = ss_->Picks();
    for (const ShaperPick& k : picks)
      if (k.gameplayIndex == p.slot.gameplayIndex && k.skin) pick = &k;
  }
  if (pick) noun = pick->skin;
  std::vector<int> members;
  if (withStatus) {
    members.push_back(kPlayerStatus);
    members.push_back(kPlayerStatusProgress);
  }
  members.push_back(kPlayerTeam);
  if (pick) {
    members.push_back(kPlayerRole);
    if (pick->page) {
      members.push_back(kPlayerLoadoutId);
      members.push_back(kPlayerLoadoutVersion);
    }
  }
  if (noun) members.push_back(kPlayerNounDef);
  members.push_back(kPlayerOnlineId);
  PlayerGlobalValues v;
  v.status = p.status;
  v.progress = p.progress;
  v.team = p.slot.team;
  v.noun = noun;
  v.onlineId = p.userId;
  if (pick) {
    v.role = pick->role;
    v.loadoutId = pick->page;
    v.loadoutVersion = pick->pageVersion;
  }
  char buf[200];
  std::snprintf(buf, sizeof buf, "%s: player %u, status 0x%x, team %u, noun 0x%08x, online id %llu", what.c_str(),
                p.slot.gameplayIndex, p.status, p.slot.team, noun, static_cast<unsigned long long>(p.userId));
  SendPlayerUpdateFor(to, p.id, p.slot.gameplayIndex, BuildPlayerGlobalUpdate(p.slot.gameplayIndex, members, v),
                      members, buf);
}

// G3: a status echo of `c`'s player to every other client that has the player.
void GmsSession::SendStatusToOthers(const Conn& c, const Bytes& own, const std::string& what) {
  for (auto& kv : conns_) {
    Conn& to = *kv.second;
    if (&to == &c || to.superseded || to.sent < kStepHello || !to.known.count(c.slot.gameplayIndex)) continue;
    SendPlayerUpdateFor(to, c.id, c.slot.gameplayIndex, own, PlayerStatusEchoMembers(),
                        "player " + std::to_string(c.slot.gameplayIndex) + "'s " + what);
  }
}

// G3: a seated human is ready for EnterPreGameFlow once its clock is primed (5 ClockSync answers) and, with connect type
// 1 (GamePrepareForStart flags bit 0: MatchFoundUI's prompt), it answered the prompt (0x10000 accepted or 0x20000
// declined); a connection past B6 is ready.
bool GmsSession::ReadyForPreGame(const Conn& c, bool prompt) const {
  if (c.sent > kStepPrepare) return true;
  if (c.sent < kStepPrepare || c.clockAnswers < 5) return false;
  // B15b: a spectator answers no match-found prompt
  return !prompt || c.spectator || (c.status & (kStatusMatchAccepted | kStatusMatchDeclined)) != 0;
}

// G3: B6 may go out when every seated human is ready and every expected human (IMatch::ExpectedHumans) has had a
// connection (one that left doesn't hold the others up). `missing` gets the ones the gate waits for.
bool GmsSession::PreGameGateOpen(std::string* missing) {
  const bool prompt = (match_.Setup().prepareFlags & 1u) != 0;
  std::string m;
  for (const auto& kv : conns_) {
    const Conn& c = *kv.second;
    if (c.superseded || c.sent < kStepHello || ReadyForPreGame(c, prompt)) continue;
    char buf[96];
    std::snprintf(buf, sizeof buf, " player %u (%s)", c.slot.gameplayIndex,
                  c.sent < kStepPrepare ? "no GamePrepareForStart yet"
                  : c.clockAnswers < 5  ? "clock not primed"
                                        : "no answer to the match-found prompt");
    m += buf;
  }
  // once B6 went out to anyone, a newcomer waits only for the humans that are there
  if (!preGameStarted_)
    for (uint64_t user : match_.ExpectedHumans())
      if (user && !seats_.ByUser(user)) m += " user " + std::to_string(user) + " (not connected)";
  if (missing) *missing = m;
  return m.empty();
}

// G3: B6 for every connection that waits for it, now.
void GmsSession::OpenPreGameGate(SteadyClock::time_point now, const char* why) {
  int n = 0;
  for (auto& kv : conns_) {
    Conn& c = *kv.second;
    if (!c.waitB6) continue;
    c.waitB6 = false;
    c.b6Go = true;
    if (!c.superseded) {
      c.due = now;
      ++n;
    }
  }
  const double waited = IsSet(gateSince_) ? std::chrono::duration<double>(now - gateSince_).count() : 0.0;
  gateSince_ = {};
  Logf("%s    pre-game: %s after %.3f s; EnterPreGameFlow to %d connection(s)", Clock().c_str(), why, waited, n);
}

// G3b: with connect type 1 (the match-found prompt) a seated human in the lobby that declined (0x20000), or hasn't
// answered matchFoundTimeoutMs after its GamePrepareForStart, aborts the game for everyone (before any B6).
void GmsSession::CheckMatchFound(SteadyClock::time_point now) {
  if (!o_.abortOnDecline || preGameStarted_ || (match_.Setup().prepareFlags & 1u) == 0) return;
  std::vector<const Conn*> culprits;
  int declined = 0, late = 0;
  for (const auto& kv : conns_) {
    const Conn& c = *kv.second;
    if (c.superseded || c.spectator || c.sent < kStepPrepare || c.sent >= kStepPreGameFlow) continue;
    if (c.status & kStatusMatchDeclined) {
      culprits.push_back(&c);
      ++declined;
    } else if (!(c.status & kStatusMatchAccepted) && IsSet(c.prepareAt) &&
               now - c.prepareAt >= std::chrono::milliseconds(o_.matchFoundTimeoutMs)) {
      culprits.push_back(&c);
      ++late;
    }
  }
  if (culprits.empty()) return;
  std::string why = "the match-found prompt was ";
  if (declined) why += "declined by";
  if (late) why += std::string(declined ? " and " : "") + "not answered within " +
                   std::to_string(o_.matchFoundTimeoutMs) + " ms by";
  for (const Conn* c : culprits)
    why += " player " + std::to_string(c->slot.gameplayIndex) + " (user " + std::to_string(c->userId) + ")";
  AbortGame(now, culprits, why);
}

// G3b: GameAborted (0x89) to every seated client before its B6: the culprits' ids first (the range the client
// checks: its own id there = no matchmaking restart), then the other seated humans' by gameplay index.
void GmsSession::AbortGame(SteadyClock::time_point now, const std::vector<const Conn*>& culprits,
                           const std::string& why) {
  aborted_ = true;
  abortedAt_ = now;
  abortWhy_ = why;
  gateSince_ = {};
  std::vector<uint64_t> ids;
  for (const Conn* c : culprits)
    if (ids.size() < 10) {
      ids.push_back(c->userId);
      abortCulprits_.push_back(c->userId);
    }
  const uint8_t count = static_cast<uint8_t>(ids.size());
  std::vector<const Conn*> others;
  for (const auto& kv : conns_) {
    const Conn& c = *kv.second;
    if (c.superseded || c.sent < kStepHello || !c.userId) continue;
    if (std::find(culprits.begin(), culprits.end(), &c) == culprits.end()) others.push_back(&c);
  }
  std::sort(others.begin(), others.end(),
            [](const Conn* a, const Conn* b) { return a->slot.gameplayIndex < b->slot.gameplayIndex; });
  for (const Conn* c : others)
    if (ids.size() < 10) ids.push_back(c->userId);
  const Bytes m = BuildGameAborted(match_.Setup().blazeGameId, ids, 0, count);
  int n = 0;
  for (auto& kv : conns_) {
    Conn& c = *kv.second;
    c.due = {};
    c.waitB6 = false;
    if (c.superseded || c.sent < kStepHello || c.sent >= kStepPreGameFlow) continue;
    c.abortSent = true;
    SendRaw(c, m, "GameAborted (game " + std::to_string(match_.Setup().blazeGameId) + ", " + std::to_string(count) +
                      " culprit(s) of " + std::to_string(ids.size()) + " ids, phase 1)");
    ++n;
  }
  Logf("%s    pre-game: %s: the game is ABORTED; GameAborted to %d connection(s), waiting up to %d ms for their 0x40000",
       Clock().c_str(), why.c_str(), n, o_.abortAckMs);
}

// G3b: once every client that got GameAborted answered 0x40000 (or left), or after abortAckMs, the match ends it.
void GmsSession::CheckAbortAcks(SteadyClock::time_point now) {
  if (abortReported_) return;
  int waiting = 0;
  for (const auto& kv : conns_)
    if (kv.second->abortSent && !kv.second->superseded && !kv.second->leftGame) ++waiting;
  const bool late = now - abortedAt_ >= std::chrono::milliseconds(o_.abortAckMs);
  if (waiting && !late) return;
  abortReported_ = true;
  if (waiting)
    Logf("%s    pre-game: %d client(s) didn't answer GameAborted within %d ms; the match ends the game", Clock().c_str(),
         waiting, o_.abortAckMs);
  else
    Logf("%s    pre-game: every client answered GameAborted (0x40000); the match ends the game", Clock().c_str());
  match_.OnGameAborted(abortCulprits_, abortWhy_);
}

// The players without a connection count as loaded once the first connection loads (script B step 28a): status
// | 0x400, progress 1.0, to every connection that has them.
void GmsSession::MarkOthersLoaded() {
  if (othersLoaded_ || others_.empty()) return;
  othersLoaded_ = true;
  for (auto& kv : others_) {
    Other& o = kv.second;
    o.p.status |= kStatusLevelLoaded;
    o.progress = 1.0f;
    char what[96];
    std::snprintf(what, sizeof what, "player %u (no connection) loaded: status 0x%x, progress 1.000",
                  o.p.gameplayIndex, o.p.status);
    for (auto& ckv : conns_) {
      Conn& to = *ckv.second;
      if (to.sent >= kStepHello && !to.superseded && to.known.count(o.p.gameplayIndex))
        SendOtherUpdate(to, o, PlayerStatusEchoMembers(), what);
    }
  }
}

bool GmsSession::SendPlayerJoined(ConnId conn, uint8_t gameplayIndex) {
  auto it = conns_.find(conn);
  if (it == conns_.end() || it->second->sent < kStepHello || it->second->superseded) return false;
  Conn& c = *it->second;
  if (!c.known.insert(gameplayIndex).second) return true;  // it has the player already
  const bool late = c.shaperSelect && c.sent >= kStepPreGameFlow && !c.loading;
  SendRaw(c, BuildPlayerJoined(gameplayIndex),
          "player " + std::to_string(gameplayIndex) + " joined" +
              (late ? " (after B6: its shaper select has no record for it)" : ""));
  return true;
}

bool GmsSession::SendPlayerDeparted(ConnId conn, uint8_t gameplayIndex) {
  auto it = conns_.find(conn);
  if (it == conns_.end() || it->second->sent < kStepHello || it->second->superseded) return false;
  Conn& c = *it->second;
  if (!c.known.erase(gameplayIndex)) return true;  // it doesn't have the player
  SendRaw(c, BuildPlayerDeparted(gameplayIndex), "player " + std::to_string(gameplayIndex) + " departed");
  return true;
}

bool GmsSession::Knows(ConnId conn, uint8_t gameplayIndex) const {
  auto it = conns_.find(conn);
  return it != conns_.end() && it->second->known.count(gameplayIndex) != 0;
}

void GmsSession::Tick() {
  const auto now = SteadyClock::now();
  const auto step = std::chrono::milliseconds(o_.stepMs);
  const auto loadTimeout = std::chrono::milliseconds(o_.loadTimeoutMs);
  match_.Tick(now, *this);
  if (ss_) {
    ss_->Tick(ServerMs(), *this);
    AfterShaperSelect();
  }
  // G3b: a decline of the match-found prompt, or no answer in time, aborts the game; then the clients' 0x40000
  if (!aborted_) CheckMatchFound(now);
  if (aborted_) CheckAbortAcks(now);
  // G3: connections waiting for B6 go together once every human is ready, or after waitPlayersMs
  if (o_.waitForPlayers && IsSet(gateSince_) && !aborted_) {
    std::string missing;
    if (PreGameGateOpen(&missing)) {
      OpenPreGameGate(now, "every human is ready");
    } else if (now - gateSince_ >= std::chrono::milliseconds(o_.waitPlayersMs)) {
      Logf("%s    pre-game: still waiting for%s after %d ms; going on without them", Clock().c_str(), missing.c_str(),
           o_.waitPlayersMs);
      OpenPreGameGate(now, "the wait for the other humans timed out");
    }
  }
  for (auto& kv : conns_) {  // B15: HelloReqs held in a custom game's lobby, answered once it starts
    Conn& c = *kv.second;
    if (!c.helloHeld || c.superseded || match_.HoldHello(c.id, c.heldHello)) continue;
    c.helloHeld = false;
    Logf("%s %8.3f    the match left its lobby: user %llu's held HelloReq answered", Clock().c_str(), c.Secs(),
         static_cast<unsigned long long>(c.heldHello.userId));
    HandleHello(c, c.heldHello, now);
  }
  NameSpectatorsLate();  // B15b
  for (auto& kv : conns_) {
    Conn& c = *kv.second;
    if (c.superseded) continue;  // G3a: replaced by its user's new connection
    // B15b: an observer whose client sent no C8 01 still goes on after the reveal (loadTimeoutMs after it)
    if (c.spectator && c.shaperSelect && c.sent == kStepPreGameFlow && c.revealed && ss_ && ss_->Done(ServerMs()) &&
        now >= c.revealAt + loadTimeout) {
      Logf("%s %8.3f    no C8 01 from the spectator's client within %d ms of the reveal; continuing", Clock().c_str(),
           c.Secs(), o_.loadTimeoutMs);
      c.sent = kStepShaperSelect;
    }
    if (c.sent == kStepLoad && !(o_.echoStatus ? c.allLoaded : c.loaded) && IsSet(c.loadDeadline) &&
        now >= c.loadDeadline && !IsSet(c.due) && o_.stopAfter >= kStepDone) {
      Logf("%s %8.3f    no %s from the client within %d ms of the load; continuing", Clock().c_str(), c.Secs(),
           o_.echoStatus ? "C0 04" : "status 0x401", o_.loadTimeoutMs);
      c.due = now;
    }
    // D3-core: after the reveal, C0 01 step-ms after the client's C0 04 (or loadTimeoutMs without it), once
    // RevealTime is over
    if (c.sent == kStepShaperSelect && c.revealed && !IsSet(c.due) && o_.stopAfter >= kStepLoad &&
        ss_->Done(ServerMs())) {
      if (c.picksAcked) {
        c.due = now + step;
      } else if (now >= c.revealAt + loadTimeout) {
        Logf("%s %8.3f    no C0 04 from the client within %d ms of the reveal; continuing", Clock().c_str(), c.Secs(),
             o_.loadTimeoutMs);
        c.due = now;
      }
    }
    if (aborted_ && c.sent >= kStepHello) c.due = {};  // G3b: no step after GameAborted
    if (!IsSet(c.due) || now < c.due) continue;
    c.due = {};
    int next = c.sent + 1;
    if (next == kStepShaperSelect && !c.shaperSelect) next = kStepLoad;  // D2b-static's script: C0 01 after B6
    if (next == kStepPreGameFlow && o_.waitForPlayers && !c.b6Go) {
      // G3: B6 only once every human is ready (the gate above releases every waiting connection together)
      std::string missing;
      if (!PreGameGateOpen(&missing)) {
        c.waitB6 = true;
        if (!IsSet(gateSince_)) gateSince_ = now;
        Logf("%s %8.3f    pre-game: player %u is ready; EnterPreGameFlow waits for%s", Clock().c_str(), c.Secs(),
             c.slot.gameplayIndex, missing.c_str());
        continue;
      }
      c.b6Go = true;
    }
    if (next == kStepPrepare) {
      c.prepareAt = now;
      SendStep(c, kStepPrepare, BuildGamePrepareForStart(match_.Setup()));
    } else if (next == kStepPreGameFlow) {
      preGameStarted_ = true;
      SendStep(c, kStepPreGameFlow, BuildEnterPreGameFlow());
      if (c.status & kStatusSkipShaperSelect) {
        // status 0x10: PreDungeonState_OnEnter (0x006AA720) goes to loading by itself; C0 01 would assert
        c.sent = kStepLoad;
        c.loading = c.loadImplicit = true;
        c.loadDeadline = now + loadTimeout;
        Logf("%s %8.3f    step load implicit (status 0x10: the client loads on EnterPreGameFlow)", Clock().c_str(),
             c.Secs());
        Point(c, SessionPoint::kLoad);
        MarkOthersLoaded();
      } else if (c.shaperSelect) {
        // D3-core: the client opens shaper select and sends C8 01, C8 02; its C8 02 starts the step. G3a: its records
        // are the players it has now (B6 creates them), so shaper select sends it only theirs
        if (ss_) ss_->SetKnown(c.id, std::vector<uint8_t>(c.known.begin(), c.known.end()));
      } else if (o_.stopAfter >= kStepLoad) {
        c.due = now + step;
      }
    } else if (next == kStepLoad) {
      SendStep(c, kStepLoad, BuildPreGame(kPreGameLoad));
      if (ss_ && c.shaperSelect) ss_->Close(c.id);  // TransitionToLoading destroys the client's shaper select
      c.loading = true;
      c.loadDeadline = now + loadTimeout;
      MarkOthersLoaded();
      if ((o_.echoStatus ? c.allLoaded : c.loaded) && o_.stopAfter >= kStepDone) c.due = now + step;
    } else if (next == kStepDone) {
      SendStep(c, kStepDone, BuildPreGame(kPreGameDone));
      if (o_.stopAfter >= kStepEnterGame) c.due = now + step;
    } else if (next == kStepEnterGame) {
      SendStep(c, kStepEnterGame, BuildEnterGame());
    }
  }
  if (now >= nextReport_) {
    // run_session.ps1 stops gms_script with a kill, so the state line can't wait for the end: it is printed when
    // something besides the ClockSync count changed, and every 30 s (the summary takes the last one).
    for (auto& kv : conns_) Report(*kv.second, false);
    nextReport_ = now + std::chrono::seconds(5);
  }
}

// the strict check (core/protocol/check.h) of one client message. True = the message is not to be handled (dropped,
// or the connection was kicked and `c` no longer exists).
bool GmsSession::Screen(Conn& c, uint8_t b, const uint8_t* body, size_t nb) {
  if (!boundsSet_) {
    bounds_ = check::BoundsForLevel(match_.Setup().level);
    boundsSet_ = true;
  }
  check::Result r;
  if (b != kGmsHelloReq && c.sent < kStepHello) {
    r.verdict = check::Verdict::kInvalid;
    r.reason = std::string(GmsName(b)) + " before the HelloReq was answered";
  } else {
    r = check::CheckFormat(b, body, nb, bounds_);
  }
  if (r.ok() && c.sent >= kStepHello) {
    if (check::IWorld* w = match_.CheckWorld()) r = check::CheckWorld(b, body, nb, c.id, *w);
  }
  const double nowMs = std::chrono::duration<double, std::milli>(SteadyClock::now() - serverStart_).count();
  const bool kick = guard_.Note(c.id, b, r.verdict, nowMs);
  if (r.ok()) return false;
  const bool drop = o_.check.mode == 1;
  if (guard_.ShouldLog())
    Logf("%s %8.3f    check: %02x %s (%zu B) %s, %s: %s; %s", Clock().c_str(), c.Secs(), b, GmsName(b), nb,
         check::VerdictName(r.verdict), drop ? "dropped" : "passed (strict_check 2)", r.reason.c_str(),
         Hex(body, nb, 48).c_str());
  if (drop && r.verdict == check::Verdict::kStale) {
    // a normal client's race: answer it as the server's refusal would, so its client clears its pending command
    if (b == kGmsActionCommandMsgs) {
      ActionCommand cmd;
      std::string error;
      if (ParseActionCommand(body, nb, &cmd, &error) && cmd.seq)
        net_.Send(c.id, BuildActionCommandFailed(c.slot.gameplayIndex, cmd.seq, cmd.type, cmd.guid, 0));
    } else if (b == kGmsItemTransaction) {
      ItemTransactionRequest t;
      if (ParseItemTransaction(body, nb, &t))
        net_.Send(c.id, BuildItemTransactionResult(c.slot.gameplayIndex, t.type, t.item, t.slot, false));
    }
  }
  if (kick && drop) {
    Kick(c.id, "too many bad messages");
    return true;
  }
  return drop;
}

void GmsSession::Kick(ConnId conn, const char* why) {
  auto it = conns_.find(conn);
  if (it == conns_.end()) return;
  Logf("%s %8.3f    check: connection %s kicked: %s (%d malformed or invalid within %d ms); %s", Clock().c_str(),
       it->second->Secs(), it->second->addr.c_str(), why, o_.check.kickCount, o_.check.kickWindowMs,
       guard_.Total().Describe().c_str());
  net_.Close(conn);
  guard_.Forget(conn);
  OnDisconnected(conn);  // RakNet reports no disconnection for a connection closed on this side
}

bool GmsSession::OnOtherPacket(ConnId conn, const uint8_t* data, size_t n) {
  if (!o_.check.mode) return true;
  const uint8_t id = n ? data[0] : 0;
  const bool known = conns_.count(conn) != 0;
  const double nowMs = std::chrono::duration<double, std::milli>(SteadyClock::now() - serverStart_).count();
  const bool kick = guard_.NoteOther(known ? conn : 0, id, nowMs) && known;
  const bool log = guard_.ShouldLog();
  if (kick && o_.check.mode == 1) Kick(conn, "too many unexpected RakNet packets");
  return log;
}

void GmsSession::Finish() {
  if (o_.check.mode) Logf("%s check totals: %s", Clock().c_str(), guard_.Total().Describe().c_str());  
  for (auto& kv : conns_) Report(*kv.second, true);
  conns_.clear();
}

bool GmsSession::Send(ConnId conn, const Bytes& msg, const std::string& what) {
  auto it = conns_.find(conn);
  if (it == conns_.end() || msg.empty() || it->second->superseded) {
    Logf("%s    -> %s for an unknown%s connection; dropped: %s", Clock().c_str(), Hex(msg, 16).c_str(),
         it != conns_.end() && it->second->superseded ? " (superseded)" : "", what.c_str());
    return false;
  }
  return SendRaw(*it->second, msg, what);
}

bool GmsSession::SendQuiet(ConnId conn, const Bytes& msg) {
  auto it = conns_.find(conn);
  if (it == conns_.end() || msg.empty() || it->second->superseded) {
    Logf("%s    -> %s for an unknown%s connection; dropped (quiet send)", Clock().c_str(), Hex(msg, 16).c_str(),
         it != conns_.end() && it->second->superseded ? " (superseded)" : "");
    return false;
  }
  return net_.Send(conn, msg);
}

std::string GmsSession::State(ConnId conn) const {
  auto it = conns_.find(conn);
  return it == conns_.end() ? std::string() : StateOf(*it->second);
}

void GmsSession::SendStep(Conn& c, int step, const Bytes& m) {
  SendRaw(c, m, std::string("step ") + SessionStepName(step));
  c.sent = step;
  if (step == kStepConnected) return;
  Point(c, PointOfStep(step));
}

bool GmsSession::SendRaw(Conn& c, const Bytes& m, const std::string& what) {
  const bool ok = net_.Send(c.id, m);
  Logf("%s %8.3f    -> %02x %s (%zu B) %s: %s", Clock().c_str(), c.Secs(), m[0], GmsName(m[0]), m.size(),
       Hex(m, 64).c_str(), ok ? what.c_str() : (what + " SEND FAILED").c_str());
  return ok;
}

void GmsSession::Point(Conn& c, SessionPoint p) { match_.OnPoint(c.id, p, *this); }

void GmsSession::LogClientMessage(Conn& c, uint8_t b, const uint8_t* body, size_t n) {
  // ClockSync bodies differ every time: print the first few by wire byte (the answers log every 25th). Everything
  // else: the first 3 of each distinct body; repeats are counted and reported every 5 s.
  const std::string key = Hex(&b, 1) + (b == kGmsClockSync ? std::string() : " " + Hex(body, n, 64));
  const int limit = b == kGmsClockSync ? 6 : 3;
  // at most kMaxPrintedKeys distinct bodies are remembered per connection (a client sending ever new bodies
  // would otherwise grow the map and the log without bound); the rest are counted under one key
  constexpr size_t kMaxPrintedKeys = 4096;
  if (c.printed.size() >= kMaxPrintedKeys && c.printed.find(key) == c.printed.end()) {
    ++c.suppressed["(new bodies beyond the first 4096)"];
    return;
  }
  if (++c.printed[key] > limit) {
    if (b != kGmsClockSync) ++c.suppressed[key];
    return;
  }
  char buf[160];
  buf[0] = 0;
  if (b == kGmsPlayerStatusUpdate && n == 8) {
    std::snprintf(buf, sizeof buf, "flags 0x%x, progress %.3f", GetU32LE(body), GetF32LE(body + 4));
  } else if (b == kGmsClockSync && n == 8) {
    std::snprintf(buf, sizeof buf, "client %lld ms", static_cast<long long>(GetI64LE(body)));
  } else if (b == kGmsBattlegroundsPreGameMsgs && n >= 1) {
    std::snprintf(buf, sizeof buf, "subtype %u%s", body[0], body[0] == kPreGameAllLoaded ? " (ready)" : "");
  } else if (b == kGmsFriendInfo && n == 2) {
    std::snprintf(buf, sizeof buf, "friend mask 0x%04x", body[0] | body[1] << 8);
  } else if (b == kGmsShaperSelectMsgs && n >= 1) {
    std::snprintf(buf, sizeof buf, "subtype %u", body[0]);
  } else if (b == kGmsHelloReq && n == 16) {
    std::snprintf(buf, sizeof buf, "user id %llu, playgroup id %llu",
                  static_cast<unsigned long long>(GetU64LE(body)), static_cast<unsigned long long>(GetU64LE(body + 8)));
  }
  Logf("%s %8.3f <- %02x %s (%zu B body) %s%s%s", Clock().c_str(), c.Secs(), b, GmsName(b), n, Hex(body, n, 64).c_str(),
       buf[0] ? ": " : "", buf);
  if (c.printed[key] == limit) Logf("    (further repeats of this message are counted, not printed)");
}

void GmsSession::ReportSuppressed(Conn& c) {
  if (c.suppressed.empty()) return;
  std::string s;
  for (const auto& kv : c.suppressed) s += " " + kv.first.substr(0, 20) + " x" + std::to_string(kv.second) + ";";
  Logf("%s %8.3f    repeats since the last report:%s", Clock().c_str(), c.Secs(), s.c_str());
  c.suppressed.clear();
}

std::string GmsSession::StateOf(const Conn& c) const {
  std::string steps, got;
  for (int i = 0; i <= c.sent && i < kNumSessionSteps; ++i) {
    if (i == kStepShaperSelect && !c.shaperSelect) continue;
    steps += std::string(i ? ">" : "") + kStepNames[i] + (i == kStepLoad && c.loadImplicit ? "(implicit)" : "");
  }
  if (c.helloRefused) steps += "; HelloReq refused by the match";
  if (c.helloHeld) steps += "; HelloReq held (the match waits in its lobby)";
  if (c.spectator) steps += "; spectator";  // B15b
  if (c.superseded) steps += "; superseded by its user's new connection";
  if (o_.echoStatus) {
    char buf[64];
    std::snprintf(buf, sizeof buf, "; status 0x%x; all loaded (C0 04) %s", c.status, c.allLoaded ? "yes" : "no");
    steps += buf;
  }
  if (c.shaperSelect && ss_) steps += "; " + ss_->Describe();
  const std::string note = match_.StateNote(c.id);
  if (!note.empty()) steps += "; " + note;
  for (const auto& kv : c.total)
    if (kv.first != "ClockSync") got += " " + kv.first + " x" + std::to_string(kv.second) + ";";
  return "steps sent " + (steps.empty() ? std::string("none") : steps) + "; client loaded (0x401) " +
         (c.loaded ? "yes" : "no") + "; client sent:" + (got.empty() ? std::string(" nothing") : got);
}

void GmsSession::Report(Conn& c, bool force) {
  ReportSuppressed(c);
  const std::string state = StateOf(c);
  const auto now = SteadyClock::now();
  if (!force && state == c.lastState && now - c.lastStateAt < std::chrono::seconds(30)) return;
  c.lastState = state;
  c.lastStateAt = now;
  Logf("session %s: %s ClockSync x%d (all answered)", c.addr.c_str(), state.c_str(), c.clockAnswers);
}

}  // namespace gms
