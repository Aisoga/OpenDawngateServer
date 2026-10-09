// One poll of the GMS endpoint into the session (gms/core, G1): ADR-2's loop step 1 for the network. gms_script calls
// it every 5 ms; the host (G2) once per engine tick, followed by GmsSession::Tick().
#pragma once

#include <vector>

#include "core/net/endpoint.h"
#include "core/session/gms_session.h"

namespace gms {

// Q3c: what a pump measured, appended to (the host collects a minute of them; docs/SERVER_MODE.md *Timing and desync
// (Q3c)*). Times in ms (gms::timing::NowMs()).
struct PumpTiming {
  std::vector<double> ageMs;          // each GMS message: its arrival in RakNet -> this poll (the wait for the engine)
  std::vector<double> clockAnswerMs;  // each ClockSync: its arrival -> its answer handed to the endpoint
  int unknownArrival = 0;             // GMS messages without an arrival time (split by RakNet)
};

// Drains every pending packet: GMS messages (first byte >= 0x81) and connection events go to the session; every
// other RakNet packet, and the connection events too, are logged with a hex dump as raknet_probe --listen prints
// them ("HH:MM:SS.mmm   t.ttt 13 ID_NEW_INCOMING_CONNECTION (94 B) from 127.0.0.1|64433, guid ...", t = seconds
// since `start`), which run_session.ps1's summary counts. Returns the number of packets handled. With `timing`, the
// Q3c measurements are appended to it.
int PumpEndpoint(Endpoint& ep, GmsSession& session, SteadyClock::time_point start, PumpTiming* timing = nullptr);

// Q3c: while the session handles a GMS message from PumpEndpoint, the message's arrival in RakNet (timing::NowMs; the
// poll time when RakNet's plugin had none); -1 outside. The host's command intake reads it.
double CurrentArrivalMs();

}  // namespace gms
