package gmshost

import (
	"os"
	"os/exec"
	"syscall"
)

// H4a: a host on Linux runs under Wine (wine.go). Like the job object on Windows (job_windows.go, H2b)
// it must end with the process that started it, in any way:
//   - Pdeathsig SIGKILL: the kernel kills the host (the wine process, which is the Windows process
//     itself) when its parent ends, also when dgserver crashes or is killed. (Go ties it to the thread
//     that started the child; Go's threads outlive their goroutines, so this holds.)
//   - Setpgid: the host gets a process group of its own, so killHost also ends what it started (a
//     winedbg after a crash, a launcher), and a Ctrl+C in dgserver's terminal reaches dgserver alone,
//     which then ends its hosts itself.
// The wineserver and Wine's helper processes (services.exe, explorer.exe, ...) are shared by every
// Wine process of the prefix and end by themselves a few seconds after its last process.

// startInJob starts cmd in a process group of its own, killed when this process ends.
func startInJob(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return cmd.Start()
}

// killHost kills the host's process group (the host and whatever it started), else the host alone.
func killHost(p *os.Process) error {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return p.Kill()
}
