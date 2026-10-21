package dgserver

import (
	"os"
	"strings"
	"testing"

	"dawngate/server/internal/discordhook"
)

const hookToken = "tok3n-must-never-be-logged_0123456789abcdefghijklmnopqrstuvwxyz"

// The Discord flags: a webhook that isn't Discord's is refused at the start (exit 2), a local receiver only with the
// test aid; the error never shows the URL.
func TestDiscordFlagsChecked(t *testing.T) {
	for _, args := range [][]string{
		{"--discord-webhook=https://example.com/api/webhooks/1/" + hookToken},
		{"--discord-webhook-server=http://127.0.0.1:9/api/webhooks/1/" + hookToken},
		{"--discord-queue-ping=@everyone"},
		{"--discord-names=maybe"},
		{"--discord-result-modes=pvp,ranked"},
	} {
		if code := Run(append(args, "--print-ports"), Hooks{}); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := Run([]string{"--test-discord-local", "--discord-webhook-server=http://127.0.0.1:9/api/webhooks/1/" + hookToken,
		"--discord-result-modes=practice", "--discord-names=off", "--print-ports"}, Hooks{}); code != 0 {
		t.Errorf("a local receiver with --test-discord-local: exit %d", code)
	}
}

// The start-up line names each event's webhook by the last digits of its id, never its token.
func TestDiscordStartLine(t *testing.T) {
	s := discordhook.DefaultSettings()
	s.URLs = discordhook.URLs{Default: "http://127.0.0.1:9/api/webhooks/123456789/" + hookToken}
	s.AllowLocal = true
	in, err := start(config{bind: "127.0.0.1", certs: testCerts(t), logDir: t.TempDir(), discord: s})
	if err != nil {
		t.Fatal(err)
	}
	path := in.log.Path
	in.stop()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	if strings.Contains(log, hookToken) {
		t.Errorf("the webhook's token is in the log:\n%s", log)
	}
	if !strings.Contains(log, "discord webhooks: queue on (local test receiver 127.0.0.1:9, webhook id …6789), result on (") ||
		!strings.Contains(log, "server off") {
		t.Errorf("no start-up line:\n%s", log)
	}
}
