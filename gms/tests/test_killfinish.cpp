// KILLFINISH (gms/core/agents/killfinish.h): a bot about to leave a fight stays when it wins the trade; the damage ring
// its rate comes from; BOTCYCLE's pass-through of a plan whose link fires in the think it was entered; the tower dive
// a finish allows (reflex.h).
#include "core/agents/killfinish.h"
#include "core/agents/reflex.h"
#include "tests/check.h"

using namespace gms::agents;

namespace {
// A low target 10 units away, the bot at 20 % of 1000 HP dealing 150 per second to shapers, nobody hitting it.
KillFinishSensors Duel() {
  KillFinishSensors s;
  s.target = true;
  s.selfHp = 200.f;
  s.selfMaxHp = 1000.f;
  s.targetHp = 240.f;
  s.targetMaxHp = 1200.f;
  s.dist = 14.f;
  s.dpsShapers = 150.f;
  s.dpsAll = 150.f;
  return s;
}
}  // namespace

GMS_TEST(KillfinishTheUsersCase) {
  // the user's report: the bot's own HP is lower than the target's (20 % against 20 %, BailOnKill bails), but the
  // target dies in under 2 s and nothing hits the bot
  const KillFinishDecision d = DecideKillFinish(Duel());
  CHECK(d.finish);
  CHECK_EQ(d.why, static_cast<int>(kKfFinish));
  CHECK(d.ttk > 1.8f && d.ttk < 1.9f);  // 240 / 150 + (14 - 12) / 9
  CHECK(d.ttd > 1.0e8f);
  CHECK_EQ(std::string(KillFinishWhyName(d.why)), std::string("finish"));
}

GMS_TEST(KillfinishTheTrade) {
  KillFinishSensors s = Duel();
  s.takenDps = 60.f;  // 200 HP: dies in 3.33 s; 1.5 x 1.82 = 2.73: still wins
  CHECK(DecideKillFinish(s).finish);
  s.takenDps = 80.f;  // 2.5 s < 2.73: loses
  KillFinishDecision d = DecideKillFinish(s);
  CHECK(!d.finish);
  CHECK_EQ(d.why, static_cast<int>(kKfLosing));
  s.takenDps = 0.f;
  s.targetHp = 700.f;  // 58 %: not a finish
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfTargetHealthy));
  s.targetHp = 400.f;  // 33 %, 400 / 150 + 0.22 = 2.9 s: fine
  CHECK(DecideKillFinish(s).finish);
  s.dpsShapers = 70.f;  // 5.94 s: too slow
  s.dpsAll = 70.f;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfTooSlow));
  s.dpsShapers = 0.f;  // only minions hit lately: half their rate counts
  s.dpsAll = 400.f;
  CHECK(DecideKillFinish(s).finish);
  s.dpsAll = 1.f;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfNoDamage));
}

GMS_TEST(KillfinishReachNumbersAndHold) {
  KillFinishSensors s = Duel();
  s.dist = 31.f;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfFar));
  s.dist = 23.4f;  // live round 1: the HighHuntress let a Desecrator at 2 % go 23.4 away; 1.6 s + 11.4 / 9 s of walking
  KillFinishDecision d = DecideKillFinish(s);
  CHECK(d.finish && d.ttk > 2.86f && d.ttk < 2.88f);
  s.dist = 20.f;
  s.enemiesNear = 1;  // a second enemy, no ally
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfOutnumbered));
  s.alliesNear = 1;
  CHECK(DecideKillFinish(s).finish);
  s.heldMs = 4999;
  CHECK(DecideKillFinish(s).finish);
  s.heldMs = 5000;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfHoldOver));
  s.heldMs = -1;
  s.targetVisible = false;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfNoTarget));
  s.targetVisible = true;
  s.selfHp = 0.f;
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfNoTarget));
  KillFinishSensors none;
  CHECK(!DecideKillFinish(none).finish);
}

GMS_TEST(KillfinishUnderATower) {
  KillFinishSensors s = Duel();
  s.underTower = true;  // 1.82 s > 1.5 s: not near-certain
  KillFinishDecision d = DecideKillFinish(s);
  CHECK(!d.finish);
  CHECK_EQ(d.why, static_cast<int>(kKfTower));
  s.targetHp = 120.f;  // 0.8 + 0.22 = 1.02 s; the tower's assumed 100/s: dies in 2 s < 2.5 x 1.02
  d = DecideKillFinish(s);
  CHECK(!d.finish && d.why == kKfTower);
  s.selfHp = 300.f;  // 3 s >= 2.55 s
  CHECK(DecideKillFinish(s).finish);
  s.towerHitting = true;  // its damage is in the measured rate already
  s.takenDps = 150.f;     // 2 s < 2.55 s
  CHECK_EQ(DecideKillFinish(s).why, static_cast<int>(kKfTower));
}

GMS_TEST(KillfinishTheDamageRing) {
  KillFinishDealt r;
  float sh = -1.f, all = -1.f;
  r.Rates(10000, 4000, 2000, &sh, &all);
  CHECK(sh == 0.f && all == 0.f);
  r.Note(100.f, true, 9000);
  r.Note(50.f, false, 9500);
  r.Note(0.f, true, 9600);  // nothing dealt: not a hit
  CHECK_EQ(r.n, 2);
  r.Rates(10000, 4000, 2000, &sh, &all);  // 1 s since the first hit: the 2-s floor
  CHECK(sh == 50.f && all == 75.f);
  r.Note(150.f, true, 7000);  // 3 s back
  r.Rates(10000, 4000, 2000, &sh, &all);
  CHECK(sh > 83.3f && sh < 83.4f && all == 100.f);
  r.Rates(12500, 4000, 2000, &sh, &all);  // the 7000 hit left the window
  CHECK(sh > 28.5f && sh < 28.6f);        // 100 over 3.5 s
  for (int i = 0; i < 40; ++i) r.Note(1.f, false, 13000 + i);
  CHECK_EQ(r.n, KillFinishDealt::kHits);
}

GMS_TEST(KillfinishTheStanceDwellAndTheJoinBlock) {
  // Lane -> Lane_Cautious -> Lane within 1.2 s: held; leaving a fight or fleeing never is
  CHECK(PlanBounceHeld(true, 230, 1200, false, false));
  CHECK(!PlanBounceHeld(true, 1200, 1200, false, false));
  CHECK(!PlanBounceHeld(false, 230, 1200, false, false));  // not back into the plan left
  CHECK(!PlanBounceHeld(true, 230, 1200, true, false));    // EngageForKill -> Lane: BailOnKill's exit is not delayed
  CHECK(!PlanBounceHeld(true, 230, 1200, false, true));    // into Flee or a fight
  CHECK(!PlanBounceHeld(true, 230, 0, false, false));      // off
  CHECK(EngageBlocked(1000, 9000));
  CHECK(!EngageBlocked(9000, 9000));
  CHECK(!EngageBlocked(1000, 0));
}

GMS_TEST(KillfinishThePlanChain) {
  const int visited[3] = {17, 11, -1};  // EngageForKill -> Lane in this think
  CHECK(PlanChainAllows(visited, 1, 17, 0, 2));   // the think's first change: anything
  CHECK(PlanChainAllows(visited, 2, 15, 1, 2));   // Lane -> Cautious, passed through
  CHECK(!PlanChainAllows(visited, 2, 17, 1, 2));  // never back into the EngageForKill it left
  CHECK(!PlanChainAllows(visited, 2, 11, 1, 2));
  CHECK(!PlanChainAllows(visited, 2, 15, 2, 2));  // two changes per think at most
}

GMS_TEST(KillfinishTheTowerDive) {
  // BOTREFLEX's tower back-off spares a bot finishing a won trade (the decision's own tower test holds)
  ReflexSensors s;
  s.role = 0;
  s.hpFrac = 0.2f;
  s.inTowerZone = true;
  s.targetIsShaper = true;
  s.targetHpFrac = 0.1f;
  s.targetInTowerZone = true;
  CHECK_EQ(DecideReflex(s).kind, kReflexBack);  // below diveSelfHp
  s.finishing = true;
  CHECK_EQ(DecideReflex(s).kind, kReflexNone);
  ReflexConfig early;
  early.noShaperDive = true;  // the early Desecrator still backs off
  CHECK_EQ(DecideReflex(s, early).kind, kReflexBack);
}
