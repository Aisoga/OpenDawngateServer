package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/exepatch"
)

// remoteAddress stands in for another machine's server: a loopback address no test lane uses.
const remoteAddress = "127.0.9.3"

// serveOn runs dgserver with --serve-ca and a fresh certificate set for address until the test ends; it returns the
// set's CA modulus.
func serveOn(t *testing.T, address string) []byte {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "certs")
	set, err := certs.Make(dir, address)
	if err != nil {
		t.Fatal(err)
	}
	stop, started, done := make(chan struct{}), make(chan bool, 1), make(chan int, 1)
	go func() {
		done <- dgserver.Run([]string{"--bind", address, "--certs", dir, "--log", t.TempDir(), "--data", "", "--serve-ca",
			"--broadcast-file", ""}, dgserver.Hooks{Stop: stop,
			Started: func(dgserver.Info) { started <- true }, StartFailed: func(error) { started <- false }})
	}()
	if !<-started {
		t.Fatal("dgserver didn't start")
	}
	t.Cleanup(func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("dgserver didn't stop")
		}
	})
	return set.CAModulus
}

// Start-Client against "another machine's" server: the CA fetched over HTTP, refused until trusted, trusted with
// --trust (or the player's y), remembered, a changed CA refused again; the copy patched with the server's CA.
func TestStartClientRemote(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.3's server ports")
	}
	orig := remoteServer
	remoteServer = func(a string) bool { return a == remoteAddress }
	defer func() { remoteServer = orig }()
	dir := release(t) // a player's release folder with Dawngate.zip; no Start-Server.ini
	store := filepath.Join(dir, DefaultFolder, "servers", remoteAddress, certs.CAModulusFile)

	code, out := run(t, RunClient, dir, "--non-interactive", remoteAddress)
	if code != 1 || !strings.Contains(out, "No server answers at "+remoteAddress) {
		t.Fatalf("no server: exit %d\n%s", code, out)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", remoteAddress)
	if code != 1 || !strings.Contains(out, "its CA isn't known yet") {
		t.Fatalf("no server, --setup-only: exit %d\n%s", code, out)
	}
	ca := serveOn(t, remoteAddress)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", remoteAddress)
	if code != 1 || !strings.Contains(out, "isn't trusted yet") || !strings.Contains(out, certs.Fingerprint(ca)) || exists(store) {
		t.Fatalf("an unconfirmed CA: exit %d\n%s", code, out)
	}
	// the player answers y (an interactive run: the question with the fingerprint)
	var buf bytes.Buffer
	code = RunClient([]string{"--setup-only", remoteAddress}, IO{In: strings.NewReader("y\n"), Out: &buf, ExeDir: dir, Interactive: true})
	out = buf.String()
	if code != 0 || !strings.Contains(out, "First connection to the server "+remoteAddress) ||
		!strings.Contains(out, "trusting the server's CA "+certs.Fingerprint(ca)) {
		t.Fatalf("confirmed: exit %d\n%s", code, out)
	}
	if got, _ := os.ReadFile(store); !bytes.Equal(got, ca) {
		t.Error("the CA isn't stored")
	}
	l := setup.Layout{Root: filepath.Join(dir, DefaultFolder)}
	exe, _ := os.ReadFile(filepath.Join(l.ClientCopy(), "game", "Dawngate.exe"))
	if d, _ := exepatch.Describe(exe); d["ca_sha256"] != certs.Fingerprint(ca) || d["redirector"] != remoteAddress {
		t.Errorf("client exe %v", d)
	}
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", remoteAddress)
	if code != 0 || strings.Contains(out, "trusting") {
		t.Errorf("a known CA: exit %d\n%s", code, out)
	}
}

// A server that was set up anew (another CA) is refused until the player confirms the change; --ca with the
// operator's file trusts it at once.
func TestStartClientRemoteChangedCA(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.3's server ports")
	}
	orig := remoteServer
	remoteServer = func(a string) bool { return a == remoteAddress }
	defer func() { remoteServer = orig }()
	dir := release(t)
	store := filepath.Join(dir, DefaultFolder, "servers", remoteAddress, certs.CAModulusFile)
	os.MkdirAll(filepath.Dir(store), 0o755)
	old := bytes.Repeat([]byte{0x5A}, 128)
	os.WriteFile(store, old, 0o644)
	ca := serveOn(t, remoteAddress)
	code, out := run(t, RunClient, dir, "--non-interactive", "--setup-only", remoteAddress)
	if code != 1 || !strings.Contains(out, "has another CA") {
		t.Fatalf("a changed CA: exit %d\n%s", code, out)
	}
	file := filepath.Join(t.TempDir(), "ca_modulus.bin")
	os.WriteFile(file, ca, 0o644)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--ca", file, remoteAddress)
	if code != 0 || !strings.Contains(out, "new CA") {
		t.Fatalf("--ca: exit %d\n%s", code, out)
	}
	// a handed-out file that isn't the server's CA: the redirector's certificate doesn't verify
	os.WriteFile(file, old, 0o644)
	code, out = run(t, RunClient, dir, "--non-interactive", "--setup-only", "--ca", file, remoteAddress)
	if code != 1 || !strings.Contains(out, "isn't signed by this CA") {
		t.Fatalf("a wrong --ca: exit %d\n%s", code, out)
	}
}
