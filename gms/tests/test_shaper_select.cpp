// D3-core: shaper select without the game (docs/GMS_PROTOCOL.md *Shaper select (D2c, D3-static)*, *Test cases
// (D3-live)*). The builders against script B's bytes from re/scripts/shaper_select.py (O1), and the match-wide
// ShaperSelect driven with a synthetic server clock and a recording sender: script B byte for byte (O2's exact part),
// invalid requests (O3), the timers with a silent client (O4), two teams (O5). The session over RakNet with the test
// client model (O2, O4 on loopback) is in test_session.cpp.
#include <cstdio>
#include <map>
#include <string>
#include <utility>
#include <variant>
#include <vector>

#include "core/common/bytes.h"
#include "core/common/log.h"
#include "core/protocol/messages.h"
#include "core/protocol/shaper_select.h"
#include "core/session/match.h"
#include "core/session/shaper_select.h"
#include "control/messages.h"
#include "script/roster_shaper.h"
#include "script/scripted_match.h"
#include "tests/check.h"

using gms::Bytes;

namespace {

// ---- script B's values (shaper_select.py: LIST, TEST_TIMERS, the ally) ------------------------------------------------
const uint32_t kAlpha = 0xEF044AAB;
const uint32_t kArtificer = 0x5DA8B09E;
const uint32_t kGunman = 0x7C1D1E27;

// The session logs through gms::Logf; keep the lines and print them only when a test failed.
struct QuietLog {
  int failuresBefore = gmstest::Failures();
  static std::vector<std::string>& Lines() {
    static std::vector<std::string> lines;
    return lines;
  }
  static void Sink(const std::string& line) { Lines().push_back(line); }
  QuietLog() {
    Lines().clear();
    gms::SetLogSink(Sink);
  }
  ~QuietLog() {
    gms::SetLogSink(nullptr);
    if (gmstest::Failures() != failuresBefore)
      for (const std::string& l : Lines()) std::printf("  | %s\n", l.c_str());
  }
  bool Has(const std::string& text) const {
    for (const std::string& l : Lines())
      if (l.find(text) != std::string::npos) return true;
    return false;
  }
};

// Records every send: (connection, hex, quiet).
struct Sent {
  gms::ConnId conn;
  std::string hex;
  bool quiet;
};
struct RecordingSender final : gms::ISender {
  std::vector<Sent> sent;
  bool Send(gms::ConnId conn, const Bytes& msg, const std::string&) override {
    sent.push_back({conn, gms::Hex(msg, static_cast<size_t>(-1)), false});
    return true;
  }
  bool SendQuiet(gms::ConnId conn, const Bytes& msg) override {
    sent.push_back({conn, gms::Hex(msg, static_cast<size_t>(-1)), true});
    return true;
  }
  // The logged sends to `conn` from position `from` on (the quiet one-second repeats left out).
  std::vector<std::string> To(gms::ConnId conn, size_t from = 0) const {
    std::vector<std::string> v;
    for (size_t i = from; i < sent.size(); ++i)
      if (sent[i].conn == conn && !sent[i].quiet) v.push_back(sent[i].hex);
    return v;
  }
  size_t Quiet() const {
    size_t n = 0;
    for (const Sent& s : sent) n += s.quiet ? 1 : 0;
    return n;
  }
};

void CheckList(const std::vector<std::string>& got, const std::vector<std::string>& want, int line) {
  if (got.size() != want.size())
    gmstest::Fail(__FILE__, line, "count: got " + std::to_string(got.size()) + ", want " + std::to_string(want.size()));
  for (size_t i = 0; i < got.size() && i < want.size(); ++i) {
    if (got[i] != want[i]) {
      gmstest::Fail(__FILE__, line, "message " + std::to_string(i) + ": got " + got[i] + ", want " + want[i]);
      return;
    }
  }
  if (got.size() != want.size())
    for (const std::string& g : got) std::printf("    got %s\n", g.c_str());
}
#define CHECK_LIST(got, ...) CheckList((got), std::vector<std::string>(__VA_ARGS__), __LINE__)

Bytes Req(uint8_t sub, uint32_t id = 0, uint8_t role = 0, uint8_t kind = 0) {
  gms::ShaperSelectRequest r;
  r.sub = sub;
  r.id = id;
  r.role = role;
  r.kind = kind;
  const Bytes m = gms::BuildShaperSelectRequest(r);
  return Bytes(m.begin() + 1, m.end());  // the body after the wire byte, as the session hands it on
}

void Msg(gms::ShaperSelect& ss, gms::ConnId conn, const Bytes& body, int64_t ms, gms::ISender& out) {
  ss.OnMessage(conn, body.data(), body.size(), ms, out);
}

gms::ShaperListEntry Entry(uint32_t noun, uint8_t flags, uint32_t offer = 0) {
  gms::ShaperListEntry e;
  e.noun = noun;
  e.flags = flags;
  e.offer = offer;
  return e;
}

gms::ShaperSelectTimers Timers(uint32_t a, uint32_t b, uint32_t c, uint32_t d) {
  gms::ShaperSelectTimers t;
  t.pickMs = a;
  t.rolePerkMs = b;
  t.skinMs = c;
  t.revealMs = d;
  return t;
}

gms::ShaperSelectPlayer Scripted(uint8_t index, uint8_t team, uint32_t noun, int pickMs, int lockMs, uint8_t role) {
  gms::ShaperSelectPlayer p;
  p.gameplayIndex = index;
  p.team = team;
  p.scripted = true;
  p.plan.noun = noun;
  p.plan.pickMs = pickMs;
  p.plan.lockMs = lockMs;
  p.plan.role = role;
  return p;
}

std::string Zeros(size_t bytes) { return std::string(bytes * 2, '0'); }

}  // namespace

// ---- O1: the builders against script B (shaper_select.py script-b, s = 10000) ----------------------------------------
GMS_TEST(ShaperSelectBuildersMatchScriptB) {
  CHECK_EQ(gms::AssetIdOf("alpha.Noun"), kAlpha);
  CHECK_EQ(gms::AssetIdOf("artificer.Noun"), kArtificer);
  CHECK_EQ(gms::AssetIdOf("gunman.Noun"), kGunman);

  // 1: HelloPlayer, me and the ally
  gms::HelloPlayerRecord me, ally;
  ally.type = 1;
  ally.gameplayIndex = 1;
  CHECK_HEX(gms::BuildHelloPlayer({me, ally}), "8200007f000001273a01017f000001273a");
  // 2, 2a: the first updates (the human without status and shaper; the ally accepted, bot, online id 1)
  CHECK_HEX(gms::BuildPlayerUpdate(0, 0, 1, 0, 2000000001ull), "a900320100010194357700000000");
  gms::PlayerGlobalValues v;
  v.status = 0x10000;
  v.team = 1;
  v.isBot = 1;
  v.onlineId = 1;
  CHECK_HEX(gms::BuildPlayerGlobalUpdate(1, {0, 8, 11, 17}, v), "a9013209010000010001010100000000000000");
  // 9: the human's list
  CHECK_HEX(gms::BuildShaperList({Entry(kAlpha, 1), Entry(kArtificer, 1), Entry(kGunman, 2)}),
            "c802ab4a04ef0100000000009eb0a85d010000000000271e1d7c020000000000");
  CHECK(gms::BuildShaperList({}).empty());
  // 10, 12, 13, 15, 17, 22: records
  gms::ShaperRecord r;
  CHECK_HEX(gms::BuildShaperRecord(r), "c80300" + Zeros(14));
  r.index = 1;
  r.shaper = r.skin = kArtificer;
  CHECK_HEX(gms::BuildShaperRecord(r), "c803019eb0a85d9eb0a85d000000000000");
  r.flags = gms::kRecordShaperLocked;
  CHECK_HEX(gms::BuildShaperRecord(r), "c803019eb0a85d9eb0a85d000000000001");
  r.role = 1;
  r.flags = gms::kRecordShaperLocked | gms::kRecordRoleLocked;
  CHECK_HEX(gms::BuildShaperRecord(r), "c803019eb0a85d9eb0a85d010000000003");
  gms::ShaperRecord back;
  const Bytes rb = gms::BuildShaperRecord(r);
  CHECK(gms::ParseShaperRecord(rb.data() + 2, rb.size() - 2, &back));
  CHECK(back.index == 1 && back.shaper == kArtificer && back.skin == kArtificer && back.role == 1 && back.flags == 3);
  // 11, 20, 23, 25: phases
  CHECK_HEX(gms::BuildShaperPhase(40000, 1), "c801409c00000000000001000000");
  CHECK_HEX(gms::BuildShaperPhase(46000, 2), "c801b0b300000000000002000000");
  CHECK_HEX(gms::BuildShaperPhase(29000, 3), "c801487100000000000003000000");
  CHECK_HEX(gms::BuildShaperPhase(29016, 4), "c801587100000000000004000000");
  // 15, 17: the answers and the skins
  CHECK_HEX(gms::BuildShaperRequestResult(kAlpha, true), "c804ab4a04ef01");
  CHECK_HEX(gms::BuildShaperRequestResult(kAlpha, false), "c804ab4a04ef00");
  CHECK_HEX(gms::BuildSkins({kAlpha}), "c80b01ab4a04ef00");
  CHECK(gms::BuildSkins({}).empty());
  CHECK_HEX(gms::BuildShaperLockResult(kAlpha), "c805ab4a04ef01");
  // 26: the reveal's updates (members 0, 9, 16), via the pick helpers too
  gms::ShaperPick k;
  k.gameplayIndex = 0;
  k.status = 0x10180;
  k.role = 0;
  k.shaper = k.skin = kAlpha;
  CHECK_HEX(gms::BuildPlayerGlobalUpdate(0, gms::RevealMembers(k), gms::RevealValues(k)), "a9003102018001010000ab4a04ef");
  k.gameplayIndex = 1;
  k.status = 0x10100;
  k.role = 1;
  k.shaper = k.skin = kArtificer;
  CHECK_HEX(gms::BuildPlayerGlobalUpdate(1, gms::RevealMembers(k), gms::RevealValues(k)), "a90131020100010100019eb0a85d");
  // 28a, 30: the "loaded" updates are status echoes
  v = gms::PlayerGlobalValues();
  v.status = 0x10500;
  v.progress = 1.0f;
  CHECK_HEX(gms::BuildPlayerGlobalUpdate(1, gms::PlayerStatusEchoMembers(), v), "a90113000501000000803f");
  CHECK_HEX(gms::BuildPlayerStatusEcho(0, 0x10581, 1.0f), "a90013810501000000803f");
  // the general writer gives the session's bytes for script A's update and echo
  v = gms::PlayerGlobalValues();
  v.status = 0x10;
  v.team = 1;
  v.noun = kAlpha;
  v.onlineId = 2000000011ull;
  CHECK_EQ(gms::Hex(gms::BuildPlayerGlobalUpdate(0, gms::PlayerUpdateMembers(0x10, kAlpha), v), 99),
           gms::Hex(gms::BuildPlayerUpdate(0, 0x10, 1, kAlpha, 2000000011ull), 99));
  // shaper_select.py selftest: a member-13/14 update (mStatus 0x10181, role 2, loadout 7 version 3, alpha.Noun) parses back
  v = gms::PlayerGlobalValues();
  v.status = 0x10181;
  v.role = 2;
  v.loadoutId = 7;
  v.loadoutVersion = 3;
  v.noun = kAlpha;
  const Bytes withPage = gms::BuildPlayerGlobalUpdate(0, {0, 9, 13, 14, 16}, v);
  uint8_t idx = 9;
  std::vector<int> members;
  gms::PlayerGlobalValues got;
  CHECK(gms::ParsePlayerGlobalUpdate(withPage.data(), withPage.size(), &idx, &members, &got));
  CHECK(idx == 0 && members == std::vector<int>({0, 9, 13, 14, 16}));
  CHECK(got.status == 0x10181 && got.role == 2 && got.loadoutId == 7 && got.loadoutVersion == 3 && got.noun == kAlpha);
  // C0 02 (shaper_select.py selftest): empty, and MaxHealth Add 50.0 + one modifier
  CHECK_HEX(gms::BuildPreGameLoadout(0, {}, {}), "c002000000");
  gms::LoadoutAttribute a;
  a.attribute = 2;
  a.op = 1;
  a.value = 50.0f;
  CHECK_HEX(gms::BuildPreGameLoadout(0, {a}, {0x1234}), "c00200010201000048420134120000");
  CHECK_HEX(gms::BuildPreGameLoadout(0, {a}, {}), "c0020001020100004842" "00");  // GMS_PROTOCOL.md's example
  // the other GMS messages
  CHECK_HEX(gms::BuildRoleToggle(0, 2), "c8060002");
  CHECK_HEX(gms::BuildPerkPages({7, 9}), "c809020700000009000000");
  CHECK_HEX(gms::BuildVanity(1, 0x12345678), "c80e0178563412");
  // the client's messages of script B parse and build back
  const char* clientHex[] = {"c801", "c802", "c804ab4a04ef", "c805ab4a04ef", "c80700", "c80800", "c80dab4a04ef",
                             "c80e0178563412", "c80cab4a04ef", "c80a07000000", "c80602"};
  for (const char* h : clientHex) {
    Bytes m;
    CHECK(gms::ParseHex(h, m));
    gms::ShaperSelectRequest q;
    CHECK(gms::ParseShaperSelectRequest(m.data() + 1, m.size() - 1, &q));
    CHECK_HEX(gms::BuildShaperSelectRequest(q), h);
  }
  gms::ShaperSelectRequest q;
  const uint8_t shortLock[] = {0x05, 0xAB, 0x4A};
  CHECK(!gms::ParseShaperSelectRequest(shortLock, sizeof shortLock, &q));
  const uint8_t unknown[] = {0x03};
  CHECK(!gms::ParseShaperSelectRequest(unknown, sizeof unknown, &q));  // 03 is GMS -> client only
}

// ---- O2, the exact part: script B with the ally scripted at s+2 s / s+4 s, on a synthetic clock -----------------------
GMS_TEST(ShaperSelectScriptBExact) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(30000, 30000, 10000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1), Entry(kGunman, 2)};
  cfg.players.push_back(Scripted(1, 1, kArtificer, 2000, 4000, 1));
  gms::ShaperSelect ss(cfg);
  const gms::ConnId human = 77;
  ss.AddHuman(human, 0, 1);
  RecordingSender out;
  const int64_t s = 10000;
  auto tickTo = [&](int64_t from, int64_t to) {
    for (int64_t t = from; t <= to; t += 100) ss.Tick(t, out);
  };
  Msg(ss, human, Req(gms::kShaperHello), s, out);
  CHECK_EQ(ss.Phase(), 0);
  ss.OnReady(human, s, out);
  CHECK_EQ(ss.Phase(), 1);
  tickTo(s + 100, s + 5000);                              // the ally picks at s+2000 and locks at s+4000
  Msg(ss, human, Req(gms::kShaperRequest, kAlpha), s + 5000, out);   // 14
  tickTo(s + 5100, s + 6000);
  Msg(ss, human, Req(gms::kShaperLockIn, kAlpha), s + 6000, out);    // 16: everyone locked -> phase 2
  CHECK_EQ(ss.Phase(), 2);
  Msg(ss, human, Req(gms::kShaperSelectRole, 0, 0), s + 6000, out);  // 18: no change, no record (19)
  tickTo(s + 6100, s + 9000);
  Msg(ss, human, Req(gms::kShaperLockRole, 0, 0), s + 9000, out);    // 21: the ally follows -> phase 3
  CHECK_EQ(ss.Phase(), 3);
  tickTo(s + 9100, s + 18900);
  CHECK(!ss.Revealed());
  // 24 at s+19000, the phase-3 timer's moment: script B's bytes assume the timer (the skin is the record's anyway)
  Msg(ss, human, Req(gms::kShaperLockSkin, kAlpha), s + 19000, out);
  CHECK(ss.Revealed());
  CHECK(!ss.Done(s + 19015));
  CHECK(ss.Done(s + 19016));

  CHECK_LIST(out.To(human), {
      "c802ab4a04ef0100000000009eb0a85d010000000000271e1d7c020000000000",  // 9
      "c80300" + Zeros(14), "c80301" + Zeros(8) + "01" + Zeros(5),       // 10 (the ally's record shows its planned role 1)
      "c801409c00000000000001000000",                                     // 11
      "c803019eb0a85d9eb0a85d010000000000",                               // 12 (s+2000)
      "c803019eb0a85d9eb0a85d010000000001",                               // 13 (s+4000)
      "c80300ab4a04efab4a04ef000000000000", "c804ab4a04ef01",             // 15
      "c80300ab4a04efab4a04ef000000000001", "c80b01ab4a04ef00", "c805ab4a04ef01",  // 17
      "c801b0b300000000000002000000", "c8060101",                         // 20 (+ the ally's intended role)
      "c80300ab4a04efab4a04ef000000000003", "c803019eb0a85d9eb0a85d010000000003",  // 22
      "c801487100000000000003000000",                                     // 23
      "c801587100000000000004000000",                                     // 25
  });
  CHECK(out.Quiet() >= 15);  // the one-second repeats of the current phase
  for (const Sent& x : out.sent)
    if (x.quiet) CHECK_EQ(x.hex.substr(0, 4), std::string("c801"));
  const std::vector<gms::ShaperPick> picks = ss.Picks();
  CHECK_EQ(picks.size(), size_t{2});
  if (picks.size() == 2) {
    CHECK(picks[0].gameplayIndex == 0 && picks[0].shaper == kAlpha && picks[0].skin == kAlpha && picks[0].role == 0);
    CHECK(picks[1].gameplayIndex == 1 && picks[1].shaper == kArtificer && picks[1].role == 1 && picks[1].scripted);
  }
  CHECK_EQ(ss.Describe(), std::string("shaper select phase 4; picks 0=0xef044aab/0xef044aab r0 "
                                      "1=0x5da8b09e/0x5da8b09e r1"));
  // after C0 01 nothing more goes to the client
  ss.Close(human);
  const size_t before = out.sent.size();
  ss.Tick(s + 30000, out);
  CHECK_EQ(out.sent.size(), before);
}

// ---- O3: invalid requests -------------------------------------------------------------------------------------------
GMS_TEST(ShaperSelectInvalidRequests) {
  QuietLog log;
  const uint32_t fenmore = gms::AssetIdOf("fenmore.Noun");
  const uint32_t nobody = gms::AssetIdOf("nobody.Noun");
  const uint32_t alphaB = gms::AssetIdOf("alpha_b.Noun");
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(1000, 1000, 1000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 0), Entry(kGunman, 0x11), Entry(fenmore, 1)};
  cfg.humanDefault.skins[kAlpha] = {alphaB};
  cfg.players.push_back(Scripted(1, 1, fenmore, 100, 300, 2));
  gms::ShaperSelect ss(cfg);
  const gms::ConnId h = 5;
  ss.AddHuman(h, 0, 1);
  RecordingSender out;
  ss.OnReady(h, 5000, out);
  size_t mark = out.sent.size();
  auto since = [&]() {
    std::vector<std::string> v = out.To(h, mark);
    mark = out.sent.size();
    return v;
  };
  Msg(ss, h, Req(gms::kShaperRequest, kArtificer), 5010, out);  // unowned (flags 0)
  Msg(ss, h, Req(gms::kShaperRequest, kGunman), 5020, out);     // hidden (owned, bit 4)
  Msg(ss, h, Req(gms::kShaperRequest, nobody), 5030, out);      // not in the list
  CHECK_LIST(since(), {"c8049eb0a85d00", "c804271e1d7c00", "c804" + gms::Hex(Req(gms::kShaperRequest, nobody)).substr(2) + "00"});
  CHECK_EQ(ss.Record(0)->shaper, 0u);
  ss.Tick(5100, out);  // the ally picks fenmore
  since();
  Msg(ss, h, Req(gms::kShaperRequest, fenmore), 5110, out);     // a teammate holds it
  CHECK_LIST(since(), {"c804" + gms::Hex(Req(gms::kShaperRequest, fenmore)).substr(2) + "00"});
  CHECK_EQ(ss.Record(0)->shaper, 0u);
  // wrong phase: a role lock, a skin and a skin lock in phase 1 are ignored; so are a role out of range and vanity of
  // an unknown kind
  Msg(ss, h, Req(gms::kShaperLockRole, 0, 1), 5120, out);
  Msg(ss, h, Req(gms::kShaperSelectSkin, kAlpha), 5130, out);
  Msg(ss, h, Req(gms::kShaperLockSkin, kAlpha), 5140, out);
  Msg(ss, h, Req(gms::kShaperSelectRole, 0, 7), 5150, out);
  Msg(ss, h, Req(gms::kShaperSelectVanity, 0x1234, 0, 2), 5160, out);
  CHECK(since().empty());
  // vanity with no ownership data is accepted and echoed
  Msg(ss, h, Req(gms::kShaperSelectVanity, 0x1234, 0, gms::kVanityAnnouncer), 5170, out);
  CHECK_LIST(since(), {"c80e0134120000"});
  // a lock with nothing picked: the first selectable entry no teammate holds, answered 01 (never 00)
  Msg(ss, h, Req(gms::kShaperLockIn, 0), 5200, out);
  CHECK_LIST(since(), {"c80300ab4a04efab4a04ef000000000001", "c80b02ab4a04ef00" + gms::Hex(Req(gms::kShaperLockIn, alphaB)).substr(2) + "00",
                       "c805ab4a04ef01"});
  CHECK_EQ(ss.Phase(), 1);  // the ally locks at +300
  // a request after the lock is answered 00
  Msg(ss, h, Req(gms::kShaperRequest, kAlpha), 5210, out);
  CHECK_LIST(since(), {"c804ab4a04ef00"});
  CHECK_EQ(ss.Record(0)->shaper, kAlpha);
  ss.Tick(5300, out);  // the ally locks -> phase 2
  CHECK_EQ(ss.Phase(), 2);
  since();
  Msg(ss, h, Req(gms::kShaperSelectSkin, alphaB), 5310, out);  // a skin in phase 2: ignored
  CHECK(since().empty());
  Msg(ss, h, Req(gms::kShaperLockRole, 0, 1), 5320, out);  // the human's role lock; the ally follows with role 2
  CHECK_EQ(ss.Phase(), 3);
  CHECK_EQ(ss.Record(1)->role, 2);
  since();
  Msg(ss, h, Req(gms::kShaperSelectRole, 0, 2), 5330, out);  // role in phase 3: ignored
  Msg(ss, h, Req(gms::kShaperSelectSkin, nobody), 5340, out);  // not one of its skins
  Msg(ss, h, Req(gms::kShaperLockSkin, nobody), 5350, out);
  CHECK(since().empty());
  CHECK_EQ(ss.Phase(), 3);
  CHECK_EQ(ss.Record(0)->role, 1);
  Msg(ss, h, Req(gms::kShaperSelectSkin, alphaB), 5360, out);  // a listed skin: the record to the team
  CHECK_LIST(since(), {"c80300ab4a04ef" + gms::Hex(Req(gms::kShaperLockIn, alphaB)).substr(2) + "010000000003"});
  ss.Tick(6320, out);  // the skin timer: the reveal keeps the last C8 0C
  CHECK(ss.Revealed());
  CHECK_EQ(ss.Picks()[0].skin, alphaB);
  CHECK_EQ(ss.Picks()[0].announcer, 0x1234u);
  since();
  Msg(ss, h, Req(gms::kShaperRequest, kAlpha), 6330, out);  // phase 4: still answered (00)
  Msg(ss, h, Req(gms::kShaperLockIn, kAlpha), 6340, out);   // phase 4: ignored, never 00
  CHECK_LIST(since(), {"c804ab4a04ef00"});
  for (const Sent& x : out.sent) CHECK(x.hex.substr(0, 4) != "c805" || x.hex.substr(12) == "01");
  CHECK(log.Has("player 0: lock role ignored (wrong phase or role locked)"));
}

GMS_TEST(ShaperSelectRandomPickAndLock) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(1000, 1000, 1000, 16);
  cfg.seed = 7;
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 2), Entry(kGunman, 0x20)};
  gms::ShaperSelect ss(cfg);
  ss.AddHuman(1, 0, 1);
  RecordingSender out;
  ss.OnReady(1, 1000, out);
  const size_t mark = out.sent.size();
  Msg(ss, 1, Req(gms::kShaperRequest, gms::kNounRandom), 1010, out);  // the Random card is always accepted
  CHECK_LIST(out.To(1, mark), {"c80300ffffffffffffffff000000000000", "c804ffffffff01"});
  Msg(ss, 1, Req(gms::kShaperLockIn, gms::kNounRandom), 1020, out);   // the lock makes it a real, free shaper
  const uint32_t s = ss.Record(0)->shaper;
  CHECK(s == kAlpha || s == kArtificer || s == kGunman);
  CHECK_EQ(ss.Record(0)->skin, s);
  CHECK_EQ(ss.Phase(), 2);
  // the same seed gives the same pick
  gms::ShaperSelect again(cfg);
  again.AddHuman(1, 0, 1);
  RecordingSender out2;
  again.OnReady(1, 1000, out2);
  Msg(again, 1, Req(gms::kShaperRequest, gms::kNounRandom), 1010, out2);
  Msg(again, 1, Req(gms::kShaperLockIn, gms::kNounRandom), 1020, out2);
  CHECK_EQ(again.Record(0)->shaper, s);
}

// ---- O4: the timers with a silent client (L1's options: artificer and alpha owned, 8000/8000/4000/16 ms) ---------------
GMS_TEST(ShaperSelectTimeoutsWithASilentClient) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(8000, 8000, 4000, 16);
  cfg.humanDefault.list = {Entry(kArtificer, 1), Entry(kAlpha, 1)};
  gms::ShaperSelect ss(cfg);
  const gms::ConnId h = 3;
  ss.AddHuman(h, 0, 1);
  RecordingSender out;
  ss.OnReady(h, 10000, out);
  // the live client's own messages without clicks: 07 on entering phase 2, 08 on 2 -> 3 (L1: ShaperSelectMsgs x4)
  int clientPhase = 1;
  size_t seen = out.sent.size();  // after the list, the record and phase 1 (end 18000)
  CHECK_LIST(out.To(h), {"c8029eb0a85d010000000000ab4a04ef010000000000", "c80300" + Zeros(14),
                         "c801504600000000000001000000"});
  std::vector<std::pair<int64_t, std::string>> timeline;
  for (int64_t t = 10050; t <= 31000; t += 50) {
    ss.Tick(t, out);
    for (; seen < out.sent.size(); ++seen) {
      const Sent& x = out.sent[seen];
      if (x.quiet) continue;
      timeline.emplace_back(t, x.hex);
      if (x.hex.substr(0, 4) != "c801") continue;
      const int p = std::stoi(x.hex.substr(20, 2), nullptr, 16);
      if (p == 2 && clientPhase == 1) Msg(ss, h, Req(gms::kShaperSelectRole, 0, 0), t, out);
      if (p == 3 && clientPhase == 2) Msg(ss, h, Req(gms::kShaperLockRole, 0, 0), t, out);
      clientPhase = p;
    }
  }
  std::vector<std::string> got;
  for (const auto& e : timeline) got.push_back(std::to_string(e.first) + " " + e.second);
  CHECK_LIST(got, {
      // phase 1 timer at 18000: the GMS locks the first selectable entry, the skins before phase 2, no C8 05
      "18000 c803009eb0a85d9eb0a85d000000000001", "18000 c80b019eb0a85d00", "18000 c801906500000000000002000000",
      // phase 2 timer at 26000 (end 18000 + 8000); the client's 08 follows the phase change
      "26000 c801307500000000000003000000", "26000 c803009eb0a85d9eb0a85d000000000003",
      // phase 3 timer at 30000: the default skin, the reveal for 16 ms
      "30000 c801407500000000000004000000",
  });
  CHECK(ss.Revealed() && ss.Done(30016) && !ss.Done(30015));
  CHECK_EQ(ss.Describe(), std::string("shaper select phase 4; picks 0=0x5da8b09e/0x5da8b09e r0"));
}

GMS_TEST(ShaperSelectSavedSkinAtTheTimer) {
  QuietLog log;
  const uint32_t alphaB = gms::AssetIdOf("alpha_b.Noun");
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(100, 100, 100, 16);
  gms::ShaperSelectPlayer a, b;  // two humans with their own content: a's saved skin is owned, b's is not
  a.gameplayIndex = 0;
  a.content.list = {Entry(kAlpha, 1)};
  a.content.skins[kAlpha] = {alphaB};
  a.content.savedSkins[kAlpha] = alphaB;
  b = a;
  b.gameplayIndex = 5;
  b.team = 2;
  b.content.skins.clear();
  cfg.players = {a, b};
  gms::ShaperSelect ss(cfg);
  ss.AddHuman(1, 0, 1);
  ss.AddHuman(2, 5, 2);
  RecordingSender out;
  ss.OnReady(1, 1000, out);
  ss.OnReady(2, 1000, out);
  for (int64_t t = 1000; t <= 1400; t += 10) ss.Tick(t, out);
  CHECK(ss.Revealed());
  CHECK_EQ(ss.Record(0)->skin, alphaB);
  CHECK_EQ(ss.Record(5)->skin, kAlpha);
  // the skin record went to the own team before the reveal, then every record to the other team (silent humans:
  // shaper locked by the timer, role not locked)
  const std::vector<std::string> to1 = out.To(1);
  CHECK(to1.size() >= 3);
  if (to1.size() >= 3) {
    CHECK_EQ(to1[to1.size() - 3], "c80300ab4a04ef" + gms::Hex(Req(gms::kShaperLockIn, alphaB)).substr(2) + "000000000001");
    CHECK_EQ(to1[to1.size() - 2].substr(0, 4), std::string("c801"));
    CHECK_EQ(to1.back(), std::string("c80305ab4a04efab4a04ef000000000001"));
  }
}

// ---- O5: two teams ---------------------------------------------------------------------------------------------------
GMS_TEST(ShaperSelectTwoTeams) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(30000, 30000, 30000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
  cfg.players.push_back(Scripted(1, 1, kArtificer, 100, 200, 3));
  gms::ShaperSelect ss(cfg);
  const gms::ConnId blue = 11, red = 22;
  ss.AddHuman(blue, 0, 1);
  ss.AddHuman(red, 5, 2);
  RecordingSender out;
  ss.OnReady(blue, 1000, out);
  ss.OnReady(red, 1000, out);
  for (int64_t t = 1000; t <= 1300; t += 50) ss.Tick(t, out);  // the blue ally picks and locks artificer
  // the same shaper on both teams is accepted
  Msg(ss, blue, Req(gms::kShaperRequest, kAlpha), 1400, out);
  Msg(ss, red, Req(gms::kShaperRequest, kAlpha), 1410, out);
  CHECK_EQ(ss.Record(0)->shaper, kAlpha);
  CHECK_EQ(ss.Record(5)->shaper, kAlpha);
  // but not a teammate's: red may take artificer, blue may not
  Msg(ss, red, Req(gms::kShaperRequest, kArtificer), 1420, out);
  CHECK_EQ(ss.Record(5)->shaper, kArtificer);
  Msg(ss, blue, Req(gms::kShaperRequest, kArtificer), 1430, out);
  CHECK_EQ(ss.Record(0)->shaper, kAlpha);
  // a preferred-role toggle is echoed to the own team only
  const size_t toggleMark = out.sent.size();
  Msg(ss, blue, Req(gms::kShaperPreferredRole, 0, 2), 1440, out);
  CHECK_LIST(out.To(blue, toggleMark), {"c8060002"});
  CHECK(out.To(red, toggleMark).empty());
  Msg(ss, blue, Req(gms::kShaperLockIn, kAlpha), 1500, out);
  Msg(ss, red, Req(gms::kShaperLockIn, kArtificer), 1510, out);
  CHECK_EQ(ss.Phase(), 2);
  Msg(ss, blue, Req(gms::kShaperLockRole, 0, 0), 1600, out);
  CHECK_EQ(ss.Phase(), 2);  // red hasn't locked; the ally waits for every connected human
  CHECK(!(ss.Record(1)->flags & gms::kRecordRoleLocked));
  Msg(ss, red, Req(gms::kShaperLockRole, 0, 1), 1610, out);
  CHECK_EQ(ss.Phase(), 3);
  CHECK_EQ(ss.Record(1)->role, 3);
  const size_t revealAt = out.sent.size();
  Msg(ss, blue, Req(gms::kShaperLockSkin, kAlpha), 1700, out);
  CHECK_EQ(ss.Phase(), 3);
  Msg(ss, red, Req(gms::kShaperLockSkin, kArtificer), 1710, out);
  CHECK(ss.Revealed());
  // no enemy record before phase 4; at phase 4 the other team's records
  auto recordIndex = [](const std::string& hex) { return std::stoi(hex.substr(4, 2), nullptr, 16); };
  for (size_t i = 0; i < revealAt; ++i) {
    const Sent& x = out.sent[i];
    if (x.hex.substr(0, 4) != "c803") continue;
    const int idx = recordIndex(x.hex);
    if (x.conn == blue) CHECK(idx == 0 || idx == 1);
    if (x.conn == red) CHECK_EQ(idx, 5);
  }
  CHECK_LIST(out.To(blue, revealAt), {"c801" + gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), 4)).substr(4),
                                      "c80305" + gms::Hex(gms::BuildShaperRecord(*ss.Record(5))).substr(6)});
  CHECK_LIST(out.To(red, revealAt), {"c801" + gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), 4)).substr(4),
                                     gms::Hex(gms::BuildShaperRecord(*ss.Record(0))),
                                     gms::Hex(gms::BuildShaperRecord(*ss.Record(1)))});
  // every client ends with every player's current record
  for (gms::ConnId c : {blue, red}) {
    std::map<int, std::string> last;
    for (const Sent& x : out.sent)
      if (x.conn == c && x.hex.substr(0, 4) == "c803") last[recordIndex(x.hex)] = x.hex;
    CHECK_EQ(last.size(), size_t{3});
    for (int i : {0, 1, 5}) CHECK_EQ(last[i], gms::Hex(gms::BuildShaperRecord(*ss.Record(static_cast<uint8_t>(i)))));
  }
}

// Q2: a human whose connection ended and who comes back (or whose C8 02 comes late) after the phase-1 timer locked its
// record. Nothing goes to the new connection before its C8 02 (its client has no shaper select yet), and its C8 02 gets
// the locked shaper's skins (C8 0B) before the current phase: the client asserts numSkins > 0 in phase 3 without them.
GMS_TEST(ShaperSelectRejoiningHumanGetsItsSkins) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(1000, 30000, 30000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
  gms::ShaperSelect ss(cfg);
  const gms::ConnId a = 11, b = 12, b2 = 13;
  ss.AddHuman(a, 0, 1);
  ss.AddHuman(b, 1, 1);
  RecordingSender out;
  ss.OnReady(a, 1000, out);
  ss.OnReady(b, 1000, out);
  Msg(ss, a, Req(gms::kShaperRequest, kAlpha), 1100, out);
  Msg(ss, a, Req(gms::kShaperLockIn, kAlpha), 1150, out);
  ss.RemoveHuman(b);  // b's connection ends in phase 1
  for (int64_t t = 1200; t <= 2000; t += 50) ss.Tick(t, out);
  CHECK_EQ(ss.Phase(), 2);
  CHECK_EQ(ss.Record(1)->shaper, kArtificer);  // the timer's pick: alpha is a's
  CHECK(ss.Record(1)->flags & gms::kRecordShaperLocked);
  // b comes back on a new connection: nothing for it (not even the quiet repeat) until its C8 02
  ss.AddHuman(b2, 1, 1);
  const size_t mark = out.sent.size();
  for (int64_t t = 2100; t <= 3200; t += 100) ss.Tick(t, out);
  for (size_t i = mark; i < out.sent.size(); ++i) CHECK(out.sent[i].conn != b2);
  const size_t ready = out.sent.size();
  ss.OnReady(b2, 3300, out);
  // REJOINSS: the skins (and the phase with them) wait for its client's UI: list and records only
  CHECK_LIST(out.To(b2, ready), {"c802ab4a04ef0100000000009eb0a85d010000000000",
                                 gms::Hex(gms::BuildShaperRecord(*ss.Record(0))),
                                 gms::Hex(gms::BuildShaperRecord(*ss.Record(1)))});
  // no phase repeat to it while they wait; its first C8 after the C8 02 (a role click: its UI is up) releases them
  const size_t held = out.sent.size();
  for (int64_t t = 3400; t <= 4600; t += 100) ss.Tick(t, out);
  CHECK(out.To(b2, held).empty());
  const size_t ui = out.sent.size();
  Msg(ss, b2, Req(gms::kShaperSelectRole, 0, 1), 4700, out);
  const std::vector<std::string> got = out.To(b2, ui);
  CHECK(got.size() >= 2);
  if (got.size() >= 2) {
    CHECK_EQ(got[0], "c80b019eb0a85d00");
    CHECK_EQ(got[1], gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), 2)));
  }
  CHECK_EQ(ss.Phase(), 2);
}

// REJOINSS: a returning player whose client says nothing after its C8 02 gets its held skins and
// the phase lateSkinsHoldMs after it; one that comes after the reveal (a rejoin into the running game) gets no skins at
// all and the phase at once; with lateSkinsHoldMs 0 the skins go at once, before the phase (the old behaviour).
GMS_TEST(ShaperSelectLateSkinsWaitForTheClientsUi) {
  for (int variant = 0; variant < 3; ++variant) {
    QuietLog log;
    gms::ShaperSelectConfig cfg;
    cfg.timers = Timers(1000, 30000, 30000, 16);
    cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
    cfg.lateSkinsHoldMs = variant == 2 ? 0 : 3000;
    gms::ShaperSelect ss(cfg);
    const gms::ConnId a = 11, b = 12, b2 = 13;
    ss.AddHuman(a, 0, 1);
    ss.AddHuman(b, 1, 1);
    RecordingSender out;
    ss.OnReady(a, 1000, out);
    ss.OnReady(b, 1000, out);
    Msg(ss, a, Req(gms::kShaperRequest, kAlpha), 1100, out);
    Msg(ss, a, Req(gms::kShaperLockIn, kAlpha), 1150, out);
    ss.RemoveHuman(b);
    int64_t t = 1200;
    for (; t <= 2000; t += 50) ss.Tick(t, out);
    CHECK_EQ(ss.Phase(), 2);
    if (variant == 1) {  // the rest of shaper select without b: roles by the timer, skins, the reveal
      Msg(ss, a, Req(gms::kShaperLockRole, 0, 0), t, out);
      Msg(ss, a, Req(gms::kShaperLockSkin, kAlpha), t += 10, out);
      for (int i = 0; i < 70 && !ss.Revealed(); ++i) ss.Tick(t += 500, out);
      CHECK(ss.Revealed());
    }
    ss.AddHuman(b2, 1, 1);
    const size_t ready = out.sent.size();
    ss.OnReady(b2, t += 100, out);
    const std::string skins = "c80b019eb0a85d00";
    const std::string phase = gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), static_cast<uint32_t>(ss.Phase())));
    std::vector<std::string> got = out.To(b2, ready);
    const bool hasSkins = std::find(got.begin(), got.end(), skins) != got.end();
    if (variant == 0) {
      CHECK(!hasSkins);
      CHECK(std::find(got.begin(), got.end(), phase) == got.end());
      const size_t mark = out.sent.size();
      for (int i = 0; i < 28; ++i) ss.Tick(t += 100, out);  // 2.8 s: still held
      CHECK(out.To(b2, mark).empty());
      ss.Tick(t += 300, out);  // 3.1 s after its C8 02
      got = out.To(b2, mark);
      CHECK(got.size() >= 2);
      if (got.size() >= 2) {
        CHECK_EQ(got[0], skins);
        CHECK_EQ(got[1], phase);
      }
    } else if (variant == 1) {
      CHECK_EQ(ss.Phase(), 4);
      CHECK(!hasSkins);
      CHECK(!got.empty() && got.back() == phase);
    } else {
      CHECK(hasSkins);
      CHECK(got.size() >= 2 && got[got.size() - 2] == skins && got.back() == phase);
    }
  }
}

// ---- G3a: several humans ----------------------------------------------------------------------------------------------

namespace {

// The gameplay indices of the records (C8 03) and role toggles (C8 06) sent to `conn` from position `from` on.
std::vector<int> IndicesTo(const RecordingSender& out, gms::ConnId conn, const char* sub, size_t from = 0) {
  std::vector<int> v;
  for (size_t i = from; i < out.sent.size(); ++i)
    if (out.sent[i].conn == conn && out.sent[i].hex.compare(0, 4, sub) == 0)
      v.push_back(std::stoi(out.sent[i].hex.substr(4, 2), nullptr, 16));
  return v;
}

gms::ShaperSelectPlayer Human(uint8_t index, uint8_t team) {
  gms::ShaperSelectPlayer p;
  p.gameplayIndex = index;
  p.team = team;
  return p;
}

}  // namespace

// Q2's third hole: a client has records only for the players it had when it opened shaper select; a record or role
// toggle of any other index asserts in it. With SetKnown (the session's snapshot at B6) nothing of another index goes
// to it: not the configured human who never connected (1), not a teammate its client doesn't have (3), not an enemy it
// doesn't have (5) at the reveal. Without SetKnown every index counts as known (D3-core's behaviour).
GMS_TEST(ShaperSelectRecordsOnlyForPlayersTheClientKnows) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(1000, 1000, 1000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1), Entry(kGunman, 1),
                           Entry(gms::AssetIdOf("fenmore.Noun"), 1)};
  cfg.players = {Human(0, 1), Human(1, 1), Human(3, 1), Human(5, 2)};
  for (gms::ShaperSelectPlayer& pl : cfg.players) pl.content = cfg.humanDefault;  // configured: their own content
  cfg.players.push_back(Scripted(2, 1, kArtificer, 100, 200, 1));
  gms::ShaperSelect ss(cfg);
  const gms::ConnId a = 10, d = 30, c = 50;
  ss.AddHuman(a, 0, 1);
  ss.AddHuman(d, 3, 1);
  ss.AddHuman(c, 5, 2);
  ss.SetKnown(a, {0, 2});     // its HelloPlayer: itself and the scripted ally
  ss.SetKnown(d, {});         // only itself (the session adds the own index anyway)
  ss.SetKnown(c, {0, 2, 5});  // its HelloPlayer and a PlayerJoined of 0 before its B6
  RecordingSender out;
  ss.OnReady(a, 1000, out);
  ss.OnReady(d, 1000, out);
  ss.OnReady(c, 1000, out);
  CHECK(IndicesTo(out, a, "c803") == std::vector<int>({0, 2}));  // its team's current records it has
  CHECK(IndicesTo(out, d, "c803") == std::vector<int>({3}));
  CHECK(IndicesTo(out, c, "c803") == std::vector<int>({5}));
  // a preferred-role toggle of A: to A, not to D (same team, but its client has no player 0)
  Msg(ss, a, Req(gms::kShaperPreferredRole, 0, 2), 1010, out);
  CHECK(IndicesTo(out, a, "c806") == std::vector<int>({0}));
  CHECK(IndicesTo(out, d, "c806").empty());
  // A's and D's picks: records to the team members that have the player
  Msg(ss, a, Req(gms::kShaperRequest, kAlpha), 1020, out);
  Msg(ss, d, Req(gms::kShaperRequest, kGunman), 1030, out);
  const size_t mark = out.sent.size();
  for (int64_t t = 1050; t <= 4100; t += 50) ss.Tick(t, out);  // the ally, then the timers: every record locked
  CHECK(ss.Revealed());
  for (gms::ConnId to : {a, d, c})
    for (int idx : IndicesTo(out, to, "c803")) {
      CHECK(idx != 1);  // human 1 never connected and nobody has it
      if (to == a) CHECK(idx == 0 || idx == 2);
      if (to == d) CHECK_EQ(idx, 3);
    }
  // at the reveal C gets the other team's records it has (0, 2); D gets none (it has nobody else)
  std::vector<int> cReveal = IndicesTo(out, c, "c803", mark);
  CHECK(std::find(cReveal.begin(), cReveal.end(), 0) != cReveal.end());
  CHECK(std::find(cReveal.begin(), cReveal.end(), 2) != cReveal.end());
  CHECK(std::find(cReveal.begin(), cReveal.end(), 3) == cReveal.end());
  // human 1 still got its automatic pick (the session gives it 0x4: it never connected)
  CHECK(ss.Record(1)->shaper != 0 && (ss.Record(1)->flags & gms::kRecordShaperLocked));

  // D3-core's default: a client without SetKnown gets every record of its team
  gms::ShaperSelect all(cfg);
  all.AddHuman(a, 0, 1);
  RecordingSender out2;
  all.OnReady(a, 1000, out2);
  CHECK(IndicesTo(out2, a, "c803") == std::vector<int>({0, 1, 2, 3}));
  // AddHuman for a returning player forgets the old client's set until the session's SetKnown for the new one
  ss.RemoveHuman(a);
  ss.AddHuman(77, 0, 1);
  RecordingSender out3;
  ss.OnReady(77, 4200, out3);
  CHECK(IndicesTo(out3, 77, "c803") == std::vector<int>({0, 1, 2, 3, 5}));  // phase 4: every record
}

// Q2's last hole (the shaper select side): two humans on one team; the second leaves and comes back on a new connection
// in each phase (the session's RejoinMap gives it its old gameplay index). Nothing goes to the new connection before its
// C8 02; then its list, its team's records, its locked shaper's skins and the current phase; it continues the phase
// (pick, lock role, lock skin), and the picks are its own.
GMS_TEST(ShaperSelectTwoHumansRejoinInEachPhase) {
  const uint32_t artB = gms::AssetIdOf("artificer_b.Noun");
  for (int p = 1; p <= 4; ++p) {
    QuietLog log;
    gms::ShaperSelectConfig cfg;
    cfg.timers = Timers(10000, 10000, 10000, 16);
    cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
    cfg.humanDefault.skins[kArtificer] = {artB};
    cfg.players = {Human(0, 1), Human(1, 1)};
    for (gms::ShaperSelectPlayer& pl : cfg.players) pl.content = cfg.humanDefault;  // configured: their own content
    gms::ShaperSelect ss(cfg);
    const gms::ConnId a = 10, b1 = 11, b2 = 21;
    gms::ConnId b = b1;
    ss.AddHuman(a, 0, 1);
    ss.AddHuman(b1, 1, 1);
    ss.SetKnown(a, {0, 1});
    ss.SetKnown(b1, {0, 1});
    RecordingSender out;
    int64_t t = 1000;
    ss.OnReady(a, t, out);
    ss.OnReady(b1, t, out);
    auto rejoin = [&]() {
      ss.RemoveHuman(b1);
      for (int i = 0; i < 3; ++i) ss.Tick(t += 400, out);
      ss.AddHuman(b2, 1, 1);  // the same index on a new connection
      const size_t before = out.sent.size();
      for (int i = 0; i < 3; ++i) ss.Tick(t += 400, out);  // a one-second repeat: not to b2 yet
      for (size_t i = before; i < out.sent.size(); ++i) CHECK(out.sent[i].conn != b2);
      ss.SetKnown(b2, {0, 1});
      const size_t ready = out.sent.size();
      ss.OnReady(b2, t += 10, out);
      std::vector<std::string> want = {gms::Hex(gms::BuildShaperList(cfg.humanDefault.list)),
                                       gms::Hex(gms::BuildShaperRecord(*ss.Record(0))),
                                       gms::Hex(gms::BuildShaperRecord(*ss.Record(1)))};
      // REJOINSS: a locked shaper's skins (and the phase with them) wait for the client's UI before phase 4; from
      // phase 4 on no skins, the phase at once
      const bool held = (ss.Record(1)->flags & gms::kRecordShaperLocked) && p < 4;
      if (!held) want.push_back(gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), static_cast<uint32_t>(p))));
      CheckList(out.To(b2, ready), want, __LINE__);
      if (held) {  // its UI shows (a preferred-role toggle): the skins, then the phase
        const size_t ui = out.sent.size();
        Msg(ss, b2, Req(gms::kShaperPreferredRole, 0, 3), t += 10, out);
        const std::vector<std::string> got = out.To(b2, ui);
        CHECK(got.size() >= 2);
        if (got.size() >= 2) {
          CHECK_EQ(got[0], gms::Hex(gms::BuildSkins({kArtificer, artB})));
          CHECK_EQ(got[1], gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), static_cast<uint32_t>(p))));
        }
      }
      CHECK_EQ(ss.Phase(), p);
      if (p >= 2) CHECK(ss.Record(1)->flags & gms::kRecordShaperLocked);
      b = b2;
    };
    for (int ph = 1; ph <= 3; ++ph) {
      CHECK_EQ(ss.Phase(), ph);
      if (ph == p) rejoin();
      if (ph == 1) {  // B picks and locks artificer, A alpha: everyone locked, phase 2
        Msg(ss, b, Req(gms::kShaperRequest, kArtificer), t += 10, out);
        Msg(ss, b, Req(gms::kShaperLockIn, kArtificer), t += 10, out);
        Msg(ss, a, Req(gms::kShaperRequest, kAlpha), t += 10, out);
        Msg(ss, a, Req(gms::kShaperLockIn, kAlpha), t += 10, out);
      } else if (ph == 2) {  // role locks: B hunter (2), A gladiator (0)
        Msg(ss, b, Req(gms::kShaperLockRole, 0, 2), t += 10, out);
        Msg(ss, a, Req(gms::kShaperLockRole, 0, 0), t += 10, out);
      } else {  // skin locks
        Msg(ss, b, Req(gms::kShaperLockSkin, artB), t += 10, out);
        Msg(ss, a, Req(gms::kShaperLockSkin, kAlpha), t += 10, out);
      }
    }
    CHECK(ss.Revealed());
    if (p == 4) rejoin();
    CHECK(ss.Done(t + 20));
    CHECK(ss.HasHuman(b2) && !ss.HasHuman(b1));
    const std::vector<gms::ShaperPick> picks = ss.Picks();
    CHECK_EQ(picks.size(), size_t{2});
    if (picks.size() == 2) {
      CHECK(picks[0].shaper == kAlpha && picks[0].skin == kAlpha && picks[0].role == 0);
      CHECK(picks[1].shaper == kArtificer && picks[1].skin == artB && picks[1].role == 2 && !picks[1].scripted);
    }
    // A saw B's records throughout (its team), the new connection's included, and never one of an unknown index
    for (int idx : IndicesTo(out, a, "c803")) CHECK(idx == 0 || idx == 1);
    for (const Sent& x : out.sent) CHECK(x.conn != b1 || x.hex.substr(0, 4) != "c805" || x.hex.substr(12) == "01");
  }
}

// The phases of the C8 01 messages (repeats included) to `conn` from position `from` on.
std::vector<int> PhasesTo(const RecordingSender& out, gms::ConnId conn, size_t from = 0) {
  std::vector<int> v;
  for (size_t i = from; i < out.sent.size(); ++i)
    if (out.sent[i].conn == conn && out.sent[i].hex.compare(0, 4, "c801") == 0)
      v.push_back(std::stoi(out.sent[i].hex.substr(20, 2), nullptr, 16));
  return v;
}

// A custom game's (or any game's) two humans on one team: a player whose lock-in was answered is in phase 2 on its own
// client while the other still picks, and one that locked its role (C8 08) is in phase 3 while the other still
// chooses. The client takes any other phase in a C8 01 as a change (ShaperSelect_HandleGmsMessage 0x00521B50, no
// guard in ShaperSelect_SetPhase 0x005201B0), so the phase-1 repeats had reopened the locked player's shaper list (the
// user's report: the shaper not locked in, still switchable). It gets no C8 01 of an earlier phase than its own.
GMS_TEST(ShaperSelectLockedHumanGetsNoEarlierPhase) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(60000, 60000, 60000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
  const uint32_t alphaB = gms::AssetIdOf("alpha_b.Noun");
  cfg.humanDefault.skins[kAlpha] = {alphaB};
  gms::ShaperSelect ss(cfg);
  const gms::ConnId a = 10, b = 11;
  ss.AddHuman(a, 0, 1);
  ss.AddHuman(b, 1, 1);
  RecordingSender out;
  int64_t t = 1000;
  ss.OnReady(a, t, out);
  ss.OnReady(b, t, out);
  CHECK_EQ(ss.Phase(), 1);
  // A picks and locks; B doesn't yet: the match stays in phase 1 for 3 s of repeats
  Msg(ss, a, Req(gms::kShaperRequest, kAlpha), t += 100, out);
  Msg(ss, a, Req(gms::kShaperLockIn, kAlpha), t += 100, out);
  CHECK(ss.Record(0)->flags & gms::kRecordShaperLocked);
  size_t mark = out.sent.size();
  for (int i = 0; i < 30; ++i) ss.Tick(t += 100, out);
  CHECK_EQ(ss.Phase(), 1);
  CHECK(PhasesTo(out, a, mark).empty());                // nothing that takes A back to its list
  CHECK(PhasesTo(out, b, mark).size() >= size_t{2});    // B keeps getting phase 1's repeats
  for (int ph : PhasesTo(out, b, mark)) CHECK_EQ(ph, 1);
  // A clicks a card anyway (a stale click): refused, the record stays; Choose again: the answer, no phase 1
  Msg(ss, a, Req(gms::kShaperRequest, kArtificer), t += 100, out);
  CHECK_EQ(ss.Record(0)->shaper, kAlpha);
  Msg(ss, a, Req(gms::kShaperLockIn, kAlpha), t += 100, out);
  // B locks: phase 2 to both
  mark = out.sent.size();
  Msg(ss, b, Req(gms::kShaperRequest, kArtificer), t += 100, out);
  Msg(ss, b, Req(gms::kShaperLockIn, kArtificer), t += 100, out);
  CHECK_EQ(ss.Phase(), 2);
  CHECK(PhasesTo(out, a, mark) == std::vector<int>({2}));
  CHECK(PhasesTo(out, b, mark) == std::vector<int>({2}));
  // A locks its role (its client is in phase 3 now); B still chooses: A gets no phase-2 repeat
  Msg(ss, a, Req(gms::kShaperLockRole, 0, 2), t += 100, out);
  // A's client sends its preselected skin on entering its phase 3, and A clicks another: both taken already
  Msg(ss, a, Req(gms::kShaperSelectSkin, kAlpha), t += 100, out);
  Msg(ss, a, Req(gms::kShaperSelectSkin, alphaB), t += 100, out);
  CHECK_EQ(ss.Record(0)->skin, alphaB);
  // B's client is still in phase 2: a skin from it is refused
  Msg(ss, b, Req(gms::kShaperSelectSkin, kArtificer), t += 100, out);
  CHECK(log.Has("player 1: skin 0x5da8b09e ignored (not in phase 3)"));
  mark = out.sent.size();
  for (int i = 0; i < 30; ++i) ss.Tick(t += 100, out);
  CHECK_EQ(ss.Phase(), 2);
  CHECK(PhasesTo(out, a, mark).empty());
  CHECK(PhasesTo(out, b, mark).size() >= size_t{2});
  // B locks its role: phase 3 to both (the same phase for A's client: only its timer)
  mark = out.sent.size();
  Msg(ss, b, Req(gms::kShaperLockRole, 0, 0), t += 100, out);
  CHECK_EQ(ss.Phase(), 3);
  CHECK(PhasesTo(out, a, mark) == std::vector<int>({3}));
  CHECK(PhasesTo(out, b, mark) == std::vector<int>({3}));
  for (int i = 0; i < 20; ++i) ss.Tick(t += 100, out);
  for (int ph : PhasesTo(out, a, mark)) CHECK_EQ(ph, 3);
  // a lock-in answer is still the only C8 05 kind sent (01)
  for (const Sent& x : out.sent) CHECK(x.hex.substr(0, 4) != "c805" || x.hex.substr(12) == "01");
  CHECK(log.Has("player 0's client is in phase 2 by itself (its lock-in answered) while the match is in phase 1"));

  // the guard off ([host] shaper_phase_guard=0): the old behaviour, phase 1's repeats to the locked player too
  cfg.clientPhaseGuard = false;
  gms::ShaperSelect old(cfg);
  old.AddHuman(a, 0, 1);
  old.AddHuman(b, 1, 1);
  RecordingSender out2;
  t = 1000;
  old.OnReady(a, t, out2);
  old.OnReady(b, t, out2);
  Msg(old, a, Req(gms::kShaperLockIn, kAlpha), t += 100, out2);
  mark = out2.sent.size();
  for (int i = 0; i < 30; ++i) old.Tick(t += 100, out2);
  CHECK(PhasesTo(out2, a, mark).size() >= size_t{2});
}

// G3a: a player that joins shaper select after phase 1 (an index without a configured player, e.g. a late spectator
// turned player) gets the timers' picks at once, so its pick is never 0 (mpNounDef 0 asserts at C0 01).
GMS_TEST(ShaperSelectLateNewPlayerGetsTheAutomaticPicks) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(100, 100, 100, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
  gms::ShaperSelect ss(cfg);
  ss.AddHuman(1, 0, 1);
  RecordingSender out;
  ss.OnReady(1, 1000, out);
  for (int64_t t = 1000; t <= 1150; t += 10) ss.Tick(t, out);
  CHECK_EQ(ss.Phase(), 2);
  ss.AddHuman(2, 4, 1);  // index 4 is new
  CHECK(ss.Record(4)->shaper == kArtificer && (ss.Record(4)->flags & gms::kRecordShaperLocked));
  CHECK(log.Has("player 4 joins in phase 2; its shaper is locked to 0x5da8b09e"));
  ss.OnReady(2, 1160, out);
  CHECK(out.To(2).size() >= 2);  // list, records (REJOINSS: its skins and phase 2 wait for its UI)
  for (int64_t t = 1170; t <= 1400; t += 10) ss.Tick(t, out);
  CHECK(ss.Revealed());
  ss.AddHuman(3, 6, 2);  // and one after the reveal
  CHECK(ss.Record(6)->shaper == kAlpha && ss.Record(6)->flags == (gms::kRecordShaperLocked | gms::kRecordRoleLocked));
  CHECK_EQ(ss.Picks().size(), size_t{3});
  for (const gms::ShaperPick& k : ss.Picks()) CHECK(k.shaper != 0 && k.skin != 0);
}

// ---- gms_script's shaper select options -------------------------------------------------------------------------------
GMS_TEST(ScriptShaperSelectOptions) {
  std::vector<gms::ShaperListEntry> list;
  std::string err;
  CHECK(gms::ParseShaperList("artificer.Noun:1,alpha:0x21:0x1234,0xEF044AAB:0", &list, &err));
  CHECK_EQ(list.size(), size_t{3});
  if (list.size() == 3) {
    CHECK(list[0].noun == kArtificer && list[0].flags == 1 && list[0].offer == 0);
    CHECK(list[1].noun == kAlpha && list[1].flags == 0x21 && list[1].offer == 0x1234);
    CHECK(list[2].noun == kAlpha && list[2].flags == 0);
  }
  CHECK(!gms::ParseShaperList("alpha.Noun", &list, &err));
  CHECK(!gms::ParseShaperList("alpha.Noun:1:2:3", &list, &err));
  CHECK(!gms::ParseShaperList("-1:1", &list, &err));
  CHECK(!gms::ParseShaperList("alpha:0x100", &list, &err));

  std::map<uint32_t, std::vector<uint32_t>> skins;
  CHECK(gms::ParseSkins("alpha:alpha_b.Noun:alpha_c,artificer:artificer_b", &skins, &err));
  CHECK_EQ(skins[kAlpha].size(), size_t{2});
  CHECK_EQ(skins[kArtificer].size(), size_t{1});
  CHECK(!gms::ParseSkins("alpha", &skins, &err));
  gms::ShaperContent c;
  c.skins = skins;
  CHECK_EQ(c.SkinsFor(kAlpha).size(), size_t{3});
  CHECK_EQ(c.SkinsFor(kAlpha)[0], kAlpha);
  CHECK_EQ(c.SkinsFor(kGunman).size(), size_t{1});

  gms::ShaperSelectTimers t;
  CHECK(gms::ParseShaperTimers("8000,8000,4000,16", &t, &err));
  CHECK(t.pickMs == 8000 && t.rolePerkMs == 8000 && t.skinMs == 4000 && t.revealMs == 16);
  CHECK(!gms::ParseShaperTimers("8000,8000,4000", &t, &err));
  CHECK_EQ(gms::ShaperTimersPractice().pickMs, 300000u);
  CHECK_EQ(gms::ShaperTimersNormal().rolePerkMs, 45500u);

  gms::AllySpec a;
  CHECK(gms::ParseAlly("1:1:artificer.Noun:2000:4000:1", &a, &err));  // L3's ally
  CHECK(a.gameplayIndex == 1 && a.team == 1 && a.plan.noun == kArtificer && a.plan.pickMs == 2000 &&
        a.plan.lockMs == 4000 && a.plan.role == 1);
  CHECK(gms::ParseAlly("6:2:random", &a, &err));
  CHECK(a.gameplayIndex == 6 && a.team == 2 && a.plan.noun == gms::kNounRandom && a.plan.pickMs == 2000);
  CHECK(!gms::ParseAlly("1:3:alpha", &a, &err));            // no team 3
  CHECK(!gms::ParseAlly("12:1:alpha", &a, &err));           // players are 0-9
  CHECK(!gms::ParseAlly("1:1:alpha:100:200:4", &a, &err));  // roles 0-3
  CHECK(!gms::ParseAlly("1:1:alpha:100", &a, &err));

  // ScriptedMatch turns them into the session's input
  gms::ScriptOptions mo;
  mo.allies.push_back(a);
  gms::AllySpec b;
  CHECK(gms::ParseAlly("2:1:alpha", &b, &err));
  mo.allies.push_back(b);
  gms::ScriptedMatch match(mo);
  const std::vector<gms::OtherPlayer> others = match.OtherPlayers();
  CHECK_EQ(others.size(), size_t{2});
  if (others.size() == 2) {
    CHECK(others[0].gameplayIndex == 6 && others[0].team == 2 && others[0].onlineId == 1 && others[0].isBot &&
          others[0].status == gms::kStatusMatchAccepted);
    CHECK(others[1].gameplayIndex == 2 && others[1].onlineId == 2);
  }
  gms::ShaperSelectConfig cfg;
  CHECK(match.ShaperSelectSetup(&cfg));
  CHECK_EQ(cfg.humanDefault.list.size(), size_t{1});  // alpha.Noun owned by default
  CHECK(cfg.humanDefault.list[0].noun == kAlpha && cfg.humanDefault.list[0].flags == gms::kShaperOwned);
  CHECK_EQ(cfg.players.size(), size_t{2});
  CHECK(cfg.players[0].scripted && cfg.players[0].plan.noun == gms::kNounRandom);
  CHECK_EQ(cfg.timers.pickMs, 62000u);

  gms::InjectSpec s;
  CHECK(gms::ParseInject("picked+10:c20101", &s, &err));  // the new injection points
  CHECK(s.point == gms::SessionPoint::kPicked && s.delayMs == 10);
  CHECK(gms::ParseInject("shaperselect:c20101", &s, &err));
  CHECK(s.point == gms::SessionPoint::kShaperSelect);
}

// D3-live step 4: createGame's roster content and timers (dgserver --shaper-select) as the host's ShaperSelectConfig
// parts (script/roster_shaper.h), from the shared example line of docs/data/host_control_examples.jsonl.
GMS_TEST(ShaperSelectContentFromCreateGame) {
  const std::string line =
      "{\"t\":\"createGame\",\"gameId\":3,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,\"roster\":[{\"user\":"
      "2000000001,\"name\":\"player\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false,\"shapers\":[{\"noun\":\"alpha.Noun\","
      "\"flags\":1},{\"noun\":\"artificer.Noun\",\"flags\":1},{\"noun\":\"gunman.Noun\",\"flags\":2,\"offer\":272547319}],"
      "\"skins\":{\"alpha.Noun\":[\"alpha_thenine.Noun\",\"alpha_seawolf.Noun\"]},\"savedSkins\":{\"alpha.Noun\":"
      "\"alpha_seawolf.Noun\"},\"wards\":[\"wardskin_default.Noun\",\"wardskin_east.Noun\"],\"announcers\":["
      "\"Default.AnnouncerPack\"]},{\"user\":2,\"name\":\"bot\",\"index\":1,\"team\":1,\"noun\":\"\",\"bot\":true}],"
      "\"timers\":{\"RevealTime\":16,\"RolePerkSelectTime\":8000,\"ShaperSelectionTime\":8000,\"SkinSelectTime\":4000},"
      "\"descriptors\":1292,\"shaperSelect\":true}";
  dggms::control::Message m;
  std::string err;
  CHECK(dggms::control::Decode(line, m, &err));
  const auto* g = std::get_if<dggms::control::CreateGame>(&m);
  CHECK(g != nullptr);
  if (!g) return;
  CHECK(g->shaperSelect && g->roster.size() == 2);
  CHECK(gms::HasShaperContent(g->roster[0]) && !gms::HasShaperContent(g->roster[1]));
  const gms::ShaperContent c = gms::ShaperContentFromRoster(g->roster[0]);
  CHECK_EQ(c.list.size(), size_t{3});
  if (c.list.size() == 3) {
    CHECK(c.list[0].noun == kAlpha && c.list[0].flags == gms::kShaperOwned && c.list[0].offer == 0);
    CHECK(c.list[1].noun == kArtificer && c.list[1].flags == gms::kShaperOwned);
    CHECK(c.list[2].noun == kGunman && c.list[2].flags == gms::kShaperFreeRotation && c.list[2].offer == 272547319u);
  }
  const uint32_t nine = gms::AssetIdOf("alpha_thenine.Noun"), seawolf = gms::AssetIdOf("alpha_seawolf.Noun");
  CHECK(c.skins.size() == 1 && c.skins.count(kAlpha) && c.skins.at(kAlpha) == (std::vector<uint32_t>{nine, seawolf}));
  CHECK(c.SkinsFor(kAlpha) == (std::vector<uint32_t>{kAlpha, nine, seawolf}));  // C8 0B: the default skin first
  CHECK(c.savedSkins.size() == 1 && c.savedSkins.count(kAlpha) && c.savedSkins.at(kAlpha) == seawolf);
  CHECK(c.wards == (std::vector<uint32_t>{0x59EF67A8u, gms::AssetIdOf("wardskin_east.Noun")}));  // d3live_run1's C8 0E
  CHECK(c.announcers == (std::vector<uint32_t>{gms::AssetIdOf("Default.AnnouncerPack")}));

  std::string from;
  gms::ShaperSelectTimers t = gms::ShaperTimersFromCreateGame(*g, &from);
  CHECK(t.pickMs == 8000 && t.rolePerkMs == 8000 && t.skinMs == 4000 && t.revealMs == 16);
  CHECK(from.find("practice") == 0 && from.find("RevealTime") != std::string::npos);
  dggms::control::CreateGame pvp;
  pvp.mode = "pvp";
  pvp.timers["SkinSelectTime"] = 1000;
  pvp.timers["RevealTime"] = -1;  // ignored
  t = gms::ShaperTimersFromCreateGame(pvp);
  CHECK(t.pickMs == 62000 && t.rolePerkMs == 45500 && t.skinMs == 1000 && t.revealMs == 16);
  dggms::control::CreateGame practice;
  practice.mode = "practice";
  t = gms::ShaperTimersFromCreateGame(practice);
  CHECK(t.pickMs == 300000 && t.rolePerkMs == 300000 && t.skinMs == 15500 && t.revealMs == 16);

  // the content in a session: the human's own list (not the default alpha), its saved skin at the skin timer
  gms::ShaperSelectConfig cfg;
  cfg.timers = gms::ShaperTimersFromCreateGame(*g);
  gms::ShaperSelectPlayer human;
  human.gameplayIndex = 0;
  human.team = 1;
  human.content = c;
  cfg.players.push_back(human);
  cfg.humanDefault.list.push_back(gms::ShaperListEntry{kAlpha, gms::kShaperOwned, 0});
  QuietLog log;
  gms::ShaperSelect ss(cfg);
  ss.AddHuman(1, 0, 1);
  RecordingSender out;
  ss.OnReady(1, 10000, out);
  const std::vector<std::string> first = out.To(1);
  CHECK(!first.empty() && first[0] == gms::Hex(gms::BuildShaperList(c.list), static_cast<size_t>(-1)));
  for (int64_t at = 10000; at <= 10000 + 20100; at += 100) ss.Tick(at, out);  // every timer runs out
  CHECK(ss.Revealed());
  const std::vector<gms::ShaperPick> picks = ss.Picks();
  // the phase-1 timer locks alpha (the first selectable entry), the skin timer takes the saved skin
  CHECK(picks.size() == 1 && picks[0].shaper == kAlpha && picks[0].skin == seawolf);
}

// D3-perks: createGame's perk pages (docs/data/host_control_examples.jsonl's D3-perks line) as shaper select keeps them,
// C8 09 at the client's C8 02, C8 0A in phases 2-3 only and only for its own pages, and the pick's page with its stats.
// D3c: the page's modifiers a respawn activates again: only those the creature no longer holds, each once, in the
// page's order (vanguard's ProtectDistantAllies ends OnAgentDeath; Adventurer's BushSpeed survives the death).
GMS_TEST(ShaperSelectPageModifiersToRenew) {
  const uint32_t bush = 0xCAA01502u, protect = 0x11227E1Cu, other = 0x12345678u;
  CHECK(gms::PageModifiersToRenew({bush, protect}, {bush}) == (std::vector<uint32_t>{protect}));
  CHECK(gms::PageModifiersToRenew({bush, protect}, {bush, protect, other}).empty());
  CHECK(gms::PageModifiersToRenew({protect, bush}, {}) == (std::vector<uint32_t>{protect, bush}));
  CHECK(gms::PageModifiersToRenew({protect, protect, 0u}, {other}) == (std::vector<uint32_t>{protect}));
  CHECK(gms::PageModifiersToRenew({}, {bush}).empty());
}

// WARDSKIN: only a ward skin Noun goes into a server player's ward slot (ability_Item_Ward creates that object), only
// an AnnouncerPack into its announcer slot; the names as the assets carry them, case-insensitive.
GMS_TEST(ShaperSelectVanityAssetNameFits) {
  CHECK(gms::VanityAssetNameFits(gms::kVanityWard, "wardskin_east.Noun"));
  CHECK(gms::VanityAssetNameFits(gms::kVanityWard, "wardSkin_fish.Noun"));
  CHECK(gms::VanityAssetNameFits(gms::kVanityWard, "wardskin_default.Noun"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityWard, "guardian_spirit.Noun"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityWard, "wardskin_east.CollectionItem"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityWard, "PC_Alpha.AnnouncerPack"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityWard, ""));
  CHECK(gms::VanityAssetNameFits(gms::kVanityAnnouncer, "PC_Alpha.AnnouncerPack"));
  CHECK(gms::VanityAssetNameFits(gms::kVanityAnnouncer, "Default.AnnouncerPack"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityAnnouncer, "wardskin_east.Noun"));
  CHECK(!gms::VanityAssetNameFits(gms::kVanityAnnouncer, ".AnnouncerPack"));
  CHECK(!gms::VanityAssetNameFits(2, "wardskin_east.Noun"));
}

GMS_TEST(ShaperSelectPerkPagesFromCreateGame) {
  const std::string line =
      "{\"t\":\"createGame\",\"gameId\":4,\"level\":\"SM_Map_v5\",\"mode\":\"practice\",\"seed\":7919,\"roster\":[{\"user\":"
      "2000000001,\"name\":\"player\",\"index\":0,\"team\":1,\"noun\":\"\",\"bot\":false,\"shapers\":[{\"noun\":\"alpha.Noun\","
      "\"flags\":1}],\"perkPages\":[{\"id\":1,\"version\":3,\"attributes\":[{\"attribute\":0,\"op\":1,\"milli\":800},"
      "{\"attribute\":0,\"op\":2,\"milli\":50},{\"attribute\":2,\"op\":1,\"milli\":50000}],\"modifiers\":["
      "\"modifier_MediumPerk_BushSpeed\"]},{\"id\":4,\"version\":1}]}],\"shaperSelect\":true}";
  dggms::control::Message m;
  std::string err;
  CHECK(dggms::control::Decode(line, m, &err));
  const auto* g = std::get_if<dggms::control::CreateGame>(&m);
  CHECK(g != nullptr && g->roster.size() == 1);
  if (!g || g->roster.empty()) return;
  const gms::ShaperContent c = gms::ShaperContentFromRoster(g->roster[0]);
  CHECK_EQ(c.pages.size(), size_t{2});
  if (c.pages.size() != 2) return;
  const gms::PerkPage& p1 = c.pages[0];
  CHECK(p1.id == 1 && p1.version == 3 && c.pages[1].id == 4 && c.pages[1].version == 1);
  CHECK(c.pages[1].attributes.empty() && c.pages[1].modifiers.empty());
  CHECK_EQ(p1.attributes.size(), size_t{3});
  if (p1.attributes.size() == 3) {
    // (float)(milli x 0.001), as the client's LoadoutStats_AddPerk stores its rounded sums
    CHECK(p1.attributes[0].attribute == 0 && p1.attributes[0].op == 1 && p1.attributes[0].value == 0.8f);
    CHECK(p1.attributes[1].attribute == 0 && p1.attributes[1].op == 2 && p1.attributes[1].value == 0.05f);
    CHECK(p1.attributes[2].attribute == 2 && p1.attributes[2].op == 1 && p1.attributes[2].value == 50.f);
  }
  // the modifier's ability id: SPID = FNV-1 of the lower-cased name, the id ServerData's chunk carries
  // (Modifiers!modifier_MediumPerk_BushSpeed.lua, instance 00000000CAA01502)
  CHECK(p1.modifiers == (std::vector<uint32_t>{0xCAA01502u}));
  // only the PercentChange entry goes to the creature as a modifier; the Add entries work through the player's loadout
  const std::vector<gms::LoadoutAttribute> mods = gms::LoadoutCreatureModifiers(p1.attributes);
  CHECK(mods.size() == 1 && mods[0].attribute == 0 && mods[0].op == 2);
  CHECK(gms::LoadoutCreatureModifiers({gms::LoadoutAttribute{5, 0, 1.f}}).empty());  // NoOp

  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(1000, 1000, 1000, 16);
  gms::ShaperSelectPlayer human;
  human.gameplayIndex = 0;
  human.team = 1;
  human.content = c;
  cfg.players.push_back(human);
  gms::ShaperSelect ss(cfg);
  ss.AddHuman(5, 0, 1);
  RecordingSender out;
  ss.OnReady(5, 10000, out);
  CHECK_LIST(out.To(5), {"c802ab4a04ef010000000000", "c80300" + Zeros(14), "c809020100000004000000",
                         "c801f82a00000000000001000000"});
  // the live client's 0A right after C8 09 (its drop-down's preselection, d3perks_run1): taken in phase 1 too
  Msg(ss, 5, Req(gms::kShaperPerkPage, 4), 10100, out);
  CHECK(!log.Has("wrong phase"));
  Msg(ss, 5, Req(gms::kShaperLockIn, kAlpha), 10200, out);
  CHECK_EQ(ss.Phase(), 2);
  CHECK(ss.Record(0) && ss.Record(0)->page == 4);
  Msg(ss, 5, Req(gms::kShaperPerkPage, 2), 10300, out);  // not one of its pages
  CHECK(log.Has("not one of its pages"));
  CHECK(ss.Record(0) && ss.Record(0)->page == 4);
  Msg(ss, 5, Req(gms::kShaperPerkPage, 1), 10500, out);  // the last one counts (a drop-down change, then before 08)
  Msg(ss, 5, Req(gms::kShaperLockRole, 0, 2), 10600, out);
  CHECK_EQ(ss.Phase(), 3);
  // the record goes out with the role lock (then phase 3); its bytes 10-13 carry the page (read by nothing)
  const std::vector<std::string> sent = out.To(5);
  CHECK(sent.size() >= 2 && sent[sent.size() - 2] == "c80300ab4a04efab4a04ef020100000003");
  CHECK(!sent.empty() && sent.back().compare(0, 4, "c801") == 0);
  Msg(ss, 5, Req(gms::kShaperPerkPage, 4), 10700, out);  // after the role lock: ignored
  CHECK(log.Has("wrong phase or role locked") && ss.Record(0) && ss.Record(0)->page == 1);
  for (int64_t t = 10700; t <= 12000; t += 100) ss.Tick(t, out);
  CHECK(ss.Revealed());
  const std::vector<gms::ShaperPick> picks = ss.Picks();
  CHECK(picks.size() == 1 && picks[0].page == 1 && picks[0].pageVersion == 3 && picks[0].role == 2);
  if (picks.size() == 1) {
    CHECK(picks[0].loadout.size() == 3 && picks[0].modifiers == p1.modifiers);
    CHECK_HEX(gms::RevealLoadout(picks[0]), "c0020003" "0001cdcc4c3f" "0002cdcc4c3d" "020100004842" "01" "0215a0ca");
  }
  CHECK(ss.Describe().find("r2 p0x00000001") != std::string::npos);
}

// B15b: an observer (a spectator's connection) in two teams' shaper select. Nothing reaches it before its C8 01; then
// every record it knows (both teams: an observer's client has no blind pick) and the current phase; every record change
// of any team, every phase and its repeats, the role toggles of both teams; nothing more after C0 01 (Close). It has no
// record of its own, never starts phase 1 and never holds up a phase; its own requests are ignored.
GMS_TEST(ShaperSelectObserverSeesBothTeams) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(30000, 30000, 30000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1)};
  gms::ShaperSelect ss(cfg);
  const gms::ConnId blue = 11, red = 22, obs = 33;
  ss.AddHuman(blue, 0, 1);
  ss.AddHuman(red, 5, 2);
  ss.AddObserver(obs);
  CHECK(ss.HasObserver(obs) && !ss.HasHuman(obs) && ss.Record(10) == nullptr);
  ss.SetKnown(obs, {0, 5, 10});
  RecordingSender out;
  ss.OnReady(blue, 1000, out);
  CHECK_EQ(ss.Phase(), 1);
  CHECK(out.To(obs).empty());  // before its C8 01: nothing
  Msg(ss, red, Req(gms::kShaperRequest, kArtificer), 1100, out);
  CHECK(out.To(obs).empty());
  ss.OnObserverReady(obs, 1200, out);
  // both teams' current records, then the phase
  CHECK_LIST(out.To(obs), {gms::Hex(gms::BuildShaperRecord(*ss.Record(0))), gms::Hex(gms::BuildShaperRecord(*ss.Record(5))),
                           gms::Hex(gms::BuildShaperPhase(ss.PhaseEnd(), 1))});
  CHECK(ss.Describe().find("1 observer(s)") != std::string::npos);
  ss.OnReady(red, 1250, out);
  // the other team's pick in phase 1 reaches the observer, not the blue human
  size_t mark = out.sent.size();
  Msg(ss, red, Req(gms::kShaperRequest, kAlpha), 1300, out);
  CHECK_LIST(out.To(obs, mark), {gms::Hex(gms::BuildShaperRecord(*ss.Record(5)))});
  CHECK(out.To(blue, mark).empty());
  // a role toggle of either team reaches the observer
  mark = out.sent.size();
  Msg(ss, red, Req(gms::kShaperPreferredRole, 0, 3), 1350, out);
  CHECK_LIST(out.To(obs, mark), {"c8060503"});
  // the observer's own requests are ignored (it has no player)
  mark = out.sent.size();
  Msg(ss, obs, Req(gms::kShaperRequest, kAlpha), 1400, out);
  Msg(ss, obs, Req(gms::kShaperLockIn, kAlpha), 1410, out);
  CHECK(out.To(obs, mark).empty() && ss.Record(5)->shaper == kAlpha);
  CHECK(log.Has("a connection without a player"));
  // the humans go through the phases; the observer gets each phase, its repeats and every record
  Msg(ss, blue, Req(gms::kShaperLockIn, kAlpha), 1500, out);
  Msg(ss, red, Req(gms::kShaperLockIn, kAlpha), 1510, out);
  CHECK_EQ(ss.Phase(), 2);  // the observer holds nothing up
  const size_t quietBefore = out.Quiet();
  ss.Tick(2600, out);
  CHECK(out.Quiet() >= quietBefore + 3);  // the one-second repeat: blue, red and the observer
  Msg(ss, blue, Req(gms::kShaperLockRole, 0, 0), 2700, out);
  Msg(ss, red, Req(gms::kShaperLockRole, 0, 1), 2710, out);
  Msg(ss, blue, Req(gms::kShaperLockSkin, kAlpha), 2800, out);
  Msg(ss, red, Req(gms::kShaperLockSkin, kAlpha), 2810, out);
  CHECK(ss.Revealed());
  std::vector<int> phases;
  for (const std::string& h : out.To(obs)) {
    Bytes m;
    if (h.compare(0, 4, "c801") == 0 && gms::ParseHex(h, m) && m.size() >= 14)
      phases.push_back(static_cast<int>(gms::GetU32LE(m.data() + 10)));
  }
  CHECK(phases == (std::vector<int>{1, 2, 3, 4}));
  // its last copy of each record is the current one
  std::map<int, std::string> last;
  for (const std::string& h : out.To(obs))
    if (h.compare(0, 4, "c803") == 0) last[std::stoi(h.substr(4, 2), nullptr, 16)] = h;
  CHECK_EQ(last.size(), size_t{2});
  for (int i : {0, 5}) CHECK_EQ(last[i], gms::Hex(gms::BuildShaperRecord(*ss.Record(static_cast<uint8_t>(i)))));
  // C0 01 (Close): nothing more
  ss.Close(obs);
  mark = out.sent.size();
  ss.Tick(5000, out);
  for (size_t i = mark; i < out.sent.size(); ++i) CHECK(out.sent[i].conn != obs);
  CHECK(ss.Picks().size() == 2);  // no pick of the observer
  ss.RemoveHuman(obs);
  CHECK(!ss.HasObserver(obs));
}

// B15b: a game of bots that a spectator watches (a custom game with --custom-bots whose admin moved to the
// spectators): no human sends C8 02, so the observer's C8 01 starts phase 1; the bots pick and lock by their plans and
// the phases run to the reveal by themselves.
GMS_TEST(ShaperSelectObserverStartsAGameOfBots) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(30000, 30000, 30000, 16);
  cfg.players.push_back(Scripted(0, 1, kArtificer, 100, 200, 1));
  cfg.players.push_back(Scripted(5, 2, kGunman, 100, 200, 2));
  gms::ShaperSelect ss(cfg);
  const gms::ConnId obs = 33;
  ss.AddObserver(obs);
  RecordingSender out;
  ss.Tick(1000, out);
  CHECK_EQ(ss.Phase(), 0);
  ss.OnObserverReady(obs, 1000, out);
  CHECK_EQ(ss.Phase(), 1);
  CHECK(log.Has("no human player; observer 33's C8 01 starts phase 1"));
  for (int64_t t = 1000; t <= 1400 && !ss.Revealed(); t += 50) ss.Tick(t, out);
  CHECK(ss.Revealed());
  CHECK(ss.Record(0)->shaper == kArtificer && ss.Record(5)->shaper == kGunman);
  std::vector<int> phases;
  for (const std::string& h : out.To(obs)) {
    Bytes m;
    if (h.compare(0, 4, "c801") == 0 && gms::ParseHex(h, m) && m.size() >= 14)
      phases.push_back(static_cast<int>(gms::GetU32LE(m.data() + 10)));
  }
  CHECK(phases == (std::vector<int>{1, 2, 3, 4}));
}

// ---- BOTROLE: a scripted player shows its role from the start, announces it, and adjusts to its human teammate ----------
GMS_TEST(ShaperSelectScriptedRoleIsAnnouncedAndAdjusts) {
  QuietLog log;
  gms::ShaperSelectConfig cfg;
  cfg.timers = Timers(30000, 30000, 10000, 16);
  cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1), Entry(kGunman, 2)};
  gms::ShaperSelectPlayer ally = Scripted(1, 1, kArtificer, 2000, 4000, 1);  // plans the Tactician
  ally.plan.altRoles = 1u << 3;                                               // ... and may play the Predator
  cfg.players.push_back(ally);
  cfg.players.push_back(Scripted(2, 1, kGunman, 2200, 4200, 0));              // a Gladiator, no alternative
  gms::ShaperSelect ss(cfg);
  const gms::ConnId human = 77;
  ss.AddHuman(human, 0, 1);
  RecordingSender out;
  const int64_t s = 10000;
  auto tickTo = [&](int64_t from, int64_t to) {
    for (int64_t t = from; t <= to; t += 100) ss.Tick(t, out);
  };
  Msg(ss, human, Req(gms::kShaperHello), s, out);
  ss.OnReady(human, s, out);
  tickTo(s + 100, s + 5000);
  Msg(ss, human, Req(gms::kShaperRequest, kAlpha), s + 5000, out);
  Msg(ss, human, Req(gms::kShaperLockIn, kAlpha), s + 6000, out);
  CHECK_EQ(ss.Phase(), 2);
  // phase 2 begins: both announce their intended roles (a preferred-role toggle each)
  bool toggled1 = false, toggled2 = false;
  for (const Sent& x : out.sent) {
    toggled1 = toggled1 || x.hex == "c8060101";
    toggled2 = toggled2 || x.hex == "c8060200";
  }
  CHECK(toggled1 && toggled2);
  // the human takes the Tactician; its teammate's Tactician becomes the Predator, the Gladiator stays
  Msg(ss, human, Req(gms::kShaperLockRole, 0, 1), s + 7000, out);
  tickTo(s + 7100, s + 7500);
  const std::vector<gms::ShaperPick> picks = ss.Picks();
  CHECK_EQ(picks.size(), size_t{3});
  for (const gms::ShaperPick& p : picks) {
    if (p.gameplayIndex == 1) CHECK_EQ(static_cast<int>(p.role), 3);
    if (p.gameplayIndex == 2) CHECK_EQ(static_cast<int>(p.role), 0);
  }
  // the change is told as two toggles: the old role off, the new one on
  bool off = false, on = false;
  for (const Sent& x : out.sent) {
    off = off || x.hex == "c8060101";
    on = on || x.hex == "c8060103";
  }
  CHECK(off && on);
}

// ---- BOTDRAFT: a custom game's bots pick after the humans --------------------------------------------------------------
namespace {

struct DraftRig {
  std::vector<std::vector<gms::ShaperDraftHuman>> calls;
  gms::ShaperSelectConfig cfg;
  explicit DraftRig(int graceMs) {
    cfg.timers = Timers(30000, 30000, 10000, 16);
    cfg.humanDefault.list = {Entry(kAlpha, 1), Entry(kArtificer, 1), Entry(kGunman, 2)};
    cfg.draftGraceMs = graceMs;
    for (uint8_t i : {uint8_t{1}, uint8_t{2}}) {  // bot 1 on the human's team, bot 2 on the other
      gms::ShaperSelectPlayer b = Scripted(i, i == 1 ? 1 : 2, 0, 1000, 2500, 0);
      b.plan.draft = true;
      cfg.players.push_back(b);
    }
    // the draft: the human's team's bot takes the Hunter unless the human said Hunter (then the Gladiator), and a
    // shaper the human didn't pick
    cfg.draft = [this](const std::vector<gms::ShaperDraftHuman>& humans, const std::vector<gms::ShaperDraftBot>& bots) {
      calls.push_back(humans);
      bool hunter = false;
      uint32_t taken = 0;
      for (const gms::ShaperDraftHuman& h : humans) {
        hunter = hunter || (h.preferredRoles & 4) || h.roleCard == 2;
        taken = h.shaper;
      }
      std::vector<gms::ShaperDraftPick> out;
      for (const gms::ShaperDraftBot& b : bots) {
        gms::ShaperDraftPick k;
        k.gameplayIndex = b.gameplayIndex;
        k.noun = taken == kArtificer ? kGunman : kArtificer;
        k.role = static_cast<uint8_t>(b.team == 1 && hunter ? 0 : 2);
        out.push_back(k);
      }
      return out;
    };
  }
  DraftRig(const DraftRig&) = delete;
  DraftRig& operator=(const DraftRig&) = delete;
};

bool SentRecordWithShaper(const RecordingSender& out, uint8_t index) {
  char head[8];
  std::snprintf(head, sizeof head, "c803%02x", index);
  for (const Sent& x : out.sent)
    if (x.hex.compare(0, 6, head) == 0 && x.hex.compare(6, 8, "00000000") != 0) return true;
  return false;
}

}  // namespace

// The bots show nothing until the human locked and said its role (a preferred-role box); then they draw at once, pick,
// say their roles and lock; the human's Hunter keeps the bot teammate off the Hunter.
GMS_TEST(ShaperSelectDraftWaitsForTheHumans) {
  QuietLog log;
  DraftRig rig(5000);
  gms::ShaperSelect ss(rig.cfg);
  const gms::ConnId human = 77;
  ss.AddHuman(human, 0, 1);
  RecordingSender out;
  const int64_t s = 10000;
  auto tickTo = [&](int64_t from, int64_t to) {
    for (int64_t t = from; t <= to; t += 100) ss.Tick(t, out);
  };
  Msg(ss, human, Req(gms::kShaperHello), s, out);
  ss.OnReady(human, s, out);
  tickTo(s + 100, s + 8000);  // long past the bots' own pick and lock times
  CHECK(!SentRecordWithShaper(out, 1));
  CHECK(!SentRecordWithShaper(out, 2));
  CHECK_EQ(ss.Record(1)->shaper, 0u);
  CHECK_EQ(ss.Phase(), 1);
  CHECK(rig.calls.empty());
  Msg(ss, human, Req(gms::kShaperPreferredRole, 0, 2), s + 8000, out);  // the Hunter box
  Msg(ss, human, Req(gms::kShaperRequest, kArtificer), s + 8100, out);
  CHECK(rig.calls.empty());
  Msg(ss, human, Req(gms::kShaperLockIn, kArtificer), s + 8200, out);
  CHECK_EQ(rig.calls.size(), size_t{1});  // said its role: no grace
  CHECK_EQ(rig.calls[0].size(), size_t{1});
  CHECK_EQ(rig.calls[0][0].shaper, kArtificer);
  CHECK_EQ(static_cast<int>(rig.calls[0][0].preferredRoles), 4);
  tickTo(s + 8300, s + 12000);
  CHECK_EQ(ss.Phase(), 2);
  CHECK_EQ(ss.Record(1)->shaper, kGunman);
  CHECK_EQ(static_cast<int>(ss.Record(1)->role), 0);  // the human is the Hunter
  CHECK_EQ(static_cast<int>(ss.Record(2)->role), 2);
  bool toggle = false;
  for (const Sent& x : out.sent) toggle = toggle || (x.conn == human && x.hex == "c8060100");
  CHECK(toggle);  // the bot teammate said its role to the human
  CHECK(log.Has("BOTDRAFT: the bots draw now"));
}

// Without a role said, the draft waits the grace after the last lock; a role card chosen meanwhile counts (the
// client's own first C8 07 does not).
GMS_TEST(ShaperSelectDraftGraceAndRoleCard) {
  QuietLog log;
  DraftRig rig(4000);
  gms::ShaperSelect ss(rig.cfg);
  const gms::ConnId human = 77;
  ss.AddHuman(human, 0, 1);
  RecordingSender out;
  const int64_t s = 10000;
  Msg(ss, human, Req(gms::kShaperHello), s, out);
  ss.OnReady(human, s, out);
  Msg(ss, human, Req(gms::kShaperRequest, kAlpha), s + 1000, out);
  Msg(ss, human, Req(gms::kShaperLockIn, kAlpha), s + 1100, out);
  Msg(ss, human, Req(gms::kShaperSelectRole, 0, 0), s + 1150, out);  // the client's own, on entering its phase 2
  for (int64_t t = s + 1200; t <= s + 4000; t += 100) ss.Tick(t, out);
  CHECK(rig.calls.empty());
  Msg(ss, human, Req(gms::kShaperSelectRole, 0, 2), s + 4100, out);  // the player clicks the Hunter card
  CHECK_EQ(rig.calls.size(), size_t{1});
  CHECK_EQ(rig.calls[0][0].roleCard, 2);
  // and the grace alone: a second match where the human says nothing
  DraftRig rig2(4000);
  gms::ShaperSelect ss2(rig2.cfg);
  ss2.AddHuman(human, 0, 1);
  RecordingSender out2;
  Msg(ss2, human, Req(gms::kShaperHello), s, out2);
  ss2.OnReady(human, s, out2);
  Msg(ss2, human, Req(gms::kShaperLockIn, kAlpha), s + 1000, out2);
  for (int64_t t = s + 1100; t <= s + 4900; t += 100) ss2.Tick(t, out2);
  CHECK(rig2.calls.empty());
  ss2.Tick(s + 5000, out2);
  CHECK_EQ(rig2.calls.size(), size_t{1});
  CHECK_EQ(rig2.calls[0][0].roleCard, -1);
}

// The phase-1 timer: the human's record is locked first (the draft sees its shaper), then the bots draw and lock.
GMS_TEST(ShaperSelectDraftAtTheTimer) {
  QuietLog log;
  DraftRig rig(5000);
  gms::ShaperSelect ss(rig.cfg);
  const gms::ConnId human = 77;
  ss.AddHuman(human, 0, 1);
  RecordingSender out;
  const int64_t s = 10000;
  Msg(ss, human, Req(gms::kShaperHello), s, out);
  ss.OnReady(human, s, out);
  Msg(ss, human, Req(gms::kShaperRequest, kArtificer), s + 1000, out);
  for (int64_t t = s + 1100; t <= s + 30000; t += 100) ss.Tick(t, out);
  CHECK_EQ(ss.Phase(), 2);
  CHECK_EQ(rig.calls.size(), size_t{1});
  CHECK_EQ(rig.calls[0][0].shaper, kArtificer);
  CHECK_EQ(ss.Record(1)->shaper, kGunman);
  CHECK((ss.Record(1)->flags & gms::kRecordShaperLocked) != 0);
  CHECK((ss.Record(2)->flags & gms::kRecordShaperLocked) != 0);
}
