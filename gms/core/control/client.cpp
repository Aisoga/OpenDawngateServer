#include "control/client.h"

#include <winsock2.h>
#include <ws2tcpip.h>

#include <cstdlib>
#include <cstring>

namespace dggms::control {

namespace {

bool SetErr(std::string* err, const std::string& msg) {
  if (err) *err = msg;
  return false;
}

std::string Env(const char* name, bool& present) {
  char* value = nullptr;
  size_t len = 0;
  present = _dupenv_s(&value, &len, name) == 0 && value != nullptr;
  std::string s = present ? std::string(value) : std::string();
  std::free(value);
  return s;
}

bool ParseInt64(const std::string& s, int64_t& out) {
  if (s.empty()) return false;
  char* end = nullptr;
  long long v = std::strtoll(s.c_str(), &end, 10);
  if (end != s.c_str() + s.size()) return false;
  out = v;
  return true;
}

}  // namespace

bool SplitHostPort(const std::string& hostPort, std::string& host, uint16_t& port) {
  size_t colon = hostPort.rfind(':');
  if (colon == std::string::npos || colon == 0) return false;
  host = hostPort.substr(0, colon);
  in_addr a{};
  if (inet_pton(AF_INET, host.c_str(), &a) != 1) return false;
  int64_t p = 0;
  if (!ParseInt64(hostPort.substr(colon + 1), p) || p < 1 || p > 65535) return false;
  port = static_cast<uint16_t>(p);
  return true;
}

bool ReadHostEnv(HostEnv& out, std::string* err) {
  bool present = false;
  out.control = Env("DGGMS_CONTROL", present);
  if (!present) return SetErr(err, "");
  std::string host;
  uint16_t port = 0;
  if (!SplitHostPort(out.control, host, port)) return SetErr(err, "bad DGGMS_CONTROL \"" + out.control + "\"");
  std::string match = Env("DGGMS_MATCH", present);
  if (!ParseInt64(match, out.match)) return SetErr(err, "bad DGGMS_MATCH \"" + match + "\"");
  out.token = Env("DGGMS_TOKEN", present);
  out.bind = Env("DGGMS_BIND", present);
  if (!SplitHostPort(out.bind, host, port)) return SetErr(err, "bad DGGMS_BIND \"" + out.bind + "\"");
  return true;
}

ControlClient::ControlClient() : sock_(static_cast<uintptr_t>(INVALID_SOCKET)) {}

ControlClient::~ControlClient() { Close(); }

bool ControlClient::Connected() const { return sock_ != static_cast<uintptr_t>(INVALID_SOCKET); }

void ControlClient::Close() {
  if (Connected()) {
    closesocket(static_cast<SOCKET>(sock_));
    sock_ = static_cast<uintptr_t>(INVALID_SOCKET);
  }
  if (wsa_) {
    WSACleanup();
    wsa_ = false;
  }
  in_.clear();
}

bool ControlClient::Connect(const HostEnv& env, int64_t pid, const std::string& build, int timeoutMs,
                            std::string* err) {
  Close();
  WSADATA wsa;
  if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) return SetErr(err, "WSAStartup failed");
  wsa_ = true;
  std::string host;
  uint16_t port = 0;
  if (!SplitHostPort(env.control, host, port)) {
    Close();
    return SetErr(err, "bad control address \"" + env.control + "\"");
  }
  sockaddr_in addr{};
  addr.sin_family = AF_INET;
  addr.sin_port = htons(port);
  inet_pton(AF_INET, host.c_str(), &addr.sin_addr);

  SOCKET s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
  if (s == INVALID_SOCKET) {
    Close();
    return SetErr(err, "socket failed: " + std::to_string(WSAGetLastError()));
  }
  sock_ = static_cast<uintptr_t>(s);
  u_long nonBlocking = 1;
  ioctlsocket(s, FIONBIO, &nonBlocking);
  BOOL noDelay = TRUE;  // small lines, sent as they come
  setsockopt(s, IPPROTO_TCP, TCP_NODELAY, reinterpret_cast<const char*>(&noDelay), sizeof noDelay);
  if (connect(s, reinterpret_cast<sockaddr*>(&addr), sizeof addr) == SOCKET_ERROR) {
    int e = WSAGetLastError();
    if (e != WSAEWOULDBLOCK) {
      Close();
      return SetErr(err, "connect failed: " + std::to_string(e));
    }
    fd_set w, x;
    FD_ZERO(&w);
    FD_ZERO(&x);
    FD_SET(s, &w);
    FD_SET(s, &x);
    timeval tv{timeoutMs / 1000, (timeoutMs % 1000) * 1000};
    int n = select(0, nullptr, &w, &x, &tv);
    int soErr = 0;
    int len = sizeof soErr;
    getsockopt(s, SOL_SOCKET, SO_ERROR, reinterpret_cast<char*>(&soErr), &len);
    if (n <= 0 || FD_ISSET(s, &x) || soErr != 0) {
      Close();
      return SetErr(err, n == 0 ? "connect timed out" : "connect failed: " + std::to_string(soErr));
    }
  }
  Hello hello;
  hello.match = env.match;
  hello.token = env.token;
  hello.pid = pid;
  hello.build = build;
  return Send(hello, err);
}

bool ControlClient::Send(const Message& m, std::string* err) {
  if (!Connected()) return SetErr(err, "not connected");
  std::string line = Encode(m);
  SOCKET s = static_cast<SOCKET>(sock_);
  size_t done = 0;
  while (done < line.size()) {
    int n = send(s, line.data() + done, static_cast<int>(line.size() - done), 0);
    if (n > 0) {
      done += static_cast<size_t>(n);
      continue;
    }
    int e = WSAGetLastError();
    if (n == SOCKET_ERROR && e == WSAEWOULDBLOCK) {  // buffer full: wait until writable (5 s at most)
      fd_set w;
      FD_ZERO(&w);
      FD_SET(s, &w);
      timeval tv{5, 0};
      if (select(0, nullptr, &w, nullptr, &tv) > 0) continue;
      e = WSAETIMEDOUT;
    }
    Close();
    return SetErr(err, "send failed: " + std::to_string(e));
  }
  return true;
}

bool ControlClient::Poll(std::vector<Message>& out, std::vector<std::string>* badLines) {
  if (!Connected()) return false;
  SOCKET s = static_cast<SOCKET>(sock_);
  bool open = true;
  char buf[4096];
  for (;;) {
    int n = recv(s, buf, sizeof buf, 0);
    if (n > 0) {
      in_.append(buf, static_cast<size_t>(n));
      continue;
    }
    if (n == SOCKET_ERROR && WSAGetLastError() == WSAEWOULDBLOCK) break;
    open = false;  // 0 = orderly close, anything else = broken
    break;
  }
  size_t start = 0;
  for (;;) {
    size_t nl = in_.find('\n', start);
    if (nl == std::string::npos) break;
    std::string_view line(in_.data() + start, nl - start);
    start = nl + 1;
    if (!line.empty() && line.back() == '\r') line.remove_suffix(1);
    if (line.find_first_not_of(" \t") == std::string_view::npos) continue;
    std::string err;
    Message m;
    if (line.size() > kMaxLine) {
      if (badLines) badLines->push_back("line too long");
    } else if (Decode(line, m, &err)) {
      out.push_back(std::move(m));
    } else if (badLines) {
      badLines->push_back(err);
    }
  }
  in_.erase(0, start);
  if (in_.size() > kMaxLine) {  // a line without end: the peer is not speaking the protocol
    if (badLines) badLines->push_back("line too long");
    open = false;
  }
  if (!open) Close();
  return open;
}

}  // namespace dggms::control
