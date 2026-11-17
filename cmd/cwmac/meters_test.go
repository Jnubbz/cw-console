package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/rig"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

func TestMeterSchedule(t *testing.T) {
	var s meterSched
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	var got []string
	for i := 0; i < 40; i++ {
		cmd, _, wait := s.next(now, false)
		if cmd != "" {
			got = append(got, cmd)
			now = now.Add(meterGap)
		} else {
			now = now.Add(wait)
		}
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, ft710.MeterS) {
		t.Fatalf("no S meter on receive: %s", joined)
	}
	for _, tx := range []string{ft710.MeterPO, ft710.MeterALC, ft710.MeterSWR} {
		if strings.Contains(joined, tx) {
			t.Fatalf("transmit meter %s while receiving: %s", tx, joined)
		}
	}
	if !strings.Contains(joined, "FA;") {
		t.Fatalf("state poll missing: %s", joined)
	}

	s = meterSched{}
	now = time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	got = nil
	for i := 0; i < 12; i++ {
		cmd, pri, wait := s.next(now, true)
		if pri != rig.PriMeter && cmd != "" {
			t.Fatalf("keyed poll pri %v", pri)
		}
		if cmd != "" {
			got = append(got, cmd)
			now = now.Add(meterGap)
		} else {
			now = now.Add(wait)
		}
	}
	joined = strings.Join(got, " ")
	if strings.Contains(joined, ft710.MeterS) || strings.Contains(joined, "FA;") {
		t.Fatalf("receive poll while keyed: %s", joined)
	}
	for _, tx := range []string{ft710.MeterPO, ft710.MeterALC, ft710.MeterSWR} {
		if !strings.Contains(joined, tx) {
			t.Fatalf("missing %s: %s", tx, joined)
		}
	}
}

func TestSignalAndTXMeters(t *testing.T) {
	n, err := ft710.SignalRaw("RM1123000;")
	if err != nil || n != 123 {
		t.Fatalf("RM1 %d %v", n, err)
	}
	n, err = ft710.SignalRaw("SM0042;")
	if err != nil || n != 42 {
		t.Fatalf("SM0 %d %v", n, err)
	}
	if _, err := ft710.SignalRaw("RM6999;"); err == nil {
		t.Fatal("SWR answer accepted as S")
	}
	if ft710.SWR(0) < 1 || ft710.Watts(0) != 0 || ft710.ALC(0) != 0 {
		t.Fatal("zero raw")
	}
}
