# Environment setup

The tools a machine needs to play or to run the server. Everything else (the game files from the archive.org download, the patched client copy, the match host copy, the certificates, the builds of `server/` and `gms/`, the content file) is made by one command, `re\scripts\setup_all.py`: [PLAYING.md](PLAYING.md) *One-time setup* for a player, [OPERATORS.md](OPERATORS.md) *Setting up a machine* for a server.

## Tools

The versions are the ones on the development PC (Windows 10 Home, x64).

| Tool | Version | Purpose |
|---|---|---|
| Python | 3.12.7 | setup, the patchers, the session and play scripts. A virtual environment `.venv` with `pefile` and `cryptography`: `python -m venv .venv`, then `.venv\Scripts\pip install pefile cryptography`. |
| Go | 1.24.1 | builds the server: `go build -o bin\dgserver.exe .\cmd\dgserver` in `server/` (`setup_all.py` and `run_session.ps1` do it); tests: `go test ./...` |
| Visual Studio 2022 | 17.14 (MSVC 14.44) | *Desktop development with C++* with the **x86** compiler. Builds the match host with its bundled CMake (not on the PATH: `Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe`): `cmake -S gms -B gms\build -G "Visual Studio 17 2022" -A Win32`, then `cmake --build gms\build --config Release` (`setup_all.py` does it). |
| Git for Windows | any current | its `openssl.exe` (`C:\Program Files\Git\usr\bin\openssl.exe`, OpenSSL 1.1.1) signs the certificates with MD5 for `server/tools/make_certs.py`; Python's `cryptography` refuses MD5. |
| RakNet | 4.081 (BSD-3 plus patent grant): <https://github.com/facebookarchive/RakNet> commit `1a169895a900c9fc4841c556e16514182b75faf8` (no tags) | the match host's transport, built for protocol 5 (the client's) by `gms/CMakeLists.txt` (`gms/cmake/raknet.cmake`). Archive <https://github.com/facebookarchive/RakNet/archive/1a169895a900c9fc4841c556e16514182b75faf8.zip>, SHA-256 `60b53557aa084fb5455c8e2e7efe309738dabe7f9db5c0bc103cf41c8f97c0e0`; `setup_all.py` extracts it to `tools\raknet\RakNet\` (gitignored). Configure by hand with `-DRAKNET_ROOT=<that folder>`. |
| Wireshark with Npcap (optional) | 4.6.8 | the capture and egress check of every session (`dumpcap`). Npcap's free installer can't run silently: download it from <https://npcap.com/#download> and tick **"Support loopback traffic capture"**, since the server runs on localhost. Without it, play with `-NoCapture`. |
| LuaJ and a Java runtime (optional) | LuaJ 2.0.3 (`luaj-jse-2.0.3.jar`, MIT), JDK 21 | only for `re\scripts\bots_lua_check.py`, which compiles the match host's Lua files and runs their unit tests. LuaJ: <https://repo1.maven.org/maven2/org/luaj/luaj-jse/2.0.3/luaj-jse-2.0.3.jar> (SHA-256 `02464b0a6cf90fa404d0b62ebb587b96a38e118420a7adc467c1550f889fa41b`) into `tools\luaj\`, a JDK under `tools\` or given with `--java`. |

The game ships its DirectX 9 and Visual C++ 2010 runtime DLLs next to its exe, so nothing else needs installing for it.

`tools/` and `.venv/` are not in git; downloads may go to `tools\_downloads\` or your Downloads folder.
