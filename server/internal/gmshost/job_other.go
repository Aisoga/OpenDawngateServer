//go:build !windows

package gmshost

import "os/exec"

// startInJob just starts cmd outside Windows (the host is a Windows program; job_windows.go has the
// job object that ends the hosts with this process, H2b).
func startInJob(cmd *exec.Cmd) error { return cmd.Start() }
