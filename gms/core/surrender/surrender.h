// Surrender on the GMS (roadmap D8b; D8b-core, docs/MATCH_FLOW.md *Surrender and the karma vote (D8b-core)*).
// Engine-free: the host (gms/engine) feeds it the players, their connections, the match time and the team totals,
// passes the clients' SurrenderMsgs to it, sends what it hands back and ends the match when a team surrendered.
//
// The messages (wire 0xCA, SurrenderMsgs id 73; raw little-endian like every GMS body; re/scripts/surrender.py builds
// and decodes the same bytes, its `examples` are the golden bytes and its `selftest` re-reads the client's side):
//   client -> GMS   CA 01        vote for surrender; starts the team's vote if none runs (GameClient_SendSurrenderVote1
//                                0x006B4FC0: the in-game menu's SURRENDER GAME button, the vote window's Yes, the chat
//                                commands /surrender /forfeit /ff)
//                   CA 02        vote against (0x006B5090: the window's No, /nosurrender /noforfeit /noff)
//   GMS -> client   CA 00 + u8   surrender is now available, eSurrenderAvailablity 1..4 (0 asserts "reason > 0"): a
//                                chat line "Surrender is now available due to ..." for 2..4, none for 1 (0x006B77A0)
//                   CA 01 + u8   the request failed, eSurrenderRequestFailed 0..3: a chat line (0x006B7860)
//                   CA 03 + 20   the team's vote status (0x006B62C0): u8 team, i64 vote start (server ms), u8 state,
//                                u8 gameplay index x 5, u8 vote x 5 (low 7 bits the slot's vote, bit 7 a flag); the
//                                vote window shows it, with a timer bar to start + surrenderVoteDurationInSeconds
//                   CA 04 + 3    the team's result (GameClient_OnSurrenderTeamResult 0x006BC0A0): u8 result
//                                (3 succeeded, 4 failed), u8 team, u8 yes percent: the chat line "The surrender vote has
//                                passed/failed! (~yesVotePercent~% in favor, requires ~requiredVotePercent~%)", "The
//                                Enemy team has surrendered!" for the other team; requiredVotePercent is the client's own
//                                surrenderVoteSuccessThreshold x 100
//   Subtype 2 (and anything else) asserts on the client ("Unexpected surrender vote message").
//   The in-game menu enables SURRENDER GAME while the UI data's mSurrenderAvailablity (8B member 3, +0x08 u8) is not 0
//   and the player isn't an observer (OptionsMM.ShouldEnableSurrender 0x004ECC00); that is the only reader.
//
// SurrenderVote, one per match (the rules marked [assumed] are ours; the client only shows what it is told):
//   availability per team, sticky once given: match time >= surrenderEnableOn_MatchTimeInMinutes (both teams, reason
//     1); a player of the team disconnected for >= surrenderEnableOn_DisconnectionTimeInSeconds (2) [assumed: that
//     team]; the team's total gold behind by >= surrenderEnableOn_Economy (3) or its kills behind by >=
//     surrenderEnableOn_Kills (4) [assumed: the team behind]; CA 00 {reason} to the team [assumed] when it comes;
//   a request (CA 01) without a running vote: refused with CA 01 {reason} (3 observer, 2 the player's disconnected time
//     >= the disconnection threshold [assumed], 0 not available, 1 within surrenderVoteCooldownInSeconds of the team's
//     last failed vote); otherwise the vote starts at once with the requester's yes; the team's connected eligible
//     players are pending, the rest ineligible;
//   a vote (CA 01/CA 02) of a pending player counts once [assumed]; CA 02 without a running vote is ignored [assumed];
//     a pending player whose connection ends is ineligible for the rest of the vote, as at the start (Q2);
//   a player back on a new connection keeps its entry (AddPlayer with its old gameplay index, then OnConnection; G3a):
//     no reason 2 for a team that is complete again, its disconnected time kept, its slot unchanged;
//   the vote passes when yes / eligible >= surrenderVoteSuccessThreshold; it ends as soon as the outcome is decided
//     (endWhenDecided [assumed]), when nobody is pending, or after surrenderVoteDurationInSeconds (pending players
//     abstain); while it runs the state is 1 (would pass now) or 2 (would fail now) [inferred from the window's title];
//   every change goes out as CA 03 to the team and observers; the end as CA 03 (state 3/4) and CA 04 (to everyone when
//     it passed: the other team reads "The Enemy team has surrendered!"; to the team and observers when it failed);
//   a passed vote is the match's end: TakeEnd gives the team and the end reason (team 1 surrendered: 4, Spirit wins by
//     surrender; team 2: 2), which the host passes to its EndMatch (the surrendering team's Guardian in C1 01); a failed
//     one starts the team's cooldown.
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/endpoint.h"

namespace gms {
namespace surrender {

// ---- the messages ---------------------------------------------------------------------------------------------------

constexpr uint8_t kWire = 0xCA;               // SurrenderMsgs (id 73)
constexpr uint8_t kSubAvailable = 0;          // GMS -> client: u8 eSurrenderAvailablity
constexpr uint8_t kSubRequestFailed = 1;      // GMS -> client: u8 eSurrenderRequestFailed
constexpr uint8_t kSubStatus = 3;             // GMS -> client: the team's vote status (20 bytes)
constexpr uint8_t kSubTeamResult = 4;         // GMS -> client: u8 result, u8 team, u8 yes percent
constexpr uint8_t kClientVoteYes = 1;         // client -> GMS: CA 01
constexpr uint8_t kClientVoteNo = 2;          // client -> GMS: CA 02
constexpr size_t kStatusBodySize = 20;        // after the subtype (ReadBytes 0x14)

// eSurrenderAvailablity (the client's table 0x013F5DF4: {0, 0, 0xA8A554D5, ..D6, ..D7}; the locale ids are
// FNV-1("eSurrenderAvailablity") 0xA8A554D4 + reason - 1).
enum Availability : uint8_t {
  kUnavailable = 0,      // asserts "reason > 0" in CA 00; mSurrenderAvailablity 0 hides the menu button
  kAvailMatchTime = 1,   // "... due to match duration" (the client shows no line for it)
  kAvailDisconnect = 2,  // "... due to a player being disconnected too long"
  kAvailEconomy = 3,     // "... due to large team economy difference"
  kAvailKills = 4,       // "... due to large team kill difference"
};
// eSurrenderRequestFailed (table 0x013F5E08: 0x48DD7F7C + reason; FNV-1("eSurrenderRequestFailed") = 0x48DD7F7C).
enum RequestFailed : uint8_t {
  kFailNotYetAvailable = 0,  // "Surrender is not yet available this match"
  kFailRecentVote = 1,       // "Surrender unavailable due to recent failed surrender vote"
  kFailDisconnected = 2,     // "You are ineligible to participate in surrender voting due to your disconnection ..."
  kFailSpectator = 3,        // "Surrender is not available to spectators"
};
// The vote's state (status +9; the vote window cSurrender 0x0052EC80 and the observers' block choice 0x0052D700).
enum VoteState : uint8_t {
  kVoteStarted = 0,    // shown like 2 [unknown meaning; never sent here]
  kVotePassing = 1,    // running, title "Current Vote Status: Pass"
  kVoteFailing = 2,    // running, title "... Fail"
  kVoteSucceeded = 3,  // kSurrenderTeamResult_Succeeded: "Pass", the window closes 4 s later
  kVoteFailed = 4,     // kSurrenderTeamResult_Failed: "Fail", closes 4 s later
  kVoteClosed = 5,     // the window closes at once [inferred; never sent here]
  kVoteNone = 6,       // the client's reset value (0x004A61D0): no vote
};
// A slot's vote (status +15 + i, low 7 bits; the window's list and the local player's own line).
enum SlotVote : uint8_t {
  kSlotEmpty = 0,       // "You are not eligible to vote", not listed (the client's reset value)
  kSlotIneligible = 1,  // "You are not eligible to vote", not listed [inferred meaning]
  kSlotPending = 2,     // listed as not voted yet; the Yes/No buttons are enabled only in this state
  kSlotAbstained = 3,   // listed as not voted, buttons off [assumed meaning: the vote ended without the slot's vote]
  kSlotYes = 4,         // "You voted: Yes"
  kSlotNo = 5,          // "You voted: No"
};
constexpr int kSlots = 5;
constexpr uint8_t kNoIndex = 0xFF;  // the client's reset value of a slot's gameplay index

struct Slot {
  uint8_t index = kNoIndex;   // gameplay index (the window matches it against the viewed player)
  uint8_t vote = kSlotEmpty;  // SlotVote
  bool flag = true;           // bit 7 of the vote byte: no reader found; the client's reset value is 1 [unknown]
};

struct Status {
  uint8_t team = 0;           // 1 -> the client's Mortal block (+0xC0 of its UI data), anything else the Spirit block
  int64_t startMs = 0;        // the vote's start in server ms (the timer bar runs to start + the vote duration)
  uint8_t state = kVoteNone;  // VoteState
  Slot slots[kSlots];
};

Bytes BuildAvailable(uint8_t reason);                                     // ca 00 + u8
Bytes BuildRequestFailed(uint8_t reason);                                 // ca 01 + u8
Bytes BuildStatus(const Status& s);                                       // ca 03 + 20 bytes
Bytes BuildTeamResult(uint8_t result, uint8_t team, uint8_t yesPercent);  // ca 04 + 3 bytes

// The client's vote: the body after the wire byte, {1} or {2}.
bool ParseClientVote(const uint8_t* body, size_t n, bool* yes);

// A GMS message back into fields (the body after the wire byte; for the tests and the logs).
struct ServerMessage {
  uint8_t sub = 0;
  uint8_t reason = 0;  // CA 00 / CA 01
  Status status;       // CA 03
  uint8_t result = 0, team = 0, yesPercent = 0;  // CA 04
};
bool ParseServerMessage(const uint8_t* body, size_t n, ServerMessage* out);
// "SurrenderMsgs 03: team 1 vote status ..." for a whole message (wire byte first), either direction.
std::string Describe(const Bytes& msg, bool fromClient);

// The end reason when `surrenderingTeam` surrendered: team 1 (Mortal) -> 4 (Spirit wins by surrender), team 2 -> 2
// (Mortal wins by surrender), else 0.
uint32_t EndReason(int surrenderingTeam);
// round(100 * yes / eligible) [assumed], 0 without eligible voters.
int YesPercent(int yes, int eligible);

// ---- the vote -------------------------------------------------------------------------------------------------------

// LevelGameplayTuning (the level's; AssetData Tuning and Tuning_MM_V3 have the same values) and our own choices.
struct Tuning {
  double enableMatchMinutes = 20;     // +0x38 surrenderEnableOn_MatchTimeInMinutes (int64)
  double enableEconomy = 20000;       // +0x40 surrenderEnableOn_Economy (float)
  int enableKills = 19;               // +0x44 surrenderEnableOn_Kills (int32)
  double enableDisconnectS = 300;     // +0x48 surrenderEnableOn_DisconnectionTimeInSeconds (int64)
  double voteS = 60;                  // +0x50 surrenderVoteDurationInSeconds (int64; the client's timer bar uses it too)
  double cooldownS = 300;             // +0x58 surrenderVoteCooldownInSeconds (int64)
  float threshold = 0.666f;           // +0x60 surrenderVoteSuccessThreshold (float; the client shows 66 %, D8b live)
  bool endWhenDecided = true;         // end the vote once its outcome can't change [assumed]
};

// The team totals the availability looks at (index 1 and 2; 0 unused).
struct MatchView {
  double matchS = 0;    // match seconds (the HUD clock)
  int kills[3] = {0, 0, 0};
  double gold[3] = {0, 0, 0};  // the players' total gold summed per team [assumed measure of "economy"]
};

struct Send {
  ConnId conn = 0;
  Bytes msg;
  std::string what;  // for the host's log
};

// A passed vote: the match ends.
struct End {
  int team = 0;          // the team that surrendered
  uint32_t reason = 0;   // EndReason(team): 2 or 4
  int yesPercent = 0;
  double at = 0;         // nowS of the end
};

class SurrenderVote {
 public:
  explicit SurrenderVote(const Tuning& t = Tuning()) : t_(t) {}

  // Every player of the match (team 1 or 2) and every observer, at HelloReq; connected from then on. G3a: a gameplay
  // index that another connection has already is that player back on a new connection (the session's RejoinMap gives
  // a returning user its old index): its entry moves to `conn` with its team, its disconnected time and its slot in a
  // running vote, and stays disconnected until OnConnection(conn, true, ...) (IMatch::OnRejoin: AddPlayer, then that).
  // The old connection's later OnConnection calls find nothing. Without the move a returning player would count as
  // still away, and reason 2 would make its team's surrender available 300 s later although the team is complete.
  void AddPlayer(ConnId conn, int index, int team, bool observer = false);
  // The GMS connection was lost (false) or is back (true): the disconnected time counts for the availability
  // (reason 2) and the player's eligibility. A pending voter of a running vote who disconnects becomes ineligible for
  // that vote, as if it had been disconnected at the start (Q2); the next Tick counts the vote again. G3a: with `out`,
  // a connection that is (back) up during its team's running vote gets the vote's status (CA 03) for its new client's
  // window, also when the moved entry never counted as away; a slot made ineligible by the disconnect stays so.
  void OnConnection(ConnId conn, bool connected, double nowS, std::vector<Send>* out = nullptr);
  // Every tick: the availability, a running vote's time. `nowS` is any monotonic clock in seconds, `serverMs` the
  // session's server clock (CA 03's vote start).
  void Tick(double nowS, int64_t serverMs, const MatchView& m, std::vector<Send>* out);
  // A client message with wire byte 0xCA (the body after it). True if it was a SurrenderMsgs vote of a known player.
  bool OnClientMessage(double nowS, int64_t serverMs, ConnId conn, const uint8_t* body, size_t n,
                       std::vector<Send>* out);
  // The match ended another way (a Guardian died): no more votes, nothing more is sent.
  void Stop() { stopped_ = true; }

  // The end, once, after a vote passed.
  bool TakeEnd(End* out);
  // eSurrenderAvailablity of a team (0 until given).
  uint8_t Availability(int team) const;
  // For the UI data's mSurrenderAvailablity (8B member 3, one value for everyone): the first team's reason once any
  // team may surrender [assumed]; a request from a team without it is refused with CA 01 {0}.
  uint8_t UiAvailability() const;
  bool Running(int team) const;
  const Status& LastStatus(int team) const;
  // "surrender: team 1 available (match time), team 2 -; team 1 vote 2/5 yes, 1 no, 2 pending" for the state line.
  std::string Describe() const;

 private:
  struct Player {
    int index = -1;
    int team = 0;
    bool observer = false;
    bool connected = true;
    double disconnectedAt = 0;
    double disconnectedS = 0;  // finished disconnections
  };
  struct Team {
    uint8_t available = kUnavailable;
    bool running = false;
    double startS = 0;
    int64_t startMs = 0;
    std::map<ConnId, uint8_t> votes;  // the team's slot players -> SlotVote
    bool recount = false;             // a pending voter left: Tick evaluates the vote again (Q2)
    double cooldownUntil = -1;
    Status last;
  };
  double DisconnectedS(const Player& p, double nowS) const;
  bool Eligible(const Player& p, double nowS) const;
  Status BuildTeamStatus(int team, uint8_t state) const;
  void Recipients(int team, bool everyone, bool observers, std::vector<ConnId>* out) const;
  void SendStatus(int team, uint8_t state, std::vector<Send>* out);
  void Start(int team, ConnId requester, double nowS, int64_t serverMs, std::vector<Send>* out);
  void Evaluate(int team, double nowS, bool timeUp, std::vector<Send>* out);
  void Finish(int team, bool passed, int yes, int eligible, double nowS, std::vector<Send>* out);

  Tuning t_;
  std::map<ConnId, Player> players_;
  Team teams_[3];
  bool stopped_ = false;
  bool ended_ = false, endTaken_ = false;
  End end_;
};

}  // namespace surrender
}  // namespace gms
