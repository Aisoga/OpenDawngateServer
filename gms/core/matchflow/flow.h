// The match flow's engine-free rules (roadmap D1c-live; docs/MATCH_FLOW.md §1-§3 and *Game-mode handlers, energy
// lines and spirit wells (R10)*). The host's director and match module (gms/engine/director.cpp, match.cpp) keep their
// timers and decisions here, so gms_tests can check them without the game:
//   - the wave schedule (LevelGameplayTuning minionSpawnDelay1 / minionSpawnInterval1: waves at 60 + 30 k s on v5);
//   - a creep camp's timer (first spawn at spawnDelay, respawn respawnTime s after the camp was cleared [assumed rule,
//     MATCH_FLOW §2.6]) and which definition gives them (CAMPTIMER: the noun's by default, PickCampTimes);
//   - the end of the match (which Guardian died -> GameSimulator_EndGame's reason and the winner, §1.1, §3.4);
//   - the energy-line value -> mNubRespawnTime rule of nBattlegrounds.SetEnergyLineState (R10 §2);
//   - the tower table's slots (team index, tier from the Binding's noun name, §2.9);
//   - the wave formation [assumed, §2.2] and the distance from a point to a lane's polyline (a Binding's lane);
//   - the spawn composition (SpawnPointDef.campUpgrade[0] = counts of minor, medium, major, uber creatures);
//   - D1k: a wave's minions one by one (one constant delay between two spawns, MATCH_FLOW *D1k*) and the shape of a
//     group of minions (its length, width and closest pair: a line or a clump).
#pragma once

#include <cstdint>
#include <string>
#include <vector>

namespace gms {
namespace flow {

// Waves at delay + interval * k seconds of match time (k = 0, 1, ...); interval <= 0 means a single wave at `delay`;
// delay < 0 means none.
struct WaveSchedule {
  double delay = 60.0;
  double interval = 30.0;
  int next = 0;       // the number of the next wave (0 = the first)
  int limit = 0;      // stop after this many waves (0 = no limit; a test aid)

  double TimeOf(int k) const { return delay + (interval > 0.0 ? interval * k : 0.0); }
  bool Due(double matchSeconds) const;
  void Fired() { ++next; }
};

// One creep camp (or the parasite's camp): the first spawn at `first` s of match time; once every member is dead the
// camp is "cleared" and spawns again `respawn` s later [assumed: the timer starts when the camp is cleared].
// respawn <= 0: never again (the parasite resurrects itself, ability_Parasite_Death).
class CampTimer {
 public:
  void Init(double first, double respawn) {
    first_ = first;
    respawn_ = respawn;
    dueAt_ = first;
    alive_ = false;
    spawns_ = 0;
  }
  bool Due(double matchSeconds) const { return !alive_ && dueAt_ >= 0.0 && matchSeconds >= dueAt_; }
  void OnSpawned() {
    alive_ = true;
    ++spawns_;
  }
  // All members dead at `matchSeconds`: the next spawn is due respawn s later (or never).
  void OnCleared(double matchSeconds) {
    alive_ = false;
    dueAt_ = respawn_ > 0.0 ? matchSeconds + respawn_ : -1.0;
  }
  bool Alive() const { return alive_; }
  double DueAt() const { return dueAt_; }
  int Spawns() const { return spawns_; }
  double First() const { return first_; }
  double Respawn() const { return respawn_; }

 private:
  double first_ = 0.0, respawn_ = 0.0, dueAt_ = 0.0;
  bool alive_ = false;
  int spawns_ = 0;
};

// CAMPTIMER: which of a camp's two timer sets the director uses ([host] camp_timers, MATCH_FLOW §2.2 *Which definition
// counts*). A camp marker may carry its own SpawnPointDef (componentData.spawnPointDef) beside its noun's, and the
// stripped spawner's choice between them is lost. On SM_Map_v5 the nouns say buffs 90/300 s, small camps 90/180 s,
// the Boar (Gold Pigs) 210/300 s; the markers override the small camps with 90/60 s (two Fungus camps 110/180 s) and
// the Boar with 120/300 s. kNoun (the default; the user's decision of 2026-10-28, its source a fan site's map of May
// 2014: the pigs "Spawn: 3:30, Respawn 5:00" = the noun): the noun's spawnDelay and respawnTime, the marker's only for
// a camp whose noun has none. kMarker: the marker's where it has one (the host's rule until CAMPTIMER).
enum class CampTimerSource : int { kNoun = 0, kMarker = 1 };

// The [host] camp_timers value: "marker" (any case) or "1" -> kMarker; anything else (default "noun") -> kNoun.
inline CampTimerSource CampTimerSourceOf(const char* s) {
  if (!s) return CampTimerSource::kNoun;
  if (s[0] == '1' && s[1] == 0) return CampTimerSource::kMarker;
  const char* m = "marker";
  int i = 0;
  for (; s[i] && m[i]; ++i) {
    const char c = (s[i] >= 'A' && s[i] <= 'Z') ? static_cast<char>(s[i] - 'A' + 'a') : s[i];
    if (c != m[i]) return CampTimerSource::kNoun;
  }
  return (s[i] == 0 && m[i] == 0) ? CampTimerSource::kMarker : CampTimerSource::kNoun;
}

inline const char* CampTimerSourceName(CampTimerSource s) { return s == CampTimerSource::kMarker ? "marker" : "noun"; }

struct CampTimes {
  double first = 0.0;       // the first spawn, s of match time
  double respawn = 0.0;     // s after the camp was cleared
  bool fromMarker = false;  // the marker's set (else the noun's)
  bool ok = false;          // false: neither definition was readable
};

// A camp's timers from its noun's definition (`nounOk`, first, respawn) and its marker's (`markerOk`, ...) by `src`.
inline CampTimes PickCampTimes(CampTimerSource src, bool nounOk, double nounFirst, double nounRespawn, bool markerOk,
                               double markerFirst, double markerRespawn) {
  CampTimes t;
  const bool useMarker = markerOk && (src == CampTimerSource::kMarker || !nounOk);
  if (useMarker) {
    t.first = markerFirst;
    t.respawn = markerRespawn;
    t.fromMarker = true;
    t.ok = true;
  } else if (nounOk) {
    t.first = nounFirst;
    t.respawn = nounRespawn;
    t.ok = true;
  }
  return t;
}

// GameSimulator_EndGame's reason when the Guardian of `deadTeam` died (MATCH_FLOW §1.1): the Spirit Guardian (team 2)
// -> 1 (Mortal wins), the Mortal Guardian (team 1) -> 3 (Spirit wins); 0 for any other team.
uint32_t EndReasonForDeadGuardian(int deadTeam);
// The winning team of an end reason, as GameSimulator_EndGame (0x008EF160) sets +0x19E54: 1, 2 -> 1; 3, 4 -> 2; else 0xFF.
int WinnerOfReason(uint32_t reason);
// True for the surrender reasons (2, 4), as GameOverState_OnGameOver reads them.
inline bool IsSurrender(uint32_t reason) { return reason == 2 || reason == 4; }

// nBattlegrounds.SetEnergyLineState's value -> mNubRespawnTime (R10 §2): value < 0 -> all ones (gone for good), 0 -> 0
// (lit), > 0 -> the server's game clock in ms now + value * 1000 (lit again then).
uint64_t EnergyLineTime(float value, uint64_t nowMs);
constexpr int kEnergyLines = 12;

// The tower table (cBattlegroundsUIData mTowers[team][lane][4], §2.9): team 1 -> 0, 2 -> 1, else -1
// (TowerGraphics_GetTeamIndex 0x00537D90).
int TeamIndex(int team);
// The tier of a Binding from its noun name "Binding_<Team>_<Tier>" (an asset name's ".Noun" is ignored): Outer 0,
// Middle 1, Inner 2 [assumed order, the markers chain Outer -> Middle -> Inner]; -1 for anything else.
int BindingTier(const std::string& nounName);
// The team of a Binding from its noun name: "Binding_Mortal_*" 1, "Binding_Spirit_*" 2, else 0.
int BindingTeam(const std::string& nounName);

// The base structures (D5; SERVER_LUA_API.md nBattlegrounds.AddBaseStructuresToAggroList): the five nubs per team
// ("nub_<team>_v5_<A-E>", noun type Tower, NPC type nub, named "Core" in the UI) and the Guardian.
// The team of a nub from its noun name: "nub_mortal_*" 1, "nub_spirit_*" 2, else 0.
int NubTeam(const std::string& nounName);
struct BaseStructure {
  uint32_t id = 0;
  int team = 0;
  bool alive = false;  // HP > 0 (a dead nub waits 480 s for its own resurrect)
};
// What AddBaseStructuresToAggroList(minion) puts on a lane minion's aggro list [assumed rule; the original was compiled
// out with the game mode]: every living base structure of the other team (Mortal 1 against Spirit 2 and back; nothing
// for a minion of any other team), in the order given. The host orders the list itself (gms/core/agents/aggro.h).
std::vector<uint32_t> BaseStructuresAgainst(int minionTeam, const std::vector<BaseStructure>& all);

// D14: the Guardian's shield. "The Guardian is surrounded by five Cores. It is invincible as long as any Cores remain
// intact." (locale 0x11a95cbb, the Guardian Cores tip) [text]; the mechanism is modifier_Tower_Immunity (Immune and
// Unattackable, the titan_invulnerability effect on the Guardian), which no script and no AssetData applies: the
// stripped server did. The step for one Guardian: +1 apply the shield, -1 remove it, 0 nothing. Shielded while a core
// of its team lives; a resurrected core (480 s after its death) brings it back [assumed: "as long as any Cores remain
// intact" read as a state]; nothing for a dead Guardian.
inline int GuardianShieldStep(bool shielded, int livingCores, bool guardianAlive) {
  if (!guardianAlive) return 0;
  if (livingCores > 0 && !shielded) return 1;
  if (livingCores <= 0 && shielded) return -1;
  return 0;
}

// PARAMAP: a camp's minimap icon. The client draws a camp spawn point's icon (its noun's minimapDefinition: the
// parasite's SpawnPoint_Objective_Titan is Creep_Major, objective_major.tga) only while the camp's UI entry
// (cBattlegroundsUICampData +0 mCampVisibleToTeam, sent in 8D) has the local team's bit, 1 << team
// (GGMinimap_UpdateObjectIcon 0x005801F0); the director's own value is the NPC bit 1 alone. The original's writer
// was stripped. The rule: the user's memory of the original (2026-10-15: "when Parasite is up/spawns there was an
// icon of him in the middle of the minimap, removed when he was killed", like Baron) for the parasite; the other
// camps by the same rule only on request [assumed]. `mode`: 0 off, 1 the parasite's camp, 2 every camp, 3 (CAMPICONS,
// the default) the parasite's camp as 1 and the jungle camps by each team's knowledge (camps.h CampKnowledge; this
// function gives a jungle camp's truth then, the spectators' value). `up`: the camp has a living, born member (the
// parasite: alive and its HideHealthBar gone, i.e. after its 300-s birth).
constexpr uint8_t kCampIconNpcBit = 0x01;
constexpr uint8_t kCampIconPlayerBits = 0x06;  // Mortal (team 1) and Spirit (team 2)
inline uint8_t CampIconBits(int mode, bool parasiteCamp, bool up) {
  const bool chosen = mode >= 2 || (mode == 1 && parasiteCamp);
  return static_cast<uint8_t>(kCampIconNpcBit | (chosen && up ? kCampIconPlayerBits : 0));
}

struct Point {
  float x = 0.f, y = 0.f, z = 0.f;
};

// The 2D distance from (x, y) to the polyline through `pts` (in order); a huge value without points.
float DistanceToPolyline(float x, float y, const std::vector<Point>& pts);

// Where minion i of a wave stands, relative to its spawner [assumed formation, MATCH_FLOW §2.2]: rows of three
// across the lane, the first row (the first three: melee) `spacing` units toward knot 0, the next row at the spawner,
// then behind it; (dirX, dirY) is the unit direction from the spawner to knot 0.
void FormationOffset(int i, float dirX, float dirY, float spacing, float* dx, float* dy);

// A spawn definition's composition: campUpgrade[0]'s four floats (minor, medium, major, uber counts) rounded; negative
// or absurd counts (> 32) count 0.
struct Composition {
  int counts[4] = {0, 0, 0, 0};
  int Total() const { return counts[0] + counts[1] + counts[2] + counts[3]; }
};
Composition CompositionOf(const float upgrade0[4]);

// D14: which creature marker (a camp marker's child, in marker order) each of a camp's creatures stands on. `tiers` are
// the creatures in creation order (0 minor .. 3 uber, the composition's order), `scales` the child markers' cLabsMarker
// .scale. In the six buff camps the first child has scale 2.0 and the others 1.0, and exactly those markers are the
// tutorial's buff pings (tutorialUtils.posGreenBuff/posBlueBuff/posOrangeBuff), so the major belongs there [inferred];
// D1c-live put the creatures in creation order, which left a minor on it. The rule: the markers whose scale is above
// the camp's smallest go, largest first (ties in marker order), to the highest tiers (ties in creation order); every
// other creature takes the remaining markers in marker order. A camp whose markers all have one scale (the 16 small
// camps) keeps creation order. -1 = no marker left (the camp's own position).
std::vector<int> AssignCampSpots(const std::vector<int>& tiers, const std::vector<float>& scales);

// D1k: a wave's minions spawn one after another, member i at waveTime + i * interval (the user's footage of the
// original: one constant delay, also between the last melee and the first ranged minion; six minions between 01:00 and
// 01:03, so about 0.5 s).
// How many of a wave's `total` members are due at `now` (match seconds); interval <= 0 = all at once.
int StaggeredDueCount(double waveTime, double intervalSeconds, int total, double now);

// D1k: the shape of a group of minions (2D): `length` = the largest distance between two of them, `width` = the
// largest distance of any of them from the line through that pair, `closest` = the smallest distance between two of
// them, `meanNearest` = the mean distance from each to its nearest neighbour. A line has length >> width; a clump has
// both small. Fewer than two points: everything 0.
struct GroupShape {
  int n = 0;
  float length = 0.f;
  float width = 0.f;
  float closest = 0.f;
  float meanNearest = 0.f;
};
GroupShape ShapeOf(const std::vector<Point>& pts);

}  // namespace flow
}  // namespace gms
