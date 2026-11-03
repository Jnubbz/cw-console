package main

// FT8 receive (the Pi box): WSJT-X's own decoder (jt9, built without Qt) decodes each
// 15-second slot. The box records the rig audio, resamples it to WSJT-X's 12 kHz,
// cuts it on the UTC slot boundaries, writes the slot as a WAV file and runs
// "jt9 -8" on it. The DT of what it decodes tells how far off the box's clock is (the
// box sets its clock from the phone); the slot boundaries follow that automatically.

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ft8Rate    = 12000
	ft8Slot    = 15 * time.Second
	ft8Keep    = 300 // decodes kept for the page
	ft8MaxSkew = 5.0 // seconds of clock correction at most
)

var (
	jt9Path  = envOr("CWBOX_JT9", "/usr/bin/jt9")
	ft8Dir   = envOr("CWBOX_FT8DIR", "/tmp/ft8")
	ft8Clock = time.Now // test hook
	// the quick decode for replying: 12.4 s into the slot (all 14 decodes of WSJT-X's FT8
	// sample at depth 1; 11 at 12 s), as WSJT-X starts decoding before
	// the slot ends (the decoder copes with the last symbols missing), so an answer is
	// decoded and the reply ready before our slot starts
	ft8QuickAt = 12400 * time.Millisecond
	// decoder threads: all the cores (the Pi 5 has 4); CWBOX_FT8_THREADS overrides (1 = the
	// classic single-threaded decoder)
	ft8Threads = ft8ThreadCount()
)

func ft8ThreadCount() int {
	if n, err := strconv.Atoi(os.Getenv("CWBOX_FT8_THREADS")); err == nil && n >= 1 {
		return min(n, 12)
	}
	return min(runtime.NumCPU(), 12)
}

type ft8Decode struct {
	Slot string  `json:"slot"` // HHMMSS (UTC)
	SNR  int     `json:"snr"`
	DT   float64 `json:"dt"`
	Freq int     `json:"freq"`
	Msg  string  `json:"msg"`
}

type ft8Rx struct {
	mu sync.Mutex

	on       bool
	depth    int
	myCall   string
	hisCall  string // the station the transmitter is working (set by the engine)
	wf       ft8WF
	benching bool
	bench    ft8BenchState
	rs       resampler34
	buf      []float32 // 12 kHz audio, oldest first
	bufEnd   time.Time // wall-clock time of the sample after the last one in buf
	offset   float64   // seconds: slot boundaries happen at UTC boundary + offset
	nextEnd  time.Time // end of the next slot to decode (UTC boundary, without offset)
	busy     bool
	lower    int // depth levels dropped automatically (busy band, slow decodes)
	fast     int // quick decodes in a row since the last drop

	// transmit support (set by the engine): a quick decode at 12.4 s so the reply can
	// go out in the very next slot, results to the sequencer, and no decoding of slots
	// we transmitted in
	early     bool
	earlySlot time.Time
	quickBusy bool
	onDecodes func(slot string, decs []ft8Decode)
	skip      func(slotStart time.Time) bool

	decodes []ft8Decode // newest last
	seq     int64
	status  string
	last    struct {
		n    int
		secs float64
		slot string
	}
}

type ft8State struct {
	On     bool       `json:"on"`
	Seq    int64      `json:"seq"`
	Status string     `json:"status"`
	Offset float64    `json:"offset"`
	Auto   string     `json:"auto"`  // depth lowered automatically, and why
	WFSeq  int64      `json:"wfSeq"` // waterfall rows made so far; WF = the newest of them
	WF     [][]byte   `json:"wf"`
	Busy   bool       `json:"busy"`
	LastN  int        `json:"lastN"`
	LastS  float64    `json:"lastSecs"`
	Slot   string     `json:"lastSlot"`
	Have   bool       `json:"have"` // the decoder is installed (only on the box)
	Tx     ft8TxState `json:"tx"`
}

func ft8Available() bool {
	st, err := os.Stat(jt9Path)
	return err == nil && st.Mode().IsRegular()
}

func (f *ft8Rx) setOn(on bool, depth int, myCall string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if on && !f.on {
		f.buf, f.nextEnd, f.status = f.buf[:0], time.Time{}, "Waiting for the next 15-second slot..."
		f.rs = resampler34{}
	}
	if depth != f.depth {
		f.lower, f.fast = 0, 0 // a new choice: start from it
	}
	f.on, f.depth, f.myCall = on, depth, myCall
}

func (f *ft8Rx) depthNowLocked() int { return max(1, f.depth-f.lower) }

func (f *ft8Rx) slowerLocked() {
	if f.depthNowLocked() > 1 {
		f.lower++
	}
	f.fast = 0
}

var depthNames = map[int]string{1: "Fast", 2: "Normal", 3: "Deep"}

func (f *ft8Rx) autoTextLocked() string {
	if f.lower == 0 {
		return ""
	}
	return "busy band: decoding at " + depthNames[f.depthNowLocked()] + " for now"
}

// push takes capture audio (16 kHz) as it arrives.
func (f *ft8Rx) push(x []float32, rate int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on {
		return
	}
	if now := ft8Clock(); !f.bufEnd.IsZero() && now.Sub(f.bufEnd) > 500*time.Millisecond {
		// a gap (recording paused while we transmitted): start the buffer afresh
		f.buf, f.rs = f.buf[:0], resampler34{}
		f.wf.reset()
	}
	n0 := len(f.buf)
	switch rate {
	case 16000:
		f.buf = f.rs.process(x, f.buf)
	case 12000:
		f.buf = append(f.buf, x...)
	default:
		f.status = fmt.Sprintf("FT8 needs 12 or 16 kHz audio, not %d Hz", rate)
		return
	}
	f.wf.push(f.buf[n0:])
	f.bufEnd = ft8Clock()
	if keep := 40 * ft8Rate; len(f.buf) > keep+ft8Rate {
		f.buf = append(f.buf[:0], f.buf[len(f.buf)-keep:]...)
	}
}

// tick starts a decode when a slot is complete. Call it often (every 25-100 ms).
func (f *ft8Rx) tick() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.on {
		return
	}
	if f.benching {
		f.nextEnd = time.Time{} // start afresh when it's done
		f.status = "Benchmark running: decoding paused"
		return
	}
	now := ft8Clock()
	off := time.Duration(f.offset * float64(time.Second))
	trueNow := now.Add(-off)
	cur := trueNow.Truncate(ft8Slot)
	if f.early && trueNow.Sub(cur) >= ft8QuickAt && !f.earlySlot.Equal(cur) {
		f.earlySlot = cur
		if !f.quickBusy && (f.skip == nil || !f.skip(cur)) {
			if x := f.windowLocked(cur.Add(off), ft8QuickAt); x != nil {
				f.quickBusy = true
				go f.quickDecode(x, cur.UTC(), f.myCall)
			}
		}
	}
	if f.nextEnd.IsZero() {
		f.nextEnd = now.Add(-off).Truncate(ft8Slot).Add(ft8Slot)
	}
	end := f.nextEnd.Add(off)
	if now.Before(end.Add(150 * time.Millisecond)) {
		return
	}
	slotStart := f.nextEnd.Add(-ft8Slot)
	f.nextEnd = now.Add(-off).Truncate(ft8Slot).Add(ft8Slot)
	if now.Sub(end) > 3*time.Second { // we were held up; don't decode stale audio
		return
	}
	if f.skip != nil && f.skip(slotStart) {
		f.status = ""
		return // we were transmitting
	}
	// samples covering [slotStart+off, end)
	n := len(f.buf)
	lastIdx := n - int(f.bufEnd.Sub(end).Seconds()*ft8Rate) // index of the sample at `end`
	firstIdx := lastIdx - 15*ft8Rate
	if lastIdx > n {
		lastIdx = n
	}
	if lastIdx-max(firstIdx, 0) < 10*ft8Rate {
		f.status = "Not enough audio for this slot (is the radio connected?)"
		return
	}
	if f.busy {
		f.slowerLocked()
		f.status = "The decoder was still busy with the last slot (skipped this one)"
		return
	}
	slot := make([]float32, 15*ft8Rate)
	copy(slot[max(0, -firstIdx):], f.buf[max(firstIdx, 0):lastIdx])
	f.busy = true
	go f.decode(slot, slotStart.UTC(), f.depthNowLocked(), f.myCall)
}

// windowLocked: d of audio starting at wall time start, padded to a 15 s slot (nil if
// there isn't enough).
func (f *ft8Rx) windowLocked(start time.Time, d time.Duration) []float32 {
	n := len(f.buf)
	firstIdx := n - int(f.bufEnd.Sub(start).Seconds()*ft8Rate)
	lastIdx := firstIdx + int(d.Seconds()*ft8Rate)
	if lastIdx > n {
		lastIdx = n
	}
	if lastIdx-max(firstIdx, 0) < 10*ft8Rate {
		return nil
	}
	x := make([]float32, 15*ft8Rate)
	copy(x[max(0, -firstIdx):], f.buf[max(firstIdx, 0):lastIdx])
	return x
}

// quickDecode: depth 1 on the first 12.4 s of a slot, for the sequencer only.
func (f *ft8Rx) quickDecode(x []float32, at time.Time, myCall string) {
	decs, _ := runJT9(filepath.Join(ft8Dir, "quick"), x, at, 1, myCall, f.partner())
	f.mu.Lock()
	f.quickBusy = false
	cb := f.onDecodes
	f.mu.Unlock()
	if cb != nil {
		cb(at.Format("150405"), decs)
	}
}

var jt9Line = regexp.MustCompile(`^(\d{4,6})\s+(-?\d+)\s+(-?\d+\.\d)\s+(\d+)\s+~\s+(.*?)\s*$`)

func (f *ft8Rx) decode(x []float32, at time.Time, depth int, myCall string) {
	t0 := time.Now()
	decs, err := runJT9(ft8Dir, x, at, depth, myCall, f.partner())
	secs := time.Since(t0).Seconds()
	f.mu.Lock()
	cb := f.onDecodes
	defer func() {
		f.mu.Unlock()
		if cb != nil && err == nil {
			cb(at.Format("150405"), decs)
		}
	}()
	f.busy = false
	if err != nil {
		f.slowerLocked()
		f.status = "FT8 decoder: " + err.Error()
		return
	}
	// Keep each decode well inside the slot (with Transmit on there's also the quick
	// decode at 12.4 s): drop a level when slow, go back up after a quiet spell.
	switch {
	case secs > 11:
		f.slowerLocked()
	case secs < 5 && f.lower > 0:
		if f.fast++; f.fast >= 8 {
			f.lower--
			f.fast = 0
		}
	default:
		f.fast = 0
	}
	f.decodes = append(f.decodes, decs...)
	if len(f.decodes) > ft8Keep {
		f.decodes = append(f.decodes[:0], f.decodes[len(f.decodes)-ft8Keep:]...)
	}
	f.last.n, f.last.secs, f.last.slot = len(decs), secs, at.Format("150405")
	f.seq++
	f.status = ""
	// Clock correction: signals arriving late (DT > 0) mean our slots start early.
	if len(decs) >= 3 {
		dts := make([]float64, len(decs))
		for i, d := range decs {
			dts[i] = d.DT
		}
		sort.Float64s(dts)
		med := dts[len(dts)/2]
		if math.Abs(med-0.2) > 0.3 {
			f.offset = math.Max(-ft8MaxSkew, math.Min(ft8MaxSkew, f.offset+0.7*(med-0.2)))
		}
	}
}

func runJT9(dir string, x []float32, at time.Time, depth int, myCall, hisCall string) ([]ft8Decode, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := filepath.Join(dir, at.Format("060102_150405")+".wav")
	if err := writeWAV12k(name, x); err != nil {
		return nil, err
	}
	defer os.Remove(name)
	args := jt9Args(dir, depth, ft8Threads, myCall, hisCall)
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, jt9Path, append(args, name)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("took over 14 s; try Normal depth")
	}
	if err != nil {
		return nil, err
	}
	return parseJT9(string(out), at.Format("150405")), nil
}

func parseJT9(out, slot string) []ft8Decode {
	var r []ft8Decode
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := jt9Line.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		snr, _ := strconv.Atoi(m[2])
		dt, _ := strconv.ParseFloat(m[3], 64)
		fr, _ := strconv.Atoi(m[4])
		msg := strings.TrimSpace(m[5])
		// AP-assisted decodes end in markers like "a1"/"a2"/"?"; keep them, they're real.
		if msg != "" {
			r = append(r, ft8Decode{Slot: slot, SNR: snr, DT: dt, Freq: fr, Msg: msg})
		}
	}
	return r
}

func writeWAV12k(name string, x []float32) error {
	b := make([]byte, 44+2*len(x))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(x)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], ft8Rate)
	binary.LittleEndian.PutUint32(b[28:], ft8Rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(x)))
	// Level: WSJT-X likes the audio well below full scale; scale so peaks don't clip.
	peak := float32(1e-9)
	for _, v := range x {
		if v > peak {
			peak = v
		} else if -v > peak {
			peak = -v
		}
	}
	g := float32(1)
	if peak > 0.9 {
		g = 0.9 / peak
	}
	for i, v := range x {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(int16(math.Round(float64(v*g*32767)))))
	}
	return os.WriteFile(name, b, 0o644)
}

func (f *ft8Rx) state() *ft8State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &ft8State{On: f.on, Seq: f.seq, Status: f.status, Offset: math.Round(f.offset*10) / 10,
		WFSeq: f.wf.seq, WF: append([][]byte(nil), f.wf.rows...), Auto: f.autoTextLocked(), Busy: f.busy, LastN: f.last.n, LastS: math.Round(f.last.secs*10) / 10, Slot: f.last.slot, Have: ft8Available()}
}

func (f *ft8Rx) list() []ft8Decode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ft8Decode{}, f.decodes...)
}

func (f *ft8Rx) clear() {
	f.mu.Lock()
	f.decodes = nil
	f.seq++
	f.mu.Unlock()
}

// resampler34 converts 16 kHz to 12 kHz (up 3, down 4) with a windowed-sinc low-pass
// at 5.4 kHz, so nothing above 6 kHz folds back into the FT8 passband.
type resampler34 struct {
	hist  []float32 // last taps/3+1 input samples
	phase int       // position in the 48 kHz grid of the next output, relative to hist end
}

var rs34Taps = func() []float32 {
	const n, fs, fc = 144, 48000.0, 5400.0
	h := make([]float32, n)
	for i := range h {
		t := float64(i) - float64(n-1)/2
		s := 2 * fc / fs
		if t != 0 {
			s = math.Sin(2*math.Pi*fc/fs*t) / (math.Pi * t)
		}
		w := 0.42 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1)) + 0.08*math.Cos(4*math.Pi*float64(i)/float64(n-1))
		h[i] = float32(3 * s * w) // x3: zero-stuffing gain
	}
	return h
}()

func (r *resampler34) process(in []float32, out []float32) []float32 {
	const L, M = 3, 4
	taps := rs34Taps
	nh := len(taps)/L + 1
	if r.hist == nil {
		r.hist = make([]float32, nh)
	}
	x := append(r.hist, in...)
	// Output k sits at 48 kHz index p (counting from x[0] at index 0); taps reach back
	// len(taps)-1 points on the 48 kHz grid.
	p := r.phase
	if p == 0 {
		p = (nh - 1) * L
	}
	for p < len(x)*L {
		var acc float32
		// sum over input samples j with 0 <= p - j*L < len(taps)
		j := p / L
		for k := p - j*L; k < len(taps) && j >= 0; k, j = k+L, j-1 {
			acc += taps[k] * x[j]
		}
		out = append(out, acc)
		p += M
	}
	keep := nh
	if len(x) < keep {
		keep = len(x)
	}
	drop := len(x) - keep
	r.phase = p - drop*L
	r.hist = append(r.hist[:0:0], x[drop:]...)
	return out
}

func (f *ft8Rx) offsetNow() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offset
}

func (f *ft8Rx) setEarly(on bool) {
	f.mu.Lock()
	f.early = on
	f.mu.Unlock()
}

func (f *ft8Rx) partner() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hisCall
}

func (f *ft8Rx) setPartner(c string) {
	f.mu.Lock()
	f.hisCall = c
	f.mu.Unlock()
}

// jt9Args: the decoder's options for a depth (1-3) on threads cores.
func jt9Args(dir string, depth, threads int, myCall, hisCall string) []string {
	d := strconv.Itoa(max(1, min(3, depth)))
	args := []string{"-8", "-d", d, "-a", dir, "-t", dir}
	if threads > 1 {
		// WSJT-X's multithreaded FT8 decoder on every core; its passes (-C) and
		// sensitivity (-E) follow the depth. -U: one line per decode.
		args = append(args, "-M", "-N", strconv.Itoa(threads), "-U", "-C", d, "-E", d)
	}
	callRe := regexp.MustCompile(`^[A-Z0-9/]{3,12}$`)
	if c := strings.ToUpper(strings.TrimSpace(myCall)); c != "" && callRe.MatchString(c) {
		args = append(args, "-c", c)
	}
	if c := strings.ToUpper(strings.TrimSpace(hisCall)); c != "" && callRe.MatchString(c) {
		args = append(args, "-x", c) // the station we're working: the decoder digs for it
	}
	return args
}
