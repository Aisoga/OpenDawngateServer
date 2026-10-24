package launcher

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/tdf"
)

// readMapList is the create dialog's map list in a player copy's zz_MatchOptions.snap, without its padding.
func readMapList(t *testing.T, copyDir string) string {
	t.Helper()
	a, err := snap.Open(moviepatch.MapListArchive(copyDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range a.Entries {
		if e.Name == moviepatch.MapListPath {
			text, err := a.Read(e)
			if err != nil {
				t.Fatal(err)
			}
			return strings.TrimRight(string(text), "\n")
		}
	}
	t.Fatal("no map list in the archive")
	return ""
}

// TestFrontMapList: a login's switch writes the CREATE GAME dialog's maps for that server into the copy, in place:
// A offers MM (--custom-maps), B nothing besides The Dawngate; a copy without MM's files never lists it.
func TestFrontMapList(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.50's, 127.0.9.51's and 127.0.9.52's ports")
	}
	portA, _ := serveTunnelLogged(t, frontServerA, "--custom-maps", "MM_Map_v3")
	portB, _ := serveTunnelLogged(t, frontServerB)
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []Bookmark{{Tag: "a", Address: frontServerA, GatewayPort: portA}, {Tag: "b", Address: frontServerB, GatewayPort: portB}} {
		if _, err := bm.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	_, tlsCfg, err := tunnelCertSet(l, frontLocal, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(frontLocal), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second, ProbeTimeout: time.Second}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// the copy as the setup leaves it with MM's files: the list names MM (a made-up movie beside it)
	copyDir := t.TempDir()
	data := filepath.Join(copyDir, "game", "Data")
	os.MkdirAll(data, 0o755)
	archive := snap.Pack([]snap.File{{Name: moviepatch.MatchOptions.MoviePath, Data: []byte("a made-up movie")},
		{Name: moviepatch.MapListPath, Data: moviepatch.MapList(moviepatch.ExtraMaps), Literals: true}})
	if err := os.WriteFile(moviepatch.MapListArchive(copyDir), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "zz_MM_Map_v3.snap"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteServerList(copyDir); err != nil {
		t.Fatal(err)
	}
	const withMM, without = "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nend", "dawngate-maps\t1\nend"
	g := fakeGame{t: t}
	confirmed := map[string]bool{}
	logIn := func(tag string) {
		t.Helper()
		steps := []uint16{FrontErrNewKey, 0} // confirm the new key once, then the login
		if confirmed[tag] {
			steps = steps[1:]
		}
		confirmed[tag] = true
		g.redirect()
		c := g.dial(DefaultFrontPorts.Blaze)
		defer c.Close()
		g.start(c)
		for i, want := range steps {
			body := tdf.NewWriter().String("MAIL", "alex#"+tag).String("PASS", "dawngate1").String("TOKN", "").Int("TYPE", 0).Bytes()
			rep := g.call(c, 1, 0x28, uint32(2+i), body)
			if (want != 0 && (rep.Type != fire.ErrorReply || rep.Error != want)) || (want == 0 && rep.Type != fire.Reply) {
				t.Fatalf("login %d to %s: %v %#x\n%s", i, tag, rep.Type, rep.Error, readLogs(t, logDir))
			}
		}
	}
	logIn("b") // B offers nothing besides The Dawngate: the list without MM, the archive's size unchanged
	if got := readMapList(t, copyDir); got != without {
		t.Errorf("the list after the login to B: %q\n%s", got, readLogs(t, logDir))
	}
	if st, _ := os.Stat(moviepatch.MapListArchive(copyDir)); st == nil || st.Size() != int64(len(archive)) {
		t.Errorf("the archive's size changed: %v", st)
	}
	waitLog(t, logDir, "the create dialog's maps for b: The Dawngate (the server offers none besides The Dawngate; the copy has mm), written into", 0)
	logIn("a") // A offers MM: listed again
	if got := readMapList(t, copyDir); got != withMM {
		t.Errorf("the list after the login to A: %q", got)
	}
	waitLog(t, logDir, "the create dialog's maps for a: The Dawngate, Anshavar Pass (the server offers mm; the copy has mm), written into", 0)
	// without MM's files the copy never lists it, also for A
	if err := os.Remove(filepath.Join(data, "zz_MM_Map_v3.snap")); err != nil {
		t.Fatal(err)
	}
	logIn("b")
	logIn("a")
	if got := readMapList(t, copyDir); got != without {
		t.Errorf("A's list for a copy without MM's files: %q", got)
	}
	waitLog(t, logDir, "the create dialog's maps for a: The Dawngate (the server offers mm; the copy has none)", 0)
}
