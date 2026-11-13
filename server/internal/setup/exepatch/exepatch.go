// Package exepatch patches a copy of Dawngate.exe (build 1.0.903.854) so it talks to our server: the Go port of
// re/scripts/patch_client.py, which stays the reference (ADR-3). Patch makes
// byte for byte what patch_client.py's patch_bytes makes from the untouched exe for the same inputs (the tests pin
// the SHA-256 of each profile's output); Describe is its describe (--show), PatchFile its main() with setup_all.py's
// check that the untouched exe is the build's.
//
// The patches (each checks the original bytes first; details and evidence in patch_client.py's
// docstring):
//
//   - ca, ca-flag: our CA's 1024-bit modulus over the built-in "OTG3 Certificate Authority" (exponent 3), and bit 0 of
//     its flags cleared (else only *.ea.com subjects pass).
//   - redirector: "gosredirector.ea.com" becomes our server's address.
//   - noupnp, upnp-disc: no UPnP on the LAN (NetConnStartup always takes its "-noupnp" path; ProtoUPnP's discovery
//     refuses).
//   - loopback: a 40-byte code cave in .text's file-alignment slack narrows bind()'s INADDR_ANY to one address; the
//     five call sites and the thunk go through it (local profile, or the lan profile with a client bind address).
//   - raknet-loopback (a loopback bind other than 127.0.0.1) and raknet-lan (a LAN bind address): RakNet's start-up
//     send test goes to the bound address.
//   - data-folders: only with a DataSuffix (the test lanes): %APPDATA%\DawngateData<X>, Documents\Dawngate<X>.
//   - laa: IMAGE_FILE_LARGE_ADDRESS_AWARE (4 GB of address space), on unless NoLAA.
//   - preload: the client preloads the shaper (and with it the sound banks) of every player index 0-14, not only of
//     the first GameSimulator_CountPlayers() indexes, which left out a second team seated at 5-9 in small games; on
//     unless NoPreload.
//   - maptable: the map-name method (the custom lobby's map line, the game browser's Map column) knows one map; its
//     not-found exit jumps to a 45-byte cave after the bind cave, which names any other LevelId by the line of that id
//     in the lobby's table FE_CustomGameLobby (the key in .tls's slack; .text's and .tls's VirtualSize raised to map
//     the slack); the setup writes a line for every installed map (localepatch). On unless NoMapTable.
//   - rating: the main menu's profile box shows EA's rating field (MainMenuUI.gfx's m_glickoText), which both of its
//     C++ callers fill with an empty string; each now calls a 129-byte cave after the map-name cave that reads the
//     local user's DMAP[0x00070037] and passes L"Rating <n>" when it is above 0 (the text built in .tls's slack after
//     the map key), else the empty string as shipped. The server sends the key only for a rating players are to see
//     (blaze.ShownRating). On unless NoRating.
//   - heaplocks: every Scaleform heap takes its lock. A movie's heap is created thread-unsafe, and flash.net.Socket's
//     thread allocates from it, so a movie's socket corrupts the heap within a second without this (the UI channel,
//     docs/development/ui-channel.md). On unless NoHeapLocks.
//   - numfmt: the UI prints a number with 15 significant digits. Scaleform's AS3 number-to-string prints with 16, which
//     shows the double nearest 8.2 as 8.199999999999999 (the profile's averages); 15 digits print every such value as it
//     was meant. Its step up to more digits, which keeps large whole
//     numbers exact, starts at 1e15 instead of 1e16, so 16-digit whole numbers stay as they were. On unless NoNumberFormat.
//   - averages: the profile's averages are real divisions at full precision. The shipped total * (1 / games) ran on an x87
//     at 24 bits, so an average that is an exact tenth could land just under it and show a tenth low (87 over 10 games as
//     8.6). The block is rewritten in place: no cave. On unless NoAverages.
//   - fade: an enemy creature is drawn while its team sees it (mVisibleToTeam) or the client's own fog texture at its
//     feet is lit, and fades in and out over 0.25 s, instead of a pale ghost that turns solid at once; so a creature the
//     server sends a little early stays hidden until it is in sight, and shows without waiting for the server.
//     Observers as shipped. In place: a function only the fog exit called and an assert block (fade.go). On unless
//     NoFade.
//   - dgx: room for code and data that no longer fit .text's slack: a sixth section .dgx (code, execute, read) at
//     0x01C84000, the untouched SizeOfImage, its raw data appended after the file's end; every later piece has a fixed
//     place in it (dgx.go's dgxLayout, append-only). On unless NoDgx.
//   - draggrab: the middle-mouse drag scroll grabs the ground (the point under the cursor stays under it) instead of
//     acting as a joystick, while the game's preference OptionDragGrab is on; off or missing, the joystick runs as
//     shipped. A hook into .dgx's draggrab piece and two observer sites, state in .tls's slack (draggrab.go). On
//     unless NoDragGrab or NoDgx.
//   - profilereload: the Profile screen loads its player again when asked for the same one. As shipped it loads only
//     for another player (ProfileUI_SetShownPlayer 0x004EE200 skips the same id), so its own search native for the
//     shown name reloads nothing; the jnz past the id becomes a jmp. The Profile's mode tabs (the server answers its
//     stat requests for the chosen game type) reload the screen that way. On unless NoProfileReload.
//
// Profiles: "local" (the default; the server on a loopback address, every wildcard bind narrowed to it) and "lan" (a
// server elsewhere on our own network: ServerHost required, the binds as shipped unless ClientBind names this
// machine's LAN address). The match host's copy uses the local profile; State with HostKeys checks it as
// setup_all.py does (redirector and data folders only).
package exepatch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// ImageBase is the exe's preferred (and, without relocations, only) load address.
const ImageBase = 0x00400000

// Build854SHA256 is the untouched Dawngate.exe of build 1.0.903.854 (docs/file_manifest.csv; a test checks they agree).
const Build854SHA256 = "6f7c8e8243bd7a820699535cd84b64017d5a843db12a51be7c9a2e759dde0e2c"

// Addresses and original bytes: patch_client.py's constants (its docstring has the evidence).
const (
	otg3Entry         = 0x014647A0 // ProtoSSLCACertT: Country[32] State[32] City[32] Org[32] Unit[256] Common[64] ...
	otg3Flags         = 0x01464960 // u32 flags; bit 0 = only *.ea.com / *.easports.com subjects
	otg3KeyInfo       = 0x01464964 // u32 modulus size, u32 modulus pointer, u32 exponent size, exponent bytes
	otg3Modulus       = 0x01301D48
	redirectorHost    = 0x012E2AD8
	noupnpStrstr      = 0x00CDBF3D
	noupnpJnz         = 0x00CDBF53
	upnpDisc          = 0x00CEB0F1
	bindIAT           = 0x011AC9CC // WSOCK32!bind, imported by ordinal 2
	bindThunk         = 0x00FD6660 // jmp [bind], called by DirtySDK's SocketBind (0x00CE7EF0)
	textVSize         = 0x00DAA0FB // .text VirtualSize; SizeOfRawData is 0x00DAA200
	loopbackCave      = 0x011AB100 // in .text's file-alignment slack (zeros up to 0x011AB200)
	appdirDefault     = 0x011FA4F8 // L"DawngateData": %APPDATA%\DawngateData
	appdirRoom        = 0x20       // next: L"HKEY_LOCAL_MACHINE\...\PlayerDir" at 0x011FA518
	playerdirDefault  = 0x011FA598 // L"Dawngate": Documents\Dawngate
	playerdirRoom     = 0x14       // next: L"noAsserts" at 0x011FA5AC
	raknetLoopback    = 0x011FDBB8 // RakNet's IPV4_LOOPBACK "127.0.0.1" (IPV6_LOOPBACK "::1" follows at 0x011FDBC4)
	raknetLoopRoom    = 12
	raknetLoopbackPtr = 0x013F6AC4 // const char *IPV4_LOOPBACK
	lanLoopbackString = 0x011AB130 // where the lan profile's bind address goes (16 bytes after the 40-byte cave)
	lanLoopbackRoom   = 16
	charBuild         = 0x0103     // IMAGE_FILE_HEADER.Characteristics of build 854
	charLAA           = 0x0020     // IMAGE_FILE_LARGE_ADDRESS_AWARE
	preloadCount      = 0x0066BD1F // GameClient_PreloadPlayerCharacters' call to GameSimulator_CountPlayers (its loop bound)
	heapLocksPT       = 0x00D294E1 // a heap constructor's UseLocks (heap +0x64)
	heapLocksMH       = 0x00D727B5 // the other kind of heap's
	numFmtFormat      = 0x01329B88 // "%.16g": Scaleform's number-to-string (0x00EA69F0) copies it to its stack, its only reader
	numFmtStep        = 0x00EA6B3C // its fld of the value from which it raises the digits (1e16)
	numFmt1e15        = 0x0135D140 // a 1e15 double in .rdata, which the step reads instead
	averagesBlock     = 0x00AA63C8 // StatsManager_OnShaperHistoryStatsResult, with the games in st(0): the averages
	profileReloadSite = 0x004EE25B // ProfileUI_SetShownPlayer's jnz past the same player's id (its low dword)

	// The map-name method (0x00684380): its loop over the one-entry map table, the not-found exit, the found path
	// and where the found path checks and looks up its key.
	mapLoop   = 0x006843C1
	mapMiss   = 0x006843D2
	mapFound  = 0x006843E7
	mapResume = 0x006843EF
	mapCave   = 0x011AB140 // in .text's slack after the bind cave and the LAN string
	tlsVA     = 0x01C00000 // .tls: the TLS template is 0x01C00000-0x01C00038
	tlsVSize  = 0x39
	tlsRaw    = 0x200
	mapKey    = 0x01C00040 // the key the cave writes, in .tls's slack (writable)
	mapKeyLen = 0x40
	// mapKeyPrefix is the key before the LevelId's 8 hex digits: the custom lobby's own table.
	mapKeyPrefix = "^FE_CustomGameLobby!0x"
	// The profile box's rating text (patch_client.py's RATING_*): the two SetExtendedData callers' rating value, what
	// the cave calls, and where its text goes.
	ratingSite1     = 0x004DE1C9 // MainMenuUI_OnUserExtendedDataChanged: the value's type 7 and pointer stores
	ratingSite2     = 0x004E0661 // MainMenuUI_BindExtFunctions: mov ebp,7 (the caller stores ebp and ebx)
	emptyWString    = 0x0199D2A0 // the shared empty wide string both callers pass
	platformPtr     = 0x01BB6154 // SporeOnline_GetPlatform's [0x01BB6154]
	findUser        = 0x00C8AFD0 // UserManager_FindUserByBlazeId
	getExtValue     = 0x00C8B560 // BlazeUser_GetExtendedDataValue
	ratingKey       = 0x37       // DMAP[0x00070037]
	ratingCave      = 0x011AB170 // in .text's slack after the map-name cave
	ratingLabel     = "Rating "  // before the number
	ratingLabelVA   = 0x01C00080 // in .tls's slack after the map key
	ratingLabelRoom = 0x10
	ratingText      = 0x01C00090 // the text, built backwards from its NUL at ratingTextEnd
	ratingTextRoom  = 0x30
	ratingTextEnd   = ratingText + ratingTextRoom - 2
)

var (
	otg3ModulusHead    = mustHex("9275a15b080240b8")
	redirectorOrig     = []byte("gosredirector.ea.com\x00")
	noupnpStrstrOrig   = mustHex("6848173001" + "57" + "8935e022bf01" + "e8f00b3000" + "83c408" + "85c0")
	noupnpJnzOrig      = mustHex("7520" + "e8e6f70000" + "894610")
	noupnpJmp          = []byte{0xEB} // jmp +0x20: always take the "-noupnp" path
	upnpDiscOrig       = mustHex("e86af3ffff" + "c786a80a000001000000" + "33c0" + "5e")
	upnpDiscNew        = mustHex("83c8ff" + "eb0c") // or eax,-1; jmp 0x00CEB102 (pop esi; ...; ret)
	bindCalls          = []uint32{0x006D9344, 0x006D94B7, 0x006D9551, 0x0084923B, 0x0107E04A}
	callBind           = append([]byte{0xFF, 0x15}, le32(bindIAT)...)
	jmpBind            = append([]byte{0xFF, 0x25}, le32(bindIAT)...)
	raknetLoopbackOrig = []byte("127.0.0.1\x00\x00\x00::1\x00")
	heapLocksOrig      = mustHex("f6d280e201") // not dl; and dl,1: UseLocks = !(flags & Heap_ThreadUnsafe)
	heapLocksNew       = mustHex("b201909090") // mov dl,1; nop; nop; nop
	numFmtFormatOrig   = []byte("%.16g\x00")
	numFmtFormatNew    = []byte("%.15g\x00")
	numFmtStepOrig     = mustHex("dd05d87b3001")                                                // fld qword ptr [0x01307BD8]: 1e16
	numFmtStepNew      = mustHex("dd0540d13501")                                                // fld qword ptr [0x0135D140]: 1e15
	numFmt1e15Value    = mustHex("00003426f56b0c43")                                            // 1e15, little endian
	preloadCountOrig   = mustHex("e8ec532800")                                                  // call 0x008F1110
	preloadCountNew    = mustHex("b80f000000")                                                  // mov eax,15: every player index 0-14 (the caller's add esp,0x10 stays)
	mapLoopOrig        = mustHex("33c9" + "3904cd7c3a3f01" + "741b" + "41" + "83f901" + "72f1") // the loop, kNumPlayableMaps 1
	mapMissOrig        = mustHex("5f" + "8bc3" + "5b" + "8b4c2420")                             // pop edi; mov eax,ebx; pop ebx; ...
	mapFoundOrig       = mustHex("56" + "8b34cd783a3f01")                                       // push esi; mov esi,[ecx*8+0x013F3A78]
	mapResumeOrig      = mustHex("3bf7" + "740d")                                               // cmp esi,edi; jz ...
	mapMissNew         = append([]byte{0xE9}, le32(mapCave-(mapMiss+5))...)                     // jmp mapCave
	ratingSite1Orig    = mustHex("c744245807000000" + "c744245ca0d29901")                       // mov [esp+0x58],7; mov [esp+0x5c],empty
	ratingSite2Orig    = mustHex("bd07000000")                                                  // mov ebp,7
	ratingNext2        = mustHex("8954244c" + "c744245003000000" + "89442454" + "89542464" + "896c2468" + "895c246c")
	// call text; mov [esp+0x5c],ebx; push 7; pop ecx; mov [esp+0x58],ecx
	ratingSite1New = append(append([]byte{0xE8}, le32(ratingCave+5-(ratingSite1+5))...), mustHex("895c245c"+"6a07"+"59"+"894c2458")...)
	ratingSite2New = append([]byte{0xE8}, le32(ratingCave-(ratingSite2+5))...) // call mov ebp,7 + text
	dataFolders    = []struct {
		va   uint32
		room int
		name string
	}{{appdirDefault, appdirRoom, "DawngateData"}, {playerdirDefault, playerdirRoom, "Dawngate"}}
)

// loopbackCaveLen is the cave's length (bindCaveCode's).
const loopbackCaveLen = 40

// bindCaveCode is the cave: narrow an IPv4 INADDR_ANY in bind()'s sockaddr to address, then jmp [bind].
func bindCaveCode(address ipv4) []byte {
	code := mustHex("8b442408" + // mov eax,[esp+8]          ; name
		"85c0" + //                     test eax,eax
		"741a" + //                     jz go
		"837c240c08" + //               cmp dword [esp+0xc],8    ; namelen
		"7c13" + //                     jl go
		"66833802" + //                 cmp word [eax],2         ; AF_INET
		"750d" + //                     jne go
		"83780400" + //                 cmp dword [eax+4],0      ; INADDR_ANY
		"7507" + //                     jne go
		"c74004") //                    mov dword [eax+4],<address>  (network order)
	code = append(code, address[:]...)
	return append(code, jmpBind...) // go: jmp [bind]  (eax and flags are free)
}

// mapCaveCode is the map-name cave, reached from the not-found exit with eax = the game's LevelId: it writes the id as
// 8 hex digits into the key L"^FE_CustomGameLobby!0x????????" (mapKey) and goes on as the found path does with that
// key (patch_client.py's map_cave_code).
func mapCaveCode() []byte {
	hexAt := uint32(mapKey + 2*len(mapKeyPrefix))
	code := mustHex("b908000000" + // mov ecx,8
		"8bd0" + //     l: mov edx,eax
		"83e20f" + //   and edx,0xf
		"80c230" + //   add dl,'0'
		"80fa39" + //   cmp dl,'9'
		"7603" + //     jbe s
		"80c207" + //   add dl,7            ; 'A'..'F'
		"6689144d") //  s: mov [ecx*2+hex-2],dx
	code = append(code, le32(hexAt-2)...)
	code = append(code, mustHex("c1e804"+ // shr eax,4
		"e2e3"+ //                          loop l
		"56"+ //                            push esi            ; as the found path does
		"be")...) //                        mov esi,mapKey
	code = append(code, le32(mapKey)...)
	code = append(code, 0xE9)
	return append(code, le32(mapResume-(mapCave+uint32(len(code))+4))...) // jmp mapResume
}

// ratingCaveCode is the rating cave (patch_client.py's rating_cave_code). Entry ratingCave (the second site): mov
// ebp,7, then the text routine at ratingCave+5 (the first site): ebx = L"Rating <n>" when the local user's
// DMAP[0x00070037] is above 0, else the empty string; every other register is kept (pushad, ebx written into its slot).
func ratingCaveCode() []byte {
	var c []byte
	emit := func(h string, tail ...[]byte) {
		c = append(c, mustHex(h)...)
		for _, t := range tail {
			c = append(c, t...)
		}
	}
	call := func(to uint32) { c = append(c, le32(to-(ratingCave+uint32(len(c))+4))...) }
	hole := func() int { c = append(c, 0); return len(c) - 1 }
	emit("bd07000000")                     // mov ebp,7        (the second site's instruction)
	emit("60")                             // text: pushad
	emit("6a00" + "6a00")                  // push 0; push 0   ; [esp] = the int64 value
	emit("bb", le32(emptyWString))         // mov ebx,empty
	emit("8b35", le32(platformPtr))        // mov esi,[platform]
	emit("8b06" + "8bce" + "ff90e4000000") // mov eax,[esi]; mov ecx,esi; call [eax+0xE4] ; the local Blaze id
	emit("8b4e18" + "8b8998040000")        // mov ecx,[esi+0x18]; mov ecx,[ecx+0x498]    ; the user manager
	emit("85c9" + "74")                    // test ecx,ecx; jz done
	j1 := hole()
	emit("52" + "50" + "e8") // push edx; push eax; call FindUserByBlazeId
	call(findUser)
	emit("85c0" + "74") // test eax,eax; jz done
	j2 := hole()
	emit("8bc8" + "54")           // mov ecx,eax; push esp (&value)
	emit("6a", []byte{ratingKey}) // push 0x37
	emit("6a07" + "e8")           // push 7; call GetExtendedDataValue
	call(getExtValue)
	emit("84c0" + "74") // test al,al; jz done
	j3 := hole()
	emit("8b0424" + "85c0" + "7e") // mov eax,[esp]; test eax,eax; jle done
	j4 := hole()
	emit("bb", le32(ratingTextEnd)) // mov ebx,the NUL
	emit("6a0a" + "59")             // push 10; pop ecx
	d := len(c)                     // d: xor edx,edx; div ecx; add edx,'0'; ebx-=2; [ebx]=dx; jnz d
	emit("33d2" + "f7f1" + "83c230" + "4b4b" + "668913" + "85c0" + "75")
	c = append(c, byte(d-(len(c)+1)))
	emit("6a", []byte{byte(len(ratingLabel))}, []byte{0x59}) // push len; pop ecx
	p := len(c)                                              // p: ebx-=2; mov ax,[ecx*2+label-2]; mov [ebx],ax; loop p
	emit("4b4b"+"668b044d", le32(ratingLabelVA-2))
	emit("668903" + "e2")
	c = append(c, byte(p-(len(c)+1)))
	done := len(c)
	for _, j := range []int{j1, j2, j3, j4} {
		c[j] = byte(done - (j + 1))
	}
	emit("83c408" + "895c2410" + "61" + "c3") // done: add esp,8; mov [esp+0x10],ebx; popad; ret
	return c
}

// mapKeyText is the key as the patch writes it (the cave fills in the digits).
const mapKeyText = mapKeyPrefix + "00000000"

// heapLockSites are the heap constructors' UseLocks computations.
var heapLockSites = []uint32{heapLocksPT, heapLocksMH}

// The averages' block as shipped: 1 / games, the seconds' integer division and the list's head, then each total times
// the reciprocal.
var averagesOrig = mustHex("d9e8" + "8b842490000000" + "def1" + "33d2" + "f7f1" + "8b5c2414" + "83c378" + "dd442468" + "d8c9" +
	"89842490000000" + "8b03" + "dd5c2468" + "8bf0" + "dd442470" + "89742410" + "d8c9" + "dd5c2470" + "dd442478" + "d8c9" +
	"dd5c2478" + "dd842480000000" + "d8c9" + "dd9c2480000000" + "dc8c2488000000" + "dd9c2488000000")

// averagesNew is the same block with real divisions on an x87 at 53 bits, its padding the rest of the 97 bytes.
// The Profile's same-player check (ProfileUI_SetShownPlayer): jnz past it as shipped (the low dword differs: load), a
// jmp since PROFILE2 (always load).
var (
	profileReloadOrig = mustHex("7508")
	profileReloadNew  = mustHex("eb08")
)

var averagesNew = append(mustHex(
	"50"+ // push eax: a slot for the x87's control word
		"d93c24"+ // fnstcw word ptr [esp]: the game's
		"687f020000"+ // push 0x27f: 53-bit precision, round to nearest, exceptions masked
		"d92c24"+ // fldcw word ptr [esp]
		"58"+ // pop eax
		"8d54246c"+ // lea edx,[esp+0x6c]: the first total ([esp+0x68] before the push)
		"6a05"+ // push 5: kills, deaths, assists, CS, vim, five doubles in a row
		"58"+ // pop eax
		"dd02"+ // loop: fld qword ptr [edx]; st(1) is the games
		"d8f1"+ // fdiv st(0),st(1)
		"dd1a"+ // fstp qword ptr [edx]
		"83c208"+ // add edx,8
		"48"+ // dec eax
		"75f4"+ // jne loop
		"ddd8"+ // fstp st(0): the games
		"d92c24"+ // fldcw word ptr [esp]: the game's again
		"58"+ // pop eax
		"8b842490000000"+ // from here as shipped: mov eax,[esp+0x90], the seconds
		"33d2"+ // xor edx,edx
		"f7f1"+ // div ecx
		"89842490000000"+ // mov [esp+0x90],eax
		"8b5c2414"+ // mov ebx,[esp+0x14]
		"83c378"+ // add ebx,0x78
		"8b03"+ // mov eax,[ebx]
		"8bf0"+ // mov esi,eax
		"89742410"), // mov [esp+0x10],esi
	bytes.Repeat([]byte{0x90}, 26)...)

// Profiles.
const (
	ProfileLocal = "local"
	ProfileLAN   = "lan"
)

// Options are patch_client.py's arguments.
type Options struct {
	Profile    string // ProfileLocal (when empty) or ProfileLAN
	ServerHost string // the redirector host: local, a loopback IPv4 address or "localhost" (default 127.0.0.1); lan, required: an IPv4 address of our own network
	ClientBind string // local: a loopback address to bind (default the server's); lan: this machine's LAN address (default: binds as shipped)
	DataSuffix string // one letter or digit for the per-user folders (test lanes only); "" leaves them
	NoLAA      bool   // leave out LARGE_ADDRESS_AWARE (patch_client.py --laa off)
	NoPreload  bool   // leave out the preload of every player index's shaper (patch_client.py --preload off)
	NoMapTable bool   // leave out the map-name cave (patch_client.py --maptable off)
	NoRating   bool   // leave out the profile box's rating text (patch_client.py --rating off)
	// NoHeapLocks leaves out the UI heaps' locks (patch_client.py --heap-locks off)
	NoHeapLocks bool
	// NoNumberFormat leaves out the UI numbers' 15 digits (patch_client.py --number-format off)
	NoNumberFormat bool
	// NoAverages leaves out the profile averages' full precision (patch_client.py --averages off)
	NoAverages bool
	// NoFade leaves out the fade at the fog's edge: enemy creatures drawn as shipped
	NoFade bool
	// NoDgx leaves out the section .dgx (patch_client.py --dgx off), and with it the drag grab
	NoDgx bool
	// NoDragGrab leaves out the grab-style drag scroll (patch_client.py --drag-grab off)
	NoDragGrab bool
	// NoProfileReload leaves out the Profile's reload of the same player (patch_client.py --profile-reload off)
	NoProfileReload bool
}

// Error is a refusal: the exe isn't the expected build, or the options don't fit the profile (patch_client.py's
// PatchError).
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// ResolveProfile checks the options and returns the redirector host and the address the cave narrows the binds to
// ("" = binds as shipped).
func ResolveProfile(o Options) (server, caveAddress string, err error) {
	profile := o.Profile
	if profile == "" {
		profile = ProfileLocal
	}
	server = o.ServerHost
	switch profile {
	case ProfileLocal:
		if server == "" {
			server = "127.0.0.1"
		}
		ip, perr := parseIPv4(server)
		loopback := perr == nil && isLoopback(ip)
		if perr != nil {
			loopback = strings.ToLower(server) == "localhost"
		}
		if !loopback {
			return "", "", errorf("the local profile's sockets are loopback-only, so its server host must be loopback, "+
				"got %s; use --profile lan --server-host %s", pyRepr(server), server)
		}
		bind := "127.0.0.1" // the sockets bind the server's loopback address (a lane's copy binds 127.0.0.N)
		if perr == nil {
			bind = ip.String()
		}
		if o.ClientBind != "" {
			bip, err := parseIPv4(o.ClientBind)
			if err != nil {
				return "", "", errorf("--client-bind must be an IPv4 address, got %s", pyRepr(o.ClientBind))
			}
			if !isLoopback(bip) {
				return "", "", errorf("the local profile binds a loopback address, got --client-bind %s; use --profile lan",
					o.ClientBind)
			}
			bind = bip.String()
		}
		return server, bind, nil
	case ProfileLAN:
		if server == "" {
			return "", "", errorf("the lan profile needs --server-host <the server's IPv4 address>")
		}
		if server, err = LANAddress(server, "--server-host"); err != nil {
			return "", "", err
		}
		if o.ClientBind != "" {
			if caveAddress, err = LANAddress(o.ClientBind, "--client-bind"); err != nil {
				return "", "", err
			}
		}
		return server, caveAddress, nil
	}
	return "", "", errorf("unknown profile %s (local or lan)", pyRepr(profile))
}

// LANAddress checks the IPv4 address of a server on another machine: not loopback, wildcard, multicast or reserved.
// A public address is accepted (the operator's own risk, docs/hosting.md); IsPublic tells them apart.
// patch_client.py's lan_address, with Python 3.12's ipaddress rules. It returns the address as written.
func LANAddress(text, what string) (string, error) {
	ip, err := parseIPv4(text)
	if err != nil {
		return "", errorf("%s must be an IPv4 address, got %s", what, pyRepr(text))
	}
	switch {
	case isLoopback(ip):
		return "", errorf("%s %s is loopback; that is the local profile", what, ip)
	case isUnspecified(ip) || isMulticast(ip) || isReserved(ip):
		return "", errorf("%s %s is not a host address", what, ip)
	}
	return ip.String(), nil
}

// IsPublic says whether text is a public (internet) IPv4 address, by Python 3.12's is_global (the launchers
// show their risk notices for it).
func IsPublic(text string) bool {
	ip, err := parseIPv4(text)
	return err == nil && !isLoopback(ip) && !isUnspecified(ip) && !isMulticast(ip) && !isReserved(ip) && isGlobal(ip)
}

// CheckDataSuffix: one ASCII letter or digit (the Documents name has room for one more character).
func CheckDataSuffix(suffix string) error {
	if suffix == "" {
		return nil
	}
	if len(suffix) != 1 || !isASCIIAlnum(suffix[0]) {
		return errorf("--data-suffix must be one letter or digit, got %s", pyRepr(suffix))
	}
	return nil
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Patch patches a copy of the untouched exe's bytes (orig is not changed) with our CA's 128-byte modulus; it returns
// the patched bytes and patch_client.py's summary line.
func Patch(orig, modulus []byte, o Options) ([]byte, string, error) {
	server, caveAddress, err := ResolveProfile(o)
	if err != nil {
		return nil, "", err
	}
	if err := CheckDataSuffix(o.DataSuffix); err != nil {
		return nil, "", err
	}
	data := bytes.Clone(orig)
	if !o.NoDgx { // first, so the patches can address .dgx
		if data, err = addDgxSection(orig); err != nil {
			return nil, "", err
		}
		if _, err := dgxPlace(data, "header", dgxMagic); err != nil {
			return nil, "", err
		}
	}
	img, err := parsePE(data)
	if err != nil {
		return nil, "", err
	}
	at := func(va uint32, n int) ([]byte, error) { return img.bytesAt(va, n) }
	expect := func(va uint32, want []byte, what string) error {
		got, err := at(va, len(want))
		if err != nil {
			return errorf("unexpected bytes for %s at 0x%08x: %v (wrong build?)", what, va, err)
		}
		if !bytes.Equal(got, want) {
			return errorf("unexpected bytes for %s at 0x%08x: %x (wrong build?)", what, va, got)
		}
		return nil
	}
	write := func(va uint32, b []byte) {
		dst, _ := at(va, len(b)) // every write follows an expect of the same range (or a larger one)
		copy(dst, b)
	}

	// --- CA modulus
	if len(modulus) != 128 {
		return nil, "", errorf("expected a 128-byte (1024-bit) CA modulus, got %d", len(modulus))
	}
	if err := expect(otg3Entry+0x180, []byte("OTG3 Certificate Authority\x00"), "OTG3 common name"); err != nil {
		return nil, "", err
	}
	keyInfo := append(append(append(le32(0x80), le32(otg3Modulus)...), le32(1)...), 0x03)
	if err := expect(otg3KeyInfo, keyInfo, "OTG3 key info (size, pointer, exponent 3)"); err != nil {
		return nil, "", err
	}
	if err := expect(otg3Modulus, otg3ModulusHead, "OTG3 modulus"); err != nil {
		return nil, "", err
	}
	if _, err := at(otg3Modulus, 128); err != nil {
		return nil, "", errorf("OTG3 modulus: %v", err)
	}
	write(otg3Modulus, modulus)
	if err := expect(otg3Flags, le32(1), "OTG3 flags"); err != nil {
		return nil, "", err
	}
	write(otg3Flags, le32(0))

	// --- redirector host
	if len(server)+1 > len(redirectorOrig) {
		return nil, "", errorf("host must be at most %d characters", len(redirectorOrig)-1)
	}
	if err := expect(redirectorHost, redirectorOrig, "redirector host"); err != nil {
		return nil, "", err
	}
	write(redirectorHost, padded([]byte(server), len(redirectorOrig)))

	// --- no UPnP: force NetConnStartup's "-noupnp" path, and refuse ProtoUPnP discovery
	if err := expect(noupnpStrstr, noupnpStrstrOrig, `NetConnStartup strstr(params, "-noupnp")`); err != nil {
		return nil, "", err
	}
	if err := expect(noupnpJnz, noupnpJnzOrig, "NetConnStartup jnz around ProtoUpnpCreate"); err != nil {
		return nil, "", err
	}
	write(noupnpJnz, noupnpJmp)
	if err := expect(upnpDisc, upnpDiscOrig, "ProtoUpnpControl 'disc'"); err != nil {
		return nil, "", err
	}
	write(upnpDisc, upnpDiscNew)

	// --- narrowed sockets: bind() goes through a cave that narrows 0.0.0.0 to one address
	if len(img.sections) == 0 {
		return nil, "", errorf("no sections (wrong build?)")
	}
	text := img.sections[0]
	if text.name != ".text" || text.vsize != textVSize {
		return nil, "", errorf("unexpected first section %q / VirtualSize %#x (wrong build?)", text.name, text.vsize)
	}
	for _, va := range bindCalls {
		if err := expect(va, callBind, "call/jmp [bind]"); err != nil { // checked in every profile: the right build
			return nil, "", err
		}
	}
	if err := expect(bindThunk, jmpBind, "call/jmp [bind]"); err != nil {
		return nil, "", err
	}
	var sockets string
	if caveAddress == "" {
		sockets = "sockets as shipped (wildcard binds; loopback patch left out)"
	} else {
		ip, _ := parseIPv4(caveAddress)
		cave := bindCaveCode(ip)
		textEnd := uint64(ImageBase) + uint64(text.va) + uint64(text.rawSize)
		if !(uint64(ImageBase)+uint64(text.va)+textVSize <= loopbackCave && uint64(loopbackCave)+uint64(len(cave)) <= textEnd) {
			return nil, "", errorf("the bind cave doesn't fit into .text's slack (wrong build?)")
		}
		if err := expect(loopbackCave, make([]byte, len(cave)), "unused slack after .text"); err != nil {
			return nil, "", err
		}
		if img.dllChars&0x40 != 0 || img.characteristics&0x1 == 0 {
			return nil, "", errorf("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
		}
		vsizeAt := text.headerAt + 8 // IMAGE_SECTION_HEADER.Misc.VirtualSize
		binary.LittleEndian.PutUint32(data[vsizeAt:], text.rawSize)
		write(loopbackCave, cave)
		sites := append([]uint32(nil), bindCalls...)
		ops := []byte{0xE8, 0xE8, 0xE8, 0xE8, 0xE8}
		sites, ops = append(sites, bindThunk), append(ops, 0xE9)
		for i, site := range sites {
			// nop first, so a call's return address stays site + 6 as in the original
			rel := uint32(loopbackCave) - (site + 6)
			write(site, append([]byte{0x90, ops[i]}, le32(rel)...))
		}
		what := "loopback-only"
		if caveAddress != "127.0.0.1" {
			what = "narrowed to " + caveAddress
		}
		sockets = fmt.Sprintf("sockets %s (bind cave at 0x%08x, %d sites)", what, loopbackCave, len(bindCalls)+1)
	}
	// --- RakNet's loopback literal follows a loopback bind address other than 127.0.0.1 (a test lane)
	if err := expect(raknetLoopback, raknetLoopbackOrig, "RakNet IPV4_LOOPBACK / IPV6_LOOPBACK"); err != nil {
		return nil, "", err
	}
	caveIP, haveCave := ipv4{}, caveAddress != ""
	if haveCave {
		caveIP, _ = parseIPv4(caveAddress)
	}
	if haveCave && caveAddress != "127.0.0.1" && isLoopback(caveIP) {
		lit := append([]byte(caveAddress), 0)
		if len(lit) > raknetLoopRoom {
			return nil, "", errorf("%s doesn't fit RakNet's loopback literal", caveAddress)
		}
		write(raknetLoopback, padded(lit, raknetLoopRoom))
		sockets += ", RakNet loopback " + caveAddress
	}
	// --- a LAN bind address: RakNet's IPV4_LOOPBACK pointer to a copy of it (its start-up send test)
	if err := expect(raknetLoopbackPtr, le32(raknetLoopback), "RakNet IPV4_LOOPBACK pointer"); err != nil {
		return nil, "", err
	}
	if haveCave && !isLoopback(caveIP) {
		lit := append([]byte(caveAddress), 0)
		if len(lit) > lanLoopbackRoom || !(loopbackCave+loopbackCaveLen <= lanLoopbackString) {
			return nil, "", errorf("%s doesn't fit the slack after the bind cave", caveAddress)
		}
		if err := expect(lanLoopbackString, make([]byte, lanLoopbackRoom), "unused slack after the bind cave"); err != nil {
			return nil, "", err
		}
		write(lanLoopbackString, padded(lit, lanLoopbackRoom))
		write(raknetLoopbackPtr, le32(lanLoopbackString))
		sockets += ", RakNet's own-socket test to " + caveAddress
	}
	summary := fmt.Sprintf("redirector host -> %s; UPnP off (-noupnp forced, discovery refused); %s", server, sockets)

	// --- per-user folders with a suffix (a second client copy beside the first)
	for _, f := range dataFolders {
		old := wide(f.name)
		if err := expect(f.va, padded(old, f.room), fmt.Sprintf("data folder name %s and its padding", pyRepr(f.name))); err != nil {
			return nil, "", err
		}
	}
	if o.DataSuffix != "" {
		var names []string
		for _, f := range dataFolders {
			n := wide(f.name + o.DataSuffix)
			if len(n) > f.room {
				return nil, "", errorf("%s doesn't fit into %d bytes", pyRepr(f.name+o.DataSuffix), f.room)
			}
			write(f.va, padded(n, f.room))
			names = append(names, f.name+o.DataSuffix)
		}
		summary += fmt.Sprintf("; data folders %%APPDATA%%\\%s, Documents\\%s", names[0], names[1])
	}

	// --- LARGE_ADDRESS_AWARE (4 GB of address space on 64-bit Windows)
	charAt := img.fileHeader + 0x12 // IMAGE_FILE_HEADER.Characteristics
	if c := binary.LittleEndian.Uint16(data[charAt:]); c != charBuild {
		return nil, "", errorf("unexpected PE characteristics %#06x (wrong build?)", c)
	}
	if !o.NoLAA {
		binary.LittleEndian.PutUint16(data[charAt:], charBuild|charLAA)
		summary += "; LARGE_ADDRESS_AWARE (4 GB of address space)"
	}

	// --- preload every player index's shaper (0-14), not only the first CountPlayers() indexes
	if err := expect(preloadCount, preloadCountOrig, "GameClient_PreloadPlayerCharacters' call to GameSimulator_CountPlayers"); err != nil {
		return nil, "", err
	}
	if !o.NoPreload {
		write(preloadCount, preloadCountNew)
		summary += "; every player's shaper preloaded (player indexes 0-14)"
	}

	// --- a LevelId the map table lacks is named by the lobby table's line of that id
	for _, c := range []struct {
		va   uint32
		want []byte
		what string
	}{{mapLoop, mapLoopOrig, "the map-name method's one-entry loop"},
		{mapMiss, mapMissOrig, "the map-name method's not-found exit"},
		{mapFound, mapFoundOrig, "the map-name method's found path"},
		{mapResume, mapResumeOrig, "the map-name method's key check"}} {
		if err := expect(c.va, c.want, c.what); err != nil {
			return nil, "", err
		}
	}
	var tls *peSection
	for i := range img.sections {
		if img.sections[i].name == ".tls" {
			tls = &img.sections[i]
			break
		}
	}
	if tls == nil || tls.va != tlsVA-ImageBase || tls.vsize != tlsVSize || tls.rawSize != tlsRaw ||
		tls.characteristics&0x80000000 == 0 {
		return nil, "", errorf("unexpected .tls section (wrong build?)")
	}
	if !o.NoMapTable {
		if img.dllChars&0x40 != 0 || img.characteristics&0x1 == 0 {
			return nil, "", errorf("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
		}
		cave := mapCaveCode()
		if !(lanLoopbackString+lanLoopbackRoom <= mapCave &&
			uint64(mapCave)+uint64(len(cave)) <= uint64(ImageBase)+uint64(text.va)+uint64(text.rawSize)) {
			return nil, "", errorf("the map-name cave doesn't fit into .text's slack (wrong build?)")
		}
		if err := expect(mapCave, make([]byte, len(cave)), "unused slack after the bind cave"); err != nil {
			return nil, "", err
		}
		if err := expect(mapKey, make([]byte, mapKeyLen), "unused slack after the TLS template"); err != nil {
			return nil, "", err
		}
		// map the slack: VirtualSize = SizeOfRawData
		binary.LittleEndian.PutUint32(data[text.headerAt+8:], text.rawSize)
		binary.LittleEndian.PutUint32(data[tls.headerAt+8:], tlsRaw)
		write(mapCave, cave)
		write(mapKey, padded(wide(mapKeyText), mapKeyLen))
		write(mapMiss, mapMissNew)
		summary += fmt.Sprintf("; every map named by its LevelId's line in FE_CustomGameLobby (map-name cave at 0x%08x)", mapCave)
	}
	// RATINGSHOW1: the profile box's rating text from the local user's DMAP[0x00070037]
	if err := expect(ratingSite1, ratingSite1Orig, "the extended-data handler's rating value"); err != nil {
		return nil, "", err
	}
	if err := expect(ratingSite2, append(bytes.Clone(ratingSite2Orig), ratingNext2...), "the main menu bind's rating value"); err != nil {
		return nil, "", err
	}
	if !o.NoRating {
		if img.dllChars&0x40 != 0 || img.characteristics&0x1 == 0 {
			return nil, "", errorf("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
		}
		cave := ratingCaveCode()
		if !(mapCave+uint32(len(mapCaveCode())) <= ratingCave &&
			uint64(ratingCave)+uint64(len(cave)) <= uint64(ImageBase)+uint64(text.va)+uint64(text.rawSize)) {
			return nil, "", errorf("the rating cave doesn't fit into .text's slack (wrong build?)")
		}
		if !(mapKey+mapKeyLen <= ratingLabelVA && ratingText+ratingTextRoom <= tlsVA+tlsRaw &&
			2*(len(ratingLabel)+10)+2 <= ratingTextRoom) {
			return nil, "", errorf("the rating text doesn't fit into .tls's slack")
		}
		if err := expect(ratingCave, make([]byte, len(cave)), "unused slack after the map-name cave"); err != nil {
			return nil, "", err
		}
		if err := expect(ratingLabelVA, make([]byte, ratingLabelRoom+ratingTextRoom), "unused slack after the map key"); err != nil {
			return nil, "", err
		}
		// map the slack: VirtualSize = SizeOfRawData
		binary.LittleEndian.PutUint32(data[text.headerAt+8:], text.rawSize)
		binary.LittleEndian.PutUint32(data[tls.headerAt+8:], tlsRaw)
		write(ratingCave, cave)
		label := wide(ratingLabel)
		write(ratingLabelVA, padded(label[:len(label)-2], ratingLabelRoom))
		write(ratingSite1, ratingSite1New)
		write(ratingSite2, ratingSite2New)
		summary += fmt.Sprintf("; the profile box shows the rating the server sends (rating cave at 0x%08x)", ratingCave)
	}

	// --- every Scaleform heap takes its lock, as the movies' sockets need
	for _, va := range heapLockSites {
		if err := expect(va, heapLocksOrig, "a heap constructor's UseLocks"); err != nil {
			return nil, "", err
		}
	}
	if !o.NoHeapLocks {
		for _, va := range heapLockSites {
			write(va, heapLocksNew)
		}
		summary += "; the UI's memory heaps take their locks (a movie's socket reads on a thread)"
	}
	// --- the UI's numbers print with 15 digits: 8.2, not 8.199999999999999
	if err := expect(numFmtFormat, numFmtFormatOrig, "the number-to-string's format"); err != nil {
		return nil, "", err
	}
	if err := expect(numFmtStep, numFmtStepOrig, "the number-to-string's step to more digits"); err != nil {
		return nil, "", err
	}
	if err := expect(numFmt1e15, numFmt1e15Value, "the 1e15 constant"); err != nil {
		return nil, "", err
	}
	if !o.NoNumberFormat {
		write(numFmtFormat, numFmtFormatNew)
		write(numFmtStep, numFmtStepNew)
		summary += "; the UI's numbers print with 15 digits (8.2, not 8.199999999999999)"
	}
	// --- the profile's averages are real divisions at full precision: 8.7, not 8.6
	if err := expect(averagesBlock, averagesOrig, "the profile averages' block"); err != nil {
		return nil, "", err
	}
	if !o.NoAverages {
		write(averagesBlock, averagesNew)
		summary += "; the profile's averages at full precision (8.7, not 8.6)"
	}
	// --- an enemy creature is drawn by its team bit and fades in and out at the fog's edge
	for _, f := range fadeSites {
		if err := expect(f.va, f.orig, f.what); err != nil {
			return nil, "", err
		}
	}
	if !o.NoFade {
		for _, f := range fadeSites {
			write(f.va, f.new)
		}
		summary += "; enemy creatures fade in and out by their team's sight and the fog (0.25 s)"
	}
	// --- the middle-mouse drag grabs the ground when the preference OptionDragGrab is on
	sites := dragGrabSites()
	for _, s := range sites {
		if err := expect(s.va, s.orig, "the drag scroll's hook site"); err != nil {
			return nil, "", err
		}
	}
	optSites := dragOptionsSites()
	for _, s := range optSites {
		if err := expect(s.va, s.orig, "the gameplay options table's reader"); err != nil {
			return nil, "", err
		}
	}
	if err := expect(optTable, optTableFirst, "the gameplay options table (OptionFilterProfanity first)"); err != nil {
		return nil, "", err
	}
	if !o.NoDgx && !o.NoDragGrab {
		if img.dllChars&0x40 != 0 || img.characteristics&0x1 == 0 {
			return nil, "", errorf("exe is relocatable (ASLR or relocations); absolute addresses in the cave would break")
		}
		if !(ratingText+ratingTextRoom <= dragState && dragState+dragStateRoom <= tlsVA+tlsRaw &&
			dragRayEnd+12 <= dragState+dragStateRoom) {
			return nil, "", errorf("the drag grab's state doesn't fit into .tls's slack")
		}
		if err := expect(dragState, make([]byte, dragStateRoom), "unused slack after the rating text"); err != nil {
			return nil, "", err
		}
		binary.LittleEndian.PutUint32(data[tls.headerAt+8:], tlsRaw) // map .tls's slack (VirtualSize = SizeOfRawData)
		caveVA, err := dgxPlace(data, "draggrab", dragGrabCode())
		if err != nil {
			return nil, "", err
		}
		for _, s := range sites {
			write(s.va, s.new)
		}
		table, err := at(optTable, optTableSize)
		if err != nil {
			return nil, "", err
		}
		piece, err := dragOptionsPiece(table)
		if err != nil {
			return nil, "", err
		}
		tableVA, err := dgxPlace(data, "options", piece)
		if err != nil {
			return nil, "", err
		}
		for _, s := range optSites {
			write(s.va, s.new)
		}
		summary += fmt.Sprintf("; the middle-mouse drag grabs the ground when the preference OptionDragGrab is on "+
			"(cave at 0x%08x, the Options menu's 13-entry gameplay table at 0x%08x)", caveVA, tableVA)
	}
	if !o.NoDgx {
		summary += fmt.Sprintf("; an executable section .dgx at 0x%08x (%d bytes) for later patches", dgxVA, dgxSize)
	}
	// --- the Profile loads the same player again when asked (its mode tabs)
	if err := expect(profileReloadSite, profileReloadOrig, "the Profile's same-player check"); err != nil {
		return nil, "", err
	}
	if !o.NoProfileReload {
		write(profileReloadSite, profileReloadNew)
		summary += "; the Profile loads its player again when asked (its mode tabs)"
	}
	return data, summary, nil
}

// Description is what an exe carries (patch_client.py's describe, --show), by key: redirector, bind, appdata,
// documents, raknet_loopback, ca_sha256, ca_flag, laa, preload, maptable, rating, heaplocks, numfmt, averages, fade,
// dgx, draggrab, profilereload (DescriptionKeys, in --show's order).
type Description map[string]string

// DescriptionKeys are Description's keys in patch_client.py --show's order.
var DescriptionKeys = []string{"redirector", "bind", "appdata", "documents", "raknet_loopback", "ca_sha256", "ca_flag", "laa", "preload", "maptable", "rating", "heaplocks", "numfmt", "averages", "fade", "dgx", "draggrab", "profilereload"}

// HostKeys are what a match host's copy must carry (setup_all.py's HOST_KEYS): its sockets are the harness's.
var HostKeys = []string{"redirector", "appdata", "documents"}

// Describe reads what an exe (patched or not) carries.
func Describe(data []byte) (Description, error) {
	img, err := parsePE(data)
	if err != nil {
		return nil, err
	}
	at := func(va uint32, n int) []byte {
		b, e := img.bytesAt(va, n)
		if e != nil && err == nil {
			err = e
		}
		return b
	}
	out := Description{}
	host := at(redirectorHost, len(redirectorOrig))
	cave := at(loopbackCave, loopbackCaveLen)
	thunk := at(bindThunk, 6)
	folders := [][]byte{at(dataFolders[0].va, dataFolders[0].room), at(dataFolders[1].va, dataFolders[1].room)}
	ptrBytes := at(raknetLoopbackPtr, 4)
	mod := at(otg3Modulus, 128)
	flags := at(otg3Flags, 4)
	count := at(preloadCount, len(preloadCountOrig))
	miss := at(mapMiss, len(mapMissOrig))
	mcave := at(mapCave, len(mapCaveCode()))
	keyHead := at(mapKey, 2*len(mapKeyPrefix))
	site1 := at(ratingSite1, len(ratingSite1Orig))
	site2 := at(ratingSite2, len(ratingSite2Orig))
	rcave := at(ratingCave, len(ratingCaveCode()))
	label := at(ratingLabelVA, 2*len(ratingLabel))
	if err != nil {
		return nil, err
	}
	out["redirector"] = cString(host)
	ref := bindCaveCode(ipv4{})
	switch {
	case bytes.Equal(thunk, jmpBind):
		out["bind"] = "as-shipped"
	case bytes.Equal(cave[:len(cave)-10], ref[:len(ref)-10]) && bytes.Equal(cave[len(cave)-6:], jmpBind):
		a := cave[len(cave)-10 : len(cave)-6]
		out["bind"] = fmt.Sprintf("%d.%d.%d.%d", a[0], a[1], a[2], a[3])
	default:
		out["bind"] = "unknown"
	}
	for i, key := range []string{"appdata", "documents"} {
		raw := folders[i]
		end := len(raw)
		for j := 0; j < len(raw)-1; j += 2 { // the first UTF-16 NUL at an even offset
			if raw[j] == 0 && raw[j+1] == 0 {
				end = j
				break
			}
		}
		out[key] = utf16le(raw[:end])
	}
	ptr := binary.LittleEndian.Uint32(ptrBytes)
	target, room := uint32(raknetLoopback), raknetLoopRoom
	if ptr == lanLoopbackString {
		target, room = lanLoopbackString, lanLoopbackRoom
	}
	lit, lerr := img.bytesAt(target, room)
	if lerr != nil {
		return nil, lerr
	}
	out["raknet_loopback"] = cString(lit)
	if ptr != raknetLoopback && ptr != lanLoopbackString {
		out["raknet_loopback"] = fmt.Sprintf("unknown pointer 0x%08x", ptr)
	}
	out["ca_sha256"] = sha256Hex(mod)[:16]
	out["ca_flag"] = fmt.Sprint(binary.LittleEndian.Uint32(flags))
	out["laa"] = "off"
	if img.characteristics&charLAA != 0 {
		out["laa"] = "on"
	}
	out["heaplocks"] = "unknown"
	if l0 := at(heapLockSites[0], len(heapLocksNew)); bytes.Equal(l0, at(heapLockSites[1], len(heapLocksNew))) {
		switch {
		case bytes.Equal(l0, heapLocksNew):
			out["heaplocks"] = "on"
		case bytes.Equal(l0, heapLocksOrig):
			out["heaplocks"] = "as-shipped"
		}
	}
	out["numfmt"] = "unknown"
	nf, ns := at(numFmtFormat, len(numFmtFormatNew)), at(numFmtStep, len(numFmtStepNew))
	switch {
	case bytes.Equal(nf, numFmtFormatNew) && bytes.Equal(ns, numFmtStepNew):
		out["numfmt"] = "on"
	case bytes.Equal(nf, numFmtFormatOrig) && bytes.Equal(ns, numFmtStepOrig):
		out["numfmt"] = "as-shipped"
	}
	out["averages"] = "unknown"
	switch block := at(averagesBlock, len(averagesOrig)); {
	case bytes.Equal(block, averagesNew):
		out["averages"] = "on"
	case bytes.Equal(block, averagesOrig):
		out["averages"] = "as-shipped"
	}
	fadeOn, fadeShipped := true, true
	for _, f := range fadeSites {
		got := at(f.va, len(f.orig))
		fadeOn = fadeOn && bytes.Equal(got, f.new)
		fadeShipped = fadeShipped && bytes.Equal(got, f.orig)
	}
	switch {
	case fadeOn:
		out["fade"] = "on"
	case fadeShipped:
		out["fade"] = "as-shipped"
	default:
		out["fade"] = "unknown"
	}
	switch {
	case bytes.Equal(count, preloadCountNew):
		out["preload"] = "all"
	case bytes.Equal(count, preloadCountOrig):
		out["preload"] = "as-shipped"
	default:
		out["preload"] = "unknown"
	}
	switch {
	case bytes.Equal(miss, mapMissOrig):
		out["maptable"] = "as-shipped"
	case bytes.Equal(miss[:len(mapMissNew)], mapMissNew) && bytes.Equal(mcave, mapCaveCode()) &&
		bytes.Equal(keyHead, wide(mapKeyPrefix)[:2*len(mapKeyPrefix)]):
		out["maptable"] = "on"
	default:
		out["maptable"] = "unknown"
	}
	switch {
	case bytes.Equal(site1, ratingSite1Orig) && bytes.Equal(site2, ratingSite2Orig):
		out["rating"] = "as-shipped"
	case bytes.Equal(site1, ratingSite1New) && bytes.Equal(site2, ratingSite2New) && bytes.Equal(rcave, ratingCaveCode()) &&
		bytes.Equal(label, wide(ratingLabel)[:2*len(ratingLabel)]):
		out["rating"] = "on"
	default:
		out["rating"] = "unknown"
	}
	out["dgx"] = describeDgx(img)
	out["draggrab"] = describeDragGrab(img, out["dgx"])
	out["profilereload"] = "unknown"
	switch site := at(profileReloadSite, len(profileReloadOrig)); {
	case bytes.Equal(site, profileReloadNew):
		out["profilereload"] = "on"
	case bytes.Equal(site, profileReloadOrig):
		out["profilereload"] = "as-shipped"
	}
	return out, nil
}

// Differences lists the keys (all of DescriptionKeys when keys is nil) where got differs from want, as
// "key got (want w)" (setup_all.py's wording).
func Differences(got, want Description, keys []string) []string {
	if keys == nil {
		keys = DescriptionKeys
	}
	var out []string
	for _, k := range keys {
		if got[k] != want[k] {
			out = append(out, fmt.Sprintf("%s %s (want %s)", k, got[k], want[k]))
		}
	}
	return out
}

// --- helpers

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func padded(b []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, b)
	return out
}

// wide is a NUL-terminated UTF-16LE string.
func wide(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s + "\x00")) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func utf16le(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	s := string(utf16.Decode(u))
	if len(b)%2 == 1 {
		s += "�" // Python's "replace" for a truncated code unit
	}
	return s
}

// cString is the bytes up to the first NUL, as Python's decode("ascii", "replace") shows them.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	var sb strings.Builder
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
		} else {
			sb.WriteRune('�')
		}
	}
	return sb.String()
}

func mustHex(s string) []byte {
	var out []byte
	for i := 0; i+1 < len(s); i += 2 {
		var v byte
		for _, c := range s[i : i+2] {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= byte(c - '0')
			case c >= 'a' && c <= 'f':
				v |= byte(c-'a') + 10
			default:
				panic("bad hex " + s)
			}
		}
		out = append(out, v)
	}
	if len(s)%2 != 0 {
		panic("odd hex " + s)
	}
	return out
}

// pyRepr quotes a string as Python's repr does for the plain ASCII the messages carry.
func pyRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}
