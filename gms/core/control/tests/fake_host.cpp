// A fake match host built on gms/core/control (roadmap C1): what the host DLL will do on the control
// channel, without the game. dgserver's supervisor can start it in place of Dawngate.exe; the Go test
// TestCppFakeHost (server/internal/gmshost) runs it when DG_CONTROL_FAKEHOST names this exe.
//
// It reads DGGMS_*, dials, says hello, waits for createGame, answers gameReady with DGGMS_BIND's port,
// addPlayer for the first roster entry, reportGMSUpdate inGame, playerFinish, finishGame, logoutGMS,
// and exits 0. With argument "abort" it waits for abortGame after gameReady instead and exits 0. With
// argument "karma" (D8c) it sends playerKarma {party 0, team 1, enemy 2} after finishGame and waits for the
// server's playerRewards, which must carry that karma, before logoutGMS. With argument "stall" (H5) it plays the host
// whose watchdog saw its tick stall: after gameReady and addPlayer the fatal reportGMSError {2 "tick stalled"}, then
// logoutGMS, and exit code 6 (the host DLL's watchdog exit).
// Exit codes: 0 done, 2 not in host mode, 3 connect failed, 4 no createGame, 5 send failed, 6 no playerRewards
// with the karma, or the stall mode's exit.
#include <windows.h>

#include <chrono>
#include <cstdio>
#include <cstring>
#include <string>
#include <thread>
#include <vector>

#include "control/client.h"

using namespace dggms::control;

// WaitFor polls until a message of type T arrives (10 s at most).
template <typename T>
static bool WaitFor(ControlClient& c, T& out) {
  for (int i = 0; i < 1000; i++) {
    std::vector<Message> got;
    bool open = c.Poll(got);
    for (Message& m : got) {
      if (std::holds_alternative<T>(m)) {
        out = std::get<T>(m);
        return true;
      }
    }
    if (!open) return false;
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  return false;
}

int main(int argc, char** argv) {
  bool abortMode = argc > 1 && std::strcmp(argv[1], "abort") == 0;
  bool karmaMode = argc > 1 && std::strcmp(argv[1], "karma") == 0;
  bool stallMode = argc > 1 && std::strcmp(argv[1], "stall") == 0;
  HostEnv env;
  std::string err;
  if (!ReadHostEnv(env, &err)) {
    std::fprintf(stderr, "not in host mode %s\n", err.c_str());
    return 2;
  }
  ControlClient c;
  if (!c.Connect(env, static_cast<int64_t>(GetCurrentProcessId()), "fake_host.cpp", 5000, &err)) {
    std::fprintf(stderr, "connect: %s\n", err.c_str());
    return 3;
  }
  CreateGame cg;
  if (!WaitFor(c, cg)) return 4;
  std::string host;
  uint16_t port = 0;
  SplitHostPort(env.bind, host, port);
  GameReady ready;
  ready.port = port;
  if (!c.Send(ready, &err)) return 5;
  if (abortMode) {
    AbortGame abort;
    if (!WaitFor(c, abort)) return 4;
    c.Send(LogoutGMS{});
    return 0;
  }
  int64_t user = cg.roster.empty() ? 0 : cg.roster[0].user;
  AddPlayer add;
  add.user = user;
  if (stallMode) {  // H5: what gms/host/watchdog.cpp sends before it ends the process
    GMSError stalled;
    stalled.code = kErrTickStalled;
    stalled.text = "no server tick for 10.0 s (fake host)";
    stalled.fatal = true;
    for (const Message& m : std::vector<Message>{add, stalled, LogoutGMS{}})
      if (!c.Send(m, &err)) return 5;
    c.Close();
    return 6;
  }
  GMSUpdate up;
  up.phase = "inGame";
  up.elapsedMs = 5000;
  up.players.push_back(user);
  PlayerFinish pf;
  pf.user = user;
  pf.stats["kills"] = 3;
  FinishGame fin;
  fin.winnerTeam = 1;
  fin.durationMs = 1234;
  fin.reason = "fake host: " + cg.level;
  for (const Message& m : std::vector<Message>{add, up, pf, fin}) {
    if (!c.Send(m, &err)) return 5;
  }
  if (karmaMode) {
    PlayerKarma k;
    k.user = user;
    k.team = 1;
    k.enemy = 2;
    if (!c.Send(k, &err)) return 5;
    PlayerRewards pr;
    if (!WaitFor(c, pr) || pr.user != user || !pr.rewards.karma || pr.rewards.karma->team != 1 ||
        pr.rewards.karma->enemy != 2 || pr.rewards.karma->points != 30)
      return 6;
  }
  if (!c.Send(LogoutGMS{}, &err)) return 5;
  c.Close();
  return 0;
}
