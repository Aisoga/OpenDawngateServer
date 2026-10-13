# Running a Dawngate server (operators)

Written 2026-10-08 (H3-offline). This guide is for whoever sets up and runs the server side: `dgserver` (the Blaze services and the match-host supervisor) and the match hosts, on one machine or with extra host machines. Players read [PLAYING.md](PLAYING.md).

**Status.** On one PC everything here is checked live: `dgserver` with match hosts since C1b, `play.ps1` since P1 ([PLAYING.md](PLAYING.md)), headless match hosts (about 100 MB each) as the default since H1b. The setup command `re\scripts\setup_all.py` was tested end to end on scratch folders on the development PC (H3-offline). **Since A5b** the LAN setup below ran on one PC as server and client at once, on its own LAN address (main menu, a practice match, egress 0 flagged), and two clients on one server (the PvP queue, a party by name, the lobby chat, one match). **Not yet run anywhere:** a fresh machine set up from this guide alone (H3-live), clients on other machines over the LAN, and host machines in agent mode across machines (H2-live). All three wait for a second machine (a VM).

## What runs where

| Process | What it is | Listens on (defaults) |
|---|---|---|
| `server\bin\dgserver.exe` | the Go server: TLS redirector, Blaze, QoS, HTTP (web content, telemetry rules), the telemetry sink, accounts and progression, the tunnel gateway and the website (when switched on), and the supervisor that starts one match host per game | TCP 42127 (redirector, TLS 1.1), 10041 (Blaze), 17502 (QoS HTTP), 18080 (HTTP), 9988 (telemetry); UDP 17502 (QoS probes); TCP 10040 (the hosts' control channel, only with `--gms-dir` or `--agent-key`); TCP and UDP 42128 (the tunnel gateway, only with `--tunnel on`), TCP 443 (the website, only with `--web-port`) |
| `client\run_854_server\Dawngate\game\Dawngate.exe` | a match host: the shipped game exe from the *host copy*, whose `PhysXLoader.dll` is our `dgsim.dll`; started by `dgserver` per game, with no window, no client sockets and its own AppData | UDP 10042–10051 (one GMS port per running match, RakNet) |
| `dgserver.exe --agent …` | only on extra host machines (H2): registers with the main server and starts hosts there | nothing (it dials the main server) |

Without `--gms-dir` `dgserver` starts no hosts and only logs the GMS port (UDP 10042); PRACTICE then can't start a match.

**What a match host needs.** It is the shipped `Dawngate.exe` with our DLL, **headless (stage 2) by default since H1b**: it starts only the engine core and the server simulation, without the renderer, audio, the UI or the client game, and needs about **100 MB of RAM per running match** (about 280 MB of the 2 GB address space, flat over a whole match), about 3 % of one core, and is ready about 2 s after PRACTICE. **It makes no Direct3D 9 call at all, so it needs no GPU**: no `Direct3DCreate9` and no device. `d3d9.dll`, which ships with Windows, is still loaded because the exe imports it, but nothing calls it. Earlier versions created a Direct3D 9 object at start-up, and on a machine without a usable Direct3D 9 adapter the host stopped at "Could not start the renderer". Tested with the host simulating a machine without Direct3D 9 (`[host] test_no_d3d9=1`, `Direct3DCreate9` answers NULL): a practice match ran 15 minutes with 0 host exceptions and the log's `d3d9 not touched (Direct3DCreate9 0, stand-in 0)`, while the old start-up (`d3d9=1`) on the same simulated machine ended after 1 s at "Could not start the renderer". **A run on a real machine without a GPU hasn't been done yet.** `[host] d3d9=1` brings back the old start-up. **The way back** is stage 1, the full client start-up with a hidden window: `--gms-options "core_only=0"` (below), or `core_only=0` under `[host]` in the host copy's `game\dgsim.ini`; that takes about 1 GB of RAM per match (peak 1.05 GB private, 1.5 GB of the address space) and 5–8 s to start, and it needs a Direct3D 9 adapter.

## The release: Start-Server.exe (the way for other people)

An operator who doesn't develop needs only the release zip (`Start-Server.exe`, `Start-Client.exe`, `host\dgsim.dll` and its Lua scripts, licences, `README.txt`; no game file) and `Dawngate.zip`. Unpack the release, put `Dawngate.zip` next to `Start-Server.exe` and start it: its first start writes `Start-Server.ini`, sets everything up in `Dawngate-Data` next to it (the player copy, the match-host copy with `dgsim.dll`, certificates made for this install, the player content; about 30 s and 3.1 GB, byte for byte what `setup_all.py` makes) and then runs the server in its window with the host copy, a headless host per game and shaper select, until Ctrl+C or the window is closed (its match hosts end with it). Every start checks and repairs the setup first (a second). `Start-Server.exe --check` only reports; `--setup-only` doesn't start the server; `--zip <file>` names the archive. Checked on a clean folder on 2026-10-09: the first start in 29 s, a practice match from `Start-Client.exe`, no traffic beyond the PC, a Ctrl+C in its window stops the server and its match hosts, a second start ready in 0.5 s. Players on a second machine haven't been tried yet.

- **The first start asks who will play** (in a console, not with `--non-interactive`; again with `Start-Server.exe --configure`): **1** only this PC (Enter: `address = 127.0.0.1`, nothing else asked), **2** the home network (this PC's private addresses are listed, the default route's first: `address = <it>`), **3** the internet: the public profile's questions (*A public server* below). The answers go into `Start-Server.ini`; editing the file does the same.
- **`Start-Server.ini`** (`key = value`, `;` comments): `address` (127.0.0.1, or this PC's LAN address for players on other machines, or a public IPv4 address with the risk acceptance below; the wildcard address is refused; a certificate set per address is made in `Dawngate-Data\certs\<address>`), `bind` (a public address behind a router: this PC's LAN address), `i_accept_the_risk`, `profile` (auto, home, public: *A public server* below), `tunnel` (auto, on, off) and `gateway_port` (42128), `shaper_select` (on), `signup` (open; invite, closed), `signups_per_address` (3) and `log_days` (30) (*Accounts* below), `web_port` (443), `web_domain`, `web_email` (the website), `folder` (`Dawngate-Data`, relative to the program or absolute), `zip` (where `Dawngate.zip` is, when it isn't next to the program, in the folder above or in Downloads), `server_flags` (any `dgserver` flag of *Running the server* below, split at blanks, e.g. `--owned starter --mm-team-size 1`; it comes after the keys and overrides them). An ini of an earlier release without a key gets its default. A player's `Start-Client.ini` holds `server` (the address to play on), `folder`, `tunnel` (auto, on, off) and `gateway_port`.
- **Accounts:** `Start-Server.exe --create-account <name>`, `--reset-password <name>`, `--account-status <name>=disabled`, `--make-invite`, `--list-invites`, `--revoke-invite <id>`, `--make-admin <name>` and `--remove-admin <name>` act on `Dawngate-Data\data`'s accounts and exit (also while the server runs in its own window; *Accounts* below). An admin uses the website's admin page (`https://<server>/admin`; *Accounts* below).
- **A public server:** *A public server* below: the public profile, its questions, the two ports to forward, the website and the admin page, backups and what remains your risk.
- **Players on other machines (the CA exchange):** the game accepts only a server whose certificate comes from the certificate authority patched into its copy and names the address it was patched for, so each player's copy needs your CA's public modulus. They run `Start-Client.exe <address>`: it fetches the modulus (`Start-Server.exe` runs `dgserver --serve-ca`, which serves the public `ca_modulus.bin` on the HTTP port at `/dawngate/ca_modulus.bin`), shows its fingerprint and keeps it only if they answer `y` after comparing it with the one your window shows (trust on first use; a different one later gets a warning and the question again). Or hand out the file `Dawngate-Data\certs\<address>\ca_modulus.bin` (only that file, never a `*_key.pem`): `Start-Client.exe <address> --ca ca_modulus.bin`. Either way the redirector's certificate is checked against it before the copy is patched. **The risk:** whoever answers at your address on the player's network at that first connection could serve their own CA, and a player who confirms without comparing would then trust that impostor, which could read the login as typed and run the matches; the comparison or the handed-out file prevents it. Changing the address later makes a new set; players then confirm the new fingerprint once.
- **Ports** (*Ports and the firewall* below): TCP 42127, 10041, 17502, 18080 and 9988 and UDP 17502 on the address, one UDP port of 10042–10051 per running match, and the control port 10040 between the server and its match hosts (on 127.0.0.1 when the address is a LAN address). Players on other machines of your LAN need all but the control port. With the tunnel (a public server) the server also listens on TCP and UDP 42128, and the website on TCP 443: those two are the only ones a public server forwards (*A public server* below); *What must never be exposed* below holds for the release too.
- **Where things are:** accounts and content in `Dawngate-Data\data` (back it up), logs in `Dawngate-Data\logs` (the hosts' folders in `logs\hosts\<match>`), the copies in `client\` and `host\`.
- **The release itself** is built from the repository with `re\scripts\make_release.ps1` after a Release build of `gms\` (the developers' setup below: Go, Visual Studio 2022, RakNet 4.081). It writes `re\out\release\<name>\` and `<name>.zip` (`Dawngate-Restoration-<date>-<commit>`), with `BUILD.txt` naming the source commit and every file's SHA-256, and refuses to include any game file (*Distribution and licences* below).
- The rest of this guide is the developers' setup from the repository (Python, Go, Visual Studio), which the tests and lanes use.

## A public server

A server strangers can reach. The release has what it needs, and **the public profile** (`profile = public` in `Start-Server.ini`; `auto`, the default, takes it for every public `address`) turns it all on:
- **the encrypted tunnel is required** (`tunnel = off` is refused): every player connection goes through it; `Start-Client.exe <public address>` uses it by itself;
- **the limits are on** (`dgserver --limits on`, also for a public profile on a LAN address): *Limits* below;
- **passwords are checked** (argon2id hashes, never logged) and **sign-up is your choice** (*Accounts* below);
- the match hosts' **strict message check** and RakNet's hardening run in every match anyway.

**Setting it up.** Start `Start-Server.exe` in a console (a double-click is one) and answer **3** to *Who will play on this server?* (or `Start-Server.exe --configure` later). It asks, and writes into `Start-Server.ini`:
1. your **internet address** (`address`: the IPv4 your router shows as its internet or WAN address; a private, loopback or shared address is refused) and, when this PC can't bind it (behind a router), which of this PC's LAN addresses the router forwards to (`bind`);
2. **who may make accounts** (`signup`): `invite` (the default here: players register on the website with a code you make), `closed` (only you make accounts) or `open` (the first login makes one; at most `signups_per_address` a day per internet address);
3. the website's **domain name** (`web_domain`, optional: a name whose DNS A record points at your internet address gets a Let's Encrypt certificate by itself, TLS-ALPN-01 on 443; without one the site uses a self-signed certificate that browsers warn about once) and an e-mail for Let's Encrypt (`web_email`, optional);
4. an **admin account** (optional): `--create-account <name>` and `--make-admin <name>` on the setup folder's database; the password is printed once;
5. the **risk notice** (below) and whether you accept it (`i_accept_the_risk = yes`; without it a public address doesn't start).

On a LAN address `profile = public` (edited by hand) runs the same setup for a test: the tunnel (players use `Start-Client.exe <address> --tunnel on`) and the limits, no risk notice. The release's end-to-end check ran that way, on one PC as server and player.

**Ports.** Forward on your router **only TCP and UDP 42128** (`gateway_port`, the tunnel) **and TCP 443** (`web_port`, the website and Let's Encrypt's check) to the server PC. Nothing else: the other ports stay on `bind` for your LAN, and **never TCP 10040** (the match hosts' control channel, kept on `127.0.0.1`). If the PC holds the public address itself (a rented server, no router), the plain ports (TCP 42127, 10041, 17502, 18080, 9988; UDP 17502, 10042–10051) listen on that address too: let its firewall in only TCP and UDP 42128 and TCP 443 (Start-Server's notice and banner say so; no `dgserver` switch moves the plain listeners off a public bind yet).

**The tunnel's key.** The gateway's key (`gateway_cert.pem`, `gateway_key.pem`) is made at the first start next to the redirector's certificates (`Dawngate-Data\certs\<address>\`, `dgserver --gateway-key`); its fingerprint is in Start-Server's banner and the log, and players compare it at their first start (trust on first use, like the CA exchange above; `Start-Client.exe --gateway-pin <the 64 hex digits of the pin>` skips the question). Publish the fingerprint where your players can check it. Keep `gateway_key.pem` private and keep it: a new key makes every player confirm the new fingerprint.

**Accounts, invites and the admin page.** Under `invite` make codes on the admin page (*Invite codes*: uses, days, a note; shown once) or with `Start-Server.exe --make-invite --invite-uses 5 --invite-days 14 --invite-note "the club"`, and hand them out; players register at `https://<server>/signup` with a login name, a display name, a password twice and the code, then log in with the game. The admin page (`https://<server>/admin`, accounts with the admin role) lists and searches accounts (disable, ban, a new password, delete; a disabled, banned or deleted player's running game ends at once), invite codes, the sign-up mode (in force at once), the running matches (end one) and the audit log of every admin action. Players change their password, delete their account or download their data on the website themselves; a forgotten password is yours to reset (the server sends no mail).

**Backups.** The accounts are one SQLite database, `Dawngate-Data\data\accounts.db` with `accounts.db-wal` and `accounts.db-shm` beside it. Stop the server and copy all three (or the whole `Dawngate-Data\data` folder, which also holds `content.json`, `broadcast.txt` and the website's folder `site\` with its certificates and your `privacy.html`) to another disk, e.g. daily; restore by copying them back with the server stopped. While the server runs, a copy of the three files may be torn; `sqlite3 accounts.db "VACUUM INTO 'backup.db'"` (the SQLite command-line tool, not part of the release) makes a consistent one. Keep `Dawngate-Data\certs` too: a new gateway key makes every player confirm a new fingerprint.

**Logs.** `log_days` (30) deletes the server's logs and the match hosts' folders after that many days; they hold login names and the players' addresses (never a password).

**What remains your risk and your duty** (the notice `Start-Server.exe` prints for a public address):
- **The players trust your server.** The game is 2014 code (no ASLR); the host checks what clients send, but a client reads whatever its server sends with the old code, so a server that is broken into or taken over could attack its players' game. Keep the server PC updated, use it for nothing sensitive, keep `Dawngate-Data\certs` (the keys) private.
- **Players behind one address share the limits** (a household fits; a LAN party or a mobile carrier's NAT behind one address may be slowed down; *Limits* below).
- **Personal data.** You keep login and display names, the players' addresses (logs) and match histories. Data-protection law (e.g. the GDPR in the EU) may apply: publish who runs the server and what it keeps on the website's privacy page (`Dawngate-Data\data\site\privacy.html`, an HTML fragment), keep `log_days` short, and answer players' requests; players can download and delete their own data on the website (a deletion keeps the match history without the name).
- **Backups** are yours (above).
- **The control channel** (TCP 10040) has no login: never forward it.

**Not checked yet:** a check from outside the LAN (a phone off the Wi-Fi through a real router's port forward) and a real Let's Encrypt certificate.

## Setting up a machine

Requirements (versions as on the development PC, [SETUP.md](SETUP.md)):

- Windows 10 or 11, x64; about 4.6 GB of disk for the setup (the extracted archive and two copies of 1.5 GB each, the builds and data), plus the 1.3 GB archive. `setup_all.py` wants 1 GB to spare before each copy.
- **Python 3.12**, **Go 1.24 or newer**, **Visual Studio 2022** with *Desktop development with C++* (the x86 compiler; its bundled CMake is used), **Git for Windows** (its `openssl` signs the certificates with MD5, which Python's `cryptography` refuses). The first `go build` of `server\` downloads the pinned outside modules ([ADR-4](adr/0004-outside-go-modules.md): `modernc.org/sqlite` and its dependencies, `golang.org/x/crypto`, checked against `server\go.sum`) from the Go module proxy into the module cache, so it needs the internet once; later builds work offline (`go mod download` in `server\` fills the cache ahead).
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
| client texts | `client\run_854\Dawngate\game\Data\Locale\en-us\AA_Server.package` (`patch_locale.py --host 127.0.0.1`): the login screen says "login name", the refused-login texts without EA's help address, and its *Sign up now!*, *Forgot Password?* and legal links open the server's own pages; `--web-domain <D>` points them at the server's website instead | byte for byte what `patch_locale.py` makes from the copy's own `Locale.package` (which must be the build's) |
| RakNet | `tools\raknet\RakNet` (`Source\`, `LICENSE`, `PATENTS`, `README.md`) | `RakNetVersion.h` says 4.081 |
| gms build | `gms\build` (CMake, Visual Studio 2022, Win32, Release): `dgsim.dll`, `gms_script.exe`, `gms_tests.exe` | both outputs newer than their sources (`run_session.ps1`'s rule) |
| dgserver | `server\bin\dgserver.exe` | newer than every source file under `server\` |
| host copy, host exe / config | `client\run_854_server\Dawngate`, patched like the client copy | patched for `127.0.0.1` |
| host DLL | `setup_server_copy.py`: `dgsim.dll` as `PhysXLoader.dll` (the original kept as `PhysXLoader_orig.dll`), `dgsim.ini` (written once, never overwritten: your own changes stay; a key it lacks takes the DLL's default, e.g. `core_only=1`), the Lua probes | the installed DLL is the current build |
| content | `server\data\content.json` from your own game data (`snap.py`, `assetdata.py` into `re\out\`, `dgserver --make-content`) | the server's current format, since D3-perks with the perks' stats (a file built before is rebuilt: the perk pages' stats in a match come from it); `--no-content` leaves it out (new accounts then own no items) |
| `--probe`, `--lane b,c,d` (or `--lane-b`), `--lane e`, `--lane f`, `--lane h`, `--lane lan` | the RakNet research probe; the extra test lanes (`setup_lane.py <x>` for each, about 3 GB each); since A5b lane d's second client `client\run_854_e` (since G3b lane b's, `--lane f`: `client\run_854_f`; lane c's, `--lane h`: `client\run_854_h`) and, with `--lan-certs <S>`, the LAN-profile test copy `client\run_854_l` (about 1.5 GB each) | development only |

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
| `--data <dir>` | accounts (the SQLite database `accounts.db`, with `accounts.db-wal` and `accounts.db-shm` beside it; an `accounts.json` of an earlier build is imported at the first start and renamed `accounts.json.imported`) and the content file; default `server\data`. Back it up to keep accounts: with the server stopped copy all three `accounts.db*` files, or while it runs make a consistent copy with SQLite's `VACUUM INTO` |
| `--log <dir>` | the log folder; default `server\logs` |
| `--certs <dir>` | the redirector's certificate and key; default `server\certs` |
| `--gms-ports 10042-10051` | the hosts' UDP ports: one per running match, so at most ten matches at once by default |
| `--gms-data <dir>` | each host's own folder `<dir>\<match id>` (its `dgsim.log`, AppData, `stdout.txt`); default `<host copy>\game\host\<match id>`, rewritten by the next match with that id |
| `--gms-options "<key>=<value>;…"` | the match hosts' `[host]` keys for every host this server starts (they override the host copy's `game\dgsim.ini`). The one an operator may need: **`core_only=0`**, the way back to stage 1 (the full client start-up: about 1 GB and 5–8 s per host instead of 100 MB and 2 s), should a headless host ever misbehave. Since H1b the hosts are headless by default (`core_only=1`); before, this option was how to switch them on |
| `--gms-warm N` | keep N idle hosts ready (H1-live): a game then starts in about 0.1 s instead of about 2 s (5–8 s with `core_only=0`). Each holds a port of `--gms-ports` and its memory (about 90 MB, about 950 MB with `core_only=0`), so N must leave a port free. Not needed for a client's sake (it waits at least 90 s for its match); `play.ps1` doesn't use it |
| `--owned all\|starter`, `--ftue-required` | what new accounts own; `--ftue-required` brings back the original's Practice-only first screen (every account is past it by default; `--ftue-done` is accepted and does nothing) |
| `--mm-team-size 5`, `--mm-min-players 1`, `--mm-fill-after 15s` | PvP matchmaking (QUEUE SOLO on the play screen): players per team, and how long the oldest player waits for full teams before a match starts with at least `--mm-min-players` (the empty seats get bots, below). The defaults give a lone player a game after 15 s |
| `--mm-bots=false` | leave the empty seats of a PvP match empty. By default (`--mm-bots`, on) a match that starts with fewer than 2 × `--mm-team-size` players gets bots in the open seats, up to `--mm-team-size` a team (a lone player in the default 5v5 queue plays with four bot teammates against five bots, as in a practice match); with it off such a match has only its humans on the map (B10d). Only with `--gms-dir` (a match host fills the seats); the startup line says `bots in the open seats` or `open seats stay empty` |
| `--custom-bots` | bots fill a custom game's open seats when its admin starts it (each team up to five; off by default: a custom game has only the players its lobby gathered). Only with `--gms-dir` (B15) |
| `--mm-max-wait <d>` | the longest a player waits in the PvP queue before the client is told "Matchmaking canceled - session timed out" (default 0: the client's own 3 hours). `--mm-refuse` is a test aid: every queue request gets an error |
| `--shaper-select`, `--free-rotation <names>`, `--shaper-timers <ms>,<ms>,<ms>,<ms>` | shaper select before every hosted match: each player picks from the shapers, skins, wards and announcer packs its account owns; `--free-rotation` (comma-separated item names, Nouns, PlayerClasses or titles, e.g. `gunman,Fenmore`) makes shapers selectable for everyone; `--shaper-timers` the four phases' lengths (default: the host's, 300 s/300 s/15.5 s/16 ms in a practice game) |
| `--kill-switches <names>` | client features switched off at every login (B14; *System messages* below). Default `WalletTopUp` (the main menu's add-funds button, whose top-up page nothing serves); `none` allows everything |
| `--broadcast <text>`, `--broadcast-file <file>`, `--broadcast-flags 4` | announcements in the main menu's marquee and shaper select (B14; *System messages* below). The file defaults to `<data>\broadcast.txt` and is read every 2 s; `--broadcast-file ""` turns it off |
| `--test-spent-elsewhere` | a test aid only (B7c-live): every purchase is refused and the account's balance set to 0. Never on a server people play on |
| `--signup open\|invite\|closed` | who can make an account (*Accounts* below): `open` (the default) = the first login with a new login name makes it, with the password typed; `invite` = only with an invite code (`--make-invite`; the players register with it on the website, `--web-port`) or by you; `closed` = only you (`--create-account`). `Start-Server.ini`: `signup` |
| `--signups-per-address N` | the most accounts one **public** source address may make per UTC day under `open` (default 3; `0` = no cap). Addresses of this PC and private LAN addresses never count; a player who comes through the tunnel counts with the address the gateway sees, not the tunnel's loopback address. `Start-Server.ini`: `signups_per_address` |
| `--log-days N` | delete `dgserver`'s log files and the match hosts' folders under `--gms-data` once they are older than N days, at the start and every 6 hours (default 0 = keep; `Start-Server.ini`'s `log_days` defaults to 30). The logs hold login names and the players' addresses |
| `--create-account <name>`, `--reset-password <name>`, `--account-status <name>=<state>`, `--make-invite [--invite-uses N] [--invite-days D] [--invite-note <text>]`, `--list-invites`, `--revoke-invite <id>`, `--make-admin <name>`, `--remove-admin <name>` | the account commands (each writes the audit log): each acts on `--data`'s database and exits, also while the server runs (*Accounts* below). `Start-Server.exe` takes the same ones |
| `--status-check 5s` | how often the running server re-reads the status of the logged-in accounts: an account that `--account-status` (run as its own command, also through `Start-Server.exe`) disabled, banned or deleted loses its game session within this time (a match first, then back to the login screen); `0` = never (the change then acts at its next login). The admin page and the website end the session at once anyway |
| `--session-end disconnected\|unauthenticated\|close` | a test aid only: what the client hears before its session is ended; keep the default `disconnected` (the other two leave the client stuck) |
| `--test-login-errors <codes>` | a test aid only: the n-th login after the start is refused with the n-th error code of the list (e.g. `0x0C,0x4072`; `0` = let it through), so the client's *LOGIN ERROR* panel can be seen. Never on a server people play on |
| `--tunnel on\|auto\|off`, `--gateway-port 42128`, `--gateway-key <dir>` | the encrypted tunnel's gateway beside the plain ports: `on` serves it on `--bind`, `auto` only for a public `--public-host`, `off` (the default for `dgserver`; `Start-Server.ini`'s `tunnel = auto` is the release's default). The key is made at the first start in `--gateway-key` (default `--certs`). `--tunnel-client <host:port>` with `--tunnel-address`, `--tunnel-pin`/`--tunnel-pin-cert` runs only the player's side (the test lanes' stand-in for `Start-Client.exe`) |
| `--limits auto\|on\|off` | the rate and count limits (*Limits* below): `auto` (the default) switches them on when `--bind` or `--public-host` is a public address and leaves loopback and LAN servers as they were; `on` forces them (the test lanes' check), `off` never. With them on, the match hosts get `net_limits=1` too |
| `--web-port N`, `--web-domain <name>`, `--web-email <address>`, `--web-dir <folder>`, `--web-acme-url <directory>` | the website: HTTPS on port N (`0` = none, `dgserver`'s default; `Start-Server.ini` `web_port = 443`) with registration, password change, account deletion, data download, the status page, your privacy and terms pages and the admin page (`/admin`). With `--web-domain` (`web_domain`: a name whose A record points at your public address) it gets a Let's Encrypt certificate by itself (TLS-ALPN-01: forward TCP 443 to N); without, a self-signed one (browsers warn once; the fingerprint is in the log and the banner). `--web-dir` (default `<data>\site`) keeps the certificates and takes your `privacy.html`, `terms.html`, `about.html` (HTML fragments shown on those pages). While it runs, the HTTP port sends the game's *Sign up now!* and *Forgot Password?* links (`/dawngate/<page>`) there; with `web_domain` Start-Server's own client copy links to `https://<domain>/<page>` directly, players' copies keep the redirected links |
| `--seconds N` | stop after N seconds |
| `--print-ports` | the TCP ports this configuration listens on |

### Accounts

Players log in with a **login name** (3–32 of `A–Z a–z 0–9 . _ - @`, case doesn't matter; an e-mail address is fine) and a **password** (8–63 characters). The server keeps only an argon2id hash of each password, checks it at every login and never writes a password into a log (`PASS` shows as `***`).
- **Sign-up** (`--signup`, `Start-Server.ini` `signup`): `open` (the default) makes an account at the first login with a new name, with the password typed then, at most `--signups-per-address` (3) a day per internet address; `invite` and `closed` make none at the login: you make them, and with `invite` the players register with your codes on the server's website (`/signup`).
- **Accounts from an earlier build** have no password yet. Under `open` the password of their next login becomes theirs; under `invite`/`closed` give them one with `--reset-password <name>`.
- **Your commands** (`server\bin\dgserver.exe --data <data folder> …` or `Start-Server.exe …`; they print what they did and exit, and work while the server runs):
  - `--create-account <name>`: makes the account with a random password and prints it once. Hand it to the player.
  - `--reset-password <name>`: a player forgot the password (the server sends no mail): prints a new random one once; the old one stops working at once.
  - `--account-status <name>=disabled` (or `banned`, `deleted`, `active`): a disabled or banned player can't log in any more (the game says the account is disabled or banned and to contact the server's operator); `active` lets them in again. A player who is logged in at that moment is thrown out within 5 s (`--status-check`): out of a match (the match goes on for the others; a practice match ends), then back to the login screen; on the admin page at once.
  - `--make-invite --invite-uses 5 --invite-days 14 --invite-note "the club"`: prints a code like `K7QM-3XHP-9WRT-B2NC` once (the server keeps only its hash); `--list-invites` shows every code's uses and expiry, `--revoke-invite <id>` ends one.
  - `--make-admin <name>`: the account (an active one) gets the admin role and can log into the website's **admin page** (`/admin`) with its login name and password; `--remove-admin <name>` takes the role (a logged-in admin's session ends at its next page).
  - Every command that changes something goes into the **audit log** (actor `operator`, source `command line`; never a password or a code), which the admin page shows.
- **The admin page** (on the website; needs `--web-port`): accounts (search; disable, enable, ban; a new password shown once; delete), invite codes (make, list, revoke), the sign-up mode (in force at once and kept across restarts until you change `signup` in `Start-Server.ini`: the most recent change wins), the running matches (players, duration; end one: its players go back to the main menu, nothing is stored), and the audit log (every admin action and login, also refused ones). Its login is limited more strictly than the game's and always (also on a LAN): 5 attempts at once per address, then one per 30 s; 3 wrong passwords for a name free, then a lockout from 1 minute doubling to an hour. A session ends after 30 idle minutes, 12 hours or a restart. A disable, ban or delete there ends the player's running game at once (a match first, then the login screen); its next login is refused.
- Players change their own password, delete their account or download their data on the website (`--web-port`; `Start-Server.ini` `web_port = 443`); a forgotten password stays your `--reset-password` (the site's `/password` page says so).
- **The login screen's links:** *Sign up now!*, *Forgot Password?* and the legal links open `http://<server address>:18080/dawngate/signup`, `/password`, `/terms`, `/privacy` in the player's browser, short pages the server serves itself (how sign-up works in your mode, that you reset forgotten passwords; you have published no terms or privacy notice yet). They need the HTTP port 18080 reachable from the players; a copy set up with `--web-domain <D>` links to `https://<D>/signup` etc. instead; while the website runs the HTTP port redirects these `/dawngate/<page>` links to it.

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
| 42128 | TCP (TLS 1.3) and UDP (AES-GCM) | `dgserver --tunnel on` (`Start-Server.ini` `tunnel = auto` on a public address, `gateway_port`) | tunnelled clients (`Start-Client.exe`): every port above travels inside it |
| 443 | TCP (HTTPS) | `dgserver --web-port 443` (`Start-Server.ini` `web_port`) | browsers: the website; with `web_domain` also Let's Encrypt's validation, so a public server forwards TCP 443 too |

**A public server forwards only the tunnel and the website:** with `tunnel = auto` (the default) or `on`, the router forwards **TCP and UDP 42128** to the server PC and **TCP 443** for the website (required with `web_domain`, whose certificate Let's Encrypt validates there), and nothing else; the other ports stay on `bind` for players on the LAN, unreachable from the internet (*A public server* above).

Clients only open connections (Blaze, QoS, RakNet to the GMS), so client machines presumably need no inbound rule. **Firewall rules are the operator's decision; nothing here changes them.** With a LAN `--bind`, Windows asks once per program path the first time it listens on a non-loopback address (seen in A5b: `dgserver.exe` at its start, the host copy's `Dawngate.exe` at its GMS port, and a client copy whose sockets bind the LAN address; a "Windows Security Alert" each). Until it is answered (or if cancelled) other machines can't reach that program, while clients on the server machine itself still can. If you allow them, limit the rules to the private profile and, where you can, to your LAN's subnet. On the main server allow TCP 10040 only from your host machines' addresses; host machines need no rule for the agent, which only dials out, but their UDP 10042–10051 must be reachable by the clients. A firewall prompt on a loopback-only setup can be cancelled: everything listens on `127.0.0.1` then.

## Limits

A server on a public address limits what one address, connection or player can do (`dgserver --limits`, on by default only there; `Start-Server.exe` passes the public `address` as `--public-host`, so a public server gets them without a setting; `server_flags = --limits on` forces them on a LAN). The values (`server/internal/limits` `Default()`, the match host's `[host] net_limit_*` keys):

| What | Limit | Over it |
|---|---|---|
| New TCP connections per address (redirector, Blaze, QoS, web, telemetry and the tunnel gateway's TLS connections together) | 1 a second, 30 at once | the connection is reset at once (at the gateway before its TLS handshake) |
| Open TCP connections per address | 24 | reset |
| UDP datagrams per address (QoS) | 100 a second (burst 400), 256 KB/s (burst 1 MB) | dropped |
| Tunnel channel datagrams per real address (all of a tunnelled player's UDP, the match included; a client in a match sends at most about 35 a second) | 400 a second (burst 1,200), 256 KB/s (burst 1 MB) | dropped at the gateway before the session lookup and the decryption |
| Tunnel sessions per address | 16, live ones and those in their 60-s grace together | refused (`gateway unavailable`: Start-Client stops with that error) |
| Login attempts per address | 10 a minute (burst 10) | `ERR_SERVER_BUSY` (the client's *LOGIN ERROR* panel, `[40720000]`) |
| Failed logins | per account 5 free, then 30 s doubling to 15 min; per address 20 free, then 1 min doubling to 30 min; a success clears them, an hour of quiet forgets them | `ERR_SERVER_BUSY` |
| Blaze requests per connection | 20 a second (burst 120; a normal login's menus send at most 39 in one second) | slowed down to the rate; a connection held back for 10 s is closed |
| Games a player starts (practice, custom) | 3 a minute (burst 4), at most 2 running games at once | the client's "no game server" error (back to the menu) |
| Matchmaking queues a player joins | 6 a minute (burst 6); one queue at a time as before | the queue's error |
| Match host, per address | 400 datagrams a second (burst 1,200), 128 KB/s; a NAT address's ports together 6 times that; 4 handshake datagrams a second per IP | dropped before RakNet reads them |
| Match host, per connection | 200 GMS messages a second (burst 600); 8 connections per IP; the first GMS message within 30 s | dropped; with drops in 3 of the last 10 seconds the connection is closed and its address blocked for 30 s (the player's client sees a lost connection) |

**Players through the tunnel** count with their **real address**, the source of their TLS connections and UDP datagrams as the gateway sees it: the gateway takes every connection through the same per-address rules before its TLS handshake (a refused one costs no handshake), every channel datagram before it looks up the session or decrypts anything, and caps the sessions per address; the services behind it see the same address, so Blaze's login rate and backoff and the sign-ups per address count each player, not the loopback address every tunnelled game socket has. No address is exempt. Only the match hosts still see every tunnelled player at the server's own address (the gateway's flow sockets, one port per player), so they get it as `net_limit_shared_ips` (per-port rules only there; the gateway has already limited the player's real address before forwarding). Behind one NAT address a household fits (16 sessions, 24 connections), a LAN party of ten through the tunnel would not [assumed: the open-connection limit counts about 3 per player]. Refusals are logged once per address and kind a minute and summed up in a `limits: last minute refused: …` line of `dgserver`'s log (the hosts: a `limits:` line per match minute in their `dgsim.log`). An account's login backoff applies whoever tries it, so someone who knows a login name can keep that player out for up to 15 minutes at a time by failing on purpose; the address limits slow such a try down.

The match host also checks every message a client sends against what the client can send (the **strict message check**: sizes, ranges, the player's own objects and items; a malformed or impossible message is dropped and too many of them close the connection), and its RakNet 4.081 is built with this project's patches (*Distribution and licences* below): receive-path fixes (split messages, ACK/NAK ranges, `RangeList` and `BitStream` reads), handshake cookies, no IP ban of unverified senders and a limit of ordered messages waiting per connection. Both are on in every match.

## What must never be exposed

This started as a preservation server for a trusted LAN. **It can also run on a public address, at the operator's own risk**, and a public server has checked passwords, the encrypted tunnel, the limits, the strict message check and the website (*A public server* above; the public profile turns them on). `Start-Server.exe` prints what that covers and what remains when `address` is public and starts only with `--i-accept-the-risk` (or `i_accept_the_risk = yes` in `Start-Server.ini`); `Start-Client.exe` shows a short notice for a public server. Nothing opens ports or uses UPnP: **you forward the ports yourself**: only the tunnel's TCP and UDP 42128 and the website's TCP 443 (`tunnel = auto`, the default, turns the tunnel on for a public address), or with `tunnel = off` the plain ones (TCP 42127, 10041, 17502, 18080, 9988, UDP 17502 and UDP 10042-10051 for the match hosts), and **never forward TCP 10040** (the match hosts' control channel; `Start-Server.exe` keeps it on `127.0.0.1`, and `dgserver --gms-control-bind` with a non-loopback address still needs `--agent-key`). Behind a router set `address` to the public IPv4 address and `bind` to this PC's LAN address in `Start-Server.ini` (`dgserver --bind <LAN> --public-host <public>`).

- **Blaze is plain TCP** (it checks passwords, but under `signup = open` anyone can make accounts); the redirector's TLS is version 1.1 with MD5-signed, exponent-3 RSA-1024 certificates, because that is what the 2014 client speaks. None of it protects anything against an attacker on the network. **The tunnel carries all of it** (TLS 1.3 streams, an AES-GCM channel for UDP, the server's key pinned by every `Start-Client.exe`): on by default for a public address, so only LAN players without it still talk in the clear.
- **The game's own code is from 2014:** `Dawngate.exe` (the client and every match host) has DEP but no ASLR, and its relocations are stripped, so ASLR can't be switched on. On the match host every client packet passes our hardened RakNet 4.081, `gms/core`'s parsers and the strict message check before the game logic sees parsed values; a client, though, reads whatever its server sends with the 2014 code, so players are only as safe as the server they trust (no fix: the tunnel narrows it to the one pinned server).
- **The control channel** (TCP 10040) has no encryption and, after the agent's registration, no integrity. Keep `--gms-control-bind` at `127.0.0.1` unless you run agents, and then reachable only from your host machines.
- **Private keys stay on the server:** `server\certs\*\ca_key.pem` (whoever has it can impersonate your server to every client patched with that CA), `server_key.pem`, and the agent key (`agent.key`: whoever has it can register a host machine, steer players to it and report match results). Hand out only `ca_modulus.bin`.
- **Logs and accounts:** `server\logs\*` (and the session logs under `re\logs\`) contain login names, the players' addresses and every request (no password: they show as `***`; `--log-days` deletes old logs), the session captures hold the passwords of untunnelled logins (plain TCP); `server\data\accounts.db` (an earlier build's `accounts.json` is kept as `accounts.json.imported` after the import) holds the accounts. Keep them private.
- **Egress:** a patched client contacts nothing beyond the server it was patched for (no EA host, no UPnP; checked on loopback in every session). Check a session's capture with `re\scripts\egress_report.py <session> --allow-host <S>` (on a client machine; `play.ps1 -Server <S>` does it for every session since P2; the server's own captures: `--allow-host <each client>`). Any host address of the machine you chose is accepted there (a public one too; loopback, wildcard and multicast are not).

## Distribution and licences

- **No EA files are shipped by this project.** The repository holds no game file, and nothing it builds contains any. Every machine needs its own `Dawngate.zip` from archive.org; Dawngate is © Electronic Arts. Never redistribute the archive, the extracted folder, the working or host copies (the patched `Dawngate.exe` and config packages are EA's files with a few bytes changed), `server\data\content.json` or anything under `re\out\` (all derived from the game data).
- **Binary distributions of our builds carry the third-party notices:**
  - `dgsim.dll`, `gms_script.exe`, `gms_tests.exe` and the RakNet probe link **RakNet 4.081** statically (BSD licence, © 2014 Oculus VR, Inc., plus its additional patent grant): ship `tools\raknet\RakNet\LICENSE` and `PATENTS` with them. The protocol-5 RakNet in them is a modified copy: the build applies this project's patches (`gms\cmake\raknet_patches\*.patch`: receive-path fixes for split messages, ACK/NAK ranges, `RangeList` and `BitStream` reads, and the handshake and receive hardening: handshake cookies with SipHash-2-4, written from its public specification, no IP ban of unverified senders, a limit of ordered messages waiting per connection) to a copy in the build tree, so `tools\raknet` stays the stock 4.081. The patches are this project's code; the defects were found by SLikeNet (MIT) and public reports, and no SLikeNet code is used. The same notices cover the modified files.
  - `dgserver.exe` contains **Go's standard library and runtime** (BSD-3-Clause, © The Go Authors): ship Go's `LICENSE` with it. It also contains **`modernc.org/sqlite`** and the modules it needs and **`golang.org/x/crypto`** ([ADR-4](adr/0004-outside-go-modules.md): BSD-3-Clause and MIT, SQLite public domain): ship each module's licence files (`make_release.ps1` collects them from the module cache).
  - The MSVC static runtime needs no notice.
  - The release zip (`re\scripts\make_release.ps1`) is a binary distribution: it carries `LICENSE.txt` (AGPL-3.0, this project's code), `THIRD-PARTY-NOTICES.txt` (RakNet's `LICENSE` and `PATENTS` with a note on this project's patches, Go's `LICENSE`, the licence files of every outside Go module the two programs link) and `BUILD.txt` (the source commit). Under the AGPL, whoever hands it out also offers the source of that commit. The script refuses to put any game file into it (by name and by SHA-256 against both builds of `file_manifest.csv`).

## When something goes wrong

- `setup_all.py --check` says what is missing or stale; the same command without `--check` fixes it.
- A host that doesn't start: `[host]` lines in the server log, then `<--gms-data>\<match id>\dgsim.log` and `stdout.txt`; the error reply to PRACTICE is "Game creation failed - no servers available".
- The client can't connect or stays at "Initializing": the certificates and the client's patch don't match (another CA, or a CN other than the address the client was patched for).
- A match that ended by itself, its players back at the main menu (since H5): the server log's `[host] match N: host error 2 (tick stalled, fatal true): …` and `game N: the match host gave up` lines mean the host's watchdog found its server tick stalled for `watchdog_s` (10) seconds; `host error 3 (step failing …)` that one step of its frame threw in every run for `step_fail_s` seconds. `<--gms-data>\<match id>\watchdog.txt` and `watchdog.dmp` say where it hung; the port is free again at once. `--gms-options "watchdog_s=0"` turns the watchdog off (a hung host then ends only by the 30-s heartbeat timeout). Seen live in H5-live (2026-10-09): the lines exactly so, the match ended about 10 s after the freeze, the players at the main menu, the next game on the same port; `re\scripts\minidump_stack.py <dir>\watchdog.dmp` prints the hung thread's stack.
- A match behaves differently from what [PLAYING.md](PLAYING.md) describes and you suspect the headless host: start `dgserver` with `--gms-options "core_only=0"` (stage 1, the full client start-up) and compare; a host's `dgsim.log` says which it ran (`host: stage 2 (H1-live): … core_only 1`).
- More in [PLAYING.md](PLAYING.md) *Troubleshooting*.
