package main

import (
	"math"
	"os"
	"reflect"
	"testing"
)

func TestPlaybackControls(t *testing.T) {
	s := `Simple mixer control 'Speaker',0
  Capabilities: pvolume pswitch pswitch-joined
  Playback channels: Front Left - Front Right
  Front Left: Playback 0 [0%] [-37.00dB] [off]
Simple mixer control 'Mic',0
  Capabilities: pvolume pvolume-joined cvolume cvolume-joined pswitch pswitch-joined cswitch cswitch-joined
Simple mixer control 'Auto Gain Control',0
  Capabilities: pswitch pswitch-joined
`
	if got := playbackControls(s); !reflect.DeepEqual(got, []string{"Speaker"}) {
		t.Fatal(got)
	}
	if cardOf("plughw:CARD=Device,DEV=0") != "Device" {
		t.Fatal("card")
	}
}

func TestMakePCM(t *testing.T) {
	w := make([]float32, 48000)
	for i := range w {
		w[i] = 0.5
	}
	p := makePCM(w, 1, 44100, 2)
	pre := int(ft8PreRoll.Seconds() * 44100)
	if len(p) != 2*2*(pre+44100) {
		t.Fatal(len(p))
	}
	l := int16(uint16(p[4*pre]) | uint16(p[4*pre+1])<<8)
	r := int16(uint16(p[4*pre+2]) | uint16(p[4*pre+3])<<8)
	if l != 16000 || r != 16000 || p[0] != 0 {
		t.Fatal(l, r)
	}
	if len(makePCM(w, 1, 48000, 1)) != 2*(int(ft8PreRoll.Seconds()*48000)+48000) {
		t.Fatal("mono")
	}
}

func TestCMediaAndLanczos(t *testing.T) {
	d := t.TempDir() + "/cards"
	os.WriteFile(d, []byte(" 0 [Device         ]: USB-Audio - USB Audio Device\n                      C-Media Electronics Inc. USB Audio Device at usb-xhci-hcd.1-2.2, full speed\n 1 [CODEC          ]: USB-Audio - USB Audio CODEC\n                      Burr-Brown from TI USB Audio CODEC at usb-xhci-hcd.0-1, full speed\n"), 0o644)
	old := cardsPath
	cardsPath = d
	defer func() { cardsPath = old }()
	if !isCMediaRig("plughw:CARD=Device,DEV=0") || isCMediaRig("plughw:CARD=CODEC,DEV=0") {
		t.Fatal("cmedia detect")
	}
	// a 1500 Hz tone resampled to 44.1 kHz stays a clean 1500 Hz tone
	w := make([]float32, 48000)
	for i := range w {
		w[i] = float32(math.Sin(2 * math.Pi * 1500 * float64(i) / 48000))
	}
	var e float64
	for i := 1000; i < 43000; i++ {
		want := math.Sin(2 * math.Pi * 1500 * float64(i) / 44100)
		d := float64(lanczosAt(w, float64(i)*48000/44100)) - want
		e += d * d
	}
	if rms := math.Sqrt(e / 42000); rms > 1e-3 {
		t.Fatalf("resampling error rms %g", rms)
	}
}
