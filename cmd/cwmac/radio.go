package main

// Radio profiles: the settings that belong to a radio rather than to the operator (its
// sound card, key port, CW keying, PTT, audio out, TX level, CAT, rig control, CW pitch),
// saved per radio so switching between, say, an FT-710 and an FX-4CR is one choice. The
// box also switches by itself when the current radio's USB port is gone and another
// profile's port is plugged in.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
)

var serialByID = "/dev/serial/by-id/" // test hook

type RadioProfile struct {
	Name        string  `json:"name"`
	AudioDevice string  `json:"audioDevice"`
	TxPort      string  `json:"txPort"`
	TxLine      string  `json:"txLine"`
	RigCtl      bool    `json:"rigCtl"`
	FT8PTTLine  string  `json:"ft8PttLine"`
	FT8Out      string  `json:"ft8Out"`
	FT8Level    int     `json:"ft8Level"`
	Pitch       float64 `json:"pitch"`
	CATType     string  `json:"catType"`
	CATPort     string  `json:"catPort"`
}

func (s *Settings) profileNow(name string) RadioProfile {
	return RadioProfile{Name: name, AudioDevice: s.AudioDevice, TxPort: s.TxPort, TxLine: s.TxLine,
		RigCtl: s.RigCtl, FT8PTTLine: s.FT8PTTLine, FT8Out: s.FT8Out, FT8Level: s.FT8Level, Pitch: s.Pitch, CATType: s.CATType, CATPort: s.CATPort}
}

// effectivePTT: how the rig is keyed to send audio (FT8, CW as an audio tone). Older
// settings could say "" (same as CW): that becomes the line CW used, or CAT where CW
// isn't keyed on a line (CAT keyer, audio tone).
func effectivePTT(txLine, ptt string) string {
	switch ptt {
	case "dtr", "rts", "cat":
		return ptt
	}
	switch txLine {
	case "dtr", "rts":
		return txLine
	}
	return "cat"
}

// normalize: a profile's values made valid (they come from a file, maybe an older one).
func (p *RadioProfile) normalize() {
	switch p.TxLine {
	case "dtr", "rts", "cat", "audio":
	default:
		p.TxLine = "dtr"
	}
	p.FT8PTTLine = effectivePTT(p.TxLine, p.FT8PTTLine)
	switch p.FT8Out {
	case "", "plug", "hw48", "hw44":
	default:
		p.FT8Out = ""
	}
	if p.CATType != "yaesu" && p.CATType != "kenwood" {
		p.CATType = ""
	}
	p.FT8Level = max(0, min(100, p.FT8Level))
	if p.Pitch != 0 {
		p.Pitch = math.Round(math.Max(pitchMin, math.Min(pitchMax, p.Pitch))/5) * 5
	}
}

func (s *Settings) useProfile(p RadioProfile) {
	p.normalize()
	s.Radio = p.Name
	s.AudioDevice, s.TxPort, s.TxLine, s.RigCtl = p.AudioDevice, p.TxPort, p.TxLine, p.RigCtl
	s.FT8PTTLine, s.FT8Out, s.FT8Level, s.CATType = p.FT8PTTLine, p.FT8Out, p.FT8Level, p.CATType
	s.CATPort = p.CATPort
	if p.Pitch > 0 {
		s.Pitch = p.Pitch
	}
	s.RigPrevMode, s.RigPrevHz = "", 0 // that was the other radio's state
}

func (s *Settings) radioIndex(name string) int {
	for i, p := range s.Radios {
		if strings.EqualFold(p.Name, name) {
			return i
		}
	}
	return -1
}

// ensureRadiosLocked: there is always at least one profile, the current one exists, and
// every profile's values are valid.
func (e *Engine) ensureRadiosLocked() {
	s := &e.st
	for i := range s.Radios {
		s.Radios[i].normalize()
	}
	if len(s.Radios) == 0 {
		name := s.Radio
		if name == "" {
			name = "Radio 1"
		}
		s.Radios = []RadioProfile{s.profileNow(name)}
		s.Radio = name
	}
	if s.radioIndex(s.Radio) < 0 {
		s.Radio = s.Radios[0].Name
	}
}

// syncRadioLocked copies the radio settings into the current profile (after any change).
func (e *Engine) syncRadioLocked() {
	s := &e.st
	if i := s.radioIndex(s.Radio); i >= 0 {
		s.Radios = append([]RadioProfile(nil), s.Radios...) // don't share with copies of the settings
		s.Radios[i] = s.profileNow(s.Radios[i].Name)
	}
}

// switchRadioLocked saves the current radio and loads another one. The caller restarts
// listening on the new radio's sound card (restartCapture is set).
func (e *Engine) switchRadioLocked(name string) error {
	s := &e.st
	i := s.radioIndex(name)
	if i < 0 {
		return fmt.Errorf("no radio called %q", name)
	}
	if strings.EqualFold(s.Radio, name) {
		return nil
	}
	e.syncRadioLocked()
	// nothing keeps keying the old radio
	e.setAutoCQLocked(false)
	e.tx.closeLocked()
	e.ft8tx.enable(false, "", "")
	s.useProfile(s.Radios[i])
	e.restartCapture = true
	e.saveSettingsLocked()
	e.setNoteLocked("Radio: " + s.Radio)
	return nil
}

// radioAction: new / rename / delete / switch, from the Setup sheet.
func (e *Engine) radioAction(action, name, to string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ensureRadiosLocked()
	s := &e.st
	name, to = strings.TrimSpace(name), strings.TrimSpace(to)
	valid := func(n string) error {
		if n == "" || len(n) > 24 || strings.ContainsAny(n, "\"<>\\") {
			return errors.New("a radio name: 1-24 characters")
		}
		if s.radioIndex(n) >= 0 {
			return fmt.Errorf("there's already a radio called %q", n)
		}
		return nil
	}
	var err error
	switch action {
	case "switch":
		err = e.switchRadioLocked(name)
	case "new": // a new profile, starting from the current radio's settings
		if err = valid(to); err != nil {
			return err
		}
		e.syncRadioLocked()
		p := s.profileNow(to)
		p.TxPort, p.AudioDevice = "", ""                             // pick this radio's own ports
		p.RigCtl, p.FT8Out, p.CATType, p.CATPort = false, "", "", "" // its own CAT and sound chip may differ: start safe
		s.Radios = append(s.Radios, p)
		err = e.switchRadioLocked(to)
	case "rename":
		i := s.radioIndex(name)
		if i < 0 {
			return fmt.Errorf("no radio called %q", name)
		}
		if !strings.EqualFold(name, to) {
			if err = valid(to); err != nil {
				return err
			}
		}
		s.Radios = append([]RadioProfile(nil), s.Radios...)
		s.Radios[i].Name = to
		if strings.EqualFold(s.Radio, name) {
			s.Radio = to
		}
	case "delete":
		i := s.radioIndex(name)
		if i < 0 {
			return fmt.Errorf("no radio called %q", name)
		}
		if len(s.Radios) == 1 {
			return errors.New("that's the only radio")
		}
		cur := strings.EqualFold(s.Radio, name)
		s.Radios = append(append([]RadioProfile(nil), s.Radios[:i]...), s.Radios[i+1:]...)
		if cur {
			s.Radio = "" // so the switch below loads the first one
			err = e.switchRadioLocked(s.Radios[0].Name)
		}
	default:
		return errors.New("unknown radio action")
	}
	e.saveSettingsLocked()
	return err
}

// keyPort: the current radio's key port (for finding its sound card).
func (e *Engine) keyPort() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.TxPort
}

const autoSwitchScans = 3 // box scans (2 s apart) the radio's port must stay gone

// autoRadioLocked: the current radio's USB port has gone and exactly one other profile's
// port is there: that radio was plugged in, so switch to it. Only by-id names count (they
// say which device it is); runs on the box's device scan. The port has to stay gone for
// a few scans, so a USB hiccup doesn't swap radios.
func (e *Engine) autoRadioLocked() {
	s := &e.st
	present := func(p string) bool {
		if !strings.HasPrefix(p, serialByID) {
			return false
		}
		_, err := os.Stat(p)
		return err == nil
	}
	if s.TxPort == "" || present(s.TxPort) || !strings.HasPrefix(s.TxPort, serialByID) {
		e.radioGone = 0
		return
	}
	if e.radioGone++; e.radioGone < autoSwitchScans {
		return
	}
	found := ""
	for _, p := range s.Radios {
		if !strings.EqualFold(p.Name, s.Radio) && present(p.TxPort) {
			if found != "" {
				return // two candidates: leave it to the operator
			}
			found = p.Name
		}
	}
	if found != "" {
		e.radioGone = 0
		e.switchRadioLocked(found)
		e.setNoteLocked("Radio: " + found + " (it was plugged in)")
	}
}
