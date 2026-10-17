package gmshost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

// Env is what a host gets in its environment (ADR-2 decision 7). The DLL is in host mode only when
// these are set; without them it stays the D1 harness.
type Env struct {
	Control string // DGGMS_CONTROL: the control address to dial, "127.0.0.1:10040"
	Match   int64  // DGGMS_MATCH: the match id, echoed in hello
	Token   string // DGGMS_TOKEN: echoed in hello, checked by the server
	Bind    string // DGGMS_BIND: the address the host's GMS port binds, "127.0.0.1:10042"
	// Warm (H1-live): DGGMS_WARM=1, a host of the warm pool (Config.Warm): it waits for createGame
	// without the create timeout ([host] create_timeout_s), since its game may come much later.
	Warm bool
}

// Vars returns the environment entries ("NAME=value").
func (e Env) Vars() []string {
	v := []string{"DGGMS_CONTROL=" + e.Control, "DGGMS_MATCH=" + strconv.FormatInt(e.Match, 10),
		"DGGMS_TOKEN=" + e.Token, "DGGMS_BIND=" + e.Bind}
	if e.Warm {
		v = append(v, "DGGMS_WARM=1")
	}
	return v
}

// EnvFromLookup reads an Env (as a host does) through lookup, e.g. os.LookupEnv. ok is false if
// DGGMS_CONTROL is missing (not in host mode).
func EnvFromLookup(lookup func(string) (string, bool)) (e Env, ok bool, err error) {
	if e.Control, ok = lookup("DGGMS_CONTROL"); !ok {
		return Env{}, false, nil
	}
	m, _ := lookup("DGGMS_MATCH")
	if e.Match, err = strconv.ParseInt(m, 10, 64); err != nil {
		return Env{}, true, fmt.Errorf("gmshost: bad DGGMS_MATCH %q", m)
	}
	e.Token, _ = lookup("DGGMS_TOKEN")
	e.Bind, _ = lookup("DGGMS_BIND")
	w, _ := lookup("DGGMS_WARM")
	e.Warm = w == "1"
	return e, true, nil
}

// Process is a started host.
type Process interface {
	Pid() int
	Kill() error
	// Wait blocks until the process has exited and been reaped; code is its exit code (-1 unknown).
	Wait() (code int, err error)
}

// Launcher starts a host with env.
type Launcher interface {
	Launch(env Env) (Process, error)
}

// ExecLauncher starts Path (Dawngate.exe of the host copy) with Args in Dir, and the DGGMS_*
// variables added to this process's environment. On Windows every host goes into this process's
// host job (kill on job close, H2b), so the hosts end with the process that started them, also when
// it crashes or is killed.
type ExecLauncher struct {
	Path string
	Args []string
	Dir  string    // working directory; "" = Path's folder (the game expects game\ as its directory)
	Out  io.Writer // the process's stdout and stderr; nil = discarded (with DataDir: a file there)
	// DataDir (dgserver --gms-data, C1b): every host gets its own folder <DataDir>\<match id>, passed as
	// DGGMS_DATA (the DLL's dgsim.log, AppData and Documents), with its stdout and stderr in stdout.txt
	// there. "" = the DLL's default <game dir>\host\<match id>, output to Out.
	DataDir string
	// Options (dgserver --gms-options, C1b) becomes DGGMS_OPTIONS: "key=value;..." over the host
	// copy's dgsim.ini [host] keys, for development runs (run_session.ps1 -HostOptions). "" = unset.
	Options string
	// Prepare runs once before the first launch (dgserver: create the launcher event the game
	// needs, see launcherEvent). An error fails every launch.
	Prepare func() error
	// Runner (H4a, dgserver --gms-runner) runs Path: nil = Path itself (Windows), ["wine"] on Linux,
	// where the host also gets its data folder as a Wine path (wine.go).
	Runner []string

	once    sync.Once
	prepErr error
}

// Launch starts the process.
func (l *ExecLauncher) Launch(env Env) (Process, error) {
	l.once.Do(func() {
		if l.Prepare != nil {
			l.prepErr = l.Prepare()
		}
	})
	if l.prepErr != nil {
		return nil, l.prepErr
	}
	cmd := l.command() // Path, or the runner with Path (wine.go)
	cmd.Dir = l.Dir
	if cmd.Dir == "" {
		cmd.Dir = filepath.Dir(l.Path)
	}
	vars := env.Vars()
	cmd.Stdout, cmd.Stderr = l.Out, l.Out
	var out *os.File
	if l.DataDir != "" {
		dir, err := filepath.Abs(filepath.Join(l.DataDir, strconv.FormatInt(env.Match, 10)))
		if err == nil {
			err = os.MkdirAll(dir, 0o755)
		}
		if err == nil {
			out, err = os.Create(filepath.Join(dir, "stdout.txt"))
		}
		if err != nil {
			return nil, fmt.Errorf("gmshost: host data folder: %v", err)
		}
		vars = append(vars, "DGGMS_DATA="+dir)
		cmd.Stdout, cmd.Stderr = out, out
	}
	if l.Options != "" {
		vars = append(vars, "DGGMS_OPTIONS="+l.Options)
	}
	vars = l.hostVars(vars)                 // under Wine: the data folder as a Wine path, Wine's defaults
	cmd.Env = append(os.Environ(), vars...) // later entries win (exec dedups the environment)
	if err := startInJob(cmd); err != nil { // H2b: the host dies with this process (job_windows.go)
		if out != nil {
			out.Close()
		}
		return nil, err
	}
	return &execProcess{cmd: cmd, out: out}, nil
}

type execProcess struct {
	cmd  *exec.Cmd
	out  *os.File // the host's stdout.txt (DataDir), closed once the host has exited
	once sync.Once
	code int
	err  error
}

func (p *execProcess) Pid() int    { return p.cmd.Process.Pid }
func (p *execProcess) Kill() error { return killHost(p.cmd.Process) } // job_*.go

func (p *execProcess) Wait() (int, error) {
	p.once.Do(func() {
		err := p.cmd.Wait()
		if p.out != nil {
			p.out.Close()
		}
		p.code = -1
		if p.cmd.ProcessState != nil {
			p.code = p.cmd.ProcessState.ExitCode()
		}
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			p.err = err
		}
	})
	return p.code, p.err
}

// ResolveExe finds Dawngate.exe for --gms-dir: dir itself if it is the exe, else dir\Dawngate.exe,
// dir\game\Dawngate.exe or dir\Dawngate\game\Dawngate.exe (client\run_854_server is the last kind).
func ResolveExe(dir string) (string, error) {
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		return filepath.Abs(dir)
	}
	for _, rel := range []string{"Dawngate.exe", filepath.Join("game", "Dawngate.exe"),
		filepath.Join("Dawngate", "game", "Dawngate.exe")} {
		p := filepath.Join(dir, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("gmshost: no Dawngate.exe in %s (looked for Dawngate.exe, game\\, Dawngate\\game\\)", dir)
}
