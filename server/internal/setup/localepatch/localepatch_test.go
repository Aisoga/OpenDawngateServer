package localepatch

import (
	"crypto/sha256"
	"encoding/hex"
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
	madeUpLaneB      = "1a3acbb8f1854809a76d72db929ffa296bbec55ffb4730d6f2cbe029e201e58e" // --host 127.0.0.2
	madeUpWebDomain  = "7f664e5a945e93f9682191ae509c85a32442733f7e38be5f271c6e6ddfdf8e8c" // 192.168.1.20, Play.Example.org:8443
	synthLocaleSHA   = "528ce9623b3b1542fa600b1036136a3c39e8e68bb586019fb3a6d3c2f6374832" // synthgame.Locale()
	synthLocal       = "1be4652de6a59c8690e3a9c5597b5969185f175481cede1ad309a636883de043" // the synthetic game, 127.0.0.1
	game854Local     = "cf08c095ce697dac1b1035053ba1bd5d49a4df1ae1ae62e67636ee92f39003df" // build 854, --host 127.0.0.1
	game854LaneB     = "f9f258f930efb0b427c0d862f441d97c126a127e4ad23f9b790d716eaa68511c" // --host 127.0.0.2
	game854WebDomain = "fdb122b463579fc8560c24a91dc64b2f32dbd780cd821ca3ccec0e4d3faacabf" // 203.0.113.7, dawngate.example.org
)

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
