package setup

import (
	"bytes"
	"os"
	"testing"

	"dawngate/server/internal/content"
)

// A content file an older release wrote (here: one that lacks fields this build writes, as a file from before the
// shapers' pictures does) is stale: a check reports it, and the next start's setup builds it again from the game files,
// byte for byte what this build makes (the draft board's pictures reach a server that only updated its release).
func TestContentFromAnOlderRelease(t *testing.T) {
	s, _ := newSetup(t)
	if err := s.Server(dedicated); err != nil {
		t.Fatalf("Server: %v\n%s", err, s.Out.(*bytes.Buffer))
	}
	want, err := os.ReadFile(s.Content())
	if err != nil {
		t.Fatal(err)
	}
	c, err := content.Load(s.Content())
	if err != nil || len(c.Shapers) == 0 {
		t.Fatalf("content: %v, %d shapers", err, len(c.Shapers))
	}
	c.Shapers = c.Shapers[:len(c.Shapers)-1] // an older build's file: something this build writes is missing
	if err := c.Write(s.Content()); err != nil {
		t.Fatal(err)
	}

	check := again(s)
	check.Check = true
	if err := check.Server(dedicated); err == nil {
		t.Errorf("a check with a stale content file passed:\n%s", check.Out.(*bytes.Buffer))
	}
	if got := status(check)["content"]; got != Stale {
		t.Errorf("check: content %v, want Stale", got)
	}

	start := again(s)
	if err := start.Server(dedicated); err != nil {
		t.Fatalf("the next start: %v\n%s", err, start.Out.(*bytes.Buffer))
	}
	if got := status(start)["content"]; got != Done {
		t.Errorf("the next start: content %v, want Done", got)
	}
	if got, _ := os.ReadFile(s.Content()); !bytes.Equal(got, want) {
		t.Error("the content file built again differs from this build's")
	}
}
