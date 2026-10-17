package gmshost

import "runtime"

// killedExitCode is the exit code a host killed by execProcess.Kill reports: on Windows Process.Kill is
// TerminateProcess with exit code 1; elsewhere it is SIGKILL, which os.ProcessState.ExitCode reports as -1.
var killedExitCode = func() int {
	if runtime.GOOS == "windows" {
		return 1
	}
	return -1
}()
