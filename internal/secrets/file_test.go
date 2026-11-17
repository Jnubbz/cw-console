package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTripDoesNotEchoIntoErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	f, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", fi.Mode().Perm())
		}
	}
	if err := f.Set(QRZPassword, "not-a-real-password"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set(WRLAPIKey, "wrl_live_example"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := f.Get(QRZPassword)
	if err != nil || !ok || got != "not-a-real-password" {
		t.Fatalf("get %q %v %v", got, ok, err)
	}
	has, err := f.Has(LoTWPassword)
	if err != nil || has {
		t.Fatalf("unset key looked present: %v %v", has, err)
	}
	if err := f.Set(QRZPassword, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.Get(QRZPassword); err != nil || ok {
		t.Fatalf("deleted key still present: %v %v", ok, err)
	}
	// The WRL key survives a delete of a different key.
	if got, ok, err := f.Get(WRLAPIKey); err != nil || !ok || got != "wrl_live_example" {
		t.Fatalf("wrl %q %v %v", got, ok, err)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := again.Get(WRLAPIKey); err != nil || !ok || got != "wrl_live_example" {
		t.Fatalf("reopen %q %v %v", got, ok, err)
	}
}
