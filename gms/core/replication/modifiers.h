// The host's modifier registry (gms/core, D2g-live; docs/GMS_PROTOCOL.md *Live replication (D2g-static)*, *Modifiers*).
// No game addresses: the engine side (gms/engine/replicate.cpp) feeds it the simulator's own messages and asks it
// what to send.
//
// The simulator builds ModifierCreated (AB, one 41-byte record) and ModifierUpdated (AC, 25 bytes) itself, but
// ModifierDeleted (AD) nowhere (LabsAbilities_RemoveInstance 0x00905D40 sends nothing), and the records that
// ObjectCreateComplete (8F) and ObjectExitFoW (95) carry are built nowhere either. So the host keeps every record it
// drained, instance -> {carrier, 41 bytes}:
//   - AB registers a record (whoever receives it), AC updates its start, duration, stacks and "holds agent";
//   - once the instance is gone on the server (LabsAbilities_GetInstance 0x008FF220 finds nothing), the host erases
//     it and sends AD {carrier, instance} to the players that know the carrier;
//   - 8F/95 carry the registry's records of their object (late joiners, fog exits), in creation order.
//
// The 41-byte record (GameSimulator_SendModifierCreated 0x008F57A0; the reader AgentSummary_OnModifierCreated
// 0x00658480): +0 carrier (target object id), +4 caster id, +8 ability id, +12 instance id, +16 duration ms, +20 rank,
// +24 stack count, +28 u64 start (server ms), +36 u8 holds agent (the instance's +0xEC), +37 u32 feedback flags.
// ModifierUpdated (GameClient_ApplyModifierUpdate 0x006B55B0): u32 carrier, u32 instance, u64 start (-1 keeps it),
// u32 duration, u32 stacks, u8 holds agent = record bytes 28, 16, 24 and 36.
// ModifierDeleted (reader 0x006B9FF0 -> AgentSummary_OnModifierDeleted 0x00657F40): u32 carrier, u32 instance; the
// client ignores an unknown carrier or instance.
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <vector>

#include "core/common/bytes.h"

namespace gms {

constexpr size_t kModifierRecordSize = 41;
constexpr size_t kModifierUpdateSize = 25;
constexpr size_t kModifierDeletedSize = 8;

// The fields of a 41-byte record.
struct ModifierRecord {
  uint32_t carrier = 0, caster = 0, ability = 0, instance = 0, durationMs = 0, rank = 0, stacks = 0;
  uint64_t startMs = 0;
  uint8_t holdsAgent = 0;
  uint32_t feedback = 0;
};
bool ParseModifierRecord(const uint8_t* p, size_t n, ModifierRecord* out);  // n >= 41
Bytes BuildModifierRecord(const ModifierRecord& r);                          // 41 bytes

// ModifierUpdated's body.
struct ModifierUpdate {
  uint32_t carrier = 0, instance = 0;
  uint64_t startMs = 0;  // ~0 = keep
  uint32_t durationMs = 0, stacks = 0;
  uint8_t holdsAgent = 0;
};
bool ParseModifierUpdate(const uint8_t* p, size_t n, ModifierUpdate* out);  // n >= 25

// AD + u32 carrier + u32 instance (9 bytes with the wire byte).
Bytes BuildModifierDeleted(uint32_t carrier, uint32_t instance);

class ModifierRegistry {
 public:
  // AB's body (exactly one record). An instance already registered is replaced (its creation order kept). False if
  // the body is not 41 bytes or the instance id is 0.
  bool OnCreated(const uint8_t* body, size_t n);
  // AC's body. False if malformed or the instance is unknown (nothing changes then).
  bool OnUpdated(const uint8_t* body, size_t n);

  // Every registered instance id, in creation order.
  std::vector<uint32_t> Instances() const;
  bool Has(uint32_t instance) const { return byInstance_.count(instance) != 0; }
  // The carrier of a registered instance (0 if unknown).
  uint32_t CarrierOf(uint32_t instance) const;

  // Erases an instance; `ad` gets its ModifierDeleted. False (nothing written) if unknown.
  bool Remove(uint32_t instance, Bytes* ad);
  // Erases every record of a carrier without ModifierDeleted (the object itself was deleted). Returns how many.
  int RemoveCarrier(uint32_t carrier);

  // The carrier's records, concatenated in creation order (ObjectCreateComplete's / ObjectExitFoW's tail).
  Bytes RecordsFor(uint32_t carrier) const;
  int CountFor(uint32_t carrier) const;
  size_t Size() const { return byInstance_.size(); }

 private:
  struct Entry {
    uint64_t order = 0;
    uint32_t carrier = 0;
    Bytes record;  // 41 bytes, kept up to date with AC
  };
  std::map<uint32_t, Entry> byInstance_;
  uint64_t nextOrder_ = 0;
};

// BUFFICON test aid ([host] modifier_trace): which modifier messages to log one by one. The spec is a list of static
// ability GUIDs in hex ("108B48DA,0x33B6C2B7"; any separator) or "all". An AB whose ability is traced makes its
// instance traced, until its AD; an AC or AD is traced when its instance is.
class ModifierTrace {
 public:
  // Replaces the set; returns how many GUIDs it names (-1 for "all", 0 = off).
  int Configure(const char* spec);
  bool On() const { return all_ || !guids_.empty(); }
  // AB's body: true if the record's ability is traced (its instance is remembered).
  bool OnCreated(const uint8_t* body, size_t n);
  // AC's body (or any body that starts with u32 carrier, u32 instance): true if the instance is traced.
  bool OnUpdated(const uint8_t* body, size_t n) const;
  // The instance ended (the host's AD): true if it was traced (it is forgotten).
  bool OnDeleted(uint32_t instance);
  // The traced ability of an instance (0 if not traced).
  uint32_t AbilityOf(uint32_t instance) const;

 private:
  bool all_ = false;
  std::vector<uint32_t> guids_;
  std::map<uint32_t, uint32_t> instances_;  // instance -> ability GUID
};

}  // namespace gms
