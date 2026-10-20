// gms/core/bots (DESECBOT-2): a profile's own build with its choices, without the engine. Header-only.
//
// A bot shops through ShopPlanner (shopping.h) over one item list. For most bots the list is the shaper's ItemBuild
// asset (ShopOf in gms/engine/bots.cpp: <BaseClass>.Noun's recommendedItems[0], else RecommendedItemBuild_<Shaper>);
// an ItemBuild holds only ordered names, so a player guide's "this when that" can't be written into one. A profile
// listed in GuideBuilds() shops by its own list instead: entries in order, each with one or more options (an option is
// a list of item asset names without ".Item", resolved like the ItemBuild's items) and a rule that picks the option.
//
// Rules [assumed thresholds]:
//   kFixed        option 0.
//   kWhenAhead    option 1 when the bot is ahead (IsAhead: its level at least the enemies' average + 1, or more kills
//                 than deaths), else option 0.
//   kMagicAllies  option 0 with at least kMagicAlliesWanted allied shapers (the bot not counted) whose PlayerClass
//                 ShaperStyle_DamageStyle is magical (1; hybrid doesn't count), else option 1 when an allied Gladiator
//                 (role 0) is physical (damage style 0), else option 0.
//   notBeforeS    an entry whose turn comes before this match time goes behind the later entries (the guide's
//                 "Harmony after mid-game, Stability first").
// The choice is made when the entry's first purchase is made and then kept (BuildMemory), so a change of the context
// (the bot falls behind, an ally leaves, the minute passes) never leaves a half-built chain: before that, the option
// with the most of its items owned wins over the rule, and an entry deferred once its turn came stays behind.
#pragma once

#include <cstdint>
#include <cstdio>
#include <functional>
#include <map>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "shopping.h"

namespace gms::bots {

enum class BuildRule { kFixed, kWhenAhead, kMagicAllies };

struct BuildEntryDef {
  const char* label;                              // for the log ("first back", "Oppression")
  BuildRule rule;
  std::vector<std::vector<const char*>> options;  // option 0 the default; item asset names without ".Item"
  std::vector<const char*> optionLabels;          // one per option, for the log
  double notBeforeS = 0;                          // > 0: deferred when its turn comes before this match time
};

struct GuideBuildDef {
  const char* profile;  // the BotClass name without ".BotClass"
  const char* source;   // where the build comes from (the log)
  std::vector<BuildEntryDef> entries;
};

constexpr int kMagicAlliesWanted = 2;      // the guide: Oppression "with 2+ magic-damage allies" [guide]
constexpr double kHarmonyNotBeforeS = 1200;  // minute 20 [assumed: the guide's "after mid-game"]

// The builds, one per profile. DESECBOT-2: GLG_Tree's "Desecrator, Bane of Maridia" (moba-champion.com guide 642,
// 2014-06-20) with its items as build 854's assets: Life and two potions (the potion rule,
// [host] bot_potions); first back Purity and Discipline, or Balance and Might when ahead (then Oppression over the
// Balance and Decay over the Might); Devotion, Subjugation, Decay, Oppression (Hostility instead without two magic
// allies and with a physical Gladiator), Harmony (not before minute 20: Stability first), Stability.
inline const std::vector<GuideBuildDef>& GuideBuilds() {
  static const std::vector<GuideBuildDef> kBuilds = {
      {"PC_Desecrator_Tactician",
       "GLG_Tree's guide",
       {
           {"start", BuildRule::kFixed, {{"Basic_Life", "Consumable_Potion_HealingPotion"}}, {"Life and potions"}},
           {"first back",
            BuildRule::kWhenAhead,
            {{"Advanced_Purity", "Advanced_Discipline"},
             {"Advanced_Balance", "Advanced_Might", "Legendary_Oppression", "Legendary_Decay"}},
            {"Purity and Discipline", "Balance and Might (ahead)"}},
           {"Devotion", BuildRule::kFixed, {{"Legendary_Devotion"}}, {"Devotion"}},
           {"Subjugation", BuildRule::kFixed, {{"Legendary_Subjugation"}}, {"Subjugation"}},
           {"Decay", BuildRule::kFixed, {{"Legendary_Decay"}}, {"Decay"}},
           {"Oppression",
            BuildRule::kMagicAllies,
            {{"Legendary_Oppression"}, {"Legendary_Hostility"}},
            {"Oppression", "Hostility (a physical Gladiator ally)"}},
           {"Harmony", BuildRule::kFixed, {{"Legendary_Harmony"}}, {"Harmony"}, kHarmonyNotBeforeS},
           {"Stability", BuildRule::kFixed, {{"Legendary_Stability"}}, {"Stability"}},
       }},
      // MIKELLABOT: MikellaOnly's "No Challenge 2.0" (moba-champion.com guide 69, 2014-06-18), its "3 Shot Kill" build:
      // Hunger and a potion; first back Consumption, Time and Power (the guide's choice without a Will: the bots never
      // sell, and a Will would take the slot the sixth legendary needs); Dominance, Energy, Resonance, Voracity,
      // Aggression and Command, Rage, Justice, Fate. The guide's situational swaps (Valor, Hope, Momentum, Insight,
      // Pain, Ruin, Strife, Ambition) are not modelled [assumed].
      {"PC_MerchantPrincess_Gladiator",
       "MikellaOnly's guide",
       {
           {"start", BuildRule::kFixed, {{"Basic_Hunger", "Consumable_Potion_HealingPotion"}}, {"Hunger and potions"}},
           {"first back",
            BuildRule::kFixed,
            {{"Advanced_Consumption", "Basic_Time", "Basic_Power"}},
            {"Consumption, Time and Power"}},
           {"Dominance", BuildRule::kFixed, {{"Legendary_Dominance"}}, {"Dominance"}},
           {"Energy", BuildRule::kFixed, {{"Advanced_Energy"}}, {"Energy"}},
           {"Resonance", BuildRule::kFixed, {{"Legendary_Resonance"}}, {"Resonance"}},
           {"Voracity", BuildRule::kFixed, {{"Legendary_Voracity"}}, {"Voracity"}},
           {"Aggression and Command",
            BuildRule::kFixed,
            {{"Advanced_Aggression", "Advanced_Command"}},
            {"Aggression and Command"}},
           {"Rage", BuildRule::kFixed, {{"Legendary_Rage"}}, {"Rage"}},
           {"Justice", BuildRule::kFixed, {{"Legendary_Justice"}}, {"Justice"}},
           {"Fate", BuildRule::kFixed, {{"Legendary_Fate"}}, {"Fate"}},
       }},
      // MOYABOT: mcscrag's "Moya - The Salt-Water Sailor" (moba-champion.com guide 143, 2014-04-18), its "Diving
      // Anti-Carry" build in its build order: Resilience and potions, Might, Freedom, Pride, Strife (over the Might),
      // Vengeance (over the Pride) before Stability (over the Freedom) and Hope; then Adamance and Equilibrium. The
      // last item Betrayal when ahead (the guide's "On-Hit DPS" build for a bot far ahead has it; a reader's comment
      // swaps it for the Equilibrium) [assumed: which of the two builds' items differ is the choice].
      {"PC_Otter_Hunter",
       "mcscrag's guide",
       {
           {"start", BuildRule::kFixed, {{"Basic_Resilience", "Consumable_Potion_HealingPotion"}},
            {"Resilience and potions"}},
           {"Might", BuildRule::kFixed, {{"Advanced_Might"}}, {"Might"}},
           {"Freedom", BuildRule::kFixed, {{"Advanced_Freedom"}}, {"Freedom"}},
           {"Pride", BuildRule::kFixed, {{"Advanced_Pride"}}, {"Pride"}},
           {"Strife", BuildRule::kFixed, {{"Legendary_Strife"}}, {"Strife"}},
           {"Vengeance", BuildRule::kFixed, {{"Legendary_Vengeance"}}, {"Vengeance"}},
           {"Stability", BuildRule::kFixed, {{"Legendary_Stability"}}, {"Stability"}},
           {"Hope", BuildRule::kFixed, {{"Legendary_Hope"}}, {"Hope"}},
           {"Adamance", BuildRule::kFixed, {{"Legendary_Adamance"}}, {"Adamance"}},
           {"Equilibrium",
            BuildRule::kWhenAhead,
            {{"Legendary_Equilibrium"}, {"Legendary_Betrayal"}},
            {"Equilibrium", "Betrayal (ahead)"}},
       }},
  };
  return kBuilds;
}

// The build of `botClass` (with or without ".BotClass"), or nullptr: the bot shops by its ItemBuild.
inline const GuideBuildDef* GuideBuildFor(std::string botClass) {
  const std::size_t dot = botClass.find('.');
  if (dot != std::string::npos) botClass.resize(dot);
  for (const GuideBuildDef& d : GuideBuilds())
    if (botClass == d.profile) return &d;
  return nullptr;
}

// What the rules look at, read by the host at the shop.
struct ShopContext {
  double now = 0;           // match time (s)
  int level = 1;            // the bot's level
  int kills = 0, deaths = 0;
  double enemyLevel = 0;    // the enemy players' average level (0 = none known)
  int magicAllies = 0;      // allied shapers (not the bot) with a magical damage style
  bool physicalGladiatorAlly = false;
};

inline bool IsAhead(const ShopContext& c) {
  return (c.enemyLevel > 0 && c.level >= c.enemyLevel + 1.0) || c.kills > c.deaths;
}

struct BuildEntry {
  std::string label;
  BuildRule rule = BuildRule::kFixed;
  std::vector<std::vector<std::uint32_t>> options;  // item ids per option (unresolved names left out)
  std::vector<std::string> optionLabels;
  double notBeforeS = 0;
};

// What a bot's shopping has settled so far.
struct BuildMemory {
  std::map<int, int> chosen;  // entry -> option, from its first purchase on
  std::set<int> deferred;     // entries put behind the later ones once their turn came early
};

struct ResolvedBuild {
  std::vector<std::uint32_t> items;  // the ShopPlanner's list
  std::vector<int> entryOf;          // per item: its entry
  std::vector<int> optionOf;         // per entry: the option in use
  std::vector<std::string> whyOf;    // per entry: why that option
  std::vector<int> deferredNow;      // entries behind the later ones in this list
};

class GuidePlanner {
 public:
  GuidePlanner(const ItemCatalog* catalog, std::vector<BuildEntry> entries)
      : catalog_(catalog), entries_(std::move(entries)) {}

  // The entries of `def` with their names resolved by `idOf` (0 = unknown, left out).
  static std::vector<BuildEntry> Resolve(const GuideBuildDef& def,
                                         const std::function<std::uint32_t(const std::string&)>& idOf) {
    std::vector<BuildEntry> out;
    for (const BuildEntryDef& d : def.entries) {
      BuildEntry e;
      e.label = d.label;
      e.rule = d.rule;
      e.notBeforeS = d.notBeforeS;
      for (std::size_t o = 0; o < d.options.size(); ++o) {
        std::vector<std::uint32_t> ids;
        for (const char* n : d.options[o])
          if (const std::uint32_t id = idOf(n)) ids.push_back(id);
        e.options.push_back(ids);
        e.optionLabels.push_back(o < d.optionLabels.size() ? d.optionLabels[o] : std::to_string(o));
      }
      out.push_back(e);
    }
    return out;
  }

  // The option entry `e` uses now, and why.
  int Choose(int e, const Inventory& inv, const ShopContext& c, const BuildMemory& m, std::string* why) const {
    const BuildEntry& en = entries_[static_cast<std::size_t>(e)];
    auto it = m.chosen.find(e);
    if (it != m.chosen.end()) {
      if (why) *why = "chosen earlier";
      return it->second;
    }
    if (en.options.size() > 1) {  // the option with the most of its items owned (a tie: the rule)
      int best = -1, bestCount = 0;
      bool tie = false;
      for (std::size_t o = 0; o < en.options.size(); ++o) {
        const int n = OwnedCount(en.options[o], inv);
        if (n > bestCount) {
          best = static_cast<int>(o);
          bestCount = n;
          tie = false;
        } else if (n == bestCount && n > 0) {
          tie = true;
        }
      }
      if (best >= 0 && !tie) {
        if (why) *why = "owned";
        return best;
      }
    }
    int pick = 0;
    std::string w = "fixed";
    switch (en.rule) {
      case BuildRule::kFixed:
        break;
      case BuildRule::kWhenAhead:
        pick = IsAhead(c) ? 1 : 0;
        w = (pick ? "ahead: " : "not ahead: ") + Describe(c);
        break;
      case BuildRule::kMagicAllies:
        if (c.magicAllies >= kMagicAlliesWanted) {
          w = std::to_string(c.magicAllies) + " magic-damage allies";
        } else if (c.physicalGladiatorAlly) {
          pick = 1;
          w = std::to_string(c.magicAllies) + " magic-damage allies, a physical Gladiator ally";
        } else {
          w = std::to_string(c.magicAllies) + " magic-damage allies, no physical Gladiator ally";
        }
        break;
    }
    if (pick >= static_cast<int>(en.options.size())) pick = 0;
    if (why) *why = w;
    return pick;
  }

  ResolvedBuild Plan(const Inventory& inv, const ShopContext& c, const BuildMemory& m) const {
    ResolvedBuild r;
    r.optionOf.assign(entries_.size(), 0);
    r.whyOf.assign(entries_.size(), "");
    std::vector<int> order, later;
    for (std::size_t e = 0; e < entries_.size(); ++e) {
      const int ei = static_cast<int>(e);
      r.optionOf[e] = Choose(ei, inv, c, m, &r.whyOf[e]);
      const bool started = m.chosen.count(ei) != 0;
      const bool defer = m.deferred.count(ei) != 0 ||
                         (!started && entries_[e].notBeforeS > 0 && c.now < entries_[e].notBeforeS);
      (defer ? later : order).push_back(ei);
    }
    r.deferredNow = later;
    order.insert(order.end(), later.begin(), later.end());
    for (int e : order) {
      const auto& opts = entries_[static_cast<std::size_t>(e)].options;
      if (opts.empty()) continue;
      for (std::uint32_t id : opts[static_cast<std::size_t>(r.optionOf[static_cast<std::size_t>(e)])]) {
        r.items.push_back(id);
        r.entryOf.push_back(e);
      }
    }
    return r;
  }

  // The next step and the entry it serves (-1 with false: the build is done or blocked).
  bool Next(const Inventory& inv, const ShopContext& c, const BuildMemory& m, Purchase* out, int* entry,
            ResolvedBuild* plan = nullptr) const {
    ResolvedBuild r = Plan(inv, c, m);
    int index = -1;
    const bool ok = ShopPlanner(catalog_, r.items).Next(inv, out, &index);
    if (entry) *entry = ok && index >= 0 ? r.entryOf[static_cast<std::size_t>(index)] : -1;
    if (plan) *plan = std::move(r);
    return ok;
  }

  // After a purchase for `entry` (planned as `plan`): its option is kept, and an entry deferred because its turn came
  // early (it stood before `entry`) stays behind. Returns true when this settled a choice of more than one option.
  bool Commit(const ResolvedBuild& plan, int entry, BuildMemory* m) const {
    if (entry < 0 || entry >= static_cast<int>(entries_.size())) return false;
    const bool fresh = m->chosen.emplace(entry, plan.optionOf[static_cast<std::size_t>(entry)]).second;
    for (int d : plan.deferredNow)
      if (d < entry) m->deferred.insert(d);
    return fresh && entries_[static_cast<std::size_t>(entry)].options.size() > 1;
  }

  const std::vector<BuildEntry>& Entries() const { return entries_; }

  static std::string Describe(const ShopContext& c) {
    char buf[96];
    std::snprintf(buf, sizeof buf, "level %d vs %.1f, K/D %d/%d", c.level, c.enemyLevel, c.kills, c.deaths);
    return buf;
  }

 private:
  // How many of the option's items the inventory holds (the item itself or an upgrade of it).
  int OwnedCount(const std::vector<std::uint32_t>& items, const Inventory& inv) const {
    int n = 0;
    for (std::uint32_t item : items) {
      for (std::uint32_t owned : inv.items)
        if (owned && catalog_->Covers(owned, item)) {
          ++n;
          break;
        }
    }
    return n;
  }

  const ItemCatalog* catalog_;
  std::vector<BuildEntry> entries_;
};

}  // namespace gms::bots
