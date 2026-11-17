package contest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCTY(t *testing.T) {
	newer := bytes.ReplaceAll([]byte(builtinCTY), []byte("VER20200405"), []byte("VER20261114"))
	older := bytes.ReplaceAll([]byte(builtinCTY), []byte("VER20200405"), []byte("VER20190101"))
	dir := t.TempDir()

	if _, err := InstallCTY(dir, []byte("not a country file"), "20200405"); err == nil {
		t.Fatal("junk was installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "cty.dat")); !os.IsNotExist(err) {
		t.Fatal("junk left a cty.dat")
	}

	if _, err := InstallCTY(dir, older, "20200405"); !errors.Is(err, ErrNotNewer) {
		t.Fatalf("older file: %v", err)
	}
	if _, err := InstallCTY(dir, []byte(builtinCTY), "20200405"); !errors.Is(err, ErrNotNewer) {
		t.Fatalf("same version: %v", err)
	}

	prev := filepath.Join(dir, "cty.dat")
	if err := os.WriteFile(prev, []byte("previous-file"), 0o644); err != nil {
		t.Fatal(err)
	}
	ver, err := InstallCTY(dir, newer, "20200405")
	if err != nil || ver != "20261114" {
		t.Fatalf("install %s %v", ver, err)
	}
	bak, err := os.ReadFile(prev + ".bak")
	if err != nil || string(bak) != "previous-file" {
		t.Fatalf("backup %q %v", bak, err)
	}
	got, err := os.ReadFile(prev)
	if err != nil || !bytes.Contains(got, []byte("VER20261114")) {
		t.Fatal("new file was not installed")
	}
	c, err := ParseCTY(bytes.NewReader(got))
	if err != nil || c.Version != "20261114" {
		t.Fatalf("installed file: %v %s", err, c.Version)
	}

	// A second install of something older must not touch the file or the backup.
	if _, err := InstallCTY(dir, older, ver); !errors.Is(err, ErrNotNewer) {
		t.Fatal(err)
	}
	still, _ := os.ReadFile(prev)
	if !bytes.Equal(still, got) {
		t.Fatal("a rejected download replaced the file")
	}
	if _, err := os.Stat(prev + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
}
