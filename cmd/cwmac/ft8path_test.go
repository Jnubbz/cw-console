package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJT9SearchFindsARegularFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "jt9")
	if err := os.WriteFile(good, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope")
	if got := firstRegular([]string{missing, good, good}); got != good {
		t.Fatalf("got %q", got)
	}
	if firstRegular([]string{missing}) != "" {
		t.Fatal("missing file was accepted")
	}
}

func TestFT8ModeRequiresTheDecoder(t *testing.T) {
	old := jt9Path
	t.Cleanup(func() { jt9Path = old })
	jt9Path = filepath.Join(t.TempDir(), "missing-jt9")
	if clampMode("ft8") != "cw" {
		t.Fatal("FT8 mode was allowed with no jt9")
	}
	if hint := ft8InstallHint(); !strings.Contains(hint, "WSJT-X") {
		t.Fatalf("hint %q", hint)
	}
	p := filepath.Join(t.TempDir(), "jt9")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	jt9Path = p
	if clampMode("ft8") != "ft8" {
		t.Fatal("a real jt9 file did not enable FT8")
	}
	if clampMode("sstv") != "sstv" || clampMode("cw") != "cw" || clampMode("") != "cw" {
		t.Fatal("other modes moved")
	}
}

func TestAplayIsOnlyForAnALSAName(t *testing.T) {
	if !alsaDev("plughw:1,0") || !alsaDev("hw:0,0") || !alsaDev("default") {
		t.Fatal("ALSA names rejected")
	}
	if alsaDev("") || alsaDev("a1b2c3d4e5f60718") {
		t.Fatal("a browser device id was treated as ALSA")
	}
}
