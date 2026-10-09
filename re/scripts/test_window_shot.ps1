<#
  Offline test of re/scripts/window_shot.cs (V1), the screenshot helper of launch_monitor.ps1 -ShotFocus.
  Needs no game: part 1 checks the capture decision (WindowShot.Judge); part 2 runs the whole -ShotFocus path on a
  window of its own (a WinForms form in a known colour, parked off the desktop like -Background's game window):
  Prepare brings it on-screen and to the front, Save captures it, the PNG's pixels must be the form's colour, and
  Restore parks it again and gives the foreground back. Part 2 takes the focus for about a second (allowed for test
  sessions since 2026-10-08); -NoWindow skips it. Never moves the mouse.
  -Monitor adds part 3 (about 25 s): launch_monitor.ps1 itself, -NoCapture -Background -ShotFocus on, against a stand-in
  game (a small WinForms exe compiled into a temp folder as game\Dawngate.exe: a window titled Dawngate in two known
  colours); the monitor must park it, take its screenshot from the foreground (log line and PNG colours) and close it.

  Usage: powershell -ExecutionPolicy Bypass -File re\scripts\test_window_shot.ps1 [-NoWindow] [-Monitor]
  Exit code 0 = all checks passed, 1 = a check failed. This file must stay ASCII (Windows PowerShell 5.1).
#>
param([switch]$NoWindow, [switch]$Monitor)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type -Path (Join-Path $PSScriptRoot 'window_shot.cs') -ReferencedAssemblies System.Drawing
$failed = 0
function Check($what, $ok, $detail) {
    if ($ok) { Write-Host "ok   $what" } else { Write-Host "FAIL $what $detail" -ForegroundColor Red; $script:failed++ }
}

# --- part 1: the capture decision
$black = [Drawing.Color]::FromArgb(255, 0, 0, 0).ToArgb()   # ARGB as Bitmap.GetPixel().ToArgb() returns it
$white = [Drawing.Color]::White.ToArgb()
$mixed = @($black, $white, $black, $white, $black, $white, $black, $white, $black)
Check 'Judge: PrintWindow failed -> screen' ([WindowShot]::Judge($false, $mixed, $false) -eq 'screen')
Check 'Judge: all black -> screen' ([WindowShot]::Judge($true, [int[]](@($black) * 9), $false) -eq 'screen')
Check 'Judge: flat white, in front -> screen' ([WindowShot]::Judge($true, [int[]](@($white) * 9), $true) -eq 'screen')
Check 'Judge: flat white, behind -> window,flat' ([WindowShot]::Judge($true, [int[]](@($white) * 9), $false) -eq 'window,flat')
Check 'Judge: a picture -> window' ([WindowShot]::Judge($true, [int[]]$mixed, $true) -eq 'window')
Check 'Judge: no samples -> screen' ([WindowShot]::Judge($true, [int[]]@(), $true) -eq 'screen')
# V1-live: a parked window comes back wholly on the monitor of its home point (d7b_run2's in-game window was cut off)
$pb = [Windows.Forms.Screen]::PrimaryScreen.Bounds
$fx = 0; $fy = 0
[WindowShot]::FitOnMonitor($pb.X + 704, $pb.Y + 392, $pb.Width, $pb.Height, [ref]$fx, [ref]$fy)
Check 'FitOnMonitor: a monitor-sized window goes to the corner' ($fx -eq $pb.X -and $fy -eq $pb.Y) "got ($fx,$fy)"
[WindowShot]::FitOnMonitor($pb.X + 100, $pb.Y + 100, 320, 200, [ref]$fx, [ref]$fy)
Check 'FitOnMonitor: a window that fits stays' ($fx -eq $pb.X + 100 -and $fy -eq $pb.Y + 100) "got ($fx,$fy)"
[WindowShot]::FitOnMonitor($pb.X + $pb.Width - 100, $pb.Y + 50, 400, 300, [ref]$fx, [ref]$fy)
Check 'FitOnMonitor: a window over the right edge moves left' ($fx -eq $pb.X + $pb.Width - 400 -and $fy -eq $pb.Y + 50) "got ($fx,$fy)"

# --- part 2: a real window through Prepare / Save / Restore
if (-not $NoWindow) {
    $colour = [Drawing.Color]::FromArgb(255, 0x30, 0xA0, 0x50)
    $form = New-Object Windows.Forms.Form
    $form.Text = 'test_window_shot'
    $form.FormBorderStyle = 'None'
    $form.ShowInTaskbar = $false
    $form.BackColor = $colour
    $form.StartPosition = 'Manual'
    $form.Location = New-Object Drawing.Point ([WindowShot]::ParkedX), ([WindowShot]::ParkedY)
    $form.Size = New-Object Drawing.Size 320, 200
    # a second colour under one of the nine sample points, so the picture isn't flat and PrintWindow's path is taken
    $panelColour = [Drawing.Color]::FromArgb(255, 0xC0, 0x40, 0x20)
    $panel = New-Object Windows.Forms.Panel
    $panel.BackColor = $panelColour
    $panel.Location = New-Object Drawing.Point 0, 0
    $panel.Size = New-Object Drawing.Size 120, 80
    $form.Controls.Add($panel)
    $before = [WindowShot]::Foreground()
    $form.Show()
    $h = $form.Handle
    # give the foreground back to whoever had it, so the form starts as -Background's game window does: parked, behind
    if ($before -ne [IntPtr]::Zero) { [void][WindowShot]::BringToFront($before, $false) }
    function Pump($ms) { $end = (Get-Date).AddMilliseconds($ms); while ((Get-Date) -lt $end) { [Windows.Forms.Application]::DoEvents(); Start-Sleep -Milliseconds 20 } }
    Pump 200
    $file = Join-Path ([IO.Path]::GetTempPath()) ("test_window_shot_{0}.png" -f [Diagnostics.Process]::GetCurrentProcess().Id)
    try {
        Check 'form starts parked off the desktop' ([WindowShot]::Rect($h).L -le [WindowShot]::OffDesktop)
        $moved = $false; $prev = [IntPtr]::Zero
        $how = [WindowShot]::Prepare($h, 120, 120, $true, [ref]$moved, [ref]$prev)
        Pump 300
        Check "Prepare brings it to the front ($how)" ([WindowShot]::IsForeground($h)) "foreground is $([WindowShot]::Foreground())"
        Check 'Prepare moves it on-screen' ($moved -and [WindowShot]::Rect($h).L -eq 120) "rect L=$([WindowShot]::Rect($h).L)"
        $w = 0; $hh = 0
        $shot = [WindowShot]::Save($h, $file, 1280, [ref]$w, [ref]$hh)
        Check "Save captures it with PrintWindow ($shot, ${w}x${hh})" ($shot -eq 'window' -and $w -eq 320 -and $hh -eq 200)
        $bmp = New-Object Drawing.Bitmap $file
        $px = @($bmp.GetPixel(160, 100), $bmp.GetPixel(300, 180), $bmp.GetPixel(20, 20))
        $bmp.Dispose()
        function Near($c, $want) { [Math]::Abs($c.R - $want.R) -le 2 -and [Math]::Abs($c.G - $want.G) -le 2 -and [Math]::Abs($c.B - $want.B) -le 2 }
        $ok = (Near $px[0] $colour) -and (Near $px[1] $colour) -and (Near $px[2] $panelColour)
        Check 'the PNG shows the form' $ok ("pixels " + (($px | ForEach-Object { '{0},{1},{2}' -f $_.R, $_.G, $_.B }) -join ' '))
        # the flat case: with the window in front, a one-colour PrintWindow result is replaced by the screen area
        $panel.Visible = $false
        Pump 150
        $shot2 = [WindowShot]::Save($h, $file, 1280, [ref]$w, [ref]$hh)
        $bmp = New-Object Drawing.Bitmap $file
        $c2 = $bmp.GetPixel(20, 20)
        $bmp.Dispose()
        Check "a flat picture in front comes from the screen ($shot2)" ($shot2 -eq 'screen' -and (Near $c2 $colour)) ('{0},{1},{2}' -f $c2.R, $c2.G, $c2.B)
        $back = [WindowShot]::Restore($h, $moved, $prev)
        Pump 200
        Check "Restore parks it again ($back)" ([WindowShot]::Rect($h).L -le [WindowShot]::OffDesktop)
        if ($prev -ne [IntPtr]::Zero -and $prev -ne $h) {
            Check 'Restore gives the foreground back' ([WindowShot]::Foreground() -eq $prev) "foreground is $([WindowShot]::Foreground()), was $prev"
        } else {
            Write-Host 'skip Restore foreground check (no previous foreground window)'
        }
    } finally {
        $form.Close(); $form.Dispose()
        if (Test-Path $file) { Remove-Item $file -Force }
    }
}
# --- part 3: launch_monitor.ps1 against a stand-in game
if ($Monitor) {
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("test_window_shot_monitor_{0}" -f [Diagnostics.Process]::GetCurrentProcess().Id)
    $gameDir = Join-Path $tmp 'game'
    New-Item -ItemType Directory -Force $gameDir | Out-Null
    $exe = Join-Path $gameDir 'Dawngate.exe'
    $src = @"
using System; using System.Drawing; using System.Windows.Forms;
public static class StandInGame {
    [STAThread] public static void Main() {
        Form f = new Form();
        f.Text = "Dawngate";
        f.BackColor = Color.FromArgb(255, 0x30, 0xA0, 0x50);
        f.StartPosition = FormStartPosition.Manual;
        f.Location = new Point(200, 200);
        f.ClientSize = new Size(640, 360);
        Panel p = new Panel();
        p.BackColor = Color.FromArgb(255, 0xC0, 0x40, 0x20);
        p.Location = new Point(0, 0);
        p.Size = new Size(240, 140);
        f.Controls.Add(p);
        Timer t = new Timer();
        t.Interval = 90000;
        t.Tick += delegate { f.Close(); };
        t.Start();
        Application.Run(f);
    }
}
"@
    Add-Type -TypeDefinition $src -Language CSharp -OutputAssembly $exe -OutputType WindowsApplication `
        -ReferencedAssemblies System.Windows.Forms, System.Drawing
    $out = Join-Path $tmp 'out'
    $mon = Join-Path $PSScriptRoot 'launch_monitor.ps1'
    try {
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $mon -GameDir $tmp -OutDir $out -Seconds 6 -NoCapture `
            -CaptureIfaces none -Background -ShotFocus on -ShotTimes 4 | Out-Null
        $log = @(Get-Content (Join-Path $out 'monitor.log'))
        Check 'monitor parked the stand-in off the desktop' (@($log | Where-Object { $_ -match 'background: window moved off the desktop' }).Count -ge 1)
        $line = @($log | Where-Object { $_ -match 'screenshot t4: ' })[0]
        Check 'monitor took t4 in front and on-screen' ($line -match '\(window\) focus=(plain|attached|alt|already),on-screen\(-?\d+,-?\d+\) back=parked') "$line"
        Write-Host "     monitor.log: $(($line -split ' ', 2)[1])"
        $png = Join-Path $out 't4.png'
        if (Test-Path $png) {
            $bmp = New-Object Drawing.Bitmap $png
            $c1 = $bmp.GetPixel([int]($bmp.Width * 0.5), [int]($bmp.Height * 0.6)); $c2 = $bmp.GetPixel([int]($bmp.Width * 0.2), [int]($bmp.Height * 0.3))
            $bmp.Dispose()
            $ok = [Math]::Abs($c1.G - 0xA0) -le 2 -and [Math]::Abs($c1.R - 0x30) -le 2 -and [Math]::Abs($c2.R - 0xC0) -le 2 -and [Math]::Abs($c2.G - 0x40) -le 2
            Check 't4.png shows the stand-in' $ok ('{0},{1},{2} / {3},{4},{5}' -f $c1.R, $c1.G, $c1.B, $c2.R, $c2.G, $c2.B)
        } else { Check 't4.png written' $false }
        Check 'monitor closed the stand-in' (@($log | Where-Object { $_ -match 'game exited|closing game' }).Count -ge 1 -and
            -not (Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe }))
    } finally {
        Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | Stop-Process -Force
        Start-Sleep -Milliseconds 300
        Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}
if ($failed) { Write-Host "$failed check(s) failed" -ForegroundColor Red; exit 1 }
Write-Host 'all checks passed' -ForegroundColor Green
exit 0
