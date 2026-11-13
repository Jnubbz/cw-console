//go:build darwin

package main

// macOS termios: speeds are plain numbers in c_ispeed/c_ospeed (no Bxxx bits in c_cflag),
// set with TIOCSETA; TIOCFLUSH drops pending input and output.

import (
	"syscall"
	"unsafe"
)

func catBaudOK(baud int) bool {
	switch baud {
	case 4800, 9600, 19200, 38400, 115200:
		return true
	}
	return false
}

// catRaw: raw 8N1 at baud; reads return after 0.1 s without data.
func catRaw(fd, baud int) error {
	var t syscall.Termios
	t.Cflag = syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	t.Ispeed, t.Ospeed = uint64(baud), uint64(baud)
	t.Cc[syscall.VMIN], t.Cc[syscall.VTIME] = 0, 1
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&t))); e != 0 {
		return e
	}
	return nil
}

// catFlush drops stale input (old answers); never output, which is commands not yet out.
func catFlush(fd int) {
	what := int32(0x1) // FREAD (<sys/fcntl.h>)
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCFLUSH, uintptr(unsafe.Pointer(&what)))
}
