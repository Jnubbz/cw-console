//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

var catSpeeds = map[int]uint32{4800: syscall.B4800, 9600: syscall.B9600, 19200: syscall.B19200,
	38400: syscall.B38400, 115200: syscall.B115200}

func catBaudOK(baud int) bool { _, ok := catSpeeds[baud]; return ok }

// catRaw: raw 8N1 at baud; reads return after 0.1 s without data.
func catRaw(fd, baud int) error {
	sp := catSpeeds[baud]
	var t syscall.Termios
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL | sp
	t.Ispeed, t.Ospeed = sp, sp
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 0, 1
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

func catFlush(fd int) { syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x540B /* TCFLSH */, 2) }
