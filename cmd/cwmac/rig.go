package main

// Rig control over CAT: switching POTA Pi between CW and FT8 also switches the rig (USB
// data mode on the FT8 frequency, then back to the CW mode and frequency it was on), and
// the band lists move the rig. Two command sets:
//
//   Yaesu (FT-710 and most current Yaesus): MD0<code>; FA<9 digits>; 38400 8N1, on the
//   rig's first USB serial port (FT-710 "Enhanced COM", CP2105 if00) next to the key
//   port (if01). DATA-U = MD0C, CW-U = 3, CW-L = 7.
//   Kenwood (TS-590S, which the FX-4CR emulates): MD<code>; DA<0|1>; FA<11 digits>;
//   115200 on the radio's one USB serial port (the key port). USB = MD2, data = DA1,
//   CW = MD3, CW-R = MD7. PTT: TX1; (data send: the USB audio), RX; to receive.

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/Jnubbz/cw-console/ops"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

var catBaud = 38400 // Yaesu

// catSpec: how to reach a radio's CAT.
type catSpec struct {
	Port    string
	Baud    int
	Kenwood bool
}

// catSpecFor: from the key port and the profile's CAT type ("" = auto: a CH340 port,
// like the FX-4CR's, is Kenwood; anything else is Yaesu).
func catSpecFor(keyPort, catType string) catSpec {
	kenwood := catType == "kenwood" || (catType == "" && strings.Contains(keyPort, "1a86"))
	if kenwood {
		return catSpec{Port: keyPort, Baud: 115200, Kenwood: true}
	}
	return catSpec{Port: catPortFor(keyPort), Baud: catBaud}
}

// FT8 dial frequencies (WSJT-X defaults, IARU Region 2), Hz.
var ft8Bands = []struct {
	Band string
	Hz   int
}{
	{"160m", 1840000}, {"80m", 3573000}, {"60m", 5357000}, {"40m", 7074000}, {"30m", 10136000},
	{"20m", 14074000}, {"17m", 18100000}, {"15m", 21074000}, {"12m", 24915000}, {"10m", 28074000},
	{"6m", 50313000},
}

// CW frequencies: the middle of the US General CW segment, nudged off the digital
// window where the middle lands on it (40, 20, 12 m).
var cwBands = []struct {
	Band string
	Hz   int
}{
	{"160m", 1825000}, {"80m", 3560000}, {"40m", 7050000}, {"30m", 10120000}, {"20m", 14050000},
	{"17m", 18085000}, {"15m", 21110000}, {"12m", 24905000}, {"10m", 28150000}, {"6m", 50050000},
}

func cwBandHz(band string) int {
	for _, b := range cwBands {
		if b.Band == band {
			return b.Hz
		}
	}
	return 0
}

func ft8BandHz(band string) int {
	for _, b := range ft8Bands {
		if b.Band == band {
			return b.Hz
		}
	}
	return 0
}

// cwModeFor: CW on the lower sideband below 10 MHz (160/80/40 m), upper above, the usual
// convention (as LSB/USB). Yaesu: 7 = CW-L, 3 = CW-U. Kenwood: 7 = CW-R, 3 = CW.
func cwModeFor(hz int) string {
	if hz < 10000000 {
		return "7"
	}
	return "3"
}

// bandOfHz: the amateur band a frequency is in ("" if none).
func bandOfHz(hz int) string {
	_, band := parseFreqHz(hz)
	return band
}

func parseFreqHz(hz int) (string, string) {
	return ops.ParseFreq(strconv.FormatFloat(float64(hz)/1e6, 'f', 3, 64))
}

// catPortFor: the Yaesu CAT port that goes with a key port (by-id names: ...-if01-port0
// is the FT-710's second port, used for keying; ...-if00-port0 is CAT).
func catPortFor(keyPort string) string {
	if strings.Contains(keyPort, "-if01-") {
		return strings.Replace(keyPort, "-if01-", "-if00-", 1)
	}
	return ""
}

// ---- the two command sets ----

func (c catSpec) freqCmd(hz int) string {
	if c.Kenwood {
		return fmt.Sprintf("FA%011d;", hz)
	}
	return fmt.Sprintf("FA%09d;", hz)
}

// catFreq: Yaesu's form (kept for the FT-710 code paths and tests).
func catFreq(hz int) string { return catSpec{}.freqCmd(hz) }

// state queries; the mode comes back as the set's own code ("C" Yaesu DATA-U, "2D" Kenwood
// USB + data).
func (c catSpec) stateCmds() []string {
	if c.Kenwood {
		return []string{"MD;", "DA;", "FA;"}
	}
	return []string{"MD0;", "FA;"}
}

func (c catSpec) parseState(a []string) (mode string, hz int, err error) {
	if c.Kenwood {
		if len(a) < 3 || !strings.HasPrefix(a[0], "MD") || !strings.HasPrefix(a[1], "DA") || !strings.HasPrefix(a[2], "FA") {
			return "", 0, fmt.Errorf("unexpected CAT answer %q", a)
		}
		mode = strings.TrimSuffix(strings.TrimPrefix(a[0], "MD"), ";")
		if strings.TrimSuffix(strings.TrimPrefix(a[1], "DA"), ";") == "1" && (mode == "1" || mode == "2") {
			mode += "D"
		}
		hz, err = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(a[2], "FA"), ";"))
		return mode, hz, err
	}
	if len(a) < 2 || !strings.HasPrefix(a[0], "MD0") || !strings.HasPrefix(a[1], "FA") {
		return "", 0, fmt.Errorf("unexpected CAT answer %q", a)
	}
	mode = strings.TrimSuffix(strings.TrimPrefix(a[0], "MD0"), ";")
	hz, err = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(a[1], "FA"), ";"))
	return mode, hz, err
}

func (c catSpec) dataMode() string {
	if c.Kenwood {
		return "2D"
	}
	return "C"
}

// modeCmds: commands that put the rig in mode (a code from parseState / cwModeFor / dataMode).
func (c catSpec) modeCmds(mode string) []string {
	if !c.Kenwood {
		return []string{"MD0" + mode + ";"}
	}
	base, data := strings.TrimSuffix(mode, "D"), strings.HasSuffix(mode, "D")
	cmds := []string{"MD" + base + ";"}
	if base == "1" || base == "2" { // data mode only exists on the sidebands
		if data {
			cmds = append(cmds, "DA1;")
		} else {
			cmds = append(cmds, "DA0;")
		}
	}
	return cmds
}

// tuneCmds: mode, frequency (and Yaesu: the mode again, as the FT-710 can change it with
// the band).
func (c catSpec) tuneCmds(mode string, hz int) []string {
	cmds := append(c.modeCmds(mode), c.freqCmd(hz))
	if !c.Kenwood {
		cmds = append(cmds, c.modeCmds(mode)...)
	}
	return cmds
}

func (c catSpec) modeName(mode string) string {
	if c.Kenwood {
		if n, ok := map[string]string{"1": "LSB", "2": "USB", "1D": "LSB-DATA", "2D": "USB-DATA", "3": "CW", "7": "CW-R", "4": "FM", "5": "AM"}[mode]; ok {
			return n
		}
		return "mode " + mode
	}
	if n, ok := map[string]string{"7": "CW-L", "3": "CW-U", "1": "LSB", "2": "USB", "C": "DATA-U"}[mode]; ok {
		return n
	}
	return "mode " + mode
}

type rigCtl struct{ mu sync.Mutex }

// do sends CAT commands in order; queries (no parameters, like "FA;") return the answer.
func (r *rigCtl) do(c catSpec, cmds ...string) ([]string, error) {
	if c.Port == "" {
		return nil, errors.New("no CAT port (pick the radio's key port in Setup; on a Yaesu it must be its USB serial port)")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	conn, err := openCAT(c.Port, c.Baud)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var out []string
	for _, cmd := range cmds {
		if err := conn.write(cmd); err != nil {
			return out, err
		}
		if isCATQuery(cmd) {
			a, err := conn.readReply()
			if err != nil {
				return out, fmt.Errorf("the rig didn't answer %s over CAT (check its CAT rate is %d): %v", cmd, c.Baud, err)
			}
			out = append(out, a)
		}
	}
	return out, nil
}

func isCATQuery(cmd string) bool {
	switch cmd {
	case "FA;", "MD0;", "MD;", "DA;":
		return true
	}
	return ft710.IsQuery(cmd)
}

// state asks the rig for its mode code and VFO-A frequency (Hz).
func (r *rigCtl) state(c catSpec) (mode string, hz int, err error) {
	a, err := r.do(c, c.stateCmds()...)
	if err != nil {
		return "", 0, err
	}
	return c.parseState(a)
}

const catDataU, catCWL = "C", "7" // Yaesu codes (kept for the tests)

// ---- engine side ----

func (e *Engine) catLocked() catSpec { return catSpecFor(e.st.TxPort, e.st.CATType) }

// CW sent as audio (Key line: Audio tone) goes out through the rig's sideband
// transmitter: in USB the signal is a pitch above the dial (in LSB, below). So the band
// lists put the dial a pitch below the CW frequency, and the log gets the frequency the
// signal is on.
func audioCWDial(hz int, pitch float64) int { return hz - int(math.Round(pitch)) }

func audioCWSignal(mode string, dial int, pitch float64) int {
	switch mode {
	case "2", "2D", "C": // USB, USB data (Kenwood), DATA-U (Yaesu)
		return dial + int(math.Round(pitch))
	case "1", "1D":
		return dial - int(math.Round(pitch))
	}
	return dial
}

func mhzStr(hz int) string { return strconv.FormatFloat(float64(hz)/1e6, 'f', 3, 64) }

// rigToCWBand: the rig to a band's CW frequency, on the band's usual CW sideband (CW as
// audio: USB, the dial a pitch below).
func (e *Engine) rigToCWBand(band string) {
	if !appliance || cwBandHz(band) == 0 {
		return
	}
	go func() {
		e.mu.Lock()
		c, on, audio, pitch := e.catLocked(), e.st.RigCtl, e.st.TxLine == "audio", e.st.Pitch
		e.mu.Unlock()
		if !on {
			return
		}
		hz := cwBandHz(band)
		mode, dial := cwModeFor(hz), hz
		if audio {
			mode, dial = "2", audioCWDial(hz, pitch)
		}
		if _, err := e.rig.do(c, c.tuneCmds(mode, dial)...); err != nil {
			e.noteRig(err)
			return
		}
		e.mu.Lock()
		e.st.Freq = mhzStr(hz)
		e.saveSettingsLocked()
		if audio {
			e.setNoteLocked("Rig: USB, dial " + strconv.FormatFloat(float64(dial)/1e6, 'f', 4, 64) +
				" MHz, so your CW tone goes out on " + e.st.Freq + " MHz (" + band + ")")
		} else {
			e.setNoteLocked("Rig: " + c.modeName(mode) + ", " + e.st.Freq + " MHz (" + band + ")")
		}
		e.mu.Unlock()
	}()
}

// rigToAudioCW: CW as audio needs the rig in a sideband mode (in CW mode a radio doesn't
// transmit its USB audio). With rig control on and the rig in CW, switch it to USB with
// the dial a pitch lower, so the signal stays on the frequency it was on.
func (e *Engine) rigToAudioCW() {
	if !appliance {
		return
	}
	go func() {
		e.mu.Lock()
		c, on, pitch := e.catLocked(), e.st.RigCtl, e.st.Pitch
		e.mu.Unlock()
		if !on {
			return
		}
		mode, hz, err := e.rig.state(c)
		if err != nil {
			e.noteRig(err)
			return
		}
		if mode != "3" && mode != "7" { // CW, CW-R (Kenwood) / CW-U, CW-L (Yaesu)
			return
		}
		dial := audioCWDial(hz, pitch)
		if _, err := e.rig.do(c, c.tuneCmds("2", dial)...); err != nil {
			e.noteRig(err)
			return
		}
		e.mu.Lock()
		e.st.Freq = mhzStr(hz)
		e.saveSettingsLocked()
		e.setNoteLocked("Rig: USB (CW as audio needs it), dial " + strconv.FormatFloat(float64(dial)/1e6, 'f', 4, 64) +
			" MHz, so your CW still goes out on " + e.st.Freq + " MHz")
		e.mu.Unlock()
	}()
}

// rigToFT8: remember where the rig is (if it isn't on FT8 already), then USB data mode on
// the FT8 frequency of band ("" = the band it's on now; 20 m if it isn't on a band).
func (e *Engine) rigToFT8(band string) {
	if !appliance {
		return
	}
	go func() {
		e.mu.Lock()
		c, on := e.catLocked(), e.st.RigCtl
		e.mu.Unlock()
		if !on {
			return
		}
		mode, hz, err := e.rig.state(c)
		if err != nil {
			e.noteRig(err)
			return
		}
		e.mu.Lock()
		if mode != c.dataMode() && e.st.RigPrevMode == "" { // where to go back to
			e.st.RigPrevMode, e.st.RigPrevHz = mode, hz
		}
		if band == "" {
			band = bandOfHz(hz)
		}
		if ft8BandHz(band) == 0 {
			band = "20m"
		}
		target := ft8BandHz(band)
		e.st.FT8Band = band
		e.saveSettingsLocked()
		e.mu.Unlock()
		if _, err := e.rig.do(c, c.tuneCmds(c.dataMode(), target)...); err != nil {
			e.noteRig(err)
			return
		}
		e.mu.Lock()
		e.st.Freq = strconv.FormatFloat(float64(target)/1e6, 'f', 3, 64)
		e.saveSettingsLocked()
		e.setNoteLocked("Rig: " + c.modeName(c.dataMode()) + ", " + e.st.Freq + " MHz (" + band + " FT8)")
		e.mu.Unlock()
	}()
}

// rigToCW: back to the mode and frequency the rig was on before FT8.
func (e *Engine) rigToCW() {
	if !appliance {
		return
	}
	go func() {
		e.mu.Lock()
		c, on := e.catLocked(), e.st.RigCtl
		mode, hz := e.st.RigPrevMode, e.st.RigPrevHz
		audio, pitch := e.st.TxLine == "audio", e.st.Pitch
		e.mu.Unlock()
		if !on {
			return
		}
		if mode == "" {
			mode = cwModeFor(hz)
			if hz == 0 {
				mode = "7"
			}
		}
		if audio && (mode == "3" || mode == "7") { // CW as audio: USB, same signal frequency
			mode = "2"
			if hz > 0 {
				hz = audioCWDial(hz, pitch)
			}
		}
		cmds := c.modeCmds(mode)
		if hz > 0 {
			cmds = append(cmds, c.freqCmd(hz))
		}
		if _, err := e.rig.do(c, cmds...); err != nil {
			e.noteRig(err)
			return
		}
		e.mu.Lock()
		e.st.RigPrevMode, e.st.RigPrevHz = "", 0
		msg := "Rig: back to " + c.modeName(mode)
		if hz > 0 {
			sig := hz
			if audio {
				sig = audioCWSignal(mode, hz, pitch)
			}
			e.st.Freq = mhzStr(sig)
			msg += ", " + e.st.Freq + " MHz"
		}
		e.saveSettingsLocked()
		e.setNoteLocked(msg)
		e.mu.Unlock()
	}()
}

func (e *Engine) noteRig(err error) {
	e.mu.Lock()
	e.setNoteLocked("Rig control: " + err.Error())
	e.mu.Unlock()
}

// raw sends one CAT command and returns the answer ("(no answer)" if there's none), for
// the CAT console in Setup.
func (r *rigCtl) raw(c catSpec, cmd string) (string, error) {
	if c.Port == "" {
		return "", errors.New("no CAT port (pick the radio's key port in Setup)")
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || len(cmd) > 64 || strings.ContainsAny(cmd, "\r\n") {
		return "", errors.New("one command, up to 64 characters")
	}
	if !strings.HasSuffix(cmd, ";") {
		cmd += ";"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	conn, err := openCAT(c.Port, c.Baud)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.write(cmd); err != nil {
		return "", err
	}
	a, err := conn.readReply()
	if err != nil {
		if strings.Contains(err.Error(), "rejected") {
			return "?; (the radio doesn't know this command)", nil
		}
		return "(no answer)", nil
	}
	return a, nil
}
