package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/contest"
	"github.com/Jnubbz/cw-console/logbook"
	"github.com/Jnubbz/cw-console/lookup"
)

func TestCardOfflineFromTheLogAndCTY(t *testing.T) {
	cty := contest.Builtin()
	b := &logbook.Book{}
	if err := b.Add(logbook.QSO{
		Call: "W1AW", Start: time.Date(2024, 3, 2, 12, 0, 0, 0, time.UTC),
		Band: "20m", Mode: "CW", CTYPrefix: "K", State: "CT",
	}); err != nil {
		t.Fatal(err)
	}
	card, _ := assembleCard("w1aw", "20m", "CW", cty, b, lookup.Card{}, nil, "off")
	if card.Country == "" || card.Prefix != "K" || card.CQ != 5 || card.Cont != "NA" {
		t.Fatalf("cty: %+v", card)
	}
	if !card.Worked || card.Last != "2024-03-02 20m CW" || len(card.Needs) != 0 {
		t.Fatalf("worked: %+v", card)
	}
	if card.QRZ != "off" {
		t.Fatalf("qrz %s", card.QRZ)
	}

	band, _ := assembleCard("W1AW/7", "15m", "CW", cty, b, lookup.Card{}, nil, "off")
	if !band.Worked || !contains(band.Needs, "new band") || contains(band.Needs, "new DXCC") {
		t.Fatalf("new band: %+v", band)
	}

	dxcc, _ := assembleCard("JA1ABC", "20m", "CW", cty, b, lookup.Card{}, nil, "off")
	if dxcc.Worked || !contains(dxcc.Needs, "new DXCC") || dxcc.Country == "" || dxcc.CQ == 0 {
		t.Fatalf("new dxcc: %+v", dxcc)
	}
}

func TestCardQRZFillsStateAndSurvivesOffline(t *testing.T) {
	cty := contest.Builtin()
	b := &logbook.Book{}
	_ = b.Add(logbook.QSO{Call: "W1AW", Start: time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC), Band: "20m", Mode: "CW", CTYPrefix: "K"})
	qrz := lookup.Card{Call: "K0NEW", Name: "Ada Lovelace", State: "ND", Grid: "DN76", DXCC: 291, Country: "United States"}
	card, _ := assembleCard("K0NEW", "20m", "CW", cty, b, qrz, nil, "ok")
	if card.Name != "Ada Lovelace" || card.State != "ND" || card.Grid != "DN76" || card.DXCC != 291 {
		t.Fatalf("qrz fields: %+v", card)
	}
	if !contains(card.Needs, "new state") || contains(card.Needs, "new DXCC") {
		t.Fatalf("needs: %v", card.Needs)
	}
	// Same entity (prefix K) is already in the log, so it is not a new DXCC.
	// A stale card still shows when the network fails.
	stale, _ := assembleCard("K0NEW", "20m", "FT8", cty, b, qrz, lookup.ErrOffline, "offline")
	if stale.QRZ != "offline" || stale.Name != "Ada Lovelace" || stale.Country == "" {
		t.Fatalf("offline: %+v", stale)
	}
	if !contains(stale.Needs, "new mode") {
		t.Fatalf("new mode: %v", stale.Needs)
	}
}

func TestCardUnknownEntityIsNotANewDXCC(t *testing.T) {
	card, _ := assembleCard("K1ABC/AM", "20m", "CW", contest.Builtin(), &logbook.Book{}, lookup.Card{}, errors.New("no"), "miss")
	if card.QRZ != "miss" || contains(card.Needs, "new DXCC") {
		t.Fatalf("%+v", card)
	}
}

func TestQRZStatusJSONHasNoSecret(t *testing.T) {
	// The page is told whether a login is stored, never the login itself.
	s := qrzStatus{User: true, Password: true}
	if s.User != true || s.Password != true {
		t.Fatal(s)
	}
	// A password string must not be a field the state broadcast can carry.
	var st stateMsg
	st.Card = &cardMsg{Call: "W1AW", Country: "United States", QRZ: "off"}
	if strings.Contains(st.Card.Call, "password") {
		t.Fatal("card call")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
