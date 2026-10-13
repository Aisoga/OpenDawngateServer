// The match host's network limits:
// what one remote address or connection may send the GMS endpoint before it is dropped, then kicked. Engine- and
// RakNet-free (gms_tests checks it with made-up clocks); the endpoint (endpoint.cpp) asks it about every datagram on
// RakNet's receive thread (SetIncomingDatagramEventHandler, before RakNet parses it) and about every GMS message, new
// connection and silent connection on the engine thread (Poll), under its own mutex.
//
//   - datagrams and bytes per second per remote address (ip:port), and per IP (all its ports, times ipFactor:
//     several players behind one NAT); over the rate a datagram is dropped before RakNet sees it;
//   - offline datagrams per IP (RakNet's open-connection requests and unconnected pings, which carry no "valid"
//     bit 0x80): a handshake flood, and RakNet's own SetLimitIPConnectionFrequency, which exempts loopback;
//   - GMS messages per second per connection (after RakNet's reassembly): over the rate a message is dropped;
//   - drop, then kick: a connected address (or connection) that had drops in kickAfterS of the last kWindowS seconds
//     is closed, and its datagrams dropped for blockS;
//   - connections per IP (a new one beyond the cap is closed at once) and a hello timeout (a connection that sent no
//     GMS message within helloTimeoutS is closed: half-open slots don't stay taken).
// Shared IPs (sharedIps: the tunnel gateway's, from which every tunnelled player comes, a port each) have no per-IP
// rule: their ports count as separate clients (offline datagrams per address instead of per IP, no connection cap);
// the gateway limits players by their real address before it forwards.
// All off unless `on` ([host] net_limits: -1 auto = on for a public bind address, 1 on, 0 off).
#pragma once

#include <cstddef>
#include <cstdint>
#include <iterator>
#include <unordered_map>
#include <vector>

namespace gms {

struct NetLimitOptions {
  bool on = false;
  double datagramsPerSec = 400, datagramBurst = 1200;          // per remote address (ip:port)
  double bytesPerSec = 128 * 1024, byteBurst = 512 * 1024;     // per remote address
  double ipFactor = 6;                                         // per IP: the per-address rates times this
  double offlinePerSec = 4, offlineBurst = 16;                 // offline datagrams per IP
  double messagesPerSec = 200, messageBurst = 600;             // GMS messages per connection
  int kickAfterS = 3;       // seconds with drops (of the last kWindowS) that kick a connected address or connection
  int connectionsPerIp = 8; // open connections per IP
  int helloTimeoutS = 30;   // a connection without any GMS message this long is closed (0 = never)
  int blockS = 30;          // a kicked address's datagrams are dropped this long
  std::size_t maxTracked = 8192;  // addresses tracked at once (idle ones are forgotten first)
  std::vector<uint32_t> sharedIps;  // IPs (host order) without per-IP rules: the tunnel gateway's
};

// Whether an IPv4 address (host order, a << 24 | b << 16 | c << 8 | d) is public: not loopback, private, link-local,
// shared (100.64/10), this-network, multicast, reserved or a documentation range (net_limits -1's rule, as dgserver's
// --limits auto and exepatch.IsPublic).
inline bool IsPublicIpv4(uint32_t ip) {
  const uint32_t a = ip >> 24, b = (ip >> 16) & 0xFF, c = (ip >> 8) & 0xFF;
  if (a == 0 || a == 10 || a == 127 || a >= 224) return false;
  if (a == 100 && (b & 0xC0) == 64) return false;
  if (a == 169 && b == 254) return false;
  if (a == 172 && (b & 0xF0) == 16) return false;
  if (a == 192 && b == 168) return false;
  if (a == 192 && b == 0 && (c == 0 || c == 2)) return false;
  if (a == 198 && (b == 18 || b == 19)) return false;
  if ((a == 198 && b == 51 && c == 100) || (a == 203 && b == 0 && c == 113)) return false;
  return true;
}

class NetLimiter {
 public:
  static constexpr int kWindowS = 10;
  enum Verdict { kPass, kDrop, kKick };

  // What the limiter did and saw since the last Take.
  struct Totals {
    uint64_t datagrams = 0, bytes = 0;         // datagrams seen (all), their bytes
    uint64_t droppedRate = 0;                  // datagrams over an address's or IP's rate
    uint64_t droppedOffline = 0;               // offline datagrams over the IP's rate
    uint64_t droppedBlocked = 0;               // datagrams of a kicked address
    uint64_t droppedMessages = 0;              // GMS messages over a connection's rate
    uint64_t kickedRate = 0, kickedCap = 0, kickedSilent = 0;
    unsigned maxDatagramsPerS = 0;             // the busiest address's datagrams in one second
    uint64_t maxBytesPerS = 0;                 // ... bytes in one second
    unsigned maxMessagesPerS = 0;              // the busiest connection's GMS messages in one second
    std::size_t tracked = 0;                   // addresses tracked now
  };

  explicit NetLimiter(const NetLimitOptions& o = NetLimitOptions()) : o_(o) {}
  const NetLimitOptions& Options() const { return o_; }

  static uint64_t AddrKey(uint32_t ip, uint16_t port) { return static_cast<uint64_t>(ip) << 16 | port; }

  // A datagram of `bytes` from ip:port; `offline` = no RakNet connection bit (the first byte's 0x80). kKick only for an
  // address with a connection (OnConnected); the endpoint closes it.
  Verdict OnDatagram(uint32_t ip, uint16_t port, std::size_t bytes, bool offline, double nowMs) {
    ++t_.datagrams;
    t_.bytes += bytes;
    if (!o_.on) return kPass;
    const uint64_t key = AddrKey(ip, port);
    auto blocked = blocked_.find(key);
    if (blocked != blocked_.end()) {
      if (nowMs < blocked->second) {
        ++t_.droppedBlocked;
        return kDrop;
      }
      blocked_.erase(blocked);
    }
    Prune(nowMs);
    if (!Room(key, ip, nowMs)) {  // a flood from more addresses than maxTracked: the untracked ones are dropped
      ++t_.droppedRate;
      return kDrop;
    }
    Addr& a = addrs_[key];
    a.lastMs = nowMs;
    const long sec = static_cast<long>(nowMs / 1000.0);
    if (a.sec != sec) {
      a.sec = sec;
      a.secDatagrams = 0;
      a.secBytes = 0;
    }
    ++a.secDatagrams;
    a.secBytes += bytes;
    if (a.secDatagrams > t_.maxDatagramsPerS) t_.maxDatagramsPerS = a.secDatagrams;
    if (a.secBytes > t_.maxBytesPerS) t_.maxBytesPerS = a.secBytes;
    const bool shared = Shared(ip);
    Ip& ipState = ips_[ip];
    ipState.lastMs = nowMs;
    bool drop = false;
    if (offline) {
      if (!(shared ? a.offline : ipState.offline).Take(o_.offlinePerSec, o_.offlineBurst, nowMs, 1)) {
        ++t_.droppedOffline;
        drop = true;
      }
    } else if (!a.datagrams.Take(o_.datagramsPerSec, o_.datagramBurst, nowMs, 1) ||
               !a.bytes.Take(o_.bytesPerSec, o_.byteBurst, nowMs, static_cast<double>(bytes)) ||
               (!shared &&
                (!ipState.datagrams.Take(o_.datagramsPerSec * o_.ipFactor, o_.datagramBurst * o_.ipFactor, nowMs, 1) ||
                 !ipState.bytes.Take(o_.bytesPerSec * o_.ipFactor, o_.byteBurst * o_.ipFactor, nowMs,
                                     static_cast<double>(bytes))))) {
      ++t_.droppedRate;
      drop = true;
    }
    if (!drop) return kPass;
    if (a.conn && a.drops.Mark(sec) >= o_.kickAfterS) {
      blocked_[key] = nowMs + o_.blockS * 1000.0;
      ++t_.kickedRate;
      return kKick;
    }
    return kDrop;
  }

  // A GMS message of a connection (Poll).
  Verdict OnMessage(uint64_t conn, std::size_t, double nowMs) {
    if (!o_.on) return kPass;
    auto it = conns_.find(conn);
    if (it == conns_.end()) return kPass;
    Conn& c = it->second;
    c.spoke = true;
    const long sec = static_cast<long>(nowMs / 1000.0);
    if (c.sec != sec) {
      c.sec = sec;
      c.secMessages = 0;
    }
    if (++c.secMessages > t_.maxMessagesPerS) t_.maxMessagesPerS = c.secMessages;
    if (c.messages.Take(o_.messagesPerSec, o_.messageBurst, nowMs, 1)) return kPass;
    ++t_.droppedMessages;
    if (c.drops.Mark(sec) >= o_.kickAfterS) {
      blocked_[c.addr] = nowMs + o_.blockS * 1000.0;
      ++t_.kickedRate;
      return kKick;
    }
    return kDrop;
  }

  // A new connection: false = its IP has connectionsPerIp already (the endpoint closes it; it isn't counted).
  bool OnConnected(uint64_t conn, uint32_t ip, uint16_t port, double nowMs) {
    if (!o_.on) return true;
    unsigned n = 0;
    for (const auto& kv : conns_)
      if (kv.second.ip == ip) ++n;
    if (o_.connectionsPerIp > 0 && n >= static_cast<unsigned>(o_.connectionsPerIp) && !Shared(ip)) {
      ++t_.kickedCap;
      return false;
    }
    Conn& c = conns_[conn];
    c = Conn();
    c.ip = ip;
    c.addr = AddrKey(ip, port);
    c.since = nowMs;
    addrs_[c.addr].conn = conn;
    addrs_[c.addr].lastMs = nowMs;
    return true;
  }

  void OnDisconnected(uint64_t conn) {
    auto it = conns_.find(conn);
    if (it == conns_.end()) return;
    auto a = addrs_.find(it->second.addr);
    if (a != addrs_.end() && a->second.conn == conn) a->second.conn = 0;
    conns_.erase(it);
  }

  // The connections that sent no GMS message within helloTimeoutS (each reported once; the caller closes them).
  std::vector<uint64_t> Silent(double nowMs) {
    std::vector<uint64_t> out;
    if (!o_.on || o_.helloTimeoutS <= 0 || nowMs - lastSilentMs_ < 1000) return out;
    lastSilentMs_ = nowMs;
    for (auto& kv : conns_) {
      if (!kv.second.spoke && !kv.second.silentReported && nowMs - kv.second.since > o_.helloTimeoutS * 1000.0) {
        kv.second.silentReported = true;
        ++t_.kickedSilent;
        out.push_back(kv.first);
      }
    }
    return out;
  }

  // Blocks an address's datagrams for blockS (a kick by Silent or the cap).
  void Block(uint64_t addrKey, double nowMs) {
    if (o_.on) blocked_[addrKey] = nowMs + o_.blockS * 1000.0;
  }

  Totals Take() {
    Totals t = t_;
    t.tracked = addrs_.size();
    t_ = Totals();
    return t;
  }

 private:
  struct Bucket {
    double tokens = -1, lastMs = 0;  // tokens < 0: new (starts full)
    bool Take(double perSec, double burst, double nowMs, double n) {
      if (tokens < 0) {
        tokens = burst;
        lastMs = nowMs;
      } else if (nowMs > lastMs) {
        tokens += (nowMs - lastMs) / 1000.0 * perSec;
        if (tokens > burst) tokens = burst;
        lastMs = nowMs;
      }
      if (tokens < n) return false;
      tokens -= n;
      return true;
    }
  };
  // The seconds with drops among the last kWindowS: Mark returns how many, this one included.
  struct DropSeconds {
    long secs[kWindowS] = {};
    int n = 0;
    int Mark(long sec) {
      bool have = false;
      int count = 0;
      for (int i = 0; i < n; ++i) {
        if (secs[i] == sec) have = true;
        if (sec - secs[i] < kWindowS) ++count;
      }
      if (!have) {
        // replace the oldest (or append)
        if (n < kWindowS) {
          secs[n++] = sec;
        } else {
          int oldest = 0;
          for (int i = 1; i < n; ++i)
            if (secs[i] < secs[oldest]) oldest = i;
          secs[oldest] = sec;
        }
        ++count;
      }
      return count;
    }
  };
  bool Shared(uint32_t ip) const {
    for (uint32_t s : o_.sharedIps)
      if (s == ip) return true;
    return false;
  }
  struct Addr {
    Bucket datagrams, bytes, offline;  // offline: only for a shared IP's addresses
    DropSeconds drops;
    uint64_t conn = 0;  // its connection, 0 = none
    double lastMs = 0;
    long sec = -1;
    unsigned secDatagrams = 0;
    uint64_t secBytes = 0;
  };
  struct Ip {
    Bucket datagrams, bytes, offline;
    double lastMs = 0;
  };
  struct Conn {
    uint32_t ip = 0;
    uint64_t addr = 0;
    double since = 0;
    bool spoke = false, silentReported = false;
    Bucket messages;
    DropSeconds drops;
    long sec = -1;
    unsigned secMessages = 0;
  };

  // Forgets idle addresses and IPs (no datagram for idleMs, no connection) and ended blocks.
  void Sweep(double nowMs, double idleMs) {
    for (auto it = addrs_.begin(); it != addrs_.end();)
      it = (!it->second.conn && nowMs - it->second.lastMs > idleMs) ? addrs_.erase(it) : std::next(it);
    for (auto it = ips_.begin(); it != ips_.end();) it = nowMs - it->second.lastMs > idleMs ? ips_.erase(it) : std::next(it);
    for (auto it = blocked_.begin(); it != blocked_.end();) it = nowMs >= it->second ? blocked_.erase(it) : std::next(it);
  }
  // Every 10 s: a minute idle is forgotten.
  void Prune(double nowMs) {
    if (nowMs - lastPruneMs_ < 10000) return;
    lastPruneMs_ = nowMs;
    Sweep(nowMs, 60000);
  }
  // Whether a datagram of key/ip may be tracked: yes when it is, or the tables have room; when full, a second idle is
  // forgotten (at most once a second, so a flood of new addresses costs no sweep per datagram).
  bool Room(uint64_t key, uint32_t ip, double nowMs) {
    const bool known = addrs_.count(key) != 0 && ips_.count(ip) != 0;
    if (known || (addrs_.size() < o_.maxTracked && ips_.size() < o_.maxTracked)) return true;
    if (nowMs - lastFullSweepMs_ >= 1000) {
      lastFullSweepMs_ = nowMs;
      Sweep(nowMs, 1000);
    }
    return addrs_.size() < o_.maxTracked && ips_.size() < o_.maxTracked;
  }

  NetLimitOptions o_;
  std::unordered_map<uint64_t, Addr> addrs_;
  std::unordered_map<uint32_t, Ip> ips_;
  std::unordered_map<uint64_t, Conn> conns_;
  std::unordered_map<uint64_t, double> blocked_;  // address -> until (ms)
  double lastPruneMs_ = 0, lastFullSweepMs_ = -1e18, lastSilentMs_ = -1e18;
  Totals t_;
};

}  // namespace gms
