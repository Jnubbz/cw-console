package logbook

import (
	"strings"
	"testing"
	"time"
)

func TestRejectsACallThatWouldBreakADIF(t *testing.T) {
	_, err := Format(QSO{Call: "W1AW<EOR>", Start: time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)})
	if err == nil {
		t.Fatal("call containing <EOR> was written")
	}
	for _, bad := range []string{"CQ", "5NN", "TU5NN", "", "W1AW\nK"} {
		if ValidCall(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, good := range []string{"w1aw", "KN0BLE", "VE3/W1AW/P"} {
		if !ValidCall(good) {
			t.Fatalf("rejected %q", good)
		}
	}
}

func TestNameWithABracketRoundTrips(t *testing.T) {
	q := QSO{
		Call: "W1AW", Start: time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC),
		Band: "20m", Mode: "CW", Freq: "14.058", Name: "A<B", State: "CT",
		DXCC: 291, CTYPrefix: "K", MyCall: "KN0BLE", RSTSent: "599", RSTRcvd: "579",
	}
	s, err := Format(q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "<NAME:3>A<B ") == false {
		t.Fatalf("name field not length-prefixed: %s", s)
	}
	got, err := Parse(Header + s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "A<B" || got[0].Call != "W1AW" || got[0].DXCC != 291 || got[0].State != "CT" {
		t.Fatalf("parsed %+v", got)
	}
	if !got[0].Start.Equal(q.Start) {
		t.Fatalf("time %s", got[0].Start)
	}
}

func TestAssessWorkedNeeds(t *testing.T) {
	b := &Book{}
	if err := b.Add(QSO{
		Call: "W1AW", Start: time.Date(2024, 3, 2, 12, 0, 0, 0, time.UTC),
		Band: "20m", Mode: "CW", State: "CT", DXCC: 291, CTYPrefix: "K",
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(QSO{
		Call: "JA1ABC", Start: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		Band: "40m", Mode: "FT8", DXCC: 339, CTYPrefix: "JA",
	}); err != nil {
		t.Fatal(err)
	}

	worked := b.Assess(Query{Call: "W1AW/7", DXCC: 291, CTYPrefix: "K", Band: "20m", Mode: "CW", State: "CT"})
	if !worked.Worked || worked.Last.Band != "20m" || worked.Last.Mode != "CW" {
		t.Fatalf("worked: %+v", worked)
	}
	if worked.NewDXCC || worked.NewBand || worked.NewMode || worked.NewState {
		t.Fatalf("old contact looked like a need: %+v", worked)
	}
	if !worked.Last.Start.Equal(time.Date(2024, 3, 2, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("last %s", worked.Last.Start)
	}

	band := b.Assess(Query{Call: "W1AW", DXCC: 291, CTYPrefix: "K", Band: "15m", Mode: "CW", State: "CT"})
	if !band.Worked || !band.NewBand || band.NewDXCC || band.NewMode {
		t.Fatalf("new band: %+v", band)
	}

	mode := b.Assess(Query{Call: "W1AW", DXCC: 291, CTYPrefix: "K", Band: "20m", Mode: "FT8", State: "CT"})
	if !mode.NewMode || mode.NewBand || mode.NewDXCC {
		t.Fatalf("new mode: %+v", mode)
	}

	dxcc := b.Assess(Query{Call: "VK1ABC", DXCC: 150, CTYPrefix: "VK", Band: "20m", Mode: "CW"})
	if !dxcc.NewDXCC || dxcc.Worked || dxcc.NewBand || dxcc.NewMode {
		t.Fatalf("new dxcc: %+v labels %v", dxcc, dxcc.Labels())
	}

	// Offline: the log knows the entity by cty prefix, the decode only has the prefix.
	offline := b.Assess(Query{Call: "K0ABC", CTYPrefix: "K", Band: "20m", Mode: "CW", State: "ND"})
	if offline.NewDXCC || !offline.NewState {
		t.Fatalf("prefix match / new state: %+v", offline)
	}

	unknown := b.Assess(Query{Call: "ZZ1ZZZ", Band: "20m", Mode: "CW"})
	if unknown.NewDXCC {
		t.Fatal("unknown entity was called a new DXCC")
	}
}
