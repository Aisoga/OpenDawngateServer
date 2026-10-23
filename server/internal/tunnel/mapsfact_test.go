package tunnel

import (
	"bytes"
	"strings"
	"testing"
)

// TestFactMapTags: the maps fact on the wire (cleaned like every fact) and read back; a server that doesn't name it.
func TestFactMapTags(t *testing.T) {
	if got := MapsFact([]string{"mm", "xx"}); got != "mm+xx" {
		t.Errorf("MapsFact = %q", got)
	}
	_, facts, err := readInfoReply(bytes.NewReader(infoReply("v1", map[string]string{FactMaps: MapsFact([]string{"mm"})})))
	if tags, ok := FactMapTags(facts); err != nil || !ok || len(tags) != 1 || tags[0] != "mm" {
		t.Errorf("the maps fact read back: %v, %v, %v", tags, ok, err)
	}
	_, facts, _ = readInfoReply(bytes.NewReader(infoReply("v1", map[string]string{FactMaps: ""})))
	if tags, ok := FactMapTags(facts); !ok || len(tags) != 0 {
		t.Errorf("no maps offered: %v, %v (want named, none)", tags, ok)
	}
	if tags, ok := FactMapTags(map[string]string{FactMaps: "MM++ xx "}); !ok || strings.Join(tags, ",") != "mm,xx" {
		t.Errorf("FactMapTags of a sloppy value: %v, %v", tags, ok)
	}
	if tags, ok := FactMapTags(map[string]string{FactSignup: "open"}); ok || tags != nil {
		t.Errorf("a server without the fact: %v, %v", tags, ok)
	}
	if _, ok := FactMapTags(nil); ok {
		t.Error("no facts at all: named")
	}
}
