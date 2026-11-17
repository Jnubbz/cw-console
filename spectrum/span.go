// Package spectrum is the shared waterfall and spectrum display.
//
// Three displays exist today, and they do not share code:
//
//   - cw.Waterfall (cw/waterfall.go): Hann FFT, bins grown until about 6 Hz,
//     noise floor from the median, one row of bytes. The CW page draws it.
//   - ft8WF (cmd/cwmac/ft8wf.go): 2048-point FFT at 12 kHz, 256 columns over
//     0-3000 Hz, its own floor tracker.
//   - SSTV: an AnalyserNode in the browser (sstv-console cmd/sstv-console/web/app.js).
//     The Go process never sees those bins.
//
// One Analyzer runs a Hann FFT on the capture stream. Each mode draws a Span
// of that frame. ClickHz maps a click the way the canvases always have:
// fraction 0 is LoHz and fraction 1 is HiHz.
package spectrum

// Span is the slice of audio spectrum a mode shows.
// Cols is the waterfall width. HzAt treats column 0 as LoHz and the last
// column as HiHz, matching cw.Waterfall.
type Span struct {
	Name string
	LoHz float64
	HiHz float64
	Cols int
}

// Presets match the ranges the current displays already use, so a later
// switch can keep the same canvas width for FT8 (256) and a CW window around
// the sidetone.
var (
	SpanCW  = Span{Name: "cw", LoHz: 300, HiHz: 1200, Cols: 200}
	SpanFT8 = Span{Name: "ft8", LoHz: 0, HiHz: 3000, Cols: 256}
	// SSTV tones: 1200 Hz sync, 1500 Hz black, 2300 Hz white.
	SpanSSTV = Span{Name: "sstv", LoHz: 1100, HiHz: 2400, Cols: 256}
)

// HzAt returns the frequency at the center of column col.
func HzAt(s Span, col int) float64 {
	if s.Cols <= 1 || s.HiHz == s.LoHz {
		return s.LoHz
	}
	if col < 0 {
		col = 0
	}
	if col >= s.Cols {
		col = s.Cols - 1
	}
	return s.LoHz + (s.HiHz-s.LoHz)*float64(col)/float64(s.Cols-1)
}

// ColAt returns the column whose center is nearest hz.
func ColAt(s Span, hz float64) int {
	if s.Cols <= 1 || s.HiHz <= s.LoHz {
		return 0
	}
	if hz <= s.LoHz {
		return 0
	}
	if hz >= s.HiHz {
		return s.Cols - 1
	}
	c := int((hz-s.LoHz)/((s.HiHz-s.LoHz)/float64(s.Cols-1)) + 0.5)
	if c < 0 {
		return 0
	}
	if c >= s.Cols {
		return s.Cols - 1
	}
	return c
}
