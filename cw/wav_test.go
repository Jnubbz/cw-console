package cw

import (
	"bytes"
	"strings"
	"testing"
)

func TestWAVRoundTripAndDecode(t *testing.T) {
	sig := append(zeros(1200), MorseSamples("CQ DE KN0BLE K", 22, 700, 12000)...)
	sig = append(sig, zeros(12000)...)
	var buf bytes.Buffer
	if err := WriteWAV16(&buf, sig, 12000); err != nil {
		t.Fatal(err)
	}
	s, rate, err := ParseWAV(buf.Bytes())
	if err != nil || rate != 12000 || len(s) != len(sig) {
		t.Fatalf("parse: %v rate=%d n=%d", err, rate, len(s))
	}
	d := NewStreamDecoder(float32(rate), 700)
	d.Push(s)
	if got := strings.TrimSpace(d.Transcript()); got != "CQ DE KN0BLE K" {
		t.Fatalf("got %q", got)
	}
}

func TestFindPitchLandsOnTheTone(t *testing.T) {
	for _, f := range []float32{450, 600, 703, 875, 1040} {
		sig := MorseSamples("TEST TEST", 20, f, 12000)
		sig = mix(sig, whiteNoise(len(sig), 0.1))
		p, ratio := FindPitch(sig, 12000, 300, 1200, 10)
		if p < f-10 || p > f+10 || ratio < 5 {
			t.Fatalf("tone %v: found %v ratio %v", f, p, ratio)
		}
	}
}

func TestRejectsNonWAV(t *testing.T) {
	if _, _, err := ParseWAV([]byte("hello world, not a wav")); err == nil {
		t.Fatal("expected error")
	}
}
