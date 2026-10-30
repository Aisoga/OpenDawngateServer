package localepatch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/testgame"
	"dawngate/server/internal/setup/testgame/synthgame"
)

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// MadeUp is a made-up Locale.package with the rule's odd cases (re/scripts/test_ph2b.py builds the same bytes): a
// BOM before a first line with an id, LF and CRLF lines, "0X" upper case, an id twice, a short id, a tab after an id, a
// last line without a line end, a non-ASCII line left alone, another table, and no URL table.
func MadeUp() []byte {
	blaze := "\xef\xbb\xbf0x000c0001 old wrong\n# c\n0X000B0001 old user\r\n0x00650001 disabled http://help\r\n" +
		"0x000c0001 dup\r\n0x0016000 short id\r\n0x00160001\tTabbed\r\n0x00290001 deact"
	login := "# login\r\n0x0ac5be93 Enter Email\r\n0x00000009 Zo\xc3\xab\r\n0x0ac5be95 Remember Email\r\n"
	return dbpf.WritePackage([]dbpf.NewEntry{
		{Type: StringType, Group: StringGroup, Instance: LoginScreen, Data: []byte(login)},
		{Type: StringType, Group: StringGroup, Instance: 0x12345678, Data: []byte("0x00000001 other\r\n")},
		{Type: StringType, Group: StringGroup, Instance: BlazeErrors, Data: []byte(blaze)},
	})
}

// The SHA-256s re/scripts/patch_locale.py gives for the same inputs (test_ph2b.py pins the same values).
const (
	madeUpSHA        = "97766712cea3cf54c20204922cc472d86ac9d592c40e955f7c1a635e0567f25e"
	madeUpLaneB      = "346751fbfa659f69a74c91a6eebe15e97cc0206a35a276bf358e37b487233a3e" // --host 127.0.0.2
	madeUpWebDomain  = "e2ca3fa68bb3c50190a98fa8cc33f704793d64350452326e15f6c39bb5f302cf" // 192.168.1.20, Play.Example.org:8443
	madeUpLobbyAny   = "b5313cd2bf11cafcf66eca7029b62d4dfa10971121ae5170d688d28d560be98e" // 127.0.0.2, LobbyMapAny, testMaps
	synthLocaleSHA   = "528ce9623b3b1542fa600b1036136a3c39e8e68bb586019fb3a6d3c2f6374832" // synthgame.Locale()
	synthLocal       = "476b2381284742ffbb866771e082af47beb87900b1567f35fc15c2cf3d420a7f" // the synthetic game, 127.0.0.1
	game854Local     = "44bb48de9bd21aefa1886d8ea70e672e27d57c0ad1b3abfa53b6f3e35f690024" // build 854, --host 127.0.0.1
	game854LaneB     = "0c36b450e08e6c92431236b9312431d3e093dce51c1f7cc6b700903edcb3cbb3" // --host 127.0.0.2
	game854WebDomain = "ce31788c2878cd41273039ac60765a9561bb51f6bdf70745b30c5dd783f64b97" // 203.0.113.7, dawngate.example.org
)

// testMaps are a maps folder's packages (test_ph2b.py's MAPS): CM_Ring, and one named like MM_Map_v3 (its line is the
// first's).
var testMaps = []Map{{"CM_Ring", "Test Ring"}, {"mm_map_V3", "Not this"}}

func TestBuildMatchesPython(t *testing.T) {
	in := MadeUp()
	if got := digest(in); got != madeUpSHA {
		t.Errorf("the made-up Locale.package: SHA-256 %s, test_ph2b.py's %s", got, madeUpSHA)
	}
	for _, c := range []struct {
		name string
		in   []byte
		o    Options
		want string
	}{
		{"made-up, lane b", in, Options{Host: "127.0.0.2"}, madeUpLaneB},
		{"made-up, web domain", in, Options{Host: "192.168.1.20", WebDomain: "Play.Example.org:8443"}, madeUpWebDomain},
		{"made-up, lane b, a maps folder", in, Options{Host: "127.0.0.2", LobbyMap: LobbyMapAny, Maps: testMaps}, madeUpLobbyAny},
		{"synthetic game", synthgame.Locale(), Options{Host: "127.0.0.1"}, synthLocal},
	} {
		got, err := Build(c.in, c.o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if digest(got) != c.want {
			t.Errorf("%s: SHA-256 %s, patch_locale.py's %s", c.name, digest(got), c.want)
		}
	}
	if got := digest(synthgame.Locale()); got != synthLocaleSHA {
		t.Errorf("synthgame.Locale(): SHA-256 %s, test_ph2b.py's %s", got, synthLocaleSHA)
	}
	if dir := os.Getenv("DAWNGATE_PH2B_SYNTH_OUT"); dir != "" { // the inputs for patch_locale.py
		for name, b := range map[string][]byte{"madeup.package": in, "synthgame.package": synthgame.Locale()} {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Build 854's Locale.package (skipped without the game files).
func TestBuildGame(t *testing.T) {
	in := testgame.Read(t, "Locale", "en-us", Shipped)
	for _, c := range []struct {
		o    Options
		want string
	}{
		{Options{Host: "127.0.0.1"}, game854Local},
		{Options{Host: "127.0.0.2"}, game854LaneB},
		{Options{Host: "203.0.113.7", WebDomain: "dawngate.example.org"}, game854WebDomain},
	} {
		got, err := Build(in, c.o)
		if err != nil {
			t.Fatal(err)
		}
		if digest(got) != c.want {
			t.Errorf("%+v: SHA-256 %s, patch_locale.py's %s", c.o, digest(got), c.want)
		}
		// what the client reads back: every new line once, the other lines of the tables unchanged
		p, err := dbpf.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		tabs, _ := Tables(c.o)
		for i, e := range p.Entries {
			text, err := p.Read(e)
			if err != nil {
				t.Fatal(err)
			}
			for id, line := range tabs[uint32(e.Instance)] {
				if n := strings.Count(string(text), "0x"+hex.EncodeToString([]byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)})+" "+line+"\r\n"); n != 1 {
					t.Errorf("entry %d: line %08x %q found %d times", i, id, line, n)
				}
			}
			// the custom lobby's table: the shipped one (no final line end) and the appended map lines (the fallback,
			// MM_Map_v3's LevelId)
			if e.Instance == FECustomGameLobby {
				sp, err := dbpf.Parse(in)
				if err != nil {
					t.Fatal(err)
				}
				shipped, err := table(sp, FECustomGameLobby)
				if want := string(shipped) + "\r\n" + Comment + "\r\n0x0f2b57c5 Anshavar Pass\r\n0x420de52e Anshavar Pass\r\n"; err != nil ||
					!strings.HasSuffix(string(shipped), "0x0f2b5801 Please Wait") || string(text) != want {
					t.Errorf("FE_CustomGameLobby: %q (%v), want the shipped table and the map line", text, err)
				}
			}
		}
		if len(p.Entries) != 7 {
			t.Errorf("%d tables, want 7", len(p.Entries))
		}
	}
}

func TestPatchTable(t *testing.T) {
	lines := map[uint32]string{1: "one", 2: "two"}
	for _, c := range []struct{ in, want string }{
		{"", Comment + "\r\n0x00000001 one\r\n0x00000002 two\r\n"},
		{"0x00000001 a\r\n0x00000002 b", "0x00000001 one\r\n0x00000002 two"},
		{"0x00000001 a\n", "0x00000001 one\n" + Comment + "\r\n0x00000002 two\r\n"},
		{"x", "x\r\n" + Comment + "\r\n0x00000001 one\r\n0x00000002 two\r\n"},
		{"\xef\xbb\xbf0X00000002 b\r\n0x00000001  a", "\xef\xbb\xbf0X00000002 two\r\n0x00000001 one"},
	} {
		if got := string(PatchTable([]byte(c.in), lines)); got != c.want {
			t.Errorf("PatchTable(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOptions(t *testing.T) {
	for _, c := range []struct {
		o    Options
		base string
	}{
		{Options{Host: "127.0.0.1"}, "http://127.0.0.1:18080/dawngate/"},
		{Options{Host: "192.168.1.20", HTTPPort: 8080}, "http://192.168.1.20:8080/dawngate/"},
		{Options{Host: "127.0.0.1", WebDomain: "Dawngate.Example.ORG"}, "https://dawngate.example.org/"},
		{Options{Host: "127.0.0.1", WebDomain: "a.b:8443"}, "https://a.b:8443/"},
	} {
		if got, err := c.o.Base(); err != nil || got != c.base {
			t.Errorf("%+v: %q %v, want %q", c.o, got, err, c.base)
		}
	}
	for _, o := range []Options{{Host: "localhost"}, {Host: "::1"}, {Host: ""}, {Host: "127.0.0.1", HTTPPort: 70000},
		{Host: "127.0.0.1", WebDomain: "nodots"}, {Host: "127.0.0.1", WebDomain: "a.b/c"}, {Host: "127.0.0.1", WebDomain: "a.b:0"},
		{Host: "127.0.0.1", WebDomain: "-a.b"}, {Host: "127.0.0.1", WebDomain: "a.b:99999"}, {Host: "127.0.0.1", WebDomain: "dä.de"}} {
		if _, err := Build(MadeUp(), o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

// TestLobbyMapFor: the lobby's one map line names the copy's one map besides The Dawngate, none (LobbyMapAny) for
// several, and stays LobbyLines' own for MM_Map_v3 alone or none.
func TestLobbyMapFor(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""}, {[]string{"Anshavar Pass"}, ""}, {[]string{"Test Ring"}, "Test Ring"},
		{[]string{"Anshavar Pass", "Test Ring"}, LobbyMapAny},
	} {
		if got := LobbyMapFor(c.in); got != c.want {
			t.Errorf("LobbyMapFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	tabs, err := Tables(Options{Host: "127.0.0.1", LobbyMap: "Test Ring"})
	if err != nil || len(tabs[FECustomGameLobby]) != 2 || tabs[FECustomGameLobby][LobbyMapLine] != "Test Ring" ||
		LobbyLines[LobbyMapLine] != "Anshavar Pass" {
		t.Errorf("%v %v", err, tabs[FECustomGameLobby])
	}
}

// TestMapLines: a map's line in FE_CustomGameLobby is its LevelId's (dgserver's ids: MM_Map_v3 1108206894, CM_Ring
// 4050252090), MM_Map_v3's always, the first of an id kept, never over a line the shipped table has.
func TestMapLines(t *testing.T) {
	if a, b, c := LevelID("MM_Map_v3"), LevelID("CM_Ring"), LevelID("SM_Map_v5"); a != 0x420DE52E || b != 0xF169F13A ||
		c != 0x946D41FE {
		t.Errorf("LevelIDs %#x %#x %#x", a, b, c)
	}
	tabs, err := Tables(Options{Host: "127.0.0.1", LobbyMap: LobbyMapAny, Maps: testMaps})
	want := map[uint32]string{LobbyMapLine: LobbyMapAny, 0x420DE52E: "Anshavar Pass", 0xF169F13A: "Test Ring"}
	if err != nil || fmt.Sprint(tabs[FECustomGameLobby]) != fmt.Sprint(want) {
		t.Errorf("%v %v", err, tabs[FECustomGameLobby])
	}
	got := withoutLinesOf([]byte("\xef\xbb\xbf0x420de52e Kick\r\n0x0000000a x"), want)
	if fmt.Sprint(got) != fmt.Sprint(map[uint32]string{LobbyMapLine: LobbyMapAny, 0xF169F13A: "Test Ring"}) {
		t.Errorf("withoutLinesOf: %v", got)
	}
}
