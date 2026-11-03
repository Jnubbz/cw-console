package main

// Which sound card is the radio's own: the one on the same USB device as its key port, or
// on the same USB hub inside the radio (the FX-4CR has one: serial chip, sound chip and a
// Bluetooth module behind it). With the audio input on Auto, each radio profile listens
// on its own radio, even with two radios plugged in.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	procAsound = "/proc/asound" // test hooks
	sysClass   = "/sys/class"
)

var usbDevName = regexp.MustCompile(`^\d+-\d+(\.\d+)*$`) // 1-1, 1-1.2 (not usb1, not 1-1.2:1.0)

// usbDevOf: the sysfs path of the USB device a class entry (a tty, a sound card) is part
// of ("" if it isn't on USB).
func usbDevOf(classEntry string) string {
	p, err := filepath.EvalSymlinks(classEntry)
	if err != nil {
		return ""
	}
	for d := p; d != "/" && d != "."; d = filepath.Dir(d) {
		if usbDevName.MatchString(filepath.Base(d)) {
			return d
		}
	}
	return ""
}

// cardUSBDev: the USB device of an ALSA device id like "plughw:CARD=Device,DEV=0".
func cardUSBDev(id string) string {
	card := cardOf(id)
	if card == "" {
		return ""
	}
	n, err := os.Readlink(filepath.Join(procAsound, card)) // /proc/asound/Device -> card1
	if err != nil {
		n = card // already "card1"?
	}
	return usbDevOf(filepath.Join(sysClass, "sound", filepath.Base(n)))
}

// rigSoundCard: of devs, the sound card that belongs to the radio on keyPort ("" if none
// can be told): one on the same USB device first, then one on the same hub (a C-Media
// chip first: both radios' sound chips are C-Media, the FX-4CR's other card is Bluetooth).
func rigSoundCard(keyPort string, devs []audioDev) string {
	if keyPort == "" {
		return ""
	}
	tty, err := filepath.EvalSymlinks(keyPort)
	if err != nil {
		return ""
	}
	pd := usbDevOf(filepath.Join(sysClass, "tty", filepath.Base(tty)))
	if pd == "" {
		return ""
	}
	hub := filepath.Dir(pd)
	onHub := usbDevName.MatchString(filepath.Base(hub)) // a real hub, not the Pi's own USB bus
	var sibling []string
	for _, d := range devs {
		cd := cardUSBDev(d.ID)
		switch {
		case cd == "":
		case cd == pd:
			return d.ID
		case onHub && filepath.Dir(cd) == hub:
			sibling = append(sibling, d.ID)
		}
	}
	for _, id := range sibling {
		if v, _ := os.ReadFile(filepath.Join(cardUSBDev(id), "idVendor")); strings.TrimSpace(string(v)) == "0d8c" {
			return id
		}
	}
	if len(sibling) > 0 {
		return sibling[0]
	}
	return ""
}

// autoAudioDev: the sound card to listen on when none is picked (or the picked one isn't
// plugged in): the radio's own, else the first USB one.
func autoAudioDev(keyPort string, devs []audioDev) string {
	if id := rigSoundCard(keyPort, devs); id != "" {
		return id
	}
	if len(devs) > 0 {
		return devs[0].ID
	}
	return ""
}
