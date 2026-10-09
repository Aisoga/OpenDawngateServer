// The client's match commands and their answers (gms/core, D7-live): the raw layouts of docs/GMS_PROTOCOL.md
// *Command intake (D7-static)*, read from the client's writers and readers (re/scripts/gms_commands.py checks them
// against Dawngate.exe) and confirmed with the live client's bytes in D7-live. Every body is raw little-endian memory.
// Parsers take the body after the wire byte; builders return the whole message, wire byte first.
//
//   client -> GMS   A6 ActionCommandMsgs   8-byte header {u8 type, u8 seq, u16 garbage, u32 caster} + type data
//                   A7 AbilityRankUp       u32 slot (3-6)
//                   C3 ItemTransaction     11 bytes packed
//                   CA SurrenderMsgs, CD PauseMsgs (parsed for the log only; the game mode answers them, D8)
//   GMS -> client   B4 ActionCommandResponse (72 bytes; built by the server simulator, the host forwards it)
//                   C3 ItemTransaction answer (8 bytes)
//                   C2 MiniMapPing (21 bytes)
//                   9B LocomotionCommand (built by the simulator; described for the log)
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "core/common/bytes.h"

namespace gms {

struct Vec3 {
  float x = 0.0f, y = 0.0f, z = 0.0f;
};

// ActionCommandMsgs command types (the client's ActionCommand_GetDataSize 0x008CCBB0 knows 1-11).
enum ActionCommandType : uint8_t {
  kCmdMove = 1,
  kCmdStopMoving = 2,
  kCmdAbility = 3,
  kCmdInteract = 4,
  kCmdDance = 5,
  kCmdTaunt = 6,
  kCmdPing = 7,
  kCmdStop = 8,
  kCmdAttackMove = 9,
  kCmdVoiceLine = 10,
  kCmdHold = 11,
};
constexpr size_t kActionCommandHeader = 8;
// Data bytes after the header for `type`, or -1 for an unknown type.
int ActionCommandDataSize(uint8_t type);
const char* ActionCommandName(uint8_t type);

constexpr uint32_t kGuidReturnToBase = 0x96B845C6;  // SPID("ability_returntobase") (GameplayControls_ReturnToBase)

// One ActionCommandMsgs body, decoded. Only the members of its type are meaningful.
struct ActionCommand {
  uint8_t type = 0;
  uint8_t seq = 0;      // 1-255; the answers carry it back
  uint32_t caster = 0;  // the player's creature as a server id (0 if it has none)
  // 1 move, 9 attack-move: data {u32 0, f32 pos[3], u32 0, u32 flags (1 = button held)}
  // 7 ping: data {f32 pos[3], u32 target, u32 ping type}
  Vec3 pos;
  uint32_t moveFlags = 0;
  // 3 ability: data {u32 target, f32 aim[3], f32 target pos[3], i32 slot, u32 guid, u32 keep attacking,
  //                  u8 walk up, u8 reactivation, u16 pad}; exactly one of slot (-1 = none) and guid (0 = none)
  uint32_t target = 0;  // also 4 interact (the object) and 7 ping (0 = none)
  Vec3 aim;
  Vec3 targetPos;
  int32_t slot = -1;
  uint32_t guid = 0;
  uint32_t keepAttacking = 0;
  uint8_t walkUp = 0;
  uint8_t reactivation = 0;
  // 7 ping type (0-4), 10 voice line
  uint32_t value = 0;
};

// False (with `error`) for a short body, an unknown type or a data size that doesn't match the type. Bytes 2-3 and
// type 2's data are never written by the client (stack garbage) and are ignored.
bool ParseActionCommand(const uint8_t* body, size_t n, ActionCommand* out, std::string* error);
// The client's message for `c` (A6 + header + data, garbage bytes as 0): what gms_commands.py encode builds.
Bytes BuildActionCommand(const ActionCommand& c);
// "type 1 move, seq 7, caster 0x..: to (x, y, z), flags 0" (the gms_commands.py decode wording).
std::string DescribeActionCommand(const ActionCommand& c);

// AbilityRankUp (A7): u32 slot.
bool ParseAbilityRankUp(const uint8_t* body, size_t n, uint32_t* slot);

// ItemTransaction (C3), client -> GMS, 11 bytes packed. D7b read the server's switch (GameSimulator_OnItemTransaction
// 0x008F1390) case by case: 3 is "use an item from the inventory" (LabsPlayer_CanUseItem 0x00913560 /
// LabsPlayer_UseItem 0x00914160: the item's active ability or modifier, one of a consumable's stack removed; the
// ability panel sends it for an item whose slot has no ability), not "undo" as D7-static had it.
enum ItemTransactionType : uint8_t { kItemPurchase = 1, kItemSell = 2, kItemUse = 3, kItemConsume = 4, kItemSwap = 5 };
constexpr uint8_t kItemSlotNone = 0x0B;  // kNumInventoryItemSlots: "no slot", the purchase check picks one
struct ItemTransactionRequest {
  uint8_t type = 0;
  uint32_t item = 0;  // asset id (asset +0x60); 0 for a swap
  uint8_t slot = 0;
  uint32_t item2 = 0;
  uint8_t slot2 = 0;
};
bool ParseItemTransaction(const uint8_t* body, size_t n, ItemTransactionRequest* out);
// The client's message (C3 + 11 bytes), for tests and the host's test-side commands file.
Bytes BuildItemTransaction(const ItemTransactionRequest& r);
const char* ItemTransactionName(uint8_t type);
// "purchase: item 0x.. slot 11, item 2 0x.. slot 255" (gms_commands.py decode's wording).
std::string DescribeItemTransaction(const ItemTransactionRequest& r);
// The answer, 8 bytes: u8 player index, u8 transaction, u32 item, u8 slot, u8 success (GameClient_OnGmsItemTransaction
// 0x006B5E10 repeats a successful transaction on its copy of that player).
Bytes BuildItemTransactionResult(uint8_t player, uint8_t type, uint32_t item, uint8_t slot, bool success);

// D15: the player's wallet and inventory around a transaction (cLabsPlayer mAvailableGold +0xA8, mSpellPoints +0x20,
// mItems[11] {asset id, count}). The binary's steps can return without a change after a failed assert
// (LabsPlayer_PurchaseItem's "bAdded"/"bUpgraded" 0x00914690, LabsPlayer_SellItem's "Player_RemoveItem"), while
// GameSimulator_OnItemTransaction (0x008F1390) still reports success; the host checks the effect before it answers.
constexpr int kInventorySlots = 11;
struct InventoryState {
  float gold = 0.f;
  uint32_t spellPoints = 0;
  uint32_t item[kInventorySlots] = {};
  int32_t count[kInventorySlots] = {};
};
// True when a transaction of `type` changed what it must change: a purchase, a sale or a consume the gold, the spell
// points or a slot (a spell costs no gold but a spell point and a slot); a swap a spell slot (6-8); a use always (an
// item's active that is no consumable changes no slot, and a consumable's use may fail to start and keep the item).
bool ItemTransactionTookEffect(uint8_t type, const InventoryState& before, const InventoryState& after);

// Voice lines (ActionCommandMsgs type 10) and the emotes' voice (types 5, 6), D7b: the stripped server turned them into a
// ClientFeedbackEvent (A5) with one of 13 PlayerComm ClientFeedbackDefs, whose sound is
// "Play_~objectnounname~_VO_<line>_~uintdata~" (the speaker's PlayerClass.clientFeedbackTokenName, e.g. PC_Alpha, and
// the variant as %02u: every shaper's VO bank has 01-03). The line ids are the quick-chat keys' and the chat commands'
// (GameplayControls 0x0069CA26.., ChatCommands_Execute): 0-5 and 13 are callouts, 8-12 social lines; 6 and 7 are sent
// by nothing. The ids follow the order of the defs' static references in GameSimulator.cpp's data (0x019E207C + 0xC k).
struct VoiceLine {
  uint32_t id;
  const char* name;         // "charge"
  const char* feedbackDef;  // "PlayerCommCharge.ClientFeedbackDef"
  bool team;                // [assumed] callouts to the speaker's team, social lines to everyone
};
const VoiceLine* FindVoiceLine(uint32_t id);  // null for an unknown id
constexpr const char* kFeedbackDance = "PlayerCommDance.ClientFeedbackDef";  // type 5: the "highemote" VO
constexpr const char* kFeedbackTaunt = "PlayerCommTaunt.ClientFeedbackDef";  // type 6: the "taunt" VO
constexpr uint32_t kVoiceLineVariants = 3;

// MiniMapPing (C2), 21 bytes: u32 pinger (server id), u32 target (0 = none), f32 pos[3], u8 ping type.
Bytes BuildMiniMapPing(uint32_t pinger, uint32_t target, const Vec3& pos, uint8_t pingType);

// ActionCommandResponse (B4), 72 bytes (reader AgentSummary_OnActionCommandResponse 0x00658820).
enum ResponseKind : uint8_t { kResponseAbilityStarted = 1, kResponseFailed = 2, kResponseAbilityEnded = 4 };
constexpr size_t kActionCommandResponseSize = 0x48;
struct ActionCommandResponse {
  uint32_t commandType = 0;  // +0
  uint8_t player = 0;        // +4
  uint8_t seq = 0;           // +5
  uint8_t kind = 0;          // +6
  uint32_t guid = 0;         // +8
  int32_t result = 0;        // +0x40 (kind 2)
};
bool ParseActionCommandResponse(const uint8_t* body, size_t n, ActionCommandResponse* out);
// Kind 2 as the server's builder (GameSimulator_SendActionCommandFailed 0x008F0110) writes it, except that +5 carries
// the sequence (the builder leaves it 0; D7-static).
Bytes BuildActionCommandFailed(uint8_t player, uint8_t seq, uint32_t commandType, uint32_t guid, int32_t result);
// Sets +5 (the sequence) of a whole B4 message; false if it isn't one.
bool SetResponseSequence(Bytes* msg, uint8_t seq);

// A one-line description of a GMS -> client message the command path sends or forwards (B4, 9B, C2, C3); empty for
// other messages.
std::string DescribeCommandAnswer(const Bytes& msg);

}  // namespace gms
