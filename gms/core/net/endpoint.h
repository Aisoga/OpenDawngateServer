// The GMS's RakNet endpoint (gms/core, G1; ADR-2 decision 4): RakNet 4.081 built with RAKNET_PROTOCOL_VERSION 5
// (gms/cmake/raknet.cmake), which completes the live client's handshake (D2a, B9). D2a's settings: no security, no
// password, sliding-window congestion control, 10 internal ids, 64-bit time, IPv4. GMS messages are raw RakNet user
// packets whose first byte is the GMS wire byte (>= 0x81, protocol/wire.h), sent HIGH_PRIORITY, RELIABLE_ORDERED on
// channel 0. RakNet's I/O thread is internal; Poll() never blocks, so the host can call it from its engine tick.
// No RakNet header leaks out of this file: the session, script and host only see bytes and connection ids.
//
// Q3c (docs/SERVER_MODE.md *Timing and desync (Q3c)*) adds, all off or passive by default:
//   - arrival times: every GMS message gets the time RakNet's update thread took it off the wire (Event::arrivedMs,
//     gms::timing::NowMs()), from a RakNet plugin (OnInternalPacket), so the host can tell the wait for its poll;
//   - send timing: per message the time in RakNet's send queue (creation -> first transmission), the resends, and for
//     the wire bytes given to SetTrackedSends the whole time from Send() to the first transmission (TakeStats);
//   - the network simulator (netsim.h): SetNetSim's delay, jitter and loss on every datagram in and out;
//   - a fast lane (SetFastLane): the given wire bytes go IMMEDIATE_PRIORITY on ordering channel 1, so they neither
//     wait behind the HIGH_PRIORITY queue nor behind a lost datagram of channel 0.
//
// The network limits (limits.h), off unless SetLimits turns them on:
// every datagram passes the NetLimiter on RakNet's receive thread before RakNet parses it (dropped over its address's
// or IP's rate; offline datagrams over the IP's handshake rate), every GMS message, new connection and silent
// connection in Poll; a connection over its limits is closed ("drop, then kick") and Poll reports it as
// kDisconnected (data ID_CONNECTION_LOST), so the session sees a lost connection. RakNet's own
// SetLimitIPConnectionFrequency (one connection per IP per 100 ms, loopback exempt) is on with them, unless the limits
// name shared IPs (the tunnel gateway's).
//
// RakNet's hardening (gms/cmake/raknet_patches/RakPeer.*.patch), on by default
// (SetHardening; independent of the limits, since none of it costs a normal client anything): a cookie in
// ID_OPEN_CONNECTION_REPLY_1 that ID_OPEN_CONNECTION_REQUEST_2 must echo (a spoofed source gets no connection slot and
// no reliable, resent reply), no answer to a REQUEST_1 shorter than its reply, no IP ban of an unverified sender's
// nonsense (only its connection is closed: players behind one NAT or the tunnel gateway share an IP), and at most
// 1,024 messages / 1 MB per connection waiting ahead of a hole in an ordered channel (beyond: closed, ID_CONNECTION_LOST).
#pragma once

#include <cstdint>
#include <memory>
#include <string>
#include <vector>

#include "core/common/bytes.h"
#include "core/net/limits.h"
#include "core/net/netsim.h"

namespace gms {

using ConnId = uint64_t;  // a connection's RakNet GUID

// The session's way out (implemented by Endpoint; tests may use a fake).
class ITransport {
 public:
  virtual bool Send(ConnId conn, const Bytes& msg) = 0;
  // closes the connection (the strict check's kick); the default does nothing
  virtual void Close(ConnId conn) { (void)conn; }

 protected:
  ~ITransport() = default;
};

// RakNet's name for an internal message id (ID_NEW_INCOMING_CONNECTION, ...), "?" for others.
const char* RakMessageName(uint8_t id);

// RakNet's handshake and receive hardening (the RakPeer of Endpoint::Start).
struct RakHardeningOptions {
  bool cookies = true;                   // REPLY_1's cookie, checked in REQUEST_2
  bool nonsenseBan = false;              // stock RakNet: true (the sender's IP banned for the connection's timeout)
  bool request1NoSmaller = true;         // no REPLY_1 (or incompatible-version reply) to a shorter REQUEST_1
  unsigned orderedAheadMessages = 1024;  // per connection; 0 = no limit (stock)
  unsigned orderedAheadKb = 1024;        // per connection; 0 = no limit (stock)
  std::string Describe() const;
};
// What it did since the last TakeHardeningTotals.
struct RakHardeningTotals {
  uint64_t cookiesIssued = 0, request1Short = 0, request2BadCookie = 0, nonsenseClosed = 0, nonsenseBanned = 0,
           orderedAheadClosed = 0;
  bool Any() const {
    return request1Short || request2BadCookie || nonsenseClosed || nonsenseBanned || orderedAheadClosed;
  }
  std::string Describe() const;
};

class Endpoint final : public ITransport {
 public:
  struct Event {
    enum Kind {
      kConnected,     // ID_NEW_INCOMING_CONNECTION
      kDisconnected,  // ID_DISCONNECTION_NOTIFICATION or ID_CONNECTION_LOST
      kGms,           // a user packet whose first byte is >= 0x81
      kOther,         // any other RakNet packet
    } kind = kOther;
    ConnId conn = 0;
    std::string addr;  // "127.0.0.1|64433", as RakNet prints a SystemAddress
    std::string guid;  // the GUID as RakNet prints it
    Bytes data;        // the whole packet, id or wire byte first
    double arrivedMs = -1;  // Q3c: when RakNet's update thread took it in (timing::NowMs); -1 = unknown (split)
    double polledMs = -1;   // Q3c: when Poll handed it out
  };

  // Q3c: what the endpoint measured since the last TakeStats.
  struct Stats {
    std::vector<double> trackedWireMs;  // tracked wire bytes: Send() -> first transmission (ms)
    std::vector<uint8_t> trackedWire;   // ... and their wire byte (same order)
    std::vector<double> queueMs;        // every other GMS message: RakNet's send queue, creation -> first transmission
    uint64_t gmsSent = 0, gmsBytes = 0; // GMS messages first transmitted, their bytes
    uint64_t resends = 0;               // GMS message transmissions after the first (a datagram was lost)
    uint64_t fastLane = 0;              // messages sent on the fast lane
    // per connection, the maxima of SampleConnections() since the last TakeStats (all connections together)
    int pingMax = 0, pingLast = 0;      // RakNet's average ping (ms)
    unsigned sendBufferMsgsMax = 0;     // messages waiting in RakNet's send buffers (all priorities)
    double sendBufferBytesMax = 0;
    unsigned resendBufferMsgsMax = 0;   // messages sent and not yet acknowledged
    float packetLossMax = 0;            // RakNet's packetlossLastSecond (0..1)
    bool limitedByCongestion = false;   // RakNet's isLimitedByCongestionControl at some sample
    // the network simulator (when on): totals since Start (datagrams handed in, lost, the delay added)
    uint64_t simOut = 0, simOutDropped = 0, simIn = 0, simInDropped = 0;
    double simOutAddedMeanMs = 0, simOutAddedMaxMs = 0, simInAddedMeanMs = 0, simInAddedMaxMs = 0;
    size_t simQueueMax = 0;             // datagrams waiting in either direction, maximum
  };

  Endpoint();
  ~Endpoint();
  Endpoint(const Endpoint&) = delete;
  Endpoint& operator=(const Endpoint&) = delete;

  // Binds `bindIp`:`port` (0 = ephemeral) and accepts up to `maxConnections`. False with `error` set on failure.
  bool Start(const std::string& bindIp, uint16_t port, unsigned maxConnections, std::string* error);
  void Stop();
  bool Started() const;

  uint16_t BoundPort() const;
  std::string GuidText() const;
  static const char* RakNetVersion();  // "4.081"
  static int ProtocolVersion();        // 5

  // The next packet, if any. Never blocks.
  bool Poll(Event* e);
  bool Send(ConnId conn, const Bytes& msg) override;
  // CloseConnection with a disconnection notification to the client
  void Close(ConnId conn) override;

  // Q3c. Before Start (the simulator needs the socket Start makes; set later it applies to the next Start).
  void SetNetSim(const NetSimOptions& o);
  const NetSimOptions& NetSim() const;
  // The wire bytes whose Send() -> first transmission is measured one by one (e.g. C7, B4, C3).
  void SetTrackedSends(const std::vector<uint8_t>& wireBytes);
  // The wire bytes sent IMMEDIATE_PRIORITY on ordering channel 1 instead of HIGH_PRIORITY on channel 0 (empty = off).
  void SetFastLane(const std::vector<uint8_t>& wireBytes);
  // Samples RakNet's statistics of every connection (ping, send and resend buffers, loss) into the maxima.
  void SampleConnections();
  Stats TakeStats();

  // the network limits (limits.h), before Start (they apply from the next Start on).
  void SetLimits(const NetLimitOptions& o);
  const NetLimitOptions& Limits() const;
  // What the limits did since the last call (all zero while off).
  NetLimiter::Totals TakeLimitTotals();

  // RakNet's hardening, before Start (applies from the next Start on); HardeningAvailable() is false in a build
  // with -DDG_RAKNET_PATCHES=OFF (stock RakNet: the options are ignored).
  void SetHardening(const RakHardeningOptions& o);
  const RakHardeningOptions& Hardening() const;
  static bool HardeningAvailable();
  RakHardeningTotals TakeHardeningTotals();

 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};

}  // namespace gms
