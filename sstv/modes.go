// Package sstv encodes and decodes slow-scan TV pictures (analog FM subcarrier:
// 1200 Hz sync, 1500 Hz black to 2300 Hz white).
//
// Copied from github.com/Jnubbz/sstv-console (AGPL-3.0). The update signing key
// from that program is not in this repository.
//
// Timings and VIS codes: JL Barber N7CXI, "Proposal for SSTV Mode Specifications" (Dayton
// 2000) for Martin, Scottie and Robot; the PD figures are the ones slowrx uses.
package sstv

// Family is how a mode lays out a line.
type Family int

const (
	Martin  Family = iota // sync, G, B, R
	Scottie               // G, B, sync, R (the sync is mid-line)
	Robot36               // sync, Y, then R-Y on even lines and B-Y on odd lines (half width)
	Robot72               // sync, Y, R-Y, B-Y (chroma half width)
	PD                    // sync, Y(row 2n), R-Y, B-Y, Y(row 2n+1): one radio line is two rows
)

// Mode is one SSTV mode. Times are in seconds.
type Mode struct {
	Name          string
	VIS           byte // 7 bits
	Width, Height int
	Family        Family
	Sync          float64 // sync pulse, 1200 Hz
	Porch         float64 // after the sync, 1500 Hz
	Sep           float64 // between colour scans
	Scan          float64 // one full-width scan (G, B, R or Y)
}

const (
	FreqSync  = 1200.0
	FreqBlack = 1500.0
	FreqWhite = 2300.0
	freqLead  = 1900.0
)

// Modes is every mode this package knows, most common on HF first.
var Modes = []*Mode{
	{Name: "Scottie 1", VIS: 60, Width: 320, Height: 256, Family: Scottie, Sync: 9e-3, Porch: 1.5e-3, Sep: 1.5e-3, Scan: 138.240e-3},
	{Name: "Scottie 2", VIS: 56, Width: 320, Height: 256, Family: Scottie, Sync: 9e-3, Porch: 1.5e-3, Sep: 1.5e-3, Scan: 88.064e-3},
	{Name: "Scottie DX", VIS: 76, Width: 320, Height: 256, Family: Scottie, Sync: 9e-3, Porch: 1.5e-3, Sep: 1.5e-3, Scan: 345.6e-3},
	{Name: "Martin 1", VIS: 44, Width: 320, Height: 256, Family: Martin, Sync: 4.862e-3, Porch: 0.572e-3, Sep: 0.572e-3, Scan: 146.432e-3},
	{Name: "Martin 2", VIS: 40, Width: 320, Height: 256, Family: Martin, Sync: 4.862e-3, Porch: 0.572e-3, Sep: 0.572e-3, Scan: 73.216e-3},
	{Name: "Robot 36", VIS: 8, Width: 320, Height: 240, Family: Robot36, Sync: 9e-3, Porch: 3e-3, Sep: 6e-3, Scan: 88e-3},
	{Name: "Robot 72", VIS: 12, Width: 320, Height: 240, Family: Robot72, Sync: 9e-3, Porch: 3e-3, Sep: 6e-3, Scan: 138e-3},
	{Name: "PD120", VIS: 95, Width: 640, Height: 496, Family: PD, Sync: 20e-3, Porch: 2.08e-3, Scan: 121.6e-3},
	{Name: "PD180", VIS: 96, Width: 640, Height: 496, Family: PD, Sync: 20e-3, Porch: 2.08e-3, Scan: 183.04e-3},
	{Name: "PD240", VIS: 97, Width: 640, Height: 496, Family: PD, Sync: 20e-3, Porch: 2.08e-3, Scan: 244.48e-3},
}

// ByVIS finds a mode by its VIS code.
func ByVIS(code byte) *Mode {
	for _, m := range Modes {
		if m.VIS == code {
			return m
		}
	}
	return nil
}

// ByName finds a mode by its name.
func ByName(name string) *Mode {
	for _, m := range Modes {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// seg is one scan within a line: which plane it fills and when.
type seg struct {
	plane int     // 0,1,2 = R,G,B for RGB modes; Y,Cr,Cb for the others
	row   int     // PD: 0 = first row of the pair, 1 = second
	start float64 // from the start of the line
	dur   float64
}

// layout: the line's length, where its sync pulse starts, and its scans. A line starts at
// its sync pulse, except Scottie, where it starts at the separator before green.
func (m *Mode) layout() (line, syncAt float64, segs []seg) {
	switch m.Family {
	case Martin:
		g := m.Sync + m.Porch
		b := g + m.Scan + m.Sep
		r := b + m.Scan + m.Sep
		return r + m.Scan + m.Sep, 0, []seg{{1, 0, g, m.Scan}, {2, 0, b, m.Scan}, {0, 0, r, m.Scan}}
	case Scottie:
		g := m.Sep
		b := g + m.Scan + m.Sep
		s := b + m.Scan
		r := s + m.Sync + m.Porch
		return r + m.Scan, s, []seg{{1, 0, g, m.Scan}, {2, 0, b, m.Scan}, {0, 0, r, m.Scan}}
	case Robot36:
		y := m.Sync + m.Porch
		c := y + m.Scan + m.Sep
		return c + m.Scan/2, 0, []seg{{0, 0, y, m.Scan}, {1, 0, c, m.Scan / 2}} // plane 1 or 2 by line
	case Robot72:
		y := m.Sync + m.Porch
		c1 := y + m.Scan + m.Sep
		c2 := c1 + m.Scan/2 + m.Sep
		return c2 + m.Scan/2, 0, []seg{{0, 0, y, m.Scan}, {1, 0, c1, m.Scan / 2}, {2, 0, c2, m.Scan / 2}}
	default: // PD
		y0 := m.Sync + m.Porch
		return y0 + 4*m.Scan, 0, []seg{{0, 0, y0, m.Scan}, {1, 0, y0 + m.Scan, m.Scan}, {2, 0, y0 + 2*m.Scan, m.Scan}, {0, 1, y0 + 3*m.Scan, m.Scan}}
	}
}

// radioLines: lines on the air (PD sends two picture rows per line).
func (m *Mode) radioLines() int {
	if m.Family == PD {
		return m.Height / 2
	}
	return m.Height
}

func (m *Mode) rgb() bool { return m.Family == Martin || m.Family == Scottie }

// Duration of the picture on the air, without the header.
func (m *Mode) Duration() float64 {
	l, _, _ := m.layout()
	return l * float64(m.radioLines())
}

// Colour: the Y, R-Y, B-Y scaling from the Dayton paper (appendix B).
func toYC(r, g, b float64) (y, cr, cb float64) {
	y = 16 + .003906*(65.738*r+129.057*g+25.064*b)
	cr = 128 + .003906*(112.439*r-94.154*g-18.285*b)
	cb = 128 + .003906*(-37.945*r-74.494*g+112.439*b)
	return
}

func fromYC(y, cr, cb float64) (r, g, b float64) {
	r = .003906 * (298.082*(y-16) + 408.583*(cr-128))
	g = .003906 * (298.082*(y-16) - 100.291*(cb-128) - 208.12*(cr-128))
	b = .003906 * (298.082*(y-16) + 516.411*(cb-128))
	return
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
