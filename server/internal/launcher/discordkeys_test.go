package launcher

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// Start-Server.ini's Discord keys: each one in the template and known, each set one dgserver's flag of the same
// name, the template's defaults accepted by dgserver, an empty key not passed.
func TestDiscordFlags(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range discordKeys {
		if _, ok := cfg[k]; !ok || !slices.Contains(serverKeys, k) {
			t.Errorf("key %s: in the template %v, known %v", k, ok, slices.Contains(serverKeys, k))
		}
	}
	f := DiscordFlags(cfg)
	want := "--discord-ping-cooldown=15m --discord-ping-min-wait=30s --discord-names=on --discord-queue-edit=on --discord-result-modes=pvp,custom"
	if got := strings.Join(f, " "); got != want {
		t.Errorf("the template's flags:\n got %s\nwant %s", got, want)
	}
	if code := dgserver.Run(append(f, "--print-ports"), dgserver.Hooks{}); code != 0 {
		t.Errorf("dgserver refuses the template's Discord flags (exit %d)", code)
	}
	cfg["discord_webhook_server"] = " https://discord.com/api/webhooks/1/t "
	cfg["discord_queue_ping"] = "123"
	f = DiscordFlags(cfg)
	if !slices.Contains(f, "--discord-webhook-server=https://discord.com/api/webhooks/1/t") || !slices.Contains(f, "--discord-queue-ping=123") {
		t.Errorf("flags %v", f)
	}
	if code := dgserver.Run(append(f, "--print-ports"), dgserver.Hooks{}); code != 0 {
		t.Errorf("dgserver refuses %v (exit %d)", f, code)
	}
	cfg["discord_webhook"] = "https://example.com/api/webhooks/1/t"
	if code := dgserver.Run(append(DiscordFlags(cfg), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("a webhook elsewhere than Discord: exit %d", code)
	}
}
