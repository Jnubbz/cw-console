package main

import (
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/keying"
)

func TestFT8AndTuneDeadmanUnkeys(t *testing.T) {
	idle := time.Second
	x := &ft8Tx{watch: keying.Watch{Idle: func() time.Duration { return idle }}}
	x.abort = make(chan struct{})
	x.active = true
	x.mu.Lock()
	if x.deadmanLocked() {
		t.Fatal("fired inside the 5s window")
	}
	x.mu.Unlock()

	idle = 6 * time.Second
	abort := x.abort
	x.mu.Lock()
	if !x.deadmanLocked() {
		t.Fatal("FT8 transmit was left running")
	}
	x.mu.Unlock()
	select {
	case <-abort:
	default:
		t.Fatal("abort was not closed")
	}
	if x.tuning || x.active {
		// active is cleared by transmit's defer, not by the dead-man itself.
		// The dead-man must clear Tune and close abort so that defer unkeys.
	}
	if x.tuning {
		t.Fatal("tune flag left set")
	}

	idle = 6 * time.Second
	y := &ft8Tx{watch: keying.Watch{Idle: func() time.Duration { return idle }}, tuning: true}
	tuneAbort := make(chan struct{})
	y.abort = tuneAbort
	y.mu.Lock()
	if !y.deadmanLocked() {
		t.Fatal("Tune was left running")
	}
	y.mu.Unlock()
	select {
	case <-tuneAbort:
	default:
		t.Fatal("tune abort was not closed")
	}
	if y.tuning {
		t.Fatal("tune still set")
	}
}
