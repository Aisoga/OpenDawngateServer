<#
  Starts Dawngate.exe from a working copy without the launcher and records what it does:
  packet capture (dumpcap), network endpoints owned by the game process, child processes,
  window screenshots, and the files it writes. Closes the game afterwards.

  The launcher normally creates the named event "Global\Dawngate L2G"; without it Dawngate.exe
  restarts the launcher with -gnlh and exits with -1337. This script creates the event itself.

  Usage (from the repo root):
    powershell -ExecutionPolicy Bypass -File re\scripts\launch_monitor.ps1 `
        -GameDir client\run_854\Dawngate -OutDir re\logs\launchN [-Seconds 120] [-CaptureIfaces <dumpcap ifaces>]
        [-Login 'email|password'] [-LoginAt 12]
  -Login fills the login screen's email and password fields at t = LoginAt seconds and clicks LOGIN
  (B1). It only posts window messages (WM_LBUTTONDOWN/UP, WM_CHAR) to the game window, so the real
  mouse and keyboard are untouched and nothing can go to another window. Field positions are
  fractions of the client area, measured on the 854 login screen (t8.png of session b1_run1).
  Use dummy credentials: the stub logs them in plain text.
  -Actions (B8) is a timed list of further inputs, also posted window messages only:
    '<t>:<fx>,<fy>'   left click at fractions of the client area at t seconds
    '<t>:r<fx>,<fy>'  right click there (WM_RBUTTONDOWN/UP; D7-live: the in-game move and attack button)
    '<t>:d<fx1>,<fy1>,<fx2>,<fy2>' drag with the left button from point 1 to point 2 (WM_LBUTTONDOWN at 1, 12
                      WM_MOUSEMOVEs with MK_LBUTTON over 0.6 s, WM_LBUTTONUP at 2; B7b: a perk shape onto the perk page)
    '<t>:h<fx>,<fy>'  hover there: 6 WM_MOUSEMOVEs (no button) over 0.6 s, then after 1 s a screenshot h<t>.png
                      (D3-perks: a tooltip, e.g. the shaper panel's perk icon in game)
    '<t>:key=<vk>'    key press (WM_KEYDOWN/WM_KEYUP, virtual-key code in decimal, e.g. 27 = Escape)
    '<t>:key=<m>+<vk>' key press with modifier m held (17 Ctrl, 16 Shift, 18 Alt): the modifier is set in the game
                      thread's key state only (AttachThreadInput + SetKeyboardState), never on the real keyboard
    '<t>:keydown=<vk>' / '<t>:keyup=<vk>' (G3b) a key held from one t to a later one (WM_KEYDOWN, then WM_KEYUP), e.g.
                      Tab for the in-game scoreboard with a screenshot in between (the shot at a t comes before its actions)
    '<t>:text=<chars>' (A5b) type letters, digits and _ @ . - (since D8b also /, for chat commands such as /surrender)
                      as posted WM_CHARs into whatever has the game's keyboard focus (click the text field first, e.g.
                      the PvP lobby invite dialog's name field)
    '<t>:wheel=<delta>[x<n>]' n posted WM_MOUSEWHEELs (default 1) of <delta> (120 = one notch away from the user,
                      -120 towards) at the window's centre, 250 ms apart: the in-game camera's zoom
    '<t>:shot'        screenshot only
    '<t>:front' / '<t>:back' (G3) bring the game window in front (on-screen and the foreground, as a focused
                      screenshot does) and keep it there until 'back' gives the foreground back, e.g. around an in-game
                      chat line (key=13, text=..., key=13); the user allowed test sessions to take the focus (2026-10-08)
  separated by ';', e.g. '24:key=27;28:0.4984,0.0833'. t must be even (the watch loop steps by 2 s);
  several actions at the same t run in order. A screenshot a<t>.png is taken before the first action
  of each t. Fractions are measured on the screenshots (they show the whole client area).
  -ShotTimes overrides the default screenshot times (comma-separated seconds, even).
  -Background (D7-live) keeps the game window off the desktop: every 200 ms the monitor moves it to (-30000, -30000)
  and the bottom of the z-order without activating it (SetWindowPos with SWP_NOACTIVATE, posted). The game
  shows its splash itself in the screen's centre for 1-3 s while it loads (nothing outside it can move it then) and
  its main window for a moment. Unless shots are focused, the foreground is also locked while the user has
  been idle past ForegroundLockTimeout (the game took it then in a test run) and given back if the game takes it anyway
  (GuardForeground). Its screenshots come out black or frozen unless -ShotFocus on (below).
  -ShotFocus auto|on|off (V1): PrintWindow gets new frames only for the parts of the game window that lie on a monitor
  (V1-live; the client itself keeps rendering in the background, docs/RUNNING_THE_CLIENT.md *Screenshots and
  rendering in the background (V1)*). With 'on' every screenshot first brings the game window on-screen (from
  -Background's parking spot to where the game last put it, wholly on that monitor) and to the foreground, waits
  -ShotFocusMs (500), captures it, then parks it again (with -Background) and gives the foreground back to the window
  that had it; -Login then also fills in the form with the window in front. 'auto' (default) is 'on' without
  -Background and 'off' with it; 'off' is the old capture.
  The user allowed test sessions to take the focus (2026-10-08). Nothing moves the mouse; the only synthesized input is
  an Alt key press when Windows refuses the plain foreground switch (re\scripts\window_shot.cs, its test
  re\scripts\test_window_shot.ps1). A one-colour PrintWindow result with the window in front is replaced by the screen
  area. The log line says how: '(window)' or '(screen)', 'focus=plain|attached|alt|already|failed', 'back=...'.
  In game, the client takes its move and aim points from the real cursor (GetCursorPos), not from posted mouse
  messages: right-clicks and aimed casts posted here go wherever the user's pointer is. Use them only for UI
  clicks; send moves and aimed casts from the test side (the match host's [host] commands_file).
  -Seconds 0 (P1, play mode) watches the game until it exits, with no time limit and no closing at the end; the
  capture then gets an 8-hour safety autostop instead of 1.15 * Seconds + 40 s, and the caller (run_session.ps1
  -Play) stops it once the server is down. -ShotTimes none takes no timed screenshots. -SnapLen <bytes> keeps only
  that much of each packet (dumpcap -s; play mode uses 256: every header the egress check reads, and the capture of a
  long session stays small). -NoCapture starts no dumpcap at all (play mode only; no egress check is possible then).
  -KeepAlive <s> (B12) posts a lone WM_KEYUP of VK_F24 (0x87: no key on a standard keyboard, bound to nothing) to the
  game window every <s> seconds (even; 0 = off), so the client's AFK kick (no input for AFKThreshhold 4 min +
  AFKDisconnectThreshhold 1 min in a game: GMS disconnect, logout, login screen) never fires in a long unattended
  session. Any key-up reaches the client's AFK listener (GameClient +0x6150, slot 2 = 0x0066C280), which restarts the
  4-minute stopwatch (docs/RUNNING_THE_CLIENT.md *The AFK kick and the keep-alive (B12)*). Like every other input here
  it is a posted window message: the real keyboard, the cursor and the focus stay untouched (no SendInput,
  SetCursorPos or SetForegroundWindow).
  Typing (-Login's form, text=) is preceded by a posted WM_ACTIVATEAPP (TRUE) and WM_ACTIVATE (WA_ACTIVE) (G3b): a game
  window that was active once and then lost the foreground drops posted WM_CHARs, and these two messages tell its
  window procedure it is active again (the real foreground stays as it is). -NoTypeActivate leaves them out.
  -GameArgs '<arguments>' (A6) is the game's command line, e.g. '-multipleInstances' for the clients of lanes b, c and
  d, which run beside lane a's (lane a's client holds the Global\Dawngate mutex; docs/RUNNING_THE_CLIENT.md *Test
  lanes (A6, A6b)*).
  -Attach (the release's test hook): start nothing; wait up to -AttachWait seconds (90) for a Dawngate.exe whose
  path is <GameDir>\game\Dawngate.exe (started by the release's Start-Client.exe, which holds Global\Dawngate L2G
  itself) and watch, log in, click and close that one as if this script had started it. With Start-Client.exe
  --test-park the game window is kept off the desktop by Start-Client too; pass -Background here as well.
  Never point -GameDir at client\ia_*: those extracted builds stay pristine.
#>
param(
    [Parameter(Mandatory)] [string]$GameDir,
    [Parameter(Mandatory)] [string]$OutDir,
    [string]$CaptureIfaces,   # comma-separated dumpcap interface names; default: default-route NIC + loopback
    [int]$Seconds = 120,
    [string]$Login,           # 'email|password' to type into the login screen (optional)
    [int]$LoginAt = 12,       # seconds after start; must be even (the watch loop steps by 2 s)
    [string]$Actions,         # timed clicks/keys after login (see above)
    [string]$ShotTimes,       # screenshot times, e.g. '8,20,40', or 'none'
    [switch]$Background,      # keep the game window off the desktop (see above)
    [ValidateSet('auto', 'on', 'off')][string]$ShotFocus = 'auto',  # bring the game in front for each screenshot (V1)
    [int]$ShotFocusMs = 500,  # wait after bringing it in front, before the capture
    [int]$SnapLen = 0,        # dumpcap -s: bytes kept per packet (0 = whole packets)
    [switch]$NoCapture,       # no dumpcap (play mode only)
    [int]$KeepAlive = 0,      # B12: post a key-up of VK_F24 every N s against the AFK kick (even; 0 = off)
    [string]$GameArgs,        # the game's command line (A6: '-multipleInstances' for lanes b-d)
    [switch]$NoTypeActivate,  # G3b: no posted WM_ACTIVATEAPP/WM_ACTIVATE before -Login's and text='s typing
    [switch]$Attach,          # attach to the game Start-Client.exe started from -GameDir instead of starting it
    [int]$AttachWait = 90     # seconds -Attach waits for that game to appear
)
$ErrorActionPreference = 'Continue'
if ($KeepAlive -lt 0 -or $KeepAlive % 2) { throw "-KeepAlive $KeepAlive must be 0 or even (the watch loop steps by 2 s)" }
$GameDir = (Resolve-Path $GameDir).Path
if ($GameDir -match '\\client\\ia_') { throw "refusing to run the pristine extracted build; make a working copy first" }
New-Item -ItemType Directory -Force $OutDir | Out-Null
$OutDir = (Resolve-Path $OutDir).Path
if (-not $CaptureIfaces) {
    $route = Get-NetRoute -DestinationPrefix 0.0.0.0/0 | Sort-Object RouteMetric | Select-Object -First 1
    $nic = Get-NetAdapter -InterfaceIndex $route.ifIndex
    $CaptureIfaces = "\Device\NPF_$($nic.InterfaceGuid),\Device\NPF_Loopback"
}
$log = Join-Path $OutDir 'monitor.log'
function Log($m) { $line = "{0:HH:mm:ss.fff} {1}" -f (Get-Date), $m; Add-Content -Path $log -Value $line -Encoding utf8 }

Add-Type -AssemblyName System.Drawing
Add-Type @"
using System; using System.Runtime.InteropServices;
public static class W {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
  // the foreground guard of -Background (GuardForeground below)
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool IsWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool LockSetForegroundWindow(uint code);
  [StructLayout(LayoutKind.Sequential)] public struct LASTINPUTINFO { public uint cbSize; public uint dwTime; }
  [DllImport("user32.dll")] static extern bool GetLastInputInfo(ref LASTINPUTINFO l);
  [DllImport("user32.dll")] static extern bool SystemParametersInfoW(uint action, uint p, out uint v, uint w);
  public static uint IdleMs() { LASTINPUTINFO l = new LASTINPUTINFO(); l.cbSize = 8; GetLastInputInfo(ref l); return (uint)Environment.TickCount - l.dwTime; }
  public static uint LockTimeoutMs() { uint v; return SystemParametersInfoW(0x2000, 0, out v, 0) ? v : 0; }  // SPI_GETFOREGROUNDLOCKTIMEOUT
  [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
  [DllImport("user32.dll")] public static extern bool GetKeyboardState(byte[] s);
  [DllImport("user32.dll")] public static extern bool SetKeyboardState(byte[] s);
  // D7-live: a key with a modifier (Ctrl+Q ranks up Q). The game reads modifiers with GetKeyState, which posted
  // messages don't change; attached to the game window's thread, SetKeyboardState sets the modifier in that thread's
  // key state only (not the real keyboard, not GetAsyncKeyState) while the posted key is processed.
  public static string PressWithModifier(IntPtr h, int mod, int vk) {
    uint pid; uint game = GetWindowThreadProcessId(h, out pid); uint me = GetCurrentThreadId();
    bool attached = AttachThreadInput(me, game, true);
    byte[] ks = new byte[256]; GetKeyboardState(ks);
    int side = mod == 0x11 ? 0xA2 : mod == 0x10 ? 0xA0 : mod == 0x12 ? 0xA4 : mod;
    ks[mod] |= 0x80; ks[side] |= 0x80; SetKeyboardState(ks);
    PostMessage(h, 0x0100, (IntPtr)mod, (IntPtr)1); System.Threading.Thread.Sleep(60);
    PostMessage(h, 0x0100, (IntPtr)vk, (IntPtr)1); System.Threading.Thread.Sleep(80);
    PostMessage(h, 0x0101, (IntPtr)vk, unchecked((IntPtr)(int)0xC0000001)); System.Threading.Thread.Sleep(400);
    PostMessage(h, 0x0101, (IntPtr)mod, unchecked((IntPtr)(int)0xC0000001)); System.Threading.Thread.Sleep(60);
    ks[mod] &= 0x7F; ks[side] &= 0x7F; SetKeyboardState(ks);
    if (attached) AttachThreadInput(me, game, false);
    return attached ? "attached" : "NOT attached";
  }
}
"@
# V1: the screenshot helper (capture, foreground switch, parking), shared with re\scripts\test_window_shot.ps1
Add-Type -Path (Join-Path $PSScriptRoot 'window_shot.cs') -ReferencedAssemblies System.Drawing
$FocusShots = $ShotFocus -eq 'on' -or ($ShotFocus -eq 'auto' -and -not $Background)
# where a -Background window goes for a focused screenshot: where it was before the first parking (else near the corner)
$HomeX = 40; $HomeY = 40; $HomeKnown = $false
Log "screenshots: $(if ($FocusShots) { "the game window is brought in front for each one (-ShotFocus $ShotFocus, wait $ShotFocusMs ms)" } else { "taken where the window is (-ShotFocus $ShotFocus)" })"
# login screen controls as fractions of the client area (x, y), measured on the 854 login screen
$LoginFields = @{ email = @(0.2266, 0.5486); password = @(0.2266, 0.6292); button = @(0.3438, 0.7083) }
function ClickAt($h, $fx, $fy) {
    $r = New-Object W+RECT
    [void][W]::GetClientRect($h, [ref]$r)
    $x = [int](($r.R - $r.L) * $fx); $y = [int](($r.B - $r.T) * $fy)
    $lp = [IntPtr](($y -shl 16) -bor ($x -band 0xFFFF))
    [void][W]::PostMessage($h, 0x0200, [IntPtr]0, $lp)   # WM_MOUSEMOVE
    Start-Sleep -Milliseconds 150
    [void][W]::PostMessage($h, 0x0201, [IntPtr]1, $lp)   # WM_LBUTTONDOWN (MK_LBUTTON)
    Start-Sleep -Milliseconds 80
    [void][W]::PostMessage($h, 0x0202, [IntPtr]0, $lp)   # WM_LBUTTONUP
    Start-Sleep -Milliseconds 300
    return "($x,$y)"
}
function RightClickAt($h, $fx, $fy) {
    $r = New-Object W+RECT
    [void][W]::GetClientRect($h, [ref]$r)
    $x = [int](($r.R - $r.L) * $fx); $y = [int](($r.B - $r.T) * $fy)
    $lp = [IntPtr](($y -shl 16) -bor ($x -band 0xFFFF))
    [void][W]::PostMessage($h, 0x0200, [IntPtr]0, $lp)   # WM_MOUSEMOVE
    Start-Sleep -Milliseconds 150
    [void][W]::PostMessage($h, 0x0204, [IntPtr]2, $lp)   # WM_RBUTTONDOWN (MK_RBUTTON)
    Start-Sleep -Milliseconds 80
    [void][W]::PostMessage($h, 0x0205, [IntPtr]0, $lp)   # WM_RBUTTONUP
    Start-Sleep -Milliseconds 300
    return "($x,$y)"
}
function DragFromTo($h, $fx1, $fy1, $fx2, $fy2) {
    # B7b: a left-button drag in posted messages only (Scaleform takes the points from the messages, as for clicks)
    $r = New-Object W+RECT
    [void][W]::GetClientRect($h, [ref]$r)
    $w = $r.R - $r.L; $hgt = $r.B - $r.T
    $x1 = [int]($w * $fx1); $y1 = [int]($hgt * $fy1); $x2 = [int]($w * $fx2); $y2 = [int]($hgt * $fy2)
    $lp1 = [IntPtr](($y1 -shl 16) -bor ($x1 -band 0xFFFF))
    [void][W]::PostMessage($h, 0x0200, [IntPtr]0, $lp1)   # WM_MOUSEMOVE
    Start-Sleep -Milliseconds 150
    [void][W]::PostMessage($h, 0x0201, [IntPtr]1, $lp1)   # WM_LBUTTONDOWN (MK_LBUTTON)
    Start-Sleep -Milliseconds 150
    $steps = 12
    for ($i = 1; $i -le $steps; $i++) {
        $x = [int]($x1 + ($x2 - $x1) * $i / $steps); $y = [int]($y1 + ($y2 - $y1) * $i / $steps)
        [void][W]::PostMessage($h, 0x0200, [IntPtr]1, [IntPtr](($y -shl 16) -bor ($x -band 0xFFFF)))   # WM_MOUSEMOVE, MK_LBUTTON
        Start-Sleep -Milliseconds 50
    }
    Start-Sleep -Milliseconds 150
    [void][W]::PostMessage($h, 0x0202, [IntPtr]0, [IntPtr](($y2 -shl 16) -bor ($x2 -band 0xFFFF)))   # WM_LBUTTONUP
    Start-Sleep -Milliseconds 300
    return "($x1,$y1) -> ($x2,$y2)"
}
function HoverAt($h, $fx, $fy) {
    # D3-perks: posted mouse moves only (no button), as Scaleform takes the clicks' points from the messages
    $r = New-Object W+RECT
    [void][W]::GetClientRect($h, [ref]$r)
    $x = [int](($r.R - $r.L) * $fx); $y = [int](($r.B - $r.T) * $fy)
    for ($i = 0; $i -lt 6; $i++) {
        $xi = $x - 3 + $i
        [void][W]::PostMessage($h, 0x0200, [IntPtr]0, [IntPtr](($y -shl 16) -bor ($xi -band 0xFFFF)))   # WM_MOUSEMOVE
        Start-Sleep -Milliseconds 100
    }
    Start-Sleep -Milliseconds 1000
    return "($x,$y)"
}
function WheelAt($h, [int]$delta, [int]$n) {
    # posted WM_MOUSEWHEELs (no real mouse): wParam's high word the delta, lParam the point in screen coordinates
    # (the window's centre where it is now)
    $r = New-Object W+RECT
    [void][W]::GetWindowRect($h, [ref]$r)
    $x = [int](($r.L + $r.R) / 2); $y = [int](($r.T + $r.B) / 2)
    $wp = [IntPtr]([int](($delta -band 0xFFFF) -shl 16))
    for ($i = 0; $i -lt $n; $i++) {
        [void][W]::PostMessage($h, 0x020A, $wp, [IntPtr](($y -shl 16) -bor ($x -band 0xFFFF)))   # WM_MOUSEWHEEL
        Start-Sleep -Milliseconds 250
    }
    return "($x,$y)"
}
function PressKey($h, [int]$vk) {
    [void][W]::PostMessage($h, 0x0100, [IntPtr]$vk, [IntPtr]1)            # WM_KEYDOWN
    Start-Sleep -Milliseconds 80
    [void][W]::PostMessage($h, 0x0101, [IntPtr]$vk, [IntPtr]0xC0000001)   # WM_KEYUP
    Start-Sleep -Milliseconds 300
}
# -KeepAlive (B12): a lone key-up of VK_F24. The window procedure (EACanvas_Win32, 0x00765B90) turns WM_KEYUP into a key
# event without a focus check; the app's OnKeyUp (0x00AF97D0) hands it to every input listener, the client's AFK
# listener among them, which restarts the AFK stopwatch and lets the event pass. No key-down: nothing can stay pressed.
$KeepAlivePosts = 0
function KeepAliveKey($p, $at) {
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -eq [IntPtr]::Zero) { return }
    [void][W]::PostMessage($h, 0x0101, [IntPtr]0x87, [IntPtr]0xC0000001)   # WM_KEYUP, VK_F24
    $script:KeepAlivePosts++
    if ($script:KeepAlivePosts -le 3) { Log "keepalive t=${at}: WM_KEYUP VK_F24 posted (#$($script:KeepAlivePosts))" }
}
# -Actions: t -> list of 'fx,fy' | 'rfx,fy' | 'hfx,fy' | 'dfx1,fy1,fx2,fy2' | 'key=<vk>' | 'text=<chars>' | 'shot'
$ActionsAt = @{}
if ($Actions) {
    foreach ($a in $Actions.Split(';')) {
        $a = $a.Trim(); if (-not $a) { continue }
        if ($a -notmatch '^(\d+):(.+)$') { throw "bad -Actions item '$a'" }
        $at = [int]$Matches[1]; $what = $Matches[2].Trim()
        if ($at % 2) { throw "-Actions time $at must be even" }
        if ($what -notmatch '^([rh]?\d*\.?\d+,\d*\.?\d+|d\d*\.?\d+,\d*\.?\d+,\d*\.?\d+,\d*\.?\d+|key=\d+(\+\d+)?|keydown=\d+|keyup=\d+|wheel=-?\d+(x\d+)?|text=[A-Za-z0-9_@./-]+|shot|front|back)$') { throw "bad -Actions item '$a'" }
        if (-not $ActionsAt.ContainsKey($at)) { $ActionsAt[$at] = @() }
        $ActionsAt[$at] += $what
    }
}
function DoActions($p, $at) {
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -eq [IntPtr]::Zero) { Log "actions t=${at}: no main window"; return }
    Shot $p "a$at"
    foreach ($what in $ActionsAt[$at]) {
        if ($what -eq 'shot') { continue }
        if ($what -eq 'front') {
            # G3: the window in front (on-screen, the foreground) until 'back', e.g. while a chat line is typed: a
            # deactivated window takes no posted WM_CHAR (V1). No mouse move, no real key (window_shot.cs's switch).
            $m = $false; $pv = [IntPtr]::Zero
            $how = [WindowShot]::Prepare($h, $script:HomeX, $script:HomeY, $true, [ref]$m, [ref]$pv)
            $script:FrontMoved = $m; $script:FrontPrev = $pv; $script:InFront = $true
            Start-Sleep -Milliseconds 300
            Log "action t=${at}: window in front ($how$(if ($m) { ",on-screen$([WindowShot]::LastPlaced)" }))"
            continue
        }
        if ($what -eq 'back') {
            if ($script:InFront) { Log "action t=${at}: back=$([WindowShot]::Restore($h, $script:FrontMoved, $script:FrontPrev))"; $script:InFront = $false }
            continue
        }
        if ($what -match '^key=(\d+)$') { PressKey $h ([int]$Matches[1]); Log "action t=${at}: key $($Matches[1])"; continue }
        # G3b: a key held across screenshots (the in-game scoreboard shows while Tab is down): keydown=<vk> ... keyup=<vk>
        if ($what -match '^keydown=(\d+)$') { [void][W]::PostMessage($h, 0x0100, [IntPtr][int]$Matches[1], [IntPtr]1); Log "action t=${at}: key $($Matches[1]) down"; continue }
        if ($what -match '^keyup=(\d+)$') { [void][W]::PostMessage($h, 0x0101, [IntPtr][int]$Matches[1], [IntPtr]0xC0000001); Log "action t=${at}: key $($Matches[1]) up"; continue }
        if ($what -match '^wheel=(-?\d+)(x(\d+))?$') {
            $n = if ($Matches[3]) { [int]$Matches[3] } else { 1 }
            Log "action t=${at}: wheel $($Matches[1]) x$n at $(WheelAt $h ([int]$Matches[1]) $n)"; continue
        }
        if ($what -match '^text=(.+)$') { PostActivate $h; TypeText $h $Matches[1]; Log "action t=${at}: typed $($Matches[1].Length) chars$(if (-not $NoTypeActivate) { ' (after a posted activation)' })"; continue }
        if ($what -match '^key=(\d+)\+(\d+)$') {
            $how = [W]::PressWithModifier($h, [int]$Matches[1], [int]$Matches[2])
            Log "action t=${at}: key $($Matches[2]) with modifier $($Matches[1]) (key state $how)"; continue
        }
        if ($what -match '^d(.+),(.+),(.+),(.+)$') {
            $ic = [Globalization.CultureInfo]::InvariantCulture
            $pos = DragFromTo $h ([double]::Parse($Matches[1], $ic)) ([double]::Parse($Matches[2], $ic)) ([double]::Parse($Matches[3], $ic)) ([double]::Parse($Matches[4], $ic))
            Log "action t=${at}: dragged ($what) $pos"; continue
        }
        if ($what -match '^h(.+),(.+)$') {
            $pos = HoverAt $h ([double]::Parse($Matches[1], [Globalization.CultureInfo]::InvariantCulture)) ([double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture))
            Shot $p "h$at"
            Log "action t=${at}: hovered ($what) at $pos, screenshot h$at"; continue
        }
        if ($what -match '^r(.+),(.+)$') {
            $pos = RightClickAt $h ([double]::Parse($Matches[1], [Globalization.CultureInfo]::InvariantCulture)) ([double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture))
            Log "action t=${at}: right-clicked ($what) at $pos"; continue
        }
        $fx, $fy = $what.Split(',')
        $pos = ClickAt $h ([double]::Parse($fx, [Globalization.CultureInfo]::InvariantCulture)) ([double]::Parse($fy, [Globalization.CultureInfo]::InvariantCulture))
        Log "action t=${at}: clicked ($what) at $pos"
    }
}
function PostActivate($h) {
    # G3b: a game window that was activated once and then lost the foreground (to another lane's focused screenshot, a
    # second client's new window) takes no posted WM_CHAR (V1-live, g3b_run1b: both e-mails arrived empty). Its window
    # procedure tracks activation from WM_ACTIVATE (0x00765400) and WM_ACTIVATEAPP (0x007654B0: wParam != 0 sends the
    # UI 0x01EE1006), so posting both (active) before the typing tells the game it is active again; the real
    # foreground, the keyboard and the mouse stay untouched. -NoTypeActivate leaves it out.
    if ($NoTypeActivate) { return }
    [void][W]::PostMessage($h, 0x001C, [IntPtr]1, [IntPtr]0)   # WM_ACTIVATEAPP (TRUE)
    [void][W]::PostMessage($h, 0x0006, [IntPtr]1, [IntPtr]0)   # WM_ACTIVATE (WA_ACTIVE)
    Start-Sleep -Milliseconds 60
}
function TypeText($h, $text) {
    foreach ($c in $text.ToCharArray()) {
        [void][W]::PostMessage($h, 0x0102, [IntPtr][int]$c, [IntPtr]1)   # WM_CHAR
        Start-Sleep -Milliseconds 40
    }
}
function DoLogin($p, $cred) {
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -eq [IntPtr]::Zero) { Log "login: no main window"; return }
    # V1-live: once a focused screenshot has activated the window and given the foreground back, the login form's text
    # fields take no posted WM_CHAR any more (v1b12_run1-run5, d7b_run2: dgserver logged MAIL '' and the client logged in
    # with an empty email, or not at all); typed while never activated, or while in front, they arrive. So with -ShotFocus
    # on the window is in front while the form is filled in, then goes back as after a screenshot.
    $moved = $false; $prev = [IntPtr]::Zero; $front = ''
    if ($FocusShots) {
        $how = [WindowShot]::Prepare($h, $script:HomeX, $script:HomeY, $true, [ref]$moved, [ref]$prev)
        $front = " (window in front: $how$(if ($moved) { ",on-screen$([WindowShot]::LastPlaced)" }))"
        Start-Sleep -Milliseconds 300
    }
    $email, $pass = $cred.Split('|', 2)
    PostActivate $h  # G3b
    $at = ClickAt $h $LoginFields.email[0] $LoginFields.email[1]; TypeText $h $email
    Log "login: clicked email field at $at, typed $($email.Length) chars$front"
    $at = ClickAt $h $LoginFields.password[0] $LoginFields.password[1]; TypeText $h $pass
    Log "login: clicked password field at $at, typed $($pass.Length) chars"
    Shot $p 'login_filled'
    $at = ClickAt $h $LoginFields.button[0] $LoginFields.button[1]
    Log "login: clicked LOGIN at $at"
    if ($FocusShots) { Log "login: back=$([WindowShot]::Restore($h, $moved, $prev))" }
}
$BackgroundMoves = 0
function KeepInBackground($p) {
    if (-not $Background) { return }
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -eq [IntPtr]::Zero) { return }
    $r = New-Object W+RECT
    [void][W]::GetWindowRect($h, [ref]$r)
    if ($r.L -le -20000) { return }
    # V1-live: the home for focused shots is where the game last put its window itself (its own centring after
    # creation, the in-game borderless 1920x1080 window at (0,0)), not only the first spot: d7b_run2 brought the in-game
    # window to the front end's first spot (704,392), where only the part on the monitor came back (window_shot.cs
    # FitOnMonitor now also keeps the whole window on that monitor)
    if (-not $script:HomeKnown -or $script:HomeX -ne $r.L -or $script:HomeY -ne $r.T) {
        if ($script:HomeKnown) { Log "background: shot home now ($($r.L),$($r.T)), $($r.R - $r.L)x$($r.B - $r.T) (the game moved its window)" }
        $script:HomeX = $r.L; $script:HomeY = $r.T; $script:HomeKnown = $true
    }
    # HWND_BOTTOM, SWP_NOSIZE | SWP_NOACTIVATE | SWP_ASYNCWINDOWPOS (a plain SetWindowPos waited while the game's
    # thread loaded without handling window messages, 3 s at its splash in a test run, and held up this loop meanwhile)
    [void][W]::SetWindowPos($h, [IntPtr]1, -30000, -30000, 0, 0, 0x4011)
    $script:BackgroundMoves++
    if ($script:BackgroundMoves -le 10) { Log "background: window moved off the desktop from ($($r.L),$($r.T)) (#$($script:BackgroundMoves))" }
}
# With -Background (and no focused shots) the game must not take the foreground either. Windows lets it whenever
# the user has been idle longer than ForegroundLockTimeout (200 s; in a test run the release's game took it at its splash,
# when shaper select opened and twice in it). So while the user is away the foreground is locked
# (LockSetForegroundWindow; with it every try of the game was refused, its taskbar button flashed instead), released as
# soon as the user is back; and a game window that has the foreground anyway gets it taken back for the window that had
# it before (once per take). The release's Start-Client.exe --test-park does the same every 50 ms.
$FgPrev = [IntPtr]::Zero; $FgTried = $false; $FgLocked = $false; $FgBacks = 0
function GuardForeground($p) {
    if (-not $Background -or $FocusShots -or $script:InFront) { return }
    $timeout = [W]::LockTimeoutMs()
    if ($timeout -gt 0 -and [W]::IdleMs() -ge $timeout) {
        if (-not $script:FgLocked -and [W]::LockSetForegroundWindow(1)) { $script:FgLocked = $true; Log 'background: the user is idle past ForegroundLockTimeout: foreground locked' }
    } elseif ($script:FgLocked) {
        [void][W]::LockSetForegroundWindow(2); $script:FgLocked = $false; Log 'background: the user is back: foreground lock released'
    }
    $fg = [W]::GetForegroundWindow()
    if ($fg -eq [IntPtr]::Zero) { return }
    $fpid = [uint32]0; [void][W]::GetWindowThreadProcessId($fg, [ref]$fpid)
    if ($fpid -ne $p.Id) { $script:FgPrev = $fg; $script:FgTried = $false; return }
    if ($script:FgTried -or $script:FgPrev -eq [IntPtr]::Zero -or -not [W]::IsWindow($script:FgPrev)) { return }
    $ok = [W]::SetForegroundWindow($script:FgPrev); $script:FgTried = $true; $script:FgBacks++
    if ($script:FgBacks -le 10) { Log "background: the game took the foreground (#$($script:FgBacks)); given back to the window that had it: $ok" }
}
function Shot($p, $name) {
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -eq [IntPtr]::Zero) { Log "screenshot ${name}: no main window"; return }
    # The window's own content (PrintWindow with PW_RENDERFULLCONTENT = 2, Windows 8.1+). It has new frames only where the
    # window lies on a monitor (V1-live), so with -ShotFocus on the window comes on-screen and to the foreground first. If
    # PrintWindow fails, comes back black, or comes back one flat colour with the window in front, the screen area is
    # copied instead (re\scripts\window_shot.cs).
    $focus = ''; $moved = $false; $prev = [IntPtr]::Zero
    if ($FocusShots) {
        $how = [WindowShot]::Prepare($h, $script:HomeX, $script:HomeY, $true, [ref]$moved, [ref]$prev)
        $focus = " focus=$how$(if ($moved) { ",on-screen$([WindowShot]::LastPlaced)" })"
        Start-Sleep -Milliseconds $ShotFocusMs
    }
    $file = Join-Path $OutDir "$name.png"
    $w = 0; $hgt = 0
    try { $how = [WindowShot]::Save($h, $file, 1280, [ref]$w, [ref]$hgt) } catch { $how = "failed: $($_.Exception.Message)" }
    # the previous foreground window by its class only (no titles of the user's windows in the log)
    if ($FocusShots) { $focus += " back=$([WindowShot]::Restore($h, $moved, $prev))$(if ($prev -ne [IntPtr]::Zero -and $prev -ne $h) { " ($([WindowShot]::ClassOf($prev)))" })" }
    if ($how -eq 'empty') { Log "screenshot ${name}: empty rect$focus"; return }
    Log "screenshot ${name}: ${w}x${hgt} ($how)$focus title='$($p.MainWindowTitle)' -> $file"
}

# 1. packet capture (stops on its own). The watch loop below takes about 1.1 * Seconds (polling
#    overhead), plus up to 15 s for closing the game and a few seconds for the file scan.
#    -Seconds 0 (play mode): an 8-hour safety autostop; run_session.ps1 -Play stops it at the end.
if ($NoCapture) {
    Log "no capture (-NoCapture)"
} else {
    $pcap = Join-Path $OutDir 'capture.pcapng'
    $dumpArgs = @()
    foreach ($i in $CaptureIfaces.Split(',')) { $dumpArgs += @('-i', $i) }
    $autostop = if ($Seconds -le 0) { 8 * 3600 + 40 } else { [int]($Seconds * 1.15) + 40 }
    if ($SnapLen -gt 0) { $dumpArgs += @('-s', $SnapLen) }
    $dumpArgs += @('-a', "duration:$autostop", '-w', $pcap, '-q')
    $dump = Start-Process 'C:\Program Files\Wireshark\dumpcap.exe' -ArgumentList $dumpArgs -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 3
    Log "dumpcap pid=$($dump.Id) -> $pcap (autostop $autostop s$(if ($SnapLen -gt 0) { ", $SnapLen bytes per packet" }))"
}

# 2. play the launcher's part: Dawngate.exe exits with -1337 unless the named event
#    "Global\Dawngate L2G" exists (it opens it, SetEvent()s it, and carries on)
$start = Get-Date
$exe = Join-Path $GameDir 'game\Dawngate.exe'
if ($Attach) {
    # the release's Start-Client.exe started the game and holds the event; find that game by its path
    $l2g = $null
    $p = $null
    $deadline = (Get-Date).AddSeconds($AttachWait)
    while (-not $p -and (Get-Date) -lt $deadline) {
        $found = @(Get-CimInstance Win32_Process -Filter "Name = 'Dawngate.exe'" -ErrorAction SilentlyContinue |
            Where-Object { "$($_.ExecutablePath)" -ieq $exe })
        if ($found.Count) { $p = Get-Process -Id $found[0].ProcessId -ErrorAction SilentlyContinue }
        if (-not $p) { Start-Sleep -Milliseconds 250 }
    }
    if (-not $p) { Log "attach: no $exe appeared within $AttachWait s"; throw "-Attach: no $exe appeared within $AttachWait s" }
    $null = $p.Handle  # Windows PowerShell 5.1: the exit code needs the handle opened while the game runs
    Log "attached to $exe pid=$($p.Id) (started by another program, -Attach)"
} else {
    $l2g = New-Object System.Threading.EventWaitHandle($false, [System.Threading.EventResetMode]::ManualReset, 'Global\Dawngate L2G')
    Log "created event Global\Dawngate L2G"
    # start the game from the working copy
    $startArgs = @{ FilePath = $exe; WorkingDirectory = (Join-Path $GameDir 'game'); PassThru = $true }
    if ($GameArgs) { $startArgs.ArgumentList = $GameArgs }
    $p = Start-Process @startArgs
    Log "started $exe pid=$($p.Id)$(if ($GameArgs) { " args '$GameArgs'" })"
}

# 3. watch: network endpoints owned by the game, child processes, window, screenshots
$seen = @{}
$shots = @(8, 20, 40, 70, 100, 130)
if ($ShotTimes -eq 'none') { $shots = @() }
elseif ($ShotTimes) { $shots = @($ShotTimes.Split(',') | Where-Object { $_.Trim() } | ForEach-Object { [int]$_ }) }
function PollEndpoints($t) {
    foreach ($c in Get-NetTCPConnection -OwningProcess $p.Id -ErrorAction SilentlyContinue) {
        $k = "TCP $($c.LocalAddress):$($c.LocalPort) -> $($c.RemoteAddress):$($c.RemotePort) $($c.State)"
        if (-not $seen[$k]) { $seen[$k] = 1; Log "t=${t}s $k" }
    }
    foreach ($u in Get-NetUDPEndpoint -OwningProcess $p.Id -ErrorAction SilentlyContinue) {
        $k = "UDP bound $($u.LocalAddress):$($u.LocalPort)"
        if (-not $seen[$k]) { $seen[$k] = 1; Log "t=${t}s $k" }
    }
}
if ($Seconds -le 0) { Log "no time limit (-Seconds 0): watching until the game exits" }
for ($t = 0; $Seconds -le 0 -or $t -le $Seconds; $t += 2) {
    if ($p.HasExited) { Log "game exited at t=${t}s code=$($p.ExitCode)"; break }
    PollEndpoints $t
    foreach ($ch in Get-CimInstance Win32_Process -Filter "ParentProcessId=$($p.Id)" -ErrorAction SilentlyContinue) {
        $k = "child $($ch.ProcessId) $($ch.Name) $($ch.CommandLine)"
        if (-not $seen[$k]) { $seen[$k] = 1; Log "t=${t}s $k" }
    }
    if (-not $l2gSeen -and $l2g -and $l2g.WaitOne(0)) { $l2gSeen = $true; Log "t=${t}s game signalled Global\Dawngate L2G" }
    $p.Refresh()
    $k = "window '$($p.MainWindowTitle)' responding=$($p.Responding)"
    if (-not $seen[$k]) { $seen[$k] = 1; Log "t=${t}s $k" }
    if ($shots -contains $t) { Shot $p "t$t" }
    if ($Login -and $t -eq $LoginAt) { DoLogin $p $Login }
    if ($ActionsAt.ContainsKey($t)) { DoActions $p $t }
    if ($KeepAlive -gt 0 -and $t -gt 0 -and $t % $KeepAlive -eq 0) { KeepAliveKey $p $t }
    # poll the game's endpoints several times per step so short-lived connections are caught too
    # (an HTTP request to a local stub can still finish between two polls; the capture has those)
    $stepEnd = (Get-Date).AddSeconds(2)
    while ((Get-Date) -lt $stepEnd -and -not $p.HasExited) { PollEndpoints $t; KeepInBackground $p; GuardForeground $p; Start-Sleep -Milliseconds 200 }
}
if ($KeepAlive -gt 0) { Log "keepalive: $KeepAlivePosts key-up(s) posted (every $KeepAlive s)" }
if ($FgLocked) { [void][W]::LockSetForegroundWindow(2); Log 'background: foreground lock released (end)' }
if ($Background -and -not $FocusShots) { Log "background: the game took the foreground $FgBacks time(s) (given back each time)" }

# 4. close the game (gracefully first)
if (-not $p.HasExited) {
    Shot $p 'final'
    Log "closing game"
    [void]$p.CloseMainWindow()
    if (-not $p.WaitForExit(15000)) { Log "force-killing game"; Stop-Process -Id $p.Id -Force }
}
foreach ($ch in Get-Process -Name CrashSender1402 -ErrorAction SilentlyContinue) { Log "crash sender running pid=$($ch.Id) (left open for the user)" }

# 5. files the game wrote: working copy and per-user folders
Log "--- files written since start"
$pattern = '(?i)dawngate|battlegrounds|waystone|sporelabs|crashrpt|electronic arts|^ea\b'
$roots = @(Get-Item $GameDir)
foreach ($base in @($env:APPDATA, $env:LOCALAPPDATA, [Environment]::GetFolderPath('MyDocuments'), $env:TEMP)) {
    # only folders that look game-related, up to two levels deep, so the scan stays fast
    $roots += Get-ChildItem $base -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -match $pattern }
    $roots += Get-ChildItem $base -Directory -ErrorAction SilentlyContinue | ForEach-Object {
        Get-ChildItem $_.FullName -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -match $pattern } }
    # loose files directly in the base folder (crash dumps, temp logs)
    Get-ChildItem $base -File -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTime -ge $start -and $_.Name -match $pattern } |
        ForEach-Object { Log ("{0} ({1} bytes)" -f $_.FullName, $_.Length) }
}
foreach ($r in $roots) {
    Get-ChildItem $r.FullName -Recurse -File -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -ge $start -and $_.FullName -notmatch '\\Temp\\claude\\' } |
        ForEach-Object { Log ("{0} ({1} bytes)" -f $_.FullName, $_.Length) }
}
Get-ChildItem 'HKCU:\Software\Electronic Arts' -Recurse -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -match '(?i)dawngate|battlegrounds|waystone' } | ForEach-Object { Log "reg: $($_.Name)" }
if ($l2g) { $l2g.Dispose() }
Log "done"
