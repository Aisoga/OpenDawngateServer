// A minimal test runner for gms/ (no third-party framework; ADR-2 keeps the C++ side free of libraries).
// Header-only, so any test executable under gms/ (gms_tests, C1's control tests) can use it:
//
//   #include "tests/check.h"
//   GMS_TEST(Name) { CHECK(cond); CHECK_EQ(a, b); CHECK_HEX(bytes, "8400"); }
//   int main(int argc, char** argv) { return gmstest::RunAll(argc, argv); }   // once per executable
//
// RunAll runs every registered test (or those whose name contains argv[1]) and returns 0 if all passed.
#pragma once

#include <cstdio>
#include <exception>
#include <sstream>
#include <string>
#include <vector>

namespace gmstest {

struct Case {
  const char* name;
  void (*fn)();
};

inline std::vector<Case>& Registry() {
  static std::vector<Case> cases;
  return cases;
}
inline int& Failures() {
  static int failures = 0;
  return failures;
}

struct Register {
  Register(const char* name, void (*fn)()) { Registry().push_back({name, fn}); }
};

inline void Fail(const char* file, int line, const std::string& what) {
  ++Failures();
  std::printf("  FAILED %s(%d): %s\n", file, line, what.c_str());
}

template <class T>
std::string Show(const T& v) {
  std::ostringstream s;
  s << v;
  return s.str();
}
inline std::string Show(const unsigned char& v) { return std::to_string(static_cast<unsigned>(v)); }
inline std::string Show(const signed char& v) { return std::to_string(static_cast<int>(v)); }

inline int RunAll(int argc, char** argv) {
  const std::string filter = argc > 1 ? argv[1] : "";
  int run = 0, failed = 0;
  for (const Case& c : Registry()) {
    if (!filter.empty() && std::string(c.name).find(filter) == std::string::npos) continue;
    ++run;
    const int before = Failures();
    std::printf("[ RUN  ] %s\n", c.name);
    try {
      c.fn();
    } catch (const std::exception& e) {
      Fail(__FILE__, __LINE__, std::string("exception: ") + e.what());
    }
    const bool ok = Failures() == before;
    failed += ok ? 0 : 1;
    std::printf("[ %s ] %s\n", ok ? " OK " : "FAIL", c.name);
  }
  std::printf("%d test(s), %d failed\n", run, failed);
  return failed || run == 0 ? 1 : 0;
}

}  // namespace gmstest

#define GMS_TEST(name)                                       \
  static void name();                                        \
  static gmstest::Register name##_registration(#name, name); \
  static void name()

#define CHECK(cond)                                         \
  do {                                                      \
    if (!(cond)) gmstest::Fail(__FILE__, __LINE__, #cond);  \
  } while (0)

#define CHECK_EQ(a, b)                                                                                     \
  do {                                                                                                     \
    const auto& check_a_ = (a);                                                                            \
    const auto& check_b_ = (b);                                                                            \
    if (!(check_a_ == check_b_))                                                                           \
      gmstest::Fail(__FILE__, __LINE__,                                                                    \
                    std::string(#a " == " #b ": ") + gmstest::Show(check_a_) + " vs " + gmstest::Show(check_b_)); \
  } while (0)

// Compares a byte buffer (anything gms::Hex accepts) with lower-case hex.
#define CHECK_HEX(bytes, hex) CHECK_EQ(gms::Hex((bytes), static_cast<size_t>(-1)), std::string(hex))
