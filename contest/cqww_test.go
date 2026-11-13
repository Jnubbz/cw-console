package contest

import (
	"strings"
	"testing"
	"time"
)

func TestCQWWScoring(t *testing.T) {
	s := NewCQWW(Builtin(), "KN0BLE", 5)
	t0 := time.Date(2026, 11, 28, 0, 1, 0, 0, time.UTC)
	add := func(min int, band, call string, zone int) Scored {
		return s.Add(QSO{Time: t0.Add(time.Duration(min) * time.Minute), Band: band, Call: call, ZoneSent: 5, ZoneRcvd: zone})
	}
	// Check before logging: a new country and zone, 3 points.
	if c := s.Check("DL1ABC", "20m", 0); c.Dupe || c.Points != 3 || !c.NewZone || !c.NewCountry || c.Prefix != "DL" || c.ZoneGuess != 14 {
		t.Fatalf("check: %+v", c)
	}
	if q := add(0, "20m", "DL1ABC", 14); q.Points != 3 || !q.NewZone || !q.NewCountry {
		t.Fatalf("DL: %+v", q)
	}
	if q := add(1, "20m", "DL2XYZ", 14); q.Points != 3 || q.NewZone || q.NewCountry {
		t.Fatalf("second DL: %+v", q) // same zone and country: no new mults
	}
	if q := add(2, "20m", "DL1ABC", 14); !q.Dupe || q.Points != 0 {
		t.Fatalf("dupe: %+v", q)
	}
	if c := s.Check("dl1abc", "20m", 0); !c.Dupe || len(c.WorkedOn) != 1 {
		t.Fatalf("dupe check: %+v", c)
	}
	if q := add(3, "40m", "DL1ABC", 14); q.Dupe || !q.NewZone || !q.NewCountry {
		t.Fatalf("same station, another band: %+v", q) // mults are per band
	}
	if q := add(4, "20m", "VE3ABC", 4); q.Points != 2 || !q.NewZone || !q.NewCountry {
		t.Fatalf("NA, other country: 2 points: %+v", q)
	}
	if q := add(5, "20m", "W1AW", 5); q.Points != 0 || !q.NewZone || !q.NewCountry {
		t.Fatalf("own country: 0 points, still mults: %+v", q)
	}
	if q := add(6, "20m", "KH6ABC", 31); q.Points != 3 {
		t.Fatalf("Hawaii is Oceania: %+v", q)
	}
	if q := add(7, "20m", "PY2ABC", 11); q.Points != 3 {
		t.Fatalf("South America: %+v", q)
	}
	if q := add(8, "17m", "JA1ZZZ", 25); q.Points != 0 || q.NewZone {
		t.Fatalf("17 m isn't a contest band: %+v", q)
	}
	sum := s.Summary(t0.Add(10 * time.Minute))
	// 20 m: DL x2 (3+3), VE 2, W 0, KH6 3, PY 3 = 14; 40 m: DL 3. 17.
	// Zones 20m: 14, 4, 5, 31, 11 = 5; 40m: 14 = 1. Countries 20m: DL VE K KH6 PY = 5; 40m: 1.
	if sum.Total.QSOs != 7 || sum.Total.Dupes != 1 || sum.Total.Points != 17 || sum.Total.Zones != 6 || sum.Total.Countries != 6 || sum.Score != 17*12 || sum.OffBandQS != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if sum.Last60 != 7 || sum.Rate10 < 40 {
		t.Fatalf("rate: %+v", sum)
	}
}

func TestCQWWEuropeanStation(t *testing.T) {
	s := NewCQWW(Builtin(), "DL1ABC", 14)
	if q := s.Add(QSO{Band: "20m", Call: "F5ABC", ZoneRcvd: 14}); q.Points != 1 {
		t.Fatalf("same continent, another country, outside NA: 1 point: %+v", q)
	}
}

func TestWeekend(t *testing.T) {
	for y, want := range map[int]string{2026: "2026-11-28", 2025: "2025-11-29", 2027: "2027-11-27", 2020: "2020-11-28"} {
		s, e := Weekend(y)
		if s.Format("2006-01-02") != want || s.Weekday() != time.Saturday || e.Sub(s) != 48*time.Hour-time.Second {
			t.Errorf("%d: %v - %v, want %s", y, s, e, want)
		}
	}
}

func TestCabrillo(t *testing.T) {
	s := NewCQWW(Builtin(), "KN0BLE", 5)
	s.Add(QSO{Time: time.Date(2026, 11, 28, 0, 1, 0, 0, time.UTC), FreqKHz: 14025, Band: "20m", Call: "DL1ABC", RSTSent: "599", ZoneSent: 5, RSTRcvd: "599", ZoneRcvd: 14})
	s.Add(QSO{Time: time.Date(2026, 11, 28, 0, 3, 0, 0, time.UTC), Band: "40m", Call: "JA1ZZZ", ZoneSent: 5, ZoneRcvd: 25})
	var b strings.Builder
	if err := WriteCabrillo(&b, Header{Call: "kn0ble", Location: "fl", Assisted: true, Power: "low", ClaimedScore: 24, CreatedBy: "CW Console 0.2.9"}, s.Log()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"START-OF-LOG: 3.0\r\n", "CONTEST: CQ-WW-CW\r\n", "CALLSIGN: KN0BLE\r\n", "LOCATION: FL\r\n",
		"CATEGORY-ASSISTED: ASSISTED\r\n", "CATEGORY-POWER: LOW\r\n", "CATEGORY-BAND: ALL\r\n", "CLAIMED-SCORE: 24\r\n",
		"OPERATORS: KN0BLE\r\n",
		"QSO: 14025 CW 2026-11-28 0001 KN0BLE        599 05     DL1ABC        599 14\r\n",
		"QSO:  7000 CW 2026-11-28 0003 KN0BLE        599 05     JA1ZZZ        599 25\r\n",
		"END-OF-LOG:\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "NAME:") {
		t.Error("empty fields should be left out")
	}
}
