package main

// The Mac's sound output: cw-play, a small native player inside CW Console.app
// (packaging/mac/play) that takes raw 16-bit PCM on stdin and plays it to one output
// device, as aplay does on the Pi box. With it the Mac can key a CW tone into the
// radio's USB sound card (Audio tone keying: in DATA-FM that is MCW, which FM radios
// hear) and play FT8 to the radio's sound card rather than the default output.

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// macPlayerPath: cw-play next to the engine in the .app (CWCONSOLE_PLAY overrides, for
// tests). "" when there is none: not a Mac, or an engine built without Swift.
var macPlayerPath = func() string {
	if p := os.Getenv("CWCONSOLE_PLAY"); p != "" {
		return p
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "cw-play")
}()

func macPlayer() string {
	if macPlayerPath == "" || appliance {
		return ""
	}
	if st, err := os.Stat(macPlayerPath); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return macPlayerPath
}

var macOuts struct {
	sync.Mutex
	at   time.Time
	devs []outputDev
}

// macOutputs: the Mac's output devices, from `cw-play --list` (uid TAB name per line).
func macOutputs() []outputDev {
	p := macPlayer()
	if p == "" {
		return nil
	}
	macOuts.Lock()
	defer macOuts.Unlock()
	if time.Since(macOuts.at) < 5*time.Second {
		return macOuts.devs
	}
	b, err := exec.Command(p, "--list").Output()
	if err != nil {
		return macOuts.devs
	}
	var devs []outputDev
	for _, l := range strings.Split(string(b), "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if ok && id != "" {
			devs = append(devs, outputDev{ID: id, Name: name})
		}
	}
	macOuts.at, macOuts.devs = time.Now(), devs
	return devs
}

// macPlayArgs: cw-play's arguments, aplay's way of saying them ("" dev = the default output).
func macPlayArgs(dev string, rate, ch int) []string {
	args := []string{"-q"}
	if dev != "" {
		args = append(args, "-D", dev)
	}
	return append(args, "-t", "raw", "-f", "S16_LE", "-r", itoa(rate), "-c", itoa(ch))
}

// waitReady: cw-play says "ready" once the sound card is running. Until then nothing is
// heard, so the keyer's clock (and the rig's PTT) wait for it.
func waitReady(out io.Reader, d time.Duration) error {
	got := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "ready" {
				got <- nil
				io.Copy(io.Discard, out)
				return
			}
		}
		got <- errors.New("the sound output didn't start")
	}()
	select {
	case err := <-got:
		return err
	case <-time.After(d):
		return errors.New("the sound output took too long to start")
	}
}
