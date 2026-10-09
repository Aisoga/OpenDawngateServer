// D9: the spirit wells, their workers and the match economy on the match host (docs/MATCH_FLOW.md *D9: the spirit
// wells on the match host*; the rules are gms/core/wells/wells.h, D9-core). The game mode that ran the wells was
// compiled out of the client: nothing reads the gold-mine tuning, the titan-node state is only ever reset, nothing fills
// the follower parameters. This module wires the engine-free rules to the server simulator:
//   - at the level load (OnLevelStarted, from match::OnLevelStarted): the server's UI node list
//     (BattlegroundsUIData_CreateNodeList 0x008DDAF0, one node per well, every member written), Wells::Init with the
//     server level's LevelGameplayTuning (+0x64...+0xAC), RewardsTuning.titanGoldRewardInterval, the well noun's
//     numFollowerSlots and the worker spots (the well markers' children, director.h), and the wells' titan-node state;
//   - every match tick (Tick, from match::Tick, server simulator bound): Wells::Tick with every player's creature (team,
//     HP > 0, position, the match time of the last damage it took and, since D9c, of the last enemy player's hit:
//     OnDamaged), then the result:
//       - titan-node states into the well object (+0x88E, 90's mInteractableState high half) with
//         mGraphicsStateStartTimeMs (+0x840) = the server clock;
//       - a capture: the object's mTeam (+0x4D), a killing-blow combat event on the well with a capturer as the source
//         (GameSimulator_QueueCombatEvent 0x008F7360: the client's CombatEvent_KillingBlow shows MineCaptured), the
//         well's workers destroyed (bg_follower_death_effect and MarkForDelete, as modifier_TitanFollower_Passive's
//         Deactivate does with an orb effect the data no longer has; no Worker reward), the award and the bounty;
//       - workers: goldMineFollowerSlotNoun (Titan_Node_Follower) created at a free worker spot for the owner
//         (GameObject_CreateFromNoun 0x008E1EC0; since D9b with the well as the creature's owner, +0x48, so the
//         client's own CombatEvent_DamageAbsorb.lua names the well in MineUnderAttack when an enemy damages a
//         worker) with mFollowerParams (mMine = the well, mGatherPoint = its spot,
//         mDropOffPoint = the well's rim toward the spot [assumed], the return-to-work point = the spot), so its own
//         nBehavior_FollowerMining starts the mining loop (Locomotion_StartFollowerMining 0x00919ED0) and the 9C delta
//         carries the loop to the clients;
//       - team gold through death::GrantGold (LabsPlayer_GrantGold, OnGrantGold): well gold (WellPassive 8), the capture
//         award (Objective 6) and bounty (WellBounty 9) to every player of the team;
//       - D9 sent MineUnderAttack (nClientFeedback.SendEventToPlayersOnTeam) to the owner when a capture started
//         [assumed]; since D9b only with [host] wells_capture_announce=1, and only while the capture has at least
//         wells_capture_announce_min_s left (no script or exe code sends it; the client's own announcement about the
//         damaged worker's owner is the original path [inferred]);
//       - the 8C fields of the nodes in Wells::TakeSendMask, and mBlueGpm / mRedGpm (UI data +0x06 / +0x04) =
//         Wells::TeamGpm: D1c-live's delta writer sends what changed;
//       - once a second, VimPer5 (attribute 69, gold per 5 s, source Passive 11 [assumed]) to every player;
//   - a worker's death (OnObjectDied, from the death processing) frees its slot (Wells::OnWorkerDied);
//   - the bots' getters (RegisterLua, before the level): nBattlegrounds.GetMinesForTeam / GetFollowersForTitan /
//     IsTitanNodeLocked over the client's stubs, and the same as nBotHost.MinesForTeam / FollowersForTitan /
//     IsTitanNodeLocked for gms/engine/lua/dgsim_bots_api.lua.
// Everything runs on the engine thread with the server simulator bound; every engine call is under SEH.
// Settings ([host] keys, read by Configure): wells (1; with match_wells), wells_lock_first / wells_lock_after /
// wells_worker_period (-1 = the tuning's 900 / 240 / 15 s; test aids), wells_workers (1), wells_gold (1),
// wells_damage_pause (1; 0 = no hit pauses a capture), since D9c wells_damage_pause_any (0: only an enemy player's hit
// pauses, the damage tracker's "player damage"; 1 = D9's rule, any damage > 0) and wells_hit_log_limit (200: the
// "wells: hit" lines for hits on a shaper inside an enemy well's ring), wells_dropoff (4: the drop-off point's distance
// from the well's centre toward the spot; 0 = the centre) [assumed], wells_gather_at_well (0: gather at the spot, drop
// off at the well [assumed]; 1 the other way), wells_transition_s (2), vim_per5 (1), wells_trace_s (10), wells_log_limit (300); since D9b (and D14)
// wells_worker_owner (1: the well owns its workers; 0 = D9's owner 0), since D9b wells_capture_announce (0: the host
// sends no MineUnderAttack of its own; 1 = D9's announcement at a capture's start) and wells_capture_announce_min_s (4:
// only while the capture needs at least that long to complete, so the queued announcement plays before the well flips
// [assumed margin: the def's 3-s lifetime and the announcer queue]).
#pragma once

#include <cstddef>
#include <cstdint>

namespace wells {

struct Options {
  bool enabled = true;
  double lockFirst = -1.0;       // < 0: LevelGameplayTuning goldMineLockedDurationFirstInSeconds (900)
  double lockAfter = -1.0;       // < 0: goldMineLockedDurationInSeconds (240)
  double workerPeriod = -1.0;    // < 0: goldMineFollowerAutoFillPeriod (15)
  bool workers = true;
  bool gold = true;
  bool damagePause = true;
  float dropOff = 4.f;
  bool gatherAtWell = false;
  float transitionSeconds = 2.f;
  bool vimPer5 = true;
  double traceSeconds = 10.0;
  int logLimit = 300;
  bool workerOwner = true;                 // D9b (D14): the well is its workers' owner (+0x48)
  bool captureAnnounce = false;            // D9b: no host MineUnderAttack by default (D9's was [assumed])
  double captureAnnounceMinSeconds = 4.0;  // with captureAnnounce: only while the capture needs at least this long
  bool damagePauseAny = false;             // D9c: false = only an enemy player's hit pauses; true = D9's any damage
  int hitLogLimit = 200;                   // D9c: the "wells: hit" lines (hits on a shaper inside an enemy well's ring)
};

// Reads the [host] keys (host mode only; match::Configure calls it).
void Configure();
bool Enabled();

// Before the level (match::OnSimulatorCreated, bound): the bots' getters over the stubs and into nBotHost.
void RegisterLua(void* L, void* sim);
// After the level load (match::OnLevelStarted, after the director read the wells, bound).
void OnLevelStarted(void* sim);
// Every match tick after the director (bound). `t` = match seconds; `ended`: the match is over (nothing moves).
void Tick(void* sim, double t, bool ended);

// The damage core dealt `dealt` > 0 to `target` from `attacker` (the snapshot's caster, may be null): on a player's
// creature an enemy player's hit (core IsPlayerHit: player-controlled or a player's pet, the damage tracker's "player
// damage") pauses its capture for goldMineCaptureHitPlayerDurationInMS (D9c); with wells_damage_pause_any=1 any hit
// does (D9).
void OnDamaged(void* target, void* attacker, float dealt);
// The death processing saw `id` die: a worker frees its slot.
void OnObjectDied(std::uint32_t id);

void FormatStats(char* buf, std::size_t size);

}  // namespace wells
