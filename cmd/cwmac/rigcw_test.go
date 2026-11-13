//go:build linux

package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestRigCWControls(t *testing.T) {
	e, f := testEngine710(t)
	r := e.rigState()
	if r.Error != "" || r.Controls == nil {
		t.Fatalf("%+v", r)
	}
	c := r.Controls
	if c.BreakIn || c.FullBreakIn || c.DelayMs != 200 || c.WidthHz != 500 || c.PitchHz != 600 || c.NotchHz != 1000 {
		t.Fatalf("controls: %+v", *c)
	}
	for _, tc := range []struct {
		a       rigAction
		k, want string
	}{
		{rigAction{"breakIn", "1"}, "BI", "1"},
		{rigAction{"breakInType", "full"}, "EX020116", "1"},
		{rigAction{"breakInDelay", "300"}, "SD", "06"},
		{rigAction{"width", "250"}, "SH0", "005"},
		{rigAction{"apf", "1"}, "CO02", "0001"},
		{rigAction{"ifShift", "-100"}, "IS0", "0-0100"},
		{rigAction{"pitch", "650"}, "KP", "35"},
		{rigAction{"autoNotch", "1"}, "BC0", "1"},
		{rigAction{"notch", "1"}, "BP00", "001"},
		{rigAction{"notchFreq", "1500"}, "BP01", "150"},
	} {
		if r := e.doRig(tc.a); r.Error != "" || f.get(tc.k) != tc.want {
			t.Errorf("%+v: %s = %q (want %q) %s", tc.a, tc.k, f.get(tc.k), tc.want, r.Error)
		}
	}
	r = e.rigState()
	if c := r.Controls; !c.BreakIn || !c.FullBreakIn || c.WidthHz != 250 || !c.APF || c.IFShiftHz != -100 || c.PitchHz != 650 || !c.AutoNotch || !c.Notch || c.NotchHz != 1500 {
		t.Fatalf("read back: %+v", *c)
	}
	if r := e.doRig(rigAction{"width", "275"}); r.Error == "" {
		t.Fatal("not a width the radio has")
	}
}

func TestRigKeyerSpeedFollowsTheApp(t *testing.T) {
	e, f := testEngine710(t)
	e.applySettings(json.RawMessage(`{"wpm":22}`))
	e.rigState()
	if f.get("KS") != "022" {
		t.Fatalf("KS %s", f.get("KS"))
	}
	e.applySettings(json.RawMessage(`{"rigSync":false,"wpm":25}`))
	e.rigState()
	if f.get("KS") != "022" {
		t.Fatal("sync off: leave the radio's speed alone")
	}
}

func TestPowerRememberedPerBand(t *testing.T) {
	e, f := testEngine710(t) // 20 m, 50 W
	e.rigState()
	if got := e.st.PowerByBand["home/20m"]; got != 50 {
		t.Fatalf("first read: remember 50 W on 20 m, got %v", e.st.PowerByBand)
	}
	e.doRig(rigAction{"power", "30"})
	e.rigState()
	if got := e.st.PowerByBand["home/20m"]; got != 30 {
		t.Fatalf("set on 20 m: %v", e.st.PowerByBand)
	}
	// To 40 m (the radio's own 100 W there): new band, remembered as it is.
	f.mu.Lock()
	f.v["FA"], f.v["PC"] = "007030000", "100"
	f.mu.Unlock()
	e.rigState()
	// Back to 20 m: 30 W again, set by the app.
	f.mu.Lock()
	f.v["FA"] = "014058000"
	f.mu.Unlock()
	if r := e.rigState(); r.State.PowerW != 30 || f.get("PC") != "030" || !strings.Contains(e.note, "30 W on 20m") {
		t.Fatalf("back on 20 m: PC %s, note %q", f.get("PC"), e.note)
	}
	// POTA: its own power per band.
	e.applySettings(json.RawMessage(`{"potaMode":true}`))
	e.rigState()
	e.doRig(rigAction{"power", "5"})
	e.rigState()
	e.applySettings(json.RawMessage(`{"potaMode":false}`))
	e.rigState()
	if f.get("PC") != "030" {
		t.Fatalf("home again: 30 W, got %s", f.get("PC"))
	}
	e.applySettings(json.RawMessage(`{"potaMode":true}`))
	e.rigState()
	if f.get("PC") != "005" || e.st.PowerByBand["pota/20m"] != 5 {
		t.Fatalf("POTA again: 5 W, got %s (%v)", f.get("PC"), e.st.PowerByBand)
	}
}

func TestMeters(t *testing.T) {
	e, f := testEngine710(t)
	m := e.meters()
	if m.Error != "" || m.TX || m.S != "S7" {
		t.Fatalf("receiving: %+v", m)
	}
	f.mu.Lock()
	f.v["PO"] = "120"
	f.mu.Unlock()
	if m := e.meters(); !m.TX || m.PowerW <= 0 || m.SWR != 1.2 {
		t.Fatalf("transmitting: %+v", m)
	}
}

// A steady tone at hz in the waterfall's last second (5 Hz columns from 200 Hz).
func fakeWaterfallTone(e *Engine, hz float64) {
	e.wfHist = nil
	for r := 0; r < 8; r++ {
		row := make([]byte, cwWfCols)
		for i := range row {
			f := specLo + float64(i)*5
			row[i] = byte(math.Max(0, 200-math.Abs(f-hz)*8))
		}
		e.wfHist = append(e.wfHist, row)
	}
}

func TestZeroIn(t *testing.T) {
	e, f := testEngine710(t) // CW-U, dial 14.058000, radio pitch 600 Hz
	e.mu.Lock()
	e.st.Mode, e.st.Pitch = "cw", 720
	fakeWaterfallTone(e, 735) // the decoder's on a station at 735 Hz
	e.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); r.Error != "" {
		t.Fatal(r.Error)
	}
	// 135 Hz above the pitch in CW-U: the dial goes up 135 Hz, the decoder to 600.
	if f.get("FA") != "014058135" || e.st.Pitch != 600 {
		t.Fatalf("FA %s, pitch %v", f.get("FA"), e.st.Pitch)
	}
	// CW-L: the other way.
	f.mu.Lock()
	f.v["MD0"], f.v["FA"] = "7", "007030000"
	f.mu.Unlock()
	e.mu.Lock()
	e.st.Pitch = 560
	fakeWaterfallTone(e, 550)
	e.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); r.Error != "" || f.get("FA") != "007030050" {
		t.Fatalf("CW-L: FA %s %s", f.get("FA"), r.Error)
	}
	// Never while transmitting, never outside CW, never with nothing there.
	f.mu.Lock()
	f.v["PO"] = "120"
	f.mu.Unlock()
	e.mu.Lock()
	fakeWaterfallTone(e, 650)
	e.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "transmitting") {
		t.Fatalf("keyed: %q", r.Error)
	}
	f.mu.Lock()
	f.v["PO"], f.v["MD0"] = "000", "2"
	f.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "isn't in CW") {
		t.Fatalf("USB: %q", r.Error)
	}
	e.mu.Lock()
	e.wfHist = nil
	e.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "no signal") {
		t.Fatalf("nothing there: %q", r.Error)
	}
}
