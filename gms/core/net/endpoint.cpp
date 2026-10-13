#include "core/net/endpoint.h"

#include <algorithm>
#include <atomic>
#include <bitset>
#include <cstdio>
#include <cstring>
#include <deque>
#include <map>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <unordered_set>
#include <utility>

#include "GetTime.h"
#include "InternalPacket.h"
#include "MessageIdentifiers.h"
#include "PluginInterface2.h"
#include "RakNetSocket2.h"
#include "RakNetStatistics.h"
#include "RakNetTypes.h"
#include "RakNetVersion.h"
#include "RakPeer.h"
#include "RakPeerInterface.h"
#include "core/common/log.h"
#include "core/timing/clock.h"

namespace gms {

const char* RakMessageName(uint8_t id) {
  switch (id) {
    case ID_CONNECTED_PING: return "ID_CONNECTED_PING";
    case ID_CONNECTED_PONG: return "ID_CONNECTED_PONG";
    case ID_OPEN_CONNECTION_REQUEST_1: return "ID_OPEN_CONNECTION_REQUEST_1";
    case ID_OPEN_CONNECTION_REPLY_1: return "ID_OPEN_CONNECTION_REPLY_1";
    case ID_OPEN_CONNECTION_REQUEST_2: return "ID_OPEN_CONNECTION_REQUEST_2";
    case ID_OPEN_CONNECTION_REPLY_2: return "ID_OPEN_CONNECTION_REPLY_2";
    case ID_CONNECTION_REQUEST: return "ID_CONNECTION_REQUEST";
    case ID_CONNECTION_REQUEST_ACCEPTED: return "ID_CONNECTION_REQUEST_ACCEPTED";
    case ID_CONNECTION_ATTEMPT_FAILED: return "ID_CONNECTION_ATTEMPT_FAILED";
    case ID_ALREADY_CONNECTED: return "ID_ALREADY_CONNECTED";
    case ID_NEW_INCOMING_CONNECTION: return "ID_NEW_INCOMING_CONNECTION";
    case ID_NO_FREE_INCOMING_CONNECTIONS: return "ID_NO_FREE_INCOMING_CONNECTIONS";
    case ID_DISCONNECTION_NOTIFICATION: return "ID_DISCONNECTION_NOTIFICATION";
    case ID_CONNECTION_LOST: return "ID_CONNECTION_LOST";
    case ID_CONNECTION_BANNED: return "ID_CONNECTION_BANNED";
    case ID_INVALID_PASSWORD: return "ID_INVALID_PASSWORD";
    case ID_INCOMPATIBLE_PROTOCOL_VERSION: return "ID_INCOMPATIBLE_PROTOCOL_VERSION";
    case ID_IP_RECENTLY_CONNECTED: return "ID_IP_RECENTLY_CONNECTED";
    default: return "?";
  }
}

namespace {

constexpr uint8_t kFirstGmsByte = 0x81;
constexpr size_t kMaxSamples = 200000;   // per TakeStats window (a minute has about 25,000 messages)
constexpr double kStaleMs = 60000;       // arrival and send stamps nobody took after this long are dropped

uint64_t Fnv(uint64_t h, const unsigned char* p, size_t n) {
  for (size_t i = 0; i < n; ++i) {
    h ^= p[i];
    h *= 1099511628211ull;
  }
  return h;
}
uint64_t ContentKey(const unsigned char* p, size_t n) { return Fnv(1469598103934665603ull, p, n) ^ n; }
uint64_t AddressKey(const RakNet::SystemAddress& a, const unsigned char* p, size_t n) {
  const unsigned long ai = RakNet::SystemAddress::ToInteger(a);
  unsigned char b[4] = {static_cast<unsigned char>(ai), static_cast<unsigned char>(ai >> 8),
                        static_cast<unsigned char>(ai >> 16), static_cast<unsigned char>(ai >> 24)};
  return Fnv(ContentKey(p, n), b, sizeof b);
}

void Prune(std::unordered_map<uint64_t, std::deque<double>>* m, double now) {
  for (auto it = m->begin(); it != m->end();) {
    while (!it->second.empty() && now - it->second.front() > kStaleMs) it->second.pop_front();
    it = it->second.empty() ? m->erase(it) : std::next(it);
  }
}

}  // namespace

std::string RakHardeningOptions::Describe() const {
  char b[300];
  std::snprintf(b, sizeof b,
                "handshake cookies %s, no reply to a short REQUEST_1 %s, IP ban of an unverified sender's nonsense %s, "
                "ordered messages ahead of a hole per connection %u / %u KB (0 = no limit)",
                cookies ? "on" : "off", request1NoSmaller ? "on" : "off", nonsenseBan ? "on (stock)" : "off",
                orderedAheadMessages, orderedAheadKb);
  return b;
}

std::string RakHardeningTotals::Describe() const {
  char b[300];
  std::snprintf(b, sizeof b,
                "cookies issued %llu, REQUEST_2 without the cookie %llu, short REQUEST_1 %llu, unverified nonsense "
                "closed %llu (banned %llu), closed over the ordered-ahead limit %llu",
                static_cast<unsigned long long>(cookiesIssued), static_cast<unsigned long long>(request2BadCookie),
                static_cast<unsigned long long>(request1Short), static_cast<unsigned long long>(nonsenseClosed),
                static_cast<unsigned long long>(nonsenseBanned), static_cast<unsigned long long>(orderedAheadClosed));
  return b;
}

struct Endpoint::Impl {
  RakNet::RakPeerInterface* peer = nullptr;
  RakHardeningOptions hardOpt;
  // The GUIDs as the packets carried them (with RakNet's system index), so Send addresses a connection the way
  // raknet_probe did.
  std::map<uint64_t, RakNet::RakNetGUID> guids;
  NetSimOptions simOpt;
  std::bitset<256> tracked, fast;

  // ---- shared with RakNet's threads (the update thread's plugin and simulator, the receive thread's handler)
  std::mutex mu;
  std::unordered_map<uint64_t, std::deque<double>> arrivals;  // GMS messages taken in, not yet polled
  // the limits (limits.h; under mu) and the addresses the receive thread found over them, to close in Poll
  NetLimitOptions limitOpt;
  NetLimiter limiter;
  bool limitsOn = false;
  std::vector<RakNet::SystemAddress> kickAddrs;
  std::unordered_map<uint64_t, std::deque<double>> sends;     // tracked messages sent, not yet on the wire
  double lastPrune = 0;
  Stats st;

  // ---- the network simulator (netsim.h)
  struct Datagram {
    double due = 0;
    RakNet::SystemAddress addr;
    int len = 0;
    char data[MAXIMUM_MTU_SIZE];
  };
  std::deque<Datagram> outQ, inQ;
  DelayLine outLine, inLine;
  RakNet::RakNetSocket2* socket = nullptr;
  RakNet::RNS2EventHandler* recvHandler = nullptr;  // the RakPeer, which owns the receive structs
  // ---- the limits, the engine thread's: connections to close (with why), and the ones closed (their later loss isn't news)
  std::deque<std::pair<uint64_t, std::string>> kickQueue;
  std::unordered_set<uint64_t> kicked;
  bool simOn = false;
  std::atomic<bool> stopping{false};
  std::atomic<bool> bypass{false};  // the update thread is sending a due datagram itself (RakNetSendTo lets it through)
  std::thread::id bypassThread;

  // RNS2_Windows's socket layer override: what RakNet sends goes into the out queue; RakNet's update cycle asks
  // RakNetRecvFrom for datagrams before it reads its own buffer, which gets the due ones of the in queue.
  struct SimLayer final : RakNet::SocketLayerOverride {
    Impl* impl;
    explicit SimLayer(Impl* i) : impl(i) {}
    int RakNetSendTo(const char* data, int length, const RakNet::SystemAddress& to) override {
      if (impl->stopping || (impl->bypass && std::this_thread::get_id() == impl->bypassThread)) return -1;
      if (length <= 0 || length > MAXIMUM_MTU_SIZE) return -1;
      std::lock_guard<std::mutex> lock(impl->mu);
      double due = 0;
      if (!impl->outLine.Admit(timing::NowMs(), &due)) return length;  // lost on the way
      impl->outQ.emplace_back();
      Datagram& d = impl->outQ.back();
      d.due = due;
      d.addr = to;
      d.len = length;
      std::memcpy(d.data, data, static_cast<size_t>(length));
      impl->st.simQueueMax = std::max(impl->st.simQueueMax, impl->outQ.size() + impl->inQ.size());
      return length;
    }
    int RakNetRecvFrom(char dataOut[MAXIMUM_MTU_SIZE], RakNet::SystemAddress* senderOut, bool) override {
      if (impl->stopping) return 0;
      std::lock_guard<std::mutex> lock(impl->mu);
      if (impl->inQ.empty() || impl->inQ.front().due > timing::NowMs()) return 0;
      const Datagram& d = impl->inQ.front();
      std::memcpy(dataOut, d.data, static_cast<size_t>(d.len));
      *senderOut = d.addr;
      const int n = d.len;
      impl->inQ.pop_front();
      return n;
    }
  };

  // The timing plugin: OnInternalPacket runs on RakNet's update thread for every message taken in (after the
  // datagram's arrival) and for every transmission (first or resend).
  struct Plugin final : RakNet::PluginInterface2 {
    Impl* impl;
    explicit Plugin(Impl* i) : impl(i) {}
    bool UsesReliabilityLayer(void) const override { return true; }
    void OnInternalPacket(RakNet::InternalPacket* p, unsigned, RakNet::SystemAddress addr, RakNet::TimeMS,
                          int isSend) override {
      if (!p || !p->data || p->dataBitLength < 8) return;
      const size_t n = static_cast<size_t>((p->dataBitLength + 7) >> 3);
      const bool whole = p->splitPacketCount == 0;
      const uint8_t wire = p->data[0];
      if (!isSend) {
        if (!whole || wire < kFirstGmsByte) return;
        const double now = timing::NowMs();
        std::lock_guard<std::mutex> lock(impl->mu);
        impl->arrivals[AddressKey(addr, p->data, n)].push_back(now);
        if (now - impl->lastPrune > 5000) {
          impl->lastPrune = now;
          Prune(&impl->arrivals, now);
          Prune(&impl->sends, now);
        }
        return;
      }
      if (!(whole || p->splitPacketIndex == 0) || wire < kFirstGmsByte) return;  // a message's first fragment only
      std::lock_guard<std::mutex> lock(impl->mu);
      Stats& stats = impl->st;
      if (p->timesSent > 1) {
        ++stats.resends;
        return;
      }
      ++stats.gmsSent;
      stats.gmsBytes += whole ? n : n * p->splitPacketCount;
      if (whole && impl->tracked[wire]) {
        auto it = impl->sends.find(ContentKey(p->data, n));
        if (it != impl->sends.end() && !it->second.empty()) {
          if (stats.trackedWireMs.size() < kMaxSamples) {
            stats.trackedWireMs.push_back(timing::NowMs() - it->second.front());
            stats.trackedWire.push_back(wire);
          }
          it->second.pop_front();
          if (it->second.empty()) impl->sends.erase(it);
          return;
        }
      }
      const RakNet::TimeUS now = RakNet::GetTimeUS();
      if (stats.queueMs.size() < kMaxSamples && now >= p->creationTime)
        stats.queueMs.push_back(static_cast<double>(now - p->creationTime) / 1000.0);
    }
  };
  std::unique_ptr<SimLayer> layer;
  std::unique_ptr<Plugin> plugin;

  // RakPeer::SetIncomingDatagramEventHandler takes a plain function: the endpoints with a simulator, by socket.
  static std::mutex& RegistryMutex() {
    static std::mutex m;
    return m;
  }
  static std::vector<std::pair<RakNet::RakNetSocket2*, Impl*>>& Registry() {
    static std::vector<std::pair<RakNet::RakNetSocket2*, Impl*>> r;
    return r;
  }
  // The receive thread: the limits drop a datagram over them before RakNet parses it; a datagram for a simulated
  // endpoint goes into its in queue (or is lost). A refused struct is given back here (RakPeer::OnRNS2Recv keeps it
  // otherwise).
  static bool OnIncoming(RakNet::RNS2RecvStruct* s) {
    std::lock_guard<std::mutex> reg(RegistryMutex());
    Impl* impl = nullptr;
    for (const auto& kv : Registry())
      if (kv.first == s->socket) impl = kv.second;
    if (!impl || impl->stopping || s->bytesRead <= 0 || s->bytesRead > MAXIMUM_MTU_SIZE) return true;
    if (impl->limitsOn) {
      const uint32_t ip = ntohl(s->systemAddress.address.addr4.sin_addr.s_addr);
      const bool offline = (static_cast<uint8_t>(s->data[0]) & 0x80) == 0;  // no BITFLAG_IS_VALID: no connection's
      NetLimiter::Verdict v;
      {
        std::lock_guard<std::mutex> lock(impl->mu);
        v = impl->limiter.OnDatagram(ip, s->systemAddress.GetPort(), static_cast<size_t>(s->bytesRead), offline,
                                     timing::NowMs());
        if (v == NetLimiter::kKick) impl->kickAddrs.push_back(s->systemAddress);
      }
      if (v != NetLimiter::kPass) {
        impl->recvHandler->DeallocRNS2RecvStruct(s, _FILE_AND_LINE_);
        return false;
      }
      if (!impl->simOn) return true;
    }
    {
      std::lock_guard<std::mutex> lock(impl->mu);
      double due = 0;
      if (impl->inLine.Admit(timing::NowMs(), &due)) {
        impl->inQ.emplace_back();
        Datagram& d = impl->inQ.back();
        d.due = due;
        d.addr = s->systemAddress;
        d.len = s->bytesRead;
        std::memcpy(d.data, s->data, static_cast<size_t>(s->bytesRead));
        impl->st.simQueueMax = std::max(impl->st.simQueueMax, impl->outQ.size() + impl->inQ.size());
      }
    }
    // RakPeer::DeallocRNS2RecvStruct (a Berkley socket keeps its handler in its binding: RakNetSocket2::GetEventHandler
    // is null), through the public base RNS2EventHandler
    impl->recvHandler->DeallocRNS2RecvStruct(s, _FILE_AND_LINE_);
    return false;
  }
  // RakNet's update thread, before each update cycle (at least every 10 ms): the due datagrams go out.
  static void OnUpdateThread(RakNet::RakPeerInterface*, void* data) {
    Impl* impl = static_cast<Impl*>(data);
    if (!impl || impl->stopping || !impl->socket) return;
    std::vector<Datagram> due;
    {
      std::lock_guard<std::mutex> lock(impl->mu);
      const double now = timing::NowMs();
      while (!impl->outQ.empty() && impl->outQ.front().due <= now) {
        due.push_back(impl->outQ.front());
        impl->outQ.pop_front();
      }
    }
    if (due.empty()) return;
    impl->bypassThread = std::this_thread::get_id();
    impl->bypass = true;
    for (Datagram& d : due) {
      RakNet::RNS2_SendParameters bsp;
      bsp.data = d.data;
      bsp.length = d.len;
      bsp.systemAddress = d.addr;
      impl->socket->Send(&bsp, _FILE_AND_LINE_);
    }
    impl->bypass = false;
  }
};

Endpoint::Endpoint() : impl_(new Impl) {}
Endpoint::~Endpoint() { Stop(); }

bool Endpoint::Start(const std::string& bindIp, uint16_t port, unsigned maxConnections, std::string* error) {
  Stop();
  Impl& m = *impl_;
  m.peer = RakNet::RakPeerInterface::GetInstance();
  m.recvHandler = static_cast<RakNet::RakPeer*>(m.peer);  // GetInstance makes a RakPeer
  m.plugin.reset(new Impl::Plugin(&m));
  m.peer->AttachPlugin(m.plugin.get());
  m.simOn = m.simOpt.Active();
  m.limitsOn = m.limitOpt.on;
  {
    std::lock_guard<std::mutex> lock(m.mu);
    m.limiter = NetLimiter(m.limitOpt);
    m.kickAddrs.clear();
  }
  m.kickQueue.clear();
  m.kicked.clear();
#if DG_RAKNET_HARDENING
  {  // before Startup (RakNet's threads read it without a lock)
    RakNet::RakPeer* rp = static_cast<RakNet::RakPeer*>(m.peer);
    RakNet::RakPeer::DgHardening h = rp->DgGetHardening();  // keeps the peer's random cookie key
    h.handshakeCookies = m.hardOpt.cookies;
    h.banNonsenseSenders = m.hardOpt.nonsenseBan;
    h.request1NoSmallerThanReply = m.hardOpt.request1NoSmaller;
    h.orderedAheadMaxMessages = m.hardOpt.orderedAheadMessages;
    h.orderedAheadMaxBytes = m.hardOpt.orderedAheadKb * 1024u;
    rp->DgSetHardening(h);
  }
#endif
  if (m.limitsOn && !m.simOn) m.peer->SetIncomingDatagramEventHandler(&Impl::OnIncoming);
  if (m.simOn) {
    m.outLine = DelayLine(m.simOpt);
    NetSimOptions in = m.simOpt;
    in.seed = m.simOpt.seed * 2654435761u + 1;  // the other direction's own random sequence
    m.inLine = DelayLine(in);
    m.peer->SetIncomingDatagramEventHandler(&Impl::OnIncoming);
    m.peer->SetUserUpdateThread(&Impl::OnUpdateThread, &m);
  }
  RakNet::SocketDescriptor sd(port, bindIp.c_str());
  const RakNet::StartupResult r = m.peer->Startup(maxConnections, &sd, 1);
  if (r != RakNet::RAKNET_STARTED) {
    if (error)
      *error = "RakPeer::Startup on " + bindIp + ":" + std::to_string(port) + " failed (result " + std::to_string(r) +
               "; port in use?)";
    m.peer->DetachPlugin(m.plugin.get());
    RakNet::RakPeerInterface::DestroyInstance(m.peer);
    m.peer = nullptr;
    m.plugin.reset();
    return false;
  }
  m.peer->SetMaximumIncomingConnections(static_cast<unsigned short>(maxConnections));
  // RakNet's own (loopback exempt); not with shared IPs (the tunnel gateway's players connect from one IP)
  if (m.limitsOn && m.limitOpt.sharedIps.empty()) m.peer->SetLimitIPConnectionFrequency(true);
  if (m.simOn || m.limitsOn) {
    DataStructures::List<RakNet::RakNetSocket2*> sockets;
    m.peer->GetSockets(sockets);
    RakNet::RakNetSocket2* s = sockets.Size() ? sockets[0] : nullptr;
    if (m.simOn && s && s->GetSocketType() == RakNet::RNS2T_WINDOWS) {
      m.layer.reset(new Impl::SimLayer(&m));
      static_cast<RakNet::RNS2_Windows*>(s)->SetSocketLayerOverride(m.layer.get());
      m.socket = s;
    } else if (m.simOn) {
      m.simOn = false;  // only RNS2_Windows has the override; the simulator stays off
      if (error) *error = "network simulator: no RNS2_Windows socket; off";
    }
    if (s && (m.simOn || m.limitsOn)) {
      std::lock_guard<std::mutex> reg(Impl::RegistryMutex());
      Impl::Registry().emplace_back(s, &m);
    } else if (m.limitsOn) {
      m.limitsOn = false;
      if (error) *error = "network limits: no socket; off";
    }
  }
  return true;
}

void Endpoint::Stop() {
  Impl& m = *impl_;
  if (!m.peer) return;
  {
    std::lock_guard<std::mutex> reg(Impl::RegistryMutex());
    auto& r = Impl::Registry();
    r.erase(std::remove_if(r.begin(), r.end(), [&m](const std::pair<RakNet::RakNetSocket2*, Impl*>& kv) {
              return kv.second == &m;
            }),
            r.end());
  }
  m.stopping = true;  // the simulator lets everything through while RakNet shuts down
  m.peer->Shutdown(100);
  m.peer->DetachPlugin(m.plugin.get());
  RakNet::RakPeerInterface::DestroyInstance(m.peer);
  m.peer = nullptr;
  m.socket = nullptr;
  m.layer.reset();
  m.plugin.reset();
  m.guids.clear();
  m.kickQueue.clear();
  m.kicked.clear();
  m.limitsOn = false;
  std::lock_guard<std::mutex> lock(m.mu);
  m.kickAddrs.clear();
  m.outQ.clear();
  m.inQ.clear();
  m.arrivals.clear();
  m.sends.clear();
  m.stopping = false;
  m.simOn = false;
}

bool Endpoint::Started() const { return impl_->peer != nullptr; }

uint16_t Endpoint::BoundPort() const { return impl_->peer ? impl_->peer->GetMyBoundAddress().GetPort() : 0; }

std::string Endpoint::GuidText() const { return impl_->peer ? impl_->peer->GetMyGUID().ToString() : ""; }

const char* Endpoint::RakNetVersion() { return RAKNET_VERSION; }
int Endpoint::ProtocolVersion() { return RAKNET_PROTOCOL_VERSION; }

namespace {

// A kicked connection's event: what the session gets for a lost connection.
void LostEvent(Endpoint::Event* e, uint64_t conn, const std::string& addr, const std::string& guid) {
  e->kind = Endpoint::Event::kDisconnected;
  e->conn = conn;
  e->addr = addr;
  e->guid = guid;
  e->data.assign(1, static_cast<uint8_t>(ID_CONNECTION_LOST));
  e->arrivedMs = -1;
  e->polledMs = timing::NowMs();
}

}  // namespace

bool Endpoint::Poll(Event* e) {
  Impl& m = *impl_;
  if (!m.peer) return false;
  if (m.limitsOn) {  // the connections found over their limits (receive thread) or silent, closed one per call
    std::vector<RakNet::SystemAddress> addrs;
    std::vector<uint64_t> silent;
    {
      std::lock_guard<std::mutex> lock(m.mu);
      addrs.swap(m.kickAddrs);
      silent = m.limiter.Silent(timing::NowMs());
    }
    for (const RakNet::SystemAddress& a : addrs) {
      const RakNet::RakNetGUID g = m.peer->GetGuidFromSystemAddress(a);
      if (g != RakNet::UNASSIGNED_RAKNET_GUID && m.guids.count(g.g))
        m.kickQueue.emplace_back(g.g, "datagrams over the limits (net_limits)");
    }
    for (uint64_t c : silent)
      if (m.guids.count(c)) m.kickQueue.emplace_back(c, "no GMS message since it connected (net_limits hello timeout)");
    while (!m.kickQueue.empty()) {
      const std::pair<uint64_t, std::string> k = m.kickQueue.front();
      m.kickQueue.pop_front();
      auto it = m.guids.find(k.first);
      if (it == m.guids.end()) continue;  // gone meanwhile
      const RakNet::RakNetGUID guid = it->second;
      const RakNet::SystemAddress sa = m.peer->GetSystemAddressFromGuid(guid);
      const std::string addr = sa.ToString(true), guidText = guid.ToString();
      m.peer->CloseConnection(guid, true);
      m.guids.erase(it);
      m.kicked.insert(k.first);
      {
        std::lock_guard<std::mutex> lock(m.mu);
        m.limiter.OnDisconnected(k.first);
        m.limiter.Block(NetLimiter::AddrKey(ntohl(sa.address.addr4.sin_addr.s_addr), sa.GetPort()), timing::NowMs());
      }
      Logf("%s net limits: closed %s (guid %s): %s", Clock().c_str(), addr.c_str(), guidText.c_str(), k.second.c_str());
      LostEvent(e, k.first, addr, guidText);
      return true;
    }
  }
  for (;;) {
    RakNet::Packet* p = m.peer->Receive();
    if (!p) return false;
    e->conn = p->guid.g;
    e->addr = p->systemAddress.ToString(true);
    e->guid = p->guid.ToString();
    e->data.assign(p->data, p->data + p->length);
    e->polledMs = timing::NowMs();
    e->arrivedMs = -1;
    const uint8_t id = p->length ? p->data[0] : 0;
    bool skip = false;  // not handed out
    if (id == ID_NEW_INCOMING_CONNECTION) {
      e->kind = Event::kConnected;
      bool ok = true;
      if (m.limitsOn) {
        std::lock_guard<std::mutex> lock(m.mu);
        ok = m.limiter.OnConnected(p->guid.g, ntohl(p->systemAddress.address.addr4.sin_addr.s_addr),
                                   p->systemAddress.GetPort(), e->polledMs);
      }
      if (ok) {
        m.guids[p->guid.g] = p->guid;
      } else {
        m.peer->CloseConnection(p->guid, true);
        m.kicked.insert(p->guid.g);
        Logf("%s net limits: closed %s (guid %s) at once: its IP has %d connections (net_limits)", Clock().c_str(),
             e->addr.c_str(), e->guid.c_str(), m.limitOpt.connectionsPerIp);
        skip = true;
      }
    } else if (id == ID_DISCONNECTION_NOTIFICATION || id == ID_CONNECTION_LOST) {
      e->kind = Event::kDisconnected;
      m.guids.erase(p->guid.g);
      if (m.limitsOn) {
        std::lock_guard<std::mutex> lock(m.mu);
        m.limiter.OnDisconnected(p->guid.g);
      }
      if (m.kicked.erase(p->guid.g)) skip = true;  // reported when it was closed
    } else if (id >= kFirstGmsByte) {
      e->kind = Event::kGms;
      std::lock_guard<std::mutex> lock(m.mu);
      auto it = m.arrivals.find(AddressKey(p->systemAddress, p->data, p->length));
      if (it != m.arrivals.end() && !it->second.empty()) {
        e->arrivedMs = it->second.front();
        it->second.pop_front();
        if (it->second.empty()) m.arrivals.erase(it);
      }
      if (m.limitsOn) {
        const NetLimiter::Verdict v = m.limiter.OnMessage(p->guid.g, p->length, e->polledMs);
        if (v != NetLimiter::kPass) skip = true;
        if (v == NetLimiter::kKick) m.kickQueue.emplace_front(p->guid.g, "GMS messages over the limits (net_limits)");
      }
    } else {
      e->kind = Event::kOther;
    }
    m.peer->DeallocatePacket(p);
    if (!skip) return true;
    if (!m.kickQueue.empty()) return Poll(e);  // the kick's event next
  }
}

bool Endpoint::Send(ConnId conn, const Bytes& msg) {
  Impl& m = *impl_;
  if (!m.peer || msg.empty()) return false;
  auto it = m.guids.find(conn);
  const RakNet::RakNetGUID guid = it != m.guids.end() ? it->second : RakNet::RakNetGUID(conn);
  const bool fast = m.fast[msg[0]];
  if (m.tracked[msg[0]] || fast) {
    std::lock_guard<std::mutex> lock(m.mu);
    if (m.tracked[msg[0]]) m.sends[ContentKey(msg.data(), msg.size())].push_back(timing::NowMs());
    if (fast) ++m.st.fastLane;
  }
  return m.peer->Send(reinterpret_cast<const char*>(msg.data()), static_cast<int>(msg.size()),
                      fast ? IMMEDIATE_PRIORITY : HIGH_PRIORITY, RELIABLE_ORDERED, fast ? 1 : 0, guid, false) != 0;
}

void Endpoint::Close(ConnId conn) {
  Impl& m = *impl_;
  if (!m.peer) return;
  auto it = m.guids.find(conn);
  if (it == m.guids.end()) return;
  m.peer->CloseConnection(it->second, true);
  m.guids.erase(it);
}

void Endpoint::SetNetSim(const NetSimOptions& o) { impl_->simOpt = o; }

void Endpoint::SetLimits(const NetLimitOptions& o) { impl_->limitOpt = o; }
const NetLimitOptions& Endpoint::Limits() const { return impl_->limitOpt; }

void Endpoint::SetHardening(const RakHardeningOptions& o) { impl_->hardOpt = o; }
const RakHardeningOptions& Endpoint::Hardening() const { return impl_->hardOpt; }
bool Endpoint::HardeningAvailable() {
#if DG_RAKNET_HARDENING
  return true;
#else
  return false;
#endif
}

RakHardeningTotals Endpoint::TakeHardeningTotals() {
  RakHardeningTotals t;
#if DG_RAKNET_HARDENING
  if (impl_->peer) {
    const RakNet::RakPeer::DgCounts c = static_cast<RakNet::RakPeer*>(impl_->peer)->DgTakeCounts();
    t.cookiesIssued = c.cookiesIssued;
    t.request1Short = c.request1Short;
    t.request2BadCookie = c.request2BadCookie;
    t.nonsenseClosed = c.nonsenseClosed;
    t.nonsenseBanned = c.nonsenseBanned;
    t.orderedAheadClosed = c.orderedAheadClosed;
  }
#endif
  return t;
}

NetLimiter::Totals Endpoint::TakeLimitTotals() {
  std::lock_guard<std::mutex> lock(impl_->mu);
  return impl_->limiter.Take();
}
const NetSimOptions& Endpoint::NetSim() const { return impl_->simOpt; }

void Endpoint::SetTrackedSends(const std::vector<uint8_t>& wireBytes) {
  impl_->tracked.reset();
  for (uint8_t b : wireBytes) impl_->tracked.set(b);
}

void Endpoint::SetFastLane(const std::vector<uint8_t>& wireBytes) {
  impl_->fast.reset();
  for (uint8_t b : wireBytes) impl_->fast.set(b);
}

void Endpoint::SampleConnections() {
  Impl& m = *impl_;
  if (!m.peer) return;
  for (const auto& kv : m.guids) {
    const RakNet::SystemAddress sa = m.peer->GetSystemAddressFromGuid(kv.second);
    if (sa == RakNet::UNASSIGNED_SYSTEM_ADDRESS) continue;
    RakNet::RakNetStatistics rs;
    if (!m.peer->GetStatistics(sa, &rs)) continue;
    unsigned msgs = 0;
    double bytes = 0;
    for (int i = 0; i < NUMBER_OF_PRIORITIES; ++i) {
      msgs += rs.messageInSendBuffer[i];
      bytes += rs.bytesInSendBuffer[i];
    }
    const int ping = m.peer->GetAveragePing(kv.second);
    std::lock_guard<std::mutex> lock(m.mu);
    Stats& st = m.st;
    st.pingLast = ping;
    st.pingMax = std::max(st.pingMax, ping);
    st.sendBufferMsgsMax = std::max(st.sendBufferMsgsMax, msgs);
    st.sendBufferBytesMax = std::max(st.sendBufferBytesMax, bytes);
    st.resendBufferMsgsMax = std::max(st.resendBufferMsgsMax, rs.messagesInResendBuffer);
    st.packetLossMax = std::max(st.packetLossMax, rs.packetlossLastSecond);
    st.limitedByCongestion = st.limitedByCongestion || rs.isLimitedByCongestionControl;
  }
}

Endpoint::Stats Endpoint::TakeStats() {
  Impl& m = *impl_;
  std::lock_guard<std::mutex> lock(m.mu);
  Stats s = std::move(m.st);
  m.st = Stats();
  s.simOut = m.outLine.Admitted();
  s.simOutDropped = m.outLine.Dropped();
  s.simOutAddedMeanMs = m.outLine.AddedMeanMs();
  s.simOutAddedMaxMs = m.outLine.AddedMaxMs();
  s.simIn = m.inLine.Admitted();
  s.simInDropped = m.inLine.Dropped();
  s.simInAddedMeanMs = m.inLine.AddedMeanMs();
  s.simInAddedMaxMs = m.inLine.AddedMaxMs();
  return s;
}

}  // namespace gms
