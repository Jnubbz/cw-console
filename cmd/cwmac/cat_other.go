//go:build !linux && !darwin

package main

import (
	"errors"
	"time"

	"github.com/Jnubbz/cw-console/rig"
)

// Serial CAT on this OS is refused. Keying a COM port's DTR/RTS (openKeyLine)
// is separate and does work on Windows. The shared-port owner the Mac and Pi
// use is not wired up here, so a sweep or a CAT poll fails closed.

var errPortClosed = errors.New("serial port is closed")

var radioBus = &portBus{}

type portBus struct{}

type sharedPort struct {
	key  string
	path string
	baud int
}

func (p *sharedPort) transact([]string) ([]string, error) {
	return nil, errors.New("rig control needs macOS or Linux here")
}

func (p *sharedPort) writeRead(string, time.Duration) (string, error) {
	return "", errors.New("rig control needs macOS or Linux here")
}

func (b *portBus) holdLease(string, int, rig.Priority) (*sharedPort, func(), error) {
	return nil, nil, errors.New("rig control needs macOS or Linux here")
}

func (b *portBus) exchange(string, int, rig.Priority, []string) ([]string, error) {
	return nil, errors.New("rig control needs macOS or Linux here")
}

func (b *portBus) acquire(string, rig.Priority, func() error) error {
	return errors.New("rig control needs macOS or Linux here")
}

type busLine struct {
	sp *sharedPort
}

func (l *busLine) Key(bool) error { return errors.New("rig control needs macOS or Linux here") }
func (l *busLine) Close()         {}

type catConn struct {
	sp  *sharedPort
	bus *portBus
}

func openCAT(port string, baud int) (*catConn, error) {
	return nil, errors.New("rig control needs macOS or Linux here")
}
func (c *catConn) write(s string) error                          { return errPortClosed }
func (c *catConn) readReply() (string, error)                    { return "", errPortClosed }
func (c *catConn) readReplyWithin(time.Duration) (string, error) { return "", errPortClosed }
func (c *catConn) ask(string, time.Duration) (string, error)     { return "", errPortClosed }
func (c *catConn) Close()                                        {}
