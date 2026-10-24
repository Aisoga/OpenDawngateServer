# Building from source

- [Tools](#tools)
- [Downloads](#downloads)
- [The setup command](#the-setup-command)
- [The same steps by hand](#the-same-steps-by-hand)
- [Building the release](#building-the-release)
- [The programs' icon](#the-programs-icon)
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
| Apache Flex SDK 4.16.1, a `playerglobal.swc` for Flash Player 11.1 and a JDK | optional | Only to change our three classes in the game's UI movies: the login screen's server picker (`client_ui\serverpicker\ServerPicker.as`), the CREATE GAME dialog's bot check box (`client_ui\matchoptions\BotCheckbox.as`) and the chat's rooms opened by default (`client_ui\chatrooms\AutoRooms.as`). Their compiled bytes are committed, so nothing else needs them. `re\scripts\build_picker.py build [picker\|matchoptions\|chatrooms]` compiles them and writes `client_ui\<folder>\<Class>.abc.hex` and `server\internal\setup\moviepatch\picker_tags.hex`, `matchoptions_tags.hex` or `chatrooms_tags.hex`; `check` tells whether they match the source. It looks in `tools\` (`flex_sdk_4.16.1`, `ffdec_26.3.0\flashlib\playerglobal32_0.swc`, `jdk-21.0.12.1+1`), or set `FLEX_SDK`, `PLAYERGLOBAL` and `JAVA`. |

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
| client copy | `client\run_854\Dawngate`, patched to use only `127.0.0.1` and to trust your certificate authority, with the login texts and the Leaderboard's rule (`game\Data\Locale\en-us\AA_Server.package`), the bot profiles (`game\Data\zz_BotProfiles.snap`) and the CREATE GAME dialog with its "Fill open seats with bots" check box (`game\Data\zz_MatchOptions.snap`) and the chat that opens the rooms the server marks (`game\Data\zz_Chat.snap`) and the map's Halloween decoration, shown when the server turns it on (`game\Data\zz_Halloween.snap`), and the second map's level files (`game\Data\zz_MM_Map_v3.snap`) |
| RakNet | `tools\raknet\RakNet` |
| builds | `gms\build` (the match host) and `server\bin\dgserver.exe` |
| host copy | `client\run_854_server\Dawngate` with `dgsim.dll` installed as `PhysXLoader.dll`, its settings file `game\dgsim.ini`, the bot profiles and the second map's level files |
| content | `server\data\content.json`, what accounts can own, built from your game files (`--no-content` skips it) |

The bot profiles are BotClass assets the game doesn't ship, such as a Warden that plays the Predator role. `re\scripts\bot_profiles.py` makes `zz_BotProfiles.snap` from the copy's own `AssetData.snap` (a catalog with the new entries and their files); nothing of the game's data is committed. A copy without it still plays: the match host then runs the Predator on the Warden's own profile and corrects the shown role after every update.

The bot check box is our own ActionScript class (`client_ui\matchoptions\BotCheckbox.as`) added to the dialog's movie: `re\scripts\patch_login_movie.py --spec matchoptions` takes the movie from the copy's own `Battlegrounds.snap`, adds the class's four committed tags and writes `zz_MatchOptions.snap` (the release's setup writes the same bytes). Ticked, it ends the game's name with `~#bots`, which the server reads. The same class shows the dialog's map list when the copy has a map besides The Dawngate: the step also writes `UI/Scaleform/Battlegrounds/dgmaps.json` into `zz_MatchOptions.snap`, listing the maps whose archives are in the copy's `game\Data` (today `zz_MM_Map_v3.snap`), so it runs after the MM_Map_v3 step and rewrites the archive when a map's archive comes or goes. The list has a fixed size (1 KB, stored as Snappy literals only), because `Start-Client.exe` writes the maps of the server a login goes to into the same place while the game runs; the setup's check counts such a list as current, and the setup writes the copy's own list again.

The chat rooms the server marks as open by default are opened by another class of ours (`client_ui\chatrooms\AutoRooms.as`) in the chat's movie: `re\scripts\patch_login_movie.py --spec chatrooms` takes the movie from the copy's own `Global.snap` and writes `zz_Chat.snap` the same way. Once per login it joins every listed room that the server puts in a marked category (an id of 1000 or more) and leaves its window minimised in the bottom bar, without taking the keyboard focus; with a server that marks no room it does nothing.

The game ships a Halloween decoration for its map that its level never lists. `re\scripts\halloween.py` makes `zz_Halloween.snap` from the copy's own `AssetData.snap`: the level lists the decoration's markerset under a level flag, so the game loads it only when the server sends that flag at the match start (`Start-Server.ini`'s `halloween`, `dgserver --halloween`). Every player copy gets it; the match host's copy doesn't need it. `re\scripts\test_halloween.py` and the Go package `server\internal\setup\halloween` pin the same bytes.

The game also holds a cut one-lane map, `MM_Map_v3` (shown to players as Anshavar Pass), without the files its level names. `zz_MM_Map_v3.snap` holds them, all made from the copy's own game files, mostly from the map's line-of-sight image: the two navigation graphs, the physics mesh, the fog's wall outlines, the minimap and the ground model with its two textures (`server\internal\setup\mapmm` and its sub-packages, `server\internal\setup\dds`). The one exception is the physics mesh in its cooked form, which only PhysX's own cooker can make: it is our data (our triangles, the walls traced from the line-of-sight image, plus the cooker's search tree), committed as hex text in `server\internal\setup\mapmm\physmesh\MM_Map_v3_bin.hex`, embedded in the programs with its SHA-256 pinned, and used only when the copy's line-of-sight image has the walls it was cooked from. `physcook.exe` (`gms\physcook`, built with the match host) is the developers' tool that cooks it again over the game's own `PhysXCooking.dll`; the release doesn't ship it. The developers' setups run the release's code for the archive, `go run ./tools/mapmm <copy>` from `server\`, so every copy holds the same bytes as a release install. Every player copy and the host copy get it, whatever map the server hosts.

The first run takes a few minutes. **Run it again after every update of your checkout.** It rebuilds what changed and skips the rest in a few seconds. `--check` only reports. The script downloads nothing, installs no tool and never changes firewall rules.

## The same steps by hand

```powershell
python -I re\scripts\extract_client.py Dawngate.zip client\ia_Dawngate
robocopy client\ia_Dawngate\Dawngate client\run_854\Dawngate /E /COPY:DAT /DCOPY:T
.venv\Scripts\python -I server\tools\make_certs.py server\certs 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_client.py client\run_854\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_config.py client\run_854\Dawngate\game\Data
.venv\Scripts\python -I re\scripts\patch_locale.py client\run_854\Dawngate\game\Data
.venv\Scripts\python -I re\scripts\bot_profiles.py client\run_854\Dawngate\game\Data
cmake -S gms -B gms\build -G "Visual Studio 17 2022" -A Win32 [-DRAKNET_ROOT=<RakNet folder>]
cmake --build gms\build --config Release
go build -C server -o bin\dgserver.exe .\cmd\dgserver
.venv\Scripts\python -I re\scripts\setup_server_copy.py client\run_854\Dawngate client\run_854_server\Dawngate gms\build\Release\dgsim.dll
.venv\Scripts\python -I re\scripts\bot_profiles.py client\run_854_server\Dawngate\game\Data
cd server; go run ./tools/mapmm ..\client\run_854\Dawngate; go run ./tools/mapmm ..\client\run_854_server\Dawngate; cd ..
server\bin\dgserver.exe --make-content client\ia_Dawngate\Dawngate
```

After making new certificates, run `patch_client.py` again: the client must carry the matching certificate authority.

## Building the release

1. Run the setup above, so that `gms\` has a Release build.
2. Build the release:

   ```powershell
   powershell -ExecutionPolicy Bypass -File re\scripts\make_release.ps1
   ```

   This writes `re\out\release\<name>\` and `<name>.zip`, named `OpenDawngateServer-<date>-<commit>`, or the name given with `-Name` (a numbered release uses its tag, for example `-Name v0.6`). The name is also the programs' version: `Start-Client.exe` plays only on servers built with the same name. The release contains:

   - `Start-Server.exe` and `Start-Client.exe`;
   - `host\dgsim.dll` with its Lua scripts;
   - `README.txt`, the players' manual from `re\scripts\release\README.txt`;
   - `LICENSE.txt` and `THIRD-PARTY-NOTICES.txt`;
   - `BUILD.txt`, which names the source commit and every file's SHA-256.

   Beside it, the script writes the Linux server: `re\out\release\<name>-linux\` and `<name>-linux.tar.gz`, with one top folder `<name>-linux/` inside. It contains:

   - `start-server`, built for `linux/amd64` with the same version name as the Windows programs (players' `Start-Client.exe` connects only to a server of its own version);
   - the same `host/dgsim.dll` and Lua scripts;
   - `dawngate.service`, the systemd unit, and `README.txt`, the operator's manual from `re\scripts\release\README-Linux.txt`;
   - `LICENSE.txt`, `THIRD-PARTY-NOTICES.txt` (for the modules in `start-server`) and `BUILD.txt`.

   Its text files have LF line ends. The script writes the tar archive itself, so the modes don't depend on the file system it is built on: owner and group 0, `start-server` and the folders 0755, every other file 0644. It then reads the archive back with Windows' own `tar.exe` and checks the modes, the owner and every file's bytes. `-NoLinux` skips the Linux server and `-NoZip` skips both archives.

The script refuses to include any game file. It checks each file by name and by checksum against both archive builds.

A release is a binary distribution under the AGPL: whoever hands it out also offers the source of the commit in its `BUILD.txt`.

## The programs' icon

`Start-Client.exe` and `Start-Server.exe` carry the project's icon, `server\cmd\assets\dawngate.ico` (16, 32 and 48 pixels, 32-bit). It gets into them without any outside tool: each of the two commands has a `rsrc_windows_amd64.syso` beside its code, a small COFF object holding the icon as Windows resources (`RT_ICON` and `RT_GROUP_ICON`). The Go linker links every `.syso` file of a package into the program, and the `_windows_amd64` in the name limits that to Windows x64 builds. So every build for Windows has the icon, whether it is a plain `go build`, `make_release.ps1` or CI, and the Linux builds don't see the files. `make_release.ps1` checks that both programs hold every image of the `.ico`.

To change the icon, replace `server\cmd\assets\dawngate.ico` and run `go generate ./cmd/...` in `server\`. That runs `server\tools\icosyso` (it uses only Go's standard library), which rewrites both `.syso` files; commit them with the icon. `go test ./...` fails while the committed `.syso` files don't match the `.ico` (`server\internal\winres`). By hand: `go run ./tools/icosyso -ico <icon.ico> -o <command folder>\rsrc_windows [-arch amd64,386]` writes `rsrc_windows_<arch>.syso` per architecture. The release builds only Windows x64, so only that file is committed.

## Running the tests

| What | How |
|---|---|
| Server and setup (Go) | `go test ./...` in `server\`. Tests that need the game's files find them in `client\ia_Dawngate` and are skipped without them. |
| Match host (C++) | `gms\build\Release\gms_tests.exe` and `gms\build\core\control\Release\dg_control_tests.exe`, after the build |
| Host Lua scripts | `.venv\Scripts\python -I re\scripts\bots_lua_check.py` (needs LuaJ and a JDK) |
| Setup and scripts | `re\scripts\test_*.py` with `.venv\Scripts\python -I`, and `test_*.ps1` with `powershell -ExecutionPolicy Bypass -File` |
| Release setup against the Python setup | `re\scripts\test_p3b.py` compares a release install with the developers' setup, file by file: a `Start-Server.exe` folder (with the tunnel only its host copy and certificates; `Start-Client.exe` makes the player copy), a player's folder, or both; with `--picker` (and `--tunnel-address`, `--data-suffix`) the player copy patched for the tunnel and the server picker that `Start-Client.exe` adds to the login screen (`re\scripts\patch_login_movie.py` makes the reference); every player copy's bot check box (`zz_MatchOptions.snap`) and chat rooms class (`zz_Chat.snap`) are compared too |
| A live check | `play.ps1 -Check` (see [running-from-source.md](running-from-source.md#the-check-mode)) |

Before submitting Go changes, also run `gofmt -l .` and `go vet ./...` in `server\`.

## Continuous integration

The public repository builds itself with GitHub Actions (`.github/workflows/build.yml`) on every push to `main`, on every pushed version tag, every Monday, and on demand from the *Actions* tab. On a Windows runner it:

1. downloads RakNet 4.081 and LuaJ and checks their SHA-256;
2. runs `gofmt`, `go vet` and `go test` in `server/`;
3. builds the match host and runs `gms_tests` and `dg_control_tests`;
4. runs the Lua check;
5. builds the release zip and the Linux archive with `make_release.ps1`.

Beside it, a Linux job on `ubuntu-24.04` runs `gofmt`, `go vet` and `go test` in `server/` with the same Go version and `GOTOOLCHAIN=local`, then builds the Linux programs `dgserver` and `start-server` (`GOOS=linux GOARCH=amd64`, without cgo). `start-client` stays Windows-only: it starts the Windows game. Tests that need Wine or a host copy skip themselves there, as the ones that need the game's files do. To run the same checks on a Linux machine, use Go from `server/go.mod` and run those commands in `server/`.

A third job, also on `ubuntu-24.04`, checks the Linux archive the Windows job built: one top folder, owner and group 0, the modes, no file outside the layout, LF text files, every file's SHA-256 as its `BUILD.txt` lists it, and that `start-server --version` prints the release's name.

A successful build of `main` (every job green) replaces the **`nightly`** pre-release on the *Releases* page with its zip and its Linux archive. It is a convenience for testers: unsigned, and not tested in the game, because CI has no game files. Tests that need them skip themselves there. The numbered releases stay the stable ones.

### Numbered releases

A numbered release is made by pushing a tag to the public repository; CI does the rest:

```powershell
git tag v0.6 <commit on main>
git push <public remote> v0.6
```

- The tag's name is `v<major>.<minor>` or `v<major>.<minor>.<patch>` (`v0.6`, `v1.0.1`). Each new release takes a higher version than every earlier one, so the newest release is always the highest version. A tag of another form fails the build before anything is built.
- The tagged commit must already be on `main` (push `main` first, then the tag), and it must contain the tag trigger of `build.yml`.
- CI builds the tagged commit with `make_release.ps1 -Name <tag>`, so the programs' version and the files are named after the tag, runs every job, and then publishes the release `<tag>` on the *Releases* page with `<tag>.zip` and `<tag>-linux.tar.gz`. It is not a pre-release; GitHub marks the newest version *Latest*. A run that isn't green publishes nothing, and an existing release of the same name is never replaced.
- A manual run of a tag from the *Actions* tab builds it without publishing.
- The release text is a short fixed note about the files; what changed is added on the release page afterwards.
- Players' `Start-Client.exe` of an earlier numbered release offers the new one at its next start, once it is *Latest*: it needs the asset `<tag>.zip` with a `BUILD.txt` that lists every file's SHA-256 and names the tag as its version, exactly as `make_release.ps1` writes it ([reference.md](../reference.md#start-clientexe-and-start-clientini), *Updates*). Builds that aren't numbered releases (the nightly, a local build) never offer or take an update.
