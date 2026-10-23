package launcher

import "strings"

// discordKeys are Start-Server.ini's Discord keys, each dgserver's flag of the same name (discord_webhook →
// --discord-webhook).
var discordKeys = []string{"discord_webhook", "discord_webhook_queue", "discord_webhook_result", "discord_webhook_server",
	"discord_queue_ping", "discord_ping_cooldown", "discord_ping_min_wait", "discord_names", "discord_queue_edit",
	"discord_result_modes"}

// onlineCountKeys are Start-Server.ini's keys of the players-online count in the main menu's marquee, each dgserver's
// flag of the same name (online_count → --online-count).
var onlineCountKeys = []string{"online_count", "online_count_every"}

// DiscordFlags are dgserver's flags for the Discord keys that are set (an empty key keeps dgserver's default; every
// webhook is off by default). They come before server_flags. dgserver checks the values; its error names the flag,
// never the URL, and its log names a webhook only by the last digits of its id.
func DiscordFlags(cfg Config) []string { return keyFlags(cfg, discordKeys) }

// OnlineCountFlags are dgserver's flags for the players-online count's keys that are set (an empty or missing key, as
// in a file from an earlier release, keeps dgserver's default: on, 30s). They come before server_flags; dgserver
// checks the values.
func OnlineCountFlags(cfg Config) []string { return keyFlags(cfg, onlineCountKeys) }

// ChatRoomsFlags is dgserver's --chat-rooms for the chat_rooms key when it is set (an empty or missing key, as in a
// file from an earlier release, keeps dgserver's default rooms). It comes before server_flags; dgserver checks it.
func ChatRoomsFlags(cfg Config) []string { return keyFlags(cfg, []string{"chat_rooms"}) }

// HalloweenFlags is dgserver's --halloween for the halloween key when it is set (an empty or missing key, as in a file
// from an earlier release, keeps dgserver's default: off). It comes before server_flags; dgserver checks it.
func HalloweenFlags(cfg Config) []string { return keyFlags(cfg, []string{"halloween"}) }

// MapFlags are dgserver's --map and --custom-maps for the map and custom_maps keys that are set (an empty or missing
// key, as in a file from an earlier release, keeps dgserver's default: SM_Map_v5, no other map for custom games). They
// come before server_flags; dgserver checks them.
func MapFlags(cfg Config) []string { return keyFlags(cfg, []string{"map", "custom_maps"}) }

// keyFlags are "--<key with dashes>=<value>" for each of keys that cfg sets.
func keyFlags(cfg Config, keys []string) []string {
	var f []string
	for _, k := range keys {
		if v := strings.TrimSpace(cfg[k]); v != "" {
			f = append(f, "--"+strings.ReplaceAll(k, "_", "-")+"="+v)
		}
	}
	return f
}

// noDiscordFlags turn every webhook off: Start-Client's server for This PC never posts to Discord, whatever
// Start-Server.ini's server_flags say (later flags win).
func noDiscordFlags() []string {
	var f []string
	for _, k := range discordKeys {
		if strings.HasPrefix(k, "discord_webhook") {
			f = append(f, "--"+strings.ReplaceAll(k, "_", "-")+"=")
		}
	}
	return f
}
