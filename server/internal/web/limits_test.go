package web

import (
	"strings"
	"testing"
	"time"

	"dawngate/server/internal/limits"
)

// QoS datagrams over the source address's rate are dropped before they are read (one log line a minute per
// address; the rest counted in the minute's limits line).
func TestQoSLimits(t *testing.T) {
	r := newQoSRig(t)
	cfg := limits.Default()
	cfg.Packets = limits.Rate{PerSec: 1.0 / 60, Burst: 3}
	r.q.Limits = limits.New(cfg, nil)
	r.q.Hold = 0
	c := r.udpClient(t)
	for i := 0; i < 6; i++ {
		if _, err := c.Write(words(uint32(i+1), 1, 0, 1, 1000)); err != nil {
			t.Fatal(err)
		}
	}
	got := 0
	buf := make([]byte, 0x4F0)
	for {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := c.Read(buf); err != nil {
			break
		}
		got++
	}
	if got != 3 {
		t.Errorf("%d echoes, want 3:\n%s", got, r.log.String())
	}
	if log := r.log.String(); strings.Count(log, "limits: udp from 127.0.0.1 over its rate; dropped") != 1 {
		t.Errorf("log:\n%s", log)
	}
	if rep := r.q.Limits.TakeReport(); rep != "refused: udp 3 (1 addresses)" {
		t.Errorf("report %q", rep)
	}
}
