#include "core/protocol/check.h"

#include <algorithm>
#include <cmath>
#include <cstdarg>
#include <cstdio>

#include "core/postgame/postgame.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"
#include "core/protocol/wire.h"
#include "core/surrender/surrender.h"

namespace gms::check {

namespace {

// ActiveItems_SM_Map_V5.ItemSet (re/out/assetdata/ItemSet.json, 106 items) without the two debug consumables.
const char* const kShopItemNames[] = {
    "Spell_Blink", "Spell_Stasis", "Spell_Vanquish", "Spell_Deflect", "Spell_Wither", "Spell_Tailwind",
    "Spell_Bastion", "Spell_Exceed", "Spell_Stagger", "Spell_Dispel", "Spell_Blitz", "Spell_Drain",
    "Consumable_Potion_HealingPotion", "Consumable_Flask_Vim", "Consumable_Flask_Vision", "Consumable_Flask_Power",
    "Basic_Life", "Basic_Resilience", "Basic_Will", "Basic_Power", "Basic_Time", "Basic_Hunger", "Advanced_Vigor",
    "Advanced_Purity", "Advanced_Pride", "Advanced_Growth", "Advanced_Form", "Advanced_Integrity",
    "Advanced_Discipline", "Advanced_Conviction", "Advanced_Resolve", "Advanced_Freedom", "Advanced_Might",
    "Advanced_Command", "Advanced_Aggression", "Advanced_Control", "Advanced_Force", "Advanced_Motion",
    "Advanced_Balance", "Advanced_Abolition", "Advanced_Inspiration", "Advanced_Clarity", "Advanced_Energy",
    "Advanced_Desire", "Advanced_Consumption", "Legendary_Prosperity", "Legendary_Defiance", "Legendary_Rampancy",
    "Legendary_Harmony", "Legendary_Devotion", "Legendary_Pestilence", "Legendary_Glory", "Legendary_Vengeance",
    "Legendary_Rebirth", "Legendary_Creation", "Legendary_Vibrance", "Legendary_Preservation", "Legendary_Order",
    "Legendary_Equilibrium", "Legendary_Subjugation", "Legendary_Faith", "Legendary_Retribution",
    "Legendary_Betrayal", "Legendary_Oppression", "Legendary_Strife", "Legendary_Adamance", "Legendary_Valor",
    "Legendary_Supremacy", "Legendary_Wisdom", "Legendary_Justice", "Legendary_Judgement", "Legendary_Rage",
    "Legendary_Destruction", "Legendary_Inevitability", "Legendary_Pursuit", "Legendary_Hostility", "Legendary_Pain",
    "Legendary_Dominance", "Legendary_Divinity", "Legendary_Fluidity", "Legendary_Influence", "Legendary_Impulse",
    "Legendary_Stability", "Legendary_Momentum", "Legendary_Conquest", "Legendary_Ruin", "Legendary_Finesse",
    "Legendary_Decay", "Legendary_Corruption", "Legendary_Furor", "Legendary_Grace", "Legendary_Zeal", "Legendary_Fate",
    "Legendary_Insight", "Legendary_Duress", "Legendary_Potency", "Legendary_Chaos", "Legendary_Resonance",
    "Legendary_Empathy", "Legendary_Hope", "Legendary_Passion", "Legendary_Ambition", "Legendary_Voracity",
    "Legendary_Assimilation",
};

// Level.levelSize of the levels with a game (re/out/assetdata/Level.json).
struct LevelSize {
  const char* name;
  float x, y;
};
const LevelSize kLevelSizes[] = {
    {"SM_Map_v5.Level", 525.434f, 525.283f}, {"SM_Map_v4.Level", 438.308f, 438.4f},
    {"MM_Map_v3.Level", 580.478f, 580.478f}, {"DGTut_Map_v1.Level", 503.966f, 503.965f},
    {"TA_Map_v1.Level", 302.802f, 302.802f},
};
constexpr float kBoundsMargin = 2.0f;

constexpr uint8_t kInventorySlotNone = 0x0B;  // kNumInventoryItemSlots: "no slot" (a purchase lets the server pick)
constexpr uint8_t kSecondSlotNone = 0xFF;     // the purchase's second slot as the client sends it (census: always 255)

Result Bad(Verdict v, const char* fmt, ...) {
  Result r;
  r.verdict = v;
  char buf[200];
  va_list ap;
  va_start(ap, fmt);
  std::vsnprintf(buf, sizeof buf, fmt, ap);
  va_end(ap);
  r.reason = buf;
  return r;
}

bool Finite(const Vec3& v) { return std::isfinite(v.x) && std::isfinite(v.y) && std::isfinite(v.z); }

Result CheckPoint(const Vec3& v, const Bounds& b, const char* what) {
  if (!Finite(v)) return Bad(Verdict::kInvalid, "%s is not finite", what);
  if (!b.Contains(v.x, v.y, v.z))
    return Bad(Verdict::kInvalid, "%s (%.1f, %.1f, %.1f) is outside the level", what, v.x, v.y, v.z);
  return Result();
}

Result CheckObjectId(uint32_t id, bool zeroOk, const char* what) {
  if (id == 0) return zeroOk ? Result() : Bad(Verdict::kInvalid, "%s is 0", what);
  if (!PlausibleObjectId(id))
    return Bad(Verdict::kInvalid, "%s 0x%08x is no object id (slot %u >= %u)", what, id, id & 0xFFFFu,
               kMaxObjectSlots);
  return Result();
}

bool InventorySlot(uint8_t s) { return s < kInventorySlotNone; }

Result CheckActionCommand(const uint8_t* body, size_t n, const Bounds& b) {
  ActionCommand c;
  std::string error;
  if (!ParseActionCommand(body, n, &c, &error)) return Bad(Verdict::kMalformed, "ActionCommandMsgs: %s", error.c_str());
  Result r = CheckObjectId(c.caster, false, "the caster");
  if (!r.ok()) return r;
  switch (c.type) {
    case kCmdMove:
    case kCmdAttackMove:
      if ((r = CheckPoint(c.pos, b, "the move's point")).verdict != Verdict::kOk) return r;
      if (c.moveFlags > 1) return Bad(Verdict::kInvalid, "move flags %u (0 click, 1 held)", c.moveFlags);
      return Result();
    case kCmdAbility:
      if (c.slot < -1 || c.slot > 11) return Bad(Verdict::kInvalid, "ability slot %d (-1, 0..11)", c.slot);
      if (c.slot == -1 && c.guid != kGuidReturnToBase)
        return Bad(Verdict::kInvalid, "ability by GUID 0x%08x (only return to base 0x%08x is cast by GUID)", c.guid,
                   kGuidReturnToBase);
      if (c.slot != -1 && c.guid != 0)
        return Bad(Verdict::kInvalid, "slot %d and GUID 0x%08x both set", c.slot, c.guid);
      if (c.keepAttacking > 1 || c.walkUp > 1 || c.reactivation > 1)
        return Bad(Verdict::kInvalid, "flags keep attacking %u, walk up %u, reactivation %u (0 or 1)", c.keepAttacking,
                   c.walkUp, c.reactivation);
      if ((r = CheckObjectId(c.target, true, "the target")).verdict != Verdict::kOk) return r;
      if ((r = CheckPoint(c.aim, b, "the aim point")).verdict != Verdict::kOk) return r;
      if ((r = CheckPoint(c.targetPos, b, "the target position")).verdict != Verdict::kOk) return r;
      return Result();
    case kCmdInteract:
      // D7b: no noun of build 854 has a cInteractableDef, so the client's right-click never sends it
      return Bad(Verdict::kInvalid, "interact (no interactable object exists in build 854)");
    case kCmdPing:
      if ((r = CheckPoint(c.pos, b, "the ping's point")).verdict != Verdict::kOk) return r;
      if ((r = CheckObjectId(c.target, true, "the ping's target")).verdict != Verdict::kOk) return r;
      if (c.value > 4) return Bad(Verdict::kInvalid, "ping type %u (0-4)", c.value);
      return Result();
    case kCmdVoiceLine:
      if (!FindVoiceLine(c.value)) return Bad(Verdict::kInvalid, "voice line %u (0-5, 8-13)", c.value);
      return Result();
    default:  // 2 stop moving (its data is never written), 5 dance, 6 taunt, 8 stop, 11 hold
      return Result();
  }
}

Result CheckItemTransaction(const uint8_t* body, size_t n) {
  ItemTransactionRequest t;
  if (!ParseItemTransaction(body, n, &t)) return Bad(Verdict::kMalformed, "ItemTransaction of %zu B (11)", n);
  auto itemOrNone = [](uint32_t id) { return id == 0 || IsShopItem(id); };
  switch (t.type) {
    case kItemPurchase:
      if (!IsShopItem(t.item)) return Bad(Verdict::kInvalid, "purchase of 0x%08x, not in the shop's table", t.item);
      if (!InventorySlot(t.slot) && t.slot != kInventorySlotNone)
        return Bad(Verdict::kInvalid, "purchase into slot %u (0-10, 11 = any)", t.slot);
      if (!itemOrNone(t.item2)) return Bad(Verdict::kInvalid, "purchase's second item 0x%08x", t.item2);
      if (!InventorySlot(t.slot2) && t.slot2 != kInventorySlotNone && t.slot2 != kSecondSlotNone)
        return Bad(Verdict::kInvalid, "purchase's second slot %u", t.slot2);
      return Result();
    case kItemSell:
    case kItemUse:
    case kItemConsume:
      if (!IsShopItem(t.item)) return Bad(Verdict::kInvalid, "%s of 0x%08x, no shop item", ItemTransactionName(t.type), t.item);
      if (!InventorySlot(t.slot)) return Bad(Verdict::kInvalid, "%s from slot %u (0-10)", ItemTransactionName(t.type), t.slot);
      if (!itemOrNone(t.item2)) return Bad(Verdict::kInvalid, "%s: second item 0x%08x", ItemTransactionName(t.type), t.item2);
      return Result();
    case kItemSwap:
      if (!itemOrNone(t.item) || !itemOrNone(t.item2))
        return Bad(Verdict::kInvalid, "swap with items 0x%08x, 0x%08x", t.item, t.item2);
      if (!InventorySlot(t.slot) || !InventorySlot(t.slot2))
        return Bad(Verdict::kInvalid, "swap of slots %u and %u (0-10)", t.slot, t.slot2);
      return Result();
    default:
      return Bad(Verdict::kInvalid, "item transaction type %u (1-5)", t.type);
  }
}

Result CheckShaperSelect(const uint8_t* body, size_t n) {
  ShaperSelectRequest q;
  if (!ParseShaperSelectRequest(body, n, &q))
    return Bad(Verdict::kMalformed, "ShaperSelectMsgs %s (%zu B): no client subtype of that size",
               n ? std::to_string(body[0]).c_str() : "-", n);
  switch (q.sub) {
    case kShaperPreferredRole:
    case kShaperSelectRole:
    case kShaperLockRole:
      if (q.role >= kNumRoles) return Bad(Verdict::kInvalid, "C8 %02x role %u (0-3)", q.sub, q.role);
      return Result();
    case kShaperSelectVanity:
      if (q.kind != kVanityWard && q.kind != kVanityAnnouncer)
        return Bad(Verdict::kInvalid, "C8 0e vanity kind %u (0 ward, 1 announcer)", q.kind);
      return Result();
    default:  // Nouns, pages and skins: ShaperSelect checks them against the player's own lists (C8 02, 09, 0B)
      return Result();
  }
}

}  // namespace

const char* VerdictName(Verdict v) {
  switch (v) {
    case Verdict::kOk: return "ok";
    case Verdict::kMalformed: return "malformed";
    case Verdict::kInvalid: return "invalid";
    case Verdict::kStale: return "stale";
  }
  return "?";
}

bool Bounds::Contains(float x, float y, float z) const {
  return x >= minX && x <= maxX && y >= minY && y <= maxY && z >= minZ && z <= maxZ;
}

Bounds BoundsForLevel(uint32_t levelAssetId) {
  Bounds b;
  for (const LevelSize& l : kLevelSizes) {
    if (AssetIdOf(l.name) != levelAssetId) continue;
    b.maxX = l.x / 2 + kBoundsMargin;
    b.minX = -b.maxX;
    b.maxY = l.y / 2 + kBoundsMargin;
    b.minY = -b.maxY;
    break;
  }
  return b;
}

const std::vector<uint32_t>& ShopItems() {
  static const std::vector<uint32_t> items = [] {
    std::vector<uint32_t> v;
    for (const char* name : kShopItemNames) v.push_back(AssetIdOf(std::string(name) + ".Item"));
    std::sort(v.begin(), v.end());
    return v;
  }();
  return items;
}

bool IsShopItem(uint32_t assetId) {
  const std::vector<uint32_t>& v = ShopItems();
  return assetId != 0 && std::binary_search(v.begin(), v.end(), assetId);
}

Result CheckFormat(uint8_t wire, const uint8_t* body, size_t n, const Bounds& bounds) {
  switch (wire) {
    case kGmsHelloReq: {
      HelloReq h;
      if (!ParseHelloReq(body, n, &h)) return Bad(Verdict::kMalformed, "HelloReq of %zu B (16)", n);
      if (h.userId == 0) return Bad(Verdict::kInvalid, "HelloReq with user id 0");
      return Result();
    }
    case kGmsPlayerStatusUpdate: {
      PlayerStatusReport r;
      if (!ParsePlayerStatusUpdate(body, n, &r)) return Bad(Verdict::kMalformed, "PlayerStatusUpdate of %zu B (8)", n);
      if (r.flags & ~kClientStatusBits)
        return Bad(Verdict::kInvalid, "status bits 0x%x that only the GMS sets", r.flags & ~kClientStatusBits);
      if (!std::isfinite(r.progress) || r.progress < 0.f || r.progress > 1.f)
        return Bad(Verdict::kInvalid, "progress %g (0..1)", static_cast<double>(r.progress));
      return Result();
    }
    case kGmsActionCommandMsgs: return CheckActionCommand(body, n, bounds);
    case kGmsAbilityRankUp: {
      uint32_t slot = 0;
      if (!ParseAbilityRankUp(body, n, &slot)) return Bad(Verdict::kMalformed, "AbilityRankUp of %zu B (4)", n);
      if (slot < 3 || slot > 6) return Bad(Verdict::kInvalid, "AbilityRankUp slot %u (3-6)", slot);
      return Result();
    }
    case kGmsItemTransaction: return CheckItemTransaction(body, n);
    case kGmsClockSync: {
      int64_t ms = 0;
      if (!ParseClockSync(body, n, &ms)) return Bad(Verdict::kMalformed, "ClockSync of %zu B (8)", n);
      if (ms < 0) return Bad(Verdict::kInvalid, "ClockSync client clock %lld ms", static_cast<long long>(ms));
      return Result();
    }
    case kGmsBattlegroundsPreGameMsgs:
      if (n != 1 || body[0] != kPreGameAllLoaded)
        return Bad(Verdict::kMalformed, "BattlegroundsPreGameMsgs %s (the client sends only 04)", Hex(body, n, 8).c_str());
      return Result();
    case kGmsBattlegroundsPostGameMsgs: {
      postgame::ClientMessage m;
      if (!postgame::ParseClientMessage(body, n, &m))
        return Bad(Verdict::kMalformed, "BattlegroundsPostGameMsgs %s (01, or 03 + u16 mask)", Hex(body, n, 8).c_str());
      if (m.sub == postgame::kSubVotes && (m.votes & ~0x3FFu))
        return Bad(Verdict::kInvalid, "karma vote mask 0x%04x (players 0-9)", m.votes);
      return Result();
    }
    case kGmsShaperSelectMsgs: return CheckShaperSelect(body, n);
    case kGmsSurrenderMsgs: {
      if (n != 1) return Bad(Verdict::kMalformed, "SurrenderMsgs of %zu B (1)", n);
      bool yes = false;
      if (!surrender::ParseClientVote(body, n, &yes)) return Bad(Verdict::kInvalid, "surrender vote %u (1, 2)", body[0]);
      return Result();
    }
    case kGmsFriendInfo:
      if (n != 2) return Bad(Verdict::kMalformed, "FriendInfo of %zu B (2)", n);
      if (body[1] & 0x80) return Bad(Verdict::kInvalid, "friend mask 0x%04x (players 0-14)", body[0] | body[1] << 8);
      return Result();
    case kGmsDemoModeMsgs:
      if (n != 1) return Bad(Verdict::kMalformed, "DemoModeMsgs of %zu B (1)", n);
      if (body[0] != 1) return Bad(Verdict::kInvalid, "DemoModeMsgs %02x (the lobby exit sends 01)", body[0]);
      return Result();
    case kGmsPauseMsgs:
      if (n != 2 || body[0] != 0) return Bad(Verdict::kMalformed, "PauseMsgs %s (00 + u8)", Hex(body, n, 8).c_str());
      if (body[1] > 1) return Bad(Verdict::kInvalid, "pause toggle %u (0, 1)", body[1]);
      return Result();
    case kGmsCheatMessage: return Bad(Verdict::kInvalid, "CheatMessage (cheats are off on the match host)");
    case kGmsDebugPing: return Bad(Verdict::kInvalid, "DebugPing (the GMS sends none for a client to answer)");
    default:
      if (wire < kGmsFirstWireByte) return Bad(Verdict::kMalformed, "byte %02x is no GMS message", wire);
      return Bad(Verdict::kMalformed, "%s (%02x) goes from the GMS to the client only", GmsName(wire), wire);
  }
}

Result CheckWorld(uint8_t wire, const uint8_t* body, size_t n, ConnId conn, IWorld& world) {
  if (wire != kGmsActionCommandMsgs && wire != kGmsAbilityRankUp && wire != kGmsItemTransaction) return Result();
  SenderView s;
  if (!world.Sender(conn, &s) || !s.player)
    return Bad(Verdict::kInvalid, "%s from a connection without a player (a spectator, or not seated)", GmsName(wire));
  if (wire == kGmsActionCommandMsgs) {
    ActionCommand c;
    std::string error;
    if (!ParseActionCommand(body, n, &c, &error)) return Bad(Verdict::kMalformed, "%s", error.c_str());
    if (!s.creature) return Bad(Verdict::kStale, "a command before the player's creature exists");
    if (c.caster != s.creature)
      return Bad(Verdict::kInvalid, "caster 0x%08x is not the player's creature 0x%08x", c.caster, s.creature);
    const bool hasTarget = (c.type == kCmdAbility || c.type == kCmdPing) && c.target && c.target != s.creature;
    if (hasTarget) {
      switch (world.Target(conn, c.target)) {
        case TargetView::kGone: return Bad(Verdict::kStale, "target 0x%08x no longer exists", c.target);
        case TargetView::kUnknown:
          return Bad(Verdict::kInvalid, "target 0x%08x was never sent to this player", c.target);
        case TargetView::kHidden: return Bad(Verdict::kStale, "target 0x%08x is out of the player's view", c.target);
        case TargetView::kVisible: break;
      }
    }
    return Result();
  }
  if (wire == kGmsItemTransaction) {
    ItemTransactionRequest t;
    if (!ParseItemTransaction(body, n, &t)) return Bad(Verdict::kMalformed, "ItemTransaction of %zu B", n);
    if (t.type == kItemSell || t.type == kItemUse || t.type == kItemConsume) {
      InventoryView inv;
      if (!world.Inventory(conn, &inv)) return Bad(Verdict::kStale, "the player's inventory can't be read");
      if (t.slot >= 11 || inv.item[t.slot] != t.item || inv.count[t.slot] <= 0)
        return Bad(Verdict::kStale, "%s of 0x%08x: slot %u holds 0x%08x x%d", ItemTransactionName(t.type), t.item,
                   t.slot, t.slot < 11 ? inv.item[t.slot] : 0, t.slot < 11 ? inv.count[t.slot] : 0);
    }
  }
  return Result();
}

// ---- Stats and Guard -----------------------------------------------------------------------------------------------

void Stats::Add(const Stats& s) {
  checked += s.checked;
  ok += s.ok;
  malformed += s.malformed;
  invalid += s.invalid;
  stale += s.stale;
  kicked += s.kicked;
  other += s.other;
  for (const auto& kv : s.badByWire) badByWire[kv.first] += kv.second;
}

std::string Stats::Describe() const {
  char buf[200];
  std::snprintf(buf, sizeof buf, "checked %llu, dropped %llu (malformed %llu, invalid %llu, stale %llu",
                static_cast<unsigned long long>(checked), static_cast<unsigned long long>(Bad()),
                static_cast<unsigned long long>(malformed), static_cast<unsigned long long>(invalid),
                static_cast<unsigned long long>(stale));
  std::string s = buf;
  if (!badByWire.empty()) {
    s += ";";
    for (const auto& kv : badByWire) {
      std::snprintf(buf, sizeof buf, " %02x x%llu", kv.first, static_cast<unsigned long long>(kv.second));
      s += buf;
    }
  }
  std::snprintf(buf, sizeof buf, "), RakNet other %llu, kicked %llu", static_cast<unsigned long long>(other),
                static_cast<unsigned long long>(kicked));
  return s + buf;
}

bool Guard::Count(ConnId conn, double nowMs) {
  if (o_.kickWindowMs <= 0 || o_.kickCount <= 0) return false;
  std::deque<double>& q = recent_[conn];
  q.push_back(nowMs);
  while (!q.empty() && nowMs - q.front() > o_.kickWindowMs) q.pop_front();
  if (static_cast<int>(q.size()) < o_.kickCount) return false;
  recent_.erase(conn);
  ++total_.kicked;
  ++window_.kicked;
  return true;
}

bool Guard::Note(ConnId conn, uint8_t wire, Verdict v, double nowMs) {
  ++total_.checked;
  ++window_.checked;
  switch (v) {
    case Verdict::kOk:
      ++total_.ok;
      ++window_.ok;
      return false;
    case Verdict::kMalformed:
      ++total_.malformed;
      ++window_.malformed;
      break;
    case Verdict::kInvalid:
      ++total_.invalid;
      ++window_.invalid;
      break;
    case Verdict::kStale:
      ++total_.stale;
      ++window_.stale;
      break;
  }
  ++total_.badByWire[wire];
  ++window_.badByWire[wire];
  return CountsTowardsKick(v) && Count(conn, nowMs);
}

bool Guard::NoteOther(ConnId conn, uint8_t id, double nowMs) {
  ++total_.other;
  ++window_.other;
  ++total_.badByWire[id];
  ++window_.badByWire[id];
  return Count(conn, nowMs);
}

bool Guard::ShouldLog() {
  ++logged_;
  return logged_ <= static_cast<uint64_t>(o_.logLimit) || logged_ % 100 == 0;
}

Stats Guard::TakeWindow() {
  Stats s;
  std::swap(s, window_);
  return s;
}

}  // namespace gms::check
