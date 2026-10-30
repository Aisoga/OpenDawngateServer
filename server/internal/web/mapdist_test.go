package web

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/logging"
)

// fakeMaps hands out one package, "ring"; block holds each write until it is closed.
type fakeMaps struct {
	block chan struct{}
}

func (fakeMaps) Manifest() []byte {
	return []byte("ring CM_Ring 11 " + strings.Repeat("ab", 32) + "\n")
}

func (f fakeMaps) Package(tag string) (int64, func(io.Writer) error, bool) {
	if tag != "ring" {
		return 0, nil, false
	}
	return 11, func(w io.Writer) error {
		if f.block != nil {
			<-f.block
		}
		_, err := w.Write([]byte("DGMP-bundle"))
		return err
	}, true
}

// mapGet sends one request to h over a pipe and returns the response and its body.
func mapGet(t *testing.T, h *HTTP, method, path string) (*http.Response, string) {
	t.Helper()
	c, s := net.Pipe()
	go h.ServeConn(s)
	c.SetDeadline(time.Now().Add(5 * time.Second))
	go c.Write([]byte(method + " " + path + " HTTP/1.1\r\nHost: dawngate\r\n\r\n"))
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	c.Close()
	return resp, string(b)
}

// TestMapDist: the manifest, a package with its length, an unknown tag 404, HEAD without a body, POST refused, and the
// fifth download at once busy (503); without Maps the paths are 404.
func TestMapDist(t *testing.T) {
	var log bytes.Buffer
	fm := fakeMaps{}
	h := &HTTP{Log: logging.New(&log), Maps: fm}
	if r, b := mapGet(t, h, "GET", "/"+MapPackagesPath); r.StatusCode != 200 || b != string(fm.Manifest()) {
		t.Errorf("the manifest: %d %q", r.StatusCode, b)
	}
	if r, b := mapGet(t, h, "GET", "/"+MapPackagePrefix+"ring"); r.StatusCode != 200 || b != "DGMP-bundle" || r.ContentLength != 11 ||
		r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("the package: %d %q %d", r.StatusCode, b, r.ContentLength)
	}
	if !strings.Contains(log.String(), "-> 200 OK, sending the map package (11 bytes)") {
		t.Errorf("the log: %s", log.String())
	}
	if r, _ := mapGet(t, h, "GET", "/"+MapPackagePrefix+"../ring"); r.StatusCode != 404 {
		t.Errorf("an odd tag: %d", r.StatusCode)
	}
	if r, b := mapGet(t, h, "HEAD", "/"+MapPackagePrefix+"ring"); r.StatusCode != 200 || b != "" {
		t.Errorf("HEAD: %d %q", r.StatusCode, b)
	}
	if r, _ := mapGet(t, h, "POST", "/"+MapPackagesPath); r.StatusCode != 405 {
		t.Errorf("POST: %d", r.StatusCode)
	}

	busy := fakeMaps{block: make(chan struct{})}
	hb := &HTTP{Log: logging.New(io.Discard), Maps: busy}
	var held []net.Conn
	for i := 0; i < MapDownloads; i++ {
		c, s := net.Pipe()
		go hb.ServeConn(s)
		c.Write([]byte("GET /" + MapPackagePrefix + "ring HTTP/1.1\r\n\r\n"))
		br := bufio.NewReader(c)
		if _, err := http.ReadResponse(br, nil); err != nil { // the head is sent, the body waits
			t.Fatal(err)
		}
		held = append(held, c)
	}
	if r, b := mapGet(t, hb, "GET", "/"+MapPackagePrefix+"ring"); r.StatusCode != 503 || !strings.Contains(b, "Busy") {
		t.Errorf("the fifth: %d %q", r.StatusCode, b)
	}
	close(busy.block)
	for _, c := range held {
		io.Copy(io.Discard, c)
		c.Close()
	}

	none := &HTTP{Log: logging.New(io.Discard)}
	if r, _ := mapGet(t, none, "GET", "/"+MapPackagesPath); r.StatusCode != 404 {
		t.Errorf("without Maps: %d", r.StatusCode)
	}
}
