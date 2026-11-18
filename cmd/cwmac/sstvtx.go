package main

// SSTV transmit keys only through the key line the rest of the program already
// uses (openPTT: one serial port, DTR/RTS or CAT TX1/TX0). A failure and the
// 5 s page-disconnect dead-man both drop that line. There is no second key path.

import (
	"errors"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/keying"
)

// sstvLine is the existing key line. Key(false) must leave the radio unkeyed.
type sstvLine interface {
	Key(on bool) error
}

// sstvTx is the key lease for one SSTV picture. now and the idle clock are
// replaceable so tests can fire the dead-man without waiting.
type sstvTx struct {
	mu     sync.Mutex
	line   sstvLine
	watch  keying.Watch
	now    func() time.Time
	keyed  bool
	since  time.Time
	until  time.Time
	err    string
	retry  bool
	unkeys int
}

func (t *sstvTx) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *sstvTx) keyedNow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keyed
}

// start keys the radio for dur. If keying fails, it drops the line before returning.
func (t *sstvTx) start(line sstvLine, dur time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.keyed {
		return errors.New("already transmitting")
	}
	if line == nil {
		return errors.New("no key line")
	}
	if dur <= 0 {
		return errors.New("nothing to send")
	}
	if err := line.Key(true); err != nil {
		_ = line.Key(false)
		t.keyed = false
		t.line = nil
		return err
	}
	t.line = line
	t.keyed = true
	t.retry = false
	t.err = ""
	t.since = t.clock()
	t.until = t.since.Add(dur)
	return nil
}

// tick unkeys when the picture's time is up or the page has been gone for 5 s.
func (t *sstvTx) tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.deadmanLocked() {
		return
	}
	if t.keyed && !t.clock().Before(t.until) {
		t.unkeyLocked("")
	}
}

func (t *sstvTx) deadmanLocked() bool {
	return t.watch.Tick(t.keyed, func() {
		t.unkeyLocked("the page was closed")
	})
}

// stop unkeys because the operator asked.
func (t *sstvTx) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unkeyLocked("stopped")
}

// fail unkeys because sending the audio failed. The radio must not stay keyed.
func (t *sstvTx) fail(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.err = err.Error()
	}
	t.unkeyLocked(t.err)
}

// unkeyLocked drops the key line. A failed drop is retried by retryUnkey.
func (t *sstvTx) unkeyLocked(why string) {
	if !t.keyed && !t.retry {
		return
	}
	t.unkeys++
	if t.line != nil {
		if err := t.line.Key(false); err != nil {
			t.keyed = false
			t.retry = true
			t.err = "couldn't unkey: " + err.Error()
			return
		}
	}
	t.keyed = false
	t.retry = false
	if why != "" {
		t.err = why
	}
}

// retryUnkey tries again after a failed unkey, until the line accepts it.
func (t *sstvTx) retryUnkey() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.retry || t.line == nil {
		return
	}
	if err := t.line.Key(false); err != nil {
		t.err = "couldn't unkey: " + err.Error()
		return
	}
	t.retry = false
	t.err = ""
}
