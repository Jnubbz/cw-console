package ops

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Config is what the operator sets in the window.
type Config struct {
	CQMacro    int     `json:"cqMacro"`    // 0..7, the macro Auto-CQ sends
	ListenSecs float64 `json:"listenSecs"` // listen this long between CQs
	AutoReply  bool    `json:"autoReply"`  // answer a single clean caller automatically
	ReplyMacro int     `json:"replyMacro"` // 0..7, the macro the auto-reply sends
	MatchSpeed bool    `json:"matchSpeed"` // send at the other station's speed
	MatchMin   int     `json:"matchMin"`   // ...but never slower than this
	MatchMax   int     `json:"matchMax"`   // ...or faster than this
}

// DefaultConfig: F1 CQ, 6 s listening, reply with F3, match speed off (12-30 WPM).
func DefaultConfig() Config {
	return Config{CQMacro: 0, ListenSecs: 6, ReplyMacro: 2, MatchMin: 12, MatchMax: 30}
}

// Clamp keeps a config sane (settings files and pages can send anything).
func (c *Config) Clamp() {
	clampI := func(v *int, lo, hi int) { *v = max(lo, min(hi, *v)) }
	clampI(&c.CQMacro, 0, 7)
	clampI(&c.ReplyMacro, 0, 7)
	clampI(&c.MatchMin, 5, 50)
	clampI(&c.MatchMax, 5, 50)
	if c.MatchMax < c.MatchMin {
		c.MatchMax = c.MatchMin
	}
	if !(c.ListenSecs >= 2) { // also catches NaN
		c.ListenSecs = 2
	}
	if c.ListenSecs > 60 {
		c.ListenSecs = 60
	}
}

// Inputs are sampled by the app on every tick.
type Inputs struct {
	Now    time.Time
	TxBusy bool          // we're keying or have text waiting to go
	TxOn   bool          // transmit is enabled
	Signal bool          // the classic decoder's key is down (someone is sending)
	RxWPM  float64       // the classic decoder's speed estimate
	Lag    time.Duration // how far decoded text trails the audio (AI: a few seconds)
	MyCall string
}

// ActionKind says what the app should do.
type ActionKind int

const (
	SendMacro ActionKind = iota // send macro Macro (with Call in the Call box)
	SetCall                     // put Call in the Call box
	Note                        // show Text in the status line
	AutoCQOff                   // untick Auto-CQ in the window
)

type Action struct {
	Kind  ActionKind
	Macro int
	Call  string
	Text  string
}

type phase int

const (
	phOff       phase = iota
	phCalling         // our CQ is queued or going out
	phListening       // waiting for an answer
	phHeard           // someone is answering; waiting for them to finish
	phPaused          // operator's turn
)

// Assist runs Auto-CQ / auto-reply and keeps the captured calls, RST and the other
// station's speed. Not safe for concurrent use: the apps call it from one thread
// (Windows UI thread) or under their engine mutex (Mac).
type Assist struct {
	Cfg Config

	armed       bool // Auto-CQ ticked
	ph          phase
	since       time.Time // entered the current phase
	listenStart time.Time
	lastSignal  time.Time
	lastRx      time.Time
	lastTx      time.Time
	heard       strings.Builder // decoded text since we stopped calling

	partial  string    // last decoded word, not finished yet
	calls    []string  // recently heard calls, newest first
	rxQSO    string    // decoded text since the last logged contact (for the RST)
	qsoCall  string    // call the current contact started with
	qsoStart time.Time // when that call was first used

	theirWPM float64
	theirAt  time.Time
	myCall   string
	lag      time.Duration
}

func NewAssist() *Assist { return &Assist{Cfg: DefaultConfig()} }

// ---- what the window shows ----

// Calls returns recently heard callsigns, newest first.
func (a *Assist) Calls() []string { return append([]string(nil), a.calls...) }

// Armed reports whether Auto-CQ is ticked.
func (a *Assist) Armed() bool { return a.armed }

// RSTRcvd is the last report the other station sent us this contact ("" if none).
func (a *Assist) RSTRcvd() string { return FindRST(a.rxQSO) }

// RxText: what's been decoded since the last logged contact (contests read the zone from it).
func (a *Assist) RxText() string { return a.rxQSO }

// TheirWPM is the other station's measured speed, or 0 if nobody was heard recently.
func (a *Assist) TheirWPM(now time.Time) int {
	if a.theirWPM < 5 || now.Sub(a.theirAt) > 90*time.Second {
		return 0
	}
	return int(math.Round(a.theirWPM))
}

// SendWPM is the speed to key at: the user's setting, or the other station's speed
// (within the user's limits) when Match speed is on and someone was heard recently.
func (a *Assist) SendWPM(user int, now time.Time) int {
	if !a.Cfg.MatchSpeed {
		return user
	}
	w := a.TheirWPM(now)
	if w == 0 {
		return user
	}
	return max(a.Cfg.MatchMin, min(a.Cfg.MatchMax, w))
}

// Status is a short line about Auto-CQ for the window ("" when it's idle).
func (a *Assist) Status(now time.Time) string {
	switch a.ph {
	case phCalling:
		return "Auto-CQ: calling"
	case phListening:
		left := a.Cfg.ListenSecs + a.lag.Seconds() - now.Sub(a.listenStart).Seconds()
		if left < 0 {
			if !a.armed {
				return "Listening for an answer"
			}
			return "Auto-CQ: waiting for the frequency to clear"
		}
		if !a.armed {
			return "Listening for an answer"
		}
		return fmt.Sprintf("Auto-CQ: listening, next CQ in %.0f s", math.Ceil(left))
	case phHeard:
		return "Someone's answering..."
	case phPaused:
		if a.armed {
			return "Auto-CQ paused: your turn (resumes after you log the contact)"
		}
	}
	return ""
}

// ---- events from the app ----

// Rx takes newly decoded text (receive only, never our own transmit echo).
func (a *Assist) Rx(text string, now time.Time) {
	if text == "" {
		return
	}
	if !IsNoiseText(text) {
		a.lastRx = now
	}
	if a.ph == phListening || a.ph == phHeard {
		a.heard.WriteString(text)
	}
	a.rxQSO += text
	if len(a.rxQSO) > 4000 {
		a.rxQSO = a.rxQSO[len(a.rxQSO)-2000:]
	}
	s := a.partial + strings.ToUpper(text)
	cut := strings.LastIndexAny(s, " \n")
	if cut < 0 {
		a.partial = s
		return
	}
	a.capture(s[:cut])
	a.partial = s[cut+1:]
}

func (a *Assist) capture(text string) {
	for _, c := range FindCalls(text, a.myCall) {
		a.addCall(c)
	}
}

func (a *Assist) addCall(c string) {
	for i, o := range a.calls {
		if o == c {
			a.calls = append(a.calls[:i], a.calls[i+1:]...)
			break
		}
	}
	a.calls = append([]string{c}, a.calls...)
	if len(a.calls) > 8 {
		a.calls = a.calls[:8]
	}
}

// SetAutoCQ ticks or unticks Auto-CQ. Starting it doesn't call over a signal that's
// already on the frequency.
func (a *Assist) SetAutoCQ(on bool, now time.Time) {
	a.armed = on
	if !on {
		a.ph = phOff
		return
	}
	a.enter(phListening, now)
	a.listenStart = now.Add(-time.Duration(a.Cfg.ListenSecs * float64(time.Second)))
}

// Manual: the operator sent something themselves (typed, Enter, or an F-key). A CQ
// starts a listening period (so auto-reply can answer it); anything else is the
// operator taking over, which pauses Auto-CQ.
func (a *Assist) Manual(isCQ bool, now time.Time) {
	if isCQ {
		if a.armed || a.Cfg.AutoReply {
			a.enter(phCalling, now)
		}
		return
	}
	if a.ph != phOff {
		if a.armed {
			a.enter(phPaused, now)
		} else {
			a.ph = phOff
		}
	}
}

// Stop (Esc): stop everything, Auto-CQ included.
func (a *Assist) Stop() {
	a.armed = false
	a.ph = phOff
}

// UsedCall notes the call a contact is being made with (sent in a macro or put in the
// Call box by the auto-reply); the first use is the contact's start time.
func (a *Assist) UsedCall(call string, now time.Time) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" || call == a.qsoCall {
		return
	}
	a.qsoCall, a.qsoStart = call, now
}

// StartTime is when the contact with call began (or now, if we never saw it start).
func (a *Assist) StartTime(call string, now time.Time) time.Time {
	if strings.EqualFold(strings.TrimSpace(call), a.qsoCall) && !a.qsoStart.IsZero() {
		return a.qsoStart
	}
	return now
}

// Logged: a contact was logged. Auto-CQ (if ticked) resumes once our sending ends.
func (a *Assist) Logged(now time.Time) {
	a.rxQSO, a.qsoCall, a.qsoStart = "", "", time.Time{}
	if a.armed {
		a.enter(phCalling, now) // "calling" = wait for the TU to finish, then listen
	} else {
		a.ph = phOff
	}
}

func (a *Assist) enter(p phase, now time.Time) {
	a.ph, a.since = p, now
	if p == phListening {
		a.listenStart = now
		a.heard.Reset()
	}
}

// Tick runs the Auto-CQ / auto-reply logic. Call it every 25-100 ms.
func (a *Assist) Tick(in Inputs) []Action {
	now := in.Now
	a.myCall, a.lag = in.MyCall, in.Lag
	if in.TxBusy {
		a.lastTx = now
	} else if in.Signal {
		a.lastSignal = now
		if in.RxWPM >= 5 && in.RxWPM <= 60 {
			a.theirWPM, a.theirAt = in.RxWPM, now
		}
	}
	if a.partial != "" && now.Sub(a.lastRx) > 1500*time.Millisecond {
		a.capture(a.partial)
		a.partial = ""
	}
	if a.ph == phOff {
		return nil
	}
	if !in.TxOn {
		if a.armed {
			a.Stop()
			return []Action{{Kind: AutoCQOff}, {Kind: Note, Text: "Auto-CQ needs transmit enabled."}}
		}
		a.ph = phOff
		return nil
	}
	listen := time.Duration(a.Cfg.ListenSecs * float64(time.Second))
	// After the other station's last element, decoded text can trail by the decoder's lag.
	settle := in.Lag + 1200*time.Millisecond

	switch a.ph {
	case phCalling:
		if !in.TxBusy && now.Sub(a.since) > 250*time.Millisecond {
			a.enter(phListening, now)
		}

	case phListening:
		if in.TxBusy {
			a.listenStart = now
			return nil
		}
		if IsAnswer(a.heard.String()) {
			if a.Cfg.AutoReply {
				a.ph, a.since = phHeard, now
				return nil
			}
			if a.armed {
				a.enter(phPaused, now)
				return []Action{{Kind: Note, Text: "Someone's answering: Auto-CQ paused, your turn."}}
			}
			a.ph = phOff
			return nil
		}
		elapsed := now.Sub(a.listenStart)
		if elapsed < listen+in.Lag { // the AI's text for the end of the window is still coming
			return nil
		}
		// Someone was keying: wait for them to finish and the decoder to catch up,
		// but not forever (QRM or a carrier shouldn't stop Auto-CQ for good).
		busy := a.lastSignal.After(a.listenStart) && now.Sub(a.lastSignal) < settle
		if busy && elapsed < listen+8*time.Second+in.Lag {
			return nil
		}
		if !a.armed { // a manual CQ that nobody answered
			a.ph = phOff
			return nil
		}
		a.enter(phCalling, now)
		return []Action{{Kind: SendMacro, Macro: a.Cfg.CQMacro}}

	case phHeard:
		// The AI delivers text in bursts (every 1-4 s): wait longer than one gap.
		quietRx := max(1500*time.Millisecond, in.Lag-time.Second)
		quiet := now.Sub(a.lastSignal) >= settle && now.Sub(a.lastRx) >= quietRx
		if !quiet && now.Sub(a.since) < 25*time.Second {
			return nil
		}
		if !quiet {
			return a.pauseWith(now, "Long reply, not answering automatically: your turn.")
		}
		text := a.heard.String() + " "
		calls := distinctStations(FindCalls(text, in.MyCall))
		switch len(calls) {
		case 0:
			return a.pauseWith(now, "Heard a reply but no clear callsign: your turn.")
		case 1:
			c := calls[0]
			a.addCall(c)
			a.UsedCall(c, now)
			if a.armed {
				a.enter(phPaused, now)
			} else {
				a.ph = phOff
			}
			return []Action{{Kind: SetCall, Call: c}, {Kind: SendMacro, Macro: a.Cfg.ReplyMacro, Call: c},
				{Kind: Note, Text: "Auto-reply: answered " + c + "."}}
		default:
			return a.pauseWith(now, fmt.Sprintf("Heard %d calls (%s): click one.", len(calls), strings.Join(calls, ", ")))
		}

	case phPaused:
		// The contact fizzled out: nothing sent or heard for a while, so carry on calling.
		if now.Sub(a.since) > 30*time.Second && !in.TxBusy &&
			now.Sub(a.lastTx) > 20*time.Second && now.Sub(a.lastSignal) > 20*time.Second && now.Sub(a.lastRx) > 20*time.Second {
			a.enter(phListening, now)
			a.listenStart = now.Add(-listen)
			return []Action{{Kind: Note, Text: "Auto-CQ resumed (nothing heard for 20 s)."}}
		}
	}
	return nil
}

func (a *Assist) pauseWith(now time.Time, msg string) []Action {
	if a.armed {
		a.enter(phPaused, now)
	} else {
		a.ph = phOff
	}
	return []Action{{Kind: Note, Text: msg}}
}

// IsNoiseText: nothing but dit-only letters (E I S H 5) and T, which is what a decoder
// prints for static, plus spaces and "?". Real CW almost always has other letters.
func IsNoiseText(s string) bool {
	for _, r := range s {
		switch r {
		case 'E', 'I', 'S', 'H', '5', 'T', ' ', '\n', '?':
		default:
			return false
		}
	}
	return true
}

// courtesy words end a contact; on their own they aren't someone calling.
var courtesy = map[string]bool{
	"TU": true, "73": true, "72": true, "77": true, "GL": true, "R": true, "RR": true, "QSL": true,
	"FB": true, "TNX": true, "TKS": true, "GM": true, "GA": true, "GE": true, "GN": true, "CUL": true,
	"SK": true, "BK": true, "K": true, "KN": true, "DIT": true, "OM": true, "ES": true, "UR": true,
}

// IsAnswer reports whether decoded text looks like someone calling or answering: a word
// of at least three letters/digits that isn't static (E, EE, I, HH...) or a sign-off
// courtesy (TU, 73, GL, R...). A hunter's "73 E E" or "TU EE" doesn't count, and nor does
// a two-character decoding slip like "B3" for "73"; "K4ABC", "5NN" or "QRZ?" does.
func IsAnswer(s string) bool {
	for _, w := range strings.Fields(strings.ToUpper(s)) {
		w = strings.Trim(w, "?.,=+")
		if w == "" || IsNoiseText(w) || courtesy[w] || courtesy[strings.TrimRight(w, "EI")] {
			continue // ("TUEE", "73EE": the E E run into the word)
		}
		n := 0
		for _, r := range w {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				n++
			}
		}
		if n >= 3 {
			return true
		}
	}
	return false
}
