// CAMPREACH (gms/core/agents/camp_crowd.h): a camp's bigger creature isn't blocked by its smaller camp-mates, everyone
// else collides with the creatures as before. CAMPLEASH (gms/core/agents/leash.h): a target in the camp's area, or one
// the creature hits, outlasts the chase time and the group's drop; kiting still leashes.
#include "core/agents/camp_crowd.h"
#include "core/agents/leash.h"
#include "tests/check.h"

using namespace gms::agents;

namespace {

// LocomotionData_InitRepulsorFlags (0x0088E750): an NPC, a player-controlled mover, NPC type 12
constexpr RepulsorFlags kNpc = {0xFFFFFFFBu, 2u};
constexpr RepulsorFlags kPlayer = {0xFFFFFFFBu, 1u};
constexpr RepulsorFlags kType12 = {0xFFFFFFFFu, 4u};

// CAMPFINISH's creep leash (the defaults): own 18, target 22, home radius 10, chase 5 s
LeashOptions CreepLeash() {
  LeashOptions o;
  o.ownLimit = 18.f;
  o.targetMargin = 4.f;
  o.homeRadius = 10.f;
  o.chaseMs = 5000;
  return o;
}

}  // namespace

GMS_TEST(CampreachTierFlags) {
  const RepulsorFlags minor = CampCreepRepulsor(0, kNpc), medium = CampCreepRepulsor(1, kNpc),
                      major = CampCreepRepulsor(2, kNpc), uber = CampCreepRepulsor(3, kNpc);
  CHECK_EQ(minor.identity, 0x100u);
  CHECK_EQ(medium.identity, 0x200u);
  CHECK_EQ(major.identity, 0x400u);
  CHECK_EQ(uber.identity, 0x800u);
  CHECK_EQ(minor.blockage, 0xFFFFFFFBu);  // the smallest keeps the client's blockage
  CHECK_EQ(major.blockage, 0xFFFFFFFBu & ~0x300u);
  // the report: the major walks through (isn't blocked by) the minor and the medium; they still give way to it
  CHECK(!RepulsorBlocks(major.blockage, minor.identity));
  CHECK(!RepulsorBlocks(major.blockage, medium.identity));
  CHECK(!RepulsorBlocks(medium.blockage, minor.identity));
  CHECK(RepulsorBlocks(minor.blockage, major.identity));
  CHECK(RepulsorBlocks(medium.blockage, major.identity));
  CHECK(!RepulsorBlocks(uber.blockage, major.identity));
  // creatures of one tier still collide
  CHECK(RepulsorBlocks(minor.blockage, minor.identity));
  CHECK(RepulsorBlocks(major.blockage, major.identity));
  // everyone else collides with every creature as before (players, other NPCs, type 12), and the creatures with them
  for (const RepulsorFlags& c : {minor, medium, major, uber}) {
    CHECK(RepulsorBlocks(kPlayer.blockage, c.identity));
    CHECK(RepulsorBlocks(kNpc.blockage, c.identity));
    CHECK(RepulsorBlocks(kType12.blockage, c.identity));
    CHECK(RepulsorBlocks(c.blockage, kPlayer.identity));
    CHECK(RepulsorBlocks(c.blockage, kNpc.identity));
    CHECK(!RepulsorBlocks(c.blockage, kType12.identity));  // as the client's NPCs
  }
}

GMS_TEST(CampreachLeavesOthersAlone) {
  const RepulsorFlags none = {0u, 0u};  // noRepulsion
  CHECK_EQ(CampCreepRepulsor(2, none).blockage, 0u);
  CHECK_EQ(CampCreepRepulsor(2, none).identity, 0u);
  CHECK_EQ(CampCreepRepulsor(2, kPlayer).identity, 1u);
  CHECK_EQ(CampCreepRepulsor(2, kType12).identity, 4u);
  CHECK_EQ(CampCreepRepulsor(4, kNpc).identity, 2u);  // no such tier
  CHECK_EQ(CampCreepRepulsor(-1, kNpc).blockage, 0xFFFFFFFBu);
  CHECK_EQ(CreepTierIdentity(4), 0u);
}

GMS_TEST(CampleashChaseTimeSparesTheCamp) {
  const LeashOptions o = CreepLeash();
  // a bot match: a Boar major 10.6 from its spot, its target 6.0 from it, no hit for 5 s: kept now
  LeashState s;
  UpdateLeash(&s, o, 7u, 10.6f, 0, 6.f);
  CHECK_EQ(LeashSelfCheck(o, s, 10.6f, 12800), static_cast<int>(kLeashKeep));
  CHECK_EQ(s.outSinceMs, -1);  // no clock while the target is in the area
  // the target steps 12 out (outside the home radius): the clock starts then, and runs out 5 s later
  UpdateLeash(&s, o, 7u, 10.6f, 13000, 12.f);
  CHECK_EQ(LeashSelfCheck(o, s, 10.6f, 17999), static_cast<int>(kLeashKeep));
  CHECK_EQ(LeashSelfCheck(o, s, 10.6f, 18000), static_cast<int>(kLeashChase));
  // the own leash holds whatever the target does
  UpdateLeash(&s, o, 7u, 18.5f, 18500, 2.f);
  CHECK_EQ(LeashSelfCheck(o, s, 18.5f, 18500), static_cast<int>(kLeashOwn));
  // an unknown target distance (-1) keeps CAMPFINISH's rule
  LeashState u;
  UpdateLeash(&u, o, 7u, 12.f, 0);
  CHECK_EQ(LeashSelfCheck(o, u, 12.f, 5000), static_cast<int>(kLeashChase));
  // leash_area=0: CAMPFINISH's rule
  LeashOptions old = o;
  old.areaHolds = false;
  LeashState t;
  UpdateLeash(&t, old, 7u, 10.6f, 0, 6.f);
  CHECK_EQ(LeashSelfCheck(old, t, 10.6f, 5000), static_cast<int>(kLeashChase));
}

GMS_TEST(CampleashGroupDropSparesTheFighter) {
  const LeashOptions o = CreepLeash();
  LeashGroupDrops g;
  g.Drop(1u, 7u, 2000);  // a camp-mate dropped target 7 at 0 by its own leash or chase time
  CHECK(g.Ignored(1u, 7u, 100));
  // the medium fights it 0.9 from home, the target 5.9 from its spot: in its area, kept
  LeashState medium;
  UpdateLeash(&medium, o, 7u, 0.9f, 0, 5.9f);
  CHECK(LeashGroupHolds(o, medium, 7u, 5.9f, 100));
  // a ranged attacker 17 out that the member hits: kept while the member's hits keep coming
  LeashState hitter;
  UpdateLeash(&hitter, o, 7u, 3.f, 0, 17.f);
  NoteLeashHit(&hitter, 7u, 0);
  CHECK(LeashGroupHolds(o, hitter, 7u, 17.f, 4999));
  CHECK(!LeashGroupHolds(o, hitter, 7u, 17.f, 5000));
  // kiting: a member that hasn't hit the target, the target 17 out: it lets go with its camp
  LeashState chaser;
  UpdateLeash(&chaser, o, 7u, 12.f, 0, 17.f);
  CHECK(!LeashGroupHolds(o, chaser, 7u, 17.f, 100));
  // a hit on someone else doesn't count; another target id doesn't count
  NoteLeashHit(&chaser, 8u, 50);
  CHECK(!LeashGroupHolds(o, chaser, 7u, 17.f, 100));
  CHECK(!LeashGroupHolds(o, hitter, 9u, 17.f, 100));
  // unknown distance, nothing hit: dropped; leash_area=0: dropped
  CHECK(!LeashGroupHolds(o, chaser, 7u, -1.f, 100));
  LeashOptions old = o;
  old.areaHolds = false;
  CHECK(!LeashGroupHolds(old, medium, 7u, 5.9f, 100));
  CHECK(LeashTargetInArea(o, 10.f) && !LeashTargetInArea(o, 10.1f) && !LeashTargetInArea(o, -1.f));
}
