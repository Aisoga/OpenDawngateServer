// HostControl: the match host's side of the control channel's lifecycle (roadmap C1b; ADR-2 decisions 6 and 7;
// docs/HOST_CONTROL.md *The host side (C1b)*). Both hosts use it: gms_script in host mode and the host DLL dgsim.dll.
//
//   Start      dials DGGMS_CONTROL and says hello {match, token, pid, build}
//   Poll       every tick (never blocks): reads createGame (the roster: which HelloReq user ids are let in, with
//              which gameplay index and team; the level, seed, game id and since D8-live the descriptors),
//              removePlayer, abortGame and (D8-live) playerRewards, since B15 a custom game's updateRoster and
//              startGame (InLobby: the host holds HelloReqs until startGame); sends the heartbeat (reportGMSUpdate every
//              5 s); notices a connection that ended
//   Ready      gameReady {port} once the level is loaded and the GMS port listens
//   PlayerAdded / PlayerGone   addPlayer / disconnectPlayer per GMS connection (the joined users go into every
//              reportGMSUpdate's players)
//   SetPhase   reportGMSUpdate at once on a change ("loading" until EnterPreGameFlow, "preGame", "inGame" at
//              EnterGame, "postGame" after the match's end)
//   SendPlayerFinish, SendFinish   the results: playerFinish per player, then finishGame
//   SendPlayerKarma  (D8c) a matchmade game's karma votes per player once the post-game's vote has closed
//              (postgame::PostGame::TakeKarmaReports); dgserver answers with the karma-aware playerRewards
//   Logout     logoutGMS, the last line; the connection is closed afterwards
// What the caller must do: exit when AbortRequested() (after Logout) or Lost() is true; a host whose control
// connection ends exits by itself (HOST_CONTROL.md's requirement for H2/H2b). Every line in and out goes to the log
// sink ("control: -> ...", "control: <- ..."), so the host's log mirrors dgserver's [host] lines.
// Engine-free and single-threaded: one thread calls everything (the engine thread in the DLL).
#pragma once

#include <cstdint>
#include <functional>
#include <string>
#include <vector>

#include "control/client.h"
#include "control/messages.h"

namespace dggms::control {

// B15b: a started custom game's roster after an updateRoster: `fixed`'s players (startGame's, unchanged) and
// `update`'s spectators (a spectator of the running game comes, one leaves); a spectator entry for a user who plays
// is dropped.
std::vector<RosterEntry> WithSpectatorsOf(const std::vector<RosterEntry>& fixed, const std::vector<RosterEntry>& update);

// Phases of reportGMSUpdate (server/internal/gmshost Phase*).
inline constexpr const char* kPhaseLoading = "loading";
inline constexpr const char* kPhasePreGame = "preGame";
inline constexpr const char* kPhaseInGame = "inGame";
inline constexpr const char* kPhasePostGame = "postGame";

class HostControl {
 public:
  using LogFn = std::function<void(const std::string&)>;

  struct Options {
    int connectTimeoutMs = 5000;
    int heartbeatMs = 5000;  // reportGMSUpdate (dgserver's heartbeat timeout is 30 s)
  };

  explicit HostControl(LogFn log = nullptr) : log_(std::move(log)) {}
  HostControl(LogFn log, const Options& o) : o_(o), log_(std::move(log)) {}

  // Dials env.control and sends hello. nowMs is the caller's clock (ms); elapsedMs counts from here.
  bool Start(const HostEnv& env, int64_t pid, const std::string& build, int64_t nowMs, std::string* err = nullptr);

  // One non-blocking poll: messages in, the heartbeat out when due. Does nothing once ended.
  void Poll(int64_t nowMs);

  bool Active() const { return client_.Connected(); }
  bool HasGame() const { return hasGame_; }
  const CreateGame& Game() const { return game_; }
  // The roster entry for a HelloReq's user id; nullptr = not on the roster (the host drops the user).
  const RosterEntry* FindUser(int64_t user) const;
  // B15: a custom game (createGame's mode "custom") waits in its lobby until startGame: the host holds every HelloReq
  // meanwhile. updateRoster replaces the roster (who may connect) until then, startGame with the final one; B15b: after
  // it an updateRoster changes only the spectators (WithSpectatorsOf).
  bool InLobby() const { return hasGame_ && game_.mode == kModeCustom && !started_; }
  bool Started() const { return started_; }

  bool AbortRequested() const { return abort_; }
  const std::string& AbortReason() const { return abortReason_; }
  // The connection ended without Logout (the server closed it, or it broke): the host must exit.
  bool Lost() const { return lost_; }
  // removePlayer messages since the last call (the player left on the Blaze side).
  std::vector<RemovePlayer> TakeRemoved();
  // D8-live: playerRewards messages since the last call (what dgserver decided and stored, for the post-game's C1 04).
  std::vector<PlayerRewards> TakeRewards();

  void Ready(int64_t port);
  void PlayerAdded(int64_t user, int64_t index);
  void PlayerGone(int64_t user, const std::string& reason);
  void SetPhase(const std::string& phase, int64_t nowMs);
  const std::string& Phase() const { return phase_; }
  const std::vector<int64_t>& Players() const { return players_; }
  void SendPlayerFinish(const PlayerFinish& m);
  void SendFinish(const FinishGame& m);
  void SendPlayerKarma(const PlayerKarma& m);  // D8c
  // reportGMSError (codes: messages.h kErr*); false if it could not be sent. H5: the watchdog's and the step guards'
  // reports (a fatal one is followed by Logout and the host's exit).
  bool SendError(int64_t code, const std::string& text, bool fatal);
  int ErrorsSent() const { return errors_; }
  // logoutGMS, then the connection is closed (Lost() stays false).
  void Logout();

  // Counters for a stats line: "control 127.0.0.1:10040, match 1, game 3, phase inGame, players [..], sent n, received n".
  std::string Describe() const;

 private:
  bool SendMsg(const Message& m);
  void Heartbeat(int64_t nowMs);
  void Log(const std::string& s) const {
    if (log_) log_(s);
  }

  Options o_;
  LogFn log_;
  ControlClient client_;
  HostEnv env_;
  int64_t startMs_ = 0;
  int64_t nextBeatMs_ = 0;
  int64_t lastNowMs_ = 0;
  bool hasGame_ = false;
  CreateGame game_;
  bool started_ = false;  // B15: startGame came
  bool abort_ = false;
  std::string abortReason_;
  bool lost_ = false;
  bool loggedOut_ = false;
  std::vector<RemovePlayer> removed_;
  std::vector<PlayerRewards> rewards_;
  std::string phase_ = kPhaseLoading;
  std::vector<int64_t> players_;
  int sent_ = 0, received_ = 0, badLines_ = 0, errors_ = 0;
};

}  // namespace dggms::control
