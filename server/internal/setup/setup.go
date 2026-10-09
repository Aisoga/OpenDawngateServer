// Package setup is the player and operator setup of the release: what
// re/scripts/setup_all.py does for a player or an operator, in Go on top of the setup's subpackages (archive, certs,
// exepatch, hostcopy, configpatch, gamecontent), so that Start-Server.exe and Start-Client.exe need neither
// Python nor Go nor Visual Studio. Every step checks its result first, is skipped when it is current, repaired when it
// isn't, and reports one line ([ok], [done], [MISSING], [STALE], [FAILED], [note], [skipped]); Check only reports.
//
// The setup folder (Layout) holds, beside the release:
//
//	client\Dawngate        the player copy: extracted from Dawngate.zip, Dawngate.exe patched (patch_client.py's local
//	                       profile for a loopback server, the lan profile for a server on another machine) with the
//	                       server's CA, the config packages pointing at the server (patch_config.py --host)
//	host\Dawngate          the match-host copy: extracted, exe patched (local profile, 127.0.0.1, checked by
//	                       exepatch.HostKeys as setup_all.py does), config packages for 127.0.0.1, dgsim.dll as
//	                       PhysXLoader.dll, dgsim.ini and the Lua probes (hostcopy)
//	certs\<address>        the redirector's certificates for that server address (CN = address), made per install
//	data                   accounts.json (dgserver), content.json (the player content, from the client copy's game data)
//	logs                   dgserver's logs and the match hosts' folders
//	servers\<address>      the CA modulus of another machine's server, trusted on first use (Start-Client)
//
// Differences from setup_all.py on purpose: no client\ia_Dawngate (each copy is extracted from the archive, which
// costs one extraction more and saves a 1.5 GB pristine folder; a lost file is restored from the archive), no tool
// checks or builds (the release ships the binaries), one certificate folder per server address (no --new-certs:
// switching the address back and forth needs no new set), and the content is built from the game folder, not
// through Python's snap and AssetData dump. The resulting copies are byte for byte setup_all.py's for the same CA
// (re/scripts/test_p3b.py compares them).
package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dawngate/server/internal/setup/archive"
	"dawngate/server/internal/setup/certs"
	"dawngate/server/internal/setup/exepatch"
	"dawngate/server/internal/setup/hostcopy"
)

// Local is the default server address: everything on this machine.
const Local = "127.0.0.1"

// Layout names the folders of a setup folder (the package comment).
type Layout struct{ Root string }

// ClientCopy is the player copy's Dawngate folder.
func (l Layout) ClientCopy() string { return filepath.Join(l.Root, "client", archive.Top) }

// HostCopy is the match-host copy's Dawngate folder (dgserver --gms-dir).
func (l Layout) HostCopy() string { return filepath.Join(l.Root, "host", archive.Top) }

// Certs is the certificate folder for a server address (dgserver --certs).
func (l Layout) Certs(address string) string { return filepath.Join(l.Root, "certs", address) }

// Data is dgserver's data folder (--data).
func (l Layout) Data() string { return filepath.Join(l.Root, "data") }

// Content is the content file (dgserver's default <data>\content.json).
func (l Layout) Content() string { return filepath.Join(l.Data(), "content.json") }

// Logs is the log folder (dgserver --log; the hosts' folders below it).
func (l Layout) Logs() string { return filepath.Join(l.Root, "logs") }

// TrustedCA is where Start-Client keeps another machine's CA modulus once the player confirmed it.
func (l Layout) TrustedCA(server string) string {
	return filepath.Join(l.Root, "servers", server, certs.CAModulusFile)
}

// Status is a step's result.
type Status string

// The step results, as setup_all.py prints them.
const (
	OK      Status = "ok"
	Done    Status = "done"
	Note    Status = "note"
	Skipped Status = "skipped"
	Missing Status = "MISSING"
	Stale   Status = "STALE"
	Failed  Status = "FAILED"
)

// Bad reports a status that leaves the setup incomplete.
func (s Status) Bad() bool { return s == Missing || s == Stale || s == Failed }

// Line is one reported step.
type Line struct {
	Status Status
	Step   string
	Detail string
}

func (l Line) String() string {
	return fmt.Sprintf("  %-10s %-14s %s", "["+string(l.Status)+"]", l.Step, l.Detail)
}

// ErrIncomplete: a step failed or (with Check) something is missing or stale; Setup.Bad lists what.
var ErrIncomplete = errors.New("the setup is incomplete")

// ErrNoZip is what FindZip returns when the archive isn't there (the steps that need it then report MISSING).
var ErrNoZip = errors.New("Dawngate.zip not found")

// Setup runs the steps for one setup folder. The zero value of every field but Layout works (no output, the archive
// is never found, the build-854 manifest).
type Setup struct {
	Layout
	Out   io.Writer // the step lines and the progress (nil: none)
	Live  bool      // Out is a console: the progress rewrites one line instead of printing new ones
	Check bool      // only report; write nothing
	// FindZip returns Dawngate.zip's path when a step needs the archive (asked once, lazily: a complete setup never
	// needs it). ErrNoZip (or any error) makes those steps report MISSING with the error's text.
	FindZip func() (string, error)
	// HostFiles are dgsim.dll and the Lua probes the host copy gets (the release's host\ folder).
	HostFiles hostcopy.Files
	// Manifest is the build the archive must hold (nil: archive.Build854; tests use their own).
	Manifest *archive.Manifest

	lines   []Line
	zip     string
	zipErr  error
	zipDone bool
	prog    progress
}

func (s *Setup) out() io.Writer {
	if s.Out == nil {
		return io.Discard
	}
	return s.Out
}

func (s *Setup) manifest() *archive.Manifest {
	if s.Manifest == nil {
		s.Manifest = archive.Build854()
	}
	return s.Manifest
}

// Lines are the steps reported so far.
func (s *Setup) Lines() []Line { return append([]Line(nil), s.lines...) }

// Bad are the reported steps that leave the setup incomplete.
func (s *Setup) Bad() []Line {
	var out []Line
	for _, l := range s.lines {
		if l.Status.Bad() {
			out = append(out, l)
		}
	}
	return out
}

// Failed reports whether a step failed (not only missing or stale).
func (s *Setup) Failed() bool {
	for _, l := range s.lines {
		if l.Status == Failed {
			return true
		}
	}
	return false
}

func (s *Setup) result() error {
	if len(s.Bad()) > 0 {
		return ErrIncomplete
	}
	return nil
}

func (s *Setup) report(st Status, step, detail string) bool {
	s.prog.end(s.out())
	l := Line{st, step, detail}
	s.lines = append(s.lines, l)
	fmt.Fprintln(s.out(), l.String())
	return !st.Bad()
}

// needs reports step as missing when a precondition failed (setup_all.py's needs).
func (s *Setup) needs(step string, what ...string) bool {
	return s.report(Missing, step, "needs "+strings.Join(what, ", "))
}

func (s *Setup) say(format string, a ...any) {
	s.prog.end(s.out())
	fmt.Fprintf(s.out(), "    "+format+"\n", a...)
}

// show is a path for the report: relative to the setup folder when it is inside it.
func (s *Setup) show(p string) string {
	if rel, err := filepath.Rel(s.Root, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return p
}

func (s *Setup) zipPath() (string, error) {
	if !s.zipDone {
		s.zipDone = true
		if s.FindZip == nil {
			s.zipErr = ErrNoZip
		} else {
			s.zip, s.zipErr = s.FindZip()
		}
	}
	return s.zip, s.zipErr
}

// ClientOptions are the exe patch options of a client copy for a server address: patch_client.py's local profile for
// a loopback address (binds narrowed to it), its lan profile for an address of our own network (binds as shipped). A
// public, wildcard or multicast address, a name or a loopback address longer than RakNet's literal is refused.
func ClientOptions(server string) (exepatch.Options, error) {
	ip := net.ParseIP(server).To4()
	if ip == nil || strings.Count(server, ".") != 3 {
		return exepatch.Options{}, fmt.Errorf("the server address must be an IPv4 address such as 127.0.0.1 or 192.168.1.20, not %q", server)
	}
	o := exepatch.Options{Profile: exepatch.ProfileLocal, ServerHost: server}
	if !ip.IsLoopback() {
		if _, err := exepatch.LANAddress(server, "the server address"); err != nil {
			return o, err
		}
		o.Profile = exepatch.ProfileLAN
	}
	if _, _, err := exepatch.ResolveProfile(o); err != nil {
		return o, err
	}
	return o, nil
}

// hostOptions are the match-host copy's exe patch options (setup_all.py: the local profile for 127.0.0.1 and the
// local CA; the host binds DGGMS_BIND itself, whatever the server's address).
var hostOptions = exepatch.Options{Profile: exepatch.ProfileLocal, ServerHost: Local}

// ServerOptions are Server's.
type ServerOptions struct {
	Address   string // the server's address: Local, another loopback address, or this machine's LAN address
	NoContent bool   // leave out the content file (new accounts then own no items)
}

// Server sets up (or checks) everything Start-Server.exe runs: the certificates for the address, the player copy for
// it, the match-host copy with dgsim.dll, and the content file, in setup_all.py's order. It returns ErrIncomplete when
// a step is left undone (Bad lists them).
func (s *Setup) Server(o ServerOptions) error {
	opts, err := ClientOptions(o.Address)
	if err != nil {
		s.report(Failed, "address", err.Error())
		return s.result()
	}
	s.room([]string{s.ClientCopy(), s.HostCopy()})
	modulus := s.stepCerts(o.Address)
	if s.ensureCopy("client copy", s.ClientCopy(), clientExempt) || (s.Check && isDir(s.ClientCopy())) {
		if modulus != nil {
			s.stepExe("client exe", s.ClientCopy(), modulus, opts, nil)
		} else {
			s.needs("client exe", "certificates")
		}
		s.stepConfig("client config", s.ClientCopy(), o.Address)
	}
	host := s.ensureCopy("host copy", s.HostCopy(), hostExempt) || (s.Check && isDir(s.HostCopy()))
	patched := false
	if host {
		exe := false
		if modulus != nil {
			exe = s.stepExe("host exe", s.HostCopy(), modulus, hostOptions, exepatch.HostKeys)
		} else {
			s.needs("host exe", "certificates")
		}
		config := s.stepConfig("host config", s.HostCopy(), Local)
		patched = exe && config
	}
	if patched || (host && s.Check) {
		s.stepHostDLL()
	} else {
		s.needs("host DLL", "a patched host copy")
	}
	if o.NoContent {
		s.report(Skipped, "content", "left out: new accounts own no items")
	} else {
		s.stepContent()
	}
	return s.result()
}

// Client sets up (or checks) the player copy for a server: the copy, its exe patched for the server address with
// the server's CA modulus ca (128 bytes), its config packages pointing at the server. Start-Client.exe runs it before
// every start (a current copy costs a second).
func (s *Setup) Client(server string, ca []byte) error {
	opts, err := ClientOptions(server)
	if err != nil {
		s.report(Failed, "address", err.Error())
		return s.result()
	}
	if len(ca) != 128 {
		s.report(Failed, "server CA", fmt.Sprintf("a CA modulus has 128 bytes, this one %d", len(ca)))
		return s.result()
	}
	s.room([]string{s.ClientCopy()})
	if s.ensureCopy("client copy", s.ClientCopy(), clientExempt) || (s.Check && isDir(s.ClientCopy())) {
		s.stepExe("client exe", s.ClientCopy(), ca, opts, nil)
		s.stepConfig("client config", s.ClientCopy(), server)
	}
	return s.result()
}

// --- the steps

// clientExempt and hostExempt are the files a copy's patchers change (sizes not checked) with the name of the
// untouched file kept beside each: a file missing together with its backup is restored from the archive
// (setup_all.py's base_of restores it from client\ia_Dawngate).
var (
	clientExempt = map[string]string{
		"game/Dawngate.exe":             "game/Dawngate.exe" + exepatch.OrigSuffix,
		"game/Data/Config.package":      "game/Data/Config.package.orig",
		"game/Data/Config_Ship.package": "game/Data/Config_Ship.package.orig",
		"game/Data/Config_Pack.package": "game/Data/Config_Pack.package.orig",
	}
	hostExempt = func() map[string]string {
		m := map[string]string{"game/" + hostcopy.Loader: "game/" + hostcopy.LoaderOrig}
		for k, v := range clientExempt {
			m[k] = v
		}
		return m
	}()
)

// room checks before anything is extracted that the copies still to make fit on the drive (archive.Extract checks
// each one again).
func (s *Setup) room(copies []string) bool {
	if s.Check {
		return true
	}
	var need int64
	for _, c := range copies {
		if !isDir(c) {
			need += s.manifest().TotalSize()
		}
	}
	if need == 0 {
		return true
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return s.report(Failed, "disk space", err.Error())
	}
	free, err := archive.FreeBytes(s.Root)
	if err != nil {
		return true // the system doesn't tell; Extract will fail clearly if it runs out
	}
	if free < uint64(need)+archive.FreeMargin {
		return s.report(Failed, "disk space", fmt.Sprintf("not enough free space for the setup in %s: %.1f GB free, "+
			"%.1f GB needed (%d copies of the game, 1.5 GB each, and 1 GB to spare); free some space or put the setup "+
			"folder on another drive", s.Root, float64(free)/(1<<30), float64(uint64(need)+archive.FreeMargin)/(1<<30),
			need/s.manifest().TotalSize()))
	}
	return true
}

// ensureCopy makes sure the Dawngate folder dawngate holds every file of the build: extracted from the archive when
// it is missing, files that are missing or of another size restored from the archive (the patched files and their
// backups excepted). True when the copy is complete.
func (s *Setup) ensureCopy(step, dawngate string, exempt map[string]string) bool {
	m := s.manifest()
	if !isDir(dawngate) {
		if s.Check {
			return s.report(Missing, step, fmt.Sprintf("%s (extracted from Dawngate.zip, %.1f GB)", s.show(dawngate),
				float64(m.TotalSize())/(1<<30)))
		}
		zp, err := s.zipPath()
		if err != nil {
			return s.report(Missing, step, fmt.Sprintf("%s: %v", s.show(dawngate), err))
		}
		return s.extract(step, zp, dawngate)
	}
	skip := make([]string, 0, len(exempt))
	for k := range exempt {
		skip = append(skip, k)
	}
	bad, err := archive.CheckCopy(dawngate, m, skip)
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	for live, backup := range exempt {
		if !isFile(filepath.Join(dawngate, filepath.FromSlash(live))) && !isFile(filepath.Join(dawngate, filepath.FromSlash(backup))) {
			bad = append(bad, live)
		}
	}
	sort.Strings(bad)
	if len(bad) == 0 {
		return s.report(OK, step, s.show(dawngate)+" (every file of the build is there)")
	}
	what := fmt.Sprintf("%d file(s) of the build missing or of another size (%s%s)", len(bad), bad[0], more(len(bad)))
	if s.Check {
		return s.report(Stale, step, s.show(dawngate)+": "+what)
	}
	zp, err := s.zipPath()
	if err != nil {
		return s.report(Missing, step, fmt.Sprintf("%s: %s; restoring them needs the archive: %v", s.show(dawngate), what, err))
	}
	if err := archive.RestoreFiles(zp, dawngate, m, bad); err != nil {
		return s.report(Failed, step, fmt.Sprintf("%s: %v (if it keeps failing, delete %s and start again: it is "+
			"extracted anew)", s.show(dawngate), err, dawngate))
	}
	return s.report(Done, step, fmt.Sprintf("%s: %d file(s) restored from %s (%s%s)", s.show(dawngate), len(bad), zp,
		bad[0], more(len(bad))))
}

// extract unpacks the archive into dawngate's parent folder (through a scratch folder when the parent exists).
func (s *Setup) extract(step, zp, dawngate string) bool {
	parent := filepath.Dir(dawngate)
	dest := parent
	if exists(parent) {
		dest = parent + ".extract"
		if err := os.RemoveAll(dest); err != nil { // left by an interrupted run (only that folder: named after ours)
			return s.report(Failed, step, err.Error())
		}
	}
	s.say("extracting %s into %s (1.5 GB, about half a minute) ...", zp, s.show(dawngate))
	res, err := archive.Extract(zp, dest, s.manifest(), s.prog.callback(s.out(), s.Live))
	if err != nil {
		var ve *archive.VerifyError
		if errors.As(err, &ve) {
			os.RemoveAll(ve.Dir) // nothing to look at for a player; the check's first line says what was wrong
			return s.report(Failed, step, fmt.Sprintf("%s is not the Dawngate.zip of archive.org (build 1.0.903.854): "+
				"%s%s; download it again from https://archive.org/details/Dawngate", zp, ve.Problems[0], more(len(ve.Problems))))
		}
		return s.report(Failed, step, err.Error())
	}
	if dest != parent {
		if err := os.Rename(filepath.Join(dest, archive.Top), dawngate); err != nil {
			return s.report(Failed, step, err.Error())
		}
		os.Remove(dest)
	}
	note := ""
	if !res.KnownDownload {
		note = fmt.Sprintf("; the archive is not the known download (SHA-256 %s...), its files passed", res.ZipSHA256[:16])
	}
	return s.report(Done, step, fmt.Sprintf("%s extracted from %s and checked (%d files, SHA-256; %.0f s)%s",
		s.show(dawngate), zp, s.manifest().Len(), res.Took.Seconds(), note))
}

// stepCerts checks (or makes) the certificate set for the address; it returns the CA modulus, nil when there is no
// usable set.
func (s *Setup) stepCerts(address string) []byte {
	const step = "certificates"
	dir := s.Certs(address)
	info, err := certs.Check(dir, address)
	switch {
	case err == nil:
	case errors.Is(err, certs.ErrNoSet):
		if s.Check {
			s.report(Missing, step, fmt.Sprintf("%s (made for this install, CN %s)", s.show(dir), address))
			return nil
		}
		set, err := certs.Make(dir, address)
		if err != nil {
			s.report(Failed, step, err.Error())
			return nil
		}
		s.report(Done, step, fmt.Sprintf("%s made (CN %s, CA %s)", s.show(dir), address, certs.Fingerprint(set.CAModulus)))
		return set.CAModulus
	default:
		s.report(Failed, step, fmt.Sprintf("%v; delete the folder %s to make a new set (copies on this machine are "+
			"patched again by themselves; players on other machines confirm the new CA once)", err, dir))
		return nil
	}
	modulus, err := os.ReadFile(filepath.Join(dir, certs.CAModulusFile))
	if err != nil {
		s.report(Failed, step, err.Error())
		return nil
	}
	s.report(OK, step, fmt.Sprintf("%s (CN %s, CA %s)", s.show(dir), info.CN, info.CAModulusSHA256))
	return modulus
}

// stepExe patches a copy's Dawngate.exe (setup_all.py's step_exe): current means byte for byte what the patcher makes
// from the untouched exe, or with keys only those Describe keys (the host copy).
func (s *Setup) stepExe(step, copy string, modulus []byte, o exepatch.Options, keys []string) bool {
	exe := filepath.Join(copy, "game", "Dawngate.exe")
	fo := exepatch.FileOptions{Options: o, BaseSHA256: s.untouched("game/Dawngate.exe")}
	why, err := exepatch.State(exe, modulus, fo, keys)
	if err != nil {
		if s.Check {
			return s.report(Stale, step, err.Error())
		}
		return s.report(Failed, step, fmt.Sprintf("%v (delete %s and start again: it is extracted anew)", err, copy))
	}
	if why == "" {
		return s.report(OK, step, s.exeLine(exe, keys != nil))
	}
	if s.Check {
		return s.report(Stale, step, s.show(exe)+": "+why)
	}
	if _, err := exepatch.PatchFile(exe, modulus, fo); err != nil {
		return s.report(Failed, step, err.Error())
	}
	return s.report(Done, step, s.exeLine(exe, keys != nil)+" (was: "+why+")")
}

func (s *Setup) exeLine(exe string, host bool) string {
	b, err := os.ReadFile(exe)
	if err != nil {
		return s.show(exe)
	}
	d, err := exepatch.Describe(b)
	if err != nil {
		return s.show(exe)
	}
	note := ""
	if host {
		note = " (host copy: the host narrows its binds itself)"
	}
	return fmt.Sprintf("%s: redirector %s, binds %s, CA %s%s", s.show(exe), d["redirector"], d["bind"], d["ca_sha256"], note)
}

// untouched is the SHA-256 the manifest gives a copy path.
func (s *Setup) untouched(rel string) string {
	f, _ := s.manifest().Lookup(rel)
	return f.SHA256
}

// base is the untouched file a patcher starts from (<file>.orig, or the file before its first patch), checked
// against the manifest (setup_all.py's base_of; ensureCopy restored a file missing with its backup).
func (s *Setup) base(copy, rel string) (string, error) {
	live := filepath.Join(copy, filepath.FromSlash(rel))
	base := live + ".orig"
	if !isFile(base) {
		base = live
	}
	digest, err := archive.HashFile(base, nil)
	if err != nil {
		return "", err
	}
	if digest != s.untouched(rel) {
		return "", fmt.Errorf("%s is not the untouched build-854 file; the patchers start from it: delete %s and start "+
			"again (it is extracted anew)", s.show(base), copy)
	}
	return base, nil
}

// stepHostDLL installs dgsim.dll, dgsim.ini and the Lua probes into the host copy (setup_all.py's step_host_dll).
func (s *Setup) stepHostDLL() bool {
	const step = "host DLL"
	game := filepath.Join(s.HostCopy(), "game")
	if s.HostFiles.FS == nil {
		return s.report(Missing, step, "no dgsim.dll and Lua probes to install (the release's host folder)")
	}
	files := s.HostFiles
	files.LoaderSHA256 = s.untouched("game/" + hostcopy.Loader)
	problems, err := hostcopy.Check(s.HostCopy(), files)
	if errors.Is(err, hostcopy.ErrNotTheGamesLoader) {
		return s.report(Failed, step, fmt.Sprintf("%s: %v; delete %s and start again (it is extracted anew)",
			s.show(game), err, s.HostCopy()))
	}
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	if len(problems) == 0 {
		return s.report(OK, step, fmt.Sprintf("%s: PhysXLoader.dll = dgsim.dll (%s), dgsim.ini, Lua probes",
			s.show(game), s.dllHash()))
	}
	if s.Check {
		return s.report(Stale, step, s.show(game)+": "+strings.Join(problems, "; "))
	}
	res, err := hostcopy.Install(s.HostCopy(), files, hostcopy.Options{})
	if err != nil {
		return s.report(Failed, step, err.Error())
	}
	if left, err := hostcopy.Check(s.HostCopy(), files); err != nil || len(left) > 0 {
		return s.report(Failed, step, fmt.Sprintf("installed, but still: %s %v", strings.Join(left, "; "), err))
	}
	return s.report(Done, step, fmt.Sprintf("%s: dgsim.dll %s installed with %d Lua probes (was: %s)", s.show(game),
		res.DLLSHA256[:16], len(res.Lua), strings.Join(problems, "; ")))
}

func (s *Setup) dllHash() string {
	if s.HostFiles.FS == nil {
		return "?"
	}
	b, err := fs.ReadFile(s.HostFiles.FS, s.HostFiles.DLL)
	if err != nil {
		return "?"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}

// --- small helpers

func more(n int) string {
	if n > 1 {
		return fmt.Sprintf(", +%d more", n-1)
	}
	return ""
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }

// progress prints the slow steps' progress: on a console one line rewritten in place, else a line per quarter.
type progress struct {
	open  bool // a live line is on the screen
	phase string
	last  int // the last percentage printed
	t0    time.Time
}

func (p *progress) callback(out io.Writer, live bool) archive.Progress {
	return func(phase string, done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if phase != p.phase {
			p.end(out)
			p.phase, p.last, p.t0 = phase, -1, time.Now()
		}
		step := 25
		if live {
			step = 1
		}
		if pct/step == p.last/step && pct != 100 || pct == p.last {
			return
		}
		p.last = pct
		if live {
			fmt.Fprintf(out, "\r    %-20s %3d%%  (%d of %d MB)  ", phase, pct, done>>20, total>>20)
			p.open = true
			return
		}
		fmt.Fprintf(out, "    %-20s %3d%%\n", phase, pct)
	}
}

func (p *progress) end(out io.Writer) {
	if p.open {
		fmt.Fprintln(out)
		p.open = false
	}
	p.phase = ""
}
