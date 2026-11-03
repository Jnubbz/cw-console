package main

// FT8 benchmark: WSJT-X's own FT8 sample recording through the decoder at each depth,
// on one core and on all of them, so builds and boxes can be compared on the same audio.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ft8SamplePath = envOr("CWBOX_FT8SAMPLE", "/usr/share/cw-decoder/ft8-sample.wav")

type ft8BenchRow struct {
	Depth   string  `json:"depth"`
	Threads int     `json:"threads"`
	Secs    float64 `json:"secs"`
	N       int     `json:"n"`
	Err     string  `json:"err,omitempty"`
}

type ft8BenchState struct {
	Running bool          `json:"running"`
	Rows    []ft8BenchRow `json:"rows"`
	Done    string        `json:"done"` // when it finished
	Note    string        `json:"note"`
}

// benchStart runs the benchmark in the background; benchState reports it.
func (f *ft8Rx) benchStart(txOn bool) error {
	if txOn {
		return errors.New("untick Transmit first (the benchmark pauses decoding)")
	}
	if !ft8Available() {
		return errors.New("no FT8 decoder on this box")
	}
	if _, err := os.Stat(ft8SamplePath); err != nil {
		return errors.New("the benchmark recording isn't on this box (update it)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bench.Running {
		return errors.New("the benchmark is already running")
	}
	f.bench = ft8BenchState{Running: true}
	f.benching = true
	go f.benchRun()
	return nil
}

func (f *ft8Rx) benchRun() {
	defer func() {
		f.mu.Lock()
		f.bench.Running, f.benching = false, false
		f.bench.Done = time.Now().UTC().Format("15:04:05 UTC")
		f.mu.Unlock()
	}()
	time.Sleep(300 * time.Millisecond) // let a live decode in progress get out of the way
	f.mu.Lock()
	for f.busy || f.quickBusy {
		f.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		f.mu.Lock()
	}
	f.mu.Unlock()
	all := min(runtime.NumCPU(), 12)
	threads := []int{1}
	if all > 1 {
		threads = append(threads, all)
	}
	for _, th := range threads {
		for d := 1; d <= 3; d++ {
			r := ft8BenchRow{Depth: depthNames[d], Threads: th}
			var err error
			r.Secs, r.N, err = benchOnce(d, th)
			if err != nil {
				r.Err = err.Error()
			}
			f.mu.Lock()
			f.bench.Rows = append(f.bench.Rows, r)
			f.mu.Unlock()
		}
	}
	f.mu.Lock()
	f.bench.Note = fmt.Sprintf("WSJT-X sample 210703_133430 (21 stations), %d cores, live decoder uses %d", runtime.NumCPU(), ft8Threads)
	f.mu.Unlock()
}

func benchOnce(depth, threads int) (float64, int, error) {
	dir, err := os.MkdirTemp("", "ft8bench")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(dir)
	wav := filepath.Join(dir, "210703_133430.wav")
	b, err := os.ReadFile(ft8SamplePath)
	if err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(wav, b, 0o644); err != nil {
		return 0, 0, err
	}
	cmd := exec.Command(jt9Path, append(jt9Args(dir, depth, threads, "", ""), wav)...)
	cmd.Dir = dir
	t0 := time.Now()
	out, err := cmd.Output()
	secs := time.Since(t0).Seconds()
	if err != nil {
		return secs, 0, err
	}
	return secs, len(parseJT9(string(out), "133430")), nil
}

func (f *ft8Rx) benchState() ft8BenchState {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bench
	b.Rows = append([]ft8BenchRow(nil), b.Rows...)
	return b
}

// benchText: the results as a small table.
func (b ft8BenchState) text() string {
	var s strings.Builder
	fmt.Fprintf(&s, "%-7s %-8s %7s %8s\n", "Depth", "Cores", "Time", "Decodes")
	for _, r := range b.Rows {
		if r.Err != "" {
			fmt.Fprintf(&s, "%-7s %-8d  error: %s\n", r.Depth, r.Threads, r.Err)
			continue
		}
		fmt.Fprintf(&s, "%-7s %-8d %6.1fs %8d\n", r.Depth, r.Threads, r.Secs, r.N)
	}
	return s.String()
}
