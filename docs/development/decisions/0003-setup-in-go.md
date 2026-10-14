# Decision 3: The player and operator setup in Go

- **Status:** accepted (2026-10-09). Amends [decision 1](0001-languages.md), which kept the client patchers and the certificate generation in Python.
- **In short:** the setup a player or operator runs is ported to Go in `server/internal/setup/`, so that two programs and the game's archive are all anyone needs. This covers checking and unpacking `Dawngate.zip`, patching the game's exe and config packages, making the certificates, installing the match host and building the player content. The Python scripts stay as the reference and as the developers' tools.

## Context

- **The goal:** other people should need only `Dawngate.zip` and two programs, `Start-Server.exe` and `Start-Client.exe`. Before this, a player needed Python with two packages, Go, Visual Studio, Git's `openssl` and optionally Wireshark, and ran two scripts.
- **Certificates in Go:** decision 1 noted that Go "can't make exponent-3 keys or sign with MD5". That's true of `crypto/rsa.GenerateKey` and `crypto/x509.CreateCertificate`, but not of Go itself. The key (two primes from `crypto/rand`, d = 3⁻¹ mod lcm(p−1, q−1)), the certificate's DER (`encoding/asn1`) and the PKCS#1 v1.5 signature with MD5 (`math/big`) take about 300 lines with the standard library. `crypto/tls` already serves such a certificate.
- **The rest is byte work:** the exe patches are fixed bytes at fixed addresses of one build; the config packages are DBPF archives; the archive check is a zip and a list of checksums. None of it needs a third-party module.
- **`dgsim.dll`** (the match host) can be shipped pre-built, so nobody needs Visual Studio.

## Decision

1. The setup steps are ported to Go, in subpackages of `server/internal/setup/`: `archive`, `exepatch`, `certs`, `hostcopy`, the config packages and the content. The two programs call them.
2. **Python stays the reference.** `setup_all.py`, `patch_client.py`, `patch_config.py`, `make_certs.py`, `setup_server_copy.py` and the content tools keep working and keep their tests. The developers' setup and test harness keep using them.
3. **The Go ports must produce the Python output byte for byte**, checked by tests:
   - the patched exe's checksum for each profile is pinned in the Go tests (run where the game's exe is available);
   - the installed host files and the config packages are compared with the Python scripts' output;
   - the certificates, which are random per install, are compared field by field, and their signatures are verified.
4. A change to a Python setup script goes into its Go port in the same change.
5. **Certificates are generated per install** and never shipped or shared: whoever holds a certificate authority's key can impersonate any server to every client that trusts it.

## Consequences

- There are two implementations of the setup, held together by tests. New patches are still found and tried in Python first.
- A player's or operator's PC needs no Python, Go, Visual Studio or `openssl`, unless they build from source.
- The release carries this project's binaries and their license notices, and never a game file.
