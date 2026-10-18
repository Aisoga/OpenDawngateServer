package update_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/update"
	"dawngate/server/internal/update/updatetest"
)

func TestVersionOrder(t *testing.T) {
	for _, c := range []struct {
		own, latest string
		newer       bool
	}{
		{"v0.5", "v0.6", true},
		{"v0.6", "v0.5", false},
		{"v0.6", "v0.6", false},
		{"v0.9", "v0.10", true},
		{"v0.5", "v0.5.1", true},
		{"v0.5.0", "v0.5", false},
		{"v0.5.1", "v0.6", true},
		{"v1.0", "v0.99", false},
		{"dev-0123456789", "v0.6", false},
		{"dev-0123456789+dirty", "v9.9", false},
		{"dev", "v0.6", false},
		{"nightly", "v0.6", false},
		{"Dawngate-Restoration-20261018-0123456789", "v0.6", false},
		{"v0.5", "nightly", false},
		{"v0.5", "v0.6-rc1", false},
		{"V0.5", "v0.6", false},
		{"v0.5", "v0.6.1.2", false},
	} {
		if got := update.Newer(c.own, c.latest); got != c.newer {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.own, c.latest, got, c.newer)
		}
	}
	if v, ok := update.Parse("v1.20.3"); !ok || v.String() != "v1.20.3" {
		t.Errorf("Parse(v1.20.3) = %v %v", v, ok)
	}
	if v, ok := update.Parse("v0.6"); !ok || v.String() != "v0.6" {
		t.Errorf("Parse(v0.6) = %v %v", v, ok)
	}
}

func release(tag string) updatetest.FakeRelease {
	return updatetest.FakeRelease{Tag: tag, Files: map[string]string{
		"Start-Client.exe":   "client " + tag,
		"Start-Server.exe":   "server " + tag,
		"README.txt":         "readme " + tag,
		"LICENSE.txt":        "agpl",
		"host/dgsim.dll":     "dll " + tag,
		"host/lua/probe.lua": "-- probe " + tag,
	}}
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestLoopbackOnly(t *testing.T) {
	for _, bad := range []string{"https://api.github.com", "http://192.168.1.20:80", "http://example.org", "ftp://127.0.0.1:1",
		"http://127.0.0.1:1/path", "127.0.0.1:80"} {
		if _, err := update.Loopback(bad, "t"); err == nil {
			t.Errorf("Loopback(%q) accepted", bad)
		}
	}
	s, err := update.Loopback("http://127.0.3.20:18099/", "t")
	if err != nil || s.LatestURL() != "http://127.0.3.20:18099/repos/"+update.Repo+"/releases/latest" {
		t.Fatalf("Loopback: %v %q", err, s.LatestURL())
	}
	if g := update.GitHub("t"); g.LatestURL() != "https://api.github.com/repos/Aisoga/OpenDawngateServer/releases/latest" ||
		g.Downloads != "https://github.com/Aisoga/OpenDawngateServer/releases/download/" {
		t.Fatalf("GitHub: %q %q", g.LatestURL(), g.Downloads)
	}
}

func TestLatestAndDownload(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	rel := release("v0.6")
	f.Tag, f.ZipBytes = "v0.6", rel.Zip(nil)
	f.Digest = "sha256:" + sum(string(f.ZipBytes))
	src, err := update.Loopback(f.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	r, err := src.Latest(context.Background())
	if err != nil || r.Tag != "v0.6" || r.Zip.Name != "v0.6.zip" || r.Zip.Size != int64(len(f.ZipBytes)) || r.Zip.SHA256 != sum(string(f.ZipBytes)) {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
	dest := filepath.Join(t.TempDir(), "v0.6.zip")
	var last int64
	if err := src.Download(context.Background(), r.Zip, dest, func(n int64) { last = n }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != string(f.ZipBytes) || last != int64(len(b)) {
		t.Fatalf("downloaded %d bytes, progress %d", len(b), last)
	}

	// a wrong digest, a wrong size, an address that isn't the source's: refused, nothing left
	bad := r.Zip
	bad.SHA256 = sum("other")
	if err := src.Download(context.Background(), bad, dest+"2", nil); err == nil || exists(dest+"2") || exists(dest+"2.part") {
		t.Errorf("a wrong digest: %v", err)
	}
	bad = r.Zip
	bad.Size++
	if err := src.Download(context.Background(), bad, dest+"3", nil); err == nil || exists(dest+"3") {
		t.Errorf("a wrong size: %v", err)
	}
	bad = r.Zip
	bad.URL = "http://127.0.0.2:1/" + update.Repo + "/x.zip"
	if err := src.Download(context.Background(), bad, dest+"4", nil); err == nil || !strings.Contains(err.Error(), "not the project's") {
		t.Errorf("another address: %v", err)
	}

	// no <tag>.zip (the hand-made releases), a draft, a tag that isn't a numbered release
	f.ZipName = "Dawngate-Restoration-v0.5.zip"
	if _, err := src.Latest(context.Background()); !errors.Is(err, update.ErrNoZip) {
		t.Errorf("no <tag>.zip: %v", err)
	}
	f.ZipName = ""
	f.Draft = true
	if _, err := src.Latest(context.Background()); err == nil {
		t.Error("a draft accepted")
	}
	f.Draft, f.Tag = false, "nightly"
	if _, err := src.Latest(context.Background()); err == nil {
		t.Error("a tag nightly accepted")
	}
	f.Close()
	if _, err := src.Latest(context.Background()); err == nil {
		t.Error("no answer: no error")
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func writeZip(t *testing.T, b []byte) string {
	p := filepath.Join(t.TempDir(), "r.zip")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnpackAndCheck(t *testing.T) {
	rel := release("v0.6")
	root, err := update.Unpack(writeZip(t, rel.Zip(nil)), filepath.Join(t.TempDir(), "stage"))
	if err != nil || filepath.Base(root) != "v0.6" {
		t.Fatalf("Unpack: %q %v", root, err)
	}
	b, err := update.Check(root, "v0.6")
	if err != nil || len(b.Files) != len(rel.Files) || b.Files["host/dgsim.dll"] != sum("dll v0.6") {
		t.Fatalf("Check: %+v %v", b, err)
	}
	if _, err := update.Check(root, "v0.7"); err == nil {
		t.Error("another version accepted")
	}
	os.WriteFile(filepath.Join(root, "host", "dgsim.dll"), []byte("tampered"), 0o644)
	if _, err := update.Check(root, "v0.6"); err == nil || !strings.Contains(err.Error(), "host/dgsim.dll") {
		t.Errorf("a changed file: %v", err)
	}
	os.Remove(filepath.Join(root, "host", "dgsim.dll"))
	if _, err := update.Check(root, "v0.6"); err == nil || !strings.Contains(err.Error(), "isn't in the zip") {
		t.Errorf("a missing file: %v", err)
	}

	// an unlisted file, a path out of the folder, a protected name in BUILD.txt
	root, _ = update.Unpack(writeZip(t, rel.Zip(map[string]string{"v0.6/extra.exe": "x"})), filepath.Join(t.TempDir(), "s2"))
	if _, err := update.Check(root, "v0.6"); err == nil || !strings.Contains(err.Error(), "extra.exe") {
		t.Errorf("an unlisted file: %v", err)
	}
	for _, evil := range []string{"../evil.txt", "v0.6/../../evil.txt", "C:/evil.txt", `v0.6\..\..\evil.txt`, "v0.6/a.txt:stream"} {
		dir := filepath.Join(t.TempDir(), "s3")
		if _, err := update.Unpack(writeZip(t, rel.Zip(map[string]string{evil: "x"})), dir); err == nil {
			t.Errorf("Unpack took %q", evil)
		}
		if exists(filepath.Join(filepath.Dir(dir), "evil.txt")) {
			t.Errorf("%q written outside", evil)
		}
	}
	bad := release("v0.6")
	bad.Files["Start-Client.ini"] = "server = 1.2.3.4"
	root, _ = update.Unpack(writeZip(t, bad.Zip(nil)), filepath.Join(t.TempDir(), "s4"))
	if _, err := update.Check(root, "v0.6"); err == nil || !strings.Contains(err.Error(), "never holds") {
		t.Errorf("an ini in the release: %v", err)
	}
	if _, err := update.Unpack(writeZip(t, []byte("not a zip")), filepath.Join(t.TempDir(), "s5")); err == nil {
		t.Error("not a zip accepted")
	}
}

func TestProtected(t *testing.T) {
	for p, want := range map[string]bool{"Start-Client.ini": true, "start-server.ini": true, "Dawngate.zip": true,
		"Dawngate-Data": true, `Dawngate-Data\client\x`: true, "Start-Client.old.exe": true, "Start-Client.exe": false,
		"host/dgsim.dll": false, "host/Start-x.ini": false, "README.txt": false} {
		if update.Protected(p) != want {
			t.Errorf("Protected(%q) = %v", p, !want)
		}
	}
}

// player makes a player's folder with release r's files, its BUILD.txt and the player's own files.
func player(t *testing.T, r updatetest.FakeRelease) string {
	dir := t.TempDir()
	put := func(rel, c string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range r.Files {
		put(p, c)
	}
	put(update.BuildFile, r.BuildText())
	put("Start-Client.ini", "server = 192.168.1.20\r\n")
	put("Start-Server.ini", "address = 127.0.0.1\r\n")
	put("Dawngate.zip", "the game")
	put("Dawngate-Data/data/accounts.db", "accounts")
	put("Dawngate-Data/client/Dawngate/game/Dawngate.exe", "patched")
	return dir
}

func read(t *testing.T, dir, rel string) string {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func stage(t *testing.T, r updatetest.FakeRelease) (string, update.Build) {
	root, err := update.Unpack(writeZip(t, r.Zip(nil)), filepath.Join(t.TempDir(), "stage"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := update.Check(root, r.Tag)
	if err != nil {
		t.Fatal(err)
	}
	return root, b
}

func TestReplace(t *testing.T) {
	old := release("v0.5")
	old.Files["host/lua/gone.lua"] = "-- only in v0.5"
	dir := player(t, old)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("mine"), 0o644)
	nw := release("v0.6")
	nw.Files["host/lua/new.lua"] = "-- new in v0.6"
	root, b := stage(t, nw)

	res, err := update.Replace(dir, root, b, filepath.Join(dir, "Start-Client.exe"))
	if err != nil {
		t.Fatal(err)
	}
	for p, c := range nw.Files {
		if got := read(t, dir, p); got != c {
			t.Errorf("%s = %q, want %q", p, got, c)
		}
	}
	if read(t, dir, update.BuildFile) != nw.BuildText() || res.Installed[len(res.Installed)-1] != update.BuildFile {
		t.Error("BUILD.txt not the new one, or not last")
	}
	if exists(filepath.Join(dir, "host", "lua", "gone.lua")) || len(res.Removed) != 1 || res.Removed[0] != "host/lua/gone.lua" {
		t.Errorf("the old release's own file: removed %v", res.Removed)
	}
	if res.OldExe != "Start-Client.old.exe" || read(t, dir, "Start-Client.old.exe") != "client v0.5" {
		t.Errorf("the running program: %q", res.OldExe)
	}
	for rel, c := range map[string]string{"Start-Client.ini": "server = 192.168.1.20\r\n", "Start-Server.ini": "address = 127.0.0.1\r\n",
		"Dawngate.zip": "the game", "Dawngate-Data/data/accounts.db": "accounts", "notes.txt": "mine",
		"Dawngate-Data/client/Dawngate/game/Dawngate.exe": "patched"} {
		if read(t, dir, rel) != c {
			t.Errorf("the player's %s changed", rel)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.upd-*"))
	lua, _ := filepath.Glob(filepath.Join(dir, "host", "*", "*.upd-*"))
	if len(matches)+len(lua) != 0 {
		t.Errorf("left: %v %v", matches, lua)
	}

	// the next start: the old program goes
	if gone := update.CleanOld(dir); len(gone) != 1 || gone[0] != "Start-Client.old.exe" || exists(filepath.Join(dir, "Start-Client.old.exe")) {
		t.Errorf("CleanOld: %v", gone)
	}
	os.WriteFile(filepath.Join(dir, "host", "dgsim.dll.upd-new"), nil, 0o644)
	if gone := update.CleanOld(dir); len(gone) != 1 || gone[0] != "host/dgsim.dll.upd-new" {
		t.Errorf("CleanOld of a cut-short update: %v", gone)
	}
	if !exists(filepath.Join(dir, "Start-Client.ini")) || !exists(filepath.Join(dir, "Dawngate.zip")) {
		t.Error("CleanOld took the player's files")
	}
}

func TestReplaceUndo(t *testing.T) {
	old := release("v0.5")
	dir := player(t, old)
	root, b := stage(t, release("v0.6"))
	// a file held open without delete sharing can't be renamed on Windows: the replace must undo what it did
	f, err := os.Open(filepath.Join(dir, "host", "lua", "probe.lua"))
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := update.Replace(dir, root, b, filepath.Join(dir, "Start-Client.exe"))
	f.Close()
	if rerr == nil {
		t.Skip("this system renames a file that is open (not Windows)")
	}
	for p, c := range old.Files {
		if got := read(t, dir, p); got != c {
			t.Errorf("after the undo %s = %q, want the old %q", p, got, c)
		}
	}
	if read(t, dir, update.BuildFile) != old.BuildText() || exists(filepath.Join(dir, "Start-Client.old.exe")) {
		t.Error("the undo left the new BUILD.txt or the renamed program")
	}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if strings.Contains(p, ".upd-") {
			t.Errorf("left: %s", p)
		}
		return nil
	})
}

func TestInstall(t *testing.T) {
	f := updatetest.NewFakeGitHub()
	defer f.Close()
	nw := release("v0.6")
	f.Tag, f.ZipBytes = "v0.6", nw.Zip(nil)
	src, _ := update.Loopback(f.URL, "test")
	dir := player(t, release("v0.5"))
	work := filepath.Join(dir, "Dawngate-Data", "update")
	var asked []string
	var verified bool
	u := update.Updater{Source: src, Dir: dir, Work: work, Running: filepath.Join(dir, "Start-Client.exe"), Out: io.Discard,
		Verify: func(zip, root string, b update.Build) error {
			verified = b.Version == "v0.6" && exists(zip)
			return nil
		},
		Version: func(exe string) (string, error) {
			asked = append(asked, read(t, filepath.Dir(exe), "Start-Client.exe"))
			return "v0.6", nil
		}}
	r, err := src.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res, err := u.Install(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !verified || len(asked) != 1 || asked[0] != "client v0.6" || read(t, dir, "Start-Client.exe") != "client v0.6" || res.OldExe == "" {
		t.Errorf("verified %v, asked %v, result %+v", verified, asked, res)
	}
	if ents, _ := os.ReadDir(work); len(ents) != 0 {
		t.Errorf("the update folder keeps %d entries", len(ents))
	}

	// a new Start-Client that names another version: refused, the folder unchanged, the zip removed
	dir = player(t, release("v0.5"))
	u.Dir, u.Work, u.Running = dir, filepath.Join(dir, "Dawngate-Data", "update"), filepath.Join(dir, "Start-Client.exe")
	u.Version = func(string) (string, error) { return "v0.5", nil }
	if _, err := u.Install(context.Background(), r); err == nil || read(t, dir, "Start-Client.exe") != "client v0.5" {
		t.Errorf("a wrong --version: %v", err)
	}
	// a signature check (later) that refuses
	u.Version = nil
	u.Verify = func(string, string, update.Build) error { return errors.New("bad signature") }
	if _, err := u.Install(context.Background(), r); err == nil || !strings.Contains(err.Error(), "bad signature") ||
		read(t, dir, "Start-Client.exe") != "client v0.5" {
		t.Errorf("a refused signature: %v", err)
	}
	// a damaged zip (its BUILD.txt doesn't match)
	u.Verify = nil
	f.ZipBytes = nw.Zip(map[string]string{"v0.6/host/lua/extra.lua": "x"})
	r.Zip.Size = int64(len(f.ZipBytes))
	if _, err := u.Install(context.Background(), r); err == nil || read(t, dir, "Start-Client.exe") != "client v0.5" {
		t.Errorf("an unlisted file: %v", err)
	}
	// the download fails: the folder unchanged
	f.Close()
	if _, err := u.Install(context.Background(), r); err == nil || !strings.Contains(err.Error(), "download") {
		t.Errorf("no server: %v", err)
	}
}
