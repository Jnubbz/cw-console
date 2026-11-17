// Package logbook is the unified ADIF log for CW, FT8 and SSTV.
//
// The file the apps write today is ops.Logbook (ops/adif.go, cw-decoder-log.adi).
// ops.Record copies a callsign into the record with a length prefix and no
// check that the call is a call. A value containing "<" round-trips through
// ops.ParseADIF, which honors lengths, and it breaks a reader that scans for
// the next "<". This package refuses a call that is not a callsign, and it
// keeps the length prefix for free text, which is what ADIF 3.1.4 specifies.
//
// Nothing here replaces ops.Logbook. The apps keep writing the old records
// until phase 1 of the plan switches them over.
package logbook

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Same shape ops.IsCall uses. Kept here so this package does not import ops;
// phase 1 makes ops call this, and an import the other way would cycle.
var callRe = regexp.MustCompile(`^(?:[A-Z0-9]{1,4}/)?(?:[A-Z]{1,2}|[A-Z][0-9]|[0-9][A-Z])[0-9][A-Z]{1,4}(?:/[A-Z0-9]{1,4})?$`)

// QSO is one contact. DXCC is the ARRL entity number when a lookup has one.
// CTYPrefix is the cty.dat entity prefix ("K", "DL", "*IG9"), which is what
// the log can know offline. State is the contacted station's state.
type QSO struct {
	Call      string
	Start     time.Time
	Freq      string
	Band      string
	Mode      string
	RSTSent   string
	RSTRcvd   string
	MyCall    string
	Grid      string
	State     string
	MyState   string
	MyGrid    string
	DXCC      int
	CTYPrefix string
	Name      string
	Park      string
	Contest   string
	CQZ       int
	MyCQZ     int
}

// ValidCall reports whether s is an amateur callsign this log will store.
// Fragments the CW decoder emits ("CQ", "5NN", "TU5NN") are not calls.
func ValidCall(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) < 4 || len(s) > 14 || !callRe.MatchString(s) {
		return false
	}
	core := BaseCall(s)
	for _, bad := range []string{"5NN", "599", "55N", "57N", "58N"} {
		if strings.Contains(core, bad) {
			return false
		}
	}
	return true
}

// BaseCall strips a portable prefix or suffix ("VE3/W1AW/P" -> "W1AW").
// The longest piece wins, matching ops.baseCall.
func BaseCall(c string) string {
	best := ""
	for _, p := range strings.Split(strings.ToUpper(c), "/") {
		if len(p) > len(best) {
			best = p
		}
	}
	return best
}

// Header is the ADIF header both writers use. PROGRAMID stays "CW Console"
// so a log started by today's app and continued by the unified one is one log.
const Header = "ADIF log written by CW Console\n<ADIF_VER:5>3.1.4 <PROGRAMID:10>CW Console <EOH>\n\n"

// Format writes one ADIF record. The call is rejected, not escaped: ADIF has
// no escape syntax, the length is the count, and a call that contains "<"
// is not a call we should store. Free-text fields drop CR, LF and NUL and
// are then length-prefixed, so a name containing "<" round-trips through Parse.
func Format(q QSO) (string, error) {
	call := strings.ToUpper(strings.TrimSpace(q.Call))
	if !ValidCall(call) {
		return "", fmt.Errorf("logbook: %q is not a callsign", q.Call)
	}
	q.Call = call
	q.Mode = strings.ToUpper(strings.TrimSpace(q.Mode))
	if q.Mode == "" {
		q.Mode = "CW"
	}
	var b strings.Builder
	field := func(name, v string) {
		v = cleanText(v)
		if v == "" {
			return
		}
		fmt.Fprintf(&b, "<%s:%d>%s ", name, len(v), v)
	}
	t := q.Start.UTC()
	field("CALL", q.Call)
	field("QSO_DATE", t.Format("20060102"))
	field("TIME_ON", t.Format("150405"))
	field("BAND", q.Band)
	field("FREQ", q.Freq)
	field("MODE", q.Mode)
	field("GRIDSQUARE", q.Grid)
	field("RST_SENT", q.RSTSent)
	field("RST_RCVD", q.RSTRcvd)
	field("STATION_CALLSIGN", strings.ToUpper(q.MyCall))
	field("NAME", q.Name)
	field("STATE", strings.ToUpper(q.State))
	if q.DXCC > 0 {
		field("DXCC", strconv.Itoa(q.DXCC))
	}
	field("APP_CWCONSOLE_CTY", q.CTYPrefix)
	if q.Park != "" {
		field("MY_SIG", "POTA")
		field("MY_SIG_INFO", q.Park)
	}
	field("MY_STATE", q.MyState)
	field("MY_GRIDSQUARE", q.MyGrid)
	field("CONTEST_ID", q.Contest)
	if q.CQZ > 0 {
		field("CQZ", strconv.Itoa(q.CQZ))
	}
	if q.MyCQZ > 0 {
		field("MY_CQ_ZONE", strconv.Itoa(q.MyCQZ))
	}
	b.WriteString("<EOR>\n")
	return b.String(), nil
}

func cleanText(v string) string {
	if !utf8.ValidString(v) {
		v = strings.ToValidUTF8(v, "")
	}
	v = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, v)
	return strings.TrimSpace(v)
}

// Parse reads records this package writes. The field length is honored, so a
// "<" inside NAME is data, not a new tag.
func Parse(s string) ([]QSO, error) {
	if i := strings.Index(strings.ToUpper(s), "<EOH>"); i >= 0 {
		s = s[i+5:]
	}
	var out []QSO
	cur := map[string]string{}
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			break
		}
		s = s[i+1:]
		j := strings.IndexByte(s, '>')
		if j < 0 {
			return out, fmt.Errorf("logbook: truncated ADIF tag")
		}
		tag := strings.ToUpper(s[:j])
		s = s[j+1:]
		if tag == "EOR" {
			out = append(out, fromFields(cur))
			cur = map[string]string{}
			continue
		}
		name, n, ok := splitTag(tag)
		if !ok || n > len(s) {
			return out, fmt.Errorf("logbook: bad ADIF field %q", tag)
		}
		cur[name] = s[:n]
		s = s[n:]
	}
	return out, nil
}

func splitTag(tag string) (string, int, bool) {
	parts := strings.Split(tag, ":")
	if len(parts) < 2 {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return parts[0], n, true
}

func fromFields(m map[string]string) QSO {
	t, _ := time.ParseInLocation("20060102150405", m["QSO_DATE"]+padTime(m["TIME_ON"]), time.UTC)
	dxcc, _ := strconv.Atoi(strings.TrimSpace(m["DXCC"]))
	cqz, _ := strconv.Atoi(strings.TrimSpace(m["CQZ"]))
	mycqz, _ := strconv.Atoi(strings.TrimSpace(m["MY_CQ_ZONE"]))
	return QSO{
		Call: m["CALL"], Start: t, Freq: m["FREQ"], Band: strings.ToLower(m["BAND"]),
		Mode: strings.ToUpper(m["MODE"]), RSTSent: m["RST_SENT"], RSTRcvd: m["RST_RCVD"],
		MyCall: m["STATION_CALLSIGN"], Grid: m["GRIDSQUARE"], State: strings.ToUpper(m["STATE"]),
		MyState: m["MY_STATE"], MyGrid: m["MY_GRIDSQUARE"], DXCC: dxcc,
		CTYPrefix: m["APP_CWCONSOLE_CTY"], Name: m["NAME"], Park: m["MY_SIG_INFO"],
		Contest: m["CONTEST_ID"], CQZ: cqz, MyCQZ: mycqz,
	}
}

func padTime(t string) string {
	for len(t) < 6 {
		t += "0"
	}
	if len(t) > 6 {
		return t[:6]
	}
	return t
}

// Needs is what a decoded call is worth against the log.
// NewBand and NewMode are set only when the entity is already in the log.
// A first contact with an entity is NewDXCC, not also a new band and mode.
// NewState is independent: it is WAS-style, any station in that state.
type Needs struct {
	Worked   bool
	Last     QSO
	NewDXCC  bool
	NewBand  bool
	NewMode  bool
	NewState bool
}

// Labels is the short text the decode line shows, most useful first.
func (n Needs) Labels() []string {
	var out []string
	if n.NewDXCC {
		out = append(out, "new DXCC")
	}
	if n.NewState {
		out = append(out, "new state")
	}
	if n.NewBand {
		out = append(out, "new band")
	}
	if n.NewMode {
		out = append(out, "new mode")
	}
	return out
}

// Query is a decoded station plus whatever the lookup already knows.
// DXCC 0 and an empty prefix mean the entity is unknown, and NewDXCC stays
// false rather than claiming a new country.
type Query struct {
	Call      string
	DXCC      int
	CTYPrefix string
	Band      string
	Mode      string
	State     string
}

// Book is the in-memory index behind the decode card. Phase 1 fills it from
// the existing ADIF file.
type Book struct {
	qs []QSO
}

// Add stores q. The call must be a callsign.
func (b *Book) Add(q QSO) error {
	if !ValidCall(q.Call) {
		return fmt.Errorf("logbook: %q is not a callsign", q.Call)
	}
	q.Call = strings.ToUpper(strings.TrimSpace(q.Call))
	q.Start = q.Start.UTC()
	q.Band = strings.ToLower(strings.TrimSpace(q.Band))
	q.Mode = strings.ToUpper(strings.TrimSpace(q.Mode))
	q.State = strings.ToUpper(strings.TrimSpace(q.State))
	q.CTYPrefix = strings.ToUpper(strings.TrimSpace(q.CTYPrefix))
	b.qs = append(b.qs, q)
	return nil
}

// Assess answers "worked them before?" and which awards this contact would fill.
func (b *Book) Assess(q Query) Needs {
	var n Needs
	base := BaseCall(q.Call)
	for _, e := range b.qs {
		if base != "" && BaseCall(e.Call) == base {
			if !n.Worked || e.Start.After(n.Last.Start) {
				n.Last = e
			}
			n.Worked = true
		}
	}
	entityKnown := q.DXCC > 0 || strings.TrimSpace(q.CTYPrefix) != ""
	seenEntity := b.seenEntity(q.DXCC, q.CTYPrefix)
	if entityKnown && !seenEntity {
		n.NewDXCC = true
	}
	band := strings.ToLower(strings.TrimSpace(q.Band))
	mode := strings.ToUpper(strings.TrimSpace(q.Mode))
	if entityKnown && seenEntity && band != "" && !b.seenEntityBand(q.DXCC, q.CTYPrefix, band) {
		n.NewBand = true
	}
	if entityKnown && seenEntity && mode != "" && !b.seenEntityMode(q.DXCC, q.CTYPrefix, mode) {
		n.NewMode = true
	}
	state := strings.ToUpper(strings.TrimSpace(q.State))
	if state != "" && !b.seenState(state) {
		n.NewState = true
	}
	return n
}

func sameEntity(e QSO, dxcc int, prefix string) bool {
	if dxcc > 0 && e.DXCC == dxcc {
		return true
	}
	p := strings.ToUpper(strings.TrimSpace(prefix))
	return p != "" && strings.ToUpper(e.CTYPrefix) == p
}

func (b *Book) seenEntity(dxcc int, prefix string) bool {
	if dxcc <= 0 && strings.TrimSpace(prefix) == "" {
		return false
	}
	for _, e := range b.qs {
		if sameEntity(e, dxcc, prefix) {
			return true
		}
	}
	return false
}

func (b *Book) seenEntityBand(dxcc int, prefix, band string) bool {
	for _, e := range b.qs {
		if sameEntity(e, dxcc, prefix) && e.Band == band {
			return true
		}
	}
	return false
}

func (b *Book) seenEntityMode(dxcc int, prefix, mode string) bool {
	for _, e := range b.qs {
		if sameEntity(e, dxcc, prefix) && strings.ToUpper(e.Mode) == mode {
			return true
		}
	}
	return false
}

func (b *Book) seenState(state string) bool {
	for _, e := range b.qs {
		if e.State == state {
			return true
		}
	}
	return false
}
