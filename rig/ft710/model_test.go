package ft710

import (
	"reflect"
	"testing"
)

func TestModelForID(t *testing.T) {
	for id, want := range map[string]*Model{"ID0800;": FT710, "ID0670;": FT991A, "ID0570;": FT991, "ID0761;": nil, "": nil} {
		if got := ModelForID(id); got != want {
			t.Errorf("%q: %v", id, got)
		}
	}
	if FT991.Name != "FT-991" || FT991A.Name != "FT-991A" || FT991.SetSplit(true) != "FT3;" {
		t.Fatal("the FT-991 is an FT-991A under another name")
	}
}

func TestModelCommands(t *testing.T) {
	ok := func(c string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for got, want := range map[string]string{
		// FT-710: the package's own commands
		ok(FT710.SelectBand("20m")):         "BS05;",
		FT710.SetSplit(true):                "FT1;",
		FT710.SetSplit(false):               "FT0;",
		FT710.SetClar(true, false):          "CF00010000;",
		ok(FT710.SetClarOffset(-120)):       "CF001-0120;",
		FT710.TunerStart():                  "AC003;",
		ok(FT710.SetPower(100, 14_000_000)): "PC100;",
		// FT-991A
		ok(FT991A.SelectBand("2m")):          "BS15;",
		ok(FT991A.SelectBand("70cm")):        "BS16;",
		ok(FT991A.SelectBand("20m")):         "BS05;",
		FT991A.SetSplit(true):                "FT3;",
		FT991A.SetSplit(false):               "FT2;",
		FT991A.SetClar(true, false):          "RT1;XT0;",
		ok(FT991A.SetClarOffset(120)):        "RC;RU0120;",
		ok(FT991A.SetClarOffset(-50)):        "RC;RD0050;",
		ok(FT991A.SetClarOffset(0)):          "RC;",
		FT991A.TunerStart():                  "AC002;",
		ok(FT991A.SetFreqA(144_075_000)):     "FA144075000;",
		ok(FT991A.SetFreqA(432_085_000)):     "FA432085000;",
		ok(FT991A.SetPower(50, 144_075_000)): "PC050;",
		ok(FT991A.SetPower(100, 14_000_000)): "PC100;",
		ok(FT991A.SetPCKeying(PCKeyingDTR)):  "EX0603;",
		FT991A.PCKeyingQuery():               "EX060;",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	for _, bad := range []func() (string, error){
		func() (string, error) { return FT710.SelectBand("2m") },
		func() (string, error) { return FT710.SetFreqA(144_075_000) },
		func() (string, error) { return FT991A.SetFreqA(471_000_000) },
		func() (string, error) { return FT991A.SetPower(51, 146_520_000) }, // 50 W on 2 m
		func() (string, error) { return FT991A.SetPower(60, 432_085_000) },
		func() (string, error) { return FT710.SetPCKeying(3) },
	} {
		if c, err := bad(); err == nil {
			t.Errorf("%q should have been refused", c)
		}
	}
	if FT710.PCKeyingQuery() != "" || FT710.HasVHF() || !FT991A.HasVHF() {
		t.Fatal("PC keying / VHF")
	}
	if !reflect.DeepEqual(FT991A.Bands()[len(FT991A.Bands())-2:], []string{"2m", "70cm"}) || len(FT710.Bands()) != 11 {
		t.Fatalf("bands %v / %v", FT991A.Bands(), FT710.Bands())
	}
	if FT991A.TunerCovers(146_520_000) || !FT991A.TunerCovers(50_100_000) {
		t.Fatal("the tuner is HF and 6 m")
	}
}

func TestFT991AState(t *testing.T) {
	q := FT991A.StateQueries()
	if q[4] != "IF;" || len(q) != 8 {
		t.Fatalf("queries %v", q)
	}
	for _, c := range q {
		if !IsQuery(c) {
			t.Errorf("%s isn't treated as a query", c)
		}
	}
	// IF: channel 001, VFO-A 144.075.000, clarifier +0120 RX on TX off, mode CW-U...
	s, err := FT991A.ParseState([]string{"FA144075000;", "FB144100000;", "MD03;", "FT0;",
		"IF001144075000+012010300000;", "PC050;", "RG0255;", "AG0040;"})
	if err != nil {
		t.Fatal(err)
	}
	want := State{FreqA: 144_075_000, FreqB: 144_100_000, Mode: "3", ModeName: "CW-U", ClarRX: true, ClarHz: 120,
		PowerW: 50, RXFreq: 144_075_120, TXFreq: 144_075_000, RFGain: 255, AFGain: 40}
	if s != want {
		t.Fatalf("got %+v\nwant %+v", s, want)
	}
	if _, err := FT991A.ParseState([]string{"FA144075000;", "FB144100000;", "MD03;", "FT0;", "IF00;", "PC050;", "RG0255;", "AG0040;"}); err == nil {
		t.Fatal("a short IF answer")
	}
	if v, err := FT991A.ParsePCKeying("EX0600;"); err != nil || v != PCKeyingOff {
		t.Fatalf("PC keying: %v %v", v, err)
	}
}

func TestFT991AMeters(t *testing.T) {
	if w := FT991A.Watts(100); w != 26 {
		t.Fatalf("FT-991A power scale: %v", w)
	}
	if s := FT991A.SWR(89); s != 2.0 {
		t.Fatalf("FT-991A SWR scale: %v", s)
	}
	if FT710.Watts(205) != 100 {
		t.Fatal("FT-710 scale unchanged")
	}
}
