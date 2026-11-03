package main

import (
	"math"
	"math/rand"
	"testing"
)

func TestFT8Waterfall(t *testing.T) {
	var w ft8WF
	r := rand.New(rand.NewSource(1))
	x := make([]float32, 12000)
	for i := range x {
		x[i] = float32(0.3*math.Sin(2*math.Pi*1500*float64(i)/12000) + 0.01*r.NormFloat64())
	}
	w.push(x)
	if w.seq < 6 || len(w.rows) != wfKeep {
		t.Fatal(w.seq, len(w.rows))
	}
	row := w.rows[len(w.rows)-1]
	best := 0
	for c := range row {
		if row[c] > row[best] {
			best = c
		}
	}
	if hz := float64(best) * wfMaxHz / wfCols; math.Abs(hz-1500) > 15 || row[best] < 200 || row[60] > 60 {
		t.Fatalf("peak at %.0f Hz (%d), noise %d", hz, row[best], row[60])
	}
}

func BenchmarkFT8WaterfallSecond(b *testing.B) {
	x := make([]float32, 12000) // one second of audio
	for i := range x {
		x[i] = float32(math.Sin(float64(i)))
	}
	var w ft8WF
	for i := 0; i < b.N; i++ {
		w.push(x)
	}
}
