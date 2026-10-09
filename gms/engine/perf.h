// The match host's timing over a long match (roadmap Q3c; docs/SERVER_MODE.md *Timing and desync (Q3c)*): the
// engine-side glue of gms/core/timing. Per window (a match minute, at most [host] timing_window_s) it writes into
// dgsim.log
//   timing: ...           each server tick's duration, lag and interval (percentiles, late ticks, spikes, the worst tick
//                         and its slowest step, every guarded step's mean and maximum)
//   traffic: ...          messages and bytes per tick by kind (creates, deltas, forwarded, players, fog, answers,
//                         session, other) and the busiest tick
//   commands latency: ... each command kind's arrival -> intake, intake -> answer, intake -> effect
//   net: ...              the GMS endpoint's side (gms_host.cpp's source): ClockSync answer delays, the wait for the
//                         poll, Send -> wire of the answers, RakNet's queue, resends, ping and buffers, the simulator
//   records: ...          the gauges (objects, pools, the object id counter, the replication's registries)
// and one row of host_timing.csv in the host's data folder (beside dgsim.log; gms::timing::CsvHeader's columns).
// [host] keys: timing (1; 0 = none of it), timing_csv (1), timing_window_s (60).
// Engine thread only; the sources are called with the server simulator bound (inside ServerTick).
#pragma once

#include <cstddef>
#include <cstdint>
#include <string>

#include "core/timing/timing.h"

namespace perf {

// Reads the [host] keys (host mode) or keeps the defaults (on, harness: no CSV). Once, before the ticks.
void Configure(bool hostMode, double tickMs, const char* dataDir);
bool Enabled();

// A server tick: TickBegin before the core-only engine frame (lagMs = how late against the schedule), EngineFrameDone
// after it, TickEnd at the end of ServerTick (rolls the window when the match minute changed).
void TickBegin(double lagMs);
void EngineFrameDone();
void TickEnd();
// guards::Run: a step's time in this tick.
void StepTime(int step, double ms);

// Traffic: the kind of what is sent next (gms::timing::TrafficKind; ServerMatch sets it around its blocks), and each
// message handed to the endpoint, with the gameplay index of the player it goes to (-1 unknown): counted, and the
// command answers among them matched to the pending commands (B4, 9B, C3, AA).
void SetKind(int kind);
int Kind();
void OnSend(int toPlayer, const std::uint8_t* msg, std::size_t n);

// The command latency (commands.cpp: intakes and the moves' effects).
gms::timing::CommandLatency& Commands();

// Sources asked at each roll: append their gauges (always the same names, -1 when unknown) and optionally a log line.
using GaugeSource = void (*)(gms::timing::Gauges* g, std::string* line, int minute);
void AddSource(GaugeSource fn);

}  // namespace perf
