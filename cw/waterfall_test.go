package cw

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	x := make([]complex128, 64)
	for i := range x {
		x[i] = complex(math.Sin(float64(i)*0.41)+0.3*math.Cos(float64(i)*2.1), 0)
	}
	want := make([]complex128, len(x))
	for k := range want {
		for n := range x {
			want[k] += x[n] * cmplx.Exp(complex(0, -2*math.Pi*float64(k*n)/float64(len(x))))
		}
	}
	FFT(x)
	for k := range x {
		if cmplx.Abs(x[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v vs %v", k, x[k], want[k])
		}
	}
}

func TestWaterfallShowsTheToneWhereItIs(t *testing.T) {
	for _, sr := range []float32{12000, 48000} {
		w := NewWaterfall(sr, 200, 1400, 241) // 5 Hz columns
		noise := whiteNoise(int(sr), 0.05)
		tone := make([]float32, len(noise)) // key down at 703 Hz
		for i := range tone {
			tone[i] = 0.3 * float32(math.Sin(2*math.Pi*703*float64(i)/float64(sr)))
		}
		sig := mix(noise, tone)
		var row []byte
		for i := 0; i < 5; i++ { // a few rows, as the app makes them
			row = w.Row(sig)
		}
		if row == nil {
			t.Fatalf("%v: no row", sr)
		}
		best := 0
		for c := range row {
			if row[c] > row[best] {
				best = c
			}
		}
		f := 200 + float64(best)*5
		if math.Abs(f-703) > 10 || row[best] < 200 {
			t.Fatalf("%v: peak at %.0f Hz (%d)", sr, f, row[best])
		}
		dark := 0
		for c := range row {
			if row[c] < 60 {
				dark++
			}
		}
		if dark < len(row)*3/4 {
			t.Fatalf("%v: noise isn't dark (%d of %d)", sr, dark, len(row))
		}
	}
}
