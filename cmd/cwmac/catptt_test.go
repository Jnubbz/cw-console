//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

func TestCATPTT(t *testing.T) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	defer m.Close()
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	d := t.TempDir()
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), filepath.Join(d, "usb-Silicon_Labs_CP2105-if00-port0"))
	l, err := openPTT(filepath.Join(d, "usb-Silicon_Labs_CP2105-if01-port0"), "cat", "")
	if err != nil {
		t.Fatal(err)
	}
	l.Key(true)
	l.Key(false)
	buf := make([]byte, 64)
	k, _ := m.Read(buf)
	if got := string(buf[:k]); got != "TX1;TX0;" && got != "TX1;" {
		t.Fatalf("rig got %q", got)
	}
	l.Close()
}

// The FX-4CR (Kenwood TS-590S CAT on its CH340 port): TX1; to transmit the USB audio, RX;
// to receive, on the key port itself.
func TestCATPTTKenwood(t *testing.T) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	defer m.Close()
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	d := t.TempDir()
	port := filepath.Join(d, "usb-1a86_USB_Serial-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	l, err := openPTT(port, "cat", "")
	if err != nil {
		t.Fatal(err)
	}
	l.Key(true)
	l.Key(false)
	buf := make([]byte, 64)
	k, _ := m.Read(buf)
	if got := string(buf[:k]); got != "TX1;RX;" && got != "TX1;" {
		t.Fatalf("rig got %q", got)
	}
	l.Close()
}
