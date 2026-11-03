package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QSO is one logged contact.
type QSO struct {
	Call    string
	Start   time.Time // UTC
	Freq    string    // MHz as typed ("14.058"), may be empty
	Band    string    // "20m"
	RSTSent string
	RSTRcvd string
	MyCall  string
	Park    string // POTA reference for this activation ("US-1234"), may be empty
	MyState string
	MyGrid  string
	Mode    string // "CW" (default when empty) or "FT8"
	Grid    string // their grid square, when known (FT8)
}

func modeOf(q QSO) string {
	if q.Mode == "" {
		return "CW"
	}
	return q.Mode
}

// Logbook keeps every contact in one ADIF file (cw-decoder-log.adi) and, for POTA,
// also writes the file the POTA uploader wants: CALL@PARK-YYYYMMDD.adi (UTC date).
type Logbook struct {
	Dir     string
	Seq     int // bumps on every change (the page refetches the list)
	entries []QSO
}

const masterName = "cw-decoder-log.adi"

// OpenLog loads the existing log (if any) from dir.
func OpenLog(dir string) *Logbook {
	l := &Logbook{Dir: dir}
	if b, err := os.ReadFile(filepath.Join(dir, masterName)); err == nil {
		l.entries = ParseADIF(string(b))
	}
	return l
}

// Bands (MHz edges, IARU/US amateur allocations).
var bands = []struct {
	name   string
	lo, hi float64
}{
	{"160m", 1.8, 2.0}, {"80m", 3.5, 4.0}, {"60m", 5.06, 5.45}, {"40m", 7.0, 7.3},
	{"30m", 10.1, 10.15}, {"20m", 14.0, 14.35}, {"17m", 18.068, 18.168}, {"15m", 21.0, 21.45},
	{"12m", 24.89, 24.99}, {"10m", 28.0, 29.7}, {"6m", 50, 54}, {"2m", 144, 148},
}

// ParseFreq reads what the operator typed in the Freq box: "14.058", "14058" (kHz),
// "7030", or a band ("20m", "20"). It returns the frequency in MHz (or "") and the band
// (or "" if it can't tell).
func ParseFreq(s string) (freq, band string) {
	s = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "mhz"), " ")
	if s == "" {
		return "", ""
	}
	if strings.HasSuffix(s, "m") {
		s = strings.TrimSuffix(s, "m")
		for _, b := range bands {
			if b.name == s+"m" {
				return "", b.name
			}
		}
		return "", ""
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return "", ""
	}
	for _, b := range bands { // a bare band number ("20") — but not 6 or 2, those are MHz-ish
		if b.name == s+"m" && v >= 10 && !strings.Contains(s, ".") {
			return "", b.name
		}
	}
	if v >= 1000 { // kHz
		v /= 1000
	}
	for _, b := range bands {
		if v >= b.lo && v <= b.hi {
			return strconv.FormatFloat(v, 'f', -1, 64), b.name
		}
	}
	return strconv.FormatFloat(v, 'f', -1, 64), ""
}

// Add logs q and returns whether it's a dupe (same call, band and park on the same UTC
// day; POTA counts it once, but it's still logged).
func (l *Logbook) Add(q QSO) (dupe bool, err error) {
	q.Call = strings.ToUpper(strings.TrimSpace(q.Call))
	if q.Call == "" {
		return false, errors.New("the Call box is empty")
	}
	q.Start = q.Start.UTC().Truncate(time.Second)
	dupe = l.IsDupeMode(q.Call, q.Band, modeOf(q), q.Park, q.Start)
	l.entries = append(l.entries, q)
	if err := l.write(q); err != nil {
		l.entries = l.entries[:len(l.entries)-1]
		return false, err
	}
	l.Seq++
	return dupe, nil
}

// IsDupe: already worked call on band (and park) in CW on the UTC day of t.
func (l *Logbook) IsDupe(call, band, park string, t time.Time) bool {
	return l.IsDupeMode(call, band, "CW", park, t)
}

// IsDupeMode: the same for a given mode (POTA counts each band and mode separately).
func (l *Logbook) IsDupeMode(call, band, mode, park string, t time.Time) bool {
	day := t.UTC().Format("20060102")
	for _, e := range l.entries {
		if SameStation(e.Call, call) && e.Band == band && modeOf(e) == mode && strings.EqualFold(e.Park, park) && e.Start.UTC().Format("20060102") == day {
			return true
		}
	}
	return false
}

// Undo removes the last contact.
func (l *Logbook) Undo() (QSO, error) {
	if len(l.entries) == 0 {
		return QSO{}, errors.New("the log is empty")
	}
	q := l.entries[len(l.entries)-1]
	l.entries = l.entries[:len(l.entries)-1]
	if err := l.rewrite(q); err != nil {
		l.entries = append(l.entries, q)
		return QSO{}, err
	}
	l.Seq++
	return q, nil
}

// Recent returns up to n contacts, newest first.
func (l *Logbook) Recent(n int) []QSO {
	var out []QSO
	for i := len(l.entries) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, l.entries[i])
	}
	return out
}

// CountToday counts contacts on the current UTC day (for park, if set).
func (l *Logbook) CountToday(park string, now time.Time) int {
	day, n := now.UTC().Format("20060102"), 0
	for _, e := range l.entries {
		if e.Start.UTC().Format("20060102") == day && strings.EqualFold(e.Park, park) {
			n++
		}
	}
	return n
}

// POTAFile is the per-activation file name, e.g. KN0BLE@US-1234-20261012.adi.
func POTAFile(myCall, park string, t time.Time) string {
	call := strings.ReplaceAll(strings.ToUpper(myCall), "/", "_")
	return fmt.Sprintf("%s@%s-%s.adi", call, strings.ToUpper(park), t.UTC().Format("20060102"))
}

// LastFile is where the most recent contact went (the POTA file if it had a park).
func (l *Logbook) LastFile() string {
	if len(l.entries) == 0 {
		return filepath.Join(l.Dir, masterName)
	}
	q := l.entries[len(l.entries)-1]
	if q.Park != "" {
		return filepath.Join(l.Dir, POTAFile(q.MyCall, q.Park, q.Start))
	}
	return filepath.Join(l.Dir, masterName)
}

func (l *Logbook) write(q QSO) error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	if err := appendRecord(filepath.Join(l.Dir, masterName), q); err != nil {
		return err
	}
	if q.Park != "" {
		return appendRecord(filepath.Join(l.Dir, POTAFile(q.MyCall, q.Park, q.Start)), q)
	}
	return nil
}

// rewrite regenerates the master file and the POTA file q belonged to.
func (l *Logbook) rewrite(q QSO) error {
	if err := writeAll(filepath.Join(l.Dir, masterName), l.entries); err != nil {
		return err
	}
	if q.Park == "" {
		return nil
	}
	name := POTAFile(q.MyCall, q.Park, q.Start)
	var same []QSO
	for _, e := range l.entries {
		if e.Park != "" && POTAFile(e.MyCall, e.Park, e.Start) == name {
			same = append(same, e)
		}
	}
	if len(same) == 0 {
		err := os.Remove(filepath.Join(l.Dir, name))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return writeAll(filepath.Join(l.Dir, name), same)
}

const adifHeader = "ADIF log written by CW Decoder\n<ADIF_VER:5>3.1.4 <PROGRAMID:10>CW Decoder <EOH>\n\n"

func appendRecord(path string, q QSO) error {
	_, err := os.Stat(path)
	f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr != nil {
		return ferr
	}
	defer f.Close()
	if os.IsNotExist(err) {
		if _, err := f.WriteString(adifHeader); err != nil {
			return err
		}
	}
	_, err = f.WriteString(Record(q))
	return err
}

func writeAll(path string, qs []QSO) error {
	var b strings.Builder
	b.WriteString(adifHeader)
	for _, q := range qs {
		b.WriteString(Record(q))
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Record formats one ADIF record.
func Record(q QSO) string {
	var b strings.Builder
	f := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&b, "<%s:%d>%s ", name, len(v), v)
		}
	}
	t := q.Start.UTC()
	f("CALL", q.Call)
	f("QSO_DATE", t.Format("20060102"))
	f("TIME_ON", t.Format("150405"))
	f("BAND", q.Band)
	f("FREQ", q.Freq)
	f("MODE", modeOf(q))
	f("GRIDSQUARE", q.Grid)
	f("RST_SENT", q.RSTSent)
	f("RST_RCVD", q.RSTRcvd)
	f("STATION_CALLSIGN", q.MyCall)
	f("OPERATOR", baseCall(q.MyCall))
	if q.Park != "" {
		f("MY_SIG", "POTA")
		f("MY_SIG_INFO", q.Park)
	}
	f("MY_STATE", q.MyState)
	f("MY_GRIDSQUARE", q.MyGrid)
	b.WriteString("<EOR>\n")
	return b.String()
}

// ParseADIF reads records back (only the fields this program writes).
func ParseADIF(s string) []QSO {
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
			break
		}
		tag := strings.ToUpper(s[:j])
		s = s[j+1:]
		if tag == "EOR" {
			out = append(out, fromFields(cur))
			cur = map[string]string{}
			continue
		}
		parts := strings.Split(tag, ":")
		if len(parts) < 2 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 0 || n > len(s) {
			break
		}
		cur[parts[0]] = s[:n]
		s = s[n:]
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Start.Before(out[b].Start) })
	return out
}

func fromFields(m map[string]string) QSO {
	t, _ := time.ParseInLocation("20060102150405", m["QSO_DATE"]+padTime(m["TIME_ON"]), time.UTC)
	return QSO{Call: m["CALL"], Start: t, Freq: m["FREQ"], Band: strings.ToLower(m["BAND"]),
		RSTSent: m["RST_SENT"], RSTRcvd: m["RST_RCVD"], MyCall: m["STATION_CALLSIGN"],
		Park: m["MY_SIG_INFO"], MyState: m["MY_STATE"], MyGrid: m["MY_GRIDSQUARE"],
		Mode: strings.ToUpper(m["MODE"]), Grid: m["GRIDSQUARE"]}
}

func padTime(t string) string {
	for len(t) < 6 {
		t += "0"
	}
	return t[:6]
}
