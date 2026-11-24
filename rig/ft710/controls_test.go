package ft710

import (
	"reflect"
	"testing"
)

func TestControlCommands(t *testing.T) {
	ok := func(c string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for got, want := range map[string]string{
		// FT-710
		SetBreakInType(true):      "EX0201161;",
		SetBreakInType(false):     "EX0201160;",
		ok(SetBreakInDelay(30)):   "SD00;",
		ok(SetBreakInDelay(200)):  "SD04;",
		ok(SetBreakInDelay(320)):  "SD07;", // up to the next step
		ok(SetBreakInDelay(3000)): "SD33;",
		ok(SetCWWidth(250)):       "SH0005;",
		ok(SetCWWidth(500)):       "SH0010;",
		ok(SetCWWidth(600)):       "SH0011;",
		ok(SetIFShift(100)):       "IS00+0100;",
		ok(SetIFShift(-250)):      "IS00-0260;", // 20 Hz steps
		// FT-991A (menu 056, SD in ms, SH0nn, IS0)
		FT991A.SetBreakInType(true):      "EX0561;",
		ok(FT991A.SetBreakInDelay(200)):  "SD0200;",
		ok(FT991A.SetBreakInDelay(25)):   "SD0030;",
		ok(FT991A.SetBreakInDelay(1234)): "SD1230;",
		ok(FT991A.SetCWWidth(250)):       "SH005;",
		ok(FT991A.SetCWWidth(500)):       "SH010;",
		ok(FT991A.SetCWWidth(800)):       "SH011;", // its table goes 500, 800
		ok(FT991A.SetIFShift(-100)):      "IS0-0100;",
		// both
		SetAPF(true):           "CO020001;",
		ok(SetPitch(600)):      "KP30;",
		SetAutoNotch(true):     "BC01;",
		SetManualNotch(true):   "BP00001;",
		ok(SetNotchFreq(1000)): "BP01100;",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	for _, bad := range []func() (string, error){
		func() (string, error) { return SetBreakInDelay(4000) },
		func() (string, error) { return SetCWWidth(275) },
		func() (string, error) { return FT991A.SetCWWidth(600) }, // not on the 991
		func() (string, error) { return SetIFShift(1300) },
		func() (string, error) { return SetPitch(290) },
		func() (string, error) { return SetNotchFreq(3300) },
	} {
		if c, err := bad(); err == nil {
			t.Errorf("%q should have been refused", c)
		}
	}
	if w := FT991A.Widths(); w[10] != 800 || len(w) != 17 {
		t.Fatalf("991 widths %v", w)
	}
}

func TestParseControls(t *testing.T) {
	c, err := ParseControls([]string{"BI1;", "EX0201160;", "SD04;", "SH0010;", "CO020001;", "IS00-0100;", "KP30;", "KS022;", "BC01;", "BP00000;", "BP01150;"})
	if err != nil {
		t.Fatal(err)
	}
	want := Controls{BreakIn: true, DelayMs: 200, WidthHz: 500, Widths: FT710.Widths(), APF: true, IFShiftHz: -100,
		PitchHz: 600, KeyWPM: 22, AutoNotch: true, NotchHz: 1500}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v\nwant %+v", c, want)
	}
	// One answer the radio didn't like: that control is unknown, the rest still read.
	c, err = ParseControls([]string{"BI0;", "?;", "SD04;", "?;", "CO020000;", "IS00+0000;", "KP30;", "KS022;", "BC00;", "BP00001;", "BP01100;"})
	if err != nil || c.WidthHz != -1 || c.DelayMs != 200 || !c.Notch || c.NotchHz != 1000 {
		t.Fatalf("forgiving parse: %+v %v", c, err)
	}
	// FT-991A
	q := FT991A.ControlQueries()
	if q[1] != "EX056;" {
		t.Fatalf("991 queries %v", q)
	}
	for _, c := range q {
		if !IsQuery(c) {
			t.Errorf("%s isn't treated as a query", c)
		}
	}
	c, err = FT991A.ParseControls([]string{"BI1;", "EX0561;", "SD0250;", "SH011;", "CO020000;", "IS0+0060;", "KP40;", "KS018;", "BC00;", "BP00001;", "BP01080;"})
	if err != nil || !c.FullBreakIn || c.DelayMs != 250 || c.WidthHz != 800 || c.IFShiftHz != 60 || c.PitchHz != 700 || c.KeyWPM != 18 || c.NotchHz != 800 {
		t.Fatalf("991: %+v %v", c, err)
	}
	if IsQuery(SetAPF(true)) || IsQuery("SD0200;") {
		t.Fatal("sets aren't queries")
	}
}
