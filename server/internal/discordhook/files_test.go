package discordhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// filePart is one part of a multipart body.
type filePart struct {
	name, filename, contentType string
	data                        []byte
}

// parseMultipart splits a multipart/form-data body into its parts.
func parseMultipart(t *testing.T, contentType string, body []byte) []filePart {
	t.Helper()
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
		t.Fatalf("content type %q: %v", contentType, err)
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []filePart
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return parts
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(p)
		parts = append(parts, filePart{p.FormName(), p.FileName(), p.Header.Get("Content-Type"), data})
	}
}

// imagePost is a result post in the image style: an embed showing its uploaded picture.
func imagePost(png []byte) Message {
	e := Embed{Title: "PvP match: Team 1 wins", Description: "Team 2's Guardian fell after 31:05", Color: 0x3498DB,
		Image: &Image{URL: AttachmentURL("scoreboard.png")}, Footer: &Footer{Text: "Game 7"}}
	e.At(time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	return Message{Embeds: []Embed{e}, Files: []File{{Name: "scoreboard.png", ContentType: "image/png", Data: png}}}
}

// EncodeBody: a message without files is Encode's JSON as application/json; with a file a multipart body of
// payload_json (the JSON with attachments, the embed's image, allowed_mentions) and files[0] (the bytes).
func TestEncodeBodyFiles(t *testing.T) {
	plain := Message{Content: "hello"}
	js, _ := Encode(plain)
	body, ct, err := EncodeBody(plain)
	if err != nil || ct != "application/json" || !bytes.Equal(body, js) {
		t.Fatalf("plain: %q %s %v", body, ct, err)
	}
	png := []byte("\x89PNG\r\n\x1a\n--not-really--\x00\xff")
	body, ct, err = EncodeBody(imagePost(png))
	if err != nil {
		t.Fatal(err)
	}
	parts := parseMultipart(t, ct, body)
	if len(parts) != 2 {
		t.Fatalf("%d parts", len(parts))
	}
	if p := parts[0]; p.name != "payload_json" || p.filename != "" || p.contentType != "application/json" {
		t.Errorf("part 0 %+v", p)
	}
	want := `{"content":"","embeds":[{"title":"PvP match: Team 1 wins","description":"Team 2's Guardian fell after 31:05",` +
		`"color":3447003,"image":{"url":"attachment://scoreboard.png"},"footer":{"text":"Game 7"},` +
		`"timestamp":"2026-11-17T12:00:00Z"}],"allowed_mentions":{"parse":[]},` +
		`"attachments":[{"id":0,"filename":"scoreboard.png"}]}`
	if got := string(parts[0].data); got != want {
		t.Errorf("payload_json\n got %s\nwant %s", got, want)
	}
	if p := parts[1]; p.name != "files[0]" || p.filename != "scoreboard.png" || p.contentType != "image/png" ||
		!bytes.Equal(p.data, png) {
		t.Errorf("part 1 %+v", p)
	}
	// Encode alone (the JSON) names the attachment too
	if js, _ := Encode(imagePost(png)); string(js) != want {
		t.Errorf("Encode %s", js)
	}
	// a file name with a quote stays one parameter
	m := imagePost(png)
	m.Files[0].Name = `a"b.png`
	m.Files = append(m.Files, File{Name: "x.bin", Data: []byte{1}})
	body, ct, _ = EncodeBody(m)
	parts = parseMultipart(t, ct, body)
	if len(parts) != 3 || parts[1].filename != `a"b.png` || parts[2].name != "files[1]" ||
		parts[2].contentType != "application/octet-stream" {
		t.Errorf("parts %+v", parts)
	}
}

// A post with a file is sent as multipart, the same bytes on every retry (a 502, a 429, then 200); an edit never
// uploads (JSON, files dropped); a post without files stays JSON.
func TestPostFileRetries(t *testing.T) {
	rc := newReceiver(t)
	rc.answer = func(n int, r request, w http.ResponseWriter) {
		switch n {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"retry_after":0.5}`)
		default:
			fmt.Fprintf(w, `{"id":"%d"}`, 1000+n)
		}
	}
	h, _ := New(rc.url(), Options{AllowLocal: true, sleep: (&sleeps{}).sleep})
	png := bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 0, 0xff}, 5000)
	ref := h.Post(imagePost(png))
	if id, err := wait(t, ref); err != nil || id != "1003" {
		t.Fatalf("post: %q %v", id, err)
	}
	h.Edit(ref, imagePost(png))
	if id, err := wait(t, h.Post(Message{Content: "plain"})); err != nil || id != "1005" {
		t.Fatalf("plain post: %q %v", id, err)
	}
	h.Close(time.Second)
	reqs := rc.requests()
	if len(reqs) != 5 {
		t.Fatalf("%d requests", len(reqs))
	}
	for i := 0; i < 3; i++ {
		r := reqs[i]
		if r.Method != http.MethodPost || r.Query != "wait=true" || r.ContentType != reqs[0].ContentType ||
			r.Raw != reqs[0].Raw || !strings.HasPrefix(r.ContentType, "multipart/form-data; boundary=") {
			t.Errorf("try %d: %s %s %s (%d bytes)", i+1, r.Method, r.Query, r.ContentType, len(r.Raw))
		}
	}
	parts := parseMultipart(t, reqs[2].ContentType, []byte(reqs[2].Raw))
	if len(parts) != 2 || !bytes.Equal(parts[1].data, png) {
		t.Errorf("the file didn't arrive whole")
	}
	edit := reqs[3]
	if edit.Method != http.MethodPatch || edit.ContentType != "application/json" || strings.Contains(edit.Raw, "attachments") {
		t.Errorf("edit: %s %s %s", edit.Method, edit.ContentType, edit.Raw)
	}
	var e struct {
		Embeds []struct {
			Image struct{ URL string }
		}
	}
	if json.Unmarshal([]byte(edit.Raw), &e) != nil || len(e.Embeds) != 1 {
		t.Errorf("edit body %s", edit.Raw)
	}
	if p := reqs[4]; p.ContentType != "application/json" || p.Body["content"] != "plain" {
		t.Errorf("plain post: %s %s", p.ContentType, p.Raw)
	}
}
