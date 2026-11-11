package gmshost

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Regions (M27, M28): a server's host machines can stand in other regions (North America beside a European main
// server), and a game runs in the region its players prefer (the Blaze side's choice: a custom game's creator's, the
// vote of a PvP match's humans, ...). Every machine has a region: this machine the server's home region (SetRegions),
// an agent's machine the one the server's configuration gives its name (an agent without one counts as home, as the
// host machines of one network always did); each region has a display name (SetRegionNames). StartIn starts a game in
// a wanted region; Start uses the home region's machines only, and so does every restart. Without a home region
// regions are off and every machine is a candidate, as before.
//
// A game wanted in another region runs at home instead when that region can't take it, and the match says why
// (Match.FellBack): no machine of the region registered (down; version when its last registration was refused for
// another release, MachineRefused), none with the game's map (map, SetMachineMaps), none with a free port (full), its
// launch failed or wasn't answered within RegionLaunchTimeout (failed), or its host ended before gameReady (failed, or
// down when the machine's link was lost): then the game starts once more at home, as PORTSTUCK's retry does. After
// gameReady there is no fallback.

// The reasons a game wanted in a region runs at home (Match.FellBack).
const (
	FellBackDown    = "down"    // no machine of the region registered, or its link lost before gameReady
	FellBackFull    = "full"    // every GMS port of the region's machines taken
	FellBackVersion = "version" // the region's machine was refused for running another release
	FellBackMap     = "map"     // no machine of the region offers the game's map
	FellBackFailed  = "failed"  // its launchHost failed or wasn't answered in time, or its host ended before gameReady
)

// RegionLaunchTimeout is how long a machine of another region may take to answer launchHost before the game starts at
// home instead (Config.LaunchTimeout, 10 s, is for the home region's machines): the creator waits through it.
const RegionLaunchTimeout = 5 * time.Second

// Want is where a game should run (StartIn).
type Want struct {
	Region string // a region alias ("na", "eu"); "" = the home region
}

// regionState is the Manager's region configuration and what it heard of the machines (under Manager.mu).
type regionState struct {
	home     string              // the home region; "" = regions off
	machines map[string]string   // an agent's name -> its region
	maps     map[string][]string // an agent's name -> the maps it offers (absent: not told, every map)
	refused  map[string]string   // an agent's name -> why its last registration was refused (FellBackVersion)
	names    map[string]string   // PREF1: a region's display name (SetRegionNames; absent: DefaultRegionName)
	limit    time.Duration       // RegionLaunchTimeout (tests set it shorter)
}

// NormRegion is a region alias as the server compares it: trimmed and lower case.
func NormRegion(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// SetRegions turns regions on: home is this machine's region (the home region: the default and the fallback), and
// machines gives each agent's region by its name. home "" turns them off.
func (m *Manager) SetRegions(home string, machines map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.home = NormRegion(home)
	m.reg.machines = map[string]string{}
	for name, r := range machines {
		m.reg.machines[name] = NormRegion(r)
	}
}

// LocalRegion is the one region of a server without regions (no home region): its id on the UI channel, in the
// browser's PSAS and in lobby.state (PREF1).
const LocalRegion = "local"

// MaxRegionName is the longest display name of a region, in characters.
const MaxRegionName = 32

// DefaultRegionName is a region's display name when the operator gave it none: the alias in capitals ("eu" -> "EU"),
// LocalRegion's "Local".
func DefaultRegionName(alias string) string {
	if alias == LocalRegion {
		return "Local"
	}
	return strings.ToUpper(alias)
}

// CheckRegionName says what is wrong with a region's display name ("" when nothing): 1-MaxRegionName printable
// characters, no leading or trailing blank.
func CheckRegionName(name string) string {
	n := 0
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "a region's name has a character that isn't printable"
		}
		n++
	}
	switch {
	case n == 0:
		return "a region's name is empty"
	case n > MaxRegionName:
		return fmt.Sprintf("a region's name is over %d characters", MaxRegionName)
	case strings.TrimSpace(name) != name:
		return "a region's name starts or ends with a blank"
	}
	return ""
}

// SetRegionNames gives regions their display names (PREF1, Start-Server.ini's region_names), by alias; a region left
// out keeps DefaultRegionName. It works with regions off too (the name of LocalRegion).
func (m *Manager) SetRegionNames(names map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reg.names = map[string]string{}
	for alias, name := range names {
		m.reg.names[NormRegion(alias)] = name
	}
}

// RegionName is a region's display name (SetRegionNames, else DefaultRegionName).
func (m *Manager) RegionName(alias string) string {
	alias = NormRegion(alias)
	m.mu.Lock()
	name, ok := m.reg.names[alias]
	m.mu.Unlock()
	if ok && name != "" {
		return name
	}
	return DefaultRegionName(alias)
}

// HomeRegion is the home region ("" = regions off).
func (m *Manager) HomeRegion() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reg.home
}

// Regions lists the regions a game can be wanted in: the home region first, then the agents' regions in order. Empty
// when regions are off.
func (m *Manager) Regions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reg.home == "" {
		return nil
	}
	seen := map[string]bool{m.reg.home: true}
	var others []string
	for _, r := range m.reg.machines {
		if r != "" && !seen[r] {
			seen[r] = true
			others = append(others, r)
		}
	}
	sort.Strings(others)
	return append([]string{m.reg.home}, others...)
}

// SetMachineMaps records the maps an agent's machine offers (its levels' names; nil: unknown, every map counts as
// there). A game wanted in its region whose map no machine there offers runs at home (FellBackMap).
func (m *Manager) SetMachineMaps(name string, maps []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reg.maps == nil {
		m.reg.maps = map[string][]string{}
	}
	if maps == nil {
		delete(m.reg.maps, name)
		return
	}
	m.reg.maps[name] = append([]string{}, maps...)
}

// MachineRefused records why an agent's registration was refused (FellBackVersion for another release; "" forgets
// it). While no machine of its region is registered, a game wanted there runs at home with that reason instead of
// FellBackDown.
func (m *Manager) MachineRefused(name, why string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reg.refused == nil {
		m.reg.refused = map[string]string{}
	}
	if why == "" {
		delete(m.reg.refused, name)
		return
	}
	m.reg.refused[name] = why
}

// siteRegion is s's region (m.mu held): this machine's is the home region, an agent's the configured one or else home.
func (m *Manager) siteRegion(s *site) string {
	if s.agent == nil && s.name == LocalSite {
		return m.reg.home
	}
	if r, ok := m.reg.machines[s.name]; ok && r != "" {
		return r
	}
	return m.reg.home
}

// knownRegion: is r a region of this server (m.mu held)?
func (m *Manager) knownRegion(r string) bool {
	if r == "" || m.reg.home == "" {
		return false
	}
	if r == m.reg.home {
		return true
	}
	for _, mr := range m.reg.machines {
		if mr == r {
			return true
		}
	}
	return false
}

// RegionPlacement returns the match's region (where its host runs; "" with regions off), the region its game was wanted
// in ("" when none was asked for) and why it runs elsewhere ("" when it doesn't: a FellBack* reason). Final when StartIn
// has returned, except that a restart before gameReady may move the game home; at gameReady they don't change any more.
func (mt *Match) RegionPlacement() (region, wanted, fellBack string) {
	mt.m.mu.Lock()
	defer mt.m.mu.Unlock()
	return mt.Region, mt.Wanted, mt.FellBack
}

// StartIn is Start for a game wanted in a region (M27; every game since PREF1). The home region, an unknown region or
// regions off: Start, on the home region's machines (and the warm pool). Another region: a machine of that region with the
// game's map and the most free ports, launched within RegionLaunchTimeout; if that region can't take the game it starts
// at home (Start) and the match records why (Match.FellBack, logged as "game N: wanted R, runs on S (H): why").
func (m *Manager) StartIn(game CreateGame, ev Events, want Want) (*Match, error) {
	region := NormRegion(want.Region)
	m.mu.Lock()
	home, known := m.reg.home, m.knownRegion(region)
	m.mu.Unlock()
	if !known || region == home {
		mt, err := m.Start(game, ev)
		if err == nil && home != "" {
			m.mu.Lock()
			mt.Region = m.siteRegion(mt.site)
			if known {
				mt.Wanted = region
			}
			m.mu.Unlock()
		}
		return mt, err
	}
	mt, why, err := m.startAway(game, ev, region)
	if mt != nil || err != nil {
		return mt, err
	}
	mt, err = m.Start(game, ev)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	mt.Region, mt.Wanted, mt.FellBack = m.siteRegion(mt.site), region, why
	site := mt.Site
	m.mu.Unlock()
	m.log.Logf(who, "game %d: wanted %s, runs on %s (%s): %s", game.GameID, region, site, home, why)
	return mt, nil
}

// startAway starts game on a machine of region (not the home region). It returns the match, or (nil, why, nil) when the
// region can't take it (the caller starts it at home), or an error that ends the attempt (ErrClosed).
func (m *Manager) startAway(game CreateGame, ev Events, region string) (*Match, string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, "", ErrClosed
	}
	if m.ln == nil {
		m.mu.Unlock()
		return nil, "", errors.New("gmshost: control listener not started")
	}
	m.expireQuarantines()
	pick, why := m.awaySite(region, game.Level)
	if pick == nil {
		m.mu.Unlock()
		m.log.Logf(who, "game %d: no machine in region %s can take it: %s", game.GameID, region, why)
		return nil, why, nil
	}
	st, port, err := m.takePort(pick, "")
	if err != nil {
		m.mu.Unlock()
		m.log.Logf(who, "game %d: no port on %s in region %s: %v", game.GameID, pick.name, region, err)
		return nil, FellBackFull, nil
	}
	m.nextID++
	mt, env := m.newMatch(m.nextID, st, port, game, ev, false)
	mt.Region, mt.Wanted = region, region
	limit := m.reg.limit
	if limit <= 0 {
		limit = RegionLaunchTimeout
	}
	m.mu.Unlock()
	if err := m.launchWithin(mt, env, limit); err != nil {
		m.log.Logf(who, "match %d (game %d): launch on %s in region %s failed: %v", mt.ID, game.GameID, st.name, region, err)
		return nil, FellBackFailed, nil
	}
	m.log.Logf(who, "match %d: started pid %d%s in region %s for game %d (%s, %s, %d players), GMS %s", mt.ID,
		mt.proc.Pid(), onSite(st), region, game.GameID, game.Mode, game.Level, len(game.Roster), env.Bind)
	go mt.run()
	return mt, "", nil
}

// awaySite picks region's machine for a game on level (m.mu held): of those that offer the map, the one with the most
// free ports (ties to the earlier registration). nil and why when there is none.
func (m *Manager) awaySite(region, level string) (*site, string) {
	var in, withMap []*site
	for _, s := range m.sites {
		if (s.agent != nil || s.name != LocalSite) && m.siteRegion(s) == region {
			in = append(in, s)
		}
	}
	if len(in) == 0 {
		for name, why := range m.reg.refused {
			if m.reg.machines[name] == region && why != "" {
				return nil, why
			}
		}
		return nil, FellBackDown
	}
	for _, s := range in {
		if offers(m.reg.maps[s.name], level) {
			withMap = append(withMap, s)
		}
	}
	if len(withMap) == 0 {
		return nil, FellBackMap
	}
	var best *site
	for _, s := range withMap {
		if f := s.pool.Free(); f > 0 && (best == nil || f > best.pool.Free()) {
			best = s
		}
	}
	if best == nil {
		return nil, FellBackFull
	}
	return best, ""
}

// offers: does a machine with this maps offer (nil: not told) have level?
func offers(maps []string, level string) bool {
	if maps == nil || level == "" {
		return true
	}
	for _, mp := range maps {
		if strings.EqualFold(mp, level) {
			return true
		}
	}
	return false
}

// launchWithin is launch with a time limit of its own: a launcher that doesn't answer within d counts as failed, and
// a process it starts later is killed (its hello finds no match). On failure the match is unregistered and its port
// freed, as in launch.
func (m *Manager) launchWithin(mt *Match, env Env, d time.Duration) error {
	type result struct {
		p   Process
		err error
	}
	ch := make(chan result, 1)
	l := mt.site.launcher
	go func() {
		p, err := l.Launch(env)
		ch <- result{p, err}
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	var r result
	select {
	case r = <-ch:
	case <-t.C:
		go func() {
			if late := <-ch; late.p != nil {
				late.p.Kill()
			}
		}()
		r.err = fmt.Errorf("no answer to launchHost within %v", d)
	}
	if r.err != nil {
		m.mu.Lock()
		delete(m.matches, mt.ID)
		m.mu.Unlock()
		mt.site.pool.Release(mt.Port)
		close(mt.done) // a hello that came meanwhile is turned away
		m.wg.Done()
		return r.err
	}
	mt.proc = r.p
	return nil
}

// awayFault starts a game whose host in another region ended before gameReady once more at home (on the match's
// goroutine, like PORTSTUCK's retry, and only once: o.Retries): not if the server ended it (stopped), nor after
// RetryWindow. The reason is FellBackDown when the machine's link was lost (exit.err), else FellBackFailed. It reports
// whether the game was started again.
func (m *Manager) awayFault(mt *Match, o Outcome, exit exitInfo, stopped bool, since time.Time) bool {
	if o.Ready || stopped || o.Retries > 0 || time.Since(since) > m.cfg.RetryWindow {
		return false
	}
	m.mu.Lock()
	away := m.reg.home != "" && mt.Wanted != "" && mt.Wanted != m.reg.home && m.siteRegion(mt.site) == mt.Wanted
	home, wanted := m.reg.home, mt.Wanted
	m.mu.Unlock()
	if !away {
		return false
	}
	why := FellBackFailed
	if exit.err != nil {
		why = FellBackDown
	}
	oldSite, oldPort := mt.site, mt.Port
	if err := m.restart(mt); err != nil {
		m.log.Logf(who, "match %d: game %d not started again at home (%s): %v", mt.ID, mt.Game.GameID, why, err)
		return false
	}
	oldSite.pool.Release(oldPort)
	m.mu.Lock()
	mt.Region, mt.FellBack = m.siteRegion(mt.site), why
	site, pid := mt.Site, mt.proc.Pid()
	m.mu.Unlock()
	m.log.Logf(who, "game %d: wanted %s, runs on %s (%s): %s (its host on %s ended before gameReady)", mt.Game.GameID,
		wanted, site, home, why, oldSite.name)
	m.log.Logf(who, "match %d: started pid %d%s for game %d again at home, GMS %s", mt.ID, pid, onSite(mt.site),
		mt.Game.GameID, mt.Bind)
	return true
}

// AddTestMachine adds a host machine of name without an agent link, as if an agent had registered: its hosts dial the
// control listener from loopback, as this machine's do. For tests (here and in the packages that use a Manager).
func (m *Manager) AddTestMachine(name string, l Launcher, pool *Pool, gmsHost string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sites = append(m.sites, &site{name: name, launcher: l, pool: pool, bindHost: "127.0.0.1", gmsHost: gmsHost})
}

// AddTestLinkedMachine adds a TLS-linked machine of region with a match gateway (gateway, pin) that runs its games with
// l on pool's ports (tests of the services' way to such a machine; it has no link to write to).
func (m *Manager) AddTestLinkedMachine(name, region, gateway, pin string, l Launcher, pool *Pool) {
	a := &agentConn{m: m, name: name, ip: net.IPv4(127, 0, 0, 1), link: &linkState{region: region, gateway: gateway, pin: pin}}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sites = append(m.sites, &site{name: name, launcher: l, pool: pool, bindHost: "127.0.0.1", agent: a})
}
