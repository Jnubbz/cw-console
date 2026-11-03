package ft710

// SWR sweep: short, low-power CW key-downs across a band, reading the radio's SWR meter at
// each step. CW (A1A) is used because it's allowed everywhere in every US amateur HF band,
// so a sweep never puts a phone or data emission in the CW-only segments.
//
// The radio is keyed with the caller's key line (DTR/RTS on the Standard COM port), the
// same way CW is sent, with break-in on; frequency, mode, power and meters go over CAT.
// Whatever happens (an error, Stop), the key is released and the radio is put back the way
// it was: frequency, mode, power, split, clarifier and break-in.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CAT sends commands in order and returns the answers to the queries among them.
type CAT func(cmds ...string) ([]string, error)

// Keyer keys the transmitter (the CW key line).
type Keyer interface{ Key(down bool) error }

// Sweep limits.
const (
	MaxSweepPower  = 10  // W
	MaxSweepPoints = 101 //
	MinSweepStep   = 1000
)

// Band is a US amateur band (FCC part 97 edges, all license classes together). 60 m is
// channelized, so it isn't swept.
type Band struct {
	Name   string `json:"name"`
	FromHz int    `json:"fromHz"`
	ToHz   int    `json:"toHz"`
	StepHz int    `json:"stepHz"` // a sensible default step
}

var Bands = []Band{
	{"160m", 1_800_000, 2_000_000, 5_000},
	{"80m", 3_500_000, 4_000_000, 10_000},
	{"40m", 7_000_000, 7_300_000, 10_000},
	{"30m", 10_100_000, 10_150_000, 2_000},
	{"20m", 14_000_000, 14_350_000, 10_000},
	{"17m", 18_068_000, 18_168_000, 5_000},
	{"15m", 21_000_000, 21_450_000, 15_000},
	{"12m", 24_890_000, 24_990_000, 5_000},
	{"10m", 28_000_000, 29_700_000, 50_000},
	{"6m", 50_000_000, 54_000_000, 100_000},
}

// BandOf: the band hz is in, or nil.
func BandOf(hz int) *Band {
	for i := range Bands {
		if hz >= Bands[i].FromHz && hz <= Bands[i].ToHz {
			return &Bands[i]
		}
	}
	return nil
}

type SweepConfig struct {
	FromHz, ToHz, StepHz int
	PowerW               int
	KeyDown              time.Duration       // carrier per point (default 250 ms)
	Settle               time.Duration       // after retuning, before keying (default 80 ms)
	Gap                  time.Duration       // after unkeying (default 150 ms)
	Sleep                func(time.Duration) // test hook (default time.Sleep)
}

type Point struct {
	Hz    int     `json:"hz"`
	Raw   int     `json:"raw"`
	SWR   float64 `json:"swr"`
	Watts float64 `json:"watts"`
}

// Plan: the frequencies a sweep visits, checked against the limits.
func (c SweepConfig) Plan() ([]int, error) {
	if c.PowerW < MinPower || c.PowerW > MaxSweepPower {
		return nil, fmt.Errorf("sweep power must be %d-%d W", MinPower, MaxSweepPower)
	}
	if c.StepHz < MinSweepStep {
		return nil, fmt.Errorf("sweep step must be at least %d Hz", MinSweepStep)
	}
	if c.FromHz >= c.ToHz {
		return nil, errors.New("sweep start must be below its end")
	}
	b := BandOf(c.FromHz)
	if b == nil || c.ToHz > b.ToHz {
		return nil, fmt.Errorf("%.3f-%.3f MHz isn't inside one amateur band", float64(c.FromHz)/1e6, float64(c.ToHz)/1e6)
	}
	var f []int
	for hz := c.FromHz; hz <= c.ToHz; hz += c.StepHz {
		f = append(f, hz)
	}
	if f[len(f)-1] != c.ToHz {
		f = append(f, c.ToHz)
	}
	if len(f) > MaxSweepPoints {
		return nil, fmt.Errorf("%d points is too many (at most %d): use a bigger step", len(f), MaxSweepPoints)
	}
	return f, nil
}

// Sweep runs the sweep, calling onPoint after each reading. It returns the points read so
// far even on error or cancel.
func Sweep(ctx context.Context, cat CAT, key Keyer, c SweepConfig, onPoint func(Point)) (pts []Point, err error) {
	plan, err := c.Plan()
	if err != nil {
		return nil, err
	}
	if c.KeyDown == 0 {
		c.KeyDown = 250 * time.Millisecond
	}
	if c.Settle == 0 {
		c.Settle = 80 * time.Millisecond
	}
	if c.Gap == 0 {
		c.Gap = 150 * time.Millisecond
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	// How the radio is now, to put it back.
	saved, err := cat("FA;", "MD0;", "PC;", "BI;", "FT;", "CF000;")
	if err != nil {
		return nil, fmt.Errorf("reading the radio before the sweep: %v", err)
	}
	if len(saved) != 6 {
		return nil, fmt.Errorf("reading the radio before the sweep: %d answers", len(saved))
	}
	restore := []string{strip(saved[1], "MD0", "MD0"), strip(saved[2], "PC", "PC"), strip(saved[3], "BI", "BI"),
		strip(saved[4], "FT", "FT"), strip(saved[5], "CF000", "CF000"), saved[0]}
	for _, r := range restore {
		if r == "" {
			return nil, fmt.Errorf("unexpected answer while reading the radio: %q", saved)
		}
	}
	keyed := false
	defer func() {
		if keyed {
			key.Key(false)
		}
		if _, rerr := cat(restore...); rerr != nil && err == nil {
			err = fmt.Errorf("the sweep finished but putting the radio back failed: %v", rerr)
		}
	}()

	pw, _ := SetPower(c.PowerW)
	// Transmit where we tune: no split, clarifier off; CW, break-in on so the key line keys.
	if _, err = cat(SetSplit(false), SetClar(false, false), "MD03;", pw, SetBreakIn(true)); err != nil {
		return nil, err
	}
	for _, hz := range plan {
		if ctx.Err() != nil {
			return pts, ctx.Err()
		}
		fa, _ := SetFreqA(hz)
		if _, err = cat(fa); err != nil {
			return pts, err
		}
		sleep(c.Settle)
		if err = key.Key(true); err != nil {
			return pts, fmt.Errorf("keying: %v", err)
		}
		keyed = true
		sleep(c.KeyDown)
		a, merr := cat(MeterSWR, MeterPO)
		if err = key.Key(false); err != nil {
			return pts, fmt.Errorf("unkeying: %v", err)
		}
		keyed = false
		if merr != nil {
			return pts, merr
		}
		raw, err := MeterRaw(a[0], MeterSWR)
		if err != nil {
			return pts, err
		}
		po, err := MeterRaw(a[1], MeterPO)
		if err != nil {
			return pts, err
		}
		p := Point{Hz: hz, Raw: raw, SWR: SWR(raw), Watts: Watts(po)}
		pts = append(pts, p)
		if onPoint != nil {
			onPoint(p)
		}
		sleep(c.Gap)
	}
	return pts, nil
}

// Best: the point with the lowest SWR.
func Best(pts []Point) (Point, bool) {
	if len(pts) == 0 {
		return Point{}, false
	}
	b := pts[0]
	for _, p := range pts[1:] {
		if p.SWR < b.SWR {
			b = p
		}
	}
	return b, true
}

// strip turns a read answer back into the set command ("PC050;" -> "PC050;", checked).
func strip(answer, prefix, set string) string {
	v, err := after(answer, prefix)
	if err != nil || v == "" {
		return ""
	}
	return set + v + ";"
}
