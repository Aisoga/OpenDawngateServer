// gms/core/damage (D1h): the death recap a dead player gets (DeathRecap, GMS id 72, wire 0xC9; docs/GMS_PROTOCOL.md
// *The death recap (D1h)*). Header-only, no game addresses. The harness logs every enemy hit on a player's creature
// (the same hits the client's DamageTracker_AddDamageEvent 0x008E18D0 records) and builds the message at the death.
//
// The body (after the wire byte) as the client reads it (GameClient_OnGmsDeathRecap 0x006B6200 keeps it whole in a
// cDeathRecapData {buffer, header, physical list, magical list, true list, timeline}; DeathRecap_UpdateData 0x0042BE30,
// DeathRecap_BuildTimelineRow 0x0042AE70, DeathRecap_BuildTypeTooltip 0x0042C830, the source tooltip 0x0042D5A7,
// DeathRecap_GetSourceIcon 0x00429B30, DeathRecap_LoadSourceLabel 0x00429CA0 read it; all little-endian, packed):
//   header, 25 bytes: +0x00 f32 total damage (shown rounded, red); +0x04 f32 seconds ("%.1f"); +0x08 u8 timeline rows
//     (distinct sources; the screen draws at most 10 plus one row for the rest); +0x09/+0x0D/+0x11 f32 physical,
//     magical and true damage (the three bars: each / total); +0x15/+0x16/+0x17 u8 entries in the physical, magical
//     and true lists; +0x18 u8 timeline entries;
//   three lists of 13-byte entries (the type tooltips, at most 3 shown): u32 source noun asset id, i32 source NPC type
//     (nNPCType, -1 for a player's creature), u8 source team, f32 damage of that type;
//   the timeline, 18-byte entries: the 13 bytes above (all damage), then u32 ability GUID (the source tooltip names
//     the ability when its static ability has +0xA5 set, else the source), u8 hits ("x%d" when > 1). The screen merges
//     consecutive entries of one source into a row: same noun, or the same NPC type other than -1.
// [assumed] which hits are included, the type of a hit without IsPhysical/IsMagical (true), the orders.
#pragma once

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <vector>

#include "core/damage/rules.h"

namespace gms::damage {

constexpr std::uint8_t kDeathRecapWireId = 0xC9;  // kGmsDeathRecap (core/protocol/wire.h)
constexpr unsigned kRecapHeaderSize = 25;
constexpr unsigned kRecapTypeEntrySize = 13;
constexpr unsigned kRecapTimelineEntrySize = 18;

// One enemy hit on a player's creature (what DamageTracker_AddDamageEvent stores: time, damage, the credited ability,
// descriptors, the attacker's noun asset id, NPC type (a pet's owner's noun), team).
struct RecapHit {
  std::uint64_t ms = 0;
  float damage = 0.f;
  unsigned descLo = 0;
  std::uint32_t ability = 0;
  std::uint32_t noun = 0;
  std::int32_t npcType = -1;
  std::uint8_t team = 0;
};

// UITuning.deathRecapShortCombatTime (5 s) / deathRecapLongCombatTime (30 s). The client prunes its recorded events
// older than the long time (0x008E16B0: UITuning +8 x 1000, every 5 s).
struct RecapTuning {
  float shortSeconds = 5.f;
  float longSeconds = 30.f;
};

// Per victim: the hits of the last `longSeconds` (pruned on every add).
class RecapLog {
 public:
  std::vector<RecapHit> hits;
  void Add(const RecapHit& h, float longSeconds) {
    hits.push_back(h);
    const std::uint64_t keep = static_cast<std::uint64_t>(std::max(longSeconds, 0.f) * 1000.f);
    while (!hits.empty() && h.ms > hits.front().ms + keep) hits.erase(hits.begin());
  }
  void Clear() { hits.clear(); }
};

struct RecapSummary {
  float total = 0.f, physical = 0.f, magical = 0.f, trueDamage = 0.f, seconds = 0.f;
  unsigned hits = 0, rows = 0, entries = 0, physicalEntries = 0, magicalEntries = 0, trueEntries = 0;
};

// The damage type of a hit: 0 physical (IsPhysicalDamage 0x40), 1 magical (IsMagicalDamage 0x80), 2 true (IsTrueDamage
// 0x800, or neither) [assumed for neither].
inline int RecapDamageType(unsigned descLo) {
  if (descLo & kIsTrueDamage) return 2;
  if (descLo & kIsPhysical) return 0;
  if (descLo & kIsMagical) return 1;
  return 2;
}

// The hits the recap covers [assumed]: walking back from the death, those within longSeconds of it, up to the first
// gap of shortSeconds or more between two hits (or between the last hit and the death): the fight that killed.
inline std::vector<RecapHit> RecapWindow(const std::vector<RecapHit>& hits, std::uint64_t deathMs, const RecapTuning& t) {
  std::vector<RecapHit> sorted(hits);
  std::stable_sort(sorted.begin(), sorted.end(), [](const RecapHit& a, const RecapHit& b) { return a.ms < b.ms; });
  const std::uint64_t longMs = static_cast<std::uint64_t>(std::max(t.longSeconds, 0.f) * 1000.f);
  const std::uint64_t shortMs = static_cast<std::uint64_t>(std::max(t.shortSeconds, 0.f) * 1000.f);
  std::vector<RecapHit> out;
  std::uint64_t later = deathMs;
  for (auto it = sorted.rbegin(); it != sorted.rend(); ++it) {
    if (it->ms > deathMs) continue;
    if (deathMs - it->ms > longMs) break;
    if (shortMs && later - it->ms >= shortMs) break;
    out.push_back(*it);
    later = it->ms;
  }
  std::reverse(out.begin(), out.end());
  return out;
}

namespace recap_detail {

inline void PutU32(std::vector<std::uint8_t>& b, std::uint32_t v) {
  for (int i = 0; i < 4; ++i) b.push_back(static_cast<std::uint8_t>(v >> (8 * i)));
}
inline void PutF32(std::vector<std::uint8_t>& b, float f) {
  std::uint32_t v;
  std::memcpy(&v, &f, 4);
  PutU32(b, v);
}

// A source as the screen groups it: a player's creature (NPC type -1) by its noun, NPCs (pets included) by NPC type;
// and team.
struct Source {
  std::int32_t npcType = -1;
  std::uint32_t noun = 0;  // the most recent hit's
  std::uint8_t team = 0;
  std::uint64_t lastMs = 0;
  std::size_t lastIndex = 0;  // the position of its latest hit (ties of lastMs: the later hit is the more recent)
  float byType[3] = {0.f, 0.f, 0.f};
  float total = 0.f;
  struct Ability {
    std::uint32_t guid = 0;
    float damage = 0.f;
    unsigned hits = 0;
    std::uint64_t lastMs = 0;
  };
  std::vector<Ability> abilities;
};

inline bool SameSource(const Source& s, const RecapHit& h) {
  if (s.npcType != h.npcType || s.team != h.team) return false;
  if (h.npcType == -1) return s.noun == h.noun;
  return true;
}

inline void PutEntry(std::vector<std::uint8_t>& b, const Source& s, float damage) {
  PutU32(b, s.noun);
  PutU32(b, static_cast<std::uint32_t>(s.npcType));
  b.push_back(s.team);
  PutF32(b, damage);
}

}  // namespace recap_detail

// The whole message (wire byte 0xC9 first) for the hits `hits` (already windowed with RecapWindow, or not: they are
// taken as given) and the death at `deathMs`:
//   totals by type; the type lists: the sources with damage of that type, most damage first (ties: most recent first);
//   the timeline: the sources, most recent hit first (in one millisecond: the later hit), each with its abilities,
//   most damage first (ties: most recent first), each entry's hits counted (up to 255); at most 255 entries per list
//   (the counts are bytes) [assumed orders].
inline std::vector<std::uint8_t> BuildDeathRecap(const std::vector<RecapHit>& hits, std::uint64_t deathMs,
                                                 RecapSummary* summary = nullptr) {
  using namespace recap_detail;
  RecapSummary sum;
  std::vector<Source> sources;
  std::uint64_t first = deathMs;
  for (std::size_t i = 0; i < hits.size(); ++i) {
    const RecapHit& h = hits[i];
    if (!(h.damage > 0.f)) continue;
    ++sum.hits;
    first = std::min(first, h.ms);
    const int type = RecapDamageType(h.descLo);
    sum.total += h.damage;
    (type == 0 ? sum.physical : type == 1 ? sum.magical : sum.trueDamage) += h.damage;
    Source* s = nullptr;
    for (Source& x : sources)
      if (SameSource(x, h)) s = &x;
    if (!s) {
      sources.push_back(Source());
      s = &sources.back();
      s->npcType = h.npcType;
      s->team = h.team;
    }
    if (h.ms >= s->lastMs) {
      s->lastMs = h.ms;
      s->lastIndex = i;
      s->noun = h.noun;
    }
    s->byType[type] += h.damage;
    s->total += h.damage;
    Source::Ability* a = nullptr;
    for (Source::Ability& x : s->abilities)
      if (x.guid == h.ability) a = &x;
    if (!a) {
      s->abilities.push_back(Source::Ability());
      a = &s->abilities.back();
      a->guid = h.ability;
    }
    a->damage += h.damage;
    ++a->hits;
    a->lastMs = std::max(a->lastMs, h.ms);
  }
  sum.seconds = sum.hits ? static_cast<float>(deathMs - std::min(first, deathMs)) / 1000.f : 0.f;

  std::vector<std::uint8_t> lists[3];
  unsigned counts[3] = {0, 0, 0};
  for (int type = 0; type < 3; ++type) {
    std::vector<const Source*> order;
    for (const Source& s : sources)
      if (s.byType[type] > 0.f) order.push_back(&s);
    std::stable_sort(order.begin(), order.end(), [type](const Source* a, const Source* b) {
      if (a->byType[type] != b->byType[type]) return a->byType[type] > b->byType[type];
      if (a->lastMs != b->lastMs) return a->lastMs > b->lastMs;
      return a->lastIndex > b->lastIndex;
    });
    for (const Source* s : order) {
      if (counts[type] == 255) break;
      PutEntry(lists[type], *s, s->byType[type]);
      ++counts[type];
    }
  }

  std::vector<const Source*> timeline;
  for (const Source& s : sources) timeline.push_back(&s);
  std::stable_sort(timeline.begin(), timeline.end(), [](const Source* a, const Source* b) {
    if (a->lastMs != b->lastMs) return a->lastMs > b->lastMs;
    return a->lastIndex > b->lastIndex;
  });
  std::vector<std::uint8_t> tl;
  unsigned entries = 0, rows = 0;
  for (const Source* s : timeline) {
    if (entries == 255) break;
    std::vector<Source::Ability> ab(s->abilities);
    std::stable_sort(ab.begin(), ab.end(), [](const Source::Ability& a, const Source::Ability& b) {
      if (a.damage != b.damage) return a.damage > b.damage;
      return a.lastMs > b.lastMs;
    });
    ++rows;
    for (const Source::Ability& a : ab) {
      if (entries == 255) break;
      PutEntry(tl, *s, a.damage);
      PutU32(tl, a.guid);
      tl.push_back(static_cast<std::uint8_t>(std::min(a.hits, 255u)));
      ++entries;
    }
  }
  rows = std::min(rows, 255u);

  std::vector<std::uint8_t> m;
  m.push_back(kDeathRecapWireId);
  PutF32(m, sum.total);
  PutF32(m, sum.seconds);
  m.push_back(static_cast<std::uint8_t>(rows));
  PutF32(m, sum.physical);
  PutF32(m, sum.magical);
  PutF32(m, sum.trueDamage);
  for (int type = 0; type < 3; ++type) m.push_back(static_cast<std::uint8_t>(counts[type]));
  m.push_back(static_cast<std::uint8_t>(entries));
  for (int type = 0; type < 3; ++type) m.insert(m.end(), lists[type].begin(), lists[type].end());
  m.insert(m.end(), tl.begin(), tl.end());
  sum.rows = rows;
  sum.entries = entries;
  sum.physicalEntries = counts[0];
  sum.magicalEntries = counts[1];
  sum.trueEntries = counts[2];
  if (summary) *summary = sum;
  return m;
}

}  // namespace gms::damage
