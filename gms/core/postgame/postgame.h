// The match end and the post-game on the GMS (roadmap D8-live; docs/MATCH_FLOW.md *Match end and post-game
// (D8-static)* and *D8-live*). Engine-free: the host (gms/engine/gms_host.cpp) feeds it the end, the client's
// messages, the players' status words and the rewards, and sends what Tick hands back.
//
// The messages (re/scripts/postgame.py builds and decodes the same bytes; its `examples` are the golden bytes):
//   BF 00                  BattlegroundsMsgs subtype 0: the client switches to kGameStateGameOver
//                          (DungeonState_OnBattlegroundsMsg 0x00544C80) in the next frame
//   C1 01 + 12 bytes       u32 end reason (1 Mortal wins, 2 Mortal by surrender, 3 Spirit wins, 4 Spirit by surrender),
//                          u32 the dead Guardian's server id, u32 the game descriptors (GameOverState_OnGameOver
//                          0x00543560): the client's EndGame, the stats snapshot, the end cinematic
//   C1 02 + 16 bytes       i64 vote start, i64 vote end (server ms; GameOverState_OnVoteTimer 0x005428B0), matchmade
//   C1 04 + 0x78 bytes     the rewards (GameOverState_OnRewards 0x00543A80): loot bag, Destiny, bag points, <= 4 items,
//                          the FTUE byte (FLAG bit 0 in the client's account data)
//   C1 05                  exit: ClientLeaveGame(-2) (never sent by PostGame: it strands a client whose cinematic waits
//                          for the loot-bag click, D8-static *Leaving the GameOver state*)
// and from the client:
//   C1 01                  sent from GameOverState_OnEnter (0x00542A80) right after it subscribed id 65: the signal to
//                          send C1 01 {...} (one sent together with BF 00 reaches no handler)
//   88 {0x80000, 0.0}      PlayerStatusUpdate "ready for the karma vote" (nPlayer.SetReadyForKarmaVote 0x00673A00), sent
//                          by the practice and matchmade cinematics before their loot step
//   C1 03 + u16            the karma vote mask (bit = gameplay index) when the vote time ends
//
// PostGame, one per match, a state machine per player connection:
//   Start        BF 00 to every player in the game;
//   then         C1 01 {reason, Guardian, descriptors} on the client's own C1 01, or ackFallbackMs after BF 00 [assumed];
//   then by the game's mode (the descriptors, as the client picks its cinematic):
//     practice (IsSoloPractice | IsGroupPractice), the player's team lost: C1 04 at once (the custom cinematic waits
//                for nothing);
//     practice, won: C1 04 on the player's 0x80000 (the practice cinematic sleeps until C1 04's voteComplete), or
//                readyWaitMs after C1 01;
//     matchmade (IsMatchmade): once every player is ready (or readyWaitMs), C1 02 {now, now + voteMs}; the masks until
//                voteMs + voteGraceMs, when the vote closes and the karma votes each player got are counted
//                (D8b-core: TallyKarma; the voting view offers every other player of both teams, the own row has no
//                karma box); D8c: the host reports them to dgserver (TakeKarmaReports -> playerKarma), which decides
//                the bag with the karma points in its score; C1 04 goes out once those karma-aware rewards are in
//                (OnServerRewards withKarma: sent as they are), or rewardsWaitMs after the close with the server's
//                earlier rewards or the host's own, plus the host's tally and karmaPointsPerVote points per vote
//                [assumed];
//     custom (neither): as a lost practice game (the custom cinematic) [assumed];
//   C1 04 carries the server's rewards (dgserver's playerRewards) once they are in, else the host's own after
//   rewardsWaitMs from the end (matchmade: from the vote's close); never with rewards == kNone.
// The host exits when AllLeft() or after its post-game cap ([host] postgame_cap_s, gms/host/control.cpp).
#pragma once

#include <cstdint>
#include <map>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/endpoint.h"

namespace gms {
namespace postgame {

// ---- the messages ---------------------------------------------------------------------------------------------------

constexpr uint8_t kSubOver = 1;       // C1 01 (both directions)
constexpr uint8_t kSubVoteTimer = 2;  // C1 02
constexpr uint8_t kSubVotes = 3;      // C1 03 (client)
constexpr uint8_t kSubRewards = 4;    // C1 04
constexpr uint8_t kSubExit = 5;       // C1 05
constexpr uint32_t kStatusReadyForKarmaVote = 0x80000;
constexpr size_t kRewardsBodySize = 0x78;
constexpr size_t kMaxItems = 4;

// End reasons (GameSimulator_EndGame's; MATCH_FLOW.md §1.1) and the winning team.
constexpr uint32_t kReasonMortalWins = 1, kReasonMortalBySurrender = 2, kReasonSpiritWins = 3,
                   kReasonSpiritBySurrender = 4;
int WinnerTeam(uint32_t reason);  // 1, 2, or 0 for an unknown reason

// nGameDescriptors (Lua GlobalDefinitions; the Blaze game attribute GameFlags).
constexpr uint32_t kDescMatchmade = 1u << 0, kDescCustom = 1u << 1, kDescSolo = 1u << 2, kDescSoloPractice = 1u << 3,
                   kDescGroupPractice = 1u << 4, kDescFTUE = 1u << 8;
constexpr uint32_t kPracticeDescriptors = 0x50C;  // B8's practice game: Solo | SoloPractice | FTUE | BotsFillTeams

enum class Mode { kPractice, kMatchmade, kCustom };
Mode ModeOf(uint32_t descriptors);
const char* ModeName(Mode m);

// LootBag asset ids: FNV-1 of the lower-cased "<name>.LootBag" (AssetCatalog_FindByName's rule).
uint32_t AssetId(const std::string& name);
constexpr uint32_t kLootBagBronze = 0x4F079E93, kLootBagSilver = 0x6401D184, kLootBagGold = 0xC308B989,
                   kLootBagPlatinum = 0x103EBDF7, kLootBagDiamond = 0xC0FEE8B9;
const char* LootBagName(uint32_t id);  // "Bronze" ... or "" for another id

struct RewardItem {
  uint32_t catalogId = 0;
  bool duplicate = false;
  uint32_t alternate = 0;  // crafting currency shown for a duplicate
};

// C1 04's body (0x78 bytes; MATCH_FLOW.md *C1 04: rewards*).
struct Rewards {
  uint32_t lootBag = 0;     // +0x00 LootBag asset id (an unknown one asserts on the client)
  int32_t destiny = 0;      // +0x04 Destiny (EC) awarded
  int32_t baseRoll = 0;     // +0x08 bag points: base roll
  int32_t winPoints = 0;    // +0x0C game outcome (bonusPoints)
  int32_t winAltPoints = 0; // +0x10 game outcome (altBonusPoints: the match-time half)
  int32_t firstWinPoints = 0;  // +0x14
  int32_t karmaPoints = 0;  // +0x18
  int32_t karmaParty = 0, karmaTeam = 0, karmaEnemy = 0;  // +0x1C..+0x24
  std::vector<RewardItem> items;  // +0x28, 4 x 16 bytes; count at +0x68
  uint32_t extraItem = 0;   // +0x6C (the living-lore-vote reward, inferred)
  uint32_t extraCount = 0;  // +0x70
  bool ftue = false;        // +0x74: FLAG bit 0
};

Bytes BuildGameOver();                                                   // bf 00
Bytes BuildOver(uint32_t reason, uint32_t guardian, uint32_t descriptors);  // c1 01 + 12
Bytes BuildVoteTimer(int64_t startMs, int64_t endMs);                    // c1 02 + 16
bool BuildRewards(const Rewards& r, Bytes* out);                         // c1 04 + 0x78; false with > 4 items
Bytes BuildExit();                                                       // c1 05

// The client's post-game messages (the body after the wire byte 0xC1).
struct ClientMessage {
  uint8_t sub = 0;          // 1 = the GameOver ack, 3 = votes
  uint16_t votes = 0;       // C1 03's mask
};
bool ParseClientMessage(const uint8_t* body, size_t n, ClientMessage* out);
// The whole C1 04 back into fields (for the tests and the logs).
bool ParseRewards(const uint8_t* body, size_t n, Rewards* out);  // body after "c1 04"
std::string Describe(const Rewards& r);

// ---- a practice game's rewards (the D8 proposal; postgame.py practice_rewards, gmshost/progress.Compute) ------------

// GetLootBagBaseRoll: Round((100 * rand + 125)^3.5 / 325446.44), 67..525, in float32 like the server Lua.
int BaseRoll(double rand01);
// GetLootBagGameOutcomePoints(won, matchSeconds): 200 for a win; 0.91 per match minute up to 55, truncated.
void OutcomePoints(bool won, double seconds, int* win, int* alt);
// GetLootBagECAward(bag, ?, matchSeconds, practiceAllowed, descriptors) by the bag's name (Bronze..Diamond).
float EcAward(const char* bag, double seconds, uint32_t descriptors, bool practiceAllowed = true);
// A won FTUE practice game: Platinum (LootBagTuning.FTUEBag_Loot) with FTUELoot (the Resonance perk shape and two
// FlatPower gems); any other game Bronze (FTUEBag_Default) and no items; Destiny rounded down; ftue = IsFTUE.
Rewards PracticeRewards(bool won, double seconds, uint32_t descriptors, double rand01, bool firstWin = false);

// ---- the karma vote (D8b-core) --------------------------------------------------------------------------------------

// The karma votes a player got, by the voter's relation (C1 04 +0x1C..+0x24, the reward calc's KarmaVoteBreakdownData).
struct KarmaVotes {
  int party = 0;  // from players in the same party (same partyId >= 0)
  int team = 0;   // from the rest of the own team
  int enemy = 0;  // from the other team
  int Total() const { return party + team + enemy; }
};
struct KarmaVoter {
  int index = -1;          // gameplay index (C1 03's bit)
  int team = 0;
  long long partyId = -1;  // the Blaze group the player came with, -1 = none
  uint16_t mask = 0;       // the C1 03 mask the player sent (0 if none)
};
// Every voter's mask counted for the players it names: bits for the voter itself or for an index nobody has are
// ignored. The result is keyed by gameplay index and has an entry for every voter.
std::map<int, KarmaVotes> TallyKarma(const std::vector<KarmaVoter>& voters);

// D8c: one player's karma votes for dgserver (the control channel's playerKarma {user, party, team, enemy}; the host
// maps the connection to the user).
struct KarmaReport {
  ConnId conn = 0;
  int index = -1;  // gameplay index
  KarmaVotes votes;
};

// ---- the state machine ----------------------------------------------------------------------------------------------

struct Options {
  int ackFallbackMs = 2000;  // C1 01 {...} without the client's own C1 01 [assumed]
  int readyWaitMs = 60000;   // BattlegroundsGameplayTuning.MaxTimeBeforeVotingStarts (60 s) [assumed: the GMS's]
  // C1 04 waits this long for the server's rewards before the host's own: after the end; in a matchmade game after
  // the vote's close, for the karma-aware rewards (D8c). 0 = no wait (no control channel)
  int rewardsWaitMs = 3000;
  int voteMs = 20000;        // KarmaVotingDuration
  int voteGraceMs = 2000;    // KarmaVotingGracePeriod
  bool sendRewards = true;   // false ([host] rewards=none): no C1 04
  int karmaPointsPerVote = 10;  // C1 04 +0x18 = votes x this [assumed: no data or script has a karma rule]
};

struct Send {
  ConnId conn = 0;
  Bytes msg;
  std::string what;  // for the session's send log
};

class PostGame {
 public:
  explicit PostGame(const Options& o = Options()) : o_(o) {}

  // The players in the game at the end (before Start): the connection, its gameplay index and team, and the host's
  // own rewards for it (PracticeRewards), used if the server's don't come in time.
  void AddPlayer(ConnId conn, int index, int team, const Rewards& own);
  // B15b: a spectator in the game at the end (before Start). It gets BF 00 (its kGameStateSpectator handles it like the
  // Dungeon state: kGameStateGameOver, 0x00549360) and C1 01 on its own C1 01 (or the fallback), which shows it the
  // spectator's victory view (GameOverState_OnGameOver 0x00543560: SetSpiritTeamWon, the plain end cinematic for an
  // observer); no karma vote (it isn't a voter, no C1 02), no C1 04, no playerKarma; not counted by AllLeft.
  void AddSpectator(ConnId conn, int index);
  // The player's party (the Blaze group it joined with; votes from the same party count as party votes).
  void SetParty(ConnId conn, long long partyId);
  // The end: BF 00 to every added player. `nowS` is any monotonic clock in seconds.
  void Start(double nowS, uint32_t reason, uint32_t guardian, uint32_t descriptors, std::vector<Send>* out);

  // dgserver's playerRewards for a player (any time; ignored once C1 04 went out). withKarma (D8c): they carry the
  // karma dgserver counted (playerRewards.rewards.karma present, its fields in r's karma*): a matchmade game's C1 04
  // waits for these and sends them as they are; without, the host fills in its own tally.
  void OnServerRewards(ConnId conn, const Rewards& r, bool withKarma = false);
  // A client message with wire byte 0xC1 (the body after it). True if PostGame took it.
  bool OnClientMessage(double nowS, ConnId conn, const uint8_t* body, size_t n);
  // The session's status word after an update (the echoed one; 0x80000 = ready for the karma vote).
  void OnStatus(double nowS, ConnId conn, uint32_t status);
  void OnDisconnected(ConnId conn);
  // Everything due at `nowS`; serverMs is the session's server clock (C1 02's times).
  void Tick(double nowS, int64_t serverMs, std::vector<Send>* out);

  bool Started() const { return started_; }
  Mode GameMode() const { return mode_; }
  // Matchmade: the karma votes counted when the vote closed (voteMs + voteGraceMs after C1 02); empty before.
  const std::map<int, KarmaVotes>& Karma() const { return karma_; }
  // D8c, matchmade: every player's karma votes once, right after the vote closed (empty before and afterwards); the
  // host sends each as playerKarma, and dgserver answers with the karma-aware rewards (OnServerRewards withKarma).
  std::vector<KarmaReport> TakeKarmaReports();
  bool Has(ConnId conn) const { return players_.count(conn) != 0; }
  // Every player (not spectator) that got BF 00 has disconnected.
  bool AllLeft() const;
  // "post-game: over sent (ack 0.064 s), ready 4.1 s, C1 04 server rewards" for the session's state line.
  std::string Describe(ConnId conn) const;

 private:
  struct Player {
    int index = -1;
    int team = 0;
    bool spectator = false;  // B15b: BF 00 and C1 01 only
    long long partyId = -1;
    Rewards own;
    bool haveServer = false;
    bool serverKarma = false;  // D8c: the server's rewards carry its karma (sent as they are)
    Rewards server;
    bool left = false;
    bool acked = false;
    double ackAt = -1;
    bool overSent = false;
    double overAt = -1;
    bool ready = false;
    double readyAt = -1;
    bool votesIn = false;
    uint16_t votes = 0;
    bool rewardsSent = false;
    double rewardsAt = -1;
    bool rewardsFromServer = false;
  };
  bool RewardsDue(const Player& p, double nowS) const;
  void SendRewards(ConnId conn, Player& p, double nowS, std::vector<Send>* out);

  Options o_;
  std::map<ConnId, Player> players_;
  bool started_ = false;
  double endAt_ = 0;
  uint32_t reason_ = 0, guardian_ = 0, descriptors_ = 0;
  Mode mode_ = Mode::kPractice;
  int winner_ = 0;
  // matchmade voting
  bool voteSent_ = false;
  double voteAt_ = -1;
  bool voteClosed_ = false;
  double voteClosedAt_ = -1;
  std::map<int, KarmaVotes> karma_;
  bool karmaTaken_ = false;  // TakeKarmaReports handed them out
};

}  // namespace postgame
}  // namespace gms
