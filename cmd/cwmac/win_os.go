//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

func onBattery() bool { return false }

type portInfo struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

func setupInstaller(u *desktopUpdater) {}

func openBrowser(url string) {
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func keyerThreadSetup() {}

// COM-port keying. Opening a port can assert DTR and RTS, which would key the
// radio, so both lines are dropped before anything else. This is the same
// openPTT path the Mac and the Pi use; only the port call is Windows.

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	pCreateFileW        = kernel32.NewProc("CreateFileW")
	pCloseHandle        = kernel32.NewProc("CloseHandle")
	pGetCommState       = kernel32.NewProc("GetCommState")
	pSetCommState       = kernel32.NewProc("SetCommState")
	pEscapeCommFunction = kernel32.NewProc("EscapeCommFunction")
	pRegOpenKeyExW      = advapi32.NewProc("RegOpenKeyExW")
	pRegEnumValueW      = advapi32.NewProc("RegEnumValueW")
	pRegCloseKey        = advapi32.NewProc("RegCloseKey")
)

const (
	winGenericRead  = 0x80000000
	winGenericWrite = 0x40000000
	winOpenExisting = 3
	winInvalid      = ^uintptr(0)
	winEscSetRTS    = 3
	winEscClrRTS    = 4
	winEscSetDTR    = 5
	winEscClrDTR    = 6
	winHkeyLocal    = 0x80000002
	winKeyRead      = 0x20019
)

type winDCB struct {
	Length    uint32
	BaudRate  uint32
	Flags     uint32
	Reserved  uint16
	XonLim    uint16
	XoffLim   uint16
	ByteSize  byte
	Parity    byte
	StopBits  byte
	XonChar   byte
	XoffChar  byte
	ErrorChar byte
	EofChar   byte
	EvtChar   byte
	Reserved1 uint16
}

type winLine struct {
	h      uintptr
	useRTS bool
	port   string
}

func openKeyLine(port string, useRTS bool) (keyLine, error) {
	if port == "" {
		return nil, fmt.Errorf("no key port")
	}
	path := `\\.\` + port
	h, _, err := pCreateFileW.Call(uintptr(unsafe.Pointer(utf16Ptr(path))), winGenericRead|winGenericWrite, 0, 0, winOpenExisting, 0, 0)
	if h == winInvalid {
		return nil, fmt.Errorf("can't open %s: %v (is another program, e.g. WSJT-X, using it?)", port, err)
	}
	s := &winLine{h: h, useRTS: useRTS, port: port}
	s.idle()
	var last error
	for _, baud := range []uint32{9600, 19200, 4800, 2400, 1200} {
		var d winDCB
		d.Length = uint32(unsafe.Sizeof(d))
		if r, _, e := pGetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r == 0 {
			last = e
			break
		}
		d.BaudRate = baud
		d.Flags |= 1
		d.Flags &^= 1 << 1
		d.Flags &^= 1<<2 | 1<<3
		d.Flags &^= 3 << 4
		d.Flags &^= 1 << 6
		d.Flags &^= 1<<8 | 1<<9
		d.Flags &^= 3 << 12
		d.ByteSize, d.Parity, d.StopBits = 8, 0, 0
		if r, _, e := pSetCommState.Call(h, uintptr(unsafe.Pointer(&d))); r != 0 {
			last = nil
			break
		} else {
			last = e
		}
	}
	s.idle()
	if last != nil {
		s.Close()
		return nil, fmt.Errorf("%s opened but won't accept settings: %v", port, last)
	}
	return s, nil
}

func (s *winLine) idle() {
	pEscapeCommFunction.Call(s.h, winEscClrDTR)
	pEscapeCommFunction.Call(s.h, winEscClrRTS)
}

func (s *winLine) Key(down bool) error {
	fn := uintptr(winEscClrDTR)
	switch {
	case s.useRTS && down:
		fn = winEscSetRTS
	case s.useRTS:
		fn = winEscClrRTS
	case down:
		fn = winEscSetDTR
	}
	if r, _, e := pEscapeCommFunction.Call(s.h, fn); r == 0 {
		return fmt.Errorf("%s: keying failed: %v", s.port, e)
	}
	return nil
}

func (s *winLine) Close() {
	if s.h != 0 && s.h != winInvalid {
		s.idle()
		pCloseHandle.Call(s.h)
		s.h = 0
	}
}

func listPorts() []portInfo {
	var key uintptr
	r, _, _ := pRegOpenKeyExW.Call(winHkeyLocal, uintptr(unsafe.Pointer(utf16Ptr(`HARDWARE\DEVICEMAP\SERIALCOMM`))), 0, winKeyRead, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return []portInfo{}
	}
	defer pRegCloseKey.Call(key)
	var out []portInfo
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
		com := syscall.UTF16ToString(data)
		dev := syscall.UTF16ToString(name)
		label := com
		if dev != "" {
			label = com + " — " + dev
		}
		out = append(out, portInfo{Path: com, Label: label})
	}
	sort.Slice(out, func(a, b int) bool {
		return comNum(out[a].Path) < comNum(out[b].Path)
	})
	if out == nil {
		out = []portInfo{}
	}
	return out
}

func comNum(s string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(s), "COM"))
	return n
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}
