// Plain-text log for the harness: <game dir>\dgsim.log, one line per call, flushed at once so a
// crash loses nothing. Timestamps are milliseconds since the DLL was loaded.
// H5: the last kLogTailLines lines are also kept in memory (LogTail), so the host's watchdog can put them beside its
// stall dump; LogTimed is Log for that thread, which must not wait for ever on a log lock the stalled thread holds.
#pragma once

#include <cstddef>

constexpr int kLogTailLines = 64;

void LogOpen(const char* path);
void Log(const char* fmt, ...);
unsigned LogMs();
// Log, but gives up after timeoutMs when another thread holds the log (false then: the line is lost).
bool LogTimed(unsigned timeoutMs, const char* fmt, ...);
// The last `lines` log lines (at most kLogTailLines), oldest first, each ending in '\n', into out (truncated to size).
// False when the log lock wasn't free within timeoutMs.
bool LogTail(char* out, std::size_t size, int lines, unsigned timeoutMs);
