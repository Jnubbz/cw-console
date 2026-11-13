package main

// FT-710 CW controls beyond the rig panel's basics: break-in, filter width / APF / IF
// shift, notches, one speed and one pitch (the app's WPM -> the radio's keyer; the Pitch
// slider -> the radio's CW pitch and sidetone), zero-in, live meters, and the power
// remembered per band (home and POTA separately).

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/ops"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

// readControls: the CW controls (a rejected query leaves that one unknown).
func (e *Engine) readControls(spec catSpec) (*ft710.Controls, error) {
	a, err := e.rig.doLenient(spec, ft710.ControlQueries...)
	if err != nil {
		return nil, err
	}
	c, err := ft710.ParseControls(a)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// controlCmds: the CW control actions (nil, nil: not one of them).
func (a rigAction) controlCmds() ([]string, error) {
	on := a.Value == "1" || a.Value == "true"
	n, nerr := strconv.Atoi(strings.TrimSpace(a.Value))
	num := func(f func(int) (string, error)) ([]string, error) {
		if nerr != nil {
			return nil, fmt.Errorf("%s: %q isn't a number", a.Action, a.Value)
		}
		c, err := f(n)
		if err != nil {
			return nil, err
		}
		return []string{c}, nil
	}
	switch a.Action {
	case "breakIn":
		return []string{ft710.SetBreakIn(on)}, nil
	case "breakInType":
		return []string{ft710.SetBreakInType(a.Value == "full")}, nil
	case "breakInDelay":
		return num(ft710.SetBreakInDelay)
	case "width":
		return num(ft710.SetCWWidth)
	case "apf":
		return []string{ft710.SetAPF(on)}, nil
	case "ifShift":
		return num(ft710.SetIFShift)
	case "pitch":
		return num(ft710.SetPitch)
	case "keySpeed":
		return num(ft710.SetKeySpeed)
	case "autoNotch":
		return []string{ft710.SetAutoNotch(on)}, nil
	case "notch":
		return []string{ft710.SetManualNotch(on)}, nil
	case "notchFreq":
		return num(ft710.SetNotchFreq)
	}
	return nil, nil
}

// ---- meters ----

type meterReply struct {
	TX     bool    `json:"tx"`
	SRaw   int     `json:"sRaw"`
	S      string  `json:"s"`   // "S7", "S9+20"
	SDB    float64 `json:"sDb"` // over S9
	PowerW float64 `json:"powerW"`
	ALC    float64 `json:"alc"` // % of full scale
	SWR    float64 `json:"swr"`
	Error  string  `json:"error,omitempty"`
}

// meters: the S-meter while receiving; power, ALC and SWR while transmitting (the
// power meter says which: it reads zero on receive).
func (e *Engine) meters() meterReply {
	if e.sweepRunning() {
		return meterReply{Error: "SWR sweep running"}
	}
	cat, spec, err := e.ft710CAT()
	if err != nil {
		return meterReply{Error: err.Error()}
	}
	a, err := cat(ft710.SMeterQuery, ft710.MeterPO)
	if err != nil || len(a) != 2 {
		return meterReply{Error: fmt.Sprint("meters: ", err)}
	}
	var m meterReply
	if v, err := ft710.MeterRaw(a[0], "SM0"); err == nil {
		m.SRaw, m.S, m.SDB = v, ft710.SLabel(v), math.Round(ft710.SDB(v))
	}
	po, _ := ft710.MeterRaw(a[1], ft710.MeterPO)
	if po > 4 {
		m.TX, m.PowerW = true, math.Round(ft710.Watts(po)*10)/10
		if b, err := e.rig.doLenient(spec, ft710.MeterALC, ft710.MeterSWR); err == nil && len(b) == 2 {
			if v, err := ft710.MeterRaw(b[0], ft710.MeterALC); err == nil {
				m.ALC = math.Round(ft710.ALC(v))
			}
			if v, err := ft710.MeterRaw(b[1], ft710.MeterSWR); err == nil {
				m.SWR = math.Round(ft710.SWR(v)*10) / 10
			}
		}
	}
	return m
}

// ---- zero-in ----

// Tone of the strongest signal within ±win Hz of center, from the last second of the
// waterfall (5 Hz columns, parabolic peak); ok false when nothing stands out.
func (e *Engine) toneNearLocked(center, win float64) (hz float64, ok bool) {
	rows := e.wfHist
	if len(rows) < 3 {
		return 0, false
	}
	cols := len(rows[0])
	avg := make([]float64, cols)
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			avg[i] += float64(r[i]) / float64(len(rows))
		}
	}
	colHz := float64(specHi-specLo) / float64(cols-1)
	lo := max(1, int((center-win-specLo)/colHz))
	hi := min(cols-2, int((center+win-specLo)/colHz+0.5))
	best := -1
	for i := lo; i <= hi; i++ {
		if best < 0 || avg[i] > avg[best] {
			best = i
		}
	}
	// 255 is 36 dB over the band's noise floor: ask for about 8 dB.
	if best < 0 || avg[best] < 56 {
		return 0, false
	}
	a, b, c := avg[best-1], avg[best], avg[best+1]
	off := 0.0
	if d := a - 2*b + c; d < 0 {
		off = 0.5 * (a - c) / d
	}
	return specLo + (float64(best)+off)*colHz, true
}

// zeroIn moves VFO A so the signal the decoder is on sounds at the radio's CW pitch
// (where its filters center and its sidetone is), then puts the decoder there too. Only
// in CW, never while keyed.
func (e *Engine) zeroIn() rigReply {
	e.mu.Lock()
	switch {
	case e.st.Mode != "cw":
		e.mu.Unlock()
		return rigReply{Error: "zero-in is for CW"}
	case e.tx.busyLocked() || (e.tx.keyer != nil && e.tx.keyer.Active(txMuteHang)):
		e.mu.Unlock()
		return rigReply{Error: "not while sending"}
	}
	tone, ok := e.toneNearLocked(e.st.Pitch, 120)
	e.mu.Unlock()
	if !ok {
		return rigReply{Error: "no signal near the decoder's pitch to zero in on"}
	}
	if e.sweepRunning() {
		return rigReply{Error: "an SWR sweep is running"}
	}
	cat, spec, err := e.ft710CAT()
	if err != nil {
		return rigReply{Error: err.Error()}
	}
	a, err := cat("FA;", "MD0;", "KP;", ft710.MeterPO)
	if err != nil || len(a) != 4 {
		return rigReply{Port: spec.Port, Error: fmt.Sprint("zero-in: ", err)}
	}
	if po, _ := ft710.MeterRaw(a[3], ft710.MeterPO); po > 4 {
		return rigReply{Port: spec.Port, Error: "not while transmitting"}
	}
	mode := strings.TrimSuffix(strings.TrimPrefix(a[1], "MD0"), ";")
	if mode != "3" && mode != "7" {
		return rigReply{Port: spec.Port, Error: "the radio isn't in CW"}
	}
	dial, err1 := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(a[0], "FA"), ";"))
	kp, err2 := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(a[2], "KP"), ";"))
	if err1 != nil || err2 != nil {
		return rigReply{Port: spec.Port, Error: "zero-in: odd answers from the radio"}
	}
	pitch := float64(ft710.MinPitch + kp*10)
	delta := int(math.Round(tone - pitch))
	if abs(delta) < 5 {
		e.mu.Lock()
		e.st.Pitch = pitch
		e.setNoteLocked(fmt.Sprintf("Already zero-beat (within %d Hz)", abs(delta)))
		e.mu.Unlock()
		return e.rigState()
	}
	if abs(delta) > 600 {
		return rigReply{Port: spec.Port, Error: "that signal is too far off to zero in"}
	}
	// CW-U: a tone delta Hz above the pitch is delta Hz above the dial; CW-L: below.
	to := dial + delta
	if mode == "7" {
		to = dial - delta
	}
	fa, err := ft710.SetFreqA(to)
	if err == nil {
		_, err = cat(fa)
	}
	if err != nil {
		return rigReply{Port: spec.Port, Error: "zero-in: " + err.Error()}
	}
	e.mu.Lock()
	e.st.Pitch = pitch // the station now sounds at the radio's pitch: listen there
	e.saveSettingsLocked()
	e.setNoteLocked(fmt.Sprintf("Zeroed in: %+d Hz", to-dial))
	e.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	return e.rigState()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ---- one speed, power per band ----

// afterRigReadLocked: after reading the radio. Keeps its keyer speed on the app's send
// speed, and sets / remembers the power per band (home and POTA separately).
// Returns the CAT commands to send (if any) and a note.
func (e *Engine) afterRigReadLocked(st *ft710.State, c *ft710.Controls) (cmds []string, note string) {
	if e.st.RigSync && c != nil && c.KeyWPM > 0 {
		if w := e.assist.SendWPM(e.st.WPM, time.Now()); w >= 4 && w <= 60 && w != c.KeyWPM {
			if ks, err := ft710.SetKeySpeed(w); err == nil {
				cmds = append(cmds, ks)
				c.KeyWPM = w
			}
		}
	}
	if !e.st.BandPower || st == nil || e.rp.sweep.Running {
		return cmds, ""
	}
	_, band := ops.ParseFreq(fmt.Sprintf("%.6f", float64(st.FreqA)/1e6))
	if band == "" {
		return cmds, ""
	}
	where := "home"
	if e.st.POTAMode {
		where = "pota"
	}
	key := where + "/" + band
	if e.st.PowerByBand == nil {
		e.st.PowerByBand = map[string]int{}
	}
	want, known := e.st.PowerByBand[key]
	switch {
	case key != e.powerKey: // changed band (or POTA mode): that band's power
		e.powerKey = key
		if known && want != st.PowerW {
			if pc, err := ft710.SetPower(want); err == nil {
				cmds = append(cmds, pc)
				note = fmt.Sprintf("Power %d W on %s (%s)", want, band, map[string]string{"home": "home", "pota": "POTA"}[where])
				st.PowerW = want
			}
		} else if !known && st.PowerW > 0 {
			e.st.PowerByBand[key] = st.PowerW
			e.saveSettingsLocked()
		}
	case st.PowerW > 0 && st.PowerW != want: // set on this band (app or the radio's knob): remember it
		e.st.PowerByBand[key] = st.PowerW
		e.saveSettingsLocked()
	}
	return cmds, note
}
