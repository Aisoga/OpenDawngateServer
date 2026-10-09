package configpatch

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/testgame"
)

// patch_config.py's localize(value, "HOST", 1234) for each value (Python 3.12; nil = None).
func TestLocalizeLikePython(t *testing.T) {
	o := Options{Host: "HOST", HTTPPort: 1234, TelemetryPort: 1}
	host, url := "HOST", "http://HOST:1234"
	for in, want := range map[string]*string{
		"http://cdn.strategy.ea.com/battlegrounds_test/test/x.xml": ptr(url + "/battlegrounds_test/test/x.xml"),
		"https://a.b/c d":             nil,
		"http://x":                    ptr(url + "/"),
		"http://x\n":                  ptr(url + "/"),
		"http://x/y\n":                ptr(url + "/y"),
		"http://x/y\n\n":              nil,
		"ftp+v1.2-x://host:1/p?q=1#f": ptr(url + "/p?q=1#f"),
		"1http://x/":                  nil,
		"http:/x/y":                   nil,
		"://x":                        nil,
		"http://a b/c":                nil,
		"http://a\tb":                 nil,
		"http://x/\u00a0":             nil,
		"http://x/\x1f":               nil,
		"159.153.235.32":              &host,
		"1.2.3.4\n":                   &host,
		"1.2.3":                       nil,
		"1.2.3.4.5":                   nil,
		"1234.1.1.1":                  nil,
		"\u0661.\u0662.\u0663.\u0664": &host,
		"\u00b2.1.1.1":                nil,
		"ea.com":                      &host,
		"EA.COM":                      &host,
		"a.b.tv":                      &host,
		"x.org\n":                     &host,
		".com":                        nil,
		"a..com":                      nil,
		"com":                         nil,
		"a.co":                        nil,
		"\u0130.com":                  &host,
		"\u212a.net":                  &host,
		"\u017f.org":                  &host,
		"a.\u212aom":                  nil,
		"a_b.com":                     nil,
		"a-b.com":                     &host,
		"a.com.":                      nil,
		"localhost":                   nil,
		"":                            nil,
		"0x1":                         nil,
		"a.c\u00f6m":                  nil,
	} {
		got, ok := localize(in, o)
		switch {
		case want == nil && ok:
			t.Errorf("localize(%q) = %q, want None", in, got)
		case want != nil && (!ok || got != *want):
			t.Errorf("localize(%q) = %q %v, want %q", in, got, ok, *want)
		}
	}
}

func ptr(s string) *string { return &s }

// Binary property list writers (props.py's layout, big-endian).
func phead(id uint32, typ, flags uint16) []byte {
	b := binary.BigEndian.AppendUint32(nil, id)
	b = binary.BigEndian.AppendUint16(b, typ)
	return binary.BigEndian.AppendUint16(b, flags)
}

func pstr8(id uint32, s string) []byte {
	b := binary.BigEndian.AppendUint32(phead(id, typeString8, 0), uint32(len(s)))
	return append(b, s...)
}

func pstr16(id uint32, s string) []byte {
	u := utf16.Encode([]rune(s))
	b := binary.BigEndian.AppendUint32(phead(id, typeString16, 0), uint32(len(u)))
	for _, x := range u {
		b = binary.BigEndian.AppendUint16(b, x)
	}
	return b
}

func pint(id uint32, typ uint16, v uint32) []byte {
	return binary.BigEndian.AppendUint32(phead(id, typ, 0), v)
}

func pfixed(id uint32, typ uint16, n int) []byte {
	b := phead(id, typ, 0)
	for i := 0; i < n; i++ {
		b = append(b, byte(i+1))
	}
	return b
}

func pstrs(id uint32, size uint32, items ...string) []byte {
	b := binary.BigEndian.AppendUint32(phead(id, typeString8, 0x10), uint32(len(items)))
	b = binary.BigEndian.AppendUint32(b, size)
	for _, s := range items {
		b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
		b = append(b, s...)
	}
	return b
}

func plist(props ...[]byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(props)))
	for _, p := range props {
		b = append(b, p...)
	}
	return b
}

// synthText is a made-up app property file with the forms patch_config.py's TEXT_PROP meets (Latin-1, CRLF).
const synthText = "# a comment: property Fake 0x1 string8 http://ea.com/x stays\r\n" +
	"property NewTelemetryServerIP 0x12345678 string8 159.153.235.32\r\n" +
	"property NewTelemetryServerPort (hash(NewTelemetryServerPort)) uint 9988\r\n" +
	"property NewTelemetryServerConfigURL 0x1 string8 \"http://cdn.strategy.ea.com/battlegrounds_test/live/telemetry_rules.xml\"\r\n" +
	"property CrashReportingServerURL 0x2 string16 \"http://crash.ea.com/submit\"\r\n" +
	"  property Indented 0x3 string8 www.dawngate.com\r\n" +
	"property Quoted 0x4 string8 \"a b\"\r\n" +
	"property EmptyQuoted 0x5 string8 \"\"\r\n" +
	"property NotAHost 0x6 string8 hello\r\n" +
	"property Number 0x7 int 42\r\n" +
	"property AFKThreshhold 0x8 uint 4\r\n" +
	"property AFKDisconnectThreshhold 0x9 uint \"01\"\r\n" +
	"property\tTabs\t0xA\tstring8\t\t10.0.0.1\r\n" +
	"property Latin 0xC string8 caf\xe9.com\r\n" +
	"property Word\xe9 0xD string\xe98 1.2.3.4\r\n" +
	"property Twice 0xE string8 a.com property Again 0xF string8 b.com\r\n" +
	"propertyX 0x10 string8 c.com\r\n" +
	"property NewTelemetryServerPort 0x11 string8 9988\r\n" +
	"property Last 0x12 string8 http://x.net"

// synthPackages are made-up Config, Config_Ship and Config_Pack packages with the entry types and property
// forms patch_config.py handles (compressed and plain entries, constant and per-entry index fields).
func synthPackages() map[string][]byte {
	ship := plist(
		pstr8(propID("NewTelemetryServerIP"), "159.153.239.205"),
		pint(telemetryPortID, typeUint32, 9988),
		pstr16(propID("NetworkBootstrapURL"), "http://malbec.spore.rws.ad.ea.com:56000/api/config/list/"),
		pstrs(0x11111111, 0, "a.com", "b.com"),
		pint(propID(afkName), typeUint32, 4),
		pint(propID(afkDisconnectName), typeUint32, 1),
		pint(0x22222222, typeInt32, 5),
		pfixed(0x33333333, 0x0D, 4), pfixed(0x44444444, 0x31, 16), pfixed(0x55555555, 0x01, 1), pfixed(0x66666666, 0x20, 12),
		pstr8(0x77777777, "hello"),
		pstr16(0x88888888, "\u00e9\u20ac.com"),
		pstr8(0x99999999, "http://x.org/a\n"),
	)
	other := plist(pstr8(0xAAAAAAAA, "nothing here"), pint(0xBBBBBBBB, typeUint32, 7))
	pack := plist(
		pstrs(allowedDomainsID, 0x10, ".dawngate.com", "cdn.strategy.ea.com"),
		pstr8(0xCCCCCCCC, "https://www.dawngate.com/home"),
	)
	text2 := "property NetworkBootstrapURL 0x10 string8 http://pollinator.spore.com/pollinator/public-interface/\n"
	return map[string][]byte{
		"Config.package": testgame.Package(0, []testgame.PackageEntry{
			{Type: TextType, Instance: 0x8C47952D, Data: testgame.RefPackLiterals([]byte(synthText)), Comp: dbpf.CompRefPack, USize: uint32(len(synthText))},
			{Type: TextType, Instance: 0xB16AFE42, Data: []byte(text2)},
			{Type: 0x12345678, Instance: 1, Data: []byte("not a property file: http://ea.com")},
		}),
		"Config_Ship.package": testgame.Package(3, []testgame.PackageEntry{
			{Type: BinaryType, Instance: 0x08EA246B, Data: ship},
			{Type: BinaryType, Instance: 0x08EA246C, Data: testgame.RefPackLiterals(other), Comp: dbpf.CompRefPack, USize: uint32(len(other))},
		}),
		"Config_Pack.package": testgame.Package(4, []testgame.PackageEntry{
			{Type: BinaryType, Instance: 0x9DD4AB9B, Data: pack},
			{Type: TextType, Instance: 0x9DD4AB9C, Data: []byte("property X 0x1 string8 http://cdn.strategy.ea.com/a\n")},
		}),
	}
}

// SynthEnv: with it set to a folder, TestWriteSynthetic writes synthPackages there (the input of the
// Python reference hashes below).
const SynthEnv = "DAWNGATE_P3A2_SYNTH_OUT"

func TestWriteSynthetic(t *testing.T) {
	dir := os.Getenv(SynthEnv)
	if dir == "" {
		t.Skipf("set %s to write the synthetic packages", SynthEnv)
	}
	for name, b := range synthPackages() {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// patch_config.py's output for synthPackages (SHA-256).
var synthHashes = []struct {
	o    Options
	want map[string]string
}{
	{Defaults(), map[string]string{
		"Config.package":      "c2715fd625ffe6781fb4b0269be8bae303c32e025431f4e990bc99deae10d671",
		"Config_Ship.package": "8ab859fd95b03f6ebe48b55c8078addd2dd7316ad6f531db595a6f47631614d1",
		"Config_Pack.package": "51e352cade105498259d09bf27a459333582f62017240141a536b581cbabe171",
	}},
	{Options{Host: "192.168.1.20", HTTPPort: 8080, TelemetryPort: 9000, AFKMinutes: 30, AFKDisconnectMinutes: 5}, map[string]string{
		"Config.package":      "b7242a60378b6c1d010283f33ad54ffeeaacf8e0fd87b1f876659b55d3fe0816",
		"Config_Ship.package": "d6839260240e31826f3d609ba39d24bf281b07542aa8739194be1f76f68fa266",
		"Config_Pack.package": "3eb14b4b42e68c9208ca286e63b75e69a50b6e13c8a4ba571e8a170bd8fefb5b",
	}},
}

func TestSyntheticMatchesPatchConfigPy(t *testing.T) {
	pk := synthPackages()
	for _, c := range synthHashes {
		hits := 0
		for _, name := range Packages {
			res, err := Package(pk[name], name, c.o)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got := sha(res.Data); got != c.want[name] {
				t.Errorf("%+v %s: SHA-256 %s, patch_config.py's %s", c.o, name, got, c.want[name])
			}
			hits += len(res.AFKHits)
		}
		if c.o.AFKMinutes != 0 && hits != 4 {
			t.Errorf("%d AFK hits", hits)
		}
	}
}

// The patched synthetic text entry, line by line, and the unchanged entries kept as they were.
func TestSyntheticText(t *testing.T) {
	pk := synthPackages()
	res, err := Package(pk["Config.package"], "Config.package", Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p, err := dbpf.Parse(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if p.Entries[0].Comp != dbpf.CompNone || p.Entries[1].Comp != dbpf.CompNone || res.Entries != 2 {
		t.Errorf("entries %+v, %d changed", p.Entries, res.Entries)
	}
	b, _ := p.Read(p.Entries[0])
	want := strings.NewReplacer(
		"string8 159.153.235.32", "string8 127.0.0.1",
		"uint 9988", "uint 9988",
		`"http://cdn.strategy.ea.com/battlegrounds_test/live/telemetry_rules.xml"`, `"http://127.0.0.1:18080/battlegrounds_test/live/telemetry_rules.xml"`,
		`"http://crash.ea.com/submit"`, `"http://127.0.0.1:18080/submit"`,
		"string8 www.dawngate.com", "string8 127.0.0.1",
		"\t\t10.0.0.1", "\t\t127.0.0.1",
		"string\xe98 1.2.3.4", "string\xe98 1.2.3.4",
		"string8 a.com property", "string8 127.0.0.1 property",
		"string8 http://x.net", "string8 http://127.0.0.1:18080/",
	).Replace(synthText)
	if string(b) != want {
		t.Errorf("text:\n%q\nwant\n%q", b, want)
	}
	raw, _ := p.Raw(p.Entries[2])
	if string(raw) != "not a property file: http://ea.com" {
		t.Errorf("other entry: %q", raw)
	}
}

func TestRefusals(t *testing.T) {
	pk := synthPackages()
	for _, o := range []Options{
		{Host: "", HTTPPort: 1, TelemetryPort: 1},
		{Host: "a b", HTTPPort: 1, TelemetryPort: 1},
		{Host: "a\"b", HTTPPort: 1, TelemetryPort: 1},
		{Host: "h\u00e9", HTTPPort: 1, TelemetryPort: 1},
		{Host: "h", HTTPPort: 0, TelemetryPort: 1},
		{Host: "h", HTTPPort: 1, TelemetryPort: 70000},
		{Host: "h", HTTPPort: 1, TelemetryPort: 1, AFKMinutes: AFKMax + 1},
		{Host: "h", HTTPPort: 1, TelemetryPort: 1, AFKMinutes: -1},
		{Host: "h", HTTPPort: 1, TelemetryPort: 1, AFKDisconnectMinutes: 2},
	} {
		if _, err := Package(pk["Config.package"], "Config.package", o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
	// an AFK property without its original value
	bad := testgame.Package(0, []testgame.PackageEntry{{Type: TextType, Instance: 1, Data: []byte("property AFKThreshhold 0x8 uint 5\n")}})
	o := Defaults()
	o.AFKMinutes = 10
	if _, err := Package(bad, "Config.package", o); err == nil || !strings.Contains(err.Error(), "original value 5, expected 4") {
		t.Errorf("AFK 5: %v", err)
	}
	if _, err := Package(bad, "Config.package", Defaults()); err != nil {
		t.Errorf("without --afk-minutes the AFK value isn't checked: %v", err)
	}
	for _, v := range []string{"\u00b2", "", "x", "4.0"} { // ² is isdigit but not int()-able
		b := testgame.Package(0, []testgame.PackageEntry{{Type: TextType, Instance: 1, Data: []byte("property AFKThreshhold 0x8 uint \"" + v + "\"\n")}})
		if _, err := Package(b, "Config.package", o); err == nil {
			t.Errorf("AFK %q accepted", v)
		}
	}
	binBad := testgame.Package(0, []testgame.PackageEntry{{Type: BinaryType, Instance: 1, Data: plist(pint(propID(afkDisconnectName), typeUint32, 2))}})
	o.AFKDisconnectMinutes = 3
	if _, err := Package(binBad, "Config_Ship.package", o); err == nil {
		t.Error("binary AFK 2 accepted")
	}
	// a broken property list or package
	for _, b := range [][]byte{
		testgame.Package(0, []testgame.PackageEntry{{Type: BinaryType, Instance: 1, Data: []byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 0x77, 0, 0}}}),
		testgame.Package(0, []testgame.PackageEntry{{Type: BinaryType, Instance: 1, Data: append(plist(pint(1, typeUint32, 1)), 0)}}),
		[]byte("DBPX"),
	} {
		if _, err := Package(b, "x.package", Defaults()); err == nil {
			t.Errorf("%x accepted", b)
		}
	}
}

// Patch on a folder: the pristine-extraction refusal, a missing package skipped, the AFK count check.
func TestPatchDir(t *testing.T) {
	root := t.TempDir()
	if _, err := Patch(filepath.Join(root, "client", "ia_Dawngate", "Dawngate", "game", "Data"), Defaults()); err == nil {
		t.Error("patched a client/ia_* folder")
	}
	dir := filepath.Join(root, "copy")
	os.MkdirAll(dir, 0o755)
	pk := synthPackages()
	os.WriteFile(filepath.Join(dir, "Config.package"), pk["Config.package"], 0o644)
	rep, err := DryRun(dir, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Config.package.orig")); err == nil {
		t.Error("the dry run wrote a .orig")
	}
	if !strings.Contains(strings.Join(rep.Lines, "\n"), "dry run, not written") {
		t.Errorf("dry run: %q", rep.Lines)
	}
	o := Defaults()
	o.AFKMinutes = 30
	if _, err := Patch(dir, o); err == nil || !strings.Contains(err.Error(), "found 1 time(s), expected 2") {
		t.Errorf("AFK count: %v", err)
	}
	rep, err = Patch(dir, Defaults())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(rep.Lines, "\n")
	if !strings.Contains(lines, "Config_Ship.package: missing, skipped") || !strings.Contains(lines, "patched ") {
		t.Errorf("report: %s", lines)
	}
	orig, _ := os.ReadFile(filepath.Join(dir, "Config.package.orig"))
	if string(orig) != string(pk["Config.package"]) {
		t.Error(".orig is not the package before the first patch")
	}
	if _, err := Restore(dir); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(filepath.Join(dir, "Config.package"))
	if string(back) != string(pk["Config.package"]) {
		t.Error("not restored")
	}
}
