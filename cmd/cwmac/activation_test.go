package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParkAndActivationState(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(`{"park":" us-1234 ","freq":"14.058"}`))
	if e.st.Park != "US-1234" {
		t.Fatalf("park %q", e.st.Park)
	}
	e.applySettings(json.RawMessage(`{"park":"pota"}`)) // not a reference: kept as it was
	if e.st.Park != "US-1234" || !strings.Contains(e.note, "US-1234") {
		t.Fatalf("park %q note %q", e.st.Park, e.note)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range []string{"K1ABC", "W1AW", "K1ABC"} {
		if err := e.logLocked(c); err != nil {
			t.Fatal(err)
		}
	}
	var s stateMsg
	e.fillOpsLocked(&s)
	a := s.Act
	if a.Park != "US-1234" || a.Valid != 2 || a.Dupes != 1 || a.Total != 3 || a.Need != 10 || a.Modes["CW"] != 2 || !strings.HasPrefix(a.Last, "K1ABC ") {
		t.Fatalf("%+v", a)
	}
	if q := e.logbook.Recent(1)[0]; q.Park != "US-1234" || q.Band != "20m" {
		t.Fatalf("logged %+v", q)
	}
	// {PARK} in a macro is the main screen's park
	e.tx.saveMacrosLocked("PARK = US-9999\nF1 = CQ | CQ POTA {PARK} K\n")
	e.tx.queue = nil
	if err := e.tx.sendMacroLocked(0, "", false); err != nil || !strings.Contains(string(e.tx.queue), "US-1234") {
		t.Fatalf("%v %q", err, string(e.tx.queue))
	}
	// with the box empty, the macros' PARK still works (older setups)
	e.st.Park = ""
	if e.parkLocked() != "US-9999" {
		t.Fatal(e.parkLocked())
	}
}
