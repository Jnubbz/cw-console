package main

import (
	"net"
	"testing"
)

func TestMDNSAnswer(t *testing.T) {
	q := []byte{0, 7, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	q = append(q, 6, 'P', 'o', 't', 'a', 'p', 'i', 5, 'l', 'o', 'c', 'a', 'l', 0, 0, 1, 0x80, 1)
	ip := net.IPv4(192, 168, 1, 50)
	r := mdnsAnswer(q, ip, false)
	if r == nil {
		t.Fatal("no answer")
	}
	if r[0] != 0 || r[1] != 0 || r[7] != 1 || string(r[len(r)-4:]) != string(ip.To4()) {
		t.Fatalf("bad reply % x", r)
	}
	name, next, ok := dnsName(r, 12)
	if !ok || name != "potapi.local" || r[next+2] != 0x80 {
		t.Fatalf("bad name %q", name)
	}
	l := mdnsAnswer(q, ip, true)
	if l[1] != 7 || l[5] != 1 {
		t.Fatalf("legacy: % x", l)
	}
	other := append([]byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}, 4, 'x', 'y', 'z', 'w', 5, 'l', 'o', 'c', 'a', 'l', 0, 0, 1, 0, 1)
	if mdnsAnswer(other, ip, false) != nil {
		t.Fatal("answered someone else's name")
	}
}

func TestBoxConfigHome(t *testing.T) {
	dataDir = t.TempDir()
	c := loadBoxConfig()
	c.HomeName, c.HomePassword = "Noble Home", "hunter22pw"
	if err := saveBoxConfig(c); err != nil {
		t.Fatal(err)
	}
	d := loadBoxConfig()
	if d.HomeName != "Noble Home" || d.HomePassword != "hunter22pw" || d.WifiName != "POTA-Pi" {
		t.Fatalf("%+v", d)
	}
	c.HomePassword = "short"
	if saveBoxConfig(c) == nil {
		t.Fatal("short home password accepted")
	}
	c.HomeName, c.HomePassword = `bad"name`, ""
	if saveBoxConfig(c) == nil {
		t.Fatal("quote accepted")
	}
}
