// The match recorder on the match host ([host] match_record, 0 by default). The file's format, the buffered writer,
// its size cap and the sample rate are gms/core/matchrec (engine-free); this module samples the server simulator and
// feeds it:
//   - Tick (match.cpp's tick, through experiment.cpp's TickMatch): opens the file at the match's start (the H and P
//     lines: the match, the map, every seat's team, shaper, role, account id and bot class), then every seat's snapshot
//     at [host] match_record_hz (5): position, HP and max, resource and max, level, gold and total gold,
//     alive/dead/recalling, the creature's target (its blackboard's mTarget) and the teams that see it (mVisibleToTeam);
//     every 5 s of match time the buffer goes to the file;
//   - the commands: every ActionCommandMsgs, AbilityRankUp and ItemTransaction the intake parsed (commands.cpp:
//     "client", "injected" or "bot"), and every request the bot layer made for a bot (agents.cpp's SehBotRequest,
//     tagged "bot:<what>");
//   - the events the host knows: kills (killer, victim, assisters) and other deaths credited to a seat (objectives,
//     structures), item transactions' outcomes, wells captured, the end (then F lines: each seat's final counts);
//   - the file: matchrec_<match>_<UTC start>.txt in the host's data folder (beside dgsim.log), never elsewhere; at most
//     [host] match_record_cap_mb (64) MB: at the cap it ends with "X <ms> cap" and the host stops writing (the match
//     goes on). A write error stops the recorder the same way.
// Its own cost is measured (QueryPerformanceCounter around every call): a "matchrec:" line per match minute and at
// the end (time spent, the largest tick's share, lines and bytes).
// Everything runs on the engine thread with the server simulator bound; every engine read is under SEH.
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

namespace gms {
struct ActionCommand;
struct ItemTransactionRequest;
}  // namespace gms

namespace matchrec {

bool Enabled();

// Every server tick (match.cpp Tick's caller, after it): open at the start, the snapshots, the flush.
void Tick(void* sim);

// The command intake (commands.cpp OnClientMessage), after the parse; `source` "client", "injected" or "bot".
void OnAction(unsigned index, const char* source, const gms::ActionCommand& c);
void OnRankUp(unsigned index, const char* source, unsigned slot, bool ranked);
void OnItem(unsigned index, const char* source, const gms::ItemTransactionRequest& r);
// An item transaction's outcome (commands.cpp OnItemTransaction): `slot` the server's slot, gold before and after.
void OnItemResult(unsigned index, const char* source, const gms::ItemTransactionRequest& r, unsigned slot, bool done,
                  bool undo, float goldBefore, float goldAfter);
// A request the bot layer made for the bot whose creature is `self` (agents.cpp SehBotRequest).
void OnBotRequest(std::uint32_t self, const char* where, std::uint32_t guid, int slot, std::uint32_t target,
                  const float* pos, int result);
// A consumable the bot layer used for the bot whose creature is `self` (BOTS2c's potions, bot2io::UseItem): the bot's
// "item" command (type use) and an "item" event with its outcome, as a client's use is filed.
void OnBotItemUse(std::uint32_t self, const char* where, std::uint32_t item, int invSlot, bool used, float gold);

// The events (death.cpp, wells.cpp, match.cpp), as matchstats gets them.
void OnDeath(bool playerControlled, int npcType, int victimTeam, int creditSeat, int victimSeat, int killerSeat,
             const std::vector<int>* assisters);
void OnWellCaptured(double matchSeconds, int well, int team, const std::vector<int>& capturers);
void AtMatchEnd(void* sim, int winnerTeam, unsigned long long durationMs, int surrenderTeam);

}  // namespace matchrec
