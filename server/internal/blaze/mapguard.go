package blaze

// The map check (MAPGUARD): a client whose copy lacks a map's added archive (zz_MM_Map_v3.snap) crashes at that map's
// level load (FileDepot.cpp 138 on levels/MM_Map_v3/MM_Map_v3.bin, a null read at 0x008E65A0), so the server keeps such
// a client out of every game on that map. The client's own version string (VSTR) is the same in every copy, so the
// signal is the release of its Start-Client: a Start-Client of this server's release has the archive (its setup makes
// it before every game start, and a failed step stops the start), and the tunnel's gateway knows each session's
// release (tunnel.Addr.Release; another release gets in only with --allow-any-client). A client that comes without
// the tunnel can't be told apart; MapTrust (dgserver --trust-maps) names those the server takes as having the files.
//
// The maps of a maps folder (MAPED-GUARD) are copied in by hand, so a client of this release may lack one or have
// another version of it. Their compiler is deterministic, so one SHA-256 per map: the server knows its host copy's
// (MapVersions), a Start-Client names its copy's in the tunnel's session hello (tunnel.Addr.Maps), and a player gets
// into a game on such a map only with the same hash; refusals as for MM_Map_v3. A client that named no list (a
// Start-Client from before it, a direct client --client-maps doesn't name) keeps the release rule and MapTrust.
//
// What the client shows (Dawngate.exe 854, read statically):
//   - a failed joinGame (in-flight op 2, GameEntryManager_HandleNetworkGameJoinResult 0x0068EFC0): any Blaze error
//     but GAMEMANAGER_ERR_TEAM_FULL (0x00FF0004, which retries the other team) goes to the listeners' slot 9; the
//     main menu's (0x004DD110) looks the error code up in the locale table BlazeErrors (0x1E5BC096) and adds the text
//     as a chat notification (ChatUI_AddBroadcastNotification), else "Failed to join game" (FE_MM 0x0EDFF13D); a
//     failed reconnect (op 3) always says "Failed to reconnect to game. The game probably just ended."
//   - a failed startMatchmaking: a modal pop-up "Network Message" with the same table's text by the code (B10b).
//   - a failed create (op 1): nothing but for 0x01320004 ("no servers available"), so a game the player creates
//     falls back to SM_Map_v5 instead of a refusal.
//
// The code: the frame's 16-bit error with the component makes the client's 32-bit error (code << 16 | component), so
// a GameManager reply can only name 0x....0004. Of BlazeErrors' GameManager lines only 0x01310004 fits: "Dawngate is
// unable to continue. Please close the client and re-run the launcher. Error Code: 01310004"
// (GAMEMANAGER_ERR_DYNAMIC_GAME_CREATION_TIMED_OUT; the value occurs in the exe only in the error-name function
// 0x00C9BDF0, so no code path treats it specially). Restarting Start-Client is the player's fix. The exact reason goes
// to the player beside it as a system message (a "System" chat line and a notification, sysmsg.go), in the server's
// own words, which reach any copy.

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"

	"dawngate/server/internal/matchmaker"
	"dawngate/server/internal/tunnel"
)

// ErrMapFilesMissing is the error a join into a game on a map whose files the client may lack gets: shown as
// BlazeErrors 0x01310004 (above).
const ErrMapFilesMissing = 0x0131

// MapNoteID is the broadcast record id of the map check's message to a player (one record per client, the text
// changes with each refusal: sysmsg.go's ids 1-16 are the operator's, OnlineCountID 100).
const MapNoteID = 101

// mapNoteFlags show the message as a "System" chat line and a notification in the main menu.
const mapNoteFlags = BroadcastChatLine | BroadcastNotification

// mapNoteSeconds is how long the client keeps the record [our choice]: it is shown at once or never.
const mapNoteSeconds = 600

// MapTrust names the clients without the tunnel that the server takes as having every map's files (dgserver
// --trust-maps): All, or those whose address is in one of Prefixes. The zero value trusts none: such a client plays
// only SM_Map_v5.
type MapTrust struct {
	All      bool
	Prefixes []netip.Prefix
}

// ParseMapTrust reads --trust-maps: "none" or "" (none), "all", or comma-separated addresses and networks
// ("127.0.0.3, 192.168.1.0/24").
func ParseMapTrust(s string) (MapTrust, error) {
	var m MapTrust
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "none":
		return m, nil
	case "all":
		m.All = true
		return m, nil
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var p netip.Prefix
		var err error
		if strings.Contains(part, "/") {
			p, err = netip.ParsePrefix(part)
		} else {
			var a netip.Addr
			if a, err = netip.ParseAddr(part); err == nil {
				p = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
			}
		}
		if err != nil {
			return MapTrust{}, fmt.Errorf("--trust-maps: %q is no address or network (none, all, or a list such as 127.0.0.3,192.168.1.0/24)", part)
		}
		m.Prefixes = append(m.Prefixes, p.Masked())
	}
	return m, nil
}

// ParseClientMaps reads --client-maps: comma-separated "<address>=<archive>:<sha256 hex>+<archive>:<hex>..." (an
// address with nothing after "=" has no maps), e.g. "127.0.0.4=zz_CM_Ring.snap:85d8...,127.0.4.2=". "" = none.
func ParseClientMaps(s string) (map[netip.Addr][]tunnel.InstalledMap, error) {
	out := map[netip.Addr][]tunnel.InstalledMap{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		addr, list, ok := strings.Cut(part, "=")
		a, err := netip.ParseAddr(strings.TrimSpace(addr))
		if !ok || err != nil {
			return nil, fmt.Errorf("--client-maps: %q is no <address>=<archive>:<sha256>+... (an address and its maps)", part)
		}
		maps := []tunnel.InstalledMap{}
		for _, m := range strings.Split(list, "+") {
			if m = strings.TrimSpace(m); m == "" {
				continue
			}
			name, h, ok := strings.Cut(m, ":")
			b, err := hex.DecodeString(strings.TrimSpace(h))
			if !ok || err != nil || len(b) != 32 || tunnel.CleanArchive(name) != name || name == "" {
				return nil, fmt.Errorf("--client-maps: %q is no <archive>:<sha256 in 64 hex digits>", m)
			}
			im := tunnel.InstalledMap{Archive: name}
			copy(im.SHA256[:], b)
			maps = append(maps, im)
		}
		out[a.Unmap()] = maps
	}
	return out, nil
}

// String is the log's form: "none", "all" or the list.
func (m MapTrust) String() string {
	switch {
	case m.All:
		return "all"
	case len(m.Prefixes) == 0:
		return "none"
	}
	var out []string
	for _, p := range m.Prefixes {
		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}
	return strings.Join(out, ", ")
}

func (m MapTrust) trusts(a netip.Addr) bool {
	if m.All {
		return true
	}
	a = a.Unmap()
	for _, p := range m.Prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// levelArchive is the added archive level needs in a player's copy ("" for SM_Map_v5, which ships whole, and for an
// empty level: the server's default).
func levelArchive(level string) string {
	if l, ok := FindLevel(level); ok {
		return l.Archive
	}
	return ""
}

// mapCheck is the map check's answer for one client: ok when it may play the level, why for the log, tell for the
// player's message (empty when ok).
type mapCheck struct {
	ok        bool
	why, tell string
	// version: decided by the map's version (MAPED-GUARD): tell says what the copy has, and the player's fix is the
	// map package, not Start-Client's release
	version bool
}

// canLoad reports whether c's client may play level: SM_Map_v5 always; a map of the maps folder whose version the
// server knows (MapVersions) when the client's copy names the same archive hash (versionCheck: the tunnel hello's
// maps, ClientMaps); another map, and a versioned one whose client named no maps (a Start-Client from before the
// list, a direct client ClientMaps doesn't name), when its Start-Client came through the tunnel and is this server's
// release, or when it came without the tunnel from an address MapTrust names.
func (s *Server) canLoad(c *Conn, level string) mapCheck {
	arch := levelArchive(level)
	if arch == "" {
		return mapCheck{ok: true}
	}
	if want, ok := s.MapVersions[strings.ToLower(arch)]; ok {
		if maps, how, named := s.clientMaps(c); named {
			return versionCheck(level, arch, want, maps, how)
		}
	}
	if t := c.TunnelAddr(); t != nil {
		name, same := t.Release()
		if same {
			return mapCheck{ok: true, why: "its Start-Client is this server's release " + name}
		}
		if name == "" {
			return mapCheck{why: "its Start-Client names no release (older than the release check; let in by --allow-any-client)",
				tell: "your Start-Client is older than this server's release"}
		}
		return mapCheck{why: fmt.Sprintf("its Start-Client is release %s, not this server's (let in by --allow-any-client)", name),
			tell: fmt.Sprintf("your Start-Client is release %s, not this server's", name)}
	}
	a := c.Addr()
	if s.MapTrust.trusts(a) {
		return mapCheck{ok: true, why: fmt.Sprintf("a client without the tunnel from %s, trusted by --trust-maps", a)}
	}
	from := ""
	if a.IsValid() {
		from = " from " + a.String()
	}
	return mapCheck{why: fmt.Sprintf("a client without the tunnel%s, which --trust-maps (%s) doesn't name, so the server can't tell "+
		"whether its copy has %s", from, s.MapTrust, levelArchive(level)),
		tell: "you are connected without the tunnel, so the server can't check your copy"}
}

// clientMaps are the installed maps c's client named: its tunnel session hello's (tunnel.Addr.Maps), else ClientMaps'
// entry for its address (the direct path's test aid). how says where they come from, for the log; named is false
// when neither names a list.
func (s *Server) clientMaps(c *Conn) (maps []tunnel.InstalledMap, how string, named bool) {
	if t := c.TunnelAddr(); t != nil {
		maps, named = t.Maps()
		return maps, "its Start-Client's tunnel hello", named
	}
	if a := c.Addr(); a.IsValid() {
		maps, named = s.ClientMaps[a.Unmap()]
		return maps, "--client-maps for " + a.Unmap().String(), named
	}
	return nil, "", false
}

// versionCheck is the map check of a map whose version the server knows: archive arch with the hash want. The client
// may play level when its copy's maps (how: where they were named) have arch with the same hash.
func versionCheck(level, arch string, want [32]byte, maps []tunnel.InstalledMap, how string) mapCheck {
	server := tunnel.InstalledMap{Archive: arch, SHA256: want}
	shown := LevelDisplay(level)
	for _, m := range maps {
		if !strings.EqualFold(m.Archive, arch) {
			continue
		}
		if m.SHA256 == want {
			return mapCheck{ok: true, version: true, why: fmt.Sprintf("its copy's %s is the server's version %s (%s)", arch,
				server.Short(), how)}
		}
		return mapCheck{version: true, why: fmt.Sprintf("its copy's %s is %s, not the server's version %s (%s)", arch,
			m.Short(), server.Short(), how),
			tell: fmt.Sprintf("has another version of %s (yours %s, this server's %s)", shown, m.Short(), server.Short())}
	}
	return mapCheck{version: true, why: fmt.Sprintf("its copy has no %s (%s names %s)", arch, how, tunnel.MapsWords(maps)),
		tell: fmt.Sprintf("doesn't have %s", shown)}
}

// mapFix is the player's fix after "your copy of Dawngate": the release's Start-Client (MAPGUARD), or this server's
// version of the map package (a version check): Start-Client downloads it when the server hands it out (map
// delivery, MapServed), else the player copies it.
func (s *Server) mapFix(level string, mc mapCheck) string {
	if mc.version && s.mapServed(level) {
		return mc.tell + ". Start-Client downloads this server's version of it when you log in: close the game and " +
			"start it again with Start-Client.exe, which installs it."
	}
	if mc.version {
		return mc.tell + ". Copy this server's version of its map package into the maps folder beside " +
			"Start-Client.exe, then close the game and start it again with Start-Client.exe, which installs it."
	}
	return fmt.Sprintf("may not have that map's files (%s). Close the game and start it again with Start-Client.exe of "+
		"%s, which adds them.", mc.tell, s.releaseWords())
}

// releaseWords is "this server's release (<name>)" for the player's message.
func (s *Server) releaseWords() string {
	if s.Release == "" {
		return "this server's release"
	}
	return "this server's release (" + s.Release + ")"
}

// mapRefusedText is the player's message for a refused join, mapFallbackText for a game it creates that plays on
// SM_Map_v5 instead. The players' messages name the maps as players see them (LevelDisplay: Anshavar Pass, The
// Dawngate); the log keeps the levels' names.
func (s *Server) mapRefusedText(level, what string, mc mapCheck) string {
	return fmt.Sprintf("You can't join %s: it plays on the map %s, and your copy of Dawngate %s", what,
		LevelDisplay(level), s.mapFix(level, mc))
}

func (s *Server) mapFallbackText(level string, mc mapCheck) string {
	shown := LevelDisplay(level)
	if mc.version && s.mapServed(level) {
		return fmt.Sprintf("Your game plays on %s instead of %s: your copy of Dawngate %s. Start-Client downloads this "+
			"server's version of it when you log in: close the game and start it again with Start-Client.exe to play %s.",
			LevelDisplay(HostedLevel), shown, mc.tell, shown)
	}
	if mc.version {
		return fmt.Sprintf("Your game plays on %s instead of %s: your copy of Dawngate %s. Copy this server's version "+
			"of the map package into the maps folder beside Start-Client.exe and start the game again to play %s.",
			LevelDisplay(HostedLevel), shown, mc.tell, shown)
	}
	return fmt.Sprintf("Your game plays on %s instead of %s: your copy of Dawngate may not have %s's files (%s). "+
		"Start the game with Start-Client.exe of %s to play %s.", LevelDisplay(HostedLevel), shown, shown, mc.tell,
		s.releaseWords(), shown)
}

// tellMap sends c's player the map check's message (MapNoteID; a repeated text gets a number so the client shows it
// again: it shows a record's text once).
func (s *Server) tellMap(c *Conn, text string) {
	if c == nil {
		return
	}
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.mapNotes == nil {
		sm.mapNotes = map[*Conn]int{}
	}
	sm.mapNotes[c]++
	if n := sm.mapNotes[c]; n > 1 {
		text = fmt.Sprintf("%s (%d)", text, n)
	}
	s.pushSystemLocked(c, []ClientMessage{MapNoteMessage(text)})
}

// MapNoteMessage is the map check's message to a player: a broadcast (TAG 2) with id MapNoteID, shown as a "System"
// chat line and a notification, kept mapNoteSeconds.
func MapNoteMessage(text string) ClientMessage {
	return SystemMessage(SystemTagBroadcast, []MsgAttr{
		{BroadcastAttrID, fmt.Sprint(MapNoteID)},
		{BroadcastAttrFlags, fmt.Sprint(mapNoteFlags)},
		{BroadcastAttrSeconds, fmt.Sprint(mapNoteSeconds)},
		{BroadcastAttrText, text},
	})
}

// forgetMapNotes drops c's count when its connection ends.
func (s *Server) forgetMapNotes(c *Conn) {
	s.sys.mu.Lock()
	delete(s.sys.mapNotes, c)
	s.sys.mu.Unlock()
}

// mapJoinRefused is the map check of a join into game gid (its name and level): false when the player may join
// (logged when the level needs an archive), else true after the error reply was set (refuseMapJoin).
func (s *Server) mapJoinRefused(r *Request, gid int64, name, level string) bool {
	mc := s.canLoad(r.Conn, level)
	if mc.ok {
		if mc.why != "" {
			s.Log.Logf("blaze", "   joinGame: game %d plays on %s: player %d may join (map check: %s)", gid, level,
				personaOf(r.Conn), mc.why)
		}
		return false
	}
	s.refuseMapJoin(r, gid, name, level, mc)
	return true
}

func personaOf(c *Conn) int64 {
	if c == nil || c.Session == nil {
		return 0
	}
	return c.Session.Persona.ID
}

// createLevel is the map check of game gid that a player creates (practice, a custom game, a practice group's):
// level when the creator may play it, else SM_Map_v5 (a failed create would show the player nothing; every copy has
// SM_Map_v5), logged and told to the player. changed reports the fallback.
func (s *Server) createLevel(r *Request, gid int64, level string) (lv string, changed bool) {
	mc := s.canLoad(r.Conn, level)
	if mc.ok {
		if mc.why != "" {
			s.Log.Logf("blaze", "   game %d plays on %s: its creator %d may play it (map check: %s)", gid, level,
				personaOf(r.Conn), mc.why)
		}
		return level, false
	}
	s.Log.Logf("blaze", "   game %d: %s instead of %s: its creator %d may lack the map's files, %s (map check)", gid, HostedLevel, level,
		personaOf(r.Conn), mc.why)
	conn, text := r.Conn, s.mapFallbackText(level, mc)
	r.After(func() { s.tellMap(conn, text) })
	return HostedLevel, true
}

// mapQueueRefused is the map check of a PvP queue request (ticket t: its owner and a party's members): a matchmade
// game plays on the server's map, so when that needs an archive every member must be able to play it. Otherwise the
// request gets ErrMapFilesMissing (the client's pop-up), each member who can't is told why, and the owner, when the
// one who can't is another member, too.
func (s *Server) mapQueueRefused(r *Request, t *matchmaker.Ticket) bool {
	level := s.hostedLevel()
	if levelArchive(level) == "" {
		return false
	}
	ms := t.Data.(*mmSession)
	var out, others []string
	othersVersion := false
	for i, m := range ms.members {
		mc := s.canLoad(m.conn, level)
		if mc.ok {
			s.Log.Logf("blaze", "   matchmaking: player %d may play %s (map check: %s)", t.Players[i].ID, level, mc.why)
			continue
		}
		s.Log.Logf("blaze", "   matchmaking: player %d can't play %s: %s (map check)", t.Players[i].ID, level, mc.why)
		out = append(out, t.Players[i].Name)
		conn, text := m.conn, s.mapQueueText(level, "", mc)
		if i > 0 {
			others = append(others, t.Players[i].Name)
			othersVersion = othersVersion || mc.version
			text = s.mapQueueText(level, t.Players[0].Name, mc)
		}
		r.After(func() { s.tellMap(conn, text) })
	}
	if len(out) == 0 {
		return false
	}
	if len(others) > 0 { // the owner hears who keeps the party out
		text := fmt.Sprintf("The PvP queue plays on the map %s, whose files the copies of %s may not have; they start "+
			"the game again with Start-Client.exe of %s.", LevelDisplay(level), strings.Join(others, ", "), s.releaseWords())
		if othersVersion {
			text = fmt.Sprintf("The PvP queue plays on the map %s, which the copies of %s don't have in this server's "+
				"version; they copy its map package into the maps folder beside Start-Client.exe and start the game "+
				"again.", LevelDisplay(level), strings.Join(others, ", "))
		}
		owner := ms.members[0].conn
		r.After(func() { s.tellMap(owner, text) })
	}
	s.Log.Logf("blaze", "   matchmaking for %d refused: the queue plays on %s, and %s may lack its files (map check); "+
		"error reply %#04x", t.Players[0].ID, level, strings.Join(out, ", "), ErrMapFilesMissing)
	r.Fail(ErrMapFilesMissing)
	return true
}

// mapQueueText is a queue refusal's message to one player (leader: the party's leader who queued, "" for the player
// itself).
func (s *Server) mapQueueText(level, leader string, mc mapCheck) string {
	who := "You can't queue"
	if leader != "" {
		who = fmt.Sprintf("%s's party can't queue with you", leader)
	}
	return fmt.Sprintf("%s: the PvP queue plays on the map %s, and your copy of Dawngate %s", who,
		LevelDisplay(level), s.mapFix(level, mc))
}

// refuseMapJoin answers a join into game gid on level with ErrMapFilesMissing, logs why and tells the player.
func (s *Server) refuseMapJoin(r *Request, gid int64, name, level string, mc mapCheck) []byte {
	me := personaOf(r.Conn)
	s.Log.Logf("blaze", "   joinGame: game %d: player %d refused: the game plays on %s and %s (map check); error reply %#04x",
		gid, me, level, mc.why, ErrMapFilesMissing)
	what := "this game"
	if name != "" {
		what = fmt.Sprintf("%q", name)
	}
	text := s.mapRefusedText(level, what, mc)
	conn := r.Conn
	r.After(func() { s.tellMap(conn, text) })
	r.Fail(ErrMapFilesMissing)
	return nil
}

// mapServed says whether the server hands level's package out (MapServed; map delivery).
func (s *Server) mapServed(level string) bool { return s.MapServed != nil && s.MapServed(level) }
