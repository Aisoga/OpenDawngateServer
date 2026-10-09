// Host mode, stage 1 (D1d): see host.h. Every hook here patches only Dawngate.exe's import table (or, for the boot
// hold, one slot of cBootState's vtable) and is installed only when the DGGMS_* environment says host mode.
#include "host.h"

#include <windows.h>

#define PSAPI_VERSION 2  // GetProcessMemoryInfo -> kernel32's K32GetProcessMemoryInfo, no psapi.lib
#include <psapi.h>

#include <intrin.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>

#include "engine/hook.h"
#include "engine/log.h"
#include "headless.h"

namespace {

HostConfig g_host;

// dgsim.ini [host] switches (all on by default).
bool g_holdBoot, g_blockSockets, g_hideWindow, g_redirectFolders;

// counters for the stats line
volatile LONG g_refusedSockets, g_refusedDns, g_bootHolds, g_hiddenWindows, g_refusedShows, g_messageBoxes, g_redirects;

DWORD Caller(void* ret) { return static_cast<DWORD>(reinterpret_cast<std::uintptr_t>(ret)); }

void Env(const char* name, char* out, DWORD size) {
  DWORD n = GetEnvironmentVariableA(name, out, size);
  if (n == 0 || n >= size) out[0] = 0;
}

// Creates every missing folder of an absolute path.
void MakeDirs(const char* path) {
  char buf[MAX_PATH];
  std::snprintf(buf, sizeof buf, "%s", path);
  for (char* p = buf + 3; *p; ++p)
    if (*p == '\\' || *p == '/') {
      char c = *p;
      *p = 0;
      CreateDirectoryA(buf, nullptr);
      *p = c;
    }
  CreateDirectoryA(buf, nullptr);
}

// ---------------------------------------------------------------------------------------------
// boot hold: cBootState (constructor 0x005426D0, 0x10 bytes "SP_SporeLabs/cBootState") has its vtable at
// 0x011DBE98; the state machine calls the current state's slot 10 (+0x28, cStateMachine 0x0054A470) every frame.
// Slot 10 is cBootState::Update (0x005425F0): step +8 counts 30 frames, then changes to kGameStateLogin (1), or to
// kGameStateLobby (2) when GetSporeOnline()->IsOffline() (0x0066BE20 -> 0x006B3310, SporeOnline +9); it also runs
// the client's network connectivity handler (0x00669FE0), which can change to the login state too.
constexpr std::uintptr_t kBootStateUpdateSlot = 0x011DBEC0;
constexpr std::uintptr_t kBootStateUpdate = 0x005425F0;

void __fastcall BootUpdateHold(void* self, void* /*edx*/) {
  if (InterlockedIncrement(&g_bootHolds) == 1) {
    Log("host: boot state %p held (cBootState::Update replaced; the front end stays in kGameStateBoot)", self);
    HostMark("first boot-state frame");
  }
}

bool PatchPointer(std::uintptr_t slot, std::uintptr_t expect, void* replacement) {
  auto* p = reinterpret_cast<std::uintptr_t*>(slot);
  if (*p != expect) {
    Log("host: %08X holds %08X, not %08X; not patched", static_cast<unsigned>(slot), static_cast<unsigned>(*p),
        static_cast<unsigned>(expect));
    return false;
  }
  DWORD old;
  if (!VirtualProtect(p, sizeof *p, PAGE_READWRITE, &old)) return false;
  *p = reinterpret_cast<std::uintptr_t>(replacement);
  VirtualProtect(p, sizeof *p, old, &old);
  return true;
}

// ---------------------------------------------------------------------------------------------
// socket guard (Dawngate.exe imports WSOCK32 by ordinal: socket 23, gethostbyname 52; WS2_32 by name)
constexpr DWORD kWsaENetDown = 10050;        // WSAENETDOWN
constexpr DWORD kWsaHostNotFound = 11001;    // WSAHOST_NOT_FOUND
constexpr UINT_PTR kInvalidSocket = ~static_cast<UINT_PTR>(0);

UINT_PTR WINAPI SocketHook(int af, int type, int protocol) {
  LONG k = InterlockedIncrement(&g_refusedSockets);
  if (k <= 20 || k % 100 == 0)
    Log("host: refused socket #%ld (af %d, type %d, protocol %d) returning to %08lX", k, af, type, protocol,
        Caller(_ReturnAddress()));
  SetLastError(kWsaENetDown);
  return kInvalidSocket;
}

UINT_PTR WINAPI WsaSocketHook(int af, int type, int protocol, void* info, unsigned group, DWORD flags) {
  LONG k = InterlockedIncrement(&g_refusedSockets);
  if (k <= 20 || k % 100 == 0)
    Log("host: refused WSASocketA #%ld (af %d, type %d, protocol %d, flags %lX) returning to %08lX", k, af, type, protocol,
        flags, Caller(_ReturnAddress()));
  (void)info, (void)group;
  SetLastError(kWsaENetDown);
  return kInvalidSocket;
}

void* WINAPI GetHostByNameHook(const char* name) {
  LONG k = InterlockedIncrement(&g_refusedDns);
  if (k <= 20 || k % 100 == 0) Log("host: refused gethostbyname #%ld (%s) returning to %08lX", k, name ? name : "(null)", Caller(_ReturnAddress()));
  SetLastError(kWsaHostNotFound);
  return nullptr;
}

// ---------------------------------------------------------------------------------------------
// hidden window
bool TopLevel(HWND h) { return h && !(GetWindowLongW(h, GWL_STYLE) & WS_CHILD); }

using CreateWindowExWFn = HWND(WINAPI*)(DWORD, LPCWSTR, LPCWSTR, DWORD, int, int, int, int, HWND, HMENU, HINSTANCE, LPVOID);
using CreateWindowExAFn = HWND(WINAPI*)(DWORD, LPCSTR, LPCSTR, DWORD, int, int, int, int, HWND, HMENU, HINSTANCE, LPVOID);
CreateWindowExWFn g_createWindowW;
CreateWindowExAFn g_createWindowA;

HWND WINAPI CreateWindowExWHook(DWORD ex, LPCWSTR cls, LPCWSTR title, DWORD style, int x, int y, int w, int h, HWND parent,
                                HMENU menu, HINSTANCE inst, LPVOID param) {
  bool top = !(style & WS_CHILD);
  DWORD use = top ? (style & ~WS_VISIBLE) : style;
  HWND r = g_createWindowW(ex, cls, title, use, x, y, w, h, parent, menu, inst, param);
  if (top) {
    LONG k = InterlockedIncrement(&g_hiddenWindows);
    if (k <= 20)
      Log("host: top-level window %p created hidden (class %ls, title '%ls', %dx%d, style %08lX -> %08lX)", r,
          IS_INTRESOURCE(cls) ? L"(atom)" : cls, title ? title : L"", w, h, style, use);
  }
  return r;
}

HWND WINAPI CreateWindowExAHook(DWORD ex, LPCSTR cls, LPCSTR title, DWORD style, int x, int y, int w, int h, HWND parent,
                                HMENU menu, HINSTANCE inst, LPVOID param) {
  bool top = !(style & WS_CHILD);
  DWORD use = top ? (style & ~WS_VISIBLE) : style;
  HWND r = g_createWindowA(ex, cls, title, use, x, y, w, h, parent, menu, inst, param);
  if (top) {
    LONG k = InterlockedIncrement(&g_hiddenWindows);
    if (k <= 20)
      Log("host: top-level window %p created hidden (class %s, title '%s', %dx%d, style %08lX -> %08lX)", r,
          IS_INTRESOURCE(cls) ? "(atom)" : cls, title ? title : "", w, h, style, use);
  }
  return r;
}

using ShowWindowFn = BOOL(WINAPI*)(HWND, int);
ShowWindowFn g_showWindow;
BOOL WINAPI ShowWindowHook(HWND h, int cmd) {
  if (TopLevel(h) && cmd != SW_HIDE) {
    LONG k = InterlockedIncrement(&g_refusedShows);
    if (k <= 10) Log("host: ShowWindow(%p, %d) -> SW_HIDE (returning to %08lX)", h, cmd, Caller(_ReturnAddress()));
    cmd = SW_HIDE;
  }
  return g_showWindow(h, cmd);
}

using SetWindowPosFn = BOOL(WINAPI*)(HWND, HWND, int, int, int, int, UINT);
SetWindowPosFn g_setWindowPos;
BOOL WINAPI SetWindowPosHook(HWND h, HWND after, int x, int y, int cx, int cy, UINT flags) {
  if (TopLevel(h)) {
    if (flags & SWP_SHOWWINDOW) {
      LONG k = InterlockedIncrement(&g_refusedShows);
      if (k <= 10) Log("host: SetWindowPos(%p) without SWP_SHOWWINDOW (returning to %08lX)", h, Caller(_ReturnAddress()));
    }
    flags = (flags & ~SWP_SHOWWINDOW) | SWP_NOACTIVATE;
  }
  return g_setWindowPos(h, after, x, y, cx, cy, flags);
}

BOOL WINAPI SetForegroundWindowHook(HWND h) {
  LONG k = InterlockedIncrement(&g_refusedShows);
  if (k <= 10) Log("host: SetForegroundWindow(%p) skipped (returning to %08lX)", h, Caller(_ReturnAddress()));
  return TRUE;
}

HWND WINAPI SetActiveWindowHook(HWND h) {
  LONG k = InterlockedIncrement(&g_refusedShows);
  if (k <= 10) Log("host: SetActiveWindow(%p) skipped (returning to %08lX)", h, Caller(_ReturnAddress()));
  return nullptr;
}

// The game installs WH_KEYBOARD_LL (0x00969BB7, callback 0x00969B10 swallows Alt+Tab, Alt+Esc and Ctrl+Esc for the
// whole desktop); a hidden host must not, and a low-level hook would also stall all input while the host's main
// thread is busy (level load).
using SetWindowsHookExAFn = HHOOK(WINAPI*)(int, HOOKPROC, HINSTANCE, DWORD);
SetWindowsHookExAFn g_setHook;
HHOOK WINAPI SetWindowsHookExAHook(int id, HOOKPROC fn, HINSTANCE mod, DWORD tid) {
  if (id == WH_KEYBOARD_LL || id == WH_MOUSE_LL) {
    Log("host: SetWindowsHookExA(%d, %p) refused (returning to %08lX)", id, fn, Caller(_ReturnAddress()));
    SetLastError(ERROR_ACCESS_DENIED);
    return nullptr;
  }
  return g_setHook(id, fn, mod, tid);
}

// A modal MessageBox (e.g. "Unable to establish network connection.", 0x00A97980) would stop the main thread for
// good: log it and answer the way that declines whatever it asks.
int Answer(UINT type) {
  switch (type & 0xF) {
    case MB_OK: return IDOK;
    case MB_ABORTRETRYIGNORE: return IDIGNORE;
    case MB_YESNO: return IDNO;
    case MB_CANCELTRYCONTINUE: return IDCONTINUE;
    default: return IDCANCEL;  // OKCANCEL, YESNOCANCEL, RETRYCANCEL
  }
}
int WINAPI MessageBoxWHook(HWND, LPCWSTR text, LPCWSTR caption, UINT type) {
  InterlockedIncrement(&g_messageBoxes);
  Log("host: MessageBoxW not shown (type %X, answered %d): [%ls] %ls (returning to %08lX)", type, Answer(type), caption ? caption : L"",
      text ? text : L"", Caller(_ReturnAddress()));
  return Answer(type);
}
int WINAPI MessageBoxAHook(HWND, LPCSTR text, LPCSTR caption, UINT type) {
  InterlockedIncrement(&g_messageBoxes);
  Log("host: MessageBoxA not shown (type %X, answered %d): [%s] %s (returning to %08lX)", type, Answer(type), caption ? caption : "",
      text ? text : "", Caller(_ReturnAddress()));
  return Answer(type);
}

// ---------------------------------------------------------------------------------------------
// folders: the per-user CSIDLs go below the data folder; everything else (fonts, windows, ...) stays as it is.
using SHGetFolderPathWFn = HRESULT(WINAPI*)(HWND, int, HANDLE, DWORD, LPWSTR);
SHGetFolderPathWFn g_getFolder;
wchar_t g_dataDirW[MAX_PATH];

const wchar_t* RedirectedName(int csidl) {
  switch (csidl & 0xFF) {
    case 0x05: return L"Documents";      // CSIDL_PERSONAL (Documents\Dawngate\portraits.snap)
    case 0x1A: return L"AppData";        // CSIDL_APPDATA (DawngateData: Application.log, Preferences, Debug)
    case 0x1C: return L"LocalAppData";   // CSIDL_LOCAL_APPDATA
    case 0x23: return L"CommonAppData";  // CSIDL_COMMON_APPDATA
    case 0x27: return L"Pictures";       // CSIDL_MYPICTURES
    default: return nullptr;
  }
}

HRESULT WINAPI SHGetFolderPathWHook(HWND h, int csidl, HANDLE token, DWORD flags, LPWSTR path) {
  const wchar_t* sub = RedirectedName(csidl);
  if (!sub || !path) {
    HRESULT r = g_getFolder(h, csidl, token, flags, path);
    if (InterlockedIncrement(&g_redirects) <= 20)
      Log("host: SHGetFolderPathW(csidl %02X) not redirected: %ls", csidl, (SUCCEEDED(r) && path) ? path : L"(failed)");
    return r;
  }
  _snwprintf_s(path, MAX_PATH, _TRUNCATE, L"%ls\\%ls", g_dataDirW, sub);
  CreateDirectoryW(path, nullptr);
  if (InterlockedIncrement(&g_redirects) <= 20) Log("host: SHGetFolderPathW(csidl %02X) -> %ls", csidl, path);
  return S_OK;
}

// ---------------------------------------------------------------------------------------------
// times and resources

unsigned long long FileTimeMs(const FILETIME& f) {
  return ((static_cast<unsigned long long>(f.dwHighDateTime) << 32) | f.dwLowDateTime) / 10000ull;
}

unsigned MsSinceProcessStart() {
  FILETIME created, exited, kernel, user, now;
  if (!GetProcessTimes(GetCurrentProcess(), &created, &exited, &kernel, &user)) return 0;
  GetSystemTimeAsFileTime(&now);
  return static_cast<unsigned>(FileTimeMs(now) - FileTimeMs(created));
}

template <typename T>
bool Hook(const char* dll, const char* func, T replacement, T* orig, unsigned ordinal = 0) {
  void* prev = HookIat(dll, func, reinterpret_cast<void*>(replacement), ordinal);
  if (orig) *orig = reinterpret_cast<T>(prev);
  if (!prev) Log("host: hook %s!%s FAILED (import not found)", dll, func);
  return prev != nullptr;
}

}  // namespace

const HostConfig& HostGet() { return g_host; }

const HostConfig& HostDetect(const char* gameDir) {
  Env("DGGMS_MATCH", g_host.match, sizeof g_host.match);
  Env("DGGMS_CONTROL", g_host.control, sizeof g_host.control);
  Env("DGGMS_TOKEN", g_host.token, sizeof g_host.token);
  Env("DGGMS_BIND", g_host.bind, sizeof g_host.bind);
  g_host.enabled = g_host.match[0] || g_host.control[0] || g_host.token[0] || g_host.bind[0];
  if (!g_host.enabled) return g_host;
  Env("DGGMS_OPTIONS", g_host.options, sizeof g_host.options);
  Env("DGGMS_DATA", g_host.dataDir, sizeof g_host.dataDir);
  if (!g_host.dataDir[0]) {
    char name[64];
    std::snprintf(name, sizeof name, "%s", g_host.match[0] ? g_host.match : "default");
    for (char* p = name; *p; ++p)
      if (!((*p >= 'a' && *p <= 'z') || (*p >= 'A' && *p <= 'Z') || (*p >= '0' && *p <= '9') || *p == '-' || *p == '_' || *p == '.'))
        *p = '_';
    std::snprintf(g_host.dataDir, sizeof g_host.dataDir, "%s\\host\\%s", gameDir, name);
  }
  MakeDirs(g_host.dataDir);
  MultiByteToWideChar(CP_ACP, 0, g_host.dataDir, -1, g_dataDirW, MAX_PATH);
  return g_host;
}

namespace {

// DGGMS_OPTIONS lookup: "key=value;key=value" (blanks around keys and values ignored). True if `key` is there.
bool FindOption(const char* key, char* out, std::size_t size) {
  const std::size_t klen = std::strlen(key);
  for (const char* p = g_host.options; *p;) {
    while (*p == ' ' || *p == ';') ++p;
    const char* end = std::strchr(p, ';');
    if (!end) end = p + std::strlen(p);
    const char* eq = static_cast<const char*>(std::memchr(p, '=', static_cast<std::size_t>(end - p)));
    if (eq) {
      const char* ke = eq;
      while (ke > p && ke[-1] == ' ') --ke;
      if (static_cast<std::size_t>(ke - p) == klen && _strnicmp(p, key, klen) == 0) {
        const char* v = eq + 1;
        while (v < end && *v == ' ') ++v;
        const char* ve = end;
        while (ve > v && ve[-1] == ' ') --ve;
        std::snprintf(out, size, "%.*s", static_cast<int>(ve - v), v);
        return true;
      }
    }
    p = end;
  }
  return false;
}

char g_iniPath[MAX_PATH];

}  // namespace

int HostOptionInt(const char* key, int def) {
  char v[64];
  if (FindOption(key, v, sizeof v)) return static_cast<int>(std::strtol(v, nullptr, 0));
  return g_iniPath[0] ? static_cast<int>(GetPrivateProfileIntA("host", key, def, g_iniPath)) : def;
}

void HostOptionString(const char* key, const char* def, char* out, std::size_t size) {
  if (FindOption(key, out, size)) return;
  if (g_iniPath[0])
    GetPrivateProfileStringA("host", key, def, out, static_cast<DWORD>(size), g_iniPath);
  else
    std::snprintf(out, size, "%s", def);
}

void HostInstall(const char* ini) {
  if (!g_host.enabled) return;
  std::snprintf(g_iniPath, sizeof g_iniPath, "%s", ini);
  Log("host mode (ADR-2 stage 1): match '%s', control '%s', bind '%s', token %s, data %s, options '%s'", g_host.match,
      g_host.control, g_host.bind, g_host.token[0] ? "set" : "none", g_host.dataDir, g_host.options);
  g_holdBoot = HostOptionInt("hold_boot", 1) != 0;
  g_blockSockets = HostOptionInt("block_sockets", 1) != 0;
  g_hideWindow = HostOptionInt("hide_window", 1) != 0;
  g_redirectFolders = HostOptionInt("redirect_folders", 1) != 0;
  Log("host: hold_boot %d, block_sockets %d, hide_window %d, redirect_folders %d", g_holdBoot, g_blockSockets, g_hideWindow,
      g_redirectFolders);

  if (g_holdBoot)
    Log("host: boot hold %s", PatchPointer(kBootStateUpdateSlot, kBootStateUpdate, reinterpret_cast<void*>(&BootUpdateHold))
                                  ? "installed (cBootState vtable slot 10)" : "FAILED");
  if (g_blockSockets) {
    bool ok = Hook("WSOCK32.dll", "socket", &SocketHook, static_cast<decltype(&SocketHook)*>(nullptr), 23);
    ok &= Hook("WSOCK32.dll", "gethostbyname", &GetHostByNameHook, static_cast<decltype(&GetHostByNameHook)*>(nullptr), 52);
    ok &= Hook("WS2_32.dll", "WSASocketA", &WsaSocketHook, static_cast<decltype(&WsaSocketHook)*>(nullptr));
    Log("host: socket guard %s (socket, WSASocketA and gethostbyname of Dawngate.exe fail)", ok ? "installed" : "INCOMPLETE");
  }
  if (g_hideWindow) {
    bool ok = Hook("USER32.dll", "CreateWindowExW", &CreateWindowExWHook, &g_createWindowW);
    ok &= Hook("USER32.dll", "CreateWindowExA", &CreateWindowExAHook, &g_createWindowA);
    ok &= Hook("USER32.dll", "ShowWindow", &ShowWindowHook, &g_showWindow);
    ok &= Hook("USER32.dll", "SetWindowPos", &SetWindowPosHook, &g_setWindowPos);
    ok &= Hook("USER32.dll", "SetForegroundWindow", &SetForegroundWindowHook, static_cast<decltype(&SetForegroundWindowHook)*>(nullptr));
    ok &= Hook("USER32.dll", "SetActiveWindow", &SetActiveWindowHook, static_cast<decltype(&SetActiveWindowHook)*>(nullptr));
    ok &= Hook("USER32.dll", "SetWindowsHookExA", &SetWindowsHookExAHook, &g_setHook);
    ok &= Hook("USER32.dll", "MessageBoxW", &MessageBoxWHook, static_cast<decltype(&MessageBoxWHook)*>(nullptr));
    ok &= Hook("USER32.dll", "MessageBoxA", &MessageBoxAHook, static_cast<decltype(&MessageBoxAHook)*>(nullptr));
    Log("host: window hooks %s (hidden top-level windows, no activation, no low-level input hooks, no message boxes)",
        ok ? "installed" : "INCOMPLETE");
  }
  if (g_redirectFolders) {
    bool ok = Hook("SHELL32.dll", "SHGetFolderPathW", &SHGetFolderPathWHook, &g_getFolder);
    Log("host: folder redirect %s (per-user folders -> %s)", ok ? "installed" : "FAILED", g_host.dataDir);
  }
  HeadlessInstall();  // H1-live: stage 2's start-up changes (headless.h), each behind its [host] key
  HostMark("hooks installed");
}

void HostFormatStats(char* buf, std::size_t size) {
  PROCESS_MEMORY_COUNTERS_EX pmc = {};
  pmc.cb = sizeof pmc;
  GetProcessMemoryInfo(GetCurrentProcess(), reinterpret_cast<PROCESS_MEMORY_COUNTERS*>(&pmc), sizeof pmc);
  MEMORYSTATUSEX ms = {};
  ms.dwLength = sizeof ms;
  GlobalMemoryStatusEx(&ms);
  FILETIME created, exited, kernel, user;
  GetProcessTimes(GetCurrentProcess(), &created, &exited, &kernel, &user);
  const unsigned mb = 1024 * 1024;
  std::snprintf(buf, size,
                "private %u MB, working set %u MB (peak %u MB), address space used %u MB, cpu %.1f s (user %.1f), "
                "refused sockets %ld, dns %ld, boot holds %ld, hidden windows %ld, shows refused %ld, message boxes %ld",
                static_cast<unsigned>(pmc.PrivateUsage / mb), static_cast<unsigned>(pmc.WorkingSetSize / mb),
                static_cast<unsigned>(pmc.PeakWorkingSetSize / mb),
                static_cast<unsigned>((ms.ullTotalVirtual - ms.ullAvailVirtual) / mb),
                (FileTimeMs(kernel) + FileTimeMs(user)) / 1000.0, FileTimeMs(user) / 1000.0, g_refusedSockets, g_refusedDns,
                g_bootHolds, g_hiddenWindows, g_refusedShows, g_messageBoxes);
}

void HostMark(const char* what) {
  if (!g_host.enabled) return;
  char s[400];
  HostFormatStats(s, sizeof s);
  Log("host timing: %s at %u ms after process start; %s", what, MsSinceProcessStart(), s);
}
