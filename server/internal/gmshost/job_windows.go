package gmshost

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// H2b: every host an ExecLauncher starts goes into one job object of this process, created once with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and never closed. Only this process holds a handle to it (the job
// has no name, and os/exec lets a child inherit nothing but its standard handles), so when this
// process ends for any reason (exit, crash, TerminateProcess) Windows closes the handle and kills
// every host still in the job, with the processes those hosts started themselves
// (docs/HOST_CONTROL.md, Remote host machines (H2), Hosts end with their starter (H2b)).
//
// No race with the child: it is created suspended (CREATE_SUSPENDED), assigned to the job and only
// then resumed, so it runs no instruction outside the job. os/exec closes the new thread's handle, so
// the resume goes through a thread snapshot (Toolhelp) of the child's pid. Nested jobs (Windows 8 and
// later; Go itself needs Windows 10) let a dgserver that already runs inside a job, e.g. a shell's or
// a service manager's, still put its hosts into its own.

const (
	createSuspended              = 0x00000004 // CREATE_SUSPENDED
	jobObjectExtendedLimitInfo   = 9          // JobObjectExtendedLimitInformation (JOBOBJECTINFOCLASS)
	jobObjectLimitKillOnJobClose = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	processSetQuota              = 0x0100     // PROCESS_SET_QUOTA (AssignProcessToJobObject needs it and PROCESS_TERMINATE)
	threadSuspendResume          = 0x0002     // THREAD_SUSPEND_RESUME
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW        = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
	procThread32First           = kernel32.NewProc("Thread32First")
	procThread32Next            = kernel32.NewProc("Thread32Next")
	procOpenThread              = kernel32.NewProc("OpenThread")
	procResumeThread            = kernel32.NewProc("ResumeThread")
)

// jobBasicLimit is JOBOBJECT_BASIC_LIMIT_INFORMATION and jobExtendedLimit
// JOBOBJECT_EXTENDED_LIMIT_INFORMATION (winnt.h). MSVC aligns the 64-bit members to 8 bytes, Go on
// 386 to 4, so on 386 the basic part needs 4 bytes of padding before IoInfo: 144 bytes on amd64,
// 112 on 386 (job_windows_test.go checks both; SetInformationJobObject refuses any other length).
type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	_                     [8 - unsafe.Sizeof(uintptr(0))]byte // 4 on 386, none on amd64
	IoInfo                [6]uint64                           // IO_COUNTERS
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// threadEntry32 is THREADENTRY32 (tlhelp32.h).
type threadEntry32 struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePri        int32
	DeltaPri       int32
	Flags          uint32
}

var (
	jobOnce   sync.Once
	jobHandle syscall.Handle
	jobErr    error
)

// hostJob creates this process's host job once (kill on close) and keeps it open for the process's
// lifetime. Closing it would kill every host in it.
func hostJob() (syscall.Handle, error) {
	jobOnce.Do(func() {
		h, _, err := procCreateJobObjectW.Call(0, 0) // no security attributes (not inheritable), no name
		if h == 0 {
			jobErr = fmt.Errorf("gmshost: CreateJobObject: %v", err)
			return
		}
		var info jobExtendedLimit
		info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
		if r, _, err := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInfo,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
			syscall.CloseHandle(syscall.Handle(h))
			jobErr = fmt.Errorf("gmshost: SetInformationJobObject (kill on job close): %v", err)
			return
		}
		jobHandle = syscall.Handle(h)
	})
	return jobHandle, jobErr
}

// startInJob starts cmd suspended, puts it into the host job and resumes it. If that fails the
// process is killed and reaped, and the launch fails: a host outside the job could outlive us.
func startInJob(cmd *exec.Cmd) error {
	job, err := hostJob()
	if err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := assignAndResume(job, uint32(cmd.Process.Pid)); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("gmshost: host job: %v", err)
	}
	return nil
}

// assignAndResume puts the suspended process pid into job and resumes its thread. The pid is safe to
// open: cmd.Process holds a handle to the process until it is reaped, so the pid isn't reused.
func assignAndResume(job syscall.Handle, pid uint32) error {
	h, err := syscall.OpenProcess(processSetQuota|syscall.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return fmt.Errorf("OpenProcess %d: %v", pid, err)
	}
	defer syscall.CloseHandle(h)
	if r, _, err := procAssignProcessToJob.Call(uintptr(job), uintptr(h)); r == 0 {
		return fmt.Errorf("AssignProcessToJobObject %d: %v", pid, err)
	}
	return resumeThreads(pid)
}

// resumeThreads resumes every thread of pid (a process created suspended has exactly one: its main
// thread; the loader's worker threads start only once that runs).
func resumeThreads(pid uint32) error {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %v", err)
	}
	defer syscall.CloseHandle(snap)
	te := threadEntry32{Size: uint32(unsafe.Sizeof(threadEntry32{}))}
	resumed := 0
	r, _, err := procThread32First.Call(uintptr(snap), uintptr(unsafe.Pointer(&te)))
	for ; r != 0; r, _, err = procThread32Next.Call(uintptr(snap), uintptr(unsafe.Pointer(&te))) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, _, oerr := procOpenThread.Call(threadSuspendResume, 0, uintptr(te.ThreadID))
		if th == 0 {
			return fmt.Errorf("OpenThread %d: %v", te.ThreadID, oerr)
		}
		n, _, rerr := procResumeThread.Call(th)
		syscall.CloseHandle(syscall.Handle(th))
		if uint32(n) == ^uint32(0) {
			return fmt.Errorf("ResumeThread %d: %v", te.ThreadID, rerr)
		}
		resumed++
	}
	if resumed == 0 {
		return fmt.Errorf("no thread of pid %d in the snapshot (%v)", pid, err)
	}
	return nil
}

// killHost kills the host process (what it started itself stays in the host job until this process ends).
func killHost(p *os.Process) error { return p.Kill() }
