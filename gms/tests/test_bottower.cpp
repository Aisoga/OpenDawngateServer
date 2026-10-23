// BOTTOWER (gms/core/agents/towersafety.h): when an enemy tower is a danger to a bot, which targets it leaves alone, the
// retreat step's choice against another walk (REFLEXWALK) and the measurement of the time under tower fire; and how
// BOTREFLEX's rule 1 (reflex.h) takes the new dangers.
#include "core/agents/reflex.h"
#include "core/agents/towersafety.h"
#include "tests/check.h"

using namespace gms::agents;

namespace {
// A Binding (aggro 28) 20 units from the bot, none of the bot's minions in range, nothing recent.
TowerView Binding(float dist = 20.f) {
  TowerView t;
  t.dist = dist;
  t.aggro = 28.f;
  return t;
}
}  // namespace

GMS_TEST(BotTowerDangerKinds) {
  TowerSafetyConfig c;
  TowerView t = Binding();
  t.minionsIn = 3;  // minions tank: no danger at 20
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerSafe));
  t.minionsIn = 0;  // nothing else to shoot: exposed within 28 + 2
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerExposed));
  t.dist = 30.5f;  // just beyond aggro + exposedMargin
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerSafe));
  t.targetsMe = true;  // targeted, but beyond 28 + 2: the tower keeps it on its list without reaching it
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerSafe));
  t.dist = 29.5f;  // within 28 + 2: a danger, minions or not
  t.minionsIn = 3;
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerTargets));
  t.minionsIn = 0;
  t.hitAgeMs = 2000;  // a hit 2 s ago: within 34 (BOTREFLEX's reach)
  t.dist = 33.f;
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerHit));
  t.hitAgeMs = 9000;  // the hit too old; targeted at 33: beyond the tower's reach
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerSafe));
  t.dist = 29.f;
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerTargets));
  // the Locus (aggro 38.5): a hit counts to 44.5, not just 34
  TowerView l = Binding(40.f);
  l.aggro = 38.5f;
  l.minionsIn = 2;
  l.hitAgeMs = 1000;
  CHECK_EQ(static_cast<int>(TowerDanger(l, c)), static_cast<int>(kTowerHit));
  // bot_tower_aware=0: BOTREFLEX's rule alone (a hit within 34)
  c.aware = false;
  CHECK_EQ(static_cast<int>(TowerDanger(l, c)), static_cast<int>(kTowerSafe));
  l.dist = 33.f;
  CHECK_EQ(static_cast<int>(TowerDanger(l, c)), static_cast<int>(kTowerHit));
  t = Binding();
  t.targetsMe = true;
  CHECK_EQ(static_cast<int>(TowerDanger(t, c)), static_cast<int>(kTowerSafe));
  // a structure that doesn't shoot (aggro 0) is a danger only by a hit
  c.aware = true;
  TowerView nub = Binding(5.f);
  nub.aggro = 0.f;
  CHECK_EQ(static_cast<int>(TowerDanger(nub, c)), static_cast<int>(kTowerSafe));
  nub.hitAgeMs = 100;
  CHECK_EQ(static_cast<int>(TowerDanger(nub, c)), static_cast<int>(kTowerHit));
}

GMS_TEST(BotTowerWorstDangerFirst) {
  TowerSafetyConfig c;
  TowerView v[3] = {Binding(10.f), Binding(25.f), Binding(29.f)};
  v[0].minionsIn = 4;     // safe
  v[1].minionsIn = 0;     // exposed
  v[2].targetsMe = true;  // targeted: more urgent than exposed though farther
  v[2].minionsIn = 1;
  TowerDangerKind k = kTowerSafe;
  CHECK_EQ(WorstTowerDanger(v, 3, c, &k), 2);
  CHECK_EQ(static_cast<int>(k), static_cast<int>(kTowerTargets));
  v[2].targetsMe = false;
  CHECK_EQ(WorstTowerDanger(v, 3, c, &k), 1);
  CHECK_EQ(static_cast<int>(k), static_cast<int>(kTowerExposed));
  v[1].minionsIn = 2;
  CHECK_EQ(WorstTowerDanger(v, 3, c, &k), -1);
  CHECK_EQ(static_cast<int>(k), static_cast<int>(kTowerSafe));
}

GMS_TEST(BotTowerKeepOutRules) {
  TowerSafetyConfig c;
  TowerView t = Binding(40.f);  // the bot itself outside the range
  KeepOutTarget g;
  g.distToTower = 20.f;  // a minion under the tower
  t.minionsIn = 3;       // the bot's minions there, the tower never shot it: farming is fine
  CHECK(!TowerKeepsOut(t, g, 0.8f, c));
  t.hitAgeMs = 5000;  // it shot the bot 5 s ago: out of bounds
  CHECK(TowerKeepsOut(t, g, 0.8f, c));
  t.hitAgeMs = 9000;  // long ago
  CHECK(!TowerKeepsOut(t, g, 0.8f, c));
  t.targetedAgeMs = 3000;  // targeted 3 s ago
  CHECK(TowerKeepsOut(t, g, 0.8f, c));
  t.targetedAgeMs = -1;
  t.minionsIn = 0;  // nothing else to shoot
  CHECK(TowerKeepsOut(t, g, 0.8f, c));
  g.distToTower = 29.f;  // the target just outside the tower's range
  CHECK(!TowerKeepsOut(t, g, 0.8f, c));
  // an enemy shaper under the tower: a dive only when it is low and the bot healthy, or KILLFINISH finishes it
  g.distToTower = 15.f;
  g.isShaper = true;
  g.shaperHpFrac = 0.6f;
  CHECK(TowerKeepsOut(t, g, 0.9f, c));
  g.shaperHpFrac = 0.3f;
  CHECK(!TowerKeepsOut(t, g, 0.9f, c));
  CHECK(TowerKeepsOut(t, g, 0.3f, c));  // too hurt to dive
  g.shaperHpFrac = 0.6f;
  g.finishing = true;
  CHECK(!TowerKeepsOut(t, g, 0.3f, c));
  KeepOutRules noDive;
  noDive.noShaperDive = true;  // DESECBOT-3's early Desecrator
  CHECK(TowerKeepsOut(t, g, 0.9f, c, noDive));
  // the tower itself: finished when worn down and the bot in shape, else left alone without minions
  KeepOutTarget tw;
  tw.distToTower = 0.f;
  tw.isThatTower = true;
  tw.towerHpFrac = 0.8f;
  CHECK(TowerKeepsOut(t, tw, 0.9f, c));
  tw.towerHpFrac = 0.4f;
  CHECK(!TowerKeepsOut(t, tw, 0.9f, c));
  CHECK(TowerKeepsOut(t, tw, 0.4f, c));
  t.targetsMe = true;  // ... but while it shoots the bot only a tower at 20 % or less (two bots died finishing at 50 %)
  CHECK(TowerKeepsOut(t, tw, 0.9f, c));
  tw.towerHpFrac = 0.15f;
  CHECK(!TowerKeepsOut(t, tw, 0.9f, c));
  t.targetsMe = false;
  tw.towerHpFrac = 0.4f;
  t.minionsIn = 2;  // minions tank: siege on
  tw.towerHpFrac = 0.8f;
  CHECK(!TowerKeepsOut(t, tw, 0.9f, c));
  // bot_tower_keepout=0: never
  c.keepOut = false;
  t.minionsIn = 0;
  CHECK(!TowerKeepsOut(t, tw, 0.9f, c));
}

GMS_TEST(BotTowerReflexTakesTheNewDangers) {
  ReflexSensors s;
  s.role = 0;
  s.inTowerZone = true;
  s.towerKind = kTowerTargets;
  ReflexDecision d = DecideReflex(s);
  CHECK_EQ(static_cast<int>(d.kind), static_cast<int>(kReflexBack));
  CHECK_EQ(std::string(d.why), std::string("tower targets it"));
  s.towerKind = kTowerExposed;
  CHECK_EQ(std::string(DecideReflex(s).why), std::string("tower, no minions"));
  s.towerKind = kTowerHit;
  CHECK_EQ(std::string(DecideReflex(s).why), std::string("tower"));
  // the dive still holds for every kind
  s.towerKind = kTowerExposed;
  s.targetIsShaper = true;
  s.targetInTowerZone = true;
  s.targetHpFrac = 0.2f;
  s.hpFrac = 0.9f;
  CHECK_EQ(static_cast<int>(DecideReflex(s).kind), static_cast<int>(kReflexNone));
  // finishing a tower: at 40 % while it targets the bot only with the host's stricter threshold (bot_tower_aware)
  ReflexSensors f;
  f.inTowerZone = true;
  f.towerKind = kTowerTargets;
  f.targetIsTower = true;
  f.targetTowerHpFrac = 0.4f;
  f.hpFrac = 0.9f;
  ReflexConfig rc;
  CHECK_EQ(static_cast<int>(DecideReflex(f, rc).kind), static_cast<int>(kReflexNone));  // BOTREFLEX's 50 %
  rc.finishTowerShotHp = 0.2f;
  CHECK_EQ(static_cast<int>(DecideReflex(f, rc).kind), static_cast<int>(kReflexBack));
  f.towerKind = kTowerExposed;  // not yet shot: the 50 % rule
  CHECK_EQ(static_cast<int>(DecideReflex(f, rc).kind), static_cast<int>(kReflexNone));
  f.towerKind = kTowerHit;
  f.targetTowerHpFrac = 0.15f;
  CHECK_EQ(static_cast<int>(DecideReflex(f, rc).kind), static_cast<int>(kReflexNone));
  // a Hunter under fire: the tower first, before the retaliation
  ReflexSensors h;
  h.role = kReflexRoleHunter;
  h.inTowerZone = true;
  h.towerKind = kTowerTargets;
  h.shaperHitAgeMs = 500;
  CHECK_EQ(static_cast<int>(DecideReflex(h).kind), static_cast<int>(kReflexBack));
  CHECK(DecideReflex(h).why[0] == 't');
}

GMS_TEST(BotTowerReflexWalkChoice) {
  // started: the step runs
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(1, false, 0, 0, -1)), static_cast<int>(kHeldWalkStarted));
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(2, false, 0, 0, -1)), static_cast<int>(kHeldWalkStarted));
  // refused otherwise: the caller's
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(0, false, 0, 0, -1)), static_cast<int>(kHeldWalkOther));
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(5, false, 0, 0, -1)), static_cast<int>(kHeldWalkOther));
  // held: its own step asked for 200 ms ago -> adopt it; then the same instance goes on
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, true, 0x77, 0, 200)), static_cast<int>(kHeldWalkAdopt));
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, true, 0x77, 0x77, 2000)), static_cast<int>(kHeldWalkOwn));
  // held by another walk: the plan's (never asked, or asked long ago, or another instance than its own) -> end it
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, true, 0x55, 0, -1)), static_cast<int>(kHeldWalkEnd));
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, true, 0x55, 0, 4000)), static_cast<int>(kHeldWalkEnd));
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, true, 0x55, 0x77, 200)), static_cast<int>(kHeldWalkEnd));
  // the holder unknown: treated as its own (nothing to end)
  CHECK_EQ(static_cast<int>(ChooseHeldWalk(kHeldWalkResultHolds, false, 0, 0, -1)), static_cast<int>(kHeldWalkOwn));
  CHECK_EQ(kHeldWalkResultHolds, -0x270B);
}

GMS_TEST(BotTowerExposureEpisodes) {
  TowerExposureTrack tr;
  TowerEpisode e;
  CHECK(!tr.Frame(0, 0, 0, 1.f, 0.f, false, 1500, &e));  // nothing
  CHECK(!tr.open());
  CHECK(!tr.Shot(7, false));                             // a shot with no episode open: not counted in one
  CHECK(!tr.Frame(1000, 0, 7, 0.9f, 25.f, false, 1500, &e));  // tower 7 targets it
  CHECK(tr.open());
  tr.Think(kTtNotSeen);
  CHECK(tr.Shot(7, false));
  CHECK(!tr.Shot(8, false));  // another tower's shot
  CHECK(!tr.Frame(1100, 1000, 7, 0.8f, 26.f, false, 1500, &e));
  tr.Think(kTtBackBlind);
  CHECK(!tr.Frame(1200, 1100, 7, 0.7f, 27.f, false, 1500, &e));
  CHECK(tr.Shot(7, false));
  CHECK(!tr.Frame(2000, 1200, 0, 0.7f, 0.f, false, 1500, &e));  // not targeted: the gap is not over yet
  CHECK(tr.Frame(2800, 2000, 0, 0.7f, 0.f, false, 1500, &e));   // 1.6 s after the last: ended
  CHECK(!tr.open());
  CHECK_EQ(e.tower, 7u);
  CHECK_EQ(e.shots, 2);
  CHECK_EQ(e.DurationMs(), 200ll);
  CHECK_EQ(e.targetedMs, 200ll);
  CHECK(!e.died);
  CHECK_EQ(e.thinks[kTtNotSeen], 1);
  CHECK_EQ(e.thinks[kTtBackBlind], 1);
  CHECK(e.hpStart > 0.89f && e.hpEnd < 0.71f);
  // a death ends it as died; another tower taking over ends the first and opens the second
  CHECK(!tr.Frame(5000, 4900, 7, 0.2f, 20.f, false, 1500, &e));
  CHECK(tr.Shot(7, true));
  CHECK(tr.Frame(5100, 5000, 0, 0.f, 0.f, true, 1500, &e));
  CHECK(e.died && e.lethal);
  CHECK(!tr.Frame(6000, 5900, 7, 0.5f, 20.f, false, 1500, &e));
  CHECK(tr.Frame(6100, 6000, 9, 0.5f, 20.f, false, 1500, &e));
  CHECK_EQ(e.tower, 7u);
  CHECK(tr.open());
  CHECK_EQ(tr.current().tower, 9u);
  // the totals
  TowerExposureTotals t;
  TowerEpisode a;
  a.startMs = 0;
  a.lastMs = 4000;
  a.targetedMs = 3900;
  a.shots = 4;
  a.hpStart = 0.25f;
  a.hpEnd = 0.f;
  a.died = true;
  a.lethal = true;
  a.thinks[kTtDive] = 3;
  TowerEpisode b;
  b.lastMs = 500;
  b.targetedMs = 500;
  b.shots = 1;
  b.hpStart = 0.9f;
  b.hpEnd = 0.8f;
  t.Add(a);
  t.Add(b);
  CHECK_EQ(t.episodes, 2u);
  CHECK_EQ(t.shots, 5u);
  CHECK_EQ(t.died, 1u);
  CHECK_EQ(t.lethal, 1u);
  CHECK_EQ(t.over3s, 1u);
  CHECK_EQ(t.lowEntries, 1u);
  CHECK_EQ(t.lowDeaths, 1u);
  CHECK_EQ(t.maxDurationMs, 4000ll);
  CHECK_EQ(t.targetedMs, 4400ll);
  CHECK_EQ(t.thinks[kTtDive], 3u);
  CHECK(t.hpLost > 0.34 && t.hpLost < 0.36);
}
