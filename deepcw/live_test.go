package deepcw

import (
	"strings"
	"testing"
	"time"

	"cwdecoder/cw"
)

// Feed a recording in real-time-sized chunks (fast-forwarded) through Live and check the
// stitched transcript and the line break between two overs.
func TestLiveStitchesAndBreaksLines(t *testing.T) {
	m := loadT(t)
	l := NewLive(m)
	defer l.Close()
	l.SetPlan(Plan{Threads: 2, Cadence: 0, Throttle: 0})
	l.SetFocus(DefaultFocus(650))
	l.SetEnabled(true)
	const rate = 12000
	over1 := cw.MorseSamples("CQ CQ DE KN0BLE K", 20, 650, rate)
	over2 := cw.MorseSamples("KN0BLE DE W1AW 599 BK", 22, 650, rate)
	a := make([]float32, 15*rate) // window fills with silence first
	a = append(a, over1...)
	a = append(a, make([]float32, 4*rate)...)
	a = append(a, over2...)
	a = append(a, make([]float32, 18*rate)...)
	for i := range a {
		a[i] *= 0.4
	}
	var got strings.Builder
	chunk := rate / 2
	for i := 0; i < len(a); i += chunk {
		l.Push(a[i:min(i+chunk, len(a))], rate)
		// wait for the worker to process this half-second step
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			if st, _ := l.Status(); st == "decoding" || i < 15*rate {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		got.WriteString(l.TakeText())
	}
	time.Sleep(time.Second)
	got.WriteString(l.TakeText())
	lines := strings.Split(strings.TrimSpace(got.String()), "\n")
	for i := range lines {
		lines[i] = strings.Join(strings.Fields(lines[i]), " ")
	}
	t.Logf("live transcript: %q", lines)
	if len(lines) != 2 || lines[0] != "CQ CQ DE KN0BLE K" || lines[1] != "KN0BLE DE W1AW 599 BK" {
		t.Fatalf("unexpected transcript %q", lines)
	}
}
