package main

// A tiny mDNS responder so the page is at http://potapi.local on the home network:
// it answers "potapi.local" A queries (RFC 6762) with the box's Wi-Fi address. Nothing
// else is announced or answered.

import (
	"encoding/binary"
	"net"
	"os"
	"strings"
	"time"
)

const mdnsName = "potapi.local"

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// wifiStatus: "home" (on the home network), "ap" (the box's own), "off" or ""; and the
// box's Wi-Fi IPv4 address.
func wifiStatus() (string, string) {
	b, _ := os.ReadFile("/tmp/wifi-mode")
	ip := ifaceIPv4("wlan0")
	s := ""
	if ip != nil {
		s = ip.String()
	}
	return strings.TrimSpace(string(b)), s
}

func ifaceIPv4(name string) net.IP {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	addrs, _ := ifi.Addrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return n.IP.To4()
		}
	}
	return nil
}

// runMDNS keeps a responder on wlan0, rejoining when its address changes (home <-> own network).
func runMDNS() {
	var conn *net.UDPConn
	var cur string
	for {
		ip := ifaceIPv4("wlan0")
		s := ""
		if ip != nil {
			s = ip.String()
		}
		if s != cur {
			if conn != nil {
				conn.Close()
				conn = nil
			}
			cur = s
			if ip != nil {
				if ifi, err := net.InterfaceByName("wlan0"); err == nil {
					if c, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup); err == nil {
						conn = c
						go mdnsServe(c, ip)
					} else {
						cur = "" // try again next time round
					}
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func mdnsServe(c *net.UDPConn, ip net.IP) {
	buf := make([]byte, 1500)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		legacy := from.Port != 5353 // a plain DNS client asking the mDNS address (RFC 6762 6.7)
		resp := mdnsAnswer(buf[:n], ip, legacy)
		if resp == nil {
			continue
		}
		if legacy {
			c.WriteToUDP(resp, from)
		} else {
			c.WriteToUDP(resp, mdnsGroup)
		}
	}
}

// mdnsAnswer: the reply to a query packet if it asks for potapi.local (A or ANY).
func mdnsAnswer(p []byte, ip net.IP, legacy bool) []byte {
	if len(p) < 12 || p[2]&0x80 != 0 { // too short, or a response
		return nil
	}
	id := binary.BigEndian.Uint16(p[0:])
	qd := int(binary.BigEndian.Uint16(p[4:]))
	off := 12
	var q []byte // the matching question, for legacy unicast replies
	match := false
	for i := 0; i < qd; i++ {
		name, next, ok := dnsName(p, off)
		if !ok || next+4 > len(p) {
			return nil
		}
		typ := binary.BigEndian.Uint16(p[next:])
		cls := binary.BigEndian.Uint16(p[next+2:]) & 0x7fff
		if strings.EqualFold(name, mdnsName) && (typ == 1 || typ == 255) && (cls == 1 || cls == 255) {
			match = true
			q = append(q[:0], p[off:next+4]...)
		}
		off = next + 4
	}
	if !match {
		return nil
	}
	return mdnsReply(id, q, ip, legacy)
}

// mdnsReply builds the answer packet (legacy: echo the ID and question, no cache-flush bit).
func mdnsReply(id uint16, q []byte, ip net.IP, legacy bool) []byte {
	b := make([]byte, 12, 64)
	if legacy {
		binary.BigEndian.PutUint16(b[0:], id)
		binary.BigEndian.PutUint16(b[4:], 1)
	}
	binary.BigEndian.PutUint16(b[2:], 0x8400) // response, authoritative
	binary.BigEndian.PutUint16(b[6:], 1)      // one answer
	if legacy {
		b = append(b, q...)
	}
	for _, l := range strings.Split(mdnsName, ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0)
	cls := uint16(0x8001) // IN, cache-flush (we own this name)
	if legacy {
		cls = 1
	}
	b = binary.BigEndian.AppendUint16(b, 1) // A
	b = binary.BigEndian.AppendUint16(b, cls)
	b = binary.BigEndian.AppendUint32(b, 120) // TTL
	b = binary.BigEndian.AppendUint16(b, 4)
	return append(b, ip.To4()...)
}

// dnsName reads a (possibly compressed) name at off; next is the offset after it.
func dnsName(p []byte, off int) (name string, next int, ok bool) {
	var parts []string
	next = -1
	for jumps := 0; jumps < 20; {
		if off >= len(p) {
			return "", 0, false
		}
		l := int(p[off])
		switch {
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			return strings.Join(parts, "."), next, true
		case l&0xc0 == 0xc0:
			if off+1 >= len(p) {
				return "", 0, false
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(p[off:]) & 0x3fff)
			jumps++
		case l&0xc0 != 0:
			return "", 0, false
		default:
			if off+1+l > len(p) {
				return "", 0, false
			}
			parts = append(parts, string(p[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, false
}
