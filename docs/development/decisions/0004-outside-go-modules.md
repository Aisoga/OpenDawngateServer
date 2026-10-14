# Decision 4: Pinned outside Go modules, named one by one

- **Status:** accepted (2026-10-13, for public hosting). Amends [decision 1](0001-languages.md)'s "standard library only" for `server/`.
- **In short:** `server/` may use an outside Go module where the standard library has no answer. Such a module must be named in this decision, pinned by `server/go.sum` and written in pure Go (no cgo), and its licenses ship in the release's `THIRD-PARTY-NOTICES.txt`.

## Context

- **Public hosting needs three things the standard library doesn't have:**
  - a database for the accounts. The JSON store rewrote the whole file on every change: fine for a few hundred accounts, too slow at 10,000, and without transactions;
  - password hashing with a memory-hard function (argon2id);
  - certificates from Let's Encrypt for the website (ACME with the TLS-ALPN-01 challenge, so that only port 443 needs forwarding).
- **Writing these ourselves would be the wrong trade.** SQLite is 250,000 lines of C, and argon2 and ACME are security code whose mistakes don't show up in tests.
- **What the earlier decisions promised still holds:** one static program file each, no C compiler on any machine, and nothing but the release and `Dawngate.zip` for players and operators.
- **`modernc.org/sqlite`** is SQLite 3 translated to Go: no cgo, and it builds for 32- and 64-bit Windows. The programs link ten modules through it, all BSD-3-Clause or MIT; SQLite itself is public domain.

## Decision

1. **Allowed modules:**
   - `modernc.org/sqlite`, for the account store, with what it requires;
   - `golang.org/x/crypto`, for `argon2` (password hashes) and `acme` (the website's certificates).

   Any other module needs an amendment to this decision, with its reason.
2. **Pinned.** `server/go.mod` names the exact version, and `server/go.sum` holds the checksums.
   - Updates are deliberate: `go get <module>@<version>` with `GOTOOLCHAIN=local`, then the full `go test ./...`.
   - A version that needs a newer Go than the project's (1.24) isn't taken. That makes `modernc.org/sqlite v1.46.1` and `golang.org/x/crypto v0.48.0` the latest usable versions.
3. **Pure Go only:** nothing that needs cgo, a C compiler or an extra DLL.
4. **Licenses in the release.** `make_release.ps1` lists every outside module the programs link and copies each module's license files into `THIRD-PARTY-NOTICES.txt`. A module without one stops the build.
5. **No new build steps.** The first build on a machine downloads the modules into Go's module cache, and later builds work offline. `go mod download` in `server/` fills the cache in advance.
6. **No vendoring.** `go mod vendor` would add about 150 MB of generated code to git, and the module cache plus `go.sum` give the same reproducibility.

The website's certificates use `golang.org/x/crypto/acme` with a small certificate manager of its own (`server/internal/site/acme.go`) instead of `acme/autocert`, because `autocert` would need `golang.org/x/net`, which isn't on the list.

## Consequences

- `dgserver.exe` grows from 10.7 MB to 17.0 MB.
- The account store has transactions and a versioned schema with migrations.
- A first build needs the network once.
- The release's notices grow by the modules' license texts, about 40 KB.
- We now also trust these modules' authors. The pins and Go's checksum database protect against a changed version, not against a bad one we choose, so updates are rare and reviewed.
