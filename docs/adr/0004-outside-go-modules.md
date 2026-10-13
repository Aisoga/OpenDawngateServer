# ADR-4: Pinned outside Go modules, named one by one

- **Status:** accepted, 2026-10-13 (for public hosting). It amends [ADR-1](0001-emulator-language.md)'s "Go (stdlib only)" for `server/` and its persistence row ("SQLite would need `modernc.org/sqlite` (pure Go, no cgo), which would be the first third-party module").
- **Decision in one sentence:** `server/` may use an outside Go module where the standard library has no answer, but only one named in this ADR, pinned by `server/go.sum`, pure Go (no cgo), with its licences shipped in the release's `THIRD-PARTY-NOTICES.txt`.

## Context

- **Public hosting** needs three things the standard library doesn't have: a database for the account store (the JSON store rewrites the whole file per change, about 4.6 KB per account, fine to a few hundred accounts, too slow at 10,000+, and has no transactions an admin page or a web form could rely on), password hashing with a memory-hard function (argon2id), and certificates from Let's Encrypt for the website (ACME with the TLS-ALPN-01 challenge, so only port 443 is forwarded).
- **Writing them ourselves** is the wrong trade: SQLite is 250,000 lines of C (its pure-Go translation is what `modernc.org/sqlite` is), argon2 and ACME are security code whose mistakes don't show in tests.
- **What ADR-1 and ADR-3 promised** still has to hold: one static Go binary per program (`dgserver.exe`, `Start-Server.exe`, `Start-Client.exe`), no C compiler on any machine (the release and `setup_all.py` build with `CGO_ENABLED` unset or 0 and `GOTOOLCHAIN=local`), a player or operator needs nothing but the release and `Dawngate.zip`.
- `modernc.org/sqlite` is SQLite 3 transpiled to Go: no cgo, builds for windows/amd64 and windows/386, used widely (e.g. by Go projects that ship one static binary). What the programs link from it are ten modules: modernc's own `sqlite`, `libc`, `mathutil` and `memory`, four small ones (`dustin/go-humanize`, `mattn/go-isatty`, `ncruces/go-strftime`, `remyoudompheng/bigfft`) and `golang.org/x/sys`, `golang.org/x/exp` (`google/uuid` is in `go.mod` only for modernc's tests); all BSD-3-Clause or MIT, SQLite itself public domain.

## Decision

1. **Allowed modules (named one by one):**
   - `modernc.org/sqlite` (the account store), with what it requires;
   - `golang.org/x/crypto` (`argon2` for the password hashes, `acme` for the website's certificates).
   Any other outside module needs a new ADR (or an amendment of this one) with its reason.
2. **Pinned:** `server/go.mod` names the exact version and `server/go.sum` the hashes (the Go checksum database checked them at the first download). Updates are deliberate: `go get <module>@<version>` with `GOTOOLCHAIN=local`, then the full `go test ./...`. A version whose `go.mod` needs a newer Go than the project's (go 1.24 today) is not taken: `modernc.org/sqlite v1.46.1` (2026-02-18) is the last that builds with Go 1.24 (v1.46.2 and later need 1.25).
3. **Pure Go only:** nothing that needs cgo, a C compiler or a DLL beside the binary.
4. **Licences in the release:** `re/scripts/make_release.ps1` lists every outside module the two programs link (`go list -deps`) and copies each module's licence files (`LICENSE`, `PATENTS`, `SQLITE-LICENSE`, ...) from the module cache into `THIRD-PARTY-NOTICES.txt`; a module without one stops the build.
5. **The builds stay as they were:** `setup_all.py`'s `go build`, `run_session.ps1`'s builds, `server/internal/setup` (which builds nothing) and `make_release.ps1` need no new step. The first build on a machine fetches the modules from the Go module proxy (`proxy.golang.org`, the default) into the module cache; later builds work offline. A machine that must build without the network gets the cache filled once (`go mod download` in `server/`).
6. **No vendoring:** `go mod vendor` would put about 150 MB of generated per-platform Go into git (modernc's `lib/sqlite_<os>_<arch>.go` files); the module cache and `go.sum` give the same reproducibility.

**Taken so far:** `modernc.org/sqlite v1.46.1`; `golang.org/x/crypto v0.48.0` (2026-02-09: `argon2` for the password hashes; the last version whose `go.mod` asks for Go 1.24, v0.49.0 and later need 1.25), which raised `golang.org/x/sys` from v0.37.0 to v0.41.0 (go 1.24.0) for both; fetched with `GOTOOLCHAIN=local go get golang.org/x/crypto@v0.48.0`, then the full `go test ./...`. The release's notices gained x/crypto's LICENSE and PATENTS (41 KB in all). The website uses `golang.org/x/crypto/acme` (the ACME client, standard library imports only) with a small certificate manager of its own (`server/internal/site/acme.go`) instead of `acme/autocert`, because autocert imports `golang.org/x/net/idna`, a module this ADR doesn't name; no new module.

## Consequences

- `dgserver.exe` grows from 10.7 MB to 17.0 MB, `Start-Server.exe` (release build, stripped) to 12.2 MB.
- The account store has transactions, a versioned schema with migrations and no whole-file rewrites; the password hashes and the website's account pages are a migration each.
- A first build needs the network once; `GOTOOLCHAIN=local` still prevents a toolchain download.
- The release's notices grow by the modules' licence texts (about 40 KB).
- The trust in the code now includes these modules' authors; the pin and the checksum database protect against a changed version, not against a bad one we choose. Updates are therefore rare and reviewed (decision 2).
