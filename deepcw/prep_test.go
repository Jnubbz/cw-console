package deepcw

// Ports of Nexus crates/deepcw/src/lib.rs tests.

import (
	"math"
	"strings"
	"testing"
)

func testMeta() *Metadata {
	return &Metadata{Chars: []string{"A", "B", "C", " "}, BlankIndex: 3, SampleRate: 3200,
		FFTLength: 256, HopLength: 48, MinFreqHz: 400, MaxFreqHz: 1200, FreqBins: 65, Norm: "log1p"}
}

func TestReflectPadMatchesNumpy(t *testing.T) {
	got := reflectPad([]float32{1, 2, 3, 4}, 2)
	want := []float32{3, 2, 1, 2, 3, 4, 3, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}

func TestHannPeriodic(t *testing.T) {
	w := hannPeriodic(256)
	if len(w) != 256 || math.Abs(float64(w[0])) > 1e-6 || math.Abs(float64(w[128]-1)) > 1e-6 {
		t.Fatal("bad hann")
	}
}

func TestSpectrogramShapeAndToneBin(t *testing.T) {
	m := testMeta()
	a := make([]float32, 3200)
	for i := range a {
		a[i] = float32(math.Sin(2 * math.Pi * 600 * float64(i) / 3200))
	}
	spec, frames, err := Spectrogram(a, m)
	if err != nil || frames != 1+3200/48 || len(spec) != frames*65 {
		t.Fatalf("frames %d len %d err %v", frames, len(spec), err)
	}
	mid := spec[(frames/2)*65 : (frames/2+1)*65]
	hot := 0
	for i := range mid {
		if mid[i] > mid[hot] {
			hot = i
		}
	}
	if hot != 16 {
		t.Fatalf("hottest %d", hot)
	}
}

func TestFFTAgainstDFT(t *testing.T) {
	x := make([]complex128, 256)
	for i := range x {
		x[i] = complex(math.Sin(float64(i)*0.37)+0.2*math.Cos(float64(i)*1.9), 0)
	}
	want := make([]complex128, 256)
	for k := range want {
		for n := range x {
			want[k] += x[n] * complex(math.Cos(-2*math.Pi*float64(k*n)/256), math.Sin(-2*math.Pi*float64(k*n)/256))
		}
	}
	newFFT(256).run(x)
	for k := range x {
		if d := x[k] - want[k]; math.Hypot(real(d), imag(d)) > 1e-9 {
			t.Fatalf("bin %d", k)
		}
	}
}

func TestGreedyCTCCollapses(t *testing.T) {
	m := testMeta()
	seq := []int{0, 0, 3, 0, 1, 1, 3, 3, 2}
	var flat []float32
	for _, idx := range seq {
		row := []float32{-10, -10, -10, -10}
		row[idx] = 0
		flat = append(flat, row...)
	}
	var sb strings.Builder
	for _, c := range GreedyCTCTimed(flat, len(seq), 4, 1, m) {
		sb.WriteString(c.Ch)
	}
	if sb.String() != "AABC" {
		t.Fatalf("got %q", sb.String())
	}
}

func TestResampleHalves(t *testing.T) {
	a := make([]float32, 1200)
	for i := range a {
		a[i] = float32(i)
	}
	o := ResampleLinear(a, 12000, 3200)
	if len(o) != 320 || math.Abs(float64(o[100])-100*12000.0/3200) > 1 {
		t.Fatalf("len %d o[100] %v", len(o), o[100])
	}
}
