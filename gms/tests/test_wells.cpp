// gms/core/wells (D9-core): the spirit wells' rules without the engine (docs/MATCH_FLOW.md *The spirit wells' rules
// without the engine (D9-core)*): the client's capture-speed table, the locks, capture, contest, decay, damage, the
// award and bounty, the workers, the well gold, the bots' getters and the 8C send mask.
#include <cmath>
#include <cstdint>
#include <cstdlib>
#include <vector>

#include "core/wells/wells.h"
#include "tests/check.h"

using namespace gms::wells;

namespace {
bool Near(double a, double b, double eps) { return std::fabs(a - b) <= eps; }

constexpr std::uint32_t kWellW = 0x1001, kWellS = 0x1002, kWellE = 0x1003, kWellN = 0x1004;

// SM_Map_v5's four wells (match_flow.py wells): blue (1) west and south, red (2) east and north; 18 spots each.
std::vector<WellDef> V5Wells() {
  const float pos[4][2] = {{-182.3f, 27.9f}, {28.2f, -182.6f}, {182.5f, -27.9f}, {-27.6f, 182.7f}};
  const std::uint32_t ids[4] = {kWellW, kWellS, kWellE, kWellN};
  const int teams[4] = {1, 1, 2, 2};
  std::vector<WellDef> d;
  for (int i = 0; i < 4; ++i) {
    WellDef w;
    w.object = ids[i];
    w.team = teams[i];
    w.pos.x = pos[i][0];
    w.pos.y = pos[i][1];
    for (int s = 0; s < 18; ++s) {
      Vec3 p;
      p.x = w.pos.x + 20.f + static_cast<float>(s);
      p.y = w.pos.y;
      w.spots.push_back(p);
    }
    d.push_back(w);
  }
  return d;
}

Shaper At(std::uint32_t object, int player, int team, const WellDef& w, float dx = 0.f, float dz = 0.f) {
  Shaper s;
  s.object = object;
  s.player = player;
  s.team = team;
  s.alive = true;
  s.pos.x = w.pos.x + dx;
  s.pos.y = w.pos.y;
  s.pos.z = w.pos.z + dz;
  return s;
}

void Append(TickResult* acc, const TickResult& r) {
  acc->states.insert(acc->states.end(), r.states.begin(), r.states.end());
  acc->captures.insert(acc->captures.end(), r.captures.begin(), r.captures.end());
  acc->kills.insert(acc->kills.end(), r.kills.begin(), r.kills.end());
  acc->spawns.insert(acc->spawns.end(), r.spawns.begin(), r.spawns.end());
  acc->gold.insert(acc->gold.end(), r.gold.begin(), r.gold.end());
  acc->captureStarted.insert(acc->captureStarted.end(), r.captureStarted.begin(), r.captureStarted.end());
  acc->capturePaused.insert(acc->capturePaused.end(), r.capturePaused.begin(), r.capturePaused.end());
}

// A host stand-in: ticks, confirms every worker spawn with a new id, accumulates what the ticks asked for.
struct Host {
  Wells wells;
  TickResult acc;
  std::uint32_t nextWorker = 0x5000;
  double t = 0.0;
  std::vector<double> captureAt;

  explicit Host(const Tuning& tuning = Tuning()) { wells.Init(tuning, V5Wells(), 0.0); }

  void Step(double now, const std::vector<Shaper>& shapers) {
    TickResult r;
    wells.Tick(now, shapers, &r);
    for (const WorkerSpawn& s : r.spawns) wells.OnWorkerSpawned(s.well, s.slot, nextWorker++);
    for (size_t i = 0; i < r.captures.size(); ++i) captureAt.push_back(now);
    Append(&acc, r);
    t = now;
  }
  // Ticks from the current time to `to` at `hz` per second (ticks counted, so no drift).
  void Run(double to, double hz, const std::vector<Shaper>& shapers = {}) {
    const double from = t;
    const long n = std::lround((to - from) * hz);
    for (long k = 1; k <= n; ++k) Step(from + static_cast<double>(k) / hz, shapers);
  }
  int Gold(int team, int source) const {
    int sum = 0;
    for (const TeamGold& g : acc.gold)
      if (g.team == team && g.source == source) sum += g.amount;
    return sum;
  }
  int Count(const std::vector<StateChange>& v, int well, std::uint16_t from, std::uint16_t to) const {
    int n = 0;
    for (const StateChange& c : v)
      if (c.well == well && c.from == from && c.to == to) ++n;
    return n;
  }
};
}  // namespace

// LevelGameplayTuning_BuildCaptureSpeedTable (0x008DB920) on v5's 16/14/12/10/8: the steps and the capture times.
GMS_TEST(WellsSpeedTable) {
  const float d[5] = {16.f, 14.f, 12.f, 10.f, 8.f};
  const SpeedTable t = BuildSpeedTable(d, 5);
  CHECK(t.ok);
  CHECK_EQ(t.count, 5u);
  CHECK_EQ(t.base, 16.f);
  CHECK_EQ(t.step[0], 1.f);
  CHECK(Near(t.step[1], 16.0 / 14 - 1, 1e-6));
  CHECK(Near(t.step[2], 16.0 / 12 - 16.0 / 14, 1e-6));
  CHECK(Near(t.step[3], 16.0 / 10 - 16.0 / 12, 1e-6));
  CHECK(Near(t.step[4], 16.0 / 8 - 16.0 / 10, 1e-6));
  for (int n = 1; n <= 5; ++n) CHECK(Near(CaptureSeconds(t, 1.f, n), d[n - 1], 1e-4));
  CHECK(Near(CaptureSeconds(t, 1.f, 6), 8.0, 1e-4));  // capturers beyond the table add nothing
  CHECK_EQ(CaptureSeconds(t, 1.f, 0), 0.f);
  CHECK(Near(CaptureSeconds(t, 2.f, 1), 8.0, 1e-4));  // goldMineCaptureRatePerSecond scales it
  const float w[2] = {1.f, 0.f};                      // one undamaged, one damaged at the multiplier 0
  CHECK_EQ(SpeedFactor(t, w, 2), 1.f);
  // the client's refusals: more than 5 entries, an entry not above 0.1
  const float six[6] = {16.f, 14.f, 12.f, 10.f, 8.f, 6.f};
  CHECK(!BuildSpeedTable(six, 6).ok);
  CHECK_EQ(BuildSpeedTable(six, 6).count, 0u);
  const float bad[3] = {16.f, 14.f, 0.05f};
  const SpeedTable b = BuildSpeedTable(bad, 3);
  CHECK(!b.ok);
  CHECK_EQ(b.step[2], 0.f);
  CHECK(!BuildSpeedTable(d, 0).ok);
}

// Locked until 900 s (nothing captures a locked well), then unlocking (2) for the transition and unlocked (3).
GMS_TEST(WellsLockedUntilFifteenMinutes) {
  Host h;
  CHECK_EQ(h.wells.Count(), 4);
  for (int i = 0; i < 4; ++i) {
    CHECK_EQ(h.wells.State(i), static_cast<std::uint16_t>(kLocked));
    CHECK(h.wells.IsLocked(i));
    CHECK_EQ(h.wells.Node(i).lockedTimer, 900.f);
    CHECK_EQ(h.wells.Node(i).lockedTimerDuration, 900.f);
    CHECK_EQ(static_cast<int>(h.wells.Node(i).displayIndex), i);
  }
  CHECK_EQ(h.wells.Owner(0), 1);
  CHECK_EQ(h.wells.Owner(2), 2);
  const WellDef w0 = V5Wells()[0];
  const std::vector<Shaper> enemyIn = {At(0x100, 5, 2, w0)};
  h.Run(300, 1, enemyIn);
  CHECK_EQ(h.wells.Node(0).lockedTimer, 600.f);
  h.Run(899, 1, enemyIn);
  CHECK_EQ(h.wells.Score(0), 0.f);  // a locked well isn't capturable
  CHECK(h.acc.captures.empty());
  CHECK(h.acc.captureStarted.empty());
  CHECK(h.acc.states.empty());
  CHECK(h.wells.IsLocked(0));
  CHECK(h.wells.IsTitanNodeLocked(kWellW));
  CHECK(h.wells.MinesForTeam(1, true).empty());
  h.Run(900, 1);
  CHECK(!h.wells.IsLocked(0));
  CHECK(!h.wells.IsTitanNodeLocked(kWellW));
  CHECK_EQ(h.wells.Node(0).lockedTimer, 0.f);
  CHECK_EQ(h.wells.Node(0).lockedTimerDuration, 900.f);  // stays non-zero: the panel divides by it
  CHECK_EQ(h.wells.State(0), static_cast<std::uint16_t>(kUnlocking));
  CHECK_EQ(h.Count(h.acc.states, 0, kLocked, kUnlocking), 1);
  h.Run(902, 1);
  CHECK_EQ(h.wells.State(3), static_cast<std::uint16_t>(kUnlocked));
  CHECK_EQ(h.Count(h.acc.states, 3, kUnlocking, kUnlocked), 1);
  CHECK_EQ(h.acc.states.size(), static_cast<size_t>(8));  // 4 x (1 -> 2, 2 -> 3)
  // without the transition: 1 -> 3 at once
  Tuning t;
  t.transitionSeconds = 0.f;
  Host q(t);
  q.Run(900, 1);
  CHECK_EQ(q.Count(q.acc.states, 0, kLocked, kUnlocked), 1);
  CHECK_EQ(q.acc.states.size(), static_cast<size_t>(4));
}

// One capturer at 30 Hz: 16 s, the score's sign and speed, the capture's effects, the 240 s lock both ways.
GMS_TEST(WellsCaptureByOne) {
  Host h;
  h.Run(900, 1);
  const size_t workersBefore = static_cast<size_t>(h.wells.Workers(0));
  CHECK_EQ(workersBefore, static_cast<size_t>(18));
  const WellDef w0 = V5Wells()[0];
  const std::vector<Shaper> in = {At(0x100, 6, 2, w0, 5.f)};
  h.Run(908, 30, in);
  CHECK(Near(h.wells.Score(0), 0.5, 1e-3));
  const NodeView mid = h.wells.Node(0);
  CHECK(mid.captureScore > 0.f);  // team 2 capturing: > 0 (SpiritWellDisplay_UpdateCapture's colours)
  CHECK(Near(mid.captureScoreSpeed, 1.0 / 16, 1e-6));
  CHECK(Near(mid.captureDuration, 16.0, 1e-3));
  CHECK_EQ(h.wells.State(0), static_cast<std::uint16_t>(kBeingCaptured));
  CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(1));
  CHECK(h.acc.captures.empty());
  h.Run(917, 30, in);
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
  if (h.acc.captures.empty() || h.captureAt.empty()) return;
  CHECK(Near(h.captureAt[0], 916.0, 1.0 / 30 + 1e-6));
  const Capture& c = h.acc.captures[0];
  CHECK_EQ(c.well, 0);
  CHECK_EQ(c.object, kWellW);
  CHECK_EQ(c.from, 1);
  CHECK_EQ(c.to, 2);
  CHECK_EQ(c.source, 0x100u);
  CHECK_EQ(c.capturers.size(), static_cast<size_t>(1));
  CHECK_EQ(c.award, 50.f);
  CHECK_EQ(h.Gold(2, kGoldSourceObjective), 50);
  CHECK_EQ(h.Gold(1, kGoldSourceObjective), 0);
  // every worker of the well destroyed, each with its confirmed id
  CHECK_EQ(h.acc.kills.size(), workersBefore);
  for (const WorkerKill& k : h.acc.kills) CHECK(k.worker != 0u && k.well == 0);
  CHECK_EQ(h.wells.Workers(0), 0);
  // the new owner, locked 240 s, state 4 -> 5 -> 1
  CHECK_EQ(h.wells.Owner(0), 2);
  const NodeView after = h.wells.Node(0);
  CHECK_EQ(static_cast<int>(after.teamControl), 2);
  CHECK_EQ(after.captureScore, 0.f);
  CHECK_EQ(after.captureScoreSpeed, 0.f);
  CHECK_EQ(after.lockedTimerDuration, 240.f);
  CHECK(Near(after.lockedTimer, 240.0 - (917.0 - h.captureAt[0]), 1e-3));
  CHECK(h.wells.IsLocked(0));
  CHECK_EQ(h.Count(h.acc.states, 0, kBeingCaptured, kLocking), 1);
  h.Run(920, 30, in);
  CHECK_EQ(h.Count(h.acc.states, 0, kLocking, kLocked), 1);
  CHECK(StateShowsLocked(h.wells.State(0)));
  // the bots see it
  const std::vector<std::uint32_t> red = h.wells.MinesForTeam(2);
  CHECK_EQ(red.size(), static_cast<size_t>(3));
  CHECK_EQ(h.wells.MinesForTeam(1).size(), static_cast<size_t>(1));
  CHECK_EQ(h.wells.MinesForTeam(2, true).size(), static_cast<size_t>(2));  // the new one is locked
  // the old owner can't take it back for 240 s ("cannot be retaken for 4 minutes")
  const double cap = h.captureAt[0];
  const std::vector<Shaper> back = {At(0x200, 1, 1, w0)};
  h.Run(std::floor(cap + 239.0), 1, back);
  CHECK_EQ(h.wells.Owner(0), 2);
  CHECK_EQ(h.wells.Score(0), 0.f);
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  h.Run(std::floor(cap + 240.0) + 1.0 + 17.0, 1, back);
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(2));
  CHECK_EQ(h.wells.Owner(0), 1);
  CHECK_EQ(h.Gold(1, kGoldSourceObjective), 50);
}

// 2-5 capturers: 14/12/10/8 s; a sixth adds nothing; only shapers of the other team capture.
GMS_TEST(WellsCaptureByCount) {
  const double expect[6] = {16, 14, 12, 10, 8, 8};
  for (int n = 1; n <= 6; ++n) {
    Tuning t;
    t.transitionSeconds = 0.f;
    Host h(t);
    h.Run(900, 1);
    const WellDef w2 = V5Wells()[2];  // a red well: blue (1) captures, score <= 0
    std::vector<Shaper> in;
    for (int k = 0; k < n; ++k) in.push_back(At(0x300u + static_cast<std::uint32_t>(k), k, 1, w2, static_cast<float>(k)));
    h.Run(902, 10, in);
    CHECK(h.wells.Node(2).captureScore < 0.f);
    h.Run(920, 10, in);
    CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
    if (h.captureAt.empty()) continue;
    CHECK(Near(h.captureAt[0] - 900.0, expect[n - 1], 0.1 + 1e-6));
    CHECK_EQ(h.acc.captures[0].to, 1);
    CHECK_EQ(h.acc.captures[0].capturers.size(), static_cast<size_t>(n));
    CHECK_EQ(h.acc.captures[0].source, 0x300u);  // the lowest gameplay index
  }
}

// A living shaper of the owning team in the sphere pauses the capture (no decay); it resumes when it leaves.
GMS_TEST(WellsContested) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  const WellDef w0 = V5Wells()[0];
  const Shaper cap = At(0x100, 6, 2, w0);
  h.Run(904, 10, {cap});
  CHECK(Near(h.wells.Score(0), 0.25, 1e-3));
  Shaper def = At(0x200, 1, 1, w0, -3.f);
  h.Run(910, 10, {cap, def});
  CHECK(Near(h.wells.Score(0), 0.25, 1e-3));  // paused, held
  CHECK_EQ(h.wells.Node(0).captureScoreSpeed, 0.f);
  CHECK_EQ(h.wells.State(0), static_cast<std::uint16_t>(kBeingCaptured));
  // a dead defender and one outside the sphere don't contest
  Shaper dead = def;
  dead.alive = false;
  Shaper outside = At(0x201, 2, 1, w0, 12.5f);
  h.Run(912, 10, {cap, dead, outside});
  CHECK(Near(h.wells.Score(0), 0.375, 1e-3));
  h.Run(925, 10, {cap});
  CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
  if (!h.captureAt.empty()) CHECK(Near(h.captureAt[0], 922.0, 0.1 + 1e-6));  // 16 s of capture + 6 s held
  CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(2));  // the start, and the resume after the contest
}

// Without capturers the score decays at the capture's own unit (decayRate / 16 per second) and the well goes back to 3.
GMS_TEST(WellsDecay) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  const WellDef w0 = V5Wells()[0];
  h.Run(908, 10, {At(0x100, 6, 2, w0)});
  CHECK(Near(h.wells.Score(0), 0.5, 1e-3));
  h.Run(912, 10);  // left the ring
  CHECK(Near(h.wells.Score(0), 0.25, 1e-3));
  CHECK(Near(h.wells.Node(0).captureScoreSpeed, -1.0 / 16, 1e-6));
  CHECK(h.wells.Node(0).captureScore > 0.f);  // still team 2's progress
  CHECK_EQ(h.wells.State(0), static_cast<std::uint16_t>(kBeingCaptured));
  h.Run(916.5, 10);
  CHECK_EQ(h.wells.Score(0), 0.f);
  CHECK_EQ(h.wells.Node(0).captureScoreSpeed, 0.f);
  CHECK_EQ(h.wells.State(0), static_cast<std::uint16_t>(kUnlocked));
  CHECK_EQ(h.Count(h.acc.states, 0, kBeingCaptured, kUnlocked), 1);
  CHECK(h.acc.captures.empty());
  CHECK_EQ(h.wells.Owner(0), 1);
  // own-team shapers alone don't capture and don't stop the decay; a faster decay rate is honoured
  Tuning f = t;
  f.decayRate = 2.f;
  Host g(f);
  g.Run(900, 1);
  g.Run(908, 10, {At(0x100, 6, 2, w0)});
  g.Run(910, 10, {At(0x200, 1, 1, w0)});
  CHECK(Near(g.wells.Score(0), 0.25, 1e-3));
}

// An enemy player's hit within goldMineCaptureHitPlayerDurationInMS (5 s) pauses a lone capturer (multiplier 0); with
// two, the hit one adds nothing.
GMS_TEST(WellsDamagePausesCapture) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  const WellDef w0 = V5Wells()[0];
  Shaper cap = At(0x100, 6, 2, w0);
  h.Run(903, 10, {cap});
  CHECK(Near(h.wells.Score(0), 3.0 / 16, 1e-3));
  cap.lastPlayerHitAt = 903.0;
  cap.lastDamagedAt = 903.0;  // a player's hit is damage too
  h.Run(907.9, 10, {cap});
  CHECK(Near(h.wells.Score(0), 3.0 / 16, 1e-3));  // paused, not decaying
  CHECK_EQ(h.wells.Node(0).captureScoreSpeed, 0.f);
  h.Run(925, 10, {cap});
  CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
  if (!h.captureAt.empty()) CHECK(Near(h.captureAt[0], 921.0, 0.1 + 1e-6));  // 16 s + the 5 s window
  // two capturers, one hit: the speed of one
  Host g(t);
  g.Run(900, 1);
  Shaper a = At(0x100, 6, 2, w0), b = At(0x101, 7, 2, w0, 1.f);
  b.lastPlayerHitAt = 1e9;  // hit for the whole run
  g.Run(901, 10, {a, b});
  CHECK(Near(g.wells.Node(0).captureScoreSpeed, 1.0 / 16, 1e-6));
  // a non-zero multiplier: the hit capturer adds its step scaled
  Tuning m = t;
  m.hitSpeedMultiplier = 0.5f;
  Host k(m);
  k.Run(900, 1);
  k.Run(901, 10, {a, b});
  CHECK(Near(k.wells.Node(0).captureScoreSpeed, (1.0 + 0.5 * (16.0 / 14 - 1)) / 16, 1e-6));
}

// D9c: which hits count. The damage tracker's "player damage" (DamageTracker_RecordCombatTimes 0x008E0DC0, kind 1): dealt
// > 0 by an attacker of the other team that is player-controlled (+0x56) or a player's pet; nothing else.
GMS_TEST(WellsD9cPlayerHitClassification) {
  Hit shaper;
  shaper.dealt = 12.f;
  shaper.attacker = true;
  shaper.attackerPlayerControlled = true;
  CHECK(IsPlayerHit(shaper));
  CHECK(PausesCapture(shaper, kHitByEnemyPlayer));
  CHECK(PausesCapture(shaper, kHitByAnything));
  Hit pet = shaper;  // e.g. Flin's beetle (NPC type 11)
  pet.attackerPlayerControlled = false;
  pet.attackerPlayerPet = true;
  CHECK(IsPlayerHit(pet));
  Hit npc = shaper;  // a worker, a minion, a creep, the parasite, a Binding, the Guardian
  npc.attackerPlayerControlled = false;
  CHECK(!IsPlayerHit(npc));
  CHECK(!PausesCapture(npc, kHitByEnemyPlayer));
  CHECK(PausesCapture(npc, kHitByAnything));  // D9's rule
  Hit cost;  // a health cost: no snapshot, no attacker
  cost.dealt = 30.f;
  CHECK(!IsPlayerHit(cost));
  CHECK(PausesCapture(cost, kHitByAnything));
  Hit self = shaper;  // the capturer's own ability on itself
  self.self = true;
  CHECK(!IsPlayerHit(self));
  Hit ally = shaper;  // never happens (DamageTracker.cpp's friendly-fire assert), but not a pause either
  ally.sameTeam = true;
  CHECK(!IsPlayerHit(ally));
  Hit shield = shaper;  // a shield took it all: no HP lost, nothing recorded
  shield.dealt = 0.f;
  CHECK(!IsPlayerHit(shield));
  CHECK(!PausesCapture(shield, kHitByAnything));
}

// D9c (user report 2026-10-09: 47 s instead of 16 s): workers and minions hitting the capturer every tick don't slow
// the capture; an enemy shaper's hit pauses it for the window; D9's rule (kHitByAnything) still pauses on any damage.
GMS_TEST(WellsD9cOnlyPlayerHitsPause) {
  Tuning t;
  t.transitionSeconds = 0.f;
  const WellDef w2 = V5Wells()[2];  // a red well; blue (team 1) captures
  // NPC hits on every tick: a full capture in 16 s
  {
    Host h(t);
    h.Run(900, 1);
    for (int k = 1; k <= 200; ++k) {
      Shaper cap = At(0x100, 0, 1, w2, 9.f);
      cap.lastDamagedAt = 900.0 + k / 10.0;  // hit by a worker or a minion in this tick
      h.Step(900.0 + k / 10.0, {cap});
      if (!h.captureAt.empty()) break;
    }
    CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
    if (!h.captureAt.empty()) CHECK(Near(h.captureAt[0], 916.0, 0.1 + 1e-6));
    CHECK(h.acc.capturePaused.empty());
    CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(1));  // one start, no resumes
  }
  // the same under D9's rule: never moves while hit
  {
    Tuning old = t;
    old.hitRule = kHitByAnything;
    Host h(old);
    h.Run(900, 1);
    Shaper cap = At(0x100, 0, 1, w2, 9.f);
    h.Run(903, 10, {cap});
    for (int k = 1; k <= 100; ++k) {
      cap.lastDamagedAt = 903.0 + k / 10.0;
      h.Step(903.0 + k / 10.0, {cap});
    }
    CHECK(Near(h.wells.Score(2), 3.0 / 16, 1e-3));
    CHECK(h.captureAt.empty());
    CHECK_EQ(h.acc.capturePaused.size(), static_cast<size_t>(1));
    if (!h.acc.capturePaused.empty()) {
      CHECK_EQ(h.acc.capturePaused[0].reason, static_cast<int>(kPauseHit));
      CHECK_EQ(h.acc.capturePaused[0].players.size(), static_cast<size_t>(1));
    }
  }
  // an enemy shaper's hit at 905 s among NPC hits: paused until 910 s, captured at 921 s
  {
    Host h(t);
    h.Run(900, 1);
    Shaper cap = At(0x100, 0, 1, w2, 9.f);
    for (int k = 1; k <= 250 && h.captureAt.empty(); ++k) {
      const double now = 900.0 + k / 10.0;
      cap.lastDamagedAt = now;
      if (std::fabs(now - 905.0) < 1e-6) cap.lastPlayerHitAt = now;
      h.Step(now, {cap});
    }
    CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
    if (!h.captureAt.empty()) CHECK(Near(h.captureAt[0], 921.0, 0.1 + 1e-6));
    CHECK_EQ(h.acc.capturePaused.size(), static_cast<size_t>(1));
    if (!h.acc.capturePaused.empty()) {
      CHECK_EQ(h.acc.capturePaused[0].reason, static_cast<int>(kPauseHit));
      CHECK(Near(h.acc.capturePaused[0].at, 905.0, 1e-6));
    }
    CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(2));  // the start and the resume at 910 s
  }
}

// D9c: the pauses the host logs: contested (with the defenders), hit, and left (decay); a resume is a captureStarted.
GMS_TEST(WellsD9cPauseReasons) {
  Tuning t;
  t.transitionSeconds = 0.f;
  const WellDef w0 = V5Wells()[0];
  Host h(t);
  h.Run(900, 1);
  const Shaper cap = At(0x100, 6, 2, w0);
  h.Run(902, 10, {cap});
  h.Run(904, 10, {cap, At(0x200, 3, 1, w0, -4.f)});  // a defender
  h.Run(906, 10, {cap});
  h.Run(908, 10);  // the capturer left
  CHECK_EQ(h.acc.capturePaused.size(), static_cast<size_t>(2));
  if (h.acc.capturePaused.size() == 2) {
    CHECK_EQ(h.acc.capturePaused[0].reason, static_cast<int>(kPauseContested));
    CHECK_EQ(h.acc.capturePaused[0].players.size(), static_cast<size_t>(1));
    if (!h.acc.capturePaused[0].players.empty()) CHECK_EQ(h.acc.capturePaused[0].players[0], 3);
    CHECK_EQ(h.acc.capturePaused[1].reason, static_cast<int>(kPauseLeft));
  }
  CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(2));
}

// The capture sphere: 12 units in 3D; dead, own-team and non-team creatures don't capture.
GMS_TEST(WellsCaptureSphere) {
  Tuning t;
  t.transitionSeconds = 0.f;
  const WellDef w0 = V5Wells()[0];
  struct Case {
    Shaper s;
    bool captures;
  };
  Shaper dead = At(0x100, 6, 2, w0);
  dead.alive = false;
  const Case cases[] = {
      {At(0x100, 6, 2, w0, 11.9f), true},       {At(0x100, 6, 2, w0, 12.1f), false},
      {At(0x100, 6, 2, w0, 0.f, 12.5f), false},  // a sphere, not a cylinder
      {dead, false},
      {At(0x100, 1, 1, w0), false},  // the owner's own team
      {At(0x100, 6, 0, w0), false},  // no team (NPC)
  };
  for (const Case& c : cases) {
    Host h(t);
    h.Run(900, 1);
    h.Run(901, 10, {c.s});
    CHECK_EQ(h.wells.Score(0) > 0.f, c.captures);
  }
}

// One worker every 15 s up to 18 (at 15 ... 270 s), at the spots in slot order; a death frees its slot and a full
// well's next worker comes 15 s later.
GMS_TEST(WellsWorkers) {
  Host h;
  h.Run(14, 1);
  CHECK(h.acc.spawns.empty());
  h.Run(15, 1);
  CHECK_EQ(h.acc.spawns.size(), static_cast<size_t>(4));  // one per well
  CHECK_EQ(h.wells.Workers(0), 1);
  h.Run(269, 1);
  CHECK_EQ(h.wells.Workers(0), 17);
  h.Run(270, 1);
  CHECK_EQ(h.wells.Workers(0), 18);
  h.Run(400, 1);
  CHECK_EQ(h.wells.Workers(0), 18);  // never more than numFollowerSlots
  CHECK_EQ(h.acc.spawns.size(), static_cast<size_t>(72));
  CHECK_EQ(h.wells.FollowersForTitan(kWellW), 18);
  CHECK_EQ(h.wells.FollowersForTitan(0xDEAD), 0);
  const std::vector<WellDef> defs = V5Wells();
  int slot = 0;
  for (const WorkerSpawn& s : h.acc.spawns) {
    if (s.well != 0) continue;
    CHECK_EQ(s.slot, slot);
    CHECK_EQ(s.team, 1);
    CHECK_EQ(s.pos.x, defs[0].spots[static_cast<size_t>(slot)].x);
    ++slot;
  }
  // a death at full capacity: the slot is free, the replacement 15 s later in that slot
  const std::uint32_t victim = h.wells.WorkerAt(0, 4);
  CHECK(victim != 0u);
  CHECK(h.wells.OnWorkerDied(victim));
  CHECK(!h.wells.OnWorkerDied(victim));
  CHECK(!h.wells.OnWorkerDied(0xDEAD));
  CHECK_EQ(h.wells.Workers(0), 17);
  const size_t before = h.acc.spawns.size();
  h.Run(414, 1);
  CHECK_EQ(h.acc.spawns.size(), before);
  h.Run(415, 1);
  CHECK_EQ(h.acc.spawns.size(), before + 1);
  if (h.acc.spawns.size() == before + 1) {
    CHECK_EQ(h.acc.spawns.back().well, 0);
    CHECK_EQ(h.acc.spawns.back().slot, 4);
  }
  CHECK_EQ(h.wells.Workers(0), 18);
  // a spawn the host couldn't make frees its slot again
  const std::uint32_t v2 = h.wells.WorkerAt(1, 0);
  CHECK(h.wells.OnWorkerDied(v2));
  h.Run(430, 1);
  const WorkerSpawn last = h.acc.spawns.back();
  CHECK_EQ(last.well, 1);
  h.wells.OnWorkerSpawnFailed(last.well, last.slot);
  CHECK_EQ(h.wells.Workers(1), 17);
  // the bots' harass threshold (condition_Mission_HarassWell: more than 16 workers)
  CHECK(h.wells.FollowersForTitan(kWellE) > 16);
}

// A capture destroys the workers; the new owner's first worker comes 15 s later, with its team.
GMS_TEST(WellsWorkersAfterCapture) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  const WellDef w0 = V5Wells()[0];
  h.Run(916, 1, {At(0x100, 6, 2, w0)});
  CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
  CHECK_EQ(h.acc.kills.size(), static_cast<size_t>(18));
  const size_t before = h.acc.spawns.size();
  h.Run(930, 1);
  CHECK_EQ(h.wells.Workers(0), 0);
  h.Run(931, 1);
  CHECK_EQ(h.acc.spawns.size(), before + 1);
  CHECK_EQ(h.acc.spawns.back().team, 2);
  CHECK_EQ(h.acc.spawns.back().slot, 0);
  CHECK_EQ(h.wells.Workers(0), 1);
}

// D9b: every worker is created with its well as its owner (object +0x48), whichever team owns the well, so the
// client's CombatEvent_DamageAbsorb.lua names the well in MineUnderAttack; the team still follows the well's owner.
GMS_TEST(WellsWorkersOwnedByTheirWell) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  const std::vector<WellDef> defs = V5Wells();
  CHECK_EQ(h.acc.spawns.size(), static_cast<size_t>(72));
  for (const WorkerSpawn& s : h.acc.spawns) {
    CHECK(s.well >= 0 && s.well < 4);
    if (s.well < 0 || s.well >= 4) continue;
    CHECK_EQ(s.owner, defs[static_cast<size_t>(s.well)].object);
    CHECK_EQ(s.team, defs[static_cast<size_t>(s.well)].team);
    CHECK(s.owner != 0u);
  }
  // after a capture the new owner's workers belong to the same well, with the capturers' team
  h.Run(916, 1, {At(0x100, 6, 2, defs[0])});
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  const size_t before = h.acc.spawns.size();
  h.Run(931, 1);
  CHECK_EQ(h.acc.spawns.size(), before + 1);
  if (h.acc.spawns.size() != before + 1) return;
  CHECK_EQ(h.acc.spawns.back().well, 0);
  CHECK_EQ(h.acc.spawns.back().team, 2);
  CHECK_EQ(h.acc.spawns.back().owner, kWellW);
}

// D9b: the seconds a moving capture still needs (the guard of the host's own MineUnderAttack); -1 while nothing moves.
// A capture held by a contest near its end resumes with under a second left: D9 announced that resume, and the
// announcement played after the flip (q3_run3: 0.72 s, q3b_run1: 0.99 s; MiniMapRenderer.cpp 2941).
GMS_TEST(WellsSecondsToCapture) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  CHECK_EQ(h.wells.SecondsToCapture(0), -1.0);  // nobody there
  CHECK_EQ(h.wells.SecondsToCapture(-1), -1.0);
  CHECK_EQ(h.wells.SecondsToCapture(7), -1.0);
  const WellDef w0 = V5Wells()[0];
  const Shaper cap = At(0x100, 6, 2, w0);
  h.Step(900.1, {cap});
  CHECK_EQ(h.acc.captureStarted.size(), static_cast<size_t>(1));
  CHECK(Near(h.wells.SecondsToCapture(0), 15.9, 1e-3));
  h.Run(908.1, 10, {cap});
  CHECK(Near(h.wells.SecondsToCapture(0), 7.9, 1e-2));
  // two capturers: 14 s for the whole capture, so the rest goes faster
  h.Step(908.2, {cap, At(0x101, 7, 2, w0, 1.f)});
  CHECK(Near(h.wells.SecondsToCapture(0), (1.0 - h.wells.Score(0)) * 14.0, 1e-3));
  // a defender holds it with under half a second left: nothing moves
  const Shaper def = At(0x200, 1, 1, w0, -3.f);
  h.Run(915.6, 10, {cap});
  h.Run(920, 10, {cap, def});
  CHECK_EQ(h.wells.SecondsToCapture(0), -1.0);
  CHECK(h.wells.Score(0) > 0.9);
  // the defender leaves: the capture resumes with about half a second left, and completes right after
  const size_t starts = h.acc.captureStarted.size();
  h.Step(920.1, {cap});
  CHECK_EQ(h.acc.captureStarted.size(), starts + 1);
  CHECK(h.wells.SecondsToCapture(0) >= 0.0);
  CHECK(h.wells.SecondsToCapture(0) < 1.0);
  h.Run(921, 10, {cap});
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  CHECK_EQ(h.wells.SecondsToCapture(0), -1.0);  // captured and locked
  // decaying: nothing moves forward
  Host g(t);
  g.Run(900, 1);
  g.Run(904, 10, {cap});
  g.Run(905, 10);
  CHECK(g.wells.Score(0) > 0.0);
  CHECK_EQ(g.wells.SecondsToCapture(0), -1.0);
}

// WELLDEF: the capturers in the ring are counted whatever the capture does (moving, contested by a defender, paused by
// a hit), so the bots' threat outlives the contest their defender makes; none while nobody of the other team is there,
// for a dead capturer, or once the well is captured and locked.
GMS_TEST(WellsCapturersCountedWhateverTheCapture) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  CHECK_EQ(h.wells.Capturers(0), 0);
  CHECK_EQ(h.wells.Capturers(-1), 0);
  const WellDef w0 = V5Wells()[0];
  Shaper cap = At(0x100, 6, 2, w0);
  const Shaper cap2 = At(0x101, 7, 2, w0, 1.f);
  h.Run(902, 10, {cap, cap2});
  CHECK_EQ(h.wells.Capturers(0), 2);
  CHECK(h.wells.SecondsToCapture(0) >= 0.0);
  // contested: the capture stands still, the capturers are still there
  const Shaper def = At(0x200, 1, 1, w0, -3.f);
  h.Run(903, 10, {cap, cap2, def});
  CHECK_EQ(h.wells.SecondsToCapture(0), -1.0);
  CHECK_EQ(h.wells.Capturers(0), 2);
  // paused by a hit (one capturer, hit): still counted
  Host g(t);
  g.Run(900, 1);
  cap.lastPlayerHitAt = 1e9;
  g.Run(901, 10, {cap});
  CHECK_EQ(g.wells.SecondsToCapture(0), -1.0);
  CHECK_EQ(g.wells.Capturers(0), 1);
  // a dead one, one outside the ring, the owner's own shaper: none
  Shaper dead = At(0x100, 6, 2, w0);
  dead.alive = false;
  g.Run(902, 10, {dead, At(0x102, 8, 2, w0, 12.5f), def});
  CHECK_EQ(g.wells.Capturers(0), 0);
  // captured: locked, none
  h.Run(930, 10, {At(0x100, 6, 2, w0), cap2});
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  CHECK_EQ(h.wells.Capturers(0), 0);
}

// The well gold: 1.5 per worker per minute per player of the owner, paid each second in whole units; the team GPM.
GMS_TEST(WellsGold) {
  Host h;
  h.Run(15, 1);
  CHECK_EQ(h.Gold(1, kGoldSourceWellPassive), 0);
  h.Run(300, 1);
  // per well: 15 x (1 + ... + 17) + 18 x 30 = 2835 worker-seconds -> 70.875 gold; two wells 141.75 -> 141 paid
  CHECK_EQ(h.Gold(1, kGoldSourceWellPassive), 141);
  CHECK_EQ(h.Gold(2, kGoldSourceWellPassive), 141);
  CHECK_EQ(h.wells.Node(0).goldPerMinute, 27.f);
  CHECK_EQ(h.wells.TeamGpm(1), static_cast<std::uint16_t>(54));
  CHECK_EQ(h.wells.TeamGpm(2), static_cast<std::uint16_t>(54));
  CHECK_EQ(h.wells.Node(0).availableResource, -1.f);  // unlimited (goldMineStartCapacity -1)
  CHECK_EQ(h.wells.Node(0).goldMineCapacity, -1.f);
  // steady: one minute with 18 workers in both wells pays exactly 54
  const int before = h.Gold(1, kGoldSourceWellPassive);
  h.Run(360, 1);
  CHECK_EQ(h.Gold(1, kGoldSourceWellPassive) - before, 54);
  // every grant is whole and at most one per team per second
  for (const TeamGold& g : h.acc.gold) CHECK(g.amount >= 1);
  // fewer workers, less gold: kill 8 at well 0 -> 10 workers -> 15 gpm, team 1 = 42
  for (int s = 0; s < 8; ++s) h.wells.OnWorkerDied(h.wells.WorkerAt(0, s));
  CHECK_EQ(h.wells.TeamGpm(1), static_cast<std::uint16_t>(42));
  CHECK_EQ(h.wells.Node(0).goldPerMinute, 15.f);
  CHECK_EQ(static_cast<int>(h.wells.Node(0).followerCount), 10);
}

// The bounty: half of what the well mined per player since its owner took it, paid to the capturing team.
GMS_TEST(WellsBounty) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  h.Run(900, 1);
  // well 0 mined 15 x 153 + 18 x 630 = 13635 worker-seconds -> 340.875 per player; half is the bounty
  CHECK(Near(h.wells.Node(0).bounty, 170.4375, 1e-3));
  const WellDef w0 = V5Wells()[0];
  h.Run(916, 1, {At(0x100, 6, 2, w0)});
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  if (h.acc.captures.empty()) return;
  // + 16 s at 27 gpm before the capture: 340.875 + 7.2 = 348.075 -> 174.0375
  CHECK(Near(h.acc.captures[0].bounty, 174.0375, 1e-2));
  CHECK_EQ(h.Gold(2, kGoldSourceWellBounty), 174);
  CHECK_EQ(h.Gold(1, kGoldSourceWellBounty), 0);
  CHECK_EQ(h.wells.Node(0).bounty, 0.f);
  // the new owner's bounty grows from its own workers only
  h.Run(960, 1);
  CHECK(h.wells.Node(0).bounty > 0.f);
  CHECK(h.wells.Node(0).bounty < 1.f);
}

// A limited mine (goldMineStartCapacity >= 0): mining stops when it is empty and, in mode "gold mine not empty", so
// does the auto-fill; mode "unlimited" keeps filling.
GMS_TEST(WellsLimitedMine) {
  Tuning t;
  t.startCapacity = 10.f;
  Host h(t);
  h.Run(100, 1);
  // 15 x (1 + ... + 5) + 6 x 10 = 285 worker-seconds -> 7.125 gold mined, 2.875 left
  CHECK(Near(h.wells.Node(0).availableResource, 2.875, 1e-4));
  CHECK_EQ(h.wells.Node(0).goldMineCapacity, 10.f);
  CHECK_EQ(h.wells.Workers(0), 6);
  h.Run(200, 1);  // empty at about 117 s (400 worker-seconds), the 7th worker came at 105 s
  CHECK_EQ(h.wells.Node(0).availableResource, 0.f);
  CHECK_EQ(h.wells.GoldPerMinute(0), 0.f);
  CHECK_EQ(h.wells.TeamGpm(1), static_cast<std::uint16_t>(0));
  CHECK_EQ(h.Gold(1, kGoldSourceWellPassive), 20);  // 10 per well, two wells
  CHECK_EQ(h.wells.Workers(0), 7);
  h.Run(700, 1);
  CHECK_EQ(h.wells.Workers(0), 7);  // no refill of an empty mine
  CHECK_EQ(h.Gold(1, kGoldSourceWellPassive), 20);
  t.autoFillMode = kAutoFillUnlimited;
  Host u(t);
  u.Run(600, 1);
  CHECK_EQ(u.wells.Workers(0), 18);
  CHECK_EQ(u.Gold(1, kGoldSourceWellPassive), 20);
}

// The 8C send mask: all four at first, the lock seconds once a second, a moving capture once a second, changes at once.
GMS_TEST(WellsSendMask) {
  Tuning t;
  t.transitionSeconds = 0.f;
  Host h(t);
  CHECK_EQ(static_cast<int>(h.wells.TakeSendMask(0.0)), 0x0F);
  CHECK_EQ(static_cast<int>(h.wells.TakeSendMask(0.0)), 0);
  h.Step(0.5, {});
  CHECK_EQ(static_cast<int>(h.wells.TakeSendMask(0.5)), 0);  // still in second 900 of the lock
  h.Step(1.0, {});
  CHECK_EQ(static_cast<int>(h.wells.TakeSendMask(1.0)), 0x0F);  // 899
  h.Run(900, 1);
  h.wells.TakeSendMask(900.0);
  h.Step(900.1, {});
  CHECK_EQ(static_cast<int>(h.wells.TakeSendMask(900.1)), 0);
  const WellDef w0 = V5Wells()[0];
  int sends = 0;
  const std::vector<Shaper> in = {At(0x100, 6, 2, w0)};
  const long n = std::lround((917.0 - 900.1) * 30);
  for (long k = 1; k <= n; ++k) {
    const double now = 900.1 + static_cast<double>(k) / 30;
    h.Step(now, in);
    const std::uint8_t m = h.wells.TakeSendMask(now);
    if (m & 1) ++sends;
    CHECK_EQ(static_cast<int>(m & 0x0E), 0);
  }
  CHECK_EQ(h.acc.captures.size(), static_cast<size_t>(1));
  // the start, about one refresh a second for 16 s, the capture (and the lock seconds after it)
  CHECK(sends >= 17 && sends <= 21);
}

// The same match at 1, 10 and 30 ticks a second: the capture within a tick, the gold the same.
GMS_TEST(WellsTickRateIndependent) {
  const double rates[3] = {1, 10, 30};
  double cap[3] = {0, 0, 0};
  int gold[3][2] = {{0, 0}, {0, 0}, {0, 0}};
  const WellDef w2 = V5Wells()[2];
  for (int r = 0; r < 3; ++r) {
    Tuning t;
    Host h(t);
    h.Run(900, rates[r]);
    h.Run(930, rates[r], {At(0x100, 0, 1, w2), At(0x101, 1, 1, w2, 2.f)});
    h.Run(1000, rates[r]);
    CHECK_EQ(h.captureAt.size(), static_cast<size_t>(1));
    cap[r] = h.captureAt.empty() ? 0.0 : h.captureAt[0];
    gold[r][0] = h.Gold(1, kGoldSourceWellPassive) + h.Gold(1, kGoldSourceWellBounty) + h.Gold(1, kGoldSourceObjective);
    gold[r][1] = h.Gold(2, kGoldSourceWellPassive);
  }
  for (int r = 0; r < 3; ++r) {
    CHECK(Near(cap[r], 914.0, 1.0 / rates[r] + 1e-6));
    CHECK(std::abs(gold[r][0] - gold[0][0]) <= 1);
    CHECK(std::abs(gold[r][1] - gold[0][1]) <= 1);
  }
}
