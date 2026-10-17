package exepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/setuptest"
)

var (
	modA = bytes.Repeat([]byte{0x5A}, 128) // test_a5.py's MODULUS
	modB = func() []byte {                 // bytes(range(128))
		b := make([]byte, 128)
		for i := range b {
			b[i] = byte(i)
		}
		return b
	}()
)

// What patch_client.py's patch_bytes makes from the untouched build-854 exe (SHA-256 of the output, summary, describe),
// recorded with re/scripts/patch_client.py (patch_bytes on the untouched exe with the same inputs). "local", "q3b"
// and "a4" are also test_a5.py's SNDBANK_OUTPUT_SHA256, Q3B_OUTPUT_SHA256 and A4_OUTPUT_SHA256.
var reference = []struct {
	name    string
	mod     []byte
	o       Options
	sha256  string
	summary string
	desc    string // redirector bind appdata documents raknet_loopback ca_sha256 ca_flag laa preload
}{
	{"local", modA, Options{}, "a6ca0ae63b8815bf38aeb4575f61a951e4c92f5a159f0c1465f4a47cfe57267e",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all"},
	{"local_laa_off", modA, Options{NoLAA: true}, "3952c970b7c0f5406c78bf709cd61ddccea25024dfb28fcb8405ea91f275500f",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); every player's shaper preloaded (player indexes 0-14)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 off all"},
	{"local_modb", modB, Options{Profile: ProfileLocal, ServerHost: "127.0.0.1"}, "3379939d8a729b4c454195e2a154ad1c89560a0cf597b68e920cb9b24af7336a",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 471fb943aa23c511 0 on all"},
	{"local_localhost", modA, Options{ServerHost: "localhost"}, "7431b6bc72edb5af24d8fb99b8b07cf074036817cd37a4455c49625015727c9e",
		"redirector host -> localhost; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"localhost 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all"},
	{"lan", modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20"}, "fbcc60f8a404c429325272eec2bf702d81ff12b7919068590db5589c3e58f9d0",
		"redirector host -> 192.168.1.20; UPnP off (-noupnp forced, discovery refused); sockets as shipped (wildcard binds; loopback patch left out); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"192.168.1.20 as-shipped DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all"},
	{"lan_bind", modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20", ClientBind: "192.168.1.30"}, "ebd289caee5abb86e42e92fd55461541bb587230e5bc01e72424d0c524042bc7",
		"redirector host -> 192.168.1.20; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 192.168.1.30 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 192.168.1.30; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"192.168.1.20 192.168.1.30 DawngateData Dawngate 192.168.1.30 349d65e9ba1de7b0 0 on all"},
	{"lan_bind_same", modB, Options{Profile: ProfileLAN, ServerHost: "192.168.20.40", ClientBind: "192.168.20.40"}, "d125107672631d66d6365e2fee3d6b0c1dc082ca0f92fa99b2c5796e2be24c47",
		"redirector host -> 192.168.20.40; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 192.168.20.40 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 192.168.20.40; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"192.168.20.40 192.168.20.40 DawngateData Dawngate 192.168.20.40 471fb943aa23c511 0 on all"},
	{"lan_cgnat", modA, Options{Profile: ProfileLAN, ServerHost: "100.64.1.2"}, "cc5cd0ba047b20ace7a47471abac2339459901c5868662409d5776d39edbbea9",
		"redirector host -> 100.64.1.2; UPnP off (-noupnp forced, discovery refused); sockets as shipped (wildcard binds; loopback patch left out); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"100.64.1.2 as-shipped DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all"},
	{"lane_b", modA, Options{ServerHost: "127.0.0.2", DataSuffix: "B"}, "a86a9d48730521c5f466a103708c6fddaa8c59a7b8415aaa6efdf56f4ca26f36",
		`redirector host -> 127.0.0.2; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 127.0.0.2 (bind cave at 0x011ab100, 6 sites), RakNet loopback 127.0.0.2; data folders %APPDATA%\DawngateDataB, Documents\DawngateB; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)`,
		"127.0.0.2 127.0.0.2 DawngateDataB DawngateB 127.0.0.2 349d65e9ba1de7b0 0 on all"},
	{"lane_e", modA, Options{ServerHost: "127.0.0.4", ClientBind: "127.0.4.2", DataSuffix: "E"}, "6e7905c18b970c88208801f977a45a8934a1f59d0136c4c7b46aaad9ec414c8a",
		`redirector host -> 127.0.0.4; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 127.0.4.2 (bind cave at 0x011ab100, 6 sites), RakNet loopback 127.0.4.2; data folders %APPDATA%\DawngateDataE, Documents\DawngateE; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)`,
		"127.0.0.4 127.0.4.2 DawngateDataE DawngateE 127.0.4.2 349d65e9ba1de7b0 0 on all"},
	{"lan_test_l", modA, Options{Profile: ProfileLAN, ServerHost: "10.0.0.5", ClientBind: "10.0.0.5", DataSuffix: "L"}, "fbde36d91a304e2908281b3c3d66fdf8b06af4136223323f1909ade076b5c7ed",
		`redirector host -> 10.0.0.5; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 10.0.0.5 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 10.0.0.5; data folders %APPDATA%\DawngateDataL, Documents\DawngateL; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)`,
		"10.0.0.5 10.0.0.5 DawngateDataL DawngateL 10.0.0.5 349d65e9ba1de7b0 0 on all"},
	{"q3b", modA, Options{NoPreload: true}, "237512dc68b4135e220b410d92a6579cd602188079bd093b365871e38d214613",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on as-shipped"},
	{"a4", modA, Options{NoLAA: true, NoPreload: true}, "a15dd7ad91326f3114a054fd72ef20248a833a253e0c86a7d63fc13a0ecef027",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 off as-shipped"},
}

const origDescription = "gosredirector.ea.com as-shipped DawngateData Dawngate 127.0.0.1 e96f07ca645d1661 1 off as-shipped"

func descLine(d Description) string {
	var parts []string
	for _, k := range DescriptionKeys {
		parts = append(parts, d[k])
	}
	return strings.Join(parts, " ")
}

// The game's exe: DG_PATCH_EXE, else the main checkout's client/ia_Dawngate (read only). Skipped without it.
func gameExe(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(setuptest.GameFile(t, "DG_PATCH_EXE", "game/Dawngate.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(b) != Build854SHA256 {
		t.Skip("the exe is not the untouched build 854")
	}
	return b
}

// Byte for byte what patch_client.py makes, per profile (the reference hashes above).
func TestPatchMatchesPatchClient(t *testing.T) {
	orig := gameExe(t)
	keep := bytes.Clone(orig)
	d, err := Describe(orig)
	if err != nil || descLine(d) != origDescription {
		t.Errorf("untouched: %q %v", descLine(d), err)
	}
	for _, c := range reference {
		got, summary, err := Patch(orig, c.mod, c.o)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if sha256Hex(got) != c.sha256 {
			t.Errorf("%s: SHA-256 %s, patch_client.py's %s", c.name, sha256Hex(got), c.sha256)
		}
		if summary != c.summary {
			t.Errorf("%s: summary\n %s\nwant\n %s", c.name, summary, c.summary)
		}
		d, err := Describe(got)
		if err != nil || descLine(d) != c.desc {
			t.Errorf("%s: describe %q, want %q (%v)", c.name, descLine(d), c.desc, err)
		}
	}
	if !bytes.Equal(orig, keep) {
		t.Error("Patch changed its input")
	}
	// patch_client.py's refusals that need the exe
	patched, _, _ := Patch(orig, modA, Options{})
	if _, _, err := Patch(patched, modA, Options{}); err == nil || !strings.Contains(err.Error(), "OTG3 modulus at 0x01301d48") {
		t.Errorf("an already patched exe: %v", err)
	}
	if _, _, err := Patch(orig, modA, Options{ServerHost: "127.100.100.100"}); err == nil ||
		err.Error() != "127.100.100.100 doesn't fit RakNet's loopback literal" {
		t.Errorf("a long loopback address: %v", err)
	}
}

// On request: DG_PATCHFILE_EXE=<a scratch copy of the untouched exe> DG_PATCH_MODULUS=<ca_modulus.bin> runs PatchFile
// (local profile) on it and logs the result's SHA-256, to hold against patch_client.py's on another copy.
func TestPatchFileOnACopy(t *testing.T) {
	exe, modFile := os.Getenv("DG_PATCHFILE_EXE"), os.Getenv("DG_PATCH_MODULUS")
	if exe == "" || modFile == "" {
		t.Skip("set DG_PATCHFILE_EXE and DG_PATCH_MODULUS")
	}
	mod, err := os.ReadFile(modFile)
	if err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(exe, mod, FileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exe)
	t.Logf("%+v\nSHA-256 %s", res, sha256Hex(b))
}

func TestBuildHashIsTheManifests(t *testing.T) {
	f, ok := archive.Build854().Lookup("game/Dawngate.exe")
	if !ok || f.SHA256 != Build854SHA256 {
		t.Errorf("manifest %+v, Build854SHA256 %s", f, Build854SHA256)
	}
}

// syntheticExe is SyntheticExe (synthetic.go), the name the tests use.
func syntheticExe() []byte { return SyntheticExe() }

// diffRanges lists [start, end) of the byte ranges where a and b differ (same length).
func diffRanges(a, b []byte) [][2]int {
	var out [][2]int
	start := -1
	for i := range a {
		if a[i] != b[i] && start < 0 {
			start = i
		} else if a[i] == b[i] && start >= 0 {
			out = append(out, [2]int{start, i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(a)})
	}
	return out
}

func TestSyntheticLocal(t *testing.T) {
	orig := syntheticExe()
	d, err := Describe(orig)
	if err != nil || descLine(d) != "gosredirector.ea.com as-shipped DawngateData Dawngate 127.0.0.1 "+sha256Hex(orig[synthOffset(otg3Modulus) : synthOffset(otg3Modulus)+128])[:16]+" 1 off as-shipped" {
		t.Fatalf("untouched synthetic: %q %v", descLine(d), err)
	}
	got, summary, err := Patch(orig, modB, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if summary != reference[0].summary {
		t.Errorf("summary %q", summary)
	}
	d, _ = Describe(got)
	if want := "127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 471fb943aa23c511 0 on all"; descLine(d) != want {
		t.Errorf("describe %q, want %q", descLine(d), want)
	}
	// the changes, all where patch_client.py makes them
	vsizeAt := 0x80 + 4 + 20 + 0xE0 + 8
	allowed := [][2]int{
		{synthOffset(otg3Modulus), 128}, {synthOffset(otg3Flags), 4}, {synthOffset(redirectorHost), 21},
		{synthOffset(noupnpJnz), 1}, {synthOffset(upnpDisc), len(upnpDiscNew)}, {0x80 + 4 + 18, 2}, {vsizeAt, 4},
		{synthOffset(loopbackCave), loopbackCaveLen}, {synthOffset(bindThunk), 6},
		{synthOffset(preloadCount), len(preloadCountNew)},
	}
	for _, va := range bindCalls {
		allowed = append(allowed, [2]int{synthOffset(va), 6})
	}
	for _, r := range diffRanges(orig, got) {
		ok := false
		for _, a := range allowed {
			ok = ok || a[0] <= r[0] && r[1] <= a[0]+a[1]
		}
		if !ok {
			t.Errorf("unexpected change at %#x..%#x", r[0], r[1])
		}
	}
	if v := binary.LittleEndian.Uint32(got[vsizeAt:]); v != 0xDAA200 {
		t.Errorf(".text VirtualSize %#x", v)
	}
	// every bind site calls (the thunk jumps) to the cave, after a nop
	for _, va := range append(bindCalls, bindThunk) {
		o := synthOffset(va)
		rel := binary.LittleEndian.Uint32(got[o+2:])
		if got[o] != 0x90 || va+6+rel != loopbackCave {
			t.Errorf("site %#x: % x", va, got[o:o+6])
		}
	}
}

func TestSyntheticLANAndLanes(t *testing.T) {
	orig := syntheticExe()
	lan, summary, err := Patch(orig, modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "sockets as shipped") {
		t.Errorf("summary %q", summary)
	}
	if !bytes.Equal(lan[synthOffset(loopbackCave):synthOffset(loopbackCave)+0x100], make([]byte, 0x100)) {
		t.Error("lan without a client bind wrote the cave")
	}
	bind, _, err := Patch(orig, modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20", ClientBind: "192.168.1.30"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := Describe(bind)
	if d["bind"] != "192.168.1.30" || d["raknet_loopback"] != "192.168.1.30" {
		t.Errorf("lan bind: %v", d)
	}
	lane, _, err := Patch(orig, modA, Options{ServerHost: "127.0.0.2", DataSuffix: "B"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ = Describe(lane)
	if d["appdata"] != "DawngateDataB" || d["documents"] != "DawngateB" || d["raknet_loopback"] != "127.0.0.2" {
		t.Errorf("lane b: %v", d)
	}
	if _, _, err := Patch(orig, modA, Options{ServerHost: "127.100.100.100"}); err == nil {
		t.Error("a loopback address longer than RakNet's literal accepted")
	}
}

func TestSyntheticRefusals(t *testing.T) {
	orig := syntheticExe()
	patched, _, err := Patch(orig, modA, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Patch(patched, modA, Options{}); err == nil || !strings.Contains(err.Error(), "OTG3 modulus") {
		t.Errorf("an already patched exe: %v", err)
	}
	if _, _, err := Patch(orig, modA[:64], Options{}); err == nil {
		t.Error("a 64-byte modulus accepted")
	}
	// one wrong byte at a checked place: refused in every profile (the wrong build)
	for _, va := range []uint32{bindCalls[0], bindThunk, redirectorHost, noupnpJnz, raknetLoopback, appdirDefault,
		otg3KeyInfo, otg3Flags} {
		bad := bytes.Clone(orig)
		bad[synthOffset(va)] ^= 0xFF
		for _, o := range []Options{{}, {Profile: ProfileLAN, ServerHost: "192.168.1.20"}} {
			_, _, err := Patch(bad, modA, o)
			var pe *Error
			if !errors.As(err, &pe) {
				t.Errorf("%#x %+v: %v", va, o, err)
			}
		}
	}
	// relocatable, other characteristics, not a PE
	aslr := bytes.Clone(orig)
	aslr[0x80+4+20+70] |= 0x40
	if _, _, err := Patch(aslr, modA, Options{}); err == nil || !strings.Contains(err.Error(), "relocatable") {
		t.Errorf("ASLR: %v", err)
	}
	chars := bytes.Clone(orig)
	chars[0x80+4+18] = 0x02
	if _, _, err := Patch(chars, modA, Options{Profile: ProfileLAN, ServerHost: "10.0.0.1"}); err == nil {
		t.Error("other PE characteristics accepted")
	}
	if _, _, err := Patch([]byte("not an exe"), modA, Options{}); err == nil {
		t.Error("not a PE accepted")
	}
}

// patch_client.py's option refusals and accepted forms (results recorded with Python 3.12.7's ipaddress).
func TestProfiles(t *testing.T) {
	ok := []struct {
		o            Options
		server, cave string
	}{
		{Options{}, "127.0.0.1", "127.0.0.1"},
		{Options{ServerHost: "localhost"}, "localhost", "127.0.0.1"},
		{Options{ServerHost: "LocalHost"}, "LocalHost", "127.0.0.1"},
		{Options{ServerHost: "127.0.0.5"}, "127.0.0.5", "127.0.0.5"},
		{Options{ServerHost: "127.0.0.2", ClientBind: "127.0.0.3"}, "127.0.0.2", "127.0.0.3"},
		{Options{ServerHost: "localhost", ClientBind: "127.0.0.9"}, "localhost", "127.0.0.9"},
		{Options{Profile: ProfileLAN, ServerHost: "10.1.1.1", ClientBind: "10.1.1.2"}, "10.1.1.1", "10.1.1.2"},
		{Options{Profile: ProfileLAN, ServerHost: "10.1.1.1"}, "10.1.1.1", ""},
	}
	for _, c := range ok {
		s, cave, err := ResolveProfile(c.o)
		if err != nil || s != c.server || cave != c.cave {
			t.Errorf("%+v: %q %q %v", c.o, s, cave, err)
		}
	}
	refused := map[string]Options{
		"the local profile's sockets are loopback-only, so its server host must be loopback, got '192.168.1.2'; use --profile lan --server-host 192.168.1.2": {ServerHost: "192.168.1.2"},
		"the local profile binds a loopback address, got --client-bind 192.168.1.30; use --profile lan":                                                      {ClientBind: "192.168.1.30"},
		"--client-bind must be an IPv4 address, got 'x'":                                                                                                     {ClientBind: "x"},
		"the lan profile needs --server-host <the server's IPv4 address>":                                                                                    {Profile: ProfileLAN},
		"unknown profile 'wan' (local or lan)":                                                                                                               {Profile: "wan", ServerHost: "10.1.1.1"},
		"--server-host 127.0.0.1 is loopback; that is the local profile":                                                                                     {Profile: ProfileLAN, ServerHost: "127.0.0.1"},
		"--client-bind 0.0.0.0 is not a host address":                                                                                                        {Profile: ProfileLAN, ServerHost: "192.168.1.20", ClientBind: "0.0.0.0"},
		"--server-host must be an IPv4 address, got 'myserver.lan'":                                                                                          {Profile: ProfileLAN, ServerHost: "myserver.lan"},
	}
	for want, o := range refused {
		if _, _, err := ResolveProfile(o); err == nil || err.Error() != want {
			t.Errorf("%+v: %v, want %q", o, err, want)
		}
	}
	for _, s := range []string{"", "B", "c", "9"} {
		if err := CheckDataSuffix(s); err != nil {
			t.Errorf("suffix %q: %v", s, err)
		}
	}
	for _, s := range []string{"BB", "-", "ä", " "} {
		if err := CheckDataSuffix(s); err == nil {
			t.Errorf("suffix %q accepted", s)
		}
	}
}

func TestLANAddress(t *testing.T) {
	// address -> "" (accepted) or the refusal's tail, as patch_client.py's lan_address answered on Python 3.12.7
	cases := map[string]string{
		"192.168.1.20": "", "10.0.0.2": "", "172.16.0.1": "", "100.64.1.2": "", "169.254.1.1": "", "192.0.0.8": "",
		"192.0.0.170": "", "192.0.0.171": "", "192.0.0.172": "", "0.1.2.3": "", "198.18.0.1": "", "192.0.2.1": "",
		"198.51.100.7": "", "203.0.113.9": "",
		"172.32.0.1": "", "100.128.0.1": "", "8.8.8.8": "", "192.0.0.9": "", // public
		"192.0.0.10": "", "198.20.0.1": "", "159.153.51.18": "",
		"255.255.255.255": "host", "240.0.0.1": "host", "0.0.0.0": "host", "224.0.0.251": "host",
		"239.255.255.255": "host",
		"127.0.0.1":       "loopback", "127.100.100.100": "loopback",
		"01.2.3.4": "ipv4", "1.2.3": "ipv4", "256.1.1.1": "ipv4", " 1.2.3.4": "ipv4", "1.2.3.4 ": "ipv4",
		"1.2.3.4.5": "ipv4", "1..2.3": "ipv4", "myserver.lan": "ipv4", "0.0.0.00": "ipv4", "1.2.3.0004": "ipv4",
		"١.2.3.4": "ipv4",
	}
	tails := map[string]string{
		"host":     "is not a host address",
		"loopback": "is loopback; that is the local profile",
		"ipv4":     "must be an IPv4 address, got",
	}
	for addr, want := range cases {
		got, err := LANAddress(addr, "--server-host")
		switch {
		case want == "" && (err != nil || got != addr):
			t.Errorf("%q: %q %v", addr, got, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), tails[want])):
			t.Errorf("%q: %v, want %q", addr, err, tails[want])
		}
	}
}

func TestIsPublic(t *testing.T) {
	for addr, want := range map[string]bool{"8.8.8.8": true, "172.32.0.1": true, "192.168.1.20": false, "10.0.0.2": false,
		"100.64.1.2": false, "169.254.1.1": false, "127.0.0.1": false, "0.0.0.0": false, "224.0.0.1": false, "x": false} {
		if IsPublic(addr) != want {
			t.Errorf("IsPublic(%s) = %v", addr, !want)
		}
	}
}

func TestPatchFileAndState(t *testing.T) {
	orig := syntheticExe()
	base := sha256Hex(orig)
	dir := filepath.Join(t.TempDir(), "run", "Dawngate", "game")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "Dawngate.exe")
	if err := os.WriteFile(exe, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	fo := FileOptions{BaseSHA256: base}
	if why, err := State(exe, modA, fo, nil); err != nil || why != "not patched (no .orig)" {
		t.Errorf("state before: %q %v", why, err)
	}
	// the default base is build 854's exe: a synthetic one is refused
	if _, err := PatchFile(exe, modA, FileOptions{}); err == nil || !strings.Contains(err.Error(), "not the untouched build-854 exe") {
		t.Errorf("another exe: %v", err)
	}
	res, err := PatchFile(exe, modA, fo)
	if err != nil || !res.SavedOrig || !res.Changed || !strings.HasPrefix(res.Summary, "redirector host -> 127.0.0.1") {
		t.Fatalf("first run: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(exe + OrigSuffix); !bytes.Equal(b, orig) {
		t.Error(".orig is not the untouched exe")
	}
	want, _, _ := Patch(orig, modA, Options{})
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, want) {
		t.Error("the exe is not Patch's output")
	}
	if why, err := State(exe, modA, fo, nil); err != nil || why != "" {
		t.Errorf("state after: %q %v", why, err)
	}
	res, err = PatchFile(exe, modA, fo)
	if err != nil || res.SavedOrig || res.Changed {
		t.Errorf("second run: %+v %v", res, err)
	}
	// another profile: patched from the .orig
	lan := FileOptions{Options: Options{Profile: ProfileLAN, ServerHost: "192.168.1.20"}, BaseSHA256: base}
	if why, _ := State(exe, modA, lan, nil); !strings.Contains(why, "redirector 127.0.0.1 (want 192.168.1.20)") {
		t.Errorf("state for lan: %q", why)
	}
	if res, err := PatchFile(exe, modA, lan); err != nil || !res.Changed || res.SavedOrig {
		t.Errorf("lan run: %+v %v", res, err)
	}
	// a host copy needs only the redirector and the data folders: a lane-b-like bind still counts for 127.0.0.1's
	// local profile there, not exactly
	hostLike, _, _ := Patch(orig, modA, Options{ServerHost: "127.0.0.1", ClientBind: "127.0.0.7"})
	if err := os.WriteFile(exe, hostLike, 0o644); err != nil {
		t.Fatal(err)
	}
	if why, err := State(exe, modA, fo, HostKeys); err != nil || why != "" {
		t.Errorf("host keys: %q %v", why, err)
	}
	if why, _ := State(exe, modA, fo, nil); !strings.Contains(why, "bind 127.0.0.7 (want 127.0.0.1)") {
		t.Errorf("exact: %q", why)
	}
	if err := os.WriteFile(exe, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if why, _ := State(exe, modA, fo, nil); why != "not patched" {
		t.Errorf("unpatched: %q", why)
	}
	// a changed .orig is refused: the patcher starts from it
	if err := os.WriteFile(exe+OrigSuffix, append(bytes.Clone(orig), 0), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PatchFile(exe, modA, fo); err == nil || !strings.Contains(err.Error(), "not the untouched") {
		t.Errorf("a foreign .orig: %v", err)
	}
	// option errors come before any write; the pristine extraction is never touched
	if _, err := PatchFile(exe, modA, FileOptions{Options: Options{ServerHost: "224.0.0.9"}, BaseSHA256: base}); err == nil {
		t.Error("a multicast server host accepted")
	}
	pristine := filepath.Join(t.TempDir(), "client", "ia_Dawngate", "Dawngate", "game", "Dawngate.exe")
	if _, err := PatchFile(pristine, modA, fo); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Errorf("client/ia_*: %v", err)
	}
}
