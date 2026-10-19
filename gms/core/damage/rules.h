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
  bool role = false;  // ROLEVIM-2: a role's part, granted as its own event right after the player's normal grant
};

// ROLEVIM-2: a normal grant and a role's part of it as two grants (two gold events in the client, as the user decided
// on 2026-10-18): `base` to `player`, then base x (factor - 1) as a separate role grant when the factor is above 1. A
// factor below 1 (none in the data) scales the normal grant instead, since a grant can't be negative.
inline void PushWithRole(std::vector<Grant>* out, int player, float base, float factor, bool lastHit) {
  if (player < 0 || base <= 0.f) return;
  Grant g;
  g.player = player;
  g.amount = factor < 1.f ? base * factor : base;
  g.lastHit = lastHit;
  if (g.amount > 0.f) out->push_back(g);
  if (factor > 1.f) {
    g.amount = base * (factor - 1.f);
    g.role = true;
    out->push_back(g);
  }
}

// VIMPOPUP: one LabsPlayer_GrantGold call per player and kill, the role's part passed as the grant's bonus.
// LabsPlayer_GrantGold (0x00912790) takes (amount, bonus) as two gold sources (the second is source 0x13 =
// nGoldSourceType.Role 12 + 7: the role's gold),
// LabsPlayer_AddGold (0x00912440) adds both in one gold CombatEvent: integerGoldChange = the total,
// integerBonusGoldChange = the bonus (plus whatever an OnGrantGold handler added). The client's own
// CombatEvents!CombatEvent_Gold.lua then shows two popups, combattext_gold_bonus with the bonus and combattext_gold
// with the rest. Two separate grants make two combattext_gold popups of the same def on the same object in the same
// frame, and the user's client showed only one of them (the Tactician's 4 + 4, VIMPOPUP).
struct GoldEvent {
  int player = -1;
  float amount = 0.f;  // the normal grant
  float bonus = 0.f;   // the role's part (0: none)
  bool lastHit = false;
};

// A role grant right after its player's normal grant (PushWithRole's order) becomes that grant's bonus; any other
// grant is its own event (a role grant without its normal grant keeps its amount as the normal part, bonus 0).
inline std::vector<GoldEvent> GoldEvents(const std::vector<Grant>& grants) {
  std::vector<GoldEvent> out;
  for (const Grant& g : grants) {
    if (g.role && !out.empty() && out.back().player == g.player && out.back().bonus == 0.f) {
      out.back().bonus = g.amount;
      continue;
    }
    GoldEvent e;
    e.player = g.player;
    e.amount = g.amount;
    e.lastHit = g.lastHit;
    out.push_back(e);
  }
  return out;
}

// What the client's popups show for one grant (LabsPlayer_AddGold + CombatEvent_Gold.lua): `granted` is the total
// after the OnGrantGold handlers (amount + bonus without one). The handlers' change is added to the bonus; a bonus
// equal to the whole total is sent as 0; under 1 vim no event at all (hidden). Both numbers are floored
// (CombatEvent.cpp's setters). bonusShown 0 = no combattext_gold_bonus popup.
struct GoldPopups {
  bool event = false;
  int bonusShown = 0;  // combattext_gold_bonus
  int baseShown = 0;   // combattext_gold: integerGoldChange - integerBonusGoldChange
};
inline GoldPopups PopupsOf(float amount, float bonus, float granted) {
  GoldPopups p;
  if (!(granted >= 1.f)) return p;
  p.event = true;
  float b = bonus + (granted - (amount + bonus));
  if (b == granted || b < 0.f) b = 0.f;
  const int total = static_cast<int>(std::floor(granted));
  p.bonusShown = static_cast<int>(std::floor(b));
  p.baseShown = total - p.bonusShown;
  return p;
}

// ROLEVIM: a role's NPC-kill gold modifiers, RolesTuning role[] +0x08..+0x14 (the data: Gladiator/Carry
// lastHitMinion 2.0, Tactician/Support proximity 2.0, every other value 1.0). Nothing in the client applies them (its
// readers fill only the role card's percentages); the stripped server's reward code did.
struct RoleNpcGold {
  float lastHitMinion = 1.f;         // lastHitMinionGoldModifier: "the vim gained for scoring the killing blow on lane minions"
  float lastHitCreature = 1.f;       // lastHitCreatureGoldModifier: the same for a jungle creature
  float proximity = 1.f;             // proximityToMinionOrCreatureDeathGoldModifier: "being near lane minions when they die"
  float proximityAllyLastHit = 1.f;  // proximityToAllyLastHitMinionOrCreatureGoldModifier
};
// The victim kinds the modifiers distinguish: a lane minion (NPC types 0-2), a jungle creature (3), anything else
// (workers, objectives, structures, players: no role modifier here; the Predator's worker bonus is its script's).
enum NpcGoldKind { kNpcOther = 0, kNpcMinion = 1, kNpcCreature = 2 };

// Who gets how much of one reward (XP or gold) [assumed rules, DAMAGE.md *Death handling* 5]:
//   last hit (and assist): the last hitter gets `amount` (assists aren't tracked yet);
//   area: everyone in `area` gets amount x NearbyShare(n);
//   area and last hit: the last hitter gets amount x lastHitBonusRatio (RewardsTuning.playerMinionFatalHitGoldBonusRatio
//     3.0 / playerJungleFatalHitGoldBonusRatio 1.0) and no area share; everyone else in `area` gets amount x
//     NearbyShare(n), n counting the whole area (the user's rule of 2026-10-18, ROLEVIM-2: the share compensates those
//     who didn't get the last hit; a melee minion pays its last hitter 12, each other player nearby 4);
//   team: everyone in `team` gets `amount`;
//   the titan types: nothing (no player).
// `lastHitter` is a gameplay index or -1. Zero amounts are dropped.
// ROLEVIM (gold only; `roles` indexed by gameplay index, `roleCount` entries, nullptr = every factor 1): for a minion or
// a creature (`kind`), the last-hit part (the whole amount for the last-hit types, amount x lastHitBonusRatio for
// "area and last hit") x the last hitter's lastHitMinion / lastHitCreature, and each area share x the receiver's
// proximity, and x its proximityAllyLastHit when another player of its team got the last hit [assumed: the factors
// multiply; the data's ally factor is 1.0 everywhere]. Each role part is its own grant (PushWithRole, ROLEVIM-2): a
// Gladiator's last hit on a melee minion (gold 4) is 12 + 12 (role), a Tactician's share 4 + 4 (role), its own last
// hit 12.
inline std::vector<Grant> SplitReward(int type, float amount, const std::vector<int>& area, int lastHitter,
                                      const std::vector<int>& team, const float* shares, int shareCount,
                                      float lastHitBonusRatio, NpcGoldKind kind = kNpcOther,
                                      const RoleNpcGold* roles = nullptr, int roleCount = 0) {
  std::vector<Grant> out;
  if (amount <= 0.f) return out;
  auto role = [&](int player) -> const RoleNpcGold* {
    return roles && kind != kNpcOther && player >= 0 && player < roleCount ? &roles[player] : nullptr;
  };
  auto lastHitFactor = [&]() {
    const RoleNpcGold* r = role(lastHitter);
    return !r ? 1.f : kind == kNpcMinion ? r->lastHitMinion : r->lastHitCreature;
  };
  auto areaFactor = [&](int player) {
    const RoleNpcGold* r = role(player);
    if (!r) return 1.f;
    return r->proximity * (lastHitter >= 0 && lastHitter != player ? r->proximityAllyLastHit : 1.f);
  };
  switch (type) {
    case kRewardLastHit:
    case kRewardLastHitAndAssist:
      PushWithRole(&out, lastHitter, amount, lastHitFactor(), true);
      break;
    case kRewardArea:
    case kRewardAreaAndLastHit: {
      const bool lastHitPart = type == kRewardAreaAndLastHit;
      const float share = NearbyShare(shares, shareCount, static_cast<int>(area.size()));
      for (int p : area)
        if (!lastHitPart || p != lastHitter) PushWithRole(&out, p, amount * share, areaFactor(p), false);
      if (lastHitPart) PushWithRole(&out, lastHitter, amount * lastHitBonusRatio, lastHitFactor(), true);
      break;
    }
    case kRewardTeam:
      for (int p : team) PushWithRole(&out, p, amount, 1.f, false);
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
