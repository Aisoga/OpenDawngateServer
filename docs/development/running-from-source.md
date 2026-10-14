# Running from source

This page assumes the setup from [building.md](building.md) is done. Everything here runs from the repository folder.

- [Playing: play.ps1](#playing-playps1)
- [The double-click launchers](#the-double-click-launchers)
- [Match host test options](#match-host-test-options)
- [Session logs](#session-logs)
- [The check mode](#the-check-mode)
- [Running the server directly](#running-the-server-directly)
- [Playing on a server on another PC](#playing-on-a-server-on-another-pc)
- [Extra host machines](#extra-host-machines)
- [Automated test sessions](#automated-test-sessions)

## Playing: play.ps1

```powershell
powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 [-ShaperSelect]
```

One command does everything:

1. It checks the setup. It rebuilds the match host and installs the new `dgsim.dll` when the sources changed. For anything else missing, it prints the command to run.
2. It builds and starts the server, then starts the game in a normal window.
3. When you close the game or press **Ctrl+C**, it cleans up: the game, the match hosts, the server and the packet capture. In a match, Ctrl+C closes the game after 10 seconds; a second Ctrl+C stops at once.

Without `-ShaperSelect` you play Fenmore and skip shaper select. Wait for the summary before closing the PowerShell window:

```
==== Dawngate session play_20261008-160000 over: OK
  session : ended when the game was closed at t=1230 s (code 0) after 21 min
  egress  : 0 flagged line(s) (egress.txt)
  cleanup : 0 leftover(s) stopped before the run; stopped after it: dgserver.exe x1, dumpcap.exe x1; ports free, no session process left
  logs    : <repo>\re\logs\play_20261008-160000
```

- **egress** must say `0 flagged line(s)`. `EGRESS FLAGGED` (exit code 1) means something tried to leave the PC. Keep the folder; `egress.txt` lists the traffic.
- **cleanup** must end in `ports free, no session process left`.

| Option | Effect |
|---|---|
| `-ShaperSelect` | Shaper select before the match. |
| `-NoCapture` | No packet capture and no egress check, for a PC without Wireshark. |
| `-KeepAlive` | Prevents the idle kick by sending the game a harmless key release (F24) every 60 seconds. Useful for watching a match without playing. |
| `-HostOptions 'key=value;…'` | Settings for the match host (see [below](#match-host-test-options)). |
| `-Seconds N` | End the session after N seconds. |
| `-Name <name>` | The logs folder's name. |
| `-SetupOnly` | Only check (and build) the setup. |
| `-Check` | The non-interactive check (see [below](#the-check-mode)). |
| `-Server <S>` | Play on a server on another PC (see [below](#playing-on-a-server-on-another-pc)). |

Only one session runs at a time: a second one stops with exit code 2. A session ends after 8 hours at the latest.

To try purchases with an account that doesn't own everything, run the session through `run_session.ps1` with `-ServerArgs '--owned starter'` (see [Automated test sessions](#automated-test-sessions)).

## The double-click launchers

`Start-Server.cmd` and `Start-Client.cmd` in the repository folder do the same as the release's programs, but for a checkout:

- **Start-Server.cmd** checks and builds the setup like `play.ps1 -SetupOnly`, then runs the server in its own window with shaper select. Stop it with Ctrl+C or by closing the window.
- **Start-Client.cmd** starts the game against it. Start the client again after a match or a crash; the server keeps running.

There's no packet capture or egress check here. Logs go to `re\logs\manual_<date>-<time>\`. `re\scripts\start_server.ps1 -NoShaperSelect` skips shaper select, and `-ServerArgs '<flags>'` passes [server options](../reference.md#extra-server-options-server_flags).

## Match host test options

`play.ps1 -HostOptions 'key=value;key=value'` passes settings to the match host. The same keys work under `[host]` in `client\run_854_server\Dawngate\game\dgsim.ini`, or for a whole server with `dgserver --gms-options`. They're meant for testing; a normal match needs none of them.

| Key | Effect |
|---|---|
| `bots=0`, `bots=N` | No bots, or N bots a team |
| `fog=0` | No fog of war: everything is visible |
| `fog_terrain=0`, `fog_grass=0`, `fog_stealth=0` | Walls, tall grass or stealth don't block vision |
| `fog_pursuit=0` | Units chasing into fog walk to the middle of the map, the older behaviour |
| `fow_data_by_vision=0` | Enemies' levels and items aren't sent by vision |
| `wells_lock_first=45` | Spirit wells can be captured after 45 seconds instead of 15 minutes |
| `wells_damage_pause_any=1` | Any damage pauses a well capture, not just enemy shapers |
| `surrender_after_s=20` | Surrender is available after 20 seconds |
| `team_left_surrender_s=N` | A team whose players all left surrenders after N seconds (default 60) |
| `prepare_flags=1` | Show the "Match Found" accept/decline prompt before a queue match |
| `move_takeover=0` | Bring back the original's rule that a move waits for an attack-move's first hit |
| `role_passives=0` | Switch the roles' passives off |
| `banter=0`, `banter_window=0` | No banter lines, or banter at any time of the match |
| `test_wave=20` | Spawn test minions after 20 seconds |
| `grant_xp=2200` | Give each player enough experience to rank all four abilities |
| `end_after_s=N` | End the match after N seconds |
| `watchdog_s=0` | Switch off the watchdog that ends a stalled match |
| `strict_check=2` | Count invalid client messages without dropping them (`0` switches the check off) |
| `core_only=0` | Start the match host as a full hidden client instead of headless: about 1 GB of memory and 5–8 seconds to start instead of 100 MB and 2 seconds. Use it to rule out the headless mode as the cause of a problem. |
| `commands_file=<file>` | Play scripted commands from a file (used by `re\scripts\soak.py`) |

## Session logs

Everything from a `play.ps1` session is in `re\logs\play_<date>-<time>\`:

| File | Contents |
|---|---|
| `summary.txt` | The full summary: game states, server requests, the match host, egress, cleanup |
| `application_log.txt` | The client's log for this session (its full log is `%APPDATA%\DawngateData\Application.log`) |
| `stub.log` | The server's log: logins, every request, the match hosts' control messages, telemetry |
| `host\<n>\dgsim.log` | Match *n*'s host: the simulation, the network session, replication, deaths, the match end |
| `host\<n>\host_timing.csv` | One row per match minute: tick times, traffic, command latency. `soak.py timing re\logs\<session>` makes a table of it. |
| `host\<n>\watchdog.txt`, `watchdog.dmp` | Only after a stalled match: where the host hung. `re\scripts\minidump_stack.py` reads the dump. |
| `capture.pcapng`, `egress.txt` | The packet capture (256 bytes per packet) and the egress check's report |
| `session.log`, `monitor.log` | What the scripts did |

Accounts are in `server\data\accounts.db`. The match hosts use their own AppData folders under the session folder, so they never touch your client's settings.

## The check mode

```powershell
powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 -Check [-Seconds 150]
```

The same path as playing, with a scripted login (a new account each time) and clicks to PRACTICE DAWNGATE. It only posts window messages and keeps the game window off the desktop, so your mouse and keyboard stay free. After `-Seconds` it closes the game.

The check passes (exit 0) when the client got into the match, the match host reported it was ready, egress is clean and the cleanup left nothing behind. Exit code 3 means the session was clean but the client didn't get into the match.

## Running the server directly

```powershell
server\bin\dgserver.exe --gms-dir client\run_854_server --gms-data server\logs\hosts
```

Without `--gms-dir` the server starts no match hosts, and PRACTICE can't start a match. `dgserver.exe -h` lists every option. The operator options are in [reference.md](../reference.md#extra-server-options-server_flags).

**For players on your LAN**, where `<S>` is this PC's LAN address:

```powershell
.venv\Scripts\python -I re\scripts\setup_all.py --lan-certs <S>     # once: certificates for <S> in server\certs\lan
server\bin\dgserver.exe --host <S> --certs server\certs\lan --gms-dir client\run_854_server --gms-data server\logs\hosts
```

The game checks that the server's certificate names exactly the address it was patched for. So after a change of address, make new certificates with `--lan-certs <new S> --new-certs`, and every player patches their copy again. Give players the address and the public file `server\certs\lan\ca_modulus.bin`.

## Playing on a server on another PC

On the player's PC you need the repository, `Dawngate.zip`, the Python environment and, for the egress check, Wireshark. Go, Visual Studio and RakNet aren't needed.

```powershell
.venv\Scripts\python -I re\scripts\setup_all.py --player --server-host <S> --ca <path to ca_modulus.bin> [--client-bind <C>]
powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 -Server <S>
```

The first command makes `client\run_854_lan\Dawngate`, a copy patched for the server `<S>`. With `--client-bind`, its sockets use only this PC's address `<C>`. `play.ps1 -Server` starts that copy without a local server, records the session and checks that the game talks to nothing but `<S>`.

If the operator runs extra host machines, add them with `-AllowHost <H1>,<H2>`, so the egress check allows them too.

## Extra host machines

Matches can run on other machines, while logins and menus stay on the main server. Set up each host machine like any server (`setup_all.py --no-content`), then run:

```powershell
# once, on the main server S: a shared key; copy it to each host machine over a trusted channel
server\bin\dgserver.exe --make-agent-key server\data\agent.key
# the main server
server\bin\dgserver.exe --host <S> --certs server\certs\lan --agent-key server\data\agent.key --gms-control-bind <S> [--gms-dir client\run_854_server]
# each host machine H
server\bin\dgserver.exe --agent <S>:10040 --agent-key agent.key --gms-dir client\run_854_server --host <H> [--agent-name H1]
```

Each new match goes to the machine with the most free match ports. Players connect to the host machine for the match itself. If a host machine or its link fails, its matches end.

On the main server, allow TCP 10040 only from your host machines, and keep `agent.key` private: whoever holds it can register a host machine. This setup hasn't been tried across real machines yet.

## Automated test sessions

`re\scripts\run_session.ps1` is the test harness behind `play.ps1`. It builds and starts the server, launches the client (optionally scripted: `-Login`, `-Actions`), captures the traffic, checks egress and cleans up. Its options are described in the comment at the top of the script.

`re\scripts\setup_lane.py` sets up extra, isolated test lanes: their own client, server and host copies on `127.0.0.2`–`127.0.0.4`. They let several sessions run side by side.
