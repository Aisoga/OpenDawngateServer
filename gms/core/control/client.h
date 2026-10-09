// The match host's end of the control channel (ADR-2 decisions 6 and 7, roadmap C1).
//
// dgserver starts the host with DGGMS_CONTROL (the server's control address), DGGMS_MATCH,
// DGGMS_TOKEN and DGGMS_BIND (the address the GMS port binds). The host dials DGGMS_CONTROL, sends
// hello, and then talks JSON Lines (messages.h). The engine thread polls the connection once per tick
// (ADR-2 decision 5's loop), so Poll never blocks; Send blocks only while the socket buffer is full.
// Engine-free: no game addresses, Winsock only.
#pragma once

#include <cstdint>
#include <string>
#include <vector>

#include "control/messages.h"

namespace dggms::control {

// HostEnv is the host's environment from dgserver.
struct HostEnv {
  std::string control;  // DGGMS_CONTROL, "127.0.0.1:10040"
  int64_t match = 0;    // DGGMS_MATCH
  std::string token;    // DGGMS_TOKEN
  std::string bind;     // DGGMS_BIND, "127.0.0.1:10042"
};

// ReadHostEnv reads the DGGMS_* variables. It returns false without an error if DGGMS_CONTROL is
// unset (not host mode: the DLL stays the D1 harness), and false with *err if a value is malformed.
bool ReadHostEnv(HostEnv& out, std::string* err = nullptr);

// SplitHostPort splits "a.b.c.d:port" (IPv4 dotted quad, port 1-65535).
bool SplitHostPort(const std::string& hostPort, std::string& host, uint16_t& port);

class ControlClient {
 public:
  ControlClient();
  ~ControlClient();
  ControlClient(const ControlClient&) = delete;
  ControlClient& operator=(const ControlClient&) = delete;

  // Connect dials env.control (waiting at most timeoutMs) and sends hello{match, token, pid, build}.
  bool Connect(const HostEnv& env, int64_t pid, const std::string& build, int timeoutMs, std::string* err = nullptr);

  // Send writes one message (whole line). False if the connection failed (it is closed then).
  bool Send(const Message& m, std::string* err = nullptr);

  // Poll reads what has arrived without blocking and appends every complete message to out; lines
  // that don't decode go to *badLines (if given) and are skipped. It returns false once the connection
  // is closed or broken (messages read before that are still appended).
  bool Poll(std::vector<Message>& out, std::vector<std::string>* badLines = nullptr);

  bool Connected() const;
  void Close();

 private:
  uintptr_t sock_;
  bool wsa_ = false;
  std::string in_;
};

}  // namespace dggms::control
