package web

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every web-content path the client requests gets its built-in answer, whatever the DRC version and
// locale, in origin form and as an absolute URL.
func TestBuiltinWebContent(t *testing.T) {
	cases := []struct {
		target, ctype string
		body          []byte
	}{
		{"/battlegrounds_test/live/www/client/1/en-us/dynamic_strings/dynamic_strings.xml", "text/xml", DynamicStrings()},
		{"/battlegrounds_test/live/www/client/1/en-us/dynamic_lore/dynamic_lore.snap", "application/octet-stream", DynamicLoreSnap()},
		{"/battlegrounds_test/live/www/client/1/common/home.html", "text/html", HomePage()},
		{"http://127.0.0.1:18080/battlegrounds_test/live/www/client/7/de-de/dynamic_strings/dynamic_strings.xml?x=1", "text/xml", DynamicStrings()},
		{"/battlegrounds_test/live/www/client/7/common/home.html", "text/html", HomePage()},
	}
	for _, c := range cases {
		resp, log := roundTrip(t, &HTTP{}, "GET "+c.target+" HTTP/1.1\r\nhost: 127.0.0.1:18080\r\n"+
			"user-agent: ProtoHttp 1.3/DS 8.18.11.0 (Windows)\r\naccept: */*\r\n\r\n")
		head := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", c.ctype, len(c.body))
		if resp != head+string(c.body) {
			t.Errorf("%s: response %q...", c.target, resp[:min(len(resp), 120)])
		}
		if want := fmt.Sprintf("[http] -> 200 OK, %d bytes (built-in)\n", len(c.body)); !strings.Contains(log, want) {
			t.Errorf("%s: log lacks %q:\n%s", c.target, want, log)
		}
	}
	// other paths under the web root stay 404
	if resp, _ := roundTrip(t, &HTTP{}, "GET /battlegrounds_test/live/www/client/1/en-us/other.xml HTTP/1.1\r\n\r\n"); !strings.HasPrefix(resp, "HTTP/1.1 404") {
		t.Errorf("unknown path: %q", resp)
	}
}

// A --www file of the same path wins over the built-in answer.
func TestWWWOverridesWebContent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "battlegrounds_test", "live", "www", "client", "1", "en-us", "dynamic_strings", "dynamic_strings.xml")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("<DynamicStrings/>"), 0o644)
	resp, _ := roundTrip(t, &HTTP{WWW: root}, "GET /battlegrounds_test/live/www/client/1/en-us/dynamic_strings/dynamic_strings.xml HTTP/1.1\r\n\r\n")
	if !strings.HasSuffix(resp, "\r\n\r\n<DynamicStrings/>") {
		t.Errorf("response %q", resp)
	}
}

// dgserver --write-www writes every built-in answer where the client asks for it, so the same bytes
// come back from --www.
func TestWriteBuiltins(t *testing.T) {
	root := t.TempDir()
	files, err := WriteBuiltins(root)
	if err != nil || len(files) != len(Builtins) {
		t.Fatalf("WriteBuiltins: %v, %v", files, err)
	}
	h := &HTTP{WWW: root}
	for _, b := range Builtins {
		resp, log := roundTrip(t, h, "GET /"+b.Path+" HTTP/1.1\r\n\r\n")
		if !strings.HasSuffix(resp, "\r\n\r\n"+string(b.Body)) || !strings.Contains(log, " bytes from "+root) {
			t.Errorf("%s: not served from the written file:\n%s", b.Path, log)
		}
		if !strings.HasSuffix("/"+b.Path, b.Suffix) {
			t.Errorf("%s does not end with its suffix %s", b.Path, b.Suffix)
		}
	}
}

// xmlElement is one start element with its depth (root = 1).
type xmlElement struct {
	depth int
	name  string
	attrs map[string]string
	text  string // character data directly inside the element
}

func parseXML(b []byte) ([]xmlElement, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	var out []xmlElement
	var open []int // indexes into out
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			e := xmlElement{depth: len(open) + 1, name: tok.Name.Local, attrs: map[string]string{}}
			for _, a := range tok.Attr {
				e.attrs[a.Name.Local] = a.Value
			}
			out = append(out, e)
			open = append(open, len(out)-1)
		case xml.EndElement:
			open = open[:len(open)-1]
		case xml.CharData:
			if len(open) > 0 {
				out[open[len(open)-1]].text += string(tok)
			}
		}
	}
}

// wcstoul with base 0, as the client parses tableId and instanceId (0x00833D40).
func parseID(s string) (uint32, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 0, 64)
	return uint32(v), err == nil
}

// checkDynamicStrings applies the client's rules (DynamicStrings_OnStartElement 0x00539570): entries
// are DynamicString elements at depth 2 with non-zero tableId and instanceId and one line of text. It
// returns the number of entries and the problems found.
func checkDynamicStrings(b []byte) (int, []string) {
	elems, err := parseXML(b)
	if err != nil {
		return 0, []string{err.Error()}
	}
	n, problems := 0, []string(nil)
	for _, e := range elems {
		if e.depth == 1 && e.name != "DynamicStrings" {
			problems = append(problems, fmt.Sprintf("root %q", e.name))
		}
		if e.depth == 2 && strings.EqualFold(e.name, "DynamicString") {
			table, ok1 := parseID(e.attrs["tableId"])
			inst, ok2 := parseID(e.attrs["instanceId"])
			if !ok1 || !ok2 || table == 0 || inst == 0 {
				problems = append(problems, fmt.Sprintf("entry %v: tableId and instanceId must be non-zero numbers", e.attrs))
			}
			if strings.ContainsAny(e.text, "\r\n") {
				problems = append(problems, fmt.Sprintf("entry %v: text spans lines (the client keeps only the last piece)", e.attrs))
			}
			n++
		}
	}
	return n, problems
}

// checkDynamicLore applies DynamicLore_OnStartElement's rules (0x0053D1C0): DynamicLore > Votes > Vote,
// every Vote with five non-empty attributes.
func checkDynamicLore(b []byte) (int, []string) {
	elems, err := parseXML(b)
	if err != nil {
		return 0, []string{err.Error()}
	}
	n, problems := 0, []string(nil)
	path := map[int]string{}
	for _, e := range elems {
		path[e.depth] = strings.ToLower(e.name)
		if e.depth == 1 && path[1] != "dynamiclore" {
			problems = append(problems, fmt.Sprintf("root %q", e.name))
		}
		if e.depth == 3 && path[2] == "votes" && path[3] == "vote" {
			for _, a := range []string{"name", "cardtexta", "cardtextb", "cardimga", "cardimgb"} {
				if e.attrs[a] == "" {
					problems = append(problems, fmt.Sprintf("vote %v lacks %s (\"incomplete dynamic lore vote entry\")", e.attrs, a))
				}
			}
			n++
		}
	}
	return n, problems
}

func TestDynamicStringsXML(t *testing.T) {
	if n, problems := checkDynamicStrings(DynamicStrings()); n != 0 || problems != nil {
		t.Errorf("%d entries (ours has none), problems %v", n, problems)
	}
	// the checker itself: a valid entry passes, broken ones fail
	ok := `<DynamicStrings><DynamicString tableId="0x41bbc437" instanceId="16">Hello</DynamicString></DynamicStrings>`
	if n, problems := checkDynamicStrings([]byte(ok)); n != 1 || problems != nil {
		t.Errorf("valid entry: %d, %v", n, problems)
	}
	for _, bad := range []string{
		`<DynamicStrings><DynamicString tableId="0" instanceId="16">x</DynamicString></DynamicStrings>`,
		`<DynamicStrings><DynamicString tableId="0x1" instanceId="16">a` + "\n" + `b</DynamicString></DynamicStrings>`,
		`<DynamicStrings><DynamicString>`,
	} {
		if _, problems := checkDynamicStrings([]byte(bad)); problems == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if !bytes.HasPrefix(DynamicStrings(), []byte(`<?xml version="1.0" encoding="utf-8"?>`)) {
		t.Error("no XML declaration")
	}
}

func TestDynamicLoreXML(t *testing.T) {
	if n, problems := checkDynamicLore(DynamicLoreXML()); n != 0 || problems != nil {
		t.Errorf("%d votes (ours has none), problems %v", n, problems)
	}
	if _, problems := checkDynamicLore([]byte(`<DynamicLore><Votes><Vote name="a" cardtexta="b"/></Votes></DynamicLore>`)); problems == nil {
		t.Error("an incomplete vote passed")
	}
	full := `<DynamicLore><Votes><Vote name="v" cardtexta="a" cardtextb="b" cardimga="c" cardimgb="d"/></Votes></DynamicLore>`
	if n, problems := checkDynamicLore([]byte(full)); n != 1 || problems != nil {
		t.Errorf("complete vote: %d, %v", n, problems)
	}
}

// The home page defines the hooks the client calls, uses only the native Client object, polls the relative
// home.json, loads nothing from another host (only the client's own game:// pictures) and keeps to what the
// client's web view runs (no bind, no flexbox, no unprefixed gradients).
func TestHomePage(t *testing.T) {
	page := string(HomePage())
	for _, want := range []string{
		"function onLivingLoreVoteUpdate()", "function onRegionKillswitchEuUpdate()", "function onWebVersionUpdate()",
		`clientCall("requestMainMenuContent", content)`, "<!DOCTYPE html>", `"home.json?n="`, "JSON.parse(",
		"show(playContent)", "playContent = 5", "playContent = 9", "return show(12);", "return show(10);",
		"return show(11);", "return show(0);", "return show(2);", "not run by Waystone Games or Electronic Arts",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("home.html lacks %q", want)
		}
	}
	for _, bad := range []string{"http://", "https://", "src=", "<link", "@import", "@font-face", ".bind(",
		"display: flex", "display:flex", "linear-gradient(to ", "innerHTML", "const ", "let ", "=>"} {
		if strings.Contains(page, bad) {
			t.Errorf("home.html contains %q (self-contained, ES5, text never as HTML)", bad)
		}
	}
	for _, m := range regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(page, -1) {
		if !strings.HasPrefix(m[1], "game://UI/XHTML/labs/images/") {
			t.Errorf("home.html: url(%s): only the client's own game:// pictures", m[1])
		}
	}
	for i, c := range page {
		if c > 0x7E || (c < 0x20 && c != '\n') {
			t.Fatalf("home.html: character %U at %d (keep it ASCII, LF)", c, i)
		}
	}
}
