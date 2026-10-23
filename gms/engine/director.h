// The AI director's stripped part (roadmap D1c-live; docs/MATCH_FLOW.md §2): what spawns when on SM_Map_v5, run on the
// server simulator once per tick by the match module (match.h), with the server simulator bound:
//   - the level data, read from the server simulator's own Markerset assets (Level +0x00, cLabsMarker 0xC8 bytes;
//     game.h *match flow*): the minion spawners and their lanes (the spawner's targetMarkerId is its spline's knot 0),
//     the creep camps with their creature markers, the parasite's camp, the titan nodes (Guardians, spirit wells), the
//     lane splines (SplinePathNode markers, knots sorted by their knot number);
//   - the lane table in the engine's own spline-path map (sim +0x19E60, which nothing in the binary fills): one entry
//     per spline, keyed by its head node's object id, so the minions' own bindings (nDirector.GetMinionDestination,
//     GetNextMinionDestination, nThread.WaitForNearSplinePathGoal) and the lane tether (AgentBlackboard_GetTetherCircle)
//     work as built; every wave minion gets its spline id and index 0 (locomotion +0x134/+0x138);
//   - waves at LevelGameplayTuning minionSpawnDelay1 + minionSpawnInterval1 * k s of match time (60 + 30 k on v5) from
//     every minion spawner: its noun's SpawnPointDef composition (or its override's, nDirector.OverrideMinionSpawner-
//     Asset), GameObject_CreateFromNoun (0x008E1EC0) one by one at the spawner in the composition's order (melee
//     first), one constant delay apart (D1k: the user's footage of the original: six minions in 01:00-01:03, so
//     0.5 s), each walking its lane from its first tick; with the delay 0, D1c-live's rows of three toward the lane at
//     once [assumed]; the minions push each other since D1k (crowd.h: the server space's constraint solver);
//   - the creep camps on their marker timers (first spawn spawnDelay, respawn respawnTime s after the camp was cleared
//     [assumed]), each creature at its child marker, in the camp (object +0x50 and AIDirectorImpl_AddCampMember
//     0x008AF780) so a hit creep pulls its camp (Agent_CallForHelp);
//   - the parasite at match start (its camp, director +0x18 for nDirector.GetParasiteID, followed through its stages);
//   - nDirector.OverrideMinionSpawnerAsset / ResetMinionSpawnerAsset over the client's no-op stubs;
//   - MAPSTRIDER: the lane agents where the level's striderSpawnEnabled is set (MM_Map_v3): one creature per super-
//     minion spawner (type 4) with the waves a per-team countdown picks, the tuning's strider list entry in turn
//     (gms/core/matchflow/laneagents.h [assumed rule]); [host] lane_agents, lane_agent_order, lane_agent_waves_before,
//     lane_agent_waves_between (Configure reads them).
// Every engine call runs under SEH. Settings ([host] keys, match.cpp reads them): director (1), director_waves (1),
// director_camps (1), director_parasite (1), lane_table (1), wave_delay / wave_interval (the tuning's), wave_limit (0),
// wave_spawners ("" = all; e.g. "Mortal_Top"),
// formation_spacing (2.5), director_trace_s (5), director_log_limit (400); D1k: wave_spawn_interval_ms (500; 0 = the
// whole wave at once in D1c-live's formation), wave_shape_trace_s (1; 0 = off), wave_shape_waves (2), wave_shape_for_s
// (40): the "wave shape" lines (length, width, closest pair of the first waves' minions per spawner); PARAMAP:
// camp_icons (1: the parasite's minimap icon while it is alive and born, 8D mCampVisibleToTeam; 2 every camp; 0 off;
// CAMPICONS: 3, the default, the parasite as 1 and the jungle camps by each team's knowledge, gms/core/matchflow/
// camps.h, with camp_reveal_buff_s (60) and camp_reveal_small_s (10); Configure reads camp_icons itself).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace director {

struct Options {
  bool waves = true;
  bool camps = true;
  bool parasite = true;
  bool laneTable = true;         // fill the engine's spline-path map and give wave minions their spline id
  double waveDelay = -1.0;       // < 0: LevelGameplayTuning.minionSpawnDelay1
  double waveInterval = -1.0;    // < 0: LevelGameplayTuning.minionSpawnInterval1
  int waveLimit = 0;             // stop after this many waves (0 = no limit)
  std::string spawnerFilter;     // test aid: only spawners whose name contains one of these comma-separated parts
                                 // fire waves (empty = all)
  float formationSpacing = 2.5f;  // D1c-live's formation (used only with spawnIntervalS 0)
  double traceSeconds = 5.0;     // the lane trace of the first wave's minions (0 = off)
  int logLimit = 400;
  double spawnIntervalS = 0.5;   // D1k: s between two minions of a wave (0 = all at once, D1c-live)
  double shapeTraceS = 1.0;      // D1k: a "wave shape" line every N s for the traced waves (0 = off)
  int shapeWaves = 2;            // D1k: trace the first N waves of every spawner
  double shapeForS = 40.0;       // D1k: for N s after the wave's first minion
  bool campMajorSpot = true;     // D14: a buff camp's major on its scale-2.0 marker (gms::flow::AssignCampSpots)
  // NULLPOS: the parasite spawns this far along y from its creature marker when the marker is the map's origin, so
  // nothing living stands on (0, 0), the bots' null position ([host] parasite_offset_y; 0 = on the marker)
  float parasiteOffsetY = -1.f;
  int campIcons = 3;             // PARAMAP: the camps' minimap icons (gms::flow::CampIconBits): 0 off (the director's
                                 // NPC bit), 1 the parasite's camp while the parasite is alive and born, 2 every camp
                                 // while a member lives [assumed]; CAMPICONS: 3 the jungle camps by each team's
                                 // knowledge (Configure reads [host] camp_icons, default 3, over this field)
};

void Configure(const Options& o);

// Reads the level's markers and builds the lanes, spawners, camps and titan nodes; fills the spline-path map. After
// GameSimulator_StartLevel, before the first tick (the spline nodes with deleteInGame still exist), the server
// simulator bound. False (logged) if the level data couldn't be read.
bool OnLevelStarted(void* sim);

// Registers nDirector.OverrideMinionSpawnerAsset / ResetMinionSpawnerAsset on `L` (GearsLua_RegisterNamespace).
void RegisterLua(void* L, void* sim);

// The match clock started (GameSimulator_StartGame): schedules the waves and camps.
void OnMatchStarted();

// Waves, camps, the parasite's id, the lane trace. `t` = match seconds. Once per server tick, bound.
void Tick(void* sim, double t);

// The game is over: no more spawns.
void Stop();

// PARAMAP: the camp markers whose minimap icons [host] camp_icons covers (the parasite's camp; every camp with 2);
// their spawn point objects are created for the players (replicate.h Replicated), after OnLevelStarted.
std::vector<std::uint32_t> IconCampMarkers();

// CAMPICONS: an object died (death.cpp OnDeath, the server simulator bound, before the object goes): when it is a
// jungle camp's last living creature the camp is cleared now, and the teams in its mVisibleToTeam witnessed it, and
// the killer's team (`killerTeam` 1 or 2; 0 = none or not a player team) whatever its vision (CAMPFIX).
void OnObjectDied(std::uint32_t id, int killerTeam = 0);

// CAMPICONS: the teams that see a point (x, y) now, as bits 1 << team (the host's vision model; gms_host.cpp sets it).
typedef std::uint8_t (*PointSightFn)(void* ctx, float x, float y);
void SetPointSight(PointSightFn fn, void* ctx);

// CAMPICONS: [host] camp_icons as the director uses it (0-3).
int CampIconMode();

// CAMPICONS: the camp entry's bits for a spectator (the camp's truth, gms::flow::CampTruthBits) for the camp whose
// mSpawnMarkerId is `marker`; -1 = the players' value (the parasite's camp, or camp_icons other than 3).
int SpectatorCampBits(std::uint32_t marker);

// CAMPICONS: the jungle camps (not the parasite's) with their state and each team's known respawn time.
struct CampView {
  std::uint32_t marker = 0;
  std::string name;
  float pos[3] = {};
  bool alive = false;            // spawned and not cleared
  double respawnAt = -1.0;       // the next spawn while cleared (< 0: none)
  double knownRespawnAt[2] = {-1.0, -1.0};  // team 1, team 2 (< 0: unknown)
  int spawns = 0;                // HUNTERROUTE: spawns so far (0: not up yet)
};
std::vector<CampView> CampViews();

// HUNTERROUTE: the match seconds of the director's last tick (the camps' clock).
double MatchNow();

// When the first wave is due (match seconds; < 0 = no waves).
double FirstWaveTime();
int WavesFired();

// A base titan node's Guardian (MATCH_FLOW §3.1): spawnedTitanNoun at the node's TitanSpawnPoint.
struct TitanSpawn {
  std::string node;      // the node's noun name
  void* noun = nullptr;  // the Guardian's noun asset
  std::string nounName;
  int team = 0;
  float pos[3] = {};
  float rot[3] = {};     // degrees
};
const std::vector<TitanSpawn>& Guardians();

// D9: a worker spot of a well (the well marker's children, "Follower Spawn Point"), in marker order.
struct WellSpot {
  float pos[3] = {};
  float rot[3] = {};  // degrees
};
// A spirit well (a titan node with isGoldMine; R10 §4): its marker, object and team.
struct Well {
  std::uint32_t marker = 0;
  std::uint32_t object = 0;
  int team = 0;
  float pos[3] = {};
  void* titanNodeDef = nullptr;  // D9: numFollowerSlots
  std::vector<WellSpot> spots;   // D9: the worker spots
};
const std::vector<Well>& Wells();

// The lane (nLaneID: 1 Top, 2 Bottom) whose spline passes nearest (x, y), and that distance; 0 without lanes.
int NearestLane(float x, float y, float* distance);

// D6a: the knots of `team`'s spline of lane `laneId` (nLaneID), knot 0 at that team's base; false if there is none.
// The bots' lane fronts use team 1's splines (gms/core/bots/lanes.h LanePath).
bool LaneKnots(int laneId, int team, std::vector<float>* xyz);

// MAPHOST: the level's creep camps without the parasite's (SM_Map_v5 22, MM_Map_v3 0); -1 before the level was read.
// The bots' setup seats no Hunter on a level without camps (gms/core/bots/composition.h NoJungleSeat).
int JungleCamps();

// D6e: the nearest creep camp (not the parasite's) with a living creature, by its marker position; false when
// every camp is empty (cleared, waiting for the respawn, or not spawned yet). Backs nDirector.GetClosestCampPosition.
// HUNTERBOT: with `team` (1, 2) and a penalty, a camp on that team's enemy half costs the penalty more
// (gms/core/bots/jungle.h CampRouteCost).
bool ClosestCampPosition(float x, float y, float* out, int team = 0, float enemySidePenalty = 0.f);

// The server level's LevelGameplayTuning (Level +0x614, read at OnLevelStarted; the defaults are SM_Map_v5's values).
struct Tuning {
  bool read = false;
  float waveDelay = 60.f;       // minionSpawnDelay1 (+0x0C)
  float waveInterval = 30.f;    // minionSpawnInterval1 (+0x14)
  float nubRespawn = 480.f;     // nubRespawnTime (+0xDC)
  float wellLockFirst = 900.f;  // goldMineLockedDurationFirstInSeconds (+0x7C)
  // MAPHOST: the lane agents (read and logged); MAPSTRIDER: spawned (gms/core/matchflow/laneagents.h)
  int wavesBeforeLaneAgents = 0;   // numMinionWavesBeforeLaneAgentSpawns (+0x2C)
  int wavesBetweenLaneAgents = 0;  // numMinionWavesBetweenLaneAgentSpawns (+0x30)
  bool striderSpawnEnabled = false;  // +0xF0
  int striderMortal = 0, striderSpirit = 0;  // the lists' lengths (+0xE0, +0xE8)
  // MAPSTRIDER: the lists' noun assets (team 1 striderMortal, team 2 striderSpirit; the first kMaxStriders) and each
  // tower tier's laneAgentRespawnCounterAdjustment (tower[] +0xCC, 0x10 bytes each, +0x0C; read and logged only)
  static constexpr int kMaxStriders = 8;
  void* striderNouns[2][kMaxStriders] = {};
  int towerTiers = 0;
  int towerAdjust[4] = {};
};
const Tuning& LevelTuning();

// The object id that came from a marker (object +0x84), 0 if none (scanned at OnLevelStarted).
std::uint32_t ObjectOfMarker(std::uint32_t marker);

// CAMPFINISH: the jungle camp (not the parasite's) with a living member nearest (x, y) within `radius`: its index and
// name, each listed member's id and HP (dead ones 0; at most kMembers), and the living members' count, HP and max HP.
struct CampFight {
  static constexpr int kMembers = 8;
  int camp = -1;
  const char* name = "";
  int count = 0;
  std::uint32_t ids[kMembers] = {};
  float hps[kMembers] = {};
  int alive = 0;
  float hp = 0.f, maxHp = 0.f;
  float fullHp = 0.f;   // the most max HP its living members had together (the camp's full HP while it lives)
  float nearest = 0.f;  // the nearest living member's distance
  float nearestPos[3] = {};  // ... and its position (CAMPFINISH round 3: the camp's last creature for the camp conditions)
};
bool CampFightNear(float x, float y, float radius, CampFight* out);
// CAMPFINISH: the living members of camp `camp` (CampFight::camp); -1 for no such camp.
int CampAlive(int camp);

void FormatStats(char* buf, std::size_t size);

}  // namespace director
