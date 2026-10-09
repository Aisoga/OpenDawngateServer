// Surrender on the match host, the engine side (D8b): see surrender.h.
#include "surrender.h"

#include <windows.h>

#include "game.h"
#include "log.h"

using namespace game;

namespace surr {
namespace {

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in surrender (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

}  // namespace

bool ReadTuning(void* sim, gms::surrender::Tuning* t) {
  __try {
    void* level = Field<void*>(sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* ga = ld ? Field<void*>(ld, level::kGameplayTuning) : nullptr;
    char* g = ga ? Field<char*>(ga, noun::kData) : nullptr;
    if (!g) return false;
    t->enableMatchMinutes = static_cast<double>(Field<long long>(g, gameplayTuning::kSurrenderEnableMatchMinutes));
    t->enableEconomy = Field<float>(g, gameplayTuning::kSurrenderEnableEconomy);
    t->enableKills = Field<int>(g, gameplayTuning::kSurrenderEnableKills);
    t->enableDisconnectS = static_cast<double>(Field<long long>(g, gameplayTuning::kSurrenderEnableDisconnectS));
    t->voteS = static_cast<double>(Field<long long>(g, gameplayTuning::kSurrenderVoteS));
    t->cooldownS = static_cast<double>(Field<long long>(g, gameplayTuning::kSurrenderCooldownS));
    t->threshold = Field<float>(g, gameplayTuning::kSurrenderThreshold);
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadTuning")) {
    return false;
  }
}

bool ReadTeams(void* sim, gms::surrender::MatchView* m) {
  __try {
    for (int i = 0; i < 3; ++i) {
      m->kills[i] = 0;
      m->gold[i] = 0;
    }
    for (unsigned i = 0; i < 10; ++i) {
      void* p = GetPlayerByIndex(sim, i);
      if (!p) continue;
      const int team = Field<unsigned char>(p, player::kTeam);
      if (team != 1 && team != 2) continue;
      m->kills[team] += static_cast<int>(Field<unsigned>(p, player::kKills));
      m->gold[team] += Field<float>(p, player::kTotalGold);
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "ReadTeams")) {
    return false;
  }
}

bool WriteUiAvailability(void* sim, std::uint8_t value) {
  __try {
    Field<std::uint8_t>(static_cast<char*>(sim) + matchsim::kUIData, ui::kSurrenderAvailability) = value;
    return true;
  } __except (SehNote(GetExceptionInformation(), "WriteUiAvailability")) {
    return false;
  }
}

}  // namespace surr
