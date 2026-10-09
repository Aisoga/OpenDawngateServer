// Package telemetry is the telemetry sink on NewTelemetryServerIP/Port (Config.package, pointed at us
// by re/scripts/patch_config.py), the session's debug channel (roadmap R5, docs/TELEMETRY.md).
//
// The client's DirtySDK TelemetryApi speaks ProtoAries over TCP and only ever sends '&lgr' packets:
// the first after a (re)connect carries the anonymous login token, and every packet carries the
// LABS_* events queued since the last one, enciphered. The sink decodes them and logs one line per
// packet and one per event. It never answers: the client installs no handler for any reply
// (TelemetryApiUpdate 0x00FD5D80 calls only null callbacks), and it sends its events without one.
package telemetry

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"dawngate/server/internal/logging"
)

// Sink logs telemetry connections.
type Sink struct {
	Log *logging.Log
}

// Serve accepts connections until the listener is closed.
func (s *Sink) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(c)
	}
}

const who = "telemetry"

// ServeConn decodes packets until the client closes the connection. Bytes that aren't a ProtoAries
// packet are hexdumped, and the rest of that connection is hexdumped as it arrives, as before R5.
func (s *Sink) ServeConn(c net.Conn) {
	defer c.Close()
	peer := logging.Peer(c.RemoteAddr())
	s.Log.Log(who, "connection from "+peer)
	var pending []byte
	total, packets, events := 0, 0, 0
	raw := false // lost the packet framing: dump what follows
	buf := make([]byte, 65536)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			total += n
			if raw {
				s.dump(peer, fmt.Sprintf("%d bytes", n), buf[:n])
			} else {
				pending = append(pending, buf[:n]...)
				for {
					p, rest, ok, perr := cutPacket(pending)
					if perr != nil {
						s.dump(peer, fmt.Sprintf("%d bytes, %v", len(pending), perr), pending)
						pending, raw = nil, true
						break
					}
					if !ok {
						break
					}
					pending = rest
					packets++
					events += s.logPacket(peer, p)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.Log.Logf(who, "%s %s: %v", peer, logging.ErrName(err), err)
			}
			break
		}
	}
	if len(pending) > 0 {
		s.dump(peer, fmt.Sprintf("%d bytes left over, not a full packet", len(pending)), pending)
	}
	// run_session.ps1 reads "closed after N bytes" for its summary
	s.Log.Logf(who, "%s closed after %d bytes, %s, %s", peer, total, plural(packets, "packet"), plural(events, "event"))
}

// dump logs a hexdump of b under a heading.
func (s *Sink) dump(peer, what string, b []byte) {
	more := ""
	if len(b) > logging.MaxDump {
		more = fmt.Sprintf(" (first %d shown)", logging.MaxDump)
	}
	s.Log.Logf(who, "%s %s%s:\n%s", peer, what, more, logging.Hexdump(b[:min(len(b), logging.MaxDump)]))
}

// logPacket logs one packet and its events; it returns the number of events.
func (s *Sink) logPacket(peer string, p Packet) int {
	if p.Kind != KindLogin {
		s.dump(peer, fmt.Sprintf("<- %s code %#x, %d bytes (not a telemetry packet)", KindName(p.Kind), p.Code, p.Size()), p.Body)
		return 0
	}
	b := DecodeBatch(p.Body)
	var parts []string
	switch {
	case b.Token != nil:
		ok := "MD5 ok"
		if !b.Token.MD5OK {
			ok = "MD5 wrong"
		}
		parts = append(parts, fmt.Sprintf("anonymous login %s (%s)", printable([]byte(b.Token.Text)), ok))
	case b.Login == "AUTH":
		parts = append(parts, "login AUTH="+printable([]byte(b.Auth)))
	}
	parts = append(parts, "T="+printable([]byte(b.T)))
	if p.Code != 0 {
		parts = append(parts, fmt.Sprintf("code %#x", p.Code))
	}
	for _, f := range b.Other {
		parts = append(parts, f.Key+"="+printable(f.Value))
	}
	parts = append(parts, plural(len(b.Events), "event"))
	s.Log.Logf(who, "%s <- &lgr, %d bytes: %s", peer, p.Size(), strings.Join(parts, ", "))
	for _, e := range b.Events {
		s.Log.Logf(who, "%s %s", peer, e.Line())
	}
	for _, l := range b.Legacy {
		s.Log.Logf(who, "%s STR event %s", peer, printable([]byte(l)))
	}
	for _, pr := range b.Problem {
		s.Log.Logf(who, "%s %s", peer, pr)
	}
	if len(b.Problem) > 0 {
		s.dump(peer, "the packet's body", p.Body)
	}
	return len(b.Events)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
