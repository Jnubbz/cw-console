package spectrum

import "math"

// Config is one analyzer on one capture stream. Zero values are the defaults:
// a 4096-point Hann FFT, ten frames a second.
type Config struct {
	Rate      int
	FFTSize   int
	FrameRate float64
}

// View is the slice of that FFT a mode draws, plus how the row is scaled.
type View struct {
	Span      Span
	Lift      float64 // dB added before the contrast scale (brightness)
	Range     float64 // dB from the noise floor to full brightness (contrast)
	FloorFrom float64 // columns centered below this are left out of the median
	FFT       int
	FPS       float64
}

// Analyzer turns one audio stream into magnitude frames. It does not touch
// the samples it is given. Push copies what it needs.
type Analyzer struct {
	n    int
	hop  int
	rate int
	win  []float64
	acc  []float32
	seen int
	next int
}

// New starts an analyzer. The first frame waits until FFTSize samples have arrived.
func New(cfg Config) *Analyzer {
	cfg = normalize(cfg)
	a := &Analyzer{n: cfg.FFTSize, rate: cfg.Rate, win: Hann(cfg.FFTSize)}
	a.hop = int(math.Round(float64(cfg.Rate) / cfg.FrameRate))
	if a.hop < 1 {
		a.hop = 1
	}
	a.next = a.n
	return a
}

func normalize(cfg Config) Config {
	if cfg.Rate <= 0 {
		cfg.Rate = 12000
	}
	if cfg.FrameRate <= 0 {
		cfg.FrameRate = 10
	}
	if cfg.FrameRate < 2 {
		cfg.FrameRate = 2
	}
	if cfg.FrameRate > 20 {
		cfg.FrameRate = 20
	}
	cfg.FFTSize = SnapFFT(cfg.FFTSize)
	return cfg
}

// SnapFFT clamps n to a power of two from 512 to 8192. Zero means 4096.
func SnapFFT(n int) int {
	if n <= 0 {
		return 4096
	}
	p := 512
	for p < n && p < 8192 {
		p *= 2
	}
	if p > 8192 {
		return 8192
	}
	return p
}

// Size is the FFT length.
func (a *Analyzer) Size() int { return a.n }

// Rate is the sample rate the bin math uses.
func (a *Analyzer) Rate() int { return a.rate }

// Push appends samples and returns one magnitude frame for each hop that completed.
// A frame is |X[k]| for k = 0 .. n/2-1. At most 8 frames are returned per call;
// a backlog is skipped so a stall cannot queue a burst of FFTs.
func (a *Analyzer) Push(samples []float32) [][]float64 {
	if a == nil || len(samples) == 0 {
		return nil
	}
	a.acc = append(a.acc, samples...)
	a.seen += len(samples)
	if a.seen-a.next > a.hop*8 {
		a.next = a.seen - a.hop*8
	}
	var out [][]float64
	for a.seen >= a.next && len(out) < 8 {
		end := len(a.acc) - (a.seen - a.next)
		start := end - a.n
		if start < 0 || end > len(a.acc) {
			break
		}
		out = append(out, Magnitudes(a.acc[start:end], a.win))
		a.next += a.hop
	}
	keep := a.n + a.hop
	if len(a.acc) > keep {
		drop := len(a.acc) - keep
		a.acc = append(a.acc[:0], a.acc[drop:]...)
	}
	return out
}

// BinHz is the center frequency of FFT bin k.
func BinHz(rate, n, k int) float64 {
	if n <= 0 {
		return 0
	}
	return float64(k) * float64(rate) / float64(n)
}

// BinAt is the fractional FFT bin whose center is hz.
func BinAt(rate, n int, hz float64) float64 {
	if rate <= 0 {
		return 0
	}
	return hz * float64(n) / float64(rate)
}

// ColumnsDB maps a magnitude frame onto span, one dB value per column.
// A column wider than a bin takes the loudest bin whose center falls in it
// (the FT8 waterfall collapsed two bins this way). A column narrower than a
// bin interpolates the two bins around its center (the CW waterfall).
func ColumnsDB(mag []float64, rate, n int, span Span) []float64 {
	if span.Cols < 1 || n < 2 || rate <= 0 || len(mag) == 0 {
		return nil
	}
	out := make([]float64, span.Cols)
	binHz := float64(rate) / float64(n)
	width := (span.HiHz - span.LoHz) / float64(span.Cols)
	if width <= 0 {
		width = binHz
	}
	for c := range out {
		left := span.LoHz + float64(c)*width
		right := left + width
		k0 := int(math.Ceil(left/binHz - 1e-12))
		k1 := int(math.Floor((right - 1e-12) / binHz))
		if k0 < 0 {
			k0 = 0
		}
		if k1 >= len(mag) {
			k1 = len(mag) - 1
		}
		var m float64
		if k0 <= k1 {
			m = mag[k0]
			for k := k0 + 1; k <= k1; k++ {
				if mag[k] > m {
					m = mag[k]
				}
			}
		} else {
			m = interpMag(mag, (left+right)/2/binHz)
		}
		out[c] = 20 * math.Log10(m+1e-9)
	}
	return out
}

func interpMag(mag []float64, k float64) float64 {
	if len(mag) == 1 {
		return mag[0]
	}
	if k < 0 {
		k = 0
	}
	i := int(k)
	if i >= len(mag)-1 {
		return mag[len(mag)-1]
	}
	fr := k - float64(i)
	return (1-fr)*mag[i] + fr*mag[i+1]
}

// Median returns the middle value of a copy of xs. An empty slice is 0.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	// insertion sort: a few hundred columns
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i
		for j > 0 && s[j-1] > v {
			s[j] = s[j-1]
			j--
		}
		s[j] = v
	}
	return s[len(s)/2]
}

// NoiseMedian is the median of columns whose center is at or above fromHz.
// fromHz 0 uses every column. The FT8 display ignores the rig's low-frequency rolloff.
func NoiseMedian(db []float64, span Span, fromHz float64) float64 {
	if len(db) == 0 {
		return 0
	}
	if fromHz <= span.LoHz {
		return Median(db)
	}
	width := (span.HiHz - span.LoHz) / float64(len(db))
	var s []float64
	for c, v := range db {
		center := span.LoHz + (float64(c)+0.5)*width
		if center >= fromHz {
			s = append(s, v)
		}
	}
	if len(s) == 0 {
		return Median(db)
	}
	return Median(s)
}

// TrackFloor is the smoothed noise floor both old displays used: the first
// sample is taken as-is, and later samples move the floor by alpha (0.1).
// A short burst therefore cannot lift the floor by more than alpha of the jump.
func TrackFloor(prev, sample, alpha float64, first bool) float64 {
	if first {
		return sample
	}
	if alpha <= 0 {
		return prev
	}
	if alpha >= 1 {
		return sample
	}
	return (1-alpha)*prev + alpha*sample
}

// Levels maps dB columns to 0-255. lift is brightness, rangeDB is contrast.
// 0 is black at rangeDB below (floor-lift), 254 is full scale.
func Levels(db []float64, floor, lift, rangeDB float64) []byte {
	if rangeDB <= 0 {
		rangeDB = 36
	}
	out := make([]byte, len(db))
	for i, v := range db {
		x := (v - floor + lift) / rangeDB
		if x < 0 {
			x = 0
		} else if x > 1 {
			x = 1
		}
		out[i] = byte(x*254 + 0.5)
	}
	return out
}

// Trace maps the same columns to the 0-40 scale the spectrum line is drawn with.
// The line uses a wider range than the waterfall, so a strong signal stays a
// peak instead of a flat top while the waterfall contrast stays adjustable.
func Trace(db []float64, floor, lift, rangeDB float64) []float32 {
	line := 80.0
	if rangeDB > line {
		line = rangeDB
	}
	out := make([]float32, len(db))
	for i, v := range db {
		x := (v - floor + lift) / line
		if x < 0 {
			x = 0
		} else if x > 1 {
			x = 1
		}
		out[i] = float32(x * 40)
	}
	return out
}

// ClickHz is the frequency at a fraction of the visible span.
// 0 is the left edge and 1 is the right edge, which is how both canvases
// turn a click into a frequency today.
func ClickHz(lo, hi, fraction float64) float64 {
	if fraction < 0 {
		fraction = 0
	} else if fraction > 1 {
		fraction = 1
	}
	return lo + fraction*(hi-lo)
}

// CWClickPitch is the historical CW canvas: 200–1400 Hz across the width,
// rounded to 5 Hz and clamped to the pitch slider (300–1200).
func CWClickPitch(fraction float64) float64 {
	return roundClamp(ClickHz(200, 1400, fraction), 5, 300, 1200)
}

// FT8ClickHz is the historical FT8 canvas: 0–3000 Hz across the width,
// rounded to 10 Hz and clamped to the TX frequency box (200–2900).
func FT8ClickHz(fraction float64) float64 {
	return roundClamp(ClickHz(0, 3000, fraction), 10, 200, 2900)
}

func roundClamp(v, step, lo, hi float64) float64 {
	if step <= 0 {
		step = 1
	}
	v = math.Round(v/step) * step
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Zoom narrows or widens span around hz. factor < 1 zooms in.
// The width stays between 100 Hz and 6 kHz, and the span stays in 0..6 kHz.
func Zoom(span Span, factor, around float64) Span {
	if factor <= 0 {
		factor = 1
	}
	width := (span.HiHz - span.LoHz) * factor
	if width < 100 {
		width = 100
	}
	if width > 6000 {
		width = 6000
	}
	lo := around - width/2
	if lo < 0 {
		lo = 0
	}
	hi := lo + width
	if hi > 6000 {
		hi = 6000
		lo = hi - width
		if lo < 0 {
			lo = 0
		}
	}
	span.LoHz, span.HiHz = lo, hi
	span.Name = "custom"
	return span
}

// Resolve picks the span and the scaling for a mode. An empty preset uses
// the range that mode already drew: CW 200–1400 Hz (the click math on the
// current canvas) and FT8 0–3000 Hz at 256 columns. Named presets are
// SpanCW, SpanFT8 and SpanSSTV. "custom" uses lo/hi.
func Resolve(mode, preset string, lo, hi float64, cols int, lift, rng float64, fft int, fps float64) View {
	v := View{Lift: 3, Range: 36, FFT: 4096, FPS: 10}
	switch preset {
	case "cw":
		v.Span = SpanCW
	case "ft8":
		v.Span = SpanFT8
		v.Range = 30
		v.FloorFrom = 200
		v.FPS = 8
	case "sstv":
		v.Span = SpanSSTV
	case "custom":
		v.Span = Span{Name: "custom", LoHz: lo, HiHz: hi, Cols: cols}
		if mode == "ft8" {
			v.Range = 30
			v.FloorFrom = 200
			v.FPS = 8
		}
		if v.Span.HiHz-v.Span.LoHz < 50 || v.Span.LoHz < 0 || v.Span.HiHz > 24000 {
			if mode == "ft8" {
				v.Span = SpanFT8
			} else {
				v.Span = Span{Name: "cw-wide", LoHz: 200, HiHz: 1400, Cols: 241}
			}
		}
		if cols < 32 || cols > 512 {
			v.Span.Cols = 256
		}
	default:
		if mode == "ft8" {
			v.Span = SpanFT8
			v.Range = 30
			v.FloorFrom = 200
			v.FPS = 8
		} else {
			v.Span = Span{Name: "cw-wide", LoHz: 200, HiHz: 1400, Cols: 241}
		}
	}
	if rng > 0 || lift != 0 {
		if rng > 0 {
			if rng < 6 {
				rng = 6
			}
			if rng > 80 {
				rng = 80
			}
			v.Range = rng
		}
		if lift < -12 {
			lift = -12
		}
		if lift > 24 {
			lift = 24
		}
		v.Lift = lift
	}
	if fft > 0 {
		v.FFT = SnapFFT(fft)
	}
	if fps > 0 {
		if fps < 2 {
			fps = 2
		}
		if fps > 20 {
			fps = 20
		}
		v.FPS = fps
	}
	return v
}
