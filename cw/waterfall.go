package cw

// Waterfall rows: an FFT of the latest audio (Hann window, ~6 Hz bins), resampled onto a
// fixed scale and mapped to 0-255 against the noise floor (the band's median), so the
// display shows signals the same way whatever the input level. One row per call; the app
// makes ten a second.

import (
	"math"
	"math/cmplx"
	"sort"
)

// Waterfall turns audio into display rows.
type Waterfall struct {
	sr     float32
	n      int
	win    []float32
	buf    []complex128
	Lo, Hi float32 // scale, Hz
	Cols   int     // row width
	Range  float32 // dB from the noise floor to full brightness (default 36)
	floor  float32 // smoothed noise floor, dB
}

// NewWaterfall: rows of cols bins from lo to hi Hz, for audio at sr.
func NewWaterfall(sr, lo, hi float32, cols int) *Waterfall {
	n := 256
	for float32(n) < sr*0.17 { // ~6 Hz bins
		n *= 2
	}
	w := &Waterfall{sr: sr, n: n, win: make([]float32, n), buf: make([]complex128, n), Lo: lo, Hi: hi, Cols: cols, Range: 36}
	for i := range w.win {
		w.win[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	return w
}

// Need: how many samples a row looks at.
func (w *Waterfall) Need() int { return w.n }

// Row: the newest Need() samples of audio -> Cols brightness values (nil if too little).
func (w *Waterfall) Row(audio []float32) []byte {
	if len(audio) < w.n {
		return nil
	}
	a := audio[len(audio)-w.n:]
	for i := range w.buf {
		w.buf[i] = complex(float64(a[i]*w.win[i]), 0)
	}
	FFT(w.buf)
	binHz := float64(w.sr) / float64(w.n)
	db := make([]float32, w.Cols)
	for c := range db {
		f := float64(w.Lo) + (float64(w.Hi-w.Lo))*float64(c)/float64(w.Cols-1)
		k := f / binHz
		k0 := int(k)
		if k0+1 >= w.n/2 {
			k0 = w.n/2 - 2
		}
		fr := k - float64(k0)
		m := (1-fr)*cmplx.Abs(w.buf[k0]) + fr*cmplx.Abs(w.buf[k0+1])
		db[c] = float32(20 * math.Log10(m+1e-9))
	}
	s := append([]float32(nil), db...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	med := s[len(s)/2]
	if w.floor == 0 {
		w.floor = med
	} else {
		w.floor = 0.9*w.floor + 0.1*med // steady: a burst of CW doesn't move it
	}
	out := make([]byte, w.Cols)
	for c, v := range db {
		x := (v - w.floor + 3) / w.Range // a touch above the floor stays dark
		if x < 0 {
			x = 0
		} else if x > 1 {
			x = 1
		}
		out[c] = byte(x*254 + 0.5)
	}
	return out
}

// FFT: in-place radix-2 transform (len(x) a power of two).
func FFT(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := x[start+k], x[start+k+size/2]*w
				x[start+k], x[start+k+size/2] = u+v, u-v
				w *= step
			}
		}
	}
}
