// D11b: the shapers' banter, kill and assist voice lines and the presence lines on the match host; see banter.h and
// docs/GMS_PROTOCOL.md *The shapers' banter and kill lines (D11b)*.
#include "banter.h"

#include <windows.h>

#include <cstdio>
#include <cstring>
#include <map>
#include <string>
#include <vector>

#include "commands.h"
#include "game.h"
#include "log.h"
#include "match.h"

using namespace game;
namespace gb = gms::banter;

namespace banter {
namespace {

Options g_opt;
bool g_installed;
gb::Tuning g_tuning;
bool g_tuningRead;
gb::Book g_book;
gb::Rng g_rng;
std::map<std::uint32_t, gb::Table> g_tables;  // by the creature's noun asset id
std::map<std::uint32_t, int> g_tableNotes;   // noun id -> times its table could not be read (logged once)
unsigned g_logLines;

struct Stats {
  unsigned sent[static_cast<int>(gb::Line::kCount)];
  unsigned queuedNobody, failed, steps, sequences, exceptions, tables, tablesEmpty, kills;
} g_st;

DWORD SehNote(EXCEPTION_POINTERS* ep, const char* what) {
  ++g_st.exceptions;
  Log("EXCEPTION in banter (%s): code %08lX at %08lX", what, ep->ExceptionRecord->ExceptionCode,
      reinterpret_cast<DWORD>(ep->ExceptionRecord->ExceptionAddress));
  return EXCEPTION_EXECUTE_HANDLER;
}

bool LogBudget() {
  ++g_logLines;
  return g_logLines <= static_cast<unsigned>(g_opt.logLimit) || g_logLines % 50 == 0;
}

unsigned long long SehNowMs(void* sim) {
  __try {
    return GetTimeMs(sim);
  } __except (SehNote(GetExceptionInformation(), "GetTimeMs")) {
    return 0;
  }
}

// ---- Tuning.BanterTuning ---------------------------------------------------------------------------------------

bool SehReadTuning(gb::Tuning* t) {
  __try {
    void* a = *reinterpret_cast<void**>(kBanterTuningRef);
    char* d = a ? Field<char*>(a, asset::kData) : nullptr;
    if (!d) return false;
    t->updatePeriod = Field<float>(d, 0x00);
    t->minimumBanterCooldown = Field<float>(d, 0x04);
    t->friendlyStart = Field<float>(d, 0x08);
    t->friendlyEnd = Field<float>(d, 0x0C);
    t->friendlyRange = Field<float>(d, 0x10);
    t->friendlyCooldown = Field<float>(d, 0x14);
    t->enemyStart = Field<float>(d, 0x18);
    t->enemyEnd = Field<float>(d, 0x1C);
    t->enemyRange = Field<float>(d, 0x20);
    t->enemyCooldown = Field<float>(d, 0x24);
    t->killCooldown = Field<float>(d, 0x28);
    t->maxAssistLinesPerKill = Field<int>(d, 0x2C);
    t->minimumEmoteCooldown = Field<float>(d, 0x30);
    t->sequentialEmoteCooldown = Field<float>(d, 0x34);
    t->numEmoteLines = Field<int>(d, 0x38);
    return true;
  } __except (SehNote(GetExceptionInformation(), "Tuning.BanterTuning")) {
    return false;
  }
}

const gb::Tuning& TuningNow() {
  if (g_tuningRead) return g_tuning;
  g_tuningRead = true;
  gb::Tuning t;
  const bool read = SehReadTuning(&t);
  if (read && gb::Plausible(t)) g_tuning = t;
  const gb::Tuning& u = g_tuning;
  Log("banter: Tuning.BanterTuning %s: update %.1f s, minimum %.1f s; friendly %.0f-%.0f s within %.1f, cooldown %.0f s; "
      "enemy %.0f-%.0f s within %.1f, cooldown %.0f s; kill %.1f s, assist lines per kill %d; emotes minimum %.1f s, "
      "sequence %.1f s, %d lines",
      read ? (gb::Plausible(t) ? "read" : "IMPLAUSIBLE, the data's values kept") : "UNREADABLE, the data's values kept",
      u.updatePeriod, u.minimumBanterCooldown, u.friendlyStart, u.friendlyEnd, u.friendlyRange, u.friendlyCooldown,
      u.enemyStart, u.enemyEnd, u.enemyRange, u.enemyCooldown, u.killCooldown, u.maxAssistLinesPerKill,
      u.minimumEmoteCooldown, u.sequentialEmoteCooldown, u.numEmoteLines);
  return g_tuning;
}

// ---- a noun's CharacterBanterData ------------------------------------------------------------------------------

struct RawTable {
  bool hasData = false;  // the noun names a CharacterBanterData
  std::uint32_t banterId = 0;
  int n = 0;
  struct T {
    int friendly, enemy, kill, nn;
    std::uint32_t nouns[16];
  } t[48];
  int ns = 0;
  int seq[16];
};

// 1 read (or the noun has none), 0 not readable yet (its data isn't loaded), -1 malformed or an exception.
int SehReadTable(void* nounAsset, RawTable* r) {
  __try {
    char* nd = nounAsset ? Field<char*>(nounAsset, noun::kData) : nullptr;
    if (!nd) return 0;
    void* ba = Field<void*>(nd, banterdata::kNounBanterData);
    if (!ba) return 1;
    r->hasData = true;
    r->banterId = Field<std::uint32_t>(ba, asset::kId);
    char* bd = Field<char*>(ba, asset::kData);
    if (!bd) return 0;
    char* list = Field<char*>(bd, banterdata::kTargets);
    const int count = Field<int>(bd, banterdata::kTargets + 4);
    if (count < 0 || count > 48 || (count && !list)) return -1;
    for (int i = 0; i < count; ++i) {
      char* e = list + i * banterdata::kTargetSize;
      RawTable::T& t = r->t[r->n];
      char* nl = Field<char*>(e, banterdata::kTargetNouns);
      const int nc = Field<int>(e, banterdata::kTargetNouns + 4);
      if (nc < 0 || nc > 16 || (nc && !nl)) return -1;
      t.nn = 0;
      for (int j = 0; j < nc; ++j) {
        void* na = Field<void*>(nl, static_cast<std::uintptr_t>(j) * 4);
        if (na) t.nouns[t.nn++] = Field<std::uint32_t>(na, asset::kId);
      }
      t.friendly = Field<int>(e, banterdata::kTargetFriendly);
      t.enemy = Field<int>(e, banterdata::kTargetEnemy);
      t.kill = Field<int>(e, banterdata::kTargetKill);
      ++r->n;
    }
    char* sl = Field<char*>(bd, banterdata::kSequentialEmotes);
    const int sc = Field<int>(bd, banterdata::kSequentialEmotes + 4);
    if (sc < 0 || sc > 16 || (sc && !sl)) return -1;
    for (int i = 0; i < sc; ++i) r->seq[r->ns++] = Field<int>(sl, static_cast<std::uintptr_t>(i) * 4);
    return 1;
  } __except (SehNote(GetExceptionInformation(), "CharacterBanterData")) {
    return -1;
  }
}

// The table of a noun (cached), or null while it can't be read.
const gb::Table* TableOf(std::uint32_t nounId, void* nounAsset, const char* who) {
  auto it = g_tables.find(nounId);
  if (it != g_tables.end()) return &it->second;
  static RawTable raw;  // 3 KB: kept off the stack
  raw = RawTable();
  const int rc = SehReadTable(nounAsset, &raw);
  if (rc != 1) {
    if (g_tableNotes[nounId]++ == 0)
      Log("banter: %s: noun %08X's CharacterBanterData %s", who, nounId,
          rc == 0 ? "not loaded (no targeted lines until it is)" : "MALFORMED (no targeted lines)");
    return nullptr;
  }
  gb::Table t;
  std::string targets;
  for (int i = 0; i < raw.n; ++i) {
    gb::Target x;
    x.nouns.assign(raw.t[i].nouns, raw.t[i].nouns + raw.t[i].nn);
    x.friendly = raw.t[i].friendly;
    x.enemy = raw.t[i].enemy;
    x.kill = raw.t[i].kill;
    char b[64];
    std::snprintf(b, sizeof b, " [%d noun(s) f%d e%d k%d]", raw.t[i].nn, x.friendly, x.enemy, x.kill);
    targets += b;
    t.targets.push_back(x);
  }
  t.sequentialEmotes.assign(raw.seq, raw.seq + raw.ns);
  ++g_st.tables;
  if (!raw.hasData) ++g_st.tablesEmpty;
  Log("banter: %s: noun %08X: %s %08X, %d target(s)%s, %d sequential emote(s)", who, nounId,
      raw.hasData ? "CharacterBanterData" : "no CharacterBanterData", raw.banterId, raw.n, targets.c_str(), raw.ns);
  return &(g_tables[nounId] = t);
}

// ---- the players' shapers --------------------------------------------------------------------------------------

struct RawShaper {
  int index = -1, team = 0;
  std::uint32_t creature = 0, noun = 0;
  void* nounAsset = nullptr;
  char token[64] = {};
  bool alive = false;
  float x = 0.f, y = 0.f;
  unsigned seenBy = 0;
};

bool SehReadShaper(void* sim, int i, RawShaper* r) {
  __try {
    void* p = GetPlayerByIndex(sim, static_cast<unsigned>(i));
    if (!p) return false;
    r->index = i;
    r->team = Field<unsigned char>(p, player::kTeam);
    r->creature = Field<std::uint32_t>(p, player::kCurrentCreature);
    void* o = r->creature ? GetObjectById(r->creature) : nullptr;
    if (!o) return false;
    void* na = Field<void*>(o, gobj::kNoun);
    r->nounAsset = na;
    r->noun = na ? Field<std::uint32_t>(na, noun::kId) : 0;
    r->x = Field<float>(o, gobj::kPosition);
    r->y = Field<float>(o, gobj::kPosition + 4);
    char* cd = Field<char*>(o, combatant::kOfObject);
    r->alive = cd && Field<float>(cd, combatant::kHitPoints) > 0.f;
    r->seenBy = Field<unsigned char>(o, kObjectVisibleToTeam);
    // ~objectnounname~ as ClientFeedback_GetObjectTokenName (0x006669C0) resolves it: the noun's
    // clientFeedbackTokenName, else the class's (a player's creature: +0x74, its PlayerClass; else the noun's
    // playerClassData)
    char* nd = na ? Field<char*>(na, noun::kData) : nullptr;
    const char* tok = nd ? Field<const char*>(nd, banterdata::kNounFeedbackToken) : nullptr;
    if (!tok) {
      void* ca = Field<unsigned char>(o, gobj::kPlayerControlled) ? Field<void*>(o, banterdata::kObjectPlayerClass) : nullptr;
      if (!ca && nd) ca = Field<void*>(nd, kNounPlayerClass);
      char* pc = ca ? Field<char*>(ca, asset::kData) : nullptr;
      tok = pc ? Field<const char*>(pc, banterdata::kPlayerClassFeedbackToken) : nullptr;
    }
    if (tok) {
      int k = 0;
      for (; k < 63 && tok[k]; ++k) r->token[k] = tok[k];
      r->token[k] = 0;
    }
    return true;
  } __except (SehNote(GetExceptionInformation(), "player shaper")) {
    return false;
  }
}

std::vector<gb::Shaper> Shapers(void* sim) {
  std::vector<gb::Shaper> out;
  for (int i = 0; i < gb::kMaxPlayers; ++i) {
    RawShaper r;
    if (!SehReadShaper(sim, i, &r)) continue;
    gb::Shaper s;
    s.index = r.index;
    s.team = r.team;
    s.creature = r.creature;
    s.noun = r.noun;
    s.token = r.token;
    s.alive = r.alive;
    s.x = r.x;
    s.y = r.y;
    s.seenBy = r.seenBy;
    char who[48];
    std::snprintf(who, sizeof who, "player %d (%s)", i, r.token[0] ? r.token : "?");
    s.table = TableOf(r.noun, r.nounAsset, who);
    out.push_back(s);
  }
  return out;
}

const gb::Shaper* Find(const std::vector<gb::Shaper>& v, int index) {
  for (const gb::Shaper& s : v)
    if (s.index == index) return &s;
  return nullptr;
}

std::string Who(const std::vector<gb::Shaper>& v, int index, std::uint32_t object) {
  char b[96];
  const gb::Shaper* s = Find(v, index);
  if (index < 0)
    std::snprintf(b, sizeof b, "%08X", object);
  else
    std::snprintf(b, sizeof b, "player %d's %08X (%s)", index, object, s && !s->token.empty() ? s->token.c_str() : "?");
  return b;
}

void Send(void* sim, const gb::Say& s, const std::vector<gb::Shaper>& shapers, const char* why, double matchS) {
  const int li = static_cast<int>(s.line);
  const unsigned mask = cmds::SendFeedback(sim, gb::DefName(s.line), s.object, s.teamBits, s.variant, s.targetObject,
                                           s.excludePlayers);
  if (!mask)
    ++g_st.failed;
  else if (mask == 0x10000u)
    ++g_st.queuedNobody;
  else
    ++g_st.sent[li];
  if (!LogBudget()) return;
  const bool presence = s.line == gb::Line::kPlayerLeft || s.line == gb::Line::kPlayerReconnected;
  char without[32] = "";
  if (s.excludePlayers) std::snprintf(without, sizeof without, " without players 0x%04x", s.excludePlayers);
  if (presence)
    Log("banter: %s at match %.2f s: ClientFeedbackEvent %s, uintVal %u (player %d), to teams 0x%x%s -> %s (players "
        "0x%04x)",
        why, matchS, gb::LineName(s.line), s.variant, s.target, s.teamBits, without,
        !mask ? "FAILED" : mask == 0x10000u ? "nobody" : "queued", mask & 0xFFFFu);
  else
    Log("banter: %s at match %.2f s: %s says ClientFeedbackEvent %s about %s, variant %02u, to teams 0x%x -> %s "
        "(players 0x%04x)",
        why, matchS, Who(shapers, s.speaker, s.object).c_str(), gb::LineName(s.line),
        Who(shapers, s.target, s.targetObject).c_str(), s.variant, s.teamBits,
        !mask ? "FAILED" : mask == 0x10000u ? "nobody" : "queued", mask & 0xFFFFu);
}

std::string SkipDiff(const gb::Skips& a, const gb::Skips& b) {
  std::string s;
  auto add = [&s](unsigned d, const char* what) {
    if (d) s += std::string(s.empty() ? "" : ", ") + what + " " + std::to_string(d);
  };
  add(b.dead - a.dead, "dead");
  add(b.unvoiced - a.unvoiced, "unvoiced");
  add(b.cooldown - a.cooldown, "cooldown");
  add(b.multiKill - a.multiKill, "multi-kill (its own line follows the announcer)");
  add(b.missing - a.missing, "no voice-bank event");
  add(b.unseen - a.unseen, "unseen");
  add(b.assistCap - a.assistCap, "assist lines per kill");
  return s;
}

gb::Switches SwitchesNow() {
  gb::Switches sw;
  sw.kill = g_opt.kill;
  sw.assist = g_opt.assist;
  sw.friendly = g_opt.friendly;
  sw.enemy = g_opt.enemy;
  sw.window = g_opt.window;
  sw.multiKillReplacesKill = g_opt.multiKillReplacesKill;
  return sw;
}

bool Running() { return g_installed && g_opt.enabled && match::Started() && !match::Ended(); }

}  // namespace

void Install(const Options& options) {
  g_opt = options;
  g_installed = true;
  g_rng = gb::Rng(options.seed);
  Log("banter (D11b): %s; kill %d, assist %d, friendly %d, enemy %d (window %d), multi-kill skip %d, presence %d, emote "
      "sequence %d, seed %u",
      g_opt.enabled ? "on" : "OFF ([host] banter = 0)", g_opt.kill, g_opt.assist, g_opt.friendly, g_opt.enemy,
      g_opt.window, g_opt.multiKillReplacesKill, g_opt.presence, g_opt.emoteSequence, g_opt.seed);
}

bool Enabled() { return g_installed && g_opt.enabled; }

void Tick(void* sim, double matchSeconds) {
  if (!Running() || !(g_opt.friendly || g_opt.enemy)) return;
  const gb::Tuning& t = TuningNow();
  if (!g_book.MeetDue(matchSeconds, t)) return;
  if (g_opt.window && !(matchSeconds >= t.friendlyStart && matchSeconds <= t.friendlyEnd) &&
      !(matchSeconds >= t.enemyStart && matchSeconds <= t.enemyEnd))
    return;
  ++g_st.steps;
  const std::vector<gb::Shaper> shapers = Shapers(sim);
  const std::vector<gb::Say> says = g_book.MeetStep(shapers, matchSeconds, SehNowMs(sim), t, g_rng, SwitchesNow());
  for (const gb::Say& s : says) {
    const bool friendly = s.line == gb::Line::kFriendlyInitiate || s.line == gb::Line::kFriendlyResponse;
    const gb::Shaper* a = Find(shapers, s.speaker);
    const gb::Shaper* b = Find(shapers, s.target);
    char why[64];
    std::snprintf(why, sizeof why, "%s meeting (%.1f apart)", friendly ? "friendly" : "enemy",
                  a && b ? gb::Distance(*a, *b) : -1.f);
    Send(sim, s, shapers, why, matchSeconds);
  }
}

void OnPlayerKill(void* sim, int victim, int killer, const std::vector<gb::Assist>& assisters, unsigned multiKill) {
  if (!Running() || killer < 0 || !(g_opt.kill || g_opt.assist)) return;
  ++g_st.kills;
  const gb::Tuning& t = TuningNow();
  const std::vector<gb::Shaper> shapers = Shapers(sim);
  const gb::Skips before = g_book.skips;
  const std::vector<gb::Say> says =
      g_book.OnPlayerKill(shapers, killer, victim, assisters, multiKill, SehNowMs(sim), t, SwitchesNow());
  const double m = match::MatchSeconds();
  for (const gb::Say& s : says) Send(sim, s, shapers, s.line == gb::Line::kAssist ? "assist" : "kill", m);
  const std::string skipped = SkipDiff(before, g_book.skips);
  if (!skipped.empty() && LogBudget())
    Log("banter: kill of player %d by player %d (multi-kill %u, %zu assister(s)) at match %.2f s: %zu line(s); not "
        "said: %s",
        victim, killer, multiKill, assisters.size(), m, says.size(), skipped.c_str());
}

void OnPlayerLeft(void* sim, int index) {
  if (!Running() || !g_opt.presence || index < 0 || index >= gb::kMaxPlayers) return;
  Send(sim, gb::Book::PlayerLeft(index, 0), std::vector<gb::Shaper>(), "player left", match::MatchSeconds());
}

void OnPlayerReconnected(void* sim, int index) {
  if (!Running() || !g_opt.presence || index < 0 || index >= gb::kMaxPlayers) return;
  Send(sim, gb::Book::PlayerReconnected(index, 0), std::vector<gb::Shaper>(), "player reconnected",
       match::MatchSeconds());
}

unsigned EmoteVariant(void* sim, unsigned index, std::uint32_t creature, const char* defName, unsigned randomVariant) {
  if (!g_installed || !g_opt.enabled || !g_opt.emoteSequence) return randomVariant;
  const int emote = gb::EmoteOfDef(defName);
  if (emote < 0 || index >= static_cast<unsigned>(gb::kMaxPlayers)) return randomVariant;
  RawShaper r;
  bool sequential = false;
  if (SehReadShaper(sim, static_cast<int>(index), &r) && r.creature == creature) {
    char who[48];
    std::snprintf(who, sizeof who, "player %u (%s)", index, r.token[0] ? r.token : "?");
    if (const gb::Table* t = TableOf(r.noun, r.nounAsset, who))
      for (int e : t->sequentialEmotes) sequential = sequential || e == emote;
  }
  const unsigned v = g_book.EmoteVariant(static_cast<int>(index), emote, sequential, randomVariant, SehNowMs(sim),
                                         TuningNow());
  if (sequential) {
    ++g_st.sequences;
    if (LogBudget())
      Log("banter: player %u's %s is a sequential emote: variant %02u (the random one was %02u)", index, defName, v,
          randomVariant);
  }
  return v;
}

void FormatStats(char* buf, std::size_t size) {
  auto n = [](gb::Line l) { return g_st.sent[static_cast<int>(l)]; };
  const gb::Skips& k = g_book.skips;
  std::snprintf(buf, size,
                "%s; kills seen %u: kill lines %u (targeted %u), assist %u; meeting steps %u: friendly %u+%u, enemy "
                "%u+%u (initiate+response); left %u, reconnected %u; sequential emotes %u; not said: dead %u, unvoiced "
                "%u, cooldown %u, multi-kill %u, no event %u, unseen %u, assist cap %u; to nobody %u, failed %u; tables "
                "%u (%u without data), exceptions %u",
                Enabled() ? "on" : "off", g_st.kills, n(gb::Line::kKillGeneric) + n(gb::Line::kKillWithTarget),
                n(gb::Line::kKillWithTarget), n(gb::Line::kAssist), g_st.steps, n(gb::Line::kFriendlyInitiate),
                n(gb::Line::kFriendlyResponse), n(gb::Line::kEnemyInitiate), n(gb::Line::kEnemyResponse),
                n(gb::Line::kPlayerLeft), n(gb::Line::kPlayerReconnected), g_st.sequences, k.dead, k.unvoiced,
                k.cooldown, k.multiKill, k.missing, k.unseen, k.assistCap, g_st.queuedNobody, g_st.failed, g_st.tables,
                g_st.tablesEmpty, g_st.exceptions);
}

}  // namespace banter
