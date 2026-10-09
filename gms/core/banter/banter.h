// gms/core/banter (D11b): the engine-free rules of the shapers' banter, kill and assist voice lines and the two
// presence lines (docs/GMS_PROTOCOL.md *The shapers' banter and kill lines (D11b)*). Header-only, no game addresses,
// like core/damage/kills.h: gms/engine/banter.cpp reads the players, their creatures and the data tables and queues the
// ClientFeedbackEvents; the choices are made here, where gms_tests pins them down.
//
// What the client build keeps and what is gone [code, data]:
//   - The ClientFeedbackDefs (KillBanterGeneric, KillBanterWithTarget, AssistBanter, Friendly/EnemyBanterInitiate and
//     -Response, PlayerLeftGame, PlayerReconnectedGame) are named by no exe string, no script and no other asset, and
//     their asset ids appear nowhere in the exe: the stripped server shell sent them.
//   - Tuning.BanterTuning (the values below) has an asset ref (0x019D03D4, set by the static initializer 0x0110E590)
//     that nothing else reads, and nothing reads a noun's hasBanterComponent (+0x285): the banter component and its
//     update were server-only and are gone.
//   - The client plays what it gets: one character line at a time per client (the character channel's queue, by
//     priority, then in arrival order), a queued line drops when its def's lifetime runs out or, for
//     doNotStartIfSourceDies / ...TargetDies, when its speaker or target is dead or unknown.
//   - Multikill_N_Player is NOT the server's: the client's own CombatEvent_KillingBlow.lua shows Multikill_N_Announcer
//     about the killer, and ClientFeedback_StartFinishedEvents (0x006698E0) raises its onFinishedFeedbackDef
//     (Multikill_N_Player) with the same object when the announcer's line ends.
//
// The rules [assumed where marked; every number is BanterTuning's or CharacterBanterData's]:
//   - kill: the credited killer of an enemy player's creature (D1h's kill book) says KillBanterWithTarget when its
//     CharacterBanterData lists the victim's noun with killCount > 0, else KillBanterGeneric; not while dead, not within
//     killBanterCooldown (5 s) of its last kill or assist line, not within minimumBanterCooldown (5 s) of any banter, and
//     not on a multi-kill (2+), whose own line (Multikill_N_Player) follows the client's announcer [assumed];
//   - assist: at most maxNumberAssistVOPerKill (1) assisters say AssistBanter, the one with the latest assist credit
//     first [assumed], under the same cooldowns;
//   - meeting (every updatePeriod, 1 s): two living shapers within friendlyBanterRange / enemyBanterRange (20) whose
//     speaker's CharacterBanterData lists the other's noun with friendlyCount / enemyCount > 0, while the match time is
//     within [start, end] (25-115 s) [assumed: match seconds], neither within its friendly / enemy cooldown (100 s) or
//     the minimum banter cooldown [assumed: per shaper]; two enemies must see each other [assumed]; the closest pair
//     first, one pair per kind and step [assumed]; the speaker says ..Initiate_NN (NN drawn from 1..count) about the
//     other, the other ..Response_NN about the speaker (the same NN: the voice banks pair them so), both at once: the
//     client's queue plays the response after the initiate (both priority 500, lifetime 0) [assumed: the original's
//     timing is unknown];
//   - who hears a shaper's line: the teams that see the speaker (mVisibleToTeam) and its own [assumed: the sound is
//     attached to the speaker and the client mutes fogged emitters itself; a muted line would still hold the client's
//     one character channel];
//   - presence: PlayerLeftGame when a human's connection ends during the match, PlayerReconnectedGame when a rejoined
//     human is in the game again, uintVal = its gameplay index (~playerNameFromUintData~), to everyone [assumed];
//   - emotes: a voice line listed in the speaker's CharacterBanterData.sequentialEmote (only Desecrator's joke in build
//     854) plays its numEmoteLines (3) variants in order while repeated within sequentialEmoteCooldown (10 s), else
//     from 1; a repeat within minimumEmoteCooldown (5 s, which the client drops) doesn't count [assumed]; every other
//     line keeps D7b's random variant.
//   - Lines whose sound is in no voice bank are not sent (the client's Wwise monitor turns an unknown event into an
//     assert, SPWwiseSystem.cpp 274 [inferred]): seven classes have no voice at all, and four of the data's pair lines
//     have no event (LineMissing).
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <string>
#include <utility>
#include <vector>

namespace gms::banter {

constexpr int kMaxPlayers = 10;  // gameplay indices 0-9

enum class Line : int {
  kKillGeneric = 0,
  kKillWithTarget,
  kAssist,
  kFriendlyInitiate,
  kFriendlyResponse,
  kEnemyInitiate,
  kEnemyResponse,
  kPlayerLeft,
  kPlayerReconnected,
  kCount
};

// The ClientFeedbackDef's name (AssetData Catalog) without and with its type suffix.
inline const char* LineName(Line l) {
  switch (l) {
    case Line::kKillGeneric: return "KillBanterGeneric";
    case Line::kKillWithTarget: return "KillBanterWithTarget";
    case Line::kAssist: return "AssistBanter";
    case Line::kFriendlyInitiate: return "FriendlyBanterInitiate";
    case Line::kFriendlyResponse: return "FriendlyBanterResponse";
    case Line::kEnemyInitiate: return "EnemyBanterInitiate";
    case Line::kEnemyResponse: return "EnemyBanterResponse";
    case Line::kPlayerLeft: return "PlayerLeftGame";
    case Line::kPlayerReconnected: return "PlayerReconnectedGame";
    default: return "?";
  }
}
inline const char* DefName(Line l) {
  switch (l) {
    case Line::kKillGeneric: return "KillBanterGeneric.ClientFeedbackDef";
    case Line::kKillWithTarget: return "KillBanterWithTarget.ClientFeedbackDef";
    case Line::kAssist: return "AssistBanter.ClientFeedbackDef";
    case Line::kFriendlyInitiate: return "FriendlyBanterInitiate.ClientFeedbackDef";
    case Line::kFriendlyResponse: return "FriendlyBanterResponse.ClientFeedbackDef";
    case Line::kEnemyInitiate: return "EnemyBanterInitiate.ClientFeedbackDef";
    case Line::kEnemyResponse: return "EnemyBanterResponse.ClientFeedbackDef";
    case Line::kPlayerLeft: return "PlayerLeftGame.ClientFeedbackDef";
    case Line::kPlayerReconnected: return "PlayerReconnectedGame.ClientFeedbackDef";
    default: return "";
  }
}

// Tuning.BanterTuning (AssetData type BanterTuning, 0x3C bytes); the defaults are build 854's values.
struct Tuning {
  float updatePeriod = 1.f;             // +0x00
  float minimumBanterCooldown = 5.f;    // +0x04
  float friendlyStart = 25.f;           // +0x08 friendlyBanterStartTime
  float friendlyEnd = 115.f;            // +0x0C friendlyBanterEndTime
  float friendlyRange = 20.f;           // +0x10
  float friendlyCooldown = 100.f;       // +0x14
  float enemyStart = 25.f;              // +0x18
  float enemyEnd = 115.f;               // +0x1C
  float enemyRange = 20.f;              // +0x20
  float enemyCooldown = 100.f;          // +0x24
  float killCooldown = 5.f;             // +0x28 killBanterCooldown
  int maxAssistLinesPerKill = 1;        // +0x2C maxNumberAssistVOPerKill
  float minimumEmoteCooldown = 5.f;     // +0x30
  float sequentialEmoteCooldown = 10.f; // +0x34
  int numEmoteLines = 3;                // +0x38
};

// Whether a read tuning is usable (finite, non-negative, ranges ordered); the host keeps the defaults otherwise.
inline bool Plausible(const Tuning& t) {
  const float f[] = {t.updatePeriod, t.minimumBanterCooldown, t.friendlyStart, t.friendlyEnd, t.friendlyRange,
                     t.friendlyCooldown, t.enemyStart, t.enemyEnd, t.enemyRange, t.enemyCooldown, t.killCooldown,
                     t.minimumEmoteCooldown, t.sequentialEmoteCooldown};
  for (float v : f)
    if (!(v >= 0.f && v < 100000.f)) return false;
  return t.updatePeriod > 0.f && t.friendlyStart <= t.friendlyEnd && t.enemyStart <= t.enemyEnd &&
         t.maxAssistLinesPerKill >= 0 && t.maxAssistLinesPerKill <= 10 && t.numEmoteLines >= 1 && t.numEmoteLines <= 9;
}

// CharacterBanterData (a noun's BanterData, +0x2FC): its targets, each a list of nouns with the number of the speaker's
// friend / foe initiate lines about them (variants 1..count) and whether it has a kill line about them; and the emote
// lines (nEmoteType, enum 0x0142CB70: 0 charge ... 11 joke ... 13 dance) that play in sequence.
struct Target {
  std::vector<std::uint32_t> nouns;  // noun asset ids (FNV-1 of the lower-case "<name>.noun")
  int friendly = 0, enemy = 0, kill = 0;
};
struct Table {
  std::vector<Target> targets;
  std::vector<int> sequentialEmotes;
};

inline const Target* FindTarget(const Table* t, std::uint32_t noun) {
  if (!t || !noun) return nullptr;
  for (const Target& x : t->targets)
    for (std::uint32_t n : x.nouns)
      if (n == noun) return &x;
  return nullptr;
}

// The PlayerClass tokens with no event in any of build 854's 141 sound banks (the unreleased shapers): they say
// nothing.
inline bool Voiced(const std::string& token) {
  static const char* const kUnvoiced[] = {"PC_BattleSisters", "PC_Destroyer", "PC_Ooze", "PC_Paladin",
                                          "PC_Sloth",         "PC_WeaponMaster", "PC_Weaver"};
  if (token.empty()) return false;
  for (const char* u : kUnvoiced)
    if (token == u) return false;
  return true;
}

// The pair lines CharacterBanterData allows whose event is in no voice bank (re/scripts/banter_banks.py): Dreamer's
// friend lines about Artist (the data's friendlyCount 2; neither initiate nor Artist's responses exist), Survivor's
// about Fenmore (friendlyCount 1; no initiate, no response) and King of Masks' answer 01 to Sculptor (it has only 03).
// `speaker`/`target` are the line's ~objectnounname~/~targetnounname~ tokens.
inline bool LineMissing(Line l, const std::string& speaker, const std::string& target, unsigned variant) {
  struct Gap {
    Line line;
    const char* speaker;
    const char* target;
    unsigned variant;
  };
  static const Gap kGaps[] = {
      {Line::kFriendlyInitiate, "PC_Dreamer", "PC_Artist", 1},  {Line::kFriendlyInitiate, "PC_Dreamer", "PC_Artist", 2},
      {Line::kFriendlyResponse, "PC_Artist", "PC_Dreamer", 1},  {Line::kFriendlyResponse, "PC_Artist", "PC_Dreamer", 2},
      {Line::kFriendlyInitiate, "PC_Survivor", "PC_Alpha", 1},  {Line::kFriendlyResponse, "PC_Alpha", "PC_Survivor", 1},
      {Line::kFriendlyResponse, "PC_KingOfMasks", "PC_Sculptor", 1},
  };
  for (const Gap& g : kGaps)
    if (g.line == l && g.variant == variant && speaker == g.speaker && target == g.target) return true;
  return false;
}

// The emote enum of a PlayerComm voice line's def name ("PlayerCommJoke.ClientFeedbackDef" -> 11), -1 for others.
inline int EmoteOfDef(const char* defName) {
  static const struct {
    const char* prefix;
    int emote;
  } kEmotes[] = {{"PlayerCommCharge.", 0},     {"PlayerCommCare.", 1},  {"PlayerCommNo.", 2},     {"PlayerCommSorry.", 3},
                 {"PlayerCommThankYou.", 4},   {"PlayerCommYes.", 5},   {"PlayerCommMissing.", 6},
                 {"PlayerCommCompliment.", 8}, {"PlayerCommLaugh.", 9}, {"PlayerCommTaunt.", 10},
                 {"PlayerCommJoke.", 11},      {"PlayerCommFlirt.", 12}, {"PlayerCommDance.", 13}};
  if (!defName) return -1;
  for (const auto& e : kEmotes)
    if (std::strncmp(defName, e.prefix, std::strlen(e.prefix)) == 0) return e.emote;
  return -1;
}

// One player's shaper as the rules see it.
struct Shaper {
  int index = -1;
  int team = 0;                // 1 or 2
  std::uint32_t creature = 0;  // its current creature's object id
  std::uint32_t noun = 0;      // the creature's noun asset id
  std::string token;           // ~objectnounname~ as the client resolves it (noun, else PlayerClass clientFeedbackTokenName)
  bool alive = false;
  float x = 0.f, y = 0.f;
  unsigned seenBy = 0;         // mVisibleToTeam (+0x888): bit 1 << team per team that sees the creature
  const Table* table = nullptr;
};

// A line to send: a ClientFeedbackEvent {eventId = the def's id, objectId, targetId, uintVal} to the players of
// `teamBits`' teams.
struct Say {
  Line line = Line::kCount;
  int speaker = -1, target = -1;  // player indices, -1 none
  std::uint32_t object = 0, targetObject = 0;
  unsigned variant = 0;  // uintVal (~uintdata~ as %02u, or the player index for the presence lines)
  unsigned teamBits = 0;
  unsigned excludePlayers = 0;  // gameplay-index bits left out (the rejoiner of PlayerReconnectedGame)
};

// The teams that hear a shaper's line: the ones that see it and its own [assumed].
inline unsigned HearingTeams(const Shaper& s) {
  unsigned bits = s.seenBy & 0x6u;
  if (s.team == 1 || s.team == 2) bits |= 1u << s.team;
  return bits;
}
inline bool Sees(const Shaper& viewerTeamOf, const Shaper& seen) {
  return viewerTeamOf.team >= 0 && viewerTeamOf.team < 8 && ((seen.seenBy >> viewerTeamOf.team) & 1u) != 0;
}
inline float Distance(const Shaper& a, const Shaper& b) {
  const float dx = a.x - b.x, dy = a.y - b.y;
  return std::sqrt(dx * dx + dy * dy);
}

// A small deterministic generator for the variants (xorshift32).
struct Rng {
  std::uint32_t s = 0x9E3779B9u;
  explicit Rng(std::uint32_t seed = 1) : s(seed ? seed : 0x9E3779B9u) {}
  std::uint32_t Next() {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return s;
  }
  unsigned Pick(unsigned n) { return n ? Next() % n : 0u; }  // 0 .. n-1
};

struct Switches {
  bool kill = true, assist = true, friendly = true, enemy = true;
  bool window = true;                // the meeting kinds only between their start and end time
  bool multiKillReplacesKill = true; // no kill line on a multi-kill (its Multikill_N_Player follows)
};

// Why a candidate said nothing (for the host's stats and log).
struct Skips {
  unsigned dead = 0, unvoiced = 0, cooldown = 0, multiKill = 0, missing = 0, unseen = 0, assistCap = 0;
};

struct Assist {
  int player = -1;
  std::uint64_t creditMs = 0;  // its entry in the assist matrix (AssistTracker_RecordHit), 0 unknown
};

// The per-shaper clocks and the meeting step's pacing.
class Book {
 public:
  Skips skips;

  // A player kill (D1h's KillOutcome: `killer` credited, `assisters` with their credit times, the killer's multi-kill
  // count after this kill). `shapers` are all players' shapers (any order).
  std::vector<Say> OnPlayerKill(const std::vector<Shaper>& shapers, int killer, int victim,
                                const std::vector<Assist>& assisters, unsigned multiKill, std::uint64_t nowMs,
                                const Tuning& t, const Switches& sw = Switches()) {
    std::vector<Say> out;
    const Shaper* k = Find(shapers, killer);
    const Shaper* v = Find(shapers, victim);
    if (!v || !Valid(victim)) return out;
    if (k && Valid(killer) && killer != victim && sw.kill) {
      if (!k->alive) {
        ++skips.dead;
      } else if (!Voiced(k->token)) {
        ++skips.unvoiced;
      } else if (sw.multiKillReplacesKill && multiKill >= 2) {
        ++skips.multiKill;
      } else if (!Ready(c_[killer].kill, nowMs, t.killCooldown) || !Ready(c_[killer].any, nowMs, t.minimumBanterCooldown)) {
        ++skips.cooldown;
      } else {
        const Target* tg = FindTarget(k->table, v->noun);
        Line l = tg && tg->kill > 0 ? Line::kKillWithTarget : Line::kKillGeneric;
        if (l == Line::kKillWithTarget && LineMissing(l, k->token, v->token, 0)) l = Line::kKillGeneric;
        Say s;
        s.line = l;
        s.speaker = killer;
        s.target = victim;
        s.object = k->creature;
        s.targetObject = v->creature;
        s.teamBits = HearingTeams(*k);
        out.push_back(s);
        c_[killer].kill = c_[killer].any = Stamp(nowMs);
      }
    }
    if (sw.assist && !assisters.empty()) {
      std::vector<Assist> order(assisters);
      std::stable_sort(order.begin(), order.end(), [](const Assist& a, const Assist& b) {
        if (a.creditMs != b.creditMs) return a.creditMs > b.creditMs;
        return a.player < b.player;
      });
      int said = 0;
      for (const Assist& a : order) {
        const Shaper* s = Find(shapers, a.player);
        if (!s || !Valid(a.player) || a.player == killer || a.player == victim) continue;
        if (said >= t.maxAssistLinesPerKill) {
          ++skips.assistCap;
          continue;
        }
        if (!s->alive) {
          ++skips.dead;
          continue;
        }
        if (!Voiced(s->token)) {
          ++skips.unvoiced;
          continue;
        }
        if (!Ready(c_[a.player].kill, nowMs, t.killCooldown) || !Ready(c_[a.player].any, nowMs, t.minimumBanterCooldown)) {
          ++skips.cooldown;
          continue;
        }
        Say say;
        say.line = Line::kAssist;
        say.speaker = a.player;
        say.target = victim;
        say.object = s->creature;
        say.targetObject = v->creature;
        say.teamBits = HearingTeams(*s);
        out.push_back(say);
        c_[a.player].kill = c_[a.player].any = Stamp(nowMs);
        ++said;
      }
    }
    return out;
  }

  // Whether the meeting step is due (every updatePeriod of match time; the first call is).
  bool MeetDue(double matchSeconds, const Tuning& t) {
    if (lastMeet_ >= 0.0 && matchSeconds - lastMeet_ < static_cast<double>(t.updatePeriod) - 1e-6) return false;
    lastMeet_ = matchSeconds;
    return true;
  }

  // The meeting banter of one step: at most one friendly and one enemy exchange (initiate + response each).
  std::vector<Say> MeetStep(const std::vector<Shaper>& shapers, double matchSeconds, std::uint64_t nowMs,
                            const Tuning& t, Rng& rng, const Switches& sw = Switches()) {
    std::vector<Say> out;
    if (sw.friendly && (!sw.window || InWindow(matchSeconds, t.friendlyStart, t.friendlyEnd)))
      Exchange(shapers, true, nowMs, t, rng, &out);
    if (sw.enemy && (!sw.window || InWindow(matchSeconds, t.enemyStart, t.enemyEnd)))
      Exchange(shapers, false, nowMs, t, rng, &out);
    return out;
  }

  // The variant (uintVal) of a voice line `emote` (EmoteOfDef) said by `player`: for a sequential emote the next of
  // numEmoteLines while repeated within sequentialEmoteCooldown, else 1; a repeat within minimumEmoteCooldown (which
  // the client drops: the PlayerComm defs' own 5-s cooldown per speaker) keeps the last variant and doesn't count. Any
  // other line keeps `randomVariant`.
  unsigned EmoteVariant(int player, int emote, bool sequential, unsigned randomVariant, std::uint64_t nowMs,
                        const Tuning& t) {
    if (!Valid(player) || emote < 0 || emote >= kEmotes) return randomVariant;
    EmoteClock& e = emote_[player][emote];
    if (!sequential) return randomVariant;
    if (e.at != 0 && e.variant >= 1 && !Ready(e.at, nowMs, t.minimumEmoteCooldown)) return e.variant;
    const unsigned n = static_cast<unsigned>(std::max(1, t.numEmoteLines));
    const bool chained = e.at != 0 && e.variant >= 1 && !Ready(e.at, nowMs, t.sequentialEmoteCooldown);
    e.variant = chained ? e.variant % n + 1 : 1u;
    e.at = Stamp(nowMs);
    return e.variant;
  }

  // The presence lines (no speaker; the player's creature as the object when it has one).
  static Say PlayerLeft(int index, std::uint32_t creature) {
    Say s;
    s.line = Line::kPlayerLeft;
    s.target = index;
    s.object = creature;
    s.variant = static_cast<unsigned>(index);
    s.teamBits = 0x7u;
    return s;
  }
  static Say PlayerReconnected(int index, std::uint32_t creature) {
    Say s = PlayerLeft(index, creature);
    s.line = Line::kPlayerReconnected;
    s.excludePlayers = index >= 0 && index < 16 ? 1u << index : 0u;
    return s;
  }

 private:
  static constexpr int kEmotes = 16;
  struct Clock {
    std::uint64_t any = 0, kill = 0, friendly = 0, enemy = 0;  // stamps (ms + 1; 0 = never)
  };
  struct EmoteClock {
    std::uint64_t at = 0;
    unsigned variant = 0;
  };
  Clock c_[kMaxPlayers];
  EmoteClock emote_[kMaxPlayers][kEmotes];
  double lastMeet_ = -1.0;

  static bool Valid(int p) { return p >= 0 && p < kMaxPlayers; }
  static std::uint64_t Stamp(std::uint64_t nowMs) { return nowMs + 1; }
  // Whether `seconds` have passed since `stamp` (0 = never: ready).
  static bool Ready(std::uint64_t stamp, std::uint64_t nowMs, float seconds) {
    if (!stamp) return true;
    const std::uint64_t at = stamp - 1;
    if (nowMs < at) return false;
    return static_cast<double>(nowMs - at) >= static_cast<double>(seconds) * 1000.0;
  }
  static bool InWindow(double s, float start, float end) { return s >= start && s <= end; }
  static const Shaper* Find(const std::vector<Shaper>& v, int index) {
    for (const Shaper& s : v)
      if (s.index == index) return &s;
    return nullptr;
  }

  void Exchange(const std::vector<Shaper>& shapers, bool friendly, std::uint64_t nowMs, const Tuning& t, Rng& rng,
                std::vector<Say>* out) {
    const float range = friendly ? t.friendlyRange : t.enemyRange;
    const float cooldown = friendly ? t.friendlyCooldown : t.enemyCooldown;
    const Line initiate = friendly ? Line::kFriendlyInitiate : Line::kEnemyInitiate;
    const Line response = friendly ? Line::kFriendlyResponse : Line::kEnemyResponse;
    struct Pick {
      const Shaper* a = nullptr;
      const Shaper* b = nullptr;
      float d = 0.f;
      std::vector<unsigned> variants;
    };
    Pick best;
    for (const Shaper& a : shapers) {
      if (!Valid(a.index) || !a.alive || !a.table || !Voiced(a.token)) continue;
      if (!Ready(Kind(a.index, friendly), nowMs, cooldown) || !Ready(c_[a.index].any, nowMs, t.minimumBanterCooldown))
        continue;
      for (const Shaper& b : shapers) {
        if (&a == &b || !Valid(b.index) || b.index == a.index || !b.alive || !Voiced(b.token)) continue;
        if (friendly ? a.team != b.team : (a.team == b.team || a.team < 1 || a.team > 2 || b.team < 1 || b.team > 2))
          continue;
        const Target* tg = FindTarget(a.table, b.noun);
        const int count = !tg ? 0 : friendly ? tg->friendly : tg->enemy;
        if (count <= 0) continue;
        const float d = Distance(a, b);
        if (d > range) continue;
        if (!Ready(Kind(b.index, friendly), nowMs, cooldown) || !Ready(c_[b.index].any, nowMs, t.minimumBanterCooldown))
          continue;
        if (!friendly && (!Sees(b, a) || !Sees(a, b))) {
          ++skips.unseen;
          continue;
        }
        std::vector<unsigned> vs;
        for (int v = 1; v <= count && v <= 9; ++v)
          if (!LineMissing(initiate, a.token, b.token, static_cast<unsigned>(v))) vs.push_back(static_cast<unsigned>(v));
        if (vs.empty()) {
          ++skips.missing;
          continue;
        }
        const bool better = !best.a || d < best.d ||
                            (d == best.d && (a.index < best.a->index || (a.index == best.a->index && b.index < best.b->index)));
        if (better) {
          best.a = &a;
          best.b = &b;
          best.d = d;
          best.variants = vs;
        }
      }
    }
    if (!best.a) return;
    const unsigned v = best.variants[rng.Pick(static_cast<unsigned>(best.variants.size()))];
    Say s;
    s.line = initiate;
    s.speaker = best.a->index;
    s.target = best.b->index;
    s.object = best.a->creature;
    s.targetObject = best.b->creature;
    s.variant = v;
    s.teamBits = HearingTeams(*best.a);
    out->push_back(s);
    if (!LineMissing(response, best.b->token, best.a->token, v)) {
      Say r;
      r.line = response;
      r.speaker = best.b->index;
      r.target = best.a->index;
      r.object = best.b->creature;
      r.targetObject = best.a->creature;
      r.variant = v;
      r.teamBits = HearingTeams(*best.b);
      out->push_back(r);
    } else {
      ++skips.missing;
    }
    for (int p : {best.a->index, best.b->index}) {
      Kind(p, friendly) = Stamp(nowMs);
      c_[p].any = Stamp(nowMs);
    }
  }
  std::uint64_t& Kind(int p, bool friendly) { return friendly ? c_[p].friendly : c_[p].enemy; }
};

}  // namespace gms::banter
