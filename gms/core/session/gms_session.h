// GmsSession (gms/core, G1): the GMS side of a client connection, from Connected to EnterGame, as
// docs/GMS_PROTOCOL.md derives it ("Session after HelloReq", "Player updates and the pre-game") and D2b-live played it
// with the live client. Moved here from raknet_probe.cpp (ADR-2 decision 5); the match behind it is an IMatch.
//
// The steps, in order (SessionOptions::stopAfter names the last one sent):
//   connected    -> 84                      on ID_NEW_INCOMING_CONNECTION (B9)
//   hello        -> 82 HelloPlayer          on HelloReq, if the match accepts the user
//                   [+ A9 player update: status, team, mpNounDef, online id = HelloReq's user id] (--player-update)
//   prepare      -> B5 GamePrepareForStart  step-ms later (must arrive in kGameStateLobby)
//                <- C7 {i64 client ms}, answered with C7 {i64 echo, i64 server ms > 0}
//   pregameflow  -> B6                      step-ms after the 5th ClockSync answer (the reader asserts a primed
//                                           time translator)
//   shaperselect    C8 ...                  D3-core, only with SessionOptions::shaperSelect and a match that sets it
//                                           up (IMatch::ShaperSelectSetup): from the client's C8 02 on, its C8
//                                           messages go to the match-wide ShaperSelect (session/shaper_select.h);
//                                           at the reveal every player's status gets 0x100 (IMatch::OnShaperPicks,
//                                           then one A9 per player to every client: mStatus, mRole, mpNounDef)
//   load         -> C0 01                   step-ms later (after shaper select: step-ms after the client's C0 04 for
//                                           the reveal and RevealTime): loading screen and Client_LoadLevel. Not sent
//                                           with status 0x10 (skip shaper): EnterPreGameFlow then loads at once, and a
//                                           second TransitionToLoading asserts (the step shows as "load(implicit)").
//                                           Players without a connection (IMatch::OtherPlayers) count as loaded here
//                                           (status | 0x400, progress 1.0, to every client)
//   done         -> C0 03                   step-ms after the load is reported: with echoStatus the client's C0 04
//                                           ("all players loaded", sent once its echoed status has 0x400), else its
//                                           first PlayerStatusUpdate with 0x400; or after loadTimeoutMs without one
//   entergame    -> B7 00                   step-ms later: Client_StartGame, kGameStateDungeon
// The status model (echoStatus, D2c): status |= every flag the client reports in PlayerStatusUpdate; the changed
// word is echoed with its progress as A9 idx 13 <u32 status> <f32 progress>. The client's pre-game gates read only
// the echoed word, so this is what ends its 0x401 flood. Every ClockSync is answered (server clock = ms since the
// server started + 1000). At each SessionPoint the match may send (IMatch::OnPoint).
// Players without a connection (D3-core, IMatch::OtherPlayers: GMS-scripted players, bots) get a HelloPlayer record
// (type 1) and a first update (status, team, bot, online id) to every connection after its own.
// Without shaperSelect and without other players every byte and log line is the same as before D3-core (G1-D2g).
// Several humans (G3a; docs/GMS_PROTOCOL.md *Several humans (G3a)*):
//   - a returning user (a user id that had a slot) keeps its gameplay index and team (RejoinMap, session/rejoin.h);
//     the match hears of it (IMatch::OnRejoin), its earlier connection, if still there, is superseded (nothing more is
//     sent to it, its messages are dropped, its end is no departure);
//   - a human whose connection ended keeps its last status plus 0x4 (kStatusGone), so the other clients' pre-game
//     gates leave it out: with the reveal, or at once to the clients that already have its status (the reveal's);
//   - every connection in the pre-game gets the reveal's updates, also one that comes after the reveal (a late or
//     returning player), and continues to C0 01 like the others;
//   - the session keeps which players each client has (HelloPlayer, then SendPlayerJoined/SendPlayerDeparted, which
//     G3 drives): updates and shaper select records go only to clients that have the player (ShaperSelect::SetKnown
//     with the players at B6, when the client creates its records).
// Several humans live (G3; docs/GMS_PROTOCOL.md *Several humans live (G3)*), each switchable in SessionOptions:
//   - nameHumans: a client's HelloPlayer also names every other seated human (type 1; a departed one too, which keeps
//     its 0x4), each followed by that player's update (status, team, mpNounDef unless shaper select, online id); the
//     clients already seated get PlayerJoined and the newcomer's first update. No PlayerDeparted: a departed human
//     stays in every client's simulator with 0x4 (its creature stays in the world), so a rejoin is a status change;
//   - every status change (the echo, a departure's 0x4) goes to every client that has the player, so each client's
//     0x100 and 0x400 gates and MatchFoundUI's accept count see the other humans;
//   - waitForPlayers: EnterPreGameFlow (B6) waits until every seated human and every expected one
//     (IMatch::ExpectedHumans: createGame's roster) is ready (5 ClockSyncs; with connect type 1 also its answer to the
//     match-found prompt, 0x10000 or 0x20000), or waitPlayersMs; then every waiting connection gets B6 together, so
//     every client creates its shaper select records with all humans.
// The match-found prompt's decline and timeout (G3b; docs/GMS_PROTOCOL.md *Several humans, the rest (G3b)*): with connect
//   type 1 a human's 0x20000, or no answer within matchFoundTimeoutMs of its GamePrepareForStart, aborts the game
//   before any B6: GameAborted (0x89) to every client in the lobby (the culprits' ids first: the clients that aren't
//   culprits restart matchmaking by themselves), then IMatch::OnGameAborted once each answered 0x40000 or after
//   abortAckMs; the host then ends the game.
//
// A custom game's lobby (B15; docs/HOST_CONTROL.md *Custom games on the match host (B15)*): while IMatch::HoldHello is
//   true the session keeps a connection's HelloReq unanswered (state line "HelloReq held") and asks again every Tick;
//   once false it answers it as if it had just come (OnHelloReq with the final slot, HelloPlayer, ...).
// Spectators (B15b; docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*): a connection whose slot the match put
//   at a spectator's gameplay index (10-14, IsSpectatorIndex) is an observer. Its HelloPlayer names itself (type 0, so
//   its client puts it into its spectator map) and every player; its first update carries the observer bit 0x20 (no
//   mpNounDef), which makes its client's shaper select the observer's and its EnterGame kGameStateSpectator. It goes
//   through the pre-game like a player: with shaper select its C8 01 (it sends no C8 02) starts ShaperSelect's observer
//   (every record of both teams), at the reveal its status gets 0x100 (the gates of every client that has it), its 0x401
//   is echoed. nameSpectators: the players' clients get it too (HelloPlayer/PlayerJoined; their loading screen's 15
//   slots and gates count spectators), but a spectator who comes while a client is between B6 and EnterGame is named
//   to that client only at its EnterGame, so a late spectator never holds up the others' 0x100/0x400 gates. A spectator
//   who comes after the reveal skips shaper select (0x10). A spectator's departure is PlayerDeparted (0x87) to every
//   client that has it (no 0x4 is kept); the same user later keeps its gameplay index (RejoinMap). Spectators never
//   answer the match-found prompt (no prompt wait, never a culprit).
// Log lines (common/log.h) keep raknet_probe's format; the per-connection state line "session <addr>: ..." is what
// run_session.ps1's 'gms probe' summary line shows. Not thread-safe: one thread calls everything (ADR-2's engine
// thread).
#pragma once

#include <functional>
#include <map>
#include <memory>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/endpoint.h"
#include "core/protocol/check.h"
#include "core/session/match.h"
#include "core/session/rejoin.h"

namespace gms {

enum SessionStep : int {
  kStepConnected = 0,
  kStepHello,
  kStepPrepare,
  kStepPreGameFlow,
  kStepShaperSelect,  // D3-core; left out of the state line when the connection skips shaper select
  kStepLoad,
  kStepDone,
  kStepEnterGame,
  kNumSessionSteps
};
const char* SessionStepName(int step);
int ParseSessionStep(const std::string& name);  // -1 if unknown

struct SessionOptions {
  int stopAfter = kStepEnterGame;  // the last step sent
  int stepMs = 1000;               // delay before each timed step
  int loadTimeoutMs = 60000;       // continue with `done` after this long without the load report
  bool playerUpdate = false;       // A9 after HelloPlayer: status, team, mpNounDef, online id
  bool skipShaper = false;         // the GMS's status bit 0x10 in that update (implies playerUpdate)
  bool echoStatus = false;         // D2c's status model
  bool shaperSelect = false;       // D3-core: run shaper select (implies playerUpdate and echoStatus; skipShaper
                                   // then only counts if the match sets up no shaper select)
  // HelloPlayer's address record (the client ignores it; D2b-live sent 127.0.0.1:10042)
  uint32_t helloIpv4 = 0x7F000001;
  uint16_t helloPort = 10042;
  // G3: several humans (see above). With one human nothing changes: its B6 is due step-ms after its 5th ClockSync.
  bool nameHumans = true;      // HelloPlayer/PlayerJoined and the first updates of the other humans
  bool waitForPlayers = true;  // B6 waits for every seated and expected human
  int waitPlayersMs = 60000;   // ... at most this long after the first connection was ready
  // G3b: the match-found prompt (connect type 1). A human's decline (0x20000), or no answer this long after its
  // GamePrepareForStart (the client's countdown, BattlegroundsGameplayTuning.WaitingForPlayersTimeout 20 s, plus a
  // margin), aborts the game before EnterPreGameFlow: GameAborted (0x89) to every client in the lobby, then
  // IMatch::OnGameAborted once each has answered 0x40000 or after abortAckMs (WaitForGameAbortAckTimeout 5 s).
  bool abortOnDecline = true;
  int matchFoundTimeoutMs = 23000;
  int abortAckMs = 5000;
  // B15b: the players' clients get the spectators too (HelloPlayer, PlayerJoined, their statuses); a spectator's client
  // always gets every player
  bool nameSpectators = true;
  // the strict message check (core/protocol/check.h): mode 1 drops a bad message (the default), 2 only counts and
  // logs it, 0 is off; kickCount malformed or invalid messages within kickWindowMs close the connection
  check::GuardOptions check;
};

class GmsSession final : public ISender {
 public:
  // `serverStart` is the zero of the ClockSync server clock.
  GmsSession(ITransport& net, IMatch& match, const SessionOptions& options, SteadyClock::time_point serverStart);
  ~GmsSession();
  // The ClockSync server clock. Everything the match stamps with its simulator's game clock (cooldown starts and ends,
  // death timers, ...) goes through the client's TimeTranslator, which maps the ClockSync clock to its own, so the
  // answers must carry that same clock. A non-positive value falls back to ms since `serverStart` + 1000.
  void SetServerClock(std::function<int64_t()> clock) { serverClock_ = std::move(clock); }

  void OnConnected(ConnId conn, const std::string& addr);
  void OnDisconnected(ConnId conn);
  // A GMS message from the client: data[0] is the wire byte (>= 0x81).
  void OnMessage(ConnId conn, const uint8_t* data, size_t n);
  // Timed steps, delayed sends and the periodic state line; call every few ms.
  void Tick();
  // The last state line of every connection; forgets them.
  void Finish();

  // ISender: what the match sends, logged as "-> xx Name (n B) hex: what".
  bool Send(ConnId conn, const Bytes& msg, const std::string& what) override;
  // Sent like Send, but not logged (an unknown connection still is).
  bool SendQuiet(ConnId conn, const Bytes& msg) override;

  const SessionOptions& Options() const { return o_; }
  size_t NumConnections() const { return conns_.size(); }
  // The connection's state, as the state line shows it after "session <addr>: " (empty if unknown).
  std::string State(ConnId conn) const;
  // The match's shaper select (D3-core); nullptr until the first HelloReq, or without one.
  const ShaperSelect* Shapers() const { return ss_.get(); }

  // G3a: the bookkeeping of which players a client has (its simulator's players). HelloPlayer names the client itself
  // and the players without a connection; PlayerJoined (0x86) adds one, PlayerDeparted (0x87) removes one. G3 decides
  // when to send them (the other humans); the session only sends them and keeps the record. False for an unknown or
  // superseded connection, or before its HelloPlayer; a PlayerJoined for a player it has already sends nothing.
  // A PlayerJoined after the client's B6 doesn't give it a shaper select record for that player.
  bool SendPlayerJoined(ConnId conn, uint8_t gameplayIndex);
  bool SendPlayerDeparted(ConnId conn, uint8_t gameplayIndex);
  bool Knows(ConnId conn, uint8_t gameplayIndex) const;
  // a RakNet packet other than a GMS message, a connection or a disconnection (PumpEndpoint's kOther): counted as
  // bad (a connected client's towards its kick). True if it is to be logged (the check's log limit).
  bool OnOtherPacket(ConnId conn, const uint8_t* data, size_t n);
  // the check's counts since the start, and since the last TakeCheckWindow (the host's `net:` line)
  const check::Stats& CheckTotals() const { return guard_.Total(); }
  check::Stats TakeCheckWindow() { return guard_.TakeWindow(); }
  // G3a: every user's slot, for a rejoin.
  const RejoinMap& Seats() const { return seats_; }

 private:
  struct Conn;
  struct Other {
    OtherPlayer p;
    float progress = 0.0f;
  };
  // G3a: a human whose connection ended: its last status (with 0x4) and progress, by gameplay index. G3: `seated` = it
  // had a connection (not a configured human who never connected), with its slot and user id, so a client seated
  // later still gets its record and update.
  struct Departed {
    uint32_t status = kStatusGone;
    float progress = 0.0f;
    bool seated = false;
    PlayerSlot slot;
    uint64_t userId = 0;
  };
  void HandleHello(Conn& c, const HelloReq& req, SteadyClock::time_point now);  // B15: also a held one
  void SendStep(Conn& c, int step, const Bytes& m);
  bool SendRaw(Conn& c, const Bytes& m, const std::string& what);
  void Point(Conn& c, SessionPoint p);
  void SendInitialPlayerUpdate(Conn& c);
  void SendPlayerUpdate(Conn& c, const Bytes& own, const std::vector<int>& members, const std::string& what);
  void SendPlayerUpdateFor(Conn& to, ConnId owner, uint8_t index, const Bytes& own, const std::vector<int>& members,
                           const std::string& what);
  void SendOtherUpdate(Conn& to, const Other& o, const std::vector<int>& members, const std::string& what);
  void SetUpShaperSelect(Conn& c);
  void Rejoined(Conn& c, const RejoinMap::Admission& a);
  void AfterShaperSelect();
  uint32_t RevealStatus(uint8_t gameplayIndex);
  void SendReveal(Conn& to, const std::vector<ShaperPick>& picks, const std::vector<ConnId>& owners);
  void PlayerGone(const Departed& d);
  void MarkOthersLoaded();
  // G3
  void NameHumans(Conn& c);
  void SendHumanUpdate(Conn& to, const Conn& player, bool withStatus, const std::string& what);
  void SendStatusToOthers(const Conn& c, const Bytes& own, const std::string& what);
  bool ReadyForPreGame(const Conn& c, bool prompt) const;
  bool PreGameGateOpen(std::string* missing);
  void OpenPreGameGate(SteadyClock::time_point now, const char* why);
  // B15b: spectators
  bool Names(const Conn& to, const Conn& player) const;  // whether `to`'s client gets a record of `player`
  static bool InGates(const Conn& c);                    // between its B6 and its EnterGame (the pre-game gates)
  void NameSpectatorsLate();
  void MarkSpectatorsPicked();
  void SpectatorGone(uint8_t gameplayIndex);
  // G3b: the match-found prompt's decline and timeout
  void CheckMatchFound(SteadyClock::time_point now);
  void AbortGame(SteadyClock::time_point now, const std::vector<const Conn*>& culprits, const std::string& why);
  void CheckAbortAcks(SteadyClock::time_point now);
  Conn* ConnOfIndex(uint8_t gameplayIndex);
  void LogClientMessage(Conn& c, uint8_t b, const uint8_t* body, size_t n);
  bool Screen(Conn& c, uint8_t b, const uint8_t* body, size_t nb);
  void Kick(ConnId conn, const char* why);
  void ReportSuppressed(Conn& c);
  void Report(Conn& c, bool force);
  std::string StateOf(const Conn& c) const;
  int64_t ServerMs() const;

  ITransport& net_;
  IMatch& match_;
  const SessionOptions o_;
  SteadyClock::time_point serverStart_;
  std::function<int64_t()> serverClock_;
  SteadyClock::time_point nextReport_{};
  std::map<ConnId, std::unique_ptr<Conn>> conns_;
  // D3-core: match-wide
  std::map<uint8_t, Other> others_;   // players without a connection, by gameplay index
  std::unique_ptr<ShaperSelect> ss_;  // the match's shaper select (SessionOptions::shaperSelect)
  bool ssAsked_ = false;              // IMatch::ShaperSelectSetup was asked
  bool revealed_ = false;             // the reveal's updates went out
  bool othersLoaded_ = false;         // the other players' 0x400 went out
  // G3a: several humans
  RejoinMap seats_;                       // user id -> slot
  std::map<uint8_t, Departed> departed_;  // humans whose connection ended (or who never connected), by index
  std::set<uint8_t> picked_;              // the indices IMatch::OnShaperPicks has had
  // G3: the pre-game gate (waitForPlayers)
  SteadyClock::time_point gateSince_{};   // the first connection waiting for B6 (epoch = none waits)
  bool preGameStarted_ = false;           // a B6 went out (later connections don't wait for absent humans)
  // G3b: the game aborted at the match-found prompt
  bool aborted_ = false;
  bool abortReported_ = false;            // IMatch::OnGameAborted was called
  SteadyClock::time_point abortedAt_{};
  std::vector<uint64_t> abortCulprits_;
  std::string abortWhy_;
  // the strict check
  check::Guard guard_;
  check::Bounds bounds_;
  bool boundsSet_ = false;
  unsigned boundsGeneration_ = 0;  // MAPED4: check::LevelDataGeneration() when bounds_ was taken

 public:
  // G3b: the game was aborted at the match-found prompt (no connection gets EnterPreGameFlow any more).
  bool Aborted() const { return aborted_; }
};

}  // namespace gms
