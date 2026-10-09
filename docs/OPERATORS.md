# Running a Dawngate server (operators)

Written 2026-10-08 (H3-offline). This guide is for whoever sets up and runs the server side: `dgserver` (the Blaze services and the match-host supervisor) and the match hosts, on one machine or with extra host machines. Players read [PLAYING.md](PLAYING.md).

**Status.** On one PC everything here is checked live: `dgserver` with match hosts since C1b, `play.ps1` since P1 ([PLAYING.md](PLAYING.md)), headless match hosts (about 100 MB each) as the default since H1b. The setup command `re\scripts\setup_all.py` was tested end to end on scratch folders on the development PC (H3-offline). **Since A5b** the LAN setup below ran on one PC as server and client at once, on its own LAN address (main menu, a practice match, egress 0 flagged), and two clients on one server (the PvP queue, a party by name, the lobby chat, one match). **Not yet run anywhere:** a fresh machine set up from this guide alone (H3-live), clients on other machines over the LAN, and host machines in agent mode across machines (H2-live). All three wait for a second machine (a VM).

## What runs where

| Process | What it is | Listens on (defaults) |
|---|---|---|
| `server\bin\dgserver.exe` | the Go server: TLS redirector, Blaze, QoS, HTTP (web content, telemetry rules), the telemetry sink, accounts and progression, and the supervisor that starts one match host per game | TCP 42127 (redirector, TLS 1.1), 10041 (Blaze), 17502 (QoS HTTP), 18080 (HTTP), 9988 (telemetry); UDP 17502 (QoS probes); TCP 10040 (the hosts' control channel, only with `--gms-dir` or `--agent-key`) |
| `client\run_854_server\Dawngate\game\Dawngate.exe` | a match host: the shipped game exe from the *host copy*, whose `PhysXLoader.dll` is our `dgsim.dll`; started by `dgserver` per game, with no window, no client sockets and its own AppData | UDP 10042–10051 (one GMS port per running match, RakNet) |
| `dgserver.exe --agent …` | only on extra host machines (H2): registers with the main server and starts hosts there | nothing (it dials the main server) |

Without `--gms-dir` `dgserver` starts no hosts and only logs the GMS port (UDP 10042); PRACTICE then can't start a match.

**What a match host needs.** It is the shipped `Dawngate.exe` with our DLL, **headless (stage 2) by default since H1b**: it starts only the engine core and the server simulation, without the renderer, audio, the UI or the client game, and needs about **100 MB of RAM per running match** (about 280 MB of the 2 GB address space, flat over a whole match), about 3 % of one core, and is ready about 2 s after PRACTICE. It still creates a Direct3D 9 object at start-up but no device; whether a machine without a Direct3D 9 GPU can run it hasn't been tried. **The way back** is stage 1, the full client start-up with a hidden window: `--gms-options "core_only=0"` (below), or `core_only=0` under `[host]` in the host copy's `game\dgsim.ini`; that takes about 1 GB of RAM per match (peak 1.05 GB private, 1.5 GB of the address space) and 5–8 s to start.

## The release: Start-Server.exe (the way for other people)

An operator who doesn't develop needs only the release zip (`Start-Server.exe`, `Start-Client.exe`, `host\dgsim.dll` and its Lua scripts, licences, `README.txt`; no game file) and `Dawngate.zip`. Unpack the release, put `Dawngate.zip` next to `Start-Server.exe` and start it: its first start writes `Start-Server.ini`, sets everything up in `Dawngate-Data` next to it (the player copy, the match-host copy with `dgsim.dll`, certificates made for this install, the player content; about 30 s and 3.1 GB, byte for byte what `setup_all.py` makes) and then runs the server in its window with the host copy, a headless host per game and shaper select, until Ctrl+C or the window is closed (its match hosts end with it). Every start checks and repairs the setup first (a second). `Start-Server.exe --check` only reports; `--setup-only` doesn't start the server; `--zip <file>` names the archive. Checked on a clean folder on 2026-10-09: the first start in 29 s, a practice match from `Start-Client.exe`, no traffic beyond the PC, a Ctrl+C in its window stops the server and its match hosts, a second start ready in 0.5 s. Players on a second machine haven't been tried yet.

- **`Start-Server.ini`** (`key = value`, `;` comments): `address` (127.0.0.1, or this PC's LAN address for players on other machines; it must be an address of this PC, public and wildcard addresses are refused; a certificate set per address is made in `Dawngate-Data\certs\<address>`), `shaper_select` (on), `folder` (`Dawngate-Data`, relative to the program or absolute), `zip` (where `Dawngate.zip` is, when it isn't next to the program, in the folder above or in Downloads), `server_flags` (any `dgserver` flag of *Running the server* below, split at blanks, e.g. `--owned starter --mm-team-size 1`). A player's `Start-Client.ini` holds `server` (the address to play on) and `folder`.
- **Players on other machines (the CA exchange):** the game accepts only a server whose certificate comes from the certificate authority patched into its copy and names the address it was patched for, so each player's copy needs your CA's public modulus. They run `Start-Client.exe <address>`: it fetches the modulus (`Start-Server.exe` runs `dgserver --serve-ca`, which serves the public `ca_modulus.bin` on the HTTP port at `/dawngate/ca_modulus.bin`), shows its fingerprint and keeps it only if they answer `y` after comparing it with the one your window shows (trust on first use; a different one later gets a warning and the question again). Or hand out the file `Dawngate-Data\certs\<address>\ca_modulus.bin` (only that file, never a `*_key.pem`): `Start-Client.exe <address> --ca ca_modulus.bin`. Either way the redirector's certificate is checked against it before the copy is patched. **The risk:** whoever answers at your address on the player's network at that first connection could serve their own CA, and a player who confirms without comparing would then trust that impostor, which could read the login as typed and run the matches; the comparison or the handed-out file prevents it. Changing the address later makes a new set; players then confirm the new fingerprint once.
- **Ports** (*Ports and the firewall* below): TCP 42127, 10041, 17502, 18080 and 9988 and UDP 17502 on the address, one UDP port of 10042–10051 per running match, and the control port 10040 between the server and its match hosts (on 127.0.0.1 when the address is a LAN address). Players on other machines need all but the control port; *What must never be exposed* below holds for the release too.
- **Where things are:** accounts and content in `Dawngate-Data\data` (back it up), logs in `Dawngate-Data\logs` (the hosts' folders in `logs\hosts\<match>`), the copies in `client\` and `host\`.
- **The release itself** is built from the repository with `re\scripts\make_release.ps1` after a Release build of `gms\` (the developers' setup below: Go, Visual Studio 2022, RakNet 4.081). It writes `re\out\release\<name>\` and `<name>.zip` (`Dawngate-Restoration-<date>-<commit>`), with `BUILD.txt` naming the source commit and every file's SHA-256, and refuses to include any game file (*Distribution and licences* below).
- The rest of this guide is the developers' setup from the repository (Python, Go, Visual Studio), which the tests and lanes use.

## Setting up a machine

Requirements (versions as on the development PC, [SETUP.md](SETUP.md)):

- Windows 10 or 11, x64; about 4.6 GB of disk for the setup (the extracted archive and two copies of 1.5 GB each, the builds and data), plus the 1.3 GB archive. `setup_all.py` wants 1 GB to spare before each copy.
- **Python 3.12**, **Go 1.24 or newer**, **Visual Studio 2022** with *Desktop development with C++* (the x86 compiler; its bundled CMake is used), **Git for Windows** (its `openssl` signs the certificates with MD5, which Python's `cryptography` refuses).
- Optional: **Wireshark with Npcap** ("Support loopback traffic capture"): the capture and egress check of every session. Without it, `play.ps1 -NoCapture`.
- Two downloads, never committed:
  - `Dawngate.zip` from <https://archive.org/details/Dawngate> (1,310,588,859 bytes, SHA-256 `56ae93939904605ac3145cb55ace854482f40bf128991a0a21ba56f14d4bdae1`; build 1.0.903.854). Put it in the repository folder or in your Downloads folder.
  - RakNet 4.081, commit `1a169895a900c9fc4841c556e16514182b75faf8`: <https://github.com/facebookarchive/RakNet/archive/1a169895a900c9fc4841c556e16514182b75faf8.zip> (SHA-256 `60b53557aa084fb5455c8e2e7efe309738dabe7f9db5c0bc103cf41c8f97c0e0`). Downloads folder, or `tools\_downloads\raknet\`.

Then, from the repository folder:

```powershell
python -m venv .venv
.venv\Scripts\pip install pefile cryptography
.venv\Scripts\python -I re\scripts\setup_all.py [--zip <Dawngate.zip>] [--raknet-zip <RakNet zip>]
```

`setup_all.py` (run with `-I`: it reads the archive, which is untrusted input) makes, in this order, and checks each result:

| Step | Result | Current when |
|---|---|---|
| archive | `client\ia_Dawngate` extracted from `Dawngate.zip` (through `client\ia_Dawngate.partial`, renamed when complete) | all 167 files match [`file_manifest.csv`](file_manifest.csv) (size and SHA-256; an existing folder: sizes and the 5 key files, `--verify` all). It is never written again. |
| certificates | `server\certs` (`make_certs.py`, CN `127.0.0.1`); `--lan-certs <S>` also `server\certs\lan` (CN `<S>`) | the five files exist, the CN is right, the certificate belongs to the CA. Another CN is never replaced without `--new-certs` |
| client copy | `client\run_854\Dawngate` | every file of the build is there (missing or truncated ones are copied back) |
| client exe / config | `patch_client.py` (local profile, our CA; since Q3b with `LARGE_ADDRESS_AWARE`, 4 GB of address space instead of 2 GB, so a long match doesn't run out), `patch_config.py --host 127.0.0.1` | byte for byte what the patchers make from the `.orig` files, whose SHA-256 must be the build's |
| RakNet | `tools\raknet\RakNet` (`Source\`, `LICENSE`, `PATENTS`, `README.md`) | `RakNetVersion.h` says 4.081 |
| gms build | `gms\build` (CMake, Visual Studio 2022, Win32, Release): `dgsim.dll`, `gms_script.exe`, `gms_tests.exe` | both outputs newer than their sources (`run_session.ps1`'s rule) |
| dgserver | `server\bin\dgserver.exe` | newer than every source file under `server\` |
| host copy, host exe / config | `client\run_854_server\Dawngate`, patched like the client copy | patched for `127.0.0.1` |
| host DLL | `setup_server_copy.py`: `dgsim.dll` as `PhysXLoader.dll` (the original kept as `PhysXLoader_orig.dll`), `dgsim.ini` (written once, never overwritten: your own changes stay; a key it lacks takes the DLL's default, e.g. `core_only=1`), the Lua probes | the installed DLL is the current build |
| content | `server\data\content.json` from your own game data (`snap.py`, `assetdata.py` into `re\out\`, `dgserver --make-content`) | the server's current format, since D3-perks with the perks' stats (a file built before is rebuilt: the perk pages' stats in a match come from it); `--no-content` leaves it out (new accounts then own no items) |
| `--probe`, `--lane b,c,d` (or `--lane-b`), `--lane e`, `--lane f`, `--lane lan` | the RakNet research probe; the extra test lanes (`setup_lane.py <x>` for each, about 3 GB each); since A5b lane d's second client `client\run_854_e` (since G3b lane b's, `--lane f`: `client\run_854_f`) and, with `--lan-certs <S>`, the LAN-profile test copy `client\run_854_l` (about 1.5 GB each) | development only |

It prints one line per step (`ok`, `done`, `MISSING`, `STALE`, `FAILED`) and a summary. `--check` only reports and writes nothing (exit 0 when everything is current, 1 when something is missing or stale); without it, a missing tool or a failed step is exit 2. **Run it again after every `git pull`:** it rebuilds `gms\` and `dgserver` when their sources changed and installs the new `dgsim.dll` into the host copy, and skips everything else (a few seconds). It downloads nothing, creates no `.venv`, installs no tool and changes no firewall rule.

**By hand** (what the script runs; the paths are the defaults):

```powershell
python -I re\scripts\extract_client.py Dawngate.zip client\ia_Dawngate
robocopy client\ia_Dawngate\Dawngate client\run_854\Dawngate /E /COPY:DAT /DCOPY:T
.venv\Scripts\python -I server\tools\make_certs.py server\certs 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_client.py client\run_854\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1
.venv\Scripts\python -I re\scripts\patch_config.py client\run_854\Dawngate\game\Data
cmake -S gms -B gms\build -G "Visual Studio 17 2022" -A Win32 [-DRAKNET_ROOT=<tools\raknet\RakNet>]
cmake --build gms\build --config Release
go build -C server -o bin\dgserver.exe .\cmd\dgserver
.venv\Scripts\python -I re\scripts\setup_server_copy.py client\run_854\Dawngate client\run_854_server\Dawngate gms\build\Release\dgsim.dll
# content (optional): PLAYING.md's former list, now setup_all.py's content step
.venv\Scripts\python -I re\scripts\snap.py extract client\ia_Dawngate\Dawngate\game\Data\AssetData.snap re\out\snap
.venv\Scripts\python -I re\scripts\assetdata.py dump re\out\snap\AssetData\AssetData\Binary re\out\assetdata --locale client\ia_Dawngate\Dawngate\game\Data\Locale\en-us\Locale.package
server\bin\dgserver.exe --make-content re\out\assetdata
# or the same file straight from the game folder, without Python or the two steps above
server\bin\dgserver.exe --make-content client\ia_Dawngate\Dawngate
```

`cmake.exe` is Visual Studio's (`Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin`), not on PATH by default. After new certificates, run `patch_client.py` again: the client must carry the CA.

## Running the server

**On one PC (server and player on the same machine):** `play.ps1` does everything, with the capture and egress check ([PLAYING.md](PLAYING.md)). The server alone, from the repository folder:

```powershell
server\bin\dgserver.exe --gms-dir client\run_854_server --gms-data server\logs\hosts
```

Everything then listens on `127.0.0.1` only; Ctrl+C stops it and every host it started (the hosts also end with it if it crashes: the host job).

**For players on other machines of your LAN** (A5; tried in A5b on one PC as server and player at once, not yet from a second machine). `<S>` is the server machine's LAN address:

```powershell
.venv\Scripts\python -I re\scripts\setup_all.py --lan-certs <S>          # once: server\certs\lan with CN <S>
server\bin\dgserver.exe --host <S> --certs server\certs\lan --gms-dir client\run_854_server --gms-data server\logs\hosts
```

`--host` sets both `--bind` (where every listener and the hosts' GMS ports bind) and `--public-host` (what the clients are told: the redirector's reply, the QoS site, the telemetry server and the GMS address `HNET`). The wildcard `0.0.0.0` is refused. Give each player two things: the address `<S>` and the file `server\certs\lan\ca_modulus.bin` (the CA's public modulus, nothing secret). Players set up their client with `setup_all.py --player` and play with `play.ps1 -Server <S>` (since P2: no server on their machine, a capture and an egress check that allows only `<S>`; [PLAYING.md](PLAYING.md) *Playing on a server on another machine*). The client checks the redirector certificate's CN against the address it was patched for (seen live in A5b: a certificate of the right CA with another CN gives the client's "Unable to establish connection"), so a new server address needs new LAN certificates (`--lan-certs <new S> --new-certs`) and every player's copy patched again.

**Several players on one server** need nothing extra: every client logs in with its own account; the PvP queue (`--mm-*` below) and the PvP lobby's party work between them. The lobby's invite takes a **persona name** (the name at the top right of a player's main menu), which `dgserver` looks up (`UserSessions::lookupUser`, A5b); there are no friends lists. **Developers** can run a second client on the same PC: `setup_lane.py e` makes lane d's second client copy and `run_session.ps1 -Lane d -SecondClient` runs it beside the first; `setup_lane.py lan --address <S>` (or `setup_all.py --lan-certs <S> --lane lan`) makes a LAN-profile test copy for this PC's own address, run with `run_session.ps1 -Lan <S>`.

**Other `dgserver` options** (`dgserver.exe -h` lists all):

| Option | Effect |
|---|---|
| `--data <dir>` | accounts (`accounts.json`) and the content file; default `server\data`. Back it up to keep accounts |
| `--log <dir>` | the log folder; default `server\logs` |
| `--certs <dir>` | the redirector's certificate and key; default `server\certs` |
| `--gms-ports 10042-10051` | the hosts' UDP ports: one per running match, so at most ten matches at once by default |
| `--gms-data <dir>` | each host's own folder `<dir>\<match id>` (its `dgsim.log`, AppData, `stdout.txt`); default `<host copy>\game\host\<match id>`, rewritten by the next match with that id |
| `--gms-options "<key>=<value>;…"` | the match hosts' `[host]` keys for every host this server starts (they override the host copy's `game\dgsim.ini`). The one an operator may need: **`core_only=0`**, the way back to stage 1 (the full client start-up: about 1 GB and 5–8 s per host instead of 100 MB and 2 s), should a headless host ever misbehave. Since H1b the hosts are headless by default (`core_only=1`); before, this option was how to switch them on |
| `--gms-warm N` | keep N idle hosts ready (H1-live): a game then starts in about 0.1 s instead of about 2 s (5–8 s with `core_only=0`). Each holds a port of `--gms-ports` and its memory (about 90 MB, about 950 MB with `core_only=0`), so N must leave a port free. Not needed for a client's sake (it waits at least 90 s for its match); `play.ps1` doesn't use it |
| `--owned all\|starter`, `--ftue-done` | what new accounts own; skip the practice-only first screen |
| `--mm-team-size 5`, `--mm-min-players 1`, `--mm-fill-after 15s` | PvP matchmaking (QUEUE SOLO on the play screen): players per team, and how long the oldest player waits for full teams before a match starts with at least `--mm-min-players` (the empty seats get bots, below). The defaults give a lone player a game after 15 s |
| `--mm-bots=false` | leave the empty seats of a PvP match empty. By default (`--mm-bots`, on) a match that starts with fewer than 2 × `--mm-team-size` players gets bots in the open seats, up to `--mm-team-size` a team (a lone player in the default 5v5 queue plays with four bot teammates against five bots, as in a practice match); with it off such a match has only its humans on the map (B10d). Only with `--gms-dir` (a match host fills the seats); the startup line says `bots in the open seats` or `open seats stay empty` |
| `--custom-bots` | bots fill a custom game's open seats when its admin starts it (each team up to five; off by default: a custom game has only the players its lobby gathered). Only with `--gms-dir` (B15) |
| `--mm-max-wait <d>` | the longest a player waits in the PvP queue before the client is told "Matchmaking canceled - session timed out" (default 0: the client's own 3 hours). `--mm-refuse` is a test aid: every queue request gets an error |
| `--shaper-select`, `--free-rotation <names>`, `--shaper-timers <ms>,<ms>,<ms>,<ms>` | shaper select before every hosted match: each player picks from the shapers, skins, wards and announcer packs its account owns; `--free-rotation` (comma-separated item names, Nouns, PlayerClasses or titles, e.g. `gunman,Fenmore`) makes shapers selectable for everyone; `--shaper-timers` the four phases' lengths (default: the host's, 300 s/300 s/15.5 s/16 ms in a practice game) |
| `--kill-switches <names>` | client features switched off at every login (B14; *System messages* below). Default `WalletTopUp` (the main menu's add-funds button, whose top-up page nothing serves); `none` allows everything |
| `--broadcast <text>`, `--broadcast-file <file>`, `--broadcast-flags 4` | announcements in the main menu's marquee and shaper select (B14; *System messages* below). The file defaults to `<data>\broadcast.txt` and is read every 2 s; `--broadcast-file ""` turns it off |
| `--test-spent-elsewhere` | a test aid only (B7c-live): every purchase is refused and the account's balance set to 0. Never on a server people play on |
| `--seconds N` | stop after N seconds |
| `--print-ports` | the TCP ports this configuration listens on |

Accounts: any e-mail and password log in, and the first login creates the account. **Passwords are not checked**, and the log holds what players typed in plain text.

### System messages

(B14, offline: the code and its tests are done, the check against the live client is pending.) Every client gets the server's system messages right after its login:
- **Kill switches** hide or block client features: `--kill-switches` takes a comma-separated list of `Matchmaking` (the PvP queue), `WalletTopUp` (the add-funds button), `EUGameServers` (the EU region), `PracticeSoloGames`, `FTUESoloGames` (the first-time player's practice), `PurchaseWithWaypoints` (purchases with Waypoints), `PracticeGroupGames`, `CustomGames`, `ForwardRenderer` (or their numbers 1–9); the default is `WalletTopUp`, `none` switches nothing off. They apply at the next login (a running client keeps what it got).
- **Announcements:** write one message per line into `server\data\broadcast.txt` (or the file `--broadcast-file` names, or give one message with `--broadcast "<text>"`). `dgserver` reads the file every 2 seconds and sends a change to everyone logged in at once; a player logging in later gets the current messages. Delete the file (or empty it) to take them down. By default a message shows in the main menu's scrolling status line and in shaper select (display flag 4). Put `@<flags> ` in front of a line to choose where it shows, adding up: 4 the main menu's status line and shaper select, 2 a "System" line in the chat (also in a running match: use it for "Server restart in 5 minutes"), 1 the chat's notification list in the main menu. `--broadcast-flags` sets the default. Lines starting with `#` are comments; at most 16 messages of 1,024 bytes each. Example file:

```text
# shown in the main menu
Welcome to the Dawngate restoration test server
@6 Server restart at 20:00 UTC
```

## Extra host machines (agent mode, H2)

A host machine runs matches for the main server; Blaze stays on the main server. Set it up like any server machine (`setup_all.py`; `--no-content` is enough there), then (not tried across machines yet):

```powershell
# once, on the main server S: a shared key; copy the file to each host machine over a trusted channel
server\bin\dgserver.exe --make-agent-key server\data\agent.key
# the main server: takes agents, its control port on its LAN address
server\bin\dgserver.exe --host <S> --certs server\certs\lan --agent-key server\data\agent.key --gms-control-bind <S> [--gms-dir client\run_854_server]
# each host machine H
server\bin\dgserver.exe --agent <S>:10040 --agent-key agent.key --gms-dir client\run_854_server --host <H> [--agent-name H1]
```

Each new game goes to the machine with the most free GMS ports. The agent registers by an HMAC challenge over the key, its hosts dial the main server with one-use tokens that only count from their own machine, and `HNET` names the host machine. If the agent or its link dies, its matches end (`GAME_DESTROYED`) and its hosts are killed with it. Players' clients then talk to the host machines too, so tell your players their addresses: `play.ps1 -Server <S>` allows only `<S>` in its egress check, and `play.ps1 -Server <S> -AllowHost <H1>,<H2>` allows the host machines as well (P2).

## Ports and the firewall

| Port | Protocol | Bound by | Reached by |
|---|---|---|---|
| 42127 | TCP (TLS 1.1) | `dgserver` | clients: the redirector |
| 10041 | TCP (plain) | `dgserver` | clients: Blaze (login, menus, matchmaking, chat) |
| 17502 | TCP and UDP | `dgserver` | clients: QoS (latency, bandwidth, the NAT test) |
| 18080 | TCP (HTTP) | `dgserver` | clients: telemetry rules, web content, the home page |
| 9988 | TCP | `dgserver` | clients: telemetry |
| 10040 | TCP (JSON lines) | `dgserver` with `--gms-dir` or `--agent-key` | match hosts (and agents); `127.0.0.1` unless `--gms-control-bind` |
| 10042–10051 | UDP (RakNet) | the match hosts (`Dawngate.exe` of the host copy) | clients: the game itself |

Clients only open connections (Blaze, QoS, RakNet to the GMS), so client machines presumably need no inbound rule. **Firewall rules are the operator's decision; nothing here changes them.** With a LAN `--bind`, Windows asks once per program path the first time it listens on a non-loopback address (seen in A5b: `dgserver.exe` at its start, the host copy's `Dawngate.exe` at its GMS port, and a client copy whose sockets bind the LAN address; a "Windows Security Alert" each). Until it is answered (or if cancelled) other machines can't reach that program, while clients on the server machine itself still can. If you allow them, limit the rules to the private profile and, where you can, to your LAN's subnet. On the main server allow TCP 10040 only from your host machines' addresses; host machines need no rule for the agent, which only dials out, but their UDP 10042–10051 must be reachable by the clients. A firewall prompt on a loopback-only setup can be cancelled: everything listens on `127.0.0.1` then.

## What must never be exposed

This is a preservation server for a trusted LAN. **Never forward any of these ports from the internet.**

- **Blaze is plain TCP** and accepts any credentials; the redirector's TLS is version 1.1 with MD5-signed, exponent-3 RSA-1024 certificates, because that is what the 2014 client speaks. None of it protects anything against an attacker on the network.
- **The control channel** (TCP 10040) has no encryption and, after the agent's registration, no integrity. Keep `--gms-control-bind` at `127.0.0.1` unless you run agents, and then reachable only from your host machines.
- **Private keys stay on the server:** `server\certs\*\ca_key.pem` (whoever has it can impersonate your server to every client patched with that CA), `server_key.pem`, and the agent key (`agent.key`: whoever has it can register a host machine, steer players to it and report match results). Hand out only `ca_modulus.bin`.
- **Logs and accounts:** `server\logs\*` (and the session logs under `re\logs\`) contain logins with the passwords as typed, e-mail addresses and every request; `server\data\accounts.json` holds the accounts. Keep them private.
- **Egress:** a patched client contacts nothing beyond the server it was patched for (no EA host, no UPnP; checked on loopback in every session). Check a session's capture with `re\scripts\egress_report.py <session> --allow-host <S>` (on a client machine; `play.ps1 -Server <S>` does it for every session since P2; the server's own captures: `--allow-host <each client>`). Only addresses of your own network are accepted there.

## Distribution and licences

- **No EA files are shipped by this project.** The repository holds no game file, and nothing it builds contains any. Every machine needs its own `Dawngate.zip` from archive.org; Dawngate is © Electronic Arts. Never redistribute the archive, the extracted folder, the working or host copies (the patched `Dawngate.exe` and config packages are EA's files with a few bytes changed), `server\data\content.json` or anything under `re\out\` (all derived from the game data).
- **Binary distributions of our builds carry the third-party notices:**
  - `dgsim.dll`, `gms_script.exe`, `gms_tests.exe` and the RakNet probe link **RakNet 4.081** statically (BSD licence, © 2014 Oculus VR, Inc., plus its additional patent grant): ship `tools\raknet\RakNet\LICENSE` and `PATENTS` with them.
  - `dgserver.exe` contains **Go's standard library and runtime** (BSD-3-Clause, © The Go Authors): ship Go's `LICENSE` with it.
  - The MSVC static runtime needs no notice.
  - The release zip (`re\scripts\make_release.ps1`) is a binary distribution: it carries `LICENSE.txt` (AGPL-3.0, this project's code), `THIRD-PARTY-NOTICES.txt` (RakNet's `LICENSE` and `PATENTS`, Go's `LICENSE`) and `BUILD.txt` (the source commit). Under the AGPL, whoever hands it out also offers the source of that commit. The script refuses to put any game file into it (by name and by SHA-256 against both builds of `file_manifest.csv`).

## When something goes wrong

- `setup_all.py --check` says what is missing or stale; the same command without `--check` fixes it.
- A host that doesn't start: `[host]` lines in the server log, then `<--gms-data>\<match id>\dgsim.log` and `stdout.txt`; the error reply to PRACTICE is "Game creation failed - no servers available".
- The client can't connect or stays at "Initializing": the certificates and the client's patch don't match (another CA, or a CN other than the address the client was patched for).
- A match that ended by itself, its players back at the main menu (since H5): the server log's `[host] match N: host error 2 (tick stalled, fatal true): …` and `game N: the match host gave up` lines mean the host's watchdog found its server tick stalled for `watchdog_s` (10) seconds; `host error 3 (step failing …)` that one step of its frame threw in every run for `step_fail_s` seconds. `<--gms-data>\<match id>\watchdog.txt` and `watchdog.dmp` say where it hung; the port is free again at once. `--gms-options "watchdog_s=0"` turns the watchdog off (a hung host then ends only by the 30-s heartbeat timeout). Seen live in H5-live (2026-10-09): the lines exactly so, the match ended about 10 s after the freeze, the players at the main menu, the next game on the same port; `re\scripts\minidump_stack.py <dir>\watchdog.dmp` prints the hung thread's stack.
- A match behaves differently from what [PLAYING.md](PLAYING.md) describes and you suspect the headless host: start `dgserver` with `--gms-options "core_only=0"` (stage 1, the full client start-up) and compare; a host's `dgsim.log` says which it ran (`host: stage 2 (H1-live): … core_only 1`).
- More in [PLAYING.md](PLAYING.md) *Troubleshooting*.
