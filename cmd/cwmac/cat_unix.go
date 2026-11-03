//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

type catConn struct{ fd int }

// openCAT opens a serial port raw at baud, 8N1, with DTR and RTS low (so it can't key).
func openCAT(port string, baud int) (*catConn, error) {
	if !catBaudOK(baud) {
		return nil, fmt.Errorf("unsupported baud %d", baud)
	}
	fd, err := syscall.Open(port, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open the CAT port %s: %v", port, err)
	}
	ioctlInt(fd, tiocmBIC, tiocmDTR|tiocmRTS)
	if err := catRaw(fd, baud); err != nil { // termios: per OS (cat_termios_*.go)
		syscall.Close(fd)
		return nil, fmt.Errorf("CAT port settings: %v", err)
	}
	syscall.SetNonblock(fd, false)
	catFlush(fd) // drop anything pending
	return &catConn{fd: fd}, nil
}

func (c *catConn) write(s string) error {
	_, err := syscall.Write(c.fd, []byte(s))
	time.Sleep(30 * time.Millisecond) // the rig needs a moment between commands
	return err
}

// readReply reads up to the ';' that ends a CAT answer (600 ms at most).
func (c *catConn) readReply() (string, error) { return c.readReplyWithin(600 * time.Millisecond) }

func (c *catConn) readReplyWithin(d time.Duration) (string, error) {
	var b []byte
	buf := make([]byte, 64)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		n, err := syscall.Read(c.fd, buf)
		if err != nil && err != syscall.EINTR {
			return "", err
		}
		b = append(b, buf[:max(n, 0)]...)
		for i, ch := range b {
			if ch == ';' {
				if string(b[:i+1]) == "?;" {
					return "", errors.New("the rig rejected the command")
				}
				return string(b[:i+1]), nil
			}
		}
	}
	return "", errors.New("no answer")
}

func (c *catConn) Close() { syscall.Close(c.fd) }
