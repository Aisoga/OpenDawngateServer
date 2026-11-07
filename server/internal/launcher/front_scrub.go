package launcher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/web"
)

// What a server may put in front of the game and learn from it. The player adds a server by its address and confirms its
// key once; that says who the server is, not that its operator means well. So the front decides these things itself,
// and a server of this project's release loses nothing by it:
//
//   - The game's web content (the web view runs a 2011 WebKit with the game's natives; dynamic_lore.snap is mounted over the
//     game's own files; the XML goes to its parsers) comes from Start-Client's own copy of it (web.Builtins), the same
//     build as the server's. Only the JSON the pages read comes from the server, and the pages put it into the
//     document as text (their own code: no innerHTML, no script from data).
//   - DRC names a file the game writes (Dynamic/dl/dl.<DRC>.snap): a name, never a path.
//   - The addresses the game dials itself (the match's GMS, the telemetry server) are always the tunnel address: the rest of
//     the way is the tunnel's, so another address could only send the player's traffic outside the tunnel.
//   - The player's MAC address stays on this PC.
//
// picker = off and tunnel = off have no front: a player who sets them trusts that one server with all of this.

// isServerData says whether a request of the game's goes to the server: only the JSON the game's own pages read.
func isServerData(path string) bool {
	for _, s := range []string{web.HomeJSONSuffix, web.GamesJSONSuffix, web.GameJSONSuffix} {
		if strings.HasSuffix(path, s) {
			return true
		}
	}
	return false
}

// safeDRC is what a web content version may be: BGWeb's DRC becomes part of a file name and of a URL path in the game.
var safeDRC = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// fallbackDRC replaces a DRC that isn't a plain name (blaze.WebContentVersion).
const fallbackDRC = "1"

// scrubFromServer returns the frame to give the game: raw itself unless the server said something it may not, and what it
// corrected ("": nothing). Only replies and notifications that name files or addresses are looked into, so every other
// frame reaches the game byte for byte.
func scrubFromServer(raw []byte, fr fire.Frame, tunnelIP net.IP) ([]byte, string) {
	if fr.Type != fire.Reply && fr.Type != fire.Notification {
		return raw, ""
	}
	switch {
	case fr.Component == blaze.ComponentGameManager:
	case fr.Component == blaze.ComponentUtil && (fr.Command == blaze.CmdUtilPostAuth || fr.Command == blaze.CmdUtilGetTelemetryServer):
	case fr.Component == blaze.ComponentBGWeb && fr.Command == blaze.CmdBGWebGetCurrentVers:
	default:
		return raw, ""
	}
	st, err := tdf.Decode(fr.Payload)
	if err != nil {
		return raw, ""
	}
	ip := tunnelIP.To4()
	if ip == nil {
		return raw, ""
	}
	var why []string
	if fr.Component == blaze.ComponentBGWeb {
		if v, ok := st.Str("DRC"); ok && !safeDRC.MatchString(v) {
			setField(st, "DRC", fallbackDRC)
			why = append(why, "DRC "+clipText(v))
		}
	} else {
		fixAddresses(st, ip, &why)
	}
	if len(why) == 0 {
		return raw, ""
	}
	out, err := reframe(raw, tdf.Encode(st))
	if err != nil {
		return raw, ""
	}
	return out, strings.Join(why, ", ")
}

// fixAddresses walks a message: an HNET entry's addresses and an ADRS that isn't empty become the tunnel address.
func fixAddresses(st *tdf.Struct, ip net.IP, why *[]string) {
	want := int64(binary.BigEndian.Uint32(ip))
	for i := range st.Fields {
		f := &st.Fields[i]
		switch {
		case f.Label == "HNET":
			forEachUnion(f.Value, func(s *tdf.Struct) {
				for _, label := range []string{"EXIP", "INIP"} {
					if a, ok := s.Sub(label); ok {
						if got, ok := a.Int("IP"); ok && got != want && got != 0 {
							setField(a, "IP", want)
							*why = append(*why, "HNET "+label+" "+net.IPv4(byte(got>>24), byte(got>>16), byte(got>>8), byte(got)).String())
						}
					}
				}
			})
		case f.Label == "ADRS":
			if v, ok := f.Value.(string); ok && v != "" && v != ip.String() {
				f.Value = ip.String()
				*why = append(*why, "ADRS "+clipText(v))
			}
		default:
			switch v := f.Value.(type) {
			case *tdf.Struct:
				fixAddresses(v, ip, why)
			case *tdf.List:
				for _, item := range v.Items {
					if s, ok := item.(*tdf.Struct); ok {
						fixAddresses(s, ip, why)
					}
				}
			}
		}
	}
}

// forEachUnion calls fn with each union's struct of a list of unions.
func forEachUnion(v any, fn func(*tdf.Struct)) {
	l, ok := v.(*tdf.List)
	if !ok {
		return
	}
	for _, item := range l.Items {
		switch u := item.(type) {
		case tdf.ListUnion:
			if u.Value != nil {
				fn(u.Value)
			}
		case *tdf.ListUnion:
			if u != nil && u.Value != nil {
				fn(u.Value)
			}
		case *tdf.Struct:
			fn(u)
		}
	}
}

func setField(s *tdf.Struct, label string, v any) {
	for i := range s.Fields {
		if s.Fields[i].Label == label {
			s.Fields[i].Value = v
			return
		}
	}
}

// reframe is a frame with another payload: its header's other bits as they were.
func reframe(raw, body []byte) ([]byte, error) {
	if len(body) > 0xFFFF {
		return nil, errors.New("too long")
	}
	out := append(append([]byte(nil), raw[:fire.HeaderLen]...), body...)
	binary.BigEndian.PutUint16(out[0:], uint16(len(body)))
	return out, nil
}

// clipText makes a server's text fit for one log line.
func clipText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return '?'
		}
		return r
	}, s)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// scrubPreAuth is the game's preAuth as a server may see it: without the MAC address and hardware id, which would follow
// the player from server to server. Blaze's own check of them is the server's, and ours has none.
func scrubPreAuth(raw []byte) []byte {
	if len(raw) < fire.HeaderLen {
		return raw
	}
	st, err := tdf.Decode(raw[fire.HeaderLen:])
	if err != nil {
		return raw
	}
	info, ok := st.Sub("CINF")
	if !ok {
		return raw
	}
	changed := false
	for _, label := range []string{"MAC", "HWID"} {
		if v, ok := info.Str(label); ok && v != "" {
			setField(info, label, "")
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := reframe(raw, tdf.Encode(st))
	if err != nil {
		return raw
	}
	return out
}

// maxResponseHead bounds an HTTP answer's head from a server (a status line and headers; ours are a few hundred bytes).
const maxResponseHead = 64 << 10

// headLimit is a reader that fails past its limit until off is called, for reading an answer's head from a server, which
// could otherwise send headers forever. The body then reads freely, under its own limits.
type headLimit struct {
	r    io.Reader
	left int
	off  bool
}

var errHeadTooLong = errors.New("the server's answer has a head over the limit")

func (h *headLimit) Read(p []byte) (int, error) {
	if h.off {
		return h.r.Read(p)
	}
	if h.left <= 0 {
		return 0, errHeadTooLong
	}
	if len(p) > h.left {
		p = p[:h.left]
	}
	n, err := h.r.Read(p)
	h.left -= n
	return n, err
}

// telemetryToken says whether a telemetry packet's body carries the anonymous login token (with the player's MAC address).
func telemetryToken(body []byte) bool {
	return bytes.Contains(body, []byte("ANON="))
}
