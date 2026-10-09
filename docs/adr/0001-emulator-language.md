# ADR-1: Emulator language and repository layout

- **Status:** accepted, 2026-10-07 (start of M3); revised the same day for the user's preference for Go. Revisit only on the triggers under *Consequences*. ADR-2 (2026-10-08) confirms the in-process C++ match host, so the clean-room language question is closed. It replaces decision 3's default transport: the host links RakNet 4.081 built for protocol 5 instead of driving the client's own RakNet.
- **Decision in one sentence:**
  - The emulator's network services are written in **Go** (stdlib only) in a new top-level `server/`, ported from the Python stubs in `re/stubs/`.
  - The match host is **C++** (MSVC x86) in `gms/`, once D1 confirms that the client's own simulation can run in server mode.
  - **Python** stays for research tools, client patchers, the test harness and certificate generation.

## Context

**User preference (project owner, 2026-10-07):** a slight preference for Go, for performance, but only if it stays "relatively simple" and doesn't burden the process. So Go is the default wherever its concrete blockers can be solved without heavy workarounds. Where Go would force significant complexity, this ADR says so and quantifies it.

**What the emulator must speak**:

1. **Redirector:** TLS on port 42127. ProtoSSL negotiates at most TLS 1.1, offers only RSA key exchange (AES128/256-SHA, RC4-SHA, RC4-MD5) and sends no SNI. Our certificate is RSA-1024 with e = 3, MD5-signed by the CA we patch into the exe (Q3). Whether e = 3 and MD5 are actually required is untested. Everything after the redirector is plain TCP (`SECU = 0`).
2. **Blaze core:** Fire framing plus TDF, request/reply RPC and notifications, at a low rate. Reply layouts come from the client's TDF member tables (`docs/data/tdf_member_tables.txt`, produced by the Python tool `re/scripts/tdf_tables.py`). Also HTTP (web content, QoS) and a telemetry sink. Accounts and personas will need persistence; there is none yet.
3. **Match server (GMS), from M4:** RakNet 4.x over UDP and 77 message types. The session shell, command intake and replication (38 message types nobody builds) are missing. The simulation itself is in the client. D1 tests whether a server-mode `cGameSimulator` runs inside `Dawngate.exe` (32-bit x86, MSVC 2010 ABI). If it doesn't, a clean-room host needs Lua 5.1 with a 4-byte float `lua_Number`. Checked: 1,382 of the 1,383 type-`3681D755` entries in `ServerData.package` start with `1B 4C 75 61 51 00 01 04 04 04 04 00`; the remaining one is plain source.

**What exists:** `re/stubs/` is Python 3.12 using only the standard library.
- `blaze_stub.py` (364 lines): redirector, Blaze dispatcher with `preAuth` and `ping`, and the QoS, HTTP and telemetry loggers.
- `tdf.py` (220 lines): the codec.
- `make_certs.py`: certificate generation, using `cryptography` plus Git's `openssl` because it needs MD5 signatures.

All research tools are Python too. `run_session.ps1` reads the stub's ports from `blaze_stub.py`.

**Dev box** (checked 2026-10-07; see [`SETUP.md`](../SETUP.md)):
- Installed: Windows 10, Go 1.24.1, Python 3.12.7 with OpenSSL 3.0.15, Visual Studio 2022 with MSVC 14.44 (x86 and x64) and CMake, .NET SDK 9 and TDM-GCC 10.3. So nothing needs installing for Go or C++.
- Missing: Rust.

**Who maintains it:** a small preservation community. The expected scale is dozens of concurrent players.

**Prior art:**
- PocketRelay: ME3 Blaze, Rust, MIT.
- grid-leak/blaze: Mirror's Edge Catalyst, Rust, AGPL-3.0.
- ReCap `recap_server`: Darkspore Blaze, RakNet and Lua host, C++, GPL-3.0.
- RakNet 4.081 is BSD-licensed (Oculus, 2014).

## Go's concrete blockers, checked

Two kinds of evidence here:
- **Runs:** a loopback handshake that mimics ProtoSSL (TLS ≤ 1.1, AES-SHA, no SNI) against the RSA-1024/e = 3/MD5 certificate from `make_certs.py`, plus a Go certificate-signing test. These were run for this ADR.
- **Docs:** the RakNet and Lua rows come from library documentation, not from runs.

| Need | In Go | Burden |
|---|---|---|
| Serve the legacy TLS handshake | **Works** (Go 1.24.1 `crypto/tls`): TLS 1.1 negotiated with AES128-SHA, RSA e = 3 key and MD5 certificate loaded. Requires explicit `MinVersion: VersionTLS10`, `MaxVersion: VersionTLS11` and a `CipherSuites` list, because since Go 1.22 the defaults exclude both (GODEBUG `tls10server`, `tlsrsakex`; explicit settings don't depend on them). | About 10 lines. None. |
| Generate the e = 3 key and the MD5-signed certificate | **Not in Go:** `x509.CreateCertificate` refuses with "x509: signing with MD5 is not supported", and `rsa.GenerateKey` always uses e = 65537. | Keep `make_certs.py` (Python plus the `openssl` CLI) as a one-time setup tool. Small. |
| Fire framing, TDF codec, the current handlers | Port of 584 Python lines, probably about 1,000 lines of Go including tests. The Python codec gives the golden bytes. | One work package (S1). |
| Check replies against the member tables | A Go test parses `docs/data/tdf_member_tables.txt` (text). | About 100 lines (S2). |
| Persistence | stdlib first (JSON files). SQLite would need `modernc.org/sqlite` (pure Go, no cgo), which would be the first third-party module. | None now. |
| RakNet 4.x (only if Go ever hosts the GMS session) | [`go-raknet`](https://github.com/Sandertv/go-raknet) (MIT) has a server side, but it targets Minecraft Bedrock: it hard-checks Bedrock's protocol version and uses connection cookies. It would need a fork once D2 knows Dawngate's protocol version and handshake. Untested against the client. | Small if the handshake matches (protocol byte, options). Otherwise our own reliability layer, roughly 1,500–2,500 lines. |
| Lua 5.1 with float `lua_Number` (clean-room route only) | gopher-lua can't load bytecode and uses float64. The only faithful option is cgo plus C Lua 5.1 built with `float` (cgo through TDM-GCC). | A C component and a cgo build step (moderate). The real cost of a clean room, however, is reimplementing about 450 bound host functions, and that cost is the same in any language. |
| Drive the client's simulation in-process (the D1 route) | **Not possible:** this means 32-bit `thiscall` C++ objects and the game's allocator inside `Dawngate.exe`. | C++ is forced, whatever language the services use. |

**Result:** for the services, Go's blockers are small and solved (TLS: verified; certificates: keep the Python setup tool). The only real cost is the one-package port of 584 lines. For the match server, Go adds no simplicity on the preferred D1 route, where the code must be C++ inside the game process and the client's own RakNet guarantees wire compatibility. On the clean-room route, Go would need a RakNet fork plus a cgo/C Lua. That's significant complexity, and ADR-2 weighs it only if D1 fails.

## Options

**Services (redirector, Blaze, HTTP, telemetry, persistence):**

| Option | For | Against |
|---|---|---|
| **A. Go 1.24, stdlib only** (port the stubs) | **The user's preference.** Legacy TLS verified. Static single binary for hosting. Headroom for performance and concurrency. Compiler-checked types for TDF structs. Fast builds (seconds). Already installed. | One package to port 584 lines. TDF knowledge in two languages: research decoding stays Python. Certificate generation stays Python. |
| B. Python 3.12 (grow the stubs) | Works today, nothing to port. Same language as the research tools. No build step. | Not the user's preference. Dynamic typing. Hosting needs a Python install. Legacy TLS relies on CPython keeping the deprecated `TLSVersion` members. |
| C. C# / .NET 9 | TLS verified on Windows 10 (SChannel). Typed. Installed. | New Windows releases ship TLS 1.0/1.1 off (admin fix per host), and Linux depends on the distro's OpenSSL policy; avoiding both means a BouncyCastle dependency. Full port. |
| D. Rust | Blaze prior art. Strongest typing. | rustls has no TLS 1.0/1.1 (PocketRelay hand-wrote SSLv3), so we'd need OpenSSL bindings on Windows. The toolchain needs a user-approved install. Slowest iteration loop. |
| E. C++ for everything | One language together with the match host. | The most code and bugs per feature for an RPC and persistence service. OpenSSL on Windows. |

**Match host:**
- **D1 route:** C++ in-process (forced). Its RakNet comes from the client's `sntransport`/`cTransportRakNet` (already accepts incoming connections) or from RakNet 4.081 linked in.
- **Clean room:** Go (go-raknet fork plus cgo Lua) or C++ (RakNet 4.081 plus C Lua). Decided in ADR-2 if needed.

## Decision

1. **Services: Go (go 1.24 in `go.mod`), standard library only.**
   - Covers the TLS redirector, the Blaze core and every Blaze component (including Dawngate's own), HTTP (web content, QoS), the telemetry sink, persistence (stdlib first), and later the control plane that starts matches and receives their results.
   - It replaces `re/stubs/blaze_stub.py` and `tdf.py`. The Python stub stays the reference until the Go server passes the same regression session.
2. **Python stays where it already works:**
   - research tools in `re/scripts/`;
   - the client patchers and test harness;
   - certificate generation (`make_certs.py`, because Go can't sign with MD5 or create e = 3 keys).

   The Python TDF codec `tdf.py` moves to `re/scripts/`, because `tdf_tables.py` imports it and it's useful for decoding captures.
3. **Match host: C++17, MSVC x86, CMake, in-process with the client's simulation** (the Q1 route, tested by D1).
   - By default, the GMS session shell, command intake and replication live in this host, using the client's own RakNet: it is the simplest option and has exact wire compatibility.
   - The Go services talk to the host over a small local control channel.
   - ADR-2 (after D1/D2) confirms this, or, if D1 fails, picks the clean-room language with the burden above.
4. **Layout:**
   ```
   server/                     product: Go module (stdlib only)
     go.mod                    module dawngate/server, go 1.24
     cmd/dgserver/             entry point; flags as blaze_stub.py today (--host, ports, --www, --certs, --log, --seconds)
     internal/tdf/             codec (+ member-table check in tests)
     internal/fire/            framing, dispatch
     internal/redirector/      legacy TLS + getServerInstance
     internal/blaze/           one file per component: util.go, authentication.go, usersessions.go, ...
     internal/web/             HTTP: web content, QoS
     internal/telemetry/       telemetry sink
     internal/store/           persistence (once needed)
     testdata/                 golden frames
     tools/make_certs.py       setup tool (Python, see Decision 2)
     bin/, certs/              gitignored: built binary, generated certificates
   gms/                        product, from D1 on: C++ match host (CMake)
   re/                         research only: analysis scripts, client patchers, test harness, Ghidra, logs.
                               re/stubs/ goes away after S1.
   docs/                       unchanged; decisions in docs/adr/
   ```
   S1 may rename packages, but not change the split.
5. **Rules:**
   - Product code doesn't depend on `re/`.
   - Tests may read `docs/data/` (member tables).
   - Each handler's comment names its client evidence (member table, function address).
   - `gofmt`, `go vet ./...` and `go test ./...` must pass.
   - Third-party Go modules only with a recorded reason.
   - The binary is built by `go build -o server\bin\dgserver.exe .\cmd\dgserver` (or by `run_session.ps1`).

## Consequences

**Positive:**
- B5 and the rest of M3 are built in Go in `server/`, right after S1.
- Hosting becomes a single binary, with no Python install needed for the services.

**Costs:**
- S1 is a port, not a move: one package, about 1,000 lines of Go including tests. The behaviour is pinned by golden bytes from the Python codec and by a regression session.
- Every later reply is written in Go, while the reference data (member tables) is produced by Python tools. They meet in a text file (`tdf_member_tables.txt`), so no code generator is needed.
- Three languages with clear roles: Go for the services, C++ in the game process, Python for research and setup. The control channel between Go and C++ stays small and documented (ADR-2).
- The match host is Windows/x86-only for as long as it reuses `Dawngate.exe`.

**Risk: legacy TLS in Go.** Explicit settings work today. Fallbacks, in order:
1. Pin the toolchain in `go.mod`.
2. Run the small, stateless redirector on Python's `ssl`, which has been proven with the real client.
3. Patch the client further. Whether ProtoSSL can be skipped for the redirector is untested.

**Revisit with a new ADR if:**
- Go's legacy TLS breaks without a simple fallback;
- D1 fails (ADR-2 then decides the clean-room language);
- the port turns out much bigger than estimated. Then record it under the failure rule, and keep Python for the services as option B.
