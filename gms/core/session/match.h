// IMatch: what the GMS session asks of the match it serves (gms/core, G1; ADR-2 decision 5). The session owns the
// per-connection protocol (Connected, HelloReq, HelloPlayer, ClockSync, the pre-game steps, the status model); the
// match owns the game: the level, who may join and with which slot, team and shaper, and the world the clients see.
//
// Implementations:
//   gms/script  ScriptedMatch: options and injected messages (D2b-live's script A, D2e's AssetData-built world)
//   gms/engine  ServerMatch (gms_host.cpp, G2): the host's server simulator (HelloReq -> GameSimulator_AddPlayer
//               0x008F3E20, status and team in the server's cLabsPlayer; since D2f the world and the shaper from the
//               simulator at SessionPoint::kLoaded and the player updates from the server's cLabsPlayer)
// D3-core added shaper select: OtherPlayers (scripted players and bots), ShaperSelectSetup, OnShaperPicks; their
// defaults (none, no shaper select, nothing) keep a match that doesn't implement them on script A's path. G3a added
// OnRejoin (a returning user keeps its slot; session/rejoin.h).
#pragma once

#include <chrono>
#include <cstdint>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/endpoint.h"
#include "core/protocol/messages.h"
#include "core/session/shaper_select.h"

namespace gms {

using SteadyClock = std::chrono::steady_clock;

// The moments of the session script at which the match may send (raknet_probe's --inject points, D2e):
// right after a step's message (hello, prepare, pregameflow, load, done, entergame), after the 5th ClockSync answer
// (clock5), and when the client first reports the level loaded, status 0x401 (loaded). D2e: the world and the
// player's shaper must go out at `loaded`, after the level load and before EnterGame. D3-core: `shaperselect` once the
// session answered the client's C8 02 (list, records, phase 1), `picked` after the reveal's player updates.
enum class SessionPoint {
  kHello, kPrepare, kClock5, kPreGameFlow, kShaperSelect, kPicked, kLoad, kLoaded, kDone, kEnterGame
};
const char* SessionPointName(SessionPoint p);
bool ParseSessionPoint(const std::string& name, SessionPoint* out);

// The session's sending side as the match sees it: a message to one connection, logged like the session's own.
class ISender {
 public:
  virtual bool Send(ConnId conn, const Bytes& msg, const std::string& what) = 0;
  // The same without a log line (D2g-live: the per-tick updates, which the match counts and logs per second itself).
  virtual bool SendQuiet(ConnId conn, const Bytes& msg) { return Send(conn, msg, std::string()); }

 protected:
  ~ISender() = default;
};

// What HelloPlayer and the first player update carry for a connection.
struct PlayerSlot {
  uint8_t gameplayIndex = 0;
  uint8_t team = kTeamBlue;
  uint32_t noun = kNounAlpha;  // mpNounDef; 0 = leave it out (with shaper select the session leaves it out anyway)
};

// D3-core: a player without a connection (a GMS-scripted player or a bot). Every client must know it before B6
// (HelloPlayer record type 1, then a LabsPlayerGlobalUpdate with mStatus, mTeam, mbIsBot and mPlayerOnlineId; GMS_PROTOCOL.md
// *1 to 10 players*). The session keeps its status from then on: | 0x100 at the reveal, | 0x400 (progress 1.0) at the
// load, each change sent to every client.
struct OtherPlayer {
  uint8_t gameplayIndex = 1;
  uint8_t team = kTeamBlue;
  uint64_t onlineId = 1;  // 1-10: LabsPlayer_HandlePlayerOnlineId names it "BOT n" (0 asserts)
  bool isBot = true;
  uint32_t status = kStatusMatchAccepted;  // 0x10000: the match-found count reads it
};

class IMatch {
 public:
  virtual ~IMatch() = default;

  // GamePrepareForStart's values (level, seed, flags).
  virtual GameSetup Setup() = 0;

  // A RakNet connection arrived (before Connected is sent).
  virtual void OnConnected(ConnId conn, const std::string& addr) { (void)conn, (void)addr; }

  // HelloReq: true lets the user in with `slot` filled in; false drops it (no HelloPlayer; an unknown user, ADR-2's
  // roster check, C1/G3). G3a: for a user who had a slot before, `slot` comes pre-filled with it, and the session keeps
  // that gameplay index and team whatever the match writes (RejoinMap; then OnRejoin).
  virtual bool OnHelloReq(ConnId conn, const HelloReq& req, PlayerSlot* slot) = 0;

  // B15: true while the match waits in a custom game's lobby (the client's custom lobby; dgserver's startGame ends it):
  // the session keeps the HelloReq without an answer (no HelloPlayer yet, so a player may still change teams or become
  // a spectator) and asks again every Tick; once false it handles the HelloReq as if it had just come (OnHelloReq with
  // the final slot). Default: never.
  virtual bool HoldHello(ConnId conn, const HelloReq& req) { (void)conn, (void)req; return false; }

  // G3a: the accepted HelloReq on `conn` is a user coming back (RejoinMap): `slot` is its earlier gameplay index and
  // team, `previous` its earlier connection (ended, or superseded now: the session sends that one nothing more). Called
  // after OnHelloReq, before HelloPlayer. The host moves its own per-player state to `conn` (its server player, and its
  // surrender::SurrenderVote entry: AddPlayer with the same index, then OnConnection(conn, true)); G3 and D8b wire it.
  virtual void OnRejoin(ConnId conn, ConnId previous, const PlayerSlot& slot) { (void)conn, (void)previous, (void)slot; }

  // G3: the user ids of the humans the match expects (createGame's roster), connected or not. With
  // SessionOptions::waitForPlayers no connection gets EnterPreGameFlow before each of them is ready (or the wait's
  // timeout). Empty (the default) = only the humans already seated count.
  virtual std::vector<uint64_t> ExpectedHumans() { return std::vector<uint64_t>(); }

  // G3b: the match-found prompt (connect type 1) was declined, or not answered in time, and the session aborted the
  // game before EnterPreGameFlow: every client in the lobby got GameAborted (0x89) and has answered with 0x40000, or
  // SessionOptions::abortAckMs passed. `culprits` are the user ids that declined or didn't answer. Called once; the
  // host ends the game (it exits without finishGame, so dgserver removes it: NotifyPlayerRemoved GAME_DESTROYED).
  virtual void OnGameAborted(const std::vector<uint64_t>& culprits, const std::string& why) { (void)culprits, (void)why; }

  // The session reached `point` for `conn`; the match may send through `out`.
  virtual void OnPoint(ConnId conn, SessionPoint point, ISender& out) = 0;

  // The status model changed the player's status (the echoed word and progress).
  virtual void OnPlayerStatus(ConnId conn, uint32_t status, float progress) { (void)conn, (void)status, (void)progress; }

  // A client message the session does not consume (ActionCommandMsgs for D7, ShaperSelectMsgs for D3, ...).
  virtual void OnClientMessage(ConnId conn, uint8_t wire, const uint8_t* body, size_t n) {
    (void)conn, (void)wire, (void)body, (void)n;
  }

  // The connection ended (after the session's last state line for it).
  virtual void OnDisconnected(ConnId conn) { (void)conn; }

  // Every poll, before the session's own timed steps.
  virtual void Tick(SteadyClock::time_point now, ISender& out) { (void)now, (void)out; }

  // Seam 5 (ADR-2 decision 5; D2f): the LabsPlayerGlobalUpdate (wire byte first) for `conn`'s player with exactly the
  // labsPlayerGlobalData members `members` (PlayerMember), written from the match's own player state. The session
  // asks for its first player update after HelloPlayer and for every status echo, after OnPlayerStatus; false (the
  // default) = it sends its own bytes (BuildPlayerUpdate, BuildPlayerStatusEcho). The host writes them from the
  // server's cLabsPlayer with the client's writer. D3-core: `conn` is the player's own connection (kNoConn for an
  // OtherPlayer); the session also asks for the other players' first updates and status changes and for the reveal's
  // updates (RevealMembers), each written once per receiving client.
  virtual bool PlayerUpdate(ConnId conn, uint8_t gameplayIndex, const std::vector<int>& members, Bytes* msg) {
    (void)conn, (void)gameplayIndex, (void)members, (void)msg;
    return false;
  }

  // Text for the connection's state line (e.g. "injected 121/121"); empty for none.
  virtual std::string StateNote(ConnId conn) {
    (void)conn;
    return std::string();
  }

  // ---- D3-core: shaper select and players without a connection ----------------------------------------------------
  // The scripted players and bots (asked at every accepted HelloReq; the session keeps the first answer per index).
  virtual std::vector<OtherPlayer> OtherPlayers() { return std::vector<OtherPlayer>(); }

  // Asked once, at the first accepted HelloReq, when SessionOptions::shaperSelect is set: the timers, the content and
  // the scripted players' plans. false = no shaper select (the session then skips it with status 0x10, as script A).
  virtual bool ShaperSelectSetup(ShaperSelectConfig* config) {
    (void)config;
    return false;
  }

  // The reveal (phase 4): every player's pick with its new status (0x100 set), in gameplay-index order. Called before
  // the reveal's player updates go out through PlayerUpdate (seam 5), so the host can write mpNounDef, mRole and
  // mLoadoutID/Version into its server cLabsPlayers first (D3-live step 3). For a connection the session has called
  // OnPlayerStatus with the new status just before. G3a: a human whose connection had ended has its last status plus
  // 0x4 (kStatusGone) and 0x100. Called once more, with every pick, if a player shaper select didn't have at the reveal
  // joins after it (its pick is made at its join).
  virtual void OnShaperPicks(const std::vector<ShaperPick>& picks) { (void)picks; }
};

// The connection id the session passes to PlayerUpdate for a player without a connection (OtherPlayer).
constexpr ConnId kNoConn = 0;

}  // namespace gms
