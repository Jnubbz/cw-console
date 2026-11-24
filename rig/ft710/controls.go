package ft710

// CW operating controls: break-in, filter width, APF, IF shift, pitch, notches. Formats
// from Yaesu's CAT Operation Reference Manuals (FT-710 2306-C: BI, BC, BP, IS, EX menu
// 02-01-16 CW BK-IN TYPE; FT-991A 1711-D: EX 056 CW BK-IN TYPE, SD in ms, SH0, IS0) and
// Hamlib's newcat.c, ft710.h and ft991.c (SD steps, SH tables, CO, KP).
//
//                 FT-710                    FT-991A
//   BK-IN type    EX020116 0 semi / 1 full  EX056 0 semi / 1 full
//   BK-IN delay   SD00-SD33 (steps)         SD0030-SD3000 (ms, 10 ms steps)
//   width         SH00nn                    SH0nn (its own table: no 600 Hz)
//   IF shift      IS00±nnnn                 IS0±nnnn
//   the rest      BI, CO02 (APF), KP, KS, BC0, BP00/BP01: the same

import (
	"fmt"
	"strconv"
)

// ---- break-in ----

// BreakInTypeQuery reads the FT-710's menu 02-01-16, CW BK-IN TYPE (0 semi, 1 full / QSK).
const BreakInTypeQuery = "EX020116;"

// SetBreakInType: full (QSK) or semi break-in, on the FT-710.
func SetBreakInType(full bool) string { return FT710.SetBreakInType(full) }

// SetBreakInType: full (QSK) or semi break-in.
func (m *Model) SetBreakInType(full bool) string { return m.bkType + b01(full) + ";" }

// BreakInDelays: what the FT-710's SD00-SD33 mean, in ms (30, 50, 100, 150, 200, 250, then
// 300-3000 in 100s). Semi break-in only.
var BreakInDelays = func() []int {
	d := []int{30, 50, 100, 150, 200, 250}
	for ms := 300; ms <= 3000; ms += 100 {
		d = append(d, ms)
	}
	return d
}()

// SetBreakInDelay: the FT-710's nearest step at or above ms.
func SetBreakInDelay(ms int) (string, error) { return FT710.SetBreakInDelay(ms) }

// SetBreakInDelay: semi break-in hang time, 30-3000 ms.
func (m *Model) SetBreakInDelay(ms int) (string, error) {
	if ms < 0 || ms > 3000 {
		return "", fmt.Errorf("break-in delay %d ms is outside 30-3000", ms)
	}
	if m.delayMs {
		ms = max(30, (ms+5)/10*10)
		return fmt.Sprintf("SD%04d;", min(ms, 3000)), nil
	}
	for i, d := range BreakInDelays {
		if ms <= d {
			return fmt.Sprintf("SD%02d;", i), nil
		}
	}
	return "SD33;", nil
}

// ---- receive filters ----

// CWWidths: the FT-710's SH00nn table in CW (index nn -> Hz; 0 isn't a width).
var CWWidths = []int{0, 50, 100, 150, 200, 250, 300, 350, 400, 450, 500, 600, 800, 1200, 1400, 1700,
	2000, 2400, 3000, 3200, 3500, 4000}

// cw991Widths: the FT-991A's SH0nn table in CW (Hamlib ft991_cw_widths).
var cw991Widths = []int{0, 50, 100, 150, 200, 250, 300, 350, 400, 450, 500, 800, 1200, 1400, 1700,
	2000, 2400, 3000}

// SetCWWidth: a width from the FT-710's CWWidths.
func SetCWWidth(hz int) (string, error) { return FT710.SetCWWidth(hz) }

// Widths: the CW filter widths this radio has, Hz, narrow to wide.
func (m *Model) Widths() []int { return append([]int(nil), m.widths[1:]...) }

// SetCWWidth: one of Widths.
func (m *Model) SetCWWidth(hz int) (string, error) {
	for i, w := range m.widths {
		if w == hz && i > 0 {
			return fmt.Sprintf("%s%02d;", m.widthSet, i), nil
		}
	}
	return "", fmt.Errorf("%d Hz isn't one of the %s's CW widths", hz, m.Name)
}

// SetAPF: the audio peak filter (CW), on or off (both radios: CO02).
func SetAPF(on bool) string { return "CO02000" + b01(on) + ";" }

// MaxIFShift: IS range, Hz either way.
const MaxIFShift = 1200

// SetIFShift: on the FT-710.
func SetIFShift(hz int) (string, error) { return FT710.SetIFShift(hz) }

// SetIFShift: hz in 20 Hz steps, within ±1200.
func (m *Model) SetIFShift(hz int) (string, error) {
	if hz < -MaxIFShift || hz > MaxIFShift {
		return "", fmt.Errorf("IF shift %d Hz is outside ±%d", hz, MaxIFShift)
	}
	return fmt.Sprintf("%s%+05d;", m.ifShift, roundTo(hz, 20)), nil
}

// ---- notches (both radios) ----

// SetAutoNotch: the DSP auto notch (DNF), for carriers.
func SetAutoNotch(on bool) string { return "BC0" + b01(on) + ";" }

// SetManualNotch: the manual notch, on or off.
func SetManualNotch(on bool) string {
	if on {
		return "BP00001;"
	}
	return "BP00000;"
}

// SetNotchFreq: the manual notch's audio frequency, 10-3200 Hz in 10 Hz steps.
func SetNotchFreq(hz int) (string, error) {
	n := (hz + 5) / 10
	if n < 1 || n > 320 {
		return "", fmt.Errorf("notch %d Hz is outside 10-3200", hz)
	}
	return fmt.Sprintf("BP01%03d;", n), nil
}

// ---- keyer (both radios) ----

// Pitch range (KP00-KP75: 300-1050 Hz in 10 Hz steps). The radio's CW pitch is also the
// sidetone and where its filters center.
const (
	MinPitch = 300
	MaxPitch = 1050
)

// SetPitch: the nearest 10 Hz step.
func SetPitch(hz int) (string, error) {
	if hz < MinPitch || hz > MaxPitch {
		return "", fmt.Errorf("pitch %d Hz is outside %d-%d", hz, MinPitch, MaxPitch)
	}
	return fmt.Sprintf("KP%02d;", (hz-MinPitch+5)/10), nil
}

// ---- reading them ----

// ControlQueries: the FT-710's, for ParseControls.
var ControlQueries = FT710.ControlQueries()

// ControlQueries: what to send, in order, for ParseControls.
func (m *Model) ControlQueries() []string {
	return []string{"BI;", m.bkType + ";", "SD;", "SH0;", "CO02;", "IS0;", "KP;", "KS;", "BC0;", "BP00;", "BP01;"}
}

// Controls: the CW controls as the radio has them. -1 where an answer didn't parse.
type Controls struct {
	BreakIn     bool  `json:"breakIn"`
	FullBreakIn bool  `json:"fullBreakIn"`
	DelayMs     int   `json:"delayMs"`
	WidthHz     int   `json:"widthHz"`
	Widths      []int `json:"widths"` // what this radio has
	APF         bool  `json:"apf"`
	IFShiftHz   int   `json:"ifShiftHz"`
	PitchHz     int   `json:"pitchHz"`
	KeyWPM      int   `json:"keyWpm"`
	AutoNotch   bool  `json:"autoNotch"`
	Notch       bool  `json:"notch"`
	NotchHz     int   `json:"notchHz"`
}

// ParseControls: the FT-710's.
func ParseControls(a []string) (Controls, error) { return FT710.ParseControls(a) }

// ParseControls reads the answers to ControlQueries. It's forgiving: one odd answer (a
// firmware that says "?;") leaves that control unknown instead of losing the rest.
func (m *Model) ParseControls(a []string) (Controls, error) {
	c := Controls{DelayMs: -1, WidthHz: -1, PitchHz: -1, KeyWPM: -1, NotchHz: -1, Widths: m.Widths()}
	q := m.ControlQueries()
	if len(a) != len(q) {
		return c, fmt.Errorf("expected %d answers, got %d", len(q), len(a))
	}
	tail := func(ans, prefix string, n int) (int, bool) {
		s, err := after(ans, prefix)
		if err != nil || len(s) < n {
			return 0, false
		}
		v, err := strconv.Atoi(s[len(s)-n:])
		return v, err == nil
	}
	if v, ok := tail(a[0], "BI", 1); ok {
		c.BreakIn = v == 1
	}
	if v, ok := tail(a[1], m.bkType, 1); ok {
		c.FullBreakIn = v == 1
	}
	if m.delayMs {
		if v, ok := tail(a[2], "SD", 4); ok {
			c.DelayMs = v
		}
	} else if v, ok := tail(a[2], "SD", 2); ok && v < len(BreakInDelays) {
		c.DelayMs = BreakInDelays[v]
	}
	if v, ok := tail(a[3], "SH0", 2); ok && v > 0 && v < len(m.widths) {
		c.WidthHz = m.widths[v]
	}
	if v, ok := tail(a[4], "CO02", 4); ok {
		c.APF = v == 1
	}
	if s, err := after(a[5], "IS0"); err == nil && len(s) >= 5 {
		if v, err := strconv.Atoi(s[len(s)-5:]); err == nil { // +0100 / -0100
			c.IFShiftHz = v
		}
	}
	if v, ok := tail(a[6], "KP", 2); ok {
		c.PitchHz = MinPitch + v*10
	}
	if v, ok := tail(a[7], "KS", 3); ok {
		c.KeyWPM = v
	}
	if v, ok := tail(a[8], "BC0", 1); ok {
		c.AutoNotch = v == 1
	}
	if v, ok := tail(a[9], "BP00", 3); ok {
		c.Notch = v == 1
	}
	if v, ok := tail(a[10], "BP01", 3); ok {
		c.NotchHz = v * 10
	}
	return c, nil
}

func roundTo(v, step int) int {
	if v >= 0 {
		return (v + step/2) / step * step
	}
	return -((-v + step/2) / step * step)
}
