// gms/core/damage/itempassives.h: a player's item passives across its death and its respawn.
#include <cstdint>
#include <vector>

#include "core/damage/itempassives.h"
#include "tests/check.h"

using namespace gms::damage::items;

namespace {

constexpr unsigned kDeath = gms::damage::kDeactOnAgentDeath;
constexpr unsigned kDestroyed = gms::damage::kDeactOnAgentDestroyed;
constexpr unsigned kDuration = gms::damage::kDeactOnDuration;

// the passives' GUIDs (made up) with their scripts' types
constexpr std::uint32_t kPurity = 0x11111111;      // modifier_Item_Advanced_HealNearbyAlly: Stacks, Destroyed|Death
constexpr std::uint32_t kRegen = 0x22222222;       // ..._OnTakeDamage_HealthRegen (Conviction, Faith): Stacks, Death
constexpr std::uint32_t kFaithShield = 0x33333333; // ..._StackingMRShield (Faith r3): Unique, Destroyed
constexpr std::uint32_t kSlow = 0x44444444;        // ..._OnDamage_Slow (Control r1, Inevitability r2): Unique, Duration|Death
constexpr std::uint32_t kPestilence = 0x55555555;  // ..._NearbyAllyBuff: Unique, Destroyed|Death
constexpr std::uint32_t kMight = 0x66666666;       // ..._OnLastHit_PermanentPowerHealthCapped: Unique, Destroyed
constexpr std::uint32_t kOdd = 0x77777777;         // a made-up UniqueResets passive

PassiveDef P(std::uint32_t guid, int rank, int activation, unsigned deactivation) {
  PassiveDef p;
  p.guid = guid;
  p.rank = rank;
  p.activation = activation;
  p.deactivation = deactivation;
  return p;
}
const PassiveDef kPurityDef = P(kPurity, 1, kActStacks, kDestroyed | kDeath);
const PassiveDef kRegenDef = P(kRegen, 1, kActStacks, kDeath);
const PassiveDef kShieldDef = P(kFaithShield, 3, kActUnique, kDestroyed);
const PassiveDef kPestDef = P(kPestilence, 1, kActUnique, kDestroyed | kDeath);
const PassiveDef kMightDef = P(kMight, 1, kActUnique, kDestroyed);

HeldId H(std::uint32_t id, const PassiveDef& p, bool live) {
  HeldId h;
  h.id = id;
  h.guid = p.guid;
  h.deactivation = p.deactivation;
  h.live = live;
  return h;
}

SlotView S(std::vector<PassiveDef> passives, std::vector<HeldId> held = {}) {
  SlotView s;
  s.item = true;
  s.passives = std::move(passives);
  for (std::size_t k = 0; k < held.size() && k < s.held.size(); ++k) s.held[k] = held[k];
  return s;
}

int Count(const std::vector<Step>& steps, StepKind kind) {
  int n = 0;
  for (const Step& s : steps) n += s.kind == kind ? 1 : 0;
  return n;
}

}  // namespace

// Two Purities, Pestilence and Might after the death path ended the death-type ones: the dead ids are cleared, Purity
// is activated once per slot (the second stacks), Pestilence once, Might (OnAgentDestroyed, live) untouched.
GMS_TEST(ItemPassivesRespawnPurityPestilence) {
  Inventory inv;
  inv[0] = S({kPurityDef}, {H(0x84000001, kPurityDef, false)});
  inv[1] = S({kPurityDef}, {H(0x84000001, kPurityDef, false)});
  inv[2] = S({kPestDef}, {H(0x84000002, kPestDef, false)});
  inv[3] = S({kMightDef}, {H(0x84000003, kMightDef, true)});
  const std::vector<Step> st = PlanRespawn(inv);
  const int clears = Count(st, StepKind::kClear), activates = Count(st, StepKind::kActivate);
  CHECK_EQ(clears, 3);
  CHECK_EQ(activates, 3);
  CHECK_EQ(st.size(), static_cast<std::size_t>(6));
  // the clears come first, then the slots in order, each into its first free entry (the cleared one)
  CHECK(st[3].kind == StepKind::kActivate);
  CHECK_EQ(st[3].slot, 0);
  CHECK_EQ(st[3].k, 0);
  CHECK_EQ(st[3].guid, kPurity);
  CHECK_EQ(st[4].slot, 1);
  CHECK_EQ(st[4].guid, kPurity);
  CHECK_EQ(st[5].slot, 2);
  CHECK_EQ(st[5].guid, kPestilence);
  for (const Step& s : st) CHECK(s.guid != kMight);
}

// Faith: two passives, the regeneration (death) gone and the shield (destroyed) live in entry 0: the regeneration goes
// into the free entry 1, the shield stays.
GMS_TEST(ItemPassivesRespawnTwoPassivesOneKept) {
  Inventory inv;
  inv[4] = S({kRegenDef, kShieldDef}, {H(0x84000010, kShieldDef, true), H(0x84000011, kRegenDef, false)});
  const std::vector<Step> st = PlanRespawn(inv);
  CHECK_EQ(st.size(), static_cast<std::size_t>(2));
  CHECK(st[0].kind == StepKind::kClear);
  CHECK_EQ(st[0].k, 1);
  CHECK(st[1].kind == StepKind::kActivate);
  CHECK_EQ(st[1].slot, 4);
  CHECK_EQ(st[1].k, 1);
  CHECK_EQ(st[1].guid, kRegen);
}

// A Unique passive in two slots at ranks 1 and 2 (Control, Inevitability): one activation at rank 2 in the first slot,
// the second slot shares its instance.
GMS_TEST(ItemPassivesRespawnUniqueHighestRank) {
  Inventory inv;
  const PassiveDef r1 = P(kSlow, 1, kActUnique, kDuration | kDeath), r2 = P(kSlow, 2, kActUnique, kDuration | kDeath);
  inv[0] = S({r1});  // already cleared while dead
  inv[5] = S({r2});
  const std::vector<Step> st = PlanRespawn(inv);
  CHECK_EQ(st.size(), static_cast<std::size_t>(2));
  CHECK(st[0].kind == StepKind::kActivate);
  CHECK_EQ(st[0].slot, 0);
  CHECK_EQ(st[0].rank, 2);
  CHECK(st[1].kind == StepKind::kShare);
  CHECK_EQ(st[1].slot, 5);
  CHECK_EQ(st[1].guid, kSlow);
}

// A Unique death-type passive still live in one slot (bought while dead, not yet swept): the other slot shares it,
// nothing new starts.
GMS_TEST(ItemPassivesRespawnUniqueLiveElsewhere) {
  Inventory inv;
  inv[1] = S({kPestDef}, {H(0x84000020, kPestDef, true)});
  inv[2] = S({kPestDef});
  const std::vector<Step> st = PlanRespawn(inv);
  CHECK_EQ(st.size(), static_cast<std::size_t>(1));
  CHECK(st[0].kind == StepKind::kShare);
  CHECK_EQ(st[0].slot, 2);
  CHECK_EQ(st[0].k, 0);
}

// Everything live (death_keep_item_passives, or nothing ended): no step.
GMS_TEST(ItemPassivesRespawnNothingToDo) {
  Inventory inv;
  inv[0] = S({kPurityDef}, {H(0x84000001, kPurityDef, true)});
  inv[1] = S({kRegenDef, kShieldDef}, {H(0x84000002, kRegenDef, true), H(0x84000003, kShieldDef, true)});
  CHECK(PlanRespawn(inv).empty());
  Inventory empty;
  CHECK(PlanRespawn(empty).empty());
}

// An unsupported activation type and a full slot are reported, not activated.
GMS_TEST(ItemPassivesRespawnUnsupportedAndFull) {
  Inventory inv;
  inv[0] = S({P(kOdd, 1, 8, kDeath)});
  const PassiveDef d = P(kMight, 1, kActUnique, kDestroyed);
  inv[1] = S({kPurityDef, d}, {H(1, d, true), H(2, d, true), H(3, d, true), H(4, d, true)});
  const std::vector<Step> st = PlanRespawn(inv);
  CHECK_EQ(st.size(), static_cast<std::size_t>(2));
  CHECK(st[0].kind == StepKind::kUnsupported);
  CHECK_EQ(st[0].activation, 8);
  CHECK(st[1].kind == StepKind::kFull);
  CHECK_EQ(st[1].slot, 1);
  CHECK_EQ(Count(st, StepKind::kActivate), 0);
}

// While dead: a dead id is cleared; a live death-type passive (a purchase while dead) is ended once and cleared from
// every slot that holds it; a live OnAgentDestroyed passive stays.
GMS_TEST(ItemPassivesWhileDead) {
  Inventory inv;
  inv[0] = S({kPurityDef}, {H(0x84000001, kPurityDef, false)});
  inv[1] = S({kPestDef}, {H(0x84000005, kPestDef, true)});
  inv[2] = S({kPestDef}, {H(0x84000005, kPestDef, true)});
  inv[3] = S({kMightDef, kRegenDef}, {H(0x84000006, kMightDef, true), H(0x84000007, kRegenDef, true)});
  const std::vector<DeadStep> st = PlanWhileDead(inv);
  CHECK_EQ(st.size(), static_cast<std::size_t>(4));
  CHECK_EQ(st[0].slot, 0);
  CHECK(!st[0].end);
  CHECK_EQ(st[1].slot, 1);
  CHECK(st[1].end);
  CHECK_EQ(st[2].slot, 2);
  CHECK(!st[2].end);  // the same instance: ended once
  CHECK_EQ(st[2].id, 0x84000005u);
  CHECK_EQ(st[3].slot, 3);
  CHECK_EQ(st[3].k, 1);
  CHECK(st[3].end);
  Inventory clean;
  clean[0] = S({kMightDef}, {H(0x84000006, kMightDef, true)});
  CHECK(PlanWhileDead(clean).empty());
}

// After the while-dead sweep the respawn activates what it cleared and ended (the sweep's result fed back).
GMS_TEST(ItemPassivesDeathThenRespawn) {
  Inventory inv;
  inv[0] = S({kRegenDef}, {H(0x84000030, kRegenDef, true)});
  inv[1] = S({kRegenDef}, {H(0x84000030, kRegenDef, true)});
  for (const DeadStep& d : PlanWhileDead(inv)) inv[d.slot].held[d.k] = HeldId{};
  CHECK(PlanWhileDead(inv).empty());
  const std::vector<Step> st = PlanRespawn(inv);
  CHECK_EQ(Count(st, StepKind::kActivate), 2);  // a Stacks passive: one activation per slot, the stack count 2 again
  CHECK_EQ(Count(st, StepKind::kClear), 0);
}
