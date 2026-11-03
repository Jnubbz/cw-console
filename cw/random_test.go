package cw

import (
	"math"
	"os"
	"strings"
	"testing"
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

// Randomized comparison (not tuned against): CW_RANDOM=1 go test -run Random -v ./cw
func TestRandomizedNexusVsImproved(t *testing.T) {
	if os.Getenv("CW_RANDOM") == "" {
		t.Skip("set CW_RANDOM=1")
	}
	r := &xorshift{0xC0FFEE1234567891}
	rnd := func() float64 { return float64(r.next()>>11) / (1 << 53) }
	words := strings.Fields("CQ DE TEST UR RST 599 5NN NAME QTH TNX FB OM ES 73 GL K BK R TU WX HR RIG ANT PWR 100W DIPOLE KN0BLE W1AW K4ABC VE3XYZ G4DEF JA1ZZ")
	const sr = 12000
	var sumN, sumI float64
	worse, better := 0, 0
	for c := 0; c < 120; c++ {
		var ws []string
		for k := 0; k < 6+int(rnd()*8); k++ {
			ws = append(ws, words[int(rnd()*float64(len(words)))])
		}
		truth := strings.Join(ws, " ")
		wpm := uint32(12 + rnd()*28)
		pitch := float32(450 + rnd()*500)
		sig := MorseSamples(truth, wpm, pitch, sr)
		amp := float32(0.2 + rnd()*0.5)
		fadeDepth := 0.0
		if rnd() < 0.4 {
			fadeDepth = 0.3 + rnd()*0.6
		}
		fadeHz := 0.1 + rnd()*0.4
		noise := 0.005 + rnd()*0.1
		a := make([]float32, sr/2)
		for i, s := range sig {
			f := 1 - fadeDepth*0.5*(1+math.Sin(2*math.Pi*fadeHz*float64(i)/sr))
			a = append(a, s*amp*float32(f))
		}
		a = append(a, make([]float32, sr)...)
		for i := range a {
			u1, u2 := rnd()+1e-12, rnd()
			a[i] += float32(noise * math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2))
		}
		acc := func(imp bool) float64 {
			d := NewStreamDecoder(sr, pitch)
			d.SetImproved(imp)
			for i := 0; i < len(a); i += sr {
				d.Push(a[i:min(i+sr, len(a))])
			}
			d.Push(make([]float32, sr*3/2))
			got := strings.TrimSpace(d.Transcript())
			return math.Max(0, 1-float64(editDist(got, truth))/float64(len(truth)))
		}
		n, im := acc(false), acc(true)
		sumN += n
		sumI += im
		if im < n-0.05 {
			worse++
			t.Logf("worse: wpm=%d pitch=%.0f amp=%.2f noise=%.3f fade=%.2f  nexus=%.0f%% improved=%.0f%%", wpm, pitch, amp, noise, fadeDepth, n*100, im*100)
		}
		if im > n+0.05 {
			better++
		}
	}
	t.Logf("120 random recordings: Nexus avg %.1f%%, improved avg %.1f%%; improved better on %d, worse on %d",
		sumN/1.2, sumI/1.2, better, worse)
}
