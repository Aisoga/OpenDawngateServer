package dgserver

import (
	"flag"
	"fmt"
	"time"

	"dawngate/server/internal/discordhook"
)

// discordFlags are the --discord-... flags (the Discord channel posts by webhook; Start-Server.ini's discord_...
// keys). The webhook URLs are secrets: they are never logged (discordhook.Mask), and the flag listing (-h) shows
// only their empty defaults.
type discordFlags struct {
	s                                discordhook.Settings
	names, queueEdit, resultModes    *string
	webhook, queue, result, srv, png *string
}

// addDiscordFlags declares the flags on fs.
func addDiscordFlags(fs *flag.FlagSet) *discordFlags {
	d := &discordFlags{s: discordhook.DefaultSettings()}
	d.webhook = fs.String("discord-webhook", "", "the Discord webhook (https://discord.com/api/webhooks/<id>/<token>; keep it secret) for the queue message and the match results, unless they have their own; empty = none")
	d.queue = fs.String("discord-webhook-queue", "", "the queue message's own Discord webhook (empty = --discord-webhook)")
	d.result = fs.String("discord-webhook-result", "", "the match results' own Discord webhook (empty = --discord-webhook)")
	d.srv = fs.String("discord-webhook-server", "", "the Discord webhook for the server's status (online, going down, back after a crash); only this one, never --discord-webhook; empty = none")
	d.png = fs.String("discord-queue-ping", "", "a Discord role id the queue message pings (empty = no ping)")
	fs.DurationVar(&d.s.PingCooldown, "discord-ping-cooldown", d.s.PingCooldown, "the queue message pings the role at most this often")
	fs.DurationVar(&d.s.PingMinWait, "discord-ping-min-wait", d.s.PingMinWait, "no ping when the match starts sooner than this anyway")
	d.names = fs.String("discord-names", "on", `Discord posts show player names: on, or off ("a player")`)
	d.queueEdit = fs.String("discord-queue-edit", "on", "the queue message: on = one message edited live, off = a new post per change")
	d.resultModes = fs.String("discord-result-modes", "pvp,custom", "the modes whose match results are posted, comma-separated: pvp, custom, practice (also a practice group's game); none")
	fs.BoolVar(&d.s.AllowLocal, "test-discord-local", false, "test aid: also accept http://127.x.x.x:<port>/api/webhooks/<id>/<token> webhooks (a local fake receiver)")
	return d
}

// settings checks the flags (the error names the flag, never a URL).
func (d *discordFlags) settings() (discordhook.Settings, error) {
	s := d.s
	s.URLs = discordhook.URLs{Default: *d.webhook, Queue: *d.queue, Result: *d.result, Server: *d.srv}
	s.PingRole = *d.png
	var err error
	if s.Names, err = discordhook.ParseOnOff(*d.names); err != nil {
		return s, fmt.Errorf("--discord-names: %v", err)
	}
	if s.QueueEdit, err = discordhook.ParseOnOff(*d.queueEdit); err != nil {
		return s, fmt.Errorf("--discord-queue-edit: %v", err)
	}
	if s.ResultModes, err = discordhook.ParseModes(*d.resultModes); err != nil {
		return s, fmt.Errorf("--discord-result-modes: %v", err)
	}
	if err := s.Check(); err != nil {
		return s, fmt.Errorf("--%v", err)
	}
	return s, nil
}

// discordFlush is how long a stop waits for the Discord posts still queued (the "going down" post).
const discordFlush = 3 * time.Second
