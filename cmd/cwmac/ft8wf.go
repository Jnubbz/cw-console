package main

// FT8 waterfall. The samples are the same 12 kHz stream the decoder reads.
// One spectrum.Analyzer turns them into rows; nothing else FFTs this buffer.

import "github.com/Jnubbz/cw-console/spectrum"

const (
	wfCols  = 256 // 0-3000 Hz, the wide-graph width the canvas is drawn at
	wfKeep  = 4   // rows sent with each state update (it goes out every 100 ms)
	wfMaxHz = 3000
)

type ft8WF struct {
	an    *spectrum.Analyzer
	view  spectrum.View
	rows  [][]byte
	trace []float32
	seq   int64
	floor float64
	have  bool
	fft   int
	fps   float64
}

func (w *ft8WF) setView(v spectrum.View) {
	if v.FFT == 0 {
		v = spectrum.Resolve("ft8", "", 0, 0, 0, 0, 0, 0, 0)
	}
	w.view = v
}

func (w *ft8WF) ensure() {
	if w.view.FFT == 0 {
		w.setView(spectrum.View{})
	}
	if w.an != nil && w.fft == w.view.FFT && w.fps == w.view.FPS {
		return
	}
	w.an = spectrum.New(spectrum.Config{Rate: 12000, FFTSize: w.view.FFT, FrameRate: w.view.FPS})
	w.fft, w.fps = w.view.FFT, w.view.FPS
	w.floor, w.have = 0, false
}

func (w *ft8WF) push(x []float32) {
	w.ensure()
	for _, mag := range w.an.Push(x) {
		db := spectrum.ColumnsDB(mag, w.an.Rate(), w.an.Size(), w.view.Span)
		med := spectrum.NoiseMedian(db, w.view.Span, w.view.FloorFrom)
		w.floor = spectrum.TrackFloor(w.floor, med, 0.1, !w.have)
		w.have = true
		w.rows = append(w.rows, spectrum.Levels(db, w.floor, w.view.Lift, w.view.Range))
		w.trace = spectrum.Trace(db, w.floor, w.view.Lift, w.view.Range)
		if len(w.rows) > wfKeep {
			w.rows = w.rows[len(w.rows)-wfKeep:]
		}
		w.seq++
	}
}

func (w *ft8WF) reset() {
	w.an = nil
	w.rows = nil
	w.trace = nil
	w.floor, w.have = 0, false
}
