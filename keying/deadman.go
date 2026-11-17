// Package keying is the shared transmit-enable policy for every mode.
//
// CW already stops sending when the page has been gone for 5 seconds
// (cmd/cwmac/tx.go, deadManAfter, applied in txEngine.tickLocked). FT8 and
// Tune do not consult that clock (cmd/cwmac/ft8tx.go), and an SWR sweep only
// stops when its own context is cancelled. This package is that rule, in a
// form every transmitter can call. It does not key a radio. cmd/cwmac is not
// wired to it in this change, so transmit behavior is unchanged.
package keying

import "time"

// DisconnectAfter is how long the controlling page may be gone before a
// transmission is stopped. It matches the CW dead-man.
const DisconnectAfter = 5 * time.Second

// Watch stops a transmission when the page that started it has disconnected.
type Watch struct {
	// After defaults to DisconnectAfter when zero.
	After time.Duration
	// Idle is how long no page has been connected. Zero while one is connected.
	// cmd/cwmac's hub.idleFor is the function CW already uses.
	Idle func() time.Duration
}

// Tick aborts when a transmission is in progress and the page has been gone
// longer than After. busy is true while CW is queued or keyed, while an FT8
// slot or Tune tone is on, while SSTV is transmitting, or while an SWR sweep
// is keyed. Returns whether it called abort.
func (w Watch) Tick(busy bool, abort func()) bool {
	if !busy || w.Idle == nil || abort == nil {
		return false
	}
	after := w.After
	if after <= 0 {
		after = DisconnectAfter
	}
	if w.Idle() > after {
		abort()
		return true
	}
	return false
}
