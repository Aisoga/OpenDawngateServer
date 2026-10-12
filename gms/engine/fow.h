// Fog of war on the match host, the engine side (roadmap D2g-live step 2; ADR-2 decision 5, seam 4; docs/GMS_PROTOCOL.md
// *Fog of war on the host: the engine-free core (D2g-fog-core)* and *Fog of war on the host, live (D2g-live step 2)*).
// gms/core/vision has the rule (VisionModel) and the per-player transitions (FogPlanner); this file reads their inputs
// from the server simulator and writes the answers back:
//   - per scanned object (repl::Scan) one vision::Entity from the object and its line-of-sight record (object +0x858:
//     +4 grid handle, +0x14 radius or area, +0x18 hysteresis, +0x1C flags 1 owner vision / 2 ignore terrain / 4 ignore
//     grass / 8 proxy, +0x1D team, +0x1F grass concealment, +0x20 disabledUpdateCounter), the noun's type (+0) and
//     fowControlsObjectVisibility (+0x2CD), the NPC type (object +0x78 -> NonPlayerClass data +0x68), the blackboard's
//     stealth byte (+0x1E74) and attribute 47 StealthDetection; plus one anonymous source per vision-proxy record
//     (flag 8, nFogOfWar.CreateVisionProxyObject) and per record of a live object outside the scan (no network
//     component), from the record pool (manager +0x1A030 pointers, +0x1A034 in use, kept packed);
//   - the level's line-of-sight map (manager +4 bytes, +0xC width, +0x10 height, +0x1C/+0x20 origin, +0x24/+0x28 scale;
//     LineOfSight_InitMaps 0x00917C90) copied every second (nFogOfWar.ModifyTerrain writes handle bits into it), the
//     grass map (manager +8, 32-bit cells) copied once;
//   - the answers: mVisibleToTeam (+0x888, sporelabsObject member 0; Agent_IsVisibleTo reads it for every viewer with the
//     vision rule "everything"), and for the client's own fog rendering (0x00580780, [host] fog_render)
//     mVisionRadius (+0x884, member 3) = the record's radius on every vision source, mServerGrassId/-PackedXY
//     (+0x87C/+0x880, members 1/2) by LineOfSight_UpdateGrassId's rule (0x00918500), which nothing on the server writes;
//   - the network component's visible mask (+0x10) after the fog step's transitions;
//   - D2g-pursuit: the records' per-team last-seen pursuit positions (+0x2C + team x 8), which a walk-up takes as its
//     goal for a target its follower doesn't see (WriteLastSeen below).
// Every engine read runs under SEH with the server simulator bound (the caller binds it).
#pragma once

#include <cstdint>
#include <vector>

#include "core/vision/vision.h"
#include "replicate.h"

namespace fow {

struct Options {
  bool terrain = true;   // [host] fog_terrain: the line-of-sight map blocks vision (D2g-rest; ray rule assumed)
  bool grass = true;     // [host] fog_grass: a patch hides what is in and behind it from other patches (D2g-rest)
  bool stealth = true;   // [host] fog_stealth: stealthed objects only to StealthDetection sources (D2g-rest)
  bool render = true;    // [host] fog_render: write mVisionRadius and the grass ids for the client's fog renderer
  bool pursuit = true;   // [host] fog_pursuit: write the records' per-team last-seen pursuit positions (D2g-pursuit)
  bool grassEvents = true;  // [host] grass_events: OnEnterGrass/OnExitGrass at an mbInGrass change (D3c; needs grass)
};

// What the fog step needs per scanned object beside its vision::Entity.
struct ObjectInfo {
  bool tracked = false;   // has a line-of-sight record
  std::uintptr_t record = 0;  // its address (object +0x858)
  bool maintain = false;  // record +0x20 > 0 (nFogOfWar.MaintainPosThroughFow): A3 while fogged
  float pos[3] = {};
  float quat[4] = {0.f, 0.f, 0.f, 1.f};  // +0x1C, x y z w
  // D2g-rest, with grass: this call changed mServerGrassId (to grassId; 0 = out of the grass), and the reveal state
  bool grassChanged = false;
  std::uint32_t grassId = 0;
  std::uint8_t revealed = 0;  // blackboard +0x1E76: the teams the object is revealed to in grass (RevealInGrass)
  std::uint8_t revealEnded = 0;  // this call: the teams whose reveal timer ran out (concealment restored)
  // D3c, with grass: this call set mbInGrass (+1: entered the grass) or cleared it (-1: left it), 0 = no change
  std::int8_t grassEdge = 0;
};

// D3c: OnEnterGrass (35) / OnExitGrass (36) on the object's
// ability instances (AbilityDataContainer_FireEvent 0x009064E0 on object +0x8A8, no records), as the stripped grass pass
// did when an agent's mbInGrass changed: no code in the client fires them (the 14 FireEvent call sites pass 0x1A-0x3C,
// none 0x23/0x24), and modifier_MediumPerk_BushSpeed (Adventurer) and modifier_HighHuntress_Passive (Grumbles' ward
// in the grass) answer only them. The server simulator must be bound. False when the object has no container or the
// call raised an exception.
constexpr int kEventOnEnterGrass = 35, kEventOnExitGrass = 36;
bool FireGrassEvent(void* object, bool enter);

struct ReadCounts {
  int tracked = 0;   // scanned objects with a record
  int sources = 0;   // of those, in the grid (radius > 0)
  int proxies = 0;   // vision-proxy records
  int unscanned = 0; // records of live objects without a network component (anonymous sources)
  int radiusWrites = 0, grassWrites = 0;  // render fields changed this call
  int failed = 0;    // objects whose read raised an exception (no entity: forgotten this tick)
  int inGrass = 0;   // D2g-rest: objects with an agent blackboard in grass (mbInGrass written 1)
  int revealed = 0;  // of those, revealed to some team (RevealInGrass's timer running)
};

// One Entity per object of `objs` (same order; an object whose read failed gets id 0 and no vision), then the
// anonymous sources. With options.render (or grass) also updates the render fields on the server objects first, so
// the entities' grass ids are the ones the client gets. With options.grass (D2g-rest) it also does what the stripped
// visibility pass did for grass: mbInGrass (blackboard +0x1E75, reflected: the client draws an object in grass at half
// opacity, nGameObject.IsInGrass and AgentBlackboard_RevealInGrass read it) and the reveal timers RevealInGrass starts
// (+0x1E78 + 4 x team ms, state +0x1E84 + 4 x team = 1): `elapsedMs` since the last call counts them down, and at 0 the
// team's concealment bit (record +0x1F) comes back; leaving the grass ends every reveal. False without a line-of-sight
// manager.
bool Read(void* sim, const std::vector<repl::NetObject>& objs, const Options& options, unsigned elapsedMs,
          std::vector<gms::vision::Entity>* entities, std::vector<ObjectInfo>* info, ReadCounts* counts);

// The host's copies of the level's line-of-sight and grass maps (nullptr until Read found them).
const gms::vision::LosMap* Terrain();
const gms::vision::GrassMap* Grass();

// D2g-rest: the line-of-sight record's +0x51 mask (LineOfSightRecord_IsVisibleToTeam 0x00880640) = bits[i] for
// objs[i]: the teams that would see the object if grass and stealth didn't hide it (the host's terrain-only model).
// LabsAbilities_CreateAbilityObject reveals a caster in grass only to the teams in this mask (RevealInGrass), and
// agents with mAggroVisionBlockingRule "terrain" read it. Returns how many changed.
int WriteTerrainMask(const std::vector<repl::NetObject>& objs, const std::vector<gms::vision::Entity>& entities,
                     const std::vector<std::uint8_t>& bits);

// mVisibleToTeam (+0x888) = bits[i] for objs[i] (entities with id 0 are skipped). Returns how many changed.
int WriteVisibility(const std::vector<repl::NetObject>& objs, const std::vector<gms::vision::Entity>& entities,
                    const std::vector<std::uint8_t>& bits);

// The network component's visible mask (+0x10) = mask. False without a network component.
bool SetVisibleMask(void* object, std::uint16_t mask);

// D2g-pursuit (docs/GMS_PROTOCOL.md *The last-seen pursuit position (D2g-pursuit)*; the rule in
// gms/core/vision/pursuit.h): the line-of-sight record's per-team last-seen pursuit position (+0x2C + team x 8 = x,
// +0x30 + team x 8 = y), which GameObject_GetLastSeenPursuitPosition (0x008814B0) gives Locomotion_FollowObject
// (0x0088FD50) and Locomotion_TickMovers (0x008A6CA0) as the goal for a target the follower doesn't see. For every
// tracked object of `objs`: the teams in bits[i] get its position; a record met for the first time gets it for
// every team. Returns the floats pairs written; *fresh = the records met for the first time.
int WriteLastSeen(const std::vector<repl::NetObject>& objs, const std::vector<gms::vision::Entity>& entities,
                  const std::vector<ObjectInfo>& info, const std::vector<std::uint8_t>& bits, int* fresh);

// A follow in progress (cLocomotionData, object +0x8A0: +8 the target's id, set by Locomotion_FollowObject; +0xC the
// follower saw the target at the follow's start or its last check, Locomotion_CheckFollowTarget 0x00890470; +0xB8
// the goal flags, bit 0x100 off; +0xBC the path goal; +0xC8 the target's position at the last refresh) of a scanned
// object.
struct Pursuit {
  size_t follower = 0;      // index into objs
  std::uint8_t team = 0xFF; // the follower's
  std::uint32_t target = 0;
  bool targetSeen = false;  // locomotion +0xC
  std::uint32_t flags = 0;  // locomotion +0xB8
  float goal[3] = {};       // +0xBC
  float targetPos[3] = {};  // +0xC8
  float lastSeen[2] = {};   // the target's record +0x2C + follower's team x 8 (0, 0 without a record)
  bool targetTracked = false;
};
// Every follow of the scanned objects (the server simulator bound). Returns how many.
int ReadPursuits(const std::vector<repl::NetObject>& objs, std::vector<Pursuit>* out);

}  // namespace fow
