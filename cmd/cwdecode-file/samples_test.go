package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/deepcw"
)

// Regression test: the sample recordings shipped with CW Decoder 1.1.1 (testdata/samples)
// must keep decoding to exactly this text with both decoders, using the same defaults as
// the command-line tool.

const qso = "KN0BLE DE W1AW GM UR RST 599 599 NAME JOSH QTH ORLANDO FL BK"

type sample struct {
	file string
	want string
	// classicTail: the classic decoder only has to copy the end of want (see 03).
	classicTail string
}

var samples = []sample{
	{file: "01-clean-20wpm.wav", want: "CQ CQ DE KN0BLE KN0BLE K"},
	{file: "02-noisy-qso-22wpm.wav", want: qso},
	// Known limitation: at 35 WPM the classic decoder needs the first call to lock on to
	// the speed and drops "KN0". The AI decoder copies it all. Tighten this when fixed.
	{file: "03-fast-qso-35wpm.wav", want: qso, classicTail: "BLE DE W1AW GM UR RST 599 599 NAME JOSH QTH ORLANDO FL BK"},
	{file: "04-qso-with-qrm.wav", want: qso},
}

func load(t *testing.T, name string) ([]float32, uint32) {
	t.Helper()
	s, rate, err := cw.ReadWAV(filepath.Join("..", "..", "testdata", "samples", name))
	if err != nil {
		t.Fatal(err)
	}
	return s, rate
}

func TestSamplesClassic(t *testing.T) {
	for _, c := range samples {
		t.Run(c.file, func(t *testing.T) {
			s, rate := load(t, c.file)
			got, _, _ := decodeClassic(s, rate, 0, 0.5, true)
			if c.classicTail != "" {
				if !strings.HasSuffix(got, c.classicTail) {
					t.Fatalf("got  %q\nwant ...%q", got, c.classicTail)
				}
				if got == c.want {
					t.Logf("classic now copies all of %s: tighten this test", c.file)
				}
				return
			}
			if got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestSamplesAI(t *testing.T) {
	if testing.Short() {
		t.Skip("AI decoder is slow; skipped with -short")
	}
	m, err := deepcw.Load(filepath.Join("..", "..", "third_party", "deepcw-engine"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range samples {
		t.Run(c.file, func(t *testing.T) {
			s, rate := load(t, c.file)
			got, err := decodeAI(m, s, rate, 0, deepcw.FocusWidth, deepcw.FocusFloor)
			if err != nil {
				t.Fatal(err)
			}
			if got = strings.TrimSpace(got); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}
