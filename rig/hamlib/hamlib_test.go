package hamlib

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRigctld answers like rigctld for a radio on 14.230 MHz in PKTUSB, and records the
// commands it was sent.
func fakeRigctld(t *testing.T, replies map[string]string) (addr string, got func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var cmds []string
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		r := bufio.NewReader(c)
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			l = strings.TrimSpace(l)
			mu.Lock()
			cmds = append(cmds, l)
			mu.Unlock()
			rep, ok := replies[l]
			if !ok {
				rep = "RPRT 0\n"
				for k, v := range replies {
					if strings.HasSuffix(k, "*") && strings.HasPrefix(l, strings.TrimSuffix(k, "*")) {
						rep = v
					}
				}
			}
			c.Write([]byte(rep))
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cmds...) }
}

func TestReadsFrequencyAndMode(t *testing.T) {
	addr, _ := fakeRigctld(t, map[string]string{"f": "14230000\n", "m": "PKTUSB\n3000\n"})
	c, err := Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if hz, err := c.Freq(); err != nil || hz != 14230000 {
		t.Errorf("freq %d, %v", hz, err)
	}
	if m, err := c.Mode(); err != nil || m != "PKTUSB" || DisplayMode(m) != "DATA-U" {
		t.Errorf("mode %q, %v", m, err)
	}
}

func TestSetsAndKeys(t *testing.T) {
	addr, got := fakeRigctld(t, map[string]string{})
	c, _ := Dial(addr)
	defer c.Close()
	for _, err := range []error{c.SetFreq(7171000), c.SetMode(DataLSB), c.PTT(true), c.PTT(false)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"F 7171000", "M PKTLSB 0", "T 1", "T 0"}
	if g := got(); strings.Join(g, "|") != strings.Join(want, "|") {
		t.Errorf("sent %v, want %v", g, want)
	}
}

func TestErrorsAreReadable(t *testing.T) {
	addr, _ := fakeRigctld(t, map[string]string{"f": "RPRT -5\n", "T 1": "RPRT -11\n", "F*": "RPRT -4\n"})
	c, _ := Dial(addr)
	defer c.Close()
	if _, err := c.Freq(); err == nil || !strings.Contains(err.Error(), "communication error") {
		t.Errorf("freq error: %v", err)
	}
	if err := c.PTT(true); err == nil || !strings.Contains(err.Error(), "-11") {
		t.Errorf("ptt error: %v", err)
	}
	if err := c.SetFreq(1); err == nil || !strings.Contains(err.Error(), "doesn't support") {
		t.Errorf("set error: %v", err)
	}
}

func TestBadModeAndAddr(t *testing.T) {
	c := &Client{}
	if c.SetMode("US B") == nil || c.SetMode("") == nil {
		t.Error("bad mode accepted")
	}
	for a, want := range map[string]bool{"127.0.0.1:4532": true, "shack-pi.local:4532": true, "localhost:0": false,
		"4532": false, "http://x:1": false, "a b:1": false, ":4532": false, "h:99999": false} {
		if ValidAddr(a) != want {
			t.Errorf("ValidAddr(%q) = %v", a, !want)
		}
	}
}

// An error reply (the radio is off) leaves the connection fine; a dropped link or a command
// that timed out does not, because a late reply would be read as the answer to the next one.
func TestBrokenOnlyWhenTheLinkIs(t *testing.T) {
	addr, _ := fakeRigctld(t, map[string]string{"f": "RPRT -5\n"})
	c, _ := Dial(addr)
	defer c.Close()
	if _, err := c.Freq(); err == nil || !strings.Contains(err.Error(), "communication error") {
		t.Fatalf("freq: %v", err)
	}
	if c.Broken() {
		t.Error("an error reply from the radio marked the connection broken")
	}

	// rigctld goes away
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { c, _ := ln.Accept(); c.Close() }()
	d, err := Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Freq(); err == nil || !d.Broken() {
		t.Errorf("dropped link: err %v, broken %v", err, d.Broken())
	}
	ln.Close()

	// rigctld stops answering
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln2.Close()
	go func() {
		c, _ := ln2.Accept()
		bufio.NewReader(c).ReadString('\n') // takes the command, says nothing
		time.Sleep(time.Second)
		c.Close()
	}()
	e, _ := Dial(ln2.Addr().String())
	e.Timeout = 60 * time.Millisecond
	defer e.Close()
	if _, err := e.Freq(); err == nil || !e.Broken() {
		t.Errorf("timeout: err %v, broken %v", err, e.Broken())
	}
}
