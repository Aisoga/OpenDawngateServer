# Dawngate Restoration

> **Unofficial fan project.** It is not affiliated with, endorsed by or sponsored by Electronic Arts or Waystone Games. Dawngate is a trademark of its respective owners. This repository contains no game files: get the client yourself from archive.org (<https://archive.org/details/Dawngate>, build 1.0.903.854) and set it up with the release's `Start-Server.exe` (*Play it* below) or as [`docs/PLAYING.md`](docs/PLAYING.md) describes.

> **Hosting on the internet is at your own risk.** At home (this PC or your LAN) the release needs no extra setup. For a server strangers can reach, `Start-Server.exe`'s public profile turns on what the project has for it: accounts with checked passwords (argon2id hashes, never written to a log) and a sign-up mode of your choice (invite codes by default), an encrypted tunnel that carries all of a player's traffic on one port (TLS 1.3 and AES-GCM, the server's key pinned by every `Start-Client.exe`), limits on connections, logins, games and packets, a strict check of every message a client sends to a match, and an HTTPS website for registration, password changes, account deletion and data download, with an admin page. What remains: the game is 2014 code (no ASLR) and trusts the server it plays on, so players should play only on servers they trust and use a password they use nowhere else; without the tunnel (a LAN server) the game's own traffic is unencrypted; the operator keeps personal data (login names, the players' addresses in the logs, match histories) and is responsible for updates, backups and data-protection duties. A public address starts only after you accept a risk notice (`--i-accept-the-risk`, or `i_accept_the_risk = yes` in `Start-Server.ini`); forward only TCP and UDP 42128 and TCP 443, never TCP 10040 ([`docs/OPERATORS.md`](docs/OPERATORS.md) *A public server*).

## License

This repository's own code and documentation are licensed under the GNU Affero General Public License, version 3 only (AGPL-3.0-only): see [`LICENSE`](LICENSE). Copyright (C) 2026 the Dawngate Restoration contributors.

The license covers only this repository's own code and docs. It doesn't cover the game, its files or its content, which belong to their owners and aren't included here. Third-party components keep their own licenses: RakNet 4.081 (BSD, with its patent grant), which you download separately ([`docs/SETUP.md`](docs/SETUP.md)) and which the build patches with this project's fixes (`gms/cmake/raknet_patches`, this project's code); the Go modules `server/` uses ([ADR-4](docs/adr/0004-outside-go-modules.md)): `modernc.org/sqlite` and the modules it needs (BSD-3-Clause and MIT; SQLite itself is public domain) and `golang.org/x/crypto` (BSD-3-Clause), fetched by the Go toolchain. The release's `THIRD-PARTY-NOTICES.txt` carries all their licence texts.

## About

A preservation project: a server for the Dawngate client (late 2014, build 1.0.903.854), so the game can be played again after the official servers shut down in 2015. The original client connects to it after a few patches to a copy of it (the server's address and our certificate authority), and a practice match against bots, PvP matches between players, the collection, the store and progression work.

### Play it: the release (no tools needed)

1. Download `Dawngate.zip` (build 1.0.903.854, 1.3 GB) from <https://archive.org/details/Dawngate>, and the release zip
   `Dawngate-Restoration-<date>-<commit>.zip` from this repository's Releases page (or build it, below).
2. Unpack the release into a folder of its own and put `Dawngate.zip` next to `Start-Server.exe`.
3. Double-click `Start-Server.exe`. Its first start asks who will play (press Enter for only this PC), checks the zip
   against the archive's hashes and sets everything up in `Dawngate-Data` next to it (about half a minute and 3.1 GB:
   a copy of the game to play and one for the match host, certificates made for your install, the player content),
   then runs the server. Leave its window open.
4. Double-click `Start-Client.exe`, log in with a login name and a password of 8 or more characters (the first login
   with a new name creates the account), PLAY, PRACTICE DAWNGATE, pick a shaper, and a practice match against bots
   begins.
5. To play together on your home network, answer 2 at that question (or `Start-Server.exe --configure`, or set
   `address = <this PC's LAN address>` in `Start-Server.ini`); the other players run `Start-Client.exe <that address>`
   and compare the fingerprint it shows with the server window's.
6. To host on the internet, answer 3: the public profile asks for your internet address, who may make accounts,
   the website's domain (optional) and an admin account, and shows the risk notice ([`docs/OPERATORS.md`](docs/OPERATORS.md)
   *A public server*). Players on it register on its website and play with `Start-Client.exe <address>`, which goes
   through the encrypted tunnel by itself.

Windows 10 or 11, 64-bit; nothing else to install. The release's `README.txt` has the details (ports, SmartScreen,
the firewall, accounts and the website, the options). The release contains no game file: every player needs their own `Dawngate.zip`.
Its `BUILD.txt` names the commit of this repository it was built from (the source, AGPL-3.0).

**Building the release yourself** (needs the developers' setup of `docs/SETUP.md`: Go, Visual Studio 2022 with the
x86 compiler, RakNet 4.081): build `gms\` (`cmake --build gms\build --config Release`), then
`powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1`, which writes `re\out\release\<name>\` and
`<name>.zip` and refuses to include any file of the game.

### The repository

- [`docs/PLAYING.md`](docs/PLAYING.md): **play it yourself.** One command (`re\scripts\play.ps1`) starts the server and the client and cleans up afterwards. Also: the one-time setup (one command from the archive.org download, `re\scripts\setup_all.py`), what to click, what works in a practice match today, where the logs go, troubleshooting, and a client for a server on another machine of your network.
- [`docs/OPERATORS.md`](docs/OPERATORS.md): **run a server for others.** The release's `Start-Server.exe` (its questions, `Start-Server.ini`, a public server with the tunnel, the website, the admin page, backups and what remains your risk), accounts, passwords and invite codes, the limits, setting up a machine with `setup_all.py` (what it makes and checks, and the same by hand), `dgserver` with match hosts, LAN certificates, extra host machines (agent mode), ports and firewall, what must never be exposed, and the licence notes for binaries (RakNet's BSD notice and this project's patches, the Go modules' licences; no EA files are shipped).
- [`docs/SETUP.md`](docs/SETUP.md): the tools a machine needs, with their versions and downloads.
- [`docs/adr/0001-emulator-language.md`](docs/adr/0001-emulator-language.md): why the services are in Go, the match host in C++ and the tooling in Python.
- [`docs/adr/0003-player-setup-in-go.md`](docs/adr/0003-player-setup-in-go.md): why the player and operator setup is ported to Go as well (the release's two programs need no Python, Go or Visual Studio), with the Python scripts as the reference it is tested against byte for byte.
- [`docs/adr/0004-outside-go-modules.md`](docs/adr/0004-outside-go-modules.md): why `server/` may use a few pinned outside Go modules (SQLite in pure Go for the account store, `golang.org/x/crypto` for password hashes and the website's certificates), named one by one, with their licences in the release.
- [`docs/file_manifest.csv`](docs/file_manifest.csv): size and SHA-256 of every file of the archive.org builds; `setup_all.py` checks the download against it.

## Layout

- `server/`: the services in Go (the standard library plus the pinned modules of ADR-4): the TLS redirector, the Blaze server (login with checked passwords, accounts in SQLite, collection, store, matchmaking, parties, chat), the encrypted tunnel's gateway, the website and its admin page, the rate and count limits, web content, QoS and telemetry, and the supervision of the match hosts; the release's `Start-Server.exe` and `Start-Client.exe` (`server/cmd/start-server`, `server/cmd/start-client`) with the setup in Go (`server/internal/setup`: the archive check, the exe and config patchers, the certificates, the match-host install, the player content). `go test ./...` in `server/`.
- `gms/`: the match host in C++ (MSVC, x86, CMake): a DLL that runs the shipped game's own simulation as the server of one match (one `Dawngate.exe` per match, headless), with RakNet 4.081 (patched at build time: `gms/cmake/raknet_patches`) for the clients, network limits and a strict check of every client message, and a JSON Lines control channel to `server/`. `gms/tests` (`gms_tests.exe`) and `gms/core/control/tests` test the parts that run without the game.
- `re/scripts/`: setup (`setup_all.py`, `setup_lane.py`, `setup_server_copy.py`, `extract_client.py`), the client and config patchers (`patch_client.py`, `patch_config.py`, `patch_locale.py` for the login screen's texts and links), the session and play scripts (`play.ps1`, `play_lan.ps1`, `run_session.ps1`, `launch_monitor.ps1`), the egress check (`egress_report.py`), helpers for the game's archives (`snap.py`, `dbpf.py`, `assetdata.py`), the release build (`make_release.ps1`, with the player's `release/README.txt`), and their tests (`test_*.py`, `test_*.ps1`).
- `docs/data/`: the few tables the code, its tests and the setup read.

Game files aren't included in this repository. Get them from <https://archive.org/details/Dawngate>. Dawngate is © Electronic Arts.
