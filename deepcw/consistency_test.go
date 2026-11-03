package deepcw

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/cw"
)

// Same 15 s noisy window through every speed path: plain/1 thread is the reference.
func TestFastPathsMatchReference(t *testing.T) {
	m := loadT(t)
	defer SetSIMD(true)
	defer SetThreads(1)
	a := cw.MorseSamples("KN0BLE DE W1AW UR 599 BK", 22, 650, 3200)
	r := rand.New(rand.NewSource(7))
	a = append(make([]float32, 3200), a...)
	a = append(a, make([]float32, 15*3200-len(a))...)
	a = a[:15*3200]
	for i := range a {
		a[i] = 0.3*a[i] + 0.1*float32(r.NormFloat64())
	}
	spec, frames, _ := Spectrogram(a, m.Meta)
	run := func(simd bool, th int) ([]float32, time.Duration) {
		SetSIMD(simd)
		SetThreads(th)
		t0 := time.Now()
		lp, _, _, err := m.Run(spec, frames)
		if err != nil {
			t.Fatal(err)
		}
		return lp, time.Since(t0)
	}
	ref, dref := run(false, 1)
	t.Logf("reference (portable, 1 thread): %v", dref)
	for _, c := range []struct {
		simd bool
		th   int
	}{{false, 2}, {true, 1}, {true, 2}} {
		if c.simd && !HasAVX2() {
			continue
		}
		got, d := run(c.simd, c.th)
		worst := 0.0
		argmaxDiff := 0
		for i := 0; i < len(ref); i += 42 {
			bi, bj := 0, 0
			for k := 0; k < 42; k++ {
				worst = math.Max(worst, math.Abs(float64(ref[i+k]-got[i+k])))
				if ref[i+k] > ref[i+bi] {
					bi = k
				}
				if got[i+k] > got[i+bj] {
					bj = k
				}
			}
			if bi != bj {
				argmaxDiff++
			}
		}
		t.Logf("simd=%v threads=%d: %v, max |diff| in log-probs %.2g, frames with different best class: %d", c.simd, c.th, d, worst, argmaxDiff)
		if worst > 1e-2 || argmaxDiff > 0 {
			t.Errorf("fast path deviates")
		}
	}
}
