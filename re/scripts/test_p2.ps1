<#
  Tests P2: re\scripts\play_lan.ps1 (the player launcher for a server on another machine) and play.ps1 -Server.

  Part 1 (always, about a minute; starts no game, no server, no capture, writes only its scratch folder):
   1a  play_lan.ps1, play.ps1 and this file parse and are ASCII.
   1b  play_lan.ps1's rules, taken from its syntax tree (nothing else of it runs): Test-LanAddress, ConvertFrom-ShowLines,
       Get-CopyProblems, Get-CopyKind, Get-LiveLock and Get-LockRefusal (scratch lock files naming a live helper
       process, a stale one), Get-FolderLane, Get-MutexHolders, Test-ConfigNames, Get-PlayEnd, Get-SocketsLine,
       Get-AppLogLine, Get-AppLogSection, Get-CheckResult (-Check's verdict, P2-live). The session itself (the monitor,
       Ctrl+C, the capture's stop, the egress check, the summary) needs the game: Part 2.
   1c  play_lan.ps1 -DryRun / -SetupOnly in child processes on a scratch state root (-StateRoot): two lan copies are made
       there from client\ia_Dawngate's Dawngate.exe and config packages (read only, about 18 MB) and patched by
       patch_client.py and patch_config.py for the made-up server 192.168.1.20 with a random CA modulus: setup_all.py
       --player's copy client\run_854_lan (wildcard binds, DawngateData) and the lan test copy client\run_854_l
       (--client-bind 192.168.1.20, --data-suffix L). Cases: the server from -Server and from the copy, a wrong server,
       refused addresses, -NoCapture, -AllowHost, -Check's monitor arguments, each lock (free, live, stale, the user
       playing), an unpatched and a missing copy, an existing session folder. Without client\ia_Dawngate (or with
       -NoPristine) 1c runs only the cases that need no copy.
   1d  play.ps1 -Server passes its arguments on (-Name, -Check, -NoCapture, -AllowHost; refusals that need no copy, so
       the real copies are never read), and refuses -ShaperSelect, -HostOptions with it and -AllowHost without it.

  Part 2 (-Live -Lane b|c|d [-Address auto|<IPv4>] [-Name p2_live1] [-Seconds 150] [-PlanOnly]): the main-track live
  check of roadmap P2 (A5b's step 2 with the player launcher): this PC as the server on its own LAN address and as the
  player. It needs a free test lane X (its lock, its loopback address for the hosts' control port, its host copy
  client\run_854_server_X with this checkout's dgsim.dll: setup_lane.py X), the lan test copy client\run_854_l and
  server\certs\lan for the address (setup_lane.py lan --address auto), and no other lan session. It takes lane X's lock
  and the lan lock re\logs\session_lan.lock (mode test; a run_session.ps1 -Lan session would stop a dgserver bound to
  the LAN address as its leftover), builds server\bin\lane_X\dgserver.exe in this checkout, starts it on the LAN address
  (dgserver --bind <A> --public-host <A> --certs server\certs\lan --gms-dir client\run_854_server_X --gms-control-bind
  127.0.0.N, a fresh accounts folder, its log in re\logs\<Name>_server; --bind, not --host: the lanes' leftover rules
  read --bind, p2live_run1b), then runs play_lan.ps1 -Check -Server <A> -GameDir client\run_854_l\Dawngate
  -Name <Name> -Seconds <Seconds> (the launcher takes re\logs\session_lan.lock), then stops that dgserver (and with it
  its hosts) and checks that its ports are free; a dgserver that ended during the player's session fails the check
  (exit 2). -PlanOnly runs the precondition checks and prints the plan without
  starting or writing anything. It stops nothing it didn't start (a host of its own dgserver left after the server's
  end is the one exception). Windows Firewall may ask about this checkout's lane_X\dgserver.exe the first time it
  listens on the LAN address: the user's call; same-machine traffic passes either way (A5b).

  Usage:
    powershell -ExecutionPolicy Bypass -File re\scripts\test_p2.ps1 [-NoPristine] [-Keep] [-Scratch <empty dir>]
    powershell -ExecutionPolicy Bypass -File re\scripts\test_p2.ps1 -Live -Lane c [-Address auto] [-Name p2_live1] [-Seconds 150] [-PlanOnly]
  -Scratch defaults to a new folder under %TEMP% (removed at the end unless -Keep).
  Exit code: Part 1: 0 all cases passed, 1 a case failed, 2 setup problem. Part 2: play_lan.ps1's exit code (0 passed, 1
  egress flagged, 3 clean but not in the game), 2 a precondition or the server failed.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [switch]$Live,
    [ValidateSet('', 'b', 'c', 'd')][string]$Lane = '',
    [string]$Address = 'auto',
    [string]$Name = 'p2_live1',
    [int]$Seconds = 150,
    [switch]$PlanOnly,
    [switch]$NoPristine,
    [switch]$Keep,
    [string]$Scratch
)
$ErrorActionPreference = 'Stop'
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$MainRoot = $RepoRoot
if (-not (Test-Path (Join-Path $RepoRoot 'client'))) {
    try {
        $common = "$(& git -C $RepoRoot rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
        if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'client')) { $MainRoot = $cand } }
    } catch { }
}
$Python = Join-Path $MainRoot '.venv\Scripts\python.exe'
$PlayLan = Join-Path $PSScriptRoot 'play_lan.ps1'
$PlayPs = Join-Path $PSScriptRoot 'play.ps1'

function Get-FunctionText([string]$file, [string[]]$names) {
    # the source text of the named functions of a script (parsed, not run; test_g5.ps1's)
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($file, [ref]$null, [ref]$null)
    foreach ($n in $names) {
        $fn = @($ast.FindAll({ param($a) $a -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq $n }, $true))
        if (-not $fn.Count) { throw "no function $n in $file" }
        $fn[0].Extent.Text
    }
}
function Get-ProcessStartStamp([int]$id) { try { (Get-Process -Id $id -ErrorAction Stop).StartTime.ToString('o') } catch { $null } }

# =====================================================================================================================
# Part 2: the live check (main track)
# =====================================================================================================================
if ($Live) {
    . (Join-Path $PSScriptRoot 'gms_stale.ps1')
    if (-not $Lane) { Write-Host '-Live needs -Lane b|c|d (a free test lane: its lock, its control address, its host copy)' -ForegroundColor Red; exit 2 }
    if ($Name -notmatch '^[\w.-]+$') { Write-Host "invalid -Name '$Name'" -ForegroundColor Red; exit 2 }
    $n = @{ b = 2; c = 3; d = 4 }[$Lane]
    $laneAddr = "127.0.0.$n"
    if ($Address -eq 'auto') {
        $route = Get-NetRoute -DestinationPrefix 0.0.0.0/0 -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1
        $Address = if ($route) { "$((Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $route.ifIndex -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress)" } else { '' }
    }
    $hostCopy = Join-Path $MainRoot "client\run_854_server_$Lane\Dawngate"
    $lanCopy = Join-Path $MainRoot 'client\run_854_l\Dawngate'
    $certs = Join-Path $MainRoot 'server\certs\lan'
    $content = Join-Path $MainRoot 'server\data\content.json'
    $serverExe = Join-Path $RepoRoot "server\bin\lane_$Lane\dgserver.exe"
    $simDll = Join-Path $RepoRoot 'gms\build\Release\dgsim.dll'
    $laneLock = Join-Path $MainRoot "re\logs\session_$Lane.lock"
    $srvDir = Join-Path $MainRoot "re\logs\${Name}_server"
    $sessDir = Join-Path $MainRoot "re\logs\$Name"
    $tcpPorts = @(42127, 10041, 17502, 18080, 9988)
    $udpPorts = @(17502, 10042)
    $stubSeconds = 2 * $Seconds + 600
    $bad = New-Object System.Collections.Generic.List[string]
    function Need([bool]$ok, [string]$what, [string]$fix) {
        if ($ok) { Write-Host "  ok       $what" -ForegroundColor Green }
        else { $bad.Add($what); Write-Host "  MISSING  $what$(if ($fix) { " -> $fix" })" -ForegroundColor Red }
    }
    function Get-LiveLockText([string]$file) {
        if (-not (Test-Path $file)) { return $null }
        $text = "$(Get-Content $file -Raw -ErrorAction SilentlyContinue)".Trim()
        if ($text -match 'pid=(\d+) start=(\S+)' -and [int]$Matches[1] -ne $PID -and (Get-ProcessStartStamp ([int]$Matches[1])) -eq $Matches[2]) { return $text }
        $null
    }
    Write-Host "P2 live check: lane $Lane ($laneAddr), this PC's LAN address '$Address', session $Name" -ForegroundColor Cyan
    $ip = $null
    $addrOk = [Net.IPAddress]::TryParse($Address, [ref]$ip) -and $Address -match '^\d+\.\d+\.\d+\.\d+$' -and @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $Address -ErrorAction SilentlyContinue).Count -gt 0
    Need $addrOk "the server address $Address is this PC's own IPv4 address" '-Address auto or this PC''s LAN address'
    $t = Get-LiveLockText $laneLock
    Need (-not $t) "lane $Lane is free ($laneLock$(if ($t) { ": $t" }))" 'wait, or another lane'
    $t = Get-LiveLockText (Join-Path $MainRoot 're\logs\session.lock')
    Need (-not ($t -match 'mode=play')) 'the user is not playing (lane a''s lock)' 'wait until the game is closed'
    $t = Get-LiveLockText (Join-Path $MainRoot 're\logs\session_lan.lock')
    Need (-not $t) "no other lan session$(if ($t) { ": $t" })" 'wait'
    Need (Test-Path (Join-Path $lanCopy 'game\Dawngate.exe')) "the lan test copy $lanCopy" ".venv\Scripts\python -I re\scripts\setup_lane.py lan --address $Address"
    Need (Test-Path (Join-Path $certs 'server_cert.pem')) "the LAN certificates $certs" ".venv\Scripts\python -I re\scripts\setup_lane.py lan --address $Address"
    $dllOk = (Test-Path $simDll) -and (Test-Path (Join-Path $hostCopy 'game\PhysXLoader_orig.dll')) -and -not (Get-GmsBuildStale $simDll (Join-Path $RepoRoot 'gms')) -and
             (Get-FileHash $simDll).Hash -eq (Get-FileHash (Join-Path $hostCopy 'game\PhysXLoader.dll') -ErrorAction SilentlyContinue).Hash
    Need $dllOk "lane $Lane's host copy $hostCopy runs this checkout's current dgsim.dll" "cmake --build gms\build --config Release; .venv\Scripts\python -I re\scripts\setup_lane.py $Lane"
    $held = @()
    if ($addrOk) {
        $held += @(Get-NetTCPConnection -State Listen -LocalPort $tcpPorts -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in @($Address, '0.0.0.0') } | ForEach-Object { "TCP $($_.LocalAddress):$($_.LocalPort) pid $($_.OwningProcess)" })
        $held += @(Get-NetUDPEndpoint -LocalPort $udpPorts -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in @($Address, '0.0.0.0') } | ForEach-Object { "UDP $($_.LocalAddress):$($_.LocalPort) pid $($_.OwningProcess)" })
    }
    $held += @(Get-NetTCPConnection -State Listen -LocalPort 10040 -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -eq $laneAddr } | ForEach-Object { "TCP $laneAddr`:10040 pid $($_.OwningProcess)" })
    Need (-not $held.Count) "the server's ports are free on $Address and $laneAddr`:10040$(if ($held.Count) { ": $($held -join ', ')" })" 'a leftover server? (only its own run stops it)'
    Need (-not (Test-Path $srvDir) -and -not (Test-Path $sessDir)) "the session folders $sessDir and ${srvDir} are new" 'pick another -Name'
    # --bind/--public-host as run_session.ps1 -Lan passes them, not dgserver's --host shorthand: the lanes' leftover
    # rules read --bind (an older run_session.ps1 doesn't know --host and takes such a server for lane a's: lane a's
    # cleanup stopped p2live_run1b's server in the middle of its match)
    $srvArgs = @('--bind', $Address, '--public-host', $Address, '--certs', "`"$certs`"", '--gms-dir', "`"$hostCopy`"", '--gms-args=-multipleInstances',
        '--gms-control-bind', $laneAddr, '--gms-data', "`"$(Join-Path $srvDir 'host')`"", '--data', "`"$(Join-Path $srvDir 'data')`"",
        '--log', "`"$srvDir`"", '--seconds', $stubSeconds)
    if (Test-Path $content) { $srvArgs += @('--content', "`"$content`"") }
    $launcher = "play_lan.ps1 -Check -Server $Address -GameDir $lanCopy -Name $Name -Seconds $Seconds"
    Write-Host ''
    Write-Host 'Plan:' -ForegroundColor Cyan
    Write-Host "  1. lock     $laneLock and re\logs\session_lan.lock (mode test, name $Name)"
    Write-Host "  2. build    go build -C $(Join-Path $RepoRoot 'server') -o bin\lane_$Lane\dgserver.exe .\cmd\dgserver"
    Write-Host "  3. server   $serverExe $($srvArgs -join ' ')"
    Write-Host "  4. player   $launcher   (its own lock re\logs\session_lan.lock, capture, egress --allow-host $Address)"
    Write-Host '  5. end      the host ends with the client (abortGame), then this dgserver is stopped (its job ends any host), ports checked'
    if ($bad.Count) { Write-Host "$($bad.Count) precondition(s) missing." -ForegroundColor Red; exit 2 }
    if ($PlanOnly) { Write-Host 'Preconditions met (-PlanOnly: nothing started).' -ForegroundColor Green; exit 0 }

    $lockLine = "pid=$PID start=$(Get-ProcessStartStamp $PID) name=$Name mode=test lane=$Lane since=$(Get-Date -Format s) lan=$Address"
    Set-Content -Path $laneLock -Encoding ASCII -Value $lockLine
    # the lan lock too, for the whole check: a run_session.ps1 -Lan session would take this dgserver (bound to the LAN
    # address) for its leftover. play_lan.ps1 runs in this process, so it doesn't count this lock as another session's;
    # it rewrites and removes it, and it is written again for the server's end.
    $lanLockFile = Join-Path $MainRoot 're\logs\session_lan.lock'
    Set-Content -Path $lanLockFile -Encoding ASCII -Value $lockLine
    New-Item -ItemType Directory -Force $srvDir, (Join-Path $srvDir 'data') | Out-Null
    Set-Content -Path (Join-Path $srvDir 'lane.txt') -Value $Lane -Encoding ASCII
    $srv = $null
    $code = 2
    $notes = @()
    try {
        New-Item -ItemType Directory -Force (Split-Path $serverExe -Parent) | Out-Null
        $go = if (Get-Command go.exe -ErrorAction SilentlyContinue) { 'go.exe' } else { 'C:\Program Files\Go\bin\go.exe' }
        $b = Start-Process -FilePath $go -ArgumentList @('build', '-C', "`"$(Join-Path $RepoRoot 'server')`"", '-o', "bin\lane_$Lane\dgserver.exe", '.\cmd\dgserver') `
            -NoNewWindow -Wait -PassThru -RedirectStandardOutput (Join-Path $srvDir 'build.out') -RedirectStandardError (Join-Path $srvDir 'build.err')
        if ($b.ExitCode -ne 0) { throw "server build failed (exit $($b.ExitCode)): $(Get-Content (Join-Path $srvDir 'build.err') -Raw)" }
        $srv = Start-Process -FilePath $serverExe -ArgumentList $srvArgs -WorkingDirectory $RepoRoot -NoNewWindow -PassThru `
            -RedirectStandardOutput (Join-Path $srvDir 'stub.out') -RedirectStandardError (Join-Path $srvDir 'stub.err')
        $null = $srv.Handle
        $deadline = (Get-Date).AddSeconds(20)
        while ($true) {
            Start-Sleep -Milliseconds 300
            $up = @(Get-NetTCPConnection -State Listen -LocalPort $tcpPorts -OwningProcess $srv.Id -ErrorAction SilentlyContinue | ForEach-Object { $_.LocalPort } | Sort-Object -Unique)
            if ($up.Count -eq $tcpPorts.Count) { break }
            if ($srv.HasExited) { throw "dgserver exited during startup (code $($srv.ExitCode)): $(Get-Content (Join-Path $srvDir 'stub.err') -Raw)" }
            if ((Get-Date) -gt $deadline) { throw "dgserver not listening on all ports after 20 s (listening: $($up -join ', '))" }
        }
        Write-Host "dgserver pid $($srv.Id) listening on $Address ($($up -join ', ')); starting the player launcher" -ForegroundColor Green
        & $PlayLan -Check -Server $Address -GameDir $lanCopy -Name $Name -Seconds $Seconds
        $code = $LASTEXITCODE
        if (-not (Test-Path $lanLockFile)) { Set-Content -Path $lanLockFile -Encoding ASCII -Value $lockLine }
        # the server must have run to the end: one that ended during the player's session (stopped from outside, a
        # crash) fails the check even when the client got into the game before (p2live_run1b)
        if ($srv.HasExited) {
            $notes += "dgserver ENDED DURING THE CHECK at $('{0:HH:mm:ss}' -f $srv.ExitTime) (exit $($srv.ExitCode)), not by this script: another session's cleanup or a crash (the other lanes' session.log 'stopping dgserver.exe pid=$($srv.Id)', $srvDir\stub.err)"
            if ($code -eq 0) { $code = 2 }
        }
        # the host ends by itself once the client's Blaze connection is gone (abortGame); give it time, then stop dgserver
        $log = Get-ChildItem $srvDir -File | Where-Object Name -match '^\d{8}-\d{6}\.log$' | Select-Object -First 1
        $deadline = (Get-Date).AddSeconds(20)
        while ($log -and (Get-Date) -lt $deadline) {
            $text = Get-Content $log.FullName -Raw -Encoding UTF8
            if ($text -notmatch '\[host\] match \d+: started' -or $text -match '\[host\] match \d+: ended') { break }
            Start-Sleep -Milliseconds 500
        }
    } catch {
        Write-Host "FAILED: $($_.Exception.Message)" -ForegroundColor Red
        $notes += "failure: $($_.Exception.Message)"
        $code = 2
    } finally {
        if ($srv -and -not $srv.HasExited) { Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue; Start-Sleep -Seconds 2 }
        # a host of this server left behind (its job should have ended it): lane X's host copy, our lock held
        $hostPrefix = $hostCopy.ToLower() + '\'
        foreach ($h in @(Get-CimInstance Win32_Process -Filter "Name='Dawngate.exe'" | Where-Object { "$($_.ExecutablePath)".ToLower().StartsWith($hostPrefix) })) {
            $notes += "host pid $($h.ProcessId) still running after the server's end: stopped"
            Stop-Process -Id $h.ProcessId -Force -ErrorAction SilentlyContinue
        }
        foreach ($f in @($laneLock, $lanLockFile)) {
            if ("$(Get-Content $f -Raw -ErrorAction SilentlyContinue)" -match "pid=$PID ") { Remove-Item $f -Force -ErrorAction SilentlyContinue }
        }
    }
    $log = Get-ChildItem $srvDir -File -ErrorAction SilentlyContinue | Where-Object Name -match '^\d{8}-\d{6}\.log$' | Select-Object -First 1
    $lines = @(if ($log) { Get-Content $log.FullName -Encoding UTF8 })
    # (the reply dumps' 'HNET:' lines are TDF members: only the game line that names the GMS address counts)
    $key = @($lines | Where-Object { $_ -match '\[host\] match \d+: (started|ready on port|ended)|\(HNET\)|\[stub\] accounts' } | ForEach-Object { ($_ -replace '^\S+ ', '').Trim() } | Select-Object -First 8)
    $held = @(Get-NetTCPConnection -State Listen -LocalPort $tcpPorts -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -eq $Address })
    $sum = @("==== P2 live check $Name (lane $Lane, server $Address): player launcher exit $code",
        "player   : $sessDir\summary.txt",
        "server   : $srvDir ($(if ($log) { $log.Name } else { 'no log' }))") + @($key | ForEach-Object { "           $_" }) +
        @("ports    : $(if ($held.Count) { "HELD: $(@($held | ForEach-Object { "$($_.LocalPort) pid $($_.OwningProcess)" }) -join ', ')" } else { "free on $Address" })") +
        @($notes | ForEach-Object { "note     : $_" })
    Set-Content -Path (Join-Path $srvDir 'summary.txt') -Value $sum -Encoding UTF8
    Write-Host ''
    foreach ($l in $sum) { Write-Host $l -ForegroundColor $(if ($code -eq 0 -and -not $held.Count) { 'Green' } else { 'Red' }) }
    if ($held.Count -and $code -eq 0) { $code = 2 }
    exit $code
}

# =====================================================================================================================
# Part 1: offline
# =====================================================================================================================
if (-not $Scratch) { $Scratch = Join-Path $env:TEMP ('dg_p2_' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
if ((Test-Path $Scratch) -and @(Get-ChildItem $Scratch -Force).Count) { Write-Host "-Scratch $Scratch is not empty" -ForegroundColor Red; exit 2 }
New-Item -ItemType Directory -Force $Scratch | Out-Null
$Scratch = (Resolve-Path $Scratch).Path
$scratchLower = $Scratch.ToLower()
if ($scratchLower.StartsWith($MainRoot.ToLower() + '\') -or $scratchLower.StartsWith($RepoRoot.ToLower() + '\')) { Write-Host '-Scratch must be outside the checkouts' -ForegroundColor Red; exit 2 }
if (-not (Test-Path $Python)) { Write-Host "no $Python" -ForegroundColor Red; exit 2 }
Write-Host "P2 test in $Scratch" -ForegroundColor Cyan

$script:pass = 0; $script:fail = 0
function Check([string]$case, [string]$what, [bool]$ok, [string]$detail = '') {
    if ($ok) { $script:pass++; Write-Host "  ok    $case  $what" -ForegroundColor Green }
    else { $script:fail++; Write-Host "  FAIL  $case  $what$(if ($detail) { ": $detail" })" -ForegroundColor Red }
}
$helper = $null
try {
    # ---- 1a: parse, ASCII ----
    Write-Host 'part 1a: the scripts parse and are ASCII' -ForegroundColor Cyan
    foreach ($f in @($PlayLan, $PlayPs, $PSCommandPath)) {
        $errs = $null
        [void][System.Management.Automation.Language.Parser]::ParseFile($f, [ref]$null, [ref]$errs)
        Check 'a1' "$(Split-Path $f -Leaf) parses" (-not @($errs).Count) "$(@($errs | ForEach-Object { "$($_.Extent.StartLineNumber): $($_.Message)" }) -join '; ')"
        Check 'a2' "$(Split-Path $f -Leaf) is ASCII" (-not @([IO.File]::ReadAllBytes($f) | Where-Object { $_ -gt 127 }).Count)
    }

    # ---- 1b: the rules ----
    Write-Host 'part 1b: play_lan.ps1''s rules' -ForegroundColor Cyan
    $LanTestCopy = 'client\run_854_l\Dawngate'
    $LockFiles = @{ a = 're\logs\session.lock'; lan = 're\logs\session_lan.lock' }
    foreach ($text in @(Get-FunctionText $PlayLan @('Test-LanAddress', 'ConvertFrom-ShowLines', 'Get-CopyProblems', 'Get-CopyKind', 'Get-LiveLock',
            'Get-LockRefusal', 'Get-FolderLane', 'Get-MutexHolders', 'Test-ConfigNames', 'Get-PlayEnd', 'Get-SocketsLine', 'Get-AppLogLine',
            'Get-CheckResult'))) {
        . ([scriptblock]::Create($text))
    }
    foreach ($a in @('192.168.1.20', '10.0.0.5', '172.16.0.1', '172.31.255.254', '169.254.10.1', '100.64.0.9', '192.168.20.40', '8.8.8.8', '172.32.0.1')) {
        Check 'b1' "Test-LanAddress accepts $a" (-not (Test-LanAddress $a)) "$(Test-LanAddress $a)"
    }
    foreach ($a in @('127.0.0.1', '127.0.0.4', '0.0.0.0', '240.0.0.1', '224.0.0.251', '255.255.255.255', '1', '1.2.3', 'abc', '', '192.168.1.300')) {
        Check 'b2' "Test-LanAddress refuses '$a'" ([bool](Test-LanAddress $a))
    }
    $lanShow = ConvertFrom-ShowLines @('redirector=192.168.1.20', 'bind=as-shipped', 'appdata=DawngateData', 'documents=Dawngate', 'raknet_loopback=127.0.0.1', 'ca_sha256=0123456789abcdef', 'ca_flag=0')
    Check 'b3' 'ConvertFrom-ShowLines reads --show' ($lanShow['redirector'] -eq '192.168.1.20' -and $lanShow['ca_flag'] -eq '0' -and $lanShow.Count -eq 7)
    Check 'b4' 'a lan copy for its server has no problem' (-not @(Get-CopyProblems $lanShow '192.168.1.20').Count) "$(@(Get-CopyProblems $lanShow '192.168.1.20') -join '; ')"
    Check 'b4' 'a lan copy without -Server has no problem' (-not @(Get-CopyProblems $lanShow '').Count)
    Check 'b5' 'a lan copy for another server' ("$(Get-CopyProblems $lanShow '192.168.1.21')" -match 'patched for the server 192\.168\.1\.20, not 192\.168\.1\.21')
    $localShow = ConvertFrom-ShowLines @('redirector=127.0.0.1', 'bind=127.0.0.1', 'appdata=DawngateData', 'ca_flag=0')
    Check 'b6' 'a local-profile copy (client\run_854)' ("$(Get-CopyProblems $localShow '192.168.1.20')" -match 'local-profile copy')
    $origShow = ConvertFrom-ShowLines @('redirector=gosredirector.ea.com', 'bind=as-shipped', 'appdata=DawngateData', 'ca_flag=1')
    $p = "$(Get-CopyProblems $origShow '192.168.1.20')"
    Check 'b7' 'an unpatched exe (EA redirector, CA flag set)' ($p -match 'not a server address' -and $p -match "CA flag is '1'") $p
    Check 'b8' 'nothing read' ("$(Get-CopyProblems ([ordered]@{}) '192.168.1.20')" -match 'read nothing')
    $bindShow = ConvertFrom-ShowLines @('redirector=192.168.1.20', 'bind=192.168.1.20', 'appdata=DawngateDataL', 'ca_flag=0')
    Check 'b9' 'a lan copy with --client-bind' (-not @(Get-CopyProblems $bindShow '192.168.1.20').Count)
    $sr = Join-Path $Scratch 'rules'
    New-Item -ItemType Directory -Force (Join-Path $sr 're\logs') | Out-Null
    $k = Get-CopyKind (Join-Path $sr 'client\run_854_lan\Dawngate') $lanShow $sr
    Check 'b10' 'the player copy: lane a''s lock, no -multipleInstances' ($k.Kind -eq 'player copy' -and $k.Lock -eq 'a' -and $k.Letter -eq 'a' -and -not $k.Multi -and -not $k.Problem)
    $k = Get-CopyKind (Join-Path $sr 'client\run_854_l\Dawngate\') $bindShow $sr
    Check 'b11' 'the lan test copy: the lan lock, -multipleInstances' ($k.Kind -eq 'lan test copy' -and $k.Lock -eq 'lan' -and $k.Letter -eq 'l' -and $k.Multi -and -not $k.Problem)
    $k = Get-CopyKind (Join-Path $sr 'client\run_854_l\Dawngate') $lanShow $sr
    Check 'b12' 'the lan test copy without its own data folders' ([bool]$k.Problem)
    $k = Get-CopyKind (Join-Path $sr 'client\elsewhere\Dawngate') $bindShow $sr
    Check 'b13' 'another copy with its own data folders is refused' ($k.Kind -eq 'other' -and $k.Problem -match 'DawngateDataL')
    $k = Get-CopyKind 'E:\Games\Dawngate' $lanShow $sr
    Check 'b14' 'a player copy anywhere (setup_all.py --client) shares lane a''s lock' ($k.Lock -eq 'a')

    # locks: a live helper process, a stale stamp
    $helper = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-Command', 'Start-Sleep -Seconds 900') -WindowStyle Hidden -PassThru
    Start-Sleep -Milliseconds 500
    $hs = Get-ProcessStartStamp $helper.Id
    $laneALock = Join-Path $sr 're\logs\session.lock'; $lanLock = Join-Path $sr 're\logs\session_lan.lock'
    $player = [pscustomobject]@{ Lock = 'a' }; $lantest = [pscustomobject]@{ Lock = 'lan' }
    Check 'b15' 'no lock: free' (-not (Get-LockRefusal $player $sr) -and -not (Get-LockRefusal $lantest $sr))
    Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=$hs name=t mode=test lane=a since=x" -Encoding ASCII
    Check 'b16' 'lane a test session: the player copy refuses' ("$(Get-LockRefusal $player $sr)" -match 'another session is running in lane a')
    Check 'b17' 'lane a test session: the lan test copy may run' (-not (Get-LockRefusal $lantest $sr))
    Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=$hs name=t mode=play lane=a since=x" -Encoding ASCII
    Check 'b18' 'the user plays: the lan test copy waits' ("$(Get-LockRefusal $lantest $sr)" -match 'the user is playing')
    Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=2001-01-01T00:00:00.0000000+01:00 name=t mode=play lane=a since=x" -Encoding ASCII
    Check 'b19' 'a stale lock (start stamp of another process) is free' (-not (Get-LockRefusal $player $sr) -and -not (Get-LockRefusal $lantest $sr))
    Set-Content -Path $laneALock -Value "pid=$PID start=$(Get-ProcessStartStamp $PID) name=t mode=play lane=a since=x" -Encoding ASCII
    Check 'b20' 'a lock naming this very process is not another session' (-not (Get-LockRefusal $player $sr))
    Remove-Item $laneALock
    Set-Content -Path $lanLock -Value "pid=$($helper.Id) start=$hs name=t mode=test lane=d since=x lan=192.168.20.40" -Encoding ASCII
    Check 'b21' 'a lan session (run_session.ps1 -Lan): the lan test copy refuses' ("$(Get-LockRefusal $lantest $sr)" -match 'another lan session')
    Check 'b22' 'a lan session: the player copy may run' (-not (Get-LockRefusal $player $sr))
    Remove-Item $lanLock

    # leftovers by session folder lane
    foreach ($pair in @(@('p_l', 'l'), @('p_a', 'a'), @('p_none', $null), @('p_odd', 'lan'))) {
        New-Item -ItemType Directory -Force (Join-Path $sr "re\logs\$($pair[0])") | Out-Null
        if ($pair[1]) { Set-Content -Path (Join-Path $sr "re\logs\$($pair[0])\lane.txt") -Value $pair[1] -Encoding ASCII }
    }
    $cmdOf = { param($f) "`"C:\Program Files\Wireshark\dumpcap.exe`" -i x -w $(Join-Path $sr "re\logs\$f\capture.pcapng") -q" }
    Check 'b23' 'Get-FolderLane: lane.txt l' ((Get-FolderLane (& $cmdOf 'p_l') $sr) -eq 'l')
    Check 'b23' 'Get-FolderLane: lane.txt a' ((Get-FolderLane (& $cmdOf 'p_a') $sr) -eq 'a')
    Check 'b23' 'Get-FolderLane: no lane.txt = lane a (run_session.ps1''s rule)' ((Get-FolderLane (& $cmdOf 'p_none') $sr) -eq 'a')
    Check 'b23' 'Get-FolderLane: not one letter = lane a' ((Get-FolderLane (& $cmdOf 'p_odd') $sr) -eq 'a')
    Check 'b23' 'Get-FolderLane: another folder = lane a' ((Get-FolderLane '"dumpcap.exe" -w D:\elsewhere\capture.pcapng' $sr) -eq 'a')

    # the mutex holders
    $procs = @(
        [pscustomobject]@{ ProcessId = 1; ExecutablePath = 'D:\x\client\run_854_lan\Dawngate\game\Dawngate.exe'; CommandLine = '"D:\x\client\run_854_lan\Dawngate\game\Dawngate.exe"' },
        [pscustomobject]@{ ProcessId = 2; ExecutablePath = 'D:\x\client\run_854\Dawngate\game\Dawngate.exe'; CommandLine = '"D:\x\client\run_854\Dawngate\game\Dawngate.exe"' },
        [pscustomobject]@{ ProcessId = 3; ExecutablePath = 'D:\x\client\run_854_server\Dawngate\game\Dawngate.exe'; CommandLine = '"D:\x\...\Dawngate.exe" -multipleInstances' },
        [pscustomobject]@{ ProcessId = 4; ExecutablePath = $null; CommandLine = $null })
    $mh = @(Get-MutexHolders $procs 'd:\x\client\run_854_lan\dawngate\' $null)
    Check 'b24' 'the mutex holders: lane a''s client and an unreadable one, not the copy''s own or a host' ((@($mh | ForEach-Object { $_.ProcessId }) -join ',') -eq '2,4') "$(@($mh | ForEach-Object { $_.ProcessId }) -join ',')"
    $mh = @(Get-MutexHolders $procs 'd:\x\client\run_854_lan\dawngate\' 'D:\y')
    Check 'b25' 'the mutex holders within a scope (-StateRoot)' (-not $mh.Count)

    # Config.package names the server
    $cfgDir = Join-Path $sr 'cfg\game\Data'
    New-Item -ItemType Directory -Force $cfgDir | Out-Null
    [IO.File]::WriteAllBytes((Join-Path $cfgDir 'Config.package'), [byte[]](@(0, 1, 2) + [Text.Encoding]::ASCII.GetBytes('x=http://192.168.1.20:18080/foo') + @(0xFF, 0)))
    Check 'b26' 'Test-ConfigNames: the server' (Test-ConfigNames (Join-Path $sr 'cfg') '192.168.1.20')
    Check 'b26' 'Test-ConfigNames: another server' (-not (Test-ConfigNames (Join-Path $sr 'cfg') '192.168.1.2'))

    # the summary's parts
    $mon = "12:00:00.000 started D:\x\game\Dawngate.exe pid=4242`n12:00:02.000 t=2s UDP bound 0.0.0.0:3659`n12:00:02.000 t=2s TCP 192.168.1.5:50001 -> 192.168.1.20:42127 Established`n12:00:30.000 game exited at t=1230s code=0`n"
    Check 'b27' 'Get-PlayEnd: the game closed' ((Get-PlayEnd $mon) -eq 'ended when the game was closed at t=1230 s (code 0)')
    Check 'b27' 'Get-PlayEnd: no exit line' ((Get-PlayEnd 'nothing') -match 'no game exit')
    Check 'b28' 'Get-SocketsLine: wildcard binds' ((Get-SocketsLine $mon 'as-shipped') -eq 'bound as shipped: 0.0.0.0, 192.168.1.5 (1 UDP, 1 TCP local endpoints)') "$(Get-SocketsLine $mon 'as-shipped')"
    $mon2 = "t=2s UDP bound 192.168.1.20:3659`nt=2s TCP 192.168.1.20:50001 -> 192.168.1.20:42127 Established`n"
    Check 'b29' 'Get-SocketsLine: one address' ((Get-SocketsLine $mon2 '192.168.1.20') -eq '192.168.1.20 only (1 UDP, 1 TCP local endpoints)') "$(Get-SocketsLine $mon2 '192.168.1.20')"
    Check 'b29' 'Get-SocketsLine: another address seen' ((Get-SocketsLine ($mon2 + "t=4s UDP bound 127.0.0.1:5000`n") '192.168.1.20') -match '^NOT only 192\.168\.1\.20: also 127\.0\.0\.1')
    $app = @('### 2026-10-09 12:00:00', 'x transitioning from kGameStateBoot -> kGameStateLogin', 'x transitioning from kGameStateLogin -> kGameStateLobby',
        'x initiating connection to GMS [addr=192.168.1.20][port=10042]', 'x transitioning from kGameStatePreDungeon -> kGameStateDungeon', 'Assert Failed: y')
    $line = Get-AppLogLine $app @('192.168.1.20')
    Check 'b30' 'Get-AppLogLine: states, asserts, the GMS address' ($line -eq 'states kGameStateLogin > kGameStateLobby > kGameStateDungeon; 1 assert(s); GMS connect [addr=192.168.1.20][port=10042]') $line
    $line = Get-AppLogLine $app @('192.168.1.21')
    Check 'b31' 'Get-AppLogLine: a GMS address that isn''t allowed (a host machine)' ($line -match 'NOT an allowed address.*-AllowHost 192\.168\.1\.20') $line
    # -Check's verdict (P2-live): reached and still in kGameStateDungeon at the end
    $cr = Get-CheckResult $app
    Check 'b33' 'Get-CheckResult: in the match at the end' ($cr.Gms -and $cr.InGame -and $cr.Stayed -and $cr.Last -eq 'kGameStateDungeon')
    $cr = Get-CheckResult ($app + @('00:01:42:719 ERROR: Disconnected from online platform, 0x800d0000 SDK_ERR_CONN_FAILED', 'x Client game state transitioning from kGameStateDungeon -> kGameStateLogin'))
    Check 'b34' 'Get-CheckResult: left the match (p2live_run1b: the server stopped from outside)' ($cr.Gms -and $cr.InGame -and -not $cr.Stayed -and $cr.Last -eq 'kGameStateLogin') "$($cr | Out-String)"
    $cr = Get-CheckResult @('x transitioning from kGameStateBoot -> kGameStateLogin', 'x transitioning from kGameStateLogin -> kGameStateLobby')
    Check 'b35' 'Get-CheckResult: never in the match, no GMS' (-not $cr.Gms -and -not $cr.InGame -and -not $cr.Stayed)
    Check 'b35' 'Get-CheckResult: an empty section' (-not (Get-CheckResult @()).Stayed)
    . ([scriptblock]::Create((Get-FunctionText $PlayLan @('Get-AppLogSection'))))
    $appLog = Join-Path $sr 'Application.log'
    Set-Content -Path $appLog -Encoding ASCII -Value @('### 2026-10-09 10:00:00', 'old run', '### 2026-10-09 12:00:03', 'this run', 'x transitioning from a -> b')
    $sec = @(Get-AppLogSection ([datetime]'2026-10-09 12:00:00') $appLog)
    Check 'b32' 'Get-AppLogSection: this run''s section (started within 5 s of the launch)' (($sec -join '|') -eq '### 2026-10-09 12:00:03|this run|x transitioning from a -> b') ($sec -join '|')
    $sec = @(Get-AppLogSection ([datetime]'2026-10-09 13:00:00') $appLog)
    Check 'b32' 'Get-AppLogSection: no section of this run' ($sec[0] -match '^\(no Application\.log section started after')

    # ---- 1c: dry runs on a scratch state root ----
    Write-Host 'part 1c: play_lan.ps1 -DryRun / -SetupOnly on a scratch state root' -ForegroundColor Cyan
    $root = Join-Path $Scratch 'state'
    New-Item -ItemType Directory -Force (Join-Path $root 're\logs') | Out-Null
    $runN = 0
    function Invoke-Child([string]$script, [string[]]$argList) {
        $script:runN++
        $out = Join-Path $Scratch "child_$($script:runN).out"
        $all = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$script`"") + $argList
        $p = Start-Process -FilePath 'powershell.exe' -ArgumentList $all -NoNewWindow -Wait -PassThru -RedirectStandardOutput $out -RedirectStandardError "$out.err"
        [pscustomobject]@{ Code = $p.ExitCode; Out = "$(Get-Content $out -Raw -ErrorAction SilentlyContinue)$(Get-Content "$out.err" -Raw -ErrorAction SilentlyContinue)" }
    }
    function Expect([string]$case, [string]$what, $r, [int]$code, [string[]]$has = @(), [string[]]$hasNot = @()) {
        $miss = @($has | Where-Object { -not $r.Out.Contains($_) })
        $extra = @($hasNot | Where-Object { $r.Out.Contains($_) })
        $ok = $r.Code -eq $code -and -not $miss.Count -and -not $extra.Count
        $detail = "exit $($r.Code) (want $code)$(if ($miss.Count) { "; missing '$($miss -join "', '")'" })$(if ($extra.Count) { "; unexpected '$($extra -join "', '")'" })"
        if (-not $ok) { $detail += "`n$($r.Out)" }
        Check $case $what $ok $detail
    }
    $S = '192.168.1.20'
    $pristine = Join-Path $MainRoot 'client\ia_Dawngate\Dawngate\game'
    $havePristine = -not $NoPristine -and (Test-Path (Join-Path $pristine 'Dawngate.exe'))
    # cases that need no copy
    Expect 'c1' '-Server 127.0.0.1 is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', '127.0.0.1')) 2 @('not a host address of another machine')
    Expect 'c1' '-Server 8.8.8.8 is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', '224.0.0.9')) 2 @('not a host address of another machine')
    Expect 'c2' '-AllowHost 224.0.0.9 is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S, '-AllowHost', '224.0.0.9')) 2 @('-AllowHost')
    Expect 'c3' 'a bad -Name is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S, '-Name', '"a b"')) 2 @('invalid -Name')
    Expect 'c4' '-Check -NoCapture is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S, '-Check', '-NoCapture')) 2 @('-Check always runs with the capture')
    Expect 'c5' 'no copy: the setup command' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S)) 2 @('MISSING  the lan copy', "setup_all.py --player --server-host $S")
    $fake = Join-Path $root 'client\fake\Dawngate\game\Data'
    New-Item -ItemType Directory -Force $fake | Out-Null
    foreach ($f in @('..\Dawngate.exe', 'Config.package', 'Config_Ship.package')) { [IO.File]::WriteAllText((Join-Path $fake $f), 'same'); [IO.File]::WriteAllText((Join-Path $fake "$f.orig"), 'same') }
    Expect 'c6' 'an unpatched copy (exe = .orig)' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S, '-GameDir', "`"$(Join-Path $root 'client\fake\Dawngate')`"")) 2 @('is not patched')
    Expect 'c7' 'the pristine build is refused' (Invoke-Child $PlayLan @('-StateRoot', "`"$root`"", '-DryRun', '-Server', $S, '-GameDir', "`"$(Join-Path $root 'client\ia_Dawngate\Dawngate')`"")) 2 @('refusing the pristine extracted build')

    if (-not $havePristine) {
        Write-Host "  skip  c10-c27: no $pristine (or -NoPristine): no real lan copy to dry-run against" -ForegroundColor Yellow
    } else {
        # two real lan copies, patched in the scratch folder (the pristine files are only read)
        $modulus = Join-Path $Scratch 'ca_modulus.bin'
        $rnd = New-Object byte[] 128; (New-Object Random 854).NextBytes($rnd); $rnd[0] = $rnd[0] -bor 0x80
        [IO.File]::WriteAllBytes($modulus, $rnd)
        $copies = [ordered]@{
            'client\run_854_lan\Dawngate' = @('--profile', 'lan', '--server-host', $S)
            'client\run_854_l\Dawngate'   = @('--profile', 'lan', '--server-host', $S, '--client-bind', $S, '--data-suffix', 'L')
        }
        foreach ($rel in $copies.Keys) {
            $game = Join-Path $root "$rel\game"
            New-Item -ItemType Directory -Force (Join-Path $game 'Data') | Out-Null
            Copy-Item -LiteralPath (Join-Path $pristine 'Dawngate.exe') -Destination $game
            foreach ($c in @('Config.package', 'Config_Ship.package', 'Config_Pack.package')) { Copy-Item -LiteralPath (Join-Path $pristine "Data\$c") -Destination (Join-Path $game 'Data') }
            $o1 = & $Python -I (Join-Path $PSScriptRoot 'patch_client.py') (Join-Path $game 'Dawngate.exe') $modulus @($copies[$rel])
            $c1 = $LASTEXITCODE
            $o2 = & $Python -I (Join-Path $PSScriptRoot 'patch_config.py') (Join-Path $game 'Data') --host $S
            $c2 = $LASTEXITCODE
            Check 'c9' "scratch copy $rel patched (patch_client.py, patch_config.py)" ($c1 -eq 0 -and $c2 -eq 0) "$o1 $o2"
        }
        $lanT = "`"$(Join-Path $root 'client\run_854_l\Dawngate')`""
        $sr2 = @('-StateRoot', "`"$root`"")
        Expect 'c10' 'the player copy, -Server' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S))) 0 @(
            'player copy', "the egress check allows $S only", 're\logs\session.lock, lane letter a: free', "lane.txt 'a'", 'runs without -multipleInstances',
            '-Seconds 0 -ShotTimes none -SnapLen 256', "--allow-host $S", 'leftovers: none') @('-GameArgs', '-Login', '-NoCapture', '-KeepAlive')
        Expect 'c11' 'the player copy, the server from the copy' (Invoke-Child $PlayLan ($sr2 + @('-DryRun'))) 0 @("server $S (the address the copy was patched for", "--allow-host $S")
        Expect 'c12' '-SetupOnly' (Invoke-Child $PlayLan ($sr2 + @('-SetupOnly', '-Server', $S))) 0 @("Setup complete: the lan copy is ready for $S") @('Dry run')
        Expect 'c13' 'another server than the copy''s' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', '192.168.1.21'))) 2 @("patched for the server $S, not 192.168.1.21", 'setup_all.py --player --server-host 192.168.1.21')
        Expect 'c14' '-NoCapture' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-NoCapture'))) 0 @('-NoCapture', 'capture  : none (-NoCapture)', 'egress   : not checked') @('-SnapLen')
        Expect 'c15' '-AllowHost (comma-separated)' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-AllowHost', '192.168.1.30,10.0.0.7'))) 0 @("--allow-host $S --allow-host 192.168.1.30 --allow-host 10.0.0.7")
        Expect 'c16' '-Check: login, clicks, background, a time limit' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-Check'))) 0 @(
            '-Login "play_lan_check_', '@example.com|dawngate1" -LoginAt 12', '-Actions "24:0.5555,0.8514;26:0.5555,0.8514;28:0.8016,0.8486;32:0.4984,0.0833;36:0.5,0.8236"', '-Background', 'the time limit 150 s')
        Expect 'c17' '-KeepAlive' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-KeepAlive', '-Seconds', '600'))) 0 @('-KeepAlive 60', 'the time limit 600 s')
        Expect 'c18' 'the lan test copy' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-GameDir', $lanT))) 0 @(
            'lan test copy', 're\logs\session_lan.lock, lane letter l: free', "lane.txt 'l'", 'runs with -multipleInstances', '-GameArgs:-multipleInstances', "binds $S", 'DawngateDataL')
        $laneALock = Join-Path $root 're\logs\session.lock'; $lanLock = Join-Path $root 're\logs\session_lan.lock'
        Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=$hs name=t mode=test lane=a since=x" -Encoding ASCII
        Expect 'c19' 'lane a busy: the player copy refuses' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S))) 2 @('WOULD REFUSE: another session is running in lane a')
        Expect 'c20' 'lane a busy (test): the lan test copy runs' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-GameDir', $lanT))) 0 @('session_lan.lock, lane letter l: free')
        Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=$hs name=t mode=play lane=a since=x" -Encoding ASCII
        Expect 'c21' 'the user plays: the lan test copy waits' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-GameDir', $lanT))) 2 @('WOULD REFUSE: the user is playing')
        Set-Content -Path $laneALock -Value "pid=$($helper.Id) start=2001-01-01T00:00:00.0000000+01:00 name=t mode=play lane=a since=x" -Encoding ASCII
        Expect 'c22' 'a stale lane a lock' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S))) 0 @('session.lock, lane letter a: free')
        Remove-Item $laneALock
        Set-Content -Path $lanLock -Value "pid=$($helper.Id) start=$hs name=t mode=test lane=d since=x lan=$S" -Encoding ASCII
        Expect 'c23' 'a lan session runs: the lan test copy refuses' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-GameDir', $lanT))) 2 @('WOULD REFUSE: another lan session is running')
        Expect 'c24' 'a lan session runs: the player copy may' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S))) 0 @('session.lock, lane letter a: free')
        Remove-Item $lanLock
        New-Item -ItemType Directory -Force (Join-Path $root 're\logs\taken') | Out-Null
        Expect 'c25' 'an existing session folder' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-Name', 'taken'))) 2 @('exists already')
        Expect 'c26' 'a named session' (Invoke-Child $PlayLan ($sr2 + @('-DryRun', '-Server', $S, '-Name', 'p2_mine'))) 0 @("re\logs\p2_mine (lane.txt 'a')")
        $left = @(Get-ChildItem (Join-Path $root 're\logs') -Directory | Where-Object { $_.Name -ne 'taken' -and $_.Name -notlike 'p_*' })
        $locks = @(Get-ChildItem (Join-Path $root 're\logs') -Filter '*.lock')
        Check 'c27' 'the dry runs wrote nothing (no session folder, no lock)' (-not $left.Count -and -not $locks.Count) "$(@($left + $locks | ForEach-Object { $_.Name }) -join ', ')"
    }

    # ---- 1d: play.ps1 -Server ----
    Write-Host 'part 1d: play.ps1 -Server passes on to play_lan.ps1 (refusals only: no copy is read)' -ForegroundColor Cyan
    Expect 'd1' 'play.ps1 -Server 127.0.0.1 reaches play_lan.ps1''s check' (Invoke-Child $PlayPs @('-Server', '127.0.0.1', '-SetupOnly')) 2 @('-Server: 127.0.0.1 is not a host address of another machine') @('checking the setup')
    Expect 'd2' 'play.ps1 -Server with -ShaperSelect' (Invoke-Child $PlayPs @('-Server', $S, '-ShaperSelect')) 2 @('server operator') @('checking the setup')
    Expect 'd3' 'play.ps1 -Server with -HostOptions' (Invoke-Child $PlayPs @('-Server', $S, '-HostOptions', 'bots=0')) 2 @('server operator')
    Expect 'd4' 'play.ps1 -Server passes -Name' (Invoke-Child $PlayPs @('-Server', $S, '-Name', '"a b"')) 2 @('invalid -Name') @('checking the setup')
    Expect 'd5' 'play.ps1 -Server passes -Check with -NoCapture' (Invoke-Child $PlayPs @('-Server', $S, '-Check', '-NoCapture')) 2 @('-Check always runs with the capture')
    Expect 'd6' 'play.ps1 -Server passes -AllowHost' (Invoke-Child $PlayPs @('-Server', $S, '-AllowHost', '192.168.1.30,224.0.0.9')) 2 @('-AllowHost: 224.0.0.9 is not a host address of another machine')
    Expect 'd7' 'play.ps1 -AllowHost without -Server' (Invoke-Child $PlayPs @('-AllowHost', '192.168.1.30')) 2 @('-AllowHost is for -Server') @('checking the setup')
} finally {
    if ($helper -and -not $helper.HasExited) { Stop-Process -Id $helper.Id -Force -ErrorAction SilentlyContinue }
    if (-not $Keep) { Remove-Item -LiteralPath $Scratch -Recurse -Force -ErrorAction SilentlyContinue }
}
Write-Host ''
$color = if ($script:fail) { 'Red' } else { 'Green' }
Write-Host "P2 test: $($script:pass) passed, $($script:fail) failed$(if ($Keep) { " (scratch kept: $Scratch)" })" -ForegroundColor $color
exit $(if ($script:fail) { 1 } else { 0 })
