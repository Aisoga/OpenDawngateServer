// Package discord sets the player's Discord activity (Rich Presence) through the Discord desktop app's local IPC
// pipe (\\.\pipe\discord-ipc-0 to -9 on Windows). Nothing goes over the network: the app on the same PC shows the
// activity to the player's friends.
//
// The protocol: each frame is a uint32 opcode and a uint32 length (both little-endian), then that many bytes of JSON.
// Opcodes: 0 HANDSHAKE ({"v":1,"client_id":<application id>}, answered by a FRAME with evt READY), 1 FRAME (commands
// and their answers: {"cmd":"SET_ACTIVITY","args":{"pid":<game pid>,"activity":{...}},"nonce":<n>}; activity null
// clears it), 2 CLOSE (the app refuses, e.g. an unknown application id), 3 PING (answered by 4 PONG with the same
// payload).
//
// Presence keeps the latest wanted activity and sends it when the app answers: it tries to connect every Retry
// (15 s) while the app isn't running and after it quits (sending the activity again), sends at most one update per
// MinGap (4 s; the app takes 5 per 20 s, so quick changes are coalesced and the last one wins) and clears the
// activity on Close. Only the standard library is used.
//
// Join (Options.OnJoin): the activity may carry a party id and a join secret (Activity.PartyID, JoinSecret; party
// privacy 1, public: Discord friends get a direct Join button). Presence then subscribes to ACTIVITY_JOIN after each
// READY ({"cmd":"SUBSCRIBE","evt":"ACTIVITY_JOIN"}), and the app's DISPATCH ACTIVITY_JOIN {"secret"} (a friend clicked
// Join on this player's profile, or on a Discord chat invite) goes to OnJoin.
package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultAppID is the Discord application id used when none is configured (Start-Client.ini discord_app_id): the
// project's application "OpenDawngate" (friends see "Playing OpenDawngate"), with the art asset "logo".
const DefaultAppID = "1561733580789514310"

// Opcodes of the IPC frames.
const (
	OpHandshake = 0
	OpFrame     = 1
	OpClose     = 2
	OpPing      = 3
	OpPong      = 4
)

// maxFrame bounds a frame's JSON from the app (its READY carries the user and the config: a few KB).
const maxFrame = 1 << 20

// Activity is what the player's friends see.
type Activity struct {
	Details string    // the first line ("In match · PvP")
	State   string    // the second line ("In a party"; empty: none)
	Start   time.Time // the elapsed timer's start (zero: no timer)
	// PartySize and PartyMax show "(n of m)" beside State (0: none).
	PartySize, PartyMax int
	LargeImage          string // the application's art asset key ("logo")
	LargeText           string // its hover text ("Dawngate")
	// PartyID groups the players of one party (Discord's party.id; not the game's own id). JoinSecret (Discord's
	// secrets.join, 2 to 128 characters, not the party id) gives friends a Join button: it is sent only with a party
	// id and a size whose maximum is above the count, and makes the party public (party.privacy 1).
	PartyID, JoinSecret string
}

// String is the activity for a log line.
func (a *Activity) String() string {
	if a == nil {
		return "(none)"
	}
	s := a.Details
	if a.State != "" {
		s += " / " + a.State
	}
	if a.PartySize > 0 {
		s += fmt.Sprintf(" (%d of %d)", a.PartySize, a.PartyMax)
	}
	if !a.Start.IsZero() {
		s += ", since " + a.Start.Format("15:04:05")
	}
	if a.joinable() {
		s += ", joinable"
	}
	return s
}

type activityJSON struct {
	Details    string          `json:"details,omitempty"`
	State      string          `json:"state,omitempty"`
	Timestamps *timestampsJSON `json:"timestamps,omitempty"`
	Assets     *assetsJSON     `json:"assets,omitempty"`
	Party      *partyJSON      `json:"party,omitempty"`
	Secrets    *secretsJSON    `json:"secrets,omitempty"`
}

type timestampsJSON struct {
	Start int64 `json:"start"` // Unix seconds, as Discord's own RPC library sends it [assumed: the app takes seconds]
}

type assetsJSON struct {
	LargeImage string `json:"large_image,omitempty"`
	LargeText  string `json:"large_text,omitempty"`
}

type partyJSON struct {
	ID      string  `json:"id,omitempty"`
	Size    *[2]int `json:"size,omitempty"`
	Privacy int     `json:"privacy,omitempty"` // 1 public: friends join directly
}

type secretsJSON struct {
	Join string `json:"join"`
}

// MaxSecret is the longest join secret Discord takes.
const MaxSecret = 128

// joinable says whether the join secret is sent: a party id, a size with a free seat, a secret Discord takes.
func (a *Activity) joinable() bool {
	return a.JoinSecret != "" && a.PartyID != "" && a.JoinSecret != a.PartyID && len(a.JoinSecret) >= 2 &&
		len(a.JoinSecret) <= MaxSecret && a.PartySize > 0 && a.PartyMax > a.PartySize
}

// JSON is the activity as SET_ACTIVITY's args.activity (nil: null, which clears it).
func (a *Activity) JSON() any {
	if a == nil {
		return nil
	}
	j := &activityJSON{Details: a.Details, State: a.State}
	if !a.Start.IsZero() {
		j.Timestamps = &timestampsJSON{Start: a.Start.Unix()}
	}
	if a.LargeImage != "" || a.LargeText != "" {
		j.Assets = &assetsJSON{LargeImage: a.LargeImage, LargeText: a.LargeText}
	}
	if a.PartySize > 0 && a.PartyMax >= a.PartySize {
		j.Party = &partyJSON{Size: &[2]int{a.PartySize, a.PartyMax}}
	}
	if a.PartyID != "" {
		if j.Party == nil {
			j.Party = &partyJSON{}
		}
		j.Party.ID = a.PartyID
	}
	if a.joinable() {
		j.Party.Privacy = 1
		j.Secrets = &secretsJSON{Join: a.JoinSecret}
	}
	return j
}

func (a *Activity) equal(b *Activity) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Details == b.Details && a.State == b.State && a.Start.Equal(b.Start) && a.PartySize == b.PartySize &&
		a.PartyMax == b.PartyMax && a.LargeImage == b.LargeImage && a.LargeText == b.LargeText && a.PartyID == b.PartyID &&
		a.JoinSecret == b.JoinSecret
}

// Conn is a connection to the app: the pipe, or one end of net.Pipe in the tests. A read past the deadline fails
// with os.ErrDeadlineExceeded.
type Conn interface {
	io.ReadWriteCloser
	SetReadDeadline(t time.Time) error
}

// Dialer connects to the app; name is the pipe's, for the log.
type Dialer func() (c Conn, name string, err error)

// Frame is one IPC frame.
type Frame struct {
	Op   uint32
	JSON []byte
}

// WriteFrame writes one frame in a single write.
func WriteFrame(w io.Writer, op uint32, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b := make([]byte, 8, 8+len(body))
	binary.LittleEndian.PutUint32(b[0:], op)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	_, err = w.Write(append(b, body...))
	return err
}

// errNoFrame: no frame came within the wait (nothing was read).
var errNoFrame = errors.New("no frame")

// readFrame reads one frame, waiting at most wait for its first byte and replyWait for the rest.
func readFrame(c Conn, wait, replyWait time.Duration) (Frame, error) {
	var hdr [8]byte
	c.SetReadDeadline(time.Now().Add(wait))
	n, err := io.ReadFull(c, hdr[:])
	if err != nil {
		if n == 0 && errors.Is(err, os.ErrDeadlineExceeded) {
			return Frame{}, errNoFrame
		}
		return Frame{}, err
	}
	size := binary.LittleEndian.Uint32(hdr[4:])
	if size > maxFrame {
		return Frame{}, fmt.Errorf("a frame of %d bytes", size)
	}
	body := make([]byte, size)
	c.SetReadDeadline(time.Now().Add(replyWait))
	if _, err := io.ReadFull(c, body); err != nil {
		return Frame{}, err
	}
	c.SetReadDeadline(time.Time{})
	return Frame{Op: binary.LittleEndian.Uint32(hdr[0:]), JSON: body}, nil
}

// message is the part of a FRAME's JSON Presence reads.
type message struct {
	Cmd   string          `json:"cmd"`
	Evt   *string         `json:"evt"`
	Nonce *string         `json:"nonce"`
	Data  json.RawMessage `json:"data"`
	// a CLOSE's
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// errorText is an ERROR answer's or a CLOSE's text.
func (m message) errorText() string {
	var d struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(m.Data, &d) == nil && (d.Code != 0 || d.Message != "") {
		return fmt.Sprintf("%d %s", d.Code, d.Message)
	}
	return fmt.Sprintf("%d %s", m.Code, m.Message)
}

// Options are a Presence's.
type Options struct {
	AppID string // the Discord application id (client_id)
	Pid   int    // the game's process id: the app shows the activity while it runs
	Dial  Dialer // nil: DialPipes(PipeNames(""))
	// Logf gets one line per connect, failure (the first of a row), send and clear; nil: none.
	Logf func(format string, a ...any)
	// OnJoin gets the secret of each ACTIVITY_JOIN the app dispatches (a friend's Join); nil: no subscription. It
	// runs in a goroutine of its own.
	OnJoin func(secret string)
	// Retry is the time between connect attempts (0: 15 s), MinGap the least time between two updates (0: 4 s),
	// ReplyWait how long an answer may take (0: 5 s), Poll how often a connection is checked for frames from the
	// app and for its end (0: 1 s).
	Retry, MinGap, ReplyWait, Poll time.Duration
}

// Presence keeps the player's Discord activity (Set, Close).
type Presence struct {
	o Options

	mu      sync.Mutex
	want    *Activity
	ver     uint64 // bumped by each Set that changes want
	conn    Conn   // the live connection (for Close)
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopped sync.Once
	nonce   int
}

// New starts a Presence: it connects in the background and sends the activity of each Set.
func New(o Options) *Presence {
	if o.Dial == nil {
		o.Dial = DialPipes(PipeNames(""))
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Retry <= 0 {
		o.Retry = 15 * time.Second
	}
	if o.MinGap <= 0 {
		o.MinGap = 4 * time.Second
	}
	if o.ReplyWait <= 0 {
		o.ReplyWait = 5 * time.Second
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	p := &Presence{o: o, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

// Set makes a the wanted activity (nil: none). It never blocks; an equal activity changes nothing.
func (p *Presence) Set(a *Activity) {
	if a != nil {
		c := *a
		a = &c
	}
	p.mu.Lock()
	if a.equal(p.want) {
		p.mu.Unlock()
		return
	}
	p.want = a
	p.ver++
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Close clears the activity (when connected) and ends the Presence; it waits at most a few seconds.
func (p *Presence) Close() {
	p.stopped.Do(func() { close(p.stop) })
	select {
	case <-p.done:
	case <-time.After(p.o.ReplyWait + time.Second):
		p.mu.Lock()
		if p.conn != nil {
			p.conn.Close()
		}
		p.mu.Unlock()
		<-p.done
	}
}

func (p *Presence) current() (uint64, *Activity) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ver, p.want
}

func (p *Presence) setConn(c Conn) {
	p.mu.Lock()
	p.conn = c
	p.mu.Unlock()
}

func (p *Presence) run() {
	defer close(p.done)
	var c Conn
	var sentVer uint64     // the version the app has (valid while c != nil and !dirty)
	dirty := false         // the app's activity differs from the wanted one
	shown := false         // the app shows an activity of ours
	var lastSend time.Time // the last update's time
	var nextDial time.Time // the next connect attempt (zero: now)
	failing := false       // a connect failed (logged once until one succeeds)
	drop := func(why string) {
		p.o.Logf("discord: the connection to the Discord app ended (%s); trying again every %s", why, p.o.Retry)
		c.Close()
		p.setConn(nil)
		c, shown, failing = nil, false, true
		nextDial = time.Now().Add(p.o.Retry)
	}
	for {
		ver, want := p.current()
		if ver != sentVer {
			dirty = true
		}
		now := time.Now()
		if c == nil && !now.Before(nextDial) {
			nc, name, err := p.connect()
			if err != nil {
				if !failing {
					p.o.Logf("discord: no Discord app to show the status in (%v); trying again every %s", err, p.o.Retry)
				}
				failing = true
				nextDial = now.Add(p.o.Retry)
			} else {
				c, failing = nc, false
				p.setConn(c)
				p.o.Logf("discord: connected to the Discord app (%s)", name)
				if p.o.OnJoin != nil {
					if err := p.subscribe(c); err != nil {
						drop(err.Error())
						continue
					}
				}
				dirty = want != nil // the app shows nothing of a new connection
				sentVer = ver
			}
		}
		if c != nil && dirty {
			if wait := lastSend.Add(p.o.MinGap).Sub(now); wait <= 0 {
				if err := p.send(c, want); err != nil {
					drop(err.Error())
				} else {
					sentVer, dirty, shown, lastSend = ver, false, want != nil, time.Now()
					if want == nil {
						p.o.Logf("discord: activity cleared")
					} else {
						p.o.Logf("discord: activity %s", want)
					}
				}
				continue
			}
		}
		// sleep until the next thing to do
		d := p.o.Poll
		if c == nil {
			d = time.Until(nextDial)
		} else if dirty {
			d = min(d, time.Until(lastSend.Add(p.o.MinGap)))
		}
		t := time.NewTimer(max(d, time.Millisecond))
		select {
		case <-p.stop:
			t.Stop()
			if c != nil {
				if shown {
					if err := p.send(c, nil); err == nil {
						p.o.Logf("discord: activity cleared (the game ended)")
					} else {
						p.o.Logf("discord: the activity wasn't cleared: %v", err)
					}
				}
				c.Close()
				p.setConn(nil)
			}
			return
		case <-p.wake:
			t.Stop()
		case <-t.C:
			if c != nil && (!dirty || time.Until(lastSend.Add(p.o.MinGap)) > 0) {
				if err := p.poll(c); err != nil {
					drop(err.Error())
				}
			}
		}
	}
}

// connect dials the app and makes the handshake.
func (p *Presence) connect() (Conn, string, error) {
	c, name, err := p.o.Dial()
	if err != nil {
		return nil, "", err
	}
	if err := WriteFrame(c, OpHandshake, map[string]any{"v": 1, "client_id": p.o.AppID}); err != nil {
		c.Close()
		return nil, "", fmt.Errorf("%s: the handshake: %v", name, err)
	}
	deadline := time.Now().Add(p.o.ReplyWait)
	for {
		f, err := readFrame(c, time.Until(deadline), p.o.ReplyWait)
		if err != nil {
			c.Close()
			if errors.Is(err, errNoFrame) {
				err = errors.New("no answer to the handshake")
			}
			return nil, "", fmt.Errorf("%s: %v", name, err)
		}
		var m message
		json.Unmarshal(f.JSON, &m)
		switch f.Op {
		case OpClose:
			c.Close()
			return nil, "", fmt.Errorf("%s: the app refused the handshake (%s)", name, m.errorText())
		case OpPing:
			if err := writeRaw(c, OpPong, f.JSON); err != nil {
				c.Close()
				return nil, "", err
			}
		case OpFrame:
			if m.Evt != nil && *m.Evt == "READY" {
				return c, name, nil
			}
			if m.Evt != nil && *m.Evt == "ERROR" {
				c.Close()
				return nil, "", fmt.Errorf("%s: the app refused the handshake (%s)", name, m.errorText())
			}
		}
	}
}

// send sends SET_ACTIVITY with a (nil: clear) and waits for its answer.
func (p *Presence) send(c Conn, a *Activity) error {
	_, err := p.command(c, map[string]any{"cmd": "SET_ACTIVITY", "args": map[string]any{"pid": p.o.Pid, "activity": a.JSON()}})
	return err
}

// subscribe asks the app for ACTIVITY_JOIN (a friend's Join); a refusal is logged and the connection kept.
func (p *Presence) subscribe(c Conn) error {
	m, err := p.command(c, map[string]any{"cmd": "SUBSCRIBE", "evt": "ACTIVITY_JOIN", "args": map[string]any{}})
	if err != nil {
		return err
	}
	if m.Evt != nil && *m.Evt == "ERROR" {
		p.o.Logf("discord: the app refused the Join subscription (%s)", strings.TrimSpace(m.errorText()))
	} else {
		p.o.Logf("discord: subscribed to ACTIVITY_JOIN (a friend's Join from Discord)")
	}
	return nil
}

// command sends one command frame with a new nonce and waits for its answer.
func (p *Presence) command(c Conn, cmd map[string]any) (message, error) {
	p.nonce++
	nonce := strconv.Itoa(p.nonce)
	cmd["nonce"] = nonce
	if err := WriteFrame(c, OpFrame, cmd); err != nil {
		return message{}, err
	}
	deadline := time.Now().Add(p.o.ReplyWait)
	for {
		f, err := readFrame(c, time.Until(deadline), p.o.ReplyWait)
		if errors.Is(err, errNoFrame) {
			return message{}, fmt.Errorf("no answer to %s", cmd["cmd"])
		}
		if err != nil {
			return message{}, err
		}
		m, done, err := p.handle(c, f, nonce)
		if done || err != nil {
			return m, err
		}
	}
}

// poll reads what the app sent meanwhile (a PING is answered); an error ends the connection.
func (p *Presence) poll(c Conn) error {
	for {
		f, err := readFrame(c, time.Millisecond, p.o.ReplyWait)
		if errors.Is(err, errNoFrame) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, _, err := p.handle(c, f, ""); err != nil {
			return err
		}
	}
}

// handle takes one frame from the app: done when it is the answer to nonce (m that answer); an error when the app
// closed the connection. A friend's Join (DISPATCH ACTIVITY_JOIN) goes to OnJoin whenever it comes.
func (p *Presence) handle(c Conn, f Frame, nonce string) (m message, done bool, err error) {
	json.Unmarshal(f.JSON, &m)
	switch f.Op {
	case OpPing:
		return m, false, writeRaw(c, OpPong, f.JSON)
	case OpClose:
		return m, false, fmt.Errorf("the app closed it: %s", m.errorText())
	case OpFrame:
		if m.Cmd == "DISPATCH" && m.Evt != nil && *m.Evt == "ACTIVITY_JOIN" {
			p.join(m.Data)
			return m, false, nil
		}
		if nonce == "" || m.Nonce == nil || *m.Nonce != nonce {
			return m, false, nil
		}
		if m.Evt != nil && *m.Evt == "ERROR" && m.Cmd == "SET_ACTIVITY" {
			p.o.Logf("discord: the app refused the activity (%s)", strings.TrimSpace(m.errorText()))
		}
		return m, true, nil
	}
	return m, false, nil
}

// join passes an ACTIVITY_JOIN's secret to OnJoin.
func (p *Presence) join(data json.RawMessage) {
	var d struct {
		Secret string `json:"secret"`
	}
	if json.Unmarshal(data, &d) != nil || d.Secret == "" || p.o.OnJoin == nil {
		p.o.Logf("discord: an ACTIVITY_JOIN without a secret, or without a Join handler (ignored)")
		return
	}
	p.o.Logf("discord: Join from Discord (a secret of %d characters)", len(d.Secret))
	go p.o.OnJoin(d.Secret)
}

func writeRaw(w io.Writer, op uint32, body []byte) error {
	b := make([]byte, 8, 8+len(body))
	binary.LittleEndian.PutUint32(b[0:], op)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	_, err := w.Write(append(b, body...))
	return err
}

// TestPipeEnv names the test hook: a pipe name (without \\.\pipe\) that replaces the app's pipes, served by a fake
// app; it also turns the status on in test runs. Names that start with discord-ipc are refused.
const TestPipeEnv = "DAWNGATE_TEST_DISCORD_PIPE"

// PipeNames are the pipes to try: the app's ten, or the test hook's one.
func PipeNames(test string) []string {
	if test != "" {
		return []string{pipePath(strings.TrimPrefix(test, `\\.\pipe\`))}
	}
	names := make([]string, 10)
	for i := range names {
		names[i] = pipePath(fmt.Sprintf("discord-ipc-%d", i))
	}
	return names
}

// CheckTestPipe refuses a test hook's pipe name that is the app's own (tests never set the player's status).
func CheckTestPipe(name string) error {
	n := strings.ToLower(strings.TrimPrefix(name, `\\.\pipe\`))
	if strings.HasPrefix(n, "discord-ipc") || n == "" || strings.ContainsAny(n, `\/`) {
		return fmt.Errorf("%s=%s: a test pipe needs a name of its own (not discord-ipc-*, no path)", TestPipeEnv, name)
	}
	return nil
}

// DialPipes is the Dialer that tries the pipes in order.
func DialPipes(names []string) Dialer {
	return func() (Conn, string, error) {
		var first error
		for _, n := range names {
			c, err := dialPipe(n)
			if err == nil {
				return c, n, nil
			}
			if first == nil {
				first = err
			}
		}
		if first == nil {
			first = errors.New("no pipe to try")
		}
		return nil, "", first
	}
}
