// gms/core/protocol/commands (D7-live): the client's match commands and their answers. The golden bytes are what the
// live client sent the match host in d7_run1 (re/logs/d7_run1/host/dgsim.log; posted right-clicks and keys, docs/
// SERVER_MODE.md *Command intake on the server (D7-live)*); the cast is gms_commands.py encode's (the client sent no Q
// in d7_run1: its own check refuses an unranked ability).
#include <cmath>
#include <string>
#include <vector>

#include "core/commands/testlua.h"
#include "core/common/bytes.h"
#include "core/protocol/commands.h"
#include "core/protocol/wire.h"
#include "tests/check.h"

using gms::Bytes;

namespace {

Bytes FromHex(const char* hex) {
  Bytes b;
  gms::ParseHex(hex, b);
  return b;
}

// Parses a whole A6 message (wire byte first).
bool Parse(const Bytes& m, gms::ActionCommand* c, std::string* error = nullptr) {
  return m.size() >= 1 && m[0] == gms::kGmsActionCommandMsgs && gms::ParseActionCommand(m.data() + 1, m.size() - 1, c, error);
}

bool Near(float a, float b) { return std::fabs(a - b) < 0.01f; }

// d7_run1, the client's own bytes (bytes 2-3 and type 2's data are its stack garbage)
const char* const kMove = "a6010250c3350171a6000000001e9753c396656cc39933333f0000000000000000";
const char* const kStopMoving = "a602029100350171a698fd1900de470a0101000000e041ec233b67690003000000";
const char* const kStop = "a608038f00350171a6";
const char* const kAttackMove = "a609056cc3350171a6000000001e9753c396656cc39933333f0000000000000000";
const char* const kReturnToBase =
    "a60306a800350171a600000000000000000000000000000000000000000000000000000000ffffffffc645b8960000000000006ac3";
// gms_commands.py encode cast --slot 3 --pos 12,-30,0 --aim 12,-30,0 --seq 8 --obj 0xA6710135
const char* const kCastQ =
    "a603080000350171a600000000000040410000f0c100000000000040410000f0c10000000003000000000000000000000000000000";

}  // namespace

GMS_TEST(CommandsLiveMove) {
  gms::ActionCommand c;
  CHECK(Parse(FromHex(kMove), &c));
  CHECK_EQ(static_cast<int>(c.type), static_cast<int>(gms::kCmdMove));
  CHECK_EQ(static_cast<int>(c.seq), 2);
  CHECK_EQ(c.caster, 0xA6710135u);
  CHECK(Near(c.pos.x, -211.59f) && Near(c.pos.y, -236.40f) && Near(c.pos.z, 0.70f));
  CHECK_EQ(c.moveFlags, 0u);
  CHECK_EQ(gms::DescribeActionCommand(c),
           std::string("type 1 move, seq 2, caster 0xa6710135: to (-211.59, -236.40, 0.70), flags 0"));
  // the builder writes the garbage bytes 2-3 as 0, everything else as the client did
  Bytes want = FromHex(kMove);
  want[3] = want[4] = 0;
  CHECK_EQ(gms::Hex(gms::BuildActionCommand(c), 200), gms::Hex(want, 200));
}

GMS_TEST(CommandsLiveStopAndAttackMove) {
  gms::ActionCommand c;
  CHECK(Parse(FromHex(kStopMoving), &c));  // its 24 data bytes are never written by the client
  CHECK_EQ(static_cast<int>(c.type), static_cast<int>(gms::kCmdStopMoving));
  CHECK_EQ(static_cast<int>(c.seq), 2);  // the S key: type 2 with the last move's sequence, then type 8 with the next
  CHECK(Parse(FromHex(kStop), &c));
  CHECK_EQ(static_cast<int>(c.type), static_cast<int>(gms::kCmdStop));
  CHECK_EQ(static_cast<int>(c.seq), 3);
  CHECK_EQ(c.caster, 0xA6710135u);
  CHECK_HEX(gms::BuildActionCommand(c), "a608030000350171a6");
  CHECK(Parse(FromHex(kAttackMove), &c));
  CHECK_EQ(static_cast<int>(c.type), static_cast<int>(gms::kCmdAttackMove));
  CHECK_EQ(static_cast<int>(c.seq), 5);
  CHECK(Near(c.pos.x, -211.59f) && Near(c.pos.y, -236.40f));
}

GMS_TEST(CommandsLiveReturnToBase) {
  gms::ActionCommand c;
  CHECK(Parse(FromHex(kReturnToBase), &c));
  CHECK_EQ(static_cast<int>(c.type), static_cast<int>(gms::kCmdAbility));
  CHECK_EQ(static_cast<int>(c.seq), 6);
  CHECK_EQ(c.slot, -1);
  CHECK_EQ(c.guid, gms::kGuidReturnToBase);
  CHECK_EQ(c.target, 0u);
  CHECK_EQ(c.keepAttacking, 0u);
  CHECK_EQ(static_cast<int>(c.walkUp), 0);
  CHECK_EQ(static_cast<int>(c.reactivation), 0);
}

GMS_TEST(CommandsCastBySlot) {
  gms::ActionCommand c;
  CHECK(Parse(FromHex(kCastQ), &c));
  CHECK_EQ(c.slot, 3);
  CHECK_EQ(c.guid, 0u);
  CHECK(Near(c.aim.x, 12.0f) && Near(c.aim.y, -30.0f) && Near(c.targetPos.x, 12.0f) && Near(c.targetPos.y, -30.0f));
  CHECK_EQ(gms::Hex(gms::BuildActionCommand(c), 200), std::string(kCastQ));
}

GMS_TEST(CommandsMalformed) {
  gms::ActionCommand c;
  std::string error;
  CHECK(!Parse(FromHex("a60102"), &c, &error));
  CHECK_EQ(error, std::string("short header (2 B)"));
  CHECK(!Parse(FromHex("a60c010000350171a6"), &c, &error));  // type 12: the client never sends it
  CHECK_EQ(error, std::string("unknown command type 12"));
  CHECK(!Parse(FromHex("a601020000350171a600000000"), &c, &error));  // a move needs 24 data bytes
  CHECK_EQ(error, std::string("type 1: data 4 B, expected 24"));
  for (int t = 1; t <= 11; ++t) CHECK(gms::ActionCommandDataSize(static_cast<uint8_t>(t)) >= 0);
  CHECK_EQ(gms::ActionCommandDataSize(0), -1);
}

GMS_TEST(CommandsPingAndVoiceLine) {
  gms::ActionCommand c;
  c.type = gms::kCmdPing;
  c.seq = 9;
  c.caster = 0xA6710135;
  c.pos = gms::Vec3{12.0f, -35.0f, 0.0f};
  c.target = 0;
  c.value = 3;
  const Bytes m = gms::BuildActionCommand(c);
  CHECK_EQ(m.size(), static_cast<size_t>(1 + 8 + 20));
  gms::ActionCommand back;
  CHECK(Parse(m, &back));
  CHECK_EQ(back.value, 3u);
  CHECK(Near(back.pos.y, -35.0f));
  // the relay to the team: u32 pinger, u32 target, f32 pos[3], u8 type (21 bytes)
  CHECK_HEX(gms::BuildMiniMapPing(c.caster, 0, c.pos, 3), "c2350171a6000000000000404100000cc20000000003");
  c.type = gms::kCmdVoiceLine;
  c.value = 9;  // laugh
  CHECK_HEX(gms::BuildActionCommand(c), "a60a090000350171a609000000");
}

GMS_TEST(CommandsRankUpAndItems) {
  uint32_t slot = 0;
  const Bytes r = FromHex("a703000000");  // gms_commands.py decode: AbilityRankUp slot 3 Q
  CHECK(gms::ParseAbilityRankUp(r.data() + 1, r.size() - 1, &slot));
  CHECK_EQ(slot, 3u);
  CHECK(!gms::ParseAbilityRankUp(r.data() + 1, 3, &slot));
  gms::ItemTransactionRequest it;
  const Bytes t = FromHex("c301443322110200000000ff");
  CHECK(gms::ParseItemTransaction(t.data() + 1, t.size() - 1, &it));
  CHECK_EQ(static_cast<int>(it.type), 1);
  CHECK_EQ(it.item, 0x11223344u);
  CHECK_EQ(static_cast<int>(it.slot), 2);
  CHECK_EQ(static_cast<int>(it.slot2), 255);
  CHECK_HEX(gms::BuildItemTransactionResult(0, gms::kItemPurchase, 0x11223344, 2, true), "c30001443322110201");
}

// D7b: the client's purchase as ItemShopUI sends it (GameClient_SendItemTransaction(1, item, slot, 0, 0xFF)), here
// Basic_Life.Item (FNV-1 of "basic_life.item" = 0xB22758B1) with "no slot" (11): LabsPlayer_CheckPurchase picks one.
GMS_TEST(CommandsItemTransactionRoundTrip) {
  gms::ItemTransactionRequest r;
  r.type = gms::kItemPurchase;
  r.item = 0xB22758B1;
  r.slot = gms::kItemSlotNone;
  r.item2 = 0;
  r.slot2 = 0xFF;
  const Bytes m = gms::BuildItemTransaction(r);
  CHECK_HEX(m, "c301b15827b20b00000000ff");
  gms::ItemTransactionRequest back;
  CHECK(gms::ParseItemTransaction(m.data() + 1, m.size() - 1, &back));
  CHECK_EQ(back.item, 0xB22758B1u);
  CHECK_EQ(static_cast<int>(back.slot), 11);
  CHECK_EQ(gms::DescribeItemTransaction(back),
           std::string("purchase: item 0xb22758b1 slot 11, item 2 0x00000000 slot 255"));
  CHECK(!gms::ParseItemTransaction(m.data() + 1, 10, &back));
  // the answer for slot 0: the client's reader repeats the purchase (success 1)
  const Bytes a = gms::BuildItemTransactionResult(0, gms::kItemPurchase, 0xB22758B1, 0, true);
  CHECK_HEX(a, "c30001b15827b20001");
  CHECK_EQ(gms::DescribeCommandAnswer(a),
           std::string("ItemTransaction answer: player 0 purchase, item 0xb22758b1 slot 0, success 1"));
  // 3 is "use" (LabsPlayer_UseItem 0x00914160), not "undo"
  CHECK_EQ(std::string(gms::ItemTransactionName(gms::kItemUse)), std::string("use"));
  CHECK_EQ(std::string(gms::ItemTransactionName(9)), std::string("?"));
}

// D15: the host's check that a transaction the binary reported as done changed the player (else it answers success 0)
GMS_TEST(CommandsItemTransactionTookEffect) {
  gms::InventoryState a;
  a.gold = 500.f;
  a.spellPoints = 1;
  gms::InventoryState b = a;
  CHECK(!gms::ItemTransactionTookEffect(gms::kItemPurchase, a, b));  // nothing changed: PurchaseItem's assert path
  CHECK(!gms::ItemTransactionTookEffect(gms::kItemSell, a, b));
  CHECK(!gms::ItemTransactionTookEffect(gms::kItemConsume, a, b));
  CHECK(!gms::ItemTransactionTookEffect(gms::kItemSwap, a, b));
  CHECK(gms::ItemTransactionTookEffect(gms::kItemUse, a, b));  // an active without a consumable changes no slot
  b.gold = 120.f;  // d7b_run1: Basic_Life (380) into slot 0
  b.item[0] = 0xB22758B1;
  b.count[0] = 1;
  CHECK(gms::ItemTransactionTookEffect(gms::kItemPurchase, a, b));
  gms::InventoryState spell = a;  // a spell: no gold, a spell point and slot 6
  spell.spellPoints = 0;
  spell.item[6] = 0x12345678;
  spell.count[6] = 1;
  CHECK(gms::ItemTransactionTookEffect(gms::kItemPurchase, a, spell));
  gms::InventoryState upgraded = b;  // an upgrade in place: the slot's item changes, the count stays 1
  upgraded.item[0] = 0x0BADF00D;
  upgraded.gold = 0.f;
  CHECK(gms::ItemTransactionTookEffect(gms::kItemPurchase, b, upgraded));
  gms::InventoryState swapped = spell;  // a swap of slots 6 and 7
  swapped.item[7] = spell.item[6];
  swapped.count[7] = 1;
  swapped.item[6] = 0;
  swapped.count[6] = 0;
  CHECK(gms::ItemTransactionTookEffect(gms::kItemSwap, spell, swapped));
  gms::InventoryState other = spell;  // a change outside the spell slots is no swap
  other.item[0] = 1;
  CHECK(!gms::ItemTransactionTookEffect(gms::kItemSwap, spell, other));
}

// D7b, d7b_run3: the live client's own message for its item key 4 (posted VK '4') with a healing potion in slot 9
// (GameplayControls_UseItemKey 0x0069A270 -> GameClient_SendItemTransaction(3, item, slot, 0, 0xFF)).
GMS_TEST(CommandsLiveUsePotion) {
  const Bytes m = FromHex("c30347f9a0110900000000ff");
  gms::ItemTransactionRequest r;
  CHECK(gms::ParseItemTransaction(m.data() + 1, m.size() - 1, &r));
  CHECK_EQ(static_cast<int>(r.type), static_cast<int>(gms::kItemUse));
  CHECK_EQ(r.item, 0x11A0F947u);  // Consumable_Potion_HealingPotion.Item
  CHECK_EQ(static_cast<int>(r.slot), 9);
  CHECK_EQ(r.item2, 0u);
  CHECK_EQ(static_cast<int>(r.slot2), 255);
  CHECK_EQ(gms::Hex(gms::BuildItemTransaction(r), 40), gms::Hex(m, 40));
  CHECK_HEX(gms::BuildItemTransactionResult(0, gms::kItemUse, r.item, r.slot, true), "c3000347f9a0110901");
}

// D7b: the voice lines' ids (the quick-chat keys at 0x0069CA26.. and the chat commands) and their ClientFeedbackDefs.
GMS_TEST(CommandsVoiceLines) {
  int known = 0;
  for (uint32_t id = 0; id < 20; ++id)
    if (gms::FindVoiceLine(id)) ++known;
  CHECK_EQ(known, 12);  // 0-5, 8-13; 6 and 7 are sent by nothing
  CHECK(gms::FindVoiceLine(6) == nullptr);
  CHECK(gms::FindVoiceLine(7) == nullptr);
  const gms::VoiceLine* charge = gms::FindVoiceLine(0);
  CHECK(charge && charge->team);
  CHECK_EQ(std::string(charge->feedbackDef), std::string("PlayerCommCharge.ClientFeedbackDef"));
  const gms::VoiceLine* laugh = gms::FindVoiceLine(9);
  CHECK(laugh && !laugh->team);
  CHECK_EQ(std::string(laugh->feedbackDef), std::string("PlayerCommLaugh.ClientFeedbackDef"));
  const gms::VoiceLine* missing = gms::FindVoiceLine(13);
  CHECK(missing && missing->team);
  CHECK_EQ(std::string(gms::FindVoiceLine(10)->feedbackDef), std::string(gms::kFeedbackTaunt));
  gms::ActionCommand c;
  c.type = gms::kCmdVoiceLine;
  c.seq = 4;
  c.caster = 0xA6710135;
  c.value = 4;
  CHECK_EQ(gms::DescribeActionCommand(c), std::string("type 10 voice line, seq 4, caster 0xa6710135: line 4 (thank you)"));
}

GMS_TEST(CommandsActionCommandResponse) {
  // kind 2 as GameSimulator_SendActionCommandFailed (0x008F0110) builds it, with the sequence the host fills in
  Bytes m = gms::BuildActionCommandFailed(0, 0, gms::kCmdAbility, 0x96B845C6, -9999);
  CHECK_EQ(m.size(), static_cast<size_t>(1 + gms::kActionCommandResponseSize));
  gms::ActionCommandResponse r;
  CHECK(gms::ParseActionCommandResponse(m.data() + 1, m.size() - 1, &r));
  CHECK_EQ(static_cast<int>(r.seq), 0);
  CHECK(gms::SetResponseSequence(&m, 6));
  CHECK(gms::ParseActionCommandResponse(m.data() + 1, m.size() - 1, &r));
  CHECK_EQ(static_cast<int>(r.kind), static_cast<int>(gms::kResponseFailed));
  CHECK_EQ(static_cast<int>(r.seq), 6);
  CHECK_EQ(r.commandType, 3u);
  CHECK_EQ(r.guid, 0x96B845C6u);
  CHECK_EQ(r.result, -9999);
  CHECK_EQ(gms::DescribeCommandAnswer(m),
           std::string("ActionCommandResponse failed: player 0, seq 6, command type 3, guid 0x96b845c6, result -9999"));
  Bytes notB4 = FromHex("9b0002");
  CHECK(!gms::SetResponseSequence(&notB4, 1));
  CHECK_EQ(gms::DescribeCommandAnswer(FromHex("9b00020000404100000cc200000000")),
           std::string("LocomotionCommand subtype 0, seq 2, pos (12.00, -35.00, 0.00)"));
}

// GUARDCORE: [host] test_lua's items, one call or a repeat ("<from>-<to>/<step>:<file>").
GMS_TEST(TestLuaItems) {
  using gms::commands::ParseTestLuaItem;
  using gms::commands::TimedLuaItem;
  std::vector<TimedLuaItem> v;
  CHECK(ParseTestLuaItem("20:dgsim_probe.lua", &v));
  CHECK_EQ(v.size(), static_cast<size_t>(1));
  CHECK_EQ(v[0].at, 20.0);
  CHECK(v[0].file == "dgsim_probe.lua");
  v.clear();
  CHECK(ParseTestLuaItem("20-30/2:dgsim_gcore.lua", &v));  // 20, 22, ..., 30: both ends included
  CHECK_EQ(v.size(), static_cast<size_t>(6));
  CHECK_EQ(v[5].at, 30.0);
  CHECK(v[3].file == "dgsim_gcore.lua");
  v.clear();
  CHECK(ParseTestLuaItem("20-29/2:x.lua", &v));  // the last step that fits: 28
  CHECK_EQ(v.size(), static_cast<size_t>(5));
  CHECK_EQ(v.back().at, 28.0);
  v.clear();
  CHECK(ParseTestLuaItem("1.5-2.5/0.5:x.lua", &v));
  CHECK_EQ(v.size(), static_cast<size_t>(3));
  v.clear();
  CHECK(!ParseTestLuaItem("C:/probes/p.lua", &v));  // no number before the colon
  CHECK(ParseTestLuaItem("25:C:/probes/p.lua", &v));  // an absolute path after the time
  CHECK(v.size() == 1 && v[0].file == "C:/probes/p.lua");
  v.clear();
  CHECK(!ParseTestLuaItem("20", &v));
  CHECK(!ParseTestLuaItem(":x.lua", &v));
  CHECK(!ParseTestLuaItem("20:", &v));
  CHECK(!ParseTestLuaItem("30-20/2:x.lua", &v));
  CHECK(!ParseTestLuaItem("20-30/0:x.lua", &v));
  CHECK(!ParseTestLuaItem("20-30:x.lua", &v));
  CHECK(!ParseTestLuaItem("0-100000/1:x.lua", &v));  // more than kMaxTestLuaRepeats
  CHECK(v.empty());
}
