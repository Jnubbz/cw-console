// Package rig is the single owner of a radio's serial ports and of any other
// line that keys the transmitter (a rigctld TCP session counts as one of those
// ports). Dialects stay where they are: Yaesu bytes in rig/ft710, and later the
// Kenwood commands that cmd/cwmac speaks and the Hamlib client in sstv-console.
//
// Nothing in this package opens a device or transmits. cmd/cwmac's port bus
// follows Do's lease rules: one open descriptor per port name, meter and state
// polls get ErrBusy while that port is held, and an operator command waits.
package rig

import (
	"context"
	"errors"
	"sync"
)

// Priority is how urgent a turn on a port is. Lower numbers run first.
// A PriKey holder blocks everyone else on that port. PriState and PriMeter
// do not wait behind PriKey: they return ErrBusy, so a meter poll cannot
// queue up and fire in the middle of a KY command. PriCommand (a frequency
// or mode the operator asked for) waits until the key lease is released.
type Priority int

const (
	PriKey Priority = iota
	PriCommand
	PriState
	PriMeter
)

// ErrBusy means the port is held by a keying lease. The caller should skip
// this poll. It must not retry in a tight loop.
var ErrBusy = errors.New("rig: port is held for keying")

// Port is one open serial port or one rigctld session. Exec writes one
// command and, when the command is a query, returns the radio's answer.
// Implementations must not open a second descriptor for the same name.
type Port interface {
	Exec(ctx context.Context, cmd string) (answer string, err error)
	// Lines sets DTR and RTS. On a CAT-only port both stay false.
	Lines(dtr, rts bool) error
	Close() error
}

// Owner is the only component allowed to open a named port.
type Owner interface {
	// Do runs fn while holding name at pri. fn must do one exchange, or a
	// short burst the caller already decided is one lease (an SWR sweep holds
	// PriKey for the whole sweep, not one point at a time).
	Do(ctx context.Context, name string, pri Priority, fn func(Port) error) error
}

// MemOwner is the in-memory Owner the serial implementation has to match.
// It opens nothing. Tests use it to lock the lease rules before any radio
// code moves onto them.
type MemOwner struct {
	mu   sync.Mutex
	cond *sync.Cond
	held map[string]Priority
	port map[string]*memPort
}

// NewMemOwner returns an Owner with no ports held.
func NewMemOwner() *MemOwner {
	o := &MemOwner{held: map[string]Priority{}, port: map[string]*memPort{}}
	o.cond = sync.NewCond(&o.mu)
	return o
}

// Do implements Owner.
func (o *MemOwner) Do(ctx context.Context, name string, pri Priority, fn func(Port) error) error {
	if name == "" {
		return errors.New("rig: empty port name")
	}
	if fn == nil {
		return errors.New("rig: nil exchange")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stop := o.wakeOn(ctx)
	defer stop()

	o.mu.Lock()
	for {
		if err := ctx.Err(); err != nil {
			o.mu.Unlock()
			return err
		}
		h, busy := o.held[name]
		if !busy {
			break
		}
		// A state or meter poll must not sit behind a key lease. A queued
		// poll is how CAT bytes end up in the middle of KY on the FX-4CR.
		if h == PriKey && pri > PriCommand {
			o.mu.Unlock()
			return ErrBusy
		}
		o.cond.Wait()
	}
	o.held[name] = pri
	p := o.port[name]
	if p == nil {
		p = &memPort{}
		o.port[name] = p
	}
	o.mu.Unlock()

	err := fn(p)

	o.mu.Lock()
	delete(o.held, name)
	o.cond.Broadcast()
	o.mu.Unlock()
	return err
}

// wakeOn broadcasts the condition when ctx is cancelled, so a waiter blocked
// in Do returns. The returned function stops that watcher.
func (o *MemOwner) wakeOn(ctx context.Context) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			o.mu.Lock()
			o.cond.Broadcast()
			o.mu.Unlock()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

// memPort records nothing and touches no device. The lease is the behavior
// under test; the serial Port comes in phase 2.
type memPort struct{}

func (memPort) Exec(context.Context, string) (string, error) { return "", nil }
func (memPort) Lines(bool, bool) error                       { return nil }
func (memPort) Close() error                                 { return nil }
