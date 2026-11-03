package main

// Power-supply health on the Pi box. The Pi firmware reports under-voltage through
// the rpi_volt hwmon sensor, and tells the kernel how much current the USB-C supply
// offered (5000 mA for a proper Pi 5 supply, 3000 mA for ordinary USB-C PD). Both read
// as "nothing to report" anywhere else.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	hwmonDir     = envOr("CWBOX_HWMON", "/sys/class/hwmon")
	maxCurrentDT = envOr("CWBOX_MAXCURRENT", "/proc/device-tree/chosen/power/max_current")
	uvSince      time.Time // last time under-voltage was seen (since the app started)
	uvAlarmPath  string
	uvLastLookup time.Time
)

// underVoltage: whether the supply is too weak right now.
func underVoltage() bool {
	if uvAlarmPath == "" && time.Since(uvLastLookup) > 30*time.Second {
		uvLastLookup = time.Now()
		names, _ := filepath.Glob(filepath.Join(hwmonDir, "*", "name"))
		for _, n := range names {
			if b, _ := os.ReadFile(n); strings.TrimSpace(string(b)) == "rpi_volt" {
				uvAlarmPath = filepath.Join(filepath.Dir(n), "in0_lcrit_alarm")
			}
		}
	}
	if uvAlarmPath == "" {
		return false
	}
	b, _ := os.ReadFile(uvAlarmPath)
	return strings.TrimSpace(string(b)) == "1"
}

// supplyMilliamps: what the USB-C supply said it can deliver (0 = unknown).
func supplyMilliamps() int {
	b, err := os.ReadFile(maxCurrentDT)
	if err != nil || len(b) < 4 {
		return 0
	}
	return int(binary.BigEndian.Uint32(b[:4]))
}

type powerState struct {
	UnderVoltNow bool `json:"underVoltNow"`
	UnderVoltAgo int  `json:"underVoltAgo"` // seconds since last seen; -1 = never
	SupplyMA     int  `json:"supplyMA"`
}

func readPower() powerState {
	p := powerState{UnderVoltAgo: -1, SupplyMA: supplyMilliamps()}
	if underVoltage() {
		p.UnderVoltNow = true
		uvSince = time.Now()
	}
	if !uvSince.IsZero() {
		p.UnderVoltAgo = int(time.Since(uvSince).Seconds())
	}
	return p
}
