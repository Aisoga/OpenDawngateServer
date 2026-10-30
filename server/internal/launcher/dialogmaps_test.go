package launcher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup/moviepatch"
	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/testmaps"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

// versionCopy is a player copy as the setup leaves it with MM's and two folder maps' archives (ring and grove), the
// dialog's list naming all three.
func versionCopy(t *testing.T) (copyDir string, folder []moviepatch.Map) {
	t.Helper()
	folder = []moviepatch.Map{{Tag: "ring", Archive: "zz_CM_Ring.snap", Label: "Test Ring"},
		{Tag: "grove", Archive: "zz_CM_Grove.snap", Label: "Grove"}}
	copyDir = t.TempDir()
	data := filepath.Join(copyDir, "game", "Data")
	os.MkdirAll(data, 0o755)
	all := append(append([]moviepatch.Map(nil), moviepatch.ExtraMaps...), folder...)
	archive := snap.Pack([]snap.File{{Name: moviepatch.MatchOptions.MoviePath, Data: []byte("a made-up movie")},
		{Name: moviepatch.MapListPath, Data: moviepatch.MapList(all), Literals: true}})
	if err := os.WriteFile(moviepatch.MapListArchive(copyDir), archive, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"zz_MM_Map_v3.snap": "mm", "zz_CM_Ring.snap": "ring", "zz_CM_Grove.snap": "grove"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return copyDir, folder
}

const (
	listAll    = "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nmap\tring\tTest Ring\nmap\tgrove\tGrove\nend"
	listNoRing = "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nmap\tgrove\tGrove\nend"
)

// TestDialogMaps: of the maps the server offers, one it names a version of stays only in that version; a map without
// a version, and every map of a server that names none, stays as before.
func TestDialogMaps(t *testing.T) {
	copyDir, folder := versionCopy(t)
	ring, grove := sha256.Sum256([]byte("ring")), sha256.Sum256([]byte("grove"))
	other := sha256.Sum256([]byte("another ring"))
	offered := []string{"mm", "ring", "grove"}
	for _, c := range []struct {
		name     string
		si       serverInfo
		want     []string
		whyParts []string
	}{
		{"the same versions", serverInfo{tags: offered, told: true, versions: map[string][32]byte{"ring": ring, "grove": grove}},
			[]string{"mm", "ring", "grove"}, []string{"the server offers mm, ring, grove"}},
		{"another ring", serverInfo{tags: offered, told: true, versions: map[string][32]byte{"ring": other, "grove": grove}},
			[]string{"mm", "grove"}, []string{"left out, another version than the server's: ring (the copy's " +
				hex.EncodeToString(ring[:4]) + ", the server's " + hex.EncodeToString(other[:4]) + ")"}},
		{"no versions (a server before them)", serverInfo{tags: offered, told: true}, []string{"mm", "ring", "grove"}, nil},
		{"a version of a map not offered", serverInfo{tags: []string{"mm"}, told: true, versions: map[string][32]byte{"ring": other}},
			[]string{"mm"}, nil},
		{"no maps fact, versions anyway", serverInfo{versions: map[string][32]byte{"grove": other}}, []string{"mm", "ring"},
			[]string{"the copy's", "grove (the copy's"}},
	} {
		maps, why := dialogMaps(copyDir, folder, c.si)
		if got := mapTags(maps); got != strings.Join(c.want, ", ") {
			t.Errorf("%s: %s, want %v (%s)", c.name, got, c.want, why)
		}
		for _, p := range c.whyParts {
			if !strings.Contains(why, p) {
				t.Errorf("%s: why %q lacks %q", c.name, why, p)
			}
		}
		if len(c.whyParts) == 0 && strings.Contains(why, "left out") {
			t.Errorf("%s: why %q leaves out a map", c.name, why)
		}
	}
	// the copy without ring's archive: not listed, whatever the version (CopyMaps' files check)
	os.Remove(filepath.Join(copyDir, "game", "Data", "zz_CM_Ring.snap"))
	if maps, _ := dialogMaps(copyDir, folder, serverInfo{tags: offered, told: true, versions: map[string][32]byte{"ring": ring}}); mapTags(maps) != "mm, grove" {
		t.Errorf("no ring archive: %s", mapTags(maps))
	}
}

// TestFrontMapListVersion: the front's write at a login takes the versions from the gateway's facts.
func TestFrontMapListVersion(t *testing.T) {
	copyDir, folder := versionCopy(t)
	var log bytes.Buffer
	f := &Front{Log: logging.New(&log), Maps: folder, listCopy: copyDir}
	other := sha256.Sum256([]byte("another ring"))
	facts := tunnel.MapVersionFacts(map[string][32]byte{"ring": other, "grove": sha256.Sum256([]byte("grove"))})
	facts[tunnel.FactMaps] = "mm+ring+grove"
	f.writeMapList(&frontSession{b: Bookmark{Tag: "x"}, facts: facts})
	if got := readMapList(t, copyDir); got != listNoRing {
		t.Errorf("the list with another ring: %q\n%s", got, log.String())
	}
	if !strings.Contains(log.String(), "the create dialog's maps for x: The Dawngate, Anshavar Pass, Grove (the server offers mm, ring, grove; "+
		"the copy has mm, ring, grove; left out, another version than the server's: ring (the copy's ") {
		t.Errorf("the log: %s", log.String())
	}
	delete(facts, tunnel.FactMapVersion+"ring") // the server names no version of ring: listed
	f.writeMapList(&frontSession{b: Bookmark{Tag: "x"}, facts: facts})
	if got := readMapList(t, copyDir); got != listAll {
		t.Errorf("the list without ring's version: %q", got)
	}
}

// TestWriteServerMapListVersion: without the tunnel the versions come from /dawngate/mapversions.txt; a server of
// the release before it (maps.txt only, 404 for the versions) gets the list as before.
func TestWriteServerMapListVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("binds a port on 127.0.0.1")
	}
	copyDir, folder := versionCopy(t)
	other := sha256.Sum256([]byte("another ring"))
	versionsPage := true
	mux := http.NewServeMux()
	mux.HandleFunc("/"+web.MapsPath, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("mm+ring+grove\n")) })
	mux.HandleFunc("/"+web.MapVersionsPath, func(w http.ResponseWriter, r *http.Request) {
		if !versionsPage {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(tunnel.MapVersionsText(map[string][32]byte{"ring": other})))
	})
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, mux)
	port := ln.Addr().(*net.TCPAddr).Port

	line, err := WriteServerMapList(copyDir, folder, "127.0.0.1", port, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMapList(t, copyDir); got != listNoRing {
		t.Errorf("another ring: the list %q (%s)", got, line)
	}
	if !strings.Contains(line, "left out, another version than the server's: ring (the copy's "+hex.EncodeToString(func() []byte {
		s := sha256.Sum256([]byte("ring"))
		return s[:4]
	}())+", the server's "+hex.EncodeToString(other[:4])+")") {
		t.Errorf("the line %q", line)
	}
	versionsPage = false // a server of the release before the page
	line, err = WriteServerMapList(copyDir, folder, "127.0.0.1", port, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMapList(t, copyDir); got != listAll || strings.Contains(line, "left out") {
		t.Errorf("no versions page: the list %q (%s)", got, line)
	}
}

// TestServerMapVersions: a real dgserver with a maps folder and a host copy names its version of CM_Ring in its
// gateway's facts and on its HTTP port; a checkout's list (WriteServerMapList) leaves the copy's other version of the
// ring out and lists the server's version.
func TestServerMapVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.89's ports")
	}
	orig := blaze.PlayableLevels
	t.Cleanup(func() { blaze.PlayableLevels = orig })
	host := t.TempDir()
	hostData := filepath.Join(host, "Dawngate", "game", "Data")
	if err := os.MkdirAll(hostData, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(host, "Dawngate", "game", "Dawngate.exe"), nil, 0o644)
	os.WriteFile(filepath.Join(hostData, "zz_CM_Ring.snap"), []byte("the server's ring"), 0o644)
	gw, _ := serveTunnelLogged(t, "127.0.9.89", "--maps", testmaps.Dir(), "--gms-dir", host,
		"--custom-maps", "CM_Ring,MM_Map_v3")
	served := sha256.Sum256([]byte("the server's ring"))
	info, err := tunnel.ProbeInfo(net.JoinHostPort("127.0.9.89", strconv.Itoa(gw)), 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := tunnel.FactMapVersions(info.Facts); len(v) != 1 || v["ring"] != served {
		t.Errorf("the gateway's versions %v (facts %v)", v, info.Facts)
	}

	copyDir, folder := versionCopy(t) // its ring is another version
	line, err := WriteServerMapList(copyDir, folder, "127.0.9.89", HTTPPort, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMapList(t, copyDir); got != "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nend" ||
		!strings.Contains(line, "left out, another version than the server's: ring") {
		t.Errorf("another ring: the list %q (%s)", got, line)
	}
	os.WriteFile(filepath.Join(copyDir, "game", "Data", "zz_CM_Ring.snap"), []byte("the server's ring"), 0o644)
	line, err = WriteServerMapList(copyDir, folder, "127.0.9.89", HTTPPort, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMapList(t, copyDir); got != "dawngate-maps\t1\nmap\tmm\tAnshavar Pass\nmap\tring\tTest Ring\nend" {
		t.Errorf("the server's ring: the list %q (%s)", got, line)
	}
}
