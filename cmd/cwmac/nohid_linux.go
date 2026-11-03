//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Rig USB sound chips from C-Media (0d8c:*; the FT-710's is 0013, the FX-4CR's 013c)
// have a HID part too. On a HID error,
// Linux's usbhid resets the whole chip (sound included) without saying so, which is what
// killed FT8 audio at key-down. The box doesn't use it: let go of it (udev does the same,
// this catches a chip that was claimed before udev's rule ran).
var hidReleased struct {
	sync.Mutex
	log []string
}

func releaseRigHID(sysUSB string) {
	drv := filepath.Join(sysUSB, "drivers", "usbhid")
	ents, _ := os.ReadDir(drv)
	for _, e := range ents {
		intf := e.Name()
		dev, _, ok := strings.Cut(intf, ":")
		if !ok {
			continue
		}
		v, _ := os.ReadFile(filepath.Join(sysUSB, "devices", dev, "idVendor"))
		p, _ := os.ReadFile(filepath.Join(sysUSB, "devices", dev, "idProduct"))
		_ = p
		if strings.TrimSpace(string(v)) != "0d8c" { // any C-Media chip (FT-710: 0013, FX-4CR: 013c)
			continue
		}
		err := os.WriteFile(filepath.Join(drv, "unbind"), []byte(intf), 0o200)
		msg := fmt.Sprintf("%s released the rig sound chip's HID part %s", time.Now().Format("15:04:05"), intf)
		if err != nil {
			msg += ": " + err.Error()
		}
		fmt.Fprintln(os.Stderr, msg)
		hidReleased.Lock()
		hidReleased.log = append(hidReleased.log, msg)
		hidReleased.Unlock()
	}
}

func watchRigHID() {
	for {
		releaseRigHID("/sys/bus/usb")
		time.Sleep(3 * time.Second)
	}
}

// hidStatus: for the System log.
func hidStatus() string {
	hidReleased.Lock()
	defer hidReleased.Unlock()
	ents, _ := os.ReadDir("/sys/bus/usb/drivers/usbhid")
	var bound []string
	for _, e := range ents {
		if strings.Contains(e.Name(), ":") {
			bound = append(bound, e.Name())
		}
	}
	return fmt.Sprintf("usbhid now has: %v\n%s\n", bound, strings.Join(hidReleased.log, "\n"))
}
