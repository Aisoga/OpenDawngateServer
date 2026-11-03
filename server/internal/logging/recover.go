package logging

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// Recover keeps a panic in the code that serves one connection or one datagram from ending the whole server.
// Defer it first in the goroutine that serves a connection (or at the top of the function that handles one
// datagram): on a panic it closes c (nil: nothing to close), logs the panic and its stack under who with the peer,
// and returns normally, so the handler's own deferred calls have run and the server goes on with the others. Without
// a panic it does nothing. A nil l writes the line to stderr.
//
// It must be deferred directly (defer l.Recover(...)), not called from another deferred function: recover only
// stops a panic in the function that the deferred call itself is.
func (l *Log) Recover(who, peer string, c io.Closer) {
	r := recover()
	if r == nil {
		return
	}
	if c != nil {
		c.Close()
	}
	msg := fmt.Sprintf("%s panic: %v (that connection is closed; the server goes on)\n%s", peer, r, debug.Stack())
	if l == nil {
		fmt.Fprintln(os.Stderr, "["+who+"] "+msg)
		return
	}
	l.Log(who, msg)
}
