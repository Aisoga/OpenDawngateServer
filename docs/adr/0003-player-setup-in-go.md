# ADR-3: The player and operator setup in Go

- **Status:** accepted, 2026-10-09. It amends [ADR-1](0001-emulator-language.md)'s decision 2, which kept the client patchers and the certificate generation in Python.
- **Decision in one sentence:** the setup a player or an operator runs (check and unpack `Dawngate.zip`, patch the exe and the config packages, make the certificates, install the match host into its copy, build the player content) is ported to Go in `server/internal/setup/`, so that two Go programs and the archive are all a player needs; the Python scripts stay the reference and the research and test tools.

## Context

- **The user's request (2026-10-09):** other people should need only `Dawngate.zip` and two programs (`Start-Server.exe`, `Start-Client.exe`). Today a player needs Python with `.venv` (pefile, cryptography), Go, Visual Studio (x86 C++), Git for Windows' `openssl`, optionally Wireshark, and runs `setup_all.py` and `play.ps1`.
- **What ADR-1 assumed:** Go "can't create e = 3 keys or sign with MD5", so `make_certs.py` stayed a Python plus `openssl` setup tool. That is true of `crypto/rsa.GenerateKey` (always e = 65537) and `crypto/x509.CreateCertificate` (refuses MD5), not of Go: the key (two primes from `crypto/rand`, d = 3⁻¹ mod lcm(p−1, q−1)), the certificate's DER (`encoding/asn1`) and the PKCS#1 v1.5 signature with MD5's DigestInfo (`math/big`) are about 300 lines with the standard library, and `crypto/tls` already serves such a certificate (ADR-1's check, the redirector since S1).
- **The rest is byte work:** the exe patches are fixed bytes at fixed addresses of one build (PE section arithmetic, no disassembler), the config packages are DBPF files, the archive check is a zip and a manifest. None of it needs a third-party module.
- `dgsim.dll` (our C++ match host) can be shipped pre-built, so nobody needs Visual Studio; `dgserver.exe` is one Go binary already.

## Decision

1. The setup steps a player or an operator needs are ported to Go, standard library only, in subpackages of `server/internal/setup/` (`archive`, `exepatch`, `certs`, `hostcopy`, then the config packages and the content). The launchers that call them, `Start-Server.exe` and `Start-Client.exe`, come on top.
2. **Python stays the reference.** `setup_all.py`, `patch_client.py`, `patch_config.py`, `make_certs.py`, `setup_server_copy.py` and the content tools keep working and keep their tests; the research scripts, the test harness (`run_session.ps1`, the lanes) and the developers' setup keep using them.
3. **The Go ports must give the Python output byte for byte**, checked by tests: the patched exe's SHA-256 per profile pinned in the Go tests (run where the game's exe is available, skipped elsewhere), the installed host files compared with `setup_server_copy.py`'s, the config packages likewise. For the certificates, which are random per install, the structure is compared field by field with `make_certs.py`'s (subject, issuer, extensions, serial and date rules) and the signatures are verified.
4. A change to a Python setup script is carried into its Go port in the same package (or the Go test that pins it fails).
5. **Certificates are generated per install**, never shipped or shared: whoever holds a CA key can impersonate any server to every client patched with that CA.

## Consequences

- Two implementations of the setup, held together by tests; the Python side is still where new patches are found and tried first.
- A player's machine needs no Python, Go, Visual Studio or `openssl`; an operator's neither, unless they build from source.
- The release carries our binaries (`dgserver`/the launchers, `dgsim.dll`) and their licence notices (OPERATORS.md *Distribution and licences*), never a game file.
