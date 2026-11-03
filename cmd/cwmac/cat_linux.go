//go:build linux

package main

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

type catConn struct{ fd int }

var catSpeeds = map[int]uint32{4800: syscall.B4800, 9600: syscall.B9600, 19200: syscall.B19200,
	38400: syscall.B38400, 115200: syscall.B115200}

// openCAT opens a serial port raw at baud, 8N1, with DTR and RTS low (so it can't key).
func openCAT(port string, baud int) (*catConn, error) {
	sp, ok := catSpeeds[baud]
	if !ok {
		return nil, fmt.Errorf("unsupported baud %d", baud)
	}
	fd, err := syscall.Open(port, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open the CAT port %s: %v", port, err)
	}
	ioctlInt(fd, tiocmBIC, tiocmDTR|tiocmRTS)
	var t syscall.Termios
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL | sp
	t.Ispeed, t.Ospeed = sp, sp
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 0, 1 // reads return after 0.1 s without data
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("CAT port settings: %v", e)
	}
	syscall.SetNonblock(fd, false)
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x540B /* TCFLSH */, 2) // drop anything pending
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
