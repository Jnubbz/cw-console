package main

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
	"time"
)

func TestResampler34(t *testing.T) {
	var r resampler34
	var out []float32
	in := make([]float32, 16000)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 16000))
	}
	for i := 0; i < len(in); i += 777 { // odd chunk sizes: must be seamless
		out = r.process(in[i:min(i+777, len(in))], out)
	}
	if len(out) < 11950 || len(out) > 12050 {
		t.Fatalf("got %d samples for 1 s", len(out))
	}
	// after the filter settles: a clean 1 kHz sine at 12 kHz, amplitude ~1
	var errSum, amp float64
	ph := math.NaN()
	for i := 1000; i < 11000; i++ {
		v := float64(out[i])
		amp = math.Max(amp, math.Abs(v))
		if math.IsNaN(ph) {
			ph = math.Asin(math.Max(-1, math.Min(1, v)))
			if out[i+1] < out[i] {
				ph = math.Pi - ph
			}
			ph -= 2 * math.Pi * 1000 * float64(i) / 12000
		}
		errSum += math.Abs(v - math.Sin(2*math.Pi*1000*float64(i)/12000+ph))
	}
	if amp < 0.97 || amp > 1.03 || errSum/10000 > 0.02 {
		t.Fatalf("amp %.3f, mean error %.4f", amp, errSum/10000)
	}
	// 7 kHz (above the new Nyquist) must be suppressed
	r = resampler34{}
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 7000 * float64(i) / 16000))
	}
	out = r.process(in, nil)
	var rms float64
	for _, v := range out[1000:] {
		rms += float64(v * v)
	}
	if rms = math.Sqrt(rms / float64(len(out)-1000)); rms > 0.01 {
		t.Fatalf("7 kHz leaks through: rms %.4f", rms)
	}
}

func TestParseJT9(t *testing.T) {
	out := "133430  15  0.3 2571 ~  W1FC F5BZB -08                          \n" +
		"133430 -16  0.1 1649 ~  K1JT EA3AGB -15                         \n" +
		"<DecodeFinished>   0  14        0\n"
	d := parseJT9(out, "133430")
	if len(d) != 2 || d[0].SNR != 15 || d[0].DT != 0.3 || d[0].Freq != 2571 || d[0].Msg != "W1FC F5BZB -08" || d[1].SNR != -16 {
		t.Fatalf("%+v", d)
	}
}

func readWAV12k(t *testing.T, name string) []float32 {
	b, err := os.ReadFile(name)
	if err != nil {
		t.Skip("sample not available:", err)
	}
	b = b[44:]
	x := make([]float32, len(b)/2)
	for i := range x {
		x[i] = float32(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
	}
	return x
}

// up12to16: sinc interpolation, test only.
func up12to16(x []float32) []float32 {
	n := len(x) * 4 / 3
	y := make([]float32, n)
	for i := range y {
		t := float64(i) * 0.75
		c := int(t)
		var acc float64
		for k := c - 24; k <= c+24; k++ {
			if k < 0 || k >= len(x) {
				continue
			}
			d := t - float64(k)
			s := 1.0
			if d != 0 {
				s = math.Sin(math.Pi*d) / (math.Pi * d)
			}
			w := 0.5 + 0.5*math.Cos(math.Pi*d/25)
			acc += float64(x[k]) * s * w
		}
		y[i] = float32(acc)
	}
	return y
}

// The whole receive path with WSJT-X's decoder: 16 kHz capture -> slots -> jt9.
// The audio arrives 1.5 s late (as with a box clock that's 1.5 s fast); the box
// should notice from the DTs and line its slots up.
func TestFT8EndToEnd(t *testing.T) {
	jt9 := os.Getenv("FT8_TEST_JT9")
	if jt9 == "" {
		t.Skip("set FT8_TEST_JT9 to a jt9 binary")
	}
	sample := readWAV12k(t, os.Getenv("FT8_TEST_WAV"))
	jt9Path, ft8Dir = jt9, t.TempDir()
	audio := up12to16(sample[:15*12000])
	now := time.Date(2026, 10, 19, 13, 34, 29, 0, time.UTC) // 1 s before a slot
	ft8Clock = func() time.Time { return now }
	defer func() { ft8Clock = time.Now }()

	var f ft8Rx
	f.setOn(true, 3, "KN0BLE")
	slot0 := time.Date(2026, 10, 19, 13, 34, 30, 0, time.UTC)
	lag := 1500 * time.Millisecond
	sampleAt := func(tm time.Time) float32 { // the sample repeats every slot, 1.5 s late
		d := tm.Sub(slot0) - lag
		if d < 0 {
			return 0
		}
		i := int(d.Seconds()*16000) % (15 * 16000)
		if i >= len(audio) {
			return 0
		}
		return audio[i]
	}
	var results []int
	var dts []float64
	seen := int64(0)
	for step := 0; step < 4*15*20; step++ { // 4 slots in 50 ms blocks
		blk := make([]float32, 800)
		for i := range blk {
			blk[i] = sampleAt(now.Add(time.Duration(i-800) * time.Second / 16000))
		}
		f.push(blk, 16000)
		f.tick()
		now = now.Add(50 * time.Millisecond)
		for { // decode runs in the background; wait for it in "real" time
			f.mu.Lock()
			busy := f.busy
			f.mu.Unlock()
			if !busy {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if st := f.state(); st.Seq != seen {
			seen = st.Seq
			results = append(results, st.LastN)
			l := f.list()
			var s float64
			for _, d := range l[len(l)-st.LastN:] {
				s += d.DT
			}
			dts = append(dts, s/float64(max(1, st.LastN)))
			t.Logf("slot %s: %d decodes, mean DT %.2f, offset now %.1f s", st.Slot, st.LastN, dts[len(dts)-1], st.Offset)
		}
	}
	if len(results) < 3 {
		t.Fatalf("only %d slots decoded", len(results))
	}
	last := len(results) - 1
	if results[last] < 18 {
		t.Fatalf("after the clock correction expected ~21 decodes, got %d", results[last])
	}
	if math.Abs(dts[last]) > 0.6 {
		t.Fatalf("clock correction didn't converge: mean DT %.2f", dts[last])
	}
}
