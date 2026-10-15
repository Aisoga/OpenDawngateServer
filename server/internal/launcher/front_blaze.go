package launcher

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/logging"
	"dawngate/server/internal/redirector"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
)

// The game's Blaze connection through the front: answered by the front until a login, then passed to the picked
// server (the server switch).

// Blaze requests the front looks at.
const (
	cmdUserSessionsResume = 0x23 // UserSessions::resumeSession: a lost connection's first request
)

// blazeConn is one of the game's Blaze connections.
type blazeConn struct {
	f    *Front
	gc   net.Conn
	src  *net.TCPAddr
	peer string

	wmu sync.Mutex // whole frames to the game

	// preAuth and ping are the client's own first requests on this connection, as sent: a login replays them to
	// the server (msgIds 0 and 1), so the server sees the connection the client opened and the ids stay aligned
	preAuth, ping []byte

	umu    sync.Mutex
	up     net.Conn      // the server's Blaze (nil: the front answers)
	upSess *frontSession // the session up belongs to
	upDone chan struct{} // closed when up's reader has stopped
	// loginID is the msgId of a login passed to the server whose reply hasn't come yet (loginWait): only a reply
	// leaves the login screen; after the server's refusal (a wrong password) the login screen still shows
	loginID   uint32
	loginWait bool
}

// readFrame reads one Fire frame: its bytes as sent and the frame.
func readFrame(r io.Reader) ([]byte, fire.Frame, error) {
	var hdr [fire.HeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fire.Frame{}, err
	}
	raw := make([]byte, fire.HeaderLen+int(binary.BigEndian.Uint16(hdr[0:])))
	copy(raw, hdr[:])
	if _, err := io.ReadFull(r, raw[fire.HeaderLen:]); err != nil {
		return nil, fire.Frame{}, err
	}
	f, err := fire.NewReader(bytes.NewReader(raw)).Next()
	return raw, f, err
}

func (f *Front) serveBlaze(gc net.Conn) {
	bc := &blazeConn{f: f, gc: gc, src: tcpAddr(gc.RemoteAddr()), peer: logging.Peer(gc.RemoteAddr())}
	f.logf("Blaze connection from %s", bc.peer)
	defer bc.closeUp()
	r := bufio.NewReader(gc)
	for {
		raw, fr, err := readFrame(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				f.logf("Blaze %s: %s: %v", bc.peer, logging.ErrName(err), err)
			}
			f.logf("Blaze %s closed", bc.peer)
			return
		}
		if fr.Type == fire.Request && fr.Component == blaze.ComponentAuthentication && fr.Command == blaze.CmdAuthLogin {
			bc.login(fr, raw)
			continue
		}
		if up := bc.upstream(); up != nil {
			if _, err := up.Write(raw); err != nil {
				f.logf("Blaze %s: the server's connection failed: %v", bc.peer, err)
				return
			}
			continue
		}
		bc.answer(fr, raw)
	}
}

// write sends one frame to the game.
func (bc *blazeConn) write(raw []byte) error {
	bc.wmu.Lock()
	defer bc.wmu.Unlock()
	_, err := bc.gc.Write(raw)
	return err
}

func (bc *blazeConn) reply(req fire.Frame, payload []byte, what string) {
	out, err := fire.Encode(fire.Frame{Component: req.Component, Command: req.Command, Type: fire.Reply, ID: req.ID, Payload: payload})
	if err == nil {
		err = bc.write(out)
	}
	if err != nil {
		bc.f.logf("Blaze %s: %s not sent: %v", bc.peer, what, err)
		return
	}
	bc.f.logf("Blaze %s: -> %s (id %d, the front's)", bc.peer, what, req.ID)
}

// errorReply refuses a login with the front's error; a server connection the game had is ended first, so no frame
// of it can come after the refusal.
func (bc *blazeConn) errorReply(req fire.Frame, e *frontError) {
	bc.closeUp()
	out, err := fire.Encode(fire.Frame{Component: req.Component, Command: req.Command, Error: e.code, Type: fire.ErrorReply, ID: req.ID})
	if err == nil {
		err = bc.write(out)
	}
	bc.f.logf("Blaze %s: login refused (error %#04x): %s", bc.peer, e.code, e.msg)
	if err != nil {
		bc.f.logf("Blaze %s: the error reply wasn't sent: %v", bc.peer, err)
	}
}

// answer is the front's answer to a request before a login: preAuth, ping, a lost connection's resumeSession (to
// the current session's server); anything else is logged and left unanswered.
func (bc *blazeConn) answer(fr fire.Frame, raw []byte) {
	f := bc.f
	fire.Describe(f.Log, frontWho, fr)
	switch {
	case fr.Type != fire.Request:
	case fr.Component == blaze.ComponentUtil && fr.Command == blaze.CmdUtilPreAuth:
		bc.preAuth = raw
		bc.reply(fr, blaze.PreAuthReply(f.TunnelIP.String(), f.Ports.QoS, blaze.Service), "preAuth reply")
		return
	case fr.Component == blaze.ComponentUtil && fr.Command == blaze.CmdUtilPing:
		if bc.ping == nil {
			bc.ping = raw
		}
		bc.reply(fr, blaze.PingReply(uint32(f.Now().Unix())), "ping reply")
		return
	case fr.Component == blaze.ComponentUserSessions && fr.Command == cmdUserSessionsResume && bc.preAuth == nil:
		s := f.current()
		if s == nil {
			f.logf("Blaze %s: resumeSession, but no server session: not answered", bc.peer)
			return
		}
		up, err := bc.openBlaze(s, false)
		if err != nil {
			f.logf("Blaze %s: resumeSession to %s: %v (not answered)", bc.peer, s.b.Tag, err)
			return
		}
		if _, err := up.Write(raw); err != nil {
			up.Close()
			f.logf("Blaze %s: resumeSession to %s: %v", bc.peer, s.b.Tag, err)
			return
		}
		bc.setUp(up, s)
		f.logf("Blaze %s: resumeSession passed to %s (%s)", bc.peer, s.b.Tag, s.b.Gateway())
		return
	}
	f.logf("Blaze %s: (not answered before a login)", bc.peer)
}

// login handles Authentication::login: the server switch. The pick comes off MAIL: a tag (the login goes to that
// server), +<address>[=<name>] (the server added first, front_list.go) or -<tag> (the server removed, the login
// refused with FrontErrRemoved). The login goes to that server,
// through the connection's upstream when it is that server's already (a second try after a refused login), else
// through a new one (the session started or taken over, preAuth and ping replayed). A refusal is the front's
// error reply; the connection stays, as after a refused password.
func (bc *blazeConn) login(fr fire.Frame, raw []byte) {
	f := bc.f
	mail, name, tag, out, err := cleanLogin(raw)
	if err != nil {
		bc.errorReply(fr, frontErr(FrontErrServer, "the login request: %v", err))
		return
	}
	var b Bookmark
	switch {
	case len(tag) > 0 && tag[0] == pickRemove:
		bc.errorReply(fr, f.removeFromLogin(tag[1:]))
		return
	case len(tag) > 0 && tag[0] == pickAdd:
		b, err = f.addFromLogin(tag[1:])
	default:
		b, err = f.resolve(tag)
	}
	if err != nil {
		bc.errorReply(fr, asFrontError(err))
		return
	}
	f.logf("Blaze %s: login as %q (typed %q) to %s (%s)", bc.peer, name, mail, b.Tag, b.Gateway())
	if up, s := bc.upstreamOf(); up != nil && s == f.current() && s.b.Tag == b.Tag {
		bc.awaitLogin(fr.ID)
		if _, err := up.Write(out); err != nil {
			f.logf("Blaze %s: the server's connection failed: %v", bc.peer, err)
			bc.gc.Close()
		}
		return
	}
	bc.closeUp()
	start := time.Now()
	s, err := f.session(b)
	if err != nil {
		bc.errorReply(fr, asFrontError(err))
		return
	}
	up, err := bc.openBlaze(s, true)
	if err != nil {
		bc.errorReply(fr, asFrontError(err))
		return
	}
	bc.awaitLogin(fr.ID)
	if _, err := up.Write(out); err != nil {
		up.Close()
		bc.errorReply(fr, frontErr(FrontErrServer, "the login to %s: %v", b.Tag, err))
		return
	}
	bc.setUp(up, s)
	blazePort, qosPort := s.ports()
	f.logf("Blaze %s: switched to %s in %s (its Blaze %d, QoS %d); the connection is the server's from now on", bc.peer, b.Tag,
		time.Since(start).Round(time.Millisecond), blazePort, qosPort)
	if err := f.Bookmarks.Used(b.Tag, f.Now()); err != nil && !errors.Is(err, ErrNoBookmark) {
		f.logf("the server list: %v", err)
	}
	// the list file's last line now, while the game is away from the login screen: the movie loads the file when
	// the login screen comes back after a logout, at the same moment as the refresh the return asks for, and
	// preselects its last server then (a stale file preselected the server used before)
	f.wantList()
}

func asFrontError(err error) *frontError {
	var fe *frontError
	if errors.As(err, &fe) {
		return fe
	}
	return frontErr(FrontErrServer, "%v", err)
}

// cleanLogin takes the tag off a login request's MAIL: it returns MAIL as typed, the name, the tag and the request
// to send (as sent when MAIL has no '#'; else with the name only, the header's other bits kept).
func cleanLogin(raw []byte) (mail, name, tag string, out []byte, err error) {
	payload := raw[fire.HeaderLen:]
	st, err := tdf.Decode(payload)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("doesn't decode: %v", err)
	}
	mail, _ = st.Str("MAIL")
	name, tag, tagged := splitTag(mail)
	if !tagged {
		return mail, name, "", raw, nil
	}
	if !bytes.Equal(tdf.Encode(st), payload) {
		return mail, name, tag, nil, errors.New("doesn't encode again as sent")
	}
	for i := range st.Fields {
		if st.Fields[i].Label == "MAIL" {
			st.Fields[i].Value = name
		}
	}
	body := tdf.Encode(st)
	if len(body) > 0xFFFF {
		return mail, name, tag, nil, errors.New("too long")
	}
	out = append(append([]byte(nil), raw[:fire.HeaderLen]...), body...)
	binary.BigEndian.PutUint16(out[0:], uint16(len(body)))
	return mail, name, tag, out, nil
}

func (bc *blazeConn) upstream() net.Conn {
	up, _ := bc.upstreamOf()
	return up
}

func (bc *blazeConn) upstreamOf() (net.Conn, *frontSession) {
	bc.umu.Lock()
	defer bc.umu.Unlock()
	return bc.up, bc.upSess
}

// setUp makes up the connection's upstream and passes the server's frames to the game, whole, until it ends; when
// it ends while still the upstream, the game's connection is closed too (the client then reconnects).
func (bc *blazeConn) setUp(up net.Conn, s *frontSession) {
	done := make(chan struct{})
	bc.umu.Lock()
	bc.up, bc.upSess, bc.upDone = up, s, done
	bc.umu.Unlock()
	go func() {
		defer close(done)
		r := bufio.NewReader(up)
		for {
			raw, fr, err := readFrame(r)
			if err != nil {
				break
			}
			bc.loginAnswered(fr, s)
			if bc.write(raw) != nil {
				break
			}
		}
		bc.umu.Lock()
		still := bc.up == up
		if still {
			bc.up, bc.upSess = nil, nil
		}
		bc.umu.Unlock()
		up.Close()
		if still {
			bc.f.logf("Blaze %s: the server (%s) closed its side; closing the game's", bc.peer, s.b.Tag)
			bc.gc.Close()
		}
	}()
}

// awaitLogin notes a login passed to the server (its msgId): loginAnswered then looks for its reply.
func (bc *blazeConn) awaitLogin(id uint32) {
	bc.umu.Lock()
	bc.loginID, bc.loginWait = id, true
	bc.umu.Unlock()
}

// loginAnswered looks at a frame from the server for the reply to the login passed on: a reply means the player
// left the login screen (the list's refresh stops until the next logout), an error reply (the server refused the
// password, the account, ...) that the login screen still shows, so the list goes on refreshing.
func (bc *blazeConn) loginAnswered(fr fire.Frame, s *frontSession) {
	if fr.Component != blaze.ComponentAuthentication || fr.Command != blaze.CmdAuthLogin ||
		(fr.Type != fire.Reply && fr.Type != fire.ErrorReply) {
		return
	}
	bc.umu.Lock()
	match := bc.loginWait && fr.ID == bc.loginID
	if match {
		bc.loginWait = false
	}
	bc.umu.Unlock()
	if !match {
		return
	}
	if fr.Type == fire.Reply {
		bc.f.setAtLogin(false)
		return
	}
	bc.f.logf("Blaze %s: %s refused the login (error %#04x); the login screen still shows", bc.peer, s.b.Tag, fr.Error)
	bc.f.setAtLogin(true)
}

// closeUp ends the connection's upstream (if any) and waits until its frames have stopped.
func (bc *blazeConn) closeUp() {
	bc.umu.Lock()
	up, done := bc.up, bc.upDone
	bc.up, bc.upSess = nil, nil
	bc.umu.Unlock()
	if up != nil {
		up.Close()
		<-done
	}
}

// openBlaze opens a stream to the session's Blaze: the server's Blaze port is asked from its redirector once per
// session. With replay, the connection's preAuth and ping go first and their replies are dropped (preAuth's names
// the server's QoS port).
func (bc *blazeConn) openBlaze(s *frontSession, replay bool) (net.Conn, error) {
	f := bc.f
	blazePort, _ := s.ports()
	if blazePort == 0 {
		p, err := bc.askBlazePort(s)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.blaze, blazePort = p, p
		s.mu.Unlock()
	}
	up, err := s.c.OpenStream(blazePort, bc.src)
	if err != nil {
		return nil, frontErr(FrontErrServer, "no stream to %s's Blaze (port %d): %v", s.b.Tag, blazePort, err)
	}
	if !replay {
		return up, nil
	}
	r := bufio.NewReader(up)
	for _, req := range [][]byte{bc.preAuth, bc.ping} {
		if req == nil {
			continue
		}
		reqFrame, _ := fire.NewReader(bytes.NewReader(req)).Next()
		if _, err := up.Write(req); err != nil {
			up.Close()
			return nil, frontErr(FrontErrServer, "%s's Blaze: %v", s.b.Tag, err)
		}
		rep, err := bc.awaitReply(up, r, reqFrame)
		if err != nil {
			up.Close()
			return nil, frontErr(FrontErrServer, "%s's Blaze didn't answer the replayed %s: %v", s.b.Tag, describeCmd(reqFrame), err)
		}
		if reqFrame.Command == blaze.CmdUtilPreAuth {
			if st, err := tdf.Decode(rep.Payload); err == nil {
				if q, ok := st.Sub("QOSS"); ok {
					if bw, ok := q.Sub("BWPS"); ok {
						if p, ok := bw.Int("PSP"); ok && p > 0 && p < 65536 {
							s.mu.Lock()
							s.qos = int(p)
							s.mu.Unlock()
						}
					}
				}
			}
		}
		f.logf("Blaze %s: the replayed %s (id %d) answered by %s; the answer dropped", bc.peer, describeCmd(reqFrame), reqFrame.ID, s.b.Tag)
	}
	return &replayConn{Conn: up, r: r}, nil // what the server sent after the replies is read first
}

// awaitReply reads the server's frames until the reply to req (notifications before it are dropped).
func (bc *blazeConn) awaitReply(up net.Conn, r *bufio.Reader, req fire.Frame) (fire.Frame, error) {
	up.SetReadDeadline(time.Now().Add(bc.f.ReplyTimeout))
	defer up.SetReadDeadline(time.Time{})
	for {
		_, fr, err := readFrame(r)
		if err != nil {
			return fr, err
		}
		if fr.ID != req.ID || fr.Component != req.Component || fr.Command != req.Command {
			continue
		}
		switch fr.Type {
		case fire.Reply:
			return fr, nil
		case fire.ErrorReply:
			return fr, fmt.Errorf("error %#04x", fr.Error)
		}
	}
}

// askBlazePort asks the session's server's redirector for its Blaze port (getServerInstance through a stream; the
// redirector's TLS ends on the player's side, so the stream is plain).
func (bc *blazeConn) askBlazePort(s *frontSession) (int, error) {
	f := bc.f
	rc, err := s.c.OpenStream(tunnel.RedirectorPort, bc.src)
	if err != nil {
		return 0, frontErr(FrontErrServer, "no stream to %s's redirector: %v", s.b.Tag, err)
	}
	defer rc.Close()
	req, _ := fire.Encode(fire.Frame{Component: redirector.ComponentRedirector, Command: redirector.CmdGetServerInstance, Type: fire.Request,
		Payload: tdf.NewWriter().String("NAME", blaze.Service).Bytes()})
	if _, err := rc.Write(req); err != nil {
		return 0, frontErr(FrontErrServer, "%s's redirector: %v", s.b.Tag, err)
	}
	rep, err := bc.awaitReply(rc, bufio.NewReader(rc), fire.Frame{Component: redirector.ComponentRedirector, Command: redirector.CmdGetServerInstance})
	if err != nil {
		return 0, frontErr(FrontErrServer, "%s's redirector didn't answer: %v", s.b.Tag, err)
	}
	st, err := tdf.Decode(rep.Payload)
	if err != nil {
		return 0, frontErr(FrontErrServer, "%s's redirector: the reply doesn't decode: %v", s.b.Tag, err)
	}
	if a, ok := st.Get("ADDR"); ok {
		if u, ok := a.Value.(*tdf.Union); ok && u.Field != nil {
			if v, ok := u.Field.Value.(*tdf.Struct); ok {
				if p, ok := v.Int("PORT"); ok && p > 0 && p < 65536 {
					f.logf("%s's redirector names Blaze port %d", s.b.Tag, p)
					return int(p), nil
				}
			}
		}
	}
	return 0, frontErr(FrontErrServer, "%s's redirector named no Blaze port", s.b.Tag)
}

func describeCmd(fr fire.Frame) string {
	switch {
	case fr.Component == blaze.ComponentUtil && fr.Command == blaze.CmdUtilPreAuth:
		return "preAuth"
	case fr.Component == blaze.ComponentUtil && fr.Command == blaze.CmdUtilPing:
		return "ping"
	}
	return fmt.Sprintf("%s cmd %#04x", fire.ComponentName(fr.Component), fr.Command)
}
