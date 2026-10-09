<#
  Play Dawngate on a server on another machine of your network (roadmap P2; the manual is docs/PLAYING.md *Playing on a
  server on another machine*). The player's side of the LAN setup: no local server and no match host, only the client.
  play.ps1 -Server <S> runs this script.

  One command: checks the lan copy that setup_all.py --player made (client\run_854_lan\Dawngate: patched, the lan
  profile for the server <S>, the config packages pointing at <S>), starts the client through launch_monitor.ps1 (it
  plays the launcher's part) in a normal window with no time limit and no scripted input, captures the session (256
  bytes per packet) and at the end runs the egress check, egress_report.py --allow-host <S>: a flow from the game to any
  other address, and anything EA-related, is flagged. The session ends when you close the game or press Ctrl+C in this
  window, as with play.ps1 (run_session.ps1 -Play): Ctrl+C is read as a key ([Console]::TreatControlCAsInput, so the
  monitor gets no console signal), closes the game and stops it after 10 s in a match (the game ignores WM_CLOSE
  there); a second Ctrl+C stops the monitor at once. Then the capture is stopped, the egress check runs and a short
  summary follows (summary.txt in the session folder re\logs\play_lan_<date>-<time>\).

  What it checks before the start (and what it says about a gap):
    tools        .venv (Python: patch_client.py --show, egress_report.py), Wireshark's dumpcap and tshark (unless
                 -NoCapture)
    the copy     -GameDir (default client\run_854_lan\Dawngate): never client\ia_*; Dawngate.exe, Config.package and
                 Config_Ship.package patched (each differs from its .orig); patch_client.py --show: the lan profile
                 (redirector = <S>, not loopback), our CA (ca_flag 0), binds as shipped or one address; Config.package
                 names http://<S>:18080 (patch_config.py --host <S>)  -> prints the setup_all.py --player command
    the server   -Server <S>, or without it the address the copy was patched for; an address of our own network
                 (private, link-local or shared; loopback, public and multicast addresses are refused)
    one client   setup_all.py --player's copy shares lane a's data folders (%APPDATA%\DawngateData) and runs without
                 -multipleInstances, so no other Dawngate client may hold the Global\Dawngate mutex
  Locks (run_session.ps1's format and rules): setup_all.py --player's copy (data folders DawngateData) takes lane a's
  re\logs\session.lock in play mode, so play.ps1 and lane a's test sessions refuse to start meanwhile (they share its
  data folders) and lanes b-d wait as for play.ps1 ("the user is playing"); it refuses while that lock names a live
  session. On the development PC the LAN-profile test copy client\run_854_l (setup_lane.py lan, DawngateDataL) takes
  re\logs\session_lan.lock instead (one lan session at a time: run_session.ps1 -Lan refuses meanwhile) and runs with
  -multipleInstances beside lane a's client; it refuses while the user plays (lane a's play lock). Every other copy is
  refused. The session folder's lane.txt is 'a' or 'l' accordingly. Leftovers of an earlier session (after a closed
  PowerShell window) are stopped, and only those: the copy's Dawngate.exe and CrashSender1402.exe, and the monitor and
  dumpcap of a session folder of the same lane letter.

  The capture: launch_monitor.ps1's default interfaces (the default-route adapter and loopback) when the route to <S>
  leaves through the default-route adapter (or <S> is this PC); otherwise also the adapter that reaches <S>, and
  without a default route that adapter and loopback (-CaptureIfaces).

  Usage (in a PowerShell window, from the repository folder or anywhere):
    powershell -ExecutionPolicy Bypass -File re\scripts\play_lan.ps1 [-Server <S>] [-NoCapture] [-KeepAlive]
        [-Seconds N] [-Name <logs folder>] [-AllowHost <H>[,<H2>...]] [-GameDir <Dawngate folder>]
    powershell -ExecutionPolicy Bypass -File re\scripts\play_lan.ps1 -SetupOnly [-Server <S>]   # only the checks
    powershell -ExecutionPolicy Bypass -File re\scripts\play_lan.ps1 -DryRun [...]   # the checks and the plan
  -AllowHost: further machines of our own network the game may talk to, i.e. the operator's match-host machines when
  the server runs hosts on other machines (agent mode, docs/OPERATORS.md *Extra host machines*): the client connects to
  the GMS address in NotifyGameSetup's HNET, which is then that machine, not <S>. -KeepAlive (B12) posts a key-up of
  VK_F24 every 60 s against the client's idle kick. -Seconds N ends the session after N s the way Ctrl+C does.
  -DryRun runs the checks, then prints the lock it would take, the leftovers it would stop, the monitor's and the egress
  check's command lines and the capture interfaces; it starts nothing and writes nothing (exit 0 when a session would
  start, 2 when it would refuse). -StateRoot <dir> (a test aid, like setup_all.py --root) takes the default copy, the
  locks and the session folders from <dir> instead of the main checkout and counts only games under <dir> for the
  mutex check (re\scripts\test_p2.ps1's scratch folders, so its cases don't depend on the PC's running lanes).
  Check mode (the live check, roadmap P2; normally through re\scripts\test_p2.ps1 -Live): -Check logs in a new account
  and clicks to PRACTICE DAWNGATE as play.ps1 -Check does (posted window messages only, the window kept off the desktop,
  -Seconds default 150); it passes when the client reached kGameStateDungeon with a GMS address and was still in it
  when the time limit closed the game (P2-live), egress has 0 flagged lines and the cleanup is clean.
  Exit code: 0 ok; 1 egress flagged (stop and read egress.txt); 2 setup missing or the session failed; 3 (-Check) the
  session was clean but the client didn't reach the game.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [string]$Server,
    [string]$GameDir,
    [string]$AllowHost,
    [switch]$Check,
    [int]$Seconds = 0,
    [string]$Name,
    [switch]$NoCapture,
    [switch]$KeepAlive,
    [switch]$SetupOnly,
    [switch]$DryRun,
    [string]$StateRoot
)
$ErrorActionPreference = 'Stop'
# the code (these scripts) comes from this checkout; the copies, .venv, re\logs from the main checkout, also when this
# checkout is a git worktree (as run_session.ps1)
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$MainRoot = $RepoRoot
if (-not (Test-Path (Join-Path $RepoRoot 'client'))) {
    try {
        $common = "$(& git -C $RepoRoot rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
        if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'client')) { $MainRoot = $cand } }
    } catch { }
}
if ($StateRoot) {
    if (-not (Test-Path $StateRoot)) { Write-Host "-StateRoot $StateRoot does not exist" -ForegroundColor Red; exit 2 }
    $StateRoot = (Resolve-Path $StateRoot).Path
} else { $StateRoot = $MainRoot }
$Python = Join-Path $MainRoot '.venv\Scripts\python.exe'
$MonitorPs = Join-Path $RepoRoot 're\scripts\launch_monitor.ps1'
$EgressPy = Join-Path $RepoRoot 're\scripts\egress_report.py'
$PatchPy = Join-Path $RepoRoot 're\scripts\patch_client.py'
$Wireshark = 'C:\Program Files\Wireshark'
$PlayerCopy = 'client\run_854_lan\Dawngate'   # setup_all.py --player's copy
$LanTestCopy = 'client\run_854_l\Dawngate'    # setup_lane.py lan's copy (run_session.ps1's $LanCopy.Client, A5b)
$LockFiles = @{ a = 're\logs\session.lock'; lan = 're\logs\session_lan.lock' }  # run_session.ps1's lane a and lan locks
# B8's clicks after the login (play.ps1 -Check's): the news overlay's forward arrow twice, its OK button, PLAY, PRACTICE
$CheckActions = '24:0.5555,0.8514;26:0.5555,0.8514;28:0.8016,0.8486;32:0.4984,0.0833;36:0.5,0.8236'

# ---- rules without side effects (re\scripts\test_p2.ps1 runs them on their own) ----
function Test-LanAddress([string]$addr) {
    # $null when $addr is an IPv4 address of our own network (private, link-local or shared: a subset of what
    # patch_client.py's lan profile and egress_report.py --allow-host accept), else why not
    $ip = $null
    if ($addr -notmatch '^\d{1,3}(\.\d{1,3}){3}$' -or -not [Net.IPAddress]::TryParse($addr, [ref]$ip)) { return "'$addr' is not an IPv4 address" }
    $b = $ip.GetAddressBytes()
    $own = $b[0] -eq 10 -or ($b[0] -eq 172 -and $b[1] -ge 16 -and $b[1] -le 31) -or ($b[0] -eq 192 -and $b[1] -eq 168) -or
           ($b[0] -eq 169 -and $b[1] -eq 254) -or ($b[0] -eq 100 -and $b[1] -ge 64 -and $b[1] -le 127)
    if (-not $own) { return "$addr is not an address of our own network (private 10/8, 172.16/12, 192.168/16, link-local 169.254/16 or shared 100.64/10; loopback, public and multicast addresses are refused)" }
    $null
}
function ConvertFrom-ShowLines([string[]]$lines) {
    # patch_client.py --show's key=value lines
    $show = [ordered]@{}
    foreach ($l in $lines) { if ("$l" -match '^(\w+)=(.*)$') { $show[$Matches[1]] = $Matches[2].Trim() } }
    $show
}
function Get-CopyProblems($show, [string]$server) {
    # what keeps a copy from playing on $server (patch_client.py --show's values); empty = none
    $p = @()
    if (-not $show -or -not $show.Contains('redirector')) { return @('patch_client.py --show read nothing from its Dawngate.exe') }
    $r = "$($show['redirector'])"
    if ($r -match '^127\.' -or $r -eq 'localhost') { $p += "it is a local-profile copy (redirector $r), not a lan copy" }
    elseif ($r -notmatch '^\d+\.\d+\.\d+\.\d+$') { $p += "its redirector host is '$r', not a server address (not patched by patch_client.py --profile lan)" }
    elseif ($server -and $r -ne $server) { $p += "it is patched for the server $r, not $server" }
    if ("$($show['ca_flag'])" -ne '0') { $p += "its CA flag is '$($show['ca_flag'])' (not patched for a server's CA)" }
    $bind = "$($show['bind'])"
    if ($bind -ne 'as-shipped' -and $bind -notmatch '^\d+\.\d+\.\d+\.\d+$') { $p += "its socket binds are '$bind'" }
    elseif ($bind -match '^127\.') { $p += "its sockets bind the loopback address $bind (a test lane's copy)" }
    $p
}
function Get-CopyKind([string]$dir, $show, [string]$stateRoot) {
    # which lock, lane letter and command line a copy gets: setup_all.py --player's copy (shared data folders) or the
    # development PC's LAN-profile test copy client\run_854_l (its own data folders); $null Problem = playable here
    $appdata = "$($show['appdata'])"
    $lanTest = (Join-Path $stateRoot $LanTestCopy).TrimEnd('\').ToLower()
    if ($dir.TrimEnd('\').ToLower() -eq $lanTest) {
        $problem = if ($appdata -ne 'DawngateDataL') { "the lan test copy keeps its own data folders (DawngateDataL), this one ${appdata}: run setup_lane.py lan --address <S> again" } else { $null }
        return [pscustomobject]@{ Kind = 'lan test copy'; Lock = 'lan'; Letter = 'l'; Multi = $true; AppData = $appdata; Problem = $problem }
    }
    if ($appdata -eq 'DawngateData') {
        return [pscustomobject]@{ Kind = 'player copy'; Lock = 'a'; Letter = 'a'; Multi = $false; AppData = $appdata; Problem = $null }
    }
    [pscustomobject]@{ Kind = 'other'; Lock = $null; Letter = $null; Multi = $false; AppData = $appdata
        Problem = "its data folders are %APPDATA%\${appdata}: this script plays setup_all.py --player's copy (DawngateData) or the lan test copy $LanTestCopy" }
}
function Get-ProcessStartStamp([int]$id) { try { (Get-Process -Id $id -ErrorAction Stop).StartTime.ToString('o') } catch { $null } }
function Get-LiveLock([string]$file) {
    # run_session.ps1's: the lock's text if it names a live process (not this one), else $null
    if (-not (Test-Path $file)) { return $null }
    $text = "$(Get-Content $file -Raw -ErrorAction SilentlyContinue)".Trim()
    if ($text -match 'pid=(\d+) start=(\S+)' -and [int]$Matches[1] -ne $PID -and (Get-ProcessStartStamp ([int]$Matches[1])) -eq $Matches[2]) { return $text }
    $null
}
function Get-LockRefusal($kind, [string]$stateRoot) {
    # $null when the copy's lock is free, else why this session must not start
    $laneA = Join-Path $stateRoot $LockFiles['a']
    if ($kind.Lock -eq 'a') {
        $t = Get-LiveLock $laneA
        if ($t) { return "another session is running in lane a ($t; $laneA): play.ps1 and lane a's test sessions use the same data folders as this copy. Wait until it has ended." }
        return $null
    }
    $lanLock = Join-Path $stateRoot $LockFiles['lan']
    $t = Get-LiveLock $lanLock
    if ($t) { return "another lan session is running ($t; $lanLock); one lan session at a time on this PC." }
    $t = Get-LiveLock $laneA
    if ($t -match 'mode=play') { return "the user is playing (lane a: $t); the lan test copy waits until that game is closed." }
    $null
}
function Get-FolderLane([string]$cmd, [string]$stateRoot) {
    # run_session.ps1's: the lane of a dumpcap or monitor by the session folder in its command line (lane.txt there;
    # none = lane a)
    $logs = [regex]::Escape((Join-Path $stateRoot 're\logs\').ToLower())
    if ($cmd.ToLower() -notmatch "$logs([^\\`"]+)") { return 'a' }
    $marker = Join-Path $stateRoot "re\logs\$($Matches[1])\lane.txt"
    $l = "$(Get-Content $marker -Raw -ErrorAction SilentlyContinue)".Trim().ToLower()
    if ($l -match '^[a-z]$') { $l } else { 'a' }
}
function Get-MutexHolders($procs, [string]$copyPrefix, [string]$scope) {
    # the Dawngate.exe processes that hold the Global\Dawngate mutex (started without -multipleInstances) and aren't this
    # copy's own (a leftover, stopped at the start); $scope: only those under that folder (the scratch tests)
    @($procs | Where-Object {
        $exe = "$($_.ExecutablePath)".ToLower()
        -not $exe.StartsWith($copyPrefix) -and "$($_.CommandLine)" -notmatch '-multipleInstances' -and
            (-not $scope -or $exe.StartsWith($scope.ToLower().TrimEnd('\') + '\'))
    })
}
function Get-PlayEnd([string]$monText) {
    if ($monText -match 'game exited at t=(\d+)s code=(-?\d+)') { return "ended when the game was closed at t=$($Matches[1]) s (code $($Matches[2]))" }
    'ended with the monitor (no game exit in monitor.log)'
}
function Get-SocketsLine([string]$monText, [string]$bind) {
    # the local addresses of the game's sockets as the monitor saw them (run_session.ps1's 'sockets' line)
    $socks = @([regex]::Matches($monText, '(UDP bound|TCP) (\S+):(\d+)') | ForEach-Object { "$(($_.Groups[1].Value -split ' ')[0]) $($_.Groups[2].Value):$($_.Groups[3].Value)" } | Sort-Object -Unique)
    if (-not $socks.Count) { return 'no game socket seen' }
    $nUdp = @($socks | Where-Object { $_ -like 'UDP *' }).Count
    $addrs = @($socks | ForEach-Object { (($_ -split ' ')[1]) -replace ':\d+$', '' } | Sort-Object -Unique)
    $state = if ($bind -match '^\d+\.\d+\.\d+\.\d+$') {
        $other = @($addrs | Where-Object { $_ -ne $bind })
        if ($other.Count) { "NOT only $bind`: also $($other -join ', ')" } else { "$bind only" }
    } else { "bound as shipped: $($addrs -join ', ')" }
    "$state ($nUdp UDP, $($socks.Count - $nUdp) TCP local endpoints)"
}
function Get-AppLogLine([string[]]$section, [string[]]$allowed) {
    # states, asserts and the GMS address from this run's Application.log section; a GMS address the egress check
    # doesn't allow (a match host on another machine of the operator's: -AllowHost) is pointed out
    if (-not $section.Count) { return 'no Application.log section' }
    $states = @($section | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ($_ -split '-> ')[-1].Trim() })
    $asserts = @($section | Where-Object { $_ -match 'Assert Failed' }).Count
    $gms = "$($section | Where-Object { $_ -match 'initiating connection to GMS' } | Select-Object -First 1)" -replace '^.*initiating connection to GMS ', ''
    if ($gms -match '\[addr=(\d+\.\d+\.\d+\.\d+)\]' -and $allowed -notcontains $Matches[1]) { $gms += " (NOT an allowed address: a match host on another machine? -AllowHost $($Matches[1]))" }
    $kick = if (($states -join ' > ') -match 'kGameStateDungeon > kGameStateLogin') { '; DUNGEON -> LOGIN (the idle kick or a logout, B12)' } else { '' }
    "states $(if ($states) { $states -join ' > ' } else { 'none' }); $asserts assert(s)$(if ($gms) { "; GMS connect $gms" })$kick"
}
function Get-CheckResult([string[]]$section) {
    # -Check's verdict from this run's Application.log section: the GMS connect, kGameStateDungeon reached, and still in
    # it when the check closed the game (P2-live: in p2live_run1b the server was stopped from outside 41 s into the
    # match, the client went DUNGEON -> LOGIN, and a check that asked only for the reached state passed)
    $states = @($section | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ($_ -split '-> ')[-1].Trim() })
    $inGame = $states -contains 'kGameStateDungeon'
    $last = if ($states.Count) { $states[-1] } else { '' }
    [pscustomobject]@{
        Gms = @($section | Where-Object { $_ -match 'initiating connection to GMS' }).Count -gt 0
        InGame = $inGame
        Stayed = $inGame -and $last -eq 'kGameStateDungeon'
        Last = $last
    }
}
function Get-CapturePlan([string]$server) {
    # the dumpcap interfaces: $null Ifaces = launch_monitor.ps1's default (the default-route adapter and loopback)
    try {
        $route = @(Get-NetRoute -DestinationPrefix 0.0.0.0/0 -ErrorAction SilentlyContinue | Sort-Object RouteMetric) | Select-Object -First 1
        $defIdx = if ($route) { [int]$route.ifIndex } else { 0 }
        $own = @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $server -ErrorAction SilentlyContinue).Count -gt 0
        if ($own) {
            if ($defIdx) { return [pscustomobject]@{ Ifaces = $null; Note = "the default-route adapter and loopback ($server is this PC: its traffic stays on the loopback adapter)" } }
            return [pscustomobject]@{ Ifaces = '\Device\NPF_Loopback'; Note = "loopback only ($server is this PC, no default route)" }
        }
        $toS = @(Find-NetRoute -RemoteIPAddress $server -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceIndex }) | Select-Object -First 1
        $sIdx = if ($toS) { [int]$toS.InterfaceIndex } else { 0 }
        if ($defIdx -and (-not $sIdx -or $sIdx -eq $defIdx)) { return [pscustomobject]@{ Ifaces = $null; Note = "the default-route adapter (it reaches $server) and loopback" } }
        $list = @()
        foreach ($i in @(@($defIdx, $sIdx) | Where-Object { $_ } | Select-Object -Unique)) {
            $nic = Get-NetAdapter -InterfaceIndex $i -IncludeHidden -ErrorAction SilentlyContinue
            if ($nic) { $list += "\Device\NPF_$($nic.InterfaceGuid)" }
        }
        if (-not $list.Count) { return [pscustomobject]@{ Ifaces = $null; Note = "no adapter found for $server; launch_monitor.ps1's default" } }
        $list += '\Device\NPF_Loopback'
        [pscustomobject]@{ Ifaces = ($list -join ','); Note = "$(if ($defIdx) { 'the default-route adapter, ' } else { 'no default route; ' })the adapter that reaches $server and loopback" }
    } catch { [pscustomobject]@{ Ifaces = $null; Note = "route lookup failed ($($_.Exception.Message)); launch_monitor.ps1's default" } }
}

# ---- arguments ----
function Fail([string]$m) { Write-Host $m -ForegroundColor Red; exit 2 }
if ($Name -and $Name -notmatch '^[\w.-]+$') { Fail "invalid -Name '$Name' (letters, digits, _ . - only)" }
if ($Check -and $NoCapture) { Fail '-Check always runs with the capture and the egress check' }
if ($Seconds -lt 0) { Fail '-Seconds must be 0 (no time limit) or more' }
if ($Server) { $why = Test-LanAddress $Server; if ($why) { Fail "-Server: $why" } }
$Allowed = @()
foreach ($h in @("$AllowHost" -split '[,;\s]+' | Where-Object { $_ })) {
    $why = Test-LanAddress $h
    if ($why) { Fail "-AllowHost: $why" }
    if ($Allowed -notcontains $h) { $Allowed += $h }
}

$missing = New-Object System.Collections.Generic.List[string]
function Ok([string]$m) { Write-Host "  ok       $m" -ForegroundColor Green }
function Info([string]$m) { Write-Host "  note     $m" -ForegroundColor Gray }
function Missing([string]$what, [string[]]$commands) {
    $missing.Add($what)
    Write-Host "  MISSING  $what" -ForegroundColor Red
    foreach ($c in $commands) { Write-Host "             $c" -ForegroundColor Yellow }
}
function Test-Patched([string]$dir) {
    # run_session.ps1's rule: exe and both config packages differ from their .orig (patch_client.py, patch_config.py)
    foreach ($f in @('game\Dawngate.exe', 'game\Data\Config.package', 'game\Data\Config_Ship.package')) {
        $live = Join-Path $dir $f
        if (-not (Test-Path $live) -or -not (Test-Path "$live.orig")) { return $false }
        if ((Get-FileHash $live).Hash -eq (Get-FileHash "$live.orig").Hash) { return $false }
    }
    $true
}
function Test-ConfigNames([string]$dir, [string]$server) {
    # patch_config.py --host <S> wrote http://<S>:18080 into Config.package (setup_all.py's check of a host copy)
    $bytes = [IO.File]::ReadAllBytes((Join-Path $dir 'game\Data\Config.package'))
    $text = [Text.Encoding]::GetEncoding(28591).GetString($bytes)  # Latin-1: one char per byte
    $text.Contains("http://${server}:18080")
}

# ---- the setup ----
if (-not $GameDir) { $GameDir = Join-Path $StateRoot $PlayerCopy }
elseif (-not [IO.Path]::IsPathRooted($GameDir) -and -not (Test-Path $GameDir) -and (Test-Path (Join-Path $StateRoot $GameDir))) { $GameDir = Join-Path $StateRoot $GameDir }
if (Test-Path $GameDir) { $GameDir = (Resolve-Path $GameDir).Path }
$GameDir = $GameDir.TrimEnd('\')
$setupCmd = ".venv\Scripts\python -I re\scripts\setup_all.py --player --server-host $(if ($Server) { $Server } else { '<S>' }) --ca <the server's ca_modulus.bin> [--client-bind <this machine's LAN address>]"
$show = $null
$kind = $null
Write-Host "Dawngate on a server on another machine: checking the lan copy $GameDir" -ForegroundColor Cyan
if ($GameDir -match '\\client\\ia_') { Fail 'refusing the pristine extracted build (client\ia_*); play the lan copy setup_all.py --player made' }

if (Test-Path $Python) { Ok '.venv (Python)' }
else { Missing 'the Python environment .venv' @('python -m venv .venv', '.venv\Scripts\pip install pefile cryptography') }
if ($NoCapture) { Info 'no capture and no egress check (-NoCapture)' }
elseif ((Test-Path "$Wireshark\dumpcap.exe") -and (Test-Path "$Wireshark\tshark.exe")) { Ok 'Wireshark (capture and egress check)' }
else { Missing "Wireshark in $Wireshark with Npcap (loopback traffic capture on)" @('see docs/SETUP.md *Wireshark and Npcap*; or play without the capture and the egress check: -NoCapture') }

if (-not (Test-Path (Join-Path $GameDir 'game\Dawngate.exe'))) {
    Missing "the lan copy $GameDir" @($setupCmd)
} elseif (-not (Test-Patched $GameDir)) {
    Missing "the copy $GameDir is not patched (it would contact EA)" @($setupCmd)
} elseif (Test-Path $Python) {
    $show = ConvertFrom-ShowLines @(& $Python -I $PatchPy --show (Join-Path $GameDir 'game\Dawngate.exe'))
    if (-not $Server -and "$($show['redirector'])" -match '^\d+\.\d+\.\d+\.\d+$' -and -not (Test-LanAddress $show['redirector'])) {
        $Server = $show['redirector']
        Info "server $Server (the address the copy was patched for; -Server not given)"
    }
    $problems = @(Get-CopyProblems $show $Server)
    if (-not $Server -and -not $problems.Count) { $problems += "its redirector host $($show['redirector']) is not an address of our own network" }
    if ($problems.Count) { Missing "the copy doesn't fit: $($problems -join '; ')" @($setupCmd) }
    elseif (-not (Test-ConfigNames $GameDir $Server)) { Missing "the copy's Config.package doesn't name http://${Server}:18080 (patch_config.py --host $Server)" @($setupCmd) }
    else {
        $kind = Get-CopyKind $GameDir $show $StateRoot
        if ($kind.Problem) { Missing "the copy doesn't fit: $($kind.Problem)" @($setupCmd) }
        else { Ok "$($kind.Kind) $GameDir (lan profile for $Server, binds $($show['bind']), %APPDATA%\$($kind.AppData))" }
    }
}

# one client at a time without -multipleInstances (the Global\Dawngate mutex): the player copy can't run beside another
# client that holds it; this copy's own Dawngate.exe is a leftover (stopped at the start)
$copyPrefix = $GameDir.ToLower() + '\'
if ($kind -and -not $kind.Multi) {
    # (-StateRoot, the scratch tests: only games under it count, so a test doesn't depend on the PC's running lanes)
    $scope = if ($StateRoot -ne $MainRoot) { $StateRoot } else { $null }
    foreach ($g in @(Get-MutexHolders @(Get-CimInstance Win32_Process -Filter "Name='Dawngate.exe'") $copyPrefix $scope)) {
        Missing "another Dawngate client is running (pid $($g.ProcessId), $(if ($g.ExecutablePath) { $g.ExecutablePath } else { 'path unknown' }))" @('close it first: only one Dawngate client can run at a time (the Global\Dawngate mutex)')
    }
}

if ($missing.Count) {
    Write-Host ''
    Write-Host "Setup incomplete ($($missing.Count) item(s) above). Run the yellow commands from the repository folder, then start this again (docs/PLAYING.md *Playing on a server on another machine*)." -ForegroundColor Red
    exit 2
}
if ($SetupOnly) { Write-Host "Setup complete: the lan copy is ready for $Server." -ForegroundColor Green; exit 0 }

# ---- the plan ----
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
if (-not $Name) { $Name = if ($Check) { "play_lan_check_$stamp" } else { "play_lan_$stamp" } }
if ($Check -and $Seconds -le 0) { $Seconds = 150 }
$SessionDir = Join-Path $StateRoot "re\logs\$Name"
if (Test-Path $SessionDir) { Fail "session folder $SessionDir exists already; pick another -Name" }
$LockFile = Join-Path $StateRoot $LockFiles[$kind.Lock]
$refusal = Get-LockRefusal $kind $StateRoot
$Allowed = @(@($Server) + @($Allowed | Where-Object { $_ -ne $Server }))
$capture = if ($NoCapture) { $null } else { Get-CapturePlan $Server }
$monArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$MonitorPs`"", '-GameDir', "`"$GameDir`"", '-OutDir', "`"$SessionDir`"",
    '-Seconds', 0, '-ShotTimes', 'none')
if ($NoCapture) { $monArgs += '-NoCapture' }
else {
    $monArgs += @('-SnapLen', 256)
    if ($capture.Ifaces) { $monArgs += @('-CaptureIfaces', "`"$($capture.Ifaces)`"") }
}
if ($KeepAlive) { $monArgs += @('-KeepAlive', 60) }
if ($kind.Multi) { $monArgs += '-GameArgs:-multipleInstances' }  # the colon form: -File would take '-multipleInstances' for a parameter
if ($Check) { $monArgs += @('-Login', "`"$Name@example.com|dawngate1`"", '-LoginAt', 12, '-Actions', "`"$CheckActions`"", '-Background') }
$egArgs = @('-I', "`"$EgressPy`"", "`"$SessionDir`"") + @($Allowed | ForEach-Object { @('--allow-host', $_) })

function Get-SessionProcesses {
    # what this session (or an earlier one of the same copy and lane letter) runs: the copy's game and crash sender,
    # and the monitor and dumpcap of a session folder with this lane letter. Never anything else.
    $all = @(Get-CimInstance Win32_Process)
    @($all | Where-Object {
        $cmd = "$($_.CommandLine)".ToLower(); $exe = "$($_.ExecutablePath)".ToLower()
        ([int]$_.ProcessId -ne $PID) -and (
            ($_.Name -in @('Dawngate.exe', 'CrashSender1402.exe') -and $exe.StartsWith($copyPrefix)) -or
            ($_.Name -eq 'dumpcap.exe' -and $cmd.Contains((Join-Path $StateRoot 're\logs\').ToLower()) -and (Get-FolderLane $cmd $StateRoot) -eq $kind.Letter) -or
            ($_.Name -eq 'powershell.exe' -and $cmd -match '-file\s+"?[^"]*launch_monitor\.ps1' -and $cmd.Contains((Join-Path $StateRoot 're\logs\').ToLower()) -and (Get-FolderLane $cmd $StateRoot) -eq $kind.Letter))
    })
}

if ($DryRun) {
    $left = @(Get-SessionProcesses)
    Write-Host ''
    Write-Host "Dry run (nothing started, nothing written):" -ForegroundColor Cyan
    Write-Host "  server   : $Server; the egress check allows $($Allowed -join ', ') only"
    Write-Host "  copy     : $($kind.Kind) $GameDir (data folders %APPDATA%\$($kind.AppData); $(if ($kind.Multi) { 'runs with -multipleInstances' } else { 'runs without -multipleInstances' }))"
    Write-Host "  lock     : $LockFile, lane letter $($kind.Letter): $(if ($refusal) { "WOULD REFUSE: $refusal" } else { 'free' })"
    Write-Host "  folder   : $SessionDir (lane.txt '$($kind.Letter)')"
    Write-Host "  leftovers: $(if ($left.Count) { "would stop $(@($left | ForEach-Object { "$($_.Name) pid=$($_.ProcessId)" }) -join ', ')" } else { 'none' })"
    Write-Host "  capture  : $(if ($NoCapture) { 'none (-NoCapture): no egress check' } else { "256 bytes per packet; $($capture.Note)" })"
    Write-Host "  monitor  : powershell.exe $($monArgs -join ' ')"
    Write-Host "  egress   : $(if ($NoCapture) { 'not checked' } else { "$Python $($egArgs -join ' ')" })"
    Write-Host "  end      : the game closed, Ctrl+C here$(if ($Seconds -gt 0) { ", or the time limit $Seconds s" }); then the capture stops and the egress check and the summary follow"
    exit $(if ($refusal) { 2 } else { 0 })
}
if ($refusal) {
    Write-Host $refusal -ForegroundColor Red
    Write-Host 'Only one session at a time per copy: a new one would stop the running one''s processes as leftovers.' -ForegroundColor Red
    exit 2
}

# ---- Ctrl+C as a key, closing the game (run_session.ps1 -Play's) ----
$CtrlC = @{ Enabled = $false; Saved = $null }
function Enable-CtrlCAsInput {
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
function Restore-CtrlC { if ($null -ne $CtrlC.Saved) { try { [Console]::TreatControlCAsInput = $CtrlC.Saved } catch { } } }
function Say([string]$m, [string]$color = 'Gray') {
    $line = '{0:HH:mm:ss} {1}' -f (Get-Date), $m
    Write-Host $line -ForegroundColor $color
    Add-Content -Path (Join-Path $SessionDir 'session.log') -Value $line -Encoding utf8
}
function Stop-PlayGame([string]$why) {
    # the copy's client only; in a match it ignores WM_CLOSE
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
function Stop-SessionProcesses([string]$why, [scriptblock]$filter = { $true }) {
    $procs = @(Get-SessionProcesses | Where-Object -FilterScript $filter)
    foreach ($p in $procs) {
        Say ('{0}: stopping {1} pid={2}' -f $why, $p.Name, $p.ProcessId) 'Yellow'
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($procs.Count) { Start-Sleep -Milliseconds 1000 }
    $procs
}
function Get-AppLogSection([datetime]$since, [string]$file) {
    # Application.log is appended to; each launch starts with "### yyyy-MM-dd HH:mm:ss" (local time)
    if (-not (Test-Path $file)) { return @("(no $file)") }
    $lines = @(Get-Content $file)
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        if ($lines[$i] -match '^### (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})') {
            $t = [datetime]::ParseExact($Matches[1], 'yyyy-MM-dd HH:mm:ss', [Globalization.CultureInfo]::InvariantCulture)
            if ($t -ge $since.AddSeconds(-5)) { return $lines[$i..($lines.Count - 1)] }
            break
        }
    }
    @("(no Application.log section started after $since; the last 40 lines of $file follow)") + @($lines | Select-Object -Last 40)
}

# ---- the session ----
New-Item -ItemType Directory -Force $SessionDir | Out-Null
Set-Content -Path (Join-Path $SessionDir 'lane.txt') -Value $kind.Letter -Encoding ASCII
$lockLine = "pid=$PID start=$(Get-ProcessStartStamp $PID) name=$Name mode=play lane=$($kind.Letter) since=$(Get-Date -Format s) lan=$Server"
Set-Content -Path $LockFile -Encoding ASCII -Value $lockLine
$t0 = Get-Date
$failure = $null
$launchTime = $null
$playEnd = $null
$leftoversBefore = @()
$stoppedAfter = @()
$mon = $null
$capFile = Join-Path $SessionDir 'capture.pcapng'
try {
    Say "session $Name -> $SessionDir (play mode; server $Server; $($kind.Kind) $GameDir; lock $LockFile)"
    if (-not $Check) {
        Write-Host ''
        Write-Host "Starting Dawngate for the server $Server (the window opens after about 5 s):" -ForegroundColor Cyan
        Write-Host '  1. Log in with any e-mail and password (the first login on this server creates the account).'
        Write-Host '  2. The first time, a news overlay appears: click its right arrow twice, then OK.'
        Write-Host '  3. PLAY, then PRACTICE DAWNGATE: the server starts a match host and the game loads.'
        Write-Host '  To stop: quit the game, or press Ctrl+C here. The egress check and a summary follow.'
        Write-Host ''
    } else { Say "check mode: scripted login and PRACTICE DAWNGATE, window off the desktop, $Seconds s" 'Cyan' }
    Enable-CtrlCAsInput
    $leftoversBefore = @(Stop-SessionProcesses 'leftover')
    $launchTime = Get-Date
    Say "launch_monitor.ps1 -Seconds 0: the session ends when the game is closed (or on Ctrl+C here)$(if ($Seconds -gt 0) { "; this script closes it after $Seconds s" })$(if (-not $NoCapture) { "; capture: $($capture.Note)" })" 'Green'
    $mon = Start-Process -FilePath 'powershell.exe' -ArgumentList $monArgs -WorkingDirectory $RepoRoot -NoNewWindow -PassThru `
        -RedirectStandardOutput (Join-Path $SessionDir 'monitor.out') -RedirectStandardError (Join-Path $SessionDir 'monitor.err')
    $null = $mon.Handle  # keeps ExitCode readable after the process ends (Windows PowerShell quirk)
    $monDeadline = (Get-Date).AddSeconds(8 * 3600 + 600)
    while (-not $mon.HasExited) {
        if ((Get-Date) -gt $monDeadline) { Say 'launch_monitor.ps1 still running after 8 hours; stopping it' 'Yellow'; Stop-Process -Id $mon.Id -Force -ErrorAction SilentlyContinue; break }
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
        Start-Sleep -Milliseconds 1000
    }
    Say "launch_monitor.ps1 finished (exit $($mon.ExitCode))"
    if (-not $playEnd) { $playEnd = Get-PlayEnd "$(Get-Content (Join-Path $SessionDir 'monitor.log') -Raw -Encoding UTF8 -ErrorAction SilentlyContinue)" }
    if (-not $NoCapture) {
        # the capture has no time limit (an 8-hour safety autostop): stop it after a moment for the last packets
        # (egress_report.py reads a capture whose last packet was cut short)
        Start-Sleep -Seconds 2
        $capLower = $capFile.ToLower()
        $stoppedAfter += @(Stop-SessionProcesses 'end of play (capture)' { $_.Name -eq 'dumpcap.exe' -and "$($_.CommandLine)".ToLower().Contains($capLower) })
    }
    $capItem = Get-Item $capFile -ErrorAction SilentlyContinue
    Say "capture finished ($(if ($capItem) { "$([int]($capItem.Length / 1KB)) KB" } elseif ($NoCapture) { 'none: -NoCapture' } else { 'no capture file' }))"
} catch {
    $failure = "$($_.Exception.Message)"
    Say "FAILED: $failure" 'Red'
} finally {
    # cleanup: whatever this session may have started (also on an error)
    $stoppedAfter += @(Stop-SessionProcesses 'cleanup')
    Restore-CtrlC
    if ("$(Get-Content $LockFile -Raw -ErrorAction SilentlyContinue)" -match "pid=$PID ") { Remove-Item $LockFile -Force -ErrorAction SilentlyContinue }
}
$leftAfter = @(Get-SessionProcesses)
$cleanNote = if ($leftAfter.Count) { 'NOT CLEAN: ' + (@($leftAfter | ForEach-Object { "$($_.Name) pid=$($_.ProcessId) still running" }) -join '; ') } else { 'no session process left' }
if ($leftAfter.Count) { Say $cleanNote 'Red'; if (-not $failure) { $failure = $cleanNote } }

# the egress check (whenever a capture exists): only <S> and -AllowHost are allowed
$egress = $null
if (Test-Path $capFile) {
    $p = Start-Process -FilePath $Python -ArgumentList $egArgs -WorkingDirectory $RepoRoot -NoNewWindow -PassThru `
        -RedirectStandardOutput (Join-Path $SessionDir 'egress.txt') -RedirectStandardError (Join-Path $SessionDir 'egress.err')
    $null = $p.Handle
    $p.WaitForExit()
    $text = @(Get-Content (Join-Path $SessionDir 'egress.txt') -Encoding UTF8)
    $egress = [pscustomobject]@{ ExitCode = $p.ExitCode; Total = "$($text | Where-Object { $_ -match '^\d+ flagged line' } | Select-Object -Last 1)"; Flagged = @($text | Where-Object { $_ -match '<-- FLAGGED' }) }
    if ($egress.ExitCode -eq 1) {
        $bar = '!' * 78
        Write-Host $bar -ForegroundColor Red
        Write-Host "EGRESS FLAGGED: $($egress.Total). Stop playing and report it (egress.txt):" -ForegroundColor Red
        foreach ($l in $egress.Flagged) { Write-Host $l -ForegroundColor Red }
        Write-Host $bar -ForegroundColor Red
        Say "egress: $($egress.Total)" 'Red'
    } elseif ($egress.ExitCode -ne 0) {
        $msg = "egress_report.py failed (exit $($egress.ExitCode)): $("$(Get-Content (Join-Path $SessionDir 'egress.err') -Raw -ErrorAction SilentlyContinue)" -replace '\s+', ' ')"
        Say $msg 'Red'
        if (-not $failure) { $failure = $msg }
    } else { Say "egress: $($egress.Total)" 'Green' }
} elseif ($launchTime -and -not $failure -and -not $NoCapture) { $failure = 'no capture.pcapng; the egress check could not run' }
foreach ($f in @('monitor.out', 'monitor.err', 'egress.err')) {
    $item = Get-Item (Join-Path $SessionDir $f) -ErrorAction SilentlyContinue
    if ($item -and $item.Length -eq 0) { Remove-Item $item.FullName -ErrorAction SilentlyContinue }
}

# collect this run's Application.log section, then the summary
$appSection = @()
if ($launchTime) {
    $appSection = @(Get-AppLogSection $launchTime (Join-Path $env:APPDATA "$($kind.AppData)\Application.log"))
    Set-Content -Path (Join-Path $SessionDir 'application_log.txt') -Value $appSection -Encoding UTF8
}
$result, $code = if ($egress -and $egress.ExitCode -eq 1) { 'EGRESS FLAGGED', 1 } elseif ($failure) { 'FAILED', 2 } else { 'OK', 0 }
$monText = "$(Get-Content (Join-Path $SessionDir 'monitor.log') -Raw -Encoding UTF8 -ErrorAction SilentlyContinue)"
$sum = @("==== session $Name`: $result (exit $code)$(if ($failure) { " - $failure" })", "folder   : $SessionDir")
$sum += "server   : $Server ($($kind.Kind) $GameDir; lane letter $($kind.Letter), lock $LockFile; egress --allow-host $($Allowed -join ', '))"
$sum += "play     : $(if ($playEnd) { $playEnd } else { 'ended by a failure' }) after $([int]((Get-Date) - $t0).TotalMinutes) min"
$game = @()
if ($monText -match 'started \S+ pid=(\d+)') { $game += "pid $($Matches[1])" }
if ($monText -match 'game exited at t=(\d+)s code=(-?\d+)') { $game += "exited at t=$($Matches[1])s (code $($Matches[2]))" }
if ($monText -match 'crash sender running') { $game += 'CRASH SENDER RAN (game crashed?)' }
if ($monText -match 'keepalive: (\d+) key-up\(s\) posted \(every (\d+) s\)') { $game += "keep-alive key-ups $($Matches[1]) (every $($Matches[2]) s, B12)" }
$sum += "game     : $(if ($game.Count) { $game -join ', ' } else { 'no monitor.log' })"
$sum += "sockets  : $(Get-SocketsLine $monText "$($show['bind'])")"
$sum += "app log  : $(Get-AppLogLine $appSection $Allowed)"
if ($egress) { $sum += "egress   : $($egress.Total) (egress.txt)" } elseif ($NoCapture) { $sum += 'egress   : not checked (-NoCapture: no capture)' }
$stoppedNames = @($stoppedAfter | Where-Object { $_ } | ForEach-Object { $_.Name } | Group-Object | ForEach-Object { "$($_.Name) x$($_.Count)" })
$sum += "cleanup  : $($leftoversBefore.Count) leftover(s) stopped before the run; stopped after it: $(if ($stoppedNames) { $stoppedNames -join ', ' } else { 'nothing' }); $cleanNote"
$sum += "duration : $([int]((Get-Date) - $t0).TotalSeconds) s"
Set-Content -Path (Join-Path $SessionDir 'summary.txt') -Value $sum -Encoding UTF8

# the short summary (play.ps1's form)
$color = if ($code -eq 0) { 'Green' } else { 'Red' }
Write-Host ''
Write-Host "==== Dawngate session $Name over: $(if ($code -eq 0) { 'OK' } elseif ($code -eq 1) { 'EGRESS FLAGGED: stop and read egress.txt' } else { 'FAILED (see summary.txt)' })" -ForegroundColor $color
Write-Host "  server  : $Server" -ForegroundColor $color
Write-Host "  session : $(if ($playEnd) { $playEnd } else { 'ended by a failure' })" -ForegroundColor $color
Write-Host "  egress  : $(if ($egress) { $egress.Total } elseif ($NoCapture) { 'not checked (-NoCapture)' } else { 'not run' })" -ForegroundColor $color
Write-Host "  cleanup : $cleanNote" -ForegroundColor $color
Write-Host "  logs    : $SessionDir" -ForegroundColor $color
Write-Host '            summary.txt; the client: application_log.txt; the server''s logs are on the server machine' -ForegroundColor $color

if ($Check) {
    $cr = Get-CheckResult $appSection
    if ($code -eq 0 -and $cr.Stayed -and $cr.Gms) {
        Write-Host "CHECK PASSED: login on $Server, PRACTICE, the client reached kGameStateDungeon on the server's match host and was still in it at the end; egress and cleanup clean." -ForegroundColor Green
        exit 0
    }
    $left = if ($cr.InGame -and -not $cr.Stayed) { " (it left the match: kGameStateDungeon -> $($cr.Last); the server or the match host went away?)" } else { '' }
    Write-Host "CHECK FAILED: session exit $code; GMS connect: $($cr.Gms); client in kGameStateDungeon: $($cr.InGame); still in it at the end: $($cr.Stayed)$left." -ForegroundColor Red
    exit $(if ($code -ne 0) { $code } else { 3 })
}
exit $code
