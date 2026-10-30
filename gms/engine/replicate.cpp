// Replication from the server simulator (D2f, D2g-live): see replicate.h.
#include "replicate.h"

#include <windows.h>

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>

#include "core/damage/regen.h"
#include "core/protocol/wire.h"
#include "core/replication/create_order.h"
#include "core/replication/outgoing.h"
#include "core/replication/resource_wire.h"
#include "game.h"
#include "log.h"
#include "passives.h"

using namespace game;

namespace repl {
namespace {

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in replication (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// The GMS message as GmsMessage_WriteBytes (0x00ADAEE0) and message.size() (0x008230F0) see it (constructor
// 0x00ADADF0). The host's bodies are written into its own buffer through one of these; nothing else of the game
// touches it.
struct HostMessage {
  std::uint32_t vtable;       // +0x00
  std::uint8_t unused4;       // +0x04
  std::uint8_t transportId;   // +0x05
  std::uint16_t pad6;         // +0x06
  std::uint32_t size;         // +0x08 bytes written
  unsigned char* buffer;      // +0x0C
  std::uint32_t capacity;     // +0x10
  std::uint8_t ownsBuffer;    // +0x14
  std::uint8_t pad15[3];      //
  std::uint32_t readPos;      // +0x18
  unsigned char* cursor;      // +0x1C
};
static_assert(sizeof(HostMessage) == 0x20, "the GMS message is 0x20 bytes");

enum TypeSlot {
  kCreate, kObject, kAttr, kComb, kPlayer, kFow, kLoco, kProj, kInter, kAgent, kUI, kNode,
  kAnim, kEffect, kCamp,  // D2g-live step 3
  kNumTypes
};
const std::uint32_t kTypeHashes[kNumTypes] = {kTypeCreateData,       kTypeSporelabsObject,  kTypeAttributeData,
                                              kTypeCombatantData,    kTypeLabsPlayerGlobalData, kTypeLabsPlayerFowData,
                                              kTypeLocomotionData,   kTypeProjectileData,   kTypeInteractableData,
                                              kTypeAgentBlackboard,  kTypeUIData,           kTypeUINodeData,
                                              kTypeAnimationData,    kTypeAttachedEffectData, kTypeUICampData};
const int kTypeMembers[kNumTypes] = {9, 18, 69, 5, 18, 63, 21, 29, 1, 6, 63, 21, 12, 7, 2};
const char* const kTypeNames[kNumTypes] = {"cGameObjectCreateData", "sporelabsObject",   "cAttributeData",
                                           "cCombatantData",        "labsPlayerGlobalData", "labsPlayerFowData",
                                           "cLocomotionData",       "cProjectileData",   "cInteractableData",
                                           "cAgentBlackboard",      "cBattlegroundsUIData", "cBattlegroundsUINodeData",
                                           "cAnimationData",        "cAttachedEffectData", "cBattlegroundsUICampData"};
void* g_types[kNumTypes];
bool g_ready;
bool g_netRateOk = true;

// The parts (replicate.h Part): type, wire byte, the struct's pointer in the object (0 = the object itself), its field
// state's offset in the struct (for the object: in the network component).
struct PartInfo {
  TypeSlot type;
  std::uint8_t wire;
  std::uintptr_t ofObject;
  std::uintptr_t fieldState;
  const char* name;
};
const PartInfo kParts[kNumParts] = {
    {kObject, gms::kGmsObjectUpdate, 0, part::kFsNetwork, "object"},
    {kProj, gms::kGmsProjectileDataCreate, part::kProjectile, part::kFsProjectile, "projectile"},
    {kLoco, gms::kGmsLocomotionDataUpdate, part::kLocomotion, part::kFsLocomotion, "locomotion"},
    {kAttr, gms::kGmsAttributeDataUpdate, part::kAttributes, part::kFsAttributes, "attributes"},
    {kComb, gms::kGmsCombatantDataUpdate, part::kCombatant, part::kFsCombatant, "combatant"},
    {kAgent, gms::kGmsAgentBlackboardUpdate, part::kBlackboard, part::kFsBlackboard, "blackboard"},
    {kInter, gms::kGmsInteractableDataUpdate, part::kInteractable, part::kFsInteractable, "interactable"},
};

// Analyzer modes (cReflectData +0): 1 = conditional members too (full state), 8 = incremental (skip the full-state-only
// members).
constexpr unsigned kModeFull = 1;
constexpr unsigned kModeIncremental = 8;

// ---- the value hash and the analyzer's choice, as NetworkUtils_AnalyzeReflectedFields (0x008F90F0) computes them ---

std::uint32_t U32At(const void* p, std::uintptr_t off) {
  std::uint32_t v;
  std::memcpy(&v, static_cast<const char*>(p) + off, 4);
  return v;
}

// A member's bytes: `asset` members count 4 per element (the global 0x014322FC, the pointer), others their size.
unsigned MemberWidth(const unsigned char* rec) {
  const unsigned elem = U32At(rec, 4) == kAssetTypeHash ? *At<const std::uint32_t*>(kAssetMemberSize) : U32At(rec, 8);
  return elem * U32At(rec, 0x18);
}

std::uint32_t MemberHash(const unsigned char* rec, const unsigned char* data) {
  const unsigned w = MemberWidth(rec);
  const unsigned char* p = data + U32At(rec, 0x14);
  if (w == 4) return U32At(p, 0);
  if (w == 2) return static_cast<std::uint32_t>(p[0] | p[1] << 8);
  if (w == 1) return p[0];
  std::uint32_t h = 0x811C9DC5u;
  for (unsigned i = 0; i < w; ++i) h = (h * 0x01000193u) ^ p[i];
  return h;
}

bool Chosen(const unsigned char* rec, const unsigned char* data, unsigned mode, std::uint32_t hash) {
  const unsigned char f = rec[0x44];
  if ((mode & 8) && f == 1) return false;
  if (!(mode & 1) && (f & 0x20) && data[U32At(rec, 0x48)] == 0) return false;
  if (((mode & 4) || (f & 0x10)) && hash == U32At(rec, 0x20)) return false;
  return true;
}

// Whether any member the analyzer would choose in `mode` differs from the field state (cheap: no engine call).
bool AnyDelta(void* data, void* type, void* fieldState, unsigned mode) {
  __try {
    const unsigned char* d = static_cast<const unsigned char*>(data);
    const unsigned char* records = Field<unsigned char*>(type, 8);
    const int n = Field<int>(type, 0xC);
    for (int i = 0; i < n; ++i) {
      const unsigned char* rec = records + i * kMemberRecordSize;
      const std::uint32_t h = MemberHash(rec, d);
      if (Chosen(rec, d, mode, h) && U32At(fieldState, i * 8) != h) return true;
    }
    return false;
  } __except (SehNote(GetExceptionInformation(), "AnyDelta")) {
    return false;
  }
}

// One reflected body of `data` (laid out as `type`) into `buf`: the analyzer's choice in `mode`, narrowed to the
// members with keep[index] (all if `keep` is null) and, with `delta`, to those whose value hash differs from the field
// state. Every kept member's hash goes into its entry, so the writer stores it in `fieldState` (if given; the writer
// asserts on the analyzer's 0xCDCDCDCD otherwise). Returns the body size; 0 if no member is left (the writer then
// writes nothing, and a reader would run past the end); -1 on an exception; -2 if the writer wrote another count than
// NetworkUtils_GetReflectedSize said; -3 if it doesn't fit. `written` gets the number of members.
int WriteBody(void* data, void* type, unsigned mode, const bool* keep, void* fieldState, bool delta, unsigned char* buf,
              unsigned cap, int* written) {
  __try {
    *written = 0;
    alignas(8) unsigned char rd[kReflectDataSize];
    std::memset(rd, 0, sizeof rd);
    ReflectDataInit(rd, data, 0, type, 0);
    Field<unsigned>(rd, 0) = mode;
    AnalyzeReflected(data, type, rd);
    unsigned char* begin = Field<unsigned char*>(rd, 0x10);
    unsigned char* end = Field<unsigned char*>(rd, 0x14);
    const unsigned char* records = Field<unsigned char*>(type, 8);
    const unsigned char* d = static_cast<const unsigned char*>(data);
    unsigned char* out = begin;
    unsigned size = 0, bits = 0;
    int n = 0;
    for (unsigned char* e = begin; e < end; e += 8) {
      const unsigned index = e[0];
      if (keep && (index >= 128 || !keep[index])) continue;
      const unsigned char* rec = records + index * kMemberRecordSize;
      const std::uint32_t h = MemberHash(rec, d);
      if (delta && fieldState && U32At(fieldState, index * 8) == h) continue;
      std::memcpy(e + 4, &h, 4);
      size += MemberWidth(rec);
      if (index < 32) bits |= 1u << index;
      if (out != e) std::memmove(out, e, 8);
      out += 8;
      ++n;
    }
    Field<unsigned char*>(rd, 0x14) = out;
    Field<unsigned>(rd, 4) = size;
    Field<unsigned>(rd, 8) = bits;
    *written = n;
    if (!n) return 0;
    const int total = GetReflectedSize(rd);
    if (total <= 0) return 0;
    if (static_cast<unsigned>(total) > cap) return -3;
    HostMessage m;
    std::memset(&m, 0, sizeof m);
    m.vtable = kMessageVtable;
    m.buffer = buf;
    m.capacity = cap;
    m.cursor = buf;
    WriteReflected(data, fieldState, type, &m, rd);
    // The chosen members live in cReflectData's inline room (+0x24, 96 entries; the largest type here has 69). Had
    // it spilled to the heap, the game's free (0x006A9B30) would be due, as GameSimulator_BuildServerEventMessage does.
    if (Field<unsigned char*>(rd, 0x10) != rd + 0x24) Log("replication: cReflectData spilled to the heap (leaked)");
    return m.size == static_cast<unsigned>(total) ? total : -2;
  } __except (SehNote(GetExceptionInformation(), "WriteBody")) {
    return -1;
  }
}

const char* WriteError(int n) {
  return n == 0 ? "no member chosen" : n == -1 ? "exception" : n == -2 ? "size mismatch" : "too large";
}

void* SehPlayer(void* sim, unsigned index) {
  __try {
    return GetPlayerByIndex(sim, index);
  } __except (SehNote(GetExceptionInformation(), "GetPlayerByIndex")) {
    return nullptr;
  }
}

bool CodeMatches(std::uintptr_t va, const unsigned char* bytes, std::size_t n) {
  __try {
    return std::memcmp(reinterpret_cast<const void*>(va), bytes, n) == 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

void* SehFindType(std::uint32_t hash, int* members) {
  __try {
    void* t = FindType(hash);
    *members = t ? Field<int>(t, 0xC) : -1;
    return t;
  } __except (SehNote(GetExceptionInformation(), "FindType")) {
    return nullptr;
  }
}

// What the create data is built from, read from one server object.
struct CreateInfo {
  void* noun;
  float pos[3];
  float quat[4];
  float scale;
  std::uint8_t team, playerIdx, playerControlled, hasCollision;
  std::uint32_t owner;
  std::uint32_t markerId;
};

bool ReadCreateInfo(void* o, CreateInfo* i) {
  __try {
    std::memset(i, 0, sizeof *i);
    i->noun = Field<void*>(o, gobj::kNoun);
    std::memcpy(i->pos, static_cast<char*>(o) + gobj::kPosition, sizeof i->pos);
    std::memcpy(i->quat, static_cast<char*>(o) + gobj::kOrientation, sizeof i->quat);
    i->scale = Field<float>(o, gobj::kScale);
    i->team = Field<std::uint8_t>(o, gobj::kTeam);
    i->playerIdx = Field<std::uint8_t>(o, gobj::kPlayerIndex);
    i->playerControlled = Field<std::uint8_t>(o, gobj::kPlayerControlled);
    i->hasCollision = Field<std::uint8_t>(o, gobj::kHasCollision);
    i->owner = Field<std::uint32_t>(o, gobj::kOwner);
    i->markerId = Field<std::uint32_t>(o, gobj::kMarkerId);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadCreateInfo")) {
    return false;
  }
}

// The live networked objects (network component, not pending delete), at most `max`. With `pendingEffects` instead
// only the placeable effects that are pending delete and that no player knows (ABILVFX: made and destroyed in one
// tick). -1 on an exception.
int ScanRaw(void* sim, NetObject* out, int max, bool pendingEffects) {
  __try {
    void* om = Field<void*>(sim, sim::kObjectManager);
    int it = ObjMgrIterBegin(om);
    int n = 0;
    for (void* o = ObjMgrIterNext(om, &it); o && n < max; o = ObjMgrIterNext(om, &it)) {
      char* nc = Field<char*>(o, gobj::kNetwork);
      if (!nc) continue;
      const bool pending = Field<unsigned char>(o, gobj::kPendingDelete) != 0;
      if (pending != pendingEffects) continue;
      if (pendingEffects) {
        if (Field<std::uint16_t>(nc, kNetworkKnownMask)) continue;
        void* nn = Field<void*>(o, gobj::kNoun);
        char* nd = nn ? Field<char*>(nn, noun::kData) : nullptr;
        if (!nd || Field<std::int32_t>(nd, noun::kType) != noun::kTypePlaceableEffect) continue;
      }
      NetObject& i = out[n++];
      i = NetObject();
      i.object = o;
      i.id = Field<std::uint32_t>(o, gobj::kId);
      void* noun = Field<void*>(o, gobj::kNoun);
      if (noun) {
        const char* name = Field<const char*>(noun, 0);
        if (name) std::strncpy(i.name, name, sizeof i.name - 1);
        char* data = Field<char*>(noun, noun::kData);
        i.nounType = data ? Field<std::int32_t>(data, noun::kType) : 0;
      }
      i.known = Field<std::uint16_t>(nc, kNetworkKnownMask);
      i.visible = Field<std::uint16_t>(nc, kNetworkVisibleMask);
      i.data[kPartObject] = o;
      for (int p = 1; p < kNumParts; ++p) i.data[p] = Field<void*>(o, kParts[p].ofObject);
      std::memcpy(i.pos, static_cast<char*>(o) + gobj::kPosition, sizeof i.pos);
      i.hp = i.data[kPartCombatant] ? Field<float>(i.data[kPartCombatant], combatant::kHitPoints) : -1.f;
      i.owner = Field<std::uint32_t>(o, gobj::kOwner);
      i.markerId = Field<std::uint32_t>(o, gobj::kMarkerId);
      if (i.data[kPartLocomotion]) i.parent = Field<std::uint32_t>(i.data[kPartLocomotion], loco::kParent);
      if (i.data[kPartProjectile])
        i.target = Field<std::uint32_t>(i.data[kPartProjectile], projdata::kTargetObjectId);
      i.pendingDelete = pending;
    }
    return n;
  } __except (SehNote(GetExceptionInformation(), "Scan")) {
    return -1;
  }
}

void* SehFieldState(const NetObject& o, int p) {
  __try {
    if (p == kPartObject) {
      char* nc = Field<char*>(o.object, gobj::kNetwork);
      return nc ? nc + kParts[p].fieldState : nullptr;
    }
    return o.data[p] ? static_cast<char*>(o.data[p]) + kParts[p].fieldState : nullptr;
  } __except (SehNote(GetExceptionInformation(), "FieldState")) {
    return nullptr;
  }
}

// RENZO9999: the resource of a pool that is exactly 0 goes out as gms::kResourceWireFloor
// (core/replication/resource_wire.h). The writers get a copy of the combatant data with that value; the field state
// they fill is still the object's own, so the next delta compares the server's value with what the clients have.
bool g_resourceFloor = true;
int g_effectsBeforeComplete = 1;  // ABILVFX-WALLS: SetEffectsBeforeComplete
std::uint32_t g_floorLogged[64];  // the objects whose first floored body was logged
int g_floorLoggedCount;
unsigned g_floorBodies;

void NoteFloor(const NetObject& o, float maximum, const char* what) {
  ++g_floorBodies;
  for (int i = 0; i < g_floorLoggedCount; ++i)
    if (g_floorLogged[i] == o.id) return;
  if (g_floorLoggedCount >= static_cast<int>(sizeof g_floorLogged / sizeof g_floorLogged[0])) return;
  g_floorLogged[g_floorLoggedCount++] = o.id;
  Log("replication: %08X %s: resource 0 of %.1f goes out as %.3f in its %s and later A0s (the HUD bar's numbers; "
      "[host] resource_wire_floor 1; %u floored bodies so far)",
      o.id, o.name, maximum, gms::kResourceWireFloor, what, g_floorBodies);
}

// `data` (the object's combatant data), or `copy` holding it with the resource the clients get.
void* SehCombatantWire(const NetObject& o, void* data, unsigned char* copy, const char* what) {
  if (!g_resourceFloor || !data) return data;
  __try {
    const float value = Field<float>(data, combatant::kResourcePoints);
    if (value != 0.f) return data;  // the common case, without an engine call
    void* ad = o.data[kPartAttributes];
    if (!ad) return data;
    const float maximum = GetAttribute(ad, regenAttr::kMaxResourcePoints, 1);
    const bool health = GetResourceType(o.object) == gms::damage::kResourceTypeHealth;
    const float wire = gms::ResourceWireValue(value, maximum, health);
    if (wire == value) return data;
    std::memcpy(copy, data, combatant::kSize);
    Field<float>(copy, combatant::kResourcePoints) = wire;
    NoteFloor(o, maximum, what);
    return copy;
  } __except (SehNote(GetExceptionInformation(), "combatant wire value")) {
    return data;
  }
}

// The data a part's body is written from: the object's own, except the combatant data (above).
void* WireData(const NetObject& o, int p, unsigned char* combatantCopy, const char* what) {
  return p == kPartCombatant ? SehCombatantWire(o, o.data[p], combatantCopy, what) : o.data[p];
}

// Euler angles in degrees from the object's quaternion (x, y, z, w), the inverse of the reader's q = Rz * Ry * Rx
// (Math_EulerToQuaternion 0x00673A90).
void EulerDegrees(const float q[4], float* rx, float* ry, float* rz) {
  const double x = q[0], y = q[1], z = q[2], w = q[3];
  const double kDeg = 180.0 / 3.14159265358979323846;
  *rx = static_cast<float>(std::atan2(2.0 * (w * x + y * z), 1.0 - 2.0 * (x * x + y * y)) * kDeg);
  double s = 2.0 * (w * y - z * x);
  if (s > 1.0) s = 1.0;
  if (s < -1.0) s = -1.0;
  *ry = static_cast<float>(std::asin(s) * kDeg);
  *rz = static_cast<float>(std::atan2(2.0 * (w * z + x * y), 1.0 - 2.0 * (y * y + z * z)) * kDeg);
}

void PutU32(Bytes& b, std::uint32_t v) {
  for (int k = 0; k < 4; ++k) b.push_back(static_cast<std::uint8_t>(v >> (8 * k)));
}

Bytes IdMessage(std::uint8_t wire, std::uint32_t id) {
  Bytes m;
  m.push_back(wire);
  PutU32(m, id);
  return m;
}

// Appends one full-state body of `data` (as `slot`) to `m`; false (logged) if it could not be written.
bool AppendFull(Bytes& m, void* data, TypeSlot slot, void* fieldState, std::uint32_t id, const char* name) {
  static unsigned char buf[2048];
  int written = 0;
  const int n = WriteBody(data, g_types[slot], kModeFull, nullptr, fieldState, false, buf, sizeof buf, &written);
  if (n <= 0) {
    Log("replication: %s of %08X (%s) not written (%d: %s)", kTypeNames[slot], id, name, n, WriteError(n));
    return false;
  }
  m.insert(m.end(), buf, buf + n);
  return true;
}

std::uint32_t SehDeploy(void* sim, unsigned index) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return 0;
    DeployCharacter(p, At<const float*>(kZeroVector), At<const float*>(kIdentityQuaternion));
    return Field<std::uint32_t>(p, player::kCurrentCreature);
  } __except (SehNote(GetExceptionInformation(), "DeployCharacter")) {
    return 0;
  }
}

// ---- seam 1 ---------------------------------------------------------------------------------------------------------

struct RawEntry {
  int kind;
  std::uint32_t object;
  std::uint32_t recipients;
  bool objectAlive;
  std::uint16_t seers;  // kind 2: the live object's known & visible masks
  bool hasMessage;
  std::uint8_t wire;
  std::uint32_t offset, length;
  bool hasPoint;        // kind 3: the line-of-sight record's position, team and radius (read before its release)
  float x, y, radius;
  std::uint8_t team;
};

// Pass 1: the number of entries and the bytes of their messages.
bool MeasureOutgoing(void* sim, int* entries, unsigned* bytes) {
  __try {
    char* b = Field<char*>(sim, sim::kOutgoingBegin);
    char* e = Field<char*>(sim, sim::kOutgoingEnd);
    *entries = static_cast<int>((e - b) / kOutgoingEntrySize);
    *bytes = 0;
    for (char* p = b; p < e; p += kOutgoingEntrySize) {
      char* m = Field<char*>(p, 0x10);
      if (m && Field<std::uint32_t>(m, 0) == kMessageVtable) *bytes += Field<std::uint32_t>(m, msg::kSize);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "MeasureOutgoing")) {
    return false;
  }
}

// Pass 2: the entries (at most `max`) and their message bytes (at most `cap`); kind-3 entries' line-of-sight records
// are released (`released` counts them).
int ReadOutgoing(void* sim, RawEntry* out, int max, unsigned char* bytes, unsigned cap, int* released) {
  __try {
    char* b = Field<char*>(sim, sim::kOutgoingBegin);
    char* e = Field<char*>(sim, sim::kOutgoingEnd);
    void* los = Field<void*>(sim, los::kOfSim);
    int k = 0;
    unsigned used = 0;
    for (char* p = b; p < e && k < max; p += kOutgoingEntrySize, ++k) {
      RawEntry& r = out[k];
      std::memset(&r, 0, sizeof r);
      r.kind = Field<int>(p, 0);
      r.object = Field<std::uint32_t>(p, 4);
      r.recipients = Field<std::uint32_t>(p, 0xC);
      char* m = Field<char*>(p, 0x10);
      if (m && Field<std::uint32_t>(m, 0) == kMessageVtable) {
        const std::uint32_t len = Field<std::uint32_t>(m, msg::kSize);
        if (used + len <= cap) {
          r.hasMessage = true;
          r.wire = Field<std::uint8_t>(m, msg::kTransportId);
          if (len) std::memcpy(bytes + used, Field<const unsigned char*>(m, msg::kBuffer), len);
          r.offset = used;
          r.length = len;
          used += len;
        }
      }
      if (r.kind == 2) {
        void* o = GetObjectById(r.object);
        char* nc = o ? Field<char*>(o, gobj::kNetwork) : nullptr;
        r.objectAlive = o != nullptr;
        r.seers = nc ? static_cast<std::uint16_t>(Field<std::uint16_t>(nc, kNetworkKnownMask) &
                                                  Field<std::uint16_t>(nc, kNetworkVisibleMask))
                     : 0;
      }
      // D2g: the stripped drain released the kind-3 entry's line-of-sight record; the vector's destructor doesn't.
      // D2g-live step 2: its position (+0xC/+0x10), team (+0x1D) and radius (+0x14) route the message first.
      if (r.kind == 3 && los && Field<void*>(p, 8)) {
        char* t = Field<char*>(p, 8);
        r.hasPoint = true;
        r.x = Field<float>(t, 0x0C);
        r.y = Field<float>(t, 0x10);
        r.radius = Field<float>(t, 0x14);
        r.team = Field<std::uint8_t>(t, 0x1D);
        ReleaseTracked(los, reinterpret_cast<void**>(p + 8));
        ++*released;
      }
    }
    return k;
  } __except (SehNote(GetExceptionInformation(), "ReadOutgoing")) {
    return -1;
  }
}

// Releases every entry's message and empties the vector, as the simulator's reset does (0x008F3BC0).
bool ClearOutgoing(void* sim) {
  __try {
    char* vec = static_cast<char*>(sim) + sim::kOutgoingBegin;
    char* b = Field<char*>(vec, 0);
    char* e = Field<char*>(vec, 4);
    if (b != e) {
      OutgoingDestroyRange(vec, b, e);
      Field<char*>(vec, 4) = b;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ClearOutgoing")) {
    return false;
  }
}

// GameSimulator_UpdateNetworkRateTimers takes the simulator in ESI (and saves EBX and ESI itself).
__declspec(naked) void __cdecl NetworkRateThunk(void* /*sim*/) {
  __asm {
    push esi
    mov esi, dword ptr [esp + 8]
    mov eax, 0x008EE900
    call eax
    pop esi
    ret
  }
}

bool SehNetworkRate(void* sim, unsigned* mask) {
  __try {
    NetworkRateThunk(sim);
    *mask = Field<unsigned>(sim, sim::kNetworkRateMask);
    return true;
  } __except (SehNote(GetExceptionInformation(), "UpdateNetworkRateTimers")) {
    return false;
  }
}

// ---- D2g-live step 3: attached effects, animation state -------------------------------------------------------------

// The used attached-effect slots (asset != 0) as a bit mask; -1 on an exception.
int SehEffectSlots(void* object) {
  __try {
    int mask = 0;
    for (int i = 0; i < kNumAttachedEffects; ++i) {
      char* slot = static_cast<char*>(object) + kAttachedEffects + i * kAttachedEffectSize;
      if (Field<std::uint32_t>(slot, kAttachedEffectAsset)) mask |= 1 << i;
    }
    return mask;
  } __except (SehNote(GetExceptionInformation(), "attached effects")) {
    return -1;
  }
}

}  // namespace

int EffectSlotNames(void* object, char* buf, std::size_t size) {
  if (!size) return 0;
  buf[0] = 0;
  int n = 0;
  std::size_t at = 0;
  __try {
    for (int i = 0; i < kNumAttachedEffects; ++i) {
      char* slot = static_cast<char*>(object) + kAttachedEffects + i * kAttachedEffectSize;
      void* asset = Field<void*>(slot, kAttachedEffectAsset);
      if (!asset) continue;
      const char* name = Field<const char*>(asset, 0);
      char nm[80];
      std::size_t k = 0;
      for (; name && k + 1 < sizeof nm && name[k]; ++k) nm[k] = name[k];
      nm[k] = 0;
      const int w = std::snprintf(buf + at, size - at, "%s%d:%s %08X", n ? ", " : "", i, nm,
                                  Field<std::uint32_t>(slot, kAttachedEffectHandle));
      ++n;
      if (w < 0 || static_cast<std::size_t>(w) >= size - at) break;
      at += static_cast<std::size_t>(w);
    }
  } __except (SehNote(GetExceptionInformation(), "attached effect names")) {
  }
  return n;
}

namespace {

// ABILVFX-WALLS: whether a used slot's event has a draw box (its def's graphics part, draw box count > 0); false on an
// exception (logged).
bool SehHasDrawBoxSlot(void* object) {
  __try {
    for (int i = 0; i < kNumAttachedEffects; ++i) {
      char* slot = static_cast<char*>(object) + kAttachedEffects + i * kAttachedEffectSize;
      char* def = Field<char*>(slot, kAttachedEffectAsset);
      if (!def) continue;
      char* gfx = Field<char*>(def, kServerEventDefGraphics);
      if (gfx && Field<std::int32_t>(gfx, kEventGraphicsDrawBoxCount) > 0) return true;
    }
    return false;
  } __except (SehNote(GetExceptionInformation(), "attached effects' draw boxes")) {
    return false;
  }
}

struct AnimState {
  bool present;
  std::uint32_t overrides[anim::kNumOverrides];
  std::uint32_t state, overlay;
  std::uint64_t playTime, overlayTime;
  float timeScale;
};

bool SehAnim(void* object, AnimState* a) {
  __try {
    std::memset(a, 0, sizeof *a);
    char* d = Field<char*>(object, anim::kOfObject);
    if (!d) return true;
    a->present = true;
    std::memcpy(a->overrides, d + anim::kOverrides, sizeof a->overrides);
    a->state = Field<std::uint32_t>(d, anim::kLastState);
    a->playTime = Field<std::uint64_t>(d, anim::kLastPlayTime);
    a->timeScale = Field<float>(d, anim::kLastTimeScale);
    a->overlay = Field<std::uint32_t>(d, anim::kOverlayState);
    a->overlayTime = Field<std::uint64_t>(d, anim::kOverlayPlayTime);
    return true;
  } __except (SehNote(GetExceptionInformation(), "animation data")) {
    return false;
  }
}

void* SehAnimData(void* object) {
  __try {
    return Field<void*>(object, anim::kOfObject);
  } __except (SehNote(GetExceptionInformation(), "animation data")) {
    return nullptr;
  }
}

}  // namespace

std::uint8_t PartWire(int p) { return p >= 0 && p < kNumParts ? kParts[p].wire : 0; }
const char* PartName(int p) { return p >= 0 && p < kNumParts ? kParts[p].name : "?"; }

bool Init() {
  if (g_ready) return true;
  struct Check {
    std::uintptr_t va;
    const unsigned char* bytes;
    std::size_t n;
    const char* name;
  };
  const Check checks[] = {
      {0x008B49B0, kFindTypeBytes, sizeof kFindTypeBytes, "ReflectionRegistry_FindType"},
      {0x008F90A0, kReflectDataInitBytes, sizeof kReflectDataInitBytes, "ReflectData_Init"},
      {0x008F90F0, kAnalyzeReflectedBytes, sizeof kAnalyzeReflectedBytes, "NetworkUtils_AnalyzeReflectedFields"},
      {0x008F8680, kGetReflectedSizeBytes, sizeof kGetReflectedSizeBytes, "NetworkUtils_GetReflectedSize"},
      {0x008F8770, kWriteReflectedBytes, sizeof kWriteReflectedBytes, "NetworkUtils_WriteReflectedFields"},
      {0x00ADAEE0, kWriteBytesBytes, sizeof kWriteBytesBytes, "GmsMessage_WriteBytes"},
      {0x008F1CA0, kOutgoingDestroyRangeBytes, sizeof kOutgoingDestroyRangeBytes, "OutgoingVector_DestroyRange"},
      {0x00915400, kDeployCharacterBytes, sizeof kDeployCharacterBytes, "LabsPlayer_DeployCharacter"},
      {0x00917670, kReleaseTrackedBytes, sizeof kReleaseTrackedBytes, "LineOfSight_ReleaseTracked"},
  };
  for (const Check& c : checks)
    if (!CodeMatches(c.va, c.bytes, c.n)) {
      Log("replication: %s at %08X has other bytes; no replication", c.name, static_cast<unsigned>(c.va));
      return false;
    }
  for (int i = 0; i < kNumTypes; ++i) {
    int members = -1;
    g_types[i] = SehFindType(kTypeHashes[i], &members);
    Log("replication: type %s (%08X) -> %p, %d members", kTypeNames[i], kTypeHashes[i], g_types[i], members);
    if (!g_types[i] || members != kTypeMembers[i]) {
      Log("replication: %s not as expected (%d members); no replication", kTypeNames[i], kTypeMembers[i]);
      return false;
    }
  }
  g_ready = true;
  LogPlayerFowMembers(4, 13);
  return true;
}

std::uint32_t DeployShaper(void* sim, unsigned index) {
  const std::uint32_t id = SehDeploy(sim, index);
  Log("replication: LabsPlayer_DeployCharacter(player %u, start point) -> creature %08X", index, id);
  if (id) passives::Activate(sim, index, "deploy");  // D6c: the shaper's passive (passives.h)
  return id;
}

int Scan(void* sim, std::vector<NetObject>* out) {
  out->resize(0x400);  // a server simulator's object pool holds 0x400 (ObjectPool_Init, D2e)
  const int n = ScanRaw(sim, out->data(), static_cast<int>(out->size()), false);
  out->resize(n > 0 ? static_cast<size_t>(n) : 0);
  return n;
}

int ScanPendingEffects(void* sim, std::vector<NetObject>* out) {
  out->resize(0x400);
  const int n = ScanRaw(sim, out->data(), static_cast<int>(out->size()), true);
  out->resize(n > 0 ? static_cast<size_t>(n) : 0);
  return n;
}

bool Replicated(std::int32_t t, bool all) {
  if (all) return t != noun::kTypeSpawnPoint;
  return t == noun::kTypeTower || t == noun::kTypeCreature || t == noun::kTypeInteractable;
}

namespace {
std::vector<std::uint32_t> g_iconSpawnPoints;  // PARAMAP: the camp markers whose spawn point objects are created
}  // namespace

void SetIconSpawnPoints(const std::vector<std::uint32_t>& markerIds) { g_iconSpawnPoints = markerIds; }

void SetResourceWireFloor(bool on) { g_resourceFloor = on; }
void SetEffectsBeforeComplete(int mode) { g_effectsBeforeComplete = mode; }

bool Replicated(const NetObject& o, bool all) {
  if (o.nounType == noun::kTypeSpawnPoint && o.markerId)
    for (std::uint32_t m : g_iconSpawnPoints)
      if (m == o.markerId) return true;
  return Replicated(o.nounType, all);
}

const char* NounTypeName(std::int32_t t) {
  switch (t) {
    case noun::kTypeTower: return "Tower";
    case noun::kTypeCreature: return "creature";
    case noun::kTypeInteractable: return "interactable";
    case noun::kTypeSpawnPoint: return "spawn point";
    case noun::kTypeProjectile: return "projectile";
    case noun::kTypeTitan: return "titan";
    case noun::kTypeAbilityObject: return "ability object";
    case noun::kTypePlaceableEffect: return "placeable effect";
    case noun::kTypeLuaJob: return "Lua job";
    case noun::kTypeVisionObject: return "vision object";
    case noun::kTypeObstacle: return "obstacle";
    default: return "other";
  }
}

bool BuildCreate(const NetObject& o, bool withFieldState, const Bytes& modifierRecords, std::vector<WorldMessage>* out,
                 int* bytes) {
  *bytes = 0;
  if (!g_ready) return false;
  CreateInfo ci;
  if (!ReadCreateInfo(o.object, &ci)) return false;
  alignas(8) unsigned char cd[kCreateDataSize];
  std::memset(cd, 0, sizeof cd);
  Field<void*>(cd, create::kNoun) = ci.noun;
  std::memcpy(cd + create::kPosition, ci.pos, sizeof ci.pos);
  float rx, ry, rz;
  EulerDegrees(ci.quat, &rx, &ry, &rz);
  Field<float>(cd, create::kRotX) = rx;
  Field<float>(cd, create::kRotY) = ry;
  Field<float>(cd, create::kRotZ) = rz;
  Field<float>(cd, create::kScale) = ci.scale;
  Field<std::uint8_t>(cd, create::kTeam) = ci.team;
  Field<std::uint32_t>(cd, create::kOwner) = ci.owner;
  Field<std::uint8_t>(cd, create::kFlags) =
      static_cast<std::uint8_t>((ci.hasCollision ? 1 : 0) | (ci.playerControlled ? 2 : 0));

  std::vector<WorldMessage> msgs;
  WorldMessage begin{IdMessage(gms::kGmsObjectCreateBegin, o.id), std::string("create ") + o.name};
  void* objectState = withFieldState ? SehFieldState(o, kPartObject) : nullptr;
  if (!AppendFull(begin.msg, cd, kCreate, nullptr, o.id, o.name) ||
      !AppendFull(begin.msg, o.object, kObject, objectState, o.id, o.name))
    return false;
  msgs.push_back(begin);
  char parts[96] = "";
  alignas(8) unsigned char combatantCopy[combatant::kSize];
  for (int p = kPartProjectile; p < kNumParts; ++p) {
    if (!o.data[p]) continue;
    WorldMessage m{IdMessage(kParts[p].wire, o.id), std::string(kParts[p].name) + " of " + o.name};
    if (!AppendFull(m.msg, WireData(o, p, combatantCopy, "create"), kParts[p].type,
                    withFieldState ? SehFieldState(o, p) : nullptr, o.id, o.name))
      return false;
    msgs.push_back(m);
    std::strncat(parts, " ", sizeof parts - std::strlen(parts) - 1);
    std::strncat(parts, kParts[p].name, sizeof parts - std::strlen(parts) - 1);
  }
  // D2g-live step 3: the animation overrides (9E) when any is set, before 8F; after 8F (which creates the model) the
  // attached effects (92) and the last animation (B1) when there are any
  Bytes e9, b1, e2;
  int effects = 0;
  AnimationMessages(o, false, &e9, &b1);
  if (!e9.empty()) {
    msgs.push_back(WorldMessage{e9, std::string("animation overrides of ") + o.name});
    std::strncat(parts, " animation", sizeof parts - std::strlen(parts) - 1);
  }
  WorldMessage complete{IdMessage(gms::kGmsObjectCreateComplete, o.id), std::string("complete ") + o.name};
  complete.msg.insert(complete.msg.end(), modifierRecords.begin(), modifierRecords.end());
  // ABILVFX-WALLS: the attached effects before 8F where the model's creation must see them (a draw box: the client
  // makes it only from the slots it holds at 8F; a 92 after 8F starts effects only), [host] effects_before_complete
  const bool haveEffects = AttachedEffectsUpdate(o, &e2, &effects) && effects > 0;
  const bool effectsFirst =
      haveEffects && gms::EffectsBeforeComplete(g_effectsBeforeComplete,
                                                g_effectsBeforeComplete == 1 && SehHasDrawBoxSlot(o.object));
  if (effectsFirst) {
    msgs.push_back(WorldMessage{e2, std::string("attached effects of ") + o.name + " (before 8F)"});
    std::strncat(parts, " effects-first", sizeof parts - std::strlen(parts) - 1);
  }
  msgs.push_back(complete);
  if (haveEffects && !effectsFirst) msgs.push_back(WorldMessage{e2, std::string("attached effects of ") + o.name});
  if (!b1.empty()) msgs.push_back(WorldMessage{b1, std::string("last animation of ") + o.name});
  int n = 0;
  for (const auto& m : msgs) n += static_cast<int>(m.msg.size());
  *bytes = n;
  out->insert(out->end(), msgs.begin(), msgs.end());
  char target[40] = "";
  if (o.target) std::snprintf(target, sizeof target, " target %08X", o.target);  // ABILVFX: a homing projectile's
  Log("replication: create %08X %-30s %-14s pos (%.2f, %.2f, %.2f) rot z %.1f team %u owner %08X flags %u marker %u%s%s; "
      "parts:%s; hp %.0f; %zu modifier record(s); %zu messages, %d bytes%s%s",
      o.id, o.name, NounTypeName(o.nounType), ci.pos[0], ci.pos[1], ci.pos[2], rz, ci.team, ci.owner,
      Field<std::uint8_t>(cd, create::kFlags), ci.markerId, ci.playerControlled ? " (player controlled)" : "", target,
      parts[0] ? parts : " none", o.hp, modifierRecords.size() / 41, msgs.size(), n,
      withFieldState ? "" : " (without field states: others knew it)", o.pendingDelete ? " (pending delete)" : "");
  return true;
}

bool BuildFullState(const NetObject& o, std::vector<WorldMessage>* out, std::vector<WorldMessage>* after, int* bytes) {
  *bytes = 0;
  if (!g_ready) return false;
  std::vector<WorldMessage> msgs, post;
  WorldMessage obj{IdMessage(gms::kGmsObjectUpdate, o.id), std::string("fog exit: object of ") + o.name};
  if (!AppendFull(obj.msg, o.object, kObject, nullptr, o.id, o.name)) return false;
  msgs.push_back(obj);
  alignas(8) unsigned char combatantCopy[combatant::kSize];
  for (int p = kPartProjectile; p < kNumParts; ++p) {
    if (!o.data[p]) continue;
    WorldMessage m{IdMessage(kParts[p].wire, o.id), std::string("fog exit: ") + kParts[p].name + " of " + o.name};
    if (!AppendFull(m.msg, WireData(o, p, combatantCopy, "fog exit"), kParts[p].type, nullptr, o.id, o.name))
      return false;
    msgs.push_back(m);
  }
  // D2g-live step 3: every override (the client may hold older ones), and after 95 the attached effects (cleared
  // first when there are any, so a slot re-created with the same asset and a new handle doesn't assert) and B1
  Bytes e9, b1, e2;
  AnimationMessages(o, true, &e9, &b1);
  if (!e9.empty()) msgs.push_back(WorldMessage{e9, std::string("fog exit: animation overrides of ") + o.name});
  int effects = 0;
  if (AttachedEffectsUpdate(o, &e2, &effects)) {
    if (effects > 0) {
      Bytes clear = IdMessage(gms::kGmsAttachedEffectUpdate, o.id);
      clear.push_back(0);
      post.push_back(WorldMessage{clear, std::string("fog exit: attached effects cleared on ") + o.name});
    }
    post.push_back(WorldMessage{e2, std::string("fog exit: attached effects of ") + o.name});
  }
  if (!b1.empty()) post.push_back(WorldMessage{b1, std::string("fog exit: last animation of ") + o.name});
  for (const auto& m : msgs) *bytes += static_cast<int>(m.msg.size());
  for (const auto& m : post) *bytes += static_cast<int>(m.msg.size());
  out->insert(out->end(), msgs.begin(), msgs.end());
  if (after) after->insert(after->end(), post.begin(), post.end());
  return true;
}

bool AttachedEffectsUpdate(const NetObject& o, Bytes* out, int* slots) {
  out->clear();
  *slots = 0;
  if (!g_ready) return false;
  const int mask = SehEffectSlots(o.object);
  if (mask < 0) return false;
  Bytes m = IdMessage(gms::kGmsAttachedEffectUpdate, o.id);
  m.push_back(0);  // the count, filled in below
  static unsigned char buf[256];
  for (int i = 0; i < kNumAttachedEffects; ++i) {
    if (!(mask & (1 << i))) continue;
    void* slot = static_cast<char*>(o.object) + kAttachedEffects + i * kAttachedEffectSize;
    int written = 0;
    const int n = WriteBody(slot, g_types[kEffect], kModeFull, nullptr, nullptr, false, buf, sizeof buf, &written);
    if (n <= 0) {
      Log("replication: attached effect %d of %08X (%s) not written (%d: %s)", i, o.id, o.name, n, WriteError(n));
      return false;
    }
    m.push_back(static_cast<std::uint8_t>(i));
    m.insert(m.end(), buf, buf + n);
    ++*slots;
  }
  m[5] = static_cast<std::uint8_t>(*slots);
  *out = std::move(m);
  return true;
}

bool AnimationMessages(const NetObject& o, bool allOverrides, Bytes* overrides, Bytes* init) {
  overrides->clear();
  init->clear();
  if (!g_ready) return false;
  AnimState a;
  if (!SehAnim(o.object, &a) || !a.present) return false;
  bool any = allOverrides;
  for (int k = 0; k < anim::kNumOverrides; ++k) any = any || a.overrides[k] != 0;
  if (any) {
    std::vector<int> members;
    for (int k = 0; k < anim::kNumOverrides; ++k) members.push_back(k);
    bool keep[128] = {};
    for (int k : members) keep[k] = true;
    static unsigned char buf[256];
    int written = 0;
    void* data = SehAnimData(o.object);
    const int n = data ? WriteBody(data, g_types[kAnim], kModeFull, keep, nullptr, false, buf, sizeof buf, &written) : -1;
    if (n > 0) {
      *overrides = IdMessage(gms::kGmsAnimationDataUpdate, o.id);
      overrides->insert(overrides->end(), buf, buf + n);
    } else {
      Log("replication: animation overrides of %08X (%s) not written (%d: %s)", o.id, o.name, n, WriteError(n));
    }
  }
  if (a.state != 0 || a.overlay != 0) {
    Bytes& b = *init;
    b = IdMessage(gms::kGmsInitAnimationInfo, o.id);
    auto put32 = [&b](std::uint32_t v) { PutU32(b, v); };
    put32(a.state);
    put32(static_cast<std::uint32_t>(a.playTime));
    put32(static_cast<std::uint32_t>(a.playTime >> 32));
    std::uint32_t scale;
    std::memcpy(&scale, &a.timeScale, 4);
    put32(scale);
    put32(a.overlay);
    put32(static_cast<std::uint32_t>(a.overlayTime));
    put32(static_cast<std::uint32_t>(a.overlayTime >> 32));
  }
  return true;
}

int AnimationOverrideDelta(const NetObject& o, std::uint32_t* shadow, bool* seeded, Bytes* out) {
  out->clear();
  if (!g_ready) return 0;
  AnimState a;
  if (!SehAnim(o.object, &a) || !a.present) return 0;
  if (!*seeded) {
    std::memcpy(shadow, a.overrides, sizeof a.overrides);
    *seeded = true;
    return 0;
  }
  bool keep[128] = {};
  int changed = 0;
  for (int k = 0; k < anim::kNumOverrides; ++k)
    if (a.overrides[k] != shadow[k]) {
      keep[k] = true;
      ++changed;
    }
  if (!changed) return 0;
  std::memcpy(shadow, a.overrides, sizeof a.overrides);
  static unsigned char buf[256];
  int written = 0;
  void* data = SehAnimData(o.object);
  const int n = data ? WriteBody(data, g_types[kAnim], kModeFull, keep, nullptr, false, buf, sizeof buf, &written) : -1;
  if (n <= 0) {
    Log("replication: animation override delta of %08X (%s) not written (%d: %s)", o.id, o.name, n, WriteError(n));
    return -1;
  }
  *out = IdMessage(gms::kGmsAnimationDataUpdate, o.id);
  out->insert(out->end(), buf, buf + n);
  return written;
}

int BuildDelta(const NetObject& o, int p, Bytes* out) {
  out->clear();
  if (!g_ready || p < 0 || p >= kNumParts || p == kPartProjectile || !o.data[p]) return 0;
  void* fs = SehFieldState(o, p);
  if (!fs) return 0;
  void* type = g_types[kParts[p].type];
  alignas(8) unsigned char combatantCopy[combatant::kSize];
  void* data = WireData(o, p, combatantCopy, "delta");
  if (!AnyDelta(data, type, fs, kModeIncremental)) return 0;
  static unsigned char buf[2048];
  int written = 0;
  const int n = WriteBody(data, type, kModeIncremental, nullptr, fs, true, buf, sizeof buf, &written);
  if (n < 0) {
    Log("replication: delta %s of %08X (%s) not written (%d: %s)", kParts[p].name, o.id, o.name, n, WriteError(n));
    return n;
  }
  if (n == 0) return 0;
  *out = IdMessage(kParts[p].wire, o.id);
  out->insert(out->end(), buf, buf + n);
  return written;
}

bool AddPlayerBits(void* object, std::uint16_t known, std::uint16_t visible) {
  __try {
    char* nc = Field<char*>(object, gobj::kNetwork);
    if (!nc) return false;
    Field<std::uint16_t>(nc, kNetworkKnownMask) |= known;
    Field<std::uint16_t>(nc, kNetworkVisibleMask) |= visible;
    // D2g-live step 3: kNetFlag_SentCreationMessage, which the stripped creation path set: the server's own attached-
    // effect events (GameObject_CreateAttachedEffect 0x00881D30, _DestroyAttachedEffect 0x00880A40) go out only with it
    if (known) Field<std::uint8_t>(nc, kNetworkFlags) |= kNetFlagSentCreationMessage;
    return true;
  } __except (SehNote(GetExceptionInformation(), "AddPlayerBits")) {
    return false;
  }
}

bool RemovePlayerBits(void* object, std::uint16_t bits) {
  __try {
    char* nc = Field<char*>(object, gobj::kNetwork);
    if (!nc) return false;
    Field<std::uint16_t>(nc, kNetworkKnownMask) &= static_cast<std::uint16_t>(~bits);
    Field<std::uint16_t>(nc, kNetworkVisibleMask) &= static_cast<std::uint16_t>(~bits);
    return true;
  } __except (SehNote(GetExceptionInformation(), "RemovePlayerBits")) {
    return false;
  }
}

bool ObjectSeers(std::uint32_t id, std::uint16_t* seers) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    char* nc = Field<char*>(o, gobj::kNetwork);
    *seers = nc ? static_cast<std::uint16_t>(Field<std::uint16_t>(nc, kNetworkKnownMask) &
                                             Field<std::uint16_t>(nc, kNetworkVisibleMask))
                : 0;
    return true;
  } __except (SehNote(GetExceptionInformation(), "ObjectSeers")) {
    return false;
  }
}

bool ObjectMasks(std::uint32_t id, std::uint16_t* known, std::uint16_t* visible) {
  __try {
    void* o = GetObjectById(id);
    if (!o) return false;
    char* nc = Field<char*>(o, gobj::kNetwork);
    if (!nc) return false;
    *known = Field<std::uint16_t>(nc, kNetworkKnownMask);
    *visible = Field<std::uint16_t>(nc, kNetworkVisibleMask);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ObjectMasks")) {
    return false;
  }
}

bool InstanceExists(std::uint32_t instance) {
  __try {
    return GetInstance(instance) != nullptr;
  } __except (SehNote(GetExceptionInformation(), "GetInstance")) {
    return true;  // keep it registered
  }
}

void BuildWorld(void* sim, unsigned index, bool all, RecordsFn records, void* ctx, std::vector<WorldMessage>* out,
                WorldCounts* counts) {
  *counts = WorldCounts{};
  if (!g_ready) return;
  std::vector<NetObject> objs;
  const int n = Scan(sim, &objs);
  if (n < 0) return;
  counts->networked = n;
  const std::uint16_t bit = static_cast<std::uint16_t>(1u << index);
  // D9b: every object after its owner (the client maps the full state's mOwnerID through its id map, an unknown id
  // becomes 0); otherwise the object manager's order
  std::vector<gms::CreateOrderItem> items(objs.size());
  for (size_t k = 0; k < objs.size(); ++k)
    items[k] = gms::CreateOrderItem{objs[k].id, objs[k].owner, objs[k].parent, objs[k].target};
  for (const size_t k : gms::CreateOrder(items)) {
    const NetObject& o = objs[k];
    if (!Replicated(o, all)) {
      ++counts->skipped;
      Log("replication: %08X %-30s %-14s not sent (not a replicated noun type) pos (%.2f, %.2f, %.2f)", o.id, o.name,
          NounTypeName(o.nounType), o.pos[0], o.pos[1], o.pos[2]);
      continue;
    }
    if (o.known & bit) continue;
    const size_t before = out->size();
    int b = 0;
    const Bytes recs = records ? records(ctx, o.id) : Bytes();
    if (!BuildCreate(o, o.known == 0, recs, out, &b) || !AddPlayerBits(o.object, bit, bit)) {
      out->resize(before);
      ++counts->failed;
      continue;
    }
    ++counts->created;
    if (o.data[kPartAttributes] || o.data[kPartCombatant]) ++counts->withHp;
    counts->bytes += b;
  }
  counts->messages = static_cast<int>(out->size());
  Log("replication: world for player %u: %d networked objects, %d created (%d with attributes or HP), %d not sent, "
      "%d failed; %d messages, %d bytes",
      index, counts->networked, counts->created, counts->withHp, counts->skipped, counts->failed, counts->messages,
      counts->bytes);
}

int PendingOutgoing(void* sim) {
  int entries = 0;
  unsigned bytes = 0;
  return MeasureOutgoing(sim, &entries, &bytes) ? entries : -1;
}

void Drain(void* sim, std::vector<Outgoing>* out, DrainCounts* counts) {
  *counts = DrainCounts{};
  int entries = 0;
  unsigned total = 0;
  if (!MeasureOutgoing(sim, &entries, &total) || entries <= 0) return;
  std::vector<RawEntry> raw(static_cast<size_t>(entries));
  std::vector<unsigned char> bytes(total + 1);
  const int n = ReadOutgoing(sim, raw.data(), entries, bytes.data(), total, &counts->released);
  if (n < 0) return;
  ClearOutgoing(sim);
  counts->entries = n;
  for (int k = 0; k < n; ++k) {
    const RawEntry& r = raw[k];
    Outgoing o;
    o.kind = r.kind;
    o.object = r.object;
    o.recipients = static_cast<std::uint16_t>(r.recipients);
    o.objectAlive = r.objectAlive;
    o.seers = r.seers;
    o.hasPoint = r.hasPoint;
    o.x = r.x;
    o.y = r.y;
    o.radius = r.radius;
    o.team = r.team;
    if (r.kind < 0 || r.kind > 3) {
      ++counts->unreadable;
      Log("replication: outgoing entry %d of unknown kind %d dropped", k, r.kind);
      continue;
    }
    ++counts->kinds[r.kind];
    if (r.kind == 0) {
      o.msg = IdMessage(gms::kGmsObjectDelete, r.object);  // ObjectDelete (91: n x id) to the players that knew it
    } else {
      if (!r.hasMessage) {
        ++counts->unreadable;
        Log("replication: outgoing entry %d (kind %d, object %08X) has no readable message; dropped", k, r.kind,
            r.object);
        continue;
      }
      o.msg.push_back(r.wire);
      o.msg.insert(o.msg.end(), bytes.begin() + r.offset, bytes.begin() + r.offset + r.length);
    }
    out->push_back(std::move(o));
  }
  // A ClientAbilityMessage (AE) names objects the script has just removed (Sakari's "forcefield_remove": RemoveObjectById,
  // then SendMessageToClientAbility), and the client decodes an id through its server-to-client id table, which
  // GameClient_OnGmsObjectDelete (0x006B7F60) empties at once: an AE behind the ObjectDelete reads the id as 0 and
  // removes nothing, so the ghost kept snapping to icebergs that were gone. The deletes in front of an AE go behind the
  // last one of the tick (stable; the other messages keep their order).
  std::size_t lastAe = 0;
  bool anyAe = false;
  for (std::size_t k = 0; k < out->size(); ++k) {
    const Outgoing& o = (*out)[k];
    if (o.kind != 0 && !o.msg.empty() && o.msg[0] == gms::kGmsClientAbilityMessage) {
      lastAe = k;
      anyAe = true;
    }
  }
  if (anyAe)
    std::stable_partition(out->begin(), out->begin() + static_cast<std::ptrdiff_t>(lastAe) + 1,
                          [](const Outgoing& o) { return o.kind != 0; });
}

namespace {
bool SehPool(void* sim, int* inUse, int* capacity) {
  __try {
    char* los = Field<char*>(sim, los::kOfSim);
    if (!los) return false;
    *inUse = Field<int>(los, los::kPoolInUse);
    *capacity = Field<int>(los, los::kPoolCapacity);
    return true;
  } __except (SehNote(GetExceptionInformation(), "LineOfSightPool")) {
    return false;
  }
}
}  // namespace

bool LineOfSightPool(void* sim, int* inUse, int* capacity) { return SehPool(sim, inUse, capacity); }

bool UpdateNetworkRateTimers(void* sim, unsigned* mask) {
  if (!g_netRateOk) return false;
  static bool checked;
  if (!checked) {
    checked = true;
    if (!CodeMatches(kUpdateNetworkRateTimers, kUpdateNetworkRateTimersBytes, sizeof kUpdateNetworkRateTimersBytes)) {
      Log("replication: GameSimulator_UpdateNetworkRateTimers at %08X has other bytes; network-rate timers off",
          static_cast<unsigned>(kUpdateNetworkRateTimers));
      g_netRateOk = false;
      return false;
    }
  }
  if (!SehNetworkRate(sim, mask)) {
    g_netRateOk = false;
    Log("replication: network-rate timers off after an exception");
    return false;
  }
  return true;
}

// ---- players --------------------------------------------------------------------------------------------------------

namespace {

// `head` (the wire byte and the index or id), then the members `members` of `data` laid out as `slot`, written with
// `fieldState` (may be null).
bool SelectedUpdate(void* data, TypeSlot slot, void* fieldState, const std::vector<int>& members, Bytes head,
                    const char* what, Bytes* out) {
  bool keep[128] = {};
  for (int m : members)
    if (m >= 0 && m < 128) keep[m] = true;
  unsigned char buf[1024];
  int written = 0;
  const int n = WriteBody(data, g_types[slot], kModeFull, keep, fieldState, false, buf, sizeof buf, &written);
  if (n <= 0 || written != static_cast<int>(members.size())) {
    Log("replication: %s (%s) not written (%d, %d of %zu members)", what, kTypeNames[slot], n, written,
        members.size());
    return false;
  }
  *out = std::move(head);
  out->insert(out->end(), buf, buf + n);
  return true;
}

int PlayerDelta(void* sim, unsigned index, TypeSlot slot, std::uintptr_t fsOff, std::uint8_t wire, Bytes* out) {
  out->clear();
  if (!g_ready) return 0;
  void* p = SehPlayer(sim, index);
  if (!p) return 0;
  void* fs = static_cast<char*>(p) + fsOff;
  if (!AnyDelta(p, g_types[slot], fs, kModeIncremental)) return 0;
  unsigned char buf[1024];
  int written = 0;
  const int n = WriteBody(p, g_types[slot], kModeIncremental, nullptr, fs, true, buf, sizeof buf, &written);
  if (n < 0) {
    Log("replication: %s delta of player %u not written (%d: %s)", kTypeNames[slot], index, n, WriteError(n));
    return n;
  }
  if (n == 0) return 0;
  out->push_back(wire);
  out->push_back(static_cast<std::uint8_t>(index));
  out->insert(out->end(), buf, buf + n);
  return written;
}

bool SehTeam(void* sim, unsigned index, std::uint8_t* team) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    *team = Field<std::uint8_t>(p, player::kTeam);
    return true;
  } __except (SehNote(GetExceptionInformation(), "PlayerTeam")) {
    return false;
  }
}

void* SehLocomotionOf(void* object) {
  __try {
    return Field<void*>(object, loco::kOfObject);
  } __except (SehNote(GetExceptionInformation(), "LocomotionOf")) {
    return nullptr;
  }
}

}  // namespace

bool PlayerUpdate(void* sim, unsigned index, const std::vector<int>& members, Bytes* out) {
  if (!g_ready || members.empty()) return false;
  void* p = SehPlayer(sim, index);
  if (!p) {
    Log("replication: no server player %u for a player update", index);
    return false;
  }
  return SelectedUpdate(p, kPlayer, static_cast<char*>(p) + part::kFsPlayerGlobal, members,
                        Bytes{gms::kGmsLabsPlayerGlobalUpdate, static_cast<std::uint8_t>(index)}, "player update", out);
}

bool PlayerFowUpdate(void* sim, unsigned index, const std::vector<int>& members, Bytes* out) {
  if (!g_ready || members.empty()) return false;
  void* p = SehPlayer(sim, index);
  if (!p) {
    Log("replication: no server player %u for a FoW player update", index);
    return false;
  }
  return SelectedUpdate(p, kFow, static_cast<char*>(p) + part::kFsPlayerFow, members,
                        Bytes{gms::kGmsLabsPlayerFowUpdate, static_cast<std::uint8_t>(index)}, "FoW player update", out);
}

int PlayerGlobalDelta(void* sim, unsigned index, Bytes* out) {
  return PlayerDelta(sim, index, kPlayer, part::kFsPlayerGlobal, gms::kGmsLabsPlayerGlobalUpdate, out);
}

int PlayerFowDelta(void* sim, unsigned index, Bytes* out) {
  return PlayerDelta(sim, index, kFow, part::kFsPlayerFow, gms::kGmsLabsPlayerFowUpdate, out);
}

bool PlayerTeam(void* sim, unsigned index, std::uint8_t* team) { return SehTeam(sim, index, team); }

bool PlayerLevel(void* sim, unsigned index, std::uint32_t* level) {
  __try {
    void* p = GetPlayerByIndex(sim, index);
    if (!p) return false;
    *level = Field<std::uint32_t>(p, player::kLevel);
    return true;
  } __except (SehNote(GetExceptionInformation(), "PlayerLevel")) {
    return false;
  }
}

bool LocomotionUpdate(void* object, std::uint32_t id, const std::vector<int>& members, Bytes* out) {
  if (!g_ready || members.empty() || !object) return false;
  void* locomotion = SehLocomotionOf(object);
  if (!locomotion) return false;
  return SelectedUpdate(locomotion, kLoco, static_cast<char*>(locomotion) + part::kFsLocomotion, members,
                        IdMessage(gms::kGmsLocomotionDataUpdate, id), "locomotion update", out);
}

// ---- the HUD's battleground data (D1c-live) ---------------------------------------------------------------------------

namespace {

void* SehUiNodes(void* sim, int* count) {
  __try {
    char* ui = static_cast<char*>(sim) + matchsim::kUIData;
    *count = Field<int>(ui, ui::kNodeCapacity);
    return Field<void*>(ui, ui::kNodeList);
  } __except (SehNote(GetExceptionInformation(), "UI nodes")) {
    return nullptr;
  }
}

// One body of `data` (as `slot`): the full state (mode 1, the frequency-1 members too), written with the field state if
// `withFieldState`; or the members that changed since the field state (mode 8).
int UiBody(void* data, TypeSlot slot, void* fieldState, bool full, bool withFieldState, unsigned char* buf, unsigned cap,
           int* written) {
  *written = 0;
  if (full)
    return WriteBody(data, g_types[slot], kModeFull, nullptr, withFieldState ? fieldState : nullptr, false, buf, cap,
                     written);
  if (!AnyDelta(data, g_types[slot], fieldState, kModeIncremental)) return 0;
  return WriteBody(data, g_types[slot], kModeIncremental, nullptr, fieldState, true, buf, cap, written);
}

}  // namespace

int UIDataUpdate(void* sim, bool full, bool withFieldState, Bytes* out) {
  out->clear();
  if (!g_ready) return 0;
  char* ui = static_cast<char*>(sim) + matchsim::kUIData;
  static unsigned char buf[2048];
  int written = 0;
  const int n = UiBody(ui, kUI, ui + ui::kFieldState, full, withFieldState, buf, sizeof buf, &written);
  if (n < 0) {
    Log("replication: UIDataState (%s) not written (%d: %s)", full ? "full" : "delta", n, WriteError(n));
    return n;
  }
  if (n == 0) return 0;
  out->push_back(gms::kGmsUIDataState);
  out->insert(out->end(), buf, buf + n);
  return written;
}

int SpiritWellUpdate(void* sim, bool full, bool withFieldState, Bytes* out) {
  out->clear();
  if (!g_ready) return 0;
  int count = 0;
  char* nodes = static_cast<char*>(SehUiNodes(sim, &count));
  if (!nodes || count <= 0) return 0;
  if (count > 8) count = 8;  // the mask is one byte
  static unsigned char buf[1024];
  Bytes body;
  std::uint8_t mask = 0;
  int total = 0;
  for (int i = 0; i < count; ++i) {
    char* nd = nodes + i * ui::kNodeSize;
    int written = 0;
    const int n = UiBody(nd, kNode, nd + ui::node::kFieldState, full, withFieldState, buf, sizeof buf, &written);
    if (n < 0) {
      Log("replication: UISpiritWellUpdate node %d (%s) not written (%d: %s)", i, full ? "full" : "delta", n,
          WriteError(n));
      return n;
    }
    if (n == 0) continue;
    mask = static_cast<std::uint8_t>(mask | (1u << i));
    body.insert(body.end(), buf, buf + n);
    total += written;
  }
  if (!mask) return 0;
  out->push_back(gms::kGmsUISpiritWellUpdate);
  out->push_back(mask);
  out->insert(out->end(), body.begin(), body.end());
  return total;
}

namespace {
char* SehUiCamps(void* sim, int* count) {
  __try {
    char* ui = static_cast<char*>(sim) + matchsim::kUIData;
    *count = Field<int>(ui, ui::kCampCapacity);
    return Field<char*>(ui, ui::kCampList);
  } __except (SehNote(GetExceptionInformation(), "UI camps")) {
    return nullptr;
  }
}
std::uint32_t SehCampMarker(char* c) {
  __try {
    return Field<std::uint32_t>(c, camp::kSpawnMarkerId);
  } __except (SehNote(GetExceptionInformation(), "UI camp")) {
    return 0;
  }
}
}  // namespace

int CampUpdate(void* sim, bool full, bool withFieldState, Bytes* out) {
  out->clear();
  if (!g_ready) return 0;
  int count = 0;
  char* camps = SehUiCamps(sim, &count);
  if (!camps || count <= 0) return 0;
  if (count > 32) count = 32;  // the mask is a u32
  static unsigned char buf[256];
  Bytes body;
  std::uint32_t mask = 0;
  int total = 0;
  for (int i = 0; i < count; ++i) {
    char* c = camps + i * kCampSize;
    if (full && !SehCampMarker(c)) continue;  // an entry the director never added (AIDirector's camp pool has 32)
    int written = 0;
    const int n = UiBody(c, kCamp, c + camp::kFieldState, full, withFieldState, buf, sizeof buf, &written);
    if (n < 0) {
      Log("replication: UICreepCampUpdate camp %d (%s) not written (%d: %s)", i, full ? "full" : "delta", n,
          WriteError(n));
      return n;
    }
    if (n == 0) continue;
    mask |= 1u << i;
    body.insert(body.end(), buf, buf + n);
    total += written;
  }
  if (!mask) return 0;
  out->push_back(gms::kGmsUICreepCampUpdate);
  PutU32(*out, mask);
  out->insert(out->end(), body.begin(), body.end());
  return total;
}

namespace {
// Copies `n` bytes of the server's camp list (under SEH); false on an exception.
bool SehCopyCamps(const char* from, char* to, std::size_t n) {
  __try {
    std::memcpy(to, from, n);
    return true;
  } __except (SehNote(GetExceptionInformation(), "UI camp view")) {
    return false;
  }
}
}  // namespace

int CampViewUpdate(void* sim, CampViewBuffer* view, int (*bitsOf)(std::uint32_t marker), bool full,
                   bool withFieldState, Bytes* out) {
  out->clear();
  if (!g_ready || !view) return 0;
  int count = 0;
  char* camps = SehUiCamps(sim, &count);
  if (!camps || count <= 0) return 0;
  if (count > 32) count = 32;  // the mask is a u32
  const std::size_t size = static_cast<std::size_t>(count) * kCampSize;
  if (view->size() != size) {  // the first call: the whole entries, the field states as the engine set them up
    view->assign(size, 0);
    if (!SehCopyCamps(camps, view->data(), size)) {
      view->clear();
      return 0;
    }
  }
  static unsigned char buf[256];
  Bytes body;
  std::uint32_t mask = 0;
  int total = 0;
  for (int i = 0; i < count; ++i) {
    char* v = view->data() + i * kCampSize;
    if (!SehCopyCamps(camps + i * kCampSize, v, camp::kFieldState)) return 0;  // mCampVisibleToTeam, mSpawnMarkerId
    std::uint32_t marker = 0;
    std::memcpy(&marker, v + camp::kSpawnMarkerId, 4);
    if (bitsOf && marker) {
      const int b = bitsOf(marker);
      if (b >= 0) v[0] = static_cast<char>(b);
    }
    if (full && !marker) continue;
    int written = 0;
    const int n = UiBody(v, kCamp, v + camp::kFieldState, full, withFieldState, buf, sizeof buf, &written);
    if (n < 0) {
      Log("replication: UICreepCampUpdate (spectators) camp %d (%s) not written (%d: %s)", i, full ? "full" : "delta",
          n, WriteError(n));
      return n;
    }
    if (n == 0) continue;
    mask |= 1u << i;
    body.insert(body.end(), buf, buf + n);
    total += written;
  }
  if (!mask) return 0;
  out->push_back(gms::kGmsUICreepCampUpdate);
  PutU32(*out, mask);
  out->insert(out->end(), body.begin(), body.end());
  return total;
}

namespace {
void SehLogFow(int first, int last) {
  __try {
    void* t = g_types[kFow];
    const unsigned char* records = Field<unsigned char*>(t, 8);
    const int n = Field<int>(t, 0xC);
    for (int i = first; i <= last && i < n; ++i) {
      const unsigned char* rec = records + i * kMemberRecordSize;
      Log("replication: labsPlayerFowData member %d at run time: offset +0x%03X, %u x %u bytes, frequency 0x%02X", i,
          U32At(rec, 0x14), U32At(rec, 0x18), U32At(rec, 8), rec[0x44]);
    }
  } __except (SehNote(GetExceptionInformation(), "LogPlayerFowMembers")) {
  }
}
}  // namespace

void LogPlayerFowMembers(int first, int last) {
  if (g_ready) SehLogFow(first, last);
}

}  // namespace repl
