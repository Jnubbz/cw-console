package main

// FT8 QSO sequencing, the way WSJT-X does it with its standard messages:
//
//   Tx1  THEM ME GRID       (answering a CQ)
//   Tx2  THEM ME -12        (report)
//   Tx3  THEM ME R-12       (roger + report)
//   Tx4  THEM ME RR73
//   Tx5  THEM ME 73
//   Tx6  CQ [POTA] ME GRID
//
// As the CQ station (activator): CQ -> caller answers -> Tx2 -> they send R-xx -> Tx4,
// logged -> next caller or CQ again. Answering a CQ (hunter): Tx1 -> they send a report
// -> Tx3 -> they send RR73/RRR -> Tx5, logged -> done.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type seqStep int

const (
	stIdle    seqStep = iota
	stCQ              // sending CQ
	stCall            // sending Tx1 to them
	stReport          // sending Tx2 (their report)
	stRReport         // sending Tx3 (R + their report)
	stRR73            // sent Tx4 (RR73), contact logged
	st73              // sending Tx5 (73), contact logged
)

var stepNames = map[seqStep]string{stIdle: "idle", stCQ: "calling CQ", stCall: "calling",
	stReport: "sent report", stRReport: "sent R-report", stRR73: "sent RR73", st73: "sent 73"}

const (
	maxCallTries = 8 // Tx1 repeats before giving up on a station (2 min of their CQs)
	maxQSOTries  = 5 // repeats of a QSO message with no progress
)

type ft8Log struct {
	Call, Grid, Sent, Rcvd string
}

type ft8Seq struct {
	MyCall, MyGrid string
	CQPota         bool // "CQ POTA KN0BLE EL98"
	AutoCQ         bool // after a contact (or giving up), go back to CQ instead of stopping

	step      seqStep
	parity    int // my transmit period: 0 = :00/:30, 1 = :15/:45
	their     string
	theirGrid string
	sent      string  // report we give them ("-12")
	rcvd      string  // report they gave us
	last      *ft8Log // the previous station worked (or tried)
	tries     int
	logged    bool
	seen      map[string]bool // slot+message already handled
	lastDone  string          // last finished contact (to resend RR73 if they missed it)
	lastSlot  string          // last receive slot counted (early and full decodes of a slot)
	rr73Slot  string          // slot whose decode made us send RR73 (it hasn't gone out yet)
}

var (
	reGrid   = regexp.MustCompile(`^[A-R]{2}[0-9]{2}([A-X]{2})?$`)
	reReport = regexp.MustCompile(`^[+-][0-9]{2}$`)
	reRRpt   = regexp.MustCompile(`^R[+-][0-9]{2}$`)
)

func fmtReport(snr int) string {
	snr = max(-30, min(30, snr))
	return fmt.Sprintf("%+03d", snr)
}

func bareCall(w string) string { return strings.Trim(w, "<>") }

func slotParity(slot string) int { // "HHMMSS" -> 0/1
	if len(slot) != 6 {
		return 0
	}
	s, _ := strconv.Atoi(slot[4:])
	return (s / 15) % 2
}

// cqCaller: the station calling CQ in a message, and its grid ("" if none).
func cqCaller(msg string) (call, grid string, ok bool) {
	w := strings.Fields(msg)
	if len(w) < 2 || w[0] != "CQ" {
		return "", "", false
	}
	if len(w) >= 3 && reGrid.MatchString(w[len(w)-1]) && w[len(w)-1] != "RR73" {
		return bareCall(w[len(w)-2]), w[len(w)-1], true
	}
	return bareCall(w[len(w)-1]), "", true
}

// Next is the message to transmit in my next period ("" = nothing).
func (s *ft8Seq) Next() string {
	me := s.MyCall
	switch s.step {
	case stCQ:
		m := "CQ "
		if s.CQPota {
			m += "POTA "
		}
		return strings.TrimSpace(m + me + " " + s.MyGrid)
	case stCall:
		return strings.TrimSpace(s.their + " " + me + " " + s.MyGrid)
	case stReport:
		return s.their + " " + me + " " + s.sent
	case stRReport:
		return s.their + " " + me + " R" + s.sent
	case stRR73:
		return s.their + " " + me + " RR73"
	case st73:
		return s.their + " " + me + " 73"
	}
	return ""
}

func (s *ft8Seq) Status() string {
	if s.step == stIdle {
		return ""
	}
	if s.their != "" && s.step != stCQ {
		return fmt.Sprintf("%s %s", stepNames[s.step], s.their)
	}
	return stepNames[s.step]
}

func (s *ft8Seq) reset() {
	if s.their != "" { // remember it, for logging by hand
		s.last = &ft8Log{Call: s.their, Grid: s.theirGrid, Sent: s.sent, Rcvd: s.rcvd}
	}
	s.their, s.theirGrid, s.sent, s.rcvd, s.tries, s.logged = "", "", "", "", 0, false
}

// Candidate: the contact a "Log QSO" press would log: the station being worked now,
// else the last one.
func (s *ft8Seq) Candidate() *ft8Log {
	if s.their != "" {
		return &ft8Log{Call: s.their, Grid: s.theirGrid, Sent: s.sent, Rcvd: s.rcvd}
	}
	return s.last
}

// StartCQ: call CQ in the given period.
func (s *ft8Seq) StartCQ(parity int) {
	s.reset()
	s.step, s.parity = stCQ, parity&1
}

// Stop: nothing more to send.
func (s *ft8Seq) Stop() { s.reset(); s.step = stIdle }

// Call answers a station heard in message msg (from the slot it was heard in): a CQ
// gets Tx1, and if the message was already addressed to us we pick up from there.
func (s *ft8Seq) Call(msg, slot string, snr int) error {
	w := strings.Fields(msg)
	var call, grid string
	if c, g, ok := cqCaller(msg); ok {
		call, grid = c, g
	} else if len(w) >= 2 {
		call = bareCall(w[1]) // "ME THEM ..." or "OTHER THEM ...": THEM is the sender
		if len(w) >= 3 && reGrid.MatchString(w[2]) {
			grid = w[2]
		}
	}
	if call == "" || call == s.MyCall {
		return fmt.Errorf("no station to call in %q", msg)
	}
	s.reset()
	s.their, s.theirGrid, s.parity = call, grid, 1-slotParity(slot)
	s.sent = fmtReport(snr)
	s.step = stCall
	if len(w) >= 2 && bareCall(w[0]) == s.MyCall { // they were already calling us
		s.step = stIdle
		s.handle(w, snr)
		if s.step == stIdle {
			s.step = stCall
		}
	}
	return nil
}

// Heard takes the decodes of one receive slot ("HHMMSS", the other period from ours).
// It may be called twice for a slot (a quick early decode, then the full one). It
// returns a contact to log, if one was just completed.
func (s *ft8Seq) Heard(slot string, decs []ft8Decode) (logIt *ft8Log) {
	if s.step == stIdle {
		return nil
	}
	if s.seen == nil || len(s.seen) > 2000 {
		s.seen = map[string]bool{}
	}
	if slotParity(slot) == s.parity {
		return nil // our own period: that's us (or someone on top of us)
	}
	again := slot == s.lastSlot
	s.lastSlot = slot
	var toMe []ft8Decode
	for _, d := range decs {
		key := d.Slot + "|" + d.Msg
		if s.seen[key] {
			continue
		}
		s.seen[key] = true
		w := strings.Fields(d.Msg)
		if len(w) >= 2 && bareCall(w[0]) == s.MyCall {
			toMe = append(toMe, d)
		}
	}
	progressed := false
	// 1) the station we're working
	if s.their != "" {
		for _, d := range toMe {
			w := strings.Fields(d.Msg)
			if bareCall(w[1]) == s.their {
				final := len(w) >= 3 && w[2] == "73" && (s.step == stRR73 || s.step == st73)
				if l := s.handle(w, d.SNR); l != nil {
					logIt = l
				}
				if final { // they said 73 to our RR73/73: done
					s.finish()
					break
				}
				progressed = true // still with us: answer (or repeat) accordingly
				break
			}
		}
	}
	if s.step == stRR73 && progressed {
		s.rr73Slot = slot
	}
	rr73Out := s.step == stRR73 && s.rr73Slot != slot // RR73 has been transmitted
	// 2) the last contact missed our RR73 and sent R-report again
	if !progressed && s.lastDone != "" && (s.step == stCQ || rr73Out) {
		for _, d := range toMe {
			w := strings.Fields(d.Msg)
			if bareCall(w[1]) == s.lastDone && len(w) >= 3 && reRRpt.MatchString(w[2]) {
				s.their, s.step, s.tries, s.logged, s.rr73Slot = s.lastDone, stRR73, 0, true, slot
				return logIt
			}
		}
	}
	// 3) CQing (or just finished): take the strongest new caller
	if !progressed && (s.step == stCQ || rr73Out) {
		sort.SliceStable(toMe, func(a, b int) bool { return toMe[a].SNR > toMe[b].SNR })
		for _, d := range toMe {
			w := strings.Fields(d.Msg)
			c := bareCall(w[1])
			if c == s.lastDone {
				continue
			}
			s.reset()
			s.their, s.sent, s.step = c, fmtReport(d.SNR), stIdle
			s.handle(w, d.SNR)
			if s.step == stIdle {
				s.step = stReport
			}
			return logIt
		}
	}
	if progressed || again {
		return logIt
	}
	// nothing new for us: count a repeat, give up after a while
	s.tries++
	switch {
	case rr73Out: // RR73 sent once and no complaint: done
		s.finish()
	case s.step == st73:
		s.finish()
	case s.step == stCall && s.tries >= maxCallTries, s.step != stCQ && s.step != stCall && s.tries >= maxQSOTries:
		s.finish()
	}
	return logIt
}

// handle: a message w (to me) from the station I'm working (or about to work).
func (s *ft8Seq) handle(w []string, snr int) *ft8Log {
	if s.sent == "" {
		s.sent = fmtReport(snr)
	}
	third := ""
	if len(w) >= 3 {
		third = w[2]
	}
	switch {
	case reGrid.MatchString(third) && third != "RR73", third == "":
		// Tx1 from a caller: they want a report
		if third != "" {
			s.theirGrid = third
		}
		if s.step == stIdle || s.step == stCQ || s.step == stReport {
			s.step, s.tries = stReport, 0
			s.sent = fmtReport(snr)
		}
	case reReport.MatchString(third):
		// they gave us a report: roger it with ours
		s.rcvd = third
		if s.step != stRReport {
			s.step, s.tries = stRReport, 0
			s.sent = fmtReport(snr) // report what we hear on this message, as WSJT-X does
		}
	case reRRpt.MatchString(third):
		// roger + report: we finish with RR73 and log
		s.rcvd = third[1:]
		if s.step != stRR73 {
			s.step, s.tries = stRR73, 0
			return s.logOnce()
		}
	case third == "RR73" || third == "RRR":
		s.step, s.tries = st73, 0
		return s.logOnce()
	case third == "73":
		l := s.logOnce()
		if s.step != st73 && s.step != stRR73 {
			s.step = st73
		}
		return l
	}
	return nil
}

func (s *ft8Seq) logOnce() *ft8Log {
	if s.logged {
		return nil
	}
	s.logged = true
	return &ft8Log{Call: s.their, Grid: s.theirGrid, Sent: s.sent, Rcvd: s.rcvd}
}

// finish ends the current contact (or attempt): back to CQ or stop.
func (s *ft8Seq) finish() {
	if s.logged {
		s.lastDone = s.their
	}
	cq := s.AutoCQ && s.MyCall != ""
	s.reset()
	if cq {
		s.step = stCQ
	} else {
		s.step = stIdle
	}
}

// Sent tells the sequencer a message went out (after RR73/73 once, we're done).
func (s *ft8Seq) Sent() {}
