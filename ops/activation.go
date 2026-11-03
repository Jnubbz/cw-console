package ops

// An activation as POTA counts it: the contacts from one park on one UTC day, with
// duplicates (same call, band and mode that day) counted once. Ten make it valid.

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ActivationNeeded: contacts for a valid activation (POTA rules: 10 in one UTC day).
const ActivationNeeded = 10

// LogRow is one contact as the page lists it.
type LogRow struct {
	QSO
	Dupe bool // an earlier contact the same UTC day has the same call, band and mode
}

// Activation is the UTC day's contacts for a park ("" = contacts logged with no park).
type Activation struct {
	Day   string   // YYYYMMDD, UTC
	Park  string   //
	Rows  []LogRow // oldest first
	Valid int      // what POTA counts: unique call + band + mode
	Dupes int
	Modes map[string]int // valid contacts by mode
}

func dupeKey(q QSO) string {
	return strings.ToUpper(q.Call) + "|" + strings.ToLower(q.Band) + "|" + modeOf(q)
}

// ActivationOn gathers park's contacts on the UTC day of now.
func (l *Logbook) ActivationOn(park string, now time.Time) Activation {
	a := Activation{Day: now.UTC().Format("20060102"), Park: park, Modes: map[string]int{}}
	seen := map[string]bool{}
	for _, e := range l.entries {
		if e.Start.UTC().Format("20060102") != a.Day || !strings.EqualFold(e.Park, park) {
			continue
		}
		k := dupeKey(e)
		r := LogRow{QSO: e, Dupe: seen[k]}
		seen[k] = true
		if r.Dupe {
			a.Dupes++
		} else {
			a.Valid++
			a.Modes[modeOf(e)]++
		}
		a.Rows = append(a.Rows, r)
	}
	return a
}

// DayCount: contacts on a UTC day.
type DayCount struct {
	Day string `json:"day"` // YYYYMMDD
	N   int    `json:"n"`
}

// Unparked lists the UTC days that have contacts logged without a park, newest first.
func (l *Logbook) Unparked() []DayCount {
	m := map[string]int{}
	for _, e := range l.entries {
		if e.Park == "" {
			m[e.Start.UTC().Format("20060102")]++
		}
	}
	out := []DayCount{}
	for d, n := range m {
		out = append(out, DayCount{d, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	return out
}

var parkRef = regexp.MustCompile(`^[A-Z0-9]{1,4}-[0-9]{4,6}$`)

// NormalizePark: "us-1234 " -> "US-1234"; "" stays ""; anything else is an error.
func NormalizePark(s string) (string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || parkRef.MatchString(s) {
		return s, nil
	}
	return "", errors.New("a park reference looks like US-1234")
}

// AssignPark gives every contact on UTC day (YYYYMMDD) that has no park the park, and
// writes the POTA file for it. Returns how many changed.
func (l *Logbook) AssignPark(day, park string) (int, error) {
	park, err := NormalizePark(park)
	if err != nil || park == "" {
		return 0, errors.New("a park reference looks like US-1234")
	}
	old := append([]QSO(nil), l.entries...)
	n := 0
	var changed QSO
	for i, e := range l.entries {
		if e.Park == "" && e.Start.UTC().Format("20060102") == day {
			l.entries[i].Park = park
			changed = l.entries[i]
			n++
		}
	}
	if n == 0 {
		return 0, errors.New("no contacts without a park that day")
	}
	if err := l.rewrite(changed); err != nil {
		l.entries = old
		return 0, err
	}
	l.Seq++
	return n, nil
}
