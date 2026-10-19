// gms/core/damage (D1h): the engine-free rules of player kill bookkeeping (docs/DAMAGE.md *D1h*). Header-only, no game
// addresses, like rules.h: the harness (gms/engine/death.cpp, damage.cpp) reads and writes the client's fields and
// calls its grant functions; the rules the client lost (with the stripped server shell) are here, where gms_tests pins
// them down:
//   - the assist matrix the client still fills (sim +0xCD84: AssistTracker_RecordHit 0x008E1140 for player-on-player
//     hits, AssistTracker_HandleBuffEvent 0x008E0A70 for buffs and debuffs; u64 server ms at [attacker * 10 + target])
//     and who counts as an assister at a death;
//   - the kill, death and assist counters, kill streaks and multi-kills per player (cLabsPlayer +0x240..+0x260; the
//     combat event's flags 0x2000/0x4000/0x8000 read two of them, Damage_ReportHpChange 0x008DFB10) and first blood;
//   - a player kill's gold and XP from RewardsTuning (the Player CharacterRewardData is 0/0: "area" XP, "last player to
//     hit and assist" gold) and RolesTuning's per-role modifiers.
// [assumed] marks the rules no client code shows (DAMAGE.md *D1h*, *Open*).
#pragma once

#include <algorithm>
#include <cstdint>
#include <vector>

#include "core/damage/rules.h"

namespace gms::damage {

constexpr int kMaxPlayers = 10;  // AssistTracker_RecordHit: player indices < 10

// nCombatEventFlags (GlobalDefinitions.lua) of a player's killing blow. The report sets 0x2000, 0x4000, 0x8000 and
// 0x80000 itself from the fields the host writes first; FirstKill only comes in through its extra-flags argument.
constexpr unsigned kEventLastPlayerMultiKill = 0x2000;
constexpr unsigned kEventLastPlayerKillStreak = 0x4000;
constexpr unsigned kEventTargetKillStreakEnd = 0x8000;
constexpr unsigned kEventPlayerTeamDead = 0x80000;
constexpr unsigned kEventFirstKill = 0x400000;

// ---- the assist matrix ------------------------------------------------------------------------------------------

constexpr unsigned kAssistMatrixBytes = kMaxPlayers * kMaxPlayers * 8;  // 0x320: u64 ms [attacker * 10 + target]
// How long a hit (or a buff on a teammate who hit) keeps its assist credit [assumed: 10 s, the window
// AssistTracker_HandleBuffEvent applies when it passes a buffed teammate's credit on to the buffer].
constexpr std::uint64_t kAssistWindowMs = 10000;

inline std::uint64_t AssistTime(const std::uint64_t* matrix, int attacker, int target) {
  if (!matrix || attacker < 0 || attacker >= kMaxPlayers || target < 0 || target >= kMaxPlayers) return 0;
  return matrix[attacker * kMaxPlayers + target];
}

// The assisters of `victim`'s death: every other player of another team (team 1 or 2, not the victim's) whose credit on
// the victim is at most `windowMs` old, without the killer. `team[i]` is player i's team (0 = no such player).
inline std::vector<int> Assisters(const std::uint64_t* matrix, int victim, int killer, const int* team, std::uint64_t nowMs,
                                  std::uint64_t windowMs = kAssistWindowMs) {
  std::vector<int> out;
  if (!matrix || !team || victim < 0 || victim >= kMaxPlayers) return out;
  for (int p = 0; p < kMaxPlayers; ++p) {
    if (p == victim || p == killer) continue;
    if (team[p] != 1 && team[p] != 2) continue;
    if (team[p] == team[victim]) continue;
    const std::uint64_t t = AssistTime(matrix, p, victim);
    if (!t || t > nowMs + 1000 || nowMs - std::min(t, nowMs) > windowMs) continue;
    out.push_back(p);
  }
  return out;
}

// After a death the credits on the victim are spent [assumed]: its column is cleared, so the next death needs new hits.
inline void ClearAssists(std::uint64_t* matrix, int victim) {
  if (!matrix || victim < 0 || victim >= kMaxPlayers) return;
  for (int p = 0; p < kMaxPlayers; ++p) matrix[p * kMaxPlayers + victim] = 0;
}

// ---- the counters ---------------------------------------------------------------------------------------------------

struct Tally {
  unsigned kills = 0, deaths = 0, assists = 0, creeps = 0;
  unsigned killStreak = 0;   // kills since the last death (cLabsPlayer +0x260, read by the report)
  unsigned deathStreak = 0;  // deaths since the last kill [assumed: kills only end it]
  unsigned bestStreak = 0;
  unsigned multiKill = 0;    // kills in the current multi-kill sequence (cLabsPlayer +0x250, read by the report)
  std::uint64_t lastKillMs = 0;
  unsigned multiKills[6] = {0, 0, 0, 0, 0, 0};  // [n]: sequences whose largest size was n (2 double ... 5 omni)
};

struct KillOutcome {
  int victim = -1;
  int killer = -1;  // the credited player (the victim's last player to hit), -1 none
  std::vector<int> assisters;
  unsigned victimKillStreak = 0, victimDeathStreak = 0;  // before this death
  unsigned killerPrevStreak = 0, killerStreak = 0, multiKill = 0;
  bool firstBlood = false;
  unsigned extraFlags = 0;  // for Damage_ReportHpChange: FirstKill
};

class KillBook {
 public:
  Tally t[kMaxPlayers];
  bool firstBloodDone = false;

  bool Valid(int p) const { return p >= 0 && p < kMaxPlayers; }

  // A player's death [assumed rules where marked]:
  //   victim: deaths + 1, its kill streak (remembered for the bounty and TargetKillStreakEnd) and its multi-kill
  //     sequence end, death streak + 1;
  //   killer (another player): kills + 1, kill streak + 1 (best kept), death streak 0, multi-kill + 1 when its last
  //     kill is at most multiKillTimeoutMs old, else 1 (BattlegroundsGameplayTuning.multiKillTimeoutMs 10,000); the
  //     sequence is counted once, at its largest size (a triple kill is a triple, not also a double); the match's first
  //     player kill is first blood (CombatEvent FirstKill 0x400000);
  //   assisters: assists + 1.
  KillOutcome PlayerDeath(int victim, int killer, const std::vector<int>& assisters, std::uint64_t nowMs,
                          std::uint64_t multiKillTimeoutMs) {
    KillOutcome o;
    o.victim = victim;
    if (Valid(victim)) {
      Tally& v = t[victim];
      o.victimKillStreak = v.killStreak;
      o.victimDeathStreak = v.deathStreak;
      ++v.deaths;
      v.killStreak = 0;
      ++v.deathStreak;
      v.multiKill = 0;
      v.lastKillMs = 0;
    }
    if (Valid(killer) && killer != victim) {
      Tally& k = t[killer];
      o.killer = killer;
      o.killerPrevStreak = k.killStreak;
      ++k.kills;
      ++k.killStreak;
      k.bestStreak = std::max(k.bestStreak, k.killStreak);
      k.deathStreak = 0;
      const bool chained = k.multiKill > 0 && k.lastKillMs && nowMs >= k.lastKillMs &&
                           nowMs - k.lastKillMs <= multiKillTimeoutMs;
      k.multiKill = chained ? k.multiKill + 1 : 1;
      k.lastKillMs = nowMs;
      if (k.multiKill >= 2 && k.multiKill <= 5) {
        ++k.multiKills[k.multiKill];
        if (k.multiKill > 2) --k.multiKills[k.multiKill - 1];
      }
      o.killerStreak = k.killStreak;
      o.multiKill = k.multiKill;
      if (!firstBloodDone) {
        firstBloodDone = true;
        o.firstBlood = true;
        o.extraFlags |= kEventFirstKill;
      }
    }
    for (int a : assisters) {
      if (!Valid(a) || a == victim || a == o.killer) continue;
      bool dup = false;
      for (int b : o.assisters) dup = dup || b == a;
      if (dup) continue;
      ++t[a].assists;
      o.assisters.push_back(a);
    }
    return o;
  }

  // A last hit on a minion or jungle creep [assumed rule: NPC types 0-3, the credited player].
  void Creep(int player, unsigned n = 1) {
    if (Valid(player)) t[player].creeps += n;
  }
};

// Whether a victim of this NPC type adds to the last hitter's creep score [assumed: MeleeMinion 0, RangedMinion 1,
// SuperMinion 2, Creep 3; not workers (Follower 12), structures, objectives, players].
inline bool CountsAsCreep(int npcType) { return npcType >= 0 && npcType <= 3; }

// ---- a player kill's gold and XP [assumed formulas; every amount from RewardsTuning / RolesTuning / LevelUpTuning] ----

struct FloatArray {
  const float* p = nullptr;
  int n = 0;
  // Entry `index`, clamped to the last; `none` for an empty array.
  float At(int index, float none) const {
    if (!p || n <= 0) return none;
    if (index < 0) index = 0;
    return p[std::min(index, n - 1)];
  }
};

struct PlayerKillTuning {
  FloatArray killStreakGold;   // playerKillStreakGold [300, 350, 400, 450, 500]: by the victim's kill streak
  FloatArray deathStreakGold;  // playerDeathStreakGold [300, 270, 189, ...]: by the victim's death streak
  FloatArray lowerLevelGold, higherLevelGold;  // playerLower/HigherLevelGoldMultiplier [1.0]
  FloatArray lowerLevelXp, higherLevelXp;      // playerLower/HigherLevelXPMultiplier [1, 0.9, ...] / [1, 1.1, ...]
  FloatArray xpNearbyShare;    // playerXPRewardNearbyShare [1, 0.65, 0.4333, 0.325, 0.26]
  FloatArray assistShare;      // playerRewardAssistShare [0.7]
  float firstBloodGold = 100.f;   // firstBloodGoldReward
  float killXpPercent = 0.6f;     // playerKillXPPercent
};

// The victim's bounty: with a kill streak, playerKillStreakGold[streak]; without, playerDeathStreakGold[death streak]
// (both arrays start at 300, so a victim with neither is worth 300).
inline float PlayerKillBaseGold(const PlayerKillTuning& t, unsigned victimKillStreak, unsigned victimDeathStreak) {
  if (victimKillStreak > 0) return t.killStreakGold.At(static_cast<int>(victimKillStreak), 300.f);
  return t.deathStreakGold.At(static_cast<int>(victimDeathStreak), 300.f);
}

// The level-difference multiplier for a receiver: a lower-level victim (diff = receiver level - victim level > 0)
// takes lower[diff], a higher-level one higher[-diff], an equal one lower[0].
inline float LevelMultiplier(const FloatArray& lower, const FloatArray& higher, int receiverLevel, int victimLevel) {
  const int diff = receiverLevel - victimLevel;
  if (diff >= 0) return lower.At(diff, 1.f);
  return higher.At(-diff, 1.f);
}

// The XP of the victim's current level step, LevelUpTuning.Level[level + 1].xpRequired - Level[level].xpRequired (the
// last step at the maximum level). `xpRequired[i]` is Level[i] (index 1 = level 1, 0 XP; index 2 = 260).
inline float LevelStepXp(const float* xpRequired, int n, int level) {
  if (!xpRequired || n < 2) return 0.f;
  int l = std::max(level, 1);
  if (l + 1 >= n) l = n - 2;
  const float s = xpRequired[l + 1] - xpRequired[l];
  return s > 0.f ? s : 0.f;
}

struct KillReceiver {
  int player = -1;
  int level = 1;
  bool inArea = false;          // alive within radiusForPlayerKillRewards (48.53) of the victim
  float killGoldModifier = 1.f;    // RolesTuning role[mRole].enemyPlayerKillGoldModifier
  float assistGoldModifier = 1.f;  // .enemyPlayerAssistGoldModifier
  float xpModifier = 1.f;          // .enemyPlayerKillOrAssistXPModifier
};

struct PlayerKillRewards {
  float baseGold = 0.f;  // the bounty before modifiers
  float xpBase = 0.f;    // killXpPercent x the victim's level step
  std::vector<Grant> gold;    // the killer's (lastHit) and the assisters'
  std::vector<Grant> xp;      // the area's, the killer's and the assisters'
  float firstBloodGold = 0.f; // to the killer, on top
};

// A player kill [assumed rules]:
//   gold ("last player to hit and assist"): the killer gets the bounty x its level multiplier x its role's kill-gold
//     modifier, plus firstBloodGoldReward for the match's first kill; the n assisters share bounty x
//     playerRewardAssistShare[n - 1] (the last entry beyond the array), each x its level multiplier and role's
//     assist-gold modifier; playerGoldRewardNearbyShare is empty in the data, so nobody else gets gold;
//   XP ("area"): killXpPercent x the victim's level step, shared by the killer's team's players in the area plus the
//     killer and the assisters (playerXPRewardNearbyShare[n - 1] each), each x its level multiplier and role's XP
//     modifier.
// ROLEVIM-2: a role's kill or assist gold modifier above 1 (the Predator's 1.5 and 2.0) is its own grant right after
// the normal one (PushWithRole: 300, then 150 for a level-even Predator's kill). The XP modifier stays inside the one
// XP grant [assumed: no XP popup to split], since the client's own OnGrantXP dispatch would run on both parts.
// `receivers` are the killer's team's players (the killer and the assisters among them).
inline PlayerKillRewards ComputePlayerKillRewards(const PlayerKillTuning& t, const KillOutcome& o, int victimLevel,
                                                  float victimStepXp, const std::vector<KillReceiver>& receivers) {
  PlayerKillRewards r;
  if (o.killer < 0) return r;
  r.baseGold = PlayerKillBaseGold(t, o.victimKillStreak, o.victimDeathStreak);
  r.xpBase = t.killXpPercent * victimStepXp;
  auto isAssister = [&o](int p) {
    for (int a : o.assisters)
      if (a == p) return true;
    return false;
  };
  const int nAssist = static_cast<int>(o.assisters.size());
  const float assistTotal = nAssist > 0 ? r.baseGold * t.assistShare.At(nAssist - 1, 0.f) : 0.f;
  std::vector<const KillReceiver*> xpTakers;
  for (const KillReceiver& k : receivers) {
    if (k.player < 0) continue;
    const bool killer = k.player == o.killer, assister = isAssister(k.player);
    if (killer) {
      PushWithRole(&r.gold, k.player,
                   r.baseGold * LevelMultiplier(t.lowerLevelGold, t.higherLevelGold, k.level, victimLevel),
                   k.killGoldModifier, true);
    } else if (assister && assistTotal > 0.f) {
      PushWithRole(&r.gold, k.player,
                   assistTotal / static_cast<float>(nAssist) *
                       LevelMultiplier(t.lowerLevelGold, t.higherLevelGold, k.level, victimLevel),
                   k.assistGoldModifier, false);
    }
    if (killer || assister || k.inArea) xpTakers.push_back(&k);
  }
  if (o.firstBlood) r.firstBloodGold = t.firstBloodGold;
  if (r.xpBase > 0.f && !xpTakers.empty()) {
    const float share = t.xpNearbyShare.At(static_cast<int>(xpTakers.size()) - 1, 1.f);
    for (const KillReceiver* k : xpTakers) {
      Grant g;
      g.player = k->player;
      g.amount = r.xpBase * share * LevelMultiplier(t.lowerLevelXp, t.higherLevelXp, k->level, victimLevel) *
                 k->xpModifier;
      g.lastHit = k->player == o.killer;
      if (g.amount > 0.f) r.xp.push_back(g);
    }
  }
  return r;
}

}  // namespace gms::damage
