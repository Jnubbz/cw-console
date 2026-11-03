package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRadioProfiles(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	e := newEngine(newHub())
	if len(e.st.Radios) != 1 || e.st.Radio != "Radio 1" {
		t.Fatalf("first profile: %+v", e.st.Radios)
	}
	e.applySettings(json.RawMessage(`{"txPort":"/dev/serial/by-id/usb-Silicon_Labs_CP2105-if01-port0","txLine":"rts","ft8Level":62,"ft8Out":"hw44"}`))
	if err := e.radioAction("rename", "Radio 1", "FT-710"); err != nil {
		t.Fatal(err)
	}
	if err := e.radioAction("new", "", "FX-4CR"); err != nil {
		t.Fatal(err)
	}
	if e.st.Radio != "FX-4CR" || e.st.TxPort != "" || e.st.FT8Level != 62 {
		t.Fatalf("new radio: %+v", e.st)
	}
	e.applySettings(json.RawMessage(`{"txPort":"/dev/serial/by-id/usb-FX4CR-if00","txLine":"dtr","ft8Level":40,"ft8Out":""}`))
	if err := e.radioAction("switch", "FT-710", ""); err != nil {
		t.Fatal(err)
	}
	if e.st.TxPort != "/dev/serial/by-id/usb-Silicon_Labs_CP2105-if01-port0" || e.st.TxLine != "rts" || e.st.FT8Level != 62 || e.st.FT8Out != "hw44" {
		t.Fatalf("back to the 710: %+v", e.st)
	}
	e.radioAction("switch", "FX-4CR", "")
	if e.st.TxPort != "/dev/serial/by-id/usb-FX4CR-if00" || e.st.FT8Level != 40 || e.st.TxLine != "dtr" {
		t.Fatalf("the FX: %+v", e.st)
	}
	// saved and loaded again
	e2 := newEngine(newHub())
	if e2.st.Radio != "FX-4CR" || len(e2.st.Radios) != 2 || e2.st.Radios[e2.st.radioIndex("FT-710")].FT8Out != "hw44" {
		t.Fatalf("reload: %+v", e2.st.Radios)
	}
	// a setting from the page can't overwrite the profiles
	e.applySettings(json.RawMessage(`{"radios":[],"radio":"x"}`))
	if len(e.st.Radios) != 2 || e.st.Radio != "FX-4CR" {
		t.Fatal("profiles overwritten")
	}
	if e.radioAction("new", "", "FT-710") == nil || e.radioAction("delete", "Nope", "") == nil {
		t.Fatal("bad actions accepted")
	}
	if err := e.radioAction("delete", "FX-4CR", ""); err != nil || e.st.Radio != "FT-710" || e.st.FT8Out != "hw44" {
		t.Fatalf("delete current: %v %+v", err, e.st)
	}
	if e.radioAction("delete", "FT-710", "") == nil {
		t.Fatal("deleted the only radio")
	}
	_ = os.Remove(filepath.Join(dataDir, "x"))
}

func TestRadioAutoSwitch(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	old := serialByID
	serialByID = t.TempDir() + "/"
	defer func() { serialByID = old }()
	e := newEngine(newHub())
	p710, pfx := serialByID+"usb-Silicon_Labs_CP2105-if01-port0", serialByID+"usb-FX4CR-if00"
	os.WriteFile(p710, nil, 0o644)
	e.applySettings(json.RawMessage(`{"txPort":"` + p710 + `"}`))
	e.radioAction("rename", "Radio 1", "FT-710")
	e.radioAction("new", "", "FX-4CR")
	e.applySettings(json.RawMessage(`{"txPort":"` + pfx + `"}`))
	e.radioAction("switch", "FT-710", "")
	e.mu.Lock()
	e.autoRadioLocked() // the 710 is still plugged in: stay
	if e.st.Radio != "FT-710" {
		t.Fatal("switched away from a radio that's there")
	}
	os.Remove(p710)
	e.autoRadioLocked() // 710 gone, FX not there either: stay
	if e.st.Radio != "FT-710" {
		t.Fatal("switched to a radio that isn't there")
	}
	os.WriteFile(pfx, nil, 0o644)
	e.radioGone = 0
	e.autoRadioLocked() // the FX is plugged in, but the 710 has only just gone
	if e.st.Radio != "FT-710" {
		t.Fatal("switched on a moment's dropout")
	}
	e.autoRadioLocked()
	e.autoRadioLocked() // gone for three scans: the FX it is
	if e.st.Radio != "FX-4CR" || e.st.TxPort != pfx || !e.restartCapture {
		t.Fatalf("no switch: %+v", e.st.Radio)
	}
	os.WriteFile(p710, nil, 0o644)
	os.Remove(pfx)
	e.autoRadioLocked()
	os.WriteFile(pfx, nil, 0o644) // back within a scan: no switch, and the count starts over
	e.autoRadioLocked()
	os.Remove(pfx)
	e.autoRadioLocked()
	e.autoRadioLocked()
	if e.st.Radio != "FX-4CR" {
		t.Fatal("a dropout that came back counted towards switching")
	}
	e.mu.Unlock()
}

// settings saved by an older version: PTT "same as CW" (""), a missing key line, junk
func TestRadioMigration(t *testing.T) {
	dataDir = t.TempDir()
	defer func() { dataDir = "" }()
	old := `{"radio":"FX-4CR","txPort":"/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0","txLine":"audio","ft8PttLine":"",
	  "radios":[
	    {"name":"FT-710","txPort":"/dev/serial/by-id/usb-Silicon_Labs_CP2105-if01-port0","txLine":"rts","ft8PttLine":"","ft8Out":"hw44","ft8Level":70,"pitch":600},
	    {"name":"FX-4CR","txPort":"/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0","txLine":"audio","ft8PttLine":"","ft8Level":100,"catType":"kenwood"},
	    {"name":"Old","txLine":"","ft8PttLine":"","ft8Out":"bogus","ft8Level":250,"catType":"icom","pitch":5000}
	  ]}`
	if err := os.WriteFile(filepath.Join(dataDir, "mac-settings.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEngine(newHub())
	if e.st.Radio != "FX-4CR" || e.st.FT8PTTLine != "cat" {
		t.Fatalf("current: %q PTT %q", e.st.Radio, e.st.FT8PTTLine)
	}
	want := map[string]string{"FT-710": "rts", "FX-4CR": "cat", "Old": "dtr"}
	for _, p := range e.st.Radios {
		if p.FT8PTTLine != want[p.Name] {
			t.Fatalf("%s: PTT %q, want %q", p.Name, p.FT8PTTLine, want[p.Name])
		}
	}
	o := e.st.Radios[e.st.radioIndex("Old")]
	if o.TxLine != "dtr" || o.FT8Out != "" || o.FT8Level != 100 || o.CATType != "" || o.Pitch != pitchMax {
		t.Fatalf("Old not cleaned: %+v", o)
	}
	if err := e.radioAction("switch", "FT-710", ""); err != nil {
		t.Fatal(err)
	}
	if e.st.TxLine != "rts" || e.st.FT8PTTLine != "rts" || e.st.FT8Out != "hw44" || e.st.FT8Level != 70 {
		t.Fatalf("FT-710: %+v", e.st)
	}
	// the page can't put "same as CW" back
	e.applySettings(json.RawMessage(`{"ft8PttLine":""}`))
	if e.st.FT8PTTLine != "rts" {
		t.Fatalf("PTT %q", e.st.FT8PTTLine)
	}
	// saved explicitly
	b, _ := os.ReadFile(filepath.Join(dataDir, "mac-settings.json"))
	if json.Unmarshal(b, &e.st) != nil || e.st.Radios[e.st.radioIndex("FX-4CR")].FT8PTTLine != "cat" {
		t.Fatalf("saved: %s", b)
	}
}
