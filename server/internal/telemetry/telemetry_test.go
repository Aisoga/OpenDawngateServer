package telemetry

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// Three packets as the client sent them in session d7_run5 (the sink's hexdumps, re/logs/d7_run5):
// the first after connecting (LABS_QOS_DATA and the anonymous login token), the login screen's LABS_UI
// and LABS_JOINED_MATCH.
var (
	pkLogin = mustHex("266c67720000000000000127543d30303030303030300a5354523d5ec0e4b4a1850a544c4d333d4061332d922e236632" +
		"343332478d5243905251535b9059417336272a4326621a3c4a693a343a5b2d7627271d2e2021387f3821732c30383824" +
		"889126336949203959385286178d30848f8e411a8b8623313028581e722a3069304f445a588e5043934c59565f862d2f" +
		"7432499583864f383754862737262055904f26662e265e5b86273b693749494a366f1938589144425b4458904f34742b" +
		"2c5e5a17505f94404d4f5a572e0a414e4f4e3d245ef993c0bccc9d90f8ce93aec2b3b2ecb5a0a1c39bf6c5d8b0f4e8b1" +
		"abf6ccdcb7f5dc919bd7a5cc96f0c6e9c1878f9ebcbae2dcc99386cd99b9ecd4b9a3879ebcf8f0e9c1878f9ebcbacab9" +
		"abb5ca82800a00")
	pkUI = mustHex("266c67720000000000000119543d30303030303030300a5354523d5ec0e4b4a1850a544c4d333d4065652d922e236632" +
		"343332478d5243905251535b905a417336272a4326621a3c4a75bd273c5b2d7627271d2e2021387f3c2786363a2f2558" +
		"7c184535521b1658268d4c8c92538747332e692d53924344495c551e645a538636273c30559147438e5b515045185c5a" +
		"862333343f581f67515292522226262b1d851c478b8f901f411d8b7f92178d85934b72202464384f91431a954f236e23" +
		"2c5e4d732cbd744e454e55541a675153174154203224675443863220242e1d2581471a1a2a8f174b3186436e213b3149" +
		"599059439052515359164fbd613e24445d551e7a585590523521bd251d58441953555052165e5b2000")
	pkJoined = mustHex("266c677200000000000000e7543d30303030303030300a5354523d5ec0e4b4a1850a544c4d333d4062632d922e236632" +
		"343332478d5243905251535b9050417336272a4326621a3c4a7026bd365b2d7627271d2e2021387f232169bd31bd3328" +
		"6f802b2d86313c23bd5572255e70908e8746728c824592871c4a356b1d2c58924442454458905941164423362264545f" +
		"8e435a405c56806c5d51863926363c55904f206531355e5a17505f94404d4f5b5088812e29674942533abd692d4e9152" +
		"51535b90595e9245522a2d287e695955905a4245444e67bd3a6d5f505452915d5d194446496c00")
)

const (
	evQoS = "#0 LABS_QOS_DATA SESS/CLNT/INFO: ENVI=RL-Prod-Release PEID=0 BUID=1.0.903.854 DATA=local_1 SEST=0 " +
		"UFLG=0 NUID=0 SAMP=100.000 GTIM=1791439662"
	evUI = "#3 LABS_UI SESS/CLNT/UIUI: ENVI=RL-Prod-Release PEID=2000000026 BUID=1.0.903.854 PGMS=13972 " +
		"PSRC=loginstate_active HADL=d7r5 PNAM= SEST=1791439675 UFLG=0 PAGE=LoginScreen NUID=1000000026 " +
		"SAMP=100.000 GTIM=1791439675"
	evJoined = "#9 LABS_JOINED_MATCH SESS/CLNT/PROC: ENVI=RL-Prod-Release PEID=2000000026 BUID=1.0.903.854 MTCH=0 " +
		"SEST=1791439675 UFLG=0 NUID=1000000026 SAMP=100.000 GTIM=1791439720"
	loginText = "pc/battlegrounds-1-pc:xxxx:1791439657:xxxx:xxxx:enUS"
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// The client's encoders, written from the static reading (docs/TELEMETRY.md §2), to build test packets.

// encipher is 0x00FD4850's encode branch for n = len(plain)+1: "@<n>-" and n-1 bytes (the n-th, the
// terminator, is overwritten with a NUL by the encoder).
func encipher(plain []byte) []byte {
	out := []byte(fmt.Sprintf("@%x-", len(plain)+1))
	for i, c := range plain {
		k := Key[i%len(Key)]
		x := k ^ c
		if int8(x) < 0x16 || x == '=' {
			x = (c + 0x80) ^ k
		}
		out = append(out, x)
	}
	return out
}

// bin7 is TagFieldSetRaw's encoder 0x00CF5560.
func bin7(b []byte) []byte {
	var out []byte
	var acc uint32
	bits := 0
	for _, c := range b {
		acc |= uint32(c) << bits
		bits += 8
		for bits >= 7 {
			out = append(out, byte(acc&0x7f)|0x80)
			acc >>= 7
			bits -= 7
		}
	}
	if bits > 0 {
		out = append(out, byte(acc&0x7f)|0x80)
	}
	return out
}

// token is the ANON value (0x00FD47C0): '$', '^' and bin7(MD5(Key+text), text, '\n', NUL).
func token(text string) []byte {
	sum := md5.Sum([]byte(Key + text))
	return append([]byte("$^"), bin7(append(sum[:], text+"\n\x00"...))...)
}

// event3 formats one TLM3 event the way 0x00FD61E0 does.
func event3(seq uint32, mod, grp, id string, attrs ...string) string {
	return fmt.Sprintf("FFFFFFFF/-;%08X/%s/%s/%s/%s", seq, mod, grp, id, strings.Join(attrs, "&"))
}

// packet builds an '&lgr' packet as 0x00FD57E0 and ProtoAriesSend do: T, STR (bin7 of the enciphered
// legacy events and their NUL), TLM3 (the enciphered events and their NUL: the event buffer's last ','
// becomes the NUL and is counted), optionally ANON; NUL-terminated.
func packet(events []string, legacy string, anon string) []byte {
	body := "T=00000000\nSTR=^" + string(bin7(encipher([]byte(legacy+"\x00")))) +
		"\nTLM3=" + string(encipher([]byte(strings.Join(events, ",")+"\x00")))
	if anon != "" {
		body += "\nANON=" + string(token(anon)) + "\n"
	}
	body += "\x00"
	b := make([]byte, HeaderSize, HeaderSize+len(body))
	binary.BigEndian.PutUint32(b[0:4], KindLogin)
	binary.BigEndian.PutUint32(b[8:12], uint32(HeaderSize+len(body)))
	return append(b, body...)
}

func TestEncodersReproduceTheClient(t *testing.T) {
	// The test encoders give the client's bytes for the login packet, so they can stand in for it.
	p, _, ok, err := cutPacket(pkLogin)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	b := DecodeBatch(p.Body)
	if len(b.Events) != 1 {
		t.Fatalf("events: %+v", b)
	}
	var attrs []string
	for _, a := range b.Events[0].Attrs {
		attrs = append(attrs, a.Key+"="+a.Value)
	}
	again := packet([]string{event3(0, "SESS", "CLNT", "INFO", attrs...)}, "", loginText)
	if !bytes.Equal(again, pkLogin) {
		t.Errorf("rebuilt login packet differs:\n%x\n%x", again, pkLogin)
	}
}

func TestDecodeLoginPacket(t *testing.T) {
	p, rest, ok, err := cutPacket(append(append([]byte(nil), pkLogin...), pkUI[:20]...))
	if !ok || err != nil || p.Kind != KindLogin || p.Code != 0 || p.Size() != 295 || len(rest) != 20 {
		t.Fatalf("cutPacket: %v %v %+v rest %d", ok, err, p, len(rest))
	}
	b := DecodeBatch(p.Body)
	if b.T != "00000000" || b.Login != "ANON" || b.Token == nil || b.Token.Text != loginText || !b.Token.MD5OK {
		t.Errorf("login: %+v %+v", b, b.Token)
	}
	if len(b.Legacy) != 0 || len(b.Other) != 0 || len(b.Problem) != 0 {
		t.Errorf("unexpected: %+v", b)
	}
	if len(b.Events) != 1 || b.Events[0].Line() != evQoS || b.Events[0].Name() != "LABS_QOS_DATA" ||
		b.Events[0].Time != ServerTime || b.Events[0].Mark != '-' {
		t.Errorf("events: %+v", b.Events)
	}
}

func TestDecodeEventPackets(t *testing.T) {
	for _, c := range []struct {
		pk   []byte
		line string
	}{{pkUI, evUI}, {pkJoined, evJoined}} {
		p, _, ok, _ := cutPacket(c.pk)
		b := DecodeBatch(p.Body)
		if !ok || b.Login != "" || b.Token != nil || len(b.Problem) != 0 || len(b.Events) != 1 || b.Events[0].Line() != c.line {
			t.Errorf("%+v", b)
		}
	}
}

func TestDecodeBatchOfSeveral(t *testing.T) {
	evs := []string{
		event3(1, "SESS", "CLNT", "PROC", "EVNT=LABS_LOGIN_SUCCESS", "UUID=0", "PNAM="),
		event3(2, "MATC", "SERV", "TICK", "EVNT=LABS_PACE_AGGR", "POSX=1.500"),
		event3(0x1A, "SESS", "CLNT", "EROR", "EVNT=LABS_CONNECT_TO_GMS_FAILED"),
	}
	b := DecodeBatch(packet(evs, "0000002A/-;00000007/MODL/GRUP/hello,0000002B/-;00000008/MODL/GRUP/-", "")[HeaderSize:])
	want := []string{
		"#1 LABS_LOGIN_SUCCESS SESS/CLNT/PROC: UUID=0 PNAM=",
		"#2 LABS_PACE_AGGR MATC/SERV/TICK: POSX=1.500",
		"#26 LABS_CONNECT_TO_GMS_FAILED SESS/CLNT/EROR:",
	}
	if len(b.Events) != len(want) || len(b.Problem) != 0 {
		t.Fatalf("%+v", b)
	}
	for i, w := range want {
		if got := b.Events[i].Line(); got != w {
			t.Errorf("event %d: %q, want %q", i, got, w)
		}
	}
	if strings.Join(b.Legacy, "|") != "0000002A/-;00000007/MODL/GRUP/hello|0000002B/-;00000008/MODL/GRUP/-" {
		t.Errorf("STR: %q", b.Legacy)
	}
}

func TestCipher(t *testing.T) {
	var all []byte
	for c := 1; c < 0x80; c++ {
		all = append(all, byte(c))
	}
	for _, plain := range [][]byte{all, []byte("EVNT=LABS_UI&PAGE=x"), {}, bytes.Repeat([]byte("=\x01z"), 40)} {
		enc := encipher(plain)
		body := enc[bytes.IndexByte(enc, '-')+1:]
		for _, c := range body {
			if c < 0x16 || c == '=' || c == '\n' {
				t.Fatalf("cipher output byte %#x", c)
			}
		}
		got, err := Decipher(enc)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("round trip of %q: %q, %v", plain, got, err)
		}
	}
	for _, bad := range []string{"", "@", "x1-a", "@-", "@zz-a", "@0-"} {
		if _, err := Decipher([]byte(bad)); err == nil {
			t.Errorf("Decipher(%q) accepted", bad)
		}
	}
	if got, err := Decipher([]byte("@5-T")); err == nil || string(got) != "" {
		t.Errorf("short body: %q %v", got, err)
	}
}

func TestBin7(t *testing.T) {
	for n := 0; n < 40; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*37 + n)
		}
		enc := bin7(b)
		if len(enc) != (n*8+6)/7 {
			t.Errorf("bin7 of %d bytes is %d long", n, len(enc))
		}
		if got := Bin7(enc); !bytes.Equal(got, b) {
			t.Errorf("Bin7 round trip of %x: %x", b, got)
		}
	}
	// STR of an empty ring, as in every packet of d7_run5: "^" + bin7("@2-T")
	if got := Bin7([]byte{0xc0, 0xe4, 0xb4, 0xa1, 0x85}); string(got) != "@2-T" {
		t.Errorf("STR: %q", got)
	}
}

func TestToken(t *testing.T) {
	tok, err := ParseToken(token("product:xxxx:7:xxxx:$0000001122334455:deDE"))
	if err != nil || tok.Text != "product:xxxx:7:xxxx:$0000001122334455:deDE" || !tok.MD5OK {
		t.Errorf("%+v %v", tok, err)
	}
	bad := token("abc")
	bad[3] ^= 0x01 // a bit of the MD5
	if tok, err := ParseToken(bad); err != nil || tok.MD5OK || tok.Text != "abc" {
		t.Errorf("tampered: %+v %v", tok, err)
	}
	if _, err := ParseToken([]byte("plain")); err == nil {
		t.Error("accepted a token without $^")
	}
}

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{`"a b,c"`: "a b,c", "x%3Dy%25": "x=y%", "%zz%4": "%zz%4", `"`: `"`} {
		if got := string(Unescape([]byte(in))); got != want {
			t.Errorf("Unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFields(t *testing.T) {
	f := Fields([]byte("T=1\nSTR=a=b\n\nSUB=x\x00ignored=1"))
	if len(f) != 3 || f[0].Key != "T" || string(f[1].Value) != "a=b" || f[2].Key != "SUB" {
		t.Errorf("%+v", f)
	}
}

// lockedBuffer collects log text written from the connection goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// session plays chunks to a sink on an ephemeral loopback port, closes, and returns the peer's name
// and the log once the sink has logged the close.
func session(t *testing.T, chunks ...[]byte) (string, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf lockedBuffer
	go (&Sink{Log: logging.New(&buf)}).Serve(ln)
	c, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	peer := logging.Peer(c.LocalAddr())
	for _, ch := range chunks {
		if _, err := c.Write(ch); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond) // separate reads on the sink's side
	}
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), " closed after ") {
		if time.Now().After(deadline) {
			t.Fatalf("no close logged:\n%s", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return peer, buf.String()
}

func lines(log string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(log, "\n"), "\n") {
		if i := strings.Index(l, " [telemetry] "); i >= 0 {
			out = append(out, l[i+len(" [telemetry] "):])
		} else {
			out = append(out, l)
		}
	}
	return out
}

func TestSinkSession(t *testing.T) {
	// The client's first three packets of interest, split and joined across reads.
	stream := append(append(append([]byte(nil), pkLogin...), pkUI...), pkJoined...)
	peer, log := session(t, stream[:5], stream[5:300], stream[300:])
	want := []string{
		"connection from " + peer,
		peer + " <- &lgr, 295 bytes: anonymous login " + loginText + " (MD5 ok), T=00000000, 1 event",
		peer + " " + evQoS,
		peer + " <- &lgr, 281 bytes: T=00000000, 1 event",
		peer + " " + evUI,
		peer + " <- &lgr, 231 bytes: T=00000000, 1 event",
		peer + " " + evJoined,
		peer + " closed after 807 bytes, 3 packets, 3 events",
	}
	if got := lines(log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSinkSeveralEventsAndLeftover(t *testing.T) {
	pk := packet([]string{
		event3(1, "SESS", "CLNT", "PROC", "EVNT=LABS_LOGIN_SUCCESS", "UUID=0"),
		event3(2, "SESS", "CLNT", "STND", "EVNT=LABS_SYSINFO", "OSYS=Windows_8"),
	}, "", "")
	peer, log := session(t, pk, []byte("&lgr\x00\x00"))
	got := lines(log)
	want := []string{
		peer + fmt.Sprintf(" <- &lgr, %d bytes: T=00000000, 2 events", len(pk)),
		peer + " #1 LABS_LOGIN_SUCCESS SESS/CLNT/PROC: UUID=0",
		peer + " #2 LABS_SYSINFO SESS/CLNT/STND: OSYS=Windows_8",
		peer + " 6 bytes left over, not a full packet:",
		"    0000  26 6c 67 72 00 00                                &lgr..",
		peer + fmt.Sprintf(" closed after %d bytes, 1 packet, 2 events", len(pk)+6),
	}
	if strings.Join(got[1:], "\n") != strings.Join(want, "\n") {
		t.Errorf("log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSinkGarbage(t *testing.T) {
	// An impossible length field ("TP/1") loses the framing: hexdump it and everything after.
	peer, log := session(t, []byte("GET / HTTP/1.0\r\n\r\n"), []byte("more"))
	for _, w := range []string{
		peer + " 18 bytes, not a ProtoAries packet: length field 1414541105:\n    0000  47 45 54 20",
		peer + " 4 bytes:\n    0000  6d 6f 72 65 ",
		peer + " closed after 22 bytes, 0 packets, 0 events",
	} {
		if !strings.Contains(log, w) {
			t.Errorf("log lacks %q:\n%s", w, log)
		}
	}
}

func TestSinkOtherKindAndProblems(t *testing.T) {
	other := []byte("&beg\x00\x00\x00\x00\x00\x00\x00\x0fx=1")
	bad := []byte("T=1\nTLM3=@9-abc\nANON=nope\x00")
	pk := make([]byte, HeaderSize)
	binary.BigEndian.PutUint32(pk[0:4], KindLogin)
	binary.BigEndian.PutUint32(pk[4:8], 7)
	binary.BigEndian.PutUint32(pk[8:12], uint32(HeaderSize+len(bad)))
	pk = append(pk, bad...)
	peer, log := session(t, other, pk)
	for _, w := range []string{
		peer + " <- &beg code 0x0, 15 bytes (not a telemetry packet):\n    0000  78 3d 31 ",
		peer + fmt.Sprintf(" <- &lgr, %d bytes: T=1, code 0x7, 0 events", len(pk)),
		peer + " TLM3: cipher length 9, but 3 bytes follow",
		peer + " TLM3: not an event: ",
		peer + " ANON: no '$^' token prefix",
		peer + " the packet's body:\n    0000  54 3d 31 0a",
		peer + " closed after ",
	} {
		if !strings.Contains(log, w) {
			t.Errorf("log lacks %q:\n%s", w, log)
		}
	}
}
