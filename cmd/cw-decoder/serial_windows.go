//go:build windows

package main

// Serial "keyline" CW keying: toggle a COM port's DTR or RTS line with Morse timing.
// The port carries no data. Lessons from Nexus (crates/tempo-audio/src/control_line.rs):
//   - Windows/drivers may ASSERT DTR and RTS when a port opens. On a typical setup that
//     keys the transmitter, so both lines are dropped immediately, before anything else.
//   - Some rigs refuse particular baud rates on their keying port (the FTX-1 rejects 1200),
//     so a ladder of rates is tried. The rate has no effect on keying.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	pRegOpenKeyExW      = advapi32.NewProc("RegOpenKeyExW")
	pRegEnumValueW      = advapi32.NewProc("RegEnumValueW")
	pRegCloseKey        = advapi32.NewProc("RegCloseKey")
	pCreateFileW        = kernel32.NewProc("CreateFileW")
	pCloseHandle        = kernel32.NewProc("CloseHandle")
	pGetCommState       = kernel32.NewProc("GetCommState")
	pSetCommState       = kernel32.NewProc("SetCommState")
	pEscapeCommFunction = kernel32.NewProc("EscapeCommFunction")
	pGetCurrentThread   = kernel32.NewProc("GetCurrentThread")
	pSetThreadPriority  = kernel32.NewProc("SetThreadPriority")
	pTimeBeginPeriod    = winmm.NewProc("timeBeginPeriod")
	pTimeEndPeriod      = winmm.NewProc("timeEndPeriod")
)

const (
	hkeyLocalMachine = 0x80000002
	keyRead          = 0x20019
	genericRead      = 0x80000000
	genericWrite     = 0x40000000
	openExisting     = 3
	invalidHandle    = ^uintptr(0)

	escSETRTS = 3
	escCLRRTS = 4
	escSETDTR = 5
	escCLRDTR = 6

	threadPriorityHighest = 2
)

// DCB (winbase.h). Flags packs the bitfields.
type dcb struct {
	DCBlength  uint32
	BaudRate   uint32
	Flags      uint32
	wReserved  uint16
	XonLim     uint16
	XoffLim    uint16
	ByteSize   byte
	Parity     byte
	StopBits   byte
	XonChar    byte
	XoffChar   byte
	ErrorChar  byte
	EofChar    byte
	EvtChar    byte
	wReserved1 uint16
}

type comPort struct {
	Name     string // "COM5"
	Device   string // "\Device\Silabser0"
	Friendly string // "Silicon Labs Dual CP2105 USB to UART Bridge: Standard COM Port"
}

func (p comPort) Label() string {
	f := p.Friendly
	if i := strings.LastIndex(f, ": "); i >= 0 { // keep the useful tail, e.g. "Standard COM Port"
		f = f[i+2:]
	}
	if f != "" {
		return p.Name + " - " + f
	}
	dev := p.Device
	if i := strings.LastIndex(dev, `\`); i >= 0 {
		dev = dev[i+1:]
	}
	if dev == "" {
		return p.Name
	}
	return p.Name + "  (" + dev + ")"
}

var (
	setupapi                           = syscall.NewLazyDLL("setupapi.dll")
	pSetupDiGetClassDevsW              = setupapi.NewProc("SetupDiGetClassDevsW")
	pSetupDiEnumDeviceInfo             = setupapi.NewProc("SetupDiEnumDeviceInfo")
	pSetupDiGetDeviceRegistryPropertyW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	pSetupDiDestroyDeviceInfoList      = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
)

type guid struct {
	D1     uint32
	D2, D3 uint16
	D4     [8]byte
}

type spDevinfoData struct {
	CbSize    uint32
	ClassGuid guid
	DevInst   uint32
	Reserved  uintptr
}

// friendlyPortNames maps "COM6" -> "Silicon Labs ...: Standard COM Port" via SetupAPI
// (the names Device Manager shows). Best effort: an empty map on any failure.
func friendlyPortNames() map[string]string {
	out := map[string]string{}
	ports := guid{0x4D36E978, 0xE325, 0x11CE, [8]byte{0xBF, 0xC1, 0x08, 0x00, 0x2B, 0xE1, 0x03, 0x18}}
	h, _, _ := pSetupDiGetClassDevsW.Call(uintptr(unsafe.Pointer(&ports)), 0, 0, 0x2 /*DIGCF_PRESENT*/)
	if h == 0 || h == invalidHandle {
		return out
	}
	defer pSetupDiDestroyDeviceInfoList.Call(h)
	for i := uintptr(0); i < 256; i++ {
		var d spDevinfoData
		d.CbSize = uint32(unsafe.Sizeof(d))
		if r, _, _ := pSetupDiEnumDeviceInfo.Call(h, i, uintptr(unsafe.Pointer(&d))); r == 0 {
			break
		}
		buf := make([]uint16, 512)
		var typ, need uint32
		r, _, _ := pSetupDiGetDeviceRegistryPropertyW.Call(h, uintptr(unsafe.Pointer(&d)), 0xC, /*SPDRP_FRIENDLYNAME*/
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&need)))
		if r == 0 {
			continue
		}
		name := syscall.UTF16ToString(buf)
		o, c := strings.LastIndex(name, "(COM"), strings.LastIndex(name, ")")
		if o < 0 || c < o {
			continue
		}
		out[strings.ToUpper(name[o+1:c])] = strings.TrimSpace(name[:o])
	}
	return out
}

// listCOMPorts reads HKLM\HARDWARE\DEVICEMAP\SERIALCOMM (present ports only).
func listCOMPorts() []comPort {
	var key uintptr
	r, _, _ := pRegOpenKeyExW.Call(hkeyLocalMachine,
		uintptr(unsafe.Pointer(u16(`HARDWARE\DEVICEMAP\SERIALCOMM`))), 0, keyRead, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return nil
	}
	defer pRegCloseKey.Call(key)
	var ports []comPort
	for i := uint32(0); ; i++ {
		name := make([]uint16, 256)
		data := make([]uint16, 256)
		nameLen := uint32(len(name))
		dataLen := uint32(len(data) * 2)
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(key, uintptr(i),
			uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nameLen)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dataLen)))
		if r != 0 {
			break
		}
		ports = append(ports, comPort{Name: syscall.UTF16ToString(data), Device: syscall.UTF16ToString(name)})
	}
	sort.Slice(ports, func(a, b int) bool { return comNum(ports[a].Name) < comNum(ports[b].Name) })
	names := friendlyPortNames()
	for i := range ports {
		ports[i].Friendly = names[strings.ToUpper(ports[i].Name)]
	}
	return ports
}

func comNum(s string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(s), "COM"))
	return n
}

// serialLine keys one control line of an open COM port.
type serialLine struct {
	h      uintptr
	useRTS bool
	port   string
}

var baudLadder = []uint32{9600, 19200, 4800, 2400, 1200}

func openSerialLine(port string, useRTS bool) (*serialLine, error) {
	path := `\\.\` + port
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(u16(path))), genericRead|genericWrite, 0, 0, openExisting, 0, 0)
	if h == invalidHandle {
		return nil, fmt.Errorf("can't open %s: %v\n\nIs another program (Nexus, WSJT-X, a logger) using that port?", port, err)
	}
	s := &serialLine{h: h, useRTS: useRTS, port: port}
	s.idle() // FIRST: both lines down, in case the open asserted them
	var lastErr error
	for _, baud := range baudLadder {
		var d dcb
		d.DCBlength = uint32(unsafe.Sizeof(d))
		if r, _, e := pGetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r == 0 {
			lastErr = e
			break
		}
		d.BaudRate = baud
		d.Flags |= 1            // fBinary
		d.Flags &^= 1 << 1      // no parity check
		d.Flags &^= 1<<2 | 1<<3 // no CTS/DSR output flow control
		d.Flags &^= 3 << 4      // fDtrControl = DTR_CONTROL_DISABLE (we drive it)
		d.Flags &^= 1 << 6      // fDsrSensitivity off
		d.Flags &^= 1<<8 | 1<<9 // no XON/XOFF
		d.Flags &^= 3 << 12     // fRtsControl = RTS_CONTROL_DISABLE (we drive it)
		d.ByteSize, d.Parity, d.StopBits = 8, 0, 0
		if r, _, e := pSetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r != 0 {
			lastErr = nil
			break
		} else {
			lastErr = e
		}
	}
	s.idle()
	if lastErr != nil {
		s.Close()
		return nil, fmt.Errorf("%s opened but won't accept settings: %v", port, lastErr)
	}
	return s, nil
}

func (s *serialLine) idle() {
	pEscapeCommFunction.Call(s.h, escCLRDTR)
	pEscapeCommFunction.Call(s.h, escCLRRTS)
}

// Key implements cw.KeyLine.
func (s *serialLine) Key(down bool) error {
	var fn uintptr
	switch {
	case s.useRTS && down:
		fn = escSETRTS
	case s.useRTS:
		fn = escCLRRTS
	case down:
		fn = escSETDTR
	default:
		fn = escCLRDTR
	}
	if r, _, e := pEscapeCommFunction.Call(s.h, fn); r == 0 {
		return fmt.Errorf("%s: keying failed: %v", s.port, e)
	}
	return nil
}

func (s *serialLine) Close() {
	if s.h != 0 && s.h != invalidHandle {
		s.idle()
		pCloseHandle.Call(s.h)
		s.h = 0
	}
}

// keyerThreadSetup runs on the keyer goroutine's locked OS thread: high priority and
// 1 ms timer resolution for clean element timing.
func keyerThreadSetup() {
	t, _, _ := pGetCurrentThread.Call()
	pSetThreadPriority.Call(t, threadPriorityHighest)
	pTimeBeginPeriod.Call(1)
}

func keyerTimersDone() { pTimeEndPeriod.Call(1) }
