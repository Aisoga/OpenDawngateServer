//go:build !windows

package launcher

import (
	"errors"
	"time"
)

// ClientMutex is the game's single-instance mutex (Windows only).
const ClientMutex = `Global\Dawngate`

// ClientRunning is false outside Windows.
func ClientRunning() bool { return false }

// ForegroundWindow is the foreground window (Windows only).
func ForegroundWindow() uintptr { return 0 }

// ParkEvents are what Park tells its caller.
type ParkEvents struct {
	Moved          func(n int)
	ForegroundBack func(n int, ok bool)
	Lock           func(locked bool)
}

// StartOptions are StartGame's.
type StartOptions struct {
	Args       string
	NoActivate bool
}

// Game is a started Dawngate.exe (Windows only).
type Game struct {
	Pid int
	Exe string
}

// StartGame runs only on Windows.
func StartGame(string, StartOptions) (*Game, error) { return nil, errors.ErrUnsupported }

func (g *Game) ID() int                                   { return g.Pid }
func (g *Game) Wait() (uint32, error)                     { return 0, errors.ErrUnsupported }
func (g *Game) Exited(time.Duration) bool                 { return true }
func (g *Game) Close()                                    {}
func (g *Game) Kill() error                               { return errors.ErrUnsupported }
func (g *Game) Release()                                  {}
func (g *Game) Park(<-chan struct{}, uintptr, ParkEvents) {}
