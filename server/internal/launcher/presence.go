package launcher

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/discord"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
)

// The player's Discord status (internal/discord): what the game does, read from the Blaze frames the front relays.
// The tracker only decodes copies of frames it is shown (fromGame, fromServer) and never changes the relay; a frame
// that doesn't decode changes nothing. Its states and their frames (docs: reference.md, playing.md):
//
//	login screen   the redirector's request (the game's start, each logout: Front.setAtLogin), logout
//	the menu       the login reply (Authentication::login; the own persona id from PLST, loginPersona's PDTL),
//	               NotifyPlayerRemoved (GameManager 0x28) for the own id, a cancelled or failed queue
//	in a party     NotifyJoinPlaygroup 0x33 / MemberJoined 0x34 / MemberRemoved 0x35 / Destroy 0x32 of a playgroup
//	               without a chat key (the game's chats GG-<id>, TG-<id>-<team> are playgroups too), leavePlaygroup;
//	               in the menu with a party the game shows the PvP lobby ("In the lobby · PvP")
//	in queue       startMatchmaking (0x0D; out on cancelMatchmaking 0x0E, its error reply or NotifyMatchmakingFailed
//	               0x0A); a party's guests by the leader's MatchmakingStatus attribute (NotifyPlaygroupAttributesSet)
//	shaper select  NotifyGameSetup (0x14) in PRE_GAME; a custom game's or practice group's: its lobby
//	in match       NotifyGameStateChange (0x64) IN_GAME (0x83): the timer starts then
//	post-game      NotifyGameStateChange POST_GAME (4)
//
// The mode comes from the game's GameFlags attribute (cGameEntryManager::Game_Create): bit 1 matched (PvP), 4 solo
// (practice against bots, 0x40C/0x50C), 0x412 a practice group, 2 a custom game.

// DiscordAppIDKey and DiscordKey are Start-Client.ini's keys for the status.
const (
	DiscordKey      = "discord"
	DiscordAppIDKey = "discord_app_id"
	DiscordJoinKey  = "discord_join"
	// discordTestAppID is the application id sent to a test hook's fake app when none is configured.
	discordTestAppID = "000000000000000000"
)

type presenceState int

const (
	psLogin presenceState = iota
	psMenu
	psQueue
	psLobby
	psSetup
	psMatch
	psPost
)

// partyMax is a party's size when its playgroup names none (the PvP lobby's MLIM 5).
const partyMax = 5

type trackedParty struct {
	members map[int64]bool
	max     int
	seq     int // the order the parties were joined in (the last one is shown)
}

// presenceTracker turns the relayed Blaze frames into the Discord activity.
type presenceTracker struct {
	mu  sync.Mutex
	now func() time.Time
	out func(*discord.Activity) // nil until the game runs (setOutput)

	own     int64
	state   presenceState
	mode    string
	since   time.Time
	game    int64
	mmID    uint32
	mmWait  bool
	parties map[int64]*trackedParty
	seq     int
	last    *discord.Activity

	// Join from Discord (front_join.go): issued is the front's last join code for the target joinTarget names (shown
	// while that is still the target), lobbyRev counts the lobby's roster changes, joinKey is what the front last
	// heard about, changed wakes the front (never blocks; nil: no Join)
	issued   *issuedJoin
	lobbyRev int
	joinKey  string
	changed  func()
}

func newPresenceTracker(now func() time.Time) *presenceTracker {
	if now == nil {
		now = time.Now
	}
	return &presenceTracker{now: now, parties: map[int64]*trackedParty{}}
}

// setOutput starts sending the activity to out (the game runs), the current one first.
func (t *presenceTracker) setOutput(out func(*discord.Activity)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out = out
	t.last = nil
	t.publish()
}

// Activity is the activity for the current state.
func (t *presenceTracker) Activity() *discord.Activity {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activity()
}

func (t *presenceTracker) activity() *discord.Activity {
	a := &discord.Activity{LargeImage: "logo", LargeText: "Dawngate"}
	with := func(s string) string {
		if t.mode == "" {
			return s
		}
		return s + " · " + t.mode
	}
	switch t.state {
	case psLogin:
		a.Details = "At the login screen"
		return a
	case psMenu:
		a.Details = "In the menu"
		if t.party() != nil { // the game shows the PvP lobby while it holds a party (alone too)
			a.Details = "In the lobby · PvP"
		}
	case psQueue:
		a.Details, a.Start = with("In queue"), t.since
	case psLobby:
		a.Details = with("In the lobby")
	case psSetup:
		a.Details = with("Shaper select")
	case psMatch:
		a.Details, a.Start = with("In match"), t.since
	case psPost:
		a.Details = with("Post-game")
	}
	if p := t.party(); p != nil {
		a.State, a.PartySize, a.PartyMax = "In a party", len(p.members), p.max
		if a.PartyMax < a.PartySize {
			a.PartyMax = a.PartySize
		}
	}
	if j := t.issued; j != nil && j.secret != "" {
		if want, ok := t.joinTarget(); ok && want == j.want {
			a.PartyID, a.JoinSecret = j.partyID, j.secret
			a.PartySize, a.PartyMax = int(j.info.Size), int(j.info.Max)
			if a.State == "" {
				a.State = "Open to join"
			}
		}
	}
	return a
}

// joinTarget is what a friend's Join would join now: the lobby of a practice group or custom game while it shows,
// else the party in the menu or the queue; none at the login screen, in shaper select, a match or its post-game.
func (t *presenceTracker) joinTarget() (joinWant, bool) {
	switch t.state {
	case psLobby:
		if t.game != 0 && (t.mode == "Practice group" || t.mode == "Custom game") {
			return joinWant{blaze.JoinTargetGame, t.game}, true
		}
	case psMenu, psQueue:
		var best int64
		seq := -1
		for id, p := range t.parties {
			if p.seq > seq {
				best, seq = id, p.seq
			}
		}
		if seq >= 0 {
			return joinWant{blaze.JoinTargetParty, best}, true
		}
	}
	return joinWant{}, false
}

// JoinTarget is joinTarget for the front, with the key of what it last heard (noteJoinChange).
func (t *presenceTracker) JoinTarget() (joinWant, bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	w, ok := t.joinTarget()
	return w, ok, t.joinKey
}

// inMenu says whether the game shows the main menu or a lobby (where an invite's pop-up can be answered).
func (t *presenceTracker) inMenu() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == psMenu || t.state == psLobby
}

// setJoin keeps the front's join code (nil: none) and sends the activity again when it changed.
func (t *presenceTracker) setJoin(j *issuedJoin) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.issued = j
	t.publish()
}

// noteJoinChange wakes the front when the join target, the state or the members changed since it last heard.
func (t *presenceTracker) noteJoinChange() {
	if t.changed == nil {
		return
	}
	want, ok := t.joinTarget()
	members := 0
	if p := t.party(); p != nil {
		members = len(p.members)
	}
	key := fmt.Sprintf("%d|%v|%d|%d|%d", t.state, ok, want.id, members, t.lobbyRev)
	if key != t.joinKey {
		t.joinKey = key
		t.changed()
	}
}

// party is the party shown: the last one joined.
func (t *presenceTracker) party() *trackedParty {
	var best *trackedParty
	for _, p := range t.parties {
		if best == nil || p.seq > best.seq {
			best = p
		}
	}
	return best
}

func (t *presenceTracker) publish() {
	t.noteJoinChange()
	if t.out == nil {
		return
	}
	a := t.activity()
	if t.last != nil && *t.last == *a {
		return
	}
	t.last = a
	t.out(a)
}

func (t *presenceTracker) toMenu() {
	t.state, t.mode, t.since, t.game, t.mmWait = psMenu, "", time.Time{}, 0, false
}

// atLogin: the login screen shows (on; the front's setAtLogin), or a login went through (off).
func (t *presenceTracker) atLogin(on bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if on {
		t.toLogin()
	} else if t.state == psLogin {
		t.toMenu()
	}
	t.publish()
}

func (t *presenceTracker) toLogin() {
	t.toMenu()
	t.state = psLogin
	t.parties = map[int64]*trackedParty{}
}

// guard keeps a frame that doesn't decode as expected from ever reaching the relay.
func guard() { recover() }

// fromGame looks at a frame the game sent (before the front passes it on).
func (t *presenceTracker) fromGame(fr fire.Frame) {
	if t == nil || fr.Type != fire.Request {
		return
	}
	defer guard()
	switch {
	case fr.Component == blaze.ComponentGameManager && fr.Command == blaze.CmdGameManagerStartMatchmaking:
		mode := "PvP"
		if st, err := tdf.Decode(fr.Payload); err == nil {
			if f, ok := gameFlags(st); ok {
				if m := modeOf(f); m != "" {
					mode = m
				}
			}
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state != psQueue {
			t.since = t.now()
		}
		t.state, t.mode, t.mmID, t.mmWait = psQueue, mode, fr.ID, true
		t.publish()
	case fr.Component == blaze.ComponentGameManager && fr.Command == blaze.CmdGameManagerCancelMatchmaking:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == psQueue {
			t.toMenu()
			t.publish()
		}
	case fr.Component == blaze.ComponentPlaygroups && fr.Command == blaze.CmdPlaygroupsLeavePlaygroup:
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		id, ok := st.Int("PGID")
		if !ok {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.parties[id]; ok {
			delete(t.parties, id)
			t.publish()
		}
	case fr.Component == blaze.ComponentAuthentication && fr.Command == blaze.CmdAuthLogout:
		t.mu.Lock()
		defer t.mu.Unlock()
		t.toLogin()
		t.publish()
	}
}

// fromServer looks at a frame the server sent (before the front passes it to the game).
func (t *presenceTracker) fromServer(fr fire.Frame) {
	if t == nil || fr.Type == fire.Request {
		return
	}
	defer guard()
	switch fr.Component {
	case blaze.ComponentAuthentication:
		t.authFrame(fr)
	case blaze.ComponentGameManager:
		t.gameFrame(fr)
	case blaze.ComponentPlaygroups:
		if fr.Type == fire.Notification {
			t.playgroupFrame(fr)
		}
	case blaze.ComponentUserSessions:
		if fr.Type == fire.Reply && fr.Command == cmdUserSessionsResume { // a lost connection resumed: past the login
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.state == psLogin {
				t.toMenu()
				t.publish()
			}
		}
	}
}

func (t *presenceTracker) authFrame(fr fire.Frame) {
	if fr.Type != fire.Reply {
		return
	}
	var own int64
	switch fr.Command {
	case blaze.CmdAuthLogin: // LoginResponse: PLST, the account's personas (one: the player's)
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		f, ok := st.Get("PLST")
		if !ok {
			return
		}
		if l, ok := f.Value.(*tdf.List); ok && len(l.Items) > 0 {
			if p, ok := l.Items[0].(*tdf.Struct); ok {
				own, _ = p.Int("PID")
			}
		}
	case blaze.CmdAuthLoginPersona: // SessionInfo: PDTL, the persona logged in
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		if p, ok := st.Sub("PDTL"); ok {
			own, _ = p.Int("PID")
		}
	default:
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if own != 0 {
		t.own = own
	}
	if fr.Command == blaze.CmdAuthLogin && t.state == psLogin {
		t.toMenu()
		t.publish()
	}
}

func (t *presenceTracker) gameFrame(fr fire.Frame) {
	if fr.Type == fire.ErrorReply && fr.Command == blaze.CmdGameManagerStartMatchmaking {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == psQueue && t.mmWait && fr.ID == t.mmID {
			t.toMenu()
			t.publish()
		}
		return
	}
	if fr.Type != fire.Notification {
		return
	}
	switch fr.Command {
	default: // the lobby's roster or attributes changed: its join code is issued again (size, private, full)
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		gid, ok := st.Int("GID")
		t.mu.Lock()
		defer t.mu.Unlock()
		if ok && t.state == psLobby && gid == t.game && gid != 0 {
			t.lobbyRev++
			t.publish()
		}
	case blaze.NotifyMatchmakingFailedID:
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state == psQueue {
			t.toMenu()
			t.publish()
		}
	case blaze.NotifyGameSetupID:
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		g, ok := st.Sub("GAME")
		if !ok {
			return
		}
		gid, _ := g.Int("GID")
		gsta, _ := g.Int("GSTA")
		flags, known := gameFlags(g)
		mode := ""
		if known {
			mode = modeOf(flags)
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		same := t.game == gid && gid != 0
		t.game, t.mode, t.mmWait = gid, mode, false
		switch {
		case gsta == blaze.GameStateInGame: // a rejoin of a running match
			if !same || t.state != psMatch {
				t.state, t.since = psMatch, t.now()
			}
		case gsta == blaze.GameStatePostGame:
			t.state = psPost
		case known && blaze.IsLobbyGame(flags):
			t.state, t.since = psLobby, time.Time{}
		default:
			t.state, t.since = psSetup, time.Time{}
		}
		t.publish()
	case blaze.NotifyGameStateChangeID:
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		gid, ok1 := st.Int("GID")
		gsta, ok2 := st.Int("GSTA")
		if !ok1 || !ok2 {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.game != 0 && gid != t.game {
			return
		}
		switch gsta {
		case blaze.GameStateInGame:
			if t.state != psMatch {
				t.state, t.since = psMatch, t.now()
			}
		case blaze.GameStatePostGame:
			t.state, t.since = psPost, time.Time{}
		default:
			return
		}
		t.game = gid
		t.publish()
	case blaze.NotifyPlayerRemovedID:
		st, err := tdf.Decode(fr.Payload)
		if err != nil {
			return
		}
		pid, ok := st.Int("PID")
		gid, _ := st.Int("GID")
		t.mu.Lock()
		defer t.mu.Unlock()
		if !ok || t.own == 0 || pid != t.own || (t.game != 0 && gid != t.game) {
			return // another player left the game
		}
		if t.state >= psLobby {
			t.toMenu()
			t.publish()
		}
	}
}

// chatKey says whether a playgroup's unique key is one of the game's chats (not a party).
func chatKey(key string) bool {
	return strings.HasPrefix(key, "GG-") || strings.HasPrefix(key, "TG-")
}

func memberID(m *tdf.Struct) (int64, bool) {
	u, ok := m.Sub("USER")
	if !ok {
		return 0, false
	}
	return u.Int("ID")
}

func (t *presenceTracker) playgroupFrame(fr fire.Frame) {
	st, err := tdf.Decode(fr.Payload)
	if err != nil {
		return
	}
	switch fr.Command {
	case blaze.NotifyJoinPlaygroupID: // INFO {PGID, UKEY, MLIM}, MLST members, USER the joiner (this player)
		info, ok := st.Sub("INFO")
		if !ok {
			return
		}
		id, ok := info.Int("PGID")
		key, _ := info.Str("UKEY")
		if !ok || chatKey(key) {
			return
		}
		lim, _ := info.Int("MLIM")
		p := &trackedParty{members: map[int64]bool{}, max: int(lim)}
		if p.max <= 0 {
			p.max = partyMax
		}
		if f, ok := st.Get("MLST"); ok {
			if l, ok := f.Value.(*tdf.List); ok {
				for _, it := range l.Items {
					if m, ok := it.(*tdf.Struct); ok {
						if mid, ok := memberID(m); ok {
							p.members[mid] = true
						}
					}
				}
			}
		}
		user, _ := st.Int("USER")
		t.mu.Lock()
		defer t.mu.Unlock()
		if user != 0 {
			p.members[user] = true
		}
		t.seq++
		p.seq = t.seq
		t.parties[id] = p
		t.publish()
	case blaze.NotifyMemberJoinedPlaygroupID: // MEMB {USER {ID}}, PGID
		id, ok := st.Int("PGID")
		m, ok2 := st.Sub("MEMB")
		if !ok || !ok2 {
			return
		}
		mid, ok := memberID(m)
		if !ok {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if p := t.parties[id]; p != nil {
			p.members[mid] = true
			t.publish()
		}
	case blaze.NotifyMemberRemovedFromPlaygroupID: // MLST the member's id, PGID
		id, ok := st.Int("PGID")
		mid, ok2 := st.Int("MLST")
		if !ok || !ok2 {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if p := t.parties[id]; p != nil {
			if mid == t.own && t.own != 0 {
				delete(t.parties, id)
			} else {
				delete(p.members, mid)
			}
			t.publish()
		}
	case blaze.NotifyDestroyPlaygroupID:
		id, ok := st.Int("PGID")
		if !ok {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.parties[id]; ok {
			delete(t.parties, id)
			t.publish()
		}
	case blaze.NotifyPlaygroupAttributesSetID: // the leader's MatchmakingStatus "<kGEMS>|<counter>|<ms>" (hex)
		id, ok := st.Int("PGID")
		if !ok {
			return
		}
		v, ok := mapString(st, "ATTR", "MatchmakingStatus")
		if !ok {
			return
		}
		gems, err := strconv.ParseUint(strings.SplitN(v, "|", 2)[0], 16, 32)
		if err != nil {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.parties[id] == nil {
			return
		}
		switch {
		case gems == 1 && t.state == psMenu: // a guest: the leader queues the party
			t.state, t.mode, t.since = psQueue, "PvP", t.now()
			t.publish()
		case gems == 0 && t.state == psQueue:
			t.toMenu()
			t.publish()
		}
	}
}

// mapString is a string map member's value for key.
func mapString(st *tdf.Struct, label, key string) (string, bool) {
	f, ok := st.Get(label)
	if !ok {
		return "", false
	}
	m, ok := f.Value.(*tdf.Map)
	if !ok {
		return "", false
	}
	for _, e := range m.Entries {
		if k, _ := e.Key.(string); k == key {
			v, ok := e.Value.(string)
			return v, ok
		}
	}
	return "", false
}

// gameFlags finds the GameFlags attribute (a decimal string) in a struct or anywhere below it: NotifyGameSetup's
// GAME.ATTR, startMatchmaking's game creation data.
func gameFlags(st *tdf.Struct) (int64, bool) {
	return findFlags(st, 0)
}

func findFlags(v any, depth int) (int64, bool) {
	if depth > 8 {
		return 0, false
	}
	switch x := v.(type) {
	case *tdf.Struct:
		if x == nil {
			return 0, false
		}
		for _, f := range x.Fields {
			if n, ok := findFlags(f.Value, depth+1); ok {
				return n, true
			}
		}
	case *tdf.Map:
		for _, e := range x.Entries {
			if k, _ := e.Key.(string); k == "GameFlags" {
				if s, ok := e.Value.(string); ok {
					if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
						return n, true
					}
				}
			}
			if n, ok := findFlags(e.Value, depth+1); ok {
				return n, true
			}
		}
	case *tdf.List:
		for _, it := range x.Items {
			if n, ok := findFlags(it, depth+1); ok {
				return n, true
			}
		}
	case *tdf.Union:
		if x.Field != nil {
			return findFlags(x.Field.Value, depth+1)
		}
	}
	return 0, false
}

// modeOf names a game's mode by its GameFlags ("": unknown).
func modeOf(flags int64) string {
	switch {
	case flags&blaze.GameFlagMatched != 0:
		return "PvP"
	case flags&blaze.GameFlagSolo != 0:
		return "Practice vs bots"
	case flags&blaze.GameFlagCustom != 0 && flags&0x10 != 0:
		return "Practice group"
	case flags&blaze.GameFlagCustom != 0:
		return "Custom game"
	}
	return ""
}

// discordConfig is the status's setting for one start.
type discordConfig struct {
	on    bool
	appID string
	pipes []string // the pipes to try (the test hook: one)
	why   string   // the console's line
	hook  string   // the test hook's pipe name ("": the app's pipes)
	// join is Join from Discord (front_join.go): friends join the player's party or lobby, and the player's Join
	// clicks arrive (discordJoinSetting)
	join bool
}

// discordSetting decides the status: --discord, then Start-Client.ini's discord, then on; never in a test run
// (--test-lane, --test-park, --non-interactive) unless the test hook names a fake app's pipe; off without an
// application id (discord_app_id, else defaultDiscordAppID).
func discordSetting(flagV string, ini Config, test bool, env func(string) string) (discordConfig, error) {
	v := firstNonEmpty(flagV, ini[DiscordKey])
	on, err := onOff(DiscordKey, v)
	if err != nil {
		return discordConfig{}, err
	}
	if !on {
		return discordConfig{why: "off (discord = off)"}, nil
	}
	hook := env(discord.TestPipeEnv)
	if hook != "" {
		if err := discord.CheckTestPipe(hook); err != nil {
			return discordConfig{}, err
		}
	}
	if test && hook == "" {
		return discordConfig{why: "off (a test run; " + discord.TestPipeEnv + " not set)"}, nil
	}
	id := firstNonEmpty(strings.TrimSpace(ini[DiscordAppIDKey]), defaultDiscordAppID)
	if id == "" && hook != "" {
		id = discordTestAppID
	}
	if id == "" {
		return discordConfig{why: "off (no Discord application id yet: " + DiscordAppIDKey + " in " + ClientConfigName + ")"}, nil
	}
	if strings.Trim(id, "0123456789") != "" {
		return discordConfig{}, fmt.Errorf("%s = %s: a Discord application id is a number", DiscordAppIDKey, id)
	}
	c := discordConfig{on: true, appID: id, pipes: discord.PipeNames(hook),
		why: "your Discord status shows what you do in the game (discord = off in " + ClientConfigName + " turns it off)"}
	if hook != "" {
		c.why = "test hook: the status goes to the fake app's pipe " + c.pipes[0]
		c.hook = hook
	}
	return c, nil
}

// discordJoinSetting decides Join from Discord: --discord-join, then Start-Client.ini's discord_join, then on; only
// with the status on (a test run only through the test hook, as the status).
func discordJoinSetting(flagV string, ini Config, dc discordConfig) (bool, string, error) {
	on, err := onOff(DiscordJoinKey, firstNonEmpty(flagV, ini[DiscordJoinKey]))
	switch {
	case err != nil:
		return false, "", err
	case !dc.on:
		return false, "", nil
	case !on:
		return false, "Join off (discord_join = off)", nil
	}
	return true, "friends on Discord can join your party or lobby (discord_join = off turns it off)", nil
}

// onOff reads an on/off key ("": on).
func onOff(key, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("%s = %s: use on or off", key, v)
}

// startPresence starts the status for the game's process: the tracker's activity (with the front), else "In the
// game" since the game's start. It returns the function that clears and ends it.
func startPresence(c discordConfig, pid int, t *presenceTracker, logf func(string, ...any), onJoin func(string)) func() {
	p := discord.New(discord.Options{AppID: c.appID, Pid: pid, Dial: discord.DialPipes(c.pipes), Logf: logf, OnJoin: onJoin})
	if t != nil {
		t.setOutput(p.Set)
	} else {
		p.Set(&discord.Activity{Details: "In the game", Start: time.Now(), LargeImage: "logo", LargeText: "Dawngate"})
	}
	return func() {
		if t != nil {
			t.setOutput(nil)
		}
		p.Close()
	}
}

// discordEnv is os.Getenv (the tests replace it).
var discordEnv = os.Getenv

// defaultDiscordAppID is the application id without discord_app_id: the release's built-in one (the tests set it
// empty to check a build without one).
var defaultDiscordAppID = discord.DefaultAppID
