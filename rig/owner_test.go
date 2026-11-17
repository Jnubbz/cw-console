package rig

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMeterDoesNotRunDuringKeyLease(t *testing.T) {
	o := NewMemOwner()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := o.Do(context.Background(), "fx4cr", PriKey, func(Port) error {
			close(started)
			<-release
			return nil
		})
		if err != nil {
			t.Errorf("key lease: %v", err)
		}
	}()
	<-started

	err := o.Do(context.Background(), "fx4cr", PriMeter, func(Port) error {
		t.Fatal("meter exchange ran while the key lease was held")
		return nil
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("meter during key: got %v, want ErrBusy", err)
	}
	err = o.Do(context.Background(), "fx4cr", PriState, func(Port) error {
		t.Fatal("state poll ran while the key lease was held")
		return nil
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("state during key: got %v, want ErrBusy", err)
	}

	// A different port (the FT-710 CAT port beside the key port) still runs.
	if err := o.Do(context.Background(), "ft710-cat", PriMeter, func(Port) error { return nil }); err != nil {
		t.Fatalf("CAT port meter: %v", err)
	}

	close(release)
	<-done
}

func TestCommandWaitsUntilKeyLeaseEnds(t *testing.T) {
	o := NewMemOwner()
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = o.Do(context.Background(), "fx4cr", PriKey, func(Port) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	ran := make(chan struct{})
	go func() {
		err := o.Do(context.Background(), "fx4cr", PriCommand, func(Port) error {
			close(ran)
			return nil
		})
		if err != nil {
			t.Errorf("command: %v", err)
		}
	}()
	select {
	case <-ran:
		t.Fatal("frequency command ran while the key lease was held")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("frequency command did not run after the key lease ended")
	}
}

func TestCancelledWaitReturns(t *testing.T) {
	o := NewMemOwner()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_ = o.Do(context.Background(), "fx4cr", PriKey, func(Port) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := o.Do(ctx, "fx4cr", PriCommand, func(Port) error {
		t.Fatal("command ran after its context was cancelled")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyPortRejected(t *testing.T) {
	o := NewMemOwner()
	if err := o.Do(context.Background(), "", PriMeter, func(Port) error { return nil }); err == nil {
		t.Fatal("empty port name was accepted")
	}
}
