package keying

import (
	"testing"
	"time"
)

func TestWatchAbortsOnlyABusyDisconnectedTransmitter(t *testing.T) {
	idle := 6 * time.Second
	w := Watch{Idle: func() time.Duration { return idle }}
	aborted := false
	abort := func() { aborted = true }

	if !w.Tick(true, abort) || !aborted {
		t.Fatal("a busy transmitter with the page gone was left running")
	}

	aborted = false
	idle = time.Second
	if w.Tick(true, abort) || aborted {
		t.Fatal("aborted inside the 5s window")
	}

	aborted = false
	idle = 6 * time.Second
	if w.Tick(false, abort) || aborted {
		t.Fatal("aborted a transmitter that was already idle")
	}
}

func TestWatchUsesTheCWInterval(t *testing.T) {
	if DisconnectAfter != 5*time.Second {
		t.Fatalf("dead-man %s, CW uses 5s", DisconnectAfter)
	}
}
