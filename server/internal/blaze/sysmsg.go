package blaze

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"dawngate/server/internal/store"
	"dawngate/server/internal/tdf"
)

// System messages (B14; docs/BLAZE.md *System messages and other users' wins (B14)*, docs/UI_CONTRACT.md *Kill
// switches (R9)* and *The status marquee*). The server's own messages to the front end are Messaging NotifyMessages
// whose PYLD has TYPE 300: cLobbyMessaging registers a global callback at the platform's start (OnGlobalMessage
// 0x00ACC9F0, registered by 0x00ACD0D0 from the platform's init 0x00AD64E0, long before any login), and
// MessagingAPI_OnNotifyMessage (0x00CAA840) calls every global callback after the dispatcher of the payload's TTYP, so
// the message needs no target. The callback switches on TAG:
//   - 0: kill switches, ATTR {<switch id>: "<value>"}, each entry posted as message 0x0FFCEDD5 {id, strtoul(value)}
//     to the platform (BlazePlatform_HandleMessage 0x00AD7EF0), which keeps it in its map and posts 0x0FFCEDD6 when it
//     is new or changed; cGameEntryManager reads the map at its start (GameEntryManager_Initialize) and applies later
//     changes (GameEntryManager_HandleMessage 0x00690EE0): value 0 = allowed, anything else = switched off. The
//     platform's state change to "disconnected" (onBlazeStateChange 0x00AD4CB0, state 3) clears the map (0x00AD45A0),
//     so every login needs them again.
//   - 1: hide every broadcast message (message 0x450A5382 -> 0x004ACC00 clears each record's display flags).
//   - 2: a broadcast message, ATTR 0xB0000 id, 0xB0001 display flags, 0xB0002 seconds to show, 0xB0003 the text (all
//     four, or nothing happens; the numbers by strtoul base 10) -> message 0xE235C1EB -> GameUI_AddBroadcastMessage
//     (0x004B7310): GameUI +0x94B0 keeps up to 64 records {id, flags, expiry, text, shown flags}; a known id gets the
//     new flags, expiry and text (a changed text is shown again), an empty text, 0 seconds or 0 flags is ignored.
//   - 3-5: the client-update notice (scheduled, required, cancelled). Never sent: we never patch the client, and tag
//     4 pushes players out of their lobby.
//
// GameUI subscribes to these messages in its constructor (0x004AD1A0, table .data 0x013E00C8, from GameClient_Init),
// so they work at any time after the login. The display flags (GameUI update 0x004B5DE0, the main menu's
// 0x004E1000): 4 = the main menu's status marquee (SetStatusText, after message 0xCB796E00) and shaper select's
// OnGetBroadcastMsgs; 2 = a "System" line in the chat (0x0049B560 -> 0x0049B1E0) in the main menu, shaper select and
// the match (game states 2, 4 and 5); 1 = the chat's AddNotification panel (0x0049B580 -> 0x0049A680) in the main menu
// only (state 2). A record with flag 1 or 2 is shown there once (its shown flags), and again only when its text
// changes. Which TTYP and SRCE EA's server gave these messages isn't known; ours is a global message (BlazeSDK's
// Messaging has sendGlobalMessage, cmd 7): no target (TTYP (0, 0), TIDS empty, so no TTYP dispatcher runs), SRCE
// (0, 0, 0), NAME empty.
const (
	MessageTypeSystem       = 300
	SystemTagKillSwitches   = 0
	SystemTagHideBroadcasts = 1
	SystemTagBroadcast      = 2
)

// Broadcast message ATTR keys (TAG 2; LobbyMessaging_OnGlobalMessage 0x00ACC9F0 looks each up in the sorted map).
const (
	BroadcastAttrID      = 0xB0000
	BroadcastAttrFlags   = 0xB0001
	BroadcastAttrSeconds = 0xB0002
	BroadcastAttrText    = 0xB0003
)

// Broadcast display flags (see above) and our limits.
const (
	BroadcastNotification = 0x1 // the chat's notification panel, main menu only
	BroadcastChatLine     = 0x2 // a "System" chat line: main menu, shaper select, match
	BroadcastMarquee      = 0x4 // the main menu's status marquee and shaper select
	DefaultBroadcastFlags = BroadcastMarquee
	// BroadcastSeconds is how long a client shows a broadcast (the record's expiry is now + seconds x 1000 ms as a
	// uint32, so at most 49 days); 7 days [our choice]: every login and every change sends them again anyway.
	BroadcastSeconds  = 7 * 24 * 3600
	MaxBroadcasts     = 16   // [our choice]; the client keeps 64 records, the oldest goes
	MaxBroadcastBytes = 1024 // [our choice], as the relay's attribute limit
)

// KillSwitch is one of the client's kill switches: the enum's names (.rdata 0x012EEB98: kKillswitch_<Name>, ids 1-9)
// and where each lands (docs/UI_CONTRACT.md *Kill switches (R9)*).
type KillSwitch struct {
	ID   int64
	Name string
}

// KillSwitches lists every switch the client knows, by id.
var KillSwitches = []KillSwitch{
	{1, "Matchmaking"},           // the matchmaking block mask: the play screen's and the party lobby's queue
	{2, "WalletTopUp"},           // the main menu's add-funds button (IsWalletTopUpAllowed)
	{3, "EUGameServers"},         // the EU region in the region list
	{4, "PracticeSoloGames"},     // the play screen's solo practice
	{5, "FTUESoloGames"},         // the first-time-user practice path of the PLAY button
	{6, "PurchaseWithWaypoints"}, // Waypoints (PC) purchases in the collection and the perks
	{7, "PracticeGroupGames"},    // group practice
	{8, "CustomGames"},           // custom games [its reader not followed, R9]
	{9, "ForwardRenderer"},       // [no reader found, R9]
}

// DefaultKillSwitches is what dgserver switches off unless told otherwise (--kill-switches): the add-funds button,
// which opens a top-up page nothing serves.
const DefaultKillSwitches = "WalletTopUp"

// KillSwitchName names switch id ("switch <id>" for an unknown one).
func KillSwitchName(id int64) string {
	for _, k := range KillSwitches {
		if k.ID == id {
			return k.Name
		}
	}
	return fmt.Sprintf("switch %d", id)
}

// ParseKillSwitches reads dgserver --kill-switches: comma-separated switch names (case-insensitive) or ids 1-9; ""
// or "none" = none. The result is sorted and without duplicates.
func ParseKillSwitches(s string) ([]int64, error) {
	var ids []int64
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" || strings.EqualFold(f, "none") {
			continue
		}
		id := int64(0)
		for _, k := range KillSwitches {
			if strings.EqualFold(k.Name, f) || strconv.FormatInt(k.ID, 10) == f {
				id = k.ID
			}
		}
		if id == 0 {
			var names []string
			for _, k := range KillSwitches {
				names = append(names, k.Name)
			}
			return nil, fmt.Errorf("unknown kill switch %q (known: %s)", f, strings.Join(names, ", "))
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// DescribeKillSwitches is ids for the log: "2 WalletTopUp, 8 CustomGames" or "none".
func DescribeKillSwitches(ids []int64) string {
	if len(ids) == 0 {
		return "none"
	}
	var out []string
	for _, id := range ids {
		out = append(out, fmt.Sprintf("%d %s", id, KillSwitchName(id)))
	}
	return strings.Join(out, ", ")
}

// Broadcast is one operator message (TAG 2): its id (1, 2, ... in the list's order), display flags and text.
type Broadcast struct {
	ID    int64
	Flags int64
	Text  string
}

// SystemMessage is a TYPE 300 payload with tag and attributes (sorted by key, as the client's map holds them), no
// target.
func SystemMessage(tag int64, attrs []MsgAttr) ClientMessage {
	attrs = slices.Clone(attrs)
	slices.SortStableFunc(attrs, func(a, b MsgAttr) int { return cmp.Compare(a.Key, b.Key) })
	return ClientMessage{Attrs: attrs, Tag: tag, Type: MessageTypeSystem}
}

// KillSwitchMessage switches the given switches off (value "1"; TAG 0).
func KillSwitchMessage(ids []int64) ClientMessage {
	var attrs []MsgAttr
	for _, id := range ids {
		attrs = append(attrs, MsgAttr{id, "1"})
	}
	return SystemMessage(SystemTagKillSwitches, attrs)
}

// HideBroadcastsMessage hides every broadcast message the client holds (TAG 1).
func HideBroadcastsMessage() ClientMessage { return SystemMessage(SystemTagHideBroadcasts, nil) }

// BroadcastMessage shows b for BroadcastSeconds (TAG 2).
func BroadcastMessage(b Broadcast) ClientMessage {
	return SystemMessage(SystemTagBroadcast, []MsgAttr{
		{BroadcastAttrID, strconv.FormatInt(b.ID, 10)},
		{BroadcastAttrFlags, strconv.FormatInt(b.Flags, 10)},
		{BroadcastAttrSeconds, strconv.FormatInt(BroadcastSeconds, 10)},
		{BroadcastAttrText, b.Text},
	})
}

// SystemMessageNotification is Messaging::NotifyMessage (Blaze::Messaging::ServerMessage 0x012EE2D0) carrying a
// system message: FLAG 0, MGID id, NAME empty, PYLD m, SRCE the zero object id (no sender), TIME.
func SystemMessageNotification(id int64, m ClientMessage, now int64) []byte {
	w := tdf.NewWriter()
	w.Int("FLAG", 0)
	w.Int("MGID", id)
	w.String("NAME", "")
	w.Struct("PYLD", func(w *tdf.Writer) { writeClientMessage(w, m) })
	w.Value("SRCE", tdf.TypeObjID, tdf.ObjID{})
	w.Int("TIME", now)
	return w.Bytes()
}

// ParseBroadcasts reads broadcast messages from text (an operator's file or dgserver --broadcast): one message per
// line; empty lines and lines starting with '#' are skipped; a line may start with "@<flags> " (1-7, the display
// flags above) instead of defaultFlags. A leading UTF-8 byte order mark is dropped, a line over MaxBroadcastBytes is
// cut, lines beyond MaxBroadcasts are dropped; notes says what was changed. The ids are assigned by SetBroadcasts.
func ParseBroadcasts(text string, defaultFlags int64) (out []Broadcast, notes []string) {
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		flags := defaultFlags
		if rest, ok := strings.CutPrefix(line, "@"); ok {
			num, msg, _ := strings.Cut(rest, " ")
			f, err := strconv.ParseInt(num, 10, 64)
			if err != nil || f < 1 || f > 7 {
				notes = append(notes, fmt.Sprintf("line %d: flags %q not 1-7, using %d", n+1, num, defaultFlags))
			} else {
				flags = f
			}
			line = strings.TrimSpace(msg)
			if line == "" {
				continue
			}
		}
		if len(line) > MaxBroadcastBytes {
			cut := MaxBroadcastBytes
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut]
			notes = append(notes, fmt.Sprintf("line %d: cut to %d bytes", n+1, cut))
		}
		if len(out) == MaxBroadcasts {
			notes = append(notes, fmt.Sprintf("line %d and later: more than %d messages, dropped", n+1, MaxBroadcasts))
			break
		}
		out = append(out, Broadcast{Flags: flags, Text: line})
	}
	return out, notes
}

// systemMessages is the broadcast state (B14): whether broadcasts are on (SetBroadcasts was called) and the current
// list. mu also orders a login's system messages against a change pushed to everyone (sendSystemMessages,
// SetBroadcasts), so the last list a client gets is the current one.
type systemMessages struct {
	mu    sync.Mutex
	on    bool
	list  []Broadcast
	count onlineCount // ONLINECOUNT: the players-online count (onlinecount.go)
	// mapNotes counts the map check's messages per connection (MAPGUARD, mapguard.go tellMap)
	mapNotes map[*Conn]int
}

// SetBroadcasts makes list the current broadcast messages (ids 1, 2, ... in order) and turns broadcasts on: from now
// on every login gets TAG 1 (hide what the client still shows from an earlier login) and a TAG 2 per message. When
// the list changed, every logged-in client gets the same at once. It reports whether the list changed.
func (s *Server) SetBroadcasts(list []Broadcast) bool {
	list = slices.Clone(list)
	for i := range list {
		list[i].ID = int64(i + 1)
	}
	sm := &s.sys
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.on && slices.Equal(sm.list, list) {
		return false
	}
	sm.on, sm.list = true, list
	conns := s.onlineConns()
	s.Log.Logf("blaze", "broadcasts: %s; sent to %d logged-in connections", describeBroadcasts(list), len(conns))
	for _, c := range conns {
		msgs := broadcastMessages(list)
		if _, got := sm.count.sent[c]; got { // TAG 1 hid the count too (a connection still before its login's gets it then)
			msgs = append(msgs, s.countMessagesLocked(c)...)
		}
		s.pushSystemLocked(c, msgs)
	}
	return true
}

// Broadcasts returns the current broadcast messages.
func (s *Server) Broadcasts() []Broadcast {
	s.sys.mu.Lock()
	defer s.sys.mu.Unlock()
	return slices.Clone(s.sys.list)
}

func describeBroadcasts(list []Broadcast) string {
	if len(list) == 0 {
		return "none"
	}
	var out []string
	for _, b := range list {
		out = append(out, fmt.Sprintf("%d %q (flags %d)", b.ID, b.Text, b.Flags))
	}
	return strings.Join(out, ", ")
}

// broadcastMessages is what puts list on a client: hide all, then each message.
func broadcastMessages(list []Broadcast) []ClientMessage {
	out := []ClientMessage{HideBroadcastsMessage()}
	for _, b := range list {
		out = append(out, BroadcastMessage(b))
	}
	return out
}

// sendSystemMessages sends a client its system messages right after its login (loginPersona; on the connection's
// goroutine after the reply and its notifications): the kill switches (Server.KillSwitches) and, when broadcasts are
// on, the broadcast messages, then with the players-online count on (StartOnlineCount) its own broadcast. Nothing when
// none is configured (the server before B14).
func (s *Server) sendSystemMessages(c *Conn, p store.Persona) {
	sm := &s.sys
	sm.mu.Lock()
	var msgs []ClientMessage
	if len(s.KillSwitches) > 0 {
		msgs = append(msgs, KillSwitchMessage(s.KillSwitches))
	}
	if sm.on || sm.count.on {
		msgs = append(msgs, broadcastMessages(sm.list)...)
	}
	count := s.countMessagesLocked(c) // ONLINECOUNT: after the operator's lines, so the marquee shows it last
	msgs = append(msgs, count...)
	if len(msgs) == 0 {
		sm.mu.Unlock()
		return
	}
	bc := "off"
	if sm.on || sm.count.on {
		bc = strconv.Itoa(len(sm.list))
	}
	if len(count) > 0 {
		bc += fmt.Sprintf("; online count %q", sm.count.sent[c])
	}
	s.Log.Logf("blaze", "   system messages for %d %q: kill switches %s; broadcasts %s", p.ID, p.Name,
		DescribeKillSwitches(s.KillSwitches), bc)
	s.pushSystemLocked(c, msgs)
	sm.mu.Unlock()
	if len(count) > 0 {
		s.forgetCount(c)
	}
}

// pushSystemLocked sends msgs to c as NotifyMessages (s.sys.mu held).
func (s *Server) pushSystemLocked(c *Conn, msgs []ClientMessage) {
	now := s.Now().Unix()
	for _, m := range msgs {
		if !c.Push(s, ComponentMessaging, NotifyMessageID, "NotifyMessage", SystemMessageNotification(s.msgs.newID(), m, now)) {
			return
		}
	}
}

// newID returns the next message id (MGID), shared with the relay's messages.
func (m *messaging) newID() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return m.nextID
}

// OnlineUsers is how many users are logged in now (the messaging relay's online list: one per persona with a
// connection), for the website's status page.
func (s *Server) OnlineUsers() int {
	m := &s.msgs
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.online)
}

// onlineConns returns every logged-in connection once (the messaging relay's, B10c).
func (s *Server) onlineConns() []*Conn {
	m := &s.msgs
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Conn
	for _, cs := range m.online {
		for _, c := range cs {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// WatchBroadcasts keeps the broadcasts at static plus the messages of the file at path (ParseBroadcasts with flags):
// it reads the file now (so the first login already finds them), then every interval in its own goroutine until the
// returned stop function is called (which waits for it), and SetBroadcasts sends a change to everyone logged in. A
// missing file means no messages from it; another read error keeps the last list (logged once). This is the
// operator's way to announce something while the server runs (e.g. "Server restart at 20:00 UTC"): edit the file, or
// delete it to take the messages down.
func (s *Server) WatchBroadcasts(path string, static []Broadcast, flags int64, every time.Duration) (stop func()) {
	lastErr, lastNotes := "", ""
	check := func() {
		data, err := os.ReadFile(path)
		switch {
		case err != nil && os.IsNotExist(err):
			data, err = nil, nil
		case err != nil:
			if msg := err.Error(); msg != lastErr {
				s.Log.Logf("blaze", "broadcast file %s: %v (keeping the last messages)", path, err)
				lastErr = msg
			}
			return
		}
		lastErr = ""
		list, notes := ParseBroadcasts(string(data), flags)
		if n := strings.Join(notes, "; "); n != lastNotes {
			if n != "" {
				s.Log.Logf("blaze", "broadcast file %s: %s", path, n)
			}
			lastNotes = n
		}
		s.SetBroadcasts(append(slices.Clone(static), list...))
	}
	check()
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				check()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}
