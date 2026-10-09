#include "core/protocol/commands.h"

#include <cstdio>

#include "core/protocol/wire.h"

namespace gms {

namespace {

Vec3 GetVec3(const uint8_t* p) {
  Vec3 v;
  v.x = GetF32LE(p);
  v.y = GetF32LE(p + 4);
  v.z = GetF32LE(p + 8);
  return v;
}

void PutVec3(Bytes& b, const Vec3& v) {
  PutF32LE(b, v.x);
  PutF32LE(b, v.y);
  PutF32LE(b, v.z);
}

std::string Fmt3(const Vec3& v) {
  char buf[64];
  std::snprintf(buf, sizeof buf, "(%.2f, %.2f, %.2f)", v.x, v.y, v.z);
  return buf;
}

const char* SlotName(int32_t slot) {
  switch (slot) {
    case 0: return "basic";
    case 1: return "ward";
    case 2: return "passive";
    case 3: return "Q";
    case 4: return "W";
    case 5: return "E";
    case 6: return "R";
    default: return slot >= 7 && slot <= 11 ? "item" : "?";
  }
}

// GMS_PROTOCOL.md *Voice lines and emotes (D7b)*: the ids of the quick-chat keys and chat commands, the defs in the
// order of their static references (Charge 0x019E207C ... Missing 0x019E2100, Dance 0x019E210C).
const VoiceLine kVoiceLines[] = {
    {0, "charge", "PlayerCommCharge.ClientFeedbackDef", true},
    {1, "care", "PlayerCommCare.ClientFeedbackDef", true},
    {2, "no", "PlayerCommNo.ClientFeedbackDef", true},
    {3, "sorry", "PlayerCommSorry.ClientFeedbackDef", true},
    {4, "thank you", "PlayerCommThankYou.ClientFeedbackDef", true},
    {5, "yes", "PlayerCommYes.ClientFeedbackDef", true},
    {8, "compliment", "PlayerCommCompliment.ClientFeedbackDef", false},
    {9, "laugh", "PlayerCommLaugh.ClientFeedbackDef", false},
    {10, "taunt", "PlayerCommTaunt.ClientFeedbackDef", false},
    {11, "joke", "PlayerCommJoke.ClientFeedbackDef", false},
    {12, "flirt", "PlayerCommFlirt.ClientFeedbackDef", false},
    {13, "missing", "PlayerCommMissing.ClientFeedbackDef", true},
};

}  // namespace

const char* ItemTransactionName(uint8_t t) {
  switch (t) {
    case kItemPurchase: return "purchase";
    case kItemSell: return "sell";
    case kItemUse: return "use";
    case kItemConsume: return "consume";
    case kItemSwap: return "swap";
    default: return "?";
  }
}

const VoiceLine* FindVoiceLine(uint32_t id) {
  for (const VoiceLine& v : kVoiceLines)
    if (v.id == id) return &v;
  return nullptr;
}

int ActionCommandDataSize(uint8_t type) {
  switch (type) {
    case kCmdMove:
    case kCmdStopMoving:
    case kCmdAttackMove: return 0x18;
    case kCmdAbility: return 0x2C;
    case kCmdInteract:
    case kCmdVoiceLine: return 4;
    case kCmdPing: return 0x14;
    case kCmdDance:
    case kCmdTaunt:
    case kCmdStop:
    case kCmdHold: return 0;
    default: return -1;
  }
}

const char* ActionCommandName(uint8_t type) {
  switch (type) {
    case kCmdMove: return "move";
    case kCmdStopMoving: return "stop moving";
    case kCmdAbility: return "ability";
    case kCmdInteract: return "interact";
    case kCmdDance: return "dance";
    case kCmdTaunt: return "taunt";
    case kCmdPing: return "ping";
    case kCmdStop: return "stop";
    case kCmdAttackMove: return "attack-move";
    case kCmdVoiceLine: return "voice line";
    case kCmdHold: return "hold position";
    default: return "?";
  }
}

bool ParseActionCommand(const uint8_t* body, size_t n, ActionCommand* out, std::string* error) {
  char buf[96];
  if (n < kActionCommandHeader) {
    std::snprintf(buf, sizeof buf, "short header (%zu B)", n);
    if (error) *error = buf;
    return false;
  }
  ActionCommand c;
  c.type = body[0];
  c.seq = body[1];
  c.caster = GetU32LE(body + 4);
  const int want = ActionCommandDataSize(c.type);
  if (want < 0) {
    std::snprintf(buf, sizeof buf, "unknown command type %u", c.type);
    if (error) *error = buf;
    return false;
  }
  if (n - kActionCommandHeader != static_cast<size_t>(want)) {
    std::snprintf(buf, sizeof buf, "type %u: data %zu B, expected %d", c.type, n - kActionCommandHeader, want);
    if (error) *error = buf;
    return false;
  }
  const uint8_t* d = body + kActionCommandHeader;
  switch (c.type) {
    case kCmdMove:
    case kCmdAttackMove:
      c.pos = GetVec3(d + 4);
      c.moveFlags = GetU32LE(d + 0x14);
      break;
    case kCmdAbility:
      c.target = GetU32LE(d);
      c.aim = GetVec3(d + 4);
      c.targetPos = GetVec3(d + 0x10);
      c.slot = static_cast<int32_t>(GetU32LE(d + 0x1C));
      c.guid = GetU32LE(d + 0x20);
      c.keepAttacking = GetU32LE(d + 0x24);
      c.walkUp = d[0x28];
      c.reactivation = d[0x29];
      break;
    case kCmdInteract:
      c.target = GetU32LE(d);
      break;
    case kCmdPing:
      c.pos = GetVec3(d);
      c.target = GetU32LE(d + 0xC);
      c.value = GetU32LE(d + 0x10);
      break;
    case kCmdVoiceLine:
      c.value = GetU32LE(d);
      break;
    default:
      break;
  }
  *out = c;
  return true;
}

Bytes BuildActionCommand(const ActionCommand& c) {
  Bytes m{kGmsActionCommandMsgs, c.type, c.seq, 0, 0};
  PutU32LE(m, c.caster);
  switch (c.type) {
    case kCmdMove:
    case kCmdAttackMove:
      PutU32LE(m, 0);
      PutVec3(m, c.pos);
      PutU32LE(m, 0);
      PutU32LE(m, c.moveFlags);
      break;
    case kCmdStopMoving:
      m.resize(m.size() + 0x18, 0);
      break;
    case kCmdAbility:
      PutU32LE(m, c.target);
      PutVec3(m, c.aim);
      PutVec3(m, c.targetPos);
      PutU32LE(m, static_cast<uint32_t>(c.slot));
      PutU32LE(m, c.guid);
      PutU32LE(m, c.keepAttacking);
      PutU8(m, c.walkUp);
      PutU8(m, c.reactivation);
      PutU16LE(m, 0);
      break;
    case kCmdInteract:
      PutU32LE(m, c.target);
      break;
    case kCmdPing:
      PutVec3(m, c.pos);
      PutU32LE(m, c.target);
      PutU32LE(m, c.value);
      break;
    case kCmdVoiceLine:
      PutU32LE(m, c.value);
      break;
    default:
      break;
  }
  return m;
}

std::string DescribeActionCommand(const ActionCommand& c) {
  char head[96];
  std::snprintf(head, sizeof head, "type %u %s, seq %u, caster 0x%08x", c.type, ActionCommandName(c.type), c.seq,
                c.caster);
  char buf[256];
  switch (c.type) {
    case kCmdMove:
    case kCmdAttackMove:
      std::snprintf(buf, sizeof buf, ": to %s, flags %u", Fmt3(c.pos).c_str(), c.moveFlags);
      break;
    case kCmdAbility:
      if (c.slot != -1)
        std::snprintf(buf, sizeof buf, ": slot %d %s", c.slot, SlotName(c.slot));
      else
        std::snprintf(buf, sizeof buf, ": guid 0x%08x%s", c.guid, c.guid == kGuidReturnToBase ? " (return to base)" : "");
      {
        char more[200];
        std::snprintf(more, sizeof more, ", target 0x%08x, aim %s, target pos %s, repeat %u, approach %u, "
                      "reactivation %u", c.target, Fmt3(c.aim).c_str(), Fmt3(c.targetPos).c_str(), c.keepAttacking,
                      c.walkUp, c.reactivation);
        return std::string(head) + buf + more;
      }
    case kCmdInteract:
      std::snprintf(buf, sizeof buf, ": object 0x%08x", c.target);
      break;
    case kCmdPing:
      std::snprintf(buf, sizeof buf, ": at %s, target 0x%08x, ping type %u", Fmt3(c.pos).c_str(), c.target, c.value);
      break;
    case kCmdVoiceLine: {
      const VoiceLine* v = FindVoiceLine(c.value);
      std::snprintf(buf, sizeof buf, ": line %u (%s)", c.value, v ? v->name : "unknown");
      break;
    }
    default:
      buf[0] = 0;
      break;
  }
  return std::string(head) + buf;
}

bool ParseAbilityRankUp(const uint8_t* body, size_t n, uint32_t* slot) {
  if (n != 4) return false;
  *slot = GetU32LE(body);
  return true;
}

bool ParseItemTransaction(const uint8_t* body, size_t n, ItemTransactionRequest* out) {
  if (n != 11) return false;
  out->type = body[0];
  out->item = GetU32LE(body + 1);
  out->slot = body[5];
  out->item2 = GetU32LE(body + 6);
  out->slot2 = body[10];
  return true;
}

Bytes BuildItemTransaction(const ItemTransactionRequest& r) {
  Bytes m{kGmsItemTransaction, r.type};
  PutU32LE(m, r.item);
  PutU8(m, r.slot);
  PutU32LE(m, r.item2);
  PutU8(m, r.slot2);
  return m;
}

std::string DescribeItemTransaction(const ItemTransactionRequest& r) {
  char buf[160];
  std::snprintf(buf, sizeof buf, "%s: item 0x%08x slot %u, item 2 0x%08x slot %u", ItemTransactionName(r.type), r.item,
                r.slot, r.item2, r.slot2);
  return buf;
}

Bytes BuildItemTransactionResult(uint8_t player, uint8_t type, uint32_t item, uint8_t slot, bool success) {
  Bytes m{kGmsItemTransaction, player, type};
  PutU32LE(m, item);
  PutU8(m, slot);
  PutU8(m, success ? 1 : 0);
  return m;
}

bool ItemTransactionTookEffect(uint8_t type, const InventoryState& before, const InventoryState& after) {
  auto slotsChanged = [&](int from, int to) {
    for (int s = from; s < to; ++s)
      if (before.item[s] != after.item[s] || before.count[s] != after.count[s]) return true;
    return false;
  };
  switch (type) {
    case kItemPurchase:
    case kItemSell:
    case kItemConsume:
      return before.gold != after.gold || before.spellPoints != after.spellPoints || slotsChanged(0, kInventorySlots);
    case kItemSwap:
      return slotsChanged(6, 9);
    default:
      return true;
  }
}

Bytes BuildMiniMapPing(uint32_t pinger, uint32_t target, const Vec3& pos, uint8_t pingType) {
  Bytes m{kGmsMiniMapPing};
  PutU32LE(m, pinger);
  PutU32LE(m, target);
  PutVec3(m, pos);
  PutU8(m, pingType);
  return m;
}

bool ParseActionCommandResponse(const uint8_t* body, size_t n, ActionCommandResponse* out) {
  if (n != kActionCommandResponseSize) return false;
  out->commandType = GetU32LE(body);
  out->player = body[4];
  out->seq = body[5];
  out->kind = body[6];
  out->guid = GetU32LE(body + 8);
  out->result = static_cast<int32_t>(GetU32LE(body + 0x40));
  return true;
}

Bytes BuildActionCommandFailed(uint8_t player, uint8_t seq, uint32_t commandType, uint32_t guid, int32_t result) {
  Bytes m{kGmsActionCommandResponse};
  m.resize(1 + kActionCommandResponseSize, 0);
  uint8_t* b = m.data() + 1;
  b[0] = static_cast<uint8_t>(commandType);
  b[1] = static_cast<uint8_t>(commandType >> 8);
  b[2] = static_cast<uint8_t>(commandType >> 16);
  b[3] = static_cast<uint8_t>(commandType >> 24);
  b[4] = player;
  b[5] = seq;
  b[6] = kResponseFailed;
  for (int i = 0; i < 4; ++i) b[8 + i] = static_cast<uint8_t>(guid >> (8 * i));
  for (int i = 0; i < 4; ++i) b[0x40 + i] = static_cast<uint8_t>(static_cast<uint32_t>(result) >> (8 * i));
  return m;
}

bool SetResponseSequence(Bytes* msg, uint8_t seq) {
  if (!msg || msg->size() != 1 + kActionCommandResponseSize || (*msg)[0] != kGmsActionCommandResponse) return false;
  (*msg)[1 + 5] = seq;
  return true;
}

std::string DescribeCommandAnswer(const Bytes& msg) {
  if (msg.empty()) return std::string();
  const uint8_t* b = msg.data() + 1;
  const size_t n = msg.size() - 1;
  char buf[200];
  switch (msg[0]) {
    case kGmsActionCommandResponse: {
      ActionCommandResponse r;
      if (!ParseActionCommandResponse(b, n, &r)) return "ActionCommandResponse (bad size)";
      const char* kind = r.kind == kResponseAbilityStarted ? "ability started"
                         : r.kind == kResponseFailed       ? "failed"
                         : r.kind == kResponseAbilityEnded ? "ability ended"
                                                           : "kind ?";
      std::snprintf(buf, sizeof buf, "ActionCommandResponse %s: player %u, seq %u, command type %u, guid 0x%08x, "
                    "result %d", kind, r.player, r.seq, r.commandType, r.guid, r.result);
      return buf;
    }
    case kGmsLocomotionCommand:
      if (n < 2) return "LocomotionCommand (short)";
      if (n >= 14)
        std::snprintf(buf, sizeof buf, "LocomotionCommand subtype %u, seq %u, pos %s", b[0], b[1],
                      Fmt3(GetVec3(b + 2)).c_str());
      else
        std::snprintf(buf, sizeof buf, "LocomotionCommand subtype %u, seq %u, %zu data bytes", b[0], b[1], n - 2);
      return buf;
    case kGmsMiniMapPing:
      if (n != 21) return "MiniMapPing (bad size)";
      std::snprintf(buf, sizeof buf, "MiniMapPing pinger 0x%08x, target 0x%08x, at %s, type %u", GetU32LE(b),
                    GetU32LE(b + 4), Fmt3(GetVec3(b + 8)).c_str(), b[20]);
      return buf;
    case kGmsItemTransaction:
      if (n != 8) return std::string();
      std::snprintf(buf, sizeof buf, "ItemTransaction answer: player %u %s, item 0x%08x slot %u, success %u", b[0],
                    ItemTransactionName(b[1]), GetU32LE(b + 2), b[6], b[7]);
      return buf;
    default:
      return std::string();
  }
}

}  // namespace gms
