package launcher

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/discord"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/setup"
	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// Join from Discord's addresses: one server, two players' fronts. No lane uses them.
const (
	joinServerAddr = "127.0.9.80"
	joinFrontA     = "127.0.9.81"
	joinFrontB     = "127.0.9.82"
)

func TestJoinSecret(t *testing.T) {
	var pin tunnel.Pin
	for i := range pin {
		pin[i] = byte(i * 7)
	}
	code := "AbCdEfGhIjKlMnOpQrSt_-"
	s := joinSecret{Address: "play.example.org", Port: tunnel.DefaultPort, Pin: pin, Code: code}
	v := s.String()
	if !strings.HasPrefix(v, "dg1/play.example.org/") || !strings.HasSuffix(v, "/"+code) || len(v) > discord.MaxSecret {
		t.Fatalf("%q (%d)", v, len(v))
	}
	back, err := parseJoinSecret(v)
	if err != nil || back != s {
		t.Fatalf("%+v %v", back, err)
	}
	// another port is written; the longest IPv4 address with a port fits Discord's 128 characters
	s2 := joinSecret{Address: "203.255.255.255", Port: 50000, Pin: pin, Code: code}
	if v2 := s2.String(); !strings.Contains(v2, "/203.255.255.255:50000/") || len(v2) > discord.MaxSecret {
		t.Errorf("%q", v2)
	} else if back, err := parseJoinSecret(v2); err != nil || back != s2 {
		t.Errorf("%+v %v", back, err)
	}
	for _, bad := range []string{"", "dg2/a/b/c", "dg1/play.example.org/" + "x/" + code, "dg1/bad host/AAAA/" + code,
		strings.Replace(v, code, "short", 1), strings.Replace(v, code, "no spaces in a code!!", 1), v + "/more",
		strings.Replace(v, "play.example.org", "play.example.org:0", 1)} {
		if _, err := parseJoinSecret(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if a, b := partyIDOf(code), partyIDOf(code+"x"); a == b || len(a) != 16 || strings.Contains(v, a) {
		t.Errorf("party ids %q %q", a, b)
	}
}

func TestRegisterDiscordLaunch(t *testing.T) {
	reg := &memRegistry{}
	root := discordLaunchRoot("")
	changed, err := registerDiscordLaunch(reg, root, "123", `C:\Games\Dawngate\Start-Client.exe`)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if v, _ := reg.Get(`Software\Classes\discord-123\shell\open\command`, ""); v != `"C:\Games\Dawngate\Start-Client.exe"` {
		t.Errorf("command %q", v)
	}
	if _, ok := reg.Get(`Software\Classes\discord-123`, "URL Protocol"); !ok {
		t.Error("no URL Protocol")
	}
	if changed, _ := registerDiscordLaunch(reg, root, "123", `C:\Games\Dawngate\Start-Client.exe`); changed {
		t.Error("the same path written again")
	}
	if changed, _ := registerDiscordLaunch(reg, root, "123", `D:\Dawngate\Start-Client.exe`); !changed {
		t.Error("a moved program not written")
	}
	if r := discordLaunchRoot(`\\.\pipe\dawngate-test-discord-b`); r != `Software\DawngateRestorationTest\dawngate-test-discord-b` {
		t.Errorf("the test hook's root %q", r)
	}
	if _, err := registerDiscordLaunch(reg, root, "", "x"); err == nil {
		t.Error("no id accepted")
	}
	if _, ok := discordRegistry.(*memRegistry); !ok {
		t.Error("the tests' registry isn't the memory one (TestMain)")
	}
}

func TestDiscordJoinSetting(t *testing.T) {
	on := discordConfig{on: true}
	for _, c := range []struct {
		flag string
		ini  Config
		dc   discordConfig
		want bool
		why  string
		bad  bool
	}{
		{"", nil, on, true, "friends on Discord can join", false},
		{"", Config{DiscordJoinKey: "off"}, on, false, "discord_join = off", false},
		{"on", Config{DiscordJoinKey: "off"}, on, true, "", false},
		{"", nil, discordConfig{}, false, "", false},
		{"maybe", nil, on, false, "", true},
	} {
		got, why, err := discordJoinSetting(c.flag, c.ini, c.dc)
		if (err != nil) != c.bad || got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%+v: %v %q %v", c, got, why, err)
		}
	}
}

// joinGame is a fake game on a front: every frame it gets is kept, requests are answered by waiting for their reply.
type joinGame struct {
	t  *testing.T
	c  net.Conn
	mu sync.Mutex
	fr []fire.Frame
	id uint32
}

func newJoinGame(t *testing.T, ip string) *joinGame {
	g := fakeGame{t: t, ip: ip}
	c := g.dial(DefaultFrontPorts.Blaze)
	t.Cleanup(func() { c.Close() })
	g.start(c)
	jg := &joinGame{t: t, c: c, id: 2}
	go func() {
		r := bufio.NewReader(c)
		for {
			_, f, err := readFrame(r)
			if err != nil {
				return
			}
			jg.mu.Lock()
			jg.fr = append(jg.fr, f)
			jg.mu.Unlock()
		}
	}()
	return jg
}

// call sends a request and waits for its reply.
func (g *joinGame) call(comp, cmd uint16, payload []byte) fire.Frame {
	g.t.Helper()
	g.mu.Lock()
	g.id++
	id := g.id
	g.mu.Unlock()
	out, _ := fire.Encode(fire.Frame{Component: comp, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if _, err := g.c.Write(out); err != nil {
		g.t.Fatal(err)
	}
	return g.wait("the reply to "+describeCmd(fire.Frame{Component: comp, Command: cmd}), func(f fire.Frame) bool {
		return f.ID == id && f.Component == comp && f.Command == cmd && f.Type != fire.Notification && f.Type != fire.Request
	})
}

// wait waits for a frame that matches.
func (g *joinGame) wait(what string, match func(fire.Frame) bool) fire.Frame {
	g.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		frames := append([]fire.Frame(nil), g.fr...)
		g.mu.Unlock()
		for _, f := range frames {
			if match(f) {
				return f
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	g.t.Fatalf("no %s", what)
	return fire.Frame{}
}

// login logs in on the front (MAIL as typed), then the persona and the friends lists, as the game does.
func (g *joinGame) login(mail string) {
	g.t.Helper()
	if rep := g.call(1, 0x28, loginBody(mail)); rep.Type != fire.Reply {
		g.t.Fatalf("login %s: error %#x", mail, rep.Error)
	}
	name, _, _ := splitTag(mail)
	if rep := g.call(1, 0x6E, tdf.NewWriter().String("PNAM", store.PersonaName(name)).Bytes()); rep.Type != fire.Reply {
		g.t.Fatalf("loginPersona %s: error %#x", mail, rep.Error)
	}
	g.call(blaze.ComponentAssociationLists, blaze.CmdALGetLists, tdf.NewWriter().Bytes())
}

// messages are the NotifyMessages the game got: their PYLD's TYPE and their ATTR texts.
func (g *joinGame) messages() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, f := range g.fr {
		if f.Component != blaze.ComponentMessaging || f.Command != blaze.NotifyMessageID || f.Type != fire.Notification {
			continue
		}
		st, err := tdf.Decode(f.Payload)
		if err != nil {
			continue
		}
		p, _ := st.Sub("PYLD")
		typ, _ := p.Int("TYPE")
		s := fmt.Sprintf("type 0x%X", typ)
		if a, ok := p.Get("ATTR"); ok {
			if m, ok := a.Value.(*tdf.Map); ok {
				for _, e := range m.Entries {
					if v, ok := e.Value.(string); ok {
						s += " | " + v
					}
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// joinFront starts a front with the Discord status's tracker and Join on; acts gets its activities.
func joinFront(t *testing.T, ip string, servers ...Bookmark) (*Front, setup.Layout, string, func() []*discord.Activity) {
	t.Helper()
	l := setup.Layout{Root: t.TempDir()}
	bm, err := LoadBookmarks(BookmarksPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range servers {
		if _, err := bm.Add(b); err != nil {
			t.Fatal(err)
		}
	}
	_, tlsCfg, err := tunnelCertSet(l, ip, false)
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	lf, err := os.Create(filepath.Join(logDir, "front.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lf.Close() })
	var mu sync.Mutex
	var acts []*discord.Activity
	tr := newPresenceTracker(nil)
	tr.setOutput(func(a *discord.Activity) {
		mu.Lock()
		acts = append(acts, a)
		mu.Unlock()
	})
	f := &Front{Log: logging.New(lf), Layout: l, Bookmarks: bm, TunnelIP: net.ParseIP(ip), RedirectorTLS: tlsCfg,
		ConnectTimeout: 3 * time.Second, ListRefresh: -1, Presence: tr, DiscordJoin: true, JoinLoopback: true,
		JoinSettle: 20 * time.Millisecond, JoinAfterLists: 200 * time.Millisecond, JoinAnswerWait: 1500 * time.Millisecond}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Close)
	return f, l, logDir, func() []*discord.Activity {
		mu.Lock()
		defer mu.Unlock()
		return append([]*discord.Activity(nil), acts...)
	}
}

// waitActivity waits for an activity that matches.
func waitActivity(t *testing.T, acts func() []*discord.Activity, what string, match func(*discord.Activity) bool) *discord.Activity {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range acts() {
			if match(a) {
				return a
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no activity %s: %v", what, acts())
	return nil
}

// A party's join: A's front puts the server's join code into A's activity (a secret with the server and its key),
// B's front takes the secret before B's login (the server added to B's empty list with its key, preselected), B logs
// in without a tag, and the server's invite in A's name reaches B's game; B joins, A's activity counts two; a second
// Join of B is refused with a chat line; a secret that can't be used gives a chat line; no join-code frame ever
// reaches a game.
func TestFrontDiscordJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("binds 127.0.9.80-82's ports")
	}
	port, serverLog := serveTunnelLogged(t, joinServerAddr)
	fa, _, _, actsA := joinFront(t, joinFrontA, Bookmark{Address: joinServerAddr, GatewayPort: port})
	fb, lb, logB, _ := joinFront(t, joinFrontB)

	// A: the key confirmed once, logged in, a party
	ga := newJoinGame(t, joinFrontA)
	if rep := ga.call(1, 0x28, loginBody("alpha#a")); rep.Error != FrontErrNewKey {
		t.Fatalf("A's first login: %#x", rep.Error)
	}
	ga.login("alpha#a")
	if rep := ga.call(blaze.ComponentPlaygroups, blaze.CmdPlaygroupsCreatePlaygroup, tdf.NewWriter().Int("JOIN", 0).Struct("PGRP",
		func(w *tdf.Writer) { w.Int("MLIM", 5).String("NAME", "party") }).Bytes()); rep.Type != fire.Reply {
		t.Fatalf("createPlaygroup: %#x", rep.Error)
	}
	a := waitActivity(t, actsA, "with a join secret", func(a *discord.Activity) bool { return a.JoinSecret != "" })
	if a.PartySize != 1 || a.PartyMax != 5 || a.PartyID == "" || a.State != "In a party" {
		t.Errorf("A's activity %+v", a)
	}
	sec, err := parseJoinSecret(a.JoinSecret)
	if err != nil || sec.Address != joinServerAddr || sec.Port != port {
		t.Fatalf("A's secret %q: %+v %v", a.JoinSecret, sec, err)
	}
	if pin, ok := fa.trustedPin(Bookmark{Address: joinServerAddr}); !ok || pin != sec.Pin {
		t.Error("the secret's key isn't A's trusted one")
	}
	waitLog(t, serverLog, "issueJoinCode by", 0)

	// B: the Join before its login: the server added with its key, preselected
	fb.DiscordJoinSecret(a.JoinSecret)
	deadline := time.Now().Add(5 * time.Second)
	for fb.preselected() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	b, ok := fb.Bookmarks.Find(fb.preselected())
	if !ok || b.Address != joinServerAddr || b.GatewayPort != port {
		t.Fatalf("B's list: %+v (%v)\n%s", b, ok, readLogs(t, logB))
	}
	if pin, ok := fb.trustedPin(b); !ok || pin != sec.Pin {
		t.Error("B doesn't trust the secret's key")
	}
	if raw, _ := os.ReadFile(BookmarksPath(lb)); !strings.Contains(string(raw), joinServerAddr) {
		t.Error("B's bookmarks file lacks the server")
	}
	gb := newJoinGame(t, joinFrontB)
	gb.login("bravo") // no tag: the preselected server, its key trusted: no confirmation
	// a new account: the game shows its news overlay, and the invite waits until the game closes it
	waitLog(t, logB, "the game shows its news overlay", 0)
	time.Sleep(700 * time.Millisecond)
	if strings.Contains(readLogs(t, logB), "redeemed on") {
		t.Fatal("redeemed while the news overlay showed")
	}
	if rep := gb.call(blaze.ComponentUserSessions, blaze.CmdUserSessionsSetUserInfoAttribute,
		tdf.NewWriter().Int("ATTV", int64(blaze.UserInfoNewsSeen)).Int("MASK", 0).Bytes()); rep.Type != fire.Reply {
		t.Fatalf("setUserInfoAttribute: %#x", rep.Error)
	}
	waitLog(t, logB, "the game's news overlay was closed", 0)
	st, _ := tdf.Decode(gb.wait("the invite's frame", func(f fire.Frame) bool {
		if f.Component != blaze.ComponentMessaging || f.Command != blaze.NotifyMessageID {
			return false
		}
		st, _ := tdf.Decode(f.Payload)
		p, _ := st.Sub("PYLD")
		typ, _ := p.Int("TYPE")
		return typ == 0x176A0D2B
	}).Payload)
	if name, _ := st.Str("NAME"); name != store.PersonaName("alpha") {
		t.Errorf("the invite's sender %q", name)
	}
	waitLog(t, logB, "discord join: the server's invite (NotifyMessage", 0)
	// no answer within JoinAnswerWait: the front invites again
	waitLog(t, logB, "inviting again (2 of 3)", 0)
	gb.wait("the second invite", func(fire.Frame) bool {
		return strings.Count(strings.Join(gb.messages(), "\n"), "type 0x176A0D2B") >= 2
	})
	// YES: the game's answer "T" to the inviter (the front sees it and stops), then B joins the party by its id
	pgid, number, leader := int64(0), "", int64(0)
	p, _ := st.Sub("PYLD")
	attrs, _ := p.Get("ATTR")
	for _, e := range attrs.Value.(*tdf.Map).Entries {
		switch k, _ := e.Key.(int64); k {
		case 0xB0003:
			pgid, _ = strconv.ParseInt(e.Value.(string), 10, 64)
		case 0xB0005:
			number = e.Value.(string)
		}
	}
	if src, ok := st.Get("SRCE"); ok {
		leader = src.Value.(tdf.ObjID).ID
	}
	answer := tdf.NewWriter().Map("ATTR", tdf.TypeInt, tdf.TypeString, tdf.KV(int64(0xB0002), "T"), tdf.KV(int64(0xB0005), number)).
		Int("FLAG", 0).Int("STAT", 0).Int("TAG", 0).List("TIDS", tdf.TypeInt, []int64{leader}).
		Value("TTYP", tdf.TypeObjType, tdf.ObjType{Component: 0x7802, Type: 1}).Int("TYPE", answerParty).Bytes()
	if rep := gb.call(blaze.ComponentMessaging, blaze.CmdMsgSendMessage, answer); rep.Type != fire.Reply {
		t.Fatalf("B's answer: %#x", rep.Error)
	}
	waitLog(t, logB, "discord join: the game answered the server's invite", 0)
	if rep := gb.call(blaze.ComponentPlaygroups, blaze.CmdPlaygroupsJoinPlaygroup, tdf.NewWriter().Int("PGID", pgid).Bytes()); rep.Type != fire.Reply {
		t.Fatalf("B's join of party %d: %#x", pgid, rep.Error)
	}
	waitActivity(t, actsA, "of two with a secret", func(a *discord.Activity) bool { return a.PartySize == 2 && a.JoinSecret != "" })

	// B's second Join: refused (already in it), a chat line; a secret that can't be used: a chat line
	fb.DiscordJoinSecret(a.JoinSecret)
	gb.wait("the 'in it already' line", func(fire.Frame) bool {
		return strings.Contains(strings.Join(gb.messages(), "\n"), "you are in it already")
	})
	fb.DiscordJoinSecret("dg1/nonsense")
	gb.wait("the 'can't be used' line", func(fire.Frame) bool {
		return strings.Contains(strings.Join(gb.messages(), "\n"), "This Join from Discord can't be used")
	})
	// a Join for another server while logged in here: added with its key, preselected, a line to log in there
	other := sec
	other.Address = "127.0.9.83"
	other.Pin[0] ^= 1
	fb.DiscordJoinSecret(other.String())
	gb.wait("the 'log in there' line", func(fire.Frame) bool {
		return strings.Contains(strings.Join(gb.messages(), "\n"), "log out and log in on 127.0.9.83")
	})
	if b, ok := fb.Bookmarks.Find(fb.preselected()); !ok || b.Address != "127.0.9.83" {
		t.Errorf("the other server isn't preselected: %+v", b)
	}
	// a Join naming a known server with another key: refused, nothing changes
	other.Pin[1] ^= 1
	fb.DiscordJoinSecret(other.String())
	gb.wait("the 'another key' line", func(fire.Frame) bool {
		return strings.Contains(strings.Join(gb.messages(), "\n"), "another key for 127.0.9.83")
	})
	for _, g := range []*joinGame{ga, gb} {
		g.mu.Lock()
		for _, f := range g.fr {
			if f.Component == blaze.ComponentJoinCodes {
				t.Errorf("a join-code frame reached a game: %+v", f)
			}
		}
		g.mu.Unlock()
	}
}

// A second Start-Client while the game runs (Discord starting it for a Join) stops before it writes anything: no
// server list, no Start-Client.ini change, no bookmark of the address it was given.
func TestSecondStartClientWritesNothing(t *testing.T) {
	dir := release(t)
	clientRunning = func() bool { return true }
	code, out := run(t, RunClient, dir, "--non-interactive", "203.0.113.5")
	if code == 0 || !strings.Contains(out, "already running") {
		t.Fatalf("%d %s", code, out)
	}
	for _, p := range []string{filepath.Join(dir, ClientConfigName), BookmarksPath(setup.Layout{Root: filepath.Join(dir, DefaultFolder)})} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s written", p)
		}
	}
}

// The news overlay gates the invite: a login whose UATT has bit 62 is ready shortly after its friends lists; one
// without it waits for the game's setUserInfoAttribute with the bit (or the server's update), at most joinNewsWait;
// a login without the own user's extended data (an older server) keeps the timer.
func TestJoinNewsOverlay(t *testing.T) {
	now := time.Unix(1760000000, 0)
	f := &Front{Now: func() time.Time { return now }}
	persona := func(bc *blazeConn, pid int64) {
		bc.watchLogin(fire.Frame{Type: fire.Reply, Component: blaze.ComponentAuthentication, Command: blaze.CmdAuthLoginPersona,
			Payload: tdf.NewWriter().Struct("PDTL", func(w *tdf.Writer) { w.Int("PID", pid) }).Bytes()})
		bc.watchLogin(fire.Frame{Type: fire.Reply, Component: blaze.ComponentAssociationLists, Command: blaze.CmdALGetLists})
	}
	update := func(bc *blazeConn, pid int64, uatt uint64) {
		bc.watchLogin(fire.Frame{Type: fire.Notification, Component: blaze.ComponentUserSessions, Command: blaze.NotifyUserSessionExtendedDataUpdate,
			Payload: blaze.ExtendedDataUpdateNotification(pid, uatt, 0)})
	}
	// seen at an earlier login: ready joinAfterNews later
	a := &blazeConn{f: f}
	persona(a, 7)
	update(a, 8, 0) // another user's update changes nothing
	update(a, 7, blaze.UserInfoNewsSeen)
	if a.joinReady(now) || !a.joinReady(now.Add(joinAfterNews)) {
		t.Error("seen before: not ready joinAfterNews later")
	}
	// a new account: the overlay shows, nothing until its dismissal (the game's request), then joinAfterNews
	b := &blazeConn{f: f}
	persona(b, 9)
	update(b, 9, 0)
	if b.joinReady(now.Add(time.Minute)) || b.news != newsClear {
		t.Error("ready while the overlay shows")
	}
	if !b.joinReady(now.Add(joinNewsWait)) {
		t.Error("not ready after joinNewsWait")
	}
	now = now.Add(40 * time.Second)
	b.watchGame(fire.Frame{Type: fire.Request, Component: blaze.ComponentUserSessions, Command: blaze.CmdUserSessionsSetUserInfoAttribute,
		Payload: tdf.NewWriter().Int("ATTV", int64(blaze.UserInfoNewsSeen)).Int("MASK", 0).Bytes()})
	if b.news != newsSet || b.joinReady(now) || !b.joinReady(now.Add(joinAfterNews)) {
		t.Error("the dismissal didn't make it ready joinAfterNews later")
	}
	// the server's update after the dismissal changes nothing more; a logout forgets it
	update(b, 9, blaze.UserInfoNewsSeen)
	b.loggedOut()
	if b.news != newsUnknown || b.joinReady(now.Add(time.Hour)) {
		t.Error("a logout kept the state")
	}
	// no extended data of the own user: the timer
	c := &blazeConn{f: f}
	persona(c, 11)
	if c.joinReady(now.Add(JoinAfterLists-time.Second)) || !c.joinReady(now.Add(JoinAfterLists)) {
		t.Error("unknown: not the timer")
	}
}
