package main

// The engine: everything the Windows app does on its UI thread, behind one mutex.
// Audio arrives from the browser page; decoded text, spectrum and status go back out
// as server-sent events.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/deepcw"
	"github.com/Jnubbz/cw-console/internal/appdata"
	"github.com/Jnubbz/cw-console/ops"
)

const (
	specLo, specHi, specStep = 200, 1400, 10
	pitchMin, pitchMax       = 300, 1200
	breakAfter               = 2500 * time.Millisecond
	historyCap               = 200_000 // transcript bytes kept for page reloads
	txMuteHang               = 400 * time.Millisecond
)

// Settings persist across launches (JSON in the app's support folder).
type Settings struct {
	Pitch        float64        `json:"pitch"`
	Sensitivity  float64        `json:"sensitivity"`
	Improved     bool           `json:"improved"`
	Decoder      string         `json:"decoder"` // "classic" | "ai"
	AISpeed      int            `json:"aiSpeed"`
	AIFocus      bool           `json:"aiFocus"`
	WPM          int            `json:"wpm"`
	TxMode       string         `json:"txMode"` // "type" | "enter"
	TxPort       string         `json:"txPort"`
	TxLine       string         `json:"txLine"` // "dtr" | "rts" | "cat" (radio's keyer) | "audio" (a tone, rig in USB)
	AudioDevice  string         `json:"audioDevice"`
	Ops          ops.Config     `json:"ops"`
	Freq         string         `json:"freq"`      // what the operator typed: "14.058", "20m"...
	Mode         string         `json:"mode"`      // "cw" | "ft8" (FT8 on the Pi box only)
	FT8Depth     int            `json:"ft8Depth"`  // WSJT-X decode depth: 1 fast, 2 normal, 3 deep
	FT8TxFreq    int            `json:"ft8TxFreq"` // audio Hz
	FT8Level     int            `json:"ft8Level"`  // TX level 0-100 (dB = (L-100)*0.6), FT8 and CW as a tone
	FT8Out       string         `json:"ft8Out"`    // audio format to the rig: "" (auto) | "plug" (48k mono) | "hw48" | "hw44"
	FT8CQPota    bool           `json:"ft8CQPota"`
	FT8AutoCQ    bool           `json:"ft8AutoCQ"`
	FT8NoAutoLog bool           `json:"ft8NoAutoLog"` // don't log finished contacts by themselves (Log QSO does)
	FT8Grid      string         `json:"ft8Grid"`      // "" = MYGRID from the macros
	FT8PTTLine   string         `json:"ft8PttLine"`   // PTT for audio (FT8, CW as a tone): "dtr" | "rts" | "cat"
	FT8Band      string         `json:"ft8Band"`      // "20m"...
	CWBand       string         `json:"cwBand"`       // last CW band picked in the list
	RigCtl       bool           `json:"rigCtl"`       // switch the rig's mode/frequency over CAT
	RigPrevMode  string         `json:"rigPrevMode"`  // CAT mode code to go back to after FT8
	RigPrevHz    int            `json:"rigPrevHz"`
	CATType      string         `json:"catType"` // the radio's CAT commands: "" auto | "yaesu" | "kenwood"
	CATPort      string         `json:"catPort"` // Yaesu CAT port (Enhanced COM); "" = find it
	Park         string         `json:"park"`    // POTA reference being activated ("" = PARK from the macros, if set)
	Radio        string         `json:"radio"`   // current radio profile
	Radios       []RadioProfile `json:"radios"`  // the radio-specific settings, per radio
}

func defaultSettings() Settings {
	return Settings{Pitch: 600, Sensitivity: 0.5, Improved: true, Decoder: "classic",
		AISpeed: int(deepcw.SpeedAuto), AIFocus: true, WPM: 20, TxMode: "type", TxLine: "dtr", FT8PTTLine: "dtr",
		Ops: ops.DefaultConfig(), Mode: "cw", FT8Depth: 3, FT8TxFreq: 1500, FT8Level: 50,
		FT8CQPota: true, FT8AutoCQ: true, RigCtl: true}
}

type Engine struct {
	mu sync.Mutex
	st Settings

	rate int
	dec  *cw.StreamDecoder
	hist []float32
	spec []float32
	freq []float32

	// transcript
	text     strings.Builder
	atBreak  bool
	pending  bool
	lastChar time.Time

	// AI
	model     *deepcw.Model
	live      *deepcw.Live
	aiLoading bool
	aiErr     string
	onBattery bool

	tx txEngine

	// operating aids
	assist  *ops.Assist
	logbook *ops.Logbook
	call    string // the Call box
	callSeq int64  // bumps when the app changes the Call box (auto-reply, after logging)

	note      string
	noteUntil time.Time
	lastAudio time.Time

	hub *hub

	lastClassic time.Time

	// appliance (Pi box) only
	cap            capture
	box            boxConfig
	boxScan        time.Time
	boxDevs        []audioDev
	restartCapture bool // a radio switch: listen on its sound card
	radioGone      int  // box scans the current radio's port has been missing
	boxLogs        []string
	boxTemp        float64
	boxPower       powerState
	ft8            ft8Rx
	ft8tx          ft8Tx
	rig            rigCtl

	rp rigPanel // FT-710 rig panel and SWR sweep (Mac and Pi box)
}

func newEngine(h *hub) *Engine {
	e := &Engine{st: defaultSettings(), hub: h, atBreak: true}
	e.loadSettings()
	for f := float32(specLo); f <= specHi; f += specStep {
		e.freq = append(e.freq, f)
	}
	e.spec = make([]float32, len(e.freq))
	e.assist = ops.NewAssist()
	e.assist.Cfg = e.st.Ops
	e.logbook = ops.OpenLog(logDir())
	e.tx.init(e)
	e.ft8.onDecodes = e.ft8tx.heard
	e.ft8.skip = e.ft8tx.transmittedIn
	e.ft8tx.onLog = e.logFT8
	e.ft8tx.pauseRx = e.cap.setPaused
	if e.st.Decoder == "ai" {
		e.ensureModelLocked()
	}
	return e
}

// ---- paths ----

func supportDir() string {
	if dataDir != "" {
		return dataDir
	}
	d, err := os.UserConfigDir() // ~/Library/Application Support on macOS
	if err != nil {
		return "."
	}
	if appliance {
		return filepath.Join(d, "CW Decoder") // the box keeps its folder (it has no CW Decoder next to it)
	}
	return appdata.Dir(d) // CW Console's own folder, started from a copy of CW Decoder's
}

func logDir() string { return filepath.Join(supportDir(), "Logs") }

func modelDirs() []string {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	return []string{
		filepath.Join(dir, "..", "Resources", "deepcw"), // inside the .app bundle
		filepath.Join(dir, "deepcw"),
		filepath.Join(dir, "..", "..", "third_party", "deepcw-engine"), // dev tree
		"third_party/deepcw-engine",
		"/usr/share/cw-decoder/deepcw", // the Pi box
	}
}

// ---- settings ----

func (e *Engine) loadSettings() {
	b, err := os.ReadFile(filepath.Join(supportDir(), "mac-settings.json"))
	if err != nil {
		e.ensureRadiosLocked()
		return
	}
	s := defaultSettings()
	if json.Unmarshal(b, &s) == nil {
		s.Pitch = math.Max(pitchMin, math.Min(pitchMax, s.Pitch))
		s.Sensitivity = math.Max(0, math.Min(1, s.Sensitivity))
		if s.WPM < 5 || s.WPM > 50 {
			s.WPM = 20
		}
		s.Ops.Clamp()
		if s.Mode != "ft8" || !appliance {
			s.Mode = "cw"
		}
		clampFT8(&s)
		e.st = s
	}
	e.ensureRadiosLocked()
}

func (e *Engine) saveSettingsLocked() {
	e.syncRadioLocked() // the current radio's profile follows every change
	os.MkdirAll(supportDir(), 0o755)
	b, _ := json.MarshalIndent(e.st, "", "  ")
	os.WriteFile(filepath.Join(supportDir(), "mac-settings.json"), b, 0o644)
}

// applySettings merges a partial update from the page.
func (e *Engine) applySettings(raw json.RawMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.st
	json.Unmarshal(raw, &e.st)
	s := &e.st
	s.Radio, s.Radios = old.Radio, old.Radios // profiles change through radioAction only
	s.Pitch = math.Round(math.Max(pitchMin, math.Min(pitchMax, s.Pitch))/5) * 5
	s.Sensitivity = math.Max(0, math.Min(1, s.Sensitivity))
	if s.WPM < 5 {
		s.WPM = 5
	} else if s.WPM > 50 {
		s.WPM = 50
	}
	if s.AISpeed < 0 || s.AISpeed >= len(deepcw.SpeedNames) {
		s.AISpeed = int(deepcw.SpeedAuto)
	}
	s.Ops.Clamp()
	if s.Mode != "ft8" || !appliance {
		s.Mode = "cw"
	}
	if s.FT8Depth < 1 || s.FT8Depth > 3 {
		s.FT8Depth = 3
	}
	clampFT8(s)
	if s.Mode == "ft8" && old.Mode != "ft8" {
		// The CW keyer and Auto-CQ stand down (FT8 keys the rig itself, when asked).
		e.setAutoCQLocked(false)
		e.tx.closeLocked()
	}
	if s.Mode != "ft8" && old.Mode == "ft8" {
		e.ft8tx.enable(false, "", "")
		e.rigToCW()
	}
	if s.Mode == "ft8" && old.Mode != "ft8" {
		e.rigToFT8(s.FT8Band)
	} else if s.Mode == "ft8" && s.FT8Band != old.FT8Band && s.FT8Band != "" {
		e.rigToFT8(s.FT8Band)
	} else if s.Mode == "cw" && old.Mode == "cw" && s.CWBand != old.CWBand && s.CWBand != "" {
		e.rigToCWBand(s.CWBand)
	}
	if appliance && !s.RigCtl && (s.Mode != old.Mode || (s.FT8Band != old.FT8Band && s.FT8Band != "") ||
		(s.CWBand != old.CWBand && s.CWBand != "")) {
		e.setNoteLocked("Rig control is off (Setup > Radio), so the radio wasn't changed")
	}
	if len(s.Freq) > 20 {
		s.Freq = s.Freq[:20]
	}
	if p, err := ops.NormalizePark(s.Park); err != nil {
		s.Park = old.Park
		e.setNoteLocked("Park: " + err.Error())
	} else {
		s.Park = p
	}
	if appliance && s.TxPort != old.TxPort && s.AudioDevice == "" {
		e.restartCapture = true // Auto audio input: listen on the new port's radio
	}
	e.assist.Cfg = s.Ops
	if e.dec != nil {
		if s.Pitch != old.Pitch {
			e.dec.Retune(float32(s.Pitch))
		}
		e.dec.SetSensitivity(float32(s.Sensitivity))
		e.dec.SetImproved(s.Improved)
	}
	if s.Decoder != old.Decoder {
		if !e.atBreak {
			e.appendRawLocked("\n")
		}
		if s.Decoder == "ai" {
			e.ensureModelLocked()
		}
	}
	if e.tx.keyer != nil && s.WPM != old.WPM {
		e.tx.keyer.SetWPM(s.WPM)
	}
	e.applyAILocked()
	e.saveSettingsLocked()
}

// ---- audio ----

// pushAudio takes a block of receive audio from the page.
func (e *Engine) pushAudio(samples []float32, rate int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if rate < 4000 || rate > 192000 {
		return
	}
	if rate != e.rate || e.dec == nil {
		e.rate = rate
		e.dec = cw.NewStreamDecoder(float32(rate), float32(e.st.Pitch))
		e.dec.SetSensitivity(float32(e.st.Sensitivity))
		e.dec.SetImproved(e.st.Improved)
		e.hist = e.hist[:0]
	}
	e.lastAudio = time.Now()
	if e.st.Mode == "ft8" {
		e.ft8.push(samples, rate)
		return
	}
	dec := samples
	if e.tx.keyer != nil && e.tx.keyer.Active(txMuteHang) {
		dec = make([]float32, len(samples)) // silence for the decoders while we transmit
	}
	e.dec.Push(dec)
	if t := e.dec.TakeText(); t != "" {
		if !ops.IsNoiseText(t) {
			e.lastClassic = time.Now() // the classic decoder is copying real CW, not static
		}
		if e.st.Decoder == "classic" {
			e.appendTextLocked(t)
			e.lastChar = time.Now()
		}
	}
	if e.live != nil && e.st.Decoder == "ai" {
		e.live.Push(dec, rate)
	}
	e.hist = append(e.hist, samples...)
	if keep := rate * 4; len(e.hist) > keep {
		e.hist = append(e.hist[:0], e.hist[len(e.hist)-keep:]...)
	}
}

func (e *Engine) audioRunning() bool { return time.Since(e.lastAudio) < time.Second }

// ---- transcript ----

func (e *Engine) appendRawLocked(s string) {
	if s == "" {
		return
	}
	e.text.WriteString(s)
	if e.text.Len() > historyCap {
		keep := e.text.String()[e.text.Len()-historyCap/2:]
		e.text.Reset()
		e.text.WriteString(keep)
		e.hub.broadcast(event{T: "history", S: keep})
	} else {
		e.hub.broadcast(event{T: "text", S: s})
	}
	e.atBreak = strings.HasSuffix(s, "\n")
}

func (e *Engine) appendTextLocked(t string) {
	if e.atBreak {
		e.assist.Rx(" ", time.Now()) // a line break separates words
		t = strings.TrimLeft(t, " ")
		if t == "" {
			return
		}
	}
	e.appendRawLocked(t)
	e.pending = true
	e.assist.Rx(t, time.Now())
}

func (e *Engine) markBreakLocked() {
	if !e.atBreak {
		e.appendRawLocked("\n")
	}
	e.pending = false
}

func (e *Engine) clearTranscript() {
	e.mu.Lock()
	e.text.Reset()
	e.atBreak, e.pending = true, false
	e.mu.Unlock()
	e.hub.broadcast(event{T: "history", S: ""})
}

func (e *Engine) setNoteLocked(s string) {
	e.note, e.noteUntil = s, time.Now().Add(5*time.Second)
}

// ---- AI ----

func (e *Engine) ensureModelLocked() {
	if e.model != nil || e.aiLoading {
		return
	}
	e.aiLoading, e.aiErr = true, ""
	go func() {
		var m *deepcw.Model
		var err error
		for _, d := range modelDirs() {
			if m, err = deepcw.Load(d); err == nil {
				break
			}
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.aiLoading = false
		if err != nil {
			e.aiErr = "AI model not found: " + err.Error()
			e.st.Decoder = "classic"
			e.setNoteLocked(e.aiErr)
			return
		}
		e.model = m
		e.live = deepcw.NewLive(m)
		e.applyAILocked()
		e.setNoteLocked("AI model ready (" + deepcw.SIMDName() + "). Text appears after 15 s of audio.")
	}()
}

func (e *Engine) planLocked() deepcw.Plan {
	return deepcw.PlanFor(deepcw.Speed(e.st.AISpeed), runtime.NumCPU(), e.onBattery)
}

func (e *Engine) applyAILocked() {
	if e.live == nil {
		return
	}
	e.live.SetEnabled(e.st.Decoder == "ai")
	e.live.SetPlan(e.planLocked())
	if e.st.AIFocus {
		e.live.SetFocus(deepcw.DefaultFocus(e.st.Pitch))
	} else {
		e.live.SetFocus(nil)
	}
}

func (e *Engine) aiStatusLocked() string {
	switch {
	case e.aiLoading:
		return "Loading the AI model..."
	case e.live == nil:
		return "AI model not loaded"
	}
	st, pass := e.live.Status()
	p := e.planLocked()
	mode := deepcw.SpeedNames[e.st.AISpeed]
	if deepcw.Speed(e.st.AISpeed) == deepcw.SpeedAuto {
		mode = "Auto: Balanced"
		if e.onBattery {
			mode = "Auto: Eco (on battery)"
		}
	}
	s := "AI " + st + " | " + mode + ", " + itoa(p.Threads) + " thread(s)"
	if pass > 0 {
		s += " | pass " + ftoa(pass.Seconds(), 2) + " s, new text every ~" + ftoa(p.Interval(pass).Seconds(), 1) + " s"
	}
	return s
}

// ---- spectrum / autotune ----

func (e *Engine) updateSpectrumLocked() {
	if e.rate == 0 {
		return
	}
	n := e.rate * 3 / 10
	if len(e.hist) < n {
		return
	}
	p := cw.Spectrum(e.hist[len(e.hist)-n:], float32(e.rate), e.freq)
	med := median(p)
	for i, v := range p {
		db := float32(10 * math.Log10(float64(v/med)+1e-12))
		db = float32(math.Max(0, math.Min(40, float64(db))))
		if db > e.spec[i] {
			e.spec[i] = db
		} else {
			e.spec[i] = e.spec[i]*0.6 + db*0.4
		}
	}
}

func (e *Engine) autoTune() (float64, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rate == 0 || len(e.hist) < e.rate {
		return e.st.Pitch, "Auto tune needs a second or two of audio. Press Start and let it listen."
	}
	f, ratio := cw.FindPitch(e.hist, float32(e.rate), pitchMin, pitchMax, 5)
	if ratio < 6 {
		return e.st.Pitch, "No clear CW tone found. Click the signal in the spectrum instead."
	}
	e.st.Pitch = math.Round(float64(f)/5) * 5
	if e.dec != nil {
		e.dec.Retune(float32(e.st.Pitch))
	}
	e.applyAILocked()
	e.saveSettingsLocked()
	return e.st.Pitch, ""
}

// ---- file decode ----

func (e *Engine) decodeFile(name string, data []byte) {
	samples, rate, err := cw.ParseWAV(data)
	if err != nil {
		e.mu.Lock()
		e.setNoteLocked("Could not read " + name + ": " + err.Error())
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	sr := float32(rate)
	head := samples
	if n := int(sr * 60); len(head) > n {
		head = head[:n]
	}
	pitch := float32(e.st.Pitch)
	if f, ratio := cw.FindPitch(head, sr, pitchMin, pitchMax, 5); ratio >= 6 {
		pitch = f
	}
	useAI, model, focusOn := e.st.Decoder == "ai", e.model, e.st.AIFocus
	sens, improved := e.st.Sensitivity, e.st.Improved
	e.setNoteLocked("Decoding " + name + "...")
	e.mu.Unlock()

	var text, how string
	if useAI && model != nil {
		var focus *deepcw.Focus
		if focusOn {
			focus = deepcw.DefaultFocus(float64(pitch))
		}
		deepcw.Exclusive(runtime.NumCPU(), func() {
			text, err = deepcw.DecodeStream(model, samples, int(rate), 2, focus)
		})
		how = "AI"
	} else {
		d := cw.NewStreamDecoder(sr, pitch)
		d.SetSensitivity(float32(sens))
		d.SetImproved(improved)
		var out strings.Builder
		for i := 0; i < len(samples); i += int(sr) {
			d.Push(samples[i:min(i+int(sr), len(samples))])
			out.WriteString(d.TakeText())
		}
		d.Push(make([]float32, int(sr*1.5)))
		out.WriteString(d.TakeText())
		text, how = strings.TrimSpace(out.String()), "classic"
	}
	if err != nil {
		text = "decode failed: " + err.Error()
	}
	if text == "" {
		text = "(nothing decoded)"
	}
	e.mu.Lock()
	e.markBreakLocked()
	e.appendRawLocked("--- " + name + "  (" + how + ", " + itoa(int(pitch)) + " Hz) ---\n" + text + "\n")
	e.mu.Unlock()
}

// ---- ticker: line breaks, AI text, TX feeding, state broadcast ----

func (e *Engine) run() {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	n := 0
	powerAt := time.Time{}
	for range tick.C {
		n++
		e.mu.Lock()
		e.tx.tickLocked()
		e.assistTickLocked()
		e.ft8.setOn(appliance && e.st.Mode == "ft8", e.st.FT8Depth, e.tx.ctx.MyCall)
		if appliance {
			e.ft8tx.configure(e.cap.device(), e.st.FT8TxFreq, ft8LevelGain(e.st.FT8Level), e.tx.ctx.MyCall,
				e.ft8GridLocked(), e.st.FT8CQPota, e.st.FT8AutoCQ)
			e.ft8tx.setOut(e.st.FT8Out)
			e.ft8tx.setCATType(e.st.CATType)
			e.ft8.setEarly(e.st.Mode == "ft8" && e.ft8tx.isEnabled())
			e.ft8.setPartner(e.ft8tx.partnerCall())
		}
		if e.live != nil {
			if t := e.live.TakeText(); t != "" && e.st.Decoder == "ai" {
				for i, part := range strings.Split(t, "\n") {
					if i > 0 {
						e.markBreakLocked()
					}
					e.appendTextLocked(part)
				}
			}
		}
		if e.st.Decoder == "classic" && e.pending && time.Since(e.lastChar) > breakAfter {
			e.appendRawLocked("\n")
			e.pending = false
		}
		if time.Since(powerAt) > 30*time.Second {
			powerAt = time.Now()
			if b := onBattery(); b != e.onBattery {
				e.onBattery = b
				e.applyAILocked()
			}
		}
		if n%4 == 0 {
			e.updateSpectrumLocked()
			ev := e.stateLocked()
			restart, dev := e.restartCapture, e.st.AudioDevice
			e.restartCapture = false
			e.mu.Unlock()
			if restart && appliance {
				go e.cap.start(e, dev)
			}
			e.hub.broadcast(ev)
			continue
		}
		e.mu.Unlock()
		e.ft8.tick()
		if appliance {
			e.ft8tx.tick(e.ft8.offsetNow())
		}
	}
}

func clampFT8(s *Settings) {
	if s.FT8Depth < 1 || s.FT8Depth > 3 {
		s.FT8Depth = 3
	}
	if s.FT8TxFreq < 200 || s.FT8TxFreq > 2900 {
		s.FT8TxFreq = 1500
	}
	s.FT8Level = max(0, min(100, s.FT8Level))
	if s.CATType != "yaesu" && s.CATType != "kenwood" {
		s.CATType = ""
	}
	if s.FT8Out != "hw48" && s.FT8Out != "hw44" && s.FT8Out != "plug" {
		s.FT8Out = ""
	}
	if s.TxLine != "rts" && s.TxLine != "cat" && s.TxLine != "audio" {
		s.TxLine = "dtr"
	}
	s.FT8PTTLine = effectivePTT(s.TxLine, s.FT8PTTLine) // no more "same as CW"
	if s.FT8Band != "" && ft8BandHz(s.FT8Band) == 0 {
		s.FT8Band = ""
	}
	if s.CWBand != "" && cwBandHz(s.CWBand) == 0 {
		s.CWBand = ""
	}
	s.FT8Grid = strings.ToUpper(strings.TrimSpace(s.FT8Grid))
	if len(s.FT8Grid) > 6 {
		s.FT8Grid = s.FT8Grid[:6]
	}
}

// ft8GridLocked: the grid FT8 sends.
func (e *Engine) ft8GridLocked() string {
	if e.st.FT8Grid != "" {
		return e.st.FT8Grid
	}
	return e.tx.ctx.MyGrid
}

// logFT8: the sequencer finished a contact (manual: Log QSO was pressed).
func (e *Engine) logFT8(l *ft8Log, manual bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !manual && e.st.FT8NoAutoLog {
		e.setNoteLocked("Contact with " + l.Call + " complete: press Log QSO to log it")
		return
	}
	freq, band := ops.ParseFreq(e.st.Freq)
	ctx := e.tx.ctx
	q := ops.QSO{Call: l.Call, Start: time.Now(), Freq: freq, Band: band, RSTSent: l.Sent, RSTRcvd: l.Rcvd,
		MyCall: ctx.MyCall, Park: e.parkLocked(), MyState: ctx.MyState, MyGrid: e.ft8GridLocked(),
		Mode: "FT8", Grid: l.Grid}
	dupe, err := e.logbook.Add(q)
	if err != nil {
		e.setNoteLocked("couldn't write the log: " + err.Error())
		return
	}
	e.ft8tx.noteLogged(l.Call)
	msg := "Logged " + l.Call + " (FT8)"
	if dupe {
		msg += " (dupe)"
	}
	if band == "" {
		msg += ". No band: type the dial frequency in Freq."
	}
	e.setNoteLocked(msg)
}

func (e *Engine) stateLocked() event {
	s := &stateMsg{Settings: e.st, Spec: e.spec, Running: e.audioRunning(),
		FocusWidth: deepcw.FocusWidth, SIMD: deepcw.SIMDName()}
	if e.dec != nil {
		s.Key = e.dec.KeyDown()
		s.Signal = e.dec.Present()
		s.WPM = int(e.dec.WPM())
	}
	if e.st.Decoder == "ai" {
		s.Status = e.aiStatusLocked()
	}
	if time.Now().Before(e.noteUntil) {
		s.Note = e.note
	}
	e.tx.fillStateLocked(s)
	e.fillOpsLocked(s)
	if appliance {
		on, dev, err, want := e.cap.status()
		if time.Since(e.boxScan) > 2*time.Second { // cheap, but no need 10x a second
			e.boxScan, e.boxDevs, e.boxLogs, e.boxTemp, e.boxPower = time.Now(), listAudioDevs(), logFiles(), cpuTempC(), readPower()
			e.autoRadioLocked()
		}
		s.Appliance = &boxState{CaptureOn: on, Device: dev, Want: want, Error: err, Devices: e.boxDevs,
			TempC: e.boxTemp, Power: e.boxPower, WifiName: e.box.WifiName, DefaultPassword: e.box.isDefault(), Logs: e.boxLogs}
		s.Running = s.Running && dev != ""
		s.FT8 = e.ft8.state()
		s.FT8.Tx = e.ft8tx.state()
		s.MyCall = e.tx.ctx.MyCall
		s.MyGrid = e.tx.ctx.MyGrid
	}
	return event{T: "state", State: s}
}

type stateMsg struct {
	Settings   Settings  `json:"settings"`
	Spec       []float32 `json:"spec"`
	Running    bool      `json:"running"`
	Key        bool      `json:"key"`
	Signal     bool      `json:"signal"`
	WPM        int       `json:"wpm"`
	Status     string    `json:"status"`
	Note       string    `json:"note"`
	FocusWidth float64   `json:"focusWidth"`
	SIMD       string    `json:"simd"`
	// sender
	TxOn     bool     `json:"txOn"`
	TxActive bool     `json:"txActive"`
	TxSent   string   `json:"txSent"`
	TxQueue  string   `json:"txQueue"`
	TxPort   string   `json:"txPortOpen"`
	Macros   []string `json:"macros"`
	QueueSeq int64    `json:"queueSeq"`
	// operating aids
	AutoCQ    bool      `json:"autoCQ"`
	OpsStatus string    `json:"opsStatus"`
	Calls     []string  `json:"calls"`
	Call      string    `json:"call"`
	CallSeq   int64     `json:"callSeq"`
	Dupe      bool      `json:"dupe"`
	TheirWPM  int       `json:"theirWPM"`
	SendWPM   int       `json:"sendWPM"`
	Band      string    `json:"band"`
	Act       actState  `json:"act"`
	Appliance *boxState `json:"appliance,omitempty"`
	FT8       *ft8State `json:"ft8,omitempty"`
	MyCall    string    `json:"myCall"`
	MyGrid    string    `json:"myGrid"`
}

type boxState struct {
	CaptureOn       bool       `json:"captureOn"`
	Device          string     `json:"device"`
	Want            string     `json:"want"`
	Error           string     `json:"error"`
	Devices         []audioDev `json:"devices"`
	TempC           float64    `json:"tempC"`
	Power           powerState `json:"power"`
	WifiName        string     `json:"wifiName"`
	DefaultPassword bool       `json:"defaultPassword"`
	Logs            []string   `json:"logs"`
}

func median(p []float32) float32 {
	s := append([]float32(nil), p...)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	m := s[len(s)/2]
	if m < 1e-12 {
		m = 1e-12
	}
	return m
}

// ft8LevelGain: the page's Level (0-100) as an amplitude: decibels, like WSJT-X's Pwr
// slider, 0.6 dB a step (100 = full scale, 50 = -30 dB, 0 = silence). A straight
// percentage put all the rig's working range in the bottom few percent.
func ft8LevelGain(level int) float64 {
	if level <= 0 {
		return 0
	}
	return math.Pow(10, ft8LevelDB(level)/20)
}

func ft8LevelDB(level int) float64 { return float64(min(level, 100)-100) * 0.6 }
