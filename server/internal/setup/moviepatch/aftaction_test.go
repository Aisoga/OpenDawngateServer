package moviepatch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func TestAfterActionTags(t *testing.T) {
	// DefineSprite 2000, DoABC "afteractiontabs", SymbolClass 2000 -> AfterActionTabs, PlaceObject3 at depth 2000
	sp := AfterAction
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("aftaction_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if d := sp.Tags[tags[1].start:tags[1].end]; !bytes.HasPrefix(d, []byte("\x01\x00\x00\x00afteractiontabs\x00")) {
		t.Errorf("DoABC %x…: not lazy and named afteractiontabs", d[:16])
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	if err != nil || len(ps) != 1 || ps[0] != (pair{sp.CharID, sp.Class}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s", ps, err, sp.CharID, sp.Class)
	}
	d := sp.Tags[tags[3].start:tags[3].end]
	if !bytes.Equal(d, cat([]byte{0x06, 0}, u16s(sp.Depth, sp.CharID), []byte{0})) {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, sp.CharID, sp.Depth)
	}
	// the class reads the file this package writes, by the path the game's opener gives it
	if !bytes.Contains(sp.Tags, []byte("Battlegrounds/dgweb.json")) || !bytes.Contains(sp.Tags, []byte("dawngate-web\t1")) {
		t.Error("the class doesn't read Battlegrounds/dgweb.json with its first line")
	}
	once, err := sp.PatchMovie(synth())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.PatchMovie(once); err == nil || !strings.Contains(err.Error(), "the After Action Review's tabs already") {
		t.Errorf("twice: %v", err)
	}
}

func TestWebRootFile(t *testing.T) {
	got, err := WebRootFile("127.0.0.2", WebPort)
	want := "dawngate-web\t1\ncommon\thttp://127.0.0.2:18080/battlegrounds_test/live/www/client/1/common\nend\n"
	if err != nil || string(got) != want {
		t.Errorf("WebRootFile = %q, %v", got, err)
	}
	if got, err := WebRootFile("dawngate.example.org", 8080); err != nil || !bytes.Contains(got, []byte("\thttp://dawngate.example.org:8080/")) {
		t.Errorf("a host name: %q, %v", got, err)
	}
	for _, bad := range []struct {
		host string
		port int
	}{{"", 18080}, {"a b", 18080}, {"a\"b", 18080}, {"a<b", 18080}, {"hé", 18080}, {"a\tb", 18080}, {"x", 0}, {"x", 65536}} {
		if _, err := WebRootFile(bad.host, bad.port); err == nil {
			t.Errorf("WebRootFile(%q, %d) passed", bad.host, bad.port)
		}
	}
	// a spec's archive: the movie, then the root of WebHost (DefaultWebHost when empty); the bytes test_picker.py pins
	src := snap.Pack([]snap.File{{Name: AfterAction.MoviePath, Data: synth()}})
	sp := AfterAction
	sp.Tags = []byte("OURTAGS")
	for _, c := range []struct{ host, want string }{
		{"", "b397d4db173ebbcc86393a26fd73134d90f02d6bee39c08b4a028f6bda27d45c"},
		{"192.168.1.20", "8a94e55eb9d5f6950880e9d2d06e3eb9c388ffb2994496fb38a40b1e1e6ffd7b"},
	} {
		sp.WebHost = c.host
		out, err := sp.Build(src)
		if err != nil {
			t.Fatal(err)
		}
		a, err := snap.Parse(out)
		if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != AfterAction.MoviePath || a.Entries[1].Name != WebRootPath {
			t.Fatalf("the archive: %v, %+v", err, a)
		}
		host := c.host
		if host == "" {
			host = DefaultWebHost
		}
		wantRoot, _ := WebRootFile(host, WebPort)
		if root, err := a.Read(a.Entries[1]); err != nil || !bytes.Equal(root, wantRoot) {
			t.Errorf("the archive's web root %q, %v", root, err)
		}
		if got := sum(out); got != c.want {
			t.Errorf("Build(host %q): %d bytes, %s; patch_login_movie.py's: %s", c.host, len(out), got, c.want)
		}
	}
	sp.WebHost = "a b"
	if _, err := sp.Build(src); err == nil {
		t.Error("a bad host passed Build")
	}
	// the other specs have no web root
	cr := MatchOptions
	cr.WebHost = "192.168.1.20"
	if out, err := cr.Build(snap.Pack([]snap.File{{Name: MatchOptions.MoviePath, Data: synth()}})); err != nil {
		t.Fatal(err)
	} else if a, err := snap.Parse(out); err != nil || len(a.Entries) != 2 || a.Entries[1].Name != MapListPath {
		t.Errorf("the bot check box's archive: %v, %+v", err, a)
	}
}

// The player's own Battlegrounds.snap of build 854 (skipped without the game files).
func TestBuildAfterAction854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "Battlegrounds.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := AfterAction.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != AfterAction.MoviePath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["AfterActionTabs"] || !s.names["maxis.AfterActionReview"] || !s.names["AARTabBtn"] ||
		!s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v; %v", err, s.names)
	}
	again, _ := AfterAction.Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_AFTERACTION_OUT"); p != "" { // for a byte-for-byte comparison with the Python twin
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
