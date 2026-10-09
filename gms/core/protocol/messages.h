// The raw fixed layouts of the GMS session messages (gms/core, G1), as docs/GMS_PROTOCOL.md derives them from the
// client's readers and writers (B9, D2b-static, D2c) and D2b-live confirmed them with the live client. Every message
// is the wire byte (protocol/wire.h) followed by raw little-endian memory, except where noted. Builders return the
// whole message, wire byte first; parsers take the body after the wire byte.
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "core/common/bytes.h"

namespace gms {

// ---- values the live sessions use -----------------------------------------------------------------------------------
constexpr uint32_t kLevelSmMapV5 = 0x946D41FE;  // FNV-1 of "sm_map_v5.level" (the AssetCatalog id)
constexpr uint32_t kNounAlpha = 0xEF044AAB;     // FNV-1 of "alpha.noun"; resolves live (D2b-live)
constexpr uint8_t kTeamBlue = 1;                // mTeam: 1 blue, 2 red (255 = unset, the constructor's default)
constexpr uint8_t kTeamRed = 2;

// FNV-1 of the lower-cased name: the AssetCatalog id rule ("SM_Map_v5.Level" -> 0x946D41FE, "alpha.Noun" ->
// 0xEF044AAB; D2b, D2c).
uint32_t AssetIdOf(const std::string& name);

// mStatus bits (docs/GMS_PROTOCOL.md "Player status flags").
constexpr uint32_t kStatusGone = 0x4;                // G3a: the GMS's bit for a player whose connection ended (D2c's
                                                     // reading "gone"); the pre-game gates leave such players out
constexpr uint32_t kStatusSkipShaperSelect = 0x10;   // the GMS's own bit: PreDungeon loads the level at once
constexpr uint32_t kStatusShaperSelectDone = 0x100;
constexpr uint32_t kStatusLevelLoaded = 0x400;       // the client reports 0x401 once the level is loaded
constexpr uint32_t kStatusMatchAccepted = 0x10000;   // the match-found accept; the GMS gives it to scripted players
constexpr uint32_t kStatusMatchDeclined = 0x20000;   // the match-found decline (MatchFoundUI's callback 0x005456F0)
constexpr uint32_t kStatusLeftGame = 0x40000;        // G3b: the client's answer to GameAborted (LobbyState's handler)

// B15b: spectators (docs/GMS_PROTOCOL.md *Spectators on the match host (B15b)*). Gameplay indexes 10-14 are a game's
// spectators: the client's player-added callback puts such an index into its simulator's spectator map
// (GameSimulator_IsSpectatorIndex 0x008EED10: index - 10 <= 4; GameSimulator_AddPlayer 0x008F3E20). What the client's
// observer paths read is the observer bit 0x20 of its own player's mStatus (LabsPlayer_IsObserver 0x00912040, through
// GameClient_IsLocalPlayerObserver 0x0066D010): EnterGame goes to kGameStateSpectator (6) instead of Dungeon
// (GameClient_OnGmsEnterGame 0x006B5CD0), shaper select shows both teams and sends no C8 02 (ShaperSelect_Init
// 0x00524620), commands aren't sent, the game-over view is the spectator's.
constexpr uint8_t kFirstSpectatorIndex = 10;
constexpr uint8_t kMaxSpectators = 5;
inline bool IsSpectatorIndex(uint8_t index) {
  return static_cast<uint8_t>(index - kFirstSpectatorIndex) < kMaxSpectators;
}
constexpr uint32_t kStatusObserver = 0x20;
// The mTeam a spectator's player gets [assumed]: an observer's shaper select splits the ten records by its own team
// (ShaperSelect_RefreshPlayerSlots 0x0051F050: GameClient_GetLocalTeam(0) left, every other team right, into two lists of
// five without a bound check), so a team other than 1 or 2 would put all ten on one side. Team 1 puts the Mortals left.
constexpr uint8_t kSpectatorTeam = kTeamBlue;

// BattlegroundsPreGameMsgs (0xC0) subtypes.
constexpr uint8_t kPreGameLoad = 1;       // GMS -> client: loading screen and Client_LoadLevel (0x006AABA0)
constexpr uint8_t kPreGameDone = 3;       // GMS -> client: pre-game sub-state 3, before EnterGame
constexpr uint8_t kPreGameAllLoaded = 4;  // client -> GMS: every player's echoed status has 0x400

// ---- client -> GMS --------------------------------------------------------------------------------------------------
// HelloReq (0x81): u64 user id, u64 playgroup id. cClientSession::OnGmsConnected (0x00AB2A30) copies the 16 bytes
// from the session at +0x08..+0x17 (B9). The user id is the persona id the client logged in with.
struct HelloReq {
  uint64_t userId = 0;
  uint64_t playgroupId = 0;
};
bool ParseHelloReq(const uint8_t* body, size_t n, HelloReq* out);  // needs 8 bytes; playgroup 0 if absent

// ClockSync (0xC7) from the client: i64 client clock in ms.
bool ParseClockSync(const uint8_t* body, size_t n, int64_t* clientMs);

// PlayerStatusUpdate (0x88): u32 status flags (the bits the client reports), f32 progress.
struct PlayerStatusReport {
  uint32_t flags = 0;
  float progress = 0.0f;
};
bool ParsePlayerStatusUpdate(const uint8_t* body, size_t n, PlayerStatusReport* out);

// ---- GMS -> client --------------------------------------------------------------------------------------------------
// Connected (0x84), no body: the GMS speaks first; the client sends HelloReq only after it (B9).
Bytes BuildConnected();

// HelloPlayer (0x82): n x {u8 type (0 = me), u8 gameplayIndex, u32 IPv4, u16 port}; IP and port in network order.
// The client ignores IP and port (a compiled-out log line); the session sends 127.0.0.1:10042 (D2b-live).
struct HelloPlayerRecord {
  uint8_t type = 0;
  uint8_t gameplayIndex = 0;
  uint32_t ipv4 = 0x7F000001;  // as a number: 0x7F000001 = 127.0.0.1
  uint16_t port = 10042;
};
Bytes BuildHelloPlayer(const std::vector<HelloPlayerRecord>& records);

// PlayerJoined (0x86) and PlayerDeparted (0x87): u8 gameplayIndex. PlayerJoined goes through HelloPlayer's
// player-added callback (GameSimulator_AddPlayer with that index; reader 0x00AB3010), PlayerDeparted through the
// player-removed callback (GameSimulator_RemovePlayer; reader 0x00AB3190). G3a: GmsSession::SendPlayerJoined keeps
// the bookkeeping of which players a client has; G3 decides when to send them.
Bytes BuildPlayerJoined(uint8_t gameplayIndex);
Bytes BuildPlayerDeparted(uint8_t gameplayIndex);

// GamePrepareForStart (0xB5), 21 bytes: u32 0, u32 flags, u32 level, u32 seed, u32 level flags, u8 0 (non-zero would
// force connect type 4). Reader GameClient_OnGmsGamePrepareForStart (0x006B5AB0); must arrive in kGameStateLobby.
struct GameSetup {
  uint32_t prepareFlags = 2;  // bit 1: connect type 3, accepts by itself (bit 0 = type 1 shows MatchFoundUI's prompt)
  uint32_t level = kLevelSmMapV5;
  uint32_t seed = 1;
  uint32_t levelFlags = 0;
  uint64_t blazeGameId = 0;   // G3b: not in GamePrepareForStart; GameAborted's game id (createGame's gameId)
};
Bytes BuildGamePrepareForStart(const GameSetup& s);

// G3b: GameAborted (0x89), 91 bytes (GameClient_OnGmsGameAborted 0x006B6400 reads them raw and posts
// kMessageGMSGameAborted 0x58383C9F; LobbyState_HandleMessage 0x00546A40 handles it): u64 Blaze game id (asserted equal
// to the client's game), 10 x u64 player ids (Blaze ids), u8 first and u8 count of the range of ids that caused the
// abort (first + count <= 10), u8 gamePhase (1 = the lobby's waiting phase; asserted). In a matchmade game (the game's
// GameFlags bit 0) a client whose own id is not in the range restarts matchmaking (GameEntryManager vtable slot 43,
// GameEntryManager_RestartMatchmaking 0x0068D980) and shows a dialog; every client answers with status 0x40000.
constexpr uint8_t kGameAbortPhaseWaiting = 1;
Bytes BuildGameAborted(uint64_t blazeGameId, const std::vector<uint64_t>& playerIds, uint8_t first, uint8_t count,
                       uint8_t gamePhase = kGameAbortPhaseWaiting);

// ClockSync (0xC7) answer: the client's i64 echoed, then the server clock in ms (> 0, else the reader 0x006B6110
// drops the sample).
Bytes BuildClockSyncAnswer(int64_t clientMs, int64_t serverMs);

// EnterPreGameFlow (0xB6), no body. The reader (0x006B5C60) asserts a primed time translator (5 ClockSyncs).
Bytes BuildEnterPreGameFlow();

// BattlegroundsPreGameMsgs (0xC0) + subtype.
Bytes BuildPreGame(uint8_t subtype);

// EnterGame (0xB7) 00: Client_StartGame, kGameStateDungeon (0x006B5CD0).
Bytes BuildEnterGame();

// LabsPlayerGlobalUpdate (0xA9): u8 gameplayIndex + a reflected labsPlayerGlobalData (D2c): mask bytes (high nibble of
// the first = their count N; the last byte holds members 0-7), then the set members in order, raw little-endian.
// The first update after HelloPlayer: members 0 mStatus (left out while 0), 8 mTeam, 16 mpNounDef (left out while 0),
// 17 mPlayerOnlineId. Mask: N = 3; byte 0 = 0x30 | members 16-17, byte 1 = members 8-15, byte 2 = members 0-7.
Bytes BuildPlayerUpdate(uint8_t gameplayIndex, uint32_t status, uint8_t team, uint32_t noun, uint64_t onlineId);

// The status echo (D2c's status model): members 0 mStatus and 1 mStatusProgress, mask 0x13.
Bytes BuildPlayerStatusEcho(uint8_t gameplayIndex, uint32_t status, float progress);

// labsPlayerGlobalData member indexes (docs/data/gms_reflected_types.txt), for IMatch::PlayerUpdate (D2f, seam 5).
enum PlayerMember : int {
  kPlayerStatus = 0,          // mStatus
  kPlayerStatusProgress = 1,  // mStatusProgress
  kPlayerCurrentCreature = 2, // mCurrentCreatureId (tObjID, mapped by the client)
  kPlayerTeam = 8,            // mTeam
  kPlayerRole = 9,            // mRole (D3-core)
  kPlayerIsBot = 11,          // mbIsBot (D3-core)
  kPlayerLoadoutId = 13,      // mLoadoutID (D3-core)
  kPlayerLoadoutVersion = 14, // mLoadoutVersion (D3-core)
  kPlayerNounDef = 16,        // mpNounDef (asset id)
  kPlayerOnlineId = 17,       // mPlayerOnlineId
};
constexpr int kPlayerMemberCount = 18;
// The members BuildPlayerUpdate / BuildPlayerStatusEcho carry for these values.
std::vector<int> PlayerUpdateMembers(uint32_t status, uint32_t noun);
std::vector<int> PlayerStatusEchoMembers();

// D3-core: any set of labsPlayerGlobalData members. The values of all 18 members (sizes from
// docs/data/gms_reflected_types.txt); BuildPlayerGlobalUpdate writes the listed ones in member order with the client
// writer's mask rule (0x008F8770: N = ceil((highest + 5) / 8) mask bytes, N in the first byte's high nibble), so for
// the members of BuildPlayerUpdate and BuildPlayerStatusEcho it gives the same bytes.
struct PlayerGlobalValues {
  uint32_t status = 0;
  float progress = 0.0f;
  uint32_t creature = 0;
  uint32_t kills = 0, deaths = 0, assists = 0;
  uint64_t deathTime = 0, respawnTime = 0;
  uint8_t team = 255, role = 0, playerIndex = 0, isBot = 0;
  uint32_t developerPermissions = 0;
  uint32_t loadoutId = 0, loadoutVersion = 0;
  uint32_t botData = 0;  // asset id
  uint32_t noun = 0;     // mpNounDef, asset id
  uint64_t onlineId = 0;
};
// Empty Bytes for an empty or out-of-range member list.
Bytes BuildPlayerGlobalUpdate(uint8_t gameplayIndex, const std::vector<int>& members, const PlayerGlobalValues& v);
// The reverse, for a whole message (wire byte 0xA9 first): the index, the members present in order, their values.
bool ParsePlayerGlobalUpdate(const uint8_t* msg, size_t n, uint8_t* gameplayIndex, std::vector<int>* members,
                             PlayerGlobalValues* v);

}  // namespace gms
