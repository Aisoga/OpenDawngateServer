// gms/core/replication (D2g-live): the modifier registry and the outgoing vector's routing. The golden bytes are what
// the match host forwarded in d7_run5 (re/logs/d7_run5/host/dgsim.log): the level's ModifierCreated of the first shop
// and a Binding's ModifierUpdated (its attack's modifier, start kept). The second shop record and the Binding's record
// are made up from them (other instance; a duration, start, holds and feedback flags).
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/protocol/wire.h"
#include "core/replication/create_order.h"
#include "core/replication/fow_data.h"
#include "core/replication/modifiers.h"
#include "core/replication/outgoing.h"
#include "tests/check.h"

using gms::Bytes;

namespace {

Bytes FromHex(const char* hex) {
  Bytes b;
  gms::ParseHex(hex, b);
  return b;
}

// d7_run5: "-> ab ModifierCreated (42 B)" for object A5010000, and a second record of the same carrier
const char* const kCreatedShop = "000001a5000001a59b161bf4000001ae00000000010000000100000000000000000000000000000000";
const char* const kCreatedShop2 = "000001a5000001a59b161bf4020001ae00000000010000000100000000000000000000000000000000";
const char* const kCreatedBinding = "4d014ea64d014ea616c98eb51a001dae0010000001000000010000004096000000000000010a000000";
// d7_run5: "-> ac ModifierUpdated (26 B)" for A64E014D
const char* const kUpdatedBinding = "4d014ea61a001daeffffffffffffffff000000000100000000";

}  // namespace

GMS_TEST(ModifierRecordLayout) {
  const Bytes b = FromHex(kCreatedShop);
  CHECK_EQ(b.size(), gms::kModifierRecordSize);
  gms::ModifierRecord r;
  CHECK(gms::ParseModifierRecord(b.data(), b.size(), &r));
  CHECK_EQ(r.carrier, 0xA5010000u);
  CHECK_EQ(r.caster, 0xA5010000u);
  CHECK_EQ(r.ability, 0xF41B169Bu);
  CHECK_EQ(r.instance, 0xAE010000u);
  CHECK_EQ(r.durationMs, 0u);
  CHECK_EQ(r.rank, 1u);
  CHECK_EQ(r.stacks, 1u);
  CHECK_EQ(r.startMs, 0ull);
  CHECK_EQ(static_cast<int>(r.holdsAgent), 0);
  CHECK_EQ(gms::Hex(gms::BuildModifierRecord(r), 100), std::string(kCreatedShop));
  gms::ModifierUpdate u;
  const Bytes ub = FromHex(kUpdatedBinding);
  CHECK_EQ(ub.size(), gms::kModifierUpdateSize);
  CHECK(gms::ParseModifierUpdate(ub.data(), ub.size(), &u));
  CHECK_EQ(u.carrier, 0xA64E014Du);
  CHECK_EQ(u.instance, 0xAE1D001Au);
  CHECK_EQ(u.startMs, ~0ull);
  CHECK_EQ(u.stacks, 1u);
  CHECK_HEX(gms::BuildModifierDeleted(0xA64E014D, 0xAE1D001A), "ad4d014ea61a001dae");
}

GMS_TEST(ModifierRegistryCreateUpdateDelete) {
  gms::ModifierRegistry reg;
  const Bytes shop = FromHex(kCreatedShop), shop2 = FromHex(kCreatedShop2), binding = FromHex(kCreatedBinding);
  CHECK(reg.OnCreated(shop.data(), shop.size()));
  CHECK(reg.OnCreated(binding.data(), binding.size()));
  CHECK(reg.OnCreated(shop2.data(), shop2.size()));
  CHECK(!reg.OnCreated(shop.data(), 40));  // not one record
  CHECK_EQ(reg.Size(), static_cast<size_t>(3));
  CHECK_EQ(reg.CountFor(0xA5010000), 2);
  CHECK_EQ(reg.CarrierOf(0xAE1D001A), 0xA64E014Du);
  // 8F's tail: the carrier's records in creation order, nothing of other carriers
  CHECK_EQ(gms::Hex(reg.RecordsFor(0xA5010000), 200), std::string(kCreatedShop) + kCreatedShop2);
  CHECK(reg.RecordsFor(0x12345678).empty());
  std::vector<uint32_t> order = reg.Instances();
  CHECK_EQ(order.size(), static_cast<size_t>(3));
  CHECK_EQ(order[0], 0xAE010000u);
  CHECK_EQ(order[1], 0xAE1D001Au);
  CHECK_EQ(order[2], 0xAE010002u);

  // AC: duration, stacks and holds change, the start is kept (-1)
  Bytes upd = FromHex(kUpdatedBinding);
  upd[16] = 0x88;  // duration 0x1388 = 5000 ms
  upd[17] = 0x13;
  upd[20] = 3;  // stacks
  upd[24] = 1;  // holds the agent
  CHECK(reg.OnUpdated(upd.data(), upd.size()));
  gms::ModifierRecord r;
  const Bytes rec = reg.RecordsFor(0xA64E014D);
  CHECK(gms::ParseModifierRecord(rec.data(), rec.size(), &r));
  CHECK_EQ(r.durationMs, 5000u);
  CHECK_EQ(r.stacks, 3u);
  CHECK_EQ(static_cast<int>(r.holdsAgent), 1);
  CHECK_EQ(r.startMs, 0x9640ull);  // kept
  CHECK_EQ(r.feedback, 10u);       // untouched
  // a new start
  for (int k = 0; k < 8; ++k) upd[8 + k] = k == 0 ? 0x10 : 0;
  CHECK(reg.OnUpdated(upd.data(), upd.size()));
  CHECK(gms::ParseModifierRecord(reg.RecordsFor(0xA64E014D).data(), gms::kModifierRecordSize, &r));
  CHECK_EQ(r.startMs, 0x10ull);
  // an update for an unknown instance changes nothing
  upd[4] = 0x99;
  CHECK(!reg.OnUpdated(upd.data(), upd.size()));

  // the instance ends: AD once, then it is gone
  Bytes ad;
  CHECK(reg.Remove(0xAE1D001A, &ad));
  CHECK_HEX(ad, "ad4d014ea61a001dae");
  CHECK(!reg.Remove(0xAE1D001A, &ad));
  CHECK(!reg.Has(0xAE1D001A));
  // the carrier is deleted: its records go without AD
  CHECK_EQ(reg.RemoveCarrier(0xA5010000), 2);
  CHECK_EQ(reg.Size(), static_cast<size_t>(0));
}

GMS_TEST(OutgoingRouting) {
  std::vector<gms::OutgoingRoute> e(6);
  // a ModifierCreated about a live object known to players 0 and 2
  e[0].kind = 2;
  e[0].object = 0xA6710170;
  e[0].objectAlive = true;
  e[0].seers = 0x5;
  // a SetAnimationState about a minion deleted in the same tick (D2f dropped it)
  e[1].kind = 2;
  e[1].object = 0xA6720171;
  e[1].objectAlive = false;
  // a combat event to everyone
  e[2].kind = 1;
  e[2].recipients = 0x7FFF;
  // a positional event: no fog of war yet
  e[3].kind = 3;
  // the deletion, queued after the messages about the dying object
  e[4].kind = 0;
  e[4].object = 0xA6720171;
  e[4].recipients = 0x1;
  // an object deleted earlier (nobody to tell)
  e[5].kind = 2;
  e[5].object = 0xA6730172;
  gms::RouteOutgoing(&e, 0x3FF);
  CHECK_EQ(e[0].players, 0x5);
  CHECK_EQ(e[1].players, 0x1);
  CHECK(e[1].fromDeletion);
  CHECK_EQ(e[2].players, 0x7FFF);
  CHECK_EQ(e[3].players, 0x3FF);
  CHECK_EQ(e[4].players, 0x1);
  CHECK_EQ(e[5].players, 0);
  CHECK(!e[5].fromDeletion);

  gms::WireCounts c;
  CHECK(c.Empty());
  c.Add(gms::kGmsLocomotionDataUpdate, 10);
  c.Add(gms::kGmsCombatantDataUpdate, 10);
  c.Add(gms::kGmsLocomotionDataUpdate, 10);
  CHECK_EQ(c.Messages(), 3);
  CHECK_EQ(c.Format(), std::string("9c x2 20 B, a0 x1 10 B"));
  c.Clear();
  CHECK(c.Empty());
}

// D9b: a joining player's world creates every object after its owner (the client maps the full state's mOwnerID through
// its id map, an unknown id becomes 0), otherwise in the object manager's order.
GMS_TEST(CreateOrderOwnersFirst) {
  using gms::CreateOrderItem;
  // a worker in a slot below its well's, a pet after its shaper, objects without owners, an owner outside the world
  const std::vector<CreateOrderItem> items = {
      {0x10, 0},     // 0 a tower
      {0x20, 0x50},  // 1 a worker of well 0x50
      {0x30, 0},     // 2 a minion
      {0x40, 0x60},  // 3 a pet of shaper 0x60
      {0x50, 0},     // 4 the well
      {0x60, 0},     // 5 the shaper
      {0x70, 0x99},  // 6 owned by an object the world doesn't have
      {0x80, 0x50},  // 7 another worker of the well
  };
  const std::vector<size_t> order = gms::CreateOrder(items);
  const std::vector<size_t> expect = {0, 4, 1, 2, 5, 3, 6, 7};
  CHECK_EQ(order.size(), expect.size());
  for (size_t k = 0; k < order.size() && k < expect.size(); ++k) CHECK_EQ(order[k], expect[k]);
  // nothing owned: the input order
  const std::vector<CreateOrderItem> plain = {{1, 0}, {2, 0}, {3, 0}};
  const std::vector<size_t> p = gms::CreateOrder(plain);
  CHECK_EQ(p.size(), static_cast<size_t>(3));
  for (size_t k = 0; k < p.size(); ++k) CHECK_EQ(p[k], k);
  // a chain (c owned by b owned by a, listed c, b, a), a self-owner and a cycle: each placed once, owners first
  const std::vector<CreateOrderItem> chain = {{0xC, 0xB}, {0xB, 0xA}, {0xA, 0}, {0xD, 0xD}, {0xE, 0xF}, {0xF, 0xE}};
  const std::vector<size_t> c = gms::CreateOrder(chain);
  const std::vector<size_t> ce = {2, 1, 0, 3, 5, 4};
  CHECK_EQ(c.size(), ce.size());
  for (size_t k = 0; k < c.size() && k < ce.size(); ++k) CHECK_EQ(c[k], ce[k]);
  CHECK(gms::CreateOrder({}).empty());
}

// D2h: a player's labsPlayerFowData (AA) goes to its team and the spectators always, and to the players that see its
// creature (gms/core/replication/fow_data.h); without byVision to the team only (D2g-live's rule).
GMS_TEST(FowDataRecipientsTeamAndSeers) {
  const uint16_t team = 0x0400 | 0x0020 | 0x0002;  // players 1 and 5 (the player's team), spectator 10
  const uint16_t seers = 0x0001 | 0x0002 | 0x0400;  // the enemy human 0 sees the creature (and its own team does)
  const uint16_t world = 0x0001 | 0x0002 | 0x0400;  // player 5 has no world yet
  CHECK_EQ(gms::FowDataRecipients(team, seers, world, true), static_cast<uint16_t>(0x0403));
  CHECK_EQ(gms::FowDataRecipients(team, seers, world, false), static_cast<uint16_t>(0x0402));
  CHECK_EQ(gms::FowDataRecipients(team, 0, world, true), static_cast<uint16_t>(0x0402));  // no creature: the team
  CHECK_EQ(gms::FowDataRecipients(team, seers, 0, true), static_cast<uint16_t>(0));
}

// The router: the whole struct to each new recipient, the delta to the ones in step, a drop when one stops seeing it.
GMS_TEST(FowDataRouterNewcomersGetTheWholeStruct) {
  gms::FowDataRouter r;
  const int bot = 3;  // an enemy bot of the human 0; its team: human 6 and spectator 10
  const uint16_t team = 0x0440;
  gms::FowDataSends s = r.Route(bot, team);  // the first tick: everyone new
  CHECK_EQ(s.full, team);
  CHECK_EQ(s.delta, static_cast<uint16_t>(0));
  s = r.Route(bot, team);
  CHECK_EQ(s.delta, team);
  CHECK_EQ(s.full, static_cast<uint16_t>(0));
  s = r.Route(bot, team | 0x0001);  // the human 0 sees the bot: the whole struct to it, the delta to the rest
  CHECK_EQ(s.full, static_cast<uint16_t>(0x0001));
  CHECK_EQ(s.delta, team);
  CHECK_EQ(s.dropped, static_cast<uint16_t>(0));
  s = r.Route(bot, team | 0x0001);
  CHECK_EQ(s.delta, static_cast<uint16_t>(team | 0x0001));
  s = r.Route(bot, team);  // into fog for the human 0
  CHECK_EQ(s.dropped, static_cast<uint16_t>(0x0001));
  CHECK_EQ(s.delta, team);
  CHECK_EQ(r.Synced(bot), team);
  s = r.Route(bot, team | 0x0001);  // out of fog again: the whole struct again
  CHECK_EQ(s.full, static_cast<uint16_t>(0x0001));
  // the human 6 leaves (no world) and rejoins: dropped, then the whole struct
  s = r.Route(bot, 0x0401);
  CHECK_EQ(s.dropped, static_cast<uint16_t>(0x0040));
  s = r.Route(bot, 0x0441);
  CHECK_EQ(s.full, static_cast<uint16_t>(0x0040));
  CHECK_EQ(s.delta, static_cast<uint16_t>(0x0401));
  // a whole struct that couldn't be written: still new in the next tick
  r.Unsync(bot, 0x0040);
  s = r.Route(bot, 0x0441);
  CHECK_EQ(s.full, static_cast<uint16_t>(0x0040));
  // other players are independent; out-of-range indexes route nothing
  CHECK_EQ(r.Route(4, 0x0001).full, static_cast<uint16_t>(0x0001));
  CHECK_EQ(r.Route(16, 0xFFFF).full, static_cast<uint16_t>(0));
  CHECK_EQ(r.Route(-1, 0xFFFF).delta, static_cast<uint16_t>(0));
}

// The router against the field-state semantics the engine has (one field state per player: a delta carries the members
// that changed since anyone was last sent them, the whole struct carries every member): every client that gets the
// player's AA this tick ends the tick with the server's values, and a client that doesn't see the creature keeps the
// level it last saw, until the creature shows again (the enemy bot leveled in fog shows its new level at once).
GMS_TEST(FowDataRouterKeepsEveryRecipientInStep) {
  struct Data {
    uint32_t xp = 0, level = 1, gold = 500;
  };
  Data server, fieldState;  // fieldState = what the last delta (or whole struct) carried
  Data client[16];          // each client's copy of the bot's cLabsPlayer
  gms::FowDataRouter r;
  const int bot = 2;
  const uint16_t team = 0x0008;   // the bot's teammate, human 3
  const uint16_t world = 0x0009;  // humans 0 (the enemy) and 3
  // per tick: does the human 0 see the bot's creature, and what the server's bot gains
  struct Tick {
    bool seen;
    uint32_t xp, level, gold;
  };
  const Tick ticks[] = {
      {false, 0, 1, 500},     {true, 0, 1, 500},      {true, 120, 1, 520},  // seen, gains XP in view
      {false, 300, 2, 560},   {false, 700, 3, 640},                         // levels twice in fog
      {true, 700, 3, 640},                                                  // back in view: level 3 at once
      {true, 1100, 4, 700},   {false, 1100, 4, 700},  {true, 1600, 5, 800},  // a level in view, one in fog
  };
  int t = 0;
  for (const Tick& k : ticks) {
    server.xp = k.xp;
    server.level = k.level;
    server.gold = k.gold;
    const uint16_t seers = static_cast<uint16_t>(team | (k.seen ? 0x0001 : 0));
    const uint16_t to = gms::FowDataRecipients(team, seers, world, true);
    const gms::FowDataSends s = r.Route(bot, to);
    // the delta: the members that differ from the field state, then the field state advances
    const bool dxp = server.xp != fieldState.xp, dlevel = server.level != fieldState.level,
               dgold = server.gold != fieldState.gold;
    fieldState = server;
    for (int p = 0; p < 16; ++p) {
      if (!((s.delta >> p) & 1)) continue;
      if (dxp) client[p].xp = server.xp;
      if (dlevel) client[p].level = server.level;
      if (dgold) client[p].gold = server.gold;
    }
    for (int p = 0; p < 16; ++p)
      if ((s.full >> p) & 1) client[p] = server;  // the whole struct, after the delta
    for (int p = 0; p < 16; ++p) {
      if (!((to >> p) & 1)) continue;
      CHECK_EQ(client[p].level, server.level);
      CHECK_EQ(client[p].xp, server.xp);
      CHECK_EQ(client[p].gold, server.gold);
    }
    if (t == 4) CHECK_EQ(client[0].level, 1u);  // in fog since tick 3: the level it last saw
    if (t == 5) CHECK_EQ(client[0].level, 3u);  // the new level at once
    if (t == 7) CHECK_EQ(client[0].level, 4u);
    ++t;
  }
  CHECK_EQ(client[0].level, 5u);
  CHECK_EQ(client[3].level, 5u);  // the teammate got every change
}
