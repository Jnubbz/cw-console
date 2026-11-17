package spectrum

import (
	"math"
	"testing"
)

func TestBinCenters(t *testing.T) {
	// 4096 points at 12 kHz: bin 512 is exactly 1500 Hz.
	if hz := BinHz(12000, 4096, 512); hz != 1500 {
		t.Fatalf("bin 512 is %v Hz", hz)
	}
	if k := BinAt(12000, 4096, 1500); k != 512 {
		t.Fatalf("1500 Hz is bin %v", k)
	}
	if BinHz(16000, 4096, 0) != 0 {
		t.Fatal("dc")
	}
}

func TestColumnsPutTheToneOnItsFrequency(t *testing.T) {
	const rate, n = 12000, 4096
	mag := make([]float64, n/2)
	k := int(BinAt(rate, n, 1500))
	mag[k] = 1
	db := ColumnsDB(mag, rate, n, SpanFT8)
	best := 0
	for i := range db {
		if db[i] > db[best] {
			best = i
		}
	}
	left := SpanFT8.LoHz + float64(best)*(SpanFT8.HiHz-SpanFT8.LoHz)/float64(SpanFT8.Cols)
	right := left + (SpanFT8.HiHz-SpanFT8.LoHz)/float64(SpanFT8.Cols)
	if 1500 < left || 1500 >= right {
		t.Fatalf("1500 Hz landed in column %d (%.1f–%.1f)", best, left, right)
	}
	// A column wider than a bin keeps the louder of the bins inside it.
	wide := Span{LoHz: 0, HiHz: 3000, Cols: 16}
	mag[k+2] = 2
	db = ColumnsDB(mag, rate, n, wide)
	best = 0
	for i := range db {
		if db[i] > db[best] {
			best = i
		}
	}
	if best != ColAt(wide, BinHz(rate, n, k+2)) && best != ColAt(wide, 1500) {
		t.Fatalf("wide peak at column %d", best)
	}
}

func TestNoiseFloorDoesNotJumpForABurst(t *testing.T) {
	floor := TrackFloor(0, -40, 0.1, true)
	if floor != -40 {
		t.Fatalf("first sample %v", floor)
	}
	next := TrackFloor(floor, 0, 0.1, false)
	if math.Abs(next-(-36)) > 1e-9 {
		t.Fatalf("one burst moved the floor to %v", next)
	}
	// Ten quiet frames pull it back down, not in one step.
	for i := 0; i < 10; i++ {
		next = TrackFloor(next, -40, 0.1, false)
	}
	if next >= -36 || next < -40 {
		t.Fatalf("floor after the burst %v", next)
	}
	span := SpanFT8
	db := make([]float64, span.Cols)
	for i := range db {
		db[i] = -50
	}
	// Below 200 Hz is the rig's rolloff. It must not set the floor.
	for c := 0; c < 10; c++ {
		db[c] = 20
	}
	if med := NoiseMedian(db, span, 200); med != -50 {
		t.Fatalf("median %v", med)
	}
}

func TestClickMatchesTheCanvases(t *testing.T) {
	if CWClickPitch(0) != 300 || CWClickPitch(1) != 1200 || CWClickPitch(0.5) != 800 {
		t.Fatalf("cw %v %v %v", CWClickPitch(0), CWClickPitch(0.5), CWClickPitch(1))
	}
	// A quarter of the old 200–1400 canvas is 500 Hz, which the pitch slider accepts.
	if CWClickPitch(0.25) != 500 {
		t.Fatalf("quarter %v", CWClickPitch(0.25))
	}
	if FT8ClickHz(0) != 200 || FT8ClickHz(1) != 2900 || FT8ClickHz(0.5) != 1500 {
		t.Fatalf("ft8 %v %v %v", FT8ClickHz(0), FT8ClickHz(0.5), FT8ClickHz(1))
	}
	if FT8ClickHz(1000.0/3000) != 1000 {
		t.Fatalf("1000 Hz click %v", FT8ClickHz(1000.0/3000))
	}
	// Zooming does not change the rule: the fraction is still the visible span.
	z := Zoom(SpanFT8, 0.5, 1500)
	if math.Abs(ClickHz(z.LoHz, z.HiHz, 0.5)-1500) > 1e-6 {
		t.Fatalf("zoom center %v (%v..%v)", ClickHz(z.LoHz, z.HiHz, 0.5), z.LoHz, z.HiHz)
	}
	if z.HiHz-z.LoHz != 1500 {
		t.Fatalf("zoom width %v", z.HiHz-z.LoHz)
	}
}

func TestResolvePresets(t *testing.T) {
	cw := Resolve("cw", "", 0, 0, 0, 0, 0, 0, 0)
	if cw.Span.LoHz != 200 || cw.Span.HiHz != 1400 || cw.Span.Cols != 241 || cw.FFT != 4096 || cw.FPS != 10 {
		t.Fatalf("cw default %+v", cw)
	}
	ft8 := Resolve("ft8", "", 0, 0, 0, 0, 0, 0, 0)
	if ft8.Span != SpanFT8 || ft8.FloorFrom != 200 || ft8.FPS != 8 {
		t.Fatalf("ft8 default %+v", ft8)
	}
	sstv := Resolve("cw", "sstv", 0, 0, 0, 0, 0, 0, 0)
	if sstv.Span != SpanSSTV {
		t.Fatalf("sstv %+v", sstv)
	}
	sidetone := Resolve("cw", "cw", 0, 0, 0, 0, 0, 0, 0)
	if sidetone.Span != SpanCW {
		t.Fatalf("sidetone %+v", sidetone)
	}
	custom := Resolve("cw", "custom", 400, 800, 100, 0, 24, 1024, 5)
	if custom.Span.LoHz != 400 || custom.Span.HiHz != 800 || custom.Span.Cols != 100 || custom.FFT != 1024 || custom.FPS != 5 || custom.Range != 24 {
		t.Fatalf("custom %+v", custom)
	}
}

func TestAnalyzerFindsTheTone(t *testing.T) {
	const rate = 12000
	a := New(Config{Rate: rate, FFTSize: 4096, FrameRate: 8})
	x := make([]float32, rate)
	for i := range x {
		x[i] = float32(0.3*math.Sin(2*math.Pi*1500*float64(i)/rate) + 0.01*math.Sin(float64(i)*0.2))
	}
	frames := a.Push(x)
	if len(frames) < 2 {
		t.Fatalf("frames %d", len(frames))
	}
	view := Resolve("ft8", "", 0, 0, 0, 0, 0, 0, 0)
	db := ColumnsDB(frames[len(frames)-1], rate, a.Size(), view.Span)
	floor := TrackFloor(0, NoiseMedian(db, view.Span, view.FloorFrom), 0.1, true)
	row := Levels(db, floor, view.Lift, view.Range)
	best := 0
	for i := range db {
		if db[i] > db[best] {
			best = i
		}
	}
	hz := view.Span.LoHz + float64(best)*(view.Span.HiHz-view.Span.LoHz)/float64(view.Span.Cols)
	if math.Abs(hz-1500) > 15 || row[best] < 200 || db[best] < db[60]+30 {
		t.Fatalf("peak %.0f Hz brightness %d, %.1f dB above column 60", hz, row[best], db[best]-db[60])
	}
}

func BenchmarkAnalyzeSecond(b *testing.B) {
	for _, rate := range []int{12000, 16000, 48000} {
		b.Run(itoa(rate), func(b *testing.B) {
			a := New(Config{Rate: rate, FFTSize: 4096, FrameRate: 10})
			x := make([]float32, rate)
			for i := range x {
				x[i] = float32(math.Sin(float64(i) * 0.2))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.seen, a.next, a.acc = 0, a.n, a.acc[:0]
				_ = a.Push(x)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [8]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
