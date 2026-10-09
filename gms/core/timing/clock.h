// The one clock of the Q3c timing (gms/core/timing): milliseconds on std::chrono::steady_clock (QueryPerformanceCounter
// on Windows), as a double. Header-only, so the RakNet endpoint (gms_core) and the host's timing (dgsim) stamp with the
// same clock without linking each other.
#pragma once

#include <chrono>

namespace gms {
namespace timing {

inline double NowMs() {
  return std::chrono::duration<double, std::milli>(std::chrono::steady_clock::now().time_since_epoch()).count();
}

}  // namespace timing
}  // namespace gms
