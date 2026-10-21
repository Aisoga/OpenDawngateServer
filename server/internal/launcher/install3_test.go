package launcher

import (
	"bytes"
	"strings"
	"testing"

	"dawngate/server/internal/dgserver"
)

// The client package's Start-Client.ini: this release's template with the server, its tunnel port (only when it
// isn't 42128) and its key; every key is one Start-Client knows, and the file reads back as the server's.
func TestPackageClientIni(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	b := PackageClientIni("play.example.org", 42128, pin)
	if dgserver.ClientIni == nil {
		t.Error("the launcher doesn't give dgserver its ini")
	}
	if bytes.Contains(bytes.ReplaceAll(b, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("not CRLF lines")
	}
	cfg, err := ParseConfig(bytes.NewReader(b), ClientConfigName)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["server"] != "play.example.org" || cfg[GatewayPinKey] != pin || cfg["gateway_port"] != "" || cfg["tunnel"] != "auto" {
		t.Errorf("keys %v", cfg)
	}
	if u := cfg.Unknown(clientKeys...); len(u) != 0 {
		t.Errorf("unknown keys %v", u)
	}
	if err := checkIniGatewayPin(cfg); err != nil {
		t.Error(err)
	}
	if !strings.Contains(string(b), "; The tunnel key of the server above") {
		t.Error("the template's comments are missing")
	}
	cfg, _ = ParseConfig(bytes.NewReader(PackageClientIni("203.0.113.5", 42000, pin)), ClientConfigName)
	if cfg["gateway_port"] != "42000" || cfg["server"] != "203.0.113.5" {
		t.Errorf("another port: %v", cfg)
	}
}
