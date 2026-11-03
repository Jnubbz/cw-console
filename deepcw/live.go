package deepcw

// Live decoding: a rolling window of receive audio is decoded on a background goroutine
// and stitched into one transcript. Port of Nexus crates/tempo-audio/src/aicw.rs
// (15 s window, 1 s tail guard, absolute-time cursor), with the cadence, throttle and
// thread count made adjustable (Speed).

import (
	"strings"
	"sync"
	"time"
)

// Speed is a CPU-versus-lag setting.
type Speed int

const (
	SpeedAuto     Speed = iota // Eco on battery, Balanced on mains power
	SpeedEco                   // 1 thread, every ~4 s, ≤ ~half a core
	SpeedBalanced              // 1 thread, every 2 s (Nexus's cadence)
	SpeedFast                  // all cores, every 1 s
	SpeedMax                   // all cores, back to back
)

var SpeedNames = []string{"Auto (Eco on battery)", "Eco", "Balanced", "Fast", "Max"}

// Plan is how a Speed runs.
type Plan struct {
	Threads  int
	Cadence  time.Duration // minimum time between decode passes
	Throttle float64       // and at least this many × the last pass time
}

// PlanFor resolves a speed for the given core count and power state.
func PlanFor(s Speed, cores int, onBattery bool) Plan {
	if s == SpeedAuto {
		s = SpeedBalanced
		if onBattery {
			s = SpeedEco
		}
	}
	switch s {
	case SpeedEco:
		return Plan{1, 4 * time.Second, 2}
	case SpeedFast:
		return Plan{cores, time.Second, 1.25}
	case SpeedMax:
		return Plan{cores, 250 * time.Millisecond, 1}
	default:
		return Plan{1, 2 * time.Second, 2}
	}
}

// TailGuardSecs: characters starting in the last this-many seconds of a window wait for
// the next window. Nexus uses 1.0; 1.5 stops slow (~14 WPM) characters being cut off
// at the window edge (tested on the recordings in internal/testgen).
var TailGuardSecs = 1.5

const (
	WindowSecs     = 15.0 // Nexus's window
	idlePoll       = 100 * time.Millisecond
	maxBufferedSec = WindowSecs
)

// DedupeSecs: the same character decoded by two overlapping windows can land a frame
// either side of the stitch cursor; a repeat within this time is the same character.
// (Genuine doubled letters are >= ~120 ms apart even at 40 WPM.)
var DedupeSecs = 0.06

// stitcher merges overlapping windows into one transcript (Nexus's absolute-time
// cursor + tail guard, plus duplicate suppression and line breaks).
type stitcher struct {
	emittedUntil float64
	lastCharAbs  float64
	recent       []TimedChar // emitted in the last second (absolute times)
	started      bool
}

func (s *stitcher) reset() { *s = stitcher{} }

func (s *stitcher) add(chars []TimedChar, startAbs, endAbs, cutoff float64, breaks bool) string {
	if !s.started || s.emittedUntil < startAbs || s.emittedUntil > endAbs {
		s.emittedUntil = startAbs
		s.lastCharAbs = -1
		s.started = true
	}
	var out strings.Builder
	for _, c := range chars {
		t := startAbs + c.At
		if t <= s.emittedUntil || t > cutoff {
			continue
		}
		dup := false
		for _, r := range s.recent {
			if r.Ch == c.Ch && t-r.At < DedupeSecs && t-r.At > -DedupeSecs {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if c.Ch != " " {
			if breaks && s.lastCharAbs >= 0 && t-s.lastCharAbs > BreakSecs {
				out.WriteString("\n")
			}
			s.lastCharAbs = t
		}
		out.WriteString(c.Ch)
		s.recent = append(s.recent, TimedChar{t, c.Ch})
	}
	if cutoff > s.emittedUntil {
		s.emittedUntil = cutoff
	}
	keep := s.recent[:0]
	for _, r := range s.recent {
		if r.At > s.emittedUntil-1 {
			keep = append(keep, r)
		}
	}
	s.recent = keep
	return out.String()
}

// Live is a background AI decoder fed with receive audio.
type Live struct {
	model *Model

	mu           sync.Mutex
	rate         int
	ring         []float32 // last WindowSecs of audio at `rate`
	fed          int64     // total samples fed (absolute stream clock)
	plan         Plan
	text         strings.Builder
	status       string
	lastPass     time.Duration
	enabled      bool
	stop         chan struct{}
	resetPending bool
	focus        *Focus
}

// BreakSecs: a pause this long between characters starts a new line ("\n" in the text).
var BreakSecs = 2.5

// inferMu serialises inference (the thread count is global to the matrix code).
var inferMu sync.Mutex

// Exclusive runs f with the decoder to itself at the given thread count.
func Exclusive(threads int, f func()) {
	inferMu.Lock()
	defer inferMu.Unlock()
	SetThreads(threads)
	f()
}

// SetFocus sets (or clears, with nil) the pitch focus for later passes.
func (l *Live) SetFocus(f *Focus) {
	l.mu.Lock()
	if f != nil {
		c := *f
		l.focus = &c
	} else {
		l.focus = nil
	}
	l.mu.Unlock()
}

func NewLive(m *Model) *Live {
	l := &Live{model: m, plan: PlanFor(SpeedBalanced, 1, false), stop: make(chan struct{})}
	go l.run()
	return l
}

// SetPlan changes the speed plan (takes effect on the next pass).
func (l *Live) SetPlan(p Plan) {
	l.mu.Lock()
	l.plan = p
	l.mu.Unlock()
}

// SetEnabled turns decoding on/off (off = no work).
func (l *Live) SetEnabled(on bool) {
	l.mu.Lock()
	if on && !l.enabled { // start fresh: don't re-decode audio from before the pause
		l.ring = l.ring[:0]
		l.fed = 0
		l.resetPending = true
	}
	l.enabled = on
	l.mu.Unlock()
}

// Push adds receive audio at the given sample rate.
func (l *Live) Push(samples []float32, rate int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.enabled {
		return
	}
	if rate != l.rate {
		l.rate = rate
		l.ring = l.ring[:0]
		l.fed = 0
		l.resetPending = true
	}
	l.ring = append(l.ring, samples...)
	l.fed += int64(len(samples))
	if keep := int(maxBufferedSec * float64(rate)); len(l.ring) > keep {
		l.ring = append(l.ring[:0], l.ring[len(l.ring)-keep:]...)
	}
}

// Reset clears audio and the stitch cursor (e.g. after retuning).
func (l *Live) Reset() {
	l.mu.Lock()
	l.ring = l.ring[:0]
	l.fed = 0
	l.resetPending = true
	l.mu.Unlock()
}

// TakeText returns newly decoded text since the last call.
func (l *Live) TakeText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.text.String()
	l.text.Reset()
	return s
}

// Status is a short state string for the UI; LastPass is the latest inference time.
func (l *Live) Status() (string, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status, l.lastPass
}

func (l *Live) Close() { close(l.stop) }

func (l *Live) run() {
	var lastDecode time.Time
	var st stitcher
	for {
		select {
		case <-l.stop:
			return
		case <-time.After(idlePoll):
		}
		l.mu.Lock()
		if !l.enabled {
			l.mu.Unlock()
			continue
		}
		if l.resetPending {
			st.reset()
			l.resetPending = false
		}
		plan, last := l.plan, l.lastPass
		wait := plan.Cadence
		if t := time.Duration(float64(last) * plan.Throttle); t > wait {
			wait = t
		}
		need := int(WindowSecs * float64(l.rate))
		if l.rate == 0 || len(l.ring) < need || time.Since(lastDecode) < wait {
			if l.rate != 0 && len(l.ring) < need {
				l.status = "listening (filling the 15 s window)"
			}
			l.mu.Unlock()
			continue
		}
		window := append([]float32(nil), l.ring[len(l.ring)-need:]...)
		rate, fed := l.rate, l.fed
		focus := l.focus
		l.mu.Unlock()

		lastDecode = time.Now()
		endAbs := float64(fed) / float64(rate)
		startAbs := endAbs - float64(len(window))/float64(rate)
		audio := ResampleLinear(window, rate, l.model.Meta.SampleRate)
		var chars []TimedChar
		var err error
		var pass time.Duration
		Exclusive(plan.Threads, func() {
			t0 := time.Now()
			chars, err = l.model.DecodeTimedFocus(audio, focus)
			pass = time.Since(t0)
		})

		l.mu.Lock()
		l.lastPass = pass
		if err != nil {
			l.status = "decode error: " + err.Error()
			l.mu.Unlock()
			continue
		}
		if l.resetPending { // retuned/cleared mid-pass: drop this result
			l.mu.Unlock()
			continue
		}
		if s := st.add(chars, startAbs, endAbs, endAbs-TailGuardSecs, true); strings.Trim(s, " ") != "" {
			l.text.WriteString(s)
		}
		l.status = "decoding"
		l.mu.Unlock()
	}
}

// DecodeStream decodes a whole recording the way the live decoder would: windows of
// WindowSecs every `step` seconds, stitched with the same cursor and tail guard.
func DecodeStream(m *Model, samples []float32, rate int, step float64, focus *Focus) (string, error) {
	need := int(WindowSecs * float64(rate))
	pad := make([]float32, need) // lead-in silence so the first window is full
	all := append(pad, samples...)
	all = append(all, make([]float32, int(2*float64(rate)))...) // flush the tail guard
	var out strings.Builder
	var st stitcher
	stepN := int(step * float64(rate))
	for end := need; ; end += stepN {
		if end > len(all) {
			end = len(all)
		}
		window := all[end-need : end]
		endAbs := float64(end) / float64(rate)
		startAbs := endAbs - WindowSecs
		chars, err := m.DecodeTimedFocus(ResampleLinear(window, rate, m.Meta.SampleRate), focus)
		if err != nil {
			return "", err
		}
		cutoff := endAbs - TailGuardSecs
		if end == len(all) {
			cutoff = endAbs
		}
		out.WriteString(st.add(chars, startAbs, endAbs, cutoff, false))
		if end == len(all) {
			break
		}
	}
	return strings.Join(strings.Fields(out.String()), " "), nil
}

// BenchResult is one pass time at a thread count.
type BenchResult struct {
	Threads int
	Pass    time.Duration
}

// Benchmark times a full 15 s decode at 1 thread and at `cores` threads.
func Benchmark(m *Model, cores int, window []float32) ([]BenchResult, error) {
	var res []BenchResult
	counts := []int{1}
	if cores > 1 {
		counts = append(counts, cores)
	}
	for _, th := range counts {
		var best time.Duration
		var err error
		Exclusive(th, func() {
			for i := 0; i < 2; i++ { // first pass warms caches; keep the faster
				t0 := time.Now()
				_, err = m.DecodeTimed(window)
				if d := time.Since(t0); best == 0 || d < best {
					best = d
				}
			}
		})
		if err != nil {
			return nil, err
		}
		res = append(res, BenchResult{th, best})
	}
	return res, nil
}

// Interval is how often a plan decodes given a pass time.
func (p Plan) Interval(pass time.Duration) time.Duration {
	iv := p.Cadence
	if t := time.Duration(float64(pass) * p.Throttle); t > iv {
		iv = t
	}
	return iv
}
