// gms/core/wells (D9-core): the spirit wells' rules without the engine (docs/MATCH_FLOW.md *Game-mode handlers, energy
// lines and spirit wells (R10)* §4 and *The spirit wells' rules without the engine (D9-core)*). Header-only, no game
// addresses, like core/damage: the game mode that ran the wells was compiled out of the client (nothing reads the
// gold-mine tuning at run time, the titan-node state is only ever reset), so the host runs these rules and the harness
// (D9, main track) only wires them to the well objects, the worker creatures, the gold grants and the 8C/90 messages.
//
// What it does, per well (4 on SM_Map_v5, 8C's mask allows 8):
//   - the lock: locked until goldMineLockedDurationFirstInSeconds (900 s, "At fifteen minutes enemy Spirit Wells can be
//     captured"), then goldMineLockedDurationInSeconds (240 s) after every capture ("Once taken, a Well cannot be
//     captured again for four minutes"); a locked well can't be captured; SpiritWell_IsLocked is mLockedTimer > 0;
//   - the capture: living shapers of the other team within the capture sphere (titanNodeDef captureVolume, a 12-unit
//     sphere) move mCaptureScore toward 1 at the client's own speed table (LevelGameplayTuning_BuildCaptureSpeedTable
//     0x008DB920: 16/14/12/10/8 s for 1-5 capturers); a living shaper of the owning team in the sphere pauses it, as does
//     a hit by an enemy player within goldMineCaptureHitPlayerDurationInMS (5000 ms, speed multiplier 0) (tutorial:
//     "Your capture of the Well will pause if you take damage, or if an enemy Shaper is also within the Spirit Well's
//     ring"; since D9c only an enemy player's hit counts, as the engine's damage tracker records "player damage":
//     IsPlayerHit below); without capturers it decays (goldMineCaptureDecayRatePerSecond);
//   - the capture itself: the owner changes, every worker of the well dies ("When you capture a Well, all of its Workers
//     are destroyed"), the capture award (goldMineCaptureAwardGoldPerPlayer 50) and the bounty go to the capturing team;
//   - the workers (goldMineFollowerSlotNoun Titan_Node_Follower): none at the start (goldMineFollowerCountOnSpawn 0),
//     goldMineFollowerAutoFillCount (1) more every goldMineFollowerAutoFillPeriod (15 s) while the well has a free slot
//     (numFollowerSlots 18) and, in mode "gold mine not empty", gold left; each one owned by its well (D9b: the
//     client's own "well under attack" announcement names the damaged worker's owner);
//   - the well gold: mGoldPerMinute = goldMinedPerMinute (0) + goldMinedPerMinutePerFollower (1.5) x workers, paid to
//     every player of the owning team every titanGoldRewardInterval (1 s) in whole units (source WellPassive 8), and
//     the HUD's mBlueGpm/mRedGpm as the team sums;
//   - the titan-node state of the well object (+0x88E, the high half of 90's mInteractableState) and the 8C node fields,
//     with a send mask for 8C;
//   - the bots' getters GetMinesForTeam / GetFollowersForTitan / IsTitanNodeLocked over the same state.
// [assumed] marks every rule no client code, script or tutorial text shows (MATCH_FLOW.md D9-core *Open*). D9c
// (MATCH_FLOW.md *D9c*) narrowed the damage pause to an enemy player's hits (HitRule below).
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <utility>
#include <vector>

namespace gms::wells {

constexpr int kMaxWells = 8;           // UISpiritWellUpdate (8C) carries a u8 node mask
constexpr unsigned kMaxCaptureEntries = 5;  // cLevelGameplayTuning::MAX_GOLD_MINE_SPEED_MULTIPLIERS (0x008DB920)

// The titan-node state (object +0x88E, u16; TitanNode_GetGfxState 0x008FC430 picks titanNodeDef's graphics state by it,
// SpiritWellDisplay_Update 0x0043E5D0 shows the lock ring in 1 and the capture ring in 3/4, the minimap's padlock shows
// in 1 and 5: 0x008FC390). 0 is what TitanNode_InitState (0x008FC370) writes; it draws as unlocked.
enum TitanState : std::uint16_t {
  kStateNone = 0,
  kLocked = 1,         // graphicsState_locked ("darius_mine_dais_locked")
  kUnlocking = 2,      // graphicsState_unlocking ("darius_mine_dais_unlock", one shot)
  kUnlocked = 3,       // graphicsState_unlocked ("darius_mine_dais_switch_up")
  kBeingCaptured = 4,  // graphicsState_beingCaptured ("darius_mine_dais_switch_down")
  kLocking = 5,        // graphicsState_locking ("darius_mine_dais_lock", one shot)
  kDestroyed = 6,      // graphicsState_destroyed (none on the wells)
};
// The minimap's TitanNodeLockedMinimapData (the padlock) test, 0x008FC390: state 1 or 5.
inline bool StateShowsLocked(std::uint16_t s) { return s == kLocked || s == kLocking; }

// nGoldSourceType (Lua/GlobalDefinitions) of the grants this module asks for.
constexpr int kGoldSourceObjective = 6;   // the capture award [assumed source]
constexpr int kGoldSourceWellPassive = 8;
constexpr int kGoldSourceWellBounty = 9;

// goldMineFollowerAutoFillMode (enum 0x014280c8).
constexpr int kAutoFillUnlimited = 0;
constexpr int kAutoFillGoldMineNotEmpty = 1;

// D9c: which hits pause a capture (goldMineCaptureHitPlayerSpeedMultiplier for goldMineCaptureHitPlayerDurationInMS).
// No client code reads the two fields (the gameplay tuning's only accessor, LabsLevel_GetActiveGameplayTuning
// 0x00910760, serves the surrender UI and the towers' graphics), so the rule is inferred from the engine: its damage
// tracker (object +0x918) keeps three "damage taken" times, written by DamageTracker_RecordCombatTimes (0x008E0DC0) for
// a hit with dealt > 0 from an attacker of the other team: kind 0 any such attacker (+0x28), kind 1 an attacker that
// is player-controlled (+0x56: shapers and bot shapers) or a player's pet (GameObject_IsPlayerPet 0x00880830, NPC type
// 11) (+0x50), kind 2 a structure (+0x78); DamageTracker_HasTakenDamageWithin (0x008E0FC0, (object, int64 ms, kind))
// asks "within N ms", and nGameObject.HasTakenPlayerDamage (0x00943360) is that call with 5000 ms and kind 1. The
// tuning's int64 milliseconds (5000) is that argument, and "HitPlayer" a hit by a player [inferred]. The user's footage
// of the original agrees: "Killing workers does not influence the well at all. Only the presence of shapers does."
// (roadmap D9c, 2026-10-09). D9 had paused for any damage > 0 [assumed]; [host] wells_damage_pause_any=1 keeps it.
enum HitRule : int {
  kHitByEnemyPlayer = 0,  // D9c: kind 1 of the damage tracker (an enemy shaper or a player's pet)
  kHitByAnything = 1,     // D9: any damage > 0 (minions, workers, creeps, structures, health costs)
};
// One hit on a player's creature, as the host's damage core saw it.
struct Hit {
  float dealt = 0.f;                      // HP taken (after shields); 0 = a shield took it all
  bool attacker = false;                  // an attacker object (the snapshot's caster) exists
  bool self = false;                      // the attacker is the target (health costs, self damage)
  bool sameTeam = false;                  // the attacker's team is the target's
  bool attackerPlayerControlled = false;  // the attacker's +0x56 (a shaper, a bot's shaper)
  bool attackerPlayerPet = false;         // the attacker is a player's pet (NPC type 11)
};
// Any damage: D9's rule.
inline bool IsAnyHit(const Hit& h) { return h.dealt > 0.f; }
// The damage tracker's kind 1 ("player damage", DamageTracker_RecordCombatTimes +0x50): dealt > 0 by an attacker of
// the other team that is player-controlled or a player's pet. Workers, minions, creeps, the parasite, Bindings and the
// Guardian are none of these; a health cost has no attacker.
inline bool IsPlayerHit(const Hit& h) {
  return h.dealt > 0.f && h.attacker && !h.self && !h.sameTeam && (h.attackerPlayerControlled || h.attackerPlayerPet);
}
// Whether `h` pauses a capturer under `rule`.
inline bool PausesCapture(const Hit& h, int rule) { return rule == kHitByAnything ? IsAnyHit(h) : IsPlayerHit(h); }

// The tuning, with SM_Map_v5's values (re/scripts/match_flow.py wells). The host reads them from the server level's
// LevelGameplayTuning (Level data +0x614; offsets below) and RewardsTuning; the last block are the host's own choices.
struct Tuning {
  float captureDurations[kMaxCaptureEntries] = {16.f, 14.f, 12.f, 10.f, 8.f};  // +0x64 goldMineCaptureDurationInSeconds
  unsigned captureDurationCount = 5;
  float hitSpeedMultiplier = 0.f;   // +0x6C goldMineCaptureHitPlayerSpeedMultiplier
  double hitWindowSeconds = 5.0;    // +0x70 goldMineCaptureHitPlayerDurationInMS (int64, 5000) / 1000
  int hitRule = kHitByEnemyPlayer;  // D9c: which hits count (Shaper::lastPlayerHitAt; kHitByAnything: lastDamagedAt)
  float awardPerPlayer = 50.f;      // +0x78 goldMineCaptureAwardGoldPerPlayer
  float lockFirst = 900.f;          // +0x7C goldMineLockedDurationFirstInSeconds
  float lockAfterCapture = 240.f;   // +0x80 goldMineLockedDurationInSeconds
  float captureRate = 1.f;          // +0x84 goldMineCaptureRatePerSecond
  float decayRate = 1.f;            // +0x88 goldMineCaptureDecayRatePerSecond
  int followersOnSpawn = 0;         // +0x90 goldMineFollowerCountOnSpawn (u16)
  float autoFillPeriod = 15.f;      // +0x94 goldMineFollowerAutoFillPeriod
  int autoFillCount = 1;            // +0x98 goldMineFollowerAutoFillCount (u16)
  int autoFillMode = kAutoFillGoldMineNotEmpty;  // +0x9C goldMineFollowerAutoFillMode
  float startCapacity = -1.f;       // +0xA0 goldMineStartCapacity; < 0 = unlimited [assumed meaning]
  float minedPerMinute = 0.f;       // +0xA4 goldMinedPerMinute
  float minedPerMinutePerFollower = 1.5f;  // +0xA8 goldMinedPerMinutePerFollower
  float bountyPercentage = 0.5f;    // +0xAC goldMineBountyPercentage
  int followerSlots = 18;           // the well noun's titanNodeDef numFollowerSlots
  float captureRadius = 12.f;       // titanNodeDef captureVolume.sphereRadius (shape sphere)
  float goldInterval = 1.f;         // RewardsTuning titanGoldRewardInterval
  // The host's choices (no data):
  float transitionSeconds = 2.f;    // how long the one-shot states 2 (unlocking) and 5 (locking) last [assumed]; 0 = skip
  float workerStartDelay = 0.f;     // match second the worker timers start [assumed: at the match start]
  float sendRefreshSeconds = 1.f;   // an 8C refresh while a capture moves [assumed; the client extrapolates in between]
};

// The capture-speed table exactly as LevelGameplayTuning_BuildCaptureSpeedTable (0x008DB920) builds it after the load:
// +0xB0 base = durations[0], +0xB4 step[0] = 1.0, step[i] = base / durations[i] - (sum of the steps before), +0xC8
// count = min(n, 5); n == 0 leaves base 0. Durations after the first must be > 0.1 (the client asserts and stops).
// So step[k] is what the (k+1)-th capturer adds, and n capturers move at base / durations[n - 1] times one.
struct SpeedTable {
  float base = 0.f;
  float step[kMaxCaptureEntries] = {0.f, 0.f, 0.f, 0.f, 0.f};
  unsigned count = 0;
  bool ok = false;
};
inline SpeedTable BuildSpeedTable(const float* durations, unsigned n) {
  SpeedTable t;
  if (n > kMaxCaptureEntries) return t;  // the client asserts "Can only support 5 ... Ignoring excess." and stops
  t.count = n;
  if (n == 0 || !durations) return t;
  t.base = durations[0];
  t.step[0] = 1.f;
  float sum = 1.f;
  for (unsigned i = 1; i < n; ++i) {
    if (!(durations[i] > 0.1f)) return t;  // "entry #%d is %f. Must be greater than 0.1": the rest stays 0
    const float s = t.base / durations[i] - sum;
    t.step[i] = s;
    sum += s;
  }
  t.ok = t.base > 0.f;
  return t;
}
// The speed factor of the capturers: the sum of the first n steps, each weighted (1 for an undamaged capturer, the hit
// multiplier for a damaged one; `weights` sorted with the undamaged first) [assumed: per capturer, the table's
// per-capturer steps suggest it; with the shipped multiplier 0 a damaged capturer simply doesn't count].
// Capturers beyond the table's count add nothing (8 s is the fastest on v5).
inline float SpeedFactor(const SpeedTable& t, const float* weights, int n) {
  float f = 0.f;
  for (int k = 0; k < n && static_cast<unsigned>(k) < t.count; ++k) f += t.step[k] * (weights ? weights[k] : 1.f);
  return f;
}
// The capture time in seconds for n undamaged capturers (0 for none or a broken table).
inline float CaptureSeconds(const SpeedTable& t, float rate, int n) {
  const float f = SpeedFactor(t, nullptr, n);
  if (f <= 0.f || rate <= 0.f || t.base <= 0.f) return 0.f;
  return t.base / (rate * f);
}

struct Vec3 {
  float x = 0.f, y = 0.f, z = 0.f;
};

// One well at the level load (D1c-live's director already finds the four: marker, object, team, position).
struct WellDef {
  std::uint32_t object = 0;  // the well object's server id (mServerObjId, the bots' handle)
  int team = 0;              // the starting owner, the noun's spawnTeamId: blue 1, red 2 ("Each team initially controls
                             // two of them")
  Vec3 pos;                  // mWorldPos, the capture sphere's centre
  int displayIndex = -1;     // mHUDMineStatusDisplayIndex 0-3; -1 = the well's index [assumed: marker order]
  std::vector<Vec3> spots;   // the 18 worker-spot child markers ("Follower Spawn Point"), slot order; empty = the well
  // MAPMINES: false for a well on an island only NPCs may use (islands.h OutOfPlayersReach: MM_Map_v3's two team gold
  // mines). The rules don't change (nobody reaches its ring); MinesForTeam can leave it out for the bots.
  bool playersReach = true;
};

// Per tick, every player's creature (shapers and bot shapers; nothing else captures: "an opposing Shaper").
struct Shaper {
  std::uint32_t object = 0;
  int player = -1;               // the gameplay index
  int team = 0;                  // 1 or 2
  bool alive = false;
  Vec3 pos;
  double lastDamagedAt = -1e30;    // match seconds of the last damage > 0 it took from any source (D9's rule); very
                                   // negative = never
  double lastPlayerHitAt = -1e30;  // D9c: match seconds of the last IsPlayerHit (an enemy player's hit): what pauses a
                                   // capture by default
};

// The match second of the last hit that counts under `rule`.
inline double HitAt(const Shaper& s, int rule) {
  return rule == kHitByAnything ? s.lastDamagedAt : s.lastPlayerHitAt;
}

// cBattlegroundsUINodeData (8C), member by member (docs/data/gms_reflected_types.txt; frequency 1 = full state only).
struct NodeView {
  std::uint8_t teamControl = 0;      // +0x00 the owner
  std::uint8_t followerCount = 0;    // +0x01 workers (no client reader)
  std::uint8_t flags = 0;            // +0x28 (no client reader; 0 [unknown])
  float goldPerMinute = 0.f;         // +0x04 per player of the owner (no client reader)
  float availableResource = -1.f;    // +0x08 gold left, -1 unlimited [assumed meaning]
  float bounty = 0.f;                // +0x0C the bounty a capture pays now, per player [assumed meaning]
  float captureScore = 0.f;          // +0x10 -1..1: > 0 team 2 capturing, <= 0 team 1 (SpiritWellDisplay_UpdateCapture)
  float captureScoreSpeed = 0.f;     // +0x14 d|score|/dt; the client draws |score| + speed x t between updates
  float lockedTimer = 0.f;           // +0x18 seconds left locked; SpiritWell_IsLocked = > 0
  float lockedTimerDuration = 0.f;   // +0x1C the lock's length; the lock ring and the panel show timer / duration
  float claimedTimer = 0.f;          // +0x20 (no client reader; 0 [unknown])
  float captureDuration = 0.f;       // +0x24 seconds a full capture takes at the current speed, 0 without one [assumed]
  float goldMineCapacity = -1.f;     // +0x48 (freq 1) goldMineStartCapacity
  std::uint32_t serverObjId = 0;     // +0x2C (freq 1)
  Vec3 worldPos;                     // +0x30 (freq 1)
  std::uint8_t followerCapacity = 0; // +0x3C (freq 1)
  std::uint8_t displayIndex = 0;     // +0x3D (freq 1) the score panel's display (< 4)
  float positionX = 0.f;             // +0x40 (freq 1)
  float positionY = 0.f;             // +0x44 (freq 1)
  // +0x4C mPreBlueNodeIds / +0x5C mPreRedNodeIds (tObjID x 4, freq 1): 0 (no reader, no data)
};

// ---- what a tick asks the host to do ----------------------------------------------------------------------------------
struct StateChange {  // write +0x88E (90 member 10's high half) and mGraphicsStateStartTimeMs (+0x840) = server ms of `at`
  int well = -1;
  std::uint32_t object = 0;
  std::uint16_t from = 0, to = 0;
  double at = 0.0;
};
struct WorkerSpawn {  // create a goldMineFollowerSlotNoun creature of `team` at `pos`, then OnWorkerSpawned(well, slot, id)
  int well = -1;
  int slot = -1;
  int team = 0;
  Vec3 pos;
  // The creature's owner (object +0x48, GameObject_CreateFromNoun's third argument, replicated as sporelabsObject.
  // mOwnerID): the well's object (D9b). The client's own CombatEvents!CombatEvent_DamageAbsorb.lua announces
  // MineUnderAttack about nGameObject.GetOwnerID(worker) when an enemy damages a Follower; with owner 0 that
  // announcement had no object (ClientFeedback.cpp 1160/1164/1831, docs/ASSERTS.md). Nothing else reads a worker's
  // owner: the engine's owner paths are for NPC type 11 (player pets) only, nGameObject.IsPet needs a player-
  // controlled owner, and the worker noun has no fowUseOwnerVision (docs/MATCH_FLOW.md *D9b*).
  std::uint32_t owner = 0;
};
struct WorkerKill {  // remove the worker (a capture: "all of its Workers are destroyed"); id 0 = never confirmed
  int well = -1;
  int slot = -1;
  std::uint32_t worker = 0;
};
struct Capture {
  int well = -1;
  std::uint32_t object = 0;
  int from = 0, to = 0;          // the well object's mTeam and the node's mTeamControl become `to`
  std::uint32_t source = 0;      // a capturer's creature: the KillingBlow combat event's source (MineCaptured) [assumed]
  std::vector<int> capturers;    // the capturers' gameplay indices
  float award = 0.f;             // per player of `to`
  float bounty = 0.f;            // per player of `to`
  double at = 0.0;
};
struct TeamGold {  // grant `amount` to every player of `team` with nGoldSourceType `source`
  int team = 0;
  int amount = 0;
  int source = 0;
};
// D9c: a capture that moved in the last tick and doesn't in this one (for the host's log; resumes are captureStarted).
enum PauseReason : int {
  kPauseContested = 1,  // a living shaper of the owning team in the sphere
  kPauseHit = 2,        // every capturer hit within the window (by the tuning's hitRule)
  kPauseLeft = 3,       // no capturer left in the sphere (the score decays)
};
struct CapturePause {
  int well = -1;
  int reason = 0;
  std::vector<int> players;  // kPauseHit: the hit capturers' gameplay indices; kPauseContested: the defenders'
  double at = 0.0;
};
struct TickResult {
  std::vector<StateChange> states;
  std::vector<Capture> captures;
  std::vector<WorkerKill> kills;
  std::vector<WorkerSpawn> spawns;
  std::vector<TeamGold> gold;
  // Wells whose capture started (or resumed) moving this tick. D9 announced MineUnderAttack for each [assumed]; since
  // D9b the host does so only on request ([host] wells_capture_announce) and only while SecondsToCapture(well) is long
  // enough for the announcement to play before the well flips (docs/MATCH_FLOW.md *D9b*).
  std::vector<int> captureStarted;
  std::vector<CapturePause> capturePaused;  // D9c
  void Clear() {
    states.clear();
    captures.clear();
    kills.clear();
    spawns.clear();
    gold.clear();
    captureStarted.clear();
    capturePaused.clear();
  }
};

class Wells {
 public:
  // `now` = match seconds (the HUD's mGameTime clock; the locks count from 0).
  void Init(const Tuning& tuning, const std::vector<WellDef>& defs, double now = 0.0) {
    tuning_ = tuning;
    table_ = BuildSpeedTable(tuning.captureDurations, std::min(tuning.captureDurationCount, kMaxCaptureEntries));
    wells_.clear();
    const size_t n = std::min(defs.size(), static_cast<size_t>(kMaxWells));
    for (size_t i = 0; i < n; ++i) {
      Well w;
      w.def = defs[i];
      if (w.def.displayIndex < 0) w.def.displayIndex = static_cast<int>(i);
      w.owner = defs[i].team;
      w.lockedUntil = tuning.lockFirst;
      w.lockDuration = tuning.lockFirst;
      const int slots = std::max(0, tuning.followerSlots);
      w.used.assign(static_cast<size_t>(slots), 0);
      w.worker.assign(static_cast<size_t>(slots), 0u);
      w.available = tuning.startCapacity;
      w.nextSpawnAt = static_cast<double>(tuning.workerStartDelay) + tuning.autoFillPeriod;
      w.spawnOnStart = tuning.followersOnSpawn > 0;
      w.state = DesiredState(w, now);
      w.stateSince = now;
      wells_.push_back(w);
    }
    accrual_[0] = accrual_[1] = 0.0;
    now_ = now;
    nextPayAt_ = now + std::max(0.001, static_cast<double>(tuning.goldInterval));
    started_ = false;
  }

  const Tuning& GetTuning() const { return tuning_; }
  const SpeedTable& Table() const { return table_; }
  int Count() const { return static_cast<int>(wells_.size()); }
  double Now() const { return now_; }

  // One step to match second `now` with every player's creature. Order: the gold mined since the last tick (with the
  // owners and workers as they were), the captures, the workers, the gold payout, the titan-node states.
  void Tick(double now, const std::vector<Shaper>& shapers, TickResult* out) {
    if (!out) return;
    const double last = started_ ? now_ : now;
    const double dt = std::max(0.0, now - last);
    started_ = true;
    now_ = now;
    for (int i = 0; i < Count(); ++i) Mine(i, dt);
    for (int i = 0; i < Count(); ++i) StepCapture(i, last, now, shapers, out);
    for (int i = 0; i < Count(); ++i) StepWorkers(i, now, out);
    Pay(now, out);
    for (int i = 0; i < Count(); ++i) {
      Well& w = wells_[static_cast<size_t>(i)];
      const std::uint16_t s = DesiredState(w, now);
      if (s != w.state) {
        StateChange c;
        c.well = i;
        c.object = w.def.object;
        c.from = w.state;
        c.to = s;
        c.at = now;
        out->states.push_back(c);
        w.state = s;
        w.stateSince = now;
      }
    }
  }

  // ---- workers (the host creates and reports them) ----
  void OnWorkerSpawned(int well, int slot, std::uint32_t worker) {
    Well* w = At(well);
    if (!w || slot < 0 || slot >= static_cast<int>(w->used.size()) || !w->used[static_cast<size_t>(slot)]) return;
    w->worker[static_cast<size_t>(slot)] = worker;
  }
  // The host couldn't create it: the slot is free again (and the timer runs).
  void OnWorkerSpawnFailed(int well, int slot) {
    Well* w = At(well);
    if (!w || slot < 0 || slot >= static_cast<int>(w->used.size())) return;
    Free(*w, static_cast<size_t>(slot));
  }
  // A worker died: from the host's death processing (its death ability is ability_Follower_Death). The scripts' own hook,
  // nBattlegrounds.OnTitanFollowerDeath (modifier_TitanFollower_Passive's Deactivate; unbound in the client), never runs:
  // Titan_Node_Follower's AIDefinition has no passiveAbility. A death frees the slot; a full well's next worker comes one
  // period after it [assumed]. False for an unknown id.
  bool OnWorkerDied(std::uint32_t worker) {
    if (!worker) return false;
    for (Well& w : wells_)
      for (size_t s = 0; s < w.worker.size(); ++s)
        if (w.used[s] && w.worker[s] == worker) {
          Free(w, s);
          return true;
        }
    return false;
  }

  // ---- queries (the bots' nBattlegrounds getters and the host's views) ----
  int IndexOf(std::uint32_t object) const {
    for (int i = 0; i < Count(); ++i)
      if (object && wells_[static_cast<size_t>(i)].def.object == object) return i;
    return -1;
  }
  int Owner(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].owner : 0; }
  // Locked (can't be captured): SpiritWell_IsLocked's mLockedTimer > 0.
  bool IsLocked(int i) const { return Valid(i) && now_ < wells_[static_cast<size_t>(i)].lockedUntil; }
  int Workers(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].count : 0; }
  double Score(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].score : 0.0; }
  // Seconds until a capture that moved in the last tick completes at its current speed; -1 while none moves (paused,
  // contested, decaying or no capturers). A capture that resumes after a pause near its end has little left: the
  // host's own announcement then arrived after the flip (q3_run3, q3b_run1: MiniMapRenderer.cpp 2941).
  double SecondsToCapture(int i) const {
    if (!Valid(i)) return -1.0;
    const Well& w = wells_[static_cast<size_t>(i)];
    if (!w.moving || !(w.speed > 0.f)) return -1.0;
    return std::max(0.0, (1.0 - w.score) / static_cast<double>(w.speed));
  }
  // WELLDEF: the living shapers of the capturing team in the well's ring in the last tick, whatever the capture did
  // (moving, contested by a defender, paused by hits); 0 while the well is locked or unowned.
  int Capturers(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].capturers : 0; }
  std::uint16_t State(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].state : kStateNone; }
  double StateSince(int i) const { return Valid(i) ? wells_[static_cast<size_t>(i)].stateSince : 0.0; }
  std::uint32_t WorkerAt(int i, int slot) const {
    if (!Valid(i)) return 0;
    const Well& w = wells_[static_cast<size_t>(i)];
    return slot >= 0 && slot < static_cast<int>(w.worker.size()) ? w.worker[static_cast<size_t>(slot)] : 0;
  }
  // nBattlegrounds.GetMinesForTeam(team [, onlyUnlocked]): the wells `team` owns, as object ids, in well order;
  // MAPMINES: with `reachableOnly` not the wells out of the players' reach (WellDef::playersReach false).
  std::vector<std::uint32_t> MinesForTeam(int team, bool onlyUnlocked = false, bool reachableOnly = false) const {
    std::vector<std::uint32_t> out;
    for (int i = 0; i < Count(); ++i)
      if (wells_[static_cast<size_t>(i)].owner == team && !(onlyUnlocked && IsLocked(i)) &&
          !(reachableOnly && !PlayersReach(i)))
        out.push_back(wells_[static_cast<size_t>(i)].def.object);
    return out;
  }
  // MAPMINES: whether a player can reach the well (false for an unknown index).
  bool PlayersReach(int i) const { return Valid(i) && wells_[static_cast<size_t>(i)].def.playersReach; }
  // nBattlegrounds.GetFollowersForTitan(well): the workers at the well (0 for an unknown id).
  int FollowersForTitan(std::uint32_t object) const { return Workers(IndexOf(object)); }
  // nBattlegrounds.IsTitanNodeLocked(well): false for an unknown id (the client stub's answer).
  bool IsTitanNodeLocked(std::uint32_t object) const { return IsLocked(IndexOf(object)); }

  // The well's gold per minute per player of its owner: goldMinedPerMinute + perFollower x workers; 0 once a limited
  // mine is empty.
  float GoldPerMinute(int i) const {
    if (!Valid(i)) return 0.f;
    const Well& w = wells_[static_cast<size_t>(i)];
    if (Limited() && w.available <= 0.0) return 0.f;
    return tuning_.minedPerMinute + tuning_.minedPerMinutePerFollower * static_cast<float>(w.count);
  }
  // mBlueGpm (team 1, UI data +0x06) / mRedGpm (team 2, +0x04): the sum over the team's wells, rounded (u16)
  // [assumed meaning: per player, the wells only; R10 §3].
  std::uint16_t TeamGpm(int team) const {
    double sum = 0.0;
    for (int i = 0; i < Count(); ++i)
      if (wells_[static_cast<size_t>(i)].owner == team) sum += GoldPerMinute(i);
    const long v = std::lround(sum);
    return static_cast<std::uint16_t>(std::clamp(v, 0L, 65535L));
  }

  NodeView Node(int i) const {
    NodeView v;
    if (!Valid(i)) return v;
    const Well& w = wells_[static_cast<size_t>(i)];
    v.teamControl = static_cast<std::uint8_t>(w.owner);
    v.followerCount = static_cast<std::uint8_t>(std::clamp(w.count, 0, 255));
    v.goldPerMinute = GoldPerMinute(i);
    v.availableResource = Limited() ? static_cast<float>(std::max(0.0, w.available)) : -1.f;
    v.bounty = static_cast<float>(w.bounty);
    const float s = w.score > 0.0 ? static_cast<float>(w.score) : 0.f;
    v.captureScore = s > 0.f ? (CaptureTeam(w) == 2 ? s : -s) : 0.f;
    v.captureScoreSpeed = w.speed;
    v.lockedTimer = static_cast<float>(std::max(0.0, w.lockedUntil - now_));
    v.lockedTimerDuration = w.lockDuration;
    v.captureDuration = w.captureDuration;
    v.goldMineCapacity = tuning_.startCapacity;
    v.serverObjId = w.def.object;
    v.worldPos = w.def.pos;
    v.followerCapacity = static_cast<std::uint8_t>(std::clamp(tuning_.followerSlots, 0, 255));
    v.displayIndex = static_cast<std::uint8_t>(std::clamp(w.def.displayIndex, 0, 255));
    v.positionX = w.def.pos.x;
    v.positionY = w.def.pos.y;
    return v;
  }

  // The 8C mask to send now (bit i = node i): every node the first time; then a node whose owner, workers, gold per
  // minute, titan-node state, capture speed or whole lock second changed, and a moving capture once every
  // sendRefreshSeconds. The client extrapolates the capture ring from the speed, so the score itself needs no per-tick
  // send [assumed cadence]. Marks what it returns as sent. Call it after Tick(now) (the lock seconds are the tick's);
  // the first call returns every node, which the host may take as the full 8C 0F after the first 8B (D1c-live).
  std::uint8_t TakeSendMask(double now) {
    std::uint8_t mask = 0;
    for (int i = 0; i < Count(); ++i) {
      Well& w = wells_[static_cast<size_t>(i)];
      const Sent cur = Snapshot(i);
      const bool refresh = w.score > 0.0 && now - w.sentAt >= tuning_.sendRefreshSeconds - 1e-9;
      if (!w.sent || !(cur == w.last) || refresh) {
        mask = static_cast<std::uint8_t>(mask | (1u << i));
        w.last = cur;
        w.sentAt = now;
        w.sent = true;
      }
    }
    return mask;
  }

 private:
  struct Sent {
    int owner = 0, workers = 0, gpm100 = 0, lockSecond = 0;
    std::uint16_t state = 0;
    float speed = 0.f;
    bool operator==(const Sent& o) const {
      return owner == o.owner && workers == o.workers && gpm100 == o.gpm100 && lockSecond == o.lockSecond &&
             state == o.state && speed == o.speed;
    }
  };
  struct Well {
    WellDef def;
    int owner = 0;
    double score = 0.0;           // |mCaptureScore|, toward CaptureTeam() (double: no drift at 30 Hz)
    float speed = 0.f;            // d|score|/dt in the last tick
    float captureDuration = 0.f;
    bool moving = false;          // the capture moved forward in the last tick
    int capturers = 0;            // WELLDEF: living shapers of the capturing team in the ring in the last tick
    double lockedUntil = 0.0;     // match seconds
    float lockDuration = 0.f;
    double capturedAt = -1e30;
    std::uint16_t state = 0;
    double stateSince = 0.0;
    std::vector<char> used;       // per worker slot
    std::vector<std::uint32_t> worker;
    int count = 0;
    double nextSpawnAt = -1.0;    // < 0: the timer is off (full or empty)
    bool spawnOnStart = false;
    double available = -1.0;      // gold left per player in a limited mine
    double bounty = 0.0;          // per player
    bool sent = false;
    Sent last;
    double sentAt = 0.0;
  };

  bool Valid(int i) const { return i >= 0 && i < Count(); }
  Well* At(int i) { return Valid(i) ? &wells_[static_cast<size_t>(i)] : nullptr; }
  bool Limited() const { return tuning_.startCapacity >= 0.f; }
  static int CaptureTeam(const Well& w) { return w.owner == 1 ? 2 : (w.owner == 2 ? 1 : 0); }

  std::uint16_t DesiredState(const Well& w, double now) const {
    const double tr = std::max(0.f, tuning_.transitionSeconds);
    if (now < w.lockedUntil) return now < w.capturedAt + tr ? kLocking : kLocked;
    if (w.score > 0.0) return kBeingCaptured;
    if (now < w.lockedUntil + tr) return kUnlocking;
    return kUnlocked;
  }

  Sent Snapshot(int i) const {
    const Well& w = wells_[static_cast<size_t>(i)];
    Sent s;
    s.owner = w.owner;
    s.workers = w.count;
    s.gpm100 = static_cast<int>(std::lround(GoldPerMinute(i) * 100.f));
    s.lockSecond = static_cast<int>(std::ceil(std::max(0.0, w.lockedUntil - now_) - 1e-6));
    s.state = w.state;
    s.speed = w.speed;
    return s;
  }

  // The gold a well mined for its owner over dt (per player), its bounty and a limited mine's rest.
  void Mine(int i, double dt) {
    Well& w = wells_[static_cast<size_t>(i)];
    if (dt <= 0.0 || (w.owner != 1 && w.owner != 2)) return;
    double mined = static_cast<double>(GoldPerMinute(i)) * dt / 60.0;
    if (Limited()) {
      mined = std::min(mined, std::max(0.0, w.available));
      w.available = std::max(0.0, w.available - mined);
    }
    if (mined <= 0.0) return;
    accrual_[w.owner - 1] += mined;
    w.bounty += static_cast<double>(tuning_.bountyPercentage) * mined;  // [assumed: a share of what it mined since taken]
  }

  static float Dist(const Vec3& a, const Vec3& b) {
    const float dx = a.x - b.x, dy = a.y - b.y, dz = a.z - b.z;
    return std::sqrt(dx * dx + dy * dy + dz * dz);
  }

  // A capturer hit within the window by a hit the rule counts (D9c: an enemy player's).
  bool IsHit(const Shaper& s, double now) const { return now - HitAt(s, tuning_.hitRule) < tuning_.hitWindowSeconds; }

  void Paused(int i, int reason, std::vector<int> players, double now, TickResult* out) {
    CapturePause p;
    p.well = i;
    p.reason = reason;
    std::sort(players.begin(), players.end());
    p.players = std::move(players);
    p.at = now;
    out->capturePaused.push_back(std::move(p));
  }

  void StepCapture(int i, double last, double now, const std::vector<Shaper>& shapers, TickResult* out) {
    Well& w = wells_[static_cast<size_t>(i)];
    const bool wasMoving = w.moving;
    w.moving = false;
    w.capturers = 0;
    w.captureDuration = 0.f;
    if (now < w.lockedUntil || (w.owner != 1 && w.owner != 2)) {  // locked: not capturable (the score is 0 here)
      w.speed = 0.f;
      return;
    }
    const double dtc = std::max(0.0, now - std::max(last, w.lockedUntil));  // only the unlocked part of the step
    const int capTeam = CaptureTeam(w);
    std::vector<const Shaper*> caps;
    std::vector<int> defenders;
    for (const Shaper& s : shapers) {
      if (!s.alive || (s.team != 1 && s.team != 2)) continue;
      if (Dist(s.pos, w.def.pos) > tuning_.captureRadius) continue;
      if (s.team == capTeam)
        caps.push_back(&s);
      else if (s.team == w.owner)
        defenders.push_back(s.player);
    }
    w.capturers = static_cast<int>(caps.size());
    const float base = table_.base > 0.f ? table_.base : 1.f;
    if (caps.empty()) {  // decay toward 0 [assumed scale: decayRate / base per second, the capture's own unit]
      const float d = tuning_.decayRate / base;
      w.score = std::max(0.0, w.score - static_cast<double>(d) * dtc);
      w.speed = w.score > 0.0 ? -d : 0.f;
      if (wasMoving) Paused(i, kPauseLeft, {}, now, out);
      return;
    }
    if (!defenders.empty()) {  // contested: the capture pauses, no decay
      w.speed = 0.f;
      if (wasMoving) Paused(i, kPauseContested, defenders, now, out);
      return;
    }
    // unhit capturers take the first table entries, hit ones the rest at the hit multiplier; which hits count is the
    // tuning's hitRule (D9c: an enemy player's)
    std::vector<float> weights;
    std::vector<int> hit;
    weights.reserve(caps.size());
    for (const Shaper* s : caps)
      if (!IsHit(*s, now)) weights.push_back(1.f);
    for (const Shaper* s : caps)
      if (IsHit(*s, now)) {
        weights.push_back(tuning_.hitSpeedMultiplier);
        hit.push_back(s->player);
      }
    const float factor = SpeedFactor(table_, weights.data(), static_cast<int>(weights.size()));
    const float speed = tuning_.captureRate * factor / base;
    w.speed = speed > 0.f ? speed : 0.f;
    if (w.speed <= 0.f) {  // every capturer hit: paused
      if (wasMoving) Paused(i, kPauseHit, hit, now, out);
      return;
    }
    w.captureDuration = 1.f / w.speed;
    w.moving = true;
    if (!wasMoving) out->captureStarted.push_back(i);
    w.score += static_cast<double>(w.speed) * dtc;
    if (w.score < 1.0 - 1e-6) return;
    CaptureNow(i, now, caps, out);
  }

  void CaptureNow(int i, double now, const std::vector<const Shaper*>& caps, TickResult* out) {
    Well& w = wells_[static_cast<size_t>(i)];
    Capture c;
    c.well = i;
    c.object = w.def.object;
    c.from = w.owner;
    c.to = CaptureTeam(w);
    c.at = now;
    const Shaper* src = nullptr;  // the source of the MineCaptured event: the lowest gameplay index [assumed]
    for (const Shaper* s : caps) {
      c.capturers.push_back(s->player);
      if (!src || (s->player >= 0 && (src->player < 0 || s->player < src->player))) src = s;
    }
    std::sort(c.capturers.begin(), c.capturers.end());
    c.source = src ? src->object : 0;
    c.award = tuning_.awardPerPlayer;
    c.bounty = static_cast<float>(w.bounty);
    // every worker dies (tutorial SpiritWells_Capturing_5)
    for (size_t s = 0; s < w.used.size(); ++s)
      if (w.used[s]) {
        WorkerKill k;
        k.well = i;
        k.slot = static_cast<int>(s);
        k.worker = w.worker[s];
        out->kills.push_back(k);
        w.used[s] = 0;
        w.worker[s] = 0;
      }
    w.count = 0;
    // the award and the bounty to every player of the capturing team [assumed recipients]
    const int award = static_cast<int>(std::lround(tuning_.awardPerPlayer));
    if (award > 0) out->gold.push_back(TeamGold{c.to, award, kGoldSourceObjective});
    const int bounty = static_cast<int>(std::lround(w.bounty));
    if (bounty > 0) out->gold.push_back(TeamGold{c.to, bounty, kGoldSourceWellBounty});
    out->captures.push_back(c);
    // the new owner, locked again, its own workers from now on ("Friendly Workers will then begin to gather there")
    w.owner = c.to;
    w.score = 0.0;
    w.speed = 0.f;
    w.moving = false;
    w.captureDuration = 0.f;
    w.lockedUntil = now + tuning_.lockAfterCapture;
    w.lockDuration = tuning_.lockAfterCapture;
    w.capturedAt = now;
    w.bounty = 0.0;
    w.nextSpawnAt = now + tuning_.autoFillPeriod;
    w.spawnOnStart = tuning_.followersOnSpawn > 0;  // [assumed: a capture spawns the mine's starting workers again]
  }

  bool CanFill(const Well& w) const {
    return tuning_.autoFillMode == kAutoFillUnlimited || !Limited() || w.available > 0.0;
  }

  int FreeSlot(const Well& w) const {
    for (size_t s = 0; s < w.used.size(); ++s)
      if (!w.used[s]) return static_cast<int>(s);
    return -1;
  }

  void Spawn(int i, TickResult* out) {
    Well& w = wells_[static_cast<size_t>(i)];
    const int slot = FreeSlot(w);
    if (slot < 0) return;
    w.used[static_cast<size_t>(slot)] = 1;
    w.worker[static_cast<size_t>(slot)] = 0;
    ++w.count;
    WorkerSpawn sp;
    sp.well = i;
    sp.slot = slot;
    sp.team = w.owner;
    sp.owner = w.def.object;  // the worker belongs to its well (D9b), whichever team owns the well
    sp.pos = static_cast<size_t>(slot) < w.def.spots.size() ? w.def.spots[static_cast<size_t>(slot)] : w.def.pos;
    out->spawns.push_back(sp);
  }

  void Free(Well& w, size_t s) {
    if (s >= w.used.size() || !w.used[s]) return;
    w.used[s] = 0;
    w.worker[s] = 0;
    --w.count;
    if (w.nextSpawnAt < 0.0 && CanFill(w)) w.nextSpawnAt = now_ + tuning_.autoFillPeriod;
  }

  void StepWorkers(int i, double now, TickResult* out) {
    Well& w = wells_[static_cast<size_t>(i)];
    if (w.owner != 1 && w.owner != 2) return;
    if (now < tuning_.workerStartDelay) return;
    const int slots = static_cast<int>(w.used.size());
    if (w.spawnOnStart) {
      w.spawnOnStart = false;
      for (int k = 0; k < tuning_.followersOnSpawn && w.count < slots; ++k) Spawn(i, out);
    }
    if (!CanFill(w) || w.count >= slots || tuning_.autoFillPeriod <= 0.f) {
      w.nextSpawnAt = -1.0;
      return;
    }
    if (w.nextSpawnAt < 0.0) w.nextSpawnAt = now + tuning_.autoFillPeriod;
    while (now >= w.nextSpawnAt - 1e-9 && w.count < slots) {
      for (int k = 0; k < std::max(1, tuning_.autoFillCount) && w.count < slots; ++k) Spawn(i, out);
      w.nextSpawnAt += tuning_.autoFillPeriod;
    }
    if (w.count >= slots) w.nextSpawnAt = -1.0;
  }

  // Every titanGoldRewardInterval: the whole gold each team's players have earned from their wells, the fraction kept.
  void Pay(double now, TickResult* out) {
    const double interval = std::max(0.001, static_cast<double>(tuning_.goldInterval));
    if (now < nextPayAt_ - 1e-9) return;
    while (nextPayAt_ <= now + 1e-9) nextPayAt_ += interval;
    for (int t = 0; t < 2; ++t) {
      const double whole = std::floor(accrual_[t] + 1e-6);
      if (whole < 1.0) continue;
      accrual_[t] = std::max(0.0, accrual_[t] - whole);
      out->gold.push_back(TeamGold{t + 1, static_cast<int>(whole), kGoldSourceWellPassive});
    }
  }

  Tuning tuning_;
  SpeedTable table_;
  std::vector<Well> wells_;
  double accrual_[2] = {0.0, 0.0};  // per player, team 1 and team 2
  double now_ = 0.0;
  double nextPayAt_ = 1.0;
  bool started_ = false;
};

}  // namespace gms::wells
