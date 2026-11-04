package main

import (
	"encoding/json"
	"testing"

	"github.com/Jnubbz/cw-console/spots"
)

func TestSpotsForBand(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	t.Setenv("CWCONSOLE_RBN_ADDR", "127.0.0.1:1") // nowhere: no network in tests
	t.Setenv("CWCONSOLE_POTA_URL", "http://127.0.0.1:1/")
	e := newEngine(newHub())
	r := e.spotsFor("20m")
	if !r.On || r.From != 14000 || r.To != 14070 || len(r.Bands) != len(stripBands) {
		t.Fatalf("%+v", r)
	}
	e.feeds.store.AddRBN(spots.RBNSpot{Skimmer: "W3LPL", KHz: 14025, Call: "K1ABC", SNR: 20, Kind: "CQ"})
	e.feeds.store.AddRBN(spots.RBNSpot{Skimmer: "W3LPL", KHz: 7030, Call: "K2ABC", SNR: 20, Kind: "CQ"})
	if r := e.spotsFor("20m"); len(r.Spots) != 1 || r.Spots[0].Call != "K1ABC" {
		t.Fatalf("20 m: %+v", r.Spots)
	}
	if r := e.spotsFor("nope"); r.Band != "" || len(r.Spots) != 0 {
		t.Fatalf("unknown band: %+v", r)
	}
	e.applySettings(json.RawMessage(`{"spotsOn":false}`))
	if r := e.spotsFor("20m"); r.On || len(r.Spots) != 0 || e.feeds.on {
		t.Fatalf("off: %+v (feeds on %v)", r, e.feeds.on)
	}
}

func TestHomeStationByDefault(t *testing.T) {
	s := defaultSettings()
	if s.POTAMode || !s.SpotsOn {
		t.Fatalf("defaults: potaMode %v spotsOn %v", s.POTAMode, s.SpotsOn)
	}
}
