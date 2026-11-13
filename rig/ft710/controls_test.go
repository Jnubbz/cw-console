package ft710

import "testing"

func TestControlCommands(t *testing.T) {
	ok := func(c string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for got, want := range map[string]string{
		SetBreakInType(true):      "EX0201161;",
		SetBreakInType(false):     "EX0201160;",
		ok(SetBreakInDelay(30)):   "SD00;",
		ok(SetBreakInDelay(200)):  "SD04;",
		ok(SetBreakInDelay(300)):  "SD06;",
		ok(SetBreakInDelay(320)):  "SD07;", // up to the next step
		ok(SetBreakInDelay(3000)): "SD33;",
		ok(SetCWWidth(250)):       "SH0005;",
		ok(SetCWWidth(500)):       "SH0010;",
		ok(SetCWWidth(2400)):      "SH0017;",
		SetAPF(true):              "CO020001;",
		SetAPF(false):             "CO020000;",
		ok(SetIFShift(100)):       "IS00+0100;",
		ok(SetIFShift(-250)):      "IS00-0260;", // 20 Hz steps
		ok(SetIFShift(0)):         "IS00+0000;",
		ok(SetPitch(300)):         "KP00;",
		ok(SetPitch(600)):         "KP30;",
		ok(SetPitch(704)):         "KP40;",
		ok(SetPitch(1050)):        "KP75;",
		SetAutoNotch(true):        "BC01;",
		SetManualNotch(true):      "BP00001;",
		SetManualNotch(false):     "BP00000;",
		ok(SetNotchFreq(1000)):    "BP01100;",
		ok(SetNotchFreq(10)):      "BP01001;",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	for _, bad := range []func() (string, error){
		func() (string, error) { return SetBreakInDelay(4000) },
		func() (string, error) { return SetCWWidth(275) },
		func() (string, error) { return SetIFShift(1300) },
		func() (string, error) { return SetPitch(290) },
		func() (string, error) { return SetPitch(1060) },
		func() (string, error) { return SetNotchFreq(3300) },
		func() (string, error) { return SetNotchFreq(0) },
	} {
		if c, err := bad(); err == nil {
			t.Errorf("%q should have been refused", c)
		}
	}
}

func TestParseControls(t *testing.T) {
	c, err := ParseControls([]string{"BI1;", "EX0201160;", "SD04;", "SH0010;", "CO020001;", "IS00-0100;", "KP30;", "KS022;", "BC01;", "BP00000;", "BP01150;"})
	if err != nil {
		t.Fatal(err)
	}
	want := Controls{BreakIn: true, FullBreakIn: false, DelayMs: 200, WidthHz: 500, APF: true, IFShiftHz: -100,
		PitchHz: 600, KeyWPM: 22, AutoNotch: true, Notch: false, NotchHz: 1500}
	if c != want {
		t.Fatalf("got %+v\nwant %+v", c, want)
	}
	// One answer the radio didn't like: that control is unknown, the rest still read.
	c, err = ParseControls([]string{"BI0;", "?;", "SD04;", "?;", "CO020000;", "IS00+0000;", "KP30;", "KS022;", "BC00;", "BP00001;", "BP01100;"})
	if err != nil || c.WidthHz != -1 || c.DelayMs != 200 || !c.Notch || c.NotchHz != 1000 {
		t.Fatalf("forgiving parse: %+v %v", c, err)
	}
	if _, err := ParseControls([]string{"BI1;"}); err == nil {
		t.Fatal("too few answers")
	}
}

func TestSMeter(t *testing.T) {
	for raw, want := range map[int]string{0: "S0", 12: "S1", 65: "S5", 95: "S7", 112: "S8", 130: "S9", 125: "S9", 118: "S8", 172: "S9+20", 255: "S9+60"} {
		if got := SLabel(raw); got != want {
			t.Errorf("raw %d: %s, want %s", raw, got, want)
		}
	}
	if !IsQuery(SMeterQuery) || !IsQuery(BreakInTypeQuery) || !IsQuery("BP01;") || IsQuery(SetAPF(true)) {
		t.Fatal("IsQuery")
	}
}
