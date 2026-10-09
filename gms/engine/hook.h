// Minimal hooking for x86: IAT slots of Dawngate.exe and 5-byte JMP detours with a trampoline.
// Every detour names the exact bytes it expects at the target, so a different build fails loudly
// instead of patching the wrong code (the same rule as re/scripts/patch_client.py).
#pragma once

#include <cstddef>
#include <cstdint>

// Replaces the IAT slot of dll!func in the main module (imported by name, or by `ordinal` if given:
// Dawngate.exe imports WSOCK32 by ordinal). Returns the previous pointer or nullptr.
void* HookIat(const char* dll, const char* func, void* replacement, unsigned ordinal = 0);

// Detours `target` to `replacement`. `expect` are the first `len` (>= 5) bytes at target; they must
// be whole instructions without relative operands, because they are copied into the trampoline.
// Returns a callable trampoline (the original function) or nullptr if the bytes don't match.
void* HookDetour(std::uintptr_t target, const unsigned char* expect, std::size_t len, void* replacement);

// Replaces the function at `target` with `replacement` (a 5-byte JMP, the rest of `len` NOPed), without a trampoline:
// for functions the host rewrites whole, whose bytes may hold relative operands (D7-live's send stub 0x008F9090).
// `expect` are the first `len` (>= 5) bytes. False if they don't match.
bool HookReplace(std::uintptr_t target, const unsigned char* expect, std::size_t len, void* replacement);
