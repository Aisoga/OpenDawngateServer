package launcher

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dawngate/server/internal/blaze"
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

// Start-Server.ini's players-online count keys: in the template and known, passed as dgserver's flags of the same
// names, which dgserver accepts; a file without them (an earlier release's) passes none (dgserver's defaults).
func TestOnlineCountFlags(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range onlineCountKeys {
		if _, ok := cfg[k]; !ok || !slices.Contains(serverKeys, k) {
			t.Errorf("key %s: in the template %v, known %v", k, ok, slices.Contains(serverKeys, k))
		}
	}
	f := OnlineCountFlags(cfg)
	if got, want := strings.Join(f, " "), "--online-count=on --online-count-every=30s"; got != want {
		t.Errorf("the template's flags:\n got %s\nwant %s", got, want)
	}
	if code := dgserver.Run(append(f, "--print-ports"), dgserver.Hooks{}); code != 0 {
		t.Errorf("dgserver refuses the template's flags (exit %d)", code)
	}
	if code := dgserver.Run(append(OnlineCountFlags(Config{"online_count": "maybe"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("online_count = maybe: exit %d", code)
	}
	if f := OnlineCountFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}

// Start-Server.ini's halloween key: in the template, off, and known, passed as --halloween, which dgserver accepts (and
// refuses a bad value); none from a file without it (dgserver's default, off).
func TestHalloweenKey(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(serverKeys, "halloween") {
		t.Error("halloween isn't a known key")
	}
	f := HalloweenFlags(cfg)
	if got := strings.Join(f, " "); got != "--halloween=off" {
		t.Errorf("the template's flag: %s", got)
	}
	for _, v := range []string{"off", "on"} {
		if code := dgserver.Run(append(HalloweenFlags(Config{"halloween": v}), "--print-ports"), dgserver.Hooks{}); code != 0 {
			t.Errorf("halloween = %s: exit %d", v, code)
		}
	}
	if code := dgserver.Run(append(HalloweenFlags(Config{"halloween": "pumpkins"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("halloween = pumpkins: exit %d", code)
	}
	if f := HalloweenFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}

// Start-Server.ini's chat_rooms key: in the template with dgserver's default and known, passed as --chat-rooms, which
// dgserver accepts (and refuses a bad value); none from a file without it.
func TestChatRoomsKey(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(serverKeys, "chat_rooms") || cfg["chat_rooms"] != blaze.DefaultChatRooms {
		t.Errorf("chat_rooms: known %v, template %q", slices.Contains(serverKeys, "chat_rooms"), cfg["chat_rooms"])
	}
	f := ChatRoomsFlags(cfg)
	if got, want := strings.Join(f, " "), "--chat-rooms="+blaze.DefaultChatRooms; got != want {
		t.Errorf("flags: got %s, want %s", got, want)
	}
	if code := dgserver.Run(append(f, "--print-ports"), dgserver.Hooks{}); code != 0 {
		t.Errorf("dgserver refuses the template's chat_rooms (exit %d)", code)
	}
	if code := dgserver.Run(append(ChatRoomsFlags(Config{"chat_rooms": "a:1"}), "--print-ports"), dgserver.Hooks{}); code != 2 {
		t.Errorf("chat_rooms = a:1: exit %d", code)
	}
	if f := ChatRoomsFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}

// Start-Server.ini's map and custom_maps keys: in the template (SM_Map_v5, none), known, passed as --map and
// --custom-maps, which dgserver accepts (and refuses a map it can't host); none from a file without them.
func TestMapKeys(t *testing.T) {
	cfg, _, err := LoadOrCreate(filepath.Join(t.TempDir(), ServerConfigName), ServerConfigText)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"map", "custom_maps"} {
		if !slices.Contains(serverKeys, k) {
			t.Errorf("%s isn't a known key", k)
		}
	}
	if got := strings.Join(MapFlags(cfg), " "); got != "--map=SM_Map_v5" {
		t.Errorf("the template's flags: %s", got)
	}
	good := Config{"map": "MM_Map_v3", "custom_maps": "MM_Map_v3, SM_Map_v5"}
	if got := strings.Join(MapFlags(good), " "); got != "--map=MM_Map_v3 --custom-maps=MM_Map_v3, SM_Map_v5" {
		t.Errorf("flags: %s", got)
	}
	if code := dgserver.Run(append(MapFlags(good), "--print-ports"), dgserver.Hooks{}); code != 0 {
		t.Errorf("%v: exit %d", good, code)
	}
	for _, bad := range []Config{{"map": "TA_Map_v1"}, {"custom_maps": "MM_Map_v3, Hero_testArena"}} {
		if code := dgserver.Run(append(MapFlags(bad), "--print-ports"), dgserver.Hooks{}); code != 2 {
			t.Errorf("%v: exit %d, want 2", bad, code)
		}
	}
	if f := MapFlags(Config{"address": "127.0.0.1"}); len(f) != 0 {
		t.Errorf("an older file: %v", f)
	}
}
