//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// a pty standing in for the FX-4CR: answers FA; (and, with ky, accepts KY; refuses it
// with "?;" otherwise), records everything
func fakeKenwood(t *testing.T, ky bool) (string, *os.File, chan string) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	port := filepath.Join(t.TempDir(), "usb-1a86_USB_Serial-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	got := make(chan string, 100)
	go func() {
		buf := make([]byte, 256)
		var acc string
		for {
			k, err := m.Read(buf)
			if err != nil { // EIO while no one has the port open: wait for the next open
				if errors.Is(err, os.ErrClosed) {
					return
				}
				time.Sleep(5 * time.Millisecond)
				continue
			}
			acc += string(buf[:k])
			for {
				i := strings.IndexByte(acc, ';')
				if i < 0 {
					break
				}
				cmd := acc[:i+1]
				acc = acc[i+1:]
				switch {
				case cmd == "FA;":
					m.Write([]byte("FA00014040000;"))
				case strings.HasPrefix(cmd, "KY") && !ky:
					m.Write([]byte("?;"))
				}
				got <- cmd
			}
		}
	}()
	return port, m, got
}

func TestCATKeyer(t *testing.T) {
	port, m, got := fakeKenwood(t, true)
	defer m.Close()
	k, err := newCATKeyer(catSpecFor(port, ""), 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range "cq pota de kn0ble " {
		k.Feed(ch)
	}
	var cmds []string
	deadline := time.After(2 * time.Second)
	for len(cmds) < 3 {
		select {
		case c := <-got:
			cmds = append(cmds, c)
		case <-deadline:
			t.Fatalf("radio got %q", cmds)
		}
	}
	want := []string{"FA;", "KS016;", "KY CQ POTA DE KN0BLE ;"} // one command: it all arrived at once
	if strings.Join(cmds, "") != strings.Join(want, "") {
		t.Fatalf("radio got %q, want %q", cmds, want)
	}
	if !k.Busy() || !k.Active(0) {
		t.Fatal("not keying")
	}
	select { // the echo follows the predicted keying
	case ch := <-k.Sent():
		if ch != 'C' {
			t.Fatalf("first echo %q", ch)
		}
	case <-time.After(time.Second):
		t.Fatal("no echo")
	}
	// typed one word at a time, each word goes as it's finished
	for _, ch := range "TU " {
		k.Feed(ch)
	}
	if c := <-got; c != "KY TU ;" {
		t.Fatalf("typed word: %q", c)
	}
	k.Abort()
	if k.Busy() {
		t.Fatal("still busy after abort")
	}
	stop := []string{<-got, <-got}
	if stop[0] != "KY0;" || stop[1] != "RX;" {
		t.Fatalf("abort sent %q", stop)
	}
	k.Close()
}

func TestCATKeyerNotSupported(t *testing.T) {
	port, m, _ := fakeKenwood(t, false)
	defer m.Close()
	if _, err := newCATKeyer(catSpecFor(port, "yaesu"), 16); err == nil {
		t.Fatal("yaesu accepted")
	}
	k, err := newCATKeyer(catSpecFor(port, ""), 16)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	for _, ch := range "TEST " {
		k.Feed(ch)
	}
	select {
	case err := <-k.Errors():
		if !strings.Contains(err.Error(), "refused") {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a radio without KY wasn't noticed")
	}
}

func TestCATConsole(t *testing.T) {
	port, m, _ := fakeKenwood(t, false)
	defer m.Close()
	var r rigCtl
	c := catSpecFor(port, "")
	if a, err := r.raw(c, "FA"); err != nil || a != "FA00014040000;" {
		t.Fatal(a, err)
	}
	if a, _ := r.raw(c, "KY TEST;"); !strings.HasPrefix(a, "?;") {
		t.Fatal(a)
	}
	if a, _ := r.raw(c, "RX;"); a != "(no answer)" {
		t.Fatal(a)
	}
}
