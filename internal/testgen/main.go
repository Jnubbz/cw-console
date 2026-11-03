// testgen writes a set of CW test recordings (clean, noisy, fading, hand-sent, QRM)
// plus a manifest of "file pitch truth" lines, for checking the decoder end to end.
//
//	go run ./internal/testgen OUTDIR
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jnubbz/cw-console/cw"
)

type rng struct{ s uint64 }

func (r *rng) f() float64 { // uniform [0,1)
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return float64(r.s>>11) / (1 << 53)
}
func (r *rng) gauss() float64 { // Box-Muller
	u1, u2 := r.f()+1e-12, r.f()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// handSent keys text with per-element timing jitter (like a straight key / bug).
func handSent(text string, wpm, pitch float64, sr int, jitter float64, r *rng) []float32 {
	dit := 1.2 / wpm
	var out []float32
	phase := 0.0
	tone := func(sec float64) {
		n := int(sec * float64(sr))
		ramp := int(0.005 * float64(sr))
		for i := 0; i < n; i++ {
			env := 1.0
			if i < ramp {
				env = 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(ramp))
			} else if n-1-i < ramp {
				env = 0.5 - 0.5*math.Cos(math.Pi*float64(n-1-i)/float64(ramp))
			}
			out = append(out, float32(env*math.Sin(phase)))
			phase += 2 * math.Pi * pitch / float64(sr)
		}
	}
	gap := func(sec float64) {
		for i := 0; i < int(sec*float64(sr)); i++ {
			out = append(out, 0)
		}
	}
	j := func() float64 { return 1 + jitter*(2*r.f()-1) }
	for wi, w := range strings.Fields(text) {
		if wi > 0 {
			gap(7 * dit * j() * 1.2) // hand senders stretch word gaps
		}
		for ci, ch := range w {
			code, _ := cw.MorseCode(ch)
			if ci > 0 {
				gap(3 * dit * j())
			}
			for ei, el := range code {
				if ei > 0 {
					gap(dit * j())
				}
				if el == '-' {
					tone(3 * dit * j())
				} else {
					tone(dit * j())
				}
			}
		}
	}
	return out
}

func pad(s []float32, sr int, lead, trail float64) []float32 {
	out := make([]float32, int(lead*float64(sr)))
	out = append(out, s...)
	return append(out, make([]float32, int(trail*float64(sr)))...)
}

func scale(s []float32, a float32) []float32 {
	for i := range s {
		s[i] *= a
	}
	return s
}

func addNoise(s []float32, amp float64, r *rng) []float32 {
	for i := range s {
		s[i] += float32(amp * r.gauss())
	}
	return s
}

func clip(s []float32) []float32 {
	for i := range s {
		if s[i] > 1 {
			s[i] = 1
		} else if s[i] < -1 {
			s[i] = -1
		}
	}
	return s
}

func main() {
	dir := os.Args[1]
	os.MkdirAll(dir, 0o755)
	var manifest strings.Builder
	write := func(name string, pitch int, truth string, s []float32, sr int) {
		f, _ := os.Create(filepath.Join(dir, name))
		cw.WriteWAV16(f, clip(s), uint32(sr))
		f.Close()
		fmt.Fprintf(&manifest, "%s|%d|%s\n", name, pitch, truth)
	}
	r := &rng{0x1234567887654321}
	call := "CQ CQ DE KN0BLE KN0BLE K"
	qso := "KN0BLE DE W1AW GM UR RST 599 599 NAME JOSH QTH ORLANDO FL BK"

	write("01-clean-20wpm.wav", 600, call, pad(scale(cw.MorseSamples(call, 20, 600, 12000), 0.5), 12000, 0.5, 1), 12000)
	write("02-48k-28wpm-700hz.wav", 700, qso, pad(scale(cw.MorseSamples(qso, 28, 700, 48000), 0.5), 48000, 0.5, 1), 48000)
	write("03-slow-13wpm-550hz.wav", 550, call, pad(scale(cw.MorseSamples(call, 13, 550, 12000), 0.5), 12000, 0.5, 1), 12000)
	write("04-fast-35wpm.wav", 650, qso, pad(scale(cw.MorseSamples(qso, 35, 650, 12000), 0.5), 12000, 0.5, 1), 12000)

	s := pad(scale(cw.MorseSamples(qso, 22, 600, 12000), 0.5), 12000, 0.5, 1)
	write("05-noise-moderate.wav", 600, qso, addNoise(s, 0.08, r), 12000)

	s = pad(scale(cw.MorseSamples(qso, 20, 600, 12000), 0.3), 12000, 0.5, 1)
	write("06-noise-heavy.wav", 600, qso, addNoise(s, 0.12, r), 12000)

	s = pad(scale(handSent(qso, 18, 620, 12000, 0.2, r), 0.5), 12000, 0.5, 1)
	write("07-hand-sent-18wpm.wav", 620, qso, addNoise(s, 0.03, r), 12000)

	// QRM: a second, stronger-ish station 250 Hz away sending something else.
	want := pad(scale(cw.MorseSamples(qso, 22, 600, 12000), 0.4), 12000, 0.5, 1)
	qrm := pad(scale(cw.MorseSamples("TEST TEST DE K1ZZ K1ZZ TEST TEST DE K1ZZ", 26, 850, 12000), 0.5), 12000, 0.2, 0)
	for i := range want {
		if i < len(qrm) {
			want[i] += qrm[i]
		}
	}
	write("08-qrm-250hz-away.wav", 600, qso, addNoise(want, 0.03, r), 12000)

	// QSB: slow fading between ~15% and 100%.
	s = pad(scale(cw.MorseSamples(qso, 22, 600, 12000), 0.5), 12000, 0.5, 1)
	for i := range s {
		s[i] *= float32(0.575 + 0.425*math.Sin(2*math.Pi*0.25*float64(i)/12000))
	}
	write("09-qsb-fading.wav", 600, qso, addNoise(s, 0.02, r), 12000)

	// Speed change mid-transmission (decoder must re-track).
	a := cw.MorseSamples("CQ CQ DE KN0BLE", 15, 600, 12000)
	b := cw.MorseSamples("KN0BLE DE W1AW 599 BK", 30, 600, 12000)
	s = append(a, make([]float32, 12000)...)
	s = pad(scale(append(s, b...), 0.5), 12000, 0.5, 1)
	write("10-speed-change-15-to-30.wav", 600, "CQ CQ DE KN0BLE KN0BLE DE W1AW 599 BK", addNoise(s, 0.02, r), 12000)

	// Extra stress cases for the speed/fade fixes.
	write("11-fast-40wpm.wav", 700, qso, addNoise(pad(scale(cw.MorseSamples(qso, 40, 700, 12000), 0.5), 12000, 0.5, 1), 0.02, r), 12000)
	write("12-fast-30wpm-noisy.wav", 600, qso, addNoise(pad(scale(cw.MorseSamples(qso, 30, 600, 12000), 0.5), 12000, 0.5, 1), 0.08, r), 12000)
	a = cw.MorseSamples("KN0BLE DE W1AW 599 BK", 32, 600, 12000)
	b = cw.MorseSamples("CQ CQ DE KN0BLE", 14, 600, 12000)
	s = append(a, make([]float32, 12000)...)
	s = pad(scale(append(s, b...), 0.5), 12000, 0.5, 1)
	write("13-speed-change-32-to-14.wav", 600, "KN0BLE DE W1AW 599 BK CQ CQ DE KN0BLE", addNoise(s, 0.02, r), 12000)
	s = pad(scale(cw.MorseSamples(qso, 20, 600, 12000), 0.5), 12000, 0.5, 1)
	for i := range s { // deeper, faster fade: 5%..100%, 2.5 s period
		s[i] *= float32(0.525 + 0.475*math.Sin(2*math.Pi*0.4*float64(i)/12000))
	}
	write("14-qsb-deep.wav", 600, qso, addNoise(s, 0.01, r), 12000)
	s = pad(scale(handSent(qso, 24, 600, 12000, 0.25, r), 0.4), 12000, 0.5, 1)
	write("15-hand-sent-24wpm-noisy.wav", 600, qso, addNoise(s, 0.06, r), 12000)

	os.WriteFile(filepath.Join(dir, "manifest.txt"), []byte(manifest.String()), 0o644)
}
