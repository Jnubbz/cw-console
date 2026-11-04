package main

import (
	"reflect"
	"testing"
)

// Josh's Mac: the FT-710 under both drivers, plus Bluetooth headphones.
var joshsPorts = []portInfo{
	{"/dev/cu.BoseQC45", "cu.BoseQC45"},
	{"/dev/cu.BoseRevolveIISoundLink", "cu.BoseRevolveIISoundLink"},
	{"/dev/cu.SLAB_USBtoUART", "cu.SLAB_USBtoUART"},
	{"/dev/cu.SLAB_USBtoUART8", "cu.SLAB_USBtoUART8"},
	{"/dev/cu.usbserial-01AE6E900", "cu.usbserial-01AE6E900"},
	{"/dev/cu.usbserial-01AE6E901", "cu.usbserial-01AE6E901"},
}

func TestCP2105TwinsOnAMac(t *testing.T) {
	if got := pairedCATPort("/dev/cu.usbserial-01AE6E901", joshsPorts); got != "/dev/cu.usbserial-01AE6E900" {
		t.Fatalf("paired CAT port %q", got)
	}
	if got := pairedCATPort("/dev/cu.SLAB_USBtoUART8", joshsPorts); got != "" {
		t.Fatalf("a SLAB key port can't be paired by name: %q", got)
	}
	// Whatever the key port, only interface 0 is ever probed: never a Standard port and
	// never a SLAB duplicate (one of them is the key port's twin), nor Bluetooth.
	for _, key := range []string{"/dev/cu.SLAB_USBtoUART8", "/dev/cu.usbserial-01AE6E901", "/dev/cu.SLAB_USBtoUART", ""} {
		if got := catCandidates(key, joshsPorts); !reflect.DeepEqual(got, []string{"/dev/cu.usbserial-01AE6E900"}) {
			t.Fatalf("key %q: candidates %v", key, got)
		}
	}
	if got := catCandidates("/dev/cu.usbserial-01AE6E900", joshsPorts); len(got) != 0 {
		t.Fatalf("key on interface 0: candidates %v", got)
	}
	for path, want := range map[string]string{
		"/dev/cu.usbserial-01AE6E900": "FT-710 Enhanced port: CAT",
		"/dev/cu.usbserial-01AE6E901": "FT-710 Standard port: key",
		"/dev/cu.SLAB_USBtoUART8":     "duplicate of a usbserial port: use those",
		"/dev/cu.BoseQC45":            "",
	} {
		if got := portRole(path, joshsPorts); got != want {
			t.Errorf("%s: %q", path, got)
		}
	}
}

func TestNoApplePairFallsBackToProbing(t *testing.T) {
	ports := []portInfo{{"/dev/cu.SLAB_USBtoUART", ""}, {"/dev/cu.SLAB_USBtoUART2", ""}, {"/dev/cu.BoseQC45", ""}}
	if got := catCandidates("/dev/cu.SLAB_USBtoUART2", ports); !reflect.DeepEqual(got, []string{"/dev/cu.SLAB_USBtoUART"}) {
		t.Fatalf("%v", got)
	}
	// a lone usbserial port (say the FX-4CR's CH340) isn't a pair
	if len(cp2105Pairs([]portInfo{{"/dev/cu.usbserial-1420", ""}})) != 0 {
		t.Fatal("not a pair")
	}
}

func TestCATNeverOnAStandardPort(t *testing.T) {
	old := portList
	portList = func() []portInfo { return joshsPorts }
	defer func() { portList = old }()
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	e.st.TxPort, e.st.CATPort = "/dev/cu.SLAB_USBtoUART8", "/dev/cu.usbserial-01AE6E901" // CAT set to a Standard port
	if s, err := e.ft710Spec(); err == nil && s.Port == "/dev/cu.usbserial-01AE6E901" {
		t.Fatal("CAT on a Standard (keying) port")
	}
	e.st.TxPort, e.st.CATPort = "/dev/cu.usbserial-01AE6E901", ""
	if s, err := e.ft710Spec(); err != nil || s.Port != "/dev/cu.usbserial-01AE6E900" {
		t.Fatalf("auto pairing: %+v %v", s, err)
	}
}
