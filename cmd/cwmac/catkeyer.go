package main

// CW over CAT, for radios with no USB key line (the FX-4CR's RTS is its PTT): the text
// goes to the radio's own keyer with the Kenwood TS-590 command KY ("converts the entered
// characters into morse code while keying", up to 24 a command) at the speed set with KS
// (004-060 WPM). The radio does the timing, so the sender here only predicts it, for the
// echo, the TX lamp and the receive mute.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/rig"
)

// cwSender: what the sender (tx.go) drives: the box's own keyer (cw.Keyer, timing a key
// line) or the radio's (catKeyer).
type cwSender interface {
	SetWPM(int)
	Wants() bool
	Feed(rune) bool
	Sent() <-chan rune
	Errors() <-chan error
	Busy() bool
	Active(time.Duration) bool
	Abort()
	Close()
}

var _ cwSender = (*cw.Keyer)(nil)

const (
	kyChunk     = 20                     // characters a KY command carries (the radio takes up to 24)
	kyIdle      = 250 * time.Millisecond // send a partial word after this long with no typing
	kyAhead     = 3 * time.Second        // how much keying to hand the radio ahead of time
	kyTick      = 20 * time.Millisecond
	kyFirstWait = 150 * time.Millisecond // the radio's keyer starts a moment after the command
)

type kyEvent struct {
	ch rune
	at time.Time
}

type catKeyer struct {
	mu      sync.Mutex
	conn    *catConn
	rel     func() // PriKey held while KY is outstanding; nil when the port is free for polls
	wpm     int
	sentWPM int
	staged  []rune
	lastIn  time.Time
	endAt   time.Time // predicted end of the last character handed to the radio
	events  []kyEvent // characters to echo when the radio starts them
	sent    chan rune
	errs    chan error
	quit    chan struct{}
	done    chan struct{}
}

// newCATKeyer opens the radio's CAT port and checks that CAT answers.
func newCATKeyer(cs catSpec, wpm int) (*catKeyer, error) {
	if !cs.Kenwood {
		return nil, errors.New("CW over CAT needs a Kenwood-type radio (Setup > Radio > CAT)")
	}
	if cs.Port == "" {
		return nil, errors.New("pick the radio's key port first (Setup > Radio > Key port)")
	}
	c, err := openCAT(cs.Port, cs.Baud)
	if err != nil {
		return nil, err
	}
	// CAT alive? (a frequency read; the FX-4CR doesn't answer the KY; buffer query)
	a, err := c.ask("FA;", 600*time.Millisecond)
	if err != nil {
		c.Close()
		return nil, err
	}
	if !strings.HasPrefix(a, "FA") {
		c.Close()
		return nil, fmt.Errorf("the radio didn't answer CAT (check Setup > Radio > CAT is Kenwood, and its port): answer %q", a)
	}
	k := &catKeyer{conn: c, sent: make(chan rune, 512), errs: make(chan error, 4),
		quit: make(chan struct{}), done: make(chan struct{})}
	k.SetWPM(wpm)
	go k.run()
	return k, nil
}

func (k *catKeyer) SetWPM(w int) {
	k.mu.Lock()
	k.wpm = max(5, min(50, w))
	k.mu.Unlock()
}

// Wants: room to stage more, and the radio isn't already holding kyAhead of keying.
func (k *catKeyer) Wants() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.staged) < kyChunk && time.Until(k.endAt) < kyAhead
}

func (k *catKeyer) Feed(ch rune) bool {
	if ch == '\n' {
		ch = ' '
	}
	if ch == ';' {
		return true // can't be sent over CAT (it ends a command)
	}
	if _, ok := cw.MorseCode(ch); !ok && ch != ' ' {
		return true // skipped, as the box's own keyer does
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if ch == ' ' && (len(k.staged) == 0 || k.staged[len(k.staged)-1] == ' ') && time.Now().After(k.endAt) {
		return true // nothing to separate
	}
	k.staged = append(k.staged, []rune(strings.ToUpper(string(ch)))...)
	k.lastIn = time.Now()
	return true
}

func (k *catKeyer) Sent() <-chan rune    { return k.sent }
func (k *catKeyer) Errors() <-chan error { return k.errs }

func (k *catKeyer) Busy() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return time.Now().Before(k.endAt) || len(k.staged) > 0
}

func (k *catKeyer) Active(hang time.Duration) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return time.Now().Before(k.endAt.Add(hang))
}

// Abort: drop what's staged and stop the radio's keyer (KY0; clears the TS-590's buffer;
// RX; makes sure it's back on receive).
func (k *catKeyer) Abort() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.staged, k.events = nil, nil
	k.unkeyLocked()
	k.endAt = time.Time{}
}

// unkeyLocked clears the radio's keyer and returns it to receive. It does not
// care whether we thought we were keyed: a missed endAt must still unkey.
func (k *catKeyer) unkeyLocked() {
	if k.conn == nil || k.conn.sp == nil {
		return
	}
	_, _ = k.conn.sp.transact([]string{"KY0;", "RX;"})
}

func (k *catKeyer) Close() {
	select {
	case <-k.quit:
	default:
		close(k.quit)
	}
	<-k.done
	k.Abort()
	k.dropHold()
	if k.conn != nil {
		k.conn.Close()
		k.conn = nil
	}
}

// ensureHold takes the key lease for as long as KY is outstanding. Polls on
// this port get rig.ErrBusy until dropHold.
func (k *catKeyer) ensureHold() error {
	k.mu.Lock()
	if k.rel != nil || k.conn == nil || k.conn.sp == nil {
		k.mu.Unlock()
		return nil
	}
	sp := k.conn.sp
	bus := k.conn.bus
	path, baud := sp.path, sp.baud
	k.mu.Unlock()
	_, rel, err := bus.holdLease(path, baud, rig.PriKey)
	if err != nil {
		return err
	}
	k.mu.Lock()
	if k.rel == nil {
		k.rel = rel
		k.mu.Unlock()
		return nil
	}
	k.mu.Unlock()
	rel() // lost the race with another ensureHold
	return nil
}

func (k *catKeyer) dropHold() {
	k.mu.Lock()
	rel := k.rel
	k.rel = nil
	k.mu.Unlock()
	if rel != nil {
		rel()
	}
}

func (k *catKeyer) run() {
	defer close(k.done)
	t := time.NewTicker(kyTick)
	defer t.Stop()
	for {
		select {
		case <-k.quit:
			return
		case <-t.C:
		}
		k.mu.Lock()
		now := time.Now()
		for len(k.events) > 0 && !k.events[0].at.After(now) {
			select {
			case k.sent <- k.events[0].ch:
			default:
			}
			k.events = k.events[1:]
		}
		n := len(k.staged)
		flush := n > 0 && (k.staged[n-1] == ' ' || n >= kyChunk || now.Sub(k.lastIn) > kyIdle)
		var chunk string
		if flush {
			buf := k.staged
			if len(buf) > kyChunk {
				buf = buf[:kyChunk]
			}
			chunk = string(buf)
			k.staged = append([]rune(nil), k.staged[len(buf):]...)
		}
		busy := flush || len(k.staged) > 0 || now.Before(k.endAt)
		idle := !now.Before(k.endAt) && len(k.staged) == 0 && !flush
		k.mu.Unlock()
		if busy {
			if err := k.ensureHold(); err != nil {
				if chunk != "" {
					k.mu.Lock()
					k.staged = append([]rune(chunk), k.staged...)
					k.mu.Unlock()
				}
				select {
				case k.errs <- err:
				default:
				}
				continue
			}
		}
		if chunk != "" {
			k.mu.Lock()
			err := k.sendLocked(chunk, time.Now())
			k.mu.Unlock()
			if err != nil {
				select {
				case k.errs <- err:
				default:
				}
			}
		}
		if idle {
			k.dropHold()
		}
	}
}

func (k *catKeyer) sendLocked(chunk string, now time.Time) error {
	if k.conn == nil || k.conn.sp == nil {
		return errPortClosed
	}
	if k.wpm != k.sentWPM {
		if _, err := k.conn.sp.writeRead(fmt.Sprintf("KS%03d;", k.wpm), 0); err != nil {
			return err
		}
		k.sentWPM = k.wpm
	}
	// a radio without CW over CAT says "?;" (unknown command); a set gets no answer otherwise
	if _, err := k.conn.sp.writeRead("KY "+chunk+";", 60*time.Millisecond); err != nil {
		if strings.Contains(err.Error(), "rejected") {
			k.events, k.endAt = nil, time.Time{}
			return errors.New("the radio refused the CW command KY: it doesn't do CW over CAT (use the key jack)")
		}
		if !strings.Contains(err.Error(), "no answer") {
			return err
		}
	}
	// predict when each character keys (PARIS: dit = 1.2/WPM s; 3 dits between
	// characters, 7 between words)
	dit := time.Duration(float64(1200*time.Millisecond) / float64(k.wpm))
	t := k.endAt
	if !t.After(now) {
		t = now.Add(kyFirstWait)
	} else {
		t = t.Add(3 * dit)
	}
	for _, ch := range chunk {
		if ch == ' ' {
			t = t.Add(4 * dit)
			k.events = append(k.events, kyEvent{ch, t})
			continue
		}
		code, _ := cw.MorseCode(ch)
		k.events = append(k.events, kyEvent{ch, t})
		for i, el := range code {
			if i > 0 {
				t = t.Add(dit)
			}
			if el == '-' {
				t = t.Add(3 * dit)
			} else {
				t = t.Add(dit)
			}
		}
		t = t.Add(3 * dit)
	}
	k.endAt = t.Add(-3 * dit)
	return nil
}
