package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActivationCount(t *testing.T) {
	dir := t.TempDir()
	l := OpenLog(dir)
	day := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	add := func(call, band, mode, park string, min int) {
		if _, err := l.Add(QSO{Call: call, Band: band, Mode: mode, Park: park, MyCall: "KN0BLE", Start: day.Add(time.Duration(min) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 9; i++ {
		add("K"+string(rune('A'+i))+"1AA", "20m", "", "US-1234", i)
	}
	add("KA1AA", "20m", "", "US-1234", 20)    // dupe
	add("KA1AA", "40m", "", "US-1234", 21)    // other band: counts
	add("KA1AA", "20m", "FT8", "US-1234", 22) // other mode: counts
	add("W1AW", "20m", "", "US-5678", 23)     // other park
	add("W1AW", "20m", "", "US-1234", -19*60) // the day before (UTC)
	a := l.ActivationOn("US-1234", day.Add(5*time.Hour))
	if a.Valid != 11 || a.Dupes != 1 || len(a.Rows) != 12 || a.Modes["CW"] != 10 || a.Modes["FT8"] != 1 {
		t.Fatalf("%+v", a)
	}
	if !a.Rows[9].Dupe || a.Rows[10].Dupe {
		t.Fatal("dupe marks")
	}
	if l.ActivationOn("US-1234", day.Add(7*time.Hour)).Valid != 0 { // past 00z: a new day
		t.Fatal("next UTC day")
	}
}

func TestAssignPark(t *testing.T) {
	dir := t.TempDir()
	l := OpenLog(dir)
	day := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	for i, c := range []string{"K1ABC", "W1AW", "N0CALL"} {
		l.Add(QSO{Call: c, Band: "20m", MyCall: "KN0BLE", Start: day.Add(time.Duration(i) * time.Minute)})
	}
	l.Add(QSO{Call: "K2XYZ", Band: "20m", MyCall: "KN0BLE", Start: day.Add(3 * time.Hour)}) // Nov 2 UTC
	if u := l.Unparked(); len(u) != 2 || u[0] != (DayCount{"20261102", 1}) || u[1] != (DayCount{"20261101", 3}) {
		t.Fatalf("%v", u)
	}
	seq := l.Seq
	if _, err := l.AssignPark("20261101", "pota"); err == nil {
		t.Fatal("bad park accepted")
	}
	n, err := l.AssignPark("20261101", " us-1234")
	if err != nil || n != 3 || l.Seq == seq {
		t.Fatal(n, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "KN0BLE@US-1234-20261101.adi"))
	if err != nil || strings.Count(string(b), "<EOR>") != 3 || !strings.Contains(string(b), "<MY_SIG_INFO:7>US-1234") {
		t.Fatalf("POTA file: %v %s", err, b)
	}
	if a := OpenLog(dir).ActivationOn("US-1234", day); a.Valid != 3 { // and in the master log
		t.Fatalf("reloaded: %+v", a)
	}
	if _, err := l.AssignPark("20261101", "US-1234"); err == nil {
		t.Fatal("nothing left to assign")
	}
	for in, want := range map[string]string{"k-0001": "K-0001", "VE-12345": "VE-12345", "": ""} {
		if got, err := NormalizePark(in); err != nil || got != want {
			t.Fatal(in, got, err)
		}
	}
}
