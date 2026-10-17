package gmshost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Match hosts off Windows (H4a). The match host is Dawngate.exe with our DLL, a 32-bit Windows program,
// so on Linux it runs under Wine: ExecLauncher.Runner names the program that runs it ("wine"), and the
// host gets its data folder as Wine shows it (Z: is the root). The launcher event the game needs is
// created by the host's DLL itself when nobody holds it (a named event lives in the Wine server, so
// dgserver can't provide it there; event_other.go).

// DefaultRunner is what runs the host program exe on this OS: nothing on Windows (the exe itself), Wine
// elsewhere for a Windows program (a .exe); anything else, e.g. a test's helper, runs as it is.
func DefaultRunner(exe string) []string {
	if runtime.GOOS == "windows" || !strings.EqualFold(filepath.Ext(exe), ".exe") {
		return nil
	}
	return []string{"wine"}
}

// RunnerFor reads dgserver's --gms-runner for the host program exe: "auto" (or "") is DefaultRunner,
// "none" runs exe itself, anything else is a command line split at blanks (e.g. "wine" or
// "/opt/wine-stable/bin/wine").
func RunnerFor(spec, exe string) []string {
	switch strings.TrimSpace(spec) {
	case "", "auto":
		return DefaultRunner(exe)
	case "none":
		return nil
	}
	return strings.Fields(spec)
}

// underWine says whether hosts started with runner run under Wine: a runner off Windows. On Windows a
// runner is only a wrapper (tests), and paths stay as they are.
func underWine(runner []string) bool { return len(runner) > 0 && runtime.GOOS != "windows" }

// WinePath is an absolute path of this machine as a Windows program under Wine sees it: Wine maps the
// root to Z:. Anything else (a relative or a Windows path) is returned as it is.
func WinePath(p string) string {
	if strings.HasPrefix(p, "/") {
		return "Z:" + strings.ReplaceAll(p, "/", `\`)
	}
	return p
}

// wineVars are the environment defaults of a host under Wine, each only if the environment (lookup)
// doesn't set it: WINEDLLOVERRIDES=mscoree,mshtml= (when Wine makes a new prefix it must never offer to
// fetch Mono or Gecko from the internet; the host needs neither) and WINEDEBUG=-all (Wine's own fixme
// lines would fill the host's stdout.txt).
func wineVars(lookup func(string) (string, bool)) []string {
	var v []string
	if _, ok := lookup("WINEDLLOVERRIDES"); !ok {
		v = append(v, "WINEDLLOVERRIDES=mscoree,mshtml=")
	}
	if _, ok := lookup("WINEDEBUG"); !ok {
		v = append(v, "WINEDEBUG=-all")
	}
	return v
}

// command is the process for Path and Args: Path itself, or the runner with Path as its first argument.
func (l *ExecLauncher) command() *exec.Cmd {
	if len(l.Runner) == 0 {
		return exec.Command(l.Path, l.Args...)
	}
	argv := append(append(append([]string{}, l.Runner[1:]...), l.Path), l.Args...)
	return exec.Command(l.Runner[0], argv...)
}

// hostVars adds what a host under Wine needs to vars: its data folder as a Wine path and the Wine
// defaults (wineVars). Without a Wine runner vars stay as they are.
func (l *ExecLauncher) hostVars(vars []string) []string {
	if !underWine(l.Runner) {
		return vars
	}
	for i, v := range vars {
		if strings.HasPrefix(v, "DGGMS_DATA=") {
			vars[i] = "DGGMS_DATA=" + WinePath(strings.TrimPrefix(v, "DGGMS_DATA="))
		}
	}
	return append(vars, wineVars(os.LookupEnv)...)
}
