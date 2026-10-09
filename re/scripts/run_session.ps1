<#
  One-command test session (roadmap item A2): server, game launch, egress check, log collection,
  cleanup and a short summary. Replaces a manual test-run routine.
  Since S1 the server is the Go binary server\bin\dgserver.exe (ADR-1), built here at the start.

  Steps:
   1. Safety: the game dir must not be under client\ia_*, and patch_client.py / patch_config.py must
      have patched it (Dawngate.exe, Config.package and Config_Ship.package each have a *.orig backup
      and differ from it). The session folder re\logs\<Name> must not exist yet.
   2. Builds the server: go build -C server -o bin\dgserver.exe .\cmd\dgserver (build.out/build.err
      in the session folder; a failed build stops the session with exit 2), then asks it for its
      ports (dgserver.exe --print-ports). A leftover dgserver.exe would lock the binary, so it is
      stopped first.
   3. Leftovers: stops processes left over from earlier sessions, and only those: dgserver.exe from
      the repo's server\bin\, Dawngate.exe / CrashSender1402.exe started from the repo's client\
      folder, powershell running launch_monitor.ps1 (-File), dumpcap writing into re\logs\. Then every
      server port must be free; if some other program holds one, the session stops and that program
      is left alone.
   4. Starts dgserver.exe (log in the session folder) and waits until all its ports listen.
   5. Runs launch_monitor.ps1 into the session folder (capture, screenshots, closes the game), stops
      the server, and waits for the monitor's dumpcap to finish its capture.
   6. Cleanup: stops whatever session process is still running and checks that none is left and the
      ports are free.
   7. Runs egress_report.py on the session (egress.txt). A flagged line fails the session loudly.
   8. Collects: the server log becomes stub.log (the name predates S1), this run's section of
      %APPDATA%\DawngateData\Application.log becomes application_log.txt (the screenshots, monitor.log
      and capture.pcapng are already there), then prints a short summary and saves it as summary.txt.
      Its 'sockets' line (A4) lists the local addresses of the game's sockets seen in monitor.log:
      '127.0.0.1 only' for a copy patched by patch_client.py, else the endpoints bound wider.
  Since Q3b the session also samples its client (-GameDir's Dawngate.exe, not a second client) every 5 s into
  client_samples.csv: private bytes, working set, virtual size, the 32-bit address space (used, committed, the largest
  free block, read with VirtualQueryEx), handles, CPU, the other clients running, and every 15 s Windows 10's GPU
  counters (the process's dedicated and shared usage, every process's dedicated usage); the summary's 'client res'
  line has the maxima (soak.py report prints them per match minute).

  The ports come from dgserver.exe --print-ports, so new server ports are covered automatically;
  currently 42127, 10041, 17502, 18080, 9988.

  -GmsProbe silent|connected (B9) serves the GMS port (UDP 10042) with RakNet 4.081 instead of
  dgserver's logger: dgserver gets --gms-external (announces the port, doesn't bind it), and
  tools\raknet\build\Release\raknet_probe_p5.exe --listen 10042 <s> [--connected] runs beside it
  (build: re\scripts\raknet_probe\CMakeLists.txt). Its output is gms_probe.out in the session folder;
  the summary gets a 'gms probe' line. 'connected' answers a new connection with the GMS message
  Connected (0x84), which makes the client send HelloReq (docs/GMS_PROTOCOL.md).
  'session' (D2b-live; since G1 the gms/ binary) runs the scripted GMS session instead:
  gms\build\Release\gms_script.exe --port 10042 --seconds <s> (gms/script, session in gms/core/session:
  HelloPlayer, GamePrepareForStart, ClockSync answers, EnterPreGameFlow, pre-game 01/03, EnterGame);
  -GmsArgs adds options for it, e.g. -GmsArgs '--script-a' or '--stop-after pregameflow --step-ms 2000'
  or '--script-a --inject-file <absolute path>' (see gms/script/main.cpp). Build it first
  (gms/CMakeLists.txt); the session stops if its sources are newer than its last successful build (G5: the build
  stamp, re\scripts\gms_stale.ps1). Its output is
  gms_script.out, and the summary's 'gms probe' line ends with the session's last state line.
  -GmsArgs is split at blanks, so paths in it must not contain any. Start this script from
  PowerShell directly: Start-Process -ArgumentList drops what follows the first blank of -GmsArgs.

  -HostDir client\run_854_server\Dawngate (D1d) also runs the match host beside the client: that copy's
  Dawngate.exe with dgsim.dll in host mode (ADR-2 stage 1, docs/SERVER_MODE.md *Host mode, stage 1 (D1d)*),
  started with -multipleInstances and the environment DGGMS_MATCH=<Name>, DGGMS_BIND and DGGMS_DATA=<session>\host
  (its dgsim.log and its own AppData/Documents folders). This script holds the launcher event
  Global\Dawngate L2G for it. The host starts -HostAfter seconds (default 4) after the monitor, once the
  monitor's capture runs, so the egress check covers it; the client starts at about 3 s. Every second the
  script records the host's TCP/UDP endpoints, memory, CPU time and whether it shows a window
  (host_samples.csv); the host is stopped when the monitor ends. The summary gets 'host' lines (startup
  times, resources, sockets, window) and the 'dgsim' line reads the host's log.
  Since G2 the host serves the GMS port itself: without -GmsProbe it gets DGGMS_BIND=127.0.0.1:10042 and
  dgserver gets --gms-external (it announces the port, the host binds it once its level is loaded); the
  summary's 'host gms' line is the 'gms probe' line of the host's session (from its dgsim.log). With
  -GmsProbe the host gets DGGMS_BIND=off and the probe or gms_script serves the port (D1d's setup). The
  host copy's PhysXLoader.dll must be the current gms\build\Release\dgsim.dll (install it with
  setup_server_copy.py), and no file in gms\host, gms\engine, gms\core, gms\script, gms\CMakeLists.txt or gms\cmake may be newer
  than that build's last successful build (G5: dgsim.dll.stamp, written by every successful build even when MSBuild
  doesn't relink an unchanged DLL; re\scripts\gms_stale.ps1).
  -HostOptions 'key=value;...' becomes DGGMS_OPTIONS, which overrides the host copy's dgsim.ini [host] keys for
  this run, e.g. 'inject_file=D:\...\world_shaper_loaded.txt' (D2e's world, sent by the host at the client's
  0x401) or 'lua_probes=1' (D1a-live's test minions and tower attack; off by default in host mode).

  -GmsHost <host> (C1b) lets dgserver start the match host itself, as in production (ADR-2 decision 7,
  docs/HOST_CONTROL.md *The host side (C1b)*): dgserver gets --gms-dir <host> --gms-args <-GmsHostArgs>
  --gms-data <session>\host [--gms-options <-HostOptions>], so PRACTICE DAWNGATE's resetDedicatedServer starts the
  host, which dials the control channel (TCP 127.0.0.1:10040, checked like the other server ports), gets createGame,
  binds the pool's GMS port and answers gameReady, and dgserver then sends NotifyGameSetup. <host> is either a host
  copy (client\run_854_server or its Dawngate folder; the same dgsim.dll checks as -HostDir; -GmsHostArgs defaults to
  -multipleInstances) or gms\build\Release\gms_script.exe (the scripted GMS in host mode; -GmsHostArgs defaults to
  --script-a; test options --finish-after S, --kill-after S, --exit-before-ready, --ready-delay-ms N, see
  gms/script/main.cpp). Any other existing file is taken as is, so every launch fails (the error-reply test). Each
  match's data folder is host\<match id> (dgsim.log, AppData, stdout.txt). The script finds the host's pid in
  dgserver's log, samples it like -HostDir, and after the monitor has closed the client waits up to 20 s for the
  host to end by itself (dgserver aborts the match once the player's Blaze connection is gone). The summary gets
  'host' (started by dgserver, how it ended), 'host ctl' (dgserver's [host] lines: hello, ready, the messages each
  way, NotifyGameSetup's delay after resetDedicatedServer, the end) and, for a Dawngate host, the -HostDir lines.
  -HostKillAt <s> kills the host (-GmsHost or -HostDir) that many seconds after the monitor started (a host crash in
  a game: dgserver ends the match as aborted and sends NotifyPlayerRemoved GAME_DESTROYED).
  -ServerArgs '<dgserver flags>' (D8-live, a test aid) adds flags to dgserver's command line, split at blanks, e.g.
  '--owned starter' (new accounts own only the starter items, so a post-game's loot is not a duplicate) or
  '--ftue-done' (every account past the Practice-only gate, B7b); they come after the script's own flags, so a flag
  given twice takes this value. The summary's 'server' line shows them with dgserver's accounts and content line.
  -FreshData (B7b) gives dgserver an empty accounts folder of this session's own (re\logs\<Name>\data, with the content
  file of the main checkout's server\data\content.json), so every login is a fresh account and nothing is kept in the
  lane's accounts.json. Without it the lane's accounts persist across sessions (B7a), which a server restart test needs.
  The 'telemetry' line (R5-live) counts the client's decoded LABS_* events in dgserver's log, the packets it could
  not decode, and the client's telemetry asserts.

  -Play (P1, docs/PLAYING.md; normally started through re\scripts\play.ps1) is a session for playing by hand: it needs
  -GmsHost (dgserver starts the match host at PRACTICE), and -Seconds defaults to 0 = no time limit: the monitor watches
  the game until it exits and never closes it (launch_monitor.ps1 -Seconds 0), takes no timed screenshots and keeps
  256 bytes per packet in the capture (enough for the egress check; a long session's capture stays small). The session
  ends when the game is closed; Ctrl+C in this console closes the game (in a match it is stopped: it ignores WM_CLOSE
  there) and ends the session in the same order (host, server, capture, egress check, summary); a second Ctrl+C stops the
  monitor at once. With -Seconds N the monitor still has no limit: this script closes the game after N s the way Ctrl+C
  does (play.ps1 -Check runs that path). Ctrl+C arrives as a key ([Console]::TreatControlCAsInput), so no child process gets a console
  signal; without a console (redirected input) only the finally block's cleanup covers an interrupted run. The capture
  is stopped once the server is down (its autostop is an 8-hour safety net, like dgserver's --seconds). -NoCapture
  (only with -Play) starts no capture and runs no egress check; every test session is captured.
  One session at a time per lane: every session writes its lane's lock (re\logs\session.lock for lane a) with pid,
  process start, name, mode and lane, and removes it at the end; a session refuses to start (exit 2) while the lock names
  a live process, so a test session never stops a running play session's processes as leftovers, and vice versa.

  -Lane a|b|c|d (A6, A6b; docs/RUNNING_THE_CLIENT.md *Test lanes (A6, A6b)*): up to four sessions can run at the same
  time, one per lane. Lane a (the default) is everything above: client\run_854, client\run_854_server, 127.0.0.1,
  server\bin\dgserver.exe, %APPDATA%\DawngateData, re\logs\session.lock. Lane X = b, c or d (set up by
  re\scripts\setup_lane.py X; b = 2, c = 3, d = 4) has client\run_854_X, client\run_854_server_X, 127.0.0.N for every
  port (dgserver --bind/--public-host, the control port, the hosts' GMS ports), server\bin\lane_X\dgserver.exe with
  server\certs\lane_X and server\data\lane_X, the client started with -multipleInstances, %APPDATA%\DawngateDataX and
  re\logs\session_X.lock; a session in lane b, c or d refuses to start while the user plays (a live play lock in
  lane a). Each session folder gets lane.txt. Leftovers are only this lane's: Dawngate.exe and CrashSender by copy,
  dgserver.exe and gms_script.exe by their --bind (dgserver's --host when --bind isn't given, since P2-live; or their
  dgserver's), dumpcap and the monitor by their session folder's lane. Ports are checked on the lane's address only. The egress check gets --lane-address.
  The checkout this script runs from provides the code (server\ and gms\ builds, the scripts); client\, .venv, tools\,
  re\logs and server\certs\, server\data\ come from the main checkout, so a package in lane b, c or d can test from
  its worktree.
  -GmsProbe silent|connected is lane a only (raknet_probe binds 127.0.0.1); -GmsProbe session binds the lane's address.

  -Lan <IPv4>|auto (A5b; docs/RUNNING_THE_CLIENT.md *Playing across machines (A5)*) runs the session on the LAN profile
  of this PC: dgserver binds and announces this PC's own LAN address (--bind/--public-host <IPv4>, --certs
  server\certs\lan with CN <IPv4>; the hosts' control port stays on the lane's loopback address), the client is the lan
  copy client\run_854_l (lan profile, its sockets narrowed to <IPv4>, %APPDATA%\DawngateDataL, -multipleInstances; set
  up by setup_lane.py lan --address <IPv4>), -HostDir's DGGMS_BIND and -GmsProbe session's --bind are <IPv4>, the egress
  check gets --allow-host <IPv4> (this PC's own address only; never the router or another device), and the 'sockets'
  line expects <IPv4>. 'auto' takes the IPv4 address of the interface with the default route. One lan session at a time
  on the PC, whatever its lane (re\logs\session_lan.lock, beside the lane's own lock); the lan copy's processes and a
  dgserver or gms_script bound to <IPv4> belong to the lan session. Windows Firewall may ask about dgserver.exe and the
  copies' Dawngate.exe when they first listen on the LAN address: that is the user's call (we change no rules), and
  same-machine traffic to the own LAN address works either way.
  -SecondClient (A5b) starts the lane's second client copy beside the first (lane d: client\run_854_e, bound to
  127.0.4.2, %APPDATA%\DawngateDataE, set up by setup_lane.py e; since G3b lane b: client\run_854_f, 127.0.2.2,
  %APPDATA%\DawngateDataF, setup_lane.py f) with its own launch_monitor.ps1 (no capture of its own:
  the first monitor's capture is system-wide), -SecondAfter s (default 24) after the first: the second game window
  activates itself when it appears, and a deactivated window takes no posted WM_CHAR (a5b_run2d: the first client's
  e-mail arrived empty with 2 s), so the first login must be done by then; -Login2, -LoginAt2 (default
  -LoginAt), -Actions2 and -ShotTimes2 are its -Login, -LoginAt, -Actions and -ShotTimes. Its monitor.log, screenshots
  and application_log.txt go to the session's client2\ folder; the egress check reads both monitors' game endpoints and
  counts the second client's address as the session's own; the summary gets 'client2', 'sockets2', 'app log2' and
  'login2' lines. Not with -Lan or -Play.
  -SecondRestartAt <s> (G3) ends the second client's game s seconds after its start (its monitor's -Seconds: in a
  match the game ignores WM_CLOSE and is killed, a lost connection for Blaze and the GMS) and then starts the same copy
  again with a new monitor (folder client2b\, -Login2 again, -Actions3 and -ShotTimes3), for the rest of the session:
  the reconnect test (docs/BLAZE.md *Rejoining a running game (G3)*). The egress check reads its monitor.log too; the
  summary gets 'client2b' and 'app log2b' lines.

  Usage (from anywhere):
    powershell -ExecutionPolicy Bypass -File re\scripts\run_session.ps1 [-Name a2_run1] [-Seconds 60]
        [-GameDir client\run_854\Dawngate] [-Www <folder the server serves on its HTTP port>]
        [-Login 'email|password' [-LoginAt 12]] [-Actions '<t>:<fx>,<fy>;<t>:key=<vk>;...'] [-ShotTimes '8,20']
        [-GmsProbe silent|connected|session [-GmsArgs '<gms_script options>']]
        [-HostDir client\run_854_server\Dawngate [-HostAfter <s>] [-HostOptions 'key=value;...']] [-Background]
        [-ShotFocus auto|on|off]
        [-GmsHost client\run_854_server|gms\build\Release\gms_script.exe [-GmsHostArgs '...'] [-HostOptions '...']]
        [-HostKillAt <s>] [-ServerArgs '<dgserver flags>'] [-FreshData] [-KeepAlive <s>] [-Play [-NoCapture]]
        [-Lane a|b|c|d] [-Lan <IPv4>|auto] [-SecondClient [-SecondAfter <s>] [-Login2 'email|password' [-LoginAt2 <s>]]
        [-Actions2 '...'] [-ShotTimes2 '...'] [-SecondRestartAt <s> [-Actions3 '...'] [-ShotTimes3 '...']]]
  With -Lane b the default -GameDir is client\run_854_b\Dawngate and the host copy is client\run_854_server_b (lanes c
  and d likewise: client\run_854_c, client\run_854_server_c, ...).
  -Seconds is launch_monitor's watch time; the whole session takes about 1.15 * Seconds + 60 s.
  -Login / -LoginAt are passed to launch_monitor.ps1: it types the credentials into the login screen
  and clicks LOGIN (posted window messages only). Use dummy credentials; the server logs them.
  -Actions / -ShotTimes are passed on too (B8): timed clicks and key presses after login, and the
  screenshot times; see launch_monitor.ps1 for the format.
  -Background (D7-live) is passed on as well: the monitor keeps the game window off the desktop, out of the user's
  way. In game the client aims at the real cursor (GetCursorPos), so posted right-clicks and aimed casts depend on the
  user's pointer: send moves and aimed casts from the test side instead (-HostOptions 'commands_file=<path>').
  -KeepAlive <s> (B12) makes the monitor post a lone key-up of VK_F24 to the game every <s> s (launch_monitor.ps1
  -KeepAlive), which keeps the client's AFK kick (5 minutes without input in a game) from firing. Default -1 =
  automatic: 60 in a test session with -Seconds > 300, off in shorter ones and in play mode (play.ps1 -KeepAlive opts
  in); 0 turns it off (to watch the kick). Moves from the test side (commands_file) don't count as input for the
  client. The 'game' line counts the posts; the 'app log' line flags kGameStateDungeon -> kGameStateLogin (the kick).
  -ShotFocus auto|on|off (V1) is passed on: with 'on' the monitor brings the game window on-screen and to the foreground
  for each screenshot (about 0.5 s each) and gives the foreground back afterwards, since PrintWindow gets new frames only
  where the window lies on a monitor (V1-live; parked it is black or frozen); 'auto' (default) means on without
  -Background, off with it. The 'shots' line says how
  the screenshots were taken (window/screen) and whether one didn't get the focus.
  Exit code: 0 clean session; 1 egress_report.py flagged something (stop launching, fix that first);
             2 setup or run failure (reason in the summary).
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [string]$Name = ('session_' + (Get-Date -Format 'yyyyMMdd-HHmmss')),
    [int]$Seconds = 60,
    [string]$GameDir,
    [string]$Www,
    [string]$Login,
    [int]$LoginAt = 12,
    [string]$Actions,
    [string]$ShotTimes,
    [ValidateSet('', 'silent', 'connected', 'session')][string]$GmsProbe = '',
    [string]$GmsArgs,
    [string]$HostDir,
    [int]$HostAfter = 4,
    [string]$HostOptions,
    [string]$GmsHost,
    [string]$GmsHostArgs,
    [int]$HostKillAt = 0,
    [switch]$Background,
    [ValidateSet('auto', 'on', 'off')][string]$ShotFocus = 'auto',
    [string]$ServerArgs,
    [switch]$FreshData,
    [int]$KeepAlive = -1,
    [switch]$Play,
    [switch]$NoCapture,
    [ValidateSet('a', 'b', 'c', 'd')][string]$Lane = 'a',
    [string]$Lan,
    [switch]$SecondClient,
    [int]$SecondAfter = 24,
    [string]$Login2,
    [int]$LoginAt2 = 0,
    [string]$Actions2,
    [string]$ShotTimes2,
    [int]$SecondRestartAt = 0,
    [string]$Actions3,
    [string]$ShotTimes3
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'gms_stale.ps1')  # G5: Get-GmsBuildStale, the gms\ build check
# the code under test (server\, gms\ builds, these scripts) comes from this checkout; the shared, gitignored state
# (client\ copies, .venv, tools\, re\logs, server\certs, server\data) from the main checkout, also when this checkout is
# a git worktree (A6: a lane b, c or d package tests from its own worktree)
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$MainRoot = $RepoRoot
if (-not (Test-Path (Join-Path $RepoRoot 'client'))) {
    try {
        $common = "$(& git -C $RepoRoot rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
        if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'client')) { $MainRoot = $cand } }
    } catch { }
}
# A6: the test lanes (re\scripts\setup_lane.py has the same table; test_a6.py checks they agree)
$LaneTable = @{
    a = @{ Address = '127.0.0.1'; Client = 'client\run_854\Dawngate'; HostCopy = 'client\run_854_server\Dawngate'; Certs = 'server\certs'; Suffix = ''; Data = 'server\data'; ServerExe = 'server\bin\dgserver.exe'; Lock = 're\logs\session.lock'; GameArgs = '' }
    b = @{ Address = '127.0.0.2'; Client = 'client\run_854_b\Dawngate'; HostCopy = 'client\run_854_server_b\Dawngate'; Certs = 'server\certs\lane_b'; Suffix = 'B'; Data = 'server\data\lane_b'; ServerExe = 'server\bin\lane_b\dgserver.exe'; Lock = 're\logs\session_b.lock'; GameArgs = '-multipleInstances' }
    c = @{ Address = '127.0.0.3'; Client = 'client\run_854_c\Dawngate'; HostCopy = 'client\run_854_server_c\Dawngate'; Certs = 'server\certs\lane_c'; Suffix = 'C'; Data = 'server\data\lane_c'; ServerExe = 'server\bin\lane_c\dgserver.exe'; Lock = 're\logs\session_c.lock'; GameArgs = '-multipleInstances' }
    d = @{ Address = '127.0.0.4'; Client = 'client\run_854_d\Dawngate'; HostCopy = 'client\run_854_server_d\Dawngate'; Certs = 'server\certs\lane_d'; Suffix = 'D'; Data = 'server\data\lane_d'; ServerExe = 'server\bin\lane_d\dgserver.exe'; Lock = 're\logs\session_d.lock'; GameArgs = '-multipleInstances' }
}
# A5b: further client copies of a lane (setup_lane.py's EXTRA_CLIENTS; test_a6.py checks they agree): lane d's second
# client, since G3b lane b's too. A letter no lane uses, so every script since A6b counts the copy as nobody's but its
# lane's.
$ExtraClients = @{
    e = @{ Lane = 'd'; Client = 'client\run_854_e\Dawngate'; Bind = '127.0.4.2'; Suffix = 'E' }
    f = @{ Lane = 'b'; Client = 'client\run_854_f\Dawngate'; Bind = '127.0.2.2'; Suffix = 'F' }
}
# A5b: the LAN profile's test copy (setup_lane.py's LAN) and the PC-wide lock of the lan sessions
$LanCopy = @{ Client = 'client\run_854_l\Dawngate'; Suffix = 'L'; Certs = 'server\certs\lan'; Lock = 're\logs\session_lan.lock' }
$LaneCfg = $LaneTable[$Lane]
$LaneAddr = $LaneCfg.Address
$LanAddr = $null  # -Lan: this PC's LAN address
if ($Lan) {
    if ($Lan -eq 'auto') {
        $route = Get-NetRoute -DestinationPrefix 0.0.0.0/0 -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1
        $Lan = if ($route) { "$((Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $route.ifIndex -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress)" } else { '' }
    }
    $ip = $null
    if (-not [Net.IPAddress]::TryParse($Lan, [ref]$ip) -or $ip.AddressFamily -ne 'InterNetwork') { Write-Host "-Lan '$Lan' is not an IPv4 address (or auto)" -ForegroundColor Red; exit 2 }
    $b = $ip.GetAddressBytes()
    $private = $b[0] -eq 10 -or ($b[0] -eq 172 -and $b[1] -ge 16 -and $b[1] -le 31) -or ($b[0] -eq 192 -and $b[1] -eq 168) -or ($b[0] -eq 169 -and $b[1] -eq 254)
    if (-not $private) { Write-Host "-Lan $Lan is not a private address of our own network (loopback, public and other addresses are refused)" -ForegroundColor Red; exit 2 }
    if (-not @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $Lan -ErrorAction SilentlyContinue).Count) { Write-Host "-Lan $Lan is not an address of this PC (only this PC's own LAN address may be used)" -ForegroundColor Red; exit 2 }
    $LanAddr = $Lan
}
$ServerAddr = if ($LanAddr) { $LanAddr } else { $LaneAddr }  # where dgserver binds and what it announces
$Second = $null  # -SecondClient: the lane's extra client copy
if ($SecondClient) {
    $Second = @($ExtraClients.Values | Where-Object { $_.Lane -eq $Lane }) | Select-Object -First 1
    if (-not $Second) { Write-Host "-SecondClient: lane $Lane has no second client copy (lanes with one: $(@($ExtraClients.Values | ForEach-Object { $_.Lane }) -join ', '); setup_lane.py's EXTRA_CLIENTS)" -ForegroundColor Red; exit 2 }
    if ($Lan) { Write-Host '-SecondClient is not for -Lan: this PC has one LAN address, and both clients would bind UDP 3659 on it' -ForegroundColor Red; exit 2 }
    if ($Play) { Write-Host '-SecondClient is not for -Play' -ForegroundColor Red; exit 2 }
    if ($LoginAt2 -le 0) { $LoginAt2 = $LoginAt }
}
$Python = Join-Path $MainRoot '.venv\Scripts\python.exe'
$ServerExe = Join-Path $RepoRoot $LaneCfg.ServerExe
$Go = 'go.exe'
if (-not (Get-Command $Go -ErrorAction SilentlyContinue)) { $Go = 'C:\Program Files\Go\bin\go.exe' }
$MonitorPs = Join-Path $RepoRoot 're\scripts\launch_monitor.ps1'
$EgressPy = Join-Path $RepoRoot 're\scripts\egress_report.py'
$AppLog = Join-Path $env:APPDATA "DawngateData$(if ($LanAddr) { $LanCopy.Suffix } else { $LaneCfg.Suffix })\Application.log"
$AppLog2 = if ($Second) { Join-Path $env:APPDATA "DawngateData$($Second.Suffix)\Application.log" } else { $null }
$ProbeExe = Join-Path $MainRoot 'tools\raknet\build\Release\raknet_probe_p5.exe'
$ScriptExe = Join-Path $RepoRoot 'gms\build\Release\gms_script.exe'  # -GmsProbe session (G1)
$GmsPort = 10042  # dgserver's default --gms-port (blaze.GMSPort)
if (-not $GameDir) { $GameDir = Join-Path $MainRoot $(if ($LanAddr) { $LanCopy.Client } else { $LaneCfg.Client }) }

if ($Name -notmatch '^[\w.-]+$') { Write-Host "invalid -Name '$Name' (letters, digits, _ . - only)" -ForegroundColor Red; exit 2 }
if ($Lane -ne 'a' -and $GmsProbe -in @('silent', 'connected')) { Write-Host "-GmsProbe $GmsProbe is lane a only (raknet_probe binds 127.0.0.1); use -GmsProbe session or -GmsHost" -ForegroundColor Red; exit 2 }
if ($LanAddr -and $GmsProbe -in @('silent', 'connected')) { Write-Host "-GmsProbe $GmsProbe is loopback only (raknet_probe binds 127.0.0.1); use -GmsProbe session or -GmsHost with -Lan" -ForegroundColor Red; exit 2 }
if ($Play) {
    # P1: playing by hand needs the host dgserver starts per game; no time limit unless -Seconds is given
    if (-not $GmsHost) { Write-Host '-Play needs -GmsHost (play.ps1 passes client\run_854_server)' -ForegroundColor Red; exit 2 }
    if (-not $PSBoundParameters.ContainsKey('Seconds')) { $Seconds = 0 }
} elseif ($NoCapture) { Write-Host '-NoCapture is for -Play only: every test session is captured and egress-checked' -ForegroundColor Red; exit 2 }
if ($Seconds -lt 0 -or ($Seconds -eq 0 -and -not $Play)) { Write-Host '-Seconds must be > 0 (0 = no time limit is for -Play only)' -ForegroundColor Red; exit 2 }
# B12: the keep-alive key against the client's AFK kick. Automatic (-1): on in test sessions long enough for the kick
# (5 minutes in a game, so -Seconds > 300), off otherwise and in play mode, where the player gives input.
if ($KeepAlive -lt 0) { $KeepAlive = if (-not $Play -and $Seconds -gt 300) { 60 } else { 0 } }
if ($KeepAlive % 2) { Write-Host "-KeepAlive $KeepAlive must be even (the monitor's loop steps by 2 s)" -ForegroundColor Red; exit 2 }

# one session at a time (P1): a test session must never stop a running play session's processes as leftovers, nor the
# other way round. The lock names this process and its start time, so a stale lock (a killed session) is ignored.
# A6: one lock per lane; a session in lane b, c or d also waits while the user plays (a live play lock in lane a): its
# window would get in the way of the user's game.
$LockFile = Join-Path $MainRoot $LaneCfg.Lock
function Get-ProcessStartStamp([int]$id) { try { (Get-Process -Id $id -ErrorAction Stop).StartTime.ToString('o') } catch { $null } }
function Get-LiveLock([string]$file) {
    # the lock's text if it names a live process (not this one), else $null
    if (-not (Test-Path $file)) { return $null }
    $text = "$(Get-Content $file -Raw -ErrorAction SilentlyContinue)".Trim()
    if ($text -match 'pid=(\d+) start=(\S+)' -and [int]$Matches[1] -ne $PID -and (Get-ProcessStartStamp ([int]$Matches[1])) -eq $Matches[2]) { return $text }
    $null
}
$lockText = Get-LiveLock $LockFile
if ($lockText) {
    Write-Host "another session is running in lane $Lane ($lockText; $LockFile)." -ForegroundColor Red
    Write-Host 'Only one session at a time per lane: they share the lane''s ports, and a new one would stop the running one''s processes as leftovers. Wait until it has ended (a play session ends when its game is closed), or use the other lane (-Lane).' -ForegroundColor Red
    exit 2
}
if ($Lane -ne 'a') {
    $playText = Get-LiveLock (Join-Path $MainRoot $LaneTable['a'].Lock)
    if ($playText -match 'mode=play') {
        Write-Host "the user is playing (lane a: $playText); a lane $Lane session waits until that game is closed." -ForegroundColor Red
        exit 2
    }
}
# A5b: one lan session at a time on the PC, whatever its lane (they share the LAN address's ports and the lan copy)
$LanLockFile = Join-Path $MainRoot $LanCopy.Lock
if ($LanAddr) {
    $lanText = Get-LiveLock $LanLockFile
    if ($lanText) { Write-Host "another lan session is running ($lanText; $LanLockFile); one -Lan session at a time on this PC." -ForegroundColor Red; exit 2 }
}
$SessionDir = Join-Path $MainRoot "re\logs\$Name"
if (Test-Path $SessionDir) { Write-Host "session folder $SessionDir exists already; pick another -Name" -ForegroundColor Red; exit 2 }
New-Item -ItemType Directory -Force $SessionDir | Out-Null
$SessionLog = Join-Path $SessionDir 'session.log'
# A6: the session folder's lane, so the other lane's leftover rules can tell this session's dumpcap and monitor apart
Set-Content -Path (Join-Path $SessionDir 'lane.txt') -Value $Lane -Encoding ASCII
$lockLine = "pid=$PID start=$(Get-ProcessStartStamp $PID) name=$Name mode=$(if ($Play) { 'play' } else { 'test' }) lane=$Lane since=$(Get-Date -Format s)"
Set-Content -Path $LockFile -Encoding ASCII -Value $lockLine
if ($LanAddr) { Set-Content -Path $LanLockFile -Encoding ASCII -Value "$lockLine lan=$LanAddr" }
function Remove-SessionLock {
    foreach ($f in @($LockFile, $(if ($LanAddr) { $LanLockFile }))) {
        if ($f -and "$(Get-Content $f -Raw -ErrorAction SilentlyContinue)" -match "pid=$PID ") { Remove-Item $f -Force -ErrorAction SilentlyContinue }
    }
}

function Say([string]$m, [string]$color = 'Gray') {
    $line = '{0:HH:mm:ss} {1}' -f (Get-Date), $m
    Write-Host $line -ForegroundColor $color
    Add-Content -Path $SessionLog -Value $line -Encoding utf8
}
function Short([string]$s, [int]$n = 120) { if ($s.Length -gt $n) { $s.Substring(0, $n) + '...' } else { $s } }
function In-Session([string]$file) { Join-Path $SessionDir $file }

$Ports = @(42127, 10041, 17502, 18080, 9988)  # the A1 set, until the freshly built server names its ports
$GmsHostPath = $null; $GmsHostKind = $null  # -GmsHost (C1b): the resolved host and 'dawngate', 'script' or 'file'
function Get-ServerPorts {
    # with -GmsHost --print-ports also lists the control port (10040)
    # (A6: --bind marks the call as this lane's for the other lane's leftover rule)
    $printArgs = @('--print-ports') + $(if ($GmsHostPath) { @('--gms-dir', $GmsHostPath) } else { @() }) + $(if ($Lane -ne 'a' -or $LanAddr) { @('--bind', $ServerAddr) } else { @() })
    $ports = @("$(& $ServerExe @printArgs)" -split '\s+' | Where-Object { $_ -match '^\d+$' } | ForEach-Object { [int]$_ })
    if ($ports.Count -lt 2) { return $Ports }  # no answer: keep the A1 set
    @($ports | Sort-Object -Unique)
}

# this script's own process chain is never "ours to kill", whatever its command line says
$parentOf = @{}
foreach ($p in Get-CimInstance Win32_Process) { $parentOf[[int]$p.ProcessId] = [int]$p.ParentProcessId }
$Ancestors = @{}
$cur = [int]$PID
while ($cur -and $parentOf.ContainsKey($cur) -and -not $Ancestors.ContainsKey($cur)) { $Ancestors[$cur] = $true; $cur = $parentOf[$cur] }

function Get-CopyLane([string]$exe) {
    # A6: the lane a client\ copy belongs to (the two copies of lanes b, c and d; every other copy is lane a's, as before A6)
    foreach ($l in @($LaneTable.Keys | Where-Object { $_ -ne 'a' })) {
        foreach ($rel in @($LaneTable[$l].Client, $LaneTable[$l].HostCopy)) {
            $root = (Split-Path (Join-Path $MainRoot $rel) -Parent).ToLower() + '\'
            if ($exe.StartsWith($root)) { return $l }
        }
    }
    # A5b: a lane's further client copies (lane d's client\run_854_e, since G3b lane b's client\run_854_f) are that lane's
    foreach ($x in $ExtraClients.Keys) {
        $root = (Split-Path (Join-Path $MainRoot $ExtraClients[$x].Client) -Parent).ToLower() + '\'
        if ($exe.StartsWith($root)) { return $ExtraClients[$x].Lane }
    }
    # A5b: setup_all.py --player's lan copy (client\run_854_lan) is never lane a's ('lan': nobody's); the lan test copy
    # client\run_854_l ('l' below) belongs to whichever -Lan session runs
    if ($exe.StartsWith((Join-Path $MainRoot 'client\run_854_lan\').ToLower())) { return 'lan' }
    # A6b: a copy named like a lane's (client\run_854_<x>\, client\run_854_server_<x>\) of a lane this table doesn't know
    # is that lane's, not lane a's, so a later lane's processes are never stopped as lane a's leftovers (before A6b,
    # lane a's cleanup stopped lanes c's and d's clients, hosts, monitors and captures: d6a_run2, a6b_run1c/d)
    $client = [regex]::Escape((Join-Path $MainRoot 'client\').ToLower())
    if ($exe -match "^${client}run_854(?:_server)?_([a-z])\\") { return $Matches[1] }
    'a'
}
function Get-ServerBind([string]$cmd) {
    # A6: the address a dgserver or gms_script listens on: --bind, else dgserver's --host (A5a: --bind and --public-host
    # at once), else 127.0.0.1. P2-live: before --host counted, lane a's cleanup took a dgserver --host <LAN address>
    # (test_p2.ps1's first live run, p2live_run1b) for its own and stopped it in the middle of that check's match
    if ($cmd -match '--bind[= ]+"?(\d+\.\d+\.\d+\.\d+)') { return $Matches[1] }
    if ($cmd -match '--host[= ]+"?(\d+\.\d+\.\d+\.\d+)') { return $Matches[1] }
    '127.0.0.1'
}
function Get-BindLane([string]$cmd) {
    # A6: the lane of a dgserver or gms_script by its listen address (Get-ServerBind; default 127.0.0.1, lane a); $null
    # for an unknown address
    $bind = Get-ServerBind $cmd
    foreach ($l in $LaneTable.Keys) { if ($LaneTable[$l].Address -eq $bind) { return $l } }
    $null
}
function Get-FolderLane([string]$cmd) {
    # A6: the lane of a dumpcap or monitor by the session folder in its command line (lane.txt there; none = lane a).
    # A6b: a lane letter this table doesn't know is that lane's too (a later lane's session is never lane a's)
    $logs = [regex]::Escape((Join-Path $MainRoot 're\logs\').ToLower())
    if ($cmd -notmatch "$logs([^\\`"]+)") { return 'a' }
    $marker = Join-Path $MainRoot "re\logs\$($Matches[1])\lane.txt"
    $l = "$(Get-Content $marker -Raw -ErrorAction SilentlyContinue)".Trim().ToLower()
    if ($l -match '^[a-z]$') { $l } else { 'a' }
}
function Get-ServerExeLane([string]$exe) {
    # D1k: the lane of a dgserver.exe by its binary: server\bin\lane_<x>\dgserver.exe is lane x's (in the main checkout
    # or a worktree), server\bin\dgserver.exe lane a's. A server started without --bind (test_p2.ps1's, with --host
    # <LAN address>) used to count as lane a's by Get-BindLane's default, and lane a's end-of-run cleanup stopped lane
    # b's running P2 server (d1k_run5, 2026-10-09)
    if ($exe -match '\\server\\bin\\lane_([a-z])\\') { return $Matches[1] }
    'a'
}
function Test-OwnBind([string]$cmd) {
    # A dgserver/gms_script command line of this session: its listen address (--bind, or dgserver's --host) is the
    # lane's address, or (A5b, -Lan) this PC's LAN address, which only the session holding the lan lock uses
    if ((Get-BindLane $cmd) -eq $Lane) { return $true }
    [bool]($LanAddr -and (Get-ServerBind $cmd) -eq $LanAddr)
}
function Get-SessionProcesses {
    # Processes a test session of this lane starts. Nothing else is ever stopped (not the Ghidra MCP bridge, not
    # other python or powershell processes, not a dumpcap that Wireshark runs, not the other lane's session).
    # A5b: with -Lan also the lan test copy's game (client\run_854_l) and what binds the LAN address (never
    # client\run_854_lan, setup_all.py --player's copy, which a player may run by hand).
    $client = (Join-Path $MainRoot 'client\').ToLower()
    $mainDir = $MainRoot.ToLower() + '\'  # the main checkout and the git worktrees inside it
    $logs = (Join-Path $MainRoot 're\logs\').ToLower()
    $probeDir = (Join-Path $MainRoot 'tools\raknet\build\').ToLower()
    $all = @(Get-CimInstance Win32_Process)
    $byPid = @{}; foreach ($p in $all) { $byPid[[int]$p.ProcessId] = $p }
    @($all | Where-Object {
        $cmd = "$($_.CommandLine)".ToLower(); $exe = "$($_.ExecutablePath)".ToLower()
        (-not $Ancestors.ContainsKey([int]$_.ProcessId)) -and (
            ($_.Name -eq 'dgserver.exe' -and $exe.StartsWith($mainDir) -and $exe.Contains('\server\bin\') -and
                (Get-ServerExeLane $exe) -eq $Lane -and (Test-OwnBind $cmd)) -or
            ($_.Name -eq 'raknet_probe_p5.exe' -and $exe.StartsWith($probeDir) -and $Lane -eq 'a') -or
            ($_.Name -eq 'gms_script.exe' -and $exe.StartsWith($mainDir) -and $exe.Contains('\gms\build\') -and $(
                # its own --bind (-GmsProbe session), else the lane of the dgserver that started it (-GmsHost)
                if ($cmd -match '--bind') { Test-OwnBind $cmd }
                # D14b-live: one without --bind and not started by a dgserver is lane a's only from this checkout's own
                # gms\build (lane a's -GmsProbe session); d14b_cc1's cleanup had stopped an offline package's
                # gms_script.exe from its worktree (<worktree>\gms\build), which no lane a session starts
                else { $parent = $byPid[[int]$_.ParentProcessId]; if ($parent -and $parent.Name -eq 'dgserver.exe') { Test-OwnBind "$($parent.CommandLine)" } else { $Lane -eq 'a' -and $exe.StartsWith((Join-Path $RepoRoot 'gms\build\').ToLower()) } })) -or
            ($_.Name -in @('Dawngate.exe', 'CrashSender1402.exe') -and $exe.StartsWith($client) -and $(
                $cl = Get-CopyLane $exe; $cl -eq $Lane -or ($LanAddr -and $cl -eq 'l'))) -or
            ($_.Name -eq 'dumpcap.exe' -and $cmd.Contains($logs) -and (Get-FolderLane $cmd) -eq $Lane) -or
            ($_.Name -eq 'powershell.exe' -and $cmd -match '-file\s+"?[^"]*launch_monitor\.ps1' -and (Get-FolderLane $cmd) -eq $Lane))
    })
}
function Stop-SessionProcesses([string]$why, [scriptblock]$filter = { $true }) {
    $procs = @(Get-SessionProcesses | Where-Object -FilterScript $filter)
    foreach ($p in $procs) {
        Say ('{0}: stopping {1} pid={2} {3}' -f $why, $p.Name, $p.ProcessId, (Short "$($p.CommandLine)" 110)) 'Yellow'
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($procs.Count) { Start-Sleep -Milliseconds 1000 }
    $procs
}
$LaneAddrs = @($LaneAddr, '0.0.0.0', '::')  # A6: a listener on these blocks this lane's ports (the other lane's don't)
if ($LanAddr) { $LaneAddrs = @($LanAddr) + $LaneAddrs }  # A5b: the server's ports on the LAN address, the control port on the lane's
function Get-LaneUdp([int]$port) { @(Get-NetUDPEndpoint -LocalPort $port -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in $LaneAddrs }) }
function Get-PortHolders {
    foreach ($c in @(Get-NetTCPConnection -State Listen -LocalPort $Ports -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in $LaneAddrs })) {
        $p = Get-CimInstance Win32_Process -Filter "ProcessId=$($c.OwningProcess)"
        [pscustomobject]@{ Port = [int]$c.LocalPort; Pid = [int]$c.OwningProcess; Name = $p.Name; Cmd = "$($p.CommandLine)" }
    }
}
function Wait-PortsFree([int]$sec) {
    $deadline = (Get-Date).AddSeconds($sec)
    while ($true) {
        $held = @(Get-PortHolders)
        if (-not $held.Count -or (Get-Date) -gt $deadline) { return $held }
        Start-Sleep -Milliseconds 500
    }
}
function Get-SessionDumpcap {
    $cap = (In-Session 'capture.pcapng').ToLower()
    @(Get-CimInstance Win32_Process -Filter "Name='dumpcap.exe'" | Where-Object { "$($_.CommandLine)".ToLower().Contains($cap) })
}
function Start-Logged([string]$exe, [object[]]$argList, [string]$tag) {
    # stdout/stderr go to <tag>.out / <tag>.err in the session folder
    $p = Start-Process -FilePath $exe -ArgumentList $argList -WorkingDirectory $RepoRoot -NoNewWindow -PassThru `
        -RedirectStandardOutput (In-Session "$tag.out") -RedirectStandardError (In-Session "$tag.err")
    $null = $p.Handle  # keeps ExitCode readable after the process ends (Windows PowerShell quirk)
    $p
}
function Invoke-EgressCheck([string]$dir) {
    $egArgs = @('-I', "`"$EgressPy`"", "`"$dir`"", '--lane-address', $LaneAddr)
    # A5b: this PC's own LAN address only (-Lan); the second client's monitor log and its address (-SecondClient)
    if ($LanAddr) { $egArgs += @('--allow-host', $LanAddr) }
    if ($Second) {
        $egArgs += @('--lane-address', $Second.Bind, '--monitor', "`"$(Join-Path $dir 'monitor.log')`"", '--monitor', "`"$(Join-Path $dir 'client2\monitor.log')`"")
        # G3: the second client's restart (-SecondRestartAt)
        if (Test-Path (Join-Path $dir 'client2b\monitor.log')) { $egArgs += @('--monitor', "`"$(Join-Path $dir 'client2b\monitor.log')`"") }
    }
    $p = Start-Logged $Python $egArgs 'egress'
    $p.WaitForExit()
    Move-Item (In-Session 'egress.out') (In-Session 'egress.txt') -Force
    $text = @(Get-Content (In-Session 'egress.txt') -Encoding UTF8)
    [pscustomobject]@{
        ExitCode = $p.ExitCode
        Total = "$($text | Where-Object { $_ -match '^\d+ flagged line' } | Select-Object -Last 1)"
        Flagged = @($text | Where-Object { $_ -match '<-- FLAGGED' })
        Err = "$(Get-Content (In-Session 'egress.err') -Raw)"
    }
}
function Get-AppLogSection([datetime]$since, [string]$file = $AppLog, $until = $null) {
    # Application.log is appended to; each launch starts with "### yyyy-MM-dd HH:mm:ss" (local time)
    # (G3: $until = the start of a later launch of the same copy, -SecondRestartAt: the section before it)
    $AppLog = $file  # (A5b: the second client's own log; the messages below name the file read)
    if (-not (Test-Path $AppLog)) { return @("(no $AppLog)") }
    $lines = @(Get-Content $AppLog)
    $end = $lines.Count - 1
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        if ($lines[$i] -match '^### (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})') {
            $t = [datetime]::ParseExact($Matches[1], 'yyyy-MM-dd HH:mm:ss', [Globalization.CultureInfo]::InvariantCulture)
            if ($until -and $t -ge ([datetime]$until).AddSeconds(-5)) { $end = $i - 1; continue }
            if ($t -ge $since.AddSeconds(-5)) { return $lines[$i..$end] }
            break
        }
    }
    @("(no Application.log section started after $since; the last 40 lines of $AppLog follow)") + @($lines | Select-Object -Last 40)
}
function Get-GmsProbeSummary([string]$path) {
    # gms_probe.out of raknet_probe_p5 --listen: packets its application received, by id, plus HelloReq
    # (also the host's dgsim.log since G2: its gms/core lines carry the harness log's "<ms> [<thread>] " prefix)
    $kinds = [ordered]@{}; $notes = @(); $states = [ordered]@{}
    foreach ($l in @(Get-Content $path -Encoding UTF8 | ForEach-Object { $_ -replace '^\s*\d+ \[\s*\d+\] ', '' })) {
        if ($l -match '^\S+\s+[\d.]+ ([0-9a-f]{2}) (.+?) \((\d+) B\) from') {
            $k = "$($Matches[1]) $($Matches[2] -replace ' \(first GMS wire byte\)', '') $($Matches[3]) B"
            $kinds[$k] = 1 + [int]$kinds[$k]
        }
        elseif ($l -match '^\s+(kGmsHelloReq: .*)$') { $notes += $Matches[1] }
        elseif ($l -match ' sent (kGms\w+) .*: (\w+)$') { $notes += "sent $($Matches[1]) ($($Matches[2]))" }
        # --session: repeated, the last one per connection counts (G3: one per human, each with its address)
        elseif ($l -match '^session (?!script:)(\S+): (.*)$') { $states[$Matches[1]] = $Matches[2] }
    }
    if ($states.Count -eq 1) { $notes += @($states.Values)[0] }
    elseif ($states.Count -gt 1) { $notes += @($states.Keys | ForEach-Object { "[$_] $($states[$_])" }) -join ' | ' }
    $parts = @($kinds.Keys | ForEach-Object { if ($kinds[$_] -gt 1) { "$_ (x$($kinds[$_]))" } else { $_ } })
    (@($(if ($parts.Count) { $parts -join '; ' } else { 'nothing received' })) + @($notes | Select-Object -Unique)) -join '; '
}
function Get-StubSummary([string]$path) {
    $conn = [ordered]@{}; $reqs = [ordered]@{}; $http = [ordered]@{}; $qos = [ordered]@{}; $gms = [ordered]@{}
    $pending = @{}; $httpReq = $null; $qosReq = $null; $telBytes = 0
    function Count($table, $key) { $table[$key] = 1 + [int]$table[$key] }
    foreach ($l in @(Get-Content $path -Encoding UTF8)) {
        if ($l -notmatch '^\S+ \[(\w+)\] (.*)$') { continue }
        $who = $Matches[1]; $msg = $Matches[2]
        if ($msg -match '^connection from') { Count $conn $who }
        elseif ($who -eq 'http' -and $msg -match '^\(.*?\) ([A-Z]+) (\S+)') { Count $conn 'http'; $httpReq = "$($Matches[1]) $(Short $Matches[2] 70)" }
        elseif ($who -eq 'http' -and $httpReq -and $msg -match '^-> (\d{3}[^,]*)') { Count $http "$httpReq -> $($Matches[1])"; $httpReq = $null }
        elseif ($who -eq 'qos' -and $msg -match '^\(.*?\) ([A-Z]+) (\S+)') {
            # QoS requests by path, plus qtyp for /qos/qos (1 latency, 2 bandwidth)
            Count $conn 'qos'; $method = $Matches[1]; $target = $Matches[2]
            $qosReq = "$method $(($target -split '\?')[0])$(if ($target -match '[?&](qtyp=\d+)') { "?$($Matches[1])" })"
        }
        elseif ($who -eq 'qos' -and $qosReq -and $msg -match '^-> (\d{3}[^,]*)') { Count $qos "$qosReq -> $($Matches[1])"; $qosReq = $null }
        elseif ($who -eq 'qos' -and $msg -match '^udp \(.*?\) (latency probe|bandwidth test|firewall packet)') { Count $qos "udp $($Matches[1])" }
        elseif ($msg -match '^<- (\w+)\(0x[0-9a-f]+\) cmd (0x[0-9a-f]+) (\S+)') {
            if ($pending[$who]) { Count $reqs "$($pending[$who]) -> not answered" }
            $pending[$who] = "$($Matches[1]) cmd $($Matches[2])" + $(if ($Matches[3] -ne 'request') { " ($($Matches[3]))" } else { '' })
        }
        elseif ($pending[$who] -and $msg -match '^-> (\w+) reply') { Count $reqs "$($pending[$who]) -> $($Matches[1])"; $pending[$who] = $null }
        elseif ($pending[$who] -and $msg -match 'no handler; not answered') { Count $reqs "$($pending[$who]) -> not answered"; $pending[$who] = $null }
        elseif ($who -eq 'telemetry' -and $msg -match 'closed after (\d+) bytes') { $telBytes += [int]$Matches[1] }
        elseif ($who -eq 'gms' -and $msg -match '^udp \(.*?\) (\d+) bytes: (0x[0-9A-F]{2} [A-Za-z_0-9 ()]+?)(,|$)') { Count $gms "udp $($Matches[2]) $($Matches[1]) bytes" }
        elseif ($who -eq 'blaze' -and $msg -match '^-> (\S+::\S+) notification') { Count $reqs "notification $($Matches[1])" }
    }
    foreach ($k in @($pending.Keys)) { if ($pending[$k]) { Count $reqs "$($pending[$k]) -> not answered" } }
    $fmt = { param($t) (@($t.Keys | ForEach-Object { if ($t[$_] -gt 1) { "$_ (x$($t[$_]))" } else { $_ } }) -join '; ') }
    [pscustomobject]@{
        Connections = (@($conn.Keys | ForEach-Object { "$_ $($conn[$_])" }) -join ', ') + $(if ($conn['telemetry']) { " ($telBytes bytes)" } else { '' })
        Requests = & $fmt $reqs
        Http = & $fmt $http
        Qos = & $fmt $qos
        Gms = & $fmt $gms
    }
}

function Assert-PatchedCopy([string]$dir, [ValidateSet('client', 'host', 'second')][string]$role = 'host') {
    # only a working copy patched by patch_client.py and patch_config.py may run; returns the full path
    # (A5b: $role 'client' is the session's client (with -Lan the lan copy), 'second' the -SecondClient copy)
    $dir = (Resolve-Path $dir).Path
    if ($dir -match '\\client\\ia_') { throw 'refusing to launch a pristine extracted build (client\ia_*); use the working copy' }
    foreach ($f in @('game\Dawngate.exe', 'game\Data\Config.package', 'game\Data\Config_Ship.package')) {
        $live = Join-Path $dir $f
        if (-not (Test-Path $live) -or -not (Test-Path "$live.orig")) { throw "$f or $f.orig is missing in $dir; run patch_client.py and patch_config.py first (docs/RUNNING_THE_CLIENT.md)" }
        if ((Get-FileHash $live).Hash -eq (Get-FileHash "$live.orig").Hash) { throw "$f is unpatched (identical to $f.orig); run patch_client.py / patch_config.py first" }
    }
    # A6: a lane runs only its own copies, and the copies of lanes b-d must be patched for their address and data folders
    $copyLane = Get-CopyLane ($dir.ToLower() + '\')
    $want = $null; $fix = "setup_lane.py $Lane"
    if ($role -eq 'client' -and $LanAddr) {
        # A5b: the lan test copy, patched for this PC's LAN address (lan profile, sockets narrowed to it)
        if ($copyLane -ne 'l') { throw "-Lan runs the lan test copy $($LanCopy.Client) (setup_lane.py lan --address $LanAddr), not $dir" }
        $want = @("redirector=$LanAddr", "bind=$LanAddr", "raknet_loopback=$LanAddr", "appdata=DawngateData$($LanCopy.Suffix)", "documents=Dawngate$($LanCopy.Suffix)")
        $fix = "setup_lane.py lan --address $LanAddr"
    } elseif ($role -eq 'second') {
        if ($copyLane -ne $Lane) { throw "$dir belongs to lane $copyLane, not lane $Lane" }
        $want = @("redirector=$LaneAddr", "bind=$($Second.Bind)", "raknet_loopback=$($Second.Bind)", "appdata=DawngateData$($Second.Suffix)", "documents=Dawngate$($Second.Suffix)")
        $fix = "setup_lane.py $(@($ExtraClients.Keys | Where-Object { $ExtraClients[$_] -eq $Second })[0])"
    } else {
        if ($copyLane -ne $Lane) { throw "$dir belongs to lane $copyLane, not lane $Lane (-Lane $copyLane, or the lane's own copy)" }
        if ($Lane -ne 'a') { $want = @("redirector=$LaneAddr", "bind=$LaneAddr", "raknet_loopback=$LaneAddr", "appdata=DawngateData$($LaneCfg.Suffix)", "documents=Dawngate$($LaneCfg.Suffix)") }
    }
    if ($want) {
        $show = @(& $Python -I (Join-Path $RepoRoot 're\scripts\patch_client.py') --show (Join-Path $dir 'game\Dawngate.exe'))
        $bad = @($want | Where-Object { $show -notcontains $_ })
        if ($bad.Count) { throw "$dir is not patched for this session (missing $($bad -join ', ')); run .venv\Scripts\python -I re\scripts\$fix" }
    }
    $dir
}

# ---- the match host beside the client (-HostDir, D1d) ----
$HostState = @{ Proc = $null; Started = $null; Data = (In-Session 'host'); Sockets = [ordered]@{}; Exit = $null; Stopped = $false
                MaxPrivate = 0; MaxWs = 0; Peak = 0; Cpu = 0.0; Visible = 0; Samples = 0; Last = $null
                Match = 0; Killed = $false; ExitedAt = $null }
function Assert-HostDll([string]$dir) {
    # the host copy runs the gms/ code it was built from (G2): the installed DLL must be the current build
    if (-not (Test-Path (Join-Path $dir 'game\PhysXLoader_orig.dll'))) { throw "$dir has no dgsim.dll installed (game\PhysXLoader_orig.dll missing); run setup_server_copy.py first (docs/SERVER_MODE.md)" }
    $simDll = Join-Path $RepoRoot 'gms\build\Release\dgsim.dll'
    if (-not (Test-Path $simDll)) { throw "the host needs $simDll (build gms/: cmake -S gms -B gms\build -A Win32; cmake --build gms\build --config Release)" }
    # G5: no source of dgsim newer than its last successful build (the build stamp; re\scripts\gms_stale.ps1). This
    # checkout's build: lane b's worktree checks its own gms\build (A6)
    $stale = Get-GmsBuildStale $simDll (Join-Path $RepoRoot 'gms')
    if ($stale) { throw "$stale ($simDll); rebuild: cmake --build gms\build --config Release" }
    if ((Get-FileHash $simDll).Hash -ne (Get-FileHash (Join-Path $dir 'game\PhysXLoader.dll')).Hash) {
        throw "$dir\game\PhysXLoader.dll is not the current $simDll; install it: .venv\Scripts\python -I re\scripts\setup_server_copy.py $($LaneCfg.Client) $dir $simDll$(if ($Lane -ne 'a') { " (or setup_lane.py $Lane --dll $simDll)" })"
    }
}
function Assert-ScriptExe {
    if (-not (Test-Path $ScriptExe)) { throw "$ScriptExe is missing (build gms/: cmake -S gms -B gms\build -A Win32; cmake --build gms\build --config Release)" }
    # G5: as Assert-HostDll (gms\core\control is linked into gms_script since C1b, host mode)
    $stale = Get-GmsBuildStale $ScriptExe (Join-Path $RepoRoot 'gms')
    if ($stale) { throw "$stale ($ScriptExe); rebuild: cmake --build gms\build --config Release" }
}
function Get-ServerLogLines {
    # dgserver's log while it runs (<session>\yyyyMMdd-HHmmss.log; renamed to stub.log at the end)
    $f = Get-ChildItem $SessionDir -File -ErrorAction SilentlyContinue | Where-Object Name -match '^\d{8}-\d{6}\.log$' | Select-Object -First 1
    if (-not $f) { $f = Get-Item (In-Session 'stub.log') -ErrorAction SilentlyContinue }
    if (-not $f) { return @() }
    try { @(Get-Content -LiteralPath $f.FullName -Encoding UTF8 -ErrorAction Stop) } catch { @() }
}
function Find-ServerHost {
    # -GmsHost: dgserver logs "[host] match N: started pid P for game G (...)"; the newest match is the one sampled
    $last = @(Get-ServerLogLines | Where-Object { $_ -match '\[host\] match (\d+): started pid (\d+)' } | Select-Object -Last 1)
    if (-not $last.Count -or $last[0] -notmatch '\[host\] match (\d+): started pid (\d+)') { return }
    $match = [int]$Matches[1]; $hostPid = [int]$Matches[2]
    if ($HostState.Match -eq $match) { return }
    $HostState.Match = $match; $HostState.Started = Get-Date; $HostState.Exit = $null; $HostState.ExitedAt = $null
    $HostState.Data = Join-Path (In-Session 'host') "$match"; $HostState.Sockets = [ordered]@{}
    try {
        $p = Get-Process -Id $hostPid -ErrorAction Stop
        $null = $p.Handle  # keeps ExitCode readable
        $HostState.Proc = $p
        Say "host: dgserver started match $match, pid $hostPid ($($p.Path)); data $($HostState.Data)"
    } catch {
        Say "host: dgserver started match $match, pid $hostPid, which is already gone" 'Yellow'
    }
    if (-not (Test-Path (Join-Path $SessionDir 'host_samples.csv'))) {
        Set-Content -Path (In-Session 'host_samples.csv') -Value 't_s,private_mb,working_set_mb,peak_working_set_mb,cpu_s,window' -Encoding ASCII
    }
}
function Stop-HostAt {
    # -HostKillAt: a host crash in a game (Stop-Process -Force = TerminateProcess)
    if ($HostKillAt -le 0 -or $HostState.Killed -or -not $HostState.Proc -or $HostState.Proc.HasExited) { return }
    if (((Get-Date) - $launchTime).TotalSeconds -lt $HostKillAt) { return }
    $HostState.Killed = $true
    Say "host: -HostKillAt $HostKillAt`: killing pid $($HostState.Proc.Id) (a host crash)" 'Yellow'
    Stop-Process -Id $HostState.Proc.Id -Force -ErrorAction SilentlyContinue
}
function Get-HostControlSummary([string[]]$lines) {
    # dgserver's [host] lines (gmshost) and the GameManager's game lines for the session's newest match
    $m = $HostState.Match
    if (-not $m) {
        $err = @($lines | Where-Object { $_ -match '\[(host|blaze)\] .*(not started|launch failed|no match host)' } | ForEach-Object { ($_ -replace '^\S+ ', '') })
        if ($err.Count) { return "no host started: $($err -join '; ')" }
        return 'no match started (no resetDedicatedServer?)'
    }
    $parts = @()
    $toHost = [ordered]@{}; $fromHost = [ordered]@{}
    foreach ($l in $lines) {
        if ($l -match "\[host\] match $m`: started pid (\d+)(.*?) for game (\d+) \((.+?)\), GMS (\S+)") { $parts += "match $m (game $($Matches[3]), $($Matches[4])), pid $($Matches[1]), GMS $($Matches[5])" }
        elseif ($l -match "\[host\] match $m`: hello from pid \d+ \((.*?)\), ([\d.]+) s after the start") { $parts += "hello ($($Matches[1])) $($Matches[2]) s after the start" }
        elseif ($l -match "\[host\] match $m`: ready on port (\d+), ([\d.]+) s after the start") { $parts += "gameReady (port $($Matches[1])) $($Matches[2]) s after the start" }
        elseif ($l -match '\[blaze\]\s+game \d+: GMS (\S+) \(HNET\) ready ([\d.]+) s after (\S+)') { $parts += "NotifyGameSetup ($($Matches[1])) $($Matches[2]) s after $($Matches[3])" }
        elseif ($l -match "\[host\] match $m <- \{`"t`":`"(\w+)`"") { $fromHost[$Matches[1]] = 1 + [int]$fromHost[$Matches[1]] }
        elseif ($l -match "\[host\] match $m -> \{`"t`":`"(\w+)`"") { $toHost[$Matches[1]] = 1 + [int]$toHost[$Matches[1]] }
        elseif ($l -match "\[host\] match $m`: killing pid \d+: (.*)") { $parts += "KILLED by dgserver: $($Matches[1])" }
        elseif ($l -match "\[host\] match $m`: ended after ([\d.]+) s: (.*)$") { $parts += "ended after $($Matches[1]) s: $($Matches[2])" }
        elseif ($l -match '\[blaze\]\s+game \d+: (match host (aborted|gone before gameReady).*)$') { $parts += $Matches[1] }
    }
    $fmt = { param($t) (@($t.Keys | ForEach-Object { if ($t[$_] -gt 1) { "$_ x$($t[$_])" } else { $_ } }) -join ', ') }
    $parts += "host -> $(& $fmt $fromHost)"
    $parts += "server -> $(& $fmt $toHost)"
    $parts -join '; '
}
function Get-TelemetrySummary([string[]]$lines, [string[]]$app) {
    # R5-live: dgserver's decoded telemetry (docs/TELEMETRY.md): events by name, logins, what it could not decode
    $events = [ordered]@{}; $logins = @(); $bad = 0; $bytes = 0
    foreach ($l in $lines) {
        if ($l -notmatch '^\S+ \[telemetry\] (.*)$') { continue }
        $msg = $Matches[1]
        if ($msg -match '\) #\d+ (\w+) ') { $events[$Matches[1]] = 1 + [int]$events[$Matches[1]] }
        elseif ($msg -match '<- &lgr, \d+ bytes: (anonymous login|login AUTH)[^,]*?(\((MD5 ok|MD5 wrong)\))?,') { $logins += "$($Matches[1])$(if ($Matches[3]) { " ($($Matches[3]))" })" }
        elseif ($msg -match 'closed after (\d+) bytes') { $bytes += [int]$Matches[1] }
        if ($msg -match 'not a telemetry packet|STR event|not a full packet|the packet.s body|MD5 wrong') { $bad++ }
    }
    $asserts = @($app | Where-Object { $_ -match 'Assert Failed' -and $_ -match '(?i)telemetry' }).Count
    $ev = @($events.Keys | ForEach-Object { if ($events[$_] -gt 1) { "$_ x$($events[$_])" } else { $_ } }) -join ', '
    $n = 0; foreach ($k in $events.Keys) { $n += $events[$k] }
    "$n event(s)$(if ($ev) { ": $ev" }); $(if ($logins.Count) { ($logins | Select-Object -Unique) -join ', ' } else { 'no login packet' }); $bad undecodable or problem line(s); $bytes bytes; $asserts telemetry assert(s) in the client's log"
}
function Start-Host {
    New-Item -ItemType Directory -Force $HostState.Data | Out-Null
    Set-Content -Path (In-Session 'host_samples.csv') -Value 't_s,private_mb,working_set_mb,peak_working_set_mb,cpu_s,window' -Encoding ASCII
    # the DGGMS_* variables go into this process's environment just for the start (the child inherits them); the
    # game's console output (NavPower warnings) goes to host\stdout.txt / stderr.txt
    $exe = Join-Path $HostDir 'game\Dawngate.exe'
    # G2: the host serves the GMS port unless a -GmsProbe process does
    $hostBind = if ($GmsProbe) { 'off' } else { "${ServerAddr}:$GmsPort" }  # A6: the lane's address (A5b: -Lan's)
    $hostEnv = [ordered]@{ DGGMS_MATCH = $Name; DGGMS_BIND = $hostBind; DGGMS_DATA = $HostState.Data }
    if ($HostOptions) { $hostEnv['DGGMS_OPTIONS'] = $HostOptions }
    $saved = @{}
    try {
        foreach ($k in $hostEnv.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $hostEnv[$k]) }
        $p = Start-Process -FilePath $exe -ArgumentList '-multipleInstances' -WorkingDirectory (Join-Path $HostDir 'game') -NoNewWindow -PassThru `
            -RedirectStandardOutput (Join-Path $HostState.Data 'stdout.txt') -RedirectStandardError (Join-Path $HostState.Data 'stderr.txt')
    } finally {
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
    $null = $p.Handle  # keeps ExitCode readable
    $HostState.Proc = $p; $HostState.Started = Get-Date
    Say "host: $exe -multipleInstances (pid $($p.Id)); $(@($hostEnv.Keys | ForEach-Object { "$_=$($hostEnv[$_])" }) -join ' ')"
}
function Watch-Host {
    # one sample: the host's sockets, memory, CPU time and visible window (host_samples.csv)
    $p = $HostState.Proc
    if (-not $p -or $HostState.Stopped) { return }
    if ($p.HasExited) {
        if ($null -eq $HostState.Exit) { $HostState.Exit = $p.ExitCode; Say "host exited by itself (code $($p.ExitCode))" 'Yellow' }
        return
    }
    $t = [int]((Get-Date) - $HostState.Started).TotalSeconds
    foreach ($c in @(Get-NetTCPConnection -OwningProcess $p.Id -ErrorAction SilentlyContinue)) {
        # (C1b: an outgoing connection, the control channel, also shows a 'Bound' row 0.0.0.0:port -> 0.0.0.0:0)
        $k = "TCP $($c.LocalAddress):$($c.LocalPort) -> $($c.RemoteAddress):$($c.RemotePort) ($($c.State))"
        if (-not $HostState.Sockets.Contains($k)) { $HostState.Sockets[$k] = $t; Say "host socket at t=${t}s: $k" 'Yellow' }
    }
    foreach ($u in @(Get-NetUDPEndpoint -OwningProcess $p.Id -ErrorAction SilentlyContinue)) {
        $k = "UDP $($u.LocalAddress):$($u.LocalPort)"
        if (-not $HostState.Sockets.Contains($k)) { $HostState.Sockets[$k] = $t; Say "host socket at t=${t}s: $k" 'Yellow' }
    }
    try { $gp = Get-Process -Id $p.Id -ErrorAction Stop } catch { return }
    $priv = [int]($gp.PrivateMemorySize64 / 1MB); $ws = [int]($gp.WorkingSet64 / 1MB); $peak = [int]($gp.PeakWorkingSet64 / 1MB)
    $cpu = [math]::Round($gp.TotalProcessorTime.TotalSeconds, 1)
    $win = if ($gp.MainWindowHandle -ne [IntPtr]::Zero) { 'visible' } else { 'none' }
    if ($win -eq 'visible') { $HostState.Visible++ }
    $HostState.MaxPrivate = [math]::Max($HostState.MaxPrivate, $priv); $HostState.MaxWs = [math]::Max($HostState.MaxWs, $ws)
    $HostState.Peak = [math]::Max($HostState.Peak, $peak); $HostState.Cpu = $cpu; $HostState.Samples++
    $HostState.Last = [pscustomobject]@{ T = $t; Private = $priv; Ws = $ws; Peak = $peak; Cpu = $cpu }
    Add-Content -Path (In-Session 'host_samples.csv') -Value ('{0},{1},{2},{3},{4},{5}' -f $t, $priv, $ws, $peak,
        $cpu.ToString([Globalization.CultureInfo]::InvariantCulture), $win) -Encoding ASCII
}
function Get-HostTimings {
    # the DLL's milestones: "host timing: <what> at <ms> ms after process start"
    $f = Join-Path $HostState.Data 'dgsim.log'
    $marks = [ordered]@{}
    if (Test-Path $f) {
        foreach ($l in @(Get-Content $f -Encoding UTF8)) {
            if ($l -match 'host timing: (.+?)(?: \(.*\))? at (\d+) ms after process start') { if (-not $marks.Contains($Matches[1])) { $marks[$Matches[1]] = [int]$Matches[2] } }
        }
    }
    $marks
}

# ---- the client's memory (Q3b): client_samples.csv and the summary's 'client res' line ----
# Every $ClientEvery s the session's client (the Dawngate.exe of -GameDir; not a second client): private bytes, working
# set, the process's virtual size (with WOW64's own 64-bit part, about 10 MB), its 32-bit address space read with
# VirtualQueryEx below its limit (2 GB; 4 GB with LARGE_ADDRESS_AWARE): used, committed and the largest free block
# (a D3D9 allocation needs one block), handles, CPU, and how many other clients run (Dawngate.exe outside the host
# copies: the lanes share one GPU). Every $ClientGpuEvery s also Windows 10's GPU counters: the process's dedicated and
# shared usage ('GPU Process Memory', summed over its adapters) and every process's dedicated usage ('GPU Adapter
# Memory'); one Get-Counter call takes 0.3-1.3 s, so they are sampled less often (blank in the other rows).
$ClientState = @{ Proc = $null; Next = 0; NextGpu = 0; Samples = 0; GpuSamples = 0; Limit = 0; Laa = $false; VaError = $null
                  MaxPriv = 0; MaxWs = 0; MaxVirt = 0; MaxVa = 0; MinFree = -1; MaxDed = 0; MaxShared = 0; MaxAll = 0; MaxOthers = 0
                  First = $null; Last = $null; Pids = @() }
$ClientEvery = 5
$ClientGpuEvery = 15
$ClientVaSource = @'
using System;
using System.Runtime.InteropServices;
public static class DgClientVa {
    [StructLayout(LayoutKind.Sequential)]
    struct MBI { public IntPtr BaseAddress; public IntPtr AllocationBase; public uint AllocationProtect; public IntPtr RegionSize; public uint State; public uint Protect; public uint Type; }
    [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr OpenProcess(uint access, bool inherit, int pid);
    [DllImport("kernel32.dll", SetLastError = true)] static extern bool CloseHandle(IntPtr h);
    [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr VirtualQueryEx(IntPtr h, IntPtr addr, out MBI info, IntPtr size);
    // {used, committed, free, largest free block} in bytes from 64 KB up to limit; null if the process can't be opened
    public static long[] Scan(int pid, long limit) {
        IntPtr h = OpenProcess(0x0400, false, pid);  // PROCESS_QUERY_INFORMATION
        if (h == IntPtr.Zero) return null;
        long used = 0, committed = 0, free = 0, largest = 0, addr = 0x10000;
        try {
            MBI m;
            IntPtr sz = new IntPtr(Marshal.SizeOf(typeof(MBI)));
            while (addr < limit) {
                if (VirtualQueryEx(h, new IntPtr(addr), out m, sz) == IntPtr.Zero) break;
                long b = m.BaseAddress.ToInt64(), n = m.RegionSize.ToInt64();
                if (n <= 0) break;
                long s = Math.Max(b, addr), e = Math.Min(b + n, limit);
                if (m.State == 0x10000) { free += e - s; if (e - s > largest) largest = e - s; }  // MEM_FREE
                else { used += e - s; if (m.State == 0x1000) committed += e - s; }               // MEM_COMMIT
                addr = b + n;
            }
        } finally { CloseHandle(h); }
        return new long[] { used, committed, free, largest };
    }
}
'@
function Get-ExeLaa([string]$exe) {
    # IMAGE_FILE_LARGE_ADDRESS_AWARE (0x20) in the PE header's characteristics
    try {
        $fs = [IO.File]::Open($exe, 'Open', 'Read', 'ReadWrite')
        try { $b = New-Object byte[] 1024; [void]$fs.Read($b, 0, 1024) } finally { $fs.Close() }
        $pe = [BitConverter]::ToInt32($b, 0x3C)
        return (([BitConverter]::ToUInt16($b, $pe + 0x16)) -band 0x20) -ne 0
    } catch { return $false }
}
function Get-ClientGpu([int]$id) {
    # $null without the counters (or before the client's D3D device exists)
    $paths = @("\GPU Process Memory(pid_${id}_*)\Dedicated Usage", "\GPU Process Memory(pid_${id}_*)\Shared Usage", '\GPU Adapter Memory(*)\Dedicated Usage')
    try { $r = Get-Counter -Counter $paths -ErrorAction SilentlyContinue } catch { return $null }
    if (-not $r) { return $null }
    $g = @{ Ded = 0.0; Shared = 0.0; All = 0.0; Proc = $false }
    foreach ($s in @($r.CounterSamples)) {
        if ($s.Path -match 'gpu process memory\([^)]*\)\\dedicated usage$') { $g.Ded += $s.CookedValue; $g.Proc = $true }
        elseif ($s.Path -match 'gpu process memory\([^)]*\)\\shared usage$') { $g.Shared += $s.CookedValue; $g.Proc = $true }
        elseif ($s.Path -match 'gpu adapter memory\([^)]*\)\\dedicated usage$') { $g.All += $s.CookedValue }
    }
    $g
}
function Watch-Client {
    if (-not $launchTime) { return }
    $t = ((Get-Date) - $launchTime).TotalSeconds
    if ($t -lt $ClientState.Next) { return }
    $ClientState.Next = $t + $ClientEvery
    $want = [IO.Path]::GetFullPath((Join-Path $GameDir 'game\Dawngate.exe'))
    $all = @(Get-Process -Name Dawngate -ErrorAction SilentlyContinue)
    $p = $ClientState.Proc
    if (-not $p -or $p.HasExited) {
        $p = $null; $ClientState.Proc = $null
        foreach ($c in $all) { try { if ("$($c.Path)" -ieq $want) { $p = $c; break } } catch { } }
        if (-not $p) { return }
        $null = $p.Handle
        $ClientState.Proc = $p
        if ($ClientState.Pids -notcontains $p.Id) { $ClientState.Pids += $p.Id }
        if (-not $ClientState.Limit) {
            $ClientState.Laa = Get-ExeLaa $want
            $ClientState.Limit = if ($ClientState.Laa) { 0xFFFF0000L } else { 0x7FFF0000L }
            try { if (-not ('DgClientVa' -as [type])) { Add-Type -TypeDefinition $ClientVaSource -ErrorAction Stop } } catch { $ClientState.VaError = "$_" }
        }
        if (-not (Test-Path (In-Session 'client_samples.csv'))) {
            Set-Content -Path (In-Session 'client_samples.csv') -Encoding ASCII -Value ('t_s,wall,pid,private_mb,working_set_mb,peak_working_set_mb,virtual_mb,' +
                'va_used_mb,va_committed_mb,va_free_largest_mb,handles,cpu_s,gpu_dedicated_mb,gpu_shared_mb,gpu_all_dedicated_mb,other_clients')
        }
    }
    try { $p.Refresh(); if ($p.HasExited) { return } } catch { return }
    $mb = { param($v) [int]([double]$v / 1MB) }
    $priv = & $mb $p.PrivateMemorySize64; $ws = & $mb $p.WorkingSet64; $peak = & $mb $p.PeakWorkingSet64; $virt = & $mb $p.VirtualMemorySize64
    $va = $null
    if (-not $ClientState.VaError) { try { $va = [DgClientVa]::Scan($p.Id, $ClientState.Limit) } catch { $ClientState.VaError = "$_" } }
    $others = @($all | Where-Object { $_.Id -ne $p.Id -and "$($_.Path)" -notmatch '\\run_854_server' }).Count
    $gpu = $null
    if ($t -ge $ClientState.NextGpu) { $ClientState.NextGpu = $t + $ClientGpuEvery; $gpu = Get-ClientGpu $p.Id }
    $inv = [Globalization.CultureInfo]::InvariantCulture
    $row = [pscustomobject]@{ T = [int]$t; Private = $priv; Ws = $ws; Virt = $virt
        VaUsed = $(if ($va) { & $mb $va[0] }); VaCommitted = $(if ($va) { & $mb $va[1] }); VaFree = $(if ($va) { & $mb $va[3] })
        Ded = $(if ($gpu -and $gpu.Proc) { & $mb $gpu.Ded }); Shared = $(if ($gpu -and $gpu.Proc) { & $mb $gpu.Shared }); All = $(if ($gpu) { & $mb $gpu.All }) }
    $ClientState.Samples++
    if (-not $ClientState.First) { $ClientState.First = $row }
    $ClientState.Last = $row
    $ClientState.MaxPriv = [math]::Max($ClientState.MaxPriv, $priv); $ClientState.MaxWs = [math]::Max($ClientState.MaxWs, $ws)
    $ClientState.MaxVirt = [math]::Max($ClientState.MaxVirt, $virt); $ClientState.MaxOthers = [math]::Max($ClientState.MaxOthers, $others)
    if ($va) {
        $ClientState.MaxVa = [math]::Max($ClientState.MaxVa, $row.VaUsed)
        if ($ClientState.MinFree -lt 0 -or $row.VaFree -lt $ClientState.MinFree) { $ClientState.MinFree = $row.VaFree }
    }
    if ($gpu) {
        $ClientState.GpuSamples++
        if ($null -ne $row.Ded) { $ClientState.MaxDed = [math]::Max($ClientState.MaxDed, $row.Ded); $ClientState.MaxShared = [math]::Max($ClientState.MaxShared, $row.Shared) }
        $ClientState.MaxAll = [math]::Max($ClientState.MaxAll, $row.All)
    }
    Add-Content -Path (In-Session 'client_samples.csv') -Encoding ASCII -Value ('{0},{1},{2},{3},{4},{5},{6},{7},{8},{9},{10},{11},{12},{13},{14},{15}' -f
        $row.T, (Get-Date).ToString('HH:mm:ss.fff'), $p.Id, $priv, $ws, $peak, $virt, $row.VaUsed, $row.VaCommitted, $row.VaFree,
        $p.HandleCount, $p.TotalProcessorTime.TotalSeconds.ToString('0.0', $inv), $row.Ded, $row.Shared, $row.All, $others)
}
function Get-ClientResSummary {
    if (-not $ClientState.Samples) { return 'no sample (the client was not found)' }
    $f = $ClientState.First; $l = $ClientState.Last
    $lim = if ($ClientState.Laa) { '4096 (LARGE_ADDRESS_AWARE)' } else { '2048' }
    $va = if ($ClientState.VaError) { "address space not read ($(Short $ClientState.VaError 80))" }
          else { "32-bit address space used max $($ClientState.MaxVa) MB of $lim (first $($f.VaUsed), last $($l.VaUsed)), largest free block min $($ClientState.MinFree) MB (last $($l.VaFree))" }
    $gpu = if (-not $ClientState.GpuSamples) { 'no GPU counters' }
           else { "GPU dedicated max $($ClientState.MaxDed) MB, shared max $($ClientState.MaxShared) MB (the process), all processes' dedicated max $($ClientState.MaxAll) MB ($($ClientState.GpuSamples) GPU samples)" }
    "pid $($ClientState.Pids -join ', '), $($ClientState.Samples) samples every $ClientEvery s over $($l.T - $f.T) s: private max $($ClientState.MaxPriv) MB (first $($f.Private), last $($l.Private)), working set max $($ClientState.MaxWs) MB, virtual size max $($ClientState.MaxVirt) MB; $va; $gpu; other clients up to $($ClientState.MaxOthers) (client_samples.csv)"
}

# ---- play mode (-Play, P1): Ctrl+C as a key, closing the game ----
$CtrlC = @{ Enabled = $false; Saved = $null; Count = 0 }
function Enable-CtrlCAsInput {
    # Ctrl+C becomes a key press for everything on this console, so dgserver and the monitor get no console signal and
    # the session can end in order. Fails without a console (redirected input): then only the finally block cleans up.
    try {
        if ($null -eq $CtrlC.Saved) { $CtrlC.Saved = [Console]::TreatControlCAsInput }
        [Console]::TreatControlCAsInput = $true
        $CtrlC.Enabled = $true
    } catch { $CtrlC.Enabled = $false }
}
function Test-CtrlC {
    if (-not $CtrlC.Enabled) { return $false }
    $hit = $false
    try {
        [Console]::TreatControlCAsInput = $true  # again: a child sharing the console may have reset the mode
        while ([Console]::KeyAvailable) {
            $k = [Console]::ReadKey($true)
            if ($k.Key -eq [ConsoleKey]::C -and ($k.Modifiers -band [ConsoleModifiers]::Control)) { $hit = $true }
        }
    } catch { }
    $hit
}
function Restore-CtrlC {
    if ($null -ne $CtrlC.Saved) { try { [Console]::TreatControlCAsInput = $CtrlC.Saved } catch { } }
}
function Stop-PlayGame([string]$why) {
    # the client from -GameDir only (the host copy runs Dawngate.exe too); in a match it ignores WM_CLOSE
    $exe = (Join-Path $GameDir 'game\Dawngate.exe').ToLower()
    foreach ($g in @(Get-CimInstance Win32_Process -Filter "Name='Dawngate.exe'" | Where-Object { "$($_.ExecutablePath)".ToLower() -eq $exe })) {
        Say "$why`: closing the game (pid $($g.ProcessId))" 'Yellow'
        try {
            $gp = Get-Process -Id $g.ProcessId -ErrorAction Stop
            [void]$gp.CloseMainWindow()
            if (-not $gp.WaitForExit(10000)) {
                Say 'the game did not close within 10 s (in a match it ignores WM_CLOSE); stopping it' 'Yellow'
                Stop-Process -Id $g.ProcessId -Force -ErrorAction SilentlyContinue
            }
        } catch { }
    }
}

# ---------------------------------------------------------------------------------------------
$t0 = Get-Date
$failure = $null
$stub = $null
$launchTime = $null
$L2G = $null
$leftoversBefore = @()
$stoppedAfter = @()
$playEnd = $null  # -Play: how the session ended (the game closed, Ctrl+C, the time limit)
try {
    Say "session $Name -> $SessionDir$(if ($Play) { ' (play mode)' }) lane $Lane ($LaneAddr$(if ($LanAddr) { "; lan profile on $LanAddr" }))$(if ($RepoRoot -ne $MainRoot) { "; code from $RepoRoot" })"
    if ($Play) { Enable-CtrlCAsInput }

    # 1. safety: only a working copy patched by patch_client.py and patch_config.py may run
    $GameDir = Assert-PatchedCopy $GameDir 'client'
    Say "working copy $GameDir is patched (exe and both config packages differ from their .orig)$(if ($LanAddr) { "; lan profile on this PC's LAN address $LanAddr (A5b)" })"
    if ($Second) {
        # A5b: the lane's second client copy beside the first
        $GameDir2 = Assert-PatchedCopy (Join-Path $MainRoot $Second.Client) 'second'
        Say "second client $GameDir2 is patched (bind $($Second.Bind), %APPDATA%\DawngateData$($Second.Suffix))"
    }
    # B6-live: the copies live in the main checkout, so a relative -HostDir/-GmsHost (client\run_854_server_c) given in a
    # worktree (lanes b, c, d) is taken from there when the worktree has no such path
    if ($HostDir -and -not [IO.Path]::IsPathRooted($HostDir) -and -not (Test-Path $HostDir) -and (Test-Path (Join-Path $MainRoot $HostDir))) { $HostDir = Join-Path $MainRoot $HostDir }
    if ($GmsHost -and -not [IO.Path]::IsPathRooted($GmsHost) -and -not (Test-Path $GmsHost) -and (Test-Path (Join-Path $MainRoot $GmsHost))) { $GmsHost = Join-Path $MainRoot $GmsHost }
    if ($HostDir) {
        $HostDir = Assert-PatchedCopy $HostDir
        if ($HostDir -eq $GameDir) { throw '-HostDir must be a different copy than -GameDir' }
        Assert-HostDll $HostDir
        Say "host copy $HostDir is patched and has the current dgsim.dll"
    }
    if ($GmsHost) {
        # C1b: dgserver starts the host (--gms-dir); a host copy, gms_script.exe, or any other file (every launch fails)
        if ($GmsProbe -or $HostDir) { throw '-GmsHost excludes -GmsProbe and -HostDir (dgserver starts the host)' }
        $gh = (Resolve-Path $GmsHost).Path
        if ((Get-Item $gh).PSIsContainer) {
            $copy = if (Test-Path (Join-Path $gh 'game\Dawngate.exe')) { $gh } elseif (Test-Path (Join-Path $gh 'Dawngate\game\Dawngate.exe')) { Join-Path $gh 'Dawngate' } else { throw "-GmsHost $gh holds no game\Dawngate.exe or Dawngate\game\Dawngate.exe" }
            $copy = Assert-PatchedCopy $copy
            if ($copy -eq $GameDir) { throw '-GmsHost must be a different copy than -GameDir' }
            Assert-HostDll $copy
            $GmsHostPath = $copy; $GmsHostKind = 'dawngate'
            if (-not $PSBoundParameters.ContainsKey('GmsHostArgs')) { $GmsHostArgs = '-multipleInstances' }
            Say "host copy $copy is patched and has the current dgsim.dll; dgserver starts it per game"
        } elseif ((Split-Path $gh -Leaf) -ieq 'gms_script.exe') {
            if ($gh -ne (Resolve-Path $ScriptExe -ErrorAction SilentlyContinue).Path) { throw "-GmsHost: use $ScriptExe (the leftover rules cover gms\build only)" }
            Assert-ScriptExe
            $GmsHostPath = $gh; $GmsHostKind = 'script'
            if (-not $PSBoundParameters.ContainsKey('GmsHostArgs')) { $GmsHostArgs = '--script-a' }
            Say "dgserver starts gms_script.exe in host mode per game"
        } else {
            $GmsHostPath = $gh; $GmsHostKind = 'file'
            Say "-GmsHost $gh is no host: every launch fails (the error-reply test)" 'Yellow'
        }
    }

    # 2. build the server (a second when nothing changed); a leftover server would lock the binary
    $leftoversBefore = @(Stop-SessionProcesses 'leftover' { $_.Name -eq 'dgserver.exe' })
    # (A6: each lane its own binary, since a running server locks its exe: lane b's is bin\lane_b\dgserver.exe)
    New-Item -ItemType Directory -Force (Split-Path $ServerExe -Parent) | Out-Null
    $build = Start-Logged $Go @('build', '-C', "`"$(Join-Path $RepoRoot 'server')`"", '-o', ($LaneCfg.ServerExe -replace '^server\\', ''), '.\cmd\dgserver') 'build'
    $build.WaitForExit()
    foreach ($f in @('build.out', 'build.err')) {
        $item = Get-Item (In-Session $f) -ErrorAction SilentlyContinue
        if ($item -and $item.Length -eq 0) { Remove-Item $item.FullName }
    }
    if ($build.ExitCode -ne 0 -or -not (Test-Path $ServerExe)) {
        throw "server build failed (exit $($build.ExitCode)): $(Short ((Get-Content (In-Session 'build.err') -Raw) -replace '\s+', ' ') 300)"
    }
    $Ports = Get-ServerPorts
    Say "built $ServerExe (ports $($Ports -join ', '))"

    # 3. leftovers of earlier sessions, then the ports
    $leftoversBefore += @(Stop-SessionProcesses 'leftover')
    $held = @(Wait-PortsFree 10)
    if ($held.Count) {
        throw ('ports held by other programs (left alone): ' + (($held | ForEach-Object { "$($_.Port) by $($_.Name) pid=$($_.Pid) $(Short $_.Cmd 80)" }) -join '; '))
    }
    Say "ports free: $($Ports -join ', ')"

    # 4. server; --seconds is only a safety net in case this script dies, the server is stopped below
    # (play mode without a time limit: 8 hours, like the capture's autostop)
    $stubSeconds = if ($Seconds -le 0) { 8 * 3600 + 300 } else { 2 * $Seconds + 300 }
    $stubArgs = @('--log', "`"$SessionDir`"", '--seconds', $stubSeconds)
    if ($Lane -ne 'a' -or $LanAddr) {
        # A6: every listener, every address handed out, the control port and the hosts' GMS ports on the lane's address;
        # A5b: with -Lan every listener, address handed out and GMS port on this PC's LAN address, the control port
        # (hosts on this PC only) still on the lane's loopback address
        $stubArgs += @('--bind', $ServerAddr, '--public-host', $ServerAddr)
        if ($GmsHost) { $stubArgs += @('--gms-control-bind', $LaneAddr) }
    }
    $content = Join-Path $MainRoot 'server\data\content.json'
    if ($Lane -ne 'a' -or $RepoRoot -ne $MainRoot -or $LanAddr) {
        # A6: the lane's certificates and accounts (a worktree's server\ has neither); the content file is shared
        # (A5b: -Lan's certificates have CN = the LAN address: the client checks it against its redirector host)
        $certsDir = Join-Path $MainRoot $(if ($LanAddr) { $LanCopy.Certs } else { $LaneCfg.Certs })
        if ($LanAddr -and -not (Test-Path (Join-Path $certsDir 'server_cert.pem'))) { throw "-Lan needs $certsDir (setup_lane.py lan --address $LanAddr)" }
        $stubArgs += @('--certs', "`"$certsDir`"")
        if (-not $FreshData) { $stubArgs += @('--data', "`"$(Join-Path $MainRoot $LaneCfg.Data)`"") }
        if (Test-Path $content) { $stubArgs += @('--content', "`"$content`"") }
    }
    if ($FreshData) {
        # B7b: an empty accounts folder of this session's own (every login a fresh account), the shared content file
        $freshDir = In-Session 'data'
        New-Item -ItemType Directory -Force $freshDir | Out-Null
        $stubArgs += @('--data', "`"$freshDir`"")
        if ($Lane -eq 'a' -and $RepoRoot -eq $MainRoot -and (Test-Path $content)) { $stubArgs += @('--content', "`"$content`"") }
        Say "dgserver accounts: $freshDir (fresh, -FreshData)"
    }
    if ($Www) { $stubArgs += @('--www', "`"$((Resolve-Path $Www).Path)`"") }
    if ($GmsProbe) {
        # B9: RakNet 4.081 (protocol 5) serves the GMS port; dgserver only announces it. silent/connected: the
        # research probe; session: gms_script.exe, the scripted GMS of gms/ (G1)
        if ($GmsProbe -eq 'session') {
            Assert-ScriptExe
            $gmsExe = $ScriptExe; $gmsTag = 'gms_script'
            $probeArgs = @('--port', $GmsPort, '--seconds', $stubSeconds)
            if ($Lane -ne 'a' -or $LanAddr) { $probeArgs += @('--bind', $ServerAddr) }  # A6 (gms_script binds 127.0.0.1 by default), A5b
        } else {
            if (-not (Test-Path $ProbeExe)) { throw "-GmsProbe needs $ProbeExe (build re\scripts\raknet_probe, docs/GMS_PROTOCOL.md)" }
            $gmsExe = $ProbeExe; $gmsTag = 'gms_probe'
            $probeArgs = @('--listen', $GmsPort, $stubSeconds)
            if ($GmsProbe -eq 'connected') { $probeArgs += '--connected' }
        }
        $udpHeld = @(Get-LaneUdp $GmsPort)
        if ($udpHeld.Count) { throw "UDP ${ServerAddr}:$GmsPort (GMS) is held by pid $($udpHeld[0].OwningProcess)" }
        $stubArgs += @('--gms-port', $GmsPort, '--gms-external')
        if ($GmsArgs) { $probeArgs += @($GmsArgs -split '\s+' | Where-Object { $_ }) }
        $probe = Start-Logged $gmsExe $probeArgs $gmsTag
        $deadline = (Get-Date).AddSeconds(10)
        while (-not @(Get-NetUDPEndpoint -LocalPort $GmsPort -ErrorAction SilentlyContinue | Where-Object { $_.OwningProcess -eq $probe.Id }).Count) {
            if ($probe.HasExited -or (Get-Date) -gt $deadline) { throw "$gmsTag not listening on UDP $GmsPort (exit $($probe.ExitCode)): $(Short ((Get-Content (In-Session "$gmsTag.err") -Raw) -replace '\s+', ' ') 200)" }
            Start-Sleep -Milliseconds 200
        }
        Say "GMS $(if ($GmsProbe -eq 'session') { 'session' } else { 'probe' }): $(Split-Path $gmsExe -Leaf) $($probeArgs -join ' ') (pid $($probe.Id))"
    } elseif ($HostDir) {
        # G2: the match host binds the GMS port itself once its level is loaded; dgserver only announces it
        $udpHeld = @(Get-LaneUdp $GmsPort)
        if ($udpHeld.Count) { throw "UDP ${ServerAddr}:$GmsPort (GMS) is held by pid $($udpHeld[0].OwningProcess)" }
        $stubArgs += @('--gms-port', $GmsPort, '--gms-external')
        Say "GMS: served by the match host (DGGMS_BIND=${ServerAddr}:$GmsPort); dgserver --gms-external"
    } elseif ($GmsHost) {
        # C1b: dgserver starts a host per game; the pool's first port is 10042, as before
        $udpHeld = @(Get-LaneUdp $GmsPort)
        if ($udpHeld.Count) { throw "UDP ${ServerAddr}:$GmsPort (GMS) is held by pid $($udpHeld[0].OwningProcess)" }
        $stubArgs += @('--gms-dir', "`"$GmsHostPath`"", "`"--gms-args=$GmsHostArgs`"", '--gms-data', "`"$(In-Session 'host')`"")
        if ($HostOptions) { $stubArgs += "`"--gms-options=$HostOptions`"" }
        Say "GMS: dgserver starts the host per game: --gms-dir $GmsHostPath --gms-args '$GmsHostArgs'$(if ($HostOptions) { " --gms-options '$HostOptions'" })"
    }
    if ($ServerArgs) {
        # D8-live test aid: extra dgserver flags (e.g. --owned starter)
        $stubArgs += @($ServerArgs -split '\s+' | Where-Object { $_ })
        Say "dgserver extra flags (-ServerArgs): $ServerArgs"
    }
    $stub = Start-Logged $ServerExe $stubArgs 'stub'
    $deadline = (Get-Date).AddSeconds(20)
    while ($true) {
        Start-Sleep -Milliseconds 300
        $listening = @(Get-PortHolders | Where-Object { $_.Pid -eq $stub.Id } | ForEach-Object { $_.Port } | Sort-Object -Unique)
        if ($listening.Count -eq $Ports.Count) { break }
        if ($stub.HasExited) { throw "dgserver.exe exited during startup (code $($stub.ExitCode)): $(Short ((Get-Content (In-Session 'stub.err') -Raw) -replace '\s+', ' ') 300)" }
        if ((Get-Date) -gt $deadline) { throw "dgserver.exe not listening on all ports after 20 s (listening: $($listening -join ', '))" }
    }
    Say "server listening on $($listening -join ', ') (pid $($stub.Id))"

    # 5. the game, watched by launch_monitor.ps1 (capture, screenshots, closes the game)
    # (a server-mode harness copy, D1, writes game\dgsim.log; start each session without the old one)
    $simLog = Join-Path $GameDir 'game\dgsim.log'
    if (Test-Path $simLog) { Remove-Item $simLog -Force }
    $launchTime = Get-Date
    $monLimit = if ($Seconds -le 0) { 8 * 3600 + 600 } else { 2 * $Seconds + 180 }
    # -Play: the monitor never closes the game (-Seconds 0); a time limit is this script's, which then closes the game the
    # way Ctrl+C does, so a timed play session (play.ps1 -Check) runs the manual session's end path
    $monSeconds = if ($Play) { 0 } else { $Seconds }
    if ($Play) { Say "launch_monitor.ps1 -Seconds 0: the session ends when the game is closed (or on Ctrl+C here)$(if ($Seconds -gt 0) { "; this script closes it after $Seconds s" })" 'Green' }
    else { Say "launch_monitor.ps1 -Seconds $Seconds (the game window stays up for about $([int](1.1 * $Seconds) + 20) s)" }
    $monArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$MonitorPs`"",
        '-GameDir', "`"$GameDir`"", '-OutDir', "`"$SessionDir`"", '-Seconds', $monSeconds)
    if ($Login) { $monArgs += @('-Login', "`"$Login`"", '-LoginAt', $LoginAt) }
    if ($Actions) { $monArgs += @('-Actions', "`"$Actions`"") }
    if ($ShotTimes) { $monArgs += @('-ShotTimes', "`"$ShotTimes`"") } elseif ($Play) { $monArgs += @('-ShotTimes', 'none') }
    if ($Background) { $monArgs += @('-Background') }
    if ($KeepAlive -gt 0) { $monArgs += @('-KeepAlive', $KeepAlive); Say "keep-alive: a key-up of VK_F24 every $KeepAlive s (B12, against the AFK kick)" }
    if ($ShotFocus -ne 'auto') { $monArgs += @('-ShotFocus', $ShotFocus) }
    if ($Play) { $monArgs += @('-SnapLen', 256) }
    if ($NoCapture) { $monArgs += @('-NoCapture') }
    # A6: lane b's client beside lane a's; the colon form, since -File would take a value starting with '-' for a parameter
    # (A5b: the lan copy too, whatever the lane: lane a's client may hold the mutex)
    if ($LaneCfg.GameArgs -or $LanAddr) { $monArgs += '-GameArgs:-multipleInstances' }
    $mon = Start-Logged 'powershell.exe' $monArgs 'monitor'
    # A5b: the second client's monitor (-SecondClient): the same session, its own folder, no capture of its own
    $mon2 = $null
    $mon2Args = $null
    if ($Second) {
        $dir2 = In-Session 'client2'
        New-Item -ItemType Directory -Force $dir2 | Out-Null
        $mon2Args = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$MonitorPs`"",
            '-GameDir', "`"$GameDir2`"", '-OutDir', "`"$dir2`"", '-Seconds', $(if ($SecondRestartAt -gt 0) { $SecondRestartAt } else { $Seconds }),
            '-NoCapture', '-GameArgs:-multipleInstances')
        if ($Login2) { $mon2Args += @('-Login', "`"$Login2`"", '-LoginAt', $LoginAt2) }
        if ($Actions2) { $mon2Args += @('-Actions', "`"$Actions2`"") }
        if ($ShotTimes2) { $mon2Args += @('-ShotTimes', "`"$ShotTimes2`"") }
        if ($Background) { $mon2Args += @('-Background') }
        if ($KeepAlive -gt 0) { $mon2Args += @('-KeepAlive', $KeepAlive) }
        if ($ShotFocus -ne 'auto') { $mon2Args += @('-ShotFocus', $ShotFocus) }
    }
    # wait for the monitor; with -HostDir start the host once the monitor's capture runs, and sample it every second
    $monDeadline = (Get-Date).AddSeconds($monLimit + $(if ($Second) { $SecondAfter } else { 0 }))
    $mon3 = $null  # G3: the second client again after -SecondRestartAt
    $mon3Start = $null
    while (-not $mon.HasExited -or ($mon2 -and -not $mon2.HasExited) -or ($mon3 -and -not $mon3.HasExited)) {
        if ($SecondRestartAt -gt 0 -and $mon2 -and $mon2.HasExited -and -not $mon3 -and -not $mon.HasExited) {
            # G3: the second client's game was ended at -SecondRestartAt; start it again for the rest of the session
            $left = [int]($Seconds + $SecondAfter - ((Get-Date) - $launchTime).TotalSeconds)
            if ($left -lt 60) { $left = 60 }
            $dir3 = In-Session 'client2b'
            New-Item -ItemType Directory -Force $dir3 | Out-Null
            $mon3Args = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$MonitorPs`"",
                '-GameDir', "`"$GameDir2`"", '-OutDir', "`"$dir3`"", '-Seconds', $left, '-NoCapture', '-GameArgs:-multipleInstances')
            if ($Login2) { $mon3Args += @('-Login', "`"$Login2`"", '-LoginAt', $LoginAt2) }
            if ($Actions3) { $mon3Args += @('-Actions', "`"$Actions3`"") }
            if ($ShotTimes3) { $mon3Args += @('-ShotTimes', "`"$ShotTimes3`"") }
            if ($Background) { $mon3Args += @('-Background') }
            if ($KeepAlive -gt 0) { $mon3Args += @('-KeepAlive', $KeepAlive) }
            if ($ShotFocus -ne 'auto') { $mon3Args += @('-ShotFocus', $ShotFocus) }
            $mon3 = Start-Logged 'powershell.exe' $mon3Args 'monitor3'
            $mon3Start = Get-Date
            Say "second client restarted (-SecondRestartAt $SecondRestartAt): launch_monitor.ps1 -OutDir $dir3 -Seconds $left (pid $($mon3.Id)), $([int]((Get-Date) - $launchTime).TotalSeconds) s after the first"
        }
        if ((Get-Date) -gt $monDeadline) {
            Say "launch_monitor.ps1 still running after $monLimit s; stopping it" 'Yellow'
            foreach ($m in @($mon, $mon2, $mon3)) { if ($m -and -not $m.HasExited) { Stop-Process -Id $m.Id -Force -ErrorAction SilentlyContinue } }
            break
        }
        if ($Second -and -not $mon2 -and -not $mon.HasExited -and ((Get-Date) - $launchTime).TotalSeconds -ge $SecondAfter -and @(Get-SessionDumpcap).Count) {
            # once the first monitor's capture runs, so the egress check covers the second client from its start
            $mon2 = Start-Logged 'powershell.exe' $mon2Args 'monitor2'
            Say "second client: launch_monitor.ps1 -GameDir $GameDir2 -OutDir $(In-Session 'client2') (pid $($mon2.Id)), $([int]((Get-Date) - $launchTime).TotalSeconds) s after the first"
        }
        Watch-Client  # Q3b: every $ClientEvery s (client_samples.csv)
        if ($HostDir) {
            if (-not $HostState.Proc -and ((Get-Date) - $launchTime).TotalSeconds -ge $HostAfter -and @(Get-SessionDumpcap).Count) {
                if (-not $L2G) { $L2G = New-Object System.Threading.EventWaitHandle($false, [System.Threading.EventResetMode]::ManualReset, 'Global\Dawngate L2G') }
                Start-Host
            }
            Watch-Host
            Stop-HostAt
        }
        if ($GmsHost) {
            # C1b: dgserver starts the host at PLAY; sample it like -HostDir's
            Find-ServerHost
            Watch-Host
            Stop-HostAt
        }
        if ($Play) {
            # P1: the first Ctrl+C (or the time limit) closes the game, and the monitor then ends by itself; a Ctrl+C
            # while the session is already ending stops the monitor at once
            if (Test-CtrlC) {
                if (-not $playEnd) {
                    $playEnd = 'ended by Ctrl+C'
                    Say 'Ctrl+C: ending the session (press Ctrl+C again to stop at once)' 'Yellow'
                    Stop-PlayGame 'Ctrl+C'
                } else {
                    Say 'Ctrl+C again: stopping the monitor' 'Yellow'
                    Stop-Process -Id $mon.Id -Force -ErrorAction SilentlyContinue
                    break
                }
            } elseif ($Seconds -gt 0 -and -not $playEnd -and ((Get-Date) - $launchTime).TotalSeconds -ge $Seconds) {
                $playEnd = "ended by the time limit (-Seconds $Seconds)"
                Stop-PlayGame "time limit ($Seconds s)"
            }
        }
        Start-Sleep -Milliseconds 1000
    }
    Say "launch_monitor.ps1 finished (exit $($mon.ExitCode))$(if ($Second) { if ($mon2) { "; the second client's: exit $($mon2.ExitCode)" } else { '; the SECOND CLIENT NEVER STARTED' } })"
    if ($Play -and -not $playEnd) {
        $monText = "$(Get-Content (In-Session 'monitor.log') -Raw -Encoding UTF8 -ErrorAction SilentlyContinue)"
        $playEnd = if ($monText -match 'game exited at t=(\d+)s code=(-?\d+)') { "ended when the game was closed at t=$($Matches[1]) s (code $($Matches[2]))" } else { 'ended with the monitor (no game exit in monitor.log)' }
    }
    if ($GmsHost -and $HostState.Proc) {
        # C1b: the monitor has closed (or killed) the client, so its Blaze connection is gone: dgserver sends removePlayer
        # and abortGame, and the host must end by itself (logoutGMS, exit). Wait for that and dgserver's 'ended' line.
        $deadline = (Get-Date).AddSeconds(20)
        while (-not $HostState.Proc.HasExited -and (Get-Date) -lt $deadline) { Watch-Host; Start-Sleep -Milliseconds 250 }
        Watch-Host
        $endDeadline = (Get-Date).AddSeconds(5)
        while (-not @(Get-ServerLogLines | Where-Object { $_ -match "\[host\] match $($HostState.Match): ended" }).Count -and (Get-Date) -lt $endDeadline) { Start-Sleep -Milliseconds 250 }
        if ($HostState.Killed) { Say "host: killed by -HostKillAt $HostKillAt (code $($HostState.Proc.ExitCode))" }
        elseif ($HostState.Proc.HasExited) { Say "host: ended by itself before $([math]::Round(((Get-Date) - $launchTime).TotalSeconds)) s after the monitor started (code $($HostState.Proc.ExitCode))" }
        else { Say 'host: STILL RUNNING 20 s after the client was closed; stopping dgserver ends it (its job)' 'Yellow' }
        $HostState.Stopped = $true
    }
    if ($HostState.Proc -and -not $GmsHost) {
        Watch-Host
        $HostState.Stopped = $true
        $hostPid = $HostState.Proc.Id
        $stoppedAfter += @(Stop-SessionProcesses 'end of run (host)' { $_.ProcessId -eq $hostPid })
    }
    $stoppedAfter += @(Stop-SessionProcesses 'end of run' { $_.Name -in @('dgserver.exe', 'raknet_probe_p5.exe', 'gms_script.exe') })

    if ($Play) {
        # P1: the capture has no time limit (an 8-hour safety autostop); everything of the session has ended, so stop
        # it after a moment for the last packets (egress_report.py reads a capture whose last packet was cut short)
        Start-Sleep -Seconds 2
        $stoppedAfter += @(Stop-SessionProcesses 'end of play (capture)' { $_.Name -eq 'dumpcap.exe' })
    }
    # the monitor's dumpcap stops on its own (-a duration); wait for it so the capture is complete
    $capDeadline = $launchTime.AddSeconds([int](1.15 * $Seconds) + 70)  # autostop is 1.15 * Seconds + 40
    # (@() matters: a lone CimInstance has no .Count in Windows PowerShell 5.1)
    while (@(Get-SessionDumpcap).Count -and (Get-Date) -lt $capDeadline) { Start-Sleep -Seconds 1 }
    if (@(Get-SessionDumpcap).Count) {
        Say 'dumpcap still running past its autostop; stopping it (the capture may be cut short)' 'Yellow'
        $stoppedAfter += @(Stop-SessionProcesses 'capture overran' { $_.Name -eq 'dumpcap.exe' })
    }
    $capItem = Get-Item (In-Session 'capture.pcapng') -ErrorAction SilentlyContinue
    Say "capture finished ($(if ($capItem) { "$([int]($capItem.Length / 1KB)) KB" } elseif ($NoCapture) { 'none: -NoCapture' } else { 'no capture file' }))"
} catch {
    $failure = "$($_.Exception.Message)"
    Say "FAILED: $failure" 'Red'
} finally {
    # 6. cleanup: everything this session may have started (also runs on Ctrl+C)
    $stoppedAfter += @(Stop-SessionProcesses 'cleanup')
    if ($L2G) { $L2G.Dispose() }
    Restore-CtrlC
    Remove-SessionLock
}
$heldAfter = @(Wait-PortsFree $(if ($stub) { 10 } else { 0 }))
$leftAfter = @(Get-SessionProcesses)
$cleanNote = if ($heldAfter.Count -or $leftAfter.Count) {
    'NOT CLEAN: ' + ((@($heldAfter | ForEach-Object { "port $($_.Port) held by $($_.Name) pid=$($_.Pid)" }) +
                      @($leftAfter | ForEach-Object { "$($_.Name) pid=$($_.ProcessId) still running" })) -join '; ')
} else { 'ports free, no session process left' }
if ($cleanNote -like 'NOT CLEAN*') {
    Say $cleanNote 'Red'
    if (-not $failure) { $failure = $cleanNote }
}

# 7. egress check (whenever a capture exists)
$egress = $null
if (Test-Path (In-Session 'capture.pcapng')) {
    $egress = Invoke-EgressCheck $SessionDir
    if ($egress.ExitCode -eq 1) {
        $bar = '!' * 78
        Write-Host $bar -ForegroundColor Red
        Write-Host "EGRESS FLAGGED: $($egress.Total). Stop launching and fix this first (egress.txt):" -ForegroundColor Red
        foreach ($l in $egress.Flagged) { Write-Host $l -ForegroundColor Red }
        Write-Host $bar -ForegroundColor Red
        Say "egress: $($egress.Total)" 'Red'
    } elseif ($egress.ExitCode -ne 0) {
        $msg = "egress_report.py failed (exit $($egress.ExitCode)): $(Short ($egress.Err -replace '\s+', ' ') 200)"
        Say $msg 'Red'
        if (-not $failure) { $failure = $msg }
    } else {
        Say "egress: $($egress.Total)" 'Green'
    }
} elseif ($launchTime -and -not $failure -and -not $NoCapture) {
    $failure = 'no capture.pcapng; the egress check could not run'
}

# 8. collect: stub.log, this run's Application.log section; drop empty and duplicate files
$stubLogFile = Get-ChildItem $SessionDir -File | Where-Object Name -match '^\d{8}-\d{6}\.log$' | Select-Object -First 1
if ($stubLogFile) {
    Move-Item $stubLogFile.FullName (In-Session 'stub.log')
    Remove-Item (In-Session 'stub.out') -ErrorAction SilentlyContinue  # stdout repeats the log line by line
} elseif (Test-Path (In-Session 'stub.out')) {
    Move-Item (In-Session 'stub.out') (In-Session 'stub.log')
}
foreach ($f in @('stub.err', 'monitor.out', 'monitor.err', 'egress.err', 'gms_probe.err', 'gms_script.err')) {
    $item = Get-Item (In-Session $f) -ErrorAction SilentlyContinue
    if ($item -and $item.Length -eq 0) { Remove-Item $item.FullName -ErrorAction SilentlyContinue }
}
if ($GameDir -and (Test-Path (Join-Path $GameDir 'game\dgsim.log'))) {
    Move-Item (Join-Path $GameDir 'game\dgsim.log') (In-Session 'dgsim.log') -Force
}
$appSection = @()
if ($launchTime) {
    $appSection = @(Get-AppLogSection $launchTime)
    Set-Content -Path (In-Session 'application_log.txt') -Value $appSection -Encoding UTF8
}
$appSection2 = @()
$appSection2b = @()
if ($launchTime -and $Second -and (Test-Path (In-Session 'client2'))) {
    # A5b: the second client's own %APPDATA%\DawngateData<X>\Application.log
    $appSection2 = @(Get-AppLogSection $launchTime $AppLog2 $mon3Start)
    Set-Content -Path (In-Session 'client2\application_log.txt') -Value $appSection2 -Encoding UTF8
    if ($mon3Start -and (Test-Path (In-Session 'client2b'))) {
        # G3: the restarted second client's part of the same Application.log
        $appSection2b = @(Get-AppLogSection $mon3Start $AppLog2)
        Set-Content -Path (In-Session 'client2b\application_log.txt') -Value $appSection2b -Encoding UTF8
    }
    foreach ($f in @('monitor2.out', 'monitor2.err', 'monitor3.out', 'monitor3.err')) {
        $item = Get-Item (In-Session $f) -ErrorAction SilentlyContinue
        if ($item -and $item.Length -eq 0) { Remove-Item $item.FullName -ErrorAction SilentlyContinue }
    }
}

# summary
$result, $code = if ($egress -and $egress.ExitCode -eq 1) { 'EGRESS FLAGGED', 1 } elseif ($failure) { 'FAILED', 2 } else { 'OK', 0 }
$sum = @("==== session $Name`: $result (exit $code)$(if ($failure) { " - $failure" })", "folder   : $SessionDir")
if ($Lane -ne 'a' -or $RepoRoot -ne $MainRoot -or $LanAddr) { $sum += "lane     : $Lane ($LaneAddr; $GameDir$(if ($LanAddr) { "; lan profile on this PC's $LanAddr, egress --allow-host $LanAddr" })$(if ($Second) { "; second client $GameDir2 on $($Second.Bind)" })$(if ($RepoRoot -ne $MainRoot) { "; code from $RepoRoot" }))" }
if ($Play) { $sum += "play     : $(if ($playEnd) { $playEnd } else { 'ended by a failure' }) after $([int]((Get-Date) - $t0).TotalMinutes) min" }
function Get-GameSummary([string]$monLog, [string]$expect, [string]$tag) {
    # the 'game' and 'sockets' lines of one client from its monitor.log (A5b: also the second client's, 'client2')
    if (-not (Test-Path $monLog)) { return @() }
    $m = Get-Content $monLog -Raw -Encoding UTF8
    $game = @()
    if ($m -match 'started \S+ pid=(\d+)') { $game += "pid $($Matches[1])" }
    if ($m -match 'game exited at t=(\d+)s code=(-?\d+)') { $game += "$(if ($Play) { 'exited' } else { 'exited by itself' }) at t=$($Matches[1])s (code $($Matches[2]))" }
    elseif ($m -match 'force-killing game') { $game += 'force-killed after closing failed (modal dialog; expected)' }
    elseif ($m -match 'closing game') { $game += 'closed' }
    if ($m -match 'crash sender running') { $game += 'CRASH SENDER RAN (game crashed?)' }
    if ($m -match 'keepalive: (\d+) key-up\(s\) posted \(every (\d+) s\)') { $game += "keep-alive key-ups $($Matches[1]) (every $($Matches[2]) s, B12)" }
    $out = @("$("$tag".PadRight(9)): $($game -join ', ')")
    # local addresses of the game's sockets as the monitor saw them (A4: all 127.0.0.1 since the bind patch)
    $socks = @([regex]::Matches($m, '(UDP bound|TCP) (\S+):(\d+)') | ForEach-Object { "$(($_.Groups[1].Value -split ' ')[0]) $($_.Groups[2].Value):$($_.Groups[3].Value)" } | Sort-Object -Unique)
    if ($socks.Count) {
        $nUdp = @($socks | Where-Object { $_ -like 'UDP *' }).Count
        if ($expect -match '^127\.') {
            $wide = @($socks | Where-Object { ($_ -split ' ')[1] -notmatch '^(127\.|::1:)' })
            # A6: every socket on the lane's own address (lane b: 127.0.0.2); another loopback address is noted
            $otherLoop = @($socks | Where-Object { ($_ -split ' ')[1] -match '^(127\.|::1:)' -and -not ($_ -split ' ')[1].StartsWith("${expect}:") })
            $sockState = if ($wide.Count) { "NOT loopback-only: $($wide -join ', ')" } elseif ($otherLoop.Count) { "loopback, but not only $expect`: $($otherLoop -join ', ')" } else { "$expect only" }
        } else {
            # A5b: the lan copy binds this PC's LAN address only (the bind cave narrows every wildcard to it)
            $other = @($socks | Where-Object { -not ($_ -split ' ')[1].StartsWith("${expect}:") })
            $sockState = if ($other.Count) { "NOT only $expect`: $($other -join ', ')" } else { "$expect only (lan profile)" }
        }
        $out += "$("sockets$(if ($tag -ne 'game') { '2' })".PadRight(9)): $sockState ($nUdp UDP, $($socks.Count - $nUdp) TCP local endpoints)"
    }
    $out
}
function Get-AppLogSummary([string[]]$section, [string]$tag) {
    if (-not $section.Count) { return @() }
    $states = @($section | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ($_ -split '-> ')[-1].Trim() })
    $asserts = @($section | Where-Object { $_ -match 'Assert Failed' }).Count
    $qos = "$($section | Where-Object { $_ -match 'Ping site latency: (.*)' } | Select-Object -Last 1)" -replace '^.*Ping site latency: ', ''
    $netQos = "$($section | Where-Object { $_ -match 'Network QOS data: (.*)' } | Select-Object -Last 1)" -replace '^.*Network QOS data: ', ''
    $gmsLine = "$($section | Where-Object { $_ -match 'initiating connection to GMS' } | Select-Object -First 1)" -replace '^.*initiating connection to GMS ', ''
    # B12: in a game straight back to the login screen = the client's AFK kick (or another logout)
    $kick = if (($states -join ' > ') -match 'kGameStateDungeon > kGameStateLogin') { '; DUNGEON -> LOGIN (the AFK kick or a logout, B12)' } else { '' }
    "$($tag.PadRight(9)): states $(if ($states) { $states -join ' > ' } else { 'none' }); $asserts assert(s)$(if ($qos) { "; ping site latency $qos" })$(if ($netQos) { "; network QoS $netQos" })$(if ($gmsLine) { "; GMS connect $gmsLine" })$kick"
}
$sum += @(Get-GameSummary (In-Session 'monitor.log') $ServerAddr 'game')
$sum += @(Get-AppLogSummary $appSection 'app log')
$sum += "client res: $(Get-ClientResSummary)"
if ($Second) {
    $sum += @(Get-GameSummary (In-Session 'client2\monitor.log') $Second.Bind 'client2')
    $sum += @(Get-AppLogSummary $appSection2 'app log2')
    if ($mon3Start) {
        $sum += @(Get-GameSummary (In-Session 'client2b\monitor.log') $Second.Bind 'client2b')
        $sum += @(Get-AppLogSummary $appSection2b 'app log2b')
    }
}
if (Test-Path (In-Session 'stub.log')) {
    $s = Get-StubSummary (In-Session 'stub.log')
    # B7b: dgserver's accounts, content and new-account line, and the flags this session added
    $srv = "$(Get-Content (In-Session 'stub.log') -Encoding UTF8 -TotalCount 8 | Where-Object { $_ -match '\[stub\] accounts (in|memory)' } | Select-Object -First 1)" -replace '^.*?\[stub\] ', ''
    $sum += "server   : $(if ($srv) { $srv } else { 'no accounts/content line in stub.log' })$(if ($ServerArgs) { "; -ServerArgs $ServerArgs" })$(if ($FreshData) { '; -FreshData' })"
    # B7b: the e-mail the server got. Posted WM_CHARs are lost while another window has the focus (another lane's
    # focused screenshot, V1), so a session meant to continue an account may have logged in a new one.
    # (A5b: with a second client each login's e-mail is looked for among all MAIL lines)
    $mails = @(Get-Content (In-Session 'stub.log') -Encoding UTF8 | Where-Object { $_ -match "^\s+MAIL: '(.*)'" } | ForEach-Object { $_ -replace "^\s+MAIL: '(.*)'.*$", '$1' })
    foreach ($pair in @(@('login', $Login), @('login2', $(if ($Second) { $Login2 } else { '' })))) {
        if (-not $pair[1]) { continue }
        $wantMail = ($pair[1] -split '\|', 2)[0]
        $gotMail = if ($Second) { if ($mails -contains $wantMail) { $wantMail } else { "$($mails -join "', '")" } } else { "$($mails | Select-Object -First 1)" }
        $sum += "$($pair[0].PadRight(9)): $(if (-not $mails.Count) { 'no login request' } elseif ($gotMail -eq $wantMail) { "MAIL '$gotMail' as typed" } else { "MAIL '$gotMail' != '$wantMail' (KEYSTROKES LOST: another window had the focus while the form was typed)" })"
    }
    $sum += "stub     : connections $(if ($s.Connections) { $s.Connections } else { 'none' })"
    # (A5b: the log doesn't name the connection, so with two clients a reply can pair with the other client's request:
    # a5b_run2d's 'getCatalog -> not answered' was two interleaved, answered requests)
    if ($s.Requests) { $sum += "requests : $(if ($Second) { '(two clients: pairing approximate) ' })$($s.Requests)" }
    if ($s.Http) { $sum += "http     : $($s.Http)" }
    if ($s.Qos) { $sum += "qos      : $($s.Qos)" }
    if ($s.Gms) { $sum += "gms      : $($s.Gms)" }
    $sum += "telemetry: $(Get-TelemetrySummary @(Get-Content (In-Session 'stub.log') -Encoding UTF8) $appSection)"
}
# the GMS on the GMS port: raknet_probe's gms_probe.out or gms_script's gms_script.out (same line formats)
foreach ($f in @('gms_probe.out', 'gms_script.out')) {
    if (Test-Path (In-Session $f)) { $sum += "gms probe: $(Get-GmsProbeSummary (In-Session $f))" }
}
# the harness log: game\dgsim.log of a -GameDir harness copy (moved here), or the host's (-HostDir) in host\
$simLogFile = if ($HostState.Proc) { Join-Path $HostState.Data 'dgsim.log' } else { In-Session 'dgsim.log' }
if (Test-Path $simLogFile) {
    $d = @(Get-Content $simLogFile)
    $phase = "$($d | Where-Object { $_ -match '\] phase \d' } | Select-Object -Last 1)" -replace '^.*\] ', ''
    $exc = @($d | Where-Object { $_ -match 'EXCEPTION in' }).Count
    $sas = @($d | Where-Object { $_ -match 'ASSERT \[server\]' }).Count
    $st = "$($d | Where-Object { $_ -match '\] stats t\+' } | Select-Object -Last 1)" -replace '^.*\] stats ', ''
    $sum += "dgsim    : last $(if ($phase) { $phase } else { 'phase: none' }); $exc exception(s); $sas server assert line(s)$(if ($st) { "; last stats $st" })"
    $sa = "$($d | Where-Object { $_ -match '\] stats abilities: ' } | Select-Object -Last 1)" -replace '^.*\] stats abilities: ', ''
    $sd = "$($d | Where-Object { $_ -match '\] stats damage: ' } | Select-Object -Last 1)" -replace '^.*\] stats damage: ', ''
    if ($sa -or $sd) { $sum += "dgsim ab : $sa$(if ($sd) { "; damage: $sd" })" }
}
if ($GmsHost -and (Test-Path (In-Session 'stub.log'))) {
    # C1b: dgserver's side of the host (its [host] lines)
    $sum += "host ctl : $(Get-HostControlSummary @(Get-Content (In-Session 'stub.log') -Encoding UTF8))"
    $scriptOut = Join-Path $HostState.Data 'stdout.txt'
    if ($GmsHostKind -eq 'script' -and (Test-Path $scriptOut)) {
        $sum += "host gms : $(Get-GmsProbeSummary $scriptOut)"
        $ctlEnd = "$(Get-Content $scriptOut | Where-Object { $_ -match ' host: control ' } | Select-Object -Last 1)" -replace '^.* host: ', ''
        if ($ctlEnd) { $sum += "host end : $ctlEnd" }
    }
}
if ($HostState.Proc) {
    # -HostDir (D1d) and -GmsHost (C1b): startup milestones from the DLL, resources and sockets from the samples, the
    # host's own app log
    $marks = Get-HostTimings
    $tm = @($marks.Keys | ForEach-Object { "$_ $([math]::Round($marks[$_] / 1000.0, 1)) s" }) -join ', '
    $how = if ($HostState.Killed) { "KILLED by -HostKillAt $HostKillAt (code $($HostState.Exit))" }
           elseif ($null -ne $HostState.Exit) { if ($GmsHost) { "ended by itself (code $($HostState.Exit))" } else { "EXITED BY ITSELF (code $($HostState.Exit))" } }
           elseif ($GmsHost) { 'STILL RUNNING after the client was gone (ended with dgserver)' } else { 'stopped at the end of the run' }
    $by = if ($GmsHost) { "match $($HostState.Match), started by dgserver" } else { 'started' }
    $sum += "host     : pid $($HostState.Proc.Id) ($GmsHostKind$(if (-not $GmsHost) { 'HostDir' })), $by $([int]($HostState.Started - $launchTime).TotalSeconds) s after the monitor, $how; after process start: $(if ($tm) { $tm } else { 'no timing lines' })"
    $wall = if ($HostState.Last) { [math]::Max(1, $HostState.Last.T) } else { 1 }
    $last = $HostState.Last
    $sum += "host res : private max $($HostState.MaxPrivate) MB$(if ($last) { " (last $($last.Private))" }), working set max $($HostState.MaxWs) MB, peak working set $($HostState.Peak) MB, CPU $($HostState.Cpu) s in $wall s ($([math]::Round(100 * $HostState.Cpu / $wall)) % of one core); window $(if ($HostState.Visible) { "VISIBLE in $($HostState.Visible) of $($HostState.Samples) samples" } else { "hidden in all $($HostState.Samples) samples" }) (host_samples.csv)"
    $hs = "$(if (Test-Path (Join-Path $HostState.Data 'dgsim.log')) { Get-Content (Join-Path $HostState.Data 'dgsim.log') | Where-Object { $_ -match 'stats host: ' } | Select-Object -Last 1 })" -replace '^.*stats host: ', ''
    $hostSimLog = Join-Path $HostState.Data 'dgsim.log'
    if ((Test-Path $hostSimLog) -and (Select-String -Path $hostSimLog -Pattern 'RakNet .* server, RAKNET_PROTOCOL_VERSION' -Quiet)) {
        $sum += "host gms : $(Get-GmsProbeSummary $hostSimLog)"
        $gs = "$(Get-Content $hostSimLog | Where-Object { $_ -match 'stats gms: ' } | Select-Object -Last 1)" -replace '^.*stats gms: ', ''
        if ($gs) { $sum += "host gst : $gs" }
    }
    $sum += "host net : $(if ($HostState.Sockets.Count) { "SOCKETS: $(@($HostState.Sockets.Keys) -join ', ')" } else { 'no TCP/UDP endpoint seen' })$(if ($hs -match '(refused sockets \d+, dns \d+)') { "; DLL: $($Matches[1])" })"
    if ($hs -match '(lua heap .*)$') { $sum += "host lua : $($Matches[1]) (R1b; the last stats host line)" }
    if ((Test-Path $hostSimLog) -and $GmsHost) {
        # C1b: how the DLL ended (control.cpp: 'host: exiting (code N): why', then its control state)
        $ex = @(Get-Content $hostSimLog | Where-Object { $_ -match '\] host: (exiting \(code .*|control .*)$' } | ForEach-Object { $_ -replace '^.*\] host: ', '' })
        if ($ex.Count) { $sum += "host end : $($ex -join '; ')" }
    }
    $hostApp = Join-Path $HostState.Data "AppData\DawngateData$($LaneCfg.Suffix)\Application.log"  # A6: lane b's DawngateDataB
    if ($GmsHostKind -eq 'script') {
        # gms_script has no game folders
    } elseif (Test-Path $hostApp) {
        $ha = @(Get-Content $hostApp)
        $hstates = @($ha | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ($_ -split '-> ')[-1].Trim() })
        $sum += "host app : $hostApp ($($ha.Count) lines); states $(if ($hstates) { $hstates -join ' > ' } else { 'none (kGameStateBoot)' }); $(@($ha | Where-Object { $_ -match 'Assert Failed' }).Count) assert(s)"
    } else {
        $sum += "host app : no Application.log in $($HostState.Data)\AppData (folder redirect off or not reached)"
    }
}
if ($egress) { $sum += "egress   : $($egress.Total) (egress.txt)" }
elseif ($NoCapture) { $sum += 'egress   : not checked (-NoCapture: no capture)' }
$shots = @(Get-ChildItem $SessionDir -Filter '*.png' | Sort-Object LastWriteTime | ForEach-Object { $_.BaseName })
if ($shots.Count) {
    # V1: how each screenshot was taken (monitor.log: '(window)'/'(screen)', 'focus=...' with -ShotFocus on)
    $shotLines = @(Get-Content (In-Session 'monitor.log') -ErrorAction SilentlyContinue | Where-Object { $_ -match ' screenshot \S+: \d+x\d+ \(' })
    $byHow = @($shotLines | ForEach-Object { if ($_ -match '\((window|screen)[^)]*\)') { $Matches[1] } } | Group-Object | ForEach-Object { "$($_.Name) $($_.Count)" })
    $noFocus = @($shotLines | Where-Object { $_ -match 'focus=failed' }).Count
    $focusNote = if (@($shotLines | Where-Object { $_ -match ' focus=' }).Count) { "; brought in front$(if ($noFocus) { ", $noFocus of them failed" })" } else { '' }
    $sum += "shots    : $($shots -join ', ')$(if ($byHow) { " ($($byHow -join ', ')$focusNote)" })"
}
if ($Second -and (Test-Path (In-Session 'client2'))) {
    $shots2 = @(Get-ChildItem (In-Session 'client2') -Filter '*.png' | Sort-Object LastWriteTime | ForEach-Object { $_.BaseName })
    if ($shots2.Count) { $sum += "shots2   : client2\: $($shots2 -join ', ')" }
}
$stoppedNames = @($stoppedAfter | Where-Object { $_ } | ForEach-Object { $_.Name } | Group-Object | ForEach-Object { "$($_.Name) x$($_.Count)" })
$sum += "cleanup  : $($leftoversBefore.Count) leftover(s) stopped before the run; stopped after it: $(if ($stoppedNames) { $stoppedNames -join ', ' } else { 'nothing' }); $cleanNote"
$sum += "duration : $([int]((Get-Date) - $t0).TotalSeconds) s"
Set-Content -Path (In-Session 'summary.txt') -Value $sum -Encoding UTF8
Write-Host ''
foreach ($l in $sum) { Write-Host $l -ForegroundColor $(if ($code) { 'Red' } else { 'Green' }) }
exit $code
