package main

// CW controls beyond the rig panel's basics, for the FT-710 and FT-991A: break-in, filter
// width / APF / IF shift, notches, one speed and one pitch (the app's send speed -> the
// radio's keyer; the Pitch slider -> the radio's CW pitch and sidetone), zero-in, and the
// power remembered per band (home and POTA separately). The live meters are meters.go's.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/ops"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

// readControls: the CW controls (a query the firmware rejects leaves that one unknown).
func (e *Engine) readControls(spec catSpec, m *ft710.Model) (*ft710.Controls, error) {
	a, err := e.rig.doLenient(spec, m.ControlQueries()...)
	if err != nil {
		return nil, err
	}
	c, err := m.ParseControls(a)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// controlCmds: the CW control actions (nil, nil: not one of them).
func (a rigAction) controlCmds(m *ft710.Model) ([]string, error) {
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
		return []string{m.SetBreakInType(a.Value == "full")}, nil
	case "breakInDelay":
		return num(m.SetBreakInDelay)
	case "width":
		return num(m.SetCWWidth)
	case "apf":
		return []string{ft710.SetAPF(on)}, nil
	case "ifShift":
		return num(m.SetIFShift)
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

// ---- zero-in ----

// toneNearLocked: the strongest signal's tone within ±win Hz of center, from the last
// second of the spectrum (peak interpolated between columns); ok false when nothing
// stands 8 dB over the noise there.
func (e *Engine) toneNearLocked(center, win float64) (hz float64, ok bool) {
	rows, span := e.zHist, e.zSpan
	if len(rows) < 3 || span.HiHz <= span.LoHz {
		return 0, false
	}
	cols := len(rows[0])
	avg := make([]float64, cols)
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			avg[i] += r[i] / float64(len(rows))
		}
	}
	colHz := (span.HiHz - span.LoHz) / float64(cols)
	at := func(i int) float64 { return span.LoHz + (float64(i)+0.5)*colHz }
	best := -1
	for i := 1; i < cols-1; i++ {
		if math.Abs(at(i)-center) > win {
			continue
		}
		if best < 0 || avg[i] > avg[best] {
			best = i
		}
	}
	if best < 0 || avg[best]-e.zFloor < 8 {
		return 0, false
	}
	a, b, c := avg[best-1], avg[best], avg[best+1]
	off := 0.0
	if d := a - 2*b + c; d < 0 {
		off = 0.5 * (a - c) / d
	}
	return at(best) + off*colHz, true
}

// zeroIn moves VFO A so the signal the decoder is on sounds at the radio's CW pitch
// (where its filters center and its sidetone is), then puts the decoder there too. Only
// in CW, never while keyed.
func (e *Engine) zeroIn() rigReply {
	if e.radioKeyed() {
		return rigReply{Error: "not while sending"}
	}
	e.mu.Lock()
	if e.st.Mode != "cw" {
		e.mu.Unlock()
		return rigReply{Error: "zero-in is for CW"}
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
	m := e.rigModel()
	a, err := cat("FA;", "MD0;", "KP;", ft710.MeterPO)
	if err != nil || len(a) != 4 {
		return rigReply{Port: spec.Port, Error: fmt.Sprint("zero-in: ", err)}
	}
	if po, _ := ft710.MeterRaw(a[3], ft710.MeterPO); po > 4 {
		return rigReply{Port: spec.Port, Error: "not while transmitting"}
	}
	mode := strings.TrimSuffix(strings.TrimPrefix(a[1], "MD0"), ";")
	if mode != ft710.CWU && mode != ft710.CWL {
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
	if mode == ft710.CWL {
		to = dial - delta
	}
	fa, err := m.SetFreqA(to)
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
// speed, and sets / remembers the power per band (home and POTA separately). Returns the
// CAT commands to send (if any) and a note. Holds e.mu and e.rp.mu.
func (e *Engine) afterRigReadLocked(m *ft710.Model, st *ft710.State, c *ft710.Controls) (cmds []string, note string) {
	if e.st.RigSync && c != nil && c.KeyWPM > 0 {
		if w := e.assist.SendWPM(e.st.WPM, time.Now()); w >= ft710.MinWPM && w <= ft710.MaxWPM && w != c.KeyWPM {
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
			if pc, err := m.SetPower(want, st.FreqA); err == nil {
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
