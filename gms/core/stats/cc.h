// M18 (MSTATS4): crowd control caused and endured, engine-free. The rules are ours [assumed]: the original server's
// statistics code is gone, so the kind priority and counting slows as crowd control follow LoL-like conventions.
//
// The engine (gms/engine/matchstats_cc.cpp) feeds it the host's own modifier messages as DrainStep drains them:
//   OnCreated   a ModifierCreated record (carrier, caster, the duration after tenacity, the start on the server clock)
//               with what the engine resolved: the static ability's debuff descriptors, the caster's and the carrier's
//               player index and team
//   OnUpdated   a ModifierUpdated (a refresh: a new start and duration, or the start kept and a new duration)
//   OnEnded     the instance is gone on the server (the registry's end) or its carrier was deleted
//   Close       the match's end: every open interval ends there
// Then SeatStats gives each seat's keys (ccCausedMs, ccEnduredMs, ccCaused<Kind>Ms, ccEndured<Kind>Ms; only those not 0).
//
// The rules:
//   - one kind per modifier, by priority Pin > Suppression > Stun > Fear > Root > Knockback > Silence > Slow (a pin also
//     carries the knockback bit, IgnoreCrowdControlDurationDecrease too); a modifier with none of them isn't CC;
//   - an instance's time is [start, min(end, start + duration)] (duration 0: untimed, until it ends), re-opened by
//     each update (the old interval ends at the new start);
//   - caused: per instance, to the caster's seat, when the carrier is not of the caster's team (any enemy object:
//     shapers, minions, creeps) and isn't the caster itself;
//   - endured: on the carrier's seat (a player-controlled carrier), when the caster isn't the carrier and not of the
//     carrier's team (an unknown caster counts); per kind the union of that kind's intervals, ccEnduredMs the union of
//     all of them (two overlapping stuns count once);
//   - a refresh keeps the first caster (ModifierUpdated carries none).
#pragma once

#include <cstddef>
#include <cstdint>
#include <map>
#include <string>
#include <utility>
#include <vector>

namespace gms::stats {

// nDebuffDescriptors (GlobalDefinitions.lua), the static ability's +0x400.
constexpr unsigned kDebuffRoot = 0x1;
constexpr unsigned kDebuffSlow = 0x2;
constexpr unsigned kDebuffStun = 0x4;
constexpr unsigned kDebuffFear = 0x8;
constexpr unsigned kDebuffSilence = 0x10;
constexpr unsigned kDebuffKnockback = 0x20;
constexpr unsigned kDebuffImmuneToCleanse = 0x40;
constexpr unsigned kDebuffSuppression = 0x80;
constexpr unsigned kDebuffPin = 0x200;  // IsPin = 0x320 (with 0x100 and the knockback bit)

enum class CcKind : int { Pin, Suppression, Stun, Fear, Root, Knockback, Silence, Slow, Count, None = -1 };
CcKind CcKindOf(unsigned debuffDescriptors);
const char* CcKindName(CcKind k);  // "Pin", "Stun", ... (the keys' infix)

// The record's last field as GameSimulator_SendModifierCreated (0x008F57A0) writes it, back to the descriptors: Stun 1,
// Fear 2, Silence 4, Suppression 8, Root 0x10, Slow 0x20. Knockback and Pin aren't on the wire (the engine reads the
// static ability's own +0x400 first; this is the fallback).
unsigned DebuffFromFeedback(unsigned feedback);
// The other way, as 0x008F57A0 does it (the engine's check that a record's feedback matches the static's descriptors).
unsigned FeedbackFromDebuff(unsigned debuff);

// One ModifierCreated with what the engine resolved.
struct CcStart {
  std::uint32_t instance = 0, carrier = 0, caster = 0;  // object ids (caster 0: none)
  unsigned debuff = 0;                                  // the static ability's debuff descriptors
  std::uint64_t startMs = 0;                            // server clock
  std::uint32_t durationMs = 0;                         // after tenacity; 0 = untimed
  int casterSeat = -1;   // the caster object's player index (+0x4C), -1 none
  int casterTeam = 0;    // the caster object's team (0 unknown)
  int carrierSeat = -1;  // the carrier's player index when it is player-controlled, else -1
  int carrierTeam = 0;
};

// Why a ModifierCreated was or wasn't counted.
enum class CcVerdict { Counted, NotCc, Self, Nobody };

class CcTally {
 public:
  CcVerdict OnCreated(const CcStart& s);
  // A refresh; startMs ~0 keeps the start. False if the instance isn't tracked (or already ended).
  bool OnUpdated(std::uint32_t instance, std::uint64_t startMs, std::uint32_t durationMs);
  // The instance ended at nowMs (server clock). False if it wasn't tracked.
  bool OnEnded(std::uint32_t instance, std::uint64_t nowMs);
  // The carrier object was deleted: every instance on it ends. Returns how many.
  int OnCarrierGone(std::uint32_t carrier, std::uint64_t nowMs);
  // The match's end (once; later calls do nothing).
  void Close(std::uint64_t endMs);

  // A seat's keys and their ms (only those not 0), valid after Close.
  std::vector<std::pair<std::string, double>> SeatStats(int seat) const;

  // For the logs and tests.
  int Tracked() const { return static_cast<int>(live_.size() + done_.size()); }
  int Open() const { return static_cast<int>(live_.size()); }
  int CountOf(CcKind k) const { return k == CcKind::None ? 0 : counted_[static_cast<int>(k)]; }

 private:
  using Interval = std::pair<std::uint64_t, std::uint64_t>;  // [from, to)
  static constexpr std::uint64_t kOpen = ~0ull;
  struct Inst {
    std::uint32_t carrier = 0;
    CcKind kind = CcKind::None;
    int causedSeat = -1, enduredSeat = -1;
    std::vector<Interval> spans;  // the last may be open (to = kOpen)
  };
  static void EndAt(Inst& in, std::uint64_t nowMs);
  static std::uint64_t EndOf(std::uint64_t start, std::uint32_t durationMs);

  std::map<std::uint32_t, Inst> live_;
  std::vector<Inst> done_;
  int counted_[static_cast<int>(CcKind::Count)] = {};
  bool closed_ = false;
};

// The total length of a set of intervals counted once (sorted and merged; the input is copied).
std::uint64_t UnionMs(std::vector<std::pair<std::uint64_t, std::uint64_t>> spans);

// Wards placed: the two ward abilities create their ward first thing in their Tick (their scripts), so a new tick
// thread of one of them is one ward. GUIDs = SPID (FNV-1 of the lower-case name).
constexpr std::uint32_t kGuidItemWard = 0x8A2ED1E2;          // ability_Item_Ward (every shaper's slot 1)
constexpr std::uint32_t kGuidOutlawPassiveWard = 0xAC1F1FA3;  // ability_Outlaw_Passive_Ward (Ronan)
bool IsWardAbility(std::uint32_t guid);

class WardTally {
 public:
  // A tick thread was prepared for an ability instance: true (the seat's wardsPlaced + 1) for a ward ability's new
  // thread (CreateTickThread runs once per new instance); a recreated one (a reactivation) counts nothing.
  bool OnTickThread(std::uint32_t abilityGuid, bool recreated, int seat);
  int Placed(int seat) const { return seat >= 0 && seat < 10 ? placed_[seat] : 0; }

 private:
  int placed_[10] = {};
};

}  // namespace gms::stats
