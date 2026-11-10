// RULES4a: the match's shop rules and each player's Shop on Death pass (shoprules.h).
#include "shoprules.h"

#include <cstdio>

#include "core/timing/clock.h"
#include "log.h"

namespace shoprules {
namespace {

using gms::rules::ItemPool;
using gms::rules::Rules;
using gms::rules::ShopAccess;
using gms::rules::Verdict;

Rules g_rules;
bool g_overrideShop = false, g_overridePool = false;
gms::rules::ShopPass g_pass[gms::rules::kMaxPlayers];
gms::rules::TextPace g_pace;

struct Stats {
  unsigned shopClosed = 0, notAllowed = 0, texts = 0, paced = 0, given = 0, taken = 0;
} g_st;

void SetShop(const std::string& id, const char* source) {
  ShopAccess s = ShopAccess::kNormal;
  if (!gms::rules::ParseShopAccess(id, &s)) {
    Log("shop rules: %s: shop \"%s\" is not known to this host: the normal shop", source, id.c_str());
    s = ShopAccess::kNormal;
  }
  g_rules.shop = s;
}

void SetPool(const std::string& id, const char* source) {
  ItemPool p = ItemPool::kAll;
  if (!gms::rules::ParseItemPool(id, &p)) {
    Log("shop rules: %s: itemPool \"%s\" is not known to this host: all items", source, id.c_str());
    p = ItemPool::kAll;
  }
  g_rules.pool = p;
}

void LogRules(const char* source) {
  Log("shop rules: %s: shop %s, itemPool %s%s", source, gms::rules::Name(g_rules.shop), gms::rules::Name(g_rules.pool),
      g_rules.Any() ? " (RULES4a: a human's purchase and sale are checked before the binary's; bots are not)" : "");
}

}  // namespace

void Configure(const std::string& shopOption, const std::string& poolOption) {
  g_overrideShop = !shopOption.empty();
  g_overridePool = !poolOption.empty();
  if (g_overrideShop) SetShop(shopOption, "[host] rules_shop");
  if (g_overridePool) SetPool(poolOption, "[host] rules_item_pool");
  if (g_overrideShop || g_overridePool)
    LogRules("the host options (a test override; startGame's rules won't replace them)");
}

void SetFromStart(const std::string& shop, const std::string& itemPool) {
  if (!g_overrideShop) SetShop(shop, "startGame");
  if (!g_overridePool) SetPool(itemPool, "startGame");
  LogRules("startGame");
}

const Rules& Current() { return g_rules; }

void OnPosition(unsigned index, bool dead, bool inReach, float distance) {
  if (g_rules.shop != ShopAccess::kOnDeath || index >= gms::rules::kMaxPlayers) return;
  if (!g_pass[index].Update(dead, inReach)) return;
  const bool has = g_pass[index].Has();
  if (has) ++g_st.given;
  else ++g_st.taken;
  const char* state = dead ? "dead" : inReach ? "alive in the shop's reach" : "alive and out of the shop's reach";
  Log("shop rules: player %u %s the Shop on Death pass (%s, %.2f from the base object)", index, has ? "gets" : "loses",
      state, distance);
}

bool HasPass(unsigned index) { return index < gms::rules::kMaxPlayers && g_pass[index].Has(); }

Verdict Check(unsigned index, unsigned wireType, std::uint32_t item) {
  if (!g_rules.Any()) return Verdict::kAllowed;
  const Verdict v =
      gms::rules::MayTransact(g_rules, gms::rules::TransactionOf(wireType), HasPass(index), item);
  if (v == Verdict::kShopClosed) ++g_st.shopClosed;
  if (v == Verdict::kItemNotAllowed) ++g_st.notAllowed;
  return v;
}

bool TextDue(unsigned index) {
  const bool due = g_pace.Due(index, gms::timing::NowMs());
  if (due) ++g_st.texts;
  else ++g_st.paced;
  return due;
}

void FormatStats(char* buf, std::size_t size) {
  std::snprintf(buf, size,
                "shop rules %s/%s: refused %u (shop closed %u, item not allowed %u), texts %u (paced %u), passes "
                "given %u, taken %u",
                gms::rules::Name(g_rules.shop), gms::rules::Name(g_rules.pool), g_st.shopClosed + g_st.notAllowed,
                g_st.shopClosed, g_st.notAllowed, g_st.texts, g_st.paced, g_st.given, g_st.taken);
}

}  // namespace shoprules
