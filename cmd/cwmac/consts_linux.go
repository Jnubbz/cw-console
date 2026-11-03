//go:build linux

package main

const (
	tiocmBIS = 0x5416
	tiocmBIC = 0x5417
	tiocmDTR = 0x2
	tiocmRTS = 0x4

	browserOpener = "xdg-open"
)

var portGlobs = []string{"/dev/ttyUSB*", "/dev/ttyACM*"}

func onBattery() bool { return false }
