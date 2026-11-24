//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCWPlay puts a script in place of the Mac's cw-play: --list prints two devices;
// playing records its arguments and what it's given, after saying "ready".
func fakeCWPlay(t *testing.T) (args, pcm string) {
	dir := t.TempDir()
	args, pcm = filepath.Join(dir, "args"), filepath.Join(dir, "pcm.raw")
	p := filepath.Join(dir, "cw-play")
	script := `#!/bin/sh
if [ "$1" = "--list" ]; then
  printf 'BuiltInSpeakerDevice\tMacBook Pro Speakers\nAppleUSBAudioEngine:BurrBrown:USB AUDIO  CODEC\tUSB AUDIO  CODEC\n'
  exit 0
fi
echo "$@" > "` + args + `"
echo ready
exec cat > "` + pcm + `"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := macPlayerPath
	macPlayerPath = p
	macOuts.Lock()
	macOuts.at = time.Time{}
	macOuts.Unlock()
	t.Cleanup(func() { macPlayerPath = old })
	return args, pcm
}

func TestMacOutputs(t *testing.T) {
	fakeCWPlay(t)
	devs := outputDevices()
	if len(devs) != 2 || devs[1].Name != "USB AUDIO  CODEC" || !strings.HasPrefix(devs[1].ID, "AppleUSBAudioEngine:") {
		t.Fatalf("%+v", devs)
	}
}

// The Mac keys a CW tone through cw-play, to the playback device picked in Setup, with
// no receive pause (the page records, not the engine).
func TestAudioKeyerOnTheMac(t *testing.T) {
	args, out := fakeCWPlay(t)
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "AppleUSBAudioEngine:BurrBrown:USB AUDIO  CODEC", "", nil, 700, 0.5, 16)
	k.mu.Lock()
	k.player = macPlayer()
	k.mu.Unlock()
	k.Feed('e')
	select {
	case <-k.Sent():
	case <-time.After(3 * time.Second):
		t.Fatal("no echo")
	}
	waitFor(t, 5*time.Second, "the over to end", func() bool { return !k.Active(0) })
	k.Close()
	if e := ptt.events(); e != "on,off,close" {
		t.Fatalf("PTT %s", e)
	}
	b, _ := os.ReadFile(args)
	if a := strings.TrimSpace(string(b)); a != "-q -D AppleUSBAudioEngine:BurrBrown:USB AUDIO  CODEC -t raw -f S16_LE -r 48000 -c 1" {
		t.Fatalf("args %q", a)
	}
	if segs := tones(readPCM(t, out, 1), 48000); len(segs) != 1 {
		t.Fatalf("%d elements, want 1", len(segs))
	}
}

// Without its "ready" the keyer doesn't key the rig.
func TestAudioKeyerMacPlayerNeverReady(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cw-play")
	os.WriteFile(p, []byte("#!/bin/sh\necho 'cw-play: no output device \"x\"' >&2\nexit 1\n"), 0o755)
	old := macPlayerPath
	macPlayerPath = p
	defer func() { macPlayerPath = old }()
	ptt := &fakePTT{}
	k := newAudioKeyer(ptt, "x", "", nil, 700, 0.5, 16)
	k.mu.Lock()
	k.player = macPlayer()
	k.mu.Unlock()
	k.Feed('e')
	select {
	case err := <-k.Errors():
		if !strings.Contains(err.Error(), "no output device") {
			t.Fatalf("error %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no error")
	}
	k.Close()
	if e := ptt.events(); e != "close" {
		t.Fatalf("PTT %s", e)
	}
}

// The engine side: Audio tone keying on the Mac needs the playback device, and the state
// says the Mac can send audio CW.
func TestMacAudioToneNeedsPlaybackDevice(t *testing.T) {
	fakeCWPlay(t)
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	e.applySettings(json.RawMessage(`{"txLine":"audio","ft8PttLine":"rts"}`))
	e.mu.Lock()
	_, err := e.tx.newAudioKeyerLocked("/dev/null")
	st := e.stateLocked()
	e.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "Playback device") {
		t.Fatalf("no playback device: %v", err)
	}
	if !st.State.AudioCW {
		t.Fatal("state: audioCW false with cw-play there")
	}
}
