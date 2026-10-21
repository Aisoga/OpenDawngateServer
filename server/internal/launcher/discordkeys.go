package launcher

import "strings"

// discordKeys are Start-Server.ini's Discord keys, each dgserver's flag of the same name (discord_webhook →
// --discord-webhook).
var discordKeys = []string{"discord_webhook", "discord_webhook_queue", "discord_webhook_result", "discord_webhook_server",
	"discord_queue_ping", "discord_ping_cooldown", "discord_ping_min_wait", "discord_names", "discord_queue_edit",
	"discord_result_modes"}

// DiscordFlags are dgserver's flags for the Discord keys that are set (an empty key keeps dgserver's default; every
// webhook is off by default). They come before server_flags. dgserver checks the values; its error names the flag,
// never the URL, and its log names a webhook only by the last digits of its id.
func DiscordFlags(cfg Config) []string {
	var f []string
	for _, k := range discordKeys {
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
