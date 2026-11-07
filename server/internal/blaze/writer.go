package blaze

import (
	"errors"
	"time"

	"dawngate/server/internal/logging"
)

// Every connection writes through its own goroutine. A write blocks for as long as the client doesn't read, and pushes come
// from other players' goroutines: one message to a client that stopped reading used to hang the sender's whole connection
// (and a room's join, a custom game's change or a match's event with it), for as long as the attacker liked. Now a frame only
// waits in the connection's queue; a client that doesn't take its frames within connWriteTimeout, or lets more than
// maxQueuedBytes pile up, loses its connection, and nobody else notices.
const (
	connWriteTimeout = 15 * time.Second // a real client reads at once; its own idle timeout is 90 s
	maxQueuedFrames  = 1024
	maxQueuedBytes   = 4 << 20 // far above a login's burst of about a hundred frames
	flushOnClose     = 2 * time.Second
)

var errConnQueue = errors.New("the client doesn't take its frames")

// startWriter starts the connection's writer (ServeConn).
func (c *Conn) startWriter(s *Server) {
	c.out, c.outDone = make(chan []byte, maxQueuedFrames), make(chan struct{})
	go c.writeLoop(s)
}

func (c *Conn) writeLoop(s *Server) {
	defer close(c.outDone)
	for b := range c.out {
		c.outSize.Add(-int64(len(b)))
		if c.failed.Load() {
			continue
		}
		c.nc.SetWriteDeadline(time.Now().Add(connWriteTimeout))
		if _, err := c.nc.Write(b); err != nil {
			c.failed.Store(true)
			s.Log.Logf("blaze", "write error: %s: %v (the connection closes)", logging.ErrName(err), err)
			c.nc.Close()
		}
	}
}

// stopWriter ends the writer once it has written what waits, within flushOnClose (ServeConn's end: a last error reply or
// notice still goes out; the connection closes right after, which also ends a write that is stuck).
func (c *Conn) stopWriter() {
	c.wmu.Lock()
	c.stopped = true
	close(c.out)
	c.wmu.Unlock()
	t := time.NewTimer(flushOnClose)
	defer t.Stop()
	select {
	case <-c.outDone:
	case <-t.C:
	}
}

// write hands a whole frame to the writer, or writes it at once on a connection without one.
func (c *Conn) write(s *Server, b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.out == nil {
		_, err := c.nc.Write(b)
		return err
	}
	if c.stopped || c.failed.Load() {
		return errConnQueue
	}
	if c.outSize.Add(int64(len(b))) > maxQueuedBytes {
		c.overflow(s)
		return errConnQueue
	}
	select {
	case c.out <- b:
		return nil
	default:
		c.overflow(s)
		return errConnQueue
	}
}

func (c *Conn) overflow(s *Server) {
	if c.failed.CompareAndSwap(false, true) {
		s.Log.Logf("blaze", "a client doesn't take its frames (%d bytes waiting): its connection closes", c.outSize.Load())
		c.nc.Close()
	}
}
