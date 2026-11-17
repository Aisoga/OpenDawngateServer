package mapedit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dawngate/server/internal/rules"
)

// RULES6: the editor's Suggested lobby setup (web/rules.js LOBBY) offers the lobby's catalog: every category with its
// options' ids and names in the server's order and nothing else. The page is the editor's (its own repository): the
// test is skipped where it isn't beside the server.
func TestLobbyCatalogInPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "mapedit", "web", "rules.js"))
	if err != nil {
		t.Skip("no editor page here:", err)
	}
	src := string(b)
	start := strings.Index(src, "const LOBBY = [")
	end := strings.Index(src[max(start, 0):], "\n  ];")
	if start < 0 || end < 0 {
		t.Fatal("rules.js has no LOBBY catalog")
	}
	page := src[start : start+end]
	var want, got []string
	for _, c := range rules.Categories() {
		var opts []string
		for _, o := range c.Options {
			opts = append(opts, "['"+o.ID+"', '"+o.Name+"']")
		}
		want = append(want, "{ id: '"+c.ID+"', name: '"+c.Name+"', def: '"+c.Default+"', opts: ["+strings.Join(opts, ", ")+"] }")
	}
	for _, m := range regexp.MustCompile(`(?s)\{ id: .*?\]\] \}`).FindAllString(page, -1) {
		got = append(got, regexp.MustCompile(`\s*\n\s*`).ReplaceAllString(m, " "))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rules.js LOBBY\n%s\nthe catalog\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, p := range rules.Presets() {
		if !strings.Contains(src, strings.ToLower(p.ID)+": {") {
			t.Errorf("rules.js LOBBY_PRESETS lacks %s", p.ID)
		}
	}
}
