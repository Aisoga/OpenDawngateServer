#include "hook.h"

#include <windows.h>

#include <cstring>

#include "log.h"

void* HookIat(const char* dll, const char* func, void* replacement, unsigned ordinal) {
  auto* base = reinterpret_cast<unsigned char*>(GetModuleHandleA(nullptr));
  auto* dos = reinterpret_cast<IMAGE_DOS_HEADER*>(base);
  auto* nt = reinterpret_cast<IMAGE_NT_HEADERS32*>(base + dos->e_lfanew);
  const auto& dir = nt->OptionalHeader.DataDirectory[IMAGE_DIRECTORY_ENTRY_IMPORT];
  for (auto* imp = reinterpret_cast<IMAGE_IMPORT_DESCRIPTOR*>(base + dir.VirtualAddress); imp->Name; ++imp) {
    if (_stricmp(reinterpret_cast<char*>(base + imp->Name), dll) != 0) continue;
    auto* names = reinterpret_cast<IMAGE_THUNK_DATA32*>(base + imp->OriginalFirstThunk);
    auto* slots = reinterpret_cast<IMAGE_THUNK_DATA32*>(base + imp->FirstThunk);
    for (; names->u1.AddressOfData; ++names, ++slots) {
      if (IMAGE_SNAP_BY_ORDINAL32(names->u1.Ordinal)) {
        if (!ordinal || IMAGE_ORDINAL32(names->u1.Ordinal) != ordinal) continue;
      } else {
        auto* byName = reinterpret_cast<IMAGE_IMPORT_BY_NAME*>(base + names->u1.AddressOfData);
        if (std::strcmp(reinterpret_cast<char*>(byName->Name), func) != 0) continue;
      }
      DWORD old;
      VirtualProtect(&slots->u1.Function, 4, PAGE_READWRITE, &old);
      void* prev = reinterpret_cast<void*>(slots->u1.Function);
      slots->u1.Function = reinterpret_cast<DWORD>(replacement);
      VirtualProtect(&slots->u1.Function, 4, old, &old);
      return prev;
    }
  }
  return nullptr;
}

void* HookDetour(std::uintptr_t target, const unsigned char* expect, std::size_t len, void* replacement) {
  auto* p = reinterpret_cast<unsigned char*>(target);
  if (len < 5 || std::memcmp(p, expect, len) != 0) {
    Log("HookDetour: bytes at %08X don't match; not hooked", static_cast<unsigned>(target));
    return nullptr;
  }
  auto* tramp = static_cast<unsigned char*>(VirtualAlloc(nullptr, 64, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE));
  if (!tramp) return nullptr;
  std::memcpy(tramp, p, len);
  tramp[len] = 0xE9;
  *reinterpret_cast<std::int32_t*>(tramp + len + 1) =
      static_cast<std::int32_t>(target + len - (reinterpret_cast<std::uintptr_t>(tramp) + len + 5));
  DWORD old;
  VirtualProtect(p, len, PAGE_EXECUTE_READWRITE, &old);
  p[0] = 0xE9;
  *reinterpret_cast<std::int32_t*>(p + 1) =
      static_cast<std::int32_t>(reinterpret_cast<std::uintptr_t>(replacement) - (target + 5));
  for (std::size_t i = 5; i < len; ++i) p[i] = 0x90;
  VirtualProtect(p, len, old, &old);
  FlushInstructionCache(GetCurrentProcess(), p, len);
  return tramp;
}

bool HookReplace(std::uintptr_t target, const unsigned char* expect, std::size_t len, void* replacement) {
  auto* p = reinterpret_cast<unsigned char*>(target);
  if (len < 5 || std::memcmp(p, expect, len) != 0) {
    Log("HookReplace: bytes at %08X don't match; not replaced", static_cast<unsigned>(target));
    return false;
  }
  DWORD old;
  VirtualProtect(p, len, PAGE_EXECUTE_READWRITE, &old);
  p[0] = 0xE9;
  *reinterpret_cast<std::int32_t*>(p + 1) =
      static_cast<std::int32_t>(reinterpret_cast<std::uintptr_t>(replacement) - (target + 5));
  for (std::size_t i = 5; i < len; ++i) p[i] = 0x90;
  VirtualProtect(p, len, old, &old);
  FlushInstructionCache(GetCurrentProcess(), p, len);
  return true;
}
