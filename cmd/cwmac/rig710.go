package main

// The Yaesu rig panel (FT-710, FT-991A): VFO A/B, split, clarifier, power, tuner, band
// keys and an SWR sweep, over CAT (the radio's Enhanced COM port). Works on the Mac and the
// Pi box. CW itself still goes over the key line (DTR/RTS on the Standard COM port); the
// sweep borrows that line for its short carriers.
//
// The CAT port: Setup's "CAT port" if set; on Linux, the key port's partner by its
// by-id name; otherwise found by asking each serial port "ID;". Which radio it is, and its
// CAT rate, come from that answer: the FT-710 ships at 38400, the FT-991A at 4800, so each
// rate is tried until a known Yaesu ID comes back.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/rig"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

type rigPanel struct {
	mu        sync.Mutex
	autoPort  string       // found by probing (cleared when it stops answering)
	model     *ft710.Model // which radio is on identPort (nil: not asked yet)
	identPort string
	identBaud int
	failPort  string    // where the last identify or port search found nothing,
	failAt    time.Time // and when (the meter poll asks often; a radio that's off
	failErr   error     // isn't re-probed at every rate each time)
	sweep     sweepState
	cancel    context.CancelFunc
	done      chan struct{} // closed after the sweep has unkeyed
}

type sweepState struct {
	Running bool          `json:"running"`
	Band    string        `json:"band"`
	FromHz  int           `json:"fromHz"`
	ToHz    int           `json:"toHz"`
	StepHz  int           `json:"stepHz"`
	PowerW  int           `json:"powerW"`
	Points  []ft710.Point `json:"points"`
	Best    *ft710.Point  `json:"best,omitempty"`
	Error   string        `json:"error,omitempty"`
}

var errNotYaesu = errors.New("the rig panel is for the Yaesu FT-710 and FT-991A (this radio is set up as Kenwood CAT)")

// catBauds: the CAT rates to try, the FT-710's and FT-991A's factory rates first.
var catBauds = []int{38400, 4800, 9600, 19200}

// ft710Spec: where the Yaesu's CAT is, at its rate. May probe the serial ports (a second
// or two the first time).
func (e *Engine) ft710Spec() (catSpec, error) {
	p, err := e.yaesuPort()
	if err != nil {
		return catSpec{}, err
	}
	_, baud, err := e.identify(p)
	if err != nil {
		return catSpec{}, err
	}
	return catSpec{Port: p, Baud: baud}, nil
}

// identify: which Yaesu is on port, and at what CAT rate (asked once per port).
func (e *Engine) identify(port string) (*ft710.Model, int, error) {
	e.rp.mu.Lock()
	if e.rp.identPort == port && e.rp.model != nil {
		m, b := e.rp.model, e.rp.identBaud
		e.rp.mu.Unlock()
		return m, b, nil
	}
	if err := e.recentFailLocked(port); err != nil {
		e.rp.mu.Unlock()
		return nil, 0, err
	}
	e.rp.mu.Unlock()
	for _, baud := range catBauds {
		a, err := radioBus.exchange(port, baud, rig.PriCommand, []string{"ID;"})
		if err != nil || len(a) != 1 {
			continue
		}
		if m := ft710.ModelForID(a[0]); m != nil {
			e.rp.mu.Lock()
			e.rp.model, e.rp.identPort, e.rp.identBaud = m, port, baud
			e.rp.mu.Unlock()
			return m, baud, nil
		}
	}
	err := fmt.Errorf("no FT-710 or FT-991A answered on %s at 4800-38400 baud: is it on, and is this its Enhanced (CAT) port?", port)
	e.noteFail(port, err)
	return nil, 0, err
}

// probeBackoff: how long a port where no radio answered is left alone.
const probeBackoff = 10 * time.Second

func (e *Engine) recentFailLocked(port string) error {
	if e.rp.failPort == port && time.Since(e.rp.failAt) < probeBackoff {
		return e.rp.failErr
	}
	return nil
}

func (e *Engine) noteFail(port string, err error) {
	e.rp.mu.Lock()
	e.rp.failPort, e.rp.failAt, e.rp.failErr = port, time.Now(), err
	e.rp.mu.Unlock()
}

// rigModel: the radio last identified (the FT-710 until one has been).
func (e *Engine) rigModel() *ft710.Model {
	e.rp.mu.Lock()
	defer e.rp.mu.Unlock()
	if e.rp.model == nil {
		return ft710.FT710
	}
	return e.rp.model
}

// forgetRadio: look the radio up again next time (unplugged, renamed, another radio).
func (e *Engine) forgetRadio(port string) {
	e.rp.mu.Lock()
	if e.rp.autoPort == port {
		e.rp.autoPort = ""
	}
	if e.rp.identPort == port {
		e.rp.model, e.rp.identPort = nil, ""
	}
	e.rp.mu.Unlock()
}

// yaesuPort: the CAT port, not yet knowing which radio.
func (e *Engine) yaesuPort() (string, error) {
	e.mu.Lock()
	keyPort, catType, catPort := e.st.TxPort, e.st.CATType, e.st.CATPort
	e.mu.Unlock()
	if s := catSpecFor(keyPort, catType); s.Kenwood {
		return "", errNotYaesu
	}
	// Never CAT on the key port: opening a serial port raises its DTR (and RTS) for a
	// moment, and that's the CW key line, so the radio would key every time. (A CAT port
	// set to the key port is ignored: Auto finds the right one.)
	if catPort != "" && !samePort(catPort, keyPort) && !isStandardPort(catPort) {
		return catPort, nil
	}
	if p := catPortFor(keyPort); p != "" && !samePort(p, keyPort) {
		return p, nil
	}
	if p := pairedCATPort(keyPort, portList()); p != "" { // Mac: usbserial-...1 -> ...0
		return p, nil
	}
	e.rp.mu.Lock()
	p := e.rp.autoPort
	e.rp.mu.Unlock()
	if samePort(p, keyPort) {
		p = ""
	}
	if p == "" {
		e.rp.mu.Lock()
		ferr := e.recentFailLocked("auto:" + keyPort)
		e.rp.mu.Unlock()
		if ferr != nil {
			return "", ferr
		}
		p = findYaesu(keyPort)
		if p == "" {
			err := errors.New("couldn't find the radio's CAT port: is it on and plugged in by USB? " +
				"(FT-710: CAT-1 RATE in OPERATION SETTING > GENERAL; FT-991A: menu 031 CAT RATE. Any of 4800-38400 works.) " +
				"You can also pick the port in Setup > Radio > CAT port")
			e.noteFail("auto:"+keyPort, err)
			return "", err
		}
		e.rp.mu.Lock()
		e.rp.autoPort = p
		e.rp.mu.Unlock()
	}
	return p, nil
}

// findYaesu asks each likely serial port (not the key port) for its ID, at each CAT rate.
func findYaesu(keyPort string) string {
	for _, p := range catCandidates(keyPort, portList()) {
		for _, baud := range catBauds {
			c, err := openCAT(p, baud)
			if err != nil {
				break
			}
			ok := false
			if c.write("ID;") == nil {
				a, err := c.readReplyWithin(400 * time.Millisecond)
				ok = err == nil && ft710.ModelForID(a) != nil
			}
			c.Close()
			if ok {
				return p
			}
		}
	}
	return ""
}

// samePort: whether a and b are the same device (by-id links resolved).
func samePort(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// likelyRigPort: USB serial ports (not GPIO, Bluetooth, modems...).
func likelyRigPort(p string) bool {
	for _, s := range []string{"SLAB", "usbserial", "usbmodem", "ttyUSB", "ttyACM", "/dev/serial/by-id/"} {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// ft710CAT: a CAT function for the ft710 package, on the FT-710's port.
func (e *Engine) ft710CAT() (ft710.CAT, catSpec, error) {
	spec, err := e.ft710Spec()
	if err != nil {
		return nil, spec, err
	}
	return func(cmds ...string) ([]string, error) {
		a, err := e.rig.do(spec, cmds...)
		if err != nil && (strings.Contains(err.Error(), "can't open") || strings.Contains(err.Error(), "didn't answer")) {
			e.forgetRadio(spec.Port) // unplugged, renamed, or another radio: look again next time
		}
		return a, err
	}, spec, nil
}

type rigReply struct {
	State     *ft710.State `json:"state,omitempty"`
	Model     string       `json:"model,omitempty"`     // "FT-710", "FT-991A"
	Bands     []string     `json:"bands,omitempty"`     // its band keys
	MaxPowerW int          `json:"maxPowerW,omitempty"` // here (50 on 2 m and 70 cm on the FT-991A)
	Baud      int          `json:"baud,omitempty"`
	PCKeying  *int         `json:"pcKeying,omitempty"` // FT-991A menu 060: 0 off, 1 DAKY, 2 RTS, 3 DTR
	Warning   string       `json:"warning,omitempty"`
	Port      string       `json:"port,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// rigState reads the panel's state from the radio.
func (e *Engine) rigState() rigReply {
	cat, spec, err := e.ft710CAT()
	if err != nil {
		return rigReply{Error: err.Error()}
	}
	m := e.rigModel()
	a, err := cat(m.StateQueries()...)
	if err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	st, err := m.ParseState(a)
	if err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	r := rigReply{State: &st, Port: spec.Port, Model: m.Name, Bands: m.Bands(), MaxPowerW: m.MaxPowerAt(st.FreqA), Baud: spec.Baud}
	if q := m.PCKeyingQuery(); q != "" {
		if ans, err := cat(q); err == nil && len(ans) == 1 {
			if v, err := m.ParsePCKeying(ans[0]); err == nil {
				r.PCKeying = &v
				r.Warning = e.pcKeyingWarning(m, v)
			}
		}
	}
	return r
}

// pcKeyingWarning: the FT-991A only keys from the USB key line with menu 060 PC KEYING on
// the same line the app keys (it ships OFF).
func (e *Engine) pcKeyingWarning(m *ft710.Model, v int) string {
	e.mu.Lock()
	line := e.st.TxLine
	e.mu.Unlock()
	want, name := ft710.PCKeyingDTR, "DTR"
	if line == "rts" {
		want, name = ft710.PCKeyingRTS, "RTS"
	}
	if v == want || (line != "dtr" && line != "rts") {
		return ""
	}
	have := []string{"OFF", "DAKY", "RTS", "DTR"}[v]
	return fmt.Sprintf("The %s's menu 060 PC KEYING is %s, so CW from this app won't key it. It needs %s (the app's key line).", m.Name, have, name)
}

// rigAction: one panel button. value: Hz for freqA/freqB/clarOffset, W for power,
// "1"/"0" for the switches, a mode code for mode.
type rigAction struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}

func (a rigAction) cmds(m *ft710.Model, hz int) ([]string, error) {
	on := a.Value == "1" || a.Value == "true"
	num := func() (int, error) {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(a.Value), "%d", &n); err != nil {
			return 0, fmt.Errorf("%s: %q isn't a number", a.Action, a.Value)
		}
		return n, nil
	}
	one := func(c string, err error) ([]string, error) {
		if err != nil {
			return nil, err
		}
		return []string{c}, nil
	}
	switch a.Action {
	case "freqA", "freqB":
		hz, err := num()
		if err != nil {
			return nil, err
		}
		if a.Action == "freqA" {
			return one(m.SetFreqA(hz))
		}
		return one(m.SetFreqB(hz))
	case "mode":
		return one(ft710.SetMode(strings.ToUpper(a.Value)))
	case "swap":
		return []string{ft710.Swap}, nil
	case "atob":
		return []string{ft710.AtoB}, nil
	case "btoa":
		return []string{ft710.BtoA}, nil
	case "split":
		return []string{m.SetSplit(on)}, nil
	case "clarOffset":
		off, err := num()
		if err != nil {
			return nil, err
		}
		return one(m.SetClarOffset(off))
	case "clarClear":
		return []string{ft710.ClarClear}, nil
	case "power":
		w, err := num()
		if err != nil {
			return nil, err
		}
		return one(m.SetPower(w, hz))
	case "rfGain", "afGain":
		n, err := num()
		if err != nil {
			return nil, err
		}
		if a.Action == "rfGain" {
			return one(ft710.SetRFGain(n))
		}
		return one(ft710.SetAFGain(n))
	case "band":
		return one(m.SelectBand(a.Value))
	case "tune":
		if hz > 0 && !m.TunerCovers(hz) {
			return nil, fmt.Errorf("the %s's tuner is for HF and 6 m", m.Name)
		}
		return []string{m.TunerStart()}, nil
	case "pcKeying":
		v, err := num()
		if err != nil {
			return nil, err
		}
		return one(m.SetPCKeying(v))
	case "tuner":
		if on {
			return []string{ft710.TunerOn}, nil
		}
		return []string{ft710.TunerOff}, nil
	}
	return nil, fmt.Errorf("unknown rig action %q", a.Action)
}

// doRig runs a panel action and returns the new state. Clarifier RX/TX switches keep the
// other one as it is, so they need the current state first.
func (e *Engine) doRig(a rigAction) rigReply {
	if e.sweepRunning() {
		return rigReply{Error: "an SWR sweep is running (Stop it first)"}
	}
	cat, spec, err := e.ft710CAT()
	if err != nil {
		return rigReply{Error: err.Error()}
	}
	m := e.rigModel()
	var cmds []string
	switch a.Action {
	case "clarRx", "clarTx":
		ans, err := cat(m.StateQueries()...)
		if err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
		st, err := m.ParseState(ans)
		if err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
		on := a.Value == "1" || a.Value == "true"
		if a.Action == "clarRx" {
			st.ClarRX = on
		} else {
			st.ClarTX = on
		}
		cmds = []string{m.SetClar(st.ClarRX, st.ClarTX)}
	default:
		hz := 0 // where the radio is, for the power limit and the tuner
		if a.Action == "power" || a.Action == "tune" {
			if ans, err := cat("FA;"); err == nil && len(ans) == 1 {
				hz, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ans[0], "FA"), ";"))
			}
		}
		if cmds, err = a.cmds(m, hz); err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
	}
	if _, err := cat(cmds...); err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	switch a.Action {
	case "band":
		time.Sleep(250 * time.Millisecond) // a band change takes the radio a moment
		// Land in the middle of the band's CW segment (60 m is channelized: keep what the
		// radio recalled), in CW on the usual side (CW-L below 10 MHz, CW-U above): the
		// band key alone brings back the band stack, maybe SSB up the band.
		hz := cwMidHz(a.Value)
		if hz > 0 {
			if fa, err := m.SetFreqA(hz); err == nil {
				cat(fa)
			}
		} else if ans, err := cat("FA;"); err == nil && len(ans) == 1 {
			hz, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ans[0], "FA"), ";"))
		}
		if hz > 0 {
			if md, err := ft710.SetMode(cwModeFor(hz)); err == nil {
				cat(md)
			}
		}
	case "freqA", "swap", "btoa":
		time.Sleep(50 * time.Millisecond) // the radio settles before the read-back
	}
	return e.rigState()
}

// ---- SWR sweep ----

func (e *Engine) sweepRunning() bool {
	e.rp.mu.Lock()
	defer e.rp.mu.Unlock()
	return e.rp.sweep.Running
}

func (e *Engine) sweepStatus() sweepState {
	e.rp.mu.Lock()
	defer e.rp.mu.Unlock()
	s := e.rp.sweep
	s.Points = append([]ft710.Point(nil), s.Points...)
	return s
}

type sweepRequest struct {
	Band   string `json:"band"`   // "20m"...: its edges and default step, unless given
	FromHz int    `json:"fromHz"` //
	ToHz   int    `json:"toHz"`
	StepHz int    `json:"stepHz"`
	PowerW int    `json:"powerW"`
}

// startSweep checks everything that can be checked first, then runs in the background.
// The key line must be open (Transmit enabled, DTR or RTS) with nothing being sent.
func (e *Engine) startSweep(q sweepRequest) error {
	if q.Band != "" {
		for _, b := range ft710.Bands {
			if b.Name == q.Band {
				if q.FromHz == 0 {
					q.FromHz = b.FromHz
				}
				if q.ToHz == 0 {
					q.ToHz = b.ToHz
				}
				if q.StepHz == 0 {
					q.StepHz = b.StepHz
				}
			}
		}
	}
	if q.PowerW == 0 {
		q.PowerW = 5
	}
	cfg := ft710.SweepConfig{FromHz: q.FromHz, ToHz: q.ToHz, StepHz: q.StepHz, PowerW: q.PowerW}
	if _, err := cfg.Plan(); err != nil {
		return err
	}
	spec, err := e.ft710Spec()
	if err != nil {
		return err
	}
	cfg.Model = e.rigModel()
	e.mu.Lock()
	line := e.tx.line
	switch {
	case e.st.Mode != "cw":
		err = errors.New("switch to CW first")
	case line == nil:
		err = errors.New("the sweep keys the radio like CW: turn on Transmit enabled with Key line DTR or RTS first")
	case e.tx.busyLocked():
		err = errors.New("wait until sending has finished")
	}
	if err == nil {
		e.setAutoCQLocked(false)
		e.tx.sweeping = true // holds the send queue
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}

	e.rp.mu.Lock()
	if e.rp.sweep.Running {
		e.rp.mu.Unlock()
		e.mu.Lock()
		e.tx.sweeping = false
		e.mu.Unlock()
		return errors.New("a sweep is already running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	e.rp.cancel = cancel
	done := make(chan struct{})
	e.rp.done = done
	e.rp.sweep = sweepState{Running: true, Band: q.Band, FromHz: q.FromHz, ToHz: q.ToHz, StepHz: q.StepHz, PowerW: q.PowerW}
	e.rp.mu.Unlock()

	go func() {
		defer cancel()
		// Close done only after Sweep's unkey, and before taking e.mu.
		// closeLocked waits on done while it still holds e.mu, then closes
		// the key line. Taking e.mu first would deadlock that wait.
		finish := func() {
			e.rp.mu.Lock()
			if e.rp.done != nil {
				close(e.rp.done)
				e.rp.done = nil
			}
			e.rp.mu.Unlock()
		}
		sp, releaseCAT, herr := radioBus.holdLease(spec.Port, spec.Baud, rig.PriCommand)
		if herr != nil {
			finish()
			e.failSweep(herr)
			return
		}
		defer releaseCAT()
		// The key line is already open. Hold it only when it is a second
		// serial port (FT-710 if01), so a poll cannot open it beside the sweep.
		// test:keyline and GPIO are not serial ports.
		if bl, ok := line.(*busLine); ok && bl.sp != nil && bl.sp.key != sp.key {
			_, releaseKey, kerr := radioBus.holdLease(bl.sp.path, 0, rig.PriKey)
			if kerr != nil {
				finish()
				e.failSweep(kerr)
				return
			}
			defer releaseKey()
		}
		cat := func(cmds ...string) ([]string, error) { return sp.transact(cmds) }
		pts, err := ft710.Sweep(ctx, cat, line, cfg, func(p ft710.Point) {
			e.rp.mu.Lock()
			e.rp.sweep.Points = append(e.rp.sweep.Points, p)
			e.rp.mu.Unlock()
		})
		finish()
		e.mu.Lock()
		e.tx.sweeping = false
		if err == nil {
			if b, ok := ft710.Best(pts); ok {
				e.setNoteLocked(fmt.Sprintf("SWR sweep done: lowest %.1f at %s MHz", b.SWR, mhzStr(b.Hz)))
			}
		} else if errors.Is(err, context.Canceled) {
			e.setNoteLocked("SWR sweep stopped; the radio is back as it was")
		} else {
			e.setNoteLocked("SWR sweep: " + err.Error())
		}
		e.mu.Unlock()
		e.rp.mu.Lock()
		e.rp.sweep.Running = false
		if b, ok := ft710.Best(pts); ok {
			e.rp.sweep.Best = &b
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			e.rp.sweep.Error = err.Error()
		}
		e.rp.mu.Unlock()
	}()
	return nil
}

// failSweep leaves the queue free and the panel stopped. The radio was not keyed.
func (e *Engine) failSweep(err error) {
	e.mu.Lock()
	e.tx.sweeping = false
	e.setNoteLocked("SWR sweep: " + err.Error())
	e.mu.Unlock()
	e.rp.mu.Lock()
	e.rp.sweep.Running = false
	e.rp.sweep.Error = err.Error()
	e.rp.mu.Unlock()
}

func (e *Engine) stopSweep() {
	e.rp.mu.Lock()
	if e.rp.cancel != nil {
		e.rp.cancel()
	}
	e.rp.mu.Unlock()
}

// stopSweepWait cancels the sweep and waits until it has unkeyed. closeLocked
// calls this before it closes the key line. The caller keeps e.mu; the sweep
// goroutine closes done before it takes e.mu, so this wait cannot deadlock.
func (e *Engine) stopSweepWait() {
	e.stopSweep()
	e.rp.mu.Lock()
	done := e.rp.done
	e.rp.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
