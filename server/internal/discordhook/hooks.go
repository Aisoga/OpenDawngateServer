// Package discordhook posts the server's news to Discord channels by webhook: the live queue message with an
// optional role ping, match results and the server's own status. It is the only part of the server that talks to
// Discord over the network (package discord is Start-Client's local IPC with the Discord app, and sends nothing
// over the network).
//
// Nothing posts unless the operator configures a webhook URL; a URL is a secret (whoever has it can post), so it
// never appears in a log line: Mask names it by the last digits of the webhook's id. Hook sends from its own
// goroutine with a bounded queue, so nothing on the Blaze or match-host paths waits for Discord. Every message
// carries allowed_mentions {"parse":[]} (plus only the configured role), and Escape makes player names inert.
package discordhook

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Event is what a post is about; each has its webhook (Settings.URLs.For).
type Event string

const (
	Queue  Event = "queue"  // the live queue message (and the role ping)
	Result Event = "result" // a finished match's result
	Server Event = "server" // the server's status: online, going down, back after a crash
)

// URLs are the configured webhook URLs ("" = none): Default serves the queue and the results unless they have their
// own; the server's status posts only to its own (operator news, opt-in).
type URLs struct {
	Default, Queue, Result, Server string
}

// For is the URL an event posts to ("" = it doesn't post).
func (u URLs) For(e Event) string {
	pick := func(own string) string {
		if s := strings.TrimSpace(own); s != "" {
			return s
		}
		return strings.TrimSpace(u.Default)
	}
	switch e {
	case Queue:
		return pick(u.Queue)
	case Result:
		return pick(u.Result)
	case Server:
		return strings.TrimSpace(u.Server)
	}
	return ""
}

// Result modes (Settings.ResultModes).
const (
	ModePvP      = "pvp"
	ModeCustom   = "custom"
	ModePractice = "practice"
)

// Settings are the server's Discord options (dgserver --discord-..., Start-Server.ini discord_...).
type Settings struct {
	URLs URLs
	// PingRole is the role id the queue message pings ("" = none), at most once per PingCooldown and only when the
	// match is at least PingMinWait away.
	PingRole                  string
	PingCooldown, PingMinWait time.Duration
	// Names shows player names (off: "a player"); QueueEdit edits one queue message live (off: a new post per change).
	Names, QueueEdit bool
	// ResultModes are the modes whose results are posted (ModePvP, ModeCustom, ModePractice).
	ResultModes []string
	// AllowLocal accepts http://127.x.x.x webhook URLs (a test aid: a local fake receiver).
	AllowLocal bool
}

// DefaultSettings are the defaults: no webhook, no ping, a 15 min cooldown, 30 s least wait, names on, the queue
// message edited live, results of PvP and custom games.
func DefaultSettings() Settings {
	return Settings{PingCooldown: 15 * time.Minute, PingMinWait: 30 * time.Second, Names: true, QueueEdit: true,
		ResultModes: []string{ModePvP, ModeCustom}}
}

// ParseOnOff reads on/off (also yes/no, true/false, 1/0).
func ParseOnOff(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "yes", "true", "1":
		return true, nil
	case "off", "no", "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q: use on or off", s)
}

// ParseModes reads a comma-separated mode list (pvp, custom, practice; "" or "none" = none).
func ParseModes(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.Split(s, ",") {
		m := strings.ToLower(strings.TrimSpace(f))
		switch m {
		case "", "none":
			continue
		case ModePvP, ModeCustom, ModePractice:
		default:
			return nil, fmt.Errorf("%q is not a mode: use pvp, custom, practice (comma-separated) or none", strings.TrimSpace(f))
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out, nil
}

// Check validates the settings: each URL (CheckURL; the error names the setting, never the URL), the role id
// (digits), the durations (0 or more).
func (s Settings) Check() error {
	for _, u := range []struct{ name, url string }{{"discord-webhook", s.URLs.Default}, {"discord-webhook-queue", s.URLs.Queue},
		{"discord-webhook-result", s.URLs.Result}, {"discord-webhook-server", s.URLs.Server}} {
		if strings.TrimSpace(u.url) == "" {
			continue
		}
		if err := CheckURL(u.url, s.AllowLocal); err != nil {
			return fmt.Errorf("%s: %v", u.name, err)
		}
	}
	if r := strings.TrimSpace(s.PingRole); r != "" && (!isDigits(r) || len(r) > 20) {
		return errors.New("discord-queue-ping: a role id is digits (Discord: Server Settings, Roles, right-click the role, Copy Role ID)")
	}
	if s.PingCooldown < 0 || s.PingMinWait < 0 {
		return errors.New("discord-ping-cooldown and discord-ping-min-wait must be 0 or more")
	}
	return nil
}

// PostsResults reports whether a mode's results are posted.
func (s Settings) PostsResults(mode string) bool {
	for _, m := range s.ResultModes {
		if m == mode {
			return true
		}
	}
	return false
}

// Hooks are the server's webhooks by event: one Hook (one sender goroutine) per distinct URL, shared by the events
// that post to it. A nil *Hooks has no webhook.
type Hooks struct {
	Settings Settings
	byEvent  map[Event]*Hook
	all      []*Hook
}

// Open checks the settings and starts a sender per distinct URL (none when no URL is set: no goroutine, no egress).
func Open(s Settings, o Options) (*Hooks, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	o.AllowLocal = s.AllowLocal
	hs := &Hooks{Settings: s, byEvent: map[Event]*Hook{}}
	byURL := map[string]*Hook{}
	for _, e := range []Event{Queue, Result, Server} {
		u := s.URLs.For(e)
		if u == "" {
			continue
		}
		h := byURL[u]
		if h == nil {
			var err error
			if h, err = New(u, o); err != nil {
				hs.Close(0)
				return nil, fmt.Errorf("%s webhook: %v", e, err)
			}
			byURL[u] = h
			hs.all = append(hs.all, h)
		}
		hs.byEvent[e] = h
	}
	return hs, nil
}

// Hook is the event's webhook (nil = the event doesn't post; a nil *Hook's methods do nothing).
func (hs *Hooks) Hook(e Event) *Hook {
	if hs == nil {
		return nil
	}
	return hs.byEvent[e]
}

// Any reports whether any event posts.
func (hs *Hooks) Any() bool { return hs != nil && len(hs.all) > 0 }

// Close flushes every webhook for at most flush (together, not one after the other) and stops them.
func (hs *Hooks) Close(flush time.Duration) {
	if hs == nil {
		return
	}
	var wg sync.WaitGroup
	for _, h := range hs.all {
		wg.Add(1)
		go func(h *Hook) {
			defer wg.Done()
			h.Close(flush)
		}(h)
	}
	wg.Wait()
}

// Describe is the start-up log line, without any secret: per event on (and the masked webhook) or off, then the
// queue's ping, names, the queue message's editing and the result modes.
func (hs *Hooks) Describe() string {
	s := DefaultSettings()
	if hs != nil {
		s = hs.Settings
	}
	var parts []string
	for _, e := range []Event{Queue, Result, Server} {
		h := hs.Hook(e)
		if h == nil {
			parts = append(parts, string(e)+" off")
			continue
		}
		note := ""
		if e == Server {
			note = ", only its own webhook"
		}
		parts = append(parts, fmt.Sprintf("%s on (%s%s)", e, h.Label(), note))
	}
	if !hs.Any() {
		return "discord webhooks: off (" + strings.Join(parts, ", ") + ")"
	}
	ping := "no role ping"
	if s.PingRole != "" {
		ping = fmt.Sprintf("ping role %s (at most every %s, not within %s of the start)", s.PingRole, s.PingCooldown, s.PingMinWait)
	}
	names := "player names"
	if !s.Names {
		names = `"a player" for names`
	}
	edit := "the queue message edited live"
	if !s.QueueEdit {
		edit = "a new queue post per change"
	}
	modes := "none"
	if len(s.ResultModes) > 0 {
		modes = strings.Join(s.ResultModes, ",")
	}
	local := ""
	if s.AllowLocal {
		local = "; TEST: local receivers allowed (--test-discord-local)"
	}
	return fmt.Sprintf("discord webhooks: %s; %s; %s; %s; results of %s%s", strings.Join(parts, ", "), ping, names, edit,
		modes, local)
}
