// What one team may not learn through the host's shared sends (gms/core, no game addresses). The rules behind the
// engine's per-recipient sends:
//
// 1. mVisibleToTeam (sporelabsObject member 0, the server object's +0x888) holds a bit per team that sees the object.
//    A player's client reads only its own team's bit (the minimap icon and the attached server event, both through
//    its local vision mask, 1 << team); only a spectator's view reads the others. So each recipient gets the byte
//    masked to its audience, and a change of the other team's bit causes no message to a team at all: the timing of
//    the deltas doesn't tell either. Member 0 therefore leaves the shared object deltas and goes as its own small
//    object update, per audience, and creates and fog exits have the byte rewritten per audience.
// 2. A ServerEvent's head (type, objectId) tells a kind-1 stop or draw box apart, so they can be routed by their object.
// 3. A modifier record's caster: a recipient that doesn't know the caster would map it to 0, so it gets 0.
// 4. A player's available gold (AA member 1) stays with its team and the spectators.
#pragma once

#include <cstddef>
#include <cstdint>
#include <vector>

#include "core/common/bytes.h"
#include "core/protocol/wire.h"
#include "core/replication/modifiers.h"

namespace gms {

// ---- reflected bodies --------------------------------------------------------------------------------------------

// A type with more than 8 members starts its body with the mask bytes: their number N in the high nibble of the first
// byte, member i at bit (i & 7) of mask byte N - 1 - (i >> 3), the values right after them, in member order. The
// number of mask bytes, or 0 if `body` isn't such a body.
inline size_t ReflectedMaskBytes(const uint8_t* body, size_t n) {
  if (!body || n == 0) return 0;
  const size_t masks = body[0] >> 4;
  return masks != 0 && masks <= n ? masks : 0;
}

// Whether member i (i < 8) is in the body.
inline bool ReflectedHasLowMember(const uint8_t* body, size_t masks, int i) {
  return ((body[masks - 1] >> i) & 1) != 0;
}

// The offset of member 0's value in such a body, or -1 if the body has no member 0.
inline int ReflectedMember0At(const uint8_t* body, size_t n) {
  const size_t masks = ReflectedMaskBytes(body, n);
  if (!masks || masks >= n || !ReflectedHasLowMember(body, masks, 0)) return -1;
  return static_cast<int>(masks);
}

// ---- mVisibleToTeam per recipient --------------------------------------------------------------------------------

constexpr int kVisibleAudienceNoTeam = 0;
constexpr int kVisibleAudienceTeam1 = 1;
constexpr int kVisibleAudienceTeam2 = 2;
constexpr int kVisibleAudienceSpectators = 3;
constexpr int kVisibleAudiences = 4;

// A recipient's audience: a spectator's is the spectators'; a player's its team, 1 or 2; anything else no team.
inline int VisibleAudienceOf(bool spectator, int team) {
  if (spectator) return kVisibleAudienceSpectators;
  return team == 1 || team == 2 ? team : kVisibleAudienceNoTeam;
}

// The object's byte as `audience` gets it: a team its own bit, no team bit 0 (a client without a team reads 1 << 0),
// the spectators every bit.
inline uint8_t VisibleAudienceBits(int audience, uint8_t bits) {
  if (audience == kVisibleAudienceSpectators) return bits;
  if (audience == kVisibleAudienceTeam1 || audience == kVisibleAudienceTeam2)
    return static_cast<uint8_t>(bits & (1u << audience));
  return static_cast<uint8_t>(bits & 0x01);
}

// Member 0 alone as an object update (90): u32 id, the mask 0x11 (one mask byte, member 0), the value.
inline Bytes BuildVisibleUpdate(uint32_t id, uint8_t value) {
  Bytes b;
  PutU8(b, kGmsObjectUpdate);
  PutU32LE(b, id);
  PutU8(b, 0x11);
  PutU8(b, value);
  return b;
}

// What each audience last holds of one object. The first step (the tick the object first shows, whose creates carry
// the same byte) only seeds it; later steps return the audiences whose masked byte changed, as bits (1 << audience).
class VisibleShadow {
 public:
  unsigned Step(uint8_t bits) {
    uint8_t v[kVisibleAudiences];
    for (int a = 0; a < kVisibleAudiences; ++a) v[a] = VisibleAudienceBits(a, bits);
    return StepValues(v);
  }
  // FADE1c: the byte each audience gets, when it isn't the same byte masked (the players' client rule)
  unsigned StepValues(const uint8_t (&v)[kVisibleAudiences]) {
    unsigned changed = 0;
    for (int a = 0; a < kVisibleAudiences; ++a) {
      if (seeded_ && v[a] != sent_[a]) changed |= 1u << a;
      sent_[a] = v[a];
    }
    seeded_ = true;
    return changed;
  }

 private:
  uint8_t sent_[kVisibleAudiences] = {};
  bool seeded_ = false;
};

// ---- ServerEvent (A4) --------------------------------------------------------------------------------------------

constexpr uint8_t kServerEventAttach = 1;  // an attached effect's start; a draw box's first event
constexpr uint8_t kServerEventDetach = 2;  // GameObject_DestroyAttachedEffect's stop
constexpr uint8_t kServerEventUpdate = 3;  // an existing draw box's length

// Members 0 (u8 type) and 1 (u32 objectId) of a ServerEvent's body (the message without its wire byte). False if the
// body doesn't have both.
inline bool ParseServerEventHead(const uint8_t* body, size_t n, uint8_t* type, uint32_t* objectId) {
  const size_t masks = ReflectedMaskBytes(body, n);
  if (!masks || !ReflectedHasLowMember(body, masks, 0) || !ReflectedHasLowMember(body, masks, 1)) return false;
  if (n < masks + 1 + 4) return false;
  *type = body[masks];
  *objectId = GetU32LE(body + masks + 1);
  return true;
}

// ---- a modifier record's caster per recipient ----------------------------------------------------------------------

constexpr size_t kModifierRecordCasterAt = 4;

struct CasterView {
  uint16_t players = 0;
  Bytes msg;
};

// `msg` = a head of `recordsAt` bytes, then n 41-byte records (AB, 8F, 95). `knownOf(id)` = the players that know the
// object `id` (0 if it is gone). The views of `to`: every record's caster stays for the players that know it and is 0
// for the others. Empty if every recipient may have `msg` as it is (the usual case: no copy, send `msg`). A caster that
// is the record's own carrier is left alone: a recipient of the record has its carrier.
template <class KnownFn>
std::vector<CasterView> SplitModifierCasters(const Bytes& msg, size_t recordsAt, uint16_t to, KnownFn knownOf) {
  std::vector<CasterView> views;
  if (!to || msg.size() < recordsAt + kModifierRecordSize) return views;
  const size_t count = (msg.size() - recordsAt) / kModifierRecordSize;
  for (size_t r = 0; r < count; ++r) {
    const size_t at = recordsAt + r * kModifierRecordSize;
    const uint32_t caster = GetU32LE(msg.data() + at + kModifierRecordCasterAt);
    if (!caster || caster == GetU32LE(msg.data() + at)) continue;
    const uint16_t unknown = static_cast<uint16_t>(to & ~knownOf(caster));
    if (!unknown) continue;
    if (views.empty()) views.push_back(CasterView{to, msg});
    const size_t before = views.size();
    for (size_t v = 0; v < before; ++v) {
      const uint16_t zeroed = static_cast<uint16_t>(views[v].players & unknown);
      if (!zeroed) continue;
      if (zeroed != views[v].players) {  // the view splits: those that know the caster keep this copy
        views.push_back(CasterView{zeroed, views[v].msg});
        views[v].players = static_cast<uint16_t>(views[v].players & ~zeroed);
        CasterView& z = views.back();
        for (int k = 0; k < 4; ++k) z.msg[at + kModifierRecordCasterAt + k] = 0;
      } else {
        for (int k = 0; k < 4; ++k) views[v].msg[at + kModifierRecordCasterAt + k] = 0;
      }
    }
  }
  return views;
}

// ---- available gold ------------------------------------------------------------------------------------------------

// A labsPlayerFowData message (AA, u8 index, body) without mAvailableGold (member 1, 4 bytes), for the other team: the
// client reads it only for the local or viewed player (the item shop, the shaper panel); the scoreboard shows the total.
// False if nothing is left to send (a body with a zero mask asserts on the client); `m` is then undefined.
inline bool StripFowAvailableGold(Bytes* m) {
  constexpr size_t kBodyAt = 2;
  if (m->size() <= kBodyAt) return false;
  const uint8_t* body = m->data() + kBodyAt;
  const size_t n = m->size() - kBodyAt;
  const size_t masks = ReflectedMaskBytes(body, n);
  if (!masks) return true;  // not a body we can read: as it is
  if (!ReflectedHasLowMember(body, masks, 1)) return true;
  const size_t value = kBodyAt + masks + (ReflectedHasLowMember(body, masks, 0) ? 4 : 0);
  if (m->size() < value + 4) return true;
  m->erase(m->begin() + static_cast<std::ptrdiff_t>(value), m->begin() + static_cast<std::ptrdiff_t>(value + 4));
  (*m)[kBodyAt + masks - 1] = static_cast<uint8_t>((*m)[kBodyAt + masks - 1] & ~0x02);
  for (size_t k = 0; k < masks; ++k) {
    const uint8_t b = (*m)[kBodyAt + k];
    if (k == 0 ? (b & 0x0F) != 0 : b != 0) return true;
  }
  return false;
}

}  // namespace gms
