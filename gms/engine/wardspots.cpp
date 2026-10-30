// MAPED4b: the level's own ward spots (wardspots.h; the rules in gms/core/bots/wardspots.h).
#include "wardspots.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <string>

#include "core/bots/wardspots.h"
#include "game.h"
#include "host/host.h"
#include "log.h"

using namespace game;

namespace wardspots {
namespace {

gms::bots::WardSpots g_spots;

struct RawSpot {
  char name[40];
  float x, y;
};
constexpr int kMaxRaw = 2 * gms::bots::kMaxWardSpots + 8;

void CopyName(char* out, std::size_t size, const char* s) {
  std::size_t k = 0;
  if (s)
    for (; s[k] && k + 1 < size; ++k) out[k] = s[k];
  out[k] = 0;
}

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in wardspots (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// Every marker whose name starts "BotWard_" (cLabsMarker +0x00 markerName, a char*), in every markerset.
bool SehReadWardMarkers(void* level, RawSpot* out, int max, int* n) {
  __try {
    *n = 0;
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    if (!ld) return false;
    void** setArr = Field<void**>(ld, level::kMarkersets);
    const int ns = Field<int>(ld, level::kMarkersets + 4);
    for (int s = 0; setArr && s < ns && s < 256; ++s) {
      void* ms = setArr[s];
      char* md = ms ? Field<char*>(ms, noun::kData) : nullptr;
      if (!md) continue;
      char* markers = Field<char*>(md, 0);
      const int nm = Field<int>(md, 4);
      for (int i = 0; markers && i < nm && i < 65536; ++i) {
        char* m = markers + i * marker::kSize;
        const char* name = Field<const char*>(m, marker::kName);
        if (!name || std::strncmp(name, "BotWard_", 8) != 0 || *n >= max) continue;
        RawSpot& r = out[(*n)++];
        CopyName(r.name, sizeof r.name, name);
        const float* p = reinterpret_cast<const float*>(m + marker::kPos);
        r.x = p[0];
        r.y = p[1];
      }
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ward markers")) {
    return false;
  }
}

}  // namespace

void ReadLevel(void* level) {
  g_spots.Clear();
  RawSpot raw[kMaxRaw];
  int n = 0;
  const bool ok = SehReadWardMarkers(level, raw, kMaxRaw, &n);
  int taken = 0;
  for (int i = 0; i < n; ++i)
    if (g_spots.Add(raw[i].name, raw[i].x, raw[i].y)) ++taken;
  g_spots.Finish();
  if (!ok) {
    Log("bots: ward spots: the level's markers could not be read; the data's tables stay (MAPED4b)");
    return;
  }
  if (taken == 0) {
    Log("bots: ward spots: the level has none of its own; the data's tables stay (MAPED4b)");
    return;
  }
  for (int side = 1; side <= 2; ++side) {
    std::string list;
    float xy[2];
    for (int k = 1; g_spots.Answer(1, side, k, xy) == gms::bots::WardAnswer::kSpot; ++k) {
      char b[40];
      std::snprintf(b, sizeof b, "%s(%.1f, %.1f)", list.empty() ? "" : " ", xy[0], xy[1]);
      list += b;
    }
    Log("bots: ward spots of the level (MAPED4b): %s %d: %s", side == 1 ? "Mortal" : "Spirit", g_spots.Count(side),
        list.c_str());
  }
  if (taken != n) Log("bots: ward spots: %d of %d BotWard_ markers not taken (name or a 0 coordinate)", n - taken, n);
}

int Answer(int side, int k, float* xy) {
  static const int option = HostOptionInt("bot_ward_spots", 1);
  return static_cast<int>(g_spots.Answer(option, side, k, xy));
}

}  // namespace wardspots
