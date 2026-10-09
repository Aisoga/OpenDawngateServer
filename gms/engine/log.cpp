#include "log.h"

#include <windows.h>

#include <cstdarg>
#include <cstdio>
#include <cstring>

static FILE* g_log;
static DWORD g_t0;
static CRITICAL_SECTION g_cs;

// H5: the last kLogTailLines lines (as written, without the newline), a ring under g_cs
constexpr std::size_t kTailWidth = 320;
static char g_tail[kLogTailLines][kTailWidth];
static int g_tailNext, g_tailCount;

void LogOpen(const char* path) {
  InitializeCriticalSection(&g_cs);
  g_t0 = GetTickCount();
  g_log = std::fopen(path, "w");
}

unsigned LogMs() { return GetTickCount() - g_t0; }

static bool Enter(unsigned timeoutMs) {
  if (timeoutMs == INFINITE) {
    EnterCriticalSection(&g_cs);
    return true;
  }
  for (DWORD start = GetTickCount();;) {
    if (TryEnterCriticalSection(&g_cs)) return true;
    if (GetTickCount() - start >= timeoutMs) return false;
    Sleep(5);
  }
}

static bool Write(unsigned timeoutMs, const char* fmt, va_list ap) {
  if (!g_log) return false;
  char buf[2048];
  _vsnprintf_s(buf, sizeof buf, _TRUNCATE, fmt, ap);
  char line[2100];
  std::snprintf(line, sizeof line, "%8u [%5lu] %s", LogMs(), GetCurrentThreadId(), buf);
  if (!Enter(timeoutMs)) return false;
  std::fprintf(g_log, "%s\n", line);
  std::fflush(g_log);
  std::snprintf(g_tail[g_tailNext], kTailWidth, "%s", line);
  g_tailNext = (g_tailNext + 1) % kLogTailLines;
  if (g_tailCount < kLogTailLines) ++g_tailCount;
  LeaveCriticalSection(&g_cs);
  return true;
}

void Log(const char* fmt, ...) {
  va_list ap;
  va_start(ap, fmt);
  Write(INFINITE, fmt, ap);
  va_end(ap);
}

bool LogTimed(unsigned timeoutMs, const char* fmt, ...) {
  va_list ap;
  va_start(ap, fmt);
  const bool ok = Write(timeoutMs, fmt, ap);
  va_end(ap);
  return ok;
}

bool LogTail(char* out, std::size_t size, int lines, unsigned timeoutMs) {
  if (!size) return false;
  out[0] = 0;
  if (!g_log || !Enter(timeoutMs)) return false;
  if (lines > g_tailCount) lines = g_tailCount;
  std::size_t len = 0;
  for (int i = 0; i < lines && len + 1 < size; ++i) {
    const char* l = g_tail[(g_tailNext - lines + i + kLogTailLines) % kLogTailLines];
    const int n = std::snprintf(out + len, size - len, "%s\n", l);
    if (n < 0) break;
    len += static_cast<std::size_t>(n);
    if (len >= size) {
      len = size - 1;
      break;
    }
  }
  LeaveCriticalSection(&g_cs);
  return true;
}
