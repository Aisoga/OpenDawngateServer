package launcher

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
)

// the servers of TestWriteServerMapList: one offering MM_Map_v3 (--custom-maps), one nothing besides The Dawngate,
// and an older release's HTTP port (404)
const (
	listServerMM   = "127.0.9.85"
	listServerNone = "127.0.9.86"
	listServerOld  = "127.0.9.87"
)

// TestWriteServerMapList: without the tunnel, the dialog's list is the maps the copy has that the server's
// /dawngate/maps.txt names; a folder map the copy has but the server doesn't offer is left out; a server before the
// page gets the copy's maps.
func TestWriteServerMapList(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.85-87's ports")
	}
	serveTunnelLogged(t, listServerMM, "--custom-maps", "MM_Map_v3") // its plain HTTP port too (--plain on)
	serveTunnelLogged(t, listServerNone)
	ln, err := net.Listen("tcp4", listServerOld+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.NotFoundHandler())
	oldPort := ln.Addr().(*net.TCPAddr).Port

	// the copy has MM's files and a folder map's (CM_Test), and its list names both, as the setup leaves it
	folder := []moviepatch.Map{{Tag: "test", Archive: "zz_CM_Test.snap", Label: "Test Map"}}
	copyDir := t.TempDir()
	data := filepath.Join(copyDir, "game", "Data")
	os.MkdirAll(data, 0o755)
	all := append(append([]moviepatch.Map(nil), moviepatch.ExtraMaps...), folder...)
	archive := snap.Pack([]snap.File{{Name: moviepatch.MatchOptions.MoviePath, Data: []byte("a made-up movie")},
		{Name: moviepatch.MapListPath, Data: moviepatch.MapList(all), Literals: true}})
	if err := os.WriteFile(moviepatch.MapListArchive(copyDir), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"zz_MM_Map_v3.snap", "zz_CM_Test.snap"} {
		if err := os.WriteFile(filepath.Join(data, a), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const both = "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nmap\ttest\tTest Map\nend"
	if got := readMapList(t, copyDir); got != both {
		t.Fatalf("the setup's list: %q", got)
	}

	for _, c := range []struct {
		server string
		port   int
		want   string
		note   string
	}{
		{listServerMM, HTTPPort, "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nend", "the server offers mm"},
		{listServerNone, HTTPPort, "dawngate-maps\t1\nend", "the server offers none besides The Dawngate"},
		{listServerOld, oldPort, both, "the server doesn't tell its maps"},
	} {
		line, err := WriteServerMapList(copyDir, folder, c.server, c.port, 3*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", c.server, err)
		}
		if !strings.Contains(line, c.note) {
			t.Errorf("%s: the line %q lacks %q", c.server, line, c.note)
		}
		if got := readMapList(t, copyDir); got != c.want {
			t.Errorf("%s: the list %q, want %q", c.server, got, c.want)
		}
	}
	// nothing answers: an error, the list as it was
	if _, err := WriteServerMapList(copyDir, folder, "127.0.9.88", HTTPPort, time.Second); err == nil {
		t.Error("no server: no error")
	}
	if got := readMapList(t, copyDir); got != both {
		t.Errorf("no server: the list changed to %q", got)
	}
}
