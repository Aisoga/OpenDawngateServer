// Log output of gms/core (G1). Every line goes to one sink: stdout by default (gms_script, whose output
// run_session.ps1 keeps as gms_script.out and summarizes), the host's log file in G2. The lines keep the format of
// raknet_probe's session driver (D2b-live), which run_session.ps1's 'gms probe' summary line parses.
#pragma once

#include <string>

namespace gms {

// Receives one complete line without its newline.
using LogSink = void (*)(const std::string& line);

// nullptr restores the default (stdout, written at once).
void SetLogSink(LogSink sink);

// printf-style; one call = one line (no trailing newline in `fmt`).
void Logf(const char* fmt, ...);

// Local wall-clock time "HH:MM:SS.mmm", to line the GMS output up with the client's Application.log.
std::string Clock();

}  // namespace gms
