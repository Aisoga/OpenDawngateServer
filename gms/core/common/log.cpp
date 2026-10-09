#include "core/common/log.h"

#include <chrono>
#include <cstdarg>
#include <cstdio>
#include <ctime>
#include <mutex>

namespace gms {
namespace {

void StdoutSink(const std::string& line) {
  std::string l = line;
  l += '\n';
  std::fwrite(l.data(), 1, l.size(), stdout);
  std::fflush(stdout);
}

LogSink g_sink = StdoutSink;
std::mutex g_mutex;

}  // namespace

void SetLogSink(LogSink sink) {
  std::lock_guard<std::mutex> lock(g_mutex);
  g_sink = sink ? sink : StdoutSink;
}

void Logf(const char* fmt, ...) {
  char buf[1024];
  va_list ap;
  va_start(ap, fmt);
  const int n = std::vsnprintf(buf, sizeof buf, fmt, ap);
  va_end(ap);
  std::string line;
  if (n < 0) {
    line = "(log format error)";
  } else if (static_cast<size_t>(n) < sizeof buf) {
    line.assign(buf, static_cast<size_t>(n));
  } else {
    line.resize(static_cast<size_t>(n) + 1);
    va_start(ap, fmt);
    std::vsnprintf(&line[0], line.size(), fmt, ap);
    va_end(ap);
    line.resize(static_cast<size_t>(n));
  }
  std::lock_guard<std::mutex> lock(g_mutex);
  g_sink(line);
}

std::string Clock() {
  const auto now = std::chrono::system_clock::now();
  const std::time_t t = std::chrono::system_clock::to_time_t(now);
  const auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(now.time_since_epoch()).count() % 1000;
  std::tm tm{};
#ifdef _WIN32
  localtime_s(&tm, &t);
#else
  localtime_r(&t, &tm);
#endif
  char buf[16];
  std::snprintf(buf, sizeof buf, "%02d:%02d:%02d.%03d", tm.tm_hour, tm.tm_min, tm.tm_sec, static_cast<int>(ms));
  return buf;
}

}  // namespace gms
