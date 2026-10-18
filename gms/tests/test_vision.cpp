// gms/core/vision (D2g-fog-core): the fog-of-war vision model and the per-player transitions, without the engine.
// Distances and radii are the data's: shapers 35.3 + 3.0 hysteresis (BattlegroundsGameplayTuning), minions 29, base
// towers 50, the druid trees' area radius 14.48 (Noun.json). The golden position is D2g-static's: the shaper's start
// (-234.93, -229.92) quantizes to block 0x4543, xy 0x23DB.
#include <cmath>
#include <cstdint>
#include <vector>

#include "core/common/bytes.h"
#include "core/vision/fog.h"
#include "core/vision/pursuit.h"
#include "core/vision/vision.h"
#include "tests/check.h"

using gms::Bytes;
using namespace gms::vision;

namespace {

constexpr uint8_t kNpc = 0, kMortal = 1, kSpirit = 2;
constexpr uint8_t kBitNpc = 1, kBitMortal = 2, kBitSpirit = 4;

Entity Shaper(uint32_t id, uint8_t team, float x, float y) {
  Entity e;
  e.id = id;
  e.team = team;
  e.x = x;
  e.y = y;
  e.tracked = true;
  e.visionRadius = kPlayerVisionRadius;
  e.hysteresis = kPlayerVisionHysteresis;
  return e;
}

Entity Minion(uint32_t id, uint8_t team, float x, float y) {
  Entity e;
  e.id = id;
  e.team = team;
  e.x = x;
  e.y = y;
  e.tracked = true;
  e.visionRadius = 29.f;
  return e;
}

// A tracked object without vision (e.g. a projectile: fowTracked, radius 0)
Entity Target(uint32_t id, uint8_t team, float x, float y) {
  Entity e;
  e.id = id;
  e.team = team;
  e.x = x;
  e.y = y;
  e.tracked = true;
  return e;
}

uint8_t Run(VisionModel& m, const std::vector<Entity>& es, size_t i, const Params& p = Params()) {
  m.Update(es, p);
  return m.Bits(i);
}

int Count(const std::vector<FogAction>& a, FogActionKind k) {
  int n = 0;
  for (const FogAction& x : a)
    if (x.kind == k) ++n;
  return n;
}

}  // namespace

// Shapers: 35.3 to enter, 35.3 + 3.0 to stay (the record's radius and hysteresis, GameObject_GetVisionRadius).
GMS_TEST(VisionShaperRadiusHysteresis) {
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Target(0x202, kSpirit, 35.4f, 0.f)};
  CHECK_EQ(Run(m, es, 1), kBitSpirit);  // just outside: own team only
  es[1].x = 35.3f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // the edge counts (<=)
  es[1].x = 38.2f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // inside the hysteresis: still seen
  es[1].x = 38.4f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit);  // beyond 38.3: lost
  es[1].x = 36.f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit);  // re-entering needs 35.3 again
  es[1].x = 30.f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);
  CHECK_EQ(m.BitsOf(0x202), kBitSpirit | kBitMortal);
  CHECK_EQ(m.BitsOf(0x101), kBitMortal);  // the shaper itself: nothing of team Spirit sees it
  CHECK_EQ(m.BitsOf(0x999), 0);
  CHECK_EQ(m.Sources(), 1);
}

// Minions see 29 units, without hysteresis; vision is per team, shared by all its sources.
GMS_TEST(VisionTeamSourcesShared) {
  VisionModel m;
  std::vector<Entity> es = {Minion(0x101, kMortal, 0.f, 0.f), Minion(0x102, kMortal, 100.f, 0.f),
                            Target(0x202, kSpirit, 128.9f, 0.f), Target(0x203, kSpirit, 129.1f, 0.f)};
  m.Update(es);
  CHECK_EQ(m.Bits(2), kBitSpirit | kBitMortal);  // the second minion's 29
  CHECK_EQ(m.Bits(3), kBitSpirit);
  es[3].x = 129.05f;  // no hysteresis for a minion
  m.Update(es);
  CHECK_EQ(m.Bits(3), kBitSpirit);
  // the Spirit objects see nothing (no radius): the Mortal minions stay Mortal-only
  CHECK_EQ(m.Bits(0), kBitMortal);
  // a neutral camp creep (team NPC, radius 20) gives the NPC team its vision
  Entity creep = Minion(0x301, kNpc, 0.f, 15.f);
  creep.visionRadius = 20.f;
  es.push_back(creep);
  m.Update(es);
  CHECK_EQ(m.Bits(0), kBitMortal | kBitNpc);
  CHECK_EQ(m.Bits(4), kBitNpc | kBitMortal);  // and the Mortal minion 15 units away sees the creep
}

// Never fogged: no record, noun types Tower/obstacle/titan, NPC type 10, no fowControlsObjectVisibility.
GMS_TEST(VisionNeverFogged) {
  CHECK(NeverFoggedType(kNounTypeTower, -1));
  CHECK(NeverFoggedType(kNounTypeObstacle, -1));
  CHECK(NeverFoggedType(kNounTypeTitan, -1));
  CHECK(NeverFoggedType(113409280, kNpcTypeGoldMine));  // a gold pile (interactable, NPC type 10)
  CHECK(!NeverFoggedType(113409280, 0));                // a creature
  VisionModel m;
  Entity tower = Minion(0x401, kSpirit, 500.f, 0.f);
  tower.neverFogged = true;
  tower.visionRadius = 35.3f;  // Bindings are sources too
  Entity untracked = Target(0x402, kSpirit, 500.f, 10.f);
  untracked.tracked = false;   // e.g. bindingarchon: no record
  Entity shop = Target(0x403, kSpirit, 500.f, 20.f);
  shop.controlsVisibility = false;  // itemshoppe_archon_*
  Entity hidden = Target(0x404, kSpirit, 500.f, 25.f);
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), tower, untracked, shop, hidden};
  m.Update(es);
  CHECK_EQ(m.Bits(1), kAllTeams);
  CHECK_EQ(m.Bits(2), kAllTeams);
  CHECK_EQ(m.Bits(3), kAllTeams);
  CHECK_EQ(m.Bits(4), kBitSpirit);  // a normal tracked object beside them stays fogged
  // the tower still gives its team vision: the shaper near it is seen by Spirit
  es[0].x = 480.f;
  m.Update(es);
  CHECK_EQ(m.Bits(0), kBitMortal | kBitSpirit);
  CHECK_EQ(m.Bits(4), kBitSpirit | kBitMortal);
}

// Sources: only live, tracked objects with a radius, or anonymous proxies (id 0); a team >= 3 sees nothing.
GMS_TEST(VisionSourceKinds) {
  VisionModel m;
  Entity dead = Shaper(0x101, kMortal, 0.f, 0.f);
  dead.alive = false;
  Entity untracked = Shaper(0x102, kMortal, 0.f, 0.f);
  untracked.tracked = false;
  Entity noTeam = Shaper(0x103, 7, 0.f, 0.f);
  std::vector<Entity> es = {dead, untracked, noTeam, Target(0x202, kSpirit, 10.f, 0.f)};
  CHECK_EQ(Run(m, es, 3), kBitSpirit);
  CHECK_EQ(m.Sources(), 0);
  // a ward: a Vision_Object (radius 0.5) with SetOverrideVisionRadius, i.e. a tracked source with its own radius
  Entity ward = Target(0x105, kMortal, 0.f, 0.f);
  ward.visionRadius = 12.f;
  es.push_back(ward);
  CHECK_EQ(Run(m, es, 3), kBitSpirit | kBitMortal);
  // a vision proxy record (nFogOfWar.CreateVisionProxyObject): no object id, a fixed point
  es.pop_back();
  Entity proxy;
  proxy.id = 0;
  proxy.team = kMortal;
  proxy.x = 10.f;
  proxy.y = 5.f;
  proxy.visionRadius = 6.f;
  es.push_back(proxy);
  CHECK_EQ(Run(m, es, 3), kBitSpirit | kBitMortal);
  CHECK_EQ(m.Bits(4), 0);  // the proxy itself gets no answer
  CHECK_EQ(m.Sources(), 1);
  // the noTeam object is a target like any other (own bit none)
  CHECK_EQ(m.Bits(2), 0);
}

// fowAreaRadius (the druid trees' 14.48): seen when a source's circle reaches the area [assumed].
GMS_TEST(VisionAreaRadius) {
  VisionModel m;
  Entity tree = Target(0x501, kSpirit, 35.3f + 14.48f, 0.f);
  tree.areaRadius = 14.48f;
  std::vector<Entity> es = {Minion(0x101, kMortal, 6.f, 0.f), tree};
  es[0].visionRadius = 0.f;  // not a source
  es.push_back(Shaper(0x102, kMortal, 0.f, 0.f));
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);
  es[1].x += 0.1f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // the shaper's hysteresis keeps it
  es[1].x += 3.f;
  CHECK_EQ(Run(m, es, 1), kBitSpirit);
}

// fowUseOwnerVision: an orbit or book follows its owner's answer [assumed]; a cycle doesn't hang.
GMS_TEST(VisionOwnerVision) {
  VisionModel m;
  Entity owner = Shaper(0x202, kSpirit, 40.f, 0.f);
  Entity orbit = Target(0x203, kSpirit, 10.f, 0.f);  // close to the Mortal shaper, but its owner is far
  orbit.useOwnerVision = true;
  orbit.owner = 0x202;
  std::vector<Entity> es = {orbit, Shaper(0x101, kMortal, 0.f, 0.f), owner};
  CHECK_EQ(Run(m, es, 0), kBitSpirit);
  es[2].x = 30.f;
  CHECK_EQ(Run(m, es, 0), kBitSpirit | kBitMortal);
  // the owner not among the inputs: its own test
  es[0].owner = 0x999;
  es[2].x = 40.f;
  CHECK_EQ(Run(m, es, 0), kBitSpirit | kBitMortal);
  // two objects that name each other: a takes b's answer, b (finding the cycle) its own test
  Entity a = Target(0x301, kSpirit, 300.f, 0.f), b = Target(0x302, kSpirit, 95.f, 0.f);
  a.useOwnerVision = b.useOwnerVision = true;
  a.owner = 0x302;
  b.owner = 0x301;
  std::vector<Entity> cyc = {a, b, Shaper(0x101, kMortal, 90.f, 0.f)};
  m.Update(cyc);
  CHECK_EQ(m.Bits(0), kBitSpirit | kBitMortal);  // far away, but its owner b is seen
  CHECK_EQ(m.Bits(1), kBitSpirit | kBitMortal);
}

// Death while visible: the corpse keeps last tick's answer [assumed]; a dead source stops giving vision.
GMS_TEST(VisionDeathFreezes) {
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Minion(0x202, kSpirit, 20.f, 0.f),
                            Target(0x303, kSpirit, -60.f, 0.f)};
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitSpirit | kBitMortal);
  CHECK_EQ(m.Bits(0), kBitMortal | kBitSpirit);  // the Spirit minion (29) sees the shaper
  CHECK_EQ(m.Bits(2), kBitSpirit);
  es[1].alive = false;                           // the minion dies in view
  es[0].x = -100.f;                              // and the shaper walks away
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitSpirit | kBitMortal);  // frozen: the death plays for the Mortal team
  CHECK_EQ(m.Bits(0), kBitMortal);               // the dead minion no longer sees the shaper
  CHECK_EQ(m.Bits(2), kBitSpirit);
  // a corpse that was fogged stays fogged when a source walks up
  es[2].alive = false;
  es[0].x = -70.f;
  m.Update(es);
  CHECK_EQ(m.Bits(2), kBitSpirit);
  // a dead object first seen dead (no history) gets the normal test
  Entity late = Target(0x404, kSpirit, -75.f, 0.f);
  late.alive = false;
  es.push_back(late);
  m.Update(es);
  CHECK_EQ(m.Bits(3), kBitSpirit | kBitMortal);
}

// An id missing from a tick is forgotten: its hysteresis restarts.
GMS_TEST(VisionForgetsMissingIds) {
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Target(0x202, kSpirit, 30.f, 0.f)};
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitSpirit | kBitMortal);
  m.Update({es[0]});
  es[1].x = 37.f;  // inside the hysteresis, but the object was gone for a tick
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitSpirit);
}

// The line-of-sight map: cells, rounding and clamping, and the ray (end cells left out).
GMS_TEST(VisionLosMapCells) {
  std::vector<uint8_t> cells(64 * 64, 0);
  const LosMap map = LosMap::ForLevel(cells.data(), 64, 64, 128.f, 128.f);  // 2 units per cell, origin at (32, 32)
  int cx = -1, cy = -1;
  map.CellOf(0.f, 0.f, &cx, &cy);
  CHECK_EQ(cx, 32);
  CHECK_EQ(cy, 32);
  map.CellOf(-63.f, 10.9f, &cx, &cy);  // 32 - 31.5 = 0.5 -> 0 (to even); 32 + 5.45 -> 37
  CHECK_EQ(cx, 0);
  CHECK_EQ(cy, 37);
  map.CellOf(500.f, -500.f, &cx, &cy);  // clamped
  CHECK_EQ(cx, 63);
  CHECK_EQ(cy, 0);
  for (int y = 0; y < 64; ++y) cells[static_cast<size_t>(y) * 64 + 40] = 128;  // a wall at cell column 40 (x = 16)
  CHECK_EQ(map.At(40, 5), 128);
  CHECK_EQ(map.At(64, 5), 0);
  CHECK(map.Blocked(0.f, 0.f, 30.f, 0.f));
  CHECK(map.Blocked(0.f, 0.f, 30.f, 20.f));
  CHECK(!map.Blocked(0.f, 0.f, 14.f, 0.f));
  CHECK(!map.Blocked(0.f, 0.f, 16.f, 0.f));   // the target stands in the wall's cell: left out
  CHECK(!map.Blocked(16.f, 0.f, 20.f, 0.f));  // the source stands in it
  CHECK(!map.Blocked(0.f, 0.f, 0.f, 0.f));
  CHECK(map.Blocked(30.f, 0.f, 0.f, 0.f));    // symmetric
}

GMS_TEST(VisionTerrain) {
  std::vector<uint8_t> cells(64 * 64, 0);
  for (int y = 0; y < 64; ++y) cells[static_cast<size_t>(y) * 64 + 40] = 128;
  const LosMap map = LosMap::ForLevel(cells.data(), 64, 64, 128.f, 128.f);
  Params p;
  p.terrain = &map;
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Target(0x202, kSpirit, 30.f, 0.f)};
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // without the test
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);            // behind the wall
  es[0].ignoreTerrain = true;                        // a Terrain_Transparent vision object / the shop archons
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].ignoreTerrain = false;
  cells[static_cast<size_t>(32) * 64 + 40] = 0;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);  // a gap in the wall on the ray's row
  // ABILVFX: a dynamic terrain modifier (nFogOfWar.ModifyTerrain ORs a handle bit into the cells: Kensu's R trail)
  // opens a wall and never blocks open ground; D2g-fog-core's rule (any non-zero byte blocks) behind the switch
  cells[static_cast<size_t>(32) * 64 + 40] = 0x01;  // the trail on open ground
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  cells[static_cast<size_t>(32) * 64 + 40] = 0x80 | 0x04;  // the trail across the wall
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  CHECK(!map.BlocksCell(0x01));
  CHECK(!map.BlocksCell(0x84));
  CHECK(map.BlocksCell(0x80));
  CHECK(!map.BlocksCell(0x00));
  LosMap old = map;
  old.terrainModsOpen = false;
  CHECK(old.BlocksCell(0x01));
  CHECK(old.BlocksCell(0x84));
  p.terrain = &old;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  p.terrain = &map;
  cells[static_cast<size_t>(32) * 64 + 40] = 0x01;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  cells[static_cast<size_t>(32) * 64 + 40] = 0x80;  // the modifier released: the wall again
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  // the point query uses the same map
  CHECK_EQ(m.TeamsSeeingPoint(30.f, 0.f, kSpirit, 0.f), kBitSpirit);
  CHECK_EQ(m.TeamsSeeingPoint(10.f, 0.f, kSpirit, 0.f), kBitSpirit | kBitMortal);
  // an invalid map switches the test off
  LosMap empty;
  p.terrain = &empty;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
}

// Grass [assumed rule]: an object in a patch, concealed from team T, is seen by T only from the same patch or by a
// source that ignores grass.
GMS_TEST(VisionGrass) {
  Params p;
  p.grass = true;
  VisionModel m;
  Entity hiding = Target(0x202, kSpirit, 10.f, 0.f);
  hiding.grassId = 0xD1E0DE;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), hiding};
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // grass off
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  es[0].grassId = 0x123456;  // another patch
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  es[0].grassId = 0xD1E0DE;  // the same patch
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].grassId = 0;
  es[0].ignoreGrass = true;  // Vision_Object_Grass_Transparent
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].ignoreGrass = false;
  es[1].grassConceal = 0;  // fowIgnoreGrassTest (or revealed to every team)
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[1].grassConceal = kDefaultGrassConceal & ~kBitMortal;  // AgentBlackboard_RevealInGrass for Mortal
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  // the default mask (6) leaves the NPC team's view alone
  es[1].grassConceal = kDefaultGrassConceal;
  Entity creep = Minion(0x301, kNpc, 0.f, 0.f);
  es.push_back(creep);
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitNpc);
}

// The grass map and LineOfSight_UpdateGrassId's rule (0x00918500): the id is the whole cell value of a grass cell; it
// changes only with the patch (low 24 bits), to 0 out of the grass; the packed cell (y << 16 | x) only on a change.
GMS_TEST(VisionGrassMapUpdateId) {
  std::vector<uint32_t> cells(64 * 64, 0);
  for (int y = 30; y < 35; ++y)
    for (int x = 30; x < 35; ++x) cells[static_cast<size_t>(y) * 64 + x] = 0xFF000011u;  // patch 0x11 around (0, 0)
  for (int x = 36; x < 40; ++x) cells[static_cast<size_t>(32) * 64 + x] = 0xFF000022u;    // patch 0x22 east of it
  cells[static_cast<size_t>(32) * 64 + 40] = 0x00000011u;  // no alpha: not grass, whatever its colour
  GrassMap g;
  g.cells = cells.data();
  g.geometry = LosMap::ForLevel(nullptr, 64, 64, 128.f, 128.f);  // 2 units per cell
  CHECK(g.Valid());
  uint32_t packed = 0;
  CHECK_EQ(g.At(0.f, 0.f, &packed), 0xFF000011u);
  CHECK_EQ(packed, (32u << 16) | 32u);
  CHECK_EQ(g.At(16.f, 0.f), 0u);           // cell 40: alpha 0
  CHECK_EQ(g.Raw(16.f, 0.f), 0x00000011u);
  uint32_t id = 0, xy = 0xDEAD;
  CHECK(!UpdateGrassId(g, -40.f, 0.f, &id, &xy));  // out of the grass, id 0: no change
  CHECK_EQ(xy, 0xDEADu);
  CHECK(UpdateGrassId(g, 0.f, 0.f, &id, &xy));      // into patch 0x11
  CHECK_EQ(id, 0xFF000011u);
  CHECK_EQ(xy, (32u << 16) | 32u);
  CHECK(!UpdateGrassId(g, 2.f, 2.f, &id, &xy));     // the same patch: nothing changes, not even the cell
  CHECK_EQ(xy, (32u << 16) | 32u);
  CHECK(UpdateGrassId(g, 10.f, 0.f, &id, &xy));     // patch 0x22
  CHECK_EQ(id, 0xFF000022u);
  CHECK_EQ(xy, (32u << 16) | 37u);
  CHECK(UpdateGrassId(g, 30.f, 0.f, &id, &xy));     // out
  CHECK_EQ(id, 0u);
  CHECK(UpdateGrassId(g, 0.f, 0.f, &id, &xy));
  CHECK(!UpdateGrassId(g, 16.f, 0.f, &id, &xy));    // a non-grass cell with the patch's colour: the engine keeps it
  CHECK_EQ(id, 0xFF000011u);
  GrassMap none;
  CHECK(!UpdateGrassId(none, 0.f, 0.f, &id, &xy));
}

// Grass with the map (D2g-rest): the client's fog renderer draws every grass outline as an occluder, except for a
// vision circle whose grass id is the outline's, so a patch hides what lies behind it from a source outside it.
GMS_TEST(VisionGrassRay) {
  std::vector<uint32_t> cells(64 * 64, 0);
  for (int y = 28; y < 37; ++y)
    for (int x = 36; x < 40; ++x) cells[static_cast<size_t>(y) * 64 + x] = 0x48000011u;  // patch 0x11 at x = 8..14
  for (int y = 28; y < 37; ++y)
    for (int x = 44; x < 47; ++x) cells[static_cast<size_t>(y) * 64 + x] = 0x7D000022u;  // patch 0x22 at x = 24..28
  GrassMap g;
  g.cells = cells.data();
  g.geometry = LosMap::ForLevel(nullptr, 64, 64, 128.f, 128.f);  // 2 units per cell
  CHECK(g.BlockedBetween(0.f, 0.f, 20.f, 0.f, 0, 0));                         // through patch 0x11
  CHECK(!g.BlockedBetween(0.f, 0.f, 20.f, 0.f, 0x48000011u, 0));               // from inside it
  CHECK(!g.BlockedBetween(0.f, 0.f, 20.f, 0.f, 0, 0x00000011u));               // to an object in it (by the low 24 bits)
  CHECK(!g.BlockedBetween(0.f, 10.f, 20.f, 10.f, 0, 0));                       // beside it
  CHECK(g.BlockedBetween(10.f, 0.f, 34.f, 0.f, 0x48000011u, 0));               // out of 0x11, through 0x22
  CHECK(!g.BlockedBetween(0.f, 0.f, 0.f, 0.f, 0, 0));
  CHECK(!GrassMap().BlockedBetween(0.f, 0.f, 20.f, 0.f, 0, 0));
  Params p;
  p.grass = true;
  p.grassMap = &g;
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Target(0x202, kSpirit, 20.f, 0.f)};
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);  // grass off
  Params noMap;
  noMap.grass = true;
  CHECK_EQ(Run(m, es, 1, noMap), kBitSpirit | kBitMortal);  // D2g-live's rule: the target isn't in grass
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);                   // behind patch 0x11
  es[0].x = 10.f;
  es[0].grassId = 0x48000011u;  // the shaper steps into the patch: it sees out of it
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].x = 0.f;
  es[0].grassId = 0;
  es[0].ignoreGrass = true;  // Vision_Object_Grass_Transparent
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].ignoreGrass = false;
  p.grass = false;  // the map alone does nothing
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  p.grass = true;
  // in patch 0x11, revealed to Mortal (concealment cleared), seen from outside; patch 0x22 between still blocks
  es[1].x = 10.f;
  es[1].grassId = 0x48000011u;
  es[1].grassConceal = kDefaultGrassConceal & ~kBitMortal;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
  es[0].x = 34.f;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  // the point query: a point behind a patch, and one in the source's own patch
  es[0].x = 0.f;
  m.Update(es, p);
  CHECK_EQ(m.TeamsSeeingPoint(20.f, 0.f, kSpirit, 0.f), kBitSpirit);
  CHECK_EQ(m.TeamsSeeingPoint(-5.f, 0.f, kSpirit, 0.f), kBitSpirit | kBitMortal);
  CHECK_EQ(m.TeamsSeeingPoint(10.f, 0.f, kSpirit, 0.f), kBitSpirit | kBitMortal);  // in the patch: the point's own
}

// Stealth [assumed for the team's fog]: seen only by a source with StealthDetection.
GMS_TEST(VisionStealth) {
  Params p;
  p.stealth = true;
  VisionModel m;
  Entity sneaky = Target(0x202, kSpirit, 10.f, 0.f);
  sneaky.stealthed = true;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), sneaky};
  CHECK_EQ(Run(m, es, 1), kBitSpirit | kBitMortal);
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit);
  es[0].detectsStealth = true;
  CHECK_EQ(Run(m, es, 1, p), kBitSpirit | kBitMortal);
}

// Kind 3: a positional event is seen by its own team and by the teams with a source that reaches it.
GMS_TEST(VisionPointQuery) {
  VisionModel m;
  std::vector<Entity> es = {Shaper(0x101, kMortal, 0.f, 0.f), Minion(0x202, kSpirit, 100.f, 0.f)};
  m.Update(es);
  CHECK_EQ(m.TeamsSeeingPoint(50.f, 0.f, kNpc, 0.f), kBitNpc);
  CHECK_EQ(m.TeamsSeeingPoint(30.f, 0.f, kNpc, 0.f), kBitNpc | kBitMortal);
  CHECK_EQ(m.TeamsSeeingPoint(50.f, 0.f, kSpirit, 0.f), kBitSpirit);
  CHECK_EQ(m.TeamsSeeingPoint(50.f, 0.f, kSpirit, 15.f), kBitSpirit | kBitMortal);  // the event's radius as area
  CHECK_EQ(m.TeamsSeeingPoint(72.f, 0.f, kMortal, 0.f), kBitMortal | kBitSpirit);
  CHECK_EQ(m.TeamsSeeingPoint(0.f, 0.f, 9, 0.f), kBitMortal);  // no team
}

// ---- the per-player transitions --------------------------------------------------------------------------------------

GMS_TEST(FogPlayersForTeams) {
  const std::vector<FogPlayer> players = {{0, kMortal}, {1, kSpirit}, {2, kMortal}, {5, 9}};
  CHECK_EQ(PlayersForTeams(kBitMortal, players), 0x0025);  // 0, 2 and the teamless 5
  CHECK_EQ(PlayersForTeams(kBitSpirit | kBitMortal, players), 0x0027);
  CHECK_EQ(PlayersForTeams(0, players), 0x0020);
  CHECK_EQ(PlayersForTeams(kAllTeams, {{16, kMortal}}), 0);  // no bit beyond 15
}

// B15b: a spectator (ObserverFogPlayer, gameplay index 10-14, whatever its player's mTeam) sees everything: a
// Mortal player beside it gets 93 for a Spirit minion out of its vision, the spectator nothing, and its visible bit
// stays; an object hidden from both teams (teamBits 0) is never fogged for it either (the client's spectator view hides
// it by mVisibleToTeam: a 93 would assert there), it gets every kind-3 message, and a maintained object sends it no A3.
GMS_TEST(FogObserverSeesEverything) {
  FogPlanner fp;
  const FogPlayer obs = ObserverFogPlayer(10);
  CHECK(obs.index == 10 && obs.team == kObserverTeam && TeamBit(obs.team) == 0);
  const std::vector<FogPlayer> players = {{0, kMortal}, obs};
  std::vector<FogObject> objs(2);
  objs[0].id = 0xA6710150;
  objs[0].known = objs[0].visible = 0x0401;  // player 0 and the spectator at 10
  objs[0].teamBits = kBitSpirit;
  objs[1].id = 0xA6710151;
  objs[1].known = objs[1].visible = 0x0401;
  objs[1].teamBits = 0;  // stealthed from everyone
  objs[1].maintainPos = true;
  std::vector<uint16_t> after;
  auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kEnter), 2);
  for (const FogAction& x : a) CHECK_EQ(x.player, 0);
  CHECK_EQ(after[0], 0x0400);
  CHECK_EQ(after[1], 0x0400);
  objs[0].visible = after[0];
  objs[1].visible = after[1];
  objs[1].x = 10.f;  // the maintained object moves: an A3 for player 0 only
  a = fp.Plan(objs, players, &after);
  for (const FogAction& x : a) CHECK_EQ(x.player, 0);
  CHECK_EQ(after[1], 0x0400);
  CHECK_EQ(PlayersForTeams(0, players), 0x0400);           // a kind-3 message nobody's team sees: the spectator
  CHECK_EQ(PlayersForTeams(kBitMortal, players), 0x0401);  // and every other
}

// Enter: 93 and the bit cleared; exit: 94/full state/95 and the bit set; nothing while the state matches.
GMS_TEST(FogEnterExit) {
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}, {1, kSpirit}};
  std::vector<FogObject> objs(1);
  objs[0].id = 0xA6710150;
  objs[0].known = 0x3;
  objs[0].visible = 0x3;
  objs[0].teamBits = kBitSpirit;  // a Spirit minion out of Mortal vision
  std::vector<uint16_t> after;
  auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(a.size(), 1u);
  CHECK(a[0].kind == FogActionKind::kEnter);
  CHECK_EQ(a[0].player, 0);
  CHECK_EQ(a[0].object, 0xA6710150u);
  CHECK_EQ(a[0].objectIndex, 0u);
  CHECK_EQ(after[0], 0x2);
  objs[0].visible = after[0];
  a = fp.Plan(objs, players, &after);  // still fogged: no second 93 (the reader asserts)
  CHECK(a.empty());
  CHECK_EQ(after[0], 0x2);
  objs[0].teamBits = kBitSpirit | kBitMortal;  // the shaper walked up
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(a.size(), 1u);
  CHECK(a[0].kind == FogActionKind::kExit);
  CHECK_EQ(a[0].player, 0);
  CHECK_EQ(after[0], 0x3);
  objs[0].visible = after[0];
  CHECK(fp.Plan(objs, players, &after).empty());
}

// A player that doesn't know the object gets nothing (the create path is the host's); bits of other players kept.
GMS_TEST(FogOnlyKnownPlayers) {
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}, {1, kMortal}};
  std::vector<FogObject> objs(1);
  objs[0].id = 0x1;
  objs[0].known = 0x1 | 0x8;     // player 3 isn't in the list (no world yet)
  objs[0].visible = 0x1 | 0x8;
  objs[0].teamBits = kBitSpirit;
  std::vector<uint16_t> after;
  const auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(a.size(), 1u);
  CHECK_EQ(a[0].player, 0);
  CHECK_EQ(after[0], 0x8);
}

// A create sets +0xE and +0x10 for everyone; the same tick's plan fogs it for the players that don't see it.
GMS_TEST(FogCreateThenEnter) {
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}, {1, kSpirit}, {2, kMortal}};
  std::vector<FogObject> objs(1);
  objs[0].id = 0x77;
  objs[0].known = PlayersForTeams(kAllTeams, players);
  objs[0].visible = objs[0].known;
  objs[0].teamBits = kBitSpirit;
  std::vector<uint16_t> after;
  const auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kEnter), 2);
  CHECK_EQ(a[0].player, 0);
  CHECK_EQ(a[1].player, 2);
  CHECK_EQ(after[0], 0x2);
}

// In one tick: one object enters fog, another leaves it, a third is gone (deleted: forgotten, no action) and a
// fourth is new (created this tick).
GMS_TEST(FogSameTickMixed) {
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}};
  std::vector<FogObject> objs(3);
  objs[0].id = 0x10;  // seen
  objs[0].known = objs[0].visible = 0x1;
  objs[0].teamBits = kBitSpirit | kBitMortal;
  objs[1].id = 0x11;  // fogged
  objs[1].known = 0x1;
  objs[1].visible = 0;
  objs[1].teamBits = kBitSpirit;
  objs[2].id = 0x12;  // fogged, maintained (has A3 state)
  objs[2].known = 0x1;
  objs[2].visible = 0;
  objs[2].teamBits = kBitSpirit;
  objs[2].maintainPos = true;
  std::vector<uint16_t> after;
  auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kDisabledPos), 1);  // first sight of a maintained fogged object: its position
  // next tick: 0x10 leaves vision, 0x11 enters it, 0x12 was deleted, 0x13 is new and unseen
  objs[0].teamBits = kBitSpirit;
  objs[1].teamBits = kBitSpirit | kBitMortal;
  objs[2].id = 0x13;
  objs[2].known = objs[2].visible = 0x1;
  objs[2].maintainPos = false;
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(a.size(), 3u);
  CHECK(a[0].kind == FogActionKind::kEnter && a[0].object == 0x10);
  CHECK(a[1].kind == FogActionKind::kExit && a[1].object == 0x11);
  CHECK(a[2].kind == FogActionKind::kEnter && a[2].object == 0x13);
  CHECK_EQ(after[0], 0);
  CHECK_EQ(after[1], 0x1);
  CHECK_EQ(after[2], 0);
  // 0x12 comes back under the same id (it can't on the server, but the planner must not remember it)
  objs.push_back(FogObject());
  objs[3].id = 0x12;
  objs[3].known = 0x1;
  objs[3].teamBits = kBitSpirit;
  objs[3].maintainPos = true;
  objs[3].x = 0.1f;  // hardly moved: still an A3, because the state was dropped
  for (size_t i = 0; i < 3; ++i) objs[i].visible = after[i];
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kDisabledPos), 1);
}

// MaintainPosThroughFow objects: A3 to the players that have them fogged, every 0.75 units; not to a player that
// enters fog in this tick (it got the tick's 9C) nor to one that sees it.
GMS_TEST(FogMaintainedPosition) {
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}, {1, kSpirit}, {2, kMortal}};
  std::vector<FogObject> objs(1);
  FogObject& o = objs[0];
  o.id = 0xA61C0042;  // e.g. SkyCaptain's anchor
  o.known = 0x7;
  o.visible = 0x7;
  o.teamBits = kBitSpirit | kBitMortal;
  o.maintainPos = true;
  std::vector<uint16_t> after;
  CHECK(fp.Plan(objs, players, &after).empty());
  o.teamBits = kBitSpirit;  // Mortal loses it
  auto a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kEnter), 2);
  CHECK_EQ(Count(a, FogActionKind::kDisabledPos), 0);
  o.visible = after[0];
  o.x = 0.5f;
  CHECK(fp.Plan(objs, players, &after).empty());  // 0.5 units: not yet
  o.x = 0.8f;
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(a.size(), 2u);
  CHECK(a[0].kind == FogActionKind::kDisabledPos && a[0].player == 0);
  CHECK(a[1].kind == FogActionKind::kDisabledPos && a[1].player == 2);
  o.x = 1.2f;  // 0.4 from the last A3
  CHECK(fp.Plan(objs, players, &after).empty());
  // no longer maintained: nothing
  o.maintainPos = false;
  o.x = 10.f;
  CHECK(fp.Plan(objs, players, &after).empty());
  // maintained again while fogged and with no A3 state: one at once
  o.maintainPos = true;
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kDisabledPos), 2);
  // Mortal sees it again: exits, and no A3 in that tick
  o.teamBits = kAllTeams;
  o.x = 20.f;
  a = fp.Plan(objs, players, &after);
  CHECK_EQ(Count(a, FogActionKind::kExit), 2);
  CHECK_EQ(Count(a, FogActionKind::kDisabledPos), 0);
}

// The model and the planner together: T11 offline (an enemy beyond 35.3 of every Mortal source is fogged and shows
// again when the shaper walks up), then the minion dies in view and is deleted.
GMS_TEST(FogModelAndPlannerT11) {
  VisionModel m;
  FogPlanner fp;
  const std::vector<FogPlayer> players = {{0, kMortal}};
  std::vector<Entity> es = {Shaper(0x350171A6, kMortal, -234.93f, -229.92f),
                            Minion(0xA6510200, kSpirit, -180.f, -229.92f)};
  std::vector<FogObject> objs(2);
  objs[0].id = es[0].id;
  objs[1].id = es[1].id;
  for (FogObject& o : objs) o.known = o.visible = 0x1;  // the world (created with +0x10 for everyone)
  std::vector<uint16_t> after;
  auto tick = [&]() {
    m.Update(es);
    for (size_t i = 0; i < objs.size(); ++i) objs[i].teamBits = m.Bits(i);
    auto a = fp.Plan(objs, players, &after);
    for (size_t i = 0; i < objs.size(); ++i) objs[i].visible = after[i];
    return a;
  };
  auto a = tick();  // 54.9 units apart: fogged
  CHECK_EQ(a.size(), 1u);
  CHECK(a[0].kind == FogActionKind::kEnter && a[0].object == 0xA6510200u);
  es[0].x = -214.f;  // 34 units
  a = tick();
  CHECK_EQ(a.size(), 1u);
  CHECK(a[0].kind == FogActionKind::kExit);
  es[0].x = -217.f;  // 37: in the hysteresis
  CHECK(tick().empty());
  es[1].alive = false;  // dies in view
  es[0].x = -260.f;     // the shaper runs off
  CHECK(tick().empty());
  es.pop_back();        // deleted (91 goes to +0xE)
  objs.pop_back();
  CHECK(tick().empty());
}

// The messages, against the readers' layouts.
GMS_TEST(FogMessages) {
  CHECK_HEX(BuildObjectEnterFoW(0xA6710135), "93350171a6");
  const float pos[3] = {-234.93f, -229.92f, 0.f};
  const float rot[3] = {0.f, 0.f, 1.5f};
  const Bytes p = BuildObjectPrepareForExitFoW(0xA6710135, pos, rot);
  CHECK_EQ(p.size(), 1 + kPrepareExitFowBodySize);
  CHECK_EQ(p[0], 0x94);
  CHECK_EQ(gms::GetF32LE(p.data() + 5), -234.93f);
  CHECK_EQ(gms::GetF32LE(p.data() + 13), 0.f);
  CHECK_EQ(gms::GetF32LE(p.data() + 25), 1.5f);
  Bytes rec(41, 0xAB);
  const Bytes x = BuildObjectExitFoW(0xA6710135, rec);
  CHECK_EQ(x.size(), 5u + 41u);
  CHECK_EQ(x[0], 0x95);
  CHECK_HEX(BuildObjectExitFoW(0xA6710135, Bytes()), "95350171a6");
  // the shaper's start (D2g-static): block 0x4543, xy 0x23DB
  const Bytes d = BuildDisabledObjectUpdate(0xA6710135, -234.93f, -229.92f);
  CHECK_EQ(d.size(), 1 + kDisabledObjectUpdateBodySize);
  CHECK_HEX(d, "a3350171a64345db23");
}

GMS_TEST(FogQuantizeRoundTrip) {
  uint16_t block = 0, xy = 0;
  QuantizeXY(-234.93f, -229.92f, &block, &xy);
  CHECK_EQ(block, 0x4543);
  CHECK_EQ(xy, 0x23DB);
  float x = 0, y = 0;
  DequantizeXY(block, xy, &x, &y);
  CHECK(std::fabs(x + 234.93f) < 0.016f);
  CHECK(std::fabs(y + 229.92f) < 0.016f);
  for (float c : {-499.9f, -100.25f, 0.f, 33.3f, 499.9f}) {
    QuantizeXY(c, -c, &block, &xy);
    DequantizeXY(block, xy, &x, &y);
    CHECK(std::fabs(x - c) < 0.016f);
    CHECK(std::fabs(y + c) < 0.016f);
  }
  QuantizeXY(-600.f, 600.f, &block, &xy);  // clamped like the client's assert range
  CHECK_EQ(block, 0xFF00);
  CHECK_EQ(xy, 0xFF00);
}

// 94's angles: the inverse of the client's Math_EulerToQuaternion.
GMS_TEST(FogEulerRoundTrip) {
  const float cases[][3] = {{0.f, 0.f, 0.f}, {0.f, 0.f, 1.5f}, {0.f, 0.f, -2.9f}, {0.3f, -0.4f, 2.f}, {-1.f, 1.2f, 0.1f}};
  for (const auto& e : cases) {
    float q[4], back[3], q2[4];
    EulerToQuaternion(e, q);
    QuaternionToEuler(q, back);
    EulerToQuaternion(back, q2);
    const float dot = q[0] * q2[0] + q[1] * q2[1] + q[2] * q2[2] + q[3] * q2[3];
    CHECK(std::fabs(std::fabs(dot) - 1.f) < 1e-5f);  // the same rotation
    for (int k = 0; k < 3; ++k) CHECK(std::fabs(back[k] - e[k]) < 1e-4f);
  }
  // a yaw-only object (the usual case): q = (0, 0, sin(a/2), cos(a/2))
  const float yaw[4] = {0.f, 0.f, std::sin(0.75f), std::cos(0.75f)};
  float e[3];
  QuaternionToEuler(yaw, e);
  CHECK(std::fabs(e[0]) < 1e-6f);
  CHECK(std::fabs(e[1]) < 1e-6f);
  CHECK(std::fabs(e[2] - 1.5f) < 1e-5f);
}

// D2g-pursuit: the per-team last-seen pursuit position (gms/core/vision/pursuit.h), applied as the host does it after
// each tick's vision answer: the teams that see an object get its position, the others keep theirs, a new record gets
// its first position for every team.
namespace {
struct FakeRecord {
  float lastSeen[kNumTeams][2] = {};  // record +0x2C + team x 8
};
void ApplyLastSeen(LastSeenBook& book, const std::vector<Entity>& es, const VisionModel& m,
                   std::vector<FakeRecord*>& recs) {
  book.Begin();
  for (size_t i = 0; i < es.size(); ++i) {
    const uint8_t teams = book.Teams(es[i].id, reinterpret_cast<std::uintptr_t>(recs[i]), m.Bits(i));
    for (int t = 0; t < kNumTeams; ++t)
      if (teams & TeamBit(t)) {
        recs[i]->lastSeen[t][0] = es[i].x;
        recs[i]->lastSeen[t][1] = es[i].y;
      }
  }
  book.End();
}
}  // namespace

GMS_TEST(PursuitLastSeenStaysWhereTheTeamLostSight) {
  VisionModel m;
  LastSeenBook book;
  FakeRecord shaperRec, creepRec;
  std::vector<FakeRecord*> recs = {&shaperRec, &creepRec};
  // a Mortal shaper at the origin of this test, a Spirit creep 20 units east (seen), then walking east out of reach
  std::vector<Entity> es = {Shaper(1, kMortal, -100.f, -100.f), Target(2, kSpirit, -80.f, -100.f)};
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitMortal | kBitSpirit);
  ApplyLastSeen(book, es, m, recs);
  CHECK_EQ(book.Fresh(), 2);  // first sight: every team gets the first position
  CHECK_EQ(creepRec.lastSeen[kNpc][0], -80.f);
  CHECK_EQ(creepRec.lastSeen[kMortal][0], -80.f);
  es[1].x = -70.f;  // 30 units: still seen (35.3)
  m.Update(es);
  ApplyLastSeen(book, es, m, recs);
  CHECK_EQ(book.Fresh(), 0);
  CHECK_EQ(creepRec.lastSeen[kMortal][0], -70.f);
  CHECK_EQ(creepRec.lastSeen[kNpc][0], -80.f);  // NPC doesn't see it: keeps its value
  es[1].x = -50.f;  // 50 units: lost
  m.Update(es);
  CHECK_EQ(m.Bits(1), kBitSpirit);
  ApplyLastSeen(book, es, m, recs);
  CHECK_EQ(creepRec.lastSeen[kMortal][0], -70.f);  // where Mortal saw it last, not the origin
  CHECK_EQ(creepRec.lastSeen[kMortal][1], -100.f);
  CHECK_EQ(creepRec.lastSeen[kSpirit][0], -50.f);  // its own team always
  es[1].x = -40.f;
  m.Update(es);
  ApplyLastSeen(book, es, m, recs);
  CHECK_EQ(creepRec.lastSeen[kMortal][0], -70.f);
  es[1].x = -75.f;  // back in sight: followed again
  m.Update(es);
  ApplyLastSeen(book, es, m, recs);
  CHECK_EQ(creepRec.lastSeen[kMortal][0], -75.f);
  CHECK_EQ(shaperRec.lastSeen[kMortal][0], -100.f);
}

GMS_TEST(PursuitLastSeenNewAndReusedRecords) {
  LastSeenBook book;
  // never fogged (kAllTeams), an unseen enemy, no record / no id
  book.Begin();
  CHECK_EQ(book.Teams(10, 0x1000, kAllTeams), kAllTeams);
  CHECK_EQ(book.Teams(11, 0x2000, kBitSpirit), kAllTeams);  // first sight: its spawn for every team, never (0, 0)
  CHECK_EQ(book.Teams(12, 0, kAllTeams), 0);
  CHECK_EQ(book.Teams(0, 0x3000, kAllTeams), 0);
  book.End();
  CHECK_EQ(book.Known(), 2u);
  book.Begin();
  CHECK_EQ(book.Teams(10, 0x1000, kAllTeams), kAllTeams);
  CHECK_EQ(book.Teams(11, 0x2000, kBitSpirit), kBitSpirit);  // known: only the teams that see it
  CHECK_EQ(book.Teams(11, 0x2000, 0x08 | kBitSpirit), kBitSpirit);  // bits beyond the teams are ignored
  CHECK_EQ(book.Fresh(), 0);
  book.End();
  // object 11 gets another record (SetGrantsVision), object 10 is gone and its pooled record goes to object 13
  book.Begin();
  CHECK_EQ(book.Teams(11, 0x2400, kBitSpirit), kAllTeams);
  CHECK_EQ(book.Teams(13, 0x1000, kBitNpc), kAllTeams);
  CHECK_EQ(book.Fresh(), 2);
  book.End();
  CHECK_EQ(book.Known(), 2u);
  // object 10 comes back (a new object with a reused id): new again
  book.Begin();
  CHECK_EQ(book.Teams(10, 0x1000, kBitNpc), kAllTeams);
  book.End();
}
