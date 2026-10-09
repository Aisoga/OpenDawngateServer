// gms/core/damage (D1f-live): the engine-free rules of the damage core, death rewards and gold (docs/DAMAGE.md,
// docs/MATCH_FLOW.md *Game-mode handlers, energy lines and spirit wells (R10)* §5). Header-only, no game addresses: the
// harness (gms/engine/damage.cpp, death.cpp) calls the client's own functions for everything the client still has, and
// takes from here only what the client lost or what a test must pin down:
//   - the descriptor bits the pipeline tests (nDescriptors, GlobalDefinitions.lua) and the death descriptors it writes;
//   - the pseudo-random crit distribution (Random_RollPseudoRandomDistribution 0x008DF9B0: the polynomial C(P), the
//     counter rule), for gms_tests T9; the host itself calls the client's roll;
//   - the defense multiplier 100 / (100 + d), 2 - 100 / (100 - d) below 0 (Damage_GetPhysicalMultiplier 0x008DFEB0);
//   - the reward split by CharacterRewardData's reward type (enum 0x0142cb34) and RewardsTuning's nearby shares;
//   - passive gold: whole units, the fraction carried (RewardsTuning passiveGold per minute from passiveStartDelay);
//   - D1g: a player's death timer (LevelGameplayTuning deathTimeBase/PerLevel/PerMinute) and its respawn time.
// [assumed] marks the rules no client code shows (DAMAGE.md *Open*).
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <vector>

namespace gms::damage {

// nDescriptors (the 64-bit hit descriptors; every bit the pipeline tests is in the low word).
constexpr unsigned kIsMelee = 0x1;
constexpr unsigned kIsBasic = 0x2;
constexpr unsigned kIsDoT = 0x4;
constexpr unsigned kIsAoE = 0x8;
constexpr unsigned kIsPhysical = 0x40;
constexpr unsigned kIsMagical = 0x80;
constexpr unsigned kCanNotCrit = 0x100;
constexpr unsigned kIsProjectile = 0x400;
constexpr unsigned kIsTrueDamage = 0x800;
constexpr unsigned kIsItem = 0x10000;
constexpr unsigned kIgnoreBasicAttackScaling = 0x20000;
constexpr unsigned kNoDamageCallbacks = 0x80000;  // DoesNotGenerateDamageCallbacks
constexpr unsigned kIsSpellOverload = 0x100000;
constexpr unsigned kIsSelfInflicted = 0x400000;

// nHitDescriptors, the death descriptors at combatant +0x28 (Behavior_Death reads Critical and Knockback).
constexpr unsigned kHitCritical = 0x1;
constexpr unsigned kHitAoE = 0x100;
constexpr unsigned kHitDoT = 0x200;
constexpr unsigned kHitKnockback = 0x400;

// Step 3: crits only on basic attacks that may crit, not on an overload, and only with a snapshot (the roll needs it).
inline bool MayCrit(unsigned descLo, bool snapshot) {
  return snapshot && (descLo & kIsBasic) && !(descLo & kCanNotCrit) && !(descLo & kIsSpellOverload);
}
// Step 14: the heal core's exact test (0x008E0720): no overload of an overload, a basic, an item or true damage.
inline bool MayOverload(unsigned descLo, bool snapshot) {
  return snapshot && !(descLo & (kIsSpellOverload | kIsBasic | kIsItem | kIsTrueDamage));
}
// Step 9: the death descriptors of the killing hit [assumed mapping: Critical from the crit, AoE and DoT from the hit].
inline unsigned DeathDescriptors(bool crit, unsigned descLo) {
  return (crit ? kHitCritical : 0u) | ((descLo & kIsAoE) ? kHitAoE : 0u) | ((descLo & kIsDoT) ? kHitDoT : 0u);
}
// Step 9: the report's integerHpChange, (int)oldHP - (int)newHP (CombatEvent_SetIntegerHpChange 0x008DF220).
inline int IntegerHpChange(float oldHp, float newHp) { return static_cast<int>(oldHp) - static_cast<int>(newHp); }

// 100 / (100 + defense); below 0 the client's 2 - 100 / (100 - defense) (Damage_GetPhysicalMultiplier 0x008DFEB0).
inline float DefenseMultiplier(float defense) {
  if (defense < 0.f) return 2.f - 100.f / (100.f - defense);
  return 100.f / (100.f + defense);
}

// The PRD constant C(P) of Random_RollPseudoRandomDistribution (0x008DF9B0): x = P * 100; an 8th-degree polynomial
// above 5 %, a quadratic up to 5 %, 0 at 0 (constants 0x0124B5A8-0x0124B5D4). Warcraft III's table: C(20 %) 0.0557.
inline float PrdConstant(float p) {
  const double x = static_cast<double>(p * 100.f);  // in float, as the client's MULSS (0.05 -> exactly 5: the quadratic)
  if (x <= 0.0) return 0.f;
  if (x <= 5.0) return static_cast<float>(0.00014803999511059374 * x * x + 2.282470086356625e-05 * x - 1.411379980709171e-05);
  static const double c[9] = {0.0021423399448394775,  -0.001787330023944378,  0.0005539740086533129,
                              -3.77471988031175e-05,  1.7057300283340737e-06, -4.246010121278232e-08,
                              5.783180045959568e-10,  -4.020160072892853e-12, 1.1111600124714092e-14};
  double sum = 0.0, pw = 1.0;
  for (double ci : c) {
    sum += ci * pw;
    pw *= x;
  }
  return static_cast<float>(sum);
}
// One roll: a hit when counter * C > r (r uniform in [0, 1)); a hit resets the counter to 1, a miss adds 1.
inline bool PrdRoll(float c, int* counter, float r) {
  const bool hit = static_cast<float>(*counter) * c > r;
  *counter = hit ? 1 : *counter + 1;
  return hit;
}

// CharacterRewardData.xpRewardType / goldRewardType (enum 0x0142cb34).
enum RewardType {
  kRewardLastHit = 0,
  kRewardLastHitAndAssist = 1,
  kRewardArea = 2,
  kRewardAreaAndLastHit = 3,
  kRewardTeam = 4,
  kRewardFriendlyTitans = 5,
  kRewardEnemyTitans = 6,
};

// RewardsTuning's nearby-share arrays: entry n - 1 for n players nearby (the last entry beyond the array); 1 without an
// array (playerGoldRewardNearbyShare is empty) [assumed].
inline float NearbyShare(const float* shares, int count, int n) {
  if (n <= 0) return 0.f;
  if (!shares || count <= 0) return 1.f;
  return shares[std::min(n, count) - 1];
}

struct Grant {
  int player = -1;    // the gameplay index
  float amount = 0.f;
  bool lastHit = false;
};

// Who gets how much of one reward (XP or gold) [assumed rules, DAMAGE.md *Death handling* 5]:
//   last hit (and assist): the last hitter gets `amount` (assists aren't tracked yet);
//   area: everyone in `area` gets amount x NearbyShare(n);
//   area and last hit: the same, and the last hitter gets amount x lastHitBonusRatio on top
//     (RewardsTuning.playerMinionFatalHitGoldBonusRatio 3.0 / playerJungleFatalHitGoldBonusRatio 1.0);
//   team: everyone in `team` gets `amount`;
//   the titan types: nothing (no player).
// `lastHitter` is a gameplay index or -1. Zero amounts are dropped.
inline std::vector<Grant> SplitReward(int type, float amount, const std::vector<int>& area, int lastHitter,
                                      const std::vector<int>& team, const float* shares, int shareCount,
                                      float lastHitBonusRatio) {
  std::vector<Grant> out;
  if (amount <= 0.f) return out;
  auto add = [&out](int player, float a, bool lastHit) {
    if (player < 0 || a <= 0.f) return;
    for (Grant& g : out)
      if (g.player == player) {
        g.amount += a;
        g.lastHit = g.lastHit || lastHit;
        return;
      }
    Grant g;
    g.player = player;
    g.amount = a;
    g.lastHit = lastHit;
    out.push_back(g);
  };
  switch (type) {
    case kRewardLastHit:
    case kRewardLastHitAndAssist:
      add(lastHitter, amount, true);
      break;
    case kRewardArea:
    case kRewardAreaAndLastHit: {
      const float share = NearbyShare(shares, shareCount, static_cast<int>(area.size()));
      for (int p : area) add(p, amount * share, false);
      if (type == kRewardAreaAndLastHit) add(lastHitter, amount * lastHitBonusRatio, true);
      break;
    }
    case kRewardTeam:
      for (int p : team) add(p, amount, false);
      break;
    default:
      break;
  }
  return out;
}

// D14: who gets one of CharacterRewardData.buffs (cCharacterRewardBuffData {buffType (enum 0x0142cb34), buff Key}; the
// three jungle majors' buffs are "last player to hit", the parasite's stage 2/3 buffs "team"). Nothing in the client
// reads the list (the stripped reward code granted them; MATCH_FLOW.md *D14*). The modifier goes onto each receiver's
// creature; a receiver must be alive [assumed: every such buff ends OnAgentDeath, so a corpse never keeps one]:
//   last player to hit: the last hitter;
//   last player to hit and assist: the last hitter [assumed: assists aren't tracked for NPC kills, as SplitReward];
//   area / area and last hit: everyone in `area` (and the last hitter);
//   team: everyone in `team`;
//   the titan types: nobody.
// `alive` lists the gameplay indices whose creature is alive; the result keeps the first-seen order, no duplicates.
inline std::vector<int> BuffReceivers(int type, const std::vector<int>& area, int lastHitter, const std::vector<int>& team,
                                      const std::vector<int>& alive) {
  std::vector<int> out;
  auto add = [&](int p) {
    if (p < 0 || std::find(alive.begin(), alive.end(), p) == alive.end()) return;
    if (std::find(out.begin(), out.end(), p) == out.end()) out.push_back(p);
  };
  switch (type) {
    case kRewardLastHit:
    case kRewardLastHitAndAssist:
      add(lastHitter);
      break;
    case kRewardArea:
    case kRewardAreaAndLastHit:
      for (int p : area) add(p);
      if (type == kRewardAreaAndLastHit) add(lastHitter);
      break;
    case kRewardTeam:
      for (int p : team) add(p);
      break;
    default:
      break;
  }
  return out;
}

// Passive gold [assumed rate: per minute, paid out every second]: from `delay` match seconds on, `perMinute` / 60 per
// second, granted in whole units with the fraction carried, so 60 s pay exactly `perMinute`.
struct PassiveGold {
  float delay = 60.f;
  float perMinute = 100.f;
  double paidUpTo = -1.0;  // match seconds already paid for
  double carry = 0.0;

  // Whole gold due for (paidUpTo, t]; the first call only sets the start.
  int Due(double t) {
    if (paidUpTo < 0.0) {
      paidUpTo = t;
      return 0;
    }
    const double from = std::max(paidUpTo, static_cast<double>(delay));
    const double to = std::max(t, static_cast<double>(delay));
    paidUpTo = std::max(paidUpTo, t);
    if (to <= from) return 0;
    carry += (to - from) * perMinute / 60.0;
    const double whole = std::floor(carry + 1e-6);
    carry -= whole;
    if (carry < 0.0) carry = 0.0;
    return static_cast<int>(whole);
  }
};

// D1g: a dead player's wait in seconds, LevelGameplayTuning deathTimeBase (+0x00, 15) + deathTimePerLevel (+0x04, 0.5) x
// level + deathTimePerMinute (+0x08, 1.25) x match minutes (MATCH_FLOW.md §2.10). No client code computes it (the names
// exist only in the reflection tables) [assumed: the level as it is (1 at the start, not level - 1), the minutes with
// their fraction, the match clock at the death (0 before the match starts)].
inline float DeathSeconds(float base, float perLevel, float perMinute, unsigned level, double matchSeconds) {
  const double minutes = matchSeconds > 0.0 ? matchSeconds / 60.0 : 0.0;
  const double s = static_cast<double>(base) + static_cast<double>(perLevel) * level + perMinute * minutes;
  return s > 0.0 ? static_cast<float>(s) : 0.f;
}
// The respawn time on the server clock: labsPlayerGlobalData.mRespawnTime = mDeathTime (the server clock at the death) +
// the wait in whole ms (the client's reader 0x008B43E0 translates both to its own clock; its panels show
// (now - death) / (respawn - death) and respawn - now, 0x005326F0).
inline std::uint64_t RespawnTimeMs(std::uint64_t deathMs, float seconds) {
  if (!(seconds > 0.f)) return deathMs;
  return deathMs + static_cast<std::uint64_t>(std::llround(static_cast<double>(seconds) * 1000.0));
}

}  // namespace gms::damage
