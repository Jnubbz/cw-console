// cwdecode-file: decode a WAV file from the command line (same decoder as the GUI).
//
//	cwdecode-file [-pitch 700] [-sens 0.5] file.wav
//
// With no -pitch, the strongest tone between 300 and 1200 Hz is used.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/deepcw"
)

func main() {
	pitch := flag.Float64("pitch", 0, "CW pitch in Hz (0 = auto)")
	sens := flag.Float64("sens", 0.5, "sensitivity 0..1")
	quiet := flag.Bool("q", false, "print only the decoded text")
	nexus := flag.Bool("nexus", false, "exact Nexus behaviour (turn off the speed/fade fixes)")
	ai := flag.Bool("ai", false, "use the DeepCW neural decoder")
	bench := flag.Bool("bench", false, "time the AI decoder on this computer and exit")
	modelDir := flag.String("model", "", "folder with model.onnx and model.onnx.json (default: deepcw next to this program)")
	threads := flag.Int("threads", 0, "threads for the AI decoder (0 = all cores)")
	focusW := flag.Float64("focus", deepcw.FocusWidth, "AI: listen only within this many Hz around the pitch (0 = whole 400-1200 Hz band)")
	guard := flag.Float64("guard", deepcw.TailGuardSecs, "AI: tail guard seconds")
	focusFloor := flag.Float64("focusfloor", deepcw.FocusFloor, "AI: level kept outside the focus band (0..1)")
	flag.Parse()
	if *bench {
		dir := *modelDir
		if dir == "" {
			exe, _ := os.Executable()
			dir = filepath.Join(filepath.Dir(exe), "deepcw")
		}
		m, err := deepcw.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		rep, err := deepcw.BenchReport(m, runtime.NumCPU(), false)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(rep)
		return
	}
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cwdecode-file [-pitch Hz] [-sens 0..1] file.wav")
		os.Exit(2)
	}
	s, rate, err := cw.ReadWAV(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *ai {
		dir := *modelDir
		if dir == "" {
			exe, _ := os.Executable()
			dir = filepath.Join(filepath.Dir(exe), "deepcw")
		}
		m, err := deepcw.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		n := *threads
		if n <= 0 {
			n = runtime.NumCPU()
		}
		deepcw.SetThreads(n)
		t0 := time.Now()
		deepcw.TailGuardSecs = *guard
		text, err := decodeAI(m, s, rate, float32(*pitch), *focusW, *focusFloor)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "AI decoder (%s, %d threads): %.1f s of audio in %.1f s\n",
				deepcw.SIMDName(), n, float64(len(s))/float64(rate), time.Since(t0).Seconds())
		}
		fmt.Println(text)
		return
	}
	text, p, wpm := decodeClassic(s, rate, float32(*pitch), float32(*sens), !*nexus)
	if !*quiet {
		fmt.Fprintf(os.Stderr, "pitch %d Hz, ~%d WPM\n", int(p), wpm)
	}
	fmt.Println(text)
}

// findPitch picks the strongest tone between 300 and 1200 Hz in the first minute.
func findPitch(s []float32, rate uint32) float32 {
	head := s
	if n := int(rate) * 60; len(head) > n {
		head = head[:n]
	}
	p, _ := cw.FindPitch(head, float32(rate), 300, 1200, 5)
	return p
}

// decodeAI runs the DeepCW decoder over a whole recording. pitch 0 = auto; focusW 0 = whole band.
func decodeAI(m *deepcw.Model, s []float32, rate uint32, pitch float32, focusW, focusFloor float64) (string, error) {
	var focus *deepcw.Focus
	if focusW > 0 {
		if pitch == 0 {
			pitch = findPitch(s, rate)
		}
		focus = &deepcw.Focus{Pitch: float64(pitch), Width: focusW, Taper: deepcw.FocusTaper, Floor: focusFloor}
	}
	return deepcw.DecodeStream(m, s, int(rate), 2, focus)
}

// decodeClassic runs the classic (Nexus) decoder the way the apps do: one second at a time,
// then 1.5 s of silence to flush the last word. Returns the text, the pitch used and the WPM.
func decodeClassic(s []float32, rate uint32, pitch, sens float32, improved bool) (string, float32, int) {
	sr := float32(rate)
	if pitch == 0 {
		pitch = findPitch(s, rate)
	}
	d := cw.NewStreamDecoder(sr, pitch)
	d.SetSensitivity(sens)
	d.SetImproved(improved)
	var out strings.Builder
	for i := 0; i < len(s); i += int(sr) {
		d.Push(s[i:min(i+int(sr), len(s))])
		out.WriteString(d.TakeText())
	}
	d.Push(make([]float32, int(sr*1.5)))
	out.WriteString(d.TakeText())
	return strings.TrimSpace(out.String()), pitch, int(d.WPM())
}
