package launcher

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/discord"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// Join from Discord: a friend clicks Join on the player's Discord profile and ends up in the player's party,
// practice group or custom lobby. The game can't be pushed into a party or lobby; it joins after accepting an invite.
// So the server sends the friend the game's own invite in the leader's (owner's) name when the friend's Start-Client
// hands it the party's join code (blaze/joincodes.go: component 0x7D00, issueJoinCode and redeemJoinCode).
//
// The host side: while the player is in a joinable party or lobby (presenceTracker.joinTarget), the front asks the
// server for its code on the player's own Blaze connection (a request it puts on the stream itself, with a message id
// from the top of the 20-bit range, which the game's ids never reach; its reply is taken off the stream before the
// game sees it) and the Discord activity gets a party id (a hash of the code: the same for every member, not the
// game's id) and the join secret: the server's address and gateway port, its tunnel key and the code (joinSecret). No
// secret when the target isn't joinable now (full, closed, private, started, a match), the server has no join codes
// (an older one), its address is this PC's own, or discord_join is off. The code is asked again at every change of the
// party or lobby and every JoinRefresh.
//
// The joiner side: Discord dispatches ACTIVITY_JOIN with the secret to the friend's Start-Client (started by Discord
// through the discord-<id> URL scheme when it wasn't running). The secret's server is added to the list with its key
// when it is missing (as --gateway-pin does: the running front's list in memory, the only writer of the file) and
// preselected; once the player is logged in on that server and in the menu, the front redeems the code, and the
// server's invite (a NotifyMessage after the reply) reaches the game: its usual pop-up, whose YES joins. Logged in on
// another server, or a refused code, gives a line in the game's chat (a system message the front writes to the game).

// JoinSecretVersion starts every join secret ("dg1/<host>[:<port>]/<key>/<code>": the key's 32 bytes in base64url).
const JoinSecretVersion = "dg1"

// joinSecret is what a Discord join secret holds.
type joinSecret struct {
	Address string     // the server's address (the players' name for it)
	Port    int        // its gateway port
	Pin     tunnel.Pin // its tunnel key
	Code    string     // the party's or lobby's join code
}

func (s joinSecret) String() string {
	host := s.Address
	if s.Port != 0 && s.Port != tunnel.DefaultPort {
		host = net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	}
	return JoinSecretVersion + "/" + host + "/" + base64.RawURLEncoding.EncodeToString(s.Pin[:]) + "/" + s.Code
}

// parseJoinSecret reads a join secret; anything else is refused with the reason.
func parseJoinSecret(v string) (joinSecret, error) {
	parts := strings.Split(v, "/")
	if len(parts) != 4 || parts[0] != JoinSecretVersion {
		return joinSecret{}, errors.New("not a join of this game's version")
	}
	s := joinSecret{Address: parts[1], Port: tunnel.DefaultPort, Code: parts[3]}
	if h, p, err := net.SplitHostPort(parts[1]); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return joinSecret{}, fmt.Errorf("the port %q", p)
		}
		s.Address, s.Port = h, n
	}
	if err := ValidServerAddress(s.Address); err != nil {
		return joinSecret{}, fmt.Errorf("the server address: %v", err)
	}
	pin, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(pin) != len(s.Pin) {
		return joinSecret{}, errors.New("the server key")
	}
	copy(s.Pin[:], pin)
	if len(s.Code) < 16 || len(s.Code) > 64 || strings.Trim(s.Code, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return joinSecret{}, errors.New("the join code")
	}
	return s, nil
}

// partyIDOf is the Discord party id for a join code: the same for every member who got the code, not the code.
func partyIDOf(code string) string {
	h := sha256.Sum256([]byte("dawngate-party:" + code))
	return hex.EncodeToString(h[:8])
}

// joinWant is a join target: JoinTargetParty and a PGID, or JoinTargetGame and a GID.
type joinWant struct{ target, id int64 }

// issuedJoin is the server's answer to the front's issueJoinCode for want, and the secret made from it ("" when a
// friend can't join now).
type issuedJoin struct {
	want            joinWant
	info            blaze.JoinCodeInfo
	secret, partyID string
}

// Front timings of Join (variables for the tests).
const (
	// JoinRefresh is how often a join code is asked for again while there is a target (a server restart forgets
	// codes; a full or closed party opens again).
	JoinRefresh = time.Minute
	// joinSettle lets a burst of changes pass before the code is asked for.
	joinSettle = 300 * time.Millisecond
	// joinPendingTTL is how long a friend's Join waits for the login on its server.
	joinPendingTTL = 10 * time.Minute
	// JoinAfterLists is how long after the game's friends lists arrived (AssociationLists::getLists, once per login,
	// after the main menu's services) the invite is sent; joinAfterPersona the latest after the persona's login. An
	// invite 2.3 s after the main menu came up was never shown (DISCORD3b's first live runs: no pop-up, no answer),
	// one 33 s after it was (A5b), so the first invite waits 10 s.
	JoinAfterLists   = 10 * time.Second
	joinAfterPersona = 25 * time.Second
	// JoinAnswerWait is how long the front waits for the game's answer to the server's invite (its YES, NO or "can't
	// now", a sendMessage to the inviter) before it redeems the code again, at most joinTries times in all: the
	// game's pop-up didn't always show, and a new invite from the same sender replaces the old one.
	JoinAnswerWait = 30 * time.Second
	joinTries      = 3
	// joinAfterNews is how long after the news overlay's dismissal (or a login that doesn't show it) the invite is
	// sent; joinNewsWait how long an invite waits for the dismissal at most (then the answer watch's resends).
	// DISCORD3b's second live runs: the first invite, sent while a new account's news overlay showed, was lost in
	// every kind; the resend after it closed was answered.
	joinAfterNews = 2 * time.Second
	joinNewsWait  = 3 * time.Minute
	// injectWait bounds the server's reply to a request the front put on the connection.
	injectWait = 10 * time.Second
	// joinNoteSeconds is how long the game keeps a note's record.
	joinNoteSeconds = 600
)

// injectBase is the first message id of the front's own requests (counting down from the 20-bit maximum: the game
// counts its own up from 0 per connection).
const injectBase = 0xFFFFF

// frontJoin is the front's Join state.
type frontJoin struct {
	wake chan struct{}

	mu       sync.Mutex
	pending  *pendingJoin
	notes    []string // chat lines waiting for a logged-in game
	noteSeq  int64
	lastHost string // the last host-side log line (not repeated)
	issueKey string // what the last issue was for (the connection, the tracker's join key)
	issuedAt time.Time
	said     map[string]bool // reasons logged once
}

// pendingJoin is a friend's Join waiting for its login.
type pendingJoin struct {
	s      joinSecret
	tag    string // the list's server (after placeJoin)
	at     time.Time
	noted  string // the server a "log in there" note was written for
	waited bool   // a "finish the match" note was written
	// after a redeem: the server's invites so far, the last one's time, the answer's message type the game sends
	// (0xA501C11E party, 0x55722FA1 custom game, 0xEBBABA1B practice group) and the answer seen ("": none yet)
	tries      int
	inviting   bool // a redeem is under way (its invite may be answered before its reply is read)
	invitedAt  time.Time
	answerType int64
	answer     string
}

// The game's answers to an invite (its sendMessage TYPE to the inviter, ATTR 0xB0002 "T", "F" or "I").
const (
	answerParty    = 0xA501C11E
	answerCustom   = 0x55722FA1
	answerPractice = 0xEBBABA1B
)

// answerTypeOf is the answer type for a redeem reply's KIND.
func answerTypeOf(kind int64) int64 {
	switch kind {
	case blaze.JoinKindCustomGame:
		return answerCustom
	case blaze.JoinKindPracticeGroup:
		return answerPractice
	}
	return answerParty
}

// afterLists is JoinAfterLists or the front's own; answerWait JoinAnswerWait or the front's own.
func (f *Front) afterLists() time.Duration {
	if f.JoinAfterLists > 0 {
		return f.JoinAfterLists
	}
	return JoinAfterLists
}

func (f *Front) answerWait() time.Duration {
	if f.JoinAnswerWait > 0 {
		return f.JoinAnswerWait
	}
	return JoinAnswerWait
}

// watchAnswer looks at a request the game sends for its answer to the server's invite (Messaging::sendMessage of
// the answer type with an invite number of the server's, JoinInviteFirst and up): the pending Join is then done.
func (f *Front) watchAnswer(fr fire.Frame) {
	if fr.Type != fire.Request || fr.Component != blaze.ComponentMessaging || fr.Command != blaze.CmdMsgSendMessage {
		return
	}
	f.join.mu.Lock()
	p := f.join.pending
	waiting := p != nil && (p.tries > 0 || p.inviting) && p.answer == ""
	f.join.mu.Unlock()
	if !waiting {
		return
	}
	defer guard()
	st, err := tdf.Decode(fr.Payload)
	if err != nil {
		return
	}
	m, ok := blaze.ClientMessageFromRequest(st)
	if !ok || (m.Type != answerParty && m.Type != answerCustom && m.Type != answerPractice) {
		return
	}
	v, _ := m.Attr(0xB0002)
	num, _ := m.Attr(0xB0005)
	if n, err := strconv.ParseInt(num, 10, 64); err != nil || n < blaze.JoinInviteFirst {
		return
	}
	f.join.mu.Lock()
	if f.join.pending == p {
		p.answer = firstNonEmpty(v, "?")
	}
	f.join.mu.Unlock()
	f.logf("discord join: the game answered the server's invite %s: %q", num, v)
	f.wakeJoin()
}

// wakeJoin asks joinLoop to look again (never blocks).
func (f *Front) wakeJoin() {
	if f.join.wake == nil {
		return
	}
	select {
	case f.join.wake <- struct{}{}:
	default:
	}
}

// DiscordJoinSecret takes a friend's Join from Discord (the app's ACTIVITY_JOIN): kept until the player is logged in
// on its server.
func (f *Front) DiscordJoinSecret(secret string) {
	if !f.DiscordJoin {
		f.logf("discord join: a Join from Discord, but discord_join is off (ignored)")
		return
	}
	s, err := parseJoinSecret(secret)
	if err != nil {
		f.logf("discord join: a Join from Discord that can't be used: %v", err)
		f.queueNote("This Join from Discord can't be used (" + err.Error() + ").")
		f.wakeJoin()
		return
	}
	f.logf("discord join: a Join from Discord for %s (key %s)", net.JoinHostPort(s.Address, strconv.Itoa(s.Port)), s.Pin.Fingerprint())
	f.join.mu.Lock()
	f.join.pending = &pendingJoin{s: s, at: f.Now()}
	f.join.mu.Unlock()
	f.wakeJoin()
}

// queueNote keeps a chat line for the game (written once it is logged in).
func (f *Front) queueNote(text string) {
	f.join.mu.Lock()
	f.join.notes = append(f.join.notes, text)
	f.join.mu.Unlock()
}

// once logs a line once per run (by key).
func (f *Front) joinOnce(key, format string, a ...any) {
	f.join.mu.Lock()
	if f.join.said == nil {
		f.join.said = map[string]bool{}
	}
	seen := f.join.said[key]
	f.join.said[key] = true
	f.join.mu.Unlock()
	if !seen {
		f.logf(format, a...)
	}
}

// joinLoop issues the player's join code and redeems a friend's Join, one step at a time.
func (f *Front) joinLoop() {
	defer f.wg.Done()
	refresh := f.joinRefresh()
	settle := f.JoinSettle
	if settle <= 0 {
		settle = joinSettle
	}
	tick := time.NewTicker(refresh)
	defer tick.Stop()
	for {
		select {
		case <-f.done:
			return
		case <-f.join.wake:
		case <-tick.C:
		}
		select {
		case <-f.done:
			return
		case <-time.After(settle):
		}
		select {
		case <-f.join.wake:
		default:
		}
		f.joinHost()
		f.joinGuest()
	}
}

// authedConn is the game's connection that is logged in on the current session's server (nil: none).
func (f *Front) authedConn() *blazeConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authed
}

func (f *Front) setAuthed(bc *blazeConn, on bool) {
	f.mu.Lock()
	switch {
	case on:
		f.authed = bc
	case f.authed == bc:
		f.authed = nil
	}
	f.mu.Unlock()
	f.wakeJoin()
}

// joinServer is the server a friend's Start-Client must reach for the session: its address for players, gateway
// port and key; why says why there is none.
func (f *Front) joinServer(s *frontSession) (string, int, tunnel.Pin, string) {
	b := s.b
	pin, ok := f.trustedPin(b)
	if !ok {
		return "", 0, pin, "no trusted key for " + b.Address
	}
	addr, port := b.Address, b.GatewayPort
	if b.Tag == ThisPCTag && f.ThisPC != nil {
		o, held := f.ThisPC.running()
		if !held {
			return "", 0, pin, "This PC's server runs inside Start-Client and listens on this PC only"
		}
		addr, port = o.Address, o.GatewayPort
	}
	if port == 0 {
		port = tunnel.DefaultPort
	}
	if ip := net.ParseIP(addr); (ip != nil && ip.IsLoopback() || strings.EqualFold(addr, "localhost")) && !f.JoinLoopback {
		return "", 0, pin, "the server's address " + addr + " is this PC's own: friends elsewhere can't reach it"
	}
	return addr, port, pin, ""
}

// joinHost asks the server for the player's join code and puts the secret into the activity.
func (f *Front) joinHost() {
	t := f.Presence
	want, ok, key := t.JoinTarget()
	bc := f.authedConn()
	if !ok || bc == nil || !bc.personaKnown() {
		t.setJoin(nil)
		f.join.mu.Lock()
		f.join.issueKey = ""
		f.join.mu.Unlock()
		return
	}
	// asked again only when the party or lobby changed, for another connection, or after JoinRefresh
	key = fmt.Sprintf("%p|%s", bc, key)
	f.join.mu.Lock()
	same := key == f.join.issueKey && f.Now().Sub(f.join.issuedAt) < f.joinRefresh()-time.Second
	f.join.mu.Unlock()
	if same {
		return
	}
	_, s := bc.upstreamOf()
	if s == nil || bc.joinCodesMissing() {
		t.setJoin(nil)
		return
	}
	addr, port, pin, why := f.joinServer(s)
	if why != "" {
		f.joinOnce("host:"+why, "discord join: no Join for friends: %s", why)
		t.setJoin(nil)
		return
	}
	rep, err := bc.inject(blaze.CmdJoinCodesIssue, blaze.JoinCodeIssueRequest(want.target, want.id))
	if err != nil {
		f.logf("discord join: issueJoinCode: %v", err)
		t.setJoin(nil)
		return
	}
	if rep.Type == fire.ErrorReply {
		bc.markJoinCodesMissing()
		f.logf("discord join: %s has no join codes (error %#04x: a server of an older version): no Join for friends", s.b.Tag, rep.Error)
		t.setJoin(nil)
		return
	}
	info, err := blaze.ParseJoinCodeIssueReply(rep.Payload)
	if err != nil {
		f.logf("discord join: issueJoinCode's reply: %v", err)
		t.setJoin(nil)
		return
	}
	j := &issuedJoin{want: want, info: info}
	if info.Code != "" && info.Joinable && info.Reason == blaze.JoinOK && info.Max > info.Size {
		sec := joinSecret{Address: addr, Port: port, Pin: pin, Code: info.Code}.String()
		if len(sec) <= discord.MaxSecret {
			j.secret, j.partyID = sec, partyIDOf(info.Code)
		} else {
			f.joinOnce("long:"+addr, "discord join: the server's address %s is too long for a Discord join secret", addr)
		}
	}
	kind := "party"
	if want.target == blaze.JoinTargetGame {
		kind = "lobby"
	}
	line := fmt.Sprintf("discord join: %s %d on %s: %d of %d, %s, Join %s", kind, want.id, s.b.Tag, info.Size, info.Max,
		blaze.JoinReasonName(info.Reason), map[bool]string{true: "on", false: "off"}[j.secret != ""])
	f.join.mu.Lock()
	repeat := line == f.join.lastHost
	f.join.lastHost = line
	f.join.mu.Unlock()
	if !repeat {
		f.logf("%s", line)
	}
	f.join.mu.Lock()
	f.join.issueKey, f.join.issuedAt = key, f.Now()
	f.join.mu.Unlock()
	t.setJoin(j)
}

// joinRefresh is JoinRefresh or the front's own.
func (f *Front) joinRefresh() time.Duration {
	if f.JoinRefresh > 0 {
		return f.JoinRefresh
	}
	return JoinRefresh
}

// placeJoin finds or adds the secret's server in the list and trusts its key when none is trusted for it: the list's
// tag. A server in the list with another trusted key, or another gateway port, is refused.
func (f *Front) placeJoin(s joinSecret) (string, error) {
	b, ok := f.Bookmarks.Find(s.Address)
	if !ok || !strings.EqualFold(b.Address, s.Address) {
		var err error
		if b, err = f.Bookmarks.Add(Bookmark{Address: s.Address, GatewayPort: s.Port}); err != nil {
			return "", fmt.Errorf("adding %s to the server list: %v", s.Address, err)
		}
		f.logf("discord join: %s added to the server list as %s (from a Join)", b.Gateway(), b.Tag)
	}
	if p := b.GatewayPort; p != s.Port && !(p == 0 && s.Port == tunnel.DefaultPort) {
		return "", fmt.Errorf("your list has %s with the gateway port %d, the Join names %d", b.Address, p, s.Port)
	}
	known, ok := f.trustedPin(b)
	switch {
	case ok && !tunnel.SamePin(known, s.Pin):
		return "", fmt.Errorf("the Join names another key for %s (%s) than the one you trust (%s)", b.Address,
			s.Pin.Fingerprint(), known.Fingerprint())
	case !ok:
		if _, err := TrustGateway(b.Address, s.Pin, TrustOptions{Store: TrustedGateway(f.Layout, b.Address), Accept: true}); err != nil {
			return "", fmt.Errorf("storing %s's key: %v", b.Address, err)
		}
		f.logf("discord join: trusting %s's tunnel key %s (from a Join)", b.Address, s.Pin.Fingerprint())
	}
	return b.Tag, nil
}

// joinGuest moves a friend's Join on: the server into the list and preselected, then (logged in there, in the menu)
// the code redeemed; a note in the game's chat where the player must act.
func (f *Front) joinGuest() {
	bc := f.authedConn()
	ready := bc != nil && bc.joinReady(f.Now())
	if ready {
		f.join.mu.Lock()
		notes := f.join.notes
		f.join.notes = nil
		f.join.mu.Unlock()
		for _, n := range notes {
			f.note(bc, n)
		}
	}
	f.join.mu.Lock()
	p := f.join.pending
	f.join.mu.Unlock()
	if p == nil {
		return
	}
	drop := func() {
		f.join.mu.Lock()
		if f.join.pending == p {
			f.join.pending = nil
		}
		f.join.mu.Unlock()
	}
	if f.Now().Sub(p.at) > joinPendingTTL {
		f.logf("discord join: the Join for %s waited %s for a login there; dropped", p.s.Address, joinPendingTTL)
		drop()
		return
	}
	if p.tag == "" {
		tag, err := f.placeJoin(p.s)
		if err != nil {
			f.logf("discord join: refused: %v", err)
			drop()
			f.queueNote("This Join from Discord can't be used: " + err.Error() + ".")
			if ready {
				f.wakeJoin()
			}
			return
		}
		p.tag = tag
		f.setDefault(tag)
		f.wantList()
	}
	if bc == nil {
		return // the login screen: the server is preselected, the Join waits for the login
	}
	_, s := bc.upstreamOf()
	if s == nil {
		return
	}
	b, _ := f.Bookmarks.Find(p.tag)
	if s.b.Tag != p.tag {
		if ready && p.noted != s.b.Tag {
			p.noted = s.b.Tag
			f.logf("discord join: logged in on %s, the Join is for %s: a note to log out and in there", s.b.Tag, p.tag)
			f.note(bc, fmt.Sprintf("To join from Discord, log out and log in on %s (it is selected in the server list).", firstNonEmpty(b.Name, b.Address)))
		}
		return
	}
	if !ready {
		return
	}
	if p.tries > 0 { // invited: the game's answer ends it; none within answerWait: invited again
		f.join.mu.Lock()
		answer := p.answer
		f.join.mu.Unlock()
		switch {
		case answer == "I":
			drop()
			f.note(bc, "Your game couldn't take the invite from Discord just now. Click Join on Discord again.")
			return
		case answer != "":
			drop()
			return
		case f.Now().Sub(p.invitedAt) < f.answerWait():
			return
		case p.tries >= joinTries:
			drop()
			f.logf("discord join: no answer to %d invites; given up", p.tries)
			f.note(bc, "The invite from Discord didn't show up. Click Join on Discord again.")
			return
		}
		f.logf("discord join: no answer to the invite within %s; inviting again (%d of %d)", f.answerWait(), p.tries+1, joinTries)
	}
	if !f.Presence.inMenu() {
		if !p.waited {
			p.waited = true
			f.note(bc, "Your Join from Discord follows when you are back in the menu.")
		}
		return
	}
	if bc.joinCodesMissing() {
		drop()
		f.note(bc, "This server can't take a Join from Discord (it runs an older version).")
		return
	}
	// armed before the request: the server's invite follows its reply at once on the stream, and the game's answer
	// may come before this goroutine has read the reply
	f.join.mu.Lock()
	p.inviting = true
	f.join.mu.Unlock()
	bc.expectInvite(true)
	rep, err := bc.inject(blaze.CmdJoinCodesRedeem, blaze.JoinCodeRedeemRequest(p.s.Code))
	ok := err == nil && rep.Type == fire.Reply
	var r blaze.JoinCodeRedeemed
	if ok {
		if r, err = blaze.ParseJoinCodeRedeemReply(rep.Payload); err != nil || r.Reason != blaze.JoinOK {
			ok = false
		}
	}
	if !ok {
		bc.expectInvite(false)
		f.join.mu.Lock()
		p.inviting = false
		f.join.mu.Unlock()
	}
	if err != nil {
		f.logf("discord join: redeemJoinCode: %v", err)
		return // tried again at the next wake
	}
	if rep.Type == fire.ErrorReply {
		bc.markJoinCodesMissing()
		drop()
		f.logf("discord join: %s has no join codes (error %#04x)", s.b.Tag, rep.Error)
		f.note(bc, "This server can't take a Join from Discord (it runs an older version).")
		return
	}
	if r, err = blaze.ParseJoinCodeRedeemReply(rep.Payload); err != nil {
		f.logf("discord join: redeemJoinCode's reply: %v", err)
		drop()
		return
	}
	if r.Reason == blaze.JoinNotLoggedIn {
		return // not yet: the next wake
	}
	f.logf("discord join: redeemed on %s: %d %d, %s", s.b.Tag, r.Kind, r.ID, blaze.JoinReasonName(r.Reason))
	if r.Reason == blaze.JoinOK { // the server's invite follows on the connection: wait for the game's answer
		f.join.mu.Lock()
		p.tries++
		p.invitedAt, p.answerType = f.Now(), answerTypeOf(r.Kind)
		f.join.mu.Unlock()
		time.AfterFunc(f.answerWait()+50*time.Millisecond, f.wakeJoin)
		return
	}
	drop()
	if p.tries > 0 && r.Reason == blaze.JoinAlreadyIn {
		return // the game joined from an earlier invite whose answer wasn't seen
	}
	f.note(bc, "Join from Discord: "+joinRefusal(r.Reason))
}

// joinRefusal is the chat's line for a redeem's reason.
func joinRefusal(rsn int64) string {
	switch rsn {
	case blaze.JoinGone:
		return "that party or lobby doesn't exist any more."
	case blaze.JoinFull:
		return "it is full."
	case blaze.JoinClosed:
		return "the party is in the queue; try again when it is back."
	case blaze.JoinPrivate:
		return "it is a private game."
	case blaze.JoinStarted:
		return "its match has started."
	case blaze.JoinAlreadyIn:
		return "you are in it already."
	case blaze.JoinBlocked:
		return "you blocked its leader."
	case blaze.JoinRateLimited:
		return "too many messages just now; click Join again in a moment."
	}
	return "it can't be joined now (" + blaze.JoinReasonName(rsn) + ")."
}

// note writes a line into the game's chat: a broadcast system message (TYPE 300, TAG 2) shown as a "System" line in
// the menu, shaper select and a match, with an id of the front's own (the server's broadcasts count from 1).
func (f *Front) note(bc *blazeConn, text string) {
	f.join.mu.Lock()
	f.join.noteSeq++
	id := blaze.JoinInviteFirst + f.join.noteSeq
	f.join.mu.Unlock()
	msg := blaze.SystemMessage(blaze.SystemTagBroadcast, []blaze.MsgAttr{
		{Key: blaze.BroadcastAttrID, Value: strconv.FormatInt(id, 10)},
		{Key: blaze.BroadcastAttrFlags, Value: strconv.Itoa(blaze.BroadcastChatLine)},
		{Key: blaze.BroadcastAttrSeconds, Value: strconv.Itoa(joinNoteSeconds)},
		{Key: blaze.BroadcastAttrText, Value: text},
	})
	raw, err := fire.Encode(fire.Frame{Component: blaze.ComponentMessaging, Command: blaze.NotifyMessageID, Type: fire.Notification,
		Payload: blaze.SystemMessageNotification(id, msg, f.Now().Unix())})
	if err == nil {
		err = bc.write(raw)
	}
	if err != nil {
		f.logf("discord join: the note wasn't written: %v", err)
		return
	}
	f.logf("discord join: note in the game's chat: %s", text)
}

// setDefault preselects a server (the login screen's list and a login without a tag).
func (f *Front) setDefault(tag string) {
	f.defMu.Lock()
	f.Default = tag
	f.defMu.Unlock()
}

// preselected is Default under its lock.
func (f *Front) preselected() string {
	f.defMu.Lock()
	defer f.defMu.Unlock()
	return f.Default
}

// The connection's side of Join: the front's own requests on it and the login's progress.

// inject sends a join-code request on the connection's server side and waits for its reply, which the server
// reader takes off the stream (injected).
func (bc *blazeConn) inject(cmd uint16, payload []byte) (fire.Frame, error) {
	up := bc.upstream()
	if up == nil {
		return fire.Frame{}, errors.New("no server connection")
	}
	ch := make(chan fire.Frame, 1)
	bc.imu.Lock()
	if bc.waiting == nil {
		bc.waiting = map[uint32]chan fire.Frame{}
	}
	id := uint32(injectBase - bc.injected%0x10000)
	bc.injected++
	bc.waiting[id] = ch
	bc.imu.Unlock()
	defer func() {
		bc.imu.Lock()
		delete(bc.waiting, id)
		bc.imu.Unlock()
	}()
	raw, err := fire.Encode(fire.Frame{Component: blaze.ComponentJoinCodes, Command: cmd, Type: fire.Request, ID: id, Payload: payload})
	if err != nil {
		return fire.Frame{}, err
	}
	if err := bc.writeUp(up, raw); err != nil {
		return fire.Frame{}, err
	}
	select {
	case fr := <-ch:
		return fr, nil
	case <-time.After(injectWait):
		return fire.Frame{}, fmt.Errorf("no reply within %s", injectWait)
	case <-bc.f.done:
		return fire.Frame{}, errors.New("the front stopped")
	}
}

// injected takes a join-code frame off the server's stream (the game never asked for one): the reply to a request
// of the front's.
func (bc *blazeConn) injectedReply(fr fire.Frame) {
	bc.imu.Lock()
	ch := bc.waiting[fr.ID]
	bc.imu.Unlock()
	if ch == nil {
		bc.f.logf("Blaze %s: a join-code frame nobody waits for (id %#x; dropped)", bc.peer, fr.ID)
		return
	}
	select {
	case ch <- fr:
	default:
	}
}

// writeUp writes one whole frame to the server (the game's frames and the front's own don't interleave).
func (bc *blazeConn) writeUp(up net.Conn, raw []byte) error {
	bc.upw.Lock()
	defer bc.upw.Unlock()
	_, err := up.Write(raw)
	return err
}

// News overlay states of a login (blazeConn.news): not known (no extended data of the own user seen: an older
// server, a resumed session), clear (the game shows its news overlay over the main menu), set (closed, or seen at an
// earlier login).
const (
	newsUnknown = iota
	newsClear
	newsSet
)

// watchLogin follows the login on the server's frames: the persona's login (or a resumed session), the friends
// lists, and the own user's UserSessionExtendedDataUpdate, whose UATT bit 62 says whether the news overlay was
// closed (blaze.UserInfoNewsSeen: the game sets it when the player closes the overlay).
func (bc *blazeConn) watchLogin(fr fire.Frame) {
	var wake time.Duration
	switch {
	case fr.Type == fire.Reply && fr.Component == blaze.ComponentAuthentication && fr.Command == blaze.CmdAuthLoginPersona:
		own := int64(0)
		if st, err := tdf.Decode(fr.Payload); err == nil {
			if p, ok := st.Sub("PDTL"); ok {
				own, _ = p.Int("PID")
			}
		}
		bc.imu.Lock()
		bc.personaAt, bc.listsAt, bc.persona, bc.news, bc.newsAt = bc.f.Now(), time.Time{}, own, newsUnknown, time.Time{}
		bc.imu.Unlock()
		wake = joinAfterPersona
	case fr.Type == fire.Reply && fr.Component == blaze.ComponentUserSessions && fr.Command == cmdUserSessionsResume:
		bc.imu.Lock()
		bc.personaAt, bc.listsAt, bc.persona, bc.news, bc.newsAt = bc.f.Now(), time.Time{}, 0, newsUnknown, time.Time{}
		bc.imu.Unlock()
		wake = joinAfterPersona
	case fr.Type == fire.Reply && fr.Component == blaze.ComponentAssociationLists && fr.Command == blaze.CmdALGetLists:
		bc.imu.Lock()
		if !bc.personaAt.IsZero() && bc.listsAt.IsZero() {
			bc.listsAt = bc.f.Now()
			wake = bc.f.afterLists()
		}
		bc.imu.Unlock()
	case fr.Type == fire.Notification && fr.Component == blaze.ComponentUserSessions && fr.Command == blaze.NotifyUserSessionExtendedDataUpdate:
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		id, _ := st.Int("USID")
		data, ok := st.Sub("DATA")
		if !ok {
			return
		}
		uatt, _ := data.Int("UATT")
		bc.imu.Lock()
		if bc.persona != 0 && id == bc.persona {
			wake = bc.noteNewsLocked(uint64(uatt)&blaze.UserInfoNewsSeen != 0)
		}
		bc.imu.Unlock()
	}
	if wake > 0 {
		bc.f.wakeJoin()
		time.AfterFunc(wake+50*time.Millisecond, bc.f.wakeJoin)
	}
}

// watchGame looks at the game's requests for the news overlay's dismissal: setUserInfoAttribute with bit 62.
func (bc *blazeConn) watchGame(fr fire.Frame) {
	if fr.Type != fire.Request || fr.Component != blaze.ComponentUserSessions || fr.Command != blaze.CmdUserSessionsSetUserInfoAttribute {
		return
	}
	st, err := tdf.Decode(fr.Payload)
	if err != nil {
		return
	}
	v, _ := st.Int("ATTV")
	if uint64(v)&blaze.UserInfoNewsSeen == 0 {
		return
	}
	bc.imu.Lock()
	wake := bc.noteNewsLocked(true)
	bc.imu.Unlock()
	if wake > 0 {
		bc.f.wakeJoin()
		time.AfterFunc(wake+50*time.Millisecond, bc.f.wakeJoin)
	}
}

// noteNewsLocked records the news overlay's state (bc.imu held) and returns when to look again (0: no change).
func (bc *blazeConn) noteNewsLocked(seen bool) time.Duration {
	switch {
	case seen && bc.news != newsSet:
		if bc.news == newsClear {
			bc.f.logf("discord join: the game's news overlay was closed")
		}
		bc.news, bc.newsAt = newsSet, bc.f.Now()
		return joinAfterNews
	case !seen && bc.news == newsUnknown:
		bc.news = newsClear
		bc.f.logf("discord join: the game shows its news overlay; an invite waits until it is closed")
		return joinNewsWait
	}
	return 0
}

// expectInvite notes (or, for a refused redeem, forgets) a redeem whose invite (a NotifyMessage) comes right
// after its reply on the connection; seenInvite takes it. It is set before the request goes out: the server reader
// passes the invite on as soon as it has taken the reply off the stream, before the redeem's goroutine reads it.
func (bc *blazeConn) expectInvite(on bool) {
	bc.imu.Lock()
	bc.inviteDue = on
	bc.imu.Unlock()
}

// seenInvite logs the first NotifyMessage after a redeem: the server's invite, passed to the game.
func (bc *blazeConn) seenInvite(fr fire.Frame) {
	if fr.Type != fire.Notification || fr.Component != blaze.ComponentMessaging || fr.Command != blaze.NotifyMessageID {
		return
	}
	bc.imu.Lock()
	due := bc.inviteDue
	bc.imu.Unlock()
	if !due {
		return
	}
	st, err := tdf.Decode(fr.Payload)
	if err != nil {
		return
	}
	p, _ := st.Sub("PYLD")
	if typ, _ := p.Int("TYPE"); typ != 0x176A0D2B && typ != 0x237748AA && typ != 0x8FBFA37C {
		return // another message (a chat line) between the request and its invite
	}
	bc.imu.Lock()
	bc.inviteDue = false
	bc.imu.Unlock()
	bc.f.logf("discord join: the server's invite (NotifyMessage, %d bytes) passed to the game", len(fr.Payload))
}

// loggedOut forgets the login (the game's logout).
func (bc *blazeConn) loggedOut() {
	bc.imu.Lock()
	bc.personaAt, bc.listsAt, bc.persona, bc.news, bc.newsAt = time.Time{}, time.Time{}, 0, newsUnknown, time.Time{}
	bc.imu.Unlock()
}

func (bc *blazeConn) personaKnown() bool {
	bc.imu.Lock()
	defer bc.imu.Unlock()
	return !bc.personaAt.IsZero()
}

// joinReady says whether the game is far enough past its login for an invite (and a chat line).
func (bc *blazeConn) joinReady(now time.Time) bool {
	bc.imu.Lock()
	defer bc.imu.Unlock()
	if bc.personaAt.IsZero() {
		return false
	}
	switch bc.news {
	case newsSet: // the overlay closed (or never shown): a moment for the menu, after the friends lists
		return !now.Before(bc.newsAt.Add(joinAfterNews)) && (!bc.listsAt.IsZero() || !now.Before(bc.personaAt.Add(joinAfterPersona)))
	case newsClear: // the overlay shows: an invite then is lost; wait for its dismissal, at most joinNewsWait
		return !now.Before(bc.personaAt.Add(joinNewsWait))
	}
	// not known (an older server, a resumed session): the timer
	if !bc.listsAt.IsZero() && !now.Before(bc.listsAt.Add(bc.f.afterLists())) {
		return true
	}
	return !now.Before(bc.personaAt.Add(joinAfterPersona))
}

func (bc *blazeConn) joinCodesMissing() bool {
	bc.imu.Lock()
	defer bc.imu.Unlock()
	return bc.noJoinCodes
}

func (bc *blazeConn) markJoinCodesMissing() {
	bc.imu.Lock()
	bc.noJoinCodes = true
	bc.imu.Unlock()
}
