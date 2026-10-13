// the strict check of every client -> GMS message (core/protocol/check.h): the live client's messages pass, every message type has its malformed inputs, the world's part (caster,
// targets, inventory), the guard (counts, the kick) and the session around it (dropped, answered, kicked, mode 2 and 0).
#include <cmath>
#include <cstring>
#include <limits>
#include <map>
#include <set>
#include <string>
#include <vector>

#include "core/protocol/check.h"
#include "core/protocol/commands.h"
#include "core/protocol/messages.h"
#include "core/protocol/wire.h"
#include "core/session/gms_session.h"
#include "tests/check.h"

using gms::Bytes;
using gms::check::Verdict;

namespace {

// One message of each kind the live client sent, from the "<-" lines of the sessions' logs (2026-10-13 census; casters,
// targets and positions as the client wrote them).
const char* const kLiveClient[] = {
    "810100000000000000a2c5000000000000",  // HelloReq (user 1, playgroup 50594)
    "880000010000000000",                  // status 0x10000 (accepted)
    "88010400000000803f",                  // 0x401, progress 1.0
    "888000000000000000",                  // 0x80
    "a6010250c3350171a6000000001e9753c396656cc39933333f0000000000000000",  // move
    "a60200803f2e01777c2cfc190008d4a7230100000008d4a72301000000f815f24b",  // stop moving (data never written)
    "a60300cdc42e0177a60000000000000000000000000000000000000000000000000000000004000000000000000000000000004f23",
    "a6070200002d01787ca1ea5fc3146b66c3000000000000000003000000",  // ping
    "a608028f002d01787c",                                          // stop
    "a609056cc3350171a6000000001e9753c396656cc39933333f0000000000000000",  // attack-move
    "a60a0200002e01777c0d000000",                                  // voice line 13
    "a60b038f002d01787c",                                          // hold
    "a703000000", "a704000000", "a705000000", "a706000000",        // rank-ups
    "c004", "c101", "c1030000", "c1030100",                        // pre-game 04, post-game 01 and votes
    "c3010367208d0600000000ff",                                    // purchase Spell_Blink into slot 6
    "c301fad45e590000000000ff",                                    // purchase into slot 0
    "c30347f9a0110900000000ff",                                    // use the potion in slot 9
    "c7d4d8b35100000000",                                          // ClockSync
    "c801", "c802", "c804e6fa88af", "c805e6fa88af", "c80603", "c80700", "c80702", "c80802", "c80a01000000",
    "c80ce6fa88af", "c80e00a867ef59",                              // shaper select
    "ca01", "ca02", "cb0000", "cc01",
};

Bytes FromHex(const char* hex) {
  Bytes b;
  gms::ParseHex(hex, b);
  return b;
}

gms::check::Result Fmt(const Bytes& m) {
  return gms::check::CheckFormat(m[0], m.data() + 1, m.size() - 1, gms::check::BoundsForLevel(gms::kLevelSmMapV5));
}

Verdict V(const Bytes& m) { return Fmt(m).verdict; }

gms::ActionCommand Cmd(uint8_t type) {
  gms::ActionCommand c;
  c.type = type;
  c.seq = 7;
  c.caster = 0x7c770130;
  c.pos = {10.f, -20.f, 1.f};
  c.aim = c.targetPos = c.pos;
  if (type == gms::kCmdAbility) c.slot = 3;
  return c;
}

Bytes WithByte(Bytes m, size_t at, uint8_t v) {
  m[at] = v;
  return m;
}

Bytes Cut(Bytes m, size_t n) {
  m.resize(n);
  return m;
}

Bytes Plus(Bytes m, uint8_t extra = 0) {
  m.push_back(extra);
  return m;
}

}  // namespace

GMS_TEST(CheckAcceptsTheLiveClientsMessages) {
  for (const char* hex : kLiveClient) {
    const Bytes m = FromHex(hex);
    const gms::check::Result r = Fmt(m);
    if (!r.ok()) std::printf("  %s: %s %s\n", hex, gms::check::VerdictName(r.verdict), r.reason.c_str());
    CHECK(r.ok());
  }
  // every built command type the client sends passes too
  for (int t : {1, 2, 3, 5, 6, 7, 8, 9, 10, 11}) {
    gms::ActionCommand c = Cmd(static_cast<uint8_t>(t));
    if (t == gms::kCmdVoiceLine) c.value = 9;
    CHECK(Fmt(gms::BuildActionCommand(c)).ok());
  }
  gms::ActionCommand recall = Cmd(gms::kCmdAbility);
  recall.slot = -1;
  recall.guid = gms::kGuidReturnToBase;
  CHECK(Fmt(gms::BuildActionCommand(recall)).ok());
  CHECK_EQ(gms::check::ShopItems().size(), size_t(104));
  CHECK(gms::check::IsShopItem(gms::AssetIdOf("Spell_Blink.Item")));
  CHECK(!gms::check::IsShopItem(gms::AssetIdOf("Consumable_Debug_GrantGold.Item")));
  CHECK(!gms::check::IsShopItem(0));
}

GMS_TEST(CheckBoundsOfTheLevel) {
  const gms::check::Bounds b = gms::check::BoundsForLevel(gms::kLevelSmMapV5);
  CHECK(b.Contains(-246.f, 245.8f, 7.8f));
  CHECK(b.Contains(264.f, -264.f, 0.f));
  CHECK(!b.Contains(266.f, 0.f, 0.f));
  CHECK(!b.Contains(0.f, 0.f, 400.f));
  const gms::check::Bounds unknown = gms::check::BoundsForLevel(0x12345678);
  CHECK(unknown.Contains(299.f, -299.f, 0.f) && !unknown.Contains(301.f, 0.f, 0.f));
}

GMS_TEST(CheckMalformedHelloStatusClockSync) {
  const Bytes hello = FromHex("810100000000000000a2c5000000000000");
  CHECK(V(Cut(hello, 1)) == Verdict::kMalformed);
  CHECK(V(Cut(hello, 9)) == Verdict::kMalformed);  // the old parser took 8 bytes
  CHECK(V(Plus(hello)) == Verdict::kMalformed);
  CHECK(V(FromHex("8100000000000000000000000000000000")) == Verdict::kInvalid);  // user 0

  const Bytes status = FromHex("88010400000000803f");
  CHECK(V(Cut(status, 5)) == Verdict::kMalformed);
  CHECK(V(Plus(status)) == Verdict::kMalformed);
  for (uint32_t gmsBit : {0x4u, 0x8u, 0x10u, 0x20u, 0x100u, 0x40000000u}) {
    Bytes m = status;
    std::memcpy(&m[1], &gmsBit, 4);
    CHECK(V(m) == Verdict::kInvalid);
  }
  for (float p : {-0.5f, 1.5f, std::numeric_limits<float>::quiet_NaN(), std::numeric_limits<float>::infinity()}) {
    Bytes m = status;
    std::memcpy(&m[5], &p, 4);
    CHECK(V(m) == Verdict::kInvalid);
  }

  const Bytes clock = FromHex("c7d4d8b35100000000");
  CHECK(V(Cut(clock, 8)) == Verdict::kMalformed);
  CHECK(V(Plus(clock)) == Verdict::kMalformed);
  CHECK(V(FromHex("c7ffffffffffffffff")) == Verdict::kInvalid);  // a negative clock
}

GMS_TEST(CheckMalformedActionCommands) {
  // sizes: every type one byte short and one too long, an unknown type
  for (uint8_t t = 1; t <= 11; ++t) {
    if (t == gms::kCmdInteract) continue;
    const Bytes m = gms::BuildActionCommand(Cmd(t));
    CHECK(V(Cut(m, m.size() - 1)) == Verdict::kMalformed);
    CHECK(V(Plus(m)) == Verdict::kMalformed);
  }
  CHECK(V(FromHex("a601")) == Verdict::kMalformed);
  CHECK(V(FromHex("a60c070000300177 7c")) == Verdict::kMalformed);  // type 12
  CHECK(V(FromHex("a6000700003001777c")) == Verdict::kMalformed);   // type 0
  // the caster
  gms::ActionCommand c = Cmd(gms::kCmdMove);
  c.caster = 0;
  CHECK(V(gms::BuildActionCommand(c)) == Verdict::kInvalid);
  c.caster = 0x00010400;  // slot 1024
  CHECK(V(gms::BuildActionCommand(c)) == Verdict::kInvalid);
  // positions: NaN, infinite, outside the level, for moves, attack-moves, pings and both points of an ability
  const float bad[] = {std::numeric_limits<float>::quiet_NaN(), std::numeric_limits<float>::infinity(), 300.f, -1e9f};
  for (float f : bad) {
    for (uint8_t t : {gms::kCmdMove, gms::kCmdAttackMove, gms::kCmdPing}) {
      gms::ActionCommand m = Cmd(t);
      m.pos.x = f;
      CHECK(V(gms::BuildActionCommand(m)) == Verdict::kInvalid);
    }
    gms::ActionCommand a = Cmd(gms::kCmdAbility);
    a.aim.y = f;
    CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
    a = Cmd(gms::kCmdAbility);
    a.targetPos.z = f == 300.f ? 400.f : f;  // z may reach 300
    CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  }
  c = Cmd(gms::kCmdMove);
  c.moveFlags = 2;
  CHECK(V(gms::BuildActionCommand(c)) == Verdict::kInvalid);
  // ability: slots, the GUID rule, flags, the target
  gms::ActionCommand a = Cmd(gms::kCmdAbility);
  for (int32_t slot : {-2, 12, 100, 0x7fffffff}) {
    a.slot = slot;
    CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  }
  a = Cmd(gms::kCmdAbility);
  a.guid = 0x12345678;  // a slot and a GUID
  CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  a.slot = -1;  // a GUID that isn't return to base
  CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  a.guid = 0;  // neither
  CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  for (int f = 0; f < 3; ++f) {
    a = Cmd(gms::kCmdAbility);
    if (f == 0) a.keepAttacking = 2;
    if (f == 1) a.walkUp = 2;
    if (f == 2) a.reactivation = 9;
    CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  }
  a = Cmd(gms::kCmdAbility);
  a.target = 0x7c7f0400;  // slot 1024
  CHECK(V(gms::BuildActionCommand(a)) == Verdict::kInvalid);
  // interact (no interactable exists), ping type and target, voice lines
  CHECK(V(gms::BuildActionCommand(Cmd(gms::kCmdInteract))) == Verdict::kInvalid);
  gms::ActionCommand p = Cmd(gms::kCmdPing);
  p.value = 5;
  CHECK(V(gms::BuildActionCommand(p)) == Verdict::kInvalid);
  p.value = 1;
  p.target = 0xFFFFFFFF;
  CHECK(V(gms::BuildActionCommand(p)) == Verdict::kInvalid);
  gms::ActionCommand v = Cmd(gms::kCmdVoiceLine);
  for (uint32_t line : {6u, 7u, 14u, 0xFFFFFFFFu}) {
    v.value = line;
    CHECK(V(gms::BuildActionCommand(v)) == Verdict::kInvalid);
  }
}

GMS_TEST(CheckMalformedRankUpAndItems) {
  CHECK(V(FromHex("a7030000")) == Verdict::kMalformed);
  CHECK(V(FromHex("a70300000000")) == Verdict::kMalformed);
  for (const char* bad : {"a700000000", "a702000000", "a707000000", "a7ffffffff"}) CHECK(V(FromHex(bad)) == Verdict::kInvalid);

  const Bytes buy = FromHex("c3010367208d0600000000ff");
  CHECK(V(Cut(buy, 11)) == Verdict::kMalformed);
  CHECK(V(Plus(buy)) == Verdict::kMalformed);
  CHECK(V(WithByte(buy, 1, 0)) == Verdict::kInvalid);   // type 0
  CHECK(V(WithByte(buy, 1, 6)) == Verdict::kInvalid);   // type 6
  CHECK(V(WithByte(buy, 2, 0x11)) == Verdict::kInvalid);  // not in the shop's table
  CHECK(V(WithByte(buy, 6, 12)) == Verdict::kInvalid);  // slot 12
  CHECK(V(WithByte(buy, 7, 1)) == Verdict::kInvalid);   // a second item that isn't one
  CHECK(V(WithByte(buy, 11, 0x20)) == Verdict::kInvalid);  // second slot 32
  gms::ItemTransactionRequest debugGold;
  debugGold.type = gms::kItemPurchase;
  debugGold.item = gms::AssetIdOf("Consumable_Debug_GrantGold.Item");
  debugGold.slot = gms::kItemSlotNone;
  debugGold.slot2 = 0xFF;
  CHECK(V(gms::BuildItemTransaction(debugGold)) == Verdict::kInvalid);
  const Bytes use = FromHex("c30347f9a0110900000000ff");
  CHECK(V(WithByte(use, 6, 11)) == Verdict::kInvalid);  // use needs a real slot
  gms::ItemTransactionRequest swap;
  swap.type = gms::kItemSwap;
  swap.slot = 6;
  swap.slot2 = 7;
  CHECK(Fmt(gms::BuildItemTransaction(swap)).ok());
  swap.slot2 = 11;
  CHECK(V(gms::BuildItemTransaction(swap)) == Verdict::kInvalid);
}

GMS_TEST(CheckMalformedPreGamePostGameShaperSelect) {
  CHECK(V(FromHex("c0")) == Verdict::kMalformed);
  CHECK(V(FromHex("c001")) == Verdict::kMalformed);  // GMS -> client subtypes
  CHECK(V(FromHex("c003")) == Verdict::kMalformed);
  CHECK(V(FromHex("c00400")) == Verdict::kMalformed);
  CHECK(V(FromHex("c1")) == Verdict::kMalformed);
  CHECK(V(FromHex("c10100")) == Verdict::kMalformed);
  CHECK(V(FromHex("c10300")) == Verdict::kMalformed);
  CHECK(V(FromHex("c103000000")) == Verdict::kMalformed);
  CHECK(V(FromHex("c104")) == Verdict::kMalformed);
  CHECK(V(FromHex("c1030004")) == Verdict::kInvalid);  // a vote for player 10
  CHECK(V(FromHex("c103ffff")) == Verdict::kInvalid);
  // shaper select: every client subtype one byte short and long, the GMS's own subtypes, roles, vanity kinds
  for (const char* ok : {"c801", "c802", "c804e6fa88af", "c805e6fa88af", "c80603", "c80700", "c80802", "c80a01000000",
                         "c80ce6fa88af", "c80de6fa88af", "c80e00a867ef59", "c804ffffffff"}) {
    const Bytes m = FromHex(ok);
    CHECK(Fmt(m).ok());
    CHECK(V(Plus(m)) == Verdict::kMalformed);
    if (m.size() > 2) CHECK(V(Cut(m, m.size() - 1)) == Verdict::kMalformed);
  }
  for (const char* gmsOnly : {"c803", "c809", "c80b", "c80f", "c800", "c8"}) CHECK(V(FromHex(gmsOnly)) == Verdict::kMalformed);
  for (const char* bad : {"c80604", "c807ff", "c80809", "c80e02a867ef59"}) CHECK(V(FromHex(bad)) == Verdict::kInvalid);
}

GMS_TEST(CheckMalformedSurrenderFriendDemoPauseAndTheRest) {
  CHECK(V(FromHex("ca")) == Verdict::kMalformed);
  CHECK(V(FromHex("ca0100")) == Verdict::kMalformed);
  for (const char* bad : {"ca00", "ca03", "caff"}) CHECK(V(FromHex(bad)) == Verdict::kInvalid);
  CHECK(V(FromHex("cb00")) == Verdict::kMalformed);
  CHECK(V(FromHex("cb000000")) == Verdict::kMalformed);
  CHECK(V(FromHex("cb0080")) == Verdict::kInvalid);
  CHECK(Fmt(FromHex("cbff7f")).ok());
  CHECK(V(FromHex("cc")) == Verdict::kMalformed);
  CHECK(V(FromHex("cc0101")) == Verdict::kMalformed);
  CHECK(V(FromHex("cc02")) == Verdict::kInvalid);
  CHECK(Fmt(FromHex("cd0001")).ok());
  CHECK(V(FromHex("cd00")) == Verdict::kMalformed);
  CHECK(V(FromHex("cd0101")) == Verdict::kMalformed);
  CHECK(V(FromHex("cd0002")) == Verdict::kInvalid);
  CHECK(V(FromHex("b80000")) == Verdict::kInvalid);  // CheatMessage
  CHECK(V(FromHex("be0000000000000000")) == Verdict::kInvalid);  // DebugPing
  // every GMS -> client message from a client is malformed; every type is covered by one of the tests above
  std::set<uint8_t> client = {0x81, 0x88, 0xA6, 0xA7, 0xB8, 0xBE, 0xC0, 0xC1, 0xC3, 0xC7, 0xC8, 0xCA, 0xCB, 0xCC, 0xCD};
  for (const gms::GmsMessageType& t : gms::kGmsMessageTypes) {
    if (client.count(t.wire)) continue;
    const Bytes m = {t.wire, 0, 0, 0, 0};
    CHECK(V(m) == Verdict::kMalformed);
    CHECK(V(Bytes{t.wire}) == Verdict::kMalformed);
  }
  CHECK(V(Bytes{0x13, 0}) == Verdict::kMalformed);
}

namespace {

struct FakeWorld final : gms::check::IWorld {
  bool player = true;
  uint32_t creature = 0x7c770130;
  std::map<uint32_t, gms::check::TargetView> targets;
  gms::check::InventoryView inv;
  bool Sender(gms::ConnId, gms::check::SenderView* out) override {
    out->player = player;
    out->gameplayIndex = 0;
    out->creature = creature;
    return true;
  }
  gms::check::TargetView Target(gms::ConnId, uint32_t id) override {
    auto it = targets.find(id);
    return it == targets.end() ? gms::check::TargetView::kGone : it->second;
  }
  bool Inventory(gms::ConnId, gms::check::InventoryView* out) override {
    *out = inv;
    return true;
  }
};

Verdict W(const Bytes& m, FakeWorld& w) {
  gms::check::Result r = Fmt(m);
  if (!r.ok()) return r.verdict;
  return gms::check::CheckWorld(m[0], m.data() + 1, m.size() - 1, 1, w).verdict;
}

}  // namespace

GMS_TEST(CheckWorldCasterTargetsInventory) {
  FakeWorld w;
  gms::ActionCommand a = Cmd(gms::kCmdAbility);
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kOk);
  a.caster = 0x7c770131;  // another creature
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kInvalid);
  a = Cmd(gms::kCmdAbility);
  w.creature = 0;  // not deployed yet
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kStale);
  w.creature = 0x7c770130;
  const uint32_t enemy = 0x12340042, ghost = 0x12340043, hidden = 0x12340044, never = 0x12340045;
  w.targets[enemy] = gms::check::TargetView::kVisible;
  w.targets[hidden] = gms::check::TargetView::kHidden;
  w.targets[never] = gms::check::TargetView::kUnknown;
  a.target = enemy;
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kOk);
  a.target = ghost;
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kStale);
  a.target = hidden;
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kStale);
  a.target = never;
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kInvalid);
  a.target = w.creature;  // a self-cast at its own creature
  CHECK(W(gms::BuildActionCommand(a), w) == Verdict::kOk);
  gms::ActionCommand p = Cmd(gms::kCmdPing);
  p.target = never;
  CHECK(W(gms::BuildActionCommand(p), w) == Verdict::kInvalid);
  // a spectator's commands, rank-ups and items
  w.player = false;
  CHECK(W(gms::BuildActionCommand(Cmd(gms::kCmdStop)), w) == Verdict::kInvalid);
  CHECK(W(FromHex("a703000000"), w) == Verdict::kInvalid);
  CHECK(W(FromHex("ca01"), w) == Verdict::kOk);  // not the world's part
  w.player = true;
  // sell, use and consume need the item in the slot
  const Bytes use = FromHex("c30347f9a0110900000000ff");
  CHECK(W(use, w) == Verdict::kStale);
  w.inv.item[9] = gms::GetU32LE(&use[2]);
  w.inv.count[9] = 2;
  CHECK(W(use, w) == Verdict::kOk);
  w.inv.count[9] = 0;
  CHECK(W(use, w) == Verdict::kStale);
  CHECK(W(FromHex("c3010367208d0600000000ff"), w) == Verdict::kOk);  // a purchase needs nothing in the slot
}

GMS_TEST(CheckGuardCountsAndKicks) {
  gms::check::GuardOptions o;
  o.kickCount = 3;
  o.kickWindowMs = 1000;
  gms::check::Guard g(o);
  CHECK(!g.Note(1, 0xA6, Verdict::kOk, 0));
  CHECK(!g.Note(1, 0xA6, Verdict::kStale, 1));  // stale never counts towards the kick
  CHECK(!g.Note(1, 0xA6, Verdict::kStale, 2));
  CHECK(!g.Note(1, 0xA6, Verdict::kStale, 3));
  CHECK(!g.Note(1, 0xA6, Verdict::kInvalid, 10));
  CHECK(!g.Note(1, 0xC3, Verdict::kMalformed, 20));
  CHECK(!g.Note(2, 0xC3, Verdict::kMalformed, 20));  // another connection
  CHECK(g.Note(1, 0xA6, Verdict::kInvalid, 30));     // the third within the window
  CHECK(!g.Note(3, 0xA6, Verdict::kInvalid, 0));
  CHECK(!g.Note(3, 0xA6, Verdict::kInvalid, 600));
  CHECK(!g.Note(3, 0xA6, Verdict::kInvalid, 1700));  // the first two left the window
  CHECK(!g.Note(3, 0xA6, Verdict::kInvalid, 1800));
  CHECK(g.Note(3, 0xA6, Verdict::kInvalid, 1900));
  CHECK(g.NoteOther(4, 0x50, 0) == false);
  const gms::check::Stats t = g.Total();
  CHECK_EQ(t.checked, uint64_t(13));
  CHECK_EQ(t.ok, uint64_t(1));
  CHECK_EQ(t.stale, uint64_t(3));
  CHECK_EQ(t.invalid, uint64_t(7));
  CHECK_EQ(t.malformed, uint64_t(2));
  CHECK_EQ(t.kicked, uint64_t(2));
  CHECK_EQ(t.other, uint64_t(1));
  CHECK_EQ(t.badByWire.at(0xA6), uint64_t(10));
  const gms::check::Stats w = g.TakeWindow();
  CHECK_EQ(w.Bad(), uint64_t(12));
  CHECK_EQ(g.TakeWindow().checked, uint64_t(0));
  CHECK(t.Describe().find("dropped 12 (malformed 2, invalid 7, stale 3; 50 x1 a6 x10 c3 x2), RakNet other 1, kicked 2") !=
        std::string::npos);
}

namespace {

struct Net final : gms::ITransport {
  std::vector<std::pair<gms::ConnId, Bytes>> sent;
  std::vector<gms::ConnId> closed;
  bool Send(gms::ConnId conn, const Bytes& msg) override {
    sent.push_back({conn, msg});
    return true;
  }
  void Close(gms::ConnId conn) override { closed.push_back(conn); }
  int Count(uint8_t wire) const {
    int n = 0;
    for (const auto& s : sent) n += !s.second.empty() && s.second[0] == wire;
    return n;
  }
};

struct CheckMatch final : gms::IMatch {
  FakeWorld world;
  bool useWorld = true;
  std::vector<Bytes> got;  // the client messages the match was handed
  int disconnected = 0;
  gms::GameSetup Setup() override { return gms::GameSetup(); }
  bool OnHelloReq(gms::ConnId, const gms::HelloReq&, gms::PlayerSlot* slot) override {
    slot->gameplayIndex = 0;
    return true;
  }
  void OnPoint(gms::ConnId, gms::SessionPoint, gms::ISender&) override {}
  void OnClientMessage(gms::ConnId, uint8_t wire, const uint8_t* body, size_t n) override {
    Bytes m{wire};
    m.insert(m.end(), body, body + n);
    got.push_back(m);
  }
  void OnDisconnected(gms::ConnId) override { ++disconnected; }
  gms::check::IWorld* CheckWorld() override { return useWorld ? &world : nullptr; }
};

void Deliver(gms::GmsSession& s, gms::ConnId conn, const Bytes& m) { s.OnMessage(conn, m.data(), m.size()); }

}  // namespace

GMS_TEST(CheckInTheSession) {
  Net net;
  CheckMatch match;
  gms::SessionOptions so;
  so.stopAfter = gms::kStepHello;
  so.check.kickCount = 4;
  gms::GmsSession s(net, match, so, gms::SteadyClock::now());
  s.OnConnected(5, "127.0.0.1|1");
  // before the HelloReq is answered nothing else counts
  Deliver(s, 5, FromHex("a703000000"));
  CHECK(match.got.empty());
  Deliver(s, 5, FromHex("810100000000000000a2c5000000000000"));
  CHECK_EQ(net.Count(gms::kGmsHelloPlayer), 1);
  // a good command reaches the match; a malformed one doesn't
  Deliver(s, 5, FromHex("a703000000"));
  CHECK_EQ(match.got.size(), size_t(1));
  Deliver(s, 5, FromHex("a70300"));
  Deliver(s, 5, FromHex("a709000000"));
  CHECK_EQ(match.got.size(), size_t(1));
  // a stale ability (its target died) is answered as a refusal: B4 kind 2 with its sequence, result 0
  gms::ActionCommand a = Cmd(gms::kCmdAbility);
  a.target = 0x12340042;
  Deliver(s, 5, gms::BuildActionCommand(a));
  CHECK_EQ(match.got.size(), size_t(1));
  CHECK_EQ(net.Count(gms::kGmsActionCommandResponse), 1);
  gms::ActionCommandResponse r;
  const Bytes& b4 = net.sent.back().second;
  CHECK(gms::ParseActionCommandResponse(b4.data() + 1, b4.size() - 1, &r));
  CHECK_EQ(int(r.kind), int(gms::kResponseFailed));
  CHECK_EQ(int(r.seq), 7);
  CHECK_EQ(r.commandType, uint32_t(3));
  // a used-up potion: C3 success 0
  Deliver(s, 5, FromHex("c30347f9a0110900000000ff"));
  CHECK_EQ(net.Count(gms::kGmsItemTransaction), 1);
  CHECK_EQ(int(net.sent.back().second.back()), 0);
  // the third and fourth malformed/invalid ones (the HelloReq-less one and two above count) kick the connection
  CHECK(net.closed.empty());
  Deliver(s, 5, FromHex("b80000"));
  CHECK_EQ(net.closed.size(), size_t(1));
  CHECK_EQ(s.NumConnections(), size_t(0));
  CHECK_EQ(match.disconnected, 1);
  Deliver(s, 5, FromHex("a703000000"));  // nothing more from it
  CHECK_EQ(match.got.size(), size_t(1));
  const gms::check::Stats t = s.CheckTotals();
  CHECK_EQ(t.kicked, uint64_t(1));
  CHECK_EQ(t.stale, uint64_t(2));
  CHECK_EQ(t.invalid + t.malformed, uint64_t(4));
  // another RakNet id from a connection: counted, kicks too
  s.OnConnected(6, "127.0.0.1|2");
  const uint8_t other[] = {0x50, 1, 2};
  for (int i = 0; i < 4; ++i) s.OnOtherPacket(6, other, sizeof other);
  CHECK_EQ(net.closed.size(), size_t(2));
  s.OnOtherPacket(999, other, sizeof other);  // an unknown sender: counted only
  CHECK_EQ(s.CheckTotals().other, uint64_t(5));
}

GMS_TEST(CheckModesTwoAndOff) {
  for (int mode : {2, 0}) {
    Net net;
    CheckMatch match;
    gms::SessionOptions so;
    so.stopAfter = gms::kStepHello;
    so.check.mode = mode;
    so.check.kickCount = 2;
    gms::GmsSession s(net, match, so, gms::SteadyClock::now());
    s.OnConnected(5, "127.0.0.1|1");
    Deliver(s, 5, FromHex("810100000000000000a2c5000000000000"));
    for (int i = 0; i < 5; ++i) Deliver(s, 5, FromHex("a709000000"));
    CHECK_EQ(match.got.size(), size_t(5));  // passed on (mode 2: counted; 0: not looked at)
    CHECK(net.closed.empty());
    CHECK_EQ(s.CheckTotals().invalid, uint64_t(mode == 2 ? 5 : 0));
  }
}
