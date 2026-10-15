package telemetry

// The wire format of DirtySDK TelemetryApi as Dawngate 1.0.903.854 sends it (docs/TELEMETRY.md §2; the
// addresses are the client's). Everything here is read from the client's sender: there is no server
// reply in the protocol that the client acts on (the reply handlers at ref +0x10914..+0x1091C and
// +0x640 are never installed), so this package only decodes.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// HeaderSize is ProtoAries' packet header: kind, code and total length (header included), each a
// big-endian uint32 (ProtoAriesSend 0x00CF3B00, ProtoAriesUpdate's receive path 0x00CF3660).
const HeaderSize = 12

// MaxPacket bounds a packet's length field. The client's ProtoAries buffer is 0x2000 bytes
// (TelemetryApiConnect 0x00FD4A20); anything far above is not a packet and makes the sink resync.
const MaxPacket = 1 << 16

// KindLogin is '&lgr', the only kind the client sends (TelemetryApiSend 0x00FD57E0 →
// TelemetryApiSendPacket 0x00FD4540). Every packet carries the queued events; the first one after a
// (re)connect also carries the login token.
const KindLogin = 0x266c6772

// Key is the TelemetryApi obfuscation key (.rdata 0x0130254C): the XOR key of the '@' cipher
// (0x00FD4850) and the MD5 prefix of the anonymous login token (0x00FD47C0).
const Key = "The truth is back in style."

// Packet is one ProtoAries packet.
type Packet struct {
	Kind, Code uint32
	Body       []byte // without the header
}

// Size is the packet's length on the wire.
func (p Packet) Size() int { return HeaderSize + len(p.Body) }

// KindName renders a kind as its four characters when they are printable ("&lgr"), else in hex.
func KindName(kind uint32) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], kind)
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("%#08x", kind)
		}
	}
	return string(b[:])
}

// errNotPacket is returned for a header whose length is impossible.
var errNotPacket = errors.New("not a ProtoAries packet")

// CutPacket takes the first packet off buf (a relay forwards packet by packet: Start-Client's front). ok is false
// while the packet is incomplete; an error says buf holds no packet framing.
func CutPacket(buf []byte) (p Packet, rest []byte, ok bool, err error) { return cutPacket(buf) }

// cutPacket takes the first packet off buf. ok is false while the packet is incomplete.
func cutPacket(buf []byte) (p Packet, rest []byte, ok bool, err error) {
	if len(buf) < HeaderSize {
		return Packet{}, buf, false, nil
	}
	n := binary.BigEndian.Uint32(buf[8:12])
	if n < HeaderSize || n > MaxPacket {
		return Packet{}, buf, false, fmt.Errorf("%w: length field %d", errNotPacket, n)
	}
	if uint32(len(buf)) < n {
		return Packet{}, buf, false, nil
	}
	p = Packet{
		Kind: binary.BigEndian.Uint32(buf[0:4]),
		Code: binary.BigEndian.Uint32(buf[4:8]),
		Body: append([]byte(nil), buf[HeaderSize:n]...),
	}
	return p, buf[n:], true, nil
}

// Field is one tag field of a packet body: "KEY=value", divided by '\n' (DirtySDK TagField, divider
// 0x01464CFC = '\n'); the body ends at its NUL.
type Field struct {
	Key   string
	Value []byte // as sent: not unescaped, not decoded
}

// Fields splits a body into its tag fields. The key ends at the first '='; neither the cipher's
// output nor the '^' encoding contains '\n' or '=' (see Decipher, Bin7).
func Fields(body []byte) []Field {
	if i := bytes.IndexByte(body, 0); i >= 0 {
		body = body[:i]
	}
	var out []Field
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		k, v, _ := bytes.Cut(line, []byte{'='})
		out = append(out, Field{Key: string(k), Value: v})
	}
	return out
}

// Bin7 decodes TagFieldSetRaw's binary value after its '^' (encoder 0x00CF5560): each byte carries 7
// bits, low bits first, with the high bit set; the last byte holds the remaining bits.
func Bin7(s []byte) []byte {
	out := make([]byte, 0, len(s)*7/8)
	var acc uint32
	bits := 0
	for _, c := range s {
		acc |= uint32(c&0x7f) << bits
		bits += 7
		for bits >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			bits -= 8
		}
	}
	return out
}

// Decipher undoes TelemetryApi's cipher (0x00FD4850, decode branch), used for STR and TLM3 once the
// client has set 'cryp' (GOSTelemetry_ConnectAnonymous 0x008BB270 does). The client enciphers the
// text with its NUL (L+1 bytes) and writes "@<n hex>-" with n = L+2, then n-1 bytes: byte i is
// plain[i] XOR Key[i % 27], with bit 7 flipped where that XOR fell below 0x16 (as a signed char) or
// was '=' (so the output has no control characters and no '='); the encoder overwrites the n-th byte
// with its terminator. The result ends at the first NUL. If the byte count doesn't match n-1, the
// bytes there are deciphered anyway and the error says so.
func Decipher(s []byte) ([]byte, error) {
	if len(s) < 3 || s[0] != '@' {
		return nil, errors.New("no '@' cipher header")
	}
	dash := bytes.IndexByte(s, '-')
	if dash < 2 {
		return nil, errors.New("no '-' after the cipher length")
	}
	n, err := strconv.ParseUint(string(s[1:dash]), 16, 32)
	if err != nil || n == 0 {
		return nil, fmt.Errorf("bad cipher length %q", s[1:dash])
	}
	body := s[dash+1:]
	if uint64(len(body)) != n-1 {
		err = fmt.Errorf("cipher length %d, but %d bytes follow", n, len(body))
	}
	out := make([]byte, len(body))
	for i, c := range body {
		x := Key[i%len(Key)] ^ c
		if x > 0x80 {
			x ^= 0x80
		}
		out[i] = x
	}
	if i := bytes.IndexByte(out, 0); i >= 0 {
		out = out[:i]
	}
	return out, err
}

// Unescape undoes TagFieldSetString (0x00CF4B00): a value with a blank or a comma is quoted, and
// control characters, '"', '%', '=' and DEL are written as %XX (character table 0x013038A8, bit 0).
func Unescape(v []byte) []byte {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		if v[i] == '%' && i+2 < len(v) {
			if b, err := strconv.ParseUint(string(v[i+1:i+3]), 16, 8); err == nil {
				out = append(out, byte(b))
				i += 2
				continue
			}
		}
		out = append(out, v[i])
	}
	return out
}

// Token is the decoded login token of ANON (TelemetryApiAuthentAnonymous 0x00FD52D0, token 0x00FD47C0):
// '$', then '^' and Bin7 of MD5(Key + text), text, '\n', NUL. The text is
// "<product>:<xxxx>:<unix time>:<xxxx>:<MAC as $%016x, or xxxx>:<locale>".
type Token struct {
	Text  string
	MD5OK bool
}

// ParseToken decodes an ANON value.
func ParseToken(v []byte) (Token, error) {
	v = Unescape(v)
	if !bytes.HasPrefix(v, []byte("$^")) {
		return Token{}, errors.New("no '$^' token prefix")
	}
	raw := Bin7(v[2:])
	if len(raw) < md5.Size {
		return Token{}, errors.New("token shorter than its MD5")
	}
	text := raw[md5.Size:]
	if i := bytes.IndexByte(text, 0); i >= 0 {
		text = text[:i]
	}
	text = bytes.TrimSuffix(text, []byte{'\n'})
	sum := md5.Sum(append([]byte(Key), text...))
	return Token{Text: string(text), MD5OK: bytes.Equal(sum[:], raw[:md5.Size])}, nil
}

// Event is one TLM3 event (TelemetryApiSubmitEvent3 0x00FD61E0):
// "%08X/%c;%08X/%c%c%c%c/%c%c%c%c/%c%c%c%c/" then the attributes "KEY=value&KEY=value…".
type Event struct {
	Time   uint32 // seconds since TelemetryApiCreate; 0xFFFFFFFF = stamp on the server ('stim', set)
	Mark   byte   // 'logc' control character, '-' when unset
	Seq    uint32 // per-connection counter (ref +0x680)
	Module string // e.g. SESS, MATC
	Group  string // e.g. CLNT, SERV
	ID     string // string id, e.g. PROC, UIUI
	Attrs  []Attr // in the order sent; the first is EVNT, the event's LABS_* name
}

// Attr is one event attribute. Values are sanitized by the client (TelemetryApiEncAttributeString
// 0x00FD4E80: anything outside [0-9A-Za-z.+-_~$] became '_'), so they need no unescaping.
type Attr struct{ Key, Value string }

// ServerTime is Event.Time for events the server is to timestamp.
const ServerTime = 0xFFFFFFFF

// Name is the event's EVNT attribute.
func (e Event) Name() string {
	for _, a := range e.Attrs {
		if a.Key == "EVNT" {
			return a.Value
		}
	}
	return ""
}

// event3Header is the length of the fixed part of a TLM3 event.
const event3Header = 35

// parseEventHeader reads the fixed part of a TLM3 event; ok is false if s doesn't start with one.
func parseEventHeader(s []byte) (e Event, ok bool) {
	if len(s) < event3Header || s[8] != '/' || s[10] != ';' || s[19] != '/' || s[24] != '/' ||
		s[29] != '/' || s[34] != '/' {
		return Event{}, false
	}
	t, err1 := parseHex8(s[0:8])
	seq, err2 := parseHex8(s[11:19])
	if err1 != nil || err2 != nil {
		return Event{}, false
	}
	return Event{Time: t, Mark: s[9], Seq: seq, Module: string(s[20:24]), Group: string(s[25:29]),
		ID: string(s[30:34])}, true
}

func parseHex8(s []byte) (uint32, error) {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return 0, errors.New("not upper-case hex")
		}
	}
	v, err := strconv.ParseUint(string(s), 16, 32)
	return uint32(v), err
}

// ParseEvents splits TLM3's text into events. Events are joined by ','; a ',' that isn't followed by
// an event header stays in the event (the client never sends one: commas are sanitized away).
// Text before the first header, or a part that doesn't parse, is returned in bad.
func ParseEvents(text []byte) (events []Event, bad [][]byte) {
	var parts [][]byte
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == ',' {
			if _, ok := parseEventHeader(text[i+1:]); ok {
				parts = append(parts, text[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, text[start:])
	for _, p := range parts {
		p = bytes.TrimSuffix(p, []byte{','})
		if len(p) == 0 {
			continue
		}
		e, ok := parseEventHeader(p)
		if !ok {
			bad = append(bad, p)
			continue
		}
		for _, a := range bytes.Split(p[event3Header:], []byte{'&'}) {
			if len(a) == 0 {
				continue
			}
			k, v, _ := bytes.Cut(a, []byte{'='})
			e.Attrs = append(e.Attrs, Attr{Key: string(k), Value: string(v)})
		}
		events = append(events, e)
	}
	return events, bad
}

// Batch is one decoded '&lgr' packet.
type Batch struct {
	T       string   // T: %08X, seconds since TelemetryApiCreate at the login (+0x108E8)
	Login   string   // "ANON" or "AUTH" if the packet carries a login, else ""
	Token   *Token   // the decoded ANON token
	Auth    string   // AUTH's value (the Blaze path's key; not used by the game's own telemetry)
	Events  []Event  // TLM3
	Legacy  []string // STR: TelemetryApiEvent ring entries, "%08X/%c;%08X/%c%c%c%c/%c%c%c%c/%s"
	Other   []Field  // fields this build doesn't send (SUB, …)
	Problem []string // what didn't decode
}

// DecodeBatch decodes an '&lgr' body (TelemetryApiSend 0x00FD57E0): [SUB=] T= STR= TLM3= [ANON=|AUTH=].
func DecodeBatch(body []byte) Batch {
	var b Batch
	for _, f := range Fields(body) {
		switch f.Key {
		case "T":
			b.T = string(Unescape(f.Value))
		case "STR":
			v := Unescape(f.Value)
			if len(v) > 0 && v[0] == '^' {
				v = Bin7(v[1:])
			}
			if len(v) > 0 && v[0] == '@' {
				d, err := Decipher(v)
				if err != nil {
					b.Problem = append(b.Problem, "STR: "+err.Error())
				}
				v = d
			}
			for _, s := range bytes.Split(v, []byte{','}) {
				if len(s) > 0 {
					b.Legacy = append(b.Legacy, string(s))
				}
			}
		case "TLM3":
			v := f.Value // written raw, not through TagFieldSetString
			if len(v) > 0 && v[0] == '@' {
				d, err := Decipher(v)
				if err != nil {
					b.Problem = append(b.Problem, "TLM3: "+err.Error())
				}
				v = d
			}
			ev, bad := ParseEvents(v)
			b.Events = append(b.Events, ev...)
			for _, p := range bad {
				b.Problem = append(b.Problem, "TLM3: not an event: "+printable(p))
			}
		case "ANON":
			b.Login = "ANON"
			t, err := ParseToken(f.Value)
			if err != nil {
				b.Problem = append(b.Problem, "ANON: "+err.Error())
				continue
			}
			b.Token = &t
		case "AUTH":
			b.Login = "AUTH"
			b.Auth = string(Unescape(f.Value))
		default:
			b.Other = append(b.Other, f)
		}
	}
	return b
}

// printable renders bytes for a log line, escaping what isn't printable ASCII.
func printable(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, `\x%02x`, c)
		}
	}
	return sb.String()
}

// Line renders an event as one log line: "#seq NAME MODULE/GROUP/ID: KEY=value …".
func (e Event) Line() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "#%d %s %s/%s/%s", e.Seq, printable([]byte(e.Name())), printable([]byte(e.Module)),
		printable([]byte(e.Group)), printable([]byte(e.ID)))
	if e.Time != ServerTime {
		fmt.Fprintf(&sb, " t=%d", e.Time)
	}
	if e.Mark != '-' {
		fmt.Fprintf(&sb, " mark=%s", printable([]byte{e.Mark}))
	}
	sb.WriteByte(':')
	for _, a := range e.Attrs {
		if a.Key == "EVNT" {
			continue
		}
		sb.WriteByte(' ')
		sb.WriteString(printable([]byte(a.Key)))
		sb.WriteByte('=')
		sb.WriteString(printable([]byte(a.Value)))
	}
	return sb.String()
}
