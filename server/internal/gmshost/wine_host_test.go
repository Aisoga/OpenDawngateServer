package gmshost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// H4a: the real match host (a host copy's Dawngate.exe with dgsim.dll) under the real supervisor, run
// by this OS's runner (Wine on Linux): hello, createGame, gameReady, then the end by abortGame (exit
// code 0 through Wine) or by a kill (dgserver's shutdown), after which no process of the host is
// left. Set DG_WINE_HOST to the host copy (a folder ResolveExe takes; on Linux keep it on a Linux
// filesystem: Wine can't map the DLLs from a Docker Desktop bind mount of a Windows folder) and optionally DG_WINE_RUNNER (dgserver's --gms-runner, default auto). The host's
// first start in a new Wine prefix takes longer (the prefix is made), hence the generous timeouts.
func TestWineHost(t *testing.T) {
	dir := os.Getenv("DG_WINE_HOST")
	if dir == "" {
		t.Skip("DG_WINE_HOST not set (a host copy, on Linux run through Wine)")
	}
	exe, err := ResolveExe(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, end := range []string{"abort", "kill"} {
		t.Run(end, func(t *testing.T) {
			data := t.TempDir()
			l := &ExecLauncher{Path: exe, Args: []string{"-multipleInstances"}, DataDir: data,
				Runner: RunnerFor(os.Getenv("DG_WINE_RUNNER"), exe)}
			log := &syncBuf{}
			m := NewManager(Config{Log: logging.New(log), Launcher: l, Pool: NewPool(20044, 20044),
				HelloTimeout: 90 * time.Second, ReadyTimeout: 60 * time.Second, HeartbeatTimeout: 30 * time.Second})
			if err := m.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			closed := false
			defer func() {
				if !closed {
					m.Close()
				}
			}()
			rec := newRecorder()
			start := time.Now()
			mt, err := m.Start(practice, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			select {
			case port := <-rec.ready:
				t.Logf("gameReady on port %d %v after the start", port, time.Since(start).Round(time.Millisecond))
			case <-time.After(150 * time.Second):
				t.Fatalf("no gameReady\n%s", log.String())
			}
			var o Outcome
			if end == "abort" {
				mt.Abort("wine check")
				o = rec.wait(t)
				if !o.Ready || o.Killed || o.ExitCode != 0 || o.Reason != "abortGame: wine check" {
					t.Errorf("outcome %+v\n%s", o, log.String())
				}
			} else {
				m.Close() // dgserver's shutdown kills every host
				closed = true
				o = rec.wait(t)
				if !o.Killed {
					t.Errorf("outcome %+v\n%s", o, log.String())
				}
			}
			t.Logf("outcome: %s, exit code %d, killed %v, %v", o.Reason, o.ExitCode, o.Killed, o.Duration.Round(time.Millisecond))
			checkNoHostLeft(t, l, data)
		})
	}
}

// H4c1: where a host under Wine really listens when asked for an address (DGGMS_BIND): for each of DG_WINE_BIND's
// addresses (comma-separated, e.g. 127.0.0.1,127.0.1.10,<this machine's address>) a host with a game, then the
// kernel's view of its GMS socket (ss -ulne: the local address and a bound device, %lo or %eth0). Start-Server on
// Linux relies on the first: a host asked for 127.0.0.1 listens on 127.0.0.1 exactly.
func TestWineHostBind(t *testing.T) {
	dir, binds := os.Getenv("DG_WINE_HOST"), os.Getenv("DG_WINE_BIND")
	if dir == "" || binds == "" {
		t.Skip("DG_WINE_HOST and DG_WINE_BIND not set")
	}
	exe, err := ResolveExe(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, bind := range strings.Split(binds, ",") {
		t.Run(bind, func(t *testing.T) {
			data := t.TempDir()
			l := &ExecLauncher{Path: exe, Args: []string{"-multipleInstances"}, DataDir: data,
				Runner: RunnerFor(os.Getenv("DG_WINE_RUNNER"), exe)}
			log := &syncBuf{}
			m := NewManager(Config{Log: logging.New(log), Launcher: l, Pool: NewPool(20044, 20044), BindHost: bind,
				HelloTimeout: 90 * time.Second, ReadyTimeout: 60 * time.Second, HeartbeatTimeout: 30 * time.Second})
			if err := m.Listen("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			rec := newRecorder()
			mt, err := m.Start(practice, rec.Events())
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-rec.ready:
			case <-time.After(150 * time.Second):
				t.Fatalf("no gameReady\n%s", log.String())
			}
			ss, err := exec.Command("ss", "-ulneH", "sport = :20044").CombinedOutput()
			if err != nil {
				t.Fatalf("ss: %v %s", err, ss)
			}
			local := ""
			if f := strings.Fields(string(ss)); len(f) >= 4 {
				local = f[3]
			}
			t.Logf("asked for %s:20044, listens on %s (ss: %s)", bind, local, strings.TrimSpace(string(ss)))
			if bind == "127.0.0.1" && local != "127.0.0.1:20044" {
				t.Errorf("a host asked for 127.0.0.1 listens on %q", local)
			}
			mt.Abort("bind check")
			rec.wait(t)
			checkNoHostLeft(t, l, data)
		})
	}
}

// checkNoHostLeft: the host wrote its launcher-event line, and under Wine no Dawngate.exe process is left (Wine's own
// server and helpers end by themselves).
func checkNoHostLeft(t *testing.T, l *ExecLauncher, data string) {
	t.Helper()
	dgsim, err := os.ReadFile(filepath.Join(data, "1", "dgsim.log"))
	if err != nil || !strings.Contains(string(dgsim), `host: launcher event Global\Dawngate L2G`) {
		t.Errorf("dgsim.log (%v) has no launcher-event line", err)
	}
	if l.Runner == nil {
		return
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		out, _ := exec.Command("pgrep", "-f", "Dawngate.exe").Output()
		if len(strings.TrimSpace(string(out))) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("Dawngate.exe processes left: %s", out)
			return
		}
	}
}
