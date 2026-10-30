package mapdist

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/mapc"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/testmaps"
)

// fakeVersion stands for the archive a package compiles to: the SHA-256 of its map.json (another map.json, another
// version), so the tests need no game copy.
func fakeVersion(m *mapc.Map, _ []mapc.Other) ([32]byte, error) {
	b, err := os.ReadFile(filepath.Join(m.Dir, mapc.FileName))
	return sha256.Sum256(b), err
}

func versionOf(t *testing.T, dir string) [32]byte {
	t.Helper()
	m, err := mapc.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := fakeVersion(m, nil)
	return v
}

// servedGetter answers a Delivery's requests from a Served as the HTTP port does (status, size, body); tamper may
// change a package's bytes on the way, and gets counts the package requests.
type servedGetter struct {
	s        *Served
	tamper   func([]byte) []byte
	noList   bool
	packages int
}

func (g *servedGetter) get(p string) (io.ReadCloser, int64, int, error) {
	body := func(b []byte, status int) (io.ReadCloser, int64, int, error) {
		return io.NopCloser(bytes.NewReader(b)), int64(len(b)), status, nil
	}
	if p == ManifestPath {
		if g.noList {
			return body([]byte("404"), 404)
		}
		return body(g.s.Manifest(), 200)
	}
	tag, ok := strings.CutPrefix(p, PackagePrefix)
	if !ok {
		return body(nil, 404)
	}
	g.packages++
	size, write, ok := g.s.Package(tag)
	if !ok {
		return body(nil, 404)
	}
	var b bytes.Buffer
	if err := write(&b); err != nil {
		return nil, 0, 0, err
	}
	out := b.Bytes()
	if g.tamper != nil {
		out = g.tamper(out)
	}
	rc, _, st, err := body(out, 200)
	return rc, size, st, err
}

// server is a maps folder with CM_Marsh and CM_Ring, both handed out.
func server(t *testing.T) (dir string, s *Served) {
	t.Helper()
	dir = t.TempDir()
	copyPkg(t, "CM_Marsh", dir)
	copyPkg(t, "CM_Ring", dir)
	s, notes := NewServed(dir, []Offer{{Name: "CM_Marsh", Tag: "marsh"}, {Name: "CM_Ring", Tag: "ring"},
		{Name: "MM_Map_v3", Tag: "mm"}}, DefaultLimits())
	if len(notes) != 1 || !strings.Contains(notes[0], "hands out CM_Marsh (~#marsh, ") || !strings.Contains(notes[0], "CM_Ring (~#ring, ") {
		t.Fatalf("the notes %q", notes)
	}
	return dir, s
}

func newDelivery(mapsDir string, g *servedGetter) *Delivery {
	return &Delivery{MapsDir: mapsDir, Server: "test (127.0.0.1:42128)", Limits: DefaultLimits(), Get: g.get, Verify: fakeVersion}
}

func wantsOf(t *testing.T, srv string, names ...string) []Want {
	var ws []Want
	for _, n := range names {
		tag := map[string]string{"CM_Marsh": "marsh", "CM_Ring": "ring"}[n]
		ws = append(ws, Want{Tag: tag, Version: versionOf(t, filepath.Join(srv, n))})
	}
	return ws
}

// TestDeliver: serve and fetch. Both maps arrive in the client's maps folder with delivered.txt, bundle to the
// server's bytes, and leave no .incoming; the next run finds them present and downloads nothing.
func TestDeliver(t *testing.T) {
	srv, s := server(t)
	maps := t.TempDir()
	g := &servedGetter{s: s}
	var lines []string
	d := newDelivery(maps, g)
	d.Logf = func(format string, a ...any) { lines = append(lines, format) }
	rs := d.Run(wantsOf(t, srv, "CM_Marsh", "CM_Ring"))
	if len(rs) != 2 || rs[0].Status != Installed || rs[1].Status != Installed || rs[0].Display != "Marsh" {
		t.Fatalf("results %+v", rs)
	}
	for _, n := range []string{"CM_Marsh", "CM_Ring"} {
		if !bytes.Equal(bundleOf(t, filepath.Join(maps, n)), bundleOf(t, filepath.Join(srv, n))) {
			t.Errorf("%s: not the server's package", n)
		}
		mk, err := os.ReadFile(filepath.Join(maps, n, MarkerFile))
		if err != nil || !strings.Contains(string(mk), "from the server test (127.0.0.1:42128)") {
			t.Errorf("%s: the marker %q %v", n, mk, err)
		}
	}
	if _, err := os.Stat(filepath.Join(maps, IncomingDir)); err == nil {
		t.Error(".incoming left")
	}
	if pkgs, errs := mapc.ReadFolder(maps); len(pkgs) != 2 || len(errs) != 0 {
		t.Errorf("the folder reads %d packages, %v", len(pkgs), errs)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "downloading") {
		t.Errorf("progress lines %q", lines)
	}
	g.packages = 0
	rs = newDelivery(maps, g).Run(wantsOf(t, srv, "CM_Marsh", "CM_Ring"))
	if rs[0].Status != Present || rs[1].Status != Present || g.packages != 0 {
		t.Errorf("again: %+v, %d downloads", rs, g.packages)
	}
}

// TestDeliverHashMismatch: a byte changed on the way, or a package cut short, is not installed and leaves nothing.
func TestDeliverHashMismatch(t *testing.T) {
	srv, s := server(t)
	for _, c := range []struct {
		name   string
		tamper func([]byte) []byte
		st     Status
		why    string
	}{
		{"a byte changed", func(b []byte) []byte {
			b = append([]byte(nil), b...)
			b[len(b)-1] ^= 1
			return b
		}, Refused, "isn't the package the server announced"},
		{"cut short", func(b []byte) []byte { return b[:len(b)-10] }, Failed, "ends early"},
	} {
		maps := t.TempDir()
		rs := newDelivery(maps, &servedGetter{s: s, tamper: c.tamper}).Run(wantsOf(t, srv, "CM_Marsh"))
		if rs[0].Status != c.st || !strings.Contains(rs[0].Err.Error(), c.why) {
			t.Errorf("%s: %+v", c.name, rs[0])
		}
		if ents, _ := os.ReadDir(maps); len(ents) != 0 {
			t.Errorf("%s: left %v", c.name, ents)
		}
	}
}

// TestDeliverSizeCap: a package over the client's cap isn't even asked for.
func TestDeliverSizeCap(t *testing.T) {
	srv, s := server(t)
	maps := t.TempDir()
	g := &servedGetter{s: s}
	d := newDelivery(maps, g)
	d.Limits.Total = 4096
	rs := d.Run(wantsOf(t, srv, "CM_Marsh"))
	if rs[0].Status != Refused || !strings.Contains(rs[0].Err.Error(), "more than this Start-Client takes") || g.packages != 0 {
		t.Errorf("%+v, %d downloads", rs[0], g.packages)
	}
	// the server's cap: a package over it isn't handed out, with a warning
	lim := DefaultLimits()
	lim.File = 1000
	s2, notes := NewServed(srv, []Offer{{Name: "CM_Marsh", Tag: "marsh"}}, lim)
	if len(s2.Entries()) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "WARNING: map delivery: CM_Marsh") {
		t.Errorf("the server's cap: %v %q", s2.Entries(), notes)
	}
}

// TestDeliverBrokenPackage: a package that doesn't compile, compiles to another version, holds a file its map.json
// doesn't name, isn't the map the list says or doesn't parse is refused, and nothing is installed.
func TestDeliverBrokenPackage(t *testing.T) {
	srv, s := server(t)
	maps := t.TempDir()
	d := newDelivery(maps, &servedGetter{s: s})
	d.Verify = func(*mapc.Map, []mapc.Other) ([32]byte, error) {
		return [32]byte{}, errors.New("rule lanes.width fails")
	}
	if rs := d.Run(wantsOf(t, srv, "CM_Ring")); rs[0].Status != Refused || !strings.Contains(rs[0].Err.Error(), "doesn't compile: rule lanes.width") {
		t.Errorf("not compiling: %+v", rs[0])
	}
	w := wantsOf(t, srv, "CM_Ring")
	w[0].Version[0] ^= 1
	if rs := newDelivery(maps, &servedGetter{s: s}).Run(w); rs[0].Status != Refused || !strings.Contains(rs[0].Err.Error(), "compiles to version") {
		t.Errorf("another version: %+v", rs[0])
	}

	ring, _ := os.ReadFile(filepath.Join(srv, "CM_Ring", "map.json"))
	for _, c := range []struct {
		name  string
		entry string // the list's name for ring
		b     []byte
		why   string
	}{
		{"an extra file", "CM_Ring", fakeBundle(Magic, 1, [2]string{"map.json", string(ring)}, [2]string{"ground/x.png", pngHeadOf(8, 8)}), "aren't the ones its map.json names"},
		{"another map", "CM_Other", fakeBundle(Magic, 1, [2]string{"map.json", string(ring)}), "the server's list says CM_Other"},
		{"no map", "CM_Ring", fakeBundle(Magic, 1, [2]string{"map.json", `{"format": 1, "evil": true}`}), "map.json"},
	} {
		sum := sha256.Sum256(c.b)
		e := Entry{Tag: "ring", Name: c.entry, Size: int64(len(c.b)), SHA256: sum}
		get := func(p string) (io.ReadCloser, int64, int, error) {
			if p == ManifestPath {
				m := FormatManifest([]Entry{e})
				return io.NopCloser(strings.NewReader(m)), int64(len(m)), 200, nil
			}
			return io.NopCloser(bytes.NewReader(c.b)), int64(len(c.b)), 200, nil
		}
		d := &Delivery{MapsDir: maps, Limits: DefaultLimits(), Get: get, Verify: fakeVersion}
		rs := d.Run([]Want{{Tag: "ring", Version: versionOf(t, filepath.Join(srv, "CM_Ring"))}})
		if rs[0].Status != Refused || !errors.Is(rs[0].Err, ErrBundle) || !strings.Contains(rs[0].Err.Error(), c.why) {
			t.Errorf("%s: %+v", c.name, rs[0])
		}
	}
	if ents, _ := os.ReadDir(maps); len(ents) != 0 {
		t.Errorf("left %v", ents)
	}
}

// TestDeliverReplace: the player's own package of another version is never replaced; a delivered one is (the old
// folder gone, the marker new).
func TestDeliverReplace(t *testing.T) {
	srv, s := server(t)
	maps := t.TempDir()
	own := copyPkg(t, "CM_Ring", maps)
	js := filepath.Join(own, "map.json")
	b, _ := os.ReadFile(js)
	mine := strings.Replace(string(b), `"width": 24`, `"width": 22`, -1)
	os.WriteFile(js, []byte(mine), 0o644)
	rs := newDelivery(maps, &servedGetter{s: s}).Run(wantsOf(t, srv, "CM_Ring"))
	if rs[0].Status != Refused || !strings.Contains(rs[0].Err.Error(), "the maps folder has your own Test Ring") {
		t.Errorf("own: %+v", rs[0])
	}
	if got, _ := os.ReadFile(js); string(got) != mine {
		t.Error("the player's own package was changed")
	}
	os.WriteFile(filepath.Join(own, "layout.png"), []byte("the player's sketch"), 0o644)
	os.WriteFile(filepath.Join(own, MarkerFile), []byte("downloaded before"), 0o644)
	rs = newDelivery(maps, &servedGetter{s: s}).Run(wantsOf(t, srv, "CM_Ring"))
	if rs[0].Status != Installed {
		t.Fatalf("delivered: %+v", rs[0])
	}
	if !bytes.Equal(bundleOf(t, own), bundleOf(t, filepath.Join(srv, "CM_Ring"))) {
		t.Error("not the server's version")
	}
	if _, err := os.Stat(filepath.Join(own, "layout.png")); err == nil {
		t.Error("the old version's files are left")
	}
	if mk, _ := os.ReadFile(filepath.Join(own, MarkerFile)); !strings.Contains(string(mk), "Start-Client") {
		t.Errorf("the marker %q", mk)
	}
	// a tag another package of the player's has
	maps2 := t.TempDir()
	other := copyPkg(t, "CM_Grove", maps2)
	g, _ := os.ReadFile(filepath.Join(other, "map.json"))
	os.WriteFile(filepath.Join(other, "map.json"), []byte(strings.Replace(string(g), `"tag": "grove"`, `"tag": "ring"`, 1)), 0o644)
	rs = newDelivery(maps2, &servedGetter{s: s}).Run(wantsOf(t, srv, "CM_Ring"))
	if rs[0].Status != Refused || !strings.Contains(rs[0].Err.Error(), "your own map CM_Grove with the same tag ~#ring") {
		t.Errorf("tag clash: %+v", rs[0])
	}
}

// TestDeliverNotServed: a server without the list (off, or a release before it) and a tag it doesn't hand out; a
// package refused before in the run isn't asked for again.
func TestDeliverNotServed(t *testing.T) {
	srv, s := server(t)
	maps := t.TempDir()
	rs := newDelivery(maps, &servedGetter{s: s, noList: true}).Run(wantsOf(t, srv, "CM_Ring"))
	if rs[0].Status != NotServed || !strings.Contains(rs[0].Err.Error(), "doesn't hand out its maps") {
		t.Errorf("no list: %+v", rs[0])
	}
	rs = newDelivery(maps, &servedGetter{s: s}).Run([]Want{{Tag: "grove"}})
	if rs[0].Status != NotServed || !strings.Contains(rs[0].Err.Error(), "doesn't hand out ~#grove") {
		t.Errorf("not served: %+v", rs[0])
	}
	g := &servedGetter{s: s}
	d := newDelivery(maps, g)
	d.Skip = func([32]byte) bool { return true }
	if rs = d.Run(wantsOf(t, srv, "CM_Ring")); rs[0].Status != Refused || g.packages != 0 {
		t.Errorf("skipped: %+v, %d downloads", rs[0], g.packages)
	}
}

// TestBuildVerifierGame (needs the player's copy): a fixture package checks and builds against the copy to the
// archive mapc.Build makes from the same files; a package naming a noun the copy lacks is refused.
func TestBuildVerifierGame(t *testing.T) {
	dir := testgame.Dir(t)
	m, err := mapc.Load(testmaps.Pkg("CM_Ring"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := BuildVerifier(dir)(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	in, err := mapc.ReadInputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := mapc.Load(testmaps.Pkg("CM_Ring"))
	b, _, err := mapc.Build(m2, in, mapc.SharedMesh{}, nil)
	if err != nil || sha256.Sum256(b) != sum {
		t.Errorf("the verifier's %x, the build's %x (%v)", sum[:4], sha256.Sum256(b), err)
	}
	m.Camps = append(m.Camps, mapc.Camp{Kind: "camp", Noun: "No_Such_Spawner.Noun", Pos: mapc.Point{-60, -100}})
	if _, err := BuildVerifier(dir)(m, nil); err == nil || !strings.Contains(err.Error(), "No_Such_Spawner") {
		t.Errorf("an unknown noun: %v", err)
	}
}
