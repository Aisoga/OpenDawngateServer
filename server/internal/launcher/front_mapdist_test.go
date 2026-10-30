package launcher

import (
	"bytes"
	"crypto/sha256"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/dgserver"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/mapdist"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/testmaps"
	"dawngate/server/internal/tunnel"
)

// copyFixture copies a fixture map package into dir.
func copyFixture(t *testing.T, name, dir string) {
	t.Helper()
	src := testmaps.Pkg(name)
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dir, name, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// serveMaps starts a dgserver with the tunnel on address, a maps folder with CM_Marsh and CM_Ring offered as custom
// maps and a made-up host copy whose archives are "host <name>" (the versions); it returns the gateway and its pin.
func serveMaps(t *testing.T, address string, extra ...string) (string, tunnel.Pin) {
	t.Helper()
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	maps := t.TempDir()
	copyFixture(t, "CM_Marsh", maps)
	copyFixture(t, "CM_Ring", maps)
	host := t.TempDir()
	data := filepath.Join(host, "Dawngate", "game", "Data")
	os.MkdirAll(data, 0o755)
	os.WriteFile(filepath.Join(host, "Dawngate", "game", "Dawngate.exe"), nil, 0o644)
	for _, n := range []string{"CM_Marsh", "CM_Ring"} {
		os.WriteFile(filepath.Join(data, mapc.File(n)), []byte("host "+n), 0o644)
	}
	dir := filepath.Join(t.TempDir(), "certs")
	if _, err := certs.Make(dir, address); err != nil {
		t.Fatal(err)
	}
	stop, started, done := make(chan struct{}), make(chan dgserver.Info, 1), make(chan int, 1)
	go func() {
		done <- dgserver.Run(append([]string{"--bind", address, "--certs", dir, "--log", t.TempDir(), "--data", "",
			"--broadcast-file", "", "--tunnel", "on", "--gateway-port", "0", "--maps", maps, "--gms-dir", host,
			"--custom-maps", "CM_Ring,CM_Marsh,MM_Map_v3", "--gms-control-bind", address}, extra...), dgserver.Hooks{Stop: stop,
			Started: func(i dgserver.Info) { started <- i }, StartFailed: func(error) { started <- dgserver.Info{} }})
	}()
	info := <-started
	if info.GatewayPort == 0 {
		t.Fatal("dgserver didn't start with a gateway")
	}
	t.Cleanup(func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("dgserver didn't stop")
		}
	})
	pin, err := tunnel.PinFromCertFile(filepath.Join(dir, "gateway_cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort(address, strconv.Itoa(info.GatewayPort)), pin
}

// hostVersion stands for the archive a package compiles to: the made-up host copy's bytes for its name.
func hostVersion(string) mapdist.Verifier {
	return func(m *mapc.Map, _ []mapc.Other) ([32]byte, error) {
		return sha256.Sum256([]byte("host " + m.Name)), nil
	}
}

// deliverOnce runs the front's delivery for a session to gw (the facts its probe reads) and waits for it.
func deliverOnce(t *testing.T, f *Front, gw string, pin tunnel.Pin) {
	t.Helper()
	info, err := tunnel.ProbeInfo(gw, 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &tunnel.Client{Gateway: gw, Pin: pin, TunnelIP: net.ParseIP("127.0.0.1"), External: true,
		Deliver: func(int, *net.UDPAddr, []byte) {}}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	host, port, _ := net.SplitHostPort(gw)
	p, _ := strconv.Atoi(port)
	f.deliverMaps(&frontSession{b: Bookmark{Tag: "srv", Address: host, GatewayPort: p}, c: c, facts: info.Facts})
	f.WaitMapDelivery()
}

// TestFrontMapDelivery: a real dgserver hands out its maps through its tunnel; at a login the front fetches the two
// the copy lacks into its maps folder (the lines say to restart), the next login finds them there, the copy with the
// server's ring archive asks only for the marsh, and a server with --serve-maps off says it hands none out.
func TestFrontMapDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.93's and 127.0.9.94's ports")
	}
	gw, pin := serveMaps(t, "127.0.9.93")
	copyDir := filepath.Join(t.TempDir(), "Dawngate")
	data := filepath.Join(copyDir, "game", "Data")
	os.MkdirAll(data, 0o755)
	maps := t.TempDir()
	var mu sync.Mutex
	var lines []string
	var log bytes.Buffer
	f := &Front{Log: logging.New(&log), listCopy: copyDir, Ports: DefaultFrontPorts, TunnelIP: net.ParseIP("127.0.0.1"),
		MapDownload: &MapDownload{Dir: maps, Limits: mapdist.DefaultLimits(), Verify: hostVersion,
			Notify: func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() }}}
	deliverOnce(t, f, gw, pin)
	for _, n := range []string{"CM_Marsh", "CM_Ring"} {
		if !mapdist.Delivered(filepath.Join(maps, n)) {
			t.Errorf("%s not delivered\n%s", n, log.String())
		}
	}
	if b, _ := os.ReadFile(filepath.Join(maps, "CM_Marsh", "ground", "marsh_moss.png")); !bytes.Equal(b, func() []byte {
		x, _ := os.ReadFile(filepath.Join(testmaps.Pkg("CM_Marsh"), "ground", "marsh_moss.png"))
		return x
	}()) {
		t.Error("the marsh's picture isn't the server's")
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"srv: downloading CM_Marsh (", "Marsh from srv: downloaded and checked into " +
		filepath.Join(maps, "CM_Marsh") + "; close the game and start Start-Client.exe again to install it",
		"Test Ring from srv: downloaded and checked"} {
		if !strings.Contains(all, want) {
			t.Errorf("no line %q in\n%s", want, all)
		}
	}
	if !strings.Contains(log.String(), "map delivery: srv offers ~#marsh, ~#ring, which the copy lacks") {
		t.Errorf("the log:\n%s", log.String())
	}

	lines = nil
	deliverOnce(t, f, gw, pin) // the folder has them now (the copy's archives come at the next start)
	if all := strings.Join(lines, "\n"); strings.Count(all, "this server's version is in the maps folder already") != 2 {
		t.Errorf("again:\n%s", all)
	}

	// the copy has the server's ring archive: only the marsh is asked for
	os.WriteFile(filepath.Join(data, "zz_CM_Ring.snap"), []byte("host CM_Ring"), 0o644)
	pkgs, _ := mapc.ReadFolder(maps)
	f.Maps = setup.DialogMaps(pkgs)
	lines = nil
	deliverOnce(t, f, gw, pin)
	if all := strings.Join(lines, "\n"); strings.Contains(all, "Test Ring") || !strings.Contains(all, "Marsh from srv") {
		t.Errorf("with the ring:\n%s", all)
	}

	gwOff, pinOff := serveMaps(t, "127.0.9.94", "--serve-maps", "off")
	lines = nil
	f.Maps = nil
	deliverOnce(t, f, gwOff, pinOff)
	if all := strings.Join(lines, "\n"); strings.Count(all, "the server doesn't hand out its maps") != 2 {
		t.Errorf("serve-maps off:\n%s", all)
	}
}
