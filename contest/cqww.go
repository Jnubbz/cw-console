package contest

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// CQ WW DX (https://www.cqww.com/rules/):
//   - Exchange: RST + the CQ zone the station is in (599 05).
//   - 160, 80, 40, 20, 15, 10 m. A station counts once per band.
//   - Points: other continent 3; same continent, other country 1 (both in North America:
//     2); own country 0 (still a multiplier).
//   - Multipliers, per band: CQ zones, and countries (DXCC + WAE). Maritime mobile: zone only.
//   - Score: points x (zones + countries).

// CQWWBands, in the order the score table shows them.
var CQWWBands = []string{"160m", "80m", "40m", "20m", "15m", "10m"}

// bandEdgeKHz: what Cabrillo's frequency field gets when the dial wasn't known.
var bandEdgeKHz = map[string]int{"160m": 1800, "80m": 3500, "40m": 7000, "20m": 14000, "15m": 21000, "10m": 28000}

// QSO: one contest contact.
type QSO struct {
	Time     time.Time `json:"time"` // UTC
	FreqKHz  int       `json:"freqKHz"`
	Band     string    `json:"band"`
	Call     string    `json:"call"`
	RSTSent  string    `json:"rstSent"`
	ZoneSent int       `json:"zoneSent"`
	RSTRcvd  string    `json:"rstRcvd"`
	ZoneRcvd int       `json:"zoneRcvd"`
}

// Check: what working call on band would be worth, before logging it.
type Check struct {
	Call       string   `json:"call"`
	Country    string   `json:"country"` // cty.dat name ("Fed. Rep. of Germany"), "" unknown
	Prefix     string   `json:"prefix"`  // the country's prefix ("DL"), the multiplier
	Cont       string   `json:"cont"`
	ZoneGuess  int      `json:"zoneGuess"` // cty.dat's zone for the call; the exchange decides
	Dupe       bool     `json:"dupe"`
	Points     int      `json:"points"`
	NewZone    bool     `json:"newZone"`    // the zone isn't worked on this band yet (needs a zone)
	NewCountry bool     `json:"newCountry"` // nor is the country
	WorkedOn   []string `json:"workedOn"`   // bands this call is already in the log on
	OffBand    bool     `json:"offBand"`    // not a CQ WW band (60, 30, 17, 12, 6 m)
	MM         bool     `json:"mm"`
}

// Scored: a logged contact, as the score counts it.
type Scored struct {
	QSO
	Prefix     string `json:"prefix"`
	Points     int    `json:"points"`
	Dupe       bool   `json:"dupe"`
	NewZone    bool   `json:"newZone"`
	NewCountry bool   `json:"newCountry"`
}

// CQWW keeps score as contacts are logged.
type CQWW struct {
	cty    *CTY
	me     Match
	MyCall string
	MyZone int
	log    []Scored
	worked map[string]map[string]bool // band -> call
	zones  map[string]map[int]bool
	ctrys  map[string]map[string]bool
}

// NewCQWW: myZone is the zone you send (Florida: 5).
func NewCQWW(cty *CTY, myCall string, myZone int) *CQWW {
	s := &CQWW{cty: cty, MyCall: strings.ToUpper(myCall), MyZone: myZone,
		worked: map[string]map[string]bool{}, zones: map[string]map[int]bool{}, ctrys: map[string]map[string]bool{}}
	s.me, _ = cty.Lookup(myCall)
	return s
}

func isCQWWBand(b string) bool { _, ok := bandEdgeKHz[b]; return ok }

// normCall: the call the dupe check compares (K1ABC/P is K1ABC; DL/K1ABC isn't).
func normCall(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	for _, s := range []string{"/P", "/M", "/QRP", "/QRPP"} {
		c = strings.TrimSuffix(c, s)
	}
	return c
}

// points for a contact with m (CQ WW).
func (s *CQWW) points(m Match) int {
	if m.MM || m.Entity == nil || s.me.Entity == nil {
		return 0 // a maritime mobile station: zone multiplier only (the log checker scores it)
	}
	switch {
	case m.Entity == s.me.Entity:
		return 0
	case m.Cont != s.me.Cont:
		return 3
	case s.me.Cont == "NA":
		return 2
	}
	return 1
}

// Check: dupe / points / new multipliers for call on band; zone 0 = not known yet (the
// zone from cty.dat is used to say whether it's likely a new zone).
func (s *CQWW) Check(call, band string, zone int) Check {
	c := Check{Call: strings.ToUpper(strings.TrimSpace(call)), OffBand: !isCQWWBand(band)}
	if c.Call == "" {
		return c
	}
	m, ok := s.cty.Lookup(c.Call)
	if ok {
		c.Cont, c.ZoneGuess, c.MM = m.Cont, m.CQ, m.MM
		if m.Entity != nil {
			c.Country, c.Prefix = m.Entity.Name, strings.TrimPrefix(m.Entity.Prefix, "*")
		}
	}
	n := normCall(c.Call)
	for _, b := range CQWWBands {
		if s.worked[b][n] {
			c.WorkedOn = append(c.WorkedOn, b)
		}
	}
	if c.OffBand {
		return c
	}
	c.Dupe = s.worked[band][n]
	if c.Dupe {
		return c
	}
	c.Points = s.points(m)
	if zone == 0 {
		zone = c.ZoneGuess
	}
	c.NewZone = zone > 0 && !s.zones[band][zone]
	c.NewCountry = ok && m.Entity != nil && !m.MM && !s.ctrys[band][m.Entity.Prefix]
	return c
}

// Add logs a contact and returns how it scored.
func (s *CQWW) Add(q QSO) Scored {
	q.Call = strings.ToUpper(strings.TrimSpace(q.Call))
	sc := Scored{QSO: q}
	if !isCQWWBand(q.Band) {
		sc.Dupe = false
		s.log = append(s.log, sc)
		return sc
	}
	n := normCall(q.Call)
	m, ok := s.cty.Lookup(q.Call)
	if ok && m.Entity != nil {
		sc.Prefix = m.Entity.Prefix
	}
	if s.worked[q.Band] == nil {
		s.worked[q.Band], s.zones[q.Band], s.ctrys[q.Band] = map[string]bool{}, map[int]bool{}, map[string]bool{}
	}
	if s.worked[q.Band][n] {
		sc.Dupe = true
		s.log = append(s.log, sc)
		return sc
	}
	s.worked[q.Band][n] = true
	sc.Points = s.points(m)
	if q.ZoneRcvd >= 1 && q.ZoneRcvd <= 40 && !s.zones[q.Band][q.ZoneRcvd] {
		s.zones[q.Band][q.ZoneRcvd], sc.NewZone = true, true
	}
	if ok && m.Entity != nil && !m.MM && !s.ctrys[q.Band][m.Entity.Prefix] {
		s.ctrys[q.Band][m.Entity.Prefix], sc.NewCountry = true, true
	}
	s.log = append(s.log, sc)
	return sc
}

// Log: everything logged, in order.
func (s *CQWW) Log() []Scored { return append([]Scored(nil), s.log...) }

// BandRow: one line of the score table.
type BandRow struct {
	Band      string `json:"band"`
	QSOs      int    `json:"qsos"`
	Dupes     int    `json:"dupes"`
	Points    int    `json:"points"`
	Zones     int    `json:"zones"`
	Countries int    `json:"countries"`
}

// Summary: the score so far.
type Summary struct {
	Bands     []BandRow `json:"bands"`
	Total     BandRow   `json:"total"`
	Score     int       `json:"score"`
	Rate10    int       `json:"rate10"` // QSOs/hour over the last 10 contacts
	Last60    int       `json:"last60"` // contacts in the last 60 minutes
	OffBandQS int       `json:"offBand"`
}

// Summary as of now.
func (s *CQWW) Summary(now time.Time) Summary {
	var out Summary
	rows := map[string]*BandRow{}
	for _, b := range CQWWBands {
		rows[b] = &BandRow{Band: b, Zones: len(s.zones[b]), Countries: len(s.ctrys[b])}
	}
	var times []time.Time
	for _, q := range s.log {
		r := rows[q.Band]
		if r == nil {
			out.OffBandQS++
			continue
		}
		if q.Dupe {
			r.Dupes++
			continue
		}
		r.QSOs++
		r.Points += q.Points
		times = append(times, q.Time)
	}
	out.Total.Band = "Total"
	for _, b := range CQWWBands {
		r := *rows[b]
		out.Bands = append(out.Bands, r)
		out.Total.QSOs += r.QSOs
		out.Total.Dupes += r.Dupes
		out.Total.Points += r.Points
		out.Total.Zones += r.Zones
		out.Total.Countries += r.Countries
	}
	out.Score = out.Total.Points * (out.Total.Zones + out.Total.Countries)
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for _, t := range times {
		if now.Sub(t) <= time.Hour && !t.After(now) {
			out.Last60++
		}
	}
	if n := len(times); n >= 2 {
		k := 10
		if n < k {
			k = n
		}
		span := now.Sub(times[n-k])
		if span < time.Minute {
			span = time.Minute
		}
		if span < 2*time.Hour { // a stale rate isn't a rate
			out.Rate10 = int(float64(k)/span.Hours() + 0.5)
		}
	}
	return out
}

// Weekend: the CQ WW CW period of a year, the last full weekend of November
// (Saturday 0000 UTC to Sunday 2359:59 UTC).
func Weekend(year int) (start, end time.Time) {
	d := time.Date(year, time.November, 30, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	start = d.AddDate(0, 0, -1)
	return start, start.Add(48*time.Hour - time.Second)
}

// Header: the Cabrillo header. Empty fields are left out.
type Header struct {
	Call         string
	Location     string // US: the state (FL); everyone else DX
	Operators    string
	Assisted     bool   // decoder, RBN or cluster spots used: ASSISTED (the rules' QSO finding assistance)
	Power        string // HIGH LOW QRP
	Band         string // ALL, or 20M etc.
	Name         string
	Email        string
	Grid         string
	Address      string
	CreatedBy    string
	Soapbox      string
	ClaimedScore int
}

// WriteCabrillo writes a Cabrillo 3.0 log for CQ WW CW. Dupes stay in (the log
// checker removes them without penalty).
func WriteCabrillo(w io.Writer, h Header, qsos []Scored) error {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	assisted := "NON-ASSISTED"
	if h.Assisted {
		assisted = "ASSISTED"
	}
	power, band := strings.ToUpper(h.Power), strings.ToUpper(h.Band)
	if power == "" {
		power = "LOW"
	}
	if band == "" {
		band = "ALL"
	}
	loc := strings.ToUpper(h.Location)
	if loc == "" {
		loc = "DX"
	}
	op := h.Operators
	if op == "" {
		op = h.Call
	}
	b.WriteString("START-OF-LOG: 3.0\r\n")
	line("CONTEST", "CQ-WW-CW")
	line("CALLSIGN", strings.ToUpper(h.Call))
	line("LOCATION", loc)
	line("CATEGORY-OPERATOR", "SINGLE-OP")
	line("CATEGORY-ASSISTED", assisted)
	line("CATEGORY-BAND", band)
	line("CATEGORY-MODE", "CW")
	line("CATEGORY-POWER", power)
	line("CATEGORY-STATION", "FIXED")
	line("CATEGORY-TRANSMITTER", "ONE")
	line("CLAIMED-SCORE", fmt.Sprint(h.ClaimedScore))
	line("OPERATORS", strings.ToUpper(op))
	line("NAME", h.Name)
	line("EMAIL", h.Email)
	line("GRID-LOCATOR", strings.ToUpper(h.Grid))
	if h.Address != "" {
		for _, a := range strings.Split(h.Address, "\n") {
			line("ADDRESS", strings.TrimSpace(a))
		}
	}
	line("CREATED-BY", h.CreatedBy)
	line("SOAPBOX", h.Soapbox)
	for _, q := range qsos {
		f := q.FreqKHz
		if f == 0 {
			f = bandEdgeKHz[q.Band]
		}
		rs, rr := q.RSTSent, q.RSTRcvd
		if rs == "" {
			rs = "599"
		}
		if rr == "" {
			rr = "599"
		}
		t := q.Time.UTC()
		fmt.Fprintf(&b, "QSO: %5d CW %s %s %-13s %-3s %02d     %-13s %-3s %02d\r\n",
			f, t.Format("2006-01-02"), t.Format("1504"), strings.ToUpper(h.Call), rs, q.ZoneSent, q.Call, rr, q.ZoneRcvd)
	}
	b.WriteString("END-OF-LOG:\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}
