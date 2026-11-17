// MAPRULES4: a map's victory rules and start level on the match host (maprules.h).
#include "maprules.h"

#include <windows.h>

#include <cstdio>
#include <string>
#include <vector>

#include "bots.h"
#include "commands.h"
#include "game.h"
#include "log.h"
#include "match.h"
#include "surrender.h"

using namespace game;

namespace maprules {
namespace {

namespace mr = gms::maprules;

constexpr int kSeats = 10;  // the gameplay indices that play (0-9); spectators get no level

// The rules from the [host] overrides, createGame and startGame (WINLOBBY: the merge is core's, gms_tests
// MapRulesSources*); g_rules is its current result, copied after each source.
mr::Sources g_src;
mr::Rules g_rules;
int g_bindings[2] = {0, 0};  // WINBIND-B: each team's destructions of enemy Bindings (team 1's, team 2's)
mr::Race g_race;
bool g_raceMade = false;
bool g_deployed[kSeats] = {};
bool g_levelled[kSeats] = {};
std::vector<float> g_xp;  // LevelUpTuning.Level[i].xpRequired
bool g_xpRead = false;
int g_lastKills[2] = {-1, -1};

struct Stats {
  unsigned alerts = 0, alertsFailed = 0, seatsLevelled = 0, grants = 0, grantsFailed = 0;
} g_st;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  Log("EXCEPTION in maprules (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

// The server level's LevelUpTuning.Level[].xpRequired (stride 0xC), as death.cpp reads it for the kill XP.
bool SehReadLevelXp(void* sim, std::vector<float>* out) {
  __try {
    void* level = Field<void*>(sim, sim::kLevel);
    char* ld = level ? Field<char*>(level, noun::kData) : nullptr;
    void* lu = ld ? Field<void*>(ld, level::kLevelUpTuning) : nullptr;
    char* lud = lu ? Field<char*>(lu, noun::kData) : nullptr;
    if (!lud) return false;
    const char* lv = Field<const char*>(lud, 0);
    const int n = Field<int>(lud, 4);
    if (!lv || n <= 1 || n > 64) return false;
    for (int i = 0; i < n; ++i)
      out->push_back(Field<float>(const_cast<char*>(lv), static_cast<std::uintptr_t>(i) * 0xC));
    return true;
  } __except (SehNote(GetExceptionInformation(), "LevelUpTuning")) {
    out->clear();
    return false;
  }
}

void LogRules(const char* source) {
  Log("maprules: %s: %s%s", source, mr::Describe(g_rules).c_str(),
      g_rules.Victory() ? " (the kill race, the Binding race and the time limit end the match; the Guardian and a "
                          "surrender still do)"
                        : "");
}

void GrantStartLevel(void* sim, int index) {
  if (!g_xpRead) {
    g_xpRead = true;
    if (SehReadLevelXp(sim, &g_xp))
      Log("maprules: LevelUpTuning of the server level: %zu levels, level %d = %.0f XP", g_xp.size(), g_rules.startLevel,
          mr::CumulativeXp(g_xp.data(), static_cast<int>(g_xp.size()), g_rules.startLevel));
    else
      Log("maprules: no LevelUpTuning on the server level: no start level");
  }
  const std::vector<float> steps = mr::LevelSteps(g_xp.data(), static_cast<int>(g_xp.size()), 1, g_rules.startLevel);
  if (steps.empty()) return;
  Log("maprules: player %d (%s): start level %d, %zu level step(s) of XP", index,
      bots::IsBotIndex(index) ? "bot" : "human", g_rules.startLevel, steps.size());
  bool ok = true;
  for (float s : steps) {
    // one level a grant: a bot's LabsPlayer_GrantXP ranks the next entry of its skill order at each level gained
    if (cmds::GrantXpNow(sim, static_cast<unsigned>(index), s, "map rule start level")) {
      ++g_st.grants;
    } else {
      ++g_st.grantsFailed;
      ok = false;
      break;
    }
  }
  if (ok) ++g_st.seatsLevelled;
}

void SendAlert(void* sim, const mr::Send& s, double t) {
  const char* def = mr::DefName(s.line);
  const unsigned mask = cmds::SendFeedback(sim, def, 0, s.teamBits, 0, 0, s.excludePlayers);
  if (mask) ++g_st.alerts;
  else ++g_st.alertsFailed;
  Log("maprules: alert %s \"%s\" at match %.2f s: team bits 0x%X, left out 0x%04X -> %s0x%04X", def, mr::Text(s.line),
      t, s.teamBits, s.excludePlayers, mask ? "players " : "FAILED (no such asset in this copy?) ", mask & 0xFFFFu);
}

}  // namespace

void Configure(int kills, int minutes, int startLevel, int bindings) {
  std::string note;
  g_src.Override(kills, minutes, startLevel, bindings, &note);
  if (!g_src.AnyOverride()) return;
  g_rules = g_src.Current();
  LogRules("the host options [host] map_kills / map_minutes / start_level / map_bindings (a test override; "
           "createGame's mapRules and startGame's victory won't replace them)");
  if (!note.empty()) Log("maprules: clamped by the host: %s", note.c_str());
}

void SetFromCreate(long long kills, long long minutes, long long startLevel, long long bindings) {
  std::string note;
  const mr::Rules r = mr::Clamp(kills, minutes, startLevel, bindings, &note);
  g_src.FromCreate(r);  // a lobby's victory that came first stays (Poll handles startGame before createGame)
  g_rules = g_src.Current();
  if (!note.empty()) Log("maprules: createGame's mapRules clamped by the host: %s", note.c_str());
  LogRules(g_src.AnyOverride() ? "createGame's mapRules (the host options win where set)"
           : g_src.StartSet()  ? "createGame's mapRules (its start level; startGame's victory stays)"
                               : "createGame's mapRules");
}

void SetFromStart(long long kills, long long minutes, long long bindings) {
  std::string note;
  const mr::Rules r = mr::Clamp(kills, minutes, 0, bindings, &note);
  g_src.FromStart(r);
  g_rules = g_src.Current();
  if (!note.empty()) Log("maprules: startGame's victory clamped by the host: %s", note.c_str());
  LogRules(g_src.VictoryOverride()
               ? "startGame's victory, the lobby's (the host options win where set; createGame's start level stays)"
               : "startGame's victory, the lobby's (createGame's start level stays)");
}

void OnBindingDestroyed(int team, std::uint32_t id, double t) {
  if (team != 1 && team != 2) return;
  ++g_bindings[team - 1];
  if (g_rules.bindings > 0)
    Log("maprules: team %d destroyed an enemy Binding (%08X) at match %.2f s: Bindings %d-%d (the race to %d)%s", team,
        id, t, g_bindings[0], g_bindings[1], g_rules.bindings, g_race.SuddenDeath() ? " (sudden death)" : "");
}

bool Bindings(int out[2]) {
  out[0] = g_bindings[0];
  out[1] = g_bindings[1];
  return g_rules.bindings > 0;
}

const mr::Rules& Current() { return g_rules; }

void OnSeatDeployed(int index) {
  if (index < 0 || index >= kSeats || g_rules.startLevel <= 1) return;
  g_deployed[index] = true;
}

void Tick(void* sim, double t) {
  if (!g_rules.Any()) return;
  for (int i = 0; i < kSeats; ++i)
    if (g_deployed[i] && !g_levelled[i]) {
      g_levelled[i] = true;
      GrantStartLevel(sim, i);
    }
  if (!g_rules.Victory()) return;
  if (!g_raceMade) {
    g_raceMade = true;
    g_race = mr::Race(g_rules);
    Log("maprules: the race starts at match %.2f s: %s", t, mr::Describe(g_rules).c_str());
  }
  if (g_race.Ended()) return;
  gms::surrender::MatchView view;
  if (!surr::ReadTeams(sim, &view)) return;
  if (view.kills[1] != g_lastKills[0] || view.kills[2] != g_lastKills[1]) {
    g_lastKills[0] = view.kills[1];
    g_lastKills[1] = view.kills[2];
    Log("maprules: kills %d-%d at match %.2f s%s", view.kills[1], view.kills[2], t,
        g_race.SuddenDeath() ? " (sudden death)" : "");
  }
  const mr::StepResult r = g_race.Step(t, view.kills[1], view.kills[2], g_bindings[0], g_bindings[1]);
  for (const mr::Send& s : r.sends) SendAlert(sim, s, t);
  if (r.end != mr::Why::kNone) {
    const char* why = mr::FinishReason(r.end);
    char bind[48] = "";
    if (g_rules.bindings > 0) std::snprintf(bind, sizeof bind, ", Bindings %d-%d", g_bindings[0], g_bindings[1]);
    Log("maprules: END by the map rule \"%s\" at match %.2f s: kills %d-%d%s, team %d wins", why, t, view.kills[1],
        view.kills[2], bind, r.winner);
    if (!match::EndByRule(r.winner, why)) Log("maprules: the match did not end (it has ended already?)");
  }
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "map rules %s: kills %d-%d, Bindings %d-%d, sudden death %s, ended %s, alerts %u (failed %u), seats "
                "levelled %u (grants %u, failed %u)",
                mr::Describe(g_rules).c_str(), g_lastKills[0] < 0 ? 0 : g_lastKills[0],
                g_lastKills[1] < 0 ? 0 : g_lastKills[1], g_bindings[0], g_bindings[1],
                g_race.SuddenDeath() ? "yes" : "no",
                g_race.Ended() ? "yes" : "no", g_st.alerts, g_st.alertsFailed, g_st.seatsLevelled, g_st.grants,
                g_st.grantsFailed);
}

}  // namespace maprules
