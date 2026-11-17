//go:build linux || darwin

package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/rig"
)

func TestPortBusMeterSkipsKeyLease(t *testing.T) {
	b := newPortBus()
	key := "/dev/fake-fx4cr"
	b.live[key] = &sharedPort{key: key, path: key, skipIO: true, refs: 1, fd: -1}
	_, rel, err := b.holdLease(key, 0, rig.PriKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	_, err = b.exchange(key, 0, rig.PriMeter, []string{"RM1;"})
	if !errors.Is(err, rig.ErrBusy) {
		t.Fatalf("meter during key: %v", err)
	}
	_, err = b.exchange(key, 0, rig.PriState, []string{"FA;"})
	if !errors.Is(err, rig.ErrBusy) {
		t.Fatalf("state during key: %v", err)
	}
	// The CAT port beside the key port is a different name.
	other := "/dev/fake-ft710-cat"
	b.live[other] = &sharedPort{key: other, path: other, skipIO: true, refs: 1, fd: -1}
	// exchange retain on a skipIO port with fd -1 fails the open if it isn't
	// already live. It is live, but transact refuses fd < 0. The lease itself
	// must still be granted: acquire returns before the write.
	err = b.acquire(other, rig.PriMeter, func() error { return nil })
	if err != nil {
		t.Fatalf("other port: %v", err)
	}
}

func TestCloseLeavesTheLineUnkeyed(t *testing.T) {
	p := &sharedPort{key: "k", path: "k", skipIO: true, refs: 1}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		p.closed = false
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = p.setLine(false, true)
		}()
		go func() {
			defer wg.Done()
			p.shutdown()
		}()
		wg.Wait()
		p.mu.Lock()
		if p.dtr || p.rts || !p.closed {
			p.mu.Unlock()
			t.Fatalf("port left keyed: dtr %v rts %v closed %v", p.dtr, p.rts, p.closed)
		}
		p.mu.Unlock()
	}
}

func TestStopSweepWaitUnkeysBeforeClose(t *testing.T) {
	e := &Engine{}
	var mu sync.Mutex
	var order []string
	e.rp.done = make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		order = append(order, "unkey")
		mu.Unlock()
		close(e.rp.done)
	}()
	e.mu.Lock()
	e.stopSweepWait()
	mu.Lock()
	order = append(order, "close")
	got := stringsJoin(order)
	mu.Unlock()
	e.mu.Unlock()
	if got != "unkey,close" {
		t.Fatalf("order %s", got)
	}
}

func stringsJoin(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
