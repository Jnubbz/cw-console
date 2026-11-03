//go:build linux

package main

// Keying through the Raspberry Pi's GPIO header, for rigs that can only be keyed at
// the key jack (e.g. the FX-4CR). Wire the pin through ~1 kΩ to an optocoupler's LED
// (e.g. PC817 / 4N25) and its transistor across the key jack, so the Pi and the rig
// stay electrically separate. Uses the kernel's GPIO character device (v2 uAPI).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// GPIO pins offered for keying (BCM numbers) and their header pins.
var gpioKeyPins = []struct{ bcm, header int }{{17, 11}, {27, 13}, {22, 15}}

type gpioLineAttribute struct {
	ID      uint32
	Padding uint32
	Value   uint64 // flags / values / debounce, depending on ID
}

type gpioLineConfigAttribute struct {
	Attr gpioLineAttribute
	Mask uint64
}

type gpioLineConfig struct {
	Flags    uint64
	NumAttrs uint32
	Padding  [5]uint32
	Attrs    [10]gpioLineConfigAttribute
}

type gpioLineRequest struct {
	Offsets         [64]uint32
	Consumer        [32]byte
	Config          gpioLineConfig
	NumLines        uint32
	EventBufferSize uint32
	Padding         [5]uint32
	Fd              int32
}

type gpioLineValues struct {
	Bits, Mask uint64
}

type gpioChipInfo struct {
	Name, Label [32]byte
	Lines       uint32
}

func iowr(nr, size uintptr) uintptr { return 3<<30 | size<<16 | 0xB4<<8 | nr }
func ior(nr, size uintptr) uintptr  { return 2<<30 | size<<16 | 0xB4<<8 | nr }

var (
	gpioGetChipInfo  = ior(0x01, unsafe.Sizeof(gpioChipInfo{}))
	gpioV2GetLine    = iowr(0x07, unsafe.Sizeof(gpioLineRequest{}))
	gpioV2SetValues  = iowr(0x0F, unsafe.Sizeof(gpioLineValues{}))
	gpioFlagOutput   = uint64(1 << 3)
	gpioAttrOutValue = uint32(2)
)

// headerChip finds the GPIO chip behind the 40-pin header (RP1 on a Pi 5, the SoC's
// own on older Pis).
func headerChip() (string, error) {
	chips, _ := filepath.Glob("/dev/gpiochip*")
	best := ""
	for _, c := range chips {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		var info gpioChipInfo
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), gpioGetChipInfo, uintptr(unsafe.Pointer(&info)))
		f.Close()
		if e != 0 {
			continue
		}
		label := strings.TrimRight(string(info.Label[:]), "\x00")
		if label == "pinctrl-rp1" || label == "pinctrl-bcm2711" || label == "pinctrl-bcm2835" {
			return c, nil
		}
		if best == "" && info.Lines >= 28 {
			best = c
		}
	}
	if best == "" {
		return "", errors.New("no GPIO header found")
	}
	return best, nil
}

type gpioLine struct {
	fd  int
	pin int
}

func openGPIOKey(pin int) (keyLine, error) {
	chip, err := headerChip()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(chip, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var req gpioLineRequest
	req.Offsets[0] = uint32(pin)
	req.NumLines = 1
	copy(req.Consumer[:], "cw-decoder key")
	req.Config.Flags = gpioFlagOutput
	req.Config.NumAttrs = 1
	req.Config.Attrs[0] = gpioLineConfigAttribute{Attr: gpioLineAttribute{ID: gpioAttrOutValue, Value: 0}, Mask: 1} // starts key-up
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), gpioV2GetLine, uintptr(unsafe.Pointer(&req))); e != 0 {
		return nil, fmt.Errorf("GPIO %d: %v (is something else using that pin?)", pin, e)
	}
	l := &gpioLine{fd: int(req.Fd), pin: pin}
	if err := l.Key(false); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

func (l *gpioLine) Key(down bool) error {
	v := gpioLineValues{Mask: 1}
	if down {
		v.Bits = 1
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(l.fd), gpioV2SetValues, uintptr(unsafe.Pointer(&v))); e != 0 {
		return fmt.Errorf("GPIO %d: keying failed: %v", l.pin, e)
	}
	return nil
}

func (l *gpioLine) Close() {
	if l.fd >= 0 {
		l.Key(false)
		syscall.Close(l.fd)
		l.fd = -1
	}
}

// gpioPorts lists the header pins as keying "ports" when the board has a GPIO header.
func gpioPorts() []portInfo {
	if _, err := headerChip(); err != nil {
		return nil
	}
	var out []portInfo
	for _, p := range gpioKeyPins {
		out = append(out, portInfo{"gpio:" + strconv.Itoa(p.bcm),
			fmt.Sprintf("GPIO %d (header pin %d) - key jack via optocoupler", p.bcm, p.header)})
	}
	return out
}

func parseGPIOPort(port string) (int, bool) {
	s, ok := strings.CutPrefix(port, "gpio:")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	for _, p := range gpioKeyPins {
		if p.bcm == n {
			return n, true
		}
	}
	return 0, false
}
