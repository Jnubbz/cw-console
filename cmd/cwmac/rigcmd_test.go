package main

import (
	"reflect"
	"testing"
)

func TestCATDialects(t *testing.T) {
	y := catSpecFor("/dev/serial/by-id/usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_01AE6E90-if01-port0", "")
	if y.Kenwood || y.Baud != 38400 || y.Port != "/dev/serial/by-id/usb-Silicon_Labs_CP2105_Dual_USB_to_UART_Bridge_Controller_01AE6E90-if00-port0" {
		t.Fatalf("yaesu spec %+v", y)
	}
	// the FT-710's commands are unchanged
	if got := y.tuneCmds(y.dataMode(), 14074000); !reflect.DeepEqual(got, []string{"MD0C;", "FA014074000;", "MD0C;"}) {
		t.Fatal(got)
	}
	if m, hz, err := y.parseState([]string{"MD07;", "FA007050000;"}); m != "7" || hz != 7050000 || err != nil {
		t.Fatal(m, hz, err)
	}
	k := catSpecFor("/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0", "")
	if !k.Kenwood || k.Baud != 115200 || k.Port != "/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0" {
		t.Fatalf("kenwood spec %+v", k)
	}
	if got := k.tuneCmds(k.dataMode(), 14074000); !reflect.DeepEqual(got, []string{"MD2;", "DA1;", "FA00014074000;"}) {
		t.Fatal(got)
	}
	if got := k.tuneCmds(cwModeFor(7050000), 7050000); !reflect.DeepEqual(got, []string{"MD7;", "FA00007050000;"}) {
		t.Fatal(got)
	}
	if m, hz, err := k.parseState([]string{"MD2;", "DA1;", "FA00014074000;"}); m != "2D" || hz != 14074000 || err != nil {
		t.Fatal(m, hz, err)
	}
	if got := k.modeCmds("2"); !reflect.DeepEqual(got, []string{"MD2;", "DA0;"}) {
		t.Fatal(got)
	}
	if k.modeName("2D") != "USB-DATA" || y.modeName("C") != "DATA-U" {
		t.Fatal("names")
	}
	if catSpecFor("/dev/ttyUSB0", "kenwood").Port != "/dev/ttyUSB0" || catSpecFor("/dev/ttyUSB0", "yaesu").Port != "" {
		t.Fatal("explicit type")
	}
}
