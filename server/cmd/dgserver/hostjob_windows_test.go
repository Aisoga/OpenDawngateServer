package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dawngate/server/internal/gmshost"
)

// H2b: a dgserver process that started hosts is killed, and its hosts are gone at once (the host job,
// gmshost/job_windows.go). dgserver and its hosts are this test binary in separate processes
// (serverProcess, fakeHost "hang"); everything stays on ephemeral loopback ports.

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

// killAndWatch kills proc and checks that the host is gone within 5 s.
func killAndWatch(t *testing.T, proc *os.Process, what string, host syscall.Handle, pid int) {
	t.Helper()
	if goneWithin(host, 0) {
		t.Fatalf("host pid %d ended before the %s was killed", pid, what)
	}
	start := time.Now()
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	if !goneWithin(host, 5*time.Second) {
		t.Fatalf("host pid %d still runs 5 s after the %s pid %d was killed", pid, what, proc.Pid)
	}
	t.Logf("host pid %d gone %v after the %s pid %d was killed", pid, time.Since(start).Round(time.Millisecond), what, proc.Pid)
}

// dgserver --agent in its own process starts a host for the main server (this process); the agent is
// killed: its host is gone at once, and the main server ends the match as aborted.
func TestHostsEndWithAgent(t *testing.T) {
	key := filepath.Join(t.TempDir(), "agent.key")
	if err := writeAgentKey(key); err != nil {
		t.Fatal(err)
	}
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), agentKey: key})
	if err != nil {
		t.Fatal(err)
	}
	defer in.stop()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentLog := t.TempDir()
	agent := exec.Command(exe, "--agent", in.hosts.Addr(), "--agent-key", key, "--agent-name", "hostB", "--gms-dir", exe,
		"--gms-ports", "20046-20046", "--host", "127.0.0.1", "--log", agentLog)
	agent.Env = append(os.Environ(), mainEnv+"=1", childHostEnv+"=hang")
	stderr, err := os.Create(filepath.Join(agentLog, "stderr.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	agent.Stdout, agent.Stderr = stderr, stderr
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		agent.Process.Kill()
		agent.Wait()
	}()
	for deadline := time.Now().Add(10 * time.Second); len(in.hosts.Sites()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(stderr.Name())
			t.Fatalf("the agent did not register; its output: %s", raw)
		}
	}
	ready, end := make(chan bool, 1), make(chan gmshost.Outcome, 1)
	game := gmshost.CreateGame{GameID: 1, Level: "SM_Map_v5", Mode: gmshost.ModePractice, Seed: 7919,
		Roster: []gmshost.RosterEntry{{User: 2000000001, Name: "player", Team: 1}}}
	mt, err := in.hosts.Start(game, gmshost.Events{Ready: func(*gmshost.Match, int) { ready <- true },
		End: func(_ *gmshost.Match, o gmshost.Outcome) { end <- o }})
	if err != nil {
		t.Fatal(err)
	}
	if mt.Site != "hostB" {
		t.Fatalf("the match runs on %q, not the agent", mt.Site)
	}
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("the agent's host was not ready in 20 s")
	}
	host := watchHost(t, mt.Pid())
	killAndWatch(t, agent.Process, "agent", host, mt.Pid())
	select {
	case o := <-end:
		if o.Finished || !o.Ready {
			t.Errorf("outcome %+v", o)
		}
		t.Logf("the main server ended the match: %s (exit code %d)", o.Reason, o.ExitCode)
	case <-time.After(15 * time.Second):
		t.Error("the main server did not end the match")
	}
}

// A main server with local hosts (start with --gms-dir) in its own process is killed: its host is gone
// at once.
func TestHostsEndWithServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(exe)
	srv.Env = append(os.Environ(), localEnv+"="+testCerts(t)+"|"+t.TempDir(), childHostEnv+"=hang")
	stdout, err := srv.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		srv.Process.Kill()
		srv.Wait()
	}()
	var said []string // the server's log goes to stdout too
	pid := 0
	for sc := bufio.NewScanner(stdout); pid == 0 && sc.Scan(); {
		said = append(said, sc.Text())
		if _, err := fmt.Sscanf(sc.Text(), "host %d", &pid); err != nil {
			pid = 0
		}
	}
	if pid == 0 {
		t.Fatalf("the server ended without a host:\n%s", strings.Join(said, "\n"))
	}
	killAndWatch(t, srv.Process, "main server", watchHost(t, pid), pid)
}
