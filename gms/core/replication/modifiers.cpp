// The host's modifier registry (D2g-live): see modifiers.h.
#include "core/replication/modifiers.h"

#include <algorithm>
#include <cstring>
#include <utility>

#include "core/protocol/wire.h"

namespace gms {

bool ParseModifierRecord(const uint8_t* p, size_t n, ModifierRecord* out) {
  if (!p || n < kModifierRecordSize) return false;
  out->carrier = GetU32LE(p + 0);
  out->caster = GetU32LE(p + 4);
  out->ability = GetU32LE(p + 8);
  out->instance = GetU32LE(p + 12);
  out->durationMs = GetU32LE(p + 16);
  out->rank = GetU32LE(p + 20);
  out->stacks = GetU32LE(p + 24);
  out->startMs = GetU64LE(p + 28);
  out->holdsAgent = p[36];
  out->feedback = GetU32LE(p + 37);
  return true;
}

Bytes BuildModifierRecord(const ModifierRecord& r) {
  Bytes b;
  PutU32LE(b, r.carrier);
  PutU32LE(b, r.caster);
  PutU32LE(b, r.ability);
  PutU32LE(b, r.instance);
  PutU32LE(b, r.durationMs);
  PutU32LE(b, r.rank);
  PutU32LE(b, r.stacks);
  PutU64LE(b, r.startMs);
  PutU8(b, r.holdsAgent);
  PutU32LE(b, r.feedback);
  return b;
}

bool ParseModifierUpdate(const uint8_t* p, size_t n, ModifierUpdate* out) {
  if (!p || n < kModifierUpdateSize) return false;
  out->carrier = GetU32LE(p + 0);
  out->instance = GetU32LE(p + 4);
  out->startMs = GetU64LE(p + 8);
  out->durationMs = GetU32LE(p + 16);
  out->stacks = GetU32LE(p + 20);
  out->holdsAgent = p[24];
  return true;
}

Bytes BuildModifierDeleted(uint32_t carrier, uint32_t instance) {
  Bytes b;
  PutU8(b, kGmsModifierDeleted);
  PutU32LE(b, carrier);
  PutU32LE(b, instance);
  return b;
}

bool ModifierRegistry::OnCreated(const uint8_t* body, size_t n) {
  ModifierRecord r;
  if (n != kModifierRecordSize || !ParseModifierRecord(body, n, &r) || r.instance == 0) return false;
  auto it = byInstance_.find(r.instance);
  if (it == byInstance_.end()) {
    Entry e;
    e.order = nextOrder_++;
    it = byInstance_.emplace(r.instance, std::move(e)).first;
  }
  it->second.carrier = r.carrier;
  it->second.record.assign(body, body + kModifierRecordSize);
  return true;
}

bool ModifierRegistry::OnUpdated(const uint8_t* body, size_t n) {
  ModifierUpdate u;
  if (n != kModifierUpdateSize || !ParseModifierUpdate(body, n, &u)) return false;
  auto it = byInstance_.find(u.instance);
  if (it == byInstance_.end()) return false;
  Bytes& rec = it->second.record;
  auto put32 = [&rec](size_t at, uint32_t v) {
    for (int k = 0; k < 4; ++k) rec[at + k] = static_cast<uint8_t>(v >> (8 * k));
  };
  if (u.startMs != ~0ull)
    for (int k = 0; k < 8; ++k) rec[28 + k] = static_cast<uint8_t>(u.startMs >> (8 * k));
  put32(16, u.durationMs);
  put32(24, u.stacks);
  rec[36] = u.holdsAgent;
  return true;
}

std::vector<uint32_t> ModifierRegistry::Instances() const {
  std::vector<std::pair<uint64_t, uint32_t>> v;
  v.reserve(byInstance_.size());
  for (const auto& kv : byInstance_) v.emplace_back(kv.second.order, kv.first);
  std::sort(v.begin(), v.end());
  std::vector<uint32_t> out;
  out.reserve(v.size());
  for (const auto& p : v) out.push_back(p.second);
  return out;
}

uint32_t ModifierRegistry::CarrierOf(uint32_t instance) const {
  auto it = byInstance_.find(instance);
  return it == byInstance_.end() ? 0 : it->second.carrier;
}

bool ModifierRegistry::Remove(uint32_t instance, Bytes* ad) {
  auto it = byInstance_.find(instance);
  if (it == byInstance_.end()) return false;
  if (ad) *ad = BuildModifierDeleted(it->second.carrier, instance);
  byInstance_.erase(it);
  return true;
}

int ModifierRegistry::RemoveCarrier(uint32_t carrier) {
  int n = 0;
  for (auto it = byInstance_.begin(); it != byInstance_.end();) {
    if (it->second.carrier == carrier) {
      it = byInstance_.erase(it);
      ++n;
    } else {
      ++it;
    }
  }
  return n;
}

Bytes ModifierRegistry::RecordsFor(uint32_t carrier) const {
  std::vector<std::pair<uint64_t, const Bytes*>> v;
  for (const auto& kv : byInstance_)
    if (kv.second.carrier == carrier) v.emplace_back(kv.second.order, &kv.second.record);
  std::sort(v.begin(), v.end(), [](const auto& a, const auto& b) { return a.first < b.first; });
  Bytes out;
  for (const auto& p : v) out.insert(out.end(), p.second->begin(), p.second->end());
  return out;
}

int ModifierRegistry::CountFor(uint32_t carrier) const {
  int n = 0;
  for (const auto& kv : byInstance_)
    if (kv.second.carrier == carrier) ++n;
  return n;
}

int ModifierTrace::Configure(const char* spec) {
  all_ = false;
  guids_.clear();
  instances_.clear();
  if (!spec) return 0;
  for (const char* p = spec; *p;) {
    if ((p[0] == 'a' || p[0] == 'A') && (p[1] == 'l' || p[1] == 'L') && (p[2] == 'l' || p[2] == 'L')) {
      all_ = true;
      p += 3;
      continue;
    }
    if (p[0] == '0' && (p[1] == 'x' || p[1] == 'X')) p += 2;
    uint32_t v = 0;
    int digits = 0;
    for (;; ++p, ++digits) {
      const char c = *p;
      int d;
      if (c >= '0' && c <= '9') d = c - '0';
      else if (c >= 'a' && c <= 'f') d = c - 'a' + 10;
      else if (c >= 'A' && c <= 'F') d = c - 'A' + 10;
      else break;
      v = (v << 4) | static_cast<uint32_t>(d);
    }
    if (digits > 0 && digits <= 8 && v != 0 && std::find(guids_.begin(), guids_.end(), v) == guids_.end())
      guids_.push_back(v);
    if (digits == 0) ++p;  // a separator
  }
  return all_ ? -1 : static_cast<int>(guids_.size());
}

bool ModifierTrace::OnCreated(const uint8_t* body, size_t n) {
  ModifierRecord r;
  if (!On() || !ParseModifierRecord(body, n, &r) || r.instance == 0) return false;
  if (!all_ && std::find(guids_.begin(), guids_.end(), r.ability) == guids_.end()) return false;
  instances_[r.instance] = r.ability;
  return true;
}

bool ModifierTrace::OnUpdated(const uint8_t* body, size_t n) const {
  if (!body || n < 8 || instances_.empty()) return false;
  return instances_.count(GetU32LE(body + 4)) != 0;
}

bool ModifierTrace::OnDeleted(uint32_t instance) { return instances_.erase(instance) != 0; }

uint32_t ModifierTrace::AbilityOf(uint32_t instance) const {
  const auto it = instances_.find(instance);
  return it == instances_.end() ? 0 : it->second;
}

}  // namespace gms
