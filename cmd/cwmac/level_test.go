package main

import (
	"math"
	"testing"
	"time"
)

func TestAudioLevel(t *testing.T) {
	var l audioLevel
	t0 := time.Now()
	if l.read(t0) != nil {
		t.Fatal("nothing heard should read nil")
	}
	sine := make([]float32, 4800)
	for i := range sine {
		sine[i] = 0.5 * float32(math.Sin(2*math.Pi*700*float64(i)/48000))
	}
	l.add(sine, t0)
	m := l.read(t0)
	// 0.5 peak = -6.0 dBFS; a sine's RMS is 3 dB under its peak.
	if m == nil || math.Abs(float64(m.Peak)+6.0) > 0.1 || math.Abs(float64(m.RMS)+9.0) > 0.1 {
		t.Fatalf("got %+v", m)
	}
	l.add(make([]float32, 4800), t0.Add(time.Second))
	if m := l.read(t0.Add(time.Second)); m.Peak > -5.9 {
		t.Fatalf("the loud block is still in the window: %+v", m)
	}
	if m := l.read(t0.Add(2 * time.Second)); m == nil || m.Peak != -120 {
		t.Fatalf("only silence left: %+v", m)
	}
	if m := l.read(t0.Add(5 * time.Second)); m != nil {
		t.Fatalf("old audio should age out: %+v", m)
	}
}
