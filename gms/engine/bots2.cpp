// The second bot brain on the match host (BOTS2; bots2.h): the snapshot, the thinks, the execution and the census.
#include "bots2.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "core/agents/brain.h"  // kSlotBasic, kSlotQ, kSlotR, kResultHoldsAgent
#include "core/bots/kits.h"
#include "core/bots2/brain.h"
#include "core/bots2/census.h"
#include "core/bots2/walk.h"
#include "agents.h"
#include "bots.h"
#include "log.h"
#include "match.h"
#include "matchrec.h"
#include "wardspots.h"
#include "wells.h"
#include "host/host.h"

namespace bots2 {
namespace {

namespace b2 = gms::bots2;

struct Settings {
  b2::BrainChoice brains;  // BOTS2c: the brain of each team
  bool v2 = false;         // the second brain drives a team (brains.Any())
  bool census = true;
  int logLimit = 400;
};
Settings g_set;
b2::Tuning g_tuning;
b2::Brain g_brain;
b2::Census g_census;
unsigned long long g_nextThinkMs = 0, g_nextCensusMs = 0;
double g_nextCensusLineS = 60.0;
unsigned g_logLines = 0, g_idleLines = 0;

// How the host last drove each bot (so a walk isn't restarted at every think).
struct Exec {
  int mode = 0;  // 0 nothing yet, 1 walking to `point`, 2 following `target`, 3 stopped
  b2::Vec3 point;
  std::uint32_t target = 0;
  b2::State logged = b2::State::kCount;
  bool kitRead = false;
  const gms::bots::ShaperKit* kit = nullptr;
  unsigned long long slotRetryMs[8] = {};
  unsigned long long recallAskedMs = 0;
  b2::Act lastAct = b2::Act::kNone;  // the last intent (for the idle-stretch line)
  const char* lastWhy = "";
  b2::WalkWatch walk;  // BOTSFLIP: the walk asked last (mode 1), asked again when the bot stands short of it
  // BOTSFLIP round 2: the walks asked and the re-aims held back (for the idle stretch's end line), and their counts
  // at the stretch's start
  unsigned walksAsked = 0, reaimsHeld = 0, walksAtStretch = 0, heldAtStretch = 0;
  unsigned thinksV2 = 0, v2AtStretch = 0;
};
std::map<std::uint32_t, Exec> g_exec;
unsigned g_idleStretchLines = 0;

struct Counters {
  unsigned thinks = 0, frames = 0, moves = 0, moveRestarts = 0, stops = 0;
  unsigned casts = 0, castsStarted = 0, basics = 0, basicsStarted = 0, pursuits = 0, refused = 0;
  unsigned recalls = 0, recallsStarted = 0, heldSkips = 0, idleLines = 0, targetsDied = 0;
  // BOTS2c: the extras asked and started (spells, potions, wards), the workers the bots killed per team, the parasite
  // kills per team (its last hitter's team)
  unsigned spells = 0, spellsStarted = 0, potions = 0, potionsUsed = 0, wards = 0, wardsStarted = 0;
  unsigned workerKills[3] = {}, parasiteKills[3] = {};
  unsigned walkStuck = 0;  // BOTSFLIP: walks asked again by the watchdog (core/bots2/walk.h)
  unsigned reaimsHeld = 0;  // BOTSFLIP round 2: re-aims held back (WalkReaimNow)
};
Counters g_c;

// BOTS2c: who last hit the parasite and the workers (a bot's team), to name a kill when the object is gone
std::uint32_t g_parasiteId = 0;
int g_parasiteHitTeam = 0;
std::map<std::uint32_t, int> g_workerHitTeam;
unsigned g_extraLines = 0;

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_set.logLimit) || g_logLines % 100 == 0;
}

// BOTS2c: the extras' own log budget (spells, potions, wards: about 150 a match): 600 lines, then every 50th
bool ExtraLogBudget() {
  ++g_extraLines;
  return g_extraLines <= 600 || g_extraLines % 50 == 0;
}

// BOTS2c: the level's ward spots (MAPED4b's markers) once they are read, else the data's (SM_Map_v5's)
std::vector<b2::Vec3> g_wardSpots;
bool g_wardSpotsSettled = false;
void ReadWardSpots() {
  if (g_wardSpotsSettled) return;
  std::vector<b2::Vec3> level;
  bool keep = false, notReady = false;
  for (int side = 1; side <= 2 && !keep && !notReady; ++side)
    for (int k = 1; k <= 32; ++k) {
      float xy[2];
      const int a = wardspots::Answer(side, k, xy);
      if (a == 0) {
        keep = true;
        break;
      }
      if (a == 1) {
        notReady = true;
        break;
      }
      if (a != 2) break;
      level.push_back(b2::V(xy[0], xy[1]));
    }
  if (notReady && g_wardSpots.empty()) {  // meanwhile the data's
    int n = 0;
    const float (*d)[2] = b2::DataWardSpots(&n);
    for (int i = 0; i < n; ++i) g_wardSpots.push_back(b2::V(d[i][0], d[i][1]));
  }
  if (notReady) return;
  g_wardSpots.clear();
  if (keep || level.empty()) {
    int n = 0;
    const float (*d)[2] = b2::DataWardSpots(&n);
    for (int i = 0; i < n; ++i) g_wardSpots.push_back(b2::V(d[i][0], d[i][1]));
  } else {
    g_wardSpots = level;
  }
  g_wardSpotsSettled = true;
  Log("bots2: ward spots: %zu (%s)", g_wardSpots.size(), keep || level.empty() ? "the data's, SM_Map_v5's" : "the level's own");
}

bool IsRecall(const char* name) {
  if (!name) return false;
  std::string n(name);
  for (char& ch : n) ch = static_cast<char>(ch >= 'A' && ch <= 'Z' ? ch - 'A' + 'a' : ch);
  return n.find("returntobase") != std::string::npos;
}

const bots::Bot* BotOf(std::uint32_t creature) {
  for (const bots::Bot& b : bots::Players())
    if (b.creature == creature) return &b;
  return nullptr;
}

// ---- the snapshot ---------------------------------------------------------------------------------------------------

b2::World BuildWorld(const std::vector<ObjView>& objects, double now) {
  b2::World w;
  w.now = now;
  w.lanes = bots::LanePaths();
  for (const ObjView& o : objects) {
    const b2::Vec3 p = b2::V(o.pos[0], o.pos[1], o.pos[2]);
    if (o.player) {
      b2::Shaper s;
      s.id = o.id;
      s.team = o.team;
      s.pos = p;
      s.hp = o.hp;
      s.maxHp = o.maxHp > 0.f ? o.maxHp : 1.f;
      s.alive = o.hp > 0.f;
      float gold = 0.f, total = 0.f, xp = 0.f;
      int level = 1, ranks[7] = {};
      if (agents::bot2io::PlayerData(o.id, &gold, &total, &xp, &level, ranks)) {
        s.level = level;
        s.gold = gold;
      }
      if (const bots::Bot* b = BotOf(o.id)) {
        s.bot = true;
        s.lane = bots::CurrentLane(o.id);
        s.role = b->role;
        const char* held = nullptr;
        bool unint = false;
        s.channelling = agents::bot2io::Held(o.id, &held, &unint) && IsRecall(held);
        // BOTS2c: the next item step's price, when its gold could make a gold recall (the brain recalls only to buy)
        if (DrivesTeam(b->team) && s.gold >= g_tuning.recallGold) s.nextItemPrice = bots::NextItemPrice(o.id);
        // BOTS2c: its battle spells (inventory 6-8) and potions (9-10)
        agents::bot2io::InvSlot inv[11];
        if (agents::bot2io::Inventory(o.id, inv)) {
          for (int k = 0; k < 3; ++k) s.spells[k] = b2::SpellFromItemName(inv[6 + k].name);
          for (int k = 9; k <= 10; ++k)
            if (b2::IsPotionItemName(inv[k].name) && inv[k].count > 0) {
              s.potions += inv[k].count;
              if (s.potionSlot < 0) s.potionSlot = k + 1;  // the gambit slot (consume1 = inventory 9)
            }
        }
      }
      w.shapers.push_back(s);
      continue;
    }
    if (!(o.hp > 0.f)) continue;
    b2::Unit u;
    u.id = o.id;
    u.team = o.team;
    u.npc = o.npcType;
    u.pos = p;
    u.hp = o.hp;
    u.maxHp = o.maxHp > 0.f ? o.maxHp : o.hp;
    switch (o.npcType) {
      case b2::kNpcMelee:
      case b2::kNpcRanged:
      case b2::kNpcSuper:
        if (o.team == 1 || o.team == 2) w.minions.push_back(u);
        break;
      case b2::kNpcCreep:
      case b2::kNpcObjectiveMinor:
      case b2::kNpcObjectiveMedium:
        if (o.team != 1 && o.team != 2) w.creeps.push_back(u);
        break;
      case b2::kNpcParasite:  // BOTS2c
        w.hasParasite = true;
        w.parasiteHittable = o.attackable;
        w.parasite = u;
        break;
      case b2::kNpcWorker:
        if (o.team == 1 || o.team == 2) w.workers.push_back(u);
        break;
      case b2::kNpcWard:
        if (o.team == 1 || o.team == 2) w.wards.push_back(u);
        break;
      case b2::kNpcBinding:
      case b2::kNpcGuardian:
      case b2::kNpcCore:
      case b2::kNpcLocus: {
        if (o.team != 1 && o.team != 2) break;
        b2::Structure s;
        s.id = o.id;
        s.team = o.team;
        s.kind = o.npcType == b2::kNpcBinding  ? b2::Kind::kBinding
                 : o.npcType == b2::kNpcCore   ? b2::Kind::kCore
                 : o.npcType == b2::kNpcLocus  ? b2::Kind::kLocus
                                               : b2::Kind::kGuardian;
        s.pos = p;
        s.hp = o.hp;
        s.maxHp = u.maxHp;
        s.reach = o.reach;
        s.target = o.target;
        w.structures.push_back(s);
        if (s.kind == b2::Kind::kLocus) {
          w.base[o.team].hasLocus = true;
          w.base[o.team].locus = p;
        } else if (s.kind == b2::Kind::kGuardian) {
          w.base[o.team].hasGuardian = true;
          w.base[o.team].guardian = p;
        }
        break;
      }
      default: break;
    }
  }
  // BOTS2c: the wells (the wells module's state) and the ward spots
  wells::View wv[8];
  const int nw = wells::Views(wv, 8);
  for (int i = 0; i < nw; ++i) {
    b2::Well wl;
    wl.id = wv[i].object;
    wl.pos = b2::V(wv[i].pos[0], wv[i].pos[1], wv[i].pos[2]);
    wl.owner = wv[i].owner;
    wl.locked = wv[i].locked;
    wl.workers = wv[i].workers;
    wl.capturers = wv[i].capturers;
    wl.moving = wv[i].moving;
    wl.toCapture = wv[i].secondsToCapture;
    wl.workerHit = wv[i].workerPlayerHit;
    wl.reachable = wv[i].reachable;
    wl.radius = wv[i].radius;
    w.wells.push_back(wl);
  }
  ReadWardSpots();
  w.wardSpots = g_wardSpots;
  // the Guardian's shield: a living Core of its team
  for (b2::Structure& g : w.structures) {
    if (g.kind != b2::Kind::kGuardian) continue;
    for (const b2::Structure& c : w.structures)
      if (c.kind == b2::Kind::kCore && c.team == g.team) g.shielded = true;
  }
  // each team's spawn: a bot's start point
  for (const bots::Bot& b : bots::Players()) {
    if (!b.creature || (b.team != 1 && b.team != 2) || w.base[b.team].ok) continue;
    float sp[3];
    if (agents::bot2io::StartPoint(b.creature, sp) && !(sp[0] == 0.f && sp[1] == 0.f)) {
      w.base[b.team].ok = true;
      w.base[b.team].spawn = b2::V(sp[0], sp[1], sp[2]);
    }
  }
  // the teams' sight (BOTBUSH's rule): a team sees a shaper when one of its living shapers does (Agent_IsVisibleTo
  // answers for the viewer's team)
  std::uint32_t viewer[3] = {0, 0, 0};
  for (const b2::Shaper& s : w.shapers)
    if (s.alive && (s.team == 1 || s.team == 2) && !viewer[s.team]) viewer[s.team] = s.id;
  for (b2::Shaper& s : w.shapers) {
    for (int team = 1; team <= 2; ++team) {
      if (s.team == team) s.seenBy[team] = true;
      else s.seenBy[team] = s.alive && viewer[team] && agents::bot2io::Visible(viewer[team], s.id);
    }
  }
  return w;
}

// ---- BOTS2c: the parasite's and the workers' deaths, named by their last hitter's team (both brains) -------------------

std::map<std::uint32_t, int> g_shaperTeam;     // the last snapshot's shapers
std::map<std::uint32_t, float> g_lastWorkers;  // the last snapshot's workers
float g_parasiteFrac = 1.f;

void TrackKills(const b2::World& w) {
  for (const b2::Shaper& s : w.shapers) g_shaperTeam[s.id] = s.team;
  // a worker gone since the last snapshot that a shaper hit last: that team's raid kill
  std::map<std::uint32_t, float> now;
  for (const b2::Unit& u : w.workers) now[u.id] = u.hp;
  for (const auto& kv : g_lastWorkers) {
    if (now.count(kv.first)) continue;
    const auto hit = g_workerHitTeam.find(kv.first);
    if (hit != g_workerHitTeam.end() && (hit->second == 1 || hit->second == 2)) ++g_c.workerKills[hit->second];
    g_workerHitTeam.erase(kv.first);
  }
  g_lastWorkers.swap(now);
  // the parasite: gone while it was low = down (a stage-up replaces a living object at full health)
  if (w.hasParasite) {
    if (w.parasite.id != g_parasiteId) g_parasiteHitTeam = 0;
    g_parasiteId = w.parasite.id;
    g_parasiteFrac = w.parasite.maxHp > 0.f ? w.parasite.hp / w.parasite.maxHp : 1.f;
  } else if (g_parasiteId) {
    if (g_parasiteFrac < 0.25f) {
      if (g_parasiteHitTeam == 1 || g_parasiteHitTeam == 2) ++g_c.parasiteKills[g_parasiteHitTeam];
      const b2::TeamPlan& p = g_brain.Plan(g_parasiteHitTeam == 2 ? 2 : 1);
      Log("bots2: parasite %08X down at t %.1f: last hit by team %d%s", g_parasiteId, w.now, g_parasiteHitTeam,
          g_set.v2 && (g_parasiteHitTeam == 1 || g_parasiteHitTeam == 2) &&
                  (p.parasite || (w.now - p.parasiteEndedAt < 2.0 && std::strcmp(p.parasiteEndWhy, "down") == 0))
              ? " (the team's attempt)"
              : "");
    }
    g_parasiteId = 0;
    g_parasiteHitTeam = 0;
  }
}

// ---- BOTS2c: the laning metric of the first 10 minutes (v2: the user's "no traditional laning") ----------------------

constexpr double kLane10S = 600.0;
struct Lane10 {
  double secs[static_cast<int>(b2::State::kCount)] = {};
  int recalls = 0;  // recalls started
};
std::map<std::uint32_t, Lane10> g_lane10;
double g_pairSum = 0.0, g_pairMin = 1e9;
unsigned g_pairN = 0, g_pairClose = 0;  // samples of two bots of a lane both in Lane, and those under 3 units apart
bool g_lane10Logged = false;

void SampleLane10(const b2::World& w, double now) {
  if (!g_set.v2 || g_lane10Logged) return;
  if (now <= kLane10S) {
    for (int team = 1; team <= 2; ++team)
      for (int lane = 1; lane <= 2; ++lane) {
        std::vector<b2::Vec3> at;
        for (const bots::Bot& b : bots::Players()) {
          const b2::Shaper* s = b.creature ? w.Find(b.creature) : nullptr;
          const b2::Memory* m = s ? g_brain.MemoryOf(b.creature) : nullptr;
          if (!s || !m || !s->alive || s->team != team) continue;
          if (lane == 1) g_lane10[b.creature].secs[static_cast<int>(m->state)] += 1.0;  // once a bot a sample
          if (s->lane == lane && m->state == b2::State::kLane) at.push_back(s->pos);
        }
        for (std::size_t i = 0; i < at.size(); ++i)
          for (std::size_t j = i + 1; j < at.size(); ++j) {
            const double d = b2::Dist(at[i], at[j]);
            g_pairSum += d;
            g_pairMin = std::min(g_pairMin, d);
            ++g_pairN;
            if (d < 3.0) ++g_pairClose;
          }
      }
    return;
  }
  g_lane10Logged = true;
  char line[1900];
  int n = std::snprintf(line, sizeof line, "bots2 lane10: the first %.0f s;", kLane10S);
  for (const bots::Bot& b : bots::Players()) {
    const auto it = g_lane10.find(b.creature);
    if (!b.creature || it == g_lane10.end() || n < 0 || n >= static_cast<int>(sizeof line) - 160) continue;
    const Lane10& l = it->second;
    auto sec = [&](b2::State st) { return l.secs[static_cast<int>(st)]; };
    const double other = sec(b2::State::kDefend) + sec(b2::State::kWell) + sec(b2::State::kParasite) +
                         sec(b2::State::kJungle) + sec(b2::State::kPush);
    n += std::snprintf(line + n, sizeof line - n,
                       " [%d t%d r%d lane %.0f fight %.0f retreat %.0f recall %.0f base %.0f dead %.0f other %.0f "
                       "recalls %d cs %d]",
                       b.index, b.team, b.role, sec(b2::State::kLane), sec(b2::State::kFight), sec(b2::State::kRetreat),
                       sec(b2::State::kRecall), sec(b2::State::kBase), sec(b2::State::kDead), other, l.recalls,
                       agents::bot2io::Creeps(b.creature));
  }
  if (n > 0 && n < static_cast<int>(sizeof line) - 120)
    std::snprintf(line + n, sizeof line - n,
                  "; lane pairs (two bots of a lane, both laning): mean %.1f apart (min %.1f), under 3 units %u of %u "
                  "samples",
                  g_pairN ? g_pairSum / g_pairN : -1.0, g_pairN ? g_pairMin : -1.0, g_pairClose, g_pairN);
  Log("%s", line);
}

// ---- the census -----------------------------------------------------------------------------------------------------

void CensusLine(const b2::World& w, double now) {
  char line[1900];
  int n = std::snprintf(line, sizeof line, "bots2 census: t %.0f brain %s;", now, BrainLabel());
  for (const bots::Bot& b : bots::Players()) {
    const auto it = g_census.Bots().find(b.creature);
    if (!b.creature || it == g_census.Bots().end() || n < 0 || n >= static_cast<int>(sizeof line) - 120) continue;
    const b2::CensusBot& c = it->second;
    n += std::snprintf(line + n, sizeof line - n,
                       " [%d t%d L%d gold %.0f xp %.0f deaths %d idle %.0f away %.0f long %.0f cs %d]", b.index, b.team,
                       c.level, static_cast<double>(c.gold), static_cast<double>(c.xp), c.deaths, c.idleS, c.awayS,
                       c.idleLongestS, agents::bot2io::Creeps(b.creature));
  }
  int cnt[3][4] = {};
  for (const b2::Structure& s : w.structures)
    if (s.team == 1 || s.team == 2) ++cnt[s.team][static_cast<int>(s.kind)];
  if (n > 0 && n < static_cast<int>(sizeof line) - 80)
    std::snprintf(line + n, sizeof line - n, "; structures t1 B%d C%d G%d, t2 B%d C%d G%d", cnt[1][0], cnt[1][1],
                  cnt[1][2], cnt[2][0], cnt[2][1], cnt[2][2]);
  Log("%s", line);
}

void SampleCensus(const b2::World& w, double now) {
  for (const bots::Bot& b : bots::Players()) {
    if (!b.creature) continue;
    const b2::Shaper* s = w.Find(b.creature);
    if (!s) continue;
    float gold = 0.f, total = 0.f, xp = 0.f;
    int level = 1, ranks[7] = {};
    agents::bot2io::PlayerData(b.creature, &gold, &total, &xp, &level, ranks);
    const char* held = nullptr;
    bool unint = false;
    // busy: a cast, the recall's channel; since BOTS2c also capturing or contesting a well (standing in its ring)
    const bool busy = s->alive && (agents::bot2io::Held(b.creature, &held, &unint) || b2::InWellAction(w, *s));
    const auto before = g_census.Bots().find(b.creature);
    const double runBefore = before != g_census.Bots().end() ? before->second.idleRunS : 0.0;
    g_census.Sample(b.creature, s->team, s->alive, b2::InBase(w, *s, g_tuning), s->pos, now, total, xp, level,
                    g_tuning.idleMove, g_tuning.idleHitS, busy);
    // an idle stretch reaching 3 s: what the bot was doing (its v2 state and intent, else nothing known)
    const double runAfter = g_census.Bots().at(b.creature).idleRunS;
    if (runBefore < 1e-9 && runAfter > 0.0) {  // BOTSFLIP round 2: a stretch starts: the executor's counts now
      Exec& es = g_exec[b.creature];
      es.walksAtStretch = es.walksAsked;
      es.heldAtStretch = es.reaimsHeld;
      es.v2AtStretch = es.thinksV2;
    }
    if (runBefore < 3.0 && runAfter >= 3.0 && ++g_idleStretchLines <= 300) {
      const b2::Memory* m = DrivesTeam(b.team) ? g_brain.MemoryOf(b.creature) : nullptr;
      const Exec& e = g_exec[b.creature];
      Log("bots2 idle stretch: bot %08X %s (player %d, team %d) at (%.1f, %.1f), hp %.0f%%: 3 s without a move or a "
          "hit; state %s, last intent %s %s (t %.1f)",
          b.creature, agents::bot2io::Noun(b.creature), b.index, b.team, static_cast<double>(s->pos.x),
          static_cast<double>(s->pos.y), static_cast<double>(s->Frac() * 100.f), m ? b2::StateName(m->state) : "-",
          b2::ActName(e.lastAct), e.lastWhy, now);
    }
    // BOTS2c: its end with its length (the line above comes at 3 s; the census's "long" is the longest stretch)
    // BOTSFLIP round 2: and what the executor did meanwhile (v2's thinks of it, the walks asked, the re-aims held
    // back, the last walk's goal)
    if (runBefore >= 3.0 && runAfter < 1e-9 && ++g_idleStretchLines <= 300) {
      const Exec& e = g_exec[b.creature];
      Log("bots2 idle stretch end: bot %08X %s (player %d, team %d, %s) idle %.0f s, at (%.1f, %.1f) (t %.1f); v2 "
          "thinks %u, walks asked %u, re-aims held %u, last goal (%.1f, %.1f)",
          b.creature, agents::bot2io::Noun(b.creature), b.index, b.team, BrainOfTeam(b.team), runBefore,
          static_cast<double>(s->pos.x), static_cast<double>(s->pos.y), now, e.thinksV2 - e.v2AtStretch,
          e.walksAsked - e.walksAtStretch, e.reaimsHeld - e.heldAtStretch, static_cast<double>(e.point.x),
          static_cast<double>(e.point.y));
    }
  }
  SampleLane10(w, now);  // BOTS2c
  if (now >= g_nextCensusLineS) {
    g_nextCensusLineS = (std::floor(now / 60.0) + 1.0) * 60.0;
    CensusLine(w, now);
  }
}

// ---- the execution --------------------------------------------------------------------------------------------------

bool TargetSpot(const b2::World& w, std::uint32_t id, float* p) {
  b2::Vec3 v;
  if (const b2::Shaper* s = w.Find(id)) v = s->pos;
  else if (const b2::Structure* st = w.FindStructure(id)) v = st->pos;
  else if (const b2::Unit* u = w.FindUnit(id)) v = u->pos;
  else return false;
  p[0] = v.x;
  p[1] = v.y;
  p[2] = v.z;
  return true;
}

float NearestEnemyShaper(const b2::World& w, const b2::Shaper& me) {
  float best = -1.f;
  for (const b2::Shaper& s : w.shapers) {
    if (s.team == me.team || !s.alive || !s.seenBy[me.team]) continue;
    const float d = b2::Dist(s.pos, me.pos);
    if (best < 0.f || d < best) best = d;
  }
  return best;
}

// BOTS2c: an extra (a spell or the ward through the request path, as the kits' casts; a potion through LabsPlayer_UseItem,
// as the classic brain's consume gambits); the brain hears the result. True when it started.
bool ExecuteExtra(const b2::World& w, const b2::Shaper& me, const b2::Extra& x) {
  namespace io = agents::bot2io;
  int r = 0;
  std::uint32_t object = x.target;
  float pos[3] = {me.pos.x, me.pos.y, me.pos.z};  // a self cast: the bot's own spot
  if (x.kind == b2::ExtraKind::kPotion) {
    std::uint32_t item = 0;
    const int inv = x.slot - 1;  // consume1 = inventory 9
    const int u = io::UseItem(me.id, inv, &item);
    r = u > 0 ? 1 : gms::agents::kResultItemRefused;
    ++g_c.potions;
    if (u > 0) ++g_c.potionsUsed;
    object = item;
    matchrec::OnBotItemUse(me.id, "BOTS2 potion", item, inv, u > 0, me.gold);
  } else if (x.kind == b2::ExtraKind::kSpell || x.kind == b2::ExtraKind::kWard) {
    if (object) {
      // the snapshot's target may have died since (an earlier request of this think): the next think chooses again
      if (!TargetSpot(w, object, pos) || !io::Alive(object)) {
        ++g_c.targetsDied;
        return false;
      }
    } else if (x.atPoint) {
      pos[0] = x.point.x;
      pos[1] = x.point.y;
      pos[2] = x.point.z != 0.f ? x.point.z : me.pos.z;
    }
    const bool ward = x.kind == b2::ExtraKind::kWard;
    r = io::Request(me.id, 0, x.slot, object, pos, 1, ward ? "BOTS2 ward" : "BOTS2 spell");
    const bool started = r >= 1 && r <= 4;
    if (ward) {
      ++g_c.wards;
      if (started) ++g_c.wardsStarted;
    } else {
      ++g_c.spells;
      if (started) ++g_c.spellsStarted;
    }
  } else {
    return false;
  }
  g_brain.NoteExtra(me.id, x, r, w.now);
  const bool started = r >= 1 && r <= 4;
  if (ExtraLogBudget())
    Log("bots2: bot %08X %s %s%s%s at %08X (%.1f, %.1f) slot %d -> %d (%s) (t %.1f, hp %.0f%%)", me.id, io::Noun(me.id),
        b2::ExtraName(x.kind), x.kind == b2::ExtraKind::kSpell ? " " : "",
        x.kind == b2::ExtraKind::kSpell ? b2::SpellName(x.spell) : "", object, static_cast<double>(pos[0]),
        static_cast<double>(pos[1]), x.slot, r, x.why, w.now, static_cast<double>(me.Frac() * 100.f));
  return started;
}

void Execute(const b2::World& w, const b2::Shaper& me, const b2::Intent& in, unsigned long long nowMs) {
  using namespace gms::agents;
  namespace io = agents::bot2io;
  Exec& e = g_exec[me.id];
  const char* held = nullptr;
  bool unint = false;
  if (io::Held(me.id, &held, &unint) && unint) {  // a cast before its release, a channel: never cut
    ++g_c.heldSkips;
    return;
  }
  // BOTS2c: the extra first; a spell or ward that started holds the intent's request to the next think (as a kit cast
  // does), a potion doesn't
  if (in.extra.kind != b2::ExtraKind::kNone && in.act != b2::Act::kRecall && in.act != b2::Act::kShop) {
    if (ExecuteExtra(w, me, in.extra) && in.extra.kind != b2::ExtraKind::kPotion) return;
  }
  switch (in.act) {
    case b2::Act::kNone: return;
    case b2::Act::kShop:
      if (e.mode != 3) {
        io::FollowStop(me.id);
        e.mode = 3;
        ++g_c.stops;
      }
      return;
    case b2::Act::kRecall: {
      if (me.channelling || nowMs - e.recallAskedMs < 1000) return;
      io::FollowStop(me.id);
      e.mode = 3;
      const int r = io::RequestRecall(me.id);
      e.recallAskedMs = nowMs;
      ++g_c.recalls;
      if (r == 1 || r == 2) {
        ++g_c.recallsStarted;
        if (w.now <= kLane10S) ++g_lane10[me.id].recalls;  // BOTS2c's laning metric
      }
      if (LogBudget())
        Log("bots2: bot %08X %s recall -> %d (%s) (t %.1f)", me.id, io::Noun(me.id), r, in.why, w.now);
      return;
    }
    case b2::Act::kMove: {
      const float z = in.point.z != 0.f ? in.point.z : me.pos.z;
      const b2::Vec3 goal = b2::V(in.point.x, in.point.y, z);
      if (e.mode == 1 && b2::Dist(e.point, goal) <= g_tuning.reaimDist) {  // still walking there ...
        // BOTSFLIP: ... unless the game's follow ended short of the goal (the VPS match: 171 s standing in the Pursue
        // leaf with the goal unchanged): asked again
        if (!b2::WalkStuck(e.walk, me.pos, w.now, g_tuning)) return;
        const float q[3] = {e.point.x, e.point.y, e.point.z};
        io::FollowPoint(me.id, q, true);
        ++e.walksAsked;
        ++g_c.walkStuck;
        ++g_c.moveRestarts;
        static unsigned stuckLines = 0;
        if (++stuckLines <= 40 || stuckLines % 50 == 0)
          Log("bots2: bot %08X %s walk asked again: stood %.1f s at (%.1f, %.1f), %.0f from its goal (%.1f, %.1f) (%s; "
              "restart %u of this walk, %u in all) (t %.1f)",
              me.id, io::Noun(me.id), static_cast<double>(g_tuning.walkStuckS), static_cast<double>(me.pos.x),
              static_cast<double>(me.pos.y), static_cast<double>(b2::Dist(me.pos, e.point)),
              static_cast<double>(e.point.x), static_cast<double>(e.point.y), in.why, e.walk.restarts, g_c.walkStuck,
              w.now);
        return;
      }
      // BOTSFLIP round 2: a running walk whose bot hasn't moved since the last ask is re-aimed at most every
      // walkReaimMinS (each re-aim restarts the follow; botsflip_b1: 79 s standing under re-aims every think)
      if (e.mode == 1 && !b2::WalkReaimNow(e.walk, me.pos, w.now, g_tuning)) {
        ++g_c.reaimsHeld;
        ++e.reaimsHeld;
        return;
      }
      const float p[3] = {goal.x, goal.y, goal.z};
      const bool restart = e.mode == 1 || e.mode == 2;
      io::FollowPoint(me.id, p, restart);
      ++e.walksAsked;
      if (restart) ++g_c.moveRestarts;
      e.mode = 1;
      e.point = goal;
      e.target = 0;
      b2::WalkStart(e.walk, goal, me.pos, w.now);
      ++g_c.moves;
      return;
    }
    case b2::Act::kAttack: {
      float pos[3];
      // the snapshot's target may have died since (another bot's request earlier in this think): the next think chooses
      if (!TargetSpot(w, in.target, pos) || !io::Alive(in.target)) {
        ++g_c.targetsDied;
        return;
      }
      io::FollowTarget(me.id, in.target);
      e.mode = 2;
      e.target = in.target;
      if (in.abilities) {
        if (!e.kitRead) {
          e.kitRead = true;
          e.kit = gms::bots::KitForNoun(io::Noun(me.id));
        }
        float gold = 0.f, total = 0.f, xp = 0.f;
        int level = 1, ranks[7] = {};
        io::PlayerData(me.id, &gold, &total, &xp, &level, ranks);
        std::vector<int> slots = gms::bots::ReflexSlots(e.kit, false);
        if (in.ult) slots.insert(slots.begin(), kSlotR);
        const float targetDist = b2::Dist(me.pos, b2::V(pos[0], pos[1]));
        const float nearestEnemy = NearestEnemyShaper(w, me);
        for (int slot : slots) {
          if (slot == kSlotBasic) break;  // the basic attack below
          if (slot < 0 || slot > 6 || ranks[slot] <= 0 || nowMs < e.slotRetryMs[slot]) continue;
          if (!gms::bots::KitAllowsSlot(e.kit, slot) || !gms::bots::KitAllowsW(e.kit, slot, nearestEnemy) ||
              !gms::bots::KitAllowsWAt(e.kit, slot, targetDist))
            continue;
          const int r = io::Request(me.id, 0, slot, in.target, pos, ranks[slot], "BOTS2 cast");
          ++g_c.casts;
          if (r == 1 || r == 2) {
            ++g_c.castsStarted;
            return;
          }
          if (r == 5) {  // out of the ability's range: the follow leaf walks up
            ++g_c.pursuits;
            return;
          }
          if (r == kResultHoldsAgent) return;
          e.slotRetryMs[slot] = nowMs + 1500;  // on cooldown, no resource, refused: the next slot
          if (!io::Alive(in.target)) {  // the cast that was refused may still have hit (a kill)
            ++g_c.targetsDied;
            return;
          }
        }
      }
      const int r = io::Request(me.id, 0, kSlotBasic, in.target, pos, 1, "BOTS2 attack");
      ++g_c.basics;
      if (r == 1 || r == 2) ++g_c.basicsStarted;
      else if (r == 5) ++g_c.pursuits;
      else if (r < 0 && r != kResultCooldown && r != kResultHoldsAgent) ++g_c.refused;
      return;
    }
    default: return;
  }
}

// ---- BOTS2c round 7: the hybrid (the classic brain's micro, v2's macro) ----------------------------------------------

struct HybridState {
  bool driven = false;  // v2 drives it (BotBrainStep skipped: DrivesBot)
  double since = -1e9;
  double enemyNearAt = -1e9;  // round 8: the last think with an enemy shaper within hybridCalmRange
  double backoffUntil = -1e9;  // round 8: no task takeover before this (a task an enemy ended)
  b2::HybridDriver why = b2::HybridDriver::kClassic;
};
std::map<std::uint32_t, HybridState> g_hybrid;
struct HybridCounters {
  unsigned thinksV2 = 0, thinksClassic = 0, takeoversTask = 0, takeoversRescue = 0, releases = 0, backoffs = 0;
};
HybridCounters g_hc;
unsigned g_hybridLines = 0;

// Who drives hybrid bot `b` this think (core ChooseDriver); a takeover drops v2's stale tactics, a release stops v2's
// walk so the classic tree's own leaves run again. True when v2 drives it now.
bool HybridTurn(const b2::World& w, const bots::Bot& b, const b2::Shaper& me) {
  HybridState& hs = g_hybrid[b.creature];
  const b2::TeamPlan& p = g_brain.Plan(b.team);
  b2::HybridView v;
  v.alive = me.alive;
  v.inBase = b2::InBase(w, me, g_tuning);
  v.hp = me.Frac();
  const float ne = NearestEnemyShaper(w, me);
  v.nearestEnemy = ne < 0.f ? 1.0e30f : ne;
  if (v.nearestEnemy <= g_tuning.hybridCalmRange) hs.enemyNearAt = w.now;
  v.calmS = w.now - hs.enemyNearAt;
  // round 8: the push's task is its push group's only (the rest farm with the classic brain), and only grouped, with
  // the advantage or behind the own wave; of the wells an own-side retake and a defence (no walk into the enemy half)
  const bool inGroup = std::find(p.pushGroup.begin(), p.pushGroup.end(), b.creature) != p.pushGroup.end();
  v.teamTask = (p.push && inGroup && (p.grouped || p.advantage || p.rallySafe)) || p.parasite ||
               (p.wellBot == b.creature && p.wellKind == b2::TeamPlan::kWellRetake) ||
               (p.wellThreat && std::find(p.wellDefenders.begin(), p.wellDefenders.end(), b.creature) !=
                                    p.wellDefenders.end());
  const auto cb = g_census.Bots().find(b.creature);
  v.idleRunS = cb != g_census.Bots().end() ? cb->second.idleRunS : 0.0;
  v.driven = hs.driven;
  v.drivenSince = hs.since;
  v.rescue = hs.driven && hs.why == b2::HybridDriver::kV2Rescue;  // BOTSFLIP: a rescue holds hybridRescueHoldS
  v.backoff = w.now < hs.backoffUntil;
  // round 9: the base's siege with its edge; round 11: the Guardian phase, every living bot (a v2 takeover that holds)
  v.finish = (p.siege && p.siegeEdge && inGroup) || p.guard != b2::TeamPlan::kGuardOff;
  v.now = w.now;
  const b2::HybridDriver d = b2::ChooseDriver(v, g_tuning);
  const bool v2 = d != b2::HybridDriver::kClassic;
  if (v2 && !hs.driven) {
    g_brain.ResetTactics(b.creature);
    hs.since = w.now;
    hs.why = d;
    ++(d == b2::HybridDriver::kV2Rescue ? g_hc.takeoversRescue : g_hc.takeoversTask);
    if (++g_hybridLines <= 600 || g_hybridLines % 50 == 0)
      Log("bots2: bot %08X %s (player %d, team %d) hybrid -> v2 (%s) (t %.1f, hp %.0f%%)", b.creature,
          agents::bot2io::Noun(b.creature), b.index, b.team,
          d == b2::HybridDriver::kV2Rescue ? "rescue: idle" : v.finish ? "the finish" : "the team's task", w.now,
          static_cast<double>(v.hp * 100.f));
  } else if (!v2 && hs.driven) {
    agents::bot2io::FollowStop(b.creature);
    g_exec[b.creature].mode = 0;
    ++g_hc.releases;
    if (hs.why == b2::HybridDriver::kV2Task && v.alive && !v.inBase && v.nearestEnemy <= g_tuning.hybridFightRange) {
      hs.backoffUntil = w.now + g_tuning.hybridTaskBackoffS;  // round 8: the task met an enemy: not again for a while
      ++g_hc.backoffs;
    }
    const char* why = !v.alive                                         ? "dead"
                      : v.inBase                                       ? "in base"
                      : v.nearestEnemy <= g_tuning.hybridFightRange    ? "an enemy near"
                      : v.hp < g_tuning.retreatHp                      ? "hurt"
                                                                       : "no task";
    if (++g_hybridLines <= 600 || g_hybridLines % 50 == 0)
      Log("bots2: bot %08X %s (player %d, team %d) hybrid -> classic (%s) (t %.1f, hp %.0f%%)", b.creature,
          agents::bot2io::Noun(b.creature), b.index, b.team, why, w.now, static_cast<double>(v.hp * 100.f));
  }
  hs.driven = v2;
  ++(v2 ? g_hc.thinksV2 : g_hc.thinksClassic);
  return v2;
}

void Think(const b2::World& w, unsigned long long nowMs) {
  ++g_c.thinks;
  g_brain.PlanTeams(w);
  static std::uint32_t lastObjective[3] = {};
  static bool lastGrouped[3] = {}, lastPush[3] = {};
  for (int team = 1; team <= 2; ++team) {
    if (!DrivesTeam(team)) continue;  // BOTS2c: a classic team has no v2 plan
    const b2::TeamPlan& p = g_brain.Plan(team);
    if (p.objective == lastObjective[team] && p.grouped == lastGrouped[team] && p.push == lastPush[team]) continue;
    lastObjective[team] = p.objective;
    lastGrouped[team] = p.grouped;
    lastPush[team] = p.push;
    if (LogBudget())
      Log("bots2: team %d plan: %s%s, lane %d, objective %08X at (%.1f, %.1f), rally (%.1f, %.1f), group %d of %d%s "
          "(t %.1f)",
          team, p.push ? "push" : "laning", p.advantage ? " (advantage)" : "", p.pushLane, p.objective,
          static_cast<double>(p.objectivePos.x), static_cast<double>(p.objectivePos.y), static_cast<double>(p.rally.x),
          static_cast<double>(p.rally.y), p.groupCount, p.groupNeeded, p.grouped ? ", grouped" : "", w.now);
  }
  // round 11: the Guardian phase: its stage, target and group (its own lines, not budgeted: a few a match)
  static int lastGuard[3] = {};
  static std::uint32_t lastGuardTarget[3] = {};
  static int lastGuardCount[3] = {-1, -1, -1};
  for (int team = 1; team <= 2; ++team) {
    if (!DrivesTeam(team)) continue;
    const b2::TeamPlan& p = g_brain.Plan(team);
    const bool changed = p.guard != lastGuard[team] || p.guardTarget != lastGuardTarget[team];
    if (!changed && (p.guard != b2::TeamPlan::kGuardGather || p.guardCount == lastGuardCount[team])) continue;
    const b2::Structure* tg = w.FindStructure(p.guardTarget);
    int enemies = 0;
    for (const b2::Shaper& e : w.shapers)
      if (e.team == 3 - team && e.alive) ++enemies;
    const char* what = tg && tg->kind == b2::Kind::kGuardian ? "the Guardian" : "a Core";
    if (p.guard == b2::TeamPlan::kGuardOff) {
      Log("bots2: team %d guardian: off (%s) (t %.1f)", team, p.guardWhy, w.now);
    } else if (p.guard == b2::TeamPlan::kGuardEngage && lastGuard[team] == b2::TeamPlan::kGuardEngage) {
      Log("bots2: team %d guardian: in, %s %s %08X (t %.1f)", team, p.guardWhy, what, p.guardTarget, w.now);
    } else if (p.guard == b2::TeamPlan::kGuardEngage) {
      Log("bots2: team %d guardian: engage %s %08X with %d bots (%s; %d living, %d enemies living) (t %.1f)", team, what,
          p.guardTarget, p.guardEngaged, p.guardWhy, p.guardNeeded, enemies, w.now);
    } else if (changed && lastGuard[team] == b2::TeamPlan::kGuardEngage) {
      Log("bots2: team %d guardian: back off (%s), staging (%.1f, %.1f), group %d of %d (t %.1f)", team, p.guardWhy,
          static_cast<double>(p.staging.x), static_cast<double>(p.staging.y), p.guardCount, p.guardNeeded, w.now);
    } else {
      Log("bots2: team %d guardian: gather for %s %08X at the staging point (%.1f, %.1f)%s%s, group %d of %d (t %.1f)",
          team, what, p.guardTarget, static_cast<double>(p.staging.x), static_cast<double>(p.staging.y),
          changed ? ": " : "", changed ? p.guardWhy : "", p.guardCount, p.guardNeeded, w.now);
    }
    lastGuard[team] = p.guard;
    lastGuardTarget[team] = p.guardTarget;
    lastGuardCount[team] = p.guardCount;
  }
  // BOTS2c: the well tasks, the well defences and the parasite attempts (their own lines, not budgeted: a few a match)
  static std::uint32_t lastWellBot[3] = {}, lastWellThreat[3] = {};
  static std::size_t lastDefenders[3] = {};
  static bool lastParasite[3] = {}, lastParGrouped[3] = {};
  for (int team = 1; team <= 2; ++team) {
    if (!DrivesTeam(team)) continue;
    const b2::TeamPlan& p = g_brain.Plan(team);
    if (p.wellBot != lastWellBot[team]) {
      if (lastWellBot[team])
        Log("bots2: team %d well task ended: bot %08X %s: %s (t %.1f)", team, lastWellBot[team],
            agents::bot2io::Noun(lastWellBot[team]), p.wellEndWhy, w.now);
      if (p.wellBot) {
        const b2::Well* wl = w.FindWell(p.wellTarget);
        static const char* const kKind[] = {"none", "retake", "take", "raid"};
        Log("bots2: team %d well task: bot %08X %s %s well %08X at (%.1f, %.1f), owner %d, %d workers%s, %.0f away "
            "(t %.1f)",
            team, p.wellBot, agents::bot2io::Noun(p.wellBot), kKind[p.wellKind & 3], p.wellTarget,
            wl ? static_cast<double>(wl->pos.x) : 0.0, wl ? static_cast<double>(wl->pos.y) : 0.0, wl ? wl->owner : 0,
            wl ? wl->workers : 0, wl && wl->locked ? ", locked" : "",
            wl && w.Find(p.wellBot) ? static_cast<double>(b2::Dist(wl->pos, w.Find(p.wellBot)->pos)) : -1.0, w.now);
      }
      lastWellBot[team] = p.wellBot;
    }
    if (p.wellThreat != lastWellThreat[team] || p.wellDefenders.size() != lastDefenders[team]) {
      const b2::Well* wl = w.FindWell(p.wellThreat);
      std::string ids;
      for (std::uint32_t id : p.wellDefenders) {
        char one[16];
        std::snprintf(one, sizeof one, "%s%08X", ids.empty() ? "" : ",", id);
        ids += one;
      }
      if (p.wellThreat)
        Log("bots2: team %d well %08X threatened: %d capturer(s)%s%s, defenders %s%s%s (t %.1f)", team, p.wellThreat,
            wl ? wl->capturers : 0, wl && wl->moving ? ", capture moving" : "", wl && wl->workerHit ? ", worker hit" : "",
            ids.empty() ? "none (" : ids.c_str(), ids.empty() ? p.wellNoDefenderWhy : "", ids.empty() ? ")" : "",
            w.now);
      else if (lastWellThreat[team])
        Log("bots2: team %d well %08X: the threat is over (t %.1f)", team, lastWellThreat[team], w.now);
      lastWellThreat[team] = p.wellThreat;
      lastDefenders[team] = p.wellDefenders.size();
    }
    if (p.parasite != lastParasite[team] || p.parasiteGrouped != lastParGrouped[team]) {
      if (p.parasite && !lastParasite[team])
        Log("bots2: team %d parasite: attempt on %08X after a won fight (%d bots there, %d needed) (t %.1f)", team,
            p.parasiteId, p.parasiteCount, p.parasiteNeeded, w.now);
      else if (p.parasite && p.parasiteGrouped)
        Log("bots2: team %d parasite: the group (%d) hits it (t %.1f)", team, p.parasiteCount, w.now);
      else if (!p.parasite && lastParasite[team])
        Log("bots2: team %d parasite: attempt ended: %s (t %.1f)", team, p.parasiteEndWhy, w.now);
      lastParasite[team] = p.parasite;
      lastParGrouped[team] = p.parasiteGrouped;
    }
  }
  for (const bots::Bot& b : bots::Players()) {
    if (!b.creature || !DrivesTeam(b.team)) continue;  // BOTS2c: a classic team runs BotBrainStep
    const b2::Shaper* me = w.Find(b.creature);
    if (!me) continue;
    if (g_set.brains.Hybrid(b.team) && !HybridTurn(w, b, *me)) continue;  // round 7: the classic brain drives it now
    ++g_exec[b.creature].thinksV2;  // BOTSFLIP round 2: for the idle stretch's end line
    const b2::Intent in = g_brain.Decide(w, b.creature);
    const b2::Memory* m = g_brain.MemoryOf(b.creature);
    const bool inBase = b2::InBase(w, *me, g_tuning);
    if (b2::IntentIdle(in, me->alive, inBase)) {
      ++g_c.idleLines;
      if (g_c.idleLines <= 50)
        Log("BOTS2 IDLE: bot %08X %s (player %d, team %d) at (%.1f, %.1f), %.0f from its base, hp %.0f%%: %s (%s), "
            "state %s (t %.1f)",
            b.creature, agents::bot2io::Noun(b.creature), b.index, b.team, static_cast<double>(me->pos.x),
            static_cast<double>(me->pos.y),
            w.base[b.team == 2 ? 2 : 1].ok ? static_cast<double>(b2::Dist(me->pos, w.base[b.team == 2 ? 2 : 1].spawn))
                                           : -1.0,
            static_cast<double>(me->Frac() * 100.f), b2::ActName(in.act), in.why,
            m ? b2::StateName(m->state) : "?", w.now);
    }
    Exec& e = g_exec[b.creature];
    e.lastAct = in.act;
    e.lastWhy = in.why;
    if (m && m->state != e.logged) {
      if (LogBudget())
        Log("bots2: bot %08X %s (player %d, team %d) %s -> %s: %s %s (t %.1f, hp %.0f%%, gold %.0f, at (%.1f, %.1f))",
            b.creature, agents::bot2io::Noun(b.creature), b.index, b.team,
            e.logged == b2::State::kCount ? "start" : b2::StateName(e.logged), b2::StateName(m->state),
            b2::ActName(in.act), in.why, w.now, static_cast<double>(me->Frac() * 100.f),
            static_cast<double>(me->gold), static_cast<double>(me->pos.x), static_cast<double>(me->pos.y));
      e.logged = m->state;
    }
    if (me->alive) Execute(w, *me, in, nowMs);
    else e.mode = 0;  // a respawn starts afresh
  }
}

}  // namespace

void Configure() {
  char ai[32], ai1[32], ai2[32];
  // the default since 2026-10-29 (the user's decision after BOTS2c's head-to-head rounds): the hybrid; "classic" (the
  // game's own bot AI with the fix-up layers) and "v2" (the second brain alone) stay selectable
  HostOptionString("bot_ai", "hybrid", ai, sizeof ai);
  HostOptionString("bot_ai_team1", "", ai1, sizeof ai1);  // BOTS2c: a brain per team (head to head)
  HostOptionString("bot_ai_team2", "", ai2, sizeof ai2);
  g_set.brains = b2::ChooseBrains(ai, ai1, ai2);
  g_set.v2 = g_set.brains.Any();
  g_set.census = HostOptionInt("bot2_census", 1) != 0;
  g_set.logLimit = HostOptionInt("bot2_log_limit", g_set.logLimit);
  char tune[1024];
  HostOptionString("bot2_tune", "", tune, sizeof tune);
  std::string bad;
  const int set = b2::ApplyOverrides(&g_tuning, tune, &bad);
  if (g_tuning.thinkMs < 50) g_tuning.thinkMs = 50;
  g_brain.SetTuning(g_tuning);
  g_brain.SetTeams(g_set.brains.v2[1], g_set.brains.v2[2]);
  Log("bots2: bot_ai %s (team 1 %s, team 2 %s: %s), census %d, log limit %d; bot2_tune: %d value(s) set%s%s",
      g_set.brains.Label(), b2::BrainKindName(g_set.brains.kind[1]), b2::BrainKindName(g_set.brains.kind[2]),
      g_set.brains.Mixed()   ? "head to head"
      : g_set.brains.Hybrid(1) ? "the hybrid: the classic brain's micro, the second brain's team plan"
      : g_set.v2               ? "the second brain drives the bots"
                               : "the classic brain; census only",
      g_set.census ? 1 : 0, g_set.logLimit, set, bad.empty() ? "" : ", not understood: ", bad.c_str());
  if (g_set.v2) Log("bots2: tuning %s", b2::Describe(g_tuning).c_str());
}

bool Drives() { return g_set.v2; }
bool DrivesTeam(int team) { return (team == 1 || team == 2) && g_set.brains.v2[team]; }
bool DrivesBot(std::uint32_t creature) {
  const bots::Bot* b = BotOf(creature);
  if (!b || !DrivesTeam(b->team)) return false;
  if (!g_set.brains.Hybrid(b->team)) return true;
  const auto it = g_hybrid.find(creature);  // round 7: a hybrid bot only while v2 has taken it over
  return it != g_hybrid.end() && it->second.driven;
}
const char* BrainOfTeam(int team) {
  return b2::BrainKindName(team == 1 || team == 2 ? g_set.brains.kind[team] : b2::kBrainClassic);
}
const char* BrainLabel() { return g_set.brains.Label(); }

bool Due(unsigned long long nowMs) {
  if (bots::Players().empty()) return false;
  return (g_set.v2 && nowMs >= g_nextThinkMs) || (g_set.census && nowMs >= g_nextCensusMs);
}

void Frame(void* sim, const std::vector<ObjView>& objects, unsigned long long nowMs) {
  (void)sim;
  ++g_c.frames;
  const bool think = g_set.v2 && nowMs >= g_nextThinkMs;
  const bool census = g_set.census && nowMs >= g_nextCensusMs;
  if (!think && !census) return;
  const double now = match::MatchSeconds();
  const b2::World w = BuildWorld(objects, now);
  TrackKills(w);  // BOTS2c
  if (census) {
    g_nextCensusMs = nowMs + 1000;
    SampleCensus(w, now);
  }
  if (think && !match::Ended()) {
    g_nextThinkMs = nowMs + static_cast<unsigned long long>(g_tuning.thinkMs);
    Think(w, nowMs);
  }
}

void OnDamage(std::uint32_t victim, std::uint32_t attacker, bool attackerIsShaper, float dealt) {
  if (!(dealt > 0.f)) return;
  const double now = match::MatchSeconds();
  g_census.NoteHit(attacker, now);
  if (g_set.v2) g_brain.NoteDamage(victim, attacker, attackerIsShaper, now);
  // BOTS2c: the last shaper's team to hit the parasite or a worker (their deaths are named by it: TrackKills)
  if (attackerIsShaper && attacker) {
    const auto t = g_shaperTeam.find(attacker);
    const int team = t != g_shaperTeam.end() ? t->second : 0;
    if (victim && victim == g_parasiteId) g_parasiteHitTeam = team;
    else if (g_lastWorkers.count(victim)) g_workerHitTeam[victim] = team;
  }
}

bool FormatExtraStats(char* buf, std::size_t size) {
  if (bots::Players().empty()) return false;
  const b2::BrainStats& s = g_brain.Stats();
  char spells[320] = "";
  int n = 0;
  for (int i = 1; i < static_cast<int>(b2::Spell::kCount) && n >= 0 && n < static_cast<int>(sizeof spells); ++i)
    if (s.spellsStarted[i])
      n += std::snprintf(spells + n, sizeof spells - n, "%s%s %u", n ? ", " : "", b2::SpellName(static_cast<b2::Spell>(i)),
                         s.spellsStarted[i]);
  const int ks = static_cast<int>(b2::ExtraKind::kSpell), kp = static_cast<int>(b2::ExtraKind::kPotion),
            kw = static_cast<int>(b2::ExtraKind::kWard);
  // round 8: why no parasite attempt started (the plan thinks by reason)
  char noWhy[256] = "";
  int m = 0;
  for (int i = 1; i < static_cast<int>(b2::ParasiteNo::kCount) && m >= 0 && m < static_cast<int>(sizeof noWhy); ++i)
    m += std::snprintf(noWhy + m, sizeof noWhy - m, "%s%s %u", i > 1 ? ", " : "",
                       b2::ParasiteNoName(static_cast<b2::ParasiteNo>(i)), s.parasiteNo[i]);
  std::snprintf(buf, size,
                "brain %s; spells asked %u, requested %u, started %u (%s); potions asked %u, used %u of %u; wards asked "
                "%u, requested %u, started %u, detour thinks %u; wells: threats %u, tasks retake %u take %u raid %u, "
                "workers killed by t1 %u t2 %u; parasite: attempts %u, groups %u, down %u, given up %u, kills by t1 %u "
                "t2 %u, no attempt (plan thinks) %s; laning: trades %u, kill fights %u (low target %u, gank %u, from a "
                "trade %u), back-offs %u, "
                "stays at the Binding %u, Hunter stays in the jungle %u, first-wave waits in base %u; hybrid: v2 "
                "drove %u of %u thinks, takeovers for a task %u, rescues %u, releases %u, task back-offs %u; guardian: "
                "phases %u, engages %u with %u bots in all, back-offs %u; botsflip: threats remembered %u, holds %u, "
                "route switches %u, walks asked again %u, re-aims held %u, unstuck hits %u",
                BrainLabel(), s.extras[ks], g_c.spells, g_c.spellsStarted, n ? spells : "none",
                s.extras[kp], g_c.potionsUsed, g_c.potions, s.extras[kw], g_c.wards, g_c.wardsStarted, s.wardDetours,
                s.wellThreats, s.wellTasks[1], s.wellTasks[2], s.wellTasks[3], g_c.workerKills[1], g_c.workerKills[2],
                s.parasiteStarts, s.parasiteGroups, s.parasiteDowns, s.parasiteGiveUps, g_c.parasiteKills[1],
                g_c.parasiteKills[2], noWhy, s.trades, s.killLow + s.killGanks + s.tradeToKill, s.killLow, s.killGanks,
                s.tradeToKill, s.backOffs, s.laneStays, s.jungleStays, s.firstWaveWaits, g_hc.thinksV2,
                g_hc.thinksV2 + g_hc.thinksClassic, g_hc.takeoversTask, g_hc.takeoversRescue, g_hc.releases,
                g_hc.backoffs, s.guardPhases, s.guardEngages, s.guardEngageBots, s.guardBackOffs, s.fleeMemories,
                s.fleeHolds, s.routeSwitches, g_c.walkStuck, g_c.reaimsHeld, s.unstuckHits);
  return true;
}

bool FormatStats(char* buf, std::size_t size) {
  if (bots::Players().empty()) return false;
  const b2::BrainStats& s = g_brain.Stats();
  char states[256] = "", acts[128] = "";
  int n = 0;
  for (int i = 0; i < static_cast<int>(b2::State::kCount) && n >= 0 && n < static_cast<int>(sizeof states); ++i)
    n += std::snprintf(states + n, sizeof states - n, "%s%s %u", i ? ", " : "", b2::StateName(static_cast<b2::State>(i)),
                       s.byState[i]);
  n = 0;
  for (int i = 0; i < static_cast<int>(b2::Act::kCount) && n >= 0 && n < static_cast<int>(sizeof acts); ++i)
    n += std::snprintf(acts + n, sizeof acts - n, "%s%s %u", i ? ", " : "", b2::ActName(static_cast<b2::Act>(i)),
                       s.byAct[i]);
  const b2::TeamPlan& p1 = g_brain.Plan(1);
  const b2::TeamPlan& p2 = g_brain.Plan(2);
  std::snprintf(buf, size,
                "brain %s; thinks %u; states (%s); intents (%s); idle violations %u; engages %u, retreats %u, recalls "
                "%u, push-lane changes %u, base threats %u, groups formed %u, objectives %u, advantage pushes %u, "
                "tower escapes %u; plan t1 %s lane %d obj %08X group %d/%d%s%s, t2 %s lane %d obj %08X group %d/%d%s%s; "
                "requests: moves %u (restarts %u), stops %u, casts %u (started %u), basics %u (started %u), walk-ups "
                "%u, refused %u, targets died first %u, recalls %u (started %u), held %u; census: %.0f idle s of %.0f s "
                "away from base, %d deaths",
                BrainLabel(), g_c.thinks, states, acts, s.idleViolations, s.engages, s.retreats,
                s.recalls, s.pushLaneChanges, s.threats, s.groupedOn, s.objectives, s.advantages, s.towerEscapes,
                p1.push ? "push" : "laning", p1.pushLane, p1.objective, p1.groupCount, p1.groupNeeded,
                p1.grouped ? " grouped" : "", p1.threatened ? " threatened" : "", p2.push ? "push" : "laning",
                p2.pushLane, p2.objective, p2.groupCount, p2.groupNeeded, p2.grouped ? " grouped" : "",
                p2.threatened ? " threatened" : "", g_c.moves, g_c.moveRestarts, g_c.stops, g_c.casts,
                g_c.castsStarted, g_c.basics, g_c.basicsStarted, g_c.pursuits, g_c.refused, g_c.targetsDied,
                g_c.recalls, g_c.recallsStarted, g_c.heldSkips, g_census.IdleSeconds(), g_census.AwaySeconds(),
                g_census.Deaths());
  return true;
}

}  // namespace bots2
