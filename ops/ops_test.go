package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsCall(t *testing.T) {
	good := []string{"W1AW", "KN0BLE", "K4XYZ", "VE3ABC", "9A1A", "A71AB", "2E0ABC", "KN0BLE/P", "VE3/W1AW", "JA1XYZ", "DL1ABC/QRP"}
	bad := []string{"CQ", "5NN", "599", "TU", "73", "TU5NN", "R5NN", "QRZ", "E", "K1A", "ABCDEF", "W1", "1234"}
	for _, c := range good {
		if !IsCall(c) {
			t.Errorf("IsCall(%q) = false, want true", c)
		}
	}
	for _, c := range bad {
		if IsCall(c) {
			t.Errorf("IsCall(%q) = true, want false", c)
		}
	}
}

func TestFindCallsAndRST(t *testing.T) {
	if g := FindCalls("K4XYZK4XYZ", ""); len(g) != 1 || g[0] != "K4XYZ" {
		t.Fatalf("doubled call: %v", g)
	}
	got := FindCalls("KN0BLE DE W1AW W1AW K4XYZ? TU 5NN FL", "KN0BLE")
	if strings.Join(got, ",") != "W1AW,K4XYZ" {
		t.Fatalf("FindCalls = %v", got)
	}
	for in, want := range map[string]string{"TU 5NN FL": "599", "UR 579 579": "579", "R 55N": "559", "W1AW": "", "559 TU 5NN": "599"} {
		if g := FindRST(in); g != want {
			t.Errorf("FindRST(%q) = %q want %q", in, g, want)
		}
	}
	if s, ok := StripLog("TU 73 E E {LOG}"); !ok || s != "TU 73 E E" {
		t.Fatalf("StripLog = %q %v", s, ok)
	}
}

func TestMigrateMacros(t *testing.T) {
	old := "# hi\r\nMYCALL  = KN0BLE\r\nF4 = TU      | TU 73 E E\r\nF8 = 73      | {CALL} TU 73 DE {MYCALL} E E\r\n"
	out, ok := MigrateMacros(old)
	if !ok || !strings.Contains(out, "TU 73 E E {LOG}\r\n") || !strings.Contains(out, "{MYCALL} E E {LOG}\r\n") || !strings.Contains(out, "# {LOG} in a macro") {
		t.Fatalf("migrate:\n%s", out)
	}
	if _, again := MigrateMacros(out); again {
		t.Fatal("migrated twice")
	}
	custom := "F4 = TU | TU 72 GL\n"
	if _, ok := MigrateMacros(custom); ok {
		t.Fatal("changed a customised macro")
	}
}

func TestParseFreq(t *testing.T) {
	cases := map[string][2]string{
		"14.058": {"14.058", "20m"}, "14058": {"14.058", "20m"}, "7030": {"7.03", "40m"}, "20m": {"", "20m"},
		"40": {"", "40m"}, "28.040": {"28.04", "10m"}, "": {"", ""}, "abc": {"", ""}, "50.090": {"50.09", "6m"},
	}
	for in, want := range cases {
		f, b := ParseFreq(in)
		if f != want[0] || b != want[1] {
			t.Errorf("ParseFreq(%q) = %q,%q want %q,%q", in, f, b, want[0], want[1])
		}
	}
}

func TestLogbook(t *testing.T) {
	dir := t.TempDir()
	l := OpenLog(dir)
	t0 := time.Date(2026, 10, 12, 23, 59, 30, 0, time.UTC)
	q := QSO{Call: "W1AW", Start: t0, Freq: "14.058", Band: "20m", RSTSent: "599", RSTRcvd: "579", MyCall: "KN0BLE", Park: "US-1234", MyState: "FL"}
	if d, err := l.Add(q); err != nil || d {
		t.Fatal(d, err)
	}
	q2 := q
	q2.Start = t0.Add(10 * time.Second)
	if d, _ := l.Add(q2); !d {
		t.Fatal("second W1AW same band/day not a dupe")
	}
	q3 := q
	q3.Call, q3.Start = "K4XYZ", t0.Add(time.Minute) // next UTC day
	l.Add(q3)
	pota1 := filepath.Join(dir, "KN0BLE@US-1234-20261012.adi")
	pota2 := filepath.Join(dir, "KN0BLE@US-1234-20261013.adi")
	b, err := os.ReadFile(pota1)
	if err != nil || strings.Count(string(b), "<EOR>") != 2 {
		t.Fatalf("pota file 1: %v\n%s", err, b)
	}
	for _, want := range []string{"<CALL:4>W1AW", "<QSO_DATE:8>20261012", "<TIME_ON:6>235930", "<BAND:3>20m", "<MODE:2>CW",
		"<RST_RCVD:3>579", "<STATION_CALLSIGN:6>KN0BLE", "<OPERATOR:6>KN0BLE", "<MY_SIG:4>POTA", "<MY_SIG_INFO:7>US-1234", "<MY_STATE:2>FL"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s", want)
		}
	}
	if _, err := os.Stat(pota2); err != nil {
		t.Fatal("no file for the next UTC day")
	}
	// reload, undo
	l2 := OpenLog(dir)
	if len(l2.Recent(10)) != 3 || l2.Recent(1)[0].Call != "K4XYZ" {
		t.Fatalf("reload: %+v", l2.Recent(10))
	}
	if !l2.IsDupe("W1AW/P", "20m", "US-1234", t0) {
		t.Fatal("portable form should dupe")
	}
	if u, err := l2.Undo(); err != nil || u.Call != "K4XYZ" {
		t.Fatal(u, err)
	}
	if _, err := os.Stat(pota2); !os.IsNotExist(err) {
		t.Fatal("undo left the now-empty POTA file")
	}
	if n := len(OpenLog(dir).Recent(10)); n != 2 {
		t.Fatalf("after undo: %d", n)
	}
}

// ---- Auto-CQ / auto-reply simulation ----

type sim struct {
	t       *testing.T
	a       *Assist
	now     time.Time
	txUntil time.Time
	sent    []string
	call    string
	notes   []string
	lag     time.Duration
}

func newSim(t *testing.T) *sim {
	s := &sim{t: t, a: NewAssist(), now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return s
}

// run advances time in 25 ms steps; sig says when the other station is keying, rx
// delivers decoded text at given offsets.
func (s *sim) run(d time.Duration, sig func(time.Time) bool, rx map[time.Duration]string) {
	start := s.now
	delivered := map[time.Duration]bool{}
	for s.now.Sub(start) < d {
		s.now = s.now.Add(25 * time.Millisecond)
		for off, txt := range rx {
			if !delivered[off] && s.now.Sub(start) >= off {
				delivered[off] = true
				s.a.Rx(txt, s.now)
			}
		}
		busy := s.now.Before(s.txUntil)
		in := Inputs{Now: s.now, TxBusy: busy, TxOn: true, Signal: !busy && sig != nil && sig(s.now), RxWPM: 18, Lag: s.lag, MyCall: "KN0BLE"}
		for _, a := range s.a.Tick(in) {
			switch a.Kind {
			case SendMacro:
				s.sent = append(s.sent, []string{"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8"}[a.Macro]+":"+a.Call)
				s.txUntil = s.now.Add(3 * time.Second)
			case SetCall:
				s.call = a.Call
			case Note:
				s.notes = append(s.notes, a.Text)
			}
		}
	}
}

func TestAutoCQRepeatsUntilAnswered(t *testing.T) {
	s := newSim(t)
	s.a.Cfg.ListenSecs = 5
	s.a.SetAutoCQ(true, s.now)
	s.run(20*time.Second, nil, nil) // nobody home: CQ at 0, ~8, ~16
	if len(s.sent) != 3 {
		t.Fatalf("expected 3 CQs in 20 s, got %v", s.sent)
	}
	// Someone answers during the listening period: Auto-CQ pauses (auto-reply off).
	s.run(2*time.Second, nil, nil)
	at := s.now
	s.run(10*time.Second, func(n time.Time) bool { return n.Sub(at) < 2*time.Second }, map[time.Duration]string{1500 * time.Millisecond: "W1AW W1AW"})
	if len(s.sent) != 3 || !strings.Contains(strings.Join(s.notes, "|"), "paused") {
		t.Fatalf("should have paused: sent=%v notes=%v", s.sent, s.notes)
	}
	if c := s.a.Calls(); len(c) == 0 || c[0] != "W1AW" {
		t.Fatalf("calls %v", c)
	}
	// Operator works him and logs: Auto-CQ resumes after listening.
	s.a.Manual(false, s.now)
	s.txUntil = s.now.Add(2 * time.Second)
	s.a.Logged(s.now)
	s.run(9*time.Second, nil, nil)
	if len(s.sent) != 4 {
		t.Fatalf("should have resumed CQ after logging: %v", s.sent)
	}
}

func TestAutoCQWaitsForBusyFrequency(t *testing.T) {
	s := newSim(t)
	s.a.Cfg.ListenSecs = 5
	// A carrier/QRM keeps the classic key down on and off, no decodable text.
	s.a.SetAutoCQ(true, s.now)
	s.run(100*time.Millisecond, nil, nil)
	s.txUntil = s.now // CQ "sent" instantly
	s.run(14*time.Second, func(n time.Time) bool { return n.UnixMilli()/300%2 == 0 }, nil)
	// It holds off while the frequency is busy, but not forever (listen + 8 s max).
	if len(s.sent) != 2 {
		t.Fatalf("want 2 CQs (one held back, one after the cap), got %v", s.sent)
	}
}

func TestAutoReplySingleCaller(t *testing.T) {
	for _, lag := range []time.Duration{0, 2500 * time.Millisecond} {
		s := newSim(t)
		s.lag = lag
		s.a.Cfg.AutoReply = true
		s.a.SetAutoCQ(true, s.now)
		s.run(4*time.Second, nil, nil) // CQ goes out (3 s)
		at := s.now
		keying := func(n time.Time) bool { return n.Sub(at) < 2500*time.Millisecond }
		s.run(12*time.Second, keying, map[time.Duration]string{lag + 1000*time.Millisecond: "K4X", lag + 2000*time.Millisecond: "YZ K4XYZ"})
		if len(s.sent) != 2 || s.sent[1] != "F3:K4XYZ" || s.call != "K4XYZ" {
			t.Fatalf("lag %v: sent=%v call=%q notes=%v", lag, s.sent, s.call, s.notes)
		}
		// The reply must not start until the caller stopped and the text caught up.
		if !strings.Contains(strings.Join(s.notes, "|"), "answered K4XYZ") {
			t.Fatalf("notes %v", s.notes)
		}
		// No more CQs while it's the operator's turn.
		s.run(15*time.Second, nil, nil)
		if len(s.sent) != 2 {
			t.Fatalf("CQ'd during the contact: %v", s.sent)
		}
	}
}

func TestAutoReplyPileupAsksOperator(t *testing.T) {
	s := newSim(t)
	s.a.Cfg.AutoReply = true
	s.a.SetAutoCQ(true, s.now)
	s.run(4*time.Second, nil, nil)
	at := s.now
	s.run(8*time.Second, func(n time.Time) bool { return n.Sub(at) < 2*time.Second }, map[time.Duration]string{time.Second: "W1AW K4XYZ"})
	if len(s.sent) != 1 || !strings.Contains(strings.Join(s.notes, "|"), "click one") {
		t.Fatalf("pileup should not auto-reply: sent=%v notes=%v", s.sent, s.notes)
	}
}

func TestAutoReplyAfterManualCQ(t *testing.T) {
	s := newSim(t)
	s.a.Cfg.AutoReply = true // Auto-CQ not ticked
	s.a.Manual(true, s.now)
	s.txUntil = s.now.Add(3 * time.Second)
	s.run(4*time.Second, nil, nil)
	at := s.now
	s.run(8*time.Second, func(n time.Time) bool { return n.Sub(at) < 2*time.Second }, map[time.Duration]string{time.Second: "N4ABC N4ABC"})
	if len(s.sent) != 1 || s.sent[0] != "F3:N4ABC" {
		t.Fatalf("sent %v notes %v", s.sent, s.notes)
	}
	// And a manual CQ that nobody answers doesn't turn into Auto-CQ.
	s2 := newSim(t)
	s2.a.Cfg.AutoReply = true
	s2.a.Manual(true, s2.now)
	s2.run(30*time.Second, nil, nil)
	if len(s2.sent) != 0 {
		t.Fatalf("manual CQ repeated: %v", s2.sent)
	}
}

func TestTypingPausesAndEscStops(t *testing.T) {
	s := newSim(t)
	s.a.SetAutoCQ(true, s.now)
	s.run(time.Second, nil, nil)
	s.a.Manual(false, s.now)
	s.run(25*time.Second, nil, nil)
	if len(s.sent) != 1 {
		t.Fatalf("typing should pause: %v", s.sent)
	}
	s.a.Stop()
	s.run(60*time.Second, nil, nil)
	if len(s.sent) != 1 || s.a.Armed() {
		t.Fatalf("Esc should stop: %v", s.sent)
	}
	// Transmit switched off: Auto-CQ turns itself off.
	s.a.SetAutoCQ(true, s.now)
	acts := s.a.Tick(Inputs{Now: s.now, TxOn: false})
	if s.a.Armed() || len(acts) == 0 || acts[0].Kind != AutoCQOff {
		t.Fatal("Auto-CQ without transmit should switch off")
	}
}

func TestSpeedMatch(t *testing.T) {
	a := NewAssist()
	now := time.Now()
	if a.SendWPM(20, now) != 20 {
		t.Fatal("off: user speed")
	}
	a.Cfg.MatchSpeed = true
	a.Tick(Inputs{Now: now, Signal: true, RxWPM: 14.4})
	if w := a.SendWPM(20, now); w != 14 {
		t.Fatalf("match: %d", w)
	}
	a.Tick(Inputs{Now: now, Signal: true, RxWPM: 40})
	if w := a.SendWPM(20, now); w != 30 {
		t.Fatalf("clamp: %d", w)
	}
	if w := a.SendWPM(20, now.Add(2*time.Minute)); w != 20 {
		t.Fatalf("stale: %d", w)
	}
	a.Tick(Inputs{Now: now, TxBusy: true, Signal: true, RxWPM: 25}) // our own keying never counts
	if a.TheirWPM(now) != 40 {
		t.Fatal("measured own signal")
	}
}

func TestRSTAndStart(t *testing.T) {
	a := NewAssist()
	t0 := time.Now()
	a.UsedCall("W1AW", t0)
	a.Rx("W1AW TU 5NN FL ", t0.Add(time.Second))
	if a.RSTRcvd() != "599" || !a.StartTime("w1aw", t0.Add(time.Minute)).Equal(t0) {
		t.Fatal(a.RSTRcvd())
	}
	a.Logged(t0)
	if a.RSTRcvd() != "" {
		t.Fatal("RST not reset")
	}
}

func TestIsAnswer(t *testing.T) {
	no := []string{"E E", "EE", "I", "EE HH E E", "73 E E", "TU EE", "TU 73", "73EE", "GL 73 TU", "R R", "5 S H", "T E E ?", "TNX FB 73", "SK", "B3 E E", "K4", "7T TU"}
	yes := []string{"K4ABC", "K4A BC", "DE W1AW", "QRZ?", "5NN", "TU 5NN FL", "AGN", "K4ABC K4ABC", "N0CALL/P"}
	for _, s := range no {
		if IsAnswer(s) {
			t.Errorf("IsAnswer(%q) = true", s)
		}
	}
	for _, s := range yes {
		if !IsAnswer(s) {
			t.Errorf("IsAnswer(%q) = false", s)
		}
	}
}

func TestAutoCQIgnoresSignOffs(t *testing.T) {
	for _, reply := range []string{"E E", "EE", "73 E E", "TU EE", "I", "B3 E E"} {
		s := newSim(t)
		s.a.Cfg.ListenSecs = 5
		s.a.Cfg.AutoReply = true
		s.a.SetAutoCQ(true, s.now)
		// operator worked someone and sent TU {LOG}; the hunter answers with a sign-off
		s.a.Manual(false, s.now)
		s.txUntil = s.now.Add(2 * time.Second)
		s.a.Logged(s.now)
		s.run(3*time.Second, nil, nil)
		at := s.now
		s.run(10*time.Second, func(n time.Time) bool { return n.Sub(at) < 800*time.Millisecond }, map[time.Duration]string{time.Second: reply})
		if len(s.sent) != 1 || !strings.HasPrefix(s.sent[0], "F1") {
			t.Errorf("reply %q: want the next CQ, got sent=%v notes=%v", reply, s.sent, s.notes)
		}
	}
}

func TestTailEnderAfterTU(t *testing.T) {
	s := newSim(t)
	s.a.Cfg.ListenSecs = 5
	s.a.Cfg.AutoReply = true
	s.a.SetAutoCQ(true, s.now)
	s.a.Manual(false, s.now)
	s.txUntil = s.now.Add(2 * time.Second)
	s.a.Logged(s.now)
	s.run(2500*time.Millisecond, nil, nil)
	at := s.now
	// "E E" from the last hunter, then the next one calls before any CQ
	s.run(10*time.Second, func(n time.Time) bool { return n.Sub(at) < 3*time.Second },
		map[time.Duration]string{300 * time.Millisecond: "E E ", 1500 * time.Millisecond: "W4XYZ W4XYZ"})
	if len(s.sent) != 1 || s.sent[0] != "F3:W4XYZ" {
		t.Fatalf("want a reply to W4XYZ and no CQ: sent=%v notes=%v", s.sent, s.notes)
	}
}
