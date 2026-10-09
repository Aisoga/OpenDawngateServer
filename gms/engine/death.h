// D1f-live: death processing on the server simulator, which the client build lacks as a whole (docs/DAMAGE.md *Death
// handling*), plus the gold the stripped game mode handed out (docs/MATCH_FLOW.md *Game-mode handlers, energy lines and
// spirit wells (R10)* §5). What runs where:
//   - OnDeath (the damage core's lethal hit, and the GameObject_Kill 0x008E0210 detour after the client's kill core):
//       1. OnNearbyDeath (32) {nearbyDeathVictim, nearbyDeathKiller} on the victim itself (the Binding and parasite
//          passives learn of their own death that way), the killer, and every object with an ability container within
//          the victim's reward radius (CharacterRewardData.overrideRadius, else RewardsTuning's minion / jungle / player
//          radius) [assumed recipients];
//       2. OnKill (65) {onKillVictim} on the killer;
//       3. a player-controlled victim: OnPlayerDeath (51) {playerDeathVictim, playerDeathLastPlayerToHit,
//          playerDeathAssistPlayers (D1h: the assisters' creatures), myKillerID} on every player's creature and
//          OnPlayerKillOrAssist (66) on the killer's and (D1h) the assisters';
//       3b. D1h: the kill bookkeeping (below), a player kill's gold and XP, the death recap; a minion's or creep's last
//          hitter gets +1 creep score (LabsPlayer_AddCreeps 0x009123E0);
//       3c. D11b: a player kill's voice lines, the killer's KillBanter* and one assister's AssistBanter (banter.h);
//       4. rewards: GameObject_GetRewardForNPC (0x0091FF80) amounts, split by the reward types
//          (gms/core/damage/rules.h SplitReward) among the receiving team's players, granted through
//          LabsPlayer_GrantXPIfValid (0x00915770) and LabsPlayer_GrantGold (0x00912790); D14: then the reward data's
//          buffs (CharacterRewardData.buffs, which nothing in the client reads: the jungle majors' Green/Blue/Orange
//          buffs to the last player to hit, the parasite's stage 2/3 buffs to the team; gms::damage::BuffReceivers,
//          living receivers only) through LabsAbilities_ActivateFromDefinition, whose Unique rule renews a buff;
//       5. the victim's instances at that moment are remembered; after `endRuns` Lua scheduler runs (so the passives
//          woken in step 1 reach their reports: modifier_Binding_Passive's Tick yields twice before it calls
//          OnTitanTowerDestroyed) its modifiers with OnAgentDeath (deactivationType bit 4) are ended
//          (LabsAbilities_EndAbility) and its abilities without finishOnDeath interrupted (LabsAbilities_InterruptAbility
//          0x00905FA0). The AI definition's deathAbility and Behavior_Death come from the behavior tree (D1b-live).
//          Q3: a player's item passives are left alone (the instances its cLabsPlayer's item slots hold, mItems[s] +8):
//          modifier_Item_Basic_FlatHealing (Basic_Hunger) has OnAgentDeath, nothing in the client build activates an
//          item's passive again except LabsPlayer_OnItemAdded (0x00912B20) at a purchase, and the slot kept the dead
//          id, so the item lost its passive for the rest of the match and its upgrade asserted (LabsPlayer.cpp 278
//          "Item slot is holding onto stale passive instance id", q3_run1); death_keep_item_passives=0 ends them.
//   - the OnGrantGold stub (0x008FE500, "server only function ... from the client build", 0 gold) is replaced: every
//     grant runs the creature's modifiers' OnGrantGold (48) {grantGoldAmount, grantGoldTarget, grantGoldKiller} and
//     returns the threaded amount (the roles' gold hooks);
//   - starting gold (RewardsTuning.startingGold 500, source Start) at the deploy, passive gold (passiveGold 100 per
//     minute from passiveStartDelayInSeconds 60, source Passive, whole units) from the match's once-a-second step.
//   - D1g, a player's death timer and respawn (docs/DAMAGE.md *D1g*): at a player creature's death the cLabsPlayer's
//     mDeathTime = the server clock and mRespawnTime = it + LevelGameplayTuning deathTimeBase + deathTimePerLevel x
//     level + deathTimePerMinute x match minutes (gms::damage::DeathSeconds; the replication's A9 delta carries both to
//     the client's death timer); once the server clock reaches mRespawnTime (checked after every scheduler run) the same
//     creature is stopped (ServerCommandQueue_QueueStop), teleported to the player's start point
//     (GameSimulator_GetPlayerStartPoint, GameObject_Teleport: ObjectTeleport 98) and resurrected (GameObject_Resurrect:
//     HP full, OnRevive on its modifiers), as ability_returntobase and the nub's death ability do; its nBehavior_Death,
//     which waits for HP > 0, then ends and lifts Immobilized/Untargetable.
//   - D11 (docs/GMS_PROTOCOL.md *Spawn presentation (D11)*): right after the resurrect, the ClientFeedbackEvent PlayerRespawned about the creature
//     to its team's players (nClientFeedback.SendEventToPlayersOnTeam's path, as ability_returntobase sends
//     PlayerStartedReturnToBase): the client plays "Play_<PlayerClass audioIdentifier>_VO_respawn" on it. Nothing in the
//     client or the server scripts names that def, so the stripped server shell sent it [inferred: the respawn hook
//     0x009A3F00 that GameObject_OnRevived calls for players].
// Everything runs on the engine thread with the server simulator bound; every engine call is under SEH.
// Settings ([host] keys, or [dgsim] in the harness): death (1), death_nearby (1), death_rewards (1), death_gold (1: the
// OnGrantGold replacement), death_end_runs (3), death_interrupt (1; since D6c only abilities that still hold the agent,
// death_interrupt_released=1 for every one), death_kill_hook (1), death_log_limit (300),
// gold_start (1), gold_passive (1), respawn (1: D1g's timer and respawn), respawn_s (0 = LevelGameplayTuning's
// formula; > 0 a fixed wait for tests), respawn_feedback (1: D11's PlayerRespawned), reward_buffs (1: D14's buffs).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "control/messages.h"

namespace death {

struct Options {
  bool enabled = true;
  bool nearby = true;      // step 1-3: the death events
  bool rewards = true;     // step 4
  bool goldHook = true;    // the OnGrantGold replacement
  bool killHook = true;    // the GameObject_Kill detour
  bool interrupt = true;   // step 5's ability interrupts
  bool interruptReleased = false;  // D6c: also abilities that released the agent (D1f's rule; the engine's is holding only)
  int endRuns = 3;         // step 5's delay in Lua scheduler runs
  bool startGold = true;
  bool passiveGold = true;
  int logLimit = 300;
  bool respawn = true;          // D1g: the death timer and the respawn
  float respawnSeconds = 0.f;   // D1g: 0 = LevelGameplayTuning's formula, > 0 a fixed wait
  bool keepItemPassives = true;  // Q3: step 5 leaves the passives of a player's items alone (death.cpp)
  bool respawnFeedback = true;  // D11: PlayerRespawned (the shaper's "respawn" voice line) to the team at the respawn
  bool rewardBuffs = true;      // D14: CharacterRewardData.buffs (the jungle buffs, the parasite's team buffs) in step 4
};

// DllMain time: the GameObject_Kill detour and the OnGrantGold replacement.
bool Install(const Options& options);
bool Enabled();
void SetSim(void* sim);
// After GameSimulator_Init (server simulator bound): the match-stats object at sim +0x558 that the stripped server
// shell created. LabsPlayer_AddGold (0x00912440) and LabsPlayer_GrantXP (0x00914D80) add every grant to its player map
// at +0x194 (0x00AD8CC0 / 0x00AD8CF0 on GameSimulator_GetMatchStats 0x008EF480) without a null check, so without it
// every grant faults after the gold or XP was added (d1f_run1).
void OnSimulatorCreated(void* sim);

// Everything after a victim's HP reached 0 (killer may be null; killerId is the snapshot's caster then). `how` is
// logged ("damage", "kill").
void OnDeath(void* victim, void* killer, std::uint32_t killerId, void* snapshot, const char* how);

// After each GearsLua_TickThreads (server simulator bound): the due instance ends, then the due respawns (D1g) and the
// respawned creatures' follow-up log.
void AfterScheduler();

// Gold through LabsPlayer_GrantGold (so OnGrantGold runs): `luaSource` is nGoldSourceType's value.
bool GrantGold(int playerIndex, float amount, int luaSource, bool hide, std::uint32_t target, std::uint32_t killer);
// The deploy of player `index`'s shaper (server simulator bound): starting gold.
void OnPlayerDeployed(int playerIndex);
// match::Tick's once-a-second step (bound): passive gold for every player with a team.
void PassiveGoldTick(double matchSeconds);

// "deaths N (damage N, kill N), nearby events N, ..." for the stats lines.
void FormatStats(char* buf, std::size_t size);

// ---- D1h: player kill bookkeeping (docs/DAMAGE.md *D1h*) -------------------------------------------------------------
// The damage core's lethal hit on a player's creature, before its CombatEvent report: the death is counted here
// (gms::damage::KillBook; the killer is the victim's last player to hit of another team, the assisters come from the
// assist tracker at sim +0xCD84, which OnSimulatorCreated creates): the killer's mKills, kill streak (+0x260) and
// multi-kill count (+0x250), the victim's mDeaths (streak 0), the assisters' mAssists. The report then reads the
// killer's two fields for its flags 0x4000/0x2000 and takes the victim's streak (0x8000) and the killer's previous
// one as arguments; FirstKill (0x400000) comes in as extra flags. OnDeath takes the counted outcome (a GameObject_Kill
// death is counted in OnDeath itself) for the events' assisters, a player kill's gold and XP and the death recap.
struct KillReport {
  bool counted = false;
  unsigned extraFlags = 0;
  unsigned victimKillStreak = 0;
  unsigned killerPrevKillStreak = 0;
};
KillReport BeforeKillingBlowReport(void* victim);
// Every enemy hit on a player-controlled object (the damage core's step 11, beside Damage_RecordAttacker): logged for
// the victim's death recap (the last UITuning.deathRecapLongCombatTime seconds).
void RecordPlayerHit(void* target, void* attacker, float dealt, unsigned descLo, std::uint32_t ability);
// Messages for one player's connection, queued by the death processing (the death recap, C9) for the GMS side.
struct Outgoing {
  int player = -1;  // gameplay index
  std::vector<std::uint8_t> msg;
  std::string what;
};
void TakeOutgoing(std::vector<Outgoing>* out);
// At the match's end (match::EndMatch, bound): one playerFinish line per player with the counts kept (the control
// channel's line, gms/core/control; sent since C1b). D8-live: with a rewards provider set ([host] rewards=own) the line
// carries the host's own rewards too (playerFinish.rewards; dgserver keeps their bag, Destiny and points).
void LogPlayerFinish(int winnerTeam, unsigned long long durationMs, unsigned descriptors);
using FinishRewardsFn = bool (*)(int index, bool won, unsigned long long durationMs, unsigned descriptors,
                                 dggms::control::Rewards* out);
void SetFinishRewards(FinishRewardsFn fn);
// D8-live test fixture ([host] end_stats=K,D,A,creeps,gold,level[,role]; MATCH_FLOW.md T5): written into every server
// player at the end, before the playerFinish lines and this tick's A9/AA deltas (bound). B7c-live: the optional role
// (0 gladiator .. 3 predator) is written into mRole, so playerFinish.role (1 + mRole) names another profile role bar.
struct TestStats {
  int kills = 0, deaths = 0, assists = 0, creeps = 0, level = 1;
  int role = -1;  // -1 = keep mRole
  float totalGold = 0.f;
};
void SetTestStats(const TestStats& t);

}  // namespace death
