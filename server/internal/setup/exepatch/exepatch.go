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
	charBuild         = 0x0103 // IMAGE_FILE_HEADER.Characteristics of build 854
	charLAA           = 0x0020 // IMAGE_FILE_LARGE_ADDRESS_AWARE
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
	dataFolders        = []struct {
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

// LANAddress checks an IPv4 address of our own network: not loopback, wildcard, multicast, reserved or public
// (patch_client.py's lan_address, with Python 3.12's ipaddress rules). It returns the address as written.
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
	case isGlobal(ip):
		return "", errorf("%s %s is a public address; the lan profile is for our own machines only", what, ip)
	}
	return ip.String(), nil
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
	return data, summary, nil
}

// Description is what an exe carries (patch_client.py's describe, --show), by key: redirector, bind, appdata,
// documents, raknet_loopback, ca_sha256, ca_flag, laa (DescriptionKeys, in --show's order).
type Description map[string]string

// DescriptionKeys are Description's keys in patch_client.py --show's order.
var DescriptionKeys = []string{"redirector", "bind", "appdata", "documents", "raknet_loopback", "ca_sha256", "ca_flag", "laa"}

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
