// gms/core/vision (D2g-fog-core): the fog-of-war vision model, engine-free (docs/GMS_PROTOCOL.md *Fog of war on the
// host: the engine-free core (D2g-fog-core)*). No game addresses: the host (D2g-live step 2) fills one Entity per
// object from the server simulator (the object and its line-of-sight record, object +0x858) and writes the answer
// into mVisibleToTeam (+0x888); gms/core/vision/fog.h turns the answer into the per-player messages.
//
// What the client build has, and what this replaces (read-only Ghidra, D2g-fog-core):
//   - LineOfSight_AddObject (0x00917F40) gives every fowTracked object a record (0x64 bytes) in the line-of-sight
//     manager (sim +0x56C): +0 object, +4 grid handle, +0xC/+0x10 position at creation, +0x14 radius, +0x18
//     hysteresis, +0x1C flags (1 fowUseOwnerVision, 2 fowIgnoreTerrainTest, 4 fowIgnoreGrassTest, 8 vision proxy),
//     +0x1D team, +0x1F grass concealment (the teams it hides from in grass: 6, 0 with fowIgnoreGrassTest; cleared per
//     team by AgentBlackboard_RevealInGrass 0x008AA160), +0x20 disabledUpdateCounter (MaintainPosThroughFow), +0x50/
//     +0x51 team masks. The radius is GameObject_GetVisionRadius (0x00880740): Noun.fowVisibleRadius/fowVisibleHyst,
//     or BattlegroundsGameplayTuning.playerFoWVisibleRadius/-Hyst (35.3/3.0) for a player-controlled object.
//   - LineOfSight_AllocTracked (0x00917450) puts a record with a radius > 0 into a 30-unit grid (per cell and team,
//     the reach radius + hysteresis): the vision sources. A noun with fowVisibleRadius 0 but fowAreaRadius > 0 gets
//     the area radius in +0x14 after that, outside the grid.
//   - The pass that compared sources and targets and wrote the team masks is stripped (also the per-frame update of
//     the records' positions and grid cells). This model is that pass, from the inputs above. The client's own fog
//     rendering (0x00580780) draws its team's sources from sporelabsObject.mVisionRadius (+0x884) with the terrain and
//     grass flags, which shows that terrain and grass are part of the game's vision.
//
// The rule per object o and team T (1 << T in the answer):
//   1. T is o's own team: visible [code: GameObject_SetVisibleToOwnTeam 0x00880610, Agent_IsVisibleTo 0x008A9B90].
//   2. o has no record, or is never fogged (noun type Tower, obstacle or titan, NPC type 10 = gold mines): visible to
//      every team [code: GameSimulator_GetPlayersThatSeeObject 0x008F0A30 returns 0x7FFF for them]. Also o's noun
//      without fowControlsObjectVisibility [assumed: Agent_IsVisibleTo treats such a target as visible, and the
//      client's ObjectEnterFoW only disables it, without hiding it].
//   3. o uses its owner's vision (record flag 1, fowUseOwnerVision; orbits, books, crystals) and the owner is an
//      input: the owner's answer [assumed from the name].
//   4. o is dead and had an answer last tick: last tick's answer, frozen until it is deleted [assumed], so whoever saw
//      it die sees the death and nobody gets a fog exit for a corpse.
//   5. Otherwise: some live source s of team T with distance(s, o) <= s.radius + o.areaRadius, plus s.hysteresis if T
//      saw o last tick [code: the grid holds radius + hysteresis; tuning 35.3 + 3.0; the area radius semantics are
//      assumed], and (switches, on by default since D2g-rest: the client's own fog renderer FogOfWarRenderer.cpp draws
//      every vision circle of its team with the level's terrain and grass outlines (Level.outlinePath) as occluders,
//      and the data has vision objects that exist only to ignore them; docs/GMS_PROTOCOL.md *Terrain, grass and
//      stealth against the original (D2g-rest)*):
//        - terrain: no blocked cell of the level's line-of-sight map between s and o, unless s ignores terrain
//          (fowIgnoreTerrainTest) [data: SM_Map_v5's map is 512 x 512 bytes, 128 = blocks, 0 = open; bits 0-6 are
//          nFogOfWar.ModifyTerrain's handles, which open a wall (ABILVFX, LosMap below; assumed); the ray rule is
//          assumed: it agrees with the client's outline shadows
//          for 97.9 % of pairs on open ground within 38.3 units, re/scripts/fow_outlines.py rays];
//        - grass: o in a grass patch (grass map alpha != 0; the patch is the cell value's low 24 bits) and concealed
//          from T (record +0x1F) is seen only from the same patch or by a source that ignores grass, and with the
//          grass map (Params::grassMap) every other patch on the line between s and o blocks too [code: the
//          renderer's grass outlines cast shadows like the terrain's, except for a circle whose grass id is the
//          outline's; the cell ray is assumed];
//        - stealth: a stealthed o (blackboard +0x1E74) only by a source with StealthDetection (attribute 47) > 0
//          [code for agents: Agent_IsVisibleTo; for the team's fog: the client draws a visible stealthed object at
//          half opacity whatever its team, a ward turns stealthy 3 s after it is placed, and the spectator view hides
//          only by mVisibleToTeam].
#pragma once

#include <cstddef>
#include <cstdint>
#include <unordered_map>
#include <vector>

namespace gms::vision {

constexpr int kNumTeams = 3;          // 0 NPC (neutral), 1 Mortal, 2 Spirit ("teamId < kNumTeams", 0x008805B0)
constexpr uint8_t kAllTeams = 0x07;   // what the host writes for an object that is never fogged
inline uint8_t TeamBit(int team) { return team >= 0 && team < kNumTeams ? static_cast<uint8_t>(1u << team) : 0; }

// Noun types (enum 0x01438A48) and the NPC type that are never fogged (0x008F0A30, 0x005490C0).
constexpr int32_t kNounTypeTower = 213110983;
constexpr int32_t kNounTypeObstacle = 44938362;
constexpr int32_t kNounTypeTitan = -639573535;
constexpr int kNpcTypeGoldMine = 10;
inline bool NeverFoggedType(int32_t nounType, int npcType) {
  return nounType == kNounTypeTower || nounType == kNounTypeObstacle || nounType == kNounTypeTitan ||
         npcType == kNpcTypeGoldMine;
}

// BattlegroundsGameplayTuning "Tuning" (AssetData): a player-controlled object's vision (GameObject_GetVisionRadius).
constexpr float kPlayerVisionRadius = 35.3f;
constexpr float kPlayerVisionHysteresis = 3.0f;
// Record +0x1F as LineOfSight_AllocTracked leaves it: concealed in grass from both player teams.
constexpr uint8_t kDefaultGrassConceal = 0x06;

// One object (or one anonymous vision source) per tick.
struct Entity {
  uint32_t id = 0;         // server object id; 0 = an anonymous source (a vision proxy record): gives vision only
  uint8_t team = 0;        // mTeam (+0x4D); a proxy: record +0x1D. >= kNumTeams: no team (sees nothing)
  float x = 0.f, y = 0.f;  // position (+0x10/+0x14; a proxy: record +0xC/+0x10, fixed at its creation)
  bool alive = true;       // false while dead (combatant HP <= 0); objects without HP stay true

  // From the record (object +0x858); tracked = false: no record, never fogged and no vision.
  bool tracked = false;
  bool neverFogged = false;         // NeverFoggedType(noun type, NPC type)
  bool controlsVisibility = true;   // Noun.fowControlsObjectVisibility (+0x2CD)
  float visionRadius = 0.f;         // record +0x14 while the record is in the grid (+4 != 0): a vision source
  float hysteresis = 0.f;           // record +0x18
  float areaRadius = 0.f;           // record +0x14 while not in the grid (Noun.fowAreaRadius)
  bool useOwnerVision = false;      // record +0x1C & 1
  uint32_t owner = 0;               // mOwnerID (+0x48)
  bool ignoreTerrain = false;       // record +0x1C & 2: as a source, sees over terrain
  bool ignoreGrass = false;         // record +0x1C & 4: as a source, sees into grass
  uint8_t grassConceal = kDefaultGrassConceal;  // record +0x1F: the teams it hides from while in grass
  uint32_t grassId = 0;             // the grass patch it stands in (0 = none)
  bool stealthed = false;           // cAgentBlackboard +0x1E74 (nGameObject.SetStealthType)
  bool detectsStealth = false;      // as a source: attribute 47 StealthDetection > 0
};

// The level's line-of-sight map as the manager holds it (LineOfSight_InitMaps 0x00917C90): +4 the bytes (row-major,
// file order of images/8bit/<level>_lineOfSight.tga), +0xC width, +0x10 height, +0x1C/+0x20 the pixel of the world
// origin (width/2, height/2), +0x24/+0x28 pixels per unit (width / levelSize.x, height / levelSize.y). A world point's
// cell is ROUND(origin + coordinate x scale), clamped to the map (LineOfSight_IsInGrass 0x009185E0 and
// LineOfSight_UpdateGrassId 0x00918500 address the grass map the same way). 128 blocks; the handle bits 0-6 below.
//
// ABILVFX: the low seven bits are nFogOfWar.ModifyTerrain's handles. LineOfSight_AllocTerrainModHandle (0x00917720)
// hands out one free bit of 0x7F (the used ones in manager +0x1A03C), LineOfSight_ModifyTerrain (0x009186E0) ORs it into
// every cell of a convex polygon (LineOfSightMap_DrawLine 0x00916C30 and _FloodFill: cell |= handle), and
// LineOfSight_ReleaseTerrainModHandle (0x00918430) clears it again. The only user is Kensu's R (ability_Savage_R_StunArrow),
// whose trail also lays a dynamic navmesh along the arrow's path (nLocomotion.CreateDynamicNavmeshInBox) so his allies can
// walk it through walls: the trail opens the walls it crosses for vision too [assumed: the reader, the stripped
// visibility pass, is gone], and it never blocks open ground. D2g-fog-core counted any non-zero byte as a wall, so on
// the host the trail itself was a wall: a shaper inside or behind it in a lane went into the fog for the other team
// (the players' "shaper models flash or disappear when you enter the trail").
constexpr uint8_t kLosBlocked = 0x80;
constexpr uint8_t kLosModifierBits = 0x7F;

struct LosMap {
  const uint8_t* cells = nullptr;
  int width = 0, height = 0;
  float originX = 0.f, originY = 0.f;
  float scaleX = 0.f, scaleY = 0.f;
  // ABILVFX: a cell blocks only with bit 7 and no modifier bit (a terrain modifier opens a wall); false = the old rule
  // (any non-zero byte blocks; [host] fog_terrain_mods=0)
  bool terrainModsOpen = true;

  // A map of width x height over a level of levelSizeX x levelSizeY centred on the origin, as InitMaps sets it up.
  static LosMap ForLevel(const uint8_t* cells, int width, int height, float levelSizeX, float levelSizeY);
  bool Valid() const { return cells && width > 0 && height > 0; }
  void CellOf(float x, float y, int* cx, int* cy) const;
  uint8_t At(int cx, int cy) const;
  // Whether a cell's byte blocks the line of sight (the rule above).
  bool BlocksCell(uint8_t cell) const;
  // Whether a blocking cell lies on the line between the two points' cells (Bresenham; both end cells left out, so
  // a unit standing at a wall's edge isn't hidden by its own cell) [assumed].
  bool Blocked(float x0, float y0, float x1, float y1) const;
};

// The level's grass map as the manager holds it (manager +8: 32-bit cells, BGRA, the same width, height and cell rule as
// the line-of-sight map; LineOfSight_InitMaps 0x00917C90). A cell with alpha != 0 is grass; its whole value is the
// patch's id (the low 24 bits name the patch).
struct GrassMap {
  const uint32_t* cells = nullptr;
  LosMap geometry;  // width, height, origin, scale (its cells unused)

  bool Valid() const { return cells && geometry.width > 0 && geometry.height > 0; }
  // The cell's value at a world point (0 = no grass) and its packed cell (y << 16 | x); Raw without the alpha test.
  uint32_t At(float x, float y, uint32_t* packed = nullptr) const;
  uint32_t Raw(float x, float y, uint32_t* packed = nullptr) const;
  // Whether a grass cell of a patch other than the two given (by the low 24 bits; 0 = none) lies on the line between
  // the two points' cells (Bresenham, the end cell included, the start cell left out): grass hides what lies behind
  // it from a source outside it, as the client's grass outlines shadow it [the cell ray is assumed].
  bool BlockedBetween(float x0, float y0, float x1, float y1, uint32_t sourcePatch, uint32_t targetPatch) const;
};

// LineOfSight_UpdateGrassId (0x00918500, thiscall manager, (position, &id, &packed xy)) on the host's own copy: what the
// stripped visibility pass kept in sporelabsObject.mServerGrassId (+0x87C) and mServerGrassMapPackedXY (+0x880), which
// the client's fog renderer (0x00580780) uses for its team's vision circles. With id 0 it takes the cell's value if it
// is grass; otherwise it changes only when the patch (the low 24 bits) changes, to the new cell's value (0 out of the
// grass). `packed` is written only on a change. Returns whether the id changed.
bool UpdateGrassId(const GrassMap& map, float x, float y, uint32_t* id, uint32_t* packed);

struct Params {
  const LosMap* terrain = nullptr;  // nullptr: no terrain test; must stay valid until the next Update
  bool grass = false;
  // With grass: the level's grass map, so other patches on the line block too (nullptr: only the target's own patch
  // hides it); must stay valid until the next Update
  const GrassMap* grassMap = nullptr;
  bool stealth = false;
};

class VisionModel {
 public:
  // One tick: every entity's team bits (rule above). Entities with id 0 only give vision. An id that isn't in this
  // call is forgotten (its hysteresis and frozen state).
  void Update(const std::vector<Entity>& entities, const Params& params = Params());

  // The answer for the i-th entity of the last Update (mVisibleToTeam), and by object id (0 if not in it).
  uint8_t Bits(size_t i) const { return i < bits_.size() ? bits_[i] : 0; }
  uint8_t BitsOf(uint32_t id) const;
  size_t Size() const { return bits_.size(); }
  int Sources() const { return static_cast<int>(sources_.size()); }

  // The teams that see a point (a positional ServerEvent's line-of-sight record: kind 3 of the outgoing vector; the
  // event's team, its radius as an area [assumed]), from the last Update's sources; terrain as in Update, the grass
  // patches between (other than the source's and the point's own), no hysteresis and stealth.
  uint8_t TeamsSeeingPoint(float x, float y, uint8_t team, float radius) const;

 private:
  struct Source {
    float x, y, radius, hysteresis;
    uint8_t team;
    bool ignoreTerrain, ignoreGrass, detectsStealth;
    uint32_t grassId, id;
  };
  bool SeenBy(int team, const Entity& target, bool wasVisible) const;
  uint8_t Resolve(size_t i, std::vector<uint8_t>* state);

  const std::vector<Entity>* entities_ = nullptr;  // during Update only
  Params params_;
  std::vector<Source> sources_;
  std::vector<uint8_t> bits_;
  std::unordered_map<uint32_t, size_t> index_;     // id -> entity index (this tick)
  std::unordered_map<uint32_t, uint8_t> last_;     // id -> last tick's bits
};

}  // namespace gms::vision
