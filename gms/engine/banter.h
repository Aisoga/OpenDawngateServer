// D11b: the shapers' banter, kill and assist voice lines and the presence lines on the match host (docs/GMS_PROTOCOL.md
// *The shapers' banter and kill lines (D11b)*). The client build has the ClientFeedbackDefs, the voice banks, the
// BanterTuning and every shaper's CharacterBanterData, but nothing that sends KillBanterGeneric, KillBanterWithTarget,
// AssistBanter, Friendly/EnemyBanterInitiate/-Response, PlayerLeftGame or PlayerReconnectedGame: the banter component
// (Noun.hasBanterComponent, read by nothing) and the server shell that used them are stripped. The choices are
// gms/core/banter/banter.h's (gms_tests Banter*); this module reads the players' shapers (creature, noun, position, HP,
// mVisibleToTeam, the ~objectnounname~ token, the noun's CharacterBanterData) and Tuning.BanterTuning, and queues each
// line as a ClientFeedbackEvent {def id, speaker, target, uintVal} through cmds::SendFeedback
// (GameSimulator_SendClientFeedbackEvent 0x008F71D0 with the hearing teams' player mask; the replication's drain sends
// it, kind 1), as D7b's voice lines and D11's PlayerRespawned:
//   - OnPlayerKill (death.cpp, after D1h's kill bookkeeping of a player kill): the killer's kill line and one
//     assister's assist line;
//   - Tick (the server tick, after the match step): every BanterTuning.updatePeriod the meeting step (friendly and enemy
//     exchanges, initiate + response);
//   - OnPlayerLeft / OnPlayerReconnected (gms_host.cpp: a human's connection ends during the match / a rejoin's
//     EnterGame): PlayerLeftGame / PlayerReconnectedGame with the player's index;
//   - EmoteVariant (commands.cpp, D7b's voice lines): a line in the speaker's sequentialEmote list (Desecrator's joke)
//     plays its variants in order.
// Everything runs on the engine thread with the server simulator bound; every engine read is under SEH.
// Settings ([host] keys): banter (1: all of it), banter_kill (1), banter_assist (1), banter_friendly (1), banter_enemy
// (1), banter_window (1: the meeting kinds only within BanterTuning's start and end time; 0 = all match long, a test
// aid), banter_multikill_skip (1: no kill line on a multi-kill), banter_presence (1), banter_emote_sequence (1),
// banter_seed (1: the variants' generator), banter_log_limit (300).
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

#include "core/banter/banter.h"

namespace banter {

struct Options {
  bool enabled = true;
  bool kill = true;
  bool assist = true;
  bool friendly = true;
  bool enemy = true;
  bool window = true;
  bool multiKillReplacesKill = true;
  bool presence = true;
  bool emoteSequence = true;
  unsigned seed = 1;
  int logLimit = 300;
};

void Install(const Options& options);
bool Enabled();

// The server tick (bound), after match::Tick: the meeting step when it is due (match running, not ended).
void Tick(void* sim, double matchSeconds);

// A player kill (bound; D1h's outcome): `killer` credited (>= 0), `assisters` with their assist credit times (the assist
// matrix before it is cleared), the killer's multi-kill count after this kill.
void OnPlayerKill(void* sim, int victim, int killer, const std::vector<gms::banter::Assist>& assisters,
                  unsigned multiKill);

// A human's connection ended during the match / a rejoined human got EnterGame (bound).
void OnPlayerLeft(void* sim, int index);
void OnPlayerReconnected(void* sim, int index);

// D7b's voice line `defName` by player `index` (its creature `creature`): the variant to send (`randomVariant` unless
// the line is one of the speaker's sequential emotes).
unsigned EmoteVariant(void* sim, unsigned index, std::uint32_t creature, const char* defName, unsigned randomVariant);

// "kill N (targeted N), assist N, ..." for the stats lines.
void FormatStats(char* buf, std::size_t size);

}  // namespace banter
