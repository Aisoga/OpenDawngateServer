// gms/core/damage (D1f-live): the damage core's engine-free rules (docs/DAMAGE.md; T9 is the PRD case).
#include <cmath>
#include <cstdint>
#include <vector>

#include "core/damage/regen.h"
#include "core/damage/rules.h"
#include "core/damage/upkeep.h"
#include "tests/check.h"

using namespace gms::damage;

namespace {
bool Near(double a, double b, double eps) { return std::fabs(a - b) <= eps; }

// xorshift32 in [0, 1): deterministic, enough for the hit-rate check
struct Rng {
  std::uint32_t s = 0x9E3779B9u;
  float Next() {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return static_cast<float>(s >> 8) / 16777216.f;
  }
};
}  // namespace

// DAMAGE.md T9: C(P) from 0x008DF9B0's polynomial, and the hit rate over 10,000 rolls.
GMS_TEST(DamagePrdConstant) {
  CHECK(Near(PrdConstant(0.05f), 0.0038, 0.00005));
  CHECK(Near(PrdConstant(0.2f), 0.0552, 0.0001));
  CHECK(Near(PrdConstant(0.5f), 0.3009, 0.0002));
  CHECK_EQ(PrdConstant(0.f), 0.f);
  CHECK(PrdConstant(0.03f) > 0.f);  // the quadratic branch (x <= 5)
  CHECK(Near(PrdConstant(1.f), 1.0, 0.01));
}

GMS_TEST(DamagePrdHitRate) {
  for (float p : {0.2f, 0.5f}) {
    const float c = PrdConstant(p);
    Rng rng;
    int counter = 1, hits = 0;
    const int n = 10000;
    for (int i = 0; i < n; ++i)
      if (PrdRoll(c, &counter, rng.Next())) ++hits;
    const double rate = static_cast<double>(hits) / n;
    CHECK(Near(rate, p, 0.01));
  }
  int counter = 3;
  CHECK(PrdRoll(0.5f, &counter, 0.9f));  // 3 x 0.5 > 0.9: hit, reset
  CHECK_EQ(counter, 1);
  CHECK(!PrdRoll(0.5f, &counter, 0.9f));  // 1 x 0.5 <= 0.9: miss, +1
  CHECK_EQ(counter, 2);
}

// DAMAGE.md T4-T6's expected multipliers.
GMS_TEST(DamageDefenseMultiplier) {
  CHECK(Near(100.f * DefenseMultiplier(50.f), 66.67, 0.01));    // T4: armor 50
  CHECK(Near(100.f * DefenseMultiplier(4.5f), 95.69, 0.01));    // T5: armor 15 x 0.3 left after 70 % penetration
  CHECK(Near(100.f * DefenseMultiplier(15.f), 86.96, 0.01));    // T5 without the passive
  CHECK(Near(100.f * DefenseMultiplier(-20.f), 116.67, 0.01));  // T6: 2 - 100/120
  CHECK_EQ(DefenseMultiplier(0.f), 1.f);                         // T1: armor 0
  CHECK(Near(12.f * DefenseMultiplier(50.f), 8.0, 0.001));       // T4: the minion's basic on a Binding
}

GMS_TEST(DamageDescriptorRules) {
  CHECK(MayCrit(kIsBasic | kIsPhysical | kIsMelee, true));
  CHECK(!MayCrit(kIsBasic, false));
  CHECK(!MayCrit(kIsBasic | kCanNotCrit, true));
  CHECK(!MayCrit(kIsBasic | kIsSpellOverload, true));
  CHECK(!MayCrit(kIsPhysical | kIsProjectile, true));  // the tower shot (IsBasic stripped)
  CHECK(MayOverload(kIsMagical, true));
  CHECK(!MayOverload(kIsMagical | kIsSpellOverload, true));
  CHECK(!MayOverload(kIsBasic, true));
  CHECK(!MayOverload(kIsItem, true));
  CHECK(!MayOverload(kIsTrueDamage, true));
  CHECK(!MayOverload(kIsMagical, false));
  CHECK_EQ(DeathDescriptors(true, kIsAoE | kIsDoT), kHitCritical | kHitAoE | kHitDoT);
  CHECK_EQ(DeathDescriptors(false, kIsPhysical), 0u);
  CHECK_EQ(IntegerHpChange(108.f, 0.f), 108);  // T1's third hit
  CHECK_EQ(IntegerHpChange(450.f, 279.f), 171);
  CHECK_EQ(IntegerHpChange(3000.f, 2933.33f), 67);  // T4
}

GMS_TEST(DamageRewardSplit) {
  const float minionXpShare[5] = {1.f, 0.65f, 0.4333f, 0.325f, 0.26f};
  const float minionGoldShare[5] = {1.f, 1.f, 1.f, 1.f, 1.f};
  CHECK_EQ(NearbyShare(minionXpShare, 5, 1), 1.f);
  CHECK_EQ(NearbyShare(minionXpShare, 5, 2), 0.65f);
  CHECK_EQ(NearbyShare(minionXpShare, 5, 9), 0.26f);
  CHECK_EQ(NearbyShare(nullptr, 0, 3), 1.f);
  CHECK_EQ(NearbyShare(minionXpShare, 5, 0), 0.f);
  // a melee minion (4 gold, area and last hit, ratio 3) last-hit by player 0 alone: the fatal-hit bonus 12 only, no
  // area share for the last hitter (the user's rule, ROLEVIM-2)
  std::vector<Grant> g = SplitReward(kRewardAreaAndLastHit, 4.f, {0}, 0, {0}, minionGoldShare, 5, 3.f);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  CHECK_EQ(g[0].amount, 12.f);
  CHECK(g[0].lastHit);
  CHECK(!g[0].role);
  // two nearby, the other one last-hit: 4 for player 0, 12 for the last hitter
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, 3, {0, 3}, minionGoldShare, 5, 3.f);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CHECK_EQ(g[0].player, 0);
  CHECK_EQ(g[0].amount, 4.f);
  CHECK(!g[0].lastHit);
  CHECK_EQ(g[1].player, 3);
  CHECK_EQ(g[1].amount, 12.f);
  CHECK(g[1].lastHit);
  // the last hitter out of the area: the bonus all the same
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0}, 3, {0, 3}, minionGoldShare, 5, 3.f);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CHECK_EQ(g[1].amount, 12.f);
  // XP: area with two nearby, 58 x 0.65 each
  g = SplitReward(kRewardArea, 58.f, {0, 3}, 0, {0, 3}, minionXpShare, 5, 3.f);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CHECK(Near(g[1].amount, 37.7, 0.01));
  // a tower: 150 to every player of the receiving team, nearby or not
  g = SplitReward(kRewardTeam, 150.f, {}, -1, {0, 1, 2}, nullptr, 0, 0.f);
  CHECK_EQ(g.size(), static_cast<size_t>(3));
  CHECK_EQ(g[2].amount, 150.f);
  // a creep killed by a minion: last-hit gold goes to nobody, area XP to the players nearby
  CHECK(SplitReward(kRewardLastHit, 6.f, {0}, -1, {0}, nullptr, 0, 1.f).empty());
  CHECK_EQ(SplitReward(kRewardArea, 20.f, {0}, -1, {0}, minionXpShare, 5, 1.f).size(), static_cast<size_t>(1));
  // nothing for zero amounts or the titan types
  CHECK(SplitReward(kRewardTeam, 0.f, {}, -1, {0}, nullptr, 0, 0.f).empty());
  CHECK(SplitReward(kRewardEnemyTitans, 10.f, {0}, 0, {0}, nullptr, 0, 0.f).empty());
}

// ROLEVIM: RolesTuning's NPC-kill gold modifiers (Gladiator lastHitMinion 2.0, Tactician proximity 2.0, the rest 1.0);
// ROLEVIM-2 (the user's rules of 2026-10-18): the last hitter gets no area share, and a role's part is its own grant.
static void CheckGrant(const Grant& g, int player, float amount, bool lastHit, bool role) {
  CHECK_EQ(g.player, player);
  CHECK_EQ(g.amount, amount);
  CHECK_EQ(g.lastHit, lastHit);
  CHECK_EQ(g.role, role);
}

GMS_TEST(DamageRoleNpcGold) {
  const float minionGoldShare[5] = {1.f, 1.f, 1.f, 1.f, 1.f};
  RoleNpcGold roles[10];
  roles[0].lastHitMinion = 2.f;  // player 0 the Gladiator
  roles[3].proximity = 2.f;      // player 3 the Tactician
  // player 1 has no role modifier (a Hunter or a Predator)
  // the Gladiator's last hit on a melee minion (4 gold), alone: 12 (the bonus) + 12 (the role's own grant) = 24
  std::vector<Grant> g =
      SplitReward(kRewardAreaAndLastHit, 4.f, {0}, 0, {0}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CheckGrant(g[0], 0, 12.f, true, false);
  CheckGrant(g[1], 0, 12.f, true, true);
  // another role's last hit: 12, one grant
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {1}, 1, {1}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  CheckGrant(g[0], 1, 12.f, true, false);
  // the Gladiator last-hits beside the Tactician and player 1: the Tactician 4 + 4 (role), player 1 4, the Gladiator
  // 12 + 12 (role); the shares first, then the last hitter's grants
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 1, 3}, 0, {0, 1, 3}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(5));
  CheckGrant(g[0], 1, 4.f, false, false);
  CheckGrant(g[1], 3, 4.f, false, false);
  CheckGrant(g[2], 3, 4.f, false, true);
  CheckGrant(g[3], 0, 12.f, true, false);
  CheckGrant(g[4], 0, 12.f, true, true);
  // a minion killed by minions beside the Tactician and the Gladiator: 4 + 4 (role) and 4, nobody's last hit
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, -1, {0, 3}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(3));
  CheckGrant(g[0], 0, 4.f, false, false);
  CheckGrant(g[1], 3, 4.f, false, false);
  CheckGrant(g[2], 3, 4.f, false, true);
  // the Tactician's own last hit: 12, one grant (no area share, so nothing for its factor)
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, 3, {0, 3}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CheckGrant(g[0], 0, 4.f, false, false);
  CheckGrant(g[1], 3, 12.f, true, false);
  // the ally factor multiplies when another player of the team got the last hit (1.0 in the data): 2 x 0.5 = 1, so no
  // role grant; below 1 it scales the share itself
  roles[3].proximityAllyLastHit = 0.5f;
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, 0, {0, 3}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(3));
  CheckGrant(g[0], 3, 4.f, false, false);
  roles[3].proximityAllyLastHit = 0.25f;
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, 0, {0, 3}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 10);
  CheckGrant(g[0], 3, 2.f, false, false);
  roles[3].proximityAllyLastHit = 1.f;
  // a jungle creature (last player to hit) uses lastHitCreature, not lastHitMinion: 10 + 5 (role)
  roles[0].lastHitCreature = 1.5f;
  g = SplitReward(kRewardLastHit, 10.f, {0}, 0, {0}, nullptr, 0, 1.f, kNpcCreature, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CheckGrant(g[0], 0, 10.f, true, false);
  CheckGrant(g[1], 0, 5.f, true, true);
  roles[0].lastHitCreature = 1.f;
  g = SplitReward(kRewardLastHit, 10.f, {0}, 0, {0}, nullptr, 0, 1.f, kNpcCreature, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  CHECK_EQ(g[0].amount, 10.f);
  // a worker (area, kNpcOther) or a structure: no role factor; nor without the role table (role_passives=0) or
  // outside it: the Gladiator's last hit 12, the Tactician's share 4
  g = SplitReward(kRewardArea, 6.f, {3}, 0, {3}, minionGoldShare, 5, 0.f, kNpcOther, roles, 10);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  CHECK_EQ(g[0].amount, 6.f);
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0, 3}, 0, {0, 3}, minionGoldShare, 5, 3.f, kNpcMinion, nullptr, 0);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CheckGrant(g[0], 3, 4.f, false, false);
  CheckGrant(g[1], 0, 12.f, true, false);
  g = SplitReward(kRewardAreaAndLastHit, 4.f, {0}, 0, {0}, minionGoldShare, 5, 3.f, kNpcMinion, roles, 0);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  CHECK_EQ(g[0].amount, 12.f);
}

// ROLEVIM-2: PushWithRole, the normal grant then the role's part
GMS_TEST(DamageRoleGrantSplit) {
  std::vector<Grant> g;
  PushWithRole(&g, 2, 300.f, 1.5f, true);
  CHECK_EQ(g.size(), static_cast<size_t>(2));
  CheckGrant(g[0], 2, 300.f, true, false);
  CheckGrant(g[1], 2, 150.f, true, true);
  g.clear();
  PushWithRole(&g, 2, 300.f, 1.f, false);
  CHECK_EQ(g.size(), static_cast<size_t>(1));
  PushWithRole(&g, -1, 300.f, 2.f, false);  // nobody
  PushWithRole(&g, 4, 0.f, 2.f, false);     // nothing
  CHECK_EQ(g.size(), static_cast<size_t>(1));
}

// VIMPOPUP: the role's part rides on its player's normal grant as LabsPlayer_GrantGold's bonus (one event, two popups).
GMS_TEST(DamageGoldEventsBonus) {
  // the user's log: a Tactician's share beside a Gladiator's last hit: p0 +4 +4 (role) p3 +12 (last hit) +12 (role)
  std::vector<Grant> g;
  PushWithRole(&g, 0, 4.f, 2.f, false);
  PushWithRole(&g, 3, 12.f, 2.f, true);
  PushWithRole(&g, 1, 4.f, 1.f, false);  // another role: no bonus
  const std::vector<GoldEvent> e = GoldEvents(g);
  CHECK_EQ(e.size(), static_cast<size_t>(3));
  CHECK_EQ(e[0].player, 0);
  CHECK(Near(e[0].amount, 4.0, 1e-6));
  CHECK(Near(e[0].bonus, 4.0, 1e-6));
  CHECK(!e[0].lastHit);
  CHECK_EQ(e[1].player, 3);
  CHECK(Near(e[1].amount, 12.0, 1e-6));
  CHECK(Near(e[1].bonus, 12.0, 1e-6));
  CHECK(e[1].lastHit);
  CHECK_EQ(e[2].player, 1);
  CHECK(Near(e[2].bonus, 0.0, 1e-6));
  // the totals stay the grants' totals
  float a = 0.f, b = 0.f;
  for (const Grant& x : g) a += x.amount;
  for (const GoldEvent& x : e) b += x.amount + x.bonus;
  CHECK(Near(a, b, 1e-5));
  // a Predator's kill: 300 + 150 (role)
  g.clear();
  PushWithRole(&g, 2, 300.f, 1.5f, true);
  const std::vector<GoldEvent> k = GoldEvents(g);
  CHECK_EQ(k.size(), static_cast<size_t>(1));
  CHECK(Near(k[0].bonus, 150.0, 1e-5));
  // a role grant without its normal grant stays its own event
  Grant lone;
  lone.player = 5;
  lone.amount = 7.f;
  lone.role = true;
  const std::vector<GoldEvent> l = GoldEvents({lone});
  CHECK_EQ(l.size(), static_cast<size_t>(1));
  CHECK(Near(l[0].amount, 7.0, 1e-6));
  CHECK(Near(l[0].bonus, 0.0, 1e-6));
}

// VIMPOPUP: LabsPlayer_AddGold's CombatEvent and CombatEvent_Gold.lua's two popups.
GMS_TEST(DamageGoldPopups) {
  GoldPopups p = PopupsOf(4.f, 4.f, 8.f);  // the Tactician's share
  CHECK(p.event);
  CHECK_EQ(p.bonusShown, 4);
  CHECK_EQ(p.baseShown, 4);
  p = PopupsOf(4.03f, 4.03f, 8.06f);  // a later wave: floored
  CHECK_EQ(p.bonusShown, 4);
  CHECK_EQ(p.baseShown, 4);
  p = PopupsOf(3.03f, 3.03f, 6.06f);
  CHECK_EQ(p.bonusShown, 3);
  CHECK_EQ(p.baseShown, 3);
  p = PopupsOf(12.f, 12.f, 48.f);  // a melee Gladiator's last hit with two stored (the handler adds 24 to the bonus)
  CHECK_EQ(p.bonusShown, 36);
  CHECK_EQ(p.baseShown, 12);
  p = PopupsOf(12.f, 0.f, 36.f);  // ROLEVIM-2's normal grant with the storage: 24 + 12
  CHECK_EQ(p.bonusShown, 24);
  CHECK_EQ(p.baseShown, 12);
  p = PopupsOf(4.f, 0.f, 4.f);  // a plain share: one popup
  CHECK_EQ(p.bonusShown, 0);
  CHECK_EQ(p.baseShown, 4);
  p = PopupsOf(300.f, 150.f, 450.f);  // a Predator's kill
  CHECK_EQ(p.bonusShown, 150);
  CHECK_EQ(p.baseShown, 300);
  p = PopupsOf(0.f, 5.f, 5.f);  // a bonus equal to the whole is sent as 0
  CHECK_EQ(p.bonusShown, 0);
  CHECK_EQ(p.baseShown, 5);
  p = PopupsOf(0.5f, 0.f, 0.5f);  // under 1: no event
  CHECK(!p.event);
}

// MATCH_FLOW.md R12: 500 at deploy (the host's), then 100 per minute from 60 s in whole units.
GMS_TEST(DamagePassiveGold) {
  PassiveGold pg;
  int total = 0;
  for (int s = 0; s <= 120; ++s) total += pg.Due(s);
  CHECK_EQ(total, 100);  // 60 -> 120 s
  for (int s = 121; s <= 180; ++s) total += pg.Due(s);
  CHECK_EQ(total, 200);
  // a late start and uneven calls pay the same
  PassiveGold late;
  int t2 = late.Due(30.0);
  t2 += late.Due(59.5);
  CHECK_EQ(t2, 0);
  t2 += late.Due(90.25);
  t2 += late.Due(120.0);
  CHECK_EQ(t2, 100);
  // never twice for the same time
  CHECK_EQ(late.Due(120.0), 0);
  CHECK_EQ(late.Due(100.0), 0);
}

// D1g (MATCH_FLOW.md §2.10): deathTimeBase 15 + deathTimePerLevel 0.5 x level + deathTimePerMinute 1.25 x match minutes,
// and the respawn time on the server clock.
GMS_TEST(DamageDeathTime) {
  CHECK(Near(DeathSeconds(15.f, 0.5f, 1.25f, 1, 0.0), 15.5, 1e-5));       // level 1 before the match clock runs
  CHECK(Near(DeathSeconds(15.f, 0.5f, 1.25f, 1, 240.0), 20.5, 1e-5));     // 4 minutes: + 5
  CHECK(Near(DeathSeconds(15.f, 0.5f, 1.25f, 10, 1200.0), 45.0, 1e-5));   // level 10, 20 minutes
  CHECK(Near(DeathSeconds(15.f, 0.5f, 1.25f, 3, 90.0), 18.375, 1e-5));    // fractional minutes
  CHECK(Near(DeathSeconds(15.f, 0.5f, 1.25f, 1, -5.0), 15.5, 1e-5));      // a negative clock counts as 0
  CHECK(Near(DeathSeconds(-20.f, 0.f, 0.f, 1, 0.0), 0.0, 1e-9));          // never negative
  CHECK_EQ(RespawnTimeMs(100000ull, 15.5f), static_cast<std::uint64_t>(115500));
  CHECK_EQ(RespawnTimeMs(100000ull, 18.375f), static_cast<std::uint64_t>(118375));
  CHECK_EQ(RespawnTimeMs(100000ull, 0.f), static_cast<std::uint64_t>(100000));
}

// D1i (DAMAGE.md *D1i*): regeneration per 5 s, the Guardian's "bar over 240 s", the dead, the caps.
GMS_TEST(DamageRegenHealth) {
  // the Spirit Guardian's passive: 5000 / (240 / 5) = 104.17 per 5 s -> 20.83 HP/s, the bar back in 240 s
  const float g = 5000.f / (240.f / 5.f);
  CHECK(Near(RegenHealth(52.f, g, 5000.f, 1.f), 52.0 + 20.8333, 1e-3));
  CHECK(Near(RegenHealth(52.f, g, 5000.f, 0.5f), 52.0 + 10.4167, 1e-3));
  CHECK_EQ(RegenHealth(4990.f, g, 5000.f, 1.f), 5000.f);              // stops at the cap
  CHECK_EQ(RegenHealth(5000.f, g, 5000.f, 1.f), 5000.f);              // full: nothing
  CHECK_EQ(RegenHealth(0.f, g, 5000.f, 1.f), 0.f);                    // the dead: nothing
  CHECK_EQ(RegenHealth(-1.f, g, 5000.f, 1.f), -1.f);
  CHECK_EQ(RegenHealth(100.f, -5.f, 450.f, 1.f), 100.f);              // no degeneration
  CHECK_EQ(RegenHealth(100.f, 5.f, 450.f, 0.f), 100.f);
  // Fenmore at level 1 (PC_Alpha baseHealthRegen 5): 1 HP/s; level 4 (+ 3 x 0.45): 1.27 HP/s
  CHECK(Near(RegenHealth(150.f, 5.f, 450.f, 60.f), 210.0, 1e-3));
  CHECK(Near(RegenHealth(150.f, 5.f + 3.f * 0.45f, 450.f, 10.f), 162.7, 1e-3));
  // the base zone (modifier_base_regen: HealthRegen + max HP): full in 5 s
  CHECK(Near(RegenHealth(150.f, 5.f + 450.f, 450.f, 0.5f), 150.0 + 45.5, 1e-3));
  // a jungle creep at home (modifier_Jungle_Creep_Regen: 2000 per 5 s)
  CHECK_EQ(RegenHealth(150.f, 2000.f, 330.f, 0.5f), 330.f);
  // the derived total: x (1 + HealIncrease) x (1 - HealingReduction)
  CHECK(Near(TotalHealthRegen(10.f, 0.f, 0.f), 10.0, 1e-6));
  CHECK(Near(TotalHealthRegen(10.f, 0.2f, 0.5f), 6.0, 1e-5));
}

GMS_TEST(DamageRegenResource) {
  CHECK(Near(ResourceRegenPer5(50.f, 0.f, 200.f, false), 50.0, 1e-6));  // PC_Otter's energy: 10 per s
  CHECK(Near(ResourceRegenPer5(0.f, 0.06f, 480.f, false), 28.8, 1e-4));  // a percent regen of 480
  CHECK(Near(ResourceRegenPer5(25.f, 0.06f, 480.f, true), 0.0, 1e-9));   // DisableResourcePointRegen
  CHECK(Near(RegenResource(0.f, 50.f, 200.f, 1.f), 10.0, 1e-5));
  CHECK_EQ(RegenResource(195.f, 50.f, 200.f, 1.f), 200.f);
  CHECK_EQ(RegenResource(200.f, 50.f, 200.f, 1.f), 200.f);
  CHECK_EQ(RegenResource(10.f, 0.f, 200.f, 1.f), 10.f);
  CHECK_EQ(RegenResource(10.f, 50.f, 0.f, 1.f), 10.f);                 // no resource pool
}

GMS_TEST(DamageRegenHealthBars) {
  const float bars[3] = {4250.f, 5000.f, 8000.f};  // GuardianShared's tiers (17250)
  CHECK(Near(HealthBarTop(17250.f, bars, 3, 0), 17250.0, 1e-3));
  CHECK(Near(HealthBarTop(17250.f, bars, 3, 1), 13000.0, 1e-3));
  CHECK(Near(HealthBarTop(17250.f, bars, 3, 2), 8000.0, 1e-3));
  const float v5[1] = {5000.f};  // GuardianShared_v5: one bar
  CHECK(Near(HealthBarTop(5000.f, v5, 1, 0), 5000.0, 1e-3));
  CHECK(Near(HealthBarTop(450.f, nullptr, 0, 0), 450.0, 1e-3));
  // a Guardian in its second bar regenerates to that bar's top, not into the lost one
  CHECK_EQ(RegenHealth(12990.f, 8000.f / 48.f, HealthBarTop(17250.f, bars, 3, 1), 5.f), 13000.f);
}

// DISCIPLINE: Raina's W (ability_Guardian_W_ConeShield) pays 1.25 Discipline every 0.1 s while it runs and ends when a
// tick finds less than that; 100 Discipline last 8 s (regen is off under the shield's DisableResourcePointRegen).
GMS_TEST(DamageUpkeep) {
  const float w[8] = {1.25f, 0.f, 0.f, 0.f, 0.f, 0.f, 0.f, 0.f};
  CHECK_EQ(UpkeepCostForRank(w, 1), 1.25f);
  CHECK_EQ(UpkeepCostForRank(w, 0), 1.25f);   // the rank clamped to 1..8
  CHECK_EQ(UpkeepCostForRank(w, 3), 0.f);     // a one-entry array: the other ranks have none (the script's {1.25})
  CHECK_EQ(UpkeepCostForRank(nullptr, 1), 0.f);
  // nothing due before the first tick
  UpkeepResult r = UpkeepStep(100.f, 1.25f, 0.1f, 1000, 1099);
  CHECK_EQ(r.ticksPaid, 0);
  CHECK_EQ(r.resource, 100.f);
  CHECK(!r.end);
  // one 500 ms step: five ticks, 6.25 paid, the clock on the last tick
  r = UpkeepStep(100.f, 1.25f, 0.1f, 1000, 1530);
  CHECK_EQ(r.ticksPaid, 5);
  CHECK_EQ(r.resource, 93.75f);
  CHECK_EQ(r.lastTickMs, 1500ull);
  // run 100 Discipline down in 33 ms steps: it lasts 80 ticks (8 s) and ends at the 81st
  float res = 100.f;
  unsigned long long last = 0, now = 0;
  int paid = 0;
  bool ended = false;
  while (!ended && now < 20000) {
    now += 33;
    r = UpkeepStep(res, 1.25f, 0.1f, last, now);
    res = r.resource;
    last = r.lastTickMs;
    paid += r.ticksPaid;
    ended = r.end;
  }
  CHECK(ended);
  CHECK_EQ(paid, 80);
  CHECK_EQ(res, 0.f);
  CHECK(now >= 8100 && now < 8200);
  // a tick that can't pay ends the ability without paying part of it
  r = UpkeepStep(1.f, 1.25f, 0.1f, 0, 100);
  CHECK(r.end);
  CHECK_EQ(r.resource, 1.f);
  CHECK_EQ(r.ticksPaid, 0);
  // a stall pays at most kUpkeepMaxTicksPerStep ticks and the clock catches up
  r = UpkeepStep(100.f, 1.25f, 0.1f, 0, 5050);
  CHECK_EQ(r.ticksPaid, kUpkeepMaxTicksPerStep);
  CHECK_EQ(r.lastTickMs, 5000ull);
  // no cost or no tick rate: nothing
  CHECK_EQ(UpkeepStep(50.f, 0.f, 0.1f, 0, 5000).ticksPaid, 0);
  CHECK_EQ(UpkeepStep(50.f, 1.25f, 0.f, 0, 5000).ticksPaid, 0);
  CHECK(!UpkeepStep(0.f, 0.f, 0.1f, 0, 5000).end);
}
