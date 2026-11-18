package ft8

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReferenceTones(t *testing.T) {
	// Tone strings and packed payloads from the FT8 protocol (CRC-14, LDPC
	// (174,91), Gray 8-FSK, Costas). Captured from a reference encoder.
	cases := []struct {
		msg, tones, pack string
	}{
		{"CQ KN0BLE EN34", "3140652000000001115043266605136530473140652710010337142246660722007770023140652", "00000024c193b6885588"},
		{"CQ POTA KN0BLE EL98", "3140652000577647615043266605042530453140652316515607733055361063754146153140652", "004feef4c193b6883388"},
		{"CQ DX KN0BLE EN34", "3140652000001047615043266605136525173140652354602205701227345147156154553140652", "000046f4c193b6885588"},
		{"KN0BLE W1AW EN34", "3140652540211444506774623105136532643140652654633606675161750766337637603140652", "983276d05ff568885588"},
		{"KN0BLE W1AW", "3140652540211444506774623117455326403140652245644542350136727364212155663140652", "983276d05ff5689fa448"},
		{"W1AW KN0BLE -15", "3140652037773660615043266617461023503140652535043234506370260432744771533140652", "0bfead14c193b69fa908"},
		{"W1AW KN0BLE +05", "3140652037773660615043266617464027133140652513026365054126430646472236123140652", "0bfead14c193b69fae08"},
		{"W1AW KN0BLE R-15", "3140652037773660615043266627461022133140652401725774772144115534410455733140652", "0bfead14c193b6bfa908"},
		{"W1AW KN0BLE R+05", "3140652037773660615043266627464024503140652427742405220332545740706310323140652", "0bfead14c193b6bfae08"},
		{"W1AW KN0BLE RR73", "3140652037773660615043266617455426223140652144507137070307607054406674423140652", "0bfead14c193b69fa4c8"},
		{"W1AW KN0BLE RRR", "3140652037773660615043266617455532013140652514306712166122501136557537243140652", "0bfead14c193b69fa488"},
		{"W1AW KN0BLE 73", "3140652037773660615043266617456025013140652664504722234751562647273221553140652", "0bfead14c193b69fa508"},
		{"W1AW KN0BLE/P EN34", "3140652037773660615043266655136564663140652443042756574751662315423736303140652", "0bfead14c193b6c85590"},
		{"CQ KN0BLE/QRP", "3140652000000434355370611622720206053140652174375447463372460636032063553140652", "000032ca45c526b7d860"},
		{"73", "3140652045704737046276136050000012663140652754471157152634013156103707373140652", "1a71bae357e954400000"},
		{"TU 73", "3140652210455575266303433615000003653140652025302737165663233760357151673140652", "6469278ed4164a980000"},
		{"TNX 73 GL", "3140652207447145545361557770000004743140652025531515613601314353267455603140652", "63edce93454c9ff00000"},
	}
	for _, tc := range cases {
		got, err := Symbols(tc.msg)
		if err != nil {
			t.Errorf("%q: %v", tc.msg, err)
			continue
		}
		if got != tc.tones {
			t.Errorf("%q tones\n got %s\nwant %s", tc.msg, got, tc.tones)
		}
		p, err := Pack(tc.msg)
		if err != nil {
			t.Errorf("%q pack: %v", tc.msg, err)
			continue
		}
		if hex10(p) != tc.pack {
			t.Errorf("%q payload %s want %s", tc.msg, hex10(p), tc.pack)
		}
		low := got
		got2, err := Symbols("  " + lower(tc.msg) + " ")
		if err != nil || got2 != low {
			t.Errorf("%q case/space: %v %s", tc.msg, err, got2)
		}
	}
}

func TestWaveMatchesReferenceSamples(t *testing.T) {
	// Checkpoints from the same reference encoder, CQ KN0BLE EN34 at 1500 Hz.
	w, err := Wave("CQ KN0BLE EN34", 1500, 12000)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 151680 {
		t.Fatalf("12 kHz length %d", len(w))
	}
	check := []struct {
		i int
		v float32
	}{
		{0, 0},
		{1, 3.0578343e-05},
		{10, 0.0042569595},
		{100, -0.30813417},
		{1920, -0.32457504},
		{1921, 0.44518468},
		{75840, 0.003519051},
		{151679, 0},
	}
	for _, c := range check {
		if d := math.Abs(float64(w[c.i] - c.v)); d > 2e-4 {
			t.Errorf("12 kHz sample %d = %g, reference %g (diff %g)", c.i, w[c.i], c.v, d)
		}
	}
	w48, err := Wave("CQ KN0BLE EN34", 1500, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if len(w48) != 79*7680 {
		t.Fatalf("48 kHz length %d", len(w48))
	}
	check48 := []struct {
		i int
		v float32
	}{
		{1, 5.2972808e-07},
		{10, 0.00024474697},
		{100, 0.022759769},
		{7680, -0.32573393},
		{7681, -0.13340335},
		{303360, 0.0060014366},
	}
	for _, c := range check48 {
		if d := math.Abs(float64(w48[c.i] - c.v)); d > 2e-4 {
			t.Errorf("48 kHz sample %d = %g, reference %g (diff %g)", c.i, w48[c.i], c.v, d)
		}
	}
	// The first symbol is Costas tone 3: 1500 + 3*6.25 Hz.
	if hz := dominant(w48[:7680], 48000); math.Abs(hz-(1500+3*6.25)) > 1 {
		t.Errorf("first symbol at %g Hz, want %g", hz, 1500+3*6.25)
	}
}

func TestRefusesAMessageThatDoesNotFit(t *testing.T) {
	if _, err := Pack("THIS MESSAGE IS WAY TOO LONG"); err == nil {
		t.Fatal("accepted a message that is not standard or free text")
	}
	if _, err := Pack(""); err == nil {
		t.Fatal("accepted an empty message")
	}
}

// TestAgainstFT8TX compares the native waveform with the external helper when
// one is installed (CWBOX_FT8TX, or ft8tx on PATH). CI machines without it skip.
func TestAgainstFT8TX(t *testing.T) {
	bin := os.Getenv("CWBOX_FT8TX")
	if bin == "" {
		p, err := exec.LookPath("ft8tx")
		if err != nil {
			t.Skip("ft8tx is not installed")
		}
		bin = p
	}
	msg := "CQ KN0BLE EN34"
	dir := t.TempDir()
	out := filepath.Join(dir, "wave.f32")
	cmd := exec.Command(bin, msg, "1500", out)
	if b, err := cmd.Output(); err != nil {
		t.Skipf("ft8tx did not run (%v): %s", err, b)
	}
	raw, err := os.ReadFile(out)
	if err != nil || len(raw) < 16 || len(raw)%4 != 0 {
		t.Fatalf("ft8tx output: %v, %d bytes", err, len(raw))
	}
	ref := make([]float32, len(raw)/4)
	for i := range ref {
		ref[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	got, err := Wave(msg, 1500, 48000)
	if err != nil {
		t.Fatal(err)
	}
	corr := correlate(got, ref)
	if corr < 0.98 {
		t.Fatalf("native wave vs ft8tx correlation %g (lengths %d and %d)", corr, len(got), len(ref))
	}
}

func hex10(p []byte) string {
	const d = "0123456789abcdef"
	b := make([]byte, len(p)*2)
	for i, v := range p {
		b[2*i] = d[v>>4]
		b[2*i+1] = d[v&0x0f]
	}
	return string(b)
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func dominant(samples []float32, rate int) float64 {
	// A short Goertzel around the expected tone is enough to name it.
	best, bestP := 0.0, -1.0
	for hz := 1400.0; hz <= 1600; hz += 0.5 {
		w := 2 * math.Pi * hz / float64(rate)
		c := 2 * math.Cos(w)
		var s0, s1, s2 float64
		for _, v := range samples {
			s0 = float64(v) + c*s1 - s2
			s2, s1 = s1, s0
		}
		p := s1*s1 + s2*s2 - c*s1*s2
		if p > bestP {
			best, bestP = hz, p
		}
	}
	return best
}

func correlate(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	// Slide a window of the shorter over the longer by up to 2 symbols,
	// in case one side pads silence.
	short, long := a, b
	if len(a) > len(b) {
		short, long = b, a
	}
	step := 480
	best := 0.0
	limit := len(long) - len(short)
	if limit > 2*7680 {
		limit = 2 * 7680
	}
	if limit < 0 {
		return 0
	}
	for off := 0; off <= limit; off += step {
		var dot, ea, eb float64
		for i, v := range short {
			u := float64(long[off+i])
			x := float64(v)
			dot += x * u
			ea += x * x
			eb += u * u
		}
		if ea == 0 || eb == 0 {
			continue
		}
		c := dot / math.Sqrt(ea*eb)
		if c > best {
			best = c
		}
	}
	return best
}

func TestSampleCountFollowsTheRate(t *testing.T) {
	for _, rate := range []int{8000, 12000, 44100, 48000} {
		w, err := Wave("KN0BLE W1AW EN34", 1000, rate)
		if err != nil {
			t.Fatal(err)
		}
		n := 79 * int(0.5+float64(rate)*0.16)
		if len(w) != n {
			t.Errorf("rate %d: %d samples, want %d", rate, len(w), n)
		}
	}
}
