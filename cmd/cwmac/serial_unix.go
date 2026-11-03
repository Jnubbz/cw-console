//go:build darwin || linux

package main

// Serial keyline for macOS (and Linux, for testing): toggle DTR or RTS with ioctl.
// Like the Windows version, both lines are dropped the moment the port opens, because
// opening a tty asserts DTR (and often RTS), which on a keying setup means transmit.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type unixLine struct {
	fd     int
	useRTS bool
	port   string
}

func ioctlInt(fd int, req uintptr, v int32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&v)))
	if errno != 0 {
		return errno
	}
	return nil
}

// testLine records keying (only with CWMAC_TEST=1 and the special port name); used by the
// automated tests, never in normal use.
type testLine struct{ downs, ups int }

var lastTestLine *testLine

func (t *testLine) Key(down bool) error {
	if down {
		t.downs++
	} else {
		t.ups++
	}
	return nil
}
func (t *testLine) Close() {}

func openKeyLine(port string, useRTS bool) (keyLine, error) {
	if pin, ok := parseGPIOPort(port); ok {
		return openGPIOKey(pin)
	}
	if port == "test:keyline" && os.Getenv("CWMAC_TEST") == "1" {
		lastTestLine = &testLine{}
		return lastTestLine, nil
	}
	fd, err := syscall.Open(port, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("can't open %s: %v (is another program, e.g. Nexus or WSJT-X, using it?)", port, err)
	}
	l := &unixLine{fd: fd, useRTS: useRTS, port: port}
	if err := ioctlInt(fd, tiocmBIC, tiocmDTR|tiocmRTS); err != nil { // FIRST: both lines down
		syscall.Close(fd)
		return nil, fmt.Errorf("%s doesn't support DTR/RTS control: %v", port, err)
	}
	return l, nil
}

func (l *unixLine) Key(down bool) error {
	bit := int32(tiocmDTR)
	if l.useRTS {
		bit = tiocmRTS
	}
	req := uintptr(tiocmBIC)
	if down {
		req = tiocmBIS
	}
	if err := ioctlInt(l.fd, req, bit); err != nil {
		return fmt.Errorf("%s: keying failed: %v", l.port, err)
	}
	return nil
}

func (l *unixLine) Close() {
	if l.fd >= 0 {
		ioctlInt(l.fd, tiocmBIC, tiocmDTR|tiocmRTS)
		syscall.Close(l.fd)
		l.fd = -1
	}
}

type portInfo struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

func listPorts() []portInfo {
	out := []portInfo{} // an empty list, never null
	seen := map[string]bool{}
	// Linux: /dev/serial/by-id names stay the same from boot to boot and say what the
	// device is ("Silicon Labs CP2105 ... port 2"), unlike ttyUSB0/1 which can swap.
	if byID, _ := filepath.Glob("/dev/serial/by-id/*"); len(byID) > 0 {
		for _, p := range byID {
			if t, err := filepath.EvalSymlinks(p); err == nil {
				seen[t] = true
			}
			out = append(out, portInfo{p, prettyByID(filepath.Base(p))})
		}
	}
	for _, pat := range portGlobs {
		m, _ := filepath.Glob(pat)
		for _, p := range m {
			base := filepath.Base(p)
			if strings.Contains(base, "Bluetooth") || strings.Contains(base, "debug-console") {
				continue
			}
			if seen[p] {
				continue
			}
			out = append(out, portInfo{p, base})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return append(out, gpioPorts()...)
}

func openBrowser(url string) { exec.Command(browserOpener, url).Start() }

func keyerThreadSetup() {} // LockOSThread + the keyer's final spin is enough here

// prettyByID: "usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_00C2-if01-port0"
// -> "Silicon Labs CP2105 Dual USB to UART Bridge Controller (port 2)".
func prettyByID(n string) string {
	n = strings.TrimPrefix(n, "usb-")
	port := ""
	if i := strings.LastIndex(n, "-if"); i > 0 {
		num := strings.TrimSuffix(n[i+3:], "-port0")
		if v, err := strconv.Atoi(num); err == nil {
			port = " (port " + strconv.Itoa(v+1) + ")"
		}
		n = n[:i]
	}
	parts := strings.Split(n, "_")
	if len(parts) > 1 { // last part is the serial number
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, " ") + port
}
