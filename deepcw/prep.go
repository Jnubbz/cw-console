// Package deepcw runs the DeepCW neural CW decoder (model by e04, AGPL-3.0,
// https://github.com/e04/deepcw-engine) with a self-contained ONNX interpreter.
// Audio preparation, CTC decoding and window stitching are ported from Nexus by KD9TAW
// (crates/deepcw/src/lib.rs, crates/tempo-audio/src/aicw.rs), GPL-3.0.
package deepcw

import (
	"encoding/json"
	"fmt"
	"math"
	"math/cmplx"
	"os"
)

// Metadata is the model's model.onnx.json I/O contract.
type Metadata struct {
	Chars      []string `json:"chars"`
	BlankIndex int      `json:"blank_index"`
	SampleRate int      `json:"sample_rate"`
	FFTLength  int      `json:"fft_length"`
	HopLength  int      `json:"hop_length"`
	MinFreqHz  float32  `json:"spectrogram_min_freq_hz"`
	MaxFreqHz  float32  `json:"spectrogram_max_freq_hz"`
	FreqBins   int      `json:"spectrogram_frequency_bins"`
	Norm       string   `json:"normalization"`
	InputName  string   `json:"onnx_input_name"`
	OutputName string   `json:"onnx_output_name"`
	NumClasses int      `json:"num_classes"`
}

func LoadMetadata(path string) (*Metadata, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.FFTLength != 256 || m.Norm != "log1p" {
		return nil, fmt.Errorf("unsupported model metadata (fft %d, %s)", m.FFTLength, m.Norm)
	}
	return &m, nil
}

// reflectPad pads without repeating the edge sample (numpy mode="reflect").
func reflectPad(a []float32, pad int) []float32 {
	n := len(a)
	out := make([]float32, 0, n+2*pad)
	for i := pad; i >= 1; i-- {
		out = append(out, a[i])
	}
	out = append(out, a...)
	for i := 2; i <= pad+1; i++ {
		out = append(out, a[n-i])
	}
	return out
}

// hannPeriodic: 0.5 − 0.5·cos(2πn/N).
func hannPeriodic(n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		w[i] = 0.5 - 0.5*float32(math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	return w
}

// fft256 is an in-place radix-2 complex FFT (forward, unnormalised).
type fft struct {
	n   int
	rev []int
	tw  []complex128
}

func newFFT(n int) *fft {
	f := &fft{n: n, rev: make([]int, n), tw: make([]complex128, n/2)}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := 0; i < n; i++ {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		f.rev[i] = r
	}
	for i := range f.tw {
		f.tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/float64(n)))
	}
	return f
}

func (f *fft) run(x []complex128) {
	for i, r := range f.rev {
		if i < r {
			x[i], x[r] = x[r], x[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for s := 0; s < f.n; s += size {
			for k := 0; k < half; k++ {
				t := f.tw[k*step] * x[s+k+half]
				x[s+k+half] = x[s+k] - t
				x[s+k] += t
			}
		}
	}
}

// Focus narrows the AI to one signal: spectrogram bins outside Pitch ± Width/2 are
// turned down (raised-cosine edge over Taper Hz) to Floor of their level. Without it the
// model hears the whole 400–1200 Hz band and blends neighbouring stations.
type Focus struct {
	Pitch, Width, Taper, Floor float64
}

// DefaultFocus centres on pitch with the settings tuned on the test recordings.
func DefaultFocus(pitch float64) *Focus {
	return &Focus{Pitch: pitch, Width: FocusWidth, Taper: FocusTaper, Floor: FocusFloor}
}

var (
	FocusWidth = 250.0
	FocusTaper = 50.0
	FocusFloor = 0.05
)

func (f *Focus) gain(hz float64) float32 {
	if f == nil {
		return 1
	}
	d := math.Abs(hz-f.Pitch) - f.Width/2
	switch {
	case d <= 0:
		return 1
	case d >= f.Taper:
		return float32(f.Floor)
	}
	c := 0.5 + 0.5*math.Cos(math.Pi*d/f.Taper)
	return float32(f.Floor + (1-f.Floor)*c)
}

// Spectrogram builds the model input: flattened [time × freqBins] and the frame count.
func Spectrogram(audio []float32, m *Metadata) ([]float32, int, error) {
	return SpectrogramFocus(audio, m, nil)
}

// SpectrogramFocus is Spectrogram with an optional pitch focus (nil = whole band).
func SpectrogramFocus(audio []float32, m *Metadata, focus *Focus) ([]float32, int, error) {
	binHz := float32(m.SampleRate) / float32(m.FFTLength)
	start := int(math.Ceil(float64(m.MinFreqHz / binHz)))
	stop := int(math.Floor(float64(m.MaxFreqHz/binHz))) + 1
	if stop-start != m.FreqBins {
		return nil, 0, fmt.Errorf("metadata bin range mismatch")
	}
	pad := m.FFTLength / 2
	if len(audio) <= pad {
		return nil, 0, fmt.Errorf("audio too short")
	}
	p := reflectPad(audio, pad)
	win := hannPeriodic(m.FFTLength)
	frames := 1 + (len(p)-m.FFTLength)/m.HopLength
	out := make([]float32, 0, frames*m.FreqBins)
	gains := make([]float32, stop-start)
	for i := range gains {
		gains[i] = focus.gain(float64(start+i) * float64(binHz))
	}
	f := newFFT(m.FFTLength)
	buf := make([]complex128, m.FFTLength)
	for fi := 0; fi < frames; fi++ {
		s := fi * m.HopLength
		for i, w := range win {
			buf[i] = complex(float64(p[s+i]*w), 0)
		}
		f.run(buf)
		for i, c := range buf[start:stop] {
			out = append(out, float32(math.Log1p(cmplx.Abs(c)*float64(gains[i]))))
		}
	}
	return out, frames, nil
}

// TimedChar is a decoded character and its time (seconds into the clip).
type TimedChar struct {
	At float64
	Ch string
}

// GreedyCTCTimed is best-path CTC with per-character timing (port of greedy_ctc_timed).
func GreedyCTCTimed(logProbs []float32, time, classes int, clipSecs float64, m *Metadata) []TimedChar {
	var out []TimedChar
	prev := -1
	for t := 0; t < time; t++ {
		row := logProbs[t*classes : (t+1)*classes]
		best := m.BlankIndex
		bv := float32(math.Inf(-1))
		for i, v := range row {
			if v >= bv { // Rust's max_by returns the LAST of equal maxima; match it
				bv, best = v, i
			}
		}
		if best == m.BlankIndex {
			prev = -1
			continue
		}
		if prev != best {
			out = append(out, TimedChar{(float64(t) + 0.5) / float64(time) * clipSecs, m.Chars[best]})
		}
		prev = best
	}
	return out
}

// ResampleLinear matches Nexus's (and the reference example's) linear resampler.
func ResampleLinear(a []float32, from, to int) []float32 {
	if from == to || len(a) == 0 {
		return append([]float32(nil), a...)
	}
	n := int(math.Round(float64(len(a)) * float64(to) / float64(from)))
	out := make([]float32, n)
	for i := range out {
		pos := float64(i) * float64(from) / float64(to)
		l := int(math.Floor(pos))
		r := l + 1
		if r > len(a)-1 {
			r = len(a) - 1
		}
		if l > len(a)-1 {
			l = len(a) - 1
		}
		fr := float32(pos - float64(l))
		out[i] = a[l]*(1-fr) + a[r]*fr
	}
	return out
}
