// HUNTERROUTE: see hunterroute.h.
#include "hunterroute.h"

#include <cmath>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

#include "core/bots/route.h"
#include "director.h"
#include "log.h"

namespace hunterroute {
namespace {

namespace gb = gms::bots;

Options g_opt;
unsigned g_lines;
const gb::RouteConfig kCfg;
const gb::PullConfig kPull;

bool LogBudget() {
  ++g_lines;
  return g_lines <= static_cast<unsigned>(g_opt.logLimit) || g_lines % 10 == 0;
}

float Dist(const float* a, const float* b) {
  const float dx = a[0] - b[0], dy = a[1] - b[1];
  return std::sqrt(dx * dx + dy * dy);
}

struct Hunter {
  std::uint32_t id = 0;
  std::string noun;
  int team = 0;
  int level = 1;
  float hp = 1.f;
  float pos[3] = {};
  bool fleeing = false;
  bool dead = false;
  gb::RouteMemory mem;
  gb::LevelTimes levels;
  double lastDecideS = -1.0;
  int step = -1;
  std::string taken;  // "step@seconds" per choice
  unsigned fleesL1 = 0, deathsL1 = 0, deaths = 0;
  unsigned vqTries = 0, vqHost = 0, vqHostObjective = 0, vqPlan = 0, vqPlanObjective = 0;
  unsigned long long vqNextMs = 0;
};
std::vector<Hunter> g_hunters;

struct TeamPull {
  bool started = false, over = false;
  const char* end = "";
  std::uint32_t laner = 0;
  std::string lanerNoun;
  double spawnAt = -1.0, startedAt = -1.0, endedAt = -1.0;
  float lanerDist = 0.f;
  unsigned requests = 0, acted = 0;
};
TeamPull g_pull[3];

Hunter* Find(std::uint32_t id) {
  for (Hunter& h : g_hunters)
    if (h.id == id) return &h;
  return nullptr;
}

Hunter* HunterOfTeam(int team) {
  for (Hunter& h : g_hunters)
    if (h.team == team) return &h;
  return nullptr;
}

const std::vector<gb::RouteStep>& Steps(int team) {
  static const std::vector<gb::RouteStep> s1 = gb::OpeningRoute(1), s2 = gb::OpeningRoute(2), none;
  return team == 1 ? s1 : team == 2 ? s2 : none;
}

// The route steps' camps now, and each step's camp view (-1 none).
void CampsFor(int team, std::vector<gb::RouteCamp>* camps, std::vector<director::CampView>* views,
              std::vector<int>* index) {
  *views = director::CampViews();
  const std::vector<gb::RouteStep>& steps = Steps(team);
  camps->assign(steps.size(), gb::RouteCamp());
  index->assign(steps.size(), -1);
  for (std::size_t i = 0; i < steps.size(); ++i) {
    const float p[2] = {steps[i].x, steps[i].y};
    float bd = kCfg.matchRadius;
    for (std::size_t k = 0; k < views->size(); ++k) {
      const float d = Dist(p, (*views)[k].pos);
      if (d <= bd) {
        bd = d;
        (*index)[i] = static_cast<int>(k);
      }
    }
    if ((*index)[i] < 0) continue;
    const director::CampView& v = (*views)[static_cast<std::size_t>((*index)[i])];
    gb::RouteCamp& c = (*camps)[i];
    c.found = true;
    c.alive = v.alive;
    c.spawns = v.spawns;
    c.dueAt = v.respawnAt;
  }
}

// The route's choice for `h` now; logs a new step, the steps that ended and the route's end.
void Decide(Hunter& h, double now) {
  if (!g_opt.route || h.team < 1 || h.team > 2) {
    h.step = -1;
    return;
  }
  const std::vector<gb::RouteStep>& steps = Steps(h.team);
  std::vector<gb::RouteCamp> camps;
  std::vector<director::CampView> views;
  std::vector<int> index;
  CampsFor(h.team, &camps, &views, &index);
  const std::uint32_t doneBefore = h.mem.done;
  const bool overBefore = h.mem.over;
  const gb::RouteChoice c = gb::NextRouteStep(steps, camps, &h.mem, h.level, h.hp, now, kCfg);
  h.lastDecideS = now;
  for (std::size_t i = 0; i < steps.size() && i < 32; ++i) {
    if ((doneBefore >> i & 1u) || !(h.mem.done >> i & 1u) || !LogBudget()) continue;
    Log("agents: bot %08X %s hunter route: step %zu %s over (%s), level %d (HUNTERROUTE) (t %.2f s)", h.id,
        h.noun.c_str(), i, steps[i].label, camps[i].found ? "its camp cleared" : "no such camp", h.level, now);
  }
  if (c.step != h.step) {
    if (c.step >= 0) {
      const director::CampView* v =
          index[static_cast<std::size_t>(c.step)] >= 0 ? &views[static_cast<std::size_t>(index[static_cast<std::size_t>(c.step)])]
                                                       : nullptr;
      if (LogBudget())
        Log("agents: bot %08X %s hunter route: step %d %s (%s at (%.1f, %.1f)): %s; level %d, hp %.0f %%, at (%.1f, "
            "%.1f) (HUNTERROUTE) (t %.2f s)",
            h.id, h.noun.c_str(), c.step, steps[static_cast<std::size_t>(c.step)].label, v ? v->name.c_str() : "?",
            v ? static_cast<double>(v->pos[0]) : 0.0, v ? static_cast<double>(v->pos[1]) : 0.0, c.why, h.level,
            static_cast<double>(h.hp * 100.f), static_cast<double>(h.pos[0]), static_cast<double>(h.pos[1]), now);
      if (h.taken.size() < 200) {
        char b[24];
        std::snprintf(b, sizeof b, "%s%d@%.0f", h.taken.empty() ? "" : " ", c.step, now);
        h.taken += b;
      }
    } else if (h.mem.over && !overBefore) {
      if (LogBudget())
        Log("agents: bot %08X %s hunter route: over (%s), level %d, steps over 0x%X (HUNTERROUTE) (t %.2f s)", h.id,
            h.noun.c_str(), h.mem.overWhy, h.level, h.mem.done, now);
    } else if (LogBudget()) {
      Log("agents: bot %08X %s hunter route: no step now (%s), level %d, hp %.0f %% (HUNTERROUTE) (t %.2f s)", h.id,
          h.noun.c_str(), c.why, h.level, static_cast<double>(h.hp * 100.f), now);
    }
    h.step = c.step;
  }
}

const char* PullEndWhy(double now, const TeamPull& p, float campFrac, float lanerHpFrac) {
  if (lanerHpFrac < kPull.lanerHpMin) return "the laner hurt";
  if (campFrac <= kPull.stopFrac) return "the camp worn down";
  if (p.started && now - p.startedAt > kPull.windowS) return "the window ended";
  return "the Hunter not there in time";
}

}  // namespace

void Configure(const Options& o) {
  g_opt = o;
  if (!g_opt.route) {  // bot_hunter_route=0: HUNTERBOT's route and the data's Vanquish alone
    g_opt.vanquish = false;
    g_opt.pull = 0;
  }
  Log("hunterroute: route %s, Vanquish rule %s, pull %d (1: the kits that want it, 2: every Hunter, 0: none); %zu "
      "steps per team (HUNTERROUTE)",
      g_opt.route ? "on" : "off", g_opt.vanquish ? "on" : "off", g_opt.pull, Steps(1).size());
}

const Options& Opts() { return g_opt; }

void HunterThink(std::uint32_t bot, const char* noun, int team, int level, float hpFrac, const float* pos,
                 bool fleeing) {
  if (!bot) return;
  Hunter* h = Find(bot);
  if (!h) {
    g_hunters.push_back(Hunter());
    h = &g_hunters.back();
    h->id = bot;
    h->noun = noun ? noun : "?";
    h->team = team;
    Log("agents: bot %08X %s hunter route: a Hunter of team %d, %zu steps (HUNTERROUTE)", bot, h->noun.c_str(), team,
        Steps(team).size());
  }
  const double now = director::MatchNow();
  if (level > 0) {
    const int before = h->levels.last;
    h->level = level;
    h->levels.Note(level, now);
    if (level > before && (level == 2 || level == 4 || level == 6) && LogBudget())
      Log("agents: bot %08X %s hunter route: level %d at %.1f s (HUNTERROUTE)", bot, h->noun.c_str(), level, now);
  }
  h->hp = hpFrac;
  h->pos[0] = pos[0];
  h->pos[1] = pos[1];
  h->pos[2] = pos[2];
  h->dead = false;
  if (fleeing && !h->fleeing && h->level <= 1) {
    ++h->fleesL1;
    if (LogBudget())
      Log("agents: bot %08X %s hunter route: flees at level 1, hp %.0f %%, at (%.1f, %.1f) (HUNTERROUTE) (t %.2f s)",
          bot, h->noun.c_str(), static_cast<double>(hpFrac * 100.f), static_cast<double>(pos[0]),
          static_cast<double>(pos[1]), now);
  }
  h->fleeing = fleeing;
  if (h->lastDecideS < 0.0 || now - h->lastDecideS >= 0.5 || now < h->lastDecideS) Decide(*h, now);
}

void BotDying(std::uint32_t bot) {
  Hunter* h = Find(bot);
  if (!h || h->dead) return;
  h->dead = true;
  ++h->deaths;
  if (h->level <= 1) ++h->deathsL1;
  if (LogBudget())
    Log("agents: bot %08X %s hunter route: died at level %d, route step %d (HUNTERROUTE) (t %.2f s)", bot,
        h->noun.c_str(), h->level, h->step, director::MatchNow());
}

bool RoutePoint(std::uint32_t bot, float* out) {
  if (!g_opt.route) return false;
  Hunter* h = Find(bot);
  if (!h || h->dead) return false;
  const double now = director::MatchNow();
  Decide(*h, now);
  if (h->step < 0) return false;
  std::vector<gb::RouteCamp> camps;
  std::vector<director::CampView> views;
  std::vector<int> index;
  CampsFor(h->team, &camps, &views, &index);
  const int k = index[static_cast<std::size_t>(h->step)];
  if (k < 0) return false;
  const director::CampView& v = views[static_cast<std::size_t>(k)];
  out[0] = v.pos[0];
  out[1] = v.pos[1];
  out[2] = v.pos[2];
  return true;
}

std::uint32_t PullTarget(std::uint32_t bot, const char* noun, int team, const float* pos, float hpFrac) {
  if (g_opt.pull <= 0 || team < 1 || team > 2) return 0;
  TeamPull& p = g_pull[team];
  if (p.over || (p.laner && p.laner != bot)) return 0;
  Hunter* h = HunterOfTeam(team);
  if (!h) return 0;
  const double now = director::MatchNow();
  if (!gb::RouteKitWantsPull(h->noun.c_str(), g_opt.pull)) {
    p.over = true;
    p.end = "the Hunter's kit clears alone";
    return 0;
  }
  if (!p.started && (h->mem.done & 1u)) {
    p.over = true;
    p.end = "the first camp is over";
    return 0;
  }
  if (!p.started && h->step != 0) return 0;
  std::vector<gb::RouteCamp> camps;
  std::vector<director::CampView> views;
  std::vector<int> index;
  CampsFor(team, &camps, &views, &index);
  if (camps.empty() || index[0] < 0 || !camps[0].alive) {
    if (p.started) {
      p.over = true;
      p.end = "the camp cleared";
      p.endedAt = now;
    }
    return 0;
  }
  const director::CampView& v = views[static_cast<std::size_t>(index[0])];
  if (p.spawnAt < 0.0) p.spawnAt = now;
  director::CampFight f;
  if (!director::CampFightNear(v.pos[0], v.pos[1], 25.f, &f) || f.camp < 0) return 0;
  const float campFrac = f.fullHp > 0.f ? f.hp / f.fullHp : 1.f;
  const float lanerDist = Dist(pos, v.pos);
  if (!p.started && lanerDist > kPull.reach) {
    if (now - p.spawnAt > kPull.openS) {
      p.over = true;
      p.end = "no laner in reach in time";
      p.endedAt = now;
      if (LogBudget())
        Log("hunterroute: pull team %d: none (%s) (HUNTERROUTE) (t %.2f s)", team, p.end, now);
    }
    return 0;
  }
  if (!gb::PullGoesOn(now, p.spawnAt, p.started, p.startedAt, campFrac, hpFrac, Dist(h->pos, v.pos), kPull)) {
    if (p.started || now - p.spawnAt > kPull.openS) {
      p.over = true;
      p.end = PullEndWhy(now, p, campFrac, hpFrac);
      p.endedAt = now;
      if (LogBudget())
        Log("hunterroute: pull team %d: %s ends (%s), camp %.0f %%, after %.1f s, requests %u (acted %u) "
            "(HUNTERROUTE) (t %.2f s)",
            team, p.lanerNoun.empty() ? "nobody" : p.lanerNoun.c_str(), p.end,
            static_cast<double>(campFrac * 100.f), p.started ? now - p.startedAt : 0.0, p.requests, p.acted, now);
    }
    return 0;
  }
  if (!p.started) {
    p.started = true;
    p.startedAt = now;
    p.laner = bot;
    p.lanerNoun = noun ? noun : "?";
    p.lanerDist = lanerDist;
    if (LogBudget())
      Log("hunterroute: pull team %d: %08X %s, %.1f from %s, hits it for the Hunter %08X %s (camp %.0f %%) "
          "(HUNTERROUTE) (t %.2f s)",
          team, bot, p.lanerNoun.c_str(), static_cast<double>(lanerDist), v.name.c_str(), h->id, h->noun.c_str(),
          static_cast<double>(campFrac * 100.f), now);
  }
  // the camp's creature with the most HP (the major)
  std::uint32_t best = 0;
  float bestHp = 0.f;
  for (int k = 0; k < f.count && k < director::CampFight::kMembers; ++k)
    if (f.hps[k] > bestHp) {
      bestHp = f.hps[k];
      best = f.ids[k];
    }
  return best;
}

void NotePullRequest(std::uint32_t bot, std::uint32_t target, int res) {
  (void)target;
  for (TeamPull& p : g_pull) {
    if (p.laner != bot) continue;
    ++p.requests;
    if (res == 1 || res == 2 || res == 5) ++p.acted;
  }
}

bool VanquishDue(std::uint32_t bot, unsigned long long nowMs) {
  if (!g_opt.vanquish) return false;
  const Hunter* h = Find(bot);
  return h && nowMs >= h->vqNextMs;
}

void NoteVanquish(std::uint32_t bot, bool host, int res, bool objective, std::uint32_t target, const char* targetNoun,
                  float targetHp, unsigned long long nowMs) {
  Hunter* h = Find(bot);
  if (!h) return;
  const bool started = res == 1 || res == 2;
  if (host) {
    ++h->vqTries;
    constexpr int kOnCooldown = -9999;
    h->vqNextMs = nowMs + (started ? 1000ull : res == kOnCooldown ? 5000ull : 1000ull);
    if (started) {
      ++h->vqHost;
      if (objective) ++h->vqHostObjective;
    }
  } else if (started) {
    ++h->vqPlan;
    if (objective) ++h->vqPlanObjective;
  }
  if ((started || !host) && LogBudget())
    Log("agents: bot %08X %s hunter route: Vanquish (%s) at %08X %s, hp %.0f, level %d -> %d%s (HUNTERROUTE) (t %.2f "
        "s)",
        bot, h->noun.c_str(), host ? "host rule" : "the plan's gambit", target, targetNoun ? targetNoun : "?",
        static_cast<double>(targetHp), h->level, res, started ? " started" : "", director::MatchNow());
}

bool FormatStats(char* buf, std::size_t size) {
  if (g_hunters.empty() || !size) return false;
  int len = std::snprintf(buf, size, "route %s, Vanquish rule %s, pull %d", g_opt.route ? "on" : "off",
                          g_opt.vanquish ? "on" : "off", g_opt.pull);
  for (const Hunter& h : g_hunters) {
    if (len < 0 || static_cast<std::size_t>(len) >= size) break;
    const std::vector<gb::RouteStep>& steps = Steps(h.team);
    char lv[3][16];
    for (int i = 0; i < 3; ++i) {
      if (h.levels.at[i] < 0.0) std::snprintf(lv[i], sizeof lv[i], "-");
      else std::snprintf(lv[i], sizeof lv[i], "%.1f s", h.levels.at[i]);
    }
    len += std::snprintf(
        buf + len, size - static_cast<std::size_t>(len),
        "; %s t%d (%08X): level %d, level 2 at %s, 4 at %s, 6 at %s; route %s%s, step %d of %zu, taken [%s]; Vanquish "
        "host %u (objective %u, tries %u), plan %u (objective %u); level 1: flees %u, deaths %u; deaths %u",
        h.noun.c_str(), h.team, h.id, h.level, lv[0], lv[1], lv[2], h.mem.over ? "over: " : "on",
        h.mem.over ? h.mem.overWhy : "", h.step, steps.size(), h.taken.c_str(), h.vqHost, h.vqHostObjective,
        h.vqTries, h.vqPlan, h.vqPlanObjective, h.fleesL1, h.deathsL1, h.deaths);
  }
  for (int t = 1; t <= 2; ++t) {
    if (len < 0 || static_cast<std::size_t>(len) >= size) break;
    const TeamPull& p = g_pull[t];
    len += std::snprintf(buf + len, size - static_cast<std::size_t>(len),
                         "; pull t%d: %s%s%s, laner %s, %.1f s, requests %u (acted %u)", t,
                         p.started ? "started" : "not started", p.over ? ", over: " : "", p.over ? p.end : "",
                         p.lanerNoun.empty() ? "-" : p.lanerNoun.c_str(),
                         p.started ? (p.endedAt >= 0.0 ? p.endedAt : director::MatchNow()) - p.startedAt : 0.0,
                         p.requests, p.acted);
  }
  return true;
}

}  // namespace hunterroute
