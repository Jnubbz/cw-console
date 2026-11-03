package deepcw

import (
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"cwdecoder/cw"
)

func editDist(a, b string) int {
	d := make([]int, len(b)+1)
	for j := range d {
		d[j] = j
	}
	for i := 1; i <= len(a); i++ {
		p := d[0]
		d[0] = i
		for j := 1; j <= len(b); j++ {
			t := d[j]
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			d[j] = min(d[j]+1, d[j-1]+1, p+c)
			p = t
		}
	}
	return d[len(b)]
}

// CW_RANDOM_AI=N go test -run RandomAI -v -timeout 2h ./deepcw
// Random recordings (not used for tuning): classic-improved vs AI, both at the true pitch.
func TestRandomAIvsClassic(t *testing.T) {
	n := 0
	if v := os.Getenv("CW_RANDOM_AI"); v != "" {
		for _, c := range v {
			n = n*10 + int(c-'0')
		}
	}
	if n == 0 {
		t.Skip("set CW_RANDOM_AI=N")
	}
	m := loadT(t)
	SetThreads(2)
	r := rand.New(rand.NewSource(20261012))
	words := strings.Fields("CQ DE TEST UR RST 599 5NN NAME QTH TNX FB OM ES 73 GL K BK R TU WX HR RIG ANT PWR 100W DIPOLE KN0BLE W1AW K4ABC VE3XYZ G4DEF JA1ZZ")
	const sr = 12000
	var sumC, sumA float64
	better, worse := 0, 0
	for c := 0; c < n; c++ {
		var ws []string
		for k := 0; k < 6+r.Intn(8); k++ {
			ws = append(ws, words[r.Intn(len(words))])
		}
		truth := strings.Join(ws, " ")
		wpm := uint32(12 + r.Float64()*28)
		pitch := float32(450 + r.Float64()*500)
		sig := cw.MorseSamples(truth, wpm, pitch, sr)
		amp := float32(0.2 + r.Float64()*0.5)
		fade := 0.0
		if r.Float64() < 0.4 {
			fade = 0.3 + r.Float64()*0.6
		}
		fadeHz := 0.1 + r.Float64()*0.4
		noise := 0.005 + r.Float64()*0.1
		qrm := r.Float64() < 0.25 // a second station 150-400 Hz away
		var q []float32
		if qrm {
			off := float32(150 + r.Float64()*250)
			if pitch+off > 1150 {
				off = -off
			}
			q = cw.MorseSamples("TEST TEST DE K1ZZ K1ZZ TEST DE K1ZZ TEST", uint32(15+r.Intn(15)), pitch+off, sr)
		}
		a := make([]float32, sr/2)
		for i, s := range sig {
			f := 1 - fade*0.5*(1+math.Sin(2*math.Pi*fadeHz*float64(i)/sr))
			a = append(a, s*amp*float32(f))
		}
		a = append(a, make([]float32, sr)...)
		for i := range a {
			if qrm && i < len(q) {
				a[i] += 0.35 * q[i]
			}
			a[i] += float32(noise * r.NormFloat64())
		}
		score := func(got string) float64 {
			return math.Max(0, 1-float64(editDist(strings.TrimSpace(got), truth))/float64(len(truth)))
		}
		d := cw.NewStreamDecoder(sr, pitch)
		d.SetImproved(true)
		var cl strings.Builder
		for i := 0; i < len(a); i += sr {
			d.Push(a[i:min(i+sr, len(a))])
			cl.WriteString(d.TakeText())
		}
		d.Push(make([]float32, sr*3/2))
		cl.WriteString(d.TakeText())
		ai, err := DecodeStream(m, a, sr, 2, DefaultFocus(float64(pitch)))
		if err != nil {
			t.Fatal(err)
		}
		sc, sa := score(cl.String()), score(ai)
		sumC += sc
		sumA += sa
		if sa > sc+0.05 {
			better++
		}
		if sa < sc-0.05 {
			worse++
			t.Logf("AI worse: wpm=%d noise=%.3f fade=%.2f qrm=%v classic=%.0f%% ai=%.0f%%  ai=%q", wpm, noise, fade, qrm, sc*100, sa*100, ai)
		}
	}
	t.Logf("%d random recordings: classic (improved) avg %.1f%%, AI avg %.1f%%; AI better on %d, worse on %d",
		n, 100*sumC/float64(n), 100*sumA/float64(n), better, worse)
}
