package exepatch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
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
// and "a4" are also test_a5.py's MAPTABLE_OUTPUT_SHA256, Q3B_OUTPUT_SHA256 and A4_OUTPUT_SHA256 ("sndbank" its
// SNDBANK_OUTPUT_SHA256).
var reference = []struct {
	name    string
	mod     []byte
	o       Options
	sha256  string
	summary string
	desc    string // redirector bind appdata documents raknet_loopback ca_sha256 ca_flag laa preload maptable rating heaplocks numfmt averages fade dgx draggrab profilereload
}{
	{"local", modA, Options{}, "c12c60ec198f1e4577e6e45484dcafe591c45ee853af13c5827802b127d458c5",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"local_laa_off", modA, Options{NoLAA: true}, "6d9ef7979d7e42fba3232dce457bf4319ac0a388a7535a9419c358fd6a32908f",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 off all on on on on on on on on on"},
	{"local_modb", modB, Options{Profile: ProfileLocal, ServerHost: "127.0.0.1"}, "ac237d27977b0497b84dc3f581028db12add8964ecd9949a07edb704679204e8",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 471fb943aa23c511 0 on all on on on on on on on on on"},
	{"local_localhost", modA, Options{ServerHost: "localhost"}, "6705f943038c372c88e4c28db9c4d921ef3edd97495948d6f5140953c42eeec0",
		"redirector host -> localhost; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"localhost 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lan", modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20"}, "e92e40f98ab8fafa7756ee401fc135127d2dde304fed0ca2988f234b97883e8a",
		"redirector host -> 192.168.1.20; UPnP off (-noupnp forced, discovery refused); sockets as shipped (wildcard binds; loopback patch left out); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"192.168.1.20 as-shipped DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lan_bind", modA, Options{Profile: ProfileLAN, ServerHost: "192.168.1.20", ClientBind: "192.168.1.30"}, "2294793190d3cc45c8aea5c345a780157059b44815f0e548d43c8f870c5b6027",
		"redirector host -> 192.168.1.20; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 192.168.1.30 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 192.168.1.30; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"192.168.1.20 192.168.1.30 DawngateData Dawngate 192.168.1.30 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lan_bind_same", modB, Options{Profile: ProfileLAN, ServerHost: "192.168.20.40", ClientBind: "192.168.20.40"}, "0a8aceda0b32ec13841b33a420381415d129eb913d5800153c3a57d928da6d28",
		"redirector host -> 192.168.20.40; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 192.168.20.40 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 192.168.20.40; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"192.168.20.40 192.168.20.40 DawngateData Dawngate 192.168.20.40 471fb943aa23c511 0 on all on on on on on on on on on"},
	{"lan_cgnat", modA, Options{Profile: ProfileLAN, ServerHost: "100.64.1.2"}, "1430713c6c613916e3f9a084516a63fdac32d63f9915f6c29727cc41b60780fd",
		"redirector host -> 100.64.1.2; UPnP off (-noupnp forced, discovery refused); sockets as shipped (wildcard binds; loopback patch left out); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)",
		"100.64.1.2 as-shipped DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lane_b", modA, Options{ServerHost: "127.0.0.2", DataSuffix: "B"}, "2bfa8f0d18f30bb1041304138664a73ff9d9f770d50fe6855b1b4f146c56a9e8",
		`redirector host -> 127.0.0.2; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 127.0.0.2 (bind cave at 0x011ab100, 6 sites), RakNet loopback 127.0.0.2; data folders %APPDATA%\DawngateDataB, Documents\DawngateB; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)`,
		"127.0.0.2 127.0.0.2 DawngateDataB DawngateB 127.0.0.2 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lane_e", modA, Options{ServerHost: "127.0.0.4", ClientBind: "127.0.4.2", DataSuffix: "E"}, "0cb411c93f598c7ac077eabb0df1a7909b0a4429ed43b406dc678ee0f56179dd",
		`redirector host -> 127.0.0.4; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 127.0.4.2 (bind cave at 0x011ab100, 6 sites), RakNet loopback 127.0.4.2; data folders %APPDATA%\DawngateDataE, Documents\DawngateE; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)`,
		"127.0.0.4 127.0.4.2 DawngateDataE DawngateE 127.0.4.2 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"lan_test_l", modA, Options{Profile: ProfileLAN, ServerHost: "10.0.0.5", ClientBind: "10.0.0.5", DataSuffix: "L"}, "8c0e765a9cf79d2662b646ea711930978f40c68a74c7243dee14c55810623e97",
		`redirector host -> 10.0.0.5; UPnP off (-noupnp forced, discovery refused); sockets narrowed to 10.0.0.5 (bind cave at 0x011ab100, 6 sites), RakNet's own-socket test to 10.0.0.5; data folders %APPDATA%\DawngateDataL, Documents\DawngateL; LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s); the middle-mouse drag grabs the ground when the preference OptionDragGrab is on (cave at 0x01c84010, the Options menu's 13-entry gameplay table at 0x01c84400); an executable section .dgx at 0x01c84000 (4096 bytes) for later patches; the Profile loads its player again when asked (its mode tabs)`,
		"10.0.0.5 10.0.0.5 DawngateDataL DawngateL 10.0.0.5 349d65e9ba1de7b0 0 on all on on on on on on on on on"},
	{"averages", modA, Options{NoFade: true, NoDgx: true, NoProfileReload: true}, "73feaac8ca6b72ce79cbfe07205530f94ee583389c2e5ae991f1075f72b05a20",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on as-shipped as-shipped as-shipped as-shipped"},
	{"numfmt", modA, Options{NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "1ec27470fb246a4d81820a96929a670f8cf27476fdca2f2417c5848d59e1255d",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"heaplocks", modA, Options{NoNumberFormat: true, NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "b7254e054218a1e7a44be37a9e32c714317413c01853f15c93fd888952e7eeaf",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"maptable", modA, Options{NoRating: true, NoHeapLocks: true, NoNumberFormat: true, NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "d81339e8e44cc4a6781c92db73488f20897e24ead406c7622a683dd9768a4543",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"q3b", modA, Options{NoPreload: true, NoMapTable: true, NoRating: true, NoHeapLocks: true, NoNumberFormat: true, NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "237512dc68b4135e220b410d92a6579cd602188079bd093b365871e38d214613",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"a4", modA, Options{NoLAA: true, NoPreload: true, NoMapTable: true, NoRating: true, NoHeapLocks: true, NoNumberFormat: true, NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "a15dd7ad91326f3114a054fd72ef20248a833a253e0c86a7d63fc13a0ecef027",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 off as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"sndbank", modA, Options{NoMapTable: true, NoRating: true, NoHeapLocks: true, NoNumberFormat: true, NoAverages: true, NoFade: true, NoDgx: true, NoProfileReload: true}, "a6ca0ae63b8815bf38aeb4575f61a951e4c92f5a159f0c1465f4a47cfe57267e",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"},
	{"fade", modA, Options{NoDgx: true, NoProfileReload: true}, "30c36547e0b64b30dfaf3aad85d2d5e0f61b90a5480fee70218cd5e0fe647cf1",
		"redirector host -> 127.0.0.1; UPnP off (-noupnp forced, discovery refused); sockets loopback-only (bind cave at 0x011ab100, 6 sites); LARGE_ADDRESS_AWARE (4 GB of address space); every player's shaper preloaded (player indexes 0-14); every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x011ab140); the profile box shows the rating the server sends (rating cave at 0x011ab170); the UI's memory heaps take their locks (a movie's socket reads on a thread); the UI's numbers print with 15 digits (8.2, not 8.199999999999999); the profile's averages at full precision (8.7, not 8.6); enemy creatures fade in and out by their team's sight and the fog (0.25 s)",
		"127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 349d65e9ba1de7b0 0 on all on on on on on on as-shipped as-shipped as-shipped"},
}

const origDescription = "gosredirector.ea.com as-shipped DawngateData Dawngate 127.0.0.1 e96f07ca645d1661 1 off as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped"

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
	if _, _, err := Patch(patched, modA, Options{}); err == nil || !strings.Contains(err.Error(), "unexpected PE header for .dgx (6 sections") {
		t.Errorf("an already patched exe: %v", err)
	}
	if _, _, err := Patch(patched, modA, Options{NoDgx: true}); err == nil || !strings.Contains(err.Error(), "OTG3 modulus at 0x01301d48") {
		t.Errorf("an already patched exe without the section: %v", err)
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
	if err != nil || descLine(d) != "gosredirector.ea.com as-shipped DawngateData Dawngate 127.0.0.1 "+sha256Hex(orig[synthOffset(otg3Modulus) : synthOffset(otg3Modulus)+128])[:16]+" 1 off as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped as-shipped" {
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
	if want := "127.0.0.1 127.0.0.1 DawngateData Dawngate 127.0.0.1 471fb943aa23c511 0 on all on on on on on on on on on"; descLine(d) != want {
		t.Errorf("describe %q, want %q", descLine(d), want)
	}
	// the changes, all where patch_client.py makes them
	vsizeAt := 0x80 + 4 + 20 + 0xE0 + 8
	tlsVSizeAt := vsizeAt + 3*40 // the fourth section, .tls
	opt := 0x80 + 4 + 20
	allowed := [][2]int{
		{0x80 + 4 + 2, 2}, {opt + 4, 4}, {opt + 56, 4}, {opt + 96 + 8*securityDir, 8}, {opt + 0xE0 + 5*40, 40}, // .dgx
		{synthOffset(otg3Modulus), 128}, {synthOffset(otg3Flags), 4}, {synthOffset(redirectorHost), 21},
		{synthOffset(noupnpJnz), 1}, {synthOffset(upnpDisc), len(upnpDiscNew)}, {0x80 + 4 + 18, 2}, {vsizeAt, 4},
		{synthOffset(loopbackCave), loopbackCaveLen}, {synthOffset(bindThunk), 6},
		{synthOffset(preloadCount), len(preloadCountNew)},
		{synthOffset(mapMiss), len(mapMissNew)}, {synthOffset(mapCave), len(mapCaveCode())}, {synthOffset(mapKey), mapKeyLen},
		{tlsVSizeAt, 4},
		{synthOffset(ratingSite1), len(ratingSite1New)}, {synthOffset(ratingSite2), len(ratingSite2New)},
		{synthOffset(ratingCave), len(ratingCaveCode())}, {synthOffset(ratingLabelVA), ratingLabelRoom},
		{synthOffset(numFmtFormat), len(numFmtFormatNew)}, {synthOffset(numFmtStep), len(numFmtStepNew)},
		{synthOffset(averagesBlock), len(averagesNew)}, {synthOffset(profileReloadSite), len(profileReloadNew)},
	}
	for _, va := range bindCalls {
		allowed = append(allowed, [2]int{synthOffset(va), 6})
	}
	for _, va := range heapLockSites {
		allowed = append(allowed, [2]int{synthOffset(va), len(heapLocksNew)})
	}
	for _, f := range fadeSites {
		allowed = append(allowed, [2]int{synthOffset(f.va), len(f.new)})
	}
	for _, s := range append(dragGrabSites(), dragOptionsSites()...) {
		allowed = append(allowed, [2]int{synthOffset(s.va), len(s.new)})
	}
	for _, r := range diffRanges(orig, got[:len(orig)]) {
		ok := false
		for _, a := range allowed {
			ok = ok || a[0] <= r[0] && r[1] <= a[0]+a[1]
		}
		if !ok {
			t.Errorf("unexpected change at %#x..%#x", r[0], r[1])
		}
	}
	// NUMFMT: 15 digits, and the step to more digits reads the 1e15 constant, whose bytes the patch leaves as they are
	if o := synthOffset(numFmtFormat); string(got[o:o+6]) != "%.15g\x00" {
		t.Errorf("number format %q", got[o:o+6])
	}
	if o := synthOffset(numFmtStep); !bytes.Equal(got[o:o+2], []byte{0xDD, 0x05}) || binary.LittleEndian.Uint32(got[o+2:]) != numFmt1e15 {
		t.Errorf("number step: % x", got[o:o+6])
	}
	if o := synthOffset(numFmt1e15); math.Float64frombits(binary.LittleEndian.Uint64(got[o:])) != 1e15 {
		t.Errorf("the 1e15 constant: % x", got[o:o+8])
	}
	// AVGPREC: the block keeps its size, and the x87's control word is kept and put back
	if len(averagesNew) != len(averagesOrig) {
		t.Errorf("the averages' block: %d bytes, shipped %d", len(averagesNew), len(averagesOrig))
	}
	if o := synthOffset(averagesBlock); !bytes.Equal(got[o:o+len(averagesNew)], averagesNew) {
		t.Errorf("the averages' block: % x", got[o:o+len(averagesNew)])
	}
	if v := binary.LittleEndian.Uint32(got[vsizeAt:]); v != 0xDAA200 {
		t.Errorf(".text VirtualSize %#x", v)
	}
	if v := binary.LittleEndian.Uint32(got[tlsVSizeAt:]); v != 0x200 {
		t.Errorf(".tls VirtualSize %#x", v)
	}
	// the not-found exit jumps to the map-name cave, which ends in a jmp back to the found path's key check, and the
	// key's text is the lobby table's
	if o := synthOffset(mapMiss); got[o] != 0xE9 || mapMiss+5+binary.LittleEndian.Uint32(got[o+1:]) != mapCave {
		t.Errorf("map-name exit: % x", got[o:o+5])
	}
	c := got[synthOffset(mapCave) : synthOffset(mapCave)+len(mapCaveCode())]
	if mapCave+uint32(len(c))+binary.LittleEndian.Uint32(c[len(c)-4:]) != mapResume {
		t.Errorf("map-name cave: % x", c)
	}
	if k := got[synthOffset(mapKey) : synthOffset(mapKey)+62]; utf16le(k[:60]) != "^FE_CustomGameLobby!0x00000000" {
		t.Errorf("map key %q", utf16le(k))
	}
	// RATINGSHOW1: the first site calls the cave's text routine, the second its mov ebp,7 entry; the cave ends in ret
	// and calls the user lookup and the extended-data read; the label is L"Rating "
	if o := synthOffset(ratingSite1); got[o] != 0xE8 || ratingSite1+5+binary.LittleEndian.Uint32(got[o+1:]) != ratingCave+5 {
		t.Errorf("rating site 1: % x", got[o:o+16])
	}
	if o := synthOffset(ratingSite2); got[o] != 0xE8 || ratingSite2+5+binary.LittleEndian.Uint32(got[o+1:]) != ratingCave {
		t.Errorf("rating site 2: % x", got[o:o+5])
	}
	rc := got[synthOffset(ratingCave) : synthOffset(ratingCave)+len(ratingCaveCode())]
	if len(rc) != 129 || rc[len(rc)-1] != 0xC3 || ratingCave+uint32(len(rc)) > 0x011AB200 {
		t.Errorf("rating cave (%d bytes): % x", len(rc), rc)
	}
	calls := map[uint32]bool{}
	for i := 0; i+5 <= len(rc); i++ {
		if rc[i] == 0xE8 {
			calls[ratingCave+uint32(i)+5+binary.LittleEndian.Uint32(rc[i+1:])] = true
		}
	}
	if !calls[findUser] || !calls[getExtValue] {
		t.Errorf("rating cave calls %v", calls)
	}
	if l := got[synthOffset(ratingLabelVA) : synthOffset(ratingLabelVA)+14]; utf16le(l) != "Rating " {
		t.Errorf("rating label %q", utf16le(l))
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
	if !bytes.Equal(lan[synthOffset(loopbackCave):synthOffset(mapCave)], make([]byte, mapCave-loopbackCave)) {
		t.Error("lan without a client bind wrote the cave")
	}
	if d, _ := Describe(lan); d["bind"] != "as-shipped" || d["maptable"] != "on" {
		t.Errorf("lan: %v", d)
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
	if _, _, err := Patch(patched, modA, Options{}); err == nil || !strings.Contains(err.Error(), "unexpected PE header for .dgx") {
		t.Errorf("an already patched exe: %v", err)
	}
	if _, _, err := Patch(patched, modA, Options{NoDgx: true}); err == nil || !strings.Contains(err.Error(), "OTG3 modulus") {
		t.Errorf("an already patched exe without the section: %v", err)
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
