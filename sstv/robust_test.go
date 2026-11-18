package sstv

import (
	"math"
	"math/rand"
	"testing"
)

// feed gives the decoder audio in blocks of random size, like a sound card that hands over
// whatever it has. A panic anywhere fails the test.
func feed(d *Decoder, audio []float32, rng *rand.Rand) {
	for i := 0; i < len(audio); {
		n := 1 + rng.Intn(9000)
		if rng.Intn(10) == 0 {
			n = 1 + rng.Intn(60000)
		}
		j := min(i+n, len(audio))
		d.Write(audio[i:j])
		i = j
	}
}

func noise(rng *rand.Rand, n int, amp float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32((rng.Float64()*2 - 1) * amp)
	}
	return out
}

// Whatever comes out of a radio's USB sound card, the decoder must neither crash nor keep
// growing: noise, silence, a stuck signal, a picture that stops half way or is torn up by
// dropouts, two pictures on top of each other, at any sample rate the sound card might use.
func TestDecoderSurvivesGarbage(t *testing.T) {
	rates := []int{8000, 11025, 22050, 44100, 48000, 96000}
	if testing.Short() {
		rates = []int{8000, 44100, 48000}
	}
	card := TestCard(320, 256)
	for _, fs := range rates {
		rng := rand.New(rand.NewSource(int64(fs)))
		d := NewDecoder(fs)
		sec := func(s float64) int { return int(s * float64(fs)) }

		feed(d, noise(rng, sec(20), 1), rng)     // loud noise
		feed(d, noise(rng, sec(10), 0.001), rng) // hiss
		feed(d, make([]float32, sec(5)), rng)    // silence
		sq := make([]float32, sec(5))            // a stuck full-scale square wave
		for i := range sq {
			sq[i] = float32(1 - 2*((i/(fs/1200))%2))
		}
		feed(d, sq, rng)
		dc := make([]float32, sec(3)) // a DC offset
		for i := range dc {
			dc[i] = 0.9
		}
		feed(d, dc, rng)
		ext := noise(rng, sec(3), 10) // the most the engine lets through
		feed(d, ext, rng)

		// a real picture, cut off at a random point and followed by noise
		pic := Encode(card, ByName("Robot 36"), fs)
		for k := 0; k < 3; k++ {
			cut := rng.Intn(len(pic))
			part := append(append([]float32{}, pic[:cut]...), noise(rng, sec(8), 0.5)...)
			feed(d, part, rng)
		}
		// a picture torn by dropouts
		torn := append([]float32{}, pic...)
		for k := 0; k < 25; k++ {
			at, n := rng.Intn(len(torn)), sec(0.05+rng.Float64()*0.5)
			for i := at; i < min(at+n, len(torn)); i++ {
				torn[i] = 0
			}
		}
		feed(d, torn, rng)
		// two pictures at once
		other := Encode(card, ByName("Martin 2"), fs)
		mix := make([]float32, max(len(pic), len(other)))
		for i := range mix {
			if i < len(pic) {
				mix[i] += pic[i] * 0.6
			}
			if i < len(other) {
				mix[i] += other[i] * 0.6
			}
		}
		feed(d, mix, rng)

		// and then it still works: a clean picture decodes
		d.Reset()
		done, _ := run(t, d, pad(Encode(card, ByName("Martin 2"), fs), fs))
		if done == nil {
			t.Errorf("%d Hz: no picture decoded after the garbage", fs)
		}
	}
}

// Idle, the decoder keeps a few seconds of audio, not everything it has ever heard.
func TestDecoderMemoryStaysBounded(t *testing.T) {
	fs := 48000
	d := NewDecoder(fs)
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 60; i++ { // a minute of noise
		d.Write(noise(rng, fs, 1))
	}
	if limit := int(12 * d.fw); len(d.freq) > limit {
		t.Errorf("holding %d samples (%.0f s) after a minute of noise", len(d.freq), float64(len(d.freq))/d.fw)
	}
}

// FuzzDecoder: go test -fuzz=FuzzDecoder ./sstv  (the seeds run in a normal go test).
func FuzzDecoder(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8}, byte(0))
	f.Add(make([]byte, 4000), byte(3))
	f.Add([]byte("a short burst of text is not a picture, but it must not crash anything"), byte(6))
	hdr := Encode(TestCard(32, 32), ByName("Robot 36"), 8000)[:8000*2]
	b := make([]byte, len(hdr))
	for i, v := range hdr {
		b[i] = byte(int(math.Round(float64(v)*120)) + 128)
	}
	f.Add(b, byte(0))
	rates := []int{8000, 11025, 16000, 22050, 44100, 48000, 96000, 192000}
	f.Fuzz(func(t *testing.T, raw []byte, r byte) {
		fs := rates[int(r)%len(rates)]
		samples := make([]float32, len(raw))
		for i, v := range raw {
			samples[i] = (float32(v) - 128) / 128
		}
		d := NewDecoder(fs)
		d.Write(samples)
		d.Write(samples)
	})
}
