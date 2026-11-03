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

	"cwdecoder/cw"
	"cwdecoder/deepcw"
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
		var focus *deepcw.Focus
		if *focusW > 0 {
			p := *pitch
			if p == 0 {
				head := s
				if n := int(rate) * 60; len(head) > n {
					head = head[:n]
				}
				f, _ := cw.FindPitch(head, float32(rate), 300, 1200, 5)
				p = float64(f)
			}
			focus = &deepcw.Focus{Pitch: p, Width: *focusW, Taper: deepcw.FocusTaper, Floor: *focusFloor}
		}
		text, err := deepcw.DecodeStream(m, s, int(rate), 2, focus)
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
	sr := float32(rate)
	p := float32(*pitch)
	if p == 0 {
		head := s
		if n := int(sr * 60); len(head) > n {
			head = head[:n]
		}
		p, _ = cw.FindPitch(head, sr, 300, 1200, 5)
	}
	d := cw.NewStreamDecoder(sr, p)
	d.SetSensitivity(float32(*sens))
	d.SetImproved(!*nexus)
	var out strings.Builder
	for i := 0; i < len(s); i += int(sr) {
		e := i + int(sr)
		if e > len(s) {
			e = len(s)
		}
		d.Push(s[i:e])
		out.WriteString(d.TakeText())
	}
	d.Push(make([]float32, int(sr*1.5)))
	out.WriteString(d.TakeText())
	if !*quiet {
		fmt.Fprintf(os.Stderr, "pitch %d Hz, ~%d WPM\n", int(p), d.WPM())
	}
	fmt.Println(strings.TrimSpace(out.String()))
}
