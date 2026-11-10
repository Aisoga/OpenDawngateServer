// GLADVIM: the Gladiator's stored minions and the lane agents. Engine-free, gms_tests.
//
// modifier_Role_Gladiator (build 854's script) stores, for a melee Gladiator, vim x lastHitModifier 6 x
// meleeVimStoragePercentage 0.5 of every enemy minion (nGameObject.IsMinion: NPC types 0, 1, 2, 11, 12; well workers
// excluded by noun) that dies near him without his last hit, while modifier_Role_Gladiator_Counter has fewer than 3
// stacks, and pays the store on his next own last hit of a minion (OnGrantGold). On MM_Map_v3 the host's lane agents
// (MAPSTRIDER: Boar Major 100, Flocklord 250, Deer Major 500 vim, NPC type 2) counted too: a Deer killed by an ally
// beside him stored 1,500, more than his own last hit on it pays (500 + 500). The user's decision (2026-11-10): lane
// agents are not stored; the store is the lane minions' only (as on the original map, which spawned none). The host
// hides the script's OnNearbyDeath handler for a lane agent's death ([host] gladiator_skip_lane_agents, 1); his own
// last hit on a lane agent still pays the store and the lane agent's own vim as before.
#pragma once

#include <cstdint>

namespace gms::damage::gladiator {

constexpr int kNpcSuperMinion = 2;  // nNPCType: the lane agents' (and SM_Map_v5's unused Striders')

// A lane agent: one the director spawned (MAPSTRIDER) or any super minion.
inline bool IsLaneAgent(int npcType, bool spawnedLaneAgent) { return spawnedLaneAgent || npcType == kNpcSuperMinion; }

// Whether the host hides modifier_Role_Gladiator's OnNearbyDeath while a death's OnNearbyDeath fires.
inline bool HideStore(bool skipLaneAgents, int npcType, bool spawnedLaneAgent) {
  return skipLaneAgents && IsLaneAgent(npcType, spawnedLaneAgent);
}

// nGameObject.IsMinion (0x00942660 -> 0x00880F70): the NPC types it accepts.
inline bool IsMinion(int npcType) {
  return npcType == 0 || npcType == 1 || npcType == 2 || npcType == 11 || npcType == 12;
}

// The script's store, for the tests: one Gladiator instance and its counter.
struct Store {
  static constexpr float kLastHitModifier = 6.f;
  static constexpr float kStoragePercentage = 0.5f;
  static constexpr int kMaxStacks = 3;  // modifier_Role_Gladiator_Counter.maxStackCount
  bool melee = true;
  bool skipLaneAgents = true;  // [host] gladiator_skip_lane_agents
  float total = 0.f;           // totalStoredRewards
  int stacks = 0;              // the counter's stacks (0 = no counter)
  int unusedStacks = 0;
  int creepScore = 0;  // nPlayer.IncrementCreepScore's sum

  struct Death {
    int npcType = 0;
    bool spawnedLaneAgent = false;
    bool worker = false;  // Titan_Node_Follower
    bool enemy = true;
    bool killedByMe = false;
    float vim = 0.f;  // GetRewardForNPC's vim
  };

  // OnNearbyDeath (not called at all when the host hides the handler).
  void OnNearbyDeath(const Death& d) {
    if (HideStore(skipLaneAgents, d.npcType, d.spawnedLaneAgent)) return;
    if (!melee || !d.enemy || !IsMinion(d.npcType) || d.worker || d.killedByMe) return;
    if (stacks >= kMaxStacks) return;
    ++stacks;
    total += d.vim * kLastHitModifier * kStoragePercentage;
  }

  // OnGrantGold for a grant whose target is a dead enemy minion (not a worker) he killed: the amount plus the store.
  float OnOwnLastHit(float amount) {
    if (!melee || stacks == 0) return amount;
    const float paid = amount + total;
    total = 0.f;
    const int all = stacks + unusedStacks;
    creepScore += all / 2;
    unusedStacks = all % 2;
    stacks = 0;
    return paid;
  }

  // His death: both modifiers end (OnAgentDeath); the respawn activates a new instance.
  void OnDeath() { *this = Store{melee, skipLaneAgents}; }
};

}  // namespace gms::damage::gladiator
