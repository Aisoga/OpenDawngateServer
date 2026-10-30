// M18 (MSTATS4): crowd control caused and endured; see cc.h.
#include "core/stats/cc.h"

#include <algorithm>

namespace gms::stats {

namespace {
constexpr int kSeatCount = 10;
bool ValidSeat(int s) { return s >= 0 && s < kSeatCount; }
}  // namespace

CcKind CcKindOf(unsigned d) {
  if (d & kDebuffPin) return CcKind::Pin;
  if (d & kDebuffSuppression) return CcKind::Suppression;
  if (d & kDebuffStun) return CcKind::Stun;
  if (d & kDebuffFear) return CcKind::Fear;
  if (d & kDebuffRoot) return CcKind::Root;
  if (d & kDebuffKnockback) return CcKind::Knockback;
  if (d & kDebuffSilence) return CcKind::Silence;
  if (d & kDebuffSlow) return CcKind::Slow;
  return CcKind::None;
}

const char* CcKindName(CcKind k) {
  switch (k) {
    case CcKind::Pin: return "Pin";
    case CcKind::Suppression: return "Suppression";
    case CcKind::Stun: return "Stun";
    case CcKind::Fear: return "Fear";
    case CcKind::Root: return "Root";
    case CcKind::Knockback: return "Knockback";
    case CcKind::Silence: return "Silence";
    case CcKind::Slow: return "Slow";
    default: return "None";
  }
}

unsigned DebuffFromFeedback(unsigned f) {
  unsigned d = 0;
  if (f & 0x1) d |= kDebuffStun;
  if (f & 0x2) d |= kDebuffFear;
  if (f & 0x4) d |= kDebuffSilence;
  if (f & 0x8) d |= kDebuffSuppression;
  if (f & 0x10) d |= kDebuffRoot;
  if (f & 0x20) d |= kDebuffSlow;
  return d;
}

unsigned FeedbackFromDebuff(unsigned d) {
  unsigned f = 0;
  if (d & kDebuffStun) f |= 0x1;
  if (d & kDebuffFear) f |= 0x2;
  if (d & kDebuffSilence) f |= 0x4;
  if (d & kDebuffSuppression) f |= 0x8;
  if (d & kDebuffRoot) f |= 0x10;
  if (d & kDebuffSlow) f |= 0x20;
  return f;
}

std::uint64_t UnionMs(std::vector<std::pair<std::uint64_t, std::uint64_t>> spans) {
  std::sort(spans.begin(), spans.end());
  std::uint64_t total = 0, curFrom = 0, curTo = 0;
  bool have = false;
  for (const auto& s : spans) {
    if (s.second <= s.first || s.second == ~0ull) continue;  // empty, or still open (never after Close)
    if (have && s.first <= curTo) {
      curTo = std::max(curTo, s.second);
      continue;
    }
    if (have) total += curTo - curFrom;
    curFrom = s.first;
    curTo = s.second;
    have = true;
  }
  if (have) total += curTo - curFrom;
  return total;
}

std::uint64_t CcTally::EndOf(std::uint64_t start, std::uint32_t durationMs) {
  return durationMs == 0 ? kOpen : start + durationMs;
}

void CcTally::EndAt(Inst& in, std::uint64_t nowMs) {
  for (Interval& s : in.spans)
    if (s.second > nowMs) s.second = std::max(s.first, nowMs);
}

CcVerdict CcTally::OnCreated(const CcStart& s) {
  if (closed_) return CcVerdict::Nobody;
  const CcKind kind = CcKindOf(s.debuff);
  if (kind == CcKind::None) return CcVerdict::NotCc;
  if (s.caster != 0 && s.caster == s.carrier) return CcVerdict::Self;
  Inst in;
  in.carrier = s.carrier;
  in.kind = kind;
  if (ValidSeat(s.casterSeat) && s.carrierTeam != s.casterTeam) in.causedSeat = s.casterSeat;
  if (ValidSeat(s.carrierSeat) && (s.caster == 0 || s.casterTeam == 0 || s.casterTeam != s.carrierTeam))
    in.enduredSeat = s.carrierSeat;
  if (in.causedSeat < 0 && in.enduredSeat < 0) return CcVerdict::Nobody;
  // an instance id seen again (the registry replaces it too): the old one ends at the new start
  const auto old = live_.find(s.instance);
  if (old != live_.end()) {
    EndAt(old->second, s.startMs);
    done_.push_back(std::move(old->second));
    live_.erase(old);
  }
  in.spans.push_back({s.startMs, EndOf(s.startMs, s.durationMs)});
  live_.emplace(s.instance, std::move(in));
  ++counted_[static_cast<int>(kind)];
  return CcVerdict::Counted;
}

bool CcTally::OnUpdated(std::uint32_t instance, std::uint64_t startMs, std::uint32_t durationMs) {
  const auto it = live_.find(instance);
  if (it == live_.end() || it->second.spans.empty()) return false;
  Interval& last = it->second.spans.back();
  if (startMs == ~0ull) {
    last.second = EndOf(last.first, durationMs);  // the start kept: a new duration from it
  } else {
    last.second = std::min(last.second, std::max(last.first, startMs));
    it->second.spans.push_back({startMs, EndOf(startMs, durationMs)});
  }
  return true;
}

bool CcTally::OnEnded(std::uint32_t instance, std::uint64_t nowMs) {
  const auto it = live_.find(instance);
  if (it == live_.end()) return false;
  EndAt(it->second, nowMs);
  done_.push_back(std::move(it->second));
  live_.erase(it);
  return true;
}

int CcTally::OnCarrierGone(std::uint32_t carrier, std::uint64_t nowMs) {
  int n = 0;
  for (auto it = live_.begin(); it != live_.end();) {
    if (it->second.carrier != carrier) {
      ++it;
      continue;
    }
    EndAt(it->second, nowMs);
    done_.push_back(std::move(it->second));
    it = live_.erase(it);
    ++n;
  }
  return n;
}

void CcTally::Close(std::uint64_t endMs) {
  if (closed_) return;
  closed_ = true;
  for (auto& kv : live_) {
    EndAt(kv.second, endMs);
    done_.push_back(std::move(kv.second));
  }
  live_.clear();
}

std::vector<std::pair<std::string, double>> CcTally::SeatStats(int seat) const {
  std::vector<std::pair<std::string, double>> out;
  if (!ValidSeat(seat)) return out;
  constexpr int kKinds = static_cast<int>(CcKind::Count);
  std::uint64_t caused[kKinds] = {};
  std::vector<Interval> endured[kKinds];
  std::vector<Interval> enduredAll;
  for (const Inst& in : done_) {
    const int k = static_cast<int>(in.kind);
    if (in.causedSeat == seat) caused[k] += UnionMs(in.spans);
    if (in.enduredSeat == seat) {
      endured[k].insert(endured[k].end(), in.spans.begin(), in.spans.end());
      enduredAll.insert(enduredAll.end(), in.spans.begin(), in.spans.end());
    }
  }
  std::uint64_t causedAll = 0;
  for (int k = 0; k < kKinds; ++k) causedAll += caused[k];
  const std::uint64_t enduredSum = UnionMs(enduredAll);
  if (causedAll) out.emplace_back("ccCausedMs", static_cast<double>(causedAll));
  if (enduredSum) out.emplace_back("ccEnduredMs", static_cast<double>(enduredSum));
  for (int k = 0; k < kKinds; ++k) {
    const std::string name = CcKindName(static_cast<CcKind>(k));
    if (caused[k]) out.emplace_back("ccCaused" + name + "Ms", static_cast<double>(caused[k]));
    const std::uint64_t e = UnionMs(endured[k]);
    if (e) out.emplace_back("ccEndured" + name + "Ms", static_cast<double>(e));
  }
  return out;
}

bool IsWardAbility(std::uint32_t guid) { return guid == kGuidItemWard || guid == kGuidOutlawPassiveWard; }

bool WardTally::OnTickThread(std::uint32_t abilityGuid, bool recreated, int seat) {
  if (recreated || !IsWardAbility(abilityGuid) || !ValidSeat(seat)) return false;
  ++placed_[seat];
  return true;
}

}  // namespace gms::stats
