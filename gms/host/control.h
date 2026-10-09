// The match host's control channel (roadmap C1b; ADR-2 decisions 6 and 7; docs/HOST_CONTROL.md *The host side (C1b)*).
//
// When dgserver starts the host (DGGMS_CONTROL set; run_session.ps1 -HostDir sets none, so its hosts run without it),
// dgsim.dll speaks the JSON Lines channel through gms/core/control's HostControl, on the engine thread:
//   - at the first main-thread frame it dials DGGMS_CONTROL and sends hello {match, token, pid, build};
//     createGame brings the level, seed, game id, roster and (D8-live) the game's descriptors;
//   - every frame (Poll, from the harness's Drive) it reads createGame, removePlayer and abortGame and sends the
//     heartbeat (reportGMSUpdate every 5 s);
//   - the level load waits for createGame (its level, seed and game id; the roster decides who may join);
//   - once the level is loaded and the GMS port is bound (GmsHostStart) it sends gameReady {port};
//   - ServerMatch (gms_host.cpp) lets in only roster users (gameplay index, team and noun from the roster) and reports
//     addPlayer/disconnectPlayer; the phases go out as preGame (EnterPreGameFlow), inGame (EnterGame) and postGame;
//   - the match's end (match.cpp EndMatch) sends one playerFinish per player (death.cpp) and finishGame; the host then
//     stays for the post-game until every GMS connection has ended or [host] postgame_cap_s (105) passed, sends
//     logoutGMS and exits; dgserver's playerRewards answers (D8-live) go to the post-game's C1 04 (TakeRewards); a
//     matchmade game's karma votes go out as playerKarma once its vote has closed (D8b, PlayerKarma);
//   - abortGame: logoutGMS and exit (code 0); the control connection ended: exit (code 4); no createGame within
//     [host] create_timeout_s (60): exit (code 5). The exit is TerminateProcess after the GMS session's last state
//     lines, so the game's shutdown (and its crash reporter) never runs.
// Without DGGMS_CONTROL every function here does nothing and the host behaves as since G2.
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "control/messages.h"

namespace hostctl {

// The first main-thread frame (host mode): dials the channel if DGGMS_CONTROL is set. False = no channel.
bool Start();
// The channel runs (Start succeeded).
bool Active();
// H1-live: a host of dgserver's warm pool (DGGMS_WARM=1): no create timeout, it waits for its game.
bool Warm();
// Every main-thread frame: messages in, the heartbeat out; acts on abortGame, a lost connection, the create timeout
// and the post-game's end (each exits the process).
void Poll();

bool HasGame();
const dggms::control::CreateGame& Game();
// The roster entry of a HelloReq's user; nullptr = not on the roster (or no channel).
const dggms::control::RosterEntry* FindUser(std::int64_t user);
// B15: a custom game waits in its lobby (createGame's mode custom, no startGame yet): HelloReqs are held. updateRoster
// and startGame replace the roster FindUser reads.
bool InLobby();

void Ready(int port);
void PlayerAdded(std::int64_t user, int index);
void PlayerGone(std::int64_t user, const char* reason);
// "preGame", "inGame", "postGame" (dggms::control::kPhase*); only forward moves count.
void Phase(const char* phase);
void PlayerFinish(const dggms::control::PlayerFinish& m);
// D8-live: dgserver's playerRewards lines since the last call (the post-game's C1 04, gms_host.cpp).
std::vector<dggms::control::PlayerRewards> TakeRewards();
// D8b (D8c's protocol): a matchmade game's karma votes for one player, once its post-game vote has closed; dgserver
// answers with the karma-aware playerRewards. False without a channel.
bool PlayerKarma(const dggms::control::PlayerKarma& m);
// finishGame, then phase postGame and the post-game timer (Poll exits when it is over).
void Finish(int winnerTeam, unsigned long long durationMs, const char* reason);
// H5: reportGMSError {code (messages.h kErr*), text, fatal} on the engine thread (the step guards: a step's exception,
// or a step that failed in every run; a fatal one is followed by Exit). False without a channel.
bool ReportError(std::int64_t code, const std::string& text, bool fatal);
// H5, for the watchdog's thread (the engine thread may be stalled anywhere): once the channel's lock is free within
// waitMs, the fatal reportGMSError {code, text}, then logoutGMS; *describe gets the control state line. False without
// a channel or when the lock stayed held (the engine thread stalled inside the channel).
bool GiveUp(std::int64_t code, const std::string& text, unsigned waitMs, std::string* describe);
// G3b: the game was aborted before its pre-game (the match-found prompt declined or not answered): gmsError (not
// fatal: dgserver logs it), then the next Poll exits without finishGame, so dgserver removes the game (GAME_DESTROYED).
void EndAborted(const char* why);
// logoutGMS (when connected), the GMS session's last state lines, then TerminateProcess(code).
// Exit codes: 0 abort or finished, 2 no level or GMS endpoint, 4 control connection lost, 5 no createGame in time,
// since H5 6 the watchdog (a stalled tick; hostwd, not through here) and 7 a step that failed in every run.
[[noreturn]] void Exit(int code, const char* why);

// "control ..., match 1, game 3, phase inGame, players [..], sent n, received n" for the stats lines; "" without one.
std::string Describe();

}  // namespace hostctl
