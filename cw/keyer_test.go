package cw

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeLine struct {
	mu  sync.Mutex
	evs []keyEv
}

type keyEv struct {
	down bool
	at   time.Time
}

func (f *fakeLine) Key(down bool) error {
	f.mu.Lock()
	f.evs = append(f.evs, keyEv{down, time.Now()})
	f.mu.Unlock()
	return nil
}

func (f *fakeLine) events() []keyEv {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]keyEv(nil), f.evs...)
}

// feedLikeUI feeds text the way the window does: polling every 25 ms.
func feedLikeUI(k *Keyer, text string) {
	q := []rune(text)
	for len(q) > 0 {
		if k.Wants() && k.Feed(q[0]) {
			q = q[1:]
		}
		time.Sleep(25 * time.Millisecond)
	}
	for k.Busy() || !k.Wants() {
		time.Sleep(5 * time.Millisecond)
	}
}

// marksAndGaps turns events into alternating durations: mark, gap, mark, ...
// (redundant key-ups from idle are merged).
func marksAndGaps(evs []keyEv) (marks, gaps []time.Duration) {
	var last *keyEv
	for i := range evs {
		e := evs[i]
		if last != nil && last.down == e.down {
			continue
		}
		if last != nil {
			if last.down {
				marks = append(marks, e.at.Sub(last.at))
			} else {
				gaps = append(gaps, e.at.Sub(last.at))
			}
		}
		if last == nil && !e.down {
			continue // leading key-up
		}
		ev := e
		last = &ev
	}
	return
}

func TestKeyerTimingAt20And30WPM(t *testing.T) {
	// On CI this runs in its own step (CW_TIMING=1): next to the AI tests, which use every
	// core, a shared VM can't time anything.
	if os.Getenv("CI") != "" && os.Getenv("CW_TIMING") == "" {
		t.Skip("timed alone in CI (CW_TIMING=1)")
	}
	for _, wpm := range []int{20, 30} {
		line := &fakeLine{}
		k := NewKeyer(line, wpm, nil)
		feedLikeUI(k, "PARIS PARIS")
		k.Close()
		marks, gaps := marksAndGaps(line.events())
		dit := 1200.0 / float64(wpm) // ms
		// Expected for "PARIS PARIS": marks .--. .- .-. .. ... (x2); gaps 1/3/7 dits.
		want := strings.Repeat(".--. .- .-. .. ...", 1)
		var expMarks []float64
		var expGaps []float64
		words := []string{want, want}
		for wi, w := range words {
			if wi > 0 {
				expGaps = append(expGaps, 7)
			}
			for ci, code := range strings.Fields(w) {
				if ci > 0 {
					expGaps = append(expGaps, 3)
				}
				for ei, el := range code {
					if ei > 0 {
						expGaps = append(expGaps, 1)
					}
					if el == '-' {
						expMarks = append(expMarks, 3)
					} else {
						expMarks = append(expMarks, 1)
					}
				}
			}
		}
		if len(marks) != len(expMarks) || len(gaps) != len(expGaps) {
			t.Fatalf("%d wpm: got %d marks/%d gaps, want %d/%d", wpm, len(marks), len(gaps), len(expMarks), len(expGaps))
		}
		worst := 0.0
		where := ""
		for i := range marks {
			e := math.Abs(float64(marks[i].Microseconds())/1000 - expMarks[i]*dit)
			if e > worst {
				worst, where = e, fmt.Sprintf("mark %d (%.0f dits): %.1f ms", i, expMarks[i], float64(marks[i].Microseconds())/1000)
			}
		}
		for i := range gaps {
			e := math.Abs(float64(gaps[i].Microseconds())/1000 - expGaps[i]*dit)
			if e > worst {
				worst, where = e, fmt.Sprintf("gap %d (%.0f dits): %.1f ms", i, expGaps[i], float64(gaps[i].Microseconds())/1000)
			}
		}
		var errs []float64
		for i := range marks {
			errs = append(errs, math.Abs(float64(marks[i].Microseconds())/1000-expMarks[i]*dit))
		}
		for i := range gaps {
			errs = append(errs, math.Abs(float64(gaps[i].Microseconds())/1000-expGaps[i]*dit))
		}
		sort.Float64s(errs)
		p95 := errs[len(errs)*95/100]
		t.Logf("%d WPM (dit %.0f ms): 95%% of elements within %.2f ms; worst %.2f ms at %s", wpm, dit, p95, worst, where)
		// Judge typical timing: a shared test VM can deschedule the keyer once in a while.
		// Hosted CI runners (GitHub's Mac VMs especially) are noisier still: 6 ms there
		// still catches a real regression (a dit at 30 WPM is 40 ms).
		limit := 3.0
		if os.Getenv("CI") != "" {
			limit = 6
			if runtime.GOOS == "darwin" { // GitHub's virtual Macs have coarse timers (6.3 ms measured, alone)
				limit = 10
			}
		}
		if p95 > limit {
			t.Errorf("%d wpm: 95th-percentile timing error %.2f ms", wpm, p95)
		}
		if worst > 0.75*dit {
			t.Errorf("%d wpm: an element was off by %.1f ms (%s)", wpm, worst, where)
		}
	}
}

// The keyer's own output, rendered as a tone, must decode back to the text.
func TestKeyerOutputDecodes(t *testing.T) {
	line := &fakeLine{}
	k := NewKeyer(line, 22, nil)
	text := "CQ POTA DE KN0BLE K"
	feedLikeUI(k, text)
	k.Close()
	evs := line.events()
	const sr = 12000
	t0 := evs[0].at
	end := evs[len(evs)-1].at.Sub(t0)
	audio := make([]float32, int((end.Seconds()+1.5)*sr)+sr/5)
	down := false
	var from time.Time
	for _, e := range evs {
		if e.down && !down {
			from = e.at
		}
		if !e.down && down {
			a := int(from.Sub(t0).Seconds()*sr) + sr/5
			b := int(e.at.Sub(t0).Seconds()*sr) + sr/5
			ramp := sr * 4 / 1000 // the rig shapes each element over ~4 ms
			for i := a; i < b; i++ {
				env := 1.0
				if i-a < ramp {
					env = 0.5 - 0.5*math.Cos(math.Pi*float64(i-a)/float64(ramp))
				} else if b-1-i < ramp {
					env = 0.5 - 0.5*math.Cos(math.Pi*float64(b-1-i)/float64(ramp))
				}
				audio[i] = float32(0.5 * env * math.Sin(2*math.Pi*700*float64(i)/sr))
			}
		}
		down = e.down
	}
	d := NewStreamDecoder(sr, 700)
	d.Push(audio)
	if got := strings.TrimSpace(d.Transcript()); got != text {
		t.Fatalf("decoded %q, want %q", got, text)
	}
}

func TestKeyerAbortKeysUpFast(t *testing.T) {
	line := &fakeLine{}
	k := NewKeyer(line, 10, nil) // slow: a dah is 360 ms
	defer k.Close()
	for !k.Wants() {
		time.Sleep(time.Millisecond)
	}
	k.Feed('T')
	time.Sleep(200 * time.Millisecond) // mid-dah
	t0 := time.Now()
	k.Abort()
	time.Sleep(100 * time.Millisecond)
	evs := line.events()
	last := evs[len(evs)-1]
	if last.down {
		t.Fatal("key still down after abort")
	}
	lat := last.at.Sub(t0)
	t.Logf("abort to key-up: %v", lat)
	if lat > 10*time.Millisecond {
		t.Fatalf("key-up took %v after abort", lat)
	}
	time.Sleep(400 * time.Millisecond) // the aborted dah must not resume
	if n := len(line.events()); n != len(evs) {
		t.Fatalf("keying continued after abort")
	}
}

func TestKeyerCloseLeavesKeyUp(t *testing.T) {
	line := &fakeLine{}
	k := NewKeyer(line, 10, nil)
	k.Feed('T')
	time.Sleep(100 * time.Millisecond)
	k.Close()
	evs := line.events()
	if evs[len(evs)-1].down {
		t.Fatal("key left down on close")
	}
}

func TestExpandMacros(t *testing.T) {
	c := MacroContext{MyCall: "KN0BLE", MyName: "JOSH", MyState: "FL", HisCall: "K2DEF", RST: "599",
		Extra: map[string]string{"PARK": "US-1234"}}
	cases := map[string]string{
		"CQ CQ DE {MYCALL} {MYCALL} K":    "CQ CQ DE KN0BLE KN0BLE K",
		"{CALL} {RST} {RST} {MYSTATE} BK": "K2DEF 5NN 5NN FL BK",
		"! TU 73 E E":                     "K2DEF TU 73 E E",
		"OP {NAME} {MYGRID} QTH":          "OP JOSH QTH", // empty token collapses
		"POTA {PARK}":                     "POTA US-1234",
		"{TYPO} X":                        "{TYPO} X", // unknown tokens stay visible
	}
	for in, want := range cases {
		if got := Expand(in, c); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if CutNumbers("579") != "57N" || CutNumbers("100") != "1TT" {
		t.Error("cut numbers")
	}
}

func TestParseMacrosDefaults(t *testing.T) {
	ctx, ms := ParseMacros(DefaultMacrosText)
	if ctx.MyCall != "KN0BLE" || ms[1].Label != "CQ POTA" || ms[1].Text != "CQ POTA DE {MYCALL} {MYCALL} K" {
		t.Fatalf("parse: %+v %+v", ctx, ms[1])
	}
	ctx.HisCall = "W1AW"
	if got := Expand(ms[2].Text, ctx); got != "W1AW 5NN 5NN FL FL BK" {
		t.Fatalf("exch %q", got)
	}
}
