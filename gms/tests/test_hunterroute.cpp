// HUNTERROUTE (gms/core/bots/route.h): the bot Hunters' opening route, the Vanquish pick and the first buff's pull.
#include <vector>

#include "core/bots/jungle.h"
#include "core/bots/route.h"
#include "tests/check.h"

using namespace gms::bots;

namespace {

// Every step's camp found, not up yet, due at `due`.
std::vector<RouteCamp> Unspawned(std::size_t n, double due) {
  RouteCamp c;
  c.found = true;
  c.dueAt = due;
  return std::vector<RouteCamp>(n, c);
}

void Up(RouteCamp* c) {
  c->alive = true;
  c->spawns = 1;
  c->dueAt = -1.0;
}

void Cleared(RouteCamp* c, double respawnAt) {
  c->alive = false;
  c->dueAt = respawnAt;
}

}  // namespace

GMS_TEST(HunterrouteOpeningOnTheOwnHalf) {
  const std::vector<RouteStep> t1 = OpeningRoute(1), t2 = OpeningRoute(2);
  CHECK(t1.size() == 11);
  CHECK(t2.size() == t1.size());
  CHECK(OpeningRoute(0).empty());
  // the guides' order: green first, blue at 4, orange at 8, the pigs last
  CHECK(t1[0].kind == kRouteBuff && t1[0].minLevel == 1);
  CHECK(t1[4].kind == kRouteBuff);
  CHECK(t1[8].kind == kRouteBuff && t1[8].minLevel == 3);
  CHECK(t1.back().kind == kRoutePigs && t1.back().minLevel == 3);
  for (std::size_t i = 0; i < t1.size(); ++i) {
    // every step on the team's own half by the route cost (no enemy-half penalty), team 2's the mirror
    CHECK(CampRouteCost(10.f, t1[i].x, t1[i].y, 1, 90.f) == 10.f);
    CHECK(CampRouteCost(10.f, t2[i].x, t2[i].y, 2, 90.f) == 10.f);
    CHECK(t2[i].x == -t1[i].x && t2[i].y == -t1[i].y);
  }
}

GMS_TEST(HunterrouteStartsAtGreenAndWalksTheSteps) {
  const std::vector<RouteStep> s = OpeningRoute(1);
  const RouteConfig cfg;
  RouteMemory m;
  std::vector<RouteCamp> c = Unspawned(s.size(), 90.0);
  c[5].dueAt = 110.0;   // the 180-s mushroom camp
  c[10].dueAt = 120.0;  // the pigs
  // before the first spawn: the earliest spawning step, green
  RouteChoice r = NextRouteStep(s, c, &m, 1, 1.f, 40.0, cfg);
  CHECK(r.step == 0);
  r = NextRouteStep(s, c, &m, 1, 1.f, 75.0, cfg);
  CHECK(r.step == 0);  // spawns within waitS
  for (std::size_t i = 0; i < c.size(); ++i)
    if (c[i].dueAt <= 90.0) Up(&c[i]);
  r = NextRouteStep(s, c, &m, 1, 1.f, 91.0, cfg);
  CHECK(r.step == 0);
  // green cleared (by him or anyone): step 0 over, the small fish next
  Cleared(&c[0], 412.0);
  r = NextRouteStep(s, c, &m, 2, 0.45f, 113.0, cfg);
  CHECK(RouteStepDone(m, 0));
  CHECK(r.step == 1);
  Cleared(&c[1], 190.0);
  Cleared(&c[2], 200.0);
  Cleared(&c[3], 210.0);
  // blue at 45 % HP waits: the next small camp first (the 180-s mushroom camp, up since 110 s)
  Up(&c[5]);
  r = NextRouteStep(s, c, &m, 3, 0.45f, 150.0, cfg);
  CHECK(r.step == 5);
  // with HP back, blue
  r = NextRouteStep(s, c, &m, 3, 0.8f, 160.0, cfg);
  CHECK(r.step == 4);
  Cleared(&c[4], 470.0);
  Cleared(&c[5], 350.0);
  Cleared(&c[6], 240.0);
  Cleared(&c[7], 250.0);
  // orange from level 3 only: at level 2 the Ugger camp after it first
  r = NextRouteStep(s, c, &m, 2, 0.9f, 200.0, cfg);
  CHECK(r.step == 9);
  r = NextRouteStep(s, c, &m, 3, 0.9f, 201.0, cfg);
  CHECK(r.step == 8);
  Cleared(&c[8], 520.0);
  Cleared(&c[9], 280.0);
  Up(&c[10]);
  r = NextRouteStep(s, c, &m, 5, 0.9f, 240.0, cfg);
  CHECK(r.step == 10);
  Cleared(&c[10], 560.0);
  r = NextRouteStep(s, c, &m, 5, 0.9f, 260.0, cfg);
  CHECK(r.step == -1 && m.over);
  // over stays over (the default route from now on), even when a camp respawns
  Up(&c[1]);
  CHECK(NextRouteStep(s, c, &m, 5, 0.9f, 300.0, cfg).step == -1);
}

GMS_TEST(HunterrouteEndsAtLevelSixOrTheTime) {
  const std::vector<RouteStep> s = OpeningRoute(2);
  const RouteConfig cfg;
  std::vector<RouteCamp> c = Unspawned(s.size(), 90.0);
  for (RouteCamp& x : c) Up(&x);
  RouteMemory a;
  CHECK(NextRouteStep(s, c, &a, 6, 1.f, 200.0, cfg).step == -1 && a.over);
  RouteMemory b;
  CHECK(NextRouteStep(s, c, &b, 3, 1.f, cfg.endS, cfg).step == -1 && b.over);
  // a missing camp is skipped
  RouteMemory d;
  c[0].found = false;
  CHECK(NextRouteStep(s, c, &d, 1, 1.f, 95.0, cfg).step == 1);
  // nothing for his level now (only the level-3 steps left, none up): no step, the route not over
  RouteMemory e;
  std::vector<RouteCamp> f = Unspawned(s.size(), 90.0);
  for (std::size_t i = 0; i < f.size(); ++i) {
    Up(&f[i]);
    if (s[i].minLevel < 3) Cleared(&f[i], 400.0);
  }
  const RouteChoice r = NextRouteStep(s, f, &e, 2, 1.f, 150.0, cfg);
  CHECK(r.step == -1 && !e.over);
}

GMS_TEST(HunterrouteVanquishPick) {
  const VanquishConfig cfg;
  CHECK(VanquishDamage(1) == 330.f);
  CHECK(VanquishDamage(5) == 450.f);
  std::vector<VanquishCandidate> c(3);
  c[0] = {200.f, 200.f, 5.f, false};    // a fresh minor
  c[1] = {320.f, 950.f, 6.f, false};    // the medium, nearly dead
  c[2] = {1350.f, 1350.f, 7.f, true};   // the major, full
  CHECK(VanquishPick(c, 1, 0.8f, cfg) == 1);  // not the minor, the medium it kills
  c[2].hp = 300.f;
  CHECK(VanquishPick(c, 1, 0.8f, cfg) == 2);  // the buff's major first
  c[2].dist = 25.f;
  CHECK(VanquishPick(c, 1, 0.8f, cfg) == 1);  // out of reach
  c[1].hp = 600.f;
  CHECK(VanquishPick(c, 1, 0.8f, cfg) == -1);  // nothing it kills but the minor at good HP
  CHECK(VanquishPick(c, 1, 0.3f, cfg) == 0);   // low HP: the minor too
  CHECK(VanquishPick(c, 10, 0.8f, cfg) == 1);  // level 10: 600 kills the medium
}

GMS_TEST(HunterroutePull) {
  const PullConfig cfg;
  // not before the spawn, the Hunter not there, or the laner hurt: no
  CHECK(!PullGoesOn(89.0, 90.0, false, -1.0, 1.f, 1.f, 5.f, cfg));
  CHECK(PullGoesOn(91.0, 90.0, false, -1.0, 1.f, 1.f, 5.f, cfg));
  CHECK(!PullGoesOn(91.0, 90.0, false, -1.0, 1.f, 1.f, 40.f, cfg));
  CHECK(!PullGoesOn(91.0, 90.0, false, -1.0, 1.f, 0.5f, 5.f, cfg));
  CHECK(!PullGoesOn(125.0, 90.0, false, -1.0, 1.f, 1.f, 5.f, cfg));  // opened too late
  // once started: the window, the camp worn down
  CHECK(PullGoesOn(99.0, 90.0, true, 91.0, 0.8f, 1.f, 40.f, cfg));
  CHECK(!PullGoesOn(102.0, 90.0, true, 91.0, 0.8f, 1.f, 5.f, cfg));
  CHECK(!PullGoesOn(95.0, 90.0, true, 91.0, 0.55f, 1.f, 5.f, cfg));
  // the kits: Faris at 1, everyone at 2, nobody at 0
  CHECK(RouteKitWantsPull("golem.noun", 1));
  CHECK(!RouteKitWantsPull("otter.noun", 1));
  CHECK(RouteKitWantsPull("otter.noun", 2));
  CHECK(!RouteKitWantsPull("golem.noun", 0));
}

GMS_TEST(HunterrouteLevelTimes) {
  LevelTimes t;
  t.Note(1, 10.0);
  t.Note(2, 118.0);
  t.Note(2, 130.0);
  t.Note(5, 300.0);  // 4 passed on the way
  CHECK(t.at[0] == 118.0 && t.at[1] == 300.0 && t.at[2] < 0.0);
  t.Note(6, 400.0);
  CHECK(t.at[2] == 400.0);
}
