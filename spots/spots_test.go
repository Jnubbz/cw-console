package spots

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseRBN(t *testing.T) {
	for _, c := range []struct {
		line string
		want RBNSpot
		ok   bool
	}{
		{"DX de W3LPL-#:   14025.0  KN0BLE       CW    24 dB  22 WPM  CQ      1802Z",
			RBNSpot{Skimmer: "W3LPL", KHz: 14025.0, Call: "KN0BLE", Mode: "CW", SNR: 24, WPM: 22, Kind: "CQ"}, true},
		{"DX de VE6WZ-#:    7030.1  K1ABC/P      CW     3 dB  18 WPM  CQ      0003Z\r\n",
			RBNSpot{Skimmer: "VE6WZ", KHz: 7030.1, Call: "K1ABC/P", Mode: "CW", SNR: 3, WPM: 18, Kind: "CQ"}, true},
		{"DX de KM3T-2-#:  14100.0  4U1UN        CW    12 dB  22 WPM  NCDXF B 1803Z",
			RBNSpot{}, false}, // skimmer with a dash in its name: not this format; skipped is fine
		{"DX de DK8NE-#:   14080.0  DL1ABC       RTTY  15 dB  45 BPS  CQ      1804Z",
			RBNSpot{Skimmer: "DK8NE", KHz: 14080.0, Call: "DL1ABC", Mode: "RTTY", SNR: 15, WPM: 45, Kind: "CQ"}, true},
		{"Please enter your call:", RBNSpot{}, false},
		{": DX de W3LPL-#:   14025.0  K1ABC        CW    24 dB  22 WPM  CQ      1802Z", // after the login prompt
			RBNSpot{Skimmer: "W3LPL", KHz: 14025.0, Call: "K1ABC", Mode: "CW", SNR: 24, WPM: 22, Kind: "CQ"}, true},
		{"", RBNSpot{}, false},
	} {
		got, ok := ParseRBN(c.line)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q: got %+v %v", c.line, got, ok)
		}
	}
}

func TestStoreMergesAndExpires(t *testing.T) {
	s := NewStore()
	s.MyCall = "KN0BLE"
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.AddRBN(RBNSpot{Skimmer: "W3LPL", KHz: 14025.0, Call: "K1ABC", SNR: 10, WPM: 20, Kind: "CQ"})
	s.AddRBN(RBNSpot{Skimmer: "VE6WZ", KHz: 14025.1, Call: "K1ABC", SNR: 24, WPM: 21, Kind: "CQ"})
	s.AddRBN(RBNSpot{Skimmer: "W3LPL", KHz: 7030.0, Call: "K1ABC", SNR: 5, Kind: "CQ"}) // another band: its own
	s.AddRBN(RBNSpot{Skimmer: "KM3T", KHz: 14030.0, Call: "KN0BLE/P", SNR: 17, WPM: 22, Kind: "CQ"})
	s.AddRBN(RBNSpot{Skimmer: "W3LPL", KHz: 14030.0, Call: "KN0BLE", SNR: 27, WPM: 22, Kind: "CQ"})

	sp := s.Spots(14000, 14070)
	if len(sp) != 3 { // K1ABC, KN0BLE/P and KN0BLE (portable is its own station)
		t.Fatalf("20 m spots: %+v", sp)
	}
	var k1 Spot
	for _, x := range sp {
		if x.Call == "K1ABC" {
			k1 = x
		}
	}
	if k1.Skimmers != 2 || k1.SNR != 24 || k1.WPM != 21 || k1.Source != "rbn" {
		t.Fatalf("merged: %+v", k1)
	}
	h := s.HeardMe()
	if len(h) != 2 || h[0].Skimmer != "W3LPL" || h[0].SNR != 27 {
		t.Fatalf("heard me: %+v", h)
	}
	// POTA: CW ones join (and win the source), others are left out
	s.SetPOTA([]POTASpot{
		{Activator: "K1ABC", Reference: "US-1234", Name: "Acadia NP", Mode: "CW", KHz: 14025.0, Location: "US-ME"},
		{Activator: "W4XYZ", Reference: "US-5678", Mode: "SSB", KHz: 14250.0},
	})
	for _, x := range s.Spots(14000, 14070) {
		if x.Call == "K1ABC" && (x.Source != "pota" || x.Park != "US-1234" || x.Skimmers != 2) {
			t.Fatalf("pota merge: %+v", x)
		}
	}
	if len(s.Spots(14200, 14300)) != 0 {
		t.Fatal("SSB activator on the strip")
	}
	now = now.Add(16 * time.Minute)
	if len(s.Spots(0, 1e6)) != 0 || len(s.HeardMe()) != 0 {
		t.Fatal("old spots kept")
	}
}

func TestParsePOTA(t *testing.T) {
	// the real shape (Nexus's test payload, api.pota.app/spot/activator)
	js := `[
	  {"spotId":51518793,"activator":"F4MOJ/P","frequency":"3573.0","mode":"FT8",
	   "reference":"FR-11086","parkName":null,"spotTime":"2026-06-07T05:34:30",
	   "spotter":"OE9GHV-#","comments":"RBN 1 dB via OE9GHV-#","source":"RBN",
	   "name":"Belledonne Reserve","locationDesc":"FR-ARA","grid4":"JN35","grid6":"JN35bh"},
	  {"activator":"k1abc","frequency":"14062.5","mode":"CW","reference":"US-1234",
	   "name":"Acadia NP","spotter":"W9XYZ","comments":"","spotTime":"2026-11-03T19:59:00"},
	  {"activator":"","frequency":"7030","mode":"CW","reference":"US-1"}
	]`
	p, err := ParsePOTA([]byte(js))
	if err != nil || len(p) != 2 {
		t.Fatal(p, err)
	}
	if p[1].Activator != "K1ABC" || p[1].KHz != 14062.5 || p[1].Reference != "US-1234" || p[1].Time.Hour() != 19 {
		t.Fatalf("%+v", p[1])
	}
	if p[0].KHz != 3573 || p[0].Location != "FR-ARA" {
		t.Fatalf("%+v", p[0])
	}
}

func TestRunPOTA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"activator":"K1ABC","frequency":"14062.5","mode":"CW","reference":"US-1234"}]`))
	}))
	defer srv.Close()
	s := NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	go s.RunPOTA(ctx, srv.URL, srv.Client())
	defer cancel()
	for i := 0; i < 50 && len(s.Spots(14000, 14070)) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if sp := s.Spots(14000, 14070); len(sp) != 1 || sp[0].Park != "US-1234" {
		t.Fatalf("%+v", sp)
	}
}

// A fake RBN node: asks for a call, checks it, sends spots.
func TestRunRBN(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	gotCall := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.Write([]byte("Welcome to RBN.\r\nPlease enter your call: "))
		l, _ := bufio.NewReader(c).ReadString('\n')
		gotCall <- strings.TrimSpace(l)
		c.Write([]byte("DX de W3LPL-#:   14025.0  K1ABC        CW    24 dB  22 WPM  CQ      1802Z\r\n" +
			"DX de W3LPL-#:   14030.0  KN0BLE       CW    19 dB  22 WPM  CQ      1802Z\r\n" +
			"DX de DK8NE-#:   14080.0  DL1ABC       RTTY  15 dB  45 BPS  CQ      1804Z\r\n"))
		time.Sleep(2 * time.Second)
	}()
	s := NewStore()
	s.MyCall = "KN0BLE"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunRBN(ctx, ln.Addr().String(), "KN0BLE")
	if c := <-gotCall; c != "KN0BLE" {
		t.Fatalf("logged in as %q", c)
	}
	for i := 0; i < 100 && len(s.HeardMe()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if len(s.Spots(14000, 14100)) != 2 || len(s.HeardMe()) != 1 || !s.Status().RBN {
		t.Fatalf("spots %+v heard %+v status %+v", s.Spots(14000, 14100), s.HeardMe(), s.Status())
	}
}
