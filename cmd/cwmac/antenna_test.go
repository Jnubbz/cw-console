//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRigFreqFromPolls(t *testing.T) {
	var f rigFreq
	now := time.Now()
	for _, a := range []string{"FA445000000;", "FB014060000;", "FT0;"} {
		f.note(a, true, now)
	}
	if hz := f.txHz(now); hz != 445_000_000 {
		t.Fatalf("tx %d", hz)
	}
	f.note("FT1;", true, now)
	if hz := f.txHz(now); hz != 14_060_000 {
		t.Fatalf("split tx %d", hz)
	}
	f.note("RM5100;", true, now.Add(time.Minute)) // not a VFO: doesn't freshen it
	if hz := f.txHz(now.Add(time.Minute)); hz != 0 {
		t.Fatalf("stale %d", hz)
	}
}

// The FT-991A with only its HF antenna: no band keys for 2 m and 70 cm, and nothing keys
// the radio while it sits on 70 cm. Ticking the 144/430 MHz antenna allows both.
func TestFT991AAntennas(t *testing.T) {
	e, f := testEngine991(t)
	e.applySettings(json.RawMessage(`{"vhfAnt":false}`))
	f.set("FA", "445000000")
	r := e.rigState()
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	for _, b := range r.Bands {
		if b == "2m" || b == "70cm" {
			t.Fatalf("bands %v", r.Bands)
		}
	}
	if err := e.antennaBlock(); err == nil || !strings.Contains(err.Error(), "445.000") || !strings.Contains(err.Error(), "144/430") {
		t.Fatalf("block: %v", err)
	}
	e.applySettings(json.RawMessage(`{"vhfAnt":true}`))
	if r := e.rigState(); len(r.Bands) != 13 {
		t.Fatalf("bands %v", r.Bands)
	}
	if err := e.antennaBlock(); err != nil {
		t.Fatal(err)
	}
	// and the HF jack the other way round
	e.applySettings(json.RawMessage(`{"hfAntOff":true}`))
	f.set("FA", "14060000")
	r = e.rigState()
	if err := e.antennaBlock(); err == nil || !strings.Contains(err.Error(), "HF/50") {
		t.Fatalf("HF block: %v", err)
	}
	if len(r.Bands) != 2 || r.Bands[0] != "2m" {
		t.Fatalf("bands %v", r.Bands)
	}
}

// The FT-710 (one HF/6 m radio) is never blocked by these settings.
func TestAntennasOnlyForTheFT991A(t *testing.T) {
	e := &Engine{}
	e.st.HFAntOff = true
	e.rigFreq.note("FA014060000;", false, time.Now())
	if err := e.antennaBlock(); err != nil {
		t.Fatal(err)
	}
}

// FT8, Tune and the PTT test don't key the rig when the guard says no.
func TestFT8GuardStopsKeying(t *testing.T) {
	x := &ft8Tx{guard: func() error { return errTest("not transmitting on 445.000 MHz") }}
	ptt := &fakePTT{}
	x.transmit(nil, 0, "", ptt, make(chan struct{}), time.Second, false)
	if e := ptt.events(); e != "" {
		t.Fatalf("PTT %s", e)
	}
	if st := x.state(); !strings.Contains(st.Status, "445.000") {
		t.Fatalf("status %q", st.Status)
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

type stubSender struct{ fed []rune }

func (s *stubSender) SetWPM(int)                {}
func (s *stubSender) Wants() bool               { return true }
func (s *stubSender) Feed(r rune) bool          { s.fed = append(s.fed, r); return true }
func (s *stubSender) Sent() <-chan rune         { return nil }
func (s *stubSender) Errors() <-chan error      { return nil }
func (s *stubSender) Busy() bool                { return false }
func (s *stubSender) Active(time.Duration) bool { return false }
func (s *stubSender) Abort()                    {}
func (s *stubSender) Close()                    {}

// CW: with the radio on 70 cm and no 144/430 MHz antenna, the queued text is dropped
// and nothing is keyed.
func TestCWWontKeyWithoutTheAntenna(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	k := &stubSender{}
	e.mu.Lock()
	e.tx.keyer = k
	e.rigFreq.note("FA432085000;", true, time.Now())
	e.tx.queue = []rune("cq")
	e.tx.tickLocked()
	q, note := len(e.tx.queue), e.note
	e.mu.Unlock()
	if len(k.fed) != 0 || q != 0 || !strings.Contains(note, "432.085") {
		t.Fatalf("fed %q, queue %d, note %q", string(k.fed), q, note)
	}
	e.mu.Lock()
	e.st.VHFAnt = true
	e.tx.queue = []rune("cq")
	e.tx.tickLocked()
	e.mu.Unlock()
	if len(k.fed) == 0 {
		t.Fatal("not sent with the antenna ticked")
	}
}
