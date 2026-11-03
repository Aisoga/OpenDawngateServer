// SEC5: a new object's create held from the players whose team doesn't see it (core/replication/create_hold.h), with
// the create order (create_order.h) and the vision model's player sets (core/vision/fog.h PlayersForTeams). The
// players: 0 and 1 Mortal (team 1), 2 Spirit (team 2), 10 a spectator.
#include <vector>

#include "core/replication/create_hold.h"
#include "core/replication/create_order.h"
#include "core/vision/fog.h"
#include "tests/check.h"

namespace {

const std::vector<gms::vision::FogPlayer> kPlayers = {
    {0, 1}, {1, 1}, {2, 2}, gms::vision::ObserverFogPlayer(10)};
constexpr uint16_t kWorld = 0x0407;  // players 0, 1, 2 and the spectator 10
constexpr uint8_t kMortal = 0x2, kSpirit = 0x4, kNpc = 0x1;

uint16_t Sees(uint8_t teams) { return gms::vision::PlayersForTeams(teams, kPlayers); }

std::vector<std::size_t> Order(const std::vector<gms::CreateHoldItem>& items) {
  std::vector<gms::CreateOrderItem> o(items.size());
  for (std::size_t k = 0; k < items.size(); ++k)
    o[k] = gms::CreateOrderItem{items[k].id, items[k].owner, items[k].parent, items[k].target};
  return gms::CreateOrder(o);
}

gms::CreateHoldItem Item(uint32_t id, uint8_t teamsSeeing, uint16_t missing = kWorld) {
  gms::CreateHoldItem it;
  it.id = id;
  it.missing = missing;
  it.sees = Sees(teamsSeeing);
  return it;
}

}  // namespace

// A Spirit ward placed in the Mortal side's fog: created for its own team and the spectator, held from both Mortal
// players; with the rule off (or no vision answer) for everyone, as before.
GMS_TEST(CreateHoldWardInFog) {
  std::vector<gms::CreateHoldItem> items = {Item(0x7C010101, kSpirit)};
  const auto order = Order(items);
  auto plan = gms::PlanCreates(items, order, true);
  CHECK_EQ(plan[0].to, 0x0404);
  CHECK_EQ(plan[0].held, 0x0003);
  CHECK_EQ(plan[0].pulled, 0);
  plan = gms::PlanCreates(items, order, false);
  CHECK_EQ(plan[0].to, kWorld);
  CHECK_EQ(plan[0].held, 0);
  items[0].sees = gms::kCreateSeesUnknown;
  plan = gms::PlanCreates(items, order, true);
  CHECK_EQ(plan[0].to, kWorld);
}

// Exempt objects (a player's creature, a level object) and objects every team sees (a tower: the model's kAllTeams)
// go to everyone that lacks them.
GMS_TEST(CreateHoldExemptAndPublic) {
  std::vector<gms::CreateHoldItem> items = {Item(1, kSpirit), Item(2, gms::vision::kAllTeams), Item(3, kNpc)};
  items[0].exempt = true;  // the Spirit player's creature, deployed in its base
  const auto plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan[0].to, kWorld);
  CHECK_EQ(plan[1].to, kWorld);
  CHECK_EQ(plan[2].to, 0x0400);  // a neutral creep nobody's team sees: the spectator only
  CHECK_EQ(plan[2].held, 0x0003 | 0x0004);
}

// The create at first sight: held while unseen, created on the tick the Mortal team sees it (to the players still
// missing it only), and the ledger reports those players and how long they waited.
GMS_TEST(CreateHoldFirstSight) {
  gms::HeldCreates ledger;
  std::vector<gms::CreateHoldItem> items = {Item(0x7C010101, kSpirit)};
  auto plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(ledger.OnPlanned(items[0].id, plan[0].to, plan[0].held, 1000), 0);
  CHECK_EQ(ledger.HeldFrom(items[0].id), 0x0003);
  // the next ticks: Spirit and the spectator know it now, Mortal still doesn't see it
  items[0].missing = 0x0003;
  plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan[0].to, 0);
  CHECK_EQ(ledger.OnPlanned(items[0].id, plan[0].to, plan[0].held, 1500), 0);
  // a Mortal source reaches it
  items[0].sees = Sees(kSpirit | kMortal);
  plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan[0].to, 0x0003);
  uint64_t waited = 0;
  CHECK_EQ(ledger.OnPlanned(items[0].id, plan[0].to, plan[0].held, 4000, &waited), 0x0003);
  CHECK_EQ(waited, 3000u);
  CHECK_EQ(ledger.Size(), 0u);
}

// An object that dies unseen: the deletion names the players it was held from (they never got its create, so the 91
// goes to its known mask only), and the ledger forgets it.
GMS_TEST(CreateHoldUnseenDeath) {
  gms::HeldCreates ledger;
  std::vector<gms::CreateHoldItem> items = {Item(0x7C020202, kSpirit), Item(0x7C030303, kSpirit)};
  const auto plan = gms::PlanCreates(items, Order(items), true);
  for (std::size_t i = 0; i < items.size(); ++i) ledger.OnPlanned(items[i].id, plan[i].to, plan[i].held, 0);
  CHECK_EQ(ledger.OnDeleted(0x7C020202), 0x0003);
  CHECK_EQ(ledger.OnDeleted(0x7C020202), 0);
  CHECK_EQ(ledger.Size(), 1u);
  ledger.ForgetPlayer(0);
  CHECK_EQ(ledger.HeldFrom(0x7C030303), 0x0002);
  ledger.Retain([](uint32_t) { return false; });
  CHECK_EQ(ledger.Size(), 0u);
}

// A projectile the Mortal players see, whose target and owner they don't know (a Spirit minion in the fog shooting at a
// target object in the fog): both are pulled in for them, placed before the projectile by the create order; the
// spectator and the Spirit player get all three as anyway.
GMS_TEST(CreateHoldPullsInDependencies) {
  std::vector<gms::CreateHoldItem> items(3);
  items[0] = Item(0x10, kSpirit | kMortal);  // the projectile, in the lower pool slot
  items[0].owner = 0x20;
  items[0].target = 0x30;
  items[1] = Item(0x20, kSpirit);  // its owner, a Spirit minion in the Mortal side's fog
  items[2] = Item(0x30, kSpirit);  // its target object
  const auto order = Order(items);
  CHECK_EQ(order.size(), 3u);
  CHECK_EQ(order[2], 0u);  // the projectile after both
  const auto plan = gms::PlanCreates(items, order, true);
  CHECK_EQ(plan[0].to, kWorld);
  CHECK_EQ(plan[0].pulled, 0);
  CHECK_EQ(plan[1].to, kWorld);
  CHECK_EQ(plan[1].pulled, 0x0003);
  CHECK_EQ(plan[1].held, 0);
  CHECK_EQ(plan[2].to, kWorld);
  CHECK_EQ(plan[2].pulled, 0x0003);
}

// Chains: a hologram riding a projectile (parent) whose owner is a minion, only the hologram seen by Mortal: the
// projectile and, through it, the minion are pulled in. A dependency the player already knows (missing 0) is left
// alone, and a dependency outside the scan is ignored.
GMS_TEST(CreateHoldDependencyChain) {
  std::vector<gms::CreateHoldItem> items(4);
  items[0] = Item(0x40, kSpirit | kMortal);  // the hologram
  items[0].parent = 0x50;
  items[1] = Item(0x50, kSpirit);  // the projectile
  items[1].owner = 0x60;
  items[2] = Item(0x60, kSpirit);  // the minion
  items[3] = Item(0x70, kSpirit, 0x0002);  // known to everyone but player 1
  items[3].owner = 0x99;                   // not scanned
  const auto plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan[0].to, kWorld);
  CHECK_EQ(plan[1].pulled, 0x0003);
  CHECK_EQ(plan[2].pulled, 0x0003);
  CHECK_EQ(plan[3].to, 0);
  CHECK_EQ(plan[3].held, 0x0002);
  // the hologram already known to Mortal (missing only for nobody): nothing is pulled in
  items[0].missing = 0x0404;
  const auto plan2 = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan2[1].pulled, 0);
  CHECK_EQ(plan2[2].pulled, 0);
  CHECK_EQ(plan2[1].to, 0x0404);
}

// SEC5b: the parasite (NPC type 8) and the structures are never held: the game names them to every team (the parasite's
// kill A5 to both teams); minions (0, 1), creeps (3), buff creatures (6, 7) and workers (12) are.
GMS_TEST(CreateHoldAnnouncedNpcTypes) {
  CHECK(gms::AnnouncedNpcType(gms::kNpcTypeParasite));
  CHECK(gms::AnnouncedNpcType(9));
  CHECK(gms::AnnouncedNpcType(13));
  CHECK(gms::AnnouncedNpcType(14));
  CHECK(gms::AnnouncedNpcType(15));
  for (const int t : {-1, 0, 1, 2, 3, 6, 7, 12, 16}) CHECK(!gms::AnnouncedNpcType(t));
  // the parasite in its pit, seen by nobody: created for everyone as exempt
  std::vector<gms::CreateHoldItem> items = {Item(0x7C810122, kNpc)};
  items[0].exempt = gms::AnnouncedNpcType(8);
  const auto plan = gms::PlanCreates(items, Order(items), true);
  CHECK_EQ(plan[0].to, kWorld);
  CHECK_EQ(plan[0].held, 0);
}

// SEC5b: the objects a ClientFeedbackEvent (A5) names. The first body is a PlayerCommCharge voice line the host sent
// (members 0, 1, 3, 5); the second a parasite kill with the parasite and its killer as the target (members 0, 1, 2, 5).
GMS_TEST(CreateHoldFeedbackEventObjects) {
  gms::Bytes charge, kill;
  CHECK(gms::ParseHex("2bd17fddb52e0177a601000000d17fddb5", charge));
  uint32_t object = 1, target = 1;
  CHECK(gms::FeedbackEventObjects(charge.data(), charge.size(), &object, &target));
  CHECK_EQ(object, 0xA677012Eu);
  CHECK_EQ(target, 0u);
  CHECK(gms::ParseHex("2711223344220181" "7c28017c7c11223344", kill));
  CHECK(gms::FeedbackEventObjects(kill.data(), kill.size(), &object, &target));
  CHECK_EQ(object, 0x7C810122u);
  CHECK_EQ(target, 0x7C7C0128u);
  // cut short before the target: false; no members: no objects
  CHECK(!gms::FeedbackEventObjects(kill.data(), 9, &object, &target));
  const uint8_t none[] = {0x00};
  CHECK(gms::FeedbackEventObjects(none, 1, &object, &target));
  CHECK_EQ(object, 0u);
  CHECK(!gms::FeedbackEventObjects(nullptr, 0, &object, &target));
}
