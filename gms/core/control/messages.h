// The control channel's messages (ADR-2 decision 6, roadmap C1; docs/HOST_CONTROL.md).
//
// One UTF-8 JSON object per line, its type in "t". These structs mirror server/internal/gmshost's
// (proto.go): same names, same fields, same order, so Encode writes the lines the Go side writes
// (docs/data/host_control_examples.jsonl pins both). Decode ignores unknown fields; a missing field
// keeps its default; a field of the wrong type is an error.
#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <variant>
#include <vector>

namespace dggms::control {

// MaxLine is the longest line either side accepts (bytes, without the newline).
constexpr size_t kMaxLine = 64 * 1024;

// host -> server: the first line. match and token come from DGGMS_MATCH and DGGMS_TOKEN.
struct Hello {
  int64_t match = 0;
  std::string token;
  int64_t pid = 0;
  std::string build;
};

struct RosterEntry {
  int64_t user = 0;  // Blaze id; HelloReq's user id
  std::string name;
  int64_t index = 0;  // gameplay index (HelloPlayer)
  int64_t team = 0;   // 1 or 2
  std::string noun;   // the shaper's noun; "" = the host's choice
  bool bot = false;
  // D3-live step 4: shaper select's content for a human (only in a createGame with shaperSelect; each written
  // only when not empty). Nouns are asset names ("alpha.Noun"); the host takes their FNV-1.
  struct Shaper {
    std::string noun;   // the shaper's default skin
    int64_t flags = 0;  // 1 owned, 2 free rotation, 0x10 hidden, 0x20 available
    int64_t offer = 0;  // store offer id; written only when not 0
  };
  std::vector<Shaper> shapers;                              // the C8 02 list
  std::map<std::string, std::vector<std::string>> skins;    // default skin -> the other owned skins
  std::map<std::string, std::string> savedSkins;            // USET SKIN_0x*: default skin -> the saved skin
  std::vector<std::string> wards, announcers;               // owned vanity assets (C8 0E)
  // D3-perks: the account's perk pages with the stats each gives (gmshost.PerkPage): C8 09 lists the ids, the C8 0A
  // page becomes mLoadoutID/Version, its stats the pre-game loadout C0 02 and the server player's loadout.
  struct PerkAttribute {
    int64_t attribute = 0;  // AssetData attribute enum 0x012464B8 (0 Power, 2 MaxHealth, ...)
    int64_t op = 0;         // op enum 0x01419C70 (1 Add, 2 PercentChange, ...)
    int64_t milli = 0;      // the value in thousandths
  };
  struct PerkPage {
    int64_t id = 0;       // SUMM.PID (the loadout manager's page +8)
    int64_t version = 0;  // SUMM.VERS
    std::vector<PerkAttribute> attributes;  // written only when not empty
    std::vector<std::string> modifiers;     // the passive modifiers' Lua names; written only when not empty
  };
  std::vector<PerkPage> perkPages;  // written only when not empty
  // D8b: the PvP party (Blaze playgroup id) the player queued with, 0 = none (solo, practice, a bot); written only when
  // not 0. The post-game's karma tally counts a vote from the same party as a party vote (postgame::PostGame::SetParty).
  int64_t party = 0;
  // B15: an observer of a custom game (the Blaze spectator team): gameplay index 10-14, team 0; written only when true.
  bool spectator = false;
};

// server -> host: the match to load.
struct CreateGame {
  int64_t gameId = 0;
  std::string level;
  std::string mode;  // "practice", ...
  int64_t seed = 0;
  std::vector<RosterEntry> roster;
  std::map<std::string, int64_t> timers;  // milliseconds; written only when not empty
  // D8-live: the Blaze game's GameFlags attribute (nGameDescriptors; 0x50C = 1292 for the practice game); written only
  // when not 0, and 0 = not given (the host keeps [host] match_descriptors).
  int64_t descriptors = 0;
  // D3-live step 4: run shaper select with the roster's content (dgserver --shaper-select); written only when true.
  bool shaperSelect = false;
  // B10d: fill each team with bot players up to this many seats (a matchmade game that started with open seats:
  // dgserver sends the matchmaker's team size unless --mm-bots=false); written only when not 0, and 0 = not asked
  // (the host's [host] bots auto rule goes by the descriptors' BotsFillTeams).
  int64_t fillTeams = 0;
  // HOLIDAY1: the level flags the host loads the level with and sends in GamePrepareForStart (the client's level loader
  // loads a markerset whose condition names value c only when bit c is set; bit 0 = the Halloween decoration, dgserver
  // --halloween); written only when not 0, and 0 = not given (the host keeps dgsim.ini's [dgsim] flags).
  int64_t levelFlags = 0;
};

// host -> server: level loaded, GMS port bound.
struct GameReady {
  int64_t port = 0;
};

// host -> server: a HelloReq matched a roster entry.
struct AddPlayer {
  int64_t user = 0;
  int64_t index = 0;
};

// host -> server: a player's GMS connection ended.
struct DisconnectPlayer {
  int64_t user = 0;
  std::string reason;
};

// host -> server: heartbeat (every 5 s) and phase changes ("loading", "preGame", "inGame", "postGame").
struct GMSUpdate {
  std::string phase;
  int64_t elapsedMs = 0;
  std::vector<int64_t> players;
};

// One item of the post-game's C1 04: {catalogID, isDuplicate, duplicationAlternate (crafting currency)}.
struct RewardItem {
  int64_t catalogId = 0;
  bool duplicate = false;
  int64_t alternate = 0;
};

// D8c: C1 04's karma part as dgserver counted it into the bag score (+0x18 points, +0x1C/+0x20/+0x24 the votes from
// party, team and enemy). Go: gmshost.Karma.
struct Karma {
  int64_t points = 0;
  int64_t party = 0;
  int64_t team = 0;
  int64_t enemy = 0;
};

// B7c / D8-live: what a player gets for a game, as the post-game's C1 04 carries it (docs/MATCH_FLOW.md *Match end and
// post-game (D8-static)*): the LootBag asset id, Destiny, the bag points, at most 4 items, the FTUE byte, plus the
// crafting currency the duplicates give. Go: gmshost.Rewards (same fields, same order).
struct Rewards {
  int64_t lootBag = 0;
  int64_t destiny = 0;
  int64_t cc = 0;
  std::vector<RewardItem> items;  // written as [] when empty (Go writes null for none; both read both)
  bool ftue = false;
  int64_t baseRoll = 0;
  int64_t winPoints = 0;
  int64_t winAltPoints = 0;
  int64_t firstWinPoints = 0;
  // D8c: present in a matchmade game's playerRewards (dgserver decided the bag with the karma after playerKarma);
  // written only when present
  std::optional<Karma> karma;
};

// host -> server: one player's result.
struct PlayerFinish {
  int64_t user = 0;
  std::map<std::string, int64_t> stats;  // written only when not empty
  std::optional<Rewards> rewards;        // B7c: the host's own proposal; written only when present
};

// server -> host (B7c; the host's side since D8-live): the rewards dgserver decided and stored for one player, after
// finishGame; the host sends the player's C1 04 from them.
struct PlayerRewards {
  int64_t user = 0;
  Rewards rewards;
};

// host -> server (D8c): one player's karma votes in a matchmade game's post-game, once the vote has closed (after
// finishGame): the votes the other players' C1 03 masks gave it, by the voter's relation (postgame TallyKarma).
// dgserver decides the player's loot bag with them and answers playerRewards (with karma). Go: gmshost.PlayerKarma.
struct PlayerKarma {
  int64_t user = 0;
  int64_t party = 0;
  int64_t team = 0;
  int64_t enemy = 0;
};

// host -> server: the match result.
struct FinishGame {
  int64_t winnerTeam = 0;
  int64_t durationMs = 0;
  std::string reason;
};

// host -> server: an error; fatal = the host gives up (since H5 dgserver then ends the match as aborted, unless
// finishGame came first, and kills the host if it hasn't exited within the exit timeout).
struct GMSError {
  int64_t code = 0;
  std::string text;
  bool fatal = false;
};

// reportGMSError's codes (H5; Go: gmshost.Err*, the same numbers and names; docs/HOST_CONTROL.md *A frozen host (H5)*).
inline constexpr int64_t kErrGmsEndpoint = 1;    // the GMS port could not be bound (gms_script's host mode, and since
                                                 // PORTSTUCK the host DLL; fatal, exit code 2): dgserver quarantines
                                                 // the port and starts the game once more on another
inline constexpr int64_t kErrTickStalled = 2;    // the host's watchdog: no server tick for [host] watchdog_s (fatal;
                                                 // the host exits with code 6)
inline constexpr int64_t kErrStepFailing = 3;    // one step of the host's frame threw in every run for [host]
                                                 // step_fail_s (fatal; exit code 7)
inline constexpr int64_t kErrStepException = 4;  // a step of the host's frame threw: that run skipped, counted, the
                                                 // tick goes on (not fatal; the 1st, 10th, 100th, ... of a step)
inline constexpr int64_t kErrLevelLoad = 5;      // the host DLL's level did not load (PORTSTUCK; fatal, exit code 2:
                                                 // not the port's fault, so no retry)
// "GMS endpoint", "tick stalled", "step failing", "step exception", "level load"; "error" for any other code.
const char* ErrorCodeName(int64_t code);

// host -> server: the last line before the host exits.
struct LogoutGMS {};

// server -> host: a player left on the Blaze side (reason: a PlayerRemovedReason name).
struct RemovePlayer {
  int64_t user = 0;
  std::string reason;
};

// server -> host: end the match without a result and exit.
struct AbortGame {
  std::string reason;
};

// server -> host (B15): a custom game's lobby roster after a change (createGame's mode "custom"): every human who may
// connect, with a provisional gameplay index and team. The host lets them connect and holds their HelloReq until
// startGame. Go: gmshost.UpdateRoster.
struct UpdateRoster {
  std::vector<RosterEntry> roster;
};

// server -> host (B15): the custom game starts (its admin's Blaze advanceGameState IN_GAME) with the final roster: team
// 1 at gameplay indexes 0-4, team 2 at 5-9, the spectators at 10-14. Go: gmshost.StartGame.
struct StartGame {
  std::vector<RosterEntry> roster;
};

// createGame's mode of a custom game (B15; Go: gmshost.ModeCustom): its players gather in the client's custom lobby
// first, so the host holds their HelloReq until startGame.
inline constexpr const char* kModeCustom = "custom";

using Message = std::variant<Hello, CreateGame, GameReady, AddPlayer, DisconnectPlayer, GMSUpdate, PlayerFinish,
                             FinishGame, GMSError, LogoutGMS, RemovePlayer, AbortGame, PlayerRewards, PlayerKarma,
                             UpdateRoster, StartGame>;

// TypeName returns a message's "t".
const char* TypeName(const Message& m);

// Encode returns the message as one line, newline included.
std::string Encode(const Message& m);

// Decode parses one line (a trailing "\r\n" or "\n" is allowed). Unknown types are errors.
bool Decode(std::string_view line, Message& out, std::string* err = nullptr);

}  // namespace dggms::control
