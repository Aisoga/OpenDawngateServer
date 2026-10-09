// gms/core/banter (D11b): the shapers' banter, kill, assist and presence lines and the sequential emotes (docs/
// GMS_PROTOCOL.md *The shapers' banter and kill lines (D11b)*). The tables are build 854's CharacterBanterData
// (re/out/assetdata/CharacterBanterData.json) for the shapers used here; noun ids are FNV-1 of the lower-case
// "<name>.noun", as the AssetCatalog keys them.
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "core/banter/banter.h"
#include "tests/check.h"

using namespace gms::banter;

namespace {

std::uint32_t Fnv1(const char* s) {
  std::uint32_t h = 0x811C9DC5u;
  for (; *s; ++s) {
    h *= 0x01000193u;
    char c = *s;
    if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
    h ^= static_cast<unsigned char>(c);
  }
  return h;
}

// PC_Alpha's CharacterBanterData (Fenmore): desecrator f3 e3 k1; survivor (and three skins) f3 e0 k1; shadow_assassin,
// shadowassassin_thenine f3 e2 k1; salousthepenitent k1; loremaster and guardian 0/0/0.
Table AlphaTable() {
  Table t;
  Target d;
  d.nouns = {Fnv1("desecrator.noun")};
  d.friendly = 3;
  d.enemy = 3;
  d.kill = 1;
  Target s;
  s.nouns = {Fnv1("survivor.noun"), Fnv1("survivor_dark.noun"), Fnv1("survivor_fire.noun"), Fnv1("survivor_ice.noun")};
  s.friendly = 3;
  s.kill = 1;
  Target sa;
  sa.nouns = {Fnv1("shadow_assassin.noun"), Fnv1("shadowassassin_thenine.noun")};
  sa.friendly = 3;
  sa.enemy = 2;
  sa.kill = 1;
  Target p;
  p.nouns = {Fnv1("salousthepenitent.noun")};
  p.kill = 1;
  Target lm;
  lm.nouns = {Fnv1("loremaster.noun")};
  t.targets = {d, s, sa, p, lm};
  return t;
}
// PC_Desecrator's: alpha, alpha_thenine k1 only; artificer f3 e3 k1; merchant_princess f3 e3 k1; sequentialEmote [11].
Table DesecratorTable() {
  Table t;
  Target a;
  a.nouns = {Fnv1("alpha.noun"), Fnv1("alpha_thenine.noun")};
  a.kill = 1;
  Target ar;
  ar.nouns = {Fnv1("artificer.noun")};
  ar.friendly = 3;
  ar.enemy = 3;
  ar.kill = 1;
  t.targets = {a, ar};
  t.sequentialEmotes = {11};
  return t;
}
// PC_Survivor's: alpha f1 e3 k1 (its friend lines about Fenmore are in no voice bank).
Table SurvivorTable() {
  Table t;
  Target a;
  a.nouns = {Fnv1("alpha.noun"), Fnv1("alpha_thenine.noun")};
  a.friendly = 1;
  a.enemy = 3;
  a.kill = 1;
  t.targets = {a};
  return t;
}
// PC_Sculptor's entry about kingofmasks: f1 (King of Masks has only response 03 for it).
Table SculptorTable() {
  Table t;
  Target k;
  k.nouns = {Fnv1("kingofmasks.noun")};
  k.friendly = 1;
  k.kill = 1;
  t.targets = {k};
  return t;
}

Shaper Make(int index, int team, const char* noun, const char* token, float x, float y, const Table* table) {
  Shaper s;
  s.index = index;
  s.team = team;
  s.creature = 0x7C000000u + static_cast<std::uint32_t>(index);
  s.noun = Fnv1(noun);
  s.token = token;
  s.alive = true;
  s.x = x;
  s.y = y;
  s.seenBy = 1u << team;  // its own team sees it
  s.table = table;
  return s;
}

int Count(const std::vector<Say>& v, Line l) {
  int n = 0;
  for (const Say& s : v) n += s.line == l;
  return n;
}

}  // namespace

// The defs, the data's tuning, the hearing teams, the voiced classes.
GMS_TEST(BanterBasics) {
  CHECK_EQ(std::string(DefName(Line::kKillWithTarget)), std::string("KillBanterWithTarget.ClientFeedbackDef"));
  CHECK_EQ(std::string(DefName(Line::kPlayerReconnected)), std::string("PlayerReconnectedGame.ClientFeedbackDef"));
  CHECK_EQ(std::string(LineName(Line::kEnemyResponse)), std::string("EnemyBanterResponse"));
  const Tuning t;  // re/out/assetdata/BanterTuning.json
  CHECK(t.updatePeriod == 1.f && t.minimumBanterCooldown == 5.f && t.friendlyStart == 25.f && t.friendlyEnd == 115.f);
  CHECK(t.friendlyRange == 20.f && t.friendlyCooldown == 100.f && t.enemyRange == 20.f && t.enemyCooldown == 100.f);
  CHECK(t.killCooldown == 5.f && t.maxAssistLinesPerKill == 1 && t.sequentialEmoteCooldown == 10.f &&
        t.numEmoteLines == 3);
  CHECK(Plausible(t));
  Tuning bad;
  bad.updatePeriod = 0.f;
  CHECK(!Plausible(bad));
  bad = Tuning();
  bad.friendlyEnd = 10.f;  // before its start
  CHECK(!Plausible(bad));
  CHECK(Voiced("PC_Alpha") && Voiced("PC_HuTank_Catbus"));
  CHECK(!Voiced("PC_Weaver") && !Voiced("PC_WeaponMaster") && !Voiced(""));
  Shaper s = Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, nullptr);
  CHECK_EQ(HearingTeams(s), 0x2u);
  s.seenBy = 0x6;  // the enemy team sees it too
  CHECK_EQ(HearingTeams(s), 0x6u);
  CHECK_EQ(EmoteOfDef("PlayerCommJoke.ClientFeedbackDef"), 11);
  CHECK_EQ(EmoteOfDef("PlayerCommMissing.ClientFeedbackDef"), 6);
  CHECK_EQ(EmoteOfDef("PlayerCommDance.ClientFeedbackDef"), 13);
  CHECK_EQ(EmoteOfDef("PlayerRespawned.ClientFeedbackDef"), -1);
  // the voice banks' gaps
  CHECK(LineMissing(Line::kFriendlyInitiate, "PC_Survivor", "PC_Alpha", 1));
  CHECK(LineMissing(Line::kFriendlyResponse, "PC_KingOfMasks", "PC_Sculptor", 1));
  CHECK(!LineMissing(Line::kFriendlyResponse, "PC_KingOfMasks", "PC_Sculptor", 3));
  CHECK(!LineMissing(Line::kFriendlyInitiate, "PC_Alpha", "PC_Desecrator", 2));
}

// A kill by Fenmore of a Desecrator: the targeted line (Play_PC_Alpha_VO_kill_PC_Desecrator); of a Warden (no entry):
// the generic one; the assister with the latest credit speaks, one per kill.
GMS_TEST(BanterKillAndAssistLines) {
  const Table alpha = AlphaTable(), des = DesecratorTable();
  std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                           Make(1, 1, "warden.noun", "PC_Warden", 5, 0, nullptr),
                           Make(2, 1, "desecrator.noun", "PC_Desecrator", 6, 0, &des),
                           Make(5, 2, "desecrator.noun", "PC_Desecrator", 10, 0, &des),
                           Make(6, 2, "warden.noun", "PC_Warden", 12, 0, nullptr)};
  v[0].seenBy = 0x6;  // the victim's team sees the killer
  Book b;
  const Tuning t;
  std::vector<Assist> as = {{1, 40000}, {2, 41000}};
  std::vector<Say> s = b.OnPlayerKill(v, 0, 5, as, 1, 50000, t);
  CHECK_EQ(s.size(), static_cast<std::size_t>(2));
  CHECK(s[0].line == Line::kKillWithTarget);
  CHECK_EQ(s[0].speaker, 0);
  CHECK_EQ(s[0].object, v[0].creature);
  CHECK_EQ(s[0].targetObject, v[3].creature);  // ~targetnounname~: the victim's creature
  CHECK_EQ(s[0].teamBits, 0x6u);
  CHECK_EQ(s[0].variant, 0u);
  CHECK(s[1].line == Line::kAssist);
  CHECK_EQ(s[1].speaker, 2);  // the later credit
  CHECK_EQ(s[1].teamBits, 0x2u);
  CHECK_EQ(b.skips.assistCap, 1u);  // player 1: maxNumberAssistVOPerKill 1
  // Fenmore kills the Warden (no entry in its table): the generic line
  Book b2;
  s = b2.OnPlayerKill(v, 0, 6, {}, 1, 50000, t);
  CHECK_EQ(s.size(), static_cast<std::size_t>(1));
  CHECK(s[0].line == Line::kKillGeneric);
  // a killer without a table says the generic line
  s = b2.OnPlayerKill(v, 1, 5, {}, 1, 60000, t);
  CHECK(s.size() == 1 && s[0].line == Line::kKillGeneric && s[0].speaker == 1);
}

// The kill line's cooldowns (killBanterCooldown 5 s, minimumBanterCooldown 5 s per shaper), a dead or unvoiced
// killer, and a multi-kill (its own Multikill_N_Player follows the client's announcer).
GMS_TEST(BanterKillRules) {
  const Table alpha = AlphaTable();
  std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                           Make(5, 2, "warden.noun", "PC_Warden", 3, 0, nullptr),
                           Make(6, 2, "weaver.noun", "PC_Weaver", 3, 0, nullptr)};
  Book b;
  const Tuning t;
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 1, 10000, t).size(), static_cast<std::size_t>(1));
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 1, 14999, t).size(), static_cast<std::size_t>(0));  // 4.999 s later
  CHECK_EQ(b.skips.cooldown, 1u);
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 1, 15000, t).size(), static_cast<std::size_t>(1));  // 5 s later
  // a double kill: no kill line, but an assist line still goes out
  std::vector<Shaper> w = v;
  w.push_back(Make(1, 1, "warden.noun", "PC_Warden", 1, 0, nullptr));
  std::vector<Say> s = b.OnPlayerKill(w, 0, 5, {{1, 30000}}, 2, 30000, t);
  CHECK(s.size() == 1 && s[0].line == Line::kAssist);
  CHECK_EQ(b.skips.multiKill, 1u);
  Switches sw;
  sw.multiKillReplacesKill = false;
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 2, 40000, t, sw).size(), static_cast<std::size_t>(1));
  // dead killer, unvoiced killer
  v[0].alive = false;
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 1, 60000, t).size(), static_cast<std::size_t>(0));
  CHECK_EQ(b.skips.dead, 1u);
  CHECK_EQ(b.OnPlayerKill(v, 6, 0, {}, 1, 60000, t).size(), static_cast<std::size_t>(0));
  CHECK_EQ(b.skips.unvoiced, 1u);
  // switched off
  v[0].alive = true;
  sw = Switches();
  sw.kill = false;
  CHECK_EQ(b.OnPlayerKill(v, 0, 5, {}, 1, 90000, t, sw).size(), static_cast<std::size_t>(0));
}

// Two allies within 20 units at match 25-115 s: the speaker's FriendlyBanterInitiate_NN about the other, the other's
// FriendlyBanterResponse_NN (the same NN) about the speaker; then 100 s of cooldown for both.
GMS_TEST(BanterFriendlyMeeting) {
  const Table alpha = AlphaTable(), des = DesecratorTable();
  std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                           Make(1, 1, "desecrator.noun", "PC_Desecrator", 12, 0, &des)};
  const Tuning t;
  Rng rng(7);
  {
    Book b;
    CHECK_EQ(b.MeetStep(v, 24.9, 24900, t, rng).size(), static_cast<std::size_t>(0));  // before the window
  }
  {
    Book b;
    CHECK_EQ(b.MeetStep(v, 115.1, 115100, t, rng).size(), static_cast<std::size_t>(0));  // after it
    Switches sw;
    sw.window = false;
    CHECK_EQ(b.MeetStep(v, 115.1, 115100, t, rng, sw).size(), static_cast<std::size_t>(2));  // the test aid
  }
  Book b;
  std::vector<Say> s = b.MeetStep(v, 30.0, 30000, t, rng);
  CHECK_EQ(s.size(), static_cast<std::size_t>(2));
  CHECK(s[0].line == Line::kFriendlyInitiate && s[0].speaker == 0 && s[0].target == 1);
  CHECK(s[0].object == v[0].creature && s[0].targetObject == v[1].creature);
  CHECK(s[0].variant >= 1 && s[0].variant <= 3);
  CHECK(s[1].line == Line::kFriendlyResponse && s[1].speaker == 1 && s[1].target == 0);
  CHECK_EQ(s[1].variant, s[0].variant);
  CHECK_EQ(s[0].teamBits, 0x2u);
  // both on the friendly cooldown: nothing until 100 s later (and the window ends at 115 s)
  CHECK_EQ(b.MeetStep(v, 60.0, 60000, t, rng).size(), static_cast<std::size_t>(0));
  Switches sw;
  sw.window = false;
  CHECK_EQ(b.MeetStep(v, 129.9, 129900, t, rng, sw).size(), static_cast<std::size_t>(0));
  CHECK_EQ(b.MeetStep(v, 130.0, 130000, t, rng, sw).size(), static_cast<std::size_t>(2));
  // out of range, dead, no entry
  Book c;
  v[1].x = 20.5f;
  CHECK_EQ(c.MeetStep(v, 30.0, 30000, t, rng).size(), static_cast<std::size_t>(0));
  v[1].x = 20.0f;
  v[1].alive = false;
  CHECK_EQ(c.MeetStep(v, 30.0, 30000, t, rng).size(), static_cast<std::size_t>(0));
  v[1].alive = true;
  CHECK_EQ(c.MeetStep(v, 30.0, 30000, t, rng).size(), static_cast<std::size_t>(2));
  // the variants are drawn from 1..friendlyCount
  bool seen[4] = {false, false, false, false};
  Rng r2(3);
  for (int i = 0; i < 60; ++i) {
    Book d;
    std::vector<Say> x = d.MeetStep(v, 30.0, 30000, t, r2);
    if (!x.empty() && x[0].variant >= 1 && x[0].variant <= 3) seen[x[0].variant] = true;
  }
  CHECK(seen[1] && seen[2] && seen[3]);
}

// Two enemies: EnemyBanterInitiate/Response, only while each team sees the other.
GMS_TEST(BanterEnemyMeeting) {
  const Table alpha = AlphaTable(), des = DesecratorTable();
  std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                           Make(5, 2, "desecrator.noun", "PC_Desecrator", 15, 0, &des)};
  const Tuning t;
  Rng rng(1);
  Book b;
  CHECK_EQ(b.MeetStep(v, 60.0, 60000, t, rng).size(), static_cast<std::size_t>(0));  // fogged from each other
  CHECK(b.skips.unseen >= 1u);
  v[0].seenBy = 0x6;
  CHECK_EQ(b.MeetStep(v, 61.0, 61000, t, rng).size(), static_cast<std::size_t>(0));  // only one way
  v[1].seenBy = 0x6;
  std::vector<Say> s = b.MeetStep(v, 62.0, 62000, t, rng);
  CHECK_EQ(s.size(), static_cast<std::size_t>(2));
  CHECK(s[0].line == Line::kEnemyInitiate && s[0].speaker == 0 && s[0].teamBits == 0x6u);
  CHECK(s[1].line == Line::kEnemyResponse && s[1].speaker == 5 && s[1].variant == s[0].variant);
  CHECK(s[0].variant >= 1 && s[0].variant <= 3);
  // Desecrator has no enemy line about Fenmore: only Fenmore initiates
  Switches sw;
  sw.friendly = false;
  sw.enemy = false;
  Book c;
  CHECK_EQ(c.MeetStep(v, 62.0, 62000, t, rng, sw).size(), static_cast<std::size_t>(0));
}

// The closest pair speaks first, one exchange per kind and step; the minimum cooldown keeps a shaper out of the other
// kind's exchange in the same step; lines in no voice bank are left out.
GMS_TEST(BanterMeetingChoiceAndGaps) {
  const Table alpha = AlphaTable(), des = DesecratorTable(), sur = SurvivorTable(), scu = SculptorTable();
  const Tuning t;
  Rng rng(5);
  {
    std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                             Make(1, 1, "desecrator.noun", "PC_Desecrator", 15, 0, &des),
                             Make(2, 1, "survivor.noun", "PC_Survivor", 0, 8, &sur)};
    Book b;
    std::vector<Say> s = b.MeetStep(v, 30.0, 30000, t, rng);
    // Fenmore -> Survivor (8 apart) before Fenmore -> Desecrator (15); Survivor's friend line about Fenmore and
    // Fenmore's answer 01 are in no bank, so only Fenmore's initiate about Survivor and Survivor's response go out
    CHECK_EQ(s.size(), static_cast<std::size_t>(2));
    CHECK(s[0].speaker == 0 && s[0].target == 2 && s[0].line == Line::kFriendlyInitiate);
    CHECK(s[1].speaker == 2 && s[1].line == Line::kFriendlyResponse);
  }
  {
    // Survivor alone with Fenmore (data friendlyCount 1, no event): Survivor never initiates; Fenmore does
    std::vector<Shaper> v = {Make(2, 1, "survivor.noun", "PC_Survivor", 0, 8, &sur),
                             Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, nullptr)};
    Book b;
    CHECK_EQ(b.MeetStep(v, 30.0, 30000, t, rng).size(), static_cast<std::size_t>(0));
    CHECK(b.skips.missing >= 1u);
  }
  {
    // Sculptor -> King of Masks: the initiate 01 exists, King of Masks' response 01 doesn't
    std::vector<Shaper> v = {Make(3, 2, "sculptor.noun", "PC_Sculptor", 0, 0, &scu),
                             Make(4, 2, "kingofmasks.noun", "PC_KingOfMasks", 3, 4, nullptr)};
    Book b;
    std::vector<Say> s = b.MeetStep(v, 30.0, 30000, t, rng);
    CHECK_EQ(s.size(), static_cast<std::size_t>(1));
    CHECK(s[0].line == Line::kFriendlyInitiate && s[0].variant == 1u && s[0].speaker == 3);
  }
  {
    // friendly and enemy in one step: Fenmore talks to its ally; the enemy pair needs two other shapers
    std::vector<Shaper> v = {Make(0, 1, "alpha.noun", "PC_Alpha", 0, 0, &alpha),
                             Make(1, 1, "desecrator.noun", "PC_Desecrator", 5, 0, &des),
                             Make(5, 2, "desecrator.noun", "PC_Desecrator", 10, 0, &des)};
    for (Shaper& s : v) s.seenBy = 0x6;
    Book b;
    std::vector<Say> s = b.MeetStep(v, 30.0, 30000, t, rng);
    CHECK_EQ(Count(s, Line::kFriendlyInitiate), 1);
    CHECK_EQ(Count(s, Line::kEnemyInitiate), 0);  // Fenmore, the enemy pair's speaker, is on the minimum cooldown now
    CHECK_EQ(b.MeetStep(v, 35.0, 35000, t, rng).size(), static_cast<std::size_t>(2));  // 5 s later: the enemy pair
  }
  // the step's pacing: every updatePeriod
  Book p;
  CHECK(p.MeetDue(0.0, t));
  CHECK(!p.MeetDue(0.5, t));
  CHECK(p.MeetDue(1.0, t));
  CHECK(!p.MeetDue(1.99, t));
  CHECK(p.MeetDue(2.0, t));
}

// Desecrator's joke (sequentialEmote 11) in order while repeated within 10 s; every other line keeps its random
// variant; the presence lines.
GMS_TEST(BanterEmotesAndPresence) {
  const Tuning t;
  Book b;
  CHECK_EQ(b.EmoteVariant(0, 11, true, 3, 1000, t), 1u);
  CHECK_EQ(b.EmoteVariant(0, 11, true, 3, 6000, t), 2u);
  CHECK_EQ(b.EmoteVariant(0, 11, true, 1, 15000, t), 3u);
  CHECK_EQ(b.EmoteVariant(0, 11, true, 2, 20000, t), 1u);  // wraps after numEmoteLines
  CHECK_EQ(b.EmoteVariant(0, 11, true, 2, 30001, t), 1u);  // 10.001 s later: from the start
  CHECK_EQ(b.EmoteVariant(0, 11, true, 3, 32000, t), 1u);  // 2 s later: the client drops it (5 s); not counted
  CHECK_EQ(b.EmoteVariant(0, 11, true, 3, 36001, t), 2u);  // 6 s after the one it played
  CHECK_EQ(b.EmoteVariant(0, 9, false, 2, 30500, t), 2u);  // laugh: the random one
  CHECK_EQ(b.EmoteVariant(1, 11, true, 3, 30500, t), 1u);  // another player's own sequence
  const Say l = Book::PlayerLeft(3, 0);
  CHECK(l.line == Line::kPlayerLeft && l.variant == 3u && l.teamBits == 0x7u && l.excludePlayers == 0u);
  const Say r = Book::PlayerReconnected(3, 0);
  CHECK(r.line == Line::kPlayerReconnected && r.variant == 3u && r.excludePlayers == 0x8u);
}
