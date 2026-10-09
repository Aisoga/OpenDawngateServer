// Byte buffers for GMS messages (gms/core, G1). Every GMS body is raw little-endian memory as the client's readers
// memcpy it (GmsMessage_ReadBytes 0x00ADAF50), except the reflected structs, whose bytes come from elsewhere
// (gms_reflect.py today, the client's own writer in the host later). The writers here spell out the byte order, so
// the layouts don't depend on the build machine.
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace gms {

using Bytes = std::vector<uint8_t>;

// Lower-case hex without separators; at most `max` bytes, then "...".
std::string Hex(const uint8_t* p, size_t n, size_t max = 40);
inline std::string Hex(const Bytes& b, size_t max = 40) { return Hex(b.data(), b.size(), max); }

// Keeps only the hex digits of `text`, so separators, a UTF-8 BOM or the NULs of a UTF-16 file (PowerShell 5.1's `>`)
// don't matter. False if nothing is left or the digit count is odd.
bool ParseHex(const std::string& text, Bytes& out);

void PutU8(Bytes& b, uint8_t v);
void PutU16LE(Bytes& b, uint16_t v);
void PutU32LE(Bytes& b, uint32_t v);
void PutU64LE(Bytes& b, uint64_t v);
void PutI64LE(Bytes& b, int64_t v);
void PutF32LE(Bytes& b, float v);
void PutU16BE(Bytes& b, uint16_t v);
void PutU32BE(Bytes& b, uint32_t v);

uint32_t GetU32LE(const uint8_t* p);
uint64_t GetU64LE(const uint8_t* p);
int64_t GetI64LE(const uint8_t* p);
float GetF32LE(const uint8_t* p);

}  // namespace gms
