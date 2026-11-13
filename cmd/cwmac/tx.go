package main

// Sender: one queue of text not yet keyed (typed characters, Enter lines, macros all go
// here, in order). The keyer takes the next character when it's ready, so anything still
// in the queue can be backspaced. Stops by itself if the page disappears.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/ops"
)

const deadManAfter = 5 * time.Second // no page connected this long -> stop sending

type txEngine struct {
	e        *Engine
	line     keyLine
	keyer    cwSender // the box's keyer on a key line, or the radio's over CAT
	portName string
	queue    []rune
	queueSeq int64 // bumps on every queue change (lets the page reconcile its echo)
	sentTail []rune
	inLine   bool
	sweeping bool // an SWR sweep is using the key line: hold the queue

	macros    [8]cw.Macro
	ctx       cw.MacroContext
	macroText string

	contestMacros [8]cw.Macro // the F-keys in Contest mode (contest-macros.txt)
	contestText   string
}

type keyLine interface {
	cw.KeyLine
	Close()
}

func (t *txEngine) init(e *Engine) {
	t.e = e
	t.loadMacros()
	t.loadContestMacros()
}

func macrosPath() string { return filepath.Join(supportDir(), "macros.txt") }

func (t *txEngine) loadMacros() {
	b, err := os.ReadFile(macrosPath())
	if err != nil {
		os.MkdirAll(supportDir(), 0o755)
		os.WriteFile(macrosPath(), []byte(cw.DefaultMacrosText), 0o644)
		b = []byte(cw.DefaultMacrosText)
	}
	if m, changed := ops.MigrateMacros(string(b)); changed { // older file: add {LOG} to TU/73
		if os.WriteFile(macrosPath(), []byte(m), 0o644) == nil {
			b = []byte(m)
		}
	}
	t.macroText = string(b)
	t.ctx, t.macros = cw.ParseMacros(t.macroText)
}

func (t *txEngine) saveMacrosLocked(text string) error {
	if len(text) > 100_000 {
		return errors.New("macros file too large")
	}
	os.MkdirAll(supportDir(), 0o755)
	if err := os.WriteFile(macrosPath(), []byte(text), 0o644); err != nil {
		return err
	}
	t.macroText = text
	t.ctx, t.macros = cw.ParseMacros(text)
	return nil
}

// enable opens the port and starts the keyer (or closes it when on=false).
func (t *txEngine) enableLocked(on bool, port, line string) error {
	t.closeLocked()
	if !on {
		return nil
	}
	if port == "" {
		return errors.New("pick the radio's key port first (Setup > Radio > Key port)")
	}
	switch line {
	case "cat": // the radio's own keyer, sent text over CAT (KY)
		k, err := newCATKeyer(catSpecFor(port, t.e.st.CATType), t.e.st.WPM)
		if err != nil {
			return err
		}
		t.keyer, t.portName = k, port
	case "audio": // a keyed tone into the rig's USB audio, the rig keyed as for FT8
		k, err := t.newAudioKeyerLocked(port)
		if err != nil {
			return err
		}
		t.keyer, t.portName = k, port
		t.e.rigToAudioCW()
	default:
		l, err := openKeyLine(port, line == "rts")
		if err != nil {
			return err
		}
		t.line, t.portName = l, port
		t.keyer = cw.NewKeyer(l, t.e.st.WPM, keyerThreadSetup)
	}
	t.queue = nil // never send text typed while transmit was off
	t.queueSeq++
	t.e.st.TxPort, t.e.st.TxLine = port, line
	t.e.saveSettingsLocked()
	return nil
}

func (t *txEngine) newAudioKeyerLocked(port string) (*audioKeyer, error) {
	st := &t.e.st
	if !appliance {
		return nil, errors.New("CW as an audio tone works on the POTA Pi box")
	}
	dev := t.e.cap.device()
	if dev == "" {
		return nil, errors.New("no rig sound card yet: pick the Audio input in Setup > Radio and let it listen")
	}
	ptt, err := openPTT(port, effectivePTT("audio", st.FT8PTTLine), st.CATType)
	if err != nil {
		return nil, err
	}
	return newAudioKeyer(ptt, dev, st.FT8Out, t.e.cap.setPaused, st.Pitch, ft8LevelGain(st.FT8Level), st.WPM), nil
}

func (t *txEngine) closeLocked() {
	t.e.stopSweep() // its key line is about to close
	if t.keyer != nil {
		t.keyer.Abort()
		t.keyer.Close()
		t.keyer = nil
	}
	if t.line != nil {
		t.line.Close()
		t.line = nil
	}
	t.portName = ""
	t.e.assist.Stop()
	t.queue = nil
	t.queueSeq++
	t.endLineLocked("")
}

func (t *txEngine) appendLocked(s string) {
	s = strings.ToUpper(s)
	t.queue = append(t.queue, []rune(s)...)
	t.queueSeq++
}

// backspaceLocked removes up to n not-yet-sent characters from the end of the queue.
func (t *txEngine) backspaceLocked(n int) {
	for ; n > 0 && len(t.queue) > 0; n-- {
		t.queue = t.queue[:len(t.queue)-1]
	}
	t.queueSeq++
}

// macroLocked: the operator pressed an F-key (call = what's in the Call box).
func (t *txEngine) macroLocked(i int, call string) error {
	t.e.setCallLocked(call, false)
	return t.sendMacroLocked(i, t.e.call, true)
}

// typedLocked: the operator typed or pressed Enter; that's them taking over.
func (t *txEngine) typedLocked(s string) {
	t.appendLocked(s)
	if strings.TrimSpace(s) != "" {
		t.e.assist.Manual(false, time.Now())
	}
}

// sendMacroLocked queues macro i for call. manual = pressed by the operator (rather
// than sent by Auto-CQ / auto-reply). A {LOG} in the macro logs the contact.
func (t *txEngine) sendMacroLocked(i int, call string, manual bool) error {
	if i < 0 || i >= 8 {
		return errors.New("bad macro")
	}
	m := t.activeMacros()[i]
	if m.Text == "" {
		return errors.New("F" + itoa(i+1) + " is empty; edit the macros to set it")
	}
	body, doLog := ops.StripLog(m.Text)
	ctx := t.ctx
	ctx.HisCall = strings.ToUpper(strings.TrimSpace(call))
	ctx.Extra = map[string]string{}
	for k, v := range t.ctx.Extra {
		ctx.Extra[k] = v
	}
	if p := t.e.parkLocked(); p != "" { // {PARK}: the park set on the main screen
		ctx.Extra["PARK"] = p
	}
	if z := t.e.myZoneLocked(); z > 0 { // {ZONE}: my CQ zone (contests)
		ctx.Extra["ZONE"] = itoa(z)
	}
	text := cw.Expand(body, ctx)
	now := time.Now()
	if text != "" {
		if len(t.queue) > 0 && t.queue[len(t.queue)-1] != ' ' {
			text = " " + text
		}
		t.appendLocked(text + " ")
		if manual {
			t.e.assist.Manual(strings.HasPrefix(strings.TrimSpace(text), "CQ"), now)
		}
		if ctx.HisCall != "" && (strings.Contains(body, "{CALL}") || strings.Contains(body, "!")) {
			t.e.assist.UsedCall(ctx.HisCall, now)
		}
	}
	if doLog {
		if !t.e.autoLogLocked() {
			if ctx.HisCall != "" {
				t.e.setNoteLocked("Not logged (Auto-log is off): Log QSO or Cmd/Ctrl+Enter logs " + ctx.HisCall)
			}
			return nil
		}
		return t.e.logLocked(ctx.HisCall)
	}
	return nil
}

func (t *txEngine) stopLocked() {
	t.e.assist.Stop()
	t.e.stopSweep()
	if t.keyer != nil {
		t.keyer.Abort()
	}
	t.queue = nil
	t.queueSeq++
	t.endLineLocked(" [stopped]")
}

func (t *txEngine) endLineLocked(suffix string) {
	if t.inLine {
		t.e.appendRawLocked(suffix + "\n")
		t.e.pending = false
		t.inLine = false
	}
}

// tickLocked runs every 25 ms: feed the keyer, echo what went out, end the TX line,
// and stop if no page has been connected for a while (dead-man switch).
func (t *txEngine) tickLocked() {
	k := t.keyer
	if k == nil {
		return
	}
	select {
	case err := <-k.Errors():
		t.closeLocked()
		t.e.setNoteLocked("Keying stopped: " + err.Error())
		return
	default:
	}
	if t.e.hub.idleFor() > deadManAfter && (len(t.queue) > 0 || k.Busy()) {
		t.stopLocked()
		t.e.setNoteLocked("Sending stopped: the page was closed.")
	}
	for drained := false; !drained; {
		select {
		case ch := <-k.Sent():
			if !t.inLine {
				t.e.markBreakLocked()
				t.e.appendRawLocked("TX> ")
				t.inLine = true
			}
			t.e.appendRawLocked(string(ch))
			t.sentTail = append(t.sentTail, ch)
			if len(t.sentTail) > 200 {
				t.sentTail = t.sentTail[len(t.sentTail)-200:]
			}
		default:
			drained = true
		}
	}
	if a, ok := k.(*audioKeyer); ok { // the tone follows the decoder's pitch and the Level
		a.configure(t.e.st.Pitch, ft8LevelGain(t.e.st.FT8Level), t.e.st.FT8Out, t.e.cap.device())
	}
	if k.Wants() && len(t.queue) > 0 && !t.sweeping {
		k.SetWPM(t.e.assist.SendWPM(t.e.st.WPM, time.Now())) // Match speed (or the slider)
		if k.Feed(t.queue[0]) {
			t.queue = t.queue[1:]
			t.queueSeq++
		}
	}
	if t.inLine && !k.Busy() && len(t.queue) == 0 && !k.Active(700*time.Millisecond) {
		t.endLineLocked("")
	}
}

func (t *txEngine) fillStateLocked(s *stateMsg) {
	s.TxOn = t.keyer != nil
	s.TxActive = t.keyer != nil && t.keyer.Active(0)
	s.TxSent = string(t.sentTail)
	s.TxQueue = string(t.queue)
	s.TxPort = t.portName
	s.QueueSeq = t.queueSeq
	for _, m := range t.activeMacros() {
		s.Macros = append(s.Macros, m.Label)
	}
}

// busyLocked: keying, about to key, or text waiting (Auto-CQ listens only when idle).
func (t *txEngine) busyLocked() bool {
	k := t.keyer
	return k != nil && (len(t.queue) > 0 || k.Busy() || k.Active(txMuteHang) || t.sweeping)
}
