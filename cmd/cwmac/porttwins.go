package main

// The FT-710's USB serial chip (a Silicon Labs CP2105) has two ports: interface 0, the
// Enhanced port (CAT), and interface 1, the Standard port (CW keying, PTT). On a Mac each
// can show up twice, once per driver:
//
//	Apple's driver:        /dev/cu.usbserial-01AE6E900  (serial number + interface)
//	                       /dev/cu.usbserial-01AE6E901
//	Silicon Labs' driver:  /dev/cu.SLAB_USBtoUART, /dev/cu.SLAB_USBtoUART8 (no way to tell which)
//
// Opening any of them raises its DTR, so opening the key port's twin keys the radio (and
// mutes its receiver: no decoding). Apple's names say which interface they are, so when a
// pair of them is there, the app pairs them itself and never probes anything else.

import (
	"path/filepath"
	"regexp"
	"strings"
)

var portList = listPorts // test hook

var appleCP2105 = regexp.MustCompile(`^cu\.usbserial-([0-9A-Za-z]+)([01])$`)

// cp2105Pairs: serial number -> [interface 0 path, interface 1 path], for every Apple-named
// pair present.
func cp2105Pairs(ports []portInfo) map[string][2]string {
	seen := map[string][2]string{}
	for _, p := range ports {
		m := appleCP2105.FindStringSubmatch(filepath.Base(p.Path))
		if m == nil {
			continue
		}
		v := seen[m[1]]
		if m[2] == "0" {
			v[0] = p.Path
		} else {
			v[1] = p.Path
		}
		seen[m[1]] = v
	}
	out := map[string][2]string{}
	for k, v := range seen {
		if v[0] != "" && v[1] != "" {
			out[k] = v
		}
	}
	return out
}

// pairedCATPort: the CAT port (interface 0) that goes with a key port (interface 1), by
// Apple's names ("" if the key port isn't one of a pair).
func pairedCATPort(keyPort string, ports []portInfo) string {
	for _, v := range cp2105Pairs(ports) {
		if samePort(v[1], keyPort) {
			return v[0]
		}
	}
	return ""
}

// catCandidates: the ports worth asking for the FT-710's ID, safest first. With Apple-named
// pairs present only their interface 0 ports (never a Standard port, never the Silicon
// Labs duplicates, either of which could be the key port's twin).
func catCandidates(keyPort string, ports []portInfo) []string {
	var out []string
	if pairs := cp2105Pairs(ports); len(pairs) > 0 {
		for _, v := range pairs {
			if !samePort(v[0], keyPort) {
				out = append(out, v[0])
			}
		}
		return out
	}
	for _, p := range ports {
		if !samePort(p.Path, keyPort) && likelyRigPort(p.Path) {
			out = append(out, p.Path)
		}
	}
	return out
}

// portRole: a label for Setup ("CAT: Enhanced", "key: Standard") for Apple-named pairs.
func portRole(path string, ports []portInfo) string {
	for _, v := range cp2105Pairs(ports) {
		switch path {
		case v[0]:
			return "FT-710 Enhanced port: CAT"
		case v[1]:
			return "FT-710 Standard port: key"
		}
	}
	if strings.Contains(path, "SLAB_USBtoUART") && len(cp2105Pairs(ports)) > 0 {
		return "duplicate of a usbserial port: use those"
	}
	return ""
}

// isStandardPort: an Apple-named interface 1 port (a CP2105's Standard port: keying, never CAT).
func isStandardPort(path string) bool {
	for _, v := range cp2105Pairs(portList()) {
		if samePort(v[1], path) {
			return true
		}
	}
	return false
}
