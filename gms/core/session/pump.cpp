#include "core/session/pump.h"

#include "core/common/log.h"
#include "core/protocol/wire.h"
#include "core/timing/clock.h"

namespace gms {

namespace {
double g_currentArrival = -1;  // the engine thread's message in hand (CurrentArrivalMs)
}  // namespace

double CurrentArrivalMs() { return g_currentArrival; }

int PumpEndpoint(Endpoint& ep, GmsSession& session, SteadyClock::time_point start, PumpTiming* timing) {
  int handled = 0;
  Endpoint::Event e;
  while (ep.Poll(&e)) {
    ++handled;
    if (e.kind == Endpoint::Event::kGms) {  // the session logs it (decoded, repeats counted)
      const double arrived = e.arrivedMs >= 0 ? e.arrivedMs : e.polledMs;
      if (timing) {
        if (e.arrivedMs >= 0) timing->ageMs.push_back(e.polledMs - e.arrivedMs);
        else ++timing->unknownArrival;
      }
      g_currentArrival = arrived;
      session.OnMessage(e.conn, e.data.data(), e.data.size());
      g_currentArrival = -1;
      // the session answers a ClockSync at once (GmsSession::OnMessage): its answer's delay in the host
      if (timing && !e.data.empty() && e.data[0] == kGmsClockSync && arrived >= 0)
        timing->clockAnswerMs.push_back(timing::NowMs() - arrived);
      continue;
    }
    const double t = std::chrono::duration<double>(SteadyClock::now() - start).count();
    const uint8_t id = e.data.empty() ? 0 : e.data[0];
    // any other RakNet packet (a connected client's id below 0x81, an offline ID_ADVERTISE_SYSTEM, ...) goes to
    // the strict check and is logged only within its log limit
    if (e.kind == Endpoint::Event::kOther && !session.OnOtherPacket(e.conn, e.data.data(), e.data.size())) continue;
    Logf("%s %8.3f %02x %s (%zu B) from %s, guid %s", Clock().c_str(), t, id, RakMessageName(id), e.data.size(),
         e.addr.c_str(), e.guid.c_str());
    for (size_t off = 0; off < e.data.size(); off += 16)
      Logf("    %04zx  %s", off, Hex(e.data.data() + off, e.data.size() - off < 16 ? e.data.size() - off : 16, 16).c_str());
    if (e.kind == Endpoint::Event::kConnected) session.OnConnected(e.conn, e.addr);
    if (e.kind == Endpoint::Event::kDisconnected) session.OnDisconnected(e.conn);
  }
  return handled;
}

}  // namespace gms
