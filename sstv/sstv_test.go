package sstv

import (
	"image"
	"math"
	"math/rand"
	"testing"
)

// diff: mean absolute error per channel, leaving out pixels next to a bar edge.
func diff(a, b *image.RGBA) float64 {
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	bw := w / 8
	var s float64
	var n int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if y < h/2 && (x%bw < 6 || x%bw >= bw-6) {
				continue
			}
			if y >= h/2-2 && y <= h/2+2 {
				continue
			}
			for c := 0; c < 3; c++ {
				s += math.Abs(float64(a.Pix[(y*w+x)*4+c]) - float64(b.Pix[(y*w+x)*4+c]))
				n++
			}
		}
	}
	return s / float64(n)
}

func run(t *testing.T, d *Decoder, audio []float32) (done *Event, started *Mode) {
	t.Helper()
	for i := 0; i < len(audio); i += 4096 { // in blocks, like live audio
		j := min(i+4096, len(audio))
		for _, e := range d.Write(audio[i:j]) {
			switch e.Kind {
			case Started:
				started = e.Mode
			case Done:
				done = &e
			}
		}
	}
	return
}

func pad(audio []float32, fs int) []float32 {
	out := make([]float32, fs/2, len(audio)+fs)
	out = append(out, audio...)
	return append(out, make([]float32, fs/2)...)
}

func TestEveryModeRoundTrips(t *testing.T) {
	for _, m := range Modes {
		if testing.Short() && m.Duration() > 80 {
			continue
		}
		t.Run(m.Name, func(t *testing.T) {
			t.Parallel()
			card := TestCard(m.Width, m.Height)
			for _, fs := range []int{48000, 44100} {
				done, started := run(t, NewDecoder(fs), pad(Encode(card, m, fs), fs))
				if started != m {
					t.Fatalf("%d Hz: header not recognised (%v)", fs, started)
				}
				if done == nil {
					t.Fatalf("%d Hz: picture never finished", fs)
				}
				if e := diff(card, done.Image); e > 6 {
					t.Errorf("%d Hz: picture is off by %.1f levels", fs, e)
				}
				if done.Synced < m.radioLines()*9/10 {
					t.Errorf("%d Hz: only %d of %d syncs found", fs, done.Synced, m.radioLines())
				}
			}
		})
	}
}

func TestLineLengths(t *testing.T) {
	want := map[string]float64{"Scottie 1": 428.22, "Scottie 2": 277.692, "Scottie DX": 1050.3, "Martin 1": 446.446,
		"Martin 2": 226.798, "Robot 36": 150, "Robot 72": 300, "PD120": 508.48, "PD180": 754.24, "PD240": 1000}
	for _, m := range Modes {
		l, _, _ := m.layout()
		if math.Abs(l*1000-want[m.Name]) > 0.001 {
			t.Errorf("%s: line is %.3f ms, want %.3f", m.Name, l*1000, want[m.Name])
		}
	}
}

// Off-tune by 60 Hz (SSB mistuning) and the sender's sound card 0.08% fast: the picture
// must still come out straight and the right colours.
func TestOffTuneAndSlant(t *testing.T) {
	m := ByName("Scottie 2")
	card := TestCard(m.Width, m.Height)
	audio := pad(encode(card, m, 48000*1.0008, 60), 48000)
	done, _ := run(t, NewDecoder(48000), audio)
	if done == nil {
		t.Fatal("picture never finished")
	}
	if math.Abs(done.OffsetHz-60) > 8 {
		t.Errorf("offset measured as %.1f Hz, want 60", done.OffsetHz)
	}
	if math.Abs(done.SlantPPM-800) > 60 {
		t.Errorf("slant measured as %.0f ppm, want 800", done.SlantPPM)
	}
	if e := diff(card, done.Image); e > 7 {
		t.Errorf("picture is off by %.1f levels", e)
	}
}

func TestThroughNoise(t *testing.T) {
	m := ByName("Martin 2")
	card := TestCard(m.Width, m.Height)
	audio := pad(Encode(card, m, 48000), 48000)
	rng := rand.New(rand.NewSource(1))
	for i := range audio { // signal is 0.8 peak (0.57 rms); noise 0.18 rms in 24 kHz: about 10 dB
		audio[i] += float32(rng.NormFloat64() * 0.18)
	}
	done, _ := run(t, NewDecoder(48000), audio)
	if done == nil {
		t.Fatal("picture never finished")
	}
	if e := diff(card, done.Image); e > 20 {
		t.Errorf("picture is off by %.1f levels", e)
	}
	t.Logf("10 dB: off by %.1f levels, %d syncs", diff(card, done.Image), done.Synced)
}

func TestNoiseAloneStartsNothing(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	audio := make([]float32, 48000*120)
	for i := range audio {
		audio[i] = float32(rng.NormFloat64() * 0.3)
	}
	d := NewDecoder(48000)
	for i := 0; i < len(audio); i += 4096 {
		for _, e := range d.Write(audio[i:min(i+4096, len(audio))]) {
			if e.Kind == Started {
				t.Fatalf("noise started a %s picture", e.Mode.Name)
			}
		}
	}
	if len(d.freq) > 12000*9 {
		t.Errorf("buffer grew to %d samples while idle", len(d.freq))
	}
}

func TestTwoPicturesInARow(t *testing.T) {
	a, b := ByName("Robot 36"), ByName("Martin 2")
	audio := pad(Encode(TestCard(a.Width, a.Height), a, 48000), 48000)
	audio = append(audio, pad(Encode(TestCard(b.Width, b.Height), b, 48000), 48000)...)
	d := NewDecoder(48000)
	var got []string
	for i := 0; i < len(audio); i += 4096 {
		for _, e := range d.Write(audio[i:min(i+4096, len(audio))]) {
			if e.Kind == Done {
				got = append(got, e.Mode.Name)
			}
		}
	}
	if len(got) != 2 || got[0] != a.Name || got[1] != b.Name {
		t.Fatalf("got %v", got)
	}
}

// Tuning in partway through (no header heard): the decoder must lock on from the line
// syncs, name the mode from their spacing, and end the picture when the signal does.
func TestJoinsAPictureAlreadyUnderWay(t *testing.T) {
	for _, name := range []string{"Scottie 1", "Scottie 2", "Martin 1", "Martin 2", "Robot 36", "Robot 72", "PD120"} {
		m := ByName(name)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			card := TestCard(m.Width, m.Height)
			full := encode(card, m, 48000, 35)
			line, _, _ := m.layout()
			cut := int((0.91 + 30.4*line) * 48000) // header and 30 lines gone, mid-line
			audio := append(make([]float32, 24000), full[cut:]...)
			audio = append(audio, make([]float32, 48000*int(25*line+3))...)
			var done *Event
			var started *Event
			d := NewDecoder(48000)
			for i := 0; i < len(audio); i += 4096 {
				for _, e := range d.Write(audio[i:min(i+4096, len(audio))]) {
					if e.Kind == Started && started == nil {
						started = &e
					}
					if e.Kind == Done {
						done = &e
					}
				}
			}
			if started == nil || started.Mode != m || !started.Joined {
				t.Fatalf("did not lock on as %s: %+v", name, started)
			}
			if done == nil {
				t.Fatal("picture never finished")
			}
			if math.Abs(done.OffsetHz-35) > 15 {
				t.Errorf("offset measured as %.0f Hz, want 35", done.OffsetHz)
			}
			rows := m.Height / m.radioLines()
			skipped := 31 * rows // the first whole line heard is line 31
			h := done.Image.Bounds().Dy()
			if h < m.Height-skipped-2*rows || h > m.Height-skipped+rows {
				t.Errorf("picture is %d rows, want about %d", h, m.Height-skipped)
			}
			// compare with the card from the line it joined at
			var s float64
			var n int
			W := m.Width
			for y := 2; y < h-4; y++ {
				cy := y + skipped
				if cy >= m.Height || (cy >= m.Height/2-3 && cy <= m.Height/2+3) {
					continue
				}
				for x := 0; x < W; x++ {
					if bw := W / 8; cy < m.Height/2 && (x%bw < 6 || x%bw >= bw-6) {
						continue
					}
					for c := 0; c < 3; c++ {
						s += math.Abs(float64(card.Pix[(cy*W+x)*4+c]) - float64(done.Image.Pix[(y*W+x)*4+c]))
						n++
					}
				}
			}
			if e := s / float64(n); e > 8 {
				t.Errorf("picture is off by %.1f levels", e)
			}
		})
	}
}

func TestJoinsThroughNoise(t *testing.T) {
	m := ByName("Scottie 1")
	full := Encode(TestCard(m.Width, m.Height), m, 48000)
	audio := append([]float32{}, full[48000*20:48000*70]...)
	audio = append(audio, make([]float32, 48000*12)...)
	rng := rand.New(rand.NewSource(3))
	for i := range audio {
		audio[i] += float32(rng.NormFloat64() * 0.18) // about 10 dB
	}
	d := NewDecoder(48000)
	var done *Event
	for i := 0; i < len(audio); i += 4096 {
		for _, e := range d.Write(audio[i:min(i+4096, len(audio))]) {
			if e.Kind == Done {
				done = &e
			}
		}
	}
	if done == nil || done.Mode != m || !done.Joined {
		t.Fatalf("did not lock on through noise: %+v", done)
	}
	if h := done.Image.Bounds().Dy(); h < 100 || h > 125 { // 50 s of a 428 ms line: 116 lines
		t.Errorf("picture is %d rows, want about 116", h)
	}
	t.Logf("%d rows, %d syncs", done.Image.Bounds().Dy(), done.Synced)
}

// A transmission that stops partway, followed by a second picture: the first must end where
// its signal did (a partial), and the second must be decoded whole on its own.
func TestStopsEarlyThenNextPicture(t *testing.T) {
	a, b := ByName("Scottie 1"), ByName("Martin 2")
	full := Encode(TestCard(a.Width, a.Height), a, 48000)
	cut := full[:len(full)*45/100]
	rng := rand.New(rand.NewSource(3))
	noise := make([]float32, 48000*20)
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.25)
	}
	audio := pad(cut, 48000)
	audio = append(audio, noise...)
	audio = append(audio, pad(Encode(TestCard(b.Width, b.Height), b, 48000), 48000)...)
	d := NewDecoder(48000)
	type res struct {
		name string
		h    int
	}
	var got []res
	for i := 0; i < len(audio); i += 4096 {
		for _, e := range d.Write(audio[i:min(i+4096, len(audio))]) {
			if e.Kind == Done {
				got = append(got, res{e.Mode.Name, e.Image.Bounds().Dy()})
			}
		}
	}
	if len(got) != 2 || got[0].name != a.Name || got[1].name != b.Name {
		t.Fatalf("got %v", got)
	}
	if got[0].h >= a.Height*3/4 || got[0].h < a.Height/4 {
		t.Fatalf("partial picture is %d rows of %d", got[0].h, a.Height)
	}
	if got[1].h != b.Height {
		t.Fatalf("second picture is %d rows, want %d", got[1].h, b.Height)
	}
}
