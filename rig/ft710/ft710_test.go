package ft710

import (
	"math"
	"testing"
)

func TestSetCommands(t *testing.T) {
	must := func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	cases := []struct{ got, want string }{
		{must(SetFreqA(14_058_000)), "FA014058000;"},
		{must(SetFreqB(7_030_500)), "FB007030500;"},
		{must(SetMode(CWU)), "MD03;"},
		{must(SetMode(DATAU)), "MD0C;"},
		{SetSplit(true), "FT1;"},
		{SetSplit(false), "FT0;"},
		{SetClar(true, false), "CF00010000;"},
		{SetClar(false, true), "CF00001000;"},
		{SetClar(false, false), "CF00000000;"},
		{must(SetClarOffset(150)), "CF001+0150;"},
		{must(SetClarOffset(-20)), "CF001-0020;"},
		{must(SetClarOffset(0)), "CF001+0000;"},
		{must(SetPower(5)), "PC005;"},
		{must(SetPower(100)), "PC100;"},
		{must(SetKeySpeed(22)), "KS022;"},
		{SetBreakIn(true), "BI1;"},
		{must(LoadKeyerMemory(1, "cq pota de kn0ble k")), "KM1CQ POTA DE KN0BLE K;"},
		{must(PlayKeyerMemory(1)), "KY6;"},
		{must(PlayKeyerMemory(5)), "KYA;"},
		{must(SelectBand("20m")), "BS05;"},
		{must(SelectBand("160m")), "BS00;"},
		{must(SelectBand("6m")), "BS10;"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

func TestRejectsOutOfRange(t *testing.T) {
	bad := []error{}
	_, e := SetFreqA(29_999)
	bad = append(bad, e)
	_, e = SetFreqB(75_000_001)
	bad = append(bad, e)
	_, e = SetMode("Z")
	bad = append(bad, e)
	_, e = SetClarOffset(10_000)
	bad = append(bad, e)
	_, e = SetPower(4)
	bad = append(bad, e)
	_, e = SetPower(101)
	bad = append(bad, e)
	_, e = SetKeySpeed(61)
	bad = append(bad, e)
	_, e = LoadKeyerMemory(6, "TEST")
	bad = append(bad, e)
	_, e = LoadKeyerMemory(1, "NO SEMICOLONS; HERE")
	bad = append(bad, e)
	_, e = PlayKeyerMemory(0)
	bad = append(bad, e)
	_, e = SelectBand("2m")
	bad = append(bad, e)
	for i, err := range bad {
		if err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
}

func TestParseState(t *testing.T) {
	s, err := ParseState([]string{"FA014058000;", "FB014060000;", "MD03;", "FT1;", "CF00010000;", "CF001-0120;", "PC050;", "RG0040;", "AG0128;"})
	if err != nil {
		t.Fatal(err)
	}
	want := State{FreqA: 14_058_000, FreqB: 14_060_000, Mode: "3", ModeName: "CW-U", Split: true,
		ClarRX: true, ClarHz: -120, PowerW: 50, RXFreq: 14_057_880, TXFreq: 14_060_000, RFGain: 40, AFGain: 128}
	if s != want {
		t.Fatalf("got  %+v\nwant %+v", s, want)
	}
	// Clarifier on TX only, no split: TX moves, RX doesn't.
	s, err = ParseState([]string{"FA007030000;", "FB007030000;", "MD07;", "FT0;", "CF00001000;", "CF001+0200;", "PC100;", "RG0255;", "AG0000;"})
	if err != nil {
		t.Fatal(err)
	}
	if s.RXFreq != 7_030_000 || s.TXFreq != 7_030_200 || s.ModeName != "CW-L" {
		t.Fatalf("got %+v", s)
	}
}

func TestGainCommands(t *testing.T) {
	for _, c := range []struct {
		f    func(int) (string, error)
		n    int
		want string
	}{{SetRFGain, 0, "RG0000;"}, {SetRFGain, 255, "RG0255;"}, {SetAFGain, 77, "AG0077;"}} {
		if got, err := c.f(c.n); err != nil || got != c.want {
			t.Errorf("%d: got %q %v, want %q", c.n, got, err, c.want)
		}
	}
	for _, n := range []int{-1, 256} {
		if _, err := SetRFGain(n); err == nil {
			t.Errorf("RF gain %d accepted", n)
		}
		if _, err := SetAFGain(n); err == nil {
			t.Errorf("AF gain %d accepted", n)
		}
	}
}

func TestParseStateRejectsGarbage(t *testing.T) {
	for _, a := range [][]string{
		{"FA014058000;"},
		{"?;", "FB014060000;", "MD03;", "FT1;", "CF00010000;", "CF001-0120;", "PC050;"},
		{"FA014058000;", "FB014060000;", "MD0;", "FT1;", "CF00010000;", "CF001-0120;", "PC050;"},
		{"FA014058000;", "FB014060000;", "MD03;", "FT1;", "CF0;", "CF001-0120;", "PC050;", "RG0255;", "AG0128;"},
		{"FA014058000;", "FB014060000;", "MD03;", "FT1;", "CF00010000;", "CF001-0120;", "PC050;", "RG0999;", "AG0128;"},
		{"FA014058000;", "FB014060000;", "MD03;", "FT1;", "CF00010000;", "CF001-0120;", "PC050;", "RG0255;", "?;"},
	} {
		if _, err := ParseState(a); err == nil {
			t.Errorf("%q: expected an error", a)
		}
	}
}

func TestMeters(t *testing.T) {
	for _, c := range []struct {
		answer, query string
		raw           int
	}{
		{"RM6045;", MeterSWR, 45},
		{"RM6089000;", MeterSWR, 89}, // six-digit firmware answer
		{"RM5205;", MeterPO, 205},
		{"RM4000;", MeterALC, 0},
	} {
		raw, err := MeterRaw(c.answer, c.query)
		if err != nil || raw != c.raw {
			t.Errorf("%q: got %d, %v", c.answer, raw, err)
		}
	}
	for _, bad := range []string{"RM5205;", "RM6;", "RM6xyz;", "RM6300;"} {
		if _, err := MeterRaw(bad, MeterSWR); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.01 }
	if !near(SWR(0), 1.0) || !near(SWR(89), 2.0) || !near(SWR(255), 25) || !near(SWR(39), 1.35) {
		t.Errorf("SWR calibration: %v %v %v %v", SWR(0), SWR(89), SWR(255), SWR(39))
	}
	if !near(Watts(0), 0) || !near(Watts(27), 5) || !near(Watts(205), 100) || !near(Watts(255), 100) {
		t.Errorf("power calibration: %v %v %v %v", Watts(0), Watts(27), Watts(205), Watts(255))
	}
	if !near(ALC(255), 100) {
		t.Errorf("ALC: %v", ALC(255))
	}
}

func TestQueriesAreQueries(t *testing.T) {
	for _, q := range append(StateQueries, MeterSWR, MeterPO, MeterALC) {
		if !IsQuery(q) {
			t.Errorf("%q should be a query", q)
		}
	}
	for _, c := range []string{"FA014058000;", Swap, SetSplit(true), TunerStart} {
		if IsQuery(c) {
			t.Errorf("%q is not a query", c)
		}
	}
}
