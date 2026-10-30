// The match host's control channel (C1b): see control.h.
#include "control.h"

#include <windows.h>

#include <chrono>
#include <cstdint>
#include <cstdio>
#include <memory>

#include "control/client.h"
#include "control/host_control.h"
#include "engine/gms_host.h"
#include "engine/log.h"
#include "host.h"

namespace hostctl {

namespace {

using dggms::control::HostControl;

std::unique_ptr<HostControl> g_ctl;
const auto g_t0 = std::chrono::steady_clock::now();
std::int64_t g_createDeadline = 0;
std::int64_t g_finishedAt = 0;
std::string g_abortedWhy;  // G3b: EndAborted's reason; Poll exits
std::int64_t g_postGameCapMs = 105000;
bool g_warm;  // H1-live: started by dgserver's warm pool (DGGMS_WARM)
const dggms::control::CreateGame kNoGame{};

// H5: the engine thread uses the channel; the watchdog's thread may too (GiveUp), so everything that polls or sends
// holds this lock (recursive on one thread: Poll may call Exit)
struct ChannelLock {
  CRITICAL_SECTION cs;
  ChannelLock() { InitializeCriticalSection(&cs); }
};
ChannelLock g_lock;
struct Locked {
  Locked() { EnterCriticalSection(&g_lock.cs); }
  ~Locked() { LeaveCriticalSection(&g_lock.cs); }
  Locked(const Locked&) = delete;
  Locked& operator=(const Locked&) = delete;
};

std::int64_t NowMs() {
  return std::chrono::duration_cast<std::chrono::milliseconds>(std::chrono::steady_clock::now() - g_t0).count();
}

// the channel's lines; LogTimed, as the watchdog's thread may write them while the engine thread holds the log
void ControlLog(const std::string& line) { LogTimed(5000, "%s", line.c_str()); }

int PhaseRank(const std::string& p) {
  if (p == dggms::control::kPhasePreGame) return 1;
  if (p == dggms::control::kPhaseInGame) return 2;
  if (p == dggms::control::kPhasePostGame) return 3;
  return 0;
}

}  // namespace

bool Start() {
  if (g_ctl || !HostGet().enabled) return Active();
  dggms::control::HostEnv env;
  std::string err;
  if (!dggms::control::ReadHostEnv(env, &err)) {
    if (!err.empty()) Log("control: not started: %s", err.c_str());
    return false;  // no DGGMS_CONTROL: run_session.ps1 -HostDir's hosts (D1d, G2)
  }
  g_postGameCapMs = static_cast<std::int64_t>(HostOptionInt("postgame_cap_s", 105)) * 1000;
  g_createDeadline = NowMs() + static_cast<std::int64_t>(HostOptionInt("create_timeout_s", 60)) * 1000;
  // H1-live: a host of dgserver's warm pool (DGGMS_WARM=1, gmshost.Config.Warm) waits for its game as long as it takes;
  // the control connection is its liveness either way
  char warm[8] = "";
  if (GetEnvironmentVariableA("DGGMS_WARM", warm, sizeof warm) && warm[0] == '1') {
    g_warm = true;
    g_createDeadline = INT64_MAX;
    Log("control: warm host (DGGMS_WARM): no create timeout; the level loads when createGame comes");
  }
  Locked lock;
  g_ctl.reset(new HostControl(ControlLog));
  char build[64];
  std::snprintf(build, sizeof build, "dgsim %s", __DATE__);
  if (!g_ctl->Start(env, static_cast<std::int64_t>(GetCurrentProcessId()), build, NowMs(), &err)) {
    // nothing to report to: a host that can't reach its server has no match
    Exit(4, "the control channel could not be reached");
  }
  HostMark("control channel: hello sent");
  return true;
}

bool Active() { return g_ctl != nullptr; }

bool Warm() { return g_warm; }

void Poll() {
  if (!g_ctl) return;
  Locked lock;
  const std::int64_t now = NowMs();
  const bool hadGame = g_ctl->HasGame();
  g_ctl->Poll(now);
  if (!hadGame && g_ctl->HasGame()) {
    const auto& g = g_ctl->Game();
    Log("control: createGame: game %lld, %s, level %s, seed %lld, descriptors 0x%llX%s, fill teams %lld%s, %zu roster "
        "entr%s",
        static_cast<long long>(g.gameId), g.mode.c_str(), g.level.c_str(), static_cast<long long>(g.seed),
        static_cast<unsigned long long>(g.descriptors), g.descriptors ? "" : " (not given)",
        static_cast<long long>(g.fillTeams), g.fillTeams ? " (B10d: bots in the open seats)" : " (not asked)",
        g.roster.size(), g.roster.size() == 1 ? "y" : "ies");
    for (const auto& e : g.roster)
      Log("control:   roster: user %lld \"%s\", gameplay index %lld, team %lld, noun \"%s\"%s",
          static_cast<long long>(e.user), e.name.c_str(), static_cast<long long>(e.index),
          static_cast<long long>(e.team), e.noun.c_str(), e.bot ? ", bot" : "");
    HostMark("control channel: createGame");
  }
  for (const auto& r : g_ctl->TakeRemoved())
    Log("control: removePlayer: user %lld left on the Blaze side (%s)", static_cast<long long>(r.user),
        r.reason.c_str());
  if (g_ctl->AbortRequested()) Exit(0, ("abortGame: " + g_ctl->AbortReason()).c_str());
  if (g_ctl->Lost()) Exit(4, "the control connection ended");
  if (!g_abortedWhy.empty()) Exit(0, ("the game was aborted before its pre-game (G3b): " + g_abortedWhy).c_str());
  if (!g_ctl->HasGame() && now >= g_createDeadline) Exit(5, "no createGame in time ([host] create_timeout_s)");
  if (g_finishedAt) {
    const int conns = GmsHostConnections();
    if (conns == 0) Exit(0, "finished, and every GMS connection has ended");
    if (now >= g_finishedAt + g_postGameCapMs) Exit(0, "finished, and the post-game cap ([host] postgame_cap_s) is over");
  }
}

bool HasGame() { return g_ctl && g_ctl->HasGame(); }

const dggms::control::CreateGame& Game() { return g_ctl ? g_ctl->Game() : kNoGame; }

const dggms::control::RosterEntry* FindUser(std::int64_t user) { return g_ctl ? g_ctl->FindUser(user) : nullptr; }

bool InLobby() { return g_ctl && g_ctl->InLobby(); }

void Ready(int port) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->Ready(port);
  HostMark("control channel: gameReady");
}

void PlayerAdded(std::int64_t user, int index) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->PlayerAdded(user, index);
}

void PlayerGone(std::int64_t user, const char* reason) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->PlayerGone(user, reason);
}

void Phase(const char* phase) {
  if (!g_ctl) return;
  Locked lock;
  if (PhaseRank(phase) <= PhaseRank(g_ctl->Phase())) return;
  g_ctl->SetPhase(phase, NowMs());
}

void PlayerFinish(const dggms::control::PlayerFinish& m) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->SendPlayerFinish(m);
}

void SeatStats(const dggms::control::SeatStats& m) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->SendSeatStats(m);
}

void MatchTimeline(const dggms::control::MatchTimeline& m) {
  if (!g_ctl) return;
  Locked lock;
  g_ctl->SendTimeline(m);
}

std::vector<dggms::control::PlayerRewards> TakeRewards() {
  if (!g_ctl) return std::vector<dggms::control::PlayerRewards>();
  Locked lock;
  return g_ctl->TakeRewards();
}

bool PlayerKarma(const dggms::control::PlayerKarma& m) {
  if (!g_ctl) return false;
  Locked lock;
  if (!g_ctl->Active()) return false;
  g_ctl->SendPlayerKarma(m);
  return true;
}

void Finish(int winnerTeam, unsigned long long durationMs, const char* reason) {
  if (!g_ctl || g_finishedAt) return;
  Locked lock;
  dggms::control::FinishGame f;
  f.winnerTeam = winnerTeam;
  f.durationMs = static_cast<std::int64_t>(durationMs);
  f.reason = reason;
  g_ctl->SendFinish(f);
  Phase(dggms::control::kPhasePostGame);
  g_finishedAt = NowMs();
  Log("control: finished; the host stays until every GMS connection has ended or for %lld s ([host] postgame_cap_s)",
      static_cast<long long>(g_postGameCapMs / 1000));
}

void EndAborted(const char* why) {
  if (!g_abortedWhy.empty()) return;
  g_abortedWhy = why && *why ? why : "aborted";
  if (g_ctl) {
    Locked lock;
    if (g_ctl->Active()) g_ctl->SendError(0, "game aborted before the pre-game: " + g_abortedWhy, false);
  }
  Log("control: the game is aborted (%s); the next frame exits without finishGame", g_abortedWhy.c_str());
}

bool ReportError(std::int64_t code, const std::string& text, bool fatal) {
  if (!g_ctl) return false;
  Locked lock;
  return g_ctl->Active() && g_ctl->SendError(code, text, fatal);
}

bool GiveUp(std::int64_t code, const std::string& text, unsigned waitMs, std::string* describe) {
  if (!g_ctl) return false;
  for (const DWORD start = GetTickCount(); !TryEnterCriticalSection(&g_lock.cs);) {
    if (GetTickCount() - start >= waitMs) return false;  // the engine thread holds it: stalled inside the channel
    Sleep(5);
  }
  const bool sent = g_ctl->Active() && g_ctl->SendError(code, text, true);
  if (g_ctl->Active()) g_ctl->Logout();
  if (describe) *describe = g_ctl->Describe();
  LeaveCriticalSection(&g_lock.cs);
  return sent;
}

void Exit(int code, const char* why) {
  Log("host: exiting (code %d): %s", code, why);
  GmsHostShutdown();  // the session's last state lines (run_session.ps1's 'host gms' line)
  if (g_ctl) {
    Locked lock;
    if (g_ctl->Active()) g_ctl->Logout();
    Log("host: %s", g_ctl->Describe().c_str());
  }
  HostMark("exit");
  TerminateProcess(GetCurrentProcess(), static_cast<UINT>(code));
  for (;;) Sleep(1000);  // TerminateProcess on the own process does not return
}

std::string Describe() {
  if (!g_ctl) return std::string();
  Locked lock;
  return g_ctl->Describe();
}

}  // namespace hostctl
