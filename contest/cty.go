// Package contest: contest logging aids. The country and CQ zone of a call (AD1C's
// cty.dat), CQ WW DX scoring and the Cabrillo log the contest wants.
//
// cty.dat is Jim Reisert AD1C's country file (https://www.country-files.com), the one
// nearly every contest logger reads. A copy is built in; a newer cty.dat in the app's
// data folder is read instead (AD1C posts updates before the big contests).
package contest

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"strconv"
	"strings"
)

//go:embed cty.dat
var builtinCTY string

// Entity: one country in cty.dat. For CQ WW a country is a DXCC entity or one of the WAE
// list's extras (Prefix starts with "*": *IG9, *4U1V, *GM/s, *IT9, *JW/b, *TA1).
type Entity struct {
	Name   string
	Prefix string // primary prefix, as in cty.dat ("K", "DL", "*IG9"): the country's identity
	CQ     int    // CQ zone
	ITU    int
	Cont   string // NA SA EU AF AS OC
}

// Match: what a call looks up to. CQ and Cont may differ from the entity's (cty.dat
// overrides them for some prefixes and calls).
type Match struct {
	Entity *Entity
	CQ     int
	Cont   string
	MM     bool // maritime mobile (/MM): a zone, but no country (CQ WW)
}

type ctyItem struct {
	ent  *Entity
	cq   int
	cont string
}

// CTY: a parsed country file.
type CTY struct {
	Version  string // =VERyyyymmdd, when the file has one
	Entities []*Entity
	prefix   map[string]ctyItem
	exact    map[string]ctyItem
	maxLen   int
}

// Builtin: the country file built into the app.
func Builtin() *CTY {
	c, err := ParseCTY(strings.NewReader(builtinCTY))
	if err != nil {
		panic("built-in cty.dat: " + err.Error())
	}
	return c
}

// ParseCTY reads a cty.dat: a header line per entity
// (Name: CQ: ITU: Cont: Lat: Lon: UTC offset: Prefix:) then its prefixes, comma
// separated up to a ";". "=CALL" is a whole call; (n) [n] {XX} override the CQ zone,
// ITU zone and continent; <lat/lon> and ~tz~ are skipped.
func ParseCTY(r io.Reader) (*CTY, error) {
	c := &CTY{prefix: map[string]ctyItem{}, exact: map[string]ctyItem{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	var cur *Entity
	var body strings.Builder
	flush := func() {
		if cur == nil {
			return
		}
		for _, it := range strings.Split(strings.TrimSuffix(strings.TrimSpace(body.String()), ";"), ",") {
			c.addItem(cur, strings.TrimSpace(it))
		}
		body.Reset()
		cur = nil
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			flush()
			f := strings.Split(line, ":")
			if len(f) < 8 {
				return nil, fmt.Errorf("cty.dat: bad entity line %q", line)
			}
			cq, err1 := strconv.Atoi(strings.TrimSpace(f[1]))
			itu, err2 := strconv.Atoi(strings.TrimSpace(f[2]))
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("cty.dat: bad zones in %q", line)
			}
			cur = &Entity{Name: strings.TrimSpace(f[0]), CQ: cq, ITU: itu,
				Cont: strings.TrimSpace(f[3]), Prefix: strings.TrimSpace(f[7])}
			c.Entities = append(c.Entities, cur)
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("cty.dat: prefixes before any entity: %q", line)
		}
		body.WriteString(line)
		if strings.Contains(line, ";") {
			flush()
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(c.Entities) < 300 {
		return nil, fmt.Errorf("cty.dat: only %d entities; not a country file", len(c.Entities))
	}
	return c, nil
}

func (c *CTY) addItem(e *Entity, it string) {
	if it == "" {
		return
	}
	exact := strings.HasPrefix(it, "=")
	it = strings.TrimPrefix(it, "=")
	item := ctyItem{ent: e, cq: e.CQ, cont: e.Cont}
	key := it
	if i := strings.IndexAny(it, "([<{~"); i >= 0 {
		key = it[:i]
		rest := it[i:]
		for len(rest) > 0 {
			open := rest[0]
			close := map[byte]byte{'(': ')', '[': ']', '<': '>', '{': '}', '~': '~'}[open]
			j := strings.IndexByte(rest[1:], close)
			if close == 0 || j < 0 {
				break
			}
			v := rest[1 : 1+j]
			switch open {
			case '(':
				if n, err := strconv.Atoi(v); err == nil {
					item.cq = n
				}
			case '{':
				item.cont = v
			}
			rest = rest[j+2:]
		}
	}
	key = strings.ToUpper(key)
	m := c.prefix
	if exact {
		m = c.exact
	}
	// A call listed under both a DXCC entity and a WAE one (4U1VIC: Austria and Vienna
	// Intl Ctr) is the WAE one: that's the list CQ WW counts.
	if old, ok := m[key]; ok && strings.HasPrefix(old.ent.Prefix, "*") && !strings.HasPrefix(e.Prefix, "*") {
		return
	}
	if exact {
		if strings.HasPrefix(key, "VER") && e.Name != "" && c.Version == "" && len(key) == 11 {
			c.Version = key[3:]
		}
		c.exact[key] = item
		return
	}
	m[key] = item
	if len(key) > c.maxLen {
		c.maxLen = len(key)
	}
}

// Lookup: the country and CQ zone of a call as heard on the air (portable forms too:
// DL/W1ABC, W1ABC/KH6, VE3ABC/2, G4ABC/P, K1ABC/MM).
func (c *CTY) Lookup(call string) (Match, bool) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" {
		return Match{}, false
	}
	if it, ok := c.exact[call]; ok {
		return Match{Entity: it.ent, CQ: it.cq, Cont: it.cont}, true
	}
	parts := strings.Split(call, "/")
	mm := false
	// Drop the suffixes that don't change the country.
	for len(parts) > 1 {
		switch parts[len(parts)-1] {
		case "P", "M", "QRP", "QRPP", "A", "B", "J", "R", "LH", "":
			parts = parts[:len(parts)-1]
			continue
		case "MM":
			mm = true
			parts = parts[:len(parts)-1]
			continue
		case "AM": // aeronautical mobile: no country, no zone
			return Match{}, false
		}
		break
	}
	if len(parts) == 1 || mm {
		if it, ok := c.exact[parts[0]]; ok && !mm {
			return Match{Entity: it.ent, CQ: it.cq, Cont: it.cont}, true
		}
		m, ok := c.byPrefix(parts[0])
		if mm {
			// Somewhere at sea: the zone comes from the exchange; no country.
			return Match{CQ: 0, Cont: m.Cont, MM: true}, true
		}
		return m, ok
	}
	if it, ok := c.exact[strings.Join(parts, "/")]; ok {
		return Match{Entity: it.ent, CQ: it.cq, Cont: it.cont}, true
	}
	a, b := parts[0], parts[len(parts)-1]
	// W1ABC/4, VE3ABC/2, UA9XYZ/1: the same call in another call area.
	if len(b) == 1 && b[0] >= '0' && b[0] <= '9' {
		if i := strings.IndexAny(a, "0123456789"); i > 0 {
			return c.byPrefix(a[:i] + b)
		}
		return c.byPrefix(a)
	}
	// Where the station is: the shorter part (DL/W1ABC, W1ABC/KH6); a tie, the first.
	if len(b) < len(a) {
		a = b
	}
	return c.byPrefix(a)
}

func (c *CTY) byPrefix(s string) (Match, bool) {
	n := len(s)
	if n > c.maxLen {
		n = c.maxLen
	}
	for ; n > 0; n-- {
		if it, ok := c.prefix[s[:n]]; ok {
			return Match{Entity: it.ent, CQ: it.cq, Cont: it.cont}, true
		}
	}
	return Match{}, false
}
