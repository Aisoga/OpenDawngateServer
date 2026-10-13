#include "core/protocol/shaper_select.h"

#include "core/protocol/messages.h"
#include "core/protocol/wire.h"

namespace gms {

ShaperSelectTimers ShaperTimersNormal() { return ShaperSelectTimers{}; }

ShaperSelectTimers ShaperTimersPractice() {
  ShaperSelectTimers t;
  t.pickMs = 300000;
  t.rolePerkMs = 300000;
  return t;
}

namespace {

Bytes Start(uint8_t sub) { return Bytes{kGmsShaperSelectMsgs, sub}; }

}  // namespace

Bytes BuildShaperPhase(uint64_t endServerMs, uint32_t phase) {
  Bytes m = Start(kShaperPhase);
  PutU64LE(m, endServerMs);
  PutU32LE(m, phase);
  return m;
}

Bytes BuildShaperList(const std::vector<ShaperListEntry>& entries) {
  if (entries.empty()) return Bytes();
  Bytes m = Start(kShaperList);
  for (const ShaperListEntry& e : entries) {
    PutU32LE(m, e.noun);
    PutU8(m, e.flags);
    PutU8(m, 0);
    PutU32LE(m, e.offer);
  }
  return m;
}

Bytes BuildShaperRecord(const ShaperRecord& r) {
  Bytes m = Start(kShaperRecord);
  PutU8(m, r.index);
  PutU32LE(m, r.shaper);
  PutU32LE(m, r.skin);
  PutU8(m, r.role);
  PutU32LE(m, r.page);
  PutU8(m, r.flags);
  return m;
}

Bytes BuildShaperRequestResult(uint32_t noun, bool ok) {
  Bytes m = Start(kShaperRequestResult);
  PutU32LE(m, noun);
  PutU8(m, ok ? 1 : 0);
  return m;
}

Bytes BuildShaperLockResult(uint32_t noun) {
  Bytes m = Start(kShaperLockResult);
  PutU32LE(m, noun);
  PutU8(m, 1);
  return m;
}

Bytes BuildRoleToggle(uint8_t gameplayIndex, uint8_t role) {
  Bytes m = Start(kShaperRoleToggle);
  PutU8(m, gameplayIndex);
  PutU8(m, role);
  return m;
}

Bytes BuildPerkPages(const std::vector<uint32_t>& pageIds) {
  Bytes m = Start(kShaperPerkPages);
  PutU8(m, static_cast<uint8_t>(pageIds.size()));
  for (uint32_t id : pageIds) PutU32LE(m, id);
  return m;
}

Bytes BuildSkins(const std::vector<uint32_t>& skinNouns) {
  if (skinNouns.empty()) return Bytes();
  Bytes m = Start(kShaperSkins);
  PutU8(m, static_cast<uint8_t>(skinNouns.size()));
  for (uint32_t noun : skinNouns) {
    PutU32LE(m, noun);
    PutU8(m, 0);
  }
  return m;
}

Bytes BuildVanity(uint8_t kind, uint32_t asset) {
  Bytes m = Start(kShaperVanity);
  PutU8(m, kind);
  PutU32LE(m, asset);
  return m;
}

Bytes BuildPreGameLoadout(uint8_t gameplayIndex, const std::vector<LoadoutAttribute>& attributes,
                          const std::vector<uint32_t>& modifiers) {
  Bytes m{kGmsBattlegroundsPreGameMsgs, 0x02, gameplayIndex, static_cast<uint8_t>(attributes.size())};
  for (const LoadoutAttribute& a : attributes) {
    PutU8(m, a.attribute);
    PutU8(m, a.op);
    PutF32LE(m, a.value);
  }
  PutU8(m, static_cast<uint8_t>(modifiers.size()));
  for (uint32_t id : modifiers) PutU32LE(m, id);
  return m;
}

bool ParseShaperRecord(const uint8_t* p, size_t n, ShaperRecord* out) {
  if (n < 15) return false;
  out->index = p[0];
  out->shaper = GetU32LE(p + 1);
  out->skin = GetU32LE(p + 5);
  out->role = p[9];
  out->page = GetU32LE(p + 10);
  out->flags = p[14];
  return true;
}

namespace {

// Body bytes after the subtype, per client subtype; -1 = not a client subtype.
int RequestSize(uint8_t sub) {
  switch (sub) {
    case kShaperHello:
    case kShaperReady: return 0;
    case kShaperPreferredRole:
    case kShaperSelectRole:
    case kShaperLockRole: return 1;
    case kShaperRequest:
    case kShaperLockIn:
    case kShaperPerkPage:
    case kShaperSelectSkin:
    case kShaperLockSkin: return 4;
    case kShaperSelectVanity: return 5;
    default: return -1;
  }
}

}  // namespace

bool ParseShaperSelectRequest(const uint8_t* body, size_t n, ShaperSelectRequest* out) {
  if (n < 1) return false;
  const int size = RequestSize(body[0]);
  if (size < 0 || n != 1 + static_cast<size_t>(size)) return false;  // exact
  ShaperSelectRequest r;
  r.sub = body[0];
  if (size == 1) r.role = body[1];
  if (size == 4) r.id = GetU32LE(body + 1);
  if (size == 5) {
    r.kind = body[1];
    r.id = GetU32LE(body + 2);
  }
  *out = r;
  return true;
}

Bytes BuildShaperSelectRequest(const ShaperSelectRequest& r) {
  Bytes m = Start(r.sub);
  const int size = RequestSize(r.sub);
  if (size == 1) PutU8(m, r.role);
  if (size == 4) PutU32LE(m, r.id);
  if (size == 5) {
    PutU8(m, r.kind);
    PutU32LE(m, r.id);
  }
  return m;
}

const char* ShaperRequestName(uint8_t sub) {
  switch (sub) {
    case kShaperHello: return "hello";
    case kShaperReady: return "ready";
    case kShaperRequest: return "request shaper";
    case kShaperLockIn: return "lock shaper";
    case kShaperPreferredRole: return "preferred role";
    case kShaperSelectRole: return "role";
    case kShaperLockRole: return "lock role";
    case kShaperPerkPage: return "perk page";
    case kShaperSelectSkin: return "skin";
    case kShaperLockSkin: return "lock skin";
    case kShaperSelectVanity: return "vanity";
    default: return "?";
  }
}

}  // namespace gms
