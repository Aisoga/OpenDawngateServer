# Building from source

- [Tools](#tools)
- [Downloads](#downloads)
- [The setup command](#the-setup-command)
- [The same steps by hand](#the-same-steps-by-hand)
- [Building the release](#building-the-release)
- [Running the tests](#running-the-tests)
- [Continuous integration](#continuous-integration)

## Tools

Windows 10 or 11, x64, with about 6 GB of free disk space for the setup.

| Tool | Version used | What for |
|---|---|---|
| Python | 3.12 | The setup, the patchers and the play and test scripts. Create a virtual environment in the repository folder: `python -m venv .venv`, then `.venv\Scripts\pip install pefile cryptography`. |
| Go | 1.24 | The server and the release's programs. Keep `GOTOOLCHAIN=local`. The first build downloads the pinned modules (`modernc.org/sqlite`, `golang.org/x/crypto`; checked against `server/go.sum`). Later builds work offline. |
| Visual Studio 2022 | 17.14 (MSVC 14.44) | *Desktop development with C++* with the **x86** compiler. Its bundled CMake builds the match host. CMake isn't on the PATH; it's at `Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe`. |
| Git for Windows | any | Its `openssl.exe` signs the developers' certificates with MD5, which Python's `cryptography` refuses. |
| Wireshark with Npcap | optional | Captures each play session so the egress check can prove nothing left the PC. Install Npcap with **"Support loopback traffic capture"**. Without it, play with `-NoCapture`. |
| LuaJ 2.0.3 and a JDK | optional | Only for `re\scripts\bots_lua_check.py`, which compiles the match host's Lua files and runs their tests. Put `luaj-jse-2.0.3.jar` ([download](https://repo1.maven.org/maven2/org/luaj/luaj-jse/2.0.3/luaj-jse-2.0.3.jar), SHA-256 `02464b0a6cf90fa404d0b62ebb587b96a38e118420a7adc467c1550f889fa41b`) into `tools\luaj\`. |

The game brings its own DirectX 9 and Visual C++ 2010 runtimes, so nothing else needs to be installed for it.

## Downloads

Two downloads, never committed. Put them in the repository folder or leave them in your Downloads folder.

- **`Dawngate.zip`** from <https://archive.org/details/Dawngate>: build 1.0.903.854, 1,310,588,859 bytes, SHA-256 `56ae93939904605ac3145cb55ace854482f40bf128991a0a21ba56f14d4bdae1`.
- **RakNet 4.081**, commit `1a169895a900c9fc4841c556e16514182b75faf8`: <https://github.com/facebookarchive/RakNet/archive/1a169895a900c9fc4841c556e16514182b75faf8.zip> (SHA-256 `60b53557aa084fb5455c8e2e7efe309738dabe7f9db5c0bc103cf41c8f97c0e0`). The build applies this project's patches (`gms/cmake/raknet_patches`) to a copy, so the extracted folder stays unmodified.

## The setup command

From the repository folder:

```powershell
python -m venv .venv
.venv\Scripts\pip install pefile cryptography
.venv\Scripts\python -I re\scripts\setup_all.py [--zip <Dawngate.zip>] [--raknet-zip <RakNet zip>]
```

`setup_all.py` runs these steps and checks each result:

| Step | Result |
|---|---|
| archive | `client\ia_Dawngate`: the game extracted from `Dawngate.zip`, every file checked against [`file_manifest.csv`](../file_manifest.csv). It is never modified afterwards. |
| certificates | `server\certs`: a certificate authority and server certificate for `127.0.0.1` |
| client copy | `client\run_854\Dawngate`, patched to use only `127.0.0.1` and to trust your certificate authority |
| RakNet | `tools\raknet\RakNet` |
| builds | `gms\build` (the match host) and `server\bin\dgserver.exe` |
| host copy | `client\run_854_server\Dawngate` with `dgsim.dll` installed as `PhysXLoader.dll` and its settings file `game\dgsim.ini` |
| content | `server\data\content.json`, what accounts can own, built from your game files (`--no-content` skips it) |

The first run takes a few minutes. **Run it again after every update of your checkout.** It rebuilds what changed and skips the rest in a few seconds. `--check` only reports. The script downloads nothing, installs no tool and never changes firewall rules.

## The same steps by hand

```powershell
python -I re\scripts\extract_client.py Dawngate.zip client\ia_Dawngate
robocopy client\ia_Dawngate\Dawngate client\run_854\Dawngate /E /COPY:DAT /DCOPY:T
.venv\Scripts\python -I server\tools\make_certs.py server\certs 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_client.py client\run_854\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_config.py client\run_854\Dawngate\game\Data
cmake -S gms -B gms\build -G "Visual Studio 17 2022" -A Win32 [-DRAKNET_ROOT=<RakNet folder>]
cmake --build gms\build --config Release
go build -C server -o bin\dgserver.exe .\cmd\dgserver
.venv\Scripts\python -I re\scripts\setup_server_copy.py client\run_854\Dawngate client\run_854_server\Dawngate gms\build\Release\dgsim.dll
server\bin\dgserver.exe --make-content client\ia_Dawngate\Dawngate
```

After making new certificates, run `patch_client.py` again: the client must carry the matching certificate authority.

## Building the release

1. Run the setup above, so that `gms\` has a Release build.
2. Build the release:

   ```powershell
   powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1
   ```

   This writes `re\out\release\<name>\` and `<name>.zip`, named `Dawngate-Restoration-<date>-<commit>`. The release contains:

   - `Start-Server.exe` and `Start-Client.exe`;
   - `host\dgsim.dll` with its Lua scripts;
   - `README.txt`, the players' manual from `re\scripts\release\README.txt`;
   - `LICENSE.txt` and `THIRD-PARTY-NOTICES.txt`;
   - `BUILD.txt`, which names the source commit and every file's SHA-256.

The script refuses to include any game file. It checks each file by name and by checksum against both archive builds.

A release is a binary distribution under the AGPL: whoever hands it out also offers the source of the commit in its `BUILD.txt`.

## Running the tests

| What | How |
|---|---|
| Server and setup (Go) | `go test ./...` in `server\`. Tests that need the game's files find them in `client\ia_Dawngate` and are skipped without them. |
| Match host (C++) | `gms\build\Release\gms_tests.exe` and `gms\build\core\control\Release\dg_control_tests.exe`, after the build |
| Host Lua scripts | `.venv\Scripts\python -I re\scripts\bots_lua_check.py` (needs LuaJ and a JDK) |
| Setup and scripts | `re\scripts\test_*.py` with `.venv\Scripts\python -I`, and `test_*.ps1` with `powershell -ExecutionPolicy Bypass -File` |
| Release setup against the Python setup | `re\scripts\test_p3b.py` compares a release install with the developers' setup, file by file |
| A live check | `play.ps1 -Check` (see [running-from-source.md](running-from-source.md#the-check-mode)) |

Before submitting Go changes, also run `gofmt -l .` and `go vet ./...` in `server\`.

## Continuous integration

The public repository builds itself with GitHub Actions (`.github/workflows/build.yml`) on every push to `main`, every Monday, and on demand from the *Actions* tab. On a Windows runner it:

1. downloads RakNet 4.081 and LuaJ and checks their SHA-256;
2. runs `gofmt`, `go vet` and `go test` in `server/`;
3. builds the match host and runs `gms_tests` and `dg_control_tests`;
4. runs the Lua check;
5. builds the release zip with `make_release.ps1`.

A successful build of `main` replaces the **`nightly`** pre-release on the *Releases* page with its zip. It is a convenience for testers: unsigned, and not tested in the game, because CI has no game files. Tests that need them skip themselves there. The numbered releases stay the stable ones.
