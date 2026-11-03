package cw

import (
	"math"
	"sort"
	"strings"
)

// Decode is a batch CW decode result: recovered text and estimated speed.
type Decode struct {
	Text string
	WPM  uint32
}

// DecodeCW decodes CW from mono samples heard at pitchHz, sampled at sr Hz.
// Port of Nexus `decode_cw`.
func DecodeCW(samples []float32, sr, pitchHz float32) Decode {
	if sr <= 0 || len(samples) < int(sr*0.05) {
		return Decode{}
	}
	hop := int(max32(sr*0.004, 1))
	var env []float32
	for i := 0; i+hop <= len(samples); i += hop {
		env = append(env, TonePower(samples[i:i+hop], sr, pitchHz))
	}
	if len(env) < 8 {
		return Decode{}
	}
	sorted := append([]float32(nil), env...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	lo := max32(sorted[len(sorted)/10], 1e-9)
	hi := sorted[len(sorted)*9/10]
	if hi < lo*3 {
		return Decode{}
	}
	thresh := (lo + hi) * 0.5
	type seg struct {
		mark bool
		n    int
	}
	var segs []seg
	for _, p := range env {
		mark := p >= thresh
		if len(segs) > 0 && segs[len(segs)-1].mark == mark {
			segs[len(segs)-1].n++
		} else {
			segs = append(segs, seg{mark, 1})
		}
	}
	start := len(segs)
	for i, s := range segs {
		if s.mark {
			start = i
			break
		}
	}
	segs = segs[start:]
	if len(segs) == 0 {
		return Decode{}
	}
	durs := make([]int, len(segs))
	for i, s := range segs {
		durs[i] = s.n
	}
	sort.Ints(durs)
	r := durs[len(durs)/5]
	if r < 1 {
		r = 1
	}
	rough := float32(r)
	sum, cnt := 0, 0
	for _, d := range durs {
		if float32(d) < 2*rough {
			sum += d
			cnt++
		}
	}
	unit := rough
	if cnt > 0 {
		unit = float32(sum) / float32(cnt)
	}
	var text, sym strings.Builder
	flush := func() {
		if sym.Len() > 0 {
			if c, ok := morseToChar(sym.String()); ok {
				text.WriteRune(c)
			}
			sym.Reset()
		}
	}
	for _, s := range segs {
		u := float32(s.n) / unit
		if s.mark {
			if u < 2 {
				sym.WriteByte('.')
			} else {
				sym.WriteByte('-')
			}
		} else if u >= 2 {
			flush()
			if u >= 5 {
				text.WriteByte(' ')
			}
		}
	}
	flush()
	ditSecs := unit * float32(hop) / sr
	var wpm uint32
	if ditSecs > 0 {
		wpm = uint32(clampF(float32(math.Round(float64(1.2/ditSecs))), 0, 99))
	}
	return Decode{Text: strings.TrimSpace(text.String()), WPM: wpm}
}

// SkimHit is a signal found by the wideband skimmer.
type SkimHit struct {
	PitchHz uint32
	Text    string
	WPM     uint32
}

// SkimCW decodes at every local power peak between loHz and hiHz. Port of `skim_cw`.
func SkimCW(samples []float32, sr float32, loHz, hiHz, stepHz uint32) []SkimHit {
	if stepHz < 1 {
		stepHz = 1
	}
	var channels []uint32
	for f := loHz; f <= hiHz; f += stepHz {
		channels = append(channels, f)
	}
	if len(channels) < 3 {
		return nil
	}
	powers := make([]float32, len(channels))
	for i, f := range channels {
		powers[i] = TonePower(samples, sr, float32(f))
	}
	sorted := append([]float32(nil), powers...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	floor := max32(sorted[len(sorted)/2], 1e-9)
	var hits []SkimHit
	for i := range channels {
		p := powers[i]
		peak := p > floor*4 &&
			(i == 0 || p >= powers[i-1]) &&
			(i+1 >= len(powers) || p >= powers[i+1])
		if !peak {
			continue
		}
		d := DecodeCW(samples, sr, float32(channels[i]))
		glyphs := 0
		for _, c := range d.Text {
			if c != ' ' && c != '\t' && c != '\n' {
				glyphs++
			}
		}
		dup := false
		for _, h := range hits {
			if h.Text == d.Text {
				dup = true
			}
		}
		if glyphs >= 3 && d.WPM >= 8 && d.WPM <= 50 && !dup {
			hits = append(hits, SkimHit{channels[i], d.Text, d.WPM})
		}
	}
	return hits
}

const transcriptCap = 4000

var offNoiseHz = [4]float32{250, 400, 600, 800}

// StreamDecoder is the streaming, stateful CW decoder (port of `CwStreamDecoder`):
// Goertzel at the pitch → fast-attack peak / slow floor AGC → Schmitt keying →
// spike rejection → adaptive dit/dah tracking → Morse → text.
type StreamDecoder struct {
	sr, pitch                   float32
	hop                         int
	carry                       []float32
	noise, offNoise, peak       float32
	markPeak, loThresh, hiThres float32
	present, keyDown            bool
	run, spaceRun               int
	ditAvg, dahAvg              float32
	sawMark                     bool
	sym                         []byte
	flushedChar, flushedWord    bool
	transcript                  []byte
	sensitivity                 float32

	// Improvements over Nexus (off = exact Nexus behaviour). See SetImproved.
	improved bool
	marks    []float32 // recent mark lengths (hops), for speed re-sync
	gaps     []float32 // recent gap lengths before each mark (hops)
	Resyncs  int       // how many times the speed re-sync has fired (diagnostics)
}

// SetImproved turns on two fixes not in Nexus's classic decoder:
//
//  1. Speed re-sync: Nexus only moves its dah estimate when a mark is already called a
//     dah, so a sender much faster than the tracker (or one who speeds up) gets every dah
//     read as a dit, forever. This keeps the last 12 marks, splits them into two
//     clusters, and if they form a clear dit/dah pair (2–5× apart) that the tracker's
//     dit/dah boundary disagrees with, snaps the tracker to them.
//  2. Fade following: during key-down the signal-peak follower releases toward the
//     current mark level (~200 ms) instead of only its ~4 s decay, so QSB doesn't leave
//     the keying threshold stranded above a faded signal. Between marks it still holds.
func (d *StreamDecoder) SetImproved(on bool) { d.improved = on }

// NewStreamDecoder makes a decoder for audio at sr Hz listening at pitchHz.
func NewStreamDecoder(sr, pitchHz float32) *StreamDecoder {
	hop := int(max32(sr*0.004, 1))
	h := hop
	if h < 1 {
		h = 1
	}
	defaultDit := max32((1.2/20.0)*sr/float32(h), 1)
	return &StreamDecoder{
		sr: sr, pitch: pitchHz, hop: hop,
		noise: 1e-9, offNoise: 1e-9,
		hiThres:     float32(math.Inf(1)),
		ditAvg:      defaultDit,
		dahAvg:      3 * defaultDit,
		sensitivity: 0.5,
		improved:    defaultImproved,
	}
}

// defaultImproved is the improved-mode setting for new decoders (tests flip it).
var defaultImproved = false

// SetSensitivity sets operator sensitivity in [0,1] (0.5 = Nexus default).
func (d *StreamDecoder) SetSensitivity(s float32) { d.sensitivity = clampF(s, 0, 1) }

// Sensitivity returns the current sensitivity.
func (d *StreamDecoder) Sensitivity() float32 { return d.sensitivity }

// Pitch returns the listening pitch.
func (d *StreamDecoder) Pitch() float32 { return d.pitch }

// Present reports whether a keyed signal is currently detected.
func (d *StreamDecoder) Present() bool { return d.present }

// KeyDown reports the current key state.
func (d *StreamDecoder) KeyDown() bool { return d.keyDown }

func (d *StreamDecoder) snrMult() float32     { return 6 - 4*d.sensitivity }
func (d *StreamDecoder) presentMult() float32 { return 4 - 2*d.sensitivity }
func (d *StreamDecoder) hiFrac() float32      { return 0.85 - 0.5*d.sensitivity }
func (d *StreamDecoder) loFrac() float32      { return d.hiFrac() - 0.20 }
func (d *StreamDecoder) spikeFrac() float32   { return 0.55 - 0.3*d.sensitivity }

func (d *StreamDecoder) timingPlausible() bool {
	k := 1.6 + 2.8*d.sensitivity
	ratio := d.dahAvg / max32(d.ditAvg, 1e-6)
	if d.improved && (d.ditAvg < 0.8*d.ditHops(60) || d.ditAvg > d.ditHops(5)) {
		return false // tracked speed outside human Morse (static crashes): mute
	}
	return ratio >= 3/k && ratio <= 3*k
}

// Retune to a new pitch. Resets decode state and transcript (keeps sensitivity),
// exactly like Nexus.
func (d *StreamDecoder) Retune(pitchHz float32) {
	if float32(math.Abs(float64(pitchHz-d.pitch))) > 1 {
		s, imp := d.sensitivity, d.improved
		*d = *NewStreamDecoder(d.sr, pitchHz)
		d.sensitivity, d.improved = s, imp
	}
}

// Transcript returns the accumulated text.
func (d *StreamDecoder) Transcript() string { return string(d.transcript) }

// TakeText returns and removes accumulated text WITHOUT touching decode state
// (so an in-progress character keeps decoding). Added for the GUI.
func (d *StreamDecoder) TakeText() string {
	s := string(d.transcript)
	d.transcript = d.transcript[:0]
	return s
}

// WPM is the estimated sending speed; 0 before any marks.
func (d *StreamDecoder) WPM() uint32 {
	if !d.sawMark {
		return 0
	}
	ditSecs := d.ditAvg * float32(d.hop) / d.sr
	if ditSecs > 0 {
		return uint32(clampF(float32(math.Round(float64(1.2/ditSecs))), 0, 99))
	}
	return 0
}

// Clear drops the text (and pending symbol), keeping learned speed/threshold.
func (d *StreamDecoder) Clear() {
	d.transcript = d.transcript[:0]
	d.sym = d.sym[:0]
	d.flushedChar = false
	d.flushedWord = false
}

// Push feeds newly-arrived audio.
func (d *StreamDecoder) Push(samples []float32) {
	if d.sr <= 0 || d.hop == 0 {
		return
	}
	d.carry = append(d.carry, samples...)
	i := 0
	for i+d.hop <= len(d.carry) {
		h := d.carry[i : i+d.hop]
		p := TonePower(h, d.sr, d.pitch)
		off := float32(math.Inf(1))
		for _, df := range offNoiseHz {
			off = min32(off, TonePower(h, d.sr, d.pitch+df))
			if d.pitch-df >= 250 {
				off = min32(off, TonePower(h, d.sr, d.pitch-df))
			}
		}
		if math.IsInf(float64(off), 0) {
			off = d.noise
		}
		i += d.hop
		span := max32(d.peak-d.noise, 0)
		d.present = d.peak >= max32(d.noise, d.offNoise)*d.presentMult() && span > 1e-9
		d.hiThres = d.noise + d.hiFrac()*span
		d.loThresh = d.noise + d.loFrac()*span
		d.step(p)
		if p > d.peak {
			d.peak = p
		} else if d.improved {
			// Release toward the current level (fast while keyed, slower in gaps), but never
			// below a margin over the true noise floor, or noise would key itself.
			k := float32(0.996)
			if d.keyDown {
				k = 0.9
			}
			floor := max32(d.noise, d.offNoise) * d.snrMult() * 4
			d.peak = max32(d.peak*k+p*(1-k), min32(d.peak, floor))
		} else {
			d.peak = d.peak*0.999 + p*0.001
		}
		if p < d.noise {
			d.noise = p
		} else {
			d.noise = d.noise*0.9995 + p*0.0005
		}
		d.offNoise = d.offNoise*0.98 + off*0.02
	}
	d.carry = append(d.carry[:0], d.carry[i:]...)
}

func (d *StreamDecoder) step(p float32) {
	if d.keyDown {
		d.run++
		d.markPeak = max32(d.markPeak, p)
		if p < d.loThresh {
			d.onMarkEnd()
			d.keyDown = false
			d.run = 0
		}
	} else if d.present && p >= d.hiThres {
		if d.improved {
			d.gaps = append(d.gaps, float32(d.spaceRun))
			if len(d.gaps) > 16 {
				d.gaps = d.gaps[len(d.gaps)-16:]
			}
		}
		d.keyDown = true
		d.run = 1
		d.markPeak = p
	} else {
		d.spaceRun++
		d.onGapTick()
	}
}

func (d *StreamDecoder) onMarkEnd() {
	spikeMin := max32(d.spikeFrac()*d.ditAvg, 1)
	strong := d.markPeak >= max32(d.noise, d.offNoise)*d.snrMult()
	if float32(d.run) < spikeMin || !strong {
		// Improved mode: a strong mark too short for the CURRENT speed guess may be a
		// real dit from a faster sender, so let the speed re-sync see it (it is still
		// not decoded). Anything faster than 60 WPM is ignored.
		if d.improved && strong && float32(d.run) >= d.ditHops(60) {
			d.resync(float32(d.run))
		}
		d.spaceRun += d.run
		return
	}
	d.sawMark = true
	run := float32(d.run)
	if run < (d.ditAvg+d.dahAvg)*0.5 {
		d.sym = append(d.sym, '.')
		d.ditAvg += 0.25 * (run - d.ditAvg)
	} else {
		d.sym = append(d.sym, '-')
		d.dahAvg += 0.25 * (run - d.dahAvg)
	}
	d.spaceRun = 0
	d.flushedChar = false
	d.flushedWord = false
	if d.improved {
		d.resync(run)
	}
}

// ditHops is the dit length, in envelope hops, at the given speed.
func (d *StreamDecoder) ditHops(wpm float32) float32 {
	return (1.2 / wpm) * d.sr / float32(d.hop)
}

func (d *StreamDecoder) resync(run float32) {
	d.marks = append(d.marks, run)
	if len(d.marks) > 12 {
		d.marks = d.marks[len(d.marks)-12:]
	}
	if len(d.marks) < 6 {
		return
	}
	lo, hi := d.marks[0], d.marks[0]
	for _, m := range d.marks {
		lo, hi = min32(lo, m), max32(hi, m)
	}
	if hi < 2*lo {
		return // all one length (e.g. "EEE" or "TOM"): nothing to learn
	}
	c1, c2 := lo, hi
	var n1, n2 int
	for it := 0; it < 8; it++ {
		var s1, s2 float32
		n1, n2 = 0, 0
		for _, m := range d.marks {
			if m-c1 < c2-m {
				s1 += m
				n1++
			} else {
				s2 += m
				n2++
			}
		}
		if n1 == 0 || n2 == 0 {
			return
		}
		c1, c2 = s1/float32(n1), s2/float32(n2)
	}
	if c1 < d.ditHops(60) || c1 > d.ditHops(5) {
		return // not a human sending speed
	}
	if r := c2 / c1; r < 2 || r > 5 || n1 < 2 || n2 < 2 {
		return
	}
	// Real keying gives two TIGHT groups; static crashes are spread all over and can
	// always be split in two. Only trust tight groups (coefficient of variation < 0.3),
	// otherwise re-syncing would defeat Nexus's timing-plausibility noise squelch.
	var v1, v2 float32
	for _, m := range d.marks {
		if m-c1 < c2-m {
			v1 += (m - c1) * (m - c1)
		} else {
			v2 += (m - c2) * (m - c2)
		}
	}
	cv1 := float32(math.Sqrt(float64(v1/float32(n1)))) / c1
	cv2 := float32(math.Sqrt(float64(v2/float32(n2)))) / c2
	if cv1 > 0.3 || cv2 > 0.3 {
		return
	}
	// Real Morse also has many ~1-dit gaps (between the elements of a character);
	// gaps between static crashes are random. Require that too.
	if len(d.gaps) < 8 {
		return
	}
	near := 0
	for _, g := range d.gaps {
		if g >= 0.5*c1 && g <= 1.6*c1 {
			near++
		}
	}
	if float32(near) < 0.45*float32(len(d.gaps)) {
		return
	}
	mid, trueMid := (d.ditAvg+d.dahAvg)*0.5, (c1+c2)*0.5
	if mid > trueMid*1.25 || mid < trueMid*0.8 {
		d.ditAvg, d.dahAvg = c1, c2
		d.Resyncs++
	}
}

func (d *StreamDecoder) onGapTick() {
	g := float32(d.spaceRun)
	if !d.flushedChar && len(d.sym) > 0 && g >= 2*d.ditAvg {
		if d.timingPlausible() {
			if c, ok := morseToChar(string(d.sym)); ok {
				d.pushChar(byte(c))
			}
		}
		d.sym = d.sym[:0]
		d.flushedChar = true
	}
	if !d.flushedWord && d.flushedChar && g >= 5*d.ditAvg {
		d.pushChar(' ')
		d.flushedWord = true
	}
}

func (d *StreamDecoder) pushChar(c byte) {
	d.transcript = append(d.transcript, c)
	if len(d.transcript) > transcriptCap {
		drop := len(d.transcript) - transcriptCap
		d.transcript = append(d.transcript[:0], d.transcript[drop:]...)
	}
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
