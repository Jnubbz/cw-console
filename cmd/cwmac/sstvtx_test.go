package main

import (
	"errors"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/keying"
)

type fakeSSTVLine struct {
	down      bool
	failKey   bool
	failUnkey bool
	calls     []bool
}

func (f *fakeSSTVLine) Key(on bool) error {
	f.calls = append(f.calls, on)
	if on && f.failKey {
		return errors.New("cat refused")
	}
	if !on && f.failUnkey {
		return errors.New("port gone")
	}
	f.down = on
	return nil
}

func TestSSTVUnkeysOnDeadmanAndError(t *testing.T) {
	idle := time.Second
	now := time.Now()
	line := &fakeSSTVLine{}
	tx := &sstvTx{
		watch: keying.Watch{Idle: func() time.Duration { return idle }},
		now:   func() time.Time { return now },
	}
	if err := tx.start(line, time.Minute); err != nil {
		t.Fatal(err)
	}
	tx.tick()
	if !line.down || !tx.keyedNow() {
		t.Fatal("unkeyed inside the 5s window")
	}

	idle = 6 * time.Second
	tx.tick()
	if line.down || tx.keyedNow() {
		t.Fatal("page disconnect left the radio keyed")
	}
	if len(line.calls) < 2 || line.calls[len(line.calls)-1] {
		t.Fatalf("dead-man calls %v", line.calls)
	}

	line = &fakeSSTVLine{}
	now = time.Now()
	tx = &sstvTx{now: func() time.Time { return now }}
	if err := tx.start(line, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	tx.fail(errors.New("playback stopped"))
	if line.down || tx.keyedNow() {
		t.Fatal("an error left the radio keyed")
	}

	line = &fakeSSTVLine{failUnkey: true}
	now = time.Now()
	tx = &sstvTx{now: func() time.Time { return now }}
	if err := tx.start(line, time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	tx.tick()
	if tx.keyedNow() || !tx.retry {
		t.Fatal("a failed unkey was forgotten")
	}
	line.failUnkey = false
	tx.retryUnkey()
	if line.down || tx.retry {
		t.Fatal("retry did not unkey")
	}

	bad := &fakeSSTVLine{failKey: true}
	tx = &sstvTx{now: time.Now}
	if err := tx.start(bad, time.Second); err == nil || bad.down {
		t.Fatalf("failed key left the line down=%v err=%v", bad.down, err)
	}
	if len(bad.calls) != 2 || bad.calls[1] {
		t.Fatalf("failed key did not drop the line: %v", bad.calls)
	}
}
