package contest

import (
	"strings"
	"testing"
)

func TestBuiltinCTY(t *testing.T) {
	c := Builtin()
	if c.Version != "20200405" {
		t.Errorf("version %q", c.Version)
	}
	for _, tc := range []struct {
		call, prefix string
		cq           int
		cont         string
	}{
		{"KN0BLE", "K", 5, "NA"}, // cty.dat's guess; the exchange has the real zone
		{"W1AW", "K", 5, "NA"},
		{"DL1ABC", "DL", 14, "EU"},
		{"JA1ZZZ", "JA", 25, "AS"},
		{"VE3ABC", "VE", 5, "NA"}, // (this file has Canada in zone 5 but for listed exceptions)
		{"KH6ABC", "KH6", 31, "OC"},
		{"KL7XX", "KL", 1, "NA"},
		{"IT9ABC", "*IT9", 15, "EU"},   // Sicily: a WAE country, counts in CQ WW
		{"IG9A", "*IG9", 33, "AF"},     // African Italy
		{"GM4ABC", "GM", 14, "EU"},     // Scotland
		{"UA9AA", "UA9", 17, "AS"},     // Asiatic Russia
		{"UA9AA/1", "UA", 16, "EU"},    // ...operating in European Russia
		{"DL/W1ABC", "DL", 14, "EU"},   // where the station is
		{"W1ABC/KH6", "KH6", 31, "OC"}, //
		{"G4ABC/P", "G", 14, "EU"},
		{"VE8ABC/2", "VE", 5, "NA"},
		{"VY0ABC", "VE", 2, "NA"}, // a (2) override
		{"PY2ABC", "PY", 11, "SA"},
		{"ZL1ABC", "ZL", 32, "OC"},
		{"ZS6ABC", "ZS", 38, "AF"},
		{"4U1VIC", "*4U1V", 15, "EU"},
	} {
		m, ok := c.Lookup(tc.call)
		if !ok || m.Entity == nil || m.Entity.Prefix != tc.prefix || m.CQ != tc.cq || m.Cont != tc.cont {
			got := "nothing"
			if ok && m.Entity != nil {
				got = m.Entity.Prefix + " " + m.Entity.Name
			}
			t.Errorf("%s: got %s zone %d %s; want %s zone %d %s", tc.call, got, m.CQ, m.Cont, tc.prefix, tc.cq, tc.cont)
		}
	}
	if m, ok := c.Lookup("K1ABC/MM"); !ok || !m.MM || m.Entity != nil {
		t.Errorf("maritime mobile: %+v %v", m, ok)
	}
	for _, bad := range []string{"", "K1ABC/AM"} {
		if _, ok := c.Lookup(bad); ok {
			t.Errorf("%q should look up to nothing", bad)
		}
	}
}

func TestParseCTYRejectsJunk(t *testing.T) {
	for _, s := range []string{"", "hello world\n", "Name: x: 1: NA: 0: 0: 0: K:\n    K;\n"} {
		if _, err := ParseCTY(strings.NewReader(s)); err == nil {
			t.Errorf("%q: expected an error", s)
		}
	}
}
