package deepcw

import (
	"fmt"
	"math/rand"
	"strings"

	"cwdecoder/cw"
)

// BenchReport times the decoder on this computer and describes what every speed
// setting will cost. Shared by the window's Benchmark button and the CLI.
func BenchReport(m *Model, cores int, onBattery bool) (string, error) {
	w := cw.MorseSamples("CQ CQ DE KN0BLE KN0BLE K", 20, 650, 3200)
	w = append(make([]float32, 3200), w...)
	if n := 15*3200 - len(w); n > 0 {
		w = append(w, make([]float32, n)...)
	}
	w = w[:15*3200]
	r := rand.New(rand.NewSource(1))
	for i := range w {
		w[i] = 0.4*w[i] + 0.05*float32(r.NormFloat64())
	}
	res, err := Benchmark(m, cores, w)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	power := "plugged in"
	if onBattery {
		power = "battery"
	}
	fmt.Fprintf(&b, "Math: %s.  CPU threads: %d.  Power: %s.\n\n", SIMDName(), cores, power)
	for _, x := range res {
		fmt.Fprintf(&b, "One 15-second decode on %d thread(s): %.2f s\n", x.Threads, x.Pass.Seconds())
	}
	b.WriteString("\nWhat each AI speed setting costs on this computer:\n\n")
	for s := SpeedEco; s <= SpeedMax; s++ {
		p := PlanFor(s, cores, onBattery)
		pass := res[0].Pass
		cpuSecs := pass.Seconds()
		upTo := ""
		if p.Threads > 1 && len(res) > 1 {
			pass = res[1].Pass
			cpuSecs = pass.Seconds() * float64(p.Threads) // upper bound: every thread busy
			upTo = "up to "
		}
		iv := p.Interval(pass)
		load := 100 * cpuSecs / iv.Seconds() / float64(cores)
		// A character waits for the tail guard, then for the next pass to start and finish.
		lag := TailGuardSecs + iv.Seconds()/2 + pass.Seconds()
		fmt.Fprintf(&b, "%s: new text every %.1f s, about %.1f s behind, CPU %s%.0f%%\n",
			SpeedNames[s], iv.Seconds(), lag, upTo, load)
	}
	b.WriteString("\nThe classic decoder uses well under 1% of the CPU.")
	return b.String(), nil
}
