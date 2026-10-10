// The match lifecycle on the server simulator (roadmap D1c-live; docs/MATCH_FLOW.md §1, §3, §4 and *Game-mode
// handlers, energy lines and spirit wells (R10)*). What the stripped game mode did, run by the host:
//   - before the level (OnSimulatorCreated): the game-mode Lua functions over the client's stubs, registered with the
//     engine's GearsLua_RegisterNamespace (0x00873170) on the shared lua_State: nBattlegrounds.GetGuardianOnTeam (the
//     Guardians spawned below), OnTitanTowerDestroyed (-> the host's structure handling and the game end),
//     SetEnergyLineState (mNubRespawnTime/mNubTeams of the UI data, R10 §2), GetNubRespawnTuning (the level's 480) and,
//     since D5, AddBaseStructuresToAggroList (the other team's living nubs and Guardian onto a lane minion's aggro list
//     at its lane's end, through agents::AddBaseStructures),
//     plus the director's nDirector overrides (director.h). On any other simulator they behave like the stubs;
//   - at the level load (OnLevelStarted): the director's level data (director.h), both Guardians at their base titan
//     nodes' TitanSpawnPoints (GameObject_CreateFromNoun 0x008E1EC0), the UI data's tower table (mTowers,
//     mNumTowersPerLane), the nubs (D5's base structures) and, since D9, the spirit wells (wells.h: the node list,
//     the rules of gms/core/wells);
//   - at the first EnterGame (OnEnterGame, or [host] match_start_s without a player): GameSimulator_StartGame
//     (0x008F2EC0) with the game's descriptors, the MatchStart announcer, the director's schedule;
//   - every tick (Tick, after the command intake and before the agent step): the director, the wells (D9: wells.h);
//     once a second the NPC levels (NPC_UpdateLevelForMatchTime 0x00881170 with force 0), mGameTime; the Guardian HP check
//     and the structures OnTitanTowerDestroyed reported (a Binding: mTowerRespawnTime all ones and
//     nGameModeHandlers[OnTowerDestroyed] through a Lua chunk; a Guardian: the end);
//   - the end (a Guardian died, or the D8-live fixture end_after_s): GameSimulator_EndGame (0x008EF160) with reason 1 or
//     3, the director stopped, mGameTime frozen at the duration, the playerFinish lines and finishGame for dgserver
//     (C1b), and the end handed to the GMS side (TakeEnd: the post-game, gms/core/postgame through gms_host.cpp);
//     since D8b also a passed surrender vote (EndBySurrender: reason 2 or 4, finishGame reason "surrender").
// Everything runs on the engine thread with the server simulator bound; every engine call under SEH.
// Settings ([host] keys): match (1), match_guardians (1), match_levels (1), match_wells (1), match_lua (1),
// match_end_hp (1), match_descriptors (0x50C; createGame's descriptors replace it), match_game_id (0), match_start_s
// (0 = at the first EnterGame), match_log_limit (300), the D8-live fixture end_after_s (0 = off), end_reason (3),
// end_guardian (0), and the director's (director.h).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "director.h"

namespace match {

struct Options {
  bool enabled = true;
  bool guardians = true;
  bool levels = true;
  bool wells = true;
  bool lua = true;
  bool endOnHp = true;
  int endHpGraceMs = 2000;            // D1f-live: the HP check waits this long for the Guardian's own report (T13)
  bool minionRangedPursue = true;     // MINRANGE2: ability_Minion_Ranged gets interfaceType Targeted + shouldPursue at the level load
  bool guardianShield = true;         // D14: modifier_Tower_Immunity on a Guardian while a core of its team lives
  std::uint32_t descriptors = 0x50C;  // the practice game's GameFlags (BLAZE.md); C1b passes the real ones
  unsigned long long gameId = 0;
  double startAfterS = 0.0;           // > 0: start the match this long after the level load if nobody entered
  int logLimit = 300;
  // D8-live test fixture: end the match endAfterS s of match time after its start as if a Guardian had died: reason 1
  // (the Spirit Guardian died: team 1 wins) or 3 (the Mortal Guardian: team 2 wins); endGuardian != 0 replaces the dead
  // Guardian's id in C1 01 (a stand-in object for the death camera).
  double endAfterS = 0.0;
  int endReason = 3;
  std::uint32_t endGuardian = 0;
  // D8-live test fixture (T5): "K,D,A,creeps,gold,level" written into every server player at the end ("" = off)
  std::string endStats;
  director::Options director;
};

// Reads nothing from the game; logs the settings and checks the prologues of the functions it calls.
bool Configure(const Options& o);
bool Enabled();

// After GameSimulator_Init, before the level (server simulator bound): the Lua functions.
void OnSimulatorCreated(void* sim);
// After GameSimulator_StartLevel, before the first tick (bound).
void OnLevelStarted(void* sim);
// The session sent the first EnterGame: the match starts in the next Tick.
void OnEnterGame();
// Every server tick while the simulator runs (bound), after the command intake and before the agent step.
void Tick(void* sim);

bool Started();
bool Ended();
// Match seconds (GameSimulator_GetMatchElapsedMs / 1000), 0 before the start.
double MatchSeconds();

// The end, once, for the GMS side.
struct End {
  std::uint32_t reason = 0;      // GameSimulator_EndGame's: 1 Mortal wins, 3 Spirit wins (D8b: 2/4 by surrender)
  std::uint32_t guardian = 0;    // the dead Guardian's server id
  std::uint32_t descriptors = 0;
  std::uint32_t killer = 0;      // OnTitanTowerDestroyed's killer (0 from the HP check)
  int winner = 0xFF;
  unsigned long long durationMs = 0;
};
bool TakeEnd(End* out);
// D8b: a team's surrender vote passed (gms/core/surrender's TakeEnd, from ServerMatch): the end as after a Guardian's
// death, with `reason` (2: team 2 surrendered, Mortal wins; 4: team 1 surrendered, Spirit wins), the surrendering
// team's Guardian in C1 01 (the end cinematics show the surrender announcer on it) and finishGame's reason
// "surrender". The caller has the server simulator bound. False if the match isn't running (or wrong arguments).
bool EndBySurrender(int team, std::uint32_t reason, int yesPercent);

// C1b: the Blaze game id from createGame (GameSimulator_StartGame, C1 01), before the match starts.
void SetGameId(unsigned long long gameId);
// D8-live: createGame's descriptors (the game's GameFlags) for GameSimulator_StartGame, C1 01 and the rewards.
void SetDescriptors(std::uint32_t descriptors);
// The game's descriptors as they stand (nGameDescriptors: 0x8 IsSoloPractice, 0x100 IsFTUE, 0x400 BotsFillTeams, ...).
std::uint32_t Descriptors();

void FormatStats(char* buf, std::size_t size);

}  // namespace match
