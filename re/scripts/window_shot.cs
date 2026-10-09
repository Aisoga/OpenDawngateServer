// V1: screenshots of the game window while another window has the focus (roadmap V1, docs/RUNNING_THE_CLIENT.md
// *Screenshots and rendering in the background (V1)*). Loaded with Add-Type -Path by launch_monitor.ps1 (-ShotFocus) and by its offline test
// re/scripts/test_window_shot.ps1. C# 5 (the .NET Framework compiler of Windows PowerShell 5.1); keep it ASCII.
//
// Why: the windowed client keeps rendering and presenting while unfocused or off the desktop (V1-static's reading of
// the render path), but PrintWindow gets new frames only for the parts of the window that lie on a monitor (V1-live:
// parked by -Background it comes back black or frozen; covered or unfocused on-screen it is fine). Since
// 2026-10-08 the user allows test sessions to take the focus, so a shot can bring the window on-screen and to the
// foreground first, capture it, and give the foreground back. No mouse input and no cursor move, ever; the only
// synthesized input is the Alt-key fallback that Windows' foreground rules may need (BringToFront, allowAlt).
using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;

public static class WindowShot {
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
    [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] static extern bool PrintWindow(IntPtr h, IntPtr hdc, uint flags);
    [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] static extern bool BringWindowToTop(IntPtr h);
    [DllImport("user32.dll")] static extern bool IsIconic(IntPtr h);
    [DllImport("user32.dll")] static extern bool IsWindow(IntPtr h);
    [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr h, int cmd);
    [DllImport("user32.dll")] static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a, uint b, bool attach);
    [DllImport("user32.dll")] static extern bool IsGUIThread(bool convert);
    [DllImport("user32.dll")] static extern void keybd_event(byte vk, byte scan, uint flags, UIntPtr extra);
    [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
    [StructLayout(LayoutKind.Sequential)] struct POINT { public int X, Y; }
    [StructLayout(LayoutKind.Sequential)] struct MONITORINFO { public int cbSize; public RECT rcMonitor, rcWork; public uint dwFlags; }
    [DllImport("user32.dll")] static extern IntPtr MonitorFromPoint(POINT pt, uint flags);
    [DllImport("user32.dll")] static extern bool GetMonitorInfo(IntPtr mon, ref MONITORINFO mi);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr h, System.Text.StringBuilder s, int n);

    // launch_monitor.ps1 -Background parks the window at (-30000, -30000); anything left of this counts as parked.
    public const int OffDesktop = -20000;
    public const int ParkedX = -30000, ParkedY = -30000;
    const uint PW_RENDERFULLCONTENT = 2;
    const uint SWP_NOSIZE = 0x0001, SWP_NOACTIVATE = 0x0010, SWP_SHOWWINDOW = 0x0040;
    const int SW_RESTORE = 9;
    const byte VK_MENU = 0x12;
    const uint KEYEVENTF_KEYUP = 0x0002;
    const int Black = unchecked((int)0xFF000000);

    public static bool IsForeground(IntPtr h) { return h != IntPtr.Zero && GetForegroundWindow() == h; }
    public static IntPtr Foreground() { return GetForegroundWindow(); }

    public static RECT Rect(IntPtr h) {
        RECT r;
        if (!GetWindowRect(h, out r)) { r.L = r.T = r.R = r.B = 0; }
        return r;
    }

    // Makes h the foreground window. Tries, in order: SetForegroundWindow; the same while this thread is attached to
    // the current foreground window's input queue (AttachThreadInput); and, with allowAlt, the same with the Alt key
    // held (keybd_event: Windows lets the process that sent the last input set the foreground; the key-up then goes to
    // h, whose window procedure handles WM_SYSKEYUP itself, so no menu opens). Returns already, plain, attached, alt or
    // failed.
    public static string BringToFront(IntPtr h, bool allowAlt) {
        if (h == IntPtr.Zero || !IsWindow(h)) { return "failed"; }
        if (IsForeground(h)) { return "already"; }
        IsGUIThread(true);  // AttachThreadInput needs a message queue on this thread
        if (IsIconic(h)) { ShowWindow(h, SW_RESTORE); }
        if (SetForegroundWindow(h) && IsForeground(h)) { return "plain"; }
        uint pid;
        IntPtr fg = GetForegroundWindow();
        uint fgThread = fg != IntPtr.Zero ? GetWindowThreadProcessId(fg, out pid) : 0;
        uint me = GetCurrentThreadId();
        bool attached = fgThread != 0 && fgThread != me && AttachThreadInput(me, fgThread, true);
        BringWindowToTop(h);
        SetForegroundWindow(h);
        if (attached) { AttachThreadInput(me, fgThread, false); }
        if (IsForeground(h)) { return "attached"; }
        if (!allowAlt) { return "failed"; }
        keybd_event(VK_MENU, 0, 0, UIntPtr.Zero);
        SetForegroundWindow(h);
        keybd_event(VK_MENU, 0, KEYEVENTF_KEYUP, UIntPtr.Zero);
        return IsForeground(h) ? "alt" : "failed";
    }

    // Where a w x h window placed at (x, y) lies wholly on the monitor that holds (x, y) (the nearest one if none):
    // shifted left/up just enough, never past the monitor's top-left corner. V1-live: PrintWindow returns black for the
    // parts of the game window that aren't on that monitor (d7b_run2: the in-game 1920x1080 borderless window brought to
    // the front-end window's spot (704,392) came back with only its top-left 1216x688, the part on the primary monitor,
    // which looked like a stale frame because the HUD was cut off).
    public static void FitOnMonitor(int x, int y, int w, int h, out int fx, out int fy) {
        POINT pt; pt.X = x; pt.Y = y;
        MONITORINFO mi = new MONITORINFO();
        mi.cbSize = Marshal.SizeOf(typeof(MONITORINFO));
        IntPtr mon = MonitorFromPoint(pt, 2);  // MONITOR_DEFAULTTONEAREST
        if (mon == IntPtr.Zero || !GetMonitorInfo(mon, ref mi)) { fx = x; fy = y; return; }
        RECT m = mi.rcMonitor;
        fx = Math.Max(m.L, Math.Min(x, m.R - w));
        fy = Math.Max(m.T, Math.Min(y, m.B - h));
    }

    // The window class of h ("" if none); launch_monitor.ps1 logs the previous foreground window by class, not title.
    public static string ClassOf(IntPtr h) {
        if (h == IntPtr.Zero) { return ""; }
        System.Text.StringBuilder s = new System.Text.StringBuilder(128);
        return GetClassName(h, s, s.Capacity) > 0 ? s.ToString() : "";
    }

    // Where Prepare put the window last ("" if it didn't move it), for the monitor's log line.
    public static string LastPlaced = "";

    // Before a shot: if h is parked off the desktop, moves it to (homeX, homeY), fitted onto that point's monitor
    // (FitOnMonitor; moved = true), then brings it to the front. previous is the window that had the foreground, for
    // Restore.
    public static string Prepare(IntPtr h, int homeX, int homeY, bool allowAlt, out bool moved, out IntPtr previous) {
        previous = GetForegroundWindow();
        moved = false;
        LastPlaced = "";
        if (h == IntPtr.Zero || !IsWindow(h)) { return "failed"; }
        RECT r = Rect(h);
        if (r.L <= OffDesktop) {
            int x, y;
            FitOnMonitor(homeX, homeY, r.R - r.L, r.B - r.T, out x, out y);
            SetWindowPos(h, IntPtr.Zero, x, y, 0, 0, SWP_NOSIZE | SWP_SHOWWINDOW);
            moved = true;
            LastPlaced = "(" + x + "," + y + ")";
        }
        return BringToFront(h, allowAlt);
    }

    // After a shot: parks h off the desktop again if Prepare moved it (bottom of the z-order, not activated), and gives
    // the foreground back to previous (no Alt fallback here). Returns parked/-, then the foreground result.
    public static string Restore(IntPtr h, bool moved, IntPtr previous) {
        string how = "";
        if (moved && h != IntPtr.Zero && IsWindow(h)) {
            SetWindowPos(h, new IntPtr(1), ParkedX, ParkedY, 0, 0, SWP_NOSIZE | SWP_NOACTIVATE);
            how = "parked,";
        }
        if (previous == IntPtr.Zero || previous == h || !IsWindow(previous)) { return how + "kept"; }
        return how + "previous " + BringToFront(previous, false);
    }

    // The capture decision from PrintWindow's result and nine sample pixels (ARGB): the window's own picture unless
    // PrintWindow failed, came back black everywhere (the D7-live -Background symptom), or came back as one flat colour
    // while the window is in front (the white D7-live symptom; with the window in front the screen area is the game).
    public static string Judge(bool printed, int[] samples, bool foreground) {
        if (!printed || samples == null || samples.Length == 0) { return "screen"; }
        bool allBlack = true, flat = true;
        foreach (int s in samples) {
            if (s != Black) { allBlack = false; }
            if (s != samples[0]) { flat = false; }
        }
        if (allBlack) { return "screen"; }
        if (flat && foreground) { return "screen"; }
        return flat ? "window,flat" : "window";
    }

    // Captures h's window rectangle into a PNG at most maxWidth wide: PrintWindow(PW_RENDERFULLCONTENT), or the screen
    // area when Judge says so. width/height are the window's size. Returns window, window,flat, screen or empty.
    public static string Save(IntPtr h, string file, int maxWidth, out int width, out int height) {
        RECT r = Rect(h);
        width = r.R - r.L;
        height = r.B - r.T;
        if (width <= 0 || height <= 0) { return "empty"; }
        string how;
        using (Bitmap bmp = new Bitmap(width, height, PixelFormat.Format32bppArgb)) {
            using (Graphics g = Graphics.FromImage(bmp)) {
                IntPtr hdc = g.GetHdc();
                bool printed;
                try { printed = PrintWindow(h, hdc, PW_RENDERFULLCONTENT); } finally { g.ReleaseHdc(hdc); }
                int[] samples = new int[9];
                int i = 0;
                foreach (double fx in new double[] { 0.25, 0.5, 0.75 }) {
                    foreach (double fy in new double[] { 0.25, 0.5, 0.75 }) {
                        samples[i++] = bmp.GetPixel((int)((width - 1) * fx), (int)((height - 1) * fy)).ToArgb();
                    }
                }
                how = Judge(printed, samples, IsForeground(h));
                if (how == "screen") { g.CopyFromScreen(r.L, r.T, 0, 0, new Size(width, height)); }
            }
            double scale = Math.Min(1.0, (double)maxWidth / width);
            using (Bitmap outBmp = new Bitmap(Math.Max(1, (int)(width * scale)), Math.Max(1, (int)(height * scale)))) {
                using (Graphics g2 = Graphics.FromImage(outBmp)) {
                    g2.InterpolationMode = InterpolationMode.HighQualityBicubic;
                    g2.DrawImage(bmp, 0, 0, outBmp.Width, outBmp.Height);
                }
                outBmp.Save(file, ImageFormat.Png);
            }
        }
        return how;
    }
}
