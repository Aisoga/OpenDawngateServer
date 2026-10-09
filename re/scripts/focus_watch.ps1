<#
  Logs whether a game window takes the focus or shows on the desktop (the user's rule of 2026-10-09: test
  runs must not take the focus). It watches every Dawngate.exe whose path lies under -Under (e.g. a release's
  Dawngate-Data folder: the player copy and the match-host copy) and writes -Out:
    - every foreground change (EVENT_SYSTEM_FOREGROUND, an out-of-context WinEvent hook: also changes shorter than a
      poll), with the new foreground's process name and window class (no titles); a change to a game window that
      GetForegroundWindow confirms at once is a 'FOREGROUND GAME' line (with the user's idle time); an unconfirmed one
      (the game activating a window in its own background input queue raises the event too, and no poll ever saw
      those windows as the foreground) is logged and counted apart;
    - the game's top-level windows: created, shown (EVENT_OBJECT_SHOW, with where: 'ON THE DESKTOP' or off it),
      destroyed, and every move of a visible one onto the desktop (EVENT_OBJECT_LOCATIONCHANGE);
    - the shell's view (a hidden window registered with RegisterShellHookWindow): a game window activated
      (HSHELL_WINDOWACTIVATED / HSHELL_RUDEAPPACTIVATED) or flashing its taskbar button (HSHELL_FLASH: the game asked
      for the foreground and Windows refused it);
    - a poll every -IntervalMs (100): GetForegroundWindow, and each visible game window that lies on the desktop
      (left edge above -20000; parked windows sit at -30000) with how long it stayed there.
  It stops when -StopFile exists or after -Seconds, writes a summary line ('focus: ...') to -Out and prints it.
  Read-only: it moves, activates and clicks nothing.

  Usage:
    powershell -ExecutionPolicy Bypass -File re\scripts\focus_watch.ps1 -Out <file> -Under <folder> [-StopFile <file>]
        [-IntervalMs 100] [-Seconds 3600]
  This file must stay ASCII: Windows PowerShell 5.1 reads scripts without a BOM in the ANSI codepage.
#>
param(
    [Parameter(Mandatory)] [string]$Out,
    [Parameter(Mandatory)] [string]$Under,
    [string]$StopFile,
    [int]$IntervalMs = 100,
    [int]$Seconds = 3600
)
$ErrorActionPreference = 'Stop'
if (-not $StopFile) { $StopFile = "$Out.stop" }
Add-Type -TypeDefinition @"
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

public static class DgFocusWatch {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [StructLayout(LayoutKind.Sequential)] public struct MSG { public IntPtr hwnd; public uint message; public IntPtr wParam; public IntPtr lParam; public uint time; public int x; public int y; }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] public struct WNDCLASS {
    public uint style; public IntPtr lpfnWndProc; public int cbClsExtra; public int cbWndExtra; public IntPtr hInstance;
    public IntPtr hIcon; public IntPtr hCursor; public IntPtr hbrBackground; public string lpszMenuName; public string lpszClassName; }
  public delegate void WinEventProc(IntPtr hook, uint ev, IntPtr hwnd, int idObject, int idChild, uint thread, uint time);
  public delegate IntPtr WndProcDelegate(IntPtr h, uint msg, IntPtr w, IntPtr l);
  public delegate bool EnumProc(IntPtr h, IntPtr l);
  [DllImport("user32.dll")] static extern IntPtr SetWinEventHook(uint min, uint max, IntPtr mod, WinEventProc proc, uint pid, uint tid, uint flags);
  [DllImport("user32.dll")] static extern bool UnhookWinEvent(IntPtr h);
  [DllImport("user32.dll")] static extern int GetMessageW(out MSG m, IntPtr h, uint a, uint b);
  [DllImport("user32.dll")] static extern bool TranslateMessage(ref MSG m);
  [DllImport("user32.dll")] static extern IntPtr DispatchMessageW(ref MSG m);
  [DllImport("user32.dll")] static extern bool PostThreadMessageW(uint tid, uint msg, IntPtr w, IntPtr l);
  [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
  [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll")] static extern IntPtr GetAncestor(IntPtr h, uint flags);
  [DllImport("user32.dll")] static extern IntPtr GetWindow(IntPtr h, uint cmd);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassNameW(IntPtr h, StringBuilder sb, int n);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc cb, IntPtr l);
  [DllImport("kernel32.dll")] static extern IntPtr OpenProcess(uint access, bool inherit, uint pid);
  [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
  [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern bool QueryFullProcessImageNameW(IntPtr h, uint flags, StringBuilder sb, ref uint size);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern ushort RegisterClassW(ref WNDCLASS wc);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern IntPtr CreateWindowExW(uint ex, string cls, string name, uint style, int x, int y, int w, int h, IntPtr parent, IntPtr menu, IntPtr inst, IntPtr param);
  [DllImport("user32.dll")] static extern IntPtr DefWindowProcW(IntPtr h, uint msg, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] static extern bool RegisterShellHookWindow(IntPtr h);
  [DllImport("user32.dll")] static extern bool DeregisterShellHookWindow(IntPtr h);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern uint RegisterWindowMessageW(string s);
  [DllImport("user32.dll")] static extern bool DestroyWindow(IntPtr h);
  [DllImport("kernel32.dll")] static extern IntPtr GetModuleHandleW(IntPtr n);

  static readonly object Gate = new object();
  static StreamWriter Out;
  static string Prefix;
  static Dictionary<uint, string> PathOf = new Dictionary<uint, string>();
  static WinEventProc EventCb = OnEvent;
  static WndProcDelegate WndCb = OnWnd;
  static uint ShellMsg, LoopThread;
  static Stopwatch Clock = Stopwatch.StartNew();
  static int FgChanges, FgGameEvents, FgGameUnconfirmed, FgGameSamples, Samples, Shows, ShowsOnDesktop, Created, Episodes, Moves, Flashes, Activations, AboveSamples;
  [StructLayout(LayoutKind.Sequential)] public struct LASTINPUTINFO { public uint cbSize; public uint dwTime; }
  [DllImport("user32.dll")] static extern bool GetLastInputInfo(ref LASTINPUTINFO l);
  // how long the user has given no input: past ForegroundLockTimeout (200 s) Windows lets any program take the foreground
  static uint IdleMs() { LASTINPUTINFO l = new LASTINPUTINFO(); l.cbSize = 8; GetLastInputInfo(ref l); return (uint)Environment.TickCount - l.dwTime; }
  // whether window a lies above window b in the z-order (b's predecessors up to the top)
  static bool Above(IntPtr a, IntPtr b) {
    int n = 0;
    for (IntPtr w = GetWindow(b, 3); w != IntPtr.Zero && n < 5000; w = GetWindow(w, 3), n++) if (w == a) return true;  // GW_HWNDPREV
    return false;
  }
  static long LongestMs;
  static bool LastFgGame;
  static Dictionary<IntPtr, long> OnDesktopSince = new Dictionary<IntPtr, long>();

  static void Log(string m) { lock (Gate) { Out.WriteLine(DateTime.Now.ToString("HH:mm:ss.fff") + " " + m); } }

  static string ImagePath(uint pid) {
    string p;
    lock (Gate) { if (PathOf.TryGetValue(pid, out p)) return p; }
    p = "";
    IntPtr h = OpenProcess(0x1000, false, pid);  // PROCESS_QUERY_LIMITED_INFORMATION
    if (h != IntPtr.Zero) {
      StringBuilder sb = new StringBuilder(1024); uint n = 1024;
      if (QueryFullProcessImageNameW(h, 0, sb, ref n)) p = sb.ToString();
      CloseHandle(h);
    }
    lock (Gate) { PathOf[pid] = p; }
    return p;
  }
  // the game's label (its path under -Under) or null
  static string Label(uint pid) {
    string p = ImagePath(pid);
    if (p.Length == 0 || !p.StartsWith(Prefix, StringComparison.OrdinalIgnoreCase)) return null;
    if (!p.EndsWith("\\Dawngate.exe", StringComparison.OrdinalIgnoreCase)) return null;
    return p.Substring(Prefix.Length);
  }
  static string ClassOf(IntPtr h) { StringBuilder sb = new StringBuilder(256); GetClassNameW(h, sb, 256); return sb.ToString(); }
  static string Proc(uint pid) { string p = ImagePath(pid); return (p.Length == 0 ? "?" : Path.GetFileName(p)) + " pid " + pid; }
  static string RectOf(IntPtr h) { RECT r; GetWindowRect(h, out r); return "(" + r.L + "," + r.T + ") " + (r.R - r.L) + "x" + (r.B - r.T); }

  static void OnEvent(IntPtr hook, uint ev, IntPtr hwnd, int idObject, int idChild, uint thread, uint time) {
    try {
      if (hwnd == IntPtr.Zero) return;
      uint pid; GetWindowThreadProcessId(hwnd, out pid);
      if (ev == 3) {  // EVENT_SYSTEM_FOREGROUND
        FgChanges++;
        string lab = Label(pid);
        // the game's own activation of a window in its background input queue raises this event too (test runs:
        // no poll ever saw those windows as the foreground, and nothing gave it back), so each one is checked against
        // GetForegroundWindow right away; only a confirmed one counts
        IntPtr now = GetForegroundWindow();
        if (lab != null && now == hwnd) { FgGameEvents++; Log("FOREGROUND GAME: " + lab + " pid " + pid + " window " + ClassOf(hwnd) + " " + RectOf(hwnd) + " (confirmed; the user idle " + IdleMs() + " ms)"); }
        else if (lab != null) { FgGameUnconfirmed++; Log("foreground event for a game window, not confirmed (GetForegroundWindow: " + (now == IntPtr.Zero ? "none" : ClassOf(now)) + "): " + lab + " pid " + pid + " window " + ClassOf(hwnd)); }
        else Log("foreground: " + Proc(pid) + " (" + ClassOf(hwnd) + ")");
        return;
      }
      if (idObject != 0 || GetAncestor(hwnd, 2) != hwnd) return;  // OBJID_WINDOW, top-level (GA_ROOT) only
      string l2 = Label(pid);
      if (l2 == null) return;
      RECT r; GetWindowRect(hwnd, out r);
      bool desk = r.L > -20000;
      if (ev == 0x8000) { Created++; Log("game window created: " + l2 + " pid " + pid + " " + ClassOf(hwnd) + " " + RectOf(hwnd) + " visible " + IsWindowVisible(hwnd)); }
      else if (ev == 0x8001) Log("game window destroyed: " + l2 + " pid " + pid);
      else if (ev == 0x8002) { Shows++; if (desk) ShowsOnDesktop++; Log("game window shown: " + l2 + " pid " + pid + " " + ClassOf(hwnd) + " " + RectOf(hwnd) + (desk ? " ON THE DESKTOP" : " off the desktop")); }
      else if (ev == 0x800B && desk && IsWindowVisible(hwnd)) { Moves++; if (Moves <= 50) Log("game window moved onto the desktop: " + l2 + " pid " + pid + " " + ClassOf(hwnd) + " " + RectOf(hwnd)); }
    } catch (Exception e) { Log("event error: " + e.Message); }
  }

  static IntPtr OnWnd(IntPtr h, uint msg, IntPtr w, IntPtr l) {
    if (ShellMsg != 0 && msg == ShellMsg) {
      try {
        int code = w.ToInt32(); uint pid; GetWindowThreadProcessId(l, out pid); string lab = Label(pid);
        if (lab != null) {
          if (code == 0x8006) { Flashes++; if (Flashes <= 20) Log("SHELL FLASH: " + lab + " pid " + pid + " window " + ClassOf(l) + " (it asked for the foreground; its taskbar button flashes)"); }
          else if (code == 4 || code == 0x8004) { Activations++; Log("SHELL ACTIVATED: " + lab + " pid " + pid + " window " + ClassOf(l)); }
          else if (code == 1) Log("shell: game window created (taskbar button): " + lab + " pid " + pid + " " + ClassOf(l));
          else if (code == 2) Log("shell: game window destroyed: " + lab + " pid " + pid);
        }
      } catch (Exception e) { Log("shell error: " + e.Message); }
      return IntPtr.Zero;
    }
    return DefWindowProcW(h, msg, w, l);
  }

  static void Loop(object ready) {
    LoopThread = GetCurrentThreadId();
    List<IntPtr> hooks = new List<IntPtr>();
    hooks.Add(SetWinEventHook(3, 3, IntPtr.Zero, EventCb, 0, 0, 0));            // foreground (WINEVENT_OUTOFCONTEXT)
    hooks.Add(SetWinEventHook(0x8000, 0x8002, IntPtr.Zero, EventCb, 0, 0, 0));  // create, destroy, show
    hooks.Add(SetWinEventHook(0x800B, 0x800B, IntPtr.Zero, EventCb, 0, 0, 0));  // location change
    int ok = 0; foreach (IntPtr x in hooks) if (x != IntPtr.Zero) ok++;
    WNDCLASS wc = new WNDCLASS();
    wc.lpfnWndProc = Marshal.GetFunctionPointerForDelegate(WndCb);
    wc.hInstance = GetModuleHandleW(IntPtr.Zero);
    wc.lpszClassName = "DgFocusWatchShell";
    RegisterClassW(ref wc);
    IntPtr win = CreateWindowExW(0, "DgFocusWatchShell", "", 0, 0, 0, 0, 0, IntPtr.Zero, IntPtr.Zero, wc.hInstance, IntPtr.Zero);
    ShellMsg = RegisterWindowMessageW("SHELLHOOK");
    bool shell = win != IntPtr.Zero && RegisterShellHookWindow(win);
    Log("hooks: " + ok + " of 3 WinEvent hooks, shell hook " + (shell ? "on" : "OFF"));
    ((ManualResetEvent)ready).Set();
    MSG m;
    while (GetMessageW(out m, IntPtr.Zero, 0, 0) > 0) { TranslateMessage(ref m); DispatchMessageW(ref m); }
    foreach (IntPtr x in hooks) if (x != IntPtr.Zero) UnhookWinEvent(x);
    if (win != IntPtr.Zero) { DeregisterShellHookWindow(win); DestroyWindow(win); }
  }

  public static string Run(string outFile, string under, string stopFile, int intervalMs, int maxSeconds) {
    Prefix = Path.GetFullPath(under).TrimEnd('\\') + "\\";
    Out = new StreamWriter(outFile, true, new UTF8Encoding(false));
    Out.AutoFlush = true;
    Log("focus watch: Dawngate.exe under " + Prefix + ", poll every " + intervalMs + " ms, stop file " + stopFile);
    ManualResetEvent ready = new ManualResetEvent(false);
    Thread t = new Thread(Loop); t.IsBackground = true; t.Start(ready); ready.WaitOne(5000);
    Log("the user idle " + IdleMs() + " ms at the start");
    DateTime deadline = DateTime.Now.AddSeconds(maxSeconds);
    long lastClear = 0;
    while (!File.Exists(stopFile) && DateTime.Now < deadline) {
      long now = Clock.ElapsedMilliseconds;
      if (now - lastClear > 2000) { lock (Gate) { PathOf.Clear(); } lastClear = now; }  // pids are reused
      Samples++;
      IntPtr fg = GetForegroundWindow(); uint fpid = 0;
      if (fg != IntPtr.Zero) GetWindowThreadProcessId(fg, out fpid);
      bool fgGame = fg != IntPtr.Zero && Label(fpid) != null;
      if (fgGame) FgGameSamples++;
      if (fgGame != LastFgGame) { Log(fgGame ? "POLL: the foreground IS a game window (" + Label(fpid) + ", " + ClassOf(fg) + "; the user idle " + IdleMs() + " ms)" : "poll: the foreground is no longer a game window"); LastFgGame = fgGame; }
      List<IntPtr> desk = new List<IntPtr>();
      EnumWindows(delegate(IntPtr h, IntPtr l) {
        uint p; GetWindowThreadProcessId(h, out p);
        if (Label(p) == null || !IsWindowVisible(h)) return true;
        RECT r; GetWindowRect(h, out r);
        if (r.L > -20000) desk.Add(h);
        return true;
      }, IntPtr.Zero);
      bool above = false;
      foreach (IntPtr h in desk) {
        bool up = fg != IntPtr.Zero && !fgGame && Above(h, fg);
        if (up) above = true;
        if (OnDesktopSince.ContainsKey(h)) continue;
        OnDesktopSince[h] = now; Episodes++;
        uint p; GetWindowThreadProcessId(h, out p);
        Log("POLL: a game window lies on the desktop: " + Label(p) + " pid " + p + " " + ClassOf(h) + " " + RectOf(h) +
          (fgGame ? " (it is the foreground)" : up ? ", ABOVE the foreground window" : ", below the foreground window"));
      }
      if (above) AboveSamples++;
      foreach (IntPtr h in new List<IntPtr>(OnDesktopSince.Keys)) {
        if (desk.Contains(h)) continue;
        long d = now - OnDesktopSince[h];
        if (d > LongestMs) LongestMs = d;
        Log("poll: that game window left the desktop after about " + d + " ms (" + RectOf(h) + ")");
        OnDesktopSince.Remove(h);
      }
      Thread.Sleep(intervalMs);
    }
    foreach (IntPtr h in OnDesktopSince.Keys) { long d = Clock.ElapsedMilliseconds - OnDesktopSince[h]; if (d > LongestMs) LongestMs = d; Log("poll: a game window still on the desktop at the end, for " + d + " ms"); }
    PostThreadMessageW(LoopThread, 0x0012, IntPtr.Zero, IntPtr.Zero);  // WM_QUIT
    t.Join(2000);
    string s = "focus: " + FgChanges + " foreground change(s), " + FgGameEvents + " to a game window (confirmed; " + FgGameUnconfirmed +
      " unconfirmed: activations inside the game's background queue); " + Samples + " polls every " +
      intervalMs + " ms, the game in the foreground in " + FgGameSamples + "; game windows created " + Created + ", shown " + Shows +
      " (" + ShowsOnDesktop + " on the desktop), on the desktop at a poll " + Episodes + " time(s) (longest about " + LongestMs +
      " ms; above the foreground window in " + AboveSamples + " poll(s)), moved onto the desktop " + Moves + " time(s); shell: activated " +
      Activations + ", flashed " + Flashes + "; the user idle " + IdleMs() + " ms at the end";
    Log(s);
    Out.Close();
    return s;
  }
}
"@
[DgFocusWatch]::Run($Out, $Under, $StopFile, $IntervalMs, $Seconds)
