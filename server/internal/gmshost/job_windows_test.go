package gmshost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// H2b: the host job (job_windows.go). The hosts here are this test binary in "sleep" mode.

const processQueryLimitedInformation = 0x1000

var (
	procIsProcessInJob            = kernel32.NewProc("IsProcessInJob")
	procQueryInformationJobObject = kernel32.NewProc("QueryInformationJobObject")
)

// inJob reports whether pid runs in job (0: in any job).
func inJob(t *testing.T, pid int, job syscall.Handle) bool {
	t.Helper()
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess %d: %v", pid, err)
	}
	defer syscall.CloseHandle(h)
	var in int32
	if r, _, err := procIsProcessInJob.Call(uintptr(h), uintptr(job), uintptr(unsafe.Pointer(&in))); r == 0 {
		t.Fatalf("IsProcessInJob %d: %v", pid, err)
	}
	return in != 0
}

// watchHost opens pid so the test can wait for its end; the handle keeps the pid from being reused,
// and the cleanup kills the process if it is still there (a failed test leaves nothing running).
func watchHost(t *testing.T, pid int) syscall.Handle {
	t.Helper()
	h, err := syscall.OpenProcess(syscall.SYNCHRONIZE|syscall.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess %d: %v", pid, err)
	}
	t.Cleanup(func() {
		syscall.TerminateProcess(h, 1)
		syscall.CloseHandle(h)
	})
	return h
}

// goneWithin waits up to d for the process to end.
func goneWithin(h syscall.Handle, d time.Duration) bool {
	ev, err := syscall.WaitForSingleObject(h, uint32(d/time.Millisecond))
	return err == nil && ev == syscall.WAIT_OBJECT_0
}

// The Windows structs match winnt.h/tlhelp32.h on this architecture (run with GOARCH=386 too).
func TestJobLayout(t *testing.T) {
	size, io := uintptr(144), uintptr(64)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		size, io = 112, 48
	}
	var info jobExtendedLimit
	if got := unsafe.Sizeof(info); got != size {
		t.Errorf("JOBOBJECT_EXTENDED_LIMIT_INFORMATION: %d bytes, want %d", got, size)
	}
	if got := unsafe.Offsetof(info.IoInfo); got != io {
		t.Errorf("IoInfo at %d, want %d", got, io)
	}
	if got := unsafe.Offsetof(info.Basic.LimitFlags); got != 16 {
		t.Errorf("LimitFlags at %d, want 16", got)
	}
	if got := unsafe.Sizeof(threadEntry32{}); got != 28 {
		t.Errorf("THREADENTRY32: %d bytes, want 28", got)
	}
}

// An ExecLauncher's host runs (it was resumed) inside the host job, and the job kills on close.
func TestExecLauncherJob(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(helperEnv, "sleep")
	out := &syncBuf{}
	p, err := (&ExecLauncher{Path: exe, Args: []string{"-test.run=^$"}, Out: out}).Launch(Env{Match: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Kill()
		p.Wait()
	}()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "running"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the host does not run (still suspended?)")
		}
	}
	job, err := hostJob()
	if err != nil {
		t.Fatal(err)
	}
	if !inJob(t, p.Pid(), job) {
		t.Error("the host is not in the host job")
	}
	if inJob(t, os.Getpid(), job) {
		t.Error("the launching process is in its own host job")
	}
	var info jobExtendedLimit
	if r, _, err := procQueryInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInfo,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0); r == 0 {
		t.Fatalf("QueryInformationJobObject: %v", err)
	}
	if info.Basic.LimitFlags != jobObjectLimitKillOnJobClose {
		t.Errorf("job limit flags %#x, want %#x", info.Basic.LimitFlags, jobObjectLimitKillOnJobClose)
	}
	// Informational: a test run from a shell that keeps its children in a job (as the agent harness
	// here does) makes the host job a nested one.
	t.Logf("this test process runs in a job: %v", inJob(t, os.Getpid(), 0))
}

// A process that started a host with an ExecLauncher is killed: its host is gone at once. Also with
// the launcher inside another job (as under a shell or service manager that keeps its children in
// one): the host is born in that job, and its host job becomes a nested job (Windows 8 and later).
func TestHostsEndWithLauncher(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "alone", true: "in a job"}[nested], func(t *testing.T) {
			cmd := exec.Command(exe, "-test.run=^$")
			cmd.Env = append(os.Environ(), launcherEnv+"=1")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var outer syscall.Handle
			if nested {
				// the outer job kills on close too, so a failed test leaves nothing behind
				h, _, err := procCreateJobObjectW.Call(0, 0)
				if h == 0 {
					t.Fatal(err)
				}
				outer = syscall.Handle(h)
				defer syscall.CloseHandle(outer)
				var info jobExtendedLimit
				info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
				if r, _, err := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInfo,
					uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
					t.Fatal(err)
				}
				cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createSuspended}
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cmd.Process.Kill()
				cmd.Wait()
			}()
			if nested {
				if err := assignAndResume(outer, uint32(cmd.Process.Pid)); err != nil {
					t.Fatal(err)
				}
			}
			line, err := bufio.NewReader(stdout).ReadString('\n')
			var pid int
			if _, perr := fmt.Sscanf(line, "host %d", &pid); err != nil || perr != nil {
				t.Fatalf("launcher said %q (%v)", line, err)
			}
			host := watchHost(t, pid)
			if goneWithin(host, 0) {
				t.Fatal("the host ended before its launcher was killed")
			}
			if nested && !inJob(t, pid, outer) {
				t.Error("the host is not in its launcher's outer job")
			}
			start := time.Now()
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if !goneWithin(host, 5*time.Second) {
				t.Fatalf("host pid %d still runs 5 s after its launcher was killed", pid)
			}
			t.Logf("host pid %d gone %v after its launcher pid %d was killed", pid, time.Since(start).Round(time.Millisecond), cmd.Process.Pid)
		})
	}
}
