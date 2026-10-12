// D15b: the test aid `<s> gold <amount>` (gms/engine/commands.cpp cmds::GrantGoldNow) in steps the engine can carry.
// Engine-free and header-only, so gms_tests checks it without the game.
//
// LabsPlayer_GrantGold queues a CombatEvent with the gold delta, and CombatEvent.cpp 67 asserts
// "nDelta <= 0xffffU | Optimization fail - Gold Delta (...) too large to pack into 16 bits [0,65535]": the event packs
// the delta into 16 bits (seen with `gold 250000` in every test match that used it; the grant itself still arrived). So
// the aid grants a large amount as several grants of at most kMaxGoldDelta each.
#pragma once

#include <vector>

namespace gms::cmdgold {

constexpr double kMaxGoldDelta = 65535.0;  // CombatEvent.cpp 67: the delta's 16 bits

// The grants that add up to `amount`: none for amount <= 0, else ceil(amount / maxStep) steps, all maxStep but the
// last (the remainder).
inline std::vector<double> GoldGrantSteps(double amount, double maxStep = kMaxGoldDelta) {
  std::vector<double> steps;
  if (!(amount > 0.0) || !(maxStep > 0.0)) return steps;
  double left = amount;
  while (left > maxStep) {
    steps.push_back(maxStep);
    left -= maxStep;
  }
  if (left > 0.0) steps.push_back(left);
  return steps;
}

}  // namespace gms::cmdgold
