//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jnubbz/cw-console/spectrum"
)

func TestRigCWControlsFT710(t *testing.T) {
	e, f := testEngine710(t)
	r := e.rigState()
	if r.Error != "" || r.Controls == nil {
		t.Fatalf("%+v", r)
	}
	if c := r.Controls; c.BreakIn || c.DelayMs != 200 || c.WidthHz != 500 || c.PitchHz != 600 || c.NotchHz != 1000 || len(c.Widths) != 21 {
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

func TestRigCWControlsFT991A(t *testing.T) {
	e, f := testEngine991(t)
	r := e.rigState()
	if r.Error != "" || r.Controls == nil || r.Controls.DelayMs != 200 || r.Controls.WidthHz != 500 {
		t.Fatalf("%+v %+v", r, r.Controls)
	}
	for _, tc := range []struct {
		a       rigAction
		k, want string
	}{
		{rigAction{"breakIn", "1"}, "BI", "1"},
		{rigAction{"breakInType", "full"}, "EX056", "1"}, // menu 056, not 02-01-16
		{rigAction{"breakInDelay", "300"}, "SD", "0300"}, // ms, not a step number
		{rigAction{"width", "800"}, "SH0", "11"},         // its own table
		{rigAction{"ifShift", "-100"}, "IS0", "-0100"},   // IS0, not IS00
		{rigAction{"apf", "1"}, "CO02", "0001"},
		{rigAction{"pitch", "700"}, "KP", "40"},
	} {
		if r := e.doRig(tc.a); r.Error != "" || f.get(tc.k) != tc.want {
			t.Errorf("%+v: %s = %q (want %q) %s", tc.a, tc.k, f.get(tc.k), tc.want, r.Error)
		}
	}
	r = e.rigState()
	if c := r.Controls; !c.FullBreakIn || c.DelayMs != 300 || c.WidthHz != 800 || c.IFShiftHz != -100 || c.PitchHz != 700 {
		t.Fatalf("read back: %+v", *c)
	}
	if r := e.doRig(rigAction{"width", "600"}); r.Error == "" {
		t.Fatal("the FT-991A has no 600 Hz CW width")
	}
	f.mu.Lock()
	for _, c := range f.cmds {
		// The FT-710's forms: menu 02-01-16, a two-digit SD step, SH00nn, IS00±nnnn.
		if strings.HasPrefix(c, "EX020116") || (strings.HasPrefix(c, "SD") && len(c) == 5) ||
			strings.HasPrefix(c, "SH00") || strings.HasPrefix(c, "IS00") {
			t.Errorf("FT-710 form %q sent to an FT-991A", c)
		}
	}
	f.mu.Unlock()
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
	f.mu.Lock()
	f.v["FA"], f.v["PC"] = "007030000", "100" // to 40 m on the radio: its own 100 W there
	f.mu.Unlock()
	e.rigState()
	f.mu.Lock()
	f.v["FA"] = "014058000"
	f.mu.Unlock()
	if r := e.rigState(); r.State.PowerW != 30 || f.get("PC") != "030" || !strings.Contains(e.note, "30 W on 20m") {
		t.Fatalf("back on 20 m: PC %s, note %q", f.get("PC"), e.note)
	}
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

// fakeTone: the last second of the spectrum with a steady tone at hz, 30 dB over the noise.
func fakeTone(e *Engine, hz float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.zSpan = spectrum.Span{LoHz: 200, HiHz: 1400, Cols: 240}
	e.zFloor = -100
	e.zHist = nil
	for r := 0; r < 8; r++ {
		row := make([]float64, 240)
		for i := range row {
			f := 200 + (float64(i)+0.5)*5
			d := (f - hz) / 20
			row[i] = -100 + max(0, 30-d*d)
		}
		e.zHist = append(e.zHist, row)
	}
}

func TestZeroIn(t *testing.T) {
	e, f := testEngine991(t) // 7.030 CW-L, radio pitch 600 Hz
	e.mu.Lock()
	e.st.Mode, e.st.Pitch = "cw", 540
	e.mu.Unlock()
	fakeTone(e, 550) // the decoder's on a station at 550 Hz
	if r := e.doRig(rigAction{"zeroIn", ""}); r.Error != "" {
		t.Fatal(r.Error)
	}
	// CW-L: 50 Hz under the pitch means 50 Hz above the dial.
	if f.get("FA") != "007030050" || e.st.Pitch != 600 {
		t.Fatalf("CW-L: FA %s, pitch %v", f.get("FA"), e.st.Pitch)
	}
	f.mu.Lock()
	f.v["MD0"], f.v["FA"] = "3", "144075000" // 2 m, CW-U
	f.mu.Unlock()
	e.mu.Lock()
	e.st.Pitch = 720
	e.mu.Unlock()
	fakeTone(e, 735)
	if r := e.doRig(rigAction{"zeroIn", ""}); r.Error != "" || f.get("FA") != "144075135" {
		t.Fatalf("CW-U: FA %s %s", f.get("FA"), r.Error)
	}
	// Never while transmitting, never outside CW, never with nothing there.
	f.mu.Lock()
	f.v["PO"] = "120"
	f.mu.Unlock()
	fakeTone(e, 650)
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "transmitting") {
		t.Fatalf("keyed: %q", r.Error)
	}
	f.mu.Lock()
	f.v["PO"], f.v["MD0"] = "", "2"
	f.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "isn't in CW") {
		t.Fatalf("USB: %q", r.Error)
	}
	e.mu.Lock()
	e.zHist = nil
	e.mu.Unlock()
	if r := e.doRig(rigAction{"zeroIn", ""}); !strings.Contains(r.Error, "no signal") {
		t.Fatalf("nothing there: %q", r.Error)
	}
}
