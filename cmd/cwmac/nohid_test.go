//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseRigHID(t *testing.T) {
	d := t.TempDir()
	drv := filepath.Join(d, "drivers", "usbhid")
	os.MkdirAll(filepath.Join(drv, "3-2.2:1.3"), 0o755)
	os.MkdirAll(filepath.Join(drv, "1-1:1.0"), 0o755) // a keyboard: leave it
	for dev, id := range map[string][2]string{"3-2.2": {"0d8c", "0013"}, "1-1": {"046d", "c31c"}} {
		os.MkdirAll(filepath.Join(d, "devices", dev), 0o755)
		os.WriteFile(filepath.Join(d, "devices", dev, "idVendor"), []byte(id[0]+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "devices", dev, "idProduct"), []byte(id[1]+"\n"), 0o644)
	}
	releaseRigHID(d)
	os.Chmod(filepath.Join(drv, "unbind"), 0o600) // written write-only, like sysfs's; readable for the check (non-root)
	b, _ := os.ReadFile(filepath.Join(drv, "unbind"))
	if string(b) != "3-2.2:1.3" {
		t.Fatalf("unbind got %q", b)
	}
}
