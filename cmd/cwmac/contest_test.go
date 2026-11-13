package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeardZone(t *testing.T) {
	for text, want := range map[string]int{
		"KN0BLE 5NN 14":        14,
		"TU 5NN 5 ":            5,
		"599 T5":               5,
		"5NN 1T":               10,
		"DL1ABC DL1ABC":        0,
		"5NN":                  0,
		"5NN 99":               0,  // no zone 99
		"5NN 14 TEST 5NN 15 E": 15, // the latest
		"R 5NN 25?":            25,
	} {
		if got := heardZone(text); got != want {
			t.Errorf("%q: got %d, want %d", text, got, want)
		}
	}
}

func contestEngine(t *testing.T) *Engine {
	dataDir = t.TempDir()
	t.Cleanup(func() { dataDir = "" })
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(`{"contest":"cqww","freq":"14.025"}`))
	if e.st.Contest != "cqww" || e.st.ContestSince == 0 {
		t.Fatalf("contest on: %+v", e.st)
	}
	e.st.ContestSince = time.Now().Add(-time.Minute).Unix()
	return e
}

func TestContestRunFlow(t *testing.T) {
	e := contestEngine(t)
	e.mu.Lock()
	defer e.mu.Unlock()
	sent := func() string { s := strings.TrimSpace(string(e.tx.queue)); e.tx.queue = nil; return s }

	if err := e.contestEnterLocked("", 0); err != nil || sent() != "CQ TEST KN0BLE KN0BLE TEST" {
		t.Fatalf("empty Call box: CQ: %v", err)
	}
	if err := e.contestEnterLocked("dl1abc", 0); err != nil || sent() != "DL1ABC 5NN 5" {
		t.Fatalf("a call: their call and the exchange: %v", err)
	}
	var s stateMsg
	e.fillContestLocked(&s)
	c := s.Contest
	if c == nil || !c.Sent || c.Zone != 14 || c.ZoneFrom != "guess" || !c.Check.NewCountry || c.Check.Points != 3 || c.Check.Country != "Fed. Rep. of Germany" {
		t.Fatalf("state: %+v", c)
	}
	// They answer with their zone (decoded): Enter sends TU and logs it.
	e.assist.Rx("KN0BLE 5NN 14", time.Now())
	if err := e.contestEnterLocked("DL1ABC", 0); err != nil || sent() != "TU KN0BLE" {
		t.Fatalf("TU: %v", err)
	}
	q := e.logbook.Recent(1)
	if len(q) != 1 || q[0].Call != "DL1ABC" || q[0].CQZ != 14 || q[0].MyCQZ != 5 || q[0].Contest != cqwwID || q[0].Band != "20m" {
		t.Fatalf("logged %+v", q)
	}
	if e.call != "" || e.zoneRcvd != 0 || !strings.Contains(e.note, "New multiplier") {
		t.Fatalf("after logging: call %q zone %d note %q", e.call, e.zoneRcvd, e.note)
	}
	// A W6 calls: the typed zone wins over cty.dat's guess (5).
	e.assist.Logged(time.Now())
	e.contestEnterLocked("W6XYZ", 0)
	sent()
	if err := e.contestEnterLocked("W6XYZ", 3); err != nil || sent() != "TU KN0BLE" {
		t.Fatalf("TU W6: %v", err)
	}
	if q := e.logbook.Recent(1)[0]; q.CQZ != 3 {
		t.Fatalf("typed zone: %+v", q)
	}
	s = stateMsg{}
	e.fillContestLocked(&s)
	if sum := s.Contest.Summary; sum.Total.QSOs != 2 || sum.Total.Points != 3 || sum.Total.Zones != 2 || sum.Total.Countries != 2 || sum.Score != 12 {
		t.Fatalf("score: %+v", sum)
	}
	// Same station again on 20 m: a dupe.
	e.setCallLocked("DL1ABC", false)
	s = stateMsg{}
	e.fillContestLocked(&s)
	if !s.Contest.Check.Dupe || !s.Dupe {
		t.Fatalf("dupe: %+v", s.Contest.Check)
	}
}

func TestContestSPFlow(t *testing.T) {
	e := contestEngine(t)
	e.applySettings(json.RawMessage(`{"contestSP":true}`))
	e.mu.Lock()
	defer e.mu.Unlock()
	sent := func() string { s := strings.TrimSpace(string(e.tx.queue)); e.tx.queue = nil; return s }
	if err := e.contestEnterLocked("", 0); err == nil {
		t.Fatal("S&P with no call should say so")
	}
	if err := e.contestEnterLocked("JA1ZZZ", 0); err != nil || sent() != "KN0BLE" {
		t.Fatalf("S&P: my call: %v", err)
	}
	if err := e.contestEnterLocked("JA1ZZZ", 25); err != nil || sent() != "TU 5NN 5" {
		t.Fatalf("S&P: TU + exchange: %v", err)
	}
	if q := e.logbook.Recent(1)[0]; q.Call != "JA1ZZZ" || q.CQZ != 25 {
		t.Fatalf("logged %+v", q)
	}
}

func TestContestCabrilloAndFreshLog(t *testing.T) {
	e := contestEngine(t)
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, _, err := e.writeCabrilloLocked(); err == nil {
		t.Fatal("empty log: nothing to write")
	}
	e.zoneRcvd = 14
	if err := e.logLocked("DL1ABC"); err != nil {
		t.Fatal(err)
	}
	p, n, err := e.writeCabrilloLocked()
	if err != nil || n != 1 || filepath.Base(p) != "KN0BLE-CQWW-CW-"+time.Now().UTC().Format("2006")+".log" {
		t.Fatalf("%s %d %v", p, n, err)
	}
	b, _ := os.ReadFile(p)
	for _, want := range []string{"CALLSIGN: KN0BLE", "LOCATION: FL", "CATEGORY-ASSISTED: ASSISTED", "CATEGORY-POWER: LOW", "CLAIMED-SCORE: 6",
		" 599 05     DL1ABC        599 14"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q:\n%s", want, b)
		}
	}
	// A regular contact (contest mode off) isn't in the contest log.
	e.st.Contest = ""
	if err := e.logLocked("W1AW"); err != nil {
		t.Fatal(err)
	}
	e.st.Contest = "cqww"
	if len(e.contestLogLocked().Log()) != 1 {
		t.Fatal("a regular contact got into the contest log")
	}
	// A new contest log leaves the old contacts out of the score.
	e.st.ContestSince = time.Now().Add(time.Second).Unix()
	if len(e.contestLogLocked().Log()) != 0 {
		t.Fatal("new log should be empty")
	}
}

func TestContestWeekendStartsTheLog(t *testing.T) {
	e := contestEngine(t)
	e.mu.Lock()
	defer e.mu.Unlock()
	start := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	e.st.ContestSince = start.Add(-48 * time.Hour).Unix() // practice on Thursday
	e.contestTickLocked(start.Add(-time.Minute))
	if e.st.ContestSince != start.Add(-48*time.Hour).Unix() {
		t.Fatal("before the start: nothing changes")
	}
	e.contestTickLocked(start.Add(time.Minute))
	if e.st.ContestSince != start.Unix() || !strings.Contains(e.note, "started") {
		t.Fatalf("at the start the log starts at 0000Z: %v %q", time.Unix(e.st.ContestSince, 0).UTC(), e.note)
	}
}

func TestAutoLogPerMode(t *testing.T) {
	e := contestEngine(t)
	e.applySettings(json.RawMessage(`{"contestAutoLog":false}`))
	if !e.st.AutoLog || e.st.ContestAutoLog {
		t.Fatalf("per mode: everyday %v, contest %v", e.st.AutoLog, e.st.ContestAutoLog)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.logbook.Entries())
	// Contest, Auto-log off: Enter's TU goes out but nothing is logged; the call stays.
	e.contestEnterLocked("DL1ABC", 0)
	e.contestEnterLocked("DL1ABC", 14)
	if !strings.Contains(string(e.tx.queue), "TU KN0BLE") || len(e.logbook.Entries()) != n || e.call != "DL1ABC" || !strings.Contains(e.note, "Not logged") {
		t.Fatalf("queue %q, entries %d, call %q, note %q", string(e.tx.queue), len(e.logbook.Entries()), e.call, e.note)
	}
	if err := e.logLocked(e.call); err != nil || len(e.logbook.Entries()) != n+1 {
		t.Fatalf("Log QSO still logs: %v", err)
	}
	// Everyday CW, Auto-log on: TU (F4) logs.
	e.st.Contest = ""
	e.tx.queue = nil
	e.setCallLocked("W1AW", false)
	if err := e.tx.sendMacroLocked(3, "W1AW", true); err != nil || len(e.logbook.Entries()) != n+2 {
		t.Fatalf("everyday TU should log: %v", err)
	}
	// and off there too, when switched off
	e.st.AutoLog = false
	e.setCallLocked("K1ABC", false)
	if err := e.tx.sendMacroLocked(3, "K1ABC", true); err != nil || len(e.logbook.Entries()) != n+2 {
		t.Fatalf("Auto-log off: TU shouldn't log: %v", err)
	}
}
