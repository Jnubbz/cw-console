//go:build linux || darwin

package main

import (
	"fmt"
	"time"
)

// catConn is one user of a shared serial port. Close drops the reference.
// The descriptor stays open while anyone else, including a key lease, holds it.
type catConn struct {
	sp  *sharedPort
	bus *portBus
}

// openCAT opens a serial port raw at baud, 8N1, with DTR and RTS low (so it can't key).
// The same device name always returns the same descriptor.
func openCAT(port string, baud int) (*catConn, error) {
	if !catBaudOK(baud) {
		return nil, fmt.Errorf("unsupported baud %d", baud)
	}
	sp, err := radioBus.retain(port, baud)
	if err != nil {
		return nil, err
	}
	return &catConn{sp: sp, bus: radioBus}, nil
}

func (c *catConn) write(s string) error {
	if c == nil || c.sp == nil {
		return errPortClosed
	}
	_, err := c.sp.writeRead(s, 0)
	return err
}

func (c *catConn) readReply() (string, error) { return c.readReplyWithin(600 * time.Millisecond) }

func (c *catConn) readReplyWithin(d time.Duration) (string, error) {
	if c == nil || c.sp == nil {
		return "", errPortClosed
	}
	c.sp.mu.Lock()
	defer c.sp.mu.Unlock()
	if c.sp.closed || c.sp.fd < 0 {
		return "", errPortClosed
	}
	return c.sp.readLocked(d)
}

// ask writes one command and, when wait > 0, reads the answer, as one section.
func (c *catConn) ask(s string, wait time.Duration) (string, error) {
	if c == nil || c.sp == nil {
		return "", errPortClosed
	}
	return c.sp.writeRead(s, wait)
}

func (c *catConn) Close() {
	if c == nil || c.sp == nil {
		return
	}
	c.bus.release(c.sp)
	c.sp = nil
}
