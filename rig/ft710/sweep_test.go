package ft710

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRig: a pretend FT-710 that remembers settings, answers queries, and reads an SWR
// dip at 14.150 MHz while keyed.
type fakeRig struct {
	fa, mode, pc, bi, ft, cf string
	keyed                    bool
	log                      []string
	failAt                   string
}

func newFake() *fakeRig {
	return &fakeRig{fa: "014058000", mode: "7", pc: "100", bi: "0", ft: "1", cf: "10000"}
}

func (r *fakeRig) cat(cmds ...string) ([]string, error) {
	var out []string
	for _, c := range cmds {
		r.log = append(r.log, c)
		if r.failAt != "" && strings.HasPrefix(c, r.failAt) {
			return out, errors.New("radio went away")
		}
		switch {
		case c == "FA;":
			out = append(out, "FA"+r.fa+";")
		case c == "MD0;":
			out = append(out, "MD0"+r.mode+";")
		case c == "PC;":
			out = append(out, "PC"+r.pc+";")
		case c == "BI;":
			out = append(out, "BI"+r.bi+";")
		case c == "FT;":
			out = append(out, "FT"+r.ft+";")
		case c == "CF000;":
			out = append(out, "CF000"+r.cf+";")
		case c == MeterSWR:
			raw := 0
			if r.keyed {
				hz, _ := strconv.Atoi(r.fa)
				d := hz - 14_150_000
				if d < 0 {
					d = -d
				}
				raw = min(255, d/1000) // 1.0 at 14.150, rising either side
			}
			out = append(out, fmt.Sprintf("RM6%03d;", raw))
		case c == MeterPO:
			po := 0
			if r.keyed {
				po = 27 // 5 W
			}
			out = append(out, fmt.Sprintf("RM5%03d;", po))
		case strings.HasPrefix(c, "FA"):
			r.fa = c[2:11]
		case strings.HasPrefix(c, "MD0"):
			r.mode = c[3:4]
		case strings.HasPrefix(c, "PC"):
			r.pc = c[2:5]
		case strings.HasPrefix(c, "BI"):
			r.bi = c[2:3]
		case strings.HasPrefix(c, "FT"):
			r.ft = c[2:3]
		case strings.HasPrefix(c, "CF000"):
			r.cf = c[5:10]
		default:
			return out, fmt.Errorf("fake rig doesn't know %q", c)
		}
	}
	return out, nil
}

type fakeKey struct {
	r          *fakeRig
	downs, ups int
}

func (k *fakeKey) Key(down bool) error {
	if down {
		if k.r.mode != "3" || k.r.bi != "1" || k.r.pc > "010" {
			return fmt.Errorf("keyed with mode %s, break-in %s, power %s", k.r.mode, k.r.bi, k.r.pc)
		}
		k.downs++
	} else {
		k.ups++
	}
	k.r.keyed = down
	return nil
}

func noSleep(time.Duration) {}

func TestSweepFindsTheDipAndRestores(t *testing.T) {
	r := newFake()
	k := &fakeKey{r: r}
	var seen int
	pts, err := Sweep(context.Background(), r.cat, k,
		SweepConfig{FromHz: 14_000_000, ToHz: 14_350_000, StepHz: 10_000, PowerW: 5, Sleep: noSleep},
		func(Point) { seen++ })
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 36 || seen != 36 || k.downs != 36 || k.ups != 36 {
		t.Fatalf("points %d, callbacks %d, downs %d, ups %d", len(pts), seen, k.downs, k.ups)
	}
	b, _ := Best(pts)
	if b.Hz != 14_150_000 || b.SWR != 1.0 || b.Watts != 5 {
		t.Fatalf("best %+v", b)
	}
	if r.keyed || r.fa != "014058000" || r.mode != "7" || r.pc != "100" || r.bi != "0" || r.ft != "1" || r.cf != "10000" {
		t.Fatalf("radio not restored: %+v", r)
	}
}

func TestSweepUnkeysAndRestoresOnError(t *testing.T) {
	r := newFake()
	k := &fakeKey{r: r}
	r.failAt = "RM6" // meters fail while keyed
	_, err := Sweep(context.Background(), r.cat, k,
		SweepConfig{FromHz: 7_000_000, ToHz: 7_100_000, StepHz: 10_000, PowerW: 5, Sleep: noSleep}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	r.failAt = ""
	if r.keyed || k.downs != k.ups {
		t.Fatalf("left keyed: downs %d ups %d", k.downs, k.ups)
	}
	if r.mode != "7" || r.pc != "100" || r.fa != "014058000" {
		t.Fatalf("radio not restored: %+v", r)
	}
}

func TestSweepStopsWhenCancelled(t *testing.T) {
	r := newFake()
	k := &fakeKey{r: r}
	ctx, cancel := context.WithCancel(context.Background())
	pts, err := Sweep(ctx, r.cat, k,
		SweepConfig{FromHz: 14_000_000, ToHz: 14_350_000, StepHz: 10_000, PowerW: 5, Sleep: noSleep},
		func(p Point) {
			if p.Hz >= 14_050_000 {
				cancel()
			}
		})
	if !errors.Is(err, context.Canceled) || len(pts) != 6 {
		t.Fatalf("err %v, %d points", err, len(pts))
	}
	if r.keyed || r.mode != "7" {
		t.Fatalf("not restored: %+v", r)
	}
}

func TestSweepPlanLimits(t *testing.T) {
	for _, c := range []SweepConfig{
		{FromHz: 14_000_000, ToHz: 14_350_000, StepHz: 10_000, PowerW: 50}, // too much power
		{FromHz: 14_000_000, ToHz: 14_350_000, StepHz: 500, PowerW: 5},     // step too small
		{FromHz: 13_990_000, ToHz: 14_100_000, StepHz: 10_000, PowerW: 5},  // starts out of band
		{FromHz: 14_300_000, ToHz: 14_400_000, StepHz: 10_000, PowerW: 5},  // ends out of band
		{FromHz: 28_000_000, ToHz: 29_700_000, StepHz: 10_000, PowerW: 5},  // 171 points
		{FromHz: 5_330_500, ToHz: 5_405_000, StepHz: 10_000, PowerW: 5},    // 60 m: not swept
		{FromHz: 14_100_000, ToHz: 14_000_000, StepHz: 10_000, PowerW: 5},  // backwards
	} {
		if _, err := c.Plan(); err == nil {
			t.Errorf("%+v: expected an error", c)
		}
	}
	p, err := SweepConfig{FromHz: 10_100_000, ToHz: 10_150_000, StepHz: 15_000, PowerW: 5}.Plan()
	if err != nil || len(p) != 5 || p[4] != 10_150_000 {
		t.Fatalf("plan %v %v", p, err)
	}
	for _, b := range Bands {
		if _, err := (SweepConfig{FromHz: b.FromHz, ToHz: b.ToHz, StepHz: b.StepHz, PowerW: 5}).Plan(); err != nil {
			t.Errorf("default %s sweep: %v", b.Name, err)
		}
	}
}
