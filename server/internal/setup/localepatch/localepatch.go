// Package localepatch makes a player copy's added locale package game\Data\Locale\en-us\AA_Server.package:
// changed copies of three of the copy's own string tables with the server's login texts and links, byte for byte
// what re/scripts/patch_locale.py makes (that script's docstring has the rule). The client mounts every *.package of Locale\en-us and the one whose name
// sorts first wins, so AA_Server.package overrides the shipped Locale.package, which stays untouched. The game's
// text can't ship, so the package is made at setup time from the copy's own Locale.package.
package localepatch

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/sitelinks"
)

// The package's name and place.
const (
	File    = "AA_Server.package"
	Shipped = "Locale.package"
	Dir     = "game/Data/Locale/en-us"                                     // in a copy
	Rel     = Dir + "/" + File                                             // the added package, in a copy
	Source  = Dir + "/" + Shipped                                          // the copy's own tables it is made from
	Comment = "# PH2b: lines the build lacks, added by the server's setup" // before appended lines
)

// The string tables' key parts and the three tables (FNV-1 of BlazeErrors, the URL table's id, LoginScreen).
const (
	StringType  = 0x02FAC0B6
	StringGroup = 0x02FABF01
	BlazeErrors = 0x1E5BC096
	URLs        = 0x30B0670B
	LoginScreen = 0x69E51800
)

// ErrorLines are BlazeErrors' new lines (full error codes); 0x00CA0001 and 0x40720000 have no line in build 854.
var ErrorLines = map[uint32]string{
	0x000B0001: "The login name or password you entered is incorrect.",
	0x000C0001: "The login name or password you entered is incorrect.",
	0x00160001: "Your login name contains invalid characters. Use letters, digits and . _ - @ only.",
	0x00CA0001: "Too short: a login name needs at least 3 characters and a password at least 8.",
	0x00200001: "This account has been banned. Please contact the server's operator.",
	0x00290001: "This account has been deleted. Please contact the server's operator if this is a mistake.",
	0x002B0001: "This account has been banned. Please contact the server's operator.",
	0x00650001: "This account has been disabled. Please contact the server's operator.",
	0x400C0000: "Too long: a login name may have at most 32 characters and a password at most 63.",
	0x40720000: "Too many sign-ups or login attempts from your address. Please wait a while and try again.",
	// Start-Client's server picker (launcher.FrontErr*): the login refused before it reached a server.
	0x00E00001: "That server isn't in your list. Pick one with SERVER above the login, or add it there.",
	0x00E10001: "The server you picked doesn't answer. It may be offline, or its address may be wrong. Try again later.",
	0x00E20001: "The server's key has changed since you last played, so the login was refused. Ask its operator: see the troubleshooting guide.",
	0x00E30001: "Something answers at that address, but no Dawngate server with the tunnel on. Ask its operator.",
	0x00E40001: "The server you picked answered, but couldn't take the login. Try again in a moment.",
	0x00E50001: "The server was removed from your list.",
	0x00E60001: "That isn't a server address. Use an IPv4 address such as 203.0.113.5, or a host name.",
	0x00E70001: "New server key: check the fingerprint shown in the list, then log in again to trust it.",
	0x00E80001: "This PC is always in the list: it can't be removed.",
	0x00E90001: "The server on this PC is still being set up. Log in again when the list says online.",
	0x00EA0001: "The server on this PC didn't start: Start-Client's window says why.",
}

// LoginLines are LoginScreen's: the account field's label and the check box beside it, and the server picker's
// texts (client_ui/serverpicker/ServerPicker.as reads 0x5E70A1xx through Global.ForceTranslateString, its English
// fallbacks the same; {1} is a ping, {2} the key a server shows, {3} the trusted key).
var LoginLines = map[uint32]string{
	0x0AC5BE93: "Enter Login Name",
	0x0AC5BE95: "Remember Login Name",
	0x5E70A101: "SERVER",
	0x5E70A102: "SELECT A SERVER",
	0x5E70A103: "ADD SERVER",
	0x5E70A104: "REMOVE",
	0x5E70A105: "DONE",
	0x5E70A106: "ADD",
	0x5E70A107: "CANCEL",
	0x5E70A108: "Server address",
	0x5E70A109: "Name (optional)",
	0x5E70A10A: "Added at your next login, which checks the server and shows its key: log in again to trust it.",
	0x5E70A10B: "Type the server's address: an IPv4 address such as 203.0.113.5, or a host name.",
	0x5E70A10C: "No servers yet: add one.",
	0x5E70A10D: "none: add a server",
	0x5E70A10E: "pick a server",
	0x5E70A10F: "This PC",
	0x5E70A110: "online, {1} ms",
	0x5E70A111: "online, new key",
	0x5E70A112: "KEY CHANGED",
	0x5E70A113: "offline",
	0x5E70A114: "no tunnel",
	0x5E70A115: "added at login",
	0x5E70A116: "not checked",
	0x5E70A117: "starts at login",
	0x5E70A118: "setting up, {1} %",
	0x5E70A119: "starting",
	0x5E70A11A: "didn't start",
	0x5E70A120: "Online, {1} ms. Its key: {2}.",
	0x5E70A121: "New server key {2}: compare it with the fingerprint its operator gives you. The first login stops here; log in again to trust it.",
	0x5E70A122: "KEY CHANGED: you trusted {3}, the server now shows {2}. Don't log in until its operator confirms the new key: see the troubleshooting guide.",
	0x5E70A123: "No answer from this server. It may be offline, or its address may be wrong.",
	0x5E70A124: "Something answers at this address, but no Dawngate server with the tunnel on.",
	0x5E70A125: "Not checked yet.",
	0x5E70A126: "The server on this PC. It starts when you log in; the first time it sets up its match host first (about half a minute).",
	0x5E70A127: "Setting up the server on this PC: {1} %. Your login waits for it, or log in again when it says online.",
	0x5E70A128: "The server on this PC is starting.",
	0x5E70A129: "The server on this PC didn't start: Start-Client's window says why. Log in again to retry.",
}

// URLPages are the URL table's lines that link to the server's pages (sitelinks): the legal links 1, 2, 7, Forgot
// Password? 3, the account and persona creation 4, 5, Sign up now! 6, forum, survey and funds 0x100, 0x101, 0x103.
// 0x102 (Adobe's Flash download) stays.
var URLPages = map[uint32]string{
	0x00000001: sitelinks.Terms, 0x00000002: sitelinks.Privacy, 0x00000003: sitelinks.Password,
	0x00000004: sitelinks.Signup, 0x00000005: sitelinks.Signup, 0x00000006: sitelinks.Signup,
	0x00000007: sitelinks.Terms, 0x00000100: sitelinks.Index, 0x00000101: sitelinks.Index,
	0x00000103: sitelinks.Index,
}

// Options say where the links point: the address the copy is patched for, dgserver's HTTP port (0: 18080) and the
// server's website ("" = none yet: dgserver's own pages).
type Options struct {
	Host      string
	HTTPPort  int
	WebDomain string
}

// Base is where the links point (sitelinks.Base).
func (o Options) Base() (string, error) {
	port := o.HTTPPort
	if port == 0 {
		port = sitelinks.HTTPPort
	}
	return sitelinks.Base(o.Host, port, o.WebDomain)
}

// Tables are the new lines per table instance.
func Tables(o Options) (map[uint32]map[uint32]string, error) {
	base, err := o.Base()
	if err != nil {
		return nil, err
	}
	urls := map[uint32]string{}
	for id, page := range URLPages {
		urls[id] = base + page
	}
	return map[uint32]map[uint32]string{BlazeErrors: ErrorLines, URLs: urls, LoginScreen: LoginLines}, nil
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// lineID is a table line's id: "0x" or "0X", 8 hex digits and a blank; n is that prefix's length.
func lineID(body []byte) (id uint32, n int, ok bool) {
	if len(body) < 11 || body[0] != '0' || (body[1] != 'x' && body[1] != 'X') || body[10] != ' ' {
		return 0, 0, false
	}
	for _, c := range body[2:10] {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return 0, 0, false
		}
	}
	v, err := strconv.ParseUint(string(body[2:10]), 16, 32)
	if err != nil {
		return 0, 0, false
	}
	return uint32(v), 11, true
}

// PatchTable puts lines into a table's text (patch_locale.py's patch_table): a line with one of the ids gets the new
// text after its "0x<id> " prefix (a first line's UTF-8 BOM and a final "\r" kept); missing ids are appended in id order
// after Comment, each "0x<id> <text>\r\n", after a "\r\n" when the text doesn't end in "\n".
func PatchTable(text []byte, lines map[uint32]string) []byte {
	parts := bytes.Split(text, []byte("\n"))
	seen := map[uint32]bool{}
	for i, part := range parts {
		body, cr := part, []byte(nil)
		if bytes.HasSuffix(body, []byte("\r")) {
			body, cr = body[:len(body)-1], []byte("\r")
		}
		var lead []byte
		if i == 0 && bytes.HasPrefix(body, bom) {
			lead, body = bom, body[len(bom):]
		}
		id, n, ok := lineID(body)
		if !ok {
			continue
		}
		if t, ok := lines[id]; ok {
			seen[id] = true
			var b []byte
			b = append(b, lead...)
			b = append(b, body[:n]...)
			b = append(b, t...)
			parts[i] = append(b, cr...)
		}
	}
	out := bytes.Join(parts, []byte("\n"))
	var missing []uint32
	for id := range lines {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	if len(missing) > 0 {
		if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, "\r\n"...)
		}
		out = append(out, Comment+"\r\n"...)
		for _, id := range missing {
			out = append(out, fmt.Sprintf("0x%08x %s\r\n", id, lines[id])...)
		}
	}
	return out
}

// table is the first entry of the string table inst in the package, decompressed; nil when it has none.
func table(p *dbpf.Package, inst uint32) ([]byte, error) {
	for _, e := range p.Entries {
		if e.Type == StringType && e.Group == StringGroup && e.Instance == uint64(inst) {
			return p.Read(e)
		}
	}
	return nil, nil
}

// Build makes AA_Server.package from the copy's Locale.package (its bytes): the three tables changed, in instance
// order, stored uncompressed (dbpf.WritePackage). A table missing from it (a synthetic test build) is made of the
// appended lines alone.
func Build(locale []byte, o Options) ([]byte, error) {
	want, err := Tables(o)
	if err != nil {
		return nil, err
	}
	p, err := dbpf.Parse(locale)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Shipped, err)
	}
	insts := make([]uint32, 0, len(want))
	for inst := range want {
		insts = append(insts, inst)
	}
	sort.Slice(insts, func(i, j int) bool { return insts[i] < insts[j] })
	var entries []dbpf.NewEntry
	for _, inst := range insts {
		text, err := table(p, inst)
		if err != nil {
			return nil, fmt.Errorf("%s: table %08X: %w", Shipped, inst, err)
		}
		entries = append(entries, dbpf.NewEntry{Type: StringType, Group: StringGroup, Instance: uint64(inst),
			Data: PatchTable(text, want[inst])})
	}
	return dbpf.WritePackage(entries), nil
}
