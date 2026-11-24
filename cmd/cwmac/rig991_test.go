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

// fake991: a pty standing in for the FT-991A's Enhanced COM port at its factory 4800
// baud. Silent at any other rate, like the real radio (whose answers would be garbage).
// Split is set with FT2/FT3 and read back 0/1 (FT0/FT1 toggle transmit on this radio, so
// the fake fails them); the clarifier is RT/XT/RC/RU/RD, read from IF; CF doesn't exist.
type fake991 struct {
	mu      sync.Mutex
	v       map[string]string
	clar    int
	cmds    []string
	toggled string // an FT0/FT1 the app should never send
}

func (f *fake991) get(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v[k]
}

func (f *fake991) sent(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func startFake991(t *testing.T) (string, *fake991) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { m.Close() })
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	port := filepath.Join(t.TempDir(), "usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_Y-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	f := &fake991{v: map[string]string{"FA": "007030000", "FB": "007040000", "MD0": "7", "FT": "0", "RT": "0", "XT": "0",
		"PC": "100", "BI": "0", "ID": "0670", "RG0": "255", "AG0": "040", "EX060": "0",
		"EX056": "0", "SD": "0200", "SH0": "10", "CO02": "0000", "IS0": "+0000", "KP": "30", "KS": "020",
		"BC0": "0", "BP00": "000", "BP01": "100"}}
	at4800 := func() bool {
		var tio syscall.Termios
		syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&tio)))
		return tio.Cflag&0o10017 == syscall.B4800 // CBAUD
	}
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
			if !at4800() {
				acc = ""
				continue // wrong rate: nothing intelligible comes back
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

func (f *fake991) handle(cmd string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	body := strings.TrimSuffix(cmd, ";")
	switch {
	case body == "IF":
		return fmt.Sprintf("IF001%s%+05d%s%s%s0000;", f.v["FA"], f.clar, f.v["RT"], f.v["XT"], f.v["MD0"])
	case body == "RC":
		f.clar = 0
		return ""
	case strings.HasPrefix(body, "RU") && len(body) == 6:
		d, _ := strconv.Atoi(body[2:])
		f.clar += d
		return ""
	case strings.HasPrefix(body, "RD") && len(body) == 6:
		d, _ := strconv.Atoi(body[2:])
		f.clar -= d
		return ""
	case body == "FT2":
		f.v["FT"] = "0"
		return ""
	case body == "FT3":
		f.v["FT"] = "1"
		return ""
	case body == "FT0" || body == "FT1":
		f.toggled = body
		return ""
	case strings.HasPrefix(body, "CF"):
		return "?;" // the FT-991A has no CF
	case strings.HasPrefix(body, "BS") && len(body) == 4:
		// The band key recalls the band stack: SSB somewhere up the band.
		fa, ok := map[string]string{"05": "014250000", "15": "144200000", "16": "432100000"}[body[2:]]
		if !ok {
			return "?;"
		}
		f.v["FA"], f.v["MD0"] = fa, "2"
		return ""
	case body == "AC002":
		f.v["TUNE"] = "1"
		return ""
	case body == "RM5":
		if po := f.v["PO"]; po != "" {
			return "RM5" + po + ";" // transmitting
		}
		return "RM5000;"
	}
	for _, k := range []string{"EX060", "EX056", "MD0", "RG0", "AG0", "FA", "FB", "FT", "PC", "BI", "ID", "RT", "XT",
		"SD", "SH0", "CO02", "IS0", "KP", "KS", "BC0", "BP00", "BP01"} {
		if strings.HasPrefix(body, k) {
			if rest := body[len(k):]; rest != "" {
				if k == "PC" && f.v["FA"] >= "144000000" && rest > "050" {
					return "?;" // 50 W at most on 2 m and 70 cm
				}
				f.v[k] = rest
				return ""
			}
			return k + f.v[k] + ";"
		}
	}
	return "?;"
}

func testEngine991(t *testing.T) (*Engine, *fake991) {
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = "" })
	port, f := startFake991(t)
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(fmt.Sprintf(`{"catPort":%q}`, port)))
	return e, f
}

func TestRigPanelFindsAnFT991AAt4800(t *testing.T) {
	e, f := testEngine991(t)
	r := e.rigState()
	if r.Error != "" || r.State == nil {
		t.Fatalf("state: %+v", r)
	}
	if r.Model != "FT-991A" || r.Baud != 4800 || r.State.FreqA != 7_030_000 || r.State.ModeName != "CW-L" || r.State.PowerW != 100 {
		t.Fatalf("state: %+v %+v", r, *r.State)
	}
	if b := r.Bands; len(b) != 13 || b[11] != "2m" || b[12] != "70cm" {
		t.Fatalf("bands %v", b)
	}
	// Menu 060 PC KEYING ships OFF: no key-line CW until it's DTR (the app's line).
	if r.PCKeying == nil || *r.PCKeying != 0 || !strings.Contains(r.Warning, "PC KEYING is OFF") || !strings.Contains(r.Warning, "DTR") {
		t.Fatalf("PC keying: %v %q", r.PCKeying, r.Warning)
	}
	if r := e.doRig(rigAction{"pcKeying", "3"}); r.Error != "" || f.get("EX060") != "3" || r.Warning != "" {
		t.Fatalf("set PC KEYING to DTR: %q, EX060 %s, warning %q", r.Error, f.get("EX060"), r.Warning)
	}
	if f.sent("CF") || f.toggled != "" {
		t.Fatalf("FT-710 commands sent: CF %v, toggle %q", f.sent("CF"), f.toggled)
	}
}

func TestFT991ABandKeysVHF(t *testing.T) {
	e, f := testEngine991(t)
	// 2 m: the band stack had 144.200 USB; the key lands in the CW segment, in CW.
	if r := e.doRig(rigAction{"band", "2m"}); r.Error != "" || f.get("FA") != "144075000" || f.get("MD0") != "3" || r.MaxPowerW != 50 {
		t.Fatalf("2 m: FA %s MD0 %s %+v", f.get("FA"), f.get("MD0"), r)
	}
	if r := e.doRig(rigAction{"power", "100"}); r.Error == "" {
		t.Fatal("100 W on 2 m: refused before it reaches the radio")
	}
	if r := e.doRig(rigAction{"power", "50"}); r.Error != "" || f.get("PC") != "050" {
		t.Fatalf("50 W on 2 m: %s, PC %s", r.Error, f.get("PC"))
	}
	if r := e.doRig(rigAction{"tune", ""}); r.Error == "" || f.get("TUNE") != "" {
		t.Fatal("the tuner is HF and 6 m")
	}
	if r := e.doRig(rigAction{"band", "70cm"}); r.Error != "" || f.get("FA") != "432085000" || f.get("MD0") != "3" {
		t.Fatalf("70 cm: FA %s MD0 %s %s", f.get("FA"), f.get("MD0"), r.Error)
	}
	if r := e.doRig(rigAction{"freqA", "146520000"}); r.Error != "" || f.get("FA") != "146520000" {
		t.Fatalf("146.52: %s", r.Error)
	}
	if r := e.doRig(rigAction{"band", "20m"}); r.Error != "" || f.get("FA") != "014035000" || f.get("MD0") != "3" {
		t.Fatalf("20 m: FA %s MD0 %s", f.get("FA"), f.get("MD0"))
	}
	if r := e.doRig(rigAction{"tune", ""}); r.Error != "" || f.get("TUNE") != "1" {
		t.Fatalf("tuner on 20 m (AC002): %s", r.Error)
	}
}

func TestFT991ASplitAndClarifier(t *testing.T) {
	e, f := testEngine991(t)
	if r := e.doRig(rigAction{"split", "1"}); r.Error != "" || f.get("FT") != "1" || !r.State.Split {
		t.Fatalf("split on: %s FT %s", r.Error, f.get("FT"))
	}
	if r := e.doRig(rigAction{"split", "0"}); r.Error != "" || f.get("FT") != "0" || r.State.Split {
		t.Fatalf("split off: %s", r.Error)
	}
	if r := e.doRig(rigAction{"clarRx", "1"}); r.Error != "" || f.get("RT") != "1" || !r.State.ClarRX {
		t.Fatalf("RX clarifier: %s", r.Error)
	}
	if r := e.doRig(rigAction{"clarOffset", "120"}); r.Error != "" || r.State.ClarHz != 120 || r.State.RXFreq != 7_030_120 {
		t.Fatalf("offset +120: %s %+v", r.Error, r.State)
	}
	if r := e.doRig(rigAction{"clarOffset", "-50"}); r.Error != "" || r.State.ClarHz != -50 {
		t.Fatalf("offset -50 (cleared, then down): %s %+v", r.Error, r.State)
	}
	if r := e.doRig(rigAction{"clarTx", "1"}); r.Error != "" || f.get("XT") != "1" || f.get("RT") != "1" {
		t.Fatalf("TX clarifier keeps RX: %s RT %s XT %s", r.Error, f.get("RT"), f.get("XT"))
	}
	if f.sent("CF") || f.toggled != "" {
		t.Fatalf("FT-710 commands sent: CF %v, toggle %q", f.sent("CF"), f.toggled)
	}
}

func TestRadioOffIsNotProbedEveryPoll(t *testing.T) {
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = "" })
	// A port that opens but never answers: a radio that's switched off.
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { m.Close() })
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	go func() { // drain, never answer
		buf := make([]byte, 256)
		for {
			if _, err := m.Read(buf); err != nil && errors.Is(err, os.ErrClosed) {
				return
			}
		}
	}()
	port := filepath.Join(t.TempDir(), "usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_Z-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(fmt.Sprintf(`{"catPort":%q}`, port)))
	t0 := time.Now()
	if _, err := e.ft710Spec(); err == nil {
		t.Fatal("no radio there")
	}
	first := time.Since(t0)
	t1 := time.Now()
	for i := 0; i < 20; i++ {
		e.ft710Spec()
	}
	if again := time.Since(t1); again > first/2 || again > 200*time.Millisecond {
		t.Fatalf("re-probed: first %v, 20 more %v", first, again)
	}
}
