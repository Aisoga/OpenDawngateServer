# Dawngate Restoration

> **Unofficial fan project.** It is not affiliated with, endorsed by or sponsored by Electronic Arts or Waystone Games. Dawngate is a trademark of its respective owners. This repository contains no game files: get the client yourself from archive.org (<https://archive.org/details/Dawngate>, build 1.0.903.854) and set it up as [`docs/PLAYING.md`](docs/PLAYING.md) describes.

> **Not for the open internet.** The server accepts any login (any e-mail and password open or create an account), keeps the accounts in a plain JSON file, and isn't hardened against hostile clients or traffic. Run it for yourself or a group you trust, on a LAN or a private network, never as a public server ([`docs/OPERATORS.md`](docs/OPERATORS.md) says what must never be exposed).

## License

This repository's own code and documentation are licensed under the GNU Affero General Public License, version 3 only (AGPL-3.0-only): see [`LICENSE`](LICENSE). Copyright (C) 2026 the Dawngate Restoration contributors.

The license covers only this repository's own code and docs. It doesn't cover the game, its files or its content, which belong to their owners and aren't included here. Third-party components keep their own licenses, for example RakNet 4.081 (BSD), which you download separately ([`docs/SETUP.md`](docs/SETUP.md)).

## About

A preservation project: a server for the Dawngate client (late 2014, build 1.0.903.854), so the game can be played again after the official servers shut down in 2015. The original client connects to it after a few patches to a copy of it (the server's address and our certificate authority), and a practice match against bots, PvP matches between players, the collection, the store and progression work.

- [`docs/PLAYING.md`](docs/PLAYING.md): **play it yourself.** One command (`re\scripts\play.ps1`) starts the server and the client and cleans up afterwards. Also: the one-time setup (one command from the archive.org download, `re\scripts\setup_all.py`), what to click, what works in a practice match today, where the logs go, troubleshooting, and a client for a server on another machine of your network.
- [`docs/OPERATORS.md`](docs/OPERATORS.md): **run a server for others.** Setting up a machine with `setup_all.py` (what it makes and checks, and the same by hand), `dgserver` with match hosts, LAN certificates, extra host machines (agent mode), ports and firewall, what must never be exposed, and the licence notes for binaries (RakNet's BSD notice; no EA files are shipped).
- [`docs/SETUP.md`](docs/SETUP.md): the tools a machine needs, with their versions and downloads.
- [`docs/adr/0001-emulator-language.md`](docs/adr/0001-emulator-language.md): why the services are in Go, the match host in C++ and the tooling in Python.
- [`docs/file_manifest.csv`](docs/file_manifest.csv): size and SHA-256 of every file of the archive.org builds; `setup_all.py` checks the download against it.

## Layout

- `server/`: the services in Go (standard library only): the TLS redirector, the Blaze server (login, accounts, collection, store, matchmaking, parties, chat), web content, QoS and telemetry, and the supervision of the match hosts. `go test ./...` in `server/`.
- `gms/`: the match host in C++ (MSVC, x86, CMake): a DLL that runs the shipped game's own simulation as the server of one match (one `Dawngate.exe` per match, headless), with RakNet 4.081 for the clients and a JSON Lines control channel to `server/`. `gms/tests` (`gms_tests.exe`) and `gms/core/control/tests` test the parts that run without the game.
- `re/scripts/`: setup (`setup_all.py`, `setup_lane.py`, `setup_server_copy.py`, `extract_client.py`), the client and config patchers (`patch_client.py`, `patch_config.py`), the session and play scripts (`play.ps1`, `play_lan.ps1`, `run_session.ps1`, `launch_monitor.ps1`), the egress check (`egress_report.py`), helpers for the game's archives (`snap.py`, `dbpf.py`, `assetdata.py`), and their tests (`test_*.py`, `test_*.ps1`).
- `docs/data/`: the few tables the code, its tests and the setup read.

Game files aren't included in this repository. Get them from <https://archive.org/details/Dawngate>. Dawngate is © Electronic Arts.
