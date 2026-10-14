# Development

These pages are for people who want to build the project from source, run it from a checkout, or change it. Players and server operators need only the release: see [playing.md](../playing.md) and [hosting.md](../hosting.md).

| Page | What |
|---|---|
| [building.md](building.md) | Tools, the one-command setup, building the release, running the tests |
| [running-from-source.md](running-from-source.md) | Playing and serving from a checkout, test options for the match host, logs, LAN play, extra host machines |
| [decisions/](decisions/) | Design decisions: languages, the setup in Go, third-party Go modules |
| [../how-it-works.md](../how-it-works.md) | An overview of the pieces |

## Repository layout

| Folder | Contents |
|---|---|
| `server/` | Go: the server (`cmd/dgserver`, with its code in `internal/dgserver`), the release's `Start-Server.exe` and `Start-Client.exe` (`cmd/start-server`, `cmd/start-client`), the setup they run (`internal/setup`), and the Blaze services, accounts, matchmaker, website, tunnel and limits under `internal/`. |
| `gms/` | C++ (MSVC, x86, CMake): the match host `dgsim.dll`. `core/` holds the game-independent parts: network, protocol, session, control channel, and rules with unit tests. `engine/` holds what runs inside the game process, with the host's Lua scripts in `engine/lua/`. `host/` is the DLL shell. `script/` is a scripted test server. `tests/` holds the unit tests. |
| `re/scripts/` | Python and PowerShell: the developers' setup (`setup_all.py`), the client patchers, the play and test-session scripts, the release build (`make_release.ps1`), and tests. |
| `docs/` | These docs. `docs/data/` and `docs/file_manifest.csv` are data files that the code and tests read. |
| `Start-Server.cmd`, `Start-Client.cmd` | Double-click launchers for a checkout that has been set up (see [running-from-source.md](running-from-source.md)). |

Generated and downloaded things stay out of git: `client/` (the game copies), `tools/`, `.venv/`, `gms/build/`, `server/bin/`, `server/certs/`, `server/data/`, `server/logs/`, `re/logs/` and `re/out/`.

## Rules of thumb

- Run Python that reads game files with `python -I`. The archive is untrusted input.
- Never commit game files or anything derived from them.
- The release's Go setup must give the same result as the Python scripts, byte for byte. A change to a Python setup script goes into its Go port too, and tests check both.
