// BOTDEFEND (gms/core/agents/defend.h): a bot an enemy shaper attacks while its plan backs off or flees shoots back
// when it can't get away, and doesn't recall while that shaper is on it. The public server's case: Petrus (PC_Creature,
// 1,398 HP) chased through the jungle by Kindra.
#include "core/agents/brain.h"
#include "core/agents/defend.h"
#include "core/agents/recall.h"
#include "tests/check.h"

using namespace gms::agents;

namespace {

DefendInputs Attacked(int phase, float dist) {
  DefendInputs in;
  in.attacker = true;
  in.visible = true;
  in.dist = dist;
  in.phase = phase;
  in.reach = DefendConfig().reachDefault;
  return in;
}

}  // namespace

GMS_TEST(BotDefendPhasesFromTheData) {
  CHECK_EQ(DefendPhaseOf("Creature_Gladiator_Reposition.Phase"), static_cast<int>(kDefendBackOff));
  CHECK_EQ(DefendPhaseOf("Creature_Gladiator_Flee.Phase"), static_cast<int>(kDefendFlee));
  // EngageForKill's tower phase is the plan's own business (KILLFINISH), as for BOTRECALL
  CHECK_EQ(DefendPhaseOf("Creature_Gladiator_FleeTowerTargeting.Phase"), static_cast<int>(kDefendOtherPhase));
  CHECK_EQ(DefendPhaseOf("Creature_Gladiator_SelfDefense.Phase"), static_cast<int>(kDefendOtherPhase));
  CHECK_EQ(DefendPhaseOf(nullptr), static_cast<int>(kDefendOtherPhase));
}

// The chain at 929.9-933.7 s: PlayerCombat's phase was Reposition (Counterattack false below 60 %), Kindra 4 units
// away hitting it; the data has no attack there, the rule shoots back.
GMS_TEST(BotDefendShootsBackWhileBackingOff) {
  const DefendConfig c;
  CHECK_EQ(DecideDefend(Attacked(kDefendBackOff, 4.f), c), static_cast<int>(kDwShoot));
  // a ranged attacker beyond its reach: no walk toward it, the retreat goes on
  CHECK_EQ(DecideDefend(Attacked(kDefendBackOff, 20.f), c), static_cast<int>(kDwOutOfReach));
  // the plan's own fight phase (SelfDefense has the basic attack) and the lane: left alone
  CHECK_EQ(DecideDefend(Attacked(kDefendOtherPhase, 4.f), c), static_cast<int>(kDwPhase));
  DefendInputs hidden = Attacked(kDefendBackOff, 4.f);
  hidden.visible = false;  // BOTBUSH: nothing on a shaper its team doesn't see
  CHECK_EQ(DecideDefend(hidden, c), static_cast<int>(kDwHidden));
  DefendInputs none = Attacked(kDefendBackOff, 4.f);
  none.attacker = false;
  CHECK_EQ(DecideDefend(none, c), static_cast<int>(kDwNoAttacker));
}

// 933.7-935.1 s: Flee at 19 %, Kindra 15 units behind. Running first; caught for 1.5 s, it shoots back.
GMS_TEST(BotDefendFleeShootsOnlyWhenCaught) {
  const DefendConfig c;
  ReachMemory m;
  m.Hit(16.f, c);  // its basic attack started on a shaper 16 units away earlier
  DefendInputs in = Attacked(kDefendFlee, 15.f);
  in.reach = m.Get(c);
  long long since = CaughtSince(0, true, true, true, in.dist, in.reach, 933700);
  CHECK_EQ(since, 933700LL);
  in.caughtForMs = 933700 - since;
  CHECK_EQ(DecideDefend(in, c), static_cast<int>(kDwRunning));
  since = CaughtSince(since, true, true, true, in.dist, in.reach, 934300);
  in.caughtForMs = 934300 - since;
  CHECK_EQ(DecideDefend(in, c), static_cast<int>(kDwRunning));
  since = CaughtSince(since, true, true, true, in.dist, in.reach, 935200);
  in.caughtForMs = 935200 - since;
  CHECK_EQ(DecideDefend(in, c), static_cast<int>(kDwShoot));
  // it got away (beyond its reach): the watch starts again
  CHECK_EQ(CaughtSince(since, true, true, true, 25.f, in.reach, 935400), 0LL);
  CHECK_EQ(CaughtSince(since, false, true, true, 5.f, in.reach, 935400), 0LL);  // not fleeing
}

GMS_TEST(BotDefendLearnsItsReach) {
  const DefendConfig c;
  ReachMemory m;
  CHECK_EQ(m.Get(c), c.reachDefault);
  m.Hit(18.5f, c);  // a ranged basic
  CHECK_EQ(m.Get(c), 18.5f);
  m.Hit(10.f, c);  // a shorter one proves nothing new
  CHECK_EQ(m.Get(c), 18.5f);
  m.Miss(17.f, c);  // "out of range" at 17: below that
  CHECK_EQ(m.Get(c), 16.5f);
  m.Miss(20.f, c);  // a miss beyond the reach changes nothing
  CHECK_EQ(m.Get(c), 16.5f);
  ReachMemory melee;
  melee.Miss(6.f, c);
  melee.Miss(3.2f, c);
  CHECK_EQ(melee.Get(c), c.reachMin);  // never below a melee swing
  ReachMemory far;
  far.Hit(45.f, c);
  CHECK_EQ(far.Get(c), c.reachMax);
}

// The recall: a chaser 15 units away that hit it 0.2 s ago stops it; so does one 50 away (outside recall.h's 45) that
// hit it 5 s ago (after the quiet time); one that stopped chasing (8+ s) or fell 60+ behind doesn't.
GMS_TEST(BotDefendNoRecallWhileChased) {
  const DefendConfig c;
  CHECK(RecallChased(15.1f, 200, c));
  CHECK(RecallChased(50.f, 5000, c));
  CHECK(!RecallChased(50.f, 9000, c));
  CHECK(!RecallChased(70.f, 5000, c));
  CHECK(!RecallChased(-1.f, 200, c));  // no attacker seen
  // with recall.h's own rule: quiet for 5 s, nobody within 45, far from home -> allowed, unless the chaser counts
  RecallInputs in;
  in.fleePhase = true;
  in.sinceHitMs = 5000;
  in.nearestEnemyShaper = 50.f;
  in.baseDistance = 200.f;
  const RecallConfig rc;
  CHECK(RecallAllowed(in, rc));
  in.chased = RecallChased(50.f, 5000, c);
  CHECK(!RecallAllowed(in, rc));
  // the Flee gambit's recall held: the list goes on to the walk home (no wait on it, as for a basic on cooldown)
  CHECK(GambitRefusalSkips(kResultRecallChased, kSlotNone));
}

GMS_TEST(BotDefendNames) {
  for (int w = 0; w < kDwWhyCount; ++w) CHECK(DefendWhyName(w)[0] != '?');
  for (int e = 0; e < kDeEndCount; ++e) CHECK(DefendEndName(e)[0] != '?');
}
