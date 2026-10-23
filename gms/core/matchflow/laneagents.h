// MAPSTRIDER: the lane agents (striders), the creatures of a level's super-minion spawners (SpawnPointDef type 4).
//
// The data [data: AssetData]: LevelGameplayTuning numMinionWavesBeforeLaneAgentSpawns (+0x2C),
// numMinionWavesBetweenLaneAgentSpawns (+0x30), striderMortal / striderSpirit (+0xE0 / +0xE8, noun lists) and
// striderSpawnEnabled (+0xF0); each tower tier's laneAgentRespawnCounterAdjustment (tower[] +0xCC, entry +0x0C).
// SM_Map_v5's Tuning: 0, 0, empty lists, false (its spawnpoint_*_Strider spawners stay unused). MM_Map_v3's
// Tuning_MM_V3: 6, 4, both lists Minion_MM_v3_Boar_Major, _Flocklord, _Deer_Major, true; towers 0 and -1. Nothing in
// the client reads any of them (the director's tick was stripped, MATCH_FLOW §2.1), so the rule is the host's
// [assumed rule]:
//   - a countdown per team, started at `before`: every minion wave that finds it at 0 carries a lane agent and starts
//     it again at `between`; any other wave counts it down by one. So the lane agents come with waves before + 1,
//     before + between + 2, ... (MM_Map_v3: waves 7, 12, 17, ..., at 240, 390, 540 s of match time): `before` waves
//     without one first, then `between` waves without one between two;
//   - the n-th lane agent of a team (n = 0, 1, ...) is the team's list entry n, and the list's last from then on
//     (escalation: MM's three nouns pay 100 / 250 / 500 gold, 150 / 300 / 600 XP; SM_Map_v5's unused Striders are
//     Small / Medium / Large); `cycle` takes entry n mod size instead;
//   - one creature per super-minion spawner of the team per lane-agent wave, on the spline its targetMarkerId names;
//     with an empty list the spawner's own composition.
// A tower's adjustment (`Adjust`) moves the countdown; the host doesn't apply it yet (which team's countdown, and the
// tier order on a two-tier level, are open). Header-only; gms_tests MapStrider*.
#pragma once

namespace gms {
namespace flow {

class LaneAgentCountdown {
 public:
  void Init(int before, int between) {
    between_ = between < 0 ? 0 : between;
    left_ = before < 0 ? 0 : before;
    spawned_ = 0;
  }
  // A minion wave fires: true when it carries a lane agent (then Spawned() counts it).
  bool OnWave() {
    if (left_ > 0) {
      --left_;
      return false;
    }
    left_ = between_;
    ++spawned_;
    return true;
  }
  // laneAgentRespawnCounterAdjustment (-1: one wave sooner); never below 0.
  void Adjust(int delta) {
    left_ += delta;
    if (left_ < 0) left_ = 0;
  }
  int Left() const { return left_; }        // waves without a lane agent before the next one
  int Spawned() const { return spawned_; }  // lane-agent waves so far
  int Between() const { return between_; }

 private:
  int between_ = 0, left_ = 0, spawned_ = 0;
};

// The 0-based wave index of the n-th lane-agent wave without adjustments (MM_Map_v3: 6, 11, 16, ...); -1 for n < 0.
inline int LaneAgentWaveIndex(int before, int between, int n) {
  if (n < 0) return -1;
  if (before < 0) before = 0;
  if (between < 0) between = 0;
  return before + n * (between + 1);
}

// Which list entry the n-th lane agent of a team is (see above); -1 for an empty list or n < 0.
inline int LaneAgentListIndex(int n, int listSize, bool cycle) {
  if (listSize <= 0 || n < 0) return -1;
  if (cycle) return n % listSize;
  return n < listSize ? n : listSize - 1;
}

// Whether the director spawns lane agents: `option` is [host] lane_agents (-1 the level's striderSpawnEnabled, 0 never,
// 1 always).
inline bool LaneAgentsOn(int option, bool striderSpawnEnabled) {
  if (option == 0) return false;
  if (option > 0) return true;
  return striderSpawnEnabled;
}

}  // namespace flow
}  // namespace gms
