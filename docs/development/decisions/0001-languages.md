# Decision 1: Languages and repository layout

- **Status:** accepted (2026-10-07). Amended by [decision 3](0003-setup-in-go.md) (the release's setup in Go) and [decision 4](0004-outside-go-modules.md) (a few pinned Go modules).
- **In short:**
  - The network services are written in **Go**, in `server/`.
  - The match host is written in **C++** (MSVC, x86), in `gms/`, and runs inside the game's own process.
  - **Python** is used for the developers' setup, the client patchers and the test scripts.

## Context

**What the server has to speak:**

1. **The redirector**, the client's first contact: TLS on port 42127. The client's TLS library negotiates at most TLS 1.1, offers only RSA key exchange (AES128/256-SHA, RC4-SHA, RC4-MD5) and sends no server name. Our certificate is RSA-1024 with public exponent 3, MD5-signed by the certificate authority patched into the client. Everything after the redirector is plain TCP.
2. **Blaze**, EA's online platform: a binary framing with a tagged data format (TDF), request/reply calls and notifications, at a low rate. The layout of every reply comes from the client's own type tables, which are kept in `docs/data/tdf_member_tables.txt`. Also needed: HTTP (web content, the connection-quality test), a telemetry sink and persistent accounts.
3. **The match server:** RakNet 4.x over UDP, with several dozen game message types. The game's simulation is in the client. The plan was to run it in server mode inside `Dawngate.exe` (32-bit, MSVC 2010). The alternative, a server written from scratch, would also need Lua 5.1 with 4-byte floats to run the game's server scripts.

**Who maintains it:** a small preservation community. The expected scale is dozens of concurrent players.

**Prior art:** PocketRelay (Mass Effect 3's Blaze, Rust, MIT), grid-leak/blaze (Mirror's Edge Catalyst, Rust, AGPL-3.0), ReCap's `recap_server` (Darkspore's Blaze, RakNet and Lua host, C++, GPL-3.0). RakNet 4.081 is BSD-licensed.

## Go's obstacles, checked

| Need | In Go | Cost |
|---|---|---|
| Serve the old TLS handshake | **Works** with `crypto/tls`: TLS 1.1 with AES128-SHA, an exponent-3 RSA key and an MD5 certificate. Needs explicit `MinVersion`, `MaxVersion` and `CipherSuites`, because Go's defaults exclude both. | About 10 lines |
| Make the exponent-3 key and the MD5 certificate | `x509.CreateCertificate` refuses MD5 and `rsa.GenerateKey` always uses exponent 65537. | At first, a Python setup tool (`make_certs.py`). Later done in Go by hand (decision 3). |
| The framing, the TDF codec, the handlers | A port of the existing Python prototype, about 1,000 lines of Go with tests. The Python codec provides golden test bytes. | One work package |
| Check replies against the client's type tables | A Go test reads the text table. | About 100 lines |
| Persistence | The standard library first (JSON files); SQLite later (decision 4). | None at first |
| Drive the game's simulation in-process | **Not possible in Go:** 32-bit C++ objects and the game's own memory allocator inside `Dawngate.exe`. | C++ is required, whatever the services use. |

For the services, Go's obstacles are small and solved. For the match host, Go adds nothing: code that runs inside the game process must be C++.

## Options for the services

| Option | For | Against |
|---|---|---|
| **Go** | The maintainer's preference. Old TLS verified. One static program file for hosting. Typed. Fast builds. | One port of the prototype. Certificates first stay in Python. |
| Python | Works already, nothing to port. | Dynamic typing. Hosting needs a Python install. Old TLS relies on deprecated CPython features. |
| C# / .NET | TLS works on Windows. Typed. | New Windows versions switch TLS 1.0/1.1 off system-wide. |
| Rust | Blaze prior art. Strong typing. | No TLS 1.0/1.1 in rustls, so OpenSSL bindings would be needed. Slowest iteration. |
| C++ for everything | One language with the match host. | The most code per feature for a request/reply service. |

## Decision

1. **Services: Go,** with the standard library (plus the modules of decision 4). This covers the redirector, Blaze and all its components, HTTP, telemetry, persistence and the control channel to the match hosts.
2. **Python** remains for the developers' setup, the client patchers and the test harness.
3. **Match host: C++17, MSVC x86, CMake,** running in-process with the game's simulation. It links RakNet 4.081 built for the client's protocol version. The Go server talks to the match hosts over a small local control channel (JSON lines over TCP).
4. **Rules:**
   - Product code (`server/`, `gms/`) doesn't depend on `re/`.
   - Tests may read `docs/data/`.
   - Each handler's comment names the client evidence it's based on.
   - `gofmt`, `go vet ./...` and `go test ./...` must pass.
   - Third-party Go modules only with a recorded reason (decision 4).

## Consequences

- Hosting is a single program file, and no Python install is needed for the services.
- Three languages with clear roles: Go for the services, C++ inside the game process, Python for setup and tests.
- The reference data (the client's type tables) is produced once and kept as a text file, so no code generator is needed.
- The match host is Windows/x86-only for as long as it reuses `Dawngate.exe`.
- **Risk: old TLS in Go.** Explicit settings work today. Fallbacks, in order: pin the toolchain; run the small redirector on Python's `ssl`; patch the client further.
