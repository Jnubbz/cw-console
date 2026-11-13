package ft710

// CW operating controls: break-in, filter width, APF, IF shift, pitch, notches and the
// S-meter. Formats from Yaesu's FT-710 CAT Operation Reference Manual (BI, BC, BP, IS,
// EX menu 02-01-16 CW BK-IN TYPE) and Hamlib's newcat.c / ft710.h, which drive the radio
// for WSJT-X and the rest (SD, SH, CO, KP, SM and the S-meter scale).

import (
	"fmt"
	"strconv"
	"strings"
)

// ---- break-in ----

// BreakInTypeQuery reads menu 02-01-16, CW BK-IN TYPE (0 semi, 1 full / QSK).
const BreakInTypeQuery = "EX020116;"

// SetBreakInType: full (QSK) or semi break-in.
func SetBreakInType(full bool) string { return "EX020116" + b01(full) + ";" }

// BreakInDelays: what SD00-SD33 mean, in ms (30, 50, 100, 150, 200, 250, then 300-3000
// in 100s). Semi break-in only.
var BreakInDelays = func() []int {
	d := []int{30, 50, 100, 150, 200, 250}
	for ms := 300; ms <= 3000; ms += 100 {
		d = append(d, ms)
	}
	return d
}()

// SetBreakInDelay: the nearest step at or above ms (3000 at most).
func SetBreakInDelay(ms int) (string, error) {
	if ms < 0 || ms > 3000 {
		return "", fmt.Errorf("break-in delay %d ms is outside 30-3000", ms)
	}
	for i, d := range BreakInDelays {
		if ms <= d {
			return fmt.Sprintf("SD%02d;", i), nil
		}
	}
	return "SD33;", nil
}

// ---- receive filters ----

// CWWidths: SH00nn's table in CW (index nn -> Hz; 0 isn't a width).
var CWWidths = []int{0, 50, 100, 150, 200, 250, 300, 350, 400, 450, 500, 600, 800, 1200, 1400, 1700,
	2000, 2400, 3000, 3200, 3500, 4000}

// SetCWWidth: a width from CWWidths.
func SetCWWidth(hz int) (string, error) {
	for i, w := range CWWidths {
		if w == hz && i > 0 {
			return fmt.Sprintf("SH00%02d;", i), nil
		}
	}
	return "", fmt.Errorf("%d Hz isn't one of the FT-710's CW widths", hz)
}

// SetAPF: the audio peak filter (CW), on or off.
func SetAPF(on bool) string { return "CO02000" + b01(on) + ";" }

// MaxIFShift: IS range, Hz either way.
const MaxIFShift = 1200

// SetIFShift: hz in 20 Hz steps, within ±1200.
func SetIFShift(hz int) (string, error) {
	if hz < -MaxIFShift || hz > MaxIFShift {
		return "", fmt.Errorf("IF shift %d Hz is outside ±%d", hz, MaxIFShift)
	}
	hz = roundTo(hz, 20)
	return fmt.Sprintf("IS00%+05d;", hz), nil
}

// ---- notches ----

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

// ---- keyer ----

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

// ControlQueries: what to send, in order, for ParseControls.
var ControlQueries = []string{"BI;", BreakInTypeQuery, "SD;", "SH0;", "CO02;", "IS0;", "KP;", "KS;", "BC0;", "BP00;", "BP01;"}

// Controls: the CW controls as the radio has them. -1 / "" where an answer didn't parse.
type Controls struct {
	BreakIn     bool `json:"breakIn"`
	FullBreakIn bool `json:"fullBreakIn"`
	DelayMs     int  `json:"delayMs"`
	WidthHz     int  `json:"widthHz"`
	APF         bool `json:"apf"`
	IFShiftHz   int  `json:"ifShiftHz"`
	PitchHz     int  `json:"pitchHz"`
	KeyWPM      int  `json:"keyWpm"`
	AutoNotch   bool `json:"autoNotch"`
	Notch       bool `json:"notch"`
	NotchHz     int  `json:"notchHz"`
}

// ParseControls reads the answers to ControlQueries. It's forgiving: one odd answer
// (a firmware that says "?;") leaves that control unknown instead of losing the rest.
func ParseControls(a []string) (Controls, error) {
	c := Controls{DelayMs: -1, WidthHz: -1, PitchHz: -1, KeyWPM: -1, NotchHz: -1}
	if len(a) != len(ControlQueries) {
		return c, fmt.Errorf("expected %d answers, got %d", len(ControlQueries), len(a))
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
	if v, ok := tail(a[1], "EX020116", 1); ok {
		c.FullBreakIn = v == 1
	}
	if v, ok := tail(a[2], "SD", 2); ok && v < len(BreakInDelays) {
		c.DelayMs = BreakInDelays[v]
	}
	if v, ok := tail(a[3], "SH0", 2); ok && v > 0 && v < len(CWWidths) {
		c.WidthHz = CWWidths[v]
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

// ---- S-meter ----

// SMeterQuery: the main receiver's S-meter, answered SM0nnn; (0-255).
const SMeterQuery = "SM0;"

// FT-710's S-meter scale (Hamlib FT710_STR_CAL): raw -> dB relative to S9.
var strCal = []calPoint{{0, -54}, {12, -48}, {27, -42}, {40, -36}, {55, -30}, {65, -24}, {80, -18},
	{95, -12}, {112, -6}, {130, 0}, {150, 10}, {172, 20}, {190, 30}, {220, 40}, {240, 50}, {255, 60}}

// SDB: dB over (or under) S9 for a raw S-meter reading.
func SDB(raw int) float64 { return interp(strCal, raw) }

// SLabel: "S7", "S9", "S9+20".
func SLabel(raw int) string {
	db := SDB(raw)
	if db >= 1 {
		return fmt.Sprintf("S9+%d", int(db+0.5))
	}
	s := 9 + int(db/6-0.5) // 6 dB an S unit
	if db > -3 {
		s = 9
	}
	if s < 0 {
		s = 0
	}
	return "S" + strconv.Itoa(s)
}

func roundTo(v, step int) int {
	if v >= 0 {
		return (v + step/2) / step * step
	}
	return -((-v + step/2) / step * step)
}

// isControlQuery: the reads above (for IsQuery).
func isControlQuery(cmd string) bool {
	if cmd == SMeterQuery {
		return true
	}
	for _, q := range ControlQueries {
		if q == cmd {
			return true
		}
	}
	return strings.HasPrefix(cmd, "EX") && len(cmd) == 9 // EX menu reads: EXnnnnnn;
}
