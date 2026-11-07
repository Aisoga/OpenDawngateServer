// SEC4B: what one team may not learn through the host's shared sends (core/replication/team_private.h, the routing in
// core/replication/outgoing.h): mVisibleToTeam per audience, the deltas' rule, a kind-1 A4 by its object, a modifier
// record's caster, the other team's copy of AA.
#include <map>
#include <vector>

#include "core/replication/modifiers.h"
#include "core/replication/outgoing.h"
#include "core/replication/team_private.h"
#include "tests/check.h"

namespace {

constexpr uint8_t kNpc = 0x1, kMortal = 0x2, kSpirit = 0x4;

// A reflected body as the client's writer lays it out: `masks` mask bytes (the count in the first byte's high nibble),
// the low member bits in the last, then the values.
gms::Bytes Body(size_t masks, uint8_t lowBits, const gms::Bytes& values) {
  gms::Bytes b(masks, 0);
  b[masks - 1] = lowBits;
  b[0] = static_cast<uint8_t>(b[0] | (masks << 4));
  b.insert(b.end(), values.begin(), values.end());
  return b;
}

gms::Bytes Record(uint32_t carrier, uint32_t caster, uint32_t instance) {
  gms::ModifierRecord r;
  r.carrier = carrier;
  r.caster = caster;
  r.ability = 0xABCD0001;
  r.instance = instance;
  return gms::BuildModifierRecord(r);
}

uint32_t CasterOf(const gms::Bytes& msg, size_t recordAt) { return gms::GetU32LE(msg.data() + recordAt + 4); }

}  // namespace

// A recipient's audience: a spectator's whatever its team says, a player's its team, anything else no team.
GMS_TEST(Sec4bVisibleAudienceOfARecipient) {
  CHECK_EQ(gms::VisibleAudienceOf(true, 1), gms::kVisibleAudienceSpectators);
  CHECK_EQ(gms::VisibleAudienceOf(false, 1), gms::kVisibleAudienceTeam1);
  CHECK_EQ(gms::VisibleAudienceOf(false, 2), gms::kVisibleAudienceTeam2);
  CHECK_EQ(gms::VisibleAudienceOf(false, 0), gms::kVisibleAudienceNoTeam);
  CHECK_EQ(gms::VisibleAudienceOf(false, 0xFF), gms::kVisibleAudienceNoTeam);
}

// Every byte: a team never gets another bit than its own, the spectators the truth.
GMS_TEST(Sec4bVisibleEveryByteMasked) {
  for (int v = 0; v < 256; ++v) {
    const uint8_t bits = static_cast<uint8_t>(v);
    const uint8_t one = gms::VisibleAudienceBits(gms::kVisibleAudienceTeam1, bits);
    const uint8_t two = gms::VisibleAudienceBits(gms::kVisibleAudienceTeam2, bits);
    CHECK_EQ(static_cast<int>(one), bits & kMortal);
    CHECK_EQ(static_cast<int>(two), bits & kSpirit);
    CHECK_EQ(static_cast<int>(gms::VisibleAudienceBits(gms::kVisibleAudienceNoTeam, bits)), bits & kNpc);
    CHECK_EQ(static_cast<int>(gms::VisibleAudienceBits(gms::kVisibleAudienceSpectators, bits)), static_cast<int>(bits));
  }
}

// The client's readers keep working: a player's mask (1 << team) finds its bit, and the spectators' default mask 6 the
// same answer as on the truth. What the masked byte can't answer any more is exactly the other team's sight.
GMS_TEST(Sec4bVisibleTheClientsReadersGetTheSameAnswer) {
  for (int v = 0; v < 8; ++v) {
    const uint8_t bits = static_cast<uint8_t>(v);
    for (int team = 1; team <= 2; ++team) {
      const uint8_t mask = static_cast<uint8_t>(1u << team);
      const uint8_t got = gms::VisibleAudienceBits(team, bits);
      CHECK_EQ((got & mask) != 0, (bits & mask) != 0);  // GGMinimap_UpdateObjectIcon, _PlayAttachServerEvent
      // _PlayAttachServerEvent's `== 6` path implies the local bit, so it decides nothing for a player
      CHECK(bits != 6 || (got & mask) != 0);
    }
  }
}

// The delta rule: no message to a team when only the other team's bit changes, one when its own does; the spectators
// see every change of either; the first step only seeds.
GMS_TEST(Sec4bVisibleNoDeltaWhenOnlyTheOtherTeamChanged) {
  const unsigned t1 = 1u << gms::kVisibleAudienceTeam1, t2 = 1u << gms::kVisibleAudienceTeam2,
                 spec = 1u << gms::kVisibleAudienceSpectators;
  gms::VisibleShadow s;
  CHECK_EQ(s.Step(kMortal), 0u);                      // created: its create carried this
  CHECK_EQ(s.Step(kMortal), 0u);                      // nothing changed
  CHECK_EQ(s.Step(kMortal | kSpirit), t2 | spec);     // an enemy ward starts seeing it: not to team 1
  CHECK_EQ(s.Step(kMortal), t2 | spec);               // and stops: not to team 1
  CHECK_EQ(s.Step(kSpirit), t1 | t2 | spec);          // each team's bit flips
  CHECK_EQ(s.Step(kSpirit | kNpc), (1u << gms::kVisibleAudienceNoTeam) | spec);  // the NPC bit: no team's
}

// The member-0 update's bytes: 90, u32 id, the mask for one mask byte and member 0, the value.
GMS_TEST(Sec4bVisibleUpdateGoldenBytes) {
  const gms::Bytes m = gms::BuildVisibleUpdate(0x7C010203, 0x02);
  const gms::Bytes want = {0x90, 0x03, 0x02, 0x01, 0x7C, 0x11, 0x02};
  CHECK(m == want);
}

// Where member 0's value is: right after the mask bytes, whatever their number; -1 without member 0 or a bad count.
GMS_TEST(Sec4bReflectedMember0At) {
  const gms::Bytes three = Body(3, 0x01, {0x06, 0x11});
  CHECK_EQ(gms::ReflectedMember0At(three.data(), three.size()), 3);
  const gms::Bytes one = Body(1, 0x01, {0x04});
  CHECK_EQ(gms::ReflectedMember0At(one.data(), one.size()), 1);
  const gms::Bytes none = Body(3, 0x02, {0, 0, 0, 0});
  CHECK_EQ(gms::ReflectedMember0At(none.data(), none.size()), -1);
  const gms::Bytes noCount = {0x01, 0x06};
  CHECK_EQ(gms::ReflectedMember0At(noCount.data(), noCount.size()), -1);
  const gms::Bytes cut = {0x31};  // 3 mask bytes announced, 1 there
  CHECK_EQ(gms::ReflectedMember0At(cut.data(), cut.size()), -1);
  const gms::Bytes noValue = {0x11};
  CHECK_EQ(gms::ReflectedMember0At(noValue.data(), noValue.size()), -1);
}

// A ServerEvent's head: type and objectId, with the mask bytes the writer chose.
GMS_TEST(Sec4bServerEventHead) {
  gms::Bytes v = {gms::kServerEventDetach};
  gms::PutU32LE(v, 0x7C440101);
  gms::PutU32LE(v, 0x00000099);  // objectFxHandle
  const gms::Bytes body = Body(2, 0x07, v);
  std::uint8_t type = 0;
  uint32_t id = 0;
  CHECK(gms::ParseServerEventHead(body.data(), body.size(), &type, &id));
  CHECK_EQ(static_cast<int>(type), static_cast<int>(gms::kServerEventDetach));
  CHECK_EQ(id, 0x7C440101u);
  // no objectId, no type, too short: not ours to route
  const gms::Bytes noObject = Body(1, 0x01, {gms::kServerEventAttach});
  CHECK(!gms::ParseServerEventHead(noObject.data(), noObject.size(), &type, &id));
  const gms::Bytes noType = Body(1, 0x02, {1, 2, 3, 4});
  CHECK(!gms::ParseServerEventHead(noType.data(), noType.size(), &type, &id));
  const gms::Bytes cut = Body(1, 0x03, {1, 2, 3});
  CHECK(!gms::ParseServerEventHead(cut.data(), cut.size(), &type, &id));
}

// A stop goes to whoever knows the object, seeing it or not; to nobody for an object that is gone without a deletion in
// this drain; by the deletion's known mask when the deletion is in the same drain. Other kind-1 entries keep their mask.
GMS_TEST(Sec4bStopByKnownObject) {
  std::vector<gms::OutgoingRoute> e(5);
  e[0].kind = 1;  // alive: known 0x0403 (players 0, 1 and the spectator 10), fogged or not
  e[0].wire = gms::kGmsServerEvent;
  e[0].recipients = 0x7FFF;
  e[0].object = 0x11;
  e[0].objectAlive = true;
  e[0].toKnown = true;
  e[0].known = 0x0403;
  e[1].kind = 1;  // gone, no deletion here
  e[1].wire = gms::kGmsServerEvent;
  e[1].recipients = 0x7FFF;
  e[1].object = 0x22;
  e[1].toKnown = true;
  e[2].kind = 1;  // gone, its deletion follows in this drain
  e[2].wire = gms::kGmsServerEvent;
  e[2].recipients = 0x7FFF;
  e[2].object = 0x33;
  e[2].toKnown = true;
  e[3].kind = 0;
  e[3].object = 0x33;
  e[3].recipients = 0x0005;
  e[4].kind = 1;  // the kill announcement: its own mask
  e[4].wire = 0xBA;
  e[4].recipients = 0x7FFF;
  gms::RouteOutgoing(&e, 0x7FFF, true);
  CHECK_EQ(static_cast<int>(e[0].players), 0x0403);
  CHECK_EQ(static_cast<int>(e[1].players), 0);
  CHECK_EQ(static_cast<int>(e[2].players), 0x0005);
  CHECK(e[2].fromDeletion);
  CHECK_EQ(static_cast<int>(e[4].players), 0x7FFF);
}

// A draw box's events, routed as a message about their object: those that see it now, by the deletion's masks when gone.
GMS_TEST(Sec4bDrawBoxBySight) {
  std::vector<gms::OutgoingRoute> e(1);
  e[0].kind = 2;
  e[0].wire = gms::kGmsServerEvent;
  e[0].object = 0x44;
  e[0].objectAlive = true;
  e[0].seers = 0x0403;
  e[0].seeNow = 0x0401;  // player 1's team just lost sight
  gms::RouteOutgoing(&e, 0x7FFF, true);
  CHECK_EQ(static_cast<int>(e[0].players), 0x0401);
}

// A record's caster: unchanged views (none) when everyone knows it; 0 only for the recipients that don't.
GMS_TEST(Sec4bCasterZeroForUnknown) {
  const uint32_t carrier = 0x7C000001, caster = 0x7C000002, other = 0x7C000003;
  std::map<uint32_t, uint16_t> known;
  known[carrier] = 0x0007;
  known[caster] = 0x0003;
  known[other] = 0x0005;
  const auto knownOf = [&](uint32_t id) {
    const auto it = known.find(id);
    return it != known.end() ? it->second : static_cast<uint16_t>(0);
  };
  // AB: one record, player 2 doesn't know the caster
  gms::Bytes ab = {gms::kGmsModifierCreated};
  const gms::Bytes rec = Record(carrier, caster, 0x90000001);
  ab.insert(ab.end(), rec.begin(), rec.end());
  std::vector<gms::CasterView> v = gms::SplitModifierCasters(ab, 1, 0x0007, knownOf);
  CHECK_EQ(v.size(), static_cast<size_t>(2));
  CHECK_EQ(static_cast<int>(v[0].players), 0x0003);
  CHECK_EQ(CasterOf(v[0].msg, 1), caster);
  CHECK_EQ(static_cast<int>(v[1].players), 0x0004);
  CHECK_EQ(CasterOf(v[1].msg, 1), 0u);
  // the rest of the record is the same
  gms::Bytes same = v[1].msg;
  for (int k = 0; k < 4; ++k) same[1 + 4 + k] = ab[1 + 4 + k];
  CHECK(same == ab);
  // everyone that gets it knows the caster: no copy
  CHECK(gms::SplitModifierCasters(ab, 1, 0x0003, knownOf).empty());

  // 8F with two records and three recipients: each its own view
  gms::Bytes complete = {gms::kGmsObjectCreateComplete, 1, 0, 0, 0x7C};
  const gms::Bytes rec2 = Record(carrier, other, 0x90000002);
  complete.insert(complete.end(), rec.begin(), rec.end());
  complete.insert(complete.end(), rec2.begin(), rec2.end());
  v = gms::SplitModifierCasters(complete, 5, 0x0007, knownOf);
  std::map<int, std::pair<uint32_t, uint32_t>> got;
  uint16_t players = 0;
  for (const gms::CasterView& x : v) {
    CHECK_EQ(static_cast<int>(x.players & players), 0);  // each recipient in one view
    players = static_cast<uint16_t>(players | x.players);
    got[x.players] = {CasterOf(x.msg, 5), CasterOf(x.msg, 5 + 41)};
  }
  CHECK_EQ(static_cast<int>(players), 0x0007);
  CHECK(got[0x0001] == std::make_pair(caster, other));  // player 0 knows both
  CHECK(got[0x0002] == std::make_pair(caster, 0u));     // player 1 the first
  CHECK(got[0x0004] == std::make_pair(0u, other));      // player 2 the second

  // a caster that is the carrier itself, and no caster at all: left alone
  gms::Bytes self = {gms::kGmsModifierCreated};
  const gms::Bytes rec3 = Record(carrier, carrier, 0x90000003);
  self.insert(self.end(), rec3.begin(), rec3.end());
  CHECK(gms::SplitModifierCasters(self, 1, 0x7FFF, knownOf).empty());
  gms::Bytes none = {gms::kGmsModifierCreated};
  const gms::Bytes rec4 = Record(carrier, 0, 0x90000004);
  none.insert(none.end(), rec4.begin(), rec4.end());
  CHECK(gms::SplitModifierCasters(none, 1, 0x7FFF, knownOf).empty());
  // a message without records: nothing to do
  const gms::Bytes bare = {gms::kGmsObjectCreateComplete, 1, 0, 0, 0x7C};
  CHECK(gms::SplitModifierCasters(bare, 5, 0x7FFF, knownOf).empty());
}

// The other team's AA: mAvailableGold out, the bit cleared, the other members in place; nothing left, nothing to send.
GMS_TEST(Sec4bFowDataWithoutAvailableGold) {
  // XP, gold, total gold (members 0, 1, 2)
  gms::Bytes values;
  gms::PutF32LE(values, 12.f);
  gms::PutF32LE(values, 345.f);
  gms::PutF32LE(values, 678.f);
  gms::Bytes m = {0xAA, 3};
  const gms::Bytes body = Body(1, 0x07, values);
  m.insert(m.end(), body.begin(), body.end());
  CHECK(gms::StripFowAvailableGold(&m));
  gms::Bytes want = {0xAA, 3, 0x15};
  gms::PutF32LE(want, 12.f);
  gms::PutF32LE(want, 678.f);
  CHECK(m == want);
  // gold alone: nothing left
  gms::Bytes solo = {0xAA, 3};
  gms::Bytes v1;
  gms::PutF32LE(v1, 345.f);
  const gms::Bytes soloBody = Body(1, 0x02, v1);
  solo.insert(solo.end(), soloBody.begin(), soloBody.end());
  CHECK(!gms::StripFowAvailableGold(&solo));
  // gold among members of a higher group (2 mask bytes), no XP: the high group stays
  gms::Bytes wide = {0xAA, 3};
  gms::Bytes wideBody = {0x21, 0x02};  // two mask bytes: member 8 (bit 0 of the first), member 1 (bit 1 of the last)
  gms::PutF32LE(wideBody, 345.f);
  gms::PutF32LE(wideBody, 9.f);
  wide.insert(wide.end(), wideBody.begin(), wideBody.end());
  CHECK(gms::StripFowAvailableGold(&wide));
  gms::Bytes wantWide = {0xAA, 3, 0x21, 0x00};
  gms::PutF32LE(wantWide, 9.f);
  CHECK(wide == wantWide);
  // no gold in it: as it is
  gms::Bytes noGold = {0xAA, 3};
  gms::Bytes v2;
  gms::PutF32LE(v2, 12.f);
  const gms::Bytes noGoldBody = Body(1, 0x01, v2);
  noGold.insert(noGold.end(), noGoldBody.begin(), noGoldBody.end());
  const gms::Bytes before = noGold;
  CHECK(gms::StripFowAvailableGold(&noGold));
  CHECK(noGold == before);
}
