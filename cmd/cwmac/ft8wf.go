package main

// FT8 waterfall: 0-3000 Hz of the receive audio (12 kHz), like WSJT-X's wide graph.
// Rows of 256 bytes (11.7 Hz each), about 8 a second, brightness = dB above the noise.

import (
	"math"
	"math/cmplx"
	"sort"
)

const (
	wfN     = 2048 // FFT size at 12 kHz: 5.86 Hz bins
	wfHop   = 1536 // ~7.8 rows a second
	wfCols  = 256  // 0-3000 Hz, two FFT bins per column
	wfKeep  = 4    // rows sent with each state update (it goes out every 100 ms)
	wfMaxHz = 3000
)

type ft8WF struct {
	acc   []float32
	win   []float64
	tw    []complex128
	rows  [][]byte // newest last
	seq   int64    // rows made so far
	floor float64  // noise floor, dB (smoothed)
}

func (w *ft8WF) push(x []float32) {
	if w.win == nil {
		w.win = make([]float64, wfN)
		for i := range w.win {
			w.win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/wfN)
		}
		w.tw = make([]complex128, wfN/2)
		for i := range w.tw {
			w.tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/wfN))
		}
	}
	w.acc = append(w.acc, x...)
	for len(w.acc) >= wfN {
		w.row(w.acc[:wfN])
		w.acc = append(w.acc[:0], w.acc[wfHop:]...)
	}
}

func (w *ft8WF) reset() { w.acc = w.acc[:0] }

func (w *ft8WF) row(x []float32) {
	buf := make([]complex128, wfN)
	for i, v := range x {
		buf[i] = complex(float64(v)*w.win[i], 0)
	}
	fftRadix2(buf, w.tw)
	db := make([]float64, wfCols)
	for c := 0; c < wfCols; c++ {
		p := 0.0
		for k := 2 * c; k < 2*c+2; k++ {
			p = math.Max(p, real(buf[k])*real(buf[k])+imag(buf[k])*imag(buf[k]))
		}
		db[c] = 10 * math.Log10(p+1e-20)
	}
	s := append([]float64(nil), db[17:]...) // above 200 Hz (the rig's filter rolls off below)
	sort.Float64s(s)
	med := s[len(s)/2]
	if w.seq == 0 {
		w.floor = med
	} else {
		w.floor = 0.9*w.floor + 0.1*med
	}
	r := make([]byte, wfCols)
	for c, v := range db {
		// -3 dB below the floor is black, 27 dB above is full scale
		r[c] = byte(math.Max(0, math.Min(255, (v-w.floor+3)/30*255)))
	}
	w.rows = append(w.rows, r)
	if len(w.rows) > wfKeep {
		w.rows = w.rows[len(w.rows)-wfKeep:]
	}
	w.seq++
}

// fftRadix2: in-place iterative FFT; len(x) a power of 2, tw[k] = exp(-2πik/n).
func fftRadix2(x []complex128, tw []complex128) {
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
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < size/2; k++ {
				t := tw[k*step] * x[start+k+size/2]
				x[start+k+size/2] = x[start+k] - t
				x[start+k] += t
			}
		}
	}
}
