package main

// Contest mode (CQ WW DX CW): the zone exchange, dupes and multipliers as you type a call,
// the score, Enter-sends-message for running and search-and-pounce, contest macros, and
// the Cabrillo log. Contacts go in the regular ADIF log too (with CONTEST_ID and CQZ).

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Jnubbz/cw-console/contest"
	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/ops"
)

const cqwwID = "CQ-WW-CW"

// The contest macros' slots that Enter uses (the labels and text are yours to change).
const (
	ctCQ    = 0 // F1 CQ (Run, empty Call box)
	ctExch  = 1 // F2 their call + exchange (Run)
	ctTU    = 2 // F3 TU, logs (Run)
	ctMy    = 3 // F4 my call (S&P)
	ctAgn   = 5 // F6 AGN (Run, no zone yet)
	ctSPTU  = 7 // F8 TU + exchange, logs (S&P)
	ctNSlot = 8
)

// defaultContestMacros: contest-macros.txt when there isn't one.
const defaultContestMacros = `# Contest macros: the F-keys while Contest mode is on (Setup > Contest).
# Same format as macros.txt. {ZONE} is your CQ zone, {CALL} the Call box, {LOG} logs
# the contact (with the zone in the Zone box).
#
# Enter in the Call or Zone box sends the right one of these:
#   Run:  empty Call box -> F1 | a call -> F2 | then, with their zone -> F3 (logs)
#         (no zone yet -> F6)
#   S&P:  a call -> F4 | then, with their zone -> F8 (logs)

F1 = CQ       | CQ TEST {MYCALL} {MYCALL} TEST
F2 = Exch     | {CALL} 5NN {ZONE}
F3 = TU       | TU {MYCALL} {LOG}
F4 = My call  | {MYCALL}
F5 = His call | {CALL}
F6 = AGN      | AGN
F7 = ?        | ?
F8 = S&P TU   | TU 5NN {ZONE} {LOG}
`

func contestMacrosPath() string { return filepath.Join(supportDir(), "contest-macros.txt") }

func (t *txEngine) loadContestMacros() {
	b, err := os.ReadFile(contestMacrosPath())
	if err != nil {
		b = []byte(defaultContestMacros)
		os.MkdirAll(supportDir(), 0o755)
		os.WriteFile(contestMacrosPath(), b, 0o644)
	}
	t.contestText = string(b)
	_, t.contestMacros = cw.ParseMacros(t.contestText)
}

func (t *txEngine) saveContestMacrosLocked(text string) error {
	if len(text) > 100_000 {
		return errors.New("macros file too large")
	}
	os.MkdirAll(supportDir(), 0o755)
	if err := os.WriteFile(contestMacrosPath(), []byte(text), 0o644); err != nil {
		return err
	}
	t.contestText = text
	_, t.contestMacros = cw.ParseMacros(text)
	return nil
}

// activeMacros: the F-keys in use (the contest set while Contest mode is on).
func (t *txEngine) activeMacros() *[8]cw.Macro {
	if t.e.st.Contest != "" {
		return &t.contestMacros
	}
	return &t.macros
}

// autoLogLocked: whether {LOG} in a macro logs (Auto-log, per mode: everyday CW / contest).
func (e *Engine) autoLogLocked() bool {
	if e.st.Contest != "" {
		return e.st.ContestAutoLog
	}
	return e.st.AutoLog
}

// contestCTY: AD1C's country file. A cty.dat in the data folder wins over the built-in
// one (drop a new one there before a contest; it's read when the app starts).
func (e *Engine) contestCTY() *contest.CTY {
	if e.cty != nil {
		return e.cty
	}
	if f, err := os.Open(filepath.Join(supportDir(), "cty.dat")); err == nil {
		c, err := contest.ParseCTY(f)
		f.Close()
		if err == nil {
			e.cty, e.ctyFrom = c, "cty.dat in the data folder"
			return c
		}
		e.setNoteLocked("cty.dat in the data folder couldn't be read (" + err.Error() + "); using the built-in one")
	}
	e.cty, e.ctyFrom = contest.Builtin(), "built in"
	return e.cty
}

// myZoneLocked: the zone you send (Setup), else cty.dat's zone for your call.
func (e *Engine) myZoneLocked() int {
	if z := e.st.ContestZone; z >= 1 && z <= 40 {
		return z
	}
	if m, ok := e.contestCTY().Lookup(e.tx.ctx.MyCall); ok && m.CQ > 0 {
		return m.CQ
	}
	return 0
}

// contestLogLocked: the score, rebuilt from the log when it changed.
func (e *Engine) contestLogLocked() *contest.CQWW {
	key := fmt.Sprintf("%d|%d|%s|%d", e.logbook.Seq, e.st.ContestSince, e.tx.ctx.MyCall, e.myZoneLocked())
	if e.ct != nil && e.ctKey == key {
		return e.ct
	}
	s := contest.NewCQWW(e.contestCTY(), e.tx.ctx.MyCall, e.myZoneLocked())
	since := time.Unix(e.st.ContestSince, 0)
	for _, q := range e.logbook.Entries() {
		if q.Contest != cqwwID || q.Start.Before(since) {
			continue
		}
		s.Add(contest.QSO{Time: q.Start, FreqKHz: freqKHz(q.Freq), Band: q.Band, Call: q.Call,
			RSTSent: q.RSTSent, ZoneSent: q.MyCQZ, RSTRcvd: q.RSTRcvd, ZoneRcvd: q.CQZ})
	}
	e.ct, e.ctKey = s, key
	return s
}

func freqKHz(mhz string) int {
	f, err := strconv.ParseFloat(strings.TrimSpace(mhz), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f*1000 + 0.5)
}

// contestTickLocked: when the contest weekend starts, the log starts there (contacts from
// before, practice, stay in the regular log but out of the score).
func (e *Engine) contestTickLocked(now time.Time) {
	if e.st.Contest == "" {
		return
	}
	start, end := contest.Weekend(now.UTC().Year())
	if !now.Before(start) && !now.After(end) && e.st.ContestSince < start.Unix() {
		e.st.ContestSince = start.Unix()
		e.saveSettingsLocked()
		e.setNoteLocked("CQ WW has started: the contest log starts fresh at 0000Z (earlier contacts stay in your regular log). Good luck!")
	}
}

// heardZone: their zone from the decoded text: the number after a report ("5NN 14",
// "599 5", "TU 5NN T5"), cut numbers too. 0 when there isn't one.
func heardZone(text string) int {
	toks := strings.Fields(strings.ToUpper(text))
	for i := len(toks) - 2; i >= 0; i-- {
		if ops.FindRST(toks[i]) == "" {
			continue
		}
		z := strings.Map(func(r rune) rune {
			switch r {
			case 'T', 'O':
				return '0'
			case 'N':
				return '9'
			case 'A':
				return '1'
			}
			return r
		}, strings.Trim(toks[i+1], "?.,=+"))
		if len(z) == 0 || len(z) > 2 || strings.IndexFunc(z, func(r rune) bool { return !unicode.IsDigit(r) }) >= 0 {
			continue
		}
		if n, _ := strconv.Atoi(z); n >= 1 && n <= 40 {
			return n
		}
	}
	return 0
}

// theirZoneLocked: the zone to log: typed, else heard, else cty.dat's guess.
func (e *Engine) theirZoneLocked(call string) (zone int, from string) {
	if e.zoneRcvd > 0 {
		return e.zoneRcvd, "typed"
	}
	if z := heardZone(e.assist.RxText()); z > 0 {
		return z, "heard"
	}
	if m, ok := e.contestCTY().Lookup(call); ok && m.CQ > 0 && !m.MM {
		return m.CQ, "guess"
	}
	return 0, ""
}

// logContestLocked: logLocked for a contest contact.
func (e *Engine) logContestLocked(call string) error {
	zone, _ := e.theirZoneLocked(call)
	if zone == 0 {
		return errString("no zone for " + call + ": type it in the Zone box")
	}
	now := time.Now()
	freq, band := ops.ParseFreq(e.st.Freq)
	rst := e.assist.RSTRcvd()
	if rst == "" {
		rst = "599"
	}
	chk := e.contestLogLocked().Check(call, band, zone)
	q := ops.QSO{Call: call, Start: e.assist.StartTime(call, now), Freq: freq, Band: band,
		RSTSent: "599", RSTRcvd: rst, MyCall: e.tx.ctx.MyCall, MyState: e.tx.ctx.MyState,
		MyGrid: e.tx.ctx.MyGrid, Contest: cqwwID, CQZ: zone, MyCQZ: e.myZoneLocked()}
	if _, err := e.logbook.Add(q); err != nil {
		return errString("couldn't write the log: " + err.Error())
	}
	msg := fmt.Sprintf("Logged %s, zone %d", call, zone)
	switch {
	case band == "":
		msg += ". No band: the radio's frequency is needed for the score (Freq)."
	case chk.OffBand:
		msg += " (" + band + " isn't a CQ WW band: no points)"
	case chk.Dupe:
		msg += " (dupe: no points, no harm)"
	default:
		var m []string
		if chk.NewZone {
			m = append(m, fmt.Sprintf("zone %d", zone))
		}
		if chk.NewCountry {
			m = append(m, chk.Country)
		}
		if len(m) > 0 {
			msg += ". New multiplier: " + strings.Join(m, " + ")
		}
	}
	e.setNoteLocked(msg)
	e.assist.Logged(now)
	e.setCallLocked("", true)
	e.zoneRcvd, e.ctSentTo = 0, ""
	e.zoneSeq++
	return nil
}

// contestEnterLocked: Enter in the Call / Zone box (N1MM's "Enter sends message").
func (e *Engine) contestEnterLocked(call string, zone int) error {
	if e.st.Contest == "" {
		return errString("Contest mode is off")
	}
	e.setCallLocked(call, false)
	e.zoneRcvd = zone
	call = e.call
	send := func(i int) error { return e.tx.sendMacroLocked(i, call, true) }
	if !e.st.ContestSP { // running
		switch {
		case call == "":
			return send(ctCQ)
		case e.ctSentTo != call:
			e.ctSentTo = call
			return send(ctExch)
		}
		if z, _ := e.theirZoneLocked(call); z == 0 {
			return send(ctAgn)
		}
		return send(ctTU) // logs ({LOG})
	}
	switch { // search and pounce
	case call == "":
		return errString("type the call of the station you're calling")
	case e.ctSentTo != call:
		e.ctSentTo = call
		return send(ctMy)
	}
	if z, _ := e.theirZoneLocked(call); z == 0 {
		return send(ctMy)
	}
	return send(ctSPTU)
}

// contestState: for the page.
type contestState struct {
	On        bool            `json:"on"`
	SP        bool            `json:"sp"`
	MyZone    int             `json:"myZone"`
	Check     contest.Check   `json:"check"`
	Zone      int             `json:"zone"`     // zone that would be logged
	ZoneFrom  string          `json:"zoneFrom"` // typed | heard | guess
	ZoneSeq   int             `json:"zoneSeq"`  // bumps when the app clears the Zone box
	Sent      bool            `json:"sent"`     // the exchange (Run) / my call (S&P) went to this call
	AutoLog   bool            `json:"autoLog"`
	Summary   contest.Summary `json:"summary"`
	Recent    []ctRow         `json:"recent"`
	Since     int64           `json:"since"`
	Start     int64           `json:"start"` // this year's weekend
	End       int64           `json:"end"`
	CTY       string          `json:"cty"` // "20200405, built in"
	Location  string          `json:"location"`
	Practice  bool            `json:"practice"` // outside the contest weekend
	FileName  string          `json:"fileName"`
	MacroKeys []string        `json:"macroKeys"`
}

type ctRow struct {
	Time   string `json:"time"`
	Call   string `json:"call"`
	Band   string `json:"band"`
	Zone   int    `json:"zone"`
	Points int    `json:"points"`
	Mult   bool   `json:"mult"`
	Dupe   bool   `json:"dupe"`
}

func (e *Engine) fillContestLocked(s *stateMsg) {
	if e.st.Contest == "" {
		return
	}
	now := time.Now()
	e.contestTickLocked(now)
	ct := e.contestLogLocked()
	_, band := ops.ParseFreq(e.st.Freq)
	c := &contestState{On: true, SP: e.st.ContestSP, AutoLog: e.st.ContestAutoLog, MyZone: e.myZoneLocked(), Since: e.st.ContestSince,
		ZoneSeq: e.zoneSeq, Summary: ct.Summary(now), Recent: []ctRow{}, Location: e.contestLocationLocked()}
	if e.call != "" {
		c.Zone, c.ZoneFrom = e.theirZoneLocked(e.call)
		c.Check = ct.Check(e.call, band, c.Zone)
		c.Sent = e.ctSentTo == e.call
		s.Dupe = c.Check.Dupe // a contest dupe is per band, for the whole contest
	}
	log := ct.Log()
	for i := len(log) - 1; i >= 0 && len(c.Recent) < 8; i-- {
		q := log[i]
		c.Recent = append(c.Recent, ctRow{q.Time.UTC().Format("1504"), q.Call, q.Band, q.ZoneRcvd, q.Points, q.NewZone || q.NewCountry, q.Dupe})
	}
	st, en := contest.Weekend(now.UTC().Year())
	c.Start, c.End = st.Unix(), en.Unix()
	c.Practice = now.Before(st) || now.After(en)
	cty := e.contestCTY()
	c.CTY = cty.Version + ", " + e.ctyFrom
	c.FileName = e.cabrilloNameLocked()
	for _, m := range e.tx.contestMacros {
		c.MacroKeys = append(c.MacroKeys, m.Label)
	}
	s.Contest = c
}

func (e *Engine) contestLocationLocked() string {
	if l := strings.ToUpper(strings.TrimSpace(e.st.ContestLocation)); l != "" {
		return l
	}
	if m, ok := e.contestCTY().Lookup(e.tx.ctx.MyCall); ok && m.Entity != nil && m.Entity.Prefix == "K" && e.tx.ctx.MyState != "" {
		return e.tx.ctx.MyState // US: the state (MYSTATE in the macros)
	}
	return "DX"
}

func (e *Engine) cabrilloNameLocked() string {
	call := strings.ReplaceAll(strings.ToUpper(e.tx.ctx.MyCall), "/", "-")
	if call == "" {
		call = "MYCALL"
	}
	year := time.Unix(e.st.ContestSince, 0).UTC().Year()
	if e.st.ContestSince == 0 {
		year = time.Now().UTC().Year()
	}
	return fmt.Sprintf("%s-CQWW-CW-%d.log", call, year)
}

// writeCabrilloLocked saves the Cabrillo log in the Logs folder; returns its path.
func (e *Engine) writeCabrilloLocked() (string, int, error) {
	ct := e.contestLogLocked()
	log := ct.Log()
	if len(log) == 0 {
		return "", 0, errors.New("the contest log is empty")
	}
	if e.tx.ctx.MyCall == "" {
		return "", 0, errors.New("set MYCALL in the macros first")
	}
	sum := ct.Summary(time.Now())
	h := contest.Header{Call: e.tx.ctx.MyCall, Location: e.contestLocationLocked(), Assisted: e.st.ContestAssisted,
		Power: e.st.ContestPower, Grid: e.tx.ctx.MyGrid, Name: e.st.ContestName, Email: e.st.ContestEmail,
		ClaimedScore: sum.Score, CreatedBy: "CW Console " + appVersion}
	var b bytes.Buffer
	if err := contest.WriteCabrillo(&b, h, log); err != nil {
		return "", 0, err
	}
	os.MkdirAll(logDir(), 0o755)
	p := filepath.Join(logDir(), e.cabrilloNameLocked())
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		return "", 0, err
	}
	return p, len(log), nil
}

// clampContestLocked: after a settings change from the page.
func (e *Engine) clampContestLocked(old Settings) {
	s := &e.st
	if s.Contest != "" && s.Contest != "cqww" {
		s.Contest = old.Contest
	}
	if s.ContestZone < 0 || s.ContestZone > 40 {
		s.ContestZone = old.ContestZone
	}
	switch strings.ToUpper(s.ContestPower) {
	case "HIGH", "LOW", "QRP":
		s.ContestPower = strings.ToUpper(s.ContestPower)
	default:
		s.ContestPower = "LOW"
	}
	if len(s.ContestLocation) > 6 {
		s.ContestLocation = s.ContestLocation[:6]
	}
	if len(s.ContestName) > 60 {
		s.ContestName = s.ContestName[:60]
	}
	if len(s.ContestEmail) > 80 {
		s.ContestEmail = s.ContestEmail[:80]
	}
	if s.ContestSince != old.ContestSince {
		s.ContestSince = old.ContestSince // only "Start a new contest log" moves it
	}
	if s.Contest != "" && old.Contest == "" && s.ContestSince == 0 {
		s.ContestSince = time.Now().Unix()
	}
	if s.Contest != old.Contest || s.ContestSP != old.ContestSP {
		e.ctSentTo = ""
		e.assist.Stop() // Auto-CQ's macro numbers are the other set's
	}
}

// contestSpotMarks: dupe / new-multiplier marks for the band strip's spots.
func (e *Engine) contestSpotMarksLocked(band string, calls []string) map[string]string {
	if e.st.Contest == "" || band == "" {
		return nil
	}
	ct := e.contestLogLocked()
	out := map[string]string{}
	for _, c := range calls {
		k := ct.Check(c, band, 0)
		switch {
		case k.Dupe:
			out[c] = "dupe"
		case k.NewZone || k.NewCountry:
			out[c] = "mult"
		}
	}
	return out
}
