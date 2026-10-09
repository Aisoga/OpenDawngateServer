<#
  Checks play_lan.ps1's Ctrl+C path live (roadmap P2-live) without the real keyboard: runs re\scripts\test_p2.ps1
  -Live in a hidden console of its own (so play_lan.ps1 has a console input and [Console]::TreatControlCAsInput works;
  under a redirected stdin the Ctrl+C path can't run at all), waits until the lan test copy's Application.log
  (%APPDATA%\DawngateDataL) shows kGameStateDungeon, then writes one Ctrl+C key event into that console's input buffer
  (a child of this script in -InjectInto mode: FreeConsole, AttachConsole, WriteConsoleInput; no SendInput, no focus
  change, nothing on the real keyboard; P1's p1_ctrlc1 did the same by hand). play_lan.ps1 reads it as a key, closes the
  game (stopped after 10 s in a match) and ends in order; dgserver and the monitor, which share that console, get no
  console signal (a dgserver that ended early would fail test_p2.ps1's check). The check's own -Seconds stays as a
  fallback (default 400 here, so the Ctrl+C comes first).

  Usage:
    powershell -ExecutionPolicy Bypass -File re\scripts\test_p2_ctrlc.ps1 -Lane b [-Name p2live_ctrlc2b] [-Seconds 400] [-AfterDungeon 20]
    powershell -ExecutionPolicy Bypass -File re\scripts\test_p2_ctrlc.ps1 -InjectInto <pid>   # one Ctrl+C into <pid>'s console
  Prints the times of the steps; the check's console output goes to re\logs\<Name>_server\console.txt (a transcript).
  Read re\logs\<Name>\summary.txt: 'play : ended by Ctrl+C'. Exit code: test_p2.ps1's (0 passed), 2 if the client
  never reached the match (no Ctrl+C sent) or the injection failed.
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [ValidateSet('', 'b', 'c', 'd')][string]$Lane = '',
    [string]$Name = 'p2live_ctrlc',
    [int]$Seconds = 400,
    [int]$AfterDungeon = 20,
    [int]$InjectInto = 0
)
$ErrorActionPreference = 'Stop'

if ($InjectInto) {
    # this process's own console is freed first: attach to the target's and write Ctrl+C (key down, key up)
    Add-Type @"
using System; using System.Runtime.InteropServices;
public static class DgConsoleCtrlC {
  [StructLayout(LayoutKind.Explicit, CharSet = CharSet.Unicode)]
  public struct INPUT_RECORD {
    [FieldOffset(0)] public ushort EventType;
    [FieldOffset(4)] public int bKeyDown;
    [FieldOffset(8)] public ushort wRepeatCount;
    [FieldOffset(10)] public ushort wVirtualKeyCode;
    [FieldOffset(12)] public ushort wVirtualScanCode;
    [FieldOffset(14)] public char UnicodeChar;
    [FieldOffset(16)] public uint dwControlKeyState;
  }
  [DllImport("kernel32.dll", SetLastError = true)] public static extern bool FreeConsole();
  [DllImport("kernel32.dll", SetLastError = true)] public static extern bool AttachConsole(uint pid);
  [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  public static extern IntPtr CreateFileW(string name, uint access, uint share, IntPtr sa, uint disp, uint flags, IntPtr tmpl);
  [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
  public static extern bool WriteConsoleInputW(IntPtr h, INPUT_RECORD[] buf, uint len, out uint written);
  public static string CtrlC(uint pid) {
    FreeConsole();
    if (!AttachConsole(pid)) return "AttachConsole failed " + Marshal.GetLastWin32Error();
    IntPtr h = CreateFileW("CONIN$", 0xC0000000, 3, IntPtr.Zero, 3, 0, IntPtr.Zero);
    if (h == new IntPtr(-1)) { int e = Marshal.GetLastWin32Error(); FreeConsole(); return "CONIN$ failed " + e; }
    var recs = new INPUT_RECORD[2];
    for (int i = 0; i < 2; i++) {
      recs[i].EventType = 1; recs[i].bKeyDown = i == 0 ? 1 : 0; recs[i].wRepeatCount = 1;
      recs[i].wVirtualKeyCode = 0x43; recs[i].wVirtualScanCode = 0x2E; recs[i].UnicodeChar = (char)3;
      recs[i].dwControlKeyState = 0x0008;  // LEFT_CTRL_PRESSED
    }
    uint n; bool ok = WriteConsoleInputW(h, recs, 2, out n);
    FreeConsole();
    return ok ? ("written " + n) : ("WriteConsoleInput failed " + Marshal.GetLastWin32Error());
  }
}
"@
    $r = [DgConsoleCtrlC]::CtrlC([uint32]$InjectInto)
    # the console is gone: the result goes to the file the caller names in DG_CTRLC_OUT
    if ($env:DG_CTRLC_OUT) { Set-Content -Path $env:DG_CTRLC_OUT -Value $r -Encoding ASCII }
    exit $(if ($r -like 'written 2') { 0 } else { 2 })
}

if (-not $Lane) { Write-Host '-Lane b|c|d (a free test lane, as for test_p2.ps1 -Live)' -ForegroundColor Red; exit 2 }
if ($Name -notmatch '^[\w.-]+$') { Write-Host "invalid -Name '$Name'" -ForegroundColor Red; exit 2 }
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$MainRoot = $RepoRoot
if (-not (Test-Path (Join-Path $RepoRoot 'client'))) {
    try {
        $common = "$(& git -C $RepoRoot rev-parse --path-format=absolute --git-common-dir 2>$null)".Trim()
        if ($common) { $cand = Split-Path ($common -replace '/', '\') -Parent; if (Test-Path (Join-Path $cand 'client')) { $MainRoot = $cand } }
    } catch { }
}
$srvDir = Join-Path $MainRoot "re\logs\${Name}_server"
if ((Test-Path $srvDir) -or (Test-Path (Join-Path $MainRoot "re\logs\$Name"))) { Write-Host "session folders re\logs\$Name(_server) exist already; pick another -Name" -ForegroundColor Red; exit 2 }
$appLog = Join-Path $env:APPDATA 'DawngateDataL\Application.log'
function Read-AppLogFrom([long]$off) {
    try {
        $fs = [IO.File]::Open($appLog, 'Open', 'Read', 'ReadWrite')
        try { if ($off -gt $fs.Length) { $off = 0 }; [void]$fs.Seek($off, 'Begin'); (New-Object IO.StreamReader($fs)).ReadToEnd() } finally { $fs.Dispose() }
    } catch { '' }
}
function Say([string]$m) { Write-Host ('{0:HH:mm:ss} {1}' -f (Get-Date), $m) }
$start = try { (Get-Item $appLog -ErrorAction Stop).Length } catch { 0 }
# the transcript goes to a scratch file first (test_p2.ps1 wants new session folders), then into the server folder
$tr = Join-Path $env:TEMP "dg_${Name}_console.txt"
$testP2 = Join-Path $PSScriptRoot 'test_p2.ps1'
$cmd = "Start-Transcript -Path '$tr' -Force | Out-Null; & '$testP2' -Live -Lane $Lane -Name $Name -Seconds $Seconds; `$c = `$LASTEXITCODE; Stop-Transcript | Out-Null; exit `$c"
if ($cmd -match '"') { Write-Host 'paths with a double quote are not supported' -ForegroundColor Red; exit 2 }
$p = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', "`"$cmd`"") -WindowStyle Hidden -PassThru
$null = $p.Handle  # keeps ExitCode readable (Windows PowerShell quirk)
Say "test_p2.ps1 -Live -Lane $Lane -Name $Name -Seconds $Seconds in a hidden console: pid $($p.Id)"
$deadline = (Get-Date).AddSeconds(360)
$inGame = $false
while (-not $p.HasExited -and (Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 2
    if ((Read-AppLogFrom $start) -match 'transitioning from \S+ -> kGameStateDungeon') { $inGame = $true; break }
}
$injected = $false
if (-not $inGame) { Say "no kGameStateDungeon in $appLog (check ended: $($p.HasExited)); no Ctrl+C sent" }
else {
    Say "the client is in kGameStateDungeon; Ctrl+C in $AfterDungeon s"
    Start-Sleep -Seconds $AfterDungeon
    $env:DG_CTRLC_OUT = Join-Path $env:TEMP "dg_${Name}_ctrlc.txt"
    $i = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-InjectInto', $p.Id) -WindowStyle Hidden -Wait -PassThru
    $res = "$(Get-Content $env:DG_CTRLC_OUT -Raw -ErrorAction SilentlyContinue)".Trim()
    Remove-Item $env:DG_CTRLC_OUT -ErrorAction SilentlyContinue
    $injected = $i.ExitCode -eq 0
    Say "Ctrl+C written into pid $($p.Id)'s console: $res"
}
$p.WaitForExit()
if (Test-Path $srvDir) { Move-Item $tr (Join-Path $srvDir 'console.txt') -Force -ErrorAction SilentlyContinue }
$end = "$(Get-Content (Join-Path $MainRoot "re\logs\$Name\summary.txt") -ErrorAction SilentlyContinue | Where-Object { $_ -match '^play ' })"
Say "test_p2.ps1 exit $($p.ExitCode); $(if ($end) { $end } else { 'no player summary' }); console: $srvDir\console.txt"
if (-not $inGame -or -not $injected) { exit 2 }
exit $p.ExitCode
