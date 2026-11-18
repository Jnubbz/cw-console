package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/keying"
	"github.com/Jnubbz/cw-console/ops"
	"github.com/Jnubbz/cw-console/sstv"
)

// sstvSession is one SSTV transmit. The key line is the same openPTT line CW and
// FT8 use. Audio for the page is the WAV; the line is what keys the radio.
type sstvSession struct {
	tx   sstvTx
	line keyLine
	wav  []byte
	id   int
	mode string
	to   string
	secs float64
}

type sstvMsg struct {
	Receiving bool    `json:"receiving"`
	Mode      string  `json:"mode,omitempty"`
	Line      int     `json:"line,omitempty"`
	Lines     int     `json:"lines,omitempty"`
	LiveV     int     `json:"liveV"`
	PicturesV int     `json:"picturesV"`
	Note      string  `json:"note,omitempty"`
	Keyed     bool    `json:"keyed"`
	Elapsed   float64 `json:"elapsed,omitempty"`
	Seconds   float64 `json:"seconds,omitempty"`
	Error     string  `json:"error,omitempty"`
	ID        int     `json:"id,omitempty"`
}

func (e *Engine) ensureSSTV() *receiver {
	if e.sstv == nil {
		dir := filepath.Join(supportDir(), "sstv")
		e.sstv = newReceiver(dir, filepath.Join(dir, "saved"), func() int { return 0 })
		e.sstv.muted = func() bool { return e.sstvSend.tx.keyedNow() }
		e.sstvSend.tx.watch = keying.Watch{Idle: e.hub.idleFor}
		e.sstvSend.tx.now = time.Now
	}
	return e.sstv
}

func (e *Engine) sstvKeep() {
	e.sstvSend.tx.tick()
	e.sstvSend.tx.retryUnkey()
	if e.sstvSend.line != nil && !e.sstvSend.tx.keyedNow() {
		e.sstvSend.line.Close()
		e.sstvSend.line = nil
	}
}

func (e *Engine) sstvStop() {
	e.sstvSend.tx.stop()
	e.sstvKeep()
}

func (e *Engine) sstvStatus() *sstvMsg {
	rx := e.ensureSSTV().status()
	msg := &sstvMsg{
		Receiving: rx.Receiving, Mode: rx.Mode, Line: rx.Line, Lines: rx.Lines,
		LiveV: rx.LiveV, PicturesV: rx.PicturesV, Note: rx.Note,
		Keyed: e.sstvSend.tx.keyedNow(), ID: e.sstvSend.id, Seconds: e.sstvSend.secs,
	}
	e.sstvSend.tx.mu.Lock()
	msg.Error = e.sstvSend.tx.err
	if msg.Keyed {
		msg.Elapsed = e.sstvSend.tx.clock().Sub(e.sstvSend.tx.since).Seconds()
		if msg.Elapsed < 0 {
			msg.Elapsed = 0
		}
	}
	e.sstvSend.tx.mu.Unlock()
	return msg
}

// sstvBegin encodes the picture, then keys. Encode failures never key.
// The key line is opened with openPTT and dropped again by sstvKeep.
func (e *Engine) sstvAudio(id int) []byte {
	if id != e.sstvSend.id {
		return nil
	}
	return e.sstvSend.wav
}

func (e *Engine) sstvBegin(pic []byte, mode, to string) (int, float64, error) {
	if e.sstvSend.tx.keyedNow() {
		return 0, 0, errors.New("already transmitting")
	}
	m := sstv.ByName(mode)
	if m == nil {
		return 0, 0, fmt.Errorf("no SSTV mode %q", mode)
	}
	img, _, err := image.Decode(bytes.NewReader(pic))
	if err != nil {
		return 0, 0, errors.New("that isn't a picture this can send")
	}
	audio := sstv.Encode(img, m, 48000)
	wav, secs := sstvWAV(audio)
	e.mu.Lock()
	port, ptt, cat := e.st.TxPort, e.st.TxLine, e.st.CATType
	e.mu.Unlock()
	if ptt != "dtr" && ptt != "rts" && ptt != "cat" {
		ptt = "dtr"
	}
	if port == "" {
		return 0, 0, errors.New("pick the radio's key port first (Setup > Radio > Key port)")
	}
	e.mu.Lock()
	e.ensureSSTV()
	e.mu.Unlock()
	line, err := openPTT(port, ptt, cat)
	if err != nil {
		return 0, 0, err
	}
	e.ensureSSTV()
	if err := e.sstvSend.tx.start(line, time.Duration(secs*float64(time.Second))); err != nil {
		line.Close()
		return 0, 0, err
	}
	e.sstvSend.line = line
	e.sstvSend.id++
	e.sstvSend.wav = wav
	e.sstvSend.mode = m.Name
	e.sstvSend.to = to
	e.sstvSend.secs = secs
	return e.sstvSend.id, secs, nil
}

func sstvWAV(audio []float32) ([]byte, float64) {
	const rate = 48000
	lead, tail := rate*3/10, rate/10
	n := (lead + len(audio) + tail) * 2
	w := make([]byte, 44, 44+n)
	copy(w, "RIFF")
	binary.LittleEndian.PutUint32(w[4:], uint32(36+n))
	copy(w[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(w[16:], 16)
	binary.LittleEndian.PutUint16(w[20:], 1)
	binary.LittleEndian.PutUint16(w[22:], 1)
	binary.LittleEndian.PutUint32(w[24:], rate)
	binary.LittleEndian.PutUint32(w[28:], rate*2)
	binary.LittleEndian.PutUint16(w[32:], 2)
	binary.LittleEndian.PutUint16(w[34:], 16)
	copy(w[36:], "data")
	binary.LittleEndian.PutUint32(w[40:], uint32(n))
	w = append(w, make([]byte, lead*2)...)
	for _, s := range audio {
		w = binary.LittleEndian.AppendUint16(w, uint16(int16(math.Round(float64(s)*32767))))
	}
	w = append(w, make([]byte, tail*2)...)
	return w, float64(lead+len(audio)+tail) / rate
}

func (e *Engine) sstvSetCall(id, call string) bool {
	call = strings.ToUpper(strings.TrimSpace(call))
	if !e.ensureSSTV().setCall(id, call) {
		return false
	}
	if call != "" {
		e.mu.Lock()
		e.setCallLocked(call, true)
		e.noteQRZ(call)
		e.mu.Unlock()
	}
	return true
}

func (e *Engine) sstvLog(call string) error {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" {
		return errors.New("type the station's callsign first")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	freq, band := ops.ParseFreq(e.st.Freq)
	q := ops.QSO{Call: call, Start: time.Now(), Freq: freq, Band: band, Mode: "SSTV",
		RSTSent: "595", RSTRcvd: "595", MyCall: e.tx.ctx.MyCall, Park: e.parkLocked(),
		MyState: e.tx.ctx.MyState, MyGrid: e.tx.ctx.MyGrid}
	q = e.knownQSO(q, "SSTV")
	q.Mode = "SSTV"
	dupe, err := e.logbook.Add(q)
	if err != nil {
		return err
	}
	e.setCallLocked(call, true)
	msg := "Logged " + call + " (SSTV)"
	if dupe {
		msg += " (dupe)"
	}
	e.setNoteLocked(msg)
	return nil
}
