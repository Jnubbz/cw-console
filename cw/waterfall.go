package cw

// Waterfall rows. The FFT, the column mapping and the noise floor live in
// spectrum so the CW display and the FT8 display share one implementation.
// Row still looks at the newest Need() samples, which is what the tests and
// the historical 10 Hz display did.

import "github.com/Jnubbz/cw-console/spectrum"

// Waterfall turns audio into display rows.
type Waterfall struct {
	sr     float32
	n      int
	win    []float64
	Lo, Hi float32 // scale, Hz
	Cols   int     // row width
	Range  float32 // dB from the noise floor to full brightness (default 36)
	floor  float64
	have   bool
}

// NewWaterfall: rows of cols bins from lo to hi Hz, for audio at sr.
func NewWaterfall(sr, lo, hi float32, cols int) *Waterfall {
	n := 256
	for float32(n) < sr*0.17 { // ~6 Hz bins, as the original display
		n *= 2
	}
	return &Waterfall{sr: sr, n: n, win: spectrum.Hann(n), Lo: lo, Hi: hi, Cols: cols, Range: 36}
}

// Need: how many samples a row looks at.
func (w *Waterfall) Need() int { return w.n }

// Row: the newest Need() samples of audio -> Cols brightness values (nil if too little).
func (w *Waterfall) Row(audio []float32) []byte {
	if len(audio) < w.n {
		return nil
	}
	a := audio[len(audio)-w.n:]
	span := spectrum.Span{LoHz: float64(w.Lo), HiHz: float64(w.Hi), Cols: w.Cols}
	db := spectrum.ColumnsDB(spectrum.Magnitudes(a, w.win), int(w.sr), w.n, span)
	med := spectrum.Median(db)
	w.floor = spectrum.TrackFloor(w.floor, med, 0.1, !w.have)
	w.have = true
	return spectrum.Levels(db, w.floor, 3, float64(w.Range))
}

// FFT: in-place radix-2 transform (len(x) a power of two).
func FFT(x []complex128) { spectrum.FFT(x) }
