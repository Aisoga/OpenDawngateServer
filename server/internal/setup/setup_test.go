package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"dawngate/server/internal/content"
	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/hostcopy"
	"dawngate/server/internal/setup/localepatch"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/synthzip"
)

// FakeHostFiles are a stand-in for the release's host folder: a "dgsim.dll" with the marker hostcopy looks for and
// two Lua probes.
func fakeHostFiles() hostcopy.Files {
	return hostcopy.Files{FS: fstest.MapFS{
		"dgsim.dll":           {Data: []byte("MZ fake dgsim.dll loaded"), ModTime: synthzip.When},
		"lua/dgsim_bots.lua":  {Data: []byte("-- bots\n")},
		"lua/dgsim_probe.lua": {Data: []byte("-- a probe\r\n")},
		"lua/readme.txt":      {Data: []byte("not a probe")},
		"lua/sub/deeper.lua":  {Data: []byte("-- not installed")},
	}, DLL: "dgsim.dll", Lua: "lua"}
}

// newSetup is a Setup on a fresh folder with the synthetic archive.
func newSetup(t *testing.T) (*Setup, string) {
	t.Helper()
	zp, m := synthzip.Zip(t, t.TempDir())
	s := &Setup{Layout: Layout{Root: filepath.Join(t.TempDir(), "setup")}, Out: &bytes.Buffer{}, Manifest: m,
		FindZip: func() (string, error) { return zp, nil }, HostFiles: fakeHostFiles()}
	return s, zp
}

// status maps each step to its last status.
func status(s *Setup) map[string]Status {
	m := map[string]Status{}
	for _, l := range s.Lines() {
		m[l.Step] = l.Status
	}
	return m
}

func expect(t *testing.T, s *Setup, want map[string]Status) {
	t.Helper()
	got := status(s)
	bad := false
	for step, st := range want {
		if got[step] != st {
			t.Errorf("%s: %s, want %s", step, got[step], st)
			bad = true
		}
	}
	if bad {
		t.Logf("report:\n%s", s.Out.(*bytes.Buffer).String())
	}
}

func again(s *Setup) *Setup {
	return &Setup{Layout: s.Layout, Out: &bytes.Buffer{}, Manifest: s.Manifest, FindZip: s.FindZip, HostFiles: s.HostFiles,
		WebDomain: s.WebDomain}
}

var allSteps = []string{"certificates", "client copy", "client exe", "client config", "client texts", "host copy",
	"host exe", "host config", "host DLL", "content"}

// synthLocal is the client copy's AA_Server.package for 127.0.0.1 from the synthetic archive's Locale.package: what
// re/scripts/patch_locale.py makes from the same file (test_ph2b.py and localepatch_test.go pin the same SHA-256).
const synthLocal = "476b2381284742ffbb866771e082af47beb87900b1567f35fc15c2cf3d420a7f"

func localeSHA(t *testing.T, copy string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(copy, filepath.FromSlash(localepatch.Rel)))
	if err != nil {
		return err.Error()
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func every(st Status) map[string]Status {
	m := map[string]Status{}
	for _, s := range allSteps {
		m[s] = st
	}
	return m
}

func TestServerSetupAndRerun(t *testing.T) {
	s, _ := newSetup(t)
	if err := s.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatalf("Server: %v\n%s", err, s.Out.(*bytes.Buffer))
	}
	expect(t, s, every(Done))
	// the client exe is what the patcher makes from the untouched exe with this install's CA
	modulus, err := os.ReadFile(filepath.Join(s.Certs(Local), certs.CAModulusFile))
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := exepatch.Patch(exepatch.SyntheticExe(), modulus, exepatch.Options{ServerHost: Local})
	if err != nil {
		t.Fatal(err)
	}
	for _, copy := range []string{s.ClientCopy(), s.HostCopy()} {
		got, err := os.ReadFile(filepath.Join(copy, "game", "Dawngate.exe"))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: not the patched exe (%v)", copy, err)
		}
		orig, _ := os.ReadFile(filepath.Join(copy, "game", "Dawngate.exe.orig"))
		if !bytes.Equal(orig, exepatch.SyntheticExe()) {
			t.Errorf("%s: no untouched .orig", copy)
		}
		cfg, _ := os.ReadFile(filepath.Join(copy, "game", "Data", "Config.package"))
		if !bytes.Contains(cfg, []byte("http://127.0.0.1:18080/battlegrounds_test/")) {
			t.Errorf("%s: Config.package not patched", copy)
		}
	}
	game := filepath.Join(s.HostCopy(), "game")
	if b, _ := os.ReadFile(filepath.Join(game, hostcopy.Loader)); !bytes.Contains(b, []byte("dgsim.dll loaded")) {
		t.Error("no dgsim.dll in the host copy")
	}
	if b, _ := os.ReadFile(filepath.Join(game, hostcopy.LoaderOrig)); !bytes.Equal(b, synthzip.Loader) {
		t.Error("the game's loader isn't kept")
	}
	for _, f := range []string{"dgsim_bots.lua", "dgsim_probe.lua", hostcopy.Ini} {
		if !isFile(filepath.Join(game, f)) {
			t.Errorf("host copy: no %s", f)
		}
	}
	if got := localeSHA(t, s.ClientCopy()); got != synthLocal {
		t.Errorf("client texts: %s, patch_locale.py's %s", got, synthLocal)
	}
	if isFile(filepath.Join(s.HostCopy(), filepath.FromSlash(localepatch.Rel))) {
		t.Error("the host copy got the client texts")
	}
	if c, err := content.Load(s.Content()); err != nil || c.Format != content.Format {
		t.Errorf("content: %v", err)
	}
	for _, leftover := range []string{"client.partial", "client.extract", "host.partial", "host.extract"} {
		if exists(filepath.Join(s.Root, leftover)) {
			t.Errorf("%s left behind", leftover)
		}
	}

	s2 := again(s)
	if err := s2.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	expect(t, s2, every(OK))
}

func TestCheckWritesNothing(t *testing.T) {
	s, _ := newSetup(t)
	s.Check = true
	if err := s.Server(ServerOptions{Address: Local}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Check on an empty folder: %v", err)
	}
	expect(t, s, map[string]Status{"certificates": Missing, "client copy": Missing, "host copy": Missing,
		"host DLL": Missing, "content": Missing})
	if exists(s.Root) {
		t.Error("Check wrote the setup folder")
	}
	s2 := again(s)
	s2.Server(ServerOptions{Address: Local})
	// a changed Lua probe and a config package put back: Check sees both and changes nothing
	game := filepath.Join(s.HostCopy(), "game")
	if err := os.WriteFile(filepath.Join(game, "dgsim_bots.lua"), []byte("-- old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(s.ClientCopy(), "game", "Data", "Config_Ship.package")
	orig, _ := os.ReadFile(cfg + ".orig")
	if err := os.WriteFile(cfg, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	s3 := again(s)
	s3.Check = true
	if err := s3.Server(ServerOptions{Address: Local}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Check: %v", err)
	}
	want := every(OK)
	want["host DLL"], want["client config"] = Stale, Stale
	expect(t, s3, want)
	if b, _ := os.ReadFile(cfg); !bytes.Equal(b, orig) {
		t.Error("Check patched")
	}
	s4 := again(s)
	if err := s4.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatal(err)
	}
	want = every(OK)
	want["host DLL"], want["client config"] = Done, Done
	expect(t, s4, want)
}

func TestLostFilesAreRestored(t *testing.T) {
	s, _ := newSetup(t)
	s.Server(ServerOptions{Address: Local})
	game := filepath.Join(s.ClientCopy(), "game")
	for _, f := range []string{"readme.txt", "Dawngate.exe", "Dawngate.exe.orig"} {
		if err := os.Remove(filepath.Join(game, f)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(game, "Data", "AssetData.snap"), []byte("cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := again(s)
	c.Check = true
	c.Server(ServerOptions{Address: Local})
	expect(t, c, map[string]Status{"client copy": Stale})

	s2 := again(s)
	if err := s2.Server(ServerOptions{Address: Local}); err != nil {
		t.Fatal(err)
	}
	expect(t, s2, map[string]Status{"client copy": Done, "client exe": Done, "host copy": OK})
	if out := s2.Out.(*bytes.Buffer).String(); !strings.Contains(out, "3 file(s) restored") {
		t.Errorf("report:\n%s", out)
	}
	if bad, _ := archive.CheckCopy(s.ClientCopy(), s.Manifest, archive.PatchedFiles); len(bad) != 0 {
		t.Errorf("still missing: %v", bad)
	}
	// without the archive the same loss is reported, not repaired
	if err := os.Remove(filepath.Join(game, "readme.txt")); err != nil {
		t.Fatal(err)
	}
	s3 := again(s)
	s3.FindZip = func() (string, error) { return "", ErrNoZip }
	s3.Server(ServerOptions{Address: Local})
	expect(t, s3, map[string]Status{"client copy": Missing})
}

func TestAddressChange(t *testing.T) {
	s, _ := newSetup(t)
	s.Server(ServerOptions{Address: Local})
	s2 := again(s)
	if err := s2.Server(ServerOptions{Address: "127.0.9.1"}); err != nil {
		t.Fatal(err)
	}
	// a set of its own; the client copy patched for it, the host copy kept (only its redirector and data folders count)
	expect(t, s2, map[string]Status{"certificates": Done, "client exe": Done, "client config": Done, "client texts": Done,
		"host exe": OK, "host config": OK})
	exe, _ := os.ReadFile(filepath.Join(s.ClientCopy(), "game", "Dawngate.exe"))
	d, err := exepatch.Describe(exe)
	if err != nil || d["redirector"] != "127.0.9.1" || d["bind"] != "127.0.9.1" {
		t.Errorf("client exe %v %v", d, err)
	}
	if _, err := certs.Check(s.Certs(Local), Local); err != nil {
		t.Errorf("the first set is gone: %v", err)
	}
	// and back: the first set is still there, the copy is patched for it again
	s3 := again(s)
	s3.Server(ServerOptions{Address: Local})
	expect(t, s3, map[string]Status{"certificates": OK, "client exe": Done, "client config": Done, "client texts": Done})
	if got := localeSHA(t, s.ClientCopy()); got != synthLocal {
		t.Errorf("client texts back for %s: %s", Local, got)
	}
}

func TestClientForAnotherServer(t *testing.T) {
	s, _ := newSetup(t)
	set, err := certs.Generate("192.168.1.20", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Client("192.168.1.20", set.CAModulus); err != nil {
		t.Fatalf("%v:\n%s", err, s.Out.(*bytes.Buffer).String())
	}
	expect(t, s, map[string]Status{"client copy": Done, "client exe": Done, "client config": Done, "client texts": Done,
		"bot check box": Done, "chat rooms": Done, "after action review": Done, "shop refund": Done, "halloween": Done,
		"collection items": Done})
	aar := moviepatch.AfterAction
	aar.WebHost = "192.168.1.20" // the review's pages come from the copy's server
	for _, sp := range []moviepatch.Spec{moviepatch.MatchOptions, moviepatch.ChatRooms, aar, moviepatch.ItemShop} {
		src, _ := os.ReadFile(filepath.Join(s.ClientCopy(), filepath.FromSlash(sp.Source)))
		want, err := sp.Build(src)
		if b, err2 := os.ReadFile(filepath.Join(s.ClientCopy(), filepath.FromSlash(sp.Rel))); err != nil ||
			err2 != nil || !bytes.Equal(b, want) {
			t.Errorf("%s: %v, %v", sp.What, err, err2)
		}
	}
	// the Collection's items: the movie and the item list of the copy's own game data
	if b, err := os.ReadFile(filepath.Join(s.ClientCopy(), filepath.FromSlash(moviepatch.Collection.Rel))); err != nil {
		t.Errorf("%s: %v", moviepatch.Collection.What, err)
	} else if a, err := snap.Parse(b); err != nil || len(a.Entries) != 2 || a.Entries[1].Name != moviepatch.ItemsPath {
		t.Errorf("%s: %v, %+v", moviepatch.Collection.What, err, a)
	}
	if b, _ := os.ReadFile(filepath.Join(s.ClientCopy(), filepath.FromSlash(localepatch.Rel))); !bytes.Contains(b,
		[]byte("0x00000003 http://192.168.1.20:18080/dawngate/password\r\n")) {
		t.Error("client texts: Forgot Password? doesn't link to the server's page")
	}
	exe, _ := os.ReadFile(filepath.Join(s.ClientCopy(), "game", "Dawngate.exe"))
	d, _ := exepatch.Describe(exe)
	if d["redirector"] != "192.168.1.20" || d["bind"] != "as-shipped" || d["ca_sha256"] != certs.Fingerprint(set.CAModulus) {
		t.Errorf("client exe %v", d)
	}
	cfg, _ := os.ReadFile(filepath.Join(s.ClientCopy(), "game", "Data", "Config_Ship.package"))
	if !bytes.Contains(cfg, []byte("192.168.1.20")) {
		t.Error("Config_Ship.package not patched for the server")
	}
	if exists(s.HostCopy()) || exists(filepath.Join(s.Root, "certs")) || exists(s.Data()) {
		t.Error("Client made more than the player copy")
	}
	s2 := again(s)
	if err := s2.Client("192.168.1.20", set.CAModulus); err != nil {
		t.Fatal(err)
	}
	expect(t, s2, map[string]Status{"client copy": OK, "client exe": OK, "client config": OK, "client texts": OK,
		"bot check box": OK, "chat rooms": OK, "after action review": OK, "halloween": OK})
	// a map's archive in the copy: the create dialog's map list names it (its dropdown), and drops it again
	mmArchive := filepath.Join(s.ClientCopy(), "game", "Data", "zz_MM_Map_v3.snap")
	added := filepath.Join(s.ClientCopy(), filepath.FromSlash(moviepatch.MatchOptions.Rel))
	for _, withMM := range []bool{true, false} {
		if withMM {
			if err := os.WriteFile(mmArchive, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(mmArchive); err != nil {
			t.Fatal(err)
		}
		sm := again(s)
		if err := sm.Client("192.168.1.20", set.CAModulus); err != nil {
			t.Fatal(err)
		}
		expect(t, sm, map[string]Status{"bot check box": Done, "chat rooms": OK, "after action review": OK})
		b, _ := os.ReadFile(added)
		a, err := snap.Parse(b)
		if err != nil || len(a.Entries) != 2 {
			t.Fatalf("%s: %v", added, err)
		}
		list, _ := a.Read(a.Entries[1])
		if got := bytes.Contains(list, []byte("map\tmm\tAnshavar Pass\n")); got != withMM {
			t.Errorf("with MM's archive %v: the dialog's map list names MM %v (%q)", withMM, got, list)
		}
	}
	// the website: the links go there
	s4 := again(s)
	s4.WebDomain = "Dawngate.Example.org"
	if err := s4.Client("192.168.1.20", set.CAModulus); err != nil {
		t.Fatal(err)
	}
	expect(t, s4, map[string]Status{"client exe": OK, "client texts": Done})
	if b, _ := os.ReadFile(filepath.Join(s.ClientCopy(), filepath.FromSlash(localepatch.Rel))); !bytes.Contains(b,
		[]byte("0x00000006 https://dawngate.example.org/signup\r\n")) {
		t.Error("client texts: Sign up now! doesn't link to the website")
	}
	s3 := again(s)
	if s3.Client("192.168.1.20", set.CAModulus[:100]); !s3.Failed() {
		t.Error("a short CA passed")
	}
	// picker mode: the login screen's server picker is added, current the next time, removed without picker mode
	picker := filepath.Join(s.ClientCopy(), filepath.FromSlash(moviepatch.Rel))
	for i, c := range []struct {
		picker, check bool
		want          Status
		there         bool
	}{{true, true, Missing, false}, {true, false, Done, true}, {true, false, OK, true}, {false, true, Stale, true},
		{false, false, Done, false}, {false, false, OK, false}} {
		sp := again(s)
		sp.Picker, sp.Check = c.picker, c.check
		sp.Client("192.168.1.20", set.CAModulus)
		got, ok := status(sp)["server picker"]
		if !ok && c.want == OK && !c.picker {
			got = OK // nothing to remove: no line
		}
		if got != c.want || exists(picker) != c.there {
			t.Errorf("picker step %d: %v, the archive there %v; want %v, %v\n%v", i, got, exists(picker), c.want, c.there, sp.Lines())
		}
	}
}

func TestNoArchive(t *testing.T) {
	s, _ := newSetup(t)
	s.FindZip = func() (string, error) { return "", ErrNoZip }
	s.Server(ServerOptions{Address: Local})
	expect(t, s, map[string]Status{"certificates": Done, "client copy": Missing, "host copy": Missing,
		"host DLL": Missing, "content": Missing})
	if out := s.Out.(*bytes.Buffer).String(); !strings.Contains(out, "Dawngate.zip not found") {
		t.Errorf("report:\n%s", out)
	}
}

func TestWrongArchive(t *testing.T) {
	s, _ := newSetup(t)
	files := s.Manifest.Files()
	files[0].SHA256 = strings.Repeat("0", 64)
	s.Manifest = archive.NewManifest(files)
	s.Client(Local, bytes.Repeat([]byte{0x5A}, 128))
	expect(t, s, map[string]Status{"client copy": Failed})
	if out := s.Out.(*bytes.Buffer).String(); !strings.Contains(out, "is not the Dawngate.zip of archive.org") {
		t.Errorf("report:\n%s", out)
	}
	entries, _ := os.ReadDir(s.Root)
	for _, e := range entries {
		t.Errorf("left behind: %s", e.Name())
	}
}

func TestClientOptions(t *testing.T) {
	for addr, profile := range map[string]string{"127.0.0.1": exepatch.ProfileLocal, "127.0.9.1": exepatch.ProfileLocal,
		"192.168.1.20": exepatch.ProfileLAN, "10.0.0.5": exepatch.ProfileLAN, "8.8.8.8": exepatch.ProfileLAN} {
		o, err := ClientOptions(addr)
		if err != nil || o.Profile != profile || o.ServerHost != addr {
			t.Errorf("%s: %+v %v", addr, o, err)
		}
	}
	for _, addr := range []string{"", "localhost", "0.0.0.0", "224.0.0.1", "127.000.000.001.5", "::1", "1.2.3"} {
		if _, err := ClientOptions(addr); err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}

func TestLock(t *testing.T) {
	root := t.TempDir()
	release, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(root); err != nil { // our own pid: taken over
		t.Errorf("own lock: %v", err)
	}
	release()
	// a lock of a process that ended is taken over
	if err := os.WriteFile(filepath.Join(root, LockFile), []byte("pid=999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err = Lock(root)
	if err != nil {
		t.Fatalf("a dead process's lock: %v", err)
	}
	release()
	if exists(filepath.Join(root, LockFile)) {
		t.Error("the lock stayed after its release")
	}
}
