package cw

// Port of the unit tests in Nexus crates/tempo-core/src/cw_decode.rs — same inputs,
// same assertions.

import (
	"math"
	"os"
	"strings"
	"testing"
)

func init() { defaultImproved = os.Getenv("CW_IMPROVED") == "1" }

const tSR float32 = 48000
const tPitch float32 = 600

func zeros(n int) []float32 { return make([]float32, n) }

func decodeT(text string, wpm uint32) Decode {
	a := zeros(int(tSR * 0.1))
	a = append(a, MorseSamples(text, wpm, tPitch, uint32(tSR))...)
	a = append(a, zeros(int(tSR*0.1))...)
	return DecodeCW(a, tSR, tPitch)
}

func absI(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestDecodesACleanCallsignAndEstimatesWPM(t *testing.T) {
	d := decodeT("CQ TEST DE W1ABC", 20)
	if d.Text != "CQ TEST DE W1ABC" {
		t.Fatalf("got %q", d.Text)
	}
	if absI(int(d.WPM)-20) > 2 {
		t.Fatalf("wpm %d", d.WPM)
	}
}

func TestDecodesAcrossSpeeds(t *testing.T) {
	for _, c := range []struct {
		s string
		w uint32
	}{{"PARIS", 15}, {"599 TU", 25}, {"K", 30}} {
		if got := decodeT(c.s, c.w).Text; got != c.s {
			t.Fatalf("%s@%d got %q", c.s, c.w, got)
		}
	}
}

func steady(n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(float64(2 * math.Pi * float64(tPitch) * float64(i) / float64(tSR))))
	}
	return s
}

func TestEmptyOnSilenceAndSteadyTone(t *testing.T) {
	if d := DecodeCW(zeros(48000), tSR, tPitch); d != (Decode{}) {
		t.Fatalf("silence: %+v", d)
	}
	if d := DecodeCW(steady(48000), tSR, tPitch); d.Text != "" {
		t.Fatalf("steady: %+v", d)
	}
}

func TestSkimmerFindsASignalAtItsPitch(t *testing.T) {
	a := zeros(int(tSR * 0.1))
	a = append(a, MorseSamples("CQ TEST", 22, 600, uint32(tSR))...)
	hits := SkimCW(a, tSR, 300, 1200, 50)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	found := false
	for _, h := range hits {
		if h.Text == "CQ TEST" {
			found = true
		}
		if absI(int(h.PitchHz)-600) > 300 {
			t.Fatalf("far hit %+v", h)
		}
	}
	if !found {
		t.Fatalf("text not decoded: %+v", hits)
	}
}

func TestSkimmerEmptyOnSilence(t *testing.T) {
	if len(SkimCW(zeros(48000), tSR, 300, 1200, 50)) != 0 {
		t.Fatal("hits on silence")
	}
}

func TestMorseToCharReversesTheTable(t *testing.T) {
	for code, want := range map[string]rune{".": 'E', "-.-.": 'C', ".....": '5'} {
		if c, ok := morseToChar(code); !ok || c != want {
			t.Fatalf("%s -> %c", code, c)
		}
	}
	if _, ok := morseToChar(".-.-"); ok {
		t.Fatal(".-.- should be none")
	}
}

func morseAudio(text string, wpm uint32, trail float32) []float32 {
	a := zeros(int(tSR * 0.1))
	a = append(a, MorseSamples(text, wpm, tPitch, uint32(tSR))...)
	return append(a, zeros(int(tSR*trail))...)
}

func TestStreamDecodesACallsignAndEstimatesWPM(t *testing.T) {
	d := NewStreamDecoder(tSR, tPitch)
	d.Push(morseAudio("CQ TEST DE W1ABC", 20, 0.6))
	if got := strings.TrimSpace(d.Transcript()); got != "CQ TEST DE W1ABC" {
		t.Fatalf("got %q", got)
	}
	if absI(int(d.WPM())-20) > 3 {
		t.Fatalf("wpm %d", d.WPM())
	}
}

func TestStreamAccumulatesAcrossSmallChunks(t *testing.T) {
	a := morseAudio("PARIS DE K1ABC", 25, 0.6)
	d := NewStreamDecoder(tSR, tPitch)
	for i := 0; i < len(a); i += 1000 {
		e := i + 1000
		if e > len(a) {
			e = len(a)
		}
		d.Push(a[i:e])
	}
	if got := strings.TrimSpace(d.Transcript()); got != "PARIS DE K1ABC" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamKeepsEarlierTextWhenMoreArrives(t *testing.T) {
	d := NewStreamDecoder(tSR, tPitch)
	d.Push(morseAudio("K", 20, 0.6))
	if strings.TrimSpace(d.Transcript()) != "K" {
		t.Fatalf("got %q", d.Transcript())
	}
	d.Push(morseAudio("W1ABC", 20, 0.6))
	if !strings.Contains(d.Transcript(), "K") || !strings.Contains(d.Transcript(), "W1ABC") {
		t.Fatalf("got %q", d.Transcript())
	}
}

func TestStreamStaysSilentOnNoiseAndUnkeyedCarrier(t *testing.T) {
	d := NewStreamDecoder(tSR, tPitch)
	d.Push(zeros(int(tSR)))
	if d.Transcript() != "" {
		t.Fatalf("silence: %q", d.Transcript())
	}
	d2 := NewStreamDecoder(tSR, tPitch)
	d2.Push(steady(int(tSR)))
	if d2.Transcript() != "" {
		t.Fatalf("carrier: %q", d2.Transcript())
	}
}

type xorshift struct{ s uint64 }

func (x *xorshift) next() uint64 {
	x.s ^= x.s << 13
	x.s ^= x.s >> 7
	x.s ^= x.s << 17
	return x.s
}

func crashStorm(secs float32) []float32 {
	r := &xorshift{0x2545F4914F6CDD1D}
	rnd := func() float32 { return float32(r.next()>>40) / 16777216.0 }
	n := int(tSR * secs)
	a := make([]float32, n)
	for i := 0; i < n; {
		on := int(tSR * (0.004 + 0.05*rnd()))
		off := int(tSR * (0.01 + 0.08*rnd()))
		amp := 0.3 + 0.7*rnd()
		for j := 0; j < on && i+j < n; j++ {
			a[i+j] = amp * float32(math.Sin(float64(2*math.Pi*float64(tPitch)*float64(i+j)/float64(tSR))))
		}
		i += on + off
	}
	return a
}

func glyphs(s string) int {
	n := 0
	for _, c := range s {
		if c != ' ' && c != '\t' && c != '\n' {
			n++
		}
	}
	return n
}

func stormGlyphs(sens float32) int {
	d := NewStreamDecoder(tSR, tPitch)
	d.SetSensitivity(sens)
	d.Push(crashStorm(12))
	return glyphs(d.Transcript())
}

func TestSensitivitySliderVisiblyGatesANoiseStorm(t *testing.T) {
	strict, def, loose := stormGlyphs(0), stormGlyphs(0.5), stormGlyphs(1)
	if !(strict*2 < def) {
		t.Fatalf("strict=%d default=%d", strict, def)
	}
	if !(def < loose) {
		t.Fatalf("default=%d loose=%d", def, loose)
	}
}

func TestStrictSensitivityStillCopiesCleanCW(t *testing.T) {
	d := NewStreamDecoder(tSR, tPitch)
	d.SetSensitivity(0)
	d.Push(morseAudio("CQ DX DE W1ABC", 22, 0.6))
	if got := strings.TrimSpace(d.Transcript()); got != "CQ DX DE W1ABC" {
		t.Fatalf("got %q", got)
	}
}

func whiteNoise(n int, amp float32) []float32 {
	r := &xorshift{0x9E3779B97F4A7C15}
	o := make([]float32, n)
	for i := range o {
		o[i] = amp * (float32(r.next()>>33)/float32(uint32(1)<<31) - 1)
	}
	return o
}

func mix(sig, noise []float32) []float32 {
	o := make([]float32, len(sig))
	for i := range sig {
		o[i] = clampF(sig[i]+noise[i], -1, 1)
	}
	return o
}

func TestOffPitchSNRLetsTheSliderTradeCopyInBroadbandNoise(t *testing.T) {
	cp := func(sens float32) string {
		d := NewStreamDecoder(tSR, tPitch)
		d.SetSensitivity(sens)
		sig := MorseSamples("PARIS PARIS PARIS PARIS", 18, tPitch, uint32(tSR))
		for i := range sig {
			sig[i] *= 0.30
		}
		d.Push(mix(sig, whiteNoise(len(sig), 0.15)))
		return d.Transcript()
	}
	strict, loose := cp(0), cp(1)
	if !strings.Contains(loose, "PARIS") || glyphs(loose) == 0 {
		t.Fatalf("loose lost copy: %q", loose)
	}
	if glyphs(loose) < glyphs(strict) {
		t.Fatalf("strict=%q loose=%q", strict, loose)
	}
}

func TestStreamSuppressesNearNoiseMarks(t *testing.T) {
	dit := int(tSR * 0.06)
	var sig []float32
	for k := 0; k < 40; k++ {
		for j := 0; j < dit; j++ {
			n := len(sig) + j // same expression as Nexus (len grows as we push)
			sig = append(sig, 0.10*float32(math.Sin(float64(2*math.Pi*float64(tPitch)*float64(n)/float64(tSR)))))
		}
		sig = append(sig, zeros(3*dit)...)
	}
	d := NewStreamDecoder(tSR, tPitch)
	d.Push(mix(sig, whiteNoise(len(sig), 0.14)))
	if j := glyphs(d.Transcript()); j > 8 {
		t.Fatalf("storm %d: %q", j, d.Transcript())
	}
}

func TestStreamClearAndRetuneResetText(t *testing.T) {
	d := NewStreamDecoder(tSR, tPitch)
	d.Push(morseAudio("K", 20, 0.6))
	if d.Transcript() == "" {
		t.Fatal("empty")
	}
	d.Clear()
	if d.Transcript() != "" {
		t.Fatal("not cleared")
	}
	d.Push(morseAudio("E", 20, 0.6))
	if strings.TrimSpace(d.Transcript()) != "E" {
		t.Fatalf("got %q", d.Transcript())
	}
	d.Retune(tPitch + 200)
	if d.Transcript() != "" {
		t.Fatal("retune kept text")
	}
}

// Extra (not in Nexus): TakeText must not disturb an in-progress character.
func TestTakeTextKeepsDecodingMidCharacter(t *testing.T) {
	a := morseAudio("CQ TEST DE KN0BLE", 20, 0.6)
	d := NewStreamDecoder(tSR, tPitch)
	var got strings.Builder
	for i := 0; i < len(a); i += 777 {
		e := i + 777
		if e > len(a) {
			e = len(a)
		}
		d.Push(a[i:e])
		got.WriteString(d.TakeText())
	}
	if s := strings.TrimSpace(got.String()); s != "CQ TEST DE KN0BLE" {
		t.Fatalf("got %q", s)
	}
}
