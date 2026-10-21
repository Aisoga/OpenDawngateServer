<#
  Play Dawngate yourself (the manual is docs/development/running-from-source.md).

  One command: checks the setup (and builds or installs what is missing where that is safe and quick, otherwise prints
  the exact command), starts our server so that PLAY -> PRACTICE DAWNGATE starts a match host, starts the client in a
  normal window with no time limit and no scripted input, and cleans up (client, host, server, ports) when you close
  the client or press Ctrl+C in this window. The packet capture and the egress check (nothing may leave this PC) stay on;
  -NoCapture turns both off. At the end it prints a short summary: egress, cleanup, where the logs are.

  It is run_session.ps1 -Play -GmsHost client\run_854_server with the same safety:
  patched working copies only, leftovers of earlier sessions stopped, capture, egress check, cleanup, one session at a
  time (re\logs\session.lock). The logs go to re\logs\play_<date>-<time>\.

  What it checks, in order (and what it does about a gap):
    tools        .venv (Python), Go (builds the server), Wireshark's dumpcap/tshark (unless -NoCapture)  -> prints the setup
    certificates server\certs\server_cert.pem, server_key.pem, ca_modulus.bin                            -> prints make_certs.py
    client copy  client\run_854\Dawngate patched by patch_client.py and patch_config.py, with our CA      -> prints the copy/patch commands
    bot box      its game\Data\zz_MatchOptions.snap is current (CREATE GAME's bot check box, BOTBOX)      -> installs it (setup_lane.py's step)
    bot profiles its game\Data\zz_BotProfiles.snap is current (bot_profiles.py --check; PLAYPROF)        -> installs it (bot_profiles.py)
    host copy    client\run_854_server\Dawngate patched, with the harness installed                     -> prints setup_server_copy.py
    bot profiles the host copy's game\Data\zz_BotProfiles.snap is current, as for the client copy        -> installs it (bot_profiles.py)
    gms build    gms\build\Release\dgsim.dll's last build newer than gms\host, gms\engine, gms\core, gms\script -> builds it (cmake), if gms\build is configured
                 (G5: its build stamp dgsim.dll.stamp; re\scripts\gms_stale.ps1)
    host DLL     the host copy's PhysXLoader.dll is that dgsim.dll                                      -> installs it (setup_server_copy.py)
    no other     Dawngate.exe running from outside client\ (one client at a time: the Global\Dawngate mutex)
    content      server\data\content.json (optional: without it new accounts own no items)              -> prints the commands
  The server itself (server\bin\dgserver.exe) is built by run_session.ps1 at every start (a second when nothing changed).
  re\scripts\setup_all.py (H3) makes all of the above from Dawngate.zip in one command; play.ps1 only checks and refreshes.

  Usage (in a PowerShell window, from anywhere):
    powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 [-NoCapture] [-Name <logs folder>] [-HostOptions 'k=v;...'] [-KeepAlive]
        [-ShaperSelect]
    powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 -SetupOnly      # only check (and build) the setup
    powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 -Server <S> [-AllowHost <H>[,<H2>]] [-NoCapture] [-KeepAlive] [-SetupOnly]
                                                                               # P2: the server is another machine
  Check mode (non-interactive, for the autonomous loop and after changes to the start-up path): the same code path with
  a scripted login and B8's clicks to PRACTICE DAWNGATE (posted window messages only), the window kept off the desktop
  (-Background), a time limit (-Seconds, default 150) and a new account each time; it passes when the client reaches
  kGameStateDungeon and the session is clean (egress 0 flagged, cleanup clean). It never uses the cursor or keyboard.
    powershell -ExecutionPolicy Bypass -File re\scripts\play.ps1 -Check [-Seconds 150]
  -Seconds N also limits a manual session (the game is then closed after about N s); -HostOptions is passed to the match
  host (dgsim.ini [host] keys, e.g. 'test_wave=20'). -KeepAlive (B12, opt-in) posts a harmless key-up
  (VK_F24) to the game every 60 s, so the client's idle kick (5 minutes without input in a match) never fires, e.g. while
  you watch a match without playing; it only posts a window message, your keyboard and mouse stay untouched.
  -ShaperSelect (D3-live) starts dgserver with --shaper-select: before the match you pick your shaper, role and skin
  from your account's shapers (the bots pick theirs); without it you play Fenmore as before.
  -Server <S> (P2) plays on a server on another machine of your network instead: none of the checks above, no local
  server and no match host; it runs re\scripts\play_lan.ps1 -Server <S> with -AllowHost, -Check, -Seconds, -Name,
  -NoCapture, -KeepAlive and -SetupOnly passed on (the lan copy setup_all.py --player made, the capture and the egress check with
  egress_report.py --allow-host <S>, Ctrl+C and the game's end as here; docs/development/running-from-source.md *Playing on a server on
  another PC*). -HostOptions and -ShaperSelect are the server operator's then (dgserver's --gms-options,
  --shaper-select). -AllowHost (only with -Server): the operator's match-host machines (agent mode), which the egress
  check then allows besides <S>.
  Exit code: 0 ok; 1 egress flagged (stop and read egress.txt); 2 setup missing or the session failed;
             3 (-Check) the session was clean but the client didn't reach the game.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [switch]$Check,
    [int]$Seconds = 0,
    [string]$Name,
    [switch]$NoCapture,
    [string]$HostOptions,
    [switch]$KeepAlive,
    [switch]$ShaperSelect,
    [switch]$SetupOnly,
    [string]$Server,
    [string]$AllowHost
)
$ErrorActionPreference = 'Stop'
if ($AllowHost -and -not $PSBoundParameters.ContainsKey('Server')) { Write-Host '-AllowHost is for -Server (a server on another machine)' -ForegroundColor Red; exit 2 }
if ($PSBoundParameters.ContainsKey('Server')) {
    # P2: a server on another machine: the lan copy through play_lan.ps1 (no local server, host or setup checks here)
    if ($HostOptions -or $ShaperSelect) {
        Write-Host '-HostOptions and -ShaperSelect are the server operator''s with -Server (dgserver --gms-options, --shaper-select)' -ForegroundColor Red
        exit 2
    }
    $fwd = @{ Server = $Server }
    foreach ($k in @('AllowHost', 'Check', 'Seconds', 'Name', 'NoCapture', 'KeepAlive', 'SetupOnly')) { if ($PSBoundParameters.ContainsKey($k)) { $fwd[$k] = $PSBoundParameters[$k] } }
    & (Join-Path $PSScriptRoot 'play_lan.ps1') @fwd
    exit $LASTEXITCODE
}
. (Join-Path $PSScriptRoot 'gms_stale.ps1')  # G5: Get-GmsBuildStale, the gms\ build check
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$Python = Join-Path $RepoRoot '.venv\Scripts\python.exe'
$Pristine = Join-Path $RepoRoot 'client\ia_Dawngate\Dawngate'
$ClientDir = Join-Path $RepoRoot 'client\run_854\Dawngate'
$HostCopy = Join-Path $RepoRoot 'client\run_854_server\Dawngate'
$Certs = Join-Path $RepoRoot 'server\certs'
$GmsBuild = Join-Path $RepoRoot 'gms\build'
$SimDll = Join-Path $GmsBuild 'Release\dgsim.dll'
$Wireshark = 'C:\Program Files\Wireshark'
# B8's clicks after the login (fractions of the client area; docs/BLAZE.md *PLAY to the GMS address (B8)*): the news
# overlay's forward arrow twice, its OK button, PLAY, PRACTICE DAWNGATE
$CheckActions = '24:0.5555,0.8514;26:0.5555,0.8514;28:0.8016,0.8486;32:0.4984,0.0833;36:0.1109,0.4722;40:0.4055,0.8236'

if ($Check -and $NoCapture) { Write-Host '-Check always runs with the capture and the egress check' -ForegroundColor Red; exit 2 }

$missing = New-Object System.Collections.Generic.List[string]
function Ok([string]$m) { Write-Host "  ok       $m" -ForegroundColor Green }
function Info([string]$m) { Write-Host "  note     $m" -ForegroundColor Gray }
function Missing([string]$what, [string[]]$commands) {
    $missing.Add($what)
    Write-Host "  MISSING  $what" -ForegroundColor Red
    foreach ($c in $commands) { Write-Host "             $c" -ForegroundColor Yellow }
}
function Test-Patched([string]$dir) {
    # what run_session.ps1 demands: exe and both config packages differ from their .orig (patch_client.py, patch_config.py)
    foreach ($f in @('game\Dawngate.exe', 'game\Data\Config.package', 'game\Data\Config_Ship.package')) {
        $live = Join-Path $dir $f
        if (-not (Test-Path $live) -or -not (Test-Path "$live.orig")) { return $false }
        if ((Get-FileHash $live).Hash -eq (Get-FileHash "$live.orig").Hash) { return $false }
    }
    $true
}
function Test-CaInExe([string]$exe) {
    # the client must carry server\certs' CA modulus (patch_client.py); new certificates need a new patch
    $code = "import sys; d = open(sys.argv[1], 'rb').read(); m = open(sys.argv[2], 'rb').read(); sys.exit(0 if len(m) == 128 and m in d else 1)"
    & $Python -I -c $code $exe (Join-Path $Certs 'ca_modulus.bin')
    $LASTEXITCODE -eq 0
}
function Get-StaleGmsSource {
    # the rule of run_session.ps1's Assert-HostDll (G5, re\scripts\gms_stale.ps1): no source in gms\host, gms\engine, gms\core,
    # gms\script (or gms\CMakeLists.txt, gms\cmake) newer than dgsim's last successful build (its build stamp)
    Get-GmsBuildStale $SimDll (Join-Path $RepoRoot 'gms')
}
function Find-CMake {
    $cache = Join-Path $GmsBuild 'CMakeCache.txt'
    if (Test-Path $cache) {
        $line = @(Get-Content $cache | Where-Object { $_ -match '^CMAKE_COMMAND:INTERNAL=(.+)$' } | Select-Object -First 1)
        if ($line.Count -and $line[0] -match '^CMAKE_COMMAND:INTERNAL=(.+)$' -and (Test-Path $Matches[1])) { return ($Matches[1] -replace '/', '\') }
    }
    $vs = @(Get-ChildItem 'C:\Program Files\Microsoft Visual Studio\2022\*\Common7\IDE\CommonExtensions\Microsoft\CMake\CMake\bin\cmake.exe' -ErrorAction SilentlyContinue)
    if ($vs.Count) { return $vs[0].FullName }
    $onPath = Get-Command cmake.exe -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    $null
}
function Get-LastLogLine([string]$log) {
    # the log's last non-empty line, read beside the writer (it holds the file open)
    try {
        $fs = [IO.File]::Open($log, 'Open', 'Read', 'ReadWrite')
        try {
            $fs.Seek([Math]::Max(0, $fs.Length - 2048), 'Begin') | Out-Null
            $text = (New-Object IO.StreamReader($fs)).ReadToEnd()
        } finally { $fs.Dispose() }
        $last = $text -split "`r?`n" | Where-Object { $_.Trim() } | Select-Object -Last 1
        if ($last) { $last.Trim() } else { '' }
    } catch { '' }
}
function Invoke-Logged([string]$exe, [string[]]$argList, [string]$log) {
    # the output goes to $log; meanwhile one line ticks with the time taken and the log's last line, so a long build
    # doesn't look like a hang (a redirected output gets a line every 15 s instead)
    $p = Start-Process -FilePath $exe -ArgumentList $argList -WorkingDirectory $RepoRoot -NoNewWindow -PassThru `
        -RedirectStandardOutput $log -RedirectStandardError "$log.err"
    $null = $p.Handle   # keeps the exit code readable after the process has ended
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $live = -not [Console]::IsOutputRedirected
    $width = if ($live) { [Math]::Max(40, [Console]::WindowWidth - 1) } else { 0 }
    $next = 15
    while (-not $p.WaitForExit(1000)) {
        $t = $sw.Elapsed
        $line = '           {0}:{1:00}  {2}' -f [int][Math]::Floor($t.TotalMinutes), $t.Seconds, (Get-LastLogLine $log)
        if ($live) { Write-Host ("`r" + $line.PadRight($width).Substring(0, $width)) -NoNewline -ForegroundColor Gray }
        elseif ($t.TotalSeconds -ge $next) { Write-Host $line; $next += 15 }
    }
    $p.WaitForExit()
    if ($live) { Write-Host ("`r" + ''.PadRight($width) + "`r") -NoNewline }
    Write-Host ('           done in {0:0} s' -f $sw.Elapsed.TotalSeconds) -ForegroundColor Gray
    $p.ExitCode
}
function Update-BotProfiles([string]$copy, [string]$what) {
    # PLAYPROF: the copy's added game\Data\zz_BotProfiles.snap (the project's own bot profiles: the bots' shaper, role and
    # name), byte for byte what bot_profiles.py makes now from the copy's own AssetData.snap; a newer profile table makes
    # an older archive stale, so it is rewritten here (setup_lane.py's bot_profiles_step, setup_all.py's step too)
    $data = Join-Path $copy 'game\Data'
    $script = Join-Path $PSScriptRoot 'bot_profiles.py'
    $label = "bot profiles ($what, game\Data\zz_BotProfiles.snap)"
    $show = $data.Replace("$RepoRoot\", '')
    $fix = ".venv\Scripts\python -I re\scripts\bot_profiles.py $show"
    if (-not (Test-Path (Join-Path $data 'AssetData.snap'))) {
        Missing "$label`: $show\AssetData.snap is missing (an incomplete copy)" @('make the copy again: .venv\Scripts\python -I re\scripts\setup_all.py', "then: $fix")
        return
    }
    $out = @(& $Python -I $script $data --check)
    if ($LASTEXITCODE -eq 0) { Ok $label; return }
    Write-Host "  install  $label ($(if ("$($out | Select-Object -Last 1)" -match '^missing:') { 'not there' } else { 'older profiles' }))" -ForegroundColor Yellow
    & $Python -I $script $data | ForEach-Object { Write-Host "           $_" -ForegroundColor Gray }
    $null = & $Python -I $script $data --check
    if ($LASTEXITCODE -eq 0) { Ok $label }
    else { Missing "installing the $label failed (is the game running from the $what?)" @($fix) }
}

Push-Location $RepoRoot
try {
    Write-Host "Dawngate: checking the setup in $RepoRoot" -ForegroundColor Cyan

    # ---- tools
    if (Test-Path $Python) { Ok '.venv (Python)' }
    else { Missing 'the Python environment .venv' @('python -m venv .venv', '.venv\Scripts\pip install pefile cryptography') }
    $go = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($go -or (Test-Path 'C:\Program Files\Go\bin\go.exe')) { Ok 'Go (builds the server)' }
    else { Missing 'Go 1.24 (the server is built at every start)' @('install it from https://go.dev/dl/ (docs/development/building.md)') }
    if ($NoCapture) { Info 'no capture and no egress check (-NoCapture)' }
    elseif ((Test-Path "$Wireshark\dumpcap.exe") -and (Test-Path "$Wireshark\tshark.exe")) { Ok 'Wireshark (capture and egress check)' }
    else { Missing "Wireshark in $Wireshark with Npcap (loopback capture on)" @('see docs/development/building.md *Tools*; or play without the capture: play.ps1 -NoCapture') }

    # ---- certificates (server\certs; the client copy carries their CA)
    $certFiles = @('server_cert.pem', 'server_key.pem', 'ca_modulus.bin')
    if (@($certFiles | Where-Object { -not (Test-Path (Join-Path $Certs $_)) }).Count) {
        Missing 'the server certificates in server\certs' @('.venv\Scripts\python -I server\tools\make_certs.py server\certs 127.0.0.1',
            'then patch the client copy again (next item): new certificates need a new patch')
    } else { Ok 'certificates (server\certs)' }

    # ---- the client copy
    $patchCmds = @('.venv\Scripts\python -I re\scripts\patch_client.py client\run_854\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1',
                   '.venv\Scripts\python -I re\scripts\patch_config.py client\run_854\Dawngate\game\Data')
    if (-not (Test-Path (Join-Path $ClientDir 'game\Dawngate.exe'))) {
        $copy = @('robocopy client\ia_Dawngate\Dawngate client\run_854\Dawngate /E /COPY:DAT /DCOPY:T') + $patchCmds
        if (-not (Test-Path (Join-Path $Pristine 'game\Dawngate.exe'))) { $copy = @('python -I re\scripts\extract_client.py Dawngate.zip client\ia_Dawngate   (Dawngate.zip from https://archive.org/details/Dawngate)') + $copy }
        Missing 'the client working copy client\run_854\Dawngate' $copy
    } elseif (-not (Test-Patched $ClientDir)) {
        Missing 'the client copy is not patched (it would contact EA)' $patchCmds
    } elseif ((Test-Path $Python) -and (Test-Path (Join-Path $Certs 'ca_modulus.bin')) -and -not (Test-CaInExe (Join-Path $ClientDir 'game\Dawngate.exe'))) {
        Missing 'the client copy does not carry server\certs'' CA (new certificates?)' @($patchCmds[0])
    } else { Ok 'client copy client\run_854 (patched, our CA)' }
    # B6-live: without our host in Config_Pack.package's WebkitAllowedDomains the main menu's home page (centre panel)
    # stays empty; not needed to play, so only a note
    $pack = Join-Path $ClientDir 'game\Data\Config_Pack.package'
    if ((Test-Path $pack) -and (-not (Test-Path "$pack.orig") -or (Get-FileHash $pack).Hash -eq (Get-FileHash "$pack.orig").Hash)) {
        Info "Config_Pack.package is not patched (B6), so the main menu's home page stays empty: $($patchCmds[1])"
    }
    # BOTBOX: CREATE GAME's "Fill open seats with bots" box is the added game\Data\zz_MatchOptions.snap, byte for byte what
    # setup_lane.py's match_options_step makes from the copy's own Battlegrounds.snap (setup_all.py's step too)
    if ((Test-Path $Python) -and (Test-Path (Join-Path $ClientDir 'game\Data\Battlegrounds.snap'))) {
        $data = Join-Path $ClientDir 'game\Data'
        $boxCode = "import sys; sys.path.insert(0, sys.argv[1]); import setup_lane; m = []; setup_lane.match_options_step(sys.argv[2], sys.argv[3] == 'check', m); sys.exit(1 if m else 0)"
        & $Python -I -c $boxCode $PSScriptRoot $data 'check'
        if ($LASTEXITCODE -eq 0) { Ok 'bot check box (game\Data\zz_MatchOptions.snap)' }
        else {
            Write-Host '  install  the CREATE GAME dialog''s bot check box (game\Data\zz_MatchOptions.snap)' -ForegroundColor Yellow
            & $Python -I -c $boxCode $PSScriptRoot $data 'install' | ForEach-Object { Write-Host "           $_" -ForegroundColor Gray }
            & $Python -I -c $boxCode $PSScriptRoot $data 'check'
            if ($LASTEXITCODE -eq 0) { Ok 'bot check box (game\Data\zz_MatchOptions.snap)' }
            else { Missing 'installing the bot check box failed (is the game running from client\run_854?)' @(
                '.venv\Scripts\python -I re\scripts\patch_login_movie.py --spec matchoptions client\run_854\Dawngate\game\Data\Battlegrounds.snap client\run_854\Dawngate\game\Data\zz_MatchOptions.snap') }
        }
    }
    # PLAYPROF: the client shows the bots' shaper, role and name from these profiles
    if ((Test-Path $Python) -and (Test-Path (Join-Path $ClientDir 'game\Dawngate.exe'))) { Update-BotProfiles $ClientDir 'client copy' }
    # LBMIN: the client texts (game\Data\Locale\en-us\AA_Server.package: the login texts and links, the Leaderboard's
    # rule), byte for byte what patch_locale.py makes now for 127.0.0.1 (setup_all.py's step too); a copy whose links
    # go to a website (setup_all.py --web-domain) is left to setup_all.py, which knows the domain
    $locData = Join-Path $ClientDir 'game\Data'
    if ((Test-Path $Python) -and (Test-Path (Join-Path $locData 'Locale\en-us\Locale.package'))) {
        $locLabel = 'client texts (game\Data\Locale\en-us\AA_Server.package)'
        $locScript = Join-Path $PSScriptRoot 'patch_locale.py'
        $locAdded = Join-Path $locData 'Locale\en-us\AA_Server.package'
        $null = & $Python -I $locScript $locData --host 127.0.0.1 --check
        if ($LASTEXITCODE -eq 0) { Ok $locLabel }
        elseif ((Test-Path $locAdded) -and -not [Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($locAdded)).Contains('http://127.0.0.1:18080/dawngate/terms')) {
            Missing "$locLabel are older (their links go to a website)" @('.venv\Scripts\python -I re\scripts\setup_all.py --web-domain <the domain>')
        } else {
            Write-Host "  install  $locLabel (older texts)" -ForegroundColor Yellow
            & $Python -I $locScript $locData --host 127.0.0.1 | ForEach-Object { Write-Host "           $_" -ForegroundColor Gray }
            $null = & $Python -I $locScript $locData --host 127.0.0.1 --check
            if ($LASTEXITCODE -eq 0) { Ok $locLabel }
            else { Missing "installing the $locLabel failed (is the game running from client\run_854?)" @(
                '.venv\Scripts\python -I re\scripts\patch_locale.py client\run_854\Dawngate\game\Data --host 127.0.0.1') }
        }
    }

    # ---- the host copy and the match-host build
    $hostSetup = '.venv\Scripts\python -I re\scripts\setup_server_copy.py client\run_854\Dawngate client\run_854_server\Dawngate gms\build\Release\dgsim.dll'
    $hostReady = $false
    if (-not (Test-Path (Join-Path $HostCopy 'game\Dawngate.exe'))) {
        Missing 'the match-host copy client\run_854_server\Dawngate (a copy of client\run_854, about 1.5 GB, with the harness)' @(
            'build gms\ first (next item), then:', $hostSetup)
    } elseif (-not (Test-Patched $HostCopy)) {
        Missing 'the host copy is not patched' @('.venv\Scripts\python -I re\scripts\patch_client.py client\run_854_server\Dawngate\game\Dawngate.exe server\certs\ca_modulus.bin 127.0.0.1',
            '.venv\Scripts\python -I re\scripts\patch_config.py client\run_854_server\Dawngate\game\Data')
    } else { $hostReady = $true }
    # PLAYPROF: the match host loads the bots' profiles (BotClass assets) from the host copy's own archive
    if ((Test-Path $Python) -and (Test-Path (Join-Path $HostCopy 'game\Dawngate.exe'))) { Update-BotProfiles $HostCopy 'host copy' }

    $stale = Get-StaleGmsSource
    if ($stale) {
        $cmake = Find-CMake
        if ((Test-Path (Join-Path $GmsBuild 'CMakeCache.txt')) -and $cmake) {
            $log = Join-Path $GmsBuild 'play_build.log'
            Write-Host "  build    $stale; building gms\ (Release; the first build takes a few minutes; log $log)" -ForegroundColor Yellow
            $code = Invoke-Logged $cmake @('--build', "`"$GmsBuild`"", '--config', 'Release') $log
            $stale = Get-StaleGmsSource
            if ($code -ne 0 -or $stale) { Missing "gms\ did not build (exit $code$(if ($stale) { "; $stale" })): read $log" @("`"$cmake`" --build gms\build --config Release") }
            else { Ok 'gms\ built (dgsim.dll is current)' }
        } else {
            $c = if ($cmake) { "`"$cmake`"" } else { 'cmake' }
            Missing "gms\ is not built ($stale) and gms\build is not configured" @(
                "$c -S gms -B gms\build -G `"Visual Studio 17 2022`" -A Win32   (needs RakNet in tools\raknet\RakNet: docs/development/building.md)",
                "$c --build gms\build --config Release")
        }
    } else { Ok 'gms\ build (dgsim.dll is current)' }

    if ($hostReady -and -not (Get-StaleGmsSource)) {
        $loader = Join-Path $HostCopy 'game\PhysXLoader.dll'
        if (-not (Test-Path (Join-Path $HostCopy 'game\PhysXLoader_orig.dll')) -or (Get-FileHash $SimDll).Hash -ne (Get-FileHash $loader).Hash) {
            Write-Host '  install  the current dgsim.dll into the host copy (setup_server_copy.py)' -ForegroundColor Yellow
            & $Python -I (Join-Path $RepoRoot 're\scripts\setup_server_copy.py') $ClientDir $HostCopy $SimDll | ForEach-Object { Write-Host "           $_" -ForegroundColor Gray }
            if ($LASTEXITCODE -ne 0 -or (Get-FileHash $SimDll).Hash -ne (Get-FileHash $loader).Hash) { Missing 'installing dgsim.dll into the host copy failed' @($hostSetup) }
            else { Ok 'host copy client\run_854_server (patched, current dgsim.dll installed)' }
        } else { Ok 'host copy client\run_854_server (patched, current dgsim.dll installed)' }
    }

    # ---- one client at a time (the Global\Dawngate mutex); our own leftovers are stopped by run_session.ps1
    $clientRoot = (Join-Path $RepoRoot 'client\').ToLower()
    foreach ($g in @(Get-CimInstance Win32_Process -Filter "Name='Dawngate.exe'")) {
        $path = "$($g.ExecutablePath)"
        if (-not $path.ToLower().StartsWith($clientRoot)) {
            Missing "another Dawngate.exe is running (pid $($g.ProcessId), $(if ($path) { $path } else { 'path unknown' }))" @('close it first: only one Dawngate client can run at a time (the Global\Dawngate mutex)')
        }
    }

    # ---- optional: the player content (owned items, store) for new accounts
    if (Test-Path (Join-Path $RepoRoot 'server\data\content.json')) { Ok 'player content (server\data\content.json)' }
    else { Info 'no server\data\content.json: new accounts own no items (optional; docs/development/building.md *The setup command*)' }

    if ($missing.Count) {
        Write-Host ''
        Write-Host "Setup incomplete ($($missing.Count) item(s) above). Run the yellow commands from $RepoRoot, then start play.ps1 again (docs/development/building.md *The setup command*)." -ForegroundColor Red
        Write-Host "Or let one command do all of it (H3): .venv\Scripts\python -I re\scripts\setup_all.py   (--check only reports)" -ForegroundColor Yellow
        exit 2
    }
    if ($SetupOnly) { Write-Host 'Setup complete.' -ForegroundColor Green; exit 0 }

    # ---- the session (run_session.ps1 -Play: same safety, same summary)
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    if (-not $Name) { $Name = if ($Check) { "play_check_$stamp" } else { "play_$stamp" } }
    $params = @{ Name = $Name; Play = $true; GameDir = $ClientDir; GmsHost = (Split-Path $HostCopy -Parent) }
    if ($HostOptions) { $params.HostOptions = $HostOptions }
    if ($NoCapture) { $params.NoCapture = $true }
    if ($KeepAlive) { $params.KeepAlive = 60 }  # B12: opt-in; run_session.ps1 keeps it off in play mode otherwise
    if ($ShaperSelect) { $params.ServerArgs = '--shaper-select' }  # D3-live: shaper select with the account's content
    if ($Check) {
        if ($Seconds -le 0) { $Seconds = 150 }
        $params.Seconds = $Seconds
        $params.Background = $true
        # a new account each time, so the news overlay shows (B8's clicks expect it); the server logs the credentials
        $params.Login = "$Name@example.com|dawngate1"
        $params.Actions = $CheckActions
        Write-Host ''
        Write-Host "Check mode: scripted login and PRACTICE DAWNGATE, window off the desktop, $Seconds s." -ForegroundColor Cyan
    } else {
        if ($Seconds -gt 0) { $params.Seconds = $Seconds }
        Write-Host ''
        Write-Host 'Starting Dawngate (the window opens after about 5 s):' -ForegroundColor Cyan
        Write-Host '  1. Log in with a login name and a password (8+ characters; the first login with a new name creates the account, later logins need that password).'
        Write-Host '  2. The first time, a news overlay appears: click its right arrow twice, then OK.'
        Write-Host '  3. PLAY, then PRACTICE DAWNGATE: the match host starts and the game loads (about 30 s).'
        if ($ShaperSelect) {
            Write-Host '     Shaper select: click a shaper and its Choose button, then a role and Finalize, then a skin.'
        }
        Write-Host '  To stop: quit the game, or press Ctrl+C here. Cleanup, the egress check and a summary follow.'
        Write-Host '  More in docs\playing.md (what works in a match) and docs\troubleshooting.md.'
        Write-Host ''
    }
    & (Join-Path $PSScriptRoot 'run_session.ps1') @params
    $code = $LASTEXITCODE

    # ---- the short summary: egress, cleanup, where the logs are
    $dir = Join-Path $RepoRoot "re\logs\$Name"
    $sum = @(if (Test-Path (Join-Path $dir 'summary.txt')) { Get-Content (Join-Path $dir 'summary.txt') -Encoding UTF8 })
    function Line([string]$key) { "$($sum | Where-Object { $_ -match "^$key\s*:" } | Select-Object -First 1)" -replace "^$key\s*:\s*", '' }
    $color = if ($code -eq 0) { 'Green' } else { 'Red' }
    Write-Host ''
    Write-Host "==== Dawngate session $Name over: $(if ($code -eq 0) { 'OK' } elseif ($code -eq 1) { 'EGRESS FLAGGED: stop and read egress.txt' } else { 'FAILED (see summary.txt)' })" -ForegroundColor $color
    if (Line 'play') { Write-Host "  session : $(Line 'play')" -ForegroundColor $color }
    Write-Host "  egress  : $(if (Line 'egress') { Line 'egress' } else { 'not run' })" -ForegroundColor $color
    Write-Host "  cleanup : $(Line 'cleanup')" -ForegroundColor $color
    Write-Host "  logs    : $dir" -ForegroundColor $color
    Write-Host '            summary.txt; the client: application_log.txt; the server: stub.log; each match: host\<n>\dgsim.log' -ForegroundColor $color

    if ($Check) {
        $app = Join-Path $dir 'application_log.txt'
        $inGame = (Test-Path $app) -and (Select-String -Path $app -Pattern 'transitioning from \S+ -> kGameStateDungeon' -Quiet)
        $ready = (Line 'host ctl') -match 'gameReady'
        if ($code -eq 0 -and $inGame -and $ready) {
            Write-Host 'CHECK PASSED: login, PRACTICE started the match host (gameReady), the client reached kGameStateDungeon; egress and cleanup clean.' -ForegroundColor Green
            exit 0
        }
        Write-Host "CHECK FAILED: session exit $code; host gameReady: $ready; client in kGameStateDungeon: $inGame." -ForegroundColor Red
        exit $(if ($code -ne 0) { $code } else { 3 })
    }
    exit $code
} finally {
    Pop-Location
}
