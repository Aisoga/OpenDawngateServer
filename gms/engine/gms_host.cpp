// The match host's GMS (G2): see gms_host.h.
#include "gms_host.h"

#include <windows.h>

#include <algorithm>
#include <cctype>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <iterator>
#include <map>
#include <memory>
#include <random>
#include <set>
#include <sstream>
#include <string>
#include <vector>

#include "core/common/log.h"
#include "core/net/endpoint.h"
#include "core/postgame/postgame.h"
#include "core/protocol/messages.h"
#include "core/protocol/wire.h"
#include "core/replication/fow_data.h"
#include "core/replication/modifiers.h"
#include "core/replication/outgoing.h"
#include "core/session/gms_session.h"
#include "core/session/pump.h"
#include "core/session/spectators.h"
#include "core/surrender/surrender.h"
#include "core/surrender/team_left.h"
#include "core/vision/fog.h"
#include "core/vision/vision.h"
#include "agents.h"
#include "banter.h"
#include "bots.h"
#include "control/host_control.h"
#include "commands.h"
#include "death.h"
#include "fow.h"
#include "game.h"
#include "host/control.h"
#include "host/host.h"
#include "log.h"
#include "match.h"
#include "perf.h"
#include "perks.h"
#include "replicate.h"
#include "surrender.h"
#include "script/roster_shaper.h"
#include "script/scripted_match.h"

using namespace game;

namespace {

// ---------------------------------------------------------------------------------------------
// Engine calls under SEH (functions with __try can't hold C++ objects). Each logs an exception and reports failure;
// the session goes on. The player calls bind the server simulator first (BoundTo below): the player pool is the
// current simulator's (LabsPlayer_FromHandle 0x00913990), and the stats line runs outside the server tick, where the
// host's own client simulator is current (g2_run1: every stats line asserted "TryToGet: Index (0) ... cLabsPlayer"
// on the client's empty player pool).

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in gms %s: code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

void* SehFindAssetById(std::uint32_t id) {
  __try {
    return FindAssetById(id);
  } __except (SehNote(GetExceptionInformation(), "FindAssetById")) {
    return nullptr;
  }
}

// D11 test aid (the commands file's {nearest:<prefix>} and {nearestpos:<prefix>}): the live object nearest to object
// `from` whose noun name starts with `prefix` (no pending delete; a combatant only while its HP is above 0), with its
// position. False if there is none. The server simulator must be bound.
// D12: `filter` (the placeholder's "@<filter>" after the prefix) narrows it to combatants: "enemy" (team 1 or 2, not
// `from`'s), "ally" (`from`'s team), "npc" (any other team, e.g. the jungle's), each optionally with "player" (a
// player's creature: mPlayerIdx set), e.g. {nearest:@enemyplayer}; nullptr or "" = no filter.
bool SehNearest(void* sim, std::uint32_t from, const char* prefix, const char* filter, std::uint32_t* id,
                float pos[3]) {
  __try {
    void* self = from ? GetObjectById(from) : nullptr;
    if (!self) return false;
    const float* me = reinterpret_cast<const float*>(static_cast<char*>(self) + gobj::kPosition);
    void* om = Field<void*>(sim, sim::kObjectManager);
    const std::size_t len = std::strlen(prefix);
    const std::uint8_t myTeam = Field<std::uint8_t>(self, gobj::kTeam);
    const bool wantEnemy = filter && std::strstr(filter, "enemy") != nullptr;
    const bool wantAlly = filter && std::strstr(filter, "ally") != nullptr;
    const bool wantNpc = filter && std::strstr(filter, "npc") != nullptr;
    const bool wantPlayer = filter && std::strstr(filter, "player") != nullptr;
    const bool combatantOnly = wantEnemy || wantAlly || wantNpc || wantPlayer;
    float best = 1e30f;
    int it = ObjMgrIterBegin(om);
    for (void* o = ObjMgrIterNext(om, &it); o; o = ObjMgrIterNext(om, &it)) {
      if (o == self || Field<unsigned char>(o, gobj::kPendingDelete)) continue;
      void* nounAsset = Field<void*>(o, gobj::kNoun);
      const char* name = nounAsset ? Field<const char*>(nounAsset, 0) : nullptr;
      if (!name || _strnicmp(name, prefix, len) != 0) continue;
      char* cd = Field<char*>(o, combatant::kOfObject);
      if (cd && !(Field<float>(cd, combatant::kHitPoints) > 0.f)) continue;
      if (combatantOnly) {
        if (!cd) continue;
        const std::uint8_t team = Field<std::uint8_t>(o, gobj::kTeam);
        const bool fighting = team == 1 || team == 2;
        if (wantEnemy && !(fighting && team != myTeam)) continue;
        if (wantAlly && team != myTeam) continue;
        if (wantNpc && fighting) continue;
        if (wantPlayer && Field<std::uint8_t>(o, gobj::kPlayerIndex) == 0xFF) continue;
      }
      const float* p = reinterpret_cast<const float*>(static_cast<char*>(o) + gobj::kPosition);
      const float dx = p[0] - me[0], dy = p[1] - me[1];
      const float d = dx * dx + dy * dy;
      if (d < best) {
        best = d;
        *id = Field<std::uint32_t>(o, gobj::kId);
        std::memcpy(pos, p, 3 * sizeof(float));
      }
    }
    return best < 1e30f;
  } __except (SehNote(GetExceptionInformation(), "nearest object")) {
    return false;
  }
}

// GameSimulator_AddPlayer on the server simulator, then the labsPlayerGlobalData members the session announces. B15b:
// a spectator (gameplay index 10-14) goes into the spectator map (+0x1A8FC), which GameSimulator_GetPlayerByIndex
// (0x008F0D90) searches for 10-14 and GameSimulator_GetPlayerMaskForTeams (0x008F08D0) adds whole when its spectators
// flag is set, whatever the team (the server's own sends to (1 << index) | 0x7C00 reach it that way).
void* TryAddPlayer(void* sim, unsigned index, unsigned char team, void* noun, unsigned long long onlineId,
                   unsigned status, int spectator) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) p = AddPlayer(sim, index, spectator);
    if (!p) return nullptr;
    Field<unsigned char>(p, player::kTeam) = team;
    Field<void*>(p, player::kNounDef) = noun;
    Field<unsigned long long>(p, player::kOnlineId) = onlineId;
    Field<unsigned>(p, player::kStatus) = status;
    return p;
  } __except (SehNote(GetExceptionInformation(), "AddPlayer")) {
    return nullptr;
  }
}

bool TrySetStatus(void* sim, unsigned index, unsigned status, float progress) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    Field<unsigned>(p, player::kStatus) = status;
    Field<float>(p, player::kStatusProgress) = progress;
    return true;
  } __except (SehNote(GetExceptionInformation(), "SetStatus")) {
    return false;
  }
}

// The server player's status, team and index as the simulator holds them; false if there is no such player.
bool TryReadPlayer(void* sim, unsigned index, unsigned* status, unsigned* team, unsigned* storedIndex) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    *status = Field<unsigned>(p, player::kStatus);
    *team = Field<unsigned char>(p, player::kTeam);
    *storedIndex = Field<unsigned char>(p, player::kIndex);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadPlayer")) {
    return false;
  }
}

// The server simulator bound to the thread for one call, the previous one restored afterwards.
class BoundTo {
 public:
  explicit BoundTo(void* sim) : prev_(GetCurrentSim()) { SetCurrentSim(sim); }
  ~BoundTo() { SetCurrentSim(prev_); }
  BoundTo(const BoundTo&) = delete;
  BoundTo& operator=(const BoundTo&) = delete;

 private:
  void* prev_;
};

void* SehAddPlayer(void* sim, unsigned index, unsigned char team, void* noun, unsigned long long onlineId,
                   unsigned status, int spectator = 0) {
  BoundTo b(sim);
  return TryAddPlayer(sim, index, team, noun, onlineId, status, spectator);
}
bool SehSetStatus(void* sim, unsigned index, unsigned status, float progress) {
  BoundTo b(sim);
  return TrySetStatus(sim, index, status, progress);
}

// G3: mStatus |= bits (a departure's 0x4), the progress kept; the new status in `*now`.
bool TryAddStatusBits(void* sim, unsigned index, unsigned bits, unsigned* now) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    Field<unsigned>(p, player::kStatus) |= bits;
    *now = Field<unsigned>(p, player::kStatus);
    return true;
  } __except (SehNote(GetExceptionInformation(), "AddStatusBits")) {
    return false;
  }
}
bool SehAddStatusBits(void* sim, unsigned index, unsigned bits, unsigned* now) {
  BoundTo b(sim);
  return TryAddStatusBits(sim, index, bits, now);
}
bool SehReadPlayer(void* sim, unsigned index, unsigned* status, unsigned* team, unsigned* storedIndex) {
  BoundTo b(sim);
  return TryReadPlayer(sim, index, status, team, storedIndex);
}

// D6a: the server player's mpNounDef (its shaper, for the bots' team fill); null if there is no such player.
void* TryPlayerNoun(void* sim, unsigned index) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    return p ? Field<void*>(p, player::kNounDef) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "PlayerNoun")) {
    return nullptr;
  }
}
void* SehPlayerNoun(void* sim, unsigned index) {
  BoundTo b(sim);
  return TryPlayerNoun(sim, index);
}

// D3-live: shaper select's pick into the server cLabsPlayer, before the client's 0x401 (kLoaded), where D2f's deploy
// (LabsPlayer_DeployCharacter 0x00915400) reads mpNounDef: the chosen skin's Noun asset, mRole and the perk page
// (mLoadoutID/mLoadoutVersion). The reveal's player updates (seam 5) are then written from these fields.
bool TryWritePick(void* sim, unsigned index, void* noun, unsigned char role, std::uint32_t page, std::uint32_t version) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    if (noun) Field<void*>(p, player::kNounDef) = noun;
    Field<unsigned char>(p, player::kRole) = role;
    Field<std::uint32_t>(p, player::kLoadoutId) = page;
    Field<std::uint32_t>(p, player::kLoadoutVersion) = version;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WritePick")) {
    return false;
  }
}
bool SehWritePick(void* sim, unsigned index, void* noun, unsigned char role, std::uint32_t page, std::uint32_t version) {
  BoundTo b(sim);
  return TryWritePick(sim, index, noun, role, page, version);
}

// D2g-live test fixture: runs a Lua chunk on the server simulator's lua_State (the master state; the caller binds the
// server simulator) under lua_pcall; `out` gets its string result (or the error). False if it failed.
bool SehRunLua(void* sim, const char* chunk, unsigned size, const char* name, char* out, unsigned cap) {
  __try {
    out[0] = 0;
    void* L = Field<void*>(static_cast<char*>(sim) + sim::kLua, luasim::kLuaState);
    const int top = LuaGetTop(L);
    int rc = LuaLoadBuffer(L, chunk, size, name);
    if (rc == 0) rc = LuaPcall(L, 0, 1, 0);
    const char* s = LuaToString(L, -1, nullptr);
    if (s) {
      unsigned k = 0;
      for (; s[k] && k + 1 < cap; ++k) out[k] = s[k];
      out[k] = 0;
    }
    LuaSetTop(L, top);
    return rc == 0;
  } __except (SehNote(GetExceptionInformation(), "RunLua")) {
    return false;
  }
}

// D2g-live test fixture: a wave minion's HP (false if the object is gone), and marking it for delete (what
// nGameObject.MarkForDelete 0x00940D60 does: +0x55 = 1; DeletePending deletes it in the next tick).
bool SehObjectHp(std::uint32_t id, float* hp) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    void* c = Field<void*>(o, combatant::kOfObject);
    *hp = c ? Field<float>(c, combatant::kHitPoints) : -1.f;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ObjectHp")) {
    return false;
  }
}

// Q3c test aid (the commands file's {casterpos} and {creaturepos:<index>}): an object's position. The server simulator
// must be bound.
bool SehObjectPos(std::uint32_t id, float pos[3]) {
  __try {
    void* o = id ? GetObjectById(id) : nullptr;
    if (!o) return false;
    std::memcpy(pos, static_cast<char*>(o) + gobj::kPosition, 3 * sizeof(float));
    return true;
  } __except (SehNote(GetExceptionInformation(), "ObjectPos")) {
    return false;
  }
}

bool SehMarkForDelete(std::uint32_t id) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    Field<unsigned char>(o, gobj::kPendingDelete) = 1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "MarkForDelete")) {
    return false;
  }
}

// ---------------------------------------------------------------------------------------------

// FNV-1 of the lower-cased "<name>.<class>": the AssetCatalog id rule (D2b, D2c).
std::uint32_t AssetId(const char* name) {
  std::uint32_t h = 0x811C9DC5u;
  for (const char* p = name; *p; ++p) {
    h *= 16777619u;
    h ^= static_cast<unsigned char>(std::tolower(static_cast<unsigned char>(*p)));
  }
  return h;
}

// "a.b.c.d" -> 0xAABBCCDD (HelloPlayerRecord's number form); 0 if malformed.
std::uint32_t ParseIpv4(const std::string& s) {
  unsigned a, b, c, d;
  char tail;
  if (std::sscanf(s.c_str(), "%u.%u.%u.%u%c", &a, &b, &c, &d, &tail) != 4 || a > 255 || b > 255 || c > 255 || d > 255)
    return 0;
  return a << 24 | b << 16 | c << 8 | d;
}

constexpr unsigned kMaxPlayers = 10;  // gameplay indexes 0-9 (10-14 are spectators)

// Q3c: the traffic kind (gms::timing::TrafficKind) of what the enclosed code sends; the kind before it at the end.
class KindScope {
 public:
  explicit KindScope(int kind) : prev_(perf::Kind()) { perf::SetKind(kind); }
  ~KindScope() { perf::SetKind(prev_); }
  KindScope(const KindScope&) = delete;
  KindScope& operator=(const KindScope&) = delete;

 private:
  int prev_;
};

// IMatch over the server simulator (ADR-2 decision 5's engine side; since D2f the world comes from the simulator).
class ServerMatch final : public gms::IMatch {
 public:
  ServerMatch(void* sim, const gms::ScriptOptions& o) : sim_(sim), o_(o), inject_(o) {}

  gms::GameSetup Setup() override { return o_.setup; }

  void OnConnected(gms::ConnId conn, const std::string& addr) override { inject_.OnConnected(conn, addr); }

  // B15: a custom game (createGame's mode custom) waits in the client's custom lobby until dgserver's startGame: every
  // HelloReq is held, so a player may still change teams or become a spectator; at the start the roster has the final
  // slots (team 1 at 0-4, team 2 at 5-9, spectators at 10-14) and OnHelloReq takes them as for any roster.
  bool HoldHello(gms::ConnId conn, const gms::HelloReq& req) override {
    if (!hostctl::InLobby()) return false;
    if (!lobbyLogged_.count(conn)) {
      lobbyLogged_.insert(conn);
      gms::Logf("%s match: HelloReq from user %llu held: the custom game waits in its lobby (%s)", gms::Clock().c_str(),
                static_cast<unsigned long long>(req.userId),
                hostctl::FindUser(static_cast<std::int64_t>(req.userId)) ? "on the lobby's roster" : "not on the roster yet");
    }
    return true;
  }

  bool OnHelloReq(gms::ConnId conn, const gms::HelloReq& req, gms::PlayerSlot* slot) override {
    if (Rejoining(conn, req, slot)) return true;  // G3: a user with a seat keeps its server player
    auto it = players_.find(conn);
    int index = it != players_.end() ? it->second.index : -1;
    uint8_t team = o_.slot.team;
    uint32_t nounId = o_.slot.noun;
    // C1b: with the control channel only createGame's roster gets in, with its gameplay index, team and noun
    const bool controlled = hostctl::Active();
    const dggms::control::RosterEntry* entry = nullptr;
    if (controlled && index < 0) {
      entry = hostctl::FindUser(static_cast<std::int64_t>(req.userId));
      if (!entry) {
        gms::Logf("%s match: HelloReq from user %llu refused: not on createGame's roster", gms::Clock().c_str(),
                  static_cast<unsigned long long>(req.userId));
        return false;
      }
      if (entry->spectator) return AcceptSpectator(conn, req, *entry, slot);  // B15b
      if (entry->index < 0 || entry->index >= static_cast<std::int64_t>(kMaxPlayers) || used_[entry->index]) {
        gms::Logf("%s match: HelloReq from user %llu refused: the roster's gameplay index %lld is invalid or taken",
                  gms::Clock().c_str(), static_cast<unsigned long long>(req.userId),
                  static_cast<long long>(entry->index));
        return false;
      }
      index = static_cast<int>(entry->index);
      team = static_cast<uint8_t>(entry->team);
      if (!entry->noun.empty()) nounId = AssetId(entry->noun.c_str());
    }
    for (unsigned i = 0; index < 0 && i < kMaxPlayers; ++i)
      if (!used_[i]) index = static_cast<int>(i);
    if (index < 0) {
      gms::Logf("%s match: HelloReq from user %llu refused: all %u player slots taken", gms::Clock().c_str(),
                static_cast<unsigned long long>(req.userId), kMaxPlayers);
      return false;
    }
    void* noun = nounId ? SehFindAssetById(nounId) : nullptr;
    const unsigned status = skipShaper_ ? gms::kStatusSkipShaperSelect : 0;
    void* p = SehAddPlayer(sim_, static_cast<unsigned>(index), team, noun, req.userId, status);
    gms::Logf("%s match: HelloReq from user %llu -> gameplay index %d%s; server player %p (GameSimulator_AddPlayer), "
              "team %u, noun 0x%08x (asset %p), online id %llu, status 0x%x",
              gms::Clock().c_str(), static_cast<unsigned long long>(req.userId), index,
              entry ? " (createGame's roster)" : "", p, team, nounId, noun, static_cast<unsigned long long>(req.userId),
              status);
    if (!p) {
      gms::Logf("%s match: no server player for index %d; HelloReq refused", gms::Clock().c_str(), index);
      return false;
    }
    used_[index] = true;
    Player& pl = players_[conn];
    pl.index = index;
    pl.user = req.userId;
    *slot = o_.slot;
    slot->gameplayIndex = static_cast<uint8_t>(index);
    slot->team = team;
    slot->noun = nounId;
    if (entry) hostctl::PlayerAdded(static_cast<std::int64_t>(req.userId), index);
    DecideSurrender();
    surrender_.AddPlayer(conn, index, team);  // D8b: a voter of its team (humans only; the bots don't vote [assumed])
    return true;
  }

  // B15b (docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*): a roster entry with `spectator` (the Blaze
  // spectator team of a custom game). Its gameplay index is the host's own (SpectatorSeats: the roster's when free; a
  // returning user keeps its earlier one), its server cLabsPlayer goes into the server simulator's spectator map
  // (GameSimulator_AddPlayer with spectator 1) with the observer bit 0x20, [host] spectator_team (1) and no noun, so
  // seam 5 writes its updates and the server's own sends to (1 << index) | 0x7C00 reach it. No deploy, no team, no bot
  // fill, no vote (surrender: an observer, refused with reason 3), no rewards.
  bool AcceptSpectator(gms::ConnId conn, const gms::HelloReq& req, const dggms::control::RosterEntry& entry,
                       gms::PlayerSlot* slot) {
    if (!spectators_) {
      gms::Logf("%s match: HelloReq from user %llu refused: a spectator, and [host] spectators=0", gms::Clock().c_str(),
                static_cast<unsigned long long>(req.userId));
      return false;
    }
    uint64_t evicted = 0;
    const int index = spectatorSeats_.Admit(req.userId, static_cast<int>(entry.index), &evicted);
    if (index < 0) {
      gms::Logf("%s match: HelloReq from user %llu refused: a spectator, and all %u spectator slots are taken",
                gms::Clock().c_str(), static_cast<unsigned long long>(req.userId),
                static_cast<unsigned>(gms::kMaxSpectators));
      return false;
    }
    const unsigned status = gms::kStatusObserver | (skipShaper_ ? gms::kStatusSkipShaperSelect : 0);
    void* p = SehAddPlayer(sim_, static_cast<unsigned>(index), spectatorTeam_, nullptr, req.userId, status, 1);
    gms::Logf("%s match: HelloReq from user %llu -> spectator, gameplay index %d (roster %lld)%s; server player %p "
              "(GameSimulator_AddPlayer, spectator map), team %u, no noun, online id %llu, status 0x%x; spectators %s",
              gms::Clock().c_str(), static_cast<unsigned long long>(req.userId), index,
              static_cast<long long>(entry.index),
              evicted ? (", the seat kept for user " + std::to_string(evicted) + " given away").c_str() : "", p,
              static_cast<unsigned>(spectatorTeam_), static_cast<unsigned long long>(req.userId), status,
              spectatorSeats_.Describe().c_str());
    if (!p) {
      spectatorSeats_.Left(req.userId);
      gms::Logf("%s match: no server player for spectator index %d; HelloReq refused", gms::Clock().c_str(), index);
      return false;
    }
    Player& pl = players_[conn];
    pl = Player();
    pl.index = index;
    pl.user = req.userId;
    pl.spectator = true;
    *slot = o_.slot;
    slot->gameplayIndex = static_cast<uint8_t>(index);
    slot->team = spectatorTeam_;
    slot->noun = 0;
    ForgetObjects(index);  // a returning spectator's new client knows nothing yet
    hostctl::PlayerAdded(static_cast<std::int64_t>(req.userId), index);
    DecideSurrender();
    surrender_.AddPlayer(conn, index, 0, true);  // D8b's observer: the vote's status, its own requests refused
    return true;
  }

  void OnPoint(gms::ConnId conn, gms::SessionPoint point, gms::ISender& out) override {
    inject_.OnPoint(conn, point, out);
    // D2e: the world and the player's shaper go out once the client has loaded the level (0x401), before EnterGame
    if (point == gms::SessionPoint::kLoaded && world_) SendWorld(conn, out);
    if (point == gms::SessionPoint::kPreGameFlow) hostctl::Phase(dggms::control::kPhasePreGame);  // C1b
    // X2a: the full UI data one step before EnterGame (the client's TowerGraphics_Init reads it at EnterGame)
    if (point == gms::SessionPoint::kDone) UiBeforeEnterGame(conn, out);
    if (point == gms::SessionPoint::kEnterGame) {
      hostctl::Phase(dggms::control::kPhaseInGame);  // C1b
      auto it = players_.find(conn);
      if (it != players_.end()) {
        it->second.inGame = true;
        it->second.enterGame = gms::SteadyClock::now();
        if (it->second.rejoined && it->second.index >= 0) {  // D11b: "<name> has reconnected to the game." to the others
          BoundTo b(sim_);
          banter::OnPlayerReconnected(sim_, it->second.index);
        }
      }
      // D1c-live: the match clock starts with the first EnterGame (B15b: a spectator's too: a game of bots it watches)
      match::OnEnterGame();
    }
  }

  void OnPlayerStatus(gms::ConnId conn, uint32_t status, float progress) override {
    auto it = players_.find(conn);
    if (it == players_.end()) return;
    if (!SehSetStatus(sim_, static_cast<unsigned>(it->second.index), status, progress))
      gms::Logf("%s match: server player %d is gone; status 0x%x not stored", gms::Clock().c_str(), it->second.index,
                status);
    // D8-live: 0x80000 = ready for the karma vote (nPlayer.SetReadyForKarmaVote, the practice and matchmade end
    // cinematics before their loot step): a won practice game's C1 04 waits for it
    if (postgame_.Started() && (status & gms::postgame::kStatusReadyForKarmaVote) && !it->second.readyLogged) {
      it->second.readyLogged = true;
      gms::Logf("%s match: player %d is ready for the karma vote (status 0x%x) %.2f s after the end",
                gms::Clock().c_str(), it->second.index, status, NowS() - endAtS_);
    }
    postgame_.OnStatus(NowS(), conn, status);
  }

  void OnClientMessage(gms::ConnId conn, uint8_t wire, const uint8_t* body, size_t n) override {
    // the session logs every client message; D7-live: the commands go to the intake with the connection's gameplay
    // index (the connection -> player lookup the binary lacks); shaper select is D3
    auto it = players_.find(conn);
    if (it == players_.end()) return;
    // D8b: SurrenderMsgs (CA 01 for, CA 02 against: the in-game menu's SURRENDER GAME, the vote window, /surrender)
    if (wire == gms::kGmsSurrenderMsgs) {
      const size_t before = surrenderOut_.size();
      if (surrender_.OnClientMessage(NowS(), ServerMs(), conn, body, n, &surrenderOut_)) {
        ++surrenderIntake_;
        gms::Logf("%s match: the client's CA %s from player %d (match %.1f s): %zu answer(s); %s", gms::Clock().c_str(),
                  gms::Hex(body, n).c_str(), it->second.index, match::MatchSeconds(), surrenderOut_.size() - before,
                  surrender_.Describe().c_str());
        return;
      }
    }
    // D8-live (MATCH_FLOW.md *Match end and post-game (D8-static)*): the client's own C1 01 when it entered
    // kGameStateGameOver (the signal to send C1 01 {reason, Guardian, descriptors}) and its C1 03 votes
    if (wire == gms::kGmsBattlegroundsPostGameMsgs) {
      const double t = NowS();
      if (postgame_.OnClientMessage(t, conn, body, n)) {
        char votes[96] = "";
        if (n >= 3 && body[0] == gms::postgame::kSubVotes) {  // D8b: the karma vote's mask (bit = gameplay index)
          const unsigned mask = body[1] | static_cast<unsigned>(body[2]) << 8;
          std::string who;
          for (int bit = 0; bit < 16; ++bit)
            if (mask >> bit & 1) who += (who.empty() ? "" : ",") + std::to_string(bit);
          std::snprintf(votes, sizeof votes, " (karma votes: mask 0x%04X = player(s) [%s])", mask, who.c_str());
        }
        gms::Logf("%s match: the client's C1 %02x%s%s from player %d, %.3f s after BF 00", gms::Clock().c_str(),
                  n ? body[0] : 0, n && body[0] == 1 ? " (it entered kGameStateGameOver)" : "", votes,
                  it->second.index, t - endAtS_);
      } else {
        gms::Logf("%s match: client C1 message %s from player %d not taken (post-game %s)", gms::Clock().c_str(),
                  gms::Hex(body, n).c_str(), it->second.index, postgame_.Started() ? "running" : "not started");
      }
      return;
    }
    if (it->second.spectator) {
      // B15b: no input from a spectator (its client sends no commands: GameClient_SendActionCommand 0x006B41B0 and the
      // gameplay controls check the observer bit); anything else it sends is counted, the first few logged
      ++it->second.unhandled[wire];
      if (++spectatorInputLogged_ <= 20)
        gms::Logf("%s match: %02x %s from spectator %d ignored (a spectator has no input)", gms::Clock().c_str(), wire,
                  gms::GmsName(wire), it->second.index);
      return;
    }
    if (commands_ && it->second.index >= 0) {
      BoundTo b(sim_);
      if (cmds::OnClientMessage(sim_, static_cast<unsigned>(it->second.index), wire, body, n, "client")) {
        ++it->second.commands[wire];
        return;
      }
    }
    ++it->second.unhandled[wire];
  }

  void OnDisconnected(gms::ConnId conn) override {
    inject_.OnDisconnected(conn);
    postgame_.OnDisconnected(conn);  // D8-live
    // D8b: its disconnected time counts (availability 2, refusal 2); a pending voter is out of a running vote. A
    // connection a rejoin superseded finds nothing (AddPlayer moved its entry).
    surrender_.OnConnection(conn, false, NowS());
    auto it = players_.find(conn);
    if (it == players_.end()) return;  // also a connection a rejoin superseded (OnRejoin took its entry)
    const int index = it->second.index;
    if (it->second.spectator) {
      // B15b: the server spectator keeps its index with 0x4 (GameSimulator_GetPlayerMaskForTeams leaves it out when
      // asked to skip 0x4) and its seat for the user's return; nothing of it stays in the world
      unsigned status = 0;
      SehAddStatusBits(sim_, static_cast<unsigned>(index), gms::kStatusGone, &status);
      const int forgot = ForgetObjects(index);
      spectatorSeats_.Left(it->second.user);
      gms::Logf("%s match: spectator %d (user %llu) left: status 0x%x, its bit cleared in %d object(s); seat kept "
                "(spectators %s)",
                gms::Clock().c_str(), index, static_cast<unsigned long long>(it->second.user), status, forgot,
                spectatorSeats_.Describe().c_str());
      if (hostctl::Active()) hostctl::PlayerGone(static_cast<std::int64_t>(it->second.user), "disconnected");
      players_.erase(it);
      return;
    }
    if (index >= 0) {
      // G3: the server player stays with 0x4 (the session's departed status; GMS_PROTOCOL.md *Several humans live
      // (G3)*): its creature stays in the world, its kills, gold and picks stay, the index stays taken, and a rejoin of
      // the same user takes it back (Rejoining). Its A9/AA deltas still go out (ReplicatedPlayers).
      unsigned status = 0;
      const bool kept = SehAddStatusBits(sim_, static_cast<unsigned>(index), gms::kStatusGone, &status);
      gms::Logf("%s match: connection of user %llu ended; server player %d %s (creature %08X stays)",
                gms::Clock().c_str(), static_cast<unsigned long long>(it->second.user), index,
                kept ? ("kept with status 0x" + Hex8(status)).c_str() : "MISSING", it->second.creature);
      Player gone = it->second;
      gone.world = gone.inGame = gone.ui = false;
      gone.creatureSent.clear();
      departed_[gone.user] = gone;
      const int forgot = ForgetObjects(index);
      gms::Logf("%s match: player %d left: its bit cleared in %d object(s)' known and visible masks", gms::Clock().c_str(),
                index, forgot);
      if (it->second.inGame) {  // D11b: it was in the game: "<name> has left the game." to the others
        BoundTo b(sim_);
        banter::OnPlayerLeft(sim_, index);
      }
    }
    if (hostctl::Active()) hostctl::PlayerGone(static_cast<std::int64_t>(it->second.user), "disconnected");  // C1b
    players_.erase(it);
  }

  // G3: a user who has a seat already (its earlier connection is still there, or it left: departed_) comes back on a
  // new connection. It keeps its gameplay index and its server cLabsPlayer (creature, picks, kills, gold); its status
  // starts over for its new pre-game (0x10 without shaper select, as a new player's). The world is built again for the
  // new client at its 0x401 (SendWorld without a new deploy). The session's OnRejoin follows.
  bool Rejoining(gms::ConnId conn, const gms::HelloReq& req, gms::PlayerSlot* slot) {
    if (!req.userId) return false;
    const Player* had = nullptr;
    for (const auto& kv : players_)
      if (kv.first != conn && kv.second.user == req.userId && kv.second.index >= 0 && !kv.second.spectator)
        had = &kv.second;  // B15b: a spectator's return is AcceptSpectator's
    auto dep = departed_.find(req.userId);
    if (!had && dep != departed_.end()) had = &dep->second;
    if (!had) return false;
    const Player was = *had;
    unsigned status = 0, team = 0, idx = 0;
    if (!SehReadPlayer(sim_, static_cast<unsigned>(was.index), &status, &team, &idx)) {
      gms::Logf("%s match: HelloReq from user %llu: a rejoin, but server player %d is gone; refused", gms::Clock().c_str(),
                static_cast<unsigned long long>(req.userId), was.index);
      return false;
    }
    const unsigned fresh = skipShaper_ ? gms::kStatusSkipShaperSelect : 0;
    SehSetStatus(sim_, static_cast<unsigned>(was.index), fresh, 0.0f);
    const int forgot = ForgetObjects(was.index);  // its new client knows nothing yet (also after a superseded one)
    if (dep != departed_.end()) departed_.erase(dep);
    Player& pl = players_[conn];
    pl = Player();
    pl.index = was.index;
    pl.user = was.user;
    pl.creature = was.creature;  // no second deploy at its world
    pl.rejoined = true;
    used_[pl.index] = true;
    *slot = o_.slot;
    slot->gameplayIndex = static_cast<uint8_t>(pl.index);
    slot->team = static_cast<uint8_t>(team);
    ++rejoins_;
    gms::Logf("%s match: HelloReq from user %llu: a rejoin -> gameplay index %d, team %u; server player kept (status 0x%x "
              "-> 0x%x), creature %08X; its bit cleared in %d object(s)",
              gms::Clock().c_str(), static_cast<unsigned long long>(req.userId), pl.index, team, status, fresh,
              pl.creature, forgot);
    if (hostctl::Active()) hostctl::PlayerAdded(static_cast<std::int64_t>(req.userId), pl.index);
    // D8b (G3a's rule): the surrender entry moves to the new connection (team, disconnected time, its slot in a running
    // vote) and its disconnection ends; during its team's vote the new client gets the vote's status (CA 03)
    DecideSurrender();
    surrender_.AddPlayer(conn, pl.index, static_cast<int>(team));
    surrender_.OnConnection(conn, true, NowS(), &surrenderOut_);
    return true;
  }

  // G3a/G3: the session's rejoin: an earlier connection that is still there (its loss not noticed yet) is superseded;
  // its entry goes (no departure: the server player went to `conn` in Rejoining).
  void OnRejoin(gms::ConnId conn, gms::ConnId previous, const gms::PlayerSlot& slot) override {
    if (previous == conn) return;
    auto it = players_.find(previous);
    if (it == players_.end()) return;
    gms::Logf("%s match: player %u's earlier connection is superseded by its rejoin; its entry goes",
              gms::Clock().c_str(), slot.gameplayIndex);
    players_.erase(it);
  }

  // G3b: the match-found prompt (connect type 1, [host] prepare_flags=1) was declined or not answered: the session sent
  // GameAborted and the clients answered 0x40000 (their non-culprits restart matchmaking by themselves); the host now
  // exits without finishGame, so dgserver removes the game (NotifyPlayerRemoved GAME_DESTROYED).
  void OnGameAborted(const std::vector<uint64_t>& culprits, const std::string& why) override {
    std::string users;
    for (uint64_t u : culprits) users += " " + std::to_string(u);
    gms::Logf("%s match: the game is aborted before its pre-game (%s; culprit user(s)%s); the host ends it",
              gms::Clock().c_str(), why.c_str(), users.c_str());
    hostctl::EndAborted(why.c_str());
  }

  // G3: createGame's human roster (the session's pre-game waits for each of them).
  std::vector<uint64_t> ExpectedHumans() override {
    std::vector<uint64_t> users;
    if (hostctl::Active() && hostctl::HasGame())
      for (const dggms::control::RosterEntry& e : hostctl::Game().roster)
        if (!e.bot && !e.spectator && e.user > 0) users.push_back(static_cast<uint64_t>(e.user));  // B15: no spectator
    return users;
  }

  // D6a: the bot players (bots.h), planned and created on the server at the first accepted HelloReq, for every client's
  // HelloPlayer (records of type 1) and first player updates. The humans: the connected players and createGame's roster
  // entries that haven't connected yet (their indexes are kept free; since G3b they count for their roster team too).
  std::vector<gms::OtherPlayer> OtherPlayers() override {
    std::vector<bots::Human> humans, waiting;
    for (const auto& kv : players_) {
      if (kv.second.index < 0 || kv.second.spectator) continue;  // B15b: a spectator takes no seat on a team
      bots::Human h;
      h.index = kv.second.index;
      unsigned status = 0, team = 0, idx = 0;
      if (SehReadPlayer(sim_, static_cast<unsigned>(h.index), &status, &team, &idx)) h.team = static_cast<int>(team);
      h.noun = SehPlayerNoun(sim_, static_cast<unsigned>(h.index));
      humans.push_back(h);
    }
    if (hostctl::Active() && hostctl::HasGame())
      for (const auto& e : hostctl::Game().roster)
        if (!e.bot && e.index >= 0 && e.index < static_cast<std::int64_t>(kMaxPlayers) && !used_[e.index]) {
          bots::Human w;
          w.index = static_cast<int>(e.index);
          w.team = static_cast<int>(e.team);
          waiting.push_back(w);
        }
    std::vector<gms::OtherPlayer> out;
    for (const bots::Bot& b : bots::EnsurePlayers(sim_, humans, waiting)) {
      gms::OtherPlayer p;
      p.gameplayIndex = static_cast<uint8_t>(b.index);
      p.team = static_cast<uint8_t>(b.team);
      p.onlineId = b.onlineId;
      p.isBot = true;
      p.status = b.status;
      out.push_back(p);
    }
    return out;
  }

  // Seam 5 (D2f): the session's player updates written from the server's cLabsPlayer by the client's writer.
  bool PlayerUpdate(gms::ConnId conn, uint8_t gameplayIndex, const std::vector<int>& members, gms::Bytes* msg) override {
    if (conn == gms::kNoConn && bots::IsBotIndex(gameplayIndex)) {
      // D6a: a bot's update also carries mRole (9), mbIsBot (11), mpBotData (15: the client's LabsPlayer_ReflectBotData
      // sets the noun, role and "<shaper> BOT" name) and mpNounDef (16, preloaded on pre-game 03)
      std::vector<int> m = members;
      for (int extra : {static_cast<int>(gms::kPlayerRole), static_cast<int>(gms::kPlayerIsBot), 15, 16})
        if (std::find(m.begin(), m.end(), extra) == m.end()) m.push_back(extra);
      std::sort(m.begin(), m.end());
      BoundTo b(sim_);
      if (!repl::Init()) return false;
      return repl::PlayerUpdate(sim_, gameplayIndex, m, msg);
    }
    if (conn == gms::kNoConn) {
      // G3: a departed human (the session's 0x4 and the newcomers' updates of it) from its kept server player
      if (!playerUpdates_ || !DepartedAt(gameplayIndex)) return false;
      BoundTo b(sim_);
      if (!repl::Init()) return false;
      return repl::PlayerUpdate(sim_, gameplayIndex, members, msg);
    }
    auto it = players_.find(conn);
    if (!playerUpdates_ || it == players_.end() || it->second.index != gameplayIndex) return false;
    BoundTo b(sim_);
    if (!repl::Init()) return false;
    const bool ok = repl::PlayerUpdate(sim_, gameplayIndex, members, msg);
    if (ok) ++it->second.playerUpdates;
    return ok;
  }

  // D3-live step 3 ([host] shaper_select=1; GMS_PROTOCOL.md *Shaper select on the match host (D3-live)*): the humans'
  // content ([host] shaper_list/skins until createGame carries it), the timers ([host] shaper_timers, else createGame's
  // timers keys, else the practice values for a practice createGame, else ShaperSelectTuning's), and the bots (D6a) as
  // the GMS-scripted players: each picks and locks its BotClass's shaper, team by team, one after the other.
  bool ShaperSelectSetup(gms::ShaperSelectConfig* config) override {
    if (!shaperSelect_) return false;
    config->timers = ShaperTimers();
    config->seed = o_.setup.seed;
    config->humanDefault = o_.shapers;
    if (config->humanDefault.list.empty()) {
      gms::ShaperListEntry e;
      e.noun = gms::kNounAlpha;
      e.flags = gms::kShaperOwned;
      config->humanDefault.list.push_back(e);
    }
    // step 4: each human's own content from createGame's roster (dgserver --shaper-select)
    std::string humans;
    if (hostctl::Active() && hostctl::HasGame())
      for (const dggms::control::RosterEntry& e : hostctl::Game().roster) {
        if (e.bot || !gms::HasShaperContent(e) || e.index < 0 || e.index >= static_cast<std::int64_t>(kMaxPlayers))
          continue;
        gms::ShaperSelectPlayer p;
        p.gameplayIndex = static_cast<uint8_t>(e.index);
        p.team = static_cast<uint8_t>(e.team);
        p.content = gms::ShaperContentFromRoster(e);
        config->players.push_back(p);
        char buf[160];
        std::snprintf(buf, sizeof buf,
                      " [%lld: user %lld, %zu shapers, skins of %zu, %zu saved, %zu wards, %zu announcers, %zu perk pages]",
                      static_cast<long long>(e.index), static_cast<long long>(e.user), e.shapers.size(), e.skins.size(),
                      e.savedSkins.size(), e.wards.size(), e.announcers.size(), e.perkPages.size());
        humans += buf;
      }
    if (!humans.empty())
      gms::Logf("%s match: shaper select content from createGame's roster:%s", gms::Clock().c_str(), humans.c_str());
    OtherPlayers();  // the bots are planned and created at the first accepted HelloReq (the session asks again later)
    int nth[3] = {};
    std::string desc;
    for (const bots::Bot& b : bots::Players()) {
      gms::ShaperSelectPlayer p;
      p.gameplayIndex = static_cast<uint8_t>(b.index);
      p.team = static_cast<uint8_t>(b.team);
      p.scripted = true;
      p.plan.noun = b.noun;
      const int k = b.team >= 0 && b.team <= 2 ? nth[b.team]++ : 0;
      p.plan.pickMs = botPickMs_ + k * botStepMs_;
      p.plan.lockMs = p.plan.pickMs + botLockMs_;
      p.plan.role = static_cast<uint8_t>(b.role >= 0 && b.role < gms::kNumRoles ? b.role : 0);
      config->players.push_back(p);
      char buf[96];
      std::snprintf(buf, sizeof buf, " [%d: team %d, 0x%08x r%d, pick %d ms, lock %d ms]", b.index, b.team, b.noun,
                    p.plan.role, p.plan.pickMs, p.plan.lockMs);
      desc += buf;
    }
    gms::Logf("%s match: shaper select (D3-live) set up: timers %u/%u/%u/%u ms (%s), %zu list entr%s for the humans, "
              "scripted players (the bots)%s",
              gms::Clock().c_str(), config->timers.pickMs, config->timers.rolePerkMs, config->timers.skinMs,
              config->timers.revealMs, timersFrom_.c_str(), config->humanDefault.list.size(),
              config->humanDefault.list.size() == 1 ? "y" : "ies", desc.empty() ? " none" : desc.c_str());
    return true;
  }

  // D3-live step 3: the reveal's picks into the server cLabsPlayers (before the reveal's updates, which seam 5 writes
  // from them, and before the 0x401 where the deploy reads mpNounDef). A bot keeps what LabsPlayer_ReflectBotData gave
  // it unless its pick differs (its shaper was taken by a teammate: the lock took another).
  void OnShaperPicks(const std::vector<gms::ShaperPick>& picks) override {
    for (const gms::ShaperPick& k : picks) {
      bool human = false;
      for (const auto& kv : players_) human = human || kv.second.index == k.gameplayIndex;
      const bots::Bot* bot = nullptr;
      for (const bots::Bot& b : bots::Players())
        if (b.index == k.gameplayIndex) bot = &b;
      if (!human && !bot) {
        gms::Logf("%s match: shaper select pick of player %u (0x%08x): no server player; not stored", gms::Clock().c_str(),
                  k.gameplayIndex, k.skin);
        continue;
      }
      if (bot && bot->noun == k.skin && bot->role == k.role) continue;  // the bot's own (ReflectBotData's)
      void* noun = k.skin ? SehFindAssetById(k.skin) : nullptr;
      const bool ok = noun && SehWritePick(sim_, k.gameplayIndex, noun, k.role, k.page, k.pageVersion);
      gms::Logf("%s match: shaper select pick of player %u (%s, team %u): shaper 0x%08x, skin 0x%08x (asset %p), role %u, "
                "perk page %u/%u (%zu attribute(s), %zu modifier(s)), status 0x%x -> server player %s",
                gms::Clock().c_str(), k.gameplayIndex, human ? "human" : "bot", k.team, k.shaper, k.skin, noun, k.role,
                k.page, k.pageVersion, k.loadout.size(), k.modifiers.size(), k.status,
                ok ? "written (mpNounDef, mRole, mLoadoutID/Version)" : "NOT written");
      if (ok && human) ++picksWritten_;
      // D3-perks: the page's stats into the server player's loadout before the deploy (the creature's attributes add
      // its Add entries), the rest at the deploy (SendWorld)
      if (ok && human && k.page) {
        pagePicks_[k.gameplayIndex] = k;
        perks::WritePlayerLoadout(sim_, k.gameplayIndex, k.loadout);
      }
    }
  }

  void Tick(gms::SteadyClock::time_point now, gms::ISender& out) override { inject_.Tick(now, out); }

  // After the simulation (GmsHostFlush, ADR-2's loop steps 4-5): the replication step (D2g-live: creates, seam 1,
  // updates, modifier ends; D2f's drain alone with [host] replicate = 0), the command intake's answers, the test-side
  // commands due and the test wave.
  void Flush(gms::ISender& out) {
    KindScope traffic(gms::timing::kTrafficOther);  // Q3c: what no block below claims (the HUD data, the end, recaps)
    // D8-live: after BF 00 the world is frozen for the clients ([host] postgame_replicate 0): the simulator's messages
    // are drained and dropped, no creates, deltas, HUD data, command answers or recaps go out. In the post-game the
    // client loads LootBagPresentation_Map_Victory (or leaves for the lobby) and drops the level's objects; updates
    // for them asserted "localObjectID != kObjIDNone" (SporeOnline_Common.cpp 1464/1413: d1c_run3, d1f_run1, d5_run2,
    // where the NPC level-ups at match 240 s sent 8 x 9F and 22 x A2 84 s after the end).
    if (frozen_) {
      FrozenStep();
      EndStep(out);
      InjectCommands();
      TickTestLua();
      return;
    }
    // D8b: surrender first: its availability byte goes into this tick's 8B delta, and a passed vote ends the match
    // here, so this Flush's passes send the final A9/AA and 8B and EndStep sends BF 00 after CA 03/CA 04
    SurrenderStep(out);
    // D7b: the item transaction answers before the replication step, so a client repeats a purchase on its copy of the
    // player before the AA delta of the same tick brings the new inventory and gold (in the other order its
    // LabsPlayer_CheckPurchase would run on the updated copy and assert).
    // D14a-live: since then every answer goes first ([host] answers_first 1): the original's send stub sent the B4s at
    // once, while the simulator ran, and the outgoing vector at the frame's end, so a BC (AbilityUpdate) generated after
    // an ability's start reached the client after its B4 kind 1. That B4 rewrites the GUID's cooldown record
    // (AgentSummary_OnActionCommandResponse 0x00658820: start +0x10, end +0x20), and Blink pays its cooldown in its own
    // script after the teleport (B4's end = its start), so with the B4 after the BC the HUD showed Blink ready although
    // the server refused it for 300 s (d14a_spells_a1c).
    std::vector<cmds::Answer> answers;
    if (answersFirst_) cmds::TakeAnswers(&answers);
    else cmds::TakeEarlyAnswers(&answers);
    SendAnswers(out, answers);
    if (replicate_ && WorldMask()) ReplicationStep(out);
    else if (draining_) DrainStep(out, nullptr);
    cmds::TakeAnswers(&answers);
    SendAnswers(out, answers);
    // D1h: the death processing's messages for one player (the death recap C9 at a player's death)
    std::vector<death::Outgoing> deathMsgs;
    death::TakeOutgoing(&deathMsgs);
    for (const auto& m : deathMsgs) {
      bool any = false;
      for (auto& kv : players_) {
        Player& pl = kv.second;
        if (!pl.world || pl.index != m.player) continue;
        out.Send(kv.first, m.msg, m.what);
        any = true;
      }
      if (!any)
        gms::Logf("%s match: %s for player %d, who has no connection with the world; dropped", gms::Clock().c_str(),
                  m.what.c_str(), m.player);
    }
    UiStep(out);
    EndStep(out);
    InjectCommands();
    TickWave();
    TickTestLua();
  }

  // The command intake's answers, each to the connections of the players in its mask that have the world.
  void SendAnswers(gms::ISender& out, const std::vector<cmds::Answer>& answers) {
    KindScope traffic(gms::timing::kTrafficAnswers);
    for (const auto& a : answers) {
      bool any = false;
      for (auto& kv : players_) {
        Player& pl = kv.second;
        if (!pl.world || pl.index < 0 || !((a.players >> pl.index) & 1)) continue;
        out.Send(kv.first, a.msg, a.what);
        ++pl.answers;
        any = true;
      }
      if (!any && ++droppedLogged_ <= 50)
        gms::Logf("%s match: %02x %s (players 0x%04x) for nobody with the world; dropped", gms::Clock().c_str(),
                  a.msg.empty() ? 0 : a.msg[0], a.msg.empty() ? "?" : gms::GmsName(a.msg[0]), a.players);
    }
  }

  // D2g-live: [host] replicate (1), create_types (1 = every networked noun type but spawn points, 0 = D2f's Tower,
  // creature, interactable), network_rate (1: GameSimulator_UpdateNetworkRateTimers every tick), log_first (3: sends
  // logged in full per wire byte before only the per-second counts are).
  void SetLiveReplication(bool replicate, bool allTypes, bool networkRate, int logFirst) {
    replicate_ = replicate;
    allTypes_ = allTypes;
    networkRate_ = networkRate;
    logFirst_ = logFirst;
  }
  // X2a: [host] ui_before_entergame (1: the full UI data at the `done` point; 0 = after EnterGame, D1c-live's order)
  void SetUiBeforeEnterGame(bool on) { uiBeforeEnterGame_ = on; }
  // D14a-live: [host] answers_first (1: every command answer before the tick's replicated messages, the original's
  // order; 0 = only the item transactions' answers first, the others after the replication step, D7-live's order)
  void SetAnswersFirst(bool on) { answersFirst_ = on; }
  bool NetworkRate() const { return networkRate_; }
  void OnNetworkRate(unsigned mask) {
    ++netRateTicks_;
    if (mask & 0x10) ++netRate200_;
  }

  // D2g-live test fixture ([host] test_wave = seconds after the first player's EnterGame, 0 = off): dgsim_wave.lua
  // spawns test_wave_count minions (test_wave_noun) beside the first Binding and sends them toward the second
  // (test_wave_from / test_wave_to, the top lane's); a wave minion whose HP reached 0 is marked for delete
  // test_wave_delete_ms later (a stand-in for D1f-live's death processing: the placeholder damage only kills).
  // D2g-live step 2: test_wave_team (an nTeamID name, 'Mortal'), test_wave_start (12: units from `from` toward `to`)
  // and test_wave_stop (8: units before `to`), so a T11 wave can be an enemy one that stays beyond every vision source.
  void SetTestWave(double at, int count, const char* noun, const char* from, const char* to, int deleteMs,
                   const char* team, double start, double stop) {
    waveAt_ = at;
    waveCount_ = count;
    waveNoun_ = noun;
    waveFrom_ = from;
    waveTo_ = to;
    waveDeleteMs_ = deleteMs;
    waveTeam_ = team;
    waveStart_ = start;
    waveStop_ = stop;
  }

  // D2g-live step 2 ([host] fog, fog_terrain, fog_grass, fog_stealth, fog_render, fog_log_limit): fog of war from the
  // vision model (gms/core/vision) instead of "everything visible" (fow.h).
  void SetFog(bool on, const fow::Options& o, int logLimit) {
    fog_ = on;
    fowOpt_ = o;
    fogLogLimit_ = logLimit;
  }

  // D2h ([host] fow_data_by_vision 1, fow_data_log_limit 200): a player's AA also to the players that see its
  // creature, the whole struct to each new recipient (FowDataStep); 0 = D2g-live's team-only deltas and G3's catch-up.
  void SetFowDataRouting(bool byVision, int logLimit) {
    fowDataByVision_ = byVision;
    fowDataLogLimit_ = logLimit;
  }

  // D1b-live test aid ([host] test_lua = "<seconds after EnterGame>:<file>[,<seconds>:<file>...]"): each Lua file (in
  // the game folder unless absolute; gms/engine/lua's dgsim_*.lua are installed there) runs once on the server
  // simulator's lua_State at its time, like the test wave's chunk; its string result is logged line by line.
  void SetTestLua(const char* spec) {
    std::string s(spec);
    size_t pos = 0;
    while (pos < s.size()) {
      size_t comma = s.find(',', pos);
      if (comma == std::string::npos) comma = s.size();
      const std::string item = s.substr(pos, comma - pos);
      const size_t colon = item.find(':');
      if (colon != std::string::npos && colon > 0) {
        TimedLua t;
        t.at = std::atof(item.substr(0, colon).c_str());
        t.file = item.substr(colon + 1);
        testLua_.push_back(t);
        Log("host: test_lua %.1f s after EnterGame: %s", t.at, t.file.c_str());
      } else if (!item.empty()) {
        Log("host: test_lua item '%s' is not <seconds>:<file>; skipped", item.c_str());
      }
      pos = comma + 1;
    }
  }

  // [host] commands_file: lines "<seconds after EnterGame> [p<gameplay index>] <hex message>" ('#' starts a comment;
  // "{caster}" is replaced by the player's creature id, little-endian, and since G3 "{creature:<index>}" by any
  // player's, e.g. a basic attack on the other human), fed to the command intake as if that player had sent them (the
  // test side). Without p<index> a line is the first player's in the game (the only one before G3); the time counts
  // from that player's EnterGame. D11: "xp <amount>" in place of the message gives that player XP then, and
  // "{nearest:<noun prefix>}"/"{nearestpos:<noun prefix>}" become the id/position of the live object nearest to its
  // creature (ResolveNearest; D12: "<prefix>@enemy|ally|npc[player]" filters by team); Q3c: "{casterpos}"/
  // "{creaturepos:<index>}" the position of its own/any player's creature (ResolvePositions; D12 had added the same
  // {casterpos}). D15: "gold <amount>" gives it gold, "report [<label>] [near <radius>]" logs its items, attributes and
  // modifiers (and those of the objects within the radius): cmds::GrantGoldNow, cmds::ItemReportNow.
  bool LoadCommandsFile(const char* path) {
    std::ifstream in(path);
    if (!in) {
      Log("host: commands file %s: cannot open", path);
      return false;
    }
    std::string line;
    int lineNo = 0;
    while (std::getline(in, line)) {
      ++lineNo;
      const size_t hash = line.find('#');
      if (hash != std::string::npos) line.resize(hash);
      const size_t first = line.find_first_not_of(" \t\r");
      if (first == std::string::npos) continue;
      line = line.substr(first);
      const size_t sp = line.find_first_of(" \t");
      if (sp == std::string::npos) {
        Log("host: commands file %s:%d: no message; skipped", path, lineNo);
        continue;
      }
      TimedCommand c;
      c.at = std::atof(line.substr(0, sp).c_str());
      c.hex = line.substr(sp + 1);
      const size_t hexStart = c.hex.find_first_not_of(" \t");
      if (hexStart != std::string::npos) c.hex = c.hex.substr(hexStart);
      if (c.hex.size() > 1 && (c.hex[0] == 'p' || c.hex[0] == 'P') && std::isdigit(static_cast<unsigned char>(c.hex[1]))) {
        const size_t end = c.hex.find_first_of(" \t");
        const size_t msg = end == std::string::npos ? end : c.hex.find_first_not_of(" \t\r", end);
        if (msg == std::string::npos) {
          Log("host: commands file %s:%d: no message after the player; skipped", path, lineNo);
          continue;
        }
        c.player = std::atoi(c.hex.c_str() + 1);
        c.hex = c.hex.substr(msg);
      }
      timed_.push_back(c);
    }
    Log("host: commands file %s: %zu command(s)", path, timed_.size());
    return true;
  }

  std::string StateNote(gms::ConnId conn) override {
    std::string note;
    auto it = players_.find(conn);
    if (it != players_.end() && it->second.index >= 0) {
      unsigned status = 0, team = 0, idx = 0;
      char buf[96];
      const char* kind = it->second.spectator ? "server spectator" : "server player";  // B15b
      if (SehReadPlayer(sim_, static_cast<unsigned>(it->second.index), &status, &team, &idx))
        std::snprintf(buf, sizeof buf, "%s %d: status 0x%x, team %u", kind, it->second.index, status, team);
      else
        std::snprintf(buf, sizeof buf, "%s %d: MISSING", kind, it->second.index);
      note = buf;
      for (const auto& kv : it->second.unhandled)
        note += std::string(note.empty() ? "" : ", ") + "to the match " + gms::GmsName(kv.first) + " x" +
                std::to_string(kv.second);
      for (const auto& kv : it->second.commands)
        note += std::string(note.empty() ? "" : ", ") + "commands " + gms::GmsName(kv.first) + " x" +
                std::to_string(kv.second);
      if (it->second.answers) note += ", answers sent " + std::to_string(it->second.answers);
    }
    if (it != players_.end() && it->second.world) {
      char buf[160];
      std::snprintf(buf, sizeof buf,
                    "world %d objects (%d with HP) in %d messages, shaper %08X; forwarded %d; player updates from the "
                    "server %d",
                    it->second.counts.created, it->second.counts.withHp, it->second.counts.messages,
                    it->second.creature, it->second.forwarded, it->second.playerUpdates);
      note += (note.empty() ? "" : "; ") + std::string(buf);
      if (replicate_) {
        std::snprintf(buf, sizeof buf,
                      "replicated: %d creates, %d deletes, %d updates, modifiers %d registered / %d ended, sent %d",
                      creates_, deletes_, updates_, modsRegistered_, modEnds_, it->second.replicated);
        note += "; " + std::string(buf);
        if (fog_) {
          std::snprintf(buf, sizeof buf, "fog: %d enters, %d exits, %d A3, kind 3 by position %d", fogEnters_,
                        fogExits_, fogA3_, fogKind3_);
          note += "; " + std::string(buf);
        }
      }
    }
    const std::string inj = inject_.StateNote(conn);
    if (!inj.empty()) note += (note.empty() ? "" : "; ") + inj;
    const std::string pg = postgame_.Describe(conn);  // D8-live
    if (!pg.empty()) note += (note.empty() ? "" : "; ") + pg;
    return note;
  }

  // Q3c: every message handed to the endpoint (the session's transport): counted by the current kind, and matched to
  // the pending commands of the player it goes to (perf.h).
  void OnSent(gms::ConnId conn, const gms::Bytes& msg) {
    auto it = players_.find(conn);
    perf::OnSend(it != players_.end() ? it->second.index : -1, msg.data(), msg.size());
  }

  // Q3c: the replication's records for perf's window (always the same names; -1 = unknown).
  void PerfGauges(gms::timing::Gauges* g) {
    int inUse = -1, capacity = -1;
    {
      BoundTo b(sim_);
      repl::LineOfSightPool(sim_, &inUse, &capacity);
    }
    g->push_back({"known_objects", knownObjects_});
    g->push_back({"modifier_records", static_cast<double>(mods_.Size())});
    g->push_back({"modifiers_ended", modEnds_});
    g->push_back({"line_of_sight_in_use", inUse});
    g->push_back({"fog_in_grass", fog_ && fogReady_ ? fogLast_.inGrass : -1});
    g->push_back({"fog_revealed", fog_ && fogReady_ ? fogLast_.revealed : -1});
    g->push_back({"pursuits", static_cast<double>(pursuits_.size())});
    g->push_back({"creates_total", creates_});
    g->push_back({"deletes_total", deletes_});
  }

  std::string Players() const {
    std::string s;
    for (const auto& kv : players_) {
      if (kv.second.index < 0) continue;
      unsigned status = 0, team = 0, idx = 0;
      char buf[96];
      if (SehReadPlayer(sim_, static_cast<unsigned>(kv.second.index), &status, &team, &idx))
        std::snprintf(buf, sizeof buf, " [%d: user %llu, %sstatus 0x%x, team %u]", kv.second.index,
                      static_cast<unsigned long long>(kv.second.user), kv.second.spectator ? "spectator, " : "",
                      status, team);
      else
        std::snprintf(buf, sizeof buf, " [%d: user %llu, no server player]", kv.second.index,
                      static_cast<unsigned long long>(kv.second.user));
      s += buf;
    }
    for (const auto& kv : departed_) {  // G3
      unsigned status = 0, team = 0, idx = 0;
      char buf[112];
      if (SehReadPlayer(sim_, static_cast<unsigned>(kv.second.index), &status, &team, &idx))
        std::snprintf(buf, sizeof buf, " [%d: user %llu, departed, status 0x%x, team %u, creature %08X]",
                      kv.second.index, static_cast<unsigned long long>(kv.first), status, team, kv.second.creature);
      else
        std::snprintf(buf, sizeof buf, " [%d: user %llu, departed, no server player]", kv.second.index,
                      static_cast<unsigned long long>(kv.first));
      s += buf;
    }
    if (rejoins_ || catchUps_) {
      char buf[64];
      std::snprintf(buf, sizeof buf, " (rejoins %d, creature catch-ups %d)", rejoins_, catchUps_);
      s += buf;
    }
    for (const bots::Bot& b : bots::Players()) {
      char buf[96];
      std::snprintf(buf, sizeof buf, " [%d: bot %s, team %d, creature %08X]", b.index, b.botClass.c_str(), b.team,
                    b.creature);
      s += buf;
    }
    return s.empty() ? std::string(" none") : s;
  }

  void SetSkipShaper(bool v) { skipShaper_ = v; }
  // B15b: [host] spectators (1 = a custom game's spectators are served, 0 = refused as before B15b) and spectator_team
  // (the mTeam of a spectator's player: 1 [assumed], see core/protocol/messages.h kSpectatorTeam)
  void SetSpectators(bool on, int team) {
    spectators_ = on;
    spectatorTeam_ = static_cast<std::uint8_t>(team);
  }
  // D3-live step 3: shaper select on ([host] shaper_select), whether [host] shaper_timers gave the timers (o_'s
  // shaperTimers), and the bots' pick plan ([host] shaper_bot_pick_ms, shaper_bot_step_ms, shaper_bot_lock_ms).
  void SetShaperSelect(bool on, bool timersGiven, int botPickMs, int botStepMs, int botLockMs) {
    shaperSelect_ = on;
    shaperTimersGiven_ = timersGiven;
    botPickMs_ = botPickMs;
    botStepMs_ = botStepMs;
    botLockMs_ = botLockMs;
  }
  int PicksWritten() const { return picksWritten_; }
  // D8-live: the post-game's timings and rewards ([host] postgame_*, rewards), whether the world is replicated after the
  // end ([host] postgame_replicate), the session clock's zero (C1 02's server ms) and the seed of the rewards' base
  // rolls (createGame's).
  void SetPostGame(const gms::postgame::Options& o, bool ownInFinish, bool replicateAfterEnd, unsigned seed) {
    postgame_ = gms::postgame::PostGame(o);
    postgameOptions_ = o;
    ownInFinish_ = ownInFinish;
    replicateAfterEnd_ = replicateAfterEnd;
    seed_ = seed;
  }
  void SetSessionStart(gms::SteadyClock::time_point t) { sessionStart_ = t; }
  // D8b: surrender ([host] surrender: -1 auto = matchmade and custom games, 0 off, 1 on; practice games keep it off
  // [assumed: a lone practice player's request would pass alone]) and the test aids that override the level's tuning
  // (-1 = the level's): surrender_after_s (availability 1 at this match second instead of 20 minutes),
  // surrender_vote_s, surrender_cooldown_s, surrender_disconnect_s.
  struct SurrenderKeys {
    int mode = -1;
    int afterS = -1, voteS = -1, cooldownS = -1, disconnectS = -1;
    int teamLeftS = 60;  // G3b: team_left_surrender_s (-1 = off)
  };
  void SetSurrender(const SurrenderKeys& k) {
    surrenderKeys_ = k;
    teamLeft_ = gms::surrender::TeamLeftWatch(k.teamLeftS);
  }
  std::string SurrenderState() const {
    return surrender_.Describe() + (surrenderOn_ ? "" : " (off: the vote never becomes available)");
  }
  // The host's own rewards for a player (PostGame's fallback; with [host] rewards=own also playerFinish.rewards): the
  // practice rule (gms::postgame::PracticeRewards) with a base roll from the game's seed and the gameplay index.
  gms::postgame::Rewards OwnRewards(int index, bool won, unsigned long long durationMs, uint32_t descriptors) const {
    std::mt19937 rng(seed_ * 2654435761u + static_cast<unsigned>(index) + 1u);
    const double roll = std::uniform_real_distribution<double>(0.0, 1.0)(rng);
    return gms::postgame::PracticeRewards(won, durationMs / 1000.0, descriptors, roll);
  }
  bool OwnInFinish() const { return ownInFinish_; }
  void SetCommands(bool v) { commands_ = v; }
  // D2f: build the world from the server simulator (world), deploy the player's shaper (deploy), forward the
  // simulator's own messages (drain), write the player updates from the server player (player_updates); [host] keys
  // of the same names, all on by default.
  void SetReplication(bool world, bool deploy, bool drain, bool playerUpdates) {
    world_ = world;
    deploy_ = deploy;
    drain_ = drain;
    playerUpdates_ = playerUpdates;
  }

 private:
  struct Player {
    int index = -1;
    uint64_t user = 0;
    std::map<uint8_t, int> unhandled;  // client messages the session handed to the match, by wire byte
    bool world = false;                // the initial world was sent (D2f)
    uint32_t creature = 0;             // the shaper's server id
    repl::WorldCounts counts;
    int forwarded = 0;                 // messages from the outgoing vector sent to this player
    int playerUpdates = 0;             // LabsPlayerGlobalUpdates written from the server's cLabsPlayer (seam 5)
    std::map<uint8_t, int> commands;   // client messages the command intake took (D7-live), by wire byte
    int answers = 0;                   // command answers and updates sent (D7-live)
    bool inGame = false;               // EnterGame sent
    gms::SteadyClock::time_point enterGame{};
    int replicated = 0;                // messages of the replication step sent to this player (D2g-live)
    bool ui = false;                   // D1c-live: the first (full) UIDataState and UISpiritWellUpdate went out
    bool readyLogged = false;          // D8-live: the first status with 0x80000 was logged
    // G3: the other players' mCurrentCreatureId this client has (index -> creature), and whether the catch-up of the
    // other players' state (A9 3-7, the team's AA) is due (set when it got its world)
    std::map<int, uint32_t> creatureSent;
    bool catchUp = false;
    bool rejoined = false;  // G3: a rejoin; its catch-up also carries its own A9 3-7 and AA (its kills, gold, items)
    bool spectator = false;  // B15b: a spectator (gameplay index 10-14): no creature, no team, no input
  };

  // B15b: the spectators that have the world (bit = gameplay index 10-14).
  uint16_t SpectatorMask() const {
    uint16_t m = 0;
    for (const auto& kv : players_)
      if (kv.second.spectator && kv.second.world && kv.second.index >= 0)
        m = static_cast<uint16_t>(m | (1u << kv.second.index));
    return m;
  }

  // G3: a player that left, or whose rejoin replaces its client, knows no object any more: its bit goes from every
  // object's known and visible masks (network component +0xE/+0x10), so the world at its next 0x401 creates every
  // object again (BuildWorld skips the known ones; g3_run7d's rejoin got only what was made while it was away, then
  // 193 "localObjectID != kObjIDNone" asserts on the updates of the rest). Returns the objects changed, -1 on an error.
  int ForgetObjects(int index) {
    if (index < 0 || index >= 16) return 0;
    BoundTo b(sim_);
    if (!repl::Init()) return -1;
    std::vector<repl::NetObject> objs;
    if (repl::Scan(sim_, &objs) < 0) return -1;
    const uint16_t bit = static_cast<uint16_t>(1u << index);
    int n = 0;
    for (const repl::NetObject& o : objs)
      if (((o.known | o.visible) & bit) && repl::RemovePlayerBits(o.object, bit)) ++n;
    return n;
  }

  // G3: the departed human with this gameplay index, or null.
  const Player* DepartedAt(int index) const {
    for (const auto& kv : departed_)
      if (kv.second.index == index) return &kv.second;
    return nullptr;
  }

  // G3: the creature of any player (connected, departed or bot); 0 if none.
  uint32_t CreatureOf(int index) const {
    for (const auto& kv : players_)
      if (kv.second.index == index) return kv.second.creature;
    if (const Player* d = DepartedAt(index)) return d->creature;
    for (const bots::Bot& b : bots::Players())
      if (b.index == index) return b.creature;
    return 0;
  }
  struct TimedCommand {
    double at = 0;    // seconds after the player's EnterGame
    int player = -1;  // G3: the gameplay index ("p<index>"); -1 = the first player in the game (before G3: always)
    std::string hex;  // the whole message, wire byte first
    bool done = false;
  };

  // D3-live: the shaper select timers: [host] shaper_timers; else createGame's timers keys (ShaperSelectionTime,
  // RolePerkSelectTime, SkinSelectTime, RevealTime, ms; a missing key keeps the mode's value) over the mode's values
  // (practice: 300000/300000/15500/16, else ShaperSelectTuning's 62000/45500/15500/16).
  gms::ShaperSelectTimers ShaperTimers() {
    if (shaperTimersGiven_) {
      timersFrom_ = "[host] shaper_timers";
      return o_.shaperTimers;
    }
    if (!hostctl::Active() || !hostctl::HasGame()) {
      timersFrom_ = "ShaperSelectTuning's";
      return gms::ShaperTimersNormal();
    }
    return gms::ShaperTimersFromCreateGame(hostctl::Game(), &timersFrom_);
  }

  static std::string IdHexLE(uint32_t v) {
    char id[12];
    std::snprintf(id, sizeof id, "%02x%02x%02x%02x", v & 0xFF, (v >> 8) & 0xFF, (v >> 16) & 0xFF, v >> 24);
    return id;
  }

  // D11: {nearest:<prefix>} -> the id (little-endian hex) of the live object nearest to the player's creature whose noun
  // starts with <prefix> (e.g. Minion_Spirit, Creep_Jungle_Fish, highhuntress); {nearestpos:<prefix>} -> its position
  // (three floats). False (logged) if a placeholder finds nothing; the command is then dropped. D12: "<prefix>@<filter>"
  // narrows it to combatants by team ({nearest:@enemyplayer}, {nearestpos:@allyplayer}, {nearest:Creep@npc}; SehNearest).
  bool ResolveNearest(const Player& pl, std::string* hex) {
    for (;;) {
      const size_t at = hex->find("{nearest");
      if (at == std::string::npos) return true;
      const size_t colon = hex->find(':', at);
      const size_t close = hex->find('}', at);
      if (colon == std::string::npos || close == std::string::npos || colon > close) {
        Log("host: commands file: bad placeholder in '%s'; skipped", hex->c_str());
        return false;
      }
      const bool wantPos = hex->compare(at, 12, "{nearestpos:") == 0;
      std::string prefix = hex->substr(colon + 1, close - colon - 1);
      std::string filter;  // D12: "<prefix>@<filter>" (SehNearest)
      const size_t atSign = prefix.find('@');
      if (atSign != std::string::npos) {
        filter = prefix.substr(atSign + 1);
        prefix.resize(atSign);
      }
      std::uint32_t id = 0;
      float pos[3] = {0.f, 0.f, 0.f};
      bool found;
      {
        BoundTo b(sim_);
        found = SehNearest(sim_, pl.creature, prefix.c_str(), filter.c_str(), &id, pos);
      }
      if (!found) {
        Log("host: commands file: no live object '%s*' for %s; the command is dropped", prefix.c_str(),
            hex->substr(at, close - at + 1).c_str());
        return false;
      }
      std::string rep;
      if (wantPos) {
        const unsigned char* b = reinterpret_cast<const unsigned char*>(pos);
        for (int i = 0; i < 12; ++i) {
          char two[4];
          std::snprintf(two, sizeof two, "%02x", b[i]);
          rep += two;
        }
      } else {
        rep = IdHexLE(id);
      }
      Log("host: commands file: %s -> %08X at (%.2f, %.2f, %.2f)", hex->substr(at, close - at + 1).c_str(), id, pos[0],
          pos[1], pos[2]);
      hex->replace(at, close - at + 1, rep);
    }
  }

  // Q3c: {casterpos} -> the position of the player's creature, {creaturepos:<index>} -> that of any player's creature
  // (three floats, as {nearestpos:...}), e.g. a cast at the caster's own feet (a zero-length aim) or the target point of
  // a cast at an enemy shaper. False (logged) if the creature is missing; the command is then dropped.
  bool ResolvePositions(const Player& pl, std::string* hex) {
    for (;;) {
      size_t at = hex->find("{casterpos}"), len = 11;
      std::uint32_t id = pl.creature;
      if (at == std::string::npos) {
        at = hex->find("{creaturepos:");
        if (at == std::string::npos) return true;
        const size_t close = hex->find('}', at);
        if (close == std::string::npos) {
          Log("host: commands file: bad placeholder in '%s'; skipped", hex->c_str());
          return false;
        }
        id = CreatureOf(std::atoi(hex->c_str() + at + 13));
        len = close + 1 - at;
      }
      float pos[3] = {0.f, 0.f, 0.f};
      bool found;
      {
        BoundTo b(sim_);
        found = SehObjectPos(id, pos);
      }
      const std::string what = hex->substr(at, len);
      if (!found) {
        Log("host: commands file: %s: no creature %08X; the command is dropped", what.c_str(), id);
        return false;
      }
      std::string rep;
      const unsigned char* b = reinterpret_cast<const unsigned char*>(pos);
      for (int i = 0; i < 12; ++i) {
        char two[4];
        std::snprintf(two, sizeof two, "%02x", b[i]);
        rep += two;
      }
      Log("host: commands file: %s -> %08X at (%.2f, %.2f, %.2f)", what.c_str(), id, pos[0], pos[1], pos[2]);
      hex->replace(at, len, rep);
    }
  }

  void InjectCommands() {
    if (timed_.empty()) return;
    const auto now = gms::SteadyClock::now();
    const Player* first = nullptr;  // lines without p<index>: the first player in the game (players_'s order)
    gms::ConnId firstConn = 0;
    for (const auto& kv : players_)
      if (kv.second.inGame && kv.second.index >= 0 && !kv.second.spectator) {  // B15b: a player, not a spectator
        first = &kv.second;
        firstConn = kv.first;
        break;
      }
    for (TimedCommand& c : timed_) {
      if (c.done) continue;
      const Player* pl = c.player < 0 ? first : nullptr;
      gms::ConnId conn = c.player < 0 ? firstConn : 0;
      for (const auto& kv : players_)
        if (c.player >= 0 && kv.second.index == c.player && kv.second.inGame) {
          pl = &kv.second;
          conn = kv.first;
        }
      if (!pl) continue;
      const double secs = std::chrono::duration<double>(now - pl->enterGame).count();
      if (secs < c.at) continue;
      c.done = true;
      if (pl->spectator) {
        // B15b test aid: "p<10-14> <hex>" is a spectator's message as its client would send it: the session's OnMessage
        // would hand it to OnClientMessage, which takes only CA and C1 from a spectator (so CA 01 tests the refusal)
        gms::Bytes m;
        if (!gms::ParseHex(c.hex, m) || m.empty()) continue;
        gms::Logf("%s match: injected (test side) %.1f s after EnterGame for spectator %d: %s", gms::Clock().c_str(),
                  secs, pl->index, gms::Hex(m, 80).c_str());
        OnClientMessage(conn, m[0], m.data() + 1, m.size() - 1);
        continue;
      }
      std::string hex = c.hex;
      if (hex.compare(0, 3, "xp ") == 0) {  // D11: "xp <amount>", XP to the player now (levels and ability points)
        BoundTo b(sim_);
        gms::Logf("%s match: injected (test side) %.1f s after EnterGame for player %d: %s", gms::Clock().c_str(), secs,
                  pl->index, hex.c_str());
        cmds::GrantXpNow(sim_, static_cast<unsigned>(pl->index), static_cast<float>(std::atof(hex.c_str() + 3)),
                         "injected");
        continue;
      }
      if (hex.compare(0, 5, "gold ") == 0) {  // D15: "gold <amount>", gold to the player now (nGoldSourceType.Cheat)
        BoundTo b(sim_);
        gms::Logf("%s match: injected (test side) %.1f s after EnterGame for player %d: %s", gms::Clock().c_str(), secs,
                  pl->index, hex.c_str());
        cmds::GrantGoldNow(sim_, static_cast<unsigned>(pl->index), static_cast<float>(std::atof(hex.c_str() + 5)),
                           "injected");
        continue;
      }
      if (hex.compare(0, 6, "report") == 0) {  // D15: "report [<label>] [near <radius>]", the item report (commands.h)
        std::string label = "report";
        float radius = 0.f;  // (not "near": windef.h defines it away)
        std::istringstream words(hex.substr(6));
        std::string w;
        while (words >> w) {
          if (w == "near" && (words >> w)) radius = static_cast<float>(std::atof(w.c_str()));
          else label = w;
        }
        BoundTo b(sim_);
        cmds::ItemReportNow(sim_, static_cast<unsigned>(pl->index), label.c_str(), radius);
        continue;
      }
      if (!ResolvePositions(*pl, &hex)) continue;  // Q3c: {casterpos}, {creaturepos:<index>}
      for (size_t at = hex.find("{caster}"); at != std::string::npos; at = hex.find("{caster}"))
        hex.replace(at, 8, IdHexLE(pl->creature));
      for (size_t at = hex.find("{creature:"); at != std::string::npos; at = hex.find("{creature:")) {
        const size_t close = hex.find('}', at);
        if (close == std::string::npos) break;
        const int other = std::atoi(hex.c_str() + at + 10);
        hex.replace(at, close + 1 - at, IdHexLE(CreatureOf(other)));
      }
      if (!ResolveNearest(*pl, &hex)) continue;
      gms::Bytes m;
      if (!gms::ParseHex(hex, m) || m.empty()) {
        Log("host: commands file: '%s' is no hex message; skipped", c.hex.c_str());
        continue;
      }
      gms::Logf("%s match: injected (test side) %.1f s after EnterGame for player %d: %s", gms::Clock().c_str(), secs,
                pl->index, gms::Hex(m, 80).c_str());
      if (m[0] == gms::kGmsSurrenderMsgs) {  // D8b: a test-side CA 01/CA 02 goes to the vote as the player's own
        const bool took =
            surrender_.OnClientMessage(NowS(), ServerMs(), conn, m.data() + 1, m.size() - 1, &surrenderOut_);
        gms::Logf("%s match: injected CA %s for player %d %s; %s", gms::Clock().c_str(),
                  gms::Hex(m.data() + 1, m.size() - 1).c_str(), pl->index,
                  took ? "taken by the surrender vote" : "NOT taken", surrender_.Describe().c_str());
        continue;
      }
      BoundTo b(sim_);
      if (!cmds::OnClientMessage(sim_, static_cast<unsigned>(pl->index), m[0], m.data() + 1, m.size() - 1, "injected"))
        gms::Logf("%s match: injected %02x is no command message; ignored", gms::Clock().c_str(), m[0]);
    }
  }

  // D2f (ADR-2 decision 5, seam 2 and the shaper): the player's creature is deployed on the server, then every
  // networked object of a visible type is created for the player from the server object; then the outgoing vector,
  // which holds the level's ModifierCreated and the deploy's PlayerCharacterDeploy, is forwarded (seam 1), and from
  // now on every tick. Also writes what was sent to <data dir>\world_p<index>.txt (gms_script's inject format).
  void SendWorld(gms::ConnId conn, gms::ISender& out) {
    KindScope traffic(gms::timing::kTrafficCreates);  // Q3c: the world at the client's 0x401
    auto it = players_.find(conn);
    if (it == players_.end() || it->second.index < 0 || it->second.world) return;
    Player& pl = it->second;
    BoundTo b(sim_);
    if (!repl::Init()) {
      gms::Logf("%s match: replication unavailable (see the log above); no world for player %d", gms::Clock().c_str(),
                pl.index);
      return;
    }
    if (pl.spectator) {
      // B15b: a spectator has no shaper; it gets every object (BuildWorld with its bit), the fog never hides one from it.
      // The bots deploy before the first world, a spectator's too (a custom game of bots watched by a spectator)
      const int deployed = deploy_ ? bots::DeployAll(sim_) : 0;
      if (deployed)
        for (const bots::Bot& bot : bots::Players())
          perks::ApplyBotLoadout(sim_, static_cast<unsigned>(bot.index), bot.creature);
      gms::Logf("%s match: spectator %d: no deploy; its world is every networked object%s", gms::Clock().c_str(),
                pl.index,
                deployed ? (" (" + std::to_string(deployed) + " bot creature(s) deployed first)").c_str() : "");
    } else if (deploy_ && pl.creature) {
      // G3: a rejoin: its shaper is still in the world (LabsPlayer_DeployCharacter would replace it)
      gms::Logf("%s match: player %d rejoined: its creature %08X stays (no second deploy)", gms::Clock().c_str(),
                pl.index, pl.creature);
    } else if (deploy_) {
      pl.creature = repl::DeployShaper(sim_, static_cast<unsigned>(pl.index));
      death::OnPlayerDeployed(pl.index);  // D1f-live: starting gold (MATCH_FLOW.md R10 §5)
      // D3-perks: the perk page's modifiers (and its non-Add entries) on the new creature
      auto page = pagePicks_.find(static_cast<std::uint8_t>(pl.index));
      if (page != pagePicks_.end())
        perks::ApplyAtDeploy(sim_, static_cast<unsigned>(pl.index), pl.creature, page->second.loadout,
                             page->second.modifiers);
      // D6a: the bots' creatures at their team's start points, once, before the first world is built
      const int deployed = bots::DeployAll(sim_);
      if (deployed) {
        gms::Logf("%s match: %d bot creature(s) deployed before player %d's world", gms::Clock().c_str(), deployed,
                  pl.index);
        for (const bots::Bot& bot : bots::Players())  // D3-perks: the bots' stand-in for a perk page
          perks::ApplyBotLoadout(sim_, static_cast<unsigned>(bot.index), bot.creature);
      }
    }
    std::vector<repl::WorldMessage> world;
    // D2g-live: with the field states (the first incremental pass then sends nothing), the replicated noun types and
    // the objects' modifier records from the registry
    repl::BuildWorld(sim_, static_cast<unsigned>(pl.index), allTypes_, &ServerMatch::RecordsOf, this, &world,
                     &pl.counts);
    std::vector<std::string> lines;
    for (const auto& m : world) {
      out.Send(conn, m.msg, "world: " + m.what);
      counts_.Add(m.msg[0], m.msg.size());
      lines.push_back("loaded:" + gms::Hex(m.msg, m.msg.size()));
    }
    pl.world = true;
    pl.catchUp = true;  // G3: the other players' state after this tick's delta pass (CatchUpStep)
    gms::Logf("%s match: world for player %d from the server simulator: %d networked objects, %d created (%d with "
              "attributes or HP), %d not sent, %d failed, %d messages; shaper %08X",
              gms::Clock().c_str(), pl.index, pl.counts.networked, pl.counts.created, pl.counts.withHp,
              pl.counts.skipped, pl.counts.failed, pl.counts.messages, pl.creature);
    if (drain_) {
      draining_ = true;
      DrainStep(out, &lines);
    }
    // seam 5: the server player's mCurrentCreatureId (PlayerCharacterDeploy above sets the same client field)
    gms::Bytes upd;
    if (playerUpdates_ && pl.creature &&
        repl::PlayerUpdate(sim_, static_cast<unsigned>(pl.index), {gms::kPlayerCurrentCreature}, &upd)) {
      out.Send(conn, upd, "player update from the server: mCurrentCreatureId");
      lines.push_back("loaded:" + gms::Hex(upd, upd.size()));
      ++pl.playerUpdates;
    }
    WriteWorldFile(pl.index, lines);
  }

  // The players that have the world (bit i = gameplay index i).
  uint16_t WorldMask() const {
    uint16_t m = 0;
    for (const auto& kv : players_)
      if (kv.second.world && kv.second.index >= 0) m = static_cast<uint16_t>(m | (1u << kv.second.index));
    return m;
  }

  // The players with the world on `index`'s team; B15b: and the spectators with the world (a player's AA,
  // labsPlayerFowData, is fogged from the other team only; the spectator's HUD shows both teams [assumed]).
  uint16_t TeamMask(unsigned index) const {
    std::uint8_t team = 0, t = 0;
    if (!repl::PlayerTeam(sim_, index, &team)) return 0;
    uint16_t m = SpectatorMask();
    for (const auto& kv : players_)
      if (kv.second.world && kv.second.index >= 0 && !kv.second.spectator &&
          repl::PlayerTeam(sim_, kv.second.index, &t) && t == team)
        m = static_cast<uint16_t>(m | (1u << kv.second.index));
    return m;
  }

  // The players whose A9/AA the delta pass sends: every connection with the world and, D6a, every deployed bot; G3,
  // every departed human with a creature (its shaper is still in the game). B15b: no spectator (the session sends its
  // status; it has nothing else).
  std::vector<unsigned> ReplicatedPlayers() const {
    std::vector<unsigned> v;
    for (const auto& kv : players_)
      if (kv.second.world && kv.second.index >= 0 && !kv.second.spectator)
        v.push_back(static_cast<unsigned>(kv.second.index));
    for (const auto& kv : departed_)
      if (kv.second.creature && kv.second.index >= 0) v.push_back(static_cast<unsigned>(kv.second.index));
    for (const bots::Bot& b : bots::Players())
      if (b.creature) v.push_back(static_cast<unsigned>(b.index));
    return v;
  }

  // G3: what a client lacks of the other players when it got its world after them (a second human, a late joiner, a
  // rejoin): their mCurrentCreatureId, once its client has the creature (the deploy's A9 member 2 went to that player
  // alone, a bot's to the players with the world then; the tObjID member maps through ObjectCreateComplete's map, so
  // the creature must exist on the client first), and once, at its world, every other player's kills, deaths,
  // assists and death times (A9 3-7) and its teammates' whole labsPlayerFowData (AA 0-62). After the delta pass, so the
  // field states are current and the later deltas go to it like to everyone.
  void CatchUpStep(gms::ISender& out, const std::vector<repl::NetObject>& objs, const std::vector<uint16_t>& createdNow) {
    KindScope traffic(gms::timing::kTrafficPlayers);
    std::vector<std::pair<int, uint32_t>> creatures;  // every player with a creature: gameplay index, server id
    for (const auto& kv : players_)
      if (kv.second.index >= 0 && kv.second.creature) creatures.push_back({kv.second.index, kv.second.creature});
    for (const auto& kv : departed_)
      if (kv.second.index >= 0 && kv.second.creature) creatures.push_back({kv.second.index, kv.second.creature});
    for (const bots::Bot& b : bots::Players())
      if (b.creature) creatures.push_back({b.index, b.creature});
    for (auto& kv : players_) {
      Player& r = kv.second;
      if (!r.world || r.index < 0) continue;
      const uint16_t bit = static_cast<uint16_t>(1u << r.index);
      for (const auto& pc : creatures) {
        if (pc.first == r.index) continue;
        auto sent = r.creatureSent.find(pc.first);
        if (sent != r.creatureSent.end() && sent->second == pc.second) continue;
        bool has = false;
        for (size_t i = 0; i < objs.size(); ++i)
          if (objs[i].id == pc.second) {
            has = ((objs[i].known | createdNow[i]) & bit) != 0;
            break;
          }
        if (!has) continue;
        gms::Bytes m;
        if (!repl::PlayerUpdate(sim_, static_cast<unsigned>(pc.first), {gms::kPlayerCurrentCreature}, &m)) continue;
        out.Send(kv.first, m, "player " + std::to_string(pc.first) + "'s mCurrentCreatureId " + Hex8(pc.second) +
                                  " (G3 catch-up)");
        r.creatureSent[pc.first] = pc.second;
        ++catchUps_;
      }
      if (!r.catchUp) continue;
      r.catchUp = false;
      std::uint8_t myTeam = 0, team = 0;
      repl::PlayerTeam(sim_, static_cast<unsigned>(r.index), &myTeam);
      int sentA9 = 0, sentAA = 0;
      std::vector<int> fow;
      for (int m = 0; m <= 62; ++m) fow.push_back(m);
      for (const auto& pc : creatures) {
        if (pc.first == r.index && !r.rejoined) continue;  // a rejoin gets its own too (its new client has 0s)
        gms::Bytes m;
        if (repl::PlayerUpdate(sim_, static_cast<unsigned>(pc.first), {3, 4, 5, 6, 7}, &m)) {
          out.SendQuiet(kv.first, m);
          ++sentA9;
        }
        // B15b: a spectator gets every player's AA (TeamMask gives it the deltas of both teams too). D2h: with
        // fow_data_by_vision FowDataStep sends the whole AA to every new recipient, this one included
        if (!fowDataByVision_ && repl::PlayerTeam(sim_, static_cast<unsigned>(pc.first), &team) &&
            (r.spectator || team == myTeam) && repl::PlayerFowUpdate(sim_, static_cast<unsigned>(pc.first), fow, &m)) {
          out.SendQuiet(kv.first, m);
          ++sentAA;
        }
      }
      gms::Logf("%s match: G3 catch-up for %s %d: %d %splayers' A9 (kills, deaths, assists, death times), %s",
                gms::Clock().c_str(), r.spectator ? "spectator" : "player", r.index, sentA9,
                r.rejoined ? "(own included, a rejoin) " : "other ",
                fowDataByVision_
                    ? "the AA by FowDataStep (D2h: the whole labsPlayerFowData of every player it gets it from)"
                    : (std::to_string(sentAA) + (r.spectator ? " players'" : " teammates'") +
                       " AA (the whole labsPlayerFowData)")
                          .c_str());
    }
  }

  // D2h ([host] fow_data_by_vision, 1): every player's labsPlayerFowData (AA: XP, gold, level, points, ranks, items) to
  // its own team and the spectators, as before, and to every player that currently sees its creature (the creature's
  // known & visible masks after this tick's fog transitions: GameSimulator_GetPlayersThatSeeObject's rule, the
  // binary's own for data about an object fog of war hides; gms/core/replication/fow_data.h). A player that starts
  // receiving it (an enemy that sees the creature again or for the first time, a new world, a rejoin) gets the whole
  // struct (members 0-62) once, written after the delta (which advances the one field state), so a level gained in
  // fog shows at once; the others get the delta. After FogStep: an exit's 94/full state/95 come first.
  void FowDataStep(gms::ISender& out) {
    KindScope traffic(gms::timing::kTrafficPlayers);
    const uint16_t world = WorldMask();
    std::map<unsigned, uint32_t> creatures;  // gameplay index -> its creature (players, departed humans, bots)
    for (const auto& kv : players_)
      if (kv.second.index >= 0 && kv.second.creature) creatures[static_cast<unsigned>(kv.second.index)] = kv.second.creature;
    for (const auto& kv : departed_)
      if (kv.second.index >= 0 && kv.second.creature) creatures[static_cast<unsigned>(kv.second.index)] = kv.second.creature;
    for (const bots::Bot& b : bots::Players())
      if (b.creature) creatures[static_cast<unsigned>(b.index)] = b.creature;
    static const std::vector<int> kAll = [] {
      std::vector<int> v;
      for (int m = 0; m < gms::kFowDataMembers; ++m) v.push_back(m);
      return v;
    }();
    for (const unsigned index : ReplicatedPlayers()) {
      uint16_t seers = 0;
      const auto c = creatures.find(index);
      const uint32_t creature = c != creatures.end() ? c->second : 0;
      if (creature && !repl::ObjectSeers(creature, &seers)) seers = 0;
      const uint16_t team = TeamMask(index);
      const uint16_t to = gms::FowDataRecipients(team, seers, world, true);
      const gms::FowDataSends s = fowRouter_.Route(static_cast<int>(index), to);
      gms::Bytes m;
      if (repl::PlayerFowDelta(sim_, index, &m) > 0) {  // always: it advances the field state
        if (s.delta) {
          SendTo(out, s.delta, m, nullptr);
          ++fowDataDeltas_;
          if (s.delta & ~team) ++fowDataDeltasEnemy_;
        }
        ++updates_;
      }
      std::uint32_t level = 0;
      if ((s.full | s.dropped) & ~team) repl::PlayerLevel(sim_, index, &level);
      if (s.full) {
        gms::Bytes f;
        if (repl::PlayerFowUpdate(sim_, index, kAll, &f)) {
          SendTo(out, s.full, f, nullptr);
          ++fowDataFull_;
          if (s.full & ~team) {
            ++fowDataFullEnemy_;
            if (++fowDataLogged_ <= fowDataLogLimit_)
              Log("fow data: player %u's whole labsPlayerFowData (AA, %zu B, level %u) to player(s) 0x%04x, who see its "
                  "creature %08X (team 0x%04x, seers 0x%04x)",
                  index, f.size(), level, s.full & ~team, creature, team, seers);
          }
        } else {
          fowRouter_.Unsync(static_cast<int>(index), s.full);  // logged by PlayerFowUpdate; tried again next tick
        }
      }
      if (s.dropped & ~team) {
        ++fowDataDrops_;
        if (++fowDataLogged_ <= fowDataLogLimit_)
          Log("fow data: player %u's creature %08X out of sight for player(s) 0x%04x: its AA held back there (level %u "
              "now)",
              index, creature, s.dropped & ~team, level);
      }
    }
  }

  static gms::Bytes RecordsOf(void* ctx, uint32_t carrier) {
    return static_cast<ServerMatch*>(ctx)->mods_.RecordsFor(carrier);
  }

  // One message to the players in `players` that have the world. `what` = a logged send (the session's line), null =
  // quiet (counted in the per-second line; the first log_first of each wire byte are logged in full here). Returns how
  // many players got it.
  int SendTo(gms::ISender& out, uint16_t players, const gms::Bytes& m, const char* what) {
    if (m.empty()) return 0;
    int n = 0;
    for (auto& kv : players_) {
      Player& pl = kv.second;
      if (!pl.world || pl.index < 0 || !((players >> pl.index) & 1)) continue;
      if (what) out.Send(kv.first, m, what);
      else out.SendQuiet(kv.first, m);
      counts_.Add(m[0], m.size());
      ++pl.replicated;
      ++n;
    }
    if (n && !what && ++firstLogged_[m[0]] <= logFirst_)
      Log("replication: %02x %s to players 0x%04x (first of its kind): %s", m[0], gms::GmsName(m[0]), players,
          gms::Hex(m, 96).c_str());
    return n;
  }

  // Seam 1 (D2f, with D2g-live's fixes): the outgoing vector's entries to the players they name among those that have
  // the world. Every ModifierCreated/Updated goes into the registry first (whoever gets it); kind 2 of an object
  // deleted in the same tick goes to its deletion's recipients; a deletion drops the object's registry entries.
  // `lines` (the world file): at the world point the sends are logged one by one (D2f's way), else quietly.
  void DrainStep(gms::ISender& out, std::vector<std::string>* lines) {
    KindScope traffic(gms::timing::kTrafficForwarded);
    BoundTo b(sim_);
    std::vector<repl::Outgoing> msgs;
    repl::DrainCounts dc;
    repl::Drain(sim_, &msgs, &dc);
    released_ += dc.released;
    if (dc.entries == 0) return;
    std::vector<gms::OutgoingRoute> routes(msgs.size());
    for (size_t i = 0; i < msgs.size(); ++i) {
      const repl::Outgoing& m = msgs[i];
      gms::OutgoingRoute& r = routes[i];
      r.kind = m.kind;
      r.object = m.object;
      r.recipients = m.recipients;
      r.objectAlive = m.objectAlive;
      r.seers = m.seers;
      // D2g-live step 2: with fog a positional message goes to the players whose team sees the point (its line-of-sight
      // record: position, team, radius), handed to the router as kind 1 with that mask
      if (fog_ && fogReady_ && m.kind == 3 && m.hasPoint) {
        if (fogPlayersTick_.empty()) fogPlayersTick_ = FogPlayers();
        r.kind = 1;
        const uint8_t teams = model_.TeamsSeeingPoint(m.x, m.y, m.team, m.radius);
        r.recipients = gms::vision::PlayersForTeams(teams, fogPlayersTick_);
        ++fogKind3_;
        if (!(r.recipients & WorldMask())) ++fogKind3Hidden_;
        if (++fogKind3Logged_ <= fogLogLimit_ / 10)
          Log("fog: kind 3 %02x %s at (%.1f, %.1f), team %u, radius %.1f -> teams %u, players 0x%04x",
              m.msg.empty() ? 0 : m.msg[0], m.msg.empty() ? "?" : gms::GmsName(m.msg[0]), m.x, m.y, m.team, m.radius,
              teams, r.recipients);
      }
      if (m.msg.size() > 1 && m.msg[0] == gms::kGmsModifierCreated) {
        if (mods_.OnCreated(m.msg.data() + 1, m.msg.size() - 1)) ++modsRegistered_;
        else Log("replication: ModifierCreated of %zu B not registered: %s", m.msg.size() - 1, gms::Hex(m.msg).c_str());
      } else if (m.msg.size() > 1 && m.msg[0] == gms::kGmsModifierUpdated) {
        if (!mods_.OnUpdated(m.msg.data() + 1, m.msg.size() - 1) && ++unknownUpdates_ <= 20)
          Log("replication: ModifierUpdated for an unregistered instance: %s", gms::Hex(m.msg).c_str());
      }
    }
    fogPlayersTick_.clear();
    gms::RouteOutgoing(&routes, kAllPlayers);
    const uint16_t world = WorldMask();
    int sent = 0, dropped = 0, viaDeletion = 0;
    for (size_t i = 0; i < msgs.size(); ++i) {
      const repl::Outgoing& m = msgs[i];
      const gms::OutgoingRoute& r = routes[i];
      const uint16_t to = static_cast<uint16_t>(r.players & world);
      if (m.kind == 0) {
        animShadow_.erase(m.object);
        const int recs = mods_.RemoveCarrier(m.object);
        ++deletes_;
        Log("replication: delete %08X -> ObjectDelete to players 0x%04x (known 0x%04x)%s", m.object, to, r.recipients,
            recs ? (", " + std::to_string(recs) + " modifier record(s) dropped").c_str() : "");
      }
      if (r.fromDeletion) ++viaDeletion;
      if (!to) {
        ++dropped;
        if (++droppedLogged_ <= 50)
          gms::Logf("%s match: outgoing %02x %s (kind %d, object %08X, players 0x%04x) for nobody with the world; "
                    "dropped",
                    gms::Clock().c_str(), m.msg.empty() ? 0 : m.msg[0], m.msg.empty() ? "?" : gms::GmsName(m.msg[0]),
                    m.kind, m.object, r.players);
        continue;
      }
      char what[96];
      std::snprintf(what, sizeof what, "from the simulator (kind %d%s)", m.kind,
                    m.kind == 0 || m.kind == 2 ? (", object " + Hex8(m.object)).c_str() : "");
      const int n = SendTo(out, to, m.msg, lines || m.kind == 0 ? what : nullptr);
      if (lines)
        for (int k = 0; k < n; ++k) lines->push_back("loaded:" + gms::Hex(m.msg, m.msg.size()));
      for (auto& kv : players_)
        if (kv.second.world && kv.second.index >= 0 && ((to >> kv.second.index) & 1)) ++kv.second.forwarded;
      sent += n;
    }
    forwardedTotal_ += sent;
    forwardedViaDeletion_ += viaDeletion;
    if (lines || dropped || dc.kinds[0] || dc.unreadable || viaDeletion)
      gms::Logf("%s match: outgoing vector: %d entries (delete %d, recipients %d, object %d, position %d, unreadable %d; "
                "%d line-of-sight record(s) released); %d sent, %d for nobody yet, %d routed by their deletion",
                gms::Clock().c_str(), dc.entries, dc.kinds[0], dc.kinds[1], dc.kinds[2], dc.kinds[3], dc.unreadable,
                dc.released, sent, dropped, viaDeletion);
  }

  // D2g-live (docs/GMS_PROTOCOL.md *Live replication (D2g-static)*, *One replication step*), after the simulation:
  //   0. fog of war ([host] fog, D2g-live step 2): the vision model's answer for every scanned object, written into
  //      mVisibleToTeam (+0x888) (VisionStep);
  //   1. creates: every replicated networked object a player with the world doesn't know (8E ... 8F with the
  //      registry's records; with the field states if nobody knew it), then its known and visible bits;
  //   2. the outgoing vector (DrainStep), after the creates, so a new object's own messages follow its 8F; with fog,
  //      positional messages (kind 3) go to the players whose team sees the point;
  //   3. updates: per known object and struct the changed members (mode 8 against the field state) to the players
  //      that know and see it (not those that just got its create); with nobody seeing it the field state still
  //      advances (a fog exit sends the full state); A9 to every player, AA to the player's team;
  //   4. modifier ends: ModifierDeleted for every registered instance the server no longer has;
  //   5. fog of war: the per-player transitions (FogStep: 93; 94 + full state + 95; A3) and the visible masks (+0x10).
  // Without fog every object is visible to everyone who knows it (+0x10 = +0xE).
  void ReplicationStep(gms::ISender& out) {
    BoundTo b(sim_);
    if (!repl::Init()) return;
    const uint16_t world = WorldMask();
    std::vector<repl::NetObject> objs;
    if (repl::Scan(sim_, &objs) < 0) return;
    std::vector<gms::vision::Entity> ents;
    std::vector<fow::ObjectInfo> info;
    if (fog_) VisionStep(objs, &ents, &info);
    std::vector<uint16_t> createdNow(objs.size(), 0);
    int known = 0;
    KindScope traffic(gms::timing::kTrafficCreates);  // Q3c: then forwarded (DrainStep), deltas, players, fog
    for (size_t i = 0; i < objs.size(); ++i) {
      const repl::NetObject& o = objs[i];
      if (!repl::Replicated(o.nounType, allTypes_)) continue;
      const uint16_t missing = static_cast<uint16_t>(world & ~o.known);
      if (o.known) ++known;
      if (!missing || failedCreates_.count(o.id)) continue;
      std::vector<repl::WorldMessage> msgs;
      int bytes = 0;
      if (!repl::BuildCreate(o, o.known == 0, mods_.RecordsFor(o.id), &msgs, &bytes) ||
          !repl::AddPlayerBits(o.object, missing, missing)) {
        failedCreates_.insert(o.id);  // logged by BuildCreate; not retried every tick
        continue;
      }
      for (const auto& m : msgs) SendTo(out, missing, m.msg, nullptr);
      createdNow[i] = missing;
      ++creates_;
      ++known;
    }
    if (drain_) DrainStep(out, nullptr);
    perf::SetKind(gms::timing::kTrafficDeltas);
    for (size_t i = 0; i < objs.size(); ++i) {
      const repl::NetObject& o = objs[i];
      const uint16_t knownHere = static_cast<uint16_t>(o.known & world & ~createdNow[i]);
      const uint16_t to = static_cast<uint16_t>(knownHere & o.visible);
      if (!knownHere || !repl::Replicated(o.nounType, allTypes_)) continue;
      for (int p = 0; p < repl::kNumParts; ++p) {
        gms::Bytes m;
        if (repl::BuildDelta(o, p, &m) > 0) {
          if (to) {
            SendTo(out, to, m, nullptr);
            ++updates_;
          } else {
            ++fogAdvanced_;  // everyone who knows it has it fogged: the field state advances, the exit sends it all
          }
        }
      }
    }
    // D2g-live step 3: cAnimationData has no field state, so the host keeps what it sent of the overrides (members 0-6)
    // and sends 9E with the ones that changed (seeded at the object's first pass, after its create sent them)
    for (size_t i = 0; i < objs.size(); ++i) {
      const repl::NetObject& o = objs[i];
      const uint16_t knownAll = static_cast<uint16_t>((o.known | createdNow[i]) & world);
      if (!knownAll || !repl::Replicated(o.nounType, allTypes_)) continue;
      AnimShadow& sh = animShadow_[o.id];
      gms::Bytes m;
      if (repl::AnimationOverrideDelta(o, sh.overrides, &sh.seeded, &m) > 0) {
        const uint16_t to = static_cast<uint16_t>(knownAll & o.visible & ~createdNow[i]);
        if (to) SendTo(out, to, m, nullptr);
        ++animDeltas_;
      }
    }
    perf::SetKind(gms::timing::kTrafficPlayers);
    for (const unsigned index : ReplicatedPlayers()) {
      gms::Bytes m;
      if (repl::PlayerGlobalDelta(sim_, index, &m) > 0) {
        SendTo(out, world, m, nullptr);
        ++updates_;
      }
      // D2h: with [host] fow_data_by_vision the AA goes out in FowDataStep, after this tick's fog transitions
      if (!fowDataByVision_ && repl::PlayerFowDelta(sim_, index, &m) > 0) {
        SendTo(out, TeamMask(index), m, nullptr);
        ++updates_;
      }
    }
    CatchUpStep(out, objs, createdNow);  // G3
    perf::SetKind(gms::timing::kTrafficDeltas);  // the modifier ends
    for (uint32_t inst : mods_.Instances()) {
      if (repl::InstanceExists(inst)) continue;
      const uint32_t carrier = mods_.CarrierOf(inst);
      gms::Bytes ad;
      mods_.Remove(inst, &ad);
      uint16_t seers = 0;
      const bool alive = repl::ObjectSeers(carrier, &seers);
      const uint16_t to = static_cast<uint16_t>(alive ? seers & world : 0);
      if (to) SendTo(out, to, ad, nullptr);
      ++modEnds_;
      Log("replication: modifier %08X on %08X ended -> ModifierDeleted to players 0x%04x%s; registry %zu", inst, carrier,
          to, alive ? "" : " (carrier gone: not sent)", mods_.Size());
    }
    if (fog_ && fogReady_) FogStep(out, objs, ents, info, createdNow);
    if (fowDataByVision_) FowDataStep(out);  // D2h
    knownObjects_ = known;
    ReportCounts();
  }

  // The players with the world and their teams (FogPlanner's and PlayersForTeams' input).
  std::vector<gms::vision::FogPlayer> FogPlayers() const {
    std::vector<gms::vision::FogPlayer> v;
    for (const auto& kv : players_) {
      if (!kv.second.world || kv.second.index < 0) continue;
      if (kv.second.spectator) {  // B15b: sees everything (no fog transitions; its client hides by mVisibleToTeam)
        v.push_back(gms::vision::ObserverFogPlayer(kv.second.index));
        continue;
      }
      std::uint8_t team = 0xFF;
      repl::PlayerTeam(sim_, static_cast<unsigned>(kv.second.index), &team);
      v.push_back({kv.second.index, team});
    }
    return v;
  }

  // Step 0 (fog): the inputs from the server simulator (fow::Read: objects, records, proxies, the maps), the vision
  // model's update, its answer into every scanned object's mVisibleToTeam.
  void VisionStep(const std::vector<repl::NetObject>& objs, std::vector<gms::vision::Entity>* ents,
                  std::vector<fow::ObjectInfo>* info) {
    fow::ReadCounts rc;
    const auto tickAt = gms::SteadyClock::now();
    unsigned elapsedMs = 0;  // the grass reveal timers' step (D2g-rest)
    if (visionLast_ != gms::SteadyClock::time_point{}) {
      const auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(tickAt - visionLast_).count();
      elapsedMs = ms > 0 ? static_cast<unsigned>(ms < 1000 ? ms : 1000) : 0;
    }
    visionLast_ = tickAt;
    fogReady_ = fow::Read(sim_, objs, fowOpt_, elapsedMs, ents, info, &rc);
    if (!fogReady_) {
      if (!fogUnavailableLogged_) {
        fogUnavailableLogged_ = true;
        Log("fog: no line-of-sight manager or map on the server simulator; fog of war off until one appears");
      }
      return;
    }
    gms::vision::Params p;
    p.terrain = fowOpt_.terrain ? fow::Terrain() : nullptr;
    p.grass = fowOpt_.grass;
    p.grassMap = fowOpt_.grass ? fow::Grass() : nullptr;
    p.stealth = fowOpt_.stealth;
    model_.Update(*ents, p);
    std::vector<uint8_t> bits(objs.size(), 0);
    for (size_t i = 0; i < objs.size(); ++i) bits[i] = model_.Bits(i);
    // D2g-rest: the record's +0x51 mask = the teams that would see the object without grass and stealth (the terrain
    // test only), which LabsAbilities_CreateAbilityObject reads to reveal a caster in grass (RevealInGrass)
    if (p.grass || p.stealth) {
      gms::vision::Params tp;
      tp.terrain = p.terrain;
      modelTerrain_.Update(*ents, tp);
      std::vector<uint8_t> tbits(objs.size(), 0);
      for (size_t i = 0; i < objs.size(); ++i) tbits[i] = modelTerrain_.Bits(i);
      fogTerrainMaskWrites_ += fow::WriteTerrainMask(objs, *ents, tbits);
    }
    // D2g-rest: the grass ids as the host writes them (the client's renderer compares them with its grass outlines'),
    // and the reveals (RevealInGrass's timers)
    for (size_t i = 0; i < objs.size() && i < info->size(); ++i) {
      const fow::ObjectInfo& in = (*info)[i];
      if (in.grassChanged && ++fogGrassLogged_ <= fogLogLimit_)
        Log("fog: %08X %-30s team %u at (%.1f, %.1f): mServerGrassId 0x%08X%s", objs[i].id, objs[i].name,
            (*ents)[i].team, in.pos[0], in.pos[1], in.grassId, in.grassId ? " (in grass)" : " (out of the grass)");
      const uint8_t was = revealedLast_.count(objs[i].id) ? revealedLast_[objs[i].id] : 0;
      if ((in.revealed != was || in.revealEnded) && ++fogGrassLogged_ <= fogLogLimit_)
        Log("fog: %08X %-30s in grass: revealed to team bits %u (was %u%s), concealment 0x%02X", objs[i].id,
            objs[i].name, in.revealed, was, in.revealEnded ? ", a reveal timer ran out" : "", (*ents)[i].grassConceal);
      if (in.revealed) revealedLast_[objs[i].id] = in.revealed;
      else revealedLast_.erase(objs[i].id);
    }
    // With the terrain, grass or stealth test: the same model without them, as evidence which teams they keep from an
    // object (logged when that set changes; counted per tick)
    if (p.terrain || p.grass || p.stealth) {
      modelFree_.Update(*ents, gms::vision::Params());
      std::map<uint32_t, uint8_t> now;
      for (size_t i = 0; i < objs.size(); ++i) {
        const uint8_t hidden = static_cast<uint8_t>(modelFree_.Bits(i) & ~bits[i]);
        if (!(*ents)[i].id || !hidden) continue;
        now[objs[i].id] = hidden;
        ++fogTerrainHidden_;
        auto it = terrainHidden_.find(objs[i].id);
        if ((it == terrainHidden_.end() || it->second != hidden) && ++fogLogged_ <= fogLogLimit_)
          Log("fog: %08X %-30s team %u at (%.1f, %.1f)%s%s: team bits %u within reach but hidden by terrain, grass or "
              "stealth (seen by %u)",
              objs[i].id, objs[i].name, (*ents)[i].team, (*info)[i].pos[0], (*info)[i].pos[1],
              (*ents)[i].grassId ? " in grass" : "", (*ents)[i].stealthed ? " stealthed" : "", hidden, bits[i]);
      }
      terrainHidden_.swap(now);
    }
    fogVisWrites_ += fow::WriteVisibility(objs, *ents, bits);
    // D2g-pursuit: each team that sees an object gets its position as the last-seen pursuit position (record +0x2C +
    // team x 8), which a follow takes for a target its follower doesn't see; a team that doesn't keeps its value
    if (fowOpt_.pursuit) {
      int fresh = 0;
      fogLastSeenWrites_ += fow::WriteLastSeen(objs, *ents, *info, bits, &fresh);
      fogLastSeenFresh_ += fresh;
    }
    PursuitWatch(objs);
    fogRadiusWrites_ += rc.radiusWrites;
    fogGrassWrites_ += rc.grassWrites;
    fogLast_ = rc;
    if (rc.revealed) ++fogRevealTicks_;
    if (rc.failed && ++fogFailLogged_ <= 20) Log("fog: %d object(s) could not be read this tick", rc.failed);
  }

  // D2g-pursuit, evidence: every follow of a scanned object (cLocomotionData +8 target, +0xC seen, +0xC8 goal). A line
  // when a follow of a hidden target starts or its goal moves, when the follower sees its target again or loses it,
  // and when a follow of a hidden target ends (up to fog_log_limit; a goal at the map origin always, up to 50); counted
  // per tick for the per-second fog line.
  void PursuitWatch(const std::vector<repl::NetObject>& objs) {
    std::vector<fow::Pursuit> ps;
    fow::ReadPursuits(objs, &ps);
    std::map<uint32_t, PursuitState> now;
    for (const fow::Pursuit& p : ps) {
      const repl::NetObject& f = objs[p.follower];
      PursuitState s;
      s.target = p.target;
      s.seen = p.targetSeen;
      s.goal[0] = p.goal[0];
      s.goal[1] = p.goal[1];
      s.pos[0] = f.pos[0];
      s.pos[1] = f.pos[1];
      std::snprintf(s.name, sizeof s.name, "%s", f.name);
      ++pursuitTicks_;
      const auto it = pursuits_.find(f.id);
      const bool had = it != pursuits_.end() && it->second.target == p.target;
      const bool origin = std::fabs(p.goal[0]) < 2.f && std::fabs(p.goal[1]) < 2.f;
      if (!p.targetSeen) {
        ++pursuitHiddenTicks_;
        if (origin) ++pursuitOriginTicks_;
        const bool moved = !had || it->second.seen || std::fabs(it->second.goal[0] - p.goal[0]) > 0.5f ||
                           std::fabs(it->second.goal[1] - p.goal[1]) > 0.5f;
        if (moved && (origin ? ++pursuitOriginLogged_ <= 50 : ++pursuitLogged_ <= fogLogLimit_))
          Log("pursuit: %08X %-28s (team %u) at (%.1f, %.1f) follows %08X, hidden from it: goal (%.1f, %.1f, %.1f), the "
              "target's last-seen position for team %u %s(%.1f, %.1f) (its position at the follow's refresh (%.1f, "
              "%.1f))%s",
              f.id, f.name, p.team, f.pos[0], f.pos[1], p.target, p.goal[0], p.goal[1], p.goal[2], p.team,
              p.targetTracked ? "" : "(no record) ", p.lastSeen[0], p.lastSeen[1], p.targetPos[0], p.targetPos[1],
              origin ? "; AT THE MAP ORIGIN" : "");
      } else if (had && !it->second.seen && ++pursuitLogged_ <= fogLogLimit_) {
        Log("pursuit: %08X %-28s at (%.1f, %.1f) sees its target %08X again: follows it, goal (%.1f, %.1f)", f.id, f.name,
            f.pos[0], f.pos[1], p.target, p.goal[0], p.goal[1]);
      }
      now[f.id] = s;
    }
    for (const auto& kv : pursuits_) {
      const auto it = now.find(kv.first);
      if (it != now.end() && it->second.target == kv.second.target) continue;
      if (!kv.second.seen && ++pursuitLogged_ <= fogLogLimit_)
        Log("pursuit: %08X %-28s ended its follow of the hidden %08X at (%.1f, %.1f), goal (%.1f, %.1f)", kv.first,
            kv.second.name, kv.second.target, kv.second.pos[0], kv.second.pos[1], kv.second.goal[0],
            kv.second.goal[1]);
    }
    pursuits_.swap(now);
  }

  // Step 5 (fog): FogPlanner's transitions for every replicated object some player with the world knows, executed in
  // its order; then the visible masks. Docs: GMS_PROTOCOL.md *The per-player transitions*.
  void FogStep(gms::ISender& out, const std::vector<repl::NetObject>& objs, const std::vector<gms::vision::Entity>& ents,
               const std::vector<fow::ObjectInfo>& info, const std::vector<uint16_t>& createdNow) {
    KindScope traffic(gms::timing::kTrafficFog);
    const uint16_t world = WorldMask();
    const std::vector<gms::vision::FogPlayer> players = FogPlayers();
    std::vector<gms::vision::FogObject> fos;
    std::vector<size_t> at;
    fos.reserve(objs.size());
    for (size_t i = 0; i < objs.size() && i < ents.size(); ++i) {
      const repl::NetObject& o = objs[i];
      if (!ents[i].id || !repl::Replicated(o.nounType, allTypes_)) continue;
      const uint16_t known = static_cast<uint16_t>(o.known | createdNow[i]);
      if (!(known & world)) continue;
      gms::vision::FogObject f;
      f.id = o.id;
      f.known = known;
      f.visible = static_cast<uint16_t>(o.visible | createdNow[i]);
      f.teamBits = model_.Bits(i);
      f.maintainPos = info[i].maintain;
      f.x = info[i].pos[0];
      f.y = info[i].pos[1];
      fos.push_back(f);
      at.push_back(i);
    }
    std::vector<uint16_t> after;
    const std::vector<gms::vision::FogAction> actions = planner_.Plan(fos, players, &after);
    struct Exit {
      std::vector<repl::WorldMessage> state, after;  // between 94 and 95; after 95 (step 3: 92, B1)
    };
    std::map<size_t, Exit> fullStates;  // per object, built once per tick
    for (const gms::vision::FogAction& a : actions) {
      const size_t i = at[a.objectIndex];
      const repl::NetObject& o = objs[i];
      const uint16_t bit = static_cast<uint16_t>(1u << a.player);
      if (a.kind == gms::vision::FogActionKind::kEnter) {
        SendTo(out, bit, gms::vision::BuildObjectEnterFoW(o.id), nullptr);
        ++fogEnters_;
        if (++fogLogged_ <= fogLogLimit_)
          Log("fog: %08X %-30s team %u at (%.1f, %.1f) enters fog for player %d (team bits %u)", o.id, o.name,
              ents[i].team, info[i].pos[0], info[i].pos[1], a.player, model_.Bits(i));
      } else if (a.kind == gms::vision::FogActionKind::kExit) {
        float euler[3];
        gms::vision::QuaternionToEuler(info[i].quat, euler);
        SendTo(out, bit, gms::vision::BuildObjectPrepareForExitFoW(o.id, info[i].pos, euler), nullptr);
        auto it = fullStates.find(i);
        if (it == fullStates.end()) {
          Exit e;
          int bytes = 0;
          if (!repl::BuildFullState(o, &e.state, &e.after, &bytes)) e = Exit();  // logged; 95 still enables the object
          it = fullStates.emplace(i, std::move(e)).first;
        }
        int bytes = 0;
        for (const auto& m : it->second.state) {
          SendTo(out, bit, m.msg, nullptr);
          bytes += static_cast<int>(m.msg.size());
        }
        const gms::Bytes recs = mods_.RecordsFor(o.id);
        SendTo(out, bit, gms::vision::BuildObjectExitFoW(o.id, recs), nullptr);
        for (const auto& m : it->second.after) {
          SendTo(out, bit, m.msg, nullptr);
          bytes += static_cast<int>(m.msg.size());
        }
        ++fogExits_;
        fogExitBytes_ += bytes;
        if (++fogLogged_ <= fogLogLimit_)
          Log("fog: %08X %-30s team %u at (%.1f, %.1f) exits fog for player %d (team bits %u): 94, %zu full-state "
              "message(s) (%d B), 95 with %zu modifier record(s)",
              o.id, o.name, ents[i].team, info[i].pos[0], info[i].pos[1], a.player, model_.Bits(i),
              it->second.state.size() + it->second.after.size(), bytes, recs.size() / gms::kModifierRecordSize);
      } else {
        SendTo(out, bit, gms::vision::BuildDisabledObjectUpdate(o.id, info[i].pos[0], info[i].pos[1]), nullptr);
        ++fogA3_;
      }
    }
    for (size_t k = 0; k < fos.size(); ++k)
      if (after[k] != fos[k].visible && !fow::SetVisibleMask(objs[at[k]].object, after[k]))
        Log("fog: visible mask of %08X not written", fos[k].id);
  }

  // The per-second line: messages per wire byte, plus the registry, the line-of-sight pool and the network-rate
  // timers. Nothing sent: a line every 10 s.
  void ReportCounts() {
    const auto now = gms::SteadyClock::now();
    if (countsSince_ == gms::SteadyClock::time_point{}) countsSince_ = now;
    const double secs = std::chrono::duration<double>(now - countsSince_).count();
    if (secs < 1.0 || (counts_.Empty() && secs < 10.0)) return;
    int inUse = -1, capacity = -1;
    repl::LineOfSightPool(sim_, &inUse, &capacity);
    Log("replication: last %.1f s: %d messages, %zu bytes%s%s; %d objects known, %d creates, %d deletes, %d updates, "
        "modifiers %zu registered (%d ended); line-of-sight pool %d/%d (%d released); network-rate ticks %u (200 ms bit "
        "%u); animation override deltas %d",
        secs, counts_.Messages(), counts_.TotalBytes(), counts_.Empty() ? "" : ": ", counts_.Format().c_str(),
        knownObjects_, creates_, deletes_, updates_, mods_.Size(), modEnds_, inUse, capacity, released_, netRateTicks_,
        netRate200_, animDeltas_);
    if (fog_ && fogReady_)
      Log("fog: %d enters, %d exits (%d B of full state), %d A3, %d mVisibleToTeam writes, %d mVisionRadius / %d grass-id "
          "writes, %d deltas held back (nobody sees the object), kind 3 by position %d (%d seen by nobody); now %d "
          "tracked, %d sources, %d proxies, %d unscanned records; hidden by terrain/grass/stealth %zu objects now (%d "
          "object-ticks); %d in grass, %d revealed (%d ticks with a reveal), %d terrain-mask writes; pursuit: %d "
          "last-seen writes (%d new records), %zu follows now, follow object-ticks %d (%d of hidden targets, %d with a "
          "goal at the map origin)",
          fogEnters_, fogExits_, fogExitBytes_, fogA3_, fogVisWrites_, fogRadiusWrites_, fogGrassWrites_, fogAdvanced_,
          fogKind3_, fogKind3Hidden_, fogLast_.tracked, fogLast_.sources, fogLast_.proxies, fogLast_.unscanned,
          terrainHidden_.size(), fogTerrainHidden_, fogLast_.inGrass, fogLast_.revealed, fogRevealTicks_,
          fogTerrainMaskWrites_, fogLastSeenWrites_, fogLastSeenFresh_, pursuits_.size(), pursuitTicks_,
          pursuitHiddenTicks_, pursuitOriginTicks_);
    // D2h: the FoW data's sends, when they changed
    const int fowDataNow = fowDataDeltas_ + fowDataFull_ + fowDataDrops_;
    if (fowDataByVision_ && fowDataNow != fowDataReported_) {
      fowDataReported_ = fowDataNow;
      Log("fow data: %d AA deltas (%d also to enemies that see the creature), %d whole structs to new recipients (%d to "
          "enemies that see the creature), %d drops out of sight",
          fowDataDeltas_, fowDataDeltasEnemy_, fowDataFull_, fowDataFullEnemy_, fowDataDrops_);
    }
    counts_.Clear();
    countsSince_ = now;
  }

  // D1c-live: the HUD's battleground data (MATCH_FLOW.md *Game-mode handlers, energy lines and spirit wells (R10)* §3,
  // §4.3). A player in the game gets the full UIDataState once (with mNodeCapacity/mCampCapacity, so the client creates
  // its well and camp lists) and the full UISpiritWellUpdate after it; then everyone who has them gets the deltas (the
  // match clock once a second, energy lines, towers, the wells' lock timers). The first full one is written with the
  // field states (the later deltas compare against it), a later player's without.
  //
  // X2a: the full UIDataState must reach the client before EnterGame. GameClient_OnGmsEnterGame (0x006B5CD0) calls
  // Client_StartGame synchronously, whose TowerGraphics_Init (0x00539310) copies mNumTowersPerLane (+0xD0) once as the
  // stride of its per-tower state slots (0x01542889). Sent after EnterGame (D1c-live until X2a), the copy was 0, so
  // tier i of all four (team, lane) groups shared slot i: from the first Binding death on, TowerGraphics_Update
  // (0x00538D50) saw that slot change to "destroyed" every frame and replayed tower_death_effect (the visual effect,
  // Play_Minion_Tower_explo, the TowerDestroyed feedback, the Binding-destroyed UI event) each time, until the Wwise
  // lower-engine pool ran dry (SPWwiseSystem.cpp 274; docs/ASSERTS.md *X2a*). So the full UI data goes out at the
  // session's `done` point (after the world, one step before EnterGame); the after-EnterGame send below stays the
  // fallback ([host] ui_before_entergame 0 = the old order, or a player without the world).
  bool SendUiFull(gms::ConnId conn, Player& pl, gms::ISender& out, const char* when) {
    const bool withFieldState = !uiFieldStateUsed_;
    gms::Bytes m;
    const int members = repl::UIDataUpdate(sim_, true, withFieldState, &m);
    if (members > 0) {
      out.Send(conn, m, "UIDataState (full: match time, towers, energy lines, well and camp capacities)");
      counts_.Add(m[0], m.size());
    }
    gms::Bytes w;
    const int wellMembers = repl::SpiritWellUpdate(sim_, true, withFieldState, &w);
    if (wellMembers > 0) {
      out.Send(conn, w, "UISpiritWellUpdate (full: the four spirit wells)");
      counts_.Add(w[0], w.size());
    }
    // D2g-live step 3: the creep camps (8D), after the 8B that gave the client its camp list
    gms::Bytes c;
    const int campMembers = repl::CampUpdate(sim_, true, withFieldState, &c);
    if (campMembers > 0) {
      out.Send(conn, c, "UICreepCampUpdate (full: the creep camps)");
      counts_.Add(c[0], c.size());
    }
    gms::Logf("%s match: UI data for player %d %s: 8B %d members (%zu B), 8C %d members (%zu B), 8D %d members (%zu B)%s",
              gms::Clock().c_str(), pl.index, when, members, m.size(), wellMembers, w.size(), campMembers, c.size(),
              withFieldState ? ", with the field states" : "");
    uiFieldStateUsed_ = true;
    pl.ui = true;
    return members > 0;
  }

  // X2a: at the session's `done` point, the player's full UI data before its EnterGame (SendUiFull above).
  void UiBeforeEnterGame(gms::ConnId conn, gms::ISender& out) {
    if (!uiBeforeEnterGame_ || !match::Enabled() || !replicate_) return;
    auto it = players_.find(conn);
    if (it == players_.end() || it->second.ui || it->second.index < 0) return;
    if (!it->second.world) {
      gms::Logf("%s match: player %d has no world at its `done` point; its UI data waits for EnterGame",
                gms::Clock().c_str(), it->second.index);
      return;
    }
    BoundTo b(sim_);
    if (!repl::Init()) return;
    SendUiFull(conn, it->second, out, "before EnterGame");
  }

  void UiStep(gms::ISender& out) {
    if (!match::Enabled() || !replicate_) return;
    BoundTo b(sim_);
    if (!repl::Init()) return;
    uint16_t have = 0;
    for (auto& kv : players_) {
      Player& pl = kv.second;
      if (!pl.world || pl.index < 0) continue;
      if (pl.ui) {
        have = static_cast<uint16_t>(have | (1u << pl.index));
        continue;
      }
      if (!pl.inGame) continue;
      SendUiFull(kv.first, pl, out, uiBeforeEnterGame_ ? "after EnterGame (missed the done point)" : "after EnterGame");
    }
    if (!have) return;  // the deltas compare against the field state the first full one left
    gms::Bytes d;
    if (repl::UIDataUpdate(sim_, false, true, &d) > 0) SendTo(out, have, d, nullptr);
    gms::Bytes wd;
    if (repl::SpiritWellUpdate(sim_, false, true, &wd) > 0) SendTo(out, have, wd, nullptr);
    gms::Bytes cd;
    if (repl::CampUpdate(sim_, false, true, &cd) > 0) SendTo(out, have, cd, nullptr);
  }

  double NowS() const {
    return std::chrono::duration<double>(gms::SteadyClock::now().time_since_epoch()).count();
  }
  int64_t ServerMs() const {
    return std::chrono::duration_cast<std::chrono::milliseconds>(gms::SteadyClock::now() - sessionStart_).count();
  }

  // D8b: once, before the first player or tick: the level's surrender tuning ([host] test keys over it) and whether the
  // vote runs in this game (by the descriptors createGame gave: matchmade and custom games; MATCH_FLOW.md
  // *Surrender and the karma vote (D8b-core)* §5). Off, the intake still answers a request: CA 01 {0} "not yet
  // available" (nothing ever makes it available).
  void DecideSurrender() {
    if (surrenderDecided_) return;
    surrenderDecided_ = true;
    gms::surrender::Tuning t;
    bool read = false;
    {
      BoundTo b(sim_);
      read = surr::ReadTuning(sim_, &t);
    }
    std::string tests;
    const SurrenderKeys& k = surrenderKeys_;
    if (k.afterS >= 0) {
      t.enableMatchMinutes = k.afterS / 60.0;
      tests += " surrender_after_s=" + std::to_string(k.afterS);
    }
    if (k.voteS > 0) {
      t.voteS = k.voteS;
      tests += " surrender_vote_s=" + std::to_string(k.voteS);
    }
    if (k.cooldownS >= 0) {
      t.cooldownS = k.cooldownS;
      tests += " surrender_cooldown_s=" + std::to_string(k.cooldownS);
    }
    if (k.disconnectS >= 0) {
      t.enableDisconnectS = k.disconnectS;
      tests += " surrender_disconnect_s=" + std::to_string(k.disconnectS);
    }
    surrender_ = gms::surrender::SurrenderVote(t);
    const uint32_t desc = match::Descriptors();
    const gms::postgame::Mode mode = gms::postgame::ModeOf(desc);
    surrenderOn_ = k.mode > 0 || (k.mode < 0 && mode != gms::postgame::Mode::kPractice);
    gms::Logf("%s match: surrender (D8b) %s ([host] surrender %d, descriptors 0x%X: %s game); tuning %s: available at "
              "match %.0f s, economy %.0f, kills %d, disconnected %.0f s; vote %.0f s, cooldown %.0f s, threshold "
              "%.3f%s%s",
              gms::Clock().c_str(), surrenderOn_ ? "on" : "off", k.mode, desc, gms::postgame::ModeName(mode),
              read ? "from the level's LevelGameplayTuning" : "NOT readable (SM_Map_v5's values)",
              t.enableMatchMinutes * 60.0, t.enableEconomy, t.enableKills, t.enableDisconnectS, t.voteS, t.cooldownS,
              t.threshold, tests.empty() ? "" : "; test keys:", tests.c_str());
  }

  // D8b, every tick before the replication step (server simulator bound): the vote's Tick with the match time and the
  // team totals, its messages (to a client once it got EnterGame: a rejoin's status waits for its game), the 8B byte
  // mSurrenderAvailablity, and a passed vote's end (match::EndBySurrender). A Guardian's death stops the vote.
  void SurrenderStep(gms::ISender& out) {
    DecideSurrender();
    if (match::Ended() && !surrenderStopped_) {
      surrenderStopped_ = true;
      surrender_.Stop();
    }
    if (surrenderOn_ && match::Started() && !match::Ended()) {
      gms::surrender::MatchView mv;
      mv.matchS = match::MatchSeconds();
      surr::ReadTeams(sim_, &mv);
      surrender_.Tick(NowS(), ServerMs(), mv, &surrenderOut_);
    }
    if (!surrenderOut_.empty()) {
      std::vector<gms::surrender::Send> held;
      for (gms::surrender::Send& s : surrenderOut_) {
        auto it = players_.find(s.conn);
        if (it == players_.end()) continue;  // gone: dropped
        if (!it->second.inGame) {
          held.push_back(std::move(s));
          continue;
        }
        out.Send(s.conn, s.msg, s.what);
        ++surrenderSent_;
      }
      surrenderOut_.swap(held);
    }
    const uint8_t avail = surrender_.UiAvailability();
    if (avail != uiAvailability_ && surr::WriteUiAvailability(sim_, avail)) {
      gms::Logf("%s match: mSurrenderAvailablity %u -> %u in the UI data (8B member 3) at match %.1f s; %s",
                gms::Clock().c_str(), uiAvailability_, avail, match::MatchSeconds(), surrender_.Describe().c_str());
      uiAvailability_ = avail;
    }
    TeamLeftStep();
    gms::surrender::End e;
    if (surrender_.TakeEnd(&e)) {
      const bool ended = match::EndBySurrender(e.team, e.reason, e.yesPercent);
      gms::Logf("%s match: team %d surrendered (%d %% in favour): end reason %u -> %s", gms::Clock().c_str(), e.team,
                e.yesPercent, e.reason,
                ended ? "the match ends (match::EndBySurrender)" : "NOT ended (match not running?)");
    }
  }

  // G3b: a team whose every human has left (no connection for team_left_surrender_s, 60) while the other team has a
  // connected one surrenders: match::EndBySurrender with the team's reason (2 or 4), as a passed vote (the surrender
  // cinematic, VICTORY for the others, finishGame "surrender"). Only where surrender runs (matchmade and custom games).
  void TeamLeftStep() {
    if (!surrenderOn_ || !match::Started() || match::Ended() || teamLeft_.Fired() || teamLeft_.GraceS() < 0) return;
    gms::surrender::TeamHumans teams[3];
    auto count = [&](int index, bool connected) {
      unsigned status = 0, team = 0, idx = 0;
      if (index < 0 || !SehReadPlayer(sim_, static_cast<unsigned>(index), &status, &team, &idx) || team < 1 || team > 2)
        return;
      ++teams[team].seated;
      if (connected) ++teams[team].connected;
    };
    for (const auto& kv : players_)
      if (!kv.second.spectator) count(kv.second.index, true);  // B15b: a spectator is on no team
    for (const auto& kv : departed_) count(kv.second.index, false);
    const int t = teamLeft_.Tick(NowS(), teams[1], teams[2]);
    for (int team = 1; team <= 2; ++team) {
      const bool gone = teamLeft_.LeftSince(team) >= 0;
      if (gone != teamLeftLogged_[team])
        gms::Logf("%s match: team %d %s (%d human(s) seated, %d connected); %s", gms::Clock().c_str(), team,
                  gone ? "has no connected human any more" : "has a connected human again", teams[team].seated,
                  teams[team].connected,
                  gone ? ("it surrenders in " + std::to_string(static_cast<int>(teamLeft_.GraceS())) +
                          " s unless one comes back").c_str()
                       : "no team-left surrender");
      teamLeftLogged_[team] = gone;
    }
    if (!t) return;
    const bool ended = match::EndBySurrender(t, t == 2 ? 2u : 4u, -1);
    gms::Logf("%s match: every human of team %d has left for %.0f s: the team surrenders (end reason %u) -> %s",
              gms::Clock().c_str(), t, teamLeft_.GraceS(), t == 2 ? 2u : 4u,
              ended ? "the match ends (match::EndBySurrender)" : "NOT ended (match not running?)");
  }

  // D8-live: the simulator's outgoing vector emptied without sending (its kind-3 line-of-sight records released), the
  // command answers and the death processing's messages dropped; counted for the stats line.
  void FrozenStep() {
    BoundTo b(sim_);
    std::vector<repl::Outgoing> msgs;
    repl::DrainCounts dc;
    repl::Drain(sim_, &msgs, &dc);
    released_ += dc.released;
    frozenDropped_ += dc.entries;
    std::vector<cmds::Answer> answers;
    cmds::TakeAnswers(&answers);
    frozenDropped_ += static_cast<int>(answers.size());
    std::vector<death::Outgoing> deathMsgs;
    death::TakeOutgoing(&deathMsgs);
    frozenDropped_ += static_cast<int>(deathMsgs.size());
    const double t = NowS();
    if (frozenDropped_ != frozenLogged_ && t - frozenLogAt_ >= 10.0) {
      frozenLogAt_ = t;
      frozenLogged_ = frozenDropped_;
      gms::Logf("%s match: post-game: %d message(s) of the frozen world dropped so far (outgoing vector, answers, "
                "recaps; [host] postgame_replicate 0)",
                gms::Clock().c_str(), frozenDropped_);
    }
  }

  // D8-live (MATCH_FLOW.md *Match end and post-game (D8-static)* §1, §2): once the match module ended the match (in this
  // tick, so this Flush's delta pass already sent every player's final A9 to everyone, the final AA to the player's
  // team and the 8B with the frozen mGameTime), the final AA of every player goes to the other team's clients too
  // (they never got a player's fog data), then BF 00 through PostGame; from then on PostGame's messages: C1 01 on the
  // client's own C1 01 (2 s fallback), C1 04 from dgserver's playerRewards (or the host's own), by the game's mode.
  void EndStep(gms::ISender& out) {
    match::End e;
    const double nowS = NowS();
    if (match::TakeEnd(&e)) {
      end_ = e;
      endAtS_ = nowS;
      FinalFogData(out);
      int added = 0;
      int spectators = 0;
      for (auto& kv : players_) {
        Player& pl = kv.second;
        if (!pl.world || pl.index < 0) continue;
        if (pl.spectator) {  // B15b: BF 00 and C1 01 (the spectator's victory view); no vote, no rewards
          postgame_.AddSpectator(kv.first, pl.index);
          ++spectators;
          continue;
        }
        std::uint8_t team = 0;
        {
          BoundTo b(sim_);
          repl::PlayerTeam(sim_, static_cast<unsigned>(pl.index), &team);
        }
        const bool won = team != 0 && team == e.winner;
        const gms::postgame::Rewards own = OwnRewards(pl.index, won, e.durationMs, e.descriptors);
        postgame_.AddPlayer(kv.first, pl.index, team, own);
        postgameUsers_[kv.first] = pl.user;  // D8b: playerKarma and playerRewards by user, also after a disconnect
        // D8b: the PvP party from createGame's roster (a vote from the same party counts as a party vote)
        long long party = 0;
        if (const dggms::control::RosterEntry* re = hostctl::FindUser(static_cast<std::int64_t>(pl.user)))
          party = static_cast<long long>(re->party);
        if (party) postgame_.SetParty(kv.first, party);
        ++added;
        gms::Logf("%s match: post-game for player %d (team %u, %s%s): the host's own rewards %s", gms::Clock().c_str(),
                  pl.index, team, won ? "won" : "lost", party ? (", party " + std::to_string(party)).c_str() : "",
                  gms::postgame::Describe(own).c_str());
      }
      std::vector<gms::postgame::Send> sends;
      postgame_.Start(nowS, e.reason, e.guardian, e.descriptors, &sends);
      for (const auto& snd : sends) out.Send(snd.conn, snd.msg, snd.what);
      frozen_ = !replicateAfterEnd_;
      gms::Logf("%s match: game over: reason %u, Guardian %08X, winner team %d, duration %llu ms, descriptors 0x%X "
                "(%s); BF 00 to %d player(s)%s; %s",
                gms::Clock().c_str(), e.reason, e.guardian, e.winner, e.durationMs, e.descriptors,
                gms::postgame::ModeName(postgame_.GameMode()), added,
                spectators ? (" and " + std::to_string(spectators) + " spectator(s)").c_str() : "",
                frozen_ ? "the world is frozen for the clients from now on" : "replication goes on ([host] postgame_replicate 1)");
    }
    if (!postgame_.Started()) return;
    // dgserver's rewards (playerRewards, after finishGame), by user
    for (const auto& pr : hostctl::TakeRewards()) {
      gms::ConnId conn = 0;
      for (const auto& kv : postgameUsers_)
        if (static_cast<std::int64_t>(kv.second) == pr.user) conn = kv.first;
      gms::postgame::Rewards r;
      r.lootBag = static_cast<uint32_t>(pr.rewards.lootBag);
      r.destiny = static_cast<int32_t>(pr.rewards.destiny);
      r.baseRoll = static_cast<int32_t>(pr.rewards.baseRoll);
      r.winPoints = static_cast<int32_t>(pr.rewards.winPoints);
      r.winAltPoints = static_cast<int32_t>(pr.rewards.winAltPoints);
      r.firstWinPoints = static_cast<int32_t>(pr.rewards.firstWinPoints);
      r.ftue = pr.rewards.ftue;
      for (const auto& it : pr.rewards.items)
        if (r.items.size() < gms::postgame::kMaxItems)
          r.items.push_back(gms::postgame::RewardItem{static_cast<uint32_t>(it.catalogId), it.duplicate,
                                                      static_cast<uint32_t>(it.alternate)});
      // D8b (D8c's protocol): a matchmade game's rewards carry the karma dgserver counted into the bag score; C1 04
      // waits for these and sends them as they are
      const bool withKarma = pr.rewards.karma.has_value();
      if (withKarma) {
        r.karmaPoints = static_cast<int32_t>(pr.rewards.karma->points);
        r.karmaParty = static_cast<int32_t>(pr.rewards.karma->party);
        r.karmaTeam = static_cast<int32_t>(pr.rewards.karma->team);
        r.karmaEnemy = static_cast<int32_t>(pr.rewards.karma->enemy);
      }
      const bool known = conn != 0 && postgame_.Has(conn);
      if (known) postgame_.OnServerRewards(conn, r, withKarma);
      gms::Logf("%s match: dgserver's playerRewards for user %lld %.3f s after the end%s: %s%s", gms::Clock().c_str(),
                static_cast<long long>(pr.user), nowS - endAtS_, withKarma ? " (with its karma, D8c)" : "",
                gms::postgame::Describe(r).c_str(), known ? "" : " (no player in the post-game: ignored)");
    }
    std::vector<gms::postgame::Send> sends;
    postgame_.Tick(nowS, ServerMs(), &sends);
    for (const auto& snd : sends) out.Send(snd.conn, snd.msg, snd.what);
    // D8b: a matchmade game's karma vote has closed (C1 02's end + the grace): every player's votes once, as
    // playerKarma for dgserver, which decides the bag with them and answers the karma-aware playerRewards
    for (const gms::postgame::KarmaReport& k : postgame_.TakeKarmaReports()) {
      const auto u = postgameUsers_.find(k.conn);
      dggms::control::PlayerKarma m;
      m.user = u != postgameUsers_.end() ? static_cast<std::int64_t>(u->second) : 0;
      m.party = k.votes.party;
      m.team = k.votes.team;
      m.enemy = k.votes.enemy;
      const bool sent = m.user != 0 && hostctl::PlayerKarma(m);
      ++karmaReports_;
      gms::Logf("%s match: karma vote closed %.1f s after the end: player %d (user %lld) got %d vote(s): party %d, "
                "team %d, enemy %d -> playerKarma %s",
                gms::Clock().c_str(), nowS - endAtS_, k.index, static_cast<long long>(m.user), k.votes.Total(),
                k.votes.party, k.votes.team, k.votes.enemy,
                sent ? "sent" : "not sent (no control channel: C1 04 with the host's tally)");
    }
    if (postgame_.AllLeft() && !allLeftLogged_) {
      allLeftLogged_ = true;
      gms::Logf("%s match: post-game: every player has left the GMS (%.1f s after the end)", gms::Clock().c_str(),
                nowS - endAtS_);
    }
  }

  // The final LabsPlayerFowUpdate (AA members 2-4: gold, creeps, level; 19-62: the 11 inventory slots) of every player
  // to the clients of the other team (D2g-live's delta pass sent AA only to the player's team; since D2h FowDataStep also
  // sends it to the players that see the creature, but an enemy that hasn't seen it lately has stale values). One
  // team: nothing to send.
  void FinalFogData(gms::ISender& out) {
    std::vector<int> members = {2, 3, 4};
    for (int m = 19; m <= 62; ++m) members.push_back(m);
    const uint16_t world = WorldMask();
    int sent = 0;
    for (const unsigned index : ReplicatedPlayers()) {
      BoundTo b(sim_);
      const uint16_t others = static_cast<uint16_t>(world & ~TeamMask(index));
      if (!others) continue;
      gms::Bytes m;
      if (repl::PlayerFowUpdate(sim_, index, members, &m)) sent += SendTo(out, others, m, "final LabsPlayerFowUpdate");
    }
    gms::Logf("%s match: the final A9/AA deltas and the 8B (mGameTime %llu s) went out in this tick's passes; the final "
              "AA to the other team: %d message(s)",
              gms::Clock().c_str(), end_.durationMs / 1000, sent);
  }

  // The test wave (SetTestWave): spawned once the first player has been in the game test_wave s; its dead minions are
  // marked for delete test_wave_delete_ms after their HP reached 0.
  void TickWave() {
    if (waveAt_ <= 0) return;
    const auto now = gms::SteadyClock::now();
    if (!waveSpawned_) {
      for (const auto& kv : players_) {
        if (!kv.second.inGame || !kv.second.world || kv.second.spectator) continue;
        if (std::chrono::duration<double>(now - kv.second.enterGame).count() < waveAt_) continue;
        waveSpawned_ = true;
        SpawnWave();
        break;
      }
      return;
    }
    for (WaveMinion& w : wave_) {
      if (w.marked) continue;
      float hp = 0.f;
      if (!SehObjectHp(w.id, &hp)) {
        w.marked = true;
        Log("replication: test wave minion %08X is gone", w.id);
        continue;
      }
      if (hp > 0.f) continue;
      if (w.dead == gms::SteadyClock::time_point{}) {
        w.dead = now;
        if (waveDeleteMs_ > 0)
          Log("replication: test wave minion %08X dead (hp %.1f); marked for delete in %d ms", w.id, hp, waveDeleteMs_);
        else
          Log("replication: test wave minion %08X dead (hp %.1f); its behavior tree deletes it (nBehavior_Death)", w.id, hp);
      } else if (waveDeleteMs_ > 0 &&
                 std::chrono::duration<double, std::milli>(now - w.dead).count() >= waveDeleteMs_) {
        w.marked = true;
        Log("replication: test wave minion %08X marked for delete (+0x55, as nGameObject.MarkForDelete): %s", w.id,
            SehMarkForDelete(w.id) ? "ok" : "FAILED");
      }
    }
  }

  void SpawnWave() {
    char path[MAX_PATH];
    GetModuleFileNameA(nullptr, path, MAX_PATH);
    if (char* slash = std::strrchr(path, '\\')) *slash = 0;
    std::strncat(path, "\\dgsim_wave.lua", sizeof path - std::strlen(path) - 1);
    std::ifstream in(path, std::ios::binary);
    if (!in) {
      Log("replication: test wave: %s not found (setup_server_copy.py copies gms/engine/lua/*.lua)", path);
      return;
    }
    std::string chunk((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
    char head[400];
    std::snprintf(head, sizeof head,
                  "dgsim_wave_count = %d dgsim_wave_noun = '%s' dgsim_wave_from = '%s' dgsim_wave_to = '%s' "
                  "dgsim_wave_team = '%s' dgsim_wave_start = %g dgsim_wave_stop = %g\n",
                  waveCount_, waveNoun_.c_str(), waveFrom_.c_str(), waveTo_.c_str(), waveTeam_.c_str(), waveStart_,
                  waveStop_);
    chunk = head + chunk;
    static char report[4096];
    BoundTo b(sim_);
    const bool ok = SehRunLua(sim_, chunk.c_str(), static_cast<unsigned>(chunk.size()), "dgsim_wave.lua", report,
                              sizeof report);
    Log("replication: test wave (dgsim_wave.lua)%s:", ok ? "" : " FAILED");
    std::string text(report);
    size_t pos = 0;
    while (pos < text.size()) {
      size_t nl = text.find('\n', pos);
      if (nl == std::string::npos) nl = text.size();
      const std::string line = text.substr(pos, nl - pos);
      Log("  %s", line.c_str());
      if (line.rfind("wave ids:", 0) == 0) {
        for (size_t at = line.find("0x"); at != std::string::npos; at = line.find("0x", at + 2)) {
          WaveMinion w;
          w.id = static_cast<uint32_t>(std::strtoul(line.c_str() + at + 2, nullptr, 16));
          if (w.id) wave_.push_back(w);
        }
      }
      pos = nl + 1;
    }
    Log("replication: test wave: %zu minion(s) watched", wave_.size());
  }

  void TickTestLua() {
    if (testLua_.empty()) return;
    const auto now = gms::SteadyClock::now();
    double secs = -1;
    for (const auto& kv : players_)
      if (kv.second.inGame && kv.second.world && !kv.second.spectator) {
        secs = std::chrono::duration<double>(now - kv.second.enterGame).count();
        break;
      }
    if (secs < 0) return;
    for (TimedLua& t : testLua_) {
      if (t.done || secs < t.at) continue;
      t.done = true;
      std::string path = t.file;
      if (path.size() < 2 || path[1] != ':') {
        char dir[MAX_PATH];
        GetModuleFileNameA(nullptr, dir, MAX_PATH);
        if (char* slash = std::strrchr(dir, '\\')) *slash = 0;
        path = std::string(dir) + "\\" + path;
      }
      std::ifstream in(path, std::ios::binary);
      if (!in) {
        Log("host: test_lua %s not found", path.c_str());
        continue;
      }
      std::string chunk((std::istreambuf_iterator<char>(in)), std::istreambuf_iterator<char>());
      static char report[8192];
      BoundTo b(sim_);
      const bool ok = SehRunLua(sim_, chunk.c_str(), static_cast<unsigned>(chunk.size()), t.file.c_str(), report,
                                sizeof report);
      Log("host: test_lua %s at %.1f s after EnterGame%s:", t.file.c_str(), secs, ok ? "" : " FAILED");
      std::string text(report);
      size_t pos = 0;
      while (pos < text.size()) {
        size_t nl = text.find('\n', pos);
        if (nl == std::string::npos) nl = text.size();
        Log("  %s", text.substr(pos, nl - pos).c_str());
        pos = nl + 1;
      }
    }
  }

  static std::string Hex8(uint32_t v) {
    char b[12];
    std::snprintf(b, sizeof b, "%08X", v);
    return b;
  }

  static void WriteWorldFile(int index, const std::vector<std::string>& lines) {
    char path[MAX_PATH];
    std::snprintf(path, sizeof path, "%s\\world_p%d.txt", HostGet().dataDir, index);
    FILE* f = std::fopen(path, "w");
    if (!f) {
      gms::Logf("match: cannot write %s", path);
      return;
    }
    std::fprintf(f, "# The world the match host sent player %d at the session point 'loaded' (D2f), in gms_script's "
                    "--inject-file format\n", index);
    for (const auto& l : lines) std::fprintf(f, "%s\n", l.c_str());
    std::fclose(f);
    gms::Logf("match: world of player %d written to %s (%zu messages)", index, path, lines.size());
  }

  void* sim_;
  const gms::ScriptOptions o_;
  gms::ScriptedMatch inject_;  // an inject file's messages ([host] inject_file), for format experiments
  std::map<gms::ConnId, Player> players_;
  std::map<uint64_t, Player> departed_;  // G3: humans whose connection ended, by user id (their server player stays)
  int rejoins_ = 0, catchUps_ = 0;
  std::set<gms::ConnId> lobbyLogged_;  // B15: connections whose HelloReq the custom lobby holds (logged once)
  bool used_[kMaxPlayers] = {};
  // B15b: [host] spectators (1: serve createGame's spectator entries), spectator_team (1, the observer's mTeam)
  gms::SpectatorSeats spectatorSeats_;
  bool spectators_ = true;
  std::uint8_t spectatorTeam_ = gms::kSpectatorTeam;
  int spectatorInputLogged_ = 0;
  bool skipShaper_ = true;
  // D3-live step 3
  bool shaperSelect_ = false, shaperTimersGiven_ = false;
  int botPickMs_ = 1000, botStepMs_ = 600, botLockMs_ = 1500;
  int picksWritten_ = 0;
  std::map<std::uint8_t, gms::ShaperPick> pagePicks_;  // D3-perks: the humans' picks with a perk page, by index
  std::string timersFrom_;
  bool world_ = true;
  bool deploy_ = true;
  bool drain_ = true;
  bool playerUpdates_ = true;
  bool draining_ = false;  // seam 1 runs every tick once a player has the world
  int forwardedTotal_ = 0;
  int droppedLogged_ = 0;
  bool commands_ = true;  // D7-live: the client's commands go to the intake
  std::vector<TimedCommand> timed_;

  // D2g-live
  static constexpr uint16_t kAllPlayers = 0x7FFF;  // kind 3 (positional) without fog of war: everyone
  bool replicate_ = true;
  bool allTypes_ = true;
  bool networkRate_ = true;
  int logFirst_ = 3;
  bool uiBeforeEnterGame_ = true;  // X2a
  bool answersFirst_ = true;       // D14a-live
  gms::ModifierRegistry mods_;
  gms::WireCounts counts_;
  gms::SteadyClock::time_point countsSince_{};
  std::map<uint8_t, int> firstLogged_;
  std::set<uint32_t> failedCreates_;
  int creates_ = 0, deletes_ = 0, updates_ = 0, modsRegistered_ = 0, modEnds_ = 0, unknownUpdates_ = 0;
  int released_ = 0, forwardedViaDeletion_ = 0, knownObjects_ = 0;
  unsigned netRateTicks_ = 0, netRate200_ = 0;
  struct WaveMinion {
    uint32_t id = 0;
    gms::SteadyClock::time_point dead{};
    bool marked = false;
  };
  double waveAt_ = 0;
  int waveCount_ = 3, waveDeleteMs_ = 2000;
  std::string waveNoun_, waveFrom_, waveTo_, waveTeam_ = "Mortal";
  double waveStart_ = 12, waveStop_ = 8;
  bool waveSpawned_ = false;
  std::vector<WaveMinion> wave_;
  struct TimedLua {
    double at = 0;
    std::string file;
    bool done = false;
  };
  std::vector<TimedLua> testLua_;
  // D2g-live step 2: fog of war
  bool fog_ = false, fogReady_ = false, fogUnavailableLogged_ = false;
  fow::Options fowOpt_;
  int fogLogLimit_ = 200, fogLogged_ = 0, fogFailLogged_ = 0;
  gms::vision::VisionModel model_;
  gms::vision::VisionModel modelFree_;      // without the terrain, grass and stealth tests (evidence only)
  gms::vision::VisionModel modelTerrain_;   // D2g-rest: the terrain test only (the records' +0x51 mask)
  gms::SteadyClock::time_point visionLast_{};
  std::map<uint32_t, uint8_t> revealedLast_;  // D2g-rest: the grass reveals last tick (logged when they change)
  int fogTerrainMaskWrites_ = 0, fogRevealTicks_ = 0, fogGrassLogged_ = 0;
  std::map<uint32_t, uint8_t> terrainHidden_;
  int fogTerrainHidden_ = 0;
  gms::vision::FogPlanner planner_;
  fow::ReadCounts fogLast_;
  std::vector<gms::vision::FogPlayer> fogPlayersTick_;
  int fogEnters_ = 0, fogExits_ = 0, fogExitBytes_ = 0, fogA3_ = 0, fogVisWrites_ = 0, fogRadiusWrites_ = 0;
  int fogGrassWrites_ = 0, fogAdvanced_ = 0, fogKind3_ = 0, fogKind3Hidden_ = 0, fogKind3Logged_ = 0;
  // D2h: the players' FoW data (AA) by vision (FowDataStep)
  bool fowDataByVision_ = true;
  gms::FowDataRouter fowRouter_;
  int fowDataLogLimit_ = 200, fowDataLogged_ = 0, fowDataDeltas_ = 0, fowDataDeltasEnemy_ = 0, fowDataFull_ = 0;
  int fowDataFullEnemy_ = 0, fowDataDrops_ = 0, fowDataReported_ = -1;
  // D2g-pursuit: the last-seen pursuit positions and the follows (PursuitWatch)
  int fogLastSeenWrites_ = 0, fogLastSeenFresh_ = 0;
  struct PursuitState {
    uint32_t target = 0;
    bool seen = false;
    float goal[2] = {}, pos[2] = {};
    char name[48] = {};
  };
  std::map<uint32_t, PursuitState> pursuits_;  // follower id -> its follow last tick
  int pursuitTicks_ = 0, pursuitHiddenTicks_ = 0, pursuitOriginTicks_ = 0, pursuitLogged_ = 0, pursuitOriginLogged_ = 0;
  // D2g-live step 3: what the players were sent of each object's animation overrides
  struct AnimShadow {
    uint32_t overrides[7] = {};
    bool seeded = false;
  };
  std::map<uint32_t, AnimShadow> animShadow_;
  int animDeltas_ = 0;
  // D1c-live
  bool uiFieldStateUsed_ = false;
  match::End end_;
  // D8-live
  gms::postgame::PostGame postgame_;
  gms::postgame::Options postgameOptions_;
  bool ownInFinish_ = false;
  bool replicateAfterEnd_ = false;
  bool frozen_ = false;
  unsigned seed_ = 0;
  gms::SteadyClock::time_point sessionStart_ = gms::SteadyClock::now();
  double endAtS_ = 0;
  int frozenDropped_ = 0, frozenLogged_ = 0;
  double frozenLogAt_ = 0;
  bool allLeftLogged_ = false;
  // D8b: surrender (gms/core/surrender) and the karma vote's reports
  gms::surrender::SurrenderVote surrender_;
  std::vector<gms::surrender::Send> surrenderOut_;  // the vote's messages, sent in SurrenderStep (held until EnterGame)
  SurrenderKeys surrenderKeys_;
  gms::surrender::TeamLeftWatch teamLeft_;  // G3b
  bool teamLeftLogged_[3] = {};
  bool surrenderDecided_ = false, surrenderOn_ = false, surrenderStopped_ = false;
  uint8_t uiAvailability_ = 0;
  int surrenderIntake_ = 0, surrenderSent_ = 0, karmaReports_ = 0;
  std::map<gms::ConnId, uint64_t> postgameUsers_;  // the post-game's connections -> Blaze users
};

// Q3c: the session's way out: every message is shown to the match (perf's traffic and answers), then sent.
class TimedTransport final : public gms::ITransport {
 public:
  gms::Endpoint* ep = nullptr;
  ServerMatch* match = nullptr;
  bool Send(gms::ConnId conn, const gms::Bytes& msg) override {
    if (match) match->OnSent(conn, msg);
    return ep->Send(conn, msg);
  }
};

struct GmsHost {
  gms::Endpoint ep;
  TimedTransport tx;  // Q3c: the session sends through it
  std::unique_ptr<ServerMatch> match;
  std::unique_ptr<gms::GmsSession> session;
  gms::SteadyClock::time_point start;
  std::string ip;
  unsigned port = 0;
  unsigned polls = 0;
  unsigned packets = 0;
  gms::PumpTiming pump;    // Q3c: the polls' measurements since perf's last window
  double lastSampleMs = 0; // Q3c: Endpoint::SampleConnections every 250 ms
  bool fastLane = false;
};
// Never destroyed: the process ends with the match (ADR-2 decision 3), and RakNet must not be shut down under the
// loader lock at DLL_PROCESS_DETACH.
GmsHost* g_gms;

void GmsLogLine(const std::string& line) { Log("%s", line.c_str()); }

// D8-live, [host] rewards=own: the host's own rewards in every playerFinish (death::LogPlayerFinish at the end); the same
// ones the post-game falls back on.
bool FinishRewards(int index, bool won, unsigned long long durationMs, unsigned descriptors,
                   dggms::control::Rewards* out) {
  if (!g_gms || !g_gms->match) return false;
  const gms::postgame::Rewards r = g_gms->match->OwnRewards(index, won, durationMs, descriptors);
  *out = dggms::control::Rewards();
  out->lootBag = r.lootBag;
  out->destiny = r.destiny;
  out->ftue = r.ftue;
  out->baseRoll = r.baseRoll;
  out->winPoints = r.winPoints;
  out->winAltPoints = r.winAltPoints;
  out->firstWinPoints = r.firstWinPoints;
  for (const auto& it : r.items) out->items.push_back(dggms::control::RewardItem{it.catalogId, it.duplicate, it.alternate});
  return true;
}

// Q3c (perf.h): the GMS endpoint's side of a window, the `net:` line: ClockSync answers (their arrival in RakNet ->
// the answer handed over, then Send -> the wire), the GMS messages' wait for the engine's poll, the tracked answers'
// Send -> wire, every other message's time in RakNet's send queue, resends, RakNet's ping and buffers, the simulator.
void NetGauges(gms::timing::Gauges* g, std::string* line, int minute) {
  using gms::timing::FormatSummary;
  using gms::timing::Summarize;
  GmsHost* h = g_gms;
  if (!h) return;
  const gms::Endpoint::Stats st = h->ep.TakeStats();
  gms::PumpTiming pt;
  std::swap(pt, h->pump);
  std::vector<double> clockWire, answerWire;
  for (size_t i = 0; i < st.trackedWireMs.size() && i < st.trackedWire.size(); ++i)
    (st.trackedWire[i] == gms::kGmsClockSync ? clockWire : answerWire).push_back(st.trackedWireMs[i]);
  const gms::timing::Summary clock = Summarize(pt.clockAnswerMs), age = Summarize(pt.ageMs),
                             cw = Summarize(clockWire), aw = Summarize(answerWire), queue = Summarize(st.queueMs);
  const gms::NetSimOptions& sim = h->ep.NetSim();
  char simText[300] = "off";
  if (sim.Active())
    std::snprintf(simText, sizeof simText,
                  "delay %d ms, jitter %d ms, loss %.1f %% each way: out %llu datagrams (%llu lost), added %.1f ms "
                  "mean / %.1f max; in %llu (%llu lost), added %.1f / %.1f (totals)",
                  sim.delayMs, sim.jitterMs, sim.lossPercent, static_cast<unsigned long long>(st.simOut),
                  static_cast<unsigned long long>(st.simOutDropped), st.simOutAddedMeanMs, st.simOutAddedMaxMs,
                  static_cast<unsigned long long>(st.simIn), static_cast<unsigned long long>(st.simInDropped),
                  st.simInAddedMeanMs, st.simInAddedMaxMs);
  char b[1800];
  std::snprintf(b, sizeof b,
                "net: minute %d (ms): ClockSync answers %d (arrival -> answer %s; Send -> wire %s); GMS messages "
                "waiting for the poll %s (%d without an arrival time); answers B4/C3/C2/9B Send -> wire %s; other "
                "messages in RakNet's queue %s; %llu sent (%llu B), %llu resends, %llu on the fast lane%s; RakNet: ping "
                "%d (max %d), send buffer max %u messages / %.0f B, resend buffer max %u, loss max %.1f %%%s; simulator "
                "%s",
                minute, clock.n, FormatSummary(clock).c_str(), FormatSummary(cw).c_str(), FormatSummary(age).c_str(),
                pt.unknownArrival, FormatSummary(aw).c_str(), FormatSummary(queue).c_str(),
                static_cast<unsigned long long>(st.gmsSent), static_cast<unsigned long long>(st.gmsBytes),
                static_cast<unsigned long long>(st.resends), static_cast<unsigned long long>(st.fastLane),
                h->fastLane ? " (C7, B4)" : "", st.pingLast, st.pingMax, st.sendBufferMsgsMax, st.sendBufferBytesMax,
                st.resendBufferMsgsMax, st.packetLossMax * 100.0, st.limitedByCongestion ? ", congestion-limited" : "",
                simText);
  *line = b;
  auto add = [g](const char* name, double v) { g->push_back({name, v}); };
  add("clock_answer_p99_ms", clock.n ? clock.p99 : -1);
  add("clock_answer_max_ms", clock.n ? clock.max : -1);
  add("clock_wire_p99_ms", cw.n ? cw.p99 : -1);
  add("poll_wait_p99_ms", age.n ? age.p99 : -1);
  add("poll_wait_max_ms", age.n ? age.max : -1);
  add("answer_wire_p99_ms", aw.n ? aw.p99 : -1);
  add("answer_wire_max_ms", aw.n ? aw.max : -1);
  add("raknet_queue_p99_ms", queue.n ? queue.p99 : -1);
  add("raknet_queue_max_ms", queue.n ? queue.max : -1);
  add("gms_sent", static_cast<double>(st.gmsSent));
  add("gms_bytes", static_cast<double>(st.gmsBytes));
  add("resends", static_cast<double>(st.resends));
  add("ping_max_ms", st.pingMax);
  add("send_buffer_max", st.sendBufferMsgsMax);
  add("resend_buffer_max", st.resendBufferMsgsMax);
  add("loss_max_pct", st.packetLossMax * 100.0);
}

void ReplicationGauges(gms::timing::Gauges* g, std::string*, int) {
  if (g_gms && g_gms->match) g_gms->match->PerfGauges(g);
}

}  // namespace

bool GmsHostStart(void* sim, void* levelAsset, const char* levelName, unsigned seed, unsigned levelFlags) {
  const HostConfig& host = HostGet();
  if (!host.enabled || g_gms) return false;
  std::string bind = host.bind[0] ? host.bind : "127.0.0.1:10042";
  if (_stricmp(bind.c_str(), "off") == 0) {
    Log("host: GMS endpoint off (DGGMS_BIND=off; another process serves the GMS port)");
    return false;
  }
  const size_t colon = bind.rfind(':');
  const std::string ip = colon == std::string::npos ? bind : bind.substr(0, colon);
  const unsigned long port = colon == std::string::npos ? 10042ul : std::strtoul(bind.c_str() + colon + 1, nullptr, 10);
  const std::uint32_t ipv4 = ParseIpv4(ip);
  if (!ipv4 || port == 0 || port > 65535) {
    Log("host: DGGMS_BIND '%s' is not <IPv4>:<port>; no GMS endpoint", bind.c_str());
    return false;
  }
  gms::SetLogSink(GmsLogLine);

  // GamePrepareForStart: the level the server loaded, by its asset id, and the server's seed and flags.
  gms::ScriptOptions mo;
  mo.setup.prepareFlags = static_cast<uint32_t>(HostOptionInt("prepare_flags", 2));
  mo.setup.level = AssetId(levelName);
  mo.setup.seed = seed;
  mo.setup.levelFlags = levelFlags;
  if (hostctl::Active() && hostctl::HasGame())  // G3b: GameAborted names the Blaze game
    mo.setup.blazeGameId = static_cast<std::uint64_t>(hostctl::Game().gameId);
  void* byId = SehFindAssetById(mo.setup.level);
  Log("host: GMS level id 0x%08X (FNV-1 of '%s') resolves to %p, the loaded level %p: %s", mo.setup.level, levelName,
      byId, levelAsset, byId == levelAsset ? "yes" : "NO");
  mo.slot.team = static_cast<uint8_t>(HostOptionInt("team", gms::kTeamBlue));
  char noun[32];
  HostOptionString("noun", "0xEF044AAB", noun, sizeof noun);
  mo.slot.noun = static_cast<uint32_t>(std::strtoul(noun, nullptr, 0));
  char injectFile[MAX_PATH];
  HostOptionString("inject_file", "", injectFile, sizeof injectFile);
  if (injectFile[0]) {
    std::string error;
    const int n = gms::ReadInjectFile(injectFile, &mo.inject, &error);
    if (n < 0) Log("host: inject file: %s; nothing injected", error.c_str());
    else Log("host: inject file %s: %d message(s)", injectFile, n);
  }

  // D3-live step 3: shaper select ([host] shaper_select=1) with gms_script's option syntax for the humans' content and
  // the timers (until createGame carries the content, step 4); the bots are the scripted players.
  // [host] shaper_select: 1 on, 0 off, unset (-1) = createGame's shaperSelect (dgserver --shaper-select; step 4)
  const int shaperOption = HostOptionInt("shaper_select", -1);
  const bool shaperSelect =
      shaperOption > 0 || (shaperOption < 0 && hostctl::Active() && hostctl::HasGame() && hostctl::Game().shaperSelect);
  bool shaperTimersGiven = false;
  if (shaperSelect) {
    std::string error;
    char text[4096];
    HostOptionString("shaper_list", "", text, sizeof text);
    if (text[0] && !gms::ParseShaperList(text, &mo.shapers.list, &error))
      Log("host: [host] shaper_list: %s; the default list (alpha.Noun owned)", error.c_str());
    HostOptionString("skins", "", text, sizeof text);
    if (text[0] && !gms::ParseSkins(text, &mo.shapers.skins, &error))
      Log("host: [host] skins: %s; none", error.c_str());
    HostOptionString("shaper_timers", "", text, sizeof text);
    if (text[0]) {
      shaperTimersGiven = gms::ParseShaperTimers(text, &mo.shaperTimers, &error);
      if (!shaperTimersGiven) Log("host: [host] shaper_timers: %s; createGame's or the mode's", error.c_str());
    }
  }

  gms::SessionOptions so;
  so.skipShaper = HostOptionInt("skip_shaper", 1) != 0;
  so.shaperSelect = shaperSelect;
  so.echoStatus = HostOptionInt("echo_status", 1) != 0;
  so.playerUpdate = true;
  so.stepMs = HostOptionInt("step_ms", so.stepMs);
  so.loadTimeoutMs = HostOptionInt("load_timeout_ms", so.loadTimeoutMs);
  so.helloIpv4 = ipv4;
  so.helloPort = static_cast<uint16_t>(port);
  // G3: several humans (GMS_PROTOCOL.md *Several humans live (G3)*)
  so.nameHumans = HostOptionInt("name_humans", 1) != 0;
  so.waitForPlayers = HostOptionInt("wait_players", 1) != 0;
  so.waitPlayersMs = HostOptionInt("wait_players_ms", so.waitPlayersMs);
  // G3b: the match-found prompt's decline and timeout (with prepare_flags=1)
  so.abortOnDecline = HostOptionInt("abort_on_decline", 1) != 0;
  so.matchFoundTimeoutMs = HostOptionInt("match_found_timeout_ms", so.matchFoundTimeoutMs);
  so.abortAckMs = HostOptionInt("abort_ack_ms", so.abortAckMs);
  // B15b: the players' clients get the spectators too (GMS_PROTOCOL.md *Spectators on the match host (B15b)*)
  so.nameSpectators = HostOptionInt("name_spectators", 1) != 0;

  auto* g = new GmsHost;
  // Q3c: the network simulator (delay, jitter, loss on every datagram in and out; core/net/netsim.h), the answers timed
  // from Send to the wire, and the fast lane for ClockSync answers and ActionCommandResponse ([host] net_fast_answers)
  {
    gms::NetSimOptions net;
    net.delayMs = HostOptionInt("net_delay_ms", 0);
    net.jitterMs = HostOptionInt("net_jitter_ms", 0);
    char loss[32];
    HostOptionString("net_loss_pct", "0", loss, sizeof loss);
    net.lossPercent = std::atof(loss);
    net.seed = static_cast<std::uint32_t>(HostOptionInt("net_seed", 1));
    g->ep.SetNetSim(net);
    g->ep.SetTrackedSends({gms::kGmsClockSync, gms::kGmsActionCommandResponse, gms::kGmsItemTransaction,
                           gms::kGmsMiniMapPing, gms::kGmsLocomotionCommand});
    g->fastLane = HostOptionInt("net_fast_answers", 0) != 0;
    if (g->fastLane) g->ep.SetFastLane({gms::kGmsClockSync, gms::kGmsActionCommandResponse});
    Log("host: network (Q3c): simulator %s (net_delay_ms %d, net_jitter_ms %d, net_loss_pct %.1f, net_seed %u); fast "
        "lane %s",
        net.Active() ? "ON" : "off", net.delayMs, net.jitterMs, net.lossPercent, net.seed,
        g->fastLane ? "on (net_fast_answers 1: C7 and B4 IMMEDIATE_PRIORITY on channel 1)" : "off");
  }
  std::string error;
  // B15b: ten humans and five spectators, plus a rejoin's new connection before the old one's loss is noticed
  const unsigned maxConn = static_cast<unsigned>(HostOptionInt("max_connections", 16));
  if (!g->ep.Start(ip, static_cast<uint16_t>(port), maxConn, &error)) {
    Log("host: GMS endpoint FAILED: %s", error.c_str());
    delete g;
    return false;
  }
  if (!error.empty()) Log("host: GMS endpoint: %s", error.c_str());  // the simulator could not be installed
  perf::AddSource(NetGauges);
  perf::AddSource(ReplicationGauges);
  g->ip = ip;
  g->port = g->ep.BoundPort();
  g->start = gms::SteadyClock::now();
  g->match.reset(new ServerMatch(sim, mo));
  // with shaper select the server player starts without 0x10: seam 5 writes the session's first update from it
  g->match->SetSkipShaper(so.skipShaper && !so.shaperSelect);
  {
    // B15b: a custom game's spectators ([host] spectators 1; 0 = refused, as before B15b), their player's mTeam
    const bool spectators = HostOptionInt("spectators", 1) != 0;
    const int spectatorTeam = HostOptionInt("spectator_team", gms::kSpectatorTeam);
    g->match->SetSpectators(spectators, spectatorTeam);
    Log("host: spectators (B15b): %s, spectator_team %d, name_spectators %d", spectators ? "served" : "refused",
        spectatorTeam, so.nameSpectators ? 1 : 0);
  }
  g->match->SetShaperSelect(so.shaperSelect, shaperTimersGiven, HostOptionInt("shaper_bot_pick_ms", 1000),
                            HostOptionInt("shaper_bot_step_ms", 600), HostOptionInt("shaper_bot_lock_ms", 1500));
  if (so.shaperSelect) {
    std::string list;
    for (const gms::ShaperListEntry& e : mo.shapers.list) {
      char buf[40];
      std::snprintf(buf, sizeof buf, " 0x%08x:0x%x", e.noun, e.flags);
      list += buf;
    }
    Log("host: shaper select (D3-live) on (%s): list%s; skins for %zu shaper(s); timers %s; bots pick at %d ms + %d ms "
        "each, lock %d ms later",
        shaperOption > 0 ? "[host] shaper_select=1" : "createGame's shaperSelect",
        list.empty() ? " alpha.Noun owned (default; createGame's roster may give a human its own)" : list.c_str(),
        mo.shapers.skins.size(),
        shaperTimersGiven ? "[host] shaper_timers" : "createGame's or the mode's", HostOptionInt("shaper_bot_pick_ms", 1000),
        HostOptionInt("shaper_bot_step_ms", 600), HostOptionInt("shaper_bot_lock_ms", 1500));
  }
  {
    // D3-perks: the perk pages' server-side effect (engine/perks.h) and the bots' default loadout
    perks::Options po;
    po.enabled = HostOptionInt("perks", 1) != 0;
    po.percent = HostOptionInt("perk_percent", 1) != 0;
    po.modifiers = HostOptionInt("perk_modifiers", 1) != 0;
    po.botLoadout = HostOptionInt("bot_loadout", 1) != 0;
    perks::Configure(po);
  }
  const bool world = HostOptionInt("world", 1) != 0, deploy = HostOptionInt("deploy", 1) != 0,
             drain = HostOptionInt("drain", 1) != 0, playerUpdates = HostOptionInt("player_updates", 1) != 0;
  g->match->SetReplication(world, deploy, drain, playerUpdates);
  Log("host: replication (D2f): world %d, deploy %d, drain %d, player updates %d", world, deploy, drain, playerUpdates);
  // D2g-live: live replication (creates, updates, deletes, modifiers) and its test wave
  const bool replicate = HostOptionInt("replicate", 1) != 0, allTypes = HostOptionInt("create_types", 1) != 0,
             networkRate = HostOptionInt("network_rate", 1) != 0;
  const int logFirst = HostOptionInt("log_first", 3);
  g->match->SetLiveReplication(replicate, allTypes, networkRate, logFirst);
  const bool uiBefore = HostOptionInt("ui_before_entergame", 1) != 0;
  g->match->SetUiBeforeEnterGame(uiBefore);
  Log("host: the full UI data (8B/8C/8D) %s (X2a: the client's tower graphics read mNumTowersPerLane at EnterGame)",
      uiBefore ? "before EnterGame, at the session's done point ([host] ui_before_entergame 1)"
               : "after EnterGame ([host] ui_before_entergame 0: D1c-live's order, the tower slots collide)");
  const bool answersFirst = HostOptionInt("answers_first", 1) != 0;
  g->match->SetAnswersFirst(answersFirst);
  Log("host: command answers %s",
      answersFirst ? "before the tick's replicated messages, as the original's send stub ([host] answers_first 1)"
                   : "after the replication step but the item transactions' ([host] answers_first 0: D7-live's order)");
  char waveNoun[64], waveFrom[64], waveTo[64];
  HostOptionString("test_wave_noun", "Minion_Mortal_Melee.Noun", waveNoun, sizeof waveNoun);
  HostOptionString("test_wave_from", "Binding_Mortal_Outer", waveFrom, sizeof waveFrom);
  HostOptionString("test_wave_to", "Binding_Spirit_Outer", waveTo, sizeof waveTo);
  char waveTeam[16];
  HostOptionString("test_wave_team", "Mortal", waveTeam, sizeof waveTeam);
  const int waveAt = HostOptionInt("test_wave", 0), waveCount = HostOptionInt("test_wave_count", 3),
            waveDeleteMs = HostOptionInt("test_wave_delete_ms", agents::Installed() ? 0 : 2000),
            waveStart = HostOptionInt("test_wave_start", 12), waveStop = HostOptionInt("test_wave_stop", 8);
  g->match->SetTestWave(waveAt, waveCount, waveNoun, waveFrom, waveTo, waveDeleteMs, waveTeam, waveStart, waveStop);
  // D2g-live step 2: fog of war (fow.h); [host] agent_all_visible defaults to !fog (experiment.cpp)
  const bool fog = HostOptionInt("fog", 1) != 0;  // on by default since D2g-live (step 2)
  fow::Options fo;
  // D2g-rest: terrain, grass and stealth on by default (the client's renderer, wards and stealth evidence:
  // docs/GMS_PROTOCOL.md *Terrain, grass and stealth against the original (D2g-rest)*)
  fo.terrain = HostOptionInt("fog_terrain", 1) != 0;
  fo.grass = HostOptionInt("fog_grass", 1) != 0;
  fo.stealth = HostOptionInt("fog_stealth", 1) != 0;
  fo.render = HostOptionInt("fog_render", fog ? 1 : 0) != 0;
  fo.pursuit = HostOptionInt("fog_pursuit", 1) != 0;  // D2g-pursuit: the last-seen pursuit positions (0: the old bug)
  g->match->SetFog(fog && replicate, fo, HostOptionInt("fog_log_limit", 200));
  Log("host: fog of war (D2g-live step 2): fog %d%s, terrain %d, grass %d, stealth %d, render %d", fog,
      fog && !replicate ? " (needs replicate: off)" : "", fo.terrain, fo.grass, fo.stealth, fo.render);
  // D2h: a player's FoW data (AA) also to the players that see its creature, the whole struct to each new recipient
  const bool fowDataByVision = HostOptionInt("fow_data_by_vision", 1) != 0;
  g->match->SetFowDataRouting(fowDataByVision, HostOptionInt("fow_data_log_limit", 200));
  Log("host: the players' FoW data (AA) %s",
      fowDataByVision ? "to the own team, the spectators and every player that sees the player's creature, the whole "
                        "struct to a new recipient (D2h, [host] fow_data_by_vision 1)"
                      : "to the own team and the spectators only ([host] fow_data_by_vision 0: D2g-live's rule)");
  char testLua[4096];
  HostOptionString("test_lua", "", testLua, sizeof testLua);
  if (testLua[0]) g->match->SetTestLua(testLua);
  Log("host: live replication (D2g-live): replicate %d, create_types %s, network_rate %d, log_first %d; test wave %s",
      replicate, allTypes ? "all but spawn points" : "D2f's", networkRate, logFirst,
      waveAt > 0 ? (std::to_string(waveCount) + " x " + waveNoun + " (" + waveTeam + ") " + std::to_string(waveAt) +
                    " s after EnterGame, " + waveFrom + " +" + std::to_string(waveStart) + " -> " + waveTo + " -" +
                    std::to_string(waveStop))
                       .c_str()
                 : "off");
  // D7-live: the command intake (commands.h)
  const bool commands = HostOptionInt("commands", 1) != 0;
  if (commands) {
    cmds::Options co;
    co.behaviorTree = HostOptionInt("shaper_bt", 1) != 0;
    // D2g-live's delta pass sends the creatures' positions and the players' FoW data; D7-live's pieces only without it
    co.locomotionUpdates = HostOptionInt("locomotion_updates", replicate ? 0 : 1) != 0;
    co.fowUpdates = !replicate;
    co.logLimit = HostOptionInt("command_log_limit", co.logLimit);
    co.grantAbilityPoints = HostOptionInt("grant_ability_points", 0);
    co.grantXp = HostOptionInt("grant_xp", 0);  // D10
    co.itemTransactions = HostOptionInt("item_transactions", 1) != 0;  // D7b
    co.itemEffectCheck = HostOptionInt("item_effect_check", 1) != 0;   // D15
    co.emotes = HostOptionInt("emotes", 1) != 0;                        // D7b
    co.rankLog = HostOptionInt("rank_log", 1) != 0;                     // D11
    co.moveTakeover = HostOptionInt("move_takeover", 1) != 0;           // D7c
    co.queueTrace = HostOptionInt("queue_trace", 1) != 0;               // D7c
    co.queueLogLimit = HostOptionInt("queue_log_limit", co.queueLogLimit);
    cmds::Install(co);
    char commandsFile[MAX_PATH];
    HostOptionString("commands_file", "", commandsFile, sizeof commandsFile);
    if (commandsFile[0]) g->match->LoadCommandsFile(commandsFile);
  } else {
    Log("host: command intake off ([host] commands = 0)");
  }
  g->match->SetCommands(commands && cmds::Installed());
  // D11b: the shapers' banter, kill and assist lines, PlayerLeftGame/PlayerReconnectedGame (banter.h)
  {
    banter::Options bo;
    bo.enabled = HostOptionInt("banter", 1) != 0;
    bo.kill = HostOptionInt("banter_kill", 1) != 0;
    bo.assist = HostOptionInt("banter_assist", 1) != 0;
    bo.friendly = HostOptionInt("banter_friendly", 1) != 0;
    bo.enemy = HostOptionInt("banter_enemy", 1) != 0;
    bo.window = HostOptionInt("banter_window", 1) != 0;
    bo.multiKillReplacesKill = HostOptionInt("banter_multikill_skip", 1) != 0;
    bo.presence = HostOptionInt("banter_presence", 1) != 0;
    bo.emoteSequence = HostOptionInt("banter_emote_sequence", 1) != 0;
    bo.seed = static_cast<unsigned>(HostOptionInt("banter_seed", 1));
    bo.logLimit = HostOptionInt("banter_log_limit", bo.logLimit);
    banter::Install(bo);
  }
  // D8-live: the match end and the post-game (gms/core/postgame; MATCH_FLOW.md *D8-live*)
  {
    gms::postgame::Options po;
    po.ackFallbackMs = HostOptionInt("postgame_ack_fallback_ms", po.ackFallbackMs);
    po.readyWaitMs = HostOptionInt("postgame_ready_wait_s", po.readyWaitMs / 1000) * 1000;
    // without the control channel no playerRewards can come: the host's own at once
    po.rewardsWaitMs = HostOptionInt("postgame_rewards_wait_ms", hostctl::Active() ? po.rewardsWaitMs : 0);
    po.voteMs = HostOptionInt("postgame_vote_s", po.voteMs / 1000) * 1000;
    char rewards[16];
    HostOptionString("rewards", "auto", rewards, sizeof rewards);
    po.sendRewards = _stricmp(rewards, "none") != 0;
    const bool own = _stricmp(rewards, "own") == 0;
    const bool replicateAfter = HostOptionInt("postgame_replicate", 0) != 0;
    g->match->SetPostGame(po, own, replicateAfter, seed);
    g->match->SetSessionStart(g->start);
    if (own) death::SetFinishRewards(&FinishRewards);
    Log("host: post-game (D8-live): rewards %s (%s), C1 01 fallback %d ms, ready wait %d s, rewards wait %d ms, vote %d "
        "s; the world after the end %s",
        rewards, !po.sendRewards ? "no C1 04" : own ? "the host's own in playerFinish, C1 04 from playerRewards"
                                                    : "C1 04 from dgserver's playerRewards, else the host's own",
        po.ackFallbackMs, po.readyWaitMs / 1000, po.rewardsWaitMs, po.voteMs / 1000,
        replicateAfter ? "replicated ([host] postgame_replicate 1)" : "frozen for the clients");
  }
  // D8b: surrender ([host] surrender -1 auto: matchmade and custom games; test keys over the level's tuning)
  {
    ServerMatch::SurrenderKeys k;
    k.mode = HostOptionInt("surrender", -1);
    k.afterS = HostOptionInt("surrender_after_s", -1);
    k.voteS = HostOptionInt("surrender_vote_s", -1);
    k.cooldownS = HostOptionInt("surrender_cooldown_s", -1);
    k.disconnectS = HostOptionInt("surrender_disconnect_s", -1);
    k.teamLeftS = HostOptionInt("team_left_surrender_s", 60);
    g->match->SetSurrender(k);
    Log("host: surrender (D8b): [host] surrender %d (%s), surrender_after_s %d, surrender_vote_s %d, "
        "surrender_cooldown_s %d, surrender_disconnect_s %d (-1 = the level's tuning); G3b: team_left_surrender_s %d "
        "(a team whose humans have all left surrenders; -1 off)",
        k.mode, k.mode < 0 ? "auto: matchmade and custom games" : k.mode ? "on" : "off", k.afterS, k.voteS,
        k.cooldownS, k.disconnectS, k.teamLeftS);
  }
  g->tx.ep = &g->ep;  // Q3c: the session sends through the timed transport (perf's traffic and answers)
  g->tx.match = g->match.get();
  g->session.reset(new gms::GmsSession(g->tx, *g->match, so, g->start));
  g_gms = g;
  // the gms_script lines (run_session.ps1's 'gms probe' summary parses them)
  gms::Logf("%s RakNet %s server, RAKNET_PROTOCOL_VERSION %d, listening on %s:%u, guid %s; GMS session of the match "
            "host (dgsim.dll, G2) against the server simulator",
            gms::Clock().c_str(), gms::Endpoint::RakNetVersion(), gms::Endpoint::ProtocolVersion(), ip.c_str(), g->port,
            g->ep.GuidText().c_str());
  gms::Logf("session script: stop after %s, step %d ms, load timeout %d ms, prepare flags 0x%x, level 0x%08x, seed %u, "
            "level flags 0x%x; player update yes, skip shaper %s, echo status %s, team %u, noun 0x%08x; inject: %zu "
            "message(s); G3: name humans %s, wait for players %s (at most %d ms)",
            gms::SessionStepName(so.stopAfter), so.stepMs, so.loadTimeoutMs, mo.setup.prepareFlags, mo.setup.level,
            mo.setup.seed, mo.setup.levelFlags, so.skipShaper ? "yes" : "no", so.echoStatus ? "yes" : "no",
            mo.slot.team, mo.slot.noun, mo.inject.size(), so.nameHumans ? "yes" : "no",
            so.waitForPlayers ? "yes" : "no", so.waitPlayersMs);
  HostMark("GMS endpoint bound");
  return true;
}

void GmsHostPoll() {
  GmsHost* g = g_gms;
  if (!g) return;
  ++g->polls;
  {
    KindScope traffic(gms::timing::kTrafficSession);  // Q3c: what the session sends while it handles the poll
    gms::PumpTiming* timing = perf::Enabled() ? &g->pump : nullptr;
    g->packets += static_cast<unsigned>(gms::PumpEndpoint(g->ep, *g->session, g->start, timing));
    g->session->Tick();
  }
  const double now = gms::timing::NowMs();
  if (perf::Enabled() && now - g->lastSampleMs >= 250) {
    g->lastSampleMs = now;
    g->ep.SampleConnections();
  }
}

bool GmsHostActive() { return g_gms != nullptr; }

unsigned GmsHostPort() { return g_gms ? g_gms->port : 0u; }

int GmsHostConnections() { return g_gms ? static_cast<int>(g_gms->session->NumConnections()) : 0; }

void GmsHostShutdown() {
  if (g_gms) g_gms->session->Finish();
}

bool GmsHostFormatCommandStats(char* buf, std::size_t size) {
  if (!g_gms || !cmds::Installed()) return false;
  cmds::FormatStats(buf, size);
  return true;
}

void GmsHostTickCommands() {
  if (!g_gms || !cmds::Installed()) return;
  cmds::Tick(GetCurrentSim());
}

void GmsHostTickNetworkRate() {
  GmsHost* g = g_gms;
  if (!g || !g->match->NetworkRate()) return;
  unsigned mask = 0;
  if (repl::UpdateNetworkRateTimers(GetCurrentSim(), &mask)) g->match->OnNetworkRate(mask);
}

void GmsHostFlush() {
  GmsHost* g = g_gms;
  if (!g) return;
  if (cmds::Installed()) cmds::AfterTick(GetCurrentSim());
  g->match->Flush(*g->session);
}

void GmsHostFormatStats(char* buf, std::size_t size) {
  GmsHost* g = g_gms;
  if (!g) {
    std::snprintf(buf, size, "no endpoint");
    return;
  }
  std::snprintf(buf, size, "endpoint %s:%u, polls %u, packets %u, connections %zu, players%s; %s", g->ip.c_str(),
                g->port, g->polls, g->packets, g->session->NumConnections(), g->match->Players().c_str(),
                g->match->SurrenderState().c_str());
}
