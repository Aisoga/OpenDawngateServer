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
| Apache Flex SDK 4.16.1, a `playerglobal.swc` for Flash Player 11.1 and a JDK | optional | Only to change our five classes in the game's UI movies: the login screen's server picker (`client_ui\serverpicker\ServerPicker.as`), the CREATE GAME dialog's bot check box (`client_ui\matchoptions\BotCheckbox.as`), the chat's rooms opened by default (`client_ui\chatrooms\AutoRooms.as`), the After Action Review's tabs (`client_ui\aftaction\AfterActionTabs.as`) and the item shop's refund (`client_ui\itemshop\ShopRefund.as`). Their compiled bytes are committed, so nothing else needs them. `re\scripts\build_picker.py build [picker\|matchoptions\|chatrooms\|aftaction\|itemshop]` compiles them and writes `client_ui\<folder>\<Class>.abc.hex` and `server\internal\setup\moviepatch\picker_tags.hex`, `matchoptions_tags.hex`, `chatrooms_tags.hex`, `aftaction_tags.hex` or `itemshop_tags.hex`; `check` tells whether they match the source. It looks in `tools\` (`flex_sdk_4.16.1`, `ffdec_26.3.0\flashlib\playerglobal32_0.swc`, `jdk-21.0.12.1+1`), or set `FLEX_SDK`, `PLAYERGLOBAL` and `JAVA`. |

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
| client copy | `client\run_854\Dawngate`, patched to use only `127.0.0.1` and to trust your certificate authority, with the login texts and the Leaderboard's rule (`game\Data\Locale\en-us\AA_Server.package`), the bot profiles (`game\Data\zz_BotProfiles.snap`) and the CREATE GAME dialog with its "Fill open seats with bots" check box (`game\Data\zz_MatchOptions.snap`) and the chat that opens the rooms the server marks (`game\Data\zz_Chat.snap`) and the After Action Review with its tabs (`game\Data\zz_AfterActionReview.snap`) and the item shop with its refund (`game\Data\zz_ItemShop.snap`) and the map's Halloween decoration, shown when the server turns it on (`game\Data\zz_Halloween.snap`), and the second map's level files (`game\Data\zz_MM_Map_v3.snap`) |
| RakNet | `tools\raknet\RakNet` |
| builds | `gms\build` (the match host) and `server\bin\dgserver.exe` |
| host copy | `client\run_854_server\Dawngate` with `dgsim.dll` installed as `PhysXLoader.dll`, its settings file `game\dgsim.ini`, the bot profiles and the second map's level files |
| content | `server\data\content.json`, what accounts can own and the match items, built from your game files (`--no-content` skips it) |
| collection items | `client\run_854\Dawngate\game\Data\zz_CollectionItems.snap`, the Collection's *Items* category: the front end's movie from the copy's own `code.snap` with our class (`client_ui\collection`), and the item list from `content.json` (after the content step; `Start-Client.exe` makes the list from the copy's own game files) |

The bot profiles are BotClass assets the game doesn't ship, such as a Warden that plays the Predator role. `re\scripts\bot_profiles.py` makes `zz_BotProfiles.snap` from the copy's own `AssetData.snap` (a catalog with the new entries and their files); nothing of the game's data is committed. A copy without it still plays: the match host then runs the Predator on the Warden's own profile and corrects the shown role after every update.

The bot check box is our own ActionScript class (`client_ui\matchoptions\BotCheckbox.as`) added to the dialog's movie: `re\scripts\patch_login_movie.py --spec matchoptions` takes the movie from the copy's own `Battlegrounds.snap`, adds the class's four committed tags and writes `zz_MatchOptions.snap` (the release's setup writes the same bytes). Ticked, it ends the game's name with `~#bots`, which the server reads. The same class shows the dialog's map list when the copy has a map besides The Dawngate: the step also writes `UI/Scaleform/Battlegrounds/dgmaps.json` into `zz_MatchOptions.snap`, listing the maps whose archives are in the copy's `game\Data` (today `zz_MM_Map_v3.snap`), so it runs after the MM_Map_v3 step and rewrites the archive when a map's archive comes or goes. The list has a fixed size (1 KB, stored as Snappy literals only), because `Start-Client.exe` writes the maps of the server a login goes to into the same place while the game runs; the setup's check counts such a list as current, and the setup writes the copy's own list again.

The chat rooms the server marks as open by default are opened by another class of ours (`client_ui\chatrooms\AutoRooms.as`) in the chat's movie: `re\scripts\patch_login_movie.py --spec chatrooms` takes the movie from the copy's own `Global.snap` and writes `zz_Chat.snap` the same way. Once per login it joins every listed room that the server puts in a marked category (an id of 1000 or more) and leaves its window minimised in the bottom bar, without taking the keyboard focus; with a server that marks no room it does nothing.

The After Action Review's tabs are a fourth class (`client_ui\aftaction\AfterActionTabs.as`) in the review's movie: `re\scripts\patch_login_movie.py --spec aftaction --host <the copy's server>` takes the movie from the copy's own `Battlegrounds.snap` and writes `zz_AfterActionReview.snap` the same way, with one more file in it, `UI/Scaleform/Battlegrounds/dgweb.json`: the web root of the server the copy is set up for (`http://<host>:18080/battlegrounds_test/live/www/client/1/common`, the root the config packages give the game's web content). After a match the review shows the tabs SCOREBOARD, MATCH STATS and RECENT GAMES; the last two show the server's `history.html` in the game's own web view. A copy set up for another server gets the archive written again.

The item shop's refund is a fifth class (`client_ui\itemshop\ShopRefund.as`) in the shop's movie: `re\scripts\patch_login_movie.py --spec itemshop` takes the movie from the copy's own `HUD.snap` and writes `zz_ItemShop.snap` the same way. The server takes the sale of an item bought during the current visit to the base as an undo of that purchase; the class shows it: a small gold mark on such an item, and the shop's Sell turned into Refund with the full price when it is selected. Its three texts are lines the client texts step adds to the HUD's string table.

The Collection's *Items* category is a sixth class (`client_ui\collection\CollectionItems.as`) in the front end's own movie: `re\scripts\patch_login_movie.py --spec collection --content server\data\content.json` takes `UI/Scaleform/CDN/swf/code/UIRuntime.gfx` from the copy's own `code.snap` and writes `zz_CollectionItems.snap` the same way, with one more file in it, `UI/Scaleform/Collection/dgitems.json`: the match items a player can buy (name, title, kind, cost, group and the icon's texture path, then each item's stat lines, passive and active texts, the items it builds from and into, and its tags), ordered by kind, cost and title, after the shop's filter tags. The class adds the category to the Collection's menu, shows the items as the Collection's own cards and, on a click, an item's details with its route; the icons are the game's own textures, which the front end has loaded. The release's setup makes the same list from the copy's own game files (the content the server's setup writes), so a player copy needs no server file; `re\scripts\test_picker.py` and the Go package `server\internal\setup\moviepatch` pin the same bytes on made-up inputs.

The game ships a Halloween decoration for its map that its level never lists. `re\scripts\halloween.py` makes `zz_Halloween.snap` from the copy's own `AssetData.snap`: the level lists the decoration's markerset under a level flag, so the game loads it only when the server sends that flag at the match start (`Start-Server.ini`'s `halloween`, `dgserver --halloween`). Every player copy gets it; the match host's copy doesn't need it. `re\scripts\test_halloween.py` and the Go package `server\internal\setup\halloween` pin the same bytes.

The game also holds a cut one-lane map, `MM_Map_v3` (shown to players as Anshavar Pass), without the files its level names. `zz_MM_Map_v3.snap` holds them, all made from the copy's own game files, mostly from the map's line-of-sight image: the two navigation graphs, the physics mesh, the fog's wall outlines, the minimap and the ground model with its two textures (`server\internal\setup\mapmm` and its sub-packages, `server\internal\setup\dds`). The one exception is the physics mesh in its cooked form, which only PhysX's own cooker can make: it is our data (our triangles, the walls traced from the line-of-sight image, plus the cooker's search tree), committed as hex text in `server\internal\setup\mapmm\physmesh\MM_Map_v3_bin.hex`, embedded in the programs with its SHA-256 pinned, and used only when the copy's line-of-sight image has the walls it was cooked from. `physcook.exe` (`gms\physcook`, built with the match host) is the developers' tool that cooks it again over the game's own `PhysXCooking.dll`; the release doesn't ship it. The developers' setups run the release's code for the archive, `go run ./tools/mapmm <copy>` from `server\`, so every copy holds the same bytes as a release install. Every player copy and the host copy get it, whatever map the server hosts.

New maps are map packages (one folder per map with its `map.json`, text only). No package comes with the code or the release: `maps\` holds only its README in git and ignores everything else, so the packages you copy or make there stay on your disk and out of the repository, and the test maps the tests load are fixtures in `server\testdata\maps` (found by `server\internal\testmaps`; never installed or shipped). The map compiler `mapc` (`server\internal\setup\mapc`, its command line `server\internal\mapctool`) turns a package into the map's added archive `zz_<name>.snap` from the copy's own game files, as `mapmm` does for `MM_Map_v3`. From a checkout you run it as `go run ./tools/mapc <command> …` in `server\`; the release runs the same code as `Start-Client.exe --mapc <command> …`, with the same arguments and byte for byte the same output, so a program can use the compiler without Go. `mapc version --json` prints `{"release", "format", "api"}`: the release (as `--version` prints it), the map format the compiler reads and the version of the command line itself, which goes up when a command's arguments, exit codes or JSON change in a way an existing caller would notice. `check` runs its rule checks and names the place in `map.json` each failure concerns, `preview` draws the derived layers into a folder, `build` makes the archive (the level's files, and its Level, markersets and terrain noun copied from the main map's assets and moved; `--json` also lists the catalog entries the copy's catalog needs for them and the map's label); each takes `--json` for programs. Every map gets the same physics mesh, a flat floor without walls (a match never asks the physics mesh about a wall), cooked once and committed as hex text in `server\internal\setup\mapc\shared_bin.hex`, its SHA-256 pinned; `mapc cook-shared <physcook.exe> <PhysXCooking.dll>` cooks it again and checks the bytes. A map's ground is either `tiled` (repeated earth and grass) or `baked`: its `ground` section then lists layers (a tileable texture each, drawn through a mask made of automatic rules such as the high ground or the slopes, shapes and an optional grey PNG in the package), stamps (pictures laid onto the ground), a cliff material for the steep faces and decals; the compiler paints them into 2 × 2 (or 1 × 1, 4 × 4) DXT1 tiles of 1024, 2048 or 4096 texels that the ground model maps from above, and draws the minimap from the same bake (`server\internal\setup\mapc\groundtex.go`, the painting in `mapc\bake`). An optional `void` section sets what the map shows past its ground (`server\internal\setup\mapc\void.go`): `cut` ends the ground that many metres past the walkable edge, `backdrop` lays a plane under the level (its height, a colour or texture, lit or unlit), `mist` a see-through fog layer above it (its height, range and colour) and `fog` the environment's height fog for this map; since the game clears no colour buffer, a cut needs a backdrop, which `check` enforces, and a mist must lie below the walkable floor. The test map CM_Pass (`server\testdata\maps\CM_Pass`) uses it. With the copy, `check` also warns about a prop whose top lies on the floor (it would draw torn) and proposes a height that lifts it. Shipped textures are named by archive and path and read from the copy; a package may carry its own PNGs. `mapc bake <package> <copy> <folder>` writes the bake, the minimap and each layer's mask as PNG pictures without building the archive; `--full` also writes every tile at its own size as `tile_<i>_<j>.png` (i from the west, j from the south; exactly the tile's texture, its outer 1/256 on each side the neighbour's ground), to paint the ground in another program. Listed west to east, then south to north as the baked ground's `picture`, such tiles (each exactly the tile's size) become the ground under the layers: every layer is then drawn over them through its mask, nothing of the picture is mirrored, and the edge strips over a neighbour tile are taken from that neighbour, so an exported picture imported unchanged bakes the same tiles. The tiles are big files and stay out of git (as everything in `maps\` but its README); a package with a picture needs them beside its `map.json`, and `check` names a missing one. `model`, `ground` and `scene` export the copy's models, a package's ground and a scene list (what a level or a package places where, and which model each thing draws) as glTF 2.0 with PNG textures into a folder you name (`server\internal\setup\{bmdl,depot,scene}`); that output is the game's art in another format, so keep it out of the repository. A package can place the game's own props by name (`props`: single nouns, some of them blocking where they stand, and groups lifted from a shipped level's dressing; `scatter`: props spread by a seed along the walls); `props <package> <copy>` lists every prop a package places, `nouns <copy>` the nouns a prop may name with their sizes, and `icon <copy> <noun>... <out>` bakes top-down pictures of their models (game content again, kept out of the repository). `check --copy` and `preview --copy` size the blocking props from the copy's nouns, as `build` does. `import --copy <copy> <level> <package>` writes a package from a level the game ships: its size, lanes, base, wells, camps and bushes from the level's markers, its walls traced from its line-of-sight map into polygons for the first team's half (the compiler mirrors the second), and a report of how far the level's own second team lies from that mirror and how many line-of-sight cells the package gets different. With `--dress` it also takes the level's dressing (every markerset that holds rocks, plants, lights or effects, the level's own ground excepted) as prop groups over the whole level, reaching up to 100 m past its edges for its backdrop, each a group with a polygon region (`"group": {"level", "sets", "at", "polygon"}`) and `"mirror": false`, so every marker stands once, where the level has it, on both halves (a level's hand-placed dressing is only roughly symmetric; any prop with `"mirror": false` stands once, on either half), and the report lists the markers they leave out (a set's gameplay markers, such as a tower set's Bindings beside their platforms) and how many of the second team's a mirror would have missed; `go run ./tools/mapc import --dress --copy <copy> MM_Map_v3 ..\maps\CM_Pass` made the test map CM_Pass (the level's gold mines stay on islands only their workers walk: a shape of type `island` is ground in the NPC graph and out of the Player graph, and a well on it gets the level's NPC-only island marker, so no shaper reaches it and the bots leave it alone; `check` refuses an island well the Player graph comes within 12 m of and warns within 39 m; and they stay the level's mines: a well of `"kind": "mine"` is placed as `MM_Map_v3`'s gold mine with its 36 worker spots, against the main map's well with 18, which a well without a kind gets). The navigation graphs are squares of 2 to 4 map cells: the compiler takes the smallest that keeps each graph under 7,000 areas, unless the map's `graph` section sets it (`{"cell": 2}`, at most 10,000 areas a graph); the smaller the square, the nearer the walkable ground reaches to the walls. `mapc import` keeps MM_Map_v3's own squares of 2 cells, and `go run ./tools/mapfit --copy <copy> <package> <folder>` draws where a package imported from MM_Map_v3 walks differently from the level. The setup installs every package of the maps folder (the checkout's `maps\` for the developers' setups, whatever you copied into it; `go run ./tools/mapsetup --maps ..\maps [--client] <copy>` from `server\`, which also writes the MM_Map_v3 archive, the bot profiles with the maps' catalog entries and, for a player copy, the create dialog's map list), and `dgserver --maps <folder>` lets `--map` and `--custom-maps` name them:

```powershell
cd server; go run ./tools/mapc check testdata\maps\CM_Ring; go run ./tools/mapc preview testdata\maps\CM_Ring ..\re\out\preview; cd ..
```

The map editor ([Making maps](../playing.md#making-maps)) has two parts. Its page (HTML and JavaScript, the templates, the players' guide) lives in the map editor's own repository and comes to players as a zip they unpack into the release folder. Its backend is in this repository, `server\internal\mapedit`: an `http.Handler` that serves the page from a folder, lists, opens and saves the map packages of a maps folder (keeping a package's layout and leaving a delivered package read-only), and runs `mapc` as a child process for the checks, the previews, the prop palette, the 3-D view's exports and Build. It checks the page's `editor.json` (`{"version", "api"}`) against its own interface version and, instead of a page made for another one, serves an error page naming both versions; it answers only a loopback `Host` (no DNS rebinding) and refuses a `POST` from another `Origin`. `Start-Client.exe --editor` runs it with the release's folders and gives it *Test* (`server\internal\launcher\editor.go`: the game closed, This PC's server started again with `--map`, the player copy set up again, the game started). For working on the page or the backend without the game, `server\tools\mapedit` runs the same backend without *Test*:

```powershell
cd server; go run ./tools/mapedit -pages <the page folder> -maps <a folder of map packages> -copy <a game copy> -open; cd ..
```

`-pages` is a checkout of [the map editor's repository](https://github.com/Aisoga/OpenDawngateMapEditor) (its `web` folder with `templates` and `editor.json` beside it) or a release's `editor` folder; `-maps` a scratch folder (the packages in it are written); `-copy` a game copy that is only read (an extract of `Dawngate.zip` is fine), without which the catalog checks, the props and the 3-D view are off; `-out` and `-cache` the outputs (by default `out` beside the maps folder, and `out\3d`), which are the game's art in other formats: keep them out of any repository. `-addr` (default `127.0.0.1:17890`, loopback only) and `-mapc` (another compiler; by default the tool runs itself as `mapc`, as Start-Client does) complete it. The backend's tests are in `go test ./...` (`internal/mapedit`, `internal/launcher`'s editor tests with a fake game and server).

The first run takes a few minutes. **Run it again after every update of your checkout.** It rebuilds what changed and skips the rest in a few seconds. `--check` only reports. The script downloads nothing, installs no tool and never changes firewall rules.

## The same steps by hand

```powershell
python -I re\scripts\extract_client.py Dawngate.zip client\ia_Dawngate
robocopy client\ia_Dawngate\Dawngate client\run_854\Dawngate /E /COPY:DAT /DCOPY:T
.venv\Scripts\python -I server\tools\make_certs.py server\certs 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_client.py client\run_854\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_config.py client\run_854\Dawngate\game\Data
.venv\Scripts\python -I re\scripts\patch_locale.py client\run_854\Dawngate\game\Data --maps maps
.venv\Scripts\python -I re\scripts\bot_profiles.py client\run_854\Dawngate\game\Data
cmake -S gms -B gms\build -G "Visual Studio 17 2022" -A Win32 [-DRAKNET_ROOT=<RakNet folder>]
cmake --build gms\build --config Release
go build -C server -o bin\dgserver.exe .\cmd\dgserver
.venv\Scripts\python -I re\scripts\setup_server_copy.py client\run_854\Dawngate client\run_854_server\Dawngate gms\build\Release\dgsim.dll
.venv\Scripts\python -I re\scripts\bot_profiles.py client\run_854_server\Dawngate\game\Data
cd server; go run ./tools/mapsetup --maps ..\maps --client ..\client\run_854\Dawngate; go run ./tools/mapsetup --maps ..\maps ..\client\run_854_server\Dawngate; cd ..
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
