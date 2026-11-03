//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// fake710: a pty standing in for the FT-710's Enhanced COM port. It keeps the settings
// the rig panel touches and answers their queries the way the radio does.
type fake710 struct {
	mu   sync.Mutex
	v    map[string]string // command prefix -> current value
	cmds []string
}

func (f *fake710) get(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v[k]
}

func startFake710(t *testing.T) (string, *fake710) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { m.Close() })
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	port := filepath.Join(t.TempDir(), "usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_X-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	f := &fake710{v: map[string]string{"FA": "014058000", "FB": "014060000", "MD0": "3", "FT": "0",
		"CF000": "00000", "CF001": "+0000", "PC": "050", "BI": "0", "ID": "0800"}}
	go func() {
		buf := make([]byte, 256)
		var acc string
		for {
			k, err := m.Read(buf)
			if err != nil {
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
				if a := f.handle(cmd); a != "" {
					m.Write([]byte(a))
				}
			}
		}
	}()
	return port, f
}

func (f *fake710) handle(cmd string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	body := strings.TrimSuffix(cmd, ";")
	for _, k := range []string{"CF000", "CF001", "MD0", "FA", "FB", "FT", "PC", "BI", "ID"} {
		if strings.HasPrefix(body, k) {
			if rest := body[len(k):]; rest != "" {
				f.v[k] = rest
				if k == "CF000" {
					f.v[k] = rest[:2] + "000"
				}
				return ""
			}
			return k + f.v[k] + ";"
		}
	}
	switch body {
	case "RM6":
		return "RM6026000;" // SWR 1.2
	case "RM5":
		return "RM5027;" // 5 W
	case "SV":
		f.v["FA"], f.v["FB"] = f.v["FB"], f.v["FA"]
		return ""
	case "AB":
		f.v["FB"] = f.v["FA"]
		return ""
	case "BA":
		f.v["FA"] = f.v["FB"]
		return ""
	case "RC":
		f.v["CF001"] = "+0000"
		return ""
	}
	return "?;"
}

func testEngine710(t *testing.T) (*Engine, *fake710) {
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = "" })
	port, f := startFake710(t)
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(fmt.Sprintf(`{"catPort":%q}`, port)))
	return e, f
}

func TestRigPanelReadsAndSetsTheFT710(t *testing.T) {
	e, f := testEngine710(t)
	r := e.rigState()
	if r.Error != "" || r.State == nil {
		t.Fatalf("state: %+v", r)
	}
	if r.State.FreqA != 14_058_000 || r.State.FreqB != 14_060_000 || r.State.ModeName != "CW-U" || r.State.PowerW != 50 {
		t.Fatalf("state: %+v", *r.State)
	}
	steps := []struct {
		a     rigAction
		check func() bool
	}{
		{rigAction{"split", "1"}, func() bool { return f.get("FT") == "1" }},
		{rigAction{"clarTx", "1"}, func() bool { return f.get("CF000") == "01000" }},
		{rigAction{"clarRx", "1"}, func() bool { return f.get("CF000") == "11000" }}, // TX stays on
		{rigAction{"clarOffset", "-120"}, func() bool { return f.get("CF001") == "-0120" }},
		{rigAction{"clarClear", ""}, func() bool { return f.get("CF001") == "+0000" }},
		{rigAction{"power", "25"}, func() bool { return f.get("PC") == "025" }},
		{rigAction{"freqA", "7030000"}, func() bool { return f.get("FA") == "007030000" }},
		{rigAction{"swap", ""}, func() bool { return f.get("FA") == "014060000" && f.get("FB") == "007030000" }},
		{rigAction{"atob", ""}, func() bool { return f.get("FB") == "014060000" }},
		{rigAction{"mode", "7"}, func() bool { return f.get("MD0") == "7" }},
	}
	for _, s := range steps {
		r := e.doRig(s.a)
		if r.Error != "" || !s.check() {
			t.Fatalf("%+v: reply %+v, rig %v", s.a, r, f.v)
		}
	}
	if r := e.doRig(rigAction{"power", "500"}); r.Error == "" {
		t.Fatal("500 W should be refused")
	}
	if r := e.doRig(rigAction{"warp", ""}); r.Error == "" {
		t.Fatal("unknown action should be refused")
	}
}

func TestRigPanelSaysWhenTheRigIsKenwood(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(`{"txPort":"/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0"}`))
	if r := e.rigState(); !strings.Contains(r.Error, "FT-710") {
		t.Fatalf("got %+v", r)
	}
}

func TestSWRSweepOnTheKeyLine(t *testing.T) {
	t.Setenv("CWMAC_TEST", "1")
	e, f := testEngine710(t)
	if err := e.startSweep(sweepRequest{Band: "30m", StepHz: 10_000}); err == nil {
		t.Fatal("a sweep without Transmit enabled should be refused")
	}
	e.mu.Lock()
	err := e.tx.enableLocked(true, "test:keyline", "dtr")
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.startSweep(sweepRequest{Band: "30m", StepHz: 10_000, PowerW: 50}); err == nil {
		t.Fatal("50 W should be refused")
	}
	if err := e.startSweep(sweepRequest{Band: "30m", StepHz: 10_000}); err != nil {
		t.Fatal(err)
	}
	if r := e.doRig(rigAction{"power", "100"}); r.Error == "" {
		t.Fatal("rig buttons should wait for the sweep")
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.sweepRunning() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s := e.sweepStatus()
	if s.Running || s.Error != "" || len(s.Points) != 6 || s.Best == nil || s.Best.SWR < 1.19 || s.Best.SWR > 1.21 {
		t.Fatalf("sweep: %+v", s)
	}
	if lastTestLine.downs != 6 || lastTestLine.ups < 6 {
		t.Fatalf("key line: %d downs, %d ups", lastTestLine.downs, lastTestLine.ups)
	}
	// back as it was
	if f.get("FA") != "014058000" || f.get("MD0") != "3" || f.get("PC") != "050" || f.get("BI") != "0" || f.get("FT") != "0" {
		t.Fatalf("rig not restored: %v", f.v)
	}
	e.mu.Lock()
	held := e.tx.sweeping
	e.mu.Unlock()
	if held {
		t.Fatal("send queue still held")
	}
}

func TestLikelyRigPort(t *testing.T) {
	for p, want := range map[string]bool{
		"/dev/cu.SLAB_USBtoUART": true, "/dev/cu.usbserial-1420": true, "/dev/ttyUSB0": true,
		"/dev/cu.Bluetooth-Incoming-Port": false, "gpio:17": false, "/dev/cu.debug-console": false,
	} {
		if likelyRigPort(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}
