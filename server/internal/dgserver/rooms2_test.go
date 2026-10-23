package dgserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/blaze"
)

// ROOMS2: --chat-rooms is checked before anything starts, and a started server serves and logs the configured rooms.
func TestChatRoomsFlag(t *testing.T) {
	for _, bad := range []string{"General:1", "a:queue,b:queue", "a,,b"} {
		if code := Run([]string{"--chat-rooms", bad, "--print-ports"}, Hooks{}); code != 2 {
			t.Errorf("--chat-rooms %q: exit %d, want 2", bad, code)
		}
	}
	rooms, err := blaze.ParseChatRooms("Lobby:20, News:queue")
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: logDir, chatRooms: rooms, chatRoomsSet: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	in.stop()
	files, _ := filepath.Glob(filepath.Join(logDir, "*.log"))
	if len(files) == 0 {
		t.Fatal("no log")
	}
	raw, _ := os.ReadFile(files[0])
	if want := `[stub] chat rooms (ROOMS2): 1 "Lobby" (20), 2 "News" (50, queue news)`; !strings.Contains(string(raw), want) {
		t.Errorf("log lacks %q:\n%s", want, raw)
	}
}
