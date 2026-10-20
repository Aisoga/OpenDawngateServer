// [host] test_lua's items: "<seconds>:<file>" runs a Lua probe once that many seconds
// after the first EnterGame; since GUARDCORE "<from>-<to>/<step>:<file>" runs it every <step> s from <from> to <to>
// (both included), so a long probe session needn't list hundreds of calls in the 4 KB option. Header-only, engine-free;
// gms_tests TestLuaItems.
#pragma once

#include <cstdlib>
#include <string>
#include <vector>

namespace gms {
namespace commands {

struct TimedLuaItem {
  double at = 0;
  std::string file;
};

constexpr int kMaxTestLuaRepeats = 5000;

// Appends the calls one item names; false (nothing appended) for a malformed item: no colon, an empty file, a
// non-number, a step <= 0, to < from, or more than kMaxTestLuaRepeats calls.
inline bool ParseTestLuaItem(const std::string& item, std::vector<TimedLuaItem>* out) {
  const size_t colon = item.find(':');
  if (colon == std::string::npos || colon == 0 || colon + 1 >= item.size()) return false;
  const std::string when = item.substr(0, colon), file = item.substr(colon + 1);
  auto number = [](const std::string& s, double* v) {
    if (s.empty()) return false;
    char* end = nullptr;
    *v = std::strtod(s.c_str(), &end);
    return end && *end == 0;
  };
  const size_t dash = when.find('-', 1);  // not a leading sign
  if (dash == std::string::npos) {
    double at = 0;
    if (!number(when, &at)) return false;
    out->push_back({at, file});
    return true;
  }
  const size_t slash = when.find('/', dash);
  double from = 0, to = 0, step = 0;
  if (slash == std::string::npos || !number(when.substr(0, dash), &from) ||
      !number(when.substr(dash + 1, slash - dash - 1), &to) || !number(when.substr(slash + 1), &step))
    return false;
  if (step <= 0 || to < from) return false;
  const double n = (to - from) / step;
  if (n + 1 > kMaxTestLuaRepeats) return false;
  const int count = static_cast<int>(n + 1e-9) + 1;
  for (int i = 0; i < count; ++i) out->push_back({from + step * i, file});
  return true;
}

}  // namespace commands
}  // namespace gms
