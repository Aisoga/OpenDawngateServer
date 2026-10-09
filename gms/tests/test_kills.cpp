// gms/core/damage (D1h): player kill bookkeeping and the death recap (docs/DAMAGE.md *D1h*). The two-player rules
// (kills, assists, streaks, multi-kills, first blood, a player kill's gold and XP) are only checked here until G3 runs
// two players in one match.
#include <cmath>
#include <cstdint>
#include <vector>

#include "core/common/bytes.h"
#include "core/damage/kills.h"
#include "core/damage/recap.h"
#include "core/protocol/wire.h"
#include "tests/check.h"

using namespace gms::damage;

namespace {
bool Near(double a, double b, double eps) { return std::fabs(a - b) <= eps; }

// The data's RewardsTuning arrays (re/out/assetdata/RewardsTuning.json) and LevelUpTuning's first levels.
const float kKillStreakGold[] = {300.f, 350.f, 400.f, 450.f, 500.f};
const float kDeathStreakGold[] = {300.f, 270.f, 189.f, 132.f, 93.f, 65.f, 46.f, 32.f, 22.f};
const float kOne[] = {1.f};
const float kLowerXp[] = {1.f, 0.9f, 0.675f, 0.506f, 0.38f};
const float kHigherXp[] = {1.f, 1.1f, 1.375f, 1.718f, 2.148f};
const float kXpShare[] = {1.f, 0.65f, 0.4333f, 0.325f, 0.26f};
const float kAssistShare[] = {0.7f};
const float kXpRequired[] = {0.f, 0.f, 260.f, 610.f, 1050.f, 1580.f};

PlayerKillTuning DataTuning() {
  PlayerKillTuning t;
  t.killStreakGold = {kKillStreakGold, 5};
  t.deathStreakGold = {kDeathStreakGold, 9};
  t.lowerLevelGold = {kOne, 1};
  t.higherLevelGold = {kOne, 1};
  t.lowerLevelXp = {kLowerXp, 5};
  t.higherLevelXp = {kHigherXp, 5};
  t.xpNearbyShare = {kXpShare, 5};
  t.assistShare = {kAssistShare, 1};
  t.firstBloodGold = 100.f;
  t.killXpPercent = 0.6f;
  return t;
}
}  // namespace

// AssistTracker_RecordHit's matrix: [attacker * 10 + target], u64 ms; enemies within 10 s count, not the killer.
GMS_TEST(KillsAssisters) {
  std::uint64_t m[kMaxPlayers * kMaxPlayers] = {};
  const int team[kMaxPlayers] = {1, 1, 1, 2, 2, 0, 0, 0, 0, 0};
  m[3 * 10 + 0] = 50000;  // player 3 hit player 0 at 50 s
  m[4 * 10 + 0] = 39000;  // player 4 at 39 s (too old at 50 s)
  m[1 * 10 + 0] = 49000;  // a teammate's entry (cannot happen; ignored)
  m[4 * 10 + 1] = 49500;  // player 4 hit player 1, not the victim
  CHECK_EQ(kAssistMatrixBytes, 0x320u);
  std::vector<int> a = Assisters(m, 0, -1, team, 50000);
  CHECK_EQ(a.size(), static_cast<size_t>(1));
  CHECK_EQ(a[0], 3);
  CHECK(Assisters(m, 0, 3, team, 50000).empty());  // the killer is no assister
  m[4 * 10 + 0] = 41000;
  a = Assisters(m, 0, 3, team, 50000);
  CHECK_EQ(a.size(), static_cast<size_t>(1));
  CHECK_EQ(a[0], 4);
  ClearAssists(m, 0);
  CHECK(Assisters(m, 0, -1, team, 50000).empty());
  CHECK_EQ(m[4 * 10 + 1], 49500ull);  // other victims' credits stay
}

// Kills, deaths, assists, streaks, multi-kills, first blood.
GMS_TEST(KillsBook) {
  KillBook b;
  // player 0 (team 1) kills 5 and 6 (team 2) within 10 s, with player 1 assisting the first
  KillOutcome o = b.PlayerDeath(5, 0, {1, 0, 5, 1}, 100000, 10000);
  CHECK_EQ(o.killer, 0);
  CHECK(o.firstBlood);
  CHECK_EQ(o.extraFlags, kEventFirstKill);
  CHECK_EQ(o.assisters.size(), static_cast<size_t>(1));  // the killer, the victim and the duplicate dropped
  CHECK_EQ(o.killerPrevStreak, 0u);
  CHECK_EQ(o.killerStreak, 1u);
  CHECK_EQ(o.multiKill, 1u);
  o = b.PlayerDeath(6, 0, {}, 109000, 10000);
  CHECK(!o.firstBlood);
  CHECK_EQ(o.extraFlags, 0u);
  CHECK_EQ(o.multiKill, 2u);
  CHECK_EQ(o.killerPrevStreak, 1u);
  o = b.PlayerDeath(7, 0, {}, 118000, 10000);  // 9 s after the second: a triple
  CHECK_EQ(o.multiKill, 3u);
  CHECK_EQ(b.t[0].multiKills[2], 0u);  // counted once, at its largest size
  CHECK_EQ(b.t[0].multiKills[3], 1u);
  o = b.PlayerDeath(5, 0, {}, 140000, 10000);  // 22 s later: a new sequence
  CHECK_EQ(o.multiKill, 1u);
  CHECK_EQ(o.victimDeathStreak, 1u);  // player 5 died before without a kill
  CHECK_EQ(b.t[0].kills, 4u);
  CHECK_EQ(b.t[0].killStreak, 4u);
  CHECK_EQ(b.t[0].bestStreak, 4u);
  CHECK_EQ(b.t[1].assists, 1u);
  CHECK_EQ(b.t[5].deaths, 2u);
  // player 0 dies to an NPC: its streak is the bounty, then ends; no killer, no kill
  o = b.PlayerDeath(0, -1, {}, 150000, 10000);
  CHECK_EQ(o.killer, -1);
  CHECK_EQ(o.victimKillStreak, 4u);
  CHECK_EQ(b.t[0].killStreak, 0u);
  CHECK_EQ(b.t[0].bestStreak, 4u);
  CHECK_EQ(b.t[0].deaths, 1u);
  CHECK_EQ(b.t[0].kills, 4u);
  b.Creep(0);
  b.Creep(0, 2);
  CHECK_EQ(b.t[0].creeps, 3u);
  CHECK(CountsAsCreep(0) && CountsAsCreep(3) && !CountsAsCreep(9) && !CountsAsCreep(12) && !CountsAsCreep(-1));
}

// The bounty: playerKillStreakGold by the victim's kill streak, else playerDeathStreakGold by its death streak.
GMS_TEST(KillsBaseGold) {
  const PlayerKillTuning t = DataTuning();
  CHECK_EQ(PlayerKillBaseGold(t, 0, 0), 300.f);
  CHECK_EQ(PlayerKillBaseGold(t, 1, 0), 350.f);
  CHECK_EQ(PlayerKillBaseGold(t, 9, 3), 500.f);
  CHECK_EQ(PlayerKillBaseGold(t, 0, 2), 189.f);
  CHECK_EQ(PlayerKillBaseGold(t, 0, 40), 22.f);
  CHECK_EQ(LevelStepXp(kXpRequired, 6, 1), 260.f);
  CHECK_EQ(LevelStepXp(kXpRequired, 6, 3), 440.f);
  CHECK_EQ(LevelStepXp(kXpRequired, 6, 9), 530.f);  // the last step at the top
  CHECK(Near(LevelMultiplier(t.lowerLevelXp, t.higherLevelXp, 5, 3), 0.675, 1e-6));  // victim 2 levels lower
  CHECK(Near(LevelMultiplier(t.lowerLevelXp, t.higherLevelXp, 3, 5), 1.375, 1e-6));  // 2 levels higher
  CHECK_EQ(LevelMultiplier(t.lowerLevelXp, t.higherLevelXp, 4, 4), 1.f);
}

// A first-blood kill with one assister: gold to killer (bounty + first blood) and assister (0.7 x bounty), XP shared
// by the area, the killer and the assister.
GMS_TEST(KillsPlayerRewards) {
  const PlayerKillTuning t = DataTuning();
  KillBook b;
  const KillOutcome o = b.PlayerDeath(5, 0, {1}, 60000, 10000);
  std::vector<KillReceiver> rc(3);
  rc[0].player = 0;  // the killer, level 2, a predator (kill gold 1.5, assist 2.0, XP 1.3)
  rc[0].level = 2;
  rc[0].killGoldModifier = 1.5f;
  rc[0].assistGoldModifier = 2.f;
  rc[0].xpModifier = 1.3f;
  rc[1].player = 1;  // the assister, level 1, far away
  rc[1].level = 1;
  rc[2].player = 2;  // a teammate, level 1, in the area but no assist
  rc[2].level = 1;
  rc[2].inArea = true;
  const PlayerKillRewards r = ComputePlayerKillRewards(t, o, 1, LevelStepXp(kXpRequired, 6, 1), rc);
  CHECK_EQ(r.baseGold, 300.f);
  CHECK_EQ(r.firstBloodGold, 100.f);
  CHECK(Near(r.xpBase, 156.0, 1e-4));
  CHECK_EQ(r.gold.size(), static_cast<size_t>(2));
  CHECK_EQ(r.gold[0].player, 0);
  CHECK(r.gold[0].lastHit);
  CHECK(Near(r.gold[0].amount, 450.0, 1e-4));  // 300 x 1.5
  CHECK_EQ(r.gold[1].player, 1);
  CHECK(Near(r.gold[1].amount, 210.0, 1e-4));  // 300 x 0.7 / 1
  CHECK_EQ(r.xp.size(), static_cast<size_t>(3));
  // three takers: share 0.4333; the killer is a level above the victim (lower[1] 0.9) and x 1.3
  CHECK(Near(r.xp[0].amount, 156.0 * 0.4333 * 0.9 * 1.3, 1e-3));
  CHECK(Near(r.xp[1].amount, 156.0 * 0.4333, 1e-3));
  CHECK(Near(r.xp[2].amount, 156.0 * 0.4333, 1e-3));
  // no killer (an NPC's kill): nothing
  KillOutcome none;
  none.victim = 5;
  CHECK(ComputePlayerKillRewards(t, none, 1, 260.f, rc).gold.empty());
}

// The death recap's bytes for a fixed fight (cross-checked with an independent encoding in Python, struct '<IiBf'):
// a melee minion's basics (12 x 2 physical, 50 magical) and a Binding's three shots, the death at the third.
GMS_TEST(DeathRecapGolden) {
  CHECK_EQ(kDeathRecapWireId, static_cast<std::uint8_t>(gms::kGmsDeathRecap));
  const std::uint32_t binding = 0xA64E014D, minion = 0x6D1B7E21, attack = 0x880B0EC6, melee = 0x2F7C1A55;
  std::vector<RecapHit> hits;
  auto add = [&hits](std::uint64_t ms, float d, unsigned desc, std::uint32_t ab, std::uint32_t noun, int npc) {
    RecapHit h;
    h.ms = ms;
    h.damage = d;
    h.descLo = desc;
    h.ability = ab;
    h.noun = noun;
    h.npcType = npc;
    h.team = 2;
    hits.push_back(h);
  };
  add(500, 12.f, 0x43, melee, minion, 0);
  add(1000, 157.89f, 0x440, attack, binding, 9);
  add(1500, 12.f, 0x43, melee, minion, 0);
  add(2500, 181.82f, 0x440, attack, binding, 9);
  add(3000, 50.f, 0x80, 0x12345678, minion, 0);
  add(4000, 205.74f, 0x440, attack, binding, 9);
  RecapSummary s;
  const std::vector<std::uint8_t> m = BuildDeathRecap(hits, 4000, &s);
  CHECK_EQ(m.size(), static_cast<size_t>(1 + kRecapHeaderSize + 3 * kRecapTypeEntrySize + 3 * kRecapTimelineEntrySize));
  CHECK_HEX(m,
            "c9cddc1a440000604002cd5c0e440000484200000000020100034d014ea60900000002cd5c0844217e1b6d00000000020000c041"
            "217e1b6d0000000002000048424d014ea60900000002cd5c0844c60e0b8803217e1b6d0000000002000048427856341201217e1b"
            "6d00000000020000c041551a7c2f02");
  CHECK_EQ(s.rows, 2u);
  CHECK_EQ(s.entries, 3u);
  CHECK_EQ(s.hits, 6u);
  CHECK(Near(s.seconds, 3.5, 1e-6));
  CHECK(Near(s.total, 619.45, 1e-2));
  // no hits: an empty recap (header only, all zero)
  CHECK_HEX(BuildDeathRecap({}, 4000), "c900000000000000000000000000000000000000000000000000");
}

// Sources hit in the same millisecond (d1h_run1's probe: a minion's three hits, then the Binding's lethal one): the
// later hit's source is the more recent, so the killer comes first.
GMS_TEST(DeathRecapSameTick) {
  std::vector<RecapHit> hits(4);
  for (int i = 0; i < 4; ++i) {
    hits[i].ms = 110000;
    hits[i].damage = i < 3 ? 30.f : 5000.f;
    hits[i].descLo = i < 3 ? 0x43u : 0x800u;
    hits[i].noun = i < 3 ? 0x01E5D362u : 0x0E234619u;
    hits[i].npcType = i < 3 ? 0 : 9;
    hits[i].team = 2;
  }
  const std::vector<std::uint8_t> m = BuildDeathRecap(hits, 110000);
  const size_t timeline = 1 + kRecapHeaderSize + 2 * kRecapTypeEntrySize;  // physical and true lists, one entry each
  CHECK_EQ(m.size(), timeline + 2 * kRecapTimelineEntrySize);
  CHECK_EQ(m[timeline + 4], static_cast<std::uint8_t>(9));  // the Binding's NPC type first
  CHECK_EQ(m[timeline + kRecapTimelineEntrySize + 4], static_cast<std::uint8_t>(0));
}

// Which hits the recap covers: within 30 s of the death, back to the first gap of 5 s or more.
GMS_TEST(DeathRecapWindow) {
  std::vector<RecapHit> hits;
  for (std::uint64_t ms : {1000ull, 20000ull, 23000ull, 27500ull, 31000ull, 40000ull}) {
    RecapHit h;
    h.ms = ms;
    h.damage = 10.f;
    hits.push_back(h);
  }
  const RecapTuning t;  // 5 s / 30 s
  std::vector<RecapHit> w = RecapWindow(hits, 33000, t);
  CHECK_EQ(w.size(), static_cast<size_t>(4));  // 20 s, 23 s, 27.5 s, 31 s (the 1 s hit is 32 s before the death)
  CHECK_EQ(w.front().ms, 20000ull);
  CHECK_EQ(w.back().ms, 31000ull);  // the hit after the death is not part of it
  CHECK(RecapWindow(hits, 60000, t).empty());  // 20 s after the last hit: no fight
  RecapLog log;
  for (const RecapHit& h : hits) log.Add(h, t.longSeconds);
  CHECK_EQ(log.hits.size(), static_cast<size_t>(5));  // the 1 s hit is older than 30 s at 40 s
}
