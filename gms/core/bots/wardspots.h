// gms/core/bots/wardspots.h (MAPED4b): a level's own ward spots for the bots' ward condition.
//
// The data's condition_Bot_Ward holds two fixed tables of SM_Map_v5 spots (MortalWards and SpiritWards, 11 each) and
// takes the table of the side the bot stands on (nLevel.ObjectPositionMatchesLevelTag MortalSide), then the first spot
// within 60 units that has no allied ward within 29, else the nearest within 100. A compiled map names its own spots:
// the map compiler (mapc, map.json "wards") writes each as a marker named BotWard_Mortal_<nn> or BotWard_Spirit_<nn>
// (the Spirit ones the Mortal ones mirrored), and the host reads them from the level's markersets at its start
// (gms/engine/wardspots.cpp). The Lua (dgsim_bots_api.lua B.ApplyWardSpots) asks nBotHost.WardSpot(side, k) for each
// table and puts the level's spots into the condition's tables; the condition's own rule stays the data's.
//
// Engine-free and header-only: the name rule and the answer.
#pragma once

#include <algorithm>
#include <array>
#include <cstddef>
#include <cstring>
#include <utility>
#include <vector>

namespace gms::bots {

constexpr int kMaxWardSpots = 32;  // a side (mapc allows 16 authored; the data's table holds 11)

// "BotWard_Mortal_03" -> side 1, index 3; "BotWard_Spirit_00" -> side 2, index 0. False for any other name.
inline bool ParseWardMarkerName(const char* name, int* side, int* index) {
  if (!name) return false;
  static const char kPrefix[] = "BotWard_";
  const std::size_t np = sizeof kPrefix - 1;
  if (std::strncmp(name, kPrefix, np) != 0) return false;
  const char* p = name + np;
  int s = 0;
  if (std::strncmp(p, "Mortal_", 7) == 0)
    s = 1;
  else if (std::strncmp(p, "Spirit_", 7) == 0)
    s = 2;
  else
    return false;
  p += 7;
  if (!*p) return false;
  int k = 0;
  for (; *p; ++p) {
    if (*p < '0' || *p > '9' || k > 999) return false;
    k = k * 10 + (*p - '0');
  }
  *side = s;
  *index = k;
  return true;
}

// The answer to nBotHost.WardSpot(side, k).
enum class WardAnswer {
  kKeep = 0,      // keep the data's tables (no spots of the level's, or [host] bot_ward_spots 0)
  kNotReady = 1,  // the level isn't read yet: ask again
  kSpot = 2,      // the side's spot k (1-based)
  kEnd = 3,       // the side has fewer than k spots
};

// One level's ward spots, per side (index 0 Mortal, 1 Spirit), in the markers' order (their numbers).
class WardSpots {
 public:
  void Clear() {
    read_ = false;
    for (auto& s : sides_) s.clear();
  }
  // A marker's name and position; ignored unless its name is a ward spot's. True when taken. A spot with a coordinate
  // of 0 is refused: the condition takes it for none (and (0, 0) is the bots' null position).
  bool Add(const char* name, float x, float y) {
    int side = 0, index = 0;
    if (!ParseWardMarkerName(name, &side, &index) || x == 0.f || y == 0.f) return false;
    auto& v = sides_[side - 1];
    if (static_cast<int>(v.size()) >= kMaxWardSpots) return false;
    v.push_back({index, {x, y}});
    return true;
  }
  // The level's markers are all read: sort each side by the markers' numbers.
  void Finish() {
    for (auto& v : sides_)
      std::stable_sort(v.begin(), v.end(), [](const Entry& a, const Entry& b) { return a.first < b.first; });
    read_ = true;
  }
  bool Read() const { return read_; }
  int Count(int side) const { return side == 1 || side == 2 ? static_cast<int>(sides_[side - 1].size()) : 0; }

  // nBotHost.WardSpot(side, k): option is [host] bot_ward_spots (1 default: the level's spots when it has any; 0 the
  // data's). A level with no ward marker on either side keeps the data's tables (SM_Map_v5, a map without "wards").
  WardAnswer Answer(int option, int side, int k, float* xy) const {
    if (option <= 0) return WardAnswer::kKeep;
    if (!read_) return WardAnswer::kNotReady;
    if (sides_[0].empty() && sides_[1].empty()) return WardAnswer::kKeep;
    if (side != 1 && side != 2) return WardAnswer::kKeep;
    const auto& v = sides_[side - 1];
    if (k < 1 || k > static_cast<int>(v.size())) return WardAnswer::kEnd;
    xy[0] = v[k - 1].second[0];
    xy[1] = v[k - 1].second[1];
    return WardAnswer::kSpot;
  }

 private:
  using Entry = std::pair<int, std::array<float, 2>>;
  bool read_ = false;
  std::vector<Entry> sides_[2];
};

}  // namespace gms::bots
