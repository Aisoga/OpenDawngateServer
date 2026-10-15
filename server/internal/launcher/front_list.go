package launcher

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/setup/snap"
	"dawngate/server/internal/tunnel"
)

// The server list the login screen's picker shows (client_ui/serverpicker/ServerPicker.as). The movie can read only
// files of the game's mounted archives (no HTTP), so the front writes the list into an added archive of the player
// copy before the game starts, with each server's status from a short probe of its gateway. The game reads an
// archive's index once, at its start, but an entry's bytes whenever a movie loads it, so the front writes the list
// again in place (the same layout) while the login screen shows (refreshLoop: every ListRefresh, and at once after
// an add, a remove, a refused connect and the return to the login screen), and the picker reads it again every few
// seconds.
//
// The file (UTF-8, LF, tab-separated fields):
//
//	dawngate-servers	1
//	preselect	<tag>     the server Start-Client was started for (empty: none)
//	last	<tag>          the server a login went to last (empty: none yet)
//	server	<tag>	<name>	<address>	<gateway port>	<status>	<ping ms>	<key>	<trusted key>[	fixed]
//	end
//
// status is ok, new (it answers, no key trusted yet: trusted at the first login), changed (another key than the
// trusted one: a login is refused), unreachable, no_gateway (something answers, but no Dawngate tunnel) or unknown;
// for This PC (tag pc, the tenth field fixed: the first line, never removed) while nothing answers there also local
// (it starts at the login), setup (its first setup runs; the ping field holds the percentage), starting or failed.
// key is the fingerprint of the key the server showed (empty when it wasn't reached), trusted the one trusted for it
// (empty: none yet). The last line, end, tells the picker that it read the whole list: the front writes the list
// again while the login screen shows (refreshLoop), and the picker reads it again every few seconds.
const (
	// ListArchive is the list's archive in the player copy; ListPath the list's path inside it, which the movie
	// loads as LoginScreen/dgservers.json (the file opener keeps the .json extension).
	ListArchive = "game/Data/zz_ServerList.snap"
	ListPath    = "UI/Scaleform/LoginScreen/dgservers.json"
	listMagic   = "dawngate-servers\t1"
	listFixed   = "fixed"
)

// serverStatus is a server with its probe's result.
type serverStatus struct {
	Bookmark
	Reachable bool
	PingMS    int64
	// Key is the gateway key against the trusted one: ok, new, changed, unknown (not reached).
	Key     string
	Problem string // unreachable, no_gateway
	// Served and Trusted are the fingerprints of the key the gateway showed and of the trusted one ("": none).
	Served, Trusted string
	// Local is This PC's state while no server answers there (local, setup, starting, failed; thispc.go): no probe
	Local string
}

// status is the list's word for a server's state.
func (s serverStatus) status() string {
	switch {
	case s.Local != "":
		return s.Local
	case s.Reachable:
		return s.Key
	case s.Problem != "":
		return s.Problem
	}
	return "unknown"
}

// probeResult is a probe of a server's gateway.
type probeResult struct {
	at        time.Time
	reachable bool
	ping      time.Duration
	pin       tunnel.Pin
	problem   *frontError
}

// probeFresh is how long a probe's result is reused.
const probeFresh = 3 * time.Second

// probe probes a server's gateway (a TLS handshake without the pin; reused for probeFresh).
func (f *Front) probe(b Bookmark, timeout time.Duration) probeResult {
	key := b.Gateway()
	f.probeMu.Lock()
	if r, ok := f.probes[key]; ok && time.Since(r.at) < probeFresh {
		f.probeMu.Unlock()
		return r
	}
	f.probeMu.Unlock()
	start := time.Now()
	pin, err := tunnel.ProbePin(key, timeout, f.dialerFor(b.Address))
	r := probeResult{at: time.Now(), reachable: err == nil, ping: time.Since(start), pin: pin}
	if err != nil {
		r.problem = f.classifyDial(b, err, timeout)
	}
	f.probeMu.Lock()
	f.probes[key] = r
	f.probeMu.Unlock()
	return r
}

func (f *Front) status(b Bookmark, r probeResult) serverStatus {
	st := serverStatus{Bookmark: b, Reachable: r.reachable, Key: "unknown"}
	if r.reachable {
		st.PingMS = r.ping.Milliseconds()
		var known tunnel.Pin
		st.Key, known = f.pinState(b, r.pin)
		st.Served = r.pin.Fingerprint()
		if !known.IsZero() {
			st.Trusted = known.Fingerprint()
		}
	} else if known, ok := f.trustedPin(b); ok {
		st.Trusted = known.Fingerprint()
	}
	if r.problem != nil {
		st.Problem = problemName(r.problem.code)
	}
	return st
}

func problemName(code uint16) string {
	switch code {
	case FrontErrUnreachable:
		return "unreachable"
	case FrontErrNoGateway:
		return "no_gateway"
	}
	return "error"
}

// statuses probes every server of the list at once.
func (f *Front) statuses() []serverStatus {
	if f.ThisPC != nil {
		f.ThisPC.refresh() // This PC: this folder's Start-Server while it runs, else Start-Client's own
	}
	bms := f.Bookmarks.List()
	out := make([]serverStatus, len(bms))
	var wg sync.WaitGroup
	for i, b := range bms {
		if b.Tag == ThisPCTag && f.ThisPC != nil {
			if st, pct := f.ThisPC.status(); st != "" { // nothing answers there yet: no probe
				out[i] = serverStatus{Bookmark: b, Local: st, PingMS: int64(pct), Key: "unknown"}
				continue
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = f.status(b, f.probe(b, f.ProbeTimeout))
		}()
	}
	wg.Wait()
	return out
}

// listField keeps a field on its line and in its column.
func listField(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// ServerList is the list file's text: every server probed now, preselect the server Start-Client was started for.
func (f *Front) ServerList() []byte {
	var sb strings.Builder
	sb.WriteString(listMagic + "\n")
	pre := ""
	if b, ok := f.Bookmarks.Find(f.Default); ok && f.Default != "" {
		pre = b.Tag
	}
	last := ""
	if b, ok := f.Bookmarks.Last(); ok && b.LastUsed != "" {
		last = b.Tag
	}
	fmt.Fprintf(&sb, "preselect\t%s\nlast\t%s\n", pre, last)
	for _, s := range f.statuses() {
		fmt.Fprintf(&sb, "server\t%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s", s.Tag, listField(s.Name), s.Address, s.GatewayPort,
			s.status(), s.PingMS, s.Served, s.Trusted)
		if s.Tag == ThisPCTag && f.ThisPC != nil {
			sb.WriteString("\t" + listFixed)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("end\n")
	return []byte(sb.String())
}

// WriteServerList writes the list's archive into the player copy (the folder that holds game\Data); it remembers
// the copy, so a change through the login writes the list again. An empty copy only forgets it.
func (f *Front) WriteServerList(copy string) error {
	f.listMu.Lock()
	f.listCopy = copy
	f.listMu.Unlock()
	if copy == "" {
		return nil
	}
	return f.writeList(copy)
}

// listSize is the list file's fixed size in its archive: the text padded with line ends, Snappy literals only, so
// every list archive has the same layout. The game reads an archive's index once, when it mounts the
// archives at its start, and reads an entry's bytes from the file at that index's offset and size whenever a movie
// loads it: an archive written again with another layout while the game runs fails the load (FileDepot.cpp 138, the
// picker then shows nothing), one with the same layout gives the new text. So the list a login changed is shown when
// the login screen loads again (after a logout). About 150 servers fit; more are left out.
const listSize = 16 << 10

// listArchive is the list's archive: the text padded to listSize (cut at a line when longer).
func listArchive(text []byte) ([]byte, int) {
	if len(text) > listSize {
		cut := strings.LastIndexByte(string(text[:listSize]), '\n')
		text = text[:cut+1]
	}
	padded := append(append(make([]byte, 0, listSize), text...), strings.Repeat("\n", listSize-len(text))...)
	return snap.Pack([]snap.File{{Name: ListPath, Data: padded, Literals: true}}), strings.Count(string(text), "\nserver\t")
}

func (f *Front) writeList(copy string) error {
	return f.writeListText(copy, f.ServerList(), true)
}

// writeListText writes a list text into the copy's archive; always false skips a text that was written last already.
func (f *Front) writeListText(copy string, text []byte, always bool) error {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	if !always && f.lastList != nil && string(f.lastList) == string(text) {
		return nil
	}
	data, n := listArchive(text)
	path := filepath.Join(copy, filepath.FromSlash(ListArchive))
	// the same layout: written in place (the game may have the file mounted); else a new file
	var err error
	if st, serr := os.Stat(path); serr == nil && st.Size() == int64(len(data)) {
		var w *os.File
		if w, err = os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			_, err = w.WriteAt(data, 0)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
		}
	} else {
		tmp := path + ".partial"
		if err = os.WriteFile(tmp, data, 0o644); err == nil {
			err = os.Rename(tmp, path)
		}
		if err != nil {
			os.Remove(tmp)
		}
	}
	if err != nil {
		f.lastList = nil
		return fmt.Errorf("the server list %s: %v", path, err)
	}
	f.lastList = text
	// a refresh that changed only the pings isn't logged
	if states := listSummary(text, false); always || states != f.lastStates {
		f.lastStates = states
		f.logf("the server list for the login screen: %s (%d servers: %s)", path, n, listSummary(text, true))
	}
	return nil
}

// listSummary is the list's servers and states for the log: "a ok 2ms, b unreachable" (pings: with the pings).
func listSummary(text []byte, pings bool) string {
	var out []string
	for _, line := range strings.Split(string(text), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 7 && f[0] == "server" {
			s := f[1] + " " + f[5]
			if pings && (f[5] == "ok" || f[5] == "new" || f[5] == "changed") {
				s += " " + f[6] + "ms"
			}
			if pings && f[5] == thisPCSetup {
				s += " " + f[6] + "%"
			}
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}

// setAtLogin records whether the login screen shows: true at the redirector request the client makes at its start
// and after every logout, false once a login went through to a server. Back at the login screen the list is
// written again at once.
func (f *Front) setAtLogin(on bool) {
	f.listMu.Lock()
	was := f.atLogin
	f.atLogin = on
	f.listMu.Unlock()
	if on && !was {
		f.wantList()
	}
}

// wantList asks refreshLoop for a new list now.
func (f *Front) wantList() {
	if f.refresh == nil {
		return
	}
	select {
	case f.refresh <- struct{}{}:
	default:
	}
}

// recordProbe keeps a probe a login made, so the list shows what that login found.
func (f *Front) recordProbe(b Bookmark, r probeResult) {
	f.probeMu.Lock()
	if f.probes != nil {
		f.probes[b.Gateway()] = r
	}
	f.probeMu.Unlock()
}

// refreshLoop writes the list again (each server probed again) every ListRefresh while the login screen shows, and
// at once when asked (wantList); a text that didn't change isn't written. The picker reads the file again every few
// seconds, so a server that goes offline or comes back shows while the login screen is up.
func (f *Front) refreshLoop() {
	defer f.wg.Done()
	var tick <-chan time.Time
	if f.ListRefresh > 0 {
		t := time.NewTicker(f.ListRefresh)
		defer t.Stop()
		tick = t.C
	}
	for {
		asked := false
		select {
		case <-f.done:
			return
		case <-tick:
		case <-f.refresh:
			asked = true
		}
		f.listMu.Lock()
		copy, atLogin := f.listCopy, f.atLogin
		f.listMu.Unlock()
		if copy == "" || (!asked && !atLogin) {
			continue
		}
		if err := f.writeListText(copy, f.ServerList(), false); err != nil {
			f.logf("%v", err)
		}
	}
}

// listChanged writes the list again after a change through the login (the game may hold the archive open: then
// the list shows the change at the next start, written then anyway).
func (f *Front) listChanged() {
	f.listMu.Lock()
	copy := f.listCopy
	f.listMu.Unlock()
	if copy == "" {
		return
	}
	if err := f.writeList(copy); err != nil {
		f.logf("%v (written again at the next start)", err)
	}
}

// RemoveServerList takes a list of an earlier picker-mode start out of a player copy.
func RemoveServerList(copy string) error {
	err := os.Remove(filepath.Join(copy, filepath.FromSlash(ListArchive)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// The login name's picks besides a tag (after the first '#'; '#' is no login-name character):
//
//	<name>#+<address>[:<gateway port>][=<display name>]   add the server (once its gateway answers) and log in there
//	<name>#-<tag>                                         remove the server (answered with FrontErrRemoved)
//
// Tags never start with '+' or '-' (ValidTag).
const (
	pickAdd    = '+'
	pickRemove = '-'
)

// parseAdd reads an add pick's server: address, optional gateway port and display name.
func parseAdd(spec string) (Bookmark, error) {
	addr, name, _ := strings.Cut(spec, "=")
	addr = strings.TrimSpace(addr)
	b := Bookmark{Address: addr, Name: name}
	if h, p, err := net.SplitHostPort(addr); err == nil {
		port, err := strconv.Atoi(p)
		if err != nil {
			return b, fmt.Errorf("the gateway port %q: a port number", p)
		}
		b.Address, b.GatewayPort = h, port
	}
	return checkBookmark(b)
}

// addFromLogin adds the server an add pick names once its gateway answers a probe (its key is then checked by the
// session: a new one confirmed once); a server in the list already is that server.
func (f *Front) addFromLogin(spec string) (Bookmark, error) {
	b, err := parseAdd(spec)
	if err != nil {
		return b, frontErr(FrontErrBadAddress, "add %q: %v", spec, err)
	}
	for _, old := range f.Bookmarks.List() {
		if strings.EqualFold(old.Address, b.Address) {
			f.logf("add %s: in the list already as %s", b.Address, old.Tag)
			return old, nil
		}
	}
	r := f.probe(b, f.ConnectTimeout)
	if !r.reachable {
		return b, r.problem
	}
	added, err := f.Bookmarks.Add(b)
	if err != nil {
		return b, frontErr(FrontErrBadAddress, "add %s: %v", b.Address, err)
	}
	f.logf("added %s (%s, %q) to the server list through the login", added.Tag, added.Gateway(), added.Name)
	f.listChanged()
	return added, nil
}

// removeFromLogin removes the server a remove pick names.
func (f *Front) removeFromLogin(key string) *frontError {
	b, err := f.Bookmarks.Remove(key)
	if errors.Is(err, ErrFixed) {
		return frontErr(FrontErrFixed, "remove %q: This PC is always in the list", key)
	}
	if errors.Is(err, ErrNoBookmark) {
		return frontErr(FrontErrUnknownServer, "remove %q: no such server in the list (tags: %s)", key, strings.Join(f.Bookmarks.sortedTags(), ", "))
	}
	if err != nil {
		return frontErr(FrontErrServer, "remove %q: %v", key, err)
	}
	f.listChanged()
	return frontErr(FrontErrRemoved, "removed %s (%s) from the server list through the login (its trusted key stays in %s)",
		b.Tag, b.Gateway(), TrustedGateway(f.Layout, b.Address))
}
