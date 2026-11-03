package logging

import (
	"bytes"
	"strings"
	"testing"
)

type closer struct{ closed int }

func (c *closer) Close() error { c.closed++; return nil }

// Recover stops a panic, closes the connection and logs the panic with its stack; without a panic it does nothing.
func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	c := &closer{}
	func() {
		defer l.Recover("blaze", "1.2.3.4:5", c)
		panic("bad frame")
	}()
	if c.closed != 1 {
		t.Errorf("closed %d times", c.closed)
	}
	log := buf.String()
	for _, want := range []string{"[blaze] 1.2.3.4:5 panic: bad frame (that connection is closed; the server goes on)\n", "TestRecover"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	buf.Reset()
	func() { defer l.Recover("blaze", "x", c) }()
	if c.closed != 1 || buf.Len() != 0 {
		t.Errorf("without a panic: closed %d, log %q", c.closed, buf.String())
	}
	func() { // a nil log (a component without one) still recovers, onto stderr
		defer (*Log)(nil).Recover("tunnel", "x", nil)
		panic("no log")
	}()
}
