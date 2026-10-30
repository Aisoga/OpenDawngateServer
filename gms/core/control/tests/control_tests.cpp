// Tests for gms/core/control (roadmap C1): the JSON codec, the messages against the example lines
// shared with the Go side (docs/data/host_control_examples.jsonl), the environment, and the client
// against a loopback server on an ephemeral port.
//
// Usage: dg_control_tests [examples.jsonl]   (default: the path CMake compiled in)
// Exit code 0 = all passed.
#include <winsock2.h>
#include <ws2tcpip.h>

#include <atomic>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <string>
#include <thread>
#include <vector>

#include "control/client.h"
#include "control/host_control.h"
#include "control/json.h"
#include "control/messages.h"

using namespace dggms::control;

static int g_failed = 0;
static int g_checks = 0;

#define CHECK(cond)                                                        \
  do {                                                                     \
    g_checks++;                                                            \
    if (!(cond)) {                                                         \
      g_failed++;                                                          \
      std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);          \
    }                                                                      \
  } while (0)

#define CHECK_EQ(a, b)                                                                              \
  do {                                                                                              \
    g_checks++;                                                                                     \
    auto va = (a);                                                                                  \
    auto vb = (b);                                                                                  \
    if (!(va == vb)) {                                                                              \
      g_failed++;                                                                                   \
      std::printf("FAIL %s:%d: %s == %s\n", __FILE__, __LINE__, #a, #b);                            \
    }                                                                                               \
  } while (0)

static void CheckStr(const std::string& got, const std::string& want, int line) {
  g_checks++;
  if (got != want) {
    g_failed++;
    std::printf("FAIL line %d:\n   got %s\n  want %s\n", line, got.c_str(), want.c_str());
  }
}
#define CHECK_STR(got, want) CheckStr((got), (want), __LINE__)

// ---------------------------------------------------------------------------------------------- JSON

static void TestJsonParse() {
  Json v;
  std::string err;
  CHECK(ParseJson(" {\"a\" : [1, -2, 3.5, true, false, null, \"x\"], \"b\":{}} ", v, &err));
  CHECK(v.IsObject());
  const Json* a = v.Find("a");
  CHECK(a && a->IsArray() && a->Items().size() == 7);
  if (a && a->Items().size() == 7) {
    CHECK_EQ(a->Items()[0].AsInt(), 1);
    CHECK_EQ(a->Items()[1].AsInt(), -2);
    CHECK(a->Items()[2].type() == Json::Type::Double && a->Items()[2].AsDouble() == 3.5);
    CHECK(a->Items()[3].AsBool() && !a->Items()[4].AsBool() && a->Items()[5].IsNull());
    CHECK_STR(a->Items()[6].AsString(), "x");
  }
  // 64-bit integers stay exact; beyond them a double
  CHECK(ParseJson("9223372036854775807", v) && v.IsInt() && v.AsInt() == INT64_MAX);
  CHECK(ParseJson("-9223372036854775808", v) && v.IsInt() && v.AsInt() == INT64_MIN);
  CHECK(ParseJson("9223372036854775808", v) && v.type() == Json::Type::Double);
  CHECK(ParseJson("1e3", v) && v.type() == Json::Type::Double && v.AsDouble() == 1000);
  // escapes: short ones, \u with a surrogate pair (U+1F600) and a 2-byte character (U+00E9)
  CHECK(ParseJson("\"a\\\"b\\\\c\\/d\\n\\t\\u00e9\\ud83d\\ude00\"", v, &err));
  CHECK_STR(v.AsString(), "a\"b\\c/d\n\t\xC3\xA9\xF0\x9F\x98\x80");
  CHECK(ParseJson("\"\\udc00\"", v) && v.AsString() == "\xEF\xBF\xBD");  // lone low surrogate -> U+FFFD
  // repeated member names: the last wins (as in Go)
  CHECK(ParseJson("{\"k\":1,\"k\":2}", v) && v.Members().size() == 1 && v.Find("k")->AsInt() == 2);
  // errors
  for (const char* bad : {"", "{", "[1,]", "{\"a\":}", "01", "1.", "-", "\"abc", "\"a\\x\"", "tru", "{} x",
                          "\"\x01\"", "{\"a\" 1}", "[1 2]", "\"\\u12\""}) {
    std::string e;
    bool ok = ParseJson(bad, v, &e);
    CHECK(!ok && !e.empty());
    if (ok) std::printf("   accepted: %s\n", bad);
  }
  std::string deep(kMaxDepth + 2, '[');
  deep += std::string(kMaxDepth + 2, ']');
  CHECK(!ParseJson(deep, v));
  std::string ok(kMaxDepth, '[');
  ok += std::string(kMaxDepth, ']');
  CHECK(ParseJson(ok, v));
}

static void TestJsonWrite() {
  Json o = Json::Object();
  o.Set("s", Json::Str("q\"b\\n\n\r\t\b\f\x01\x1f <a&b> \xC3\xA9 \xE2\x80\xA8\xE2\x80\xA9"));
  o.Set("i", Json::Int(INT64_MIN)).Set("b", Json::Bool(true)).Set("n", Json()).Set("a", Json::Array());
  o.Set("i", Json::Int(-1));  // replaces, keeps the position
  // what Go's encoder (SetEscapeHTML(false)) writes for the same values
  CHECK_STR(WriteJson(o),
            "{\"s\":\"q\\\"b\\\\n\\n\\r\\t\\b\\f\\u0001\\u001f <a&b> \xC3\xA9 \\u2028\\u2029\",\"i\":-1,\"b\":true,"
            "\"n\":null,\"a\":[]}");
  // write -> parse -> write is stable
  Json back;
  CHECK(ParseJson(WriteJson(o), back));
  CHECK_STR(WriteJson(back), WriteJson(o));
}

// ------------------------------------------------------------------------------------------ messages

static std::vector<std::string> ReadExamples(const std::string& path) {
  std::vector<std::string> lines;
  std::ifstream f(path, std::ios::binary);
  std::string line;
  while (std::getline(f, line)) {
    if (!line.empty() && line.back() == '\r') line.pop_back();
    if (!line.empty() && line[0] != '#') lines.push_back(line);
  }
  return lines;
}

static void TestExamples(const std::string& path) {
  std::vector<std::string> lines = ReadExamples(path);
  CHECK(lines.size() >= 12);
  if (lines.empty()) std::printf("   no examples in %s\n", path.c_str());
  bool seen[std::variant_size_v<Message>] = {};
  for (const std::string& line : lines) {
    Message m;
    std::string err;
    bool ok = Decode(line, m, &err);
    CHECK(ok);
    if (!ok) {
      std::printf("   %s: %s\n", line.c_str(), err.c_str());
      continue;
    }
    seen[m.index()] = true;
    CHECK_STR(Encode(m), line + "\n");
  }
  for (size_t i = 0; i < std::variant_size_v<Message>; i++) CHECK(seen[i]);
}

static void TestMessages() {
  CreateGame cg;
  cg.gameId = 1;
  cg.level = "SM_Map_v5";
  cg.mode = "practice";
  cg.seed = 7919;
  RosterEntry r;
  r.user = 2000000001;
  r.name = "player";
  r.team = 1;
  cg.roster.push_back(r);
  CHECK_STR(Encode(cg),
            "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,"
            "\"roster\":[{\"user\":2000000001,\"name\":\"player\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false}]}\n");
  cg.descriptors = 0x50C;  // D8-live: written after the roster (and the timers), only when not 0
  CHECK(Encode(cg).find("\"bot\":false}],\"descriptors\":1292}\n") != std::string::npos);
  {
    // D3-live step 4: the line gmshost's TestEncodeValues pins for shaper select's flag, timers and content
    CreateGame sc;
    sc.gameId = 1;
    sc.level = "SM_Map_v5";
    sc.mode = "practice";
    sc.seed = 7919;
    sc.shaperSelect = true;
    sc.timers = {{"ShaperSelectionTime", 8000}, {"RevealTime", 16}};
    RosterEntry h = r;
    h.shapers.push_back(RosterEntry::Shaper{"alpha.Noun", 1, 0});
    h.shapers.push_back(RosterEntry::Shaper{"gunman.Noun", 2, 5});
    h.wards.push_back("wardskin_default.Noun");
    sc.roster.push_back(h);
    const std::string line =
        "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,"
        "\"roster\":[{\"user\":2000000001,\"name\":\"player\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false,"
        "\"shapers\":[{\"noun\":\"alpha.Noun\",\"flags\":1},{\"noun\":\"gunman.Noun\",\"flags\":2,\"offer\":5}],"
        "\"wards\":[\"wardskin_default.Noun\"]}],\"timers\":{\"RevealTime\":16,\"ShaperSelectionTime\":8000},"
        "\"shaperSelect\":true}\n";
    CHECK_STR(Encode(sc), line);
    Message back;
    std::string err;
    CHECK(Decode(line, back, &err));
    const CreateGame* d = std::get_if<CreateGame>(&back);
    CHECK(d && d->shaperSelect && d->roster.size() == 1 && d->roster[0].shapers.size() == 2 &&
          d->roster[0].shapers[1].noun == "gunman.Noun" && d->roster[0].shapers[1].flags == 2 &&
          d->roster[0].shapers[1].offer == 5 && d->roster[0].wards.size() == 1 && d->roster[0].skins.empty());
    // a content field of the wrong type is an error
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"skins\":{\"a.Noun\":\"b.Noun\"}}]}", back, &err));
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"shapers\":[{\"noun\":1}]}]}", back, &err));
  }
  {
    // D3-perks: the line gmshost's TestEncodeValues pins for a human's perk pages (after announcers; a page's
    // attributes and modifiers only when not empty)
    CreateGame pc;
    pc.gameId = 1;
    pc.level = "SM_Map_v5";
    pc.mode = "practice";
    pc.seed = 7919;
    pc.shaperSelect = true;
    RosterEntry h = r;
    h.shapers.push_back(RosterEntry::Shaper{"alpha.Noun", 1, 0});
    RosterEntry::PerkPage p1;
    p1.id = 1;
    p1.version = 3;
    p1.attributes.push_back(RosterEntry::PerkAttribute{0, 1, 800});
    p1.attributes.push_back(RosterEntry::PerkAttribute{2, 1, 50000});
    p1.modifiers.push_back("modifier_MediumPerk_BushSpeed");
    RosterEntry::PerkPage p2;
    p2.id = 2;
    p2.version = 1;
    h.perkPages = {p1, p2};
    pc.roster.push_back(h);
    const std::string line =
        "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,"
        "\"roster\":[{\"user\":2000000001,\"name\":\"player\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false,"
        "\"shapers\":[{\"noun\":\"alpha.Noun\",\"flags\":1}],\"perkPages\":[{\"id\":1,\"version\":3,\"attributes\":"
        "[{\"attribute\":0,\"op\":1,\"milli\":800},{\"attribute\":2,\"op\":1,\"milli\":50000}],"
        "\"modifiers\":[\"modifier_MediumPerk_BushSpeed\"]},{\"id\":2,\"version\":1}]}],\"shaperSelect\":true}\n";
    CHECK_STR(Encode(pc), line);
    Message back;
    std::string err;
    CHECK(Decode(line, back, &err));
    const CreateGame* d = std::get_if<CreateGame>(&back);
    CHECK(d && d->roster.size() == 1 && d->roster[0].perkPages.size() == 2 && d->roster[0].perkPages[0].id == 1 &&
          d->roster[0].perkPages[0].version == 3 && d->roster[0].perkPages[0].attributes.size() == 2 &&
          d->roster[0].perkPages[0].attributes[1].attribute == 2 && d->roster[0].perkPages[0].attributes[1].milli == 50000 &&
          d->roster[0].perkPages[0].modifiers.size() == 1 && d->roster[0].perkPages[1].attributes.empty() &&
          d->roster[0].perkPages[1].modifiers.empty());
    // null lists are none; a field of the wrong type is an error
    CHECK(Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"perkPages\":[{\"id\":1,\"attributes\":null,"
                 "\"modifiers\":null}]}]}", back, &err));
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"perkPages\":{\"id\":1}}]}", back, &err));
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"perkPages\":[{\"id\":\"1\"}]}]}", back, &err));
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"perkPages\":[{\"id\":1,\"attributes\":[{\"milli\":1.5}]}]}]}",
                  back, &err));
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"perkPages\":[{\"id\":1,\"modifiers\":[3]}]}]}", back, &err));
  }
  {
    // B10d: a matchmade game with open seats asks for bots up to the team size; the line gmshost's TestEncodeValues and
    // blaze's TestMatchmakingHostedBots pin (fillTeams after shaperSelect, only when not 0)
    CreateGame mm;
    mm.gameId = 1;
    mm.level = "SM_Map_v5";
    mm.mode = "pvp";
    mm.seed = 7919;
    mm.descriptors = 1;
    mm.fillTeams = 5;
    RosterEntry a = r;
    a.name = "alpha";
    mm.roster.push_back(a);
    const std::string line =
        "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"pvp\",\"seed\":7919,"
        "\"roster\":[{\"user\":2000000001,\"name\":\"alpha\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false}],"
        "\"descriptors\":1,\"fillTeams\":5}\n";
    CHECK_STR(Encode(mm), line);
    Message back;
    std::string err;
    CHECK(Decode(line, back, &err));
    const CreateGame* d = std::get_if<CreateGame>(&back);
    CHECK(d && d->fillTeams == 5 && d->descriptors == 1 && d->mode == "pvp" && d->roster.size() == 1);
    // 0 = not asked: not written, and a line without it (every line before B10d) decodes to 0
    mm.fillTeams = 0;
    CHECK(Encode(mm).find("fillTeams") == std::string::npos);
    CHECK(Decode("{\"t\":\"createGame\",\"gameId\":2,\"descriptors\":1}", back, &err) &&
          std::get<CreateGame>(back).fillTeams == 0);
    CHECK(Decode("{\"t\":\"createGame\",\"fillTeams\":null}", back, &err) && std::get<CreateGame>(back).fillTeams == 0);
    // HOLIDAY1: the level flags (last, only when not 0); the line blaze's TestHalloweenLevelFlags pins is in the shared
    // examples file
    mm.levelFlags = 1;
    CHECK(Encode(mm).find(",\"levelFlags\":1}") != std::string::npos);
    CHECK(Decode(Encode(mm), back, &err) && std::get<CreateGame>(back).levelFlags == 1);
    mm.levelFlags = 0;
    CHECK(Encode(mm).find("levelFlags") == std::string::npos);
    CHECK(Decode("{\"t\":\"createGame\",\"gameId\":2}", back, &err) && std::get<CreateGame>(back).levelFlags == 0);
    // D8b: a party's member carries the party (last in the entry, only when not 0); the line blaze's
    // TestMatchmakingHostedParty pins is in the shared examples file
    mm.fillTeams = 2;
    mm.roster[0].party = 1;
    const std::string partyLine =
        "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"pvp\",\"seed\":7919,"
        "\"roster\":[{\"user\":2000000001,\"name\":\"alpha\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false,"
        "\"party\":1}],\"descriptors\":1,\"fillTeams\":2}\n";
    CHECK_STR(Encode(mm), partyLine);
    CHECK(Decode(partyLine, back, &err) && std::get<CreateGame>(back).roster.size() == 1 &&
          std::get<CreateGame>(back).roster[0].party == 1);
    CHECK(Decode(line, back, &err) && std::get<CreateGame>(back).roster[0].party == 0);
    CHECK(!Decode("{\"t\":\"createGame\",\"roster\":[{\"user\":1,\"party\":\"x\"}]}", back, &err));
  }
  PlayerFinish pf;
  pf.user = 1;
  pf.rewards = Rewards{};
  pf.rewards->lootBag = 0x103EBDF7;
  pf.rewards->destiny = 12;
  pf.rewards->items.push_back(RewardItem{0x10A4638F, false, 0});
  pf.rewards->ftue = true;
  pf.rewards->baseRoll = 218;
  pf.rewards->winPoints = 200;
  pf.rewards->winAltPoints = 10;
  // the line gmshost's TestEncodeValues pins for the same values
  CHECK_STR(Encode(pf),
            "{\"t\":\"playerFinish\",\"user\":1,\"rewards\":{\"lootBag\":272547319,\"destiny\":12,\"cc\":0,\"items\":[{\"catalogId\":"
            "279208847,\"duplicate\":false,\"alternate\":0}],\"ftue\":true,\"baseRoll\":218,\"winPoints\":200,\"winAltPoints\":10,"
            "\"firstWinPoints\":0}}\n");
  CHECK_STR(Encode(LogoutGMS{}), "{\"t\":\"logoutGMS\"}\n");
  CHECK_STR(TypeName(GMSUpdate{}), "reportGMSUpdate");

  Message m;
  std::string err;
  // unknown fields ignored, member order free, \r\n tolerated, null = default
  CHECK(Decode("{\"future\":[1,{\"x\":2}],\"port\":7,\"t\":\"gameReady\"}\r\n", m, &err));
  CHECK(std::holds_alternative<GameReady>(m) && std::get<GameReady>(m).port == 7);
  CHECK(Decode("{\"t\":\"reportGMSUpdate\",\"players\":null}", m, &err));
  CHECK(std::holds_alternative<GMSUpdate>(m) && std::get<GMSUpdate>(m).players.empty());
  CHECK(Decode("{\"t\":\"removePlayer\",\"user\":9223372036854775807,\"reason\":\"PLAYER_LEFT\"}", m, &err));
  CHECK(std::get<RemovePlayer>(m).user == INT64_MAX);
  // the Go side writes null for an empty roster
  CHECK(Decode("{\"t\":\"createGame\",\"gameId\":3,\"roster\":null}", m, &err) &&
        std::get<CreateGame>(m).gameId == 3 && std::get<CreateGame>(m).roster.empty() &&
        std::get<CreateGame>(m).descriptors == 0);
  // D8-live: a playerFinish without rewards keeps none; null rewards are none; a playerRewards with Go's null items
  CHECK(Decode("{\"t\":\"playerFinish\",\"user\":4,\"rewards\":null}", m, &err) &&
        !std::get<PlayerFinish>(m).rewards.has_value());
  CHECK(Decode("{\"t\":\"playerRewards\",\"user\":4,\"rewards\":{\"items\":null,\"cc\":7}}", m, &err) &&
        std::get<PlayerRewards>(m).rewards.items.empty() && std::get<PlayerRewards>(m).rewards.cc == 7);
  CHECK_STR(TypeName(PlayerRewards{}), "playerRewards");
  // D8c: playerKarma, and the karma in playerRewards (written only when present; the lines gmshost's TestEncodeValues
  // pins for the same values)
  PlayerKarma pk;
  pk.user = 2000000002;
  pk.team = 1;
  pk.enemy = 2;
  CHECK_STR(Encode(pk), "{\"t\":\"playerKarma\",\"user\":2000000002,\"party\":0,\"team\":1,\"enemy\":2}\n");
  CHECK_STR(TypeName(PlayerKarma{}), "playerKarma");
  PlayerRewards pr;
  pr.user = 2000000002;
  pr.rewards.lootBag = 0x4F079E93;
  pr.rewards.destiny = 24;
  pr.rewards.baseRoll = 218;
  pr.rewards.winAltPoints = 9;
  pr.rewards.karma = Karma{30, 0, 1, 2};
  CHECK_STR(Encode(pr),
            "{\"t\":\"playerRewards\",\"user\":2000000002,\"rewards\":{\"lootBag\":1325899411,\"destiny\":24,\"cc\":0,"
            "\"items\":[],\"ftue\":false,\"baseRoll\":218,\"winPoints\":0,\"winAltPoints\":9,\"firstWinPoints\":0,"
            "\"karma\":{\"points\":30,\"party\":0,\"team\":1,\"enemy\":2}}}\n");
  CHECK(Decode("{\"t\":\"playerRewards\",\"user\":4,\"rewards\":{\"karma\":null}}", m, &err) &&
        !std::get<PlayerRewards>(m).rewards.karma.has_value());
  CHECK(Decode("{\"t\":\"playerRewards\",\"user\":4,\"rewards\":{\"karma\":{\"enemy\":3}}}", m, &err) &&
        std::get<PlayerRewards>(m).rewards.karma.has_value() && std::get<PlayerRewards>(m).rewards.karma->enemy == 3 &&
        std::get<PlayerRewards>(m).rewards.karma->points == 0);
  struct Bad {
    const char* line;
    const char* err;
  };
  for (const Bad& b : {Bad{"{\"t\":\"nope\"}", "unknown message type \"nope\""},
                       Bad{"{\"port\":1}", "line without \"t\""},
                       Bad{"{\"t\":\"gameReady\",\"port\":\"x\"}", "bad gameReady: field \"port\": want an integer"},
                       Bad{"{\"t\":\"gameReady\",\"port\":1.5}", "bad gameReady: field \"port\": want an integer"},
                       Bad{"{\"t\":\"createGame\",\"roster\":[{\"user\":\"u\"}]}", "bad createGame: roster: field \"user\""},
                       Bad{"{\"t\":\"playerFinish\",\"stats\":{\"k\":\"v\"}}", "bad playerFinish: field \"stats\""},
                       Bad{"{\"t\":\"playerFinish\",\"rewards\":[]}", "bad playerFinish: field \"rewards\": want an object"},
                       Bad{"{\"t\":\"playerRewards\",\"rewards\":{\"items\":[{\"catalogId\":\"x\"}]}}",
                           "bad playerRewards: rewards: items: field \"catalogId\""},
                       Bad{"{\"t\":\"createGame\",\"descriptors\":\"0x50C\"}", "bad createGame: field \"descriptors\""},
                       Bad{"{\"t\":\"createGame\",\"fillTeams\":true}", "bad createGame: field \"fillTeams\""},
                       Bad{"{\"t\":\"playerKarma\",\"enemy\":\"1\"}", "bad playerKarma: field \"enemy\""},
                       Bad{"{\"t\":\"playerRewards\",\"rewards\":{\"karma\":[]}}",
                           "bad playerRewards: rewards: field \"karma\""},
                       Bad{"{\"t\":\"playerRewards\",\"rewards\":{\"karma\":{\"points\":true}}}",
                           "bad playerRewards: rewards: karma: field \"points\""},
                       Bad{"[1]", "bad line: not an object"},
                       Bad{"{\"t\":1}", "\"t\" is not a string"},
                       Bad{"{\"t\":\"gameReady\"", "bad line: json:"}}) {
    std::string e;
    bool ok = Decode(b.line, m, &e);
    CHECK(!ok && e.find(b.err) != std::string::npos);
    if (ok || e.find(b.err) == std::string::npos) std::printf("   %s -> %s\n", b.line, e.c_str());
  }
}

// M18 (MSTATS1): seatStats and matchTimeline as gmshost's TestEncodeValues pins them (proto_test.go), decoded back
static void TestStatsMessages() {
  SeatStats s;
  s.index = 3;
  s.user = 2000000046;
  s.team = 1;
  s.noun = 274088913;
  s.role = 2;
  s.stats = {{"timeDeadMs", 41000}, {"damageDealt", 18234}};
  StatSource ab;
  ab.kind = "ability";
  ab.slot = 0;
  ab.damage = 5210;
  StatSource it;
  it.kind = "item";
  it.id = 279208847;
  it.damage = 812;
  it.heal = 3;
  s.sources = {ab, it};
  const std::string sline =
      "{\"t\":\"seatStats\",\"index\":3,\"user\":2000000046,\"bot\":false,\"team\":1,\"noun\":274088913,\"role\":2,"
      "\"stats\":{\"damageDealt\":18234,\"timeDeadMs\":41000},\"sources\":[{\"kind\":\"ability\",\"slot\":0,\"id\":0,"
      "\"damage\":5210,\"heal\":0},{\"kind\":\"item\",\"id\":279208847,\"damage\":812,\"heal\":3}]}\n";
  CHECK_STR(Encode(s), sline);
  Message back;
  std::string err;
  CHECK(Decode(sline, back, &err));
  const SeatStats* d = std::get_if<SeatStats>(&back);
  CHECK(d && d->sources.size() == 2 && d->sources[0].slot && *d->sources[0].slot == 0 && !d->sources[1].slot &&
        d->sources[1].heal == 3 && d->stats.at("timeDeadMs") == 41000);
  SeatStats bot;
  bot.index = 9;
  bot.bot = true;
  bot.team = 2;
  CHECK_STR(Encode(bot), "{\"t\":\"seatStats\",\"index\":9,\"user\":0,\"bot\":true,\"team\":2,\"noun\":0,\"role\":0}\n");

  MatchTimeline tl;
  tl.sec = {60};
  TimelineSeat seat;
  seat.gold = {740};
  seat.xp = {310};
  seat.level = {2};
  seat.cs = {6};
  tl.seats.push_back(seat);
  TimelineTeam team;
  team.team = 1;
  team.gold = {3700};
  team.kills = {0};
  tl.teams.push_back(team);
  tl.events.push_back(TimelineEvent{5000, "kill", -1, 7});
  const std::string tline =
      "{\"t\":\"matchTimeline\",\"stepSec\":60,\"sec\":[60],\"seats\":[{\"index\":0,\"gold\":[740],\"xp\":[310],"
      "\"level\":[2],\"cs\":[6]}],\"teams\":[{\"team\":1,\"gold\":[3700],\"kills\":[0]}],\"events\":[{\"ms\":5000,"
      "\"kind\":\"kill\",\"a\":-1,\"b\":7}]}\n";
  CHECK_STR(Encode(tl), tline);
  CHECK(Decode(tline, back, &err));
  const MatchTimeline* t = std::get_if<MatchTimeline>(&back);
  CHECK(t && t->stepSec == 60 && t->seats.size() == 1 && t->seats[0].cs.size() == 1 && t->events.size() == 1 &&
        t->events[0].a == -1 && t->teams[0].kills.size() == 1);
  CHECK_STR(Encode(MatchTimeline{}), "{\"t\":\"matchTimeline\",\"stepSec\":60}\n");
  // a field of the wrong type is an error
  CHECK(!Decode("{\"t\":\"seatStats\",\"sources\":[{\"kind\":\"basic\",\"slot\":\"q\"}]}", back, &err));
  CHECK(!Decode("{\"t\":\"seatStats\",\"stats\":{\"damageDealt\":1.5}}", back, &err));
  CHECK(!Decode("{\"t\":\"matchTimeline\",\"seats\":[{\"index\":0,\"gold\":[\"x\"]}]}", back, &err));
  CHECK(!Decode("{\"t\":\"matchTimeline\",\"events\":[1]}", back, &err));
}

// ---------------------------------------------------------------------------------------- environment

static void TestEnv() {
  std::string host;
  uint16_t port = 0;
  CHECK(SplitHostPort("127.0.0.1:10040", host, port) && host == "127.0.0.1" && port == 10040);
  for (const char* bad : {"", "127.0.0.1", ":1", "127.0.0.1:0", "127.0.0.1:65536", "localhost:1", "1.2.3:4",
                          "127.0.0.1:x"}) {
    CHECK(!SplitHostPort(bad, host, port));
  }
  HostEnv env;
  std::string err = "x";
  _putenv_s("DGGMS_CONTROL", "");  // an empty value removes the variable
  CHECK(!ReadHostEnv(env, &err) && err.empty());
  _putenv_s("DGGMS_CONTROL", "127.0.0.1:10040");
  _putenv_s("DGGMS_MATCH", "12");
  _putenv_s("DGGMS_TOKEN", "00112233445566778899aabbccddeeff");
  _putenv_s("DGGMS_BIND", "127.0.0.1:10043");
  CHECK(ReadHostEnv(env, &err));
  CHECK(env.control == "127.0.0.1:10040" && env.match == 12 && env.token == "00112233445566778899aabbccddeeff" &&
        env.bind == "127.0.0.1:10043");
  _putenv_s("DGGMS_MATCH", "1x");
  CHECK(!ReadHostEnv(env, &err) && err.find("DGGMS_MATCH") != std::string::npos);
  for (const char* name : {"DGGMS_CONTROL", "DGGMS_MATCH", "DGGMS_TOKEN", "DGGMS_BIND"}) _putenv_s(name, "");
}

// --------------------------------------------------------------------------------------------- client

// A one-connection server on an ephemeral loopback port, standing in for dgserver's control listener.
struct TestServer {
  SOCKET listener = INVALID_SOCKET;
  uint16_t port = 0;

  bool Start() {
    listener = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    sockaddr_in a{};
    a.sin_family = AF_INET;
    inet_pton(AF_INET, "127.0.0.1", &a.sin_addr);
    if (bind(listener, reinterpret_cast<sockaddr*>(&a), sizeof a) != 0 || listen(listener, 1) != 0) return false;
    int len = sizeof a;
    getsockname(listener, reinterpret_cast<sockaddr*>(&a), &len);
    port = ntohs(a.sin_port);
    return true;
  }
  ~TestServer() {
    if (listener != INVALID_SOCKET) closesocket(listener);
  }
};

static std::string RecvLine(SOCKET s) {
  std::string line;
  char c;
  while (recv(s, &c, 1, 0) == 1) {
    if (c == '\n') return line;
    line += c;
  }
  return line;
}

static void SendRaw(SOCKET s, const std::string& data) { send(s, data.data(), static_cast<int>(data.size()), 0); }

// Polls until n messages arrived, the connection closed, or 3 s passed.
static bool PollFor(ControlClient& c, std::vector<Message>& got, std::vector<std::string>& bad, size_t n) {
  bool open = true;
  for (int i = 0; i < 300 && open && got.size() < n; i++) {
    open = c.Poll(got, &bad);
    if (got.size() < n && open) std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  return open;
}

static void TestClient() {
  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);
  {
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 7;
    env.token = "tok";
    env.bind = "127.0.0.1:10042";

    std::string helloLine, readyLine;
    std::atomic<bool> serverDone{false};
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      helloLine = RecvLine(c);
      // createGame split in two packets, then removePlayer and a broken line in one, then the close
      std::string cg = "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,\"roster\":[]}\n";
      SendRaw(c, cg.substr(0, 20));
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
      SendRaw(c, cg.substr(20));
      SendRaw(c, "{\"t\":\"removePlayer\",\"user\":5,\"reason\":\"PLAYER_LEFT\"}\r\n\n{\"t\":\"bogus\"}\n");
      readyLine = RecvLine(c);
      std::this_thread::sleep_for(std::chrono::milliseconds(50));
      closesocket(c);
      serverDone = true;
    });

    ControlClient client;
    std::string err;
    CHECK(client.Connect(env, 4242, "dgsim test", 2000, &err));
    if (!err.empty()) std::printf("   connect: %s\n", err.c_str());
    std::vector<Message> got;
    std::vector<std::string> bad;
    CHECK(PollFor(client, got, bad, 2));
    CHECK_EQ(got.size(), size_t(2));
    if (got.size() == 2) {
      CHECK(std::holds_alternative<CreateGame>(got[0]) && std::get<CreateGame>(got[0]).level == "SM_Map_v5");
      CHECK(std::holds_alternative<RemovePlayer>(got[1]) && std::get<RemovePlayer>(got[1]).user == 5);
    }
    GameReady ready;
    ready.port = 10042;
    CHECK(client.Send(ready, &err));
    got.clear();
    bool open = true;
    for (int i = 0; i < 300 && open; i++) {
      open = client.Poll(got, &bad);
      if (open) std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }
    CHECK(!open && !client.Connected());  // the server closed: Poll says so
    CHECK_EQ(bad.size(), size_t(1));
    if (!bad.empty()) CHECK(bad[0].find("unknown message type \"bogus\"") != std::string::npos);
    server.join();
    CHECK_STR(helloLine, "{\"t\":\"hello\",\"match\":7,\"token\":\"tok\",\"pid\":4242,\"build\":\"dgsim test\"}");
    CHECK_STR(readyLine, "{\"t\":\"gameReady\",\"port\":10042}");
    CHECK(!client.Send(LogoutGMS{}, &err));  // closed
  }
  {
    // nothing listens: connect fails (the port was just released)
    TestServer srv;
    CHECK(srv.Start());
    uint16_t port = srv.port;
    closesocket(srv.listener);
    srv.listener = INVALID_SOCKET;
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(port);
    ControlClient client;
    std::string err;
    CHECK(!client.Connect(env, 1, "x", 1000, &err) && !err.empty());
    CHECK(!client.Connected());
  }
  WSACleanup();
}

// ---------------------------------------------------------------------------------------- HostControl (C1b)

static bool PollUntil(HostControl& h, int64_t& now, const std::function<bool()>& done) {
  for (int i = 0; i < 300; i++) {
    h.Poll(now);
    if (done()) return true;
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
    now += 10;
  }
  return false;
}

static void TestHostControl() {
  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);
  {
    // the whole lifecycle: hello, createGame with a roster, gameReady, addPlayer, phases, the heartbeat,
    // removePlayer, playerRewards (D8-live: taken; before, a bad line), a bad line, the results, abortGame, logoutGMS
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 3;
    env.token = "tok";
    env.bind = "127.0.0.1:10043";
    std::vector<std::string> lines;  // what the server received, in order
    std::atomic<bool> sendAbort{false};
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      lines.push_back(RecvLine(c));  // hello
      SendRaw(c, "{\"t\":\"createGame\",\"gameId\":9,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":77,"
                 "\"roster\":[{\"user\":1000000046,\"name\":\"p\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false}]}\n");
      for (int i = 0; i < 4; i++) lines.push_back(RecvLine(c));  // gameReady, addPlayer, preGame, inGame
      lines.push_back(RecvLine(c));                             // a heartbeat
      SendRaw(c, "{\"t\":\"removePlayer\",\"user\":1000000046,\"reason\":\"PLAYER_LEFT\"}\n"
                 "{\"t\":\"playerRewards\",\"user\":1000000046,\"rewards\":{\"lootBag\":1325899411,\"destiny\":5,"
                 "\"items\":null,\"ftue\":true}}\n"
                 "{\"t\":\"futureMessage\"}\n");
      for (int i = 0; i < 4; i++) lines.push_back(RecvLine(c));  // disconnectPlayer, postGame, playerFinish, finishGame
      while (!sendAbort) std::this_thread::sleep_for(std::chrono::milliseconds(5));
      SendRaw(c, "{\"t\":\"abortGame\",\"reason\":\"no players left\"}\n");
      lines.push_back(RecvLine(c));  // logoutGMS
      lines.push_back(RecvLine(c));  // "" (closed)
      closesocket(c);
    });
    std::vector<std::string> log;
    HostControl::Options o;
    o.heartbeatMs = 100;
    HostControl h([&](const std::string& s) { log.push_back(s); }, o);
    int64_t now = 1000;
    std::string err;
    CHECK(h.Start(env, 4242, "test", now, &err));
    CHECK(PollUntil(h, now, [&] { return h.HasGame(); }));
    CHECK_EQ(h.Game().gameId, int64_t(9));
    CHECK_EQ(h.Game().seed, int64_t(77));
    CHECK(h.FindUser(1000000046) != nullptr && h.FindUser(1000000046)->team == 1);
    CHECK(h.FindUser(5) == nullptr);
    CHECK_STR(h.Phase(), "loading");
    h.Ready(10043);
    h.PlayerAdded(1000000046, 0);
    h.SetPhase(kPhasePreGame, now);
    h.SetPhase(kPhasePreGame, now);  // no change: nothing sent
    h.SetPhase(kPhaseInGame, now + 5);
    std::vector<RemovePlayer> removed;
    CHECK(PollUntil(h, now, [&] {
      auto r = h.TakeRemoved();
      removed.insert(removed.end(), r.begin(), r.end());
      return !removed.empty();
    }));
    CHECK(removed.size() == 1 && removed[0].reason == "PLAYER_LEFT");
    std::vector<PlayerRewards> rewards;
    CHECK(PollUntil(h, now, [&] {
      auto r = h.TakeRewards();
      rewards.insert(rewards.end(), r.begin(), r.end());
      return !rewards.empty();
    }));
    CHECK(rewards.size() == 1 && rewards[0].user == 1000000046 && rewards[0].rewards.lootBag == 1325899411 &&
          rewards[0].rewards.destiny == 5 && rewards[0].rewards.items.empty() && rewards[0].rewards.ftue);
    CHECK(h.TakeRewards().empty());
    h.PlayerGone(1000000046, "disconnected");
    CHECK(h.Players().empty());
    h.SetPhase(kPhasePostGame, now);
    PlayerFinish pf;
    pf.user = 1000000046;
    pf.stats["kills"] = 2;
    h.SendPlayerFinish(pf);
    FinishGame fin;
    fin.winnerTeam = 1;
    fin.durationMs = 1234;
    fin.reason = "guardian";
    h.SendFinish(fin);
    sendAbort = true;
    CHECK(PollUntil(h, now, [&] { return h.AbortRequested(); }));
    CHECK_STR(h.AbortReason(), "no players left");
    CHECK(!h.Lost());
    h.Logout();
    CHECK(!h.Active() && !h.Lost());
    server.join();
    CHECK_EQ(lines.size(), size_t(12));
    if (lines.size() == 12) {
      CHECK_STR(lines[0], "{\"t\":\"hello\",\"match\":3,\"token\":\"tok\",\"pid\":4242,\"build\":\"test\"}");
      CHECK_STR(lines[1], "{\"t\":\"gameReady\",\"port\":10043}");
      CHECK_STR(lines[2], "{\"t\":\"addPlayer\",\"user\":1000000046,\"index\":0}");
      CHECK(lines[3].find("\"phase\":\"preGame\"") != std::string::npos &&
            lines[3].find("\"players\":[1000000046]") != std::string::npos);
      CHECK(lines[4].find("\"phase\":\"inGame\",\"elapsedMs\":") != std::string::npos);
      CHECK(lines[5].find("\"t\":\"reportGMSUpdate\",\"phase\":\"inGame\"") != std::string::npos);  // heartbeat
      CHECK_STR(lines[6], "{\"t\":\"disconnectPlayer\",\"user\":1000000046,\"reason\":\"disconnected\"}");
      CHECK(lines[7].find("\"phase\":\"postGame\"") != std::string::npos && lines[7].find("\"players\":[]") != std::string::npos);
      CHECK_STR(lines[8], "{\"t\":\"playerFinish\",\"user\":1000000046,\"stats\":{\"kills\":2}}");
      CHECK_STR(lines[9], "{\"t\":\"finishGame\",\"winnerTeam\":1,\"durationMs\":1234,\"reason\":\"guardian\"}");
      CHECK_STR(lines[10], "{\"t\":\"logoutGMS\"}");
      CHECK_STR(lines[11], "");
    }
    bool sawBad = false, sawToken = false;
    for (const std::string& l : log) {
      sawBad = sawBad || l.find("line ignored: unknown message type \"futureMessage\"") != std::string::npos;
      sawToken = sawToken || l.find("tok\"") != std::string::npos;
    }
    CHECK(sawBad);
    CHECK(!sawToken);  // the token never reaches the log
    CHECK(h.Describe().find("logged out") != std::string::npos);
  }
  {
    // the server closes the connection: Lost() (the host must exit)
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 1;
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      RecvLine(c);
      closesocket(c);
    });
    HostControl h;
    int64_t now = 0;
    CHECK(h.Start(env, 1, "x", now));
    CHECK(PollUntil(h, now, [&] { return h.Lost(); }));
    CHECK(!h.HasGame() && !h.Active());
    server.join();
  }
  {
    // nothing listens: Start fails
    TestServer srv;
    CHECK(srv.Start());
    uint16_t port = srv.port;
    closesocket(srv.listener);
    srv.listener = INVALID_SOCKET;
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(port);
    HostControl h;
    std::string err;
    CHECK(!h.Start(env, 1, "x", 0, &err) && !err.empty());
  }
  WSACleanup();
}

// D8c: a matchmade game's karma round trip. createGame (pvp, descriptors 1, two players) ... finishGame; once the
// post-game's vote has closed the host sends playerKarma per player; the server answers the loser's with the
// karma-aware playerRewards, which is the example line dgserver itself sends for that game (blaze's
// TestHostedMatchmadeKarma pins it: roll 379 + 18 match-time points + 10 karma points for one enemy vote = a bag score
// of 407, the fixture's Silverish bag, which 397 without the karma doesn't reach); HostControl hands it over with the
// karma.
static void TestHostControlMatchmadeKarma(const std::string& examplesPath) {
  std::string rewardsLine, karmaLine;  // from the shared examples
  for (const std::string& l : ReadExamples(examplesPath)) {
    const bool rewards = l.find("\"t\":\"playerRewards\"") != std::string::npos;
    if (rewards && l.find("\"karma\":") != std::string::npos) rewardsLine = l;
    if (l.find("\"t\":\"playerKarma\"") != std::string::npos) karmaLine = l;
  }
  CHECK(!rewardsLine.empty() && !karmaLine.empty());
  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);
  {
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 4;
    env.token = "tok";
    env.bind = "127.0.0.1:10044";
    std::vector<std::string> lines;  // what the server received, in order
    std::atomic<bool> sendAbort{false};
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      lines.push_back(RecvLine(c));  // hello
      SendRaw(c, "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"pvp\",\"seed\":7919,"
                 "\"roster\":["
                 "{\"user\":2000000001,\"name\":\"alpha\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false},"
                 "{\"user\":2000000002,\"name\":\"bravo\",\"index\":1,\"team\":2,\"noun\":\"\",\"bot\":false}],"
                 "\"descriptors\":1}\n");
      // gameReady, addPlayer x2, postGame, playerFinish x2, finishGame: no rewards yet (they wait for the karma)
      for (int i = 0; i < 7; i++) lines.push_back(RecvLine(c));
      lines.push_back(RecvLine(c));  // playerKarma for 2000000001
      SendRaw(c, rewardsLine + "\n");
      lines.push_back(RecvLine(c));  // playerKarma for 2000000002 (dgserver: the winner's rewards; not needed here)
      while (!sendAbort) std::this_thread::sleep_for(std::chrono::milliseconds(5));
      SendRaw(c, "{\"t\":\"abortGame\",\"reason\":\"no players left\"}\n");
      lines.push_back(RecvLine(c));  // logoutGMS
      closesocket(c);
    });
    HostControl::Options o;
    o.heartbeatMs = 1000000;  // no heartbeat between the lines counted below
    HostControl h(nullptr, o);
    int64_t now = 0;
    CHECK(h.Start(env, 4243, "test", now));
    CHECK(PollUntil(h, now, [&] { return h.HasGame(); }));
    CHECK_STR(h.Game().mode, "pvp");
    CHECK_EQ(h.Game().descriptors, int64_t(1));
    CHECK_EQ(h.Game().roster.size(), size_t(2));
    h.Ready(10044);
    h.PlayerAdded(2000000001, 0);
    h.PlayerAdded(2000000002, 1);
    h.SetPhase(kPhasePostGame, now);
    for (int64_t user : {2000000001LL, 2000000002LL}) {
      PlayerFinish pf;
      pf.user = user;
      h.SendPlayerFinish(pf);
    }
    FinishGame fin;
    fin.winnerTeam = 2;
    fin.durationMs = 1200000;
    fin.reason = "guardian";
    h.SendFinish(fin);
    // the vote closed (postgame::PostGame::TakeKarmaReports): the loser got one enemy vote, the winner one too
    h.SendPlayerKarma(PlayerKarma{2000000001, 0, 0, 1});
    h.SendPlayerKarma(PlayerKarma{2000000002, 0, 0, 1});
    std::vector<PlayerRewards> rewards;
    CHECK(PollUntil(h, now, [&] {
      auto r = h.TakeRewards();
      rewards.insert(rewards.end(), r.begin(), r.end());
      return !rewards.empty();
    }));
    CHECK_EQ(rewards.size(), size_t(1));
    if (!rewards.empty()) {
      const Rewards& r = rewards[0].rewards;
      CHECK_EQ(rewards[0].user, int64_t(2000000001));
      CHECK_EQ(r.lootBag, int64_t(2079898500));  // the fixture's Silverish (RequiredBagScore 400)
      CHECK(r.karma.has_value());
      if (r.karma) {
        CHECK(r.karma->points == 10 && r.karma->party == 0 && r.karma->team == 0 && r.karma->enemy == 1);
        CHECK_EQ(r.baseRoll + r.winPoints + r.winAltPoints + r.firstWinPoints + r.karma->points, int64_t(407));
      }
      CHECK(r.items.size() == 2 && r.items[0].duplicate && r.cc == 30 && !r.ftue);
      CHECK_STR(Encode(rewards[0]), rewardsLine + "\n");  // nothing lost on the way
    }
    sendAbort = true;
    CHECK(PollUntil(h, now, [&] { return h.AbortRequested(); }));
    h.Logout();
    server.join();
    CHECK_EQ(lines.size(), size_t(11));
    if (lines.size() == 11) {
      CHECK(lines[4].find("\"phase\":\"postGame\"") != std::string::npos);
      CHECK_STR(lines[7], "{\"t\":\"finishGame\",\"winnerTeam\":2,\"durationMs\":1200000,\"reason\":\"guardian\"}");
      CHECK_STR(lines[8], karmaLine);  // the example line
      CHECK_STR(lines[9], "{\"t\":\"playerKarma\",\"user\":2000000002,\"party\":0,\"team\":0,\"enemy\":1}");
      CHECK_STR(lines[10], "{\"t\":\"logoutGMS\"}");
    }
  }
  WSACleanup();
}

// H5: the frozen host's reports. reportGMSError's codes and names (the same on the Go side); a host that reports a
// step exception (not fatal) and then a stalled tick (fatal) sends exactly the example lines (blaze's
// TestHostedGameStalls sends the same from its fake host), then logoutGMS, and counts both reports.
static void TestHostControlErrors(const std::string& examplesPath) {
  CHECK_STR(ErrorCodeName(kErrGmsEndpoint), "GMS endpoint");
  CHECK_STR(ErrorCodeName(kErrTickStalled), "tick stalled");
  CHECK_STR(ErrorCodeName(kErrStepFailing), "step failing");
  CHECK_STR(ErrorCodeName(kErrStepException), "step exception");
  CHECK_STR(ErrorCodeName(kErrLevelLoad), "level load");
  CHECK_STR(ErrorCodeName(0), "error");
  CHECK_STR(ErrorCodeName(99), "error");
  CHECK_EQ(kErrGmsEndpoint, int64_t(1));
  CHECK_EQ(kErrTickStalled, int64_t(2));
  CHECK_EQ(kErrStepFailing, int64_t(3));
  CHECK_EQ(kErrStepException, int64_t(4));
  CHECK_EQ(kErrLevelLoad, int64_t(5));
  std::string stallLine, stepLine;  // from the shared examples
  for (const std::string& l : ReadExamples(examplesPath)) {
    if (l.find("\"t\":\"reportGMSError\",\"code\":2,") != std::string::npos) stallLine = l;
    if (l.find("\"t\":\"reportGMSError\",\"code\":4,") != std::string::npos) stepLine = l;
  }
  CHECK(!stallLine.empty() && !stepLine.empty());
  Message m;
  std::string err;
  CHECK(Decode(stallLine, m, &err));
  const GMSError* stall = std::get_if<GMSError>(&m);
  CHECK(stall && stall->code == kErrTickStalled && stall->fatal);
  const GMSError stalled = stall ? *stall : GMSError{};
  CHECK(Decode(stepLine, m, &err));
  const GMSError* step = std::get_if<GMSError>(&m);
  CHECK(step && step->code == kErrStepException && !step->fatal);
  const GMSError stepped = step ? *step : GMSError{};

  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);
  {
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 5;
    env.token = "tok";
    env.bind = "127.0.0.1:10045";
    std::vector<std::string> lines;
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      lines.push_back(RecvLine(c));  // hello
      SendRaw(c, "{\"t\":\"createGame\",\"gameId\":1,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":1,"
                 "\"roster\":[{\"user\":2000000001,\"name\":\"p\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false}]}\n");
      for (int i = 0; i < 5; i++) lines.push_back(RecvLine(c));  // gameReady, the two reports, logoutGMS, "" (closed)
      closesocket(c);
    });
    HostControl h;
    int64_t now = 0;
    CHECK(h.Start(env, 77, "test", now, &err));
    CHECK(PollUntil(h, now, [&] { return h.HasGame(); }));
    h.Ready(10045);
    CHECK(h.SendError(stepped.code, stepped.text, stepped.fatal));
    CHECK(h.SendError(stalled.code, stalled.text, stalled.fatal));
    CHECK_EQ(h.ErrorsSent(), 2);
    CHECK(h.Describe().find(", errors reported 2") != std::string::npos);
    h.Logout();
    CHECK(!h.SendError(kErrTickStalled, "after the logout", true));  // nothing sent once logged out
    CHECK_EQ(h.ErrorsSent(), 2);
    server.join();
    CHECK_EQ(lines.size(), size_t(6));
    if (lines.size() == 6) {
      CHECK_STR(lines[1], "{\"t\":\"gameReady\",\"port\":10045}");
      CHECK_STR(lines[2], stepLine);
      CHECK_STR(lines[3], stallLine);
      CHECK_STR(lines[4], "{\"t\":\"logoutGMS\"}");
      CHECK_STR(lines[5], "");
    }
  }
  WSACleanup();
}

// B15b: a started custom game's roster after an updateRoster: startGame's players stay as they were (a changed team or
// index is ignored), the update's spectators replace the old ones (one came, one left), and a spectator entry for a
// user who plays is dropped.
static RosterEntry Entry(int64_t user, int64_t index, int64_t team, bool spectator) {
  RosterEntry e;
  e.user = user;
  e.name = "p" + std::to_string(user);
  e.index = index;
  e.team = team;
  e.spectator = spectator;
  return e;
}
static void TestWithSpectatorsOf() {
  const std::vector<RosterEntry> fixed = {Entry(1, 0, 1, false), Entry(2, 5, 2, false), Entry(3, 10, 0, true),
                                          Entry(4, 11, 0, true)};
  // 1 moved to team 2 (ignored), 3 left, 5 came, and a spectator entry for player 2
  const std::vector<RosterEntry> update = {Entry(1, 5, 2, false), Entry(2, 6, 2, false), Entry(4, 10, 0, true),
                                           Entry(5, 11, 0, true), Entry(2, 12, 0, true)};
  const std::vector<RosterEntry> got = WithSpectatorsOf(fixed, update);
  CHECK_EQ(got.size(), size_t{4});
  if (got.size() == 4) {
    CHECK(got[0].user == 1 && got[0].index == 0 && got[0].team == 1 && !got[0].spectator);
    CHECK(got[1].user == 2 && got[1].index == 5 && got[1].team == 2 && !got[1].spectator);
    CHECK(got[2].user == 4 && got[2].index == 10 && got[2].spectator);
    CHECK(got[3].user == 5 && got[3].index == 11 && got[3].spectator);
  }
  CHECK(WithSpectatorsOf(fixed, {}).size() == 2);  // every spectator left
}

// B15: a custom game's lobby. createGame (mode custom, the creator alone: the example line dgserver sends) puts the
// host in its lobby (InLobby: HelloReqs held); updateRoster lets a joiner in (with its provisional slot); startGame
// ends the lobby with the final roster (the joiner now a spectator at index 10). Another startGame after the start is
// ignored; B15b: an updateRoster after it (a spectator of the running game, delta at 11) changes only the spectators.
// The four lines are the shared examples (blaze's TestCustomGameHosted sends them).
static void TestHostControlCustomLobby(const std::string& examplesPath) {
  std::string createLine, updateLine, startLine, lateLine;
  for (const std::string& l : ReadExamples(examplesPath)) {
    if (l.find("\"mode\":\"custom\"") != std::string::npos) createLine = l;
    if (l.find("\"t\":\"updateRoster\"") != std::string::npos) {
      if (l.find("\"spectator\":true") == std::string::npos) updateLine = l;
      else lateLine = l;  // B15b: after the start
    }
    if (l.find("\"t\":\"startGame\"") != std::string::npos) startLine = l;
  }
  CHECK(!createLine.empty() && !updateLine.empty() && !startLine.empty() && !lateLine.empty());
  Message m;
  CHECK(Decode(startLine, m) && std::get<StartGame>(m).roster.size() == 2 && std::get<StartGame>(m).roster[1].spectator);
  CHECK_STR(TypeName(UpdateRoster{}), "updateRoster");
  CHECK_STR(TypeName(StartGame{}), "startGame");
  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);
  {
    TestServer srv;
    CHECK(srv.Start());
    HostEnv env;
    env.control = "127.0.0.1:" + std::to_string(srv.port);
    env.match = 5;
    env.token = "tok";
    std::atomic<int> step{0};
    std::thread server([&] {
      SOCKET c = accept(srv.listener, nullptr, nullptr);
      RecvLine(c);  // hello
      SendRaw(c, createLine + "\n");
      while (step < 1) std::this_thread::sleep_for(std::chrono::milliseconds(5));
      SendRaw(c, updateLine + "\n");
      while (step < 2) std::this_thread::sleep_for(std::chrono::milliseconds(5));
      SendRaw(c, startLine + "\n");
      SendRaw(c, startLine + "\n");   // a second start: ignored
      SendRaw(c, updateLine + "\n");  // after the start, B15b: only its spectators count (it has none: bravo leaves)
      SendRaw(c, lateLine + "\n");    // a spectator of the running game: bravo again at 10, delta at 11
      SendRaw(c, "{\"t\":\"abortGame\",\"reason\":\"done\"}\n");
      RecvLine(c);  // logoutGMS
      closesocket(c);
    });
    HostControl::Options o;
    o.heartbeatMs = 1000000;
    HostControl h(nullptr, o);
    int64_t now = 0;
    CHECK(h.Start(env, 4244, "test", now));
    CHECK(PollUntil(h, now, [&] { return h.HasGame(); }));
    CHECK(h.InLobby() && !h.Started());
    CHECK(h.FindUser(2000000001) != nullptr && h.FindUser(2000000002) == nullptr);
    step = 1;
    CHECK(PollUntil(h, now, [&] { return h.FindUser(2000000002) != nullptr; }));
    CHECK(h.FindUser(2000000002) && h.FindUser(2000000002)->index == 5 && h.FindUser(2000000002)->team == 2);
    CHECK(h.InLobby());
    step = 2;
    CHECK(PollUntil(h, now, [&] { return h.AbortRequested(); }));
    CHECK(!h.InLobby() && h.Started());
    const RosterEntry* b = h.FindUser(2000000002);
    CHECK(b && b->index == 10 && b->team == 0 && b->spectator);
    const RosterEntry* d = h.FindUser(2000000004);  // B15b: the late spectator
    CHECK(d && d->index == 11 && d->team == 0 && d->spectator);
    const RosterEntry* a = h.FindUser(2000000001);  // the player as startGame had it
    CHECK(a && a->index == 0 && a->team == 1 && !a->spectator);
    CHECK_EQ(h.Game().roster.size(), size_t{3});
    h.Logout();
    server.join();
  }
  WSACleanup();
}

int main(int argc, char** argv) {
  std::string examples = argc > 1 ? argv[1] : DG_CONTROL_EXAMPLES;
  TestJsonParse();
  TestJsonWrite();
  TestExamples(examples);
  TestMessages();
  TestStatsMessages();  // M18
  TestEnv();
  TestClient();
  TestHostControl();
  TestHostControlMatchmadeKarma(examples);
  TestHostControlErrors(examples);
  TestHostControlCustomLobby(examples);  // B15
  TestWithSpectatorsOf();                // B15b
  std::printf("%d checks, %d failed\n", g_checks, g_failed);
  return g_failed == 0 ? 0 : 1;
}
