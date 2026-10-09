// raknet_probe: does RakNet 4.081 accept the Dawngate client's RakNet handshake? (D2a, offline)
//
// Starts a RakNet 4.081 server (RakPeer) on 127.0.0.1 with an ephemeral port and replays the client's captured
// ID_OPEN_CONNECTION_REQUEST_1 datagrams to it from a plain UDP socket, printing each answer
// (ID_OPEN_CONNECTION_REPLY_1, or ID_INCOMPATIBLE_PROTOCOL_VERSION with the server's version byte).
//
// With --handshake it then finishes the connection with packets built byte for byte like the client's own writers in
// Dawngate.exe 1.0.903.854 (docs/GMS_PROTOCOL.md, "RakNet 4.081 vs. the client"): ID_OPEN_CONNECTION_REQUEST_2,
// then ID_CONNECTION_REQUEST and ID_NEW_INCOMING_CONNECTION in reliability-layer datagrams, then one user message
// whose first byte is 0x81 (the first GMS wire byte; the payload is a dummy, the HelloReq layout is D2b). It prints
// what comes back on the wire and what the server's application receives from RakPeer::Receive().
//
// Replay mode uses only loopback and ephemeral ports; nothing is sent anywhere else.
//
// With --listen it is instead a bare RakNet server on 127.0.0.1:<port> (0 = ephemeral) for <seconds>: it accepts
// connections and prints every packet RakPeer::Receive() hands the application (connection events, and user
// messages such as the client's first GMS message) as a hex dump with the local time. Without --connected it never
// sends a user message. With --connected (B9) it answers each new connection with the GMS message Connected (0x84),
// which makes the live client send kGmsHelloReq (0x81), and decodes that. It is meant for the live client on the GMS
// port: run_session.ps1 -GmsProbe silent|connected starts it and has dgserver announce the port without binding it
// (--gms-external); see docs/GMS_PROTOCOL.md "The live handshake (B9)".
//
// The scripted GMS session it once had (--session, D2b-live and D2e's injected messages) moved to gms/ in roadmap G1
// (ADR-2 decision 5): gms/core/session (GmsSession) and gms/script (gms_script.exe, run by run_session.ps1 -GmsProbe
// session, with the same options). The probe is a research tool again: replay, --listen and --connected.
//
// Build: CMakeLists.txt next to this file (raknet_probe_p6 = stock 4.081, raknet_probe_p5 = RAKNET_PROTOCOL_VERSION 5).
// Usage: raknet_probe_p5 <payloads.txt> [--handshake] [--to <port>]
//        raknet_probe_p5 --listen <port> <seconds> [--connected]
//   --to replays to a server already running on 127.0.0.1:<port> (e.g. a --listen instance) instead of starting one.
//   payloads.txt: one UDP payload per line in hex, as tshark writes them, e.g. for session b8_run6:
//   tshark -r re/logs/b8_run6/capture.pcapng -Y "udp.dstport==10042" -T fields -e udp.payload > payloads.txt
// Exit code: 0 = every datagram answered with ID_OPEN_CONNECTION_REPLY_1 (and, with --handshake, the server's
//            application saw ID_NEW_INCOMING_CONNECTION and the 0x81 message); 1 = the server refused or the handshake
//            stopped; 2 = usage, input or startup error. --listen: 0 = ran for <seconds>, 2 = startup error.

#include "GetTime.h"
#include "MessageIdentifiers.h"
#include "RakNetTypes.h"
#include "RakNetVersion.h"
#include "RakPeerInterface.h"

#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>

#include <cctype>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <map>
#include <string>
#include <thread>
#include <vector>

namespace {

using Bytes = std::vector<uint8_t>;

// OFFLINE_MESSAGE_DATA_ID: identical in RakPeer.cpp (4.081) and in Dawngate.exe (.rdata 0x011FDC08).
const uint8_t kMagic[16] = {0x00, 0xFF, 0xFF, 0x00, 0xFE, 0xFE, 0xFE, 0xFE,
                            0xFD, 0xFD, 0xFD, 0xFD, 0x12, 0x34, 0x56, 0x78};

// Reliabilities as RakNet writes them in the 3-bit field (PacketPriority.h).
enum : uint8_t { kUnreliable = 0, kUnreliableSequenced = 1, kReliable = 2, kReliableOrdered = 3, kReliableSequenced = 4 };

const char* MessageName(uint8_t id) {
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
    case 0x81: return "0x81 (first GMS wire byte)";
    default: return "?";
  }
}

std::string Hex(const uint8_t* p, size_t n, size_t max = 40) {
  std::string s;
  char buf[4];
  for (size_t i = 0; i < n && i < max; ++i) {
    std::snprintf(buf, sizeof buf, "%02x", p[i]);
    s += buf;
  }
  if (n > max) s += "...";
  return s;
}
std::string Hex(const Bytes& b, size_t max = 40) { return Hex(b.data(), b.size(), max); }

// Keeps only hex digits, so separators, a UTF-8 BOM or the NULs of a UTF-16 file (PowerShell 5.1's `>`) don't matter.
bool ParseHexLine(const std::string& line, Bytes& out) {
  out.clear();
  std::string h;
  for (char c : line)
    if (std::isxdigit(static_cast<unsigned char>(c))) h += c;
  if (h.empty() || h.size() % 2) return false;
  for (size_t i = 0; i < h.size(); i += 2) {
    char* end = nullptr;
    const std::string byte = h.substr(i, 2);
    const long v = std::strtol(byte.c_str(), &end, 16);
    if (*end) return false;
    out.push_back(static_cast<uint8_t>(v));
  }
  return true;
}

uint16_t BE16(const uint8_t* p) { return static_cast<uint16_t>(p[0] << 8 | p[1]); }
uint32_t LE24(const uint8_t* p) { return p[0] | p[1] << 8 | p[2] << 16; }
uint64_t BE64(const uint8_t* p) {
  uint64_t v = 0;
  for (int i = 0; i < 8; ++i) v = v << 8 | p[i];
  return v;
}
// SystemAddress as BitStream::Write(SystemAddress) puts an IPv4 address: version byte 4, the address bit-inverted
// (network order), the port (network order). Same code in 4.081 (BitStream.h) and the client (0x006C1D10).
std::string Address(const uint8_t* p) {
  if (p[0] != 4) return "(ip version " + std::to_string(p[0]) + ")";
  char buf[32];
  std::snprintf(buf, sizeof buf, "%u.%u.%u.%u:%u", static_cast<uint8_t>(~p[1]), static_cast<uint8_t>(~p[2]),
                static_cast<uint8_t>(~p[3]), static_cast<uint8_t>(~p[4]), BE16(p + 5));
  return buf;
}

struct Writer {
  Bytes b;
  void U8(uint8_t v) { b.push_back(v); }
  void BE16(uint16_t v) { U8(static_cast<uint8_t>(v >> 8)); U8(static_cast<uint8_t>(v)); }
  void LE24(uint32_t v) { U8(static_cast<uint8_t>(v)); U8(static_cast<uint8_t>(v >> 8)); U8(static_cast<uint8_t>(v >> 16)); }
  void BE64(uint64_t v) { for (int i = 7; i >= 0; --i) U8(static_cast<uint8_t>(v >> (8 * i))); }
  void Raw(const uint8_t* p, size_t n) { b.insert(b.end(), p, p + n); }
  void Raw(const Bytes& x) { b.insert(b.end(), x.begin(), x.end()); }
  void Magic() { Raw(kMagic, sizeof kMagic); }
  void Address(const sockaddr_in& a) {
    U8(4);
    const uint32_t inverted = ~a.sin_addr.s_addr;
    Raw(reinterpret_cast<const uint8_t*>(&inverted), 4);
    Raw(reinterpret_cast<const uint8_t*>(&a.sin_port), 2);
  }
  void UnassignedAddress() {  // a default SystemAddress (0.0.0.0:0)
    U8(4);
    for (int i = 0; i < 4; ++i) U8(0xFF);
    BE16(0);
  }
};

// One message in a reliability-layer datagram, laid out like ReliabilityLayer::WriteToBitStreamFromInternalPacket
// (4.081 ReliabilityLayer.cpp; client 0x006D2120) after DatagramHeaderFormat::Serialize (client 0x006D3420) for a
// data datagram without packet-pair/continuous-send/B-and-AS flags: 0x80, datagram number (uint24 LE).
Bytes Datagram(uint32_t datagramNumber, uint8_t reliability, uint32_t messageNumber, uint32_t orderingIndex,
               const Bytes& payload) {
  Writer w;
  w.U8(0x80);
  w.LE24(datagramNumber);
  w.U8(static_cast<uint8_t>(reliability << 5));  // 3 bits reliability, 1 bit "split" = 0, aligned
  w.BE16(static_cast<uint16_t>(payload.size() * 8));
  if (reliability == kReliable || reliability == kReliableOrdered || reliability == kReliableSequenced)
    w.LE24(messageNumber);
  if (reliability == kReliableOrdered) {
    w.LE24(orderingIndex);
    w.U8(0);  // ordering channel
  }
  w.Raw(payload);
  return w.b;
}

// ACK datagram for one datagram number: 0xC0 (valid, ACK, no B-and-AS), then DS_RangeList::Serialize:
// count (uint16 BE), per range minEqualsMax (one byte), min (uint24 LE)[, max].
Bytes Ack(uint32_t datagramNumber) {
  Writer w;
  w.U8(0xC0);
  w.BE16(1);
  w.U8(1);
  w.LE24(datagramNumber);
  return w.b;
}

struct InMessage {
  uint8_t reliability;
  Bytes data;
};
struct InDatagram {
  enum Kind { kInvalid, kAck, kNak, kData } kind = kInvalid;
  uint32_t number = 0;
  std::vector<InMessage> messages;
};

InDatagram ParseDatagram(const Bytes& d) {
  InDatagram out;
  if (d.empty() || !(d[0] & 0x80)) return out;
  if (d[0] & 0x40) { out.kind = InDatagram::kAck; return out; }
  if (d[0] & 0x20) { out.kind = InDatagram::kNak; return out; }
  if (d.size() < 4) return out;
  out.number = LE24(&d[1]);
  size_t p = 4;
  while (p + 3 <= d.size()) {
    const uint8_t h = d[p++];
    const uint8_t rel = h >> 5;
    const bool split = (h >> 4) & 1;
    const size_t bits = BE16(&d[p]);
    p += 2;
    if (rel == kReliable || rel == kReliableOrdered || rel == kReliableSequenced) p += 3;
    if (rel == kUnreliableSequenced || rel == kReliableSequenced) p += 3;
    if (rel == kUnreliableSequenced || rel == kReliableOrdered || rel == kReliableSequenced) p += 4;
    if (split) p += 10;
    const size_t n = (bits + 7) / 8;
    if (p + n > d.size()) return out;
    out.messages.push_back({rel, Bytes(d.begin() + p, d.begin() + p + n)});
    p += n;
  }
  out.kind = InDatagram::kData;
  return out;
}

struct UdpClient {
  SOCKET s = INVALID_SOCKET;
  sockaddr_in local{};
  sockaddr_in server{};

  bool Open(uint16_t serverPort) {
    s = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (s == INVALID_SOCKET) return false;
    sockaddr_in a{};
    a.sin_family = AF_INET;
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    a.sin_port = 0;  // ephemeral
    if (bind(s, reinterpret_cast<sockaddr*>(&a), sizeof a) != 0) return false;
    int len = sizeof local;
    getsockname(s, reinterpret_cast<sockaddr*>(&local), &len);
    server.sin_family = AF_INET;
    server.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    server.sin_port = htons(serverPort);
    return true;
  }
  bool Send(const Bytes& b) const {
    return sendto(s, reinterpret_cast<const char*>(b.data()), static_cast<int>(b.size()), 0,
                  reinterpret_cast<const sockaddr*>(&server), sizeof server) == static_cast<int>(b.size());
  }
  bool Recv(Bytes& out, int timeoutMs) const {
    fd_set fds;
    FD_ZERO(&fds);
    FD_SET(s, &fds);
    timeval tv{timeoutMs / 1000, (timeoutMs % 1000) * 1000};
    if (select(0, &fds, nullptr, nullptr, &tv) <= 0) return false;
    char buf[2048];
    sockaddr_in from{};
    int fromLen = sizeof from;
    const int n = recvfrom(s, buf, sizeof buf, 0, reinterpret_cast<sockaddr*>(&from), &fromLen);
    if (n < 0) return false;
    out.assign(buf, buf + n);
    return true;
  }
  ~UdpClient() {
    if (s != INVALID_SOCKET) closesocket(s);
  }
};

// Checks a captured request against the client's writer (RakPeer::RunUpdateCycle, client 0x006CE244..0x006CE2A8):
// ID 0x05, magic, protocol version byte, zero padding to the MTU probe size minus 28.
std::string DescribeRequest1(const Bytes& d) {
  if (d.size() < 18 || d[0] != ID_OPEN_CONNECTION_REQUEST_1 || std::memcmp(&d[1], kMagic, 16) != 0)
    return "not an ID_OPEN_CONNECTION_REQUEST_1: " + Hex(d, 24);
  size_t nonzero = 0;
  for (size_t i = 18; i < d.size(); ++i) nonzero += d[i] != 0;
  return "05 + magic + protocol " + std::to_string(d[17]) + " + " + std::to_string(d.size() - 18) + " padding bytes" +
         (nonzero ? " (" + std::to_string(nonzero) + " NOT zero)" : " (all zero)") + ", MTU probe " +
         std::to_string(d.size() + 28);
}

std::string DescribeOfflineReply(const Bytes& r) {
  if (r.empty()) return "(empty)";
  std::string s = Hex(&r[0], 1) + " " + MessageName(r[0]) + " (" + std::to_string(r.size()) + " B)";
  const bool magicAt1 = r.size() >= 17 && std::memcmp(&r[1], kMagic, 16) == 0;
  char buf[160];
  if (r[0] == ID_OPEN_CONNECTION_REPLY_1 && r.size() >= 28 && magicAt1) {
    // 06, magic, server guid (uint64 BE), hasSecurity (byte), MTU (uint16 BE); RakPeer.cpp 5163-5186.
    std::snprintf(buf, sizeof buf, ": server guid %016llx, security %u, MTU %u",
                  static_cast<unsigned long long>(BE64(&r[17])), r[25], BE16(&r[26]));
    s += buf;
  } else if (r[0] == ID_INCOMPATIBLE_PROTOCOL_VERSION && r.size() == 26 && std::memcmp(&r[2], kMagic, 16) == 0) {
    // 19, server protocol version, magic, server guid; RakPeer.cpp 5140-5144.
    std::snprintf(buf, sizeof buf, ": server protocol version %u, server guid %016llx", r[1],
                  static_cast<unsigned long long>(BE64(&r[18])));
    s += buf;
  } else if (r[0] == ID_OPEN_CONNECTION_REPLY_2 && r.size() >= 35 && magicAt1) {
    // 08, magic, server guid, client address as seen (7), MTU (uint16 BE), requiresSecurity (1 bit); 5299-5305.
    std::snprintf(buf, sizeof buf, ": server guid %016llx, client address %s, MTU %u, security bit %u",
                  static_cast<unsigned long long>(BE64(&r[17])), Address(&r[25]).c_str(), BE16(&r[32]), r[34] >> 7);
    s += buf;
  } else {
    s += ": " + Hex(r, 40);
  }
  return s;
}

std::vector<Bytes> ReadPayloads(const char* path) {
  std::vector<Bytes> out;
  std::ifstream in(path);
  std::string line;
  while (std::getline(in, line)) {
    Bytes b;
    if (ParseHexLine(line, b)) out.push_back(std::move(b));
  }
  return out;
}

// Collects datagrams from the server for up to `ms`, ACKs the data datagrams, and returns their messages.
std::vector<InMessage> Collect(const UdpClient& udp, int ms, const char* indent = "  ") {
  std::vector<InMessage> got;
  const auto end = std::chrono::steady_clock::now() + std::chrono::milliseconds(ms);
  while (std::chrono::steady_clock::now() < end) {
    Bytes d;
    if (!udp.Recv(d, 50)) continue;
    InDatagram dg = ParseDatagram(d);
    if (dg.kind == InDatagram::kAck) {
      std::printf("%s<- ACK datagram (%zu B): %s\n", indent, d.size(), Hex(d).c_str());
    } else if (dg.kind == InDatagram::kNak) {
      std::printf("%s<- NAK datagram (%zu B): %s\n", indent, d.size(), Hex(d).c_str());
    } else if (dg.kind == InDatagram::kData) {
      for (const InMessage& m : dg.messages) {
        std::printf("%s<- datagram %u, reliability %u: %02x %s (%zu B)\n", indent, dg.number, m.reliability,
                    m.data.empty() ? 0 : m.data[0], m.data.empty() ? "" : MessageName(m.data[0]), m.data.size());
        got.push_back(m);
      }
      udp.Send(Ack(dg.number));
    } else {
      std::printf("%s<- not a reliability-layer datagram (%zu B): %s\n", indent, d.size(), DescribeOfflineReply(d).c_str());
    }
  }
  return got;
}

int Handshake(RakNet::RakPeerInterface* srv, const UdpClient& udp, uint16_t mtu) {
  const uint64_t clientGuid = 0x0123456789ABCDEFull;

  // ID_OPEN_CONNECTION_REQUEST_2 as the client builds it after REPLY_1 without security (client 0x006CC343..0x006CC48B,
  // RakPeer.cpp 4757-4853): 07, magic, the server address it connects to, MTU (uint16 BE), its guid (uint64 BE).
  Writer r2;
  r2.U8(ID_OPEN_CONNECTION_REQUEST_2);
  r2.Magic();
  r2.Address(udp.server);
  r2.BE16(mtu);
  r2.BE64(clientGuid);
  std::printf("\n-> ID_OPEN_CONNECTION_REQUEST_2 (%zu B): %s\n", r2.b.size(), Hex(r2.b, 64).c_str());
  udp.Send(r2.b);
  Bytes reply;
  if (!udp.Recv(reply, 1000)) {
    std::printf("  <- no answer\n");
    return 1;
  }
  std::printf("  <- %s\n", DescribeOfflineReply(reply).c_str());
  if (reply[0] != ID_OPEN_CONNECTION_REPLY_2) return 1;

  // ID_CONNECTION_REQUEST as the client sends it after REPLY_2 (client 0x006CC937..0x006CCA49; RakPeer.cpp
  // 5005-5033): reliable, 09, guid (uint64 BE), RakNet::GetTime() (uint64 BE), doSecurity byte 0, no password
  // (cTransportRakNet::Connect 0x00ADCF70 passes an empty one).
  Writer cr;
  cr.U8(ID_CONNECTION_REQUEST);
  cr.BE64(clientGuid);
  cr.BE64(RakNet::GetTime());
  cr.U8(0);
  const Bytes dg0 = Datagram(0, kReliable, 0, 0, cr.b);
  std::printf("-> datagram 0, ID_CONNECTION_REQUEST (%zu B): %s\n", dg0.size(), Hex(dg0, 64).c_str());
  udp.Send(dg0);
  std::vector<InMessage> got = Collect(udp, 600);
  const InMessage* accepted = nullptr;
  for (const InMessage& m : got)
    if (!m.data.empty() && m.data[0] == ID_CONNECTION_REQUEST_ACCEPTED) accepted = &m;
  if (!accepted) {
    std::printf("  no ID_CONNECTION_REQUEST_ACCEPTED\n");
    return 1;
  }
  // 10, our address as the server sees it (7), system index (uint16 BE), 10 internal addresses (MAXIMUM_NUMBER_OF_
  // INTERNAL_IDS, 7 each), sendPingTime, sendPongTime (uint64 BE); RakPeer::OnConnectionRequest, client 0x006C7820.
  const Bytes& a = accepted->data;
  if (a.size() != 96) {
    std::printf("  ID_CONNECTION_REQUEST_ACCEPTED has %zu bytes, expected 96\n", a.size());
    return 1;
  }
  const uint64_t pongTime = BE64(&a[88]);
  std::printf("  ID_CONNECTION_REQUEST_ACCEPTED: our address %s, system index %u, first internal address %s, "
              "ping time %llu, pong time %llu\n",
              Address(&a[1]).c_str(), BE16(&a[8]), Address(&a[10]).c_str(),
              static_cast<unsigned long long>(BE64(&a[80])), static_cast<unsigned long long>(pongTime));

  // ID_NEW_INCOMING_CONNECTION as the client answers (client 0x006CF433..0x006CF5B9; RakPeer.cpp 6193-6201):
  // reliable ordered, 13, the server's address, 10 internal addresses, the server's pong time, our time.
  Writer ni;
  ni.U8(ID_NEW_INCOMING_CONNECTION);
  ni.Address(udp.server);
  ni.Address(udp.local);
  for (int i = 1; i < 10; ++i) ni.UnassignedAddress();
  ni.BE64(pongTime);
  ni.BE64(RakNet::GetTime());
  const Bytes dg1 = Datagram(1, kReliableOrdered, 1, 0, ni.b);
  std::printf("-> datagram 1, ID_NEW_INCOMING_CONNECTION (%zu B): %s\n", dg1.size(), Hex(dg1, 64).c_str());
  udp.Send(dg1);

  // A user message: first byte 0x81, the first GMS wire byte (GMS_PROTOCOL.md). The payload is a dummy.
  const Bytes user = {0x81, 'D', '2', 'a'};
  const Bytes dg2 = Datagram(2, kReliableOrdered, 2, 1, user);
  std::printf("-> datagram 2, user message (%zu B): %s\n", dg2.size(), Hex(dg2, 64).c_str());
  udp.Send(dg2);

  if (!srv) {  // --to: the server is another process; its own output shows what its application received
    Collect(udp, 1000, "  ");
    std::printf("handshake: ID_CONNECTION_REQUEST_ACCEPTED received; see the server's output for the rest\n");
    return 0;
  }
  bool sawNewIncoming = false, sawUser = false;
  const auto end = std::chrono::steady_clock::now() + std::chrono::milliseconds(1500);
  while (std::chrono::steady_clock::now() < end && !(sawNewIncoming && sawUser)) {
    Collect(udp, 100, "  ");
    for (RakNet::Packet* p = srv->Receive(); p; srv->DeallocatePacket(p), p = srv->Receive()) {
      std::printf("  server application: %02x %s (%u B) from %s, guid %s\n", p->data[0], MessageName(p->data[0]),
                  p->length, p->systemAddress.ToString(true), p->guid.ToString());
      sawNewIncoming |= p->data[0] == ID_NEW_INCOMING_CONNECTION;
      sawUser |= p->data[0] == 0x81 && p->length == user.size() && std::memcmp(p->data, user.data(), user.size()) == 0;
    }
  }
  std::printf("handshake: %s, user message 0x81 %s\n",
              sawNewIncoming ? "server reported ID_NEW_INCOMING_CONNECTION" : "NO ID_NEW_INCOMING_CONNECTION",
              sawUser ? "delivered unchanged" : "NOT delivered");
  return sawNewIncoming && sawUser ? 0 : 1;
}

void HexDump(const uint8_t* p, size_t n) {
  for (size_t off = 0; off < n; off += 16) {
    std::printf("    %04zx  %s\n", off, Hex(p + off, n - off < 16 ? n - off : 16, 16).c_str());
  }
}

uint64_t LE64(const uint8_t* p) {
  uint64_t v = 0;
  for (int i = 7; i >= 0; --i) v = v << 8 | p[i];
  return v;
}

// Wall-clock time (local, with milliseconds), to line the probe's output up with the client's Application.log.
std::string Clock() {
  SYSTEMTIME st;
  GetLocalTime(&st);
  char buf[16];
  std::snprintf(buf, sizeof buf, "%02u:%02u:%02u.%03u", st.wHour, st.wMinute, st.wSecond, st.wMilliseconds);
  return buf;
}

// GMS wire bytes (docs/data/gms_transport_ids.csv). The scripted session that used the others moved to
// gms/core/session and gms/script (G1).
const uint8_t kGmsHelloReq = 0x81;   // id 0, client -> GMS
const uint8_t kGmsConnected = 0x84;  // id 3

// What the client's GMS message carries (B9): one GMS wire byte, then the payload. TransportRakNet_HandleUserMessage
// (0x00ADD210) dispatches on packet byte 0 and builds the message from the bytes after it (0x00ADAE20).
void DescribeGms(const RakNet::Packet* p) {
  if (p->data[0] == kGmsHelloReq && p->length == 17) {
    // cClientSession::OnGmsConnected (0x00AB2A30) builds HelloReq with 16 payload bytes copied from the session at
    // +0x08..+0x17: +0x08 the local user's 64-bit id, +0x10 the playgroup id (ClientNetworkAdapter_InitNetworkMesh
    // 0x00AB6570), as raw little-endian memory.
    std::printf("    kGmsHelloReq: user id %llu (0x%016llx), playgroup id %llu\n",
                static_cast<unsigned long long>(LE64(p->data + 1)), static_cast<unsigned long long>(LE64(p->data + 1)),
                static_cast<unsigned long long>(LE64(p->data + 9)));
  }
}

// --listen: a bare RakNet server on 127.0.0.1:<port>. With `connected` (B9) it answers each new connection with the
// GMS message Connected (wire byte 0x84, no payload), which the client's cClientSession waits for before it sends
// HelloReq: OnGmsConnected (0x00AB2A30) is subscribed to GMS id 3 in ClientSession_ConnectToGms (0x00AB3980); the
// RakNet connection alone only runs cClientSession::OnConnected (0x00AB3310, session state 2).
int Listen(uint16_t port, int seconds, bool connected) {
  RakNet::RakPeerInterface* srv = RakNet::RakPeerInterface::GetInstance();
  RakNet::SocketDescriptor sd(port, "127.0.0.1");
  if (srv->Startup(8, &sd, 1) != RakNet::RAKNET_STARTED) {
    std::fprintf(stderr, "RakPeer::Startup on 127.0.0.1:%u failed (port in use?)\n", port);
    RakNet::RakPeerInterface::DestroyInstance(srv);
    return 2;
  }
  srv->SetMaximumIncomingConnections(8);
  std::printf("%s RakNet %s server, RAKNET_PROTOCOL_VERSION %d, listening on 127.0.0.1:%u for %d s, guid %s%s\n",
              Clock().c_str(), RAKNET_VERSION, RAKNET_PROTOCOL_VERSION, srv->GetMyBoundAddress().GetPort(), seconds,
              srv->GetMyGUID().ToString(), connected ? "; answers a new connection with kGmsConnected (0x84)" : "");
  const auto start = std::chrono::steady_clock::now();
  const auto end = start + std::chrono::seconds(seconds);
  while (std::chrono::steady_clock::now() < end) {
    for (RakNet::Packet* p = srv->Receive(); p; srv->DeallocatePacket(p), p = srv->Receive()) {
      const double t = std::chrono::duration<double>(std::chrono::steady_clock::now() - start).count();
      std::printf("%s %8.3f %02x %s (%u B) from %s, guid %s\n", Clock().c_str(), t, p->data[0],
                  MessageName(p->data[0]), p->length, p->systemAddress.ToString(true), p->guid.ToString());
      HexDump(p->data, p->length);
      DescribeGms(p);
      if (connected && p->data[0] == ID_NEW_INCOMING_CONNECTION) {
        const char msg[1] = {static_cast<char>(kGmsConnected)};
        const uint32_t receipt = srv->Send(msg, sizeof msg, HIGH_PRIORITY, RELIABLE_ORDERED, 0, p->guid, false);
        std::printf("%s %8.3f sent kGmsConnected (84, 1 B, reliable ordered, channel 0) to %s: %s\n", Clock().c_str(),
                    t, p->systemAddress.ToString(true), receipt ? "queued" : "FAILED");
      }
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(5));
  }
  std::printf("%s listen time over; shutting down\n", Clock().c_str());
  srv->Shutdown(100);
  RakNet::RakPeerInterface::DestroyInstance(srv);
  return 0;
}

}  // namespace

int main(int argc, char** argv) {
  std::setvbuf(stdout, nullptr, _IONBF, 0);  // keep the log complete if the process is stopped
  WSADATA wsa;
  WSAStartup(MAKEWORD(2, 2), &wsa);

  if (argc >= 4 && std::strcmp(argv[1], "--listen") == 0) {
    bool connected = false, ok = true;
    for (int i = 4; i < argc && ok; ++i) {
      if (std::strcmp(argv[i], "--connected") == 0) {
        connected = true;
      } else {
        ok = false;
        if (std::strcmp(argv[i], "--session") == 0)
          std::fprintf(stderr, "--session moved to gms/script (gms_script.exe, roadmap G1); see docs/GMS_PROTOCOL.md\n");
      }
    }
    if (ok) {
      const int rc = Listen(static_cast<uint16_t>(std::atoi(argv[2])), std::atoi(argv[3]), connected);
      WSACleanup();
      return rc;
    }
  }
  if (argc < 2 || argv[1][0] == '-') {
    std::fprintf(stderr,
                 "usage: %s <payloads.txt> [--handshake] [--to <port>]\n"
                 "       %s --listen <port> <seconds> [--connected]\n"
                 "(the scripted GMS session is gms_script.exe in gms/, roadmap G1)\n",
                 argv[0], argv[0]);
    return 2;
  }
  bool handshake = false;
  uint16_t to = 0;  // --to: replay to a server on 127.0.0.1:<port> (e.g. --listen) instead of an own one
  for (int i = 2; i < argc; ++i) {
    if (std::strcmp(argv[i], "--handshake") == 0) handshake = true;
    if (std::strcmp(argv[i], "--to") == 0 && i + 1 < argc) to = static_cast<uint16_t>(std::atoi(argv[++i]));
  }
  const std::vector<Bytes> payloads = ReadPayloads(argv[1]);
  if (payloads.empty()) {
    std::fprintf(stderr, "no hex payloads in %s\n", argv[1]);
    return 2;
  }

  RakNet::RakPeerInterface* srv = nullptr;
  uint16_t port = to;
  if (!to) {
    srv = RakNet::RakPeerInterface::GetInstance();
    RakNet::SocketDescriptor sd(0, "127.0.0.1");  // ephemeral loopback port
    if (srv->Startup(8, &sd, 1) != RakNet::RAKNET_STARTED) {
      std::fprintf(stderr, "RakPeer::Startup failed\n");
      return 2;
    }
    srv->SetMaximumIncomingConnections(8);
    port = srv->GetMyBoundAddress().GetPort();
    std::printf("RakNet %s server, RAKNET_PROTOCOL_VERSION %d, ID_USER_PACKET_ENUM 0x%02x, on 127.0.0.1:%u, guid %s\n",
                RAKNET_VERSION, RAKNET_PROTOCOL_VERSION, ID_USER_PACKET_ENUM, port, srv->GetMyGUID().ToString());
  } else {
    std::printf("target: a server on 127.0.0.1:%u\n", port);
  }

  UdpClient udp;
  if (!udp.Open(port)) {
    std::fprintf(stderr, "UDP socket failed\n");
    return 2;
  }
  std::printf("replaying %zu datagrams from %s (from 127.0.0.1:%u)\n", payloads.size(), argv[1], ntohs(udp.local.sin_port));

  int replies1 = 0;
  uint16_t firstMtu = 0;
  std::map<std::string, int> summary;
  for (size_t i = 0; i < payloads.size(); ++i) {
    const Bytes& d = payloads[i];
    udp.Send(d);
    Bytes r;
    std::string answer = udp.Recv(r, 1000) ? DescribeOfflineReply(r) : "no answer";
    std::printf("#%02zu %4zu B  %s\n     -> %s\n", i + 1, d.size(), DescribeRequest1(d).c_str(), answer.c_str());
    if (!r.empty() && r[0] == ID_OPEN_CONNECTION_REPLY_1 && r.size() >= 28) {
      ++replies1;
      if (!firstMtu) firstMtu = BE16(&r[26]);
      ++summary["REPLY_1 MTU " + std::to_string(BE16(&r[26]))];
    } else {
      ++summary[r.empty() ? "no answer" : MessageName(r[0])];
    }
  }
  std::printf("summary:");
  for (const auto& kv : summary) std::printf(" %s x%d;", kv.first.c_str(), kv.second);
  std::printf("\n");

  int rc = replies1 == static_cast<int>(payloads.size()) ? 0 : 1;
  if (rc == 0 && handshake) rc = Handshake(srv, udp, firstMtu);

  if (srv) {
    srv->Shutdown(100);
    RakNet::RakPeerInterface::DestroyInstance(srv);
  }
  WSACleanup();
  std::printf("result: %s\n", rc == 0 ? "OK" : "REFUSED");
  return rc;
}
