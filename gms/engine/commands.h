// Command intake on the server (roadmap D7-live; ADR-2 decision 5, inbound; docs/SERVER_MODE.md *Command intake on the
// server (D7-static)* and *Command intake on the server (D7-live)*). What Dawngate.exe lacks between the GMS and the
// server's command queue, written here:
//   - the connection -> player map: ServerMatch passes the connection's gameplay index (the binary's dispatcher passes
//     0xFF, D7-static);
//   - the wire -> server command conversion (the stripped consumer of sim +0x19E7C): ActionCommandMsgs types 1/9 move
//     and attack-move, 2/8/11 stop moving, stop, hold, 3 ability (slot or GUID) -> ServerCommand_Init (0x00922850) and
//     ServerCommandQueue_QueueCommand (0x00923E60, tickNow 1); 7 ping -> MiniMapPing to the pinger's team; 4, 5, 6, 10
//     (interact, dance, taunt, voice line) are logged only (their conversion is open);
//   - AbilityRankUp -> LabsPlayer_RankUpAbility (0x009149C0) on the server player, then LabsPlayerFowUpdate (ability
//     points, ranks) to the player, the only way its client learns the new rank;
//   - every tick ServerCommandQueue_TickPlayer (0x00923450) for each player with a creature (its per-frame caller is
//     stripped: a queued move never runs without it) and the creature's behavior tree (AgentBehaviorTree_Tick
//     0x008A9060, for "walk up, then cast");
//   - the sixth seam: GameSimulator_SendToPlayerListStub (0x008F9090) is replaced, so what the simulator sends through
//     it (ActionCommandResponse kinds 1/2/4, PauseMsgs, SetObjectGfxState, ObjectJump, ComponentMessage,
//     ClientFeedbackEvent) reaches the players in its mask; kind 2 gets the sequence its builder
//     (GameSimulator_SendActionCommandFailed 0x008F0110, detoured to remember it) leaves out;
//   - a piece of D2g: after each tick, a LocomotionDataUpdate (quantized position) for every player's creature whose
//     server locomotion tick flagged a position to send (locomotion +0x8E, set by Locomotion_TickMovers for
//     player-controlled objects), to the players that know it;
//   - D7b: ItemTransaction -> GameSimulator_OnItemTransaction's switch (0x008F1390) on the server player (purchase,
//     sell, use, consume, swap: the shop check, LabsPlayer_CheckPurchase, LabsPlayer_PurchaseItem, ...), then the 8-byte
//     answer (the binary's answer function is the emptied 0x009A3F00): a success to the player's team, whose clients
//     repeat it on their copy of the player, a refusal to the player alone. It goes out before the tick's replication
//     (an "early" answer), so a client repeats the purchase before the AA delta brings the same inventory and gold;
//   - D7b: dance (type 5) -> the shaper's PlayerClass.emoteAbility as an ability command by GUID, plus the PlayerCommDance
//     voice; taunt (6) -> the PlayerCommTaunt voice; voice line (10) -> its PlayerComm voice: a ClientFeedbackEvent
//     through GameSimulator_SendClientFeedbackEvent (0x008F71D0) to the team (callouts) or everyone. Interact (4) is
//     unreachable in build 854's data (no noun has a cInteractableDef) and only logged.
// Everything runs on the engine thread with the server simulator bound; every engine call is under SEH.
// Settings ([host] keys): commands (1), shaper_bt (1), locomotion_updates (1), command_log_limit (400),
// grant_ability_points (0: points given to each player whose creature is deployed and who has none, for tests),
// item_transactions (1), emotes (1: dance, taunt and voice lines), grant_xp (0; D10: XP given once to each human
// player at the first tick with a creature, through LabsPlayer_GrantXPIfValid with the Cheat source, for tests: 2200
// = level 6, R can be ranked), move_takeover (1; D7c: a move takes over an engaged attack-move at once, through a patch
// of TickPlayer's queued-move step at 0x0092396D), queue_trace (1) and queue_log_limit (600) (D7c: "queue:" lines).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "core/common/bytes.h"

namespace cmds {

struct Options {
  bool behaviorTree = true;       // tick each player creature's behavior tree
  bool locomotionUpdates = true;  // send LocomotionDataUpdate for moving player creatures (off when D2g's delta pass runs)
  bool fowUpdates = true;         // send LabsPlayerFowUpdate after a rank-up (off when D2g's delta pass runs)
  int logLimit = 400;             // command and answer log lines before only every 50th is logged
  int grantAbilityPoints = 0;     // test aid: ability points for a player with a creature, no points and no ranks
  int grantXp = 0;                // D10 test aid: XP granted once to each human player with a creature (levels, points)
  bool itemTransactions = true;   // D7b: carry out ItemTransaction on the server player and answer it
  bool itemEffectCheck = true;    // D15: a purchase, sale, consume or swap that changed nothing is answered success 0
  bool emotes = true;             // D7b: dance, taunt and voice lines
  bool rankLog = true;            // D11: log every player's level, points and rank changes (humans and bots)
  // D7c: a plain move that meets an attack-move engaged on a target runs at once, as a second move does (the binary
  // defers the first one until the walk-up and the attack are over; core/commands/queue.h); false = the binary's rule
  bool moveTakeover = true;
  // Sakari's W (commands.cpp kGroundedAbilities): the cast's target position gets the terrain's height instead of z 0.00
  bool groundCastZ = true;
  bool queueTrace = true;         // D7c: a "queue:" line whenever a human player's command queue changes
  int queueLogLimit = 600;        // D7c: queue trace lines before the trace stops (counted on)
};

// A message for the players in `players` (bit i = gameplay index i; since B15b the spectators' bits 10-14 of the
// simulator's own sends are kept, the item transaction answers go to the team's players only).
struct Answer {
  std::uint16_t players = 0;
  gms::Bytes msg;  // wire byte first
  std::string what;
  bool early = false;  // D7b: send before the tick's replication step (the item transaction answers)
};

// Checks the prologues, replaces the send stub and detours GameSimulator_SendActionCommandFailed. Once, on the engine
// thread (GmsHostStart). False (logged) if a prologue differs; then nothing is installed and commands are only logged.
bool Install(const Options& options);
bool Installed();

// A client message from the player with gameplay index `index` (ActionCommandMsgs, AbilityRankUp, ItemTransaction,
// SurrenderMsgs, PauseMsgs). `source` names where it came from in the log ("client" or "injected"). True if it was
// one of these.
bool OnClientMessage(void* sim, unsigned index, std::uint8_t wire, const std::uint8_t* body, std::size_t n,
                     const char* source);

// Every server tick after the time step, before the Lua scheduler: TickPlayer and the behavior tree for every player
// with a creature.
void Tick(void* sim);

// Every server tick after the simulation: the locomotion updates; the movement log; since D11 one "ranks:" line per
// player (humans and bots) whose level, ability points or ranks changed ([host] rank_log, 1).
void AfterTick(void* sim);

// What was produced since the last call (stub sends, ping relays, FoW and locomotion updates, item answers), oldest
// first. TakeEarlyAnswers takes only the early ones (ServerMatch::Flush sends them before the replication step).
void TakeAnswers(std::vector<Answer>* out);
void TakeEarlyAnswers(std::vector<Answer>* out);

// One line for the stats: commands by type, queued, ticks, answers by kind.
void FormatStats(char* buf, std::size_t size);

// D11: a ClientFeedbackEvent with the def `defName` (an asset name such as "PlayerRespawned.ClientFeedbackDef") about
// `object`, queued on the outgoing vector for the players of the teams in `teamBits` (bit 1 << team) as
// nClientFeedback.SendEventToPlayersOnTeam does; the replication's drain sends it. Returns the player mask, 0 on a
// failure (no asset, an exception). The server simulator must be bound. D11b: `target` is the event's targetId (the
// sound's ~targetnounname~, an object every recipient knows), `excludePlayers` gameplay-index bits left out of the mask.
unsigned SendFeedback(void* sim, const char* defName, std::uint32_t object, unsigned teamBits, unsigned uintValue,
                      std::uint32_t target = 0, unsigned excludePlayers = 0);

// D11 test aid (the commands file's "xp <amount>" lines): XP to the player with gameplay index `index` now, as
// [host] grant_xp does once at the start (LabsPlayer_GrantXPIfValid, nXPSourceType.Cheat); logs the level and the
// ability points before and after. False if the player is missing or the call failed.
bool GrantXpNow(void* sim, unsigned index, float amount, const char* source);

// D15 test aid (the commands file's "gold <amount>" lines): gold to the player now, through LabsPlayer_GrantGold
// (nGoldSourceType.Cheat; OnGrantGold runs), as the item batches need for 106 purchases. The client gets it with the
// player's next AA, so a purchase should come a second later (the client repeats the purchase on its own copy).
bool GrantGoldNow(void* sim, unsigned index, float amount, const char* source);

// D15 test aid (the commands file's "report <label> [near <radius>]" lines): logs the player's wallet and inventory
// (each slot's item, count and passive instances: their script and whether they live), its creature's HP and the 72
// stored attributes, its modifiers (script, stacks, duration), and with a radius the modifiers and HP of every live
// object within it. Lines "items: report <label> player <i> ..."; re/scripts/item_catalog.py verify reads them.
bool ItemReportNow(void* sim, unsigned index, const char* label, float nearRadius);

}  // namespace cmds
