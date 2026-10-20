// CAMPFINISH (gms/core/agents/campfinish.h): a Hunter below the Flee threshold finishes the jungle camp he wins
// against: the fight's rates from the samples, the decision's reasons, and whole fights stepped at the bots' thinks.
#include <vector>

#include "core/agents/campfinish.h"
#include "core/agents/leash.h"
#include "tests/check.h"

using namespace gms::agents;

namespace {

bool NearF(float a, float b, float tol) { return a > b - tol && a < b + tol; }

// A sample every 200 ms for `seconds`: the bot's HP falls by `taken`/s, member 1's by `dealt`/s (from the given HP).
void Fight(CampFightTrack* t, long long fromMs, float seconds, float botHp, float taken, float campHp, float dealt,
           const CampFinishConfig& c) {
  for (long long ms = 0; ms <= static_cast<long long>(seconds * 1000.f); ms += 200) {
    const float s = static_cast<float>(ms) / 1000.f;
    CampMemberHp m[2] = {{1u, campHp - dealt * s}, {2u, 500.f}};
    t->Note(fromMs + ms, botHp - taken * s, 7, m, 2, c);
  }
}

CampFinishInputs Winning() {
  CampFinishInputs in;
  in.hp = 450.f;  // Petrus at 30 % of 1,500
  in.maxHp = 1500.f;
  in.campAlive = 1;
  in.campHp = 400.f;
  in.campMaxHp = 2900.f;
  in.haveRates = true;
  in.spanMs = 4000;
  in.dealtPerS = 80.f;  // kill in 5 s
  in.takenPerS = 40.f;  // die in 11.25 s >= 1.5 * 5 + 1.5 = 9
  return in;
}

}  // namespace

GMS_TEST(CampfinishTrackRates) {
  const CampFinishConfig c;
  CampFightTrack t;
  float dealt = 0.f, taken = 0.f;
  long long span = 0;
  CHECK(!t.Rates(c, &dealt, &taken, &span));  // nothing yet
  Fight(&t, 10000, 3.f, 600.f, 40.f, 1000.f, 80.f, c);
  CHECK_EQ(t.Camp(), 7);
  CHECK(t.Rates(c, &dealt, &taken, &span));
  CHECK_EQ(span, 3000LL);
  CHECK(NearF(dealt, 80.f, 0.5f) && NearF(taken, 40.f, 0.5f));
  // the window: 10 s more of a slower fight, the rates are the last 4 s only
  Fight(&t, 13200, 10.f, 480.f, 10.f, 760.f, 20.f, c);
  CHECK(t.Rates(c, &dealt, &taken, &span));
  CHECK_EQ(span, 4000LL);
  CHECK(NearF(dealt, 20.f, 0.5f) && NearF(taken, 10.f, 0.5f));
  // healing (a potion) makes the net loss negative
  CampFightTrack h;
  Fight(&h, 0, 2.f, 300.f, -25.f, 900.f, 60.f, c);
  CHECK(h.Rates(c, &dealt, &taken, &span) && taken < 0.f && NearF(taken, -25.f, 0.5f));
}

GMS_TEST(CampfinishTrackMembersAndResets) {
  const CampFinishConfig c;
  CampFightTrack t;
  float dealt = 0.f, taken = 0.f;
  long long span = 0;
  // member 1 at 100 dies and is pruned between two samples: its 100 HP are damage the camp took
  CampMemberHp a[2] = {{1u, 100.f}, {2u, 800.f}};
  CampMemberHp b[1] = {{2u, 750.f}};
  t.Note(0, 500.f, 3, a, 2, c);
  t.Note(1000, 500.f, 3, b, 1, c);
  t.Note(2000, 480.f, 3, b, 1, c);
  CHECK(t.Rates(c, &dealt, &taken, &span));
  CHECK(NearF(dealt, 75.f, 0.01f) && NearF(taken, 10.f, 0.01f));  // (100 + 50) / 2 s, 20 / 2 s
  // a dead member listed at 0 HP counts the same; a creature that heals (a leash reset) is no damage
  CampFightTrack u;
  CampMemberHp d0[1] = {{5u, 200.f}}, d1[1] = {{5u, 0.f}}, d2[1] = {{6u, 300.f}};
  u.Note(0, 500.f, 4, d0, 1, c);
  u.Note(1000, 500.f, 4, d1, 1, c);
  CampMemberHp heal0[1] = {{6u, 100.f}}, heal1[1] = {{6u, 400.f}};
  u.Note(1500, 500.f, 4, heal0, 1, c);  // member 6 joins (no damage counted for a new id)
  u.Note(2000, 500.f, 4, heal1, 1, c);  // and heals
  CHECK(u.Rates(c, &dealt, &taken, &span));
  CHECK(NearF(dealt, 100.f, 0.01f));  // 200 over 2 s
  (void)d2;
  // another camp starts the history again
  t.Note(2200, 470.f, 9, b, 1, c);
  CHECK_EQ(t.Camp(), 9);
  CHECK_EQ(t.Count(), 1);
  CHECK(!t.Rates(c, &dealt, &taken, &span));
  // so does a gap longer than maxGapMs, and a clock that went back
  t.Note(2400, 470.f, 9, b, 1, c);
  CHECK_EQ(t.Count(), 2);
  t.Note(2400 + c.maxGapMs + 1, 460.f, 9, b, 1, c);
  CHECK_EQ(t.Count(), 1);
  t.Note(100, 460.f, 9, b, 1, c);
  CHECK_EQ(t.Count(), 1);
  CHECK_EQ(t.LastMs(), 100LL);
  // one sample per clock tick: the same ms replaces the last one
  t.Note(100, 450.f, 9, b, 1, c);
  CHECK_EQ(t.Count(), 1);
  // under minSpanMs of fight: no rates
  t.Note(100 + c.minSpanMs - 1, 440.f, 9, b, 1, c);
  CHECK(!t.Rates(c, &dealt, &taken, &span));
  t.Reset();
  CHECK_EQ(t.Camp(), -1);
  CHECK_EQ(t.LastMs(), -1LL);
}

GMS_TEST(CampfinishDecisionReasons) {
  const CampFinishConfig c;
  CampFinishDecision d = DecideCampFinish(Winning(), c);
  CHECK(d.stay && d.why == CampFinishWhy::kStayWinning);
  CHECK(NearF(d.killS, 5.f, 1e-3f) && NearF(d.dieS, 11.25f, 1e-3f));
  // the margin: dying in 7.5 s against a 5-s kill (needs 9) loses the trade
  CampFinishInputs in = Winning();
  in.takenPerS = 60.f;
  d = DecideCampFinish(in, c);
  CHECK(!d.stay && d.why == CampFinishWhy::kLosing && NearF(d.dieS, 7.5f, 1e-3f));
  // just at the line: die 9 s = 1.5 * 5 + 1.5 stays
  in.takenPerS = 50.f;
  CHECK(DecideCampFinish(in, c).stay);
  // not losing HP (regeneration, a potion): stays
  in = Winning();
  in.takenPerS = -5.f;
  d = DecideCampFinish(in, c);
  CHECK(d.stay && d.why == CampFinishWhy::kStayUnhurt && d.dieS < 0.f);
  // every reason to leave, in order
  in = Winning();
  in.campAlive = 0;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kNoCamp);
  in = Winning();
  in.campHp = 0.f;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kNoCamp);
  in = Winning();
  in.enemyShaperNear = true;
  CHECK(!DecideCampFinish(in, c).stay && DecideCampFinish(in, c).why == CampFinishWhy::kEnemyShaper);
  in = Winning();
  in.hp = 140.f;  // 9.3 % < 10 %
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kTooLow);
  in.hp = 0.f;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kTooLow);
  in = Winning();
  in.maxHp = 0.f;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kTooLow);
  in = Winning();
  in.heldMs = c.maxHoldMs + 1;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kHeldTooLong);
  in.heldMs = c.maxHoldMs;
  CHECK(DecideCampFinish(in, c).stay);
  in = Winning();
  in.haveRates = false;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kNotMeasured);
  in = Winning();
  in.dealtPerS = 0.f;
  CHECK(DecideCampFinish(in, c).why == CampFinishWhy::kNoDamage);
  // HUNTERBOT round 2's Faris: about 700 of an Ugger Medium left at 62/s is an 11-s kill, and he dies in 6: left
  in = Winning();
  in.hp = 180.f;
  in.maxHp = 600.f;
  in.campHp = 700.f;
  in.dealtPerS = 62.f;
  in.takenPerS = 31.f;
  d = DecideCampFinish(in, c);
  CHECK(!d.stay && d.why == CampFinishWhy::kLosing && d.killS < c.maxKillS);
  // a kill beyond maxKillS (15 s) is left even unhurt
  in.campHp = 1000.f;
  in.takenPerS = 0.f;
  d = DecideCampFinish(in, c);
  CHECK(!d.stay && d.why == CampFinishWhy::kSlowKill && d.killS > c.maxKillS);
  for (int w = 0; w < static_cast<int>(CampFinishWhy::kCount); ++w)
    CHECK(CampFinishWhyName(static_cast<CampFinishWhy>(w))[0] != '?');
}

// Whole fights at the bots' 200-ms thinks: a Hunter (1,500 max HP) against a camp of three creatures. He deals `dps`
// to the lowest living one; each living creature deals `creepDps` to him. The data's rule leaves below 30 %; the
// relative rule holds the link while DecideCampFinish says stay. Returns the camp's HP left when he went (0: finished)
// and his HP then.
struct FightResult {
  float campLeft = 0.f;
  float botHp = 0.f;
  bool died = false;
};

FightResult RunFight(bool relative, float startHp, float dps, float creepDps, const CampFinishConfig& c) {
  float hp = startHp;
  std::vector<float> creeps = {900.f, 600.f, 600.f};
  CampFightTrack t;
  long long holdSince = -1;
  for (long long ms = 0; ms < 120000; ms += 200) {
    const float dt = 0.2f;
    int alive = 0;
    float campHp = 0.f;
    for (float x : creeps)
      if (x > 0.f) {
        ++alive;
        campHp += x;
      }
    if (alive == 0) return FightResult{0.f, hp, false};
    // the think: sample, then the Flee link (HealthLow below 30 %)
    CampMemberHp m[3];
    for (int i = 0; i < 3; ++i) m[i] = CampMemberHp{static_cast<std::uint32_t>(i + 1), creeps[i] > 0.f ? creeps[i] : 0.f};
    t.Note(ms, hp, 1, m, 3, c);
    if (hp / 1500.f < 0.3f) {
      bool stay = false;
      if (relative) {
        CampFinishInputs in;
        in.hp = hp;
        in.maxHp = 1500.f;
        in.campAlive = alive;
        in.campHp = campHp;
        in.campMaxHp = 2100.f;
        in.haveRates = t.Rates(c, &in.dealtPerS, &in.takenPerS, &in.spanMs);
        in.heldMs = holdSince >= 0 ? ms - holdSince : 0;
        stay = DecideCampFinish(in, c).stay;
        if (stay && holdSince < 0) holdSince = ms;
      }
      if (!stay) return FightResult{campHp, hp, false};
    }
    // the fight
    for (float& x : creeps)
      if (x > 0.f) {
        x -= dps * dt;
        break;
      }
    hp -= creepDps * static_cast<float>(alive) * dt;
    if (hp <= 0.f) return FightResult{campHp, 0.f, true};
  }
  return FightResult{-1.f, hp, false};
}

GMS_TEST(CampfinishWholeFights) {
  const CampFinishConfig c;
  // a clear he wins: 90 dps against 3 x 12, from 1,000 HP. The data's rule leaves at 17.9 s with under a quarter of
  // the camp left (the last creature at about 490), the relative rule finishes it with HP to spare.
  FightResult old = RunFight(false, 1000.f, 90.f, 12.f, c);
  FightResult now = RunFight(true, 1000.f, 90.f, 12.f, c);
  CHECK(old.campLeft > 0.f && old.campLeft < 0.25f * 2100.f);
  CHECK_EQ(now.campLeft, 0.f);
  CHECK(!now.died && now.botHp > 0.1f * 1500.f);
  // a clear he loses: 40 dps against 3 x 30. Both leave (the relative rule as early as the data's), nobody dies.
  old = RunFight(false, 1500.f, 40.f, 30.f, c);
  now = RunFight(true, 1500.f, 40.f, 30.f, c);
  CHECK(old.campLeft > 0.f && now.campLeft > 0.f && !now.died);
  CHECK(NearF(now.botHp, old.botHp, 1.f));
  // a close one: never below the floor, never dead
  for (float creep = 10.f; creep <= 60.f; creep += 5.f) {
    now = RunFight(true, 1500.f, 70.f, creep, c);
    CHECK(!now.died && now.botHp >= c.minHpFrac * 1500.f - 40.f);
  }
}

GMS_TEST(CampfinishFinishingAndTheLooserLeash) {
  const CampStayConfig c;
  // the second round's leaves: healthy Hunters, one creature left at 1-23 % of the camp
  CHECK(CampFinishingWanted(1, 0.09f, false, 0.f, 140.f, 0.76f, false, 0, c));
  CHECK(CampFinishingWanted(1, 0.23f, true, 90.f, 653.f, 0.74f, false, 5000, c));
  // a fuller camp only when the measured kill is near: 600 at 120/s = 5 s yes, at 60/s = 10 s no
  CHECK(CampFinishingWanted(2, 0.6f, true, 120.f, 600.f, 0.8f, false, 0, c));
  CHECK(!CampFinishingWanted(2, 0.6f, true, 60.f, 600.f, 0.8f, false, 0, c));
  CHECK(!CampFinishingWanted(2, 0.6f, false, 0.f, 600.f, 0.8f, false, 0, c));
  // not with an enemy shaper near, not low himself (the Flee rule decides), not for longer than maxMs, not a dead camp
  CHECK(!CampFinishingWanted(1, 0.1f, false, 0.f, 100.f, 0.8f, true, 0, c));
  CHECK(!CampFinishingWanted(1, 0.1f, false, 0.f, 100.f, 0.3f, false, 0, c));
  CHECK(!CampFinishingWanted(1, 0.1f, false, 0.f, 100.f, 0.8f, false, c.maxMs + 1, c));
  CHECK(CampFinishingWanted(1, 0.1f, false, 0.f, 100.f, 0.8f, false, c.maxMs, c));
  CHECK(!CampFinishingWanted(0, 0.0f, false, 0.f, 0.f, 0.8f, false, 0, c));
  // the host's creep leash since this round: own 18, target 22 (the engine's tether for a jungle creature), the chase
  // clock from 10 out for 5 s. The second round's own-leash drops at 15.0-15.2 and chase drops at 11-14 keep now.
  LeashOptions o;
  o.ownLimit = 18.f;
  o.targetMargin = 4.f;
  o.homeRadius = 10.f;
  o.chaseMs = 5000;
  LeashState st;
  UpdateLeash(&st, o, 7u, 15.2f, 0);
  CHECK_EQ(LeashSelfCheck(o, st, 15.2f, 3500), static_cast<int>(kLeashKeep));  // 3.5 s without a hit: kept (was chase)
  CHECK_EQ(LeashSelfCheck(o, st, 15.2f, 5000), static_cast<int>(kLeashChase));
  CHECK_EQ(LeashSelfCheck(o, st, 18.5f, 100), static_cast<int>(kLeashOwn));
  CHECK(!LeashTargetBeyond(o, 21.9f) && LeashTargetBeyond(o, 22.1f));
  LeashState in;
  UpdateLeash(&in, o, 7u, 9.f, 0);  // inside the home radius: no chase clock
  CHECK_EQ(LeashSelfCheck(o, in, 9.f, 60000), static_cast<int>(kLeashKeep));
}
