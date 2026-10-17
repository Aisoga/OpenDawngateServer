//go:build !windows && !linux

package gmshost

import (
	"os"
	"os/exec"
)

// startInJob just starts cmd on other systems (job_windows.go has the job object that ends the hosts
// with this process, H2b; job_linux.go the process group and the parent-death signal, H4a).
func startInJob(cmd *exec.Cmd) error { return cmd.Start() }

// killHost kills the host process.
func killHost(p *os.Process) error { return p.Kill() }
