package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The match hosts on Linux: Dawngate.exe is a 32-bit Windows program, so Start-Server on Linux runs every host under
// Wine (dgserver --gms-runner). Wine is the operator's install (the setup never installs anything); the setup checks it
// and makes the Wine prefix the hosts share, once, inside the setup folder.

// WinePrefix is the Wine prefix Start-Server makes on Linux for its match hosts (WINEPREFIX), unless the environment
// names one.
func (l Layout) WinePrefix() string { return filepath.Join(l.Root, "wine") }

// WineInstallHint says what to install when Wine, or its 32-bit part, is missing.
const WineInstallHint = `The match hosts run the game under Wine, with its 32-bit part. Install it, then start again.
On Debian or Ubuntu:
  sudo dpkg --add-architecture i386
  sudo apt update
  sudo apt install --no-install-recommends wine wine32 wine64
On other distributions, install Wine with its 32-bit (WoW64) support.`

// wineMarker is the file in the prefix that says which Wine made (or last checked) it and that it ran a 32-bit program.
const wineMarker = "dawngate-wine.txt"

// WineOptions are Wine's.
type WineOptions struct {
	Command string // the wine program ("" = wine from the PATH)
	Prefix  string // the Wine prefix ("" = Layout.WinePrefix())
}

// WineEnv is what the hosts need: the wine program (dgserver --gms-runner) and the prefix (WINEPREFIX).
type WineEnv struct {
	Wine, Prefix, Version string
}

// wineTimeout bounds each Wine command of the setup (a new prefix takes 10-30 s).
var wineTimeout = 5 * time.Minute

// Wine checks (or, with Check, only reports) Wine for the match hosts: "wine" is found and answers --version; "wine
// prefix" exists and has run a 32-bit program with this Wine version (else it is made with wineboot and checked by
// running the prefix's 32-bit cmd.exe; a prefix made now that can't is removed again, so that the next start makes it
// anew after Wine's 32-bit part is installed; a prefix of the environment's WINEPREFIX is never removed). Every Wine command runs with WINEDLLOVERRIDES=mscoree,mshtml= (a new
// prefix never offers to fetch Mono or Gecko) and WINEDEBUG=-all unless the environment sets them.
func (s *Setup) Wine(o WineOptions) (WineEnv, error) {
	cmd := o.Command
	if cmd == "" {
		cmd = "wine"
	}
	env := WineEnv{Prefix: o.Prefix}
	if env.Prefix == "" {
		env.Prefix = s.WinePrefix()
	}
	path, err := exec.LookPath(cmd)
	if err != nil {
		s.report(Missing, "wine", fmt.Sprintf("not installed (no %s on the PATH): Wine with its 32-bit part runs the match hosts", cmd))
		return env, s.result()
	}
	env.Wine = path
	out, code, err := s.runWine(env, path, "--version")
	if err != nil || code != 0 {
		s.report(Failed, "wine", fmt.Sprintf("%s --version: %s", path, failure(out, code, err)))
		return env, s.result()
	}
	env.Version = firstLine(out)
	s.report(OK, "wine", env.Version+" ("+path+")")

	marker := filepath.Join(env.Prefix, wineMarker)
	sysreg := filepath.Join(env.Prefix, "system.reg")
	raw, _ := os.ReadFile(marker)
	have := isFile(sysreg)
	switch {
	case have && strings.TrimSpace(string(raw)) == env.Version:
		s.report(OK, "wine prefix", s.show(env.Prefix))
		return env, s.result()
	case s.Check && !have:
		s.report(Missing, "wine prefix", s.show(env.Prefix)+" (made at the next start)")
		return env, s.result()
	case s.Check:
		s.report(Stale, "wine prefix", s.show(env.Prefix)+" not yet checked with "+env.Version)
		return env, s.result()
	}
	start := time.Now()
	made := false
	own := env.Prefix == s.WinePrefix() // only the setup folder's own prefix is ever removed
	if !have {
		if err := os.MkdirAll(filepath.Dir(env.Prefix), 0o755); err != nil {
			s.report(Failed, "wine prefix", err.Error())
			return env, s.result()
		}
		s.say("making the Wine prefix %s (once; about half a minute) ...", env.Prefix)
		out, code, err := s.runWine(env, path, "wineboot", "--init")
		if err == nil && code == 0 {
			// the wineserver writes the registry (system.reg) when the prefix's last process has ended: wait for it
			s.runWine(env, wineserver(path), "-w")
		}
		if err != nil || code != 0 || !isFile(sysreg) {
			if own {
				os.RemoveAll(env.Prefix)
			}
			s.report(Failed, "wine prefix", "wineboot --init: "+failure(out, code, err))
			return env, s.result()
		}
		made = true
	}
	// a 32-bit program: the prefix's 32-bit cmd.exe (SysWOW64 in a 64-bit prefix, System32 in a 32-bit one; a 64-bit
	// prefix that Wine made without its 32-bit part has no SysWOW64 cmd.exe)
	cmd32 := filepath.Join(env.Prefix, "drive_c", "windows", "syswow64", "cmd.exe")
	if reg, _ := os.ReadFile(sysreg); strings.Contains(string(reg), "#arch=win32") {
		cmd32 = filepath.Join(env.Prefix, "drive_c", "windows", "system32", "cmd.exe")
	}
	out, code, err = "", -1, nil
	if !isFile(cmd32) {
		err = fmt.Errorf("the prefix has no 32-bit Windows folder (%s missing)", cmd32)
	} else {
		out, code, err = s.runWine(env, path, cmd32, "/c", "exit", "7")
	}
	if err != nil || code != 7 {
		if made && own {
			os.RemoveAll(env.Prefix) // made without the 32-bit part: the next start makes it again
		}
		s.report(Failed, "wine 32-bit", "Wine can't run 32-bit programs, so not the match hosts: "+failure(out, code, err))
		return env, s.result()
	}
	if err := os.WriteFile(marker, []byte(env.Version+"\n"), 0o644); err != nil {
		s.report(Failed, "wine prefix", err.Error())
		return env, s.result()
	}
	what := "checked again with " + env.Version
	if made {
		what = "made"
	}
	s.report(Done, "wine prefix", fmt.Sprintf("%s %s in %.0f s (32-bit programs run)", s.show(env.Prefix), what, time.Since(start).Seconds()))
	return env, s.result()
}

// WineVars are the environment a Wine command of the setup (and a match host) gets beside the process's own:
// WINEPREFIX, and the Wine defaults the environment doesn't set.
func (e WineEnv) WineVars(lookup func(string) (string, bool)) []string {
	v := []string{"WINEPREFIX=" + e.Prefix}
	if _, ok := lookup("WINEDLLOVERRIDES"); !ok {
		v = append(v, "WINEDLLOVERRIDES=mscoree,mshtml=")
	}
	if _, ok := lookup("WINEDEBUG"); !ok {
		v = append(v, "WINEDEBUG=-all")
	}
	return v
}

// runWine runs wine with args (its output into a file, not a pipe: wineboot leaves the wineserver running, which
// would hold a pipe open) and returns the output, the exit code and an error when it couldn't run or timed out.
func (s *Setup) runWine(env WineEnv, wine string, args ...string) (string, int, error) {
	f, err := os.CreateTemp("", "dawngate-wine-*.txt")
	if err != nil {
		return "", -1, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), wineTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, wine, args...)
	c.Env = append(os.Environ(), env.WineVars(os.LookupEnv)...)
	c.Stdout, c.Stderr = f, f
	c.Dir = os.TempDir()
	err = c.Run()
	b, _ := os.ReadFile(f.Name())
	if ctx.Err() != nil {
		return string(b), -1, fmt.Errorf("no answer after %s", wineTimeout)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(b), exit.ExitCode(), nil
	}
	if err != nil {
		return string(b), -1, err
	}
	return string(b), 0, nil
}

// wineserver is the wineserver beside wine (/usr/bin, /opt/wine-stable/bin), else the PATH's.
func wineserver(wine string) string {
	if p := filepath.Join(filepath.Dir(wine), "wineserver"); isFile(p) {
		return p
	}
	if p, err := exec.LookPath("wineserver"); err == nil {
		return p
	}
	return "wineserver"
}

// failure describes a Wine command that failed: its error or exit code and its first lines of output.
func failure(out string, code int, err error) string {
	why := fmt.Sprintf("exit code %d", code)
	if err != nil {
		why = err.Error()
	}
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" && len(lines) < 3 {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		why += ": " + strings.Join(lines, " / ")
	}
	return why
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	return strings.TrimSpace(l)
}
