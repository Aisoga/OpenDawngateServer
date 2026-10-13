package tunnel

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"testing"
	"time"
)

func testKeys(seed byte) Keys {
	var k Keys
	for i := range k.Up {
		k.Up[i], k.Down[i] = seed+byte(i), seed^byte(3*i+1)
	}
	return k
}

func pair(t *testing.T, k Keys, session uint64) (*Sealer, *Opener) {
	t.Helper()
	s, err := NewSealer(k.Up, session)
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewOpener(k.Up, session)
	if err != nil {
		t.Fatal(err)
	}
	return s, o
}

func TestChannelRoundTrip(t *testing.T) {
	s, o := pair(t, testKeys(1), 0x1122334455667788)
	in := InnerFor(KindData, 10042, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 3659})
	payload := []byte("RakNet datagram")
	pkt, err := s.Seal(in, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) != len(payload)+Overhead {
		t.Errorf("datagram %d bytes, want payload %d + overhead %d", len(pkt), len(payload), Overhead)
	}
	if bytes.Contains(pkt, payload) {
		t.Error("the payload is visible in the datagram")
	}
	if id, ok := SessionOf(pkt); !ok || id != 0x1122334455667788 {
		t.Errorf("SessionOf = %x, %v", id, ok)
	}
	got, p, err := o.Open(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if got != in || !bytes.Equal(p, payload) {
		t.Errorf("opened %+v %q, want %+v %q", got, p, in, payload)
	}
	if c := got.Client(); c.String() != "127.0.0.1:3659" {
		t.Errorf("client address %s", c)
	}
	// an empty keepalive
	pkt, _ = s.Seal(Inner{Kind: KindKeepalive}, nil)
	if got, p, err := o.Open(pkt); err != nil || got.Kind != KindKeepalive || len(p) != 0 {
		t.Errorf("keepalive: %+v %q %v", got, p, err)
	}
}

func TestChannelWrongKeyAndSession(t *testing.T) {
	s, _ := pair(t, testKeys(1), 7)
	_, other := pair(t, testKeys(2), 7)
	pkt, _ := s.Seal(Inner{Port: 17502}, []byte("probe"))
	if _, _, err := other.Open(pkt); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong key: %v, want ErrAuth", err)
	}
	_, otherSession := pair(t, testKeys(1), 8)
	if _, _, err := otherSession.Open(pkt); !errors.Is(err, ErrSession) {
		t.Errorf("other session: %v, want ErrSession", err)
	}
	// the direction keys differ: a datagram sealed for one direction doesn't open in the other
	down, _ := NewOpener(testKeys(1).Down, 7)
	if _, _, err := down.Open(pkt); !errors.Is(err, ErrAuth) {
		t.Errorf("other direction's key: %v, want ErrAuth", err)
	}
}

func TestChannelTamper(t *testing.T) {
	s, o := pair(t, testKeys(3), 9)
	for _, at := range []int{0, 5, 12, HeaderLen + 2, HeaderLen + InnerLen + 1, -1} {
		pkt, _ := s.Seal(Inner{Port: 10042}, []byte("move to 10,20"))
		i := at
		if i < 0 {
			i = len(pkt) - 1 // the tag
		}
		pkt[i] ^= 0x40
		_, _, err := o.Open(pkt)
		if err == nil {
			t.Errorf("byte %d changed: opened", i)
		}
		if i >= HeaderLen && !errors.Is(err, ErrAuth) {
			t.Errorf("byte %d changed: %v, want ErrAuth", i, err)
		}
	}
	for _, short := range [][]byte{nil, {1}, make([]byte, HeaderLen+InnerLen+TagLen-1)} {
		if _, _, err := o.Open(short); !errors.Is(err, ErrShort) {
			t.Errorf("%d bytes: %v, want ErrShort", len(short), err)
		}
	}
}

func TestChannelReplay(t *testing.T) {
	s, o := pair(t, testKeys(4), 1)
	pkt, _ := s.Seal(Inner{Port: 10042}, []byte("cast Q"))
	if _, _, err := o.Open(pkt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := o.Open(pkt); !errors.Is(err, ErrReplay) {
		t.Errorf("second copy: %v, want ErrReplay", err)
	}
}

func TestChannelReorderingAndLoss(t *testing.T) {
	s, o := pair(t, testKeys(5), 2)
	const n = 3000
	pkts := make([][]byte, n)
	for i := range pkts {
		pkts[i], _ = s.Seal(Inner{Port: 10042}, []byte{byte(i), byte(i >> 8)})
	}
	r := mrand.New(mrand.NewPCG(1, 2))
	// in blocks of 200: a third lost, the rest shuffled; everything within the window arrives once
	opened := 0
	for b := 0; b < n; b += 200 {
		block := pkts[b:min(b+200, n)]
		order := r.Perm(len(block))
		for _, i := range order {
			if (b+i)%3 == 0 {
				continue // lost
			}
			_, p, err := o.Open(block[i])
			if err != nil {
				t.Fatalf("datagram %d: %v", b+i, err)
			}
			if int(p[0])|int(p[1])<<8 != b+i {
				t.Fatalf("datagram %d opened as %d", b+i, int(p[0])|int(p[1])<<8)
			}
			opened++
		}
	}
	if opened != n-(n+2)/3 {
		t.Errorf("opened %d", opened)
	}
	// a lost one that comes late but within the window still arrives; one older than the window doesn't
	if _, _, err := o.Open(pkts[n-3]); err != nil { // (n-3) % 3 == 0: lost above
		t.Errorf("late datagram within the window: %v", err)
	}
	if _, _, err := o.Open(pkts[n-1-WindowSize]); !errors.Is(err, ErrReplay) {
		t.Errorf("datagram %d behind the newest: %v, want ErrReplay", WindowSize, err)
	}
	if _, _, err := o.Open(pkts[n-WindowSize]); err != nil && !errors.Is(err, ErrReplay) {
		t.Errorf("datagram at the window's edge: %v", err)
	}
	// every one already seen is a replay
	for i := n - 900; i < n; i++ {
		if i%3 == 0 && i != n-3 {
			continue // lost, never seen
		}
		if _, _, err := o.Open(pkts[i]); !errors.Is(err, ErrReplay) {
			t.Fatalf("datagram %d again: %v, want ErrReplay", i, err)
		}
	}
}

func TestChannelWindowJump(t *testing.T) {
	var w window
	for _, n := range []uint64{0, 1, 5000, 4999, 3977, 3976} {
		ok := w.update(n)
		want := n != 3976 // 5000 - 3976 = 1024: outside the window
		if ok != want {
			t.Errorf("update(%d) = %v, want %v", n, ok, want)
		}
	}
	for _, n := range []uint64{0, 1, 4999, 5000, 3977} {
		if w.check(n) {
			t.Errorf("check(%d) after it was seen: true", n)
		}
	}
	if !w.check(4000) || !w.check(5001) {
		t.Error("unseen counters refused")
	}
}

func TestChannelTooBig(t *testing.T) {
	s, _ := pair(t, testKeys(6), 3)
	if _, err := s.Seal(Inner{}, make([]byte, MaxPayload)); err != nil {
		t.Errorf("MaxPayload: %v", err)
	}
	if _, err := s.Seal(Inner{}, make([]byte, MaxPayload+1)); !errors.Is(err, ErrTooBig) {
		t.Errorf("MaxPayload+1: %v, want ErrTooBig", err)
	}
	if MaxPayload+Overhead+28 > 1500 {
		t.Errorf("a full datagram is %d bytes on the wire, above 1500", MaxPayload+Overhead+28)
	}
}

// selfSigned is a throwaway TLS key pair for tests.
func selfSigned(t testing.TB) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestDeriveKeys: both ends of one TLS 1.3 connection export the same keys; another session id or another
// connection gives others; TLS 1.2 is refused.
func TestDeriveKeys(t *testing.T) {
	cert := selfSigned(t)
	conns := func(maxVersion uint16) (tls.ConnectionState, tls.ConnectionState) {
		a, b := net.Pipe()
		srv := tls.Server(a, &tls.Config{Certificates: []tls.Certificate{cert}, MaxVersion: maxVersion})
		cli := tls.Client(b, &tls.Config{InsecureSkipVerify: true})
		done := make(chan error, 1)
		go func() { done <- srv.Handshake() }()
		if err := cli.Handshake(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		return srv.ConnectionState(), cli.ConnectionState()
	}
	s1, c1 := conns(tls.VersionTLS13)
	ks, err := DeriveKeys(s1, 42)
	if err != nil {
		t.Fatal(err)
	}
	kc, _ := DeriveKeys(c1, 42)
	if ks != kc {
		t.Error("the two ends derived different keys")
	}
	if ks.Up == ks.Down {
		t.Error("up and down keys are equal")
	}
	if other, _ := DeriveKeys(c1, 43); other == kc {
		t.Error("another session id gave the same keys")
	}
	s2, _ := conns(tls.VersionTLS13)
	if other, _ := DeriveKeys(s2, 42); other == ks {
		t.Error("another connection gave the same keys")
	}
	s3, _ := conns(tls.VersionTLS12)
	if _, err := DeriveKeys(s3, 42); err == nil {
		t.Error("TLS 1.2: keys derived")
	}
}

func TestPin(t *testing.T) {
	dir := t.TempDir()
	id, created, err := LoadOrCreateIdentity(dir)
	if err != nil || !created {
		t.Fatalf("created %v, %v", created, err)
	}
	again, created, err := LoadOrCreateIdentity(dir)
	if err != nil || created || again.Pin != id.Pin {
		t.Fatalf("second load: created %v, same pin %v, %v", created, again.Pin == id.Pin, err)
	}
	if p, err := PinFromCertFile(dir + `/` + CertFile); err != nil || p != id.Pin {
		t.Errorf("PinFromCertFile: %v %v", p == id.Pin, err)
	}
	if p, err := ParsePin(id.Pin.String()); err != nil || p != id.Pin {
		t.Errorf("ParsePin(String) = %v, %v", p == id.Pin, err)
	}
	if len(id.Pin.Fingerprint()) != 16 {
		t.Errorf("fingerprint %q", id.Pin.Fingerprint())
	}
	if _, err := ParsePin("12ab"); err == nil {
		t.Error("a short pin parsed")
	}
	other := t.TempDir()
	if err := WriteIdentity(other); err != nil {
		t.Fatal(err)
	}
	if p, _ := PinFromCertFile(other + `/` + CertFile); p == id.Pin {
		t.Error("two identities with one pin")
	}
}
