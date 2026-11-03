package main

// Operating aids: Auto-CQ, auto-reply, callsign capture, logging, match speed.
// The logic lives in package ops (shared with Windows); this is the glue.

import (
	"strings"
	"time"

	"cwdecoder/deepcw"
	"cwdecoder/ops"
)

// aiLagLocked: how far decoded text trails the audio with the current decoder.
func (e *Engine) aiLagLocked() time.Duration {
	if e.st.Decoder != "ai" || e.live == nil {
		return 300 * time.Millisecond
	}
	_, pass := e.live.Status()
	if pass <= 0 {
		return 3 * time.Second
	}
	iv := e.planLocked().Interval(pass)
	return time.Duration(deepcw.TailGuardSecs*float64(time.Second)) + iv + pass
}

func (e *Engine) assistTickLocked() {
	in := ops.Inputs{Now: time.Now(), TxBusy: e.tx.busyLocked(), TxOn: e.tx.keyer != nil,
		Lag: e.aiLagLocked(), MyCall: e.tx.ctx.MyCall}
	if e.dec != nil && e.audioRunning() {
		// Key down only counts while the classic decoder is copying characters, so
		// noise flicker doesn't look like someone keying.
		in.Signal = e.dec.KeyDown() && time.Since(e.lastClassic) < 3*time.Second
		in.RxWPM = float64(e.dec.WPM())
	}
	for _, a := range e.assist.Tick(in) {
		switch a.Kind {
		case ops.SendMacro:
			call := a.Call
			if call == "" {
				call = e.call
			}
			if err := e.tx.sendMacroLocked(a.Macro, call, false); err != nil {
				e.assist.Stop()
				e.setNoteLocked("Auto-CQ stopped: " + err.Error())
			}
		case ops.SetCall:
			e.setCallLocked(a.Call, true)
		case ops.Note:
			e.setNoteLocked(a.Text)
		case ops.AutoCQOff:
			// the page reads Armed() from the state
		}
	}
}

// setCallLocked updates the Call box. fromApp = changed by the app (so the page
// should show it), rather than typed on the page.
func (e *Engine) setCallLocked(call string, fromApp bool) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if len(call) > 14 {
		call = call[:14]
	}
	if call == e.call {
		return
	}
	e.call = call
	if fromApp {
		e.callSeq++
	} else if call != "" && e.assist.Armed() {
		e.assist.Manual(false, time.Now()) // the operator is entering a call: their turn
	}
}

func (e *Engine) setAutoCQLocked(on bool) error {
	if on && e.tx.keyer == nil {
		return errString("tick 'Transmit enabled' first")
	}
	if on && e.tx.macros[e.st.Ops.CQMacro].Text == "" {
		return errString("the CQ macro (F" + itoa(e.st.Ops.CQMacro+1) + ") is empty")
	}
	e.assist.SetAutoCQ(on, time.Now())
	return nil
}

// logLocked logs a contact with call (from the Log button or a {LOG} macro).
func (e *Engine) logLocked(call string) error {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" {
		return errString("nothing to log: the Call box is empty")
	}
	now := time.Now()
	freq, band := ops.ParseFreq(e.st.Freq)
	ctx := e.tx.ctx
	rstSent := ctx.RST
	if rstSent == "" {
		rstSent = "599"
	}
	rstRcvd := e.assist.RSTRcvd()
	if rstRcvd == "" {
		rstRcvd = "599"
	}
	q := ops.QSO{Call: call, Start: e.assist.StartTime(call, now), Freq: freq, Band: band,
		RSTSent: rstSent, RSTRcvd: rstRcvd, MyCall: ctx.MyCall, Park: e.parkLocked(),
		MyState: ctx.MyState, MyGrid: ctx.MyGrid}
	dupe, err := e.logbook.Add(q)
	if err != nil {
		return errString("couldn't write the log: " + err.Error())
	}
	msg := "Logged " + call
	if dupe {
		msg += " (dupe)"
	}
	if band == "" {
		msg += ". No band: type the frequency in Freq so the log has one."
	}
	e.setNoteLocked(msg)
	e.assist.Logged(now)
	e.setCallLocked("", true)
	return nil
}

func (e *Engine) undoLogLocked() error {
	q, err := e.logbook.Undo()
	if err != nil {
		return err
	}
	e.setNoteLocked("Removed " + q.Call + " from the log.")
	return nil
}

func (e *Engine) fillOpsLocked(s *stateMsg) {
	now := time.Now()
	s.AutoCQ = e.assist.Armed()
	s.OpsStatus = e.assist.Status(now)
	s.Calls = e.assist.Calls()
	if s.Calls == nil {
		s.Calls = []string{}
	}
	s.Call, s.CallSeq = e.call, e.callSeq
	_, band := ops.ParseFreq(e.st.Freq)
	s.Band = band
	park := e.parkLocked()
	s.Dupe = e.call != "" && e.logbook.IsDupe(e.call, band, park, now)
	s.TheirWPM = e.assist.TheirWPM(now)
	s.SendWPM = e.assist.SendWPM(e.st.WPM, now)
	a := e.logbook.ActivationOn(park, now)
	s.Act = actState{Park: park, Day: a.Day, Valid: a.Valid, Dupes: a.Dupes, Total: len(a.Rows),
		Need: ops.ActivationNeeded, Modes: a.Modes, Seq: e.logbook.Seq}
	if n := len(a.Rows); n > 0 {
		q := a.Rows[n-1]
		s.Act.Last = q.Call + " " + q.Start.UTC().Format("1504") + "z " + q.Band
	}
}

// actState: today's activation, for the bar on the page.
type actState struct {
	Park  string         `json:"park"`
	Day   string         `json:"day"` // YYYYMMDD UTC
	Valid int            `json:"valid"`
	Dupes int            `json:"dupes"`
	Total int            `json:"total"`
	Need  int            `json:"need"`
	Modes map[string]int `json:"modes"`
	Last  string         `json:"last"`
	Seq   int            `json:"seq"`
}

// parkLocked: the park being activated: the main screen's Park, else PARK from the
// macros file (where it used to be set).
func (e *Engine) parkLocked() string {
	if e.st.Park != "" {
		return e.st.Park
	}
	p, _ := ops.NormalizePark(e.tx.ctx.Extra["PARK"])
	return p
}

// logRows: the UTC day's contacts for the current park, for the page's log list.
func (e *Engine) logRows() any {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := e.logbook.ActivationOn(e.parkLocked(), time.Now())
	type row struct {
		Time, Call, Band, Mode, Freq, Sent, Rcvd string
		Dupe                                     bool
	}
	rows := []row{}
	for i := len(a.Rows) - 1; i >= 0; i-- { // newest first
		q := a.Rows[i]
		mode := q.Mode
		if mode == "" {
			mode = "CW"
		}
		rows = append(rows, row{q.Start.UTC().Format("15:04"), q.Call, q.Band, mode, q.Freq, q.RSTSent, q.RSTRcvd, q.Dupe})
	}
	file := ""
	if a.Park != "" && len(a.Rows) > 0 {
		file = ops.POTAFile(a.Rows[0].MyCall, a.Park, a.Rows[0].Start)
	}
	return map[string]any{"rows": rows, "file": file, "unparked": e.logbook.Unparked()}
}

type errString string

func (e errString) Error() string { return string(e) }
