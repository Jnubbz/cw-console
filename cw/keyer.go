package cw

// Keyer: turns typed characters into precisely timed key-down/key-up on a KeyLine
// (a serial port's DTR or RTS on Windows). The UI feeds one character at a time when
// the keyer asks for it, which gives true type-ahead: you can keep typing, and
// anything not yet taken can still be edited. PARIS timing as in Nexus's
// morse_key_events: dit = 1.2/WPM s, dah = 3, gaps 1 / 3 / 7 dits.

import (
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// KeyLine is whatever physically keys the transmitter.
type KeyLine interface {
	Key(down bool) error
}

// Keyer runs its own goroutine; all methods are safe to call from any goroutine.
type Keyer struct {
	line  KeyLine
	wpm   atomic.Int32
	feed  chan rune
	abort chan struct{}
	quit  chan struct{}
	done  chan struct{}
	sent  chan rune // characters as they START keying (buffered; UI drains)
	errs  chan error

	wants     atomic.Bool
	keyDown   atomic.Bool
	lastUpNs  atomic.Int64
	busy      atomic.Bool // a character is being keyed
	OnStart   func()      // called on the keyer goroutine before it runs (thread priority)
	SpinFinal time.Duration
}

// NewKeyer starts a keyer on line at wpm.
func NewKeyer(line KeyLine, wpm int, onStart func()) *Keyer {
	k := &Keyer{
		line: line, feed: make(chan rune, 1), abort: make(chan struct{}, 1),
		quit: make(chan struct{}), done: make(chan struct{}),
		sent: make(chan rune, 512), errs: make(chan error, 4),
		OnStart: onStart, SpinFinal: 1500 * time.Microsecond,
	}
	k.SetWPM(wpm)
	go k.run()
	return k
}

// SetWPM changes speed (takes effect from the next character). Clamped to 5..60.
func (k *Keyer) SetWPM(w int) {
	if w < 5 {
		w = 5
	}
	if w > 60 {
		w = 60
	}
	k.wpm.Store(int32(w))
}

func (k *Keyer) WPM() int { return int(k.wpm.Load()) }

// Wants reports that the keyer is ready for its next character.
func (k *Keyer) Wants() bool { return k.wants.Load() && len(k.feed) == 0 }

// Feed hands the keyer its next character (call only when Wants() is true).
func (k *Keyer) Feed(ch rune) bool {
	select {
	case k.feed <- ch:
		k.wants.Store(false)
		return true
	default:
		return false
	}
}

// Sent returns characters as they begin keying (for the UI's echo).
func (k *Keyer) Sent() <-chan rune { return k.sent }

// Errors reports line failures (the keyer keys up and keeps running).
func (k *Keyer) Errors() <-chan error { return k.errs }

// Busy is true while a character is being keyed.
func (k *Keyer) Busy() bool { return k.busy.Load() }

// Active is true while keying and for `hang` after the last key-up (decoder muting).
func (k *Keyer) Active(hang time.Duration) bool {
	if k.keyDown.Load() || k.busy.Load() {
		return true
	}
	last := k.lastUpNs.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < hang
}

// Abort keys up immediately and drops the character in progress and any fed one.
func (k *Keyer) Abort() {
	select {
	case k.abort <- struct{}{}:
	default:
	}
}

// Close stops the keyer goroutine with the key up.
func (k *Keyer) Close() {
	select {
	case <-k.quit:
	default:
		close(k.quit)
	}
	<-k.done
}

func (k *Keyer) setKey(down bool) {
	if err := k.line.Key(down); err != nil {
		select {
		case k.errs <- err:
		default:
		}
	}
	k.keyDown.Store(down)
	if !down {
		k.lastUpNs.Store(time.Now().UnixNano())
	}
}

// waitUntil sleeps until t (coarse sleep, then a short spin for precision). It returns
// false if aborted or closed, with the key already up.
func (k *Keyer) waitUntil(t time.Time) bool {
	for {
		d := time.Until(t)
		if d <= 0 {
			return true
		}
		if d > k.SpinFinal {
			timer := time.NewTimer(d - k.SpinFinal)
			select {
			case <-k.abort:
				timer.Stop()
				return false
			case <-k.quit:
				timer.Stop()
				return false
			case <-timer.C:
			}
			continue
		}
		select { // final stretch: spin, still responsive to abort
		case <-k.abort:
			return false
		case <-k.quit:
			return false
		default:
		}
	}
}

func (k *Keyer) run() {
	runtime.LockOSThread()
	if k.OnStart != nil {
		k.OnStart()
	}
	defer close(k.done)
	defer k.setKey(false)
	var nextStart time.Time // earliest start of the next character (gap after the last one)
	for {
		k.wants.Store(true)
		var ch rune
		select {
		case ch = <-k.feed:
		case <-k.abort:
			continue
		case <-k.quit:
			return
		}
		k.wants.Store(false)
		dit := time.Duration(float64(1200*time.Millisecond) / float64(k.wpm.Load()))
		if ch == ' ' || ch == '\n' {
			// Word gap: 7 dits from the end of the last character (3 already counted).
			if !nextStart.IsZero() {
				nextStart = nextStart.Add(4 * dit)
			}
			k.emit(ch)
			continue
		}
		code, ok := MorseCode(ch)
		if !ok {
			continue // unsupported characters are skipped, like Nexus
		}
		t := time.Now()
		if nextStart.After(t) {
			t = nextStart
		}
		k.busy.Store(true)
		aborted := false
		if !k.waitUntil(t) {
			aborted = true
		}
		if !aborted {
			k.emit(ch)
		}
		for i, el := range code {
			if aborted {
				break
			}
			if i > 0 {
				t = t.Add(dit) // intra-character gap
				if !k.waitUntil(t) {
					aborted = true
					break
				}
			}
			k.setKey(true)
			n := time.Duration(1)
			if el == '-' {
				n = 3
			}
			t = t.Add(n * dit)
			if !k.waitUntil(t) {
				k.setKey(false)
				aborted = true
				break
			}
			k.setKey(false)
		}
		k.busy.Store(false)
		if aborted {
			nextStart = time.Time{}
			select { // drop anything already fed
			case <-k.feed:
			default:
			}
			select {
			case <-k.quit:
				return
			default:
			}
			continue
		}
		nextStart = t.Add(3 * dit) // inter-character gap
	}
}

func (k *Keyer) emit(ch rune) {
	select {
	case k.sent <- ch:
	default:
	}
}

// MacroContext holds the values macros can insert.
type MacroContext struct {
	MyCall, MyName, MyState, MyGrid, HisCall, RST string
	Extra                                         map[string]string // any other KEY = value from the macros file
}

// Expand fills a macro template (port of Nexus cw::expand, plus {CALL} as an alias for
// the worked call). Tokens: {MYCALL} {NAME} {MYSTATE} {MYGRID} {RST} (9->N, 0->T),
// {CALL} or ! (the worked call), and {KEY} for any extra KEY set in the macros file.
// Unknown tokens are left visible; runs of spaces collapse.
func Expand(tmpl string, c MacroContext) string {
	r := strings.NewReplacer(
		"{MYCALL}", c.MyCall, "{NAME}", c.MyName, "{MYSTATE}", c.MyState,
		"{MYGRID}", c.MyGrid, "{RST}", CutNumbers(c.RST), "{CALL}", c.HisCall, "!", c.HisCall)
	out := r.Replace(tmpl)
	for k, v := range c.Extra {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return strings.Join(strings.Fields(out), " ")
}

// CutNumbers: 9->N, 0->T (casual cut numbers, as Nexus).
func CutNumbers(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '9':
			return 'N'
		case '0':
			return 'T'
		}
		return r
	}, s)
}
