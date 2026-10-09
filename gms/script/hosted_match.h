// HostedMatch (gms/script, C1b): gms_script as a match host under dgserver (ADR-2 decision 7; docs/HOST_CONTROL.md
// *The host side (C1b)*). An IMatch decorator over the ScriptedMatch that ties the GMS session to the control channel:
//   - HelloReq: only users on createGame's roster get in, with the roster's gameplay index and team (and its noun when
//     it names one); each accepted user is reported with addPlayer, each ended connection with disconnectPlayer;
//   - the phases: preGame at EnterPreGameFlow, inGame at EnterGame (reportGMSUpdate at once);
//   - everything else goes to the ScriptedMatch unchanged (script A, injected messages; D3-core: shaper select and the
//     allies, which are not on the roster).
// The host DLL's ServerMatch (gms/engine/gms_host.cpp) does the same against the server simulator.
#pragma once

#include <cstdint>
#include <map>
#include <string>

#include "control/host_control.h"
#include "core/session/match.h"

namespace gms {

// AssetIdOf (the AssetCatalog id rule) moved to core/protocol/messages.h in D3-core.

class HostedMatch final : public IMatch {
 public:
  // `msNow` gives the caller's clock in ms (the control channel's elapsedMs).
  HostedMatch(IMatch& inner, dggms::control::HostControl& control, int64_t (*msNow)())
      : inner_(inner), control_(control), msNow_(msNow) {}

  GameSetup Setup() override {
    GameSetup s = inner_.Setup();
    if (control_.HasGame()) s.blazeGameId = static_cast<uint64_t>(control_.Game().gameId);  // G3b: GameAborted's id
    return s;
  }
  void OnConnected(ConnId conn, const std::string& addr) override { inner_.OnConnected(conn, addr); }
  bool OnHelloReq(ConnId conn, const HelloReq& req, PlayerSlot* slot) override;
  // B15: a custom game (createGame's mode custom) holds every HelloReq until dgserver's startGame
  bool HoldHello(ConnId conn, const HelloReq& req) override {
    (void)conn, (void)req;
    return control_.InLobby();
  }
  void OnPoint(ConnId conn, SessionPoint point, ISender& out) override;
  void OnPlayerStatus(ConnId conn, uint32_t status, float progress) override {
    inner_.OnPlayerStatus(conn, status, progress);
  }
  void OnClientMessage(ConnId conn, uint8_t wire, const uint8_t* body, size_t n) override {
    inner_.OnClientMessage(conn, wire, body, n);
  }
  void OnDisconnected(ConnId conn) override;
  void Tick(SteadyClock::time_point now, ISender& out) override { inner_.Tick(now, out); }
  bool PlayerUpdate(ConnId conn, uint8_t gameplayIndex, const std::vector<int>& members, Bytes* msg) override {
    return inner_.PlayerUpdate(conn, gameplayIndex, members, msg);
  }
  std::string StateNote(ConnId conn) override { return inner_.StateNote(conn); }
  // D3-core: the inner match's allies and shaper select (gms_script --shaper-select, --ally); since D3-perks each roster
  // human's own content from createGame (dgserver --shaper-select: shapers, skins, saved skins, vanity, perk pages), as
  // the host DLL's ServerMatch gives it
  std::vector<OtherPlayer> OtherPlayers() override { return inner_.OtherPlayers(); }
  bool ShaperSelectSetup(ShaperSelectConfig* config) override;
  void OnShaperPicks(const std::vector<ShaperPick>& picks) override { inner_.OnShaperPicks(picks); }
  // G3a: a returning user on a new connection (the roster's addPlayer/disconnectPlayer for it are G3's)
  void OnRejoin(ConnId conn, ConnId previous, const PlayerSlot& slot) override {
    inner_.OnRejoin(conn, previous, slot);
  }
  // G3b: the match-found prompt was declined or timed out: gmsError (not fatal; for dgserver's log), then the host
  // exits without finishGame (AbortedByPrompt), so dgserver removes the game (GAME_DESTROYED)
  void OnGameAborted(const std::vector<uint64_t>& culprits, const std::string& why) override {
    inner_.OnGameAborted(culprits, why);
    control_.SendError(0, "game aborted before the pre-game: " + why, false);
    abortedByPrompt_ = true;
  }
  bool AbortedByPrompt() const { return abortedByPrompt_; }
  // G3: createGame's human roster users (the session's pre-game waits for each of them)
  std::vector<uint64_t> ExpectedHumans() override {
    std::vector<uint64_t> users;
    if (control_.HasGame())
      for (const dggms::control::RosterEntry& e : control_.Game().roster)
        if (!e.bot && !e.spectator && e.user > 0) users.push_back(static_cast<uint64_t>(e.user));  // B15: no spectator
    return users;
  }

  // The joined players (by connection): user id, gameplay index, team.
  struct Joined {
    int64_t user = 0;
    int index = 0;
    int team = 0;
  };
  const std::map<ConnId, Joined>& Players() const { return joined_; }
  // The first EnterGame (ms on msNow's clock; 0 = not yet).
  int64_t EnterGameMs() const { return enterGameMs_; }

 private:
  IMatch& inner_;
  dggms::control::HostControl& control_;
  int64_t (*msNow_)();
  std::map<ConnId, Joined> joined_;
  int64_t enterGameMs_ = 0;
  bool abortedByPrompt_ = false;
};

}  // namespace gms
