package sstv

import (
	"image"
	"math"
)

// EventKind says what happened in the decoder.
type EventKind int

const (
	Started EventKind = iota // a header was heard: Mode and OffsetHz are set, Image is blank
	Line                     // one more line is in Image
	Done                     // the picture is complete (and straightened)
	Aborted                  // the signal went away right after the header
)

// Event is what Write reports. Image is the decoder's own picture until Done: copy it
// to keep it. On Done it is a finished picture the caller owns.
type Event struct {
	Kind     EventKind
	Mode     *Mode
	Line     int // lines received so far (picture rows)
	Image    *image.RGBA
	OffsetHz float64 // how far off-tune the signal is (corrected for)
	SlantPPM float64 // the sender's clock against ours (corrected for)
	Synced   int     // lines whose sync pulse was found
	Joined   bool    // no header was heard: locked on partway through, from the line syncs
}

// Decoder turns audio into pictures. Feed it audio with Write as it arrives.
type Decoder struct {
	fs  float64
	dec int
	fw  float64 // working rate: fs/dec

	// FM demodulator: mix to 1900 Hz, low-pass, decimate, phase difference
	taps  []float64
	ringI []float64
	ringQ []float64
	rpos  int
	cnt   int
	osc   float64
	pI    float64
	pQ    float64

	freq []float32 // instantaneous frequency at the working rate
	scan int       // where the header search is up to
	cur  *rx

	// the search for line sync pulses (a picture already under way)
	pscan    int
	inLow    bool
	lowStart int
	lowSum   float64
	pulses   []pulse
}

// pulse is a short 1200 Hz-ish burst: maybe a line sync.
type pulse struct {
	pos   float64 // where it starts, in freq
	width float64 // samples
	f     float64 // its mean frequency
}

type rx struct {
	mode    *Mode
	off     float64
	lineN   float64 // nominal line length, samples
	syncAt  float64 // sync start from line start, samples (nominal)
	segs    []seg
	a, b    float64 // sync of line k is at a + b*k
	a0      float64
	ks, ys  []float64 // found syncs
	next    int
	planes  [3][]uint8
	img     *image.RGBA
	isSecnd []bool // Robot 36: line carries B-Y
	joined  bool   // started from line syncs, not a header
	lastK   int    // last line whose sync was found
}

// NewDecoder makes a decoder for audio at sampleRate (e.g. 48000).
func NewDecoder(sampleRate int) *Decoder {
	d := &Decoder{fs: float64(sampleRate)}
	d.dec = int(math.Round(d.fs / 12000))
	if d.dec < 1 {
		d.dec = 1
	}
	d.fw = d.fs / float64(d.dec)
	// windowed-sinc low-pass: passes the +-1000 Hz around 1900, stops the mixer's image
	n := int(3.3*d.fs/1600) | 1
	fc := 1400 / d.fs
	d.taps = make([]float64, n)
	sum := 0.0
	for i := range d.taps {
		x := float64(i - n/2)
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		d.taps[i] = s * (0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1)))
		sum += d.taps[i]
	}
	for i := range d.taps {
		d.taps[i] /= sum
	}
	d.ringI, d.ringQ = make([]float64, n), make([]float64, n)
	return d
}

// Receiving reports the mode being received, or nil.
func (d *Decoder) Receiving() *Mode {
	if d.cur == nil {
		return nil
	}
	return d.cur.mode
}

// Reset drops the picture in progress and goes back to listening for a header.
func (d *Decoder) Reset() {
	d.cur = nil
	d.freq = d.freq[:0]
	d.idle(0)
}

// idle: back to searching, from position from.
func (d *Decoder) idle(from int) {
	d.cur = nil
	d.scan, d.pscan = from, from
	d.inLow = false
	d.pulses = d.pulses[:0]
}

// Write takes more audio (-1..1) and returns what happened.
func (d *Decoder) Write(samples []float32) []Event {
	d.demod(samples)
	var ev []Event
	for {
		if d.cur == nil {
			if !d.findHeader(&ev) && !d.findSyncs(&ev) {
				d.trim()
				break
			}
		}
		if !d.receive(&ev) {
			break
		}
	}
	return ev
}

func (d *Decoder) demod(samples []float32) {
	n := len(d.taps)
	step := 2 * math.Pi * freqLead / d.fs
	for _, s := range samples {
		d.osc += step
		if d.osc > 2*math.Pi {
			d.osc -= 2 * math.Pi
		}
		d.ringI[d.rpos] = float64(s) * math.Cos(d.osc)
		d.ringQ[d.rpos] = -float64(s) * math.Sin(d.osc)
		d.rpos++
		if d.rpos == n {
			d.rpos = 0
		}
		d.cnt++
		if d.cnt < d.dec {
			continue
		}
		d.cnt = 0
		var I, Q float64
		j := d.rpos
		for _, t := range d.taps {
			I += t * d.ringI[j]
			Q += t * d.ringQ[j]
			j++
			if j == n {
				j = 0
			}
		}
		// angle of z * conj(prev)
		re := I*d.pI + Q*d.pQ
		im := Q*d.pI - I*d.pQ
		d.pI, d.pQ = I, Q
		d.freq = append(d.freq, float32(freqLead+math.Atan2(im, re)*d.fw/(2*math.Pi)))
	}
}

func (d *Decoder) mean(from, to float64) float64 {
	a, b := int(math.Ceil(from)), int(math.Floor(to))
	if a < 0 {
		a = 0
	}
	if b > len(d.freq) {
		b = len(d.freq)
	}
	if b <= a {
		i := int(math.Round((from + to) / 2))
		if i < 0 {
			i = 0
		}
		if i >= len(d.freq) {
			i = len(d.freq) - 1
		}
		if i < 0 {
			return 0
		}
		return float64(d.freq[i])
	}
	s := 0.0
	for _, f := range d.freq[a:b] {
		s += float64(f)
	}
	return s / float64(b-a)
}

// lowest finds x in [from, to] where the mean over [x, x+width] is lowest (a sync pulse).
func (d *Decoder) lowest(from, to, width int) (int, float64) {
	if from < 0 {
		from = 0
	}
	if to+width > len(d.freq) {
		to = len(d.freq) - width
	}
	if to < from {
		return from, math.MaxFloat64
	}
	s := 0.0
	for _, f := range d.freq[from : from+width] {
		s += float64(f)
	}
	best, bestS := from, s
	for x := from + 1; x <= to; x++ {
		s += float64(d.freq[x+width-1]) - float64(d.freq[x-1])
		if s < bestS {
			best, bestS = x, s
		}
	}
	return best, bestS / float64(width)
}

// findHeader looks for leader + VIS. On success it starts a picture and returns true;
// false means more audio is needed.
func (d *Decoder) findHeader(ev *[]Event) bool {
	ms := d.fw / 1000
	lead, bit := int(250*ms), 30*ms
	need := int(305 * ms)
	step := int(2 * ms)
	if d.scan < lead {
		d.scan = lead
	}
	for ; d.scan+need < len(d.freq); d.scan += step {
		p := d.scan
		// cheap first look: a low tone here, a steady 1900-ish before it
		if sb := d.mean(float64(p)+5*ms, float64(p)+25*ms); sb > 1450 || sb < 950 {
			continue
		}
		ld := d.mean(float64(p-lead), float64(p)-20*ms)
		off := ld - freqLead
		if math.Abs(off) > 150 {
			continue
		}
		v := 0.0
		for _, f := range d.freq[p-lead : p-int(20*ms)] {
			v += (float64(f) - ld) * (float64(f) - ld)
		}
		if math.Sqrt(v/float64(lead-int(20*ms))) > 200 {
			continue
		}
		// the exact start of the start bit
		x, sb := d.lowest(p-int(6*ms), p+int(6*ms), int(bit))
		if math.Abs(sb-(FreqSync+off)) > 80 {
			continue
		}
		s := float64(x)
		code, ones, ok := 0, 0, true
		for i := 0; i < 8 && ok; i++ {
			f := d.mean(s+(float64(i)+1.17)*bit, s+(float64(i)+1.83)*bit) - off
			switch {
			case math.Abs(f-1100) < 70:
				ones++
				if i < 7 {
					code |= 1 << i
				}
			case math.Abs(f-1300) < 70:
			default:
				ok = false
			}
		}
		if !ok || ones%2 != 0 {
			continue
		}
		if stop := d.mean(s+9.17*bit, s+9.83*bit) - off; math.Abs(stop-FreqSync) > 80 {
			continue
		}
		m := ByVIS(byte(code))
		if m == nil {
			continue
		}
		d.start(m, s+10*bit, off)
		*ev = append(*ev, Event{Kind: Started, Mode: m, Image: d.cur.img, OffsetHz: off})
		return true
	}
	return false
}

// trim keeps the last few seconds while idle: enough to go back to the first lines of a
// picture found by its syncs.
func (d *Decoder) trim() {
	keep := int(6 * d.fw)
	if len(d.freq) < keep+int(2*d.fw) {
		return
	}
	cut := len(d.freq) - keep
	d.freq = append(d.freq[:0], d.freq[cut:]...)
	d.scan = max(d.scan-cut, 0)
	d.pscan = max(d.pscan-cut, 0)
	d.lowStart -= cut
	kept := d.pulses[:0]
	for _, p := range d.pulses {
		if p.pos -= float64(cut); p.pos >= 0 {
			kept = append(kept, p)
		}
	}
	d.pulses = kept
}

// byLine: the modes, shortest line first (so Robot 36 is tried before Robot 72, whose
// line is twice as long and would match every other pulse).
var byLine = func() []*Mode {
	out := append([]*Mode{}, Modes...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			li, _, _ := out[i].layout()
			lj, _, _ := out[j].layout()
			if lj < li {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}()

// findSyncs looks for a picture already under way: sync pulses one line apart. Each
// mode's line length is different, so the spacing says which mode it is. On success it
// starts a picture at the earliest line still in memory and returns true.
func (d *Decoder) findSyncs(ev *[]Event) bool {
	ms := d.fw / 1000
	h := int(1 * ms) // smooth over 2 ms
	if d.pscan < h {
		d.pscan = h
	}
	if d.pscan+h >= len(d.freq) {
		return false
	}
	sum := 0.0
	for _, f := range d.freq[d.pscan-h : d.pscan+h] {
		sum += float64(f)
	}
	for ; d.pscan+h < len(d.freq); d.pscan++ {
		i := d.pscan
		low := sum/float64(2*h) < 1400
		sum += float64(d.freq[i+h]) - float64(d.freq[i-h])
		switch {
		case low && !d.inLow:
			d.inLow, d.lowStart, d.lowSum = true, i, 0
			fallthrough
		case low:
			d.lowSum += float64(d.freq[i])
		case d.inLow:
			d.inLow = false
			w := float64(i - d.lowStart)
			if w < 3.5*ms || w > 26*ms || d.lowStart < 0 {
				continue
			}
			d.pulses = append(d.pulses, pulse{pos: float64(d.lowStart), width: w, f: d.lowSum / w})
			if m, first, off := d.train(); m != nil {
				d.pscan++
				d.startAt(m, first, off, true)
				*ev = append(*ev, Event{Kind: Started, Mode: m, Image: d.cur.img, OffsetHz: off, Joined: true})
				return true
			}
		}
	}
	return false
}

// train: does the newest pulse end a run of pulses one line apart? It wants 4 of the
// last 5 line positions filled, and a pulse as long as the mode's sync.
func (d *Decoder) train() (*Mode, float64, float64) {
	ms := d.fw / 1000
	n := len(d.pulses)
	last := d.pulses[n-1]
	for _, m := range byLine {
		if w := last.width / ms; w < m.Sync*1000*0.55 || w > m.Sync*1000*1.45 {
			continue
		}
		line, syncAt, _ := m.layout()
		T := line * d.fw
		hits, first, fsum := 1, last.pos, last.f
		for j := 1; j <= 4; j++ {
			want := last.pos - float64(j)*T
			tol := 1*ms + float64(j)*0.001*T
			for k := n - 2; k >= 0 && d.pulses[k].pos > want-tol; k-- {
				p := d.pulses[k]
				if math.Abs(p.pos-want) <= tol && p.width/ms > m.Sync*1000*0.55 && p.width/ms < m.Sync*1000*1.45 {
					hits++
					first = p.pos
					fsum += p.f
					break
				}
			}
		}
		if hits < 4 {
			continue
		}
		off := fsum/float64(hits) - FreqSync
		if math.Abs(off) > 200 {
			continue
		}
		for first-syncAt*d.fw < 0 { // Scottie's sync is mid-line: the line must be whole
			first += T
		}
		return m, first, off
	}
	return nil, 0, 0
}

func (d *Decoder) start(m *Mode, t0, off float64) {
	_, syncAt, _ := m.layout()
	if m.Family == Scottie {
		t0 += m.Sync * d.fw // the starting sync
	}
	d.startAt(m, t0+syncAt*d.fw, off, false)
}

// startAt begins a picture whose first line's sync pulse is at a0.
func (d *Decoder) startAt(m *Mode, a0, off float64, joined bool) {
	line, syncAt, segs := m.layout()
	r := &rx{mode: m, off: off, lineN: line * d.fw, syncAt: syncAt * d.fw, segs: segs, joined: joined}
	r.a0 = a0
	r.a, r.b = r.a0, r.lineN
	for i := range r.planes {
		r.planes[i] = make([]uint8, m.Width*m.Height)
		if i > 0 && !m.rgb() {
			for j := range r.planes[i] {
				r.planes[i][j] = 128
			}
		}
	}
	r.img = image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	for i := 3; i < len(r.img.Pix); i += 4 {
		r.img.Pix[i] = 255
	}
	r.isSecnd = make([]bool, m.radioLines())
	d.cur = r
}

// receive decodes the lines whose audio has arrived. It returns true when the picture
// ended (so the caller looks for the next header), false when it needs more audio.
func (d *Decoder) receive(ev *[]Event) bool {
	r := d.cur
	m := r.mode
	ms := d.fw / 1000
	lines := m.radioLines()
	rows := m.Height / lines
	for r.next < lines {
		k := float64(r.next)
		pred := r.a + r.b*k
		if int(pred-r.syncAt+r.lineN+12*ms) >= len(d.freq) {
			return false
		}
		w := int(m.Sync * d.fw)
		x, f := d.lowest(int(pred-5*ms), int(pred+5*ms), w)
		if f < FreqSync+r.off+120 {
			r.ks, r.ys = append(r.ks, k), append(r.ys, float64(x))
			r.lastK = r.next
			d.fit(r)
		}
		if r.next == 12 && len(r.ks) < 3 { // a false start, or the signal went away
			d.idle(int(pred))
			*ev = append(*ev, Event{Kind: Aborted, Mode: m, Joined: r.joined})
			return true
		}
		// The picture ends when the syncs do: a transmission that stops early (or fades out
		// for good) must not run on into noise or into the next picture.
		if r.next-r.lastK > 20 && r.next > 20 {
			lines = r.lastK + 1
			break
		}
		d.line(r, r.next)
		r.next++
		*ev = append(*ev, Event{Kind: Line, Mode: m, Line: r.next * rows, Image: r.img, OffsetHz: r.off,
			SlantPPM: (r.b/r.lineN - 1) * 1e6, Synced: len(r.ks), Joined: r.joined})
	}
	// the whole picture again with the final timing: straightens the early lines
	d.fit(r)
	for k := 0; k < lines; k++ {
		d.line(r, k)
	}
	img := r.img
	if got := lines * rows; got < m.Height { // ended early: keep only what was received
		img = image.NewRGBA(image.Rect(0, 0, m.Width, got))
		copy(img.Pix, r.img.Pix[:got*m.Width*4])
	}
	*ev = append(*ev, Event{Kind: Done, Mode: m, Line: lines * rows, Image: img, OffsetHz: r.off,
		SlantPPM: (r.b/r.lineN - 1) * 1e6, Synced: len(r.ks), Joined: r.joined})
	end := int(r.a + r.b*float64(lines) - r.syncAt)
	if end > len(d.freq) {
		end = len(d.freq)
	}
	if end < 0 {
		end = 0
	}
	d.freq = append(d.freq[:0], d.freq[end:]...)
	d.idle(0)
	return true
}

// fit: sync positions against line numbers, least squares, wild ones left out.
func (d *Decoder) fit(r *rx) {
	n := len(r.ks)
	if n == 0 {
		return
	}
	if n < 6 { // too few for a slope: nominal line length, measured position
		s := 0.0
		for i := range r.ks {
			s += r.ys[i] - r.lineN*r.ks[i]
		}
		r.a, r.b = s/float64(n), r.lineN
		return
	}
	use := make([]bool, n)
	for i := range use {
		use[i] = true
	}
	a, b := r.a, r.b
	for pass := 0; pass < 3; pass++ {
		var sk, sy, skk, sky, c float64
		for i := range r.ks {
			if use[i] {
				sk += r.ks[i]
				sy += r.ys[i]
				skk += r.ks[i] * r.ks[i]
				sky += r.ks[i] * r.ys[i]
				c++
			}
		}
		den := c*skk - sk*sk
		if c < 6 || den == 0 {
			break
		}
		b = (c*sky - sk*sy) / den
		a = (sy - b*sk) / c
		tol := d.fw / 1000 * (1.5 - 0.4*float64(pass))
		for i := range r.ks {
			use[i] = math.Abs(r.ys[i]-(a+b*r.ks[i])) < tol
		}
	}
	if math.Abs(b/r.lineN-1) > 0.005 {
		return // not believable: keep the last fit
	}
	r.a, r.b = a, b
}

// line reads radio line k into the planes and the picture.
func (d *Decoder) line(r *rx, k int) {
	m := r.mode
	W := m.Width
	rate := r.b / r.lineN
	start := r.a + r.b*float64(k) - r.syncAt*rate
	read := func(s seg, n int, out []uint8, stride int) {
		t := start + s.start*d.fw*rate
		per := s.dur * d.fw * rate / float64(n)
		for i := 0; i < n; i++ {
			f := d.mean(t+float64(i)*per, t+float64(i+1)*per) - r.off
			v := clamp8((f - FreqBlack) * 255 / (FreqWhite - FreqBlack))
			for j := 0; j < stride; j++ {
				out[i*stride+j] = v
			}
		}
	}
	switch m.Family {
	case Martin, Scottie:
		for _, s := range r.segs {
			read(s, W, r.planes[s.plane][k*W:(k+1)*W], 1)
		}
		d.paint(r, k)
	case Robot36:
		read(r.segs[0], W, r.planes[0][k*W:(k+1)*W], 1)
		// which colour difference this line carries: its separator says (1500 = R-Y,
		// 2300 = B-Y); fall back on odd/even
		c := r.segs[1]
		sepAt := start + (c.start-m.Sep)*d.fw*rate
		sep := d.mean(sepAt+0.5e-3*d.fw, sepAt+4e-3*d.fw) - r.off
		second := k%2 == 1
		if sep < 1700 {
			second = false
		} else if sep > 2100 {
			second = true
		}
		r.isSecnd[k] = second
		plane := 1
		if second {
			plane = 2
		}
		read(c, W/2, r.planes[plane][k*W:(k+1)*W], 2)
		// the pair's other line shares it
		o := k + 1
		if second {
			o = k - 1
		}
		if o >= 0 && o < m.Height {
			copy(r.planes[plane][o*W:(o+1)*W], r.planes[plane][k*W:(k+1)*W])
			d.paint(r, o)
		}
		d.paint(r, k)
	case Robot72:
		read(r.segs[0], W, r.planes[0][k*W:(k+1)*W], 1)
		read(r.segs[1], W/2, r.planes[1][k*W:(k+1)*W], 2)
		read(r.segs[2], W/2, r.planes[2][k*W:(k+1)*W], 2)
		d.paint(r, k)
	case PD:
		y0, y1 := 2*k, 2*k+1
		read(r.segs[0], W, r.planes[0][y0*W:(y0+1)*W], 1)
		read(r.segs[1], W, r.planes[1][y0*W:(y0+1)*W], 1)
		read(r.segs[2], W, r.planes[2][y0*W:(y0+1)*W], 1)
		read(r.segs[3], W, r.planes[0][y1*W:(y1+1)*W], 1)
		copy(r.planes[1][y1*W:(y1+1)*W], r.planes[1][y0*W:(y0+1)*W])
		copy(r.planes[2][y1*W:(y1+1)*W], r.planes[2][y0*W:(y0+1)*W])
		d.paint(r, y0)
		d.paint(r, y1)
	}
}

// paint turns picture row y of the planes into RGBA.
func (d *Decoder) paint(r *rx, y int) {
	W := r.mode.Width
	rgb := r.mode.rgb()
	for x := 0; x < W; x++ {
		i := y*W + x
		p := r.img.Pix[i*4:]
		if rgb {
			p[0], p[1], p[2] = r.planes[0][i], r.planes[1][i], r.planes[2][i]
			continue
		}
		R, G, B := fromYC(float64(r.planes[0][i]), float64(r.planes[1][i]), float64(r.planes[2][i]))
		p[0], p[1], p[2] = clamp8(R), clamp8(G), clamp8(B)
	}
}
