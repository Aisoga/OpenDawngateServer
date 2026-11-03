// gms/core/matchflow/camps.h (CAMPICONS): a jungle camp's minimap icon as each team's knowledge.
#include "core/matchflow/camps.h"
#include "tests/check.h"

using gms::flow::CampKnowledge;

namespace {
constexpr uint8_t kMortal = 0x02, kSpirit = 0x04, kBoth = 0x06;
}

GMS_TEST(CampIconsNoIconBeforeTheFirstSpawn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  CHECK(!k.Spawned());
  CHECK_EQ(k.Bits(), 0x01);  // the NPC bit alone: no team draws the icon
  // seeing the empty spot before the first spawn changes nothing (no respawn time: the first spawn is public anyway)
  CHECK(k.Observe(30.0, 0, kBoth, false, -1.0).empty());
  CHECK_EQ(k.Bits(), 0x01);
  const auto c = k.OnSpawned();
  CHECK_EQ(c.size(), 2u);  // both teams: one change each
  CHECK_EQ(k.Bits(), 0x07);
  CHECK(k.Icon(1));
  CHECK(k.Icon(2));
  CHECK(k.OnSpawned().empty());  // nothing new
}

GMS_TEST(CampIconsWitnessedDeathOffUntilTheRespawn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  // Mortal kills the camp in sight; Spirit doesn't see it
  const auto c = k.OnCleared(static_cast<uint8_t>(0x01 | kMortal), 160.0);
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].team, 1);
  CHECK(!c[0].icon);
  CHECK_EQ(c[0].knownRespawnAt, 160.0);
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));  // Mortal's icon off, Spirit's stays (stale)
  CHECK_EQ(k.KnownRespawnAt(1), 160.0);
  CHECK_EQ(k.KnownRespawnAt(2), -1.0);
  // Mortal still sees the empty spot: no change (already off, the time known)
  CHECK(k.Observe(120.0, 0, kMortal, false, 160.0).empty());
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));
  // the known respawn time comes: on again without sight
  const auto r = k.Observe(160.0, 0, 0, false, 160.0);
  CHECK_EQ(r.size(), 1u);
  CHECK_EQ(r[0].team, 1);
  CHECK(r[0].icon);
  CHECK_EQ(k.Bits(), 0x07);
  CHECK_EQ(k.KnownRespawnAt(1), -1.0);
}

GMS_TEST(CampIconsUnwitnessedDeathKeepsTheStaleIcon) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  CHECK(k.OnCleared(0x01, 200.0).empty());  // only the NPC bit saw it: nobody's knowledge changes
  CHECK_EQ(k.Bits(), 0x07);
  // unseen, the stale icon stays through every observation
  for (double t = 101.0; t < 150.0; t += 1.0) CHECK(k.Observe(t, 0, 0, false, 200.0).empty());
  CHECK_EQ(k.Bits(), 0x07);
  // the respawn: nothing changes for either team (their icons were on)
  CHECK(k.OnSpawned().empty());
}

// CAMPFIX (the user, 2026-10-17): a team that sees the camp empty loses the icon, also outside the reveal window (the
// respawn time unknown then: the icon comes back with the public respawn).
GMS_TEST(CampIconsSeenEmptyTurnsTheIconOff) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  CHECK(k.OnCleared(0x01, 200.0).empty());  // unwitnessed: stale
  const auto c = k.Observe(150.0, 0, kMortal, false, 200.0);  // 50 s before: outside the 10-s window
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].team, 1);
  CHECK(!c[0].icon);
  CHECK_EQ(c[0].knownRespawnAt, -1.0);
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));  // Spirit didn't look: its icon stays
  CHECK(k.Observe(151.0, 0, kMortal, false, 200.0).empty());  // still empty: nothing new
  // inside the window the time becomes known (the icon stays off)
  const auto w = k.Observe(190.0, 0, kMortal, false, 200.0);
  CHECK_EQ(w.size(), 1u);
  CHECK(!w[0].icon);
  CHECK_EQ(w[0].knownRespawnAt, 200.0);
  // the respawn: on again for Mortal
  const auto s = k.OnSpawned();
  CHECK_EQ(s.size(), 1u);
  CHECK_EQ(k.Bits(), 0x07);
  // an empty spot seen at the very respawn second (the spawn a tick later): no flicker
  k.OnCleared(0x01, 300.0);
  CHECK(k.Observe(300.0, 0, kBoth, false, 300.0).empty());
}

GMS_TEST(CampIconsTheEnemyTeamIsUnaffected) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(kSpirit, 500.0);  // Spirit's clear, Spirit watching
  CHECK(k.Icon(1));
  CHECK(!k.Icon(2));
  // Mortal sees nothing of it; its icon stays through every observation until the respawn
  for (double t = 201.0; t < 440.0; t += 1.0) CHECK(k.Observe(t, 0, 0, false, 500.0).empty());
  CHECK(k.Icon(1));
  CHECK(!k.Icon(2));
}

GMS_TEST(CampIconsRevealWindow) {
  // a buff camp (the Boar's too): 60 s before its respawn, seeing the empty spot makes the time known
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealBuffS);
  k.OnSpawned();
  k.OnCleared(0x01, 500.0);  // unwitnessed
  const auto o = k.Observe(439.0, 0, kMortal, false, 500.0);  // 61 s before: outside, the icon off, no time
  CHECK_EQ(o.size(), 1u);
  CHECK_EQ(o[0].knownRespawnAt, -1.0);
  const auto c = k.Observe(440.0, 0, kMortal, false, 500.0);  // 60 s before: inside
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].team, 1);
  CHECK(!c[0].icon);
  CHECK_EQ(c[0].knownRespawnAt, 500.0);
  CHECK_EQ(k.KnownRespawnAt(1), 500.0);
  CHECK_EQ(k.KnownRespawnAt(2), -1.0);
  CHECK_EQ(k.Bits(), static_cast<uint8_t>(0x01 | kSpirit));
  // the time comes: the icon on without sight
  const auto r = k.Observe(500.0, 0, 0, false, 500.0);
  CHECK_EQ(r.size(), 1u);
  CHECK(r[0].icon);
  CHECK_EQ(k.KnownRespawnAt(1), -1.0);
  // a small camp: 10 s
  CampKnowledge s;
  s.Init(gms::flow::kCampRevealSmallS);
  s.OnSpawned();
  s.OnCleared(0x01, 100.0);
  CHECK_EQ(s.Observe(89.0, 0, kSpirit, false, 100.0)[0].knownRespawnAt, -1.0);  // off, the time unknown
  CHECK_EQ(s.Observe(90.0, 0, kSpirit, false, 100.0).size(), 1u);
  CHECK_EQ(s.KnownRespawnAt(2), 100.0);
}

GMS_TEST(CampIconsSeenAliveTurnsTheIconOn) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  k.OnCleared(kBoth, 160.0);
  CHECK_EQ(k.Bits(), 0x01);
  // the camp is back (the director's spawn would say so too); a team that sees a member gets the icon at once
  const auto c = k.Observe(161.0, kSpirit, kSpirit, true, -1.0);
  CHECK_EQ(c.size(), 2u);  // Spirit: seen alive; Mortal: its known time came
  CHECK_EQ(k.Bits(), 0x07);
  // an alive camp seen while the icon is on: nothing
  CHECK(k.Observe(162.0, kBoth, kBoth, true, -1.0).empty());
}

GMS_TEST(CampIconsSpectatorTruth) {
  CHECK_EQ(gms::flow::CampTruthBits(true), 0x07);   // every team's bit: any spectator view mask draws it
  CHECK_EQ(gms::flow::CampTruthBits(false), 0x01);  // empty: none
}

GMS_TEST(CampIconsNeverRespawns) {
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  k.OnSpawned();
  const auto c = k.OnCleared(kMortal, -1.0);  // no respawn: off for the witness for good
  CHECK_EQ(c.size(), 1u);
  CHECK_EQ(c[0].knownRespawnAt, -1.0);
  CHECK(k.Observe(1.0e6, 0, kMortal, false, -1.0).empty());
  CHECK(!k.Icon(1));
  CHECK(k.Icon(2));
  CHECK_EQ(k.Observe(1.0e6, 0, kSpirit, false, -1.0).size(), 1u);  // Spirit sees it empty: off for good too
  CHECK(!k.Icon(2));
}

// SEC4: each team gets only its own part of the server's camp byte; the spectators the truth.
namespace {
using gms::flow::CampAudienceBits;
using gms::flow::CampAudienceOf;
constexpr int kNoTeam = gms::flow::kCampAudienceNoTeam, kTeam1 = gms::flow::kCampAudienceTeam1,
              kTeam2 = gms::flow::kCampAudienceTeam2, kSpectators = gms::flow::kCampAudienceSpectators;
}  // namespace

GMS_TEST(Sec4CampAudienceOfARecipient) {
  CHECK_EQ(CampAudienceOf(false, 1), kTeam1);
  CHECK_EQ(CampAudienceOf(false, 2), kTeam2);
  CHECK_EQ(CampAudienceOf(false, 0), kNoTeam);  // a player whose team isn't known (yet)
  CHECK_EQ(CampAudienceOf(false, 3), kNoTeam);  // no third team's bit is ever sent to a player
  CHECK_EQ(CampAudienceOf(false, 255), kNoTeam);
  CHECK_EQ(CampAudienceOf(true, 0), kSpectators);  // a spectator has no team
  CHECK_EQ(CampAudienceOf(true, 1), kSpectators);
  CHECK_EQ(static_cast<int>(gms::flow::kCampAudiences), 4);
}

GMS_TEST(Sec4CampTeamGetsOnlyItsOwnBit) {
  for (int b = 0; b < 256; ++b) {
    const uint8_t s = static_cast<uint8_t>(b);
    const int t1 = CampAudienceBits(kTeam1, s, -1), t2 = CampAudienceBits(kTeam2, s, -1);
    CHECK_EQ(t1, s & 0x03);  // the NPC bit as the server wrote it, and Mortal's
    CHECK_EQ(t2, s & 0x05);  // the NPC bit and Spirit's
    CHECK_EQ(t1 & kSpirit, 0);
    CHECK_EQ(t2 & kMortal, 0);
    CHECK_EQ(static_cast<int>(CampAudienceBits(kNoTeam, s, -1)), s & 0x01);
    // a team never gets the truth's other bits either
    CHECK_EQ(static_cast<int>(CampAudienceBits(kTeam1, s, 0x07)), t1);
    CHECK_EQ(static_cast<int>(CampAudienceBits(kTeam2, s, 0x07)), t2);
  }
}

GMS_TEST(Sec4CampSpectatorsGetTheTruth) {
  // a camp Mortal saw cleared (0x05) that is empty: the spectators 0x01; alive but stale for Spirit (0x03): 0x07
  CHECK_EQ(static_cast<int>(CampAudienceBits(kSpectators, 0x05, gms::flow::CampTruthBits(false))), 0x01);
  CHECK_EQ(static_cast<int>(CampAudienceBits(kSpectators, 0x03, gms::flow::CampTruthBits(true))), 0x07);
  // no truth (the parasite's camp, camp_icons other than 3): the server's byte as it is, both bits
  CHECK_EQ(static_cast<int>(CampAudienceBits(kSpectators, 0x07, -1)), 0x07);
  CHECK_EQ(static_cast<int>(CampAudienceBits(kSpectators, 0x05, -1)), 0x05);
}

// A team's copy of the camp list changes (so its 8D delta is not empty) exactly when its own bits change: the delta
// writer compares each member's hash against the copy's field state, and the copy holds CampAudienceBits.
GMS_TEST(Sec4CampNoDeltaWhenOnlyTheOtherTeamChanged) {
  for (int a = 0; a < 8; ++a)
    for (int b = 0; b < 8; ++b) {
      const uint8_t x = static_cast<uint8_t>(a), y = static_cast<uint8_t>(b);
      const bool t1Changed = CampAudienceBits(kTeam1, x, -1) != CampAudienceBits(kTeam1, y, -1);
      const bool t2Changed = CampAudienceBits(kTeam2, x, -1) != CampAudienceBits(kTeam2, y, -1);
      CHECK_EQ(t1Changed, ((x ^ y) & 0x03) != 0);
      CHECK_EQ(t2Changed, ((x ^ y) & 0x05) != 0);
    }
  // through the knowledge model: what each team's copy holds after every event, and when it would send a delta
  struct Sent {
    int t1 = -1, t2 = -1;
    int deltas1 = 0, deltas2 = 0;
    void Step(uint8_t server) {
      const int n1 = CampAudienceBits(kTeam1, server, -1), n2 = CampAudienceBits(kTeam2, server, -1);
      if (n1 != t1) ++deltas1;
      if (n2 != t2) ++deltas2;
      t1 = n1;
      t2 = n2;
    }
  };
  CampKnowledge k;
  k.Init(gms::flow::kCampRevealSmallS);
  Sent s;
  s.Step(k.Bits());  // the full ones at the join: 0x01 each
  CHECK_EQ(s.t1, 0x01);
  CHECK_EQ(s.t2, 0x01);
  k.OnSpawned();  // public: both
  s.Step(k.Bits());
  CHECK_EQ(s.t1, 0x03);
  CHECK_EQ(s.t2, 0x05);
  CHECK_EQ(s.deltas1, 2);
  CHECK_EQ(s.deltas2, 2);
  k.OnCleared(kSpirit, 160.0);  // Spirit alone saw the clear: the server's byte 0x07 -> 0x03
  CHECK_EQ(k.Bits(), 0x03);
  s.Step(k.Bits());
  CHECK_EQ(s.deltas1, 2);  // nothing to Mortal: its icon stays (stale), and no update tells it Spirit saw anything
  CHECK_EQ(s.deltas2, 3);
  CHECK_EQ(s.t2, 0x01);
  k.Observe(120.0, 0, kMortal, false, 160.0);  // Mortal sees the spot empty: its icon off
  CHECK_EQ(k.Bits(), 0x01);
  s.Step(k.Bits());
  CHECK_EQ(s.deltas1, 3);
  CHECK_EQ(s.deltas2, 3);  // nothing to Spirit
  k.Observe(160.0, 0, 0, false, 160.0);  // Spirit's known time came: its icon on without sight
  CHECK_EQ(k.Bits(), 0x05);
  s.Step(k.Bits());
  CHECK_EQ(s.deltas1, 3);  // nothing to Mortal: it can't learn when Spirit knew the respawn time
  CHECK_EQ(s.deltas2, 4);
  CHECK_EQ(s.t1, 0x01);
  CHECK_EQ(s.t2, 0x05);
}
