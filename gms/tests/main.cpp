// gms_tests (G1): gms/core and gms/script without the game.
//   test_protocol.cpp  the wire-byte table against docs/data/gms_transport_ids.csv; the layouts against the bytes the
//                      live sessions sent and received (d2b_run2, d2e_run5)
//   test_script.cpp    the inject format (--inject, --inject-file)
//   test_session.cpp   GmsSession + ScriptedMatch against a RakNet 4.081 (protocol 5) test client on loopback, playing
//                      the live client's side of script A and of D2b-static's script; plus a refused HelloReq, a
//                      disconnect and the load timeout
// Usage: gms_tests [name filter]. Only 127.0.0.1 and ephemeral ports are used.
#include "tests/check.h"

int main(int argc, char** argv) {
  std::setvbuf(stdout, nullptr, _IONBF, 0);
  return gmstest::RunAll(argc, argv);
}
