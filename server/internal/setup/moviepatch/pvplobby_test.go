package moviepatch

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/setup/testgame"
)

func TestPVPLobbyTags(t *testing.T) {
	// DefineSprite 2000, DoABC "partyregion", SymbolClass 2000 -> PartyRegion, PlaceObject3 at depth 2000
	sp := PVPLobby
	tags, err := tagsOf(sp.Tags, 0, len(sp.Tags))
	if err != nil || len(tags) != 4 || tags[3].end != len(sp.Tags) {
		t.Fatalf("pvplobby_tags.hex: %d tags, %v", len(tags), err)
	}
	for i, code := range []int{39, 82, 76, 70} {
		if tags[i].code != code {
			t.Errorf("tag %d: code %d, want %d", i, tags[i].code, code)
		}
	}
	if d := sp.Tags[tags[1].start:tags[1].end]; !bytes.HasPrefix(d, []byte("\x01\x00\x00\x00partyregion\x00")) {
		t.Errorf("DoABC %x…: not lazy and named partyregion", d[:16])
	}
	ps, err := pairs(sp.Tags, tags[2].start, tags[2].end)
	if err != nil || len(ps) != 1 || ps[0] != (pair{sp.CharID, sp.Class}) {
		t.Errorf("SymbolClass %+v, %v: want %d -> %s", ps, err, sp.CharID, sp.Class)
	}
	d := sp.Tags[tags[3].start:tags[3].end]
	if !bytes.Equal(d, cat([]byte{0x06, 0}, u16s(sp.Depth, sp.CharID), []byte{0})) {
		t.Errorf("PlaceObject3 %x: not character %d at depth %d without a name", d, sp.CharID, sp.Depth)
	}
	// the class reads the channel file this package writes and the region area's state, and hides the game's choice
	for _, s := range []string{"Channel/dgchannel.json", "region.state", "PREFERRED REGION", "mcPVPLobby", "mcMatchAllRegionsCb",
		"mcRegionActivityIndicator"} {
		if !bytes.Contains(sp.Tags, []byte(s)) {
			t.Errorf("the class has no %q", s)
		}
	}
	// it only reads: no region.set, no call of the lobby's native for a region change
	if bytes.Contains(sp.Tags, []byte("region.set")) || bytes.Contains(sp.Tags, []byte("OnServerRegionChangeRequested")) {
		t.Error("the class can change the region")
	}
	once, err := sp.PatchMovie(synth())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.PatchMovie(once); err == nil || !strings.Contains(err.Error(), "the party lobby's Preferred Region already") {
		t.Errorf("twice: %v", err)
	}
}

// The archive: the movie, then where the UI channel of WebHost is (DefaultWebHost when empty); the bytes
// test_picker.py pins.
func TestPVPLobbyArchive(t *testing.T) {
	src := snap.Pack([]snap.File{{Name: PVPLobby.MoviePath, Data: synth()}})
	sp := PVPLobby
	sp.Tags = []byte("OURTAGS")
	for _, c := range []struct{ host, want string }{
		{"", pvpLobbySynth},
		{"192.168.1.20", pvpLobbySynthLAN},
	} {
		sp.WebHost = c.host
		out, err := sp.Build(src)
		if err != nil {
			t.Fatal(err)
		}
		a, err := snap.Parse(out)
		if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != PVPLobby.MoviePath || a.Entries[1].Name != ChannelPath {
			t.Fatalf("the archive: %v, %+v", err, a)
		}
		host := c.host
		if host == "" {
			host = DefaultWebHost
		}
		want, _ := ChannelFile(host, ChannelPort)
		if got, err := a.Read(a.Entries[1]); err != nil || !bytes.Equal(got, want) {
			t.Errorf("the archive's channel file %q, %v", got, err)
		}
		if got := sum(out); got != c.want {
			t.Errorf("Build(host %q): %d bytes, %s; patch_login_movie.py's: %s", c.host, len(out), got, c.want)
		}
	}
	sp.WebHost = "a b"
	if _, err := sp.Build(src); err == nil {
		t.Error("a bad host passed Build")
	}
}

// What test_picker.py's pvplobby case gives for the same made-up inputs.
const (
	pvpLobbySynth    = "6c689c298fd196d9d9df1bbb53730caba55e87c61adf4cb3d83ed8330d705498"
	pvpLobbySynthLAN = "b76ea4923c8b96d282e632adaab609965345a059d9a33f5017bcc166749bdf77"
)

// The player's own Battlegrounds.snap of build 854 (skipped without the game files).
func TestBuildPVPLobby854(t *testing.T) {
	raw, err := os.ReadFile(testgame.Data(t, "Battlegrounds.snap"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := PVPLobby.Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snap.Parse(out)
	if err != nil || len(a.Entries) != 2 || a.Entries[0].Name != PVPLobby.MoviePath {
		t.Fatalf("the archive: %v, %+v", err, a)
	}
	movie, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	s, err := scan(inflate(t, movie[8:]))
	if err != nil || !s.names["PartyRegion"] || !s.names["maxis.PVPLobbyUI"] || !s.ids[2000] || !s.depths[2000] {
		t.Errorf("the patched movie: %v; %v", err, s.names)
	}
	again, _ := PVPLobby.Build(raw)
	if !bytes.Equal(again, out) {
		t.Errorf("Build isn't deterministic")
	}
	if p := os.Getenv("DAWNGATE_PVPLOBBY_OUT"); p != "" { // for a byte-for-byte comparison with the Python twin
		if err := os.WriteFile(p, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
