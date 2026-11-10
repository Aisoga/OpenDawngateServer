// RULES4a: a custom game's shop rules (Shop on Death, the item pools) on the match host. The rule is engine-free in
// core/rules/shop.h (gms_tests Rules*); this part holds the match's rules and each player's Shop on Death pass, and
// commands.cpp asks Check before GameSimulator_OnItemTransaction's switch (and before REFUND1's undo).
// Where the rules come from: startGame.rules (shop, itemPool; host/control.cpp calls SetFromStart in the start's
// frame), or the test overrides [host] rules_shop / rules_item_pool ("" = startGame's; set, they win over it).
// Bot seats are never checked (RULES4b teaches the bots; the server's checks keep the bots off with these options).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "core/rules/shop.h"

namespace shoprules {

// At the install: the test overrides (empty = none).
void Configure(const std::string& shopOption, const std::string& poolOption);
// startGame's rules (an id this host doesn't know is logged and leaves that category at its default).
void SetFromStart(const std::string& shop, const std::string& itemPool);
const gms::rules::Rules& Current();

// Every tick and before each of a human's transactions: the player's creature state. Logs a change of the pass.
void OnPosition(unsigned index, bool dead, bool inReach, float distance);
bool HasPass(unsigned index);

// The verdict for a human's transaction (wire type 1-5, the item's id); counts it. Call OnPosition first.
gms::rules::Verdict Check(unsigned index, unsigned wireType, std::uint32_t item);
// Whether the refusal's text is due for this player now (at most one every gms::rules::kTextPaceMs).
bool TextDue(unsigned index);

// "shop rules normal/all: refused N (shop closed N, item not allowed N), texts N (paced N), passes given N, taken N".
void FormatStats(char* buf, std::size_t size);

}  // namespace shoprules
