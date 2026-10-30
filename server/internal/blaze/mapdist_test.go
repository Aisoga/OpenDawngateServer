package blaze

import (
	"strings"
	"testing"
)

// TestMapFixServed (MAPED-DIST): when the server hands the map's package out, a version refusal and the fallback say
// that Start-Client downloads it; otherwise they keep the copy-by-hand text, and MAPGUARD's release text is unchanged.
func TestMapFixServed(t *testing.T) {
	s := &Server{MapServed: func(level string) bool { return level == "CM_Ring" }, Release: "v1"}
	mc := mapCheck{version: true, tell: "doesn't have Test Ring"}
	if got := s.mapRefusedText("CM_Ring", `"G1"`, mc); !strings.HasSuffix(got, "doesn't have Test Ring. Start-Client "+
		"downloads this server's version of it when you log in: close the game and start it again with Start-Client.exe, "+
		"which installs it.") {
		t.Errorf("served: %s", got)
	}
	if got := s.mapFallbackText("CM_Ring", mc); !strings.Contains(got, "Start-Client downloads this server's version of it "+
		"when you log in: close the game and start it again with Start-Client.exe to play ") {
		t.Errorf("served, the fallback: %s", got)
	}
	if got := s.mapRefusedText("CM_Grove", `"G1"`, mc); !strings.Contains(got, "Copy this server's version of its map package") {
		t.Errorf("not served: %s", got)
	}
	if got := (&Server{}).mapQueueText("CM_Ring", "", mc); !strings.Contains(got, "Copy this server's version") {
		t.Errorf("no delivery: %s", got)
	}
	if got := s.mapRefusedText("MM_Map_v3", `"G1"`, mapCheck{tell: "x"}); !strings.Contains(got, "Start-Client.exe of this server's release") {
		t.Errorf("the release rule: %s", got)
	}
}
