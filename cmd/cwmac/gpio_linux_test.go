//go:build linux

package main

import (
	"testing"
	"unsafe"
)

// The structs must match the kernel's (linux/gpio.h) byte for byte.
func TestGPIOStructSizes(t *testing.T) {
	if s := unsafe.Sizeof(gpioLineRequest{}); s != 592 {
		t.Fatalf("gpio_v2_line_request: %d bytes, kernel has 592", s)
	}
	if s := unsafe.Sizeof(gpioLineConfig{}); s != 272 {
		t.Fatalf("gpio_v2_line_config: %d", s)
	}
	if s := unsafe.Sizeof(gpioChipInfo{}); s != 68 {
		t.Fatalf("gpiochip_info: %d", s)
	}
	if gpioV2GetLine != 0xC250B407 || gpioV2SetValues != 0xC010B40F || gpioGetChipInfo != 0x8044B401 {
		t.Fatalf("ioctl numbers %x %x %x", gpioV2GetLine, gpioV2SetValues, gpioGetChipInfo)
	}
	if off := unsafe.Offsetof(gpioLineRequest{}.Fd); off != 588 {
		t.Fatalf("fd offset %d", off)
	}
}
