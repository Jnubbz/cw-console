package main

// FT8 transmit (the Pi box). The audio is WSJT-X's own: the ft8tx helper (WSJT-X's
// genft8 + gen_ft8wave, 48 kHz, as the WSJT-X program makes it) writes the waveform;
// the box keys the rig (DTR/RTS on the key port: FT-710 menu PSK/DATA > RPTT SELECT)
// and plays the audio into the rig's USB sound card, starting 0.5 s into the slot like
// WSJT-X. The sequencer (ft8seq.go) decides what to send.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ft8txPath = envOr("CWBOX_FT8TX", "/usr/bin/ft8tx")
	aplayPath = envOr("CWBOX_APLAY", "aplay")
)

const (
	ft8TxLead    = 100 * time.Millisecond // + recorder pause + pre-roll: the signal starts ~0.6 s in
	ft8PreRoll   = 400 * time.Millisecond // silence played before the signal; PTT goes on half way
	ft8PTTDelay  = 200 * time.Millisecond
	ft8TxLateMax = 1800 * time.Millisecond // later than this into the slot: wait for the next one
	// (a reply that starts late still decodes: the FT8 decoder looks for signals up to
	// 2.5 s off the usual start; ours then shows a DT of up to about +1.8 s)
	ft8TxWatchdog = 14 * time.Second
	ft8Idle       = 6 * time.Minute // like WSJT-X's Tx watchdog: CQ stops without operator action
)

type ft8Tx struct {
	mu sync.Mutex

	seq      ft8Seq
	enabled  bool
	line     keyLine
	port     string
	lineName string // "dtr" | "rts" | "cat"

	// from the engine's settings
	dev     string // ALSA device of the rig (same as receive)
	freq    int    // audio frequency, Hz
	level   float64
	out     string  // audio format to the rig: "" (48 kHz mono, ALSA converts), "hw48", "hw44"
	diag    string  // what the kernel said about the last transmission's playback
	off     float64 // the receiver's clock correction, s (slots are numbered after it)
	catType string  // the radio's CAT command set ("" auto, "yaesu", "kenwood")

	// prepared audio
	wave     []float32 // 48 kHz, unit amplitude
	waveMsg  string
	waveFreq int
	waveSent string // the message as it will decode (WSJT-X's unpack)
	genBusy  bool
	genErr   string

	active   bool
	abort    chan struct{}
	txSlots  map[int64]bool // slot numbers (unix/15) we transmitted in
	lastTx   int64
	lastSent string
	status   string

	onLog    func(l *ft8Log, manual bool)
	loggedAt map[string]time.Time // call -> when it was logged

	touched time.Time // last operator action (CQ, call, transmit on)
	tuning  bool
	pauseRx func(bool) // stop/restart recording around a transmission
}

func ft8TxAvailable() bool {
	st, err := os.Stat(ft8txPath)
	return err == nil && st.Mode().IsRegular()
}

func slotNum(t time.Time) int64 { return t.Unix() / 15 }

// transmittedIn reports whether we transmitted in the slot starting at t (true UTC).
func (x *ft8Tx) transmittedIn(t time.Time) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.txSlots[slotNum(t)]
}

func (x *ft8Tx) enable(on bool, port, line string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.stopLocked()
	if x.line != nil {
		x.line.Close()
		x.line = nil
	}
	x.enabled = false
	if !on {
		return nil
	}
	if !ft8TxAvailable() {
		return errors.New("this box has no FT8 transmitter (update it)")
	}
	if port == "" {
		return errors.New("pick the radio's key port first (Setup > Radio > Key port): FT8 uses it for PTT")
	}
	l, err := openPTT(port, line, x.catType)
	if err != nil {
		return err
	}
	x.line, x.port, x.lineName, x.enabled = l, port, line, true
	x.touched, x.status = time.Now(), ""
	return nil
}

func (x *ft8Tx) stopLocked() {
	if x.abort != nil {
		close(x.abort)
		x.abort = nil
	}
	x.seq.Stop()
}

// heard: decodes of a receive slot, from the receiver (early and full).
func (x *ft8Tx) heard(slot string, decs []ft8Decode) {
	x.mu.Lock()
	l := x.seq.Heard(slot, decs)
	cb := x.onLog
	x.mu.Unlock()
	if l != nil && cb != nil {
		cb(l, false)
	}
}

// logCandidate: the contact to log by hand, unless it was logged in the last 15 minutes.
func (x *ft8Tx) logCandidate() (*ft8Log, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.seq.Candidate()
	if l == nil || l.Call == "" {
		return nil, errors.New("no FT8 contact to log yet")
	}
	if t, ok := x.loggedAt[l.Call]; ok && time.Since(t) < 15*time.Minute {
		return nil, fmt.Errorf("%s is already logged", l.Call)
	}
	c := *l
	return &c, nil
}

// noteLogged: remember a logged contact (so it can't be logged twice by hand).
func (x *ft8Tx) noteLogged(call string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.loggedAt == nil {
		x.loggedAt = map[string]time.Time{}
	}
	x.loggedAt[call] = time.Now()
}

// tick runs from the engine's ticker: prepares the next message's audio and starts
// the transmission on time.
func (x *ft8Tx) tick(off float64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.off = off
	if !x.enabled || x.active || x.tuning {
		return
	}
	if x.seq.step == stCQ && time.Since(x.touched) > ft8Idle {
		x.seq.Stop()
		x.status = "Stopped calling CQ: 6 minutes without you touching anything (press CQ to go on)"
		return
	}
	msg := x.seq.Next()
	if msg == "" {
		return
	}
	if (msg != x.waveMsg || x.freq != x.waveFreq) && !x.genBusy {
		x.genBusy = true
		go x.generate(msg, x.freq)
		return
	}
	if msg != x.waveMsg || x.wave == nil {
		return
	}
	trueNow := ft8Clock().Add(-time.Duration(off * float64(time.Second)))
	cur := trueNow.Truncate(ft8Slot)
	into := trueNow.Sub(cur)
	n := slotNum(cur)
	if int(n%2) != x.seq.parity || n == x.lastTx || into < ft8TxLead || into > ft8TxLateMax {
		return
	}
	if x.dev == "" {
		x.status = "No rig audio device to transmit on"
		return
	}
	if err := x.checkLineLocked(); err != nil {
		x.seq.Stop()
		x.status = err.Error()
		return
	}
	x.lastTx = n
	if x.txSlots == nil || len(x.txSlots) > 100 {
		x.txSlots = map[int64]bool{}
	}
	x.txSlots[n] = true
	x.active = true
	x.abort = make(chan struct{})
	x.lastSent = x.waveSent
	go x.transmit(x.wave, x.level, x.dev, x.line, x.abort, 14800*time.Millisecond-into, false) // done before the slot ends
}

func (x *ft8Tx) generate(msg string, freq int) {
	w, sent, err := ft8Wave(msg, freq)
	x.mu.Lock()
	defer x.mu.Unlock()
	x.genBusy = false
	x.waveMsg, x.waveFreq = msg, freq
	if err != nil {
		x.wave, x.genErr = nil, err.Error()
		return
	}
	x.wave, x.waveSent, x.genErr = w, sent, ""
}

// ft8Wave runs WSJT-X's generator: 79 symbols at 48 kHz, and the text as it will decode.
func ft8Wave(msg string, freq int) ([]float32, string, error) {
	dir := filepath.Join(ft8Dir, "tx")
	os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, "wave.f32")
	b, err := exec.Command(ft8txPath, msg, strconv.Itoa(freq), out).Output()
	sent := ""
	for _, l := range strings.Split(string(b), "\n") {
		if s, ok := strings.CutPrefix(l, "SENT "); ok {
			sent = strings.TrimSpace(s)
		}
	}
	if err != nil || strings.HasPrefix(sent, "***") {
		return nil, sent, fmt.Errorf("can't make an FT8 message of %q", msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		return nil, sent, err
	}
	w := make([]float32, len(raw)/4)
	for i := range w {
		w[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return w, sent, nil
}

// transmit keys the rig and plays the audio. late: how far past the normal start we
// are (up to ~1.7 s; receivers still decode it, with a larger DT).
func (x *ft8Tx) transmit(wave []float32, level float64, dev string, line keyLine, abort chan struct{}, maxDur time.Duration, markAll bool) {
	// receive skips every slot a Tune or test transmits in (a 30 s Tune covers three).
	// (An FT8 transmission's own slot is marked when it starts.) Slots are numbered on
	// the corrected clock, as the receiver numbers them.
	mark := func() {
		if !markAll {
			return
		}
		x.mu.Lock()
		if x.txSlots == nil || len(x.txSlots) > 100 {
			x.txSlots = map[int64]bool{}
		}
		x.txSlots[slotNum(ft8Clock().Add(-time.Duration(x.off*float64(time.Second))))] = true
		x.mu.Unlock()
	}
	mark()
	stopMark := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopMark:
				return
			case <-t.C:
				mark()
			}
		}
	}()
	defer func() {
		close(stopMark)
		mark()
		line.Key(false)
		x.mu.Lock()
		x.active = false
		x.mu.Unlock()
	}()
	if wave != nil {
		setPlaybackVolume(dev)
	}
	// The audio stream is started first (silence), and the rig keyed only once it's
	// flowing, as WSJT-X does: the FT-710's C-Media sound card falls over when its
	// playback starts while the rig is already transmitting.
	x.mu.Lock()
	pauseRx, out := x.pauseRx, x.out
	x.mu.Unlock()
	pdev, rate, ch := audioFormat(dev, out)
	pcm := makePCM(wave, level, rate, ch)
	if pauseRx != nil {
		pauseRx(true)
		defer pauseRx(false)
		time.Sleep(120 * time.Millisecond) // let the recorder close the sound card
	}
	if wave == nil { // PTT test: key only
		if err := line.Key(true); err != nil {
			x.setStatus("PTT failed: " + err.Error())
			return
		}
		select {
		case <-abort:
		case <-time.After(maxDur - time.Second):
		}
		return
	}
	cmd := exec.Command(aplayPath, "-q", "-D", pdev, "-t", "raw", "-f", "S16_LE", "-r", strconv.Itoa(rate), "-c", strconv.Itoa(ch), "--buffer-time=500000", "--period-time=50000")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		x.setStatus("audio: " + err.Error())
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{&stderr, 2000}
	if err := cmd.Start(); err != nil {
		x.setStatus("audio: " + err.Error())
		return
	}
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		stdin.Write(pcm)
		stdin.Close()
		done <- cmd.Wait()
	}()
	want := time.Duration(len(pcm)/(2*ch)) * time.Second / time.Duration(rate)
	snap := make(chan string, 1)
	go func() { // what the kernel says while it plays
		time.Sleep(1500 * time.Millisecond)
		snap <- playbackSnapshot()
	}()
	defer func() {
		took := time.Since(started)
		d := fmt.Sprintf("%s: aplay -D %s -r %d -c %d, %.1f s of audio, took %.1f s",
			time.Now().Format("15:04:05"), pdev, rate, ch, want.Seconds(), took.Seconds())
		select {
		case sn := <-snap:
			d += "\n" + sn
		default:
			d += "\n(ended before the 1.5 s snapshot)\n"
		}
		x.mu.Lock()
		x.diag = d
		x.mu.Unlock()
	}()
	select { // audio is flowing (silence): now key the rig
	case err := <-done:
		x.audioFailed(stderr.String(), err)
		return
	case <-abort:
		cmd.Process.Kill()
		<-done
		return
	case <-time.After(ft8PTTDelay):
	}
	if err := line.Key(true); err != nil {
		cmd.Process.Kill()
		<-done
		x.setStatus("PTT failed: " + err.Error())
		return
	}
	select {
	case err := <-done:
		if err != nil {
			x.audioFailed(stderr.String(), err)
			return
		}
		x.setStatus("")
	case <-abort:
		cmd.Process.Kill()
		<-done
	case <-time.After(maxDur):
		cmd.Process.Kill()
		<-done
	}
}

// audioFailed: don't keep keying the rig with no audio: stop the sequence.
func (x *ft8Tx) audioFailed(stderr string, err error) {
	if err == nil {
		err = errors.New("audio stopped")
	}
	msg := firstLine(strings.TrimSpace(stderr + " " + err.Error()))
	x.mu.Lock()
	defer x.mu.Unlock()
	x.seq.Stop()
	x.tuning = false
	x.status = "Transmit stopped: the rig's USB sound card failed (" + msg + ")."
	if strings.Contains(msg, "No such device") {
		x.status += " The Pi reset the sound card. Box > System log shows what it saw."
	}
}

func (x *ft8Tx) setStatus(s string) {
	x.mu.Lock()
	x.status = s
	x.mu.Unlock()
}

type ft8TxState struct {
	Avail   bool   `json:"avail"`
	On      bool   `json:"on"`
	Active  bool   `json:"active"`
	Next    string `json:"next"`
	NextOK  bool   `json:"nextOK"` // audio ready
	Sent    string `json:"sent"`   // last message transmitted, as it decodes
	Step    string `json:"step"`
	Parity  int    `json:"parity"`
	Status  string `json:"status"`
	Partner string `json:"partner"`
	LogCall string `json:"logCall"` // what Log QSO would log ("" = nothing)
	Tuning  bool   `json:"tuning"`
}

func (x *ft8Tx) state() ft8TxState {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := ft8TxState{Avail: ft8TxAvailable(), On: x.enabled, Active: x.active, Next: x.seq.Next(),
		Sent: x.lastSent, Step: x.seq.Status(), Parity: x.seq.parity, Status: x.status, Partner: x.seq.their,
		Tuning: x.tuning}
	if l := x.seq.Candidate(); l != nil {
		if t, ok := x.loggedAt[l.Call]; !ok || time.Since(t) >= 15*time.Minute {
			st.LogCall = l.Call
		}
	}
	st.NextOK = st.Next != "" && st.Next == x.waveMsg && x.wave != nil
	if x.genErr != "" && st.Next == x.waveMsg {
		st.Status = x.genErr
	}
	return st
}

// configure takes the engine's settings (called every tick, engine lock held).
func (x *ft8Tx) configure(dev string, freq int, level float64, myCall, grid string, cqPota, autoCQ bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if dev != "" {
		x.dev = dev
	}
	x.freq, x.level = freq, level
	x.seq.MyCall = strings.ToUpper(strings.TrimSpace(myCall))
	x.seq.MyGrid = strings.ToUpper(strings.TrimSpace(grid))
	if len(x.seq.MyGrid) > 4 {
		x.seq.MyGrid = x.seq.MyGrid[:4] // standard FT8 messages carry 4-character grids
	}
	x.seq.CQPota, x.seq.AutoCQ = cqPota, autoCQ
}

func (x *ft8Tx) isEnabled() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.enabled
}

func (x *ft8Tx) checkReadyLocked() error {
	if !x.enabled {
		return errors.New("tick Transmit first")
	}
	if x.seq.MyCall == "" {
		return errors.New("set MYCALL in the macros first")
	}
	if x.seq.MyGrid == "" {
		return errors.New("type your grid in Grid first (e.g. EL98)")
	}
	if x.tuning {
		return errors.New("stop Tune first")
	}
	return nil
}

// startCQ: CQ in the next slot we can still make.
func (x *ft8Tx) startCQ(off float64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkReadyLocked(); err != nil {
		return err
	}
	if x.seq.MyGrid == "" {
		return errors.New("set your grid (e.g. EL98) first")
	}
	trueNow := ft8Clock().Add(-time.Duration(off * float64(time.Second)))
	cur := trueNow.Truncate(ft8Slot)
	n := slotNum(cur)
	if trueNow.Sub(cur) > time.Second || x.active {
		n++
	}
	x.seq.StartCQ(int(n % 2))
	x.touched, x.status = time.Now(), ""
	return nil
}

// call: answer the station in a decoded message.
func (x *ft8Tx) call(msg, slot string, snr int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkReadyLocked(); err != nil {
		return err
	}
	x.touched, x.status = time.Now(), ""
	return x.seq.Call(msg, slot, snr)
}

func (x *ft8Tx) stop() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.stopLocked()
}

const ft8TuneMax = 30 * time.Second

// tune: a steady tone at the TX frequency (WSJT-X's Tune), to set the level/ALC and
// to check the rig's audio path. Stops by itself after 30 s.
type noKey struct{}

func (noKey) Key(bool) error { return nil }
func (noKey) Close()         {}

// tune modes: "tune" = PTT + tone (30 s); "audio" = tone into the rig's USB sound card
// with the rig NOT keyed (10 s); "ptt" = rig keyed, no audio at all (10 s). The last
// two tell a USB/audio problem from an RF/keying one.
func (x *ft8Tx) tune(on bool, mode string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if !on {
		if x.tuning && x.abort != nil {
			close(x.abort)
			x.abort = nil
		}
		x.tuning = false
		return nil
	}
	if !x.enabled {
		return errors.New("tick Transmit first")
	}
	if x.active {
		return errors.New("wait for the transmission to end")
	}
	if x.dev == "" {
		return errors.New("no rig audio device")
	}
	if err := x.checkLineLocked(); err != nil {
		return err
	}
	x.seq.Stop()
	dur := ft8TuneMax
	if mode == "audio" || mode == "ptt" {
		dur = 10 * time.Second
	}
	n := int(dur.Seconds() * 48000)
	w := make([]float32, n)
	ph, dph := 0.0, 2*math.Pi*float64(x.freq)/48000
	for i := range w {
		w[i] = float32(math.Sin(ph))
		ph += dph
		if i < 480 { // 10 ms ramps, no clicks
			w[i] *= float32(i) / 480
		} else if n-i < 480 {
			w[i] *= float32(n-i) / 480
		}
	}
	x.tuning, x.active, x.status = true, true, ""
	x.abort = make(chan struct{})
	x.touched = time.Now()
	abort := x.abort
	var line keyLine = x.line
	if mode == "audio" {
		line = noKey{}
	}
	if mode == "ptt" {
		w = nil
	}
	go func() {
		x.transmit(w, x.level, x.dev, line, abort, dur+time.Second, true)
		x.mu.Lock()
		x.tuning = false
		if x.status == "" && (mode == "audio" || mode == "ptt") {
			x.status = map[string]string{"audio": "Audio test finished OK: 10 s of tone into the rig's sound card, no errors.",
				"ptt": "PTT test finished OK: rig keyed 10 s with no audio."}[mode]
		}
		x.mu.Unlock()
	}()
	return nil
}

// checkLineLocked: the key port can vanish when the rig's USB is unplugged or replugged
// (it comes back with a new ttyUSB number; the by-id name stays the same). Reopen it.
func (x *ft8Tx) checkLineLocked() error {
	if x.line != nil && x.line.Key(false) == nil {
		return nil
	}
	if x.line != nil {
		x.line.Close()
		x.line = nil
	}
	l, err := openPTT(x.port, x.lineName, x.catType)
	if err != nil {
		return fmt.Errorf("the key port is gone (rig unplugged?): %v", err)
	}
	x.line = l
	return nil
}

// openPTT: PTT on a serial line of the key port, or over CAT (the way WSJT-X usually
// keys): Yaesu "TX1;"/"TX0;" on the rig's CAT port; Kenwood "TX1;" (data send: transmit
// the USB audio) / "RX;" on the key port itself.
func openPTT(port, line, catType string) (keyLine, error) {
	if line != "cat" {
		return openKeyLine(port, line == "rts")
	}
	cs := catSpecFor(port, catType)
	if cs.Port == "" {
		return nil, errors.New("CAT PTT needs the rig's USB key port (its CAT port is next to it)")
	}
	c, err := openCAT(cs.Port, cs.Baud)
	if err != nil {
		return nil, err
	}
	return &catPTT{c: c, kenwood: cs.Kenwood}, nil
}

type catPTT struct {
	mu      sync.Mutex
	c       *catConn
	kenwood bool
}

func (p *catPTT) Key(on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case on:
		return p.c.write("TX1;")
	case p.kenwood:
		return p.c.write("RX;")
	default:
		return p.c.write("TX0;")
	}
}

func (p *catPTT) Close() {
	p.Key(false)
	p.c.Close()
}

// makePCM: S16LE at rate (resampled from 48 kHz), ch channels (same on each), after
// the pre-roll silence.
func makePCM(wave []float32, level float64, rate, ch int) []byte {
	pre := int(ft8PreRoll.Seconds() * float64(rate))
	n := len(wave)
	if rate != 48000 {
		n = int(int64(len(wave)) * int64(rate) / 48000)
	}
	pcm := make([]byte, 2*ch*(pre+n))
	g := float32(math.Max(0, math.Min(1, level)) * 32000)
	for i := 0; i < n; i++ {
		var v float32
		if rate == 48000 {
			v = wave[i]
		} else {
			v = lanczosAt(wave, float64(i)*48000/float64(rate))
		}
		smp := uint16(int16(v * g))
		for c := 0; c < ch; c++ {
			binary.LittleEndian.PutUint16(pcm[2*(ch*(pre+i)+c):], smp)
		}
	}
	return pcm
}

// playbackSnapshot: the kernel's view of the rig sound chip's playback right now.
func playbackSnapshot() string {
	var b strings.Builder
	for _, pat := range []string{"/proc/asound/card*/pcm0p/sub0/status", "/proc/asound/card*/pcm0p/sub0/hw_params", "/proc/asound/card*/stream0"} {
		fs, _ := filepath.Glob(pat)
		for _, f := range fs {
			d, _ := os.ReadFile(f)
			txt := string(d)
			if strings.HasSuffix(f, "stream0") { // just the playback part
				if i := strings.Index(txt, "Capture:"); i > 0 {
					txt = txt[:i]
				}
			}
			fmt.Fprintf(&b, "[%s]\n%s", f, txt)
		}
	}
	return b.String()
}

func (x *ft8Tx) setOut(out string) {
	x.mu.Lock()
	x.out = out
	x.mu.Unlock()
}

func (x *ft8Tx) lastDiag() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.diag == "" {
		return "(nothing played yet)\n"
	}
	return x.diag
}

// lanczosAt: the wave's value at fractional sample position p (Lanczos, a = 8: clean
// enough that the resampled FT8 signal has no audible or on-air images).
func lanczosAt(w []float32, p float64) float32 {
	const a = 8
	j := int(math.Floor(p))
	if p == float64(j) && j < len(w) {
		return w[j]
	}
	var sum, norm float64
	for k := j - a + 1; k <= j+a; k++ {
		x := p - float64(k)
		var l float64
		if x == 0 {
			l = 1
		} else {
			px := math.Pi * x
			l = a * math.Sin(px) * math.Sin(px/a) / (px * px)
		}
		norm += l
		if k >= 0 && k < len(w) {
			sum += l * float64(w[k])
		}
	}
	return float32(sum / norm)
}

// isCMediaRig: the rig's sound chip is the FT-710's C-Media one (by its /proc/asound/cards
// name), whose playback only works sent to it directly at 44.1 kHz stereo.
func isCMediaRig(dev string) bool {
	card := cardOf(dev)
	if card == "" {
		return false
	}
	b, _ := os.ReadFile(cardsPath)
	ls := strings.Split(string(b), "\n")
	for i, l := range ls {
		if strings.Contains(l, "["+card+" ") || strings.Contains(l, "["+card+"]") {
			if strings.Contains(l, "C-Media") || (i+1 < len(ls) && strings.Contains(ls[i+1], "C-Media")) {
				return true
			}
		}
	}
	return false
}

func (x *ft8Tx) partnerCall() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.seq.their
}

func (x *ft8Tx) setCATType(t string) {
	x.mu.Lock()
	x.catType = t
	x.mu.Unlock()
}

// audioFormat: the ALSA device, rate and channels to play transmit audio to the rig in,
// from the radio's Audio out setting ("" auto, "plug" 48k mono, "hw48", "hw44").
func audioFormat(dev, out string) (pdev string, rate, ch int) {
	rate, ch, pdev = 48000, 1, dev
	if out == "" && isCMediaRig(dev) {
		out = "hw44" // the FT-710's C-Media chip only takes playback this way
	}
	if out == "plug" {
		out = ""
	}
	if out == "hw48" || out == "hw44" { // straight to the chip in its own format, no ALSA conversion
		ch, pdev = 2, strings.Replace(dev, "plughw:", "hw:", 1)
		if out == "hw44" {
			rate = 44100
		}
	}
	return pdev, rate, ch
}
