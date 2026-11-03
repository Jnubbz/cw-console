package main

import "testing"

func dec(slot, msg string, snr int) ft8Decode { return ft8Decode{Slot: slot, Msg: msg, SNR: snr} }

func newSeq() *ft8Seq { return &ft8Seq{MyCall: "KN0BLE", MyGrid: "EL98", CQPota: true, AutoCQ: true} }

func want(t *testing.T, s *ft8Seq, next string) {
	t.Helper()
	if got := s.Next(); got != next {
		t.Fatalf("next = %q, want %q (step %s)", got, next, stepNames[s.step])
	}
}

func TestSeqActivator(t *testing.T) {
	s := newSeq()
	s.StartCQ(0)
	want(t, s, "CQ POTA KN0BLE EL98")
	// nobody: keep calling CQ
	if l := s.Heard("130015", []ft8Decode{dec("130015", "CQ W9XYZ EN52", 0)}); l != nil {
		t.Fatal("logged nothing")
	}
	want(t, s, "CQ POTA KN0BLE EL98")
	// two callers: the stronger first
	l := s.Heard("130045", []ft8Decode{dec("130045", "KN0BLE W1ABC FN42", -12), dec("130045", "KN0BLE K4XYZ EM73", -3)})
	if l != nil {
		t.Fatal("too early to log")
	}
	want(t, s, "K4XYZ KN0BLE -03")
	// quick then full decode of the same slot: no double counting, no change
	s.Heard("130115", []ft8Decode{dec("130115", "KN0BLE K4XYZ R-08", -4)})
	want(t, s, "K4XYZ KN0BLE RR73")
	if l := s.Heard("130115", []ft8Decode{dec("130115", "KN0BLE K4XYZ R-08", -4), dec("130115", "KN0BLE W1ABC FN42", -11)}); l != nil {
		t.Fatal("double log")
	}
	want(t, s, "K4XYZ KN0BLE RR73") // RR73 must go out before the next caller
	// (logged when the R-report arrived)
	s2 := newSeq()
	s2.StartCQ(0)
	s2.Heard("130045", []ft8Decode{dec("130045", "KN0BLE K4XYZ EM73", -3)})
	lg := s2.Heard("130115", []ft8Decode{dec("130115", "KN0BLE K4XYZ R-08", -4)})
	if lg == nil || lg.Call != "K4XYZ" || lg.Grid != "EM73" || lg.Sent != "-03" || lg.Rcvd != "-08" {
		t.Fatalf("log %+v", lg)
	}
	// after RR73: they say 73 and W1ABC is still calling -> work W1ABC
	s.Heard("130145", []ft8Decode{dec("130145", "KN0BLE K4XYZ 73", -4), dec("130145", "KN0BLE W1ABC FN42", -10)})
	want(t, s, "W1ABC KN0BLE -10")
	s.Heard("130215", []ft8Decode{dec("130215", "KN0BLE W1ABC R-15", -10)})
	want(t, s, "W1ABC KN0BLE RR73")
	// W1ABC missed the RR73 and repeats R-15: send RR73 again, don't log twice
	if l := s.Heard("130245", []ft8Decode{dec("130245", "KN0BLE W1ABC R-15", -10)}); l != nil {
		t.Fatal("logged twice")
	}
	want(t, s, "W1ABC KN0BLE RR73")
	// silence: done, back to CQ
	s.Heard("130315", nil)
	want(t, s, "CQ POTA KN0BLE EL98")
	// and if W1ABC still missed it, RR73 once more
	s.Heard("130345", []ft8Decode{dec("130345", "KN0BLE W1ABC R-15", -10)})
	want(t, s, "W1ABC KN0BLE RR73")
}

func TestSeqCallerSkipsGrid(t *testing.T) {
	s := newSeq()
	s.StartCQ(1)
	// a caller who jumps straight to a report
	s.Heard("130000", []ft8Decode{dec("130000", "KN0BLE N0XYZ -09", -7)})
	want(t, s, "N0XYZ KN0BLE R-07")
}

func TestSeqHunter(t *testing.T) {
	s := newSeq()
	s.AutoCQ = false
	if err := s.Call("CQ POTA K4XYZ EM73", "130000", -5); err != nil {
		t.Fatal(err)
	}
	if s.parity != 1 {
		t.Fatal("must transmit in the other period")
	}
	want(t, s, "K4XYZ KN0BLE EL98")
	// our own period's decodes are ignored
	s.Heard("130015", []ft8Decode{dec("130015", "KN0BLE K4XYZ -10", 0)})
	want(t, s, "K4XYZ KN0BLE EL98")
	// they work someone else first: keep calling
	s.Heard("130030", []ft8Decode{dec("130030", "W1ABC K4XYZ -02", -5)})
	want(t, s, "K4XYZ KN0BLE EL98")
	s.Heard("130100", []ft8Decode{dec("130100", "KN0BLE K4XYZ -10", -6)})
	want(t, s, "K4XYZ KN0BLE R-06")
	l := s.Heard("130130", []ft8Decode{dec("130130", "KN0BLE K4XYZ RR73", -6)})
	if l == nil || l.Call != "K4XYZ" || l.Grid != "EM73" || l.Rcvd != "-10" || l.Sent != "-06" {
		t.Fatalf("log %+v", l)
	}
	want(t, s, "K4XYZ KN0BLE 73")
	s.Heard("130200", nil)
	want(t, s, "")
}

func TestSeqHunterGivesUp(t *testing.T) {
	s := newSeq()
	s.AutoCQ = false
	s.Call("CQ K4XYZ EM73", "130000", -5)
	slots := []string{"130030", "130100", "130130", "130200", "130230", "130300", "130330", "130400", "130430"}
	for i, sl := range slots {
		s.Heard(sl, nil)
		s.Heard(sl, nil) // full decode of the same slot
		if i < maxCallTries-1 && s.Next() == "" {
			t.Fatalf("gave up after %d", i+1)
		}
	}
	want(t, s, "")
}

func TestSeqReplyToCallerClick(t *testing.T) {
	// clicking a message that's already a call to us picks up the contact there
	s := newSeq()
	s.Call("KN0BLE W1ABC FN42", "130015", -8)
	want(t, s, "W1ABC KN0BLE -08")
	if s.parity != 0 {
		t.Fatal("parity")
	}
}
