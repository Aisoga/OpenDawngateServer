// The attached effects a modifier's script leaves behind when the modifier ends (FOGSTACKS).
//
// nEffect.CreateEffect(true, asset, object, ...) stores an effect in one of the object's 16 attached-effect slots on
// the server (GameObject_CreateAttachedEffect) and tells the players that see the object (a ServerEvent); only
// nEffect.DestroyEffect frees the slot again. A script that never destroys its effect leaves the slot in use for the
// rest of the match: Duress's counter (modifier_Item_Legendary_OnBasic_Root_Counter: its stack markers, no Deactivate)
// is one. A player who watched the effect start keeps what the client made of it, but a player who gets the object's
// whole state later (a fog exit, a late create) gets every used slot again, so the marker of a debuff that ended long
// ago came back with the object out of the fog.
//
// The ledger remembers which ability instance made each effect (the Lua that runs at the create) and forgets an effect
// when it is destroyed. When a modifier instance (type 1) is gone, its remaining effects are the leftovers the host
// destroys (the engine part checks that the slot still holds the same handle). An ability's (type 0) effects are
// only forgotten: an ability may hand its effect to something that outlives it.
//
// Engine-free (gms_tests FogStacks*); the engine part is gms/engine/effects.cpp.
#pragma once

#include <cstddef>
#include <cstdint>
#include <functional>
#include <map>
#include <vector>

namespace gms::replication {

struct EffectEntry {
  std::uint32_t object = 0;  // the object id the effect is attached to
  std::uint32_t handle = 0;  // the attach handle (counter << 8 | slot index)
  const char* asset = nullptr;  // for log lines only (the asset's name, owned by the game)
};

struct EffectOwner {
  std::uint32_t instance = 0;
  int type = -1;              // 0 ability, 1 modifier
  std::uintptr_t ident = 0;   // what names the instance besides its id (its static ability); a reused id differs
  const char* name = nullptr; // the script's name, for log lines
  std::vector<EffectEntry> effects;
};

// The slot index an attach handle names (FUN_00924050: counter << 8 | index; the reader FUN_00924040 takes the low byte).
inline int EffectSlotOf(std::uint32_t handle) { return static_cast<int>(handle & 0xFF); }

class EffectLedger {
 public:
  static constexpr std::size_t kMaxPerInstance = 32;  // a long-lived instance that never destroys: the oldest go
  static constexpr std::size_t kMaxInstances = 4096;

  // An effect made while `owner` (instance, type, ident, name) runs. Returns false when nothing was recorded.
  bool OnCreated(std::uint32_t instance, int type, std::uintptr_t ident, const char* name, const EffectEntry& e) {
    if (!instance || !e.object || !e.handle) return false;
    auto it = owners_.find(instance);
    if (it != owners_.end() && it->second.ident != ident) {  // the id was reused: the old owner ended unseen
      ended_.push_back(std::move(it->second));
      owners_.erase(it);
      it = owners_.end();
    }
    if (it == owners_.end()) {
      if (owners_.size() >= kMaxInstances) return false;
      EffectOwner o;
      o.instance = instance;
      o.type = type;
      o.ident = ident;
      o.name = name;
      it = owners_.emplace(instance, std::move(o)).first;
    }
    auto& v = it->second.effects;
    if (v.size() >= kMaxPerInstance) v.erase(v.begin());
    v.push_back(e);
    return true;
  }

  // The script (or the host) destroyed the effect: forgotten wherever it is. Returns true when it was recorded.
  bool OnDestroyed(std::uint32_t object, std::uint32_t handle) {
    for (auto it = owners_.begin(); it != owners_.end(); ++it) {
      auto& v = it->second.effects;
      for (std::size_t i = 0; i < v.size(); ++i) {
        if (v[i].object != object || v[i].handle != handle) continue;
        v.erase(v.begin() + static_cast<std::ptrdiff_t>(i));
        if (v.empty()) owners_.erase(it);
        return true;
      }
    }
    return false;
  }

  // Every owner that `alive(instance, ident)` calls ended is taken out. The ended modifiers (type 1) with effects
  // are returned (their effects are leftovers to check); ended abilities are dropped.
  std::vector<EffectOwner> TakeEnded(const std::function<bool(std::uint32_t, std::uintptr_t)>& alive) {
    std::vector<EffectOwner> out;
    for (auto& o : ended_)
      if (o.type == 1 && !o.effects.empty()) out.push_back(std::move(o));
    ended_.clear();
    for (auto it = owners_.begin(); it != owners_.end();) {
      if (alive(it->first, it->second.ident)) {
        ++it;
        continue;
      }
      if (it->second.type == 1 && !it->second.effects.empty()) out.push_back(std::move(it->second));
      it = owners_.erase(it);
    }
    return out;
  }

  std::size_t Owners() const { return owners_.size(); }
  std::size_t Effects() const {
    std::size_t n = 0;
    for (const auto& kv : owners_) n += kv.second.effects.size();
    return n;
  }
  void Clear() {
    owners_.clear();
    ended_.clear();
  }

 private:
  std::map<std::uint32_t, EffectOwner> owners_;
  std::vector<EffectOwner> ended_;  // owners whose id was reused before a sweep saw them end
};

// A leftover is destroyed only while the object's slot still holds it: the slot's handle is the recorded one and the
// slot is in use (its asset set). Anything else (destroyed some other way, the slot reused) is left alone.
inline bool EffectStillAttached(std::uint32_t slotHandle, bool slotUsed, std::uint32_t recordedHandle) {
  return slotUsed && slotHandle == recordedHandle && EffectSlotOf(recordedHandle) < 16;
}

}  // namespace gms::replication
