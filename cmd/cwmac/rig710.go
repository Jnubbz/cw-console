package main

// The FT-710 rig panel: VFO A/B, split, clarifier, power, tuner and an SWR sweep, over
// CAT (the radio's Enhanced COM port, CAT-1). Works on the Mac and the Pi box. CW itself
// still goes over the key line (DTR/RTS on the Standard COM port); the sweep borrows that
// line for its short carriers.
//
// The CAT port: Setup's "CAT port" if set; on Linux, the key port's partner by its
// by-id name; otherwise found by asking each serial port "ID;" and waiting for the
// FT-710's ID0800;.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/rig/ft710"
)

type rigPanel struct {
	mu       sync.Mutex
	autoPort string // found by probing (cleared when it stops answering)
	sweep    sweepState
	cancel   context.CancelFunc
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

var errNotYaesu = errors.New("the rig panel is for the Yaesu FT-710 (this radio is set up as Kenwood CAT)")

// ft710Spec: where the FT-710's CAT is. May probe the serial ports (a second or two).
func (e *Engine) ft710Spec() (catSpec, error) {
	e.mu.Lock()
	keyPort, catType, catPort := e.st.TxPort, e.st.CATType, e.st.CATPort
	e.mu.Unlock()
	if s := catSpecFor(keyPort, catType); s.Kenwood {
		return catSpec{}, errNotYaesu
	}
	if catPort != "" {
		return catSpec{Port: catPort, Baud: catBaud}, nil
	}
	if p := catPortFor(keyPort); p != "" {
		return catSpec{Port: p, Baud: catBaud}, nil
	}
	e.rp.mu.Lock()
	p := e.rp.autoPort
	e.rp.mu.Unlock()
	if p == "" {
		p = findFT710(keyPort)
		if p == "" {
			return catSpec{}, errors.New("couldn't find the FT-710's CAT port: is it on and plugged in by USB, " +
				"with CAT-1 RATE at 38400 (Setup menu: OPERATION SETTING > GENERAL)? You can also pick the port in Setup > Radio > CAT port")
		}
		e.rp.mu.Lock()
		e.rp.autoPort = p
		e.rp.mu.Unlock()
	}
	return catSpec{Port: p, Baud: catBaud}, nil
}

// findFT710 asks each likely serial port (not the key port) for its ID.
func findFT710(keyPort string) string {
	for _, pi := range listPorts() {
		p := pi.Path
		if p == keyPort || !likelyRigPort(p) {
			continue
		}
		c, err := openCAT(p, catBaud)
		if err != nil {
			continue
		}
		if c.write("ID;") == nil {
			if a, err := c.readReplyWithin(400 * time.Millisecond); err == nil && a == ft710.ID {
				c.Close()
				return p
			}
		}
		c.Close()
	}
	return ""
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
		if err != nil && strings.Contains(err.Error(), "can't open") {
			e.rp.mu.Lock()
			if e.rp.autoPort == spec.Port {
				e.rp.autoPort = "" // unplugged or renamed: look again next time
			}
			e.rp.mu.Unlock()
		}
		return a, err
	}, spec, nil
}

type rigReply struct {
	State *ft710.State `json:"state,omitempty"`
	Port  string       `json:"port,omitempty"`
	Error string       `json:"error,omitempty"`
}

// rigState reads the panel's state from the radio.
func (e *Engine) rigState() rigReply {
	cat, spec, err := e.ft710CAT()
	if err != nil {
		return rigReply{Error: err.Error()}
	}
	a, err := cat(ft710.StateQueries...)
	if err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	st, err := ft710.ParseState(a)
	if err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	return rigReply{State: &st, Port: spec.Port}
}

// rigAction: one panel button. value: Hz for freqA/freqB/clarOffset, W for power,
// "1"/"0" for the switches, a mode code for mode.
type rigAction struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}

func (a rigAction) cmds() ([]string, error) {
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
			return one(ft710.SetFreqA(hz))
		}
		return one(ft710.SetFreqB(hz))
	case "mode":
		return one(ft710.SetMode(strings.ToUpper(a.Value)))
	case "swap":
		return []string{ft710.Swap}, nil
	case "atob":
		return []string{ft710.AtoB}, nil
	case "btoa":
		return []string{ft710.BtoA}, nil
	case "split":
		return []string{ft710.SetSplit(on)}, nil
	case "clarOffset":
		hz, err := num()
		if err != nil {
			return nil, err
		}
		return one(ft710.SetClarOffset(hz))
	case "clarClear":
		return []string{ft710.ClarClear}, nil
	case "power":
		w, err := num()
		if err != nil {
			return nil, err
		}
		return one(ft710.SetPower(w))
	case "tune":
		return []string{ft710.TunerStart}, nil
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
	var cmds []string
	switch a.Action {
	case "clarRx", "clarTx":
		ans, err := cat(ft710.StateQueries...)
		if err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
		st, err := ft710.ParseState(ans)
		if err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
		on := a.Value == "1" || a.Value == "true"
		if a.Action == "clarRx" {
			st.ClarRX = on
		} else {
			st.ClarTX = on
		}
		cmds = []string{ft710.SetClar(st.ClarRX, st.ClarTX)}
	default:
		if cmds, err = a.cmds(); err != nil {
			return rigReply{Port: spec.Port, Error: err.Error()}
		}
	}
	if _, err := cat(cmds...); err != nil {
		return rigReply{Port: spec.Port, Error: err.Error()}
	}
	if a.Action == "freqA" || a.Action == "swap" || a.Action == "btoa" {
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
	cat, _, err := e.ft710CAT()
	if err != nil {
		return err
	}
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
	e.rp.sweep = sweepState{Running: true, Band: q.Band, FromHz: q.FromHz, ToHz: q.ToHz, StepHz: q.StepHz, PowerW: q.PowerW}
	e.rp.mu.Unlock()

	go func() {
		defer cancel()
		pts, err := ft710.Sweep(ctx, cat, line, cfg, func(p ft710.Point) {
			e.rp.mu.Lock()
			e.rp.sweep.Points = append(e.rp.sweep.Points, p)
			e.rp.mu.Unlock()
		})
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

func (e *Engine) stopSweep() {
	e.rp.mu.Lock()
	if e.rp.cancel != nil {
		e.rp.cancel()
	}
	e.rp.mu.Unlock()
}
