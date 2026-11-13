// Package ft710 speaks the Yaesu FT-710's CAT command set: building commands, reading
// answers, and turning the radio's raw meter readings into SWR and watts. No I/O: the
// caller sends the strings over the radio's Enhanced COM port (CAT-1, 38400 8N1 by
// default) and passes the answers back.
//
// Command formats follow Yaesu's FT-710 CAT Operation Reference Manual, cross-checked
// against Hamlib's FT-710 driver (rigs/yaesu/newcat.c, ft710.h), which is tested on the
// radio. Where the FT-710 differs from its Yaesu siblings it's noted:
//
//   - The clarifier is CF (the FT-710 has no RT/XT commands).
//   - TX VFO (split) is FT0;/FT1; (the FTDX10/101 use FT2;/FT3;).
//   - Tuner start is AC003; (others use AC002;).
//   - KY only plays the radio's keyer memories (loaded with KM); it can't key typed text
//     as a Kenwood's KY does. Live CW goes over the DTR/RTS key line.
package ft710

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ID is what the FT-710 answers to "ID;".
const ID = "ID0800;"

// Mode codes for MD0<code>; (main band).
const (
	LSB   = "1"
	USB   = "2"
	CWU   = "3"
	FM    = "4"
	AM    = "5"
	RTTYL = "6"
	CWL   = "7"
	DATAL = "8"
	RTTYU = "9"
	DATAU = "C"
)

var modeNames = map[string]string{
	"1": "LSB", "2": "USB", "3": "CW-U", "4": "FM", "5": "AM", "6": "RTTY-L", "7": "CW-L",
	"8": "DATA-L", "9": "RTTY-U", "A": "DATA-FM", "B": "FM-N", "C": "DATA-U", "D": "AM-N",
	"E": "PSK", "F": "DATA-FM-N",
}

// ModeName: "CW-U" for "3", and so on ("mode X" if unknown).
func ModeName(code string) string {
	if n, ok := modeNames[code]; ok {
		return n
	}
	return "mode " + code
}

// Limits.
const (
	MinHz       = 30_000     // FA/FB range, Hz
	MaxHz       = 75_000_000 //
	MaxClar     = 9999       // clarifier offset, ± Hz
	MinPower    = 5          // PC range, W
	MaxPower    = 100        //
	MaxGain     = 255        // RG / AG range 0-255
	MinWPM      = 4          // KS range
	MaxWPM      = 60         //
	KeyerMemMax = 50         // characters in a keyer memory (KM)
)

// ---- set commands ----

func freq(cmd string, hz int) (string, error) {
	if hz < MinHz || hz > MaxHz {
		return "", fmt.Errorf("%.6f MHz is outside the FT-710's range", float64(hz)/1e6)
	}
	return fmt.Sprintf("%s%09d;", cmd, hz), nil
}

// SetFreqA / SetFreqB: VFO-A / VFO-B frequency in Hz.
func SetFreqA(hz int) (string, error) { return freq("FA", hz) }
func SetFreqB(hz int) (string, error) { return freq("FB", hz) }

// SetMode: main-band mode (one of the mode codes).
func SetMode(code string) (string, error) {
	if _, ok := modeNames[code]; !ok {
		return "", fmt.Errorf("unknown mode code %q", code)
	}
	return "MD0" + code + ";", nil
}

const (
	Swap = "SV;" // exchange VFO-A and VFO-B
	AtoB = "AB;" // copy A to B
	BtoA = "BA;" // copy B to A
)

// SetSplit: transmit on VFO-B (split) or VFO-A.
func SetSplit(on bool) string { return "FT" + b01(on) + ";" }

// SetClar: clarifier on/off for receive and transmit (RIT and XIT on other radios).
func SetClar(rx, tx bool) string { return "CF000" + b01(rx) + b01(tx) + "000;" }

// SetClarOffset: clarifier offset in Hz (±9999).
func SetClarOffset(hz int) (string, error) {
	if hz < -MaxClar || hz > MaxClar {
		return "", fmt.Errorf("clarifier offset %d Hz is outside ±%d", hz, MaxClar)
	}
	return fmt.Sprintf("CF001%+05d;", hz), nil
}

// ClarClear: offset back to zero.
const ClarClear = "RC;"

// SetPower: transmit power in watts (5-100).
func SetPower(w int) (string, error) {
	if w < MinPower || w > MaxPower {
		return "", fmt.Errorf("power %d W is outside %d-%d W", w, MinPower, MaxPower)
	}
	return fmt.Sprintf("PC%03d;", w), nil
}

// SetRFGain: RF gain, 0-255 (RG0nnn; as Hamlib sends it). Turned well down, weak
// signals never reach the audio, whatever the decoder does.
func SetRFGain(n int) (string, error) {
	if n < 0 || n > MaxGain {
		return "", fmt.Errorf("RF gain %d is outside 0-%d", n, MaxGain)
	}
	return fmt.Sprintf("RG0%03d;", n), nil
}

// SetAFGain: AF gain (volume), 0-255 (AG0nnn;).
func SetAFGain(n int) (string, error) {
	if n < 0 || n > MaxGain {
		return "", fmt.Errorf("AF gain %d is outside 0-%d", n, MaxGain)
	}
	return fmt.Sprintf("AG0%03d;", n), nil
}

// SetKeySpeed: the radio's own keyer speed (4-60 WPM).
func SetKeySpeed(wpm int) (string, error) {
	if wpm < MinWPM || wpm > MaxWPM {
		return "", fmt.Errorf("%d WPM is outside %d-%d", wpm, MinWPM, MaxWPM)
	}
	return fmt.Sprintf("KS%03d;", wpm), nil
}

// BandCodes: BS<code>; picks a band the way the radio's band keys do (back to where you
// last were on it: frequency and mode from the radio's band stack).
var BandCodes = map[string]string{
	"160m": "00", "80m": "01", "60m": "02", "40m": "03", "30m": "04", "20m": "05",
	"17m": "06", "15m": "07", "12m": "08", "10m": "09", "6m": "10",
}

// SelectBand: "20m" -> "BS05;".
func SelectBand(band string) (string, error) {
	c, ok := BandCodes[band]
	if !ok {
		return "", fmt.Errorf("the FT-710 has no %q band key", band)
	}
	return "BS" + c + ";", nil
}

// SetBreakIn: CW break-in (BK-IN) on/off. Key-line CW only transmits with it on.
func SetBreakIn(on bool) string { return "BI" + b01(on) + ";" }

// Tuner (built-in ATU).
const (
	TunerOff   = "AC000;"
	TunerOn    = "AC001;"
	TunerStart = "AC003;"
)

// LoadKeyerMemory: text into keyer memory 1-5 (KM; up to 50 characters, A-Z 0-9 and the
// usual punctuation). Play it with PlayKeyerMemory.
func LoadKeyerMemory(ch int, text string) (string, error) {
	if ch < 1 || ch > 5 {
		return "", fmt.Errorf("keyer memory %d: the FT-710 has 1-5", ch)
	}
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" || len(text) > KeyerMemMax {
		return "", fmt.Errorf("keyer memory text must be 1-%d characters", KeyerMemMax)
	}
	for _, r := range text {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 /?.,=-+", r) {
			return "", fmt.Errorf("the radio's keyer can't send %q", r)
		}
	}
	return fmt.Sprintf("KM%d%s;", ch, text), nil
}

// PlayKeyerMemory: send keyer memory 1-5 on the air (KY6-KYA).
func PlayKeyerMemory(ch int) (string, error) {
	if ch < 1 || ch > 5 {
		return "", fmt.Errorf("keyer memory %d: the FT-710 has 1-5", ch)
	}
	return "KY" + string("6789A"[ch-1]) + ";", nil
}

// ---- reading the radio ----

// Meters (RM).
const (
	MeterALC = "RM4;"
	MeterPO  = "RM5;"
	MeterSWR = "RM6;"
)

// StateQueries: what to send, in order, to fill a State with ParseState.
var StateQueries = []string{"FA;", "FB;", "MD0;", "FT;", "CF000;", "CF001;", "PC;", "RG0;", "AG0;"}

// IsQuery: whether cmd asks for an answer (so the caller should read one).
func IsQuery(cmd string) bool {
	switch cmd {
	case "FA;", "FB;", "MD0;", "FT;", "CF000;", "CF001;", "PC;", "RG0;", "AG0;", "BI;", "AC;", "KS;", "ID;",
		MeterALC, MeterPO, MeterSWR:
		return true
	}
	return isControlQuery(cmd)
}

// State: what the rig panel shows.
type State struct {
	FreqA    int    `json:"freqA"` // Hz
	FreqB    int    `json:"freqB"`
	Mode     string `json:"mode"` // code
	ModeName string `json:"modeName"`
	Split    bool   `json:"split"` // transmitting on VFO-B
	ClarRX   bool   `json:"clarRx"`
	ClarTX   bool   `json:"clarTx"`
	ClarHz   int    `json:"clarHz"`
	PowerW   int    `json:"powerW"`
	RXFreq   int    `json:"rxFreq"` // where the rig listens, clarifier included
	TXFreq   int    `json:"txFreq"` // where it transmits
	RFGain   int    `json:"rfGain"` // RF gain, 0-255 (255 = full; the front-panel RF/SQL knob)
	AFGain   int    `json:"afGain"` // AF gain (volume), 0-255
}

// ParseState reads the answers to StateQueries.
func ParseState(a []string) (State, error) {
	var s State
	if len(a) != len(StateQueries) {
		return s, fmt.Errorf("expected %d answers, got %d", len(StateQueries), len(a))
	}
	var err error
	if s.FreqA, err = intAfter(a[0], "FA"); err != nil {
		return s, err
	}
	if s.FreqB, err = intAfter(a[1], "FB"); err != nil {
		return s, err
	}
	m, err := after(a[2], "MD0")
	if err != nil || len(m) != 1 {
		return s, fmt.Errorf("unexpected mode answer %q", a[2])
	}
	s.Mode, s.ModeName = m, ModeName(m)
	ft, err := after(a[3], "FT")
	if err != nil || ft == "" {
		return s, fmt.Errorf("unexpected TX VFO answer %q", a[3])
	}
	s.Split = ft[0] == '1'
	cf, err := after(a[4], "CF000")
	if err != nil || len(cf) < 2 {
		return s, fmt.Errorf("unexpected clarifier answer %q", a[4])
	}
	s.ClarRX, s.ClarTX = cf[0] == '1', cf[1] == '1'
	if s.ClarHz, err = intAfter(a[5], "CF001"); err != nil {
		return s, err
	}
	if s.PowerW, err = intAfter(a[6], "PC"); err != nil {
		return s, err
	}
	if s.RFGain, err = gainAfter(a[7], "RG0"); err != nil {
		return s, err
	}
	if s.AFGain, err = gainAfter(a[8], "AG0"); err != nil {
		return s, err
	}
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
	return s, nil
}

// MeterRaw: the 0-255 reading in an RM answer ("RM6045000;" -> 45). Some firmware sends
// six digits; the first three are the reading (as Hamlib reads it).
func MeterRaw(answer, query string) (int, error) {
	v, err := after(answer, strings.TrimSuffix(query, ";"))
	if err != nil || len(v) < 3 {
		return 0, fmt.Errorf("unexpected meter answer %q", answer)
	}
	n, err := strconv.Atoi(v[:3])
	if err != nil || n < 0 || n > 255 {
		return 0, fmt.Errorf("unexpected meter answer %q", answer)
	}
	return n, nil
}

// calibration: raw meter reading -> value, interpolated between points.
type calPoint struct{ raw, val float64 }

// FT-710 calibrations from Hamlib's ft710.h (SWR measured by G3VPX; power from FLRig).
var (
	swrCal = []calPoint{{0, 1.0}, {26, 1.2}, {52, 1.5}, {89, 2.0}, {126, 3.0}, {173, 4.0}, {236, 5.0}, {255, 25.0}}
	poCal  = []calPoint{{0, 0}, {27, 5}, {94, 25}, {147, 50}, {176, 75}, {205, 100}}
)

func interp(cal []calPoint, raw int) float64 {
	x := float64(raw)
	if x <= cal[0].raw {
		return cal[0].val
	}
	i := sort.Search(len(cal), func(i int) bool { return cal[i].raw >= x })
	if i >= len(cal) {
		return cal[len(cal)-1].val
	}
	a, b := cal[i-1], cal[i]
	return a.val + (b.val-a.val)*(x-a.raw)/(b.raw-a.raw)
}

// SWR from an RM6 reading.
func SWR(raw int) float64 { return interp(swrCal, raw) }

// Watts from an RM5 reading.
func Watts(raw int) float64 { return interp(poCal, raw) }

// ALC from an RM4 reading, 0-100 %.
func ALC(raw int) float64 { return float64(raw) * 100 / 255 }

// ---- helpers ----

func b01(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func after(answer, prefix string) (string, error) {
	answer = strings.TrimSpace(answer)
	if !strings.HasPrefix(answer, prefix) || !strings.HasSuffix(answer, ";") {
		return "", fmt.Errorf("expected %s...; got %q", prefix, answer)
	}
	return strings.TrimSuffix(strings.TrimPrefix(answer, prefix), ";"), nil
}

func gainAfter(answer, prefix string) (int, error) {
	n, err := intAfter(answer, prefix)
	if err == nil && (n < 0 || n > MaxGain) {
		err = fmt.Errorf("unexpected gain answer %q", answer)
	}
	return n, err
}

func intAfter(answer, prefix string) (int, error) {
	v, err := after(answer, prefix)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v) // handles the clarifier's sign
	if err != nil {
		return 0, fmt.Errorf("unexpected answer %q", answer)
	}
	return n, nil
}
