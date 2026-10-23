// gms_tests: MAPSTRIDER, the lane agents' rule (core/matchflow/laneagents.h): which waves carry one, which noun.
#include <cmath>
#include <vector>

#include "core/matchflow/flow.h"
#include "core/matchflow/laneagents.h"
#include "tests/check.h"

using namespace gms::flow;

namespace {

// The 0-based indices of the waves (of the first `waves`) that carry a lane agent.
std::vector<int> AgentWaves(int before, int between, int waves) {
  LaneAgentCountdown c;
  c.Init(before, between);
  std::vector<int> out;
  for (int k = 0; k < waves; ++k)
    if (c.OnWave()) out.push_back(k);
  return out;
}

}  // namespace

GMS_TEST(MapStriderCountdownMM) {
  // MM_Map_v3's Tuning_MM_V3: 6 waves before, 4 between: waves 7, 12, 17, 22 (1-based)
  CHECK(AgentWaves(6, 4, 23) == (std::vector<int>{6, 11, 16, 21}));
  for (int n = 0; n < 4; ++n) CHECK_EQ(LaneAgentWaveIndex(6, 4, n), AgentWaves(6, 4, 23)[n]);
  // ... at match time 60 + 30 k: 240, 390, 540 s
  WaveSchedule w;
  CHECK(std::abs(w.TimeOf(LaneAgentWaveIndex(6, 4, 0)) - 240.0) < 1e-9);
  CHECK(std::abs(w.TimeOf(LaneAgentWaveIndex(6, 4, 1)) - 390.0) < 1e-9);
  CHECK(std::abs(w.TimeOf(LaneAgentWaveIndex(6, 4, 2)) - 540.0) < 1e-9);
  LaneAgentCountdown c;
  c.Init(6, 4);
  for (int k = 0; k < 7; ++k) c.OnWave();
  CHECK_EQ(c.Spawned(), 1);
  CHECK_EQ(c.Left(), 4);
}

GMS_TEST(MapStriderCountdownEdges) {
  // the test settings lane_agent_waves_before=0, _between=0: every wave
  CHECK(AgentWaves(0, 0, 4) == (std::vector<int>{0, 1, 2, 3}));
  CHECK(AgentWaves(-3, -1, 3) == (std::vector<int>{0, 1, 2}));  // negative counts as 0
  CHECK(AgentWaves(2, 1, 8) == (std::vector<int>{2, 4, 6}));
  CHECK_EQ(LaneAgentWaveIndex(6, 4, -1), -1);
  // a tower's adjustment (not applied by the host yet): one wave sooner, never below 0
  LaneAgentCountdown c;
  c.Init(6, 4);
  c.Adjust(-1);
  CHECK_EQ(c.Left(), 5);
  std::vector<int> got;
  for (int k = 0; k < 7; ++k)
    if (c.OnWave()) got.push_back(k);
  CHECK(got == (std::vector<int>{5}));
  c.Adjust(-10);
  CHECK_EQ(c.Left(), 0);
  CHECK(c.OnWave());
}

GMS_TEST(MapStriderNouns) {
  // MM's list Boar_Major, Flocklord, Deer_Major: escalation, then the last one stays
  CHECK_EQ(LaneAgentListIndex(0, 3, false), 0);
  CHECK_EQ(LaneAgentListIndex(1, 3, false), 1);
  CHECK_EQ(LaneAgentListIndex(2, 3, false), 2);
  CHECK_EQ(LaneAgentListIndex(7, 3, false), 2);
  // [host] lane_agent_order=1: round
  CHECK_EQ(LaneAgentListIndex(3, 3, true), 0);
  CHECK_EQ(LaneAgentListIndex(5, 3, true), 2);
  // an empty list (SM_Map_v5): the spawner's own composition
  CHECK_EQ(LaneAgentListIndex(0, 0, false), -1);
  CHECK_EQ(LaneAgentListIndex(-1, 3, false), -1);
}

GMS_TEST(MapStriderSwitch) {
  // [host] lane_agents: -1 the level's striderSpawnEnabled (SM_Map_v5 false, MM_Map_v3 true), 0 never, 1 always
  CHECK(!LaneAgentsOn(-1, false));
  CHECK(LaneAgentsOn(-1, true));
  CHECK(!LaneAgentsOn(0, true));
  CHECK(LaneAgentsOn(1, false));
}
