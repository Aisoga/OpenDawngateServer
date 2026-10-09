#include "core/common/bytes.h"

#include <cctype>
#include <cstdio>
#include <cstring>

namespace gms {

std::string Hex(const uint8_t* p, size_t n, size_t max) {
  static const char kDigits[] = "0123456789abcdef";
  std::string s;
  s.reserve((n < max ? n : max) * 2 + 3);
  for (size_t i = 0; i < n && i < max; ++i) {
    s += kDigits[p[i] >> 4];
    s += kDigits[p[i] & 15];
  }
  if (n > max) s += "...";
  return s;
}

bool ParseHex(const std::string& text, Bytes& out) {
  out.clear();
  std::string h;
  for (char c : text)
    if (std::isxdigit(static_cast<unsigned char>(c))) h += c;
  if (h.empty() || h.size() % 2) return false;
  out.reserve(h.size() / 2);
  for (size_t i = 0; i < h.size(); i += 2) {
    auto nibble = [](char c) -> uint8_t {
      if (c >= '0' && c <= '9') return static_cast<uint8_t>(c - '0');
      if (c >= 'a' && c <= 'f') return static_cast<uint8_t>(c - 'a' + 10);
      return static_cast<uint8_t>(c - 'A' + 10);
    };
    out.push_back(static_cast<uint8_t>(nibble(h[i]) << 4 | nibble(h[i + 1])));
  }
  return true;
}

void PutU8(Bytes& b, uint8_t v) { b.push_back(v); }
void PutU16LE(Bytes& b, uint16_t v) {
  PutU8(b, static_cast<uint8_t>(v));
  PutU8(b, static_cast<uint8_t>(v >> 8));
}
void PutU32LE(Bytes& b, uint32_t v) {
  for (int i = 0; i < 4; ++i) PutU8(b, static_cast<uint8_t>(v >> (8 * i)));
}
void PutU64LE(Bytes& b, uint64_t v) {
  for (int i = 0; i < 8; ++i) PutU8(b, static_cast<uint8_t>(v >> (8 * i)));
}
void PutI64LE(Bytes& b, int64_t v) { PutU64LE(b, static_cast<uint64_t>(v)); }
void PutF32LE(Bytes& b, float v) {
  uint32_t u;
  std::memcpy(&u, &v, 4);
  PutU32LE(b, u);
}
void PutU16BE(Bytes& b, uint16_t v) {
  PutU8(b, static_cast<uint8_t>(v >> 8));
  PutU8(b, static_cast<uint8_t>(v));
}
void PutU32BE(Bytes& b, uint32_t v) {
  for (int i = 3; i >= 0; --i) PutU8(b, static_cast<uint8_t>(v >> (8 * i)));
}

uint32_t GetU32LE(const uint8_t* p) {
  return static_cast<uint32_t>(p[0]) | static_cast<uint32_t>(p[1]) << 8 | static_cast<uint32_t>(p[2]) << 16 |
         static_cast<uint32_t>(p[3]) << 24;
}
uint64_t GetU64LE(const uint8_t* p) {
  uint64_t v = 0;
  for (int i = 7; i >= 0; --i) v = v << 8 | p[i];
  return v;
}
int64_t GetI64LE(const uint8_t* p) { return static_cast<int64_t>(GetU64LE(p)); }
float GetF32LE(const uint8_t* p) {
  const uint32_t u = GetU32LE(p);
  float f;
  std::memcpy(&f, &u, 4);
  return f;
}

}  // namespace gms
