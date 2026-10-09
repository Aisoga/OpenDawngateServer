// Shaper select from createGame (roadmap D3-live step 4; docs/HOST_CONTROL.md, docs/GMS_PROTOCOL.md *Shaper select on
// the match host (D3-live)*): dgserver --shaper-select puts each human's content into its roster entry (the shaper
// list, skins, saved skins, wards, announcer packs, as asset names) and shaper select's timers into createGame's
// timers map. These turn them into gms/core's ShaperSelectConfig parts. Engine-free and header-only: the host DLL
// (ServerMatch) and gms_tests include it (both link gms_core and dg_control).
#pragma once

#include <cstdint>
#include <string>

#include "control/messages.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"
#include "core/session/shaper_select.h"

namespace gms {

// Whether the roster entry carries shaper select content (a list; the other fields only make sense with one).
inline bool HasShaperContent(const dggms::control::RosterEntry& e) { return !e.shapers.empty(); }

// D3-perks: one of the roster's perk pages as shaper select keeps it: the C0 02 entries (the value from thousandths
// as the client computes it, (float)(n x 0.001); attribute and op as bytes, out-of-range ones left out) and the
// modifiers' ability ids (SPID = FNV-1 of the lower-cased Lua name, as the client's ability registry keys them).
inline PerkPage PerkPageFromRoster(const dggms::control::RosterEntry::PerkPage& p) {
  PerkPage pg;
  pg.id = static_cast<uint32_t>(p.id);
  pg.version = static_cast<uint32_t>(p.version);
  for (const dggms::control::RosterEntry::PerkAttribute& a : p.attributes) {
    if (a.attribute < 0 || a.attribute > 0xFF || a.op < 0 || a.op > 0xFF) continue;
    LoadoutAttribute la;
    la.attribute = static_cast<uint8_t>(a.attribute);
    la.op = static_cast<uint8_t>(a.op);
    la.value = static_cast<float>(static_cast<double>(a.milli) * 0.001);
    pg.attributes.push_back(la);
  }
  for (const std::string& m : p.modifiers)
    if (!m.empty()) pg.modifiers.push_back(AssetIdOf(m));
  return pg;
}

// The roster entry's content with every Noun as its asset id (FNV-1 of the lower-cased name).
inline ShaperContent ShaperContentFromRoster(const dggms::control::RosterEntry& e) {
  ShaperContent c;
  for (const dggms::control::RosterEntry::Shaper& s : e.shapers) {
    ShaperListEntry le;
    le.noun = AssetIdOf(s.noun);
    le.flags = static_cast<uint8_t>(s.flags & 0xFF);
    le.offer = static_cast<uint32_t>(s.offer);
    c.list.push_back(le);
  }
  for (const auto& kv : e.skins) {
    std::vector<uint32_t>& v = c.skins[AssetIdOf(kv.first)];
    for (const std::string& s : kv.second) v.push_back(AssetIdOf(s));
  }
  for (const auto& kv : e.savedSkins) c.savedSkins[AssetIdOf(kv.first)] = AssetIdOf(kv.second);
  for (const std::string& w : e.wards) c.wards.push_back(AssetIdOf(w));
  for (const std::string& a : e.announcers) c.announcers.push_back(AssetIdOf(a));
  for (const dggms::control::RosterEntry::PerkPage& p : e.perkPages) c.pages.push_back(PerkPageFromRoster(p));
  return c;
}

// createGame's shaper select timers: the mode's (practice: ShaperTimersPractice, else ShaperTimersNormal), each
// overridden by the timers key of ShaperSelectTuning's name (ms; a negative value is ignored). `from` (optional)
// says where they came from, for the log.
inline ShaperSelectTimers ShaperTimersFromCreateGame(const dggms::control::CreateGame& g, std::string* from = nullptr) {
  ShaperSelectTimers t = g.mode == "practice" ? ShaperTimersPractice() : ShaperTimersNormal();
  std::string f = g.mode == "practice" ? "practice" : "ShaperSelectTuning's";
  struct Key {
    const char* name;
    uint32_t ShaperSelectTimers::*field;
  };
  static const Key kKeys[] = {{"ShaperSelectionTime", &ShaperSelectTimers::pickMs},
                              {"RolePerkSelectTime", &ShaperSelectTimers::rolePerkMs},
                              {"SkinSelectTime", &ShaperSelectTimers::skinMs},
                              {"RevealTime", &ShaperSelectTimers::revealMs}};
  for (const Key& k : kKeys) {
    auto it = g.timers.find(k.name);
    if (it == g.timers.end() || it->second < 0) continue;
    t.*k.field = static_cast<uint32_t>(it->second);
    f += std::string(", createGame's ") + k.name;
  }
  if (from) *from = f;
  return t;
}

}  // namespace gms
