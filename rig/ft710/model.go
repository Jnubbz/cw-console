package ft710

// The radios this package speaks to, and where their CAT dialects differ. The FT-710 and
// the FT-991A share Yaesu's "newcat" command set; the differences below are from Yaesu's
// CAT Operation Reference Manuals (FT-710 ENG 2306-C; FT-991A 1711-D) and Hamlib's
// newcat.c / ft710.h / ft991.c / ft991.h.
//
//                FT-710                 FT-991A
//   ID           ID0800;                ID0670; (FT-991: ID0570;)
//   CAT rate     38400 (CAT-1 RATE)     4800 by default (menu 031)
//   bands        160-6 m                160-6 m, 2 m (BS15), 70 cm (BS16)
//   split (set)  FT0; / FT1;            FT2; / FT3; (FT0/FT1 there toggle TX); reads 0/1
//   clarifier    CF000xy000; CF001±n    RT0/1; XT0/1; RC; RU/RD nnnn; offset from IF;
//   tuner start  AC003;                 AC002; (HF and 6 m only)
//   power        5-100 W                5-100 W; 50 W at most on 2 m and 70 cm
//   PC keying    -                      menu 060: 0 OFF (factory), 1 DAKY, 2 RTS, 3 DTR
//
// Everything else the app sends (FA/FB, MD0, SV, AB, BA, PC, RG0, AG0, BI, KS, KP, BS,
// RM1/4/5/6, ID) is the same on both.

import (
	"fmt"
	"strconv"
	"strings"
)

// Model is one radio's CAT dialect.
type Model struct {
	Name        string
	ID          string // its answer to "ID;"
	DefaultBaud int    // CAT rate as shipped
	MaxHz       int    // FA/FB upper limit
	bandOrder   []string
	bands       map[string]string // band -> BS code
	clarCF      bool              // CF command (FT-710) or RT/XT/RU/RD (FT-991A)
	split       [2]string         // set: off, on
	tune        string
	vhfMaxW     int // power limit at and above 144 MHz (0: no VHF)
	po, swr     []calPoint
	pcKeying    string // EX menu that has to be DTR/RTS for key-line CW ("" none)
	bkType      string // EX menu: CW BK-IN TYPE (0 semi, 1 full)
	delayMs     bool   // SD takes ms (FT-991A), not step numbers (FT-710)
	widths      []int  // SH table, CW
	widthSet    string // "SH00" / "SH0"
	ifShift     string // "IS00" / "IS0"
}

var hfBands = []string{"160m", "80m", "60m", "40m", "30m", "20m", "17m", "15m", "12m", "10m", "6m"}

var hfCodes = map[string]string{
	"160m": "00", "80m": "01", "60m": "02", "40m": "03", "30m": "04", "20m": "05",
	"17m": "06", "15m": "07", "12m": "08", "10m": "09", "6m": "10",
}

// FT710: the Yaesu FT-710.
var FT710 = &Model{Name: "FT-710", ID: "ID0800;", DefaultBaud: 38400, MaxHz: 75_000_000,
	bandOrder: hfBands, bands: hfCodes, clarCF: true, split: [2]string{"FT0;", "FT1;"},
	tune: "AC003;", po: poCal, swr: swrCal,
	bkType: "EX020116", widths: CWWidths, widthSet: "SH00", ifShift: "IS00"}

// FT991A: the Yaesu FT-991A (HF, 6 m, 2 m, 70 cm).
var FT991A = func() *Model {
	codes := map[string]string{"2m": "15", "70cm": "16"}
	for b, c := range hfCodes {
		codes[b] = c
	}
	return &Model{Name: "FT-991A", ID: "ID0670;", DefaultBaud: 4800, MaxHz: 470_000_000,
		bandOrder: append(append([]string(nil), hfBands...), "2m", "70cm"), bands: codes,
		split: [2]string{"FT2;", "FT3;"}, tune: "AC002;", vhfMaxW: 50,
		po: po991Cal, swr: swrDefaultCal, pcKeying: "EX060",
		bkType: "EX056", delayMs: true, widths: cw991Widths, widthSet: "SH0", ifShift: "IS0"}
}()

// FT991: the original FT-991, same dialect as the FT-991A.
var FT991 = func() *Model { m := *FT991A; m.Name, m.ID = "FT-991", "ID0570;"; return &m }()

// Models: every radio this package knows.
var Models = []*Model{FT710, FT991A, FT991}

// ModelForID: the radio that answers "ID;" with id, or nil.
func ModelForID(id string) *Model {
	id = strings.TrimSpace(id)
	for _, m := range Models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// FT-991 meter scales (Hamlib ft991.h, power from FLRig; SWR is Hamlib's Yaesu default).
var (
	po991Cal      = []calPoint{{0, 0}, {10, 0.8}, {50, 8}, {100, 26}, {150, 54}, {200, 92}, {250, 140}}
	swrDefaultCal = []calPoint{{0, 1.0}, {12, 1.0}, {39, 1.35}, {65, 1.5}, {89, 2.0}, {242, 5.0}, {255, 5.0}}
)

// Bands: the band keys this radio has, low to high.
func (m *Model) Bands() []string { return append([]string(nil), m.bandOrder...) }

// SelectBand: "2m" -> "BS15;".
func (m *Model) SelectBand(band string) (string, error) {
	c, ok := m.bands[band]
	if !ok {
		return "", fmt.Errorf("the %s has no %q band key", m.Name, band)
	}
	return "BS" + c + ";", nil
}

func (m *Model) freq(cmd string, hz int) (string, error) {
	if hz < MinHz || hz > m.MaxHz {
		return "", fmt.Errorf("%.6f MHz is outside the %s's range", float64(hz)/1e6, m.Name)
	}
	return fmt.Sprintf("%s%09d;", cmd, hz), nil
}

// SetFreqA / SetFreqB: VFO frequency in Hz.
func (m *Model) SetFreqA(hz int) (string, error) { return m.freq("FA", hz) }
func (m *Model) SetFreqB(hz int) (string, error) { return m.freq("FB", hz) }

// SetSplit: transmit on VFO-B (split) or VFO-A.
func (m *Model) SetSplit(on bool) string {
	if on {
		return m.split[1]
	}
	return m.split[0]
}

// SetClar: clarifier on/off for receive and transmit.
func (m *Model) SetClar(rx, tx bool) string {
	if m.clarCF {
		return "CF000" + b01(rx) + b01(tx) + "000;"
	}
	return "RT" + b01(rx) + ";XT" + b01(tx) + ";"
}

// SetClarOffset: clarifier offset in Hz (±9999). On the FT-991A that's clear, then up or
// down by that much (RC; RU/RD nnnn;).
func (m *Model) SetClarOffset(hz int) (string, error) {
	if hz < -MaxClar || hz > MaxClar {
		return "", fmt.Errorf("clarifier offset %d Hz is outside ±%d", hz, MaxClar)
	}
	if m.clarCF {
		return fmt.Sprintf("CF001%+05d;", hz), nil
	}
	switch {
	case hz > 0:
		return fmt.Sprintf("RC;RU%04d;", hz), nil
	case hz < 0:
		return fmt.Sprintf("RC;RD%04d;", -hz), nil
	}
	return "RC;", nil
}

// MaxPowerAt: the most the radio transmits at hz (W).
func (m *Model) MaxPowerAt(hz int) int {
	if m.vhfMaxW > 0 && hz >= 144_000_000 {
		return m.vhfMaxW
	}
	return MaxPower
}

// SetPower: transmit power in watts for a radio tuned to hz (0: not known; the HF limit).
func (m *Model) SetPower(w, hz int) (string, error) {
	max := m.MaxPowerAt(hz)
	if w < MinPower || w > max {
		return "", fmt.Errorf("power %d W is outside %d-%d W", w, MinPower, max)
	}
	return fmt.Sprintf("PC%03d;", w), nil
}

// TunerStart: start the built-in tuner.
func (m *Model) TunerStart() string { return m.tune }

// TunerCovers: whether the built-in tuner works at hz (HF and 6 m).
func (m *Model) TunerCovers(hz int) bool { return hz < 54_000_001 }

// HasVHF: 2 m and 70 cm.
func (m *Model) HasVHF() bool { return m.vhfMaxW > 0 }

// Watts, SWR, ALC from RM5 / RM6 / RM4 readings, on this radio's scale.
func (m *Model) Watts(raw int) float64 { return interp(m.po, raw) }
func (m *Model) SWR(raw int) float64   { return interp(m.swr, raw) }
func (m *Model) ALC(raw int) float64   { return ALC(raw) }

// ---- the rig panel's state ----

// StateQueries: what to send, in order, for ParseState.
func (m *Model) StateQueries() []string {
	if m.clarCF {
		return StateQueries
	}
	return []string{"FA;", "FB;", "MD0;", "FT;", "IF;", "PC;", "RG0;", "AG0;"}
}

// ParseState reads the answers to StateQueries.
func (m *Model) ParseState(a []string) (State, error) {
	if m.clarCF {
		return ParseState(a)
	}
	var s State
	if len(a) != 8 {
		return s, fmt.Errorf("expected 8 answers, got %d", len(a))
	}
	var err error
	if s.FreqA, err = intAfter(a[0], "FA"); err != nil {
		return s, err
	}
	if s.FreqB, err = intAfter(a[1], "FB"); err != nil {
		return s, err
	}
	md, err := after(a[2], "MD0")
	if err != nil || len(md) != 1 {
		return s, fmt.Errorf("unexpected mode answer %q", a[2])
	}
	s.Mode, s.ModeName = md, ModeName(md)
	ft, err := after(a[3], "FT")
	if err != nil || ft == "" {
		return s, fmt.Errorf("unexpected TX VFO answer %q", a[3])
	}
	s.Split = ft[0] == '1'
	if s.ClarHz, s.ClarRX, s.ClarTX, err = parseIFClar(a[4]); err != nil {
		return s, err
	}
	if s.PowerW, err = intAfter(a[5], "PC"); err != nil {
		return s, err
	}
	if s.RFGain, err = gainAfter(a[6], "RG0"); err != nil {
		return s, err
	}
	if s.AFGain, err = gainAfter(a[7], "AG0"); err != nil {
		return s, err
	}
	s.finish()
	return s, nil
}

// parseIFClar: the clarifier out of an FT-991A IF answer: IF, memory channel (3), VFO-A
// (9), offset (sign + 4), RX clarifier, TX clarifier, mode, ...
func parseIFClar(answer string) (hz int, rx, tx bool, err error) {
	v, err := after(answer, "IF")
	if err != nil || len(v) < 19 {
		return 0, false, false, fmt.Errorf("unexpected IF answer %q", answer)
	}
	if hz, err = strconv.Atoi(v[12:17]); err != nil {
		return 0, false, false, fmt.Errorf("unexpected clarifier offset in %q", answer)
	}
	return hz, v[17] == '1', v[18] == '1', nil
}

// finish: where the radio listens and transmits.
func (s *State) finish() {
	s.RXFreq, s.TXFreq = s.FreqA, s.FreqA
	if s.Split {
		s.TXFreq = s.FreqB
	}
	if s.ClarRX {
		s.RXFreq += s.ClarHz
	}
	if s.ClarTX {
		s.TXFreq += s.ClarHz
	}
}

// ---- around a sweep ----

// saveQueries: what to read before taking over the radio, for restoreCmds.
func (m *Model) saveQueries() []string {
	clar := "CF000;"
	if !m.clarCF {
		clar = "IF;"
	}
	return []string{"FA;", "MD0;", "PC;", "BI;", "FT;", clar}
}

// restoreCmds: the commands that put the radio back as the saveQueries answers say.
func (m *Model) restoreCmds(saved []string) ([]string, error) {
	if len(saved) != 6 {
		return nil, fmt.Errorf("%d answers", len(saved))
	}
	out := []string{strip(saved[1], "MD0", "MD0"), strip(saved[2], "PC", "PC"), strip(saved[3], "BI", "BI")}
	ft, err := after(saved[4], "FT")
	if err != nil || ft == "" {
		return nil, fmt.Errorf("unexpected answer %q", saved[4])
	}
	out = append(out, m.SetSplit(ft[0] == '1')) // the answer is 0/1; the set may not be
	if m.clarCF {
		out = append(out, strip(saved[5], "CF000", "CF000"))
	} else {
		_, rx, tx, err := parseIFClar(saved[5])
		if err != nil {
			return nil, err
		}
		out = append(out, m.SetClar(rx, tx))
	}
	out = append(out, saved[0])
	for _, r := range out {
		if r == "" {
			return nil, fmt.Errorf("unexpected answer while reading the radio: %q", saved)
		}
	}
	return out, nil
}

// ---- PC keying (FT-991A) ----

// PCKeyingQuery: the menu that lets the USB key line key CW ("" if the radio has none
// to check).
func (m *Model) PCKeyingQuery() string {
	if m.pcKeying == "" {
		return ""
	}
	return m.pcKeying + ";"
}

// PCKeying values (FT-991A menu 060).
const (
	PCKeyingOff  = 0
	PCKeyingDAKY = 1
	PCKeyingRTS  = 2
	PCKeyingDTR  = 3
)

// ParsePCKeying reads the menu's answer ("EX0603;" -> 3).
func (m *Model) ParsePCKeying(answer string) (int, error) {
	v, err := after(answer, m.pcKeying)
	if err != nil || len(v) != 1 || v[0] < '0' || v[0] > '3' {
		return 0, fmt.Errorf("unexpected PC KEYING answer %q", answer)
	}
	return int(v[0] - '0'), nil
}

// SetPCKeying: the menu's value (PCKeyingDTR or PCKeyingRTS for the app's key line).
func (m *Model) SetPCKeying(v int) (string, error) {
	if m.pcKeying == "" || v < 0 || v > 3 {
		return "", fmt.Errorf("the %s has no PC KEYING setting %d", m.Name, v)
	}
	return fmt.Sprintf("%s%d;", m.pcKeying, v), nil
}
