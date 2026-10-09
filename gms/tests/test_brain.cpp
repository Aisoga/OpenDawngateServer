// gms/core/agents/brain.h (D1b-live step 3): gambit choice per phase type, cooldowns, condition lists, links.
#include <vector>

#include "core/agents/brain.h"
#include "tests/check.h"

using gms::agents::GambitView;

namespace {

GambitView G(bool pass, bool cooling = false, float weight = 1.f, bool wait = false) {
  GambitView g;
  g.conditionsPass = pass;
  g.coolingDown = cooling;
  g.weight = weight;
  g.waitOnCondition = wait;
  return g;
}

}  // namespace

GMS_TEST(GambitPrioritizedList) {
  using gms::agents::ChooseGambit;
  const int kList = gms::agents::kPrioritizedList;
  CHECK_EQ(ChooseGambit(kList, {}, nullptr, 0.5), -1);
  CHECK_EQ(ChooseGambit(kList, {G(false), G(true), G(true)}, nullptr, 0.5), 1);
  CHECK_EQ(ChooseGambit(kList, {G(true, true), G(true)}, nullptr, 0.5), 1);  // cooling down: falls through
  // waitOnCondition: a passing gambit that only cools down holds the list (the parasite's melee basic)
  CHECK_EQ(ChooseGambit(kList, {G(true, true, 1.f, true), G(true)}, nullptr, 0.5), -1);
  // ... but not when its condition fails
  CHECK_EQ(ChooseGambit(kList, {G(false, false, 1.f, true), G(true)}, nullptr, 0.5), 1);
  CHECK_EQ(ChooseGambit(kList, {G(false), G(false)}, nullptr, 0.5), -1);
}

GMS_TEST(GambitSequential) {
  using gms::agents::ChooseGambit;
  const int kSeq = gms::agents::kSequential;
  int cursor = -1;
  const std::vector<GambitView> all = {G(true), G(true), G(true)};
  CHECK_EQ(ChooseGambit(kSeq, all, &cursor, 0.0), 0);
  CHECK_EQ(ChooseGambit(kSeq, all, &cursor, 0.0), 1);
  CHECK_EQ(ChooseGambit(kSeq, all, &cursor, 0.0), 2);
  CHECK_EQ(ChooseGambit(kSeq, all, &cursor, 0.0), 0);  // wraps
  CHECK_EQ(ChooseGambit(kSeq, {G(true), G(false), G(true)}, &cursor, 0.0), 2);  // skips the unusable one
  CHECK_EQ(cursor, 2);
}

GMS_TEST(GambitRandomAndWeighted) {
  using gms::agents::ChooseGambit;
  const std::vector<GambitView> g = {G(true, false, 1.f), G(false, false, 5.f), G(true, false, 3.f)};
  // random: uniform among the usable (0 and 2)
  CHECK_EQ(ChooseGambit(gms::agents::kRandom, g, nullptr, 0.0), 0);
  CHECK_EQ(ChooseGambit(gms::agents::kRandom, g, nullptr, 0.49), 0);
  CHECK_EQ(ChooseGambit(gms::agents::kRandom, g, nullptr, 0.51), 2);
  // weighted: 1 against 3
  CHECK_EQ(ChooseGambit(gms::agents::kWeightedRandom, g, nullptr, 0.2), 0);
  CHECK_EQ(ChooseGambit(gms::agents::kWeightedRandom, g, nullptr, 0.3), 2);
  CHECK_EQ(ChooseGambit(gms::agents::kWeightedRandom, g, nullptr, 0.999), 2);
  CHECK_EQ(ChooseGambit(gms::agents::kRandom, {G(false), G(true, true)}, nullptr, 0.5), -1);
}

GMS_TEST(GambitCooldown) {
  using gms::agents::GambitCooldownSeconds;
  CHECK_EQ(GambitCooldownSeconds(1.f, 0.f, false, 0.7), 1.0);
  CHECK_EQ(GambitCooldownSeconds(1.f, 3.f, false, 0.7), 1.0);
  CHECK_EQ(GambitCooldownSeconds(1.f, 3.f, true, 0.5), 2.0);
  CHECK_EQ(GambitCooldownSeconds(2.f, 1.f, true, 0.5), 2.0);  // max below min: the cooldown
  CHECK_EQ(GambitCooldownSeconds(-1.f, 0.f, false, 0.0), 0.0);
}

GMS_TEST(ConditionLists) {
  using gms::agents::CombineConditions;
  using gms::agents::ConditionResult;
  auto C = [](bool pass, int op = 0, bool negate = false) {
    ConditionResult c;
    c.pass = pass;
    c.postfixOperator = op;
    c.negate = negate;
    return c;
  };
  CHECK(CombineConditions({}));
  CHECK(CombineConditions({C(true)}));
  CHECK(!CombineConditions({C(true, 0, true)}));
  CHECK(!CombineConditions({C(true, 1), C(false)}));  // and
  CHECK(CombineConditions({C(true, 2), C(false)}));   // or
  CHECK(CombineConditions({C(false, 2), C(false, 0, true)}));
  CHECK(!CombineConditions({C(true, 2), C(false, 1), C(false)}));  // (true or false) and false, left to right
}

GMS_TEST(LinkRules) {
  gms::agents::LinkView l;
  l.conditionsPass = true;
  l.secondsInNode = 2.0;
  CHECK(gms::agents::LinkFires(l));
  l.activateTime = 5.f;
  CHECK(!gms::agents::LinkFires(l));
  l.activateTime = 0.f;
  l.cooldownLeft = 0.5;
  CHECK(!gms::agents::LinkFires(l));
  l.cooldownLeft = 0;
  l.activateOnce = true;
  l.firedBefore = true;
  CHECK(!gms::agents::LinkFires(l));
  l.firedBefore = false;
  l.conditionsPass = false;
  CHECK(!gms::agents::LinkFires(l));
}

// D6a: the bots' plan layer (AGENT_AI.md *D6a*): link cooldowns, open links, the phase choice, the gambit slots.
GMS_TEST(PlanLinkCooldown) {
  CHECK(gms::agents::LinkCooldownSeconds(5.f, 0.f, 0.9) == 5.0);
  CHECK(gms::agents::LinkCooldownSeconds(0.f, 0.f, 0.9) == 0.0);
  CHECK(gms::agents::LinkCooldownSeconds(0.f, 240.f, 0.5) == 120.0);
  CHECK(gms::agents::LinkCooldownSeconds(10.f, 16.f, 0.0) == 10.0);
  CHECK(gms::agents::LinkCooldownSeconds(10.f, 16.f, 1.0) == 16.0);
}

GMS_TEST(PlanPhaseChoice) {
  using gms::agents::LinkView;
  auto L = [](bool pass, double cooldownLeft = 0) {
    LinkView l;
    l.conditionsPass = pass;
    l.cooldownLeft = cooldownLeft;
    l.secondsInNode = 1.0;
    return l;
  };
  // Main's phase links in order: 4 FleeTowerTargeting, 5 TowerPush (5 s cooldown), 2 CautiousLaning, 16 LastHit (true)
  const std::vector<int> outs = {4, 5, 2, 16};
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(false), L(false), L(false), L(true)}, outs, -1), 3);
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(false), L(true), L(false), L(true)}, outs, 16), 1);
  // in TowerPush its cooldown (started when it fired) doesn't push the bot out while the conditions hold
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(false), L(true, 4.0), L(false), L(true)}, outs, 5), 1);
  // after leaving TowerPush the cooldown keeps it out
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(false), L(true, 4.0), L(false), L(true)}, outs, 16), 3);
  // a higher link wins over the current phase
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(true), L(true, 4.0), L(false), L(true)}, outs, 5), 0);
  // nothing passes: keep the phase
  CHECK_EQ(gms::agents::ChoosePhaseLink({L(false), L(false)}, {4, 5}, 5), -1);
  // activateTime and activateOnce still apply to the current phase's link
  LinkView once = L(true);
  once.activateOnce = true;
  once.firedBefore = true;
  CHECK(!gms::agents::LinkOpen(once, true));
  LinkView late = L(true);
  late.activateTime = 3.f;
  CHECK(!gms::agents::LinkOpen(late, true));
}

GMS_TEST(PlanGambitSlots) {
  using namespace gms::agents;
  CHECK(SlotIsRanked(3) && SlotIsRanked(6) && !SlotIsRanked(0) && !SlotIsRanked(7));
  CHECK(SlotIsItem(7) && SlotIsItem(11) && !SlotIsItem(6) && !SlotIsItem(-1));
  CHECK(SlotIsConsumable(10) && SlotIsConsumable(11) && !SlotIsConsumable(9));
  CHECK_EQ(SlotInventoryIndex(7), 6);   // spell1 = inventory slot 6 (spells 6-8)
  CHECK_EQ(SlotInventoryIndex(10), 9);  // consume1 = inventory slot 9 (consumables 9-10)
  CHECK_EQ(SlotInventoryIndex(3), -1);
}

// D6d: what a bot's gambit aims at (AGENT_AI.md *D6d*; d6d_run1c's cases).
GMS_TEST(BotGambitAim) {
  using namespace gms::agents;
  const std::uint32_t mTarget = 0x7C7B012A, minion = 0x7C980175;
  // the ward: condition_Bot_Ward gives its spot and kObjIDNone with useTarget: no mTarget, a point; 5 pursues it
  const GambitAim ward = AimGambit(0, mTarget, true, true, true);
  CHECK_EQ(ward.target, 0u);
  CHECK(ward.atPoint);
  CHECK(PursuesPoint(ward, 5));
  CHECK(!PursuesPoint(ward, 1));
  CHECK(!PursuesPoint(ward, -9999));
  // D6a's rule ([host] bot_pursue_point=0): mTarget stands in, nothing is at a point alone
  const GambitAim old = AimGambit(0, mTarget, true, true, false);
  CHECK_EQ(old.target, mTarget);
  CHECK(!old.atPoint && !PursuesPoint(old, 5));
  // ... and with an invalid mTarget (GetBestTarget 0): no object, no point pursuit (the stuck bot)
  const GambitAim stuck = AimGambit(0, 0, true, true, false);
  CHECK(stuck.target == 0 && !PursuesPoint(stuck, 5));
  // condition_base_position (a point, no useTarget) with a valid mTarget: the object, as in D6a
  const GambitAim base = AimGambit(0, mTarget, true, false, true);
  CHECK_EQ(base.target, mTarget);
  CHECK(!base.atPoint);
  // ... with no valid mTarget: no object, and its point (a cast's direction) is not pursued (d6d_soak2c's Guardian)
  const GambitAim base0 = AimGambit(0, 0, true, false, true);
  CHECK(base0.target == 0 && !base0.atPoint && !PursuesPoint(base0, 5));
  // an object gambit (last hit: the minion and its position): the object, never a point
  const GambitAim hit = AimGambit(minion, mTarget, true, false, true);
  CHECK_EQ(hit.target, minion);
  CHECK(!hit.atPoint && !PursuesPoint(hit, 5));
  // a condition's object wins over a point-only one too
  CHECK_EQ(AimGambit(minion, mTarget, true, true, true).target, minion);
  // no point, no object: nothing to aim at
  const GambitAim none = AimGambit(0, 0, false, false, true);
  CHECK(none.target == 0 && !none.atPoint);
}
