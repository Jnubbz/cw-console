//go:build linux || darwin

package main

// One open file descriptor per serial port. CAT bytes and DTR/RTS on that
// port take the same mutex, and a key lease (rig.PriKey) makes meter and
// state polls return rig.ErrBusy instead of writing into a KY command.
// Opening drops DTR and RTS before anything else. Closing, or any path that
// gives the port up, drops them again. A failure leaves the radio unkeyed.

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Jnubbz/cw-console/rig"
)

var radioBus = newPortBus()

var errPortClosed = errors.New("serial port is closed")

type portBus struct {
	mu   sync.Mutex
	cond *sync.Cond
	held map[string]rig.Priority
	live map[string]*sharedPort
}

func newPortBus() *portBus {
	b := &portBus{held: map[string]rig.Priority{}, live: map[string]*sharedPort{}}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func portKey(path string) string {
	if path == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// retain opens path once. A second retain of the same device gets the same
// descriptor. DTR and RTS are dropped on the first open, before termios.
func (b *portBus) retain(path string, baud int) (*sharedPort, error) {
	if path == "" {
		return nil, errors.New("empty serial port")
	}
	key := portKey(path)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.live == nil {
		b.live = map[string]*sharedPort{}
	}
	if p := b.live[key]; p != nil && !p.dead() {
		p.refs++
		if baud > 0 {
			if err := p.configure(baud); err != nil {
				p.refs--
				return nil, err
			}
		}
		return p, nil
	}
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open %s: %v", path, err)
	}
	// Before termios, and before anything else can key.
	ioctlInt(fd, tiocmBIC, tiocmDTR|tiocmRTS)
	p := &sharedPort{fd: fd, key: key, path: path}
	if baud > 0 {
		if err := catRaw(fd, baud); err != nil {
			syscall.Close(fd)
			return nil, fmt.Errorf("CAT port settings: %v", err)
		}
		p.baud = baud
		// Stay non-blocking so a missing answer can't hold the port mutex
		// (and therefore the key line) past the read deadline.
		catFlush(fd)
	}
	p.refs = 1
	b.live[key] = p
	return p, nil
}

func (b *portBus) release(p *sharedPort) {
	if p == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p.refs--
	if p.refs > 0 {
		return
	}
	p.shutdown()
	delete(b.live, p.key)
}

// acquire runs fn while this port is held at pri. Meter and state polls do
// not wait behind a key lease.
func (b *portBus) acquire(key string, pri rig.Priority, fn func() error) error {
	if key == "" {
		return errors.New("empty serial port")
	}
	if fn == nil {
		return errors.New("nil exchange")
	}
	b.mu.Lock()
	for {
		_, busy := b.held[key]
		if !busy {
			break
		}
		// Polls never queue. A queued meter is how CAT bytes land in the
		// middle of KY, and a poll waiting out a sweep would hold the rig
		// lock for the whole sweep. Operator commands and key leases wait.
		if pri > rig.PriCommand {
			b.mu.Unlock()
			return rig.ErrBusy
		}
		b.cond.Wait()
	}
	b.held[key] = pri
	b.mu.Unlock()

	err := fn()

	b.mu.Lock()
	delete(b.held, key)
	b.cond.Broadcast()
	b.mu.Unlock()
	return err
}

// holdLease keeps pri until release. The port stays open across the lease.
// release is idempotent and drops the lease before it drops the reference.
func (b *portBus) holdLease(path string, baud int, pri rig.Priority) (sp *sharedPort, release func(), err error) {
	sp, err = b.retain(path, baud)
	if err != nil {
		return nil, nil, err
	}
	ready := make(chan error, 1)
	unlock := make(chan struct{})
	done := make(chan struct{})
	go func() {
		err := b.acquire(sp.key, pri, func() error {
			ready <- nil
			<-unlock
			return nil
		})
		if err != nil {
			select {
			case ready <- err:
			default:
			}
		}
		close(done)
	}()
	if err := <-ready; err != nil {
		b.release(sp)
		return nil, nil, err
	}
	var once sync.Once
	return sp, func() {
		once.Do(func() {
			close(unlock)
			<-done
			b.release(sp)
		})
	}, nil
}

// exchange writes cmds under pri. A meter poll during a key lease returns
// rig.ErrBusy and writes nothing.
func (b *portBus) exchange(path string, baud int, pri rig.Priority, cmds []string) ([]string, error) {
	return b.exchangeOpt(path, baud, pri, cmds, false)
}

// exchangeLenient: like exchange, but a query the rig rejects ("?;") answers "?;" instead
// of ending the batch (reading settings a firmware may not have).
func (b *portBus) exchangeLenient(path string, baud int, pri rig.Priority, cmds []string) ([]string, error) {
	return b.exchangeOpt(path, baud, pri, cmds, true)
}

func (b *portBus) exchangeOpt(path string, baud int, pri rig.Priority, cmds []string, lenient bool) ([]string, error) {
	sp, err := b.retain(path, baud)
	if err != nil {
		return nil, err
	}
	defer b.release(sp)
	var out []string
	err = b.acquire(sp.key, pri, func() error {
		var e error
		out, e = sp.transactOpt(cmds, lenient)
		return e
	})
	return out, err
}

var errRejected = errors.New("the rig rejected the command")

type sharedPort struct {
	mu     sync.Mutex
	fd     int
	key    string
	path   string
	baud   int
	refs   int
	closed bool
	dtr    bool
	rts    bool
	skipIO bool // tests: don't touch a real descriptor
}

func (p *sharedPort) dead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *sharedPort) configure(baud int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.fd < 0 {
		return errPortClosed
	}
	if baud == p.baud {
		return nil
	}
	if err := catRaw(p.fd, baud); err != nil {
		return err
	}
	p.baud = baud
	return nil
}

// transact writes each command and reads a reply when the command is a query.
// The whole list is one critical section with Key and Close.
func (p *sharedPort) transact(cmds []string) ([]string, error) { return p.transactOpt(cmds, false) }

func (p *sharedPort) transactOpt(cmds []string, lenient bool) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.fd < 0 {
		return nil, errPortClosed
	}
	var out []string
	for _, cmd := range cmds {
		if err := p.writeLocked(cmd); err != nil {
			return out, err
		}
		if isCATQuery(cmd) {
			a, err := p.readLocked(600 * time.Millisecond)
			if err != nil && lenient && errors.Is(err, errRejected) {
				a, err = "?;", nil
			}
			if err != nil {
				return out, err
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// writeRead is one command and an optional short read (KY's reject check).
func (p *sharedPort) writeRead(cmd string, wait time.Duration) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.fd < 0 {
		return "", errPortClosed
	}
	if err := p.writeLocked(cmd); err != nil {
		return "", err
	}
	if wait <= 0 {
		return "", nil
	}
	return p.readLocked(wait)
}

func (p *sharedPort) writeLocked(s string) error {
	_, err := syscall.Write(p.fd, []byte(s))
	time.Sleep(30 * time.Millisecond)
	return err
}

func (p *sharedPort) readLocked(d time.Duration) (string, error) {
	var b []byte
	buf := make([]byte, 64)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		n, err := syscall.Read(p.fd, buf)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EAGAIN) {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", errPortClosed
		}
		b = append(b, buf[:n]...)
		for i, ch := range b {
			if ch == ';' {
				if string(b[:i+1]) == "?;" {
					return "", errRejected
				}
				return string(b[:i+1]), nil
			}
		}
	}
	return "", errors.New("no answer")
}

// setLine turns one modem line on or off. The other line is left as it is.
// Closed ports refuse to key.
func (p *sharedPort) setLine(rts bool, on bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || (!p.skipIO && p.fd < 0) {
		return errPortClosed
	}
	bit := int32(tiocmDTR)
	if rts {
		bit = tiocmRTS
	}
	req := uintptr(tiocmBIC)
	if on {
		req = tiocmBIS
	}
	if !p.skipIO && p.fd >= 0 {
		if err := ioctlInt(p.fd, req, bit); err != nil {
			return fmt.Errorf("%s: keying failed: %v", p.path, err)
		}
	}
	if rts {
		p.rts = on
	} else {
		p.dtr = on
	}
	return nil
}

// shutdown drops both key lines, then closes. Safe to call once.
func (p *sharedPort) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unkeyLocked()
	if !p.skipIO && p.fd >= 0 {
		syscall.Close(p.fd)
		p.fd = -1
	}
	p.closed = true
}

func (p *sharedPort) unkeyLocked() {
	if p.fd >= 0 {
		ioctlInt(p.fd, tiocmBIC, tiocmDTR|tiocmRTS)
	}
	p.dtr, p.rts = false, false
}

type busLine struct {
	sp  *sharedPort
	bus *portBus
	rts bool
}

func (l *busLine) Key(down bool) error {
	if l == nil || l.sp == nil {
		return errPortClosed
	}
	return l.sp.setLine(l.rts, down)
}

func (l *busLine) Close() {
	if l == nil || l.sp == nil {
		return
	}
	// Unkey even when another user still holds the descriptor.
	_ = l.sp.setLine(l.rts, false)
	l.sp.mu.Lock()
	if l.rts {
		l.sp.rts = false
	} else {
		l.sp.dtr = false
	}
	// If this was the last key line, drop both. shutdown checks refs via the bus.
	l.sp.mu.Unlock()
	l.bus.release(l.sp)
	l.sp = nil
}
