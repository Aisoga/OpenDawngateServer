// gms/core/stats (M18, MSTATS4): crowd control caused and endured, wards placed (gms/core/stats/cc.h):
// the kind priority, the feedback mapping, intervals and refreshes, the unions, the team rules.
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

#include "core/protocol/messages.h"
#include "core/stats/cc.h"
#include "tests/check.h"

using namespace gms::stats;

namespace {

// Seat 0 (team 1, object 0x100) against seat 5 (team 2, object 0x500); a minion 0x900 of team 2.
CcStart Start(std::uint32_t instance, unsigned debuff, std::uint64_t start, std::uint32_t duration, int casterSeat = 0,
              int carrierSeat = 5) {
  CcStart s;
  s.instance = instance;
  s.debuff = debuff;
  s.startMs = start;
  s.durationMs = duration;
  s.casterSeat = casterSeat;
  s.caster = casterSeat >= 0 ? 0x100u + static_cast<std::uint32_t>(casterSeat) * 0x100u : 0x777u;
  s.casterTeam = casterSeat >= 5 ? 2 : 1;
  s.carrierSeat = carrierSeat;
  s.carrier = carrierSeat >= 0 ? 0x100u + static_cast<std::uint32_t>(carrierSeat) * 0x100u : 0x900u;
  s.carrierTeam = carrierSeat >= 0 && carrierSeat < 5 ? 1 : 2;
  return s;
}

double Get(const CcTally& t, int seat, const std::string& key) {
  for (const auto& kv : t.SeatStats(seat))
    if (kv.first == key) return kv.second;
  return 0.0;
}

}  // namespace

GMS_TEST(StatsCcKindPriority) {
  CHECK(CcKindOf(0) == CcKind::None);
  CHECK(CcKindOf(kDebuffImmuneToCleanse) == CcKind::None);  // IsImmuneToCleanse alone: no CC
  CHECK(CcKindOf(kDebuffSlow) == CcKind::Slow);
  CHECK(CcKindOf(kDebuffStun) == CcKind::Stun);
  CHECK(CcKindOf(kDebuffRoot | kDebuffSlow) == CcKind::Root);
  CHECK(CcKindOf(0x320) == CcKind::Pin);                    // IsPin carries the knockback bit too
  CHECK(CcKindOf(0x40 | 0x80) == CcKind::Suppression);      // ImmuneToCleanse | Suppression
  CHECK(CcKindOf(0x4 | 0x160) == CcKind::Stun);             // modifier_BloodMage_R_Stun: Stun | IgnoreCC
  CHECK(CcKindOf(kDebuffKnockback) == CcKind::Knockback);   // modifier_Artificer_R_KnockAside
  CHECK(CcKindOf(kDebuffFear | kDebuffRoot) == CcKind::Fear);
  CHECK(CcKindOf(kDebuffSilence | kDebuffSlow) == CcKind::Silence);
  CHECK_EQ(std::string(CcKindName(CcKind::Suppression)), std::string("Suppression"));
}

GMS_TEST(StatsCcFeedbackMapping) {
  // GameSimulator_SendModifierCreated 0x008F57A0: Stun 1, Fear 2, Silence 4, Suppression 8, Root 0x10, Slow 0x20
  CHECK_EQ(DebuffFromFeedback(0x1), kDebuffStun);
  CHECK_EQ(DebuffFromFeedback(0x2), kDebuffFear);
  CHECK_EQ(DebuffFromFeedback(0x4), kDebuffSilence);
  CHECK_EQ(DebuffFromFeedback(0x8), kDebuffSuppression);
  CHECK_EQ(DebuffFromFeedback(0x10), kDebuffRoot);
  CHECK_EQ(DebuffFromFeedback(0x20), kDebuffSlow);
  CHECK_EQ(DebuffFromFeedback(0x3F), kDebuffStun | kDebuffFear | kDebuffSilence | kDebuffSuppression | kDebuffRoot |
                                         kDebuffSlow);
  CHECK_EQ(FeedbackFromDebuff(0x320), 0u);         // a pin: nothing on the wire
  CHECK_EQ(FeedbackFromDebuff(0x4 | 0x160), 0x1u);  // BloodMage_R_Stun: the stun only
  CHECK_EQ(FeedbackFromDebuff(0xC0), 0x8u);         // ImmuneToCleanse | Suppression
  for (unsigned f = 0; f < 0x40; ++f) CHECK_EQ(FeedbackFromDebuff(DebuffFromFeedback(f)), f);
}

GMS_TEST(StatsCcOneStunBothSides) {
  CcTally t;
  CHECK(t.OnCreated(Start(1, kDebuffStun, 10000, 1500)) == CcVerdict::Counted);
  CHECK(t.OnEnded(1, 12000));  // the registry sees the end a little late: clipped at start + duration
  t.Close(60000);
  CHECK_EQ(Get(t, 0, "ccCausedMs"), 1500.0);
  CHECK_EQ(Get(t, 0, "ccCausedStunMs"), 1500.0);
  CHECK_EQ(Get(t, 5, "ccEnduredMs"), 1500.0);
  CHECK_EQ(Get(t, 5, "ccEnduredStunMs"), 1500.0);
  CHECK_EQ(Get(t, 0, "ccEnduredMs"), 0.0);
  CHECK_EQ(Get(t, 5, "ccCausedMs"), 0.0);
  CHECK_EQ(t.SeatStats(0).size(), static_cast<size_t>(2));  // only the keys not 0
  CHECK_EQ(t.CountOf(CcKind::Stun), 1);
}

GMS_TEST(StatsCcEarlyEndAndUntimed) {
  CcTally t;
  t.OnCreated(Start(1, kDebuffRoot, 1000, 2000));
  t.OnEnded(1, 1800);  // cleansed after 800 ms
  t.OnCreated(Start(2, kDebuffSilence, 5000, 0));  // untimed: until it ends
  t.OnEnded(2, 5600);
  t.OnCreated(Start(3, kDebuffFear, 9000, 0));  // untimed, open at the end
  t.Close(9250);
  CHECK_EQ(Get(t, 0, "ccCausedRootMs"), 800.0);
  CHECK_EQ(Get(t, 0, "ccCausedSilenceMs"), 600.0);
  CHECK_EQ(Get(t, 0, "ccCausedFearMs"), 250.0);
  CHECK_EQ(Get(t, 0, "ccCausedMs"), 1650.0);
  CHECK_EQ(Get(t, 5, "ccEnduredMs"), 1650.0);
  CHECK(!t.OnEnded(99, 1));
}

GMS_TEST(StatsCcRefresh) {
  CcTally t;
  t.OnCreated(Start(1, kDebuffSlow, 1000, 2000));
  CHECK(t.OnUpdated(1, 2500, 2000));  // refreshed at 2.5 s: [1000, 2500) + [2500, 4500)
  t.OnEnded(1, 10000);
  t.OnCreated(Start(2, kDebuffSlow, 20000, 1000));
  CHECK(t.OnUpdated(2, ~0ull, 3000));  // the start kept, a longer duration: [20000, 23000)
  t.Close(60000);
  CHECK_EQ(Get(t, 0, "ccCausedSlowMs"), 3500.0 + 3000.0);
  CHECK_EQ(Get(t, 5, "ccEnduredSlowMs"), 6500.0);
  CHECK(!t.OnUpdated(1, 0, 0));  // ended: not tracked any more
}

GMS_TEST(StatsCcUnionEnduredSumCaused) {
  CcTally t;
  // two casters stun seat 5 at once: each caster gets its own, seat 5 endures the union once
  t.OnCreated(Start(1, kDebuffStun, 1000, 1000, 0, 5));
  t.OnCreated(Start(2, kDebuffStun, 1500, 1000, 1, 5));
  // a slow under the stun: its own kind adds its time, the overall union only what lies outside the stuns
  t.OnCreated(Start(3, kDebuffSlow, 1200, 2000, 0, 5));
  t.Close(10000);
  CHECK_EQ(Get(t, 0, "ccCausedStunMs"), 1000.0);
  CHECK_EQ(Get(t, 1, "ccCausedStunMs"), 1000.0);
  CHECK_EQ(Get(t, 0, "ccCausedMs"), 3000.0);
  CHECK_EQ(Get(t, 5, "ccEnduredStunMs"), 1500.0);
  CHECK_EQ(Get(t, 5, "ccEnduredSlowMs"), 2000.0);
  CHECK_EQ(Get(t, 5, "ccEnduredMs"), 2200.0);  // [1000, 3200)
  CHECK_EQ(UnionMs({{0, 10}, {5, 20}, {30, 40}, {40, 41}, {50, 50}}), static_cast<std::uint64_t>(31));
}

GMS_TEST(StatsCcTeamsSelfAndNobody) {
  CcTally t;
  // self-applied: never
  CcStart self = Start(1, kDebuffStun, 0, 1000, 0, 0);
  self.carrier = self.caster;
  CHECK(t.OnCreated(self) == CcVerdict::Self);
  // a non-player enemy (a minion): caused only
  CHECK(t.OnCreated(Start(2, kDebuffRoot, 0, 1000, 0, -1)) == CcVerdict::Counted);
  // a minion's (no seat) stun on seat 5: endured only
  CHECK(t.OnCreated(Start(3, kDebuffStun, 0, 700, -1, 5)) == CcVerdict::Counted);
  // an ally's slow (seat 1 on seat 0, both team 1): nobody's
  CHECK(t.OnCreated(Start(4, kDebuffSlow, 0, 1000, 1, 0)) == CcVerdict::Nobody);
  // not CC
  CHECK(t.OnCreated(Start(5, kDebuffImmuneToCleanse, 0, 1000)) == CcVerdict::NotCc);
  // the carrier is deleted (a minion dies): its instances end
  CHECK_EQ(t.OnCarrierGone(0x900, 400), 1);
  t.Close(5000);
  CHECK_EQ(Get(t, 0, "ccCausedRootMs"), 400.0);
  CHECK_EQ(Get(t, 0, "ccCausedMs"), 400.0);
  CHECK_EQ(Get(t, 0, "ccEnduredMs"), 0.0);
  CHECK_EQ(Get(t, 5, "ccEnduredStunMs"), 700.0);
  CHECK_EQ(Get(t, 1, "ccCausedMs"), 0.0);
  CHECK_EQ(t.Tracked(), 2);
  // after Close nothing more counts
  CHECK(t.OnCreated(Start(6, kDebuffStun, 6000, 1000)) == CcVerdict::Nobody);
}

GMS_TEST(StatsCcReusedInstance) {
  CcTally t;
  t.OnCreated(Start(1, kDebuffStun, 0, 0));      // untimed, never ended
  t.OnCreated(Start(1, kDebuffStun, 300, 500));  // the same id again: the first ends at 300
  t.Close(10000);
  CHECK_EQ(Get(t, 0, "ccCausedStunMs"), 800.0);
  CHECK_EQ(Get(t, 5, "ccEnduredStunMs"), 800.0);
}

GMS_TEST(StatsWardsPlaced) {
  CHECK_EQ(gms::AssetIdOf("ability_Item_Ward"), kGuidItemWard);
  CHECK_EQ(gms::AssetIdOf("ability_Outlaw_Passive_Ward"), kGuidOutlawPassiveWard);
  WardTally w;
  CHECK(w.OnTickThread(kGuidItemWard, false, 3));
  CHECK(!w.OnTickThread(kGuidItemWard, true, 3));  // a reactivation: no new ward
  CHECK(w.OnTickThread(kGuidOutlawPassiveWard, false, 3));
  CHECK(!w.OnTickThread(0x12345678, false, 3));
  CHECK(!w.OnTickThread(kGuidItemWard, false, -1));
  CHECK_EQ(w.Placed(3), 2);
  CHECK_EQ(w.Placed(4), 0);
}
