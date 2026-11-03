//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// fakeAplay puts a script in place of aplay for the test.
func fakeAplay(t *testing.T, script string) {
	p := filepath.Join(t.TempDir(), "aplay")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := aplayPath
	aplayPath = p
	t.Cleanup(func() { aplayPath = old })
}

// recordingAplay: aplay that keeps what it's given in a file.
func recordingAplay(t *testing.T) string {
	out := filepath.Join(t.TempDir(), "pcm.raw")
	fakeAplay(t, `exec cat > "`+out+`"`)
	return out
}

type fakePTT struct {
	mu sync.Mutex
	ev []string
}

func (p *fakePTT) Key(on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.ev = append(p.ev, "on")
	} else if len(p.ev) > 0 && p.ev[len(p.ev)-1] == "on" {
		p.ev = append(p.ev, "off")
	}
	return nil
}

func (p *fakePTT) Close() {
	p.mu.Lock()
	p.ev = append(p.ev, "close")
	p.mu.Unlock()
}

func (p *fakePTT) events() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.ev, ",")
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// feed hands text to a sender the way tx.go does: a character when it wants one.
func feed(k cwSender, text string, stop chan struct{}) {
	for _, c := range text {
		for !k.Wants() {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		k.Feed(c)
	}
}

// readPCM: channel 0 of S16LE audio with ch channels.
func readPCM(t *testing.T, path string, ch int) []int16 {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b)%(2*ch) != 0 {
		t.Fatalf("%d bytes isn't whole frames of %d channels", len(b), ch)
	}
	out := make([]int16, len(b)/(2*ch))
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*ch*i:]))
		for c := 1; c < ch; c++ {
			if v := int16(binary.LittleEndian.Uint16(b[2*ch*i+2*c:])); v != out[i] {
				t.Fatalf("channel %d differs at %d", c, i)
			}
		}
	}
	return out
}

// tones: [start, end) of each keyed element (runs of sound with no gap of 1 ms or more).
func tones(pcm []int16, rate int) [][2]int {
	var segs [][2]int
	gap := rate / 1000
	start, last := -1, -1
	for i, v := range pcm {
		if v == 0 {
			continue
		}
		if start >= 0 && i-last > gap {
			segs = append(segs, [2]int{start, last + 1})
			start = -1
		}
		if start < 0 {
			start = i
		}
		last = i
	}
	if start >= 0 {
		segs = append(segs, [2]int{start, last + 1})
	}
	return segs
}

func TestAudioKeyerSendsE(t *testing.T) {
	out := recordingAplay(t)
	ptt := &fakePTT{}
	var pmu sync.Mutex
	var pauses []bool
	pause := func(p bool) { pmu.Lock(); pauses = append(pauses, p); pmu.Unlock() }
	k := newAudioKeyer(ptt, "plughw:CARD=Test,DEV=0", "plug", pause, 700, 0.5, 16)
	k.Feed('e')
	select {
	case c := <-k.Sent():
		if c != 'E' {
			t.Fatalf("echo %q", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no echo")
	}
	if !k.Active(0) {
		t.Fatal("not active while sending")
	}
	waitFor(t, 5*time.Second, "the over to end", func() bool { return !k.Active(0) })
	if e := ptt.events(); e != "on,off" {
		t.Fatalf("PTT %s", e)
	}
	k.Close()
	if e := ptt.events(); e != "on,off,close" {
		t.Fatalf("PTT after close %s", e)
	}
	pmu.Lock()
	if fmt.Sprint(pauses) != "[true false]" {
		t.Fatalf("receive paused %v", pauses)
	}
	pmu.Unlock()
	pcm := readPCM(t, out, 1)
	segs := tones(pcm, 48000)
	if len(segs) != 1 {
		t.Fatalf("%d elements, want 1", len(segs))
	}
	s := segs[0]
	if n := s[1] - s[0]; n < 3600-12 || n > 3600 { // a dit at 16 WPM: 75 ms (the ramps' first samples round to 0)
		t.Fatalf("dit %d samples, want 3600", n)
	}
	// keyed only after the audio was flowing, and with time for the T/R relay
	if s[0] < int((akPTTDelay+akRelay).Seconds()*48000) {
		t.Fatalf("tone starts %d samples in", s[0])
	}
	peak, cross := 0, 0
	for i := s[0]; i < s[1]; i++ {
		peak = max(peak, int(math.Abs(float64(pcm[i]))))
		if i > s[0] && pcm[i-1] < 0 && pcm[i] >= 0 {
			cross++
		}
	}
	if peak < 15900 || peak > 16000 { // gain 0.5
		t.Fatalf("peak %d", peak)
	}
	if f := float64(cross) / 0.075; math.Abs(f-700) > 20 {
		t.Fatalf("tone %.0f Hz", f)
	}
	// shaped: no clicks (the first and last millisecond are still low)
	for _, i := range []int{s[0] + 48, s[1] - 48} {
		if math.Abs(float64(pcm[i])) > 0.15*16000 {
			t.Fatalf("hard edge at %d: %d", i, pcm[i])
		}
	}
	// after the element: silence to the end (the hang)
	if tail := len(pcm) - s[1]; tail < int(0.3*48000) {
		t.Fatalf("only %d samples after the element", tail)
	}
}

func TestAudioKeyerParis(t *testing.T) {
	out := recordingAplay(t)
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "plughw:CARD=Test,DEV=0", "hw44", nil, 600, 1, 20)
	defer k.Close()
	go feed(k, "PARIS ", nil)
	var echo []rune
	waitFor(t, 8*time.Second, "PARIS", func() bool {
		for {
			select {
			case c := <-k.Sent():
				echo = append(echo, c)
				continue
			default:
			}
			break
		}
		return len(echo) == 6 && !k.Active(0)
	})
	if string(echo) != "PARIS " {
		t.Fatalf("echo %q", string(echo))
	}
	if e := ptt.events(); e != "on,off" { // one over
		t.Fatalf("PTT %s", e)
	}
	pcm := readPCM(t, out, 2) // 44.1 kHz stereo direct
	segs := tones(pcm, 44100)
	if len(segs) != 14 { // .--. .- .-. .. ...
		t.Fatalf("%d elements, want 14", len(segs))
	}
	// PARIS is 50 dits with the word gap: 43 from the first element's start to the last's end
	want := 43 * 0.060 * 44100
	if got := float64(segs[13][1] - segs[0][0]); math.Abs(got-want) > 15 {
		t.Fatalf("PARIS took %.0f samples, want %.0f", got, want)
	}
	dah := float64(segs[1][1] - segs[1][0]) // P: dit dah dah dit
	if math.Abs(dah-3*0.060*44100) > 4 {
		t.Fatalf("dah %.0f samples", dah)
	}
}

func TestAudioKeyerAbort(t *testing.T) {
	recordingAplay(t)
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "plughw:CARD=Test,DEV=0", "plug", nil, 700, 0.5, 20)
	defer k.Close()
	stop, fed := make(chan struct{}), make(chan struct{})
	go func() { feed(k, "CQ CQ CQ POTA DE KN0BLE KN0BLE K", stop); close(fed) }()
	select {
	case <-k.Sent():
	case <-time.After(3 * time.Second):
		t.Fatal("no echo")
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-fed // nothing fed after the abort (on the box the sender stops feeding as it aborts)
	for len(k.Sent()) > 0 {
		<-k.Sent()
	}
	at := time.Now()
	k.Abort()
	waitFor(t, time.Second, "the abort", func() bool { return !k.Active(0) })
	if d := time.Since(at); d > 300*time.Millisecond {
		t.Fatalf("abort took %v", d)
	}
	if e := ptt.events(); e != "on,off" {
		t.Fatalf("PTT %s", e)
	}
	if k.Busy() {
		t.Fatal("busy after abort")
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(k.Sent()); n > 1 { // what was due on the air after the abort isn't echoed
		t.Fatalf("%d echoes after the abort", n)
	}
}

func TestAudioKeyerSoundCardFails(t *testing.T) {
	fakeAplay(t, `echo "aplay: main:831: audio open error: No such device" >&2; exit 1`)
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "plughw:CARD=Test,DEV=0", "plug", nil, 700, 0.5, 20)
	defer k.Close()
	k.Feed('T')
	select {
	case err := <-k.Errors():
		if !strings.Contains(err.Error(), "No such device") {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no error")
	}
	if e := ptt.events(); e != "" && e != "on,off" {
		t.Fatalf("PTT %s", e)
	}
	if k.Busy() {
		t.Fatal("still busy")
	}
}

// a sound card that stops taking audio (aplay stuck): the rig is keyed down, not left on
func TestAudioKeyerSoundCardStuck(t *testing.T) {
	fakeAplay(t, `exec sleep 30`)
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "plughw:CARD=Test,DEV=0", "plug", nil, 700, 0.5, 20)
	defer k.Close()
	go feed(k, "TEST TEST TEST TEST", nil)
	start := time.Now()
	select {
	case err := <-k.Errors():
		if !strings.Contains(err.Error(), "stopped taking audio") {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no error")
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("took %v to notice", d)
	}
	if e := ptt.events(); e != "on,off" {
		t.Fatalf("PTT %s", e)
	}
}

func TestAudioCWFrequencies(t *testing.T) {
	if d := audioCWDial(14050000, 700); d != 14049300 {
		t.Fatal(d)
	}
	for _, c := range []struct {
		mode string
		want int
	}{{"2", 14050000}, {"2D", 14050000}, {"C", 14050000}, {"1", 14048600}, {"3", 14049300}} {
		if got := audioCWSignal(c.mode, 14049300, 700); got != c.want {
			t.Fatalf("%s: %d, want %d", c.mode, got, c.want)
		}
	}
	if effectivePTT("audio", "") != "cat" || effectivePTT("audio", "rts") != "rts" {
		t.Fatal("PTT line")
	}
	if h := akHang(16); h != 625*time.Millisecond {
		t.Fatal(h)
	}
	if h := akHang(40); h != 400*time.Millisecond {
		t.Fatal(h)
	}
}

// fakeFX: a pty standing in for the FX-4CR's CAT (Kenwood): mode, data, frequency and
// TX state that the commands set and the queries read.
type fakeFX struct {
	mu   sync.Mutex
	mode string
	data string
	hz   int
	tx   bool
	log  []string
}

func newFakeFX(t *testing.T, mode string, hz int) (string, *fakeFX) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { m.Close() })
	var n uint32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n)))
	var u int32
	syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&u)))
	port := filepath.Join(t.TempDir(), "usb-1a86_USB_Serial-if00-port0")
	os.Symlink("/dev/pts/"+strconv.Itoa(int(n)), port)
	r := &fakeFX{mode: mode, data: "0", hz: hz}
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
				r.mu.Lock()
				r.log = append(r.log, cmd)
				arg := strings.TrimSuffix(cmd[2:], ";")
				switch {
				case cmd == "FA;":
					m.Write([]byte(fmt.Sprintf("FA%011d;", r.hz)))
				case cmd == "MD;":
					m.Write([]byte("MD" + r.mode + ";"))
				case cmd == "DA;":
					m.Write([]byte("DA" + r.data + ";"))
				case strings.HasPrefix(cmd, "FA"):
					r.hz, _ = strconv.Atoi(arg)
				case strings.HasPrefix(cmd, "MD"):
					r.mode = arg
				case strings.HasPrefix(cmd, "DA"):
					r.data = arg
				case cmd == "TX1;":
					r.tx = true
				case cmd == "RX;":
					r.tx = false
				}
				r.mu.Unlock()
			}
		}
	}()
	return port, r
}

func (r *fakeFX) state() (string, string, int, bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mode, r.data, r.hz, r.tx, strings.Join(r.log, "")
}

// The whole path on the box: Key line "Audio tone" with an FX-4CR in CW mode.
func TestAudioCWOnTheFX(t *testing.T) {
	appliance = true
	defer func() { appliance = false }()
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	out := recordingAplay(t)
	port, fx := newFakeFX(t, "3", 14050000)
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(`{"txPort":"` + port + `","txLine":"audio","ft8PttLine":"cat","rigCtl":true,"pitch":700,"ft8Level":100,"wpm":20}`))
	e.cap.lastDev = "plughw:CARD=Device,DEV=0"
	e.mu.Lock()
	err := e.tx.enableLocked(true, port, "audio")
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// the rig was in CW: now USB, the dial a pitch lower, the signal where it was
	waitFor(t, 3*time.Second, "the rig in USB", func() bool { _, _, hz, _, _ := fx.state(); return hz != 14050000 })
	if m, d, hz, _, _ := fx.state(); m != "2" || d != "0" || hz != 14049300 {
		t.Fatalf("rig %s DA%s %d", m, d, hz)
	}
	waitFor(t, time.Second, "the log frequency", func() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.st.Freq == "14.050" })
	e.mu.Lock()
	e.tx.typedLocked("tu")
	e.mu.Unlock()
	keyed := false
	waitFor(t, 6*time.Second, "TU to go out", func() bool {
		e.mu.Lock()
		e.tx.tickLocked()
		sent, busy := string(e.tx.sentTail), e.tx.busyLocked()
		e.mu.Unlock()
		if _, _, _, tx, _ := fx.state(); tx {
			keyed = true
		}
		return sent == "TU" && !busy
	})
	if !keyed {
		t.Fatal("the rig was never keyed")
	}
	if _, _, _, tx, log := fx.state(); tx || !strings.Contains(log, "TX1;") || !strings.HasSuffix(log, "RX;") {
		t.Fatalf("rig commands %s", log)
	}
	if segs := tones(readPCM(t, out, 1), 48000); len(segs) != 4 { // - ..-
		t.Fatalf("TU in %d elements", len(segs))
	}
	// the band list: USB, a pitch below the band's CW frequency
	e.rigToCWBand("40m")
	waitFor(t, 3*time.Second, "40 m", func() bool { _, _, hz, _, _ := fx.state(); return hz == 7049300 })
	waitFor(t, time.Second, "the log frequency", func() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.st.Freq == "7.050" })
	if m, _, _, _, _ := fx.state(); m != "2" {
		t.Fatalf("40 m in mode %s", m)
	}
	e.mu.Lock()
	e.tx.closeLocked()
	e.mu.Unlock()
	e.applySettings(json.RawMessage(`{"txLine":"bogus"}`))
	e.mu.Lock()
	line := e.st.TxLine
	e.mu.Unlock()
	if line != "dtr" {
		t.Fatalf("txLine %q", line)
	}
}
