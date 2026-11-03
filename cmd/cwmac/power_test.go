package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPower(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "hwmon0"), 0o755)
	os.MkdirAll(filepath.Join(dir, "hwmon1"), 0o755)
	os.WriteFile(filepath.Join(dir, "hwmon0", "name"), []byte("cpu_thermal\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "hwmon1", "name"), []byte("rpi_volt\n"), 0o644)
	alarm := filepath.Join(dir, "hwmon1", "in0_lcrit_alarm")
	os.WriteFile(alarm, []byte("0\n"), 0o644)
	mc := filepath.Join(dir, "max_current")
	os.WriteFile(mc, []byte{0, 0, 0x0b, 0xb8}, 0o644) // 3000
	hwmonDir, maxCurrentDT, uvAlarmPath, uvLastLookup, uvSince = dir, mc, "", time.Time{}, time.Time{}

	p := readPower()
	if p.UnderVoltNow || p.UnderVoltAgo != -1 || p.SupplyMA != 3000 {
		t.Fatalf("idle: %+v", p)
	}
	os.WriteFile(alarm, []byte("1\n"), 0o644)
	if p = readPower(); !p.UnderVoltNow || p.UnderVoltAgo != 0 {
		t.Fatalf("alarm: %+v", p)
	}
	os.WriteFile(alarm, []byte("0\n"), 0o644)
	if p = readPower(); p.UnderVoltNow || p.UnderVoltAgo < 0 {
		t.Fatalf("after alarm, should remember: %+v", p)
	}
}
