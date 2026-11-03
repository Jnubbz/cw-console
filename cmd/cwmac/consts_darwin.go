//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

const (
	tiocmBIS = 0x8004746c // _IOW('t', 108, int)
	tiocmBIC = 0x8004746b // _IOW('t', 107, int)
	tiocmDTR = 0x2
	tiocmRTS = 0x4

	browserOpener = "open"
)

var portGlobs = []string{"/dev/cu.*"}

// onBattery asks pmset (a Mac mini is always on AC).
func onBattery() bool {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	return err == nil && strings.Contains(string(out), "'Battery Power'")
}
