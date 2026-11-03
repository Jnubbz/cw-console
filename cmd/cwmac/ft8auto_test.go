package main

import (
	"testing"
	"time"
)

func TestFT8AutoDepth(t *testing.T) {
	f := &ft8Rx{}
	f.setOn(true, 3, "KN0BLE")
	f.slowerLocked()
	if f.depthNowLocked() != 2 || f.autoTextLocked() == "" {
		t.Fatal(f.depthNowLocked())
	}
	f.slowerLocked()
	f.slowerLocked()
	if f.depthNowLocked() != 1 {
		t.Fatal("floor")
	}
	for i := 0; i < 8; i++ { // quiet spell: back up one level
		if f.fast++; f.fast >= 8 {
			f.lower--
			f.fast = 0
		}
	}
	if f.depthNowLocked() != 2 {
		t.Fatal("recover", f.depthNowLocked())
	}
	f.setOn(true, 2, "KN0BLE") // a new choice resets
	if f.lower != 0 || f.depthNowLocked() != 2 {
		t.Fatal("reset")
	}
}

// A test transmission that ends just after a UTC slot boundary, with the receiver's
// clock running 2 s behind, belongs to the slot it was sent in, not the next one.
func TestTxMarkUsesCorrectedClock(t *testing.T) {
	old := ft8Clock
	defer func() { ft8Clock = old }()
	base := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC) // a slot boundary
	ft8Clock = func() time.Time { return base.Add(15500 * time.Millisecond) }
	x := &ft8Tx{off: 2}
	x.transmit(nil, 0.5, "", noKey{}, make(chan struct{}), 1100*time.Millisecond, true)
	if !x.transmittedIn(base) || x.transmittedIn(base.Add(15*time.Second)) {
		t.Fatalf("marked %v", x.txSlots)
	}
}

func TestFT8LogByHand(t *testing.T) {
	x := &ft8Tx{}
	if _, err := x.logCandidate(); err == nil {
		t.Fatal("nothing to log yet")
	}
	x.seq.their, x.seq.theirGrid, x.seq.sent, x.seq.rcvd = "W1AW", "FN31", "-10", "-12"
	l, err := x.logCandidate()
	if err != nil || l.Call != "W1AW" || l.Grid != "FN31" || l.Rcvd != "-12" {
		t.Fatal(l, err)
	}
	x.seq.finish() // contact over: still loggable by hand
	if l, _ := x.logCandidate(); l == nil || l.Call != "W1AW" {
		t.Fatal("last contact lost")
	}
	if x.state().LogCall != "W1AW" {
		t.Fatal("button")
	}
	x.noteLogged("W1AW")
	if _, err := x.logCandidate(); err == nil || x.state().LogCall != "" {
		t.Fatal("logged twice")
	}
}

func TestFT8LevelDB(t *testing.T) {
	if ft8LevelGain(100) != 1 || ft8LevelGain(0) != 0 {
		t.Fatal("ends")
	}
	// 50 = -30 dB, and the old 1-10 % working range (-40..-20 dB) is now mid-slider
	for lvl, want := range map[int]float64{50: 0.0316, 67: 0.1023, 33: 0.0100} {
		if g := ft8LevelGain(lvl); g < want*0.97 || g > want*1.03 {
			t.Fatalf("level %d: gain %.4f, want %.4f", lvl, g, want)
		}
	}
}
