// Package configpatch points the network hosts in a working copy's config packages at our server:
// re/scripts/patch_config.py's port (A1, B6, B12), byte for byte. The
// client reads its hosts from properties, not from code:
//
//   - Config.package: text property files (type 024A0E52). Entry 8C47952D holds the app properties,
//     including the "Nightly Release" overrides at its end that the shipped client uses (EA's CDN rules
//     URL, telemetry 159.153.235.32:9988); entry B16AFE42 the Spore-era hosts.
//   - Config_Ship.package: binary property lists (type 00B1B104). Entry 08EA246B is the compiled form of
//     the same app properties with the pre-override values.
//   - Config_Pack.package: binary property lists too. Entry 9DD4AB9B holds the EAWebKit settings, among
//     them WebkitAllowedDomains (0xC27EA5F8), the domains the main menu's home page view may open.
//
// Every string property whose value is a URL becomes http://<host>:<http port><original path>; one that
// is an IPv4 address or a host name ending in .com/.net/.org/.tv becomes <host>; NewTelemetryServerPort
// becomes <telemetry port>; <host> is added to WebkitAllowedDomains (B6). With AFKMinutes (B12, opt-in)
// the AFK kick's AFKThreshhold and AFKDisconnectThreshhold change too, in Config.package's text and
// Config_Ship.package's binary app properties; each must hold its original value (4, 1) before, or the
// patch is refused. Changed entries are stored uncompressed (dbpf.Rebuild).
//
// Patch keeps patch_config.py's files: the first run saves <name>.package.orig beside each package, every
// run patches from that backup, so running it again is safe, and Restore puts the originals back. It
// refuses a pristine extraction (a path with a folder named ia_*, the repository's client\ia_*).
//
// Differences from patch_config.py, on purpose: Options.Validate refuses an empty host or one with
// whitespace, quotes or non-ASCII characters (the script wrote any text it was given into the
// configs) and ports outside 1-65535 (the script took any integer the property could hold); a package
// is written through a temporary file and a rename.
package configpatch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"dawngate/server/internal/setup/dbpf"
	"dawngate/server/internal/setup/pytext"
)

// The DBPF entry types patch_config.py patches.
const (
	TextType   = 0x024A0E52 // text property files
	BinaryType = 0x00B1B104 // binary property lists
)

// Packages are the packages patch_config.py patches, in its order (each in the copy's game\Data).
var Packages = []string{"Config.package", "Config_Ship.package", "Config_Pack.package"}

// AFKMax is the largest AFK value in minutes (7 days: the client's stopwatch deadline is minutes × 60 ×
// QueryPerformanceFrequency in an int64).
const AFKMax = 10080

// The AFK kick's app properties (uint32 minutes) and their original values.
const (
	afkName           = "AFKThreshhold"
	afkDisconnectName = "AFKDisconnectThreshhold"
)

var afkOriginal = map[string]int64{afkName: 4, afkDisconnectName: 1}

var (
	telemetryPortID  = propID("NewTelemetryServerPort")
	allowedDomainsID = propID("WebkitAllowedDomains") // 0xC27EA5F8, Config_Pack.package 9DD4AB9B
	afkIDs           = map[uint32]string{propID(afkName): afkName, propID(afkDisconnectName): afkDisconnectName}
)

// Options are patch_config.py's options.
type Options struct {
	Host          string // --host: the server's address (default 127.0.0.1; the LAN address for a server on another machine)
	HTTPPort      int    // --http-port: dgserver's HTTP port (CDN, crash, debug telemetry; default 18080)
	TelemetryPort int    // --telemetry-port: dgserver's telemetry sink (default 9988)
	// AFKMinutes (--afk-minutes N, 0 = leave it) is AFKThreshhold, the minutes without input in a game
	// before the AFK warning (original 4); AFKDisconnectMinutes (--afk-disconnect-minutes M, 0 = leave
	// it; needs AFKMinutes) the minutes from the warning to the kick (original 1). 1..AFKMax each.
	AFKMinutes           int
	AFKDisconnectMinutes int
}

// Defaults are patch_config.py's defaults: 127.0.0.1, ports 18080 and 9988, no AFK change.
func Defaults() Options {
	return Options{Host: "127.0.0.1", HTTPPort: 18080, TelemetryPort: 9988}
}

// Validate checks the options (patch_config.py's AFK checks, plus the host and port checks of the Go
// port).
func (o Options) Validate() error {
	if o.Host == "" {
		return errors.New("configpatch: no host")
	}
	for _, r := range o.Host {
		if r <= ' ' || r >= 0x7F || r == '"' || r == '\'' {
			return fmt.Errorf("configpatch: host %q: only printable ASCII without spaces or quotes", o.Host)
		}
	}
	for _, p := range []struct {
		name string
		v    int
	}{{"http port", o.HTTPPort}, {"telemetry port", o.TelemetryPort}} {
		if p.v < 1 || p.v > 65535 {
			return fmt.Errorf("configpatch: %s %d: must be 1..65535", p.name, p.v)
		}
	}
	for _, a := range []struct {
		flag string
		v    int
	}{{"--afk-minutes", o.AFKMinutes}, {"--afk-disconnect-minutes", o.AFKDisconnectMinutes}} {
		if a.v < 0 || a.v > AFKMax {
			return fmt.Errorf("configpatch: %s %d: must be 1..%d minutes", a.flag, a.v, AFKMax)
		}
	}
	if o.AFKDisconnectMinutes != 0 && o.AFKMinutes == 0 {
		return errors.New("configpatch: --afk-disconnect-minutes needs --afk-minutes")
	}
	return nil
}

// afkWant is the requested value of an AFK property, 0 when its option isn't given.
func (o Options) afkWant(name string) int64 {
	if name == afkName {
		return int64(o.AFKMinutes)
	}
	return int64(o.AFKDisconnectMinutes)
}

// Result is one package's patch.
type Result struct {
	// Data is the patched package, nil when nothing changes.
	Data []byte
	// Changes are patch_config.py's change lines ("<package> <entry key> <property>: <old> -> <new>").
	Changes []string
	// Entries is the number of changed entries.
	Entries int
	// AFKHits names each AFK property found with its original value (patch_config.py's afk_hits).
	AFKHits []string
}

// Package patches the package bytes data (normally the untouched package, the .orig) in memory, as
// patch_config.py's patched_package does; it writes nothing. name is the package's file name in the
// change lines. The result is deterministic, so comparing it with a copy's package tells whether the
// copy is current (setup_all.py's check).
func Package(data []byte, name string, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	pkg, err := dbpf.Parse(data)
	if err != nil {
		return Result{}, fmt.Errorf("configpatch: %s: %w", name, err)
	}
	var res Result
	repl := map[string][]byte{}
	for _, e := range pkg.Entries {
		where := name + " " + e.Key()
		d, err := pkg.Read(e) // every entry, as patch_config.py reads them
		if err != nil {
			return Result{}, fmt.Errorf("configpatch: %w", err)
		}
		switch e.Type {
		case TextType:
			text := pytext.Latin1Decode(d) // byte-preserving; the files are ASCII
			out, err := patchText(text, o, &res, where)
			if err != nil {
				return Result{}, err
			}
			if out != text {
				b, ok := pytext.Latin1Encode(out)
				if !ok {
					return Result{}, fmt.Errorf("configpatch: %s: the patched text isn't Latin-1", where)
				}
				repl[e.Key()] = b
			}
		case BinaryType:
			out, err := patchBinary(d, o, &res, where)
			if err != nil {
				return Result{}, err
			}
			if out != nil {
				repl[e.Key()] = out
			}
		}
	}
	if len(repl) == 0 {
		return res, nil
	}
	if res.Data, err = pkg.Rebuild(repl); err != nil {
		return Result{}, fmt.Errorf("configpatch: %s: %w", name, err)
	}
	res.Entries = len(repl)
	return res, nil
}

// Character classes of patch_config.py's TEXT_PROP on Latin-1 text: Python's \s, \S and \w.
const (
	pyS = `\t\n\x0B\x0C\r\x1C-\x1F \x{85}\x{A0}`
	pyW = `0-9A-Za-z_\x{AA}\x{B2}\x{B3}\x{B5}\x{B9}\x{BA}\x{BC}-\x{BE}\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{FF}`
)

// textProp is patch_config.py's TEXT_PROP, property <name> <id> <type> <value> with the value optionally
// quoted: (?P<q>"?)(?P<val>[^"\s]*)(?P=q) becomes the two alternatives a backtracking engine tries in
// that order (Go's regexp has no back-references; its leftmost-first submatches are the ones Python's
// re finds). Groups: 1 the part before the value, 2 the name, 3 the type, 4 a quoted value, 5 an
// unquoted one.
var textProp = regexp.MustCompile(`(?m)^([ \t]*property[ \t]+([^` + pyS + `]+)[ \t]+[^` + pyS + `]+[ \t]+([` + pyW +
	`]+)[ \t]+)(?:"([^"` + pyS + `]*)"|([^"` + pyS + `]*))`)

// patchText patches a text property file (patch_text).
func patchText(text string, o Options, res *Result, where string) (string, error) {
	var sb strings.Builder
	last := 0
	for _, m := range textProp.FindAllStringSubmatchIndex(text, -1) {
		pre, name, typ := text[m[2]:m[3]], text[m[4]:m[5]], text[m[6]:m[7]]
		q, val := "", ""
		if m[8] >= 0 {
			q, val = `"`, text[m[8]:m[9]]
		} else {
			val = text[m[10]:m[11]]
		}
		var newVal string
		changed := false
		switch {
		case typ == "string8" || typ == "string16":
			newVal, changed = localize(val, o)
		case name == "NewTelemetryServerPort" && (typ == "int" || typ == "uint"):
			newVal, changed = strconv.Itoa(o.TelemetryPort), true
		case (name == afkName || name == afkDisconnectName) && typ == "uint":
			v, ok, err := afkValue(name, val, o, res, where)
			if err != nil {
				return "", err
			}
			if ok {
				newVal, changed = strconv.FormatInt(v, 10), true
			}
		}
		if !changed || newVal == val {
			continue
		}
		res.Changes = append(res.Changes, fmt.Sprintf("%s %s: %s -> %s", where, name, val, newVal))
		sb.WriteString(text[last:m[0]])
		sb.WriteString(pre + q + newVal + q)
		last = m[1]
	}
	if last == 0 {
		return text, nil
	}
	sb.WriteString(text[last:])
	return sb.String(), nil
}

// afkValue is the new value of an AFK property in a text file (afk_value): ok false when its option
// isn't given; an error when the original isn't the expected one. val is the text value: Python took
// int(val) when val.isdigit() and compared the string otherwise, so only a value of decimal digits
// whose number is the original passes ("04" does).
func afkValue(name, val string, o Options, res *Result, where string) (int64, bool, error) {
	want := o.afkWant(name)
	if want == 0 {
		return 0, false, nil
	}
	orig := afkOriginal[name]
	if n, ok := decimalValue(val); !ok || n != orig {
		return 0, false, fmt.Errorf("configpatch: %s %s: original value %s, expected %d; not patching an unknown config", where, name, val, orig)
	}
	res.AFKHits = append(res.AFKHits, name)
	return want, true, nil
}

// decimalValue is int(s) for a string of Unicode decimal digits (Python's str.isdecimal, Go's
// unicode.IsDigit), saturated far above any AFK value; ok false for anything else.
func decimalValue(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return 0, false
		}
		// Unicode's decimal digits come in runs of ten consecutive code points, 0 to 9.
		start := r
		for unicode.IsDigit(start - 1) {
			start--
		}
		if n < 1<<40 {
			n = n*10 + int64((r-start)%10)
		}
	}
	return n, true
}

// patchBinary patches a binary property list (patch_binary); nil when nothing changes.
func patchBinary(data []byte, o Options, res *Result, where string) ([]byte, error) {
	props, err := parseProps(data)
	if err != nil {
		return nil, fmt.Errorf("configpatch: %s: %w", where, err)
	}
	changed := false
	fail := func(err error) ([]byte, error) { return nil, fmt.Errorf("configpatch: %s: %w", where, err) }
	for _, p := range props {
		if p.id == allowedDomainsID && p.typ == typeString8 && p.isArray() {
			domains, err := p.strs()
			if err != nil {
				return fail(err)
			}
			if !contains(domains, o.Host) {
				if err := p.setStrs(append(domains, o.Host)); err != nil {
					return fail(err)
				}
				res.Changes = append(res.Changes, fmt.Sprintf("%s WebkitAllowedDomains: + %s (%d -> %d)", where, o.Host, len(domains), len(domains)+1))
				changed = true
			}
			continue
		}
		var oldText, newText string
		switch {
		case p.isString() && !p.isArray():
			old, err := p.str()
			if err != nil {
				return fail(err)
			}
			nv, ok := localize(old, o)
			if !ok || nv == old {
				continue
			}
			if err := p.setStr(nv); err != nil {
				return fail(err)
			}
			oldText, newText = old, nv
		case p.id == telemetryPortID && (p.typ == typeInt32 || p.typ == typeUint32) && !p.isArray():
			old, err := p.intValue()
			if err != nil {
				return fail(err)
			}
			if int64(o.TelemetryPort) == old {
				continue
			}
			if err := p.setInt(int64(o.TelemetryPort)); err != nil {
				return fail(err)
			}
			oldText, newText = strconv.FormatInt(old, 10), strconv.Itoa(o.TelemetryPort)
		case afkIDs[p.id] != "" && p.typ == typeUint32 && !p.isArray():
			name := afkIDs[p.id]
			want := o.afkWant(name)
			if want == 0 {
				continue
			}
			old, err := p.intValue()
			if err != nil {
				return fail(err)
			}
			if old != afkOriginal[name] {
				return nil, fmt.Errorf("configpatch: %s %s: original value %d, expected %d; not patching an unknown config", where, name, old, afkOriginal[name])
			}
			res.AFKHits = append(res.AFKHits, name)
			if want == old {
				continue
			}
			if err := p.setInt(want); err != nil {
				return fail(err)
			}
			oldText, newText = strconv.FormatInt(old, 10), strconv.FormatInt(want, 10)
		default:
			continue
		}
		res.Changes = append(res.Changes, fmt.Sprintf("%s %#010x: %s -> %s", where, p.id, oldText, newText))
		changed = true
	}
	if !changed {
		return nil, nil
	}
	return serializeProps(props), nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// localize is the local replacement for a host or URL value (patch_config.py's localize): ok false when
// the value is neither.
func localize(value string, o Options) (string, bool) {
	if path, ok := matchURL(value); ok {
		if path == "" {
			path = "/"
		}
		return fmt.Sprintf("http://%s:%d%s", o.Host, o.HTTPPort, path), true
	}
	if matchIPv4(value) || matchHostname(value) {
		return o.Host, true
	}
	return "", false
}

// anchored is the part of value that a Python pattern ending in $ (no MULTILINE) must match: $ also
// matches before a final "\n".
func anchored(value string) string {
	return strings.TrimSuffix(value, "\n")
}

// matchURL is URL = ^[A-Za-z][A-Za-z0-9+.-]*://[^/\s]*(/\S*)?$ (\s Python's); path is the group,
// "" when it didn't take part.
func matchURL(value string) (path string, ok bool) {
	rs := []rune(anchored(value))
	isScheme := func(r rune) bool {
		return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '.' || r == '-'
	}
	if len(rs) == 0 || !(rs[0] >= 'A' && rs[0] <= 'Z' || rs[0] >= 'a' && rs[0] <= 'z') {
		return "", false
	}
	i := 1
	for i < len(rs) && isScheme(rs[i]) {
		i++
	}
	if len(rs)-i < 3 || string(rs[i:i+3]) != "://" {
		return "", false
	}
	i += 3
	for i < len(rs) && rs[i] != '/' && !pytext.IsSpace(rs[i]) {
		i++
	}
	if i == len(rs) {
		return "", true
	}
	if rs[i] != '/' {
		return "", false
	}
	start := i
	for i < len(rs) && !pytext.IsSpace(rs[i]) {
		i++
	}
	if i != len(rs) {
		return "", false
	}
	return string(rs[start:]), true
}

// matchIPv4 is IPV4 = ^\d{1,3}(\.\d{1,3}){3}$ (\d: Unicode decimal digits).
func matchIPv4(value string) bool {
	parts := strings.Split(anchored(value), ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n := 0
		for _, r := range p {
			if !unicode.IsDigit(r) {
				return false
			}
			n++
		}
		if n < 1 || n > 3 {
			return false
		}
	}
	return true
}

// matchHostname is HOSTNAME = ^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.(com|net|org|tv)$ with IGNORECASE: with
// it Python's class also takes U+0130, U+0131, U+017F and U+212A (whose case folds reach ASCII letters);
// the endings take only ASCII letters.
func matchHostname(value string) bool {
	parts := strings.Split(anchored(value), ".")
	if len(parts) < 2 {
		return false
	}
	switch strings.ToLower(parts[len(parts)-1]) {
	case "com", "net", "org", "tv":
	default:
		return false
	}
	if strings.ContainsFunc(parts[len(parts)-1], func(r rune) bool { return r > 0x7F }) {
		return false
	}
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			return false
		}
		for _, r := range p {
			ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' ||
				r == 0x130 || r == 0x131 || r == 0x17F || r == 0x212A
			if !ok {
				return false
			}
		}
	}
	return true
}

// Report is what Patch, DryRun and Restore did: patch_config.py's output lines.
type Report struct {
	Lines []string
}

func (r *Report) add(format string, a ...any) { r.Lines = append(r.Lines, fmt.Sprintf(format, a...)) }

// Patch patches the config packages of a working copy's game\Data folder (patch_config.py <dir>): for
// each of Packages it saves <name>.orig on the first run, patches from the .orig and writes the package;
// a package missing with its .orig is skipped. Each requested AFK value must be found twice (Config.package
// text and Config_Ship.package binary), or Patch fails after writing the packages, as the script did.
func Patch(dataDir string, o Options) (*Report, error) { return run(dataDir, o, false) }

// DryRun reports what Patch would change; it writes nothing (patch_config.py --dry-run).
func DryRun(dataDir string, o Options) (*Report, error) { return run(dataDir, o, true) }

// Restore copies the .orig backups back over the packages (patch_config.py --restore).
func Restore(dataDir string) (*Report, error) {
	dir, err := workingCopy(dataDir)
	if err != nil {
		return nil, err
	}
	rep := &Report{}
	for _, name := range Packages {
		path := filepath.Join(dir, name)
		orig := path + ".orig"
		if !exists(path) && !exists(orig) {
			rep.add("%s: missing, skipped", path)
			continue
		}
		if exists(orig) {
			if err := copyFile(orig, path); err != nil {
				return rep, err
			}
			rep.add("restored %s", path)
		}
	}
	return rep, nil
}

// workingCopy is the absolute data folder, refused when it is a pristine extraction (patch_config.py:
// a path with "\ia_" or "/ia_" in it).
func workingCopy(dataDir string) (string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if strings.Contains(dir, string(filepath.Separator)+"ia_") || strings.Contains(dir, "/ia_") {
		return "", errors.New("configpatch: refusing to patch a pristine extraction (client/ia_*); use a working copy")
	}
	return dir, nil
}

func run(dataDir string, o Options, dry bool) (*Report, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	dir, err := workingCopy(dataDir)
	if err != nil {
		return nil, err
	}
	rep := &Report{}
	var hits []string
	for _, name := range Packages {
		path := filepath.Join(dir, name)
		orig := path + ".orig"
		if !exists(path) && !exists(orig) {
			rep.add("%s: missing, skipped", path)
			continue
		}
		if !exists(orig) && !dry {
			if err := copyFile(path, orig); err != nil {
				return rep, err
			}
			rep.add("saved original as %s", orig)
		}
		src := path
		if exists(orig) {
			src = orig
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return rep, err
		}
		res, err := Package(data, name, o)
		if err != nil {
			return rep, err
		}
		hits = append(hits, res.AFKHits...)
		for _, c := range res.Changes {
			rep.add("  %s", c)
		}
		switch {
		case res.Data == nil:
			rep.add("%s: nothing to change", path)
		case dry:
			rep.add("%s: %d changes in %d entries (dry run, not written)", path, len(res.Changes), res.Entries)
		default:
			if err := writeFile(path, res.Data); err != nil {
				return rep, err
			}
			rep.add("patched %s: %d changes in %d entries", path, len(res.Changes), res.Entries)
		}
	}
	for _, name := range []string{afkName, afkDisconnectName} {
		if o.afkWant(name) == 0 {
			continue
		}
		n := 0
		for _, h := range hits {
			if h == name {
				n++
			}
		}
		if n != 2 {
			return rep, fmt.Errorf("configpatch: %s: found %d time(s), expected 2 (Config.package text and Config_Ship.package binary app properties)", name, n)
		}
	}
	return rep, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// copyFile copies src to dst with src's modification time (Python's shutil.copy2).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := writeFile(dst, data); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

// writeFile writes data to path through a temporary file in the same folder and a rename.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}
