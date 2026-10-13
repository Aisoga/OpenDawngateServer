<#
  The release's live check: runs an unpacked release (Start-Server.exe,
  Start-Client.exe) as a player would, in lane a (127.0.0.1, %APPDATA%\DawngateData, re\logs\session.lock), with the
  game window parked and never focused (the user's rule of 2026-10-09), and checks the result:
    1. lane a must be free: its lock names no live session (or names -LockPid, a holder the caller started), no
       Dawngate.exe, dgserver.exe, Start-Server.exe or Start-Client.exe runs, no dumpcap writes into re\logs, and
       lane a's ports (TCP 42127, 10041, 17502, 18080, 9988, 10040; UDP 17502, 10042-10051) are free;
    2. re\scripts\focus_watch.ps1 logs every foreground change and every game window on the desktop (focus.log);
    3. Start-Server.exe --non-interactive (hidden console, output server.txt): the first start sets everything up next
       to it (Dawngate-Data), then runs the server;
    4. launch_monitor.ps1 -Attach -Background -ShotFocus off: the capture (dumpcap) into the session folder, the
       scripted login, play.ps1 -Check's clicks to PRACTICE DAWNGATE and shaper select's (the release runs with
       shaper select on: the first card, Choose, Hunter, Finalize; the skin phase times out), the keep-alive key, the
       game closed at -Seconds;
    5. Start-Client.exe --non-interactive --test-park, started through WMI (Win32_Process.Create, so not a descendant of
       the console this runs in; hidden console, output client.txt): the player copy checked and the game started,
       its windows parked off the desktop every 50 ms, the foreground locked while the user is away and given back if
       the game takes it anyway;
    6. while it runs: the client's Application.log (game states), re\scripts\game_peek.py <game pid> at -PeekAt
       seconds after kGameStateDungeon;
    7. the end: Start-Client ends with the game, Start-Server gets a Ctrl+C in its own console (GenerateConsoleCtrlEvent
       from a helper; no key on the real keyboard) and stops dgserver and its match hosts, the capture is stopped,
       egress_report.py checks it, the logs are collected (server log, the hosts' logs, this run's Application.log
       section), nothing may be left running and the ports must be free.
  -MenuOnly: the second start's check: login only, passes at the main menu (kGameStateLobby); the setup must be current.

  The public profile's end-to-end check:
    -Address <IPv4>|lan   Start-Server.ini's address before the first start ('lan' = this PC's address on the default
                          route; only this PC's own private address, as run_session.ps1 -Lan). The server's ports are
                          checked on it too, re\logs\session_lan.lock is held beside lane a's, and the egress check
                          gets --allow-host <it>.
    -Ini 'k=v;k=v'        more Start-Server.ini keys before the first start (e.g. 'profile=public;signup=invite').
    -ClientArgs '...'     more Start-Client.exe arguments (e.g. '--tunnel on --trust': its own tunnel to the gateway).
    -Website              the website and the admin page: before the server starts an admin account (Start-Server.exe
                          --create-account siteadmin, --make-admin), once it runs an invite code made on the admin page
                          and -Login's name registered with it on /signup (re\scripts\admin_page.py; the site's
                          self-signed certificate checked), during the match the admin page's matches, at the end its
                          audit log. The game then logs in with the registered account.
    With the tunnel on (Start-Client's 'tunnel   to ... ready' line), re\scripts\tunnel_capture.py --same-host checks
    the capture (only the gateway's port is reached on the server address).

  Usage (from the repository folder):
    powershell -ExecutionPolicy Bypass -File re\scripts\test_p3c.ps1 -Name release_check1 -Root <release folder>
        [-Seconds 400] [-Login 'release1@example.com|dawngate1'] [-PeekAt '60,190'] [-MenuOnly] [-LockPid <pid>]
  Output: re\logs\<Name>\ (summary.txt, server.txt, client.txt, monitor.log, capture.pcapng, egress.txt, focus.log,
  application_log.txt, server_log.txt, host\<match>\..., peek_<s>.txt). Exit code: 0 passed, 1 egress flagged,
  2 lane a not free or a setup/run failure, 3 the session was clean but a check failed.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [string]$Name,
    [string]$Root,
    [int]$Seconds = 400,
    [string]$Login = 'release1@example.com|dawngate1',
    [string]$Actions,
    [string]$PeekAt = '60,190',
    [switch]$MenuOnly,
    [int]$LockPid = 0,
    [int]$CtrlCInto = 0,
    [string]$Address = '127.0.0.1',
    [string]$Ini = '',
    [string]$ClientArgs = '',
    [switch]$Website
)
$ErrorActionPreference = 'Stop'

if ($CtrlCInto) {
    # helper mode: one Ctrl+C into the console of process <pid> (Start-Server.exe's own hidden console), ignored here
    Add-Type @"
using System; using System.Runtime.InteropServices;
public static class DgCtrlC {
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool FreeConsole();
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool AttachConsole(uint pid);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool SetConsoleCtrlHandler(IntPtr h, bool add);
  [DllImport("kernel32.dll", SetLastError = true)] static extern bool GenerateConsoleCtrlEvent(uint ev, uint group);
  public static string Send(uint pid) {
    FreeConsole();
    if (!AttachConsole(pid)) return "AttachConsole(" + pid + ") failed: " + Marshal.GetLastWin32Error();
    SetConsoleCtrlHandler(IntPtr.Zero, true);
    bool ok = GenerateConsoleCtrlEvent(0, 0);  // CTRL_C_EVENT to every process of that console
    int e = Marshal.GetLastWin32Error();
    System.Threading.Thread.Sleep(300);
    FreeConsole();
    return ok ? "sent" : "GenerateConsoleCtrlEvent failed: " + e;
  }
}
"@
    $r = [DgCtrlC]::Send([uint32]$CtrlCInto)
    [IO.File]::WriteAllText("$env:TEMP\dg_ctrlc_$CtrlCInto.txt", $r)
    if ($r -eq 'sent') { exit 0 } else { exit 1 }
}

if (-not $Name -or -not $Root) { Write-Host 'usage: test_p3c.ps1 -Name <session> -Root <release folder> [...]' -ForegroundColor Red; exit 2 }
$Repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$Python = Join-Path $Repo '.venv\Scripts\python.exe'
$Root = (Resolve-Path $Root).Path
$StartServer = Join-Path $Root 'Start-Server.exe'
$StartClient = Join-Path $Root 'Start-Client.exe'
$Data = Join-Path $Root 'Dawngate-Data'
$GameDir = Join-Path $Data 'client\Dawngate'
$AppLog = Join-Path $env:APPDATA 'DawngateData\Application.log'
$LockFile = Join-Path $Repo 're\logs\session.lock'
$Session = Join-Path $Repo "re\logs\$Name"
if ($Name -notmatch '^[\w.-]+$') { Write-Host "invalid -Name '$Name'" -ForegroundColor Red; exit 2 }
foreach ($f in @($StartServer, $StartClient, $Python)) { if (-not (Test-Path $f)) { Write-Host "missing $f" -ForegroundColor Red; exit 2 } }
if ($Root -match '\\client\\ia_') { Write-Host 'refusing a pristine folder' -ForegroundColor Red; exit 2 }
if (-not $Actions) {
    # play.ps1 -Check's clicks (the news overlay twice, OK, PLAY, then the full play screen's Practice and PRACTICE
    # DAWNGATE), then shaper select (it opened about 48 s in): the first card, Choose, the Hunter role and
    # Finalize, each twice in case the screen came late; the skin phase ends by itself (15.5 s)
    $Actions = '24:0.5555,0.8514;26:0.5555,0.8514;28:0.8016,0.8486;32:0.4984,0.0833;36:0.1109,0.4722;40:0.4055,0.8236'
    if (-not $MenuOnly) { $Actions += ';56:0.2477,0.2639;58:0.2477,0.2639;60:0.6531,0.75;66:0.2477,0.2639;68:0.6531,0.75;72:0.6766,0.2944;74:0.642,0.754;80:0.6766,0.2944;82:0.642,0.754' }
}
if ($MenuOnly) { $Actions = ''; if (-not $PSBoundParameters.ContainsKey('Seconds')) { $Seconds = 60 } }
# the server's address (lan = this PC's address on the default route; only this PC's own private address)
if ($Address -eq 'lan') {
    $route = Get-NetRoute -DestinationPrefix 0.0.0.0/0 -ErrorAction SilentlyContinue | Sort-Object RouteMetric | Select-Object -First 1
    $Address = if ($route) { "$((Get-NetIPAddress -AddressFamily IPv4 -InterfaceIndex $route.ifIndex -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress)" } else { '' }
}
$ipA = $null
if (-not [Net.IPAddress]::TryParse($Address, [ref]$ipA) -or $ipA.AddressFamily -ne 'InterNetwork') { Write-Host "-Address '$Address' is not an IPv4 address (or lan)" -ForegroundColor Red; exit 2 }
$IsLan = $Address -ne '127.0.0.1'
if ($IsLan) {
    $b = $ipA.GetAddressBytes()
    $private = $b[0] -eq 10 -or ($b[0] -eq 172 -and $b[1] -ge 16 -and $b[1] -le 31) -or ($b[0] -eq 192 -and $b[1] -eq 168)
    if (-not $private) { Write-Host "-Address $Address is not a private address of our own network" -ForegroundColor Red; exit 2 }
    if (-not @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $Address -ErrorAction SilentlyContinue).Count) { Write-Host "-Address $Address is not an address of this PC" -ForegroundColor Red; exit 2 }
}
$LoginName = ($Login -split '\|', 2)[0]; $LoginPw = ($Login -split '\|', 2)[1]

function Get-StartStamp([int]$id) { try { (Get-Process -Id $id -ErrorAction Stop).StartTime.ToString('o') } catch { $null } }
function Get-LiveLock {
    if (-not (Test-Path $LockFile)) { return $null }
    $t = "$(Get-Content $LockFile -Raw -ErrorAction SilentlyContinue)".Trim()
    if ($t -match 'pid=(\d+) start=(\S+)' -and (Get-StartStamp ([int]$Matches[1])) -eq $Matches[2]) { return $t }
    $null
}
$lock = Get-LiveLock
$ownLock = $false
if ($lock) {
    if (-not ($LockPid -and $lock -match "pid=$LockPid ")) { Write-Host "lane a is in use: $lock" -ForegroundColor Red; exit 2 }
} else {
    Set-Content -Path $LockFile -Encoding ASCII -Value "pid=$PID start=$(Get-StartStamp $PID) name=$Name mode=test lane=a since=$(Get-Date -Format s)"
    $ownLock = $true
}
# a LAN address takes the PC's lan lock too (one lan session at a time, run_session.ps1 -Lan's rule)
$LanLock = Join-Path $Repo 're\logs\session_lan.lock'
$ownLanLock = $false
if ($IsLan) {
    $t = if (Test-Path $LanLock) { "$(Get-Content $LanLock -Raw -ErrorAction SilentlyContinue)".Trim() } else { '' }
    if ($t -match 'pid=(\d+) start=(\S+)' -and (Get-StartStamp ([int]$Matches[1])) -eq $Matches[2]) {
        Write-Host "a lan session runs: $t" -ForegroundColor Red; if ($ownLock) { Remove-Item $LockFile -Force }; exit 2
    }
    Set-Content -Path $LanLock -Encoding ASCII -Value "pid=$PID start=$(Get-StartStamp $PID) name=$Name mode=test lane=a since=$(Get-Date -Format s)"
    $ownLanLock = $true
}
if (Test-Path $Session) { Write-Host "$Session exists; pick another -Name" -ForegroundColor Red; if ($ownLock) { Remove-Item $LockFile -Force }; exit 2 }
New-Item -ItemType Directory -Force $Session | Out-Null
Set-Content -Path (Join-Path $Session 'lane.txt') -Value 'a' -Encoding ASCII
$LogFile = Join-Path $Session 'session.log'
function Say([string]$m, [string]$c = 'Gray') {
    $l = '{0:HH:mm:ss} {1}' -f (Get-Date), $m
    Write-Host $l -ForegroundColor $c
    # never fail on the log: a test run died here when a reader (a tail -F) held session.log for a moment
    for ($i = 0; $i -lt 5; $i++) {
        try { [IO.File]::AppendAllText($LogFile, "$l`r`n", (New-Object Text.UTF8Encoding($false))); break } catch { Start-Sleep -Milliseconds 100 }
    }
}
function In-S([string]$f) { Join-Path $Session $f }

$TcpPorts = @(42127, 10041, 17502, 18080, 9988, 10040, 42128, 443)
$UdpPorts = @(17502, 42128) + (10042..10051)
$LaneAddrs = @('127.0.0.1', '0.0.0.0', '::')
if ($IsLan) { $LaneAddrs += $Address }
function Get-Held {
    $h = @()
    foreach ($c in @(Get-NetTCPConnection -State Listen -LocalPort $TcpPorts -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in $LaneAddrs })) { $h += "TCP $($c.LocalAddress):$($c.LocalPort) pid $($c.OwningProcess)" }
    foreach ($u in @(Get-NetUDPEndpoint -ErrorAction SilentlyContinue | Where-Object { $_.LocalPort -in $UdpPorts -and $_.LocalAddress -in $LaneAddrs })) { $h += "UDP $($u.LocalAddress):$($u.LocalPort) pid $($u.OwningProcess)" }
    $h
}
function Get-Blockers {
    # what must not run when the check starts (no other lane is in use during this check): any Dawngate client or host, any
    # dgserver, any release launcher, any dumpcap writing into re\logs
    $logs = (Join-Path $Repo 're\logs\').ToLower()
    @(Get-CimInstance Win32_Process | Where-Object {
        $_.Name -in @('Dawngate.exe', 'CrashSender1402.exe', 'dgserver.exe', 'Start-Server.exe', 'Start-Client.exe') -or
        ($_.Name -eq 'dumpcap.exe' -and "$($_.CommandLine)".ToLower().Contains($logs))
    })
}
function Get-Ours {
    # every process this check may have started: the release's programs and games, this session's capture, monitor and
    # focus watch (nothing else is ever stopped)
    $sess = $Session.ToLower(); $root = $Root.ToLower()
    @(Get-CimInstance Win32_Process | Where-Object {
        $exe = "$($_.ExecutablePath)".ToLower(); $cmd = "$($_.CommandLine)".ToLower()
        ($_.Name -in @('Dawngate.exe', 'CrashSender1402.exe', 'Start-Server.exe', 'Start-Client.exe') -and $exe.StartsWith($root)) -or
        ($_.Name -eq 'dumpcap.exe' -and $cmd.Contains($sess)) -or
        ($_.Name -eq 'powershell.exe' -and $cmd -match 'launch_monitor\.ps1|focus_watch\.ps1' -and $cmd.Contains($sess))
    })
}
function Get-AppSection([datetime]$since) {
    if (-not (Test-Path $AppLog)) { return @() }
    $lines = @(Get-Content $AppLog)
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        if ($lines[$i] -match '^### (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})') {
            $t = [datetime]::ParseExact($Matches[1], 'yyyy-MM-dd HH:mm:ss', [Globalization.CultureInfo]::InvariantCulture)
            if ($t -ge $since.AddSeconds(-5)) { return $lines[$i..($lines.Count - 1)] }
            break
        }
    }
    @()
}
function Wait-File([string]$file, [string]$pattern, [int]$sec, $proc) {
    $deadline = (Get-Date).AddSeconds($sec)
    while ((Get-Date) -lt $deadline) {
        if ((Test-Path $file) -and (Select-String -Path $file -Pattern $pattern -Quiet)) { return $true }
        if ($proc -and $proc.HasExited) { return $false }
        Start-Sleep -Milliseconds 250
    }
    $false
}

$fail = @(); $notes = @()
$srv = $null; $cli = $null; $mon = $null; $fw = $null
$t0 = Get-Date
try {
    # 1. lane a free
    $ours = @(Get-Blockers)
    $held = @(Get-Held)
    if ($ours.Count -or $held.Count) {
        throw ("lane a is not free: " + ((@($ours | ForEach-Object { "$($_.Name) pid $($_.ProcessId)" }) + $held) -join '; '))
    }
    Say "lane a free (lock $(if ($ownLock) { 'taken by this check' } else { "held by pid $LockPid" })); release $Root" 'Cyan'

    # 2. the focus watch
    $fwStop = In-S 'focus.stop'
    $fw = Start-Process powershell.exe -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$(Join-Path $PSScriptRoot 'focus_watch.ps1')`"",
        '-Out', "`"$(In-S 'focus.log')`"", '-Under', "`"$Root`"", '-StopFile', "`"$fwStop`"", '-Seconds', [string]([int]($Seconds * 2 + 1200))) `
        -WindowStyle Hidden -PassThru -RedirectStandardOutput (In-S 'focus.out') -RedirectStandardError (In-S 'focus.err')
    $null = $fw.Handle
    if (-not (Wait-File (In-S 'focus.log') 'hooks:' 20 $fw)) { throw 'focus_watch.ps1 did not start (focus.err)' }
    Say "focus watch: $((Select-String -Path (In-S 'focus.log') -Pattern 'hooks:').Line)"

    # 3a. Start-Server.ini before the first start (written by a --check, which changes nothing else), then the keys
    $iniPath = Join-Path $Root 'Start-Server.ini'
    if (-not (Test-Path $iniPath)) { & $StartServer --non-interactive --check *> (In-S 'ini_check.txt') }
    if (-not (Test-Path $iniPath)) { throw 'Start-Server.exe --check wrote no Start-Server.ini' }
    $keys = [ordered]@{}
    if ($PSBoundParameters.ContainsKey('Address') -or $IsLan) { $keys['address'] = $Address }
    foreach ($kv in @($Ini.Split(';') | Where-Object { $_.Trim() })) { $k, $v = $kv.Split('=', 2); $keys[$k.Trim()] = "$v".Trim() }
    if ($keys.Count) {
        $lines = [Collections.Generic.List[string]]@(Get-Content $iniPath)
        foreach ($k in $keys.Keys) {
            $i = -1
            for ($j = 0; $j -lt $lines.Count; $j++) { if ($lines[$j] -match "^\s*$([regex]::Escape($k))\s*=") { $i = $j } }
            if ($i -ge 0) { $lines[$i] = "$k = $($keys[$k])" } else { $lines.Add("$k = $($keys[$k])") }
        }
        [IO.File]::WriteAllText($iniPath, (($lines -join "`r`n") + "`r`n"), (New-Object Text.ASCIIEncoding))
        Say "Start-Server.ini: $(@($keys.Keys | ForEach-Object { "$_ = $($keys[$_])" }) -join ', ')"
    }
    $iniCfg = @{}
    foreach ($l in Get-Content $iniPath) { if ($l -match '^\s*([a-z_]+)\s*=\s*(.*)$') { $iniCfg[$Matches[1]] = $Matches[2].Trim() } }
    $webPort = if ($iniCfg['web_port']) { [int]$iniCfg['web_port'] } else { 443 }
    $SiteBase = if ($webPort -eq 443) { "https://$Address" } else { "https://${Address}:$webPort" }
    $SiteCa = Join-Path $Data 'data\site\site_selfsigned.pem'
    $AdminPage = Join-Path $PSScriptRoot 'admin_page.py'
    function Site([string]$out, [string[]]$a) {
        $r = & $Python -I $AdminPage --base $SiteBase --cafile $SiteCa @a 2>&1
        $code = $LASTEXITCODE
        Add-Content -Path (In-S $out) -Value $r -Encoding UTF8
        foreach ($l in @($r)) { Say "site: $("$l" -replace '^code: .*', 'code: <in invite.secret>')" }
        $code
    }
    # 3b. an admin for the website's admin page (the operator's account commands, before the server runs)
    if ($Website) {
        $acct = & $StartServer --non-interactive --create-account siteadmin 2>&1
        Set-Content -Path (In-S 'admin_account.txt') -Value ($acct -replace '^\s{4}\S{16}$', '    <the password>') -Encoding UTF8
        $pwLine = @($acct | Where-Object { "$_" -match '^\s{4}(\S{16})$' } | Select-Object -First 1)
        if (-not $pwLine.Count) { throw "Start-Server.exe --create-account siteadmin printed no password (admin_account.txt)" }
        $AdminPwFile = In-S 'admin_pw.secret'
        [IO.File]::WriteAllText($AdminPwFile, "$($pwLine[0])".Trim())
        $mk = & $StartServer --non-interactive --make-admin siteadmin 2>&1
        Add-Content -Path (In-S 'admin_account.txt') -Value $mk -Encoding UTF8
        if ($LASTEXITCODE -ne 0) { throw 'Start-Server.exe --make-admin siteadmin failed (admin_account.txt)' }
        Say 'admin account siteadmin made (Start-Server.exe --create-account, --make-admin)'
    }

    # 3. Start-Server.exe (its own hidden console; --seconds as a safety net)
    $srvStart = Get-Date
    $srv = Start-Process $StartServer -ArgumentList @('--non-interactive', '--seconds', [string]([int]($Seconds * 1.3) + 900)) -WorkingDirectory $Root `
        -WindowStyle Hidden -PassThru -RedirectStandardOutput (In-S 'server.txt') -RedirectStandardError (In-S 'server.err')
    $null = $srv.Handle
    Say "Start-Server.exe pid $($srv.Id) started"
    if (-not (Wait-File (In-S 'server.txt') 'The server runs on' 900 $srv)) {
        throw "Start-Server.exe did not get to 'The server runs' (exit $(if ($srv.HasExited) { $srv.ExitCode } else { 'still running' }); server.txt)"
    }
    $setupLine = "$((Select-String -Path (In-S 'server.txt') -Pattern 'the setup is complete' | Select-Object -First 1).Line)"
    $srvReady = ((Get-Date) - $srvStart).TotalSeconds
    $doneSteps = @(Select-String -Path (In-S 'server.txt') -Pattern '^\s*\[done\]').Count
    $okSteps = @(Select-String -Path (In-S 'server.txt') -Pattern '^\s*\[ok\]').Count
    Say ("server ready {0:N1} s after the start ({1} step(s) done, {2} current)" -f $srvReady, $doneSteps, $okSteps) 'Green'
    # 3c. an invite code from the admin page, and the -Login account registered with it on the website
    $siteInvite = $null; $siteRegister = $null
    if ($Website) {
        $pwFile = In-S 'player_pw.secret'
        [IO.File]::WriteAllText($pwFile, $LoginPw)
        $siteInvite = Site 'site.txt' @('--name', 'siteadmin', '--password-file', $AdminPwFile, '--out', (In-S 'invite.secret'), 'invite', '1', '1', 'release check')
        if ($siteInvite -ne 0) { throw 'no invite code from the admin page (site.txt)' }
        $siteRegister = Site 'site.txt' @('--password-file', $pwFile, '--code-file', (In-S 'invite.secret'), 'register', $LoginName, 'Reg8')
        if ($siteRegister -ne 0) { throw "the website did not register $LoginName (site.txt)" }
    }

    # 4. the monitor (waits for the game Start-Client starts; capture into the session folder)
    $monArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$(Join-Path $PSScriptRoot 'launch_monitor.ps1')`"",
        '-GameDir', "`"$GameDir`"", '-OutDir', "`"$Session`"", '-Attach', '-AttachWait', '120', '-Background', '-ShotFocus', 'off',
        '-Seconds', $Seconds, '-ShotTimes', 'none', '-Login', "`"$Login`"")
    if ($Actions) { $monArgs += @('-Actions', "`"$Actions`"") }
    if ($Seconds -gt 300) { $monArgs += @('-KeepAlive', '60') }
    $mon = Start-Process powershell.exe -ArgumentList $monArgs -WindowStyle Hidden -PassThru -RedirectStandardOutput (In-S 'monitor.out') -RedirectStandardError (In-S 'monitor.err')
    $null = $mon.Handle
    if (-not (Wait-File (In-S 'monitor.log') 'dumpcap pid=' 30 $mon)) { throw 'the monitor did not start its capture (monitor.err)' }
    Start-Sleep -Seconds 2

    # 5. Start-Client.exe --test-park, started through WMI (Win32_Process.Create: its parent is WmiPrvSE, in this
    #    session): a program started from this script inherits, through the console it runs in, the
    #    right to take the foreground whenever the user has just typed into that console, and the game took the
    #    foreground with it at its start (two test runs). cmd.exe /c only redirects the output; its console is hidden.
    $cliStart = Get-Date
    $cliCmd = "cmd.exe /c `"`"$StartClient`" --non-interactive --test-park $ClientArgs > `"$(In-S 'client.txt')`" 2> `"$(In-S 'client.err')`"`""
    $si = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
    $wmi = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = $cliCmd; CurrentDirectory = $Root; ProcessStartupInformation = $si }
    if ($wmi.ReturnValue -ne 0) { throw "Win32_Process.Create failed ($($wmi.ReturnValue)) for Start-Client.exe" }
    $cli = $null; $deadline = (Get-Date).AddSeconds(15)
    while (-not $cli -and (Get-Date) -lt $deadline) {
        $c = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($wmi.ProcessId) AND Name='Start-Client.exe'")
        if ($c.Count) { $cli = Get-Process -Id $c[0].ProcessId -ErrorAction SilentlyContinue }
        if (-not $cli) { Start-Sleep -Milliseconds 200 }
    }
    if (-not $cli) { throw 'Start-Client.exe did not start (through WMI)' }
    $null = $cli.Handle
    Say "Start-Client.exe pid $($cli.Id) started (--test-park; through WMI, cmd pid $($wmi.ProcessId))"
    if (-not (Wait-File (In-S 'client.txt') 'game pid \d+' 120 $cli)) { throw "Start-Client.exe started no game (exit $(if ($cli.HasExited) { $cli.ExitCode }); client.txt)" }
    $gamePid = [int]([regex]::Match((Get-Content (In-S 'client.txt') -Raw), 'game pid (\d+)').Groups[1].Value)
    Say ("game pid {0}, {1:N1} s after Start-Client's start" -f $gamePid, ((Get-Date) - $cliStart).TotalSeconds)

    # 6. watch: game states, peeks
    $peeks = @($PeekAt.Split(',') | Where-Object { $_.Trim() } | ForEach-Object { [int]$_ })
    $peeksDone = @{}
    $states = @(); $dungeonAt = $null; $lobbyAt = $null
    while (-not $mon.HasExited) {
        $sec = @(Get-AppSection $cliStart)
        $now = @($sec | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ([regex]::Match($_, '-> (\S+)')).Groups[1].Value })
        if ($now.Count -gt $states.Count) {
            foreach ($s in $now[$states.Count..($now.Count - 1)]) { Say "client state $s" }
            $states = $now
        }
        if (-not $lobbyAt -and $states -contains 'kGameStateLobby') { $lobbyAt = Get-Date }
        if (-not $dungeonAt -and $states -contains 'kGameStateDungeon') { $dungeonAt = Get-Date; Say 'the client is in the match (kGameStateDungeon)' 'Green' }
        if ($dungeonAt) {
            foreach ($p in $peeks) {
                if (-not $peeksDone[$p] -and ((Get-Date) - $dungeonAt).TotalSeconds -ge $p) {
                    $peeksDone[$p] = $true
                    $out = In-S "peek_$p.txt"
                    & $Python -I (Join-Path $PSScriptRoot 'game_peek.py') $gamePid --out $out 2>&1 | Out-Null
                    $obj = "$(Select-String -Path $out -Pattern '^objects:' -ErrorAction SilentlyContinue | Select-Object -First 1 | ForEach-Object { $_.Line })"
                    $pl = @(Select-String -Path $out -Pattern '^player \d+:' -ErrorAction SilentlyContinue).Count
                    Say "peek at match +$p s: $obj; players $pl"
                    if ($Website -and -not $siteMatchesDone) {  # the admin page lists the running match
                        $siteMatchesDone = $true
                        $siteMatches = Site 'site_matches.txt' @('--name', 'siteadmin', '--password-file', $AdminPwFile, 'matches')
                    }
                }
            }
        }
        Start-Sleep -Seconds 1
    }
    Say "monitor ended (exit $($mon.ExitCode))"

    # 7. the end: Start-Client ends with the game
    if (-not $cli.WaitForExit(30000)) { $fail += 'Start-Client.exe did not end with the game'; Stop-Process -Id $cli.Id -Force -ErrorAction SilentlyContinue }
    else { Say "Start-Client.exe ended (exit $($cli.ExitCode))" }
    # the match host notices the lost client; give it a moment, then stop the server with a Ctrl+C in its console
    Start-Sleep -Seconds 5
    if ($Website) { $null = Site 'site_audit.txt' @('--name', 'siteadmin', '--password-file', $AdminPwFile, 'audit') }
    $helper = Start-Process powershell.exe -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-CtrlCInto', $srv.Id) `
        -WindowStyle Hidden -PassThru -Wait
    $ctrlc = "$(Get-Content "$env:TEMP\dg_ctrlc_$($srv.Id).txt" -ErrorAction SilentlyContinue)"
    Remove-Item "$env:TEMP\dg_ctrlc_$($srv.Id).txt" -ErrorAction SilentlyContinue
    if ($srv.WaitForExit(20000)) { Say "Start-Server.exe ended after the Ctrl+C ($ctrlc; exit $($srv.ExitCode))" }
    else { $fail += "Start-Server.exe did not end on a Ctrl+C ($ctrlc)"; Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue }

    # the capture: the session is over, stop it after a moment (egress_report.py reads a cut last packet)
    Start-Sleep -Seconds 2
    foreach ($d in @(Get-CimInstance Win32_Process -Filter "Name='dumpcap.exe'" | Where-Object { "$($_.CommandLine)".ToLower().Contains($Session.ToLower()) })) {
        Stop-Process -Id $d.ProcessId -Force -ErrorAction SilentlyContinue
    }
} catch {
    $fail += "$($_.Exception.Message)"
    Say "FAILED: $($_.Exception.Message)" 'Red'
} finally {
    if ($fw -and -not $fw.HasExited) { Set-Content -Path (In-S 'focus.stop') -Value 'stop'; [void]$fw.WaitForExit(15000) }
    $left = @(Get-Ours)
    foreach ($p in $left) { Say "cleanup: stopping $($p.Name) pid $($p.ProcessId)" 'Yellow'; Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }
    if ($left.Count) { Start-Sleep -Seconds 2 }
    if ($ownLock -and "$(Get-Content $LockFile -Raw -ErrorAction SilentlyContinue)" -match "pid=$PID ") { Remove-Item $LockFile -Force -ErrorAction SilentlyContinue }
    if ($ownLanLock -and "$(Get-Content $LanLock -Raw -ErrorAction SilentlyContinue)" -match "pid=$PID ") { Remove-Item $LanLock -Force -ErrorAction SilentlyContinue }
}

# collect
if (-not $cliStart) { $cliStart = $t0 }
$appSec = @(Get-AppSection $cliStart)
Set-Content -Path (In-S 'application_log.txt') -Value $appSec -Encoding UTF8
$srvLog = Get-ChildItem (Join-Path $Data 'logs') -File -Filter '*.log' -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTime -ge $t0 } | Sort-Object LastWriteTime | Select-Object -Last 1
if ($srvLog) { Copy-Item $srvLog.FullName (In-S 'server_log.txt') }
$hostDirs = @(Get-ChildItem (Join-Path $Data 'logs\hosts') -Directory -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTime -ge $t0 })
foreach ($h in $hostDirs) { Copy-Item $h.FullName (Join-Path (In-S 'host') $h.Name) -Recurse -Force }

# egress
$egArgs = @('-I', (Join-Path $PSScriptRoot 'egress_report.py'), $Session, '--lane-address', '127.0.0.1')
if ($IsLan) { $egArgs += @('--allow-host', $Address) }
$egOut = & $Python @egArgs 2>&1
$egCode = $LASTEXITCODE
Set-Content -Path (In-S 'egress.txt') -Value $egOut -Encoding UTF8
$egTotal = "$($egOut | Where-Object { "$_" -match '^\d+ flagged line' } | Select-Object -Last 1)"

# the tunnel's capture check (Start-Client's own tunnel: only the gateway's port reached on the server address)
$tunnelLine = "$(Select-String -Path (In-S 'client.txt') -Pattern 'tunnel   to .*' -ErrorAction SilentlyContinue | Select-Object -First 1 | ForEach-Object { $_.Line.Trim() })"
$tcTotal = ''; $tcCode = 0
if ($tunnelLine) {
    $gwPort = if ($iniCfg -and $iniCfg['gateway_port']) { $iniCfg['gateway_port'] } else { '42128' }
    $tcOut = & $Python -I (Join-Path $PSScriptRoot 'tunnel_capture.py') $Session --same-host --server $Address --client 127.0.0.1 --gateway-port $gwPort 2>&1
    $tcCode = $LASTEXITCODE
    Set-Content -Path (In-S 'tunnel_capture.txt') -Value $tcOut -Encoding UTF8
    $tcTotal = "$($tcOut | Where-Object { "$_" -match '^\d+ flagged' } | Select-Object -Last 1)"
}

# cleanup check
$heldAfter = @(); $deadline = (Get-Date).AddSeconds(15)
do { $heldAfter = @(Get-Held); if ($heldAfter.Count) { Start-Sleep -Milliseconds 500 } } while ($heldAfter.Count -and (Get-Date) -lt $deadline)
$leftAfter = @(Get-Ours)
$clean = if ($heldAfter.Count -or $leftAfter.Count) { 'NOT CLEAN: ' + ((@($heldAfter) + @($leftAfter | ForEach-Object { "$($_.Name) pid $($_.ProcessId)" })) -join '; ') } else { 'ports free, no process left' }

# the checks
$sl = if (Test-Path (In-S 'server_log.txt')) { In-S 'server_log.txt' } else { $null }
function First([string]$file, [string]$pat) { if ($file -and (Test-Path $file)) { "$(Select-String -Path $file -Pattern $pat | Select-Object -First 1 | ForEach-Object { $_.Line })" } else { '' } }
function Last([string]$file, [string]$pat) { if ($file -and (Test-Path $file)) { "$(Select-String -Path $file -Pattern $pat | Select-Object -Last 1 | ForEach-Object { $_.Line })" } else { '' } }
$stateList = @($appSec | Where-Object { $_ -match 'transitioning from \S+ -> (\S+)' } | ForEach-Object { ([regex]::Match($_, '-> (\S+)')).Groups[1].Value })
$tls = First $sl '\[redirector\] connection from .*TLS'
if (-not $tls -and $tunnelLine) { $tls = First $sl '\[redirector\] ' }  # through the tunnel the game's TLS 1.1 ends in Start-Client
$loggedIn = First $sl '\[blaze\] account \d+ logged in'
$siteReg = First (In-S 'site.txt') 'register .* on /signup'
$siteInv = First (In-S 'site.txt') 'invite \('
$siteMatch = First (In-S 'site_matches.txt') 'match \d+:'
$siteAudit = @(Select-String -Path (In-S 'site_audit.txt') -Pattern '\|' -ErrorAction SilentlyContinue).Count
$mail = "$(First $sl "MAIL: '")".Trim()
$ready = First $sl 'match \d+: ready on port'
$hostEnd = First $sl '\[host\] match \d+: ended'
$hostLog = @(Get-ChildItem (In-S 'host') -Recurse -Filter dgsim.log -ErrorAction SilentlyContinue | Select-Object -First 1)
$hl = if ($hostLog.Count) { $hostLog[0].FullName } else { $null }
function Cut([string]$s, [int]$n = 260) { $s = ($s -replace '^\s*\d+ \[\s*\d+\] ', '').Trim(); if ($s.Length -gt $n) { $s.Substring(0, $n) + '...' } else { $s } }
$bots = Cut (Last $hl 'stats bots:') 140
$match = Last $hl 'stats match:'
$matchS = if ($match -match 'match ([\d.]+) s') { [double]$Matches[1] } else { 0 }
$waves = if ($match -match 'director: waves (\d+)') { $Matches[1] } else { '?' }
$ticks = Cut (Last $hl 'stats t\+\d+s ticks \d+') 160
$peekFiles = @(Get-ChildItem $Session -Filter 'peek_*.txt' -ErrorAction SilentlyContinue)
$peekObjs = @($peekFiles | ForEach-Object { "$($_.BaseName): $((Select-String -Path $_.FullName -Pattern '^objects:' | Select-Object -First 1).Line)" })
$focusSum = Last (In-S 'focus.log') '^\S+ focus: '
$focusSum = $focusSum -replace '^\S+ ', ''
$fgGame = if ($focusSum -match ' (\d+) to a game window \(confirmed; .* in the foreground in (\d+);') { [int]$Matches[1] + [int]$Matches[2] } else { -1 }
$onDesk = if ($focusSum -match 'on the desktop at a poll (\d+) time') { [int]$Matches[1] } else { -1 }
$parked = @(Select-String -Path (In-S 'monitor.log') -Pattern 'background: window moved' -ErrorAction SilentlyContinue).Count
$backs = @(Select-String -Path (In-S 'client.txt') -Pattern 'the game took the foreground' -ErrorAction SilentlyContinue | ForEach-Object { ($_.Line -replace '^\s*test hook: ', '').Trim() })

if ($MenuOnly) {
    if ($stateList -notcontains 'kGameStateLobby') { $fail += 'the client did not reach the main menu (kGameStateLobby)' }
} else {
    if ($stateList -notcontains 'kGameStateDungeon') { $fail += 'the client did not reach kGameStateDungeon' }
    if (-not $ready) { $fail += 'no gameReady from the match host' }
    if ($matchS -lt 180) { $fail += "the match ran $matchS s (< 180) by the host's last stats match line" }
    if (-not $peekObjs.Count) { $fail += 'no game_peek result' }
}
if (-not $tls) { $fail += 'no TLS connection to the redirector in the server log' }
if ($Website) {
    if ($siteReg -notmatch ': 200 ') { $fail += "the website's registration: $siteReg" }
    if (-not $MenuOnly -and $siteMatch -notmatch '\(account \d+, team \d, in the match\)') { $fail += "the admin page's matches did not list the player in the match ($siteMatch)" }
    if ($mail -notmatch [regex]::Escape("MAIL: '$LoginName'")) { $fail += "the game did not log in as $LoginName ($mail)" }
}
if ($ClientArgs -match '--tunnel\s+on' -and -not $tunnelLine) { $fail += "Start-Client's tunnel did not start (client.txt)" }
if ($tunnelLine -and $tcCode -ne 0) { $fail += "tunnel_capture.py: $tcTotal (tunnel_capture.txt)" }
if ($fgGame -ne 0) { $fail += "the game window had the foreground ($focusSum)" }
if ($clean -like 'NOT CLEAN*') { $fail += $clean }

$egressFlagged = $egCode -eq 1
$code = if ($egressFlagged) { 1 } elseif ($fail.Count) { if ($fail | Where-Object { $_ -match 'not free|did not start|did not get|started no game|NOT CLEAN' }) { 2 } else { 3 } } else { 0 }
$sum = @(
    "==== $Name ($(if ($MenuOnly) { 'main menu' } else { 'practice match' })): $(if ($code -eq 0) { 'PASSED' } elseif ($code -eq 1) { 'EGRESS FLAGGED' } else { 'FAILED' }) (exit $code)",
    "release  : $Root",
    "address  : $Address$(if ($IsLan) { ' (this PC''s LAN address; egress --allow-host it)' })$(if ($keys -and $keys.Count) { '; ini ' + (@($keys.Keys | ForEach-Object { "$_=$($keys[$_])" }) -join ' ') })$(if ($ClientArgs) { "; Start-Client $ClientArgs" })",
    "server   : $(if ($setupLine) { 'setup complete; ' })ready $([math]::Round($srvReady, 1)) s after Start-Server's start; $doneSteps step(s) done, $okSteps current (server.txt)",
    "tls      : $(if ($tls) { Cut ($tls -replace '^\S+ ', '') 120 } else { 'NONE' })",
    "login    : $(if ($mail) { $mail } else { 'no MAIL line' })$(if ($loggedIn) { '; ' + (Cut ($loggedIn -replace '^\S+ ', '') 80) })",
    "tunnel   : $(if ($tunnelLine) { "$tunnelLine; capture: $tcTotal" } else { 'not used' })",
    "website  : $(if ($Website) { "$(Cut ($siteInv -replace '^\[\s*[\d.]+\] ', '') 70); $(Cut ($siteReg -replace '^\[\s*[\d.]+\] ', '') 120); matches: $(Cut ($siteMatch -replace '^\[\s*[\d.]+\] ', '') 140); audit $siteAudit entries" } else { 'not checked' })",
    "client   : states $($stateList -join ' > '); game pid $gamePid",
    "host     : $(if ($ready) { Cut ($ready -replace '^\S+ ', '') 100 } else { 'no gameReady' }); $(if ($hostEnd) { Cut ($hostEnd -replace '^\S+ ', '') 140 } else { 'no end line' })",
    "match    : $matchS s by the host's last stats line, waves $waves; $bots",
    "ticks    : $ticks",
    "peeks    : $(if ($peekObjs.Count) { $peekObjs -join '; ' } else { 'none' })",
    "focus    : $focusSum",
    "parking  : the monitor moved the game window off the desktop $parked time(s) (Start-Client parks it first)",
    "hook     : Start-Client gave the foreground back $($backs.Count) time(s)$(if ($backs.Count) { ': ' + ($backs -join '; ') })",
    "egress   : $(if ($egTotal) { $egTotal } else { "egress_report.py exit $egCode" })",
    "cleanup  : $clean",
    "duration : $([int]((Get-Date) - $t0).TotalSeconds) s"
)
if ($fail.Count) { $sum += "problems : $($fail -join ' | ')" }
Set-Content -Path (In-S 'summary.txt') -Value $sum -Encoding UTF8
Write-Host ''
foreach ($l in $sum) { Write-Host $l -ForegroundColor $(if ($code -eq 0) { 'Green' } else { 'Red' }) }
exit $code
