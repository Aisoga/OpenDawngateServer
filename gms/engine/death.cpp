// D1f-live: death processing and gold on the server simulator; see death.h and docs/DAMAGE.md *Death handling*.
// D1h: player kill bookkeeping (K/D/A, creep score, assists, streaks, a player kill's gold and XP, the death recap).
#include "death.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "control/messages.h"
#include "core/damage/kills.h"
#include "core/damage/pipeline.h"
#include "core/damage/recap.h"
#include "core/damage/rules.h"
#include "abilities.h"
#include "banter.h"
#include "bots.h"
#include "commands.h"
#include "damage.h"
#include "director.h"
#include "game.h"
#include "hook.h"
#include "host/control.h"
#include "log.h"
#include "match.h"
#include "passives.h"
#include "perks.h"
#include "wells.h"

using namespace game;
namespace dmg = gms::damage;

namespace death {
namespace {

Options g_opt;
bool g_installed;
void* g_sim;
KillObjectFn g_origKill;
bool g_killHooked, g_goldHooked;
unsigned g_runs;  // Lua scheduler runs seen (AfterScheduler)
unsigned g_logLines;

struct Stats {
  unsigned deaths, byDamage, byKill, nearbyEvents, nearbyObjects, killEvents, playerDeaths, playerEvents, rewardDeaths,
      xpGrants, goldGrants, goldHookCalls, goldHandlers, modifiersEnded, abilitiesInterrupted, pendingDone, exceptions,
      passiveGrants, deathTimers, respawns, respawnsFailed, respawnsDropped;
  unsigned itemPassivesKept;  // Q3
  unsigned abilitiesKeptReleased;  // D6c: agent abilities that had released the agent, left running at the death
  unsigned rewardBuffs, rewardBuffsRefused;  // D14: CharacterRewardData.buffs granted / refused by the engine
  unsigned roleGoldGrants, goldHandlerChanges;  // ROLEVIM: grants a role's modifier changed; OnGrantGold changes
  double roleGold;                              // ROLEVIM: the gold the roles' modifiers added
  // D1h
  unsigned playerKills, playerDeathsCounted, assists, creeps, firstBlood, killReports, recapHits, recaps,
      playerKillGrants, assisterGrants;
  double xp, gold, goldHookIn, goldHookOut, passiveGold, startGold, playerKillGold, playerKillXp;
} g_st;

// ---- D1h state ------------------------------------------------------------------------------------------------------
dmg::KillBook g_book;
struct PendingKill {  // a player's killing blow counted before its report, until OnDeath takes it
  std::uint32_t victim = 0;
  dmg::KillOutcome o;
};
std::vector<PendingKill> g_kills;
std::map<std::uint32_t, dmg::RecapLog> g_recap;  // by the victim creature's id
std::vector<Outgoing> g_out;                      // for the GMS side (TakeOutgoing)

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 100 == 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in death (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

bool Ours() { return g_sim && GetCurrentSim() == g_sim; }

double GameSeconds() {
  __try {
    return static_cast<double>(GetTimeMs(g_sim)) / 1000.0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0.0;
  }
}

const char* NounName(void* o) {
  __try {
    void* n = o ? Field<void*>(o, gobj::kNoun) : nullptr;
    const char* s = n ? Field<const char*>(n, 0) : nullptr;
    if (s) {
      volatile char c = s[0];
      (void)c;
      return s;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return "?";
}

// The object's noun asset id (noun +0x60; what DamageTracker_AddDamageEvent records as the source).
std::uint32_t SehNounId(void* o) {
  __try {
    void* n = o ? Field<void*>(o, gobj::kNoun) : nullptr;
    return n ? Field<std::uint32_t>(n, noun::kId) : 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
}

// ---- object facts ----------------------------------------------------------------------------------------------

struct ObjInfo {
  std::uint32_t id = 0;
  int team = 0;
  int npcType = -1;  // nNPCType, -1 none
  bool player = false;  // player controlled
  int playerIndex = 0xFF;
  std::uint32_t owner = 0;
  float pos[3] = {0.f, 0.f, 0.f};
  float hp = 0.f;
  bool hasCombatant = false;
  bool hasContainer = false;
};

bool SehInfo(void* o, ObjInfo* out) {
  __try {
    out->id = Field<std::uint32_t>(o, gobj::kId);
    out->team = Field<unsigned char>(o, gobj::kTeam);
    out->player = Field<unsigned char>(o, gobj::kPlayerControlled) != 0;
    out->playerIndex = Field<unsigned char>(o, gobj::kPlayerIndex);
    out->owner = Field<std::uint32_t>(o, gobj::kOwner);
    std::memcpy(out->pos, static_cast<char*>(o) + gobj::kPosition, 12);
    char* cd = Field<char*>(o, combatant::kOfObject);
    out->hasCombatant = cd != nullptr;
    out->hp = cd ? Field<float>(cd, combatant::kHitPoints) : 0.f;
    out->hasContainer = Field<void*>(o, obj::kAbilityData) != nullptr;
    void* npc = Field<void*>(o, npc::kOfObject);
    char* nd = npc ? Field<char*>(npc, noun::kData) : nullptr;
    out->npcType = nd ? Field<int>(nd, npc::kType) : -1;
    return true;
  } __except (SehNote(GetExceptionInformation(), "object info")) {
    return false;
  }
}

void* SehFind(std::uint32_t id) {
  __try {
    return id ? GetObjectById(id) : nullptr;
  } __except (SehNote(GetExceptionInformation(), "GetObjectById")) {
    return nullptr;
  }
}

float Dist2(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

// ---- named-argument events (AbilityDataContainer_FireEvent 0x009064E0; records as LuaUtilities_FillOutPrivateTable
// 0x008939E0 reads them: +0 name, +0x54 value, +0x58 array count, +0x60 type, +0x64/+0x65 flags) ------------------------

struct Arg {
  const char* name;
  std::uint16_t type;  // 10 object id, 0xD float, 0x800A object-id array
  std::uint32_t value;
  const std::uint32_t* array;
  std::uint32_t count;
};
Arg IdArg(const char* name, std::uint32_t id) { return {name, 10, id, nullptr, 0}; }
Arg IdArrayArg(const char* name, const std::uint32_t* ids, std::uint32_t n) { return {name, 0x800A, 0, ids, n}; }

bool SehFire(void* o, int event, const Arg* args, int n) {
  __try {
    void* c = Field<void*>(o, obj::kAbilityData);
    if (!c) return false;
    alignas(8) unsigned char rec[8][kEventRecordSize];
    std::memset(rec, 0, sizeof rec);
    if (n > 8) n = 8;
    static const std::uint32_t kEmpty[1] = {0};
    for (int i = 0; i < n; ++i) {
      Field<const char*>(rec[i], 0) = args[i].name;
      if (args[i].type == 0x800A) {
        Field<const std::uint32_t*>(rec[i], 0x54) = args[i].array ? args[i].array : kEmpty;
        Field<std::uint32_t>(rec[i], 0x58) = args[i].count;
      } else {
        Field<std::uint32_t>(rec[i], 0x54) = args[i].value;
      }
      Field<std::uint16_t>(rec[i], 0x60) = args[i].type;
    }
    FireEvent(c, event, n ? rec[0] : nullptr, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "AbilityDataContainer_FireEvent")) {
    return false;
  }
}

// ---- RewardsTuning (the server level's) ------------------------------------------------------------------------

struct Shares {
  const float* p = nullptr;
  int n = 0;
};
struct Tuning {
  bool read = false;
  float startingGold = 500.f, passiveDelay = 60.f, passiveGold = 100.f;
  float radiusPlayer = 48.53f, radiusMinion = 32.9f, radiusJungle = 15.79f;
  float minionFatalRatio = 3.f, jungleFatalRatio = 1.f, towerGold = 0.f;
  Shares playerXp, playerGold, minionXp, minionGold, jungleXp, jungleGold;
  // D1g: LevelGameplayTuning's death timer (the data's values as defaults)
  bool deathRead = false;
  float deathBase = 15.f, deathPerLevel = 0.5f, deathPerMinute = 1.25f;
  // D1h: a player kill (RewardsTuning), the roles' modifiers (RolesTuning), the level steps (LevelUpTuning), the
  // multi-kill and streak tuning (BattlegroundsGameplayTuning), the recap window (UITuning)
  bool killRead = false;
  dmg::PlayerKillTuning kill;
  const char* roles = nullptr;  // role[] (0xB0 each)
  int roleCount = 0;
  std::vector<float> levelXp;  // LevelUpTuning.Level[i].xpRequired (stride 0xC in the data)
  unsigned long long multiKillTimeoutMs = 10000;
  unsigned killStreakStart = 3;
  dmg::RecapTuning recap;
} g_tuning;

Shares ReadShares(char* d, std::uintptr_t off) {
  Shares s;
  s.p = Field<const float*>(d, off);
  s.n = Field<int>(d, off + 4);
  if (!s.p || s.n < 0 || s.n > 64) s = Shares();
  return s;
}

bool SehReadTuning() {
  __try {
    void* level = Field<void*>(g_sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* asset = ld ? Field<void*>(ld, level::kRewardsTuning) : nullptr;
    char* d = asset ? Field<char*>(asset, noun::kData) : nullptr;
    if (!d) return false;
    Tuning& t = g_tuning;
    t.startingGold = Field<float>(d, rewardsTuning::kStartingGold);
    t.passiveDelay = Field<float>(d, rewardsTuning::kPassiveStartDelay);
    t.passiveGold = Field<float>(d, rewardsTuning::kPassiveGold);
    t.radiusPlayer = Field<float>(d, rewardsTuning::kRadiusPlayer);
    t.radiusMinion = Field<float>(d, rewardsTuning::kRadiusMinion);
    t.radiusJungle = Field<float>(d, rewardsTuning::kRadiusJungle);
    t.minionFatalRatio = Field<float>(d, rewardsTuning::kMinionFatalHitGoldRatio);
    t.jungleFatalRatio = Field<float>(d, rewardsTuning::kJungleFatalHitGoldRatio);
    t.towerGold = Field<float>(d, rewardsTuning::kTowerGoldReward);
    t.playerXp = ReadShares(d, rewardsTuning::kPlayerXpShare);
    t.playerGold = ReadShares(d, rewardsTuning::kPlayerGoldShare);
    t.minionXp = ReadShares(d, rewardsTuning::kMinionXpShare);
    t.minionGold = ReadShares(d, rewardsTuning::kMinionGoldShare);
    t.jungleXp = ReadShares(d, rewardsTuning::kJungleXpShare);
    t.jungleGold = ReadShares(d, rewardsTuning::kJungleGoldShare);
    t.read = true;
    return true;
  } __except (SehNote(GetExceptionInformation(), "RewardsTuning")) {
    return false;
  }
}

// D1g: LevelGameplayTuning (Level data +0x614 -> asset, data +0x68): deathTimeBase/PerLevel/PerMinute at +0/+4/+8.
bool SehReadDeathTuning() {
  __try {
    void* level = Field<void*>(g_sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* asset = ld ? Field<void*>(ld, level::kGameplayTuning) : nullptr;
    char* d = asset ? Field<char*>(asset, noun::kData) : nullptr;
    if (!d) return false;
    g_tuning.deathBase = Field<float>(d, gameplayTuning::kDeathTimeBase);
    g_tuning.deathPerLevel = Field<float>(d, gameplayTuning::kDeathTimePerLevel);
    g_tuning.deathPerMinute = Field<float>(d, gameplayTuning::kDeathTimePerMinute);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LevelGameplayTuning")) {
    return false;
  }
}

dmg::FloatArray ReadArray(char* d, std::uintptr_t off) {
  const Shares s = ReadShares(d, off);
  dmg::FloatArray a;
  a.p = s.p;
  a.n = s.n;
  return a;
}

// D1h: RewardsTuning's player-kill members, RolesTuning's roles, LevelUpTuning's levels (the server level's), and the
// global BattlegroundsGameplayTuning / UITuning.
bool SehReadKillTuning(std::string* what) {
  __try {
    Tuning& t = g_tuning;
    void* level = Field<void*>(g_sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    if (!ld) return false;
    void* ra = Field<void*>(ld, level::kRewardsTuning);
    char* rd = ra ? Field<char*>(ra, noun::kData) : nullptr;
    if (rd) {
      t.kill.killStreakGold = ReadArray(rd, rewardsTuning::kPlayerKillStreakGold);
      t.kill.deathStreakGold = ReadArray(rd, rewardsTuning::kPlayerDeathStreakGold);
      t.kill.lowerLevelGold = ReadArray(rd, rewardsTuning::kPlayerLowerLevelGold);
      t.kill.higherLevelGold = ReadArray(rd, rewardsTuning::kPlayerHigherLevelGold);
      t.kill.lowerLevelXp = ReadArray(rd, rewardsTuning::kPlayerLowerLevelXp);
      t.kill.higherLevelXp = ReadArray(rd, rewardsTuning::kPlayerHigherLevelXp);
      t.kill.xpNearbyShare = ReadArray(rd, rewardsTuning::kPlayerXpShare);
      t.kill.assistShare = ReadArray(rd, rewardsTuning::kPlayerAssistShare);
      t.kill.firstBloodGold = Field<float>(rd, rewardsTuning::kFirstBloodGold);
      t.kill.killXpPercent = Field<float>(rd, rewardsTuning::kPlayerKillXpPercent);
    } else {
      *what += " no RewardsTuning;";
    }
    void* rt = Field<void*>(ld, level::kRolesTuning);
    char* rtd = rt ? Field<char*>(rt, noun::kData) : nullptr;
    if (rtd) {
      t.roles = Field<const char*>(rtd, 0);
      t.roleCount = Field<int>(rtd, 4);
      if (!t.roles || t.roleCount < 0 || t.roleCount > 16) t.roles = nullptr, t.roleCount = 0;
    }
    void* lu = Field<void*>(ld, level::kLevelUpTuning);
    char* lud = lu ? Field<char*>(lu, noun::kData) : nullptr;
    if (lud) {
      const char* lv = Field<const char*>(lud, 0);
      const int n = Field<int>(lud, 4);
      if (lv && n > 0 && n <= 64)
        for (int i = 0; i < n; ++i) t.levelXp.push_back(Field<float>(const_cast<char*>(lv), static_cast<std::uintptr_t>(i) * 0xC));
    }
    void* bg = GetTuningAsset(reinterpret_cast<void*>(kBattlegroundsTuningHolder));
    char* bgd = bg ? Field<char*>(bg, noun::kData) : nullptr;
    if (bgd) {
      const long long ms = Field<long long>(bgd, bgTuning::kMultiKillTimeoutMs);
      if (ms > 0 && ms < 600000) t.multiKillTimeoutMs = static_cast<unsigned long long>(ms);
      t.killStreakStart = Field<unsigned>(bgd, bgTuning::kKillStreakStart);
    } else {
      *what += " no BattlegroundsGameplayTuning;";
    }
    void* ui = GetTuningAsset(reinterpret_cast<void*>(kUiTuningHolder));
    char* uid = ui ? Field<char*>(ui, noun::kData) : nullptr;
    if (uid) {
      t.recap.shortSeconds = Field<float>(uid, uiTuning::kRecapShort);
      t.recap.longSeconds = Field<float>(uid, uiTuning::kRecapLong);
    } else {
      *what += " no UITuning;";
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "player-kill tuning")) {
    return false;
  }
}

std::string ArrayText(const dmg::FloatArray& a) {
  std::string s = "[";
  char b[24];
  for (int i = 0; a.p && i < a.n; ++i) {
    std::snprintf(b, sizeof b, "%s%g", i ? " " : "", a.p[i]);
    s += b;
  }
  return s + "]";
}

const Tuning& TuningNow() {
  if (!g_tuning.killRead && g_sim) {
    g_tuning.killRead = true;
    std::string what;
    if (SehReadKillTuning(&what)) {
      const Tuning& t = g_tuning;
      Log("death: player-kill tuning: killStreakGold %s, deathStreakGold %s, firstBlood %.0f, killXPPercent %.2f, xp "
          "share %s, assist share %s, level gold %s/%s, level xp %s/%s; %d roles; %zu levels (step 1->2 %.0f XP); "
          "multiKillTimeoutMs %llu, killStreakStart %u; recap %.1f/%.1f s;%s",
          ArrayText(t.kill.killStreakGold).c_str(), ArrayText(t.kill.deathStreakGold).c_str(), t.kill.firstBloodGold,
          t.kill.killXpPercent, ArrayText(t.kill.xpNearbyShare).c_str(), ArrayText(t.kill.assistShare).c_str(),
          ArrayText(t.kill.lowerLevelGold).c_str(), ArrayText(t.kill.higherLevelGold).c_str(),
          ArrayText(t.kill.lowerLevelXp).c_str(), ArrayText(t.kill.higherLevelXp).c_str(), t.roleCount, t.levelXp.size(),
          dmg::LevelStepXp(t.levelXp.data(), static_cast<int>(t.levelXp.size()), 1), t.multiKillTimeoutMs,
          t.killStreakStart, t.recap.shortSeconds, t.recap.longSeconds, what.empty() ? "" : what.c_str());
    } else {
      Log("death: the player-kill tuning could not be read; the data's defaults are used where there are any");
    }
  }
  if (!g_tuning.read && g_sim) {
    if (SehReadTuning())
      Log("death: RewardsTuning (server level): startingGold %.0f, passive %.0f per minute from %.0f s, radius player %.2f "
          "minion %.2f jungle %.2f, fatal-hit gold ratio minion %.2f jungle %.2f, towerGoldReward %.0f, shares: minion xp "
          "%d gold %d, jungle xp %d gold %d, player xp %d gold %d",
          g_tuning.startingGold, g_tuning.passiveGold, g_tuning.passiveDelay, g_tuning.radiusPlayer, g_tuning.radiusMinion,
          g_tuning.radiusJungle, g_tuning.minionFatalRatio, g_tuning.jungleFatalRatio, g_tuning.towerGold,
          g_tuning.minionXp.n, g_tuning.minionGold.n, g_tuning.jungleXp.n, g_tuning.jungleGold.n, g_tuning.playerXp.n,
          g_tuning.playerGold.n);
    else
      Log("death: no RewardsTuning on the server level; the data's defaults are used");
    g_tuning.read = true;
  }
  if (!g_tuning.deathRead && g_sim) {
    g_tuning.deathRead = true;
    if (SehReadDeathTuning())
      Log("death: LevelGameplayTuning (server level): deathTimeBase %.2f, deathTimePerLevel %.2f, deathTimePerMinute %.2f",
          g_tuning.deathBase, g_tuning.deathPerLevel, g_tuning.deathPerMinute);
    else
      Log("death: no LevelGameplayTuning on the server level; the death timer uses 15 + 0.5 x level + 1.25 x minutes");
  }
  return g_tuning;
}

// The victim's class for radius, shares and sources: 0 player, 1 minion, 2 jungle (creeps, objectives, others),
// 3 structure (Tower, Guardian, Nub, Locus, GoldMine).
int VictimClass(const ObjInfo& v) {
  if (v.player) return 0;
  switch (v.npcType) {
    case 0: case 1: case 2: return 1;
    case 9: case 10: case 13: case 14: case 15: return 3;
    default: return 2;
  }
}
// nGoldSourceType / nXPSourceType (Lua values) of a kill.
int GoldSource(const ObjInfo& v) {
  if (v.player) return 2;  // PlayerKill
  switch (v.npcType) {
    case 0: case 1: case 2: return 4;          // Minion
    case 3: return 5;                          // JungleCreep
    case 6: case 7: case 8: return 6;          // Objective
    case 12: return 7;                         // Follower
    case 9: case 10: case 13: case 14: case 15: return 1;  // Tower
    default: return 0;
  }
}
int XpSource(const ObjInfo& v) { return GoldSource(v) > 7 ? 0 : GoldSource(v); }  // the same values up to Follower

// ---- players ---------------------------------------------------------------------------------------------------

struct PlayerView {
  int index = -1;
  void* player = nullptr;
  std::uint32_t creature = 0;
  int team = 0;
  ObjInfo body;
  bool alive = false;
};

int SehPlayers(PlayerView* out, int cap) {
  __try {
    int n = 0;
    for (int i = 0; i < 10 && n < cap; ++i) {
      void* p = GetPlayerByIndex(g_sim, static_cast<unsigned>(i));
      if (!p) continue;
      PlayerView& v = out[n];
      v.index = i;
      v.player = p;
      v.creature = Field<std::uint32_t>(p, player::kCurrentCreature);
      v.team = Field<unsigned char>(p, player::kTeam);
      v.alive = false;
      ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "players")) {
    return 0;
  }
}

std::vector<PlayerView> Players() {
  PlayerView raw[10];
  const int n = SehPlayers(raw, 10);
  std::vector<PlayerView> out;
  for (int i = 0; i < n; ++i) {
    PlayerView v = raw[i];
    void* c = SehFind(v.creature);
    if (c && SehInfo(c, &v.body)) v.alive = v.body.hp > 0.f;
    out.push_back(v);
  }
  return out;
}

float SehAvailableGold(void* player) {
  __try {
    return Field<float>(player, player::kAvailableGold);
  } __except (SehNote(GetExceptionInformation(), "mAvailableGold")) {
    return -1.f;
  }
}

bool SehGrantGold(void* player, float amount, unsigned source, bool hide, std::uint32_t target, std::uint32_t killer) {
  __try {
    game::GrantGold(player, amount, 0.f, hide, source, target, killer, 0);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_GrantGold")) {
    return false;
  }
}

bool SehGrantXp(void* player, float amount, unsigned source, std::uint32_t target, std::uint32_t lastHitter) {
  __try {
    GrantXp(player, amount, 0.f, false, source, target, lastHitter);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_GrantXPIfValid")) {
    return false;
  }
}

bool SehReward(void* victim, float* xp, float* gold, int* xpType, int* goldType, float* overrideRadius) {
  __try {
    GetRewardForNpc(victim, xp, gold);
    char* rd = GetRewardData(victim);
    if (!rd) return false;
    *overrideRadius = Field<float>(rd, reward::kOverrideRadius);
    *xpType = Field<int>(rd, reward::kXpType);
    *goldType = Field<int>(rd, reward::kGoldType);
    return true;
  } __except (SehNote(GetExceptionInformation(), "GetRewardForNPC")) {
    return false;
  }
}

// D14: the victim's CharacterRewardData.buffs (game.h reward::kBuffs): {buffType, modifier GUID, name}.
struct RewardBuff {
  int type = -1;
  std::uint32_t guid = 0;
  const char* name = "?";
};
int SehRewardBuffs(void* victim, RewardBuff* out, int cap) {
  __try {
    char* rd = GetRewardData(victim);
    if (!rd) return 0;
    char* list = Field<char*>(rd, reward::kBuffs);
    const int count = Field<int>(rd, reward::kBuffs + 4);
    if (!list || count <= 0 || count > 16) return 0;
    int n = 0;
    for (int i = 0; i < count && n < cap; ++i) {
      char* e = list + i * reward::kBuffSize;
      RewardBuff& b = out[n];
      b.type = Field<int>(e, reward::kBuffType);
      b.guid = Field<std::uint32_t>(e, reward::kBuffGuid);
      const char* nm = Field<const char*>(e, reward::kBuffName);
      b.name = nm ? nm : "?";
      if (b.guid) ++n;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "CharacterRewardData.buffs")) {
    return 0;
  }
}

// The buff on `creature` (caster the creature itself [assumed: the reward code's caster isn't known; the victim is a
// corpse about to be deleted], no snapshot, rank 1, as passives.cpp activates a passive). The engine's activation
// rules apply: a Unique modifier (every reward buff) ends the instances the creature has and starts a new one, so a
// second kill renews the 150 s (LabsAbilities_ActivateFromDefinition 0x00908AC0, activation type 1 ->
// LabsAbilities_EndAbility 0x00905E90 per existing instance). Returns the modifier id, 0 refused, -1 exception.
long long SehActivateBuff(std::uint32_t guid, std::uint32_t creature) {
  __try {
    if (!FindStaticAbility(guid)) return 0;
    return ActivateFromDefinition(guid, creature, creature, 0, 1, 0.f, 0.f, 0.f, 0);
  } __except (SehNote(GetExceptionInformation(), "LabsAbilities_ActivateFromDefinition (reward buff)")) {
    return -1;
  }
}

std::uint8_t SehLastPlayerToHit(void* victim) {
  __try {
    return LastPlayerToHit(victim);
  } __except (SehNote(GetExceptionInformation(), "DamageTracker_GetLastPlayerToHit")) {
    return 0xFF;
  }
}

// ---- D1h: the counters on the cLabsPlayer ----------------------------------------------------------------------------

unsigned long long SehNowMs();  // the server clock (below)

struct PlayerFacts {  // what the kill bookkeeping reads of one player
  void* player = nullptr;
  int team = 0;
  unsigned level = 1, role = 0;
  std::uint32_t creature = 0;
  float x = 0.f, y = 0.f;
  bool alive = false;
};

bool SehPlayerCounters(int index, PlayerFacts* f) {
  __try {
    void* p = GetPlayerByIndex(g_sim, static_cast<unsigned>(index));
    if (!p) return false;
    f->player = p;
    f->team = Field<unsigned char>(p, player::kTeam);
    f->level = Field<unsigned>(p, player::kLevel);
    f->role = Field<unsigned char>(p, player::kRole);
    f->creature = Field<std::uint32_t>(p, player::kCurrentCreature);
    return true;
  } __except (SehNote(GetExceptionInformation(), "player counters")) {
    return false;
  }
}

// +1 on a counter (kills, deaths, assists) and, when `streak`/`multi` are >= 0, the report's two fields.
bool SehBumpCounter(void* player, std::uintptr_t counter, int streak, int multi) {
  __try {
    if (counter) ++Field<unsigned>(player, counter);
    if (streak >= 0) Field<unsigned>(player, player::kKillStreak) = static_cast<unsigned>(streak);
    if (multi >= 0) Field<unsigned>(player, player::kMultiKill) = static_cast<unsigned>(multi);
    return true;
  } __except (SehNote(GetExceptionInformation(), "player counter")) {
    return false;
  }
}

bool SehAddCreeps(void* player, int n) {
  __try {
    AddCreeps(player, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LabsPlayer_AddCreeps")) {
    return false;
  }
}

struct Counters {
  unsigned kills = 0, deaths = 0, assists = 0, creeps = 0, streak = 0, multi = 0, level = 0, role = 0;
  float totalGold = 0.f;
  std::uint32_t noun = 0;
  unsigned long long onlineId = 0;
  int team = 0;
};
bool SehReadCounters(void* player, Counters* c) {
  __try {
    c->kills = Field<unsigned>(player, player::kKills);
    c->deaths = Field<unsigned>(player, player::kDeaths);
    c->assists = Field<unsigned>(player, player::kAssists);
    c->creeps = Field<unsigned>(player, player::kCreeps);
    c->streak = Field<unsigned>(player, player::kKillStreak);
    c->multi = Field<unsigned>(player, player::kMultiKill);
    c->level = Field<unsigned>(player, player::kLevel);
    c->role = Field<unsigned char>(player, player::kRole);
    c->totalGold = Field<float>(player, player::kTotalGold);
    void* n = Field<void*>(player, player::kNounDef);
    c->noun = n ? Field<std::uint32_t>(n, noun::kId) : 0;
    c->onlineId = Field<unsigned long long>(player, player::kOnlineId);
    c->team = Field<unsigned char>(player, player::kTeam);
    return true;
  } __except (SehNote(GetExceptionInformation(), "read counters")) {
    return false;
  }
}

// D8-live test fixture ([host] end_stats): the counters the post-game screens show, written into a server player.
bool SehWriteTestStats(void* player, const TestStats& t) {
  __try {
    Field<unsigned>(player, player::kKills) = static_cast<unsigned>(t.kills);
    Field<unsigned>(player, player::kDeaths) = static_cast<unsigned>(t.deaths);
    Field<unsigned>(player, player::kAssists) = static_cast<unsigned>(t.assists);
    Field<unsigned>(player, player::kCreeps) = static_cast<unsigned>(t.creeps);
    Field<float>(player, player::kTotalGold) = t.totalGold;
    Field<unsigned>(player, player::kLevel) = static_cast<unsigned>(t.level);
    if (t.role >= 0) Field<unsigned char>(player, player::kRole) = static_cast<unsigned char>(t.role);  // B7c-live
    return true;
  } __except (SehNote(GetExceptionInformation(), "write test stats")) {
    return false;
  }
}

std::string CountersText(int index) {
  PlayerFacts f;
  Counters c;
  if (!SehPlayerCounters(index, &f) || !SehReadCounters(f.player, &c)) return "?";
  char b[160];
  std::snprintf(b, sizeof b, "K/D/A %u/%u/%u, creeps %u, streak %u, multi-kill %u", c.kills, c.deaths, c.assists,
                c.creeps, c.streak, c.multi);
  return b;
}

// The assist tracker's matrix (sim +0xCD84) copied out, or false while there is none.
bool SehAssistMatrix(std::uint64_t* out) {
  __try {
    const std::uint64_t* m = Field<const std::uint64_t*>(g_sim, sim::kAssistTracker);
    if (!m) return false;
    std::memcpy(out, m, dmg::kAssistMatrixBytes);
    return true;
  } __except (SehNote(GetExceptionInformation(), "assist matrix")) {
    return false;
  }
}
void SehClearAssists(int victim) {
  __try {
    std::uint64_t* m = Field<std::uint64_t*>(g_sim, sim::kAssistTracker);
    dmg::ClearAssists(m, victim);
  } __except (SehNote(GetExceptionInformation(), "assist matrix clear")) {
  }
}

// The player whose current creature `v` is, or -1.
int PlayerOfCreature(const ObjInfo& v) {
  if (!v.player || v.playerIndex < 0 || v.playerIndex >= dmg::kMaxPlayers) return -1;
  PlayerFacts f;
  if (!SehPlayerCounters(v.playerIndex, &f) || f.creature != v.id) return -1;
  return v.playerIndex;
}

// A player creature's death counted: the credited killer (the victim's last player to hit, of another team), the
// assisters (the assist matrix, 10 s), the book (gms/core/damage/kills.h) and the client's fields: the killer's
// mKills, kill streak and multi-kill count (before the killing blow's report, which reads them), the victim's mDeaths
// and streak 0, the assisters' mAssists.
dmg::KillOutcome CountPlayerDeath(void* victim, int victimIndex) {
  const Tuning& tu = TuningNow();
  int team[dmg::kMaxPlayers] = {};
  PlayerFacts fx[dmg::kMaxPlayers];
  for (int i = 0; i < dmg::kMaxPlayers; ++i)
    if (SehPlayerCounters(i, &fx[i])) team[i] = fx[i].team;
  int killer = SehLastPlayerToHit(victim);
  if (killer >= dmg::kMaxPlayers || killer == victimIndex || !fx[killer].player || team[killer] == team[victimIndex])
    killer = -1;
  std::uint64_t matrix[dmg::kMaxPlayers * dmg::kMaxPlayers];
  std::vector<int> assisters;
  const unsigned long long now = SehNowMs();
  if (SehAssistMatrix(matrix)) assisters = dmg::Assisters(matrix, victimIndex, killer, team, now);
  const dmg::KillOutcome o = g_book.PlayerDeath(victimIndex, killer, assisters, now, tu.multiKillTimeoutMs);
  SehBumpCounter(fx[victimIndex].player, player::kDeaths, 0, 0);
  ++g_st.playerDeathsCounted;
  if (o.killer >= 0) {
    SehBumpCounter(fx[o.killer].player, player::kKills, static_cast<int>(o.killerStreak), static_cast<int>(o.multiKill));
    ++g_st.playerKills;
    if (o.firstBlood) ++g_st.firstBlood;
  }
  for (int a : o.assisters) {
    SehBumpCounter(fx[a].player, player::kAssists, -1, -1);
    ++g_st.assists;
  }
  return o;
}

// ---- the deferred ends -----------------------------------------------------------------------------------------

struct Pending {
  std::uint32_t victim = 0;
  unsigned runs = 0;  // g_runs at the death
  std::vector<std::uint32_t> modifiers, abilities;
  std::string noun;
};
std::vector<Pending> g_pending;

bool SehInstanceLists(void* o, std::uint32_t* mods, int* nm, std::uint32_t* abs, int* na, int cap) {
  __try {
    *nm = *na = 0;
    char* c = Field<char*>(o, obj::kAbilityData);
    if (!c) return false;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd) && *nm < cap; ++p)
      mods[(*nm)++] = *p;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kAbilitiesBegin);
         p && p != Field<const std::uint32_t*>(c, container::kAbilitiesEnd) && *na < cap; ++p)
      abs[(*na)++] = *p;
    return true;
  } __except (SehNote(GetExceptionInformation(), "instance lists")) {
    return false;
  }
}

// Q3: the passive instance ids a player's item slots hold (game.h player::kItemPassives), when `creature` is its creature.
int SehItemPassives(int index, std::uint32_t creature, std::uint32_t* out, int cap) {
  int n = 0;
  __try {
    if (index < 0 || index >= dmg::kMaxPlayers) return 0;
    char* pl = static_cast<char*>(GetPlayerByIndex(g_sim, static_cast<unsigned>(index)));
    if (!pl || Field<std::uint32_t>(pl, player::kCurrentCreature) != creature) return 0;
    for (unsigned s = 0; s < player::kItemSlots; ++s) {
      char* item = pl + player::kItems + s * player::kItemSize;
      if (!Field<void*>(item, 0)) continue;
      for (int k = 0; k < player::kItemPassiveCount && n < cap; ++k) {
        const std::uint32_t id = Field<std::uint32_t>(item, player::kItemPassives + 4 * k);
        if (id) out[n++] = id;
      }
    }
  } __except (SehNote(GetExceptionInformation(), "item passives")) {
  }
  return n;
}

// 1 = ended (OnAgentDeath), 0 = kept, -1 gone.
int SehEndIfOnAgentDeath(std::uint32_t id, char* name, std::size_t cap) {
  __try {
    void* in = GetInstance(id);
    if (!in) return -1;
    char* st = Field<char*>(in, inst::kStatic);
    const char* nm = st ? *reinterpret_cast<const char**>(st) : nullptr;
    std::snprintf(name, cap, "%s", nm ? nm : "?");
    // D13: the rule is gms::damage::ModifierAtDeath (core/damage/pipeline.h); the item passives were dropped from the
    // list before (death_keep_item_passives)
    if (!st || dmg::ModifierAtDeath(Field<unsigned>(st, kStaticDeactivationType), false) != dmg::AtDeath::kEnd) return 0;
    EndAbility(id);
    return 1;
  } __except (SehNote(GetExceptionInformation(), "EndAbility (OnAgentDeath)")) {
    return 0;
  }
}

// 1 = interrupted, 0 = kept (finishOnDeath or a modifier), 2 = kept (D6c: it released the agent), -1 gone.
// D6c: only an ability that still holds the agent (+0xEC) is interrupted, as the engine's own death path does: the
// behavior tree's Death leaf takes over when no finishOnDeath instance holds the agent
// (AbilityDataContainer_HasFinishOnDeathInstance 0x00900820 counts holding instances only) and aborts the running
// UseAbility action, whose abort (BTAction_UseAbility_Abort 0x008A7FC0) interrupts its instance only while it holds
// the agent. An ability that released its agent (a fired projectile it waits for: ability_TidalQueen_W_Globe,
// ability_Artist_Q_Spatter) runs on; interrupted, its projectile's hit found it gone ("Attemping to call OnHitTarget,
// but the ability has already been destroyed"). [host] death_interrupt_released=1: D1f's rule (every agent ability).
int SehInterruptIfNotFinishOnDeath(std::uint32_t id, char* name, std::size_t cap, bool released) {
  __try {
    void* in = GetInstance(id);
    if (!in) return -1;
    char* st = Field<char*>(in, inst::kStatic);
    const char* nm = st ? *reinterpret_cast<const char**>(st) : nullptr;
    std::snprintf(name, cap, "%s", nm ? nm : "?");
    if (Field<int>(in, inst::kType) != 0) return 0;
    // D13: the rule is gms::damage::AbilityAtDeath (core/damage/pipeline.h)
    const dmg::AtDeath what = dmg::AbilityAtDeath(GetFinishOnDeath(in), Field<unsigned char>(in, inst::kHoldsAgent) != 0,
                                                  released);
    if (what == dmg::AtDeath::kKeep) return 0;
    if (what == dmg::AtDeath::kKeepReleased) return 2;
    AbilityTickSetHostCause("the host's death path");
    InterruptAbility(id);
    AbilityTickSetHostCause(nullptr);
    return 1;
  } __except (SehNote(GetExceptionInformation(), "InterruptAbility")) {
    AbilityTickSetHostCause(nullptr);
    return 0;
  }
}

void RunPending(Pending& p) {
  std::string ended, interrupted, kept;
  int nEnded = 0, nInt = 0;
  char name[96];
  for (std::uint32_t id : p.modifiers) {
    const int r = SehEndIfOnAgentDeath(id, name, sizeof name);
    if (r == 1) {
      ++nEnded;
      ++g_st.modifiersEnded;
      ended += std::string(" ") + name;
    } else if (r == 0) {
      kept += std::string(" ") + name;
    }
  }
  std::string released;
  if (g_opt.interrupt)
    for (std::uint32_t id : p.abilities) {
      const int r = SehInterruptIfNotFinishOnDeath(id, name, sizeof name, g_opt.interruptReleased);
      if (r == 1) {
        ++nInt;
        ++g_st.abilitiesInterrupted;
        interrupted += std::string(" ") + name;
      } else if (r == 2) {
        ++g_st.abilitiesKeptReleased;
        released += std::string(" ") + name;
      }
    }
  if (!released.empty()) kept += " (released the agent:" + released + ")";
  ++g_st.pendingDone;
  if (LogBudget())
    Log("death: %08X %s, %u scheduler run(s) after its death (t %.2f s): OnAgentDeath modifiers ended %d:%s; abilities "
        "interrupted %d:%s; modifiers kept:%s",
        p.victim, p.noun.c_str(), g_runs - p.runs, GameSeconds(), nEnded, ended.empty() ? " -" : ended.c_str(), nInt,
        interrupted.empty() ? " -" : interrupted.c_str(), kept.empty() ? " -" : kept.c_str());
}

// ---- D1g: the player's death timer and respawn -----------------------------------------------------------------

struct Respawn {
  int index = -1;
  std::uint32_t creature = 0;
  unsigned team = 0;
  unsigned long long deathMs = 0, respawnMs = 0;
  float seconds = 0.f;
  std::string noun;
  bool ordersCleared = false;  // the stop commands after the death (outside Lua, the next AfterScheduler)
};
std::vector<Respawn> g_respawns;

// A respawned creature, watched for a few seconds: does its death behavior end and lift Immobilized/Untargetable?
struct Watch {
  std::uint32_t creature = 0;
  int index = -1;
  unsigned long long respawnedMs = 0;
  int step = 0;  // the next of kWatchMs
  std::string noun;
};
std::vector<Watch> g_watches;
const unsigned kWatchMs[] = {0, 250, 1000, 2000, 5000};

unsigned long long SehNowMs() {
  __try {
    return GetTimeMs(g_sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

// The player's level, team and creature (bound).
bool SehPlayerFacts(int index, void** player, unsigned* level, unsigned* team, std::uint32_t* creature) {
  __try {
    void* p = GetPlayerByIndex(g_sim, static_cast<unsigned>(index));
    if (!p) return false;
    *player = p;
    *level = Field<unsigned>(p, player::kLevel);
    *team = Field<unsigned char>(p, player::kTeam);
    *creature = Field<std::uint32_t>(p, player::kCurrentCreature);
    return true;
  } __except (SehNote(GetExceptionInformation(), "player facts")) {
    return false;
  }
}

bool SehWriteDeathTimer(void* player, unsigned long long deathMs, unsigned long long respawnMs) {
  __try {
    Field<unsigned long long>(player, player::kDeathTime) = deathMs;
    Field<unsigned long long>(player, player::kRespawnTime) = respawnMs;
    return true;
  } __except (SehNote(GetExceptionInformation(), "mDeathTime/mRespawnTime")) {
    return false;
  }
}

// D1g: at a player creature's death, the death timer into its cLabsPlayer (the A9 delta sends it) and a respawn entry.
void StartDeathTimer(const ObjInfo& v, std::string* line) {
  if (!g_opt.respawn) return;
  void* player = nullptr;
  unsigned level = 0, team = 0;
  std::uint32_t creature = 0;
  if (v.playerIndex == 0xFF || !SehPlayerFacts(v.playerIndex, &player, &level, &team, &creature)) {
    *line = "; death timer: no player for index " + std::to_string(v.playerIndex);
    return;
  }
  if (creature != v.id) {
    char b[120];
    std::snprintf(b, sizeof b, "; death timer: player %d's creature is %08X, not the victim; none", v.playerIndex, creature);
    *line = b;
    return;
  }
  const Tuning& t = TuningNow();
  const double matchSeconds = match::Enabled() && match::Started() ? match::MatchSeconds() : 0.0;
  const float secs = g_opt.respawnSeconds > 0.f
                         ? g_opt.respawnSeconds
                         : dmg::DeathSeconds(t.deathBase, t.deathPerLevel, t.deathPerMinute, level, matchSeconds);
  Respawn r;
  r.index = v.playerIndex;
  r.creature = v.id;
  r.team = team;
  r.deathMs = SehNowMs();
  r.respawnMs = dmg::RespawnTimeMs(r.deathMs, secs);
  r.seconds = secs;
  r.noun = NounName(SehFind(v.id));
  if (!SehWriteDeathTimer(player, r.deathMs, r.respawnMs)) return;
  ++g_st.deathTimers;
  for (auto it = g_respawns.begin(); it != g_respawns.end();)
    it = it->index == r.index ? g_respawns.erase(it) : it + 1;
  g_respawns.push_back(r);
  char b[260];
  std::snprintf(b, sizeof b,
                "; death timer: player %d (level %u, team %u) at match %.2f s: %.3f s%s -> mDeathTime %llu, mRespawnTime "
                "%llu (server ms)",
                r.index, level, team, matchSeconds, secs,
                g_opt.respawnSeconds > 0.f ? " ([host] respawn_s)" : " (deathTimeBase + PerLevel x level + PerMinute x min)",
                r.deathMs, r.respawnMs);
  *line = b;
}

struct RespawnFacts {
  float from[3] = {0.f, 0.f, 0.f};
  float pos[3] = {1e30f, 1e30f, 1e30f};
  float quat[4] = {0.f, 0.f, 0.f, 1.f};
  float hpBefore = -1.f, hpAfter = -1.f;
  bool startPoint = false;
};

// 1 done, 0 the creature is alive already (nothing to do), -1 failed.
int SehRespawn(void* object, const Respawn& r, RespawnFacts* f) {
  __try {
    std::memcpy(f->from, static_cast<char*>(object) + gobj::kPosition, sizeof f->from);
    char* cd = Field<char*>(object, combatant::kOfObject);
    if (!cd) return -1;
    f->hpBefore = Field<float>(cd, combatant::kHitPoints);
    if (f->hpBefore > 0.f) return 0;
    // nLocomotion.TeleportToPositionAndOrientation's server path with nGameSimulator.GetPlayerStartingPoint's point
    // (ability_returntobase), then nGameObject.ResurrectObject (the nub's and the parasite's respawn)
    GetPlayerStartPoint(g_sim, r.team, static_cast<unsigned>(r.index), f->pos, f->quat);
    f->startPoint = f->pos[0] < 1e29f;
    if (f->startPoint) {
      QueueStop(static_cast<unsigned>(r.index), r.creature);
      Teleport(object, f->pos, f->quat, 1);
    }
    Resurrect(object);
    f->hpAfter = Field<float>(cd, combatant::kHitPoints);
    return 1;
  } __except (SehNote(GetExceptionInformation(), "respawn")) {
    return -1;
  }
}

// The dead player's orders end, as the client's S key ends them (ClientCommandQueue_RequestStop: stop moving, type 2,
// then stop, type 8): ServerCommandQueue_TickPlayer then drops the current and queued ability commands, interrupts an
// ability that holds the agent, clears the target and sets "stopped" (no auto-acquire until the next command). Without
// it a keep-attacking basic attack survives the death: d1g_run1's shaper, killed by the Binding it attacked, walked
// back to it right after the respawn (ServerCommandQueue_RequestAbility pushing the walk-up request 0x10 again).
// [assumed: the original server ended the orders at the death; no client code shows when]
bool SehClearOrders(const Respawn& r) {
  __try {
    alignas(8) unsigned char cmd[kServerCommandSize];
    for (std::uint32_t type : {2u, 8u}) {
      std::memset(cmd, 0, sizeof cmd);
      ServerCommandInit(cmd);
      Field<std::uint32_t>(cmd, scmd::kType) = type;
      Field<std::uint32_t>(cmd, scmd::kCaster) = r.creature;
      QueueCommand(static_cast<unsigned>(r.index), cmd, 0);  // executed by the next per-tick TickPlayer
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "clear orders")) {
    return false;
  }
}

void RespawnDue() {
  if (g_respawns.empty()) return;
  const unsigned long long now = SehNowMs();
  for (auto it = g_respawns.begin(); it != g_respawns.end();) {
    if (!it->ordersCleared) {
      it->ordersCleared = true;
      const bool ok = SehClearOrders(*it);
      Log("respawn: player %d's %08X died: its orders end (stop moving + stop queued: %s)", it->index, it->creature,
          ok ? "ok" : "FAILED");
    }
    const Respawn r = *it;
    if (now < r.respawnMs) {
      ++it;
      continue;
    }
    it = g_respawns.erase(it);
    void* player = nullptr;
    unsigned level = 0, team = 0;
    std::uint32_t creature = 0;
    void* object = SehFind(r.creature);
    if (match::Enabled() && match::Ended()) {
      ++g_st.respawnsDropped;
      Log("respawn: player %d's %08X not respawned: the match has ended", r.index, r.creature);
      continue;
    }
    if (!object || !SehPlayerFacts(r.index, &player, &level, &team, &creature) || creature != r.creature) {
      ++g_st.respawnsDropped;
      Log("respawn: player %d's %08X is gone or no longer the player's creature (%08X); dropped", r.index, r.creature,
          creature);
      continue;
    }
    RespawnFacts f;
    const int rc = SehRespawn(object, r, &f);
    if (rc < 0) {
      ++g_st.respawnsFailed;
      Log("respawn: player %d's %08X %s FAILED (see the exception above)", r.index, r.creature, r.noun.c_str());
      continue;
    }
    if (rc == 0) {
      ++g_st.respawnsDropped;
      Log("respawn: player %d's %08X %s is alive already (hp %.1f); nothing to do", r.index, r.creature, r.noun.c_str(),
          f.hpBefore);
      continue;
    }
    ++g_st.respawns;
    Log("respawn: player %d's %08X %s at server %llu ms (due %llu, %.3f s after the death at %llu; match %.2f s): from "
        "(%.2f, %.2f) %s (%.2f, %.2f, %.2f) orientation (%.3f, %.3f, %.3f, %.3f), stop queued; GameObject_Resurrect: hp "
        "%.1f -> %.1f",
        r.index, r.creature, r.noun.c_str(), now, r.respawnMs, static_cast<double>(now - r.deathMs) / 1000.0, r.deathMs,
        match::Enabled() ? match::MatchSeconds() : 0.0, f.from[0], f.from[1],
        f.startPoint ? "teleported to the start point" : "NOT teleported (no start point)", f.pos[0], f.pos[1], f.pos[2],
        f.quat[0], f.quat[1], f.quat[2], f.quat[3], f.hpBefore, f.hpAfter);
    // D6c: the shaper's passive again (it ended at the death: OnAgentDeath), where GameObject_OnRevived calls the
    // compiled-out player hook (passives.h)
    passives::Activate(g_sim, static_cast<unsigned>(r.index), "respawn");
    // D3c: the perk page's modifiers that ended with the death (perks.h; [host] perk_respawn)
    perks::AtRespawn(g_sim, static_cast<unsigned>(r.index));
    if (g_opt.respawnFeedback) {
      // D11: the shaper's respawn voice line (PlayerRespawned, character channel, attached to the creature), to the
      // team as ability_returntobase sends its PlayerStartedReturnToBase [recipients assumed by that analogy]
      const unsigned mask =
          cmds::SendFeedback(g_sim, "PlayerRespawned.ClientFeedbackDef", r.creature, 1u << (r.team & 7u), 0);
      Log("respawn: player %d's %08X: ClientFeedbackEvent PlayerRespawned to team %u -> %s (players 0x%04x)", r.index,
          r.creature, r.team, mask ? "queued" : "FAILED", mask & 0xFFFFu);
    }
    Watch w;
    w.creature = r.creature;
    w.index = r.index;
    w.respawnedMs = now;
    w.noun = r.noun;
    g_watches.push_back(w);
  }
}

struct WatchFacts {
  float pos[3] = {0.f, 0.f, 0.f};
  float hp = -1.f, immobilized = -1.f, untargetable = -1.f;
  std::uintptr_t leaf = 0;
  bool pendingDelete = false;
};

bool SehWatchFacts(void* o, WatchFacts* f) {
  __try {
    std::memcpy(f->pos, static_cast<char*>(o) + gobj::kPosition, sizeof f->pos);
    char* cd = Field<char*>(o, combatant::kOfObject);
    f->hp = cd ? Field<float>(cd, combatant::kHitPoints) : -1.f;
    void* ad = Field<void*>(o, gobj::kAttributes);
    if (ad) {
      f->immobilized = GetAttribute(ad, 43, 1);   // nAttributeType.Immobilized
      f->untargetable = GetAttribute(ad, 48, 1);  // Untargetable
    }
    char* btd = Field<char*>(o, obj::kBehaviorTree);
    f->leaf = btd ? Field<std::uintptr_t>(btd, 0x2DC) : 0;  // the active leaf's decider record (agents.cpp bt::kActiveLeaf)
    f->pendingDelete = Field<unsigned char>(o, gobj::kPendingDelete) != 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "respawn watch")) {
    return false;
  }
}

void WatchRespawned() {
  if (g_watches.empty()) return;
  const unsigned long long now = SehNowMs();
  for (auto it = g_watches.begin(); it != g_watches.end();) {
    Watch& w = *it;
    if (now < w.respawnedMs + kWatchMs[w.step]) {
      ++it;
      continue;
    }
    WatchFacts f;
    void* o = SehFind(w.creature);
    if (o && SehWatchFacts(o, &f))
      Log("respawn: %08X %s %.2f s after the respawn: hp %.1f at (%.2f, %.2f), Immobilized %.0f, Untargetable %.0f, "
          "behavior-tree leaf record %08X%s",
          w.creature, w.noun.c_str(), static_cast<double>(now - w.respawnedMs) / 1000.0, f.hp, f.pos[0], f.pos[1],
          f.immobilized, f.untargetable, static_cast<unsigned>(f.leaf), f.pendingDelete ? ", MARKED FOR DELETE" : "");
    else
      Log("respawn: %08X %s gone %.2f s after the respawn", w.creature, w.noun.c_str(),
          static_cast<double>(now - w.respawnedMs) / 1000.0);
    if (!o || ++w.step >= static_cast<int>(sizeof kWatchMs / sizeof kWatchMs[0])) it = g_watches.erase(it);
    else ++it;
  }
}

// ---- the hooks -------------------------------------------------------------------------------------------------

float SehKillPrecheck(void* object) {
  __try {
    char* cd = object ? Field<char*>(object, combatant::kOfObject) : nullptr;
    return cd ? Field<float>(cd, combatant::kHitPoints) : -1.f;
  } __except (SehNote(GetExceptionInformation(), "kill precheck")) {
    return -1.f;
  }
}

void __cdecl KillHook(void* object, void* killer, char flag, int one, char fireEvents) {
  const bool ours = Ours() && object;
  const float before = ours ? SehKillPrecheck(object) : -1.f;
  g_origKill(object, killer, flag, one, fireEvents);
  if (ours && before > 0.f) {
    std::uint32_t kid = 0;
    if (killer) {
      ObjInfo k;
      if (SehInfo(killer, &k)) kid = k.id;
    }
    OnDeath(object, killer, kid, nullptr, "kill");
  }
}

// The OnGrantGold dispatch the client build stubbed (0x008FE500): the creature's modifiers with handler 48, the amount
// threaded through (as LabsAbilities_DoAbilityEvent_OnGrantXP 0x008FF440 does for XP).
int SehModifierIds(std::uint32_t creature, std::uint32_t* ids, int cap) {
  __try {
    void* o = GetObjectById(creature);
    char* c = o ? Field<char*>(o, obj::kAbilityData) : nullptr;
    if (!c) return 0;
    int n = 0;
    for (const std::uint32_t* p = Field<const std::uint32_t*>(c, container::kModifiersBegin);
         p && p != Field<const std::uint32_t*>(c, container::kModifiersEnd) && n < cap; ++p)
      ids[n++] = *p;
    return n;
  } __except (SehNote(GetExceptionInformation(), "OnGrantGold modifiers")) {
    return 0;
  }
}

float __cdecl OnGrantGoldHook(std::uint32_t creature, float amount, std::uint32_t target, std::uint32_t killer,
                              std::uint32_t unknown) {
  (void)unknown;
  if (!Ours()) return 0.f;  // the client build's answer
  ++g_st.goldHookCalls;
  g_st.goldHookIn += amount;
  std::uint32_t ids[64];
  const int n = SehModifierIds(creature, ids, 64);
  for (int i = 0; i < n; ++i) {
    HandlerField f[3] = {{"grantGoldAmount", 0, amount, nullptr, 0},
                         {"grantGoldTarget", 2, 0.f, nullptr, target},
                         {"grantGoldKiller", 2, 0.f, nullptr, killer}};
    bool called = false;
    const float r = CallInstanceHandler(ids[i], fns::kOnGrantGold, amount, f, 3, target, &called);
    if (called) {
      ++g_st.goldHandlers;
      if (r >= 0.f && r != amount) {
        // ROLEVIM: a handler that changed the amount (the Gladiator's stored last hits, the Predator's workers, ...)
        ++g_st.goldHandlerChanges;
        if (LogBudget()) {
          Log("death: OnGrantGold creature %08X modifier %08X: %.2f -> %.2f (target %08X, killer %08X)", creature, ids[i],
              amount, r, target, killer);
        }
      }
      if (r >= 0.f) amount = r;
    }
  }
  g_st.goldHookOut += amount;
  return amount;
}

// The objects within `radius` of `pos` that have an ability container (alive, not pending delete), besides `skipA` and
// `skipB`.
int SehNearby(const float* pos, float radius, std::uint32_t skipA, std::uint32_t skipB, void** out, int cap) {
  __try {
    void* om = Field<void*>(g_sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < cap; o = ObjMgrIterNext(om, &it)) {
      if (Field<unsigned char>(o, gobj::kPendingDelete) || !Field<void*>(o, obj::kAbilityData)) continue;
      const std::uint32_t id = Field<std::uint32_t>(o, gobj::kId);
      if (id == skipA || id == skipB) continue;
      char* cd = Field<char*>(o, combatant::kOfObject);
      if (cd && !(Field<float>(cd, combatant::kHitPoints) > 0.f)) continue;
      const float* p = reinterpret_cast<const float*>(static_cast<char*>(o) + gobj::kPosition);
      if (Dist2(p, pos) > radius) continue;
      out[n++] = o;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "nearby objects")) {
    return 0;
  }
}

// `base` (optional): the same split without the roles' modifiers; a grant that differs shows "role +x" (ROLEVIM).
std::string Grants(const std::vector<dmg::Grant>& g, const std::vector<dmg::Grant>* base = nullptr) {
  std::string s;
  char b[80];
  for (const dmg::Grant& x : g) {
    float before = x.amount;
    if (base)
      for (const dmg::Grant& y : *base)
        if (y.player == x.player) before = y.amount;
    char role[32] = "";
    if (std::fabs(x.amount - before) > 0.001f) std::snprintf(role, sizeof role, "role %+.2f", x.amount - before);
    std::snprintf(b, sizeof b, " p%d +%.2f%s%s%s%s", x.player, x.amount, x.lastHit || role[0] ? " (" : "",
                  x.lastHit ? "last hit" : "", x.lastHit && role[0] ? ", " : "", role[0] ? role : "");
    s += b;
    if (x.lastHit || role[0]) s += ")";
  }
  return s.empty() ? " -" : s;
}

// ROLEVIM: RolesTuning role[`role`]'s NPC-kill gold modifiers (1.0 each without the tuning or for an unknown role).
void SehRoleNpcGold(unsigned role, dmg::RoleNpcGold* out) {
  const Tuning& t = g_tuning;
  if (!t.roles || static_cast<int>(role) >= t.roleCount) return;
  __try {
    const char* r = t.roles + role * roleTuning::kSize;
    out->lastHitMinion = *reinterpret_cast<const float*>(r + roleTuning::kLastHitMinionGold);
    out->lastHitCreature = *reinterpret_cast<const float*>(r + roleTuning::kLastHitCreatureGold);
    out->proximity = *reinterpret_cast<const float*>(r + roleTuning::kProximityGold);
    out->proximityAllyLastHit = *reinterpret_cast<const float*>(r + roleTuning::kProximityAllyLastHitGold);
  } __except (SehNote(GetExceptionInformation(), "RolesTuning NPC gold")) {
  }
}

void Rewards(void* victim, const ObjInfo& v, const ObjInfo* killer, int creditIndex, std::string* line) {
  float xp = 0.f, gold = 0.f, overrideRadius = 0.f;
  int xpType = -1, goldType = -1;
  if (!SehReward(victim, &xp, &gold, &xpType, &goldType, &overrideRadius)) {
    *line = " rewards: no CharacterRewardData";
    return;
  }
  const Tuning& t = TuningNow();
  const int cls = VictimClass(v);
  float radius = cls == 0 ? t.radiusPlayer : cls == 2 ? t.radiusJungle : t.radiusMinion;
  if (overrideRadius > 0.f) radius = overrideRadius;
  int receiving = v.team == 1 ? 2 : v.team == 2 ? 1 : 0;
  if (!receiving && killer && (killer->team == 1 || killer->team == 2)) receiving = killer->team;
  const std::vector<PlayerView> players = Players();
  std::vector<int> area, team;
  int lastHitter = -1;
  for (const PlayerView& p : players) {
    if (!receiving || p.team != receiving) continue;
    team.push_back(p.index);
    if (p.alive && Dist2(p.body.pos, v.pos) <= radius) area.push_back(p.index);
    if (p.index == creditIndex) lastHitter = p.index;
  }
  const Shares xs = cls == 0 ? t.playerXp : cls == 2 ? t.jungleXp : t.minionXp;
  const Shares gs = cls == 0 ? t.playerGold : cls == 2 ? t.jungleGold : t.minionGold;
  const float ratio = cls == 2 ? t.jungleFatalRatio : cls == 1 ? t.minionFatalRatio : 0.f;
  const std::vector<dmg::Grant> xg = dmg::SplitReward(xpType, xp, area, lastHitter, team, xs.p, xs.n, 0.f);
  // ROLEVIM: the roles' gold modifiers for a lane minion or a jungle creature ([host] role_passives)
  const dmg::NpcGoldKind kind = v.player ? dmg::kNpcOther
                                : v.npcType <= 2 ? dmg::kNpcMinion
                                : v.npcType == 3 ? dmg::kNpcCreature
                                                 : dmg::kNpcOther;
  dmg::RoleNpcGold roles[dmg::kMaxPlayers];
  const bool useRoles = g_opt.roleGold && kind != dmg::kNpcOther && t.roles;
  if (useRoles)
    for (int idx : team) {
      PlayerFacts f;
      if (idx >= 0 && idx < dmg::kMaxPlayers && SehPlayerCounters(idx, &f)) SehRoleNpcGold(f.role, &roles[idx]);
    }
  const std::vector<dmg::Grant> gg0 = dmg::SplitReward(goldType, gold, area, lastHitter, team, gs.p, gs.n, ratio);
  const std::vector<dmg::Grant> gg = useRoles ? dmg::SplitReward(goldType, gold, area, lastHitter, team, gs.p, gs.n,
                                                                 ratio, kind, roles, dmg::kMaxPlayers)
                                              : gg0;
  if (useRoles)
    for (const dmg::Grant& g : gg)
      for (const dmg::Grant& g0 : gg0)
        if (g.player == g0.player && g.amount != g0.amount) {
          ++g_st.roleGoldGrants;
          g_st.roleGold += g.amount - g0.amount;
        }
  std::uint32_t lastHitterCreature = 0;
  for (const PlayerView& p : players)
    if (p.index == lastHitter) lastHitterCreature = p.creature;
  for (const dmg::Grant& g : xg)
    for (const PlayerView& p : players)
      if (p.index == g.player && SehGrantXp(p.player, g.amount, static_cast<unsigned>(XpSource(v)) + kXpSourceOffset, v.id,
                                            lastHitterCreature)) {
        ++g_st.xpGrants;
        g_st.xp += g.amount;
      }
  for (const dmg::Grant& g : gg)
    for (const PlayerView& p : players)
      if (p.index == g.player &&
          SehGrantGold(p.player, g.amount, static_cast<unsigned>(GoldSource(v)) + kGoldSourceOffset, false, v.id,
                       killer ? killer->id : 0)) {
        ++g_st.goldGrants;
        g_st.gold += g.amount;
      }
  if (!xg.empty() || !gg.empty()) ++g_st.rewardDeaths;
  char b[200];
  std::snprintf(b, sizeof b, " rewards: xp %.2f (type %d), gold %.2f (type %d), radius %.2f, team %d, area %zu, last hitter %d;",
                xp, xpType, gold, goldType, radius, receiving, area.size(), lastHitter);
  *line = std::string(b) + " xp" + Grants(xg) + "; gold" + Grants(gg, useRoles ? &gg0 : nullptr);
  // D14: the reward buffs (the jungle majors' Green/Blue/Orange to the last player to hit, the parasite's stage 2/3
  // buffs to the team), to living receivers only (gms::damage::BuffReceivers).
  if (!g_opt.rewardBuffs) return;
  RewardBuff buffs[8];
  const int nb = SehRewardBuffs(victim, buffs, 8);
  if (nb <= 0) return;
  std::vector<int> alive;
  for (const PlayerView& p : players)
    if (p.alive && p.creature && (!receiving || p.team == receiving)) alive.push_back(p.index);
  for (int i = 0; i < nb; ++i) {
    const std::vector<int> to = dmg::BuffReceivers(buffs[i].type, area, lastHitter, team, alive);
    std::string s;
    for (int idx : to)
      for (const PlayerView& p : players) {
        if (p.index != idx) continue;
        const long long m = SehActivateBuff(buffs[i].guid, p.creature);
        char e[96];
        std::snprintf(e, sizeof e, " p%d %08X -> %s %08llX", idx, p.creature, m > 0 ? "modifier" : "REFUSED",
                      m > 0 ? static_cast<unsigned long long>(m) : 0ull);
        s += e;
        if (m > 0) ++g_st.rewardBuffs;
        else ++g_st.rewardBuffsRefused;
      }
    char h[160];
    std::snprintf(h, sizeof h, " buff %s (%08X, type %d):", buffs[i].name, buffs[i].guid, buffs[i].type);
    *line += std::string(";") + h + (s.empty() ? " nobody (no living receiver)" : s);
  }
}

// RolesTuning role[`role`]'s player-kill modifiers (1.0 each without the tuning or for an unknown role).
void SehRoleModifiers(unsigned role, float* killGold, float* assistGold, float* xp) {
  const Tuning& t = g_tuning;
  if (!t.roles || static_cast<int>(role) >= t.roleCount) return;
  __try {
    const char* r = t.roles + role * roleTuning::kSize;
    *killGold = *reinterpret_cast<const float*>(r + roleTuning::kKillGold);
    *assistGold = *reinterpret_cast<const float*>(r + roleTuning::kAssistGold);
    *xp = *reinterpret_cast<const float*>(r + roleTuning::kKillXp);
  } __except (SehNote(GetExceptionInformation(), "RolesTuning")) {
  }
}

// D1h: a player kill's gold and XP (gms::damage::ComputePlayerKillRewards [assumed rules]): the killer's team's players
// as receivers, in the area when alive within radiusForPlayerKillRewards of the victim; the roles' modifiers from
// RolesTuning by mRole; the victim's level step from LevelUpTuning. Granted through LabsPlayer_GrantGold (PlayerKill 2
// / PlayerAssist 3, so OnGrantGold runs) and LabsPlayer_GrantXPIfValid (the same sources).
void PlayerKillRewards(const ObjInfo& v, int victimIndex, const dmg::KillOutcome& o, std::string* line) {
  if (o.killer < 0) {
    *line = "; player-kill rewards: none (no player credited)";
    return;
  }
  const Tuning& t = TuningNow();
  PlayerFacts vf;
  SehPlayerCounters(victimIndex, &vf);
  PlayerFacts kf;
  SehPlayerCounters(o.killer, &kf);
  const std::vector<PlayerView> players = Players();
  std::vector<dmg::KillReceiver> rc;
  std::uint32_t killerCreature = 0;
  for (const PlayerView& p : players) {
    if (p.team != kf.team) continue;
    PlayerFacts f;
    if (!SehPlayerCounters(p.index, &f)) continue;
    dmg::KillReceiver r;
    r.player = p.index;
    r.level = static_cast<int>(f.level);
    r.inArea = p.alive && Dist2(p.body.pos, v.pos) <= t.radiusPlayer;
    SehRoleModifiers(f.role, &r.killGoldModifier, &r.assistGoldModifier, &r.xpModifier);
    if (p.index == o.killer) killerCreature = p.creature;
    rc.push_back(r);
  }
  const float step = dmg::LevelStepXp(t.levelXp.data(), static_cast<int>(t.levelXp.size()), static_cast<int>(vf.level));
  const dmg::PlayerKillRewards r = dmg::ComputePlayerKillRewards(t.kill, o, static_cast<int>(vf.level), step, rc);
  for (const dmg::Grant& g : r.gold)
    for (const PlayerView& p : players)
      if (p.index == g.player &&
          SehGrantGold(p.player, g.amount, (g.lastHit ? 2u : 3u) + kGoldSourceOffset, false, v.id, killerCreature)) {
        ++(g.lastHit ? g_st.playerKillGrants : g_st.assisterGrants);
        g_st.playerKillGold += g.amount;
      }
  if (r.firstBloodGold > 0.f)
    for (const PlayerView& p : players)
      if (p.index == o.killer && SehGrantGold(p.player, r.firstBloodGold, 2u + kGoldSourceOffset, false, v.id, killerCreature))
        g_st.playerKillGold += r.firstBloodGold;
  for (const dmg::Grant& g : r.xp)
    for (const PlayerView& p : players)
      if (p.index == g.player &&
          SehGrantXp(p.player, g.amount, (g.player == o.killer ? 2u : 3u) + kXpSourceOffset, v.id, killerCreature))
        g_st.playerKillXp += g.amount;
  char b[200];
  std::snprintf(b, sizeof b,
                "; player-kill rewards: bounty %.0f (victim streak %u, death streak %u), first blood %.0f, xp %.2f (%.0f%% "
                "of the level-%u step %.0f);",
                r.baseGold, o.victimKillStreak, o.victimDeathStreak, r.firstBloodGold, r.xpBase, t.kill.killXpPercent * 100.f,
                vf.level, step);
  *line = std::string(b) + " gold" + Grants(r.gold) + "; xp" + Grants(r.xp);
}

// D1h: the victim's death recap (gms/core/damage/recap.h) from the hits logged since its last death, queued for its
// player's connection (TakeOutgoing); the log starts over.
void QueueRecap(const ObjInfo& v, int victimIndex, std::string* line) {
  const Tuning& t = TuningNow();
  const unsigned long long now = SehNowMs();
  std::vector<dmg::RecapHit> hits;
  auto it = g_recap.find(v.id);
  if (it != g_recap.end()) {
    hits = dmg::RecapWindow(it->second.hits, now, t.recap);
    g_recap.erase(it);
  }
  dmg::RecapSummary s;
  Outgoing m;
  m.player = victimIndex;
  m.msg = dmg::BuildDeathRecap(hits, now, &s);
  char b[260];
  std::snprintf(b, sizeof b,
                "DeathRecap: %u hit(s) in %.1f s, %.2f damage (physical %.2f, magical %.2f, true %.2f), %u source(s), "
                "%u timeline entr%s, type lists %u/%u/%u (%zu bytes)",
                s.hits, s.seconds, s.total, s.physical, s.magical, s.trueDamage, s.rows, s.entries,
                s.entries == 1 ? "y" : "ies", s.physicalEntries, s.magicalEntries, s.trueEntries, m.msg.size());
  m.what = b;
  if (g_out.size() < 64) g_out.push_back(m);
  ++g_st.recaps;
  *line = std::string("; ") + b + " queued for player " + std::to_string(victimIndex);
}

dmg::PassiveGold g_passive;
bool g_startGiven[10];
double g_lastGoldLog = -1.0;

// The player index that gets the last hit: the snapshot's kill-credit override, else its caster's player index, else
// the killer's (a player's creature) or its owner's (a pet) player index; 0xFF none.
int CreditIndex(void* snapshot, const ObjInfo* killer) {
  int idx = 0xFF;
  if (snapshot) {
    __try {
      idx = Field<unsigned char>(snapshot, snap::kCreditPlayerIndex);
      if (idx == 0xFF) idx = Field<unsigned char>(snapshot, snap::kPlayerIndex);
    } __except (SehNote(GetExceptionInformation(), "snapshot credit")) {
      idx = 0xFF;
    }
  }
  if (idx == 0xFF && killer) {
    if (killer->player) {
      idx = killer->playerIndex;
    } else if (killer->owner) {
      ObjInfo o;
      void* ow = SehFind(killer->owner);
      if (ow && SehInfo(ow, &o) && o.player) idx = o.playerIndex;
    }
  }
  return idx;
}

}  // namespace

bool Install(const Options& options) {
  g_opt = options;
  if (!g_opt.enabled) {
    Log("death: off ([host] death = 0)");
    return false;
  }
  const bool ok = CodeMatches(0x009064E0, kFireEventBytes, sizeof kFireEventBytes) &&
                  CodeMatches(0x00912790, kGrantGoldBytes, sizeof kGrantGoldBytes);
  if (!ok) {
    Log("death: a function's prologue differs (not build 854?); death processing stays off");
    g_opt.enabled = false;
    return false;
  }
  if (g_opt.killHook)
    g_origKill = reinterpret_cast<KillObjectFn>(
        HookDetour(0x008E0210, kKillObjectBytes, sizeof kKillObjectBytes, reinterpret_cast<void*>(&KillHook)));
  g_killHooked = g_origKill != nullptr;
  if (g_opt.goldHook)
    g_goldHooked = HookReplace(kOnGrantGoldStub, kOnGrantGoldStubBytes, sizeof kOnGrantGoldStubBytes,
                               reinterpret_cast<void*>(&OnGrantGoldHook));
  // D1g: the respawn calls the client's start-point, stop, teleport and resurrect functions
  if (g_opt.respawn && !(CodeMatches(0x008F3310, kGetPlayerStartPointBytes, sizeof kGetPlayerStartPointBytes) &&
                         CodeMatches(0x00924010, kQueueStopBytes, sizeof kQueueStopBytes) &&
                         CodeMatches(0x008E2510, kTeleportBytes, sizeof kTeleportBytes) &&
                         CodeMatches(0x008E05C0, kResurrectBytes, sizeof kResurrectBytes))) {
    Log("death: a respawn function's prologue differs; the death timer and respawn stay off");
    g_opt.respawn = false;
  }
  g_installed = true;
  Log("death (D1f-live): GameObject_Kill detour %s, OnGrantGold stub %s; nearby events %d, rewards %d, ends after %d "
      "scheduler run(s), interrupts %d, starting gold %d, passive gold %d; D1g death timer and respawn %d (%s)",
      g_opt.killHook ? (g_killHooked ? "installed" : "FAILED") : "off",
      g_opt.goldHook ? (g_goldHooked ? "replaced" : "FAILED") : "kept", g_opt.nearby, g_opt.rewards, g_opt.endRuns,
      g_opt.interrupt, g_opt.startGold, g_opt.passiveGold, g_opt.respawn,
      g_opt.respawnSeconds > 0.f ? ("fixed " + std::to_string(g_opt.respawnSeconds) + " s").c_str()
                                 : "LevelGameplayTuning's formula");
  return true;
}

bool Enabled() { return g_installed; }

void SetSim(void* sim) { g_sim = sim; }

void OnSimulatorCreated(void* sim) {
  if (!g_installed || !sim) return;
  __try {
    void*& stats = Field<void*>(sim, sim::kMatchStats);
    if (stats) {
      Log("death: the server simulator already has a match-stats object (+0x558 = %p)", stats);
    } else if (void* t = Alloc(kMatchStatsSize, "SP_Simulator/MatchStats (host)", 0, 0, 0, 0)) {
      std::memset(t, 0, kMatchStatsSize);
      stats = t;
      Log("death: the match-stats object (sim +0x558, 0x%X zeroed bytes: its player map at +0x194 empty) created at "
          "%p; LabsPlayer_AddGold and LabsPlayer_GrantXP add to it",
          kMatchStatsSize, t);
    }
  } __except (SehNote(GetExceptionInformation(), "match-stats object")) {
  }
  // D1h: the assist tracker (sim +0xCD84) the stripped server shell created: without it AssistTracker_RecordHit and
  // AssistTracker_HandleBuffEvent are skipped (Damage_RecordAttacker and 0x008E1480 check it), so no assist is recorded
  __try {
    void*& at = Field<void*>(sim, sim::kAssistTracker);
    if (at) {
      Log("death: the server simulator already has an assist tracker (+0xCD84 = %p)", at);
    } else if (void* m = Alloc(dmg::kAssistMatrixBytes, "SP_Simulator/AssistTracker (host)", 0, 0, 0, 0)) {
      std::memset(m, 0, dmg::kAssistMatrixBytes);
      at = m;
      Log("death: the assist tracker (sim +0xCD84, 10 x 10 u64 ms [attacker x 10 + target], 0x%X zeroed bytes) created "
          "at %p; AssistTracker_RecordHit and AssistTracker_HandleBuffEvent fill it",
          dmg::kAssistMatrixBytes, m);
    }
  } __except (SehNote(GetExceptionInformation(), "assist tracker")) {
  }
}

void OnDeath(void* victim, void* killer, std::uint32_t killerId, void* snapshot, const char* how) {
  if (!g_installed || !Ours() || !victim) return;
  ObjInfo v;
  if (!SehInfo(victim, &v)) return;
  if (!killer && killerId) killer = SehFind(killerId);
  ObjInfo k;
  const bool hasKiller = killer && SehInfo(killer, &k);
  if (hasKiller) killerId = k.id;
  ++g_st.deaths;
  if (std::strcmp(how, "kill") == 0) ++g_st.byKill; else ++g_st.byDamage;
  wells::OnObjectDied(v.id);  // D9: a well's worker frees its slot
  // CAMPICONS: a camp's last creature: the clear and who saw it (mVisibleToTeam now, and the killer's team)
  director::OnObjectDied(v.id, hasKiller ? k.team : 0);
  const double t = GameSeconds();
  // remember the instances that exist now (step 5)
  Pending p;
  p.victim = v.id;
  p.runs = g_runs;
  p.noun = NounName(victim);
  {
    std::uint32_t mods[64], abs[64];
    int nm = 0, na = 0;
    if (SehInstanceLists(victim, mods, &nm, abs, &na, 64)) {
      p.modifiers.assign(mods, mods + nm);
      p.abilities.assign(abs, abs + na);
    }
    // Q3: a player's item passives stay (death.h): drop them from the instances to end
    std::uint32_t keep[11 * 4];
    const int nk = g_opt.keepItemPassives && v.player ? SehItemPassives(v.playerIndex, v.id, keep, 11 * 4) : 0;
    for (int ik = 0; ik < nk; ++ik)
      for (auto it = p.modifiers.begin(); it != p.modifiers.end();)
        if (*it == keep[ik]) {
          it = p.modifiers.erase(it);
          ++g_st.itemPassivesKept;
        } else {
          ++it;
        }
  }
  std::string events;
  char b[200];
  const Tuning& tu = TuningNow();
  // D1h: a player's creature: the kill bookkeeping (already done before the killing blow's report on the damage path;
  // the GameObject_Kill path counts here)
  const int victimIndex = PlayerOfCreature(v);
  dmg::KillOutcome ko;
  bool counted = false;
  if (victimIndex >= 0) {
    for (auto it = g_kills.begin(); it != g_kills.end(); ++it)
      if (it->victim == v.id) {
        ko = it->o;
        g_kills.erase(it);
        counted = true;
        break;
      }
    if (!counted) ko = CountPlayerDeath(victim, victimIndex);
  }
  if (g_opt.nearby) {
    // 1. OnNearbyDeath on the victim, the killer and everything with abilities within the reward radius
    float radius = VictimClass(v) == 0 ? tu.radiusPlayer : VictimClass(v) == 2 ? tu.radiusJungle : tu.radiusMinion;
    {
      float xp = 0.f, gold = 0.f, over = 0.f;
      int xt = 0, gt = 0;
      if (SehReward(victim, &xp, &gold, &xt, &gt, &over) && over > 0.f) radius = over;
    }
    const Arg nearby[2] = {IdArg("nearbyDeathVictim", v.id), IdArg("nearbyDeathKiller", killerId)};
    int fired = 0;
    if (v.hasContainer && SehFire(victim, fns::kOnNearbyDeath, nearby, 2)) ++fired;
    if (hasKiller && k.id != v.id && k.hasContainer && SehFire(killer, fns::kOnNearbyDeath, nearby, 2)) ++fired;
    void* around[64];
    const int n = SehNearby(v.pos, radius, v.id, killerId, around, 64);
    for (int i = 0; i < n; ++i)
      if (SehFire(around[i], fns::kOnNearbyDeath, nearby, 2)) ++fired;
    g_st.nearbyEvents += static_cast<unsigned>(fired);
    g_st.nearbyObjects += static_cast<unsigned>(n);
    std::snprintf(b, sizeof b, " OnNearbyDeath on %d (victim, killer, %d within %.2f)", fired, n, radius);
    events += b;
    // 2. OnKill on the killer
    if (hasKiller && k.id != v.id && k.hasContainer) {
      const Arg kill[1] = {IdArg("onKillVictim", v.id)};
      if (SehFire(killer, fns::kOnKill, kill, 1)) {
        ++g_st.killEvents;
        events += "; OnKill";
      }
    }
    // 3. a player's creature: OnPlayerDeath on every player's creature, OnPlayerKillOrAssist on the killer's and (D1h)
    // the assisters' (playerDeathAssistPlayers: their creatures)
    if (v.player) {
      ++g_st.playerDeaths;
      const std::uint8_t lp = SehLastPlayerToHit(victim);
      std::uint32_t lastCreature = 0;
      std::vector<std::uint32_t> assistCreatures;
      const std::vector<PlayerView> players = Players();
      for (const PlayerView& pl : players) {
        if (ko.killer >= 0 ? pl.index == ko.killer : pl.index == lp) lastCreature = pl.creature;
        for (int a : ko.assisters)
          if (pl.index == a && pl.creature) assistCreatures.push_back(pl.creature);
      }
      const Arg pd[4] = {IdArg("playerDeathVictim", v.id), IdArg("playerDeathLastPlayerToHit", lastCreature),
                         IdArrayArg("playerDeathAssistPlayers", assistCreatures.empty() ? nullptr : assistCreatures.data(),
                                    static_cast<std::uint32_t>(assistCreatures.size())),
                         IdArg("myKillerID", killerId)};
      int pe = 0;
      for (const PlayerView& pl : players) {
        void* c = SehFind(pl.creature);
        if (c && SehFire(c, fns::kOnPlayerDeath, pd, 4)) ++pe;
      }
      std::vector<std::uint32_t> credited(assistCreatures);
      const std::uint32_t credit = lastCreature ? lastCreature : (hasKiller && k.player ? k.id : 0);
      if (credit) credited.insert(credited.begin(), credit);
      const Arg pk[1] = {IdArg("onPlayerKillVictim", v.id)};
      for (std::uint32_t id : credited) {
        void* c = SehFind(id);
        if (c && SehFire(c, fns::kOnPlayerKillOrAssist, pk, 1)) ++pe;
      }
      g_st.playerEvents += static_cast<unsigned>(pe);
      std::snprintf(b, sizeof b,
                    "; player victim: OnPlayerDeath/OnPlayerKillOrAssist %d (last player to hit %u -> %08X, %zu "
                    "assister(s))",
                    pe, lp, lastCreature, assistCreatures.size());
      events += b;
    }
  }
  // D1h: the counts after this death, a player kill's gold and XP, the death recap, the spent assist credits
  std::string kills;
  if (victimIndex >= 0) {
    std::string who;
    for (int a : ko.assisters) who += " " + std::to_string(a);
    char kb[400];
    std::snprintf(kb, sizeof kb,
                  "; kill bookkeeping (%s): victim player %d (%s), killer player %d%s, assisters:%s, victim streak %u -> "
                  "0, killer streak %u -> %u, multi-kill %u",
                  counted ? "before the report" : "here", victimIndex, CountersText(victimIndex).c_str(), ko.killer,
                  ko.firstBlood ? " FIRST BLOOD" : "", who.empty() ? " -" : who.c_str(), ko.victimKillStreak,
                  ko.killerPrevStreak, ko.killerStreak, ko.multiKill);
    kills = kb;
    if (ko.killer >= 0) kills += " (killer " + CountersText(ko.killer) + ")";
    std::string pk;
    if (g_opt.rewards) PlayerKillRewards(v, victimIndex, ko, &pk);
    kills += pk;
    std::string rc;
    QueueRecap(v, victimIndex, &rc);
    kills += rc;
    // D11b: the killer's kill line and an assister's assist line (banter.h), before the assist credits are spent (the
    // assister with the latest credit speaks)
    if (ko.killer >= 0 && banter::Enabled()) {
      std::uint64_t matrix[dmg::kMaxPlayers * dmg::kMaxPlayers];
      const bool haveMatrix = SehAssistMatrix(matrix);
      std::vector<gms::banter::Assist> credits;
      for (int a : ko.assisters) {
        gms::banter::Assist c;
        c.player = a;
        c.creditMs = haveMatrix ? dmg::AssistTime(matrix, a, victimIndex) : 0;
        credits.push_back(c);
      }
      banter::OnPlayerKill(g_sim, victimIndex, ko.killer, credits, ko.multiKill);
    }
    SehClearAssists(victimIndex);
  } else if (dmg::CountsAsCreep(v.npcType)) {
    // the last hitter's creep score (a minion or jungle creep; LabsPlayer_AddCreeps) [assumed rule]
    const int credit = CreditIndex(snapshot, hasKiller ? &k : nullptr);
    PlayerFacts f;
    if (credit >= 0 && credit < dmg::kMaxPlayers && SehPlayerCounters(credit, &f) && f.team != v.team &&
        SehAddCreeps(f.player, 1)) {
      g_book.Creep(credit);
      ++g_st.creeps;
      kills = "; creep score: player " + std::to_string(credit) + " " + CountersText(credit);
    }
  }
  // 4. rewards
  std::string rewards;
  if (g_opt.rewards) Rewards(victim, v, hasKiller ? &k : nullptr, CreditIndex(snapshot, hasKiller ? &k : nullptr), &rewards);
  // D1g: a player's death timer (mDeathTime/mRespawnTime) and its respawn entry
  std::string timer;
  if (v.player) StartDeathTimer(v, &timer);
  if (LogBudget() || v.player || !kills.empty())
    Log("death: %08X %s (team %d, NPC type %d%s) at %.2f s by %08X %s (%s):%s;%s%s%s; %zu modifiers, %zu abilities to "
        "end after %d scheduler run(s)",
        v.id, p.noun.c_str(), v.team, v.npcType, v.player ? ", a player's" : "", t, killerId,
        hasKiller ? NounName(killer) : "-", how, events.empty() ? " no events" : events.c_str(), rewards.c_str(),
        kills.c_str(), timer.c_str(), p.modifiers.size(), p.abilities.size(), g_opt.endRuns);
  // 5. the ends, later
  g_pending.push_back(p);
}

KillReport BeforeKillingBlowReport(void* victim) {
  KillReport r;
  if (!g_installed || !Ours() || !victim) return r;
  ObjInfo v;
  if (!SehInfo(victim, &v)) return r;
  const int index = PlayerOfCreature(v);
  if (index < 0) return r;
  // an entry left by a death whose OnDeath never ran (an exception in between) is stale: the creature lives again
  for (auto it = g_kills.begin(); it != g_kills.end();) it = it->victim == v.id ? g_kills.erase(it) : it + 1;
  PendingKill pk;
  pk.victim = v.id;
  pk.o = CountPlayerDeath(victim, index);
  r.counted = true;
  r.extraFlags = pk.o.extraFlags;
  r.victimKillStreak = pk.o.victimKillStreak;
  r.killerPrevKillStreak = pk.o.killerPrevStreak;
  g_kills.push_back(pk);
  ++g_st.killReports;
  return r;
}

void RecordPlayerHit(void* target, void* attacker, float dealt, unsigned descLo, std::uint32_t ability) {
  if (!g_installed || !Ours() || !target || !attacker || !(dealt > 0.f)) return;
  ObjInfo t, a;
  if (!SehInfo(target, &t) || !t.player || !SehInfo(attacker, &a) || a.team == t.team) return;
  dmg::RecapHit h;
  h.ms = SehNowMs();
  h.damage = dealt;
  h.descLo = descLo;
  h.ability = ability;
  h.npcType = a.npcType;
  h.team = static_cast<std::uint8_t>(a.team);
  void* nounOf = attacker;
  if (a.npcType == 11 && a.owner) {  // a pet: its owner's noun (DamageTracker_AddDamageEvent does the same)
    void* ow = SehFind(a.owner);
    if (ow) nounOf = ow;
  }
  h.noun = SehNounId(nounOf);
  g_recap[t.id].Add(h, TuningNow().recap.longSeconds);
  ++g_st.recapHits;
}

void TakeOutgoing(std::vector<Outgoing>* out) {
  out->insert(out->end(), g_out.begin(), g_out.end());
  g_out.clear();
}

FinishRewardsFn g_finishRewards;

void SetFinishRewards(FinishRewardsFn fn) { g_finishRewards = fn; }

void SetTestStats(const TestStats& t) {
  if (!g_installed || !Ours()) return;
  PlayerView raw[10];
  const int n = SehPlayers(raw, 10);
  for (int i = 0; i < n; ++i) {
    const bool ok = SehWriteTestStats(raw[i].player, t);
    Log("match: test fixture [host] end_stats: player %d K/D/A %d/%d/%d, creeps %d, total gold %.0f, level %d, role %d%s",
        raw[i].index, t.kills, t.deaths, t.assists, t.creeps, t.totalGold, t.level, t.role,
        ok ? "" : " (write FAILED)");
  }
}

void LogPlayerFinish(int winnerTeam, unsigned long long durationMs, unsigned descriptors) {
  if (!g_installed || !Ours()) return;
  for (int i = 0; i < dmg::kMaxPlayers; ++i) {
    PlayerFacts f;
    Counters c;
    if (!SehPlayerCounters(i, &f) || !SehReadCounters(f.player, &c)) continue;
    const dmg::Tally& t = g_book.t[i];
    dggms::control::PlayerFinish m;
    m.user = static_cast<std::int64_t>(c.onlineId);
    m.stats["won"] = c.team == winnerTeam ? 1 : 0;
    m.stats["team"] = c.team;
    m.stats["kills"] = c.kills;
    m.stats["deaths"] = c.deaths;
    m.stats["assists"] = c.assists;
    m.stats["creeps"] = c.creeps;
    m.stats["gold"] = static_cast<std::int64_t>(std::lround(c.totalGold));
    m.stats["level"] = c.level;
    m.stats["bestStreak"] = t.bestStreak;
    m.stats["doubleKills"] = t.multiKills[2];
    m.stats["tripleKills"] = t.multiKills[3];
    m.stats["tetraKills"] = t.multiKills[4];
    m.stats["omniKills"] = t.multiKills[5];
    m.stats["durationMs"] = static_cast<std::int64_t>(durationMs);
    m.stats["noun"] = c.noun;
    m.stats["role"] = 1 + static_cast<std::int64_t>(c.role);  // B7d: 1 + mRole
    dggms::control::Rewards rw;
    if (g_finishRewards && g_finishRewards(i, c.team == winnerTeam, durationMs, descriptors, &rw)) m.rewards = rw;
    std::string line = dggms::control::Encode(m);
    while (!line.empty() && (line.back() == '\n' || line.back() == '\r')) line.pop_back();
    // D6a: a bot's line is logged only (dgserver keeps results for its users; a bot's online id is 1-10)
    const bool bot = bots::IsBotIndex(i);
    Log("match: playerFinish for player %d: %s%s", i, line.c_str(),
        bot ? " (a bot: logged only)" : hostctl::Active() ? " (to dgserver)" : " (no control channel: logged only)");
    if (!bot) hostctl::PlayerFinish(m);  // C1b
  }
}

void AfterScheduler() {
  if (!g_installed || !Ours()) return;
  ++g_runs;
  if (!g_pending.empty()) {
    std::vector<Pending> due;
    for (auto it = g_pending.begin(); it != g_pending.end();) {
      if (g_runs - it->runs >= static_cast<unsigned>(g_opt.endRuns)) {
        due.push_back(*it);
        it = g_pending.erase(it);
      } else {
        ++it;
      }
    }
    for (Pending& p : due) RunPending(p);
  }
  // D1g: the respawns that are due, then the respawned creatures' follow-up lines
  RespawnDue();
  WatchRespawned();
}

bool GrantGold(int playerIndex, float amount, int luaSource, bool hide, std::uint32_t target, std::uint32_t killer) {
  if (!g_installed || !Ours() || amount <= 0.f) return false;
  PlayerView raw[10];
  const int n = SehPlayers(raw, 10);
  for (int i = 0; i < n; ++i)
    if (raw[i].index == playerIndex)
      return SehGrantGold(raw[i].player, amount, static_cast<unsigned>(luaSource) + kGoldSourceOffset, hide, target, killer);
  return false;
}

void OnPlayerDeployed(int playerIndex) {
  if (!g_installed || !g_opt.startGold || !Ours() || playerIndex < 0 || playerIndex >= 10) return;
  if (g_startGiven[playerIndex]) return;
  g_startGiven[playerIndex] = true;
  const Tuning& t = TuningNow();
  PlayerView raw[10];
  const int n = SehPlayers(raw, 10);
  for (int i = 0; i < n; ++i) {
    if (raw[i].index != playerIndex) continue;
    const float before = SehAvailableGold(raw[i].player);
    const bool ok = SehGrantGold(raw[i].player, t.startingGold, 10 + kGoldSourceOffset, true, 0, 0);  // Start (10)
    if (ok) g_st.startGold += t.startingGold;
    Log("gold: player %d's starting gold %.0f (source Start) at the deploy: mAvailableGold %.2f -> %.2f%s", playerIndex,
        t.startingGold, before, SehAvailableGold(raw[i].player), ok ? "" : " (grant FAILED)");
  }
}

void PassiveGoldTick(double matchSeconds) {
  if (!g_installed || !g_opt.passiveGold || !Ours()) return;
  const Tuning& t = TuningNow();
  g_passive.delay = t.passiveDelay;
  g_passive.perMinute = t.passiveGold;
  const int due = g_passive.Due(matchSeconds);
  PlayerView raw[10];
  const int n = SehPlayers(raw, 10);
  if (due > 0)
    for (int i = 0; i < n; ++i) {
      if (raw[i].team != 1 && raw[i].team != 2) continue;
      if (SehGrantGold(raw[i].player, static_cast<float>(due), 11 + kGoldSourceOffset, true, 0, 0)) {  // Passive (11)
        ++g_st.passiveGrants;
        g_st.passiveGold += due;
      }
    }
  // the players' gold every 10 match seconds (R12)
  if (g_lastGoldLog < 0.0 || matchSeconds - g_lastGoldLog >= 10.0) {
    g_lastGoldLog = matchSeconds;
    std::string s;
    char b[96];
    for (int i = 0; i < n; ++i) {
      std::snprintf(b, sizeof b, " player %d (team %d) mAvailableGold %.2f;", raw[i].index, raw[i].team,
                    SehAvailableGold(raw[i].player));
      s += b;
    }
    Log("gold: match %.0f s:%s passive paid %.0f per player so far", matchSeconds, s.empty() ? " no players;" : s.c_str(),
        g_st.passiveGold / (n > 0 ? n : 1));
  }
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "deaths %u (damage %u, kill %u; players %u), OnNearbyDeath %u (nearby objects %u), OnKill %u, player events "
                "%u; rewarded deaths %u: xp grants %u (%.0f), gold grants %u (%.0f), D14 reward buffs %u (refused %u); "
                "OnGrantGold calls %u (handlers %u, "
                "%.0f -> %.0f, changed %u); role gold (ROLEVIM) %u grants (%+.0f); start gold %.0f, passive grants %u (%.0f); ends: modifiers %u (item passives kept %u), "
                "abilities interrupted %u (left running, released the agent: %u), deaths done %u, pending %zu; death "
                "timers %u, respawns %u (failed %u, dropped %u, waiting %zu); D1h: "
                "player deaths counted %u (%u before the report), player kills %u (first blood %u), assists %u, creeps "
                "%u, player-kill gold %.0f (killer grants %u, assister grants %u), xp %.0f, recap hits %u, recaps %u; "
                "exceptions %u",
                g_st.deaths, g_st.byDamage, g_st.byKill, g_st.playerDeaths, g_st.nearbyEvents, g_st.nearbyObjects,
                g_st.killEvents, g_st.playerEvents, g_st.rewardDeaths, g_st.xpGrants, g_st.xp, g_st.goldGrants, g_st.gold,
                g_st.rewardBuffs, g_st.rewardBuffsRefused, g_st.goldHookCalls, g_st.goldHandlers, g_st.goldHookIn, g_st.goldHookOut, g_st.goldHandlerChanges,
                g_st.roleGoldGrants, g_st.roleGold, g_st.startGold, g_st.passiveGrants,
                g_st.passiveGold, g_st.modifiersEnded, g_st.itemPassivesKept, g_st.abilitiesInterrupted,
                g_st.abilitiesKeptReleased, g_st.pendingDone, g_pending.size(),
                g_st.deathTimers, g_st.respawns, g_st.respawnsFailed, g_st.respawnsDropped, g_respawns.size(),
                g_st.playerDeathsCounted, g_st.killReports, g_st.playerKills, g_st.firstBlood, g_st.assists, g_st.creeps,
                g_st.playerKillGold, g_st.playerKillGrants, g_st.assisterGrants, g_st.playerKillXp, g_st.recapHits,
                g_st.recaps, g_st.exceptions);
}

}  // namespace death
