package main

import (
	"os"
	"path/filepath"
	"testing"
)

// a sysfs-like tree with an FX-4CR (hub 1-1: CH340, C-Media, Bluetooth) and an FT-710
// (one USB device 1-2 with its serial port and sound chip) plugged into the Pi
func fakeUSBTree(t *testing.T) (fxPort, ftPort string, devs []audioDev) {
	root := t.TempDir()
	mk := func(p string) string {
		p = filepath.Join(root, p)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	link := func(target, name string) {
		os.MkdirAll(filepath.Dir(name), 0o755)
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	bus := "devices/platform/axi/1000120000.pcie/1f00200000.usb/xhci-hcd.0/usb1"
	ttyFX := mk(bus + "/1-1/1-1.1/1-1.1:1.0/ttyUSB0/tty/ttyUSB0")
	cmedia := mk(bus + "/1-1/1-1.2/1-1.2:1.0/sound/card1")
	bt := mk(bus + "/1-1/1-1.3/1-1.3:1.0/sound/card2")
	ttyFT := mk(bus + "/1-2/1-2:1.1/ttyUSB1/tty/ttyUSB1")
	codec := mk(bus + "/1-2/1-2:1.0/sound/card0")
	os.WriteFile(filepath.Join(root, bus, "1-1/1-1.2/idVendor"), []byte("0d8c\n"), 0o644)
	os.WriteFile(filepath.Join(root, bus, "1-1/1-1.3/idVendor"), []byte("0a12\n"), 0o644)
	link(ttyFX, filepath.Join(root, "class/tty/ttyUSB0"))
	link(ttyFT, filepath.Join(root, "class/tty/ttyUSB1"))
	link(codec, filepath.Join(root, "class/sound/card0"))
	link(cmedia, filepath.Join(root, "class/sound/card1"))
	link(bt, filepath.Join(root, "class/sound/card2"))
	link("card0", filepath.Join(root, "asound/CODEC"))
	link("card1", filepath.Join(root, "asound/Device"))
	link("card2", filepath.Join(root, "asound/QCC3024"))
	os.WriteFile(filepath.Join(root, "ttyUSB0"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "ttyUSB1"), nil, 0o644)
	fxPort = filepath.Join(root, "by-id/usb-1a86_USB_Serial-if00-port0")
	ftPort = filepath.Join(root, "by-id/usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller-if01-port0")
	link("../ttyUSB0", fxPort)
	link("../ttyUSB1", ftPort)
	oldP, oldS := procAsound, sysClass
	procAsound, sysClass = filepath.Join(root, "asound"), filepath.Join(root, "class")
	t.Cleanup(func() { procAsound, sysClass = oldP, oldS })
	devs = []audioDev{ // the Bluetooth module first, to be sure it isn't picked for that
		{ID: "plughw:CARD=QCC3024,DEV=0", Label: "QCC3024"},
		{ID: "plughw:CARD=CODEC,DEV=0", Label: "USB Audio CODEC"},
		{ID: "plughw:CARD=Device,DEV=0", Label: "USB PnP Sound Device"},
	}
	return
}

func TestRigSoundCard(t *testing.T) {
	fx, ft, devs := fakeUSBTree(t)
	if got := rigSoundCard(fx, devs); got != "plughw:CARD=Device,DEV=0" {
		t.Fatalf("FX-4CR: %q", got)
	}
	if got := rigSoundCard(ft, devs); got != "plughw:CARD=CODEC,DEV=0" {
		t.Fatalf("FT-710: %q", got)
	}
	// only the Bluetooth card left on the FX's hub: that's still the FX's
	if got := rigSoundCard(fx, devs[:2]); got != "plughw:CARD=QCC3024,DEV=0" {
		t.Fatalf("FX without C-Media: %q", got)
	}
	// a port that isn't there, or no port: nothing to tell; Auto falls back to the first card
	if got := rigSoundCard(fx+"-gone", devs); got != "" {
		t.Fatalf("missing port: %q", got)
	}
	if got := autoAudioDev("", devs); got != devs[0].ID {
		t.Fatalf("no port: %q", got)
	}
	// the FT-710 is on the Pi's own bus (no hub inside): the FX's cards aren't its siblings
	if got := rigSoundCard(ft, devs[:1]); got != "" {
		t.Fatalf("FT-710 matched %q", got)
	}
}
