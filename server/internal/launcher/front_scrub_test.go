package launcher

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
	"dawngate/server/internal/fire"
	"dawngate/server/internal/tdf"
	"dawngate/server/internal/tunnel"
	"dawngate/server/internal/web"
)

func frameOf(t *testing.T, fr fire.Frame) ([]byte, fire.Frame) {
	t.Helper()
	raw, err := fire.Encode(fr)
	if err != nil {
		t.Fatal(err)
	}
	return raw, fr
}

func hnetBody(ip int64) []byte {
	body := tdf.NewWriter()
	body.Struct("EXIP", func(w *tdf.Writer) { w.Int("IP", ip).Int("PORT", 10042) })
	body.Struct("INIP", func(w *tdf.Writer) { w.Int("IP", ip).Int("PORT", 10042) })
	return tdf.NewWriter().Int("GID", 7).List("HNET", tdf.TypeStruct, []tdf.UnionItem{{Member: blaze.NetworkAddressIpPair, Body: body}}).Bytes()
}

// A server's frames reach the game byte for byte, except for what names a file or an address the game would use by itself.
func TestScrubFromServer(t *testing.T) {
	tunnelIP := net.ParseIP("127.0.0.1")
	const loop = int64(0x7F000001)

	honest := []fire.Frame{
		{Component: blaze.ComponentGameManager, Command: blaze.NotifyGameSetupID, Type: fire.Notification, Payload: hnetBody(loop)},
		{Component: blaze.ComponentBGWeb, Command: blaze.CmdBGWebGetCurrentVers, Type: fire.Reply, Payload: tdf.NewWriter().String("DRC", "1").Bytes()},
		{Component: blaze.ComponentUtil, Command: blaze.CmdUtilGetTelemetryServer, Type: fire.Reply, Payload: tdf.NewWriter().String("ADRS", "127.0.0.1").Int("PORT", 9988).Bytes()},
		{Component: blaze.ComponentUtil, Command: blaze.CmdUtilPostAuth, Type: fire.Reply, Payload: tdf.NewWriter().Struct("TELE", func(w *tdf.Writer) { w.String("ADRS", "") }).Bytes()},
		// a message of another kind is never looked into, whatever it holds
		{Component: blaze.ComponentMessaging, Command: blaze.NotifyMessageID, Type: fire.Notification, Payload: tdf.NewWriter().String("ADRS", "203.0.113.9").Bytes()},
	}
	for _, fr := range honest {
		raw, fr := frameOf(t, fr)
		if out, why := scrubFromServer(raw, fr, tunnelIP); why != "" || !bytes.Equal(out, raw) {
			t.Errorf("an honest frame %#x/%#x changed: %q", fr.Component, fr.Command, why)
		}
	}

	// DRC: the game makes it a file name, Dynamic/dl/dl.<DRC>.snap.tmp
	for _, drc := range []string{`1\..\..\..\..\pwned`, "../x", "a/b", "a b", "", strings.Repeat("a", 33), "1.snap"} {
		raw, fr := frameOf(t, fire.Frame{Component: blaze.ComponentBGWeb, Command: blaze.CmdBGWebGetCurrentVers, Type: fire.Reply, ID: 7,
			Payload: tdf.NewWriter().String("DRC", drc).Bytes()})
		out, why := scrubFromServer(raw, fr, tunnelIP)
		if why == "" {
			t.Errorf("DRC %q reached the game", drc)
			continue
		}
		got, err := fire.NewReader(bytes.NewReader(out)).Next()
		if err != nil {
			t.Fatal(err)
		}
		st, _ := tdf.Decode(got.Payload)
		if v, _ := st.Str("DRC"); v != fallbackDRC || got.ID != 7 || got.Type != fire.Reply {
			t.Errorf("DRC %q became %q (id %d)", drc, v, got.ID)
		}
	}

	// the match's address: the game dials it itself, outside the tunnel if it isn't the tunnel's
	raw, fr := frameOf(t, fire.Frame{Component: blaze.ComponentGameManager, Command: blaze.NotifyGameSetupID, Type: fire.Notification,
		Payload: hnetBody(0xCB007109)})
	out, why := scrubFromServer(raw, fr, tunnelIP)
	if !strings.Contains(why, "203.0.113.9") {
		t.Fatalf("the outside GMS address: %q", why)
	}
	got, err := fire.NewReader(bytes.NewReader(out)).Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Payload, hnetBody(loop)) {
		t.Errorf("the corrected frame differs from an honest one:\n%x\n%x", got.Payload, hnetBody(loop))
	}

	// the telemetry server's address, in postAuth and on its own
	for _, fr := range []fire.Frame{
		{Component: blaze.ComponentUtil, Command: blaze.CmdUtilPostAuth, Type: fire.Reply, Payload: tdf.NewWriter().Struct("TELE", func(w *tdf.Writer) { w.String("ADRS", "evil.example").Int("PORT", 9988) }).Bytes()},
		{Component: blaze.ComponentUtil, Command: blaze.CmdUtilGetTelemetryServer, Type: fire.Reply, Payload: tdf.NewWriter().String("ADRS", "203.0.113.9").Bytes()},
	} {
		raw, fr := frameOf(t, fr)
		out, why := scrubFromServer(raw, fr, tunnelIP)
		if why == "" || bytes.Contains(out, []byte("evil.example")) || bytes.Contains(out, []byte("203.0.113.9")) {
			t.Errorf("the telemetry address stayed: %q %q", why, out)
		}
	}
}

// A server never learns the player's MAC address: not from the replayed preAuth, nor from telemetry's login token.
func TestPlayerIdentifiersStayHere(t *testing.T) {
	const mac = "aa:bb:cc:dd:ee:ff"
	req := tdf.NewWriter().Struct("CINF", func(w *tdf.Writer) { w.String("CLNT", "Dawngate").String("HWID", "hw1").String("MAC", mac) }).Bytes()
	raw, err := fire.Encode(fire.Frame{Component: blaze.ComponentUtil, Command: blaze.CmdUtilPreAuth, Type: fire.Request, ID: 3, Payload: req})
	if err != nil {
		t.Fatal(err)
	}
	out := scrubPreAuth(raw)
	if bytes.Contains(out, []byte(mac)) || bytes.Contains(out, []byte("hw1")) {
		t.Errorf("the replayed preAuth holds the identifiers: %q", out)
	}
	got, err := fire.NewReader(bytes.NewReader(out)).Next()
	if err != nil {
		t.Fatal(err)
	}
	st, _ := tdf.Decode(got.Payload)
	info, _ := st.Sub("CINF")
	if v, _ := info.Str("CLNT"); v != "Dawngate" || got.ID != 3 {
		t.Errorf("the rest of preAuth changed: %q, id %d", v, got.ID)
	}
	if !telemetryToken([]byte("T=1\nANON=$^abc\n")) || telemetryToken([]byte("T=first")) {
		t.Error("telemetryToken")
	}
}

// A server's answer can't keep Start-Client reading a head for ever; a body may be as long as it is.
func TestHeadLimit(t *testing.T) {
	endless := strings.NewReader("HTTP/1.1 200 OK\r\n" + strings.Repeat("X-A: "+strings.Repeat("a", 1000)+"\r\n", 1000))
	head := &headLimit{r: endless, left: maxResponseHead}
	if _, err := http.ReadResponse(bufio.NewReader(head), nil); !errors.Is(err, errHeadTooLong) {
		t.Errorf("endless headers: %v", err)
	}
	body := strings.Repeat("b", 2*maxResponseHead)
	head = &headLimit{r: strings.NewReader("HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body), left: maxResponseHead}
	r, err := http.ReadResponse(bufio.NewReader(head), nil)
	if err != nil {
		t.Fatal(err)
	}
	head.off = true
	if b, err := io.ReadAll(r.Body); err != nil || len(b) != len(body) {
		t.Errorf("the body: %d bytes, %v", len(b), err)
	}
}

// The game gets its web content from Start-Client, whatever the server would answer, and only the pages' JSON from the server.
func TestGameWebContentIsStartClients(t *testing.T) {
	f, logA := guardFront(t, func(*Front) *gameGuard { return nil })
	_ = f
	host := "http://" + frontLocal + ":" + strconv.Itoa(DefaultFrontPorts.HTTP)
	const root = "/battlegrounds_test/live/www/client/1"
	get := func(path string) (int, []byte) {
		t.Helper()
		c := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
		r, err := c.Get(host + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	for path, want := range map[string][]byte{root + "/common/home.html": web.HomePage(), root + "/common/history.html": web.HistoryPage(),
		root + "/en-us/dynamic_lore/dynamic_lore.snap": web.DynamicLoreSnap(), root + "/en-us/dynamic_strings/dynamic_strings.xml": web.DynamicStrings()} {
		if code, b := get(path); code != http.StatusOK || !bytes.Equal(b, want) {
			t.Errorf("%s: %d, %d bytes", path, code, len(b))
		}
	}
	for _, path := range []string{root + "/common/evil.js", root + "/common/font.ttf", "/anything"} {
		if code, _ := get(path); code != http.StatusNotFound {
			t.Errorf("%s: %d, want the front's 404", path, code)
		}
	}
	if text := readLogs(t, logA); strings.Contains(text, "home.html") || strings.Contains(text, "evil.js") || strings.Contains(text, "dynamic_lore") {
		t.Errorf("the web content's requests reached the server:\n%s", text)
	}
	get(root + "/common/home.json")
	waitLog(t, logA, "home.json", 0)
}

// A Join from Discord names a key, and anyone can publish one: it pins nothing, and it stays away from the player's own network.
func TestDiscordJoinConfirmsItsKey(t *testing.T) {
	f, _ := guardFront(t, func(*Front) *gameGuard { return nil })
	f.DiscordJoin = true
	var evil, real tunnel.Pin
	for i := range evil {
		evil[i], real[i] = 0xEE, 0x11
	}
	const code = "AbCdEfGhIjKlMnOpQrSt_-"
	const addr = "203.0.113.76"
	tag, err := f.placeJoin(joinSecret{Address: addr, Port: 9000, Pin: evil, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Bookmarks.Find(tag)
	if _, ok := f.trustedPin(b); ok {
		t.Error("a Join pinned its own key")
	}
	if state, _ := f.pinState(b, real); state != "new" {
		t.Errorf("the real server's key is %q after a Join with another key: it must still be new", state)
	}
	for _, a := range []string{"192.168.1.1", "10.0.0.5", "127.0.0.1", "169.254.1.1"} {
		if publicIP(net.ParseIP(a)) {
			t.Errorf("%s counts as public", a)
		}
		f.join.pending = nil
		f.DiscordJoinSecret(joinSecret{Address: a, Port: 22, Pin: evil, Code: code}.String())
		if f.join.pending != nil {
			t.Errorf("a Join to %s was taken", a)
		}
	}
	if !publicIP(net.ParseIP(addr)) {
		t.Error("a public address doesn't count as public")
	}
}
