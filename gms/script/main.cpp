// gms_script: a standalone scripted GMS (G1; ADR-2 decision 5's gms/script). It serves the GMS port with RakNet
// 4.081 (protocol 5, gms/core/net) and plays the GMS session (gms/core/session) against a ScriptedMatch built from
// the options below: D2b-live's script A takes the live client into kGameStateDungeon, and D2e's injected messages
// give it the level's objects and its shaper. It replaces raknet_probe --session (same options, same output), and it
// is what run_session.ps1 -GmsProbe session runs (output gms_script.out, the summary's 'gms probe' line). See
// docs/GMS_PROTOCOL.md "The live session (D2b-live)" and "The level's objects on the live client (D2e)".
//
// Usage: gms_script --port <port> --seconds <s> [--bind <ip>] [session and match options]
//   --bind               address to bind (default 127.0.0.1; keep development loopback-only)
//   --port, --seconds    UDP port (0 = ephemeral) and run time; the process then exits 0
// Session (SessionOptions):
//   --stop-after connected|hello|prepare|pregameflow|load|done|entergame   last step sent (default entergame)
//   --step-ms N          delay before each timed step (default 1000)
//   --load-timeout-ms N  go on with `done` after this long without the load report (default 60000)
//   --player-update      LabsPlayerGlobalUpdate after HelloPlayer (status, team, noun, online id)
//   --skip-shaper        status bit 0x10 in that update (implies --player-update); C0 01 is then left out
//   --echo-status        D2c's status model (echo every status change; C0 04 while loading triggers C0 03)
//   --script-a           --skip-shaper --echo-status (D2b-live)
// Match (ScriptOptions):
//   --prepare-flags N, --level N, --seed N, --level-flags N   GamePrepareForStart (defaults 2, sm_map_v5, 1, 0)
//   --index N, --team N, --noun N                             the player's slot (defaults 0, 1, alpha.Noun)
//   --inject <point>[+ms]:<hex>   a raw message at a point: hello, prepare, clock5, pregameflow, load, loaded,
//                                 done, entergame; +ms delays it (e.g. entergame+3000)
//   --inject-file <path>          one <point>[+ms]:<hex> per line, '#' comments (e.g. gms_reflect.py level's output;
//                                 D2e's world and shaper: re\out\d2e\world_shaper_loaded.txt, 121 messages at loaded)
//                                 (D3-core adds the points shaperselect and picked)
// Shaper select (D3-core; docs/GMS_PROTOCOL.md *Shaper select*, script B):
//   --shaper-select      run shaper select between EnterPreGameFlow and C0 01 (implies --player-update and
//                        --echo-status; overrides --skip-shaper)
//   --shaper-list <noun>:<flags>[:<offer>],...   every human's shaper list (default alpha.Noun:1); flags 1 owned,
//                        2 free rotation, 0x10 hidden, 0x20 available; nouns as asset names (alpha, artificer.Noun)
//                        or numbers
//   --skins <shaper>:<skin>[:<skin>...],...      a shaper's owned skins (C8 0B: the shaper first, then these)
//   --shaper-timers <pick>,<role/perk>,<skin>,<reveal>   in ms (default 62000,45500,15500,16; host mode: createGame's
//                        timers keys over the mode's values, a practice createGame 300000,300000,15500,16)
//   --ally <index>:<team>:<noun>[:<pick ms>:<lock ms>:<role>]   a GMS-scripted player (repeatable): in HelloPlayer,
//                        online id n for the n-th ("BOT n"), bot; with --shaper-select it picks and locks its noun
//                        at those times after phase 1 starts (defaults 2000, 4000) and locks its role once the
//                        humans have; counts as loaded at the load
// Numbers take 0x for hex. Exit code: 0 = ran for <seconds>; 2 = usage or startup error.
//
// Host mode (C1b; docs/HOST_CONTROL.md *The host side (C1b)*): when DGGMS_CONTROL is set (dgserver --gms-dir
// <this exe> --gms-args "--script-a ..." starts it like a match host), gms_script speaks the control channel: it dials
// DGGMS_CONTROL (hello), waits for createGame, binds DGGMS_BIND, sends gameReady {port}, lets in only the roster's users
// (gameplay index and team from the roster; HostedMatch), reports addPlayer/disconnectPlayer and the phases (preGame at
// EnterPreGameFlow, inGame at EnterGame, a heartbeat every 5 s), and exits on abortGame (after logoutGMS) or when the
// control connection ends. --port/--seconds are not needed (--seconds still caps the run: logoutGMS, exit 0); the
// seed and the level come from createGame unless --seed/--level are given. Since D3-perks createGame's shaperSelect
// turns shaper select on and each roster human gets its own content from createGame (dgserver --shaper-select: shapers,
// skins, saved skins, wards, announcers, perk pages), as the host DLL gives it. Test options (host mode only):
//   --ready-delay-ms N   wait N ms after the bind before gameReady (how long does the client wait for NotifyGameSetup?)
//   --exit-before-ready  exit (code 3) after createGame without gameReady (dgserver: B8's failed-connection path)
//   --finish-after S     S s after the first EnterGame: playerFinish per joined player and finishGame (reason
//                        "script"), phase postGame; then logoutGMS and exit once every player has left, or after
//                        --finish-exit-s (default 10)
//   --kill-after S       S s after the first EnterGame: TerminateProcess(self, 3), a crash without finishGame
// Host-mode exit codes: 0 abortGame, finished or the --seconds cap (after logoutGMS); 3 test exits; 4 control
// connection lost; 5 no createGame within 30 s; 2 startup error.

#include <windows.h>

#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <thread>

#include "control/host_control.h"
#include "core/common/log.h"
#include "core/net/endpoint.h"
#include "core/session/gms_session.h"
#include "core/session/pump.h"
#include "script/hosted_match.h"
#include "script/roster_shaper.h"
#include "script/scripted_match.h"

namespace {

const char kUsage[] =
    "usage: gms_script --port <port> --seconds <s> [--bind <ip>] [--stop-after connected|hello|prepare|pregameflow|"
    "load|done|entergame] [--step-ms N] [--load-timeout-ms N] [--player-update] [--skip-shaper] [--echo-status] "
    "[--script-a] [--prepare-flags N] [--level N] [--seed N] [--level-flags N] [--index N] [--team N] [--noun N] "
    "[--inject <point>[+ms]:<hex>]... [--inject-file <path>]... [--shaper-select] [--shaper-list <noun>:<flags>"
    "[:<offer>],...] [--skins <shaper>:<skin>[:<skin>...],...] [--shaper-timers <ms>,<ms>,<ms>,<ms>] "
    "[--ally <index>:<team>:<noun>[:<pick ms>:<lock ms>:<role>]]...\n"
    "host mode (DGGMS_CONTROL set): [--ready-delay-ms N] [--exit-before-ready] [--finish-after S] [--finish-exit-s S] "
    "[--kill-after S]\n";

bool ParseU32(const char* s, uint32_t* out) {
  char* end = nullptr;
  const unsigned long v = std::strtoul(s, &end, 0);
  if (end == s || *end) return false;
  *out = static_cast<uint32_t>(v);
  return true;
}

// Test options of host mode (C1b).
struct HostTest {
  uint32_t readyDelayMs = 0;
  bool exitBeforeReady = false;
  uint32_t finishAfterS = 0;
  uint32_t finishExitS = 10;
  uint32_t killAfterS = 0;
};

const gms::SteadyClock::time_point g_start = gms::SteadyClock::now();
int64_t MsNow() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(gms::SteadyClock::now() - g_start).count();
}

void ControlLog(const std::string& line) { gms::Logf("%s %s", gms::Clock().c_str(), line.c_str()); }

// D3-core: one line with the shaper select options, when there are any.
void LogShaperOptions(const gms::SessionOptions& so, const gms::ScriptOptions& mo) {
  if (!so.shaperSelect && mo.allies.empty()) return;
  std::string list, allies;
  for (const gms::ShaperListEntry& e : mo.shapers.list) {
    char b[48];
    std::snprintf(b, sizeof b, " 0x%08x:0x%x:0x%x", e.noun, e.flags, e.offer);
    list += b;
  }
  for (const gms::AllySpec& a : mo.allies) {
    char b[80];
    std::snprintf(b, sizeof b, " %u:%u:0x%08x:%d:%d:%u", a.gameplayIndex, a.team, a.plan.noun, a.plan.pickMs,
                  a.plan.lockMs, a.plan.role);
    allies += b;
  }
  gms::Logf("shaper select options: %s; list%s; skins for %zu shaper(s); timers %u,%u,%u,%u ms; allies%s",
            so.shaperSelect ? "on" : "off", list.empty() ? " alpha.Noun:1 (default)" : list.c_str(),
            mo.shapers.skins.size(), mo.shaperTimers.pickMs, mo.shaperTimers.rolePerkMs, mo.shaperTimers.skinMs,
            mo.shaperTimers.revealMs, allies.empty() ? " none" : allies.c_str());
}

// Host mode: gms_script as a match host under dgserver (see the header). Returns the exit code.
int RunHost(const dggms::control::HostEnv& env, gms::SessionOptions so, gms::ScriptOptions mo, bool seedGiven,
            bool levelGiven, bool shaperTimersGiven, uint32_t seconds, const HostTest& test) {
  using namespace dggms::control;
  HostControl control(ControlLog);
  std::string error;
  if (!control.Start(env, static_cast<int64_t>(GetCurrentProcessId()), "gms_script", MsNow(), &error)) {
    std::fprintf(stderr, "gms_script: control channel %s: %s\n", env.control.c_str(), error.c_str());
    return 2;
  }
  const int64_t createDeadline = MsNow() + 30000;
  while (!control.HasGame() && !control.Lost() && !control.AbortRequested() && MsNow() < createDeadline) {
    control.Poll(MsNow());
    std::this_thread::sleep_for(std::chrono::milliseconds(5));
  }
  if (!control.HasGame()) {
    gms::Logf("%s host: no createGame (%s); exiting", gms::Clock().c_str(),
              control.Lost() ? "control connection lost" : control.AbortRequested() ? "abortGame" : "30 s passed");
    if (control.AbortRequested()) control.Logout();
    return control.Lost() ? 4 : control.AbortRequested() ? 0 : 5;
  }
  const CreateGame& game = control.Game();
  if (!seedGiven) mo.setup.seed = static_cast<uint32_t>(game.seed);
  if (!levelGiven && !game.level.empty()) {
    const std::string asset = game.level.find('.') == std::string::npos ? game.level + ".Level" : game.level;
    mo.setup.level = gms::AssetIdOf(asset);
  }
  // D3-core: the practice timers for a practice createGame; since D3-perks also createGame's shaperSelect (dgserver
  // --shaper-select turns shaper select on, as for the host DLL) and its timers keys, unless --shaper-timers is given
  if (game.shaperSelect && !so.shaperSelect) {
    so.shaperSelect = true;
    gms::Logf("%s host: shaper select on (createGame's shaperSelect)", gms::Clock().c_str());
  }
  if (!shaperTimersGiven) mo.shaperTimers = gms::ShaperTimersFromCreateGame(game);
  gms::Logf("%s host: createGame: game %lld, %s, level %s (0x%08x), seed %u, %zu roster entr%s", gms::Clock().c_str(),
            static_cast<long long>(game.gameId), game.mode.c_str(), game.level.c_str(), mo.setup.level, mo.setup.seed,
            game.roster.size(), game.roster.size() == 1 ? "y" : "ies");
  if (test.exitBeforeReady) {
    gms::Logf("%s host: exiting before gameReady (--exit-before-ready)", gms::Clock().c_str());
    return 3;
  }
  std::string bindHost;
  uint16_t bindPort = 0;
  SplitHostPort(env.bind, bindHost, bindPort);
  gms::Endpoint ep;
  if (!ep.Start(bindHost, bindPort, 8, &error)) {
    std::fprintf(stderr, "gms_script: %s\n", error.c_str());
    control.SendError(dggms::control::kErrGmsEndpoint, "GMS endpoint: " + error, true);
    control.Logout();
    return 2;
  }
  const auto start = gms::SteadyClock::now();
  gms::Logf("%s RakNet %s server, RAKNET_PROTOCOL_VERSION %d, listening on %s:%u, guid %s; scripted GMS session "
            "(gms_script, host mode under dgserver: match %lld)",
            gms::Clock().c_str(), gms::Endpoint::RakNetVersion(), gms::Endpoint::ProtocolVersion(), bindHost.c_str(),
            ep.BoundPort(), ep.GuidText().c_str(), static_cast<long long>(env.match));
  gms::Logf("session script: stop after %s, step %d ms, load timeout %d ms, prepare flags 0x%x, level 0x%08x, "
            "seed %u, level flags 0x%x; player update %s, skip shaper %s, echo status %s, team from the roster; "
            "inject: %zu message(s)",
            gms::SessionStepName(so.stopAfter), so.stepMs, so.loadTimeoutMs, mo.setup.prepareFlags, mo.setup.level,
            mo.setup.seed, mo.setup.levelFlags, so.playerUpdate || so.skipShaper ? "yes" : "no",
            so.skipShaper ? "yes" : "no", so.echoStatus ? "yes" : "no", mo.inject.size());
  LogShaperOptions(so, mo);
  so.helloPort = ep.BoundPort();

  gms::ScriptedMatch scripted(mo);
  gms::HostedMatch match(scripted, control, MsNow);
  gms::GmsSession session(ep, match, so, start);
  const int64_t readyAt = MsNow() + test.readyDelayMs;
  bool ready = false;
  int64_t finishedAt = 0;
  const int64_t capAt = seconds ? MsNow() + int64_t(seconds) * 1000 : 0;
  int code = 0;
  for (;;) {
    const int64_t now = MsNow();
    control.Poll(now);
    if (!ready && now >= readyAt) {
      if (test.readyDelayMs)
        gms::Logf("%s host: gameReady after --ready-delay-ms %u", gms::Clock().c_str(), test.readyDelayMs);
      control.Ready(ep.BoundPort());
      ready = true;
    }
    gms::PumpEndpoint(ep, session, start);
    session.Tick();
    for (const RemovePlayer& r : control.TakeRemoved())
      gms::Logf("%s host: removePlayer: user %lld left on the Blaze side (%s)", gms::Clock().c_str(),
                static_cast<long long>(r.user), r.reason.c_str());
    if (control.AbortRequested()) {
      gms::Logf("%s host: abortGame (%s); logoutGMS and exit", gms::Clock().c_str(), control.AbortReason().c_str());
      code = 0;
      break;
    }
    if (control.Lost()) {
      gms::Logf("%s host: the control connection ended; exiting", gms::Clock().c_str());
      code = 4;
      break;
    }
    if (match.AbortedByPrompt()) {
      gms::Logf("%s host: the game was aborted at the match-found prompt (G3b); logoutGMS and exit without finishGame",
                gms::Clock().c_str());
      code = 0;
      break;
    }
    const int64_t entered = match.EnterGameMs();
    if (test.killAfterS && entered && now >= entered + int64_t(test.killAfterS) * 1000) {
      gms::Logf("%s host: --kill-after %u: TerminateProcess(self, 3), no finishGame", gms::Clock().c_str(),
                test.killAfterS);
      std::fflush(stdout);
      TerminateProcess(GetCurrentProcess(), 3);
    }
    if (test.finishAfterS && entered && !finishedAt && now >= entered + int64_t(test.finishAfterS) * 1000) {
      int winner = 0;
      for (const auto& kv : match.Players()) {
        PlayerFinish pf;
        pf.user = kv.second.user;
        pf.stats["won"] = 1;
        pf.stats["team"] = kv.second.team;
        pf.stats["durationMs"] = int64_t(test.finishAfterS) * 1000;
        control.SendPlayerFinish(pf);
        if (!winner) winner = kv.second.team;
      }
      FinishGame fin;
      fin.winnerTeam = winner;
      fin.durationMs = int64_t(test.finishAfterS) * 1000;
      fin.reason = "script";
      control.SendFinish(fin);
      control.SetPhase(kPhasePostGame, now);
      finishedAt = now;
      gms::Logf("%s host: --finish-after %u: finishGame sent (winner team %d); exit once every player has left or "
                "after %u s",
                gms::Clock().c_str(), test.finishAfterS, winner, test.finishExitS);
    }
    if (finishedAt && (match.Players().empty() || now >= finishedAt + int64_t(test.finishExitS) * 1000)) {
      gms::Logf("%s host: finished %s; logoutGMS and exit", gms::Clock().c_str(),
                match.Players().empty() ? "and every player has left" : "and the post-game time is over");
      break;
    }
    if (capAt && now >= capAt) {
      gms::Logf("%s host: --seconds %u over; logoutGMS and exit", gms::Clock().c_str(), seconds);
      break;
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(5));
  }
  session.Finish();
  if (!control.Lost()) control.Logout();
  gms::Logf("%s host: %s; exit code %d", gms::Clock().c_str(), control.Describe().c_str(), code);
  ep.Stop();
  return code;
}

}  // namespace

int main(int argc, char** argv) {
  std::setvbuf(stdout, nullptr, _IONBF, 0);  // keep the log complete if the process is stopped
  std::string bind = "127.0.0.1";
  uint32_t port = 0, seconds = 0;
  bool havePort = false, haveSeconds = false;
  gms::SessionOptions so;
  gms::ScriptOptions mo;
  HostTest test;
  bool seedGiven = false, levelGiven = false, shaperTimersGiven = false;
  std::string error;
  bool ok = true;
  for (int i = 1; i < argc && ok; ++i) {
    const std::string a = argv[i];
    const bool hasValue = i + 1 < argc;
    uint32_t v = 0;
    if (a == "--bind" && hasValue) {
      bind = argv[++i];
    } else if (a == "--port" && hasValue) {
      ok = havePort = ParseU32(argv[++i], &port) && port <= 65535;
    } else if (a == "--seconds" && hasValue) {
      ok = haveSeconds = ParseU32(argv[++i], &seconds);
    } else if (a == "--stop-after" && hasValue) {
      so.stopAfter = gms::ParseSessionStep(argv[++i]);
      ok = so.stopAfter >= 0;
    } else if (a == "--step-ms" && hasValue) {
      ok = ParseU32(argv[++i], &v);
      so.stepMs = static_cast<int>(v);
    } else if (a == "--load-timeout-ms" && hasValue) {
      ok = ParseU32(argv[++i], &v);
      so.loadTimeoutMs = static_cast<int>(v);
    } else if (a == "--player-update") {
      so.playerUpdate = true;
    } else if (a == "--skip-shaper") {
      so.skipShaper = true;
    } else if (a == "--echo-status") {
      so.echoStatus = true;
    } else if (a == "--script-a") {
      so.skipShaper = so.echoStatus = true;
    } else if (a == "--prepare-flags" && hasValue) {
      ok = ParseU32(argv[++i], &mo.setup.prepareFlags);
    } else if (a == "--level" && hasValue) {
      ok = levelGiven = ParseU32(argv[++i], &mo.setup.level);
    } else if (a == "--seed" && hasValue) {
      ok = seedGiven = ParseU32(argv[++i], &mo.setup.seed);
    } else if (a == "--level-flags" && hasValue) {
      ok = ParseU32(argv[++i], &mo.setup.levelFlags);
    } else if (a == "--index" && hasValue) {
      ok = ParseU32(argv[++i], &v) && v <= 255;
      mo.slot.gameplayIndex = static_cast<uint8_t>(v);
    } else if (a == "--team" && hasValue) {
      ok = ParseU32(argv[++i], &v) && v <= 255;
      mo.slot.team = static_cast<uint8_t>(v);
    } else if (a == "--noun" && hasValue) {
      ok = ParseU32(argv[++i], &mo.slot.noun);
    } else if (a == "--inject" && hasValue) {
      gms::InjectSpec s;
      ok = gms::ParseInject(argv[++i], &s, &error);
      if (ok) mo.inject.push_back(std::move(s));
    } else if (a == "--inject-file" && hasValue) {
      const char* path = argv[++i];
      const int n = gms::ReadInjectFile(path, &mo.inject, &error);
      ok = n > 0;
      if (n == 0) error = std::string(path) + ": no messages";
      if (ok) std::printf("inject file %s: %d message(s)\n", path, n);
    } else if (a == "--ready-delay-ms" && hasValue) {
      ok = ParseU32(argv[++i], &test.readyDelayMs);
    } else if (a == "--exit-before-ready") {
      test.exitBeforeReady = true;
    } else if (a == "--finish-after" && hasValue) {
      ok = ParseU32(argv[++i], &test.finishAfterS);
    } else if (a == "--finish-exit-s" && hasValue) {
      ok = ParseU32(argv[++i], &test.finishExitS);
    } else if (a == "--kill-after" && hasValue) {
      ok = ParseU32(argv[++i], &test.killAfterS);
    } else if (a == "--shaper-select") {  // D3-core
      so.shaperSelect = true;
    } else if (a == "--shaper-list" && hasValue) {
      ok = gms::ParseShaperList(argv[++i], &mo.shapers.list, &error);
    } else if (a == "--skins" && hasValue) {
      ok = gms::ParseSkins(argv[++i], &mo.shapers.skins, &error);
    } else if (a == "--shaper-timers" && hasValue) {
      ok = shaperTimersGiven = gms::ParseShaperTimers(argv[++i], &mo.shaperTimers, &error);
    } else if (a == "--ally" && hasValue) {
      gms::AllySpec ally;
      ok = gms::ParseAlly(argv[++i], &ally, &error);
      if (ok) mo.allies.push_back(ally);
    } else {
      ok = false;
      error = "unknown or incomplete option " + a;
    }
  }
  // C1b: started by dgserver (DGGMS_CONTROL set): host mode
  dggms::control::HostEnv env;
  std::string envError;
  const bool hostMode = dggms::control::ReadHostEnv(env, &envError);
  if (!hostMode && !envError.empty()) {
    std::fprintf(stderr, "gms_script: %s\n", envError.c_str());
    return 2;
  }
  if (ok && hostMode)
    return RunHost(env, so, mo, seedGiven, levelGiven, shaperTimersGiven, haveSeconds ? seconds : 0, test);
  if (!ok || !havePort || !haveSeconds) {
    if (!error.empty()) std::fprintf(stderr, "gms_script: %s\n", error.c_str());
    std::fputs(kUsage, stderr);
    return 2;
  }

  gms::Endpoint ep;
  if (!ep.Start(bind, static_cast<uint16_t>(port), 8, &error)) {
    std::fprintf(stderr, "gms_script: %s\n", error.c_str());
    return 2;
  }
  const auto start = gms::SteadyClock::now();
  gms::Logf("%s RakNet %s server, RAKNET_PROTOCOL_VERSION %d, listening on %s:%u for %u s, guid %s; scripted GMS "
            "session (gms_script)",
            gms::Clock().c_str(), gms::Endpoint::RakNetVersion(), gms::Endpoint::ProtocolVersion(), bind.c_str(),
            ep.BoundPort(), seconds, ep.GuidText().c_str());
  std::string inj;
  if (mo.inject.size() > 8) {
    inj = " " + std::to_string(mo.inject.size()) + " messages";
  } else {
    for (const gms::InjectSpec& s : mo.inject) inj += " " + gms::DescribeInject(s);
  }
  gms::Logf("session script: stop after %s, step %d ms, load timeout %d ms, prepare flags 0x%x, level 0x%08x, "
            "seed %u, level flags 0x%x, gameplayIndex %u; player update %s, skip shaper %s, echo status %s, "
            "team %u, noun 0x%08x; inject:%s",
            gms::SessionStepName(so.stopAfter), so.stepMs, so.loadTimeoutMs, mo.setup.prepareFlags, mo.setup.level,
            mo.setup.seed, mo.setup.levelFlags, mo.slot.gameplayIndex, so.playerUpdate || so.skipShaper ? "yes" : "no",
            so.skipShaper ? "yes" : "no", so.echoStatus ? "yes" : "no", mo.slot.team, mo.slot.noun,
            inj.empty() ? " none" : inj.c_str());
  LogShaperOptions(so, mo);

  gms::ScriptedMatch match(mo);
  gms::GmsSession session(ep, match, so, start);
  const auto end = start + std::chrono::seconds(seconds);
  while (gms::SteadyClock::now() < end) {
    gms::PumpEndpoint(ep, session, start);
    session.Tick();
    std::this_thread::sleep_for(std::chrono::milliseconds(5));
  }
  session.Finish();
  gms::Logf("%s listen time over; shutting down", gms::Clock().c_str());
  ep.Stop();
  return 0;
}
